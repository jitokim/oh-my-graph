// Package runner is the claude-execution seam of oh-my-graph. It defines the
// NodeRunner interface the Scheduler depends on, the value types crossing that
// boundary, and two implementations: ClaudeCLIRunner (the ONLY object in the
// whole program that touches os/exec) and FakeRunner (scripted, for tests).
//
// The seam exists so the entire scheduler — topological order, fan-out, fan-in,
// retry, halt-on-fail, cost summation — is unit-testable against FakeRunner with
// zero real claude subprocesses. The Scheduler never learns whether a real
// claude ran; it only ever sees a NodeOutcome or an error.
package runner

import "context"

// NodeInvocation is everything the runner needs to launch one node. It is a
// rendered spec: Prompt and Cwd are already interpolated by the Handoff, and
// ResumeSession is empty unless this node is resuming a session-parent.
//
// AllowedTools and DisallowedTools are NOT symmetric, and the difference is
// load-bearing. --allowedTools only ADDS to whatever the user's own
// ~/.claude/settings.json already grants, so it bounds what a node is asked to
// use, not what it can do. --disallowedTools SUBTRACTS, and wins over any
// prior grant, so DisallowedTools is the only field here that is an actual
// execution ceiling. It is empty for hand-written graphs (human-reviewed, the
// user's own settings are the intended policy) and set per node by auto mode,
// whose graphs are unreviewed LLM output.
type NodeInvocation struct {
	Prompt          string
	Cwd             string
	PermissionMode  string
	ResumeSession   string
	AllowedTools    []string
	DisallowedTools []string
	// BudgetUSD is the node's declared budget_usd, or 0 when it declared none.
	// A positive value is passed to claude as --max-budget-usd, which makes the
	// CLI abort THIS invocation the moment its own running spend crosses the
	// budget — a real mid-run cost kill, on top of (not replacing) the
	// scheduler's post-hoc ledger check. The bound is per `claude -p` process,
	// so it maps onto oh-my-graph's per-node budget even for a session-resume
	// node (a resumed run does not re-count the parent session's spend). A
	// non-positive value adds no flag and leaves the argv unchanged.
	BudgetUSD float64
}

// NodeOutcome is the parsed result of one node run: the claude session id (for
// session handoff and the ledger), the .result text (for success_check and
// artifact handoff), the reported cost, and the process exit code.
type NodeOutcome struct {
	SessionID    string
	Result       string
	TotalCostUSD float64
	ExitCode     int
	// BudgetExhausted is true when claude aborted the run because its own
	// --max-budget-usd cap was reached (envelope subtype error_max_budget_usd).
	// It is a budget failure, not the generic non-zero exit its ExitCode would
	// otherwise be read as: the Scheduler turns it into a *NodeBudgetError so it
	// shares the post-hoc check's budget_exceeded retry token and ledger
	// language instead of being retried by a plain nonzero_exit policy.
	BudgetExhausted bool
}

// NodeRunner runs one node to completion and returns its outcome. A non-nil
// error means the run could not produce a usable outcome at all (spawn failure,
// timeout, or unparseable output) — it is NOT how a node reports a failed
// success_check or a non-zero exit; those travel inside NodeOutcome and are
// judged by the Scheduler. Implementations must never return a zero NodeOutcome
// with a nil error to paper over a failure.
type NodeRunner interface {
	Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
}
