package main

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
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

// A twice-refused plan keeps what it paid for. Until now a rejected planner
// call left NOTHING on disk: the user paid, saw an error, and had no artifact
// to hand-edit or re-run. The spec goes under plans/ — nothing ran, so it is
// not a run — beside a preview's, under its own name so no reader walking the
// tree for graph.json mistakes it for a graph the engine would run.
func TestRunAutoWith_RejectedPlanKeepsTheSpecItPaidFor(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: refusedCycleSpec, TotalCostUSD: 0.02},
		"plan-2": {Result: refusedCycleSpec, TotalCostUSD: 0.03},
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

	planDir := solePlanDir(t)
	specPath := filepath.Join(planDir, rejectedSpecFileName)
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
	// It is NOT the accepted plan's name: a reader of the tree must not be
	// able to load it as a graph that was going to run.
	if _, statErr := os.Stat(filepath.Join(planDir, generatedSpecFileName)); statErr == nil {
		t.Errorf("a rejected plan was saved as %s — nothing may mistake it for an accepted plan", generatedSpecFileName)
	}
	// And nothing was left where the run readers look: nothing ran.
	entries, readDirErr := os.ReadDir(runsRoot())
	if readDirErr != nil && !errors.Is(readDirErr, fs.ErrNotExist) {
		t.Fatalf("read runs root: %v", readDirErr)
	}
	if len(entries) != 0 {
		t.Errorf("a rejected plan left %d director(ies) under runs/", len(entries))
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
