package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// commonRunFlags are the execution options `run` and `auto` share. One
// register method wires them onto each subcommand's FlagSet so the flag names
// and usage strings can never drift between the two.
type commonRunFlags struct {
	runtime             runner.Runtime
	inputs              inputFlag
	concurrency         int
	continueOnFail      bool
	noWeb               bool
	planningCostUnknown bool
	planningUsage       runner.TokenUsage
	// plannedModel is the model this run's planned nodes answer with — the
	// operator's own choice, read from one key of their settings file at plan
	// time (coordinator.Plan.Model, ADR 0034). Not a flag, and deliberately not
	// registered as one: there is exactly one surface for the choice, the
	// settings file, so a run cannot disagree with it (§6c). Empty for `run`,
	// which executes a hand-written graph whose nodes load those settings
	// themselves.
	plannedModel string
	// buildEvidence is the launch-time build-evidence question and its answer
	// (ADR 0030 §2.5a), for the snapshot to record and the plan screen to state.
	// Not a flag: it is what the gate concluded from the flags plus the
	// invocation directory. nil for `run`, which executes a hand-written graph
	// carrying its author's own success_check.verify and never asks the
	// question — so its snapshot records no answer.
	buildEvidence *coordinator.BuildEvidenceOutcome
	// runtimeWarnW receives the runtime-preflight warnings executeGraph's own
	// runner.ValidateGraphForRuntime call produces (ADR 0026). Not a flag: the
	// caller's answer to "have these already been shown?". `run` sets it to
	// io.Discard because it surfaced the identical list at load, as part of the
	// pre-run Codex disclosure; every other entry to executeGraph leaves it nil,
	// which means os.Stderr — nil must never mean silence, or the one path that
	// forgets to set it drops the warning.
	runtimeWarnW io.Writer
}

func (c *commonRunFlags) register(set *flag.FlagSet) {
	c.inputs = make(inputFlag)
	set.Var(c.inputs, "input", "bind a graph input as key=value (repeatable)")
	set.IntVar(&c.concurrency, "concurrency", 0, "max nodes to run at once (0 = use the graph's value; ceiling 10)")
	set.BoolVar(&c.continueOnFail, "continue-on-fail", false, "prune only a failed node's subtree instead of halting the run (ORs with the graph's on_fail field: either saying continue means continue)")
	set.BoolVar(&c.noWeb, "no-web", false, "do not serve or open the web live view for this run (it only appears when stdout is a terminal)")
}

// runFlags holds the parsed `run` subcommand options. Kept in its own type so
// parsing is testable and runGraph stays about wiring, not argv fiddling.
type runFlags struct {
	graphPath string
	dryRun    bool
	commonRunFlags

	set *flag.FlagSet
}

// newRunFlags builds a runFlags with its FlagSet configured. The graph path is a
// positional argument, so it is not registered as a flag. --dry-run is `run`'s
// own, not a commonRunFlags member: `auto` has no equivalent, since its plan
// step already costs a real planner call.
func newRunFlags() *runFlags {
	f := &runFlags{set: flag.NewFlagSet("run", flag.ContinueOnError)}
	f.register(f.set)
	f.set.BoolVar(&f.dryRun, "dry-run", false, "validate the graph and print the resolved plan, then exit without running any node")
	return f
}

// parse reads args in the order `<graph.yaml> [flags...]`. The graph path is
// required and must come first; flags follow it. A dash-prefixed first element
// is a flag rather than a graph path (positionalArg, argslot.go), so it reaches
// the FlagSet instead of being opened as a file.
func (f *runFlags) parse(args []string) error {
	if req := helpRequest(args, "run", f.set); req != nil {
		return req
	}
	graphPath, rest, ok := positionalArg(args)
	if err := f.set.Parse(rest); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("run: missing graph file (usage: oh-my-graph run <graph.yaml> [--input k=v ...])")
	}
	f.graphPath = graphPath
	return nil
}

