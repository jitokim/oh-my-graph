package schedule

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
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
	if rec, ok := findRecord(led, "flaky"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Fatalf("flaky record = %+v (present=%v), want a PASS record", rec, ok)
	}
}

// TestScheduler_RetriedSessionNodeDetailSaysItStartedCold proves that when a
// session-handoff node passes on a retry, its record's Detail says the retry
// ran without the parent session — runNode clears ResumeSession on every
// retry, and that cold start must be visible in the ledger, snapshot and
// events, not silent.
func TestScheduler_RetriedSessionNodeDetailSaysItStartedCold(t *testing.T) {
	g := mustGraph(t, `
name: session-retry
nodes:
  - { id: dev, prompt: dev }
  - id: e2e
    prompt: e2e
    depends_on: [dev]
    handoff: session
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`)
	// The chain is sequential, so the scripted order is dev, then e2e's failing
	// first attempt, then its passing retry.
	flaky := &sequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", ExitCode: 0, SessionID: "s-dev"},
		{Result: "NOPE", ExitCode: 0},
		{Result: "PASS", ExitCode: 0, SessionID: "s-e2e-retry"},
	}}
	s, h, led := newHarness(t, flaky, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("expected recovery, got error: %v", err)
	}
	rec, ok := findRecord(led, "e2e")
	if !ok || rec.Verdict != ledger.VerdictPass {
		t.Fatalf("e2e record = %+v (present=%v), want a PASS record", rec, ok)
	}
	if want := "retry started fresh — parent session not resumed"; !strings.Contains(rec.Detail, want) {
		t.Fatalf("detail %q should carry %q", rec.Detail, want)
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

	// Concurrency: 2 is load-bearing wherever a blocking double needs two
	// nodes in flight at once: with a width of 1 the runner's cross-node
	// choreography deadlocks until the go test panic timeout. Stating it
	// explicitly decouples these tests from defaultConcurrency ever dropping.
	s, h, led := newHarness(t, r, Options{Concurrency: 2})
	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if halt.NodeID != "boom" {
		t.Fatalf("halt named node %q, want boom", halt.NodeID)
	}
	// The strict form: the sibling must be present (the haltRunner guarantees
	// it starts before the halt lands) and recorded as the cancellation FAIL —
	// verified x500 under -race. The old `== VerdictPass` shape was satisfiable
	// by the sibling never having run at all.
	rec, ok := findRecord(led, "sibling")
	if !ok {
		t.Fatal("the cancelled sibling was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail {
		t.Fatalf("cancelled sibling verdict = %q, want FAIL", rec.Verdict)
	}
}

// TestScheduler_CancelledSiblingDetailNamesTheHalt proves a sibling that
// halt-on-fail cancelled is recorded with the causal story — which failure
// halted the run — not the Go error string ("claude run: context canceled")
// its cancellation surfaces as.
func TestScheduler_CancelledSiblingDetailNamesTheHalt(t *testing.T) {
	g := mustGraph(t, `
name: halt-detail
nodes:
  - { id: boom, prompt: boom, success_check: { exit_zero: true } }
  - { id: sibling, prompt: sibling }
`)
	r := &haltRunner{failKey: "boom", blockKey: "sibling", released: make(chan struct{})}

	s, h, led := newHarness(t, r, Options{Concurrency: 2})
	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to halt")
	}

	rec, ok := findRecord(led, "sibling")
	if !ok {
		t.Fatal("the cancelled sibling was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail {
		t.Fatalf("sibling verdict = %q, want FAIL", rec.Verdict)
	}
	// The causal story, plus the honest-accounting label: the sibling was
	// killed mid-flight, so whatever it spent went unreported too.
	if want := `cancelled: run halted after node "boom" failed; cost unknown (killed before reporting)`; rec.Detail != want {
		t.Errorf("sibling detail = %q, want %q", rec.Detail, want)
	}
}

// TestScheduler_ExitZeroDetailNamesTheRunnerCause is the incident fix: a node
// whose subprocess died on a subscription session limit used to be recorded as
// only "exit code 1". When the runner captured WHY (NodeOutcome.FailureCause),
// the exit_zero detail must carry it — on both the implicit (empty
// success_check) and explicit exit_zero paths.
func TestScheduler_ExitZeroDetailNamesTheRunnerCause(t *testing.T) {
	g := mustGraph(t, `
name: exit-cause
nodes:
  - { id: implicit, prompt: implicit }
  - { id: explicit, prompt: explicit, success_check: { exit_zero: true } }
`)
	died := runner.NodeOutcome{ExitCode: 1, FailureCause: "You've hit your session limit"}
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"implicit": died, "explicit": died,
	})
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected both nodes to fail")
	}
	for _, id := range []string{"implicit", "explicit"} {
		rec, ok := findRecord(led, id)
		if !ok {
			t.Fatalf("%s was never recorded in the ledger", id)
		}
		if want := "exit code 1: You've hit your session limit"; !strings.Contains(rec.Detail, want) {
			t.Errorf("%s detail = %q, want it to contain %q", id, rec.Detail, want)
		}
	}
}

