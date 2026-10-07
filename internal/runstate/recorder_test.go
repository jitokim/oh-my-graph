package runstate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func baseSnapshot(runID string) Snapshot {
	return Snapshot{
		RunID:           runID,
		GraphSourcePath: "graphs/x.yaml",
		GraphSHA256:     "deadbeef",
		Graph:           []byte(`{"name":"g","nodes":[{"id":"a","prompt":"a"},{"id":"gate1","type":"gate","depends_on":["a"]}]}`),
	}
}

// --- RecordNode writes a complete, loadable snapshot after every call -------

func TestSnapshotRecorder_RecordNodeWritesAfterEveryCall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass, SessionID: "s-a", CostUSD: 0.1, Duration: time.Second}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load after one node: %v", err)
	}
	if got.Nodes["a"].SessionID != "s-a" {
		t.Fatalf("node a not persisted after first RecordNode: %+v", got.Nodes)
	}

	if err := rec.RecordNode("b", NodeRecord{Verdict: VerdictFail, Detail: "boom"}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	got, err = Load(path)
	if err != nil {
		t.Fatalf("load after second node: %v", err)
	}
	// Both nodes must be present — the second write must not have clobbered the
	// first.
	if got.Nodes["a"].SessionID != "s-a" || got.Nodes["b"].Detail != "boom" {
		t.Fatalf("snapshot lost an earlier node's record: %+v", got.Nodes)
	}
}

// --- CompletedNodes reflects RecordNode immediately --------------------------

func TestSnapshotRecorder_RecordNodeMakesNodeCompletedOnDisk(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))
	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !got.CompletedNodes()["a"] {
		t.Fatal("a PASS node recorded via SnapshotRecorder must count as completed on reload")
	}
}

// --- RecordGateDecision --------------------------------------------------

func TestSnapshotRecorder_RecordGateDecisionPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	if err := rec.RecordGateDecision("gate1", GateApprove); err != nil {
		t.Fatalf("RecordGateDecision: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Gate.Decisions["gate1"] != GateApprove {
		t.Fatalf("gate1 decision = %q, want approve", got.Gate.Decisions["gate1"])
	}
	// A decision alone (approve/reject) must not mark the run as paused.
	if got.Gate.PausedAt != "" {
		t.Fatalf("PausedAt = %q, want empty after a non-pause decision", got.Gate.PausedAt)
	}
}

// --- RecordPause sets both PausedAt and the gate's decision -----------------

func TestSnapshotRecorder_RecordPauseSetsPausedAtAndDecision(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	if err := rec.RecordPause("gate1"); err != nil {
		t.Fatalf("RecordPause: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Gate.PausedAt != "gate1" {
		t.Fatalf("PausedAt = %q, want gate1", got.Gate.PausedAt)
	}
	if got.Gate.Decisions["gate1"] != GatePause {
		t.Fatalf("gate1 decision = %q, want pause", got.Gate.Decisions["gate1"])
	}
}

// TestRecordDescribedPause_WritesBothInOneSnapshot_348: #348 — the paused
// gate's description is persisted together with PausedAt and the pause
// decision, so a reader never sees one without the other.
func TestRecordDescribedPause_WritesBothInOneSnapshot_348(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))

	if err := rec.RecordDescribedPause("gate1", "ship T-42?"); err != nil {
		t.Fatalf("RecordDescribedPause: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Gate.PausedAt != "gate1" || got.Gate.PausedGateDescription != "ship T-42?" || got.Gate.Decisions["gate1"] != GatePause {
		t.Fatalf("gate state = %+v, want paused at gate1 with its description and a pause decision", got.Gate)
	}
	if got.Schema != Schema {
		t.Fatalf("schema = %d, the field must not move the stamp", got.Schema)
	}
}

// TestRecordPause_WritesNoDescriptionKey_348: #348 — a pause without a
// description writes exactly the bytes it wrote before the field existed.
func TestRecordPause_WritesNoDescriptionKey_348(t *testing.T) {
	dir := t.TempDir()
	plain := filepath.Join(dir, "plain.json")
	described := filepath.Join(dir, "described.json")
	if err := NewSnapshotRecorder(plain, baseSnapshot("run-1")).RecordPause("gate1"); err != nil {
		t.Fatalf("RecordPause: %v", err)
	}
	if err := NewSnapshotRecorder(described, baseSnapshot("run-1")).RecordDescribedPause("gate1", ""); err != nil {
		t.Fatalf("RecordDescribedPause: %v", err)
	}
	plainBytes, err := os.ReadFile(plain)
	if err != nil {
		t.Fatal(err)
	}
	describedBytes, err := os.ReadFile(described)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(plainBytes), "paused_gate_description") {
		t.Fatalf("an undescribed pause wrote the key:\n%s", plainBytes)
	}
	if string(plainBytes) != string(describedBytes) {
		t.Fatalf("an empty description changed the bytes:\n%s\nvs\n%s", plainBytes, describedBytes)
	}
}

// --- a resume's base carries forward the prior leg's records ----------------

func TestNewSnapshotRecorder_SeedsFromBaseNodesWithoutAliasing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := baseSnapshot("run-1")
	base.Nodes = map[string]NodeRecord{"a": {Verdict: VerdictPass, SessionID: "s-a"}}

	rec := NewSnapshotRecorder(path, base)
	if err := rec.RecordNode("gate1", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Nodes["a"].SessionID != "s-a" {
		t.Fatalf("prior leg's node record was not carried forward: %+v", got.Nodes)
	}
	if !got.CompletedNodes()["gate1"] {
		t.Fatal("newly recorded node missing from the resumed leg's snapshot")
	}

	// The caller's own base.Nodes map must not have been mutated by the
	// recorder writing into its internal copy.
	if _, mutated := base.Nodes["gate1"]; mutated {
		t.Fatal("NewSnapshotRecorder aliased the caller's Nodes map instead of copying it")
	}
}
