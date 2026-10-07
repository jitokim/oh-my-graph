package handoff

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// GateDescriptionError is one token a gate's `description:` may not carry
// (#346). It is a refusal, not a Warning: the description is printed to the
// person deciding the gate, at the moment they decide it, so a token that puts
// a model's text there is the approval prompt being written by the thing it
// approves — there is no "advice" severity for that.
type GateDescriptionError struct {
	NodeID string // the gate
	Token  string // the offending {{ ... }} token, verbatim
	Reason string
}

func (e *GateDescriptionError) Error() string {
	return fmt.Sprintf("gate %q: description: %s: %s", e.NodeID, e.Token, e.Reason)
}

// gateDescriptionSelfKeys is the set of {{ self.<ref> }} references a gate
// description may quote: the ones that render an engine fact ABOUT A GATE. It
// is empty, deliberately. The self namespace has two references, and neither
// qualifies:
//
//   - {{ self.previous }} is the node's own reply from the previous feedback
//     round — a reply, which is exactly what a description must never carry;
//   - {{ self.timeout }} is the per-attempt bound the runner kills a spawned
//     subprocess at (runner.EffectiveTimeout). A gate spawns nothing and its
//     pause has no bound, so the token would render the runner's default
//     (20m0s) — an engine-shaped number that is true of no process, printed
//     on the screen a person approves from.
//
// It is a set rather than a hard-coded refusal so a future self reference
// that IS a gate fact becomes a one-line admission here.
var gateDescriptionSelfKeys = map[string]bool{}

// GateDescriptionIssues returns one *GateDescriptionError per token in a gate
// node's `description:` that would put anything but a PATH or an ENGINE FACT
// into the text a person reads when deciding the gate (#346).
//
// A description may reference:
//
//   - {{ artifacts.<id> }} with no filter, which resolves to the artifact's
//     FILE PATH — the engine computes it from the run directory and a
//     sanitized node id, so no model chose any of it (the same reason
//     LintVerifyInlining leaves the plain token alone);
//   - {{ inputs.<name> }}, the operator's own --input value;
//   - the self references in gateDescriptionSelfKeys (none today — see there).
//
// It refuses, naming the gate and the token:
//
//   - {{ artifacts.<id> | inline }} — the producing node's reply;
//   - {{ feedback.<id> }} — a declarer's payload, its reply text (ADR 0010);
//   - {{ self.previous }} — the node's own previous-round reply (#288);
//   - any other self reference, for the reason gateDescriptionSelfKeys gives;
//   - every token LintPlaceholders would report in a prompt — a reference
//     that cannot resolve (an undeclared input, an artifact of an unknown
//     node, the gate itself or a non-ancestor, an unknown self key), a
//     malformed placeholder-like token, a stray {{ with.<name> }}. Those are
//     judged by judgeToken itself, not a copy of it, so a description and a
//     prompt can never disagree about what resolves. In a prompt they are
//     advice; here they are refusals, because a description is short,
//     decorative and read once by a person who cannot ask what it meant.
//
// Text in {{ ... }} that is not placeholder-like at all is literal, as in a
// prompt, and passes. Callers hand it an already-validated graph.
func GateDescriptionIssues(g *graph.Graph) []error {
	declared := make(map[string]bool, len(g.Inputs))
	for _, name := range g.Inputs {
		declared[name] = true
	}

	var issues []error
	for _, node := range g.Nodes {
		if node.Type != graph.TypeGate || node.Description == "" {
			continue
		}
		ancestors := ancestorsOf(g, node.ID)
		for _, token := range looseTokenPattern.FindAllString(node.Description, -1) {
			reason := gateDescriptionTokenRefused(token)
			if reason == "" {
				reason, _, _ = judgeToken(g, node.ID, declared, ancestors, token)
			}
			if reason == "" {
				continue
			}
			issues = append(issues, &GateDescriptionError{NodeID: node.ID, Token: token, Reason: reason})
		}
	}
	return issues
}

// gateDescriptionTokenRefused is the one statement of which WELL-FORMED
// tokens a gate description may not carry because of what they render — a
// reply, a payload, a non-fact — rather than because they cannot resolve. It
// returns the refusal reason, or "" for a token that renders a path or an
// engine fact, or that is not a well-formed placeholder at all (judgeToken's
// subject). GateDescriptionIssues and RenderGateDescription both judge a token
// by it, so what lint refuses and what the render refuses cannot drift apart.
func gateDescriptionTokenRefused(token string) string {
	loc := placeholderPattern.FindStringIndex(token)
	if loc == nil || loc[0] != 0 || loc[1] != len(token) {
		return ""
	}
	groups := placeholderPattern.FindStringSubmatch(token)
	kind, ref, filter := groups[1], groups[2], groups[3]
	switch {
	case kind == "artifacts" && filter == "inline":
		return fmt.Sprintf("the inline filter prints node %q's reply where a person decides this gate — a model's text, not a fact the engine recorded. Drop the filter: with none the token is the artifact's FILE PATH, which the reader can open", ref)
	case kind == "feedback":
		return fmt.Sprintf("a feedback placeholder always inlines node %q's payload, its own reply text, and has no path form — a gate description may carry only artifact paths, inputs and engine facts", ref)
	case kind == "self" && ref == SelfPrevious:
		return "{{ self.previous }} inlines a node's previous-round reply — a gate description may carry only artifact paths, inputs and engine facts"
	case kind == "self" && selfTokenRefused(ref, filter) == "" && !gateDescriptionSelfKeys[ref]:
		return fmt.Sprintf("{{ self.%s }} renders no fact about a gate — a gate spawns no process, so it has no per-attempt timeout; a gate description may quote no self reference", ref)
	}
	return ""
}
