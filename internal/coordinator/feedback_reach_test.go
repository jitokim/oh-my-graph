package coordinator

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// fanInArcSpec is issue #118's plan, reduced to its topology and rendered as
// the planner would emit it: `scope` feeding two independent producers, a
// reviewer fanning in from both, and the feedback arc aimed at ONE of them.
//
// It is VALID. Every ADR 0010 load rule holds, `lint` says "valid", and the
// run it produced spent ~$14 re-running the healthy branch against a file the
// loop could not reach before halting. The refusal below is what stops this
// shape being written in the first place.
const fanInArcSpec = `{"name":"fan-in","version":"1","nodes":[` +
	`{"id":"scope","prompt":"scope the work","allowed_tools":["Read"]},` +
	`{"id":"qa-plan","depends_on":["scope"],"prompt":"write QA-PLAN.md","allowed_tools":["Write"]},` +
	`{"id":"load-script","depends_on":["scope"],"prompt":"write canary-load.js; {{ feedback.review }}","allowed_tools":["Write"]},` +
	`{"id":"review","depends_on":["qa-plan","load-script"],"prompt":"read both files on disk and judge them",` +
	`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"load-script","max":2}}]}`

// TestPlan_RefusesFanInArcThatCannotReachAProducer is #118 at the point it was
// authored. The refusal has to carry all four ids the reporter reconstructed
// from graph.json by hand — who judges, what the arc re-runs, whose artifact is
// unreachable — plus the fourth thing they had to work out for themselves: the
// target that would have worked.
func TestPlan_RefusesFanInArcThatCannotReachAProducer(t *testing.T) {
	fake, _ := newPlannerFake(runnerOutcome(fanInArcSpec))

	planErr := planExpectingError(t, fake, "canary the staging deploy")
	for _, want := range []string{"review", "load-script", "qa-plan", "feedback"} {
		if !strings.Contains(planErr.Reason, want) {
			t.Errorf("refusal %q does not name %q", planErr.Reason, want)
		}
	}
}

// TestPlan_RefusalNamesTheTargetTheSweepComputed is the anti-drift assertion,
// and the reason this rule reuses graph.LintFeedbackReach instead of deciding
// coverage again on this side of the seam. The target the planner is told to
// aim at must be the one the sweep computed and re-validated — not a second
// answer that merely agrees today.
//
// Asserted against the sweep's own output rather than the literal "scope", so
// a change to how the covering target is chosen shows up as a real
// disagreement instead of as two literals updated in lockstep.
func TestPlan_RefusalNamesTheTargetTheSweepComputed(t *testing.T) {
	g, err := graph.Parse([]byte(fanInArcSpec))
	if err != nil {
		t.Fatalf("the fixture must load — it is a VALID graph, which is the point: %v", err)
	}
	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Suggestion == "" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory carrying a covering target", advisories)
	}

	fake, _ := newPlannerFake(runnerOutcome(fanInArcSpec))
	planErr := planExpectingError(t, fake, "canary the staging deploy")
	if !strings.Contains(planErr.Reason, advisories[0].Suggestion) {
		t.Errorf("refusal %q does not name the sweep's covering target %q", planErr.Reason, advisories[0].Suggestion)
	}
}

