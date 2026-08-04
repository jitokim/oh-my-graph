package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// --- failure cases first ----------------------------------------------------

// TestAutoFlags_PlanOnlyRejectsAGoalLoop pins the parse-time gate: every cycle
// after the first is planned from the previous cycle's execution, so a
// multi-cycle plan-only could only ever show cycle 1. Rejected rather than
// silently showing one cycle — the same posture --max-goal-budget-usd takes
// against a bound that could never fire.
func TestAutoFlags_PlanOnlyRejectsAGoalLoop(t *testing.T) {
	f := newAutoFlags()
	err := f.parse([]string{"a goal", "--plan-only", "--max-cycles", "3"})
	if err == nil {
		t.Fatal("--plan-only with --max-cycles 3 must be rejected at parse, before anything spends")
	}
	if !strings.Contains(err.Error(), "--plan-only") || !strings.Contains(err.Error(), "--max-cycles") {
		t.Errorf("the refusal must name both flags: %v", err)
	}

	ok := newAutoFlags()
	if err := ok.parse([]string{"a goal", "--plan-only"}); err != nil {
		t.Fatalf("--plan-only on a single-cycle auto must parse: %v", err)
	}
	if !ok.planOnly {
		t.Error("--plan-only parsed but did not set the flag")
	}
}

// TestPlanAndExecute_PlanOnlyRefusesACycleLoop pins the defensive backstop
// behind that parse gate: planAndExecute itself must never quietly hand a
// plan-only call to the executing loop. Unreachable from the CLI today, which
// is exactly why it is asserted — a future caller must fail loudly rather than
// get a run out of a flag that promises not to run one.
func TestPlanAndExecute_PlanOnlyRefusesACycleLoop(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(nil)

	var out strings.Builder
	err := planAndExecute(context.Background(), &out, coordinator.New(fake), fake,
		commonRunFlags{inputs: inputFlag{}}, "a goal", goalCycleOptions{maxCycles: 2}, true, nil, nil)
	if err == nil {
		t.Fatal("a plan-only multi-cycle call must fail, not fall through to the loop")
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refusal must come before any call: the runner saw %d", n)
	}
}

// --- success case -----------------------------------------------------------

// TestRunAutoWith_PlanOnlyRunsNoNode is the flag's whole point, asserted the
// way --dry-run asserts its own (TestRunGraphWith_DryRunRunsNoNode): through
// the real `auto` argv path, --plan-only must plan, print the plan and the
// tool ceiling, and stop — with the runner seam seeing the planner call and
// NOTHING else.
//
// The asymmetry with --dry-run is asserted in both directions, since checking
// only for absent node invocations would pass equally for a plan that never
// happened: the planner call MUST have been made (it is what makes a plan
// exist, and it is billed), the printout must say what it cost, and the run
// directory must hold that paid-for spec and none of the files execution
// creates.
//
// Mapping is switched off here so the test reads no part of the invoking
// user's real ~/.claude tree. That costs nothing in coverage: --plan-only is
// an early return placed AFTER printPlan inside the one plan sequence, so
// there is no second print path that could show different mappings — see
// TestPrintPlan_ShowsSkillScanAndItsLimits for the disclosure itself.
func TestRunAutoWith_PlanOnlyRunsNoNode(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only on a valid plan returned error: %v", err)
	}

	invocations := fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("--plan-only made %d calls, want exactly the one planner call", len(invocations))
	}
	if !strings.Contains(invocations[0].Prompt, "planning coordinator") {
		t.Errorf("the one call must be the planner, got prompt: %q", firstLine(invocations[0].Prompt))
	}
	// "work" is cycleSpec's single node prompt — the node the plan describes
	// and that nothing may launch.
	for _, inv := range invocations {
		if inv.Prompt == "work" {
			t.Fatal("--plan-only launched the planned node")
		}
	}

	// The plan was bought, so it is kept; nothing execution creates exists.
	runDir := soleRunDir(t)
	if _, statErr := os.Stat(filepath.Join(runDir, "graph.json")); statErr != nil {
		t.Errorf("--plan-only must keep the spec it paid for: %v", statErr)
	}
	for _, gone := range []string{stateFileName, runfeed.FileName, lockFileName} {
		if _, statErr := os.Stat(filepath.Join(runDir, gone)); statErr == nil {
			t.Errorf("--plan-only created %s — that file exists only because a run started", gone)
		}
	}

	for _, want := range []string{
		"Planned graph",              // the topology, exactly as a real auto prints it
		"work",                       // the planned node id is on it
		"Planned nodes run isolated", // the tool ceiling
		"no node was executed",
		"$0.0417", // the flag is not free, and says the number
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan-only output should contain %q:\n%s", want, out)
		}
	}
}

// soleRunDir returns the one run directory the isolated OMG_HOME holds,
// failing if there is not exactly one — so the assertions above cannot be
// satisfied by a plan-only that created no run directory at all.
func soleRunDir(t *testing.T) string {
	t.Helper()
	entries, err := os.ReadDir(runsRoot())
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one run directory, got %d", len(entries))
	}
	return filepath.Join(runsRoot(), entries[0].Name())
}

// firstLine keeps a failure message readable when it has to quote a planner
// prompt.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
