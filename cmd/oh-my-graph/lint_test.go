package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeGraphFile drops a graph fixture into a temp dir and returns its path.
func writeGraphFile(t *testing.T, yaml string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "graph.yaml")
	if err := os.WriteFile(path, []byte(yaml), 0o644); err != nil {
		t.Fatalf("write graph fixture: %v", err)
	}
	return path
}

// --- failure cases first ----------------------------------------------------

func TestLintGraph_CyclicGraphFails(t *testing.T) {
	path := writeGraphFile(t, `
name: cyclic
nodes:
  - { id: a, prompt: a, depends_on: [b] }
  - { id: b, prompt: b, depends_on: [a] }
`)
	var out strings.Builder
	err := lintGraph(&out, io.Discard, path)
	if err == nil {
		t.Fatal("a cyclic graph must fail lint")
	}
	if !strings.Contains(out.String(), "dependency cycle detected") {
		t.Errorf("report should name the cycle:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "1 issue found") {
		t.Errorf("error should carry the issue count: %v", err)
	}
}

func TestLintGraph_BadSessionHandoffFails(t *testing.T) {
	path := writeGraphFile(t, `
name: session-root
nodes:
  - { id: a, prompt: a, handoff: session }
`)
	var out strings.Builder
	err := lintGraph(&out, io.Discard, path)
	if err == nil {
		t.Fatal("a session-handoff root must fail lint")
	}
	if !strings.Contains(out.String(), "handoff: session") {
		t.Errorf("report should explain the session-handoff rule:\n%s", out.String())
	}
}

// TestLintGraph_ReportsEveryIssue is the reason lint exists over just running
// `run` against the file: all problems in one pass, not one per attempt.
func TestLintGraph_ReportsEveryIssue(t *testing.T) {
	path := writeGraphFile(t, `
name: broken
nodes:
  - { id: a, prompt: a, depends_on: [b] }
  - { id: b, prompt: b, depends_on: [a] }
  - { id: c, prompt: c, depends_on: [ghost] }
  - { id: d, prompt: d, handoff: session }
`)
	var out strings.Builder
	err := lintGraph(&out, io.Discard, path)
	if err == nil {
		t.Fatal("a broken graph must fail lint")
	}
	for _, want := range []string{"ghost", "cycle", "session"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("report should mention %q:\n%s", want, out.String())
		}
	}
	if !strings.Contains(err.Error(), "3 issues found") {
		t.Errorf("error should carry the issue count: %v", err)
	}
}

func TestLintGraph_UnreadableFileFails(t *testing.T) {
	var out strings.Builder
	err := lintGraph(&out, io.Discard, filepath.Join(t.TempDir(), "no-such.yaml"))
	if err == nil {
		t.Fatal("a missing graph file must fail lint")
	}
	if !strings.Contains(err.Error(), "read graph file") {
		t.Errorf("error should say the file could not be read: %v", err)
	}
}

func TestLintGraph_MalformedYAMLFails(t *testing.T) {
	path := writeGraphFile(t, "name: [unterminated")
	var out strings.Builder
	err := lintGraph(&out, io.Discard, path)
	if err == nil {
		t.Fatal("malformed YAML must fail lint")
	}
	if !strings.Contains(out.String(), "parse graph YAML") {
		t.Errorf("report should carry the decode error:\n%s", out.String())
	}
}

func TestRunLint_ArgvErrors(t *testing.T) {
	if err := runLint(nil); err == nil || !strings.Contains(err.Error(), "usage") {
		t.Errorf("missing graph file should show usage, got: %v", err)
	}
	if err := runLint([]string{"a.yaml", "extra"}); err == nil || !strings.Contains(err.Error(), "extra") {
		t.Errorf("an extra argument should be named, got: %v", err)
	}
}

