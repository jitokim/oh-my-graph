package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/conventions"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// ADR 0041: `auto --conventions <path>` carries an operator's conventions into
// every planned node as TEXT. These tests drive the real `auto` and `resume`
// paths against a FakeRunner (and, for the ceiling, a stub claude behind a real
// CLIRunner), and are numbered after ADR 0041 §5.

const conventionsHeader = "The person who launched this run gave these conventions for every node. Follow them.\n\n"

// conventionsHome isolates both the run home and $HOME, so the developer's
// own agents and skills cannot change what a planned node's prompt looks like.
func conventionsHome(t *testing.T) string {
	t.Helper()
	isolateRunHome(t)
	home := t.TempDir()
	t.Setenv("HOME", home)
	return home
}

// conventionFiles writes the two-file set most tests use: a plain style guide
// and a mixed file with two @-import lines, which the screen must count.
func conventionFiles(t *testing.T) (style, mixed string) {
	t.Helper()
	dir := t.TempDir()
	style = filepath.Join(dir, "style.md")
	mixed = filepath.Join(dir, "CLAUDE.md")
	writeFileTree(t, style, "use tabs, and write {{ inputs.x }} and {{ feedback.y }} literally\n")
	writeFileTree(t, mixed, "# House rules\n@docs/a.md\ntable-driven tests\n@docs/b.md\n")
	return style, mixed
}

// withoutConventions strips a conventions prefix off a spawned prompt, so an
// outcome can be keyed by the planner's own wording whether or not it carries
// one.
func withoutConventions(prompt string) string {
	if !strings.HasPrefix(prompt, conventionsHeader) {
		return prompt
	}
	if _, rest, ok := strings.Cut(prompt, "\n---\n\n"); ok {
		return rest
	}
	return prompt
}

// newConventionsFake is newCycleFake keyed through withoutConventions. onPlan,
// when non-nil, runs inside the planner call — after launch, before any node.
func newConventionsFake(outcomes map[string]runner.NodeOutcome, onPlan func()) *runner.FakeRunner {
	fake := runner.NewFakeRunner(outcomes)
	planCalls, assessCalls, nodeCalls := 0, 0, 0
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		switch {
		case strings.Contains(spec.Prompt, "planning coordinator"):
			planCalls++
			if onPlan != nil {
				onPlan()
			}
			return fmt.Sprintf("plan-%d", planCalls)
		case strings.Contains(spec.Prompt, "goal assessor"):
			assessCalls++
			return fmt.Sprintf("assess-%d", assessCalls)
		}
		nodeCalls++
		return fmt.Sprintf("%s-%d", strings.SplitN(withoutConventions(spec.Prompt), "\n\n", 2)[0], nodeCalls)
	}
	return fake
}

// nodeInvocations drops the planner's and assessor's calls.
func nodeInvocations(fake *runner.FakeRunner) []runner.NodeInvocation {
	var nodes []runner.NodeInvocation
	for _, spec := range fake.Invocations() {
		if strings.Contains(spec.Prompt, "planning coordinator") || strings.Contains(spec.Prompt, "goal assessor") {
			continue
		}
		nodes = append(nodes, spec)
	}
	return nodes
}

