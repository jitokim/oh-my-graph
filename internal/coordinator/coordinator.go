// Package coordinator turns a plain-language goal into a validated Graph by
// asking claude itself to plan the DAG — the engine behind `oh-my-graph auto`.
//
// It makes exactly ONE planner call through the same NodeRunner seam every
// node uses (ClaudeCLIRunner in production: env-scrubbed, subscription-auth,
// never the Agent SDK), asking for a graph spec as a JSON object. JSON is a
// YAML subset, so the reply is loaded through the existing graph parser,
// normalization, and DAG validation — an invalid plan fails before anything
// runs. The coordinator never executes the graph; the caller hands the result
// to the same Scheduler that runs hand-written YAML.
package coordinator

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// plannerPermissionMode is the permission mode of the planner call. The
// planner only writes a JSON reply, so it runs read-only.
const plannerPermissionMode = "plan"

// maxOutputInError caps how much of the raw planner reply an error message
// carries — enough to diagnose a bad plan without flooding the terminal.
const maxOutputInError = 500

// plannedToolAllowlist is the fixed, coordinator-owned safety allowlist for
// tools a coordinator-planned node may request. A planned graph comes from
// untrusted LLM output and runs unattended under permission_mode dontAsk (see
// schedule.defaultPermissionMode) — nothing prompts a human before a tool
// call fires. The planner prompt (below) asks the model to pick least-
// privilege tools from exactly this list, but that is only a request to an
// untrusted producer; validatePlannedNodes is what actually enforces it by
// rejecting any planned node naming a tool outside this set. This is
// explicit-allow/deny-everything-else: it does not try to parse or sanitize
// a Bash command string, it only accepts the exact patterns listed here, so
// "Bash", "Bash(*)", "Bash(rm -rf *)", "Bash(curl * | sh)", unrestricted
// WebFetch/WebSearch, and anything else not spelled out below simply never
// matches.
//
// Hand-written YAML graphs (the `run` path, internal/graph.Load) are
// human-authored and reviewed before they run, so this allowlist does NOT
// apply to them — only to graphs coordinator.Plan produced.
var plannedToolAllowlist = []string{
	"Read", "Glob", "Grep", "Edit", "Write",
	"Bash(git *)", "Bash(go *)", "Bash(make *)", "Bash(ls *)", "Bash(cat *)", "Bash(grep *)", "Bash(gh pr *)",
}

// plannedToolAllowlistSet is plannedToolAllowlist as a lookup set, built once
// at init so validatePlannedNodes doesn't rebuild it per node.
var plannedToolAllowlistSet = toSet(plannedToolAllowlist)

func toSet(items []string) map[string]bool {
	set := make(map[string]bool, len(items))
	for _, item := range items {
		set[item] = true
	}
	return set
}

// PlanError marks a planning failure that is the planner's fault (as opposed
// to a runner error or an invalid generated graph): an empty goal, a non-zero
// planner exit, a reply with no JSON object in it, or a plan with a node auto
// mode refuses to run. Output carries the raw planner reply and is included
// (truncated) in the error message so the user can see what went wrong.
type PlanError struct {
	Reason string
	Output string
}

func (e *PlanError) Error() string {
	if e.Output == "" {
		return fmt.Sprintf("graph planning failed: %s", e.Reason)
	}
	return fmt.Sprintf("graph planning failed: %s\nplanner replied:\n%s", e.Reason, truncate(e.Output, maxOutputInError))
}

// truncate shortens s to at most n bytes, marking the cut.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "… (truncated)"
}

// Plan is the coordinator's product: the validated graph, the raw JSON spec it
// was parsed from (so the caller can persist and re-run it), and what the
// planner call cost — the caller reports it so an auto run's total spend is
// honest about including the planning step.
type Plan struct {
	Graph   *graph.Graph
	Spec    []byte
	CostUSD float64
}

// Coordinator plans graphs. Construct it with New (constructor injection — no
// globals); production injects ClaudeCLIRunner, tests inject FakeRunner.
type Coordinator struct {
	runner runner.NodeRunner
}

// New builds a Coordinator bound to a NodeRunner.
func New(nodeRunner runner.NodeRunner) *Coordinator {
	return &Coordinator{runner: nodeRunner}
}

// Plan asks the planner to design a graph for goal, then loads its JSON reply
// through graph.Parse so every invariant a hand-written graph must satisfy
// (unique ids, known enums, acyclic depends_on, session-handoff arity) also
// holds for a generated one. inputKeys are the --input names the planned
// prompts may reference as {{ inputs.<name> }}.
func (c *Coordinator) Plan(ctx context.Context, goal string, inputKeys []string) (Plan, error) {
	if strings.TrimSpace(goal) == "" {
		return Plan{}, &PlanError{Reason: "goal is empty"}
	}

	outcome, err := c.runner.Run(ctx, runner.NodeInvocation{
		Prompt:         plannerPrompt(goal, inputKeys),
		PermissionMode: plannerPermissionMode,
	})
	if err != nil {
		return Plan{}, fmt.Errorf("planner run: %w", err)
	}
	if outcome.ExitCode != 0 {
		return Plan{}, &PlanError{
			Reason: fmt.Sprintf("planner exited with code %d", outcome.ExitCode),
			Output: outcome.Result,
		}
	}

	spec := extractJSON(outcome.Result)
	if spec == "" {
		return Plan{}, &PlanError{
			Reason: "planner reply contained no JSON object",
			Output: outcome.Result,
		}
	}

	g, err := graph.Parse([]byte(spec))
	if err != nil {
		return Plan{}, fmt.Errorf("generated graph is invalid: %w", err)
	}
	if err := validatePlannedNodes(g, outcome.Result); err != nil {
		return Plan{}, err
	}
	return Plan{Graph: g, Spec: []byte(spec), CostUSD: outcome.TotalCostUSD}, nil
}

