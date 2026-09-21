package schedule

import (
	"fmt"
	"time"
)

// DefaultHeartbeat is how often a running node prints its still-running line
// when Options.Heartbeat is zero (#284). `watch` uses the same constant for
// its own heartbeat so the two commands cannot drift apart. It is a package
// constant rather than a flag or an env var on purpose: not user-tunable in
// this change.
const DefaultHeartbeat = 60 * time.Second

// heartbeat is one in-flight node's ticking goroutine. stop is closed by
// stopHeartbeat; done is closed by the goroutine on its way out, and
// stopHeartbeat waits on it, which is what makes "no heartbeat line after
// the settlement line" a property of the code rather than of timing.
type heartbeat struct {
	stop chan struct{}
	done chan struct{}
}

// realTicker is the production NewTicker: it wraps time.NewTicker and hands
// back its channel and its Stop.
func realTicker(interval time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(interval)
	return t.C, t.Stop
}

// startHeartbeat begins the sign-of-life for nodeID: on every tick of the
// injected ticker it writes one `… <id>  still running (<elapsed>)` line to
// the progress feed, elapsed measured from start on the injected clock, so
// the last heartbeat and the verdict's duration agree by construction.
//
// The line reuses the `…` glyph the verifying line already carries, because
// it is the same species: an in-flight note, not a transition. It is a
// purely local timer and emits no event (docs/RUN-FEED.md, "There is
// deliberately no heartbeat event").
//
// Calling it for a node that already has a heartbeat is a bug: a node id is
// in flight at most once at a time, and the retry path stops before it
// re-arms (recordRetry).
func (s *Scheduler) startHeartbeat(nodeID string, start time.Time) {
	ticks, stopTicker := s.newTicker(s.heartbeat)
	hb := &heartbeat{stop: make(chan struct{}), done: make(chan struct{})}
	s.heartbeatMu.Lock()
	s.heartbeats[nodeID] = hb
	s.heartbeatMu.Unlock()
	go func() {
		defer close(hb.done)
		defer stopTicker()
		for {
			select {
			case <-hb.stop:
				return
			case <-ticks:
				// A tick that raced the stop loses: once the settlement has
				// begun, the node is not "still running".
				select {
				case <-hb.stop:
					return
				default:
				}
				s.logProgress("… %s  still running (%s)\n", nodeID, formatElapsed(s.since(start)))
			}
		}
	}()
}

// stopHeartbeat ends nodeID's heartbeat and does not return until the
// goroutine has exited, so no heartbeat write can land after it. Idempotent,
// and a no-op for a node that never ticked (a gate): runNode defers it as a
// safety net beside the settlement writers that call it explicitly.
func (s *Scheduler) stopHeartbeat(nodeID string) {
	s.heartbeatMu.Lock()
	hb, ok := s.heartbeats[nodeID]
	if ok {
		delete(s.heartbeats, nodeID)
	}
	s.heartbeatMu.Unlock()
	if !ok {
		return
	}
	close(hb.stop)
	<-hb.done
}

// settleProgress writes a line that ends nodeID's "running" phase — a
// verdict, a retry, a limit pause, a feedback re-arm — after stopping its
// heartbeat, so the ordering "heartbeat never follows the settlement line" is
// structural: every writer of such a line goes through here, never through
// logProgress directly.
func (s *Scheduler) settleProgress(nodeID string, format string, args ...any) {
	s.stopHeartbeat(nodeID)
	s.logProgress(format, args...)
}

// since is time.Since on the injected clock. Every duration the scheduler
// prints or records for a node — heartbeat elapsed and the verdict's
// duration alike — comes from here, so a test's fake clock moves them
// together.
func (s *Scheduler) since(start time.Time) time.Duration {
	return s.now().Sub(start)
}

// formatElapsed renders a node's elapsed time for the heartbeat line, floored
// rather than rounded so the line is true at the moment it prints:
//
//	under 1m        <1m
//	1m to 59m59s    12m
//	1h and above    1h05m, 3h07m
//
// Under DefaultHeartbeat `<1m` is unreachable, but it is defined anyway
// because tests inject shorter clocks, and `watch` reuses this so both
// commands print the identical text.
func formatElapsed(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	minutes := int64(d / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
}
