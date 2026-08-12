// Subagent auto-mapping: after a plan has been validated, the coordinator —
// trusted Go code, never the planner LLM — scans the user's own Claude Code
// agent definitions (~/.claude/agents/*.md and <cwd>/.claude/agents/*.md,
// project winning over user on a name collision), parses their frontmatter,
// and maps planned nodes onto a matching agent by setting the node's `agent:`
// so it runs under `claude --agent <name>`.
//
// The trust posture mirrors the plan-time field rejections: an unreviewed plan
// still may not carry `agent:` (validatePlannedNodeAgent stays intact), because
// letting untrusted planner output name one of the user's subagents would let
// it choose a system prompt, tool grant and model nobody reviewed. The mapping
// happens strictly after validation, from filesystem facts this process read
// itself, and every mapping made is reported on the Plan so the printed
// topology shows it before anything runs.
//
// THE MATCHING RULE, exactly (documented here because it is deliberately
// conservative — no fuzzy scoring, no description matching):
//
//   - Node ids and agent names are tokenized on '-' and '_'.
//   - An agent matches a node when some node-id token equals some agent-name
//     token, or one is a prefix of the other with the prefix at least 4 runes
//     long (so a node id containing "review" maps to an agent named
//     code-reviewer, but "e2e" never pulls in "e2e-anything-else" by accident).
//   - Exactly one agent matching a node is a candidate; zero or two-plus
//     matches mean NO mapping for that node. Ambiguity is silence, not a
//     guess.
//
// TOOL-SAFETY ALIGNMENT: auto mode's tool ceiling still binds. A candidate is
// mapped only when every tool its frontmatter declares is already in the
// node's own planned allowed_tools — the list that becomes the node's --tools
// and --allowedTools. An agent declaring anything wider is SKIPPED, and the
// skip is recorded on the Plan so the printout can say so. An agent whose
// frontmatter declares no tools inherits the node's own (already-ceilinged)
// tool set, so it is mappable.
//
// THE TRADE THAT USED TO BE HERE IS PAID OFF (ADR 0022, 2026-08-12). `--agent`
// cannot resolve under ceiling layer 1 from the USER'S OWN directories —
// `--setting-sources ""` disables discovery of them (DESIGN.md, E2; measurement
// (k)'s K-NEG re-confirms it at claude 2.1.228, where the CLI listed the five
// built-ins it could still see and neither the user's nor the repository's
// agents) — so until this ADR a mapped node dropped layer 1 and loaded the
// user's settings again. That was measured to cost the whole scope ceiling: the
// argv this file produced ran an out-of-scope `touch` under dontAsk with
// `permission_denials: []`, 2 of 2, while the same probe's isolated node denied
// the identical command.
//
// A `--plugin-dir` supplies definitions WITHOUT reopening layer 1, and a plugin
// directory can carry `agents/` as well as `skills/`. So applyAgentMapping now
// stages the matched definition (agentstage.go) and LEAVES LAYER 1 AT "". Under
// that argv the ceiling arm was denied 3 of 3 with the refusal recorded, the
// in-scope control still ran, and `--agent` resolved to the STAGED definition
// 3 of 3 — attributed by a marker token, with the staging removed as the
// control (`docs/measurements/0017-staged-agent-restores-layer-1.md`).
//
// All five ceiling layers now hold on a mapped node exactly as on any other
// planned node, and the user's CLAUDE.md, hooks, MCP servers and standing
// permission grants no longer load for it. That is a REAL LOSS for anyone whose
// mapped nodes leaned on their environment, and it is disclosed in the plan
// printout (cmd/oh-my-graph/main.go, noteAgentMappings) rather than discovered
// later. The escape hatches are unchanged: --no-agent-mapping, or --no-agent
// <name> for one.
//
// TOOL SAFETY IS NOW PINNED, NOT JUST CHECKED. toolsBeyondCeiling reads the
// definition this process SCANNED; what the CLI reads is the staged copy, and
// the two are the same bytes because the manifest carries the source path with
// its plan-time SHA-256 (agentstage.go). Before staging, the CLI re-read the
// user's live file at spawn time and an edit in that window was unchecked.
//
// Filesystem scan failures — missing directories, unreadable files, broken
// frontmatter — are silent no-mapping, never an error: zero-config stays
// zero-config. The directories scanned are the caller's to choose
// (WithAgentDirs; the CLI passes DefaultAgentDirs, tests pass temp dirs), so a
// Coordinator built with neither never touches the filesystem at all. Opt out
// entirely with `--no-agent-mapping` (WithoutAgentMapping), or one agent at a
// time with `--no-agent <name>` (WithoutAgentsNamed).
//
// A declined agent is refused AFTER candidateFor has picked it, not removed
// from the scanned set before matching, and the order is the whole point: an
// opt-out must only ever REMOVE a mapping. Dropping the definition earlier
// would change what ambiguity means — two agents matching a node is silence
// today, and declining one of them would promote the other, so a flag that
// says "not this agent" would have mapped a node that nothing mapped before.
// The refusal is recorded like any other skip, so the printout names the node
// that would have been taken.

