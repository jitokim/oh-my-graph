package schedule

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// newEventStream opens a runfeed.StreamWriter on a temp events.jsonl and
// returns it with its path, closing it when the test ends. Scheduler event
// tests run against the REAL stream writer (only the NodeRunner is faked), so
// they assert the actual bytes a consumer would tail.
func newEventStream(t *testing.T, runID string) (*runfeed.StreamWriter, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), runfeed.FileName)
	feed, err := runfeed.NewStreamWriter(path, runID)
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	t.Cleanup(func() { feed.Close() })
	return feed, path
}

// readEventStream decodes every line of the events.jsonl at path.
func readEventStream(t *testing.T, path string) []runfeed.Event {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event stream: %v", err)
	}
	var events []runfeed.Event
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e runfeed.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not valid JSON: %v (%q)", i, err, line)
		}
		events = append(events, e)
	}
	return events
}

// eventTypes projects the sequence of event types, with node ids attached
// where present ("node_passed a"), so a whole run reads as one comparable
// slice.
func eventTypes(events []runfeed.Event) []string {
	out := make([]string, len(events))
	for i, e := range events {
		out[i] = string(e.Type)
		if e.NodeID != "" {
			out[i] += " " + e.NodeID
		}
	}
	return out
}

// TestScheduler_EventStreamSequence proves a passing linear run emits exactly
// the documented lifecycle sequence, and that every event carries the schema
// version, the run id, and a parseable RFC 3339 timestamp — the contract
// docs/RUN-FEED.md promises a consumer.
func TestScheduler_EventStreamSequence(t *testing.T) {
	g := mustGraph(t, `
name: linear
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.10), "b": pass("s-b", 0.20),
	})
	feed, path := newEventStream(t, "run-events")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started a", "node_passed a",
		"node_started b", "node_passed b",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}

	for i, e := range events {
		if e.Schema != runfeed.Schema {
			t.Errorf("event %d schema = %d, want %d", i, e.Schema, runfeed.Schema)
		}
		if e.RunID != "run-events" {
			t.Errorf("event %d run_id = %q, want %q", i, e.RunID, "run-events")
		}
		if _, err := time.Parse(time.RFC3339Nano, e.Timestamp); err != nil {
			t.Errorf("event %d ts %q is not RFC 3339: %v", i, e.Timestamp, err)
		}
	}

	passedA := events[2]
	if passedA.Verdict != runfeed.VerdictPass || passedA.CostUSD != 0.10 || passedA.SessionID != "s-a" || passedA.Retries != 0 {
		t.Errorf("node_passed a payload = %+v, want verdict PASS, cost 0.10, session s-a, retries 0", passedA)
	}
	if finished := events[len(events)-1]; finished.Outcome != runfeed.OutcomePassed {
		t.Errorf("run_finished outcome = %q, want %q", finished.Outcome, runfeed.OutcomePassed)
	}
}

// TestScheduler_EventStreamRetryAndFailure proves each retry emits its own
// node_retried (with the 1-based retry ordinal) and that the terminal
// node_failed carries the verdict, the exhausted retry count, and the failure
// detail — then the run closes with outcome "failed".
func TestScheduler_EventStreamRetryAndFailure(t *testing.T) {
	g := mustGraph(t, `
name: retry-exhaust
nodes:
  - id: flaky
    prompt: flaky
    success_check: { result_matches: "PASS" }
    retry: { max: 2, on: [result_mismatch] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"flaky": {Result: "NOPE", ExitCode: 0, SessionID: "s-flaky", TotalCostUSD: 0.05},
	})
	feed, path := newEventStream(t, "run-retry")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected run to fail after retries exhausted")
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started flaky",
		"node_retried flaky", "node_retried flaky",
		"node_failed flaky",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}

	if first, second := events[2], events[3]; first.Retries != 1 || second.Retries != 2 {
		t.Errorf("node_retried ordinals = %d, %d, want 1, 2", first.Retries, second.Retries)
	}
	failed := events[4]
	if failed.Verdict != runfeed.VerdictFail || failed.Retries != 2 || failed.SessionID != "s-flaky" || failed.CostUSD != 0.05 {
		t.Errorf("node_failed payload = %+v, want verdict FAIL, retries 2, session s-flaky, cost 0.05", failed)
	}
	if !strings.Contains(failed.Detail, "result did not match") {
		t.Errorf("node_failed detail = %q, want the failing predicate named", failed.Detail)
	}
	if finished := events[len(events)-1]; finished.Outcome != runfeed.OutcomeFailed {
		t.Errorf("run_finished outcome = %q, want %q", finished.Outcome, runfeed.OutcomeFailed)
	}
}

// TestScheduler_EventStreamAppendOnly proves a second scheduler leg reopening
// the same stream (what `resume` does) appends after the first leg's events
// and leaves them byte-identical — the stream is append-only across legs.
func TestScheduler_EventStreamAppendOnly(t *testing.T) {
	g := mustGraph(t, `
name: tiny
nodes:
  - { id: a, prompt: a }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0)})

	feed, path := newEventStream(t, "run-legs")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})
	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("first leg returned error: %v", err)
	}
	if err := feed.Close(); err != nil {
		t.Fatalf("close first leg stream: %v", err)
	}
	firstLeg, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read first leg stream: %v", err)
	}

	reopened, err := runfeed.NewStreamWriter(path, "run-legs")
	if err != nil {
		t.Fatalf("reopen stream: %v", err)
	}
	t.Cleanup(func() { reopened.Close() })
	s2, h2, led2 := newHarness(t, fake, Options{EventSink: reopened})
	if err := s2.Run(context.Background(), g, h2, led2); err != nil {
		t.Fatalf("second leg returned error: %v", err)
	}

	both, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read stream after second leg: %v", err)
	}
	if !strings.HasPrefix(string(both), string(firstLeg)) {
		t.Fatal("second leg rewrote the first leg's events; the stream must be append-only")
	}
	if events := readEventStream(t, path); len(events) != 8 {
		t.Fatalf("got %d events across two legs, want 8 (two full run_started..run_finished brackets)", len(events))
	}
}
