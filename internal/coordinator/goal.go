package coordinator

import (
	"context"
	"errors"
	"fmt"
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
	// (ADR 0011 §1). Must be at least 1; 1 is exactly today's single-cycle
	// behaviour except that the one cycle is still assessed.
	MaxCycles int
	// MaxGoalBudgetUSD, when positive, is the optional cross-cycle spend
	// ceiling — a SOFT check at the cycle boundary, never a mid-flight kill
	// (ADR 0011 §3): before starting cycle k ≥ 2 the loop compares what the
	// goal has spent so far (each cycle's run total, planning included, plus
	// each assessment's cost) against it and stops with StopBudgetExceeded.
	// The honest overshoot is therefore up to one cycle plus one assessment.
	// Zero means no ceiling; negative is rejected.
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
)

// CycleReport is one completed, assessed cycle's record — the goal summary's
// row: which run it was, how it ended, what it cost, and how it was judged.
type CycleReport struct {
	Cycle      int
	RunID      string
	RunPassed  bool
	RunCostUSD float64
	Assessment Assessment
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
// still print the goal summary for what did run.
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
	remaining := ""
	for cycle := 1; cycle <= opts.MaxCycles; cycle++ {
		if cycle > 1 && opts.MaxGoalBudgetUSD > 0 && spentUSD >= opts.MaxGoalBudgetUSD {
			result.Stop = StopBudgetExceeded
			return result, nil
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
		report := CycleReport{
			Cycle:      cycle,
			RunID:      evidence.RunID,
			RunPassed:  evidence.RunPassed,
			RunCostUSD: evidence.RunCostUSD,
			Assessment: assessment,
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