package coordinator

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// DefaultAgentDirs is where the CLI has the coordinator scan for agent
// definitions: the user's ~/.claude/agents and the invocation directory's
// .claude/agents, in that order — later wins in scanAgentDirs, so a project
// agent shadows a user agent of the same name, matching Claude Code's own
// precedence. A home or cwd that cannot be resolved just drops out of the
// list (the scan is silent about missing directories anyway).
func DefaultAgentDirs() []string {
	var dirs []string
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, ".claude", "agents"))
	}
	if cwd, err := os.Getwd(); err == nil {
		dirs = append(dirs, filepath.Join(cwd, ".claude", "agents"))
	}
	return dirs
}

// minMatchPrefix is the shortest prefix that counts as a token match on its
// own. Below it, only exact token equality matches — "dev" alone must not
// claim both "developer" and "devops" (and when it textually does, the
// ambiguity rule already yields no mapping).
const minMatchPrefix = 4

// AgentMapping records one auto-mapping decision for the plan printout: a
// mapping made (SkippedReason empty) or a candidate found and refused
// (SkippedReason says why). Nodes with no candidate — including ambiguous
// ones — produce no entry at all.
type AgentMapping struct {
	NodeID string
	Agent  string
	// SkippedReason is non-empty when the candidate was refused (its
	// frontmatter tools exceed the node's planned tool ceiling).
	SkippedReason string
}

// agentDef is one parsed agent definition file: the frontmatter fields the
// mapping reads. Description and Model are parsed for completeness (they are
// what `--agent` will apply at run time) but play no part in matching — the
// rule stays name-only so it stays explainable.
type agentDef struct {
	Name        string     `yaml:"name"`
	Description string     `yaml:"description"`
	Model       string     `yaml:"model"`
	Tools       agentTools `yaml:"tools"`
	// path is the file this was parsed from — not frontmatter, and set by the
	// scan. It is what agentstage.go stages and hashes, so the definition the
	// CLI resolves is the one whose `tools:` toolsBeyondCeiling approved.
	path string
}

// agentTools accepts the two spellings agent frontmatter uses for its tool
// list — a comma-separated scalar ("Read, Grep, Bash") or a YAML sequence —
// and normalizes both to a []string. Any other shape is an error, which the
// scanner treats as broken frontmatter (file silently skipped).
type agentTools []string

func (t *agentTools) UnmarshalYAML(value *yaml.Node) error {
	switch value.Kind {
	case yaml.ScalarNode:
		var joined string
		if err := value.Decode(&joined); err != nil {
			return err
		}
		for _, part := range strings.Split(joined, ",") {
			if part = strings.TrimSpace(part); part != "" {
				*t = append(*t, part)
			}
		}
		return nil
	case yaml.SequenceNode:
		var list []string
		if err := value.Decode(&list); err != nil {
			return err
		}
		for _, part := range list {
			if part = strings.TrimSpace(part); part != "" {
				*t = append(*t, part)
			}
		}
		return nil
	}
	return fmt.Errorf("tools: unsupported YAML shape")
}

