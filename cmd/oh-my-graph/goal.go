package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// assessFileName is the documented consumer file an iterated auto cycle
// leaves beside graph.json (ADR 0011 §4, docs/RUN-FEED.md): the assessment
// verdict that judged this run, plus the assessment call's own cost — which
// the run's ledger cannot include, because the ledger prints inside
// executeGraph, before the assessment that judges the cycle has run.
const assessFileName = "assess.json"

// assessRecord is assess.json's on-disk shape. Owned here like the snapshot's
// types are owned by runstate: the file is a persisted consumer contract, not
// a dump of the runtime coordinator.Assessment.
type assessRecord struct {
	GoalMet           bool              `json:"goal_met"`
	Remaining         string            `json:"remaining,omitempty"`
	Evidence          string            `json:"evidence,omitempty"`
	AssessCostUSD     float64           `json:"assess_cost_usd"`
	AssessCostUnknown bool              `json:"assess_cost_unknown,omitempty"`
	Usage             runner.TokenUsage `json:"usage,omitzero"`
}

// planAndExecuteCycles is planAndExecute's iterated shape (ADR 0011): it
// hands coordinator.RunGoal an ExecuteCycle callback that runs the exact
// save→print→confirm→execute sequence of the single-cycle path, once per
// validated plan, each cycle as its own ordinary run. The CLI's additions
// around the engine loop are exactly the ADR's: the goal-lineage block in
// each cycle's snapshot, assess.json in each cycle's run directory, the
// per-cycle verdict printed the moment assessment returns, the goal summary
// below the final ledger, and the goal-level exit mapping.
func planAndExecuteCycles(ctx context.Context, out io.Writer, coord *coordinator.Coordinator, nodeRunner runner.NodeRunner, flags commonRunFlags, goal string, cycles goalCycleOptions, confirm func() (bool, error), web browser.Opener) error {
	fmt.Fprintf(out, "Planning a graph for goal %q (up to %d cycles)...\n", goal, cycles.maxCycles)

	firstRunID := ""
	// The CURRENT cycle's run id and leg, minted and opened by the planning
	// hook below rather than inside execute, because the whole point is that
	// they exist BEFORE the planner call — a cycle's planning phase is as long
	// and as invisible as a single-cycle auto's, and a status that depended on
	// --max-cycles would be worse than no status (ADR 0023 §9).
	cycleRunID := ""
	var leg *runLeg
	// The bracket, restored lexically over the ENTIRE RunGoal call. The leg's
	// lifetime now runs through another package's control flow — RunGoal can
	// return on c.plan failing, on a cancelled context, and on any early return
	// a future change adds there — so `defer` at the point of use cannot cover
	// it. Close is idempotent, so executeGraph's own close on the happy path
	// wins and this is a no-op there; when it is NOT a no-op it is closing a leg
	// that would otherwise read ABANDONED after a clean exit and print an
	// orphaned-subprocess warning nobody needs (ADR 0023 §2.7).
	defer func() { closeLeg(leg, runfeed.OutcomeFailed) }()

	onCyclePlanning := func(cycle int) error {
		// The previous cycle's leg is closed by its own executeGraph; closing
		// again here is the idempotent no-op that makes the sweep safe when a
		// cycle returned before ever reaching execution.
		closeLeg(leg, runfeed.OutcomeFailed)

		cycleRunID = newRunID()
		if firstRunID == "" {
			firstRunID = cycleRunID
		}
		opened, err := openRunLeg(cycleRunID)
		if err != nil {
			return err
		}
		leg = opened
		if err := leg.beginPlanning(); err != nil {
			return err
		}
		fmt.Fprintf(out, "— goal cycle %d/%d (run %s), planning… —\n", cycle, cycles.maxCycles, cycleRunID)
		return nil
	}

	execute := func(ctx context.Context, cycle int, plan coordinator.Plan) (coordinator.CycleEvidence, error) {
		runID := cycleRunID
		leg.setPlanningAccounting(plan.CostUSD, plan.CostUnknown, plan.Usage)
		specPath, err := saveGeneratedSpec(runDirFor(runID), plan.Spec)
		if err != nil {
			return coordinator.CycleEvidence{}, err
		}
		printPlanForRuntime(out, plan, specPath, flags.runtime)

		// Unreachable in v1 production — `auto` passes a nil confirm and
		// chat, the one confirm-bearing caller, is single-cycle (ADR 0011
		// §1) — but the hook's per-cycle contract (each plan gated by its
		// own [y/N], a decline ending the loop) is ADR-mandated, so it is
		// implemented and tested here for any future confirm-bearing caller.
		if confirm != nil {
			ok, err := confirm()
			if err != nil {
				return coordinator.CycleEvidence{}, err
			}
			if !ok {
				return coordinator.CycleEvidence{}, coordinator.ErrPlanDeclined
			}
		}

		// The browser opens for cycle 1 only (ADR 0011 §4): every later
		// cycle still serves its own live view and prints its URL, but does
		// not launch another tab. Swapping the opener rather than the web
		// gate keeps the view itself alive on every cycle.
		cycleWeb := web
		if cycle > 1 && web != nil {
			cycleWeb = noReopenOpener{}
		}

		goalRef := &runstate.GoalRef{Text: goal, Cycle: cycle, MaxCycles: cycles.maxCycles, FirstRunID: firstRunID}
		runErr := executePlan(ctx, runID, plan, nodeRunner, flags, specPath, cycleWeb, goalRef, leg)
		if runErr != nil && ctx.Err() != nil {
			// The user interrupted (a halt under Ctrl-C is still a
			// *HaltError): never plan another cycle on a dead context.
			return coordinator.CycleEvidence{}, runErr
		}
		if runErr != nil && !isRunFailure(runErr) {
			// A pause — the session limit is the only pause a planned graph
			// can hit (ADR 0011 §2) — or an infra error pauses/stops the
			// whole loop, with the error intact for mainExitCode's mapping.
			// executeGraph already printed the resume hint.
			return coordinator.CycleEvidence{}, runErr
		}
		return cycleEvidence(runID, plan, runErr == nil)
	}

	result, err := coord.RunGoal(ctx, goal, coordinator.GoalOptions{
		MaxCycles:        cycles.maxCycles,
		MaxGoalBudgetUSD: cycles.maxGoalBudgetUSD,
		InputKeys:        inputKeys(flags.inputs),
		OnCyclePlanning:  onCyclePlanning,
		OnCycleAssessed: func(report coordinator.CycleReport) {
			printCycleVerdict(out, report)
			if err := saveAssessment(runDirFor(report.RunID), report.Assessment); err != nil {
				fmt.Fprintf(os.Stderr, "save %s for run %s: %v\n", assessFileName, report.RunID, err)
			}
		},
	}, execute)

	// The summary prints for whatever completed, error or not: a loop that
	// paused or stopped on a garbage verdict still spent its cycles, and the
	// multiplier must be printed, not derivable (ADR 0011 §4). The error is
	// passed so the cycle that ended the loop mid-flight appears in the
	// multiplier too, instead of silently vanishing from the accounting.
	printGoalSummary(out, goal, result, err)
	if err != nil {
		// A cycle whose planning was refused paid for it like any other, so
		// its spec is persisted here too — the loop's plan step is the same
		// coordinator.plan call the single-cycle path makes.
		//
		// It goes INTO THAT CYCLE'S RUN DIRECTORY (ADR 0023 §3.1): the hook
		// above minted the id and opened the leg before the planner call, so a
		// run exists to hold it and the path says which cycle bought it without
		// a name having to encode that. The leg is closed first, with outcome
		// "failed", so the cycle has settled by the time anything reads it.
		// This is what retired rejectedPlanID and its "<first-run>-cycleK"
		// construction: the lineage is now the run id itself.
		//
		// The fallback is for an error that arrives before any cycle's hook ran
		// (an invalid MaxCycles, a nil executor) — no run exists, so plans/ is
		// the only honest home, exactly as for --plan-only.
		if cycleRunID != "" {
			closeRejectedPlanning(leg, err)
			return noteRejectedPlan(out, runDirFor(cycleRunID), err)
		}
		return noteRejectedPlan(out, planDirFor(newRunID()), err)
	}
	return goalExit(out, result)
}

