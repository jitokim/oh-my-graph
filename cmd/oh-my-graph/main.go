// Command oh-my-graph runs a YAML-defined DAG through a selected model CLI on
// the user's existing subscription. It wires the concrete collaborators together —
// the real CLIRunner, a per-run Handoff and RunLedger — and hands them to
// the Scheduler by constructor injection; there are no globals.
//
// Global runtime selection precedes the subcommand:
//
//	oh-my-graph [--runtime claude|codex] <command> ...
//
// Usage (the same command list run() prints; usage_test.go derives both from the
// dispatch switch and every subcommand's FlagSet, so neither can drift):
//
//	oh-my-graph init [dir]
//	oh-my-graph run <graph.yaml> [--dry-run] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]
//	oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--accept-no-build-evidence] [--accept-loaded-user-config] [--max-cycles N] [--max-goal-budget-usd X] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web] [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
//	oh-my-graph lint <graph.yaml>
//	oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--verify-cmd 'CMD'] [--verify-timeout D] [--concurrency N] [--no-web] [--no-skill-activation]
//	oh-my-graph runs list [--show-skipped] [--exit-in-flight]
//	oh-my-graph show <run-id>
//	oh-my-graph watch <run-id>
//	oh-my-graph serve [<run-id>] [--port N] [--no-open]   (no run id: the dashboard over every run)
//	oh-my-graph chat [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
//	oh-my-graph version
//
// Exit codes: 0 every node passed, 1 the run failed, 2 the run paused and is
// resumable — at a gate awaiting a human decision (ADR 0003) or because the
// subscription's session limit was hit (ADR 0009), 3 `auto` refused to start
// because the directory has a build system and the run would have checked none
// of it (ADR 0030), 4 `runs list --exit-in-flight` found at least one listed run
// still PLANNING or RUNNING. A pause is not a failure, and a refusal is neither:
// nothing ran, nothing is resumable, nothing was billed. Nor is 4: nothing ran
// there either — the command was asked a question about other runs and answered
// it, which is why it gets a code a supervisor loop can wait on
// (`until oh-my-graph runs list --exit-in-flight >/dev/null; do sleep 30; done`)
// instead of grepping a table that is not a contract.
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
	"github.com/jitokim/oh-my-graph/internal/usermodel"
	"github.com/jitokim/oh-my-graph/internal/verify"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

func main() {
	os.Exit(mainExitCode(os.Args[1:]))
}

// mainExitCode runs the CLI and returns the process exit code: 0 (every node
// passed), 1 (the run failed — printed to stderr), 2 (the run paused — at
// a gate or on a session limit — and is resumable; the resume hint was
// already printed to stdout by executeGraph/runResume, so this path prints
// nothing further), 3 (`auto` refused for want of build evidence — printed
// here, to stdout, by the error itself), or 4 (`runs list --exit-in-flight`
// found a run still in flight; it prints nothing beyond the table it already
// printed, because the code is the answer). Separated from main so the exit path
// lives in exactly one place and the mapping itself is testable without calling
// os.Exit.
func mainExitCode(args []string) int {
	err := run(args)
	// A help request is not one of the three outcomes above: it answers on
	// stdout, like every other report this CLI prints, and exits 0. It reaches
	// here as an error only because that is the return path subcommands have
	// (usageRequest, argslot.go).
	var usage *usageRequest
	if errors.As(err, &usage) {
		usage.print(os.Stdout)
		return 0
	}
	// The build-evidence refusal takes the same shape and for the same reason: it
	// is twenty lines of indented prose that replace a notice printed to STDOUT,
	// and routing it through the stderr `oh-my-graph: %v` line below would
	// double-prefix its first sentence and put the rest on the wrong channel
	// (ADR 0030 §2.4).
	var missingEvidence *coordinator.MissingBuildEvidenceError
	if errors.As(err, &missingEvidence) {
		missingEvidence.Print(os.Stdout)
		return exitCodeForError(err)
	}
	code := exitCodeForError(err)
	if code == 1 {
		fmt.Fprintf(os.Stderr, "oh-my-graph: %v\n", err)
	}
	return code
}

