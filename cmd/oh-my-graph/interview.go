package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/interview"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// terminalInput is `auto`'s stdin seam (ADR 0044 §2.1(a)): the reader the
// interview's answers come from, and the check that it is a terminal. It is
// read only under --interview, so every other launch leaves it untouched.
// Tests inject a strings.Reader and a fixed answer; production passes
// osStdin.
type terminalInput struct {
	r          io.Reader
	isTerminal func() bool
}

// osStdin is the process's stdin, checked with the same stdlib
// character-device test isTerminal applies to stdout for the live view.
func osStdin() terminalInput {
	return terminalInput{r: os.Stdin, isTerminal: func() bool { return isTerminal(os.Stdin) }}
}

// errInterviewNeedsTerminal is the no-TTY refusal. It comes after the free
// refusals and before any model call or run directory, so it costs nothing
// and never hangs on a keyboard nobody is at. There is no forcing spelling.
var errInterviewNeedsTerminal = errors.New("auto: --interview asks its questions on a terminal, and stdin is not one; " +
	"drop --interview, or run it from a terminal. Nothing was spent")

// runAutoInterview is the one interview of an `auto --interview` launch: run
// once per goal, before cycle 1's planner call, with the coordinator's
// interviewer asker. It prints the prefix the planner will receive, once, so
// the operator sees exactly what is about to be sent; Ctrl-C is the abort. An
// interview that could not end — an interviewer failure, a cancelled context,
// end of input before any answer — abandons the launch with what it spent.
func runAutoInterview(ctx context.Context, out io.Writer, goal string, ask interview.Asker, in io.Reader) (*interview.Result, string, error) {
	result, err := interview.Run(ctx, goal, ask, in, out)
	if err != nil {
		return nil, "", fmt.Errorf("auto --interview: %w (the interview spent %s; nothing was planned and no run directory exists)",
			err, formatCost(result.Cost.CostUSD, result.Cost.CostUnknown))
	}
	prefix, err := result.Render()
	if err != nil {
		return nil, "", fmt.Errorf("auto --interview: %w (the interview spent %s; nothing was planned)",
			err, formatCost(result.Cost.CostUSD, result.Cost.CostUnknown))
	}
	fmt.Fprintf(out, "\nInterview ended (%s): %d asked, %d answered, %d skipped; cost %s, counted in cycle 1's planning cost.\n",
		result.Ending, result.Asked, result.Answered, result.Skipped, formatCost(result.Cost.CostUSD, result.Cost.CostUnknown))
	if prefix == "" {
		fmt.Fprint(out, "No answer was given, so the planner's prompt is exactly what it would be without --interview.\n\n")
		return result, prefix, nil
	}
	fmt.Fprintf(out, "The planner receives this before its instructions, and no planned node receives it:\n\n%s", prefix)
	return result, prefix, nil
}

// interviewAccounting is the interview's whole spend in the runner's types.
func interviewAccounting(result *interview.Result) (float64, bool, runner.TokenUsage) {
	if result == nil {
		return 0, false, runner.TokenUsage{}
	}
	u := result.Cost.Usage
	return result.Cost.CostUSD, result.Cost.CostUnknown, runner.TokenUsage{
		InputTokens: u.InputTokens, CachedInputTokens: u.CachedInputTokens,
		OutputTokens: u.OutputTokens, ReasoningOutputTokens: u.ReasoningOutputTokens,
	}
}

// withInterviewCost folds the interview's spend into the plan it was asked
// for: Plan.CostUSD is the figure that exists to be the sum of everything
// planning bought (ADR 0044 §2.1(c)). Cycle 1's plan alone carries it, so the
// ledger's planning line, state.json's planning cost and the goal loop's
// spend — which --max-goal-budget-usd is checked against — all include it
// exactly once.
func withInterviewCost(plan coordinator.Plan, result *interview.Result) coordinator.Plan {
	if result == nil {
		return plan
	}
	cost, unknown, usage := interviewAccounting(result)
	plan.CostUSD += cost
	plan.CostUnknown = plan.CostUnknown || unknown
	plan.Usage = addTokenUsage(plan.Usage, usage)
	return plan
}

// stageInterview writes the interview's prefix into runDir as interview.md
// and returns the state.json record for it, the pattern the conventions use
// (ADR 0044 §2.2). Every goal-loop cycle stages the same bytes. nil, nil for
// a launch without --interview.
func stageInterview(runDir string, result *interview.Result) (*runstate.Interview, error) {
	if result == nil {
		return nil, nil
	}
	sha, err := result.Stage(runDir)
	if err != nil {
		return nil, err
	}
	cost, unknown, usage := interviewAccounting(result)
	return &runstate.Interview{
		StagedSHA256: sha,
		Asked:        result.Asked,
		Answered:     result.Answered,
		Skipped:      result.Skipped,
		Ending:       string(result.Ending),
		CostUSD:      cost,
		CostUnknown:  unknown,
		Usage: runstate.TokenUsage{
			InputTokens: usage.InputTokens, CachedInputTokens: usage.CachedInputTokens,
			OutputTokens: usage.OutputTokens, ReasoningOutputTokens: usage.ReasoningOutputTokens,
		},
	}, nil
}