// autoFlags holds the parsed `auto` subcommand options. The goal is a
// positional argument, mirroring how `run` takes its graph path.
type autoFlags struct {
	goal              string
	planOnly          bool
	noAgentMapping    bool
	noAgents          agentNameFlag
	noSkillActivation bool
	// The DEPRECATED `--no-skill-mapping` spelling has no field of its own:
	// it is rewritten to `--no-skill-activation` before parsing, by
	// deprecatedSkillFlagSpellings and rewriteDeprecatedSkillFlag below.
	maxCycles        int
	maxGoalBudgetUSD float64
	verifyCmd        string
	verifyTimeout    time.Duration
	// acceptNoBuildEvidence is ADR 0030's one opt-out. It is not a verification
	// switch and its name says so: what the operator states by typing it is that
	// THIS RUN CARRIES NO BUILD EVIDENCE, which is a true thing about the run,
	// not a feature being turned off — there was never a check to skip.
	acceptNoBuildEvidence bool
	// acceptLoadedUserConfig is ADR 0032's opt-in, and the only flag on `auto`
	// that WIDENS what a planned node may do. Its name follows the same rule
	// as the one above: what the operator states by typing it is that THIS
	// RUN'S PLANNED NODES LOAD THEIR OWN CLI CONFIGURATION, which is a true
	// thing about the run and a bill they are signing, not a feature switch.
	// `resume` registers no counterpart — it inherits the choice from the
	// snapshot's policies — and `chat` registers none either, because a flag
	// that widens an unattended run must be typed at a launch and not implied
	// by one [y/N] keystroke.
	acceptLoadedUserConfig bool
	commonRunFlags

	set *flag.FlagSet
}

