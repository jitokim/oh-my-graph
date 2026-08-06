package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

// --- at-rest permissions ----------------------------------------------------
//
// A node's artifact is its full reply, and with `| inline` it is the text that
// goes into a downstream node's prompt. It is written owner-only, the same
// stance cmd/oh-my-graph's saveGeneratedSpec takes for a saved plan — see
// SECURITY.md, "What is exposed at rest". These pin the modes so a later edit
// has to be a deliberate widening.

func TestPersistOutput_WritesOwnerOnly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-1")
	h := New(runDir, nil)

	if err := h.PersistOutput("build", "the model's full reply", "sess-1"); err != nil {
		t.Fatalf("PersistOutput: %v", err)
	}

	dirInfo, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("run dir mode = %v, want 0700 — a run directory holds every node's "+
			"prompt and reply; a co-tenant must not be able to walk into it.", got)
	}

	fileInfo, err := os.Stat(filepath.Join(runDir, "build.out"))
	if err != nil {
		t.Fatalf("stat artifact: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("artifact mode = %v, want 0600.", got)
	}
}

// TestPersistOutput_LeavesAnExistingRunDirAlone is the compatibility half: a
// run directory created by an older binary is 0755, and MkdirAll does not chmod
// a directory that already exists. So resume and serve keep reading a run that
// predates this change, and only fresh runs get the narrower mode.
func TestPersistOutput_LeavesAnExistingRunDirAlone(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "legacy-run")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("seed legacy run dir: %v", err)
	}

	if err := New(runDir, nil).PersistOutput("build", "reply", ""); err != nil {
		t.Fatalf("PersistOutput: %v", err)
	}

	info, err := os.Stat(runDir)
	if err != nil {
		t.Fatalf("stat run dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("legacy run dir mode = %v, want it untouched at 0755; MkdirAll must not "+
			"re-mode a directory that already exists.", got)
	}
}

// TestSetFeedback_WritesOwnerOnly covers the payload path, which gets its mode
// from os.CreateTemp (0600) carried over by the rename rather than from an
// explicit argument — so it is worth asserting rather than assuming.
func TestSetFeedback_WritesOwnerOnly(t *testing.T) {
	runDir := filepath.Join(t.TempDir(), "run-1")
	h := New(runDir, nil)

	if err := h.SetFeedback("review", "what went wrong"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}

	dirInfo, err := os.Stat(filepath.Join(runDir, "feedback"))
	if err != nil {
		t.Fatalf("stat feedback dir: %v", err)
	}
	if got := dirInfo.Mode().Perm(); got != 0o700 {
		t.Errorf("feedback dir mode = %v, want 0700.", got)
	}

	fileInfo, err := os.Stat(filepath.Join(runDir, "feedback", "review.out"))
	if err != nil {
		t.Fatalf("stat feedback payload: %v", err)
	}
	if got := fileInfo.Mode().Perm(); got != 0o600 {
		t.Errorf("feedback payload mode = %v, want 0600.", got)
	}
}
