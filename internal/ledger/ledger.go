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
	"math"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
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

// BudgetUsedPercent reports what share of its declared budget the node actually
// spent, and whether there was a budget to measure against at all. It is the
// same fact as BudgetDeltaUSD on one scale instead of many: a $0.02 delta means
// "one bad run from failing" against a $2.00 budget and "barely started" against
// a $200 one, so the delta alone cannot be scanned down a column, and the share
// can.
//
// Floored, never rounded: a node that passed must not read 100% until it truly
// landed at or over its budget (exactly at budget passes — see the scheduler's
// strictly-greater rule), and 99.6% of a budget is still 99% spent.
func (r Record) BudgetUsedPercent() (percent int, declared bool) {
	if r.BudgetUSD <= 0 {
		return 0, false
	}
	return int(math.Floor(r.CostUSD / r.BudgetUSD * 100)), true
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

// tableWidth is the rule under the table's header and above its footer, sized
// for the columns without the budget annotation. A run that declares a budget
// widens it by budgetAnnotationWidth so the rule still spans the table.
const tableWidth = 73

// sessionWidth is the SESSION column. It is the annotation's funding: the
// stub can only ever be sessionStub+1 columns wide, so the column was carrying
// slack that a budgeted run needed more. Paying for the annotation here rather
// than at the right edge is what keeps a budgeted run's rule at 80 columns
// instead of 85, and DETAIL within a column of where it has always sat — the
// graphs that actually declare budgets (adr-driven-dev, self-dev and
// dev-review-pr, each via one node or the e2e-verify fragment) are the mixed
// case, not the zero-budget one, so it is the case the width must fit.
const sessionWidth = 19

// sessionStub is how much of a session id survives truncation, one column
// short of sessionWidth to leave room for the ellipsis. 18 characters still
// spans three groups of a UUID, which is far past the point where two ids in
// one run diverge; the full id is in the per-node record and in `show`.
const sessionStub = 18

// budgetAnnotationWidth is the constant width the "(99%)" budget annotation
// occupies inside the COST(USD) cell, including its leading space. Constant so
// that a run mixing budgeted and budget-less nodes keeps one DETAIL column;
// wider only in the pathological case of a node that spent 10x its budget,
// which is a FAIL row whose detail spells the overspend out anyway.
const budgetAnnotationWidth = 7

// Render returns the end-of-run table as a string: a header, one aligned row per
// node, an optional planning-cost line (auto mode only, shown when non-zero),
// and a total-cost footer that already includes the planning cost. Rendering is
// pure so it is trivially testable; Print is the thin io.Writer wrapper the CLI
// calls.
func (l *RunLedger) Render() string {
	records := l.Records()
	// Whether to make room for the budget annotation is a per-RUN decision, not a
	// per-row one. A graph where no node declares budget_usd pays nothing for the
	// feature: no annotation, no blank field, no widened rule — one budgeted node
	// turns the field on for every row, so the DETAIL column stays in one place
	// whether or not a given node declared a budget.
	budgeted := anyBudgetDeclared(records)

	var b strings.Builder
	fmt.Fprintf(&b, "Run %s — %d node(s)\n", l.runID, len(records))
	fmt.Fprintf(&b, "%-16s %-10s %-*s %s  %s\n", "NODE", "VERDICT", sessionWidth, "SESSION", costHeader(budgeted), "DETAIL")
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", ruleWidth(budgeted)))

	for _, rec := range records {
		fmt.Fprintf(&b, "%-16s %-10s %s %s  %s\n",
			rec.NodeID,
			string(rec.Verdict),
			sessionCell(rec.SessionID),
			costCell(rec, budgeted),
			rec.Detail,
		)
	}
	fmt.Fprintf(&b, "%s\n", strings.Repeat("-", ruleWidth(budgeted)))
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

// anyBudgetDeclared reports whether a single row in the table has a budget to
// annotate, which is what decides whether the COST(USD) cell makes room for the
// annotation at all.
func anyBudgetDeclared(records []Record) bool {
	for _, rec := range records {
		if _, declared := rec.BudgetUsedPercent(); declared {
			return true
		}
	}
	return false
}

// costCell renders one row's COST(USD) cell: the spend, right-aligned exactly as
// it always was, plus — in a run that declares budgets — how much of THIS node's
// budget that spend used.
//
// The share rides inside the cost cell instead of in a BUDGET column of its own
// for two reasons. It qualifies the number the reader is already looking at, so
// it belongs next to it rather than several columns away in DETAIL, where the
// absolute headroom currently sits at the tail of a sentence that may also be
// carrying retry notes. And a column is a permanent tax on a table printed to a
// terminal of unknown width: it would cost a header plus a field that stays
// blank for every node in every graph that declares no budget, which is most of
// them. This cell costs those graphs exactly nothing.
//
// The annotation is a function of the record's two numbers, not of its verdict:
// a FAIL row reads 102% for the same reason a PASS row reads 99%. That leaves
// what the ledger reports on failure untouched — the DETAIL string still spells
// out budgeted-vs-actual and the overage — while sparing the column a
// verdict-dependent special case, and it keeps the share visible on a node that
// failed its success_check at 40% of budget, where DETAIL says nothing about
// money at all.
func costCell(rec Record, budgeted bool) string {
	cost := fmt.Sprintf("%10.4f", rec.CostUSD)
	if !budgeted {
		return cost
	}
	percent, declared := rec.BudgetUsedPercent()
	if !declared {
		return cost + strings.Repeat(" ", budgetAnnotationWidth)
	}
	return cost + fmt.Sprintf("%-*s", budgetAnnotationWidth, fmt.Sprintf(" (%d%%)", percent))
}

// costHeader is the COST(USD) header padded to whatever costCell will occupy,
// so the header rule and the DETAIL column agree with the rows below them.
func costHeader(budgeted bool) string {
	header := fmt.Sprintf("%10s", "COST(USD)")
	if budgeted {
		header += strings.Repeat(" ", budgetAnnotationWidth)
	}
	return header
}

// ruleWidth is the width of the rules bracketing the rows, widened to match the
// budget annotation when the run has one.
func ruleWidth(budgeted bool) int {
	if budgeted {
		return tableWidth + budgetAnnotationWidth
	}
	return tableWidth
}

// sessionCell renders the SESSION column: the stub, padded to sessionWidth in
// DISPLAY columns rather than bytes.
//
// A plain %-*s verb pads by BYTES, and the ellipsis a truncated id ends in is
// three of them for one column. At today's constants that verb happens to come
// out right — a truncated stub is 21 bytes, so it is never padded at all, and
// its sessionStub+1 runes land exactly on sessionWidth. That is a coincidence
// of two constants, not a property: shorten sessionStub without shortening
// sessionWidth and every real run's DETAIL slides two columns left of the
// header's, in the one column whose whole job is alignment. Counting runes
// costs nothing and does not depend on the coincidence holding.
func sessionCell(id string) string {
	stub := shortSession(id)
	if pad := sessionWidth - utf8.RuneCountInString(stub); pad > 0 {
		return stub + strings.Repeat(" ", pad)
	}
	return stub
}

// shortSession trims a session id to a readable stub for the table (full id is
// still in the per-node record for anyone who needs it). Empty stays "-".
func shortSession(id string) string {
	if id == "" {
		return "-"
	}
	if utf8.RuneCountInString(id) <= sessionWidth {
		return id
	}
	return string([]rune(id)[:sessionStub]) + "…"
}
