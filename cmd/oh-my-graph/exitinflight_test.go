package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// `runs list --exit-in-flight` is the corpus-wide terminal condition a
// supervisor loop waits on. Its whole value is in the exit code, so every
// assertion here OBSERVES a code — 4 or 0 — over a corpus whose status was
// established first by the shared rule (runstatus.Of), never over an empty run
// home. That ordering is the point: exit 0 is the easy answer to return by
// accident, and a test that got it from a corpus with no runs in it would pass
// against an implementation that always returns 0. So each case names the run
// it is asking about, asserts what that run IS, and only then reads the code.
//
// Every fixture is the same one the ADR 0015 tests use: a real event stream
// (StreamWriter), a real snapshot (runstate.Write), a REAL lock, and — for the
// settled corpus — real engine runs through the scripted FakeRunner. No `claude`
// is ever spawned.

// mustStatus is the shared derivation's answer for a fixture, so a test can say
// what its corpus contains before it asks what the exit code says about it.
func mustStatus(t *testing.T, runDir string) runstatus.Status {
	t.Helper()
	status, err := runstatus.Of(runDir)
	if err != nil {
		t.Fatalf("runstatus.Of(%q) returned error: %v", runDir, err)
	}
	return status
}

// TestRunsListExitInFlight_ARunningRunExitsFourAndTheOutputIsUnchanged is the
// signal's presence: a run whose leg is alive must produce exit 4, and the same
// corpus without the flag must still produce exit 0. Both codes are read from
// the SAME fixture in the same test, because that is what proves the flag is
// what changed the answer rather than the corpus.
//
// The byte-for-byte comparison of the two stdouts is the other half of the
// contract: the exit code is the machine channel, so nothing may be added to a
// table ADR 0015 (open question 4) declines to promise anything about.
func TestRunsListExitInFlight_ARunningRunExitsFourAndTheOutputIsUnchanged(t *testing.T) {
	isolateRunHome(t)
	dir := openLegRun(t, runsRoot(), "run-live", true)
	liveLegLock(t, dir)

	if status := mustStatus(t, dir); status != runstatus.Running {
		t.Fatalf("fixture status = %v, want %v — the corpus is not in flight, so the exit code would be about nothing", status, runstatus.Running)
	}

	var signalled, plain int
	withFlag := captureStdout(t, func() {
		signalled = mainExitCode([]string{"runs", "list", "--exit-in-flight"})
	})
	without := captureStdout(t, func() {
		plain = mainExitCode([]string{"runs", "list"})
	})

	if signalled != 4 {
		t.Errorf("`runs list --exit-in-flight` over a %v run = %d, want 4", runstatus.Running, signalled)
	}
	if plain != 0 {
		t.Errorf("`runs list` without the flag = %d, want 0 — no existing script's exit code may change", plain)
	}
	if row := lineContaining(t, withFlag, "run-live"); !strings.Contains(row, runstatus.Running.String()) {
		t.Fatalf("the in-flight run was not listed as %v, so exit 4 came from somewhere else: %q", runstatus.Running, row)
	}
	if withFlag != without {
		t.Errorf("the flag changed the printed output; the exit code is the machine channel and no prose may be added:\n--- with the flag ---\n%s--- without ---\n%s", withFlag, without)
	}
}

