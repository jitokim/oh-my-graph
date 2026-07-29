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
//   - no planned node may relax the sandbox with bypassPermissions.
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
// are sorted so the prompt is deterministic for a given goal + inputs.
func plannerPrompt(goal string, inputKeys []string) string {
	keys := "none"
	if len(inputKeys) > 0 {
		sorted := append([]string(nil), inputKeys...)
		sort.Strings(sorted)
		keys = strings.Join(sorted, ", ")
	}
	return fmt.Sprintf(plannerPromptTemplate, goal, keys)
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
- allowed_tools lists only the tools that node needs (for example Read, Write,
  Edit, "Bash(go *)"). Keep each node least-privilege.
- Do not set permission_mode, budget_usd, or type on any node.
`
