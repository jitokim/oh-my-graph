package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// ADR 0044 §2.1(b): `design` interviews, plans once and writes the graph to
// --out as YAML, then lints that file — and never runs it. These tests drive
// runDesignWithRuntime against a FakeRunner with stdin injected, the same
// seams the `auto --interview` tests use (interview_test.go).

// designSpec has a multi-line prompt, so the YAML layout is exercised.
const designSpec = `{"name":"designed","version":"1","nodes":[` +
	`{"id":"survey","prompt":"Read the docs.\nList what is stale.","allowed_tools":["Read"]},` +
	`{"id":"fix","prompt":"Fix {{ artifacts.survey }}","depends_on":["survey"],"allowed_tools":["Read","Edit"]}]}`

func designOutcomes() map[string]runner.NodeOutcome {
	return map[string]runner.NodeOutcome{
		"ask-1":  {Result: interviewQuestion, TotalCostUSD: 0.01},
		"ask-2":  {Result: "ENOUGH", TotalCostUSD: 0.02},
		"plan-1": {Result: designSpec, TotalCostUSD: 0.04},
	}
}

// runDesign runs `design` with the injected seams; lint nil means the real
// lintGraphForRuntime.
func runDesign(t *testing.T, fake *runner.FakeRunner, stdin terminalInput, lint designLint, args ...string) (string, string, error) {
	t.Helper()
	if lint == nil {
		lint = lintGraphForRuntime
	}
	var out, warn bytes.Buffer
	err := runDesignWithRuntime(runner.RuntimeClaude, args, fake, &out, &warn, stdin, lint)
	return out.String(), warn.String(), err
}

// assertNothingRan is the never-runs half of test 15: every call was the
// interviewer's or the planner's, and neither runs/ nor plans/ exists.
func assertNothingRan(t *testing.T, fake *runner.FakeRunner) {
	t.Helper()
	if n := len(fake.Invocations()) - len(interviewerCalls(fake)) - len(plannerCalls(fake)); n != 0 {
		t.Errorf("%d call(s) were neither the interviewer nor the planner: a node ran", n)
	}
	for _, dir := range []string{runsRoot(), filepath.Join(omgHome(), "plans")} {
		if _, err := os.Stat(dir); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("%s exists after design (stat err %v)", dir, err)
		}
	}
}

// ADR 0044 §5 test 15
func TestDesign_WritesTheFileLintsItAndRunsNothing(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")
	var linted []string
	lint := func(w, warnW io.Writer, p string, rt runner.Runtime) error {
		linted = append(linted, p)
		return lintGraphForRuntime(w, warnW, p, rt)
	}

	out, _, err := runDesign(t, fake, terminalStdin(interviewAnswer+"\n", true), lint, "tidy the docs", "--out", path)
	if err != nil {
		t.Fatalf("design: %v\n%s", err, out)
	}

	if n := len(interviewerCalls(fake)); n != 2 {
		t.Errorf("interviewer calls = %d, want 2", n)
	}
	planners := plannerCalls(fake)
	if len(planners) != 1 {
		t.Fatalf("planner calls = %d, want exactly one", len(planners))
	}
	if !strings.HasPrefix(planners[0].Prompt, interviewHeaderLead) || !strings.Contains(planners[0].Prompt, interviewAnswer) {
		t.Errorf("the planner prompt does not open with the interview prefix:\n%s", firstLine(planners[0].Prompt))
	}
	assertNothingRan(t, fake)

	// The file is YAML, describes exactly the planner's graph, and is linted.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read --out: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(data)), "{") || !strings.Contains(string(data), "prompt: |-\n") {
		t.Errorf("--out is not block YAML with a literal multi-line prompt:\n%s", data)
	}
	fromFile, err := graph.LoadFile(path)
	if err != nil {
		t.Fatalf("the written file does not load: %v", err)
	}
	fromSpec, err := graph.Parse([]byte(designSpec))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(fromFile.Graph, fromSpec) {
		t.Errorf("the written graph differs from the planner's:\nfile: %+v\nspec: %+v", fromFile.Graph, fromSpec)
	}
	if !reflect.DeepEqual(linted, []string{path}) {
		t.Errorf("lint ran on %v, want once on %s", linted, path)
	}
	for _, needle := range []string{
		path + ": valid",
		"Cost: interview $0.0300, planner $0.0400 (1 call), total $0.0700",
		"Nothing ran.",
		"oh-my-graph run " + path,
	} {
		if !strings.Contains(out, needle) {
			t.Errorf("the output lacks %q:\n%s", needle, out)
		}
	}
}

