package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"sync"
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// watchPollInterval is how long the tail loop sleeps at end-of-stream before
// re-reading — long enough not to spin, short enough that a new event appears
// on screen effectively as it lands.
const watchPollInterval = 200 * time.Millisecond

// runWatch is the `watch` subcommand: parse argv and tail one run's event
// stream (events.jsonl) as plain formatted text, one line per event, following
// appended lines until the run finishes or the user interrupts. It is
// read-only over the run directory — the deliberate plain-text stopgap before
// any richer live view, not a TUI.
func runWatch(args []string) error {
	// A dash-prefixed argument is a flag, not a run id (argslot.go).
	if err := flagInPositionalSlot(args, "watch"); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("watch: missing run id (usage: oh-my-graph watch <run-id>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("watch: unexpected argument %q (usage: oh-my-graph watch <run-id>)", args[1])
	}
	runID := args[0]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return watchRunWith(ctx, os.Stdout, os.Stderr, runDirFor(runID), runID, watchPollInterval, productionWatchHeartbeat())
}

// watchRun tails runDir's events.jsonl onto w: every already-written event is
// printed immediately, then the stream is followed tail -f style (poll is the
// end-of-stream re-read interval). It returns nil when it prints the
// run_finished event — the event that closes a leg, so a paused run's watch
// ends at its "paused" outcome — or when ctx is cancelled (Ctrl-C), which is
// the normal way to stop watching, not a failure. A stream that ends without a
// run_finished is checked ONCE at startup, against the run's lock: a leg whose
// lock is free is abandoned, its stream will never get another line, and the
// tail is refused rather than hung (ADR 0015 §4). It deliberately does not gain
// an idle-time probe for a run that dies mid-watch — runfeed.Follow has no idle
// hook, and adding one would push liveness into the pure stream reader — so
// that one hang remains, and is stated rather than fixed. The still-running
// heartbeat (#284, watchTail below) does not change that stance: it is a timer
// over what this tail has ALREADY printed, reads nothing, and claims nothing
// the `▶ running…` line did not already claim.
//
// watchRun is the production-shaped entry (its call sites are many); the
// heartbeat's ticker and clock are injected through watchRunWith.
//
// An unknown run id — no run directory on disk — is a distinct, clearly worded
// error rather than a raw file-not-found, because it is the one failure the
// user causes by mistyping an id. A directory holding a SNAPSHOT but no event
// stream is a different failure and gets its own refusal: that run is real and
// `show` can still read it, there is simply nothing to tail.
func watchRun(ctx context.Context, w, warnW io.Writer, runDir, runID string, poll time.Duration) error {
	return watchRunWith(ctx, w, warnW, runDir, runID, poll, watchHeartbeat{})
}

