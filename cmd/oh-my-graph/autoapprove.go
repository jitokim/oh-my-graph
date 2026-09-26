package main

import (
	"fmt"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// gateIDFlag collects repeated `run --auto-approve <gate-id>` values in the
// order they were typed (#285). The same shape as agentNameFlag, for the same
// reason: a blank id is a typo, and a typo that silently approved nothing would
// read exactly like a pre-approval that took.
//
// Each value is an EXACT node id, not a pattern. An exact id is validated
// against the loaded graph before any node spends money (checkAutoApprove), so
// a misspelt gate fails the load with the graph's real gate ids in the message;
// a glob or regex cannot be validated that way — it has no "does not exist"
// answer — and would silently never match, leaving the run to pause at the very
// gate the operator believed they had answered.
type gateIDFlag []string

func (f *gateIDFlag) String() string { return strings.Join(*f, ",") }

func (f *gateIDFlag) Set(id string) error {
	if strings.TrimSpace(id) == "" {
		return fmt.Errorf("invalid --auto-approve %q (want a gate node id, as written in the graph)", id)
	}
	*f = append(*f, id)
	return nil
}

// checkAutoApprove validates every `--auto-approve` id against the loaded
// graph: each must name a node that exists and is `type: gate`. It runs after
// graph validation and before anything is written or spawned — the snapshot's
// initial write, the run directory, every node — so a typo is a load error
// (exit 1) with no run to clean up, on `run` and `run --dry-run` alike.
//
// The message names the offending id, says whether it is missing or the wrong
// type, and lists the graph's actual gate ids sorted, so the fix is on the
// same line as the refusal. A duplicated id is accepted once: the decision it
// registers is the same either way. The first offending id, in argv order, is
// reported.
func checkAutoApprove(g *graph.Graph, ids []string) error {
	if len(ids) == 0 {
		return nil
	}
	gates := gateNodeIDs(g)
	for _, id := range ids {
		node, ok := g.NodeByID(id)
		switch {
		case !ok:
			return fmt.Errorf("run: --auto-approve %q: no such node (%s)", id, describeGates(gates))
		case node.Type != graph.TypeGate:
			return fmt.Errorf("run: --auto-approve %q: node is not a gate (type %s) (%s)", id, node.Type, describeGates(gates))
		}
	}
	return nil
}

// gateNodeIDs returns the ids of every `type: gate` node in g, sorted.
func gateNodeIDs(g *graph.Graph) []string {
	var ids []string
	for _, node := range g.Nodes {
		if node.Type == graph.TypeGate {
			ids = append(ids, node.ID)
		}
	}
	sort.Strings(ids)
	return ids
}

// describeGates is the parenthetical checkAutoApprove's refusals end with.
func describeGates(gates []string) string {
	if len(gates) == 0 {
		return "this graph has no gate nodes"
	}
	return "gate nodes in this graph: " + strings.Join(gates, ", ")
}

// autoApproveDecisions turns the validated `--auto-approve` list into the
// decision map a gate.RecordedController answers from: DecisionApprove for
// every named gate, nothing for any other, so an unnamed gate pauses exactly
// as it does with no flag at all. nil for an empty list.
func autoApproveDecisions(ids []string) map[string]gate.Decision {
	if len(ids) == 0 {
		return nil
	}
	decisions := make(map[string]gate.Decision, len(ids))
	for _, id := range ids {
		decisions[id] = gate.DecisionApprove
	}
	return decisions
}

// withLaunchApprovals is the resumed leg's half of #285: decisions is the
// snapshot's recorded gate decisions (plus this resume's own --approve or
// --reject), and autoApprove is the list the run was launched with. Every
// named gate with no recorded decision yet gets DecisionApprove, so a
// pre-approval outlives the leg it was typed in — a run that paused at an
// UNNAMED gate before ever reaching a named one must not stop at the named one
// on its next leg, or the flag's promise would hold for exactly one leg. A
// recorded decision always wins; there is nothing for it to lose to, since a
// named gate that was reached is recorded as approve, but the rule is stated
// so a snapshot edited by hand cannot make this function widen anything. The
// snapshot's Gate.Decisions is NOT seeded: the approval is recorded, as every
// approval is, when the scheduler actually evaluates the gate. nil in, nil out
// when there is nothing to add.
func withLaunchApprovals(decisions map[string]gate.Decision, autoApprove []string) map[string]gate.Decision {
	if len(autoApprove) == 0 {
		return decisions
	}
	merged := make(map[string]gate.Decision, len(decisions)+len(autoApprove))
	for id, d := range decisions {
		merged[id] = d
	}
	for _, id := range autoApprove {
		if _, recorded := merged[id]; !recorded {
			merged[id] = gate.DecisionApprove
		}
	}
	return merged
}

// gateControllerFor picks the GateController a fresh run injects. With no
// `--auto-approve` it is the PauseController it has always been — the branch
// is kept, rather than always passing a RecordedController over a nil map
// (which pauses every gate too), so the flag-less `run` and every `auto` keep
// the controller they had before #285 byte for byte, and no decision map is
// built on a path that has nothing to put in it. With the flag it is a
// RecordedController over the named gates' approvals: the same controller
// `resume` injects, fed from argv instead of a snapshot. The scheduler asks
// the same question either way and never learns which one answered.
func gateControllerFor(autoApprove []string) gate.GateController {
	if len(autoApprove) == 0 {
		return gate.NewPauseController()
	}
	return gate.NewRecordedController(autoApproveDecisions(autoApprove))
}
