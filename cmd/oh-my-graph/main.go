// Command oh-my-graph runs a YAML-defined DAG of claude-run nodes on your own
// logged-in claude subscription. It wires the concrete collaborators together —
// the real ClaudeCLIRunner, a per-run Handoff and RunLedger — and hands them to
// the Scheduler by constructor injection; there are no globals.
//
// Usage (the same text run() prints; usage_test.go derives both from the
// dispatch switch and every subcommand's FlagSet, so neither can drift):
//
//	oh-my-graph init [dir]
//	oh-my-graph run <graph.yaml> [--dry-run] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]
//	oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--max-cycles N] [--max-goal-budget-usd X] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web] [--no-agent-mapping] [--no-skill-mapping]
//	oh-my-graph lint <graph.yaml>
//	oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--concurrency N] [--no-web]
//	oh-my-graph runs list
//	oh-my-graph show <run-id>
//	oh-my-graph watch <run-id>
//	oh-my-graph serve [<run-id>] [--port N] [--no-open]   (no run id: the dashboard over every run)
//	oh-my-graph chat [--no-agent-mapping] [--no-skill-mapping]
//	oh-my-graph version
//
// Exit codes: 0 every node passed, 1 the run failed, 2 the run paused and is
// resumable — at a gate awaiting a human decision (ADR 0003) or because the
// subscription's session limit was hit (ADR 0009). A pause is not a failure.
// An iterated auto run (--max-cycles ≥ 2, ADR 0011) makes the contract
// goal-level: exit 0 additionally requires the assessor's goal-met verdict on
// a passed final cycle, and stopping unmet (cycles exhausted, budget ceiling,
// a declined later cycle) exits 1 even when every run passed.
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
	"sync/atomic"
	"syscall"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
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
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

func main() {
	os.Exit(mainExitCode(os.Args[1:]))
}

// mainExitCode runs the CLI and returns the process exit code: 0 (every node
// passed), 1 (the run failed — printed to stderr), or 2 (the run paused — at
// a gate or on a session limit — and is resumable; the resume hint was
// already printed to stdout by executeGraph/runResume, so this path prints
// nothing further). Separated from main so the exit path lives in exactly one
// place and the mapping itself is testable without calling os.Exit.
func mainExitCode(args []string) int {
	err := run(args)
	if err == nil {
		return 0
	}
	var paused *schedule.PausedError
	if errors.As(err, &paused) {
		return 2
	}
	var limited *schedule.LimitPausedError
	if errors.As(err, &limited) {
		return 2
	}
	fmt.Fprintf(os.Stderr, "oh-my-graph: %v\n", err)
	return 1
}

// usageLines is the one copy of the subcommand synopsis the CLI prints. The
// package doc comment above carries the same block — godoc's Usage: convention
// wants it there, and a comment cannot be a constant — and usage_test.go pins
// the two together line for line, then pins each line's flags against that
// subcommand's real FlagSet. That second half is the one that matters: a
// synopsis advertising a flag nobody registered is a support ticket waiting to
// happen (this file once documented `serve --run`), and a synopsis omitting a
// registered flag hides a feature. The continuation indent aligns each line
// under the "usage: " prefix.
const usageLines = `oh-my-graph init [dir]
       oh-my-graph run <graph.yaml> [--dry-run] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]
       oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--max-cycles N] [--max-goal-budget-usd X] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web] [--no-agent-mapping] [--no-skill-mapping]
       oh-my-graph lint <graph.yaml>
       oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--concurrency N] [--no-web]
       oh-my-graph runs list
       oh-my-graph show <run-id>
       oh-my-graph watch <run-id>
       oh-my-graph serve [<run-id>] [--port N] [--no-open]   (no run id: the dashboard over every run)
       oh-my-graph chat [--no-agent-mapping] [--no-skill-mapping]
       oh-my-graph version`

