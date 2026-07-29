// Command oh-my-graph runs a YAML-defined DAG of claude-run nodes on your own
// logged-in claude subscription. It wires the concrete collaborators together —
// the real ClaudeCLIRunner, a per-run Handoff and RunLedger — and hands them to
// the Scheduler by constructor injection; there are no globals.
//
// Usage:
//
//	oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
//	oh-my-graph auto "<goal>" [--input k=v ...] [--concurrency N] [--continue-on-fail]
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "oh-my-graph: %v\n", err)
		os.Exit(1)
	}
}

// run parses argv and dispatches to the subcommand. It returns an error rather
// than exiting so the exit path lives in exactly one place (main).
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
       oh-my-graph auto "<goal>" [--input k=v ...] [--concurrency N] [--continue-on-fail]`)
	}
	switch args[0] {
	case "run":
		return runGraph(args[1:])
	case "auto":
		return runAuto(args[1:])
	case "version":
		printVersion(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want run, auto, or version)", args[0])
	}
}

// inputFlag collects repeated --input k=v pairs into a map.
type inputFlag map[string]string

func (f inputFlag) String() string { return "" }

func (f inputFlag) Set(pair string) error {
	key, value, found := strings.Cut(pair, "=")
	if !found || key == "" {
		return fmt.Errorf("invalid --input %q (want key=value)", pair)
	}
	f[key] = value
	return nil
}

// runGraph is the `run` subcommand: load and validate the graph, warn about any
// dangerous per-node opt-ins, wire the production collaborators, and execute.
func runGraph(args []string) error {
	flags := newRunFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	g, err := graph.Load(flags.graphPath)
	if err != nil {
		return err
	}
	warnBypassPermissions(g)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// nil tool policies: a hand-written graph is the user's own reviewed
	// artifact, so its nodes run under the user's own settings, hooks, MCP
	// servers and tool permissions, unchanged. 0 planning cost: `run` has no
	// planning step, so its total shows no planning line and is exactly the
	// per-node sum.
	return executeGraph(ctx, newRunID(), g, runner.NewClaudeCLIRunner(), flags.commonRunFlags, nil, 0)
}

// runAuto is the `auto` subcommand — the zero-config path (hand-written YAML
// stays the precise-control path). The coordinator turns the goal into a graph
// spec via one planner call through the same env-scrubbed NodeRunner every
// node uses; the validated result is saved for inspection and then executed by
// the same scheduler as a hand-written graph. Generated graphs can never opt
// into bypassPermissions (the coordinator rejects them), so no warning pass is
// needed here.
func runAuto(args []string) error {
	flags := newAutoFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	nodeRunner := runner.NewClaudeCLIRunner()

	fmt.Fprintf(os.Stdout, "Planning a graph for goal %q...\n", flags.goal)
	plan, err := coordinator.New(nodeRunner).Plan(ctx, flags.goal, inputKeys(flags.inputs))
	if err != nil {
		return err
	}

	runID := newRunID()
	specPath, err := saveGeneratedSpec(runDirFor(runID), plan.Spec)
	if err != nil {
		return err
	}
	printPlan(os.Stdout, plan, specPath)

	return executePlan(ctx, runID, plan, nodeRunner, flags.commonRunFlags)
}

// executePlan runs a coordinator Plan. It exists so the planned graph and its
// execution ceiling cannot be separated at the call site: a caller that had to
// pass plan.Graph and plan.ToolPolicies as two arguments could pass the graph
// with a nil ceiling and every planned node would silently run under the
// user's own standing tool grants — the exact hole auto mode must close, and a
// failure no test of the coordinator or the scheduler alone would catch.
// Taking the whole Plan makes that mismatch unrepresentable. It also forwards
// plan.CostUSD as the run's planning cost, so the end-of-run TOTAL COST
// includes the planning call rather than undercounting it.
func executePlan(ctx context.Context, runID string, plan coordinator.Plan, nodeRunner runner.NodeRunner, flags commonRunFlags) error {
	return executeGraph(ctx, runID, plan.Graph, nodeRunner, flags, plan.ToolPolicies, plan.CostUSD)
}

// executeGraph wires the per-run collaborators (Handoff, RunLedger, Scheduler)
// around an already-validated graph and runs it — the shared back half of both
// `run` and `auto`. This is where the two exec seams are injected: the
// ClaudeCLIRunner the caller passed (a node's claude subprocess) and a
// ShellVerifier (a node's success_check.verify command). A planned graph can
// never declare a verification — the coordinator rejects the field — so for
// `auto` the verifier is wired but never reached. toolPolicies is the per-node
// execution ceiling: auto passes the coordinator's, `run` passes nil.
// planningCostUSD is the coordinator's one planning-call cost, folded into the
// ledger's total so an auto run's end-of-run TOTAL COST is honest about the
// planning step; `run` passes 0 (no planning step), so it shows no planning
// line and its total is unchanged.
func executeGraph(ctx context.Context, runID string, g *graph.Graph, nodeRunner runner.NodeRunner, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, planningCostUSD float64) error {
	h := handoff.New(runDirFor(runID), flags.inputs)
	led := ledger.New(runID)
	led.RecordPlanningCost(planningCostUSD)

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: flags.continueOnFail,
		Verifier:       verify.NewShellVerifier(),
		ToolPolicies:   toolPolicies,
	})

	fmt.Fprintf(os.Stdout, "Running graph %q (run %s)\n\n", g.Name, runID)
	runErr := scheduler.Run(ctx, g, h, led)

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)

	return runErr
}

// saveGeneratedSpec persists the planner's JSON spec into the run directory so
// an auto run stays inspectable and repeatable: JSON is valid YAML, so the
// saved file can be hand-edited and re-run directly with `oh-my-graph run
// <path>` — it is indented before writing so that editing is practical.
func saveGeneratedSpec(runDir string, spec []byte) (string, error) {
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return "", fmt.Errorf("create run dir %q: %w", runDir, err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, spec, "", "  "); err == nil {
		spec = indented.Bytes()
	}
	path := filepath.Join(runDir, "graph.json")
	if err := os.WriteFile(path, spec, 0o644); err != nil {
		return "", fmt.Errorf("save generated graph spec: %w", err)
	}
	return path, nil
}

