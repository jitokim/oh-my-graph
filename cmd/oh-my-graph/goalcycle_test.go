package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// cycleSpec is the planner reply every goal-cycle test scripts: one node,
// least-privilege tools, valid under validatePlannedNodes — so each cycle's
// plan survives the full per-cycle gauntlet and the tests exercise the loop,
// not the validator.
const cycleSpec = `{"name":"cycle-work","version":"1","nodes":[` +
	`{"id":"work","prompt":"work","allowed_tools":["Read"]}]}`

const (
	cycleAssessNotMet = `{"goal_met": false, "remaining": "add the README section", "evidence": "the work artifact does not mention it"}`
	cycleAssessMet    = `{"goal_met": true, "remaining": "", "evidence": "the work node passed and produced the section"}`
)

// newCycleFake scripts a whole goal loop through the cmd layer on one
// FakeRunner, keying each coordinator call by class and ordinal (plan-1,
// assess-1, …) and each node launch by ordinal (work-1, work-2, …) — the same
// pattern as the coordinator's own goal tests, extended to the node calls so
// a test can script cycle 1's node to fail and cycle 2's to pass under one
// static outcome map. Safe without extra locking here because every scripted
// graph has one node, so no two KeyFn calls ever race.
func newCycleFake(outcomes map[string]runner.NodeOutcome) *runner.FakeRunner {
	fake := runner.NewFakeRunner(outcomes)
	planCalls, assessCalls, nodeCalls := 0, 0, 0
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		switch {
		case strings.Contains(spec.Prompt, "planning coordinator"):
			planCalls++
			return fmt.Sprintf("plan-%d", planCalls)
		case strings.Contains(spec.Prompt, "goal assessor"):
			assessCalls++
			return fmt.Sprintf("assess-%d", assessCalls)
		}
		nodeCalls++
		return fmt.Sprintf("%s-%d", spec.Prompt, nodeCalls)
	}
	return fake
}

// runPlanAndExecute drives planAndExecute exactly as runAuto wires it (bar
// the real runner and opener), returning the out-writer text and the error.
func runPlanAndExecute(t *testing.T, fake *runner.FakeRunner, cycles goalCycleOptions, web browser.Opener) (string, error) {
	t.Helper()
	var out bytes.Buffer
	var err error
	stdout := captureStdout(t, func() {
		coord := coordinator.New(fake)
		err = planAndExecute(context.Background(), &out, coord, fake, commonRunFlags{inputs: inputFlag{}},
			"add a README section", cycles, nil, web)
	})
	return out.String() + stdout, err
}

// goalSnapshots loads every run directory's snapshot, sorted by goal cycle —
// the on-disk record a lineage assertion reads.
func goalSnapshots(t *testing.T) []runstate.Snapshot {
	t.Helper()
	entries, err := os.ReadDir(runsRoot())
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	var snaps []runstate.Snapshot
	for _, entry := range entries {
		snap, err := runstate.Load(filepath.Join(runsRoot(), entry.Name(), stateFileName))
		if err != nil {
			t.Fatalf("load %s snapshot: %v", entry.Name(), err)
		}
		snaps = append(snaps, snap)
	}
	sort.Slice(snaps, func(i, j int) bool {
		ci, cj := 0, 0
		if snaps[i].Goal != nil {
			ci = snaps[i].Goal.Cycle
		}
		if snaps[j].Goal != nil {
			cj = snaps[j].Goal.Cycle
		}
		return ci < cj
	})
	return snaps
}

