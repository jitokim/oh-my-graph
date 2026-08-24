package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/usermodel"
)

// TestExecutePlan_CarriesTheOperatorsModelIntoEveryNode covers the hop that
// makes model inheritance real: the plan reaching the scheduler together with
// the model it read. The coordinator reading one settings key and the scheduler
// forwarding a model are each tested in their own package, and neither notices
// if this hop drops it — the suite would stay green while every planned node
// answered with a model nobody selected, which is the defect (ADR 0034).
func TestExecutePlan_CarriesTheOperatorsModelIntoEveryNode(t *testing.T) {
	g := mustParse(t, `{"name":"planned","nodes":[
		{"id":"scan","prompt":"scan","allowed_tools":["Read"]},
		{"id":"edit","prompt":"edit","depends_on":["scan"],"allowed_tools":["Edit"]}]}`)
	none := ""
	rec := &capturingRunner{}
	plan := coordinator.Plan{
		Graph: g,
		Model: "opus[1m]",
		ToolPolicies: map[string]runner.ToolPolicy{
			"scan": {AllowedTools: []string{"Read"}, SettingSources: &none, StrictMCPConfig: true},
			"edit": {AllowedTools: []string{"Edit"}, SettingSources: &none, StrictMCPConfig: true},
		},
	}

	isolateRunHome(t)
	if err := executePlan(context.Background(), "model-run", plan, rec, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil); err != nil {
		t.Fatalf("executePlan returned error: %v", err)
	}

	for _, node := range []string{"scan", "edit"} {
		spec := rec.invocationFor(node)
		if spec.Model != "opus[1m]" {
			t.Errorf("node %q ran with Model %q, want the operator's %q", node, spec.Model, "opus[1m]")
		}
		// The ceiling arrived unchanged alongside it: one preference crossed,
		// no capability did.
		if spec.Policy.SettingSources == nil || *spec.Policy.SettingSources != "" {
			t.Errorf("node %q lost settings isolation while gaining a model", node)
		}
		if !spec.Policy.StrictMCPConfig {
			t.Errorf("node %q lost layer 4 while gaining a model", node)
		}
	}
}

// A plan that read no model hands the nodes none, so their argv keeps the CLI's
// own default — the state of every machine that expressed no choice, and the
// behaviour that must stay reachable.
func TestExecutePlan_NoModelLeavesNodesOnTheCLIDefault(t *testing.T) {
	g := mustParse(t, `{"name":"planned","nodes":[{"id":"scan","prompt":"scan","allowed_tools":["Read"]}]}`)
	none := ""
	rec := &capturingRunner{}
	plan := coordinator.Plan{
		Graph:        g,
		ToolPolicies: map[string]runner.ToolPolicy{"scan": {AllowedTools: []string{"Read"}, SettingSources: &none}},
	}

	isolateRunHome(t)
	if err := executePlan(context.Background(), "no-model-run", plan, rec, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil); err != nil {
		t.Fatalf("executePlan returned error: %v", err)
	}
	if got := rec.invocationFor("scan").Model; got != "" {
		t.Errorf("node ran with Model %q, want none", got)
	}
}

// TestAutoCoordinatorReadsTheOperatorsSettings pins the production wiring: a
// Claude `auto` run points the coordinator at the operator's real settings
// path, so the plan it builds carries their model. Asserted through the
// coordinator's own option rather than by launching the CLI, because the
// alternative — a test that reads the machine's real ~/.claude/settings.json —
// would pass or fail by whose machine ran it.
func TestAutoCoordinatorReadsTheOperatorsSettings(t *testing.T) {
	home := t.TempDir()
	t.Setenv("CLAUDE_CONFIG_DIR", filepath.Join(home, ".claude"))
	if err := os.MkdirAll(filepath.Join(home, ".claude"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(home, ".claude", "settings.json"), []byte(`{"model":"claude-fable-5"}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"plan": {Result: `{"name":"planned","nodes":[{"id":"scan","prompt":"scan","allowed_tools":["Read"]}]}`},
	})
	fake.KeyFn = func(runner.NodeInvocation) string { return "plan" }

	coord := coordinator.New(fake, coordinator.WithUserSettingsPath(usermodel.DefaultPath()))
	plan, err := coord.Plan(context.Background(), "scan the repo", nil)
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if plan.Model != "claude-fable-5" {
		t.Fatalf("plan.Model = %q, want the settings file's own %q", plan.Model, "claude-fable-5")
	}
}

// A broken settings file is said once, on stderr, and the run proceeds.
func TestExecutePlan_ModelWarningIsPrintedOnce(t *testing.T) {
	g := mustParse(t, `{"name":"planned","nodes":[
		{"id":"scan","prompt":"scan","allowed_tools":["Read"]},
		{"id":"edit","prompt":"edit","depends_on":["scan"],"allowed_tools":["Edit"]}]}`)
	none := ""
	rec := &capturingRunner{}
	plan := coordinator.Plan{
		Graph:        g,
		ModelWarning: "could not read your model preference (read /tmp/settings.json: unexpected end of JSON input).",
		ToolPolicies: map[string]runner.ToolPolicy{
			"scan": {AllowedTools: []string{"Read"}, SettingSources: &none},
			"edit": {AllowedTools: []string{"Edit"}, SettingSources: &none},
		},
	}

	isolateRunHome(t)
	got, runErr := captureStderr(t, func() error {
		return executePlan(context.Background(), "warn-run", plan, rec, commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil)
	})
	if runErr != nil {
		t.Fatalf("a broken settings file must not fail the run: %v", runErr)
	}
	if n := strings.Count(got, "could not read your model preference"); n != 1 {
		t.Fatalf("warning printed %d times, want exactly 1 (a run owns this warning, not a node):\n%s", n, got)
	}
}
