package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// writeFragmentGraphDir lays out one entry graph plus its fragments/ sibling
// — the on-disk shape graph.LoadFile resolves against (ADR 0013) — and
// returns the entry file's path. entryName keeps its extension: fragment
// resolution is a property of the loader, not of the file being .yaml
// (JSON is valid YAML, and a JSON-authored entry may carry use: too).
func writeFragmentGraphDir(t *testing.T, entryName, entry string, fragments map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	entryPath := filepath.Join(dir, entryName)
	if err := os.WriteFile(entryPath, []byte(entry), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "fragments"), 0o755); err != nil {
		t.Fatal(err)
	}
	for name, body := range fragments {
		if err := os.WriteFile(filepath.Join(dir, "fragments", name+".yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return entryPath
}

// gateFragment is the minimal fragment the CLI tests splice — artifact
// handoff so no session wiring distracts from what each test pins.
const gateFragment = `fragment: gate
description: the minimal CLI-test gate
substitutions: [checks]
node:
  type: claude-run
  prompt: "Continue — and {{ with.checks }}"
  success_check: { exit_zero: true }
`

// loadSingleRunSnapshot finds the one run directory a test run created under
// $OMG_HOME and loads its state.json — failing, not passing, when no run
// directory exists at all.
func loadSingleRunSnapshot(t *testing.T, home string) runstate.Snapshot {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "runs"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("want exactly one run directory under %s, got %v (err %v)", home, entries, err)
	}
	snap, err := runstate.Load(filepath.Join(home, "runs", entries[0].Name(), "state.json"))
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	return snap
}

// --- failure cases first ----------------------------------------------------

// TestRunGraphWith_BrokenFragmentRunsNoNode pins that a fragment resolution
// failure refuses the run before any node spawns — the fail-fast half of the
// loader, reached through the real `run` wiring.
func TestRunGraphWith_BrokenFragmentRunsNoNode(t *testing.T) {
	isolateRunHome(t)
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: broken\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: ghost, depends_on: [dev] }\n",
		map[string]string{"gate": gateFragment})
	fake := runner.NewFakeRunner(nil)

	err := runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	if err == nil || !strings.Contains(err.Error(), "no fragment file at the fragment location") {
		t.Fatalf("want the resolver's load error, got %v", err)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("a graph that failed to resolve invoked %d nodes, want 0", n)
	}
}

// TestRunGraphWith_JSONAuthoredFragmentGraphSnapshotsResolved is the ADR 0013
// snapshot-seam case: a JSON-authored entry file (JSON is valid YAML) carrying
// a use: node is valid JSON, so newRunRecorder's rawSource-verbatim shortcut
// would have smuggled UNRESOLVED bytes into the snapshot — and `resume`'s
// graph.Parse(snap.Graph) would die on the unresolved-fragment backstop. The
// snapshot must instead store the re-encoded resolved graph, while
// GraphSHA256 keeps hashing the ENTRY file's bytes.
func TestRunGraphWith_JSONAuthoredFragmentGraphSnapshotsResolved(t *testing.T) {
	home := isolateRunHome(t)
	entry := `{"name":"frag-json","nodes":[{"id":"dev","prompt":"build"},{"id":"e2e","use":"gate","depends_on":["dev"],"with":{"checks":"run make local."}}]}`
	path := writeFragmentGraphDir(t, "graph.json", entry, map[string]string{"gate": gateFragment})
	// FakeRunner keys by prompt: scripting the RESOLVED prompt also proves
	// the runner is handed the spliced text, never the use:/with: keys.
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"build":                          {SessionID: "s-dev", Result: "ok", ExitCode: 0},
		"Continue — and run make local.": {SessionID: "s-e2e", Result: "ok", ExitCode: 0},
	})

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}

	snap := loadSingleRunSnapshot(t, home)
	if strings.Contains(string(snap.Graph), `"use"`) {
		t.Fatalf("snapshot smuggled unresolved fragment bytes:\n%s", snap.Graph)
	}
	resolved, err := graph.Parse(snap.Graph)
	if err != nil {
		t.Fatalf("resume must be able to re-parse the snapshot, got: %v", err)
	}
	e2e, ok := resolved.NodeByID("e2e")
	if !ok || !strings.Contains(e2e.Prompt, "and run make local.") {
		t.Errorf("snapshot must hold the RESOLVED node, got prompt %q", e2e.Prompt)
	}
	if want := sha256Hex([]byte(entry)); snap.GraphSHA256 != want {
		t.Errorf("GraphSHA256 must keep hashing the entry file's bytes, got %q want %q", snap.GraphSHA256, want)
	}
}

