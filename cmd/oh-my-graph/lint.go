package main

import (
	"fmt"
	"io"
	"os"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// runLint is the `lint` subcommand: parse argv and statically validate one
// graph file without running it.
func runLint(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lint: missing graph file (usage: oh-my-graph lint <graph.yaml>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("lint: unexpected argument %q (usage: oh-my-graph lint <graph.yaml>)", args[1])
	}
	return lintGraph(os.Stdout, args[0])
}

// lintGraph reads path and reports every structural problem graph.Lint finds
// — the same load-time checks `run` enforces (DAG/cycle, unknown depends_on
// ids, the session-handoff parent rule, verify blocks, agent and worktree
// names) — without executing anything: no node spawns, no run directory is
// created, zero cost. Where `run` stops at the first violation, lint prints
// them all, so a broken graph is fixable in one pass. A valid graph prints
// one confirmation line and returns nil (exit 0); an invalid one prints one
// line per issue to w and returns an error carrying the count, which
// mainExitCode turns into exit 1.
func lintGraph(w io.Writer, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read graph file %q: %w", path, err)
	}

	issues := graph.Lint(data)
	if len(issues) == 0 {
		fmt.Fprintf(w, "%s: valid\n", path)
		return nil
	}
	for _, issue := range issues {
		fmt.Fprintf(w, "%s: %v\n", path, issue)
	}
	noun := "issues"
	if len(issues) == 1 {
		noun = "issue"
	}
	return fmt.Errorf("%s: %d %s found", path, len(issues), noun)
}