// newAutoFlags builds an autoFlags with its FlagSet configured. The goal is a
// positional argument, so it is not registered as a flag. --plan-only is
// `auto`'s counterpart to `run --dry-run` and is NOT the same bargain: a dry
// run reads a file the user already wrote and costs nothing, while a plan has
// to be bought before it can be shown. Its usage string says so, because a
// flag whose name reads free next to a flag that is free would be read as
// free. --no-agent-mapping
// and --no-skill-mapping are `auto`'s own, not commonRunFlags members: `run`
// executes a hand-written graph whose agent: fields and prompts are the user's
// explicit choice, so there is nothing automatic to switch off there.
// --max-cycles and --max-goal-budget-usd are likewise `auto`'s own
// (ADR 0011 §1): the goal loop iterates PLANS, which only `auto` produces —
// and keeping the cycle count off commonRunFlags is what keeps chat
// structurally single-cycle.
//
// --verify-cmd and --verify-timeout are NOT commonRunFlags members, for the
// same reason and a second one (ADR 0016 §2). The reason: they describe what
// trusted code attaches to a PLAN's sink nodes, and only `auto` produces a
// plan. The second one: `run` needs no such flag at all, because a hand-written
// graph writes `verify:` on whichever node it means — a flag would be a worse
// spelling of a field the user already has. `resume` registers the pair too
// (newResumeFlags), which is the re-supply half ADR 0016 §4 named and #198 hit:
// the command still comes from the human, never from the run directory.
//
// --accept-loaded-user-config is `auto`'s alone for a third reason, and it is
// the one that matters most (ADR 0032 §2.7): it WIDENS. `resume` must have no
// spelling of it, because a resumed leg's own flags may only de-escalate — it
// inherits the first leg's choice from the snapshot's policies instead, which
// is not `resume` choosing.
func newAutoFlags() *autoFlags {
	f := &autoFlags{set: flag.NewFlagSet("auto", flag.ContinueOnError)}
	f.register(f.set)
	f.set.BoolVar(&f.planOnly, "plan-only", false, "plan the graph, print it with every agent/skill mapping and the tool ceiling, then exit without running any node — NOT free, unlike `run --dry-run`: it still pays for at least one real planner call, and a validation refusal buys one corrected call on top of it")
	f.set.BoolVar(&f.noAgentMapping, "no-agent-mapping", false, "do not auto-map planned nodes onto your Claude Code agents (~/.claude/agents only — the repository's ./.claude/agents is not scanned)")
	f.set.Var(&f.noAgents, "no-agent", "do not auto-map this ONE agent, by its frontmatter name (repeatable), leaving every other mapping in place — the per-agent form of --no-agent-mapping. A mapped node runs under its agent's system prompt with its own definition staged, so it keeps the full ceiling (measured: docs/measurements/0017-staged-agent-restores-layer-1.md) but holds no Skill tool; declining it makes the node an ordinary planned node, which gets its Skill tool back and no more — your CLAUDE.md and hooks stay unloaded for it either way. A name matching no agent declines nothing")
	f.set.BoolVar(&f.noSkillActivation, "no-skill-activation", false, "do not stage your Claude Code skills (~/.claude/skills) for planned nodes — they then get no Skill tool and no --plugin-dir, exactly as before ADR 0017")
	f.set.IntVar(&f.maxCycles, "max-cycles", 1, "iterate the goal for up to N plan→run→assess cycles (ADR 0011); 1 (the default) is exactly today's single plan and run, with no assessment call. N has no upper bound, and a validation-refused plan buys one corrected planner call, so the planner-call worst case is 2 × N")
	f.set.Float64Var(&f.maxGoalBudgetUSD, "max-goal-budget-usd", 0, "soft cross-cycle spend ceiling for an iterated goal, checked before each cycle after the first — never a mid-flight kill; requires --max-cycles >= 2")
	f.set.StringVar(&f.verifyCmd, "verify-cmd", "", "shell command the ENGINE runs at every sink node of the plan, as build evidence (ADR 0016) — e.g. './gradlew build'. No node is granted anything: the command is yours, it is attached by trusted code after the plan validates, and the engine judges its exit code itself, so a check node can no longer certify a branch that does not build. Every cycle of --max-cycles plans afresh and every cycle's sinks get it")
	f.set.DurationVar(&f.verifyTimeout, "verify-timeout", 0, "bound on ONE --verify-cmd execution (0 = 10m, which is also the ceiling every verification has). Not the 2-minute default a hand-written verification gets: a cold Gradle, Cargo or Maven build is exactly what that default was not sized for")
	f.set.BoolVar(&f.acceptLoadedUserConfig, "accept-loaded-user-config", false, "state that this run's planned nodes load YOUR CLI configuration, and run anyway (ADR 0032): user/project/local settings on Claude, ~/.codex/config.toml plus repository rules and AGENTS.md on Codex, and with them your CLAUDE.md, your hooks and your MCP servers. This is not only a capability — your standing permission grants load too, so on Claude a node's declared scope like Bash(git *) stops being enforced and is a declaration again; each node's --tools set and deny list still bind, and enterprise/managed policy is unaffected and cannot be widened by this flag. Agent mapping and skill activation are turned OFF for the run, because a staged definition is shadowed by a same-named one your restored settings discover. The choice is printed with the plan and readable in this run's state.json")
	f.set.BoolVar(&f.acceptNoBuildEvidence, "accept-no-build-evidence", false, "state that this run carries no build evidence, and run anyway (ADR 0030). Without it, `auto` REFUSES to start in a directory where a build system is detected and no --verify-cmd was given — a planned node cannot carry a build command, so such a run's every judgement is the model's about its own work. This is not a verification switch: nothing is being skipped, because nothing was going to run. The choice is written to the run's state.json and printed with the plan, so a reader of that run later learns the absence was chosen. Accepted and inert where no build signal is detected")
	return f
}

// buildDeclaration is what `auto` says about running without build evidence —
// the --accept-no-build-evidence flag as the value object the gate takes
// (coordinator.RequireBuildEvidence). Not a field on VerifyCommand: that value
// object is shared with `resume`, which registers no opt-out and must have no
// field for one.
func (f *autoFlags) buildDeclaration() coordinator.BuildDeclaration {
	if f.acceptNoBuildEvidence {
		return coordinator.DeclaredByFlag
	}
	return coordinator.NoDeclaration
}

// verifyCommand is the --verify-cmd/--verify-timeout pair as the value object
// the coordinator takes (coordinator.WithVerifyCommand). The zero pair is the
// zero-config path: no attachment, and the advice line instead.
func (f *autoFlags) verifyCommand() coordinator.VerifyCommand {
	return coordinator.VerifyCommand{Command: f.verifyCmd, Timeout: f.verifyTimeout}
}