// TestScheduler_FailureDetailIsCappedAtSharedBound proves an arbitrarily long
// cause is bounded the moment it becomes a Detail — keeping the tail, where
// this codebase's long strings put the payload — so the ledger table and every
// events.jsonl line stay one readable line.
func TestScheduler_FailureDetailIsCappedAtSharedBound(t *testing.T) {
	g := mustGraph(t, `
name: cap
nodes:
  - { id: chatty, prompt: chatty }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"chatty": {ExitCode: 1, FailureCause: strings.Repeat("x", 1000) + "the actual complaint"},
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected chatty to fail")
	}
	rec, ok := findRecord(led, "chatty")
	if !ok {
		t.Fatal("chatty was never recorded in the ledger")
	}
	if got := len([]rune(rec.Detail)); got > maxDetailRunes+1 { // +1 for the "…" cut marker
		t.Errorf("detail is %d runes, want at most %d", got, maxDetailRunes+1)
	}
	if !strings.HasPrefix(rec.Detail, "…") || !strings.HasSuffix(rec.Detail, "the actual complaint") {
		t.Errorf("capping must mark the cut and keep the tail, got %q", rec.Detail)
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
	if rec, ok := findRecord(led, "independent"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent record = %+v (present=%v), want a PASS record", rec, ok)
	}
}

// TestScheduler_GraphOnFailContinuePrunesSubtree proves the graph's own
// `on_fail: continue` declaration is enough — no CLI flag — to prune only the
// failed subtree while an independent branch finishes: the graph itself now
// says what "always pass --continue-on-fail for independent-lane batches"
// used to leave to operator memory.
func TestScheduler_GraphOnFailContinuePrunesSubtree(t *testing.T) {
	g := mustGraph(t, `
name: graph-continue
on_fail: continue
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
	s, h, led := newHarness(t, fake, Options{}) // no --continue-on-fail flag

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
	if rec, ok := findRecord(led, "independent"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent record = %+v (present=%v), want a PASS record", rec, ok)
	}
}

// TestScheduler_ExplicitOnFailHaltStillHalts pins the default's spelled-out
// form: `on_fail: halt` with no CLI flag behaves exactly like today's
// undeclared graph — the first failure halts the run and the pruned dependent
// never launches.
func TestScheduler_ExplicitOnFailHaltStillHalts(t *testing.T) {
	g := mustGraph(t, `
name: graph-halt
on_fail: halt
nodes:
  - { id: bad, prompt: bad, success_check: { exit_zero: true } }
  - { id: independent, prompt: independent, depends_on: [bad] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":         {Result: "x", ExitCode: 1},
		"independent": pass("s", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var haltErr *HaltError
	if !errors.As(err, &haltErr) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if haltErr.NodeID != "bad" {
		t.Fatalf("halt named node %q, want bad", haltErr.NodeID)
	}
	if indexOf(fake.Calls(), "independent") != -1 {
		t.Errorf("dependent should never run after the halt; calls=%v", fake.Calls())
	}
}

// TestScheduler_FlagORsWithGraphOnFailHalt proves the precedence rule: the CLI
// --continue-on-fail flag ORs with the graph field, so an explicit
// `on_fail: halt` cannot cancel a flag the operator passed — either side
// saying continue means continue.
func TestScheduler_FlagORsWithGraphOnFailHalt(t *testing.T) {
	g := mustGraph(t, `
name: flag-or
on_fail: halt
nodes:
  - { id: bad, prompt: bad, success_check: { exit_zero: true } }
  - { id: independent, prompt: independent }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":         {Result: "x", ExitCode: 1},
		"independent": pass("s", 0),
	})
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError (flag ORs with graph halt), got %T: %v", err, err)
	}
	if indexOf(fake.Calls(), "independent") == -1 {
		t.Errorf("independent branch should have run under the flag; calls=%v", fake.Calls())
	}
	if rec, ok := findRecord(led, "independent"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent record = %+v (present=%v), want a PASS record", rec, ok)
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

// --- budget_usd enforcement (post-hoc) --------------------------------------

// TestScheduler_BudgetExceededHaltsRun proves a node whose actual cost exceeds
// its declared budget_usd fails exactly like a failed success_check: the run
// halts with a *HaltError naming it, the ledger row is FAIL, and its dependent
// never starts. This is the post-hoc guarantee — the overspend already happened,
// what enforcement buys is that nothing downstream spends on top of it.
func TestScheduler_BudgetExceededHaltsRun(t *testing.T) {
	g := mustGraph(t, `
name: over-budget
nodes:
  - { id: spendy, prompt: spendy, budget_usd: 0.10 }
  - { id: child, prompt: child, depends_on: [spendy] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": pass("s-spendy", 0.25),
		"child":  pass("s-child", 0.01),
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if halt.NodeID != "spendy" {
		t.Fatalf("halt named node %q, want spendy", halt.NodeID)
	}
	var budgetErr *NodeBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected halt to wrap *NodeBudgetError, got %T: %v", err, err)
	}
	if budgetErr.BudgetUSD != 0.10 || budgetErr.ActualUSD != 0.25 {
		t.Errorf("budget error = %+v, want budgeted 0.10 / actual 0.25", budgetErr)
	}
	if indexOf(fake.Calls(), "child") != -1 {
		t.Errorf("dependent of an over-budget node must never run; calls=%v", fake.Calls())
	}

	rec, ok := findRecord(led, "spendy")
	if !ok {
		t.Fatal("spendy was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail {
		t.Errorf("spendy verdict = %s, want FAIL", rec.Verdict)
	}
	if rec.BudgetUSD != 0.10 {
		t.Errorf("record BudgetUSD = %v, want 0.10", rec.BudgetUSD)
	}
	if !strings.Contains(rec.Detail, "exceeded budget_usd") {
		t.Errorf("detail should explain the overspend, got %q", rec.Detail)
	}
}

// TestScheduler_BudgetExceededStillPersistsArtifact proves a node that produced a
// real result before blowing its budget still leaves that artifact on disk. The
// budget verdict changes pass/fail only — it does not retract handoff output.
func TestScheduler_BudgetExceededStillPersistsArtifact(t *testing.T) {
	g := mustGraph(t, `
name: over-budget-artifact
nodes:
  - { id: spendy, prompt: spendy, budget_usd: 0.10 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": {Result: "useful-work-done", ExitCode: 0, SessionID: "s-spendy", TotalCostUSD: 0.25},
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the over-budget node to fail the run")
	}

	got, err := os.ReadFile(filepath.Join(runDir, "spendy.out"))
	if err != nil {
		t.Fatalf("artifact should survive a budget failure: %v", err)
	}
	if string(got) != "useful-work-done" {
		t.Fatalf("artifact content = %q, want the node's result", got)
	}
}

// --- budget_usd enforcement (native mid-run kill via --max-budget-usd) -------

// budgetKilled is a scripted outcome shaped like claude's own --max-budget-usd
// abort: a non-zero exit, the cost already spent, and BudgetExhausted set (which
// the real runner derives from the error_max_budget_usd envelope subtype). There
// is no result — the run was cut off before producing one.
func budgetKilled(sessionID string, spent float64) runner.NodeOutcome {
	return runner.NodeOutcome{SessionID: sessionID, Result: "", TotalCostUSD: spent, ExitCode: 1, BudgetExhausted: true}
}

// TestScheduler_NativeBudgetKillHaltsAsBudgetFailure proves a node that claude
// itself aborted for crossing --max-budget-usd fails as a *NodeBudgetError — not
// as the generic exit_zero failure its non-zero exit code would otherwise be —
// so the run halts naming it, the ledger row explains the overspend, its
// dependent never starts, and (unlike a post-hoc overspend) no artifact is left
// behind, because the run was interrupted before a result existed.
func TestScheduler_NativeBudgetKillHaltsAsBudgetFailure(t *testing.T) {
	g := mustGraph(t, `
name: native-kill
nodes:
  - { id: spendy, prompt: spendy, budget_usd: 0.10 }
  - { id: child, prompt: child, depends_on: [spendy] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": budgetKilled("s-spendy", 0.48),
		"child":  pass("s-child", 0.01),
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard})

	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) || halt.NodeID != "spendy" {
		t.Fatalf("expected *HaltError naming spendy, got %T: %v", err, err)
	}
	var budgetErr *NodeBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("a native budget kill must surface as *NodeBudgetError, got %T: %v", err, err)
	}
	if budgetErr.BudgetUSD != 0.10 || budgetErr.ActualUSD != 0.48 {
		t.Errorf("budget error = %+v, want budgeted 0.10 / actual 0.48", budgetErr)
	}
	if indexOf(fake.Calls(), "child") != -1 {
		t.Errorf("dependent of a budget-killed node must never run; calls=%v", fake.Calls())
	}

	rec, ok := findRecord(led, "spendy")
	if !ok {
		t.Fatal("spendy was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail || !strings.Contains(rec.Detail, "exceeded budget_usd") {
		t.Errorf("record = %+v, want FAIL naming the overspend", rec)
	}
	if _, statErr := os.Stat(filepath.Join(runDir, "spendy.out")); !os.IsNotExist(statErr) {
		t.Errorf("an interrupted budget-killed node has no result to persist; stat err = %v", statErr)
	}
}

// TestScheduler_NativeBudgetKillIgnoresNonzeroExitRetryPolicy is the retry-token
// guard for the native path: even though the kill exits non-zero, a pre-existing
// `retry: { on: [nonzero_exit] }` policy must NOT re-run it — retrying a
// budget-killed node spends the money again. It shares the post-hoc check's
// budget_exceeded token, so only an explicit opt-in retries it.
func TestScheduler_NativeBudgetKillIgnoresNonzeroExitRetryPolicy(t *testing.T) {
	g := mustGraph(t, `
name: native-kill-no-retry
nodes:
  - id: spendy
    prompt: spendy
    budget_usd: 0.10
    retry: { max: 2, on: [nonzero_exit, result_mismatch] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": budgetKilled("s-spendy", 0.48),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the budget-killed node to fail the run")
	}
	if got := fake.InvocationCount("spendy"); got != 1 {
		t.Fatalf("spendy invoked %d times, want 1 — a native budget kill must not "+
			"be retried under a nonzero_exit policy", got)
	}
}

// TestScheduler_BudgetBoundaries proves the enforcement edges: under budget and
// exactly at budget both pass, and a node that declares no budget_usd is never
// enforced no matter what it costs.
func TestScheduler_BudgetBoundaries(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		cost    float64
		wantErr bool
	}{
		{name: "under budget passes", yaml: "budget_usd: 0.50", cost: 0.20},
		{name: "exactly at budget passes", yaml: "budget_usd: 0.50", cost: 0.50},
		{name: "over budget fails", yaml: "budget_usd: 0.50", cost: 0.5001, wantErr: true},
		{name: "no budget declared is never enforced", yaml: "", cost: 99.0},
		{name: "zero budget means undeclared", yaml: "budget_usd: 0", cost: 99.0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := mustGraph(t, "name: boundary\nnodes:\n  - { id: solo, prompt: solo, "+tc.yaml+" }\n")
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"solo": pass("s-solo", tc.cost),
			})
			s, h, led := newHarness(t, fake, Options{})

			err := s.Run(context.Background(), g, h, led)
			if tc.wantErr && err == nil {
				t.Fatalf("cost %v vs %q: expected failure, got nil", tc.cost, tc.yaml)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("cost %v vs %q: expected pass, got %v", tc.cost, tc.yaml, err)
			}

			wantVerdict := ledger.VerdictPass
			if tc.wantErr {
				wantVerdict = ledger.VerdictFail
			}
			if rec, ok := findRecord(led, "solo"); !ok || rec.Verdict != wantVerdict {
				t.Errorf("record = %+v (present=%v), want verdict %s", rec, ok, wantVerdict)
			}
		})
	}
}

