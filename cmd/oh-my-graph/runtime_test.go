package main

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
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

// budgetedCodexGraph is one node with a cap the Codex runtime cannot evaluate —
// the smallest fixture that makes runner.ValidateGraphForRuntime return a
// warning and no error.
const budgetedCodexGraph = `
name: capped
nodes:
  - { id: work, prompt: work, budget_usd: 2.50 }
`

// budgetWarningMarker is the part of the warning that only the preflight line
// can produce: noteCodexRuntimePolicy's prose also says "cannot apply", so
// counting on that alone would count the disclosure as a second copy.
const budgetWarningMarker = `node "work": budget_usd 2.50 cannot apply`

// TestLintForRuntime_SurfacesBudgetWarning and its --dry-run twin pin the two
// spawn-free call sites of runner.ValidateGraphForRuntime. ADR 0026's loudest
// claim is that all five sites PRINT — deleting a warnRuntimePreflight call
// otherwise leaves `make test` green, which makes the claim a comment rather
// than a fact.
func TestLintForRuntime_SurfacesBudgetWarning(t *testing.T) {
	path := writeGraphFile(t, budgetedCodexGraph)

	var out, warn strings.Builder
	if err := lintGraphForRuntime(&out, &warn, path, runner.RuntimeCodex); err != nil {
		t.Fatalf("a budgeted graph must still lint clean under codex: %v", err)
	}
	if got := warn.String(); !strings.Contains(got, budgetWarningMarker) || !strings.Contains(got, "timeout") {
		t.Errorf("lint --runtime codex dropped the preflight warning:\n%s", got)
	}
	if got := warn.String(); !strings.Contains(got, path+": ") {
		t.Errorf("warning should name the graph file %q:\n%s", path, got)
	}

	// The Claude half of the same call site: same file, no warning at all.
	var claudeOut, claudeWarn strings.Builder
	if err := lintGraphForRuntime(&claudeOut, &claudeWarn, path, runner.RuntimeClaude); err != nil {
		t.Fatalf("lint under claude: %v", err)
	}
	if strings.Contains(claudeWarn.String(), "budget_usd") {
		t.Errorf("the claude path must warn nothing about budget_usd:\n%s", claudeWarn.String())
	}
}

func TestDryRunForRuntime_SurfacesBudgetWarning(t *testing.T) {
	path := writeGraphFile(t, budgetedCodexGraph)

	var out, warn strings.Builder
	if err := dryRunGraphForRuntime(&out, &warn, path, map[string]string{}, runner.RuntimeCodex); err != nil {
		t.Fatalf("dry run of a budgeted graph under codex: %v", err)
	}
	if got := warn.String(); !strings.Contains(got, budgetWarningMarker) || !strings.Contains(got, "timeout") {
		t.Errorf("--dry-run --runtime codex dropped the preflight warning:\n%s", got)
	}
}

// TestRunGraphWithRuntime_PrintsBudgetWarningExactlyOnce pins the other half of
// the dedup: `run` reaches TWO call sites (its own load and executeGraph's
// gate), and the io.Discard it passes is what keeps the user from reading the
// same cap twice. Counted across BOTH streams, because the fix for a double
// print must not be "move one copy somewhere the test isn't looking".
func TestRunGraphWithRuntime_PrintsBudgetWarningExactlyOnce(t *testing.T) {
	isolateRunHome(t)
	path := writeGraphFile(t, budgetedCodexGraph)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"work": {Result: "PASS", CostUnknown: true},
	})
	stdout, err := os.CreateTemp(t.TempDir(), "stdout")
	if err != nil {
		t.Fatalf("temp stdout: %v", err)
	}
	defer stdout.Close()

	stderr, runErr := captureStderr(t, func() error {
		return runGraphWithRuntime(runner.RuntimeCodex, []string{path}, fake, browser.NewFakeOpener(), stdout)
	})
	if runErr != nil {
		t.Fatalf("run of a budgeted graph under codex: %v", runErr)
	}
	printed, err := os.ReadFile(stdout.Name())
	if err != nil {
		t.Fatalf("read captured stdout: %v", err)
	}

	both := string(printed) + stderr
	if n := strings.Count(both, budgetWarningMarker); n != 1 {
		t.Errorf("`run` printed the preflight warning %d times across stdout+stderr, want exactly 1:\n--- stdout ---\n%s\n--- stderr ---\n%s", n, printed, stderr)
	}
	if !strings.Contains(string(printed), budgetWarningMarker) {
		t.Errorf("the one copy must sit on the disclosure stream it is referenced from:\n%s", printed)
	}
	// The same `<path>: ` prefix the other four sites print: `run` has the
	// graph path in hand, so its warning has no reason to be the anonymous one.
	if !strings.Contains(string(printed), path+": "+budgetWarningMarker) {
		t.Errorf("`run` should name the graph file %q on its warning:\n%s", path, printed)
	}
}
