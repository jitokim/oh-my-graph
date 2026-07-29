// Package graph holds the validated DAG that oh-my-graph executes: the Graph
// aggregate and the Node value object it is composed of, plus the YAML loader.
//
// The graph is the single source of truth for topology. Edges are NOT a
// separate list; each Node carries its own DependsOn, and every downstream
// question ("what are the roots?", "who depends on X?") is answered by walking
// that inline adjacency. Load returns only graphs that have already passed
// Validate, so every consumer downstream (Scheduler, Handoff) may assume the
// invariants in validate.go hold.
package graph

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"
)

// Node type constants. A node is a claude subprocess by default; a gate node is
// schema-reserved for the v1.1 human-pause feature (see internal/gate) and is
// rejected at execution time in v0.1.
const (
	TypeClaudeRun = "claude-run"
	TypeGate      = "gate"
)

// Handoff strategy constants — how a node hands its result to its dependents.
//
//   - HandoffArtifact (default): the engine persists the node's result to a
//     file that dependents read via {{ artifacts.<id> }}. Robust, inspectable,
//     parallel-safe; the only strategy valid for a fan-in (many parents).
//   - HandoffSession: the dependent resumes its single session-parent's claude
//     session via --resume. For tight sequential continuation only; validation
//     forbids it on a node with more than one parent (can't merge sessions).
const (
	HandoffArtifact = "artifact"
	HandoffSession  = "session"
)

// PermissionBypass is the permission mode that lets a node act without
// prompting. It is never a default: the CLI warns loudly when a hand-written
// graph opts in, and auto mode refuses planned nodes that request it. One
// exported constant so the warning and the refusal can never disagree on the
// spelling.
const PermissionBypass = "bypassPermissions"

// SuccessCheck is the predicate a node's outcome must satisfy to count as a
// success. An empty check (both fields zero) means "exit code zero is enough".
type SuccessCheck struct {
	// ExitZero requires the subprocess to have exited 0.
	ExitZero bool `yaml:"exit_zero"`
	// ResultMatches, when non-empty, is a regular expression that must match
	// somewhere in the node's .result text. Empty means "no result predicate".
	ResultMatches string `yaml:"result_matches"`
}

// IsZero reports whether no predicate was configured at all — the caller then
// falls back to the exit-zero-only default.
func (c SuccessCheck) IsZero() bool {
	return !c.ExitZero && c.ResultMatches == ""
}

// Retry is a node's flat re-run policy: up to Max additional attempts when the
// failure cause is listed in On. A retried attempt always starts a fresh claude
// session (never resumes a failed one).
type Retry struct {
	Max int      `yaml:"max"`
	On  []string `yaml:"on"`
}

// Node is an immutable value object describing one unit of work in the graph.
// It is pure data — it never runs anything; the Scheduler drives it and the
// NodeRunner executes it.
type Node struct {
	ID             string       `yaml:"id"`
	Type           string       `yaml:"type"`
	DependsOn      []string     `yaml:"depends_on"`
	Prompt         string       `yaml:"prompt"`
	Cwd            string       `yaml:"cwd"`
	AllowedTools   []string     `yaml:"allowed_tools"`
	PermissionMode string       `yaml:"permission_mode"`
	BudgetUSD      float64      `yaml:"budget_usd"`
	Handoff        string       `yaml:"handoff"`
	SuccessCheck   SuccessCheck `yaml:"success_check"`
	Retry          *Retry       `yaml:"retry"`
}

// Graph is the validated DAG: its metadata plus the nodes and a by-id index
// built once at load time so every lookup is O(1).
type Graph struct {
	Name        string `yaml:"name"`
	Version     string `yaml:"version"`
	Inputs      []string
	Concurrency int    `yaml:"concurrency"`
	Nodes       []Node `yaml:"nodes"`

	// byID indexes Nodes by their ID. Unexported: callers ask via NodeByID so
	// the index can never drift from Nodes.
	byID map[string]Node
}

// rawGraph mirrors Graph for YAML decoding only. Inputs is declared here as a
// distinct field so the "inputs:" key maps cleanly while Graph keeps a plain
// []string the rest of the engine reads.
type rawGraph struct {
	Name        string   `yaml:"name"`
	Version     string   `yaml:"version"`
	Inputs      []string `yaml:"inputs"`
	Concurrency int      `yaml:"concurrency"`
	Nodes       []Node   `yaml:"nodes"`
}

// Load reads a YAML graph file, normalizes it, and returns it only if it passes
// Validate. A returned Graph is therefore always a valid DAG whose handoff
// constraints hold — no caller needs to re-check.
func Load(path string) (*Graph, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read graph file %q: %w", path, err)
	}
	return Parse(data)
}

// Parse decodes and validates a graph from raw YAML bytes. Separated from Load
// so tests can drive it without touching the filesystem.
func Parse(data []byte) (*Graph, error) {
	var raw rawGraph
	if err := yaml.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse graph YAML: %w", err)
	}

	g := &Graph{
		Name:        raw.Name,
		Version:     raw.Version,
		Inputs:      raw.Inputs,
		Concurrency: raw.Concurrency,
		Nodes:       normalizeNodes(raw.Nodes),
	}
	g.byID = make(map[string]Node, len(g.Nodes))
	for _, n := range g.Nodes {
		g.byID[n.ID] = n
	}

	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// normalizeNodes fills in the terse-YAML defaults so the rest of the engine
// never sees an empty Type or Handoff: a node with no explicit type is a
// claude-run, and a node with no explicit handoff hands off by artifact.
func normalizeNodes(nodes []Node) []Node {
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		if n.Type == "" {
			n.Type = TypeClaudeRun
		}
		if n.Handoff == "" {
			n.Handoff = HandoffArtifact
		}
		out[i] = n
	}
	return out
}

// NodeByID returns the node with the given id. found is false when no such node
// exists — callers must not assume presence (the scheduler and handoff both
// look up ids that come from user YAML).
func (g *Graph) NodeByID(id string) (Node, bool) {
	n, ok := g.byID[id]
	return n, ok
}

// Roots returns the ids of every node with no dependencies — the scheduler's
// initial ready set. Never nil: an empty graph yields an empty slice.
func (g *Graph) Roots() []string {
	roots := make([]string, 0)
	for _, n := range g.Nodes {
		if len(n.DependsOn) == 0 {
			roots = append(roots, n.ID)
		}
	}
	return roots
}

// DependentsOf returns the ids of every node that lists id in its DependsOn —
// the nodes whose in-degree drops when id succeeds. Never nil.
func (g *Graph) DependentsOf(id string) []string {
	deps := make([]string, 0)
	for _, n := range g.Nodes {
		for _, parent := range n.DependsOn {
			if parent == id {
				deps = append(deps, n.ID)
				break
			}
		}
	}
	return deps
}
