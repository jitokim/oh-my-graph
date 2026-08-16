package graph

import (
	"fmt"
	"regexp"
	"strings"
)

// Feedback is a node's declared failure-recovery arc (ADR 0010): when the
// declaring node — typically a reviewer or verifier — fails for a judgment
// cause (verify_failed, result_mismatch, nonzero_exit) after its own retries
// are spent, the engine re-runs the depends_on path from Rerun up through the
// declarer, handing the declarer's output to the re-run as
// {{ feedback.<declarer-id> }} — at most Max times.
//
// The static graph stays a DAG: the arc is stored outside DependsOn and must
// point strictly backward along an already-validated depends_on path
// (validateFeedback), so ReadyGiven, cycle validation, the snapshot
// round-trip and every other consumer of the inline adjacency are untouched.
// Iteration is a runtime phenomenon; this field only annotates the DAG.
type Feedback struct {
	// Rerun names the node the re-run starts from. It must be a proper
	// depends_on-ancestor of the declaring node — the one check that rules
	// out self-loops, forward arcs and arcs between unrelated branches, and
	// guarantees the runtime arc closes over a path the static DAG already
	// contains.
	Rerun string `yaml:"rerun" json:"rerun,omitempty"`
	// Max is the maximum number of times the arc may fire. REQUIRED and >= 1:
	// an unbounded loop on a paid runtime must be unrepresentable, not merely
	// discouraged, so there is no default and validateFeedback refuses an
	// undeclared or non-positive bound at load. Worst case, the body runs
	// (1 + Max) times.
	Max int `yaml:"max" json:"max,omitempty"`
}

// FeedbackBody returns the loop body of id's feedback arc — every node on any
// depends_on path from the arc's target up to and including the declaring
// node — in the graph's declared node order. It returns nil when id declares
// no feedback, or names a target that does not exist or is not one of its
// ancestors (validateFeedback reports those; callers on a validated graph
// never see them).
//
// The body is what re-executes when the arc fires, and the set every
// body-shaped validation (side exits, gates, session parents, disjointness)
// is judged against. Computing it in one place keeps the validator and the
// scheduler incapable of disagreeing about what "the loop" is.
func (g *Graph) FeedbackBody(id string) []string {
	declarer, ok := g.byID[id]
	if !ok || declarer.Feedback == nil {
		return nil
	}
	target := declarer.Feedback.Rerun
	if _, ok := g.byID[target]; !ok {
		return nil
	}
	if !g.ancestorSet(id)[target] {
		return nil
	}
	return g.betweenSet(target, id)
}

// betweenSet returns every node on any depends_on path from target up to and
// including declarer, in declared node order — the body computation itself,
// with none of FeedbackBody's checks that the pair is a declared arc at all.
// Split out so a sweep may ask what the body WOULD be for a target the node
// does not currently name (LintFeedbackReach's suggestion) without a second
// definition of "the loop" appearing in the package.
func (g *Graph) betweenSet(target, declarer string) []string {
	ancestorsOfDeclarer := g.ancestorSet(declarer)
	// A node lies on a target→declarer path exactly when it is the target or
	// the declarer itself, or both an ancestor of the declarer and a
	// descendant of the target.
	body := make([]string, 0)
	for _, n := range g.Nodes {
		switch {
		case n.ID == declarer || n.ID == target:
			body = append(body, n.ID)
		case ancestorsOfDeclarer[n.ID] && g.ancestorSet(n.ID)[target]:
			body = append(body, n.ID)
		}
	}
	return body
}

// ancestorSet walks the depends_on edges up from id and returns every
// transitive parent. The visited-set walk terminates even on a cyclic graph,
// so feedback validation can run before acyclicity is settled (Issues runs
// every check regardless of earlier failures).
func (g *Graph) ancestorSet(id string) map[string]bool {
	ancestors := make(map[string]bool)
	var walk func(string)
	walk = func(current string) {
		node, ok := g.byID[current]
		if !ok {
			return
		}
		for _, parent := range node.DependsOn {
			if !ancestors[parent] {
				ancestors[parent] = true
				walk(parent)
			}
		}
	}
	walk(id)
	return ancestors
}

