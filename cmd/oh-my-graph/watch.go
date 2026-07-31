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
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
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
	if len(args) == 0 {
		return fmt.Errorf("watch: missing run id (usage: oh-my-graph watch <run-id>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("watch: unexpected argument %q (usage: oh-my-graph watch <run-id>)", args[1])
	}
	runID := args[0]

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return watchRun(ctx, os.Stdout, os.Stderr, runDirFor(runID), runID, watchPollInterval)
}

// watchRun tails runDir's events.jsonl onto w: every already-written event is
// printed immediately, then the stream is followed tail -f style (poll is the
// end-of-stream re-read interval). It returns nil when it prints the
// run_finished event — the event that closes a leg, so a paused run's watch
// ends at its "paused" outcome — or when ctx is cancelled (Ctrl-C), which is
// the normal way to stop watching, not a failure. A stream that ends without a
// run_finished (a leg that crashed mid-run) is indistinguishable from a slow
// writer, so it is followed until the user interrupts.
//
// An unknown run id — no event stream on disk — is a distinct, clearly worded
// error rather than a raw file-not-found, because it is the one failure the
// user causes by mistyping an id.
func watchRun(ctx context.Context, w, warnW io.Writer, runDir, runID string, poll time.Duration) error {
	feedPath := filepath.Join(runDir, runfeed.FileName)

	// The tail loop itself is runfeed.Follow — the same reader `serve`'s SSE
	// endpoint streams through (via its FollowWait variant), so the two
	// consumers can never drift on line framing, the shared line cap, or
	// partial-final-line tolerance. watch deliberately does NOT wait for a
	// stream to be created: here a missing stream means a mistyped run id,
	// which must be the unknown-run error below, not an endless wait. watch
	// keeps only its own interpretation: formatting, the malformed-line
	// warning, and stopping at run_finished.
	warnedSchema := false
	err := runfeed.Follow(ctx, feedPath, poll, func(line []byte) (bool, error) {
		var event runfeed.Event
		if err := json.Unmarshal(line, &event); err != nil {
			fmt.Fprintf(warnW, "WARNING: skipping malformed event line: %v\n", err)
			return false, nil
		}
		if event.Schema > runfeed.Schema && !warnedSchema {
			warnedSchema = true
			fmt.Fprintf(warnW, "WARNING: stream schema %d is newer than this build understands (%d); some events may render generically\n", event.Schema, runfeed.Schema)
		}
		fmt.Fprintln(w, formatEvent(event))
		return event.Type == runfeed.EventRunFinished, nil
	})
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("unknown run %q: no event stream at %s (see the run ids under %s)", runID, feedPath, filepath.Dir(runDir))
	}
	return err
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
		return "▶ run started"
	case runfeed.EventNodeStarted:
		return fmt.Sprintf("▶ %s  running…", e.NodeID)
	case runfeed.EventNodePassed:
		// Cost is omitted when zero, matching the stream itself (a gate spawns
		// no subprocess); the detail then carries the story ("gate approved").
		line := fmt.Sprintf("✓ %s  %s", e.NodeID, e.Verdict)
		if e.CostUSD > 0 {
			line += fmt.Sprintf("  $%.4f", e.CostUSD)
		}
		if e.Detail != "" {
			line += "  " + e.Detail
		}
		return line
	case runfeed.EventNodeFailed:
		return fmt.Sprintf("✗ %s  FAILED: %s", e.NodeID, e.Detail)
	case runfeed.EventNodeRetried:
		return fmt.Sprintf("↻ %s  retry", e.NodeID)
	case runfeed.EventRunFinished:
		return fmt.Sprintf("■ run finished: %s", e.Outcome)
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
