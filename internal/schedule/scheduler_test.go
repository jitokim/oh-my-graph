package schedule

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// mustGraph parses YAML into a validated graph or fails the test. Test graphs
// set each node's prompt to its own id so the default FakeRunner keys on the
// node id.
func mustGraph(t *testing.T, yaml string) *graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("parse test graph: %v", err)
	}
	return g
}

// newHarness wires a scheduler run against a runner with a temp run directory.
// It defaults ProgressWriter to io.Discard so tests stay quiet and
// deterministic; pass opts.ProgressWriter explicitly to inspect the live feed.
func newHarness(t *testing.T, nodeRunner runner.NodeRunner, opts Options) (*Scheduler, *handoff.Handoff, *ledger.RunLedger) {
	t.Helper()
	if opts.ProgressWriter == nil {
		opts.ProgressWriter = io.Discard
	}
	h := handoff.New(t.TempDir(), nil)
	led := ledger.New("test")
	return NewScheduler(nodeRunner, opts), h, led
}

// pass is a scripted successful outcome carrying a session id and cost.
func pass(sessionID string, cost float64) runner.NodeOutcome {
	return runner.NodeOutcome{SessionID: sessionID, Result: "PASS", TotalCostUSD: cost, ExitCode: 0}
}

func indexOf(calls []string, key string) int {
	for i, c := range calls {
		if c == key {
			return i
		}
	}
	return -1
}

// --- topological order ------------------------------------------------------

