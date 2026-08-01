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
	"time"

	"gopkg.in/yaml.v3"
)

// Node type constants. A node is a claude subprocess by default; a gate node
// pauses the run for a human decision — the Scheduler dispatches it to the
// injected gate.GateController, and `oh-my-graph resume` continues the run
// (see internal/gate).
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

// Verification timeout policy for success_check.verify. The default keeps an
// undeclared verification from wedging a node for the runner's full 20 minutes;
// the ceiling is refused at load time, because a verification runs on the
// critical path of the node that declares it and a graph asking for an
// hour-long check is asking for a stalled run it will discover much later.
const (
	defaultVerifyTimeout = 2 * time.Minute
	maxVerifyTimeout     = 10 * time.Minute
)

// Verification is the evidence-grounded predicate: a command the ENGINE runs
// itself, judged on its own exit code and output rather than on anything the
// node said about its own work. It is the only success_check predicate that
// observes state outside the model's narration.
//
// The engine runs Command through the platform shell — `sh -c` on unix,
// `cmd /c` on Windows (see internal/verify's build-tagged shells) — so it is
// arbitrary shell — the same standing as allowed_tools in a hand-written graph
// the user reviewed. Auto-planned graphs may not declare it at all.
type Verification struct {
	// Command is the shell command line to run. Required. It is interpolated
	// like a prompt, so {{ inputs.x }} / {{ artifacts.y }} resolve.
	Command string `yaml:"command" json:"command,omitempty"`
	// Cwd is where the command runs. Empty means "the node's own cwd", so the
	// common case needs no repetition. Interpolated.
	Cwd string `yaml:"cwd" json:"cwd,omitempty"`
	// Timeout is a Go duration string bounding this one command. Empty is
	// normalized to defaultVerifyTimeout; Validate parses it at LOAD time and
	// refuses anything unparseable, non-positive, or over maxVerifyTimeout, so
	// no run ever discovers a malformed duration halfway through.
	Timeout string `yaml:"timeout" json:"timeout,omitempty"`
	// ExpectExit is the exit code that counts as verified. A *int, not an int,
	// so an explicitly declared 0 is distinguishable from "not declared" — nil
	// means the default, which is also 0.
	ExpectExit *int `yaml:"expect_exit" json:"expect_exit,omitempty"`
	// OutputMatches, when non-empty, is a regular expression that must match
	// somewhere in the command's combined stdout+stderr. Compiled at load time
	// by Validate, so a bad regex is a load error, not a mid-run surprise.
	OutputMatches string `yaml:"output_matches" json:"output_matches,omitempty"`

	// timeout is Timeout parsed once, at load, by Validate. Unexported so the
	// parsed form cannot drift from the declared string: callers ask via
	// TimeoutDuration.
	timeout time.Duration
}

// TimeoutDuration returns the parsed Timeout. A Verification that reached the
// Scheduler came from Load/Parse, so this is the value Validate already proved
// parseable and within the ceiling. It falls back to the default for a
// hand-built Verification that never went through validation, so the engine
// cannot end up running an unbounded command.
func (v Verification) TimeoutDuration() time.Duration {
	if v.timeout <= 0 {
		return defaultVerifyTimeout
	}
	return v.timeout
}

// ExpectedExitCode is the exit code this verification counts as success —
// what expect_exit declared, or 0.
func (v Verification) ExpectedExitCode() int {
	if v.ExpectExit == nil {
		return 0
	}
	return *v.ExpectExit
}