// exitCodeForError is the mapping itself, separated from mainExitCode so it can
// be judged directly against a run's derived STATUS — ADR 0023 §2.6 asserts
// their agreement for a single-run invocation rather than assuming it, and an
// assertion that had to go through run() and os.Args could not be written.
//
// The distinction the ADR draws, and the reason the agreement is narrow: an
// exit code is the WRITER's answer about the process it just ran, and a status
// is a READER's answer about a directory on disk. They are computed from
// different facts. Where they can be made to agree the implementation asserts
// it; for the goal loop they cannot even in principle, because the exit code is
// goal-level (ADR 0011 §2) and one invocation makes N run directories against
// one code.
func exitCodeForError(err error) int {
	if err == nil {
		return 0
	}
	// Asked for and answered: `resume --help` is a successful invocation, not a
	// failed one (#200).
	var usage *usageRequest
	if errors.As(err, &usage) {
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
	// A refusal is neither a failed run nor a paused one: nothing ran, nothing is
	// resumable, nothing was billed. It gets its own code so a script can tell
	// "add a flag" from "the build failed" — under exit 1 a refused invocation
	// would be indistinguishable from the failing build the operator is trying to
	// catch (ADR 0030 §2.4). It does not disturb ADR 0023 §2.6's exit-code /
	// run-status agreement either: a refused invocation creates no run directory,
	// so it is outside that assertion rather than a new case within it.
	var missingEvidence *coordinator.MissingBuildEvidenceError
	if errors.As(err, &missingEvidence) {
		return 3
	}
	// The one code that is not about the invocation's own work: `runs list
	// --exit-in-flight` asked whether anything is still running and the answer
	// was yes (runsInFlightError). A question answered is not a failure — the
	// table printed and every readable directory was read — so it must not be
	// exit 1, where a supervisor loop could not tell it from a corpus it could
	// not read. That distinction is the whole point: exit 1 means "ask again",
	// exit 4 means "not yet", exit 0 means "nothing is in flight". It also stays
	// outside ADR 0023 §2.6's exit-code/run-status agreement, which is about the
	// run an invocation just executed; this invocation executes none.
	var inFlight *runsInFlightError
	if errors.As(err, &inFlight) {
		return 4
	}
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
       oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--accept-no-build-evidence] [--accept-loaded-user-config] [--max-cycles N] [--max-goal-budget-usd X] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web] [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
       oh-my-graph lint <graph.yaml>
       oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--verify-cmd 'CMD'] [--verify-timeout D] [--concurrency N] [--no-web] [--no-skill-activation]
       oh-my-graph runs list [--show-skipped] [--exit-in-flight]
       oh-my-graph show <run-id>
       oh-my-graph watch <run-id>
       oh-my-graph serve [<run-id>] [--port N] [--no-open]   (no run id: the dashboard over every run)
       oh-my-graph chat [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
       oh-my-graph version`

const runtimeUsage = "oh-my-graph [--runtime claude|codex] <command> ..."

// run parses argv and dispatches to the subcommand. It returns an error rather
// than exiting so the exit path lives in exactly one place (mainExitCode).
func run(args []string) error {
	runtime, runtimeSet, args, err := parseCommandLine(args)
	if err != nil {
		return err
	}
	if len(args) == 0 {
		return errors.New("usage: " + runtimeUsage + "\ncommands:\n       " + usageLines)
	}
	switch args[0] {
	case "init":
		if runtimeSet {
			return errors.New("--runtime applies to run, auto, lint, resume, serve, and chat")
		}
		return runInit(args[1:])
	case "run":
		return runGraphRuntime(runtime, args[1:])
	case "auto":
		return runAutoRuntime(runtime, args[1:])
	case "lint":
		return runLintRuntime(runtime, args[1:])
	case "resume":
		return runResumeRuntime(runtime, runtimeSet, args[1:])
	case "runs":
		if runtimeSet {
			return errors.New("--runtime applies to run, auto, lint, resume, serve, and chat")
		}
		return runRuns(args[1:])
	case "show":
		if runtimeSet {
			return errors.New("--runtime applies to run, auto, lint, resume, serve, and chat")
		}
		return runShow(args[1:])
	case "watch":
		if runtimeSet {
			return errors.New("--runtime applies to run, auto, lint, resume, serve, and chat")
		}
		return runWatch(args[1:])
	case "serve":
		return runServeRuntime(runtime, runtimeSet, args[1:])
	case "chat":
		return runChatRuntime(runtime, args[1:])
	case "version":
		if runtimeSet {
			return errors.New("--runtime applies to run, auto, lint, resume, serve, and chat")
		}
		printVersion(os.Stdout)
		return nil
	default:
		return fmt.Errorf("unknown command %q (want init, run, auto, lint, resume, runs, show, watch, serve, chat, or version)", args[0])
	}
}

// parseCommandLine consumes global flags before the subcommand. Runtime is a
// run-wide choice, so it lives here rather than being repeated on every
// subcommand FlagSet; flags after the subcommand remain that command's argv.
func parseCommandLine(args []string) (runner.Runtime, bool, []string, error) {
	runtime := runner.RuntimeClaude
	seenRuntime := false
	for len(args) > 0 && strings.HasPrefix(args[0], "--") {
		arg := args[0]
		var value string
		switch {
		case arg == "--runtime":
			if len(args) < 2 {
				return "", false, nil, errors.New("--runtime needs a value (want claude or codex)")
			}
			value = args[1]
			args = args[2:]
		case strings.HasPrefix(arg, "--runtime="):
			value = strings.TrimPrefix(arg, "--runtime=")
			args = args[1:]
		default:
			return runtime, seenRuntime, args, nil
		}
		if seenRuntime {
			return "", false, nil, errors.New("--runtime may be specified only once")
		}
		if value == "" {
			return "", false, nil, errors.New("--runtime needs a value (want claude or codex)")
		}
		parsed, err := runner.ParseRuntime(value)
		if err != nil {
			return "", false, nil, err
		}
		runtime = parsed
		seenRuntime = true
	}
	return runtime, seenRuntime, args, nil
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

// agentNameFlag collects repeated --no-agent <name> values in the order they
// were typed. A blank name is rejected rather than collected: `--no-agent ""`
// is a typo that would otherwise decline nothing while reading like an opt-out
// that took.
type agentNameFlag []string

func (f *agentNameFlag) String() string { return strings.Join(*f, ",") }

func (f *agentNameFlag) Set(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid --no-agent %q (want an agent name, as written in its frontmatter)", name)
	}
	*f = append(*f, name)
	return nil
}

// runGraph is the `run` subcommand: load and validate the graph, warn about any
// dangerous per-node opt-ins, wire the production collaborators, and execute.
// With --dry-run it stops after validation and the plan print — nothing is
// wired and no node runs.
func runGraph(args []string) error {
	return runGraphRuntime(runner.RuntimeClaude, args)
}

func runGraphRuntime(runtime runner.Runtime, args []string) error {
	// One of the four sites (with runAuto, runResume and runServe) injecting
	// the real browser launcher (browser.ExecOpener, the fourth exec seam —
	// ADR 0006); everywhere else the Opener stays refusing or absent.
	// webOpener still gates whether it is ever used.
	return runGraphWithRuntime(runtime, args, runner.NewCLIRunner(runtime), browser.NewExecOpener(), os.Stdout)
}

// runGraphWith is runGraph with its seams injectable: the runner (so a test
// can prove --dry-run never reaches it — a FakeRunner must see zero
// invocations) and the live view's opener plus the stdout whose TTY-ness
// gates it (so a test can prove a terminal run opens the view and a
// non-terminal run changes nothing, with a FakeOpener and no real spawn).
func runGraphWith(args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	return runGraphWithRuntime(runner.RuntimeClaude, args, nodeRunner, opener, stdout)
}

func runGraphWithRuntime(runtime runner.Runtime, args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	flags := newRunFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	if flags.dryRun {
		return dryRunGraphForRuntime(os.Stdout, os.Stderr, flags.graphPath, flags.inputs, runtime)
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
	runtimeWarnings, err := runner.ValidateGraphForRuntime(runtime, g)
	// Part of the pre-run disclosure, so it prints where the rest of the Codex
	// policy prints and not on a stream the reader may not be watching. The
	// identical list is produced again inside executeGraph, which is the gate
	// `auto` and a chat-started graph reach WITHOUT passing here; runtimeWarnW
	// below tells that call not to print this run's copy twice.
	warnRuntimePreflight(stdout, flags.graphPath, runtimeWarnings)
	if err != nil {
		return err
	}
	noteCodexRuntimePolicy(stdout, runtime, g, handWrittenNodes)
	flags.runtime = runtime
	flags.runtimeWarnW = io.Discard
	printFragmentResolutions(stdout, loaded.Resolutions)
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
	// The missing-CLI preflight, on the last line before the run costs anything:
	// executeGraph opens the run leg, which creates the run directory, and the
	// node that would otherwise discover this discovers it as a spawn failure
	// with the scheduler already running.
	//
	// LAST, not first. Everything above it — the YAML load, DAG validation, the
	// runtime check, the bypassPermissions warning — is a verdict about the
	// graph, and a graph verdict must not be displaced by a verdict about the
	// machine: a newcomer with a cycle in their YAML and no CLI installed still
	// needs to be told about the cycle. `--dry-run` returns further up for the
	// same reason from the other side: it spawns nothing, so it must keep
	// working on a machine with no model CLI at all.
	// …and only for a graph that will actually spawn one. A graph whose nodes
	// are all gates never touches the NodeRunner, so demanding a CLI from it is
	// a refusal about a capability the run does not use — which is how this
	// preflight broke TestMainExitCode_PauseMapsToExitCode2 on a machine
	// without the CLI installed (CI), while passing on one that has it.
	if graphSpawnsRuntime(loaded.Graph) {
		if err := runner.CheckRunnerCLI(nodeRunner); err != nil {
			return err
		}
	}
	// nil leg: `run` has no planning phase, so it opens its leg at executeGraph
	// exactly as it always has (ADR 0023 §2.2).
	return executeGraph(ctx, newRunID(), g, nodeRunner, flags.commonRunFlags, nil, 0, flags.graphPath, loaded.Source,
		len(loaded.Resolutions) > 0, webOpener(flags.noWeb, stdout, opener), nil, nil)
}

// runAuto is the `auto` subcommand — the zero-config path (hand-written YAML
// stays the precise-control path). The coordinator turns the goal into a graph
// spec via one planner call through the same env-scrubbed NodeRunner every
// node uses; the validated result is saved for inspection and then executed by
// the same scheduler as a hand-written graph. Generated graphs can never opt
// into bypassPermissions (the coordinator rejects them), so no warning pass is
// needed here.
func runAuto(args []string) error {
	return runAutoRuntime(runner.RuntimeClaude, args)
}

func runAutoRuntime(runtime runner.Runtime, args []string) error {
	// One of the four sites (with runGraph, runResume and runServe) injecting
	// the real browser launcher (browser.ExecOpener, the fourth exec seam —
	// ADR 0006).
	return runAutoWithRuntime(runtime, args, runner.NewCLIRunner(runtime), browser.NewExecOpener(), os.Stdout)
}

// runAutoWith is runAuto with its seams injectable, mirroring runGraphWith and
// for the same reason: --plan-only's whole claim is that no node runs, and the
// only way to prove that is through the real argv path with a FakeRunner that
// must see the planner call and nothing else. The stdout parameter is what
// gates the live view (a non-terminal one leaves it off), so a test needs no
// real spawn on that seam either.
func runAutoWith(args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	return runAutoWithRuntime(runner.RuntimeClaude, args, nodeRunner, opener, stdout)
}

func runAutoWithRuntime(runtime runner.Runtime, args []string, nodeRunner runner.NodeRunner, opener browser.Opener, stdout *os.File) error {
	flags := newAutoFlags()
	if err := flags.parse(args); err != nil {
		return err
	}
	if runtime == runner.RuntimeCodex && flags.maxGoalBudgetUSD > 0 {
		return errors.New("auto: --max-goal-budget-usd is unavailable with --runtime codex because Codex reports tokens, not USD")
	}
	flags.runtime = runtime

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// The build-evidence question, asked once, here — before the planner call and
	// therefore before any spend (ADR 0030 §2.1). A directory with a build system
	// and no --verify-cmd is REFUSED unless the operator declared the absence;
	// anything else proceeds and says out loud what it is not checking, so the
	// user can Ctrl-C and re-run with one flag instead of learning it beside a
	// finished ledger (ADR 0016 §3). "." is the invocation directory: the same
	// tree the planned nodes and the evidence command would both run in.
	//
	// Deliberately upstream of planAndExecute, which --plan-only is passed INTO:
	// that is what makes a preview refuse identically to the run it previews,
	// with no special case and no planner call bought.
	verifyCommand := flags.verifyCommand()
	evidence, err := answerBuildEvidence(os.Stdout, verifyCommand, flags.buildDeclaration(), ".")
	if err != nil {
		return err
	}
	flags.buildEvidence = evidence

	// The same preflight `run` does, and BELOW ADR 0030's gate on purpose. Both
	// refuse before anything spends, so neither is racing the other for the
	// user's money — what the order decides is which refusal a directory that
	// earns both gets, and the gate's answer must not depend on what happens to
	// be installed. Placed above, a machine with no CLI would turn a §2.4
	// refusal (exit 3) into an exit 1, which is exactly the confusion that exit
	// code exists to prevent.
	//
	// Still upstream of planAndExecute, which is where `auto` opens its run leg
	// — before the planner call, its first spawn — so a missing CLI leaves no
	// run directory behind. `--plan-only` is below this line, unlike `run`'s
	// `--dry-run`, because a plan-only invocation still spawns the planner.
	if err := runner.CheckRunnerCLI(nodeRunner); err != nil {
		return err
	}

	// Same live-view gate as `run` and `resume`. WithVerifyCommand is given to
	// the COORDINATOR, not to a cycle: every cycle of a --max-cycles goal loop
	// re-enters the same coordinator and plans afresh, so every cycle's sinks
	// carry the command and every cycle's run is gated on it (ADR 0016 §2).
	options := []coordinator.Option{coordinator.WithVerifyCommand(verifyCommand)}
	// The one option here that widens, and given to the same COORDINATOR for
	// the same reason: every cycle of a goal loop plans afresh, and the
	// operator's statement is about the run, not about cycle 1 (ADR 0032).
	// It carries its own de-escalations — agent mapping and skill activation
	// both go off inside WithLoadedUserConfig — so the branch below prints the
	// reason but does not have to enforce it.
	if flags.acceptLoadedUserConfig {
		options = append(options, coordinator.WithLoadedUserConfig())
	}
	if runtime == runner.RuntimeClaude {
		options = append(mappingOptions(os.Stdout, flags.noAgentMapping, flags.noAgents, flags.noSkillActivation, flags.acceptLoadedUserConfig), options...)
		// Claude only, and one of the two places the real settings path enters
		// the coordinator: a planned node's `--setting-sources ""` withholds the
		// file the operator's model choice lives in, so the choice is read back
		// out of it by name (ADR 0037). A codex run reads nothing here — its
		// model lives in ~/.codex/config.toml, which oh-my-graph does not read
		// (docs/LIMITATIONS.md).
		options = append(options, coordinator.WithUserSettingsPath(usermodel.DefaultPath()))
	} else {
		fmt.Fprintln(os.Stdout, "Codex runtime: Claude agent mapping and skill activation are unavailable; the generated plan will show the filesystem sandbox policy used for each node.")
	}
	coord := coordinator.New(nodeRunner, options...)
	return planAndExecute(ctx, os.Stdout, coord, nodeRunner, flags.commonRunFlags, flags.goal,
		goalCycleOptions{maxCycles: flags.maxCycles, maxGoalBudgetUSD: flags.maxGoalBudgetUSD}, flags.planOnly, nil,
		webOpener(flags.noWeb, stdout, opener))
}

// mappingOptions wires the two auto-decisions for a production Coordinator:
// subagent mapping over the default user-then-project agent directories, and
// skill activation over the default user skill directory (never a project one
// — ADR 0012's cut, held by ADR 0017), each independently switchable off by
// its flag. This is the only place the real filesystem locations enter the
// coordinator — tests construct theirs with temp dirs instead.
//
// noAgents (--no-agent) is read only on the branch where mapping is ON, and
// that is not an oversight: with --no-agent-mapping there is no mapping left
// for it to decline, so passing both is redundancy rather than a conflict and
// gets no warning it would have to earn.
//
// w takes the one thing this function knows and the plan printout cannot: a
// coordinator built with ZERO skill directories never scans, so it records no
// SkillScan and noteSkillActivation prints nothing — correct for the library
// default (coordinator.New with no dirs) and for --no-skill-activation, but
// silence here would mean activation is ON, stages nothing, and says nothing
// at all. DefaultSkillDirs returns empty in exactly one case, an unresolvable
// home directory (no $HOME: containers, launchd, some CI), and that is a
// misconfiguration nobody chose — the one silence this disclosure exists to
// remove. Said here rather than by giving every library caller a non-nil scan.
//
// loadedUserConfig short-circuits both, and prints why. The forcing itself is
// coordinator.WithLoadedUserConfig's — asserted there, so no call site can
// forget it — and what belongs here is the half that must not be silent: two
// mechanisms the operator may have been relying on are off, and the screen
// says so rather than letting them notice a missing [agent: ...] marker.
// Returning no directories at all is deliberate: with activation and mapping
// both off, scanning them would read the user's files to build a corpus
// nothing can use.
func mappingOptions(w io.Writer, noAgentMapping bool, noAgents []string, noSkillActivation bool, loadedUserConfig bool) []coordinator.Option {
	if loadedUserConfig {
		fmt.Fprint(w,
			"agent mapping and skill activation are off for this run, because --accept-loaded-user-config\n"+
				"loads your own settings: a staged agent or skill would be shadowed by a same-named one\n"+
				"your settings discover, so the definition that ran would not be the one this run staged\n"+
				"(measured: docs/measurements/0017-lifting-the-agent-mapped-exclusion.md). Your CLI\n"+
				"discovers your agents and skills itself instead, exactly as it does for `run`.\n",
		)
		return nil
	}
	var opts []coordinator.Option
	if noAgentMapping {
		opts = append(opts, coordinator.WithoutAgentMapping())
	} else {
		opts = append(opts, coordinator.WithAgentDirs(coordinator.DefaultAgentDirs()...))
		if len(noAgents) > 0 {
			opts = append(opts, coordinator.WithoutAgentsNamed(noAgents...))
		}
	}
	if noSkillActivation {
		opts = append(opts, coordinator.WithoutSkillActivation())
	} else {
		skillDirs := coordinator.DefaultSkillDirs()
		if len(skillDirs) == 0 {
			fmt.Fprint(w,
				"skill activation is on, but no skill directory could be resolved (your home directory\n"+
					"is unset), so nothing will be scanned and no skill can be staged. Set HOME, or pass\n"+
					"--no-skill-activation to say so on purpose.\n",
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
// so the sequence that must stay identical between them has exactly one home.
//
// THE INVARIANT IT KEEPS, restated by ADR 0023 §2.4 rather than quietly
// weakened: a graph started from chat is identical on disk (saved spec,
// state.json, events.jsonl) to one started at the shell GIVEN THE SAME
// COMMITMENT POINT — and confirm is the one thing that moves that point. It
// used to read "indistinguishable on disk", full stop, with confirm named as
// the one permitted divergence. That is no longer true in the letter, and the
// reason is confirm itself observed on disk: `auto` is non-interactive, so its
// commitment to execute exists BEFORE the planner call and it legitimately
// opens a run directory (and a planning run_started) there; chat's commitment
// does not exist until a human answers [y/N], which happens AFTER the planner
// call, so a chat-started run's events.jsonl carries one run_started where an
// auto run's carries two. The extra line is confirm's recorded consequence, and
// it is written here rather than left for a reader to discover.
//
// confirm: nil proceeds straight to execution (`auto` stays fully
// non-interactive), while a non-nil hook is asked between printing the
// topology and executing — false discards the plan with a note, which is not
// an error. A hook error aborts before execution and propagates as-is, so a
// caller can recognize its own sentinel (chat's EOF-at-the-prompt). web is
// the run's live-view opener or nil for none (see executeGraph); `auto`
// passes its TTY-gated decision, chat always passes nil — a chat turn's run
// stays un-wired (ADR 0006).
//
// planOnly is `auto --plan-only`: the same sequence up to and including the
// topology print, then a stop — nothing is wired, no node runs. It is an early
// return inside this one sequence (notePlanOnlyPreview does the printing and
// nothing else) rather than a parallel plan-and-print function, because the
// flag's entire value is that what it shows is what a real run would show;
// a second implementation of the print would be free to drift from this one and
// would take a mapping line with it when it did. Note that the shared thing is
// printPlan, which BOTH branches call — the extracted helper carries only the
// stop line. The planner call above it is NOT skipped and cannot be: unlike
// `run --dry-run`, which reads a file, there is no plan to inspect until one
// has been bought. The saved spec survives the stop for the same reason — it
// was paid for — but it goes under plans/, not runs/ (ADR 0023 §3).
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

	// A run directory is created by the COMMITMENT TO EXECUTE, and nothing else
	// (ADR 0023 §2.4). `auto` is non-interactive: if the plan validates, it
	// runs, so the commitment exists before the planner call and its leg opens
	// here — which is what makes the whole planner call visible to `runs list`,
	// the dashboard and any external consumer, instead of the silence #163
	// reported. The two paths without that commitment get no run id at all
	// until they have one: `--plan-only` never executes, and `chat`'s plan is
	// gated behind a [y/N] a human may answer `n`.
	committed := !planOnly && confirm == nil

	var leg *runLeg
	runID := ""
	if committed {
		runID = newRunID()
		opened, err := openRunLeg(runID)
		if err != nil {
			return err
		}
		leg = opened
		if err := leg.beginPlanning(); err != nil {
			closeLeg(leg, runfeed.OutcomeFailed)
			return err
		}
		// The bracket, restored lexically over every exit below — including the
		// ones inside executePlan. Close is idempotent, so executeGraph's own
		// close on the happy path wins and this is a no-op there (ADR 0023 §2.7).
		defer closeLeg(leg, runfeed.OutcomeFailed)
		fmt.Fprintf(out, "Planning a graph for goal %q (run %s)...\n", goal, runID)
	} else {
		fmt.Fprintf(out, "Planning a graph for goal %q...\n", goal)
	}

	plan, err := coord.Plan(ctx, goal, inputKeys(flags.inputs))
	if err != nil {
		// A refused plan's spec goes into the run directory when a run leg
		// already exists, and into plans/ when none does (ADR 0023 §3.1). The
		// leg is closed FIRST, with outcome "failed", so the run has settled by
		// the time anything reads it: the engine judged the material it was
		// given and diagnosed it, which is what a FAIL is. "paused" would make
		// it print a resume command for a run with nothing to resume.
		if leg != nil {
			closeRejectedPlanning(leg, err)
			return noteRejectedPlan(out, runDirFor(runID), err)
		}
		return noteRejectedPlan(out, planDirFor(newRunID()), err)
	}
	if leg != nil {
		leg.setPlanningAccounting(plan.CostUSD, plan.CostUnknown, plan.Usage)
	}

	if planOnly {
		return notePlanOnlyPreview(out, plan, flags.runtime, flags.buildEvidence)
	}

	if !committed {
		accepted, err := confirmPlan(out, plan, flags.runtime, flags.buildEvidence, confirm)
		if err != nil || !accepted {
			return err
		}
		// The commitment just landed, so this is where the run id is minted and
		// the run directory becomes legitimate.
		runID = newRunID()
	}

	specPath, err := saveGeneratedSpec(runDirFor(runID), plan.Spec)
	if err != nil {
		return err
	}
	if committed {
		printPlanForRuntime(out, plan, specPath, flags.runtime, flags.buildEvidence)
	} else {
		// confirmPlan already printed the topology; only the destination was
		// unknown until the answer came back.
		fmt.Fprintf(out, "plan accepted, saved to %s\n\n", specPath)
	}

	return executePlan(ctx, runID, plan, nodeRunner, flags, specPath, web, nil, leg)
}

// notePlanOnlyPreview is `auto --plan-only`'s terminal branch: print the
// topology, say what the call cost, and keep the spec under plans/.
//
// A preview is still not a run, and since ADR 0023 §3 the reason is no longer
// the old mechanism argument — that a runs/ directory holding a graph.json and
// no state.json reads as damage, which §2.5 dissolved and which is now an
// ordinary shape. The reason that survives is the enumeration itself: a preview
// has none of the six statuses and cannot be given one. It is not PLANNING (it
// finished), not RUNNING, not ABANDONED (its process left on purpose), and it
// has no verdict about work, because there was no work. So it mints no run id
// at any point, its planner call included.
func notePlanOnlyPreview(out io.Writer, plan coordinator.Plan, runtime runner.Runtime, evidence *coordinator.BuildEvidenceOutcome) error {
	specPath, err := saveGeneratedSpec(planDirFor(newRunID()), plan.Spec)
	if err != nil {
		return err
	}
	printPlanForRuntime(out, plan, specPath, runtime, evidence)
	fmt.Fprintf(out,
		"plan only: no node was executed. The %s still paid for (%s) —\n"+
			"unlike `run --dry-run`, this is not free — and its plan is kept at %s.\n"+
			"Nothing ran, so this is not a run: it gets no run directory and `runs list` stays silent\n"+
			"about it. Run it with `oh-my-graph run %s`.\n",
		plannerCallsPhrase(plan), formatCost(plan.CostUSD, plan.CostUnknown), specPath, specPath)
	return nil
}

// confirmPlan is the chat path's gate: print the topology, ask, and place the
// paid-for spec according to the answer. It reports whether to proceed; a
// decline is (false, nil) — not an error, and today's "plan discarded."
//
// The spec is saved AFTER the answer, and that ordering is the fix ADR 0023
// §2.4 makes on the way past #163. Saving into the run directory before the
// prompt meant every declined plan left behind a runs/<id>/ holding a
// graph.json and no state.json — skipped by `runs list` as a directory it
// could not read and painted `unknown` by the dashboard. Saying no
// manufactured a corrupt run. The topology therefore prints with no path,
// because which directory the spec belongs in is exactly what is being decided.
//
// A failure to save the declined spec is reported and swallowed: losing the
// artifact must not turn a decline into an error.
func confirmPlan(out io.Writer, plan coordinator.Plan, runtime runner.Runtime, evidence *coordinator.BuildEvidenceOutcome, confirm func() (bool, error)) (bool, error) {
	printPlanForRuntime(out, plan, "", runtime, evidence)
	ok, err := confirm()
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}
	path, saveErr := saveGeneratedSpec(planDirFor(newRunID()), plan.Spec)
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "save the declined plan's spec: %v\n", saveErr)
		fmt.Fprintln(out, "plan discarded.")
		return false, nil
	}
	fmt.Fprintf(out, "plan discarded. Its spec was paid for either way and is kept at %s —\n"+
		"run it later with `oh-my-graph run %s`.\n", path, path)
	return false, nil
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
// iterated auto cycle (ADR 0011 §4), nil on every single-cycle run. leg is the
// already-open planning leg this run continues, or nil for a caller that had no
// planning phase — it is forwarded rather than re-opened, and see executeGraph
// for why re-opening would fail rather than merely duplicate.
func executePlan(ctx context.Context, runID string, plan coordinator.Plan, nodeRunner runner.NodeRunner, flags commonRunFlags, specPath string, web browser.Opener, goal *runstate.GoalRef, leg *runLeg) error {
	// The staged skill corpus is bound to THIS run's directory here, and not
	// in the coordinator, because the run id does not exist until the plan has
	// been bought (ADR 0017 §5). Binding is what puts --plugin-dir into
	// plan.ToolPolicies, so it must happen before the policies are handed to
	// the scheduler and snapshotted — and it is an error, not a downgrade: the
	// policies already carry `Skill`, and running them against a directory
	// that is not there would give every node a tool with no definitions
	// behind it, which is exactly the silent absence this mechanism is most
	// exposed to.
	if err := plan.BindSkillStaging(runDirFor(runID)); err != nil {
		return err
	}
	if plan.SkillActivation != nil {
		nodeRunner = coordinator.GuardStaging(nodeRunner, plan.SkillActivation.Staging)
	}
	// The matched agent definitions are bound the same way and for the same
	// reason (ADR 0022). This one is not a downgrade either, and less so: a
	// mapped node's policy already carries --setting-sources "" beside its
	// --agent, so an unbound directory would mean the CLI cannot resolve the
	// agent and exits 1 — every mapped node, every time.
	if err := plan.BindAgentStaging(runDirFor(runID)); err != nil {
		return err
	}
	if plan.AgentStaging != nil {
		nodeRunner = coordinator.GuardAgentStaging(nodeRunner, plan.AgentStaging)
	}
	// The operator's model choice travels with the plan that read it, so no
	// path can execute a planned graph while forgetting which model its nodes
	// were meant to answer with (ADR 0037). A settings file that could not be
	// read says so once, here — one line per run, never per node — and the run
	// proceeds on the CLI's default: a broken settings file is the operator's,
	// and killing the run over it is the worse outcome.
	flags.plannedModel = plan.Model
	if plan.ModelWarning != "" {
		fmt.Fprintln(os.Stderr, plan.ModelWarning)
	}
	// false: a planned graph never resolved a fragment — the coordinator
	// refuses planner-emitted use:/with: (ADR 0013), so plan.Spec is
	// fragment-free by construction and stays reusable verbatim.
	flags.planningCostUnknown = plan.CostUnknown
	flags.planningUsage = plan.Usage
	return executeGraph(ctx, runID, plan.Graph, nodeRunner, flags, plan.ToolPolicies, plan.CostUSD, specPath, plan.Spec, false, web, goal, leg)
}

// executeGraph wires the per-run collaborators (Handoff, RunLedger, Scheduler)
// around an already-validated graph and runs it — the shared back half of both
// `run` and `auto`. This is where the engine's exec seams are injected: the
// CLIRunner the caller passed (a node's model-CLI subprocess), a
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
func executeGraph(ctx context.Context, runID string, g *graph.Graph, nodeRunner runner.NodeRunner, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, planningCostUSD float64, graphSourcePath string, rawSource []byte, fragmentsResolved bool, web browser.Opener, goal *runstate.GoalRef, leg *runLeg) error {
	runtime := flags.runtime
	if runtime == "" {
		runtime = runner.RuntimeClaude
	}
	flags.runtime = runtime
	runtimeWarnings, err := runner.ValidateGraphForRuntime(runtime, g)
	// The last gate before anything spends, and the ONLY one an `auto` or
	// chat-started graph passes through, so it surfaces its own copy (ADR 0026).
	// A nil writer means stderr; `run` alone passes io.Discard, having already
	// printed this list beside the rest of its Codex disclosure.
	runtimeWarnW := flags.runtimeWarnW
	if runtimeWarnW == nil {
		runtimeWarnW = os.Stderr
	}
	warnRuntimePreflight(runtimeWarnW, graphSourcePath, runtimeWarnings)
	if err != nil {
		return err
	}
	// The leg holds the run's resume.lock for its whole duration — the same
	// flock every `resume` takes (internal/runstate.AcquireLock). Without it, a
	// `resume <run-id> --retry-failed` raced against a still-in-flight first leg
	// would open a second scheduler over the same state.json/events.jsonl and
	// double-spawn (double-bill) nodes; instead the concurrent resume fails on
	// this lock with its usual LockHeldError.
	//
	// A leg may arrive ALREADY OPEN since ADR 0023: an auto run opens it before
	// its planner call, so the planning phase is visible, and this function
	// continues that same leg — the scheduler's untagged run_started is what
	// marks the transition from PLANNING to RUNNING on the stream. `run` and a
	// chat-started graph have no planning phase and open theirs here, exactly as
	// before. Re-acquiring would not merely be redundant: flock conflicts per
	// open file description, so a second AcquireLock from this process would take
	// a LOCK_EX against an fd this process already holds and fail with
	// LockHeldError (ADR 0015 §1's measured table).
	//
	if leg == nil {
		opened, err := openRunLeg(runID)
		if err != nil {
			return err
		}
		leg = opened
	}
	// The sweep for the exits BELOW this line and ABOVE the scheduler — today,
	// a snapshot that cannot be written. It is registered FIRST so LIFO runs it
	// LAST, after the empty close below has had its say, and Close's idempotence
	// makes it a no-op on every path that reaches the scheduler.
	//
	// Without it this function is a hole in ADR 0023 §2.7's guarantee, and the
	// hole is on the auto path the sweep exists for: an auto run's PLANNING
	// run_started is already on disk when it gets here, so returning through a
	// bare `defer leg.Close("")` would close the leg VALUE without closing the
	// leg on the STREAM — and, having marked it closed, would disarm
	// planAndExecute's own sweep. The directory would then read ABANDONED after
	// a clean exit 1 and print runstatus.OrphanWarning: a false double-spend
	// alarm, which is precisely what §2.7 closes by construction.
	defer closeLeg(leg, runfeed.OutcomeFailed)

	h := handoff.New(runDirFor(runID), flags.inputs)
	led := ledger.New(runID)
	led.RecordPlanning(planningCostUSD, flags.planningCostUnknown, ledger.TokenUsage{
		InputTokens: flags.planningUsage.InputTokens, CachedInputTokens: flags.planningUsage.CachedInputTokens,
		OutputTokens: flags.planningUsage.OutputTokens, ReasoningOutputTokens: flags.planningUsage.ReasoningOutputTokens,
	})

	recorder, err := newRunRecorder(runID, graphSourcePath, rawSource, g, fragmentsResolved, flags, toolPolicies, planningCostUSD, goal)
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

	// From here the leg belongs to the scheduler: it writes this leg's own
	// run_finished, so the close below carries an EMPTY outcome — emitting
	// another would close one leg twice. Registered after the sweep above, so
	// LIFO runs this one first and the sweep sees an already-closed leg. It sits
	// below the recorder rather than beside `scheduler.Run` so the teardown
	// order is unchanged: the live view registered after it still stops before
	// the leg gives the lock back.
	defer closeLeg(leg, "")

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
	// The same pair, read for the pause hint: a resumed leg takes no
	// verification from the run directory, so the hint has to hand the command
	// back for the user to re-supply (ADR 0016 §4).
	var resumeVerifyCmd coordinator.VerifyCommand
	if serializedVerify != nil {
		resumeVerifyCmd = injectedVerifyCommand(g)
	}

	scheduler := schedule.NewScheduler(nodeRunner, schedule.Options{
		Concurrency:           flags.concurrency,
		ContinueOnFail:        flags.continueOnFail,
		Gate:                  gate.NewPauseController(),
		Verifier:              verify.NewShellVerifier(),
		Worktrees:             worktrees,
		ToolPolicies:          toolPolicies,
		Model:                 flags.plannedModel,
		SerializedVerifyNodes: serializedVerify,
		Recorder:              recorder,
		EventSink:             leg.feed,
		OpeningAccounting: schedule.RunAccounting{
			CostUSD: planningCostUSD, CostUnknown: flags.planningCostUnknown,
			Usage: runfeed.TokenUsage{
				InputTokens: flags.planningUsage.InputTokens, CachedInputTokens: flags.planningUsage.CachedInputTokens,
				OutputTokens: flags.planningUsage.OutputTokens, ReasoningOutputTokens: flags.planningUsage.ReasoningOutputTokens,
			},
		},
	})

	fmt.Fprintf(os.Stdout, "Running graph %q (run %s)\n\n", g.Name, runID)
	runErr := scheduler.Run(ctx, g, h, led)
	// Cleanup runs on a fresh context: the run's own may already be cancelled
	// (halt-on-fail, Ctrl-C), and a halted run is exactly when leftover
	// worktrees still need tearing down.
	reportWorktreeCleanup(os.Stderr, worktrees.Cleanup(context.Background()))

	fmt.Fprintln(os.Stdout)
	led.Print(os.Stdout)
	// serializedVerify is also the discriminator for what a resume of this run
	// needs, and not by coincidence: it is non-nil exactly when the tool ceiling
	// is non-empty (what `resume` reads off snap.ToolPolicies to tell an auto
	// graph from a hand-written one) AND some node carries an injected
	// verification (what ReattachVerifyCommand refuses to take from a run
	// directory). That pair is ADR 0016 §4's refusal, so the hint prints the
	// command WITH --verify-cmd rather than a bare one that would be refused.
	printPauseHint(os.Stdout, runID, runErr, resumeVerifyCmd)

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
func newRunRecorder(runID, graphSourcePath string, rawSource []byte, g *graph.Graph, fragmentsResolved bool, flags commonRunFlags, toolPolicies map[string]runner.ToolPolicy, planningCostUSD float64, goal *runstate.GoalRef) (*runstate.SnapshotRecorder, error) {
	graphJSON := rawSource
	if fragmentsResolved || !json.Valid(rawSource) {
		marshaled, err := json.Marshal(g)
		if err != nil {
			return nil, fmt.Errorf("encode graph for snapshot: %w", err)
		}
		graphJSON = marshaled
	}

	statePath := filepath.Join(runDirFor(runID), runstate.SnapshotFileName)
	base := runstate.Snapshot{
		RunID:               runID,
		Runtime:             string(flags.runtime),
		PlanningCostUSD:     planningCostUSD,
		PlanningCostUnknown: flags.planningCostUnknown,
		PlanningUsage: runstate.TokenUsage{
			InputTokens: flags.planningUsage.InputTokens, CachedInputTokens: flags.planningUsage.CachedInputTokens,
			OutputTokens: flags.planningUsage.OutputTokens, ReasoningOutputTokens: flags.planningUsage.ReasoningOutputTokens,
		},
		GraphSourcePath: graphSourcePath,
		GraphSHA256:     sha256Hex(rawSource),
		Graph:           graphJSON,
		Inputs:          map[string]string(flags.inputs),
		ContinueOnFail:  flags.continueOnFail,
		// Recorded, not defaulted: a later `resume` must be able to put this run
		// back under the mode it was launched in even if the binary's default has
		// changed underneath it (see runstate.Snapshot.DefaultPermissionMode).
		DefaultPermissionMode: schedule.DefaultPermissionMode,
		ToolPolicies:          toNodeToolPolicies(toolPolicies),
		Goal:                  goal,
		BuildEvidence:         buildEvidenceRecord(flags.buildEvidence),
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

// rejectedSpecFileName is a REFUSED plan's file. A distinct name because it is
// not a graph the engine would run: nothing may mistake it for one, least of all
// a reader walking the tree for graph.json — and since ADR 0023 §3.1 it sits in
// the run's OWN directory whenever a run leg exists, so the name is the only
// thing keeping the two kinds of spec apart there.
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

func formatCost(costUSD float64, unknown bool) string {
	if unknown {
		if costUSD > 0 {
			return fmt.Sprintf("unknown (known subtotal $%.4f)", costUSD)
		}
		return "unknown"
	}
	return fmt.Sprintf("$%.4f", costUSD)
}

func formatUsage(usage runner.TokenUsage) string {
	return fmt.Sprintf("input %d, cached %d, output %d, reasoning %d",
		usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningOutputTokens)
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
// runs at each sink outside every layer of it (ADR 0016 §2) — and since ADR
// 0030 §2.5b that slot states the ABSENCE too when there is one, so a reader
// meets one answer or the other and never neither. evidence is the launch's
// build-evidence outcome, nil for the surfaces that never ask the question.
// An EMPTY specPath means the plan has not been saved yet, which is the chat
// path since ADR 0023 §2.4: where the spec lands depends on the answer to the
// [y/N] this screen is being printed for, so the header cannot name a path it
// does not know. The caller announces the location once the answer picks one.
// printPlan is the Claude-runtime, no-build-evidence-record shape of the
// printout — the one every test that is about the TOPOLOGY uses, so a plan
// screen assertion does not have to name a runtime and an evidence record it is
// not about.
func printPlan(w io.Writer, plan coordinator.Plan, specPath string) {
	printPlanForRuntime(w, plan, specPath, runner.RuntimeClaude, nil)
}

func printPlanForRuntime(w io.Writer, plan coordinator.Plan, specPath string, runtime runner.Runtime, evidence *coordinator.BuildEvidenceOutcome) {
	g := plan.Graph
	if specPath == "" {
		fmt.Fprintf(w, "Planned graph %q (%d nodes, planning cost %s):\n", g.Name, len(g.Nodes), formatCost(plan.CostUSD, plan.CostUnknown))
	} else {
		fmt.Fprintf(w, "Planned graph %q (%d nodes, planning cost %s, saved to %s):\n", g.Name, len(g.Nodes), formatCost(plan.CostUSD, plan.CostUnknown), specPath)
	}
	if plan.Usage != (runner.TokenUsage{}) {
		fmt.Fprintf(w, "  planning tokens: %s\n", formatUsage(plan.Usage))
	}
	for _, node := range g.Nodes {
		line := "  - " + node.ID
		if len(node.DependsOn) > 0 {
			line += " (after " + strings.Join(node.DependsOn, ", ") + ")"
		}
		if runtime == runner.RuntimeCodex {
			line += " [sandbox: " + runner.CodexSandbox(node.PermissionMode) + "]"
		} else if len(node.AllowedTools) > 0 {
			line += " [tools: " + strings.Join(node.AllowedTools, ", ") + "]"
		}
		if runtime != runner.RuntimeCodex && node.Agent != "" {
			line += " [agent: " + node.Agent + "]"
		}
		fmt.Fprintln(w, line)
	}
	// One slot, two sentences, never both and never neither: either these
	// planned nodes run isolated or they run with the operator's own
	// configuration (ADR 0032 §2.6). The predicate reads the POLICIES about to
	// be spawned rather than the flag that set them, so the screen cannot
	// disagree with the argv — and so `resume`, which has no flag at all, asks
	// the identical question of the identical value.
	posture := isolatedPlannedNodes
	if plannedNodesLoadUserConfig(plan.ToolPolicies) {
		posture = loadedUserConfigPlannedNodes
	}
	if runtime == runner.RuntimeCodex {
		noteCodexRuntimePolicy(w, runtime, g, posture)
	} else {
		noteAgentMappings(w, plan.AgentMappings, plan.AgentStaging)
		noteSkillActivation(w, plan.SkillScan, plan.SkillActivation)
		if posture == loadedUserConfigPlannedNodes {
			noteLoadedUserConfig(w, runtime)
		} else {
			noteCeiling(w, anyAgentMapped(g))
		}
	}
	noteVerifyAttachments(w, plan.VerifyAttachments)
	noteMissingBuildEvidence(w, evidence)
	noteReplan(w, plan.Repaired)
	// Last, and deliberately after the ceiling: that paragraph says planned
	// nodes "run isolated", meaning settings and tools. This one narrows it —
	// filesystem isolation is a different isolation, and in auto mode there is
	// none of it at all — so it has to be read second or the two look
	// contradictory.
	noteUnisolatedPaths(w, plan.Unisolated)
	fmt.Fprintln(w)
}

// codexSandboxLine is the one line of the Codex disclosure that another
// sentence REFERS to rather than repeats: noteLoadedUserConfig's Codex literal
// ends "the filesystem sandbox above ... are argv on every node", and "above"
// has to name text the same screen actually emitted.
//
// A resumed leg prints that sentence and nothing else of noteCodexRuntimePolicy
// (continueRun), so it prints this line immediately before it. Extracting the
// literal rather than rewording the sentence keeps the fresh-run screen
// byte-identical and keeps one copy of the sandbox mapping: a second, reworded
// copy is how the two would eventually disagree about what plan mode gets.
const codexSandboxLine = "  Codex filesystem sandbox: permission_mode plan is read-only, bypassPermissions is danger-full-access, and every other mode is workspace-write.\n"

// noteCodexRuntimePolicy prints, before any node spends, every way a Codex run
// differs from the Claude run this project's defaults describe. (Before any
// NODE, not before anything: in `auto` the planner call is already bought by
// the time this prints, and on Codex that first spend is itself cost-unknown.)
// Each line is here because the alternative is meeting it AFTER paying for the
// nodes that led up to it: the network block lands wherever the graph's first
// publishing node sits, cost-unknown lands in the ledger footer, and the
// missing session-limit pause lands as a FAILED run someone expected to resume.
//
// Deliberately one line per difference and no more. A disclosure long enough
// to scroll past is one nobody reads, so anything that is merely interesting
// belongs in docs/LIMITATIONS.md, not here.
//
// config is the last line, and it is a three-valued question rather than a
// bool because three things are true in this project and only two of them are
// about a planned node. See nodeConfigPosture.
//
// Its FIRST line is a named constant because a second screen points at it: the
// Codex half of noteLoadedUserConfig says "the filesystem sandbox above", and a
// resumed leg prints that sentence without the rest of this block. One literal,
// two call sites — see codexSandboxLine.
func noteCodexRuntimePolicy(w io.Writer, runtime runner.Runtime, g *graph.Graph, config nodeConfigPosture) {
	if runtime != runner.RuntimeCodex {
		return
	}
	fmt.Fprint(w, codexSandboxLine)
	for _, node := range g.Nodes {
		if len(node.AllowedTools) == 0 {
			continue
		}
		fmt.Fprintln(w, "  allowed_tools declarations do not become granular Codex permissions; they remain graph documentation.")
		break
	}
	// Named files, because the user's own graph is the thing at risk. Where the
	// network node SITS is the part that decides what a failure costs, and it is
	// not always the last node: apply-flags pushes from its first, merge-shepherd
	// runs `gh` in all five of its model nodes and so fails at node 1 having done
	// nothing at all.
	fmt.Fprintln(w, "  No network: a sandboxed node cannot reach it — `gh`, `git push` and `git ls-remote` fail — so a graph halts at its FIRST node that publishes, wherever that sits.")
	fmt.Fprintln(w, "    Last node: adr-driven-dev (finalize), and every user of graphs/fragments/pr-publish.yaml (self-dev, dev-review-pr, backlog-batch). First node: apply-flags (dev pushes before verify reads). Every node: merge-shepherd, which is `gh` end to end and fails at node 1 having done nothing.")
	fmt.Fprintln(w, "    Two remedies, both per node: permission_mode: bypassPermissions maps to danger-full-access, which is no sandbox — that node keeps network AND keyring. Or Codex's sandbox_workspace_write.network_access=true, which lifts the block for `git push`/`git ls-remote` but not for `gh` on a machine where gh's token is in an OS keyring the sandbox denies (measured 2026-08-14, macOS: \"no oauth token found for github.com\"; where no keyring exists gh reads ~/.config/gh/hosts.yml, which the sandbox can read).")
	fmt.Fprintln(w, "  Cost is unknown for every Codex node: tokens are counted, USD never is, so this run reports no dollar figure per node or in total.")
	fmt.Fprintln(w, "  A node's budget_usd therefore loads but cannot apply — there is no spend to compare it against, and that node's runaway guard is its timeout: (each such node is warned by name). `auto --max-goal-budget-usd` is refused instead, because it is checked only at a cycle boundary: an unmeasurable ceiling would buy a whole cycle before stopping to say it cannot be checked, where an inapplicable node cap costs nothing extra. The loop stays bounded either way — --max-cycles is what bounds iterations (ADR 0026).")
	fmt.Fprintln(w, "  approval_policy=\"never\" is passed on every node: a non-interactive run cannot answer a prompt, so nothing is escalated for approval.")
	fmt.Fprintln(w, "  Session-limit pause: unchanged — ADR 0009's resumable pause covers this runtime too, so a Codex usage limit drains the in-flight nodes, records the limited one nowhere and exits 2 with a `resume --retry-failed` hint (ADR 0009 amended 2026-09-02, closing #222).")
	fmt.Fprintln(w, "    Detection is prose on both runtimes, not a typed signal: `hit your usage limit` here, `hit your session limit` on Claude. `turn.failed` is Codex's one terminal-failure record, so the message text is what tells a limit from any other failure — a reworded message degrades to an ordinary node failure that `resume --retry-failed` still salvages.")
	switch config {
	case isolatedPlannedNodes:
		fmt.Fprintln(w, "  Auto-planned Codex nodes also ignore user configuration, repository rules, project instructions, and MCP servers.")
	case loadedUserConfigPlannedNodes:
		noteLoadedUserConfig(w, runtime)
	}
}

// nodeConfigPosture is what the nodes of the graph being described load as
// their own configuration — the single question noteCodexRuntimePolicy's last
// line and noteCeiling's paragraph both answer.
//
// It replaced a bool when ADR 0032 added a third answer, and the third value
// is the reason it had to: `run`, `--dry-run` and `lint` describe a graph the
// USER wrote, whose nodes have carried the user's configuration since before
// any of these flags existed. Saying "planned nodes run with YOUR
// configuration (--accept-loaded-user-config)" there would name a flag nobody
// typed for nodes nobody planned, so those three keep printing nothing at all,
// exactly as they did.
type nodeConfigPosture int

const (
	// handWrittenNodes: the graph came from the user, so there is no
	// difference from their own configuration to disclose. Prints nothing.
	handWrittenNodes nodeConfigPosture = iota
	// isolatedPlannedNodes: the `auto` default — layer 1 is "" and the nodes
	// load none of the user's settings.
	isolatedPlannedNodes
	// loadedUserConfigPlannedNodes: planned nodes under
	// --accept-loaded-user-config, or a leg resuming a run that used it.
	loadedUserConfigPlannedNodes
)

// plannedNodesLoadUserConfig answers the question the disclosure slot turns
// on, from the policies that are about to be spawned rather than from the flag
// that built them (ADR 0032 §2.7).
//
// Reading the policies is what lets `resume` — which registers no opt-in flag
// and must not — ask this of a snapshot it merely rehydrated, and it is what
// makes a screen that disagrees with the argv impossible: SettingSources is
// the same pointer both this and runner.buildArgs read. An empty or nil map is
// a hand-written graph, which records no policies at all
// (runstate.Snapshot.ToolPolicies), and answers false.
func plannedNodesLoadUserConfig(policies map[string]runner.ToolPolicy) bool {
	for _, policy := range policies {
		if policy.SettingSources == nil {
			return true
		}
	}
	return false
}

// noteLoadedUserConfig is the opted-in half of the disclosure slot: the
// sentence a run gets INSTEAD of noteCeiling's paragraph (Claude) or instead
// of noteCodexRuntimePolicy's isolation line (Codex). ADR 0032 §2.6 fixes both
// literals, and they differ because what the operator bought differs — on
// Codex the sandbox floor is argv on every node and survives, on Claude the
// operator's standing permission grants come back with their settings and turn
// layer 2 into a declaration again.
//
// It is deliberately not a reassurance. The most likely way this feature fails
// is an operator reading it as pure capability, so the Claude paragraph spends
// a whole sentence on the grants and both name what still binds.
//
// Printed by `auto`, by `--plan-only`, by every later cycle of a goal loop and
// by a resumed leg of an opted-in run. One writer, one literal per runtime: a
// second copy of these words somewhere else is how one of them goes stale.
func noteLoadedUserConfig(w io.Writer, runtime runner.Runtime) {
	if runtime == runner.RuntimeCodex {
		fmt.Fprint(w,
			"  Planned nodes run with YOUR configuration (--accept-loaded-user-config): ~/.codex/config.toml,\n"+
				"  your repository's rules and AGENTS.md files, and your MCP servers all load. The filesystem\n"+
				"  sandbox above and approval_policy=\"never\" are argv on every node, so this flag widens neither.\n",
		)
		return
	}
	fmt.Fprint(w,
		"  Planned nodes run with YOUR configuration (--accept-loaded-user-config): your user, project\n"+
			"  and local settings load, and with them your CLAUDE.md, your hooks and your MCP servers.\n"+
			"  Your standing permission grants load too, so a declared scope like Bash(git *) is a\n"+
			"  declaration again and not a limit — a call your own settings allow will run. Each node's\n"+
			"  --tools set and its deny list still bind. Enterprise and managed policy are unaffected by\n"+
			"  this flag and cannot be widened by it. Agent mapping and skill activation are off for this\n"+
			"  run: a staged definition would be shadowed by a same-named one your settings discover.\n",
	)
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
	fmt.Fprintf(w, "  re-planned: the first reply was refused by validation (cost %s, included above) and a corrected one was requested:\n", formatCost(repair.RejectedCostUSD, repair.RejectedCostUnknown))
	if repair.RejectedUsage != (runner.TokenUsage{}) {
		fmt.Fprintf(w, "    tokens: %s\n", formatUsage(repair.RejectedUsage))
	}
	for _, issue := range repair.Issues {
		fmt.Fprintf(w, "    ! %s\n", issue)
	}
}

// noteUnisolatedPaths warns, before anything spends, that this plan's text
// names a local git checkout other than the one oh-my-graph was invoked from
// (SECURITY.md, "Isolation stops at the invocation repository"). Silent for the
// common single-repository plan, and silent whenever the boundary could not be
// resolved.
//
// The message may not claim that the invocation repository is isolated, because
// it is not: `auto` rejects `cwd:` and `worktree:` at plan time, so it
// provisions no managed worktree anywhere and every planned node works directly
// in the tree the user opened. What the checkouts named above have that the
// invocation tree does not is that the user did not open them for this run.
//
// It is printed HERE, in the plan printout, because that is where a user
// already looks and it is what `auto --plan-only` renders: a multi-repository
// goal is exactly the case where someone wants to read the plan before paying
// for it, and the fact the engine cannot isolate half of it has to arrive
// before the run, not in a post-mortem of a collision.
//
// A warning and never a refusal — the reason is in UnisolatedScan's doc. The
// closing paragraph states the rule's own limits in the user's face for the
// same reason: detection is a heuristic read of prompt text, and a warning
// that let itself be read as complete would be worse than no warning at all,
// because a silent plan would then look like a checked one.
func noteUnisolatedPaths(w io.Writer, scan *coordinator.UnisolatedScan) {
	if scan == nil {
		return
	}
	for _, path := range scan.Paths {
		// Two lines rather than one: the checkout is the headline, and a path
		// this deep plus every node that named it does not fit beside it on a
		// terminal. The written spelling is kept — a "~/..." goal and a file
		// deep inside the repository both resolve to the root above, and only
		// the original text can be searched for.
		fmt.Fprintf(w, "  ! not isolated: %s — a local git checkout\n", path.Repo)
		detail := "    named by " + unisolatedNamedBy(path)
		if path.Mention != path.Repo {
			detail += ", written as " + path.Mention
		}
		fmt.Fprintln(w, detail)
	}
	if scan.IsRepo {
		fmt.Fprintf(w, "  in auto mode oh-my-graph isolates no checkout at all — not even the one it was invoked\n"+
			"  from (%s), where every planned node works directly in your tree\n"+
			"  (cwd: and worktree: are rejected at plan time). The difference is only whose tree it is:\n", scan.Root)
	} else {
		fmt.Fprintf(w, "  in auto mode oh-my-graph isolates no checkout at all, and it was not invoked from a git\n"+
			"  repository (%s) either — every planned node works directly in that\n"+
			"  directory. The difference is only whose tree it is:\n", scan.Root)
	}
	fmt.Fprint(w,
		"  the checkouts above are ones you did not open for this run, and oh-my-graph creates no\n"+
			"  worktree and takes no lock in them, so a node that switches a branch there changes a\n"+
			"  directory another process may be standing in. If a node must work in one, say in the goal\n"+
			"  that it has to create its own git worktree there first and stay inside it. See SECURITY.md,\n"+
			"  \"Isolation stops at the invocation repository\".\n"+
			"  This is a heuristic read of the plan's text, not a guarantee: it reports absolute paths\n"+
			"  written in the goal or in a planned prompt that resolve into a git checkout elsewhere. It\n"+
			"  cannot see a path a node builds at run time, one arriving through an --input or a parent's\n"+
			"  artifact, a repository reached by a relative path, or what a node will actually do once it\n"+
			"  is there, and it deliberately says nothing about a tool installation's own checkout (a\n"+
			"  package or version manager under /usr, /opt or a dot-directory of your home) — and it is a\n"+
			"  warning, not a refusal: a multi-repository goal is legitimate, oh-my-graph simply cannot\n"+
			"  isolate it.\n",
	)
}

// unisolatedNamedBy renders where a checkout was named — the goal, one node,
// or several — so the user can find the text that put it in the plan. The goal
// comes first because it is the user's own words, and a warning they can act
// on by rewording is more useful than one about a prompt they did not write.
// Nodes share one clause ("nodes a, b") rather than one each, which is what
// keeps the line readable when a path reaches most of the graph.
func unisolatedNamedBy(path coordinator.UnisolatedPath) string {
	var sources []string
	if path.InGoal {
		sources = append(sources, "the goal")
	}
	if len(path.NodeIDs) > 0 {
		quoted := make([]string, 0, len(path.NodeIDs))
		for _, id := range path.NodeIDs {
			quoted = append(quoted, fmt.Sprintf("%q", id))
		}
		noun := "node "
		if len(quoted) > 1 {
			noun = "nodes "
		}
		sources = append(sources, noun+strings.Join(quoted, ", "))
	}
	return strings.Join(sources, " and ")
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
// until v0.5 a rejected one left NOTHING on disk: the user paid, saw an error,
// and had no artifact to hand-edit or re-run.
//
// dir is where the spec goes, and the rule is one sentence (ADR 0023 §3.1):
// INTO THE RUN DIRECTORY WHEN A RUN LEG ALREADY EXISTS, and into plans/ when
// none does. Two independent premises put it there rather than one. A
// commitment to execute is what creates a run — `auto` and every goal cycle have
// one before the planner call, so their run id already exists and the rejection
// belongs beside it. A diagnosis about judged material is what gives a run a
// verdict — the engine was handed material, judged it, and produced a finding,
// which is a FAIL in the same sense a failed node is. `--plan-only` has the
// second without the first: its diagnosis prints, but it minted no run id at any
// point, so there is no run directory to write into and the spec goes to
// plans/ — which thereby keeps one honest meaning, "specs that never belonged
// to a run".
//
// The refusal itself is returned unchanged; this only adds a line naming where
// the rejected spec went. A failure to write it is reported and swallowed:
// losing the artifact must not replace the diagnosis the user actually needs.
func noteRejectedPlan(w io.Writer, dir string, err error) error {
	var rejection *coordinator.PlanRejection
	if !errors.As(err, &rejection) {
		return err
	}
	if rejection.CostUSD > 0 || rejection.CostUnknown {
		fmt.Fprintf(w, "planning failed after spending %s — a planner call is paid for whether or not its graph loads.\n", formatCost(rejection.CostUSD, rejection.CostUnknown))
	}
	if rejection.Usage != (runner.TokenUsage{}) {
		fmt.Fprintf(w, "planning tokens: %s\n", formatUsage(rejection.Usage))
	}
	if len(rejection.Spec) == 0 {
		return err
	}
	path, saveErr := saveSpecAs(dir, rejectedSpecFileName, rejection.Spec)
	if saveErr != nil {
		fmt.Fprintf(os.Stderr, "save rejected plan spec: %v\n", saveErr)
		return err
	}
	fmt.Fprintf(w, "The rejected spec is kept at %s — fix it by hand and run it with `oh-my-graph run %s`.\n", path, path)
	return err
}

// noteAgentMappings discloses every subagent auto-mapping decision before the
// run starts: skipped candidates with their reason, and — when any mapping
// applied — what a mapped node traded for its agent, named node by node.
// Silence here means no candidate matched and nothing changed.
//
// WHAT THIS PARAGRAPH SAID UNTIL ADR 0022, and why the reversal is not a
// softening. As shipped in v0.6.0 it told the user that a mapped node "loads
// your settings" and that its "declared scope is enforced only as far as YOUR
// settings enforce it". Both were true and both were measured. The
// code under them changed: the definition now arrives from a staged
// --plugin-dir, so layer 1 stays "" and the same ceiling arm that breached 2 of
// 2 under the old argv was denied 3 of 3 under this one
// (docs/measurements/0017-staged-agent-restores-layer-1.md). LEAVING THE OLD
// TEXT IN PLACE WOULD BE THE SAME DEFECT POINTING THE OTHER WAY — a disclosure
// that under-promises is still a wrong disclosure, and a user who reads "your
// repository can supply configuration to this node" will make decisions about
// what they check into it.
//
// What survives unchanged is the SHAPE: a per-node line, because "which nodes
// got what" is not recoverable from a paragraph that names none of them, and a
// cost stated in full — which is now a real loss of capability rather than a
// loss of isolation.
//
// EVERY STAGED DEFINITION IS PRINTED WITH THE FILE IT CAME FROM, and that is
// not decoration. "Auto-mapped onto YOUR OWN agents" is a claim about a path on
// disk; measurement (l) is what happens when nothing on the screen carries the
// path — with `<cwd>/.claude/agents` still scanned, the file staged into an
// unattended node's `--agent` was the repository's, 2 of 2, under a line that
// said "your own". The scan scope is fixed (DefaultAgentDirs), so the source is
// a user file again; printing it is what makes that checkable per run rather
// than believed. The size and hash come along for the same reason the staged
// skill corpus prints them.
func noteAgentMappings(w io.Writer, mappings []coordinator.AgentMapping, staging *coordinator.AgentStaging) {
	var applied []coordinator.AgentMapping
	for _, m := range mappings {
		if m.SkippedReason != "" {
			fmt.Fprintf(w, "  ! agent %q not applied to %s: %s\n", m.Agent, m.NodeID, m.SkippedReason)
			continue
		}
		applied = append(applied, m)
	}
	if len(applied) == 0 {
		return
	}
	fmt.Fprint(w, "  Auto-mapped onto your own Claude Code agents — oh-my-graph copies each definition into\n"+
		"  this run's directory and hands the node that copy, so your settings stay unloaded:\n")
	for _, m := range applied {
		fmt.Fprintf(w, "    %s runs as your %q — same ceiling as any other planned node, and it\n"+
			"      holds NO Skill tool\n", m.NodeID, m.Agent)
	}
	if staging != nil {
		for _, a := range staging.Agents() {
			fmt.Fprintf(w, "    staged %s from %s (%.1f KiB, sha256:%.12s…)\n",
				a.Name, a.SourcePath, float64(a.Bytes)/1024, a.SHA256)
		}
	}
	fmt.Fprint(w,
		"  The ceiling BINDS on these nodes, in scope and not only in tool names: none of your\n"+
			"    user/project/local settings load, so a node declaring Bash(git *) cannot run a\n"+
			"    non-git command even if your own settings would allow one. Measured 2026-08-12 on\n"+
			"    this build's argv — denied 3 of 3, with the same argv minus the staging breaching\n"+
			"    2 of 2 and an in-scope control still running\n"+
			"    (docs/measurements/0017-staged-agent-restores-layer-1.md).\n"+
			"  What that COSTS these nodes: your CLAUDE.md, your hooks, your MCP servers and your\n"+
			"    standing permission grants are all unavailable to them, exactly as for every other\n"+
			"    planned node. Until 2026-08-12 a mapped node was the one exception; if your agents\n"+
			"    lean on your environment, that is what changed.\n"+
			"  The agent file is read ONCE, at plan time, and pinned by hash: the copy the node\n"+
			"    resolves is the one whose declared tools were checked against this node's ceiling,\n"+
			"    and editing the original mid-run neither changes this run nor stops it. A resumed\n"+
			"    leg maps nothing at all and says so.\n"+
			"  --no-agent-mapping turns all of it off; --no-agent <name> declines one agent and keeps\n"+
			"    the rest — every node that agent would have taken gets its Skill tool back, and no\n"+
			"    other node changes.\n"+
			"  Not scanned: ./.claude/agents — the repository under work. Since 2026-08-12 only your\n"+
			"    own ~/.claude/agents is, because a scanned definition is now COPIED into the node's\n"+
			"    --agent and a repository-committed one measured 2 of 2 as the system prompt that ran\n"+
			"    (docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md). Move an agent\n"+
			"    you want mapped to ~/.claude/agents.\n",
	)
}

// noteSkillActivation discloses skill activation before the run starts
// (ADR 0017 §7): the whole staged corpus with each skill's size and hash, the
// nodes it reaches, what it costs every invocation, and — when activation is
// off despite a scan having run — why.
//
// What this can NO LONGER say is which skill a node will use. Under ADR 0012's
// inlining that was printable, because trusted code chose it; under activation
// the node's own model chooses at run time, by description, and no amount of
// printing recovers it. Saying so out loud is the point: a plan that listed
// nothing would otherwise read as a plan that activates nothing, and silent
// absence is this mechanism's signature failure. The retrospective account is
// the node's session transcript, which exists already — every node runs with
// session persistence on — and the printout says where to look.
//
// The corpus is bracketed by the SCAN it came out of, because the corpus alone
// cannot say why it is empty: a user with skills that live somewhere this scan
// deliberately does not go sees "0 skill(s) from <dir>" rather than silence. A
// shadowed definition gets its own line for the same reason — the count is the
// size of the deduped set, so a collision quietly lowers it and nothing else
// would ever name the file that lost.
//
// The exclusion line says what the exclusion COSTS, and it says it once rather
// than per node, because until 2026-08-09 it said the opposite: "that composite
// is unmeasured; it already sees your real skills". That was a capability claim
// and it is measured false — an agent-mapped node's argv carries no `Skill` in
// --tools, so the definitions its settings load are unreachable by it. A
// disclosure that reads as a reassurance while the capability is entirely absent
// is the same asymmetry §7 convicts ADR 0012's printout of, so the remedy that
// actually works (--no-agent-mapping) is named on the same lines.
//
// scan is nil only when no scan happened (--no-skill-activation, or no
// configured directories), and then this prints nothing: the user who turned
// it off does not need to be told twice — and the one case where nobody turned
// it off, an unresolvable home, is disclosed by mappingOptions instead.
func noteSkillActivation(w io.Writer, scan *coordinator.SkillScan, activation *coordinator.SkillActivation) {
	if scan == nil {
		return
	}
	fmt.Fprintf(w, "  skill scan: %d skill(s) from %s\n", scan.Found, strings.Join(scan.Dirs, ", "))
	for _, path := range scan.Shadowed {
		fmt.Fprintf(w, "  skill shadowed: %s — another definition declares the same name and wins\n", path)
	}
	if activation != nil && activation.Enabled {
		fmt.Fprintf(w, "  skill activation: ENABLED on %d of %d planned node(s)\n",
			len(activation.NodeIDs), len(activation.NodeIDs)+len(activation.ExcludedNodeIDs))
		fmt.Fprintf(w, "    %d skill(s) staged (adds ~%d tokens to EVERY node invocation, including retries\n"+
			"    and feedback re-runs)\n", len(activation.Skills), activation.EstimatedPromptTokens)
		for _, sk := range activation.Skills {
			fmt.Fprintf(w, "    %s (%.1f KiB, sha256:%.12s…) — %q\n",
				sk.Name, float64(sk.Bytes)/1024, sk.SHA256, sk.Description)
		}
		for _, id := range activation.ExcludedNodeIDs {
			fmt.Fprintf(w, "    excluded: %s is agent-mapped\n", id)
		}
		noteExclusionCost(w, activation.ExcludedNodeIDs)
		fmt.Fprint(w,
			"  Which skill a node uses is chosen by the model at run time from those descriptions.\n"+
				"  It is NOT knowable here; each invocation is recorded in that node's session transcript.\n"+
				"  MEASURED YIELD (44 spawns against this exact argv — ADR 0017; the raw record is\n"+
				"    docs/measurements/0017-skill-activation-yield.md): the planner's own prompt reached\n"+
				"    for a skill 0 of 9 times unaided, and 8 of 9 with the one fixed sentence oh-my-graph\n"+
				"    now appends to every activated prompt. Whether the WORK is better is NOT measured:\n"+
				"    the two arms were indistinguishable on the one task checkable mechanically, at\n"+
				"    $0.205 a spawn against $0.134, and an output-contract node activates under neither.\n"+
				"    The tokens above are charged either way.\n"+
				"  ceiling: UNCHANGED — for every planned node in this run, excluded ones included since\n"+
				"    2026-08-12. Their settings, CLAUDE.md, hooks and MCP servers do not load (ADR 0004\n"+
				"    layer 1 stays \"\"); a declared scope like Bash(git *) is enforced. The only change\n"+
				"    activation makes is that the Skill tool exists for the node(s) named above, which is\n"+
				"    the one half an EXCLUDED node still differs in — see its lines above.\n"+
				"  The staged corpus is re-materialized and verified before every node spawn, so a node\n"+
				"    cannot leave a skill behind for a later one. Your own skill files are read once, at\n"+
				"    staging: editing them mid-run neither changes this run nor stops it.\n"+
				"  Turn it off with --no-skill-activation.\n",
		)
	} else if activation != nil && activation.DisabledReason != "" {
		fmt.Fprintf(w, "  skill activation: OFF — %s\n", activation.DisabledReason)
		// The cost prints on THIS branch too, and this is the branch where it
		// is largest: when every planned node is agent-mapped, activation
		// turns itself off (skillstage.go), so the enabled branch above never
		// runs and the exclusion is total. A user told only "OFF — every
		// planned node is agent-mapped" would read that as a corpus that went
		// unused, not as a plan in which no node can invoke any skill at all.
		noteExclusionCost(w, activation.ExcludedNodeIDs)
	}
	fmt.Fprint(w,
		"  Not scanned: plugin-provided skills (~/.claude/plugins) and project skills (./.claude/skills).\n"+
			"  Both are out of scope (ADR 0012, held by ADR 0017), so a skill you installed through a\n"+
			"  plugin is not staged — that is a stated limit, not a failure.\n",
	)
}

// noteExclusionCost states what skill activation's one exclusion costs the
// nodes it excludes. It is a function rather than a paragraph inside the
// enabled branch because it has to print on BOTH branches: an agent-mapped
// node is excluded whether activation ended up enabled for the others or
// turned itself off because there was no one left to activate.
//
// It states a cost and never a reassurance. This text used to close with "it
// already sees your real skills", which is a capability claim and is measured
// false (docs/measurements/0017-agent-mapped-nodes-cannot-invoke-a-skill.md):
// the node's argv carries no Skill in --tools, so the definitions its settings
// load are visible to the CLI and unreachable by the node.
//
// It also used to say "lifting the exclusion is unmeasured", and that stopped
// being true on 2026-08-12. Lifting it was measured and REFUSED, and the line
// said which of the two it was: a decision with a number behind it, not a gap
// waiting on one.
//
// ADR 0022 MOVED THE GROUND OUT FROM UNDER THAT REFUSAL, and the text has to
// move with it or it becomes the reassuring half-truth pointing the other way.
// The refusal rested on what a mapped node's `nil` layer 1 loaded — a
// repository-committed skill beating the staged corpus 3 of 3 — and layer 1 is
// now "" on those nodes, where the same repository-supplied definitions were
// measured NOT to load at all. So the exclusion is no longer a refusal with a
// number behind it; it is a decision that has not been re-taken, which is a
// third thing, and the printout says exactly that. What it must NOT say is
// either "it does not work" (adding the tool works, 3 of 3) or "it is coming"
// (nothing has decided that).
func noteExclusionCost(w io.Writer, excluded []string) {
	if len(excluded) == 0 {
		return
	}
	fmt.Fprint(w,
		"    An excluded node holds NO Skill tool, so it can invoke NO skill at all — not a\n"+
			"      staged corpus, and not your own installed skills. Measured 2026-08-09: told\n"+
			"      outright to use one it fired 0 of 3 without `Skill` in --tools, and 3 of 3\n"+
			"      with it added and nothing else changed\n"+
			"      (docs/measurements/0017-agent-mapped-nodes-cannot-invoke-a-skill.md).\n"+
			"      Lifting the exclusion was measured on 2026-08-12 and refused, on the ground\n"+
			"      that a mapped node then loaded your settings and a skill name resolved against\n"+
			"      definitions the REPOSITORY UNDER WORK can write. That ground is GONE since\n"+
			"      2026-08-12: such a node now loads no settings at all, and the same\n"+
			"      repository-supplied definitions were measured not to reach it\n"+
			"      (docs/measurements/0017-staged-agent-restores-layer-1.md). The exclusion\n"+
			"      stands as a decision nobody has re-taken, not as a refusal with a number\n"+
			"      behind it — it will be re-decided in its own record, or not at all.\n"+
			"      --no-agent-mapping is what gets a node out of the exclusion, and it turns\n"+
			"      agent mapping off for the WHOLE plan; --no-agent <name> declines one agent,\n"+
			"      so only the nodes that agent would have taken change.\n",
	)
}

// noteCeiling states what running this plan actually does to the machine. It
// prints for every planned run rather than only when some node declares Bash,
// because the isolation half applies to all of them: a planned node loads none
// of your settings, so it also gets none of your CLAUDE.md, hooks or MCP
// servers. Nodes are more isolated and less capable here than in a
// hand-written graph, and that is worth one line up front rather than a
// puzzling failure ten minutes in.
//
// mapped no longer narrows the CEILING half, and that is ADR 0022's whole
// effect on this function. This paragraph makes two claims — "a declared scope
// like Bash(git *) is enforced rather than merely requested" (ADR 0004's E1,
// this project's headline claim) and "your CLAUDE.md, hooks and MCP servers are
// unavailable to them" (a cost). From 2026-08-12 both hold for EVERY planned
// node, mapped included, because a mapped node no longer loads any settings:
// its agent definition comes from a directory oh-my-graph staged, not from
// discovery. The exception that stood here for one release is deleted rather
// than softened — a warning kept past its cause teaches readers to discount the
// next one.
//
// What mapped still prints is a NOTE and not an exception: the node runs under
// someone else's system prompt, which is a fact about the plan a reader should
// carry into the per-node lines above, and the one thing about a mapped node
// that is still different.
//
// Since ADR 0032 this is one of TWO paragraphs that can fill this slot, and it
// is the one a run gets by DEFAULT. Its alternative is noteLoadedUserConfig,
// printed instead of it — never beside it — for a run that typed
// --accept-loaded-user-config. printPlanForRuntime chooses between them from
// the policies, so every sentence below stays true of every run that reads it.
func noteCeiling(w io.Writer, mapped bool) {
	fmt.Fprint(w,
		"  Planned nodes run isolated: none of your user/project/local settings load, so a declared\n"+
			"  scope like Bash(git *) is enforced rather than merely requested — and your CLAUDE.md,\n"+
			"  hooks and MCP servers are unavailable to them. See SECURITY.md for what this does not cover.\n",
	)
	if mapped {
		fmt.Fprint(w,
			"  That INCLUDES any node marked [agent: ...] above, since 2026-08-12: its agent definition\n"+
				"  is copied into this run's directory and supplied as a plugin, so nothing about it\n"+
				"  reopens your settings (measured, not inferred:\n"+
				"  docs/measurements/0017-staged-agent-restores-layer-1.md). What is still different about\n"+
				"  those nodes is that each runs under one of YOUR agents' system prompts, and holds no\n"+
				"  Skill tool. See the per-node lines above.\n",
		)
	}
}

// anyAgentMapped reports whether the printed topology contains a node marked
// [agent: ...]. It reads the GRAPH rather than plan.AgentMappings on purpose:
// the exception noteCeiling states is about the nodes the reader can see the
// marker on, and node.Agent is what puts it there. A plan carrying a mapping
// record whose node somehow lost its agent would otherwise get a warning about
// a node the screen does not show.
func anyAgentMapped(g *graph.Graph) bool {
	for _, node := range g.Nodes {
		if node.Agent != "" {
			return true
		}
	}
	return false
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
//
// A MULTI-NODE resolution (ADR 0027) overrides nothing — the using node may
// declare only wiring — so its line names the ids it spliced instead. That is
// the same disclosure duty in the shape the multi-node form takes: the reader
// of a run log learns that one `use:` became five nodes, and which five,
// without opening the fragment file.
//
// Since ADR 0029 a fragment may cite a fragment, and that sentence holds per
// LINE rather than per subtree. A nested `use:` gets a resolution of its own,
// printed after the line that explains it (the loader appends parents before
// descending), so the tree arrives as several lines whose ids say its shape. A
// parent whose internal node was itself a loop does NOT list that node: the
// resolved graph holds its expansion, not it, so the parent's `nodes:` clause
// undercounts its subtree and the nested line below carries the remainder
// (ADR 0029 §5). Depth is deliberately not printed — the ids already carry the
// nesting a reader is chasing, and §5 declines to spend a column on it.
//
// And a grant substitution ASSEMBLED — a fragment's own `{{ with.x }}` inside
// its `allowed_tools`, bound by the citing node — is neither an override nor a
// count of ids, so it gets its own clause naming the resolved list (#196).
// That clause is the only one printed for a grant a fragment wrote verbatim:
// none. A grant readable in one file is not what the two-file principle is
// about, and one line per difference and no more is what keeps these lines
// read at all. A multi-node splice that parameterized SEVERAL nodes' grants is
// the one case that outgrows the line: see grantClauses.
func printFragmentResolutions(w io.Writer, resolutions []graph.FragmentResolution) {
	for _, r := range resolutions {
		line := fmt.Sprintf("fragment: node %q spliced from %q (%s) — %s", r.NodeID, r.Fragment, r.Source, r.Description)
		if len(r.Spliced) > 0 {
			line += " — nodes: " + strings.Join(r.Spliced, ", ")
		}
		if len(r.Overridden) > 0 {
			line += " — node overrides: " + strings.Join(r.Overridden, ", ")
		}
		clauses := grantClauses(r)
		if len(clauses) == 1 {
			line += " — allowed_tools resolved from with: " + clauses[0]
		}
		fmt.Fprintln(w, line)
		if len(clauses) > 1 {
			fmt.Fprintln(w, "  allowed_tools resolved from with:")
			for _, clause := range clauses {
				fmt.Fprintln(w, "    "+clause)
			}
		}
	}
}

// grantClauses renders each substituted grant for the disclosure line. A
// single-node resolution's one grant belongs to the node the line already
// names, so it prints bare; a multi-node one qualifies each by its spliced id,
// because a loop may parameterize the grant of some of its nodes and not
// others, and "which of the five" is the whole question. A grant that resolved
// to nothing prints as `(none)`: an empty list is a disclosure, not an absence,
// and an empty tail would read as a truncated line.
//
// One clause rides the line inline; beyond one, the caller gives each its own
// indented line — the shape noteCodexRuntimePolicy already uses for a per-node
// list. Inline they would nest `,` inside `;` (`x/build: Read, Bash(go *);
// x/review: Read, Write`) and the boundary between two nodes' GRANTS would be
// the weaker separator, which is the one thing a grant disclosure may not blur.
func grantClauses(r graph.FragmentResolution) []string {
	clauses := make([]string, 0, len(r.Grants))
	for _, grant := range r.Grants {
		tools := strings.Join(grant.Tools, ", ")
		if len(grant.Tools) == 0 {
			tools = "(none)"
		}
		if grant.NodeID != r.NodeID {
			tools = grant.NodeID + ": " + tools
		}
		clauses = append(clauses, tools)
	}
	return clauses
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

// planDirFor is where a plan that was bought but never executed keeps its spec:
// `auto --plan-only`'s preview, a declined `chat` plan, and a rejection from a
// call that minted no run id. It is deliberately NOT under runsRoot(), and since
// ADR 0023 §2.5 the reason is no longer the old mechanism argument — a runs/
// directory with no state.json is an ordinary shape now, not damage. The reason
// that survives is the enumeration: none of these has any of the six statuses,
// because nothing ran. Its own tree therefore keeps the two kinds of artifact —
// what ran, and what was only planned — apart for every reader, without any of
// them needing a special case.
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
			// Not a ceiling layer, and persisted anyway: it is the ONLY
			// durable record that this run was skill-activated (the grant is
			// invisible in graph.json by design, ADR 0017 §2), so a resumed
			// leg reads it to know activation was on for an earlier leg and
			// to say so while dropping it (dropSkillActivation, ADR 0017 §6).
			PluginDirs: p.PluginDirs,
		}
	}
	return out
}

