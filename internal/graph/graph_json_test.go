package graph

import (
	"encoding/json"
	"testing"
)

// TestNode_JSONRoundTripsThroughParse guards the resumable-snapshot contract
// documented on Node and Graph: json.Marshal(*Graph) must produce bytes that
// Parse (yaml.Unmarshal under the hood) can read back into an equal graph.
// internal/runstate relies on exactly this to snapshot a hand-written `run`'s
// graph (auto mode already gets JSON straight from the planner). A field
// added to Node/Graph/SuccessCheck/Verification/Retry without a matching json
// tag would still compile, still pass every other test, and would only show
// up as that field silently vanishing from a resumed run — this test is what
// turns that into a red test instead.
func TestNode_JSONRoundTripsThroughParse(t *testing.T) {
	original := parseGraph(t, `
name: round-trip
version: "1"
inputs: [repo, target]
concurrency: 3
nodes:
  - id: dev
    prompt: "write the thing for {{ inputs.repo }}"
    cwd: /work/app
    allowed_tools: [Read, "Bash(git *)"]
    permission_mode: dontAsk
    budget_usd: 0.5
    handoff: artifact
    agent: code-reviewer
    success_check:
      exit_zero: true
      result_matches: "PASS"
      verify:
        command: "make test"
        cwd: /work/app/sub
        timeout: 3m
        expect_exit: 0
        output_matches: "ok"
    retry: { max: 2, on: [nonzero_exit, verify_failed] }
  - id: approve
    type: gate
    depends_on: [dev]
  - id: ship
    prompt: ship
    depends_on: [approve]
    handoff: session
`)

	encoded, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("json.Marshal(*Graph): %v", err)
	}
	if !json.Valid(encoded) {
		t.Fatalf("encoded graph is not valid JSON: %s", encoded)
	}

	roundTripped, err := Parse(encoded)
	if err != nil {
		t.Fatalf("Parse(json.Marshal(*Graph)) failed: %v\nencoded: %s", err, encoded)
	}

	assertNodesEqual(t, "dev", original, roundTripped)
	assertNodesEqual(t, "approve", original, roundTripped)
	assertNodesEqual(t, "ship", original, roundTripped)

	if roundTripped.Name != original.Name || roundTripped.Version != original.Version {
		t.Fatalf("graph metadata did not round-trip: got name=%q version=%q, want name=%q version=%q",
			roundTripped.Name, roundTripped.Version, original.Name, original.Version)
	}
	if len(roundTripped.Nodes) != len(original.Nodes) {
		t.Fatalf("node count = %d, want %d", len(roundTripped.Nodes), len(original.Nodes))
	}
}

// assertNodesEqual compares one node by id across two graphs field by field —
// the fields that matter for a resumed run to behave exactly like the
// original.
func assertNodesEqual(t *testing.T, id string, want, got *Graph) {
	t.Helper()
	w, ok := want.NodeByID(id)
	if !ok {
		t.Fatalf("test setup: node %q missing from original graph", id)
	}
	g, ok := got.NodeByID(id)
	if !ok {
		t.Fatalf("node %q did not survive the JSON round-trip", id)
	}
	if g.ID != w.ID || g.Type != w.Type || g.Prompt != w.Prompt || g.Cwd != w.Cwd ||
		g.PermissionMode != w.PermissionMode || g.BudgetUSD != w.BudgetUSD ||
		g.Handoff != w.Handoff || g.Agent != w.Agent {
		t.Fatalf("node %q scalar fields did not round-trip:\n got  %+v\n want %+v", id, g, w)
	}
	if !equalStringSlices(g.DependsOn, w.DependsOn) || !equalStringSlices(g.AllowedTools, w.AllowedTools) {
		t.Fatalf("node %q slice fields did not round-trip:\n got  %+v\n want %+v", id, g, w)
	}
	if (g.Retry == nil) != (w.Retry == nil) {
		t.Fatalf("node %q retry presence did not round-trip: got %+v, want %+v", id, g.Retry, w.Retry)
	}
	if g.Retry != nil && (g.Retry.Max != w.Retry.Max || !equalStringSlices(g.Retry.On, w.Retry.On)) {
		t.Fatalf("node %q retry contents did not round-trip: got %+v, want %+v", id, g.Retry, w.Retry)
	}
	if g.SuccessCheck.ExitZero != w.SuccessCheck.ExitZero || g.SuccessCheck.ResultMatches != w.SuccessCheck.ResultMatches {
		t.Fatalf("node %q success_check did not round-trip: got %+v, want %+v", id, g.SuccessCheck, w.SuccessCheck)
	}
	if (g.SuccessCheck.Verify == nil) != (w.SuccessCheck.Verify == nil) {
		t.Fatalf("node %q verify presence did not round-trip", id)
	}
	if g.SuccessCheck.Verify != nil {
		gv, wv := g.SuccessCheck.Verify, w.SuccessCheck.Verify
		if gv.Command != wv.Command || gv.Cwd != wv.Cwd || gv.OutputMatches != wv.OutputMatches ||
			gv.TimeoutDuration() != wv.TimeoutDuration() || gv.ExpectedExitCode() != wv.ExpectedExitCode() {
			t.Fatalf("node %q verify contents did not round-trip: got %+v, want %+v", id, gv, wv)
		}
	}
}

func equalStringSlices(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