// SuccessCheck is the conjunction of predicates a node's outcome must satisfy to
// count as a success — ALL configured predicates must pass. An empty check (no
// field set) means "exit code zero is enough".
//
// The predicates are not equals. exit_zero and verify judge facts the engine
// observed; result_matches judges what the node SAID about itself and is a cheap
// secondary filter, never evidence on its own.
type SuccessCheck struct {
	// ExitZero requires the subprocess to have exited 0.
	ExitZero bool `yaml:"exit_zero" json:"exit_zero,omitempty"`
	// ResultMatches, when non-empty, is a regular expression that must match
	// somewhere in the node's .result text. Empty means "no result predicate".
	// Self-reported: a node passes it by emitting the right words.
	ResultMatches string `yaml:"result_matches" json:"result_matches,omitempty"`
	// Verify, when non-nil, is a command the engine runs and judges itself.
	// A pointer so "absent" and "declared but zero-valued" stay distinguishable.
	Verify *Verification `yaml:"verify" json:"verify,omitempty"`
}

// IsZero reports whether no predicate was configured at all — the caller then
// falls back to the exit-zero-only default. Every field must be tested here: a
// check carrying only a verify would otherwise be read as "empty" and the
// evidence command would never run.
func (c SuccessCheck) IsZero() bool {
	return !c.ExitZero && c.ResultMatches == "" && c.Verify == nil
}

// Retry cause tokens — the closed set of values a node's retry.on may list.
// The Scheduler classifies every failed attempt as exactly one of these (see
// internal/schedule's causeFromRunError / causeFromCheck, which reference
// these constants), and shouldRetry matches that token against retry.on by
// string equality. The set lives here, next to the Retry field it constrains,
// so the validator and the scheduler can never disagree on a spelling.
const (
	CauseNonzeroExit    = "nonzero_exit"
	CauseRunError       = "run_error"
	CauseOutputError    = "output_error"
	CauseBudgetExceeded = "budget_exceeded"
	CauseVerifyFailed   = "verify_failed"
	CauseResultMismatch = "result_mismatch"
)

// Retry is a node's flat re-run policy: up to Max additional attempts when the
// failure cause is listed in On. A retried attempt always starts a fresh claude
// session (never resumes a failed one). Every entry in On must be one of the
// Cause* tokens above — an unknown cause would match nothing and silently mean
// "never retry", so Validate rejects it at load time (validateRetryCauses).
type Retry struct {
	Max int      `yaml:"max" json:"max,omitempty"`
	On  []string `yaml:"on" json:"on,omitempty"`
}

