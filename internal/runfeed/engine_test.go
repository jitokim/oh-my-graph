package runfeed

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

// TestEvent_EngineFlattensAndOmits: the #373 build stamp sits at the event's
// top level when set, and adds nothing to an event that does not carry it.
func TestEvent_EngineFlattensAndOmits(t *testing.T) {
	const commit = "0123456789abcdef0123456789abcdef01234567"
	stamped, err := json.Marshal(Event{Type: EventRunStarted, Engine: Engine{Version: "9.9.9-test", Commit: commit, Modified: true}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, want := range []string{`"engine_version":"9.9.9-test"`, `"engine_commit":"` + commit + `"`, `"engine_modified":true`} {
		if !strings.Contains(string(stamped), want) {
			t.Errorf("missing %s in %s", want, stamped)
		}
	}
	if strings.Contains(string(stamped), `"Engine"`) {
		t.Errorf("engine stamp must be flattened, got %s", stamped)
	}

	versionOnly, err := json.Marshal(Event{Type: EventRunStarted, Engine: Engine{Version: "1.0.0"}})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, deny := range []string{`"engine_commit"`, `"engine_modified"`} {
		if strings.Contains(string(versionOnly), deny) {
			t.Errorf("unexpected %s in %s", deny, versionOnly)
		}
	}

	plain, err := json.Marshal(Event{Type: EventNodeStarted, NodeID: "a"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(plain), `"engine_`) {
		t.Errorf("an unstamped event carries engine keys: %s", plain)
	}
}

// TestReadEvents_EngineRoundTrips: a stamped run_started reads back with its
// stamp, through the same reader every in-repo view uses.
func TestReadEvents_EngineRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	want := Engine{Version: "9.9.9-test", Commit: "0123456789abcdef0123456789abcdef01234567"}
	if err := w.Emit(Event{Type: EventRunStarted, Engine: want}); err != nil {
		t.Fatalf("emit: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	lines := readLines(t, path)
	if len(lines) != 1 {
		t.Fatalf("got %d lines, want 1", len(lines))
	}
	var got Event
	if err := json.Unmarshal([]byte(lines[0]), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Engine != want {
		t.Fatalf("engine = %+v, want %+v", got.Engine, want)
	}
}
