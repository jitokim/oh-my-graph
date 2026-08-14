package runner

import (
	"fmt"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// ValidateGraphForRuntime rejects declarations the selected CLI cannot honor
// before any node starts. Claude supports the full graph schema; Codex does
// not expose Claude's per-invocation USD cap or subagent selector.
func ValidateGraphForRuntime(runtime Runtime, g *graph.Graph) error {
	if runtime == RuntimeClaude {
		return nil
	}
	var unsupported []string
	for _, node := range g.Nodes {
		if node.BudgetUSD > 0 {
			unsupported = append(unsupported, fmt.Sprintf("node %q: budget_usd is supported only by the claude runtime", node.ID))
		}
		if node.Agent != "" {
			unsupported = append(unsupported, fmt.Sprintf("node %q: agent is supported only by the claude runtime", node.ID))
		}
	}
	if len(unsupported) > 0 {
		return fmt.Errorf("codex runtime cannot execute this graph:\n%s", strings.Join(unsupported, "\n"))
	}
	return nil
}
