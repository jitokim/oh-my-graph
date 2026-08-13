package handoff

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// TestLintVerifyInlining_WhatReachesTheShell drives the predicate through one
// two-node graph per token shape. The question the sweep asks is not "did you
// use a filter" but "does this command line carry text a MODEL wrote", so the
// cases are grouped by where the substituted value comes from: a node's reply,
// a feedback payload, an engine-computed path, an invocation input.
func TestLintVerifyInlining_WhatReachesTheShell(t *testing.T) {
	cases := []struct {
		name    string
		command string
		warns   bool
	}{
		{
			name:    "an inlined artifact is a model's reply in a command line",
			command: "test {{ artifacts.impl | inline }}",
			warns:   true,
		},
		{
			// Whitespace is not the predicate: the shared runtime pattern
			// tolerates a tight token, and so must this.
			name:    "a tight token inlines exactly the same text",
			command: "test {{artifacts.impl|inline}}",
			warns:   true,
		},
		{
			// The default's whole point. The engine computes this path from the
			// run directory and a sanitized node id, so no model chose any part
			// of it — and it is the fix the warning names.
			name:    "the default filter is a path the engine computed",
			command: `grep -q PASS {{ artifacts.impl }}`,
			warns:   false,
		},
		{
			// The shipped shape: backlog-batch.yaml's e2e nodes run
			// {{ inputs.checks_command }}. An input comes from the user's own
			// --input, so it has the standing the command line itself has.
			name:    "an input is the user's own string",
			command: "{{ inputs.checks_command }}",
			warns:   false,
		},
		{
			name:    "a command with no tokens at all",
			command: "make local",
			warns:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: verify-inlining
inputs: [checks_command]
nodes:
  - id: impl
    prompt: do the work
  - id: check
    prompt: check the work
    depends_on: [impl]
    success_check:
      exit_zero: true
      verify: { command: "`+tc.command+`" }
`)
			warnings := LintVerifyInlining(g)
			if !tc.warns {
				if len(warnings) != 0 {
					t.Fatalf("expected no inlining warning for %q, got %v", tc.command, warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning for %q, got %v", tc.command, warnings)
			}
			w := warnings[0]
			if w.NodeID != "check" || w.Field != "success_check.verify.command" {
				t.Fatalf("warning = %+v, want node check field success_check.verify.command", w)
			}
			// The useful message is what the author gets, not that a filter was
			// used: whose text this is, that it reaches a shell, and the fix.
			for _, want := range []string{"reply", "through your shell", "Drop the filter", "FILE PATH"} {
				if !strings.Contains(w.Detail, want) {
					t.Fatalf("detail %q should contain %q", w.Detail, want)
				}
			}
		})
	}
}

// TestLintVerifyInlining_TheDefaultFilterIsAPath is the assertion the whole
// message rests on, checked against Interpolate itself rather than against the
// docstring: a filterless artifacts token resolves to the persisted file path,
// and only `| inline` yields the node's reply text. If that ever inverts, the
// warning would be naming the wrong half of the schema, and this fails first.
func TestLintVerifyInlining_TheDefaultFilterIsAPath(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("impl", "; rm -rf /", "sess-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	path, err := h.Interpolate("{{ artifacts.impl }}")
	if err != nil {
		t.Fatalf("interpolate default: %v", err)
	}
	if path != filepath.Join(dir, "impl.out") {
		t.Fatalf("default filter resolved to %q, want the artifact file path", path)
	}
	inlined, err := h.Interpolate("{{ artifacts.impl | inline }}")
	if err != nil {
		t.Fatalf("interpolate inline: %v", err)
	}
	if inlined != "; rm -rf /" {
		t.Fatalf("| inline resolved to %q, want the node's reply verbatim", inlined)
	}
}

// TestLintVerifyInlining_FeedbackHasNoPathToFallBackOn pins the second token
// shape and its DIFFERENT message. A feedback placeholder always inlines the
// declarer's payload and graph.Validate refuses a filter on it, so "drop the
// filter" would be advice the author cannot take.
func TestLintVerifyInlining_FeedbackHasNoPathToFallBackOn(t *testing.T) {
	g := parseGraph(t, `
name: feedback-in-verify
nodes:
  - id: impl
    prompt: do the work
  - id: review
    prompt: |
      Previous findings: {{ feedback.review }}
    depends_on: [impl]
    feedback: { rerun: impl, max: 2 }
    success_check:
      exit_zero: true
      verify: { command: "test {{ feedback.review }}" }
`)
	warnings := LintVerifyInlining(g)
	if len(warnings) != 1 {
		t.Fatalf("expected exactly one warning, got %v", warnings)
	}
	w := warnings[0]
	if w.NodeID != "review" || w.Field != "success_check.verify.command" {
		t.Fatalf("warning = %+v, want node review field success_check.verify.command", w)
	}
	if strings.Contains(w.Detail, "Drop the filter") {
		t.Fatalf("detail %q offers a fix a feedback token cannot take — it has no filter", w.Detail)
	}
	for _, want := range []string{"feedback payload", "through your shell", "PROMPT"} {
		if !strings.Contains(w.Detail, want) {
			t.Fatalf("detail %q should contain %q", w.Detail, want)
		}
	}
}

// TestLintVerifyInlining_IsAboutTheCommandOnly pins the sweep's two scope
// edges. A prompt is where inlining a reply is the DESIGNED use — every shipped
// template does it — and a verify cwd is not shell-interpreted: it becomes
// exec.Cmd.Dir, so nothing in a reply there is parsed as syntax and no part of
// it can become a command of its own.
func TestLintVerifyInlining_IsAboutTheCommandOnly(t *testing.T) {
	g := parseGraph(t, `
name: scope
nodes:
  - id: impl
    prompt: do the work
  - id: check
    prompt: |
      The implementer reported: {{ artifacts.impl | inline }}
    depends_on: [impl]
    success_check:
      exit_zero: true
      verify: { command: "make local", cwd: "{{ artifacts.impl | inline }}" }
`)
	if warnings := LintVerifyInlining(g); len(warnings) != 0 {
		t.Fatalf("only the command is swept, got %v", warnings)
	}
}

// TestLintVerifyInlining_LeavesUnresolvableTokensToTheOtherSweep pins the
// boundary the two docstrings state. A token that can never substitute — the
// node's own artifact, or a node the graph does not have — is LintPlaceholders'
// finding and only its finding: nothing is spliced by a token that never
// resolves, so this sweep's sentence would be false and its fix ("drop the
// filter") would be untakeable, since the filterless form of a reference that
// names nothing does not resolve either. Both halves are asserted, because
// silence here is only correct while the OTHER sweep still speaks.
func TestLintVerifyInlining_LeavesUnresolvableTokensToTheOtherSweep(t *testing.T) {
	cases := []struct {
		name    string
		command string
	}{
		{
			name:    "a node's own artifact cannot exist while it runs",
			command: "test {{ artifacts.check | inline }}",
		},
		{
			name:    "a reference to no node at all",
			command: "test {{ artifacts.nowhere | inline }}",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: unresolvable
nodes:
  - id: impl
    prompt: do the work
  - id: check
    prompt: check the work
    depends_on: [impl]
    success_check:
      exit_zero: true
      verify: { command: "`+tc.command+`" }
`)
			if warnings := LintVerifyInlining(g); len(warnings) != 0 {
				t.Fatalf("a token that never resolves splices nothing, got %v", warnings)
			}
			if warnings := LintPlaceholders(g); len(warnings) != 1 {
				t.Fatalf("the unresolvable token must still be reported by LintPlaceholders, got %v", warnings)
			}
		})
	}
}