// watchRunWith is watchRun with the heartbeat's interval, ticker and clock
// injected (hb's zero value resolves to the production ones), so a test can
// push a tick and set "now" without sleeping.
func watchRunWith(ctx context.Context, w, warnW io.Writer, runDir, runID string, poll time.Duration, hb watchHeartbeat) error {
	feedPath := filepath.Join(runDir, runfeed.FileName)

	// A run directory holding a SNAPSHOT but NO event stream would otherwise get
	// two answers about itself one line apart: Spoken is true on the snapshot
	// alone, so the status line below announces PASS or FAIL, and then Follow's
	// fs.ErrNotExist renders that very same directory as an unknown run at the
	// bottom. It is neither — it is a real run, readable with `show`, that
	// simply has nothing to tail — so it is refused here, before anything is
	// announced. A directory with neither file is the other case and keeps the
	// unknown-run error below: it has said nothing, so it has no status to
	// announce either (runstatus.Spoken, ADR 0023 §2.1.1), and one answer is
	// exactly what it gets.
	if _, err := os.Stat(feedPath); errors.Is(err, fs.ErrNotExist) && hasSnapshot(runDir) {
		return fmt.Errorf("run %q has no event stream at %s: there is nothing to tail — read what it recorded with `oh-my-graph show %s`", runID, feedPath, runID)
	}

	// The shared derivation (runstatus.Of), so `watch` refuses exactly the runs
	// `runs list` calls ABANDONED and the dashboard paints abandoned.
	// Gather, not Of, for the reason `runs list` and `show` use it: a directory
	// whose stream has said nothing has no status to announce (runstatus.Spoken,
	// ADR 0023 §2.1.1), and here it is also about to be the unknown-run error
	// below — an announced FAIL one line above "unknown run" would be two
	// answers about one directory.
	//
	// A directory the derivation cannot READ used to fall through in silence,
	// on the reasoning that the damage was somebody else's to report: a missing
	// stream is the mistyped-id error below, and a stream this binary refuses is
	// Follow's. That covered the wrong half. The damage this corpus actually
	// holds is in the SNAPSHOT — 261 of 325 directories on the measuring
	// machine, every one a version-2 snapshot — which Follow never opens and
	// therefore never mentions, so the status line simply vanished with nothing
	// on screen saying why. It is named here now, in runstatus.Unreadable's
	// words: the same sentence `runs list --show-skipped` and `show` print about
	// the same directory. On warnW, because stdout is the tail itself; and the
	// tail still runs, because an unreadable snapshot says nothing about whether
	// the stream can be followed. Where the damage IS in the stream, Follow's
	// own line follows this one and says something different — this one is about
	// the status, that one about rendering the events.
	facts, err := runstatus.Gather(runDir)
	switch {
	case err != nil:
		fmt.Fprintln(warnW, runstatus.Unreadable(runID, err))
	case runstatus.Spoken(facts):
		status := runstatus.Probe(runDir, facts)
		if status == runstatus.Abandoned {
			fmt.Fprintln(warnW, runstatus.Hint(runID, hasSnapshot(runDir)))
			return fmt.Errorf("run %q is abandoned: nothing will ever be appended to its event stream, so there is nothing to tail", runID)
		}
		// The word this tail is heading toward, printed before the first event
		// rather than inferred from what scrolls past (ADR 0023 §2.6). It is
		// what tells a watcher whether the silence they are about to sit in is
		// a planner call, a running node, or a stream that is already over —
		// the three cases that look identical from a tail alone.
		//
		// The clause after it is runstatus.InFlightClause over this one run —
		// the same words `runs list` puts in its coverage line and `show` on
		// its header, so an operator learns one sentence and reads it on
		// whichever surface they opened. Here it is the answer to the question
		// a tail asks by existing: is anything going to arrive? A settled run
		// says "0 in flight" and the watcher stops waiting.
		fmt.Fprintf(w, "run %s is %s; %s.\n", runID, status, runstatus.InFlightClause(status))
	}

	// The tail loop itself is runfeed.Follow — the same reader `serve`'s SSE
	// endpoint streams through (via its FollowWait variant), so the two
	// consumers can never drift on line framing, the shared line cap, or
	// partial-final-line tolerance. watch deliberately does NOT wait for a
	// stream to be created: here a missing stream means a mistyped run id,
	// which must be the unknown-run error below, not an endless wait. watch
	// keeps only its own interpretation: formatting, the malformed-line
	// warning, and stopping at run_finished.
	//
	// Every event line goes through tail.print, which also keeps the in-flight
	// set the heartbeat ticks over. The heartbeat starts here, after the
	// status line and every early refusal above, and is stopped — joined, not
	// just signalled — when Follow returns for any reason: run_finished, ctx
	// cancel, or an error.
	tail := newWatchTail(w, hb)
	tail.start()
	defer tail.stop()
	warnedSchema := false
	err = runfeed.Follow(ctx, feedPath, poll, func(line []byte) (bool, error) {
		var event runfeed.Event
		if err := json.Unmarshal(line, &event); err != nil {
			fmt.Fprintf(warnW, "WARNING: skipping malformed event line: %v\n", err)
			return false, nil
		}
		if event.Schema > runfeed.Schema && !warnedSchema {
			warnedSchema = true
			fmt.Fprintf(warnW, "WARNING: stream schema %d is newer than this build understands (%d); some events may render generically\n", event.Schema, runfeed.Schema)
		}
		tail.print(event)
		return event.Type == runfeed.EventRunFinished, nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unknown run %q: no event stream at %s (see the run ids under %s)", runID, feedPath, filepath.Dir(runDir))
	}
	return err
}

// watchHeartbeat is the injection point for `watch`'s still-running line:
// how often it ticks, where the ticks come from, and what "now" is. The zero
// value means production (schedule.DefaultHeartbeat, time.NewTicker,
// time.Now); a test hands in a channel it owns and a clock it sets.
type watchHeartbeat struct {
	Interval  time.Duration
	NewTicker func(time.Duration) (<-chan time.Time, func())
	Now       func() time.Time
}

// productionWatchHeartbeat is what runWatch passes: the same interval the
// scheduler prints on, so a watched run ticks exactly as often as the run's
// own terminal does.
func productionWatchHeartbeat() watchHeartbeat {
	return watchHeartbeat{Interval: schedule.DefaultHeartbeat, NewTicker: realWatchTicker, Now: time.Now}
}

// realWatchTicker wraps time.NewTicker into the injectable shape.
func realWatchTicker(interval time.Duration) (<-chan time.Time, func()) {
	t := time.NewTicker(interval)
	return t.C, t.Stop
}

// watchTail owns the writer the tail prints on and the in-flight set the
// heartbeat ticks over, under ONE mutex, so that a heartbeat line and an
// event line never interleave and — the property that matters — a node is
// removed from the set in the same critical section that prints its terminal
// line. A heartbeat therefore never follows a node's settlement in the output.
//
// The clock is local and there is no heartbeat event on the stream, on
// purpose. docs/RUN-FEED.md states the rule under "Event types": the stream
// records intent, not process liveness, and its event-type set is closed per
// schema — a heartbeat event would carry no state transition and would bump
// the schema for every consumer to say something each of them can already
// derive, because every event carries `ts`. So elapsed for a running node is
// `now` minus that node's own `node_started`/`node_retried` `ts`, which is
// exactly what the scheduler's heartbeat and `serve`'s card compute too, and
// why a watch started mid-run reports how long the node has REALLY been
// running rather than how long this watch has been open.
//
// Two accepted imprecisions. A watch started late replays history in
// milliseconds, and a tick landing inside that replay may print a heartbeat
// for a node whose terminal line is on disk but not yet read — the terminal
// line still follows it, so the ordering rule holds in the output. And for a
// run that died mid-watch, the heartbeat keeps saying "still running", which
// is the claim the `▶ running…` line already makes and no stronger; the
// status line watchRun prints first remains the liveness word (ADR 0023).
type watchTail struct {
	w   io.Writer
	now func() time.Time
	hb  watchHeartbeat

	mu sync.Mutex
	// inFlight maps a node id to when its current turn began, parsed from the
	// event's ts. The zero time means the stream carried no usable ts, and the
	// heartbeat then prints no elapsed rather than inventing a start.
	inFlight map[string]time.Time

	// quit is closed by stop; exited is closed by the goroutine on its way
	// out, and stop waits on it.
	quit, exited chan struct{}
}

func newWatchTail(w io.Writer, hb watchHeartbeat) *watchTail {
	if hb.Interval <= 0 {
		hb.Interval = schedule.DefaultHeartbeat
	}
	if hb.NewTicker == nil {
		hb.NewTicker = realWatchTicker
	}
	if hb.Now == nil {
		hb.Now = time.Now
	}
	return &watchTail{
		w:        w,
		now:      hb.Now,
		hb:       hb,
		inFlight: make(map[string]time.Time),
		quit:     make(chan struct{}),
		exited:   make(chan struct{}),
	}
}

// print renders one event and updates the in-flight set, in one critical
// section. What opens and closes a node's turn is docs/RUN-FEED.md's own
// liveness rule (ADR 0023): a node is running when its latest node_started
// or node_retried is not followed by node_passed, node_failed or gate_paused
// for the same node_id — gate_paused counts because a paused gate gets no
// node terminal at all. gate_approved and gate_rejected need no entry: the
// gate's node_passed/node_failed follows each immediately. A run_started is a
// leg boundary, so the set is emptied there: a node left running by an
// earlier leg is not running, whatever the current leg does.
//
// A node_retried keeps the start it already has — a retried node is still
// unsettled, and the scheduler's heartbeat counts its elapsed from the first
// `▶` too — and only opens a fresh start when this tail never saw the
// node_started (a watch started between a retry and its verdict).
func (t *watchTail) print(e runfeed.Event) {
	t.mu.Lock()
	defer t.mu.Unlock()
	switch e.Type {
	case runfeed.EventRunStarted:
		clear(t.inFlight)
	case runfeed.EventNodeStarted, runfeed.EventNodeRetried:
		if _, open := t.inFlight[e.NodeID]; !open {
			t.inFlight[e.NodeID] = parseEventTime(e.Timestamp)
		}
	case runfeed.EventNodePassed, runfeed.EventNodeFailed, runfeed.EventGatePaused:
		delete(t.inFlight, e.NodeID)
	}
	fmt.Fprintln(t.w, formatEvent(e))
}

// start launches the heartbeat goroutine. On every tick it prints one
// still-running line per in-flight node, in node-id order so the output is
// stable when several nodes run in parallel.
func (t *watchTail) start() {
	ticks, stopTicker := t.hb.NewTicker(t.hb.Interval)
	go func() {
		defer close(t.exited)
		defer stopTicker()
		for {
			select {
			case <-t.quit:
				return
			case <-ticks:
				// A tick that raced the stop loses: Follow has returned, and
				// this tail is not going to claim anything more.
				select {
				case <-t.quit:
					return
				default:
				}
				t.beat()
			}
		}
	}()
}

// stop ends the heartbeat and does not return until the goroutine has
// exited, so no heartbeat write can land after it.
func (t *watchTail) stop() {
	close(t.quit)
	<-t.exited
}

func (t *watchTail) beat() {
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.inFlight) == 0 {
		return
	}
	ids := make([]string, 0, len(t.inFlight))
	for id := range t.inFlight {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	now := t.now()
	for _, id := range ids {
		fmt.Fprintln(t.w, formatStillRunning(id, t.inFlight[id], now))
	}
}

// formatStillRunning is the heartbeat line, in the scheduler's exact text
// (`… <id>  still running (<elapsed>)`, internal/schedule/heartbeat.go) so
// a watched run reads like the run's own terminal. The `…` glyph is the one
// the scheduler's verifying line already uses for "in flight, not settled";
// `•` is reserved for an event type this build does not know (formatEvent).
// A node whose start is unknown gets the line with no elapsed.
func formatStillRunning(id string, start, now time.Time) string {
	if start.IsZero() {
		return fmt.Sprintf("… %s  still running", id)
	}
	return fmt.Sprintf("… %s  still running (%s)", id, formatWatchElapsed(now.Sub(start)))
}

// parseEventTime reads an event's ts (RFC 3339, nanosecond precision — the
// shape runfeed.StreamWriter stamps). A missing or unparseable ts yields the
// zero time: tracking the node is still right, but an elapsed would be a
// claim the stream does not support.
func parseEventTime(ts string) time.Time {
	if ts == "" {
		return time.Time{}
	}
	at, err := time.Parse(time.RFC3339Nano, ts)
	if err != nil {
		return time.Time{}
	}
	return at
}

// formatWatchElapsed is the scheduler's elapsed rendering, floored rather
// than rounded so the line is true at the moment it prints:
//
//	under 1m        <1m
//	1m to 59m59s    12m
//	1h and above    1h05m, 3h07m
//
// It mirrors schedule.formatElapsed (internal/schedule/heartbeat.go), which
// is unexported; the two must print the identical text.
func formatWatchElapsed(d time.Duration) string {
	if d < time.Minute {
		return "<1m"
	}
	minutes := int64(d / time.Minute)
	if minutes < 60 {
		return fmt.Sprintf("%dm", minutes)
	}
	return fmt.Sprintf("%dh%02dm", minutes/60, minutes%60)
}

// formatEvent renders one stream event as the single human line `watch`
// prints for it, in the same vocabulary as the scheduler's live progress feed
// (▶ running, ✓ PASS, ✗ FAILED, ↻ retry) so a watched run reads like the run's
// own terminal. A type this build does not know — a newer stream schema — is
// rendered generically rather than dropped: the contract tells consumers to
// skip unknown types rather than fail, and for a human watcher "skip" means
// "show what we can".
func formatEvent(e runfeed.Event) string {
	switch e.Type {
	case runfeed.EventRunStarted:
		// The phase, when the event carries one, because an auto run opens TWO
		// legs on one stream — the planner call's and the scheduler's — and
		// without it a watcher sees the same line twice with nothing to say why.
		// The run page's feed renders the same phase the same way, for the same
		// reason. The PLANNING→RUNNING transition is the whole of what ADR 0023
		// made visible.
		if e.Phase != "" {
			return appendEventAccounting(fmt.Sprintf("▶ run started (%s)", e.Phase), e)
		}
		return appendEventAccounting("▶ run started", e)
	case runfeed.EventNodeStarted:
		return fmt.Sprintf("▶ %s  running…", e.NodeID)
	case runfeed.EventNodePassed:
		// Cost is omitted when zero, matching the stream itself (a gate spawns
		// no subprocess); the detail then carries the story ("gate approved").
		line := appendEventAccounting(fmt.Sprintf("✓ %s  %s", e.NodeID, e.Verdict), e)
		if e.Detail != "" {
			line += "  " + e.Detail
		}
		return line
	case runfeed.EventNodeFailed:
		return appendEventAccounting(fmt.Sprintf("✗ %s  FAILED: %s", e.NodeID, e.Detail), e)
	case runfeed.EventNodeRetried:
		return appendEventAccounting(fmt.Sprintf("↻ %s  retry", e.NodeID), e)
	case runfeed.EventRunFinished:
		return appendEventAccounting(fmt.Sprintf("■ run finished: %s", e.Outcome), e)
	default:
		line := "• "
		if e.NodeID != "" {
			line += e.NodeID + "  "
		}
		line += string(e.Type)
		if e.Detail != "" {
			line += "  " + e.Detail
		}
		return line
	}
}

func appendEventAccounting(line string, e runfeed.Event) string {
	if e.CostUnknown {
		line += "  cost unknown"
	} else if e.CostUSD > 0 {
		line += fmt.Sprintf("  $%.4f", e.CostUSD)
	}
	if e.Usage != (runfeed.TokenUsage{}) {
		line += fmt.Sprintf("  tokens %d/%d/%d/%d", e.Usage.InputTokens, e.Usage.CachedInputTokens, e.Usage.OutputTokens, e.Usage.ReasoningOutputTokens)
	}
	return line
}