// parse reads args in the order `"<goal>" [flags...]`. The goal is required,
// must come first, and must not be blank. Unlike `run` — where a wrong
// positional fails loudly at graph load — a mistaken goal here would spend a
// real planner call, so a flag-shaped goal and trailing non-flag arguments
// (an unquoted multi-word goal) are rejected before anything runs.
func (f *autoFlags) parse(args []string) error {
	// A flag-shaped goal is refused below; a flag-shaped goal that is a request
	// for help is ANSWERED, since that refusal never named the flags (#200).
	if req := helpRequest(args, "auto", f.set); req != nil {
		return req
	}
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf(`auto: missing goal (usage: oh-my-graph auto "<goal>" [--input k=v ...] — the quoted goal comes first)`)
	}
	f.goal = args[0]
	if err := f.set.Parse(rewriteDeprecatedSkillFlag(os.Stderr, f.set, args[1:])); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("auto: unexpected argument %q after the flags — quote the goal so it is a single argument", f.set.Arg(0))
	}
	// The goal loop's governance is validated at parse, before anything
	// spends: the flag IS the bound, and it has no unbounded spelling
	// (ADR 0011 §1).
	if f.maxCycles < 1 {
		return fmt.Errorf("auto: --max-cycles must be at least 1, got %d", f.maxCycles)
	}
	if f.maxGoalBudgetUSD < 0 {
		return fmt.Errorf("auto: --max-goal-budget-usd must not be negative, got %v", f.maxGoalBudgetUSD)
	}
	// The ceiling is a cycle-boundary check (ADR 0011 §3), and a single-cycle
	// run has no cycle boundary — accepting the flag there would let it
	// silently do nothing, which reads as a bound that isn't one.
	if f.maxGoalBudgetUSD > 0 && f.maxCycles < 2 {
		return fmt.Errorf("auto: --max-goal-budget-usd is a cross-cycle ceiling and needs --max-cycles of at least 2; a single-cycle run has no cycle boundary to check it at")
	}
	// The mirror of the check above, from the other side: --plan-only stops
	// before execution, and every cycle after the first is planned FROM the
	// previous cycle's execution and assessment (ADR 0011 §2). So a
	// multi-cycle plan-only could only ever show cycle 1's plan — the later
	// cycles it names do not exist yet and never will under this flag.
	// Rejected at parse rather than silently showing one cycle, for the same
	// reason as above: a bound that quietly does something other than what it
	// says is worse than no bound.
	if f.planOnly && f.maxCycles > 1 {
		return fmt.Errorf("auto: --plan-only cannot be combined with --max-cycles %d; each cycle after the first is planned from the previous cycle's run, so there is nothing to show ahead of time beyond cycle 1", f.maxCycles)
	}
	// Declaring an absence and supplying the thing whose absence you declared is
	// a contradiction, and refusing beats picking a winner: either winner
	// silently discards something the operator typed. It belongs here — this is
	// flag-vs-flag consistency over the one FlagSet that registers both, exactly
	// like the --plan-only/--max-cycles refusal above — and NOT in
	// checkVerifyFlags, which `resume` also calls: the gate is auto-only, and
	// sharing the helper would gate a resume by accident (ADR 0030 §2.3).
	if f.acceptNoBuildEvidence && f.verifyCommand().Supplied() {
		return fmt.Errorf("auto: --accept-no-build-evidence says this run carries no build evidence, but --verify-cmd %q supplies some; pass one or the other", f.verifyCmd)
	}
	// Build evidence is validated at parse for the same reason the two bounds
	// above are, and for a sharper one: a planner call is billed whether or not
	// the plan is usable, so a --verify-cmd that could never have run must be
	// refused BEFORE anything is bought (ADR 0016 §2). The coordinator makes the
	// same check again at plan time — it is a library and cannot assume a CLI
	// ran first — but by then the money is at the next line.
	return checkVerifyFlags("auto", f.verifyCommand())
}

