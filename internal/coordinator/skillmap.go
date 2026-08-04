// Skill auto-mapping: after a plan has been validated — and after subagent
// auto-mapping (agentmap.go) has run and rebuilt the graph — the coordinator,
// trusted Go code, never the planner LLM, scans the user's own Claude Code
// skill definitions (~/.claude/skills/*/SKILL.md; v1 deliberately never scans
// a project directory — ADR 0012, Alternatives) and maps planned nodes onto a
// matching skill by APPENDING the skill's body to the node's prompt inside a
// nonce-fenced, attributed block. Unlike an agent — which the CLI applies at
// spawn time via --agent — a skill is applied by the model, and ADR 0012's
// measurement shows both model-side surfaces (the skills listing and the Skill
// tool) are absent under the planned-node argv, so a reference would be dead
// text; the content itself must ride in the prompt.
//
// THE MATCHING RULE is agentmap.go's rule verbatim (tokenize on '-'/'_',
// exact token or >=4-rune prefix, exactly one candidate or nothing — ambiguity
// is silence, not a guess), reused via the same helpers. Description is parsed
// and carried into the plan printout but plays no part in matching.
//
// TWO REFUSALS, both recorded and printed rather than half-working:
//
//   - An oversize body (raw, or over the cap once neutralization has grown
//     it) is SKIPPED, never truncated — instructions cut mid-way can invert
//     meaning.
//     The cap is an empirical fit against the measured corpus (ADR 0012 §3),
//     not a principle.
//   - An agent-mapped node is never mapped a skill: applyAgentMapping drops
//     ceiling Layer 1 on such a node, and ADR 0012's probe establishes "no
//     skills listing" only under the full ceiling — the composite is
//     unmeasured, so v1 refuses it instead of assuming it.
//
// An inlined body is neutralized ('{{' -> '{ {', repeated until none remains)
// before appending: node.Prompt is a handoff template, and un-neutralized
// skill prose would otherwise become template code — a well-formed
// {{ artifacts.<id> | inline }} inside a body is a file read with no tool and
// no ceiling involvement (ADR 0012 §4). The pass repeats because a single
// non-overlapping replacement lets an odd brace run re-form a live token.
// The fence's own metadata — the skill name and its source path — is
// neutralized the same way, since it too is chosen by the scanned file.
// Templating is not a feature of inlined skill text.
//
// The fence carries a per-plan random nonce because an unfenced delimiter is
// forgeable by the very file it delimits (12 of the 35 measured bodies contain
// a bare '---' line). Each mapping records the source path, the inlined byte
// count and the SHA-256 of the exact inlined text (the neutralized body
// between the fence markers), so the local-file part of the saved prompt stays
// machine-checkably attributable.
//
// Filesystem scan failures — missing directories, unreadable files, broken
// frontmatter, a blank name, an empty body — are silent no-mapping, never an
// error: zero-config stays zero-config. The directories scanned are the
// caller's to choose (WithSkillDirs; the CLI passes DefaultSkillDirs, tests
// pass temp dirs), so a Coordinator built without them never touches the
// filesystem at all. Opt out entirely with `--no-skill-mapping`
// (WithoutSkillMapping). See docs/adr/0012-skill-mapping-is-plan-time-inlining.md.

package coordinator

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// DefaultSkillDirs is where the CLI has the coordinator scan for skill
// definitions: the user's ~/.claude/skills only. The project directory
// (<cwd>/.claude/skills) is deliberately absent — it is 100% of the genuinely
// new injection surface (a cloned repository shipping instructions into
// unattended dontAsk nodes) for 0% measured yield (ADR 0012, Alternatives).
// A home that cannot be resolved just drops out (the scan is silent about
// missing directories anyway).
func DefaultSkillDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude", "skills"))
	}
	return dirs
}

// maxInlinedSkillBytes caps the inlined (neutralized) body size. 16 KiB is an
// empirical fit against the corpus that motivated ADR 0012 (n=35: p50 6.7 KiB,
// p90 17.0 KiB; 30 of 35 fit, and the 86.6 KiB outlier stays excluded) — the
// number must be recalibrated if the corpus changes character, not defended.
const maxInlinedSkillBytes = 16 * 1024

// maxSkillFileBytes bounds the raw SKILL.md read itself — the scan's only
// otherwise-unbounded cost. It sits well above maxInlinedSkillBytes so an
// oversize-but-plausible body still earns its printed skip (ADR 0012 §3's
// 86.6 KiB outlier included); only a pathological file is refused here, and
// silently, like any other file the scan cannot use.
const maxSkillFileBytes = 1 << 20

