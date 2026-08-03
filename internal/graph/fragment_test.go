package graph

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeGraphDir lays out one entry graph plus its fragments/ sibling in a
// temp dir — the on-disk shape LoadFile resolves against — and returns the
// entry file's path.
func writeGraphDir(t *testing.T, entry string, fragments map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	entryPath := filepath.Join(dir, "graph.yaml")
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	if len(fragments) > 0 {
		if err := os.Mkdir(filepath.Join(dir, "fragments"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for name, body := range fragments {
		if err := os.WriteFile(filepath.Join(dir, "fragments", name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return entryPath
}

// testFragment is a minimal well-formed fragment most cases below start from.
const testFragment = `fragment: e2e-verify
description: cold-safe e2e gate
substitutions: [checks]
node:
  type: claude-run
  prompt: |
    Continue the work — and {{ with.checks }}
    Report exactly PASS, or FAIL with the failing step.
  allowed_tools: [Read, "Bash(make *)"]
  budget_usd: 1.5
  handoff: session
  success_check:
    exit_zero: true
    result_matches: "^PASS$"
    verify: { command: "make local", timeout: 5m }
  retry: { max: 1, on: [nonzero_exit, verify_failed] }
`

// usingGraph wraps one node body into a two-node entry graph whose second
// node supplies the session parent the fragment's handoff needs.
func usingGraph(nodeYAML string) string {
	return "name: t\nnodes:\n  - { id: dev, prompt: build it }\n" + nodeYAML
}

const usingE2E = `  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    with:
      checks: run make local.
`

// --- failure cases first: every load-error rule in the ADR, as a table -----

func TestLoadFile_FragmentLoadErrors(t *testing.T) {
	cases := []struct {
		name      string
		entry     string
		fragments map[string]string
		wantErr   string // substring of the first (fail-fast) error
	}{
		{
			name:    "use naming a fragment with no file at the fragment location",
			entry:   usingGraph("  - { id: e2e, use: ghost, depends_on: [dev] }\n"),
			wantErr: "no fragment file at the fragment location",
		},
		{
			name:    "with on a node without use",
			entry:   usingGraph("  - { id: x, prompt: p, with: { checks: c } }\n"),
			wantErr: "with: without use:",
		},
		{
			name: "with key the fragment does not declare",
			entry: usingGraph(`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    with: { checks: c, cheks: typo }
`),
			fragments: map[string]string{"e2e-verify": testFragment},
			wantErr:   `binds "cheks", which the fragment does not declare`,
		},
		{
			name:      "declared substitution point left unbound",
			entry:     usingGraph("  - { id: e2e, use: e2e-verify, depends_on: [dev] }\n"),
			fragments: map[string]string{"e2e-verify": testFragment},
			wantErr:   `substitution point "checks" is declared by the fragment but left unbound`,
		},
		{
			name: "prompt alongside use",
			entry: usingGraph(`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    prompt: my own prompt
    with: { checks: c }
`),
			fragments: map[string]string{"e2e-verify": testFragment},
			wantErr:   "prompt: alongside use:",
		},
		{
			name:  "fragment body references an undeclared substitution point",
			entry: usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": `fragment: e2e-verify
substitutions: [checks]
node:
  prompt: "do {{ with.checks }} then {{ with.undeclared }}"
`},
			wantErr: "{{ with.undeclared }}, which substitutions: does not declare",
		},
		{
			name:      "embedded token bound to a list",
			entry:     usingGraph("  - { id: e2e, use: e2e-verify, depends_on: [dev], with: { checks: [a, b] } }\n"),
			fragments: map[string]string{"e2e-verify": testFragment},
			wantErr:   "must be a scalar — got a list",
		},
		{
			name:      "with that is not a mapping",
			entry:     usingGraph("  - { id: e2e, use: e2e-verify, depends_on: [dev], with: nope }\n"),
			fragments: map[string]string{"e2e-verify": testFragment},
			wantErr:   "with: must be a mapping",
		},
		{
			name:    "use that is not a scalar name",
			entry:   usingGraph("  - { id: e2e, use: [a, b], depends_on: [dev] }\n"),
			wantErr: "use: must be a single non-empty fragment name",
		},
		{
			name:      "fragment file that does not parse",
			entry:     usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": "fragment: [unclosed"},
			wantErr:   "does not parse",
		},
		{
			name:      "fragment file without a node mapping",
			entry:     usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": "fragment: e2e-verify\nsubstitutions: [checks]\n"},
			wantErr:   "must declare a node: mapping",
		},
		{
			name:      "fragment substitutions that is not a sequence",
			entry:     usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": "fragment: f\nsubstitutions: checks\nnode: { prompt: p }\n"},
			wantErr:   "substitutions: must be a sequence",
		},
		{
			name:  "fragment nesting another fragment",
			entry: usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": `fragment: f
substitutions: [checks]
node: { use: other, prompt2: "{{ with.checks }}" }
`},
			wantErr: "fragments do not reference fragments",
		},
		{
			// filepath.Join cleans, so an unconstrained name walks straight
			// out of the fragments/ sibling — the one location the ADR
			// promises. The name is refused before any file is opened.
			name:    "use escaping the fragments sibling with ..",
			entry:   usingGraph("  - { id: e2e, use: ../../evil, depends_on: [dev] }\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			name:    "use naming a nested path",
			entry:   usingGraph("  - { id: e2e, use: sub/evil, depends_on: [dev] }\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			name:    "use naming a backslash path",
			entry:   usingGraph(`  - { id: e2e, use: "sub\\evil", depends_on: [dev] }` + "\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			name:    "use naming an absolute path",
			entry:   usingGraph("  - { id: e2e, use: /etc/passwd, depends_on: [dev] }\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			name:    "use naming the parent directory itself",
			entry:   usingGraph("  - { id: e2e, use: .., depends_on: [dev] }\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			name:    "use naming a dotfile",
			entry:   usingGraph("  - { id: e2e, use: .hidden, depends_on: [dev] }\n"),
			wantErr: "must be a bare fragment name",
		},
		{
			// The silent-verbatim case: the token is anchored on one key and
			// aliased onto another, so a walk that stops at Scalar/Sequence/
			// Mapping never sees the aliased copy — it would be neither
			// declaration-checked nor substituted, and `{{ with.checks }}`
			// would reach the model as literal text in a paid prompt.
			name:  "fragment body hiding a substitution token behind an alias",
			entry: usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": `fragment: e2e-verify
substitutions: [checks]
node:
  prompt: &p "do {{ with.checks }}"
  agent: *p
`},
			wantErr: "uses a YAML alias",
		},
		{
			name:  "fragment body using a merge key",
			entry: usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": `fragment: e2e-verify
substitutions: [checks]
base: &base { permission_mode: plan }
node:
  <<: *base
  prompt: "{{ with.checks }}"
`},
			wantErr: "uses a YAML alias",
		},
	}
	for _, wiring := range []string{"id", "depends_on", "cwd", "worktree", "feedback"} {
		value := map[string]string{
			"id": "x", "depends_on": "[dev]", "cwd": "/tmp",
			"worktree": "lane", "feedback": "{ rerun: dev, max: 1 }",
		}[wiring]
		cases = append(cases, struct {
			name      string
			entry     string
			fragments map[string]string
			wantErr   string
		}{
			name:  "fragment carrying wiring field " + wiring,
			entry: usingGraph(usingE2E),
			fragments: map[string]string{"e2e-verify": "fragment: f\nsubstitutions: [checks]\nnode: { prompt: \"{{ with.checks }}\", " +
				wiring + ": " + value + " }\n"},
			wantErr: `fragment declares "` + wiring + `"`,
		})
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := writeGraphDir(t, tc.entry, tc.fragments)

			_, err := LoadFile(path)
			if err == nil {
				t.Fatalf("LoadFile accepted a graph that must fail with %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("LoadFile error = %q, want it to contain %q", err, tc.wantErr)
			}

			// The collect-all view must agree on which problem comes first —
			// the Validate-is-Issues()[0] contract, extended to fragments.
			issues, _, lintErr := LintFile(path)
			if lintErr != nil {
				t.Fatalf("LintFile I/O error: %v", lintErr)
			}
			if len(issues) == 0 {
				t.Fatal("LintFile found nothing where LoadFile failed")
			}
			if issues[0].Error() != err.Error() {
				t.Fatalf("LintFile first issue %q != LoadFile error %q", issues[0], err)
			}
		})
	}
}

// TestLoadFile_UseCannotReadOutsideTheFragmentsSibling proves the refusal is
// a boundary and not just a message: a real, well-formed fragment file placed
// exactly where `use: ../evil` would land is never read, so the node cannot
// take its prompt, its tool grant or its verify command from outside the one
// directory a reviewer reads. An assertion on the error alone would pass
// against a loader that read the file first and complained afterwards.
func TestLoadFile_UseCannotReadOutsideTheFragmentsSibling(t *testing.T) {
	path := writeGraphDir(t, usingGraph("  - { id: e2e, use: ../evil, depends_on: [dev], with: { checks: c } }\n"),
		map[string]string{"e2e-verify": testFragment})
	outside := filepath.Join(filepath.Dir(path), "evil.yaml")
	if err := os.WriteFile(outside, []byte(`fragment: evil
substitutions: [checks]
node: { prompt: "pwned {{ with.checks }}", allowed_tools: ["Bash(*)"] }
`), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := LoadFile(path); err == nil {
		t.Fatal("use: reached outside the fragments/ sibling")
	} else if !strings.Contains(err.Error(), "must be a bare fragment name") {
		t.Fatalf("want the bare-name refusal, got: %v", err)
	}

	// And the file's contents never entered the report either.
	issues, _, err := LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, issue := range issues {
		if strings.Contains(issue.Error(), "pwned") {
			t.Errorf("the out-of-tree file was read: %v", issue)
		}
	}
}

func TestLintFile_CollectsEveryFragmentIssueWithoutCascade(t *testing.T) {
	// Two independently broken use: nodes — both reported, and neither
	// re-reported by the structural backstop (the failed nodes' use:/with:
	// are stripped so each defect surfaces exactly once).
	entry := usingGraph(
		"  - { id: a, use: ghost, depends_on: [dev] }\n" +
			"  - { id: b, use: e2e-verify, depends_on: [dev] }\n")
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	issues, _, err := LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 2 {
		t.Fatalf("want exactly 2 issues (one per broken node, no backstop echo), got %d: %v", len(issues), issues)
	}
	for _, issue := range issues {
		var unresolved *UnresolvedFragmentError
		if errors.As(issue, &unresolved) {
			t.Fatalf("the backstop echoed a defect the resolver already reported: %v", issue)
		}
	}
}

func TestLintFile_IncludesStructuralIssuesOfTheResolvedGraph(t *testing.T) {
	// The fragment resolves cleanly; the resolved node then has two parents
	// with the fragment's handoff: session — the resolved graph is validated
	// exactly like a hand-written one (no validation learns what a fragment is).
	entry := "name: t\nnodes:\n" +
		"  - { id: dev, prompt: build }\n" +
		"  - { id: dev2, prompt: build more }\n" +
		"  - { id: e2e, use: e2e-verify, depends_on: [dev, dev2], with: { checks: run make local. } }\n"
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	if _, err := LoadFile(path); err == nil || !strings.Contains(err.Error(), "session") {
		t.Fatalf("resolved graph must fail session-arity validation like a hand-written one, got: %v", err)
	}
	issues, _, err := LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 1 || !strings.Contains(issues[0].Error(), "session") {
		t.Fatalf("LintFile must collect the structural issue of the RESOLVED graph, got: %v", issues)
	}
}

func TestLintFile_AdvisesOnUnreferencedSubstitutionPoint(t *testing.T) {
	frag := `fragment: f
substitutions: [checks, unused]
node: { prompt: "do {{ with.checks }}" }
`
	entry := usingGraph(`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    with: { checks: c, unused: u }
`)
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": frag})

	issues, advisories, err := LintFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(issues) != 0 {
		t.Fatalf("an unreferenced point is drift smell, never an error: %v", issues)
	}
	if len(advisories) != 1 || !strings.Contains(advisories[0].Detail, `"unused"`) {
		t.Fatalf("want one advisory naming the unreferenced point, got %v", advisories)
	}
	// Advice must not affect the fail-fast view either.
	if _, err := LoadFile(path); err != nil {
		t.Fatalf("LoadFile must accept a graph whose only finding is advisory: %v", err)
	}
}

// --- merge rules -----------------------------------------------------------

func TestLoadFile_FragmentDefaultsTravel(t *testing.T) {
	path := writeGraphDir(t, usingGraph(usingE2E), map[string]string{"e2e-verify": testFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e2e, ok := res.Graph.NodeByID("e2e")
	if !ok {
		t.Fatal("resolved node e2e missing")
	}
	if e2e.Use != "" || e2e.With != nil {
		t.Fatalf("a validated graph must carry no fragment residue: use=%q with=%v", e2e.Use, e2e.With)
	}
	if !strings.Contains(e2e.Prompt, "and run make local.") {
		t.Errorf("substituted prompt = %q", e2e.Prompt)
	}
	if !reflect.DeepEqual(e2e.AllowedTools, []string{"Read", "Bash(make *)"}) {
		t.Errorf("fragment's allowed_tools must travel by default, got %v", e2e.AllowedTools)
	}
	if e2e.SuccessCheck.Verify == nil || e2e.SuccessCheck.Verify.Command != "make local" {
		t.Errorf("fragment's success_check.verify must travel, got %+v", e2e.SuccessCheck)
	}
	if e2e.Retry == nil || e2e.Retry.Max != 1 || len(e2e.Retry.On) != 2 {
		t.Errorf("fragment's retry must travel, got %+v", e2e.Retry)
	}
	if e2e.Handoff != HandoffSession || e2e.BudgetUSD != 1.5 {
		t.Errorf("handoff/budget must come from the fragment, got %q/%v", e2e.Handoff, e2e.BudgetUSD)
	}
	if !reflect.DeepEqual(e2e.DependsOn, []string{"dev"}) {
		t.Errorf("wiring must stay the using node's, got depends_on=%v", e2e.DependsOn)
	}

	if len(res.Resolutions) != 1 {
		t.Fatalf("want one resolution, got %v", res.Resolutions)
	}
	r := res.Resolutions[0]
	if r.NodeID != "e2e" || r.Fragment != "e2e-verify" || len(r.Overridden) != 0 {
		t.Errorf("resolution = %+v, want node e2e, fragment e2e-verify, no overrides", r)
	}
	if !strings.HasSuffix(r.Source, filepath.Join("fragments", "e2e-verify.yaml")) {
		t.Errorf("resolution source = %q", r.Source)
	}
}

func TestLoadFile_OverrideReplacesWholeSubtreeAndIsDisclosed(t *testing.T) {
	entry := usingGraph(`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    retry: { max: 2 }
    success_check: { exit_zero: true }
    budget_usd: 0
    with: { checks: run make local. }
`)
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e2e, _ := res.Graph.NodeByID("e2e")
	// Subtree replacement, never deep merge: the fragment's retry.on and the
	// whole verify block do not survive an override of their parent key.
	if e2e.Retry == nil || e2e.Retry.Max != 2 || e2e.Retry.On != nil {
		t.Errorf("retry override must replace the whole block: %+v", e2e.Retry)
	}
	if e2e.SuccessCheck.Verify != nil || e2e.SuccessCheck.ResultMatches != "" {
		t.Errorf("success_check override must replace the whole block, verify included: %+v", e2e.SuccessCheck)
	}
	// Key PRESENCE is the override judgment: budget_usd: 0 written in the
	// using node beats the fragment's 1.5, with no zero-value ambiguity.
	if e2e.BudgetUSD != 0 {
		t.Errorf("budget_usd: 0 must be an override, got %v", e2e.BudgetUSD)
	}

	want := []string{"budget_usd", "success_check", "retry"} // fragment-file key order
	if !reflect.DeepEqual(res.Resolutions[0].Overridden, want) {
		t.Errorf("disclosure must name every overridden key: got %v, want %v", res.Resolutions[0].Overridden, want)
	}
}

func TestLoadFile_StandaloneTokenSubstitutesTyped(t *testing.T) {
	frag := `fragment: f
substitutions: [tools, task]
node:
  prompt: "do {{ with.task }}"
  allowed_tools: "{{ with.tools }}"
`
	entry := usingGraph(`  - id: n
    use: f
    depends_on: [dev]
    with:
      task: the thing
      tools: [Read, Edit]
`)
	path := writeGraphDir(t, entry, map[string]string{"f": frag})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	n, _ := res.Graph.NodeByID("n")
	if !reflect.DeepEqual(n.AllowedTools, []string{"Read", "Edit"}) {
		t.Errorf("a standalone token must substitute TYPED — a bound list stays a list, got %v", n.AllowedTools)
	}
	if n.Prompt != "do the thing" {
		t.Errorf("embedded token must substitute textually, got %q", n.Prompt)
	}
}

func TestLoadFile_BoundRuntimePlaceholdersSurviveResolution(t *testing.T) {
	entry := "name: t\ninputs: [cmd]\nnodes:\n  - { id: dev, prompt: build }\n" +
		`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    with: { checks: "run {{ inputs.cmd }} and read {{ artifacts.dev | inline }}." }
`
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	e2e, _ := res.Graph.NodeByID("e2e")
	// The two layers compose because they fire at different times: with
	// resolves at load, inputs/artifacts at run.
	for _, token := range []string{"{{ inputs.cmd }}", "{{ artifacts.dev | inline }}"} {
		if !strings.Contains(e2e.Prompt, token) {
			t.Errorf("runtime placeholder %s must survive load-time resolution untouched:\n%s", token, e2e.Prompt)
		}
	}
}

// TestLoadFile_BindingWrittenAsAnAliasResolves is the other side of the
// alias rule: aliases are refused inside a fragment BODY (which gets walked
// and substituted), never in the using graph, where they are ordinary YAML.
// A binding written as `*ref` must therefore be judged as the value it names —
// including in the embedded case, whose "must be a scalar" check would
// otherwise report the alias node's kind rather than the bound value's.
func TestLoadFile_BindingWrittenAsAnAliasResolves(t *testing.T) {
	entry := "name: t\nshared: &checks run make local.\nnodes:\n  - { id: dev, prompt: build }\n" +
		`  - id: e2e
    use: e2e-verify
    depends_on: [dev]
    with: { checks: *checks }
`
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a binding written as an alias must resolve: %v", err)
	}
	e2e, _ := res.Graph.NodeByID("e2e")
	if !strings.Contains(e2e.Prompt, "run make local.") {
		t.Errorf("the aliased binding's value must be substituted into the prompt:\n%s", e2e.Prompt)
	}
}

func TestLoadFile_OneFragmentTwiceUnderTwoIDs(t *testing.T) {
	entry := usingGraph(
		"  - { id: gate-a, use: e2e-verify, depends_on: [dev], with: { checks: check A. } }\n" +
			"  - { id: gate-b, use: e2e-verify, depends_on: [gate-a], with: { checks: check B. } }\n")
	path := writeGraphDir(t, entry, map[string]string{"e2e-verify": testFragment})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := res.Graph.NodeByID("gate-a")
	b, _ := res.Graph.NodeByID("gate-b")
	if !strings.Contains(a.Prompt, "check A.") || !strings.Contains(b.Prompt, "check B.") {
		t.Errorf("two uses must substitute independently:\n a=%q\n b=%q", a.Prompt, b.Prompt)
	}
	if len(res.Resolutions) != 2 {
		t.Errorf("want a disclosure per use, got %v", res.Resolutions)
	}
}

func TestLoadFile_FragmentFreeFileEqualsParse(t *testing.T) {
	entry := "name: plain\nnodes:\n  - { id: a, prompt: hello }\n  - { id: b, prompt: world, depends_on: [a] }\n"
	path := writeGraphDir(t, entry, nil)

	res, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := Parse([]byte(entry))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(res.Graph, parsed) {
		t.Errorf("LoadFile of a fragment-free file must equal Parse:\n got  %+v\n want %+v", res.Graph, parsed)
	}
	if string(res.Source) != entry {
		t.Errorf("Source must be the entry file's raw bytes")
	}
	if len(res.Resolutions) != 0 {
		t.Errorf("no resolutions expected, got %v", res.Resolutions)
	}
}

func TestLintFile_ValidFragmentGraphIsClean(t *testing.T) {
	path := writeGraphDir(t, usingGraph(usingE2E), map[string]string{"e2e-verify": testFragment})
	issues, advisories, err := LintFile(path)
	if err != nil || len(issues) != 0 || len(advisories) != 0 {
		t.Fatalf("clean fragment graph: issues=%v advisories=%v err=%v", issues, advisories, err)
	}
}

func TestLoadFile_MissingEntryFile(t *testing.T) {
	if _, err := LoadFile(filepath.Join(t.TempDir(), "no-such.yaml")); err == nil || !strings.Contains(err.Error(), "read graph file") {
		t.Fatalf("want a read error naming the file, got %v", err)
	}
	if _, _, err := LintFile(filepath.Join(t.TempDir(), "no-such.yaml")); err == nil || !strings.Contains(err.Error(), "read graph file") {
		t.Fatalf("LintFile: want an I/O error, got %v", err)
	}
}
