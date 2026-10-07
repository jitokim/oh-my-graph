package main

import (
	"errors"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// gated-dev's review rounds (#345): the pin follows the review that PASSED,
// and a review that never passes stops the run before the gate.

const gatedDevFindings = "FINDINGS:\n- the change has no test"

// --- 5d. the pin is the HEAD the passing review saw ---------------------------

func TestGatedDev_PinIsTheHeadThePassingReviewSaw_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner(gatedDevFindings, "CLEAN")
	const runID = "gd-rework"

	pausedGatedDev(t, graphPath, repo, runID, rec)

	if n := len(rec.callsTo("dev")); n != 2 {
		t.Fatalf("dev ran %d time(s), want 2: the first pass and the one rework FINDINGS buys", n)
	}
	reviews := rec.callsTo("review")
	if len(reviews) != 2 {
		t.Fatalf("review ran %d time(s), want 2", len(reviews))
	}
	first, second := reviews[0].head, reviews[1].head
	if first == second {
		t.Fatalf("both reviews saw %s; dev's rework must have committed between them", first)
	}
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}
	if pinned != second {
		t.Errorf("the pin is at %s, want %s, the HEAD the second, passing review saw (the first round's was %s)", pinned, second, first)
	}
	if tip := mustGit(t, repo, "rev-parse", "refs/heads/"+laneBranch(runID)); tip != second {
		t.Errorf("the lane's branch is at %s at the pause, want the reviewed %s", tip, second)
	}
}

// --- 6. a review that never passes stops the run before the gate --------------

func TestGatedDev_ReviewFailingAfterTheRepairStopsBeforeTheGate_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner(gatedDevFindings)
	const runID = "gd-findings"

	err := startGatedDev(t, graphPath, repo, runID, rec)
	if err == nil {
		t.Fatal("the run returned nil; a review that keeps returning FINDINGS must fail it")
	}
	var paused *schedule.PausedError
	if errors.As(err, &paused) {
		t.Fatalf("the run paused at %q; it must stop before the gate", paused.GateID)
	}
	if code := exitCodeForError(err); code == 2 {
		t.Errorf("the failed run exits %d, the code of a pause", code)
	}

	if n := len(rec.callsTo("review")); n != 2 {
		t.Errorf("review ran %d time(s), want 2: the first round and the one repair round", n)
	}
	snap := gatedDevSnapshot(t, runID)
	assertVerdict(t, snap, "review", runstate.VerdictFail)
	if snap.Gate.PausedAt != "" {
		t.Errorf("the snapshot is paused at %q", snap.Gate.PausedAt)
	}
	if record, ok := snap.Nodes["approve-publish"]; ok {
		t.Errorf("approve-publish has a record (%+v); the run must never reach it", record)
	}
	rec.assertNeverStarted(t, "check-head")
	rec.assertNeverStarted(t, "pr")
	if sha, ok := refSHA(t, repo, pinRef(runID)); ok {
		t.Errorf("a review that never passed left a pin %s at %s", pinRef(runID), sha)
	}
}