// TestScheduler_BudgetExceededIgnoresNonzeroExitRetryPolicy is the regression
// guard for the retry-cause split: a budget failure must NOT be swept up by a
// pre-existing `retry: { on: [nonzero_exit] }` policy. Retrying an over-budget
// node spends the money a second time, so it has to be an explicit opt-in.
func TestScheduler_BudgetExceededIgnoresNonzeroExitRetryPolicy(t *testing.T) {
	g := mustGraph(t, `
name: no-accidental-retry
nodes:
  - id: spendy
    prompt: spendy
    budget_usd: 0.10
    retry: { max: 2, on: [nonzero_exit, result_mismatch] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": pass("s-spendy", 0.25),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the over-budget node to fail the run")
	}
	if got := fake.InvocationCount("spendy"); got != 1 {
		t.Fatalf("spendy invoked %d times, want 1 — a budget failure must not "+
			"be retried under a nonzero_exit policy", got)
	}
}

// TestScheduler_BudgetExceededRetriesOnlyWhenOptedIn proves the flip side: a
// graph that explicitly lists budget_exceeded in retry.on does get re-run, and
// still fails once the attempts are exhausted.
func TestScheduler_BudgetExceededRetriesOnlyWhenOptedIn(t *testing.T) {
	g := mustGraph(t, `
name: opted-in-retry
nodes:
  - id: spendy
    prompt: spendy
    budget_usd: 0.10
    retry: { max: 1, on: [budget_exceeded] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy": pass("s-spendy", 0.25),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected failure after the retry was also over budget")
	}
	if got := fake.InvocationCount("spendy"); got != 2 {
		t.Fatalf("spendy invoked %d times, want 2 (initial + 1 opted-in retry)", got)
	}
}

