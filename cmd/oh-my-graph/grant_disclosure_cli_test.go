package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// grantFragment is the #196 shape: the fragment invites the grant in, the
// citing node supplies it, and neither file alone says what the node may run.
const grantFragment = `fragment: tools
description: a gate whose grant is half the citing graph's
substitutions: [extra]
node:
  prompt: "check the work"
  allowed_tools: [Read, "{{ with.extra }}"]
  success_check: { exit_zero: true }
`

// verbatimFragment is the control every case below pairs with: same machinery,
// same binding, grant written out in one file.
const verbatimFragment = `fragment: tools
description: a gate whose grant is its own
substitutions: [extra]
node:
  prompt: "check the work and {{ with.extra }}"
  allowed_tools: [Read, "Bash(go *)"]
  success_check: { exit_zero: true }
`

// grantLoopFragment parameterizes one of two spliced nodes' grants.
const grantLoopFragment = `fragment: lanes
description: build then review, one parameterized grant
substitutions: [extra]
exit: review
nodes:
  - id: build
    prompt: "do the work"
    allowed_tools: [Read, "{{ with.extra }}"]
  - id: review
    depends_on: [build]
    prompt: "review {{ artifacts.build }}"
    allowed_tools: [Read]
`

const grantCitingGraph = "name: g\nnodes:\n" +
	"  - { id: dev, prompt: build }\n" +
	`  - { id: x, use: tools, depends_on: [dev], with: { extra: "Bash(go *)" } }` + "\n"

const grantLoopCitingGraph = "name: g\nnodes:\n" +
	"  - { id: dev, prompt: build }\n" +
	`  - { id: x, use: lanes, depends_on: [dev], with: { extra: "Bash(go *)" } }` + "\n"

// TestPrintFragmentResolutions_NamesAnAssembledGrant pins the clause's text for
// both fragment forms, and the negative that keeps it narrow: a resolution
// carrying no assembled grant prints exactly the line it printed before.
func TestPrintFragmentResolutions_NamesAnAssembledGrant(t *testing.T) {
	var single strings.Builder
	printFragmentResolutions(&single, []graph.FragmentResolution{{
		NodeID: "x", Fragment: "tools", Description: "a parameterized gate",
		Source: "graphs/fragments/tools.yaml",
		Grants: []graph.ResolvedGrant{{NodeID: "x", Tools: []string{"Read", "Bash(go *)"}}},
	}})
	// The single-node form's one grant belongs to the node the line already
	// names, so it prints bare.
	want := `fragment: node "x" spliced from "tools" (graphs/fragments/tools.yaml) — a parameterized gate` +
		" — allowed_tools resolved from with: Read, Bash(go *)\n"
	if got := single.String(); got != want {
		t.Errorf("single-node line =\n%q\nwant\n%q", got, want)
	}

	// The multi-node form qualifies each grant by its spliced id — "which of the
	// five" is the whole question there — and names only the assembled one.
	var loop strings.Builder
	printFragmentResolutions(&loop, []graph.FragmentResolution{{
		NodeID: "x", Fragment: "lanes", Description: "two lanes",
		Source:  "graphs/fragments/lanes.yaml",
		Spliced: []string{"x/build", "x/review"},
		Grants:  []graph.ResolvedGrant{{NodeID: "x/build", Tools: []string{"Read", "Bash(go *)"}}},
	}})
	want = `fragment: node "x" spliced from "lanes" (graphs/fragments/lanes.yaml) — two lanes` +
		" — nodes: x/build, x/review — allowed_tools resolved from with: x/build: Read, Bash(go *)\n"
	if got := loop.String(); got != want {
		t.Errorf("multi-node line =\n%q\nwant\n%q", got, want)
	}

	// No assembled grant, no clause: a verbatim grant is readable in one file
	// and a line per spliced node regardless is the line nobody reads.
	var verbatim strings.Builder
	printFragmentResolutions(&verbatim, []graph.FragmentResolution{{
		NodeID: "x", Fragment: "tools", Description: "a verbatim gate",
		Source: "graphs/fragments/tools.yaml",
	}})
	if strings.Contains(verbatim.String(), "allowed_tools") {
		t.Errorf("a verbatim grant must add no clause, got %q", verbatim.String())
	}
}

// TestPrintFragmentResolutions_AnEmptyResolvedGrantSaysSo — a binding that
// resolves the grant to nothing is a disclosure, not an absence, and an empty
// tail would read as a truncated line.
func TestPrintFragmentResolutions_AnEmptyResolvedGrantSaysSo(t *testing.T) {
	var out strings.Builder
	printFragmentResolutions(&out, []graph.FragmentResolution{{
		NodeID: "x", Fragment: "tools", Description: "a gate", Source: "f.yaml",
		Grants: []graph.ResolvedGrant{{NodeID: "x", Tools: nil}},
	}})
	if want := " — allowed_tools resolved from with: (none)\n"; !strings.HasSuffix(out.String(), want) {
		t.Errorf("line = %q, want suffix %q", out.String(), want)
	}
}

