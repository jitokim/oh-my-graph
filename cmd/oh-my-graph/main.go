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

	// nil deny list: a hand-written graph is the user's own reviewed artifact,
	// so its nodes run under the user's own tool permissions, unchanged.
	return executeGraph(ctx, newRunID(), g, runner.NewClaudeCLIRunner(), flags.commonRunFlags, nil)
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
// pass plan.Graph and plan.DisallowedTools as two arguments could pass the
// graph with a nil ceiling and every planned node would silently run under the
// user's own standing tool grants — the exact hole auto mode must close, and a
// failure no test of the coordinator or the scheduler alone would catch.
// Taking the whole Plan makes that mismatch unrepresentable.
func executePlan(ctx context.Context, runID string, plan coordinator.Plan, nodeRunner runner.NodeRunner, flags commonRunFlags) error {
	return executeGraph(ctx, runID, plan.Graph, nodeRunner, flags, plan.DisallowedTools)
}

// executeGraph wires the per-run collaborators (Handoff, RunLedger, Scheduler)
// around an already-validated graph and runs it — the shared back half of both
// `run` and `auto`. disallowedTools is the per-node execution ceiling: auto
// passes the coordinator's, `run` passes nil.
func executeGraph(ctx context.Context, runID string, g *graph.Graph, nodeRunner runner.NodeRunner, flags commonRunFlags, disallowedTools map[string][]string) error {
	h := handoff.New(runDirFor(runID), flags.inputs)
	led := ledger.New(runID)

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:     flags.concurrency,
		ContinueOnFail:  flags.continueOnFail,
		DisallowedTools: disallowedTools,
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
// The per-node tool list is what the plan DECLARED, which is not the same as
// what the node can do — a node declaring any scoped Bash pattern retains the
// whole Bash tool (see coordinator.deniableTools). This is the screen someone
// reads before letting an unattended run start, so it must not imply a tighter
// sandbox than exists: noteBashScope prints that caveat when it applies.
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
	noteBashScope(w, g)
	fmt.Fprintln(w)
}

// noteBashScope warns that scoped Bash patterns are a declaration, not a limit.
// A deny list can remove the Bash tool entirely but cannot narrow it to a set of
// command prefixes, so any node declaring Bash keeps all of it if the user's own
// settings grant it. Silent when no node declares Bash — those nodes really are
// denied the tool.
func noteBashScope(w io.Writer, g *graph.Graph) {
	var scoped []string
	for _, node := range g.Nodes {
		for _, tool := range node.AllowedTools {
			if strings.HasPrefix(tool, "Bash") {
				scoped = append(scoped, node.ID)
				break
			}
		}
	}
	if len(scoped) == 0 {
		return
	}
	fmt.Fprintf(w,
		"  NOTE: %s declare a scoped Bash pattern. That scope is what the plan asked for, not an enforced\n"+
			"        limit — these nodes keep the whole Bash tool if your settings.json grants it.\n",
		strings.Join(scoped, ", "),
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
