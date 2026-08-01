package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

// dryRunGraph is the `run --dry-run` path: load the graph, report every
// structural issue via the same graph.Lint pass the `lint` subcommand uses,
// resolve the {{ inputs.* }} references against the bound --input values,
// print the resolved plan, and return without wiring a runner — no node
// spawns, no run directory is created, zero cost. It differs from `lint` in
// exactly one way: lint judges the file alone, while a dry run also holds the
// invocation's --input bindings, so it can additionally prove every input
// reference resolves. Exit 0 (nil) when a real run would start, exit 1 (an
// error carrying the issue count) when it would refuse. Advisory placeholder
// warnings go to warnW through the same warnPlaceholders helper `lint` uses,
// and never affect the exit code.
func dryRunGraph(w, warnW io.Writer, path string, inputs map[string]string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read graph file %q: %w", path, err)
	}

	issues := graph.Lint(data)
	if len(issues) > 0 {
		return reportDryRunIssues(w, path, issues)
	}

	g, err := graph.Parse(data) // Lint found nothing, so this cannot fail
	if err != nil {
		return err
	}
	warnPlaceholders(warnW, path, g)
	printResolvedPlan(w, g)

	if issues := inputIssues(g, inputs); len(issues) > 0 {
		return reportDryRunIssues(w, path, issues)
	}
	fmt.Fprintf(w, "\ndry run: validation passed — no node was executed\n")
	return nil
}

// printResolvedPlan shows the topology a real run would execute: each node id
// with its depends_on edges, in file order — printPlan's shape minus the
// auto-only fields (planning cost, tool ceilings, spec path).
func printResolvedPlan(w io.Writer, g *graph.Graph) {
	fmt.Fprintf(w, "Graph %q (%d nodes):\n", g.Name, len(g.Nodes))
	for _, node := range g.Nodes {
		line := "  - " + node.ID
		if len(node.DependsOn) > 0 {
			line += " (after " + strings.Join(node.DependsOn, ", ") + ")"
		}
		fmt.Fprintln(w, line)
	}
}

// inputIssues proves every {{ inputs.<name> }} reference in the graph's
// templated fields — a node's prompt and cwd, and a verify block's command and
// cwd, exactly the fields the Scheduler interpolates — resolves against the
// bound --input values, through the same Handoff interpolation a real run
// uses. Every node id is seeded with a placeholder artifact path first, and
// any error still on the artifact side is skipped: artifacts materialize while
// the run executes, so they are the one thing a static pass must not judge.
func inputIssues(g *graph.Graph, inputs map[string]string) []error {
	h := handoff.New("", inputs)
	for _, node := range g.Nodes {
		h.Seed(node.ID, node.ID+".out", "")
	}

	var issues []error
	for _, node := range g.Nodes {
		templates := []string{node.Prompt, node.Cwd}
		if v := node.SuccessCheck.Verify; v != nil {
			templates = append(templates, v.Command, v.Cwd)
		}
		for _, tmpl := range templates {
			if _, err := h.Interpolate(tmpl); err != nil && !isArtifactSide(err) {
				issues = append(issues, fmt.Errorf("node %q: %w", node.ID, err))
			}
		}
	}
	return issues
}

// isArtifactSide reports whether err is the artifact half of interpolation —
// e.g. an `| inline` read of a file that only exists once the producing node
// has run.
func isArtifactSide(err error) bool {
	var interp *handoff.InterpolationError
	return errors.As(err, &interp) && interp.Kind == "artifacts"
}

// reportDryRunIssues prints one line per issue, mirroring lintGraph's report,
// and returns the error that maps to exit 1 — stating explicitly that nothing
// ran, since "run" is in the command the user typed.
func reportDryRunIssues(w io.Writer, path string, issues []error) error {
	for _, issue := range issues {
		fmt.Fprintf(w, "%s: %v\n", path, issue)
	}
	noun := "issues"
	if len(issues) == 1 {
		noun = "issue"
	}
	return fmt.Errorf("%s: dry run: %d %s found — no node was executed", path, len(issues), noun)
}
