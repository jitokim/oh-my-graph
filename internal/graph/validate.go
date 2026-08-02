package graph

import (
	"fmt"
	"regexp"
	"slices"
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
	validOnFail   = map[string]bool{OnFailHalt: true, OnFailContinue: true}
)

// Validate enforces the graph's structural invariants and returns the first
// violation as a *GraphValidationError. It is the fail-fast view of Issues —
// `run` needs one precise reason to refuse a graph, while `lint` renders the
// whole list — and defining it as Issues' first element keeps the two views
// incapable of disagreeing about which graphs are valid.
func (g *Graph) Validate() error {
	issues := g.Issues()
	if len(issues) == 0 {
		return nil
	}
	return issues[0]
}

// Issues enforces the graph's structural invariants and returns every
// violation found, each a *GraphValidationError, in check order. One
// graph-level check runs first — on_fail, when declared, must be halt or
// continue (the same closed-set rejection retry.on causes get) — then the
// per-node checks:
//
//  1. every node id is non-empty, unique, and a single safe path element;
//  2. every type/handoff is a known value;
//  3. every depends_on id refers to a real node;
//  4. the depends_on relation is acyclic (DFS three-colour);
//  5. a session-handoff node has exactly one parent — the session it resumes
//     (a root has no session to resume; more than one can't be merged) — and
//     that parent is not a gate, which never records a session at all;
//  6. every success_check.verify is runnable: a command, a parseable timeout
//     within the ceiling, a compilable output_matches regex;
//  7. an agent name, when present, carries no surrounding whitespace;
//  8. a worktree name, when present, is a single safe path element, and the
//     node declares no cwd alongside it;
//  9. every retry.on cause is a known token — a typoed cause would match
//     nothing and silently mean "never retry";
//  10. a node-level timeout, when present, is a parseable, positive Go
//     duration — parsed here, once, so no run ever discovers a malformed
//     duration halfway through;
//  11. every feedback arc has the shape ADR 0010 requires — a
//     proper-ancestor rerun target, a required max >= 1, a side-exit-free
//     body with no gates and in-body session parents, disjoint bodies —
//     and every {{ feedback.<id> }} placeholder sits inside the body of the
//     edge <id> declares (a LOAD error, not an advisory: an out-of-place
//     feedback token would resolve to the empty string silently, forever).
//
// Every check runs even when an earlier one failed, so a graph broken in
// several ways reports all of them at once instead of one per attempt. That
// is safe because each check tolerates the others' violations — a missing
// depends_on id simply contributes no edges to the cycle search — at the
// cost of one overlap: a self-dependency is named by both the edge check and
// the cycle check, and both statements are true.
func (g *Graph) Issues() []error {
	var issues []error
	issues = append(issues, g.validateOnFail()...)
	issues = append(issues, g.validateNodesUnique()...)
	issues = append(issues, g.validateNodeIDs()...)
	issues = append(issues, g.validateEnums()...)
	issues = append(issues, g.validateDependenciesExist()...)
	issues = append(issues, g.validateAcyclic()...)
	issues = append(issues, g.validateHandoffConstraints()...)
	issues = append(issues, g.validateSuccessChecks()...)
	issues = append(issues, g.validateAgentNames()...)
	issues = append(issues, g.validateWorktrees()...)
	issues = append(issues, g.validateRetryCauses()...)
	issues = append(issues, g.validateNodeTimeouts()...)
	issues = append(issues, g.validateFeedback()...)
	issues = append(issues, g.validateFeedbackPlaceholders()...)
	return issues
}

