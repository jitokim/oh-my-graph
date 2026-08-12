// Agent STAGING (ADR 0022): a mapped node's agent definition is copied into a
// plugin directory oh-my-graph builds, so `--agent` resolves WITHOUT dropping
// ceiling layer 1. This file is what deletes `policy.SettingSources = nil`.
//
// The argv a mapped node gets changes by exactly two flag pairs, and nothing
// else on it moves:
//
//	--setting-sources ""            layer 1, RESTORED — this is the decision
//	--plugin-dir <staged agents>    NOT a layer: it supplies one definition
//	--agent <name>                  unchanged
//	--tools / --allowedTools / --disallowedTools / --strict-mcp-config
//	                                unchanged, and no `Skill` — the ADR 0017 §9
//	                                exclusion is NOT lifted here (see below)
//
// WHY, in one paragraph. Until this shipped, mapping an agent onto a planned
// node cost the node its whole scope ceiling: `--agent` cannot resolve under
// layer 1 = "" (ADR 0004 E2), so agentmap.go dropped layer 1, and with the
// user's settings back a standing `Bash(*)` matched before the node's own
// declared `Bash(git *)` ever did. That was not an argument, it was measured
// twice — most recently as the reference arm of measurement (k), where the
// SHIPPED mapped argv ran an out-of-scope `touch` under `--permission-mode
// dontAsk` with `permission_denials: []`, 2 of 2. Under this file's argv the
// same node, same machine, same CLI build, minutes apart, was DENIED 3 of 3
// with the refusal named in `permission_denials`, while an in-scope `git init`
// control still ran 2 of 2. `docs/measurements/0017-staged-agent-restores-layer-1.md`.
//
// WHAT ELSE COMES BACK WITH LAYER 1, and it is the larger half. Under `nil` a
// mapped node loaded the user's settings AND the repository's: measurement (j)
// showed a `SKILL.md` committed to the repository under work firing 3 of 3 on
// a prompt that never mentions skills, and a `.claude/settings.json` committed
// to that repository enabling a plugin of its choosing into an unattended
// node. Both stop under `""`: (k) re-ran them and the CLI answered the model's
// own `Skill` call with `Unknown skill: …`, `is_error: true`. So this file is
// a supply-chain fix first and a ceiling fix second.
//
// THE DEFINITION IS PINNED BY HASH, WHICH IS WHY toolsBeyondCeiling STILL
// MEANS SOMETHING. mapAgents refuses any agent whose frontmatter `tools:`
// exceed the node's planned allowed_tools. That check reads the SCANNED source
// file; what the CLI reads is this directory. The two are the same bytes by
// construction — the manifest records the source path and its plan-time
// SHA-256, and Materialize restores from that source only when the hash still
// matches — so an agent file edited between the scan and the spawn cannot
// widen what was checked. Before this file, the CLI re-read the user's live
// `~/.claude/agents` at spawn time and that window was open.
//
// IT IS ITS OWN PLUGIN DIRECTORY, NOT THE SKILL CORPUS'S. Two reasons, in
// order of weight:
//
//   - The ADR 0017 §9 exclusion stays. A mapped node gets no `Skill` in
//     --tools, so handing it the staged skill corpus would charge it ADR 0017
//     §4's per-invocation prompt tax for definitions it cannot invoke.
//   - Skill staging exists only when the user HAS skills (applySkillActivation
//     returns early on an empty scan). Sharing the directory would make a
//     mapped node's ceiling depend on whether the user happens to own a
//     `~/.claude/skills`, which is the invisible coupling ADR 0004 §4 rejects.
//
// The cost of that choice is stated rather than hidden: the shipped directory
// carries `agents/` and no `skills/`, and measurement (k)'s directory carried
// both. ADR 0022 §"What is not measured" is where that gap lives. Its failure
// mode is the loud one — (k)'s K-NEG arm removed `agents/` from an otherwise
// identical directory and the CLI exited 1 with `--agent 'x' not found` — so a
// directory the CLI declined to load fails the node instead of silently
// running it unmapped.
//
// THE STAGED DIRECTORY IS NOT PROTECTED BY ITS LOCATION. Everything
// skillstage.go's header says about that applies here verbatim: a node runs as
// the same uid, `Write` is unscoped, and no path this process creates is one a
// node cannot write. The requirement is met by LIFETIME instead — the manifest
// is taken at plan time by trusted Go code and held in memory, and Materialize
// runs before EVERY spawn, deleting every path the manifest does not name and
// restoring every path whose bytes drifted. A node that plants an agent
// definition of its own here has it gone before the next node reads.
//
// ACROSS LEGS THERE IS NO CLAIM, so a resumed leg stages nothing and maps
// nothing: `resume` drops `agent:` from every planned node and says why
// (cmd/oh-my-graph's dropAgentMapping, ADR 0022 §5). That is the same rule
// ADR 0017 §6 applies to skills, for the same reason — the only record a
// second process could re-stage from lives in the run directory the first
// leg's nodes could write.
package coordinator

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"sort"
	"sync"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// stagedAgentPluginName is the staged agent plugin's own name — oh-my-graph's,
