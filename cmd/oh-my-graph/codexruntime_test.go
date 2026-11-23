package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// codexNoCommitMarker is a fragment of the coordinator's Codex paragraph
// (coordinator.WithCodexRuntime), enough to tell whether it reached a prompt.
const codexNoCommitMarker = "no planned node can commit"

// plannerPromptsOf returns every planner call's prompt the fake recorded.
func plannerPromptsOf(fake *runner.FakeRunner) []string {
	var prompts []string
	for _, call := range fake.Invocations() {
		if strings.Contains(call.Prompt, "planning coordinator") {
			prompts = append(prompts, call.Prompt)
		}
	}
	return prompts
}

// #457: `auto` on the Codex runtime hands the coordinator WithCodexRuntime, so
// the planner is told no node can commit; on Claude it does not, so that
// prompt stays what it was. Fakes only — no codex or claude process starts.
func TestRunAutoWithRuntime_OnlyCodexTellsThePlannerNotToCommit(t *testing.T) {
	for _, tc := range []struct {
		runtime runner.Runtime
		want    bool
	}{
		{runner.RuntimeCodex, true},
		{runner.RuntimeClaude, false},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{
				"plan-1": {ExitCode: 0, Result: cycleSpec, CostUnknown: true},
			})
			var runErr error
			captureStdout(t, func() {
				runErr = runAutoWithRuntime(tc.runtime, []string{"add a README section", "--plan-only",
					"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout, osStdin(), greenBaseline(nil))
			})
			if runErr != nil {
				t.Fatalf("--plan-only returned error: %v", runErr)
			}
			prompts := plannerPromptsOf(fake)
			if len(prompts) != 1 {
				t.Fatalf("made %d planner calls, want 1", len(prompts))
			}
			if got := strings.Contains(prompts[0], codexNoCommitMarker); got != tc.want {
				t.Errorf("planner prompt carries the codex no-commit paragraph = %v, want %v:\n%s", got, tc.want, prompts[0])
			}
		})
	}
}

// #457: `chat` plans with the same coordinator, so the same split holds there;
// the operator declines the plan, so no node runs.
func TestRunChatWith_OnlyCodexTellsThePlannerNotToCommit(t *testing.T) {
	for _, tc := range []struct {
		runtime runner.Runtime
		want    bool
	}{
		{runner.RuntimeCodex, true},
		{runner.RuntimeClaude, false},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			isolateRunHome(t)
			fake := newChatFake(map[string]runner.NodeOutcome{
				routerKey:   {Result: `{"mode":"graph","goal":"add a README section"}`},
				autoPlanKey: {Result: cycleSpec, CostUnknown: true},
			})
			var out strings.Builder
			var chatErr error
			captureStdout(t, func() {
				chatErr = runChatWith(tc.runtime, []string{"--no-agent-mapping", "--no-skill-activation"},
					strings.NewReader("add a README section\nn\n"), &out, fake)
			})
			if chatErr != nil {
				t.Fatalf("chat returned %v\n%s", chatErr, out.String())
			}
			prompts := plannerPromptsOf(fake)
			if len(prompts) != 1 {
				t.Fatalf("made %d planner calls, want 1\n%s", len(prompts), out.String())
			}
			if got := strings.Contains(prompts[0], codexNoCommitMarker); got != tc.want {
				t.Errorf("planner prompt carries the codex no-commit paragraph = %v, want %v", got, tc.want)
			}
		})
	}
}

// #457: `design` plans with the same coordinator too, so the same split holds;
// it writes and lints the graph but never runs it.
func TestRunDesign_OnlyCodexTellsThePlannerNotToCommit(t *testing.T) {
	for _, tc := range []struct {
		runtime runner.Runtime
		want    bool
	}{
		{runner.RuntimeCodex, true},
		{runner.RuntimeClaude, false},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			conventionsHome(t)
			fake := newInterviewFake(designOutcomes())
			path := filepath.Join(t.TempDir(), "designed.yaml")
			var out, warn bytes.Buffer
			err := runDesignWithRuntime(tc.runtime, []string{"tidy the docs", "--out", path}, fake,
				&out, &warn, terminalStdin("/done\n", true), lintGraphForRuntime)
			if err != nil {
				t.Fatalf("design returned %v\n%s", err, out.String())
			}
			prompts := plannerPromptsOf(fake)
			if len(prompts) != 1 {
				t.Fatalf("made %d planner calls, want 1\n%s", len(prompts), out.String())
			}
			if got := strings.Contains(prompts[0], codexNoCommitMarker); got != tc.want {
				t.Errorf("planner prompt carries the codex no-commit paragraph = %v, want %v", got, tc.want)
			}
		})
	}
}