// TestPlan_RefusalReadsAsEnglishForSeveralProducers exercises the sentence's
// plural path, which the single-producer fixture above never reaches. One
// refusal covers every producer a declarer cannot reach, so the agreement words
// and both pronoun cases are all interpolated — and a refusal is a PROMPT: it
// is quoted verbatim into the repair call that has to be understood well enough
// to converge on the first and only retry, so "depend on they" is a defect in
// the thing this rule spends money on, not a typo in a log line.
func TestPlan_RefusalReadsAsEnglishForSeveralProducers(t *testing.T) {
	spec := `{"name":"wide-fan-in","version":"1","nodes":[` +
		`{"id":"scope","prompt":"scope the work","allowed_tools":["Read"]},` +
		`{"id":"qa-plan","depends_on":["scope"],"prompt":"write QA-PLAN.md","allowed_tools":["Write"]},` +
		`{"id":"fixtures","depends_on":["scope"],"prompt":"write the fixtures","allowed_tools":["Write"]},` +
		`{"id":"load-script","depends_on":["scope"],"prompt":"write canary-load.js; {{ feedback.review }}","allowed_tools":["Write"]},` +
		`{"id":"review","depends_on":["qa-plan","fixtures","load-script"],"prompt":"judge all three",` +
		`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"load-script","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	planErr := planExpectingError(t, fake, "canary the staging deploy")
	for _, want := range []string{
		`excludes "qa-plan" and "fixtures" — nodes this one depends on`,
		"what they produced",
		`depends on what "qa-plan" and "fixtures" are`,
		"If they are work this node judges",
		`have "load-script" depend on them instead`,
	} {
		if !strings.Contains(planErr.Reason, want) {
			t.Errorf("refusal is missing %q:\n%s", want, planErr.Reason)
		}
	}
}

// TestPlan_FanInArcAimedAtTheCoveringTargetIsAccepted is the negative control
// the refusal is worthless without: the correction it names must itself plan.
// A rule that refused the bad shape AND the good one would just be a ban on
// fan-in review loops.
//
// The corrected spec is derived from the refused one by aiming the arc at the
// sweep's own suggestion, so this cannot pass by testing a hand-written graph
// that happens to differ in some other way too.
func TestPlan_FanInArcAimedAtTheCoveringTargetIsAccepted(t *testing.T) {
	g, err := graph.Parse([]byte(fanInArcSpec))
	if err != nil {
		t.Fatalf("parse the refused spec: %v", err)
	}
	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Suggestion == "" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory carrying a covering target", advisories)
	}
	corrected := strings.Replace(fanInArcSpec,
		`"rerun":"load-script"`, `"rerun":"`+advisories[0].Suggestion+`"`, 1)

	fake, _ := newPlannerFake(runnerOutcome(corrected))
	if _, err := New(fake).Plan(context.Background(), "canary the staging deploy", nil); err != nil {
		t.Fatalf("the target the refusal tells the planner to use must plan cleanly, got: %v", err)
	}
}

// TestPlan_StableContextParentUpstreamOfTheTargetIsAccepted is the shape this
// rule must not break: `spec → impl → review` with `rerun: impl`, the most
// idiomatic "judge the implementation against the acceptance criteria" graph
// there is. `spec` is a second parent of the reviewer and is NOT in the loop
// body, and that is correct — it is upstream of the whole loop, its artifact is
// stable, and re-running it every round would re-judge the implementation
// against criteria that just moved underneath it (ADR 0010 rule 3's carve-out).
//
// The rule distinguishes this from #118 on ancestry, not on fan-in: here the
// extra parent lies on a path to the rerun target, in #118 it lies beside it.
func TestPlan_StableContextParentUpstreamOfTheTargetIsAccepted(t *testing.T) {
	spec := `{"name":"spec-context","version":"1","nodes":[` +
		`{"id":"spec","prompt":"write the acceptance criteria","allowed_tools":["Write"]},` +
		`{"id":"impl","depends_on":["spec"],"prompt":"implement it; {{ feedback.review }}","allowed_tools":["Edit"]},` +
		`{"id":"review","depends_on":["spec","impl"],"prompt":"judge the implementation against the criteria",` +
		`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"impl","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "implement the feature", nil); err != nil {
		t.Fatalf("a stable-context parent upstream of the rerun target must still plan, got: %v", err)
	}
}

// TestPlan_FanInArcWithNoCoveringTargetIsAccepted pins the deliberate weakening.
// Two independent producer roots have no common ancestor, so no rerun target
// covers both and there is nothing to tell the planner to do — refusing would
// burn the one corrected re-plan on a shape no aiming of the arc can fix, and
// lose a plan the user already paid for.
//
// It is also where the honest false positive lives: a sibling corpus root is
// topologically identical to #118's sibling work, and the engine sees
// `depends_on`, not which files a prompt judges. Declining to refuse what
// cannot be repaired is what keeps that indistinguishability from costing a
// working plan. `lint` still warns on this graph.
func TestPlan_FanInArcWithNoCoveringTargetIsAccepted(t *testing.T) {
	spec := `{"name":"two-roots","version":"1","nodes":[` +
		`{"id":"corpus","prompt":"gather the reference corpus","allowed_tools":["Read"]},` +
		`{"id":"work","prompt":"do the work; {{ feedback.review }}","allowed_tools":["Edit"]},` +
		`{"id":"review","depends_on":["corpus","work"],"prompt":"judge the work against the corpus",` +
		`"allowed_tools":["Read"],"success_check":{"result_matches":"^PASS$"},"feedback":{"rerun":"work","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	if _, err := New(fake).Plan(context.Background(), "do the work", nil); err != nil {
		t.Fatalf("an arc with no covering target must not be refused — there is no correction to ask for, got: %v", err)
	}
}

// TestPlan_MisaimedArcBuysACorrectedReplan is what makes the refusal cheap
// enough to be a refusal at all. A rejected plan is not a lost run: the
// validator's sentence goes back to a fresh planner call, which gets one
// chance to answer it. This proves the two mechanisms compose — the refusal
// reaches the repair prompt, and the corrected plan is accepted and disclosed.
func TestPlan_MisaimedArcBuysACorrectedReplan(t *testing.T) {
	corrected := strings.Replace(fanInArcSpec, `"rerun":"load-script"`, `"rerun":"scope"`, 1)
	fake, repairPrompt := newRepairFake(
		runner.NodeOutcome{Result: fanInArcSpec, TotalCostUSD: 0.02},
		runner.NodeOutcome{Result: corrected, TotalCostUSD: 0.03},
	)

	plan, err := New(fake).Plan(context.Background(), "canary the staging deploy", nil)
	if err != nil {
		t.Fatalf("a mis-aimed arc is repairable, got: %v", err)
	}
	if plan.Repaired == nil {
		t.Fatal("plan.Repaired is nil: a plan bought twice must say so")
	}
	if !strings.Contains(repairPrompt.Prompt, "qa-plan") {
		t.Error("the repair prompt does not carry the unreachable producer, so the planner is being asked to guess")
	}
	if !strings.Contains(repairPrompt.Prompt, "scope") {
		t.Error("the repair prompt does not carry the covering target, so the correction is not actionable")
	}
	if body := plan.Graph.FeedbackBody("review"); len(body) != 4 {
		t.Errorf("FeedbackBody(review) = %v, want the whole loop — the corrected arc must reach both producers", body)
	}
}

// TestPlan_ReachRefusalSurvivesACrowdedRepairPrompt pins the ORDER
// validatePlannedNodes builds its list in. repairSection truncates the joined
// refusals at maxIssuesInPrompt from the front only, and this refusal is by
// some margin the longest one the validator emits — appended after the per-node
// refusals, as it first was, a reply that is also wrong about twenty other
// nodes pushes it past the cut. The corrected reply would then re-emit the same
// arc, be refused for it a second time, and the single repair would be gone.
//
// Twenty-five padded refusals is not a realistic plan; it is the cheapest way
// to hold the ordering in place, since the failure is silent and only shows up
// as a run that did not converge.
func TestPlan_ReachRefusalSurvivesACrowdedRepairPrompt(t *testing.T) {
	crowded := strings.TrimSuffix(fanInArcSpec, "]}")
	for i := 0; i < 25; i++ {
		crowded += fmt.Sprintf(`,{"id":"pad-%02d","prompt":"pad","allowed_tools":["Read"],"permission_mode":"bypassPermissions"}`, i)
	}
	crowded += "]}"
	fake, repairPrompt := newRepairFake(
		runner.NodeOutcome{Result: crowded, TotalCostUSD: 0.02},
		runner.NodeOutcome{Result: fanInArcSpec, TotalCostUSD: 0.03},
	)

	// The second reply is still mis-aimed, so Plan fails — the assertion is on
	// what the one repair call was told, not on the outcome.
	if _, err := New(fake).Plan(context.Background(), "canary the staging deploy", nil); err == nil {
		t.Fatal("the repaired reply repeats the arc, so the plan must still fail")
	}
	for _, want := range []string{"qa-plan", "scope", "pad-00"} {
		if !strings.Contains(repairPrompt.Prompt, want) {
			t.Errorf("the repair prompt lost %q to truncation, so the planner cannot fix it:\n%s", want, repairPrompt.Prompt)
		}
	}
}

// TestPlannerPromptTeachesFanInFeedbackTargets pins that the planner is TOLD
// the rule, not only held to it. Enforcement never depends on the model reading
// this, but the guidance is what keeps the ordinary case a one-call plan: a
// planner that is not told will keep emitting #118's shape and paying for a
// second call to be told again.
func TestPlannerPromptTeachesFanInFeedbackTargets(t *testing.T) {
	for _, want := range []string{"MORE THAN ONE producing node", "common ancestor", "UPSTREAM"} {
		if !strings.Contains(plannerPromptTemplate, want) {
			t.Errorf("planner prompt does not teach the fan-in rerun rule (%q missing), so valid-looking plans will be refused after being paid for", want)
		}
	}
}
