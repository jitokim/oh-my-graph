// Package gate is the v1.1 human-pause seam: a `gate` node lets a graph pause
// for human approval before its dependents run.
//
// A gate does not block a process waiting for that approval — oh-my-graph is a
// stateless CLI with no daemon (SECURITY.md) — it decides whether the run
// should approve past the gate, reject its subtree, or pause the whole run to
// be continued later by `oh-my-graph resume` (see DESIGN.md, "Gate nodes and
// resume", and ADR 0003). Which controller answers is chosen once, at the CLI
// boundary, from the invocation: PauseController for a fresh `run` with no
// `--auto-approve` and for `auto` (whose planned graphs can never contain a
// gate — the coordinator refuses one at plan time), RecordedController for
// `resume` (which replays the snapshot's decisions) and for a fresh `run
// --auto-approve <gate-id>` (which replays the approvals the operator
// registered on the command line; every gate not named still pauses, #285).
// The Scheduler asks the same question either way and never learns which one
// it is talking to.
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

// PauseController is the GateController a fresh `run` with no `--auto-approve`
// injects, and the one `auto` injects (a planned graph holds no gate for it to
// answer). It is also the Scheduler's default for a nil Options.Gate. Every
// gate always pauses: with no decision registered at launch there is nothing
// to carry forward, so the only honest answer is "stop here and let a human
// decide" (DESIGN.md, "PauseController"). A fresh run that DOES carry
// approvals — `run --auto-approve` — injects a RecordedController instead.
type PauseController struct{}

// NewPauseController returns the always-pause GateController.
func NewPauseController() PauseController { return PauseController{} }

// Evaluate always returns DecisionPause, nil.
func (PauseController) Evaluate(_ context.Context, _ graph.Node) (Decision, error) {
	return DecisionPause, nil
}

// RecordedController is the GateController `oh-my-graph resume` injects, and
// the one a fresh `run --auto-approve` injects. It answers from a decision map
// (gate node id -> Decision) — the snapshot's decisions on resume, the
// operator's launch-time approvals on run — and the mechanism is the same
// either way: a gate already decided replays that decision, and a gate with no
// entry pauses, so neither a resume nor a pre-approved run can silently run
// past an approval it was not given (DESIGN.md, "RecordedController").
type RecordedController struct {
	decisions map[string]Decision
}

// NewRecordedController builds a RecordedController over decisions, keyed by
// gate node id. The map is read-only to the controller; it is the caller's job
// to have built it before construction — `run` maps each --auto-approve id to
// DecisionApprove, `resume` merges its newly supplied --approve/--reject into
// the snapshot's map and adds the same launch-time approvals for any named
// gate not reached yet. A nil map behaves like an empty one — every gate
// pauses.
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
