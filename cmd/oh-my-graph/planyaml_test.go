package main

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
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
