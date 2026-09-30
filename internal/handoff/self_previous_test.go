package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// TestInterpolateFor_SelfPreviousEmptyOnFirstRound pins the first-pass
// contract (#288): before any arc has fired, {{ self.previous }} resolves to
// the EMPTY string — never an error — exactly like an unfired feedback token.
func TestInterpolateFor_SelfPreviousEmptyOnFirstRound(t *testing.T) {
	h := New(t.TempDir(), nil)

	got, err := h.InterpolateFor("review", "prior:{{ self.previous }}:end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "prior::end" {
		t.Fatalf("first-round self.previous did not resolve empty: %q", got)
	}
}

// TestArchiveRound_ReRunReadsItsOwnPreviousReply covers the re-run: after an
// arc fires, the declarer reads the reply it was handed in, a passed body node
// reads its own artifact — each node ITS OWN reply, never another's — and both
// are persisted under previous/ so a mid-loop resume can re-seed them.
func TestArchiveRound_ReRunReadsItsOwnPreviousReply(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("impl", "draft-v1", "s-impl"); err != nil {
		t.Fatalf("PersistOutput: %v", err)
	}

	if err := h.ArchiveRound("review", "FINDINGS: rename the flag", []string{"impl", "review"}); err != nil {
		t.Fatalf("ArchiveRound: %v", err)
	}

	for node, want := range map[string]string{"review": "FINDINGS: rename the flag", "impl": "draft-v1"} {
		got, err := h.InterpolateFor(node, "{{ self.previous }}")
		if err != nil {
			t.Fatalf("%s: unexpected error: %v", node, err)
		}
		if got != want {
			t.Errorf("%s: self.previous = %q, want %q", node, got, want)
		}
		onDisk, err := os.ReadFile(filepath.Join(dir, "previous", node+".out"))
		if err != nil {
			t.Fatalf("%s: previous-round reply not persisted: %v", node, err)
		}
		if string(onDisk) != want {
			t.Errorf("%s: persisted reply = %q, want %q", node, onDisk, want)
		}
	}

	// The declarer's archived reply did not pass, so it must not become an
	// artifact: {{ artifacts.review }} still means "review passed".
	if _, ok := h.ArtifactPath("review"); ok {
		t.Error("ArchiveRound registered an artifact for the failed declarer")
	}
}

// TestSeedPrevious_ResumeRehydratesAndMissingIsNoOp mirrors SeedFeedback: the
// file ArchiveRound wrote is re-read by a new process, and a node that never
// had a round archived stays at the empty first-round default.
func TestSeedPrevious_ResumeRehydratesAndMissingIsNoOp(t *testing.T) {
	dir := t.TempDir()
	if err := New(dir, nil).ArchiveRound("review", "FINDINGS: one", []string{"review"}); err != nil {
		t.Fatalf("ArchiveRound: %v", err)
	}

	resumed := New(dir, nil)
	for _, node := range []string{"review", "never-looped"} {
		if err := resumed.SeedPrevious(node); err != nil {
			t.Fatalf("SeedPrevious(%s): %v", node, err)
		}
	}
	if got, _ := resumed.InterpolateFor("review", "{{ self.previous }}"); got != "FINDINGS: one" {
		t.Errorf("resumed review self.previous = %q, want the archived reply", got)
	}
	if got, _ := resumed.InterpolateFor("never-looped", "{{ self.previous }}"); got != "" {
		t.Errorf("unarchived node self.previous = %q, want empty", got)
	}
}

// TestInterpolate_SelfRefusals: the runtime refuses what it cannot resolve
// rather than shipping it verbatim or silently empty — an unknown self
// reference, a filter, and a self token with no node to be self.
func TestInterpolate_SelfRefusals(t *testing.T) {
	h := New(t.TempDir(), nil)
	for _, tc := range []struct {
		name, nodeID, tmpl string
	}{
		{"unknown reference", "review", "{{ self.last }}"},
		{"filter", "review", "{{ self.previous | inline }}"},
		{"no node", "", "{{ self.previous }}"},
	} {
		_, err := h.InterpolateFor(tc.nodeID, tc.tmpl)
		var interp *InterpolationError
		if !errors.As(err, &interp) || interp.Kind != "self" {
			t.Errorf("%s: error = %v, want a self *InterpolationError", tc.name, err)
		}
	}
	if _, err := h.Interpolate("{{ self.previous }}"); err == nil {
		t.Error("Interpolate (no node) resolved a self token; want an error")
	}
}