// not the user's. It is separate from stagedPluginName so a run directory's
// two staged plugins are told apart in any transcript that names them.
const stagedAgentPluginName = "oh-my-graph-staged-agents"

// stagedAgentPluginDirName is the staged agent directory's name inside a run
// directory, beside state.json and beside the skill corpus's own directory.
// The same reasoning as stagedPluginDirName picked the location: not the
// node's cwd, not TMPDIR, and nowhere under ~/.claude.
const stagedAgentPluginDirName = "agents-plugin"

// maxAgentDefBytes bounds one staged agent definition. An agent file is a
// frontmatter block and a system prompt; 1 MiB is far past any real one and
// far below anything that would make a per-spawn re-materialization slow. A
// file over it is not staged, and a node that would have been mapped onto it
// runs unmapped instead.
const maxAgentDefBytes = 1 << 20

// StagedAgent is one agent definition this run staged, as the plan printout
// names it: which of the user's files it came from, and what those bytes
// hashed to at plan time. It is the same disclosure StagedSkill makes, for the
// same reason — oh-my-graph BUILDS the directory, so what a mapped node's
// `--agent` will resolve to is an artifact this process can enumerate exactly.
type StagedAgent struct {
	Name string
	// SourcePath is the user's own agent file this was copied from.
	SourcePath string
	Bytes      int64
	SHA256     string
}

// AgentStaging is a run's staged agent plugin: the manifest, the directory it
// is bound to, the nodes it was staged for, and Materialize. Manifest and
// directory are separate steps for the same reason SkillStaging splits them —
// the run id does not exist when the plan is made.
type AgentStaging struct {
	mu      sync.Mutex
	agents  []StagedAgent
	files   []stagedFile
	nodeIDs []string
	dir     string
}

// newAgentStaging builds the manifest for the agent definitions a plan's
// mappings resolved to. defs is keyed by agent name and carries the file each
// was parsed from. It returns the staging for every definition it could hash,
// plus one error per definition it could not — PER AGENT, never all-or-nothing,
// because the unit a user can act on is the agent (`--no-agent <name>`) and one
// oversized file must not cost a plan its other mappings.
//
// A refused definition is not a silent drop the way an unusable skill is. The
// caller turns each into a recorded SKIP on that agent's mappings, so those
// nodes run as ordinary planned nodes and the printout says why: a mapping
// applied without its definition staged would leave a node carrying
// `--agent <name>` under `--setting-sources ""` with nothing to resolve it,
// which the CLI exits 1 on.
//
// staging is nil when nothing could be staged at all.
func newAgentStaging(defs map[string]agentDef) (*AgentStaging, map[string]error) {
	names := make([]string, 0, len(defs))
	for name := range defs {
		names = append(names, name)
	}
	sort.Strings(names)

	staging := &AgentStaging{}
	refused := make(map[string]error)
	for _, name := range names {
		file, agent, err := agentManifestRow(name, defs[name])
		if err != nil {
			refused[name] = err
			continue
		}
		staging.files = append(staging.files, file)
		staging.agents = append(staging.agents, agent)
	}
	if len(staging.agents) == 0 {
		return nil, refused
	}
	return staging, refused
}

// agentManifestRow is one definition's manifest row and its printout record:
// where the bytes come from, where they go inside the staged directory, and
// what they hashed to AT PLAN TIME. The hash is what makes "the CLI resolved
// the definition whose tools were checked" a claim rather than a hope.
func agentManifestRow(name string, def agentDef) (stagedFile, StagedAgent, error) {
	if !safePathElement(name) {
		return stagedFile{}, StagedAgent{}, fmt.Errorf("%q is not a safe file name", name)
	}
	if def.path == "" {
		return stagedFile{}, StagedAgent{}, errors.New("no source file recorded")
	}
	info, err := os.Stat(def.path)
	if err != nil {
		return stagedFile{}, StagedAgent{}, err
	}
	if !info.Mode().IsRegular() {
		return stagedFile{}, StagedAgent{}, fmt.Errorf("%s is not a regular file", def.path)
	}
	if info.Size() > maxAgentDefBytes {
		return stagedFile{}, StagedAgent{}, fmt.Errorf("%s is larger than %d MiB", def.path, maxAgentDefBytes>>20)
	}
	digest, err := digestFile(def.path)
	if err != nil {
		return stagedFile{}, StagedAgent{}, err
	}
	return stagedFile{
			Source: def.path,
			Rel:    path.Join("agents", name+".md"),
			SHA256: digest.sum,
		}, StagedAgent{
			Name:       name,
			SourcePath: def.path,
			Bytes:      digest.size,
			SHA256:     digest.sum,
		}, nil
}