// validateNodeTimeouts parses every node-level `timeout:` at load — the same
// move validateSuccessChecks makes for the verify timeout, minus the ceiling:
// the verify ceiling protects a node's critical path from its own evidence
// check, whereas the node timeout IS the critical path, and a graph declares
// one precisely to raise it (ADR 0007). An undeclared timeout stays zero,
// which the runner reads as "use the default bound".
//
// Node is a VALUE in both Nodes and byID, so unlike the verify timeout (which
// travels behind the Verify pointer) the parsed duration must be written to
// both copies explicitly, or the Scheduler's NodeByID lookup would read a
// node that never got its timeout.
func (g *Graph) validateNodeTimeouts() []error {
	var issues []error
	for i, n := range g.Nodes {
		if n.Timeout == "" {
			continue
		}
		timeout, err := time.ParseDuration(n.Timeout)
		if err != nil {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("invalid timeout %q (want a Go duration like 30m, 1h): %v", n.Timeout, err),
			})
			continue
		}
		if timeout <= 0 {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("timeout %q must be positive", n.Timeout),
			})
			continue
		}
		g.Nodes[i].timeout = timeout
		g.byID[n.ID] = g.Nodes[i]
	}
	return issues
}

// validateOnFail rejects a graph-level on_fail outside the closed policy set —
// the graph analogue of validateRetryCauses. A typo like `on_fail: contnue`
// would otherwise normalize to "not continue" and silently mean today's halt
// behaviour: exactly the quiet mid-run surprise (every lane cancelled by one
// failure) the field exists to prevent. The message names both valid values so
// the fix needs no trip to the docs. decode has already normalized an
// undeclared field to OnFailHalt, so an empty value never reaches this check.
func (g *Graph) validateOnFail() []error {
	if validOnFail[g.OnFail] {
		return nil
	}
	return []error{&GraphValidationError{
		Reason: fmt.Sprintf("unknown on_fail %q (want %s or %s)", g.OnFail, OnFailHalt, OnFailContinue),
	}}
}

// retryCauses is the closed set of failure-cause tokens retry.on may list, in
// the order the error message presents them. A slice rather than a map so the
// message is deterministic; membership is a linear scan over six entries.
var retryCauses = []string{
	CauseNonzeroExit,
	CauseRunError,
	CauseOutputError,
	CauseBudgetExceeded,
	CauseVerifyFailed,
	CauseResultMismatch,
}

// validateRetryCauses rejects any retry.on entry outside the closed cause set.
// The scheduler matches causes by string equality, so an unknown entry — a
// typo like `nonzero-exit` — would never match a real failure and the node
// would silently never retry: exactly the quiet mid-run surprise load-time
// validation exists to move earlier. The message lists every valid token so
// the fix needs no trip to the docs.
func (g *Graph) validateRetryCauses() []error {
	var issues []error
	for _, n := range g.Nodes {
		if n.Retry == nil {
			continue
		}
		for _, cause := range n.Retry.On {
			if !slices.Contains(retryCauses, cause) {
				issues = append(issues, &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("unknown retry.on cause %q (want one of: %s)", cause, strings.Join(retryCauses, ", ")),
				})
			}
		}
	}
	return issues
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
func (g *Graph) validateWorktrees() []error {
	var issues []error
	for _, n := range g.Nodes {
		if n.Worktree == "" {
			continue
		}
		if !worktreeNamePattern.MatchString(n.Worktree) {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("worktree name %q must be a single path element: alphanumerics, '.', '_' or '-', starting with an alphanumeric", n.Worktree),
			})
		}
		if n.Cwd != "" {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("node declares both cwd %q and worktree %q — a worktree node runs in its managed checkout, so drop one", n.Cwd, n.Worktree),
			})
		}
	}
	return issues
}

// nodeIDPattern is the shape a node id must take: one path element, starting
// with an alphanumeric, using only alphanumerics, '.', '_' and '-' — the same
// rule worktreeNamePattern enforces, for the same reason. A node id becomes
// both an artifact filename under the run dir (<node-id>.out) and a URL
// parameter (serve's /api/result), so a path separator or a leading dot
// ("../x" escapes the run directory) would surface as a filesystem escape or
// a broken route mid-run — exactly the class of error load-time validation
// exists to move earlier.
var nodeIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// validateNodeIDs enforces that every node id is a single safe path element.
// An empty id is skipped here — validateNodesUnique already reports it, and
// reporting the same id twice would be noise, not precision.
func (g *Graph) validateNodeIDs() []error {
	var issues []error
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			continue
		}
		if !nodeIDPattern.MatchString(n.ID) {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("node id %q must be a single path element: alphanumerics, '.', '_' or '-', starting with an alphanumeric", n.ID),
			})
		}
	}
	return issues
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
func (g *Graph) validateAgentNames() []error {
	var issues []error
	for _, n := range g.Nodes {
		if n.Agent != "" && strings.TrimSpace(n.Agent) != n.Agent {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("agent %q has surrounding whitespace", n.Agent),
			})
		}
	}
	return issues
}

