package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// interruptedPassGraph is two independent nodes, so a session limit on b
// leaves a's PASS recorded beside it (TestResume_RetryFailedAfterAnInterruptedLimitPauseReRunsTheLimitedNode).
const interruptedPassGraph = `{"name":"g","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b"}]}`

// interruptAfterSnapshot runs a first leg of interruptedPassGraph whose Ctrl-C
// lands on its after remote snapshot, and checks the leg ended interrupted:
// exit 1, and the run reading FAIL in runs list and show (#434).
func interruptAfterSnapshot(t *testing.T, fake *runner.FakeRunner, guarded bool) error {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	obs := &afterSnapshotInterrupter{FakeRemoteObserver: originObserver(), interrupt: cancel}
	useRemoteObserver(t, obs, remoteBaseSHA)
	var err error
	captureStdout(t, func() {
		err = executeGraph(ctx, "run-1", mustParse(t, interruptedPassGraph), fake,
			commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: guarded}, nil, 0, "g.yaml", []byte(interruptedPassGraph), false, nil, nil, nil)
	})
	if obs.remotesCalls != 2 {
		t.Fatalf("Remotes ran %d times, want twice: the interrupt must land on the after snapshot", obs.remotesCalls)
	}
	if code := exitCodeForError(err); !errors.Is(err, context.Canceled) || code != 1 {
		t.Fatalf("err = %v (exit %d), want context.Canceled, exit 1", err, code)
	}
	assertRunReads(t, "run-1", runstatus.Fail, runfeed.OutcomeFailed)
	return err
}

// #434: a leg interrupted during its after remote snapshot once every node had
// passed reads FAIL, as its exit 1 says, and `resume --retry-failed` is the
// recovery: it is accepted, launches no node (there is nothing left to run),
// and its leg closes passed, so the run reads PASS as its exit 0 says. A
// guarded run is left as it was: the retry stays the no-op it was, and the run
// keeps reading FAIL, because its failed close may be a forbidden remote
// change an empty leg must not paper over (ADR 0046 §5.1).
func TestResume_RetryFailedOnALegInterruptedAfterEveryNodePassedEndsPass(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		name := map[bool]string{false: "unguarded", true: "guarded"}[guarded]
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0},
				"b": {SessionID: "s-b", Result: "PASS", ExitCode: 0},
			})
			interruptAfterSnapshot(t, fake, guarded)
			loaded := remoteRunSnapshot(t, "run-1").Nodes
			if loaded["a"].Verdict != runstate.VerdictPass || loaded["b"].Verdict != runstate.VerdictPass {
				t.Fatalf("first leg recorded %+v, want a and b PASS", loaded)
			}
			firstCalls := len(fake.Calls())

			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			var err error
			out := captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
			})
			if err != nil {
				t.Fatalf("resume --retry-failed: %v", err)
			}
			if calls := fake.Calls()[firstCalls:]; len(calls) != 0 {
				t.Errorf("resume --retry-failed re-ran %v, want no node: every node had passed", calls)
			}
			if nodes := remoteRunSnapshot(t, "run-1").Nodes; !reflect.DeepEqual(nodes, loaded) {
				t.Errorf("nodes = %+v after the retry, want them as loaded: %+v", nodes, loaded)
			}
			if guarded {
				if !strings.Contains(out, "has no failed nodes to retry") {
					t.Errorf("a guarded run's retry is not the no-op it was:\n%s", out)
				}
				assertRunReads(t, "run-1", runstatus.Fail, runfeed.OutcomeFailed)
				return
			}
			if !strings.Contains(out, "every node passed") {
				t.Errorf("the retry leg does not say why it ran:\n%s", out)
			}
			assertRunReads(t, "run-1", runstatus.Pass, runfeed.OutcomePassed)
		})
	}
}

// #431 review: a Ctrl-C on the after remote snapshot of a leg that had hit the
// session limit ends it interrupted, so ClearPause withdraws the LimitPause and
// the run reads FAIL (exit 1), never PAUSED. The limited node was never
// recorded, so `resume --retry-failed` re-runs it, and only it, and the run
// ends PASS.
func TestResume_RetryFailedAfterAnInterruptedLimitPauseReRunsTheLimitedNode(t *testing.T) {
	isolateRunHome(t)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0},
		"b": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
	})
	err := interruptAfterSnapshot(t, fake, false)
	var limited *schedule.LimitPausedError
	if errors.As(err, &limited) {
		t.Errorf("err %v still reads as the session-limit pause", err)
	}
	snap := remoteRunSnapshot(t, "run-1")
	if snap.LimitPause != nil {
		t.Errorf("state.json limit pause = %+v, want it withdrawn: the leg ended interrupted", snap.LimitPause)
	}
	if snap.Nodes["a"].Verdict != runstate.VerdictPass {
		t.Errorf("a = %+v, want PASS", snap.Nodes["a"])
	}
	if rec, ok := snap.Nodes["b"]; ok {
		t.Errorf("b = %+v, want no record: a limited node never really ran", rec)
	}
	firstCalls := len(fake.Calls())

	fake.SetOutcome("b", runner.NodeOutcome{SessionID: "s-b", Result: "PASS", ExitCode: 0})
	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume --retry-failed: %v", err)
	}
	if calls := fake.Calls()[firstCalls:]; !reflect.DeepEqual(calls, []string{"b"}) {
		t.Errorf("calls after the interrupt = %v, want b re-run alone", calls)
	}
	if b := remoteRunSnapshot(t, "run-1").Nodes["b"]; b.Verdict != runstate.VerdictPass {
		t.Errorf("b = %+v, want PASS", b)
	}
	assertRunReads(t, "run-1", runstatus.Pass, runfeed.OutcomePassed)
}

// #434 review: an unguarded run whose every node passed but whose events.jsonl
// cannot be read has a status the retry cannot know, so `resume --retry-failed`
// fails, naming the run, instead of reporting nothing to retry and exiting 0,
// and it starts no node.
func TestResume_RetryFailedWhenEveryNodePassedButTheStatusCannotBeReadFails(t *testing.T) {
	isolateRunHome(t)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0},
		"b": {SessionID: "s-b", Result: "PASS", ExitCode: 0},
	})
	interruptAfterSnapshot(t, fake, false)
	firstCalls := len(fake.Calls())

	events := filepath.Join(runDirFor("run-1"), runfeed.FileName)
	if err := os.Remove(events); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(events, 0o755); err != nil {
		t.Fatal(err)
	}

	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
	})
	if err == nil {
		t.Fatalf("resume --retry-failed succeeded on a run whose status cannot be read:\n%s", out)
	}
	if msg := err.Error(); !strings.Contains(msg, "cannot read the run's status") || !strings.Contains(msg, `"run-1"`) {
		t.Errorf("err = %v, want it to name run-1 and say its status cannot be read", err)
	}
	if code := exitCodeForError(err); code == 0 {
		t.Errorf("exit code = 0 for %v, want non-zero", err)
	}
	if strings.Contains(out, "has no failed nodes to retry") {
		t.Errorf("the retry reported nothing to retry on a run whose status cannot be read:\n%s", out)
	}
	if calls := fake.Calls()[firstCalls:]; len(calls) != 0 {
		t.Errorf("resume --retry-failed started %v, want no node", calls)
	}
}
