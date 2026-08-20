package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// This file tests `show`'s half of the skip-reporting change: it now reads
// its "status could not be derived" sentence from the SAME place `watch` does
// (runstatus.StatusUnavailable) rather than spelling its own copy — one fact,
// one wording, one place. The reachable path is a run whose SNAPSHOT loads
// fine but whose STATUS cannot be derived (an unreadable event stream): the
// per-node table still renders from the snapshot, and the shared sentence
// says why the header has no status word beside it.

// TestShowRun_UnreadableStreamPrintsTheSharedSentenceButStillShowsTheSnapshot
// pins the exact line show.go's own comment calls out: when the snapshot
// loads but the stream does not, the failure is named via
// runstatus.StatusUnavailable rather than silently dropping the status word
// or duplicating watch's wording.
func TestShowRun_UnreadableStreamPrintsTheSharedSentenceButStillShowsTheSnapshot(t *testing.T) {
	dir := t.TempDir()
	// A snapshot that loads perfectly well.
	if err := runstate.Write(filepath.Join(dir, stateFileName), runstate.Snapshot{
		RunID: "run-bad-stream",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"a","prompt":"a"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.42},
		},
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	// An event stream this build refuses: a schema newer than it understands.
	future := `{"schema":99,"run_id":"run-bad-stream","event":"run_started"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, runfeed.FileName), []byte(future), 0o644); err != nil {
		t.Fatalf("write future-schema stream: %v", err)
	}

	var out strings.Builder
	if err := showRun(&out, dir, "run-bad-stream"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	got := out.String()

	// Presence: the shared sentence, on its own line.
	if !strings.Contains(got, "WARNING: this run's status could not be derived:") {
		t.Errorf("out = %q, want the shared StatusUnavailable sentence", got)
	}
	if !strings.Contains(got, "newer than this binary understands") {
		t.Errorf("out = %q, want the stream's own reason named", got)
	}
	// Presence: the per-node table still rendered from the snapshot that DID
	// load — the failure of one file must not hide the other's contents.
	if !strings.Contains(got, "a") || !strings.Contains(got, "0.4200") {
		t.Errorf("out = %q, want the snapshot's per-node row still printed", got)
	}
	if !strings.Contains(got, "TOTAL COST: $0.4200") {
		t.Errorf("out = %q, want the total still computed from the readable snapshot", got)
	}
	// The header must not claim a status word it could not derive.
	if strings.Contains(got, "run-bad-stream — PASS") || strings.Contains(got, "run-bad-stream — FAIL") {
		t.Errorf("out = %q, must not announce a status it could not derive", got)
	}
}

// TestShowRun_CleanRunPrintsNoStatusUnavailableSentence is the paired
// negative control: a run whose stream and snapshot both read cleanly must
// never print the shared refusal sentence at all, and must still show its
// derived status and its per-node table.
func TestShowRun_CleanRunPrintsNoStatusUnavailableSentence(t *testing.T) {
	dir := t.TempDir()
	if err := runstate.Write(filepath.Join(dir, stateFileName), runstate.Snapshot{
		RunID: "run-clean",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"a","prompt":"a"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, CostUSD: 0.10},
		},
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	var out strings.Builder
	if err := showRun(&out, dir, "run-clean"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "status could not be derived") {
		t.Errorf("out = %q, a clean run must not print the refusal sentence", got)
	}
	if !strings.Contains(got, "Run run-clean — PASS, 1 node(s)") {
		// No event stream at all: the leg reads closed (no open leg to
		// report), and the snapshot shows every node completed, so Derive's
		// snapshot-refinement arm answers PASS.
		t.Errorf("out = %q, want the derived status still printed for a clean run", got)
	}
}