// SkillMapping records one skill auto-mapping decision for the plan printout:
// a mapping made (SkippedReason empty; InlinedBytes and SHA256 describe the
// exact text appended) or a candidate found and refused (SkippedReason says
// why). Nodes with no candidate — including ambiguous ones — produce no entry
// at all.
type SkillMapping struct {
	NodeID string
	Skill  string
	// Description is the skill's frontmatter description, carried so the
	// printout shows WHAT got mapped, not just its name. It plays no part in
	// matching.
	Description string
	// SourcePath is the SKILL.md file the body was read from.
	SourcePath string
	// InlinedBytes is the byte count of the inlined text (the neutralized
	// body between the fence markers). Zero for a skipped candidate.
	InlinedBytes int
	// SHA256 is the hex SHA-256 of that exact inlined text — the integrity
	// link between the printed plan and the saved graph.json. Empty for a
	// skipped candidate.
	SHA256 string
	// SkippedReason is non-empty when the candidate was refused (its body
	// exceeds maxInlinedSkillBytes, or the node is agent-mapped).
	SkippedReason string
}

// skillDef is one parsed SKILL.md: the frontmatter fields the mapping reads
// plus the body it inlines. Description is parsed for the printout but plays
// no part in matching — the rule stays name-only so it stays explainable.
type skillDef struct {
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// body is the file's content below its frontmatter — the text that gets
	// neutralized and inlined. Never part of the YAML.
	body string
	// path is where the file was read from, recorded on the mapping.
	path string
}

// scanSkillDirs reads every <dir>/*/SKILL.md under dirs, in order, later
// directories overwriting earlier ones on a name collision (mirroring
// scanAgentDirs; DefaultSkillDirs passes one directory, but the precedence
// shape is kept so tests and a future measured project scan need no new
// mechanism). Every failure — a missing directory, an unreadable file,
// frontmatter that does not parse, a blank or whitespace-carrying name, an
// empty body — skips just that much and stays silent: a broken skill file
// must not break `auto`.
//
// Directory-ness is decided by os.Stat, not entry.IsDir(): os.ReadDir does not
// follow symlinks, so a `~/.claude/skills/<name>` symlinked out to a dotfiles
// checkout — how skills are commonly kept under version control — would
// otherwise be invisible here while the equivalent symlinked agent .md already
// scans fine (scanAgentDirs filters on suffix, not on IsDir).
func scanSkillDirs(dirs []string) map[string]skillDef {
	skills := make(map[string]skillDef)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			info, err := os.Stat(filepath.Join(dir, entry.Name()))
			if err != nil || !info.IsDir() {
				continue
			}
			def, ok := parseSkillFile(filepath.Join(dir, entry.Name(), "SKILL.md"))
			if !ok {
				continue
			}
			skills[def.Name] = def
		}
	}
	return skills
}

// parseSkillFile extracts the YAML frontmatter and the body of one SKILL.md.
// The format is Claude Code's own: a leading `---` line, YAML fields, a
// closing `---` line, then the skill's instructions — which, unlike an agent's
// system prompt, the mapping DOES read: the body is what gets inlined. ok is
// false for anything that is not that shape, including an empty body (there
// would be nothing to inline) and a file over maxSkillFileBytes (nothing that
// large could ever inline, so it is not worth holding in memory).
func parseSkillFile(path string) (skillDef, bool) {
	f, err := os.Open(path)
	if err != nil {
		return skillDef{}, false
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxSkillFileBytes+1))
	if err != nil || len(raw) > maxSkillFileBytes {
		return skillDef{}, false
	}
	content := strings.TrimPrefix(string(raw), "\ufeff")
	rest, found := strings.CutPrefix(content, "---\n")
	if !found {
		if rest, found = strings.CutPrefix(content, "---\r\n"); !found {
			return skillDef{}, false
		}
	}
	front, body, found := strings.Cut(rest, "\n---")
	if !found {
		return skillDef{}, false
	}
	var def skillDef
	if err := yaml.Unmarshal([]byte(front), &def); err != nil {
		return skillDef{}, false
	}
	if def.Name == "" || strings.ContainsAny(def.Name, " \t\n") {
		return skillDef{}, false
	}
	def.body = strings.TrimSpace(body)
	if def.body == "" {
		return skillDef{}, false
	}
	def.path = path
	return def, true
}

// mapSkills applies the matching rule to every node of a validated plan and
// returns the decisions in node order. It only decides; the caller applies the
// mappings (applySkillMapping), so this stays a pure function of graph ×
// skills that tests can read straight off. It runs on the graph
// applyAgentMapping already rebuilt, so node.Agent is the authoritative
// signal for the agent-mapped skip.
func mapSkills(g *graph.Graph, skills map[string]skillDef) []SkillMapping {
	var mappings []SkillMapping
	for _, node := range g.Nodes {
		def, ok := skillCandidateFor(node.ID, skills)
		if !ok {
			continue
		}
		if node.Agent != "" {
			mappings = append(mappings, SkillMapping{
				NodeID:        node.ID,
				Skill:         def.Name,
				Description:   def.Description,
				SourcePath:    def.path,
				SkippedReason: "node is agent-mapped (composite with dropped Layer 1 unmeasured)",
			})
			continue
		}
		// The raw body is checked BEFORE neutralization: neutralization only
		// grows text ('{{' -> '{ {'), so a body already over the cap would be
		// refused anyway — refusing it on its own size skips the repeated
		// neutralization passes an oversize brace-heavy file would cost.
		size := len(def.body)
		inlined := ""
		if size <= maxInlinedSkillBytes {
			inlined = inlinedSkillText(def)
			size = len(inlined)
		}
		if size > maxInlinedSkillBytes {
			mappings = append(mappings, SkillMapping{
				NodeID:        node.ID,
				Skill:         def.Name,
				Description:   def.Description,
				SourcePath:    def.path,
				SkippedReason: fmt.Sprintf("body %.1f KiB exceeds %d KiB cap", float64(size)/1024, maxInlinedSkillBytes/1024),
			})
			continue
		}
		sum := sha256.Sum256([]byte(inlined))
		mappings = append(mappings, SkillMapping{
			NodeID:       node.ID,
			Skill:        def.Name,
			Description:  def.Description,
			SourcePath:   def.path,
			InlinedBytes: len(inlined),
			SHA256:       hex.EncodeToString(sum[:]),
		})
	}
	return mappings
}