// TestAutoFlags_GoalCycleGovernance pins the parse-time gate: the bound is
// validated before anything spends, and a ceiling that could never fire is
// rejected rather than silently accepted (ADR 0011 §1, §3).
func TestAutoFlags_GoalCycleGovernance(t *testing.T) {
	t.Run("defaults to a single cycle", func(t *testing.T) {
		f := newAutoFlags()
		if err := f.parse([]string{"goal"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if f.maxCycles != 1 || f.maxGoalBudgetUSD != 0 {
			t.Errorf("defaults = %d cycles, $%v ceiling; want 1 and 0", f.maxCycles, f.maxGoalBudgetUSD)
		}
	})
	t.Run("accepts an iterated goal with a ceiling", func(t *testing.T) {
		f := newAutoFlags()
		if err := f.parse([]string{"goal", "--max-cycles", "3", "--max-goal-budget-usd", "2.5"}); err != nil {
			t.Fatalf("parse: %v", err)
		}
		if f.maxCycles != 3 || f.maxGoalBudgetUSD != 2.5 {
			t.Errorf("parsed = %d cycles, $%v ceiling", f.maxCycles, f.maxGoalBudgetUSD)
		}
	})
	rejected := map[string][]string{
		"zero max-cycles":               {"goal", "--max-cycles", "0"},
		"negative max-cycles":           {"goal", "--max-cycles", "-2"},
		"negative budget ceiling":       {"goal", "--max-cycles", "2", "--max-goal-budget-usd", "-1"},
		"budget ceiling without cycles": {"goal", "--max-goal-budget-usd", "5"},
	}
	for name, args := range rejected {
		t.Run(name, func(t *testing.T) {
			if err := newAutoFlags().parse(args); err == nil {
				t.Errorf("parse(%v) accepted invalid goal governance", args)
			}
		})
	}
}

// TestPlanAndExecute_MultiCycleAutoRun is ADR 0011 end to end through the cmd
// layer: cycle 1 runs and is judged unmet, cycle 2 replans (carrying the
// assessor's remaining), runs, and is judged met — exit clean. Each cycle is
// its own ordinary run on disk (own graph.json, state.json, events.jsonl
// bracket), linked by the additive goal block and judged in its own
// assess.json; the goal summary prints per-cycle run totals with planning
// included, so the ledger accumulation is visible, never averaged.
func TestPlanAndExecute_MultiCycleAutoRun(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-work-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.02},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-work-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.02},
	})

	out, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	if err != nil {
		t.Fatalf("a met goal on passed runs must exit clean, got: %v", err)
	}

	// The printed record accumulates live: each cycle's banner and verdict,
	// then the goal summary with the honest multiplier.
	for _, want := range []string{
		"up to 3 cycles",
		"— goal cycle 1/3",
		"— goal cycle 2/3",
		"Cycle 1 assessment",
		"goal not met",
		"remaining: add the README section",
		"Cycle 2 assessment",
		"goal met",
		"GOAL SUMMARY",
		"GOAL TOTAL: $1.2400 across 2 cycle(s)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output is missing %q:\n%s", want, out)
		}
	}
	// Per-cycle run total = planning ($0.10) + the node ($0.50): the summary
	// must show it per cycle, planning included.
	if got := strings.Count(out, "run $0.6000"); got != 2 {
		t.Errorf("goal summary shows %d cycles at run $0.6000, want 2:\n%s", got, out)
	}

	// The assessor's remaining is the one datum threaded into cycle 2's plan.
	prompts := map[string]string{}
	invocations := fake.Invocations()
	for i, key := range fake.Calls() {
		prompts[key] = invocations[i].Prompt
	}
	if !strings.Contains(prompts["plan-2"], "add the README section") {
		t.Error("cycle 2's planner prompt does not carry the assessor's remaining")
	}
	if !strings.Contains(prompts["assess-2"], "add the README section") {
		t.Error("cycle 2's assessment does not carry the previous cycle's remaining")
	}
	// The assessor reads the artifact content the run persisted.
	if !strings.Contains(prompts["assess-1"], "PASS") || !strings.Contains(prompts["assess-1"], "engine-excerpted run output") {
		t.Errorf("cycle 1's assessment material is missing the artifact excerpt:\n%s", prompts["assess-1"])
	}

	// On disk: two ordinary runs, linked by the goal block, each judged in
	// its own assess.json and bracketed on its own event stream.
	snaps := goalSnapshots(t)
	if len(snaps) != 2 {
		t.Fatalf("%d run directories, want one per cycle (2)", len(snaps))
	}
	for i, snap := range snaps {
		cycle := i + 1
		if snap.Goal == nil {
			t.Fatalf("cycle %d's snapshot has no goal block", cycle)
		}
		if snap.Goal.Cycle != cycle || snap.Goal.MaxCycles != 3 || snap.Goal.Text != "add a README section" {
			t.Errorf("cycle %d goal block = %+v", cycle, snap.Goal)
		}
		if snap.Goal.FirstRunID != snaps[0].RunID {
			t.Errorf("cycle %d first_run_id = %q, want cycle 1's run id %q", cycle, snap.Goal.FirstRunID, snaps[0].RunID)
		}
		if _, err := os.Stat(filepath.Join(runDirFor(snap.RunID), "graph.json")); err != nil {
			t.Errorf("cycle %d has no saved spec: %v", cycle, err)
		}

		var assessed assessRecord
		data, err := os.ReadFile(filepath.Join(runDirFor(snap.RunID), assessFileName))
		if err != nil {
			t.Fatalf("cycle %d has no %s: %v", cycle, assessFileName, err)
		}
		if err := json.Unmarshal(data, &assessed); err != nil {
			t.Fatalf("cycle %d %s does not parse: %v", cycle, assessFileName, err)
		}
		wantMet := cycle == 2
		if assessed.GoalMet != wantMet || assessed.AssessCostUSD != 0.02 {
			t.Errorf("cycle %d %s = %+v", cycle, assessFileName, assessed)
		}

		events := readRunEvents(t, snap.RunID)
		if len(events) == 0 || events[0].Type != runfeed.EventRunStarted {
			t.Fatalf("cycle %d's stream does not open with run_started", cycle)
		}
		if last := events[len(events)-1]; last.Type != runfeed.EventRunFinished || last.Outcome != "passed" {
			t.Errorf("cycle %d's stream does not close with run_finished passed: %+v", cycle, last)
		}
	}
}

