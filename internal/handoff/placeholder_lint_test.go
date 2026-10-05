package handoff

import (
	"errors"
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
			name:       "dotted reference is one whole name, judged against declarations",
			prompt:     "use {{ inputs.repo.name }}",
			wantDetail: "does not declare",
		},
		{
			name:       "case-variant artifacts kind warns with a lowercase hint",
			prompt:     "read {{ Artifacts.a }}",
			wantDetail: "did you mean lowercase?",
		},
		{
			name:       "case-variant inputs kind warns with a lowercase hint",
			prompt:     "use {{ INPUTS.repo }}",
			wantDetail: "did you mean lowercase?",
		},
		{
			name:       "case-variant singular typo warns with a lowercase hint",
			prompt:     "use {{ Input.repo }}",
			wantDetail: "did you mean lowercase?",
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
		{
			name:       "self names a reference other than previous",
			prompt:     "recall {{ self.last }}",
			wantDetail: "one reference, {{ self.previous }}",
		},
		{
			name:       "self takes no filter",
			prompt:     "recall {{ self.previous | inline }}",
			wantDetail: "takes no filter",
		},
		{
			name:       "case-variant self kind warns with a lowercase hint",
			prompt:     "recall {{ Self.previous }}",
			wantDetail: "did you mean lowercase?",
		},
		// --- silent cases ----------------------------------------------------
		{
			// Legal on any node, loop or not (#288): outside a feedback body it
			// is simply always empty, which a fragment must be able to rely on.
			name:       "self.previous stays silent outside any loop",
			prompt:     "recall {{ self.previous }}",
			wantDetail: "",
		},
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

// TestLintPlaceholders_WithTokens covers the fragment substitution kind
// (ADR 0013): `with` resolves at LOAD time inside a fragment body, so any
// with-token surviving into a validated graph sits in a plain node and will
// ship verbatim into a paid prompt — worth its own message, not the generic
// malformed-token one. Failure (warning) cases first.
func TestLintPlaceholders_WithTokens(t *testing.T) {
	cases := []struct {
		name       string
		prompt     string
		wantDetail string // "" = expect silence
	}{
		{
			name:       "with token in a plain node warns with the load-time message",
			prompt:     "run {{ with.checks }} now",
			wantDetail: "resolved at load time",
		},
		{
			name:       "case-variant With token is judged, not shipped silently",
			prompt:     "run {{ With.checks }} now",
			wantDetail: "resolved at load time",
		},
		{
			name:       "standalone with token warns too",
			prompt:     "{{ with.checks }}",
			wantDetail: "resolved at load time",
		},
		{
			name:       "a non-kind leading word stays deliberate literal text",
			prompt:     "the {{ withering }} heights",
			wantDetail: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, `
name: t
nodes:
  - id: a
    prompt: "`+tc.prompt+`"
`)
			warnings := LintPlaceholders(g)
			if tc.wantDetail == "" {
				if len(warnings) != 0 {
					t.Fatalf("want silence, got %v", warnings)
				}
				return
			}
			if len(warnings) != 1 || !strings.Contains(warnings[0].Detail, tc.wantDetail) {
				t.Fatalf("want one warning containing %q, got %v", tc.wantDetail, warnings)
			}
		})
	}
}

// TestInterpolate_NeverLearnsWith pins the other half of the two-table split:
// the runtime's placeholderPattern must NOT resolve a with-token — it passes
// through verbatim (the loader is the only resolver), never errors, never
// substitutes.
func TestInterpolate_NeverLearnsWith(t *testing.T) {
	h := New(t.TempDir(), map[string]string{"checks": "should-never-appear"})
	got, err := h.Interpolate("run {{ with.checks }} now")
	if err != nil {
		t.Fatalf("a with-token must pass through, not error: %v", err)
	}
	if got != "run {{ with.checks }} now" {
		t.Fatalf("the runtime resolved a with-token: %q", got)
	}
}

