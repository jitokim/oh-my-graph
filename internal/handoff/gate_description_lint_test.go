package handoff

import (
	"errors"
	"strings"
	"testing"
)

// gateDescriptionGraph is the fixture every #346 lint case shares: build
// produces an artifact, approve is a gate downstream of it carrying the
// description under test, and stray is a node approve does not descend from.
func gateDescriptionGraph(t *testing.T, description string) []error {
	t.Helper()
	g := parseGraph(t, `
name: gate-description
inputs: [ticket]
nodes:
  - id: build
    prompt: build it
  - id: stray
    prompt: unrelated
  - id: approve
    type: gate
    depends_on: [build]
    description: "`+description+`"
`)
	return GateDescriptionIssues(g)
}

// TestGateDescriptionIssues_RefusesContent covers #346's refusals: every token
// that would print a model's text, or anything else that is not a path or an
// engine fact, where a person decides the gate.
func TestGateDescriptionIssues_RefusesContent(t *testing.T) {
	cases := []struct {
		name        string
		description string
		token       string
		wantInError string
	}{
		{
			name:        "#346: an inline filter prints the producer's reply",
			description: "ship {{ artifacts.build | inline }}?",
			token:       "{{ artifacts.build | inline }}",
			wantInError: "inline filter",
		},
		{
			name:        "#346: a tight inline token is the same reply",
			description: "ship {{artifacts.build|inline}}?",
			token:       "{{artifacts.build|inline}}",
			wantInError: "inline filter",
		},
		{
			name:        "#346: a feedback reference inlines a payload",
			description: "last review said {{ feedback.build }}",
			token:       "{{ feedback.build }}",
			wantInError: "feedback placeholder",
		},
		{
			name:        "#346: self.previous inlines a reply",
			description: "earlier: {{ self.previous }}",
			token:       "{{ self.previous }}",
			wantInError: "self.previous",
		},
		{
			name:        "#346: self.timeout is no fact about a gate",
			description: "bounded by {{ self.timeout }}",
			token:       "{{ self.timeout }}",
			wantInError: "no per-attempt timeout",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertOneGateIssue(t, gateDescriptionGraph(t, tc.description), tc.token, tc.wantInError)
		})
	}
}

// TestGateDescriptionIssues_RefusesUnresolvable covers #346's rule that an
// unresolvable reference fails exactly as judgeToken judges it in a prompt —
// the same sentence, here as a refusal.
func TestGateDescriptionIssues_RefusesUnresolvable(t *testing.T) {
	cases := []struct {
		name        string
		description string
		token       string
		wantInError string
	}{
		{
			name:        "#346: an artifact of a node the graph does not have",
			description: "see {{ artifacts.ghost }}",
			token:       "{{ artifacts.ghost }}",
			wantInError: "not a node in the graph",
		},
		{
			name:        "#346: an artifact of a node the gate does not descend from",
			description: "see {{ artifacts.stray }}",
			token:       "{{ artifacts.stray }}",
			wantInError: "not an ancestor",
		},
		{
			name:        "#346: the gate's own artifact",
			description: "see {{ artifacts.approve }}",
			token:       "{{ artifacts.approve }}",
			wantInError: "own artifact",
		},
		{
			name:        "#346: an input the graph does not declare",
			description: "for {{ inputs.missing }}",
			token:       "{{ inputs.missing }}",
			wantInError: "does not declare",
		},
		{
			name:        "#346: an unknown self key",
			description: "as {{ self.id }}",
			token:       "{{ self.id }}",
			wantInError: "two references",
		},
		{
			name:        "#346: a malformed placeholder-like token",
			description: "see {{ artifact.build }}",
			token:       "{{ artifact.build }}",
			wantInError: "does not match",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertOneGateIssue(t, gateDescriptionGraph(t, tc.description), tc.token, tc.wantInError)
		})
	}
}

// TestGateDescriptionIssues_AdmitsPathsAndFacts covers #346's admissions: a
// filterless artifact (its file path), a declared input, literal braces, and
// plain prose. No self key is admitted — gateDescriptionSelfKeys says why —
// so the self case lives in the refusals above.
func TestGateDescriptionIssues_AdmitsPathsAndFacts(t *testing.T) {
	for _, description := range []string{
		"approve the build at {{ artifacts.build }}",
		"approve the build at {{artifacts.build}}",
		"approve {{ inputs.ticket }}",
		"approve {{ inputs.ticket }} built at {{ artifacts.build }}",
		"literal {{ not a placeholder }} text",
		"plain prose, no tokens",
	} {
		t.Run(description, func(t *testing.T) {
			if issues := gateDescriptionGraph(t, description); len(issues) != 0 {
				t.Fatalf("%q must pass, got %v", description, issues)
			}
		})
	}
}

// TestGateDescriptionIssues_ReportsEveryToken: one issue per offending token,
// so a broken description is fixable in one lint pass (#346).
func TestGateDescriptionIssues_ReportsEveryToken(t *testing.T) {
	issues := gateDescriptionGraph(t, "{{ artifacts.build | inline }} {{ inputs.ticket }} {{ self.previous }}")
	if len(issues) != 2 {
		t.Fatalf("want 2 issues (inline, self.previous), got %v", issues)
	}
}

// TestGateDescriptionIssues_IgnoresPromptsAndOtherNodes: the rule is about a
// gate's description only — the same inline token in a prompt is a prompt's
// business (#346).
func TestGateDescriptionIssues_IgnoresPromptsAndOtherNodes(t *testing.T) {
	g := parseGraph(t, `
name: prompts-may-inline
nodes:
  - id: build
    prompt: build it
  - id: review
    prompt: "review {{ artifacts.build | inline }}"
    depends_on: [build]
  - id: approve
    type: gate
    depends_on: [review]
`)
	if issues := GateDescriptionIssues(g); len(issues) != 0 {
		t.Fatalf("no gate description, so no issue; got %v", issues)
	}
}

func assertOneGateIssue(t *testing.T, issues []error, token, wantInError string) {
	t.Helper()
	if len(issues) != 1 {
		t.Fatalf("want exactly one issue for %s, got %v", token, issues)
	}
	var gde *GateDescriptionError
	if !errors.As(issues[0], &gde) {
		t.Fatalf("want *GateDescriptionError, got %T: %v", issues[0], issues[0])
	}
	if gde.NodeID != "approve" || gde.Token != token {
		t.Errorf("issue must name the gate and the token: got node %q token %q", gde.NodeID, gde.Token)
	}
	msg := issues[0].Error()
	for _, want := range []string{`gate "approve"`, token, wantInError} {
		if !strings.Contains(msg, want) {
			t.Errorf("message %q should contain %q", msg, want)
		}
	}
}