// TestScheduler_BudgetExceededPrunesSubtreeOnContinueOnFail proves the budget
// verdict flows through the --continue-on-fail path the same way a
// success_check failure does: the subtree is pruned, independent branches run.
func TestScheduler_BudgetExceededPrunesSubtreeOnContinueOnFail(t *testing.T) {
	g := mustGraph(t, `
name: over-budget-continue
nodes:
  - { id: spendy, prompt: spendy, budget_usd: 0.10 }
  - { id: child, prompt: child, depends_on: [spendy] }
  - { id: independent, prompt: independent }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"spendy":      pass("s-spendy", 0.25),
		"child":       pass("s-child", 0),
		"independent": pass("s-ind", 0),
	})
	s, h, led := newHarness(t, fake, Options{ContinueOnFail: true})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}
	if !equalStrings(runFailed.FailedNodes, []string{"spendy"}) {
		t.Fatalf("failed nodes = %v, want [spendy]", runFailed.FailedNodes)
	}
	if indexOf(fake.Calls(), "child") != -1 {
		t.Errorf("pruned child should never run; calls=%v", fake.Calls())
	}
	if rec, ok := findRecord(led, "independent"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent record = %+v (present=%v), want a PASS record", rec, ok)
	}
}

// TestScheduler_WithinBudgetRecordsHeadroom proves a passing node with a declared
// budget carries the budget and its headroom into the ledger, so "ran at 99% of
// budget" is visible before it ever becomes a failure.
func TestScheduler_WithinBudgetRecordsHeadroom(t *testing.T) {
	g := mustGraph(t, `
name: headroom
nodes:
  - { id: thrifty, prompt: thrifty, budget_usd: 0.50 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"thrifty": pass("s-thrifty", 0.20),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	rec, ok := findRecord(led, "thrifty")
	if !ok {
		t.Fatal("thrifty was never recorded in the ledger")
	}
	if rec.BudgetUSD != 0.50 {
		t.Errorf("record BudgetUSD = %v, want 0.50", rec.BudgetUSD)
	}
	delta, declared := rec.BudgetDeltaUSD()
	if !declared || delta > -0.29 || delta < -0.31 {
		t.Errorf("delta = %v (declared=%v), want ~-0.30", delta, declared)
	}
	if !strings.Contains(rec.Detail, "under budget by") {
		t.Errorf("detail should report headroom, got %q", rec.Detail)
	}
}

// --- gate pause is the default (ADR 0003) ------------------------------------

// TestScheduler_GatePausesByDefault proves a graph reaching a gate node stops
// the run with a *PausedError naming it, rather than the v0.1 halt-on-refusal
// behaviour: pausing is not a failure.
func TestScheduler_GatePausesByDefault(t *testing.T) {
	g := mustGraph(t, `
name: with-gate
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s", 0)})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var paused *PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected *PausedError, got %T: %v", err, err)
	}
	if paused.GateID != "approve" {
		t.Fatalf("paused gate = %q, want approve", paused.GateID)
	}
	if !strings.Contains(err.Error(), "approve") {
		t.Fatalf("pause error should name the gate node: %v", err)
	}
}

// TestScheduler_PauseStopsNewLaunchesAndRecordsSnapshot proves a paused gate's
// dependents never launch, and that Run() calls Recorder.RecordPause naming
// the gate exactly once the drain has finished.
func TestScheduler_PauseStopsNewLaunchesAndRecordsSnapshot(t *testing.T) {
	g := mustGraph(t, `
name: pause-stop
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
  - { id: ship, prompt: ship, depends_on: [approve] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"a": pass("s-a", 0), "ship": pass("s-ship", 0),
	}}
	fr := newFakeRecorder()
	s, h, led := newHarness(t, rec, Options{Recorder: fr})

	err := s.Run(context.Background(), g, h, led)
	var paused *PausedError
	if !errors.As(err, &paused) || paused.GateID != "approve" {
		t.Fatalf("expected *PausedError naming approve, got %T: %v", err, err)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Error("a gate's dependent must never launch while the run is paused")
	}
	if !fr.pauseRecorded("approve") {
		t.Error("Recorder.RecordPause was not called for the paused gate")
	}
}

// TestScheduler_PauseDrainsInFlightSiblingsInsteadOfCancelling is the direct
// proof of ADR 0003's core claim: a pause does NOT cancel the shared context
// the way halt-on-fail does. An independent sibling that is genuinely in
// flight while the gate elsewhere decides to pause still runs to completion
// and is recorded as a real PASS — not interrupted with a context error the
// way TestScheduler_HaltOnFailCancelsSiblings proves a halt DOES interrupt
// one.
func TestScheduler_PauseDrainsInFlightSiblingsInsteadOfCancelling(t *testing.T) {
	g := mustGraph(t, `
name: pause-drain
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
  - { id: slow, prompt: slow }
`)
	r := &pauseDrainRunner{started: make(chan struct{}), release: make(chan struct{})}
	s, h, led := newHarness(t, r, Options{Concurrency: 2})

	done := make(chan error, 1)
	go func() { done <- s.Run(context.Background(), g, h, led) }()

	select {
	case <-r.started:
	case <-time.After(2 * time.Second):
		t.Fatal("the independent sibling never started")
	}
	// The sibling is now genuinely in flight while the gate independently
	// pauses the run (it has no dependency relationship with the gate at
	// all). Release it and prove it still completes normally.
	close(r.release)

	var runErr error
	select {
	case runErr = <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the sibling was released")
	}

	var paused *PausedError
	if !errors.As(runErr, &paused) {
		t.Fatalf("expected *PausedError, got %T: %v", runErr, runErr)
	}
	if paused.GateID != "approve" {
		t.Fatalf("paused at gate %q, want approve", paused.GateID)
	}

	slowRec, ok := findRecord(led, "slow")
	if !ok {
		t.Fatal("the drained sibling was never recorded in the ledger")
	}
	if slowRec.Verdict != ledger.VerdictPass {
		t.Fatalf("the drained sibling must complete as a real PASS, got verdict %q (detail %q)", slowRec.Verdict, slowRec.Detail)
	}
	if slowRec.SessionID != "s-slow" {
		t.Fatalf("the drained sibling's real outcome was not recorded: %+v", slowRec)
	}
}

// TestScheduler_RecordPauseFailureIsFatal proves a pause snapshot write
// failure is NOT reported as a clean *PausedError: an unpersisted pause is an
// unrecoverable stop (DESIGN.md).
func TestScheduler_RecordPauseFailureIsFatal(t *testing.T) {
	g := mustGraph(t, `
name: pause-fatal
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0)})
	fr := newFakeRecorder()
	fr.failRecordPause = errors.New("disk full")
	s, h, led := newHarness(t, fake, Options{Recorder: fr})

	err := s.Run(context.Background(), g, h, led)
	var paused *PausedError
	if errors.As(err, &paused) {
		t.Fatal("a pause whose snapshot write failed must not be reported as a clean *PausedError")
	}
	if err == nil {
		t.Fatal("expected an error when the pause snapshot write fails")
	}
}

// --- gate approve / reject via a resumed leg's RecordedController ----------

// TestScheduler_GateApprovedRunsDependents proves an approved gate (as a
// resumed leg's RecordedController would answer) records a PASS and lets its
// dependents run — the same shape resume's scheduler.Run sees.
func TestScheduler_GateApprovedRunsDependents(t *testing.T) {
	g := mustGraph(t, `
name: gate-approve
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
  - { id: ship, prompt: ship, depends_on: [approve] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"a": pass("s-a", 0), "ship": pass("s-ship", 0),
	}}
	controller := gate.NewRecordedController(map[string]gate.Decision{"approve": gate.DecisionApprove})
	fr := newFakeRecorder()
	s, h, led := newHarness(t, rec, Options{Gate: controller, Recorder: fr})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.invocationFor("ship").Prompt == "" {
		t.Error("an approved gate's dependent must run")
	}
	gateRec, ok := findRecord(led, "approve")
	if !ok {
		t.Fatal("the approved gate was never recorded in the ledger")
	}
	if gateRec.Verdict != ledger.VerdictPass {
		t.Errorf("approved gate verdict = %s, want PASS", gateRec.Verdict)
	}
	if fr.gateDecisions["approve"] != runstate.GateApprove {
		t.Errorf("recorder gate decision = %q, want approve", fr.gateDecisions["approve"])
	}
}

// TestScheduler_GateRejectedPrunesSubtreeRegardlessOfContinueOnFail proves a
// rejected gate always prunes its own subtree — never a *HaltError, even with
// --continue-on-fail explicitly off — while an independent branch still
// finishes (DESIGN.md, "It is not a crash").
func TestScheduler_GateRejectedPrunesSubtreeRegardlessOfContinueOnFail(t *testing.T) {
	g := mustGraph(t, `
name: gate-reject
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
  - { id: ship, prompt: ship, depends_on: [approve] }
  - { id: independent, prompt: independent }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0), "ship": pass("s-ship", 0), "independent": pass("s-ind", 0),
	})
	controller := gate.NewRecordedController(map[string]gate.Decision{"approve": gate.DecisionReject})
	s, h, led := newHarness(t, fake, Options{Gate: controller, ContinueOnFail: false})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError (never a halt), got %T: %v", err, err)
	}
	if !equalStrings(runFailed.FailedNodes, []string{"approve"}) {
		t.Fatalf("failed nodes = %v, want [approve]", runFailed.FailedNodes)
	}
	if indexOf(fake.Calls(), "ship") != -1 {
		t.Error("a rejected gate's dependent must never run")
	}
	if indexOf(fake.Calls(), "independent") == -1 {
		t.Error("an independent branch must still finish after a gate rejection")
	}
	gateRec, ok := findRecord(led, "approve")
	if !ok {
		t.Fatal("the rejected gate was never recorded in the ledger")
	}
	if gateRec.Verdict != ledger.VerdictFail {
		t.Errorf("rejected gate verdict = %s, want FAIL", gateRec.Verdict)
	}
}

// TestScheduler_RecordedControllerPausesAtNextUndecidedGate proves the
// "multiple gates -> multiple resumes" shape end to end at the scheduler
// level: a RecordedController that only knows about gate1 lets the run
// proceed past it and pause again at gate2.
func TestScheduler_RecordedControllerPausesAtNextUndecidedGate(t *testing.T) {
	g := mustGraph(t, `
name: multi-gate
nodes:
  - { id: a, prompt: a }
  - { id: gate1, type: gate, depends_on: [a] }
  - { id: b, prompt: b, depends_on: [gate1] }
  - { id: gate2, type: gate, depends_on: [b] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0), "b": pass("s-b", 0)})
	controller := gate.NewRecordedController(map[string]gate.Decision{"gate1": gate.DecisionApprove})
	s, h, led := newHarness(t, fake, Options{Gate: controller})

	err := s.Run(context.Background(), g, h, led)
	var paused *PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected *PausedError, got %T: %v", err, err)
	}
	if paused.GateID != "gate2" {
		t.Fatalf("paused at %q, want gate2 (gate1 was already approved)", paused.GateID)
	}
	if indexOf(fake.Calls(), "b") == -1 {
		t.Error("b should have run after gate1 was approved")
	}
}

