package coordinator

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// withSlot is the fragment loader's substitution token for name, assembled
// here so no test string spells the placeholder form out (#338).
func withSlot(name string) string {
	return "{" + "{ with." + name + " }" + "}"
}

// admissiblePair is an ADMITTED multi-node shape (#338): no nested use:, only
// read-only tools, one slot that lands in a prompt: alone, and exit: declared.
func admissiblePair() string {
	return `fragment: pair
description: two readers in a row
substitutions: [topic]
exit: second
nodes:
  - { id: first, prompt: "read about ` + withSlot("topic") + `", allowed_tools: [Read] }
  - { id: second, depends_on: [first], prompt: "summarise what first found", allowed_tools: [Read, Grep] }
`
}

// pairCitingSpec is a reply citing pair under the id "audit", with extra
// appended to the citing node.
func pairCitingSpec(extra string) string {
	return `{"name":"plan","nodes":[{"id":"impl","prompt":"do it","allowed_tools":["Read"]},` +
		`{"id":"audit","depends_on":["impl"],"reuse":"pair","bind":{"topic":"docs"}` + extra + `}]}`
}

// #338 finding 3a: a node citing a MULTI-NODE shape and also setting handoff —
// a key the splice refuses beside a multi-node use: — was refused at the
// splice, which no re-plan reaches. It is now an ordinary repairable refusal
// naming the key and the node, so the planner's corrected reply is accepted
// after exactly one repair.
func TestReuseKeys_MultiNodeCitationWithHandoffBuysOneRepair(t *testing.T) {
	dir := plantCatalog(t, map[string]string{"pair": admissiblePair()})
	fake, repairPrompt := newRepairFake(
		runnerOutcome(pairCitingSpec(`,"handoff":"artifact"`)),
		runnerOutcome(pairCitingSpec("")),
	)

	plan, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
	if err != nil {
		t.Fatalf("the corrected citation must be accepted: %v", err)
	}
	if plan.Repaired == nil || len(plan.Repaired.Issues) != 1 {
		t.Fatalf("Repaired = %+v, want exactly one refusal repaired", plan.Repaired)
	}
	want := `planned node "audit" sets reuse "pair" and also sets handoff;`
	if issue := plan.Repaired.Issues[0]; !strings.Contains(issue, want) || !strings.Contains(issue, "drop handoff") {
		t.Errorf("refusal = %q, want it to name the node and the key", issue)
	}
	if !strings.Contains(repairPrompt.Prompt, want) {
		t.Error("the repair prompt does not carry the refusal")
	}
	if n := len(fake.Invocations()); n != 2 {
		t.Errorf("made %d planner calls, want 2", n)
	}
	for _, id := range []string{"audit/first", "audit/second"} {
		if _, ok := plan.Graph.NodeByID(id); !ok {
			t.Errorf("no spliced node %q in %+v", id, plan.Graph.Nodes)
		}
	}
}

// #338: the refusal judges the keys the planner WROTE, so a default value
// written out is refused like any other, and it lists exactly the splice's
// own set in the planner's spelling; a single-node citation is not judged by
// it at all.
func TestReuseKeys_MultiNodeRefusalNamesEveryKeyAndTheSplicesSet(t *testing.T) {
	pair := ReuseEntry{ID: "pair", MultiNode: true, Binds: []string{"topic"}}
	refusals := reuseRefusals(t, pairCitingSpec(`,"type":"claude-run","timeout":"5m"`), []ReuseEntry{pair})
	if len(refusals) != 2 {
		t.Fatalf("refusals = %q, want one each for type and timeout", refusals)
	}
	for i, key := range []string{"type", "timeout"} {
		if !strings.Contains(refusals[i], `"audit" sets reuse "pair" and also sets `+key+";") {
			t.Errorf("refusal %d = %q, want it to name %s", i, refusals[i], key)
		}
		if !strings.Contains(refusals[i], "outside bind, cwd, depends_on, id, reuse and worktree") {
			t.Errorf("refusal %d = %q, want the splice's set", i, refusals[i])
		}
	}
	if got := strings.Join(graph.MultiNodeCitingKeys(), ","); got != "bind,cwd,depends_on,id,reuse,worktree" {
		t.Errorf("MultiNodeCitingKeys = %s", got)
	}

	single := ReuseEntry{ID: "pair", Binds: []string{"topic"}}
	if refusals := reuseRefusals(t, pairCitingSpec(`,"timeout":"5m"`), []ReuseEntry{single}); len(refusals) != 0 {
		t.Errorf("a single-node citation was judged by the multi-node rule: %q", refusals)
	}
}

// #338: the menu says what a multi-node citation may carry, from the same
// list the refusal reads, and says nothing of it when every entry is a single
// node.
func TestReuseKeys_MenuStatesTheMultiNodeRuleOnlyWhenOneIsOffered(t *testing.T) {
	single := ReuseEntry{ID: "read-and-report", Contributes: singleNodeContribution, Binds: []string{"target"}, Summary: "read"}
	pair := ReuseEntry{ID: "pair", Contributes: reuseContribution([]string{"first", "second"}), MultiNode: true, Binds: []string{"topic"}, Summary: "pair"}
	const rule = "sets no field outside bind, cwd, depends_on, id, reuse and worktree"

	block, err := reuseMenuBlock([]ReuseEntry{single})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(block, "several nodes") {
		t.Errorf("single-node menu carries the multi-node rule:\n%s", block)
	}
	block, err = reuseMenuBlock([]ReuseEntry{single, pair})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(strings.Fields(block), " "), rule) {
		t.Errorf("menu does not state the multi-node rule:\n%s", block)
	}
}

// #338: the catalog records which shapes are multi-node.
func TestReuseKeys_CatalogRecordsMultiNode(t *testing.T) {
	catalog := scanPlanted(t, map[string]string{"pair": admissiblePair(), "probe": admissibleFragment("probe")})
	got := map[string]bool{}
	for _, entry := range catalog.Offered {
		got[entry.ID] = entry.MultiNode
	}
	if len(got) != 2 || !got["pair"] || got["probe"] {
		t.Errorf("MultiNode by id = %v, skipped %+v", got, catalog.Skipped)
	}
}
