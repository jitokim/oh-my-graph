package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// limitPauseAt is a fixed instant, so a round-trip can be compared exactly.
var limitPauseAt = time.Date(2026, 10, 8, 17, 20, 0, 0, time.UTC)

const claudeLimitCause = "You've hit your session limit · resets 5:20pm"

// TestRecordLimitPause_WritesTheRecordInOneSnapshot_358 (#358): ADR 0031 §8.1 — the
// limited nodes (sorted), the cause exactly as captured, and when, all in one
// write; the limited nodes themselves stay out of Nodes (ADR 0009) and the
// stamp does not move (§8.2).
func TestRecordLimitPause_WritesTheRecordInOneSnapshot_358(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	rec := NewSnapshotRecorder(path, baseSnapshot("run-1"))
	if err := rec.RecordNode("b", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatalf("RecordNode: %v", err)
	}

	ids := []string{"gate1", "a"}
	if err := rec.RecordLimitPause(LimitPause{NodeIDs: ids, Cause: claudeLimitCause, At: limitPauseAt}); err != nil {
		t.Fatalf("RecordLimitPause: %v", err)
	}
	if ids[0] != "gate1" {
		t.Errorf("RecordLimitPause sorted the caller's slice in place: %v", ids)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	want := &LimitPause{NodeIDs: []string{"a", "gate1"}, Cause: claudeLimitCause, At: limitPauseAt}
	if got.LimitPause == nil || !reflect.DeepEqual(got.LimitPause.NodeIDs, want.NodeIDs) ||
		got.LimitPause.Cause != want.Cause || !got.LimitPause.At.Equal(want.At) {
		t.Fatalf("limit pause = %+v, want %+v", got.LimitPause, want)
	}
	if _, ok := got.Nodes["a"]; ok {
		t.Error("a limited node must not be recorded in Nodes (ADR 0009)")
	}
	if got.Nodes["b"].Verdict != VerdictPass {
		t.Errorf("the limit record dropped an earlier node: %+v", got.Nodes)
	}
	if got.Gate.PausedAt != "" {
		t.Errorf("a limit pause must not touch the gate record, PausedAt = %q", got.Gate.PausedAt)
	}
	if s := rawSchema(t, path); s != Schema {
		t.Errorf("stamped schema %d, want %d — the record is additive (ADR 0031 §8.2)", s, Schema)
	}

	var block map[string]json.RawMessage
	if err := json.Unmarshal(snapshotKeys(t, path)["limit_pause"], &block); err != nil {
		t.Fatalf("limit_pause block: %v", err)
	}
	for _, key := range []string{"node_ids", "cause", "at"} {
		if _, ok := block[key]; !ok {
			t.Errorf("limit_pause has no %q key: %v", key, block)
		}
	}
	if len(block) != 3 {
		t.Errorf("limit_pause = %v, want exactly node_ids, cause and at", block)
	}
}

// TestRecordLimitPause_NoLimitWritesNoKey_358 (#358): a run that never hits a limit
// writes no limit_pause key, by Write or by the recorder, so its state.json is
// byte for byte what it was before the record existed.
func TestRecordLimitPause_NoLimitWritesNoKey_358(t *testing.T) {
	dir := t.TempDir()
	written := filepath.Join(dir, "written.json")
	if err := Write(written, sampleSnapshot()); err != nil {
		t.Fatal(err)
	}
	recorded := filepath.Join(dir, "recorded.json")
	rec := NewSnapshotRecorder(recorded, baseSnapshot("run-1"))
	if err := rec.RecordNode("a", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatal(err)
	}
	if err := rec.RecordPause("gate1"); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{written, recorded} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(raw), "limit_pause") {
			t.Errorf("%s: a run with no limit pause carries the key:\n%s", filepath.Base(path), raw)
		}
	}
}

// TestLoad_OlderSnapshotWithoutLimitPauseLoadsUnchanged_358 (#358): a schema-3
// snapshot written before the field existed loads with no record and every
// other field intact, and a leg seeded from it writes no key.
func TestLoad_OlderSnapshotWithoutLimitPauseLoadsUnchanged_358(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	older := `{
  "schema": 3,
  "run_id": "run-old",
  "runtime": "claude",
  "graph_source_path": "graphs/x.yaml",
  "graph_sha256": "deadbeef",
  "graph": {"name":"g","nodes":[{"id":"a","prompt":"a"},{"id":"gate1","type":"gate","depends_on":["a"]}]},
  "nodes": {"a": {"verdict": "PASS", "session_id": "s-a", "cost_usd": 0.5, "duration": 1000000000, "artifact_path": "/r/a.out"}},
  "gate": {"paused_at": "gate1", "decisions": {"gate1": "pause"}}
}`
	if err := os.WriteFile(path, []byte(older), 0o600); err != nil {
		t.Fatal(err)
	}

	snap, err := Load(path)
	if err != nil {
		t.Fatalf("load older snapshot: %v", err)
	}
	if snap.LimitPause != nil {
		t.Fatalf("an older snapshot loaded a limit pause: %+v", snap.LimitPause)
	}
	if snap.RunID != "run-old" || snap.Gate.PausedAt != "gate1" || snap.Gate.Decisions["gate1"] != GatePause ||
		snap.Nodes["a"].SessionID != "s-a" || snap.Nodes["a"].CostUSD != 0.5 {
		t.Fatalf("older snapshot did not load unchanged: %+v", snap)
	}

	resumed := filepath.Join(t.TempDir(), "state.json")
	if err := NewSnapshotRecorder(resumed, snap).RecordNode("gate1", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatal(err)
	}
	if _, ok := snapshotKeys(t, resumed)["limit_pause"]; ok {
		t.Fatal("a leg seeded from an older snapshot wrote a limit_pause key")
	}
	if s := rawSchema(t, resumed); s != Schema {
		t.Fatalf("stamped schema %d, want %d", s, Schema)
	}
}

// TestRecordLimitPause_BesideAGatePauseKeepsBoth_358 (#358): ADR 0031 §8.3 — a leg
// that pauses on a limit and at a gate persists both records, whichever is
// written first, because each write is the whole snapshot.
func TestRecordLimitPause_BesideAGatePauseKeepsBoth_358(t *testing.T) {
	limit := LimitPause{NodeIDs: []string{"a"}, Cause: claudeLimitCause, At: limitPauseAt}
	orders := map[string]func(*SnapshotRecorder) error{
		"limit then gate": func(r *SnapshotRecorder) error {
			if err := r.RecordLimitPause(limit); err != nil {
				return err
			}
			return r.RecordDescribedPause("gate1", "ship it?")
		},
		"gate then limit": func(r *SnapshotRecorder) error {
			if err := r.RecordDescribedPause("gate1", "ship it?"); err != nil {
				return err
			}
			return r.RecordLimitPause(limit)
		},
	}
	for name, write := range orders {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.json")
			if err := write(NewSnapshotRecorder(path, baseSnapshot("run-1"))); err != nil {
				t.Fatal(err)
			}
			got, err := Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if got.Gate.PausedAt != "gate1" || got.Gate.PausedGateDescription != "ship it?" {
				t.Errorf("gate record lost: %+v", got.Gate)
			}
			if got.LimitPause == nil || got.LimitPause.Cause != claudeLimitCause {
				t.Errorf("limit record lost: %+v", got.LimitPause)
			}
		})
	}
}
