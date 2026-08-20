package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// refusedCycleSpec is a planner reply the coordinator refuses: a tool outside
// auto mode's allowlist. The refusal is repairable — it is the planner's own
// judgment, precisely diagnosed — so it is what buys the one re-plan.
const refusedCycleSpec = `{"name":"cycle-work","version":"1","nodes":[` +
	`{"id":"work","prompt":"work","allowed_tools":["Bash(rm -rf *)"]}]}`

// A re-plan is never silent. The price printed with the plan is already the
// sum of both calls, so without the disclosure a repaired plan is
// indistinguishable from an expensive one — and nobody could measure whether a
// planner-prompt change reduced the refusal rate, because the refusals would
// have stopped being visible.
//
// Asserted through the real `auto --plan-only` argv path, so the wording
// tested is the wording a user sees.
func TestRunAutoWith_PlanOnlyDisclosesARePlanAndItsCost(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: refusedCycleSpec, TotalCostUSD: 0.02},
		"plan-2": {Result: cycleSpec, TotalCostUSD: 0.03},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("a repaired plan must succeed: %v", err)
	}
	if got := len(fake.Invocations()); got != 2 {
		t.Fatalf("made %d calls, want the refused planner call and its one correction", got)
	}

	for _, want := range []string{
		// the fact
		"re-planned:",
		// what the rejected attempt cost, and that it is inside the total
		"$0.0200",
		"included above",
		// the refusal the planner was asked to answer — the measurable datum
		"outside auto mode's tool allowlist",
		// the total, which is the sum of both calls
		"$0.0500",
		// and the plan-only sentence, now plural and honest about which calls
		"2 planner calls above (the refused first reply and its correction) were",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a repaired plan's output should contain %q:\n%s", want, out)
		}
	}
	// The singular claim must be gone, not merely joined by the plural one.
	if strings.Contains(out, "The planner call above was") {
		t.Error("the output still claims one planner call was paid for after buying two")
	}
}

// A first-try plan says nothing about a re-plan and keeps the singular
// sentence: the disclosure means "this happened", so it must be absent when it
// did not.
func TestRunAutoWith_PlanOnlyIsSilentWhenNoRePlanHappened(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "The planner call above was still paid for") {
		t.Errorf("a first-try preview lost the singular cost sentence:\n%s", out)
	}
	if strings.Contains(out, "re-planned:") {
		t.Errorf("a first-try plan claims a re-plan happened:\n%s", out)
	}
}

