package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// A Codex graph carrying `agent:` is refused, because running the node without
// the named subagent's system prompt runs a different node than the one
// declared. `budget_usd` on the SAME graph is not part of that refusal (ADR
// 0026): the message must name the agent node and must not claim the cap is
// unsupported.
func TestValidateGraphForRuntimeRejectsAgentOnly(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{
		{ID: "budgeted", BudgetUSD: 0.25},
		{ID: "delegated", Agent: "reviewer"},
	}}

	warnings, err := ValidateGraphForRuntime(RuntimeCodex, g)
	if err == nil {
		t.Fatal("ValidateGraphForRuntime(codex) accepted an agent: node")
	}
	for _, want := range []string{"delegated", "agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, missing %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "budget_usd") {
		t.Errorf("error = %q, still refuses budget_usd", err)
	}
	// The two verdicts are independent: a graph refused for one node's `agent:`
	// still carries the other node's inapplicable cap, and dropping the warning
	// because the error is non-nil would hide it from the reader who fixes the
	// agent node next.
	if len(warnings) != 1 || !strings.Contains(warnings[0], "budgeted") {
		t.Errorf("warnings = %q, want one naming the budgeted node", warnings)
	}
}

// budget_usd alone LOADS under Codex, and the warning says both halves of why:
// the cap cannot apply, and which guard is still in force. A node with its own
// `timeout:` is named by that timeout; a node without one is named by the
// runner's default, so nobody reads the acceptance as "nothing bounds this".
func TestValidateGraphForRuntimeAcceptsBudgetAndNamesTheSurvivingGuard(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{
		{ID: "explicit", BudgetUSD: 10, Timeout: "45m"},
		{ID: "defaulted", BudgetUSD: 0.5},
	}}

	warnings, err := ValidateGraphForRuntime(RuntimeCodex, g)
	if err != nil {
		t.Fatalf("budget_usd refused under codex: %v", err)
	}
	if len(warnings) != 2 {
		t.Fatalf("warnings = %q, want one per budgeted node", warnings)
	}
	for _, want := range []string{"explicit", "budget_usd", "cannot apply", "explicit timeout: 45m"} {
		if !strings.Contains(warnings[0], want) {
			t.Errorf("warning = %q, missing %q", warnings[0], want)
		}
	}
	for _, want := range []string{"defaulted", "default timeout: " + defaultTimeout.String()} {
		if !strings.Contains(warnings[1], want) {
			t.Errorf("warning = %q, missing %q", warnings[1], want)
		}
	}
}

// The Claude path returns early: it neither refuses nor warns about anything,
// whatever the graph declares. A shared Codex graph is likewise untouched.
func TestValidateGraphForRuntimeAllowsSharedFieldsAndClaude(t *testing.T) {
	shared := &graph.Graph{Nodes: []graph.Node{{
		ID:             "work",
		Prompt:         "work",
		Handoff:        graph.HandoffSession,
		PermissionMode: "plan",
	}}}
	warnings, err := ValidateGraphForRuntime(RuntimeCodex, shared)
	if err != nil {
		t.Fatalf("shared Codex graph rejected: %v", err)
	}
	if len(warnings) != 0 {
		t.Errorf("warnings = %q, want none for a graph declaring no budget", warnings)
	}
	claudeOnly := &graph.Graph{Nodes: []graph.Node{{ID: "work", BudgetUSD: 1, Agent: "reviewer"}}}
	warnings, err = ValidateGraphForRuntime(RuntimeClaude, claudeOnly)
	if err != nil {
		t.Fatalf("Claude graph rejected: %v", err)
	}
	if warnings != nil {
		t.Errorf("warnings = %q, want nil — the Claude path says nothing new", warnings)
	}
}

// The availability check is a PATH lookup and reports the runtime, the command
// name and what to do next. It must NOT claim anything about being signed in:
// that state is invisible without spawning the CLI, and a message that blurred
// the two would send a logged-out user to reinstall a binary they already have.
func TestCheckCLIAvailableReportsAMissingBinaryNarrowly(t *testing.T) {
	restore := lookPath
	lookPath = func(name string) (string, error) {
		return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
	}
	defer func() { lookPath = restore }()

	err := NewCLIRunner(RuntimeClaude).CheckCLIAvailable()
	var notFound *CLINotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("CheckCLIAvailable() = %v, want *CLINotFoundError", err)
	}
	if notFound.Runtime != RuntimeClaude || notFound.Binary != "claude" {
		t.Errorf("error = %+v, want the claude runtime and its binary", notFound)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("error does not unwrap to the lookup failure: %v", err)
	}
	text := err.Error()
	for _, want := range []string{
		"claude runtime",
		`"claude" is not on PATH`,
		"docs/INSTALL.md",
		"--runtime codex",
		"NOT checked: whether it is signed in",
		"no run directory was created",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("message = %q, missing %q", text, want)
		}
	}
	// The suggestion must never offer the CLI that was just found missing.
	codex := NewCLIRunner(RuntimeCodex).CheckCLIAvailable()
	if !strings.Contains(codex.Error(), "--runtime claude") {
		t.Errorf("codex message = %q, want it to suggest the claude runtime", codex)
	}
	if strings.Contains(codex.Error(), "--runtime codex") {
		t.Errorf("codex message = %q, suggests the runtime that is missing", codex)
	}
}

// A CLI that resolves is silence: the check has no verdict to add, and a run
// proceeds exactly as it did before this check existed.
func TestCheckCLIAvailablePassesWhenTheBinaryResolves(t *testing.T) {
	restore := lookPath
	lookPath = func(name string) (string, error) { return "/usr/local/bin/" + name, nil }
	defer func() { lookPath = restore }()

	if err := NewCLIRunner(RuntimeCodex).CheckCLIAvailable(); err != nil {
		t.Fatalf("CheckCLIAvailable() = %v, want nil for a resolvable binary", err)
	}
}

// CheckRunnerCLI is what commands call, so it must answer for any NodeRunner:
// the scripted answer for a runner that has one, and nil for a runner that
// cannot know — a scripted fake has no PATH, and forcing it to guess would fail
// runs on machines where the fake is the whole point.
func TestCheckRunnerCLIAsksOnlyRunnersThatCanAnswer(t *testing.T) {
	fake := NewFakeRunner(nil)
	if err := CheckRunnerCLI(fake); err != nil {
		t.Fatalf("CheckRunnerCLI(fake) = %v, want nil by default", err)
	}
	injected := &CLINotFoundError{Runtime: RuntimeClaude, Binary: "claude"}
	fake.InjectCLIUnavailable(injected)
	if err := CheckRunnerCLI(fake); !errors.Is(err, error(injected)) {
		t.Errorf("CheckRunnerCLI(fake) = %v, want the scripted error", err)
	}
	if err := CheckRunnerCLI(silentRunner{}); err != nil {
		t.Errorf("CheckRunnerCLI(runner without the interface) = %v, want nil", err)
	}
}

// silentRunner is a NodeRunner that does not implement CLIAvailabilityChecker.
type silentRunner struct{}

func (silentRunner) Run(context.Context, NodeInvocation) (NodeOutcome, error) {
	return NodeOutcome{}, nil
}