// TestPlanAndExecute_SingleCycleStaysByteIdenticalToToday pins ADR 0011 §1's
// compatibility promise at the cmd layer: with the default single cycle there
// is no assessment call, no assess.json, no goal block in the snapshot, and
// no goal summary — the fake would fail the run loudly on any unscripted
// assessor call, so "no assessment" is asserted by the run's own success.
func TestPlanAndExecute_SingleCycleStaysByteIdenticalToToday(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	out, err := runPlanAndExecute(t, fake, singleCycle, nil)
	if err != nil {
		t.Fatalf("single-cycle auto run failed: %v", err)
	}
	for _, absent := range []string{"goal cycle", "GOAL SUMMARY", "assessment"} {
		if strings.Contains(out, absent) {
			t.Errorf("single-cycle output mentions %q — the loop leaked into the default path:\n%s", absent, out)
		}
	}

	snaps := goalSnapshots(t)
	if len(snaps) != 1 {
		t.Fatalf("%d run directories, want 1", len(snaps))
	}
	if snaps[0].Goal != nil {
		t.Errorf("single-cycle snapshot carries a goal block: %+v", snaps[0].Goal)
	}
	if _, err := os.Stat(filepath.Join(runDirFor(snaps[0].RunID), assessFileName)); !os.IsNotExist(err) {
		t.Errorf("single-cycle run has an %s (stat err %v), want none", assessFileName, err)
	}
}

