package main

import (
	"fmt"
	"io"
	"os"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
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
	return lintGraph(os.Stdout, os.Stderr, args[0])
}

// lintGraph reports every load-time problem graph.LintFile finds — fragment
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
// the node's implied exit-zero guard). Those are printed to
// warnW as `warning:` lines and never touch the exit code. At run time the
// warned placeholder classes diverge: a MALFORMED token passes through
// verbatim (a prompt may legitimately contain literal {{ }} text), while a
// well-formed reference to an undeclared input or unknown node fails its
// node with an InterpolationError when it runs — the warning is the cheap
// early copy of that failure. `run --dry-run` prints the same warnings
// through the same helper (see dryRunGraph).
func lintGraph(w, warnW io.Writer, path string) error {
	issues, fragmentAdvisories, err := graph.LintFile(path)
	if err != nil {
		return err
	}
	if len(issues) == 0 {
		loaded, err := graph.LoadFile(path) // LintFile found nothing, so this cannot fail
		if err != nil {
			return err
		}
		printFragmentResolutions(w, loaded.Resolutions)
		warnAdvisories(warnW, path, loaded.Graph)
		warnFragmentAdvisories(warnW, path, fragmentAdvisories)
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
// `run --dry-run`, covering all three handoff sweeps: unresolvable
// placeholder-like tokens, session-handoff resumes that may start cold, and
// verdicts a node's own success_check cannot read.
// Warnings are advice only: they never affect any exit code.
func warnAdvisories(warnW io.Writer, path string, g *graph.Graph) {
	advisories := append(handoff.LintPlaceholders(g), handoff.LintSessions(g)...)
	for _, warning := range append(advisories, handoff.LintVerdicts(g)...) {
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