func stateSchemaAndConventions(t *testing.T, runID string) (int, json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var head struct {
		Schema      int             `json:"schema"`
		Conventions json.RawMessage `json:"conventions"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	return head.Schema, head.Conventions
}

// wantConventionsLines is ADR 0041 §2.4's operator-screen literal for the
// two-file fixture, built from the validated set's own figures.
func wantConventionsLines(set *conventions.Set) string {
	return fmt.Sprintf("  Planned nodes are prefixed with your conventions (2 files, %d bytes, sha256 %s) — text only: no settings, grants, hooks or MCP servers come with it.\n",
		set.TotalBytes(), set.SHA256()[:12]) + wantConventionsFileLines(set)
}

func wantConventionsFileLines(set *conventions.Set) string {
	s, m := set.Sources[0], set.Sources[1]
	return fmt.Sprintf("    1/2  %s  %d bytes  sha256 %s\n", s.Path, s.Bytes, s.SHA256[:12]) +
		fmt.Sprintf("    2/2  %s  %d bytes  sha256 %s  (2 @-import lines not followed)\n", m.Path, m.Bytes, m.SHA256[:12])
}

// --- tests 2, 3, 11, 14: a normal launch -------------------------------------

func TestAutoConventions_PrefixesEveryPlannedNodeFromTheStagedCopy(t *testing.T) {
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	launched, err := conventions.Load([]string{style, mixed})
	if err != nil {
		t.Fatal(err)
	}

	// Test 11: the source changes after launch — inside the planner call — and
	// no node may see the change.
	fake := newConventionsFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
	}, func() { writeFileTree(t, style, "EDITED AFTER LAUNCH\n") })

	var runErr error
	out := captureStdout(t, func() {
		runErr = runAutoWith([]string{"tidy the docs", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if runErr != nil {
		t.Fatalf("auto: %v", runErr)
	}

	for _, spec := range fake.Invocations() {
		if strings.Contains(spec.Prompt, "planning coordinator") && strings.Contains(spec.Prompt, conventionsHeader) {
			t.Error("the planner call was prefixed; the planner is untouched (§2.6)")
		}
	}
	nodes := nodeInvocations(fake)
	if len(nodes) != 1 {
		t.Fatalf("node spawns = %d, want 1", len(nodes))
	}
	prompt := nodes[0].Prompt
	// Test 2: the header, both files under ordinal and basename, the separator,
	// then the node's own prompt unchanged.
	if want := string(launched.Staged) + "work"; prompt != want {
		t.Errorf("node prompt:\n%q\nwant:\n%q", prompt, want)
	}
	for _, needle := range []string{"## Conventions 1/2: style.md\n", "## Conventions 2/2: CLAUDE.md\n", "{{ inputs.x }} and {{ feedback.y }}"} {
		if !strings.Contains(prompt, needle) {
			t.Errorf("node prompt lacks %q", needle)
		}
	}
	for _, src := range launched.Sources {
		if strings.Contains(prompt, src.Path) || strings.Contains(prompt, filepath.Dir(src.Path)) || strings.Contains(prompt, src.SHA256[:12]) {
			t.Errorf("the node prompt carries %s's path or hash", src.Path)
		}
	}
	if strings.Contains(prompt, "EDITED AFTER LAUNCH") {
		t.Error("a source edited after launch reached a node; the staged copy is authoritative")
	}

	runID := soleRunID(t)
	staged, err := os.ReadFile(filepath.Join(runDirFor(runID), conventions.StagedFileName))
	if err != nil {
		t.Fatalf("no staged conventions.md: %v", err)
	}
	if string(staged) != string(launched.Staged) {
		t.Errorf("staged copy differs from what was validated at launch:\n%s", staged)
	}
	schema, record := stateSchemaAndConventions(t, runID)
	if schema != runstate.SchemaWithConventions {
		t.Errorf("state.json schema = %d, want %d", schema, runstate.SchemaWithConventions)
	}
	var rec runstate.Conventions
	if err := json.Unmarshal(record, &rec); err != nil || rec.StagedSHA256 != launched.SHA256() || len(rec.Sources) != 2 ||
		rec.Sources[0].Path != style || rec.Sources[1].ImportLines != 2 {
		t.Errorf("state.json conventions record = %s (err %v)", record, err)
	}

	// Test 14: the whole literal, and test 3's lint half — nothing on the
	// screen reacts to the {{ }} in the operator's text.
	if !strings.Contains(out, wantConventionsLines(launched)) {
		t.Errorf("plan screen lacks the conventions lines:\n%s\nwant:\n%s", out, wantConventionsLines(launched))
	}
	if strings.Contains(out, "feedback.y") || strings.Contains(out, "inputs.x") {
		t.Errorf("the operator's template syntax reached a lint or validation message:\n%s", out)
	}
}

// --- test 1: default off ------------------------------------------------------

func TestAutoConventions_DefaultOffIsUnchanged(t *testing.T) {
	conventionsHome(t)
	fake := newConventionsFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
	}, nil)
	var runErr error
	out := captureStdout(t, func() {
		runErr = runAutoWith([]string{"tidy the docs", "--accept-no-build-evidence"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if runErr != nil {
		t.Fatalf("auto: %v", runErr)
	}
	nodes := nodeInvocations(fake)
	if len(nodes) != 1 || nodes[0].Prompt != "work" {
		t.Fatalf("node prompts = %+v, want exactly the planner's \"work\"", nodes)
	}
	runID := soleRunID(t)
	if _, err := os.Stat(filepath.Join(runDirFor(runID), conventions.StagedFileName)); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a run without the flag staged conventions (stat err %v)", err)
	}
	schema, record := stateSchemaAndConventions(t, runID)
	if schema != runstate.Schema || record != nil {
		t.Errorf("state.json schema = %d, conventions = %s; want %d and no record", schema, record, runstate.Schema)
	}
	if strings.Contains(out, "with your conventions") || strings.Contains(out, "Your conventions") {
		t.Errorf("a run without the flag prints a conventions line:\n%s", out)
	}
}

// --- test 9 at the CLI: every refusal is before the planner call -------------

func TestAutoConventions_RefusesBeforeThePlannerCall(t *testing.T) {
	const secret = "SECRET-CONVENTIONS-CONTENT"
	dir := t.TempDir()
	good := filepath.Join(dir, "good.md")
	writeFileTree(t, good, secret+"\n")
	blank := filepath.Join(dir, "blank.md")
	writeFileTree(t, blank, "\n  \n")
	importOnly := filepath.Join(dir, "imports.md")
	writeFileTree(t, importOnly, "@docs/style.md\n@docs/testing.md\n")
	badUTF8 := filepath.Join(dir, "bad.md")
	writeFileTree(t, badUTF8, secret+"\xff")
	big1 := filepath.Join(dir, "big1.md")
	writeFileTree(t, big1, strings.Repeat("a", conventions.MaxStagedBytes/2+1))
	big2 := filepath.Join(dir, "big2.md")
	writeFileTree(t, big2, strings.Repeat("b", conventions.MaxStagedBytes/2))

	for _, tc := range []struct {
		name  string
		paths []string
		want  []string
	}{
		{"missing path", []string{filepath.Join(dir, "nope.md")}, []string{"nope.md", "cannot be read"}},
		{"directory", []string{dir}, []string{"is a directory"}},
		{"blank file", []string{blank}, []string{"blank.md", "is blank"}},
		{"import-only file", []string{importOnly}, []string{"imports.md", "--conventions docs/style.md --conventions docs/testing.md"}},
		{"same file twice", []string{good, good}, []string{"good.md", "same file"}},
		{"one byte over the cap", []string{big1, big2}, []string{"98305 bytes", "98304-byte cap", "big1.md 49153 bytes", "big2.md 49152 bytes"}},
		{"invalid UTF-8 in the second file", []string{good, badUTF8}, []string{"bad.md", "not valid UTF-8"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conventionsHome(t)
			fake := newConventionsFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}}, nil)
			args := []string{"tidy the docs", "--accept-no-build-evidence"}
			for _, p := range tc.paths {
				args = append(args, "--conventions", p)
			}
			var err error
			captureStdout(t, func() { err = runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout) })

			var refusal *conventions.RefusalError
			if !errors.As(err, &refusal) {
				t.Fatalf("want a *conventions.RefusalError, got %T: %v", err, err)
			}
			for _, part := range tc.want {
				if !strings.Contains(err.Error(), part) {
					t.Errorf("refusal lacks %q: %v", part, err)
				}
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("refusal quotes file content: %v", err)
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("the planner was called %d time(s); a refusal must cost nothing", n)
			}
			if entries, _ := os.ReadDir(runsRoot()); len(entries) != 0 {
				t.Errorf("a refused launch minted %d run director(ies)", len(entries))
			}
		})
	}
}

// TestAutoConventions_AStageFailureSettlesTheRunAsFailed: a run directory the
// conventions cannot be staged into fails the run after the planner call and
// before any node spawns — and the leg is closed on the way out, so the run
// reads FAIL with its lock released, never in-flight or abandoned.
func TestAutoConventions_AStageFailureSettlesTheRunAsFailed(t *testing.T) {
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	// Inside the planner call the run directory exists; a DIRECTORY where
	// conventions.md goes makes the staging write fail.
	fake := newConventionsFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
	}, func() {
		if err := os.MkdirAll(filepath.Join(soleRunDir(t), conventions.StagedFileName), 0o700); err != nil {
			t.Fatalf("plant the obstacle: %v", err)
		}
	})

	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"tidy the docs", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if runErr == nil || !strings.Contains(runErr.Error(), "stage conventions") {
		t.Fatalf("want a stage conventions error, got %v", runErr)
	}
	if nodes := nodeInvocations(fake); len(nodes) != 0 {
		t.Errorf("%d node(s) spawned after staging failed", len(nodes))
	}

	dir := soleRunDir(t)
	status, err := runstatus.Of(dir)
	if err != nil {
		t.Fatalf("runstatus.Of: %v", err)
	}
	if status != runstatus.Fail {
		t.Errorf("the run reads %v, want %v — the planning leg must be closed on the stream", status, runstatus.Fail)
	}
	release, err := acquireRunLock(filepath.Join(dir, lockFileName))
	if err != nil {
		t.Fatalf("the run lock is still held after the run returned: %v", err)
	}
	release()
}

func TestAutoFlags_ConventionsRejectsABlankPath(t *testing.T) {
	if err := newAutoFlags().parse([]string{"goal", "--conventions", " "}); err == nil {
		t.Error("a blank --conventions path parsed")
	}
	f := newAutoFlags()
	if err := f.parse([]string{"goal", "--conventions", "a.md", "--conventions", "b.md"}); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if want := (conventionsFlag{"a.md", "b.md"}); !reflect.DeepEqual(f.conventionPaths, want) {
		t.Errorf("conventionPaths = %v, want %v in command-line order", f.conventionPaths, want)
	}
}

// --- test 15: --plan-only says the conventions do not carry -------------------

func TestAutoConventions_PlanOnlySaysTheyAreNotInTheSavedGraph(t *testing.T) {
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	set, err := conventions.Load([]string{style, mixed})
	if err != nil {
		t.Fatal(err)
	}
	fake := newConventionsFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}}, nil)
	out := captureStdout(t, func() {
		if err := runAutoWith([]string{"tidy the docs", "--plan-only", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout); err != nil {
			t.Fatalf("auto --plan-only: %v", err)
		}
	})
	want := fmt.Sprintf("  Your conventions (2 files, %d bytes) are NOT in the saved graph: `run <graph.yaml>` does not prefix them. Launch with `auto` to apply them.\n", set.TotalBytes()) +
		wantConventionsFileLines(set)
	if !strings.Contains(out, want) {
		t.Errorf("--plan-only screen lacks:\n%s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "Planned nodes are prefixed with your conventions") {
		t.Errorf("--plan-only promised a prefix `run` does not deliver:\n%s", out)
	}
}

// --- test 14's second half: every goal-loop cycle prints and prefixes --------

func TestAutoConventions_EveryGoalCyclePrintsAndPrefixes(t *testing.T) {
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	set, err := conventions.Load([]string{style, mixed})
	if err != nil {
		t.Fatal(err)
	}
	fake := newConventionsFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0},
		"assess-1": {Result: cycleAssessNotMet},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0},
		"assess-2": {Result: cycleAssessMet},
	}, nil)
	out := captureStdout(t, func() {
		if err := runAutoWith([]string{"tidy the docs", "--max-cycles", "2", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout); err != nil {
			t.Fatalf("goal loop: %v", err)
		}
	})
	if got := strings.Count(out, wantConventionsLines(set)); got != 2 {
		t.Errorf("the conventions lines appeared %d times over two cycles, want once per cycle:\n%s", got, out)
	}
	nodes := nodeInvocations(fake)
	if len(nodes) != 2 {
		t.Fatalf("node spawns = %d, want 2", len(nodes))
	}
	for i, spec := range nodes {
		if !strings.HasPrefix(spec.Prompt, string(set.Staged)) {
			t.Errorf("cycle %d's node is not prefixed:\n%s", i+1, spec.Prompt)
		}
	}
}

// --- test 12: resume ----------------------------------------------------------

// pausedConventionsRun launches a conventions run whose only node hits the
// session limit, and returns the fake, the run id and the launched set.
func pausedConventionsRun(t *testing.T) (*runner.FakeRunner, string, *conventions.Set) {
	t.Helper()
	conventionsHome(t)
	style, mixed := conventionFiles(t)
	set, err := conventions.Load([]string{style, mixed})
	if err != nil {
		t.Fatal(err)
	}
	fake := newConventionsFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec},
		"work-1": {ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true},
		"work-2": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
	}, nil)
	var runErr error
	captureStdout(t, func() {
		runErr = runAutoWith([]string{"tidy the docs", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	var limited *schedule.LimitPausedError
	if !errors.As(runErr, &limited) {
		t.Fatalf("the first leg should pause on the session limit, got %T: %v", runErr, runErr)
	}
	return fake, soleRunID(t), set
}

func TestResumeConventions_PrefixesFromTheStagedCopyAndReprints(t *testing.T) {
	fake, runID, set := pausedConventionsRun(t)
	before := len(fake.Invocations())

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if resumeErr != nil {
		t.Fatalf("resume: %v", resumeErr)
	}
	spawned := fake.Invocations()[before:]
	if len(spawned) != 1 {
		t.Fatalf("the resumed leg spawned %d node(s), want 1", len(spawned))
	}
	if want := string(set.Staged) + "work"; spawned[0].Prompt != want {
		t.Errorf("resumed node prompt:\n%q\nwant:\n%q", spawned[0].Prompt, want)
	}
	if !strings.Contains(out, wantConventionsLines(set)) {
		t.Errorf("the resumed leg did not reprint the conventions lines:\n%s", out)
	}
	// The record survives the resumed leg's rewrite of the whole snapshot.
	if schema, record := stateSchemaAndConventions(t, runID); schema != runstate.SchemaWithConventions || record == nil {
		t.Errorf("after resume: schema = %d, record = %s; the resumed leg erased the conventions", schema, record)
	}
}

func TestResumeConventions_RefusesAMissingOrAlteredStagedCopy(t *testing.T) {
	for _, tc := range []struct {
		name   string
		tamper func(t *testing.T, path string)
		want   string
	}{
		{"altered", func(t *testing.T, path string) { writeFileTree(t, path, "be sloppy\n") }, ""},
		{"missing", func(t *testing.T, path string) {
			if err := os.Remove(path); err != nil {
				t.Fatal(err)
			}
		}, "missing"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fake, runID, set := pausedConventionsRun(t)
			staged := filepath.Join(runDirFor(runID), conventions.StagedFileName)
			tc.tamper(t, staged)
			before := len(fake.Invocations())

			var err error
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
			})
			var mismatch *conventions.StagedMismatchError
			if !errors.As(err, &mismatch) {
				t.Fatalf("want *conventions.StagedMismatchError, got %T: %v", err, err)
			}
			if !strings.Contains(err.Error(), set.SHA256()) {
				t.Errorf("the refusal does not name the recorded hash: %v", err)
			}
			if tc.want != "" && !strings.Contains(err.Error(), tc.want) {
				t.Errorf("the refusal lacks %q: %v", tc.want, err)
			}
			if tc.name == "altered" && (mismatch.Found == "" || !strings.Contains(err.Error(), mismatch.Found)) {
				t.Errorf("the refusal does not name the hash it found: %v", err)
			}
			if n := len(fake.Invocations()) - before; n != 0 {
				t.Errorf("a refused resume spawned %d node(s)", n)
			}
		})
	}
}

func TestResumeFlags_RegisterNoConventions(t *testing.T) {
	f := newResumeFlags()
	if f.set.Lookup("conventions") != nil {
		t.Error("`resume` registers --conventions; a resumed leg may not re-point, add or drop them (§2.6)")
	}
	if err := f.parse([]string{"run-1", "--retry-failed", "--conventions", "x.md"}); err == nil {
		t.Error("`resume` parsed --conventions")
	}
	if newRunFlags().set.Lookup("conventions") != nil {
		t.Error("`run` registers --conventions; a hand-written node loads the CLI's configuration natively (§2.6)")
	}
	if err := newChatFlags().parse([]string{"--conventions", "x.md"}); err == nil {
		t.Error("`chat` parsed --conventions (§2.6)")
	}
}

// --- test 7: the ceiling, at the argv a real CLIRunner builds -----------------

// TestAutoConventions_TheCeilingAndTheGraphDoNotMove drives the stub-claude
// harness twice, with and without the flag, and compares every node's whole
// argv except the prompt — the staged agent's --agent/--plugin-dir and the
// activated nodes' skill plugin dir included — plus the saved graph.json.
func TestAutoConventions_TheCeilingAndTheGraphDoNotMove(t *testing.T) {
	style, mixed := conventionFiles(t)
	type run struct {
		graph  []byte
		argv   map[string]recordedArgv
		prompt map[string]string
	}
	launch := func(args ...string) run {
		runID, spawns := runAutoCapturingArgv(t, append([]string{"--accept-no-build-evidence"}, args...)...)
		graphJSON, err := os.ReadFile(filepath.Join(runDirFor(runID), generatedSpecFileName))
		if err != nil {
			t.Fatal(err)
		}
		r := run{graph: graphJSON, argv: map[string]recordedArgv{}, prompt: map[string]string{}}
		runDir := runDirFor(runID)
		for prompt, argv := range spawns {
			node := strings.SplitN(withoutConventions(prompt), "\n\n", 2)[0]
			normalized := make(recordedArgv, len(argv))
			for i, a := range argv {
				switch {
				case a == prompt:
					normalized[i] = "<prompt>"
				case i > 0 && argv[i-1] == "--session-id":
					// Minted fresh per spawn, so it differs between any two runs.
					normalized[i] = "<session-id>"
				default:
					normalized[i] = strings.ReplaceAll(a, runDir, "<run-dir>")
				}
			}
			r.argv[node] = normalized
			r.prompt[node] = prompt
		}
		return r
	}
	plain := launch()
	with := launch("--conventions", style, "--conventions", mixed)

	if string(plain.graph) != string(with.graph) {
		t.Errorf("graph.json differs with the flag:\n%s\nvs\n%s", plain.graph, with.graph)
	}
	if len(plain.argv) != 4 || len(with.argv) != 4 {
		t.Fatalf("spawned %d / %d nodes, want 4 each", len(plain.argv), len(with.argv))
	}
	for node, argv := range plain.argv {
		if !reflect.DeepEqual(argv, with.argv[node]) {
			t.Errorf("%s: argv moved with the flag\nwithout: %q\nwith:    %q", node, argv, with.argv[node])
		}
		if !strings.HasPrefix(with.prompt[node], conventionsHeader) {
			t.Errorf("%s: not prefixed under the flag:\n%s", node, with.prompt[node])
		}
		if strings.HasPrefix(plain.prompt[node], conventionsHeader) {
			t.Errorf("%s: prefixed without the flag", node)
		}
	}
	// The precondition that makes the comparison worth something: the mapped
	// node really carries its staged agent, under the flag.
	if name, ok := with.argv["judge the proposal"].value("--agent"); !ok || name != "code-reviewer" {
		t.Errorf("the agent-mapped node lost --agent under the flag: %q", with.argv["judge the proposal"])
	}
	if sources, ok := with.argv["judge the proposal"].value("--setting-sources"); !ok || sources != "" {
		t.Errorf("layer 1 moved under the flag: %q", with.argv["judge the proposal"])
	}
}

func TestGroupThousands(t *testing.T) {
	for n, want := range map[int]string{0: "0", 999: "999", 1000: "1,000", 69812: "69,812", 131072: "131,072", 1234567: "1,234,567"} {
		if got := groupThousands(n); got != want {
			t.Errorf("groupThousands(%d) = %q, want %q", n, got, want)
		}
	}
}
