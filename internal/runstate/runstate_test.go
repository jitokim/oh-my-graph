package runstate

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

func strptr(s string) *string { return &s }

func TestAccountingRecords_OmitEmptyUsage(t *testing.T) {
	for name, value := range map[string]any{
		"node":     NodeRecord{},
		"snapshot": Snapshot{},
	} {
		encoded, err := json.Marshal(value)
		if err != nil {
			t.Fatalf("marshal %s: %v", name, err)
		}
		if strings.Contains(string(encoded), `"usage"`) || strings.Contains(string(encoded), `"planning_usage"`) {
			t.Fatalf("%s zero token usage must be absent, got %s", name, encoded)
		}
	}
}

// sampleSnapshot is a fully-populated snapshot exercising every field, including
// the load-bearing distinctions (a SettingSources pointer to "", a FAIL record, a
// gate paused with recorded decisions).
func sampleSnapshot() Snapshot {
	return Snapshot{
		RunID:           "run-123",
		Runtime:         "codex",
		GraphSourcePath: "graphs/dev-review-pr.yaml",
		GraphSHA256:     "abc123",
		Graph:           json.RawMessage(`{"name":"g","nodes":[{"id":"dev","prompt":"dev"}]}`),
		Inputs:          map[string]string{"repo": "/work/app"},
		ContinueOnFail:  true,
		ToolPolicies: map[string]NodeToolPolicy{
			"dev": {
				AllowedTools:    []string{"Read", "Bash(git *)"},
				DisallowedTools: []string{"Bash"},
				Tools:           []string{"Read", "Bash"},
				SettingSources:  strptr(""), // "load none" — must survive as non-nil ""
				StrictMCPConfig: true,
			},
		},
		Nodes: map[string]NodeRecord{
			"dev": {
				Verdict:      VerdictPass,
				SessionID:    "sess-dev",
				CostUSD:      0.12,
				BudgetUSD:    0.50,
				Duration:     1500 * time.Millisecond,
				ArtifactPath: ".oh-my-graph/runs/run-123/dev.out",
				Detail:       "under budget by $0.3800",
				CostUnknown:  true,
				Usage: TokenUsage{
					InputTokens: 11, CachedInputTokens: 3, OutputTokens: 2, ReasoningOutputTokens: 1,
				},
			},
			"e2e": {
				Verdict:   VerdictFail,
				SessionID: "sess-e2e",
				CostUSD:   0.03,
				Duration:  500 * time.Millisecond,
				Detail:    `node "e2e" failed success_check exit_zero: exit code 1`,
			},
		},
		Gate: GateState{
			PausedAt:  "approve",
			Decisions: map[string]GateDecision{"approve": GatePause},
		},
		BuildEvidence: &BuildEvidence{
			Answer:     "declared",
			DeclaredBy: "--accept-no-build-evidence",
			Signals:    []string{"gradlew", "package.json"},
		},
	}
}

// TestBuildEvidence_AbsentStaysAbsentAndSchemaHolds is the compatibility half of
// ADR 0030 §2.5a. A `run` of a hand-written graph never asks the build-evidence
// question, so it must write no block at all — an "answer" nobody was asked is
// worse than silence, because the four strata are meant to sum to the auto-mode
// launches and nothing else. And the field is additive, so schema stays 3: every
// existing state.json stays readable and unchanged in meaning.
func TestBuildEvidence_AbsentStaysAbsentAndSchemaHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	snap := sampleSnapshot()
	snap.BuildEvidence = nil

	if err := Write(path, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "build_evidence") {
		t.Errorf("a run that never asked the question recorded an answer:\n%s", raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.BuildEvidence != nil {
		t.Errorf("absent build_evidence loaded as %+v, want nil", got.BuildEvidence)
	}
	if got.Schema != 3 {
		t.Errorf("schema = %d, want 3 — an additive optional field is not a format change", got.Schema)
	}
}

// jsonEqual reports whether two JSON blobs are semantically equal, ignoring the
// whitespace that atomic pretty-printing introduces into a stored RawMessage.
func jsonEqual(t *testing.T, a, b json.RawMessage) bool {
	t.Helper()
	var av, bv any
	if err := json.Unmarshal(a, &av); err != nil {
		t.Fatalf("unmarshal a: %v", err)
	}
	if err := json.Unmarshal(b, &bv); err != nil {
		t.Fatalf("unmarshal b: %v", err)
	}
	return reflect.DeepEqual(av, bv)
}

// --- round-trip -------------------------------------------------------------

func TestWriteLoad_RoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	orig := sampleSnapshot()

	if err := Write(path, orig); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got.Schema != Schema {
		t.Fatalf("loaded schema = %d, want %d", got.Schema, Schema)
	}
	// The graph round-trips semantically (byte layout changes under indentation).
	if !jsonEqual(t, orig.Graph, got.Graph) {
		t.Fatalf("graph did not round-trip: %s vs %s", orig.Graph, got.Graph)
	}
	// Everything else must be exactly equal. Compare with Schema and Graph
	// normalized out of the way.
	want := orig
	want.Schema = Schema
	want.Graph = nil
	got.Graph = nil
	if !reflect.DeepEqual(want, got) {
		t.Fatalf("round-trip mismatch:\n want %+v\n got  %+v", want, got)
	}
}

