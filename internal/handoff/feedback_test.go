package handoff

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestInterpolate_FeedbackDefaultsToEmpty pins the iteration-1 contract: a
// {{ feedback.<id> }} with no round fired resolves to the EMPTY string —
// never an InterpolationError — because "not there yet" is the namespace's
// documented first-pass state, not a wiring bug (ADR 0010).
func TestInterpolate_FeedbackDefaultsToEmpty(t *testing.T) {
	h := New(t.TempDir(), nil)

	got, err := h.Interpolate("feedback follows:{{ feedback.review }}:end")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "feedback follows::end" {
		t.Fatalf("unfired feedback did not resolve empty: %q", got)
	}
}

// TestSetFeedback_InlinesAndPersists covers the fired arc: the payload
// inlines (never a path), and the internal <id>.feedback.out file holds the
// same bytes so a mid-loop resume can re-seed it.
func TestSetFeedback_InlinesAndPersists(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, nil)

	if err := h.SetFeedback("review", "round 1 findings: rename the flag"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}

	got, err := h.Interpolate("{{ feedback.review }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "round 1 findings: rename the flag" {
		t.Fatalf("feedback did not inline the payload: %q", got)
	}

	onDisk, err := os.ReadFile(filepath.Join(dir, "review.feedback.out"))
	if err != nil {
		t.Fatalf("payload file not persisted: %v", err)
	}
	if string(onDisk) != "round 1 findings: rename the flag" {
		t.Fatalf("persisted payload differs from the inlined one: %q", onDisk)
	}
}

// TestSetFeedback_LatestRoundWins: the payload file and the resolved value
// are overwritten per round — the re-run always reads the round that just
// judged it, never an older one.
func TestSetFeedback_LatestRoundWins(t *testing.T) {
	h := New(t.TempDir(), nil)

	if err := h.SetFeedback("review", "round 1"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}
	if err := h.SetFeedback("review", "round 2"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}

	got, err := h.Interpolate("{{ feedback.review }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "round 2" {
		t.Fatalf("latest payload did not win: %q", got)
	}
}

// TestInterpolate_FeedbackRejectsFilter: the namespace has no path form and
// no filter (ADR 0010) — graph.Validate refuses it at load, and this is the
// runtime backstop for a hand-built template that never went through
// validation. The failure is loud, not a silent pass-through.
func TestInterpolate_FeedbackRejectsFilter(t *testing.T) {
	h := New(t.TempDir(), nil)

	_, err := h.Interpolate("{{ feedback.review | inline }}")
	var interpErr *InterpolationError
	if !errors.As(err, &interpErr) {
		t.Fatalf("filtered feedback placeholder did not fail with InterpolationError: %v", err)
	}
	if interpErr.Kind != "feedback" || interpErr.Reference != "review" {
		t.Fatalf("error names the wrong reference: %+v", interpErr)
	}
}

// TestSeedFeedback rehydrates the payload from the persisted file on resume,
// and treats a missing file as the clean "no round fired" no-op.
func TestSeedFeedback(t *testing.T) {
	dir := t.TempDir()

	first := New(dir, nil)
	if err := first.SetFeedback("review", "carry me across legs"); err != nil {
		t.Fatalf("SetFeedback: %v", err)
	}

	resumed := New(dir, nil)
	if err := resumed.SeedFeedback("review"); err != nil {
		t.Fatalf("SeedFeedback: %v", err)
	}
	got, err := resumed.Interpolate("{{ feedback.review }}")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "carry me across legs" {
		t.Fatalf("seeded payload did not resolve: %q", got)
	}

	if err := resumed.SeedFeedback("never-fired"); err != nil {
		t.Fatalf("SeedFeedback on a missing payload must be a no-op, got: %v", err)
	}
}

// TestLintPlaceholders_FeedbackKind pins the advisory split: a well-formed
// feedback token is lint-silent (its semantics are a LOAD error owned by
// graph.Validate, not an advisory), while the plural typo is caught as
// placeholder-like text that will ship verbatim.
func TestLintPlaceholders_FeedbackKind(t *testing.T) {
	g := parseGraph(t, `
name: g
nodes:
  - id: impl
    prompt: "redo per {{ feedback.review }} and {{ feedbacks.review }}"
  - id: review
    depends_on: [impl]
    prompt: judge
    feedback: { rerun: impl, max: 1 }
`)

	warnings := LintPlaceholders(g)
	if len(warnings) != 1 {
		t.Fatalf("want exactly the typo warning, got %d: %v", len(warnings), warnings)
	}
	if warnings[0].NodeID != "impl" || !strings.Contains(warnings[0].Detail, "{{ feedbacks.review }}") {
		t.Fatalf("warning does not name the typo token: %+v", warnings[0])
	}
}