// injectedVerifyCommand reads back the evidence command a planned graph's sinks
// carry, for the one purpose of printing it in a resume hint the user can
// actually paste (ADR 0009's "one copy-pasteable command", kept for a
// --verify-cmd run by #198).
//
// The BOUND comes back with it, not just the command: a run started with
// --verify-timeout 2m whose hint offered only --verify-cmd would be resumed at
// the 10m default, which is the one place the next leg's check would differ
// from the leg it continues — the asymmetry ADR 0016 §4 refuses for the command
// itself. verifyResumeSuffix decides which half needs printing.
//
// The first one found is the answer because attachVerification writes ONE
// user-supplied string, under ONE resolved timeout, to every sink it attaches
// to; there is no shape in which two sinks of one planned graph disagree.
// Callers gate this on the graph actually being a planned one — a hand-written
// graph's `verify:` round-trips through a resume untouched and needs no flag.
func injectedVerifyCommand(g *graph.Graph) coordinator.VerifyCommand {
	for _, n := range g.Nodes {
		if n.SuccessCheck.Verify != nil {
			return coordinator.VerifyCommand{
				Command: n.SuccessCheck.Verify.Command,
				Timeout: n.SuccessCheck.Verify.TimeoutDuration(),
			}
		}
	}
	return coordinator.VerifyCommand{}
}

