package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// #354: `--input-file` wired onto `run` and `auto`. These tests drive the real
// argv path against a FakeRunner; the loader's own refusals are judged in
// inputfile_test.go, and here only that they surface at load, before anything.

// boundGraph references both inputs in the one node's prompt, so the prompt
// the FakeRunner records shows exactly which values were bound.
const boundGraph = `
name: bound
inputs: [repo, ticket]
nodes:
  - { id: scan, prompt: "scan {{ inputs.repo }} for {{ inputs.ticket }}", allowed_tools: [] }
`

// secretValue is unique enough that finding it anywhere is a finding.
const secretValue = "sk-354-7f3e9a1c-do-not-print"

// firstWordRunner keys each call by its prompt's first word, so an
// interpolated prompt keys the same whatever was bound into it.
func firstWordRunner(words ...string) *runner.FakeRunner {
	outcomes := map[string]runner.NodeOutcome{}
	for _, w := range words {
		outcomes[w] = runner.NodeOutcome{SessionID: "s-" + w, Result: "PASS", ExitCode: 0}
	}
	fake := runner.NewFakeRunner(outcomes)
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		return strings.Fields(spec.Prompt)[0]
	}
	return fake
}

// runWithInputs runs `run` through argv, returning stdout, stderr and the error.
func runWithInputs(t *testing.T, fake *runner.FakeRunner, args ...string) (string, string, error) {
	t.Helper()
	var stdout string
	stderr, err := captureStderr(t, func() error {
		var err error
		stdout = captureStdout(t, func() {
			err = runGraphWith(args, fake, browser.NewFakeOpener(), os.Stdout)
		})
		return err
	})
	return stdout, stderr, err
}

// scanPrompt is the one prompt the fake saw.
func scanPrompt(t *testing.T, fake *runner.FakeRunner) string {
	t.Helper()
	calls := fake.Invocations()
	if len(calls) != 1 {
		t.Fatalf("fake saw %d calls, want 1", len(calls))
	}
	return calls[0].Prompt
}

func TestInputFile_BindsInputsOnRun_354(t *testing.T) {
	home := isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.yaml", "repo: /work/app\nticket: T-354\n")
	fake := firstWordRunner("scan")

	_, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", file)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := scanPrompt(t, fake), "scan /work/app for T-354"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if strings.Contains(stderr, "overrides") {
		t.Errorf("one source per key printed a precedence line:\n%s", stderr)
	}
	snap, err := runstate.Load(filepath.Join(runDirFor(onlyRunID(t, home)), runstate.SnapshotFileName))
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.Inputs["repo"] != "/work/app" || snap.Inputs["ticket"] != "T-354" || len(snap.Inputs) != 2 {
		t.Errorf("state.json inputs = %v, want the file's two bindings", snap.Inputs)
	}
}

func TestInputFile_InputFlagBeatsFileAndNamesBothSources_354(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "f1.yaml", "repo: /from/file\nticket: T-1\n")
	fake := firstWordRunner("scan")

	// --input sits BEFORE the file in argv and still wins.
	_, stderr, err := runWithInputs(t, fake, graphPath, "--input", "repo=/from/flag", "--input-file", file)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := scanPrompt(t, fake), "scan /from/flag for T-1"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if want := `input "repo": --input overrides ` + file + "\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the precedence line %q:\n%s", want, stderr)
	}
	if strings.Contains(stderr, `input "ticket"`) {
		t.Errorf("a key with one source got a precedence line:\n%s", stderr)
	}
}

func TestInputFile_TwoFilesMergeInOrder_354(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	f1 := writeInputFile(t, "f1.yaml", "repo: /one\nticket: T-1\n")
	f2 := writeInputFile(t, "f2.json", `{"repo": "/two"}`)
	fake := firstWordRunner("scan")

	_, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", f1, "--input-file", f2)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if got, want := scanPrompt(t, fake), "scan /two for T-1"; got != want {
		t.Errorf("prompt = %q, want %q", got, want)
	}
	if want := `input "repo": ` + f2 + " overrides " + f1 + "\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the precedence line %q:\n%s", want, stderr)
	}
}

