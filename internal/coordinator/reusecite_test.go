package coordinator

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// testMenu is an offered set as a render would hold it (#338): two entries,
// one with slots and one with none.
var testMenu = []ReuseEntry{
	{ID: "read-and-report", Contributes: singleNodeContribution, Binds: []string{"target", "question"}, Summary: "read and report"},
	{ID: "lint-pass", Contributes: singleNodeContribution, Summary: "lint the tree"},
}

// citingReply is a planner reply with one ordinary node and one node whose
// fields are the given JSON pairs, under the id "cite".
func citingReply(pairs string) string {
	return `{"name":"plan","nodes":[` +
		`{"id":"impl","prompt":"do the work","allowed_tools":["Read"]},` +
		`{"id":"cite","depends_on":["impl"],` + pairs + `}]}`
}

// reuseRefusals parses reply the way attemptPlan does and returns every
// refusal validatePlannedNodes makes against offered.
func reuseRefusals(t *testing.T, reply string, offered []ReuseEntry) []string {
	t.Helper()
	g, err := graph.ParsePlannerReply([]byte(reply))
	if err != nil {
		t.Fatalf("reply does not parse: %v", err)
	}
	return planIssueReasons(validatePlannedNodes(g, reply, offered))
}

// assertOneRefusal requires exactly one refusal, naming the citing node and
// carrying every wanted substring.
func assertOneRefusal(t *testing.T, refusals []string, want ...string) {
	t.Helper()
	if len(refusals) != 1 {
		t.Fatalf("got %d refusals, want exactly 1: %q", len(refusals), refusals)
	}
	for _, w := range append([]string{`"cite"`}, want...) {
		if !strings.Contains(refusals[0], w) {
			t.Errorf("refusal %q does not carry %q", refusals[0], w)
		}
	}
}

// #338: a well-formed citation passes — the node writes no prompt and no
// allowed_tools, and neither emptiness check fires. That is the whole of the
// exemption; the next test shows nothing else is exempt.
func TestReuseCitation_WellFormedCitationIsExemptFromTheTwoEmptinessChecks(t *testing.T) {
	refusals := reuseRefusals(t, citingReply(`"reuse":"read-and-report","bind":{"target":"README.md","question":"what is it"}`), testMenu)
	if len(refusals) != 0 {
		t.Fatalf("a well-formed citation was refused: %q", refusals)
	}
	if refusals := reuseRefusals(t, citingReply(`"reuse":"lint-pass"`), testMenu); len(refusals) != 0 {
		t.Fatalf("a citation of a slotless shape, with no bind, was refused: %q", refusals)
	}

	// The same node without reuse: is refused for both — the control that
	// shows the exemption, not some other gap, is what let it through.
	refusals = reuseRefusals(t, citingReply(`"handoff":"artifact"`), testMenu)
	if len(refusals) != 2 || !strings.Contains(refusals[0], "empty prompt") || !strings.Contains(refusals[1], "no allowed_tools") {
		t.Fatalf("an uncited node with no prompt and no tools: %q", refusals)
	}
}

// #338: every other per-node check applies to a citing node unchanged.
func TestReuseCitation_EveryOtherCheckStillApplies(t *testing.T) {
	bind := `"reuse":"read-and-report","bind":{"target":"a","question":"b"},`
	for field, pairs := range map[string]string{
		"gate":     bind + `"type":"gate"`,
		"cwd":      bind + `"cwd":"/tmp/elsewhere"`,
		"agent":    bind + `"agent":"code-reviewer"`,
		"worktree": bind + `"worktree":"lane"`,
		"verify":   bind + `"success_check":{"verify":{"command":"touch /tmp/pwned"}}`,
		"retry":    bind + `"retry":{"max":40,"on":["verify_failed"]}`,
		"bypass":   bind + `"permission_mode":"bypassPermissions"`,
	} {
		t.Run(field, func(t *testing.T) {
			assertOneRefusal(t, reuseRefusals(t, citingReply(pairs), testMenu))
		})
	}
	t.Run("slash in id", func(t *testing.T) {
		reply := `{"name":"plan","nodes":[{"id":"cite/x","reuse":"lint-pass"}]}`
		refusals := reuseRefusals(t, reply, testMenu)
		if len(refusals) != 1 || !strings.Contains(refusals[0], "'/' in its id") {
			t.Fatalf("refusals: %q", refusals)
		}
	})
}

