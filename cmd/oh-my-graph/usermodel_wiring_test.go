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
// answered with a model nobody selected, which is the defect (ADR 0037).
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

// The resumed leg's half of ADR 0037 §2.8. The exception for a malformed settings
// file rests on the fallback being ANNOUNCED wherever a planned node is spawned,
// and `resume` is the second such place — its warning was uncovered while the
// first leg's (TestExecutePlan_ModelWarningIsPrintedOnce) was not, which made
// half the exception's premise a reading of the code rather than a measurement.
func TestResumedPlannedModel_MalformedSettingsWarnsAndNamesThePath(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, usermodel.SettingsFileName)
	// A credential in the fixture: the warning may name the path and the decode
	// error, never a byte of the contents (internal/usermodel's one-field struct).
	if err := os.WriteFile(path, []byte(`{"model": "opus[1m]", "env": {"TOKEN": "super-secret-value"`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	none := ""
	policies := map[string]runner.ToolPolicy{"scan": {AllowedTools: []string{"Read"}, SettingSources: &none}}

	var out strings.Builder
	model := resumedPlannedModel(&out, runner.RuntimeClaude, policies, path)

	t.Logf("resumedPlannedModel returned model=%q, wrote:\n%s", model, out.String())
	if !strings.Contains(out.String(), "could not read your model preference") {
		t.Fatalf("a broken settings file must not be silent on a resumed leg; got:\n%s", out.String())
	}
	if !strings.Contains(out.String(), path) {
		t.Errorf("warning does not name the settings path %q:\n%s", path, out.String())
	}
	if strings.Contains(out.String(), "super-secret-value") {
		t.Errorf("warning leaks the settings file's contents:\n%s", out.String())
	}
}

// The same seam on the ordinary path: a readable choice is carried to the leg's
// nodes and says nothing.
func TestResumedPlannedModel_CarriesTheOperatorsChoice(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, usermodel.SettingsFileName)
	if err := os.WriteFile(path, []byte(`{"model": "opus[1m]"}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	none := ""
	policies := map[string]runner.ToolPolicy{"scan": {AllowedTools: []string{"Read"}, SettingSources: &none}}

	var out strings.Builder
	if got := resumedPlannedModel(&out, runner.RuntimeClaude, policies, path); got != "opus[1m]" {
		t.Fatalf("resumedPlannedModel = %q, want the settings file's own %q", got, "opus[1m]")
	}
	if out.String() != "" {
		t.Errorf("a readable settings file owes the screen nothing; got:\n%s", out.String())
	}
}

// A hand-written graph carries no tool ceiling, and a Codex run has no --model
// at all (ADR 0025) — neither reads the file, so neither can warn about it.
func TestResumedPlannedModel_OnlyAClaudePlannedLegReadsTheFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, usermodel.SettingsFileName)
	if err := os.WriteFile(path, []byte(`{"model": "opus[1m]"}`), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	none := ""
	planned := map[string]runner.ToolPolicy{"scan": {AllowedTools: []string{"Read"}, SettingSources: &none}}

	for name, tc := range map[string]struct {
		rt       runner.Runtime
		policies map[string]runner.ToolPolicy
	}{
		"hand-written graph has no ceiling": {runner.RuntimeClaude, nil},
		"codex leg sends no model":          {runner.RuntimeCodex, planned},
	} {
		var out strings.Builder
		if got := resumedPlannedModel(&out, tc.rt, tc.policies, path); got != "" || out.String() != "" {
			t.Errorf("%s: model = %q, wrote %q; want both empty", name, got, out.String())
		}
	}
}
