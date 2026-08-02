package schedule

import (
	"errors"
	"fmt"
	"strings"
)

// NodeCheckError is a node that ran but failed its success_check. It names the
// node and the predicate (exit_zero / result_matches / verify) that did not
// hold, so the ledger and the halt message can be precise about why. For a
// failed verification the Detail carries the command, its exit code and a
// truncated tail of its output — the difference between "the evidence check
// failed" and knowing what the evidence actually said.
type NodeCheckError struct {
	NodeID    string
	Predicate string
	Detail    string
}

func (e *NodeCheckError) Error() string {
	return fmt.Sprintf("node %q failed success_check %s: %s", e.NodeID, e.Predicate, e.Detail)
}

// NodeBudgetError is a node that ran and passed its success_check but whose
// actual cost exceeded the budget_usd it declared. It is deliberately a
// *post-hoc* verdict: total_cost_usd only exists in the JSON envelope the
// subprocess prints as it exits, so by the time this error is built the money is
// already spent. What the verdict buys is everything downstream — the node's
// dependents never start, and by default the run halts rather than spending more
// on top of an already-blown budget.
type NodeBudgetError struct {
	NodeID    string
	BudgetUSD float64
	ActualUSD float64
}

func (e *NodeBudgetError) Error() string {
	return fmt.Sprintf("node %q exceeded budget_usd: $%.4f actual vs $%.4f budgeted (over by $%.4f)",
		e.NodeID, e.ActualUSD, e.BudgetUSD, e.ActualUSD-e.BudgetUSD)
}

// HaltError is the run-level error returned in halt-on-fail mode: it names the
// node whose failure stopped the run and wraps the underlying cause (a
// NodeCheckError, a NodeBudgetError, a runner error, or a gate refusal).
type HaltError struct {
	NodeID string
	Err    error
}

func (e *HaltError) Error() string {
	return fmt.Sprintf("run halted at node %q: %v", e.NodeID, e.Err)
}

func (e *HaltError) Unwrap() error { return e.Err }

// RunFailedError is the run-level error returned in continue-on-fail mode: the
// run finished its independent branches, but these nodes (and their pruned
// subtrees) failed.
type RunFailedError struct {
	FailedNodes []string
}

func (e *RunFailedError) Error() string {
	return fmt.Sprintf("run completed with failed node(s): %s", strings.Join(e.FailedNodes, ", "))
}

// PausedError is Run's result when a gate decision paused the run: in-flight
// siblings were drained (not cancelled) and the snapshot was persisted, so
// `oh-my-graph resume <run-id> (--approve|--reject) <gate-id>` can continue
// it. cmd/oh-my-graph maps this to exit code 2 — a pause is not a failure and
// must never be reported or logged as one (ADR 0003).
type PausedError struct {
	GateID string
}

func (e *PausedError) Error() string {
	return fmt.Sprintf(
		"run paused at gate %q; resume with `oh-my-graph resume <run-id> --approve %s` or `--reject %s`",
		e.GateID, e.GateID, e.GateID,
	)
}

// LimitPausedError is Run's result when a node's claude subprocess hit the
// subscription's session limit (ADR 0009): the limited node was recorded
// nowhere (not FAILED — it never really ran), in-flight siblings were drained
// (not cancelled), and no new work launched, so the run is resumable with
// `oh-my-graph resume <run-id> --retry-failed` once the limit resets — the
// limited node re-runs because it was never marked passed. NodeIDs lists every
// node that hit the limit before the drain finished (siblings draining
// concurrently may join the paused set), sorted; Cause is the first limited
// node's captured failure cause, carrying the CLI's own "resets <time>" hint
// for the CLI layer to surface. cmd/oh-my-graph maps this to exit code 2,
// exactly like a gate pause — a limit is not a failure and must never be
// reported or logged as one.
type LimitPausedError struct {
	NodeIDs []string
	Cause   string
}