// --- the runtime is always written -----------------------------------------

// TestWrite_UnsetRuntimeIsWrittenAsClaude is the write hole itself: a snapshot
// whose Runtime the caller never set must still land on disk NAMING its
// runtime, because a consumer reading a schema-3 file cannot tell "the writer
// forgot" from "this was a Claude run".
func TestWrite_UnsetRuntimeIsWrittenAsClaude(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	snap := sampleSnapshot()
	snap.Runtime = "" // a caller of the package, not the CLI's canonicalizing path

	if err := Write(path, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if !strings.Contains(string(raw), `"runtime": "claude"`) {
		t.Fatalf("snapshot on disk does not name its runtime:\n%s", raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Runtime != RuntimeClaude {
		t.Fatalf("loaded runtime = %q, want %q", got.Runtime, RuntimeClaude)
	}
}

// TestMarshalJSON_CanonicalizesRuntimeOffTheWritePath proves the guarantee is
// the type's and not Write's: a future writer that marshals a Snapshot by any
// other route gets the same canonicalized bytes.
func TestMarshalJSON_CanonicalizesRuntimeOffTheWritePath(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{RunID: "run-1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"runtime":"claude"`) {
		t.Fatalf("a direct marshal dropped the runtime: %s", encoded)
	}
}

// TestMarshalJSON_NamedRuntimeSurvives guards the other half: canonicalization
// must only fill an empty value, never overwrite a real one — a Codex run
// recorded as claude is exactly the misreading schema 3 exists to prevent.
func TestMarshalJSON_NamedRuntimeSurvives(t *testing.T) {
	encoded, err := json.Marshal(Snapshot{RunID: "run-1", Runtime: RuntimeCodex})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"runtime":"codex"`) {
		t.Fatalf("canonicalization overwrote a named runtime: %s", encoded)
	}
}

// TestMarshalJSON_DoesNotMutateCaller keeps the canonicalization local to the
// encoding: SnapshotRecorder marshals the same base snapshot after every node,
// and a marshaler that rewrote it would be changing a caller's value behind its
// back.
func TestMarshalJSON_DoesNotMutateCaller(t *testing.T) {
	snap := Snapshot{RunID: "run-1"}
	if _, err := json.Marshal(snap); err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if snap.Runtime != "" {
		t.Fatalf("marshaling rewrote the caller's snapshot: runtime = %q", snap.Runtime)
	}
}

// TestLoad_AbsentRuntimeStillReadsAsClaude is the backward-compatibility half:
// a schema-3 snapshot written by v0.8.0 with no `runtime` key must keep loading
// exactly as it does today — the read rule did not change, only the write side
// did. The empty value it decodes to is the absent case, and it still means
// claude; re-writing that snapshot now says so out loud.
func TestLoad_AbsentRuntimeStillReadsAsClaude(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := os.WriteFile(path, []byte(`{"schema":3,"run_id":"old-run","graph":{}}`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("an existing schema-3 snapshot must still load: %v", err)
	}
	if got.Runtime != "" {
		t.Fatalf("Load must not rewrite an absent runtime, got %q", got.Runtime)
	}

	// Carried forward by a resumed leg, the same run is now explicit — same
	// meaning, said rather than assumed.
	rewritten := filepath.Join(dir, "resumed.json")
	if err := Write(rewritten, got); err != nil {
		t.Fatalf("write carried-forward snapshot: %v", err)
	}
	reloaded, err := Load(rewritten)
	if err != nil {
		t.Fatalf("load carried-forward snapshot: %v", err)
	}
	if reloaded.Runtime != RuntimeClaude {
		t.Fatalf("carried-forward runtime = %q, want %q", reloaded.Runtime, RuntimeClaude)
	}
}

func TestWriteLoad_SettingSourcesEmptyPointerSurvives(t *testing.T) {
	// The pointer distinction is the Layer-1 isolation switch: nil = flag omitted,
	// &"" = "load no settings". A round-trip must not collapse &"" into nil.
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, sampleSnapshot()); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	ss := got.ToolPolicies["dev"].SettingSources
	if ss == nil {
		t.Fatal("SettingSources collapsed to nil; the &\"\" isolation switch was lost")
	}
	if *ss != "" {
		t.Fatalf("SettingSources = %q, want empty string", *ss)
	}
}

func TestCompletedNodes_OnlyPassingNodes(t *testing.T) {
	done := sampleSnapshot().CompletedNodes()
	if !done["dev"] {
		t.Fatal("a PASS node must count as completed")
	}
	if done["e2e"] {
		t.Fatal("a FAIL node must NOT count as completed (its dependents stay blocked)")
	}
	if done == nil {
		t.Fatal("CompletedNodes must never be nil")
	}
}

// TestSettledNodes_PassAndFail proves SettledNodes' whole reason to exist: it
// includes a FAIL alongside a PASS, unlike CompletedNodes — the set resume
// uses to stop a failed node itself from relaunching, without letting that
// failure satisfy its dependents (see Scheduler.Options.SettledNodes).
func TestSettledNodes_PassAndFail(t *testing.T) {
	settled := sampleSnapshot().SettledNodes()
	if !settled["dev"] {
		t.Fatal("a PASS node must count as settled")
	}
	if !settled["e2e"] {
		t.Fatal("a FAIL node must count as settled — it must not be launched again")
	}
	if settled == nil {
		t.Fatal("SettledNodes must never be nil")
	}
}

// TestSettledNodes_EmptySnapshot proves SettledNodes degrades cleanly (an
// empty, non-nil map) for a snapshot with no node records yet, the same shape
// CompletedNodes already guarantees.
func TestSettledNodes_EmptySnapshot(t *testing.T) {
	settled := Snapshot{}.SettledNodes()
	if settled == nil {
		t.Fatal("SettledNodes must never be nil, even for an empty snapshot")
	}
	if len(settled) != 0 {
		t.Fatalf("expected no settled nodes, got %v", settled)
	}
}

// --- schema-version mismatch is refused, not misread ------------------------

func TestLoad_SchemaMismatchRejected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	// A structurally valid snapshot from an incompatible future format version.
	if err := os.WriteFile(path, []byte(`{"schema":999,"run_id":"x","graph":{}}`), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	_, err := Load(path)
	var mErr *SchemaMismatchError
	if !errors.As(err, &mErr) {
		t.Fatalf("expected *SchemaMismatchError, got %T: %v", err, err)
	}
	if mErr.Found != 999 || mErr.Want != Schema {
		t.Fatalf("mismatch error carried wrong versions: %+v", mErr)
	}
}

func TestLoad_MissingFile(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "does-not-exist.json"))
	if err == nil {
		t.Fatal("expected an error loading a missing snapshot")
	}
	// It must not be masquerading as a schema mismatch.
	var mErr *SchemaMismatchError
	if errors.As(err, &mErr) {
		t.Fatalf("missing file reported as schema mismatch: %v", err)
	}
}

func TestLoad_MalformedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{ not valid json"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("expected a decode error for malformed JSON")
	}
}

// --- atomicity: a failed write never corrupts a good snapshot ---------------

func TestWrite_FailedEncodeDoesNotCorruptExisting(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")

	good := sampleSnapshot()
	if err := Write(path, good); err != nil {
		t.Fatalf("write good snapshot: %v", err)
	}

	// A snapshot whose Graph holds invalid JSON fails to marshal. Because Write
	// marshals BEFORE touching the destination, the existing good file must be
	// left completely intact.
	broken := sampleSnapshot()
	broken.Graph = json.RawMessage("{ this is not valid json")
	if err := Write(path, broken); err == nil {
		t.Fatal("expected Write to fail encoding an invalid graph")
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("good snapshot was corrupted by a failed write: %v", err)
	}
	if !jsonEqual(t, good.Graph, got.Graph) || got.RunID != good.RunID {
		t.Fatalf("failed write mutated the good snapshot: %+v", got)
	}
}

func TestWrite_NoLeftoverTempFiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	if err := Write(path, sampleSnapshot()); err != nil {
		t.Fatalf("write: %v", err)
	}
	// The temp file must have been renamed away, not left beside the final file.
	leftovers, err := filepath.Glob(filepath.Join(dir, "state.json.tmp-*"))
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	if len(leftovers) != 0 {
		t.Fatalf("temp files left behind: %v", leftovers)
	}
}

func TestWrite_CreatesRunDirectory(t *testing.T) {
	// Write must create .oh-my-graph/runs/<run-id>/ if it does not exist yet.
	nested := filepath.Join(t.TempDir(), ".oh-my-graph", "runs", "run-123", "state.json")
	if err := Write(nested, sampleSnapshot()); err != nil {
		t.Fatalf("write into a missing directory tree: %v", err)
	}
	if _, err := Load(nested); err != nil {
		t.Fatalf("load from the created tree: %v", err)
	}
}
