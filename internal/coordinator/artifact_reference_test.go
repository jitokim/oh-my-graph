package coordinator

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// nonAncestorSpec is the corpus's own shape, written as the planner emitted it
// (docs/measurements/0244-auto-path-sweeps.md, run 20260820-162555): a node that
// reads another node's artifact without depending on it. Every load rule holds —
// it is a VALID graph, and it planned, ran and died at
// `cannot resolve {{ artifacts.corpus }}: artifact not available`.
const nonAncestorSpec = `{"name":"non-ancestor","version":"1","nodes":[` +
	`{"id":"corpus","prompt":"count the runs","allowed_tools":["Read"]},` +
	`{"id":"predicate","prompt":"state the predicate","allowed_tools":["Read"]},` +
	`{"id":"writeup","depends_on":["predicate"],"prompt":"write it up from {{ artifacts.corpus }}","allowed_tools":["Edit"]}]}`

// TestPlan_RefusesArtifactTokenNamingANonAncestor is the escalation #244 takes.
// The advisory already existed for this graph — `lint` prints it from the saved
// spec — and the run died anyway, because `auto` plans, saves and executes
// without ever calling a sweep. What the refusal must carry is what the planner
// needs to correct it in one re-plan: which node, which token, and both exits,
// since the rule cannot tell a mis-wired reference from a quoted example.
func TestPlan_RefusesArtifactTokenNamingANonAncestor(t *testing.T) {
	fake, _ := newPlannerFake(runnerOutcome(nonAncestorSpec))

	planErr := planExpectingError(t, fake, "measure the corpus and write it up")
	for _, want := range []string{`"writeup"`, "{{ artifacts.corpus }}", "not an ancestor", "depends_on", "break the two braces apart"} {
		if !strings.Contains(planErr.Reason, want) {
			t.Errorf("refusal %q does not carry %q", planErr.Reason, want)
		}
	}
}

// TestPlan_RefusesArtifactTokenNamingNoNode is the class three of the corpus's
// four graphs actually wrote: the token as an ILLUSTRATION, quoting the syntax
// at the model rather than wiring anything. Intent does not save it — the engine
// resolves every token in a prompt, including one that is only being explained —
// so the refusal has to fire here too, and has to say that, or the planner reads
// a complaint about wiring it never meant to do and re-emits the same prompt.
func TestPlan_RefusesArtifactTokenNamingNoNode(t *testing.T) {
	spec := `{"name":"illustration","version":"1","nodes":[` +
		`{"id":"emit","prompt":"read a prior node's output with {{ artifacts.x }}, e.g. {{ artifacts.x }}","allowed_tools":["Read"]}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	planErr := planExpectingError(t, fake, "explain the artifact syntax")
	for _, want := range []string{`"emit"`, "{{ artifacts.x }}", "is not a node in the graph", "only being quoted or explained"} {
		if !strings.Contains(planErr.Reason, want) {
			t.Errorf("refusal %q does not carry %q", planErr.Reason, want)
		}
	}
}

// TestPlan_ArtifactTokenNamingAnAncestorIsAccepted is the negative control that
// makes the rule mean something narrower than "planned graphs may not read
// artifacts". Reading an ancestor's artifact is the mechanism the planner prompt
// asks for; refusing it would refuse nearly every plan.
func TestPlan_ArtifactTokenNamingAnAncestorIsAccepted(t *testing.T) {
	spec := strings.Replace(nonAncestorSpec, `"depends_on":["predicate"]`, `"depends_on":["predicate","corpus"]`, 1)
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "measure the corpus and write it up", nil); err != nil {
		t.Fatalf("a token naming an ancestor is the wiring the prompt asks for and must plan, got: %v", err)
	}
}

// TestPlan_ManyImpossibleTokensCostOneRefusal pins the compaction. The corpus's
// worst graph carried SIX of these in one plan; per-token refusals would repeat
// one paragraph six times into a repair prompt truncated at maxIssuesInPrompt,
// which is how the graph-level refusals get crowded out of the one retry that
// has to converge. It also reads the plural rendering for a leftover singular:
// a refusal listing three faults that then says "the node" names a node the
// planner has to guess at.
func TestPlan_ManyImpossibleTokensCostOneRefusal(t *testing.T) {
	spec := `{"name":"three-faults","version":"1","nodes":[` +
		`{"id":"corpus","prompt":"count the runs","allowed_tools":["Read"]},` +
		`{"id":"a","prompt":"use {{ artifacts.corpus }}","allowed_tools":["Read"]},` +
		`{"id":"b","prompt":"use {{ artifacts.nope }}","allowed_tools":["Read"]},` +
		`{"id":"c","prompt":"use {{ artifacts.c }}","allowed_tools":["Read"]}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	issues := validatePlannedArtifactReferences(parsePlannedSpec(t, spec))
	if len(issues) != 1 {
		t.Fatalf("three faults must render one refusal, got %d: %v", len(issues), issues)
	}
	reason := issues[0].Reason
	for _, want := range []string{`"a"`, `"b"`, `"c"`, "each of those nodes", "planned nodes reference artifacts"} {
		if !strings.Contains(reason, want) {
			t.Errorf("compacted refusal %q does not carry %q", reason, want)
		}
	}
	if strings.Contains(reason, "the node fails") {
		t.Errorf("compacted refusal keeps a singular that names no node: %q", reason)
	}

	// And the whole path still refuses, so the compaction is what Plan returns
	// rather than something only this unit test sees.
	planExpectingError(t, fake, "read three artifacts")
}

// parsePlannedSpec parses a planner reply the way attemptPlan does — through
// graph.Parse — so a unit test of one validator judges the same *graph.Graph
// the coordinator judges.
func parsePlannedSpec(t *testing.T, spec string) *graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(spec))
	if err != nil {
		t.Fatalf("spec must parse: %v", err)
	}
	return g
}
