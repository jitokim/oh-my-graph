package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// observingRunner wraps a NodeRunner and calls observe with the invocation
// BEFORE delegating — so a test can look at the run directory from INSIDE a
// call, which is the only way to judge a phase that exists exactly while a
// subprocess is outstanding. It spawns nothing; the wrapped runner is a
// FakeRunner, so no real claude is involved.
type observingRunner struct {
	inner   runner.NodeRunner
	observe func(spec runner.NodeInvocation)
}

func (r observingRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.observe(spec)
	return r.inner.Run(ctx, spec)
}

// isPlannerCall reports whether an invocation is the coordinator's planner
// call, by the same marker the cycle fakes key on.
func isPlannerCall(spec runner.NodeInvocation) bool {
	return strings.Contains(spec.Prompt, "planning coordinator")
}

// --- #163 itself: the planner call is visible while it is running -----------

// TestPlanAndExecute_ThePlannerCallIsVisibleWhileItRuns is the report. A
// colleague ran `auto`, saw "Planning a graph for goal …", and then nothing:
// `runs list` printed "No runs found." and the dashboard showed no card for the
// whole planner call — the longest single wait in the tool and the first one a
// new user meets. The cause was one line's position, `runID := newRunID()`
// AFTER coord.Plan returned, so there was no run directory for any reader to
// find.
//
// Everything here is judged from INSIDE the planner call, because that is the
// only instant the phase exists.
func TestPlanAndExecute_ThePlannerCallIsVisibleWhileItRuns(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var listedDuringPlanning, warnedDuringPlanning string
	var statusDuringPlanning runstatus.Status
	var resolvedDuringPlanning string
	planningSeen := false

	observed := observingRunner{inner: fake, observe: func(spec runner.NodeInvocation) {
		if !isPlannerCall(spec) {
			return
		}
		planningSeen = true
		var out, warn strings.Builder
		if err := listRuns(&out, &warn, runsRoot()); err != nil {
			t.Errorf("listRuns during planning: %v", err)
		}
		listedDuringPlanning, warnedDuringPlanning = out.String(), warn.String()

		dir := soleRunDir(t)
		status, err := runstatus.Of(dir)
		if err != nil {
			t.Errorf("runstatus.Of during planning: %v", err)
		}
		statusDuringPlanning = status
		// The single-run view's own choice of run, from the same instant.
		resolvedDuringPlanning, err = serve.ResolveRun(runsRoot(), "")
		if err != nil {
			t.Errorf("ResolveRun during planning: %v", err)
		}
	}}

	var out strings.Builder
	stdout := captureStdout(t, func() {
		coord := coordinator.New(observed)
		if err := planAndExecute(context.Background(), &out, coord, observed,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, false, nil, nil); err != nil {
			t.Fatalf("planAndExecute: %v", err)
		}
	})

	if !planningSeen {
		t.Fatal("the planner call never happened; the rest of this test judges nothing")
	}
	if statusDuringPlanning != runstatus.Planning {
		t.Errorf("status during the planner call = %v, want %v", statusDuringPlanning, runstatus.Planning)
	}
	if warnedDuringPlanning != "" {
		t.Errorf("a run inside its planner call must not read as a damaged directory: %q", warnedDuringPlanning)
	}
	if strings.Contains(listedDuringPlanning, "No runs found") {
		t.Errorf("#163 verbatim — `runs list` was silent through the planner call:\n%s", listedDuringPlanning)
	}
	if !strings.Contains(listedDuringPlanning, "PLANNING") {
		t.Errorf("`runs list` must show a PLANNING row during the planner call:\n%s", listedDuringPlanning)
	}
	// The live view would open on it: PLANNING is in flight, and ResolveRun
	// prefers an in-flight run (the §2.1.1 silent-loss case, on the surface
	// #163 names).
	if resolvedDuringPlanning != filepath.Base(soleRunDir(t)) {
		t.Errorf("ResolveRun during planning = %q, want the planning run", resolvedDuringPlanning)
	}
	// The run id is on screen from the start, so the row above is findable.
	if !strings.Contains(stdout+out.String(), "Planning a graph for goal") ||
		!strings.Contains(out.String(), filepath.Base(soleRunDir(t))) {
		t.Errorf("the planning line must name the run id it just minted:\n%s", out.String())
	}
	// And the phase ENDED: the committed plan's untagged run_started is what
	// moves the run to RUNNING, and by now the whole run has settled as PASS.
	final, err := runstatus.Of(soleRunDir(t))
	if err != nil {
		t.Fatalf("runstatus.Of after the run: %v", err)
	}
	if final != runstatus.Pass {
		t.Errorf("status after a passing run = %v, want %v", final, runstatus.Pass)
	}
}