func TestInputFile_PrecedenceLinesAreSortedAndNameEverySource_354(t *testing.T) {
	var c commonRunFlags
	c.inputs = inputFlag{"b": "flag"}
	c.inputFiles = inputFileFlag{
		{path: "f1.yaml", bindings: map[string]string{"a": "1", "b": "1", "c": "1"}},
		{path: "f2.yaml", bindings: map[string]string{"a": "2", "b": "2"}},
	}
	var out strings.Builder
	c.bindInputFiles(&out)

	want := `input "a": f2.yaml overrides f1.yaml` + "\n" +
		`input "b": --input overrides f1.yaml, f2.yaml` + "\n"
	if out.String() != want {
		t.Errorf("precedence lines\ngot:\n%s\nwant:\n%s", out.String(), want)
	}
	if c.inputs["a"] != "2" || c.inputs["b"] != "flag" || c.inputs["c"] != "1" || len(c.inputs) != 3 {
		t.Errorf("merged inputs = %v", c.inputs)
	}
}

func TestInputFile_PrecedenceLineNeverPrintsTheValue_354(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, `
name: quiet
inputs: [token]
nodes:
  - { id: use, prompt: "use the token", allowed_tools: [] }
`)
	f1 := writeInputFile(t, "f1.yaml", "token: "+secretValue+"-one\n")
	f2 := writeInputFile(t, "f2.yaml", "token: "+secretValue+"-two\n")
	fake := firstWordRunner("use")

	stdout, stderr, err := runWithInputs(t, fake, graphPath, "--input-file", f1, "--input-file", f2, "--input", "token="+secretValue)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := `input "token": --input overrides ` + f1 + ", " + f2 + "\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the precedence line %q:\n%s", want, stderr)
	}
	for name, out := range map[string]string{"stdout": stdout, "stderr": stderr} {
		if strings.Contains(out, secretValue) {
			t.Errorf("%s carries the input's value:\n%s", name, out)
		}
	}
}

func TestInputFile_RefusesAtLoadBeforeAnything_354(t *testing.T) {
	for name, tc := range map[string]struct{ content, want string }{
		"nested value":  {"repo:\n  path: /x\n", `key "repo" is a map (nested)`},
		"list value":    {"repo: [a, b]\n", `key "repo" is a list`},
		"null value":    {"repo: null\n", `key "repo" is null`},
		"non-map top":   {"- repo\n- ticket\n", "the top level is a list"},
		"json non-map":  {`["repo"]`, "the top level is a list"},
		"json null val": {`{"repo": null}`, `key "repo" is null`},
		"second doc":    {"repo: /x\n---\ntask: do it\n", "more than one YAML document"},
	} {
		t.Run(name, func(t *testing.T) {
			home := isolateRunHome(t)
			graphPath := writeGraphFile(t, boundGraph)
			file := writeInputFile(t, "in.yaml", tc.content)

			for _, extra := range [][]string{nil, {"--dry-run"}} {
				fake := firstWordRunner("scan")
				args := append([]string{graphPath, "--input-file", file}, extra...)
				_, _, err := runWithInputs(t, fake, args...)
				if err == nil {
					t.Fatalf("%v accepted %q", args, tc.content)
				}
				if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), file) {
					t.Errorf("refusal %q does not name %q and the path", err, tc.want)
				}
				if code := exitCodeForError(err); code != 1 {
					t.Errorf("exit code = %d, want 1 like a malformed --input", code)
				}
				assertRefusedBeforeRun(t, home, fake)
			}
		})
	}
}

// A malformed --input and a refused --input-file come back through one path:
// both are flag-parse errors from the same FlagSet.
func TestInputFile_RefusalTakesTheMalformedInputPath_354(t *testing.T) {
	file := writeInputFile(t, "in.yaml", "repo: [a]\n")
	for _, args := range [][]string{{"--input", "repo"}, {"--input-file", file}} {
		f := newRunFlags()
		f.set.SetOutput(new(strings.Builder))
		err := f.parse(append([]string{"g.yaml"}, args...))
		if err == nil || !strings.HasPrefix(err.Error(), "invalid value ") || !strings.Contains(err.Error(), "for flag "+strings.TrimPrefix(args[0], "-")) {
			t.Errorf("%v: err = %v, want the flag package's invalid-value error", args, err)
		}
	}
}

