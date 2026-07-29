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
}

func (c *commonRunFlags) register(set *flag.FlagSet) {
	c.inputs = make(inputFlag)
	set.Var(c.inputs, "input", "bind a graph input as key=value (repeatable)")
	set.IntVar(&c.concurrency, "concurrency", 0, "max nodes to run at once (0 = use the graph's value; ceiling 10)")
	set.BoolVar(&c.continueOnFail, "continue-on-fail", false, "prune only a failed node's subtree instead of halting the run")
}

// runFlags holds the parsed `run` subcommand options. Kept in its own type so
// parsing is testable and runGraph stays about wiring, not argv fiddling.
type runFlags struct {
	graphPath string
	commonRunFlags

	set *flag.FlagSet
}

// newRunFlags builds a runFlags with its FlagSet configured. The graph path is a
// positional argument, so it is not registered as a flag.
func newRunFlags() *runFlags {
	f := &runFlags{set: flag.NewFlagSet("run", flag.ContinueOnError)}
	f.register(f.set)
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
	goal string
	commonRunFlags

	set *flag.FlagSet
}

// newAutoFlags builds an autoFlags with its FlagSet configured. The goal is a
// positional argument, so it is not registered as a flag.
func newAutoFlags() *autoFlags {
	f := &autoFlags{set: flag.NewFlagSet("auto", flag.ContinueOnError)}
	f.register(f.set)
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
	return nil
}
