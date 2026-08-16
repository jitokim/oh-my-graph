package graph

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
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

// TestLintFeedbackReach_SuggestionFieldAndProseAgree is the guard on the
// advisory's second consumer. The coordinator refuses a planned arc exactly
// when Suggestion is non-empty, while a human reads Detail — so if the field
// and the prose could disagree, `lint` would print advice auto mode did not
// act on, or auto mode would refuse a plan while the message said the fix was
// structural.
//
// Asserted as the property rather than against a literal target: whatever
// coveringFeedbackTarget picks, the prose must name that node and the field
// must hold it, and applying the field's value must actually silence the
// sweep.
func TestLintFeedbackReach_SuggestionFieldAndProseAgree(t *testing.T) {
	advisories := parseGraph(t, fanInReviewYAML).LintFeedbackReach()
	if len(advisories) != 1 {
		t.Fatalf("LintFeedbackReach() = %v, want exactly 1", advisories)
	}
	got := advisories[0]
	if got.Suggestion == "" {
		t.Fatal("a covering target exists for #118's graph, so Suggestion must carry it")
	}
	if !strings.Contains(got.Detail, "rerun: "+got.Suggestion) {
		t.Errorf("Detail %q does not name Suggestion %q, so the field and the prose disagree", got.Detail, got.Suggestion)
	}
	fixed := parseGraph(t, strings.Replace(fanInReviewYAML, "rerun: load-script", "rerun: "+got.Suggestion, 1))
	if advisories := fixed.LintFeedbackReach(); len(advisories) != 0 {
		t.Errorf("aiming the arc at Suggestion %q still warns: %v", got.Suggestion, advisories)
	}
}