// TestPlanAndExecute_TheCommittedPlanClosesThePlanningPhaseOnTheStream pins the
// stream shape ADR 0023 §2.3 publishes as a contract change: ONE leg opened by
// a run_started{phase:"planning"}, then the scheduler's untagged run_started,
// then one run_finished. Two opens, one close — which is exactly what a
// consumer keeping a stack of legs has to know, and why docs/RUN-FEED.md says
// to count legs by run_started events with NO phase.
func TestPlanAndExecute_TheCommittedPlanClosesThePlanningPhaseOnTheStream(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	var out strings.Builder
	captureStdout(t, func() {
		coord := coordinator.New(fake)
		if err := planAndExecute(context.Background(), &out, coord, fake,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, false, nil, nil); err != nil {
			t.Fatalf("planAndExecute: %v", err)
		}
	})

	events := readStreamEvents(t, soleRunDir(t))
	var opens, phased, closes int
	for _, e := range events {
		switch e.Type {
		case runfeed.EventRunStarted:
			opens++
			if e.Phase == runfeed.PhasePlanning {
				phased++
			}
		case runfeed.EventRunFinished:
			closes++
		}
	}
	if opens != 2 || phased != 1 || closes != 1 {
		t.Errorf("stream shape = %d open(s) (%d phased), %d close(s); want 2, 1, 1", opens, phased, closes)
	}
	if events[0].Type != runfeed.EventRunStarted || events[0].Phase != runfeed.PhasePlanning {
		t.Errorf("the FIRST event must be the planning open, got %+v", events[0])
	}
	// The phase field is what disambiguates a leg count, so the SECOND open
	// must carry none — "the latest run_started says nothing" is what makes the
	// run read RUNNING affirmatively rather than by absence of node events.
	for _, e := range events[1:] {
		if e.Type == runfeed.EventRunStarted && e.Phase != "" {
			t.Errorf("the scheduler's own run_started must carry no phase, got %q", e.Phase)
		}
	}
}

// readStreamEvents reads a run directory's whole event stream through the real
// reader.
func readStreamEvents(t *testing.T, runDir string) []runfeed.Event {
	t.Helper()
	var events []runfeed.Event
	if err := runfeed.Walk(filepath.Join(runDir, runfeed.FileName), func(e runfeed.Event) error {
		events = append(events, e)
		return nil
	}); err != nil {
		t.Fatalf("walk stream: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("the run wrote no events at all")
	}
	return events
}

// --- the commitment boundary (ADR 0023 §2.4) --------------------------------

// TestPlanAndExecute_ADeclinedChatPlanManufacturesNoRun is a defect ADR 0023
// §2.4 found while confirming #163 and fixes on the way past: planAndExecute
// saved graph.json into the run directory BEFORE calling confirm, so answering
// `n` left behind a runs/<id>/ holding a graph.json and no state.json — which
// `runs list` reported as "WARNING: skipping run …" and the dashboard painted
// unknown. Saying no manufactured a corrupt run.
//
// The commitment to execute is what creates a run, and for chat it does not
// exist until a human answers. So: no run directory at all, and the paid-for
// spec goes to plans/, where a spec that never belonged to a run belongs.
func TestPlanAndExecute_ADeclinedChatPlanManufacturesNoRun(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
	})

	var out strings.Builder
	captureStdout(t, func() {
		coord := coordinator.New(fake)
		declined := func() (bool, error) { return false, nil }
		if err := planAndExecute(context.Background(), &out, coord, fake,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, false, declined, nil); err != nil {
			t.Fatalf("a declined plan is not an error: %v", err)
		}
	})

	entries, err := os.ReadDir(runsRoot())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a declined plan left %d director(ies) under runs/; nothing was committed to", len(entries))
	}
	// Nothing to skip means nothing to warn about, which is the whole point.
	var listed, warned strings.Builder
	if err := listRuns(&listed, &warned, runsRoot()); err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	if warned.Len() != 0 {
		t.Errorf("a declined plan must not produce a corrupt-run warning: %q", warned.String())
	}
	// The spec was paid for, so it is kept — under plans/.
	planDir := solePlanDir(t)
	if _, err := os.Stat(filepath.Join(planDir, generatedSpecFileName)); err != nil {
		t.Errorf("a declined plan must keep the spec it paid for: %v", err)
	}
	if !strings.Contains(out.String(), "plan discarded") || !strings.Contains(out.String(), planDir) {
		t.Errorf("a declined plan must say where its spec went:\n%s", out.String())
	}
}

