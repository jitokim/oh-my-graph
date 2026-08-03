package main

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// blockingRunner parks every node until release is closed, closing started on
// the first invocation — how a test holds a first leg in flight while racing a
// resume against it.
type blockingRunner struct {
	startedOnce sync.Once
	started     chan struct{}
	release     chan struct{}
}

func (r *blockingRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.startedOnce.Do(func() { close(r.started) })
	<-r.release
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

// TestRun_FirstLegHoldsRunLockAgainstConcurrentResume pins the concurrent-leg
// guard: the `run`/`auto` first leg holds the run's resume.lock for its whole
// duration, so a `resume --retry-failed` raced against the in-flight leg fails
// on the lock (no double-spawn, no state.json/events.jsonl write race) instead
// of opening a second scheduler — and once the leg finishes and releases the
// lock, the same resume proceeds.
func TestRun_FirstLegHoldsRunLockAgainstConcurrentResume(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"lock-race","nodes":[{"id":"a","prompt":"a"}]}`)
	rec := &blockingRunner{started: make(chan struct{}), release: make(chan struct{})}
	runID := "run-lock-race"

	firstLeg := make(chan error, 1)
	go func() {
		firstLeg <- executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "lock-race.yaml", []byte("name: lock-race\n"), nil, nil)
	}()
	<-rec.started

	err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	var held *runstate.LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("resume against an in-flight first leg = %T: %v, want *runstate.LockHeldError", err, err)
	}

	close(rec.release)
	if err := <-firstLeg; err != nil {
		t.Fatalf("first leg should finish cleanly once released, got: %v", err)
	}

	// The finished leg released the lock, so the same resume now proceeds past
	// it — to a clean "nothing to retry" no-op, since the run passed.
	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("resume after the leg released the lock: %v", resumeErr)
	}
	if !strings.Contains(out, "no failed nodes to retry") {
		t.Fatalf("the post-leg resume should reach the no-op message, not stop at the lock:\n%s", out)
	}
}
