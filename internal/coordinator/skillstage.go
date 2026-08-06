// Skill ACTIVATION (ADR 0017): a planned node gets Claude Code's own
// description-driven skill activation, from a plugin directory oh-my-graph
// stages, instead of ADR 0012's plan-time inlining of one name-matched body.
//
// The mechanism is one flag and one tool name, and neither of them is a
// ceiling relaxation:
//
//	--setting-sources ""            layer 1, UNCHANGED — this is the decision
//	--plugin-dir <staged>           NOT a layer: it supplies definitions only
//	--tools ...,Skill               layer 3, the one layer that moves
//	--strict-mcp-config             layer 4, unchanged
//
// Layer 1 stays "" because measurement (g) settled what relaxing it costs: a
// node that DECLARED `Bash(git *)`, run under `--setting-sources user`,
// executed an out-of-scope command, because `--tools` bounds tool NAMES and
// not SCOPES and the user's own standing `Bash(*)` becomes a live allow rule
// alongside ours. `--plugin-dir` is the one source of skill definitions that
// reaches the node without reopening that. Nothing here loads the user's
// CLAUDE.md, hooks, model pin, env block, permission rules or MCP servers.
//
// THE STAGED DIRECTORY IS NOT PROTECTED BY ITS LOCATION, and pretending
// otherwise would be the load-bearing lie of this file. `Write` is in
// plannedToolAllowlist unscoped, a node runs as the same uid as oh-my-graph,
// and there is no path this process can create that such a node cannot write —
// 0700 included, chmod included. The requirement — A NODE MUST NOT BE ABLE TO
// STAGE A SKILL FOR A LATER NODE — is met by LIFETIME instead:
//
//   - the manifest (source path, staged path, SHA-256, per file) is taken at
//     PLAN time by trusted Go code, from the scan, after validation, and is
//     held in memory for the leg;
//   - Materialize runs immediately before EVERY node spawn: it deletes every
//     path the manifest does not name and rewrites every path it does. What a
//     node wrote is gone before the next node reads. That is prevention, not
//     detection — a seal could only halt after the fact;
//   - a SOURCE file that changed or vanished since planning halts the run with
//     the path named, so the corpus cannot change under a leg either.
//
// Two residuals, stated rather than closed: the window between verification
// and the CLI's own read is not closed (that would need the CLI to accept a
// content hash, which it does not), and a node that writes has already
// written — what it cannot do is have a successor read it.
//
// STAGING IS TRUSTED CODE, NEVER THE PLANNER. The planner cannot name a skill,
// a directory or the Skill tool: plannedToolAllowlist is unchanged, so
// validatePlannedNodeTools still refuses a plan that DECLARES `Skill`, and the
// grant is a policy-level act invisible to graph.json (ADR 0017 §2) — the same
// posture as agentmap.go's `agent:` and verifycmd.go's injected verification.
// The durable record of the grant is state.json's tool_policies.
//
// FAILURE IS SILENT NO-ACTIVATION, NEVER AN ERROR, matching scanSkillDirs: an
// unreadable skill tree, a name that is not a safe directory element, a corpus
// past the size bound — each drops out and the run proceeds without
// activation, with the reason printed. Zero-config stays zero-config.
package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// SkillToolName is the CLI's name for the tool that invokes a skill. It enters
// a planned node's policy at LAYER 3 (narrowedToolsFor) and nowhere else:
// plannedToolAllowlist bounds what a plan may DECLARE, which is a different
// question, and measurement (c) settled that layer 2 needs no `Skill` allow
// rule — the skill fired under `dontAsk` with an allow list naming only
// `Bash(git *)` and `permission_denials: []`.
// It is exported because `resume`'s de-escalation has to subtract exactly this
// name from a rehydrated tool set (ADR 0017 §6), and a CLI-side string literal
// for it would be a second source of truth for the one word this whole
// mechanism turns on.
const SkillToolName = "Skill"

// stagedPluginName is the staged plugin's own name. It is oh-my-graph's, not
// the user's, so a collision with one of the user's plugins is not possible
// under layer 1 = "" (no other plugin loads) and is at least attributable if
// anyone ever lifts the agent-mapped exclusion.
const stagedPluginName = "oh-my-graph-staged-skills"