// TestMainExitCode_LintMapsToOneAndZero pins the shell contract end to end
// through run()'s subcommand switch: exit 0 for a valid graph, exit 1 for an
// invalid one.
func TestMainExitCode_LintMapsToOneAndZero(t *testing.T) {
	valid := writeGraphFile(t, "name: ok\nnodes:\n  - { id: a, prompt: a }\n")
	if code := mainExitCode([]string{"lint", valid}); code != 0 {
		t.Errorf("lint of a valid graph exited %d, want 0", code)
	}
	invalid := writeGraphFile(t, "name: bad\nnodes:\n  - { id: a, prompt: a, depends_on: [ghost] }\n")
	if code := mainExitCode([]string{"lint", invalid}); code != 1 {
		t.Errorf("lint of an invalid graph exited %d, want 1", code)
	}
}

// --- placeholder warnings ---------------------------------------------------

// TestLintGraph_WarnsOnPlaceholderTypos pins the warning contract: a
// placeholder-like token that will not resolve is reported to the warning
// writer in `warning: <graph>: node "<id>": ...` form, while the graph still
// lints as valid — nil error, "valid" on stdout, warnings never on stdout.
func TestLintGraph_WarnsOnPlaceholderTypos(t *testing.T) {
	path := writeGraphFile(t, `
name: typo
nodes:
  - { id: dev, prompt: dev }
  - { id: review, prompt: "read {{ artifacts.dev | inlin }}", depends_on: [dev] }
`)
	var out, warnings strings.Builder
	if err := lintGraph(&out, &warnings, path); err != nil {
		t.Fatalf("placeholder warnings must not fail lint: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("the graph must still lint as valid:\n%s", out.String())
	}
	want := "warning: " + path + `: node "review": prompt: `
	if !strings.Contains(warnings.String(), want) {
		t.Errorf("warning should be prefixed %q:\n%s", want, warnings.String())
	}
	if !strings.Contains(warnings.String(), "{{ artifacts.dev | inlin }}") {
		t.Errorf("warning should quote the offending token:\n%s", warnings.String())
	}
	if strings.Contains(out.String(), "warning:") {
		t.Errorf("warnings must not leak to stdout:\n%s", out.String())
	}
}

// TestLintGraph_CleanPlaceholdersStaySilent pins the silent side: declared
// inputs, ancestor artifacts, and deliberate literal {{ }} text produce no
// warnings at all.
func TestLintGraph_CleanPlaceholdersStaySilent(t *testing.T) {
	path := writeGraphFile(t, `
name: clean
inputs: [repo]
nodes:
  - { id: dev, prompt: "work in {{ inputs.repo }}; explain {{ mustache }} syntax" }
  - { id: review, prompt: "read {{ artifacts.dev | inline }}", depends_on: [dev] }
`)
	var out, warnings strings.Builder
	if err := lintGraph(&out, &warnings, path); err != nil {
		t.Fatalf("valid graph failed lint: %v", err)
	}
	if warnings.String() != "" {
		t.Errorf("clean placeholders should warn nothing, got:\n%s", warnings.String())
	}
}

// TestMainExitCode_PlaceholderWarningsKeepExitZero pins that warnings are
// advice only: through the real argv path, a graph that only has placeholder
// warnings still exits 0.
func TestMainExitCode_PlaceholderWarningsKeepExitZero(t *testing.T) {
	path := writeGraphFile(t, "name: warned\nnodes:\n  - { id: a, prompt: \"use {{ inputs.ghost }}\" }\n")
	if code := mainExitCode([]string{"lint", path}); code != 0 {
		t.Errorf("lint with only placeholder warnings exited %d, want 0", code)
	}
}

// --- success case -----------------------------------------------------------

func TestLintGraph_ValidGraphPasses(t *testing.T) {
	path := writeGraphFile(t, `
name: clean
nodes:
  - { id: root, prompt: root }
  - { id: left, prompt: left, depends_on: [root] }
  - { id: right, prompt: right, depends_on: [root] }
  - { id: join, prompt: join, depends_on: [left, right] }
`)
	var out strings.Builder
	if err := lintGraph(&out, io.Discard, path); err != nil {
		t.Fatalf("valid graph failed lint: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("a clean lint should say so:\n%s", out.String())
	}
}