// TestRunGraphWith_PrintsFragmentDisclosure pins the ADR 0013 disclosure
// posture on the real `run` path: one line per resolved fragment, at every
// run.
func TestRunGraphWith_PrintsFragmentDisclosure(t *testing.T) {
	isolateRunHome(t)
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: disclosed\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: gate, depends_on: [dev], with: { checks: run make local. } }\n",
		map[string]string{"gate": gateFragment})
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"build":                          {SessionID: "s-dev", Result: "ok", ExitCode: 0},
		"Continue — and run make local.": {SessionID: "s-e2e", Result: "ok", ExitCode: 0},
	})

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("run returned error: %v", err)
	}
	if !strings.Contains(out, `fragment: node "e2e" spliced from "gate"`) {
		t.Errorf("run must disclose the resolution:\n%s", out)
	}
}

// TestPrintFragmentResolutions_NamesEveryOverriddenKey pins the disclosure
// line's format directly: source file always, the override list exactly when
// the using node overrode fragment keys — the hollowing-out must be
// announced, not summarized.
func TestPrintFragmentResolutions_NamesEveryOverriddenKey(t *testing.T) {
	var out strings.Builder
	printFragmentResolutions(&out, []graph.FragmentResolution{
		{NodeID: "e2e", Fragment: "e2e-verify", Description: "cold-safe e2e gate", Source: "graphs/fragments/e2e-verify.yaml"},
		{NodeID: "gate", Fragment: "e2e-verify", Description: "cold-safe e2e gate", Source: "graphs/fragments/e2e-verify.yaml", Overridden: []string{"success_check", "retry"}},
	})
	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("want one line per resolution, got:\n%s", out.String())
	}
	// The description travels with the disclosure so a run log says what the
	// spliced shape IS, not merely which file it came from.
	if want := `fragment: node "e2e" spliced from "e2e-verify" (graphs/fragments/e2e-verify.yaml) — cold-safe e2e gate`; lines[0] != want {
		t.Errorf("line 0 = %q, want %q", lines[0], want)
	}
	if want := ` — node overrides: success_check, retry`; !strings.HasSuffix(lines[1], want) {
		t.Errorf("line 1 = %q, want suffix %q", lines[1], want)
	}
}

// TestLintGraph_SurfacesTheFragmentDescription pins why description: is a
// required key rather than a decorative one: `lint` is where a reader asks
// "what did this use: pull in", and the answer must be readable without
// opening the fragment file.
func TestLintGraph_SurfacesTheFragmentDescription(t *testing.T) {
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: g\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: gate, depends_on: [dev], with: { checks: c } }\n",
		map[string]string{"gate": gateFragment})

	var out, warnings strings.Builder
	if err := lintGraph(&out, &warnings, path); err != nil {
		t.Fatalf("valid graph failed lint: %v", err)
	}
	if !strings.Contains(out.String(), "the minimal CLI-test gate") {
		t.Errorf("lint must print each resolved fragment's description:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("the graph must still lint as valid:\n%s", out.String())
	}
}

// TestPrintFragmentResolutions_SilentWhenFragmentFree — the common case must
// not print even a header.
func TestPrintFragmentResolutions_SilentWhenFragmentFree(t *testing.T) {
	var out strings.Builder
	printFragmentResolutions(&out, nil)
	if got := out.String(); got != "" {
		t.Errorf("fragment-free run printed %q, want nothing", got)
	}
}

// TestLintGraph_CollectsFragmentIssues pins lint's collect-all contract
// extended to fragments: every broken use: reported in one pass, exit 1.
func TestLintGraph_CollectsFragmentIssues(t *testing.T) {
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: broken\nnodes:\n"+
			"  - { id: dev, prompt: build }\n"+
			"  - { id: a, use: ghost, depends_on: [dev] }\n"+
			"  - { id: b, use: gate, depends_on: [dev] }\n", // gate declares checks; b binds nothing
		map[string]string{"gate": gateFragment})

	var out, warnings strings.Builder
	err := lintGraph(&out, &warnings, path)
	if err == nil || !strings.Contains(err.Error(), "2 issues found") {
		t.Fatalf("want both fragment issues counted, got %v", err)
	}
	for _, want := range []string{"no fragment file at the fragment location", "left unbound"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("lint output should contain %q:\n%s", want, out.String())
		}
	}
}