// TestPlanAndExecute_AnAcceptedChatPlanRunsWithOneLeg is the other half of the
// same boundary, and the reason ADR 0023 §2.4 restates the "indistinguishable
// on disk" invariant rather than deleting it. A chat run has no planning phase
// — its commitment lands after the planner call — so its stream carries ONE
// run_started where an auto run's carries two, and everything else about the
// directory is the same.
func TestPlanAndExecute_AnAcceptedChatPlanRunsWithOneLeg(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})

	var out strings.Builder
	captureStdout(t, func() {
		coord := coordinator.New(fake)
		accepted := func() (bool, error) { return true, nil }
		if err := planAndExecute(context.Background(), &out, coord, fake,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, false, accepted, nil); err != nil {
			t.Fatalf("planAndExecute: %v", err)
		}
	})

	runDir := soleRunDir(t)
	opens := 0
	for _, e := range readStreamEvents(t, runDir) {
		if e.Type == runfeed.EventRunStarted {
			opens++
			if e.Phase != "" {
				t.Errorf("a chat-started run must have no planning phase, got phase %q", e.Phase)
			}
		}
	}
	if opens != 1 {
		t.Errorf("a chat-started run's stream carries %d run_started line(s), want 1", opens)
	}
	// The three files the invariant names are all there, exactly as for a
	// shell-started run.
	for _, name := range []string{generatedSpecFileName, stateFileName, runfeed.FileName} {
		if _, err := os.Stat(filepath.Join(runDir, name)); err != nil {
			t.Errorf("an accepted chat plan's run directory is missing %s: %v", name, err)
		}
	}
	if status, err := runstatus.Of(runDir); err != nil || status != runstatus.Pass {
		t.Errorf("status = %v (err %v), want %v", status, err, runstatus.Pass)
	}
}

// TestPlanAndExecute_PlanOnlyStillMintsNoRunDirectory is ADR 0023 §3's decided
// question, pinned. The old argument for keeping a preview out of runs/ was a
// mechanism argument — a directory with a graph.json and no state.json reads as
// damage — and §2.5 dissolved exactly that mechanism. The reason that survives
// is the enumeration itself: a preview has none of the six statuses and cannot
// be given one. It is not PLANNING (it finished), not RUNNING, not ABANDONED
// (its process left on purpose), and it has no verdict about work, because
// there was no work.
//
// The load-bearing half is "at any point": a preview must not even open a
// planning leg, or it would leave a directory that later reads FAIL.
func TestPlanAndExecute_PlanOnlyStillMintsNoRunDirectory(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
	})

	sawRunDirDuringPlanning := false
	observed := observingRunner{inner: fake, observe: func(spec runner.NodeInvocation) {
		if !isPlannerCall(spec) {
			return
		}
		if entries, err := os.ReadDir(runsRoot()); err == nil && len(entries) > 0 {
			sawRunDirDuringPlanning = true
		}
	}}

	var out strings.Builder
	captureStdout(t, func() {
		coord := coordinator.New(observed)
		if err := planAndExecute(context.Background(), &out, coord, observed,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, true, nil, nil); err != nil {
			t.Fatalf("planAndExecute --plan-only: %v", err)
		}
	})

	if sawRunDirDuringPlanning {
		t.Error("a preview opened a run directory during its planner call; it has no status to be listed under")
	}
	entries, err := os.ReadDir(runsRoot())
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("a preview left %d director(ies) under runs/", len(entries))
	}
	if _, err := os.Stat(filepath.Join(solePlanDir(t), generatedSpecFileName)); err != nil {
		t.Errorf("a preview must keep the spec it paid for, under plans/: %v", err)
	}
}

