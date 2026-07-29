package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// --- Seed rehydrates without touching disk ----------------------------------

func TestSeed_DoesNotCreateArtifactFile(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	missing := filepath.Join(dir, "dev.out") // deliberately not created

	h.Seed("dev", missing, "sess-dev")

	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatalf("Seed must not write a file; stat err = %v", err)
	}
	// The recorded path is still resolvable by default (path substitution reads
	// nothing), exactly as a normal run's artifact reference would be.
	got, err := h.Interpolate("read {{ artifacts.dev }}")
	if err != nil {
		t.Fatalf("path interpolation after Seed: %v", err)
	}
	if got != "read "+missing {
		t.Fatalf("interpolated = %q, want the seeded path", got)
	}
}

func TestSeed_InlineMissingFileStillErrors(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	// Seed a path whose file does not exist, then force an inline read: Handoff
	// stays ignorant of snapshots and raises the same InterpolationError a normal
	// run would when the .out is unreadable.
	h.Seed("dev", filepath.Join(dir, "nope.out"), "sess")

	_, err := h.Interpolate("{{ artifacts.dev | inline }}")
	var iErr *InterpolationError
	if !errors.As(err, &iErr) {
		t.Fatalf("expected *InterpolationError, got %T: %v", err, err)
	}
	if iErr.Kind != "artifacts" || iErr.Reference != "dev" {
		t.Fatalf("wrong reference in error: %+v", iErr)
	}
}

// --- Seed makes a resumed leg behave like the artifact/session already ran ---

func TestSeed_ArtifactInlineResolvesExistingFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "dev.out")
	if err := os.WriteFile(path, []byte("prior leg output"), 0o644); err != nil {
		t.Fatalf("prepare prior artifact: %v", err)
	}
	h := New(dir, nil)
	h.Seed("dev", path, "sess-dev")

	got, err := h.Interpolate("critique: {{ artifacts.dev | inline }}")
	if err != nil {
		t.Fatalf("inline interpolation after Seed: %v", err)
	}
	if got != "critique: prior leg output" {
		t.Fatalf("inline did not read the seeded file: %q", got)
	}
}

func TestSeed_SessionChildResumesSeededParent(t *testing.T) {
	h := New(t.TempDir(), nil)
	h.Seed("dev", filepath.Join(t.TempDir(), "dev.out"), "sess-dev")

	child := graph.Node{ID: "e2e", Handoff: graph.HandoffSession, DependsOn: []string{"dev"}}
	session, err := h.ResumeSessionFor(child)
	if err != nil {
		t.Fatalf("resume after Seed: %v", err)
	}
	if session != "sess-dev" {
		t.Fatalf("resumed session = %q, want sess-dev", session)
	}
}

func TestSeed_UnknownArtifactStillUnresolved(t *testing.T) {
	h := New(t.TempDir(), nil)
	h.Seed("dev", "/some/dev.out", "sess-dev")
	// A reference to a node that was never seeded is still an error — Seed adds
	// only the ids it was given, it does not make every reference resolve.
	_, err := h.Interpolate("{{ artifacts.other }}")
	var iErr *InterpolationError
	if !errors.As(err, &iErr) {
		t.Fatalf("expected *InterpolationError for unseeded ref, got %T: %v", err, err)
	}
}