// stagedPluginDirName is the staged directory's name inside a run directory —
// beside state.json, under <OMG_HOME>/runs/<run-id>/. Not the node's cwd (the
// user's checkout, the one directory a node is EXPECTED to write), not TMPDIR
// (reaped out from under a resumable run, and listable by other users on a
// shared machine), and nowhere under ~/.claude (the user's, and oh-my-graph
// does not write there).
const stagedPluginDirName = "skills-plugin"

// stagedManifestSuffix names the manifest sidecar, written beside the staged
// directory rather than inside it so re-materialization — which deletes
// everything the manifest does not name — cannot delete its own manifest.
const stagedManifestSuffix = ".manifest.json"

// maxStagedCorpusBytes bounds the whole staged corpus. A skill's bundled files
// are staged with it (references/ included — they are reachable through the
// CLI's own progressive disclosure, so they are part of the corpus and are
// hashed with it), which is an unbounded tree in principle: a dotfiles
// checkout symlinked in as a skill directory can carry anything. 64 MiB is far
// above any plausible corpus (the measured one is under 2 MiB) and far below
// anything that would make a per-spawn re-materialization slow.
const maxStagedCorpusBytes = 64 << 20

// promptTokensPerStagedSkill is ADR 0017 §4's measured per-skill prompt cost,
// on claude 2.1.223/macOS: a 35-skill corpus added 6,008 prompt tokens to every
// invocation and a 3-skill subset added 409, so the marginal skill is ~172
// tokens and long descriptions dominate. It is used ONLY to print an estimate,
// because the honest number a user needs before approving a plan is "this is
// paid by every node, on every retry and every feedback re-run", and a count
// alone does not say that.
const promptTokensPerStagedSkill = 172

// StagedSkill is one skill of the staged corpus, as the plan printout names
// it. Under inlining the printout could say WHICH skill went into WHICH node;
// under activation the choice happens at run time, inside the model, by
// description, so that is gone permanently (ADR 0017 §7). What replaces it is
// this: because oh-my-graph BUILDS the directory, the corpus offered to the
// nodes is an artifact this process can enumerate exactly, so every staged
// skill is named with its size and hash — strictly more than ADR 0012's
// printout managed, which named only the 7% that matched.
type StagedSkill struct {
	Name        string
	Description string
	// SourceDir is the user's own skill directory this was copied from.
	SourceDir string
	// Bytes and SHA256 describe the SKILL.md itself, not the bundled tree.
	Bytes  int64
	SHA256 string
	// Files is how many files were staged for this skill, SKILL.md included.
	// More than one means bundled material (references/ and the like) came
	// along, which is exactly what inlining could not carry.
	Files int
}

// stagedFile is one manifest row: where the bytes come from, where they go
// relative to the staged directory, and what they hashed to AT PLAN TIME. The
// hash is the whole point — it is what makes "the corpus did not change under
// this run" a checkable claim rather than a hope.
type stagedFile struct {
	Source string `json:"source"`
	Rel    string `json:"rel"`
	SHA256 string `json:"sha256"`
}

// stagedManifest is the sidecar's on-disk shape — a persisted contract read by
// `resume`, so it is a type of its own rather than a dump of SkillStaging.
type stagedManifest struct {
	Plugin string        `json:"plugin"`
	Skills []StagedSkill `json:"skills"`
	Files  []stagedFile  `json:"files"`
}

// SkillStaging is a run's staged skill plugin: the manifest, the directory it
// is bound to, and Materialize. The manifest is taken at plan time and the
// directory is bound when the run id exists, which is why the two are separate
// steps — `auto --plan-only` never binds, and a preview that never ran is not
// a run and gets no run directory.
type SkillStaging struct {
	mu     sync.Mutex
	skills []StagedSkill
	files  []stagedFile
	dir    string
}

