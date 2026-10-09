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
on_fail: continue
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
  - id: impl
    prompt: "redo with {{ feedback.check }}"
  - id: check
    depends_on: [impl]
    prompt: judge
    feedback: { rerun: impl, max: 2 }
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

// TestNode_JSONOmitsAnEmptySuccessCheck pins the omitzero tag on
// Node.SuccessCheck (#371): a node with no check encodes with NO success_check
// key — omitempty never omits a struct, and wrote `"success_check":{}` into
// every re-encoded graph.json — while each single predicate still makes the
// node carry it. Decoded as raw objects, because Parse reads an absent key and
// an empty object as the same zero check.
func TestNode_JSONOmitsAnEmptySuccessCheck(t *testing.T) {
	for name, tc := range map[string]struct {
		check SuccessCheck
		want  bool
	}{
		"no check":       {SuccessCheck{}, false},
		"exit_zero":      {SuccessCheck{ExitZero: true}, true},
		"result_matches": {SuccessCheck{ResultMatches: "^OK"}, true},
		"verify":         {SuccessCheck{Verify: &Verification{Command: "make test"}}, true},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(Node{ID: "n", Prompt: "p", SuccessCheck: tc.check})
			if err != nil {
				t.Fatalf("json.Marshal(Node): %v", err)
			}
			var raw map[string]json.RawMessage
			if err := json.Unmarshal(encoded, &raw); err != nil {
				t.Fatalf("encoded node is not a JSON object: %v", err)
			}
			if _, got := raw["success_check"]; got != tc.want {
				t.Errorf("success_check key present = %v, want %v: %s", got, tc.want, encoded)
			}
		})
	}
}
