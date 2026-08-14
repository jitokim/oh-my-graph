package coordinator

import (
	"context"
	"errors"
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// ErrPlanDeclined is the sentinel an ExecuteCycle callback returns when a
// present confirm hook declined the cycle's plan. Declining is a human stop
// and a stop is final (ADR 0011 §1): the loop ends immediately — no replan, no
// retry, no assessment of a run that never started — with StopDeclined.
var ErrPlanDeclined = errors.New("plan declined by the confirm hook")

// GoalOptions is the goal loop's governance. The loop runs on a paid runtime,
// so unbounded iteration is unrepresentable: MaxCycles is required (there is
// no unbounded spelling), and the optional budget ceiling adds a second,
// money-denominated bound on top.
type GoalOptions struct {
	// MaxCycles bounds the loop structurally — the flag IS the bound
	// (ADR 0011 §1). Must be at least 1. A MaxCycles-1 RunGoal is NOT
	// today's single run: its one cycle is still assessed (a paid call)
	// and judged goal-level. The CLI keeps `--max-cycles 1` byte-identical
	// to today by dispatching to the single-cycle path without entering
	// this loop at all.
	MaxCycles int
	// MaxGoalBudgetUSD, when positive, is the optional cross-cycle spend
	// ceiling — a SOFT check at the cycle boundary, never a mid-flight kill
	// (ADR 0011 §3): before starting cycle k ≥ 2 the loop compares what the
	// goal has spent so far (each cycle's run total, planning included, plus
	// each assessment's cost) against it and stops with StopBudgetExceeded.
	// The honest overshoot is therefore up to one cycle plus one assessment.
	// Zero means no ceiling; negative is rejected. A cycle whose cost is
	// unknown ends a ceilinged loop with StopBudgetUnmeasurable rather than
	// counting as $0 (ADR 0025).
	MaxGoalBudgetUSD float64
	// InputKeys are the --input names every cycle's planner call may
	// reference, identical across cycles.
	InputKeys []string
	// OnCycleAssessed, when non-nil, receives each cycle's report the moment
	// its assessment returns — before the loop decides whether to continue —
	// so the caller can print the verdict and persist it (assess.json) live
	// rather than as a closing summary (ADR 0011 §2–§3: the record
	// accumulates as it happens). Purely observational: the loop's control
	// flow never depends on it.
	OnCycleAssessed func(CycleReport)
	// OnCyclePlanning, when non-nil, is called immediately BEFORE each cycle's
	// planner call — the "planning begins" hook ADR 0023 §9 requires so the
	// CLI can mint that cycle's run id and open its leg before a call that is
	// the longest single wait in the tool. Without it, PLANNING would exist for
	// a single-cycle `auto` and not for an iterated one, and a status that
	// depends on a flag is worse than no status.
	//
	// The loop mints nothing itself and takes on no closing obligation: run ids
	// stay the CLI's, as they already are, and so does the leg. That is what
	// lets the CLI's own deferred close-if-open cover the exits this function
	// may grow later, instead of an enumeration here that a maintainer would
	// have to remember to extend (ADR 0023 §2.7).
	//
	// An error stops the loop with that error before anything is spent: failing
	// to take the run lock means another leg holds this run, and planning past
	// that would spend on a cycle with nowhere to record itself.
	OnCyclePlanning func(cycle int) error
}

// ExecuteCycle is the hand-off-to-caller seam: it receives one cycle's
// validated Plan and must run it as an ordinary run — save the spec, print
// the topology, ask the confirm hook when one is present, execute — and
// report the engine-produced evidence, assembled from the cycle's snapshot by
// trusted code. Leave Evidence.PreviousRemaining empty; the loop threads it.
//
// A non-nil error stops the loop and propagates verbatim, so a paused cycle
// (a session-limit pause is the only pause an auto run can hit — ADR 0011 §2)
// pauses the whole loop with its pause error intact for the caller's exit
// mapping. ErrPlanDeclined is the one recognized sentinel: it ends the loop
// as a clean StopDeclined rather than an error. An ordinary FAILED run is not
// an error — return its evidence with RunPassed false; a failed cycle's
// failure detail is precisely what the next plan needs to route around.
type ExecuteCycle func(ctx context.Context, cycle int, plan Plan) (CycleEvidence, error)

// StopReason says why the goal loop ended when it ended cleanly (an
// *AssessError, a *PlanError or an executor error ends it with an error
// instead).
type StopReason string

const (
	// StopGoalMet: the assessor judged the goal met. The verdict always stops
	// the loop — a met goal spends nothing more — but it never decides the
	// run outcome: the caller must still check the final cycle's RunPassed
	// (a failed run survives any verdict — ADR 0011 §2).
	StopGoalMet StopReason = "goal_met"
	// StopCyclesExhausted: MaxCycles ran and the goal is still unmet; the
	// final cycle's Assessment.Remaining says what is left.
	StopCyclesExhausted StopReason = "cycles_exhausted"
	// StopBudgetExceeded: the optional budget ceiling was reached at a cycle
	// boundary; the final cycle's Assessment.Remaining says what is left.
	StopBudgetExceeded StopReason = "budget_exceeded"
	// StopDeclined: a confirm hook declined a cycle's plan. On cycle 1 this
	// is today's "plan discarded"; on cycle k ≥ 2 the caller applies the
	// unmet-goal exit, as if cycles were exhausted (ADR 0011 §1).
	StopDeclined StopReason = "declined"
	// StopBudgetUnmeasurable: a ceilinged loop reached a cycle boundary having
	// spent an amount it cannot know, so it stopped rather than plan another
	// cycle. "Budgets are never evaluated against an unknown cost" (ADR 0025)
	// is a property of the system, not of one command: the CLI refuses
	// --max-goal-budget-usd with a runtime that reports tokens instead of USD,
	// but a cost can also go unknown at runtime — a node killed before it
	// reported, a garbled envelope — and the library must hold the guarantee
	// on its own. Counting an unknown as $0 would let a capped loop iterate
	// forever under a ceiling it can no longer measure, so the loop refuses to
	// continue rather than continue on a number it does not have. Only a
	// ceiling makes the unknown matter; without one the loop never compares
	// anything and runs on.
	//
	// It is a StopReason and not an error for the reason StopDeclined is: by
	// the time it fires, every cycle COMPLETED and was assessed — nothing
	// failed, the loop is simply refusing to buy another one. An error here
	// would make the caller read both a field and an error to learn why a
	// budgeted loop stopped, cost it the `remaining:` line every other clean
	// stop prints, and — because the check runs BEFORE the next cycle's
	// planning hook — invite a summary to bill a cycle that never existed.
	//
	// This is the OPPOSITE of how the same ADR 0025 sentence is implemented one
	// layer down, and deliberately so: schedule.evaluateBudget and
	// ledger.Record.BudgetDeltaUSD simply do not compare when the cost is
	// unknown, letting the node pass. A node budget is a post-hoc backstop on
	// one call that has already finished and already been paid for, so
	// declining to judge it costs nothing; the goal budget is the only thing
	// standing between a paid runtime and unbounded iteration, so declining to
	// judge it there would spend the very money the ceiling exists to bound.
	// Permissive where the money is already gone, strict where it is not.
	//
	// Blast radius, known and accepted: CostUnknown is also what ONE ordinary
	// 20-minute node timeout sets, so a single timed-out node can end a
	// budgeted goal loop that ADR 0011 §2 would otherwise keep iterating ("a
	// FAILED run still iterates"). The stop is honest — that cycle's spend
	// really is unknown — but a reader meeting it should know a timeout, not
	// only an exotic runtime, can produce it.
	// TestRunGoal_ATimedOutCycleEndsABudgetedLoop pins that behaviour.
	StopBudgetUnmeasurable StopReason = "budget_unmeasurable"
)

// CycleReport is one completed, assessed cycle's record — the goal summary's
// row: which run it was, how it ended, what it cost, and how it was judged.
type CycleReport struct {
	Cycle          int
	RunID          string
	RunPassed      bool
	RunCostUSD     float64
	RunCostUnknown bool
	Usage          runner.TokenUsage
	Assessment     Assessment
}

// GoalResult is the loop's outcome: every completed cycle in order, and why
// the loop stopped. A cycle that never completed (declined, paused, failed to
// plan or assess) has no report.
type GoalResult struct {
	Cycles []CycleReport
	Stop   StopReason
}

// RunGoal is the goal loop (ADR 0011): at most opts.MaxCycles cycles of
// plan → validate → execute (via the caller's seam) → assess. Planning and
// validation are coordinator.Plan verbatim every cycle — there is no code
// path in which a cycle's graph reaches the caller unvalidated and no "the
// last plan was fine" caching. Assessment runs after every completed cycle
// including the last; each verdict decides only whether the loop continues,
// never what any run's outcome was.
//
// The loop returns an error when a cycle could not complete — a *PlanError
// (planning or validation failed mid-cycle), an *AssessError (garbage
// verdict), or whatever the executor returned (a pause, an infra failure) —
// always with the completed cycles so far in GoalResult, so the caller can
// still print the goal summary for what did run. A loop that stops itself
// (goal met, cycles exhausted, budget exceeded or unmeasurable, plan declined)
// returns no error: the StopReason alone says why.
func (c *Coordinator) RunGoal(ctx context.Context, goal string, opts GoalOptions, execute ExecuteCycle) (GoalResult, error) {
	if opts.MaxCycles < 1 {
		return GoalResult{}, fmt.Errorf("goal loop: max cycles must be at least 1, got %d", opts.MaxCycles)
	}
	if opts.MaxGoalBudgetUSD < 0 {
		return GoalResult{}, fmt.Errorf("goal loop: goal budget ceiling must not be negative, got %v", opts.MaxGoalBudgetUSD)
	}
	if execute == nil {
		return GoalResult{}, errors.New("goal loop: no execute callback")
	}

	var result GoalResult
	spentUSD := 0.0
	spendUnknown := false
	remaining := ""
	for cycle := 1; cycle <= opts.MaxCycles; cycle++ {
		if cycle > 1 && opts.MaxGoalBudgetUSD > 0 {
			// Order matters, and it is the only place the unknown is
			// tolerable: what IS known can only be a floor on true spend, so
			// "the known part already reaches the ceiling" stays sound however
			// much went unreported, and stopping on it is honest. Only when
			// the known part is under the ceiling does an unknown decide the
			// comparison — and then there is no answer, so the loop refuses.
			if spentUSD >= opts.MaxGoalBudgetUSD {
				result.Stop = StopBudgetExceeded
				return result, nil
			}
			if spendUnknown {
				result.Stop = StopBudgetUnmeasurable
				return result, nil
			}
		}

		if opts.OnCyclePlanning != nil {
			if err := opts.OnCyclePlanning(cycle); err != nil {
				return result, err
			}
		}

		plan, err := c.plan(ctx, goal, opts.InputKeys, remaining)
		if err != nil {
			return result, err
		}

		evidence, err := execute(ctx, cycle, plan)
		if err != nil {
			if errors.Is(err, ErrPlanDeclined) {
				result.Stop = StopDeclined
				return result, nil
			}
			return result, err
		}
		evidence.PreviousRemaining = remaining

		assessment, err := c.Assess(ctx, goal, evidence)
		if err != nil {
			return result, err
		}
		spentUSD += evidence.RunCostUSD + assessment.CostUSD
		spendUnknown = spendUnknown || evidence.RunCostUnknown || assessment.CostUnknown
		report := CycleReport{
			Cycle:          cycle,
			RunID:          evidence.RunID,
			RunPassed:      evidence.RunPassed,
			RunCostUSD:     evidence.RunCostUSD,
			RunCostUnknown: evidence.RunCostUnknown,
			Usage:          evidence.Usage,
			Assessment:     assessment,
		}
		result.Cycles = append(result.Cycles, report)
		if opts.OnCycleAssessed != nil {
			opts.OnCycleAssessed(report)
		}

		if assessment.GoalMet {
			result.Stop = StopGoalMet
			return result, nil
		}
		remaining = assessment.Remaining
	}
	result.Stop = StopCyclesExhausted
	return result, nil
}
