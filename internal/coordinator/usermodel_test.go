package coordinator

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// settingsFileWith writes an operator settings document to a temp dir and
// returns its path. A temp file, never the real ~/.claude/settings.json: the
// Coordinator only reads what its constructor was pointed at, and a test that
// read the real one would pass or fail by whose machine ran it.
func settingsFileWith(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	return path
}

// A plan carries the operator's model so the caller can hand it to the
// Scheduler. It is read ONCE, at plan time, from one key — not per node, and
// not in internal/runner, which is an exec seam.
func TestPlan_CarriesTheOperatorsModel(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	path := settingsFileWith(t, `{"model":"opus[1m]","permissions":{"allow":["Bash(*)"]}}`)

	plan, err := New(fake, WithUserSettingsPath(path)).Plan(context.Background(), "lint the repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.Model != "opus[1m]" {
		t.Errorf("plan.Model = %q, want the operator's own string verbatim", plan.Model)
	}
	if plan.ModelWarning != "" {
		t.Errorf("plan.ModelWarning = %q, want none", plan.ModelWarning)
	}
}

// The ceiling does not move because a model crossed it. Every layer of
// toolPolicyFor must be byte-for-byte what it was: the model is a preference,
// and the layers are capability.
func TestPlan_ModelLeavesTheCeilingUntouched(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	path := settingsFileWith(t, `{"model":"opus[1m]"}`)

	withModel, err := New(fake, WithUserSettingsPath(path)).Plan(context.Background(), "lint the repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	bare, err := New(fake).Plan(context.Background(), "lint the repo", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if bare.Model != "" {
		t.Fatalf("a coordinator built with no settings path read one anyway: %q", bare.Model)
	}
	for id, want := range bare.ToolPolicies {
		got, ok := withModel.ToolPolicies[id]
		if !ok {
			t.Fatalf("node %s lost its policy", id)
		}
		if got.SettingSources == nil || *got.SettingSources != "" {
			t.Errorf("node %s: layer 1 is no longer isolated: %#v", id, got.SettingSources)
		}
		if !equalPolicies(got, want) {
			t.Errorf("node %s ceiling changed:\n got=%#v\nwant=%#v", id, got, want)
		}
	}
}

func equalPolicies(a, b runner.ToolPolicy) bool {
	if (a.SettingSources == nil) != (b.SettingSources == nil) {
		return false
	}
	if a.SettingSources != nil && *a.SettingSources != *b.SettingSources {
		return false
	}
	return a.StrictMCPConfig == b.StrictMCPConfig &&
		strings.Join(a.AllowedTools, ",") == strings.Join(b.AllowedTools, ",") &&
		strings.Join(a.DisallowedTools, ",") == strings.Join(b.DisallowedTools, ",") &&
		strings.Join(a.Tools, ",") == strings.Join(b.Tools, ",") &&
		strings.Join(a.PluginDirs, ",") == strings.Join(b.PluginDirs, ",")
}

// A settings file with no model key, or none at all, is the ordinary case on a
// machine that never expressed a choice: no model, no warning, no failure.
func TestPlan_NoModelChoiceIsSilent(t *testing.T) {
	cases := map[string]string{
		"key absent":  settingsFileWith(t, `{"permissions":{"allow":["Read"]}}`),
		"blank value": settingsFileWith(t, `{"model":"  "}`),
		"file absent": filepath.Join(t.TempDir(), "settings.json"),
		"no path":     "",
	}
	for name, path := range cases {
		fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
		plan, err := New(fake, WithUserSettingsPath(path)).Plan(context.Background(), "lint the repo", nil)
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", name, err)
		}
		if plan.Model != "" || plan.ModelWarning != "" {
			t.Errorf("%s: model = %q, warning = %q, want both empty", name, plan.Model, plan.ModelWarning)
		}
	}
}

// A broken settings file warns ONCE and the plan survives. Killing a paid plan
// — and the 45-node run behind it — over a file oh-my-graph does not own is the
// worse outcome; staying silent about it reproduces the defect with a new
// cause, so neither is what happens.
func TestPlan_MalformedSettingsWarnsButStillPlans(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	path := settingsFileWith(t, `{"model":{"name":"opus"},"env":{"TOKEN":"super-secret-value"}}`)

	plan, err := New(fake, WithUserSettingsPath(path)).Plan(context.Background(), "lint the repo", nil)
	if err != nil {
		t.Fatalf("a broken settings file must not fail the plan: %v", err)
	}
	if plan.Model != "" {
		t.Errorf("plan.Model = %q, want none — a file that could not be decoded chose nothing", plan.Model)
	}
	if plan.ModelWarning == "" {
		t.Fatal("plan.ModelWarning is empty: a broken settings file must not be silent")
	}
	if !strings.Contains(plan.ModelWarning, path) {
		t.Errorf("warning %q does not name the path %q", plan.ModelWarning, path)
	}
	if strings.Contains(plan.ModelWarning, "super-secret-value") {
		t.Errorf("warning %q leaks the file's contents", plan.ModelWarning)
	}
	if plan.Graph == nil || len(plan.Graph.Nodes) != 2 {
		t.Errorf("the plan itself is damaged: %#v", plan.Graph)
	}
}

// The planner call keeps the CLI's default (ADR 0037 §2.5): it already loads the
// operator's settings (it sets no SettingSources at all), and the engine PARSES
// its reply, so its model is a compatibility surface rather than a preference.
func TestPlan_ThePlannerCallCarriesNoModelOfItsOwn(t *testing.T) {
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	path := settingsFileWith(t, `{"model":"opus[1m]"}`)

	if _, err := New(fake, WithUserSettingsPath(path)).Plan(context.Background(), "lint the repo", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.Model != "" {
		t.Errorf("the planner invocation carries Model %q; the operator's choice governs the nodes that do the work, not the engine's machinery", captured.Model)
	}
	if captured.Policy.SettingSources != nil {
		t.Errorf("the planner is no longer loading the user's settings: %#v", captured.Policy.SettingSources)
	}
}

// The assessor keeps the CLI's default for the same reason, and it is the
// sharper case: it IS isolated, so nothing else would have given it a model.
func TestAssessorInvocation_CarriesNoModel(t *testing.T) {
	spec := assessorInvocation("judge this")
	if spec.Model != "" {
		t.Errorf("assessor invocation carries Model %q, want none — the engine parses its verdict", spec.Model)
	}
}
