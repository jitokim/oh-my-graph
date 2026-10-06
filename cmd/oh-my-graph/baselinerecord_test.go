package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// recordedBaseline is the skipped-baseline block the run left in its snapshot
// (#328) — nil when the run took the baseline.
func recordedBaseline(t *testing.T, runID string) *runstate.Baseline {
	t.Helper()
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), runstate.SnapshotFileName))
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	return snap.Baseline
}

var wantBaselineRecord = runstate.Baseline{Skipped: true, DeclaredBy: "--no-baseline"}

// #328: `auto --no-baseline` records the skip in state.json, so a reader of
// the finished run can tell it was chosen.
func TestRunAuto_NoBaselineIsRecordedInTheSnapshot(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	verifier := verify.NewFakeVerifier(nil)

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--no-baseline", "--verify-cmd", script)

	if err != nil {
		t.Fatalf("auto --no-baseline: %v\n%s", err, out)
	}
	got := recordedBaseline(t, soleRunID(t))
	if got == nil || *got != wantBaselineRecord {
		t.Errorf("baseline record = %+v, want %+v", got, wantBaselineRecord)
	}
}

// #328: a run that took the baseline writes no baseline key at all — checked
// on the raw bytes, so a run without the flag stays byte-identical to before.
func TestRunAuto_TakenBaselineWritesNoRecord(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{script: {ExitCode: 0}})

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--verify-cmd", script)

	if err != nil {
		t.Fatalf("auto with a green baseline: %v\n%s", err, out)
	}
	if n := len(verifier.Calls()); n != 1 {
		t.Fatalf("baseline ran %d times, want once", n)
	}
	raw, err := os.ReadFile(filepath.Join(runDirFor(soleRunID(t)), runstate.SnapshotFileName))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"baseline"`) {
		t.Errorf("a run that took the baseline recorded a baseline block:\n%s", raw)
	}
}

// #328: a --max-cycles loop opens a fresh run directory and recorder per
// cycle, so the skip must reach every cycle's state.json, not just cycle 1's.
func TestRunAuto_NoBaselineIsRecordedInEveryCycle(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})
	verifier := verify.NewFakeVerifier(nil)

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--no-baseline", "--verify-cmd", script, "--max-cycles", "2")

	if err != nil {
		t.Fatalf("a --no-baseline goal loop must run to its met verdict: %v\n%s", err, out)
	}
	snaps := goalSnapshots(t)
	if len(snaps) != 2 {
		t.Fatalf("%d run directories, want one per cycle (2)", len(snaps))
	}
	for i, snap := range snaps {
		if snap.Baseline == nil || *snap.Baseline != wantBaselineRecord {
			t.Errorf("cycle %d baseline record = %+v, want %+v", i+1, snap.Baseline, wantBaselineRecord)
		}
	}
}

// #328: the resume recorder rewrites the whole snapshot on every settle, so
// a resumed leg must carry the first leg's record forward unchanged.
func TestResume_CarriesTheSkippedBaselineIntoTheSecondLeg(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	_, err := runBaselineAuto(t, fake, verify.NewFakeVerifier(nil), osStdin(), "add a README section", "--no-baseline", "--verify-cmd", script)
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("the first leg should pause on the session limit, got %T: %v", err, err)
	}
	runID := soleRunID(t)
	if before := recordedBaseline(t, runID); before == nil || *before != wantBaselineRecord {
		t.Fatalf("the paused leg recorded %+v, want %+v", before, wantBaselineRecord)
	}

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed", "--verify-cmd", script}), fake, browser.NewFakeOpener())
	})
	if resumeErr != nil {
		t.Fatalf("the retry leg should finish the run cleanly, got: %v", resumeErr)
	}
	if after := recordedBaseline(t, runID); after == nil || *after != wantBaselineRecord {
		t.Errorf("after resume baseline record = %+v, want the first leg's %+v", after, wantBaselineRecord)
	}
}