// resumeFlags holds the parsed `resume` subcommand options. Deliberately does
// NOT register --input: DESIGN.md requires resume to reject it (inputs come
// from the snapshot, and changing one mid-run would make the already-persisted
// artifacts inconsistent with the prompts that produced them), and the
// simplest way to reject a flag is to never define it — flag.Parse fails on
// its own with "flag provided but not defined: -input".
type resumeFlags struct {
	runID       string
	approveGate string
	rejectGate  string
	retryFailed bool
	concurrency int
	noWeb       bool
	// noSkillActivation drops skill activation from a resumed leg (ADR 0017
	// §6). It is the ONLY direction this flag has: a resume can turn a run's
	// activation off, and nothing on `resume` can turn it on, so no resumed
	// leg can ever run wider than the leg that started it.
	noSkillActivation bool
	// verifyCmd/verifyTimeout are ADR 0016 §4's re-supply half: the build
	// evidence a resumed leg runs comes from THIS invocation, exactly as a
	// fresh leg's comes from `auto`'s. They are not a widening — the ceiling,
	// the value object and the validation are `auto`'s, the engine still runs
	// the command and judges its exit code, and no node is granted anything.
	// What they replace is a refusal that was terminal (#198).
	verifyCmd     string
	verifyTimeout time.Duration

	set *flag.FlagSet
}

// newResumeFlags builds a resumeFlags with its FlagSet configured. The run id
// is a positional argument, mirroring `run`'s graph path and `auto`'s goal.
// --concurrency and --no-web are declared here rather than through
// commonRunFlags.register because resume must NOT get that set's --input (see
// the type's doc comment); their usage strings are kept verbatim identical to
// run/auto's, since a resumed leg's live view and concurrency ceiling behave
// exactly as a first leg's.
//
// --verify-cmd/--verify-timeout are declared here for the opposite reason: a
// resumed leg's build evidence does NOT behave exactly as a first leg's, and
// the difference is the whole point. On `auto` the pair describes what trusted
// code attaches to a plan it is about to buy; here it describes what this
// invocation attaches to a graph that already exists, replacing whatever the
// run directory holds — which is why the usage strings say so rather than being
// copied across (ADR 0016 §4, #198).
func newResumeFlags() *resumeFlags {
	f := &resumeFlags{set: flag.NewFlagSet("resume", flag.ContinueOnError)}
	f.set.StringVar(&f.approveGate, "approve", "", "approve the named gate and continue past it")
	f.set.StringVar(&f.rejectGate, "reject", "", "reject the named gate, pruning its subtree")
	f.set.BoolVar(&f.retryFailed, "retry-failed", false, "re-execute a failed run's failed and cancelled nodes, or finish a session-limit-paused run's unfinished nodes; every passed node's result is kept")
	f.set.IntVar(&f.concurrency, "concurrency", 0, "max nodes to run at once (0 = use the graph's value; ceiling 10)")
	f.set.BoolVar(&f.noWeb, "no-web", false, "do not serve or open the web live view for this run (it only appears when stdout is a terminal)")
	f.set.BoolVar(&f.noSkillActivation, "no-skill-activation", false, "accepted and redundant since 2026-08-07: NO resumed leg activates skills, because the only manifest it could re-stage from lives in the run directory the previous leg's nodes could write (ADR 0017 §6). Passing it changes nothing but the line resume prints. De-escalation only — there is no flag that turns activation on for a resumed leg")
	f.set.StringVar(&f.verifyCmd, "verify-cmd", "", "shell command the ENGINE runs at every sink node of a resumed AUTO run, as build evidence (ADR 0016 §4) — e.g. './gradlew build'. A resumed leg takes no verification from the run directory, so an auto run started with --verify-cmd needs the command supplied again HERE, by you; without it the resume is refused rather than run with weaker checking than the leg it continues. It attaches exactly as a fresh leg's does — after the same command validation and the same graph re-parse, under the same ceiling, with no node granted anything — and it is refused on a hand-written graph, whose own success_check.verify is your reviewed artifact and round-trips untouched")
	f.set.DurationVar(&f.verifyTimeout, "verify-timeout", 0, "bound on ONE --verify-cmd execution (0 = 10m, which is also the ceiling every verification has) — the same bound and the same ceiling auto applies, since a resumed leg must not be able to attach a check a fresh run could not")
	return f
}

// verifyCommand is `resume`'s half of the --verify-cmd/--verify-timeout pair,
// built as the SAME value object `auto` builds (autoFlags.verifyCommand), so
// the ceiling, the blank-command refusal and the resolved default are one
// implementation and cannot drift between the two subcommands. The zero pair
// means "no command supplied", which for a planned snapshot carrying one is the
// refusal ADR 0016 §4 keeps.
func (f *resumeFlags) verifyCommand() coordinator.VerifyCommand {
	return coordinator.VerifyCommand{Command: f.verifyCmd, Timeout: f.verifyTimeout}
}