// TestScheduler_TopologicalOrder proves a linear chain a->b->c runs strictly in
// dependency order.
func TestScheduler_TopologicalOrder(t *testing.T) {
	g := mustGraph(t, `
name: linear
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
  - { id: c, prompt: c, depends_on: [b] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0), "b": pass("s-b", 0), "c": pass("s-c", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got, want := fake.Calls(), []string{"a", "b", "c"}; !equalStrings(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
}

// --- parallel fan-out -------------------------------------------------------

// TestScheduler_ParallelFanOut proves that siblings sharing one parent all run
// after it, and that all of them run (emergent parallelism).
func TestScheduler_ParallelFanOut(t *testing.T) {
	g := mustGraph(t, `
name: fanout
nodes:
  - { id: root, prompt: root }
  - { id: b, prompt: b, depends_on: [root] }
  - { id: c, prompt: c, depends_on: [root] }
  - { id: d, prompt: d, depends_on: [root] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"root": pass("s", 0), "b": pass("s", 0), "c": pass("s", 0), "d": pass("s", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	calls := fake.Calls()
	if len(calls) != 4 {
		t.Fatalf("expected 4 node runs, got %v", calls)
	}
	rootAt := indexOf(calls, "root")
	for _, child := range []string{"b", "c", "d"} {
		if at := indexOf(calls, child); at < 0 || at < rootAt {
			t.Errorf("child %q ran at %d, must be after root at %d", child, at, rootAt)
		}
	}
}

// --- fan-in waits for both parents -----------------------------------------

// TestScheduler_FanInWaitsForBoth proves a node with two parents runs only after
// BOTH have succeeded.
func TestScheduler_FanInWaitsForBoth(t *testing.T) {
	g := mustGraph(t, `
name: fanin
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - { id: c, prompt: c, depends_on: [a, b] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s", 0), "b": pass("s", 0), "c": pass("s", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	calls := fake.Calls()
	cAt := indexOf(calls, "c")
	if cAt != 2 {
		t.Fatalf("c ran at %d, want last (2); calls=%v", cAt, calls)
	}
	if indexOf(calls, "a") > cAt || indexOf(calls, "b") > cAt {
		t.Fatalf("c ran before a parent; calls=%v", calls)
	}
}

// --- retry ------------------------------------------------------------------

// TestScheduler_RetryExhausted proves a node whose success_check keeps failing is
// re-run exactly max+1 times, then reported as a failure.
func TestScheduler_RetryExhausted(t *testing.T) {
	g := mustGraph(t, `
name: retry-exhaust
nodes:
  - id: flaky
    prompt: flaky
    success_check: { result_matches: "PASS" }
    retry: { max: 2, on: [result_mismatch] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"flaky": {Result: "NOPE", ExitCode: 0},
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	if err == nil {
		t.Fatal("expected run to fail after retries exhausted")
	}
	if got := fake.InvocationCount("flaky"); got != 3 {
		t.Fatalf("flaky invoked %d times, want 3 (initial + 2 retries)", got)
	}
}

// TestScheduler_RetryRecovers proves a node that fails once then passes is
// retried and ends PASS — driven by a stateful runner that fails the first
// attempt only.
func TestScheduler_RetryRecovers(t *testing.T) {
	g := mustGraph(t, `
name: retry-recover
nodes:
  - id: flaky
    prompt: flaky
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	flaky := &sequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "NOPE", ExitCode: 0},
		{Result: "PASS", ExitCode: 0, SessionID: "s-final"},
	}}
	s, h, led := newHarness(t, flaky, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	if flaky.calls != 2 {
		t.Fatalf("flaky invoked %d times, want 2", flaky.calls)
	}
	if rec := findRecord(led, "flaky"); rec.Verdict != ledger.VerdictPass {
		t.Fatalf("flaky verdict = %s, want PASS", rec.Verdict)
	}
}

// --- halt on fail cancels siblings -----------------------------------------

// TestScheduler_HaltOnFailCancelsSiblings proves that when one node fails, the
// shared context is cancelled and an in-flight sibling is interrupted (it never
// produces a real outcome). The run returns a *HaltError naming the failing
// node.
func TestScheduler_HaltOnFailCancelsSiblings(t *testing.T) {
	g := mustGraph(t, `
name: halt
nodes:
  - { id: boom, prompt: boom, success_check: { exit_zero: true } }
  - { id: sibling, prompt: sibling }
`)
	r := &haltRunner{failKey: "boom", blockKey: "sibling", released: make(chan struct{})}

	s, h, led := newHarness(t, r, Options{})
	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if halt.NodeID != "boom" {
		t.Fatalf("halt named node %q, want boom", halt.NodeID)
	}
	if rec := findRecord(led, "sibling"); rec.Verdict == ledger.VerdictPass {
		t.Fatalf("sibling should have been cancelled, but was recorded PASS")
	}
}

// --- continue on fail prunes only the failed subtree ------------------------

// TestScheduler_ContinueOnFailPrunesSubtree proves --continue-on-fail lets an
// independent branch finish while the failed node's dependents are pruned.
func TestScheduler_ContinueOnFailPrunesSubtree(t *testing.T) {
	g := mustGraph(t, `
name: continue
nodes:
  - { id: bad, prompt: bad, success_check: { exit_zero: true } }
  - { id: child, prompt: child, depends_on: [bad] }
  - { id: independent, prompt: independent }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":         {Result: "x", ExitCode: 1},
		"child":       pass("s", 0),
		"independent": pass("s", 0),
	})
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}
	if !equalStrings(runFailed.FailedNodes, []string{"bad"}) {
		t.Fatalf("failed nodes = %v, want [bad]", runFailed.FailedNodes)
	}

	calls := fake.Calls()
	if indexOf(calls, "child") != -1 {
		t.Errorf("pruned child should never run; calls=%v", calls)
	}
	if indexOf(calls, "independent") == -1 {
		t.Errorf("independent branch should have run; calls=%v", calls)
	}
	if rec := findRecord(led, "independent"); rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent verdict = %s, want PASS", rec.Verdict)
	}
}

// --- cost summation ---------------------------------------------------------

// TestScheduler_CostSummation proves the ledger sums per-node reported cost.
func TestScheduler_CostSummation(t *testing.T) {
	g := mustGraph(t, `
name: cost
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.10), "b": pass("s-b", 0.25),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := led.TotalCost(); got < 0.349 || got > 0.351 {
		t.Fatalf("total cost = %v, want ~0.35", got)
	}
}

// --- artifact handoff through the scheduler ---------------------------------

// TestScheduler_ArtifactHandoff proves a downstream node's prompt is interpolated
// with its parent's persisted artifact path before it runs.
func TestScheduler_ArtifactHandoff(t *testing.T) {
	g := mustGraph(t, `
name: handoff
nodes:
  - { id: writer, prompt: writer }
  - { id: reader, prompt: "reader {{ artifacts.writer }}", depends_on: [writer] }
`)
	rec := &recordingRunner{
		outcomes: map[string]runner.NodeOutcome{
			"writer": {Result: "the-writer-output", ExitCode: 0, SessionID: "s-w"},
			"reader": {Result: "PASS", ExitCode: 0, SessionID: "s-r"},
		},
	}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	readerPrompt := rec.promptFor("reader")
	if !strings.Contains(readerPrompt, "writer.out") {
		t.Fatalf("reader prompt was not interpolated with the artifact path: %q", readerPrompt)
	}
}

// --- gate execution is rejected in v0.1 -------------------------------------

// TestScheduler_GateNodeRejected proves a graph that parses with a gate node
// still refuses to EXECUTE it in v0.1, halting with the not-implemented error
// rather than silently running past the approval.
func TestScheduler_GateNodeRejected(t *testing.T) {
	g := mustGraph(t, `
name: with-gate
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s", 0)})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	if err == nil {
		t.Fatal("expected the gate node to halt the run")
	}
	if !strings.Contains(err.Error(), "approve") {
		t.Fatalf("halt error should name the gate node: %v", err)
	}
}

// --- live progress feed ------------------------------------------------------

// TestScheduler_ProgressWriter_EmitsPassAndFailLines proves the scheduler
// writes one live line per terminal node event to the injected ProgressWriter,
// so a long-running graph doesn't leave the terminal looking dead before the
// final ledger table prints. The failing node is asserted first.
func TestScheduler_ProgressWriter_EmitsPassAndFailLines(t *testing.T) {
	g := mustGraph(t, `
name: progress
nodes:
  - { id: bad, prompt: bad, success_check: { exit_zero: true } }
  - { id: good, prompt: good }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":  {Result: "x", ExitCode: 1},
		"good": pass("s-good", 0.05),
	})
	var buf bytes.Buffer
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true, ProgressWriter: &buf})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}

	feed := buf.String()
	if !strings.Contains(feed, "✗ bad  FAILED:") {
		t.Errorf("progress feed missing the failing node's FAILED line: %q", feed)
	}
	if !strings.Contains(feed, "✓ good  PASS") {
		t.Errorf("progress feed missing the passing node's PASS line: %q", feed)
	}
}

// --- test doubles -----------------------------------------------------------

// sequenceRunner returns a scripted sequence of outcomes, one per call — used to
// model a node that fails then recovers.
type sequenceRunner struct {
	mu       sync.Mutex
	calls    int
	outcomes []runner.NodeOutcome
}

func (r *sequenceRunner) Run(_ context.Context, _ runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	i := r.calls
	r.calls++
	if i >= len(r.outcomes) {
		i = len(r.outcomes) - 1
	}
	return r.outcomes[i], nil
}

// haltRunner fails one node immediately and blocks another until the context is
// cancelled — proving halt-on-fail interrupts an in-flight sibling.
type haltRunner struct {
	failKey  string
	blockKey string
	released chan struct{}
}

func (r *haltRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	switch spec.Prompt {
	case r.failKey:
		return runner.NodeOutcome{Result: "boom", ExitCode: 1}, nil
	case r.blockKey:
		<-ctx.Done()
		return runner.NodeOutcome{}, ctx.Err()
	default:
		return runner.NodeOutcome{Result: "PASS", ExitCode: 0}, nil
	}
}

// recordingRunner returns scripted outcomes keyed by the first token of the
// prompt and records every invocation so a test can inspect the interpolated
// prompt a node actually received.
type recordingRunner struct {
	mu        sync.Mutex
	outcomes  map[string]runner.NodeOutcome
	invoked   map[string]runner.NodeInvocation
	callOrder []string
}

func (r *recordingRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	key := strings.Fields(spec.Prompt)[0]
	r.mu.Lock()
	if r.invoked == nil {
		r.invoked = make(map[string]runner.NodeInvocation)
	}
	r.invoked[key] = spec
	r.callOrder = append(r.callOrder, key)
	outcome := r.outcomes[key]
	r.mu.Unlock()
	return outcome, nil
}

func (r *recordingRunner) promptFor(key string) string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invoked[key].Prompt
}

// --- helpers ----------------------------------------------------------------

func findRecord(led *ledger.RunLedger, nodeID string) ledger.Record {
	for _, rec := range led.Records() {
		if rec.NodeID == nodeID {
			return rec
		}
	}
	return ledger.Record{}
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