// skillCandidateFor returns the single skill matching nodeID, if there is
// exactly one — agentmap.go's candidate rule over the skill set. Two or more
// matches are ambiguity, and ambiguity is no mapping.
func skillCandidateFor(nodeID string, skills map[string]skillDef) (skillDef, bool) {
	var found skillDef
	count := 0
	for _, def := range skills {
		if agentMatchesNode(nodeID, def.Name) {
			found = def
			if count++; count > 1 {
				return skillDef{}, false
			}
		}
	}
	return found, count == 1
}

// inlinedSkillText is the exact text that goes between the fence markers: the
// skill's body neutralized until no '{{' remains. A single non-overlapping
// ReplaceAll is not enough — an odd brace run re-forms a live token ('{{{'
// becomes '{ {{') — so the pass repeats until the output is free of '{{',
// which is the one property placeholderPattern needs to never match. Both the
// deciding pass (size cap, hash) and the applying pass call this, so what was
// measured is what lands.
func inlinedSkillText(def skillDef) string {
	return neutralizeBraces(def.body)
}

// neutralizeBraces is that repeated pass, over any string a skill file gets to
// choose. Everything the fence renders comes from the file — body, frontmatter
// name, and the path (a directory name the user's skills tree supplies) — so
// all three go through here rather than the body alone.
func neutralizeBraces(s string) string {
	for strings.Contains(s, "{{") { // terminates: each pass shortens every brace run
		s = strings.ReplaceAll(s, "{{", "{ {")
	}
	return s
}

// fencedSkillBlock renders the attributed block appended to a mapped node's
// prompt. The nonce appears in both markers so the fenced text — which the
// fence's author does not control — cannot forge a matching delimiter. The
// metadata is neutralized like the body: a skill named or stored under a path
// holding '{{' would otherwise put a live placeholder into node.Prompt from
// OUTSIDE the fenced text, which the body's neutralization does not cover
// (review round 4).
func fencedSkillBlock(def skillDef, inlined, nonce string) string {
	return fmt.Sprintf(
		"\n\n--- skill: %s %s (mapped by oh-my-graph from %s) ---\n%s\n--- end skill: %s %s ---\n",
		neutralizeBraces(def.Name), nonce, neutralizeBraces(def.path), inlined,
		neutralizeBraces(def.Name), nonce,
	)
}

// applySkillMapping is the coordinator's second post-validation step, strictly
// after applyAgentMapping's rebuild: scan, decide, append each mapped node's
// fenced block, and rebuild the plan the same way — through json.Marshal and
// graph.Parse — so the plan's Graph, Spec and saved graph.json stay one
// artifact. `run` and `resume` execute that saved artifact and never invoke
// mapping, so an already-inlined graph is never inlined twice; the recorded
// hashes make any accidental second pass detectable rather than silent.
func (c *Coordinator) applySkillMapping(plan *Plan) error {
	if c.skillMappingOff || len(c.skillDirs) == 0 {
		return nil
	}
	skills := scanSkillDirs(c.skillDirs)
	if len(skills) == 0 {
		return nil
	}
	mappings := mapSkills(plan.Graph, skills)
	plan.SkillMappings = mappings

	applied := false
	nonce := ""
	for _, m := range mappings {
		if m.SkippedReason != "" {
			continue
		}
		if nonce == "" {
			minted, err := fenceNonce("skill")
			if err != nil {
				return err
			}
			nonce = minted
		}
		def := skills[m.Skill]
		for i := range plan.Graph.Nodes {
			if plan.Graph.Nodes[i].ID == m.NodeID {
				plan.Graph.Nodes[i].Prompt += fencedSkillBlock(def, inlinedSkillText(def), nonce)
			}
		}
		applied = true
	}
	if !applied {
		return nil
	}

	spec, err := json.Marshal(plan.Graph)
	if err != nil {
		return fmt.Errorf("re-encode skill-mapped plan: %w", err)
	}
	g, err := graph.Parse(spec)
	if err != nil {
		return fmt.Errorf("re-parse skill-mapped plan: %w", err)
	}
	plan.Graph = g
	plan.Spec = spec
	return nil
}