func (g *Graph) validateNodesUnique() []error {
	var issues []error
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			issues = append(issues, &GraphValidationError{Reason: "a node has an empty id"})
			continue
		}
		if seen[n.ID] {
			issues = append(issues, &GraphValidationError{NodeID: n.ID, Reason: "duplicate node id"})
			continue
		}
		seen[n.ID] = true
	}
	return issues
}

func (g *Graph) validateEnums() []error {
	var issues []error
	for _, n := range g.Nodes {
		if !validTypes[n.Type] {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown type %q (want %s or %s)", n.Type, TypeClaudeRun, TypeGate),
			})
		}
		if !validHandoffs[n.Handoff] {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown handoff %q (want %s or %s)", n.Handoff, HandoffArtifact, HandoffSession),
			})
		}
	}
	return issues
}

func (g *Graph) validateDependenciesExist() []error {
	var issues []error
	for _, n := range g.Nodes {
		for _, parent := range n.DependsOn {
			if _, ok := g.byID[parent]; !ok {
				issues = append(issues, &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("depends_on unknown node %q", parent),
				})
				continue
			}
			if parent == n.ID {
				issues = append(issues, &GraphValidationError{NodeID: n.ID, Reason: "node depends on itself"})
			}
		}
	}
	return issues
}

// three-colour DFS marks for cycle detection: an unvisited node is white, a
// node on the current DFS stack is grey, a fully-explored node is black.
// Reaching a grey node means an edge closes a back-edge — a cycle.
const (
	colourWhite = iota
	colourGrey
	colourBlack
)

// validateAcyclic reports at most one cycle per pass: a found cycle aborts
// the DFS with its stack still grey, so continuing the sweep could reach one
// of those stale grey nodes from another entry point and re-report the same
// back-edge as a second, spurious cycle. One precise report, fix, re-lint.
func (g *Graph) validateAcyclic() []error {
	colour := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		if colour[n.ID] == colourWhite {
			if cycleNode, found := g.visit(n.ID, colour); found {
				return []error{&GraphValidationError{
					NodeID: cycleNode,
					Reason: "dependency cycle detected",
				}}
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
func (g *Graph) validateSuccessChecks() []error {
	var issues []error
	for _, n := range g.Nodes {
		verification := n.SuccessCheck.Verify
		if verification == nil {
			continue
		}
		timeout, err := validateVerification(n.ID, verification)
		if err != nil {
			issues = append(issues, err)
			continue
		}
		verification.timeout = timeout
	}
	return issues
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

// validateHandoffConstraints enforces that a session-handoff node has a
// session to resume: exactly one parent, and that parent a node that will
// actually record a session id. A gate parent fails the second half — a gate
// spawns no subprocess and records no session (see the scheduler's
// recordGateApprove), so the child would validate today and then die mid-run
// at "parent has no recorded session id": exactly the failure load-time
// validation exists to move earlier. A parent id that names no node at all is
// tolerated here — validateDependenciesExist already reports it.
func (g *Graph) validateHandoffConstraints() []error {
	var issues []error
	for _, n := range g.Nodes {
		if n.Handoff != HandoffSession {
			continue
		}
		if len(n.DependsOn) != 1 {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf(
					"handoff: session with %d parents — a session-handoff node must resume exactly one parent's session; use handoff: artifact for a root node or for fan-in",
					len(n.DependsOn),
				),
			})
			continue
		}
		if parent, ok := g.byID[n.DependsOn[0]]; ok && parent.Type == TypeGate {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf(
					"handoff: session with gate parent %q — a gate spawns no subprocess and records no session to resume; use handoff: artifact",
					parent.ID,
				),
			})
		}
	}
	return issues
}
