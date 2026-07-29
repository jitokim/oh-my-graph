// Command oh-my-graph runs a YAML-defined DAG of claude-run nodes on your own
// logged-in claude subscription. It wires the concrete collaborators together —
// the real ClaudeCLIRunner, a per-run Handoff and RunLedger — and hands them to
// the Scheduler by constructor injection; there are no globals.
//
// Usage:
//
//	oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
//	oh-my-graph auto "<goal>" [--input k=v ...] [--concurrency N] [--continue-on-fail]
//	oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id>) [--concurrency N]
//	oh-my-graph runs list
//	oh-my-graph show <run-id>
//
// Exit codes: 0 every node passed, 1 the run failed, 2 the run paused at a
// gate and is resumable (ADR 0003) — a pause is not a failure.
package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

func main() {
	os.Exit(mainExitCode(os.Args[1:]))
}

// mainExitCode runs the CLI and returns the process exit code: 0 (every node
// passed), 1 (the run failed — printed to stderr), or 2 (the run paused at a
// gate and is resumable — the resume hint was already printed to stdout by
// executeGraph/runResume, so this path prints nothing further). Separated
// from main so the exit path lives in exactly one place and the mapping
// itself is testable without calling os.Exit.
func mainExitCode(args []string) int {
	err := run(args)
	if err == nil {
		return 0
	}
	var paused *schedule.PausedError
	if errors.As(err, &paused) {
		return 2
	}
	fmt.Fprintf(os.Stderr, "oh-my-graph: %v\n", err)
	return 1
}

// run parses argv and dispatches to the subcommand. It returns an error rather
// than exiting so the exit path lives in exactly one place (mainExitCode).
func run(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf(`usage: oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
       oh-my-graph auto "<goal>" [--input k=v ...] [--concurrency N] [--continue-on-fail]
       oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id>) [--concurrency N]
       oh-my-graph runs list
       oh-my-graph show <run-id>`)
	}
	switch args[0] {
	case "run":
		return runGraph(args[1:])
	case "auto":
		return runAuto(args[1:])
	case "resume":
		return runResume(args[1:])
	case "runs":
		return runRuns(args[1:])
	case "show":
		return runShow(args[1:])
	case "version":
		printVersion(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want run, auto, resume, runs, show, or version)", args[0])
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

	// Read the raw bytes ourselves (rather than graph.Load, which discards
	// them after parsing) so executeGraph can snapshot both the graph's
	// original source path and the SHA-256 of its original bytes — the datum
	// `resume` uses to warn when the YAML has changed on disk since the run
	// paused (DESIGN.md, "GraphSHA256").
	raw, err := os.ReadFile(flags.graphPath)
	if err != nil {
		return fmt.Errorf("read graph file %q: %w", flags.graphPath, err)
	}
	g, err := graph.Parse(raw)
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
	return executeGraph(ctx, newRunID(), g, runner.NewClaudeCLIRunner(), flags.commonRunFlags, nil, 0, flags.graphPath, raw)
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

	return executePlan(ctx, runID, plan, nodeRunner, flags.commonRunFlags, specPath)
}

// executePlan runs a coordinator Plan. It exists so the planned graph and its
// execution ceiling cannot be separated at the call site: a caller that had to
// pass plan.Graph and plan.ToolPolicies as two arguments could pass the graph
// with a nil ceiling and every planned node would silently run under the
// user's own standing tool grants — the exact hole auto mode must close, and a
// failure no test of the coordinator or the scheduler alone would catch.
// Taking the whole Plan makes that mismatch unrepresentable. It also forwards
// plan.CostUSD as the run's planning cost, so the end-of-run TOTAL COST
// includes the planning call rather than undercounting it. specPath is where
// the planner's JSON spec was saved (runAuto's graph.json) — plan.Spec is
// already the re-parseable JSON the resumable snapshot needs, so it is reused
// as-is rather than re-marshaling plan.Graph the way a hand-written `run` has
// to (see buildRecorder).
func executePlan(ctx context.Context, runID string, plan coordinator.Plan, nodeRunner runner.NodeRunner, flags commonRunFlags, specPath string) error {
	return executeGraph(ctx, runID, plan.Graph, nodeRunner, flags, plan.ToolPolicies, plan.CostUSD, specPath, plan.Spec)
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
// line and its total is unchanged. graphSourcePath and rawSource are the
// snapshot's GraphSourcePath/GraphSHA256 material — the .yaml file (and its
// bytes) for `run`, the saved graph.json (and the planner's JSON bytes) for
// `auto`.
func executeGraph(ctx context.Context, runID string, g *graph.Graph, nodeRunner runner.NodeRunner, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, planningCostUSD float64, graphSourcePath string, rawSource []byte) error {
	h := handoff.New(runDirFor(runID), flags.inputs)
	led := ledger.New(runID)
	led.RecordPlanningCost(planningCostUSD)

	recorder, err := newRunRecorder(runID, graphSourcePath, rawSource, g, flags, toolPolicies)
	if err != nil {
		return fmt.Errorf("prepare run snapshot: %w", err)
	}

	// The consumer event stream (docs/RUN-FEED.md) lives next to state.json.
	// Failing to open it is fatal up front — unlike a mid-run emit failure,
	// which the Scheduler downgrades to a progress warning — because a run
	// that never had a stream at all is silently invisible to fleetops.
	feed, err := runfeed.NewStreamWriter(filepath.Join(runDirFor(runID), runfeed.FileName), runID)
	if err != nil {
		return fmt.Errorf("prepare run event stream: %w", err)
	}
	defer feed.Close()

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:    flags.concurrency,
		ContinueOnFail: flags.continueOnFail,
		Gate:           gate.NewPauseController(),
		Verifier:       verify.NewShellVerifier(),
		ToolPolicies:   toolPolicies,
		Recorder:       recorder,
		EventSink:      feed,
	})

	fmt.Fprintf(os.Stdout, "Running graph %q (run %s)\n\n", g.Name, runID)
	runErr := scheduler.Run(ctx, g, h, led)

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)
	printPauseHint(os.Stdout, runID, runErr)

	return runErr
}

