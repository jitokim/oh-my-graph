package gate

import (
	"context"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// --- PauseController ---------------------------------------------------------

// TestPauseController_AlwaysPauses proves the controller a fresh `run` with no
// `--auto-approve` (and `auto`) injects: with nothing registered at launch
// there is no approval to carry forward, so no matter which node it is asked
// about it always answers DecisionPause. A fresh `run --auto-approve` is the
// RecordedController case below, not this one.
func TestPauseController_AlwaysPauses(t *testing.T) {
	c := NewPauseController()
	for _, id := range []string{"approve", "ship-review", ""} {
		decision, err := c.Evaluate(context.Background(), graph.Node{ID: id})
		if err != nil {
			t.Fatalf("node %q: unexpected error: %v", id, err)
		}
		if decision != DecisionPause {
			t.Fatalf("node %q: decision = %q, want %q", id, decision, DecisionPause)
		}
	}
}

// --- RecordedController -------------------------------------------------------

// TestRecordedController_ReplaysRecordedDecision proves resume's controller
// answers from the decision map it was built with, for both terminal outcomes.
func TestRecordedController_ReplaysRecordedDecision(t *testing.T) {
	c := NewRecordedController(map[string]Decision{
		"approve": DecisionApprove,
		"reject":  DecisionReject,
	})

	got, err := c.Evaluate(context.Background(), graph.Node{ID: "approve"})
	if err != nil || got != DecisionApprove {
		t.Fatalf("approve gate = (%q, %v), want (%q, nil)", got, err, DecisionApprove)
	}
	got, err = c.Evaluate(context.Background(), graph.Node{ID: "reject"})
	if err != nil || got != DecisionReject {
		t.Fatalf("reject gate = (%q, %v), want (%q, nil)", got, err, DecisionReject)
	}
}

// TestRecordedController_UndecidedGatePauses proves a gate absent from the
// decision map pauses again rather than silently approving or rejecting — a
// resume must never guess at a decision it was not given.
func TestRecordedController_UndecidedGatePauses(t *testing.T) {
	c := NewRecordedController(map[string]Decision{"approve": DecisionApprove})

	got, err := c.Evaluate(context.Background(), graph.Node{ID: "next-gate"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DecisionPause {
		t.Fatalf("undecided gate = %q, want %q", got, DecisionPause)
	}
}

// TestRecordedController_NilMapPausesEveryGate proves a nil decision map (the
// zero value, never constructed with NewRecordedController(nil) but reachable
// via a struct literal) behaves like an empty one rather than panicking.
func TestRecordedController_NilMapPausesEveryGate(t *testing.T) {
	c := NewRecordedController(nil)
	got, err := c.Evaluate(context.Background(), graph.Node{ID: "approve"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != DecisionPause {
		t.Fatalf("decision = %q, want %q", got, DecisionPause)
	}
}