// verifyResupplyNote is the sentence printed under a resume command that
// carries --verify-cmd, saying why the flag is repeated rather than remembered.
//
// It used to say the opposite — that the run could not be resumed at all —
// because `resume` had no flag to re-supply the command with, so ADR 0016 §4's
// refusal was terminal and ADR 0009's promise (a session limit is a PAUSE, the
// work is banked, a later leg finishes it) was not kept for any auto run
// carrying build evidence (#198). The flag exists now; what stays true is the
// reason it must be typed again: the run directory is not an admissible source.
const verifyResupplyNote = "  (--verify-cmd is repeated because `resume` takes no verification from a run\n" +
	"   directory — the command has to come from you, not from disk. ADR 0016 §4.)"

// verifyResumeSuffix is the flag pair appended to a printed resume command when
// this run's sinks carry an injected evidence command. Empty for every run that
// carries none, so the common hint is byte-identical to what it always was.
//
// The timeout is printed only when it is not the resolved default, which is
// also the ceiling: appending `--verify-timeout 10m0s` to every hint would be
// noise on the common path, and omitting a 2m bound the user chose would hand
// back a weaker check than the leg being continued.
//
// The command is quoted the way the usage synopsis spells it, and the quoting
// is real (shellSingleQuoted) rather than a bare wrap. `'` is deliberately IN
// verifyShellMetachars — a --verify-cmd may contain one, it just stands the
// pre-flight down — so a naive '%s' turns `sh -c 'make && ./x'` into a printed
// line that runs a DIFFERENT command and then executes `./x` on its own. This
// function's promise is that what it prints runs, and #198 is exactly the cost
// of a printed instruction the tool cannot honour.
func verifyResumeSuffix(v coordinator.VerifyCommand) string {
	if !v.Supplied() {
		return ""
	}
	suffix := " --verify-cmd " + shellSingleQuoted(v.Command)
	// The zero value's resolved timeout IS the default, so this compares against
	// the coordinator's own constant without exporting it.
	if v.ResolvedTimeout() != (coordinator.VerifyCommand{}).ResolvedTimeout() {
		suffix += fmt.Sprintf(" --verify-timeout %s", v.ResolvedTimeout())
	}
	return suffix
}