// An unknown key is reported however an unknown --input is today: the two
// spellings of the same binding produce byte-identical output and the same
// exit code. Today that is acceptance — nothing checks a bound key against the
// graph's inputs: [..] — so both exit 0 and a real run still runs the node.
func TestInputFile_UnknownKeyReportedLikeUnknownInput_354(t *testing.T) {
	graphPath := writeGraphFile(t, boundGraph)
	file := writeInputFile(t, "in.yaml", "repo: /x\nticket: T-1\nextra: y\n")
	viaFlagArgs := []string{"--input", "repo=/x", "--input", "ticket=T-1", "--input", "extra=y"}
	viaFileArgs := []string{"--input-file", file}

	type outcome struct {
		stdout, stderr, err string
		code                int
	}
	dryRun := func(args []string) outcome {
		isolateRunHome(t)
		stdout, stderr, err := runWithInputs(t, firstWordRunner("scan"), append([]string{graphPath, "--dry-run"}, args...)...)
		o := outcome{stdout: stdout, stderr: stderr, code: exitCodeForError(err)}
		if err != nil {
			o.err = err.Error()
		}
		return o
	}
	viaFlag, viaFile := dryRun(viaFlagArgs), dryRun(viaFileArgs)
	if viaFlag != viaFile {
		t.Errorf("unknown key reported differently\n--input:      %+v\n--input-file: %+v", viaFlag, viaFile)
	}
	if viaFile.code != 0 {
		t.Errorf("--dry-run exit code = %d, want 0 like an unknown --input", viaFile.code)
	}

	// A real run: same exit code, the same prompt reaches the node, and the
	// unknown key is kept in state.json's inputs rather than dropped. Its
	// stdout names a fresh run id, so only the outcome is compared here.
	for name, args := range map[string][]string{"--input": viaFlagArgs, "--input-file": viaFileArgs} {
		home := isolateRunHome(t)
		fake := firstWordRunner("scan")
		_, _, err := runWithInputs(t, fake, append([]string{graphPath}, args...)...)
		if code := exitCodeForError(err); code != 0 {
			t.Errorf("%s: run exit code = %d (%v), want 0", name, code, err)
		}
		if got, want := scanPrompt(t, fake), "scan /x for T-1"; got != want {
			t.Errorf("%s: prompt = %q, want %q", name, got, want)
		}
		snap, err := runstate.Load(filepath.Join(runDirFor(onlyRunID(t, home)), runstate.SnapshotFileName))
		if err != nil {
			t.Fatalf("%s: load snapshot: %v", name, err)
		}
		if snap.Inputs["extra"] != "y" || len(snap.Inputs) != 3 {
			t.Errorf("%s: state.json inputs = %v, want all three bindings, extra included", name, snap.Inputs)
		}
	}
}

// A missing required input is reported exactly as today, and --dry-run sees
// the file's values.
func TestInputFile_DryRunSeesMergedValuesAndMissingInput_354(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, boundGraph)
	partial := writeInputFile(t, "partial.yaml", "repo: /x\n")

	viaFlagOut, _, viaFlagErr := runWithInputs(t, firstWordRunner("scan"), graphPath, "--dry-run", "--input", "repo=/x")
	viaFileOut, _, viaFileErr := runWithInputs(t, firstWordRunner("scan"), graphPath, "--dry-run", "--input-file", partial)
	if viaFileErr == nil || viaFlagErr == nil || viaFileErr.Error() != viaFlagErr.Error() || viaFileOut != viaFlagOut {
		t.Errorf("missing input reported differently\n--input:      %v\n%s\n--input-file: %v\n%s", viaFlagErr, viaFlagOut, viaFileErr, viaFileOut)
	}
	if !strings.Contains(viaFileOut, "inputs.ticket") {
		t.Errorf("dry run does not name the missing input:\n%s", viaFileOut)
	}

	full := writeInputFile(t, "full.yaml", "repo: /x\nticket: T-1\n")
	fake := firstWordRunner("scan")
	if _, _, err := runWithInputs(t, fake, graphPath, "--dry-run", "--input-file", full); err != nil {
		t.Fatalf("dry run with every input in the file: %v", err)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("--dry-run invoked %d nodes, want 0", n)
	}
}

func TestInputFile_JSONAndYAMLBothBindEndToEnd_354(t *testing.T) {
	for name, tc := range map[string]struct{ file, content string }{
		"yaml": {"in.yaml", "repo: /work/app\nticket: \"0123\"\n"},
		"json": {"in.json", `{"repo": "/work/app", "ticket": "0123"}`},
	} {
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			graphPath := writeGraphFile(t, boundGraph)
			fake := firstWordRunner("scan")
			if _, _, err := runWithInputs(t, fake, graphPath, "--input-file", writeInputFile(t, tc.file, tc.content)); err != nil {
				t.Fatalf("run: %v", err)
			}
			if got, want := scanPrompt(t, fake), "scan /work/app for 0123"; got != want {
				t.Errorf("prompt = %q, want %q", got, want)
			}
		})
	}
}

