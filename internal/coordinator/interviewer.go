package coordinator

import (
	"context"
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/interview"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// WithInterviewPrefix carries the operator's interview answers to the planner
// — the `auto --interview` flag's implementation (ADR 0044 §2.2). prefix is
// what interview.Result.Render returned: an engine-authored header and the
// answers inside a nonce fence, already bounded and already ending in a blank
// line. plan puts it in front of the planner instruction on every attempt, so
// the engine's rules and the JSON-only reply requirement stay the last words
// the planner reads, and a repair attempt or a cycle-2 continuation answers
// the same question the first attempt was asked.
//
// It is text for the planner and nothing else. It reaches no planned node's
// prompt, and it changes neither toolPolicyFor nor coordinatorInvocation: the
// planner's NodeInvocation differs from a run without it in Prompt alone.
// Empty — the default — is a planner prompt byte-identical to one built
// without this option.
func WithInterviewPrefix(prefix string) Option {
	return func(c *Coordinator) { c.interviewPrefix = prefix }
}

// plannerBase is plannerPromptFor with the interview prefix in front. The
// prefix goes on the base, not on the first prompt alone: plan builds every
// repair attempt and every cycle's continuation from what this returns, and
// one that re-planned without the answers would be answering a different
// question from the one refused (ADR 0044 §2.2).
func (c *Coordinator) plannerBase(goal string, inputKeys []string, remaining string) (string, error) {
	base, err := plannerPromptFor(goal, inputKeys, remaining, c.verifyCommand.Supplied())
	if err != nil {
		return "", err
	}
	return c.interviewPrefix + base, nil
}

// Interviewer returns the asker internal/interview's Run calls once per
// question (ADR 0044 §2.1(c), "Who asks"). Each call is a fresh, stateless
// coordinatorInvocation: read-only permission mode, no tools, the deny list of
// a node that declared nothing, and no SettingSources. That is the planner's
// stance and deliberately not a narrower one: the interviewer's job is to ask
// what the repository does not already answer, so it has to read the
// repository's CLAUDE.md the way the planner does — the reasoning attemptPlan
// gives. No session is resumed, so a failed call costs one call, and
// --accept-loaded-user-config does not touch it: it is a coordinator-owned
// call with no grant.
//
// Every call's cost comes back as the asker's Accounting, including a call
// that exited non-zero, so Result.Cost is the interview's whole spend and the
// caller can report it with the plan instead of dropping it. A spawn that never
// produced a reply returns no accounting, as attemptPlan's does.
func (c *Coordinator) Interviewer() interview.Asker {
	return func(ctx context.Context, prompt string) (string, interview.Accounting, error) {
		outcome, err := runAssessorWithSpawnRetry(ctx, c.runner, coordinatorInvocation(prompt))
		if err != nil {
			return "", interview.Accounting{}, fmt.Errorf("interviewer run: %w", err)
		}
		accounting := interviewAccounting(outcome)
		if outcome.ExitCode != 0 {
			return "", accounting, fmt.Errorf("interviewer exited with code %d\ninterviewer replied:\n%s",
				outcome.ExitCode, fence.Truncate(outcome.Result, maxOutputInError))
		}
		return outcome.Result, accounting, nil
	}
}

// interviewAccounting copies one call's cost and usage into the interview
// package's own types, which restate runner.TokenUsage field for field so that
// package imports no runner.
func interviewAccounting(outcome runner.NodeOutcome) interview.Accounting {
	return interview.Accounting{
		CostUSD:     outcome.TotalCostUSD,
		CostUnknown: outcome.CostUnknown,
		Usage: interview.Usage{
			InputTokens:           outcome.Usage.InputTokens,
			CachedInputTokens:     outcome.Usage.CachedInputTokens,
			OutputTokens:          outcome.Usage.OutputTokens,
			ReasoningOutputTokens: outcome.Usage.ReasoningOutputTokens,
		},
	}
}