// shellSingleQuoted wraps s so a POSIX shell passes it through as one word,
// byte for byte. The rule is the standard one and not an invention: inside
// single quotes nothing is special except `'` itself, which is closed,
// backslash-escaped and reopened. It exists ONLY for printing a command back to
// the user — nothing here builds argv, and the engine still runs the command through
// `sh -c` at the verify seam.
func shellSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// printPauseHint prints the exact resume commands when runErr is one of the
// two pause shapes — a *schedule.PausedError (the gate lifecycle's "print the
// exact resume command" step — DESIGN.md, "Gate nodes and resume") or a
// *schedule.LimitPausedError (ADR 0009: the hint carries the CLI's own
// "resets <time>" when the captured cause yields one, and drops the time
// rather than inventing one when it doesn't) — and is a silent no-op for any
// other outcome (success or failure), so it is safe to call unconditionally
// after every run.
//
// verifyCmd is the evidence command this run's sinks carry (with its resolved
// bound), or the zero VerifyCommand for none, and it is the caller's to supply
// because only the caller knows whether this graph
// holds an injected verification under a tool ceiling. When it is set the
// printed command carries the flag back: `resume` drops a snapshot-borne
// verification rather than replaying it, so a bare resume of such a run is
// refused. A session-limit pause is exactly the shape a long auto run reaches,
// so this is the hint that fires most often for a --verify-cmd run — and the
// one that, printed without the flag, sends the reader into a refusal.
func printPauseHint(w io.Writer, runID string, runErr error, verifyCmd coordinator.VerifyCommand) {
	resupply := verifyResumeSuffix(verifyCmd)
	note := ""
	if resupply != "" {
		note = verifyResupplyNote + "\n"
	}
	var paused *schedule.PausedError
	if errors.As(runErr, &paused) {
		// The gate half is unreachable together with verifyCmd today —
		// validatePlannedNodes refuses a planned gate node, so only an auto graph
		// can carry an injected verification and only a hand-written one can pause
		// at a gate. Composed anyway rather than special-cased: this function's
		// whole promise is that the command it prints runs.
		fmt.Fprintf(w, "\nPaused at gate %q. Resume with:\n  oh-my-graph resume %s --approve %s%s\n  oh-my-graph resume %s --reject %s%s\n%s",
			paused.GateID, runID, paused.GateID, resupply, runID, paused.GateID, resupply, note)
		return
	}
	var limited *schedule.LimitPausedError
	if !errors.As(runErr, &limited) {
		return
	}
	reset := runner.SessionLimitReset(limited.Cause)
	if reset != "" {
		fmt.Fprintf(w, "\nSession limit reached (resets %s). Resume after %s with:\n  oh-my-graph resume %s --retry-failed%s\n%s",
			reset, reset, runID, resupply, note)
		return
	}
	fmt.Fprintf(w, "\nSession limit reached. Resume with:\n  oh-my-graph resume %s --retry-failed%s\n%s", runID, resupply, note)
}

// graphSpawnsRuntime reports whether any node of this graph would launch the
// model CLI. A gate node pauses for a human and spawns nothing; a graph made
// only of them needs no CLI installed, and refusing it for a missing one is a
// verdict about a capability the run never reaches for.
func graphSpawnsRuntime(g *graph.Graph) bool {
	for _, node := range g.Nodes {
		if node.Type != graph.TypeGate {
			return true
		}
	}
	return false
}
