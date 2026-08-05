package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
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
		firstLeg <- executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "lock-race.yaml", []byte("name: lock-race\n"), false, nil, nil)
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

// --- the lock brackets the leg's event stream ---------------------------------

// legStream is what a leg's events.jsonl said at one instant: how many events
// it held and the type of the last one. A missing file is zero events.
type legStream struct {
	events int
	last   string
}

func readLegStream(t *testing.T, path string) legStream {
	t.Helper()
	var seen legStream
	err := runfeed.Walk(path, func(ev runfeed.Event) error {
		seen.events++
		seen.last = string(ev.Type)
		return nil
	})
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read the leg's event stream: %v", err)
	}
	return seen
}

// lockProbingRunner probes the run's lock from INSIDE the only node's Run —
// strictly mid-run, with the leg's opening event already on disk — and records
// what it saw for the test to judge once the run has ended.
type lockProbingRunner struct {
	lockPath string
	feedPath string

	liveness runstate.Liveness
	stream   legStream
	acquire  error
}

func (r *lockProbingRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.liveness = runstate.ProbeLock(r.lockPath)
	var seen legStream
	_ = runfeed.Walk(r.feedPath, func(ev runfeed.Event) error {
		seen.events++
		seen.last = string(ev.Type)
		return nil
	})
	r.stream = seen
	if release, err := runstate.AcquireLock(r.lockPath); err != nil {
		r.acquire = err
	} else {
		release()
	}
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

// TestRunLeg_LockBracketsTheEventStream pins the ordering invariant ADR 0015
// §2 names — "a leg must hold the flock before it writes its first event, and
// must still hold it after it writes its last" — which the abandoned-run
// derivation (open leg AND free lock ⇒ abandoned) silently rests on. Today the
// call site satisfies it by arrangement: the acquire sits above
// runfeed.NewStreamWriter, and `defer` LIFO puts the release after the feed's
// close. Move the acquire below the writer and every run in the world would
// read abandoned for its first instants — with nothing else failing, which is
// exactly why this test exists.
//
// It judges the leg at the two instants that matter, from inside the lock's
// own acquire/release, plus one from inside a node:
//
//	acquire  → the leg has written no events yet (nothing to misread)
//	mid-run  → the lock reads HELD while the leg's opening event stands
//	release  → the leg's last event is already run_finished, lock still HELD
func TestRunLeg_LockBracketsTheEventStream(t *testing.T) {
	isolateRunHome(t)
	runID := "run-lock-brackets"
	runDir := runDirFor(runID)
	lockPath := filepath.Join(runDir, lockFileName)
	feedPath := filepath.Join(runDir, runfeed.FileName)

	var atAcquire, atRelease legStream
	var livenessAtRelease runstate.Liveness
	var acquired, releasedOnce bool

	previous := acquireRunLock
	t.Cleanup(func() { acquireRunLock = previous })
	acquireRunLock = func(path string) (func() error, error) {
		atAcquire = readLegStream(t, feedPath)
		acquired = true
		release, err := previous(path)
		if err != nil {
			return nil, err
		}
		return func() error {
			atRelease = readLegStream(t, feedPath)
			// Probed from a second descriptor: the leg still holds the lock at
			// the instant its closing event is already on disk.
			livenessAtRelease = runstate.ProbeLock(path)
			releasedOnce = true
			return release()
		}, nil
	}

	g := mustParse(t, `{"name":"lock-brackets","nodes":[{"id":"a","prompt":"a"}]}`)
	rec := &lockProbingRunner{lockPath: lockPath, feedPath: feedPath}
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "lock-brackets.yaml", []byte("name: lock-brackets\n"), false, nil, nil)
	if err != nil {
		t.Fatalf("executeGraph: %v", err)
	}
	if !acquired || !releasedOnce {
		t.Fatalf("the leg must take and give back the run lock (acquired=%v, released=%v)", acquired, releasedOnce)
	}

	// Before the first event: the lock is taken first, so there is no window in
	// which an open leg stands beside a lock nobody holds.
	if atAcquire.events != 0 {
		t.Errorf("the leg had already written %d event(s) when it took the lock; the lock must come first", atAcquire.events)
	}

	// Mid-run: the leg's opening event is on disk AND the lock reads held —
	// the exact pair the derivation reads as "in flight".
	if rec.stream.events == 0 || rec.stream.last == "" {
		t.Fatalf("mid-run the leg's stream should already carry its opening event, got %+v", rec.stream)
	}
	if rec.liveness != runstate.LivenessHeld {
		t.Errorf("mid-run ProbeLock = %v, want %v — a live run must never read as abandoned", rec.liveness, runstate.LivenessHeld)
	}
	var held *runstate.LockHeldError
	if !errors.As(rec.acquire, &held) {
		t.Errorf("mid-run AcquireLock = %v, want *runstate.LockHeldError — a second leg must be refused", rec.acquire)
	}

	// After the last event: run_finished is already written when the lock goes
	// back, and the lock is still held at that instant.
	if want := string(runfeed.EventRunFinished); atRelease.last != want {
		t.Errorf("the leg's last event when it released the lock = %q, want %q — the release must come after the last event", atRelease.last, want)
	}
	if atRelease.events <= atAcquire.events {
		t.Errorf("the leg wrote %d event(s) inside the lock, want more than the %d it started with", atRelease.events, atAcquire.events)
	}
	if livenessAtRelease != runstate.LivenessHeld {
		t.Errorf("ProbeLock at the instant of release = %v, want %v", livenessAtRelease, runstate.LivenessHeld)
	}

	// And once the leg is over, the lock is free beside a closed leg: the run
	// is settled, not abandoned — the derivation never consults the lock here,
	// but the file it left behind is the handle a later resume takes.
	if got := runstate.ProbeLock(lockPath); got != runstate.LivenessFree {
		t.Errorf("ProbeLock after the leg finished = %v, want %v", got, runstate.LivenessFree)
	}
	if _, err := os.Stat(lockPath); err != nil {
		t.Errorf("the finished leg must leave its lock file behind, not unlink it: %v", err)
	}
}