// SkillActivation is the plan's disclosure of ADR 0017 §1's decision: whether
// activation is on for this run, over which corpus, on which nodes, and — when
// it is off — why. It is printed with the plan for the same reason agent
// mapping is: a decision trusted code made that the human never saw before
// execution defeats the reason it lives in trusted code.
type SkillActivation struct {
	// Enabled is the per-RUN predicate: a scan that found at least one usable
	// definition and a corpus that staged. A user with no ~/.claude/skills
	// pays nothing for a capability there is nothing to exercise.
	Enabled bool
	// DisabledReason says why activation is off when a scan DID run. Empty
	// when Enabled, and empty when no scan happened at all (the printout has
	// nothing to disclose in that case — SkillScan is nil too).
	DisabledReason string
	// Skills is the staged corpus, sorted by name. Whole, never a subset: any
	// rule good enough to pick "this node needs this skill" at plan time IS a
	// skill selector, and substituting one for the CLI's own description gate
	// would cap activation's recall at the selector's (ADR 0017 §4).
	Skills []StagedSkill
	// NodeIDs are the nodes granted activation, in graph order.
	NodeIDs []string
	// ExcludedNodeIDs are agent-mapped nodes, which are excluded and said so:
	// applyAgentMapping drops their layer 1 to nil, so `--agent` plus a staged
	// plugin plus the user's settings is a different, unmeasured composite.
	// Under nil such a node already sees the user's real skills, so the
	// exclusion costs it little.
	ExcludedNodeIDs []string
	// PluginDir is the staged directory, once bound. Empty for a --plan-only
	// preview, which stages nothing because nothing runs.
	PluginDir string
	// EstimatedPromptTokens is what the corpus adds to EVERY node invocation,
	// including every retry and every feedback re-run — an estimate from
	// ADR 0017 §4's measurement, printed because a per-node tax nobody sees is
	// how ADR 0012's 84%-no-candidate became a cost nobody priced.
	EstimatedPromptTokens int
	// Staging carries the manifest into the run. Nil when activation is off.
	Staging *SkillStaging
}

// newSkillStaging builds the manifest for a whole scanned corpus. Every file
// of every skill directory is hashed, because the bundled tree is part of what
// the node can reach.
//
// A skill whose name is not a safe single path element is dropped rather than
// sanitized: `name:` is a field of a file the scan read, so a name of "../x"
// would otherwise choose where this process writes. A skill whose directory
// cannot be walked is dropped the same way. Both are silent — the corpus is
// smaller, the run still works.
//
// It returns an error only when there is no corpus left to stage or the corpus
// is past maxStagedCorpusBytes; both resolve to no-activation with the reason
// printed, never to a failed plan.
func newSkillStaging(skills map[string]skillDef) (*SkillStaging, error) {
	names := make([]string, 0, len(skills))
	for name := range skills {
		names = append(names, name)
	}
	sort.Strings(names)

	staging := &SkillStaging{}
	total := int64(0)
	for _, name := range names {
		if !safeSkillDirName(name) {
			continue
		}
		def := skills[name]
		files, skillMD, bytes, err := walkSkillDir(def)
		if err != nil {
			continue
		}
		total += bytes
		if total > maxStagedCorpusBytes {
			return nil, fmt.Errorf("staged skill corpus exceeds %d MiB", maxStagedCorpusBytes>>20)
		}
		staging.files = append(staging.files, files...)
		staging.skills = append(staging.skills, StagedSkill{
			Name:        name,
			Description: def.Description,
			SourceDir:   def.dir(),
			Bytes:       skillMD.size,
			SHA256:      skillMD.sum,
			Files:       len(files),
		})
	}
	if len(staging.skills) == 0 {
		return nil, errors.New("no skill directory could be staged")
	}
	return staging, nil
}

// safeSkillDirName is the guard on where staging writes. The name comes from a
// scanned file's frontmatter, so it is data, and it becomes a path element.
// One path element, no separators, no dot-prefix (which would collide with
// .claude-plugin and would hide the result from an `ls`).
func safeSkillDirName(name string) bool {
	if name == "" || strings.HasPrefix(name, ".") {
		return false
	}
	if strings.ContainsRune(name, '/') || strings.ContainsRune(name, os.PathSeparator) {
		return false
	}
	return filepath.Base(name) == name && path.Base(name) == name
}

// fileDigest is one file's measured size and hash.
type fileDigest struct {
	size int64
	sum  string
}

