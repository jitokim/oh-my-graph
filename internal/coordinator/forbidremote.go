package coordinator

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

// forbidRemoteSuffix is the suffix plannerPromptFor takes: the forbid-remote
// sentence when forbidRemoteChanges is set, and empty otherwise, so an unset
// run's planner prompt is byte-identical to one built without the option.
// plannerPromptFor appends it after the rules and the start commit and only
// then fences any continuation section, so a first attempt and a continuation
// carry it in the same place and `remaining` is fenced the same way either way.
func forbidRemoteSuffix(forbidRemoteChanges bool) string {
	if !forbidRemoteChanges {
		return ""
	}
	return plannerForbidRemoteChangesSentence
}
