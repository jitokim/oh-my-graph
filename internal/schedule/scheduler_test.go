package schedule

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
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

	rec := findRecord(led, "spendy")
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

	rec := findRecord(led, "spendy")
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
			if rec := findRecord(led, "solo"); rec.Verdict != wantVerdict {
				t.Errorf("verdict = %s, want %s", rec.Verdict, wantVerdict)
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
	if rec := findRecord(led, "independent"); rec.Verdict != ledger.VerdictPass {
		t.Errorf("independent verdict = %s, want PASS", rec.Verdict)
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

	rec := findRecord(led, "thrifty")
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

func (r *recordingRunner) invocationFor(key string) runner.NodeInvocation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invoked[key]
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
