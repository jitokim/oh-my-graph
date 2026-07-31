package graph

import (
	"fmt"
	"regexp"
	"strings"
	"time"
)

// GraphValidationError names the offending node and the invariant it broke. It
// is the single error type Load/Parse return for a structurally invalid graph,
// so a caller can render a precise "node X: <why>" message without string
// matching.
type GraphValidationError struct {
	NodeID string
	Reason string
}

func (e *GraphValidationError) Error() string {
	if e.NodeID == "" {
		return fmt.Sprintf("invalid graph: %s", e.Reason)
	}
	return fmt.Sprintf("invalid graph: node %q: %s", e.NodeID, e.Reason)
}

// validTypes and validHandoffs are the closed sets a node's type/handoff may
// take. Kept as maps so membership is a single lookup and the error message can
// list the allowed values.
var (
	validTypes    = map[string]bool{TypeClaudeRun: true, TypeGate: true}
	validHandoffs = map[string]bool{HandoffArtifact: true, HandoffSession: true}
)

// Validate enforces the graph's structural invariants and returns the first
// violation as a *GraphValidationError. The checks run in dependency order so
// that later checks (cycles, handoff) may assume earlier ones (unique ids,
// existing parents) already hold:
//
//  1. every node id is non-empty and unique;
//  2. every type/handoff is a known value;
//  3. every depends_on id refers to a real node;
//  4. the depends_on relation is acyclic (DFS three-colour);
//  5. a session-handoff node has exactly one parent — the session it resumes
//     (a root has no session to resume; more than one can't be merged);
//  6. every success_check.verify is runnable: a command, a parseable timeout
//     within the ceiling, a compilable output_matches regex;
//  7. an agent name, when present, carries no surrounding whitespace;
//  8. a worktree name, when present, is a single safe path element, and the
//     node declares no cwd alongside it.
func (g *Graph) Validate() error {
	if err := g.validateNodesUnique(); err != nil {
		return err
	}
	if err := g.validateEnums(); err != nil {
		return err
	}
	if err := g.validateDependenciesExist(); err != nil {
		return err
	}
	if err := g.validateAcyclic(); err != nil {
		return err
	}
	if err := g.validateHandoffConstraints(); err != nil {
		return err
	}
	if err := g.validateSuccessChecks(); err != nil {
		return err
	}
	if err := g.validateAgentNames(); err != nil {
		return err
	}
	return g.validateWorktrees()
}

// worktreeNamePattern is the shape a worktree name must take: one path
// element, starting with an alphanumeric, using only alphanumerics, '.', '_'
// and '-'. The name becomes both a directory under the run dir and a branch
// segment (omg/<run-id>/<name>), so a path separator, a leading dot (".."
// escapes the managed directory) or whitespace would surface as a filesystem
// or git failure mid-run — exactly the class of error load-time validation
// exists to move earlier.
var worktreeNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validateWorktrees enforces the two invariants a `worktree:` declaration
// must satisfy before the engine will run `git worktree add` on its behalf:
// the name is a single safe path element, and the node does not also declare
// a cwd — the worktree IS the node's directory, so a cwd alongside it could
// only be dead text or a contradiction, and either is worth rejecting.
func (g *Graph) validateWorktrees() error {
	for _, n := range g.Nodes {
		if n.Worktree == "" {
			continue
		}
		if !worktreeNamePattern.MatchString(n.Worktree) {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("worktree name %q must be a single path element: alphanumerics, '.', '_' or '-', starting with an alphanumeric", n.Worktree),
			}
		}
		if n.Cwd != "" {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("node declares both cwd %q and worktree %q — a worktree node runs in its managed checkout, so drop one", n.Cwd, n.Worktree),
			}
		}
	}
	return nil
}

// validateAgentNames rejects an agent name carrying surrounding whitespace —
// both the whitespace-only `agent: " "` and the padded `agent: " reviewer "`.
// Either would reach the argv verbatim and fail the node at run time with a CLI
// error naming no graph at all, which is exactly the failure a load-time check
// exists to move earlier. One rule covers both, since TrimSpace collapses the
// blank case to "".
//
// A name that merely does not exist is NOT rejected: which names resolve
// depends on the user's ~/.claude/agents and the checkout's .claude/agents, so
// it is a property of the machine, not of the graph file this validator is
// reading. Rejecting it would make a graph valid on one machine invalid on
// another.
func (g *Graph) validateAgentNames() error {
	for _, n := range g.Nodes {
		if n.Agent != "" && strings.TrimSpace(n.Agent) != n.Agent {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("agent %q has surrounding whitespace", n.Agent),
			}
		}
	}
	return nil
}

func (g *Graph) validateNodesUnique() error {
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return &GraphValidationError{Reason: "a node has an empty id"}
		}
		if seen[n.ID] {
			return &GraphValidationError{NodeID: n.ID, Reason: "duplicate node id"}
		}
		seen[n.ID] = true
	}
	return nil
}

