package main

import (
	"fmt"
	"io"
	"os"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// runLint is the `lint` subcommand: parse argv and statically validate one
// graph file without running it.
func runLint(args []string) error {
	return runLintRuntime(runner.RuntimeClaude, args)
}

func runLintRuntime(runtime runner.Runtime, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("lint: missing graph file (usage: oh-my-graph lint <graph.yaml>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("lint: unexpected argument %q (usage: oh-my-graph lint <graph.yaml>)", args[1])
	}
	return lintGraphForRuntime(os.Stdout, os.Stderr, args[0], runtime)
}

// lintGraph reports every load-time problem graph.LintLoadFile finds — fragment
// resolution failures (an unresolvable `use:`, a broken binding — ADR 0013)
// plus every structural check `run` enforces on the RESOLVED graph (DAG/cycle,
// unknown depends_on ids, the session-handoff parent rule, verify blocks,
// agent and worktree names) — without executing anything: no node spawns, no
// run directory is created, zero cost. Where `run` stops at the first
// violation, lint prints them all, so a broken graph is fixable in one pass.
// A valid graph prints one disclosure line per resolved `use:` — the same
// line `run` prints, naming the fragment's source file and its own
// description — then one confirmation line, and returns nil (exit 0); an
// invalid one prints one line per issue to w and returns an error carrying
// the count, which mainExitCode turns into exit 1. Errors first, advisories
// after: fragment advisories (drift smell in a fragment file) print as
// `warning:` lines either way and never touch the exit code.
//
// A structurally valid graph is additionally swept for advisories: for
// placeholder-like {{ ... }} tokens that will not resolve
// (handoff.LintPlaceholders), for session-handoff nodes whose resume may
// quietly start cold (handoff.LintSessions — a cwd/worktree differing from
// the session-parent's, a retry on a session node), and for verdicts nothing
// checks (handoff.LintVerdicts — a prompt demanding a token with no
// `result_matches` to read it, or a `result_matches` that silently dropped
// the node's implied exit-zero guard), for nodes that can observe no tool
// denial at all (handoff.LintToolGrants — neither an `allowed_tools` grant nor
// a `success_check.verify`, so a denied tool leaves only prose a
// `result_matches` passes on), for a `success_check.verify.command` that
// splices a model's own text into the shell command line the engine runs
// (handoff.LintVerifyInlining — `{{ artifacts.<id> | inline }}` or
// `{{ feedback.<id> }}`, where the filterless artifacts token would be the
// engine's own file path), and for a feedback arc that cannot
// reach a producer its declarer fans in from (graph.LintFeedbackReach — the
// loop would re-judge an unchanged artifact until its rounds are spent).
// Those are printed to
// warnW as `warning:` lines and never touch the exit code. At run time the
// warned placeholder classes diverge: a MALFORMED token passes through
// verbatim (a prompt may legitimately contain literal {{ }} text), while a
// well-formed reference to an undeclared input or unknown node fails its
// node with an InterpolationError when it runs — the warning is the cheap
// early copy of that failure. `run --dry-run` prints the same warnings
// through the same helper (see dryRunGraph).
func lintGraph(w, warnW io.Writer, path string) error {
	return lintGraphForRuntime(w, warnW, path, runner.RuntimeClaude)
}

func lintGraphForRuntime(w, warnW io.Writer, path string, runtime runner.Runtime) error {
	issues, fragmentAdvisories, loaded, err := graph.LintLoadFile(path)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		runtimeWarnings, err := runner.ValidateGraphForRuntime(runtime, loaded.Graph)
		warnRuntimePreflight(warnW, path, runtimeWarnings)
		if err != nil {
			return err
		}
		printFragmentResolutions(w, loaded.Resolutions)
		warnAdvisories(warnW, path, loaded.Graph)
		warnFragmentAdvisories(warnW, path, fragmentAdvisories)
		noteCodexRuntimePolicy(w, runtime, loaded.Graph, false)
		fmt.Fprintf(w, "%s: valid\n", path)
		return nil
	}
	for _, issue := range issues {
		fmt.Fprintf(w, "%s: %v\n", path, issue)
	}
	warnFragmentAdvisories(warnW, path, fragmentAdvisories)
	noun := "issues"
	if len(issues) == 1 {
		noun = "issue"
	}
	return fmt.Errorf("%s: %d %s found", path, len(issues), noun)
}

// warnAdvisories prints one `warning:` line per advisory finding in an
// already-validated graph — the shared reporting half of `lint` and
// `run --dry-run`, covering the five handoff sweeps (unresolvable
// placeholder-like tokens, session-handoff resumes that may start cold,
// verdicts a node's own success_check cannot read, nodes that can observe
// no tool denial, and a verify command carrying a model's own text into the
// shell) plus the graph-topology
// one (a feedback arc that cannot reach one of its declarer's producers).
// Warnings are advice only: they never affect any exit code.
func warnAdvisories(warnW io.Writer, path string, g *graph.Graph) {
	advisories := append(handoff.LintPlaceholders(g), handoff.LintSessions(g)...)
	advisories = append(advisories, handoff.LintToolGrants(g)...)
	advisories = append(advisories, handoff.LintVerifyInlining(g)...)
	for _, warning := range append(advisories, handoff.LintVerdicts(g)...) {
		fmt.Fprintf(warnW, "warning: %s: %s\n", path, warning)
	}
	for _, advisory := range g.LintFeedbackReach() {
		fmt.Fprintf(warnW, "warning: %s: %s\n", path, advisory)
	}
}

// warnRuntimePreflight prints one `warning:` line per runtime-preflight
// warning — today, a `budget_usd` the selected runtime cannot evaluate
// (ADR 0026). It is called at EVERY runner.ValidateGraphForRuntime call site,
// including the ones whose call also returns an error: the two verdicts are
// independent (a graph refused for `agent:` may also carry an inapplicable
// cap), and a warning nobody prints is exactly the silent drop the split
// exists to avoid. path may be empty, for the callers that judge a graph in
// memory rather than a file on disk. Advice only: never an exit code.
func warnRuntimePreflight(warnW io.Writer, path string, warnings []string) {
	for _, warning := range warnings {
		if path == "" {
			fmt.Fprintf(warnW, "warning: %s\n", warning)
			continue
		}
		fmt.Fprintf(warnW, "warning: %s: %s\n", path, warning)
	}
}

// warnFragmentAdvisories prints one `warning:` line per fragment-file
// advisory (ADR 0013 — e.g. a declared substitution point the fragment body
// never references). Same standing as warnAdvisories: advice only, never an
// exit code.
func warnFragmentAdvisories(warnW io.Writer, path string, advisories []graph.FragmentAdvisory) {
	for _, advisory := range advisories {
		fmt.Fprintf(warnW, "warning: %s: %s\n", path, advisory)
	}
}
