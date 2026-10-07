package coordinator

import (
	"context"
	"io"
	"os"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
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

// shippedReadAndReport plants the repository's own read-and-report — the
// single-node shape that declares handoff artifact, timeout 10m and a DONE
// success_check — so the keys below are judged against the shape as shipped.
func shippedReadAndReport(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile("../../graphs/fragments/read-and-report.yaml")
	if err != nil {
		t.Fatal(err)
	}
	return plantCatalog(t, map[string]string{"read-and-report": string(data)})
}

// readAndReportCitingSpec is a reply citing read-and-report under the id
// "report", every slot bound, with extra appended to the citing node.
func readAndReportCitingSpec(extra string) string {
	return `{"name":"plan","nodes":[{"id":"impl","prompt":"do it","allowed_tools":["Read"]},` +
		`{"id":"report","depends_on":["impl"],"reuse":"read-and-report",` +
		`"bind":{"target":"README.md","question":"what is it"}` + extra + `}]}`
}

// #338 finding 3b: on a node citing the SINGLE-NODE read-and-report, handoff,
// timeout, retry and success_check are accepted with no repair, the spliced
// node carries the citing node's handoff, retry and success_check over the
// shape's own, and it runs under them: it resumes its parent's session, its
// first reply fails the citing node's result_matches (the shape's DONE would
// have passed it), and the citing node's retry re-runs it to a pass.
func TestReuseKeys_SingleNodeCitationKeepsItsBehaviorKeys(t *testing.T) {
	dir := shippedReadAndReport(t)
	fake, _ := newPlannerFake(runnerOutcome(readAndReportCitingSpec(
		`,"handoff":"session","timeout":"5m","retry":{"max":1,"on":["result_mismatch"]},` +
			`"success_check":{"exit_zero":true,"result_matches":"^VERIFIED"}`)))

	plan, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
	if err != nil {
		t.Fatalf("the citation must be accepted: %v", err)
	}
	if plan.Repaired != nil || len(fake.Invocations()) != 1 {
		t.Fatalf("Repaired = %+v after %d calls, want no repair", plan.Repaired, len(fake.Invocations()))
	}
	report, ok := plan.Graph.NodeByID("report")
	if !ok {
		t.Fatalf("no report node in %+v", plan.Graph.Nodes)
	}
	if report.Handoff != graph.HandoffSession {
		t.Errorf("handoff = %q, want the citing node's session", report.Handoff)
	}
	if report.Retry == nil || report.Retry.Max != 1 || strings.Join(report.Retry.On, ",") != "result_mismatch" {
		t.Errorf("retry = %+v, want the citing node's", report.Retry)
	}
	if report.SuccessCheck.ResultMatches != "^VERIFIED" || !report.SuccessCheck.ExitZero {
		t.Errorf("success_check = %+v, want the citing node's", report.SuccessCheck)
	}
	if !strings.HasPrefix(report.Prompt, "Read README.md") || strings.Join(report.AllowedTools, ",") != "Read,Grep,Glob" {
		t.Errorf("not spliced: prompt %q tools %v", report.Prompt, report.AllowedTools)
	}

	var mu sync.Mutex
	attempts := 0
	nodes := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl":     {Result: "done", SessionID: "s-impl"},
		"report-1": {Result: "DONE the file says nothing", SessionID: "s-report"},
		"report-2": {Result: "VERIFIED README.md:1", SessionID: "s-report"},
	})
	nodes.KeyFn = func(spec runner.NodeInvocation) string {
		if spec.Prompt == "do it" {
			return "impl"
		}
		mu.Lock()
		defer mu.Unlock()
		attempts++
		return "report-" + strconv.Itoa(attempts)
	}
	s := schedule.NewScheduler(nodes, schedule.Options{ProgressWriter: io.Discard, Verifier: verify.NewFakeVerifier(nil), ToolPolicies: plan.ToolPolicies})
	if err := s.Run(context.Background(), plan.Graph, handoff.New(t.TempDir(), nil), ledger.New("test")); err != nil {
		t.Fatalf("the spliced node did not run to a pass: %v", err)
	}
	if got := strings.Join(nodes.Calls(), ","); got != "impl,report-1,report-2" {
		t.Errorf("calls = %s, want the citing node's retry to re-run it once", got)
	}
	if first := nodes.Invocations()[1]; first.ResumeSession != "s-impl" {
		t.Errorf("first attempt resumed %q, want the parent's session s-impl", first.ResumeSession)
	}
}

// #338 finding 3b: cwd on a node citing read-and-report is refused as a
// repairable refusal — the cwd rule every planned node meets — and the
// corrected reply passes after exactly one re-plan.
func TestReuseKeys_SingleNodeCitationWithCwdBuysOneRepair(t *testing.T) {
	dir := shippedReadAndReport(t)
	fake, _ := newRepairFake(
		runnerOutcome(readAndReportCitingSpec(`,"cwd":"docs"`)),
		runnerOutcome(readAndReportCitingSpec("")),
	)

	plan, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
	if err != nil {
		t.Fatalf("the corrected citation must be accepted: %v", err)
	}
	if plan.Repaired == nil || len(plan.Repaired.Issues) != 1 || !strings.Contains(plan.Repaired.Issues[0], `planned node "report" set cwd "docs"`) {
		t.Fatalf("Repaired = %+v, want exactly the cwd refusal", plan.Repaired)
	}
	if n := len(fake.Invocations()); n != 2 {
		t.Errorf("made %d planner calls, want 2", n)
	}
	if report, _ := plan.Graph.NodeByID("report"); report.Cwd != "" {
		t.Errorf("cwd = %q survived the repair", report.Cwd)
	}
}

// #338 finding 3c: read-and-report declares timeout 10m and the citing node
// sets its own. The citing node's value is the one the spliced node keeps —
// the fragment loader overlays the using node's keys onto the shape's body —
// and a citing node that sets none keeps the shape's 10m.
func TestReuseKeys_CitingNodesTimeoutWinsOverTheShapes(t *testing.T) {
	dir := shippedReadAndReport(t)
	for extra, want := range map[string]string{`,"timeout":"5m"`: "5m", "": "10m"} {
		fake, _ := newPlannerFake(runnerOutcome(readAndReportCitingSpec(extra)))
		plan, err := New(fake, WithInvocationDir(dir)).Plan(context.Background(), "audit the docs", nil)
		if err != nil {
			t.Fatal(err)
		}
		if report, _ := plan.Graph.NodeByID("report"); report.Timeout != want {
			t.Errorf("citing node %q: spliced timeout = %q, want %q", extra, report.Timeout, want)
		}
	}
}