func (g *Graph) validateEnums() error {
	for _, n := range g.Nodes {
		if !validTypes[n.Type] {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown type %q (want %s or %s)", n.Type, TypeClaudeRun, TypeGate),
			}
		}
		if !validHandoffs[n.Handoff] {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown handoff %q (want %s or %s)", n.Handoff, HandoffArtifact, HandoffSession),
			}
		}
	}
	return nil
}

func (g *Graph) validateDependenciesExist() error {
	for _, n := range g.Nodes {
		for _, parent := range n.DependsOn {
			if _, ok := g.byID[parent]; !ok {
				return &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("depends_on unknown node %q", parent),
				}
			}
			if parent == n.ID {
				return &GraphValidationError{NodeID: n.ID, Reason: "node depends on itself"}
			}
		}
	}
	return nil
}

// three-colour DFS marks for cycle detection: an unvisited node is white, a
// node on the current DFS stack is grey, a fully-explored node is black.
// Reaching a grey node means an edge closes a back-edge — a cycle.
const (
	colourWhite = iota
	colourGrey
	colourBlack
)

func (g *Graph) validateAcyclic() error {
	colour := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		if colour[n.ID] == colourWhite {
			if cycleNode, found := g.visit(n.ID, colour); found {
				return &GraphValidationError{
					NodeID: cycleNode,
					Reason: "dependency cycle detected",
				}
			}
		}
	}
	return nil
}

// visit explores id depth-first, following the depends_on edges. It returns the
// id at which a cycle was closed (a grey node reached again) and found=true, or
// found=false when the subtree rooted at id is acyclic.
func (g *Graph) visit(id string, colour map[string]int) (string, bool) {
	colour[id] = colourGrey
	node := g.byID[id]
	for _, parent := range node.DependsOn {
		switch colour[parent] {
		case colourGrey:
			return parent, true
		case colourWhite:
			if cycleNode, found := g.visit(parent, colour); found {
				return cycleNode, true
			}
		}
	}
	colour[id] = colourBlack
	return "", false
}

// validateSuccessChecks proves every declared verification can actually be run
// before a single node starts. Everything it checks is knowable from the file
// alone, so discovering it mid-run — after paying for the nodes that ran first —
// would be a pure waste of the user's money and time.
//
// It is also where the timeout string becomes a time.Duration: judging it
// against the ceiling requires parsing it, and throwing that result away would
// mean parsing the same string again on the critical path of every attempt.
// Verify is a pointer, so the parsed value reaches the copy of the Node held in
// byID and the one the Scheduler reads.
func (g *Graph) validateSuccessChecks() error {
	for _, n := range g.Nodes {
		verification := n.SuccessCheck.Verify
		if verification == nil {
			continue
		}
		timeout, err := validateVerification(n.ID, verification)
		if err != nil {
			return err
		}
		verification.timeout = timeout
	}
	return nil
}

// validateVerification checks one node's verify block and returns its parsed
// timeout. Every failure names the node, so a graph with twenty nodes says which
// one is wrong.
func validateVerification(nodeID string, v *Verification) (time.Duration, error) {
	if strings.TrimSpace(v.Command) == "" {
		return 0, &GraphValidationError{
			NodeID: nodeID,
			Reason: "success_check.verify needs a command — an evidence check with nothing to run would pass every time",
		}
	}

	timeout, err := time.ParseDuration(v.Timeout)
	if err != nil {
		return 0, &GraphValidationError{
			NodeID: nodeID,
			Reason: fmt.Sprintf("success_check.verify has an invalid timeout %q (want a Go duration like 30s, 2m): %v", v.Timeout, err),
		}
	}
	if timeout <= 0 {
		return 0, &GraphValidationError{
			NodeID: nodeID,
			Reason: fmt.Sprintf("success_check.verify timeout %q must be positive", v.Timeout),
		}
	}
	if timeout > maxVerifyTimeout {
		return 0, &GraphValidationError{
			NodeID: nodeID,
			Reason: fmt.Sprintf("success_check.verify timeout %s exceeds the %s ceiling — a verification runs on the node's critical path; split the work into its own node instead", timeout, maxVerifyTimeout),
		}
	}

	if v.OutputMatches != "" {
		if _, err := regexp.Compile(v.OutputMatches); err != nil {
			return 0, &GraphValidationError{
				NodeID: nodeID,
				Reason: fmt.Sprintf("success_check.verify has an invalid output_matches regex %q: %v", v.OutputMatches, err),
			}
		}
	}
	return timeout, nil
}

func (g *Graph) validateHandoffConstraints() error {
	for _, n := range g.Nodes {
		if n.Handoff == HandoffSession && len(n.DependsOn) != 1 {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf(
					"handoff: session with %d parents — a session-handoff node must resume exactly one parent's session; use handoff: artifact for a root node or for fan-in",
					len(n.DependsOn),
				),
			}
		}
	}
	return nil
}
