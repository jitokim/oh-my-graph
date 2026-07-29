package coordinator

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// plannerKey collapses every invocation onto one FakeRunner key: the
// coordinator makes exactly one planner call, so the scripted outcome map has
// a single entry regardless of the generated prompt text.
const plannerKey = "planner"

// validSpec is a well-formed planner reply: two nodes, one artifact edge, the
// second node continuing the first's session.
const validSpec = `{"name":"lint-and-fix","version":"1","nodes":[` +
	`{"id":"scan","prompt":"scan {{ inputs.repo }}","allowed_tools":["Read"]},` +
	`{"id":"fix","depends_on":["scan"],"prompt":"fix using {{ artifacts.scan }}","allowed_tools":["Edit"],"handoff":"session"}]}`

// newPlannerFake scripts the single planner call and captures the invocation
// the coordinator built, so tests can assert on the prompt and permissions.
func newPlannerFake(outcome runner.NodeOutcome) (*runner.FakeRunner, *runner.NodeInvocation) {
	captured := &runner.NodeInvocation{}
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{plannerKey: outcome})
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		*captured = spec
		return plannerKey
	}
	return fake, captured
}

// planExpectingError runs Plan and asserts it failed with a *PlanError.
func planExpectingError(t *testing.T, fake *runner.FakeRunner, goal string) *PlanError {
	t.Helper()
	_, err := New(fake).Plan(context.Background(), goal, nil)
	var planErr *PlanError
	if !errors.As(err, &planErr) {
		t.Fatalf("err = %v, want *PlanError", err)
	}
	return planErr
}

func TestPlan_ReturnsValidatedNormalizedGraph(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec, TotalCostUSD: 0.03})

	plan, err := New(fake).Plan(context.Background(), "lint the repo and fix findings", []string{"repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if string(plan.Spec) != validSpec {
		t.Errorf("spec = %q, want the raw planner JSON", plan.Spec)
	}
	if plan.CostUSD != 0.03 {
		t.Errorf("cost = %v, want the planner call's cost", plan.CostUSD)
	}
	g := plan.Graph
	if g.Name != "lint-and-fix" || len(g.Nodes) != 2 {
		t.Fatalf("graph = %q with %d nodes", g.Name, len(g.Nodes))
	}

	scan, ok := g.NodeByID("scan")
	if !ok {
		t.Fatal("node scan missing")
	}
	// Terse-spec defaults must be filled by the existing normalization.
	if scan.Type != graph.TypeClaudeRun || scan.Handoff != graph.HandoffArtifact {
		t.Errorf("scan not normalized: type=%q handoff=%q", scan.Type, scan.Handoff)
	}

	fix, ok := g.NodeByID("fix")
	if !ok {
		t.Fatal("node fix missing")
	}
	if fix.Handoff != graph.HandoffSession || len(fix.DependsOn) != 1 || fix.DependsOn[0] != "scan" {
		t.Errorf("fix edge wrong: handoff=%q depends_on=%v", fix.Handoff, fix.DependsOn)
	}
}

func TestPlan_MakesExactlyOneReadOnlyCallCarryingGoalAndInputs(t *testing.T) {
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: validSpec})

	_, err := New(fake).Plan(context.Background(), "lint the repo", []string{"port", "repo"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fake.InvocationCount(plannerKey); got != 1 {
		t.Errorf("planner invoked %d times, want exactly 1", got)
	}
	if captured.PermissionMode != "plan" {
		t.Errorf("planner permission mode = %q, want plan", captured.PermissionMode)
	}
	if captured.ResumeSession != "" || len(captured.AllowedTools) != 0 {
		t.Errorf("planner must start fresh with no tools: resume=%q tools=%v", captured.ResumeSession, captured.AllowedTools)
	}
	if !strings.Contains(captured.Prompt, "lint the repo") {
		t.Error("planner prompt does not contain the goal")
	}
	if !strings.Contains(captured.Prompt, "port, repo") {
		t.Error("planner prompt does not list the sorted input keys")
	}
}

func TestPlan_ToleratesFenceAndProseAroundJSON(t *testing.T) {
	reply := "Here is the graph:\n```json\n" + validSpec + "\n```\nGood luck!"
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reply})

	plan, err := New(fake).Plan(context.Background(), "lint the repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.Graph.Nodes) != 2 {
		t.Errorf("got %d nodes, want 2", len(plan.Graph.Nodes))
	}
}

