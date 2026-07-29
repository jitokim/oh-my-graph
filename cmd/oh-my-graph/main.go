// Command oh-my-graph runs a YAML-defined DAG of claude-run nodes on your own
// logged-in claude subscription. It wires the concrete collaborators together —
// the real ClaudeCLIRunner, a per-run Handoff and RunLedger — and hands them to
// the Scheduler by constructor injection; there are no globals.
//
// Usage:
//
//	oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

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
		return fmt.Errorf("usage: oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]")
	}
	switch args[0] {
	case "run":
		return runGraph(args[1:])
	case "version":
		printVersion(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (only `run` and `version` are supported in v0.1)", args[0])
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

	runID := newRunID()
	runDir := filepath.Join(".oh-my-graph", "runs", runID)
	h := handoff.New(runDir, flags.inputs)
	led := ledger.New(runID)

	nodeRunner := runner.NewClaudeCLIRunner()
	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: flags.continueOnFail,
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stdout, "Running graph %q (run %s)\n\n", g.Name, runID)
	runErr := scheduler.Run(ctx, g, h, led)

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)

	return runErr
}

// warnBypassPermissions prints a loud, per-node warning for any node that opts
// into bypassPermissions — it is never a graph default and the user should see
// exactly which node relaxed the sandbox.
func warnBypassPermissions(g *graph.Graph) {
	for _, node := range g.Nodes {
		if node.PermissionMode == "bypassPermissions" {
			fmt.Fprintf(os.Stderr,
				"WARNING: node %q uses permission_mode: bypassPermissions — it can act without prompting. Review the graph before running.\n",
				node.ID,
			)
		}
	}
}

// newRunID is a filesystem-safe, sortable run id: a UTC timestamp to the
// second. One run directory per invocation keeps a run's artifacts inspectable.
func newRunID() string {
	return time.Now().UTC().Format("20060102-150405")
}