// printPlan shows the planned topology (node ids, edges, each node's tools)
// and the planning cost before execution. The full prompts live in the saved
// spec file, which is named here so the user can review it.
//
// This is the screen someone reads before letting an unattended run start, so
// it must describe the ceiling as it actually is — neither tighter (it used to
// have to disclaim that a declared Bash scope was not enforced) nor looser
// (planned nodes now run without the user's own settings, which is a real
// behaviour change and not something to be discovered later). noteCeiling
// prints both halves.
func printPlan(w io.Writer, plan coordinator.Plan, specPath string) {
	g := plan.Graph
	fmt.Fprintf(w, "Planned graph %q (%d nodes, planning cost $%.4f, saved to %s):\n", g.Name, len(g.Nodes), plan.CostUSD, specPath)
	for _, node := range g.Nodes {
		line := "  - " + node.ID
		if len(node.DependsOn) > 0 {
			line += " (after " + strings.Join(node.DependsOn, ", ") + ")"
		}
		if len(node.AllowedTools) > 0 {
			line += " [tools: " + strings.Join(node.AllowedTools, ", ") + "]"
		}
		fmt.Fprintln(w, line)
	}
	noteCeiling(w)
	fmt.Fprintln(w)
}

// noteCeiling states what running this plan actually does to the machine. It
// prints for every planned run rather than only when some node declares Bash,
// because the isolation half applies to all of them: a planned node loads none
// of your settings, so it also gets none of your CLAUDE.md, hooks or MCP
// servers. Nodes are more isolated and less capable here than in a
// hand-written graph, and that is worth one line up front rather than a
// puzzling failure ten minutes in.
func noteCeiling(w io.Writer) {
	fmt.Fprint(w,
		"  Planned nodes run isolated: none of your user/project/local settings load, so a declared\n"+
			"  scope like Bash(git *) is enforced rather than merely requested — and your CLAUDE.md,\n"+
			"  hooks and MCP servers are unavailable to them. See SECURITY.md for what this does not cover.\n",
	)
}

// inputKeys lists the bound --input names — what the planner is allowed to
// reference as {{ inputs.<name> }} in generated prompts.
func inputKeys(inputs inputFlag) []string {
	keys := make([]string, 0, len(inputs))
	for key := range inputs {
		keys = append(keys, key)
	}
	return keys
}

// warnBypassPermissions prints a loud, per-node warning for any node that opts
// into bypassPermissions — it is never a graph default and the user should see
// exactly which node relaxed the sandbox.
func warnBypassPermissions(g *graph.Graph) {
	for _, node := range g.Nodes {
		if node.PermissionMode == graph.PermissionBypass {
			fmt.Fprintf(os.Stderr,
				"WARNING: node %q uses permission_mode: bypassPermissions — it can act without prompting. Review the graph before running.\n",
				node.ID,
			)
		}
	}
}

// runDirFor is the on-disk home of one run's artifacts and (for auto runs) its
// generated graph spec.
func runDirFor(runID string) string {
	return filepath.Join(".oh-my-graph", "runs", runID)
}

// newRunID is a filesystem-safe, sortable run id: a UTC timestamp to the
// second. One run directory per invocation keeps a run's artifacts inspectable.
func newRunID() string {
	return time.Now().UTC().Format("20060102-150405")
}
