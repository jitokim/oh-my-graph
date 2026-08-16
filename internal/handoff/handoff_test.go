package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// --- interpolation: inputs --------------------------------------------------

func TestInterpolate_Input(t *testing.T) {
	h := New(t.TempDir(), map[string]string{"repo": "/work/app"})
	got, err := h.Interpolate("cd {{ inputs.repo }} && build")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "cd /work/app && build" {
		t.Fatalf("interpolated = %q", got)
	}
}

func TestInterpolate_MissingInput(t *testing.T) {
	h := New(t.TempDir(), nil)
	_, err := h.Interpolate("{{ inputs.nope }}")
	var iErr *InterpolationError
	if !errors.As(err, &iErr) {
		t.Fatalf("expected *InterpolationError, got %T: %v", err, err)
	}
	if iErr.Reference != "nope" || iErr.Kind != "inputs" {
		t.Fatalf("error identified the wrong reference: %+v", iErr)
	}
}

// --- interpolation: artifacts ----------------------------------------------

func TestInterpolate_ArtifactPathByDefault(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("writer", "hello world", "s-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, err := h.Interpolate("read {{ artifacts.writer }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	wantPath := filepath.Join(dir, "writer.out")
	if got != "read "+wantPath {
		t.Fatalf("expected the artifact PATH, got %q", got)
	}
}

func TestInterpolate_ArtifactInlineFilter(t *testing.T) {
	h := New(t.TempDir(), nil)
	if err := h.PersistOutput("writer", "the content", "s-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}

	got, err := h.Interpolate("critique: {{ artifacts.writer | inline }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "critique: the content" {
		t.Fatalf("inline filter did not inline content: %q", got)
	}
}

// TestInterpolate_NamespacedArtifactAndFeedbackResolve is the runtime half of
// ADR 0027's charset change. A spliced prompt says
// {{ artifacts.qa-a/impl | inline }} and {{ feedback.qa-a/review }}; if the
// reference class did not admit '/', neither token would match, and the
// runtime would pass BOTH through verbatim into a paid prompt — the exact
// silent-verbatim failure the load-time/run-time token split exists to abolish.
func TestInterpolate_NamespacedArtifactAndFeedbackResolve(t *testing.T) {
	h := New(t.TempDir(), nil)
	if err := h.PersistOutput("qa-a/impl", "the implementation", "s-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if err := h.SetFeedback("qa-a/review", "findings: fix the thing"); err != nil {
		t.Fatalf("set feedback: %v", err)
	}

	got, err := h.Interpolate("did {{ artifacts.qa-a/impl | inline }} / said {{ feedback.qa-a/review }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "did the implementation / said findings: fix the thing" {
		t.Fatalf("namespaced tokens did not resolve: %q", got)
	}
}

func TestInterpolate_ArtifactNotYetAvailable(t *testing.T) {
	h := New(t.TempDir(), nil)
	_, err := h.Interpolate("{{ artifacts.pending }}")
	var iErr *InterpolationError
	if !errors.As(err, &iErr) {
		t.Fatalf("expected *InterpolationError, got %T: %v", err, err)
	}
	if iErr.Kind != "artifacts" || iErr.Reference != "pending" {
		t.Fatalf("wrong reference in error: %+v", iErr)
	}
}

func TestPersistOutput_WritesFile(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("node1", "RESULT-BODY", "sess"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "node1.out"))
	if err != nil {
		t.Fatalf("read persisted file: %v", err)
	}
	if string(data) != "RESULT-BODY" {
		t.Fatalf("persisted content = %q", string(data))
	}
}

// TestPersistOutput_SlashInNodeIDCannotEscapeRunDir pins the sanitization to
// the '/' form specifically: '/' is a path separator on Windows too, so an id
// like "../escape" must land inside the run directory on every OS, not only
// where '/' happens to equal os.PathSeparator.
func TestPersistOutput_SlashInNodeIDCannotEscapeRunDir(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)
	if err := h.PersistOutput("../escape", "BODY", "sess"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if _, err := os.ReadFile(filepath.Join(dir, "..~escape.out")); err != nil {
		t.Fatalf("sanitized artifact should live inside the run dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(dir), "escape.out")); !os.IsNotExist(err) {
		t.Fatalf("artifact escaped the run directory (stat err = %v)", err)
	}
}

// --- placeholder detection --------------------------------------------------

// ContainsPlaceholder is the contract callers outside this package lean on to
// prove text is template-inert (the coordinator's skill-inlining neutralizer
// is judged by it). It must answer for exactly what Interpolate would resolve:
// a live placeholder with or without the inline filter, nothing for text a
// neutralizing pass has already broken, and nothing for a namespace this
// engine does not own.
func TestContainsPlaceholder(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{"input", "cd {{ inputs.repo }}", true},
		{"artifact with inline filter", "{{ artifacts.a | inline }}", true},
		{"feedback", "see {{ feedback.reviewer }}", true},
		{"no whitespace", "{{artifacts.a}}", true},
		{"neutralized", "{ { artifacts.a }}", false},
		{"unknown namespace", "{{ other.a }}", false},
		{"unopened", "artifacts.a }}", false},
		{"plain prose", "no placeholders here", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ContainsPlaceholder(tc.in); got != tc.want {
				t.Errorf("ContainsPlaceholder(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// --- session resolution -----------------------------------------------------

func TestResumeSessionFor_ArtifactNodeReturnsEmpty(t *testing.T) {
	h := New(t.TempDir(), nil)
	node := graph.Node{ID: "n", Handoff: graph.HandoffArtifact, DependsOn: []string{"p"}}
	session, err := h.ResumeSessionFor(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session != "" {
		t.Fatalf("artifact node should resume nothing, got %q", session)
	}
}

func TestResumeSessionFor_SessionNodeResolvesParent(t *testing.T) {
	h := New(t.TempDir(), nil)
	if err := h.PersistOutput("dev", "done", "sess-dev"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	node := graph.Node{ID: "e2e", Handoff: graph.HandoffSession, DependsOn: []string{"dev"}}

	session, err := h.ResumeSessionFor(node)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if session != "sess-dev" {
		t.Fatalf("resolved session = %q, want sess-dev", session)
	}
}

func TestResumeSessionFor_ParentSessionMissing(t *testing.T) {
	h := New(t.TempDir(), nil)
	node := graph.Node{ID: "e2e", Handoff: graph.HandoffSession, DependsOn: []string{"dev"}}
	_, err := h.ResumeSessionFor(node)
	if err == nil {
		t.Fatal("expected an error when the parent session was never recorded")
	}
	if !strings.Contains(err.Error(), "dev") {
		t.Fatalf("error should name the parent: %v", err)
	}
}