// stageFor records which nodes will be handed this directory. It is separate
// from newAgentStaging because the node list is only final after the caller has
// dropped the mappings whose definition was refused.
func (s *AgentStaging) stageFor(nodeIDs []string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nodeIDs = append([]string(nil), nodeIDs...)
}

// Agents is the staged agent set, for the printout.
func (s *AgentStaging) Agents() []StagedAgent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StagedAgent(nil), s.agents...)
}

// NodeIDs are the mapped nodes this directory was staged for, in graph order.
func (s *AgentStaging) NodeIDs() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]string(nil), s.nodeIDs...)
}

// Dir is the bound staged directory, empty until BindTo.
func (s *AgentStaging) Dir() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dir
}

// BindTo attaches the manifest to a directory, materializes it, and writes the
// manifest sidecar beside it as this run's record of the definition it staged.
func (s *AgentStaging) BindTo(dir string) error {
	s.mu.Lock()
	s.dir = dir
	s.mu.Unlock()
	if err := s.Materialize(); err != nil {
		return err
	}
	return s.writeManifest()
}

// Materialize makes the staged directory equal the manifest, and runs
// immediately before EVERY node spawn (see GuardAgentStaging). It is
// SkillStaging.Materialize's contract, over one file instead of a corpus:
// prune first so a planted symlink at a path component is gone before anything
// is written through it, then restore any manifest-named file whose bytes no
// longer hash to plan time.
//
// A source that changed or vanished while the staged copy needs restoring
// fails the spawn, and here that is sharper than it is for a skill: the planned
// system prompt exists nowhere, and running the node against whatever is there
// instead would be exactly the substitution mapping's ceiling check exists to
// prevent.
func (s *AgentStaging) Materialize() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.dir == "" {
		return errors.New("agent staging: no directory bound")
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return fmt.Errorf("agent staging: %w", err)
	}

	pluginJSON := filepath.Join(s.dir, ".claude-plugin", "plugin.json")
	keep := make(map[string]bool, len(s.files)+1)
	keep[pluginJSON] = true
	for _, f := range s.files {
		keep[filepath.Join(s.dir, filepath.FromSlash(f.Rel))] = true
	}
	if err := pruneStagedDir(s.dir, keep); err != nil {
		return err
	}

	for _, f := range s.files {
		dst := filepath.Join(s.dir, filepath.FromSlash(f.Rel))
		if stagedFileMatches(dst, f.SHA256) {
			continue
		}
		raw, err := os.ReadFile(f.Source)
		if err != nil {
			return fmt.Errorf("agent staging: %s must be restored from %s — the staged copy is missing or no longer the planned bytes — and that source cannot be read (%w); the agent definition this node was mapped onto no longer exists anywhere", f.Rel, f.Source, err)
		}
		sum := sha256.Sum256(raw)
		if hex.EncodeToString(sum[:]) != f.SHA256 {
			return fmt.Errorf("agent staging: %s must be restored from %s — the staged copy is missing or no longer the planned bytes — and that source changed since this run was planned; the agent definition whose tools this run's ceiling check approved no longer exists anywhere", f.Rel, f.Source)
		}
		if err := writeStagedFile(dst, raw); err != nil {
			return err
		}
	}
	return writeStagedFile(pluginJSON, pluginManifestJSON(stagedAgentPluginName,
		"Agent definitions oh-my-graph staged for this run's mapped nodes (ADR 0022)."))
}

// writeManifest persists the manifest as this run's RECORD of the definition it
// staged — the source path and the plan-time hash, beside the staged directory.
// Like the skill sidecar it is a record and not evidence: nothing reads it back
// to decide what a node may load, because it lives where the nodes can write.
func (s *AgentStaging) writeManifest() error {
	s.mu.Lock()
	m := struct {
		Plugin string        `json:"plugin"`
		Agents []StagedAgent `json:"agents"`
		Nodes  []string      `json:"nodes"`
		Files  []stagedFile  `json:"files"`
	}{Plugin: stagedAgentPluginName, Agents: s.agents, Nodes: s.nodeIDs, Files: s.files}
	dir := s.dir
	s.mu.Unlock()
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("agent staging: encode manifest: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(dir), 0o700); err != nil {
		return fmt.Errorf("agent staging: %w", err)
	}
	if err := os.WriteFile(manifestPath(dir), append(raw, '\n'), 0o600); err != nil {
		return fmt.Errorf("agent staging: %w", err)
	}
	return nil
}