// ADR 0044 §5 test 15
func TestDesign_RefusesAnExistingOutBeforeAnyModelCall(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "mine.yaml")
	const mine = "name: hand-written\n"
	if err := os.WriteFile(path, []byte(mine), 0o644); err != nil {
		t.Fatal(err)
	}
	stdin := terminalStdin(interviewAnswer+"\n", true)

	_, _, err := runDesign(t, fake, stdin, nil, "tidy the docs", "--out", path)
	if err == nil || !strings.Contains(err.Error(), "already exists") || !strings.Contains(err.Error(), path) {
		t.Fatalf("want the existing-file refusal naming %s, got %v", path, err)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refusal came after %d model call(s)", n)
	}
	if n := stdin.r.(*strings.Reader).Len(); n != len(interviewAnswer)+1 {
		t.Errorf("the refusal read stdin (%d bytes left)", n)
	}
	if got, _ := os.ReadFile(path); string(got) != mine {
		t.Errorf("the existing file changed: %q", got)
	}
	assertNothingRan(t, fake)
}

// ADR 0044 §5 test 15
func TestDesign_RefusesConventions(t *testing.T) {
	for _, args := range [][]string{
		{"tidy the docs", "--out", "x.yaml", "--conventions", "style.md"},
		{"tidy the docs", "--conventions=style.md", "--out", "x.yaml"},
		{"tidy the docs", "-conventions", "style.md", "--out", "x.yaml"},
	} {
		t.Run(strings.Join(args[1:], " "), func(t *testing.T) {
			conventionsHome(t)
			chdirTemp(t)
			fake := newInterviewFake(designOutcomes())

			_, _, err := runDesign(t, fake, terminalStdin(interviewAnswer+"\n", true), nil, args...)
			if err == nil || !strings.Contains(err.Error(), "--conventions is refused") {
				t.Fatalf("want the --conventions refusal, got %v", err)
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("the refusal came after %d model call(s)", n)
			}
			if _, err := os.Stat("x.yaml"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("--out was written (stat err %v)", err)
			}
			assertNothingRan(t, fake)
		})
	}

	// And through the real dispatch, which refuses at parse before any spawn.
	conventionsHome(t)
	chdirTemp(t)
	err := run([]string{"design", "tidy the docs", "--out", "x.yaml", "--conventions", "style.md"})
	if err == nil || !strings.Contains(err.Error(), "--conventions is refused") || exitCodeForError(err) != 1 {
		t.Fatalf("`oh-my-graph design ... --conventions` = %v, want the refusal and exit 1", err)
	}
}

// ADR 0044 §5 test 15
func TestDesign_LintFailureKeepsTheFileNamesItAndExitsNonZero(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")
	// A plan the coordinator accepted lints clean, so the failure is made on
	// disk between the write and the real lint: a dangling depends_on, which
	// is what the lint's own report must then name.
	lint := func(w, warnW io.Writer, p string, rt runner.Runtime) error {
		f, err := os.OpenFile(p, os.O_APPEND|os.O_WRONLY, 0)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.WriteString("  - id: orphan\n    prompt: x\n    depends_on: [nowhere]\n"); err != nil {
			t.Fatal(err)
		}
		_ = f.Close()
		return lintGraphForRuntime(w, warnW, p, rt)
	}

	out, _, err := runDesign(t, fake, terminalStdin(interviewAnswer+"\n", true), lint, "tidy the docs", "--out", path)
	if err == nil {
		t.Fatalf("design passed a graph that does not lint:\n%s", out)
	}
	if code := exitCodeForError(err); code == 0 {
		t.Errorf("exit code = 0 for a lint failure")
	}
	if !strings.Contains(err.Error(), path) || !strings.Contains(err.Error(), "is kept") {
		t.Errorf("the error does not name the kept file %s: %v", path, err)
	}
	if !strings.Contains(out, path+": ") || !strings.Contains(out, "nowhere") {
		t.Errorf("the lint's own report is not in the output:\n%s", out)
	}
	if _, err := os.Stat(path); err != nil {
		t.Errorf("the file was not kept: %v", err)
	}
	if !strings.Contains(out, "total $0.0700") {
		t.Errorf("the cost is not reported on a lint failure:\n%s", out)
	}
	assertNothingRan(t, fake)
}