// Node is an immutable value object describing one unit of work in the graph.
// It is pure data — it never runs anything; the Scheduler drives it and the
// NodeRunner executes it.
//
// Every field also carries a `json` tag mirroring its `yaml` tag (same key,
// same casing). YAML decoding never uses these — Parse always goes through
// yaml.Unmarshal — but internal/runstate's resumable snapshot stores the
// normalized graph as re-parseable JSON (Snapshot.Graph), produced by
// json.Marshal(*Graph) at the CLI boundary for a hand-written `run` (auto
// mode already has JSON from the planner and reuses it as-is). Because a
// JSON object is valid YAML, graph.Parse reads that JSON back through the
// exact same decoder, and it only round-trips correctly because the encode
// and decode sides agree on every key name. A field added to Node without a
// matching json tag would still compile and still pass Validate — and would
// then silently vanish from every resumed run, which is why the tag pair is
// part of the field, not an afterthought (see TestNode_JSONRoundTripsThroughParse).
type Node struct {
	ID             string       `yaml:"id" json:"id,omitempty"`
	Type           string       `yaml:"type" json:"type,omitempty"`
	DependsOn      []string     `yaml:"depends_on" json:"depends_on,omitempty"`
	Prompt         string       `yaml:"prompt" json:"prompt,omitempty"`
	Cwd            string       `yaml:"cwd" json:"cwd,omitempty"`
	AllowedTools   []string     `yaml:"allowed_tools" json:"allowed_tools,omitempty"`
	PermissionMode string       `yaml:"permission_mode" json:"permission_mode,omitempty"`
	BudgetUSD      float64      `yaml:"budget_usd" json:"budget_usd,omitempty"`
	Handoff        string       `yaml:"handoff" json:"handoff,omitempty"`
	SuccessCheck   SuccessCheck `yaml:"success_check" json:"success_check,omitempty"`
	Retry          *Retry       `yaml:"retry" json:"retry,omitempty"`
	// Timeout, when non-empty, is a Go duration string bounding this node's
	// whole subprocess run, replacing the runner's 20-minute default — the
	// declaration a legitimately long node makes so the engine's wedge
	// protection stops killing its real work (ADR 0007). Validate parses it at
	// LOAD time and refuses anything unparseable or non-positive. Unlike the
	// verify timeout it has no ceiling: a verification rides on its node's
	// critical path, whereas this IS the critical path, and raising it is the
	// point of declaring it. Empty keeps the runner's default.
	Timeout string `yaml:"timeout" json:"timeout,omitempty"`
	// Agent, when set, names a Claude Code subagent this node runs as —
	// rendered as `claude -p --agent <name>`, which resolves it against the
	// user's OWN ~/.claude/agents or <cwd>/.claude/agents definitions, so the
	// node inherits that subagent's system prompt, tools and model. Empty
	// means plain `claude -p`.
	//
	// Load-time validation only rejects a blank/whitespace-only name: whether
	// a name resolves depends on the machine and the checkout, not on the
	// graph file, so it is not a property this validator can decide. An
	// unresolvable name is a node FAILURE at run time, never a silent fallback
	// to plain claude — see runner.NodeInvocation.Agent.
	//
	// Hand-written graphs only: coordinator.validatePlannedNodes rejects it on
	// a planned node, which would otherwise let an unreviewed plan pick which
	// of the user's subagents (and so which system prompt, tool grant and
	// model) runs the node.
	Agent string `yaml:"agent" json:"agent,omitempty"`
	// Worktree, when set, names the managed git worktree this node runs in
	// instead of the plain invocation cwd. The engine creates the worktree
	// once per unique name per run — `git worktree add` under the run
	// directory, on a fresh branch off the invocation repo's HEAD — and every
	// node sharing the name runs in that same checkout, while nodes with
	// different names get different checkouts and can edit files in parallel
	// without racing each other or the user's own tree (internal/worktree,
	// ADR 0005). Empty means today's behaviour: run in the node's cwd (or the
	// invocation directory).
	//
	// Mutually exclusive with cwd — a worktree node's directory is managed,
	// so a declared cwd would be dead text — and the name must be a single
	// safe path element, since it becomes both a directory under the run dir
	// and a branch segment. Both are load errors (validateWorktrees), never
	// mid-run surprises.
	//
	// Hand-written graphs only: coordinator.validatePlannedNodes rejects it
	// on a planned node — an unreviewed plan must not create checkouts or
	// branches in the user's repository.
	Worktree string `yaml:"worktree" json:"worktree,omitempty"`

	// timeout is Timeout parsed once, at load, by Validate
	// (validateNodeTimeouts). Unexported so the parsed form cannot drift from
	// the declared string: callers ask via TimeoutDuration.
	timeout time.Duration
}

// TimeoutDuration returns the parsed node-level Timeout, or 0 when the node
// declared none — zero means "use the runner's default bound", so a
// hand-built Node that never went through validation still ends up under the
// default, never unbounded.
func (n Node) TimeoutDuration() time.Duration {
	return n.timeout
}

// Graph is the validated DAG: its metadata plus the nodes and a by-id index
// built once at load time so every lookup is O(1).
//
// The yaml tags here are vestigial — Parse decodes through rawGraph, never
// through Graph directly — but the json tags are load-bearing for the same
// reason as Node's: json.Marshal(*Graph) is how a hand-written `run` snapshots
// its graph for `oh-my-graph resume` (see Node's doc comment), and the tags
// must name the same keys rawGraph's yaml tags expect on the way back in.
type Graph struct {
	Name        string   `yaml:"name" json:"name,omitempty"`
	Version     string   `yaml:"version" json:"version,omitempty"`
	Inputs      []string `json:"inputs,omitempty"`
	Concurrency int      `yaml:"concurrency" json:"concurrency,omitempty"`
	Nodes       []Node   `yaml:"nodes" json:"nodes,omitempty"`

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
	g, err := decode(data)
	if err != nil {
		return nil, err
	}
	if err := g.Validate(); err != nil {
		return nil, err
	}
	return g, nil
}

