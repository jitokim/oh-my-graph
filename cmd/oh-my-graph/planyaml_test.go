package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// planYAMLSpec is a planner reply with more shape than cycleSpec: an edge, a
// multi-line prompt holding a colon and a `#`, a handoff, a timeout, and a
// version that would turn into a number if the projection unquoted it. Every
// one of those is a way a hand-written YAML copy could drift from the JSON.
const planYAMLSpec = `{"name":"plan-yaml","version":"1","nodes":[` +
	`{"id":"survey","prompt":"survey the repo\nnote: list the # of files","allowed_tools":["Read","Grep"],"timeout":"5m"},` +
	`{"id":"write","depends_on":["survey"],"handoff":"session","prompt":"write it","allowed_tools":["Read","Write"]}]}`

// previewPlan runs `auto --plan-only` on spec through the real argv path and
// returns the printed output and the one plan directory it kept.
func previewPlan(t *testing.T, spec string) (string, string) {
	t.Helper()
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {ExitCode: 0, Result: spec, TotalCostUSD: 0.0417},
	})
	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--plan-only", "--no-agent-mapping", "--no-skill-activation"},
			fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("--plan-only returned error: %v", err)
	}
	return out, solePlanDir(t)
}

// --- failure cases first ----------------------------------------------------

// TestSpecAsYAML_RefusesWhatIsNotASpec (#342): the projection reports a
// spec it cannot read instead of writing an empty or partial graph.yaml.
func TestSpecAsYAML_RefusesWhatIsNotASpec(t *testing.T) {
	if _, err := specAsYAML([]byte(`{"name": "broken", "nodes": [`)); err == nil {
		t.Fatal("a truncated spec must be refused, not rendered")
	}
}

// TestSpecAsYAML_QuotesOnlyWhatWouldOtherwiseChangeType (#342): clearing the
// JSON quoting must not turn a string into a number or a bool, and the
// result must be block YAML — the point of the file is that a person edits it.
func TestSpecAsYAML_QuotesOnlyWhatWouldOtherwiseChangeType(t *testing.T) {
	out, err := specAsYAML([]byte(`{"version":"1","flag":"true","n":2,"name":"plain","list":["a"]}`))
	if err != nil {
		t.Fatalf("specAsYAML: %v", err)
	}
	got := string(out)
	for _, want := range []string{`version: "1"`, `flag: "true"`, "n: 2\n", "name: plain\n", "list:\n  - a\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("YAML projection should contain %q:\n%s", want, got)
		}
	}
	if strings.ContainsAny(got, "{}[]") {
		t.Errorf("YAML projection kept JSON's flow style:\n%s", got)
	}
}

// --- success case -----------------------------------------------------------

// TestRunAutoWith_PlanOnlySavesTheSameGraphAsYAML is #342's equality claim
// (ADR 0039 §9.1): --plan-only writes graph.yaml beside graph.json, owner-only
// like it, and graph.Load of each yields the same graph — same nodes, same
// fields, same order. The YAML is a projection of the spec, never a second
// source, so any difference at all is a failure.
func TestRunAutoWith_PlanOnlySavesTheSameGraphAsYAML(t *testing.T) {
	isolateRunHome(t)
	_, planDir := previewPlan(t, planYAMLSpec)

	jsonPath := filepath.Join(planDir, generatedSpecFileName)
	yamlPath := filepath.Join(planDir, generatedSpecYAMLFileName)
	if filepath.Base(yamlPath) != "graph.yaml" {
		t.Fatalf("the YAML copy is named %q, want graph.yaml", filepath.Base(yamlPath))
	}
	info, err := os.Stat(yamlPath)
	if err != nil {
		t.Fatalf("--plan-only must write graph.yaml beside graph.json: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Errorf("graph.yaml mode = %#o, want 0600 — the same content as graph.json, so the same stance", got)
	}
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read graph.yaml: %v", err)
	}
	if strings.HasPrefix(strings.TrimSpace(string(raw)), "{") {
		t.Errorf("graph.yaml is JSON under a YAML name, not block YAML:\n%s", raw)
	}

	fromJSON, err := graph.Load(jsonPath)
	if err != nil {
		t.Fatalf("graph.Load(graph.json): %v", err)
	}
	fromYAML, err := graph.Load(yamlPath)
	if err != nil {
		t.Fatalf("graph.Load(graph.yaml): %v", err)
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Errorf("graph.yaml loads a different graph than graph.json:\njson: %+v\nyaml: %+v", fromJSON, fromYAML)
	}
	if len(fromYAML.Nodes) != 2 || fromYAML.Nodes[0].ID != "survey" || fromYAML.Nodes[1].ID != "write" {
		t.Errorf("node order changed: %+v", fromYAML.Nodes)
	}
	if fromYAML.Version != "1" {
		t.Errorf("version = %q, want the string \"1\"", fromYAML.Version)
	}
}

