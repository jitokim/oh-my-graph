// Package coordinator turns a plain-language goal into a validated Graph by
// asking claude itself to plan the DAG — the engine behind `oh-my-graph auto`.
// It also classifies chat turns (Route, router.go) for the `chat` prototype.
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

// plannerPermissionMode is the permission mode of every coordinator-owned
// call (the planner, the chat router). They only write a JSON reply, so they
// run read-only.
const plannerPermissionMode = "plan"

// maxOutputInError caps how much of a coordinator call's raw reply an error
// message carries — enough to diagnose a bad reply without flooding the
// terminal.
const maxOutputInError = 500

// plannedToolAllowlist is the fixed, coordinator-owned safety allowlist for
// tools a coordinator-planned node may DECLARE. A planned graph comes from
// untrusted LLM output and runs unattended under permission_mode dontAsk (see
// schedule.defaultPermissionMode) — nothing prompts a human before a tool call
// fires. The planner prompt (below) asks the model to pick least-privilege
// tools from exactly this list, but that is only a request to an untrusted
// producer; validatePlannedNodeTools is what enforces it, rejecting any planned
// node naming a tool outside this set. So "Bash", "Bash(*)", "Bash(rm -rf *)",
// "Bash(curl * | sh)", unrestricted WebFetch/WebSearch and anything else not
// spelled out below never survive planning.
//
// This is a DECLARATION bound — layer 0 of the ceiling. What a declaration is
// worth at run time is decided by toolPolicyFor below, which turns it into a
// layered runner.ToolPolicy.
//
// Hand-written YAML graphs (the `run` path, internal/graph.Load) are
// human-authored and reviewed before they run, so neither this allowlist nor
// any ceiling layer applies to them — only to graphs coordinator.Plan produced.
var plannedToolAllowlist = []string{
	"Read", "Glob", "Grep", "Edit", "Write",
	"Bash(git *)", "Bash(go *)", "Bash(make *)", "Bash(ls *)", "Bash(cat *)", "Bash(grep *)", "Bash(gh pr *)",
}

