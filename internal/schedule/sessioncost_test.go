package schedule

import (
	"context"
	"math"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// Session-cost correction (#398): claude's total_cost_usd is cumulative for a
// resumed session, so a `handoff: session` node is booked with its parent's
// raw figure subtracted. Every fixture here is a FakeRunner under the default
// (claude) runtime, which reports CostIncludesResumedSession exactly when the
// invocation resumed — no real spawn anywhere.

const sessionCostUnknownNote = "session-cost correction not applied: parent's raw cost unknown"

func nearly(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// runSessionGraph runs yaml against r and returns the ledger, failing the
// test on any run error.
func runSessionGraph(t *testing.T, yaml string, r *runner.FakeRunner) *ledger.RunLedger {
	t.Helper()
	s, h, led := newHarness(t, r, Options{})
	if err := s.Run(context.Background(), mustGraph(t, yaml), h, led); err != nil {
		t.Fatalf("run: %v", err)
	}
	return led
}

func mustRecord(t *testing.T, led *ledger.RunLedger, nodeID string) ledger.Record {
	t.Helper()
	rec, ok := findRecord(led, nodeID)
	if !ok {
		t.Fatalf("no ledger record for %q", nodeID)
	}
	return rec
}

// (a) A session child's cumulative total is booked as its own delta, with the
// raw figure kept beside it.
func TestSessionCost_ChildIsBookedWithItsOwnDelta(t *testing.T) {
	led := runSessionGraph(t, `
name: session-delta
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`, runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": pass("s-dev", 0.40),
		"e2e": pass("s-dev", 0.65),
	}))

	dev := mustRecord(t, led, "dev")
	if !nearly(dev.CostUSD, 0.40) || !nearly(dev.ReportedCostUSD, 0.40) || dev.CostUnknown {
		t.Fatalf("dev (cold start) = cost %v reported %v unknown %v, want 0.40 / 0.40 / false", dev.CostUSD, dev.ReportedCostUSD, dev.CostUnknown)
	}
	e2e := mustRecord(t, led, "e2e")
	if e2e.CostUnknown || !nearly(e2e.CostUSD, 0.25) {
		t.Fatalf("e2e cost = %v (unknown %v), want its own delta 0.25", e2e.CostUSD, e2e.CostUnknown)
	}
	if !nearly(e2e.ReportedCostUSD, 0.65) || e2e.ReportedCostUnknown {
		t.Fatalf("e2e reported = %v (unknown %v), want the raw cumulative 0.65", e2e.ReportedCostUSD, e2e.ReportedCostUnknown)
	}
}

// (b) and (e) A chain of two session children subtracts each one's own
// parent, and the run total is the sum of the corrected rows — which for a
// single session chain is the last raw figure, not the sum of the raw ones.
func TestSessionCost_ChainOfSessionChildren(t *testing.T) {
	led := runSessionGraph(t, `
name: session-chain
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a], handoff: session }
  - { id: c, prompt: c, depends_on: [b], handoff: session }
`, runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0.10),
		"b": pass("s-a", 0.25),
		"c": pass("s-a", 0.45),
	}))

	want := map[string]float64{"a": 0.10, "b": 0.15, "c": 0.20}
	sum := 0.0
	for id, cost := range want {
		rec := mustRecord(t, led, id)
		if rec.CostUnknown || !nearly(rec.CostUSD, cost) {
			t.Errorf("%s cost = %v (unknown %v), want %v", id, rec.CostUSD, rec.CostUnknown, cost)
		}
		sum += rec.CostUSD
	}
	if led.CostUnknown() {
		t.Fatalf("run total reported unknown, want known")
	}
	if got := led.TotalCost(); !nearly(got, sum) || !nearly(got, 0.45) {
		t.Fatalf("run total = %v, want the corrected sum %v (= 0.45), not the raw sum 0.80", got, sum)
	}
}

// (c) A retry of a session node starts cold, so its reported figure is its
// own and must NOT be subtracted.
func TestSessionCost_ColdRetryIsNotSubtracted(t *testing.T) {
	r := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": pass("s-dev", 0.40),
		// The first attempt resumes dev's session and fails its check; the
		// retry starts cold and passes.
		"e2e":      {SessionID: "s-dev", Result: "NOPE", TotalCostUSD: 0.70},
		"e2e-cold": pass("s-e2e-retry", 0.30),
	})
	r.KeyFn = func(spec runner.NodeInvocation) string {
		key := nodePromptKey(spec)
		if key == "e2e" && spec.ResumeSession == "" {
			return "e2e-cold"
		}
		return key
	}
	led := runSessionGraph(t, `
name: session-cold-retry
nodes:
  - { id: dev, prompt: dev }
  - id: e2e
    prompt: e2e
    depends_on: [dev]
    handoff: session
    success_check: { result_matches: "PASS" }
    retry: { max: 1, on: [result_mismatch] }
`, r)

	if got := r.InvocationCount("e2e-cold"); got != 1 {
		t.Fatalf("cold retry invoked %d times, want 1", got)
	}
	e2e := mustRecord(t, led, "e2e")
	if e2e.Verdict != ledger.VerdictPass || e2e.CostUnknown || !nearly(e2e.CostUSD, 0.30) || !nearly(e2e.ReportedCostUSD, 0.30) {
		t.Fatalf("e2e = %+v, want a PASS costing its raw 0.30 (cold, not 0.30-0.40)", e2e)
	}
	if strings.Contains(e2e.Detail, "session-cost correction") {
		t.Fatalf("cold retry detail %q mentions a session-cost correction", e2e.Detail)
	}
}