// --- snapshot recording happens after every node, not only at gates --------

// TestScheduler_RecorderRecordsEveryNodeWithArtifactPath proves RecordNode is
// called for an ordinary (non-gate) node too, carrying the same
// verdict/session/cost the ledger got plus the artifact path Handoff
// persisted — the datum a resumed leg's Handoff.Seed needs.
func TestScheduler_RecorderRecordsEveryNodeWithArtifactPath(t *testing.T) {
	g := mustGraph(t, `
name: snapshot
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.10), "b": pass("s-b", 0.05),
	})
	fr := newFakeRecorder()
	s, h, led := newHarness(t, fake, Options{Recorder: fr})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	recA, ok := fr.recordFor("a")
	if !ok {
		t.Fatal("Recorder never saw node a")
	}
	if recA.Verdict != runstate.VerdictPass || recA.SessionID != "s-a" || recA.CostUSD != 0.10 {
		t.Fatalf("node a snapshot record = %+v, want Pass/s-a/0.10", recA)
	}
	if recA.ArtifactPath == "" {
		t.Fatal("a passing node's snapshot record must carry its artifact path")
	}
}

// TestScheduler_RecorderWriteFailureMidRunIsNonFatal proves a snapshot write
// failure on an ordinary node's completion does not fail the run — the ledger
// remains the run's authority mid-run — but is warned on the progress feed.
func TestScheduler_RecorderWriteFailureMidRunIsNonFatal(t *testing.T) {
	g := mustGraph(t, `
name: snapshot-fail
nodes:
  - { id: a, prompt: a }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0)})
	fr := newFakeRecorder()
	fr.failRecordNode = errors.New("disk full")
	var buf bytes.Buffer
	s, h, led := newHarness(t, fake, Options{Recorder: fr, ProgressWriter: &buf})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("a snapshot write failure mid-run must not fail the run: %v", err)
	}
	if rec, ok := findRecord(led, "a"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Fatalf("ledger record = %+v (present=%v), want a PASS record even though the snapshot write failed", rec, ok)
	}
	if !strings.Contains(buf.String(), "snapshot write failed") {
		t.Errorf("the snapshot write failure must be warned on the progress feed: %q", buf.String())
	}
}

// --- resume seeds from CompletedNodes, not Roots ----------------------------