// --- the leg's lifetime (ADR 0023 §2.7) -------------------------------------

// TestRunLeg_CloseIsIdempotent is what makes the deferred close-if-open sweep
// safe to compose with executeGraph's own close. Without idempotency the sweep
// would emit a SECOND run_finished onto a leg the scheduler already closed,
// which a consumer pairing opens with closes would read as a leg that closed
// twice.
func TestRunLeg_CloseIsIdempotent(t *testing.T) {
	isolateRunHome(t)
	leg, err := openRunLeg("run-idempotent")
	if err != nil {
		t.Fatalf("openRunLeg: %v", err)
	}
	if err := leg.beginPlanning(); err != nil {
		t.Fatalf("beginPlanning: %v", err)
	}
	if err := leg.Close(runfeed.OutcomeFailed); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	// The sweep, arriving after the fact.
	if err := leg.Close(runfeed.OutcomeFailed); err != nil {
		t.Fatalf("second Close: %v", err)
	}
	closes := 0
	for _, e := range readStreamEvents(t, runDirFor("run-idempotent")) {
		if e.Type == runfeed.EventRunFinished {
			closes++
		}
	}
	if closes != 1 {
		t.Errorf("the leg wrote %d run_finished event(s), want exactly 1", closes)
	}
	// A nil leg takes the same call — that is what lets a caller with no
	// planning phase (`chat`, `--plan-only`) defer the identical line.
	var absent *runLeg
	if err := absent.Close(runfeed.OutcomeFailed); err != nil {
		t.Errorf("Close on a leg that was never opened: %v", err)
	}
}

// TestRunLeg_ClosingReleasesTheLockForALaterLeg pins that Close really gives
// the flock back, not just the file: a leg that closed must be resumable, and
// the planning leg's whole liveness story depends on the lock changing hands.
func TestRunLeg_ClosingReleasesTheLockForALaterLeg(t *testing.T) {
	isolateRunHome(t)
	leg, err := openRunLeg("run-relock")
	if err != nil {
		t.Fatalf("openRunLeg: %v", err)
	}
	if err := leg.beginPlanning(); err != nil {
		t.Fatalf("beginPlanning: %v", err)
	}
	// While it is open, a second leg over the same run is refused — the
	// double-spawn guard that made the lock exist in the first place.
	if _, err := openRunLeg("run-relock"); err == nil {
		t.Error("a second leg was allowed over a run whose leg is open")
	}
	if err := leg.Close(runfeed.OutcomeFailed); err != nil {
		t.Fatalf("Close: %v", err)
	}
	next, err := openRunLeg("run-relock")
	if err != nil {
		t.Fatalf("a closed leg must release its lock for the next one: %v", err)
	}
	if err := next.Close(""); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestPlanAndExecuteCycles_ARefusedCycleLeavesNoOpenLeg is ADR 0023 §2.7's
// named regression, and the reason the sweep is structural rather than a list
// of RunGoal's exits. A leg left open by a process that then exits NORMALLY
// reads ABANDONED — and ABANDONED prints the orphaned-`claude` warning, which
// is the ONLY mitigation ADR 0015 accepted for its largest cost. A leaked leg
// would therefore raise a false double-spend alarm on a clean exit, and a
// warning that cries wolf on healthy runs is worth less on the runs that need
// it.
func TestPlanAndExecuteCycles_ARefusedCycleLeavesNoOpenLeg(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.03},
		// Cycle 2's plan is refused, and its one correction too: RunGoal
		// returns from c.plan, with cycle 2's leg open and executeGraph never
		// reached.
		"plan-2": {Result: refusedCycleSpec, TotalCostUSD: 0.02},
		"plan-3": {Result: refusedCycleSpec, TotalCostUSD: 0.04},
	})

	out, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 3}, nil)
	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) {
		t.Fatalf("expected *PlanRejection, got %T: %v", err, err)
	}

	names := runDirNames(t)
	if len(names) != 2 {
		t.Fatalf("want two run directories, got %v", names)
	}
	refused := filepath.Join(runsRoot(), names[1])
	status, statusErr := runstatus.Of(refused)
	if statusErr != nil {
		t.Fatalf("runstatus.Of: %v", statusErr)
	}
	if status != runstatus.Fail {
		t.Errorf("the refused cycle reads %v, want %v — a leaked leg would read ABANDONED on a clean exit", status, runstatus.Fail)
	}

	// And no surface says a subprocess may still be spending, because none is.
	var listed, warned strings.Builder
	if listErr := listRuns(&listed, &warned, runsRoot()); listErr != nil {
		t.Fatalf("listRuns: %v", listErr)
	}
	for _, text := range []string{out, listed.String()} {
		if strings.Contains(text, runstatus.OrphanWarning) {
			t.Errorf("a clean exit must not raise the orphaned-subprocess alarm:\n%s", text)
		}
	}
}

