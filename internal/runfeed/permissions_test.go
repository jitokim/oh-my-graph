package runfeed

import (
	"os"
	"path/filepath"
	"testing"
)

// TestNewStreamWriter_CreatesTheStreamOwnerOnly pins the at-rest mode of
// events.jsonl. The stream carries node results and session ids, and
// docs/RUN-FEED.md's "a consumer may tail this file" has always meant a
// consumer running as you — see SECURITY.md, "What is exposed at rest".
func TestNewStreamWriter_CreatesTheStreamOwnerOnly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-1")
	path := filepath.Join(runDir, "events.jsonl")

	w, err := NewStreamWriter(path, "run-1")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()

	dirInfo, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("run dir mode = %v, want 0700.", got)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat event stream: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("events.jsonl mode = %v, want 0600.", got)
	}
}

// TestNewStreamWriter_LeavesAnExistingStreamAlone is the compatibility half:
// O_CREATE's mode applies only when the file is created, and MkdirAll does not
// chmod an existing directory — so a resumed leg appends to a stream an older
// binary created without re-moding it or its run directory. Nothing about
// resume or serve changes for a run that predates this.
func TestNewStreamWriter_LeavesAnExistingStreamAlone(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "legacy-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("seed legacy run dir: %v", err)
	}
	path := filepath.Join(runDir, "events.jsonl")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatalf("seed legacy stream: %v", err)
	}
	// Both modes above are masked by the caller's umask, so on a machine with a
	// hardened umask the fixture would not be the 0755/0644 pair this test is
	// about — it would assert the developer's umask. chmod(2) is not masked.
	if err := os.Chmod(runDir, 0o755); err != nil {
		t.Fatalf("chmod legacy run dir: %v", err)
	}
	if err := os.Chmod(path, 0o644); err != nil {
		t.Fatalf("chmod legacy stream: %v", err)
	}

	w, err := NewStreamWriter(path, "legacy-run")
	if err != nil {
		t.Fatalf("NewStreamWriter: %v", err)
	}
	defer w.Close()

	dirInfo, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o755 {
		t.Errorf("legacy run dir mode = %v, want it untouched at 0755.", got)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat event stream: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o644 {
		t.Errorf("legacy events.jsonl mode = %v, want it untouched at 0644.", got)
	}
}