// TestRunsListExitInFlight_ASettledCorpusExitsZero is the zero case, and the
// hardest one to assert honestly: the corpus holds two runs that really exist
// and really settled — one PASS through the real engine and one PAUSED at its
// gate — and both are named in the table the same invocation printed. An
// implementation that listed nothing, or that skipped both directories, would
// fail here before the exit code is ever read.
func TestRunsListExitInFlight_ASettledCorpusExitsZero(t *testing.T) {
	isolateRunHome(t)
	completedRun(t, "run-passed",
		`{"name":"done","nodes":[{"id":"a","prompt":"a"}]}`,
		map[string]runner.NodeOutcome{
			"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.01},
		})

	g := mustParse(t, `{"name":"gated","nodes":[{"id":"approve","type":"gate"}]}`)
	pauseErr := executeGraph(context.Background(), "run-paused", g, &capturingRunner{},
		commonRunFlags{inputs: inputFlag{}}, nil, 0, "gated.yaml", []byte("name: gated\n"), false, nil, nil, nil)
	var paused *schedule.PausedError
	if !errors.As(pauseErr, &paused) {
		t.Fatalf("expected the gate fixture to pause, got %T: %v", pauseErr, pauseErr)
	}

	if status := mustStatus(t, runDirFor("run-passed")); status != runstatus.Pass {
		t.Fatalf("fixture status = %v, want %v", status, runstatus.Pass)
	}
	// PAUSED is settled: the leg closed and nothing is working on it, so a loop
	// waiting for "everything is done" must stop rather than wait on a human.
	if status := mustStatus(t, runDirFor("run-paused")); status != runstatus.Paused {
		t.Fatalf("fixture status = %v, want %v", status, runstatus.Paused)
	}

	var code int
	out := captureStdout(t, func() {
		code = mainExitCode([]string{"runs", "list", "--exit-in-flight"})
	})
	if code != 0 {
		t.Errorf("`runs list --exit-in-flight` over a settled corpus = %d, want 0", code)
	}
	if row := lineContaining(t, out, "run-passed"); !strings.Contains(row, runstatus.Pass.String()) {
		t.Errorf("the settled corpus must be the one that was listed: %q", row)
	}
	if row := lineContaining(t, out, "run-paused"); !strings.Contains(row, runstatus.Paused.String()) {
		t.Errorf("the settled corpus must be the one that was listed: %q", row)
	}
}

// TestRunsListExitInFlight_AnAbandonedRunExitsZero is the case that decides
// whether this signal is worth having. An abandoned run's leg is OPEN on the
// event stream forever — a consumer reading events.jsonl alone (which cannot
// flock, docs/RUN-FEED.md) would wait on it until the machine is rebooted. The
// exit code comes from runstatus instead, where ABANDONED is neither settled nor
// in flight, so the loop ends: nothing is working on that run.
func TestRunsListExitInFlight_AnAbandonedRunExitsZero(t *testing.T) {
	isolateRunHome(t)
	dir := openLegRun(t, runsRoot(), "run-dead", true)
	deadLegLock(t, dir)

	if status := mustStatus(t, dir); status != runstatus.Abandoned {
		t.Fatalf("fixture status = %v, want %v", status, runstatus.Abandoned)
	}

	var code int
	out := captureStdout(t, func() {
		code = mainExitCode([]string{"runs", "list", "--exit-in-flight"})
	})
	if code != 0 {
		t.Errorf("`runs list --exit-in-flight` over an %v run = %d, want 0 — an open leg nobody holds is not work in progress", runstatus.Abandoned, code)
	}
	if row := lineContaining(t, out, "run-dead"); !strings.Contains(row, runstatus.Abandoned.String()) {
		t.Fatalf("the abandoned run was not listed as %v, so exit 0 was not about it: %q", runstatus.Abandoned, row)
	}
}

// TestExitCodeForError_InFlightIsItsOwnCode pins 4 in the function that owns the
// mapping, beside the code it must not collide with. Exit 1 is "I could not read
// the corpus", and a loop that saw the two as one would either spin forever on a
// broken run home or declare everything finished over one.
func TestExitCodeForError_InFlightIsItsOwnCode(t *testing.T) {
	if got := exitCodeForError(&runsInFlightError{count: 2}); got != 4 {
		t.Errorf("exitCodeForError(runsInFlightError) = %d, want 4", got)
	}
	if got := exitCodeForError(errors.New(`read runs dir "/nope"`)); got != 1 {
		t.Errorf("a read failure = %d, want 1 — it must stay distinguishable from 4", got)
	}
	if got := exitCodeForError(nil); got != 0 {
		t.Errorf("exitCodeForError(nil) = %d, want 0", got)
	}
}

// TestRunsListFlags_ExitInFlightIsOptInAndParses pins the flag itself: it
// reaches the field that decides the code, and it is off unless typed.
func TestRunsListFlags_ExitInFlightIsOptInAndParses(t *testing.T) {
	if def := newRunsListFlags(); def.exitInFlight {
		t.Error("--exit-in-flight must default to off — exit 0 is the answer every existing invocation gets")
	}
	f := newRunsListFlags()
	if err := f.parse([]string{"--exit-in-flight"}); err != nil {
		t.Fatalf("`runs list --exit-in-flight` must parse: %v", err)
	}
	if !f.exitInFlight {
		t.Error("--exit-in-flight parsed but did not set the field it controls")
	}
}
