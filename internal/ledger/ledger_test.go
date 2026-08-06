package ledger

import (
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestTotalCost_SumsRecords(t *testing.T) {
	l := New("run-1")
	l.Record(Record{NodeID: "a", CostUSD: 0.10, Verdict: VerdictPass})
	l.Record(Record{NodeID: "b", CostUSD: 0.25, Verdict: VerdictPass})
	l.Record(Record{NodeID: "c", CostUSD: 0.00, Verdict: VerdictFail})

	if got := l.TotalCost(); got < 0.349 || got > 0.351 {
		t.Fatalf("total cost = %v, want ~0.35", got)
	}
}

func TestRecords_SortedByNodeID(t *testing.T) {
	l := New("run-1")
	l.Record(Record{NodeID: "c", Verdict: VerdictPass})
	l.Record(Record{NodeID: "a", Verdict: VerdictPass})
	l.Record(Record{NodeID: "b", Verdict: VerdictFail})

	got := l.Records()
	want := []string{"a", "b", "c"}
	for i, rec := range got {
		if rec.NodeID != want[i] {
			t.Fatalf("records not sorted: %v", got)
		}
	}
}

// TestRecords_DuplicateNodeIDKeepsInsertionOrder pins the stability Records
// promises: a node with several execution rows (feedback rounds) keeps them
// in the order they were recorded — round 1 above round 2 — after the
// node-id sort interleaves other nodes around them.
func TestRecords_DuplicateNodeIDKeepsInsertionOrder(t *testing.T) {
	l := New("run-1")
	l.Record(Record{NodeID: "review", Detail: "feedback round 1/2", Verdict: VerdictFail})
	l.Record(Record{NodeID: "impl", Verdict: VerdictPass})
	l.Record(Record{NodeID: "review", Detail: "feedback round 2/2", Verdict: VerdictPass})

	got := l.Records()
	if len(got) != 3 || got[0].NodeID != "impl" || got[1].NodeID != "review" || got[2].NodeID != "review" {
		t.Fatalf("records not sorted by node id: %+v", got)
	}
	if got[1].Detail != "feedback round 1/2" || got[2].Detail != "feedback round 2/2" {
		t.Fatalf("duplicate-id rows lost their insertion order: %+v", got)
	}
}

func TestRender_IncludesRowsAndTotal(t *testing.T) {
	l := New("run-42")
	l.Record(Record{NodeID: "write", SessionID: "sess-write", CostUSD: 0.02, Verdict: VerdictPass, Duration: time.Second})
	l.Record(Record{NodeID: "critique", SessionID: "sess-critique", CostUSD: 0.03, Verdict: VerdictPass})

	out := l.Render()
	for _, want := range []string{"run-42", "write", "critique", "PASS", "TOTAL COST", "0.0500"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

func TestBudgetDeltaUSD(t *testing.T) {
	cases := []struct {
		name         string
		rec          Record
		wantDelta    float64
		wantDeclared bool
	}{
		{
			name:         "over budget reports a positive delta",
			rec:          Record{BudgetUSD: 0.10, CostUSD: 0.25},
			wantDelta:    0.15,
			wantDeclared: true,
		},
		{
			name:         "under budget reports a negative delta",
			rec:          Record{BudgetUSD: 0.50, CostUSD: 0.20},
			wantDelta:    -0.30,
			wantDeclared: true,
		},
		{
			name:         "exactly at budget reports zero delta",
			rec:          Record{BudgetUSD: 0.50, CostUSD: 0.50},
			wantDelta:    0,
			wantDeclared: true,
		},
		{
			name:         "no budget declared has no delta",
			rec:          Record{CostUSD: 99},
			wantDeclared: false,
		},
		{
			name:         "a negative budget counts as undeclared",
			rec:          Record{BudgetUSD: -1, CostUSD: 99},
			wantDeclared: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			delta, declared := tc.rec.BudgetDeltaUSD()
			if declared != tc.wantDeclared {
				t.Fatalf("declared = %v, want %v", declared, tc.wantDeclared)
			}
			if !declared {
				return
			}
			if diff := delta - tc.wantDelta; diff > 0.0001 || diff < -0.0001 {
				t.Fatalf("delta = %v, want ~%v", delta, tc.wantDelta)
			}
		})
	}
}

func TestBudgetUsedPercent(t *testing.T) {
	cases := []struct {
		name         string
		rec          Record
		wantPercent  int
		wantDeclared bool
	}{
		{
			name:         "a node one bad run from failing reads 99%",
			rec:          Record{BudgetUSD: 2.00, CostUSD: 1.98},
			wantPercent:  99,
			wantDeclared: true,
		},
		{
			name:         "the same spend against a roomy budget reads far lower",
			rec:          Record{BudgetUSD: 2.00, CostUSD: 0.12},
			wantPercent:  6,
			wantDeclared: true,
		},
		{
			name:         "floored, so a hair under budget never reads 100%",
			rec:          Record{BudgetUSD: 2.00, CostUSD: 1.9999},
			wantPercent:  99,
			wantDeclared: true,
		},
		{
			name:         "exactly at budget reads 100% — that spend passes",
			rec:          Record{BudgetUSD: 2.00, CostUSD: 2.00},
			wantPercent:  100,
			wantDeclared: true,
		},
		{
			name:         "over budget reads past 100%",
			rec:          Record{BudgetUSD: 0.10, CostUSD: 0.25},
			wantPercent:  250,
			wantDeclared: true,
		},
		{
			name:         "a negligible spend against a big budget reads 0%",
			rec:          Record{BudgetUSD: 5.00, CostUSD: 0.0001},
			wantPercent:  0,
			wantDeclared: true,
		},
		{
			name:         "no budget declared has no share to report",
			rec:          Record{CostUSD: 99},
			wantDeclared: false,
		},
		{
			name:         "a negative budget counts as undeclared",
			rec:          Record{BudgetUSD: -1, CostUSD: 99},
			wantDeclared: false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			percent, declared := tc.rec.BudgetUsedPercent()
			if declared != tc.wantDeclared {
				t.Fatalf("declared = %v, want %v", declared, tc.wantDeclared)
			}
			if !declared {
				return
			}
			if percent != tc.wantPercent {
				t.Fatalf("percent = %d, want %d", percent, tc.wantPercent)
			}
		})
	}
}

// TestRender_HeadroomDistinguishesTightFromRoomy is issue #115 stated as an
// assertion: two nodes under the same budget, one 99% spent and one with 15x
// headroom, must not look alike in the table. Both rows report their own share
// of budget, and they differ.
func TestRender_HeadroomDistinguishesTightFromRoomy(t *testing.T) {
	l := New("run-headroom")
	l.Record(Record{NodeID: "tight", CostUSD: 1.98, BudgetUSD: 2.00, Verdict: VerdictPass})
	l.Record(Record{NodeID: "roomy", CostUSD: 0.12, BudgetUSD: 2.00, Verdict: VerdictPass})

	tight := rowFor(t, l.Render(), "tight")
	roomy := rowFor(t, l.Render(), "roomy")

	if !strings.Contains(tight, "(99%)") {
		t.Errorf("the 99%%-spent row must say so:\n%s", tight)
	}
	if !strings.Contains(roomy, "(6%)") {
		t.Errorf("the row with headroom must say so:\n%s", roomy)
	}
	if strings.TrimPrefix(tight, "tight") == strings.TrimPrefix(roomy, "roomy") {
		t.Errorf("the two rows are still indistinguishable:\n%s\n%s", tight, roomy)
	}
}

// TestRender_NoBudgetDeclaredCostsTheTableNothing pins the constraint that pays
// for this design: budget-less nodes are the common case, so a run without a
// single declared budget must render exactly the table it always did — no
// annotation, no widened rule, nothing extra to read past.
func TestRender_NoBudgetDeclaredCostsTheTableNothing(t *testing.T) {
	l := New("run-plain")
	l.Record(Record{NodeID: "write", CostUSD: 0.02, Verdict: VerdictPass})
	l.Record(Record{NodeID: "critique", CostUSD: 0.03, Verdict: VerdictPass})

	out := l.Render()
	if strings.Contains(out, "%") {
		t.Errorf("a budget-less run must carry no budget annotation:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "--") && len(line) != tableWidth {
			t.Errorf("rule widened to %d without a budget to annotate:\n%s", len(line), out)
		}
	}
}

// TestRender_BudgetlessRowKeepsDetailAligned covers the mixed run: one node
// declares a budget and another does not, and the annotation must not knock the
// budget-less node's DETAIL out of the column.
func TestRender_BudgetlessRowKeepsDetailAligned(t *testing.T) {
	l := New("run-mixed")
	l.Record(Record{NodeID: "capped", CostUSD: 0.50, BudgetUSD: 1.00, Verdict: VerdictPass, Detail: "capped-detail"})
	l.Record(Record{NodeID: "uncapped", CostUSD: 0.50, Verdict: VerdictPass, Detail: "uncapped-detail"})

	out := l.Render()
	capped := strings.Index(rowFor(t, out, "capped"), "capped-detail")
	uncapped := strings.Index(rowFor(t, out, "uncapped"), "uncapped-detail")
	header := strings.Index(rowFor(t, out, "NODE"), "DETAIL")

	if capped != uncapped || capped != header {
		t.Errorf("DETAIL starts at columns %d (budgeted), %d (budget-less), %d (header):\n%s",
			capped, uncapped, header, out)
	}
}

func TestRender_ShowsBudgetDetail(t *testing.T) {
	l := New("run-budget")
	detail := `node "spendy" exceeded budget_usd: $0.2500 actual vs $0.1000 budgeted (over by $0.1500)`
	l.Record(Record{
		NodeID:    "spendy",
		CostUSD:   0.25,
		BudgetUSD: 0.10,
		Verdict:   VerdictFail,
		Detail:    detail,
	})

	out := l.Render()
	for _, want := range []string{"spendy", "FAIL", "exceeded budget_usd", "over by $0.1500"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
	// The failing path is the one the issue says already works: the annotation
	// added for passing nodes must leave the failure's own account of the
	// overspend verbatim, not summarize or replace it.
	if !strings.HasSuffix(strings.TrimRight(rowFor(t, out, "spendy"), " "), detail) {
		t.Errorf("the failure detail must reach the table untouched:\n%s", out)
	}
}

// TestRender_BudgetedRuleFitsAnEightyColumnTerminal pins the width the budget
// annotation is allowed to cost. A mixed run — one budgeted node among several
// that are not — is the shipped reality, not the exception: adr-driven-dev,
// self-dev and dev-review-pr all declare exactly one budget, so the widened
// table IS the table users see. If its rule wraps on an 80-column terminal,
// every run prints a stray broken line, twice.
func TestRender_BudgetedRuleFitsAnEightyColumnTerminal(t *testing.T) {
	l := New("run-wide")
	l.Record(Record{NodeID: "capped", SessionID: "0198a3f7-0a3b-7bd2-9c1e-4f2b6d8e1a55", CostUSD: 0.50, BudgetUSD: 1.00, Verdict: VerdictPass})
	l.Record(Record{NodeID: "uncapped", SessionID: "0198a3f7-0a3b-7bd2-9c1e-4f2b6d8e1a56", CostUSD: 0.50, Verdict: VerdictPass})

	for _, line := range strings.Split(l.Render(), "\n") {
		if strings.HasPrefix(line, "--") && len(line) > 80 {
			t.Errorf("a budgeted run's rule is %d columns, past an 80-column terminal:\n%s", len(line), l.Render())
		}
	}
}

// TestRender_WorstRowFitsAnEightyColumnTerminal is the assertion neither #115
// nor #119 could have written, because each shipped against a table the other
// had not widened yet. Both features spend columns in the same 80, and the
// worst realistic row spends them at once: a 16-column node id, the widest
// verdict cell the closed qualifier set can produce, a truncated session stub,
// and a cost carrying its budget annotation. It asserts the FRAME — everything
// up to and including the last fixed column — not the whole line: DETAIL is
// ragged by design and runs to the scheduler's 240-rune cap, so a row with a
// detail on it was never going to fit and is not what wraps a terminal twice
// per run.
func TestRender_WorstRowFitsAnEightyColumnTerminal(t *testing.T) {
	l := New("run-worst")
	l.Record(Record{
		NodeID:     "review-security",
		SessionID:  "0198a3f7-0a3b-7bd2-9c1e-4f2b6d8e1a55",
		CostUSD:    1.98,
		BudgetUSD:  2.00,
		Verdict:    VerdictPass,
		Provenance: "self-reported",
		Detail:     "worst-detail",
	})

	row := rowFor(t, l.Render(), "review-security")
	frame := columnOf(row, "worst-detail")
	if frame != tableWidth+budgetAnnotationWidth-detailWidth {
		t.Fatalf("DETAIL starts at column %d, but the rule reserves %d for the columns before it",
			frame, tableWidth+budgetAnnotationWidth-detailWidth)
	}
	if frame+len("DETAIL") > 80 {
		t.Errorf("the widest qualifier beside a budget annotation pushes DETAIL's header to column %d, past an 80-column terminal:\n%s",
			frame+len("DETAIL"), l.Render())
	}
}

// TestRender_TruncatedSessionKeepsDetailAligned measures the SESSION column in
// display columns rather than bytes, which is the only measure a terminal
// reader has: a truncated id's ellipsis is three bytes and one column, and a
// row with no session id at all has neither, so if the two are padded on
// different scales they cannot both land on the header's DETAIL.
//
// It does not currently fail against a byte-padded %-*s — a truncated stub is
// 21 bytes, so that verb declines to pad it and its 19 runes land on
// sessionWidth by coincidence. This asserts the property the coincidence
// happens to satisfy, so that changing sessionStub is caught here rather than
// in a user's terminal.
func TestRender_TruncatedSessionKeepsDetailAligned(t *testing.T) {
	l := New("run-sessions")
	l.Record(Record{NodeID: "long", SessionID: "0198a3f7-0a3b-7bd2-9c1e-4f2b6d8e1a55", CostUSD: 0.01, Verdict: VerdictPass, Detail: "long-detail"})
	l.Record(Record{NodeID: "none", CostUSD: 0.01, Verdict: VerdictPass, Detail: "none-detail"})

	out := l.Render()
	long := columnOf(rowFor(t, out, "long"), "long-detail")
	none := columnOf(rowFor(t, out, "none"), "none-detail")
	header := columnOf(rowFor(t, out, "NODE"), "DETAIL")

	if long != none || long != header {
		t.Errorf("DETAIL starts at columns %d (truncated session), %d (no session), %d (header):\n%s",
			long, none, header, out)
	}
}

// columnOf is strings.Index in display columns rather than bytes, which is the
// only measure the reader of a terminal table has.
func columnOf(line, substr string) int {
	at := strings.Index(line, substr)
	if at < 0 {
		return -1
	}
	return utf8.RuneCountInString(line[:at])
}

// rowFor returns the rendered line beginning with prefix, failing the test if
// the table has no such row.
func rowFor(t *testing.T, rendered, prefix string) string {
	t.Helper()
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	t.Fatalf("no row for %q in:\n%s", prefix, rendered)
	return ""
}

func TestRender_EmptyLedger(t *testing.T) {
	l := New("empty")
	out := l.Render()
	if !strings.Contains(out, "TOTAL COST: $0.0000") {
		t.Fatalf("empty ledger should show zero total:\n%s", out)
	}
}

func TestTotalCost_IncludesPlanningCost(t *testing.T) {
	l := New("auto-run")
	l.Record(Record{NodeID: "write", CostUSD: 0.7977, Verdict: VerdictPass})
	l.Record(Record{NodeID: "critique", CostUSD: 0.5327, Verdict: VerdictPass})
	l.RecordPlanningCost(0.6069)

	// 0.7977 + 0.5327 + 0.6069 = 1.9373 — the planning call is part of the total.
	if got := l.TotalCost(); got < 1.9372 || got > 1.9374 {
		t.Fatalf("total cost = %v, want ~1.9373 (nodes + planning)", got)
	}
}

func TestRender_ShowsPlanningLineAndFoldedTotal(t *testing.T) {
	l := New("auto-run")
	l.Record(Record{NodeID: "write", CostUSD: 0.7977, Verdict: VerdictPass})
	l.Record(Record{NodeID: "critique", CostUSD: 0.5327, Verdict: VerdictPass})
	l.RecordPlanningCost(0.6069)

	out := l.Render()
	for _, want := range []string{"PLANNING COST: $0.6069", "TOTAL COST: $1.9373"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
}

// TestRender_NoPlanningLineWhenZero pins the invariant that the planning-cost
// feature leaves the hand-written `run` path unaffected: with no planning cost
// recorded, the summary shows no planning line and the total equals exactly the
// per-node sum. This is the one most likely to silently regress later, so it is
// asserted explicitly.
func TestRender_NoPlanningLineWhenZero(t *testing.T) {
	l := New("run-path")
	l.Record(Record{NodeID: "a", CostUSD: 0.10, Verdict: VerdictPass})
	l.Record(Record{NodeID: "b", CostUSD: 0.25, Verdict: VerdictPass})

	out := l.Render()
	if strings.Contains(out, "PLANNING COST") {
		t.Errorf("run path must not show a planning line:\n%s", out)
	}
	if !strings.Contains(out, "TOTAL COST: $0.3500") {
		t.Errorf("total must equal the node-cost sum $0.3500:\n%s", out)
	}
}

// The provenance qualifier (ADR 0016 §6). #119's run printed four rows of the
// word PASS, one of which was a 17-second self-report certifying a branch that
// did not compile. These tests pin the rendering that stops the ledger from
// spelling a self-report and a measurement the same way.

// mixedProvenanceLedger is #119's run as it would render today: a self-reported
// $11.01 node, a self-reported check beside it — here upgraded to `verified`
// because --verify-cmd was supplied — a human gate, an unchecked node, and a
// failure. One run touching all four qualifiers plus the unqualified case.
func mixedProvenanceLedger() *RunLedger {
	l := New("run-119")
	l.Record(Record{NodeID: "plan-branch", SessionID: "sess-plan", CostUSD: 0.0134, Verdict: VerdictPass, Provenance: "exit-only"})
	l.Record(Record{NodeID: "apply-fix", SessionID: "sess-apply", CostUSD: 11.01, Verdict: VerdictPass, Provenance: "self-reported"})
	l.Record(Record{NodeID: "review-gate", Verdict: VerdictPass, Detail: "gate approved", Provenance: "approved"})
	l.Record(Record{NodeID: "verify-branch", SessionID: "sess-verify", CostUSD: 0.13, Verdict: VerdictPass, Provenance: "verified"})
	l.Record(Record{NodeID: "publish", SessionID: "sess-pub", CostUSD: 0.09, Verdict: VerdictFail, Detail: "verify: exit 1 (want 0)"})
	return l
}

// TestRender_QualifiesEveryPassNotOnlyTheWeakOnes is the rendering decision
// itself, asserted rather than left to taste. Marking only `self-reported` and
// `exit-only` would be narrower and would still have caught #119 — but it
// would encode "the engine gathered evidence" as the ABSENCE of a mark, and
// #119 is a story about a reader who read absence as assurance. `verified` in
// particular is the confirmation a user who paid for --verify-cmd is owed.
func TestRender_QualifiesEveryPassNotOnlyTheWeakOnes(t *testing.T) {
	out := mixedProvenanceLedger().Render()

	for _, want := range []string{
		"PASS (exit-only)",
		"PASS (self-reported)",
		"PASS (approved)",
		"PASS (verified)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q — every PASS carries its qualifier:\n%s", want, out)
		}
	}
}

// TestRender_FailCarriesNoQualifier — the qualifier qualifies a PASS. A FAIL
// already states its cause in DETAIL, and a strength word on a failure invites
// reading it as "how sure we are it failed".
func TestRender_FailCarriesNoQualifier(t *testing.T) {
	out := mixedProvenanceLedger().Render()

	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, "publish") {
			continue
		}
		// Match on the verdict cell, not the whole line: this row's DETAIL
		// legitimately contains parentheses ("exit 1 (want 0)").
		if strings.Contains(line, "FAIL (") {
			t.Errorf("the FAIL row carries a qualifier: %q", line)
		}
		if !strings.Contains(line, "FAIL") {
			t.Errorf("the FAIL row lost its verdict: %q", line)
		}
		return
	}
	t.Fatal("no publish row in the table, so this asserted nothing")
}

