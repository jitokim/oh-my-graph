package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// testPoll keeps the tail loop's end-of-stream sleep short so follow tests
// finish in milliseconds rather than at the production interval.
const testPoll = 5 * time.Millisecond

// writeWatchEvents persists events into dir/events.jsonl through the real
// StreamWriter, so what the watch tests read back is exactly what a run
// leaves on disk — same schema stamp, same line framing.
func writeWatchEvents(t *testing.T, dir, runID string, events ...runfeed.Event) {
	t.Helper()
	feed, err := runfeed.NewStreamWriter(filepath.Join(dir, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open event stream: %v", err)
	}
	defer feed.Close()
	for _, e := range events {
		if err := feed.Emit(e); err != nil {
			t.Fatalf("emit event: %v", err)
		}
	}
}

func TestWatchRun_PrintsWholeStreamAndStopsAtRunFinished(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "20260731-000101",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "alpha"},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "alpha", Verdict: runfeed.VerdictPass, CostUSD: 0.1234, SessionID: "s-1"},
		runfeed.Event{Type: runfeed.EventNodeRetried, NodeID: "beta", Retries: 1},
		runfeed.Event{Type: runfeed.EventNodeFailed, NodeID: "beta", Verdict: runfeed.VerdictFail, Detail: "verify failed: exit 1"},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
	)

	var out, warn strings.Builder
	// The background context proves the stop comes from run_finished, not
	// cancellation: without it a completed stream would be followed forever.
	if err := watchRun(context.Background(), &out, &warn, dir, "20260731-000101", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}

	got := out.String()
	for _, want := range []string{
		"▶ run started",
		"▶ alpha  running…",
		"✓ alpha  PASS  $0.1234",
		"↻ beta  retry",
		"✗ beta  FAILED: verify failed: exit 1",
		"■ run finished: failed",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	if warn.Len() != 0 {
		t.Errorf("clean stream must not warn, got: %q", warn.String())
	}
}

// TestWatchRun_FollowsAppendedEvents pins the tail -f behavior: events
// appended after the watcher has caught up with end-of-stream still appear,
// and the appended run_finished ends the watch.
func TestWatchRun_FollowsAppendedEvents(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "20260731-000202",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "alpha"},
	)

	var out, warn strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- watchRun(context.Background(), &out, &warn, dir, "20260731-000202", testPoll)
	}()

	// Give the watcher time to reach end-of-stream, then append the rest the
	// way a live run would: reopening the same file in append mode.
	time.Sleep(10 * testPoll)
	writeWatchEvents(t, dir, "20260731-000202",
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "alpha", Verdict: runfeed.VerdictPass, CostUSD: 0.5},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watchRun returned error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchRun did not stop after the appended run_finished")
	}

	got := out.String()
	for _, want := range []string{"▶ alpha  running…", "✓ alpha  PASS  $0.5000", "■ run finished: passed"} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
}

// TestWatchRun_StopsOnContextCancel pins the Ctrl-C path: a stream with no
// run_finished is followed until the context is cancelled, and cancellation
// is a clean stop (nil), not a failure.
func TestWatchRun_StopsOnContextCancel(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "20260731-000303",
		runfeed.Event{Type: runfeed.EventRunStarted},
	)

	ctx, cancel := context.WithCancel(context.Background())
	var out, warn strings.Builder
	done := make(chan error, 1)
	go func() {
		done <- watchRun(ctx, &out, &warn, dir, "20260731-000303", testPoll)
	}()

	time.Sleep(10 * testPoll)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("cancelled watch must return nil, got: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watchRun did not stop on context cancellation")
	}
	if !strings.Contains(out.String(), "▶ run started") {
		t.Errorf("events before cancellation must still print:\n%s", out.String())
	}
}

func TestWatchRun_UnknownRunID(t *testing.T) {
	var out, warn strings.Builder
	err := watchRun(context.Background(), &out, &warn, filepath.Join(t.TempDir(), "no-such-run"), "no-such-run", testPoll)
	if err == nil {
		t.Fatal("watchRun on a missing run directory must fail")
	}
	if !strings.Contains(err.Error(), `unknown run "no-such-run"`) {
		t.Errorf("error does not name the unknown run id: %v", err)
	}
}

// TestWatchRun_ZeroCostPassOmitsCost pins the gate rendering: a zero-cost
// PASS (a gate, which spawns no subprocess) shows its detail but no "$0.0000".
func TestWatchRun_ZeroCostPassOmitsCost(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "20260731-000404",
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "approve", Verdict: runfeed.VerdictPass, Detail: "gate approved"},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "20260731-000404", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}
	if !strings.Contains(out.String(), "✓ approve  PASS  gate approved") {
		t.Errorf("gate pass row wrong:\n%s", out.String())
	}
	if strings.Contains(out.String(), "$") {
		t.Errorf("zero-cost pass must omit the cost:\n%s", out.String())
	}
}