// TestPlanAndExecuteCycles_EveryCyclesPlanningPhaseIsVisible pins that PLANNING
// is not a single-cycle privilege. Without the per-cycle hook the status would
// exist for `auto` and not for `auto --max-cycles 3`, and a status that depends
// on a flag is worse than no status (ADR 0023 §8).
func TestPlanAndExecuteCycles_EveryCyclesPlanningPhaseIsVisible(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.03},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessMet, TotalCostUSD: 0.03},
	})

	var planningStatuses []runstatus.Status
	observed := observingRunner{inner: fake, observe: func(spec runner.NodeInvocation) {
		if !isPlannerCall(spec) {
			return
		}
		names := runDirNames(t)
		status, err := runstatus.Of(filepath.Join(runsRoot(), names[len(names)-1]))
		if err != nil {
			t.Errorf("runstatus.Of during planning: %v", err)
			return
		}
		planningStatuses = append(planningStatuses, status)
	}}

	var out strings.Builder
	captureStdout(t, func() {
		coord := coordinator.New(observed)
		if err := planAndExecute(context.Background(), &out, coord, observed,
			commonRunFlags{inputs: inputFlag{}}, "add a README section",
			goalCycleOptions{maxCycles: 3}, false, nil, nil); err != nil {
			t.Fatalf("planAndExecute: %v", err)
		}
	})

	if len(planningStatuses) != 2 {
		t.Fatalf("observed %d planner call(s), want 2", len(planningStatuses))
	}
	for i, status := range planningStatuses {
		if status != runstatus.Planning {
			t.Errorf("cycle %d's status during its planner call = %v, want %v", i+1, status, runstatus.Planning)
		}
	}
	// Both cycles settled as PASS afterwards: the per-cycle leg is opened AND
	// handed on, not merely opened.
	for _, name := range runDirNames(t) {
		status, err := runstatus.Of(filepath.Join(runsRoot(), name))
		if err != nil {
			t.Fatalf("runstatus.Of: %v", err)
		}
		if status != runstatus.Pass {
			t.Errorf("run %s = %v, want %v", name, status, runstatus.Pass)
		}
	}
}

// --- exit codes and statuses are computed from different facts --------------

// TestExitCode_AgreesWithTheStatusForASingleRunInvocation is ADR 0023 §2.6's
// invariant, and the NARROW version is the whole invariant. An exit code is the
// writer's answer about the process it just ran; a status is a reader's answer
// about a directory on disk. Where they can be made to agree, the agreement is
// asserted rather than assumed — and where they cannot, the next test says so.
func TestExitCode_AgreesWithTheStatusForASingleRunInvocation(t *testing.T) {
	for _, tc := range []struct {
		name       string
		graph      string
		outcomes   map[string]runner.NodeOutcome
		wantExit   int
		wantStatus runstatus.Status
	}{
		{
			name:       "every node passed",
			graph:      `{"name":"g","nodes":[{"id":"a","prompt":"a"}]}`,
			outcomes:   map[string]runner.NodeOutcome{"a": {SessionID: "s", Result: "PASS", ExitCode: 0}},
			wantExit:   0,
			wantStatus: runstatus.Pass,
		},
		{
			name:       "a node failed",
			graph:      `{"name":"g","nodes":[{"id":"a","prompt":"a","success_check":{"exit_zero":true}}]}`,
			outcomes:   map[string]runner.NodeOutcome{"a": {SessionID: "s", Result: "nope", ExitCode: 1}},
			wantExit:   1,
			wantStatus: runstatus.Fail,
		},
		{
			name:       "a gate paused it",
			graph:      `{"name":"g","nodes":[{"id":"approve","type":"gate"}]}`,
			outcomes:   map[string]runner.NodeOutcome{},
			wantExit:   2,
			wantStatus: runstatus.Paused,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			runID := "run-exit"
			g := mustParse(t, tc.graph)
			fake := runner.NewFakeRunner(tc.outcomes)

			var runErr error
			captureStdout(t, func() {
				runErr = executeGraph(context.Background(), runID, g, fake,
					commonRunFlags{inputs: inputFlag{}}, nil, 0, "g.yaml", []byte(tc.graph), false, nil, nil, nil)
			})
			if got := exitCodeForError(runErr); got != tc.wantExit {
				t.Errorf("exit code = %d, want %d (error %v)", got, tc.wantExit, runErr)
			}
			status, err := runstatus.Of(runDirFor(runID))
			if err != nil {
				t.Fatalf("runstatus.Of: %v", err)
			}
			if status != tc.wantStatus {
				t.Errorf("status = %v, want %v", status, tc.wantStatus)
			}
		})
	}
}

