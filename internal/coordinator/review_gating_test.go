package coordinator

import (
	"context"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// TestPlannedReviewGatingPatternMatchesReviewFragments holds the planner's copy
// of the gating verdict byte-identical to the spelling the shipped review
// fragments document (ADR 0043). The fragments give the pattern in a comment,
// single-quoted as a caller would paste it into YAML, so the assertion is that
// the quoted Go constant appears there verbatim; gated-lane, which gates for
// real, must carry it as its review's success_check.
func TestPlannedReviewGatingPatternMatchesReviewFragments(t *testing.T) {
	for fragment, want := range map[string]string{
		"fragments/review-style.yaml":    "'" + plannedReviewGatingPattern + "'",
		"fragments/review-security.yaml": "'" + plannedReviewGatingPattern + "'",
		"fragments/gated-lane.yaml":      "result_matches: '" + plannedReviewGatingPattern + "'",
	} {
		raw, err := graphs.FS.ReadFile(fragment)
		if err != nil {
			t.Fatalf("reading the shipped fragment: %v", err)
		}
		if !strings.Contains(string(raw), want) {
			t.Errorf("%s no longer documents coordinator.plannedReviewGatingPattern %q — the two spellings of the gating verdict drifted", fragment, plannedReviewGatingPattern)
		}
	}
}

// TestPlannedReviewGatingPatternGates pins what the pattern is for: CLEAN and
// MINOR: pass, emphasis included, and FINDINGS: or a preamble fails.
func TestPlannedReviewGatingPatternGates(t *testing.T) {
	re := regexp.MustCompile(plannedReviewGatingPattern)
	for reply, want := range map[string]bool{
		"CLEAN":                      true,
		"**CLEAN**":                  true,
		"MINOR:\n- wrap a line":      true,
		"**MINOR**: - wrap a line":   true,
		"FINDINGS:\n- a defect":      false,
		"CLEANUP needed":             false,
		"Reviewed it.\nCLEAN":        false,
		"Logic checks out. MINOR: x": false,
	} {
		if got := re.MatchString(reply); got != want {
			t.Errorf("gating pattern on %q = %v, want %v", reply, got, want)
		}
	}
}

// TestPlan_FeedbackBulletTeachesTheGatingVerdict pins #288's prompt half: the
// feedback-arc bullet tells the reviewing node the three-value verdict and
// hands it the gating pattern JSON-quoted, as the planner must copy it.
func TestPlan_FeedbackBulletTeachesTheGatingVerdict(t *testing.T) {
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	if _, err := New(fake).Plan(context.Background(), "implement the feature and review it", nil); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	const head = "- For an iterative implement→review loop"
	start := strings.Index(captured.Prompt, head)
	if start < 0 {
		t.Fatalf("planner prompt has no feedback-arc bullet starting %q", head)
	}
	bullet := captured.Prompt[start+len(head):]
	if end := strings.Index(bullet, "\n- "); end >= 0 {
		bullet = bullet[:end]
	}
	for _, want := range []string{
		"CLEAN",
		"MINOR:",
		"FINDINGS:",
		`"result_matches": ` + strconv.Quote(plannedReviewGatingPattern),
		// An untrue claim filed as "wording" must not pass as a nit.
		"claim that is not true",
		"is FINDINGS:, never MINOR:",
		"MINOR: is only for an item whose meaning is already",
	} {
		if !strings.Contains(bullet, want) {
			t.Errorf("feedback-arc bullet is missing %q:\n%s", want, bullet)
		}
	}
}

// TestPlan_FeedbackReviewerWithGatingCheckValidates is the plan the bullet asks
// for, end to end: a reviewer declaring the arc and carrying the gating
// success_check plans, and keeps the pattern unchanged.
func TestPlan_FeedbackReviewerWithGatingCheckValidates(t *testing.T) {
	spec := `{"name":"gated-loop","version":"1","nodes":[` +
		`{"id":"impl","prompt":"write the feature; review feedback (empty on the first pass): {{ feedback.review }}","allowed_tools":["Edit"]},` +
		`{"id":"review","depends_on":["impl"],"prompt":"Review it. Your previous reply: {{ self.previous }}. START with CLEAN, MINOR: or FINDINGS:.",` +
		`"allowed_tools":["Read"],"success_check":{"exit_zero":true,"result_matches":` + strconv.Quote(plannedReviewGatingPattern) + `},` +
		`"feedback":{"rerun":"impl","max":2}}]}`
	fake, _ := newPlannerFake(runnerOutcome(spec))

	plan, err := New(fake).Plan(context.Background(), "implement the feature and review it", nil)
	if err != nil {
		t.Fatalf("a planned reviewer with the gating success_check must plan, got: %v", err)
	}
	check := graphNode(t, plan.Graph, "review").SuccessCheck
	if !check.ExitZero || check.ResultMatches != plannedReviewGatingPattern {
		t.Errorf("review success_check = %+v, want exit_zero and the gating pattern unchanged", check)
	}
}