// newRunRecorder builds the SnapshotRecorder a fresh `run`/`auto` invocation
// hands the Scheduler, seeded with everything fixed for the whole run: the
// run id, where the graph came from, its normalized form as re-parseable
// JSON, the run's inputs/flags, and the auto-mode tool ceiling (nil for a
// hand-written graph, preserved as nil — see toNodeToolPolicies). Nodes and
// Gate start empty: nothing has run yet.
//
// rawSource decides whether the snapshot's Graph needs re-encoding: auto
// mode's rawSource is already the planner's JSON reply, so it is valid,
// re-parseable JSON as-is and is reused directly; a hand-written graph's
// rawSource is YAML text, which is not embeddable as-is inside the JSON
// snapshot document (runstate.Write would fail marshaling it), so it is
// re-encoded via json.Marshal(g) — safe because graph.Node/Graph's json tags
// mirror their yaml tags exactly (see internal/graph, Node's doc comment).
func newRunRecorder(runID, graphSourcePath string, rawSource []byte, g *graph.Graph, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy) (*runstate.SnapshotRecorder, error) {
	graphJSON := rawSource
	if !json.Valid(rawSource) {
		marshaled, err := json.Marshal(g)
		if err != nil {
			return nil, fmt.Errorf("encode graph for snapshot: %w", err)
		}
		graphJSON = marshaled
	}

	statePath := filepath.Join(runDirFor(runID), "state.json")
	base := runstate.Snapshot{
		RunID:           runID,
		GraphSourcePath: graphSourcePath,
		GraphSHA256:     sha256Hex(rawSource),
		Graph:           graphJSON,
		Inputs:          map[string]string(flags.inputs),
		ContinueOnFail:  flags.continueOnFail,
		ToolPolicies:    toNodeToolPolicies(toolPolicies),
	}
	return runstate.NewSnapshotRecorder(statePath, base), nil
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

// runsRoot is the on-disk home of every run directory — what `runs list`
// enumerates.
func runsRoot() string {
	return filepath.Join(".oh-my-graph", "runs")
}

// runDirFor is the on-disk home of one run's artifacts and (for auto runs) its
// generated graph spec.
func runDirFor(runID string) string {
	return filepath.Join(runsRoot(), runID)
}

// newRunID is a filesystem-safe, sortable run id: a UTC timestamp to the
// second. One run directory per invocation keeps a run's artifacts inspectable.
func newRunID() string {
	return time.Now().UTC().Format("20060102-150405")
}

// sha256Hex is the hex SHA-256 of data — Snapshot.GraphSHA256's exact format,
// so `resume` can compare it against a freshly-hashed file without any
// encoding back-and-forth.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// toNodeToolPolicies converts a run's runner.ToolPolicy map (the Scheduler's
// shape) into the snapshot's runstate.NodeToolPolicy map — the CLI boundary
// DESIGN.md assigns this conversion to, so neither package depends on the
// other's type. A nil map (the `run` path: no ceiling imposed) stays nil,
// never an empty non-nil map, because Scheduler.policyFor and the resumed
// leg's rebuilt Options.ToolPolicies both branch on nilness to mean "no
// ceiling at all" — collapsing that distinction here would silently start
// imposing an empty ceiling on a hand-written graph's resume.
func toNodeToolPolicies(policies map[string]runner.ToolPolicy) map[string]runstate.NodeToolPolicy {
	if policies == nil {
		return nil
	}
	out := make(map[string]runstate.NodeToolPolicy, len(policies))
	for id, p := range policies {
		out[id] = runstate.NodeToolPolicy{
			AllowedTools:    p.AllowedTools,
			DisallowedTools: p.DisallowedTools,
			Tools:           p.Tools,
			SettingSources:  p.SettingSources,
			StrictMCPConfig: p.StrictMCPConfig,
		}
	}
	return out
}

// printPauseHint prints the exact resume commands when runErr is a
// *schedule.PausedError — the "print the exact resume command" step of the
// gate lifecycle (DESIGN.md, "Gate nodes and resume") — and is a silent no-op
// for any other outcome (success or failure), so it is safe to call
// unconditionally after every run.
func printPauseHint(w io.Writer, runID string, runErr error) {
	var paused *schedule.PausedError
	if !errors.As(runErr, &paused) {
		return
	}
	fmt.Fprintf(w, "\nPaused at gate %q. Resume with:\n  oh-my-graph resume %s --approve %s\n  oh-my-graph resume %s --reject %s\n",
		paused.GateID, runID, paused.GateID, runID, paused.GateID)
}