// TestLintVerifyInlining_WarnsOnANonAncestorToo is the other side of that
// boundary. A non-ancestor's artifact MAY exist by the time this node runs —
// LintPlaceholders says only that it may not — so if it does resolve, a model's
// reply is on the command line, and the token earns a line from each sweep for
// two different reasons.
func TestLintVerifyInlining_WarnsOnANonAncestorToo(t *testing.T) {
	g := parseGraph(t, `
name: non-ancestor
nodes:
  - id: impl
    prompt: do the work
  - id: sibling
    prompt: something else
  - id: check
    prompt: check the work
    depends_on: [impl]
    success_check:
      exit_zero: true
      verify: { command: "test {{ artifacts.sibling | inline }}" }
`)
	if warnings := LintVerifyInlining(g); len(warnings) != 1 {
		t.Fatalf("a non-ancestor reference can still resolve and splice, got %v", warnings)
	}
}

// TestLintVerifyInlining_ShippedGraphsAreClean is the sweep's own regression
// test, mirroring TestLintToolGrants_ShippedGraphsAreClean: no graph this repo
// ships may hand a model's text to the verify seam. It is also half of the
// shipped measurement — the sweep found zero hits here — so a template that
// later grows one fails in the test suite rather than in a paid run. Loaded
// through graph.LoadFile so `use:` fragments are resolved in (ADR 0013), which
// is what brings e2e-verify.yaml's own verify block into the population.
func TestLintVerifyInlining_ShippedGraphsAreClean(t *testing.T) {
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
			if warnings := LintVerifyInlining(loaded.Graph); len(warnings) != 0 {
				t.Fatalf("%s: shipped graph should hand no model text to the verify seam, got %v", path, warnings)
			}
		})
	}
}
