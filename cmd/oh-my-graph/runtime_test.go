package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

func TestParseCommandLineRuntime(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantRuntime runner.Runtime
		wantArgs    []string
	}{
		{name: "default", args: []string{"run", "graph.yaml"}, wantRuntime: runner.RuntimeClaude, wantArgs: []string{"run", "graph.yaml"}},
		{name: "separate value", args: []string{"--runtime", "codex", "auto", "goal"}, wantRuntime: runner.RuntimeCodex, wantArgs: []string{"auto", "goal"}},
		{name: "equals value", args: []string{"--runtime=codex", "chat"}, wantRuntime: runner.RuntimeCodex, wantArgs: []string{"chat"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotRuntime, _, gotArgs, err := parseCommandLine(test.args)
			if err != nil {
				t.Fatalf("parseCommandLine: %v", err)
			}
			if gotRuntime != test.wantRuntime || !reflect.DeepEqual(gotArgs, test.wantArgs) {
				t.Errorf("parseCommandLine(%q) = (%q, %q), want (%q, %q)", test.args, gotRuntime, gotArgs, test.wantRuntime, test.wantArgs)
			}
		})
	}
}

func TestParseCommandLineRejectsInvalidRuntimeFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--runtime"},
		{"--runtime", "gemini", "run", "graph.yaml"},
		{"--runtime=", "run", "graph.yaml"},
		{"--runtime", "codex", "--runtime", "claude", "run", "graph.yaml"},
	} {
		if _, _, _, err := parseCommandLine(args); err == nil || !strings.Contains(err.Error(), "runtime") {
			t.Errorf("parseCommandLine(%q) error = %v, want runtime error", args, err)
		}
	}
}

func TestParseCommandLineRequiresRuntimeBeforeSubcommand(t *testing.T) {
	gotRuntime, _, gotArgs, err := parseCommandLine([]string{"run", "graph.yaml", "--runtime", "codex"})
	if err != nil {
		t.Fatalf("parseCommandLine: %v", err)
	}
	if gotRuntime != runner.RuntimeClaude || !reflect.DeepEqual(gotArgs, []string{"run", "graph.yaml", "--runtime", "codex"}) {
		t.Fatalf("runtime after subcommand was consumed globally: runtime=%q args=%q", gotRuntime, gotArgs)
	}
}

func TestExecuteGraphPersistsRunWideRuntime(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"codex","nodes":[{"id":"work","prompt":"work"}]}`)
	if err := executeGraph(context.Background(), "codex-run", g,
		runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": {Result: "PASS", CostUnknown: true}}),
		commonRunFlags{inputs: inputFlag{}, runtime: runner.RuntimeCodex}, nil, 0,
		"graph.yaml", []byte(`{"name":"codex","nodes":[{"id":"work","prompt":"work"}]}`), false, nil, nil, nil); err != nil {
		t.Fatalf("executeGraph: %v", err)
	}
	snap, err := runstate.Load(filepath.Join(runDirFor("codex-run"), stateFileName))
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.Runtime != string(runner.RuntimeCodex) {
		t.Errorf("snapshot runtime = %q, want codex", snap.Runtime)
	}
}

func TestPersistedNodeRunnerRefusesRuntimeChange(t *testing.T) {
	isolateRunHome(t)
	dir := runDirFor("codex-run")
	if err := runstate.Write(filepath.Join(dir, stateFileName), runstate.Snapshot{
		RunID: "codex-run", Runtime: string(runner.RuntimeCodex),
		Graph: []byte(`{"name":"codex","nodes":[{"id":"work","prompt":"work"}]}`),
	}); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
	if _, err := persistedNodeRunner("codex-run", runner.RuntimeClaude, true); err == nil || !strings.Contains(err.Error(), `uses runtime "codex"`) {
		t.Errorf("runtime mismatch error = %v", err)
	}
	if _, err := persistedNodeRunner("codex-run", runner.RuntimeClaude, false); err != nil {
		t.Errorf("implicit runtime should use the persisted choice: %v", err)
	}
}

func TestExecutePlanPersistsPlannerAccountingForShow(t *testing.T) {
	isolateRunHome(t)
	raw := []byte(`{"name":"codex-plan","nodes":[{"id":"work","prompt":"work"}]}`)
	g := mustParse(t, string(raw))
	plan := coordinator.Plan{
		Graph: g, Spec: raw, CostUnknown: true,
		Usage:        runner.TokenUsage{InputTokens: 17, CachedInputTokens: 4, OutputTokens: 7, ReasoningOutputTokens: 3},
		ToolPolicies: map[string]runner.ToolPolicy{"work": {}},
	}
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"work": {SessionID: "thread-work", Result: "PASS"},
	})
	if err := executePlan(context.Background(), "codex-plan-run", plan, fake,
		commonRunFlags{inputs: inputFlag{}, runtime: runner.RuntimeCodex}, "graph.json", nil, nil, nil); err != nil {
		t.Fatalf("executePlan: %v", err)
	}

	var out strings.Builder
	if err := showRun(&out, runDirFor("codex-plan-run"), "codex-plan-run"); err != nil {
		t.Fatalf("showRun: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "TOTAL COST: unknown") || !strings.Contains(got, "TOKEN USAGE: input 17, cached 4, output 7, reasoning 3") {
		t.Errorf("persisted detail omitted planner accounting:\n%s", got)
	}
}
