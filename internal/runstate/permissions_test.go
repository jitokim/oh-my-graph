package runstate

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// --- at-rest permissions ----------------------------------------------------
//
// A snapshot carries every node's prompt (Graph, verbatim) and the values
// interpolated into them (Inputs), so state.json is the single most sensitive
// file in a run directory. It has always landed at 0600 — os.CreateTemp's mode,
// carried over by the rename — but the directory around it was 0755, which left
// the rest of the run readable. Both are pinned here; see SECURITY.md, "What is
// exposed at rest".

func TestWrite_SnapshotAndRunDirAreOwnerOnly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-1")
	path := filepath.Join(runDir, "state.json")

	if err := Write(path, Snapshot{RunID: "run-1", Graph: json.RawMessage(`{"nodes":[]}`)}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dirInfo, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("run dir mode = %v, want 0700.", got)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat snapshot: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("state.json mode = %v, want 0600 — it holds every node's prompt.", got)
	}
}

// TestAcquireLock_CreatesTheRunDirOwnerOnly matters more than the lock file's
// own mode: the lock is usually the FIRST thing written into a fresh run
// directory, so its MkdirAll is what decides that directory's mode for
// everything the run writes afterwards.
func TestAcquireLock_CreatesTheRunDirOwnerOnly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-1")
	path := filepath.Join(runDir, LockFileName)

	release, err := AcquireLock(path)
	if err != nil {
		t.Fatalf("AcquireLock: %v", err)
	}
	defer release()

	dirInfo, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("run dir mode = %v, want 0700 — the lock creates the directory every "+
			"later write inherits.", got)
	}

	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat lock: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("resume.lock mode = %v, want 0600.", got)
	}
}

// TestWrite_LeavesAnExistingRunDirAlone is the compatibility half: a run
// directory from an older binary keeps its 0755, because MkdirAll does not
// chmod a directory that already exists. That is what makes resume and serve
// keep working across this change.
func TestWrite_LeavesAnExistingRunDirAlone(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "legacy-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("seed legacy run dir: %v", err)
	}
	// MkdirAll's mode is masked by the caller's umask, so on a machine with a
	// hardened umask the fixture would not be the 0755 this test is about — it
	// would assert the developer's umask. chmod(2) is not masked.
	if err := os.Chmod(runDir, 0o755); err != nil {
		t.Fatalf("chmod legacy run dir: %v", err)
	}

	if err := Write(filepath.Join(runDir, "state.json"), Snapshot{RunID: "legacy-run"}); err != nil {
		t.Fatalf("Write: %v", err)
	}

	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("legacy run dir mode = %v, want it untouched at 0755.", got)
	}
}