// TestWatchRun_UnknownEventTypeRendersGenerically pins forward compatibility:
// an event type from a newer stream schema is shown generically, never
// dropped or fatal (docs/RUN-FEED.md tells consumers to skip unknown types).
func TestWatchRun_UnknownEventTypeRendersGenerically(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "20260731-000505",
		runfeed.Event{Type: "gate_paused", NodeID: "approve"},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
	)

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "20260731-000505", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "• approve  gate_paused") {
		t.Errorf("unknown event type not rendered generically:\n%s", got)
	}
	if !strings.Contains(got, "■ run finished: paused") {
		t.Errorf("missing run_finished line:\n%s", got)
	}
}

// TestWatchRun_MalformedLineWarnsAndContinues pins the damage-tolerance rule:
// a complete line that is not valid JSON is warned about and skipped, and the
// rest of the stream still renders.
func TestWatchRun_MalformedLineWarnsAndContinues(t *testing.T) {
	dir := t.TempDir()
	feedPath := filepath.Join(dir, runfeed.FileName)
	if err := os.WriteFile(feedPath, []byte("{not json\n"), 0o644); err != nil {
		t.Fatalf("write malformed line: %v", err)
	}
	writeWatchEvents(t, dir, "20260731-000606",
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "20260731-000606", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}
	if !strings.Contains(warn.String(), "skipping malformed event line") {
		t.Errorf("malformed line must warn, got: %q", warn.String())
	}
	if !strings.Contains(out.String(), "■ run finished: passed") {
		t.Errorf("stream after the malformed line must still render:\n%s", out.String())
	}
}

// TestWatchRun_NewerSchemaWarnsOnce pins the consumer contract: an event
// stamped with a schema newer than this build warns (once), but still renders.
func TestWatchRun_NewerSchemaWarnsOnce(t *testing.T) {
	dir := t.TempDir()
	future := `{"schema":99,"run_id":"r","event":"node_started","node_id":"alpha"}` + "\n" +
		`{"schema":99,"run_id":"r","event":"run_finished","outcome":"passed"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, runfeed.FileName), []byte(future), 0o644); err != nil {
		t.Fatalf("write future-schema stream: %v", err)
	}

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "future", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}
	if got := strings.Count(warn.String(), "newer than this build"); got != 1 {
		t.Errorf("newer schema must warn exactly once, warned %d times: %q", got, warn.String())
	}
	if !strings.Contains(out.String(), "▶ alpha  running…") {
		t.Errorf("newer-schema events must still render:\n%s", out.String())
	}
}

func TestRunWatch_ArgumentErrors(t *testing.T) {
	if err := runWatch(nil); err == nil || !strings.Contains(err.Error(), "missing run id") {
		t.Errorf("bare `watch` must ask for a run id, got: %v", err)
	}
	if err := runWatch([]string{"a", "b"}); err == nil || !strings.Contains(err.Error(), `unexpected argument "b"`) {
		t.Errorf("`watch` with extra arguments must reject them, got: %v", err)
	}
}

// TestMainExitCode_WatchUnknownRunIsNonZero pins the whole path the user
// actually hits: `oh-my-graph watch <bad-id>` dispatches through run()'s
// switch and exits non-zero.
func TestMainExitCode_WatchUnknownRunIsNonZero(t *testing.T) {
	isolateRunHome(t)
	if code := mainExitCode([]string{"watch", "nope"}); code != 1 {
		t.Errorf("watch of an unknown run must exit 1, got %d", code)
	}
}

// TestFormatEvent_APlanningLegSaysWhichPhaseItOpened pins the one line that
// makes ADR 0023's transition visible on the live human view of the stream. An
// auto run opens TWO legs on one stream — the planner call's and the
// scheduler's — so without the phase a watcher sees "run started" twice with
// nothing to say why, and the PLANNING→RUNNING transition happens invisibly.
func TestFormatEvent_APlanningLegSaysWhichPhaseItOpened(t *testing.T) {
	planning := formatEvent(runfeed.Event{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning})
	if !strings.Contains(planning, runfeed.PhasePlanning) {
		t.Errorf("a planning leg's opening line = %q, want it to name the phase", planning)
	}
	// A scheduler leg carries no phase and its line is unchanged: every
	// hand-written run's stream still reads exactly as before.
	if plain := formatEvent(runfeed.Event{Type: runfeed.EventRunStarted}); plain != "▶ run started" {
		t.Errorf("an untagged leg's opening line = %q, want it unchanged", plain)
	}
}
