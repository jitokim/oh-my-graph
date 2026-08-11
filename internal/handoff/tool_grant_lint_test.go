package handoff

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// TestLintToolGrants_ObservabilityOfADenial drives the predicate through one
// single-node graph per shape: the warning fires only when the node has
// NEITHER of the two things that can make a tool denial visible — its own
// grant, or a check the engine runs outside the node's reply. This is the
// #154 shape: the node was denied every tool it reached for, said so in prose,
// exited 0, and passed its own `result_matches`.
func TestLintToolGrants_ObservabilityOfADenial(t *testing.T) {
	cases := []struct {
		name  string
		node  string // YAML fields spliced into the single node
		warns bool
	}{
		{
			name:  "neither a grant nor a verify",
			node:  "prompt: \"Push this branch and open a PR with gh\"\n    success_check: { exit_zero: true, result_matches: '^DONE' }",
			warns: true,
		},
		{
			name:  "no declarations at all",
			node:  `prompt: "Build the binary and run the smoke graph"`,
			warns: true,
		},
		{
			name:  "an empty grant list is no grant",
			node:  "prompt: \"Push this branch\"\n    allowed_tools: []",
			warns: true,
		},
		{
			name:  "a declared grant stays silent",
			node:  "prompt: \"Push this branch\"\n    allowed_tools: [\"Bash(git push*)\"]",
			warns: false,
		},
		{
			name:  "a verify stays silent — the engine checks outside the reply",
			node:  "prompt: \"Build the binary\"\n    success_check: { exit_zero: true, verify: { command: \"test -x bin/oh-my-graph\" } }",
			warns: false,
		},
		{
			name:  "bypassPermissions cannot be denied anything",
			node:  "prompt: \"Push this branch\"\n    permission_mode: bypassPermissions",
			warns: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := parseGraph(t, "name: grants\nnodes:\n  - id: only\n    "+tc.node+"\n")
			warnings := LintToolGrants(g)
			if !tc.warns {
				if len(warnings) != 0 {
					t.Fatalf("expected no tool-grant warning, got %v", warnings)
				}
				return
			}
			if len(warnings) != 1 {
				t.Fatalf("expected exactly one warning, got %v", warnings)
			}
			w := warnings[0]
			if w.NodeID != "only" || w.Field != "allowed_tools" {
				t.Fatalf("warning = %+v, want node only field allowed_tools", w)
			}
			if !strings.Contains(w.Detail, "name the tools this node needs") {
				t.Fatalf("detail %q should tell the author what to do, not only what is missing", w.Detail)
			}
		})
	}
}

// TestLintToolGrants_SkipsGateNodes pins the other structural exemption: a
// gate node spawns no subprocess, so it has no tools to be denied — warning
// about its missing grant would be advice with no action behind it.
func TestLintToolGrants_SkipsGateNodes(t *testing.T) {
	g := parseGraph(t, `
name: gated
nodes:
  - id: work
    prompt: do the thing
    allowed_tools: [Read]
  - id: approve
    type: gate
    depends_on: [work]
`)
	if warnings := LintToolGrants(g); len(warnings) != 0 {
		t.Fatalf("a gate node has no tools to grant, got %v", warnings)
	}
}

// TestLintToolGrants_ShippedGraphsAreClean is the sweep's own regression test,
// mirroring TestLintVerdicts_ShippedGraphsAreClean: every graph this repo ships
// must already declare what its nodes reach for, so a shipped node that could
// not observe its own denials fails here rather than in a paid run. Loaded
// through graph.LoadFile so `use:` fragments are resolved in (ADR 0013) — the
// spliced fragment nodes are judged here too.
func TestLintToolGrants_ShippedGraphsAreClean(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "..", "graphs", "*.yaml"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no shipped graphs found to sweep: %v", err)
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			loaded, err := graph.LoadFile(path)
			if err != nil {
				t.Fatalf("load %s: %v", path, err)
			}
			if warnings := LintToolGrants(loaded.Graph); len(warnings) != 0 {
				t.Fatalf("%s: shipped graph should have no tool-grant advisories, got %v", path, warnings)
			}
		})
	}
}