// walkSkillDir hashes every regular file under one skill's directory and
// returns the manifest rows for it, the SKILL.md's own digest, and the total
// bytes. Symlinks are resolved (os.Stat, not the walker's Lstat view) because
// a `~/.claude/skills/<name>` symlinked out to a dotfiles checkout is how
// skills are commonly kept under version control — scanSkillDirs already reads
// them, so the stager must too, or the corpus would list a skill it cannot
// stage. Anything that is not a regular file after resolution, and any file
// over maxSkillFileBytes, is skipped silently.
func walkSkillDir(def skillDef) ([]stagedFile, fileDigest, int64, error) {
	root, err := filepath.EvalSymlinks(def.dir())
	if err != nil {
		return nil, fileDigest{}, 0, err
	}
	var files []stagedFile
	var skillMD fileDigest
	total := int64(0)
	err = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := os.Stat(p)
		if err != nil || !info.Mode().IsRegular() || info.Size() > maxSkillFileBytes {
			return nil
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return nil
		}
		digest, err := digestFile(p)
		if err != nil {
			return nil
		}
		total += digest.size
		if rel == "SKILL.md" {
			skillMD = digest
		}
		files = append(files, stagedFile{
			Source: p,
			Rel:    path.Join("skills", def.Name, filepath.ToSlash(rel)),
			SHA256: digest.sum,
		})
		return nil
	})
	if err != nil {
		return nil, fileDigest{}, 0, err
	}
	if skillMD.sum == "" {
		return nil, fileDigest{}, 0, fmt.Errorf("skill %q: no SKILL.md under %s", def.Name, root)
	}
	return files, skillMD, total, nil
}

// digestFile is one file's size and hex SHA-256, read whole. The files are
// bounded by maxSkillFileBytes, so reading rather than streaming is the
// simpler shape and costs nothing measurable.
func digestFile(p string) (fileDigest, error) {
	raw, err := os.ReadFile(p)
	if err != nil {
		return fileDigest{}, err
	}
	sum := sha256.Sum256(raw)
	return fileDigest{size: int64(len(raw)), sum: hex.EncodeToString(sum[:])}, nil
}

// Skills is the staged corpus, for the printout.
func (s *SkillStaging) Skills() []StagedSkill {
	return append([]StagedSkill(nil), s.skills...)
}

// Dir is the bound staged directory, empty until BindTo.
func (s *SkillStaging) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// BindTo attaches the manifest to a directory, materializes it, and writes the
// manifest sidecar beside it so a resumed leg has something to verify against
// rather than a bare path to trust.
//
// Binding is separate from building because the run id does not exist when the
// plan is made: `auto` mints it after Plan returns, and the goal loop mints one
// per cycle. A --plan-only preview never binds — it never ran, so it is not a
// run and gets no run directory (its printout still names the whole corpus).
func (s *SkillStaging) BindTo(dir string) error {
	s.mu.Lock()
	s.dir = dir
	s.mu.Unlock()
	if err := s.Materialize(); err != nil {
		return err
	}
	return s.writeManifest()
}

// Materialize makes the staged directory equal the manifest, and is the whole
// of the "a node cannot stage a skill for a later node" property. It runs
// immediately before EVERY node spawn (see GuardStaging):
//
//   - every source is re-read and re-hashed. A source that CHANGED since
//     planning halts the run with the path named; one that VANISHED halts too.
//     A run whose instruction corpus changed under it should stop, and silence
//     is not an option: a --plugin-dir pointing at nothing exits 0 with no
//     warning, so absence looks exactly like a model that chose no skill;
//   - every path the manifest does not name is DELETED. This is the direction
//     that matters — the hazard is a node ADDING a skill directory of its own,
//     which overwriting alone would leave in place;
//   - every path the manifest does name is rewritten when its bytes differ.
//
// Rewriting only on difference keeps the common case free of writes while a
// sibling node's claude may be reading the tree. The mutex serializes
// reconciles against each other; it cannot serialize them against the CLI's
// own reads, which is the residual ADR 0017 §5 records rather than closes.
func (s *SkillStaging) Materialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return errors.New("skill staging: no directory bound")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}

	keep := make(map[string]bool, len(s.files)+2)
	keep[filepath.Join(s.dir, ".claude-plugin", "plugin.json")] = true
	for _, f := range s.files {
		raw, err := os.ReadFile(f.Source)
		if err != nil {
			return fmt.Errorf("skill staging: source %s is gone since this run was planned (%v); the corpus this run depends on no longer exists", f.Source, err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("skill staging: source %s changed since this run was planned; a run whose instruction corpus changed under it does not continue", f.Source)
		}
		dst := filepath.Join(s.dir, filepath.FromSlash(f.Rel))
		keep[dst] = true
		if err := writeStagedFile(dst, raw); err != nil {
			return err
		}
	}
	if err := writeStagedFile(filepath.Join(s.dir, ".claude-plugin", "plugin.json"), pluginManifestJSON()); err != nil {
		return err
	}
	return s.pruneTo(keep)
}