// TestGrantDisclosure_IsWiredThroughTheRealLoader holds the agreement no
// compiler checks: the resolver decides WHICH grants to record and under which
// ids, the printer decides the words, and the two meet only in a format string.
// Rename the field, change the id a splice mints, or reword the clause on one
// side and this reddens — a unit test of either half alone would not.
func TestGrantDisclosure_IsWiredThroughTheRealLoader(t *testing.T) {
	for _, tc := range []struct {
		name     string
		entry    string
		fragment map[string]string
		want     string
	}{
		{
			name: "single-node", entry: grantCitingGraph,
			fragment: map[string]string{"tools": grantFragment},
			want: `fragment: node "x" spliced from "tools" (%s) — a gate whose grant is half the citing graph's` +
				" — allowed_tools resolved from with: Read, Bash(go *)",
		},
		{
			name: "multi-node", entry: grantLoopCitingGraph,
			fragment: map[string]string{"lanes": grantLoopFragment},
			want: `fragment: node "x" spliced from "lanes" (%s) — build then review, one parameterized grant` +
				" — nodes: x/build, x/review — allowed_tools resolved from with: x/build: Read, Bash(go *)",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := writeFragmentGraphDir(t, "graph.yaml", tc.entry, tc.fragment)
			var name string
			for n := range tc.fragment {
				name = n
			}
			source := filepath.Join(filepath.Dir(path), "fragments", name+".yaml")
			want := strings.Replace(tc.want, "%s", source, 1)

			var out, warnings strings.Builder
			if err := lintGraph(&out, &warnings, path); err != nil {
				t.Fatalf("a parameterized grant must still lint valid: %v\n%s", err, out.String())
			}
			if !strings.Contains(out.String(), want) {
				t.Errorf("lint must print\n%s\ngot\n%s", want, out.String())
			}
		})
	}
}

// TestGrantDisclosure_ReachesEveryCommandThatResolvesFragments is the #185
// standard applied here: a disclosure that reaches only the call sites its
// author happened to find is the defect. All three commands that resolve a
// `use:` must show the assembled grant — and `run --dry-run` is the one that
// showed no fragment disclosure at all before this change.
func TestGrantDisclosure_ReachesEveryCommandThatResolvesFragments(t *testing.T) {
	const clause = "allowed_tools resolved from with: Read, Bash(go *)"

	t.Run("lint", func(t *testing.T) {
		path := writeFragmentGraphDir(t, "graph.yaml", grantCitingGraph, map[string]string{"tools": grantFragment})
		var out, warnings strings.Builder
		if err := lintGraph(&out, &warnings, path); err != nil {
			t.Fatalf("lint: %v", err)
		}
		if !strings.Contains(out.String(), clause) {
			t.Errorf("lint is silent about the assembled grant:\n%s", out.String())
		}
	})

	t.Run("dry-run", func(t *testing.T) {
		path := writeFragmentGraphDir(t, "graph.yaml", grantCitingGraph, map[string]string{"tools": grantFragment})
		var out, warnings strings.Builder
		if err := dryRunGraph(&out, &warnings, path, nil); err != nil {
			t.Fatalf("dry run: %v", err)
		}
		if !strings.Contains(out.String(), clause) {
			t.Errorf("--dry-run is silent about the assembled grant:\n%s", out.String())
		}
		// The whole splice disclosure was missing here, not only its new clause.
		if !strings.Contains(out.String(), `fragment: node "x" spliced from "tools"`) {
			t.Errorf("--dry-run must print the splice disclosure lint and run print:\n%s", out.String())
		}
	})

	t.Run("run", func(t *testing.T) {
		isolateRunHome(t)
		path := writeFragmentGraphDir(t, "graph.yaml", grantCitingGraph, map[string]string{"tools": grantFragment})
		fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
			"build":          {SessionID: "s-dev", Result: "ok", ExitCode: 0},
			"check the work": {SessionID: "s-x", Result: "ok", ExitCode: 0},
		})
		var err error
		out := captureStdout(t, func() {
			err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		if err != nil {
			t.Fatalf("run: %v", err)
		}
		if !strings.Contains(out, clause) {
			t.Errorf("run is silent about the assembled grant:\n%s", out)
		}
		// The grant really is what the node ran under, not a line about one.
		if n := len(fake.Invocations()); n != 2 {
			t.Fatalf("%d nodes invoked, want 2", n)
		}
		var granted []string
		for _, invocation := range fake.Invocations() {
			if invocation.Prompt == "check the work" {
				granted = invocation.Policy.AllowedTools
			}
		}
		if !strings.Contains(strings.Join(granted, ","), "Bash(go *)") {
			t.Errorf("the spliced node ran with %v, which is not what the line announced", granted)
		}
	})
}

// TestGrantDisclosure_VerbatimGrantStaysSilentOnEveryCommand is the negative
// control at the CLI level. Without it, an implementation that printed every
// spliced node's grant would pass every case above.
func TestGrantDisclosure_VerbatimGrantStaysSilentOnEveryCommand(t *testing.T) {
	entry := "name: g\nnodes:\n" +
		"  - { id: dev, prompt: build }\n" +
		`  - { id: x, use: tools, depends_on: [dev], with: { extra: "run make local" } }` + "\n"

	path := writeFragmentGraphDir(t, "graph.yaml", entry, map[string]string{"tools": verbatimFragment})

	var lintOut, lintWarn strings.Builder
	if err := lintGraph(&lintOut, &lintWarn, path); err != nil {
		t.Fatalf("lint: %v", err)
	}
	var dryOut, dryWarn strings.Builder
	if err := dryRunGraph(&dryOut, &dryWarn, path, nil); err != nil {
		t.Fatalf("dry run: %v", err)
	}
	for name, out := range map[string]string{"lint": lintOut.String(), "dry-run": dryOut.String()} {
		if strings.Contains(out, "allowed_tools resolved from with") {
			t.Errorf("%s announced a grant readable in one file:\n%s", name, out)
		}
		// Still spliced, so the silence is about the grant and not about a
		// resolution that never happened.
		if !strings.Contains(out, `spliced from "tools"`) {
			t.Errorf("%s must still disclose the splice:\n%s", name, out)
		}
	}
}
