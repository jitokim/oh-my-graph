package coordinator

import (
	"context"
	"encoding/json"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// reviewVerdictPattern is a prefix verdict as the planner prompt describes it,
// decoded — the backslash is the part a careless re-encode would mangle.
const reviewVerdictPattern = `^[*_\s]*APPROVED`

// lintVerdictPattern is the verdict of a node that already names exit_zero.
const lintVerdictPattern = `^OK`

// exitZeroSpec is a typical planned graph from before #371's prompt fix: work,
// a reviewing node that demands a prefix verdict, and a final check node with
// the whole-reply pin — both verdicts written as result_matches ALONE, the way
// the prompt used to hand them out. Beside them sit a node with no
// success_check and one that already pairs its verdict with exit_zero, which
// the normalisation must leave as they are.
var exitZeroSpec = `{"name":"fix-and-check","version":"1","nodes":[` +
	`{"id":"implement","prompt":"fix the bug in {{ inputs.repo }}","allowed_tools":["Read","Edit"]},` +
	`{"id":"notes","depends_on":["implement"],"prompt":"summarise {{ artifacts.implement }}","allowed_tools":["Read"]},` +
	`{"id":"lint","depends_on":["implement"],"prompt":"Read {{ artifacts.implement }}. START your reply with OK or NO.","allowed_tools":["Read"],` +
	`"success_check":{"exit_zero":true,"result_matches":` + strconv.Quote(lintVerdictPattern) + `}},` +
	`{"id":"review","depends_on":["implement"],"prompt":"Review {{ artifacts.implement }}. START your reply with APPROVED or REJECTED, then the findings.","allowed_tools":["Read","Grep"],` +
	`"success_check":{"result_matches":` + strconv.Quote(reviewVerdictPattern) + `}},` +
	`{"id":"check","depends_on":["review","notes","lint"],"prompt":"Run git log --oneline -1 fix-branch. Your reply is exactly PASS when it exists, FAIL otherwise.","allowed_tools":["Bash(git *)"],` +
	`"success_check":{"result_matches":` + strconv.Quote(plannedVerdictPattern) + `}}]}`

// specNodes decodes a plan's Spec without going through graph.Parse, so the
// assertions read the bytes graph.json will hold rather than a Go value that
// a decoder may have defaulted.
func specNodes(t *testing.T, spec []byte) map[string]map[string]json.RawMessage {
	t.Helper()
	var raw struct {
		Nodes []map[string]json.RawMessage `json:"nodes"`
	}
	if err := json.Unmarshal(spec, &raw); err != nil {
		t.Fatalf("plan.Spec is not JSON: %v", err)
	}
	nodes := make(map[string]map[string]json.RawMessage, len(raw.Nodes))
	for _, n := range raw.Nodes {
		var id string
		if err := json.Unmarshal(n["id"], &id); err != nil {
			t.Fatalf("plan.Spec node has no string id: %v", err)
		}
		nodes[id] = n
	}
	return nodes
}

// specCheck is one node's success_check as plan.Spec spells it; ok is false
// when the node carries none.
func specCheck(t *testing.T, nodes map[string]map[string]json.RawMessage, id string) (check map[string]json.RawMessage, ok bool) {
	t.Helper()
	node, found := nodes[id]
	if !found {
		t.Fatalf("plan.Spec lost node %q", id)
	}
	rawCheck, ok := node["success_check"]
	if !ok {
		return nil, false
	}
	if err := json.Unmarshal(rawCheck, &check); err != nil {
		t.Fatalf("node %q: success_check is not an object: %v", id, err)
	}
	return check, true
}

func planExitZeroSpec(t *testing.T, spec string, opts ...Option) Plan {
	t.Helper()
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: spec})
	opts = append([]Option{WithInvocationDir(t.TempDir())}, opts...)
	plan, err := New(fake, opts...).Plan(context.Background(), "fix the bug and check the branch", []string{"repo"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	return plan
}

func graphNode(t *testing.T, g *graph.Graph, id string) graph.Node {
	t.Helper()
	for _, n := range g.Nodes {
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("graph lost node %q", id)
	return graph.Node{}
}

// #371 (a): a planned verdict written as result_matches alone comes out of
// Plan with exit_zero set — in plan.Graph, which runs, AND in plan.Spec, which
// becomes graph.json and the --plan-only plan — and with its pattern exactly as
// the planner wrote it. Remove the requireExitZero call from attemptPlan and
// every assertion on review and check fails.
func TestPlan_PairsAPlannedResultMatchesWithExitZero_371(t *testing.T) {
	plan := planExitZeroSpec(t, exitZeroSpec)
	nodes := specNodes(t, plan.Spec)

	for id, pattern := range map[string]string{"review": reviewVerdictPattern, "check": plannedVerdictPattern} {
		got := graphNode(t, plan.Graph, id).SuccessCheck
		if !got.ExitZero {
			t.Errorf("plan.Graph node %q: exit_zero not set; its exit code would go unchecked", id)
		}
		if got.ResultMatches != pattern {
			t.Errorf("plan.Graph node %q: result_matches = %q, want %q unchanged", id, got.ResultMatches, pattern)
		}

		check, ok := specCheck(t, nodes, id)
		if !ok {
			t.Fatalf("plan.Spec node %q lost its success_check", id)
		}
		if string(check["exit_zero"]) != "true" {
			t.Errorf("plan.Spec node %q: exit_zero = %s, want true — graph.json would replay the node without it", id, check["exit_zero"])
		}
		var specPattern string
		if err := json.Unmarshal(check["result_matches"], &specPattern); err != nil {
			t.Fatalf("plan.Spec node %q: result_matches is not a string: %v", id, err)
		}
		if specPattern != pattern {
			t.Errorf("plan.Spec node %q: result_matches = %q, want %q byte for byte", id, specPattern, pattern)
		}
	}

	// The Spec is what `run`/`resume` load; it must parse to the same checks.
	reloaded, err := graph.Parse(plan.Spec)
	if err != nil {
		t.Fatalf("plan.Spec does not re-parse: %v", err)
	}
	for _, id := range []string{"review", "check"} {
		if !graphNode(t, reloaded, id).SuccessCheck.ExitZero {
			t.Errorf("graph re-parsed from plan.Spec: node %q lost exit_zero", id)
		}
	}

	if want := []string{"review", "check"}; !reflect.DeepEqual(plan.ExitZeroAdded, want) {
		t.Errorf("plan.ExitZeroAdded = %q, want %q (declared order, changed nodes only)", plan.ExitZeroAdded, want)
	}
}

// #371 (b) and (c): the normalisation only ever adds a guard where a verdict
// removed it. A node with no success_check already gets exit-zero by default
// and must stay checkless (an explicit {exit_zero: true} would be the same
// check spelled differently, and graph.json would change for nothing); a node
// that already names exit_zero is left alone and not reported as changed.
func TestPlan_ExitZeroLeavesUncheckedAndAlreadyGuardedNodesAlone_371(t *testing.T) {
	plan := planExitZeroSpec(t, exitZeroSpec)
	nodes := specNodes(t, plan.Spec)

	for _, id := range []string{"implement", "notes"} {
		if got := graphNode(t, plan.Graph, id).SuccessCheck; !got.IsZero() {
			t.Errorf("plan.Graph node %q had no success_check and now has %+v", id, got)
		}
		if check, ok := specCheck(t, nodes, id); ok && len(check) > 0 {
			t.Errorf("plan.Spec node %q had no success_check and now has %v", id, check)
		}
	}

	lint := graphNode(t, plan.Graph, "lint").SuccessCheck
	if want := (graph.SuccessCheck{ExitZero: true, ResultMatches: lintVerdictPattern}); !reflect.DeepEqual(lint, want) {
		t.Errorf("plan.Graph node \"lint\" = %+v, want %+v unchanged", lint, want)
	}
	for _, id := range plan.ExitZeroAdded {
		if id == "lint" || id == "implement" || id == "notes" {
			t.Errorf("plan.ExitZeroAdded lists %q, which it did not change", id)
		}
	}
}

// #371 (e): a typical planned graph produces ZERO verdict-lint warnings once it
// has been through Plan — the advisory `run --dry-run` and the launch print is
// right, and after this fix it has nothing to say about the planner's own
// output. Checked on plan.Graph and on the graph graph.json reloads.
func TestPlan_TypicalPlannedGraphHasNoVerdictLintWarnings_371(t *testing.T) {
	// The fixture must still be one the lint WOULD warn about, or this test
	// proves nothing about the step under it.
	raw, err := graph.Parse([]byte(exitZeroSpec))
	if err != nil {
		t.Fatalf("fixture does not parse: %v", err)
	}
	if len(handoff.LintVerdicts(raw)) == 0 {
		t.Fatal("fixture draws no verdict-lint warning as the planner wrote it; it no longer exercises #371")
	}

	plan := planExitZeroSpec(t, exitZeroSpec)
	if warnings := handoff.LintVerdicts(plan.Graph); len(warnings) != 0 {
		t.Errorf("plan.Graph draws verdict-lint warnings: %v", warnings)
	}
	reloaded, err := graph.Parse(plan.Spec)
	if err != nil {
		t.Fatalf("plan.Spec does not re-parse: %v", err)
	}
	if warnings := handoff.LintVerdicts(reloaded); len(warnings) != 0 {
		t.Errorf("graph reloaded from plan.Spec draws verdict-lint warnings: %v", warnings)
	}
}

// #371: with --verify-cmd, attachVerifyCommand re-encodes the graph after this
// step. The guard must survive that rebuild into the final Spec, beside the
// attached verify on the sink — the ordering comment in attemptPlan is what
// this pins.
func TestPlan_ExitZeroSurvivesVerifyAttachment_371(t *testing.T) {
	plan := planExitZeroSpec(t, exitZeroSpec, WithVerifyCommand(VerifyCommand{Command: "make test"}))

	sink := graphNode(t, plan.Graph, "check").SuccessCheck
	if !sink.ExitZero || sink.ResultMatches != plannedVerdictPattern || sink.Verify == nil {
		t.Errorf("sink check = %+v, want exit_zero, the unchanged pattern and the attached verify together", sink)
	}
	check, ok := specCheck(t, specNodes(t, plan.Spec), "review")
	if !ok || string(check["exit_zero"]) != "true" {
		t.Errorf("plan.Spec node \"review\" lost exit_zero through verify attachment: %v", check)
	}
	if want := []string{"review", "check"}; !reflect.DeepEqual(plan.ExitZeroAdded, want) {
		t.Errorf("plan.ExitZeroAdded = %q, want %q", plan.ExitZeroAdded, want)
	}
}

// #371: the bounded re-plan builds its Plan through the same attemptPlan, so a
// repaired reply gets the guard too. (The goal loop's continuation does as
// well: RunGoal plans every cycle through Coordinator.plan.)
func TestPlan_RepairedPlanAlsoPairsExitZero_371(t *testing.T) {
	fake, _ := newRepairFake(
		runner.NodeOutcome{Result: toolRefusedSpec},
		runner.NodeOutcome{Result: exitZeroSpec},
	)
	plan, err := New(fake, WithInvocationDir(t.TempDir())).Plan(context.Background(), "fix the bug and check the branch", []string{"repo"})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Repaired == nil {
		t.Fatal("plan was not repaired; the fixture no longer reaches the re-plan")
	}
	if !graphNode(t, plan.Graph, "review").SuccessCheck.ExitZero {
		t.Error("repaired plan's review node has no exit_zero")
	}
	if len(plan.ExitZeroAdded) == 0 {
		t.Error("repaired plan records no ExitZeroAdded")
	}
}

// #371: a plan with no result_matches anywhere is untouched — no re-encode, no
// disclosure — so the step costs a plan that never needed it nothing.
func TestPlan_NoVerdictMeansNoExitZeroChange_371(t *testing.T) {
	plan := planExitZeroSpec(t, validSpec)
	if plan.ExitZeroAdded != nil {
		t.Errorf("plan.ExitZeroAdded = %q, want nil", plan.ExitZeroAdded)
	}
	if strings.Contains(string(plan.Spec), "exit_zero") {
		t.Errorf("plan.Spec gained an exit_zero with no verdict to pair it with: %s", plan.Spec)
	}
}

// #371: graph.SuccessCheck.ExitZero is a plain bool, so a planner's explicit
// "exit_zero": false is indistinguishable from an absent key and is
// normalised too. That is the safe reading — an unreviewed plan may not opt a
// node out of the exit-code guard — and this pins that it is deliberate.
func TestPairExitZero_ExplicitFalseIsNormalised_371(t *testing.T) {
	g, err := graph.Parse([]byte(`{"name":"g","version":"1","nodes":[` +
		`{"id":"review","prompt":"review it","allowed_tools":["Read"],"success_check":{"exit_zero":false,"result_matches":"^OK"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	out, _, added, err := pairExitZero(g)
	if err != nil {
		t.Fatal(err)
	}
	if !out.Nodes[0].SuccessCheck.ExitZero || !reflect.DeepEqual(added, []string{"review"}) {
		t.Errorf("explicit exit_zero:false: got %+v, added %q; want exit_zero set and the node listed", out.Nodes[0].SuccessCheck, added)
	}
}

// #371: the graph handed in is never mutated — the rebuilt one comes back
// through graph.Parse, as attachVerification's does — so a caller still
// holding the validated graph sees what validation saw.
func TestPairExitZero_DoesNotMutateItsInput_371(t *testing.T) {
	g, err := graph.Parse([]byte(exitZeroSpec))
	if err != nil {
		t.Fatal(err)
	}
	before := graphNode(t, g, "review").SuccessCheck
	out, spec, _, err := pairExitZero(g)
	if err != nil {
		t.Fatal(err)
	}
	if out == g || spec == nil {
		t.Fatal("pairExitZero changed nodes but handed back the input graph or no spec")
	}
	if after := graphNode(t, g, "review").SuccessCheck; !reflect.DeepEqual(before, after) {
		t.Errorf("input graph mutated: review check %+v became %+v", before, after)
	}
}
