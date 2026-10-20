package runner

import (
	"context"
	"testing"
)

// TestCostIncludesResumedSession_OnlyAResumedClaudeInvocation pins which
// reported totals the scheduler may treat as cumulative (#398): claude's, and
// only when the invocation resumed a session. Codex reports no USD total, so it
// never answers yes.
func TestCostIncludesResumedSession_OnlyAResumedClaudeInvocation(t *testing.T) {
	cases := []struct {
		runtime Runtime
		resume  string
		want    bool
	}{
		{RuntimeClaude, "s-parent", true},
		{RuntimeClaude, "", false},
		{RuntimeCodex, "s-parent", false},
		{RuntimeCodex, "", false},
	}
	for _, tc := range cases {
		got := protocolFor(tc.runtime).costIncludesResumedSession(NodeInvocation{ResumeSession: tc.resume})
		if got != tc.want {
			t.Errorf("%s with ResumeSession %q: costIncludesResumedSession = %v, want %v", tc.runtime, tc.resume, got, tc.want)
		}
	}
}

// TestFakeRunner_ReportsCumulativeCostLikeItsRuntime proves the fake answers
// CostIncludesResumedSession through the real protocol, as CLIRunner.Run does,
// so an engine test sees the flag exactly when production would set it.
func TestFakeRunner_ReportsCumulativeCostLikeItsRuntime(t *testing.T) {
	for _, tc := range []struct {
		runtime Runtime
		want    bool
	}{{"", true}, {RuntimeClaude, true}, {RuntimeCodex, false}} {
		f := NewFakeRunner(map[string]NodeOutcome{"child": {TotalCostUSD: 0.5}})
		f.SetRuntime(tc.runtime)
		resumed, err := f.Run(context.Background(), NodeInvocation{Prompt: "child", ResumeSession: "s-parent"})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if resumed.CostIncludesResumedSession != tc.want {
			t.Errorf("runtime %q resumed: CostIncludesResumedSession = %v, want %v", tc.runtime, resumed.CostIncludesResumedSession, tc.want)
		}
		cold, err := f.Run(context.Background(), NodeInvocation{Prompt: "child"})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if cold.CostIncludesResumedSession {
			t.Errorf("runtime %q cold start: CostIncludesResumedSession = true, want false", tc.runtime)
		}
	}
}
