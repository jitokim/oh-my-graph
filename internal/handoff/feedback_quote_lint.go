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
// It stays a warning rather than a load error for the standing reason every
// sweep in this package does: only a person can write what it condemns.
// `coordinator.validatePlannedNodes` refuses a planner-authored `feedback:`
// outright, so every arc this sweep can see is in a human's own reviewed file —
// and the shape is expressible on purpose, if barely: a body whose re-run reads
// the repository rather than the reply (a formatter re-run after a linter
// judged it) genuinely needs no payload. That author should still see the line
// once.
func LintFeedbackQuoting(g *graph.Graph) []Warning {
	var warnings []Warning
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
		warnings = append(warnings, Warning{
			NodeID: declarer.Feedback.Rerun,
			Field:  "prompt",
			Detail: fmt.Sprintf(
				"node %[1]q reruns this node on failure, but nothing in the loop quotes {{ feedback.%[1]s }} — the re-run gets the prompt it already ran, produces the same output, and %[1]q fails again for the same reason, at twice the cost. Quote {{ feedback.%[1]s }} in this prompt where the repair should read it (it is empty on the first pass by design)",
				declarer.ID),
		})
	}
	return warnings
}

// bodyQuotesFeedback reports whether any node in the loop body except the
// declarer itself quotes the declarer's feedback payload in its prompt. It
// matches with placeholderPattern — the very pattern Interpolate substitutes
// with — so what this sweep counts as a quote and what the runtime actually
// splices cannot drift apart, and a namespaced id spliced in from a multi-node
// fragment is matched like any other.
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
			if groups[1] == "feedback" && groups[2] == declarerID {
				return true
			}
		}
	}
	return false
}
