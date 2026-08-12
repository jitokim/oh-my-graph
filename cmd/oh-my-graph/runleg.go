package main

import (
	"fmt"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// runLeg is one leg of a run as a VALUE: the run's flock and its event-stream
// writer, taken and given back together, in the order the abandoned-run
// derivation rests on (acquireRunLock's invariant — the lock before the first
// event, and still held after the last).
//
// It exists because ADR 0023 moved the leg's OPEN earlier than its close. Until
// then both sat inside executeGraph and the invariant was enforced by shape:
// `defer release()` above `defer feed.Close()`, LIFO doing the rest. An auto
// run's leg now opens before the planner call — which is what makes the
// planning phase visible at all — and closes inside executeGraph on the happy
// path, so its lifetime runs through another package's control flow
// (coordinator.RunGoal's) and no `defer` can bracket it at its point of use.
//
// The failure mode that makes this worth a type rather than care: a leg left
// open by a process that then exits NORMALLY reads ABANDONED, and ABANDONED
// prints runstatus.OrphanWarning — "a `claude` subprocess started by the dead
// leg may still be running and spending". A leaked leg would therefore raise a
// FALSE DOUBLE-SPEND ALARM on a clean exit, degrading the one and only
// mitigation ADR 0015 accepted for its largest cost. A warning that cries wolf
// on healthy runs is worth less on the runs that need it.
//
// So Close is IDEMPOTENT, and the bracket is restored lexically one scope up:
// the CLI defers a close-if-still-open over the whole RunGoal call (ADR 0023
// §2.7). Enumerating RunGoal's exits instead was considered and is the weaker
// fix — the list would be correct only until the next change to
// coordinator/goal.go, in a package that cannot see the leg.
type runLeg struct {
	runID string
	feed  *runfeed.StreamWriter
	// release gives the flock back. Held rather than deferred, because this
	// leg's lifetime is not lexical.
	release func() error
	closed  bool
}

// openRunLeg takes the run's lock and opens its event stream, creating the run
// directory on the way (runstate.AcquireLock does, 0o700). It writes NO event:
// which event opens the leg is the caller's business — the scheduler emits its
// own untagged run_started, and a planning phase calls beginPlanning below.
//
// Failing to open the stream is fatal here, as it always has been in
// executeGraph and for the same reason: a run that never had a stream at all is
// silently invisible to every consumer of the run directory, which is exactly
// the condition #163 reports. The lock is given back before returning, so a
// failed open leaves no half-taken leg behind.
func openRunLeg(runID string) (*runLeg, error) {
	runDir := runDirFor(runID)
	release, err := acquireRunLock(filepath.Join(runDir, lockFileName))
	if err != nil {
		return nil, err
	}
	feed, err := runfeed.NewStreamWriter(filepath.Join(runDir, runfeed.FileName), runID)
	if err != nil {
		release()
		return nil, fmt.Errorf("prepare run event stream: %w", err)
	}
	return &runLeg{runID: runID, feed: feed, release: release}, nil
}

// beginPlanning writes the run_started that opens a PLANNING phase — the one
// event ADR 0023 adds to the stream, and the only trace a planner call leaves.
// ABANDONED needed no event because it was derivable from bytes already on
// disk; PLANNING is derivable from nothing, because a planner call in progress
// writes nothing at all unless the engine writes it (ADR 0023 §2.3).
//
// A failure here is fatal, unlike a mid-run emit failure (which the scheduler
// downgrades to a progress warning). The trade is different at this instant:
// nothing has been spent yet, so refusing costs the user a retry, while
// continuing would leave a directory holding a lock and an EMPTY stream — a
// live run that every reader would report as a settled FAIL, which is worse
// than the invisibility this whole change removes.
func (l *runLeg) beginPlanning() error {
	if err := l.feed.Emit(runfeed.Event{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning}); err != nil {
		return fmt.Errorf("open the run's planning phase: %w", err)
	}
	return nil
}

// Close ends the leg: it emits a run_finished carrying outcome when one is
// given, closes the stream writer, and releases the lock — in that order, which
// is the second half of the ordering invariant (the last event is on disk
// before the lock goes back, so no reader ever sees an open leg beside a lock
// nobody holds).
//
// An EMPTY outcome means "the stream has already been closed by whoever wrote
// its last event" — the happy path, where the scheduler emitted its own
// run_finished. Passing runfeed.OutcomeFailed is what the sweep does for a leg
// that got no closing event at all.
//
// It is IDEMPOTENT, and nil-safe, and both are load-bearing rather than
// defensive: the happy path's close and the deferred close-if-open sweep
// compose without the sweep needing to know what happened, and a caller that
// never opened a leg (`chat`, `--plan-only`) can defer the same line.
func (l *runLeg) Close(outcome string) error {
	if l == nil || l.closed {
		return nil
	}
	l.closed = true

	var firstErr error
	if outcome != "" {
		if err := l.feed.Emit(runfeed.Event{Type: runfeed.EventRunFinished, Outcome: outcome}); err != nil {
			firstErr = err
		}
	}
	if err := l.feed.Close(); err != nil && firstErr == nil {
		firstErr = err
	}
	if err := l.release(); err != nil && firstErr == nil {
		firstErr = err
	}
	return firstErr
}
