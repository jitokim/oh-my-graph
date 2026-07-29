package runfeed

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// readLines splits the file at path into its non-empty lines.
func readLines(t *testing.T, path string) []string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event stream: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(string(data), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// TestStreamWriter_StampsEveryEvent proves Emit stamps the schema version, the
// run id, and a parseable RFC 3339 timestamp onto every line, in emission
// order, one JSON object per line.
func TestStreamWriter_StampsEveryEvent(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()

	if err := w.Emit(Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Emit run_started: %v", err)
	}
	if err := w.Emit(Event{Type: EventNodeStarted, NodeID: "a"}); err != nil {
		t.Fatalf("Emit node_started: %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != 2 {
		t.Fatalf("got %d lines, want 2: %v", len(lines), lines)
	}
	wantTypes := []EventType{EventRunStarted, EventNodeStarted}
	for i, line := range lines {
		var e Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not valid JSON: %v (%q)", i, err, line)
		}
		if e.Schema != Schema {
			t.Errorf("line %d schema = %d, want %d", i, e.Schema, Schema)
		}
		if e.RunID != "run-1" {
			t.Errorf("line %d run_id = %q, want %q", i, e.RunID, "run-1")
		}
		if e.Type != wantTypes[i] {
			t.Errorf("line %d event = %q, want %q", i, e.Type, wantTypes[i])
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
			t.Errorf("line %d ts %q is not RFC 3339: %v", i, e.Timestamp, err)
		}
	}
}

// TestStreamWriter_TerminalFieldsRoundTrip proves a terminal node event's
// payload (verdict, cost, session, retries, detail) survives the encode
// unchanged — the fields docs/RUN-FEED.md promises a consumer.
func TestStreamWriter_TerminalFieldsRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()

	in := Event{
		Type:      EventNodeFailed,
		NodeID:    "flaky",
		Verdict:   VerdictFail,
		CostUSD:   0.25,
		SessionID: "s-1",
		Retries:   2,
		Detail:    "result did not match /PASS/",
	}
	if err := w.Emit(in); err != nil {
		t.Fatalf("Emit: %v", err)
	}

	var out Event
	if err := json.Unmarshal([]byte(readLines(t, path)[0]), &out); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if out.NodeID != in.NodeID || out.Verdict != in.Verdict || out.CostUSD != in.CostUSD ||
		out.SessionID != in.SessionID || out.Retries != in.Retries || out.Detail != in.Detail {
		t.Fatalf("round trip changed the event: got %+v, want payload of %+v", out, in)
	}
}

// TestStreamWriter_AppendsAcrossReopen proves reopening the same path (what a
// resumed leg does) appends after the existing lines and leaves them
// byte-for-byte untouched — the stream is append-only.
func TestStreamWriter_AppendsAcrossReopen(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)

	first, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter (first leg): %v", err)
	}
	if err := first.Emit(Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Emit (first leg): %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("Close (first leg): %v", err)
	}
	firstLeg := readLines(t, path)

	second, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter (second leg): %v", err)
	}
	defer second.Close()
	if err := second.Emit(Event{Type: EventRunFinished, Outcome: OutcomePassed}); err != nil {
		t.Fatalf("Emit (second leg): %v", err)
	}

	lines := readLines(t, path)
	if len(lines) != len(firstLeg)+1 {
		t.Fatalf("got %d lines after reopen, want %d", len(lines), len(firstLeg)+1)
	}
	for i, line := range firstLeg {
		if lines[i] != line {
			t.Errorf("line %d changed after reopen:\n  before: %q\n  after:  %q", i, line, lines[i])
		}
	}
}

// TestStreamWriter_EmitAfterCloseFails proves an emit on a closed writer
// reports the failure instead of silently dropping the event.
func TestStreamWriter_EmitAfterCloseFails(t *testing.T) {
	w, err := NewStreamWriter(filepath.Join(t.TempDir(), FileName), "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if err := w.Emit(Event{Type: EventRunStarted}); err == nil {
		t.Fatal("Emit after Close returned nil, want error")
	}
}

// TestNewStreamWriter_CreatesRunDir proves the writer creates a missing run
// directory rather than failing, matching runstate.Write's behaviour.
func TestNewStreamWriter_CreatesRunDir(t *testing.T) {
	path := filepath.Join(t.TempDir(), "runs", "20260730-000000", FileName)
	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()
	if err := w.Emit(Event{Type: EventRunStarted}); err != nil {
		t.Fatalf("Emit: %v", err)
	}
	if got := readLines(t, path); len(got) != 1 {
		t.Fatalf("got %d lines, want 1", len(got))
	}
}
