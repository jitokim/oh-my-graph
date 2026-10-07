package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// #356: the undeclared-input warning wired onto `run` and `run --dry-run`,
// driven through argv against a FakeRunner. The pure helper is judged in
// undeclaredinput_test.go.

// secretValue356 is unique enough that finding it anywhere is a finding.
const secretValue356 = "v4lue-356-SECRET"

// nodeRanMarker356 is written to stderr by the fake the moment a node runs,
// so a test can read off whether a line came before or after it.
const nodeRanMarker356 = "FAKE-NODE-RAN-356"

// markingRunner is firstWordRunner whose every call first writes
// nodeRanMarker356 to the stderr the test is capturing.
func markingRunner(words ...string) *runner.FakeRunner {
	fake := firstWordRunner(words...)
	key := fake.KeyFn
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		fmt.Fprintln(os.Stderr, nodeRanMarker356)
		return key(spec)
	}
	return fake
}

func undeclaredLine356(graphPath, key, source string) string {
	return fmt.Sprintf("warning: %s: input %q (from %s) is not declared in the graph's inputs list; it is bound anyway", graphPath, key, source)
}

func TestUndeclaredInput356_InputFlagKeyWarnsBeforeAnyNodeRuns(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	fake := markingRunner("scan")

	stdout, stderr, err := runWithInputs(t, fake, graphPath,
		"--input", "repo=/r", "--input", "ticket=T", "--input", "zzz="+secretValue356)
	if err != nil {
		t.Fatalf("run: %v (exit status must be today's: success)", err)
	}
	want := undeclaredLine356(graphPath, "zzz", "--input") + "\n"
	at := strings.Index(stderr, want)
	if at < 0 {
		t.Fatalf("stderr missing %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, "did you mean") {
		t.Errorf("far-off key got a suggestion:\n%s", stderr)
	}
	ran := strings.Index(stderr, nodeRanMarker356)
	if ran < 0 || ran < at {
		t.Errorf("warning at %d, first node at %d: the warning must come before any node runs:\n%s", at, ran, stderr)
	}
	if len(fake.Invocations()) != 1 {
		t.Errorf("fake saw %d calls, want 1: the warning must not stop the run", len(fake.Invocations()))
	}
	if strings.Contains(stdout+stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
	if strings.Contains(stderr, `"repo"`) || strings.Contains(stderr, `"ticket"`) {
		t.Errorf("a declared key printed a line:\n%s", stderr)
	}
}

func TestUndeclaredInput356_FileKeyNamesTheFileAndSuggests(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.yaml", "reop: "+secretValue356+"\nticket: T\n")
	fake := markingRunner("scan")

	stdout, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", file, "--input", "repo=/r")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	want := undeclaredLine356(graphPath, "reop", file) + ` — did you mean "repo"?` + "\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if strings.Count(stderr, "is not declared") != 1 {
		t.Errorf("want exactly one undeclared line:\n%s", stderr)
	}
	if strings.Contains(stdout+stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
}

func TestUndeclaredInput356_DeclaredKeysPrintNothing(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.yaml", "repo: /r\n")
	fake := markingRunner("scan")

	_, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", file, "--input", "ticket=T")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.Contains(stderr, "is not declared") {
		t.Errorf("declared keys warned:\n%s", stderr)
	}
}

func TestUndeclaredInput356_EscapeKeyFromFileNeverReachesStderrRaw(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.json", `{"repo": "/r", "ticket": "T", "\u001b[2J\u001b]0;pwn\u0007k": "`+secretValue356+`"}`)
	fake := markingRunner("scan")

	_, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", file)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if strings.ContainsAny(stderr, "\x1b\x07") {
		t.Errorf("raw control byte reached stderr: %q", stderr)
	}
	if want := `input "\x1b[2J\x1b]0;pwn\ak" (from ` + file + ")"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the %%q-quoted key %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
}

func TestUndeclaredInput356_RunOnGraphWithoutInputsWarnsEveryKey(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, `
name: bare
nodes:
  - { id: scan, prompt: "scan the tree", allowed_tools: [] }
`)
	fake := markingRunner("scan")

	_, stderr, err := runWithInputs(t, fake, graphPath, "--input", "repo="+secretValue356)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := undeclaredLine356(graphPath, "repo", "--input") + "\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
}

func TestUndeclaredInput356_DryRunWarnsAndKeepsItsVerdict(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.yaml", "reop: "+secretValue356+"\n")
	fake := markingRunner("scan")

	stdout, stderr, err := runWithInputs(t, fake, graphPath, "--dry-run",
		"--input-file", file, "--input", "repo=/r", "--input", "ticket=T")
	if err != nil {
		t.Fatalf("dry run: %v (a warning must not change the verdict)", err)
	}
	if !strings.Contains(stdout, "dry run: validation passed") {
		t.Errorf("dry run verdict changed:\n%s", stdout)
	}
	want := undeclaredLine356(graphPath, "reop", file) + ` — did you mean "repo"?` + "\n"
	if !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if len(fake.Invocations()) != 0 {
		t.Errorf("dry run spawned %d nodes", len(fake.Invocations()))
	}
	if strings.Contains(stdout+stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
}

// The warning changes nothing but stderr: a run whose node fails exits with the
// same status with and without an undeclared key, and state.json's inputs keep
// the undeclared key bound beside the declared ones.
func TestUndeclaredInput356_ExitStatusAndStateInputsUnchanged(t *testing.T) {
	graphPath := writeGraphFile(t, boundGraph)
	run := func(extra ...string) (int, map[string]string, string) {
		home := isolateRunHome(t)
		fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
			"scan": {SessionID: "s-scan", Result: "FAIL", ExitCode: 1},
		})
		fake.KeyFn = func(spec runner.NodeInvocation) string { return strings.Fields(spec.Prompt)[0] }
		args := append([]string{graphPath, "--input", "repo=/r", "--input", "ticket=T"}, extra...)
		_, stderr, err := runWithInputs(t, fake, args...)
		snap, loadErr := runstate.Load(filepath.Join(runDirFor(onlyRunID(t, home)), runstate.SnapshotFileName))
		if loadErr != nil {
			t.Fatalf("load snapshot: %v", loadErr)
		}
		return exitCodeForError(err), snap.Inputs, stderr
	}

	baseCode, _, _ := run()
	code, inputs, stderr := run("--input", "zzz="+secretValue356)
	if baseCode == 0 {
		t.Fatalf("the failing node must fail the run; exit code = 0")
	}
	if code != baseCode {
		t.Errorf("exit code = %d with an undeclared key, %d without: the warning must not change it", code, baseCode)
	}
	if want := undeclaredLine356(graphPath, "zzz", "--input"); !strings.Contains(stderr, want) {
		t.Errorf("stderr missing %q:\n%s", want, stderr)
	}
	if inputs["zzz"] != secretValue356 || inputs["repo"] != "/r" || inputs["ticket"] != "T" || len(inputs) != 3 {
		t.Errorf("state.json inputs = %v, want repo, ticket and the undeclared zzz all bound", inputs)
	}
	if strings.Contains(stderr, secretValue356) {
		t.Errorf("the value was printed")
	}
}