// parse reads args in the order `<run-id> [flags...]`. The run id is
// required and must come first. Whether --approve/--reject were actually
// supplied is deliberately NOT enforced here: a bare `resume <run-id>` is
// only an error once the snapshot is loaded and the pending gate is known, so
// that the error can name it (see resumeDecision) — DESIGN.md, "A bare
// `resume <run-id>` on a paused run is an error naming the pending gate."
// A dash-prefixed first element is a flag, not a run id (positionalArg,
// argslot.go): #198's reporter, stranded mid-run, typed `resume --help` to
// learn which flags existed and was told there was no run called "--help".
func (f *resumeFlags) parse(args []string) error {
	if req := helpRequest(args, "resume", f.set); req != nil {
		return req
	}
	runID, rest, ok := positionalArg(args)
	if err := f.set.Parse(rest); err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("resume: missing run id (usage: oh-my-graph resume <run-id> ((--approve|--reject) <gate-id> | --retry-failed))")
	}
	f.runID = runID
	// The same parse-time gate `auto` applies, through the same helper. A
	// resumed leg buys no planner call, so the sharper half of auto's reason is
	// absent — but the flat one is not: a --verify-timeout past the ceiling or a
	// build command that cannot run would otherwise be discovered at the sink,
	// after the leg has re-spawned every unfinished node and paid for them.
	return checkVerifyFlags("resume", f.verifyCommand())
}

// deprecatedSkillFlagSpellings are the ways `--no-skill-mapping` can be typed.
// It is the ADR 0012 name for what ADR 0017 replaced, and it is rewritten
// rather than registered: registering it would advertise a dead mechanism in
// `--help` and in the usage synopsis, and dropping it would break a script
// that already passes it. The user intent behind it — "keep my skills out of
// my auto runs" — is unchanged; only the mechanism is, and the effect is now
// stronger rather than weaker.
var deprecatedSkillFlagSpellings = map[string]string{
	"-no-skill-mapping":  "-no-skill-activation",
	"--no-skill-mapping": "--no-skill-activation",
}

// rewriteDeprecatedSkillFlag translates the deprecated spelling in place and
// says so once on w. It never rewrites SILENTLY: the flag names a mechanism
// that no longer exists, so a user who typed it is owed the sentence.
//
// It rewrites only elements in FLAG POSITION, which is why it needs the
// FlagSet: `--verify-cmd --no-skill-mapping` passes that string to another
// flag as its VALUE, and rewriting it there would edit the user's build
// command. So an element consumed as a preceding flag's value is skipped, and
// so is everything after the `--` terminator.
func rewriteDeprecatedSkillFlag(w io.Writer, set *flag.FlagSet, args []string) []string {
	out := make([]string, len(args))
	copy(out, args)
	noticed := false
	isValue := false
	for i, arg := range out {
		if isValue {
			isValue = false
			continue
		}
		if arg == "--" {
			break
		}
		name, value, hasValue := strings.Cut(arg, "=")
		replacement, deprecated := deprecatedSkillFlagSpellings[name]
		if !deprecated {
			isValue = !hasValue && takesSeparateValue(set, name)
			continue
		}
		if hasValue {
			out[i] = replacement + "=" + value
		} else {
			out[i] = replacement
		}
		if !noticed {
			fmt.Fprint(w,
				"--no-skill-mapping is deprecated: the plan-time inlining it named is gone (ADR 0017).\n"+
					"Read as --no-skill-activation, which is what it now does.\n",
			)
			noticed = true
		}
	}
	return out
}

// takesSeparateValue reports whether arg is a registered non-boolean flag, and
// so consumes the NEXT element as its value. An unregistered spelling counts
// as consuming nothing: flag.Parse will reject it a moment later, and guessing
// that it takes a value would swallow the element after it.
func takesSeparateValue(set *flag.FlagSet, arg string) bool {
	if !strings.HasPrefix(arg, "-") {
		return false
	}
	name := strings.TrimLeft(arg, "-")
	if name == "" {
		return false
	}
	f := set.Lookup(name)
	if f == nil {
		return false
	}
	boolFlag, ok := f.Value.(interface{ IsBoolFlag() bool })
	return !ok || !boolFlag.IsBoolFlag()
}
