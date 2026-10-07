package main

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// escapedLimitCause is a real Claude cause with terminal escapes planted in
// it: a CSI that would repaint the screen and an OSC that would retitle the
// window. The record keeps it verbatim (it is runtime text); no terminal
// surface may print the ESC byte.
const escapedLimitCause = "You've hit your session limit\x1b[2J\x1b]0;pwned\x07 · resets 5:20pm"

// sanitizedLimitCause is escapedLimitCause as fence.SanitizeTerminalLine
// leaves it.
const sanitizedLimitCause = "You've hit your session limit · resets 5:20pm"

// limitPausedRun leaves runID paused on a usage limit at node a, its record
// carrying cause, and returns the leg's stdout.
func limitPausedRun(t *testing.T, runID, cause string) string {
	t.Helper()
	g := mustParse(t, `{"name":"limit-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"b","prompt":"b","depends_on":["a"]}]}`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome(cause), "b": passedOutcome("b")})
	var runErr error
	out := captureStdout(t, func() {
		runErr = executeGraph(context.Background(), runID, g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-flow.yaml", []byte("name: limit-flow\n"), false, nil, nil, nil)
	})
	if code := exitCodeForError(runErr); code != 2 {
		t.Fatalf("fixture: exit code = %d, want 2 (err %v)", code, runErr)
	}
	if got := loadSnapshot(t, runID).LimitPause; got == nil || got.Cause != cause {
		t.Fatalf("fixture: the record must carry the cause verbatim, got %+v", got)
	}
	return out
}

// TestListRuns_NamesTheLimitCauseFromTheRecord_358 (#358): ADR 0031 §8.3 — `runs
// list` says why a limit-paused run stopped, from the record, sanitized, and
// the run is still PAUSED.
func TestListRuns_NamesTheLimitCauseFromTheRecord_358(t *testing.T) {
	isolateRunHome(t)
	limitPausedRun(t, "run-limit-358", escapedLimitCause)

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), true); err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	got := out.String()
	if row := lineContaining(t, got, "run-limit-358 "); !strings.Contains(row, "PAUSED") {
		t.Errorf("a limit-paused run must still render PAUSED: %q", row)
	}
	want := "\n  paused on a usage limit at a: " + sanitizedLimitCause + "\n"
	if !strings.Contains(got, want) {
		t.Errorf("runs list must name the recorded cause (%q):\n%s", want, got)
	}
	if strings.ContainsRune(got, 0x1b) {
		t.Errorf("an ESC planted in the cause reached the terminal: %q", got)
	}
}

// TestListRuns_NoRecordNoLimitLine_358 (#358): a run paused at a gate, with no limit
// record, prints exactly the hint it always did.
func TestListRuns_NoRecordNoLimitLine_358(t *testing.T) {
	isolateRunHome(t)
	captureStdout(t, func() { pausedGateFlowRun(t) })
	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), true); err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	if strings.Contains(out.String(), "usage limit") {
		t.Errorf("a gate-paused run must not be named as limit-paused:\n%s", out.String())
	}
}

// TestRunsListExitInFlight_LimitPausedRunKeepsItsExitCode_358 (#358): ADR 0031 §8.2
// — the record changes no exit code. A limit-paused run is not in flight, so
// `runs list --exit-in-flight` exits 0 with the record and without it.
func TestRunsListExitInFlight_LimitPausedRunKeepsItsExitCode_358(t *testing.T) {
	isolateRunHome(t)
	limitPausedRun(t, "run-limit-358", limitCauseMsg)

	exitCode := func() int {
		var code int
		captureStdout(t, func() { code = mainExitCode([]string{"runs", "list", "--exit-in-flight"}) })
		return code
	}
	if code := exitCode(); code != 0 {
		t.Fatalf("with the record: exit code = %d, want 0", code)
	}
	dropLimitPauseKey(t, statePath("run-limit-358"))
	if code := exitCode(); code != 0 {
		t.Fatalf("without the record: exit code = %d, want 0", code)
	}
}

// TestPrintPauseHint_NamesTheRecordedCause_358 (#358): the end-of-leg hint names the
// cause from the record, sanitized, on both branches — with a reset time and
// without one — and keeps the resume command it always printed.
func TestPrintPauseHint_NamesTheRecordedCause_358(t *testing.T) {
	// The error's cause is deliberately different: the record is the source.
	runErr := &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: "stale cause"}
	for _, tc := range []struct {
		name, cause, want string
	}{
		{"with a reset time", escapedLimitCause,
			"\nSession limit reached (resets 5:20pm): " + sanitizedLimitCause + "\nResume after 5:20pm with:\n  oh-my-graph resume run-9 --retry-failed\n"},
		{"without one", "You've reached your Fable limit.\x1b[31m Switch to another model.",
			"\nSession limit reached: You've reached your Fable limit. Switch to another model.\nResume with:\n  oh-my-graph resume run-9 --retry-failed\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			record := &runstate.LimitPause{NodeIDs: []string{"a"}, Cause: tc.cause}
			printDescribedPauseHint(&buf, "run-9", runErr, coordinator.VerifyCommand{}, "", record)
			if buf.String() != tc.want {
				t.Fatalf("hint:\n got %q\nwant %q", buf.String(), tc.want)
			}
		})
	}
}

// TestRun_LimitPauseHintIsSanitized_358 (#358): end to end, the hint a limited leg
// prints names its cause and carries no ESC byte.
func TestRun_LimitPauseHintIsSanitized_358(t *testing.T) {
	isolateRunHome(t)
	out := limitPausedRun(t, "run-limit-358", escapedLimitCause)
	if !strings.Contains(out, "Session limit reached (resets 5:20pm): "+sanitizedLimitCause+"\n") {
		t.Errorf("the end-of-leg hint must name the recorded cause:\n%s", out)
	}
	if strings.ContainsRune(out, 0x1b) {
		t.Errorf("an ESC planted in the cause reached the terminal: %q", out)
	}
}

// TestResume_GateModeOnALimitPausedRunNamesTheCause_358 (#358): `resume --approve`
// on a run paused on a limit, not at a gate, says so from the record —
// sanitized — instead of "is not paused", and still refuses with exit 1.
func TestResume_GateModeOnALimitPausedRunNamesTheCause_358(t *testing.T) {
	isolateRunHome(t)
	limitPausedRun(t, "run-limit-358", escapedLimitCause)

	err := executeResume(parseResumeFlags(t, []string{"run-limit-358", "--approve", "a"}), runner.NewFakeRunner(nil), nil)
	if code := exitCodeForError(err); code != 1 {
		t.Fatalf("exit code = %d, want 1 (err %v)", code, err)
	}
	msg := err.Error()
	want := `run "run-limit-358" is paused on a usage limit at a: ` + sanitizedLimitCause + ", not at a gate (resume it with --retry-failed)"
	if msg != want {
		t.Fatalf("refusal:\n got %q\nwant %q", msg, want)
	}
}
