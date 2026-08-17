package main

import (
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// dryRunGraph is the `run --dry-run` path: load the graph through the same
// fragment-resolving graph.LintLoadFile pass the `lint` subcommand uses (ADR
// 0013), report every issue — fragment resolution failures plus the resolved
// graph's structural problems — resolve the {{ inputs.* }} references against
// the bound --input values, print the fragment disclosure `lint` and `run`
// print plus the resolved plan, and return without
// wiring a runner: no node spawns, no run directory is created, zero cost. It
// differs from `lint` in exactly one way: lint judges the file alone, while a
// dry run also holds the invocation's --input bindings, so it can
// additionally prove every input reference resolves. Exit 0 (nil) when a real
// run would start, exit 1 (an error carrying the issue count) when it would
// refuse. Advisory warnings (placeholders, session-handoff cold-resume risks,
// fragment drift smell) go to warnW through the same warnAdvisories /
// warnFragmentAdvisories helpers `lint` uses, and never affect the exit code.
func dryRunGraph(w, warnW io.Writer, path string, inputs map[string]string) error {
	return dryRunGraphForRuntime(w, warnW, path, inputs, runner.RuntimeClaude)
}

func dryRunGraphForRuntime(w, warnW io.Writer, path string, inputs map[string]string, runtime runner.Runtime) error {
	issues, fragmentAdvisories, loaded, err := graph.LintLoadFile(path)
	if err != nil {
		return err
	}
	if len(issues) > 0 {
		err := reportDryRunIssues(w, path, issues)
		warnFragmentAdvisories(warnW, path, fragmentAdvisories)
		return err
	}

	g := loaded.Graph
	runtimeWarnings, err := runner.ValidateGraphForRuntime(runtime, g)
	warnRuntimePreflight(warnW, path, runtimeWarnings)
	if err != nil {
		return err
	}
	warnAdvisories(warnW, path, g)
	warnFragmentAdvisories(warnW, path, fragmentAdvisories)
	// The disclosure `lint` and `run` both print, on the third command that
	// resolves fragments — and the one that had it missing. A dry run is where a
	// reader checks what a graph WILL do before paying for it, so a splice
	// announced under both neighbours and silent here is the disclosure
	// contradicting itself, the same way the advisory channel above was.
	printFragmentResolutions(w, loaded.Resolutions)
	printResolvedPlan(w, g)
	noteCodexRuntimePolicy(w, runtime, g, false)

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
//
// The seeded placeholder path is the real writer's computation
// (handoff.SanitizeNodeID), not a second spelling of it: a spliced node's id
// carries a '/' (ADR 0027), and `node.ID+".out"` would print a path no run
// ever writes.
func inputIssues(g *graph.Graph, inputs map[string]string) []error {
	h := handoff.New("", inputs)
	for _, node := range g.Nodes {
		h.Seed(node.ID, handoff.SanitizeNodeID(node.ID)+".out", "")
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
