package handoff

import (
	"slices"
	"testing"
)

// TestInputReferences356_EveryInterpolatedField pins #356's reference scan:
// a well-formed inputs token counts in a prompt, a cwd, a verify command, a
// verify cwd and a gate's description, whether or not the graph declares it,
// and the result is sorted and deduplicated.
func TestInputReferences356_EveryInterpolatedField(t *testing.T) {
	g := parseGraph(t, `
name: refs
inputs: [repo]
nodes:
  - id: a
    prompt: "use {{ inputs.repo }} and {{inputs.repo}} again"
    cwd: "{{ inputs.dir }}"
    success_check:
      verify:
        command: "test -f {{ inputs.file | inline }}"
        cwd: "{{ inputs.vdir }}"
  - id: approve
    type: gate
    depends_on: [a]
    description: "ship {{ inputs.ticket }}?"
`)
	want := []string{"dir", "file", "repo", "ticket", "vdir"}
	if got := InputReferences(g); !slices.Equal(got, want) {
		t.Errorf("InputReferences = %v, want %v", got, want)
	}
}

// TestInputReferences356_MalformedAndLiteralTokensNameNothing: a token the
// runtime would not resolve as an inputs reference — a singular typo, a
// case-variant, an unknown filter, another kind, literal text — names no input.
func TestInputReferences356_MalformedAndLiteralTokensNameNothing(t *testing.T) {
	g := parseGraph(t, `
name: none
nodes:
  - id: a
    prompt: "{{ input.repo }} {{ Inputs.repo }} {{ inputs.repo | inlin }} {{ inputs. }} {{ name }} {{ artifacts.repo }}"
`)
	if got := InputReferences(g); len(got) != 0 {
		t.Errorf("InputReferences = %v, want none", got)
	}
}
