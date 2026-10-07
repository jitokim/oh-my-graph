package main

import (
	"strings"
	"testing"
)

// #356: the pure half — the distance, the near-miss rule, the warning lines
// and the winning source bindInputFiles records. The `run` wiring is judged
// in undeclaredinput_wiring_test.go.

func TestUndeclaredInput356_Levenshtein(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"", "", 0},
		{"repo", "repo", 0},
		{"", "abc", 3},
		{"abc", "", 3},
		{"reop", "repo", 2},
		{"rep", "repo", 1},
		{"ticket", "tikcet", 2},
		{"kitten", "sitting", 3},
		{"zzz", "repo", 4},
		// Runes, not bytes: one é against e is one substitution, not two.
		{"café", "cafe", 1},
		{"日本語", "日本", 1},
	}
	for _, tc := range cases {
		if got := levenshtein(tc.a, tc.b); got != tc.want {
			t.Errorf("levenshtein(%q, %q) = %d, want %d", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestUndeclaredInput356_NearestDeclaredInput(t *testing.T) {
	cases := []struct {
		name     string
		key      string
		declared []string
		want     string
		ok       bool
	}{
		{"transposition at 4 runes", "reop", []string{"repo", "ticket"}, "repo", true},
		{"distance 2 allowed at 4 runes", "rexx", []string{"repo"}, "repo", true},
		{"distance 3 refused at 4+ runes", "abcdef", []string{"uvwdef"}, "", false},
		{"distance 1 allowed at 3 runes", "rpo", []string{"repo"}, "repo", true},
		{"distance 2 refused at 3 runes", "rop", []string{"ropes"}, "", false},
		{"never at 2 runes", "rp", []string{"rpo"}, "", false},
		{"never at 1 rune", "a", []string{"b"}, "", false},
		{"far-off key", "zzz", []string{"repo", "ticket"}, "", false},
		{"smallest distance wins", "repos", []string{"rxpoxs", "repo"}, "repo", true},
		{"tie goes to the alphabetically first", "abcd", []string{"abcy", "abcx"}, "abcx", true},
		{"no declared names", "repo", nil, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := nearestDeclaredInput(tc.key, tc.declared)
			if got != tc.want || ok != tc.ok {
				t.Errorf("nearestDeclaredInput(%q, %v) = (%q, %v), want (%q, %v)", tc.key, tc.declared, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestUndeclaredInput356_WarningLines(t *testing.T) {
	got := undeclaredInputWarnings(
		[]string{"repo", "ticket"},
		map[string]string{"repo": "--input", "zzz": "--input", "reop": "in.yaml", "ticket": "f.json"},
	)
	want := []string{
		`input "reop" (from in.yaml) is not declared in the graph's inputs list; it is bound anyway — did you mean "repo"?`,
		`input "zzz" (from --input) is not declared in the graph's inputs list; it is bound anyway`,
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("lines\ngot:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestUndeclaredInput356_DeclaredKeysAndNoKeysPrintNothing(t *testing.T) {
	if got := undeclaredInputWarnings([]string{"repo"}, map[string]string{"repo": "--input"}); len(got) != 0 {
		t.Errorf("declared key warned: %q", got)
	}
	if got := undeclaredInputWarnings([]string{"repo"}, nil); len(got) != 0 {
		t.Errorf("no bound keys warned: %q", got)
	}
}

func TestUndeclaredInput356_NoInputsDeclarationWarnsEveryKey(t *testing.T) {
	got := undeclaredInputWarnings(nil, map[string]string{"b": "--input", "a": "x.yaml"})
	if len(got) != 2 || !strings.HasPrefix(got[0], `input "a" (from x.yaml)`) || !strings.HasPrefix(got[1], `input "b" (from --input)`) {
		t.Errorf("lines = %q, want one per key, sorted", got)
	}
}

func TestUndeclaredInput356_NoRawEscapeInKeyOrSource(t *testing.T) {
	got := undeclaredInputWarnings([]string{"repo"}, map[string]string{"\x1b[31mred": "dir/\x1b]0;evil\x07in.yaml"})
	if len(got) != 1 {
		t.Fatalf("lines = %q, want 1", got)
	}
	if strings.ContainsAny(got[0], "\x1b\x07") {
		t.Errorf("raw control byte reached the line: %q", got[0])
	}
	if !strings.Contains(got[0], `input "\x1b[31mred"`) {
		t.Errorf("key not %%q-quoted: %q", got[0])
	}
}

func TestUndeclaredInput356_SourcesRecordTheWinner(t *testing.T) {
	var c commonRunFlags
	c.inputs = inputFlag{"b": "flag", "d": "flag"}
	c.inputFiles = inputFileFlag{
		{path: "f1.yaml", bindings: map[string]string{"a": "1", "b": "1", "c": "1"}},
		{path: "f2.yaml", bindings: map[string]string{"a": "2"}},
	}
	var out strings.Builder
	c.bindInputFiles(&out)
	want := map[string]string{"a": "f2.yaml", "b": "--input", "c": "f1.yaml", "d": "--input"}
	if len(c.inputSources) != len(want) {
		t.Fatalf("inputSources = %v, want %v", c.inputSources, want)
	}
	for k, v := range want {
		if c.inputSources[k] != v {
			t.Errorf("inputSources[%q] = %q, want %q", k, c.inputSources[k], v)
		}
	}
}

func TestUndeclaredInput356_InputOnlyInvocationRecordsSources(t *testing.T) {
	var c commonRunFlags
	c.inputs = inputFlag{"repo": "x"}
	var out strings.Builder
	c.bindInputFiles(&out)
	if out.Len() != 0 {
		t.Errorf("--input alone printed a precedence line: %q", out.String())
	}
	if c.inputSources["repo"] != "--input" || len(c.inputSources) != 1 || c.inputs["repo"] != "x" || len(c.inputs) != 1 {
		t.Errorf("inputs = %v, sources = %v", c.inputs, c.inputSources)
	}
}
