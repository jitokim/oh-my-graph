package coordinator

import (
	"strings"
	"testing"
)

// TestPlannerPromptSaysWhenToContinueTheParentSession is #391 at the planner:
// the handoff rule used to say only that "session" continues a single parent's
// claude session, never WHEN, so planned nodes that kept editing the same files
// always ran "artifact" and re-read everything in a fresh session. The rule now
// names the case for "session" and the cases that stay "artifact" — a node
// judging another node's work, and every node inside a feedback loop. The
// phrases must sit INSIDE the handoff rule, not merely somewhere in the prompt
// (the feedback-arc rule also names the rerun target and the reviewer). The
// template is shared by every cycle, so the first and the continuation prompt
// are both checked. Guidance to an untrusted producer, not enforcement:
// graph.Validate still only checks a session node's parent arity.
func TestPlannerPromptSaysWhenToContinueTheParentSession(t *testing.T) {
	first, _, continuation := plannerPrompts(t)
	for name, prompt := range map[string]string{"first": first.Prompt, "continuation": continuation.Prompt} {
		start := strings.Index(prompt, `- "handoff" is "artifact" unless`)
		if start < 0 {
			t.Fatalf("%s planner prompt has no handoff rule", name)
		}
		end := start + 1 + strings.Index(prompt[start+1:], "\n- ")
		if end <= start {
			t.Fatalf("%s planner prompt: the handoff rule has no following rule to end it", name)
		}
		rule := prompt[start:end]
		for _, want := range []string{
			// the case for "session": continuing the single parent's edits
			"continues the SAME work its\n  single parent just did",
			"edits the same files",
			`set "handoff": "session"`,
			// why, in one clause
			"a fresh session re-reads everything",
			// review/check nodes stay "artifact"
			`Keep "artifact" for a node that reviews, checks or
  judges another node's work`,
			// feedback-loop nodes stay "artifact"
			`Keep "artifact" for every node inside a feedback loop`,
			"the rerun\n  target, the reviewer, and anything between them",
			// the existing constraint survives
			`A "session" node must
  have exactly one parent.`,
		} {
			if !strings.Contains(rule, want) {
				t.Errorf("%s planner prompt: the handoff rule lacks %q", name, want)
			}
		}
	}
}
