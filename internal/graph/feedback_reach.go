package graph

import (
	"fmt"
	"strings"
)

// FeedbackAdvisory is an advisory finding about a feedback arc that cannot
// reach one of the producers its declarer fans in from: the arc is valid
// (ADR 0010's seven load rules all hold) but a defect the declarer finds in
// that producer's output has no path back to the node that wrote it. Advice
// only, with the same standing as FragmentAdvisory and handoff's lint
// warnings — it never affects whether a graph is valid or what any command
// exits with.
//
// The three ids are separate fields rather than only prose because they are
// the three the reader of issue #118 had to reconstruct from graph.json by
// hand: who judges (Declarer), what the arc re-runs (Rerun), and whose
// artifact is therefore unrepairable (Producer). Suggestion is the fourth
// datum for the same reason, and one more: it is the only part of the finding
// a SECOND consumer has to branch on. The coordinator refuses a planned arc
// exactly when a covering target exists (validatePlannedFeedbackReach), so
// "is there a fix?" has to be answerable without parsing Detail's prose —
// otherwise the rule would be computed twice and drift.
type FeedbackAdvisory struct {
	Declarer string
	Rerun    string
	Producer string
	// Suggestion is the rerun target whose body would cover every producer
	// this declarer depends on AND still validate, or "" when no such target
	// exists and the fix is structural. Detail renders it; nothing recomputes
	// it.
	Suggestion string
	Detail     string
}

func (a FeedbackAdvisory) String() string {
	return fmt.Sprintf("node %q: feedback: %s", a.Declarer, a.Detail)
}

// LintFeedbackReach reports, for every feedback declarer that fans in from
// more than one producer, each producer that lies OUTSIDE the arc's loop body
// — the shape of issue #118, where a reviewer judged two producers' files,
// `rerun` named one of them, and the five defects it found were all in the
// other's. The loop then re-ran a healthy branch until its rounds were spent
// and halted, having never touched the defective file, at roughly $14 of the
// run's $42.
//
// What this sweep can see is exactly `depends_on` and the between-set. What it
// CANNOT see is which artifacts a prompt will actually judge: the #118
// reviewer named its two files by literal path (`stg-canary/QA-PLAN.md`), not
// through `{{ artifacts.qa-plan }}`, so no placeholder scan would have caught
// it either. "This producer is outside the loop" is therefore a statement
// about topology, not about intent — which is precisely why it is an
// advisory and not an eighth load rule:
//
//   - A producer may be outside the body ON PURPOSE. ADR 0010's own rule 3
//     blesses the shape — "Parents feeding into the body from outside are
//     fine — they ran once, their artifacts are stable". The two skips below
//     keep the sweep off the shapes rules 3 and 4 bless, but neither skip is
//     a proof of intent, so a legitimate graph can still be warned about: a
//     sibling CORPUS root, on no path to the target, is topologically
//     identical to #118's sibling WORK. Refusing that would break working
//     hand-written graphs to catch a planner's mistake.
//   - Refusing would also contradict rule 4. A gate can never be in a body,
//     so a fan-in declarer with a gate parent could satisfy neither rule; the
//     sweep skips gate parents for the same reason rather than emitting
//     advice no author can act on.
//
// The two skips are a gate parent (rule 4, above) and a parent that is an
// ANCESTOR OF THE RERUN TARGET. The second is rule 3's carve-out stated in
// topology: such a parent sits upstream of the whole loop, its output already
// flows into the body, and the loop re-runs its consumers — so
// `spec → impl → review` with `rerun: impl`, the most idiomatic "judge the
// implementation against the acceptance criteria" graph there is, stays quiet.
// Without that skip the sweep would warn on it and advise `rerun: spec`, which
// re-runs the criteria every round and re-judges the implementation against
// criteria that just moved underneath it. #118's sibling producer lies on no
// path to the target, and still warns.
//
// A single-parent declarer is never reported and needs no special case: its
// sole parent lies on every path to it, so the between-set contains it
// whatever `rerun` names.
//
// Each advisory names the declarer, the rerun target and the unreachable
// producer, and — when one exists — a concrete replacement target whose body
// would cover every producer AND still pass validateFeedback. That suggestion
// is what keeps this from being an advisory everyone learns to scroll past:
// the finding arrives with the edit that fixes it.
//
// Advisory is this sweep's standing for a graph a HUMAN wrote. Auto mode holds
// planner output to more (coordinator.validatePlannedFeedbackReach): it reads
// these same advisories and refuses the plan for the ones carrying a
// Suggestion, because unreviewed output has no author to weigh a warning and a
// refused plan buys one corrected re-plan. That is a decision about who wrote
// the graph, not a second opinion about the topology — the topology is
// computed here, once.
func (g *Graph) LintFeedbackReach() []FeedbackAdvisory {
	var advisories []FeedbackAdvisory
	for _, n := range g.Nodes {
		if n.Feedback == nil {
			continue
		}
		body := g.FeedbackBody(n.ID)
		if len(body) == 0 {
			// Not a validated arc (FeedbackBody reports that as nil, and
			// validateFeedback has already refused it); there is no loop to
			// judge coverage against.
			continue
		}
		inBody := make(map[string]bool, len(body))
		for _, id := range body {
			inBody[id] = true
		}

		upstreamOfLoop := g.ancestorSet(n.Feedback.Rerun)

		var unreachable []string
		for _, parent := range n.DependsOn {
			if inBody[parent] || upstreamOfLoop[parent] {
				continue
			}
			node, ok := g.byID[parent]
			if !ok || node.Type == TypeGate {
				continue
			}
			unreachable = append(unreachable, parent)
		}
		if len(unreachable) == 0 {
			continue
		}

		suggestion := g.coveringFeedbackTarget(n.ID, unreachable)
		for _, producer := range unreachable {
			advisories = append(advisories, FeedbackAdvisory{
				Declarer:   n.ID,
				Rerun:      n.Feedback.Rerun,
				Producer:   producer,
				Suggestion: suggestion,
				Detail: fmt.Sprintf(
					"arc reruns %q, whose loop body {%s} excludes producer %q, and %[3]q is not upstream of the loop either — so if this node judges %[3]q's output, a defect it finds there cannot be repaired: every round re-judges an unchanged artifact until the rounds are spent. This sweep reads `depends_on`, not which artifacts the prompt judges, so ignore it if %[3]q is only stable context. %s",
					n.Feedback.Rerun, strings.Join(body, ", "), producer, advice(suggestion)),
			})
		}
	}
	return advisories
}

