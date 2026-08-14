package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
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
	fake.KeyFn = nodePromptKey
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

// TestScheduler_EventStreamNodeStartedPublishesSessionID proves the live-view
// promise: node_started publishes the session id owned by the selected runtime.
// A fresh runtime reports its new id; a session-handoff reports the parent id
// it resumes. The scheduler neither mints nor guesses either value.
func TestScheduler_EventStreamNodeStartedPublishesSessionID(t *testing.T) {
	g := mustGraph(t, `
name: session-publish
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a], handoff: session }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.10), "b": pass("s-a", 0.20),
	})
	feed, path := newEventStream(t, "run-session-publish")
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
	if startedA := events[1]; startedA.SessionID != "s-a" {
		t.Errorf("node_started a session_id = %q, want runtime-owned %q", startedA.SessionID, "s-a")
	}
	if startedB := events[3]; startedB.SessionID != "s-a" {
		t.Errorf("node_started b session_id = %q, want resumed parent %q", startedB.SessionID, "s-a")
	}

	invocations := fake.Invocations()
	if len(invocations) != 2 {
		t.Fatalf("invocations = %d, want 2", len(invocations))
	}
	if invocations[0].ResumeSession != "" {
		t.Errorf("a's invocation resume = %q, want a fresh runtime session", invocations[0].ResumeSession)
	}
	if invocations[1].ResumeSession != "s-a" {
		t.Errorf("b's invocation resume = %q, want s-a", invocations[1].ResumeSession)
	}

	// The absence must be a truly ABSENT key, not an empty string: RUN-FEED.md
	// tells consumers zero/empty fields are omitted from the JSON.
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read event stream: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if !strings.Contains(lines[1], `"session_id":"s-a"`) {
		t.Errorf("node_started a line = %s, want a session_id key carrying s-a", lines[1])
	}
	if !strings.Contains(lines[3], `"session_id":"s-a"`) {
		t.Errorf("node_started b line = %s, want the resumed session_id", lines[3])
	}
}

// TestScheduler_EventStreamRetryPublishesFreshSessionID proves each retried
// attempt runs under a FRESH pre-assigned id, published on its node_retried:
// the failed attempt's id already names a real session its child wrote, so
// reusing it would point a live view (and claude itself) at the wreckage.
func TestScheduler_EventStreamRetryPublishesFreshSessionID(t *testing.T) {
	g := mustGraph(t, `
name: retry-session
nodes:
  - id: flaky
    prompt: flaky
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	fake := &sequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "NOPE", SessionID: "s-1", TotalCostUSD: 0.05},
		{Result: "NOPE", SessionID: "s-2", TotalCostUSD: 0.05},
	}}
	feed, path := newEventStream(t, "run-retry-session")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected run to fail after retries exhausted")
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started flaky", "node_retried flaky",
		"node_failed flaky",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if started := events[1]; started.SessionID != "s-1" {
		t.Errorf("node_started session_id = %q, want %q", started.SessionID, "s-1")
	}
	if retried := events[2]; retried.SessionID != "s-2" {
		t.Errorf("node_retried session_id = %q, want the fresh %q", retried.SessionID, "s-2")
	}
}

// TestScheduler_EventStreamGatePause proves a fresh run reaching a gate emits
// gate_paused — carrying the gate's node id — before the leg closes with
// outcome "paused", so a stream consumer knows both that the run paused and
// which gate a resume must decide, without reading state.json.
func TestScheduler_EventStreamGatePause(t *testing.T) {
	g := mustGraph(t, `
name: gate-pause
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
  - { id: ship, prompt: ship, depends_on: [approve] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0.10)})
	feed, path := newEventStream(t, "run-gate-pause")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	var paused *PausedError
	if err := s.Run(context.Background(), g, h, led); !errors.As(err, &paused) {
		t.Fatalf("expected *PausedError, got %T: %v", err, err)
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started a", "node_passed a",
		"node_started approve", "gate_paused approve",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if pausedEvent := events[4]; pausedEvent.NodeID != "approve" {
		t.Errorf("gate_paused node_id = %q, want %q", pausedEvent.NodeID, "approve")
	}
	if gateStarted := events[3]; gateStarted.SessionID != "" {
		t.Errorf("gate node_started session_id = %q, want none (a gate spawns no subprocess)", gateStarted.SessionID)
	}
	if finished := events[len(events)-1]; finished.Outcome != runfeed.OutcomePaused {
		t.Errorf("run_finished outcome = %q, want %q", finished.Outcome, runfeed.OutcomePaused)
	}
}

// TestScheduler_EventStreamGateApprove proves a resumed leg's approve decision
// (a RecordedController replaying --approve, exactly what `resume` injects)
// emits gate_approved ahead of the gate's ordinary terminal node_passed, then
// runs the dependents and closes the leg with outcome "passed".
func TestScheduler_EventStreamGateApprove(t *testing.T) {
	g := mustGraph(t, `
name: gate-approve
nodes:
  - { id: approve, type: gate }
  - { id: ship, prompt: ship, depends_on: [approve] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"ship": pass("s-ship", 0.30)})
	feed, path := newEventStream(t, "run-gate-approve")
	controller := gate.NewRecordedController(map[string]gate.Decision{"approve": gate.DecisionApprove})
	s, h, led := newHarness(t, fake, Options{EventSink: feed, Gate: controller})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started approve", "gate_approved approve", "node_passed approve",
		"node_started ship", "node_passed ship",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if approved := events[2]; approved.NodeID != "approve" {
		t.Errorf("gate_approved node_id = %q, want %q", approved.NodeID, "approve")
	}
	if gatePassed := events[3]; gatePassed.Verdict != runfeed.VerdictPass || gatePassed.CostUSD != 0 || gatePassed.SessionID != "" {
		t.Errorf("gate node_passed payload = %+v, want verdict PASS with no cost/session (a gate spawns no subprocess)", gatePassed)
	}
	if finished := events[len(events)-1]; finished.Outcome != runfeed.OutcomePassed {
		t.Errorf("run_finished outcome = %q, want %q", finished.Outcome, runfeed.OutcomePassed)
	}
}

// TestScheduler_EventStreamGateReject proves a resumed leg's reject decision
// emits gate_rejected ahead of the gate's terminal node_failed, never runs the
// pruned dependent, and closes the leg with outcome "failed".
func TestScheduler_EventStreamGateReject(t *testing.T) {
	g := mustGraph(t, `
name: gate-reject
nodes:
  - { id: approve, type: gate }
  - { id: ship, prompt: ship, depends_on: [approve] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"ship": pass("s-ship", 0.30)})
	feed, path := newEventStream(t, "run-gate-reject")
	controller := gate.NewRecordedController(map[string]gate.Decision{"approve": gate.DecisionReject})
	s, h, led := newHarness(t, fake, Options{EventSink: feed, Gate: controller})

	var runFailed *RunFailedError
	if err := s.Run(context.Background(), g, h, led); !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started approve", "gate_rejected approve", "node_failed approve",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}
	if rejected := events[2]; rejected.NodeID != "approve" {
		t.Errorf("gate_rejected node_id = %q, want %q", rejected.NodeID, "approve")
	}
	if gateFailed := events[3]; gateFailed.Verdict != runfeed.VerdictFail {
		t.Errorf("gate node_failed verdict = %q, want %q", gateFailed.Verdict, runfeed.VerdictFail)
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