// #338: an id the menu did not list is refused, naming the id and listing the
// offered ids so the repair can converge.
func TestReuseCitation_UnlistedIDIsRefused(t *testing.T) {
	assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"review-style"`), testMenu),
		`"review-style"`, "not one of the shapes offered", "offered: read-and-report, lint-pass")
}

// #338: the file name the menu's id corresponds to is still not the id, and
// neither is a path to it.
func TestReuseCitation_FileNameOrPathAsIDIsRefused(t *testing.T) {
	for _, id := range []string{
		"read-and-report.yaml",
		"graphs/fragments/read-and-report.yaml",
		"/abs/graphs/fragments/read-and-report.yaml",
		"../read-and-report",
	} {
		t.Run(id, func(t *testing.T) {
			assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"`+id+`","bind":{"target":"a","question":"b"}`), testMenu),
				`"`+id+`"`, "not one of the shapes offered", "offered: read-and-report, lint-pass", "never a file name or a path")
		})
	}
}

// #338: a slot the entry does not list is refused on its own.
func TestReuseCitation_UnlistedSlotIsRefused(t *testing.T) {
	assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"read-and-report","bind":{"target":"a","question":"b","verify_command":"x"}`), testMenu),
		`"verify_command"`, "does not have", "its binds: target, question")
}

// #338: a slot the entry lists and the citation leaves out is refused on its
// own, as a separate refusal from an unlisted one.
func TestReuseCitation_MissingSlotIsRefused(t *testing.T) {
	assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"read-and-report","bind":{"target":"a"}`), testMenu),
		`"question"`, "unbound", "its binds: target, question")

	both := reuseRefusals(t, citingReply(`"reuse":"read-and-report","bind":{"target":"a","extra":"b"}`), testMenu)
	if len(both) != 2 || !strings.Contains(both[0], `"extra"`) || !strings.Contains(both[1], `"question"`) {
		t.Fatalf("an unlisted AND a missing slot must be two refusals: %q", both)
	}
}

// #338: a prompt written next to reuse: is refused.
func TestReuseCitation_PromptNextToReuseIsRefused(t *testing.T) {
	assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"lint-pass","prompt":"lint it my way"`), testMenu),
		"own prompt")
}

// #338: allowed_tools written next to reuse: is refused — even tools from the
// allowlist, since the shape supplies the node's tools.
func TestReuseCitation_AllowedToolsNextToReuseIsRefused(t *testing.T) {
	assertOneRefusal(t, reuseRefusals(t, citingReply(`"reuse":"lint-pass","allowed_tools":["Read"]`), testMenu),
		"own allowed_tools")
}

// #338: bind: without reuse: is refused, menu or no menu.
func TestReuseCitation_BindWithoutReuseIsRefused(t *testing.T) {
	for name, offered := range map[string][]ReuseEntry{"offered": testMenu, "nothing offered": nil} {
		t.Run(name, func(t *testing.T) {
			assertOneRefusal(t, reuseRefusals(t, citingReply(`"prompt":"p","allowed_tools":["Read"],"bind":{"target":"a"}`), offered),
				"bind without reuse")
		})
	}
}

// #338: with nothing offered, reuse: is refused by its disposition case alone
// — not as an unlisted id, and without the prompt/tools advice, which would
// point the wrong way when the node has to write both itself. Nor does it
// repeat the cited id: the repair prompt quotes it to a call offered nothing.
func TestReuseCitation_NothingOfferedRefusesReuse(t *testing.T) {
	refusals := reuseRefusals(t, citingReply(`"reuse":"read-and-report","bind":{"target":"a","question":"b"}`), nil)
	assertOneRefusal(t, refusals, "sets reuse, but this plan has nothing it may reuse")
	if strings.Contains(refusals[0], "not one of the shapes offered") {
		t.Errorf("nothing offered was reported as an unlisted id: %q", refusals[0])
	}
	if strings.Contains(refusals[0], "read-and-report") {
		t.Errorf("the refusal repeats the unoffered id: %q", refusals[0])
	}
}
