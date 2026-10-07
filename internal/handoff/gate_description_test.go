package handoff

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

func gateNode(description string) graph.Node {
	return graph.Node{ID: "approve", Type: graph.TypeGate, DependsOn: []string{"build"}, Description: description}
}

// TestRenderGateDescription_Interpolates: #346 — the description renders the
// artifact's FILE PATH and the input's value, through the prompt machinery.
func TestRenderGateDescription_Interpolates(t *testing.T) {
	dir := t.TempDir()
	h := New(dir, map[string]string{"ticket": "T-42"})
	if err := h.PersistOutput("build", "the build's own reply", "s-1"); err != nil {
		t.Fatalf("persist: %v", err)
	}
	got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }} from {{ artifacts.build }}?"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "ship T-42 from " + filepath.Join(dir, "build.out") + "?"
	if got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
	if strings.Contains(got, "own reply") {
		t.Fatalf("a filterless artifact must render its path, never its content: %q", got)
	}
}

// TestRenderGateDescription_SanitisesInputs: #346 — an --input value carrying
// a screen clear, an OSC retitle and a newline reaches the reader as one inert
// line.
func TestRenderGateDescription_SanitisesInputs(t *testing.T) {
	h := New(t.TempDir(), map[string]string{
		"ticket": "T-42\x1b[2J\x1b]0;approved\x07\napprove? [y/N] y",
	})
	got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }}"))
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := "ship T-42 approve? [y/N] y"; got != want {
		t.Fatalf("rendered %q, want %q", got, want)
	}
}

// TestRenderGateDescription_StripsFormatCharactersFromInputs: #346 — an
// --input value carrying a bidi override, a line separator or a zero-width
// character reaches the reader without it.
func TestRenderGateDescription_StripsFormatCharactersFromInputs(t *testing.T) {
	for name, tc := range map[string]struct {
		ticket, want string
		r            rune
	}{
		"U+202E right-to-left override": {"T-42\u202e24-T", "ship T-4224-T", '\u202e'},
		"U+2028 line separator":         {"T-42\u2028approve? [y/N] y", "ship T-42approve? [y/N] y", '\u2028'},
		"U+200B zero-width space":       {"T-\u200b42", "ship T-42", '\u200b'},
	} {
		t.Run(name, func(t *testing.T) {
			h := New(t.TempDir(), map[string]string{"ticket": tc.ticket})
			got, err := h.RenderGateDescription(gateNode("ship {{ inputs.ticket }}"))
			if err != nil {
				t.Fatalf("render: %v", err)
			}
			if strings.ContainsRune(got, tc.r) {
				t.Fatalf("rendered description still carries %U: %q", tc.r, got)
			}
			if got != tc.want {
				t.Fatalf("rendered %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRenderGateDescription_EmptyIsEmpty: a gate with no description renders
// nothing, and needs no artifact to do it (#346).
func TestRenderGateDescription_EmptyIsEmpty(t *testing.T) {
	got, err := New(t.TempDir(), nil).RenderGateDescription(gateNode(""))
	if err != nil || got != "" {
		t.Fatalf("want \"\", nil; got %q, %v", got, err)
	}
}

// TestRenderGateDescription_RefusesContentTokens: #346 — the render refuses
// what lint refuses, by the same predicate, so an unlinted graph still cannot
// print a reply where a person approves. The artifact exists, so the refusal
// is the predicate's, not a resolution failure.
func TestRenderGateDescription_RefusesContentTokens(t *testing.T) {
	for _, token := range []string{
		"{{ artifacts.build | inline }}",
		"{{ feedback.build }}",
		"{{ self.previous }}",
		"{{ self.timeout }}",
	} {
		t.Run(token, func(t *testing.T) {
			h := New(t.TempDir(), nil)
			if err := h.PersistOutput("build", "SECRET REPLY", "s-1"); err != nil {
				t.Fatalf("persist: %v", err)
			}
			got, err := h.RenderGateDescription(gateNode("ship " + token))
			var gde *GateDescriptionError
			if !errors.As(err, &gde) {
				t.Fatalf("want *GateDescriptionError, got %q, %v", got, err)
			}
			if gde.NodeID != "approve" || gde.Token != token {
				t.Errorf("error must name the gate and token: %+v", gde)
			}
		})
	}
}

// TestRenderGateDescription_UnresolvableFails: #346 — an artifact whose
// producer has not run is the prompt's *InterpolationError, not an empty
// substitution.
func TestRenderGateDescription_UnresolvableFails(t *testing.T) {
	_, err := New(t.TempDir(), nil).RenderGateDescription(gateNode("ship {{ artifacts.build }}"))
	var iErr *InterpolationError
	if !errors.As(err, &iErr) || iErr.Reference != "build" {
		t.Fatalf("want *InterpolationError for build, got %v", err)
	}
}