// isRunFailure reports whether err is the scheduler saying "this run
// completed, and it failed" — *schedule.HaltError or *schedule.RunFailedError
// — as opposed to a pause or an infrastructure error. A failed cycle still
// iterates: its failure detail is precisely what the next plan needs to route
// around (ADR 0011 §2).
func isRunFailure(err error) bool {
	var halt *schedule.HaltError
	var failed *schedule.RunFailedError
	return errors.As(err, &halt) || errors.As(err, &failed)
}

// cycleEvidence assembles the assessor's engine-produced material by
// re-reading the cycle's snapshot from disk — the observation seam ADR 0011
// §2 names: executeGraph prints the ledger and returns only error, so rather
// than widening that signature the loop reads back what the run durably
// recorded. The run total is the planning call plus every node's
// snapshot-recorded spend (a node's cost_usd accumulates across its feedback
// rounds; a retried node's record prices its terminal attempt, exactly as
// the ledger does). Artifact content is read raw but bounded
// (readArtifactBounded caps what one file may contribute, so a huge artifact
// cannot inflate the cycle's peak memory); the coordinator still owns the
// prompt-level excerpting and caps, which live
// beside the prompt they protect. A node with no record (never launched — a
// pruned or cancelled subtree) contributes nothing, and an artifact that
// cannot be read back contributes its verdict without content rather than
// failing the assessment.
func cycleEvidence(runID string, plan coordinator.Plan, runPassed bool) (coordinator.CycleEvidence, error) {
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		return coordinator.CycleEvidence{}, fmt.Errorf("read cycle snapshot for assessment: %w", err)
	}
	evidence := coordinator.CycleEvidence{
		RunID:          runID,
		RunPassed:      runPassed,
		RunCostUSD:     plan.CostUSD,
		RunCostUnknown: plan.CostUnknown,
		Usage:          plan.Usage,
	}
	// Graph order, not map order, so the assessor's material is
	// deterministic for a given run.
	for _, node := range plan.Graph.Nodes {
		rec, ok := snap.Nodes[node.ID]
		if !ok {
			continue
		}
		evidence.RunCostUSD += rec.CostUSD
		evidence.RunCostUnknown = evidence.RunCostUnknown || rec.CostUnknown
		evidence.Usage = addTokenUsage(evidence.Usage, runner.TokenUsage{
			InputTokens: rec.Usage.InputTokens, CachedInputTokens: rec.Usage.CachedInputTokens,
			OutputTokens: rec.Usage.OutputTokens, ReasoningOutputTokens: rec.Usage.ReasoningOutputTokens,
		})
		nodeEvidence := coordinator.NodeEvidence{
			ID:          node.ID,
			Verdict:     string(rec.Verdict),
			Detail:      rec.Detail,
			CostUSD:     rec.CostUSD,
			CostUnknown: rec.CostUnknown,
			Usage: runner.TokenUsage{
				InputTokens: rec.Usage.InputTokens, CachedInputTokens: rec.Usage.CachedInputTokens,
				OutputTokens: rec.Usage.OutputTokens, ReasoningOutputTokens: rec.Usage.ReasoningOutputTokens,
			},
		}
		if rec.ArtifactPath != "" {
			if content, readErr := readArtifactBounded(rec.ArtifactPath); readErr == nil {
				nodeEvidence.Artifact = content
			}
		}
		evidence.Nodes = append(evidence.Nodes, nodeEvidence)
	}
	return evidence, nil
}

