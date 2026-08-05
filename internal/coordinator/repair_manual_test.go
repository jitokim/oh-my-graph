//go:build manual

package coordinator

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// stubbedFirstReply answers the FIRST planner call with a canned refused spec
// and every later call with the real runner. It is what makes the measurement
// below possible at all: a real planner cannot be made to emit a specific
// invalid graph on demand, but the repair prompt it then receives is
// byte-identical to the one a genuine refusal produces — same base prompt,
// same validator refusals, same fence — because plan() builds it from the
// refusal and nothing else.
type stubbedFirstReply struct {
	real         runner.NodeRunner
	refused      string
	calls        int
	repairPrompt string
}

func (r *stubbedFirstReply) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.calls++
	if r.calls == 1 {
		return runner.NodeOutcome{Result: r.refused}, nil
	}
	r.repairPrompt = spec.Prompt
	return r.real.Run(ctx, spec)
}

// TestManual_RepairPromptConvergesOnARealPlanner measures the bounded re-plan's
// entire hypothesis — that a real planner, handed the validator's own refusals
// and told it is a fresh call, comes back with a graph that clears the same
// ceiling. Run against a REAL `claude` on the user's own subscription; it costs
// a few cents and is MANUAL ONLY, never CI (the same posture as `make smoke`):
//
//	go test -tags manual ./internal/coordinator -run TestManual_Repair -v -count=1
//
// The unit tests prove the mechanism is bounded, untrusted and fenced. None of
// them can say whether it CONVERGES, because a FakeRunner's corrected reply is
// whatever the fixture scripted. This is the only place that question gets a
// real answer, and a failure here is a finding about the prompt, not a bug in
// the harness — record the verbatim refusal in the CHANGELOG's measurement
// note rather than deleting the case.
//
// The two fixtures are the measured failure shapes: a `{{ feedback.<id> }}`
// carrying a filter (a graph.Parse load refusal) and a tool outside auto mode's
// allowlist (a planned-node refusal). They enter through different layers, so
// they exercise both kinds of refusal text the repair prompt can quote.
func TestManual_RepairPromptConvergesOnARealPlanner(t *testing.T) {
	cases := []struct {
		what    string
		goal    string
		refused string
	}{
		{
			what:    "a feedback placeholder carrying a filter",
			goal:    "implement a small helper function and have a reviewer node send it back until the review passes",
			refused: feedbackFilterSpec,
		},
		{
			what:    "a tool outside auto mode's allowlist",
			goal:    "find the build artifacts in this repository and list the ones that are safe to delete",
			refused: toolRefusedSpec,
		},
	}
	for _, tc := range cases {
		t.Run(tc.what, func(t *testing.T) {
			stub := &stubbedFirstReply{real: runner.NewClaudeCLIRunner(), refused: tc.refused}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
			defer cancel()
			plan, err := New(stub).Plan(ctx, tc.goal, nil)

			if stub.calls != 2 {
				t.Fatalf("planner ran %d times, want the stubbed refusal and exactly one real correction", stub.calls)
			}
			if !strings.Contains(stub.repairPrompt, repairMarker) {
				t.Fatal("the real call did not receive a repair prompt; this measurement would report on the wrong prompt")
			}
			if err != nil {
				t.Fatalf("THE REPAIR DID NOT CONVERGE — the corrected reply was refused too: %v", err)
			}

			// The verbatim record: what the planner produced when it was told
			// which rule it broke.
			t.Logf("converged: graph %q, %d node(s), correction cost $%.4f",
				plan.Graph.Name, len(plan.Graph.Nodes), plan.CostUSD)
			if plan.Repaired == nil {
				t.Error("a converged repair is not disclosed on the Plan")
			}
		})
	}
}