// deniableTools is layer 5 of the ceiling — the residual deny list: the
// consequential tool NAMES a planned node is denied unless it declared them.
// It was the whole ceiling before layer 1 existed and is deliberately RETAINED
// now that it isn't, because the layers are independent mechanisms: if an
// assumption about settings-source isolation, tool narrowing or strict MCP
// turns out to be wrong on some future CLI build, the ceiling degrades to this
// list rather than to nothing.
//
// The entries are bare tool NAMES, not scoped patterns, because that is the
// granularity a deny actually enforces. Measured against a real CLI (claude
// 2.1.220, `claude -p`, permission-mode dontAsk, settings.json granting
// `Bash(*)`):
//
//   - a bare-name deny ("Bash") removes the tool outright — it beats both the
//     user's standing settings grant and this process's own --allowedTools;
//   - a wildcard deny ("Bash(*)") is a NO-OP. The specifier is matched as a
//     command pattern, so it only matches a command literally beginning with
//     "*". Denying `Bash(*)` closes nothing, which is exactly why it is absent
//     here despite looking like the obvious thing to write;
//   - denying a name the CLI does not have (verified with a junk name) is
//     accepted and the run still succeeds — so listing both Task and Agent,
//     the two spellings the subagent-spawning tool has had across versions,
//     costs nothing.
//
// Read/Glob/Grep are deliberately NOT deniable: they cannot mutate state or
// reach the network on their own once shell, write and fetch are gone, and
// denying them would make plans brittle for no safety gain.
//
// On its own this list is an ENUMERATION over an open set, and its one
// structural hole is that a node declaring any scoped Bash pattern keeps the
// ENTIRE Bash tool — a deny cannot express "all Bash except these prefixes".
// That hole is what layer 1 closes; see toolPolicyFor.
var deniableTools = []string{
	"Bash", "Edit", "Write", "MultiEdit", "NotebookEdit", "WebFetch", "WebSearch", "Task", "Agent",
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
// was parsed from (so the caller can persist and re-run it), what the planner
// call cost — the caller reports it so an auto run's total spend is honest
// about including the planning step — and the execution ceiling every planned
// node must run under.
//
// ToolPolicies travels WITH the plan rather than being left for the caller to
// remember, because it is not optional decoration: it is the part of the tool
// guard that binds at runtime. A caller that executes Plan.Graph must hand
// ToolPolicies to the Scheduler, or the graph runs with the user's own
// (possibly wide-open) standing grants.
type Plan struct {
	Graph   *graph.Graph
	Spec    []byte
	CostUSD float64
	// ToolPolicies maps each planned node's id to the COMPLETE tool policy its
	// subprocess must run under — including that node's own --allowedTools, so
	// the scheduler never has to merge two half-policies. Never nil for a
	// successful plan, and it has an entry for every node in Graph.
	ToolPolicies map[string]runner.ToolPolicy
}

// Coordinator plans graphs (Plan) and classifies chat turns (Route). Construct
// it with New (constructor injection — no globals); production injects
// ClaudeCLIRunner, tests inject FakeRunner.
type Coordinator struct {
	runner runner.NodeRunner
}

// New builds a Coordinator bound to a NodeRunner.
func New(nodeRunner runner.NodeRunner) *Coordinator {
	return &Coordinator{runner: nodeRunner}
}

// coordinatorInvocation is the shared stance of every coordinator-owned call
// (the planner, the chat router): read-only permission mode, no tools granted,
// and the full deny list of a node that declared nothing. A helper rather than
// a convention so a future coordinator call cannot forget the deny list and
// silently inherit the user's standing grants.
func coordinatorInvocation(prompt string) runner.NodeInvocation {
	return runner.NodeInvocation{
		Prompt:         prompt,
		PermissionMode: plannerPermissionMode,
		Policy:         runner.ToolPolicy{DisallowedTools: disallowedToolsFor(graph.Node{})},
	}
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

	// The planner decides the whole graph, so it must not be the least
	// constrained call in an auto run. It only emits a JSON object, declares no
	// tools, and runs read-only — but "read-only permission mode" is not a tool
	// ceiling, and without a deny list it would inherit the user's full standing
	// grants. A node declaring nothing denies everything deniable.
	//
	// It deliberately gets the deny list ONLY, not toolPolicyFor's full ceiling.
	// The layered policy is scoped to planned NODES — the untrusted artifact the
	// planner produces — while the planner call itself is oh-my-graph's own
	// prompt, and isolating it would also drop the user's CLAUDE.md from the one
	// call whose job is to understand this repository. Widening the ceiling here
	// is a product decision about plan quality, not a safety fix, so it is not
	// made silently as part of one.
	outcome, err := c.runner.Run(ctx, coordinatorInvocation(plannerPrompt(goal, inputKeys)))
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
	return Plan{
		Graph:        g,
		Spec:         []byte(spec),
		CostUSD:      outcome.TotalCostUSD,
		ToolPolicies: toolPoliciesByNode(g),
	}, nil
}

// toolPoliciesByNode derives the run's execution ceiling: one complete policy
// per planned node. It runs after validation, so every node here already
// declared a non-empty allowed_tools drawn from plannedToolAllowlist.
func toolPoliciesByNode(g *graph.Graph) map[string]runner.ToolPolicy {
	byNode := make(map[string]runner.ToolPolicy, len(g.Nodes))
	for _, node := range g.Nodes {
		byNode[node.ID] = toolPolicyFor(node)
	}
	return byNode
}

// toolPolicyFor is one planned node's execution ceiling — the layered policy
// DESIGN.md specifies, assembled in one place so no layer can be forgotten at
// a call site:
//
//	1 isolation  --setting-sources ""   the user's standing grants, and hooks
//	2 grant      --allowedTools         what this node declared, now binding
//	3 narrowing  --tools <bare names>   what the model can even attempt
//	4 MCP        --strict-mcp-config    mcp__<server>__<tool>
//	5 residual   --disallowedTools      anything the layers above got wrong
//
// Layer 1 is the load-bearing one. Permission rules are matched from every
// loaded source, so a standing `Bash(*)` in the user's ~/.claude/settings.json
// was matching before this node's own narrower `Bash(git *)` ever mattered —
// which is why layer 2 used to be a declaration rather than a limit. Loading
// none of the user/project/local settings leaves this argv as the only
// allow-rule source, and under permission-mode dontAsk an unmatched call
// resolves to ask and an unanswerable ask becomes a DENY. Measured end-to-end
// on claude 2.1.220 (DESIGN.md, E1): with the user's settings granting
// `Bash(*)` and this node declaring `Bash(git *)`, an out-of-scope `touch` ran
// without isolation and was denied with it, while `git init` kept working.
//
// Layer 1 also closes the settings-hook gap for free: writing a
// `.claude/settings.local.json` into the invocation directory achieves nothing
// when no node of this run — or of any later auto run — loads local settings.
//
// The cost, which belongs in the README and not in a surprise: a planned node
// also loses the user's CLAUDE.md, hooks and MCP servers. Planned nodes are
// more isolated and less capable than they were. That is the intended
// direction.
func toolPolicyFor(node graph.Node) runner.ToolPolicy {
	return runner.ToolPolicy{
		AllowedTools:    node.AllowedTools,
		Tools:           narrowedToolsFor(node),
		SettingSources:  isolatedSettingSources(),
		StrictMCPConfig: true,
		DisallowedTools: disallowedToolsFor(node),
	}
}

// isolatedSettingSources is layer 1's value: load NONE of the user/project/
// local settings files. A fresh pointer per call so no caller can mutate a
// shared one into meaning something else.
//
// Enterprise policy settings and --settings flag settings are unioned on top
// and cannot be dropped by this flag, so it can never be used to step around a
// corporate policy.
func isolatedSettingSources() *string {
	none := ""
	return &none
}

// narrowedToolsFor is layer 3: the bare tool names this node declared, which
// become the ENTIRE built-in tool set available to it. --tools replaces that
// set rather than adding to it — measured on claude 2.1.220 (DESIGN.md, E4), a
// tool left out of --tools is absent even when --allowedTools names it — so
// this must list every tool the node needs and nothing more.
//
// Scope is dropped ("Bash(git *)" -> "Bash") because --tools takes tool names;
// the scoped pattern still binds, as layer 2's --allowedTools rule. Order
// follows the declaration and duplicates collapse, so a node declaring two
// Bash patterns yields one "Bash" and the argv stays deterministic.
func narrowedToolsFor(node graph.Node) []string {
	tools := make([]string, 0, len(node.AllowedTools))
	seen := make(map[string]bool, len(node.AllowedTools))
	for _, rule := range node.AllowedTools {
		name := toolName(rule)
		if seen[name] {
			continue
		}
		seen[name] = true
		tools = append(tools, name)
	}
	return tools
}

// disallowedToolsFor is layer 5: every deniable tool the node did not declare.
// Scope is dropped when comparing, so a node declaring "Bash(git *)" counts as
// having declared Bash and is not denied it — the best a deny list can do on
// its own, which is why it is now the residual layer rather than the ceiling.
func disallowedToolsFor(node graph.Node) []string {
	declared := make(map[string]bool, len(node.AllowedTools))
	for _, tool := range node.AllowedTools {
		declared[toolName(tool)] = true
	}
	denied := make([]string, 0, len(deniableTools))
	for _, tool := range deniableTools {
		if !declared[tool] {
			denied = append(denied, tool)
		}
	}
	return denied
}

// toolName strips a permission rule's scope: "Bash(git *)" → "Bash", "Read" →
// "Read".
func toolName(rule string) string {
	name, _, _ := strings.Cut(rule, "(")
	return name
}

// validatePlannedNodes enforces what auto mode refuses beyond the structural
// validation every graph gets. A hand-written graph is seen and owned by the
// user, so it may opt into more; an auto-planned graph was never reviewed:
//
//   - no planned graph may be empty — a nodeless plan would "succeed" while
//     doing nothing, after paying for the planner call;
//   - no planned node may run without a prompt;
//   - no planned node may be a gate — every gate pauses a fresh run for a
//     human decision, so an unreviewed auto-plan could park the run awaiting
//     an approval nobody knows to give, forever;
//   - no planned node may relax the sandbox with bypassPermissions;
//   - every planned node must declare a non-empty allowed_tools, and every
//     tool it names must be in plannedToolAllowlist. Omitting allowed_tools
//     is rejected too: the runner only appends --allowedTools when the list
//     is non-empty (internal/runner.ClaudeCLIRunner.buildArgs), so an empty
//     list would run under the CLI's own default tool set instead of this
//     allowlist — that gap would make the allowlist opt-in for an attacker
//     simply by leaving the field off;
//   - no planned node may set cwd (validatePlannedNodeCwd);
//   - no planned node may set success_check.verify
//     (validatePlannedNodeVerify);
//   - no planned node may set agent (validatePlannedNodeAgent).
//
// EVERY FIELD ON graph.Node MUST HAVE AN EXPLICIT DISPOSITION HERE — allowed,
// constrained, or rejected. This class of hole recurs every time the schema
// grows (`verify:` was one, `agent:` was the next), so adding a field to Node
// without adding a case here is a review-blocking defect, not a nit.
// TestPlannedNodeFieldDispositionsAreComplete walks graph.Node and
// graph.SuccessCheck by reflection and fails on any field with no recorded
// disposition, which turns that rule from a convention a reviewer has to
// remember into a red test.
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
		if err := validatePlannedNodeCwd(node); err != nil {
			return err
		}
		if err := validatePlannedNodeVerify(node); err != nil {
			return err
		}
		if err := validatePlannedNodeAgent(node); err != nil {
			return err
		}
		if err := validatePlannedNodeTools(node); err != nil {
			return err
		}
	}
	return nil
}