// TestLintGraph_FragmentAdvisoryWarnsAndKeepsExitZero pins the advisory
// channel: drift smell in a fragment file is a warning line, never an exit
// code.
func TestLintGraph_FragmentAdvisoryWarnsAndKeepsExitZero(t *testing.T) {
	frag := "fragment: gate\ndescription: a gate\nsubstitutions: [checks, unused]\nnode: { prompt: \"do {{ with.checks }}\" }\n"
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: drifty\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: gate, depends_on: [dev], with: { checks: c, unused: u } }\n",
		map[string]string{"gate": frag})

	var out, warnings strings.Builder
	if err := lintGraph(&out, &warnings, path); err != nil {
		t.Fatalf("an advisory-only graph must lint clean: %v", err)
	}
	if !strings.Contains(out.String(), "valid") {
		t.Errorf("lint should confirm validity:\n%s", out.String())
	}
	if !strings.Contains(warnings.String(), "warning: "+path+": fragment") ||
		!strings.Contains(warnings.String(), `"unused"`) {
		t.Errorf("want a warning naming the unreferenced point:\n%s", warnings.String())
	}
}

// TestLintGraph_SweepsTheResolvedGraph pins that the advisory placeholder
// sweep runs on the RESOLVED nodes: a typoed token living in the fragment
// body is warned about on the using graph — no sweep learns what a fragment
// is, it just sees the spliced result.
func TestLintGraph_SweepsTheResolvedGraph(t *testing.T) {
	frag := "fragment: gate\ndescription: a gate\nsubstitutions: [checks]\nnode: { prompt: \"do {{ with.checks }} then read {{ artifact.dev }}\" }\n"
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: typo\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: gate, depends_on: [dev], with: { checks: c } }\n",
		map[string]string{"gate": frag})

	var out, warnings strings.Builder
	if err := lintGraph(&out, &warnings, path); err != nil {
		t.Fatalf("a placeholder typo is advisory, not an error: %v", err)
	}
	if !strings.Contains(warnings.String(), `node "e2e"`) {
		t.Errorf("the placeholder sweep must warn about the resolved node's prompt:\n%s", warnings.String())
	}
}

// TestDryRunGraph_ReportsFragmentIssues — the dry-run view of the same
// collect-all contract.
func TestDryRunGraph_ReportsFragmentIssues(t *testing.T) {
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: broken\nnodes:\n  - { id: dev, prompt: build }\n  - { id: e2e, use: ghost, depends_on: [dev] }\n",
		map[string]string{"gate": gateFragment})

	var out, warnings strings.Builder
	err := dryRunGraph(&out, &warnings, path, nil)
	if err == nil || !strings.Contains(err.Error(), "no node was executed") {
		t.Fatalf("want a dry-run refusal, got %v", err)
	}
	if !strings.Contains(out.String(), "no fragment file at the fragment location") {
		t.Errorf("dry run should report the fragment issue:\n%s", out.String())
	}
}

// TestDryRunGraph_ChecksInputsInsideResolvedFragmentPrompts pins the
// compose rule end to end: a {{ inputs.* }} reference bound INTO a fragment
// survives resolution, so the dry run's input check must see and judge it.
func TestDryRunGraph_ChecksInputsInsideResolvedFragmentPrompts(t *testing.T) {
	path := writeFragmentGraphDir(t, "graph.yaml",
		"name: needs-cmd\ninputs: [cmd]\nnodes:\n"+
			"  - { id: dev, prompt: build }\n"+
			"  - { id: e2e, use: gate, depends_on: [dev], with: { checks: \"run {{ inputs.cmd }}\" } }\n",
		map[string]string{"gate": gateFragment})

	var out, warnings strings.Builder
	err := dryRunGraph(&out, &warnings, path, nil)
	if err == nil || !strings.Contains(out.String(), `node "e2e"`) {
		t.Fatalf("an unbound input inside a resolved fragment prompt must fail the dry run, got %v:\n%s", err, out.String())
	}

	out.Reset()
	warnings.Reset()
	if err := dryRunGraph(&out, &warnings, path, map[string]string{"cmd": "make local"}); err != nil {
		t.Fatalf("bound input must pass: %v", err)
	}
	if !strings.Contains(out.String(), "e2e (after dev)") || !strings.Contains(out.String(), "validation passed") {
		t.Errorf("dry run should print the RESOLVED plan:\n%s", out.String())
	}
}
