package main

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// skippedBaselinePlanLine is the plan screen's line for `auto --no-baseline`
// (#328) — distinct from the launch line, which starts "Baseline: skipped".
const skippedBaselinePlanLine = "  baseline: skipped (--no-baseline) — the starting tree was not checked against --verify-cmd\n"

// planScreen is the output from the first plan header on, so an assertion
// about the plan screen cannot be satisfied by the launch line above it.
func planScreen(t *testing.T, out string) string {
	t.Helper()
	i := strings.Index(out, "Planned graph ")
	if i < 0 {
		t.Fatalf("no plan screen in the output:\n%s", out)
	}
	return out[i:]
}

// #328: --plan-only writes no state.json, so the preview's plan screen is the
// only place the skip is recorded.
func TestRunAuto_PlanOnlyPrintsTheSkippedBaselineWithThePreview(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 0)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
	})

	out, err := runBaselineAuto(t, fake, verify.NewFakeVerifier(nil), osStdin(), "add a README section", "--plan-only", "--no-baseline", "--verify-cmd", script)

	if err != nil {
		t.Fatalf("auto --plan-only --no-baseline: %v\n%s", err, out)
	}
	if got := strings.Count(planScreen(t, out), skippedBaselinePlanLine); got != 1 {
		t.Errorf("preview printed the skipped-baseline line %d times, want once:\n%s", got, out)
	}
}

// #328: the executed plan screen states the skip — on every cycle of a goal
// loop, since each cycle prints its own plan.
func TestRunAuto_ExecutedPlanPrintsTheSkippedBaseline(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cycles []string
		fake   map[string]runner.NodeOutcome
		want   int
	}{
		{"single cycle", nil, map[string]runner.NodeOutcome{
			"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
			"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		}, 1},
		{"goal loop", []string{"--max-cycles", "2"}, map[string]runner.NodeOutcome{
			"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
			"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
			"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
			"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
			"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
			"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
		}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			script := writeVerifyScript(t, 0)
			args := append([]string{"add a README section", "--no-baseline", "--verify-cmd", script}, tc.cycles...)

			out, err := runBaselineAuto(t, newCycleFake(tc.fake), verify.NewFakeVerifier(nil), osStdin(), args...)

			if err != nil {
				t.Fatalf("auto --no-baseline: %v\n%s", err, out)
			}
			if got := strings.Count(planScreen(t, out), skippedBaselinePlanLine); got != tc.want {
				t.Errorf("plan screens printed the skipped-baseline line %d times, want %d:\n%s", got, tc.want, out)
			}
		})
	}
}

// #328: without --no-baseline no screen prints the line — the preview, the
// executed plan, and chat's confirm screen (chat has no --no-baseline, so the
// confirm screen never applies to the flag).
func TestPlanScreens_TakenBaselinePrintsNoSkipLine(t *testing.T) {
	t.Run("plan-only preview", func(t *testing.T) {
		isolateRunHome(t)
		script := writeVerifyScript(t, 0)
		fake := newCycleFake(map[string]runner.NodeOutcome{
			"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		})
		verifier := verify.NewFakeVerifier(map[string]verify.Result{script: {ExitCode: 0}})

		out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--verify-cmd", script)

		if err != nil {
			t.Fatalf("auto --plan-only: %v\n%s", err, out)
		}
		if strings.Contains(planScreen(t, out), "baseline: skipped") {
			t.Errorf("a preview that took the baseline printed the skip line:\n%s", out)
		}
	})
	t.Run("executed plan", func(t *testing.T) {
		isolateRunHome(t)
		script := writeVerifyScript(t, 0)
		fake := newCycleFake(map[string]runner.NodeOutcome{
			"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
			"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		})
		verifier := verify.NewFakeVerifier(map[string]verify.Result{script: {ExitCode: 0}})

		out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--verify-cmd", script)

		if err != nil {
			t.Fatalf("auto: %v\n%s", err, out)
		}
		if strings.Contains(out, "baseline: skipped") {
			t.Errorf("a run that took the baseline printed the skip line:\n%s", out)
		}
	})
	t.Run("confirm screen", func(t *testing.T) {
		isolateRunHome(t)
		var out strings.Builder

		accepted, err := confirmPlan(&out, planPolicies(t, false), runner.RuntimeClaude, nil, func() (bool, error) { return true, nil })

		if err != nil || !accepted {
			t.Fatalf("confirmPlan = %v, %v; want accepted", accepted, err)
		}
		if strings.Contains(out.String(), "baseline: skipped") {
			t.Errorf("chat's confirm screen printed the skip line:\n%s", out.String())
		}
	})
}
