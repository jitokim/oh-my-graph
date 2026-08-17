package handoff

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// LintFeedbackQuoting inspects every declared feedback arc for the one shape in
// which the loop runs exactly as written and repairs nothing: the arc fires,
// the engine re-runs the body, and no node in that body ever quotes
// `{{ feedback.<declarer> }}`. The re-run model is handed the same prompt it
// was handed the first time, produces the same output, and the declarer fails
// again for the same reason — twice the money, the same result. The warning
// goes on the RERUN TARGET, because that is where the fix goes.
//
// `feedback:` and `{{ feedback.<declarer> }}` are two halves of one mechanism
// (ADR 0010), and until this sweep either half loaded clean alone.
// `validateFeedback` checks the arc's topology and `validateFeedbackPlaceholders`
// checks that a token that IS written can resolve — neither asks whether the
// pair exists. Run 20260816-163759.091162000-1 is the specimen: a two-node loop
// whose `check` node declared `feedback: { rerun: build, max: 1 }` correctly and
// whose `build` prompt never mentioned the payload. The engine wrote
// `feedback/qa-a~check.out`, re-ran `build`, and round two failed identically to
// round one. `lint` said nothing.
//
// What counts as quoting it is any body node OTHER than the declarer, not the
// rerun target alone. On the two-node loop those are the same node, but on
// `build → refine → check` with `rerun: build` a token in `refine`'s prompt
// means round two really does read the findings and change what it produces —
// the loop repairs, just not at its first node — and warning there would be
// advice whose premise is false. The declarer itself is excluded on the
// opposite ground: it is the JUDGE, so its own re-run cannot repair anything.
// A loop where only the declarer quotes the payload re-judges unchanged
// artifacts while being reminded of its prior findings, which is not a repair
// round, it is asking a reviewer to change its mind — and that is the specimen's
// failure with an extra step, not an exemption from it.
//
// Only `prompt` is scanned. The token is legal in `cwd` and in a verify command
// too (validateFeedbackPlaceholders permits the whole body), but neither is read
// by a model: a payload in a verify command is the fifth sweep's finding
// (LintVerifyInlining) and a payload in a cwd is a path. This sweep's subject is
// whether the re-run's MODEL can see what was wrong.
//
// It must hold after fragment splicing, which is why it matches with the
// runtime's own placeholderPattern rather than by formatting a string: the
// specimen's real ids were `qa-a/check` and `qa-a/build`, so the token the
// loader rewrote to was `{{ feedback.qa-a/check }}` (ADR 0027). A sweep that
// only worked on hand-written ids would have missed the run that motivated it.
//
// It stays a warning rather than a load error because an ABSENT token has one
// legitimate reading (a body whose re-run reads the repository rather than the
// reply — a formatter re-run after a linter judged the tree — genuinely needs no
// payload), where a MISPLACED one has none, which is why ADR 0010 made that a
// load error. What it is NOT is advisory on the grounds that only a person can
// write this shape — a machine writes it too:
// `coordinator.validatePlannedNodes` constrains a planner-authored
// `feedback:` (its max, and its reach) rather than refusing it, and the planner
// prompt asks for this very pairing in prose. That half is machine-checked for a
// PLANNED graph by `coordinator.validatePlannedFeedbackQuoting`, which escalates
// these findings to a plan refusal — the same advisory-here/refusal-there split
// `graph.LintFeedbackReach` already has, and for the same reason: planner output
// has no author to read a warning (ADR 0028 §Decision 5).
func LintFeedbackQuoting(g *graph.Graph) []Warning {
	findings := FeedbackQuoteFindings(g)
	warnings := make([]Warning, 0, len(findings))
	for _, finding := range findings {
		warnings = append(warnings, Warning{
			NodeID: finding.Rerun,
			Field:  "prompt",
			Detail: fmt.Sprintf(
				"node %[1]q reruns this node on failure, but nothing in the loop quotes {{ feedback.%[1]s }} — the re-run gets the prompt it already ran, produces the same output, and %[1]q fails again for the same reason, at twice the cost. Quote {{ feedback.%[1]s }} in this prompt where the repair should read it (it is empty on the first pass by design)",
				finding.Declarer),
		})
	}
	if len(warnings) == 0 {
		return nil
	}
	return warnings
}

// FeedbackQuoteFinding is one blind loop, named from both ends: the arc's
// Declarer (whose payload nothing reads) and its Rerun target (whose prompt the
// missing quote belongs in).
//
// It exists so the coordinator can escalate this sweep to a plan refusal
// without re-deciding the predicate. Two computations of one rule drift — this
// repository has paid for that once already — so the sweep stays the single
// definition and the disposition lives at each caller, exactly as
// `graph.LintFeedbackReach` and `coordinator.validatePlannedFeedbackReach` are
// split.
type FeedbackQuoteFinding struct {
	Declarer string
	Rerun    string
}

// FeedbackQuoteFindings is LintFeedbackQuoting's predicate, before it is worded.
func FeedbackQuoteFindings(g *graph.Graph) []FeedbackQuoteFinding {
	var findings []FeedbackQuoteFinding
	for _, declarer := range g.Nodes {
		if declarer.Feedback == nil {
			continue
		}
		body := g.FeedbackBody(declarer.ID)
		if len(body) == 0 {
			// Not a validated arc — FeedbackBody reports an unknown or
			// non-ancestor target as nil and validateFeedback has already
			// refused it. There is no loop whose body could quote anything.
			continue
		}
		if bodyQuotesFeedback(g, body, declarer.ID) {
			continue
		}
		findings = append(findings, FeedbackQuoteFinding{Declarer: declarer.ID, Rerun: declarer.Feedback.Rerun})
	}
	return findings
}

// bodyQuotesFeedback reports whether any node in the loop body except the
// declarer itself quotes the declarer's feedback payload in its prompt. It
// matches with placeholderPattern — the very pattern Interpolate substitutes
// with — so what this sweep counts as a quote and what the runtime actually
// splices cannot drift apart, and a namespaced id spliced in from a multi-node
// fragment is matched like any other.
//
// The FILTER group is part of that promise, not a detail: `{{ feedback.D |
// inline }}` matches placeholderPattern, but the runtime does the opposite of
// splicing it — resolveLocked returns an *InterpolationError and the node fails
// (graph.Validate refuses the token at load, so no graph that came through Parse
// carries one). Counting it as a quote would be the ONE way this sweep and the
// runtime disagree: the runtime would fail the node, the sweep would call it
// wired. Both of today's callers run on a parsed graph, so the guard is
// unreachable today and is here so the pattern's third group cannot become a
// hole the next caller falls into.
func bodyQuotesFeedback(g *graph.Graph, body []string, declarerID string) bool {
	for _, id := range body {
		if id == declarerID {
			continue
		}
		node, ok := g.NodeByID(id)
		if !ok {
			continue
		}
		for _, groups := range placeholderPattern.FindAllStringSubmatch(node.Prompt, -1) {
			if groups[1] == "feedback" && groups[2] == declarerID && groups[3] == "" {
				return true
			}
		}
	}
	return false
}