// scanAgentDirs reads every *.md agent definition under dirs, in order, later
// directories overwriting earlier ones on a name collision. Every failure —
// a missing directory, an unreadable file, frontmatter that does not parse, a
// blank or whitespace-carrying name — skips just that much and stays silent:
// a broken agent file must not break `auto`.
func scanAgentDirs(dirs []string) map[string]agentDef {
	agents := make(map[string]agentDef)
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".md") {
				continue
			}
			def, ok := parseAgentFile(filepath.Join(dir, entry.Name()))
			if !ok {
				continue
			}
			agents[def.Name] = def
		}
	}
	return agents
}

// parseAgentFile extracts the YAML frontmatter of one agent definition file.
// The format is Claude Code's own: a leading `---` line, YAML fields, a
// closing `---` line, then the agent's system prompt (which the mapping never
// reads — the CLI applies it at run time). ok is false for anything that is
// not that shape.
func parseAgentFile(path string) (agentDef, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return agentDef{}, false
	}
	content := strings.TrimPrefix(string(raw), "\ufeff")
	rest, found := strings.CutPrefix(content, "---\n")
	if !found {
		if rest, found = strings.CutPrefix(content, "---\r\n"); !found {
			return agentDef{}, false
		}
	}
	front, _, found := strings.Cut(rest, "\n---")
	if !found {
		return agentDef{}, false
	}
	var def agentDef
	if err := yaml.Unmarshal([]byte(front), &def); err != nil {
		return agentDef{}, false
	}
	if def.Name == "" || strings.ContainsAny(def.Name, " \t\n") {
		return agentDef{}, false
	}
	def.path = path
	return def, true
}

// declinedReason is the SkippedReason of a candidate refused by
// `--no-agent <name>`. It names the flag because the printout's whole job on
// this line is to let the user tell their own opt-out apart from the tool
// ceiling's refusal.
const declinedReason = "declined by --no-agent"

// mapAgents applies the package's matching rule to every node of a validated
// plan and returns the decisions in node order. It only decides; the caller
// applies the mappings (applyAgentMapping), so this stays a pure function of
// graph × agents × declined that tests can read straight off.
func mapAgents(g *graph.Graph, agents map[string]agentDef, declined map[string]bool) []AgentMapping {
	var mappings []AgentMapping
	for _, node := range g.Nodes {
		def, ok := candidateFor(node.ID, agents)
		if !ok {
			continue
		}
		if declined[strings.ToLower(def.Name)] {
			mappings = append(mappings, AgentMapping{
				NodeID:        node.ID,
				Agent:         def.Name,
				SkippedReason: declinedReason,
			})
			continue
		}
		if wider := toolsBeyondCeiling(def.Tools, node.AllowedTools); len(wider) > 0 {
			mappings = append(mappings, AgentMapping{
				NodeID:        node.ID,
				Agent:         def.Name,
				SkippedReason: fmt.Sprintf("tools exceed ceiling: %s", strings.Join(wider, ", ")),
			})
			continue
		}
		mappings = append(mappings, AgentMapping{NodeID: node.ID, Agent: def.Name})
	}
	return mappings
}

// candidateFor returns the single agent matching nodeID, if there is exactly
// one. Two or more matches are ambiguity, and ambiguity is no mapping.
func candidateFor(nodeID string, agents map[string]agentDef) (agentDef, bool) {
	var found agentDef
	count := 0
	for _, def := range agents {
		if agentMatchesNode(nodeID, def.Name) {
			found = def
			if count++; count > 1 {
				return agentDef{}, false
			}
		}
	}
	return found, count == 1
}

// agentMatchesNode is the name-only token rule documented in the package
// comment.
func agentMatchesNode(nodeID, agentName string) bool {
	for _, nodeToken := range nameTokens(nodeID) {
		for _, agentToken := range nameTokens(agentName) {
			if tokensMatch(nodeToken, agentToken) {
				return true
			}
		}
	}
	return false
}

func nameTokens(name string) []string {
	return strings.FieldsFunc(strings.ToLower(name), func(r rune) bool {
		return r == '-' || r == '_'
	})
}

