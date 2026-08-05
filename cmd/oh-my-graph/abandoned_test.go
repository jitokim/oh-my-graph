package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// The CLI half of ADR 0015 §4: `runs list` labels an abandoned run, `watch`
// refuses to tail one, and `resume` says what it is about to do it beside. Every
// fixture here is a real event stream (the StreamWriter), a real snapshot
// (runstate.Write) and a REAL lock (runstate.AcquireLock), because the whole
// question is what the lock says about a leg that is gone.

// deadLegLock leaves exactly what a leg that died leaves behind: a marked lock
// file nothing holds. It skips where the probe cannot answer — no flock(2), or
// a filesystem outside runstate's known-local allowlist — since there the
// derivation is unknown by design (and the pre-ADR-0015 answer is correct).
func deadLegLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, lockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release fixture lock: %v", err)
	}
	if got := runstate.ProbeLock(path); got != runstate.LivenessFree {
		t.Skipf("the lock probe cannot answer here (%v)", got)
	}
}

// liveLegLock holds the run's lock for the test, as a running leg does.
func liveLegLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, lockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	t.Cleanup(func() { release() })
	if got := runstate.ProbeLock(path); got != runstate.LivenessHeld {
		t.Skipf("the lock probe cannot answer here (%v)", got)
	}
}

// openLegRun writes a run whose leg started a node and never closed: the shape
// both zombie runs on the maintainer's machine have on disk.
func openLegRun(t *testing.T, root, runID string, withSnapshot bool) string {
	t.Helper()
	dir := filepath.Join(root, runID)
	writeEventFixture(t, dir, runID, []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodeStarted, NodeID: "a"},
	})
	if withSnapshot {
		if err := runstate.Write(filepath.Join(dir, stateFileName), runstate.Snapshot{
			RunID: runID,
			Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b","depends_on":["a"]}]}`),
			Nodes: map[string]runstate.NodeRecord{},
		}); err != nil {
			t.Fatalf("write fixture snapshot: %v", err)
		}
	}
	return dir
}

// --- `runs list` ---------------------------------------------------------------

func TestListRuns_AbandonedRunIsLabelledAndHinted(t *testing.T) {
	root := t.TempDir()
	dir := openLegRun(t, root, "run-dead", true)
	deadLegLock(t, dir)

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()

	row := lineContaining(t, got, "run-dead")
	fields := strings.Fields(row)
	if verdict := fields[len(fields)-1]; verdict != verdictAbandoned {
		t.Errorf("verdict = %q, want %q (row %q)", verdict, verdictAbandoned, row)
	}
	// Not FAIL: a FAIL is a verdict about the work, and the work has none.
	if strings.Contains(row, "FAIL") {
		t.Errorf("an abandoned run must not be rendered as a failure: %q", row)
	}
	if !strings.Contains(got, "oh-my-graph resume run-dead --retry-failed") {
		t.Errorf("the row must be followed by the recovery hint:\n%s", got)
	}
	if !strings.Contains(got, runstatus.OrphanWarning) {
		t.Errorf("the hint must carry the orphaned-claude warning:\n%s", got)
	}
	if warn.Len() != 0 {
		t.Errorf("an abandoned run is listable, not skippable, got warnings: %q", warn.String())
	}
}

// TestListRuns_ASnapshotlessAbandonedRunIsStillListed is the regression ADR
// 0015 §4 calls "the single easiest way to ship this ADR as a net loss": the
// snapshot-less excuse used to be granted only to an IN-FLIGHT run, so the
// moment such a run derived abandoned it would have vanished from the listing
// behind a WARNING — hiding exactly the runs this change exists to reveal.
func TestListRuns_ASnapshotlessAbandonedRunIsStillListed(t *testing.T) {
	root := t.TempDir()
	dir := openLegRun(t, root, "run-dead-early", false)
	deadLegLock(t, dir)

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()

	if warn.Len() != 0 {
		t.Fatalf("a snapshot-less abandoned run must not be skipped, got warnings: %q", warn.String())
	}
	row := lineContaining(t, got, "run-dead-early")
	if !strings.Contains(row, verdictAbandoned) {
		t.Errorf("row = %q, want an %s row with placeholders", row, verdictAbandoned)
	}
	if !strings.Contains(row, "-") {
		t.Errorf("row = %q, want the same %q placeholders an unknown graph renders", row, "-")
	}
	// Nothing to resume FROM: the honest recovery is to run the graph again.
	if strings.Contains(got, "oh-my-graph resume") {
		t.Errorf("a snapshot-less run must not be handed a resume command that would fail:\n%s", got)
	}
	if !strings.Contains(got, "run the graph again") {
		t.Errorf("the hint must say what recovery actually is here:\n%s", got)
	}
}

