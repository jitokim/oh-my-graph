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

func intPtr(n int) *int { return &n }

// TestVerificationRecord_RoundTrips (#332): the engine's verify record
// survives Write/Load field for field, the exit code included, and an absent
// exit code stays absent rather than coming back as 0.
func TestVerificationRecord_RoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	passed := &VerificationRecord{
		Command:          "go test ./...",
		ExitCode:         intPtr(0),
		ExpectedExitCode: 0,
		Duration:         1500 * time.Millisecond,
		Status:           VerificationPassed,
		OutputTail:       "ok  \tgithub.com/x/y\t0.1s\n",
	}
	timedOut := &VerificationRecord{
		Command:          "make e2e",
		ExpectedExitCode: 3,
		Duration:         5 * time.Minute,
		Status:           VerificationTimedOut,
		OutputTail:       "still waiting",
	}
	snap := sampleSnapshot()
	snap.Nodes = map[string]NodeRecord{
		"build": {Verdict: VerdictPass, Verification: passed},
		"e2e":   {Verdict: VerdictFail, Judged: false, Verification: timedOut},
	}
	if err := Write(path, snap); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got.Nodes["build"].Verification, passed) {
		t.Errorf("passed record = %+v, want %+v", got.Nodes["build"].Verification, passed)
	}
	if !reflect.DeepEqual(got.Nodes["e2e"].Verification, timedOut) {
		t.Errorf("timed-out record = %+v, want %+v", got.Nodes["e2e"].Verification, timedOut)
	}
	if got.Nodes["e2e"].Verification.ExitCode != nil {
		t.Errorf("an absent exit code loaded as %d, want nil", *got.Nodes["e2e"].Verification.ExitCode)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	for _, key := range []string{`"verification"`, `"command"`, `"exit_code"`, `"expected_exit_code"`, `"output_tail"`, `"status": "timed out"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("state.json lacks %s:\n%s", key, raw)
		}
	}
	// Exactly one exit_code key: the passed node's. The timed-out one must not
	// write a 0 or a -1 that a reader could take for an exit.
	if n := strings.Count(string(raw), `"exit_code"`); n != 1 {
		t.Errorf("exit_code written %d times, want 1 (the timed-out record must omit it):\n%s", n, raw)
	}
}

// TestVerificationRecord_ZeroExitIsWritten (#332): exit code 0 is the pass
// case, and a pointer to 0 must be written — omitempty on a plain int would
// drop exactly the value a PASS is evidenced by.
func TestVerificationRecord_ZeroExitIsWritten(t *testing.T) {
	encoded, err := json.Marshal(VerificationRecord{Command: "true", ExitCode: intPtr(0), Status: VerificationPassed})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if !strings.Contains(string(encoded), `"exit_code":0`) {
		t.Fatalf("exit_code 0 was not written: %s", encoded)
	}
}

// TestVerification_AbsentStaysAbsentAndSchemaHolds (#332): a node with no
// verify writes no verification key, so a run without one writes the same
// bytes it did before the field existed, and the schema stamp does not move.
func TestVerification_AbsentStaysAbsentAndSchemaHolds(t *testing.T) {
	encoded, err := json.Marshal(NodeRecord{Verdict: VerdictPass, Provenance: "exit-only"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), "verification") {
		t.Fatalf("a record without a verify grew a verification key: %s", encoded)
	}

	path := filepath.Join(t.TempDir(), "state.json")
	if err := Write(path, sampleSnapshot()); err != nil {
		t.Fatalf("write: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if strings.Contains(string(raw), "verification") {
		t.Errorf("a run without a verify recorded one:\n%s", raw)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if got.Schema != Schema || Schema != 3 {
		t.Errorf("schema = %d (const %d), want 3 — an additive optional field is not a format change", got.Schema, Schema)
	}
	for id, rec := range got.Nodes {
		if rec.Verification != nil {
			t.Errorf("node %q loaded a verification it never had: %+v", id, rec.Verification)
		}
	}
}

// TestSnapshotRecorder_ResumeCarriesVerificationForward (#332): a resumed
// leg seeds its recorder with the earlier leg's records; settling a
// different node must rewrite the file with the earlier node's verification
// unchanged, and must not invent one for the new node.
func TestSnapshotRecorder_ResumeCarriesVerificationForward(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	earlier := &VerificationRecord{
		Command:    "go test ./...",
		ExitCode:   intPtr(0),
		Duration:   time.Second,
		Status:     VerificationPassed,
		OutputTail: "PASS\n",
	}
	base := sampleSnapshot()
	base.Nodes = map[string]NodeRecord{"build": {Verdict: VerdictPass, Verification: earlier}}
	rec := NewSnapshotRecorder(path, base)
	if err := rec.RecordNode("deploy", NodeRecord{Verdict: VerdictPass}); err != nil {
		t.Fatalf("record: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !reflect.DeepEqual(got.Nodes["build"].Verification, earlier) {
		t.Errorf("earlier leg's verification = %+v, want it carried unchanged: %+v", got.Nodes["build"].Verification, earlier)
	}
	if got.Nodes["deploy"].Verification != nil {
		t.Errorf("the verify-free node grew a verification: %+v", got.Nodes["deploy"].Verification)
	}
}