// TestScheduler_ResumeSeedsFromCompletedNodesNotRoots proves the mechanism
// DESIGN.md assigns to resume: Options.CompletedNodes seeds the initial ready
// set from graph.ReadyGiven(completed) rather than graph.Roots(), so a node
// an earlier leg already finished is never re-run (and re-paid for) — only
// its not-yet-run dependent launches.
func TestScheduler_ResumeSeedsFromCompletedNodesNotRoots(t *testing.T) {
	g := mustGraph(t, `
name: resume-seed
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{"b": pass("s-b", 0)}}
	s, h, led := newHarness(t, rec, Options{CompletedNodes: map[string]bool{"a": true}})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.invocationFor("a").Prompt != "" {
		t.Error("a node already marked completed must not be re-run on resume")
	}
	if rec.invocationFor("b").Prompt == "" {
		t.Error("b should run: its only parent is already completed")
	}
}

// TestScheduler_ResumeSeedsInDegreeAgainstCompletedParents proves a fan-in
// node whose parents are a mix of "completed in an earlier leg" and "not yet
// completed" waits correctly: it must not launch until the still-pending
// parent finishes THIS leg, even though its other parent will never run
// again.
func TestScheduler_ResumeSeedsInDegreeAgainstCompletedParents(t *testing.T) {
	g := mustGraph(t, `
name: resume-fanin
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - { id: join, prompt: join, depends_on: [a, b] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"b": pass("s-b", 0), "join": pass("s-join", 0),
	}}
	s, h, led := newHarness(t, rec, Options{CompletedNodes: map[string]bool{"a": true}})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.invocationFor("a").Prompt != "" {
		t.Error("a must not be re-run")
	}
	if rec.invocationFor("join").Prompt == "" {
		t.Error("join should run once its remaining parent (b) completes this leg")
	}
}

// --- resume never relaunches a settled-failed node --------------------------

// TestScheduler_ResumeSkipsSettledFailedNode proves the fix for the
// re-run-and-double-record bug: a node that FAILED under --continue-on-fail
// on an earlier leg is absent from CompletedNodes by design (see
// runstate.Snapshot.CompletedNodes), so graph.ReadyGiven(completedNodes) would
// hand it right back as ready — its own dependencies were satisfied for it to
// have run at all. Options.SettledNodes is what stops it from actually
// launching a second time.
func TestScheduler_ResumeSkipsSettledFailedNode(t *testing.T) {
	g := mustGraph(t, `
name: resume-settled-fail
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`)
	rec := &recordingRunner{}
	s, h, led := newHarness(t, rec, Options{
		ContinueOnFail: true,
		// "a" failed on an earlier leg: CompletedNodes (pass-only) does not
		// include it, only SettledNodes does.
		CompletedNodes: map[string]bool{},
		SettledNodes:   map[string]bool{"a": true},
	})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.invocationFor("a").Prompt != "" {
		t.Error("a settled-failed node must never be launched again on resume")
	}
	if rec.invocationFor("b").Prompt != "" {
		t.Error("b must stay blocked: a never completed this leg, only failed, so b's dependency is still unsatisfied")
	}
}

// TestScheduler_ResumeSettledFailedNodeSubtreeStaysPrunedAcrossFanIn proves
// SettledNodes only gates the failed node's OWN relaunch — it must not also
// leak into ReadyGiven's dependency-satisfaction question, or a fan-in node
// with one settled-failed parent and one still-pending parent would wrongly
// read as unblocked the moment the pending parent finishes. join here must
// stay pruned forever, exactly as it would within a single leg.
func TestScheduler_ResumeSettledFailedNodeSubtreeStaysPrunedAcrossFanIn(t *testing.T) {
	g := mustGraph(t, `
name: resume-settled-fail-fanin
nodes:
  - { id: a, prompt: a }
  - { id: other, prompt: other }
  - { id: join, prompt: join, depends_on: [a, other] }
`)
	rec := &recordingRunner{}
	s, h, led := newHarness(t, rec, Options{
		ContinueOnFail: true,
		CompletedNodes: map[string]bool{},
		SettledNodes:   map[string]bool{"a": true},
	})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec.invocationFor("a").Prompt != "" {
		t.Error("a settled-failed node must never re-run")
	}
	if rec.invocationFor("other").Prompt == "" {
		t.Error("other (a's independent sibling, not yet run in any leg) should still launch")
	}
	if rec.invocationFor("join").Prompt != "" {
		t.Error("join must stay blocked: one of its parents (a) only failed, it never completed")
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

// haltRunner fails one node and blocks another until the context is cancelled
// — proving halt-on-fail interrupts an in-flight sibling. The fail node WAITS
// until the blocked sibling has actually started (released): without that
// ordering the halt can land before the scheduler launches the sibling at
// all, and a never-started node is (by design) never recorded — which made
// the sibling-detail assertion flake in CI.
type haltRunner struct {
	failKey   string
	blockKey  string
	released  chan struct{}
	startOnce sync.Once
}

func (r *haltRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	switch spec.Prompt {
	case r.failKey:
		// Fail only once the sibling is provably in flight, so the halt
		// always has an in-flight sibling to cancel.
		select {
		case <-r.released:
		case <-ctx.Done():
			return runner.NodeOutcome{}, ctx.Err()
		}
		return runner.NodeOutcome{Result: "boom", ExitCode: 1}, nil
	case r.blockKey:
		r.startOnce.Do(func() { close(r.released) })
		<-ctx.Done()
		return runner.NodeOutcome{}, ctx.Err()
	default:
		return runner.NodeOutcome{Result: "PASS", ExitCode: 0}, nil
	}
}

// pauseDrainRunner blocks the "slow" node on release, closing started the
// moment it begins — the counterpart to haltRunner's blockKey, but released
// explicitly by the test instead of by ctx cancellation, because a pause
// deliberately never cancels ctx (that's the entire point being proved).
type pauseDrainRunner struct {
	started   chan struct{}
	release   chan struct{}
	startOnce sync.Once
}

func (r *pauseDrainRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	if spec.Prompt == "slow" {
		r.startOnce.Do(func() { close(r.started) })
		select {
		case <-r.release:
			return runner.NodeOutcome{Result: "PASS", ExitCode: 0, SessionID: "s-slow", TotalCostUSD: 0.02}, nil
		case <-ctx.Done():
			return runner.NodeOutcome{}, ctx.Err()
		}
	}
	return runner.NodeOutcome{Result: "PASS", ExitCode: 0, SessionID: "s-" + spec.Prompt}, nil
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

func (r *recordingRunner) invocationFor(key string) runner.NodeInvocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invoked[key]
}

// fakeRecorder is the scripted schedule.Recorder tests inject to observe the
// snapshot-facing calls the Scheduler makes without touching a filesystem —
// the same role FakeRunner plays for NodeRunner and FakeVerifier for Verifier.
// Its two failXxx fields let a test script a write failure to prove the
// fatal/non-fatal split between RecordPause and the other two methods.
type fakeRecorder struct {
	mu              sync.Mutex
	nodes           map[string]runstate.NodeRecord
	gateDecisions   map[string]runstate.GateDecision
	pausedGates     map[string]bool
	failRecordNode  error
	failRecordPause error
}

func newFakeRecorder() *fakeRecorder {
	return &fakeRecorder{
		nodes:         make(map[string]runstate.NodeRecord),
		gateDecisions: make(map[string]runstate.GateDecision),
		pausedGates:   make(map[string]bool),
	}
}

func (f *fakeRecorder) RecordNode(nodeID string, rec runstate.NodeRecord) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nodes[nodeID] = rec
	return f.failRecordNode
}

func (f *fakeRecorder) RecordGateDecision(gateNodeID string, decision runstate.GateDecision) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gateDecisions[gateNodeID] = decision
	return nil
}

