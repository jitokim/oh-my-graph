package schedule

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// --- heartbeat: the plain-text sign-of-life for a long node (#284) ----------
//
// None of these tests sleeps or depends on real seconds: the ticker is a
// channel the test owns, the clock is a value the test advances, and every
// wait is on a channel (holdRunner.entered, signalWriter.wrote, Run's own
// return).

// neverTicks is an Options.NewTicker whose channel nothing ever sends on.
func neverTicks(time.Duration) (<-chan time.Time, func()) {
	return make(chan time.Time), func() {}
}

// TestScheduler_Heartbeat_HeldNodePrintsStillRunningBeforeItsVerdict is the
// feature: a node held in flight past one tick prints a line naming the node
// and its elapsed time, and that line lands before the node's verdict — and
// never after it, because settlement joins the goroutine before writing.
func TestScheduler_Heartbeat_HeldNodePrintsStillRunningBeforeItsVerdict(t *testing.T) {
	g := mustGraph(t, `
name: heartbeat
nodes:
  - { id: a, prompt: a }
`)
	hold := &holdRunner{
		entered:  make(chan struct{}),
		release:  make(chan struct{}),
		outcomes: []runner.NodeOutcome{pass("s-a", 0.05)},
	}
	clock := &fakeClock{t: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
	ticks := make(chan time.Time, 1)
	feed := newSignalWriter()
	s, h, led := newHarness(t, hold, Options{
		ProgressWriter: feed,
		Now:            clock.Now,
		NewTicker:      func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	<-hold.entered
	clock.Advance(12 * time.Minute)
	ticks <- clock.Now()
	feed.waitFor(t, "… a  still running (12m)\n")
	close(hold.release)
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	settled := feed.String()
	want := "▶ a  running…\n" +
		"… a  still running (12m)\n" +
		"✓ a  PASS  $0.0500  12m0s\n"
	if settled != want {
		t.Errorf("progress feed after the run:\n got %q\nwant %q", settled, want)
	}

	// A tick after settlement changes nothing: stopHeartbeat joined the
	// goroutine before the verdict line, so nobody is left to receive it.
	ticks <- clock.Now()
	if after := feed.String(); after != settled {
		t.Errorf("a tick after the verdict added to the feed:\n got %q\nwas %q", after, settled)
	}
}

// TestScheduler_Heartbeat_NodeSettledBeforeAnyTickPrintsNoExtraLine pins the
// ordinary case: a node that finishes before its first tick leaves the feed
// exactly as it was before the heartbeat existed.
func TestScheduler_Heartbeat_NodeSettledBeforeAnyTickPrintsNoExtraLine(t *testing.T) {
	g := mustGraph(t, `
name: quick
nodes:
  - { id: a, prompt: a }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0.05)})
	clock := &fakeClock{t: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
	ticks := make(chan time.Time, 1)
	feed := newSignalWriter()
	s, h, led := newHarness(t, fake, Options{
		ProgressWriter: feed,
		Now:            clock.Now,
		NewTicker:      func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	settled := feed.String()
	want := "▶ a  running…\n✓ a  PASS  $0.0500  0s\n"
	if settled != want {
		t.Errorf("progress feed:\n got %q\nwant %q", settled, want)
	}

	ticks <- clock.Now()
	if after := feed.String(); after != settled {
		t.Errorf("a tick after the run added to the feed:\n got %q\nwas %q", after, settled)
	}
}

// TestScheduler_ProgressWriter_FeedIsExactlyTheLifecycleLines is the first
// pin on the feed's line count and order, not just its contents: two
// sequential nodes produce exactly their four `▶`/`✓` lines, in order, with
// nothing added between them.
func TestScheduler_ProgressWriter_FeedIsExactlyTheLifecycleLines(t *testing.T) {
	g := mustGraph(t, `
name: exact-feed
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.05),
		"b": pass("s-b", 0.05),
	})
	feed := newSignalWriter()
	s, h, led := newHarness(t, fake, Options{ProgressWriter: feed, NewTicker: neverTicks})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	lines := strings.Split(strings.TrimSuffix(feed.String(), "\n"), "\n")
	wantPrefixes := []string{
		"▶ a  running…",
		"✓ a  PASS  $0.0500  ",
		"▶ b  running…",
		"✓ b  PASS  $0.0500  ",
	}
	if len(lines) != len(wantPrefixes) {
		t.Fatalf("feed has %d lines, want %d:\n%s", len(lines), len(wantPrefixes), feed.String())
	}
	for i, prefix := range wantPrefixes {
		if !strings.HasPrefix(lines[i], prefix) {
			t.Errorf("feed line %d = %q, want prefix %q", i, lines[i], prefix)
		}
	}
}

// TestScheduler_Heartbeat_RetryKeepsOneClock proves a retried attempt gets its
// own heartbeat window but not its own clock: elapsed after the retry still
// counts from the node's first `▶`, the same start the verdict's duration
// prints, and the retry line itself is never followed by a stale heartbeat.
func TestScheduler_Heartbeat_RetryKeepsOneClock(t *testing.T) {
	g := mustGraph(t, `
name: retry-heartbeat
nodes:
  - id: flaky
    prompt: flaky
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	clock := &fakeClock{t: time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)}
	hold := &holdRunner{
		entered: make(chan struct{}),
		release: make(chan struct{}),
		holdAt:  1,
		outcomes: []runner.NodeOutcome{
			{Result: "NOPE", ExitCode: 0},
			pass("s-final", 0.05),
		},
		// The first attempt takes five minutes before it fails.
		onAttempt: func(i int) {
			if i == 0 {
				clock.Advance(5 * time.Minute)
			}
		},
	}
	ticks := make(chan time.Time, 1)
	feed := newSignalWriter()
	s, h, led := newHarness(t, hold, Options{
		ProgressWriter: feed,
		Now:            clock.Now,
		NewTicker:      func(time.Duration) (<-chan time.Time, func()) { return ticks, func() {} },
	})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	<-hold.entered
	// Seven more minutes into the retried attempt: twelve since the first ▶.
	clock.Advance(7 * time.Minute)
	ticks <- clock.Now()
	feed.waitFor(t, "… flaky  still running (12m)\n")
	close(hold.release)
	if err := <-done; err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := "▶ flaky  running…\n" +
		"↻ flaky  retry\n" +
		"… flaky  still running (12m)\n" +
		"✓ flaky  PASS  $0.0500  12m0s\n"
	if got := feed.String(); got != want {
		t.Errorf("progress feed:\n got %q\nwant %q", got, want)
	}
}

// TestScheduler_Heartbeat_GateNeverTicks proves a gate node creates no
// ticker at all: waiting on a human is not "running". The count of tickers
// made is the evidence, because a non-event cannot be waited for.
func TestScheduler_Heartbeat_GateNeverTicks(t *testing.T) {
	g := mustGraph(t, `
name: gate-heartbeat
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0)})
	var tickers atomic.Int32
	feed := newSignalWriter()
	s, h, led := newHarness(t, fake, Options{
		ProgressWriter: feed,
		NewTicker: func(d time.Duration) (<-chan time.Time, func()) {
			tickers.Add(1)
			return make(chan time.Time), func() {}
		},
	})

	err := s.Run(context.Background(), g, h, led)
	var paused *PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected *PausedError, got %T: %v", err, err)
	}
	if n := tickers.Load(); n != 1 {
		t.Errorf("tickers created = %d, want 1 (one for node a, none for the gate)", n)
	}
	if got := feed.String(); !strings.Contains(got, "⏸ approve  gate paused\n") || strings.Contains(got, "still running") {
		t.Errorf("gate feed should pause and never tick:\n%q", got)
	}
}

