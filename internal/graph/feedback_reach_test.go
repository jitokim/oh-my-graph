package graph

import (
	"path/filepath"
	"strings"
	"testing"
)

// fanInReviewYAML is issue #118's graph, reduced to its topology: one scope
// node feeding two independent producers, a reviewer fanning in from both,
// and a feedback arc aimed at ONE of them. The reviewer's prompt judges both
// producers' files — by literal path, exactly as the planner wrote it, which
// is why no placeholder scan sees the second one.
//
// Note what qa-plan does NOT have: a {{ feedback.review }} token. A producer
// left outside the body that still ASKS for the payload is already a load
// error (validateFeedbackPlaceholders), which is the half of this bug the
// engine could always catch. This sweep exists for the other half — the
// producer that never asks, and so loads clean.
const fanInReviewYAML = `
name: fan-in
nodes:
  - id: scope
    prompt: scope the work
  - id: qa-plan
    depends_on: [scope]
    prompt: "write stg-canary/QA-PLAN.md"
  - id: load-script
    depends_on: [scope]
    prompt: "write stg-canary/k6/canary-load.js; feedback: {{ feedback.review }}"
  - id: review
    depends_on: [qa-plan, load-script]
    prompt: read stg-canary/QA-PLAN.md and stg-canary/k6/canary-load.js on disk
    success_check: { exit_zero: true, result_matches: "^PASS$" }
    feedback: { rerun: load-script, max: 2 }
  - id: runbook
    depends_on: [review]
    prompt: write the runbook
`

// TestLintFeedbackReach_FanInSiblingProducerIsUnreachable is issue #118: the
// graph is VALID — it loads, and every ADR 0010 rule holds — and still cannot
// repair the file its reviewer judged. The advisory has to carry all three
// ids, because reconstructing them from graph.json by hand is what the
// reporter had to do.
func TestLintFeedbackReach_FanInSiblingProducerIsUnreachable(t *testing.T) {
	g := parseGraph(t, fanInReviewYAML)

	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 {
		t.Fatalf("LintFeedbackReach() returned %d advisories, want exactly 1 (for qa-plan): %v", len(advisories), advisories)
	}
	got := advisories[0]
	if got.Declarer != "review" || got.Rerun != "load-script" || got.Producer != "qa-plan" {
		t.Errorf("advisory identifies (declarer, rerun, producer) = (%q, %q, %q), want (review, load-script, qa-plan)", got.Declarer, got.Rerun, got.Producer)
	}
	for _, want := range []string{"review", "load-script", "qa-plan", "rerun: scope"} {
		if !strings.Contains(got.String(), want) {
			t.Errorf("advisory %q does not name %q", got.String(), want)
		}
	}
}

// TestLintFeedbackReach_SuggestedTargetActuallyFixesIt closes the loop on the
// suggestion: applying it must make the sweep quiet AND leave the graph
// valid. An advisory that recommends an edit nobody checked would be the
// planner's mistake one level up.
func TestLintFeedbackReach_SuggestedTargetActuallyFixesIt(t *testing.T) {
	fixed := parseGraph(t, strings.Replace(fanInReviewYAML, "rerun: load-script", "rerun: scope", 1))
	if body := fixed.FeedbackBody("review"); len(body) != 4 {
		t.Fatalf("FeedbackBody(review) with rerun: scope = %v, want all four loop nodes", body)
	}
	if advisories := fixed.LintFeedbackReach(); len(advisories) != 0 {
		t.Errorf("the suggested rerun target still warns: %v", advisories)
	}
}

// TestLintFeedbackReach_LinearLoopIsQuiet guards the shape the construct was
// designed for (graphs/review-loop.yaml): one producer, one reviewer, nothing
// to be unreachable.
func TestLintFeedbackReach_LinearLoopIsQuiet(t *testing.T) {
	if advisories := parseGraph(t, validLoopYAML).LintFeedbackReach(); len(advisories) != 0 {
		t.Errorf("the minimal impl → localrun → review loop warned: %v", advisories)
	}
}

// TestLintFeedbackReach_ShippedGraphsAreQuiet is the false-positive gate for
// every graph this repo ships: none of them may warn. It is the evidence that
// the sweep does not fire on the templates users start from — and it will
// fail loudly if someone later ships a fan-in reviewer whose arc misses a
// producer, which is the point.
func TestLintFeedbackReach_ShippedGraphsAreQuiet(t *testing.T) {
	for _, name := range shippedTemplateNames(t) {
		loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("shipped graph %s failed to load: %v", name, err)
		}
		if advisories := loaded.Graph.LintFeedbackReach(); len(advisories) != 0 {
			t.Errorf("shipped graph %s warned: %v", name, advisories)
		}
	}
}