// validatePlannedNodes enforces what auto mode refuses beyond the structural
// validation every graph gets. A hand-written graph is seen and owned by the
// user, so it may opt into more; an auto-planned graph was never reviewed:
//
//   - no planned graph may be empty — a nodeless plan would "succeed" while
//     doing nothing, after paying for the planner call;
//   - no planned node may run without a prompt;
//   - no planned node may be a gate (v0.1 rejects gates at execution time, so
//     a planned one only halts the run after the planning spend);
//   - no planned node may relax the sandbox with bypassPermissions;
//   - every planned node must declare a non-empty allowed_tools, and every
//     tool it names must be in plannedToolAllowlist. Omitting allowed_tools
//     is rejected too: the runner only appends --allowedTools when the list
//     is non-empty (internal/runner.ClaudeCLIRunner.buildArgs), so an empty
//     list would run under the CLI's own default tool set instead of this
//     allowlist — that gap would make the allowlist opt-in for an attacker
//     simply by leaving the field off.
func validatePlannedNodes(g *graph.Graph, reply string) error {
	if len(g.Nodes) == 0 {
		return &PlanError{Reason: "planner produced a graph with no nodes", Output: reply}
	}
	for _, node := range g.Nodes {
		if strings.TrimSpace(node.Prompt) == "" {
			return &PlanError{Reason: fmt.Sprintf("planned node %q has an empty prompt", node.ID)}
		}
		if node.Type == graph.TypeGate {
			return &PlanError{Reason: fmt.Sprintf("planned node %q is a gate node, which auto mode cannot run", node.ID)}
		}
		if node.PermissionMode == graph.PermissionBypass {
			return &PlanError{
				Reason: fmt.Sprintf("planned node %q requested permission_mode %s, which auto mode never grants", node.ID, graph.PermissionBypass),
			}
		}
		if err := validatePlannedNodeTools(node); err != nil {
			return err
		}
	}
	return nil
}

// validatePlannedNodeTools rejects a planned node whose allowed_tools is
// empty or names anything outside plannedToolAllowlist. See
// validatePlannedNodes for why an empty list is rejected rather than passed
// through.
func validatePlannedNodeTools(node graph.Node) error {
	if len(node.AllowedTools) == 0 {
		return &PlanError{
			Reason: fmt.Sprintf("planned node %q has no allowed_tools; auto mode requires an explicit least-privilege tool list", node.ID),
		}
	}
	for _, tool := range node.AllowedTools {
		if !plannedToolAllowlistSet[tool] {
			return &PlanError{
				Reason: fmt.Sprintf("planned node %q requested tool %q, which is outside auto mode's tool allowlist (%s)", node.ID, tool, strings.Join(plannedToolAllowlist, ", ")),
			}
		}
	}
	return nil
}

// extractJSON isolates the JSON object from the planner's reply, tolerating a
// markdown code fence or stray prose around it: everything outside the first
// '{' and the last '}' is discarded. Returns "" when no object is present.
func extractJSON(result string) string {
	start := strings.Index(result, "{")
	end := strings.LastIndex(result, "}")
	if start == -1 || end < start {
		return ""
	}
	return result[start : end+1]
}

// plannerPrompt renders the coordinator instruction for one goal. Input keys
// are sorted so the prompt is deterministic for a given goal + inputs. The
// tool list rendered into the prompt is plannedToolAllowlist itself — telling
// the planner the exact set validatePlannedNodes enforces so a well-behaved
// plan validates on the first try. Enforcement does not depend on the
// planner reading or following this text.
func plannerPrompt(goal string, inputKeys []string) string {
	keys := "none"
	if len(inputKeys) > 0 {
		sorted := append([]string(nil), inputKeys...)
		sort.Strings(sorted)
		keys = strings.Join(sorted, ", ")
	}
	return fmt.Sprintf(plannerPromptTemplate, goal, keys, strings.Join(plannedToolAllowlist, ", "))
}

const plannerPromptTemplate = `You are the planning coordinator for oh-my-graph, an orchestrator that runs
each node of a DAG as its own claude subprocess.

Design the smallest graph of nodes that accomplishes this goal:

%s

Available inputs: %s. A node's prompt may reference an available input as
{{ inputs.<name> }}; never reference an input that is not listed.

Reply with ONLY a JSON object in exactly this shape — no markdown fence, no
prose before or after:

{
  "name": "<short-kebab-case-graph-name>",
  "version": "1",
  "nodes": [
    {
      "id": "<short-node-id>",
      "depends_on": ["<parent-id>"],
      "prompt": "<complete, self-contained instruction for this node>",
      "allowed_tools": ["Read", "Bash(make *)"],
      "handoff": "artifact"
    }
  ]
}

Rules:
- Use 1 to 6 nodes. depends_on must be acyclic; a node with no dependencies
  omits depends_on.
- A node reads a parent's result by writing {{ artifacts.<parent-id> }} in its
  prompt (it resolves to a file path; append " | inline" inside the braces to
  inline the content instead).
- "handoff" is "artifact" unless a node should continue its single parent's
  claude session, then "session". A "session" node must have exactly one
  parent.
- Every node MUST set allowed_tools to a non-empty list drawn ONLY from this
  exact set: %s
  Pick just the tools that node needs (least privilege). Any tool outside
  this set, an empty list, or a bare "Bash" / "Bash(*)" will be rejected —
  there is no other Bash pattern available, so a node needing a different
  shell command cannot be planned; break it into steps that fit the list
  above instead.
- Do not set permission_mode, budget_usd, or type on any node.
`
