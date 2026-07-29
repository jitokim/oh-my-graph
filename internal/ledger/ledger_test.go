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