// TestExitCode_DoesNotAgreeWithStatusForTheGoalLoop is the assertion ADR 0023
// §2.6 adds BECAUSE the broad claim was the trap: an earlier draft claimed the
// exit code and the status agree unconditionally, and §9 turns that paragraph
// into a test, so an implementer following the broad wording would have written
// a red test and had to relitigate the ADR to learn which half was wrong.
//
// Two independent reasons, either of which alone breaks a blanket claim. The
// goal loop's exit code is GOAL-level (ADR 0011 §2): stopping unmet exits 1
// even when every run passed — asserting agreement there would not find a bug,
// it would assert that ADR 0011 is wrong. And it is not a function: one
// invocation produces N run directories against one exit code.
func TestExitCode_DoesNotAgreeWithStatusForTheGoalLoop(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-1": {Result: cycleAssessNotMet, TotalCostUSD: 0.03},
		"plan-2":   {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		"assess-2": {Result: cycleAssessNotMet, TotalCostUSD: 0.03},
	})

	_, err := runPlanAndExecute(t, fake, goalCycleOptions{maxCycles: 2}, nil)
	if err == nil {
		t.Fatal("a goal that ran out of cycles unmet must not exit 0")
	}
	if !strings.Contains(err.Error(), "goal not met") {
		t.Fatalf("want the cycles-exhausted error, got %v", err)
	}
	if got := exitCodeForError(err); got != 1 {
		t.Fatalf("the goal error must map to exit 1, got %d: %v", got, err)
	}

	names := runDirNames(t)
	if len(names) != 2 {
		t.Fatalf("want two cycle run directories, got %v", names)
	}
	for _, name := range names {
		status, statusErr := runstatus.Of(filepath.Join(runsRoot(), name))
		if statusErr != nil {
			t.Fatalf("runstatus.Of: %v", statusErr)
		}
		if status != runstatus.Pass {
			t.Errorf("cycle run %s = %v, want %v — every cycle's WORK passed", name, status, runstatus.Pass)
		}
	}
}

// --- ResolveRun's preference (ADR 0023 §2.1.1) ------------------------------

// TestResolveRun_PrefersAPlanningRunOverAnOlderSettledOne is the silent-loss
// case §2.1.1 names: rewritten as `== Running`, ResolveRun would stop
// preferring a run inside its planner call and park the single-run view on an
// older, finished run instead — withholding exactly the state this change
// exists to reveal.
func TestResolveRun_PrefersAPlanningRunOverAnOlderSettledOne(t *testing.T) {
	isolateRunHome(t)
	root := runsRoot()
	// An older settled run.
	older := filepath.Join(root, "run-1-old")
	writeEventFixture(t, older, "run-1-old", []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	})
	// A newer one, inside its planner call, holding its lock.
	newer := filepath.Join(root, "run-2-planning")
	writeEventFixture(t, newer, "run-2-planning", []runfeed.Event{
		{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
	})
	liveLegLock(t, newer)

	resolved, err := serve.ResolveRun(root, "")
	if err != nil {
		t.Fatalf("ResolveRun: %v", err)
	}
	if resolved != "run-2-planning" {
		t.Errorf("ResolveRun = %q, want the planning run", resolved)
	}
}
