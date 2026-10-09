// The exit-code guard for a planned verdict (#371): trusted code that, after
// validation, sets `exit_zero` on every planned node whose success_check
// declares a `result_matches` and no `exit_zero`.
//
// Exit-zero is the engine's default only while a node declares NO check at all
// (graph.SuccessCheck.IsZero). The moment a node names a result predicate, its
// exit code goes unchecked unless the check also says `exit_zero` — so a
// planner that adds a verdict to a node silently deletes the guard that node
// had for free. handoff.LintVerdicts warns about exactly that, and it is right
// to; the planner prompt now pairs the two every time it hands out a
// `result_matches`. This file is what keeps the pairing from resting on the
// prompt alone: a prompt is not a mechanism, and a planner that writes the
// verdict without the guard still gets a graph that checks the exit code.
//
// The direction is the whole safety argument, as it is for attachVerifyCommand:
// this step only ever makes a check STRICTER. It adds `exit_zero` and nothing
// else — `result_matches` is never removed or rewritten, a node with no
// success_check is never touched (exit-zero is already its default), and a node
// that already sets `exit_zero` is left exactly as it is.
package coordinator

import (
	"encoding/json"
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// requireExitZero is the post-validation step itself. It runs on the
// planned-graph path only — a hand-written run graph is the user's own
// reviewed artifact, and its `result_matches` without `exit_zero` stays a lint
// advisory rather than something trusted code rewrites. Every Plan, the
// bounded re-plan and the goal loop's continuation all reach it, because all
// three go through attemptPlan.
//
// A plan with nothing to add keeps its Spec byte for byte: nothing is
// re-encoded, and ExitZeroAdded stays nil.
func (c *Coordinator) requireExitZero(plan *Plan) error {
	g, spec, added, err := pairExitZero(plan.Graph)
	if err != nil {
		return err
	}
	if len(added) == 0 {
		return nil
	}
	plan.Graph = g
	plan.Spec = spec
	plan.ExitZeroAdded = added
	return nil
}

// pairExitZero sets ExitZero on every node of g whose SuccessCheck has a
// non-empty ResultMatches and no ExitZero, and returns the rebuilt graph, its
// JSON spec and the ids it changed, in the graph's declared order. When no node
// needs it, it returns g itself, a nil spec and no ids.
//
// graph.SuccessCheck.ExitZero is a plain bool, so an explicit
// `"exit_zero": false` decodes to the same value as an absent key and cannot be
// told apart from it here. Both are normalised. That is safe in this direction
// only: the field is planner-authored, so honouring an explicit false would let
// an unreviewed plan opt a node OUT of the exit-code guard, which is the
// weakening this step exists to stop.
//
// Like attachVerification, the rebuilt graph goes back through graph.Parse
// rather than being handed over mutated in place, so g itself — which a caller
// may still hold — is never changed, and the graph that runs is one Validate
// accepted.
func pairExitZero(g *graph.Graph) (*graph.Graph, []byte, []string, error) {
	var added []string
	out := *g
	out.Nodes = append([]graph.Node(nil), g.Nodes...)
	for i := range out.Nodes {
		check := &out.Nodes[i].SuccessCheck
		if check.ResultMatches == "" || check.ExitZero {
			continue
		}
		check.ExitZero = true
		added = append(added, out.Nodes[i].ID)
	}
	if len(added) == 0 {
		return g, nil, nil, nil
	}

	spec, err := json.Marshal(&out)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-encode exit_zero-paired graph: %w", err)
	}
	reparsed, err := graph.Parse(spec)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("re-parse exit_zero-paired graph: %w", err)
	}
	return reparsed, spec, added, nil
}