// validateFeedback enforces the shape a feedback arc must have before the
// engine will re-run anything on its behalf (ADR 0010, "Load-time
// validation"). Everything here is knowable from the file alone, and every
// violation would otherwise surface as spend — a loop that cannot converge,
// an artifact race, a silently re-approved gate — so it is refused at load:
//
//  1. rerun names an existing node that is a proper depends_on-ancestor of
//     the declaring node (rules out self-loops, forward arcs, cross-branch
//     arcs);
//  2. max is required and >= 1 — an unbounded loop on a paid runtime is
//     unrepresentable, not merely discouraged;
//  3. the body is side-exit free: every dependent of a body node other than
//     the declarer is itself in the body, or it would observe whichever
//     iteration happened to write last — a race with no right answer;
//  4. no gate in the body: replaying a recorded human decision on iteration
//     two would silently re-approve a round the human never saw (ADR 0003);
//  5. a body node with handoff: session names a session-parent that is
//     itself in the body, or round 2's --resume would continue a session
//     round 1 already continued;
//  6. bodies are disjoint: overlapping loops would multiply worst-case spend
//     as (1+max₁)×(1+max₂) and give the re-arming path two owners for one
//     node.
//
// validateFeedbackPlaceholders (called from Issues alongside this) is the
// placeholder half of the same contract.
func (g *Graph) validateFeedback() []error {
	var issues []error
	// bodyOwner maps each body node to the declarer whose arc claimed it
	// first, so an overlap names both arcs in its error.
	bodyOwner := make(map[string]string)
	for _, n := range g.Nodes {
		f := n.Feedback
		if f == nil {
			continue
		}
		if f.Max < 1 {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("feedback.max must be declared and >= 1 (got %d) — a bounded loop is the whole contract, so there is no default", f.Max),
			})
		}
		if strings.TrimSpace(f.Rerun) == "" {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: "feedback needs a rerun target — the depends_on-ancestor the re-run starts from",
			})
			continue
		}
		if _, ok := g.byID[f.Rerun]; !ok {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("feedback.rerun names unknown node %q", f.Rerun),
			})
			continue
		}
		if !g.ancestorSet(n.ID)[f.Rerun] {
			issues = append(issues, &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("feedback.rerun %q is not a proper depends_on-ancestor of this node — a feedback arc points strictly backward along an existing depends_on path", f.Rerun),
			})
			continue
		}

		body := g.FeedbackBody(n.ID)
		bodySet := make(map[string]bool, len(body))
		for _, id := range body {
			bodySet[id] = true
		}
		for _, id := range body {
			if owner, taken := bodyOwner[id]; taken {
				issues = append(issues, &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("feedback bodies must be disjoint: node %q already lies in the body of %q's feedback edge", id, owner),
				})
				continue
			}
			bodyOwner[id] = n.ID
		}
		for _, id := range body {
			member := g.byID[id]
			if member.Type == TypeGate {
				issues = append(issues, &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("feedback body contains gate %q — replaying its recorded decision on a later round would silently re-approve work the human never saw", id),
				})
			}
			if member.Handoff == HandoffSession && len(member.DependsOn) == 1 && !bodySet[member.DependsOn[0]] {
				issues = append(issues, &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("body node %q has handoff: session with out-of-body parent %q — a later round's --resume would continue a session an earlier round already continued; move the parent into the loop or use handoff: artifact", id, member.DependsOn[0]),
				})
			}
			if id == n.ID {
				// The declarer's own dependents legitimately sit outside the
				// loop: they consume its settled, final result.
				continue
			}
			for _, dependent := range g.DependentsOf(id) {
				if !bodySet[dependent] {
					issues = append(issues, &GraphValidationError{
						NodeID: n.ID,
						Reason: fmt.Sprintf("feedback body has a side exit: %q depends on body node %q but is outside the loop — it would observe whichever iteration wrote last; move it after %q or into the body", dependent, id, n.ID),
					})
				}
			}
		}
	}
	return issues
}

// feedbackTokenPattern matches a {{ feedback.<id> }} token, capturing the id
// and any filter text after it. The filter group exists only to be REJECTED:
// the feedback namespace always inlines the declarer's payload and has no
// path form (ADR 0010), so a filter could only be dead text or a misread
// contract. The reference character class matches the one
// internal/handoff's placeholderPattern resolves, so what load accepts and
// what the runtime substitutes can never drift apart.
//
// That coupling is why '/' is in the class (ADR 0027). A spliced body carries
// {{ feedback.qa-a/review }}; widening only handoff's pattern would let that
// token interpolate at run time while staying INVISIBLE to
// validateFeedbackPlaceholders below — and an unconfined feedback token does
// not fail, it resolves to the empty string, silently, forever. The two
// patterns move together or not at all.
var feedbackTokenPattern = regexp.MustCompile(`\{\{\s*feedback\.([A-Za-z0-9._/-]+)\s*(\|[^}]*)?\}\}`)

// validateFeedbackPlaceholders makes an unresolvable {{ feedback.<id> }} a
// LOAD error, not an advisory lint: the feedback namespace resolves to the
// empty string whenever no round has fired, so — unlike an artifact
// placeholder, which fails loudly — an out-of-place feedback token would
// resolve to "" silently, forever. A token is only legal where it can ever
// resolve: on a node inside the body of the feedback edge that <id>
// declares, with no filter.
func (g *Graph) validateFeedbackPlaceholders() []error {
	// bodies collects each declarer's body as a membership set once, so the
	// per-token judgment is a lookup.
	bodies := make(map[string]map[string]bool)
	for _, n := range g.Nodes {
		if n.Feedback == nil {
			continue
		}
		body := g.FeedbackBody(n.ID)
		set := make(map[string]bool, len(body))
		for _, id := range body {
			set[id] = true
		}
		bodies[n.ID] = set
	}

	var issues []error
	for _, n := range g.Nodes {
		fields := []struct{ name, tmpl string }{
			{"prompt", n.Prompt},
			{"cwd", n.Cwd},
		}
		if v := n.SuccessCheck.Verify; v != nil {
			fields = append(fields,
				struct{ name, tmpl string }{"success_check.verify.command", v.Command},
				struct{ name, tmpl string }{"success_check.verify.cwd", v.Cwd},
			)
		}
		for _, field := range fields {
			for _, groups := range feedbackTokenPattern.FindAllStringSubmatch(field.tmpl, -1) {
				token, ref, filter := groups[0], groups[1], groups[2]
				if filter != "" {
					issues = append(issues, &GraphValidationError{
						NodeID: n.ID,
						Reason: fmt.Sprintf("%s: %s — a feedback placeholder takes no filter: {{ feedback.<id> }} always inlines the declarer's payload", field.name, token),
					})
					continue
				}
				body, declares := bodies[ref]
				if !declares {
					issues = append(issues, &GraphValidationError{
						NodeID: n.ID,
						Reason: fmt.Sprintf("%s: %s references %q, which declares no feedback edge — the placeholder could never resolve", field.name, token, ref),
					})
					continue
				}
				if !body[n.ID] {
					issues = append(issues, &GraphValidationError{
						NodeID: n.ID,
						Reason: fmt.Sprintf("%s: %s is only legal inside the body of %q's feedback edge — outside it the placeholder would silently be empty forever", field.name, token, ref),
					})
				}
			}
		}
	}
	return issues
}