func TestPlan_BracesInProseFailLoudly(t *testing.T) {
	// extractJSON spans first '{' to last '}', so braces in surrounding prose
	// corrupt the extracted spec. Pin that this fails loudly, never silently.
	reply := "Note {an aside} first.\n" + validSpec
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reply})

	if _, err := New(fake).Plan(context.Background(), "lint the repo", nil); err == nil {
		t.Fatal("expected an error for a reply with braces in prose")
	}
}

func TestPlan_EmptyGoalNeverCallsPlanner(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})

	planExpectingError(t, fake, "   ")
	if fake.InvocationCount(plannerKey) != 0 {
		t.Error("planner was invoked for an empty goal")
	}
}

func TestPlan_RunnerErrorPropagates(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{})
	fake.InjectError(plannerKey, errors.New("spawn failed"))

	_, err := New(fake).Plan(context.Background(), "lint the repo", nil)
	if err == nil || !strings.Contains(err.Error(), "spawn failed") {
		t.Fatalf("err = %v, want the wrapped runner error", err)
	}
}

func TestPlan_NonZeroPlannerExitFails(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec, ExitCode: 1})

	planExpectingError(t, fake, "lint the repo")
}

func TestPlan_ReplyWithoutJSONFails(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: "I cannot help with that."})

	planErr := planExpectingError(t, fake, "lint the repo")
	if !strings.Contains(planErr.Error(), "I cannot help with that.") {
		t.Error("PlanError message should show the raw planner reply")
	}
}

func TestPlan_InvalidGeneratedGraphRejectedByValidator(t *testing.T) {
	cyclic := `{"name":"bad","nodes":[` +
		`{"id":"a","depends_on":["b"],"prompt":"a"},` +
		`{"id":"b","depends_on":["a"],"prompt":"b"}]}`
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: cyclic})

	_, err := New(fake).Plan(context.Background(), "lint the repo", nil)
	var validationErr *graph.GraphValidationError
	if !errors.As(err, &validationErr) {
		t.Fatalf("err = %v, want *graph.GraphValidationError", err)
	}
}

func TestPlan_RejectsEmptyPlan(t *testing.T) {
	for _, spec := range []string{`{"name":"noop"}`, `{"name":"noop","nodes":[]}`} {
		fake, _ := newPlannerFake(runner.NodeOutcome{Result: spec})

		planErr := planExpectingError(t, fake, "lint the repo")
		if !strings.Contains(planErr.Reason, "no nodes") {
			t.Errorf("reason %q does not name the empty plan", planErr.Reason)
		}
	}
}

func TestPlan_RejectsNodeWithEmptyPrompt(t *testing.T) {
	blank := `{"name":"bad","nodes":[{"id":"a","prompt":"  "}]}`
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: blank})

	planErr := planExpectingError(t, fake, "lint the repo")
	if !strings.Contains(planErr.Reason, "empty prompt") {
		t.Errorf("reason %q does not name the empty prompt", planErr.Reason)
	}
}

func TestPlan_RejectsGateNode(t *testing.T) {
	gated := `{"name":"bad","nodes":[{"id":"a","type":"gate","prompt":"approve"}]}`
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: gated})

	planErr := planExpectingError(t, fake, "lint the repo")
	if !strings.Contains(planErr.Reason, "gate") {
		t.Errorf("reason %q does not name the gate node", planErr.Reason)
	}
}

func TestPlan_RejectsBypassPermissions(t *testing.T) {
	privileged := `{"name":"bad","nodes":[` +
		`{"id":"a","prompt":"a","permission_mode":"bypassPermissions"}]}`
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: privileged})

	planErr := planExpectingError(t, fake, "lint the repo")
	if !strings.Contains(planErr.Reason, graph.PermissionBypass) {
		t.Errorf("reason %q does not name %s", planErr.Reason, graph.PermissionBypass)
	}
}
