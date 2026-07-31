package main

import (
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
	err := lintGraph(&out, path)
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
	err := lintGraph(&out, path)
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
	err := lintGraph(&out, path)
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
	err := lintGraph(&out, filepath.Join(t.TempDir(), "no-such.yaml"))
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
	err := lintGraph(&out, path)
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
	if err := lintGraph(&out, path); err != nil {
		t.Fatalf("valid graph failed lint: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("a clean lint should say so:\n%s", out.String())
	}
}