// TestScheduler_Heartbeat_DefaultsResolve pins the zero-value contract: a
// zero Heartbeat means DefaultHeartbeat, and nil NewTicker/Now mean the real
// time package.
func TestScheduler_Heartbeat_DefaultsResolve(t *testing.T) {
	s := NewScheduler(runner.NewFakeRunner(nil), Options{})
	if s.heartbeat != DefaultHeartbeat {
		t.Errorf("heartbeat = %v, want DefaultHeartbeat (%v)", s.heartbeat, DefaultHeartbeat)
	}
	if s.newTicker == nil || s.now == nil {
		t.Error("newTicker and now must default to the real time package, not nil")
	}
	if DefaultHeartbeat != 60*time.Second {
		t.Errorf("DefaultHeartbeat = %v, want 60s", DefaultHeartbeat)
	}
	custom := NewScheduler(runner.NewFakeRunner(nil), Options{Heartbeat: 5 * time.Second})
	if custom.heartbeat != 5*time.Second {
		t.Errorf("heartbeat = %v, want the injected 5s", custom.heartbeat)
	}
}

// TestFormatElapsed pins the floor-not-round rendering the heartbeat line
// uses, so the line is true at the moment it prints.
func TestFormatElapsed(t *testing.T) {
	cases := []struct {
		in   time.Duration
		want string
	}{
		{0, "<1m"},
		{59 * time.Second, "<1m"},
		{time.Minute, "1m"},
		{12*time.Minute + 34*time.Second, "12m"},
		{59*time.Minute + 59*time.Second, "59m"},
		{time.Hour, "1h00m"},
		{time.Hour + 5*time.Minute, "1h05m"},
		{3*time.Hour + 7*time.Minute + 59*time.Second, "3h07m"},
	}
	for _, tc := range cases {
		if got := formatElapsed(tc.in); got != tc.want {
			t.Errorf("formatElapsed(%v) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