// TestImpossibleArtifactFindings_IsTheRuntimeRefusedSubset pins the boundary
// auto mode escalates on (#244). The subset must be drawn where the ENGINE
// draws it — a token Interpolate refuses rather than one it passes through —
// because the coordinator turns membership into a refused plan, and a plan
// refused for a token that would merely have shipped verbatim is a run the user
// paid for and did not get.
//
// The graph carries one of each: two impossible artifact references, and two
// findings that are advisory everywhere (a malformed token, an undeclared
// input). All four must be LintPlaceholders warnings; exactly two must be here.
func TestImpossibleArtifactFindings_IsTheRuntimeRefusedSubset(t *testing.T) {
	g := parseGraph(t, `
name: subset
version: "1"
inputs: [repo]
nodes:
  - id: corpus
    prompt: count the runs
  - id: writeup
    prompt: |
      from {{ artifacts.corpus }} and {{ artifacts.nope }},
      with {{ artifact.corpus }} and {{ inputs.undeclared }}
`)

	if all := LintPlaceholders(g); len(all) != 4 {
		t.Fatalf("the sweep must still report all four findings, got %d: %v", len(all), all)
	}

	findings := ImpossibleArtifactFindings(g)
	if len(findings) != 2 {
		t.Fatalf("want the two artifact references, got %d: %v", len(findings), findings)
	}
	for _, finding := range findings {
		if finding.NodeID != "writeup" {
			t.Errorf("finding names %q, not the node that quotes the token: %v", finding.NodeID, finding)
		}
	}
	joined := findings[0].String() + "\n" + findings[1].String()
	for _, want := range []string{"{{ artifacts.corpus }}", "not an ancestor", "{{ artifacts.nope }}", "is not a node in the graph"} {
		if !strings.Contains(joined, want) {
			t.Errorf("the subset does not carry %q:\n%s", want, joined)
		}
	}
	// The negative half, and the one that costs a run if it is wrong: a token
	// that ships verbatim is expensive, not fatal, and must stay advisory.
	for _, unwanted := range []string{"{{ artifact.corpus }}", "does not declare in its inputs list"} {
		if strings.Contains(joined, unwanted) {
			t.Errorf("the subset swept in an advisory-only finding %q:\n%s", unwanted, joined)
		}
	}
}

// TestLintPlaceholders_QuotingHint pins that two lint warnings carry the
// quoting hint — an undeclared input, an artifact of a node the graph does not
// have — and that the other warnings checked here do not. The hint is asserted
// by its words, through assertQuotingHint.
func TestLintPlaceholders_QuotingHint(t *testing.T) {
	g := parseGraph(t, `
name: hint
version: "1"
inputs: [repo]
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: "{{ inputs.ghost }} {{ artifacts.nope }} {{ artifacts.c }} {{ Artifacts.a }}", depends_on: [a] }
  - { id: c, prompt: c }
`)
	byToken := map[string]Warning{}
	for _, w := range LintPlaceholders(g) {
		byToken[w.Detail[:strings.Index(w.Detail, "}}")+2]] = w
	}
	if len(byToken) != 4 {
		t.Fatalf("want one warning per token, got %v", byToken)
	}

	for _, token := range []string{"{{ inputs.ghost }}", "{{ artifacts.nope }}"} {
		assertQuotingHint(t, errors.New(byToken[token].Detail))
	}
	for _, token := range []string{"{{ artifacts.c }}", "{{ Artifacts.a }}"} {
		refuteQuotingHint(t, byToken[token].Detail)
	}
}

// TestImpossibleArtifactFindings_CarriesNoQuotingHint pins the shared detail:
// the coordinator's plan refusal quotes it inside a refusal that explains
// quoting in its own words, so the hint must be added by LintPlaceholders
// alone — never by judgeToken.
func TestImpossibleArtifactFindings_CarriesNoQuotingHint(t *testing.T) {
	g := parseGraph(t, `
name: hint
version: "1"
nodes:
  - { id: b, prompt: "read {{ artifacts.nope }}" }
`)
	findings := ImpossibleArtifactFindings(g)
	if len(findings) != 1 || !strings.Contains(findings[0].Detail, "which is not a node in the graph") {
		t.Fatalf("want the one missing-node finding, got %v", findings)
	}
	refuteQuotingHint(t, findings[0].Detail)
}

// refuteQuotingHint is assertQuotingHint's negative: none of the hint's words
// may appear in detail.
func refuteQuotingHint(t *testing.T, detail string) {
	t.Helper()
	for _, unwanted := range quotingHintWords {
		if strings.Contains(detail, unwanted) {
			t.Fatalf("detail carries the quoting hint %q but must not:\n%s", unwanted, detail)
		}
	}
}
