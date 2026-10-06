package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"
)

func snapshotKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

// A run without an interview writes no interview key and keeps its stamp, so
// its snapshot is what it was before the record existed.
func TestWrite_NoInterviewWritesNoKey(t *testing.T) {
	dir := t.TempDir()
	graphJSON := json.RawMessage(`{"name":"g","nodes":[]}`)

	for _, tc := range []struct {
		name        string
		snap        Snapshot
		wantSchema  int
		description string
	}{
		{"plain", Snapshot{RunID: "r", Graph: graphJSON}, Schema, "no record at all"},
		{"conventions", Snapshot{RunID: "r", Graph: graphJSON, Conventions: &Conventions{StagedSHA256: "abc"}}, SchemaWithConventions, "conventions only"},
	} {
		path := filepath.Join(dir, tc.name+".json")
		if err := Write(path, tc.snap); err != nil {
			t.Fatal(err)
		}
		keys := snapshotKeys(t, path)
		if _, present := keys["interview"]; present {
			t.Errorf("%s: a snapshot with no interview carries an interview key", tc.description)
		}
		if got := rawSchema(t, path); got != tc.wantSchema {
			t.Errorf("%s: stamped schema %d, want %d", tc.description, got, tc.wantSchema)
		}
	}
}

// The record's JSON shape: hash, counts, ending and cost, every one written
// even at zero, and nothing else — no question, no answer.
func TestWrite_InterviewRecordShape(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	record := &Interview{
		StagedSHA256: "e3b0c442",
		Asked:        3,
		Answered:     2,
		Skipped:      1,
		Ending:       "cap",
		CostUSD:      0.125,
		CostUnknown:  true,
		Usage:        TokenUsage{InputTokens: 900, OutputTokens: 40},
	}
	if err := Write(path, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), Interview: record}); err != nil {
		t.Fatal(err)
	}

	var block map[string]json.RawMessage
	if err := json.Unmarshal(snapshotKeys(t, path)["interview"], &block); err != nil {
		t.Fatalf("interview block: %v", err)
	}
	var names []string
	for name := range block {
		names = append(names, name)
	}
	sort.Strings(names)
	want := []string{"answered", "asked", "cost_unknown", "cost_usd", "ending", "skipped", "staged_sha256", "usage"}
	if !reflect.DeepEqual(names, want) {
		t.Errorf("interview keys = %v, want %v", names, want)
	}
	var usage TokenUsage
	if err := json.Unmarshal(block["usage"], &usage); err != nil || usage != record.Usage {
		t.Errorf("usage = %s, want the token counts %+v", block["usage"], record.Usage)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Interview == nil || *snap.Interview != *record {
		t.Errorf("interview record did not round-trip: %+v", snap.Interview)
	}
}

// /done at the first prompt is still an interview: recorded with zero
// answers, its ending and its cost, not left out as though none was asked.
func TestWrite_ZeroAnswerInterviewIsRecordedInFull(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	record := &Interview{StagedSHA256: "e3b0c442", Asked: 1, Ending: "operator", CostUSD: 0}
	if err := Write(path, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), Interview: record}); err != nil {
		t.Fatal(err)
	}
	var block map[string]json.RawMessage
	if err := json.Unmarshal(snapshotKeys(t, path)["interview"], &block); err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"answered": "0", "skipped": "0", "cost_usd": "0", "ending": `"operator"`, "asked": "1"} {
		if got := string(block[key]); got != want {
			t.Errorf("%s = %s, want %s", key, got, want)
		}
	}
	for _, absent := range []string{"cost_unknown", "usage"} {
		if _, present := block[absent]; present {
			t.Errorf("%s is written for a zero value", absent)
		}
	}
}

// The record holds no field that could carry the operator's text: every
// field is a hash, a count, the ending token, or the cost. A field added
// later that is a free string fails here until someone decides it is not an
// answer.
func TestInterview_HoldsNoAnswerText(t *testing.T) {
	allowedStrings := map[string]bool{"StagedSHA256": true, "Ending": true}
	for _, field := range reflect.VisibleFields(reflect.TypeOf(Interview{})) {
		if field.Type.Kind() == reflect.String && !allowedStrings[field.Name] {
			t.Errorf("Interview.%s is a free string; the record must not hold question or answer text", field.Name)
		}
		if k := field.Type.Kind(); k == reflect.Slice || k == reflect.Map {
			t.Errorf("Interview.%s is a %s; the record must not hold the exchanges", field.Name, k)
		}
	}
}

// The interview record does not move the stamp (see Interview): with it alone
// a snapshot stays at Schema, and beside conventions it is what conventions
// stamp. Load reads both.
func TestWrite_InterviewKeepsTheStamp(t *testing.T) {
	dir := t.TempDir()
	record := &Interview{StagedSHA256: "abc", Asked: 1, Answered: 1, Ending: "enough"}

	alone := filepath.Join(dir, "alone.json")
	if err := Write(alone, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), Interview: record}); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, alone); got != Schema {
		t.Errorf("an interview record alone stamped schema %d, want %d", got, Schema)
	}

	both := filepath.Join(dir, "both.json")
	if err := Write(both, Snapshot{RunID: "r", Graph: json.RawMessage(`{}`), Interview: record,
		Conventions: &Conventions{StagedSHA256: "def"}}); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, both); got != SchemaWithConventions {
		t.Errorf("interview beside conventions stamped schema %d, want %d", got, SchemaWithConventions)
	}

	for _, path := range []string{alone, both} {
		snap, err := Load(path)
		if err != nil {
			t.Fatalf("load %s: %v", filepath.Base(path), err)
		}
		if snap.Interview == nil || *snap.Interview != *record {
			t.Errorf("%s: interview record did not round-trip: %+v", filepath.Base(path), snap.Interview)
		}
	}
}
