package main

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// startInterrupter is a NodeRunner whose first node is interrupted as it
// starts: it sends this process SIGINT, as a Ctrl-C at the terminal, and
// returns once the leg's context is cancelled, the node never having run.
type startInterrupter struct {
	t *testing.T
}

func (s startInterrupter) Run(ctx context.Context, _ runner.NodeInvocation) (runner.NodeOutcome, error) {
	sigint(s.t)()
	<-ctx.Done()
	return runner.NodeOutcome{}, ctx.Err()
}

func (startInterrupter) CheckCLIAvailable() error { return nil }

// #433: a resume --approve whose Ctrl-C lands after the approval was saved —
// the pause cleared, the gate passed — as the first node of the leg starts
// ends interrupted: exit 1, and the run reads FAIL, which already agreed since
// #434. runs list and show then point at --retry-failed and never at the
// --approve the gate no longer takes; that --approve says the gate was already
// approved and names --retry-failed; and --retry-failed passes the approved
// gate and finishes.
func TestResume_AnInterruptAfterTheApprovalWasSavedPointsToRetryFailed(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":    {Result: "PASS", ExitCode: 0},
		"ship": {Result: "PASS", ExitCode: 0},
	})
	g := mustParse(t, `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the first leg to pause, got %v", err)
	}

	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), startInterrupter{t}, nil)
	})
	if code := exitCodeForError(err); !errors.Is(err, context.Canceled) || errors.As(err, &paused) || code != 1 {
		t.Fatalf("err = %v (exit %d), want an interrupted leg, context.Canceled, exit 1", err, code)
	}
	if strings.Contains(out, "Paused at gate") || strings.Contains(out, "--approve approve") {
		t.Errorf("the interrupted leg printed a pause hint:\n%s", out)
	}
	snap := remoteRunSnapshot(t, "run-1")
	if snap.Gate.PausedAt != "" || snap.Gate.Decisions["approve"] != runstate.GateApprove {
		t.Fatalf("gate = %+v, want approve saved as approved and no pause", snap.Gate)
	}
	assertRunReads(t, "run-1", runstatus.Fail, runfeed.OutcomeFailed)

	hint := "run run-1 is FAIL and not paused: gate \"approve\" was already approved, so continue it with `oh-my-graph resume run-1 --retry-failed` (not --approve)."
	var list, warn strings.Builder
	if err := listRuns(&list, &warn, runsRoot(), false); err != nil {
		t.Fatalf("runs list: %v", err)
	}
	if !strings.Contains(list.String(), "\n"+hint+"\n") || strings.Contains(list.String(), "--approve <gate-id>") {
		t.Errorf("runs list does not point at --retry-failed alone, want %q:\n%s", hint, list.String())
	}
	var show strings.Builder
	if err := showRun(&show, runDirFor("run-1"), "run-1"); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.HasSuffix(show.String(), "\n\n"+hint+"\n") {
		t.Errorf("show does not end with %q:\n%s", hint, show.String())
	}

	firstCalls := len(fake.Calls())
	err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	want := "run \"run-1\" is not paused: gate \"approve\" was already approved, so continue it with `oh-my-graph resume run-1 --retry-failed`"
	if err == nil || err.Error() != want {
		t.Errorf("resume --approve = %v, want %q", err, want)
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("resume --approve exit = %d, want 1, as the not-paused refusal", code)
	}
	if calls := fake.Calls(); len(calls) != firstCalls {
		t.Errorf("the refused --approve ran %v", calls[firstCalls:])
	}

	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume --retry-failed: %v", err)
	}
	if !slices.Contains(fake.Calls()[firstCalls:], "ship") {
		t.Errorf("calls = %v, want ship run past the approved gate", fake.Calls())
	}
	if snap := remoteRunSnapshot(t, "run-1"); snap.Nodes["ship"].Verdict != runstate.VerdictPass {
		t.Errorf("ship = %+v, want PASS", snap.Nodes["ship"])
	}
	assertRunReads(t, "run-1", runstatus.Pass, runfeed.OutcomePassed)

	// Finished, the run gets no hint, and an --approve has nothing to continue.
	list.Reset()
	if err := listRuns(&list, &warn, runsRoot(), false); err != nil {
		t.Fatalf("runs list: %v", err)
	}
	if strings.Contains(list.String(), "--retry-failed") {
		t.Errorf("a finished run still points at --retry-failed:\n%s", list.String())
	}
	err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	if want := `run "run-1" is not paused: gate "approve" was already approved (nothing to resume)`; err == nil || err.Error() != want {
		t.Errorf("resume --approve on the finished run = %v, want %q", err, want)
	}
}

func TestApprovedRetryHint_NamesEveryApprovedGate(t *testing.T) {
	got := approvedRetryHint("run-1", []string{"design", "ship"})
	want := "run run-1 is FAIL and not paused: gates \"design\", \"ship\" were already approved, so continue it with `oh-my-graph resume run-1 --retry-failed` (not --approve)."
	if got != want {
		t.Errorf("hint = %q, want %q", got, want)
	}
}
