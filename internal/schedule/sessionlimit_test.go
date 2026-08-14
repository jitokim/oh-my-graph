package schedule

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// limitedOutcome is the scripted NodeOutcome of a subprocess the subscription
// session limit killed, exactly as CLIRunner classifies it
// (internal/runner/sessionlimit.go).
func limitedOutcome() runner.NodeOutcome {
	return runner.NodeOutcome{
		ExitCode:       1,
		FailureCause:   "You've hit your session limit · resets 5:20pm",
		SessionLimited: true,
	}
}

// TestScheduler_SessionLimitPausesInsteadOfFailing is ADR 0009's core claim
// end to end: a limited node is recorded NOWHERE (no ledger row, no snapshot
// record — it is un-run, not FAILED), its dependents never launch, the run
// returns *LimitPausedError, and the leg closes on the stream as outcome
// "paused" with a detail naming the limit.
func TestScheduler_SessionLimitPausesInsteadOfFailing(t *testing.T) {
	g := mustGraph(t, `
name: limit-pause
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome(), "b": pass("s-b", 0)})
	fr := newFakeRecorder()
	feed, path := newEventStream(t, "limit-pause")
	s, h, led := newHarness(t, fake, Options{Recorder: fr, EventSink: feed})

	err := s.Run(context.Background(), g, h, led)
	var limited *LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected *LimitPausedError, got %T: %v", err, err)
	}
	if len(limited.NodeIDs) != 1 || limited.NodeIDs[0] != "a" {
		t.Fatalf("limited nodes = %v, want [a]", limited.NodeIDs)
	}
	if !strings.Contains(limited.Cause, "resets 5:20pm") {
		t.Fatalf("the error must carry the CLI's own cause (with its reset hint), got %q", limited.Cause)
	}
	if got := fake.InvocationCount("b"); got != 0 {
		t.Errorf("the limited node's dependent ran %d time(s), want 0", got)
	}
	if _, ok := findRecord(led, "a"); ok {
		t.Error("a limited node must not get a ledger row — it is un-run, not FAILED")
	}
	if _, ok := fr.recordFor("a"); ok {
		t.Error("a limited node must not reach the snapshot — resume must see it as never run")
	}

	events := readEventStream(t, path)
	last := events[len(events)-1]
	if last.Type != runfeed.EventRunFinished || last.Outcome != runfeed.OutcomePaused {
		t.Fatalf("the leg must close with outcome paused, got %+v", last)
	}
	if !strings.Contains(last.Detail, "session limit reached at a") {
		t.Fatalf("run_finished detail should name the limit and the node, got %q", last.Detail)
	}
	for _, e := range events {
		if e.Type == runfeed.EventNodeFailed {
			t.Fatalf("no node may be recorded FAILED on a pure limit pause, got %+v", e)
		}
	}
}

// TestScheduler_SessionLimitIgnoresRetryPolicy: an immediate re-spawn meets
// the same limit, so a limited attempt is never retried — not even under a
// retry.on policy that would catch its non-zero exit.
func TestScheduler_SessionLimitIgnoresRetryPolicy(t *testing.T) {
	g := mustGraph(t, `
name: limit-no-retry
nodes:
  - { id: a, prompt: a, retry: { max: 2, on: [nonzero_exit, run_error] } }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome()})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var limited *LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected *LimitPausedError, got %T: %v", err, err)
	}
	if got := fake.InvocationCount("a"); got != 1 {
		t.Fatalf("the limited node was invoked %d time(s), want exactly 1 — no retry against a standing limit", got)
	}
}

// limitDrainRunner is pauseDrainRunner's session-limit counterpart: "slow"
// blocks until the test releases it, and "limit" returns the limited outcome
// only once slow is provably in flight — so the limit always lands with a
// live sibling to drain.
type limitDrainRunner struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (r *limitDrainRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	switch spec.Prompt {
	case "slow":
		r.startOnce.Do(func() { close(r.started) })
		select {
		case <-r.release:
			return runner.NodeOutcome{Result: "PASS", ExitCode: 0, SessionID: "s-slow", TotalCostUSD: 0.02}, nil
		case <-ctx.Done():
			return runner.NodeOutcome{}, ctx.Err()
		}
	case "limit":
		select {
		case <-r.started:
		case <-ctx.Done():
			return runner.NodeOutcome{}, ctx.Err()
		}
		return limitedOutcome(), nil
	default:
		return runner.NodeOutcome{Result: "PASS", ExitCode: 0, SessionID: "s-" + spec.Prompt}, nil
	}
}