// runInputFileAuto runs `auto` through argv. Mapping and activation are off so
// no part of the real ~/.claude is read; no planner outcome is scripted, so the
// one planner call fails after its prompt is recorded.
func runInputFileAuto(t *testing.T, fake *runner.FakeRunner, args ...string) (string, error) {
	t.Helper()
	args = append([]string{"scan the repo"}, append(args, "--accept-no-build-evidence", "--no-agent-mapping", "--no-skill-activation", "--no-reuse")...)
	return captureStderr(t, func() error {
		var err error
		captureStdout(t, func() {
			err = runAutoWithRuntime(runner.RuntimeClaude, args, fake, browser.NewFakeOpener(), os.Stdout, terminalStdin("", false), greenBaseline(args))
		})
		return err
	})
}

func TestInputFile_AutoPlannerSeesTheMergedKeys_354(t *testing.T) {
	isolateRunHome(t)
	file := writeInputFile(t, "in.yaml", "repo: /x\nticket: T-1\n")
	fake := runner.NewFakeRunner(nil)

	stderr, _ := runInputFileAuto(t, fake, "--input-file", file, "--input", "branch=main", "--input", "repo=/y")
	calls := plannerCalls(fake)
	if len(calls) == 0 {
		t.Fatal("auto made no planner call")
	}
	if want := "Available inputs: branch, repo, ticket."; !strings.Contains(calls[0].Prompt, want) {
		t.Errorf("planner prompt missing %q", want)
	}
	if want := `input "repo": --input overrides ` + file + "\n"; !strings.Contains(stderr, want) {
		t.Errorf("stderr missing the precedence line %q:\n%s", want, stderr)
	}
}

func TestInputFile_AutoRefusalMakesNoPlannerCall_354(t *testing.T) {
	home := isolateRunHome(t)
	file := writeInputFile(t, "in.yaml", "repo:\n  nested: true\n")
	fake := runner.NewFakeRunner(nil)

	_, err := runInputFileAuto(t, fake, "--input-file", file)
	if err == nil || !strings.Contains(err.Error(), `key "repo" is a map (nested)`) {
		t.Fatalf("err = %v, want the loader's refusal", err)
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	assertNothingSpent(t, home, fake)
}

func TestInputFile_RunAndAutoParseRepeatedFlag_354(t *testing.T) {
	f1 := writeInputFile(t, "f1.yaml", "a: 1\n")
	f2 := writeInputFile(t, "f2.json", `{"a": "2", "b": "3"}`)
	repeated := []string{"--input-file", f1, "--input-file", f2}

	run := newRunFlags()
	if err := run.parse(append([]string{"g.yaml"}, repeated...)); err != nil {
		t.Fatalf("run parse: %v", err)
	}
	auto := newAutoFlags()
	if err := auto.parse(append([]string{"a goal"}, repeated...)); err != nil {
		t.Fatalf("auto parse: %v", err)
	}
	for name, c := range map[string]commonRunFlags{"run": run.commonRunFlags, "auto": auto.commonRunFlags} {
		if len(c.inputFiles) != 2 || c.inputFiles[0].path != f1 || c.inputFiles[1].path != f2 {
			t.Errorf("%s: inputFiles = %+v, want both paths in argv order", name, c.inputFiles)
		}
		if c.inputs["a"] != "2" || c.inputs["b"] != "3" {
			t.Errorf("%s: inputs = %v, want the later file over the earlier", name, c.inputs)
		}
	}
}

// Resume reads the inputs from the snapshot and never the file: rewriting the
// file between the pause and the approval changes nothing.
func TestInputFile_ResumeDoesNotRereadTheFile_354(t *testing.T) {
	home := isolateRunHome(t)
	graphPath := writeGraphFile(t, `
name: gated
inputs: [ticket]
nodes:
  - { id: build, prompt: "build {{ inputs.ticket }}", allowed_tools: [] }
  - { id: approve, type: gate, depends_on: [build] }
  - { id: ship, prompt: "ship {{ inputs.ticket }}", depends_on: [approve], allowed_tools: [] }
`)
	file := writeInputFile(t, "in.yaml", "ticket: ORIGINAL-354\n")
	fake := firstWordRunner("build", "ship")

	if _, _, err := runWithInputs(t, fake, graphPath, "--input-file", file); exitCodeForError(err) != 2 {
		t.Fatalf("run: %v, want a pause at the gate (exit 2)", err)
	}
	if err := os.WriteFile(file, []byte("ticket: REWRITTEN-354\n"), 0o600); err != nil {
		t.Fatalf("rewrite: %v", err)
	}
	runID := onlyRunID(t, home)
	var err error
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	var shipPrompt string
	for _, call := range fake.Invocations() {
		if strings.HasPrefix(call.Prompt, "ship ") {
			shipPrompt = call.Prompt
		}
	}
	if want := "ship ORIGINAL-354"; shipPrompt != want {
		t.Errorf("resumed node's prompt = %q, want %q", shipPrompt, want)
	}
}