// A twice-refused plan keeps what it paid for. Before v0.5 a rejected planner
// call left NOTHING on disk: the user paid, saw an error, and had no artifact
// to hand-edit or re-run. Since ADR 0023 §3.1 the spec goes into THE RUN
// DIRECTORY, not plans/, and the reason is two premises rather than one: `auto`
// is non-interactive, so its commitment to execute existed before the planner
// call and a run already exists; and the engine judged the material it was
// handed and produced a finding about it, which is a FAIL in the same sense a
// failed node is. It keeps its own file name so no reader walking the tree for
// graph.json mistakes it for a graph the engine would run.
func TestRunAutoWith_RejectedPlanKeepsTheSpecItPaidFor(t *testing.T) {
	isolateRunHome(t)
	// The two replies differ, so "the saved spec is the LAST rejected one"
	// is actually asserted: with both calls returning the same bytes, saving
	// the first reply would pass a test that means to pin PlanRejection.Spec
	// to the final refusal.
	finalRefusedSpec := strings.Replace(
		refusedCycleSpec, `"name":"cycle-work"`, `"name":"final-refused-cycle"`, 1)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: refusedCycleSpec, TotalCostUSD: 0.02},
		"plan-2": {Result: finalRefusedSpec, TotalCostUSD: 0.03},
	})

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("a twice-refused plan must fail, not run")
	}
	if got := len(fake.Invocations()); got != 2 {
		t.Fatalf("made %d calls, want exactly two — the refusal and its one correction", got)
	}

	runDir := soleRunDir(t)
	specPath := filepath.Join(runDir, rejectedSpecFileName)
	info, statErr := os.Stat(specPath)
	if statErr != nil {
		t.Fatalf("a rejected plan must keep the spec it paid for: %v", statErr)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("rejected spec mode = %#o, want 0600 — same stance as an accepted one", got)
	}
	saved, readErr := os.ReadFile(specPath)
	if readErr != nil {
		t.Fatalf("read rejected spec: %v", readErr)
	}
	if !strings.Contains(string(saved), "Bash(rm -rf *)") {
		t.Errorf("the saved spec is not the rejected one:\n%s", saved)
	}
	if !strings.Contains(string(saved), "final-refused-cycle") {
		t.Errorf("the saved spec is the FIRST rejection, not the last one that was paid for:\n%s", saved)
	}
	// It is NOT the accepted plan's name: a reader of the tree must not be
	// able to load it as a graph that was going to run.
	if _, statErr := os.Stat(filepath.Join(runDir, generatedSpecFileName)); statErr == nil {
		t.Errorf("a rejected plan was saved as %s — nothing may mistake it for an accepted plan", generatedSpecFileName)
	}
	// The directory is the ADR 0023 §2.1.1 shape exactly: a lock, a two-line
	// stream, a rejected spec, and NO snapshot — settled, with no verdict about
	// work because no work happened.
	if _, statErr := os.Stat(filepath.Join(runDir, stateFileName)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("a refused plan must leave no snapshot, got %v", statErr)
	}
	// And it renders — as a FAIL row, not a WARNING+skip. That is the
	// regression ADR 0023 §9 names as the one that would otherwise ship this
	// change as a net loss.
	var listed, warned strings.Builder
	if listErr := listRuns(&listed, &warned, runsRoot(), false); listErr != nil {
		t.Fatalf("listRuns: %v", listErr)
	}
	if warned.Len() != 0 {
		t.Errorf("a refused plan must not be skipped as damage: %q", warned.String())
	}
	row := lineContaining(t, listed.String(), filepath.Base(runDir))
	if fields := strings.Fields(row); fields[len(fields)-1] != "FAIL" {
		t.Errorf("a refused plan's row = %q, want a FAIL status", row)
	}
	// Nothing under plans/: that tree keeps one honest meaning — specs that
	// never belonged to a run (§3.1).
	planEntries, readDirErr := os.ReadDir(filepath.Join(os.Getenv("OMG_HOME"), "plans"))
	if readDirErr != nil && !errors.Is(readDirErr, fs.ErrNotExist) {
		t.Fatalf("read plans root: %v", readDirErr)
	}
	if len(planEntries) != 0 {
		t.Errorf("a refused auto plan left %d director(ies) under plans/; it belongs to its run", len(planEntries))
	}

	for _, want := range []string{
		// what it cost, said out loud
		"$0.0500",
		"paid for whether or not its graph loads",
		// where the artifact went, and what to do with it
		specPath,
		"oh-my-graph run",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("a rejected plan's output should contain %q:\n%s", want, out)
		}
	}
	// The re-plan is disclosed on the failure path too, or the user is billed
	// twice with no way to tell.
	if !strings.Contains(err.Error(), "bought twice") {
		t.Errorf("the refusal does not say a re-plan was attempted: %v", err)
	}
}

