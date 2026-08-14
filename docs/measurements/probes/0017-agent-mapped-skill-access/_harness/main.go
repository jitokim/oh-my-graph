//go:build ignore

// Reconstructs the argv a PLANNED node is really spawned with, from the code
// that builds it — never from a transcription of it into a shell script.
//
// The chain exercised here is the production one, end to end:
//
//	coordinator.Plan            -> validation, applyAgentMapping,
//	                               applySkillActivation (the real functions)
//	Plan.BindSkillStaging       -> the staged plugin dir, as `auto` binds it
//	schedule's buildInvocation  -> re-created field for field below
//	runner.CLIRunner.Run  -> runner.buildArgs, the thing under measurement
//
// Exactly two things are substituted, and both are ends of the chain rather
// than steps in it: the planner is a canned NodeRunner returning a fixed graph
// JSON, and the claude binary is a shim (runner.WithBinary) that writes its own
// argv to $OMG_ARGV_DIR and exits. Everything in between is the shipped code.
//
// Usage: go run _harness/main.go <workspace-dir> <shim-path>
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// cannedPlanner stands in for the planner call. Plan() only ever reads the
// outcome's Result, ExitCode and TotalCostUSD, so a fixed reply drives the
// whole post-validation pipeline for free.
type cannedPlanner struct{ reply string }

func (p cannedPlanner) Run(context.Context, runner.NodeInvocation) (runner.NodeOutcome, error) {
	return runner.NodeOutcome{SessionID: "canned", Result: p.reply, ExitCode: 0}, nil
}

// The prompt is identical for both nodes so the two argv rows differ only in
// what the coordinator did to them. It NAMES the skill: this probe asks
// whether a node CAN invoke one, not whether the description gate fires.
const nodePrompt = "Use the `omg-probe-standalone-html` skill and follow its procedure exactly, " +
	"then render the note below into `design.html` — a standalone single-file HTML page — " +
	"in the current working directory.\n\n" +
	"# Job runner scheduling\n\n" +
	"A small local job runner executes short-lived jobs submitted from a CLI. Jobs are\n" +
	"IO-bound, arrive in bursts, and must not outlive the process that started them.\n" +
	"Decision: a worker pool, bounded to NCPU-2 workers.\n"

func main() {
	if len(os.Args) != 3 {
		fmt.Fprintln(os.Stderr, "usage: main.go <workspace-dir> <shim-path>")
		os.Exit(2)
	}
	ws, shim := os.Args[1], os.Args[2]
	work := filepath.Join(ws, "work")
	argvRoot := filepath.Join(ws, "argv")
	runDir := filepath.Join(ws, "run")

	// `omg-probe-writer` matches the planted agent by agentmap's token rule and
	// so is AGENT-MAPPED. `render-artifact` shares no token with it and so is
	// the ordinary ACTIVATED node. One plan, both dispositions, same prompt.
	spec, err := json.Marshal(graph.Graph{
		Name:    "omg-agentmap-skill-probe",
		Version: "1",
		Nodes: []graph.Node{
			{ID: "omg-probe-writer", Prompt: nodePrompt, AllowedTools: []string{"Write"}},
			{ID: "render-artifact", Prompt: nodePrompt, AllowedTools: []string{"Write"}},
		},
	})
	must(err)

	c := coordinator.New(
		cannedPlanner{reply: string(spec)},
		coordinator.WithAgentDirs(filepath.Join(work, ".claude", "agents")),
		coordinator.WithSkillDirs(filepath.Join(ws, "skills-src")),
		coordinator.WithInvocationDir(work),
	)
	plan, err := c.Plan(context.Background(), "render the note as a standalone HTML page", nil)
	must(err)
	must(plan.BindSkillStaging(runDir))

	// A planned node never sets cwd, so the scheduler leaves NodeInvocation.Cwd
	// empty and the child inherits this process's directory. Chdir rather than
	// setting Cwd, so the argv and the cwd are both the production shape.
	must(os.Chdir(work))

	report := map[string]any{
		"activation_enabled":  plan.SkillActivation != nil && plan.SkillActivation.Enabled,
		"activated_node_ids":  ids(plan, func(a *coordinator.SkillActivation) []string { return a.NodeIDs }),
		"excluded_node_ids":   ids(plan, func(a *coordinator.SkillActivation) []string { return a.ExcludedNodeIDs }),
		"agent_mappings":      plan.AgentMappings,
		"staged_skill_count":  len(stagedSkills(plan)),
		"plugin_dir":          pluginDir(plan),
		"nodes":               map[string]any{},
		"prompt_notice_bytes": promptNotice(plan),
	}
	nodes := report["nodes"].(map[string]any)

	cli := runner.NewCLIRunner(runner.RuntimeClaude, runner.WithBinary(shim))
	for _, node := range plan.Graph.Nodes {
		dir := filepath.Join(argvRoot, node.ID)
		must(os.MkdirAll(dir, 0o755))
		must(os.Setenv("OMG_ARGV_DIR", dir))

		policy, ok := plan.ToolPolicies[node.ID]
		if !ok {
			panic("no policy for " + node.ID)
		}
		// Field for field, schedule.Scheduler.buildInvocation (scheduler.go:1323)
		// for a node with no worktree, no cwd, no handoff placeholders and no
		// session-parent to resume.
		mode := node.PermissionMode
		if mode == "" {
			mode = string(graph.PermissionDontAsk)
		}
		_, runErr := cli.Run(context.Background(), runner.NodeInvocation{
			Prompt:         node.Prompt,
			Cwd:            node.Cwd,
			PermissionMode: mode,
			SessionID:      runner.NewSessionID(),
			Agent:          node.Agent,
			BudgetUSD:      node.BudgetUSD,
			Timeout:        node.TimeoutDuration(),
			Policy:         policy,
		})
		if runErr != nil {
			fmt.Fprintf(os.Stderr, "note: shim run for %s returned %v (argv is still recorded)\n", node.ID, runErr)
		}
		nodes[node.ID] = map[string]any{
			"agent":            node.Agent,
			"argv_dir":         dir,
			"tools":            policy.Tools,
			"allowed_tools":    policy.AllowedTools,
			"disallowed_tools": policy.DisallowedTools,
			"setting_sources":  settingSources(policy),
			"plugin_dirs":      policy.PluginDirs,
			"prompt_bytes":     len(node.Prompt),
		}
	}

	out, err := json.MarshalIndent(report, "", "  ")
	must(err)
	fmt.Println(string(out))
}

// settingSources renders layer 1 the way buildArgs decides it: nil means the
// FLAG IS OMITTED (the user's settings load as usual), which is a different
// thing from a pointer to "".
func settingSources(p runner.ToolPolicy) any {
	if p.SettingSources == nil {
		return nil
	}
	return *p.SettingSources
}

func ids(p coordinator.Plan, pick func(*coordinator.SkillActivation) []string) []string {
	if p.SkillActivation == nil {
		return nil
	}
	return pick(p.SkillActivation)
}

func stagedSkills(p coordinator.Plan) []coordinator.StagedSkill {
	if p.SkillActivation == nil {
		return nil
	}
	return p.SkillActivation.Skills
}

func pluginDir(p coordinator.Plan) string {
	if p.SkillActivation == nil {
		return ""
	}
	return p.SkillActivation.PluginDir
}

func promptNotice(p coordinator.Plan) string {
	if p.SkillActivation == nil {
		return ""
	}
	return p.SkillActivation.PromptNotice
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
