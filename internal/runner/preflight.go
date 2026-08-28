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
// and that is deliberately NOT symmetric with the node-level cap. The asymmetry
// is about WHEN each declaration discovers it cannot be evaluated, not about
// what is left unbounded: an iterating loop is hard-bounded by --max-cycles
// either way, a flag with no unbounded spelling (ADR 0011 §1, enforced at
// parse), and the ceiling is only its SPEND-shaped bound. What differs is the
// price of accepting it. An inapplicable node cap costs nothing extra — it is
// simply never compared. The goal ceiling is checked at a CYCLE BOUNDARY only
// (coordinator/goal.go, the `cycle > 1` block), so accepting an unmeasurable
// one would buy a whole cycle before StopBudgetUnmeasurable stops the loop to
// say what preflight can say for free, before anything spends.
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

// CLIAvailabilityChecker is the NodeRunner-side plumbing for the one question a
// caller can ask about a run before it starts: is the CLI this runner would
// spawn even installed? It is an optional interface rather than a NodeRunner
// method because a scripted runner has no PATH to consult and must not be forced
// to invent an answer.
type CLIAvailabilityChecker interface {
	// CheckCLIAvailable returns a *CLINotFoundError when the provider CLI cannot
	// be found on PATH, and nil otherwise — including when the runner has no way
	// to tell.
	CheckCLIAvailable() error
}

// CheckRunnerCLI is what a command calls before it commits to a run: it asks the
// runner it is about to hand nodes to whether that runner's CLI exists, and
// returns nil for any runner that cannot answer. Callers therefore need no type
// switch of their own, and none of them import os/exec to ask.
func CheckRunnerCLI(r NodeRunner) error {
	checker, ok := r.(CLIAvailabilityChecker)
	if !ok {
		return nil
	}
	return checker.CheckCLIAvailable()
}

// CLINotFoundError is the missing-CLI refusal. Without it the same machine state
// surfaced far downstream: the run directory was already on disk, the planner
// call had already been retried for seconds, and what finally reached the
// operator was Go's exec.Error wording about a file in $PATH — a sentence that
// names neither oh-my-graph's runtime selection nor what to install.
//
// Its text is deliberately NARROW. It reports a PATH lookup, so it claims a PATH
// lookup: "not installed" and "installed but signed out" are different states,
// and only the first one is visible without spawning the CLI. Widening this
// message to speak for the second would be a guess printed in the voice of a
// check.
type CLINotFoundError struct {
	// Runtime is the run-wide runtime whose CLI was looked for.
	Runtime Runtime
	// Binary is the command name that was not found.
	Binary string
	// Err is the underlying lookup failure (an *exec.Error), kept so a caller
	// can still reach the original wording.
	Err error
}

func (e *CLINotFoundError) Error() string {
	return fmt.Sprintf("%s runtime: %q is not on PATH, so nothing in this run can start\n"+
		"  install it and complete its login — docs/INSTALL.md, \"Runtime prerequisite\"\n"+
		"  or select the CLI you do have, before the subcommand: oh-my-graph --runtime %s ...\n"+
		"  checked: that the command exists. NOT checked: whether it is signed in — that\n"+
		"  cannot be known without running it, and a signed-out CLI fails later as an\n"+
		"  ordinary non-zero exit. Nothing has been spent; no run directory was created.",
		e.Runtime, e.Binary, otherRuntime(e.Runtime))
}

func (e *CLINotFoundError) Unwrap() error { return e.Err }

// otherRuntime names the runtime a reader could switch to, so the suggestion in
// CLINotFoundError never offers the very CLI that was just found missing.
func otherRuntime(r Runtime) Runtime {
	if r == RuntimeCodex {
		return RuntimeClaude
	}
	return RuntimeCodex
}