// A goal loop's refused cycle is in the multiplier, not below it. The whole
// risk of a loop on a paid runtime is the multiplier, and ADR 0011 §4 requires
// it printed rather than derivable — so a cycle that spent two planner calls
// and produced no graph must be counted in GOAL TOTAL, exactly as a failed
// assessment's own cost is. It has no ledger of its own to hide behind:
// nothing ran.
//
// The rejected spec's directory also carries the lineage, and since ADR 0023
// §3.1 it carries it by BEING that cycle's run directory rather than by
// encoding "<first-run>-cycle2" into a plans/ name. The per-cycle planning hook
// mints the id and opens the leg before the planner call, so the refusal has a
// run of its own to be recorded in — which is also what makes the cycle's own
// planner call visible while it is happening.
func TestPlanAndExecute_ARefusedCyclesPlanningSpendIsInTheGoalTotal(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.03},
		// Cycle 2 is refused, and its one correction is refused too.
		"plan-2": {Result: refusedCycleSpec, TotalCostUSD: 0.02},
		"plan-3": {Result: refusedCycleSpec, TotalCostUSD: 0.04},
	})

	out, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) {
		t.Fatalf("expected *PlanRejection, got %T: %v", err, err)
	}
	if got := fake.InvocationCount("plan-4"); got != 0 {
		t.Errorf("a twice-refused cycle bought %d further planner call(s), want 0", got)
	}
	for _, want := range []string{
		// cycle 1's own line, unchanged
		"cycle 1: run ",
		"run $0.6000",
		// the refused cycle, named as a planning refusal and counted
		"cycle 2: incomplete — its planning was refused after spending $0.0600 (counted)",
		// 0.60 + 0.03 + 0.06 — the two refused planner calls included
		"GOAL TOTAL: $0.6900 across 1 assessed cycle(s) + 1 incomplete cycle",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("goal summary is missing %q:\n%s", want, out)
		}
	}
	// The old wording claimed the spend was reported elsewhere. It is not:
	// nothing ran, so there is no ledger for it above.
	if strings.Contains(out, "it never reached assessment") {
		t.Errorf("a refused plan is still reported as an unaccounted incomplete cycle:\n%s", out)
	}

	// Lineage: the refused cycle has its own run directory, newer than cycle
	// 1's, holding rejected.json and no snapshot.
	cycleOneRunID := goalSnapshots(t)[0].RunID
	runDirs := runDirNames(t)
	if len(runDirs) != 2 {
		t.Fatalf("want two run directories (the run cycle and the refused one), got %v", runDirs)
	}
	if runDirs[0] != cycleOneRunID {
		t.Errorf("first run directory = %q, want cycle 1's run %q", runDirs[0], cycleOneRunID)
	}
	refusedDir := filepath.Join(runsRoot(), runDirs[1])
	if _, statErr := os.Stat(filepath.Join(refusedDir, rejectedSpecFileName)); statErr != nil {
		t.Errorf("the refused cycle must keep its spec in its own run directory: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(refusedDir, stateFileName)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("a refused cycle must leave no snapshot, got %v", statErr)
	}
	// And nothing under plans/: a goal cycle always has a run.
	if _, statErr := os.Stat(filepath.Join(os.Getenv("OMG_HOME"), "plans")); !errors.Is(statErr, fs.ErrNotExist) {
		t.Errorf("a refused goal cycle wrote under plans/, got %v", statErr)
	}
}

// runDirNames lists the run directory names under runs/, oldest first (run ids
// are timestamps that sort lexically).
func runDirNames(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir(runsRoot())
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	return names
}

// soleRunDir returns the one run directory under runs/, failing if there is not
// exactly one.
func soleRunDir(t *testing.T) string {
	t.Helper()
	names := runDirNames(t)
	if len(names) != 1 {
		t.Fatalf("want exactly one run directory, got %v", names)
	}
	return filepath.Join(runsRoot(), names[0])
}

// A fault the reply's content did not cause buys nothing and leaves no spec —
// there was never one to keep.
func TestRunAutoWith_NonJudgmentPlanFailureBuysNoRePlan(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: "I would rather not plan this.", TotalCostUSD: 0.02},
		"plan-2": {Result: cycleSpec, TotalCostUSD: 0.03},
	})

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--no-agent-mapping", "--no-skill-mapping"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("a reply with no JSON object must fail")
	}
	if got := len(fake.Invocations()); got != 1 {
		t.Errorf("made %d calls, want exactly 1 — a blind retry is not a repair", got)
	}
	root := filepath.Join(os.Getenv("OMG_HOME"), "plans")
	entries, readErr := os.ReadDir(root)
	if readErr != nil && !errors.Is(readErr, fs.ErrNotExist) {
		t.Fatalf("read plans root: %v", readErr)
	}
	if len(entries) != 0 {
		t.Errorf("a reply with no spec in it left %d plan director(ies)", len(entries))
	}
}
