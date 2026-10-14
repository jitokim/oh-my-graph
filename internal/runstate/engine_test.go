package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testCommit = "0123456789abcdef0123456789abcdef01234567"

// TestEngine_RoundTrip: the build stamp (#373) survives Write and Load, and a
// snapshot without it round-trips without gaining one.
func TestEngine_RoundTrip(t *testing.T) {
	for name, stamp := range map[string]Snapshot{
		"stamped":   {EngineVersion: "9.9.9-test", EngineCommit: testCommit, EngineModified: true},
		"unstamped": {},
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			snap := sampleSnapshot()
			snap.EngineVersion, snap.EngineCommit, snap.EngineModified = stamp.EngineVersion, stamp.EngineCommit, stamp.EngineModified
			if err := Write(path, snap); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatalf("load: %v", err)
			}
			if got.EngineVersion != stamp.EngineVersion || got.EngineCommit != stamp.EngineCommit || got.EngineModified != stamp.EngineModified {
				t.Fatalf("engine = (%q, %q, %v), want (%q, %q, %v)",
					got.EngineVersion, got.EngineCommit, got.EngineModified,
					stamp.EngineVersion, stamp.EngineCommit, stamp.EngineModified)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read back: %v", err)
			}
			if name == "unstamped" && strings.Contains(string(raw), `"engine_`) {
				t.Fatalf("an unstamped snapshot gained engine keys:\n%s", raw)
			}
		})
	}
}

// TestEngine_OldSnapshotStillLoads: a file written before #373 carries none of
// the three keys and loads as before, under the same schema.
func TestEngine_OldSnapshotStillLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"schema":3,"run_id":"old-run","runtime":"claude","graph":{}}`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("a pre-#373 snapshot must still load: %v", err)
	}
	if got.EngineVersion != "" || got.EngineCommit != "" || got.EngineModified {
		t.Fatalf("absent engine keys decoded to (%q, %q, %v)", got.EngineVersion, got.EngineCommit, got.EngineModified)
	}
}

// TestEngine_OmitRules: no empty engine_commit, no engine_modified:false, and
// no engine_modified without a commit — the last enforced at the type's own
// encoding boundary.
func TestEngine_OmitRules(t *testing.T) {
	for name, tc := range map[string]struct {
		snap       Snapshot
		want, deny []string
	}{
		"version only": {
			snap: Snapshot{EngineVersion: "1.0.0"},
			want: []string{`"engine_version":"1.0.0"`},
			deny: []string{`"engine_commit"`, `"engine_modified"`},
		},
		"clean commit": {
			snap: Snapshot{EngineVersion: "1.0.0", EngineCommit: testCommit},
			want: []string{`"engine_commit":"` + testCommit + `"`},
			deny: []string{`"engine_modified"`},
		},
		"dirty commit": {
			snap: Snapshot{EngineVersion: "1.0.0", EngineCommit: testCommit, EngineModified: true},
			want: []string{`"engine_commit":"` + testCommit + `"`, `"engine_modified":true`},
		},
		"modified without commit": {
			snap: Snapshot{EngineVersion: "1.0.0", EngineModified: true},
			deny: []string{`"engine_commit"`, `"engine_modified"`},
		},
	} {
		t.Run(name, func(t *testing.T) {
			encoded, err := json.Marshal(tc.snap)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			for _, w := range tc.want {
				if !strings.Contains(string(encoded), w) {
					t.Errorf("missing %s in %s", w, encoded)
				}
			}
			for _, d := range tc.deny {
				if strings.Contains(string(encoded), d) {
					t.Errorf("unexpected %s in %s", d, encoded)
				}
			}
		})
	}
}