// TestVerdictCell_AbsentProvenanceRendersBare covers the row a resume carries
// forward from a snapshot written before ADR 0016. Absent is not a qualifier
// and must never be rounded up into one — a row the engine knows nothing about
// renders exactly as it always did.
func TestVerdictCell_AbsentProvenanceRendersBare(t *testing.T) {
	if got := VerdictCell(Record{Verdict: VerdictPass}); got != "PASS" {
		t.Errorf("VerdictCell with no provenance = %q, want %q", got, "PASS")
	}
	if got := VerdictCell(Record{Verdict: VerdictPass, Provenance: "verified"}); got != "PASS (verified)" {
		t.Errorf("VerdictCell = %q, want %q", got, "PASS (verified)")
	}
}

// TestRender_ColumnsStayAlignedAcrossQualifiers is why VerdictWidth is a
// constant sized to the widest cell the closed set can produce, and not a
// per-run measurement. The ledger is read at a glance in a terminal of unknown
// width; a column that shifts left when a run happens to contain no
// self-report would make two runs' tables incomparable, and a ragged table
// unreadable at any width.
func TestRender_ColumnsStayAlignedAcrossQualifiers(t *testing.T) {
	lines := strings.Split(strings.TrimRight(mixedProvenanceLedger().Render(), "\n"), "\n")
	// Row lines only: skip the "Run …" line, the header, and both rules; stop
	// before the total footer.
	want := -1
	for _, line := range lines {
		nodeID, _, ok := strings.Cut(line, " ")
		if !ok || !strings.Contains(line, "PASS") && !strings.Contains(line, "FAIL") {
			continue
		}
		at := strings.Index(line, "sess-")
		if at < 0 {
			at = strings.Index(line, "-  ") // the gate's dashed session cell
		}
		if at < 0 {
			t.Fatalf("row %q has no session cell to align on", line)
		}
		if want < 0 {
			want = at
		} else if at != want {
			t.Errorf("row %q starts its SESSION cell at column %d, want %d — the table is ragged", nodeID, at, want)
		}
	}
	if want < 0 {
		t.Fatal("no rows found, so this asserted nothing")
	}
}
