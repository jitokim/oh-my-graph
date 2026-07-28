package main

import (
	"flag"
	"fmt"
)

// runFlags holds the parsed `run` subcommand options. Kept in its own type so
// parsing is testable and runGraph stays about wiring, not argv fiddling.
type runFlags struct {
	graphPath      string
	inputs         inputFlag
	concurrency    int
	continueOnFail bool

	set *flag.FlagSet
}

// newRunFlags builds a runFlags with its FlagSet configured. The graph path is a
// positional argument, so it is not registered as a flag.
func newRunFlags() *runFlags {
	f := &runFlags{
		inputs: make(inputFlag),
		set:    flag.NewFlagSet("run", flag.ContinueOnError),
	}
	f.set.Var(f.inputs, "input", "bind a graph input as key=value (repeatable)")
	f.set.IntVar(&f.concurrency, "concurrency", 0, "max nodes to run at once (0 = use the graph's value; ceiling 10)")
	f.set.BoolVar(&f.continueOnFail, "continue-on-fail", false, "prune only a failed node's subtree instead of halting the run")
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