// advice renders the fix half of the message: the covering target when one
// exists, and otherwise the honest statement that no single target does — the
// author has to move the producer onto the loop's path or split the review,
// and inventing a target that would fail validation would be worse than
// saying nothing.
func advice(target string) string {
	if target == "" {
		return "No single rerun target covers every producer and still validates, so the fix is structural: put the producers on one path below a shared ancestor, or give each its own reviewer."
	}
	return fmt.Sprintf("Consider `rerun: %s` — its body covers every producer this node depends on and still validates.", target)
}

// coveringFeedbackTarget returns the rerun target the declarer SHOULD name:
// the one whose body contains every unreachable producer and the current
// target, and whose graph still passes BOTH feedback rule sets (a wider body
// can swallow a gate, cross another arc's body, or grow a side exit — a
// suggestion that does not load is worse than none). Both are checked rather
// than only validateFeedback because "and still validates" is what the printed
// advice claims: widening a body can only make more placeholder sites legal
// today, but the claim should not be resting on that reasoning holding for
// every future rule. Ties break on the
// smallest body, then on declared node order, so the advice is deterministic
// and re-runs no more of the graph than it must. Returns "" when no candidate
// qualifies.
func (g *Graph) coveringFeedbackTarget(declarer string, unreachable []string) string {
	current := g.byID[declarer].Feedback.Rerun
	var best string
	var bestSize int
	for candidate := range g.ancestorSet(declarer) {
		body := g.betweenSet(candidate, declarer)
		covered := make(map[string]bool, len(body))
		for _, id := range body {
			covered[id] = true
		}
		if !covered[current] {
			continue
		}
		missed := false
		for _, producer := range unreachable {
			if !covered[producer] {
				missed = true
				break
			}
		}
		if missed {
			continue
		}
		aimed := g.withRerun(declarer, candidate)
		if len(aimed.validateFeedback()) > 0 || len(aimed.validateFeedbackPlaceholders()) > 0 {
			continue
		}
		if best == "" || len(body) < bestSize || (len(body) == bestSize && g.declarationIndex(candidate) < g.declarationIndex(best)) {
			best, bestSize = candidate, len(body)
		}
	}
	return best
}

// withRerun returns a copy of the graph with declarer's feedback arc aimed at
// target, so a candidate suggestion can be validated before it is printed.
// The copy is shallow except for the one Feedback it rewrites, which is
// replaced with a fresh pointer — the caller's graph is never touched.
func (g *Graph) withRerun(declarer, target string) *Graph {
	clone := *g
	clone.Nodes = make([]Node, len(g.Nodes))
	copy(clone.Nodes, g.Nodes)
	clone.byID = make(map[string]Node, len(clone.Nodes))
	for i, n := range clone.Nodes {
		if n.ID == declarer && n.Feedback != nil {
			aimed := *n.Feedback
			aimed.Rerun = target
			clone.Nodes[i].Feedback = &aimed
		}
		clone.byID[clone.Nodes[i].ID] = clone.Nodes[i]
	}
	return &clone
}

// declarationIndex is a node's position in the file, the tie-break that keeps
// two equally small candidate bodies from suggesting different targets on
// different runs (ancestorSet is a map, so its iteration order is random).
func (g *Graph) declarationIndex(id string) int {
	for i, n := range g.Nodes {
		if n.ID == id {
			return i
		}
	}
	return len(g.Nodes)
}