func (e *LimitPausedError) Error() string {
	return fmt.Sprintf(
		"run paused: session limit reached at node(s) %s; resume with `oh-my-graph resume <run-id> --retry-failed` once the limit resets",
		strings.Join(e.NodeIDs, ", "),
	)
}

// pauseSignal is runNode's internal signal that a gate node's decision was
// DecisionPause. It is not a node failure: Run's launch loop recognizes it via
// errors.As before it can reach the continue-on-fail or halt-on-fail paths, so
// it never crosses the Run boundary — a caller only ever sees *PausedError,
// returned once for the whole run after in-flight siblings have drained.
type pauseSignal struct{ NodeID string }

func (e *pauseSignal) Error() string { return fmt.Sprintf("node %q: gate paused", e.NodeID) }

// rejectSignal is runNode's internal signal that a gate node's decision was
// DecisionReject. Like pauseSignal, Run's launch loop intercepts it before the
// normal failure paths: a reject always prunes its own subtree regardless of
// --continue-on-fail (DESIGN.md, "--reject <gate-id> prunes the gate's
// subtree ... It is not a crash"), so it is folded into the same
// *RunFailedError a continue-on-fail prune would produce, never a *HaltError.
type rejectSignal struct{ NodeID string }

func (e *rejectSignal) Error() string { return fmt.Sprintf("node %q: gate rejected", e.NodeID) }

// limitSignal is runNode's internal signal that the node's subprocess hit the
// subscription session limit (NodeOutcome.SessionLimited). Like pauseSignal,
// Run's launch loop intercepts it before the continue-on-fail / halt-on-fail
// paths — a limit pauses the whole run regardless of failure policy, and the
// limited node is deliberately recorded nowhere (no ledger row, no snapshot
// record, no terminal event), so a later `resume --retry-failed` sees it as
// simply never run and launches it again (ADR 0009).
type limitSignal struct {
	NodeID string
	Cause  string
}

func (e *limitSignal) Error() string {
	return fmt.Sprintf("node %q: session limit reached", e.NodeID)
}

// feedbackSignal is runNode's internal signal that a feedback declarer's
// judgment failure re-armed its loop (ADR 0010) — the fourth intercepted
// signal beside pauseSignal, limitSignal and rejectSignal. By the time it
// surfaces, the non-final record trio is already durable (ledger row, marker
// snapshot record, node_retried event); what remains is scheduling: Run's
// launch loop re-seeds the body's in-degrees (counting only in-body parents)
// and relaunches Target, unless a pause elsewhere (gate or session limit)
// has already stopped the run — then the relaunch is suppressed exactly as a
// successful node's dependents are, and the marker makes a later resume
// re-enter the loop. It never reaches the continue-on-fail or halt paths: a
// round with budget left is not a failure yet.
type feedbackSignal struct {
	NodeID string // the declarer whose arc fired
	Target string // the rerun target the loop re-launches
	Round  int    // the round now beginning (1-based)
}

func (e *feedbackSignal) Error() string {
	return fmt.Sprintf("node %q: feedback round %d — re-running %s", e.NodeID, e.Round, e.Target)
}

// MissingToolPolicyError means a caller imposed a tool ceiling (a non-nil
// Options.ToolPolicies) that has no entry for this node.
//
// This is a node failure rather than a fall back to "no ceiling" on purpose.
// The fallback would produce exactly the outcome the ceiling exists to prevent
// — one unreviewed planned node running with the user's full standing grants
// while every other node in the same run is capped — and it would do it
// silently, in the path nobody looks at because the run succeeded. Reaching
// this error means the policy map and the graph disagree, which is a bug in
// whoever built them; it should say so.
type MissingToolPolicyError struct {
	NodeID string
}

func (e *MissingToolPolicyError) Error() string {
	return fmt.Sprintf("node %q has no tool policy, but this run imposes one: refusing to run it without its execution ceiling", e.NodeID)
}

// asErr is errors.As with the argument order flipped for readability at the call
// site (asErr(err, &target)). It keeps the classification helpers terse.
func asErr(err error, target any) bool {
	return errors.As(err, target)
}
