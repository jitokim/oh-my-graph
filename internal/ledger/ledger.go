// Package ledger records what happened in a run and renders the end-of-run
// summary: one row per node EXECUTION (session id, cost, verdict, duration),
// the coordinator's one-time planning cost (auto mode only — zero and hidden
// for a hand-written `run`), and the total cost across the graph including
// that planning call. Most nodes execute once and get one row; a feedback
// round (ADR 0010) re-executes its loop body, and every round's execution of
// every body node appends its own row, with "feedback round k/N" in the
// detail — the ledger's one job is the cost story, and the entire risk of a
// loop on a paid runtime IS the multiplier, so round 2's cost must sit next
// to round 1's in the same table as everything else. It is the run's
// accounting book — write-only from the Scheduler's side (per execution)
// plus a single planning-cost entry from the CLI, rendered once at the end
// by the CLI.
package ledger

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"
)

// Verdict is a node's terminal judgement in the ledger.
type Verdict string

const (
	VerdictPass Verdict = "PASS"
	VerdictFail Verdict = "FAIL"
)

// Record is one node's line in the ledger.
type Record struct {
	NodeID    string
	SessionID string
	CostUSD   float64
	// BudgetUSD is the budget_usd the node declared, or 0 when it declared none.
	// Recorded next to CostUSD so the budget-vs-actual delta is derivable from a
	// Record alone, without consulting the graph it came from.
	BudgetUSD float64
	Verdict   Verdict
	Duration  time.Duration
	// Detail is a short human note — the failure cause (predicate plus why),
	// the retry count, the budget delta, or empty on a clean pass with no
	// budget declared. Always one line, and capped by the scheduler at one
	// shared bound (240 runes) so the table stays readable.
	Detail string
}

// BudgetDeltaUSD reports how far the node's actual cost landed from its declared
// budget (positive = over, negative = under) and whether there was a budget to
// compare against at all. A node with no budget_usd has no delta to report.
func (r Record) BudgetDeltaUSD() (delta float64, declared bool) {
	if r.BudgetUSD <= 0 {
		return 0, false
	}
	return r.CostUSD - r.BudgetUSD, true
}

// RunLedger accumulates records across concurrently-running nodes and renders
// the summary. Safe for concurrent Record calls.
type RunLedger struct {
	runID string

	mu      sync.Mutex
	records []Record
	// planningCostUSD is the coordinator's one planning-call cost, folded into
	// the run's total (auto mode only). Zero for a hand-written `run`, which has
	// no planning step — so its summary shows no planning line and its total is
	// unchanged. Guarded by mu, matching records.
	planningCostUSD float64
}

// New builds an empty ledger tagged with the run id.
func New(runID string) *RunLedger {
	return &RunLedger{runID: runID}
}

// Record appends one node's result. Called once per node from its goroutine.
func (l *RunLedger) Record(rec Record) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.records = append(l.records, rec)
}

// RecordPlanningCost adds the coordinator's one planning-call cost to the run's
// total. Auto mode calls it once with plan.CostUSD so the end-of-run total is
// honest about the planning step; `run` (hand-written YAML, no planning) passes
// 0, which is a no-op that renders no planning line. It is additive and lock-
// guarded, so it is safe to call concurrently with Record, though in practice
// the CLI calls it once before the run starts.
func (l *RunLedger) RecordPlanningCost(costUSD float64) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.planningCostUSD += costUSD
}

// planningCost returns the recorded planning cost under the lock.
func (l *RunLedger) planningCost() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.planningCostUSD
}

// Records returns a stable, node-id-sorted copy of the recorded rows so the
// output is deterministic regardless of the order goroutines finished in.
// The sort is stable: a node with several execution rows (feedback rounds)
// keeps them in the order they were recorded — round 1 above round 2.
func (l *RunLedger) Records() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Record, len(l.records))
	copy(out, l.records)
	sort.SliceStable(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// TotalCost sums the reported cost across every recorded node plus the
// coordinator's planning cost (zero for a hand-written `run`), so it is the run's
// true total spend.
func (l *RunLedger) TotalCost() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	total := l.planningCostUSD
	for _, rec := range l.records {
		total += rec.CostUSD
	}
	return total
}

// Render returns the end-of-run table as a string: a header, one aligned row per
// node, an optional planning-cost line (auto mode only, shown when non-zero),
// and a total-cost footer that already includes the planning cost. Rendering is
// pure so it is trivially testable; Print is the thin io.Writer wrapper the CLI
// calls.
func (l *RunLedger) Render() string {
	records := l.Records()

	var b strings.Builder
	fmt.Fprintf(&b, "Run %s — %d node(s)\n", l.runID, len(records))
	fmt.Fprintf(&b, "%-16s %-10s %-24s %10s  %s\n", "NODE", "VERDICT", "SESSION", "COST(USD)", "DETAIL")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 78))

	for _, rec := range records {
		fmt.Fprintf(&b, "%-16s %-10s %-24s %10.4f  %s\n",
			rec.NodeID,
			string(rec.Verdict),
			shortSession(rec.SessionID),
			rec.CostUSD,
			rec.Detail,
		)
	}
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 78))
	if planning := l.planningCost(); planning != 0 {
		fmt.Fprintf(&b, "PLANNING COST: $%.4f\n", planning)
	}
	fmt.Fprintf(&b, "TOTAL COST: $%.4f\n", l.TotalCost())
	return b.String()
}

// Print writes the rendered table to w.
func (l *RunLedger) Print(w io.Writer) {
	fmt.Fprint(w, l.Render())
}

// shortSession trims a session id to a readable stub for the table (full id is
// still in the per-node record for anyone who needs it). Empty stays "-".
func shortSession(id string) string {
	if id == "" {
		return "-"
	}
	if len(id) <= 20 {
		return id
	}
	return id[:20] + "…"
}