func TestDesign_NoTerminalRefusesBeforeAnyModelCall(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")

	_, _, err := runDesign(t, fake, terminalStdin(interviewAnswer+"\n", false), nil, "tidy the docs", "--out", path)
	if !errors.Is(err, errDesignNeedsTerminal) {
		t.Fatalf("want the no-terminal refusal, got %v", err)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("the refusal came after %d model call(s)", n)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("--out was written (stat err %v)", err)
	}
}

func TestDesign_RequiresOutAndAGoal(t *testing.T) {
	for _, args := range [][]string{
		{"tidy the docs"},
		{"tidy the docs", "--out", " "},
		{"--out", "x.yaml"},
		{"tidy", "the", "docs", "--out", "x.yaml"},
		{"tidy the docs", "--out", filepath.Join("no", "such", "dir", "x.yaml")},
	} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			conventionsHome(t)
			chdirTemp(t)
			fake := newInterviewFake(designOutcomes())
			if _, _, err := runDesign(t, fake, terminalStdin("", true), nil, args...); err == nil {
				t.Fatal("design accepted it")
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("the refusal came after %d model call(s)", n)
			}
		})
	}
}

// An interview that ends before any answer abandons design: nothing is
// planned or written, and the spend is named.
func TestDesign_EndOfInputBeforeAnAnswerWritesNothing(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")

	_, _, err := runDesign(t, fake, terminalStdin("", true), nil, "tidy the docs", "--out", path)
	if err == nil || !strings.Contains(err.Error(), "$0.0100") {
		t.Fatalf("want the interview's refusal with its cost, got %v", err)
	}
	if n := len(plannerCalls(fake)); n != 0 {
		t.Errorf("planner calls = %d after an abandoned interview", n)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("--out was written (stat err %v)", err)
	}
}

// /done at the first prompt sends the planner no prefix, and the graph is
// still written and linted.
func TestDesign_DoneFirstPlansWithoutAPrefix(t *testing.T) {
	conventionsHome(t)
	fake := newInterviewFake(designOutcomes())
	path := filepath.Join(t.TempDir(), "designed.yaml")

	out, _, err := runDesign(t, fake, terminalStdin("/done\n", true), nil, "tidy the docs", "--out", path)
	if err != nil {
		t.Fatalf("design: %v\n%s", err, out)
	}
	planners := plannerCalls(fake)
	if len(planners) != 1 || strings.Contains(planners[0].Prompt, interviewHeaderLead) {
		t.Fatalf("want one planner call with no interview prefix, got %d", len(planners))
	}
	if !strings.Contains(out, path+": valid") {
		t.Errorf("the file was not linted:\n%s", out)
	}
}

func TestGraphSpecYAML_KeepsEveryValue(t *testing.T) {
	spec := `{"name":"y","nodes":[{"id":"a","prompt":"line one\nline two  \n\ttabbed\n","on_fail":"true",` +
		`"retry":{"max_attempts":2},"inputs":["123","- dash","a: b","null","~"]}]}`
	data, err := graphSpecYAML([]byte(spec))
	if err != nil {
		t.Fatalf("graphSpecYAML: %v", err)
	}
	var want, got any
	if err := yaml.Unmarshal([]byte(spec), &want); err != nil {
		t.Fatal(err)
	}
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("the YAML does not parse: %v\n%s", err, data)
	}
	if !reflect.DeepEqual(want, got) {
		t.Errorf("values changed:\nwant %#v\ngot  %#v\n%s", want, got, data)
	}
	if strings.Contains(string(data), "{") {
		t.Errorf("flow style survived:\n%s", data)
	}
}
