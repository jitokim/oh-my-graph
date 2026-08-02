package runstate

import (
	"encoding/json"
	"path/filepath"
	"testing"
)

// TestSnapshot_FeedbackMarkerIsNeitherCompletedNorSettled pins the marker
// contract (ADR 0010): a feedback declarer's non-terminal marker (no
// verdict, round k) must not unblock dependents and must not block its own
// relaunch — a leg stopped mid-loop resumes INTO the loop, not out of it.
func TestSnapshot_FeedbackMarkerIsNeitherCompletedNorSettled(t *testing.T) {
	snap := Snapshot{Nodes: map[string]NodeRecord{
		"impl":   {Verdict: VerdictPass, Round: 1},
		"review": {Round: 1, Detail: "feedback round 1/2: verify failed"},
		"other":  {Verdict: VerdictFail},
	}}

	completed := snap.CompletedNodes()
	if !completed["impl"] || completed["review"] || completed["other"] {
		t.Fatalf("CompletedNodes = %v; the marker (and the FAIL) must be absent, the PASS present", completed)
	}
	settled := snap.SettledNodes()
	if !settled["impl"] || !settled["other"] || settled["review"] {
		t.Fatalf("SettledNodes = %v; the marker must stay launchable, both real verdicts must not", settled)
	}
}

// TestNodeRecord_RoundRoundTrips: the additive round field survives
// Write/Load and is omitted when zero, so an existing reader of a
// round-free snapshot sees byte-identical records.
func TestNodeRecord_RoundRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := Snapshot{
		RunID: "r1",
		Graph: json.RawMessage(`{"name":"g"}`),
		Nodes: map[string]NodeRecord{
			"impl":   {Verdict: VerdictPass, Round: 2, CostUSD: 0.5},
			"plain":  {Verdict: VerdictPass},
			"review": {Round: 2},
		},
	}
	if err := Write(path, want); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.Nodes["impl"].Round != 2 || got.Nodes["review"].Round != 2 {
		t.Fatalf("round did not survive the round-trip: %+v", got.Nodes)
	}
	if got.Nodes["plain"].Round != 0 {
		t.Fatalf("a round-free record grew a round: %+v", got.Nodes["plain"])
	}

	encoded, err := json.Marshal(NodeRecord{Verdict: VerdictPass})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"verdict":"PASS","session_id":"","cost_usd":0,"duration":0,"artifact_path":""}` {
		t.Fatalf("round is not omitempty — existing readers would see a new key on every record: %s", encoded)
	}
}

// TestSnapshotRecorder_AccumulatesCostAcrossRounds pins the cost story: a
// superseded round's spend folds into the record that replaces it, in both
// the shapes the loop produces — a body node's round k+1 PASS over its
// round k PASS, and a declarer's terminal record over its own round's
// marker (the marker priced the failing execution; the terminal prices the
// re-run — the node paid for both).
func TestSnapshotRecorder_AccumulatesCostAcrossRounds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, Snapshot{RunID: "r1", Graph: json.RawMessage(`{}`)})

	// Body node: round 0 PASS, then round 1 PASS — spend accumulates.
	if err := rec.RecordNode("impl", NodeRecord{Verdict: VerdictPass, CostUSD: 0.10}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := rec.RecordNode("impl", NodeRecord{Verdict: VerdictPass, CostUSD: 0.20, Round: 1}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}

	// Declarer: round-1 marker over the (implicit round-0) failing pass,
	// then the round-1 terminal PASS over the marker.
	if err := rec.RecordNode("review", NodeRecord{CostUSD: 0.05, Round: 1, Detail: "feedback round 1/2"}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := rec.RecordNode("review", NodeRecord{Verdict: VerdictPass, CostUSD: 0.07, Round: 1}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := snap.Nodes["impl"].CostUSD; !approx(got, 0.30) {
		t.Errorf("body node spend did not accumulate across rounds: got %v, want 0.30", got)
	}
	if got := snap.Nodes["review"].CostUSD; !approx(got, 0.12) {
		t.Errorf("declarer spend did not accumulate marker → terminal: got %v, want 0.12", got)
	}
}

// TestSnapshotRecorder_PlainOverwriteDoesNotAccumulate is the boundary: an
// ordinary same-round overwrite (no feedback involved) replaces the record
// outright, exactly as before ADR 0010.
func TestSnapshotRecorder_PlainOverwriteDoesNotAccumulate(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, Snapshot{RunID: "r1", Graph: json.RawMessage(`{}`)})

	if err := rec.RecordNode("dev", NodeRecord{Verdict: VerdictFail, CostUSD: 0.10}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	if err := rec.RecordNode("dev", NodeRecord{Verdict: VerdictPass, CostUSD: 0.20}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got := snap.Nodes["dev"].CostUSD; !approx(got, 0.20) {
		t.Errorf("a plain overwrite must replace, not accumulate: got %v, want 0.20", got)
	}
}

// approx compares reported spend within a float tolerance far below a cent.
func approx(got, want float64) bool {
	diff := got - want
	return diff < 1e-9 && diff > -1e-9
}