// maxArtifactReadBytes bounds what one node's artifact may contribute to the
// assessor's material BEFORE the coordinator's own excerpting runs, so a huge
// artifact cannot inflate the cycle's peak memory on its way to a 2KB excerpt.
const maxArtifactReadBytes = 64 << 10

// artifactReadCutMarker marks the bytes readArtifactBounded dropped from the
// middle of an oversized artifact — the cut is said out loud, never silent.
const artifactReadCutMarker = "\n… (middle omitted at read: the artifact exceeds the read bound) …\n"

// readArtifactBounded reads at most maxArtifactReadBytes of the artifact at
// path: the whole file when it fits, otherwise its head and tail halves around
// a cut marker. Head AND tail, not a prefix, because the coordinator's excerpt
// keeps both for the same reason — a check node prints its "PASS"/"FAIL" last,
// and a prefix-only read would hide exactly that.
func readArtifactBounded(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if info.Size() <= maxArtifactReadBytes {
		data, err := io.ReadAll(f)
		if err != nil {
			return "", err
		}
		return string(data), nil
	}
	half := int64(maxArtifactReadBytes / 2)
	head := make([]byte, half)
	if _, err := io.ReadFull(f, head); err != nil {
		return "", err
	}
	tail := make([]byte, half)
	if _, err := f.ReadAt(tail, info.Size()-half); err != nil {
		return "", err
	}
	return string(head) + artifactReadCutMarker + string(tail), nil
}

