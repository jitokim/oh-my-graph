package ledger

import (
	"strings"
	"testing"
	"time"
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

func TestRender_ShowsBudgetDetail(t *testing.T) {
	l := New("run-budget")
	l.Record(Record{
		NodeID:    "spendy",
		CostUSD:   0.25,
		BudgetUSD: 0.10,
		Verdict:   VerdictFail,
		Detail:    `node "spendy" exceeded budget_usd: $0.2500 actual vs $0.1000 budgeted (over by $0.1500)`,
	})

	out := l.Render()
	for _, want := range []string{"spendy", "FAIL", "exceeded budget_usd", "over by $0.1500"} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q:\n%s", want, out)
		}
	}
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

// TestRender_ColumnsStayAlignedAcrossQualifiers is why verdictWidth is a
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
