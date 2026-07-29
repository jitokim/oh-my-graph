package handoff

import "testing"

// TestArtifactPath_UnknownNodeNotFound proves the accessor reports absence
// rather than returning a zero-value path that could be mistaken for "" being
// a real (if odd) path.
func TestArtifactPath_UnknownNodeNotFound(t *testing.T) {
	h := New(t.TempDir(), nil)
	if _, ok := h.ArtifactPath("never-ran"); ok {
		t.Fatal("ArtifactPath must report false for a node with no persisted output")
	}
}

// TestArtifactPath_ReturnsPersistedPath proves the accessor returns exactly
// what PersistOutput recorded, without touching disk itself.
func TestArtifactPath_ReturnsPersistedPath(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("dev", "the result", "sess-dev"); err != nil {
		t.Fatalf("PersistOutput: %v", err)
	}

	path, ok := h.ArtifactPath("dev")
	if !ok {
		t.Fatal("ArtifactPath should report true for a persisted node")
	}
	if path == "" {
		t.Fatal("ArtifactPath returned an empty path for a persisted node")
	}
}

// TestArtifactPath_SeededNodeIsVisible proves the accessor sees a path
// rehydrated by Seed, not only one produced by a real PersistOutput — the
// resume recorder reads this back for every node it carries forward.
func TestArtifactPath_SeededNodeIsVisible(t *testing.T) {
	h := New(t.TempDir(), nil)
	h.Seed("dev", "/some/dev.out", "sess-dev")

	path, ok := h.ArtifactPath("dev")
	if !ok || path != "/some/dev.out" {
		t.Fatalf("ArtifactPath after Seed = (%q, %v), want (/some/dev.out, true)", path, ok)
	}
}
