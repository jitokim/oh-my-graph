package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// #328: a skipped baseline survives Write and Load unchanged, and the record
// does not move the schema stamp.
func TestWrite_BaselineRecordRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	record := &Baseline{Skipped: true, DeclaredBy: "--no-baseline"}
	if err := Write(path, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), Baseline: record}); err != nil {
		t.Fatal(err)
	}

	var block map[string]json.RawMessage
	if err := json.Unmarshal(snapshotKeys(t, path)["baseline"], &block); err != nil {
		t.Fatalf("baseline block: %v", err)
	}
	if string(block["skipped"]) != "true" || string(block["declared_by"]) != `"--no-baseline"` || len(block) != 2 {
		t.Errorf("baseline block = %v, want exactly skipped:true and declared_by:\"--no-baseline\"", block)
	}
	if got := rawSchema(t, path); got != Schema {
		t.Errorf("stamped schema %d, want %d — the record is additive", got, Schema)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Baseline == nil || *snap.Baseline != *record {
		t.Errorf("baseline record did not round-trip: %+v", snap.Baseline)
	}
}

// #328: a run that did not skip the baseline writes no baseline key at all,
// so its snapshot is byte for byte what it was before the record existed.
func TestWrite_NoBaselineRecordWritesNoKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"baseline"`) {
		t.Errorf("a snapshot with no skipped baseline carries a baseline key:\n%s", raw)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Baseline != nil {
		t.Errorf("absent baseline loaded as %+v, want nil", snap.Baseline)
	}
}