func tokensMatch(a, b string) bool {
	if a == b {
		return true
	}
	if len(a) >= minMatchPrefix && strings.HasPrefix(b, a) {
		return true
	}
	return len(b) >= minMatchPrefix && strings.HasPrefix(a, b)
}

// toolsBeyondCeiling returns every frontmatter tool not literally present in
// the node's planned allowed_tools — the tools the agent expects that the
// node's ceiling (--tools/--allowedTools) will not provide. Comparison is by
// exact rule string: a frontmatter "Bash" against a planned "Bash(git *)" is
// beyond the ceiling, because the bare name asks for more than the scope the
// plan was validated with. Empty frontmatter tools yield nil: such an agent
// inherits the node's own tool set and cannot exceed it.
func toolsBeyondCeiling(agentTools, nodeTools []string) []string {
	allowed := toSet(nodeTools)
	var wider []string
	for _, tool := range agentTools {
		if !allowed[tool] {
			wider = append(wider, tool)
		}
	}
	return wider
}

// applyAgentMapping is the coordinator's post-validation step: scan, decide,
// stage the definitions the decisions resolved to, and rebuild the plan with
// each mapped node's `agent:` set. The rebuilt graph goes back through
// graph.Parse so the plan's Graph, Spec and saved graph.json stay one artifact
// — a resumed or re-run plan keeps its mappings instead of silently shedding
// them.
//
// NO TOOL POLICY IS TOUCHED HERE ANY MORE, and that deletion is ADR 0022's
// whole edit to this function: the line that read `policy.SettingSources = nil`
// is gone, so a mapped node keeps the layer 1 toolPolicyFor gave it. What
// replaces it is BindAgentStaging adding one --plugin-dir at run-directory
// time.
//
// STAGING FAILURE IS NO MAPPING, NEVER A FAILED PLAN OR A MAPPED NODE WITH
// NOTHING TO RESOLVE — and it is PER AGENT. A definition that cannot be staged
// costs exactly the mappings that named it: those nodes stay ordinary planned
// nodes with the reason recorded on their skip line, and every other mapping
// stands. That is the only safe direction, because mapping without staging
// would spawn `--agent` under `--setting-sources ""` with no definition
// anywhere, which the CLI exits 1 on; and it is the right GRAIN, because the
// agent is the unit a user can act on (`--no-agent <name>`).
func (c *Coordinator) applyAgentMapping(plan *Plan) error {
	if c.agentMappingOff || len(c.agentDirs) == 0 {
		return nil
	}
	agents := scanAgentDirs(c.agentDirs)
	if len(agents) == 0 {
		return nil
	}
	mappings := mapAgents(plan.Graph, agents, c.declinedAgents)
	plan.AgentMappings = mappings

	candidates := make(map[string]agentDef)
	for _, m := range mappings {
		if m.SkippedReason == "" {
			candidates[m.Agent] = agents[m.Agent]
		}
	}
	if len(candidates) == 0 {
		return nil
	}
	staging, refused := newAgentStaging(candidates)

	var nodeIDs []string
	for i, m := range plan.AgentMappings {
		if m.SkippedReason != "" {
			continue
		}
		if err, bad := refused[m.Agent]; bad {
			plan.AgentMappings[i].SkippedReason = fmt.Sprintf("its definition could not be staged, so mapping it would lose this node's ceiling: %v", err)
			continue
		}
		for j := range plan.Graph.Nodes {
			if plan.Graph.Nodes[j].ID == m.NodeID {
				plan.Graph.Nodes[j].Agent = m.Agent
			}
		}
		nodeIDs = append(nodeIDs, m.NodeID)
	}
	if len(nodeIDs) == 0 {
		return nil
	}
	staging.stageFor(nodeIDs)
	plan.AgentStaging = staging

	spec, err := json.Marshal(plan.Graph)
	if err != nil {
		return fmt.Errorf("re-encode mapped plan: %w", err)
	}
	g, err := graph.Parse(spec)
	if err != nil {
		return fmt.Errorf("re-parse mapped plan: %w", err)
	}
	plan.Graph = g
	plan.Spec = spec
	return nil
}
