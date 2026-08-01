package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// --- failure cases first ----------------------------------------------------

func TestDryRunGraph_InvalidGraphFails(t *testing.T) {
	path := writeGraphFile(t, `
name: cyclic
nodes:
  - { id: a, prompt: a, depends_on: [b] }
  - { id: b, prompt: b, depends_on: [a] }
`)
	var out strings.Builder
	err := dryRunGraph(&out, io.Discard, path, nil)
	if err == nil {
		t.Fatal("a cyclic graph must fail a dry run")
	}
	if !strings.Contains(out.String(), "dependency cycle detected") {
		t.Errorf("report should name the cycle:\n%s", out.String())
	}
	if !strings.Contains(err.Error(), "no node was executed") {
		t.Errorf("error should state nothing ran: %v", err)
	}
}

func TestDryRunGraph_MissingInputFails(t *testing.T) {
	path := writeGraphFile(t, `
name: needs-repo
nodes:
  - { id: scan, prompt: "scan {{ inputs.repo }}" }
`)
	var out strings.Builder
	err := dryRunGraph(&out, io.Discard, path, map[string]string{"other": "x"})
	if err == nil {
		t.Fatal("an unresolved {{ inputs.* }} reference must fail a dry run")
	}
	if !strings.Contains(err.Error(), "1 issue found") {
		t.Errorf("error should carry the issue count: %v", err)
	}
	if !strings.Contains(out.String(), "inputs.repo") {
		t.Errorf("report should name the missing input:\n%s", out.String())
	}
}

func TestDryRunGraph_MissingInputInVerifyCommandFails(t *testing.T) {
	path := writeGraphFile(t, `
name: verify-input
nodes:
  - id: build
    prompt: build it
    success_check:
      verify: { command: "test -d {{ inputs.dir }}", timeout: 30s }
`)
	var out strings.Builder
	if err := dryRunGraph(&out, io.Discard, path, nil); err == nil {
		t.Fatal("an unresolved input in a verify command must fail a dry run")
	}
	if !strings.Contains(out.String(), "inputs.dir") {
		t.Errorf("report should name the missing input:\n%s", out.String())
	}
}

func TestDryRunGraph_UnreadableFileFails(t *testing.T) {
	var out strings.Builder
	if err := dryRunGraph(&out, io.Discard, "no-such.yaml", nil); err == nil || !strings.Contains(err.Error(), "read graph file") {
		t.Errorf("a missing graph file should fail with a read error, got: %v", err)
	}
}

// TestMainExitCode_DryRunMapsToZeroAndOne pins the shell contract through
// run()'s subcommand switch: exit 0 when a real run would start, 1 when not.
func TestMainExitCode_DryRunMapsToZeroAndOne(t *testing.T) {
	isolateRunHome(t)
	valid := writeGraphFile(t, "name: ok\nnodes:\n  - { id: a, prompt: a }\n")
	if code := mainExitCode([]string{"run", valid, "--dry-run"}); code != 0 {
		t.Errorf("dry run of a valid graph exited %d, want 0", code)
	}
	invalid := writeGraphFile(t, "name: bad\nnodes:\n  - { id: a, prompt: a, depends_on: [ghost] }\n")
	if code := mainExitCode([]string{"run", invalid, "--dry-run"}); code != 1 {
		t.Errorf("dry run of an invalid graph exited %d, want 1", code)
	}
}

// --- success cases ----------------------------------------------------------

// TestRunGraphWith_DryRunRunsNoNode is the flag's whole point: through the
// real `run` argv path, --dry-run must validate and print the plan while the
// runner seam sees zero invocations and no run directory is created — proof
// the dry run costs nothing.
func TestRunGraphWith_DryRunRunsNoNode(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, `
name: diamond
nodes:
  - { id: root, prompt: "use {{ inputs.repo }}" }
  - { id: left, prompt: left, depends_on: [root] }
  - { id: right, prompt: right, depends_on: [root] }
  - { id: join, prompt: "read {{ artifacts.left }}", depends_on: [left, right] }
`)
	fake := runner.NewFakeRunner(nil)

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path, "--dry-run", "--input", "repo=."}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("dry run of a valid graph returned error: %v", err)
	}

	if n := len(fake.Invocations()); n != 0 {
		t.Fatalf("--dry-run invoked %d nodes, want 0", n)
	}
	if entries, err := os.ReadDir(home); err == nil && len(entries) != 0 {
		t.Errorf("--dry-run created artifacts under OMG_HOME: %v", entries)
	}
	for _, want := range []string{"root", "join (after left, right)", "validation passed", "no node was executed"} {
		if !strings.Contains(out, want) {
			t.Errorf("plan output should contain %q:\n%s", want, out)
		}
	}
}

// TestDryRunGraph_PrintsPlaceholderWarnings pins that a dry run reports the
// same advisory placeholder findings `lint` does, through the same
// warnPlaceholders path, without failing the dry run.
func TestDryRunGraph_PrintsPlaceholderWarnings(t *testing.T) {
	path := writeGraphFile(t, `
name: typo
nodes:
  - { id: dev, prompt: dev }
  - { id: review, prompt: "read {{ artifact.dev }}", depends_on: [dev] }
`)
	var out, warnings strings.Builder
	if err := dryRunGraph(&out, &warnings, path, nil); err != nil {
		t.Fatalf("placeholder warnings must not fail a dry run: %v", err)
	}
	want := "warning: " + path + `: node "review": prompt: `
	if !strings.Contains(warnings.String(), want) {
		t.Errorf("dry run should print the lint warning prefixed %q:\n%s", want, warnings.String())
	}
	if !strings.Contains(out.String(), "validation passed") {
		t.Errorf("the dry run should still pass:\n%s", out.String())
	}
}

// TestDryRunGraph_ToleratesArtifactReferences pins the inputs/artifacts
// asymmetry: {{ artifacts.* }} — including `| inline` reads of files that do
// not exist yet — resolves at run time, so a static pass must not judge it.
func TestDryRunGraph_ToleratesArtifactReferences(t *testing.T) {
	path := writeGraphFile(t, `
name: artifacts-ok
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: "summarize {{ artifacts.a | inline }}", depends_on: [a] }
`)
	var out strings.Builder
	if err := dryRunGraph(&out, io.Discard, path, nil); err != nil {
		t.Fatalf("artifact references must not fail a dry run: %v", err)
	}
	if !strings.Contains(out.String(), "validation passed") {
		t.Errorf("a clean dry run should say so:\n%s", out.String())
	}
}
