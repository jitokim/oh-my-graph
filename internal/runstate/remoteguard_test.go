package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestWrite_RemoteGuardStampsSchema5 is ADR 0046 §8.9's snapshot half: a
// guarded snapshot stamps SchemaWithRemoteGuard with or without conventions,
// and an unguarded one keeps Schema, or SchemaWithConventions with them, and
// carries no forbid_remote_changes key.
func TestWrite_RemoteGuardStampsSchema5(t *testing.T) {
	graphJSON := json.RawMessage(`{"name":"g","nodes":[]}`)
	conv := &Conventions{StagedSHA256: "abc"}
	for _, tc := range []struct {
		name string
		snap Snapshot
		want int
	}{
		{"unguarded", Snapshot{RunID: "r", Graph: graphJSON}, Schema},
		{"unguarded with conventions", Snapshot{RunID: "r", Graph: graphJSON, Conventions: conv}, SchemaWithConventions},
		{"guarded", Snapshot{RunID: "r", Graph: graphJSON, ForbidRemoteChanges: true}, SchemaWithRemoteGuard},
		{"guarded with conventions", Snapshot{RunID: "r", Graph: graphJSON, Conventions: conv, ForbidRemoteChanges: true}, SchemaWithRemoteGuard},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := Write(path, tc.snap); err != nil {
				t.Fatal(err)
			}
			if got := rawSchema(t, path); got != tc.want {
				t.Errorf("stamped schema %d, want %d", got, tc.want)
			}
			data, _ := os.ReadFile(path)
			var keys map[string]json.RawMessage
			if err := json.Unmarshal(data, &keys); err != nil {
				t.Fatal(err)
			}
			if _, present := keys["forbid_remote_changes"]; present != tc.snap.ForbidRemoteChanges {
				t.Errorf("forbid_remote_changes key present = %v, want %v", present, tc.snap.ForbidRemoteChanges)
			}
			snap, err := Load(path)
			if err != nil {
				t.Fatalf("the current reader must load it: %v", err)
			}
			if snap.ForbidRemoteChanges != tc.snap.ForbidRemoteChanges {
				t.Errorf("ForbidRemoteChanges = %v after the round trip, want %v", snap.ForbidRemoteChanges, tc.snap.ForbidRemoteChanges)
			}
		})
	}
	// An older binary accepts 3 and 4: the guard's stamp must be neither, or
	// that binary would resume the run without the guard.
	if SchemaWithRemoteGuard <= SchemaWithConventions {
		t.Fatalf("the remote guard stamp (%d) must be above the conventions stamp (%d)", SchemaWithRemoteGuard, SchemaWithConventions)
	}
}

// TestLoad_PreGuardSchema3SnapshotLoadsUnguarded is ADR 0046 §8.9's old-file
// half: a schema-3 snapshot written before the remote change record existed,
// with no remote_changes and no forbid_remote_changes, still loads, unguarded.
func TestLoad_PreGuardSchema3SnapshotLoadsUnguarded(t *testing.T) {
	path := filepath.Join("testdata", "pre-ceiling-record-state.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote_changes", "run_base_sha", "forbid_remote_changes"} {
		if _, present := keys[key]; present {
			t.Fatalf("the fixture must predate %s", key)
		}
	}
	if got := rawSchema(t, path); got != Schema {
		t.Fatalf("fixture schema = %d, want %d", got, Schema)
	}
	snap, err := Load(path)
	if err != nil {
		t.Fatalf("a pre-guard schema-3 snapshot must load: %v", err)
	}
	if snap.ForbidRemoteChanges || snap.RemoteChanges != nil {
		t.Errorf("loaded guard = %v, record = %+v; want unguarded with no record", snap.ForbidRemoteChanges, snap.RemoteChanges)
	}
}
