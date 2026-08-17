package coordinator

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// blindLoopSpec is ADR 0028's specimen written as the planner would emit it: a
// correct arc, a correct topology, and an implementing node whose prompt never
// quotes the payload. It is VALID — every ADR 0010 load rule holds and `lint`
// would call it valid — so before validatePlannedFeedbackQuoting it planned,
// ran, and paid for `max` identical rounds.
const blindLoopSpec = `{"name":"blind-loop","version":"1","nodes":[` +
	`{"id":"impl","prompt":"write the feature","allowed_tools":["Edit"]},` +
	`{"id":"review","depends_on":["impl"],"prompt":"judge it",` +
	`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"impl","max":2}}]}`

// TestPlan_RefusesFeedbackArcNoBodyNodeQuotes is the escalation ADR 0028 §5
// takes: the planner is ASKED for the arc and its quote in one prompt sentence,
// and until this refusal only the arc was checked. Auto mode is where the fault
// is worst — no human reads `lint` on a graph `auto` planned and ran in the same
// breath — so the half that was prose is machine-checked here.
//
// The refusal must carry all three things the author of a blind loop otherwise
// reconstructs by hand: who declares the arc, whose prompt is missing the line,
// and the exact token to paste.
func TestPlan_RefusesFeedbackArcNoBodyNodeQuotes(t *testing.T) {
	fake, _ := newPlannerFake(runnerOutcome(blindLoopSpec))

	planErr := planExpectingError(t, fake, "implement the feature and review it")
	for _, want := range []string{`"review"`, `"impl"`, "{{ feedback.review }}", "empty on the first pass"} {
		if !strings.Contains(planErr.Reason, want) {
			t.Errorf("refusal %q does not carry %q", planErr.Reason, want)
		}
	}
}

// TestPlan_FeedbackArcQuotedAtTheRerunTargetIsAccepted is the refusal's negative
// control: the same graph with the token added is the plan the prompt asked for,
// and must pass. Without it the test above would also pass on a rule that
// refused every planned feedback arc — which is what ADR 0028 wrongly claimed
// the coordinator already did.
func TestPlan_FeedbackArcQuotedAtTheRerunTargetIsAccepted(t *testing.T) {
	spec := strings.Replace(blindLoopSpec, `"prompt":"write the feature"`,
		`"prompt":"write the feature; review feedback (empty on the first pass): {{ feedback.review }}"`, 1)
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "implement the feature and review it", nil); err != nil {
		t.Fatalf("a planned loop whose rerun target quotes the payload must plan, got: %v", err)
	}
}

// TestPlan_FeedbackArcQuotedAtAMiddleBodyNodeIsAccepted carries ADR 0028's
// decision 2 across the plan boundary. `impl → docs → verify` with
// `rerun: impl`, quoted at `docs`: the round genuinely reads the findings and
// produces something different, just not at the loop's first node. Refusing it
// would be a refusal whose premise is false — and it is the one shape where the
// escalated rule could be stricter than the advisory without anyone noticing,
// since the corpus contains no example of it (docs/measurements/0028-feedback-quote-corpus.md).
func TestPlan_FeedbackArcQuotedAtAMiddleBodyNodeIsAccepted(t *testing.T) {
	spec := `{"name":"middle-quote","version":"1","nodes":[` +
		`{"id":"impl","prompt":"write the feature","allowed_tools":["Edit"]},` +
		`{"id":"docs","depends_on":["impl"],"prompt":"document it; address {{ feedback.verify }}","allowed_tools":["Edit"]},` +
		`{"id":"verify","depends_on":["docs"],"prompt":"judge both",` +
		`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"impl","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "implement and document the feature", nil); err != nil {
		t.Fatalf("a quote at a middle body node is a repairing loop and must plan, got: %v", err)
	}
}

// TestPlan_FeedbackArcQuotedOnlyByTheDeclarerIsRefused pins the exclusion that
// makes the rule mean anything. The judge re-reading its own findings against
// unchanged artifacts is the specimen with an extra step: nothing in the body
// produces anything new, so the rounds are spent identically.
func TestPlan_FeedbackArcQuotedOnlyByTheDeclarerIsRefused(t *testing.T) {
	spec := strings.Replace(blindLoopSpec, `"prompt":"judge it"`,
		`"prompt":"judge it, recalling {{ feedback.review }}"`, 1)
	fake, _ := newPlannerFake(runnerOutcome(spec))

	planErr := planExpectingError(t, fake, "implement the feature and review it")
	if !strings.Contains(planErr.Reason, "{{ feedback.review }}") {
		t.Errorf("a loop only its own judge quotes must still be refused, got: %v", planErr.Reason)
	}
}

// TestPlan_BlindLoopBuysACorrectedReplan is what makes this a refusal rather
// than a warning nobody reads: a refused plan is not a lost run. The sentence
// goes back to a fresh planner call, which gets one chance to answer it — and
// the correction it asks for is one placeholder, harmless even in the case the
// refusal is wrong, since the feedback namespace resolves to empty on the first
// pass by design.
func TestPlan_BlindLoopBuysACorrectedReplan(t *testing.T) {
	corrected := strings.Replace(blindLoopSpec, `"prompt":"write the feature"`,
		`"prompt":"write the feature; {{ feedback.review }}"`, 1)
	fake, repairPrompt := newRepairFake(
		runner.NodeOutcome{Result: blindLoopSpec, TotalCostUSD: 0.02},
		runner.NodeOutcome{Result: corrected, TotalCostUSD: 0.03},
	)

	plan, err := New(fake).Plan(context.Background(), "implement the feature and review it", nil)
	if err != nil {
		t.Fatalf("the corrected plan must be accepted: %v", err)
	}
	if plan.Repaired == nil {
		t.Fatal("plan.Repaired is nil: a plan bought twice must say so")
	}
	if !strings.Contains(repairPrompt.Prompt, "{{ feedback.review }}") {
		t.Error("the repair prompt does not carry the token to paste, so the correction is not actionable")
	}
}