// pruneTo deletes every regular file under the staged directory the manifest
// does not name, and every directory left empty by that. A node that wrote a
// skill of its own here loses it before the next node spawns.
func (s *SkillStaging) pruneTo(keep map[string]bool) error {
	var dirs []string
	err := filepath.WalkDir(s.dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != s.dir {
				dirs = append(dirs, p)
			}
			return nil
		}
		if keep[p] {
			return nil
		}
		return os.RemoveAll(p)
	})
	if err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}
	// Deepest first, so a directory emptied by the sweep above is itself
	// removable. os.Remove on a non-empty directory fails and is ignored,
	// which is what keeps a legitimately-populated subtree in place.
	sort.Sort(sort.Reverse(sort.StringSlice(dirs)))
	for _, dir := range dirs {
		_ = os.Remove(dir)
	}
	return nil
}

// writeStagedFile writes one staged file 0600 under a 0700 tree, skipping the
// write when the bytes already match. Permissions are the polite half of the
// answer, never the load-bearing half — see this file's header.
func writeStagedFile(dst string, content []byte) error {
	if existing, err := os.ReadFile(dst); err == nil && string(existing) == string(content) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o700); err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}
	if err := os.WriteFile(dst, content, 0o600); err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}
	return nil
}

// pluginManifestJSON is the .claude-plugin/plugin.json the CLI needs to see a
// directory as a plugin at all — the shape measurement (f) used.
func pluginManifestJSON() []byte {
	raw, _ := json.MarshalIndent(map[string]string{
		"name":        stagedPluginName,
		"version":     "0.0.0",
		"description": "Skill definitions oh-my-graph staged for this run's planned nodes (ADR 0017).",
	}, "", "  ")
	return append(raw, '\n')
}

// manifestPath is the sidecar's path — beside the staged directory, never
// inside it, because Materialize deletes everything inside that the manifest
// does not name and a manifest that deletes itself is a bad manifest.
func manifestPath(dir string) string { return dir + stagedManifestSuffix }

// writeManifest persists the manifest so a resumed leg can re-materialize and
// verify rather than trust the path it read out of state.json.
func (s *SkillStaging) writeManifest() error {
	s.mu.Lock()
	m := stagedManifest{Plugin: stagedPluginName, Skills: s.skills, Files: s.files}
	dir := s.dir
	s.mu.Unlock()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("skill staging: encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}
	if err := os.WriteFile(manifestPath(dir), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("skill staging: %w", err)
	}
	return nil
}

// LoadSkillStaging reads a bound directory's manifest back. It is `resume`'s
// entry point and it deliberately gives no way to proceed without one: a
// resumed leg that cannot find the manifest cannot vouch for the directory,
// and a directory it cannot vouch for is one whose skills may simply be
// absent — silently, with exit 0 (ADR 0017 §6).
func LoadSkillStaging(dir string) (*SkillStaging, error) {
	raw, err := os.ReadFile(manifestPath(dir))
	if err != nil {
		return nil, fmt.Errorf("skill staging: no manifest at %s (%w); this run's staged skill corpus cannot be verified, so it is not re-created", manifestPath(dir), err)
	}
	var m stagedManifest
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("skill staging: manifest at %s is unreadable: %w", manifestPath(dir), err)
	}
	if len(m.Files) == 0 {
		return nil, fmt.Errorf("skill staging: manifest at %s names no files", manifestPath(dir))
	}
	return &SkillStaging{skills: m.Skills, files: m.Files, dir: dir}, nil
}

// GuardStaging wraps a NodeRunner so the staged corpus is re-materialized and
// verified immediately before every spawn. It is a NodeRunner decorator rather
// than a scheduler concern on purpose: the Scheduler's job is topology, and
// "the instruction corpus is what it was when this run was planned" is a
// property of the process the runner is about to start.
//
// A verification failure fails the node with the changed path named, which
// under the scheduler's default halt-on-fail stops the run — the loud outcome
// ADR 0017 §5 requires, rather than a leg that quietly runs on a corpus
// somebody edited.
func GuardStaging(next runner.NodeRunner, staging *SkillStaging) runner.NodeRunner {
	if staging == nil {
		return next
	}
	return &stagingRunner{next: next, staging: staging}
}

type stagingRunner struct {
	next    runner.NodeRunner
	staging *SkillStaging
}

func (r *stagingRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	if err := r.staging.Materialize(); err != nil {
		return runner.NodeOutcome{}, err
	}
	return r.next.Run(ctx, spec)
}