// printCycleVerdict prints one cycle's assessment the moment it returns, so
// the goal's record accumulates live rather than arriving as a closing
// summary — the human's early warning that cycles are making no progress
// (ADR 0011 §2–§3).
func printCycleVerdict(w io.Writer, report coordinator.CycleReport) {
	verdict := "goal not met"
	if report.Assessment.GoalMet {
		verdict = "goal met"
	}
	fmt.Fprintf(w, "Cycle %d assessment (run %s): %s (assessment cost %s)\n",
		report.Cycle, report.RunID, verdict, formatCost(report.Assessment.CostUSD, report.Assessment.CostUnknown))
	if report.Assessment.Usage != (runner.TokenUsage{}) {
		fmt.Fprintf(w, "  assessment tokens: %s\n", formatUsage(report.Assessment.Usage))
	}
	if report.Assessment.Remaining != "" {
		fmt.Fprintf(w, "  remaining: %s\n", report.Assessment.Remaining)
	}
	if report.Assessment.Evidence != "" {
		fmt.Fprintf(w, "  evidence: %s\n", report.Assessment.Evidence)
	}
}

// saveAssessment persists one cycle's verdict as assess.json in that cycle's
// run directory, so the observation step leaves the same kind of on-disk
// trace the planning step does (graph.json) — ADR 0011 §4. Owner-only (0o600)
// for the same reason graph.json is: the verdict quotes the goal and the
// evidence the assessor read out of the run.
func saveAssessment(runDir string, a coordinator.Assessment) error {
	data, err := json.MarshalIndent(assessRecord{
		GoalMet:           a.GoalMet,
		Remaining:         a.Remaining,
		Evidence:          a.Evidence,
		AssessCostUSD:     a.CostUSD,
		AssessCostUnknown: a.CostUnknown,
		Usage:             a.Usage,
	}, "", "  ")
	if err != nil {
		return fmt.Errorf("encode assessment: %w", err)
	}
	return os.WriteFile(filepath.Join(runDir, assessFileName), append(data, '\n'), 0o600)
}

// printGoalSummary prints the goal loop's closing accounting below the final
// cycle's ledger: one line per cycle — run id, run outcome, run total
// (planning included), assessment cost, verdict — and the grand total.
// Nothing is averaged or hidden: the entire risk of a loop on a paid runtime
// is the multiplier, and the multiplier must be printed, not derivable
// (ADR 0011 §4). That is why loopErr is part of the accounting: a cycle that
// ended the loop mid-flight (a pause, a planning failure, a garbage verdict)
// still spent, so it appears in the multiplier as an explicit incomplete
// cycle rather than vanishing — with its own cost counted wherever the seam
// carries it: AssessError.CostUSD for a failed assessment, PlanRejection.CostUSD
// for a refused plan (up to two planner calls, since a refusal buys one
// correction). Silent only when there is no spend story at all: no cycle
// completed and no error (a declined cycle-1 plan).
func printGoalSummary(w io.Writer, goal string, result coordinator.GoalResult, loopErr error) {
	if len(result.Cycles) == 0 && loopErr == nil {
		return
	}
	fmt.Fprintf(w, "\nGOAL SUMMARY — %q\n", goal)
	total := 0.0
	anyUnknown := false
	var usage runner.TokenUsage
	for _, c := range result.Cycles {
		total += c.RunCostUSD + c.Assessment.CostUSD
		anyUnknown = anyUnknown || c.RunCostUnknown || c.Assessment.CostUnknown
		usage = addTokenUsage(usage, c.Usage)
		usage = addTokenUsage(usage, c.Assessment.Usage)
		outcome := "FAILED"
		if c.RunPassed {
			outcome = "PASSED"
		}
		verdict := "not met"
		if c.Assessment.GoalMet {
			verdict = "met"
		}
		fmt.Fprintf(w, "  cycle %d: run %s  %s  run %s  assess %s  → %s\n",
			c.Cycle, c.RunID, outcome, formatCost(c.RunCostUSD, c.RunCostUnknown), formatCost(c.Assessment.CostUSD, c.Assessment.CostUnknown), verdict)
	}
	if loopErr == nil {
		fmt.Fprintf(w, "GOAL TOTAL: %s across %d cycle(s)\n", formatCost(total, anyUnknown), len(result.Cycles))
		if usage != (runner.TokenUsage{}) {
			fmt.Fprintf(w, "GOAL TOKENS: %s\n", formatUsage(usage))
		}
		return
	}
	incomplete := len(result.Cycles) + 1
	var assessErr *coordinator.AssessError
	var rejection *coordinator.PlanRejection
	switch {
	case errors.As(loopErr, &assessErr):
		total += assessErr.CostUSD
		anyUnknown = anyUnknown || assessErr.CostUnknown
		usage = addTokenUsage(usage, assessErr.Usage)
		fmt.Fprintf(w, "  cycle %d: incomplete — its assessment failed after spending %s (counted); its run spend appears only on its own ledger above\n",
			incomplete, formatCost(assessErr.CostUSD, assessErr.CostUnknown))
	case errors.As(loopErr, &rejection):
		// A refused plan has no ledger of its own — nothing ran — so this is
		// the only place its spend can be counted. Uncounted, a loop whose
		// cycle-k plan was refused twice would hide two whole planner calls
		// from the multiplier the ADR requires printed.
		total += rejection.CostUSD
		anyUnknown = anyUnknown || rejection.CostUnknown
		usage = addTokenUsage(usage, rejection.Usage)
		fmt.Fprintf(w, "  cycle %d: incomplete — its planning was refused after spending %s (counted); nothing ran, so it has no ledger above\n",
			incomplete, formatCost(rejection.CostUSD, rejection.CostUnknown))
	default:
		fmt.Fprintf(w, "  cycle %d: incomplete — it never reached assessment; its spend (if any) appears only in its own output above\n", incomplete)
	}
	fmt.Fprintf(w, "GOAL TOTAL: %s across %d assessed cycle(s) + 1 incomplete cycle\n", formatCost(total, anyUnknown), len(result.Cycles))
	if usage != (runner.TokenUsage{}) {
		fmt.Fprintf(w, "GOAL TOKENS: %s\n", formatUsage(usage))
	}
}

