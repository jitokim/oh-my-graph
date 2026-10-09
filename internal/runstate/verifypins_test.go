package runstate

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

var samplePins = []VerifyPin{
	{Word: "./check.sh", Path: "/work/check.sh", SHA256: strings.Repeat("ab", 32)},
	{Word: "tests/accept.txt", Path: "/work/tests/accept.txt", SHA256: strings.Repeat("cd", 32)},
}

// TestWrite_VerifyPinsRoundTrip_363 (#363): the pins survive Write and Load in
// order, each entry is exactly word, path and sha256, and the record does not
// move the schema stamp.
func TestWrite_VerifyPinsRoundTrip_363(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), VerifyPins: samplePins}); err != nil {
		t.Fatal(err)
	}

	var block []map[string]json.RawMessage
	if err := json.Unmarshal(snapshotKeys(t, path)["verify_pins"], &block); err != nil {
		t.Fatalf("verify_pins block: %v", err)
	}
	if len(block) != 2 || len(block[0]) != 3 ||
		string(block[0]["word"]) != `"./check.sh"` || string(block[0]["path"]) != `"/work/check.sh"` ||
		string(block[0]["sha256"]) != `"`+samplePins[0].SHA256+`"` {
		t.Errorf("verify_pins block = %v, want word, path and sha256 per entry", block)
	}
	if got := rawSchema(t, path); got != Schema {
		t.Errorf("stamped schema %d, want %d — the record is additive", got, Schema)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(snap.VerifyPins, samplePins) {
		t.Errorf("verify pins did not round-trip: %+v", snap.VerifyPins)
	}
}

// TestWrite_NoVerifyPinsIsByteIdentical_363 (#363): a run without
// --verify-cmd writes no verify_pins key, and an empty (non-nil) slice writes
// the same bytes as nil, so such a snapshot is what it was before.
func TestWrite_NoVerifyPinsIsByteIdentical_363(t *testing.T) {
	dir := t.TempDir()
	without, empty := filepath.Join(dir, "without.json"), filepath.Join(dir, "empty.json")
	if err := Write(without, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	snap := sampleSnapshot()
	snap.VerifyPins = []VerifyPin{}
	if err := Write(empty, snap); err != nil {
		t.Fatal(err)
	}
	a, err := os.ReadFile(without)
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(empty)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(a), `"verify_pins"`) {
		t.Errorf("a snapshot with no pins carries a verify_pins key:\n%s", a)
	}
	if !bytes.Equal(a, b) {
		t.Errorf("an empty pin list changed the bytes:\n%s\nvs\n%s", a, b)
	}
	loaded, err := Load(without)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.VerifyPins != nil {
		t.Errorf("absent verify_pins loaded as %+v, want nil", loaded.VerifyPins)
	}
}

// TestSnapshotRecorder_CarriesVerifyPinsOnEveryWrite_363 (#363): the pins are
// part of the recorder's static base, so every write — the initial one, a
// node's, a pause's — keeps them; a resumed leg seeded with them therefore
// leaves them for the next resume.
func TestSnapshotRecorder_CarriesVerifyPinsOnEveryWrite_363(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	base := baseSnapshot("run-1")
	base.VerifyPins = samplePins
	rec := NewSnapshotRecorder(path, base)

	writes := []func() error{
		rec.WriteInitial,
		func() error { return rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}) },
		func() error { return rec.RecordPause("gate1") },
	}
	for i, write := range writes {
		if err := write(); err != nil {
			t.Fatalf("write %d: %v", i, err)
		}
		snap, err := Load(path)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(snap.VerifyPins, samplePins) {
			t.Errorf("write %d dropped or changed the pins: %+v", i, snap.VerifyPins)
		}
	}
}
