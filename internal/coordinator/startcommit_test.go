package coordinator

import (
	"strings"
	"testing"
)

// #414: with the start commit known, every planner prompt — first, repair and
// continuation — names it and measures scope from it; without it, none does.
func TestPlan_StartCommitScopesThePlannerPrompt(t *testing.T) {
	const sha = "0123456789abcdef0123456789abcdef01234567"
	const scoped = sha + "..HEAD"

	first, repair, continuation := plannerPrompts(t, WithStartCommit(sha))
	for name, prompt := range map[string]string{"first": first.Prompt, "repair": repair.Prompt, "continuation": continuation.Prompt} {
		if !strings.Contains(prompt, sha) {
			t.Errorf("%s: the prompt does not name the start commit %s", name, sha)
		}
		if !strings.Contains(prompt, scoped) {
			t.Errorf("%s: the prompt does not measure scope with %q", name, scoped)
		}
		if strings.Count(prompt, "This run started from commit") != 1 {
			t.Errorf("%s: the start-commit paragraph is not rendered exactly once", name)
		}
		if !strings.Contains(prompt, "no planned node asserts that it has not moved since "+sha) {
			t.Errorf("%s: the prompt does not forbid asserting main has not moved since the start commit", name)
		}
		if !strings.Contains(prompt, "'git merge-base --is-ancestor <a commit this run made> main' exits non-zero") {
			t.Errorf("%s: the prompt does not show the run's work is off main with merge-base --is-ancestor", name)
		}
	}

	plainFirst, plainRepair, plainCont := plannerPrompts(t)
	for name, prompt := range map[string]string{"first": plainFirst.Prompt, "repair": plainRepair.Prompt, "continuation": plainCont.Prompt} {
		if strings.Contains(prompt, sha) || strings.Contains(prompt, scoped) {
			t.Errorf("%s: a prompt built without WithStartCommit names the start commit", name)
		}
	}

	// Empty is unknown: the prompt is byte-identical to one built without it.
	emptyFirst, emptyRepair, emptyCont := plannerPrompts(t, WithStartCommit(""))
	for name, pair := range map[string][2]string{
		"first":        {plainFirst.Prompt, emptyFirst.Prompt},
		"repair":       {plainRepair.Prompt, emptyRepair.Prompt},
		"continuation": {plainCont.Prompt, emptyCont.Prompt},
	} {
		if withoutNonces(pair[0]) != withoutNonces(pair[1]) {
			t.Errorf("%s: WithStartCommit(\"\") changed the planner prompt", name)
		}
	}
}