// run parses argv and dispatches to the subcommand. It returns an error rather
// than exiting so the exit path lives in exactly one place (mainExitCode).
func run(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: " + usageLines)
	}
	switch args[0] {
	case "init":
		return runInit(args[1:])
	case "run":
		return runGraph(args[1:])
	case "auto":
		return runAuto(args[1:])
	case "lint":
		return runLint(args[1:])
	case "resume":
		return runResume(args[1:])
	case "runs":
		return runRuns(args[1:])
	case "show":
		return runShow(args[1:])
	case "watch":
		return runWatch(args[1:])
	case "serve":
		return runServe(args[1:])
	case "chat":
		return runChat(args[1:])
	case "version":
		printVersion(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want init, run, auto, lint, resume, runs, show, watch, serve, chat, or version)", args[0])
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
// With --dry-run it stops after validation and the plan print — nothing is
// wired and no node runs.
func runGraph(args []string) error {
	// One of the four sites (with runAuto, runResume and runServe) injecting
	// the real browser launcher (browser.ExecOpener, the fourth exec seam —
	// ADR 0006); everywhere else the Opener stays refusing or absent.
	// webOpener still gates whether it is ever used.
	return runGraphWith(args, runner.NewClaudeCLIRunner(), browser.NewExecOpener(), os.Stdout)
}

// runGraphWith is runGraph with its seams injectable: the runner (so a test
// can prove --dry-run never reaches it — a FakeRunner must see zero
// invocations) and the live view's opener plus the stdout whose TTY-ness
// gates it (so a test can prove a terminal run opens the view and a
// non-terminal run changes nothing, with a FakeOpener and no real spawn).
func runGraphWith(args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	flags := newRunFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.dryRun {
		return dryRunGraph(os.Stdout, os.Stderr, flags.graphPath, flags.inputs)
	}

	// The path-aware load stage (ADR 0013): resolve any `use:` fragments
	// against the graph file's own fragments/ sibling before validation.
	// LoadFile keeps the entry file's raw bytes so executeGraph can snapshot
	// both the graph's original source path and the SHA-256 of its original
	// bytes — the datum `resume` uses to warn when the YAML has changed on
	// disk since the run paused (DESIGN.md, "GraphSHA256").
	loaded, err := graph.LoadFile(flags.graphPath)
	if err != nil {
		return err
	}
	g := loaded.Graph
	printFragmentResolutions(os.Stdout, loaded.Resolutions)
	// The advisory half of the same disclosure, through the same helper `lint`
	// and `--dry-run` use: a run that announces which fragments it spliced must
	// announce their drift smell too, or an identical file warns under `lint`
	// and goes quiet under the command that spends money. Advice only — it
	// cannot fail the run. The three handoff sweeps (placeholders, cold session
	// resumes, unchecked verdicts) stay lint-only by contrast: those judge the
	// whole graph and are the pre-flight command's job, not a per-run
	// disclosure.
	warnFragmentAdvisories(os.Stderr, flags.graphPath, loaded.Advisories)
	warnBypassPermissions(g)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// nil tool policies: a hand-written graph is the user's own reviewed
	// artifact, so its nodes run under the user's own settings, hooks, MCP
	// servers and tool permissions, unchanged. 0 planning cost: `run` has no
	// planning step, so its total shows no planning line and is exactly the
	// per-node sum.
	return executeGraph(ctx, newRunID(), g, nodeRunner, flags.commonRunFlags, nil, 0, flags.graphPath, loaded.Source,
		len(loaded.Resolutions) > 0, webOpener(flags.noWeb, stdout, opener), nil)
}

// runAuto is the `auto` subcommand — the zero-config path (hand-written YAML
// stays the precise-control path). The coordinator turns the goal into a graph
// spec via one planner call through the same env-scrubbed NodeRunner every
// node uses; the validated result is saved for inspection and then executed by
// the same scheduler as a hand-written graph. Generated graphs can never opt
// into bypassPermissions (the coordinator rejects them), so no warning pass is
// needed here.
func runAuto(args []string) error {
	// One of the four sites (with runGraph, runResume and runServe) injecting
	// the real browser launcher (browser.ExecOpener, the fourth exec seam —
	// ADR 0006).
	return runAutoWith(args, runner.NewClaudeCLIRunner(), browser.NewExecOpener(), os.Stdout)
}

// runAutoWith is runAuto with its seams injectable, mirroring runGraphWith and
// for the same reason: --plan-only's whole claim is that no node runs, and the
// only way to prove that is through the real argv path with a FakeRunner that
// must see the planner call and nothing else. The stdout parameter is what
// gates the live view (a non-terminal one leaves it off), so a test needs no
// real spawn on that seam either.
func runAutoWith(args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	flags := newAutoFlags()
	if err := flags.parse(args); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The zero-config path says out loud what it is not checking, before the
	// planner call rather than after it, so the user can Ctrl-C and re-run with
	// one flag instead of learning it beside a finished ledger (ADR 0016 §3).
	// "." is the invocation directory: the same tree the planned nodes and the
	// evidence command would both run in.
	verifyCommand := flags.verifyCommand()
	noteVerifyAdvice(os.Stdout, verifyCommand, ".")

	// Same live-view gate as `run` and `resume`. WithVerifyCommand is given to
	// the COORDINATOR, not to a cycle: every cycle of a --max-cycles goal loop
	// re-enters the same coordinator and plans afresh, so every cycle's sinks
	// carry the command and every cycle's run is gated on it (ADR 0016 §2).
	options := append(mappingOptions(os.Stdout, flags.noAgentMapping, flags.noSkillMapping),
		coordinator.WithVerifyCommand(verifyCommand))
	coord := coordinator.New(nodeRunner, options...)
	return planAndExecute(ctx, os.Stdout, coord, nodeRunner, flags.commonRunFlags, flags.goal,
		goalCycleOptions{maxCycles: flags.maxCycles, maxGoalBudgetUSD: flags.maxGoalBudgetUSD}, flags.planOnly, nil,
		webOpener(flags.noWeb, stdout, opener))
}

// mappingOptions wires the two auto-mappings for a production Coordinator:
// subagent mapping over the default user-then-project agent directories, and
// skill mapping over the default user skill directory (never a project one —
// ADR 0012), each independently switchable off by its flag. This is the only
// place the real filesystem locations enter the coordinator — tests construct
// theirs with temp dirs instead.
//
// w takes the one thing this function knows and the plan printout cannot: a
// coordinator built with ZERO skill directories never scans, so it records no
// SkillScan and noteSkillMappings prints nothing — correct for the library
// default (coordinator.New with no dirs) and for --no-skill-mapping, but
// silence here would mean skill mapping is ON, maps nothing, and says nothing
// at all. DefaultSkillDirs returns empty in exactly one case, an unresolvable
// home directory (no $HOME: containers, launchd, some CI), and that is a
// misconfiguration nobody chose — the one silence this disclosure exists to
// remove. Said here rather than by giving every library caller a non-nil scan.
func mappingOptions(w io.Writer, noAgentMapping, noSkillMapping bool) []coordinator.Option {
	var opts []coordinator.Option
	if noAgentMapping {
		opts = append(opts, coordinator.WithoutAgentMapping())
	} else {
		opts = append(opts, coordinator.WithAgentDirs(coordinator.DefaultAgentDirs()...))
	}
	if noSkillMapping {
		opts = append(opts, coordinator.WithoutSkillMapping())
	} else {
		skillDirs := coordinator.DefaultSkillDirs()
		if len(skillDirs) == 0 {
			fmt.Fprint(w,
				"skill mapping is on, but no skill directory could be resolved (your home directory is\n"+
					"unset), so nothing will be scanned and no skill can map. Set HOME, or pass\n"+
					"--no-skill-mapping to say so on purpose.\n",
			)
		}
		opts = append(opts, coordinator.WithSkillDirs(skillDirs...))
	}
	return opts
}

// goalCycleOptions is the goal-iteration surface planAndExecute receives from
// its caller: `auto` forwards its --max-cycles/--max-goal-budget-usd flags,
// chat always passes singleCycle. Chat staying single-cycle in v1 (ADR 0011
// §1) is enforced by what its caller can pass — commonRunFlags carry no cycle
// count — not by a runtime check.
type goalCycleOptions struct {
	maxCycles        int
	maxGoalBudgetUSD float64
}

// singleCycle is the non-iterating goalCycleOptions: one plan, one run, no
// assessment — today's behaviour, byte-identical (ADR 0011 §1).
var singleCycle = goalCycleOptions{maxCycles: 1}

// planAndExecute is one goal's full auto sequence — plan, save the spec, print
// the topology, execute. It is shared verbatim by `auto` and a chat graph turn
// so the sequence that must stay identical between them has exactly one home:
// a graph started from chat is indistinguishable on disk (saved spec,
// state.json, events.jsonl) from one started at the shell. confirm is the one
// permitted divergence: nil proceeds straight to execution (`auto` stays fully
// non-interactive), while a non-nil hook is asked between printing the
// topology and executing — false discards the plan with a note, which is not
// an error. A hook error aborts before execution and propagates as-is, so a
// caller can recognize its own sentinel (chat's EOF-at-the-prompt). web is
// the run's live-view opener or nil for none (see executeGraph); `auto`
// passes its TTY-gated decision, chat always passes nil — a chat turn's run
// stays un-wired (ADR 0006).
//
// planOnly is `auto --plan-only`: the same sequence up to and including the
// topology print, then a stop — nothing is wired, no node runs. It sits HERE,
// as an early return inside the one sequence, rather than in a parallel
// plan-and-print function, because the flag's entire value is that what it
// shows is what a real run would show; a second implementation of the print
// would be free to drift from this one and would take a mapping line with it
// when it did. The planner call above it is NOT skipped and cannot be: unlike
// `run --dry-run`, which reads a file, there is no plan to inspect until one
// has been bought. The saved spec survives the stop for the same reason —
// it was paid for — but it is saved under plans/, not runs/ (see the branch
// below), because nothing about it ran.
//
// cycles decides whether the sequence runs once (maxCycles 1 — this function
// body, exactly today's) or as the bounded goal loop of ADR 0011 (maxCycles
// ≥ 2 — planAndExecuteCycles, which re-enters coordinator.Plan per cycle and
// runs this same save→print→confirm→execute sequence as the loop's
// ExecuteCycle callback). It is an explicit parameter of the call so that
// planAndExecute stays the sequence's one home for both shapes.
func planAndExecute(ctx context.Context, out io.Writer, coord *coordinator.Coordinator, nodeRunner runner.NodeRunner, flags commonRunFlags, goal string, cycles goalCycleOptions, planOnly bool, confirm func() (bool, error), web browser.Opener) error {
	if cycles.maxCycles > 1 {
		if planOnly {
			// Unreachable from the CLI — autoFlags.parse rejects the
			// combination — and loud rather than silent so that a future
			// caller cannot get an executing run out of a flag that promises
			// not to execute.
			return fmt.Errorf("plan-only is not defined for a %d-cycle goal loop: only cycle 1 can be planned ahead of any execution", cycles.maxCycles)
		}
		return planAndExecuteCycles(ctx, out, coord, nodeRunner, flags, goal, cycles, confirm, web)
	}

	fmt.Fprintf(out, "Planning a graph for goal %q...\n", goal)
	plan, err := coord.Plan(ctx, goal, inputKeys(flags.inputs))
	if err != nil {
		return noteRejectedPlan(out, newRunID(), err)
	}

	runID := newRunID()
	// A plan-only preview keeps its paid-for spec OUTSIDE runs/. A directory
	// under runs/ holding a graph.json and nothing else is, to every reader of
	// that tree, a run whose state.json never appeared: `runs list` reports it
	// through the same WARNING+skip channel as a corrupt snapshot, and the
	// `serve` dashboard enumerates the same tree, so the preview would show up
	// as a card for a run that never ran. A preview never ran, so it is not a
	// run.
	specDir := runDirFor(runID)
	if planOnly {
		specDir = planDirFor(runID)
	}
	specPath, err := saveGeneratedSpec(specDir, plan.Spec)
	if err != nil {
		return err
	}
	printPlan(out, plan, specPath)

	if planOnly {
		fmt.Fprintf(out,
			"plan only: no node was executed. The %s still paid for ($%.4f) —\n"+
				"unlike `run --dry-run`, this is not free — and its plan is kept at %s.\n"+
				"Nothing ran, so this is not a run: it gets no run directory and `runs list` stays silent\n"+
				"about it. Run it with `oh-my-graph run %s`.\n",
			plannerCallsPhrase(plan), plan.CostUSD, specPath, specPath)
		return nil
	}

	if confirm != nil {
		ok, err := confirm()
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(out, "plan discarded.")
			return nil
		}
	}

	return executePlan(ctx, runID, plan, nodeRunner, flags, specPath, web, nil)
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
// to (see buildRecorder). goal is the run's goal-lineage block for an
// iterated auto cycle (ADR 0011 §4), nil on every single-cycle run.
func executePlan(ctx context.Context, runID string, plan coordinator.Plan, nodeRunner runner.NodeRunner, flags commonRunFlags, specPath string, web browser.Opener, goal *runstate.GoalRef) error {
	// false: a planned graph never resolved a fragment — the coordinator
	// refuses planner-emitted use:/with: (ADR 0013), so plan.Spec is
	// fragment-free by construction and stays reusable verbatim.
	return executeGraph(ctx, runID, plan.Graph, nodeRunner, flags, plan.ToolPolicies, plan.CostUSD, specPath, plan.Spec, false, web, goal)
}

// executeGraph wires the per-run collaborators (Handoff, RunLedger, Scheduler)
// around an already-validated graph and runs it — the shared back half of both
// `run` and `auto`. This is where the engine's exec seams are injected: the
// ClaudeCLIRunner the caller passed (a node's claude subprocess), a
// ShellVerifier (a node's success_check.verify command), and a
// worktree.GitManager (a node's managed `worktree:` checkout) — three of the
// program's four seams; the fourth arrives already injected as web. A planned
// graph can declare neither a verification nor a worktree — the coordinator
// rejects both fields — so for `auto` those two are wired but never reached. toolPolicies is the per-node
// execution ceiling: auto passes the coordinator's, `run` passes nil.
// planningCostUSD is the coordinator's one planning-call cost, folded into the
// ledger's total so an auto run's end-of-run TOTAL COST is honest about the
// planning step; `run` passes 0 (no planning step), so it shows no planning
// line and its total is unchanged. graphSourcePath and rawSource are the
// snapshot's GraphSourcePath/GraphSHA256 material — the .yaml file (and its
// bytes) for `run`, the saved graph.json (and the planner's JSON bytes) for
// `auto`. fragmentsResolved says whether any node of g resolved a `use:`
// fragment on the way in (ADR 0013): when true the snapshot must store the
// re-encoded resolved graph, never rawSource verbatim (see newRunRecorder);
// `auto` passes false by construction — the coordinator refuses
// planner-emitted fragments. web, when non-nil, is the Opener the run's embedded live view
// hands its URL to (browser.ExecOpener behind the fourth exec seam, ADR
// 0006); nil means no live view at all — the gate (TTY-and-not---no-web for
// run/auto, and for `resume` through the same webOpener; always nil for a
// chat turn) is the caller's decision, made before this function so nothing
// here ever probes a terminal. goal is the goal-lineage block an iterated
// auto cycle stamps into its snapshot (ADR 0011 §4); nil — the only value
// `run` and single-cycle auto ever pass — keeps the snapshot byte-identical
// to today's.
func executeGraph(ctx context.Context, runID string, g *graph.Graph, nodeRunner runner.NodeRunner, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, planningCostUSD float64, graphSourcePath string, rawSource []byte, fragmentsResolved bool, web browser.Opener, goal *runstate.GoalRef) error {
	// The first leg holds the run's resume.lock for its whole duration — the
	// same flock every `resume` takes (internal/runstate.AcquireLock).
	// Without it, a `resume <run-id> --retry-failed` raced against a
	// still-in-flight first leg would open a second scheduler over the same
	// state.json/events.jsonl and double-spawn (double-bill) nodes; instead
	// the concurrent resume fails on this lock with its usual LockHeldError.
	// It is taken here, before the event stream below, and released by a defer
	// registered before the stream's: the ordering invariant a reader's
	// abandoned-run derivation rests on (acquireRunLock, ADR 0015 §2).
	release, err := acquireRunLock(filepath.Join(runDirFor(runID), lockFileName))
	if err != nil {
		return err
	}
	defer release()

	h := handoff.New(runDirFor(runID), flags.inputs)
	led := ledger.New(runID)
	led.RecordPlanningCost(planningCostUSD)

	recorder, err := newRunRecorder(runID, graphSourcePath, rawSource, g, fragmentsResolved, flags, toolPolicies, goal)
	if err != nil {
		return fmt.Errorf("prepare run snapshot: %w", err)
	}
	// The initial write makes state.json exist from the run's first moment,
	// not only after a node settles — the difference between a first-node
	// session-limit pause being resumable and it leaving no snapshot at all
	// (runstate.SnapshotRecorder.WriteInitial, ADR 0009).
	if err := recorder.WriteInitial(); err != nil {
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

	// The worktree manager is per-run: nodes declaring `worktree:` get their
	// managed checkouts under this run's directory, created off the invocation
	// repo's HEAD (repoDir "" = the process cwd), and whatever was created is
	// torn down after the run with committed work retained on its branch
	// (internal/worktree, ADR 0005). A graph with no worktree nodes never
	// spawns git at all.
	worktrees := worktreeManagerFor(runID)

	// The embedded live view (serve's own listener/handler/lifecycle on an
	// ephemeral port, plus one browser open) lives exactly as long as the
	// run: the deferred stop waits for the server to exit, after the ledger
	// print below, so the process never outlives it — and never exits while
	// it still holds the port.
	if web != nil {
		defer startLiveView(ctx, web, runID)()
	}

	// An auto run's injected evidence commands run one at a time (ADR 0016 §2).
	// The set is derived from the graph rather than carried from the Plan so a
	// resumed leg could ask the same question of the same material; the
	// discriminator is the tool ceiling, which is non-empty exactly for a
	// planned graph — a hand-written graph's own verifications are the user's
	// reviewed artifact and keep running concurrently.
	var serializedVerify map[string]bool
	if len(toolPolicies) > 0 {
		serializedVerify = coordinator.InjectedVerifyNodes(g)
	}

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:           flags.concurrency,
		ContinueOnFail:        flags.continueOnFail,
		Gate:                  gate.NewPauseController(),
		Verifier:              verify.NewShellVerifier(),
		Worktrees:             worktrees,
		ToolPolicies:          toolPolicies,
		SerializedVerifyNodes: serializedVerify,
		Recorder:              recorder,
		EventSink:             feed,
	})

	fmt.Fprintf(os.Stdout, "Running graph %q (run %s)\n\n", g.Name, runID)
	runErr := scheduler.Run(ctx, g, h, led)
	// Cleanup runs on a fresh context: the run's own may already be cancelled
	// (halt-on-fail, Ctrl-C), and a halted run is exactly when leftover
	// worktrees still need tearing down.
	reportWorktreeCleanup(os.Stderr, worktrees.Cleanup(context.Background()))

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
//
// fragmentsResolved forces the re-encode regardless of rawSource's shape
// (ADR 0013): JSON is valid YAML, so a JSON-authored entry file may carry
// `use:` nodes, and reusing those bytes verbatim would snapshot an UNRESOLVED
// graph — `resume`'s graph.Parse(snap.Graph) would then die on the
// unresolved-fragment backstop. The rawSource-verbatim shortcut is only legal
// for a document resolution never touched; with that, resume works on
// fragment-free material by construction.
func newRunRecorder(runID, graphSourcePath string, rawSource []byte, g *graph.Graph, fragmentsResolved bool, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, goal *runstate.GoalRef) (*runstate.SnapshotRecorder, error) {
	graphJSON := rawSource
	if fragmentsResolved || !json.Valid(rawSource) {
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
		Goal:            goal,
	}
	return runstate.NewSnapshotRecorder(statePath, base), nil
}

// saveGeneratedSpec persists the planner's JSON spec into dir — the run's own
// directory for a run, plans/<id> for a `--plan-only` preview — so an auto plan
// stays inspectable and repeatable: JSON is valid YAML, so the saved file can
// be hand-edited and re-run directly with `oh-my-graph run <path>` — it is
// indented before writing so that editing is practical.
//
// It is written owner-only (0o700 / 0o600) because a spec is not merely a
// topology: skill mapping inlines the body of a local SKILL.md into the node
// prompts it carries, so the saved file can hold the user's own private
// instructions verbatim.
func saveGeneratedSpec(dir string, spec []byte) (string, error) {
	return saveSpecAs(dir, generatedSpecFileName, spec)
}

// generatedSpecFileName is the accepted plan's file — the one every consumer
// of a run directory already reads.
const generatedSpecFileName = "graph.json"

// rejectedSpecFileName is a REFUSED plan's file. A distinct name, in a
// plans/<id> directory of its own, because it is not a graph the engine would
// run: nothing may mistake it for one, least of all a reader walking the tree
// for graph.json.
const rejectedSpecFileName = "rejected.json"

// saveSpecAs writes one planner spec into dir under name, with the indentation
// and the owner-only permissions saveGeneratedSpec documents. Shared so an
// accepted plan and a rejected one cannot be persisted under two different
// permission stances.
func saveSpecAs(dir, name string, spec []byte) (string, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create spec dir %q: %w", dir, err)
	}
	var indented bytes.Buffer
	if err := json.Indent(&indented, spec, "", "  "); err == nil {
		spec = indented.Bytes()
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, spec, 0o600); err != nil {
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
// prints both halves. noteVerifyAttachments adds the one thing on this screen
// the ceiling does NOT cover: the user's own --verify-cmd, which the engine
// runs at each sink outside every layer of it (ADR 0016 §2).
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
		if node.Agent != "" {
			line += " [agent: " + node.Agent + "]"
		}
		fmt.Fprintln(w, line)
	}
	noteAgentMappings(w, plan.AgentMappings)
	noteSkillMappings(w, plan.SkillScan, plan.SkillMappings)
	noteVerifyAttachments(w, plan.VerifyAttachments)
	noteReplan(w, plan.Repaired)
	noteCeiling(w)
	fmt.Fprintln(w)
}

