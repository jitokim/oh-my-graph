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

func TestRender_EmptyLedger(t *testing.T) {
	l := New("empty")
	out := l.Render()
	if !strings.Contains(out, "TOTAL COST: $0.0000") {
		t.Fatalf("empty ledger should show zero total:\n%s", out)
	}
}