// TestPlanAndExecute_CyclesExhaustedIsTheUnmetGoalExit: every run passed, the
// assessor never judged the goal met, the bound ran out — the command's
// contract is goal-level, so this exits non-zero with the final remaining
// (ADR 0011 §2, "not met, exhausted").
func TestPlanAndExecute_CyclesExhaustedIsTheUnmetGoalExit(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0},
		"assess-1": {Result: cycleAssessNotMet},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0},
		"assess-2": {Result: cycleAssessNotMet},
	})

	out, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 2}, nil)
	if err == nil {
		t.Fatal("an unmet goal after exhausted cycles must not exit 0")
	}
	for _, want := range []string{"goal not met after 2 cycle(s)", "add the README section"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("exit error %q is missing %q", err, want)
		}
	}
	if !strings.Contains(out, "GOAL SUMMARY") {
		t.Errorf("the goal summary must still print on an unmet exit:\n%s", out)
	}
	if got := fake.InvocationCount("plan-3"); got != 0 {
		t.Errorf("a plan beyond the bound was made: %d calls", got)
	}
}

// TestPlanAndExecute_FailedCycleStillIterates: a failed run is not the end of
// the goal — its evidence (verdict FAIL, the failure detail) feeds the
// assessment, and the next cycle routes around it (ADR 0011 §2's precedence
// table, "next cycle" on failed).
func TestPlanAndExecute_FailedCycleStillIterates(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "boom", ExitCode: 1, FailureCause: "tests failed"},
		"assess-1": {Result: cycleAssessNotMet},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0},
		"assess-2": {Result: cycleAssessMet},
	})

	out, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	if err != nil {
		t.Fatalf("cycle 2 met the goal on a passed run; the failed cycle 1 must not have ended the loop: %v", err)
	}
	if !strings.Contains(out, "FAILED") {
		t.Errorf("the goal summary should show cycle 1's run as FAILED:\n%s", out)
	}
}

// TestPlanAndExecute_MetVerdictNeverFlipsAFailedRun: the untrusted judge may
// stop the loop, but an engine-reported failure survives any verdict — a
// "met" on a FAILED final cycle is a printed contradiction and a non-zero
// exit (ADR 0011 §2, §3).
func TestPlanAndExecute_MetVerdictNeverFlipsAFailedRun(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "boom", ExitCode: 1, FailureCause: "tests failed"},
		"assess-1": {Result: cycleAssessMet},
	})

	_, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	if err == nil {
		t.Fatal("a goal-met verdict on a FAILED run must not exit 0")
	}
	if !strings.Contains(err.Error(), "never overrides") {
		t.Errorf("exit error %q does not name the contradiction", err)
	}
}

// TestPlanAndExecute_SessionLimitPausesTheWholeLoop: the one outcome that
// short-circuits assessment. The pause error propagates intact (mainExitCode
// maps it to exit 2), no assessor call is made, and the paused cycle's
// snapshot still carries its goal lineage so the pause does not orphan the
// run from its group (ADR 0011 §2).
func TestPlanAndExecute_SessionLimitPausesTheWholeLoop(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
	})

	_, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected the loop to pause with *LimitPausedError, got %T: %v", err, err)
	}
	if got := fake.InvocationCount("assess-1"); got != 0 {
		t.Errorf("a paused cycle was assessed %d times, want 0", got)
	}
	snaps := goalSnapshots(t)
	if len(snaps) != 1 || snaps[0].Goal == nil || snaps[0].Goal.Cycle != 1 {
		t.Fatalf("the paused cycle's snapshot must carry its goal lineage: %+v", snaps)
	}
}

// TestPlanAndExecute_BrowserOpensForCycleOneOnly: one surprise tab per goal
// (ADR 0011 §4) — every cycle serves its own live view, but only cycle 1's
// launch reaches the opener seam.
func TestPlanAndExecute_BrowserOpensForCycleOneOnly(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0},
		"assess-1": {Result: cycleAssessNotMet},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0},
		"assess-2": {Result: cycleAssessMet},
	})
	opener := browser.NewFakeOpener()

	if _, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, opener); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := len(opener.URLs()); got != 1 {
		t.Errorf("the browser was launched %d times across 2 cycles, want exactly 1 (cycle 1's)", got)
	}
}
