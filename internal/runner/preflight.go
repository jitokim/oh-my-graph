package runner

import (
	"fmt"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// ValidateGraphForRuntime judges a graph against the selected CLI before any
// node starts. Claude supports the full graph schema and returns immediately.
//
// For Codex it returns TWO things, because the declarations Claude owns are two
// different kinds of thing and one verdict for both was a defect (ADR 0026):
//
//   - `agent:` names a Claude Code subagent. Running the node without it means
//     running it without that agent's system prompt — a materially different
//     node than the graph declares. That is REFUSED, in the error.
//   - `budget_usd` is a USD ceiling, and Codex reports no USD at all
//     (codexProtocol starts every outcome at CostUnknown). There is no quantity
//     to bound, so the cap is INAPPLICABLE, not unsafe: the graph LOADS, and a
//     warning says the cap cannot apply — naming the guard that does remain in
//     force, this node's own `timeout:` or the runner's default when it
//     declares none. Nothing runs unguarded, because a wall-clock bound always
//     exists; the budget was never the hang guard (graphs/fragments/
//     e2e-verify.yaml says so beside its own `budget_usd: 10.00`).
//
// The goal-level `auto --max-goal-budget-usd` stays refused at the CLI boundary
// and that is deliberately NOT symmetric with the node-level cap: a per-node
// budget is one node's insurance and simply has no quantity to bound, while a
// goal ceiling is the only thing bounding an ITERATING loop, so under Codex the
// loop would stop at its first boundary with StopBudgetUnmeasurable having
// bought a whole cycle to learn what preflight can say for free.
//
// Warnings are returned rather than printed because this function must stay
// side-effect free — every caller surfaces them, and none may drop them.
func ValidateGraphForRuntime(runtime Runtime, g *graph.Graph) ([]string, error) {
	if runtime == RuntimeClaude {
		return nil, nil
	}
	var warnings, unsupported []string
	for _, node := range g.Nodes {
		if node.BudgetUSD > 0 {
			warnings = append(warnings, fmt.Sprintf(
				"node %q: budget_usd %.2f cannot apply under the codex runtime — it reports no USD, so no cost is ever compared against the cap; this node's runaway guard is %s",
				node.ID, node.BudgetUSD, nodeTimeoutGuard(node)))
		}
		if node.Agent != "" {
			unsupported = append(unsupported, fmt.Sprintf("node %q: agent is supported only by the claude runtime", node.ID))
		}
	}
	if len(unsupported) > 0 {
		return warnings, fmt.Errorf("codex runtime cannot execute this graph:\n%s", strings.Join(unsupported, "\n"))
	}
	return warnings, nil
}

// nodeTimeoutGuard names the wall-clock bound that survives a budget_usd the
// runtime cannot evaluate, and says WHICH one it is: the node's declared
// `timeout:` when it has one, otherwise the runner's own default, quoted from
// the constant the CLIRunner actually applies (cli.go) so the message cannot
// drift from the bound.
func nodeTimeoutGuard(node graph.Node) string {
	if node.Timeout != "" {
		return fmt.Sprintf("its explicit timeout: %s", node.Timeout)
	}
	return fmt.Sprintf("the runner's default timeout: %s", defaultTimeout)
}