// noteReplan discloses that this plan was bought twice: the first planner
// reply was refused by validation, its refusals were handed back, and the
// corrected reply is what is printed above. Silence means the ordinary
// first-try plan.
//
// It is printed for the same reason the mappings are: the price above is
// already the sum of both calls, so without this line a repaired plan is
// indistinguishable from an expensive one — and nobody could measure whether
// a planner-prompt change reduced the refusal rate, because the refusals
// would have stopped being visible.
func noteReplan(w io.Writer, repair *coordinator.PlanRepair) {
	if repair == nil {
		return
	}
	fmt.Fprintf(w, "  re-planned: the first reply was refused by validation (cost $%.4f, included above) and a corrected one was requested:\n", repair.RejectedCostUSD)
	for _, issue := range repair.Issues {
		fmt.Fprintf(w, "    ! %s\n", issue)
	}
}

// plannerCallsPhrase names how many planner calls a plan-only preview paid
// for, so the "was still paid for" sentence cannot claim one call when a
// bounded re-plan bought two.
func plannerCallsPhrase(plan coordinator.Plan) string {
	if plan.Repaired == nil {
		return "planner call above was"
	}
	return "2 planner calls above (the refused first reply and its correction) were"
}

// noteRejectedPlan persists a refused plan's spec before returning the
// refusal. A planner call is paid for whether or not its graph loads, and
// until now a rejected one left NOTHING on disk: the user paid, saw an error,
// and had no artifact to hand-edit or re-run. The spec is written beside a
// plan-only preview's, under plans/, for the same reason that one is not under
// runs/ — nothing ran, so it is not a run.
//
// planID names the directory it goes into, so a caller that knows more about
// where the refusal came from than "a plan was bought" can say so in the path
// (the goal loop names the goal's first run and the refused cycle).
//
// The refusal itself is returned unchanged; this only adds a line naming where
// the rejected spec went. A failure to write it is reported and swallowed:
// losing the artifact must not replace the diagnosis the user actually needs.
func noteRejectedPlan(w io.Writer, planID string, err error) error {
	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) {
		return err
	}
	if rejection.CostUSD > 0 {
		fmt.Fprintf(w, "planning failed after spending $%.4f — a planner call is paid for whether or not its graph loads.\n", rejection.CostUSD)
	}
	if len(rejection.Spec) == 0 {
		return err
	}
	path, saveErr := saveSpecAs(planDirFor(planID), rejectedSpecFileName, rejection.Spec)
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "save rejected plan spec: %v\n", saveErr)
		return err
	}
	fmt.Fprintf(w, "The rejected spec is kept at %s — fix it by hand and run it with `oh-my-graph run %s`.\n", path, path)
	return err
}

