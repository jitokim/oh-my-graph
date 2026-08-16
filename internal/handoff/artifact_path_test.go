package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSanitizeNodeID_IsInjectiveOverTheIDsTheLoaderAdmits is the property ADR
// 0027 leans on and the reason the replacement character is '~' rather than
// the '_' this function used to write. Once a splice mints `<using>/<internal>`
// ids, the map is no longer the identity, and a NON-injective one would point
// two distinct nodes at one artifact file: distinct ids pass
// validateNodesUnique, the in-memory artifact map is fine, and the collision is
// invisible until one node's `| inline` reads another node's reply out of a
// file that got overwritten.
//
// The three ids below are exactly that collision under '_': they all sanitize
// to a_b_c. Under '~' they stay three files, because '~' is outside
// nodeIDPattern's character class and so cannot appear in any authored segment.
func TestSanitizeNodeID_IsInjectiveOverTheIDsTheLoaderAdmits(t *testing.T) {
	seen := make(map[string]string)
	for _, id := range []string{"a/b_c", "a_b/c", "a_b_c", "a/b.c", "a.b/c", "qa-a/impl", "qa-a_impl"} {
		name := SanitizeNodeID(id)
		if other, clash := seen[name]; clash {
			t.Errorf("node ids %q and %q both sanitize to %q — two nodes, one artifact file", other, id, name)
		}
		seen[name] = id
	}
}

// TestSanitizeNodeID_IsTheIdentityOnEveryPreNamespaceID is the compatibility
// half: no id that could exist before ADR 0027 spells differently on disk, so
// no artifact file moves, no resume is disturbed, and no consumer breaks.
func TestSanitizeNodeID_IsTheIdentityOnEveryPreNamespaceID(t *testing.T) {
	for _, id := range []string{"dev", "review-a", "Feature_2.x", "lane-1"} {
		if got := SanitizeNodeID(id); got != id {
			t.Errorf("SanitizeNodeID(%q) = %q, want it unchanged", id, got)
		}
	}
}

// TestPersistOutput_NamespacedNodesGetDistinctFiles is the same property one
// layer up, through the writer the run actually uses: two spliced nodes whose
// ids collide under the old '_' spelling must leave two readable files with
// their own contents.
func TestPersistOutput_NamespacedNodesGetDistinctFiles(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	for id, body := range map[string]string{"a/b_c": "FIRST", "a_b/c": "SECOND", "a_b_c": "THIRD"} {
		if err := h.PersistOutput(id, body, "sess"); err != nil {
			t.Fatalf("persist %q: %v", id, err)
		}
	}
	for id, want := range map[string]string{"a/b_c": "FIRST", "a_b/c": "SECOND", "a_b_c": "THIRD"} {
		path, ok := h.ArtifactPath(id)
		if !ok {
			t.Fatalf("no artifact path recorded for %q", id)
		}
		if filepath.Dir(path) != dir {
			t.Errorf("artifact for %q escaped the run dir: %q", id, path)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read artifact for %q: %v", id, err)
		}
		if string(data) != want {
			t.Errorf("artifact for %q = %q, want %q — a collision overwrote it", id, data, want)
		}
	}
}

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
