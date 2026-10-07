package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// tok spells a handoff placeholder for expr without a literal double-curly
// token in this file's source (#348).
func tok(expr string) string {
	return "{" + "{ " + expr + " }" + "}"
}

// twoDescribedGatesRun pauses a run whose two gates both carry a description:
// approve quotes the ticket input, final quotes ship's artifact. Approving the
// first gives a resumed leg that pauses again, at a gate with its own text
// (#348).
func twoDescribedGatesRun(t *testing.T, ticket string) (runID string, rec *capturingRunner) {
	t.Helper()
	g := mustParse(t, `{"name":"gate-flow","inputs":["ticket"],"nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"],"description":"ship `+tok("inputs.ticket")+`?"},
		{"id":"ship","prompt":"ship","depends_on":["approve"]},
		{"id":"final","type":"gate","depends_on":["ship"],"description":"release `+tok("artifacts.ship")+`?"}]}`)
	rec = &capturingRunner{}
	runID = "run-1"
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{"ticket": ticket}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "approve" {
		t.Fatalf("expected the run to pause at approve, got %T: %v", err, err)
	}
	return runID, rec
}

// TestPause_StoresDescriptionOnFreshRun_348: #348 — a fresh run's pause stores
// in state.json exactly the description its pause block printed.
func TestPause_StoresDescriptionOnFreshRun_348(t *testing.T) {
	isolateRunHome(t)
	runID, _, out := describedGateFlowRun(t, "T-42")
	shown := shownFlowDescription(runID, "T-42")

	raw, snap := loadRunSnapshot(t, runID)
	if snap.Gate.PausedAt != "approve" || snap.Gate.PausedGateDescription != shown {
		t.Fatalf("gate state = %+v, want paused at approve with %q", snap.Gate, shown)
	}
	if !strings.Contains(out, "Paused at gate \"approve\" ("+snap.Gate.PausedGateDescription+"). Resume with:\n") {
		t.Fatalf("the stored description is not what the pause printed\nstored: %q\nprinted:\n%s", snap.Gate.PausedGateDescription, out)
	}
	if !strings.Contains(raw, `"paused_gate_description"`) {
		t.Fatalf("state.json lacks the key:\n%s", raw)
	}
}

// TestPause_StoresDescriptionOnResumedLegThatPausesAgain_348: #348 — a resumed
// leg that pauses at a later gate stores that gate's description, and none of
// the gate it just decided.
func TestPause_StoresDescriptionOnResumedLegThatPausesAgain_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec := twoDescribedGatesRun(t, "T-42")
	if _, snap := loadRunSnapshot(t, runID); snap.Gate.PausedGateDescription != "ship T-42?" {
		t.Fatalf("first pause stored %q, want %q", snap.Gate.PausedGateDescription, "ship T-42?")
	}

	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "final" {
		t.Fatalf("expected the approved leg to pause at final, got %T: %v", err, err)
	}
	_, snap := loadRunSnapshot(t, runID)
	want := "release " + filepath.Join(runDirFor(runID), "ship.out") + "?"
	if snap.Gate.PausedAt != "final" || snap.Gate.PausedGateDescription != want {
		t.Fatalf("second pause gate state = %+v, want paused at final with %q", snap.Gate, want)
	}
	if !strings.Contains(out, "Paused at gate \"final\" ("+want+"). Resume with:\n") {
		t.Fatalf("the stored description is not what the resumed leg printed:\n%s", out)
	}
	if got := snap.Nodes["approve"].GateDescription; got != "ship T-42?" {
		t.Fatalf("decided gate recorded %q, want %q", got, "ship T-42?")
	}
}

// TestPause_FieldClearedWhenLegDoesNotPause_348: #348 — a leg that runs to
// completion leaves no paused_gate_description key behind.
func TestPause_FieldClearedWhenLegDoesNotPause_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec := twoDescribedGatesRun(t, "T-42")
	for _, gateID := range []string{"approve", "final"} {
		var err error
		captureStdout(t, func() {
			err = executeResume(parseResumeFlags(t, []string{runID, "--approve", gateID}), rec, nil)
		})
		if gateID == "final" && err != nil {
			t.Fatalf("final leg: %v", err)
		}
	}
	raw, snap := loadRunSnapshot(t, runID)
	if snap.Gate.PausedAt != "" || strings.Contains(raw, "paused_gate_description") {
		t.Fatalf("a completed run still carries a paused gate description:\n%s", raw)
	}
}