// validatePlannedNodeCwd rejects a planned node that redirects its working
// directory. cwd is a plain graph field, and the planner's reply is JSON parsed
// through the same graph.Parse as hand-written YAML, so nothing else stops a
// plan from naming an arbitrary path that flows straight into cmd.Dir. An
// auto-planned node always runs where the user invoked oh-my-graph; the planner
// prompt never offers cwd as a field, so any value here is out of band.
//
// What this bounds, precisely: WHERE an unreviewed plan can act — it keeps a
// planned node from reaching into an unrelated directory (a sibling checkout, a
// path under $HOME) that the user never put in scope. It does NOT make a
// write-capable node safe: such a node can still write inside the invocation
// directory, including a `.claude/settings.local.json` that a later node in
// this run, or a future run started there, would load. That gap is real and is
// listed with the others on deniableTools; do not read this check as closing
// it.
//
// Any non-empty value is rejected, including whitespace-only. A blank cwd is
// not equivalent to unset: it is passed through interpolation unchanged and
// reaches exec as a non-empty cmd.Dir, which fails the spawn with "chdir: no
// such file or directory". Accepting it would let a plan validate and then halt
// the run on its first node.
func validatePlannedNodeCwd(node graph.Node) error {
	if node.Cwd == "" {
		return nil
	}
	return &PlanError{
		Reason: fmt.Sprintf("planned node %q set cwd %q; auto mode always runs planned nodes in the invocation's working directory", node.ID, node.Cwd),
	}
}