// Lint decodes a graph from raw YAML bytes and returns every structural
// violation found — the full list whose first element is what Parse would
// have returned, so the two can never disagree about which graphs are valid.
// A YAML syntax error is the whole report on its own, since nothing
// structural can be judged about a document that did not decode. An empty
// result means the graph is valid: exactly the graphs Parse accepts.
func Lint(data []byte) []error {
	g, err := decode(data)
	if err != nil {
		return []error{err}
	}
	return g.Issues()
}

// decode unmarshals and normalizes raw YAML into a Graph with its by-id index
// built, without judging validity — the shared front half of Parse (which
// then fail-fasts via Validate) and Lint (which collects every issue). No
// caller outside those two may use it: an unvalidated Graph must never
// escape this package.
func decode(data []byte) (*Graph, error) {
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
	return g, nil
}

// normalizeNodes fills in the terse-YAML defaults so the rest of the engine
// never sees an empty Type, Handoff or verification timeout: a node with no
// explicit type is a claude-run, a node with no explicit handoff hands off by
// artifact, and a verification with no declared timeout gets the default.
func normalizeNodes(nodes []Node) []Node {
	out := make([]Node, len(nodes))
	for i, n := range nodes {
		if n.Type == "" {
			n.Type = TypeClaudeRun
		}
		if n.Handoff == "" {
			n.Handoff = HandoffArtifact
		}
		if verification := n.SuccessCheck.Verify; verification != nil && verification.Timeout == "" {
			verification.Timeout = defaultVerifyTimeout.String()
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
//
// Roots is the "nothing has completed yet" case of ReadyGiven, so it is defined
// in terms of it: there is one place that decides what "ready" means, and a
// fresh run and a resumed run cannot drift apart on that definition.
func (g *Graph) Roots() []string {
	return g.ReadyGiven(nil)
}

// ReadyGiven returns the ids of every not-yet-completed node whose dependencies
// are all present in done — the set that becomes runnable once the nodes in done
// have finished. It is the topology question both a fresh run and a resume ask:
//
//   - Roots() is ReadyGiven(nil): with nothing completed, only nodes that have no
//     dependencies at all are ready.
//   - On resume, done is the set of node ids the earlier leg(s) already completed
//     successfully, and ReadyGiven(done) is exactly the nodes whose parents are
//     all satisfied but which have not themselves run yet — the ready set to seed
//     the second leg with, recomputed rather than persisted so it can never go
//     stale against the graph (see DESIGN.md, "What the snapshot must hold").
//
// A node already in done is never returned (it must not re-run), and the result
// preserves the graph's declared node order so the ready set is deterministic.
// Never nil: no ready node yields an empty slice. A nil or empty done map is
// treated as "nothing completed".
func (g *Graph) ReadyGiven(done map[string]bool) []string {
	ready := make([]string, 0)
	for _, n := range g.Nodes {
		if done[n.ID] {
			continue
		}
		if dependenciesSatisfied(n.DependsOn, done) {
			ready = append(ready, n.ID)
		}
	}
	return ready
}

// dependenciesSatisfied reports whether every parent id is present in done. An
// empty parent list is vacuously satisfied — which is what makes a root ready
// under an empty done map, and thus Roots() a special case of ReadyGiven.
func dependenciesSatisfied(parents []string, done map[string]bool) bool {
	for _, parent := range parents {
		if !done[parent] {
			return false
		}
	}
	return true
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
