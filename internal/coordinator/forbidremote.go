package coordinator

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/fence"
)

// WithForbidRemoteChanges tells every planning call that the operator forbade
// remote changes (--forbid-remote-changes, ADR 0046 §5.2): the planner prompt
// gains plannerForbidRemoteChangesSentence, in each cycle's prompt and each
// repair's, since both are built from the same base. It is a hint and nothing
// else: the engine's own remote snapshot is the enforcement, so no planned
// node is asked to check the remote either. Unset, the planner prompt is
// byte-identical to one built without this option.
func WithForbidRemoteChanges() Option {
	return func(c *Coordinator) { c.forbidRemoteChanges = true }
}

// plannerForbidRemoteChangesSentence is the ONE sentence --forbid-remote-changes
// adds to the planner prompt (ADR 0046 §5.2), its wording settled in
// testdata/planner-prompt-first-forbid-remote.golden.
const plannerForbidRemoteChangesSentence = `

The operator forbade remote changes for this run and the engine observes the
remote itself, so no planned node pushes, adds a remote, or opens or edits a
pull request.`

// plannerPromptForbidding is plannerPromptFor with, when forbidRemoteChanges
// is set, plannerForbidRemoteChangesSentence after the rules and the start
// commit and before any continuation section, which is fenced exactly as
// plannerPromptFor fences it. Unset, it returns what plannerPromptFor does.
func plannerPromptForbidding(goal string, inputKeys []string, remaining string, verifyCommand string, offered []ReuseEntry, startCommit string, forbidRemoteChanges bool) (string, error) {
	if !forbidRemoteChanges {
		return plannerPromptFor(goal, inputKeys, remaining, verifyCommand, offered, startCommit)
	}
	prompt, err := plannerPromptFor(goal, inputKeys, "", verifyCommand, offered, startCommit)
	if err != nil {
		return "", err
	}
	prompt += plannerForbidRemoteChangesSentence
	if remaining == "" {
		return prompt, nil
	}
	nonce, err := fence.Nonce("planner continuation")
	if err != nil {
		return "", err
	}
	return prompt + fmt.Sprintf(plannerContinuationTemplate, nonce, fence.Truncate(remaining, maxRemainingInPrompt)), nil
}
