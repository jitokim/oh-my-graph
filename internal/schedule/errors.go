package schedule

import (
	"errors"
	"fmt"
	"strings"
)

// NodeCheckError is a node that ran but failed its success_check. It names the
// node and the predicate (exit_zero / result_matches) that did not hold, so the
// ledger and the halt message can be precise about why.
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

// asErr is errors.As with the argument order flipped for readability at the call
// site (asErr(err, &target)). It keeps the classification helpers terse.
func asErr(err error, target any) bool {
	return errors.As(err, target)
}
