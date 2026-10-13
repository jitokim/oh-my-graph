package graph_test

import (
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/graphs"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// TestShippedBashPrefixNodesGetTheCompoundShellRule (#380): every claude-run
// node a shipped graph grants a scoped Bash rule, and no blanket Bash, gets the
// one-command-per-Bash-call rule exactly when its effective permission mode is
// dontAsk — asked through runner.NeedsCompoundShellRule, the one predicate the
// argv, dry-run, the plan screen and the run record share, with the mode from
// schedule.EffectivePermissionMode, the fallback the scheduler spawns under.
// merge-shepherd's ready-and-wait, the node whose `gh pr ready …; echo` was
// denied, must be among them, so the test cannot pass by finding none.
//
// This is an external test package because runner imports graph.
func TestShippedBashPrefixNodesGetTheCompoundShellRule(t *testing.T) {
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

	var ruled []string
	for _, name := range names {
		loaded, err := graph.LoadFile(filepath.Join("..", "..", "graphs", name))
		if err != nil {
			t.Fatalf("load %s: %v", name, err)
		}
		for _, n := range loaded.Graph.Nodes {
			if n.Type != graph.TypeClaudeRun || !scopedBashOnly(n.AllowedTools) {
				continue
			}
			mode := schedule.EffectivePermissionMode(n, schedule.DefaultPermissionMode)
			want := mode == graph.PermissionDontAsk
			if got := runner.NeedsCompoundShellRule(mode, n.AllowedTools); got != want {
				t.Errorf("%s node %q (mode %s, allowed_tools %q): gets the compound shell rule = %v, want %v",
					name, n.ID, mode, n.AllowedTools, got, want)
			}
			if want {
				ruled = append(ruled, name+"/"+n.ID)
			}
		}
	}
	if !slices.Contains(ruled, "merge-shepherd.yaml/ready-and-wait") {
		t.Errorf("merge-shepherd's ready-and-wait does not get the compound shell rule; nodes that do: %q", ruled)
	}
}

// scopedBashOnly reports whether tools holds at least one scoped Bash rule
// and no blanket `Bash` or `Bash(*)`.
func scopedBashOnly(tools []string) bool {
	scoped := false
	for _, tool := range tools {
		switch {
		case tool == "Bash" || tool == "Bash(*)":
			return false
		case strings.HasPrefix(tool, "Bash("):
			scoped = true
		}
	}
	return scoped
}
