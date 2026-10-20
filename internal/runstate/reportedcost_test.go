package runstate

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func usd(v float64) *float64 { return &v }

// TestNodeRecord_ReportedCostRoundTrips (#398): the raw reported figure
// survives Write/Load — a known $0 included, which must not collapse into the
// absent key that means unknown — and a nil one writes no key at all.
func TestNodeRecord_ReportedCostRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, Snapshot{
		RunID: "r1",
		Graph: json.RawMessage(`{"name":"g"}`),
		Nodes: map[string]NodeRecord{
			"child":   {Verdict: VerdictPass, SessionID: "s", CostUSD: 0.25, ReportedCostUSD: usd(0.65)},
			"free":    {Verdict: VerdictPass, SessionID: "s2", ReportedCostUSD: usd(0)},
			"unknown": {Verdict: VerdictPass, SessionID: "s3", CostUnknown: true},
		},
	}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if r := got.Nodes["child"].ReportedCostUSD; r == nil || *r != 0.65 || got.Nodes["child"].CostUSD != 0.25 {
		t.Fatalf("child = %+v, want cost 0.25 beside reported 0.65", got.Nodes["child"])
	}
	if r := got.Nodes["free"].ReportedCostUSD; r == nil || *r != 0 {
		t.Fatalf("free reported = %v, want a known 0", r)
	}
	if r := got.Nodes["unknown"].ReportedCostUSD; r != nil {
		t.Fatalf("unknown reported = %v, want nil", *r)
	}

	encoded, err := json.Marshal(NodeRecord{Verdict: VerdictPass, SessionID: "s"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "reported_cost_usd") {
		t.Fatalf("a record with no raw figure wrote the key: %s", encoded)
	}
}

// TestLoad_PreReportedCostRecordIsUnknown: a snapshot an engine from before
// #398 wrote still loads, and its session record's raw figure reads as
// unknown (nil) — never as its booked cost_usd, and never as $0.
func TestLoad_PreReportedCostRecordIsUnknown(t *testing.T) {
	snap, err := Load(filepath.Join("testdata", "pre-ceiling-record-state.json"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	rec := snap.Nodes["reply-drafts"]
	if rec.SessionID == "" || rec.CostUSD == 0 {
		t.Fatalf("fixture record = %+v, want a costed session record", rec)
	}
	if rec.ReportedCostUSD != nil {
		t.Fatalf("old record reported = %v, want nil (unknown)", *rec.ReportedCostUSD)
	}
}

// TestRecordNode_ReportedCostIsNotFoldedAcrossRounds: CostUSD accumulates
// across feedback rounds, but the raw figure is the latest execution's — the
// baseline a session child resumes from — so it is replaced, not summed.
func TestRecordNode_ReportedCostIsNotFoldedAcrossRounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	r := NewSnapshotRecorder(path, Snapshot{RunID: "r1", Graph: json.RawMessage(`{}`)})
	if err := r.RecordNode("impl", NodeRecord{Verdict: VerdictPass, SessionID: "s1", CostUSD: 0.30, ReportedCostUSD: usd(0.30), Round: 1}); err != nil {
		t.Fatal(err)
	}
	if err := r.RecordNode("impl", NodeRecord{Verdict: VerdictPass, SessionID: "s2", CostUSD: 0.20, ReportedCostUSD: usd(0.20), Round: 2}); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	rec := got.Nodes["impl"]
	if rec.CostUSD != 0.5 || rec.ReportedCostUSD == nil || *rec.ReportedCostUSD != 0.20 {
		t.Fatalf("impl = cost %v reported %v, want the folded 0.5 beside the latest raw 0.20", rec.CostUSD, rec.ReportedCostUSD)
	}
}
