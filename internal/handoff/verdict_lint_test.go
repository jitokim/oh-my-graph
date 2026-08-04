package handoff

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// TestLintVerdicts_UncheckedVerdict drives the first class through one
// single-node graph: a prompt that demands a verdict token, against the
// success_check that is (or is not) able to read one. This is the
// merge-shepherd shape — the node asked for a merge SHA under
// `{ exit_zero: true }`, replied with a promise, and passed.
func TestLintVerdicts_UncheckedVerdict(t *testing.T) {
	cases := []struct {
		name  string
		node  string // YAML fields spliced into the single node
		warns bool
	}{
		{
			name:  "verdict demanded, nothing reads it",
			node:  "prompt: \"START your reply with the bare token MERGED and the SHA\"\n    success_check: { exit_zero: true }",
			warns: true,
		},
		{
			name:  "verdict demanded with no success_check at all",
			node:  `prompt: "Your reply is exactly the seven bare characters COVERED"`,
			warns: true,
		},
		{
			name:  "lowercase phrasing is caught too",
			node:  "prompt: \"When you are done, start your reply with DONE\"\n    success_check: { exit_zero: true }",
			warns: true,
		},
		{
			name:  "verdict demanded and matched stays silent",
			node:  "prompt: \"START your reply with the bare word DONE\"\n    success_check: { exit_zero: true, result_matches: '^DONE\\b' }",
			warns: false,
		},
		{
			name:  "a prompt demanding no verdict stays silent",
			node:  "prompt: \"Implement the task and commit it. Start work on the tests first.\"\n    success_check: { exit_zero: true }",
			warns: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, "name: verdicts\nnodes:\n  - id: only\n    "+tc.node+"\n")
			warnings := LintVerdicts(g)
			if !tc.warns {
				for _, w := range warnings {
					if strings.Contains(w.Detail, "nothing reads the answer") {
						t.Fatalf("expected no unchecked-verdict warning, got %v", warnings)
					}
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %v", warnings)
			}
			w := warnings[0]
			if w.NodeID != "only" || w.Field != "success_check" {
				t.Fatalf("warning = %+v, want node only field success_check", w)
			}
			if !strings.Contains(w.Detail, "nothing reads the answer") {
				t.Fatalf("detail %q should name the unchecked verdict", w.Detail)
			}
		})
	}
}

// TestLintVerdicts_ResultMatchesDropsExitZero pins the second class: exit-zero
// is implied only for a node with NO success_check, so adding result_matches
// to such a node deletes its exit-code guard. That is exactly how a review
// commit once added one guard and removed another in the same line.
func TestLintVerdicts_ResultMatchesDropsExitZero(t *testing.T) {
	g := parseGraph(t, `
name: dropped-guard
nodes:
  - id: implicit
    prompt: no check at all, so exit_zero is the default
  - id: dropped
    prompt: gains a pattern and loses its exit code
    success_check: { result_matches: '^DONE' }
  - id: both
    prompt: names both predicates
    success_check: { exit_zero: true, result_matches: '^DONE' }
`)
	warnings := LintVerdicts(g)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %v", warnings)
	}
	w := warnings[0]
	if w.NodeID != "dropped" || w.Field != "success_check" {
		t.Fatalf("warning = %+v, want node dropped field success_check", w)
	}
	if !strings.Contains(w.Detail, "exit code is now unchecked") {
		t.Fatalf("detail %q should say the exit code went unchecked", w.Detail)
	}
}

// TestLintVerdicts_ShippedGraphsAreClean is the sweep's own regression test:
// every graph this repo ships must already satisfy both rules, so a node that
// demands a verdict nothing reads fails here rather than in a paid run. It
// loads through graph.LoadFile so each template's `use:` fragments are
// resolved into it (ADR 0013) — the two review fragments demand verdicts of
// their own, and this is where they get judged.
func TestLintVerdicts_ShippedGraphsAreClean(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "graphs", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped graphs found to sweep: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			loaded, err := graph.LoadFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if warnings := LintVerdicts(loaded.Graph); len(warnings) != 0 {
				t.Fatalf("%s: shipped graph should have no verdict advisories, got %v", path, warnings)
			}
		})
	}
}
