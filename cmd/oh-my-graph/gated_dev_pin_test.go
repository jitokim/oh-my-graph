package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// The paused lane, tampered with before the person approves (#345). Each test
// pauses the shipped gated-dev graph, changes the lane's HEAD or its pin in
// the shared repo, then resumes with --approve: check-head's engine verify
// must FAIL and pr's agent must never start. See gated_dev_run_test.go for why
// moving the lane's HEAD means moving refs/heads/omg/<run-id>/lane.

// advanceBranch puts a new commit on top of branch in repo without checking it
// out, and returns its SHA.
func advanceBranch(t *testing.T, repo, branch string) string {
	t.Helper()
	ref := "refs/heads/" + branch
	tip := mustGit(t, repo, "rev-parse", "--verify", ref)
	moved := mustGit(t, repo, "commit-tree", tip+"^{tree}", "-p", tip, "-m", "moved after the review")
	mustGit(t, repo, "update-ref", ref, moved, tip)
	return moved
}

// assertCheckHeadRefusedThePublish is the outcome every tampering test wants
// from the resumed leg: it failed, check-head's verdict is FAIL, and pr's
// agent was never started.
func assertCheckHeadRefusedThePublish(t *testing.T, runID string, resumeErr error, rec *gatedDevRunner) {
	t.Helper()
	if resumeErr == nil {
		t.Error("the resumed leg returned nil; check-head must fail it")
	}
	if n := len(rec.callsTo("check-head")); n != 1 {
		t.Errorf("check-head's agent started %d time(s), want 1: its verify is the check under test", n)
	}
	assertVerdict(t, gatedDevSnapshot(t, runID), "check-head", runstate.VerdictFail)
	rec.assertNeverStarted(t, "pr")
}

// --- 4. HEAD moved during the pause ------------------------------------------

func TestGatedDev_HeadMovedDuringThePauseFailsCheckHead_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-moved"

	pausedGatedDev(t, graphPath, repo, runID, rec)

	// What the pause left: no lane worktree, the branch retained, the pin on
	// its tip. The move below is only meaningful against that state.
	if _, err := os.Stat(filepath.Join(runDirFor(runID), "worktrees", "lane")); !os.IsNotExist(err) {
		t.Fatalf("the lane worktree is still on disk at the pause (stat: %v); the resumed leg would not re-attach the branch", err)
	}
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}
	if tip := mustGit(t, repo, "rev-parse", "refs/heads/"+laneBranch(runID)); tip != pinned {
		t.Fatalf("the pin %s is at %s, but the lane's branch is at %s", pinRef(runID), pinned, tip)
	}

	moved := advanceBranch(t, repo, laneBranch(runID))

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertCheckHeadRefusedThePublish(t, runID, err, rec)
	// The leg really checked the moved HEAD: check-head's agent ran on it.
	if calls := rec.callsTo("check-head"); len(calls) == 1 && calls[0].head != moved {
		t.Errorf("check-head ran on %s, want the moved HEAD %s", calls[0].head, moved)
	}
	if sha, ok := refSHA(t, repo, pinRef(runID)); !ok || sha != pinned {
		t.Errorf("a failed check-head changed the pin: %s is %q (exists %v), want %s", pinRef(runID), sha, ok, pinned)
	}
}

// --- 5. a second run's pin is not this run's ----------------------------------

// Run B pins the HEAD it reviewed under its OWN ref. Run A's lane is then
// pointed at exactly the commit B pinned, so a pin shared between runs would
// match A's HEAD and let A publish what nobody approved for A.
func TestGatedDev_AnotherRunsPinDoesNotApproveThisRun_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	const runA, runB = "gd-run-a", "gd-run-b"

	recA := newGatedDevRunner("CLEAN")
	pausedGatedDev(t, graphPath, repo, runA, recA)
	pinnedA, ok := refSHA(t, repo, pinRef(runA))
	if !ok {
		t.Fatalf("run A left no pin %s at its pause", pinRef(runA))
	}

	recB := newGatedDevRunner("CLEAN")
	pausedGatedDev(t, graphPath, repo, runB, recB)
	if pinRef(runB) == pinRef(runA) {
		t.Fatalf("both runs pin under %s", pinRef(runA))
	}
	if want := "refs/omg-approved/omg/" + runB + "/lane"; pinRef(runB) != want {
		t.Fatalf("pinRef(%s) = %s, want %s", runB, pinRef(runB), want)
	}
	pinnedB, ok := refSHA(t, repo, pinRef(runB))
	if !ok {
		t.Fatalf("run B left no pin %s, named after its branch %s", pinRef(runB), laneBranch(runB))
	}
	if tipB := mustGit(t, repo, "rev-parse", "refs/heads/"+laneBranch(runB)); pinnedB != tipB {
		t.Fatalf("run B's pin is at %s, but its lane's branch is at %s", pinnedB, tipB)
	}
	if pinnedB == pinnedA {
		t.Fatalf("both runs pinned %s; B's dev committed, so B's HEAD must differ", pinnedA)
	}
	if sha, _ := refSHA(t, repo, pinRef(runA)); sha != pinnedA {
		t.Fatalf("run B moved run A's pin from %s to %s", pinnedA, sha)
	}

	// A's HEAD moves to the commit B's review approved.
	mustGit(t, repo, "update-ref", "refs/heads/"+laneBranch(runA), pinnedB)

	err := resumeGatedDev(t, runA, "--approve", recA)
	assertCheckHeadRefusedThePublish(t, runA, err, recA)
	if sha, ok := refSHA(t, repo, pinRef(runB)); !ok || sha != pinnedB {
		t.Errorf("run A's check-head touched run B's pin: %s is %q (exists %v), want %s", pinRef(runB), sha, ok, pinnedB)
	}
}

// --- 5b. the pin rewritten during the pause -----------------------------------

func TestGatedDev_PinRewrittenDuringThePauseFailsCheckHead_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-rewritten"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	pinned, ok := refSHA(t, repo, pinRef(runID))
	if !ok {
		t.Fatalf("no pin %s at the pause", pinRef(runID))
	}
	other := mustGit(t, repo, "rev-parse", "refs/heads/main")
	if other == pinned {
		t.Fatalf("main is at the pinned %s; the rewrite would change nothing", pinned)
	}
	mustGit(t, repo, "update-ref", pinRef(runID), other)

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertCheckHeadRefusedThePublish(t, runID, err, rec)
}

// --- 5c. the pin deleted during the pause -------------------------------------

func TestGatedDev_PinDeletedDuringThePauseFailsCheckHead_345(t *testing.T) {
	isolateRunHome(t)
	graphPath := gatedDevGraphPath(t)
	repo := gatedDevRepo(t)
	rec := newGatedDevRunner("CLEAN")
	const runID = "gd-deleted"

	pausedGatedDev(t, graphPath, repo, runID, rec)
	if _, ok := refSHA(t, repo, pinRef(runID)); !ok {
		t.Fatalf("no pin %s at the pause; deleting it would prove nothing", pinRef(runID))
	}
	mustGit(t, repo, "update-ref", "-d", pinRef(runID))

	err := resumeGatedDev(t, runID, "--approve", rec)
	assertCheckHeadRefusedThePublish(t, runID, err, rec)
}
