package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/serve"
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
// exist, and it is billed), the printout must say what it cost, and the paid-for
// spec must survive — in plans/, holding none of the files execution creates,
// and leaving runs/ untouched so no reader of that tree sees a phantom.
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

	// The plan was bought, so it is kept — under plans/, not runs/. Owner-only,
	// because a saved spec can carry inlined SKILL.md bodies: the user's own
	// private instructions, not just a topology.
	planDir := solePlanDir(t)
	dirInfo, dirErr := os.Stat(planDir)
	if dirErr != nil {
		t.Fatalf("stat plan directory: %v", dirErr)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("plan directory mode = %#o, want 0700 — a spec dir is not world-readable", got)
	}
	specInfo, statErr := os.Stat(filepath.Join(planDir, "graph.json"))
	if statErr != nil {
		t.Errorf("--plan-only must keep the spec it paid for: %v", statErr)
	} else if got := specInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("saved spec mode = %#o, want 0600 — it can hold inlined skill bodies", got)
	}
	for _, gone := range []string{stateFileName, runfeed.FileName, lockFileName} {
		if _, statErr := os.Stat(filepath.Join(planDir, gone)); statErr == nil {
			t.Errorf("--plan-only created %s — that file exists only because a run started", gone)
		}
	}

	// And nothing was left where the run readers look. A directory under runs/
	// holding only a graph.json is not a harmless leftover: it is exactly the
	// shape of a run whose state.json never arrived, so it would be reported
	// through the same channel as real damage and, being newest, would become
	// the run `serve` opens by default.
	entries, readErr := os.ReadDir(runsRoot())
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		t.Fatalf("read runs root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("--plan-only left %d director(ies) under runs/ — nothing ran, so nothing is a run", len(entries))
	}

	var listed, warned strings.Builder
	if listErr := listRuns(&listed, &warned, runsRoot()); listErr != nil {
		t.Fatalf("runs list after a preview returned error: %v", listErr)
	}
	if warned.Len() != 0 {
		t.Errorf("a preview must not read as a broken run to `runs list`:\n%s", warned.String())
	}
	if _, resolveErr := serve.ResolveRun(runsRoot(), ""); resolveErr == nil {
		t.Error("resolving with no explicit run id must still find no run after a preview, not resolve onto it")
	}

	for _, want := range []string{
		"Planned graph",              // the topology, exactly as a real auto prints it
		"work",                       // the planned node id is on it
		"Planned nodes run isolated", // the tool ceiling
		"no node was executed",
		"$0.0417",     // the flag is not free, and says the number
		"not a run",   // and says it left nothing for the run readers
		"`runs list`", // naming the one that would otherwise report it
		filepath.Join(os.Getenv("OMG_HOME"), "plans"), // the kept spec is named by its real path
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan-only output should contain %q:\n%s", want, out)
		}
	}
}

func TestRunAutoWith_CodexPlanPrintsSandboxNotGranularToolClaims(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: cycleSpec, CostUnknown: true},
	})

	var runErr error
	out := captureStdout(t, func() {
		runErr = runAutoWithRuntime(runner.RuntimeCodex,
			[]string{"add a README section", "--plan-only"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if runErr != nil {
		t.Fatalf("Codex --plan-only returned error: %v", runErr)
	}
	for _, misleading := range []string{"[tools:", "scope like Bash(git *) is enforced"} {
		if strings.Contains(out, misleading) {
			t.Errorf("Codex plan claims unsupported granular enforcement %q:\n%s", misleading, out)
		}
	}
	for _, want := range []string{"Codex filesystem sandbox", "allowed_tools declarations"} {
		if !strings.Contains(out, want) {
			t.Errorf("Codex plan does not disclose %q:\n%s", want, out)
		}
	}
}

// solePlanDir returns the one plan directory the isolated OMG_HOME holds,
// failing if there is not exactly one — so the assertions above cannot be
// satisfied by a plan-only that kept nothing at all.
func solePlanDir(t *testing.T) string {
	t.Helper()
	root := filepath.Join(os.Getenv("OMG_HOME"), "plans")
	entries, err := os.ReadDir(root)
	if err != nil {
		t.Fatalf("read plans root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one plan directory, got %d", len(entries))
	}
	return filepath.Join(root, entries[0].Name())
}

// firstLine keeps a failure message readable when it has to quote a planner
// prompt.
func firstLine(s string) string {
	line, _, _ := strings.Cut(s, "\n")
	return line
}
