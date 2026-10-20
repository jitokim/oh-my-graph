package schedule

import (
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// Session-cost correction (#398). claude's total_cost_usd is cumulative for the
// whole session, so an invocation that resumed its parent's session reports
// the parent's spend as well as its own. Booked as-is, every `handoff: session`
// node would be charged for its ancestors — in its own row, the run total, the
// ledger, the goal total, and its budget_usd verdict. The scheduler therefore
// rewrites such an outcome's cost into this execution's own spend:
//
//	recorded = reported(child) − reported(the passed execution it resumed)
//
// so a chain A → B(session) → C(session) books B = rawB − rawA and
// C = rawC − rawB. Whether a reported figure IS cumulative is the runner's
// answer (NodeOutcome.CostIncludesResumedSession), because only the runner
// knows its runtime: a cold start and every codex invocation are never
// subtracted.

// reportedCost is one passed execution's cost exactly as its CLI reported it.
type reportedCost struct {
	usd     float64
	unknown bool
}

// rememberReportedCost keeps a passed execution's raw figure as the baseline a
// session child resuming it will subtract. A later pass of the same node (a
// feedback re-run) replaces it, exactly as Handoff.PersistOutput replaces the
// session id a child resumes.
func (s *Scheduler) rememberReportedCost(nodeID string, outcome runner.NodeOutcome) {
	s.reportedCostMu.Lock()
	defer s.reportedCostMu.Unlock()
	s.reportedCosts[nodeID] = reportedCost{usd: outcome.ReportedCostUSD, unknown: outcome.ReportedCostUnknown}
}

// sessionBaseline returns the raw figure of the execution a session-resuming
// spec continues — its session parent's last pass — and whether this leg
// holds one. It is read before the spawn, so the baseline is the session as it
// stood when the child resumed it. A parent that passed in an EARLIER leg has
// none here.
func (s *Scheduler) sessionBaseline(node graph.Node, spec runner.NodeInvocation) (reportedCost, bool) {
	if spec.ResumeSession == "" || len(node.DependsOn) != 1 {
		return reportedCost{}, false
	}
	s.reportedCostMu.Lock()
	defer s.reportedCostMu.Unlock()
	baseline, ok := s.reportedCosts[node.DependsOn[0]]
	return baseline, ok
}

// correctSessionCost records the raw reported figure on outcome and, when the
// runner says that figure includes a resumed session, replaces TotalCostUSD
// with this execution's own share. It never guesses: without a known baseline,
// or with a session total below it, the cost is recorded unknown — the same
// CostUnknown every surface already renders as "unknown", never as $0 — and
// CostNote says why.
func correctSessionCost(outcome *runner.NodeOutcome, baseline reportedCost, haveBaseline bool) {
	outcome.ReportedCostUSD, outcome.ReportedCostUnknown = outcome.TotalCostUSD, outcome.CostUnknown
	if !outcome.CostIncludesResumedSession || outcome.CostUnknown {
		return
	}
	switch {
	case !haveBaseline || baseline.unknown:
		outcome.TotalCostUSD, outcome.CostUnknown = 0, true
		outcome.CostNote = "session-cost correction not applied: parent's raw cost unknown"
	case outcome.TotalCostUSD < baseline.usd:
		outcome.CostNote = fmt.Sprintf("session-cost correction not applied: reported session total $%.4f is below parent's $%.4f", outcome.TotalCostUSD, baseline.usd)
		outcome.TotalCostUSD, outcome.CostUnknown = 0, true
	default:
		outcome.TotalCostUSD -= baseline.usd
	}
}