// planNodeRunner passes every node planYAMLSpec plans, and the "ship" node the
// gate tests add after their gate, keyed by the first line of the prompt.
func planNodeRunner() *runner.FakeRunner {
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"survey the repo": {SessionID: "s-survey", Result: "PASS", ExitCode: 0},
		"write it":        {SessionID: "s-write", Result: "PASS", ExitCode: 0},
		"ship it":         {SessionID: "s-ship", Result: "PASS", ExitCode: 0},
	})
	fake.KeyFn = func(spec runner.NodeInvocation) string { return firstLine(spec.Prompt) }
	return fake
}

// addGateToPlanYAML is the edit ADR 0039 §9.1 asks of the user, done the way
// a user would: text appended to the saved graph.yaml's node list — a
// `type: gate` node with its depends_on on an existing node, and a node
// behind the gate that may run only once it is approved.
func addGateToPlanYAML(t *testing.T, yamlPath string) {
	t.Helper()
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read graph.yaml: %v", err)
	}
	// The append lands in the node list only if that list is the file's last
	// key, which the projection keeps from the planner's key order.
	if !strings.Contains(string(raw), "\nnodes:\n") || !strings.HasSuffix(string(raw), "      - Write\n") {
		t.Fatalf("graph.yaml no longer ends with its node list; the edit below would land elsewhere:\n%s", raw)
	}
	edit := "  - id: approve\n    type: gate\n    depends_on:\n      - write\n" +
		"  - id: ship\n    depends_on:\n      - approve\n    prompt: ship it\n"
	if err := os.WriteFile(yamlPath, append(raw, edit...), 0o600); err != nil {
		t.Fatalf("edit graph.yaml: %v", err)
	}
}

// --- the closing note -------------------------------------------------------

// TestRunAutoWith_PlanOnlyNoteRunsTheYAMLAndSaysHowToAddAGate pins #342's
// closing note (ADR 0039 §9.1(2)): the run command it prints names graph.yaml
// by its full path, it names the shipped gate example, and it prints NO run
// command for graph.json — the file whose edit would not hold a gate the user
// added to the YAML. The facts the note already carried stay.
func TestRunAutoWith_PlanOnlyNoteRunsTheYAMLAndSaysHowToAddAGate(t *testing.T) {
	isolateRunHome(t)
	out, planDir := previewPlan(t, planYAMLSpec)
	jsonPath := filepath.Join(planDir, generatedSpecFileName)
	yamlPath := filepath.Join(planDir, generatedSpecYAMLFileName)

	if strings.Contains(out, "oh-my-graph run "+jsonPath) {
		t.Errorf("the note still prints a run command for graph.json:\n%s", out)
	}
	if strings.Contains(out, "run <graph.json>") {
		t.Errorf("the preview still names `run <graph.json>` as its next step:\n%s", out)
	}
	for _, want := range []string{
		"`oh-my-graph run " + yamlPath + "`",
		"add a human gate",
		"`type: gate`",
		"`depends_on`",
		"`approve-merge` in graphs/merge-shepherd.yaml",
		// The facts the note carried before #342.
		"no node was executed",
		"$0.0417",
		"this is not a run",
		"`runs list`",
		jsonPath,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("plan-only note should contain %q:\n%s", want, out)
		}
	}
	// One sentence: the gate instruction is a single line of the note.
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "add a human gate") && !strings.Contains(line, "approve-merge") {
			t.Errorf("the gate instruction is split across lines:\n%s", out)
		}
	}
	// The example the note names must exist and must be a gate, or the note
	// sends the user to a file that does not show what it says.
	example, err := graph.Load(filepath.Join("..", "..", "graphs", "merge-shepherd.yaml"))
	if err != nil {
		t.Fatalf("load the named example: %v", err)
	}
	found := false
	for _, n := range example.Nodes {
		if n.ID == "approve-merge" {
			found = n.Type == graph.TypeGate
		}
	}
	if !found {
		t.Error("graphs/merge-shepherd.yaml has no `approve-merge` gate, but the note names it")
	}
}

