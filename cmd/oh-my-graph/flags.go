package main

import (
	"flag"
	"fmt"
	"strings"
)

// commonRunFlags are the execution options `run` and `auto` share. One
// register method wires them onto each subcommand's FlagSet so the flag names
// and usage strings can never drift between the two.
type commonRunFlags struct {
	inputs         inputFlag
	concurrency    int
	continueOnFail bool
	noWeb          bool
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
// required and must come first; flags follow it.
func (f *runFlags) parse(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("run: missing graph file (usage: oh-my-graph run <graph.yaml> [--input k=v ...])")
	}
	f.graphPath = args[0]
	return f.set.Parse(args[1:])
}

// autoFlags holds the parsed `auto` subcommand options. The goal is a
// positional argument, mirroring how `run` takes its graph path.
type autoFlags struct {
	goal             string
	planOnly         bool
	noAgentMapping   bool
	noSkillMapping   bool
	maxCycles        int
	maxGoalBudgetUSD float64
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
func newAutoFlags() *autoFlags {
	f := &autoFlags{set: flag.NewFlagSet("auto", flag.ContinueOnError)}
	f.register(f.set)
	f.set.BoolVar(&f.planOnly, "plan-only", false, "plan the graph, print it with every agent/skill mapping and the tool ceiling, then exit without running any node — NOT free, unlike `run --dry-run`: it still pays for one real planner call")
	f.set.BoolVar(&f.noAgentMapping, "no-agent-mapping", false, "do not auto-map planned nodes onto your Claude Code agents (~/.claude/agents, ./.claude/agents)")
	f.set.BoolVar(&f.noSkillMapping, "no-skill-mapping", false, "do not inline your Claude Code skills (~/.claude/skills) into matching planned nodes' prompts")
	f.set.IntVar(&f.maxCycles, "max-cycles", 1, "iterate the goal for up to N plan→run→assess cycles (ADR 0011); 1 (the default) is exactly today's single plan and run, with no assessment call. N has no upper bound, and a refused plan buys one corrected planner call, so the planner-call worst case is 2 × N")
	f.set.Float64Var(&f.maxGoalBudgetUSD, "max-goal-budget-usd", 0, "soft cross-cycle spend ceiling for an iterated goal, checked before each cycle after the first — never a mid-flight kill; requires --max-cycles >= 2")
	return f
}

// parse reads args in the order `"<goal>" [flags...]`. The goal is required,
// must come first, and must not be blank. Unlike `run` — where a wrong
// positional fails loudly at graph load — a mistaken goal here would spend a
// real planner call, so a flag-shaped goal and trailing non-flag arguments
// (an unquoted multi-word goal) are rejected before anything runs.
func (f *autoFlags) parse(args []string) error {
	if len(args) == 0 || strings.TrimSpace(args[0]) == "" || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf(`auto: missing goal (usage: oh-my-graph auto "<goal>" [--input k=v ...] — the quoted goal comes first)`)
	}
	f.goal = args[0]
	if err := f.set.Parse(args[1:]); err != nil {
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
	return nil
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

	set *flag.FlagSet
}

// newResumeFlags builds a resumeFlags with its FlagSet configured. The run id
// is a positional argument, mirroring `run`'s graph path and `auto`'s goal.
// --concurrency and --no-web are declared here rather than through
// commonRunFlags.register because resume must NOT get that set's --input (see
// the type's doc comment); their usage strings are kept verbatim identical to
// run/auto's, since a resumed leg's live view and concurrency ceiling behave
// exactly as a first leg's.
func newResumeFlags() *resumeFlags {
	f := &resumeFlags{set: flag.NewFlagSet("resume", flag.ContinueOnError)}
	f.set.StringVar(&f.approveGate, "approve", "", "approve the named gate and continue past it")
	f.set.StringVar(&f.rejectGate, "reject", "", "reject the named gate, pruning its subtree")
	f.set.BoolVar(&f.retryFailed, "retry-failed", false, "re-execute a failed run's failed and cancelled nodes, or finish a session-limit-paused run's unfinished nodes; every passed node's result is kept")
	f.set.IntVar(&f.concurrency, "concurrency", 0, "max nodes to run at once (0 = use the graph's value; ceiling 10)")
	f.set.BoolVar(&f.noWeb, "no-web", false, "do not serve or open the web live view for this run (it only appears when stdout is a terminal)")
	return f
}

// parse reads args in the order `<run-id> [flags...]`. The run id is
// required and must come first. Whether --approve/--reject were actually
// supplied is deliberately NOT enforced here: a bare `resume <run-id>` is
// only an error once the snapshot is loaded and the pending gate is known, so
// that the error can name it (see resumeDecision) — DESIGN.md, "A bare
// `resume <run-id>` on a paused run is an error naming the pending gate."
func (f *resumeFlags) parse(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("resume: missing run id (usage: oh-my-graph resume <run-id> ((--approve|--reject) <gate-id> | --retry-failed))")
	}
	f.runID = args[0]
	return f.set.Parse(args[1:])
}
