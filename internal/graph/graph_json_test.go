package graph

import (
	"encoding/json"
	"reflect"
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
    timeout: 45m
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
    depends_on: [dev]
    handoff: session
    worktree: lane
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

	// Whole-graph deep equality, not a hand-enumerated field list: a field
	// added to Node/Graph/SuccessCheck/Verification/Retry that fails to
	// round-trip (missing json tag, mismatched key) turns this red without the
	// test needing to know the field exists. Both graphs came out of Parse, so
	// unexported derived state (byID, parsed timeouts) is comparable too.
	if !reflect.DeepEqual(original, roundTripped) {
		t.Fatalf("graph did not survive the JSON round-trip:\n got  %+v\n want %+v\nencoded: %s",
			roundTripped, original, encoded)
	}
}