// TestLintFeedbackReach_StructuralAdviceCarriesNoSuggestion is the other half:
// when no target covers both producers the field must be empty, because that
// emptiness is what keeps auto mode from refusing a plan it could not tell the
// planner how to fix.
func TestLintFeedbackReach_StructuralAdviceCarriesNoSuggestion(t *testing.T) {
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
	if len(advisories) != 1 {
		t.Fatalf("LintFeedbackReach() = %v, want exactly 1", advisories)
	}
	if advisories[0].Suggestion != "" {
		t.Errorf("Suggestion = %q, want empty — no ancestor covers two independent roots", advisories[0].Suggestion)
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
// every YAML this repo ships — templates AND fragments, since a fragment is
// what pulls a reviewer into a template that never declared one.
//
// Be honest about what it proves TODAY: nothing. No shipped graph has a fan-in
// feedback declarer (only review-loop.yaml carries an arc at all, and it is
// linear), so the sweep has no shape here to fire on and the assertion passes
// vacuously. Its value is entirely forward-looking: it fails the moment
// someone ships a fan-in reviewer whose arc misses a producer, which is the
// mistake #118 was.
//
// A MULTI-NODE fragment file is excluded, and the reason is structural rather
// than an exemption (ADR 0027). The sweep needs a *Graph, and a fragment only
// becomes one when it is spliced: its `nodes:` carry `{{ with.x }}` where a
// graph carries values, so loading the file itself would judge templates as
// though they were durations and regexes. It is swept through every template
// that cites it — which the loop above walks, and which
// TestEmbeddedGraphsLoadFromTheBinarysOwnPayload proves at least one of exists.
// A SINGLE-node fragment stays in, unchanged: it declares no `nodes:` at all,
// so it loads as an empty graph and the sweep passes over it vacuously, exactly
// as it always did.
func TestLintFeedbackReach_ShippedGraphsAreQuiet(t *testing.T) {
	for _, name := range shippedYAMLNames(t) {
		if isMultiNodeFragmentFile(t, name) {
			continue
		}
		loaded, err := LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("shipped YAML %s failed to load: %v", name, err)
		}
		if advisories := loaded.Graph.LintFeedbackReach(); len(advisories) != 0 {
			t.Errorf("shipped YAML %s warned: %v", name, advisories)
		}
	}
}

// isMultiNodeFragmentFile reports whether an embedded payload path is a
// fragment declaring the multi-node form — the one shape that is a fragment AND
// spells the `nodes:` key a graph decoder reads.
func isMultiNodeFragmentFile(t *testing.T, name string) bool {
	t.Helper()
	if !strings.HasPrefix(name, "fragments/") {
		return false
	}
	data, err := graphs.FS.ReadFile(name)
	if err != nil {
		t.Fatalf("read embedded %s: %v", name, err)
	}
	return strings.Contains(string(data), "\nnodes:")
}

// shippedYAMLNames is every embedded YAML payload path, fragments included —
// unlike shippedTemplateNames, which filters them out because a fragment is
// not a runnable graph. For an advisory sweep that distinction does not apply:
// a fragment parses and validates on its own, and the sweep reads topology,
// so a fragment declaring a mis-aimed arc must be caught in the file that
// declares it rather than only in whichever templates happen to cite it.
func shippedYAMLNames(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed patterns in graphs/embed.go matched nothing")
	}
	return names
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

// TestLintFeedbackReach_ProducerUpstreamOfTheTargetIsQuiet is the shape the
// sweep must NOT warn on: `spec → impl → review` with `rerun: impl` is the
// most idiomatic "judge the implementation against the acceptance criteria"
// graph there is, and it is correct. spec is an ancestor of the rerun target,
// so it is upstream of the whole loop — ADR 0010 rule 3's carve-out exactly —
// and its output already flows into the body every round.
//
// Warning here would be worse than noise, because the covering target the
// advisory would name is `rerun: spec`: that re-runs the acceptance criteria
// every feedback round, so the implementation gets re-judged against criteria
// that just moved underneath it. The advice would be actively harmful.
func TestLintFeedbackReach_ProducerUpstreamOfTheTargetIsQuiet(t *testing.T) {
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
	if advisories := g.LintFeedbackReach(); len(advisories) != 0 {
		t.Errorf("a producer upstream of the rerun target warned: %v", advisories)
	}
}

// TestLintFeedbackReach_SiblingWorkStillWarnsUnderTheUpstreamSkip guards the
// narrowing from swallowing the bug it was built for. #118's qa-plan is a
// sibling of the rerun target, not an ancestor of it: nothing the arc re-runs
// can rewrite its file. The skip keys on ancestry precisely so this case is
// still reported while the spec shape above goes quiet — the two differ only
// in whether the producer lies on a path to the target.
func TestLintFeedbackReach_SiblingWorkStillWarnsUnderTheUpstreamSkip(t *testing.T) {
	g := parseGraph(t, fanInReviewYAML)
	if advisories := g.LintFeedbackReach(); len(advisories) != 1 || advisories[0].Producer != "qa-plan" {
		t.Fatalf("LintFeedbackReach() = %v, want the sibling producer qa-plan still reported", advisories)
	}
}

// TestLintFeedbackReach_MessageOwnsItsBlindSpot pins the honesty of the string
// the user actually reads, as opposed to the doc comment nobody reads. The
// sweep sees `depends_on`; it cannot see which artifacts a prompt judges, so
// the advisory may not assert the defect as fact — a sibling corpus root is
// topologically identical to #118's sibling work and is a false positive. The
// message has to carry that caveat itself, because the reader has only the
// message.
func TestLintFeedbackReach_MessageOwnsItsBlindSpot(t *testing.T) {
	advisories := parseGraph(t, fanInReviewYAML).LintFeedbackReach()
	if len(advisories) != 1 {
		t.Fatalf("LintFeedbackReach() = %v, want exactly 1", advisories)
	}
	detail := advisories[0].Detail
	if strings.Contains(detail, "can never be repaired") {
		t.Errorf("advisory states as fact a defect it cannot see: %q", detail)
	}
	for _, want := range []string{"depends_on", "stable context"} {
		if !strings.Contains(detail, want) {
			t.Errorf("advisory %q does not disclose its blind spot (%q missing)", detail, want)
		}
	}
}

// TestLintFeedbackReach_SuggestionIsDeterministicUnderATie exercises the
// declarationIndex tie-break, which exists only because ancestorSet returns a
// map and Go randomizes map iteration. Without a fixture producing two equal-
// size covering candidates the branch never executes, and advice that differed
// between runs would ship undetected.
//
// Here x and y are interchangeable diamond shoulders: both bodies are
// {shoulder, p, q, review}, both cover the current target and the unreachable
// producer, and both validate. Only declaration order separates them, so x
// must win on every one of these runs.
func TestLintFeedbackReach_SuggestionIsDeterministicUnderATie(t *testing.T) {
	g := parseGraph(t, `
name: diamond-tie
nodes:
  - id: root
    prompt: scope the work
  - id: x
    depends_on: [root]
    prompt: left shoulder
  - id: y
    depends_on: [root]
    prompt: right shoulder
  - id: p
    depends_on: [x, y]
    prompt: "write one: {{ feedback.review }}"
  - id: q
    depends_on: [x, y]
    prompt: write two
  - id: review
    depends_on: [p, q]
    prompt: judge both
    success_check: { result_matches: "ready" }
    feedback: { rerun: p, max: 2 }
`)
	// Enough repetitions that a tie decided by map order would almost surely
	// disagree with itself at least once.
	for i := 0; i < 64; i++ {
		advisories := g.LintFeedbackReach()
		if len(advisories) != 1 || advisories[0].Producer != "q" {
			t.Fatalf("LintFeedbackReach() = %v, want one advisory about q", advisories)
		}
		if !strings.Contains(advisories[0].Detail, "rerun: x") {
			t.Fatalf("run %d suggested a different target than the earlier runs: %q", i, advisories[0].Detail)
		}
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

// judgedAgainstAnInterpolatedSiblingYAML is the counterexample that closed the
// escalation lane on issue #118. The proposal was to make this sweep SAFE TO
// ESCALATE on a subset — fire hard (a load error, not advice) only when the
// declarer's prompt interpolates the unreachable producer's artifact, since
// `{{ artifacts.criteria }}` proves the reviewer actually reads it.
//
// This graph satisfies that predicate exactly, and it is CORRECT. Every defect
// `review` can emit is a defect in `impl` measured against `criteria`, and
// `rerun: impl` repairs precisely that; `criteria` is the yardstick, not the
// thing measured. Escalating would refuse it, and the covering target the
// advisory names — `rerun: scope` — would rewrite the acceptance criteria every
// round, ADR 0010's "actively harmful" (see
// TestLintFeedbackReach_ProducerUpstreamOfTheTargetIsQuiet).
//
// The predicate's error is that `{{ artifacts.P }}` proves the reviewer READS
// P. The defect needs the reviewer to JUDGE P. The graph has no token
// separating "read as yardstick" from "read as thing measured" — the two are
// the same syntax — so the scan narrows the blind spot without touching the
// load-bearing half.
const judgedAgainstAnInterpolatedSiblingYAML = `
name: judged-against-criteria
nodes:
  - id: scope
    prompt: scope the work
  - id: criteria
    depends_on: [scope]
    prompt: write the acceptance criteria
  - id: impl
    depends_on: [scope]
    prompt: "implement it; feedback: {{ feedback.review }}"
  - id: review
    depends_on: [criteria, impl]
    prompt: "judge {{ artifacts.impl }} against the criteria in {{ artifacts.criteria }}"
    success_check: { exit_zero: true, result_matches: "^PASS$" }
    feedback: { rerun: impl, max: 2 }
`

// TestLintFeedbackReach_InterpolatingASiblingStaysAdvisory pins the rejection
// so the next reader does not re-derive it. It asserts the two facts that
// together sink the "escalate on {{ artifacts.P }}" proposal, in the one
// direction each fails:
//
//   - The predicate FIRES on a correct graph. Refusing it would break working
//     hand-written graphs, and interpolating a sibling's artifact is the
//     idiomatic way to supply stable context — so the subset selects TOWARD
//     false positives.
//   - The predicate does NOT fire on #118 itself, whose reviewer named its two
//     files by literal path. The escalation would not have caught the bug that
//     motivated it.
//
// The predicate itself is mechanically computable — judgesByPlaceholder below
// decides it — but a rule with both false positives (it hard-fails correct
// graphs) and false negatives (it misses the motivating one) is not safe to
// escalate on. The sweep stays advisory; the remaining lever is runtime
// (let the reviewer name its own next target), not static.
func TestLintFeedbackReach_InterpolatingASiblingStaysAdvisory(t *testing.T) {
	g := parseGraph(t, judgedAgainstAnInterpolatedSiblingYAML)
	if err := g.Validate(); err != nil {
		t.Fatalf("the counterexample must be a valid graph, else it proves nothing: %v", err)
	}

	advisories := g.LintFeedbackReach()
	if len(advisories) != 1 || advisories[0].Producer != "criteria" {
		t.Fatalf("LintFeedbackReach() = %v, want one advisory about criteria", advisories)
	}
	if !judgesByPlaceholder(g, "review", "criteria") {
		t.Fatal("the counterexample must interpolate {{ artifacts.criteria }}, or it is not in the proposed subset")
	}
	if advisories[0].Suggestion != "scope" {
		t.Errorf("advisory suggests %q, want scope — the record is that the escalated advice would re-run the criteria every round", advisories[0].Suggestion)
	}

	// #118's own reviewer reads its producers off disk by literal path, so the
	// proposed predicate is false there: the escalation would have stayed quiet
	// on the very bug it was meant to refuse.
	bug := parseGraph(t, fanInReviewYAML)
	if judgesByPlaceholder(bug, "review", "qa-plan") {
		t.Error("issue #118's reviewer must reach its producer by literal path; if it interpolates, this test no longer records the adverse selection")
	}
}

// judgesByPlaceholder reports whether declarer's prompt interpolates
// producer's artifact — the predicate the rejected escalation would have
// keyed on. Spelled out here rather than reused from the handoff sweep
// because it is the PROPOSAL's rule, not the engine's: nothing in production
// computes it, and nothing should start.
func judgesByPlaceholder(g *Graph, declarer, producer string) bool {
	return strings.Contains(g.byID[declarer].Prompt, "{{ artifacts."+producer+" }}")
}
