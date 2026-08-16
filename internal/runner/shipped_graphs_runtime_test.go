package runner

import (
	"io/fs"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// shippedGraphUnderCodex is what `oh-my-graph --runtime codex lint <graph>` is
// expected to say about one shipped graph. refusedFor is the declaration that
// makes it unloadable — empty means it loads clean — and why records the
// decision, so a failure reads as a changed verdict rather than a changed
// number.
type shippedGraphUnderCodex struct {
	refusedFor  string
	warnsBudget bool
	why         string
}

// shippedGraphsUnderCodex names EVERY graph this binary embeds and the verdict
// the Codex runtime owes it. Named rather than counted on purpose: a count
// says "one more graph broke", a name says which one and what it declared.
//
// A new or edited shipped graph that becomes unloadable under Codex fails here
// with its own name, so the refusal is a decision somebody made rather than a
// side effect of adding a field. Adding a graph without adding its row fails
// too (TestShippedGraphsUnderEachRuntime's coverage check): the table cannot
// silently fall behind graphs/, whose //go:embed patterns are globs.
//
// This costs nothing to keep true — preflight spawns no process and needs no
// login — which is why it lives in `make test` rather than in a CI shell step
// that only the merge queue would ever run.
var shippedGraphsUnderCodex = map[string]shippedGraphUnderCodex{
	"adr-driven-dev.yaml": {
		refusedFor:  "agent",
		warnsBudget: true,
		why:         "three review nodes name Claude Code subagents; without those system prompts they are different nodes (ADR 0026). Its localrun budget_usd only warns.",
	},
	"apply-flags.yaml":   {why: "declares neither agent: nor budget_usd"},
	"backlog-batch.yaml": {warnsBudget: true, why: "both lanes inherit budget_usd from the e2e-verify fragment; the cap cannot apply, timeout: still guards"},
	"dev-review-pr.yaml": {warnsBudget: true, why: "its e2e node inherits budget_usd from the e2e-verify fragment"},
	"haiku-smoke.yaml":   {why: "the smoke graph declares neither"},
	"merge-shepherd.yaml": {
		why: "gh end to end, but declares neither agent: nor budget_usd",
	},
	"review-loop.yaml": {warnsBudget: true, why: "impl and review both cap spend; the caps cannot apply under Codex"},
	"self-dev.yaml":    {warnsBudget: true, why: "its e2e node inherits budget_usd from the e2e-verify fragment"},
}

// TestShippedGraphsUnderEachRuntime lints every embedded graph under BOTH
// runtimes and checks the verdict against the table above.
//
// The Claude half is the load-bearing half of this test even though it asserts
// the dullest thing: ValidateGraphForRuntime returns early for Claude, so a
// Claude user can observe nothing here — no refusal, no warning — and any diff
// that changes that shows up as a failure naming the graph it changed.
func TestShippedGraphsUnderEachRuntime(t *testing.T) {
	names := shippedTemplateNames(t)
	assertTableCoversShippedGraphs(t, names)

	for _, name := range names {
		want := shippedGraphsUnderCodex[name]
		loaded, err := graph.LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Errorf("shipped graph %s failed to load: %v", name, err)
			continue
		}

		claudeWarnings, err := ValidateGraphForRuntime(RuntimeClaude, loaded.Graph)
		if err != nil || claudeWarnings != nil {
			t.Errorf("%s under claude: warnings %q, err %v — the Claude path must stay silent and permissive", name, claudeWarnings, err)
		}

		warnings, err := ValidateGraphForRuntime(RuntimeCodex, loaded.Graph)
		switch {
		case want.refusedFor == "" && err != nil:
			t.Errorf("%s is refused under codex but should load (%s): %v", name, want.why, err)
		case want.refusedFor != "" && err == nil:
			t.Errorf("%s loads under codex but should be refused for %s (%s)", name, want.refusedFor, want.why)
		case want.refusedFor != "" && !strings.Contains(err.Error(), want.refusedFor):
			t.Errorf("%s refused under codex for the wrong reason: want %s (%s), got %v", name, want.refusedFor, want.why, err)
		}
		if got := len(warnings) > 0; got != want.warnsBudget {
			t.Errorf("%s under codex: budget warnings = %v, want %v (%s); warnings were %q", name, got, want.warnsBudget, want.why, warnings)
		}
		for _, warning := range warnings {
			// Every accepted cap must say which guard survives it, or the
			// acceptance reads as "this node is now unbounded".
			if !strings.Contains(warning, "timeout") {
				t.Errorf("%s: warning %q does not name the surviving timeout guard", name, warning)
			}
		}
	}
}

// assertTableCoversShippedGraphs fails when graphs/ and the expectation table
// disagree in either direction — a graph shipped with no recorded verdict, or
// a row left behind by a deleted graph.
func assertTableCoversShippedGraphs(t *testing.T, names []string) {
	t.Helper()
	shipped := make(map[string]bool, len(names))
	for _, name := range names {
		shipped[name] = true
		if _, ok := shippedGraphsUnderCodex[name]; !ok {
			t.Errorf("shipped graph %s has no row in shippedGraphsUnderCodex: decide what the codex runtime owes it, then record it", name)
		}
	}
	var stale []string
	for name := range shippedGraphsUnderCodex {
		if !shipped[name] {
			stale = append(stale, name)
		}
	}
	sort.Strings(stale)
	if len(stale) > 0 {
		t.Errorf("shippedGraphsUnderCodex names graphs that are no longer shipped: %v", stale)
	}
}

// shippedTemplateNames lists the graph templates the binary carries, skipping
// graphs/fragments/*.yaml: a fragment is a single-node definition that only
// loads through a template citing it (ADR 0013), so it has no runtime verdict
// of its own — its budget_usd is judged inside every template that splices it.
func shippedTemplateNames(t *testing.T) []string {
	t.Helper()
	var names []string
	err := fs.WalkDir(graphs.FS, ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && !strings.Contains(path, "/") {
			names = append(names, path)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read embedded graphs: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("no graphs are embedded — the //go:embed patterns in graphs/embed.go matched nothing")
	}
	sort.Strings(names)
	return names
}