// GuardAgentStaging wraps a NodeRunner so the staged agent directory is
// re-materialized and verified immediately before every spawn — the same
// decorator shape, and the same reason, as GuardStaging.
//
// It guards EVERY spawn and not only a mapped node's, because the hazard is
// what an unmapped node wrote: the directory is one a node of this run could
// have planted an agent definition in, and the check that catches it has to run
// between that node and the next one to read.
func GuardAgentStaging(next runner.NodeRunner, staging *AgentStaging) runner.NodeRunner {
	if staging == nil {
		return next
	}
	return &agentStagingRunner{next: next, staging: staging}
}

type agentStagingRunner struct {
	next    runner.NodeRunner
	staging *AgentStaging
}

func (r *agentStagingRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	if err := r.staging.Materialize(); err != nil {
		return runner.NodeOutcome{}, fmt.Errorf("%w\nThis node never ran: the staged agent definition could not be restored before it spawned, so the failure recorded against it is the engine's, not its work. Resume with --no-agent-mapping to continue without the mapped agents", err)
	}
	return r.next.Run(ctx, spec)
}

// BindAgentStaging materializes this plan's staged agent definitions into the
// run directory and hands every mapped node the resulting --plugin-dir. It is
// the caller's job for the same reason BindSkillStaging is: the run id — and so
// the run directory — does not exist until after the plan is bought.
//
// A failure here DOES fail the run. The policies already carry
// `--setting-sources ""` beside their `--agent`, and proceeding would spawn
// nodes whose agent cannot resolve — the CLI exits 1 on that, so the outcome
// would be a paid run of failed nodes rather than a silent one, but failing
// before anything spends is better than failing loudly N times.
func (p *Plan) BindAgentStaging(runDir string) error {
	if p.AgentStaging == nil {
		return nil
	}
	dir := filepath.Join(runDir, stagedAgentPluginDirName)
	if err := p.AgentStaging.BindTo(dir); err != nil {
		return err
	}
	for _, id := range p.AgentStaging.NodeIDs() {
		policy, ok := p.ToolPolicies[id]
		if !ok {
			continue
		}
		policy.PluginDirs = append(policy.PluginDirs, dir)
		p.ToolPolicies[id] = policy
	}
	return nil
}

// DropAgentMapping returns g with every node's `agent:` removed, plus the ids
// it removed one from, in graph order. It is `resume`'s de-escalation and the
// exported half of ADR 0022 §5.
//
// A resumed leg is a second process with no in-memory manifest, so the only
// thing it could re-stage an agent definition from is a record in the run
// directory — which the previous leg's nodes could have written. That is the
// hazard ADR 0017 §6 refuses to guess at for skills, and an agent definition is
// strictly worse to get wrong than a skill: it is the node's SYSTEM PROMPT, and
// it arrives without the model having to choose it.
//
// The de-escalation is to run the node as an ordinary planned node — isolated,
// under its own unchanged tool ceiling, without the user's agent persona. That
// direction only ever REMOVES capability, which is why it is safe to do
// unconditionally; what it costs is a different system prompt from the one the
// first leg's node had, which is why `resume` prints it rather than doing it
// quietly.
//
// It re-parses through graph.Parse for the same reason applyAgentMapping does:
// graph.Graph answers NodeByID from a map of node VALUES built at load, so
// assigning to Nodes[i] alone would leave the change in a slice nothing reads.
func DropAgentMapping(g *graph.Graph) (*graph.Graph, []string, error) {
	var dropped []string
	for i := range g.Nodes {
		if g.Nodes[i].Agent == "" {
			continue
		}
		dropped = append(dropped, g.Nodes[i].ID)
		g.Nodes[i].Agent = ""
	}
	if len(dropped) == 0 {
		return g, nil, nil
	}
	spec, err := json.Marshal(g)
	if err != nil {
		return nil, nil, fmt.Errorf("re-encode unmapped graph: %w", err)
	}
	parsed, err := graph.Parse(spec)
	if err != nil {
		return nil, nil, fmt.Errorf("re-parse unmapped graph: %w", err)
	}
	return parsed, dropped, nil
}
