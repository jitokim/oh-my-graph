package main

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// parsePlanned builds the graph a planner reply becomes, so these tests judge
// the same value printPlanForRuntime is handed on the auto and chat paths.
func parsePlanned(t *testing.T, spec string) *graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(spec))
	if err != nil {
		t.Fatal(err)
	}
	return g
}

// unsweptVerdictSpec carries the finding the corpus's own reproduction printed
// alongside the fatal ones: `result_matches` with no `exit_zero`, which silently
// drops the node's exit-code guard. It is deliberately NOT the impossible-artifact
// class — that one is refused at plan time and never reaches this screen.
const unsweptVerdictSpec = `{"name":"unswept","version":"1","nodes":[` +
	`{"id":"check","prompt":"judge it","allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"}}]}`

// TestPrintPlan_SweepsThePlannedGraph is #244's advisory half. Before it, a
// planner-emitted graph met no sweep anywhere: `auto` goes planner →
// saveGeneratedSpec → printPlan → executePlan, and `lint` is on none of those
// paths, so a defect the engine could name from the saved spec was paid for
// instead (docs/measurements/0244-auto-path-sweeps.md).
func TestPrintPlan_SweepsThePlannedGraph(t *testing.T) {
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: parsePlanned(t, unsweptVerdictSpec)}, "/tmp/graph.json")

	got := out.String()
	for _, want := range []string{
		"warning: /tmp/graph.json: ",
		`node "check"`,
		"result_matches without exit_zero",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("plan screen does not carry %q:\n%s", want, got)
		}
	}
}

// TestPrintPlan_CleanGraphIsSilent is the control that keeps the sweep from
// becoming noise every plan screen carries. A graph with nothing to report must
// print nothing extra — the plan screen is read before a [y/N], and a warning
// that is always there is one nobody reads.
func TestPrintPlan_CleanGraphIsSilent(t *testing.T) {
	spec := `{"name":"clean","version":"1","nodes":[` +
		`{"id":"scan","prompt":"scan","allowed_tools":["Read"]},` +
		`{"id":"fix","depends_on":["scan"],"prompt":"fix using {{ artifacts.scan }}","allowed_tools":["Edit"]}]}`

	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: parsePlanned(t, spec)}, "/tmp/graph.json")

	if strings.Contains(out.String(), "warning:") {
		t.Errorf("a clean planned graph must print no warning:\n%s", out.String())
	}
}

// TestPrintPlan_ChatPathWarningNamesNoPath pins the empty-path form. On `chat`
// the spec has not been saved when this screen prints — where it lands is what
// the [y/N] decides (ADR 0023 §2.4) — so there is no path to name, and the line
// must drop the segment rather than print `warning: : ...`. Same shape
// warnRuntimePreflight has had for the callers that judge a graph in memory.
func TestPrintPlan_ChatPathWarningNamesNoPath(t *testing.T) {
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: parsePlanned(t, unsweptVerdictSpec)}, "")

	got := out.String()
	if !strings.Contains(got, `warning: node "check"`) {
		t.Errorf("chat plan screen must warn without a path segment:\n%s", got)
	}
	if strings.Contains(got, "warning: :") {
		t.Errorf("empty path printed as an empty segment:\n%s", got)
	}
}

// TestPrintPlan_WarningIsAdviceNotAVerdict states the standing of the new lines
// in the one place a reader might doubt it: they sit on the screen `auto` prints
// immediately before it spawns, and they must not change what runs. printPlan
// returns nothing to exit on, so what this pins is the screen itself — the
// topology is still printed in full beside the warning, not replaced by it.
func TestPrintPlan_WarningIsAdviceNotAVerdict(t *testing.T) {
	var out strings.Builder
	printPlan(&out, coordinator.Plan{Graph: parsePlanned(t, unsweptVerdictSpec)}, "/tmp/graph.json")

	got := out.String()
	if !strings.Contains(got, "  - check") {
		t.Errorf("the warned graph's topology must still print:\n%s", got)
	}
}
