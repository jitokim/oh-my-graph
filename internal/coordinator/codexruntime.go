package coordinator

// WithCodexRuntime tells every planning call that the run's nodes run under
// the Codex runtime (#457): the planner prompt gains
// plannerCodexNoCommitSentence, in each cycle's prompt and each repair's,
// since both are built from the same base. Codex's workspace-write sandbox
// mounts .git read-only, so a node told to commit fails to do what it was
// told, and one run uses one runtime (ADR 0025), so no later node can commit
// for it. Unset, the planner prompt is byte-identical to one built without
// this option.
func WithCodexRuntime() Option {
	return func(c *Coordinator) { c.codexRuntime = true }
}

// plannerCodexNoCommitSentence is the ONE paragraph the Codex runtime adds to
// the planner prompt (#457), its wording settled in
// testdata/planner-prompt-first-codex.golden.
const plannerCodexNoCommitSentence = `

This run's nodes run under the Codex runtime, whose sandbox keeps .git
read-only, so no planned node can commit: plan no commit step and tell no
node to commit, stage or amend. Changes stay in the working tree for the
operator, and the end-of-run 'working tree:' line reports them.`

// codexRuntimeSuffix is the Codex sentence when codexRuntime is set, and empty
// otherwise. plannerBase appends it after forbidRemoteSuffix's, so with both
// options the forbid-remote sentence comes first and both sit ahead of any
// continuation section.
func codexRuntimeSuffix(codexRuntime bool) string {
	if !codexRuntime {
		return ""
	}
	return plannerCodexNoCommitSentence
}
