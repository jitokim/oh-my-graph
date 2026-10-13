package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// compoundShellRuleGraph holds one node the engine hands
// runner.CompoundShellRule (dontAsk + a Bash prefix rule) and two it does not:
// a blanket Bash grant, and the same prefix rule under a mode other than
// dontAsk.
const compoundShellRuleGraph = `
name: shell-rule
nodes:
  - { id: scoped, prompt: scoped, permission_mode: dontAsk, allowed_tools: [Read, "Bash(gh *)"] }
  - { id: blanket, prompt: blanket, permission_mode: dontAsk, allowed_tools: [Read, "Bash(*)"] }
  - { id: classified, prompt: classified, permission_mode: auto, allowed_tools: [Read, "Bash(gh *)"] }
`

// compoundShellRuleRunner scripts a passing outcome for every node of
// compoundShellRuleGraph, keyed by prompt.
func compoundShellRuleRunner() *runner.FakeRunner {
	outcomes := map[string]runner.NodeOutcome{}
	for _, id := range []string{"scoped", "blanket", "classified"} {
		outcomes[id] = runner.NodeOutcome{SessionID: "s-" + id, Result: "PASS"}
	}
	return runner.NewFakeRunner(outcomes)
}

// attemptRules returns, per node id, the compound_shell_rule value of every
// node_started and node_retried in the only run's events.jsonl, in order.
func attemptRules(t *testing.T, home string) map[string][]string {
	t.Helper()
	path := filepath.Join(home, "runs", onlyRunID(t, home), runfeed.FileName)
	got := map[string][]string{}
	err := runfeed.Walk(path, func(e runfeed.Event) error {
		if e.Type == runfeed.EventNodeStarted || e.Type == runfeed.EventNodeRetried {
			got[e.NodeID] = append(got[e.NodeID], e.CompoundShellRule)
		}
		if e.Type != runfeed.EventNodeStarted && e.Type != runfeed.EventNodeRetried && e.CompoundShellRule != "" {
			t.Errorf("%s for %q carries compound_shell_rule %q; only an attempt's opening event may", e.Type, e.NodeID, e.CompoundShellRule)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", path, err)
	}
	return got
}

// assertAttemptRules compares attemptRules against want, node by node.
func assertAttemptRules(t *testing.T, got, want map[string][]string) {
	t.Helper()
	for id, rules := range want {
		if len(got[id]) != len(rules) {
			t.Errorf("node %q: %d opening events, want %d (%q)", id, len(got[id]), len(rules), got[id])
			continue
		}
		for i := range rules {
			if got[id][i] != rules[i] {
				t.Errorf("node %q attempt %d: compound_shell_rule = %q, want %q", id, i, got[id][i], rules[i])
			}
		}
	}
}

// TestRunFeed_RecordsTheCompoundShellRulePerAttempt (#380): the run's record
// says the engine appended the one-command-per-Bash-call rule, and why, on the
// event that opens each attempt that got it — for a hand-written `run` and an
// `auto` plan alike — and nowhere else: not for a blanket Bash grant, not
// under a mode other than dontAsk, and never under codex.
func TestRunFeed_RecordsTheCompoundShellRulePerAttempt(t *testing.T) {
	const appended = "appended: dontAsk + Bash prefix grant (#380)"

	t.Run("run", func(t *testing.T) {
		home := isolateRunHome(t)
		path := writeGraphFile(t, compoundShellRuleGraph)
		var err error
		captureStdout(t, func() {
			err = runGraphWith([]string{path, "--no-web"}, compoundShellRuleRunner(), browser.NewFakeOpener(), os.Stdout)
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		assertAttemptRules(t, attemptRules(t, home), map[string][]string{
			"scoped":     {appended},
			"blanket":    {""},
			"classified": {""},
		})
	})

	t.Run("codex", func(t *testing.T) {
		home := isolateRunHome(t)
		path := writeGraphFile(t, compoundShellRuleGraph)
		fake := compoundShellRuleRunner()
		fake.SetRuntime(runner.RuntimeCodex)
		var err error
		captureStdout(t, func() {
			err = runGraphWithRuntime(runner.RuntimeCodex, []string{path, "--no-web"}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		if err != nil {
			t.Fatalf("codex run: %v", err)
		}
		assertAttemptRules(t, attemptRules(t, home), map[string][]string{
			"scoped":     {""},
			"blanket":    {""},
			"classified": {""},
		})
	})

	t.Run("retry", func(t *testing.T) {
		home := isolateRunHome(t)
		path := writeGraphFile(t, `
name: shell-rule-retry
nodes:
  - { id: flaky, prompt: flaky, permission_mode: dontAsk, allowed_tools: ["Bash(gh *)"], retry: { max: 1, on: [nonzero_exit] } }
`)
		fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
			"flaky": {SessionID: "s-flaky", Result: "boom", ExitCode: 1},
		})
		// The retry's prompt quotes the failed reply after the original text.
		fake.KeyFn = func(spec runner.NodeInvocation) string {
			first, _, _ := strings.Cut(spec.Prompt, "\n")
			return first
		}
		captureStdout(t, func() {
			// The node fails both attempts by design; only the record matters.
			_ = runGraphWith([]string{path, "--no-web"}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		assertAttemptRules(t, attemptRules(t, home), map[string][]string{
			"flaky": {appended, appended},
		})
	})

	t.Run("auto", func(t *testing.T) {
		home := isolateRunHome(t)
		g := mustParse(t, `{"name":"shell-rule-auto","nodes":[
			{"id":"scoped","prompt":"scoped","permission_mode":"dontAsk"},
			{"id":"blanket","prompt":"blanket","permission_mode":"dontAsk"},
			{"id":"classified","prompt":"classified"}]}`)
		plan := coordinator.Plan{Graph: g, ToolPolicies: map[string]runner.ToolPolicy{
			"scoped":     {AllowedTools: []string{"Read", "Bash(gh *)"}},
			"blanket":    {AllowedTools: []string{"Read", "Bash(*)"}},
			"classified": {AllowedTools: []string{"Read", "Bash(gh *)"}},
		}}
		var err error
		captureStdout(t, func() {
			err = executePlan(context.Background(), "shell-rule-auto", plan, compoundShellRuleRunner(), commonRunFlags{inputs: inputFlag{}}, "graph.json", nil, nil, nil)
		})
		if err != nil {
			t.Fatalf("auto run: %v", err)
		}
		assertAttemptRules(t, attemptRules(t, home), map[string][]string{
			"scoped":     {appended},
			"blanket":    {""},
			"classified": {""},
		})
	})
}
