// Package gate is the v1.1 human-pause seam: a `gate` node lets a graph pause
// for human approval before its dependents run.
//
// A gate does not block a process waiting for that approval — oh-my-graph is a
// stateless CLI with no daemon (SECURITY.md) — it decides whether the run
// should approve past the gate, reject its subtree, or pause the whole run to
// be continued later by `oh-my-graph resume` (see DESIGN.md, "Gate nodes and
// resume", and ADR 0003). Which controller answers is chosen once, at the CLI
// boundary, from the invocation: PauseController for a fresh `run`/`auto`
// (which can never carry an approval decided outside it), RecordedController
// for `resume` (which replays the snapshot's decisions). The Scheduler asks
// the same question either way and never learns which one it is talking to.
package gate

import (
	"context"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// Decision is a gate node's outcome. It is data, not a different controller —
// the Scheduler branches on the value, not on which GateController produced
// it.
type Decision string

const (
	// DecisionApprove lets the gate's dependents proceed.
	DecisionApprove Decision = "approve"
	// DecisionReject prunes the gate's subtree; independent branches still
	// finish and the run reports it, but does not halt/cancel on it.
	DecisionReject Decision = "reject"
	// DecisionPause stops the run: no new work is launched, in-flight
	// siblings are drained (not cancelled), and the run persists a resumable
	// snapshot instead of completing.
	DecisionPause Decision = "pause"
)

// GateController decides a gate node's outcome. A non-nil error means the
// decision itself could not be made (as opposed to the decision being
// DecisionReject) and is treated as an ordinary node failure by the
// Scheduler.
type GateController interface {
	Evaluate(ctx context.Context, node graph.Node) (Decision, error)
}

// PauseController is the GateController a fresh `run`/`auto` invocation
// injects. Every gate always pauses: a fresh run has no prior decision to
// carry forward, so the only honest answer is "stop here and let a human
// decide" (DESIGN.md, "PauseController — injected by run/auto. Always
// DecisionPause: a fresh run cannot carry an approval.").
type PauseController struct{}

// NewPauseController returns the always-pause GateController.
func NewPauseController() PauseController { return PauseController{} }

// Evaluate always returns DecisionPause, nil.
func (PauseController) Evaluate(_ context.Context, _ graph.Node) (Decision, error) {
	return DecisionPause, nil
}

// RecordedController is the GateController `oh-my-graph resume` injects. It
// answers from a snapshot-backed decision map (gate node id -> Decision): a
// gate already decided replays that decision, and a gate with no entry pauses
// again, so a resume can never silently run past an approval it was not given
// (DESIGN.md, "RecordedController (snapshot-backed) for resume").
type RecordedController struct {
	decisions map[string]Decision
}

// NewRecordedController builds a RecordedController over decisions, keyed by
// gate node id. The map is read-only to the controller; it is the resume
// command's job to have already merged the newly supplied --approve/--reject
// into it before construction. A nil map behaves like an empty one — every
// gate pauses.
func NewRecordedController(decisions map[string]Decision) *RecordedController {
	return &RecordedController{decisions: decisions}
}

// Evaluate returns the recorded decision for node, or DecisionPause when none
// is recorded.
func (c *RecordedController) Evaluate(_ context.Context, node graph.Node) (Decision, error) {
	if decision, ok := c.decisions[node.ID]; ok {
		return decision, nil
	}
	return DecisionPause, nil
}