// --- running the saved files --------------------------------------------------

// TestRunGraphWith_PlanYAMLAndJSONRunTheSameWay (#342, ADR 0039 §9.1(1)):
// run of an unedited graph.yaml takes the same path as run of its graph.json —
// the same nodes reach the runner, in the same order, under the same tool
// policy and permission mode. Neither file is a planned graph to `run`, so
// neither gets a different ceiling for its extension.
func TestRunGraphWith_PlanYAMLAndJSONRunTheSameWay(t *testing.T) {
	isolateRunHome(t)
	_, planDir := previewPlan(t, planYAMLSpec)

	type seen struct {
		Prompt, PermissionMode, ResumeSession string
		Policy                                runner.ToolPolicy
	}
	runFile := func(name string) []seen {
		fake := planNodeRunner()
		var err error
		captureStdout(t, func() {
			err = runGraphWith([]string{filepath.Join(planDir, name)}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		if err != nil {
			t.Fatalf("run %s returned error: %v", name, err)
		}
		var got []seen
		for _, inv := range fake.Invocations() {
			got = append(got, seen{inv.Prompt, inv.PermissionMode, inv.ResumeSession, inv.Policy})
		}
		return got
	}
	fromJSON, fromYAML := runFile(generatedSpecFileName), runFile(generatedSpecYAMLFileName)
	if len(fromJSON) != 2 {
		t.Fatalf("run graph.json launched %d nodes, want 2", len(fromJSON))
	}
	if !reflect.DeepEqual(fromJSON, fromYAML) {
		t.Errorf("run graph.yaml launched differently from run graph.json:\njson: %+v\nyaml: %+v", fromJSON, fromYAML)
	}
}

// TestRunGraphWith_EditedPlanYAMLPausesAtTheAddedGate is the test ADR 0039
// §9.1(3) says matters (#342): save a plan with --plan-only, edit its
// graph.yaml to add a `type: gate` node, run that file through the real `run`
// entrypoint, and the run pauses at the gate — exit 2, the gate recorded as
// paused, the node behind it not launched.
func TestRunGraphWith_EditedPlanYAMLPausesAtTheAddedGate(t *testing.T) {
	home := isolateRunHome(t)
	_, planDir := previewPlan(t, planYAMLSpec)
	yamlPath := filepath.Join(planDir, generatedSpecYAMLFileName)
	addGateToPlanYAML(t, yamlPath)

	fake := planNodeRunner()
	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{yamlPath}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "approve" {
		t.Fatalf("expected the run of the edited graph.yaml to pause at gate approve, got %T: %v", err, err)
	}
	if code := exitCodeForError(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a paused run", code)
	}
	ran := map[string]bool{}
	for _, inv := range fake.Invocations() {
		ran[firstLine(inv.Prompt)] = true
	}
	if !ran["survey the repo"] || !ran["write it"] {
		t.Errorf("the planned nodes before the gate must run; saw %v", ran)
	}
	if ran["ship it"] {
		t.Error("the node behind the added gate ran before anyone approved it")
	}
	snap := loadSnapshot(t, onlyRunID(t, home))
	if snap.Gate.PausedAt != "approve" {
		t.Errorf("snapshot paused at %q, want approve", snap.Gate.PausedAt)
	}
	if got := snap.Gate.Decisions["approve"]; got != runstate.GatePause {
		t.Errorf("Gate.Decisions[approve] = %q, want pause", got)
	}
}

// TestRunGraphWith_RunReadsOnlyTheFileItIsGiven is the failure direction of
// the same edit (#342): run of the graph.json beside an edited graph.yaml runs
// the JSON — it never picks up the YAML's gate by looking in the directory.
// (The warning ADR 0039 §9.1 asks of that run is a separate slice; this pins
// only that no file but the one named is read.)
func TestRunGraphWith_RunReadsOnlyTheFileItIsGiven(t *testing.T) {
	isolateRunHome(t)
	_, planDir := previewPlan(t, planYAMLSpec)
	addGateToPlanYAML(t, filepath.Join(planDir, generatedSpecYAMLFileName))

	fake := planNodeRunner()
	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{filepath.Join(planDir, generatedSpecFileName)}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("run graph.json must run the JSON's own two nodes to completion, got: %v", err)
	}
	for _, inv := range fake.Invocations() {
		if firstLine(inv.Prompt) == "ship it" {
			t.Error("run graph.json launched a node that exists only in the edited graph.yaml")
		}
	}
}
