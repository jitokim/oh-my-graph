package handoff

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// parseGraph builds a validated graph from YAML — LintPlaceholders' documented
// input shape.
func parseGraph(t *testing.T, yaml string) *graph.Graph {
	t.Helper()
	g, err := graph.Parse([]byte(yaml))
	if err != nil {
		t.Fatalf("fixture graph must be valid: %v", err)
	}
	return g
}

// TestLintPlaceholders_Warnings drives every warning class through one graph
// shape: node b depends on a, so a is b's only legitimate artifact reference.
func TestLintPlaceholders_Warnings(t *testing.T) {
	cases := []struct {
		name       string
		prompt     string // node b's prompt
		wantDetail string // substring of the single expected warning; "" = expect silence
	}{
		// --- warning cases ---------------------------------------------------
		{
			name:       "typoed filter fails the strict pattern",
			prompt:     "read {{ artifacts.a | inlin }}",
			wantDetail: "does not match",
		},
		{
			name:       "singular artifact typo",
			prompt:     "read {{ artifact.a }}",
			wantDetail: "does not match",
		},
		{
			name:       "singular input typo",
			prompt:     "use {{ input.repo }}",
			wantDetail: "does not match",
		},
		{
			name:       "nested path fails the strict pattern",
			prompt:     "use {{ inputs.repo.name }}",
			wantDetail: "does not match",
		},
		{
			name:       "undeclared input",
			prompt:     "use {{ inputs.ghost }}",
			wantDetail: "does not declare",
		},
		{
			name:       "artifact of a node that does not exist",
			prompt:     "read {{ artifacts.ghost }}",
			wantDetail: "not a node in the graph",
		},
		{
			name:       "artifact of the node itself",
			prompt:     "read {{ artifacts.b }}",
			wantDetail: "own artifact",
		},
		{
			name:       "artifact of a non-ancestor node",
			prompt:     "read {{ artifacts.c }}",
			wantDetail: "not an ancestor",
		},
		// --- silent cases ----------------------------------------------------
		{
			name:       "deliberate literal braces stay silent",
			prompt:     "explain what {{ mustache.templates }} and {{ x }} mean",
			wantDetail: "",
		},
		{
			name:       "declared input stays silent",
			prompt:     "use {{ inputs.repo }}",
			wantDetail: "",
		},
		{
			name:       "ancestor artifact stays silent",
			prompt:     "read {{ artifacts.a }} and {{ artifacts.a | inline }}",
			wantDetail: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: fixture
inputs: [repo]
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: "`+tc.prompt+`", depends_on: [a] }
  - { id: c, prompt: c }
`)
			warnings := LintPlaceholders(g)
			if tc.wantDetail == "" {
				if len(warnings) != 0 {
					t.Fatalf("expected silence, got %v", warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %v", warnings)
			}
			w := warnings[0]
			if w.NodeID != "b" || w.Field != "prompt" {
				t.Errorf("warning should name node b's prompt, got node %q field %q", w.NodeID, w.Field)
			}
			if !strings.Contains(w.Detail, tc.wantDetail) {
				t.Errorf("warning should contain %q, got: %s", tc.wantDetail, w.Detail)
			}
		})
	}
}

// TestLintPlaceholders_InspectsEveryInterpolatedField pins that the sweep
// covers the exact field set the Scheduler interpolates — cwd and the verify
// block's command/cwd, not just the prompt.
func TestLintPlaceholders_InspectsEveryInterpolatedField(t *testing.T) {
	g := parseGraph(t, `
name: fields
nodes:
  - id: a
    prompt: fine
    cwd: "{{ inputs.dir }}"
    success_check:
      verify:
        command: "test -f {{ artifacts.ghost }}"
        cwd: "{{ input.dir }}"
`)
	warnings := LintPlaceholders(g)
	gotFields := make(map[string]bool, len(warnings))
	for _, w := range warnings {
		gotFields[w.Field] = true
	}
	for _, want := range []string{"cwd", "success_check.verify.command", "success_check.verify.cwd"} {
		if !gotFields[want] {
			t.Errorf("expected a warning for field %q, got %v", want, warnings)
		}
	}
}

// TestLintPlaceholders_TransitiveAncestorStaysSilent pins that ancestry is
// transitive: a grandparent's artifact is a legitimate reference.
func TestLintPlaceholders_TransitiveAncestorStaysSilent(t *testing.T) {
	g := parseGraph(t, `
name: chain
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
  - { id: c, prompt: "read {{ artifacts.a }}", depends_on: [b] }
`)
	if warnings := LintPlaceholders(g); len(warnings) != 0 {
		t.Fatalf("a grandparent artifact reference must stay silent, got %v", warnings)
	}
}
