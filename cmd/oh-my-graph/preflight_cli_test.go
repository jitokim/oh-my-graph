package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// missingCLIFake is a scripted runner that reports its CLI as absent — the
// machine state of measurement 0037 row 7, reproduced without uninstalling
// anything and without a single real spawn.
func missingCLIFake(t *testing.T) *runner.FakeRunner {
	t.Helper()
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"do the work": {SessionID: "s-dev", Result: "PASS", ExitCode: 0},
	})
	fake.InjectCLIUnavailable(&runner.CLINotFoundError{Runtime: runner.RuntimeClaude, Binary: "claude"})
	return fake
}

// `run` must say the CLI is missing BEFORE it commits to anything. The two
// halves of "before" are what row 7 measured as absent: the runner seam sees no
// invocation, and OMG_HOME holds no run directory for a run that never had a CLI
// to run.
func TestRunGraphWith_MissingCLIIsRefusedBeforeAnyRunDirectory(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, "name: one\nnodes:\n  - { id: dev, prompt: do the work }\n")
	fake := missingCLIFake(t)

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})

	var notFound *runner.CLINotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("run with no CLI on PATH returned %v, want *runner.CLINotFoundError", err)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the preflight let %d node(s) reach the runner", n)
	}
	if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
		t.Errorf("a refused run left artifacts under OMG_HOME: %v", entries)
	}
	// The message the refusal actually prints is the one the operator acts on,
	// and its narrowness is load-bearing: it must not offer a verdict on login.
	if !strings.Contains(err.Error(), "NOT checked: whether it is signed in") {
		t.Errorf("refusal = %q, missing the limit of what was checked", err)
	}
}

// `auto` needs the same refusal one step earlier than `run` does: its first
// spawn is the planner call, and the run leg — the run directory — is opened
// before that call is made.
func TestRunAutoWith_MissingCLIIsRefusedBeforeThePlannerCall(t *testing.T) {
	home := isolateRunHome(t)
	fake := missingCLIFake(t)

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"tidy the docs", "--accept-no-build-evidence"}, fake, browser.NewFakeOpener(), os.Stdout)
	})

	var notFound *runner.CLINotFoundError
	if !errors.As(err, &notFound) {
		t.Fatalf("auto with no CLI on PATH returned %v, want *runner.CLINotFoundError", err)
	}
	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("the planner call was made anyway: %v", calls)
	}
	if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
		t.Errorf("a refused auto left artifacts under OMG_HOME: %v", entries)
	}
}

// --dry-run spawns nothing, so it must keep working on a machine that has no
// model CLI at all. A preflight placed one line higher would have taken this
// away, and taken away the only free thing a newcomer can do before installing
// anything.
func TestRunGraphWith_DryRunStillWorksWithNoCLIOnPath(t *testing.T) {
	isolateRunHome(t)
	path := writeGraphFile(t, "name: one\nnodes:\n  - { id: dev, prompt: do the work }\n")
	fake := missingCLIFake(t)

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path, "--dry-run"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--dry-run refused for a missing CLI it never spawns: %v", err)
	}
	if !strings.Contains(out, "validation passed") {
		t.Errorf("--dry-run output lost its verdict:\n%s", out)
	}
}

// The preflight must never displace ADR 0030's refusal. A directory that earns
// both — a build system, no --verify-cmd, no declaration — still gets the
// evidence refusal and still exits 3, whether or not a CLI is installed. Both
// refuse before anything spends, so the order is not about money; it is about
// the exit code a script reads, and exit 3 exists precisely so "add a flag" is
// distinguishable from everything else.
func TestRunAutoWith_MissingCLIDoesNotDisplaceTheBuildEvidenceRefusal(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "gradlew")
	fake := missingCLIFake(t)

	var err error
	captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section"}, fake, browser.NewFakeOpener(), os.Stdout)
	})

	var missingEvidence *coordinator.MissingBuildEvidenceError
	if !errors.As(err, &missingEvidence) {
		t.Fatalf("auto returned %v, want ADR 0030's refusal even with no CLI installed", err)
	}
	var notFound *runner.CLINotFoundError
	if errors.As(err, &notFound) {
		t.Fatalf("the CLI preflight answered a question ADR 0030 owns: %v", err)
	}
	if code := exitCodeForError(err); code != 3 {
		t.Errorf("exit code = %d, want 3 — the refusal's own code, unchanged by the preflight", code)
	}
}

// A verdict about the graph must not be displaced by a verdict about the
// machine: a newcomer with a broken YAML and no CLI installed still needs to be
// told about the YAML, which is the one thing they can fix without installing
// anything.
func TestRunGraphWith_MissingCLIDoesNotDisplaceAGraphVerdict(t *testing.T) {
	isolateRunHome(t)
	path := writeGraphFile(t, "name: cyclic\nnodes:\n  - { id: a, prompt: a, depends_on: [b] }\n  - { id: b, prompt: b, depends_on: [a] }\n")
	fake := missingCLIFake(t)

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("a cyclic graph was accepted")
	}
	var notFound *runner.CLINotFoundError
	if errors.As(err, &notFound) {
		t.Fatalf("the CLI preflight spoke over DAG validation: %v", err)
	}
	if !strings.Contains(strings.ToLower(err.Error()), "cycle") {
		t.Errorf("error = %q, want the cycle the reader can actually fix", err)
	}
}