// applySkillActivation is the post-validation mutation step that replaced
// applySkillMapping: ordered after applyAgentMapping's rebuild (so node.Agent
// is authoritative) and before the verify attachment. It adjusts the POLICY
// and never the graph.
//
// It cannot live in toolPolicyFor, and the reason is worth stating because the
// first draft of ADR 0017 put it there: toolPolicyFor is called from
// toolPoliciesByNode BEFORE either mapping runs, and applyAgentMapping
// afterwards sets policy.SettingSources = nil on mapped nodes — so a §1
// implemented there would hand an agent-mapped node `Skill` in Tools PLUS nil
// setting sources, wider than anything ADR 0017 decided. It also could not
// work: toolPolicyFor is a pure function of one graph.Node and cannot see the
// scan, which the predicate requires.
//
// Layers 0, 1, 2, 4 and 5 do not move. Layer 3 is recomputed through
// narrowedToolsFor — the one function that builds that list — rather than
// appended to here, so `Skill` cannot appear in a node's tool set by any route
// this file owns.
func (c *Coordinator) applySkillActivation(plan *Plan) error {
	if c.skillActivationOff || len(c.skillDirs) == 0 {
		return nil
	}
	skills, shadowed := scanSkillDirs(c.skillDirs)
	// Recorded BEFORE the empty-corpus return, because the empty corpus is
	// exactly the case the record exists for: a scan that found nothing must
	// still be able to say where it looked.
	plan.SkillScan = &SkillScan{Dirs: append([]string(nil), c.skillDirs...), Found: len(skills), Shadowed: shadowed}
	if len(skills) == 0 {
		plan.SkillActivation = &SkillActivation{DisabledReason: "the scan found no usable skill definition"}
		return nil
	}

	staging, err := newSkillStaging(skills)
	if err != nil {
		// Silent no-activation, never an error: a corpus that cannot be staged
		// is the same class of fact as a corpus that is not there, and a paid
		// planner call must not be thrown away over it. The reason is printed.
		plan.SkillActivation = &SkillActivation{DisabledReason: err.Error()}
		return nil
	}

	activation := &SkillActivation{
		Enabled:               true,
		Skills:                staging.Skills(),
		EstimatedPromptTokens: len(staging.Skills()) * promptTokensPerStagedSkill,
		Staging:               staging,
	}
	for _, node := range plan.Graph.Nodes {
		policy, ok := plan.ToolPolicies[node.ID]
		if !ok {
			continue
		}
		if node.Agent != "" {
			activation.ExcludedNodeIDs = append(activation.ExcludedNodeIDs, node.ID)
			continue
		}
		policy.Tools = narrowedToolsFor(node, true)
		plan.ToolPolicies[node.ID] = policy
		activation.NodeIDs = append(activation.NodeIDs, node.ID)
	}
	if len(activation.NodeIDs) == 0 {
		// Every node is agent-mapped, so there is nothing to stage for. Say
		// so rather than binding a directory no node will ever be given.
		plan.SkillActivation = &SkillActivation{
			DisabledReason:  "every planned node is agent-mapped, and an agent-mapped node is excluded",
			ExcludedNodeIDs: activation.ExcludedNodeIDs,
		}
		return nil
	}
	plan.SkillActivation = activation
	return nil
}

// BindSkillStaging materializes this plan's staged corpus into the run
// directory and hands every activated node the resulting --plugin-dir. It is
// the caller's job because the run id — and so the run directory — does not
// exist until after the plan is bought.
//
// A staging failure here DOES fail the run, unlike a staging failure at plan
// time: at plan time no-activation is a smaller run, while here the policies
// already carry `Skill` and proceeding would hand nodes a tool with no
// definitions behind it — the silent-absence shape this whole mechanism is
// most exposed to.
func (p *Plan) BindSkillStaging(runDir string) error {
	if p.SkillActivation == nil || !p.SkillActivation.Enabled {
		return nil
	}
	dir := filepath.Join(runDir, stagedPluginDirName)
	if err := p.SkillActivation.Staging.BindTo(dir); err != nil {
		return err
	}
	p.SkillActivation.PluginDir = dir
	for _, id := range p.SkillActivation.NodeIDs {
		policy, ok := p.ToolPolicies[id]
		if !ok {
			continue
		}
		policy.PluginDirs = []string{dir}
		p.ToolPolicies[id] = policy
	}
	return nil
}