func (f *fakeRecorder) RecordPause(gateNodeID string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.pausedGates[gateNodeID] = true
	return f.failRecordPause
}

func (f *fakeRecorder) recordFor(nodeID string) (runstate.NodeRecord, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	rec, ok := f.nodes[nodeID]
	return rec, ok
}

func (f *fakeRecorder) pauseRecorded(gateNodeID string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.pausedGates[gateNodeID]
}

// --- execution ceiling ------------------------------------------------------

// TestScheduler_ForwardsPerNodeToolPolicy proves the Scheduler routes each
// node's own policy into its invocation. The ceiling is per node, so a
// mis-keyed lookup would silently hand one node another node's (weaker) policy
// — this pins that each node gets exactly its own, whole.
func TestScheduler_ForwardsPerNodeToolPolicy(t *testing.T) {
	// Fan-out, not a chain: the auto-mode shape runs siblings concurrently, so
	// this also puts two goroutines on the same ceiling map under -race.
	g := mustGraph(t, `
name: planned
nodes:
  - { id: root, prompt: root, allowed_tools: [Read] }
  - { id: scan, prompt: scan, depends_on: [root], allowed_tools: [Read] }
  - { id: edit, prompt: edit, depends_on: [root], allowed_tools: [Edit] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"root": pass("s-root", 0), "scan": pass("s-scan", 0), "edit": pass("s-edit", 0),
	}}
	none := ""
	s, h, led := newHarness(t, rec, Options{ToolPolicies: map[string]runner.ToolPolicy{
		"root": {AllowedTools: []string{"Read"}, DisallowedTools: []string{"Bash"}},
		"scan": {AllowedTools: []string{"Read"}, DisallowedTools: []string{"Bash", "Write"}, SettingSources: &none, StrictMCPConfig: true, Tools: []string{"Read"}},
		"edit": {AllowedTools: []string{"Edit"}, DisallowedTools: []string{"Bash", "WebFetch"}},
	}})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := rec.invocationFor("scan").Policy.DisallowedTools; !equalStrings(got, []string{"Bash", "Write"}) {
		t.Errorf("scan denied %v, want [Bash Write]", got)
	}
	if got := rec.invocationFor("edit").Policy.DisallowedTools; !equalStrings(got, []string{"Bash", "WebFetch"}) {
		t.Errorf("edit denied %v, want [Bash WebFetch]", got)
	}
	// The whole policy travels, not just the deny list: dropping a layer in
	// transit is exactly the failure ToolPolicy exists to prevent.
	scan := rec.invocationFor("scan").Policy
	if scan.SettingSources == nil || *scan.SettingSources != "" {
		t.Errorf("scan lost its isolation layer: SettingSources = %v", scan.SettingSources)
	}
	if !scan.StrictMCPConfig || !equalStrings(scan.Tools, []string{"Read"}) {
		t.Errorf("scan lost a ceiling layer in transit: %+v", scan)
	}
}

// TestScheduler_HandWrittenGraphGetsNoCeiling is the regression guard for the
// `run` path: a hand-written graph is the user's own reviewed artifact and must
// keep running under the user's own settings, hooks, MCP servers and tool
// permissions. Options.ToolPolicies is nil there, and the node must fall back
// to a policy carrying its OWN allowed_tools and no ceiling layer at all — so
// no --setting-sources, --tools, --strict-mcp-config or --disallowedTools is
// ever built for it.
func TestScheduler_HandWrittenGraphGetsNoCeiling(t *testing.T) {
	g := mustGraph(t, `
name: handwritten
nodes:
  - { id: only, prompt: only, allowed_tools: [Read, "Bash(make *)"] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{"only": pass("s-only", 0)}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	policy := rec.invocationFor("only").Policy
	if len(policy.DisallowedTools) != 0 {
		t.Errorf("hand-written node got a deny list %v, want none", policy.DisallowedTools)
	}
	if policy.SettingSources != nil {
		t.Errorf("hand-written node got settings isolation %q; its own settings are the intended policy", *policy.SettingSources)
	}
	if policy.Tools != nil {
		t.Errorf("hand-written node got tool narrowing %v, want none", policy.Tools)
	}
	if policy.StrictMCPConfig {
		t.Error("hand-written node lost its MCP servers, which the `run` path must never do")
	}
	// The declared tools must still be forwarded — the guard subtracts nothing
	// from the `run` path.
	if !equalStrings(policy.AllowedTools, []string{"Read", "Bash(make *)"}) {
		t.Errorf("allowed tools = %v, want the node's own list", policy.AllowedTools)
	}
}

// TestScheduler_MissingPolicyUnderAnImposedCeilingFailsClosed pins the
// direction this fails in. When a ceiling IS imposed (a non-nil policy map) but
// some node has no entry, the tempting behaviour — fall back to the node's own
// declaration — is precisely the outcome the ceiling exists to prevent: one
// unreviewed planned node running with the user's full standing grants while
// every sibling is capped, silently, in a run that then reports success.
//
// Unreachable through coordinator.Plan today, which builds an entry per node.
// It becomes reachable the moment a policy map is rebuilt from somewhere else —
// a resumed run rehydrating from a snapshot, say — which is exactly when nobody
// is looking.
func TestScheduler_MissingPolicyUnderAnImposedCeilingFailsClosed(t *testing.T) {
	g := mustGraph(t, `
name: planned
nodes:
  - { id: capped, prompt: capped, allowed_tools: [Read] }
  - { id: forgotten, prompt: forgotten, allowed_tools: [Read, "Bash(git *)"] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"capped": pass("s-capped", 0), "forgotten": pass("s-forgotten", 0),
	}}
	// A non-nil map that covers only one of the two nodes.
	s, h, led := newHarness(t, rec, Options{ToolPolicies: map[string]runner.ToolPolicy{
		"capped": {AllowedTools: []string{"Read"}, DisallowedTools: []string{"Bash"}},
	}})

	err := s.Run(context.Background(), g, h, led)
	if err == nil {
		t.Fatal("a node with no policy under an imposed ceiling must fail the run, not run uncapped")
	}
	var missing *MissingToolPolicyError
	if !errors.As(err, &missing) {
		t.Fatalf("err = %v, want *MissingToolPolicyError", err)
	}
	if missing.NodeID != "forgotten" {
		t.Errorf("error names node %q, want forgotten", missing.NodeID)
	}
	// The uncapped node must never have reached the runner.
	if rec.invocationFor("forgotten").Prompt != "" {
		t.Error("the node with no ceiling was executed anyway")
	}
}

// TestScheduler_ImposedPolicyIsNotMergedWithTheNodeDeclaration pins the rule
// that an imposed policy is COMPLETE. If the scheduler helpfully merged the
// node's own allowed_tools over the policy it was handed, a coordinator bug
// that forgot layer 2 would be invisible here and would reappear as a node
// running with tools the ceiling never granted. The imposed policy wins
// wholesale — including when it deliberately grants less than the node asked.
func TestScheduler_ImposedPolicyIsNotMergedWithTheNodeDeclaration(t *testing.T) {
	g := mustGraph(t, `
name: planned
nodes:
  - { id: only, prompt: only, allowed_tools: [Read, Edit, Write] }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{"only": pass("s-only", 0)}}
	s, h, led := newHarness(t, rec, Options{ToolPolicies: map[string]runner.ToolPolicy{
		"only": {AllowedTools: []string{"Read"}},
	}})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := rec.invocationFor("only").Policy.AllowedTools; !equalStrings(got, []string{"Read"}) {
		t.Errorf("imposed policy was merged with the node's declaration: allowed = %v, want [Read]", got)
	}
}

// TestScheduler_NodeAgentReachesInvocation proves a node's `agent:` YAML field
// survives buildInvocation and arrives at the NodeRunner as
// NodeInvocation.Agent — the plumbing that lets a hand-written node run as the
// user's own Claude Code subagent via ClaudeCLIRunner's `--agent <name>`.
func TestScheduler_NodeAgentReachesInvocation(t *testing.T) {
	g := mustGraph(t, `
name: agent-node
nodes:
  - { id: review, prompt: review, agent: code-reviewer }
  - { id: plain, prompt: plain }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"review": pass("s-r", 0), "plain": pass("s-p", 0),
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := rec.invocationFor("review").Agent; got != "code-reviewer" {
		t.Errorf("agent for review node = %q, want code-reviewer", got)
	}
	if got := rec.invocationFor("plain").Agent; got != "" {
		t.Errorf("agent for node with no agent: field = %q, want empty", got)
	}
}

// TestScheduler_NodeTimeoutReachesInvocation proves a node's `timeout:` YAML
// field survives buildInvocation and arrives at the NodeRunner as
// NodeInvocation.Timeout, already parsed — the plumbing that lets a
// legitimately long node replace ClaudeCLIRunner's 20m default (ADR 0007). A
// node that declared none carries zero, which the runner reads as "apply the
// default".
func TestScheduler_NodeTimeoutReachesInvocation(t *testing.T) {
	g := mustGraph(t, `
name: long-node
nodes:
  - { id: slow, prompt: slow, timeout: 45m }
  - { id: plain, prompt: plain }
`)
	rec := &recordingRunner{outcomes: map[string]runner.NodeOutcome{
		"slow": pass("s-s", 0), "plain": pass("s-p", 0),
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := rec.invocationFor("slow").Timeout; got != 45*time.Minute {
		t.Errorf("timeout for slow node = %s, want 45m", got)
	}
	if got := rec.invocationFor("plain").Timeout; got != 0 {
		t.Errorf("timeout for node with no timeout: field = %s, want 0", got)
	}
}

// --- honest accounting for killed nodes (run 20260802-062446) ---------------

// TestScheduler_KilledNodeDetailSaysCostUnknown pins the honest-accounting
// rule: a node killed by a context (its own timeout here) died before printing
// the envelope that carries total_cost_usd, so its ledger row must say the
// cost is unknown instead of implying the $0.0000 it records was the spend.
func TestScheduler_KilledNodeDetailSaysCostUnknown(t *testing.T) {
	g := mustGraph(t, `
name: killed
nodes:
  - { id: dev, prompt: dev }
`)
	fake := runner.NewFakeRunner(nil)
	fake.InjectError("dev", fmt.Errorf("claude run: timed out after 100ms (node timeout): %w", context.DeadlineExceeded))
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the killed node to fail the run")
	}

	rec, ok := findRecord(led, "dev")
	if !ok {
		t.Fatal("killed node has no ledger record")
	}
	if rec.Verdict != ledger.VerdictFail {
		t.Fatalf("killed node verdict = %s, want %s", rec.Verdict, ledger.VerdictFail)
	}
	if rec.CostUSD != 0 {
		t.Fatalf("killed node cost = %v, want 0 (nothing was reported)", rec.CostUSD)
	}
	if !strings.Contains(rec.Detail, "timed out after 100ms (node timeout)") {
		t.Errorf("detail lost the timeout cause, got %q", rec.Detail)
	}
	if !strings.Contains(rec.Detail, "cost unknown (killed before reporting)") {
		t.Errorf("detail must say the cost is unknown, got %q", rec.Detail)
	}
}

// TestScheduler_SpawnFailureDetailHasNoCostUnknownLabel is the boundary: a
// child that never spawned genuinely cost nothing, so its $0.0000 is the
// truth and the unknown-cost label must not appear.
func TestScheduler_SpawnFailureDetailHasNoCostUnknownLabel(t *testing.T) {
	g := mustGraph(t, `
name: unspawnable
nodes:
  - { id: dev, prompt: dev }
`)
	fake := runner.NewFakeRunner(nil)
	fake.InjectError("dev", errors.New("claude run: spawn failed: exec: \"claude\": executable file not found"))
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the unspawnable node to fail the run")
	}

	rec, ok := findRecord(led, "dev")
	if !ok {
		t.Fatal("unspawnable node has no ledger record")
	}
	if strings.Contains(rec.Detail, "cost unknown") {
		t.Errorf("a spawn failure spent nothing; its detail must not claim unknown cost, got %q", rec.Detail)
	}
}

// --- helpers ----------------------------------------------------------------

// findRecord reports whether the ledger holds a record for nodeID. Callers
// must state explicitly whether they mean "absent" or "present with verdict X"
// — a zero-value Record is indistinguishable from a never-recorded node, which
// is exactly how an assertion gets silently satisfied by the node not running.
func findRecord(led *ledger.RunLedger, nodeID string) (ledger.Record, bool) {
	for _, rec := range led.Records() {
		if rec.NodeID == nodeID {
			return rec, true
		}
	}
	return ledger.Record{}, false
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
