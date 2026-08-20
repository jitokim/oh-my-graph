package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// This file tests `watch`'s half of the skip-reporting change: the ONE
// refusal that is watch's to report — an incompatible snapshot schema on an
// otherwise-tailable run — must now be said once (runstatus.StatusUnavailable)
// instead of silently dropped, while every other refusal keeps falling
// through untouched, exactly as before.

// TestWatchRun_IncompatibleSnapshotBreaksItsSilenceButStillTails is the
// regression this whole surface exists to close: before the fix, a run whose
// state.json was written by an incompatible schema tailed perfectly with NO
// mention anywhere that its status could not be derived. The stream still
// tails; the one new fact is the warning.
func TestWatchRun_IncompatibleSnapshotBreaksItsSilenceButStillTails(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "run-old-schema",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte(`{"schema":2}`), 0o644); err != nil {
		t.Fatalf("write schema-mismatch snapshot: %v", err)
	}

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "run-old-schema", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}

	// Presence: the shared sentence, naming the schema mismatch.
	gotWarn := warn.String()
	if !strings.Contains(gotWarn, "WARNING: this run's status could not be derived:") {
		t.Errorf("warn = %q, want the shared StatusUnavailable sentence", gotWarn)
	}
	if !strings.Contains(gotWarn, "schema version 2") {
		t.Errorf("warn = %q, want it to name the schema mismatch", gotWarn)
	}
	// Presence: the tail itself was in no way interrupted by the refusal —
	// every event before and after the status question still printed.
	gotOut := out.String()
	for _, want := range []string{"▶ run started", "▶ a  running…", "✓ a  PASS", "■ run finished: passed"} {
		if !strings.Contains(gotOut, want) {
			t.Errorf("out = %q missing %q: the tail must proceed exactly as before the fix", gotOut, want)
		}
	}
	// And it must NOT print the ordinary "run X is STATUS" line: there is no
	// status to announce, which is the whole reason a sentence had to replace
	// it instead of nothing at all.
	if strings.Contains(gotOut, "run run-old-schema is ") {
		t.Errorf("out = %q, must not announce a status it could not derive", gotOut)
	}
}

// TestWatchRun_OtherUnreadableSnapshotStaysSilentOnStatusButStillTails pins the
// deliberate asymmetry: a corrupt (non-schema-mismatch) snapshot is NOT one of
// the refusals watch reports — that is the stream's or a future reader's to
// report, and printing two complaints about the same directory is exactly what
// the design note says this surface must avoid. The stream must still tail.
func TestWatchRun_OtherUnreadableSnapshotStaysSilentOnStatusButStillTails(t *testing.T) {
	dir := t.TempDir()
	writeWatchEvents(t, dir, "run-corrupt-snap",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	var out, warn strings.Builder
	if err := watchRun(context.Background(), &out, &warn, dir, "run-corrupt-snap", testPoll); err != nil {
		t.Fatalf("watchRun returned error: %v", err)
	}

	if strings.Contains(warn.String(), "status could not be derived") {
		t.Errorf("warn = %q, a non-schema-mismatch snapshot error is not watch's to report", warn.String())
	}
	// Presence: the tail still ran to completion despite the unreadable
	// snapshot sitting beside it.
	gotOut := out.String()
	if !strings.Contains(gotOut, "▶ run started") || !strings.Contains(gotOut, "■ run finished: passed") {
		t.Errorf("out = %q, want the stream to tail regardless of the unreadable snapshot", gotOut)
	}
}