// TestScheduler_SessionLimitDrainsInFlightSiblingsInsteadOfCancelling mirrors
// the gate-pause drain proof for ADR 0009: a sibling genuinely in flight when
// the limit hits runs to completion and is recorded as a real PASS — never
// interrupted the way halt-on-fail interrupts one.
func TestScheduler_SessionLimitDrainsInFlightSiblingsInsteadOfCancelling(t *testing.T) {
	g := mustGraph(t, `
name: limit-drain
nodes:
  - { id: limit, prompt: limit }
  - { id: slow, prompt: slow }
`)
	r := &limitDrainRunner{started: make(chan struct{}), release: make(chan struct{})}
	s, h, led := newHarness(t, r, Options{Concurrency: 2})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the independent sibling never started")
	}
	close(r.release)

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the sibling was released")
	}

	var limited *LimitPausedError
	if !errors.As(runErr, &limited) {
		t.Fatalf("expected *LimitPausedError, got %T: %v", runErr, runErr)
	}
	if len(limited.NodeIDs) != 1 || limited.NodeIDs[0] != "limit" {
		t.Fatalf("limited nodes = %v, want [limit]", limited.NodeIDs)
	}
	slowRec, ok := findRecord(led, "slow")
	if !ok {
		t.Fatal("the drained sibling was never recorded in the ledger")
	}
	if slowRec.Verdict != ledger.VerdictPass || slowRec.SessionID != "s-slow" {
		t.Fatalf("the drained sibling must complete as a real PASS, got %+v", slowRec)
	}
}

// TestScheduler_DrainingSiblingsMayJoinThePausedSet: parallel siblings that
// each hit the limit all land in NodeIDs (sorted) — a draining sibling joins
// the pause rather than failing.
func TestScheduler_DrainingSiblingsMayJoinThePausedSet(t *testing.T) {
	g := mustGraph(t, `
name: limit-both
concurrency: 2
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome(), "b": limitedOutcome()})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var limited *LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected *LimitPausedError, got %T: %v", err, err)
	}
	if len(limited.NodeIDs) != 2 || limited.NodeIDs[0] != "a" || limited.NodeIDs[1] != "b" {
		t.Fatalf("limited nodes = %v, want [a b]", limited.NodeIDs)
	}
	if got := len(led.Records()); got != 0 {
		t.Errorf("no node may be recorded on a pure limit pause, ledger holds %d row(s)", got)
	}
}

// TestScheduler_SessionLimitOutranksPrunedFailures: under continue-on-fail a
// real failure prunes and is recorded FAILED, but the run's own verdict is
// the limit pause — unfinished and resumable, not finished-and-failed — so a
// later --retry-failed can clear the failure and re-run both.
func TestScheduler_SessionLimitOutranksPrunedFailures(t *testing.T) {
	g := mustGraph(t, `
name: limit-vs-fail
concurrency: 2
nodes:
  - { id: bad, prompt: bad }
  - { id: limit, prompt: limit }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":   {Result: "boom", ExitCode: 1},
		"limit": limitedOutcome(),
	})
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true})

	err := s.Run(context.Background(), g, h, led)
	var limited *LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected *LimitPausedError to outrank the pruned failure, got %T: %v", err, err)
	}
	badRec, ok := findRecord(led, "bad")
	if !ok || badRec.Verdict != ledger.VerdictFail {
		t.Fatalf("the real failure must still be recorded FAILED, got %+v (ok=%v)", badRec, ok)
	}
	if _, ok := findRecord(led, "limit"); ok {
		t.Error("the limited node must still not get a ledger row")
	}
}