// TestLintFeedbackReach_GateParentIsNotWarned covers the shape where refusing
// would contradict ADR 0010's own rule 4: a gate may never be in a feedback
// body, so a fan-in declarer with a gate parent could satisfy no coverage
// rule at all. Advice nobody can act on is noise, so the gate is skipped.
func TestLintFeedbackReach_GateParentIsNotWarned(t *testing.T) {
	g := parseGraph(t, `
name: gated
nodes:
  - id: approve
    type: gate
    prompt: approve the plan
  - id: impl
    prompt: "implement: {{ feedback.review }}"
  - id: review
    depends_on: [approve, impl]
    prompt: judge the work
    success_check: { result_matches: "ready" }
    feedback: { rerun: impl, max: 2 }
`)
	if advisories := g.LintFeedbackReach(); len(advisories) != 0 {
		t.Errorf("a gate parent warned: %v", advisories)
	}
}

// TestLintFeedbackReach_StableContextProducerWarnsToo documents the sweep's
// known false positive, and is the reason this is not a load error: a
// reviewer reading a settled spec node alongside the work under review is a
// legitimate graph — ADR 0010 rule 3 blesses out-of-body parents explicitly —
// and it warns anyway, because nothing in the topology distinguishes "stable
// context" from "sibling work the reviewer will find defects in". Refusing
// this graph would break it to catch a planner's mistake.
func TestLintFeedbackReach_StableContextProducerWarnsToo(t *testing.T) {
	g := parseGraph(t, `
name: spec-context
nodes:
  - id: spec
    prompt: write the acceptance criteria
  - id: impl
    depends_on: [spec]
    prompt: "implement against the criteria: {{ feedback.review }}"
  - id: review
    depends_on: [spec, impl]
    prompt: judge the implementation against the criteria
    success_check: { result_matches: "ready" }
    feedback: { rerun: impl, max: 2 }
`)
	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Producer != "spec" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory about the deliberately-out-of-body spec node", advisories)
	}
	if !strings.Contains(advisories[0].Detail, "rerun: spec") {
		t.Errorf("advisory %q should still offer the covering target, since aiming there is the author's call", advisories[0].Detail)
	}
}

// TestLintFeedbackReach_NoCoveringTargetSaysSo covers the case where the two
// producers are independent roots: no ancestor covers both, so there is no
// honest suggestion to make and the advisory must say the fix is structural
// rather than name a target that would not validate.
func TestLintFeedbackReach_NoCoveringTargetSaysSo(t *testing.T) {
	g := parseGraph(t, `
name: two-roots
nodes:
  - id: plan
    prompt: "plan: {{ feedback.review }}"
  - id: script
    prompt: write the script
  - id: review
    depends_on: [plan, script]
    prompt: judge both
    success_check: { result_matches: "ready" }
    feedback: { rerun: plan, max: 2 }
`)
	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Producer != "script" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory about script", advisories)
	}
	if !strings.Contains(advisories[0].Detail, "the fix is structural") {
		t.Errorf("advisory %q invented a target where none covers both roots", advisories[0].Detail)
	}
}

// TestLintFeedbackReach_SuggestionSkipsATargetThatWouldNotValidate proves the
// suggestion is validated and not merely covering: here the only ancestor
// covering both producers pulls a gate into the body, which ADR 0010 rule 4
// refuses. The sweep must fall back to "structural" rather than recommend a
// graph that will not load.
func TestLintFeedbackReach_SuggestionSkipsATargetThatWouldNotValidate(t *testing.T) {
	g := parseGraph(t, `
name: gated-ancestor
nodes:
  - id: scope
    prompt: scope the work
  - id: approve
    type: gate
    depends_on: [scope]
    prompt: approve the scope
  - id: qa-plan
    depends_on: [approve]
    prompt: write the plan
  - id: load-script
    depends_on: [approve]
    prompt: "write the script: {{ feedback.review }}"
  - id: review
    depends_on: [qa-plan, load-script]
    prompt: judge both
    success_check: { result_matches: "ready" }
    feedback: { rerun: load-script, max: 2 }
`)
	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Producer != "qa-plan" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory about qa-plan", advisories)
	}
	if strings.Contains(advisories[0].Detail, "Consider `rerun:") {
		t.Errorf("advisory %q suggested a target whose body would contain the gate", advisories[0].Detail)
	}
}