// TestListRuns_VerdictAgreesWithTheSharedRule is the CLI arm of the
// cross-surface agreement test: the same open-leg stream must read RUNNING or
// ABANDONED strictly according to runstatus, never by a rule this command
// composes for itself.
func TestListRuns_VerdictAgreesWithTheSharedRule(t *testing.T) {
	for _, lock := range []string{"no lock file", "lock held", "lock free"} {
		t.Run(lock, func(t *testing.T) {
			root := t.TempDir()
			dir := openLegRun(t, root, "run-1", true)
			switch lock {
			case "lock held":
				liveLegLock(t, dir)
			case "lock free":
				deadLegLock(t, dir)
			}

			shared, err := runstatus.Of(dir)
			if err != nil {
				t.Fatalf("runstatus.Of returned error: %v", err)
			}
			var out, warn strings.Builder
			if err := listRuns(&out, &warn, root); err != nil {
				t.Fatalf("listRuns returned error: %v", err)
			}
			row := lineContaining(t, out.String(), "run-1")
			fields := strings.Fields(row)
			if verdict, want := fields[len(fields)-1], unsettledVerdict(shared); verdict != want {
				t.Errorf("verdict = %q, want %q (the shared rule says %v)", verdict, want, shared)
			}
		})
	}
}

// --- `watch` -------------------------------------------------------------------

func TestWatchRun_RefusesToTailAnAbandonedRun(t *testing.T) {
	root := t.TempDir()
	dir := openLegRun(t, root, "run-dead", true)
	deadLegLock(t, dir)

	var out, warn strings.Builder
	// A background context, so a hang would hang this test rather than pass it:
	// the point is that watch returns without waiting on a stream that will
	// never get another line.
	err := watchRun(context.Background(), &out, &warn, dir, "run-dead", testPoll)
	if err == nil {
		t.Fatal("watchRun tailed an abandoned run; want a refusal")
	}
	if !strings.Contains(err.Error(), "run-dead") || !strings.Contains(err.Error(), "abandoned") {
		t.Errorf("the refusal must name the run and say why: %v", err)
	}
	if !strings.Contains(warn.String(), runstatus.OrphanWarning) {
		t.Errorf("the refusal must carry the recovery hint: %q", warn.String())
	}
}

// TestWatchRun_TailsARunWhoseLegIsAlive is the other half: the refusal must key
// off the LOCK, not off "the last leg is open", or watch would refuse every
// live run there is.
func TestWatchRun_TailsARunWhoseLegIsAlive(t *testing.T) {
	root := t.TempDir()
	dir := openLegRun(t, root, "run-live", true)
	liveLegLock(t, dir)

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	var out, warn strings.Builder
	if err := watchRun(ctx, &out, &warn, dir, "run-live", testPoll); err != nil {
		t.Fatalf("watchRun refused a live run: %v", err)
	}
	if !strings.Contains(out.String(), "▶ run started") {
		t.Errorf("a live run must be tailed as before:\n%s", out.String())
	}
}

// --- `resume` ------------------------------------------------------------------

// TestResume_WarnsAboutTheOrphanedChildOfADeadLeg pins the fourth surface. The
// warning has to be printed BEFORE the lock is taken — taking it is what makes
// the answer "held" — and it is the whole mitigation ADR 0015 accepts for its
// largest cost: the dead leg's `claude` may still be running and spending, and
// this leg is about to run that node again beside it.
func TestResume_WarnsAboutTheOrphanedChildOfADeadLeg(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)
	runDir := runDirFor(runID)
	// The first leg paused cleanly and left its lock behind (release does not
	// unlink). Reopen a leg on the stream and never close it: a leg that died.
	writeEventFixture(t, runDir, runID, []runfeed.Event{{Type: runfeed.EventRunStarted}})
	deadLegLock(t, runDir)

	stderr, resumeErr := captureStderr(t, func() error {
		return executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(stderr, "abandoned") || !strings.Contains(stderr, runstatus.OrphanWarning) {
		t.Errorf("resuming an abandoned run must warn about the orphaned child, got %q", stderr)
	}
}

// TestResume_SnapshotlessRunSaysThereIsNothingToResumeFrom pins ADR 0015 §5's
// one requirement about words: a run killed before its first node settled has
// no state.json, `resume` fails on it — --retry-failed included — and it must
// say why rather than surfacing a bare "no such file".
func TestResume_SnapshotlessRunSaysThereIsNothingToResumeFrom(t *testing.T) {
	isolateRunHome(t)
	const runID = "run-dead-early"
	dir := openLegRun(t, runsRoot(), runID, false)
	deadLegLock(t, dir)

	err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), &capturingRunner{}, nil)
	if err == nil {
		t.Fatal("resuming a snapshot-less run must fail")
	}
	if !strings.Contains(err.Error(), "run the graph again") {
		t.Errorf("the error must say why and what to do instead, got %v", err)
	}
}