// validatePlannedNodeVerify rejects a planned node that declares an evidence
// command. success_check.verify is arbitrary shell run by the ENGINE, not by
// claude: it is not a tool call, so it passes outside every guard this package
// builds — no permission mode, no allowed_tools, no deny list, and not even the
// cwd restriction, since a verification can name its own working directory. A
// plan that may write `verify: { command: "curl … | sh" }` has a hole straight
// through the rest of this file.
//
// Only the field itself is refused, not the whole check: exit_zero and
// result_matches are inert predicates over an outcome the engine already holds,
// so a planned node may still use them.
func validatePlannedNodeVerify(node graph.Node) error {
	if node.SuccessCheck.Verify == nil {
		return nil
	}
	return &PlanError{
		Reason: fmt.Sprintf(
			"planned node %q set success_check.verify (command %q); auto mode never runs a shell command from an unreviewed plan — exit_zero and result_matches are available instead",
			node.ID, node.SuccessCheck.Verify.Command,
		),
	}
}

// validatePlannedNodeAgent rejects a planned node that names a subagent.
// `agent:` routes around layers 0-3 in one word: it makes an unreviewed plan
// the thing that chooses which of the user's own subagents — and so which
// system prompt, which tool grant and which model — runs the node. Whatever
// bound the plan's declared allowed_tools were supposed to be, the resolved
// subagent's frontmatter is a second, unreviewed source of the same answers,
// and oh-my-graph does not parse it or reconcile it (see DESIGN.md, E6: the
// CLI's precedence between the two is measured in one direction only).
//
// Rejection rather than reconciliation, and rejection rather than silently
// clearing the field, because a plan that asked to run as someone's
// code-reviewer and instead ran as plain claude would be a different plan than
// the one that was validated.
//
// The field is also mutually exclusive with layer 1 in practice: a node under
// `--setting-sources ""` cannot resolve the user's ~/.claude/agents at all
// (DESIGN.md, E2), so an accepted `agent:` on a planned node would not even
// start. Two independent reasons, one rule.
func validatePlannedNodeAgent(node graph.Node) error {
	if node.Agent == "" {
		return nil
	}
	return &PlanError{
		Reason: fmt.Sprintf("planned node %q requested agent %q; auto mode never runs a planned node as one of your subagents", node.ID, node.Agent),
	}
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

// extractJSON isolates the JSON object from a coordinator call's reply (the
// planner's or the router's), tolerating a markdown code fence or stray prose
// around it: everything outside the first '{' and the last '}' is discarded.
// Returns "" when no object is present.
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
- Do not set permission_mode, budget_usd, type, cwd, or agent on any node.
  Every node runs in the directory oh-my-graph was invoked from, as plain
  claude — never as one of the user's subagents.
- If the goal involves creating a branch or committing on one, the final
  check node MUST verify which branch HEAD is on: its prompt runs
  'git rev-parse --abbrev-ref HEAD' (declare "Bash(git *)"), asserts the
  output equals the intended feature branch AND is not the repository's
  default branch (e.g. main or master), and replies PASS only when both
  hold, FAIL otherwise. Give that node
  "success_check": {"result_matches": "PASS"} so a commit that landed on
  the wrong branch fails the run instead of passing silently.
`
