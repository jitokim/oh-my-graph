package runner

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

func TestValidateGraphForRuntimeRejectsClaudeOnlyCodexFields(t *testing.T) {
	g := &graph.Graph{Nodes: []graph.Node{
		{ID: "budgeted", BudgetUSD: 0.25},
		{ID: "delegated", Agent: "reviewer"},
	}}

	err := ValidateGraphForRuntime(RuntimeCodex, g)
	if err == nil {
		t.Fatal("ValidateGraphForRuntime(codex) succeeded")
	}
	for _, want := range []string{"budgeted", "budget_usd", "delegated", "agent"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, missing %q", err, want)
		}
	}
}

func TestValidateGraphForRuntimeAllowsSharedFieldsAndClaude(t *testing.T) {
	shared := &graph.Graph{Nodes: []graph.Node{{
		ID:             "work",
		Prompt:         "work",
		Handoff:        graph.HandoffSession,
		PermissionMode: "plan",
	}}}
	if err := ValidateGraphForRuntime(RuntimeCodex, shared); err != nil {
		t.Fatalf("shared Codex graph rejected: %v", err)
	}
	claudeOnly := &graph.Graph{Nodes: []graph.Node{{ID: "work", BudgetUSD: 1, Agent: "reviewer"}}}
	if err := ValidateGraphForRuntime(RuntimeClaude, claudeOnly); err != nil {
		t.Fatalf("Claude graph rejected: %v", err)
	}
}
