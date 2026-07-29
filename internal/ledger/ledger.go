// Package ledger records what happened in a run and renders the end-of-run
// summary: one row per node (session id, cost, verdict, duration) plus the total
// cost across the graph. It is the run's accounting book — write-only from the
// Scheduler's side, rendered once at the end by the CLI.
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
	// Detail is a short human note — the failing predicate, the retry count, the
	// budget delta, or empty on a clean pass with no budget declared.
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

// Records returns a stable, node-id-sorted copy of the recorded rows so the
// output is deterministic regardless of the order goroutines finished in.
func (l *RunLedger) Records() []Record {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]Record, len(l.records))
	copy(out, l.records)
	sort.Slice(out, func(i, j int) bool { return out[i].NodeID < out[j].NodeID })
	return out
}

// TotalCost sums the reported cost across every recorded node.
func (l *RunLedger) TotalCost() float64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	var total float64
	for _, rec := range l.records {
		total += rec.CostUSD
	}
	return total
}

// Render returns the end-of-run table as a string: a header, one aligned row per
// node, and a total-cost footer. Rendering is pure so it is trivially testable;
// Print is the thin io.Writer wrapper the CLI calls.
func (l *RunLedger) Render() string {
	records := l.Records()

	var b strings.Builder
	fmt.Fprintf(&b, "Run %s — %d node(s)\n", l.runID, len(records))
	fmt.Fprintf(&b, "%-16s %-10s %-24s %10s  %s\n", "NODE", "VERDICT", "SESSION", "COST(USD)", "DETAIL")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 78))

	var total float64
	for _, rec := range records {
		total += rec.CostUSD
		fmt.Fprintf(&b, "%-16s %-10s %-24s %10.4f  %s\n",
			rec.NodeID,
			string(rec.Verdict),
			shortSession(rec.SessionID),
			rec.CostUSD,
			rec.Detail,
		)
	}
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", 78))
	fmt.Fprintf(&b, "TOTAL COST: $%.4f\n", total)
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
