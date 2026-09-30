package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func rawSchema(t *testing.T, path string) int {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var head struct {
		Schema int `json:"schema"`
	}
	if err := json.Unmarshal(data, &head); err != nil {
		t.Fatal(err)
	}
	return head.Schema
}

// TestWrite_ConventionsStampSchema4 is ADR 0041 §5 test 13 and test 1's
// snapshot half: the conventions record stamps schema 4, and a snapshot
// without one keeps schema 3 and carries no conventions key.
func TestWrite_ConventionsStampSchema4(t *testing.T) {
	dir := t.TempDir()
	graphJSON := json.RawMessage(`{"name":"g","nodes":[]}`)

	plain := filepath.Join(dir, "plain.json")
	if err := Write(plain, Snapshot{RunID: "r", Graph: graphJSON}); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, plain); got != Schema {
		t.Errorf("a snapshot with no conventions stamped schema %d, want %d", got, Schema)
	}
	data, _ := os.ReadFile(plain)
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	if _, present := keys["conventions"]; present {
		t.Error("a snapshot with no conventions carries a conventions key")
	}

	withConv := filepath.Join(dir, "conv.json")
	record := &Conventions{StagedSHA256: "abc", Sources: []ConventionsSource{{Path: "/x/style.md", Bytes: 9, SHA256: "def", ImportLines: 1}}}
	if err := Write(withConv, Snapshot{RunID: "r", Graph: graphJSON, Conventions: record}); err != nil {
		t.Fatal(err)
	}
	if got := rawSchema(t, withConv); got != SchemaWithConventions {
		t.Errorf("a snapshot with conventions stamped schema %d, want %d", got, SchemaWithConventions)
	}
	// An older binary compared strictly against its own Schema, which was 3:
	// the stamp must differ from it or that binary would resume the run
	// without the conventions. It must also stay ABOVE it: a future bump of
	// Schema to 5 that left this stamp at 4 would stamp new-format runs with a
	// version this build accepts, so it would misread them without refusing.
	if SchemaWithConventions <= Schema {
		t.Fatalf("the conventions stamp (%d) must be above the plain schema (%d): a Schema bump must also move or retire SchemaWithConventions",
			SchemaWithConventions, Schema)
	}

	for _, path := range []string{plain, withConv} {
		snap, err := Load(path)
		if err != nil {
			t.Fatalf("the current reader must load %s: %v", filepath.Base(path), err)
		}
		if path == withConv {
			if snap.Conventions == nil || snap.Conventions.StagedSHA256 != "abc" || len(snap.Conventions.Sources) != 1 ||
				snap.Conventions.Sources[0] != record.Sources[0] {
				t.Errorf("conventions record did not round-trip: %+v", snap.Conventions)
			}
		}
	}
}

// TestLoad_SchemaBeyondConventionsIsRefused: accepting 4 does not open the
// door to every newer number.
func TestLoad_SchemaBeyondConventionsIsRefused(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schema":5,"run_id":"x","graph":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	var mErr *SchemaMismatchError
	if !errors.As(err, &mErr) || mErr.Found != 5 {
		t.Fatalf("want a SchemaMismatchError naming 5, got %T: %v", err, err)
	}
	// The refusal names BOTH accepted versions, not just Schema.
	if mErr.Want != Schema || mErr.WantAlso != SchemaWithConventions {
		t.Errorf("mismatch carried want %d/%d, want %d/%d", mErr.Want, mErr.WantAlso, Schema, SchemaWithConventions)
	}
	if msg := err.Error(); !strings.Contains(msg, "understands versions 3 and 4") {
		t.Errorf("message does not name both accepted versions: %s", msg)
	}
}