// (d) An unknown parent raw figure is never guessed at: the child's cost is
// recorded unknown, with a note saying why, and the run total follows.
func TestSessionCost_UnknownParentRawMakesChildUnknown(t *testing.T) {
	led := runSessionGraph(t, `
name: session-unknown-parent
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`, runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": {SessionID: "s-dev", Result: "PASS", CostUnknown: true},
		"e2e": pass("s-dev", 0.65),
	}))

	e2e := mustRecord(t, led, "e2e")
	if !e2e.CostUnknown || e2e.CostUSD != 0 {
		t.Fatalf("e2e cost = %v (unknown %v), want unknown", e2e.CostUSD, e2e.CostUnknown)
	}
	if !nearly(e2e.ReportedCostUSD, 0.65) || e2e.ReportedCostUnknown {
		t.Fatalf("e2e reported = %v (unknown %v), want the raw 0.65 kept", e2e.ReportedCostUSD, e2e.ReportedCostUnknown)
	}
	if !strings.Contains(e2e.Detail, sessionCostUnknownNote) {
		t.Fatalf("e2e detail %q should carry %q", e2e.Detail, sessionCostUnknownNote)
	}
	if !led.CostUnknown() {
		t.Fatalf("run total should be marked unknown")
	}
}

// A codex session child is never subtracted: the runner, which knows the
// runtime, does not flag its figure as cumulative.
func TestSessionCost_CodexIsNeverSubtracted(t *testing.T) {
	r := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": pass("s-dev", 0.40),
		"e2e": pass("s-dev", 0.65),
	})
	r.SetRuntime(runner.RuntimeCodex)
	led := runSessionGraph(t, `
name: session-codex
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`, r)

	if e2e := mustRecord(t, led, "e2e"); !nearly(e2e.CostUSD, 0.65) || e2e.CostUnknown {
		t.Fatalf("codex e2e cost = %v (unknown %v), want its scripted 0.65 untouched", e2e.CostUSD, e2e.CostUnknown)
	}
}

// correctSessionCost's own table, including the cases no graph fixture
// reaches directly: a parent with no baseline in this leg (it passed in an
// earlier one) and a session total below its baseline.
func TestCorrectSessionCost(t *testing.T) {
	cases := []struct {
		name         string
		outcome      runner.NodeOutcome
		baseline     reportedCost
		haveBaseline bool
		wantCost     float64
		wantUnknown  bool
		wantNote     string
	}{
		{"cold start untouched", runner.NodeOutcome{TotalCostUSD: 0.5}, reportedCost{usd: 0.2}, true, 0.5, false, ""},
		{"resumed delta", runner.NodeOutcome{TotalCostUSD: 0.5, CostIncludesResumedSession: true}, reportedCost{usd: 0.2}, true, 0.3, false, ""},
		{"no baseline in this leg", runner.NodeOutcome{TotalCostUSD: 0.5, CostIncludesResumedSession: true}, reportedCost{}, false, 0, true, sessionCostUnknownNote},
		{"baseline unknown", runner.NodeOutcome{TotalCostUSD: 0.5, CostIncludesResumedSession: true}, reportedCost{unknown: true}, true, 0, true, sessionCostUnknownNote},
		{"total below baseline", runner.NodeOutcome{TotalCostUSD: 0.1, CostIncludesResumedSession: true}, reportedCost{usd: 0.2}, true, 0, true, "below parent's $0.2000"},
		{"own cost unknown", runner.NodeOutcome{CostUnknown: true, CostIncludesResumedSession: true}, reportedCost{usd: 0.2}, true, 0, true, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := tc.outcome
			correctSessionCost(&out, tc.baseline, tc.haveBaseline)
			if !nearly(out.TotalCostUSD, tc.wantCost) || out.CostUnknown != tc.wantUnknown {
				t.Fatalf("cost = %v (unknown %v), want %v (unknown %v)", out.TotalCostUSD, out.CostUnknown, tc.wantCost, tc.wantUnknown)
			}
			if out.ReportedCostUSD != tc.outcome.TotalCostUSD || out.ReportedCostUnknown != tc.outcome.CostUnknown {
				t.Fatalf("reported = %v (unknown %v), want the raw %v (unknown %v)", out.ReportedCostUSD, out.ReportedCostUnknown, tc.outcome.TotalCostUSD, tc.outcome.CostUnknown)
			}
			if tc.wantNote == "" && out.CostNote != "" || !strings.Contains(out.CostNote, tc.wantNote) {
				t.Fatalf("note = %q, want %q", out.CostNote, tc.wantNote)
			}
		})
	}
}