// noteAgentMappings discloses every subagent auto-mapping decision before the
// run starts: skipped candidates with their reason, and — when any mapping
// applied — the isolation trade a mapped node makes (agent resolution needs
// the user's settings loaded, so such a node gives up the "no settings load"
// half of the ceiling; the declared tool list still binds). The mappings
// themselves are already visible as [agent: ...] on the node lines above;
// silence here means no candidate matched and nothing changed.
func noteAgentMappings(w io.Writer, mappings []coordinator.AgentMapping) {
	applied := false
	for _, m := range mappings {
		if m.SkippedReason != "" {
			fmt.Fprintf(w, "  ! agent %q not applied to %s: %s\n", m.Agent, m.NodeID, m.SkippedReason)
			continue
		}
		applied = true
	}
	if applied {
		fmt.Fprint(w,
			"  Nodes marked [agent: ...] were auto-mapped onto your own Claude Code agents; they load\n"+
				"  your settings so the agent can resolve (their declared tool list still binds).\n"+
				"  Pass --no-agent-mapping to turn this off.\n",
		)
	}
}

// noteSkillMappings discloses every skill auto-mapping decision before the run
// starts, one line per decision (ADR 0012 §6): a mapping's name, inlined size,
// hash prefix, target node and description — the printed hash is the integrity
// link to the full inlined text in the saved spec file, which printPlan already
// names — and a refused candidate's reason (oversize body, agent-mapped node).
//
// The decisions are bracketed by the SCAN they came out of, because the
// decision list alone cannot say why it is empty. No-match is the majority
// outcome of a name-only rule (measured: 22 of the 32 shipped node ids), so an
// empty list used to print nothing at all and read identically to "skill
// mapping never ran" — and identically, too, to the case a user actually hits:
// a corpus that lives somewhere this scan deliberately does not go. Naming the
// scanned directories and the count turns "I have skills but see none" into
// one readable line, and the exclusion note states the limit instead of
// letting it look like a match failure. A shadowed definition gets its own
// line for the same reason: the count above it is the size of the deduped set,
// so a collision quietly lowers it, and nothing else would ever name the file
// that lost. scan is nil only when no scan happened (--no-skill-mapping, or no
// configured directories), and then this prints nothing: the user who turned
// it off does not need to be told twice — and the one case where nobody turned
// it off, an unresolvable home, is disclosed by mappingOptions instead.
func noteSkillMappings(w io.Writer, scan *coordinator.SkillScan, mappings []coordinator.SkillMapping) {
	if scan == nil {
		return
	}
	fmt.Fprintf(w, "  skill scan: %d skill(s) from %s\n", scan.Found, strings.Join(scan.Dirs, ", "))
	for _, path := range scan.Shadowed {
		fmt.Fprintf(w, "  skill shadowed: %s — another definition declares the same name and wins\n", path)
	}
	applied := false
	for _, m := range mappings {
		if m.SkippedReason != "" {
			fmt.Fprintf(w, "  skill skipped: %s -> %s: %s\n", m.Skill, m.NodeID, m.SkippedReason)
			continue
		}
		fmt.Fprintf(w, "  skill mapped: %s (%.1f KiB, sha256:%.12s…) -> %s — %q\n",
			m.Skill, float64(m.InlinedBytes)/1024, m.SHA256, m.NodeID, m.Description)
		applied = true
	}
	if applied {
		fmt.Fprint(w,
			"  Nodes marked above got that skill's SKILL.md body appended to their prompt (full text\n"+
				"  in the saved spec file). Pass --no-skill-mapping to turn this off.\n",
		)
	}
	fmt.Fprint(w,
		"  Not scanned: plugin-provided skills (~/.claude/plugins) and project skills (./.claude/skills).\n"+
			"  Both are out of scope in v1 (ADR 0012), so a skill you installed through a plugin maps\n"+
			"  nothing here — that is a stated limit, not a failed match.\n",
	)
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

// printFragmentResolutions prints one line per resolved `use:` naming the
// fragment source file, what that fragment says it is, and every key the using
// node overrides — the ADR 0013 disclosure posture, so a hollowed-out
// success_check or a widened allowed_tools is announced at every run, not only
// visible to whoever reads the file. The description is what makes the line
// readable without opening the fragment, which is why an empty one is a load
// error rather than an empty tail here. Silent for a fragment-free graph.
func printFragmentResolutions(w io.Writer, resolutions []graph.FragmentResolution) {
	for _, r := range resolutions {
		line := fmt.Sprintf("fragment: node %q spliced from %q (%s) — %s", r.NodeID, r.Fragment, r.Source, r.Description)
		if len(r.Overridden) > 0 {
			line += " — node overrides: " + strings.Join(r.Overridden, ", ")
		}
		fmt.Fprintln(w, line)
	}
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

// omgHome resolves the tool's per-user home directory — the single base every
// run artifact lives under, regardless of which directory oh-my-graph was
// invoked from. An OMG_HOME environment override wins (tests point it at a
// temp dir); otherwise it is ~/.oh-my-graph. If the user's home directory
// cannot be resolved, fall back to a cwd-relative .oh-my-graph so the tool
// still works in home-less environments (minimal containers).
func omgHome() string {
	if dir := os.Getenv("OMG_HOME"); dir != "" {
		return dir
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".oh-my-graph"
	}
	return filepath.Join(home, ".oh-my-graph")
}

// runsRoot is the on-disk home of every run directory — what `runs list`
// enumerates.
func runsRoot() string {
	return filepath.Join(omgHome(), "runs")
}

// runDirFor is the on-disk home of one run's artifacts and (for auto runs) its
// generated graph spec.
func runDirFor(runID string) string {
	return filepath.Join(runsRoot(), runID)
}

// planDirFor is where a plan that was bought but never executed keeps its spec
// — `auto --plan-only`. It is deliberately NOT under runsRoot(): `runs list`
// and the `serve` dashboard both enumerate that tree and read a directory with
// no state.json as a broken run, so a preview left there would be reported as
// damage — and, being the newest directory, would sit at the top of both
// listings. Its own tree keeps the two kinds of artifact — what ran, and what
// was only planned — apart for every reader, without any of them needing a
// special case.
func planDirFor(planID string) string {
	return filepath.Join(omgHome(), "plans", planID)
}

// worktreeManagerFor builds one run's worktree manager: checkouts created off
// the invocation repo (repoDir "" = the process cwd) into the run directory's
// worktrees/ area — never the user's checked-out tree. Shared by `run`/`auto`
// (executeGraph) and `resume` so the two legs manage the same location.
func worktreeManagerFor(runID string) *worktree.GitManager {
	return worktree.NewGitManager("", filepath.Join(runDirFor(runID), "worktrees"), runID)
}

// reportWorktreeCleanup prints one line per worktree-cleanup note — a branch
// retained because it carries commits, a worktree kept because it holds
// uncommitted changes, or a teardown failure. Silent when there is nothing to
// say, which is the common case (no worktree nodes, or clean removals).
func reportWorktreeCleanup(w io.Writer, notes []string) {
	for _, note := range notes {
		fmt.Fprintf(w, "worktree cleanup: %s\n", note)
	}
}

// runIDSeq distinguishes run ids minted in the same instant by one process;
// the nanosecond timestamp distinguishes concurrent processes.
var runIDSeq atomic.Uint64

// newRunID is a filesystem-safe, sortable run id: a UTC timestamp to the
// nanosecond plus a per-process sequence number. One run directory per
// invocation keeps a run's artifacts inspectable; second resolution alone let
// two runs started in the same second share (and clobber) one run directory.
func newRunID() string {
	return fmt.Sprintf("%s-%d", time.Now().UTC().Format("20060102-150405.000000000"), runIDSeq.Add(1))
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

// printPauseHint prints the exact resume commands when runErr is one of the
// two pause shapes — a *schedule.PausedError (the gate lifecycle's "print the
// exact resume command" step — DESIGN.md, "Gate nodes and resume") or a
// *schedule.LimitPausedError (ADR 0009: the hint carries the CLI's own
// "resets <time>" when the captured cause yields one, and drops the time
// rather than inventing one when it doesn't) — and is a silent no-op for any
// other outcome (success or failure), so it is safe to call unconditionally
// after every run.
func printPauseHint(w io.Writer, runID string, runErr error) {
	var paused *schedule.PausedError
	if errors.As(runErr, &paused) {
		fmt.Fprintf(w, "\nPaused at gate %q. Resume with:\n  oh-my-graph resume %s --approve %s\n  oh-my-graph resume %s --reject %s\n",
			paused.GateID, runID, paused.GateID, runID, paused.GateID)
		return
	}
	var limited *schedule.LimitPausedError
	if !errors.As(runErr, &limited) {
		return
	}
	if reset := runner.SessionLimitReset(limited.Cause); reset != "" {
		fmt.Fprintf(w, "\nSession limit reached (resets %s). Resume after %s with:\n  oh-my-graph resume %s --retry-failed\n",
			reset, reset, runID)
		return
	}
	fmt.Fprintf(w, "\nSession limit reached. Resume with:\n  oh-my-graph resume %s --retry-failed\n", runID)
}