func addTokenUsage(a, b runner.TokenUsage) runner.TokenUsage {
	a.InputTokens += b.InputTokens
	a.CachedInputTokens += b.CachedInputTokens
	a.OutputTokens += b.OutputTokens
	a.ReasoningOutputTokens += b.ReasoningOutputTokens
	return a
}

// goalExit maps a cleanly-stopped goal loop onto the process exit contract
// (ADR 0011 §2's precedence table): exit 0 requires BOTH the assessor's
// goal-met verdict AND a passed final run — the untrusted judge may stop the
// loop, but it can never convert an engine-reported failure into success.
// Every other clean stop is the unmet-goal exit (1), with the final
// `remaining` in the error. The one nil-without-goal-met case is a declined
// cycle-1 plan, which is today's "plan discarded", not a failed goal.
func goalExit(w io.Writer, result coordinator.GoalResult) error {
	switch result.Stop {
	case coordinator.StopGoalMet:
		final := result.Cycles[len(result.Cycles)-1]
		if final.RunPassed {
			return nil
		}
		return fmt.Errorf("the assessor judged the goal met, but the final cycle's run FAILED (run %s) — a verdict never overrides the engine's own outcome", final.RunID)
	case coordinator.StopDeclined:
		if len(result.Cycles) == 0 {
			fmt.Fprintln(w, "plan discarded.")
			return nil
		}
		return fmt.Errorf("goal not met: the cycle %d plan was declined; %s", len(result.Cycles)+1, finalRemaining(result))
	case coordinator.StopBudgetExceeded:
		return fmt.Errorf("goal not met: the goal budget ceiling was reached after cycle %d; %s", len(result.Cycles), finalRemaining(result))
	default: // StopCyclesExhausted
		return fmt.Errorf("goal not met after %d cycle(s); %s", len(result.Cycles), finalRemaining(result))
	}
}

// finalRemaining phrases the last assessment's `remaining` for an unmet-goal
// exit message.
func finalRemaining(result coordinator.GoalResult) string {
	if len(result.Cycles) == 0 {
		return "no cycle completed"
	}
	remaining := result.Cycles[len(result.Cycles)-1].Assessment.Remaining
	if remaining == "" {
		// Defensive only: the assess contract requires `remaining` on an
		// unmet verdict (Assess rejects its absence as garbage), so every
		// completed-but-unmet cycle carries one.
		return "no remaining work was recorded"
	}
	return "remaining: " + remaining
}

// noReopenOpener is the later-cycle live-view opener: the view still serves
// and prints its URL, but no second browser tab is launched — one surprise
// tab per goal (ADR 0011 §4). It spawns nothing, so it is not a fifth exec
// seam.
type noReopenOpener struct{}

func (noReopenOpener) Open(context.Context, string) error { return nil }
