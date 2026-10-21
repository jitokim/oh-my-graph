package main

import (
	"context"
	"os"

	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// launchCommit returns the full HEAD sha of the git repository containing
// dir, or "" — silently — when dir is in no repository or anything goes
// wrong. `auto` and `design` call it once at launch and hand the result to
// coordinator.WithStartCommit, so every cycle of one goal measures scope from
// the same commit (#414).
//
// It asks git through worktree.GitManager, the third exec seam (ADR 0005), so
// the CLI itself still spawns nothing.
func launchCommit(dir string) string {
	return worktree.NewGitManager("", "", "").HeadCommit(context.Background(), dir)
}

// launchCommitOfWorkingDir is launchCommit on the invocation directory, ""
// when even that cannot be found.
func launchCommitOfWorkingDir() string {
	wd, err := os.Getwd()
	if err != nil {
		return ""
	}
	return launchCommit(wd)
}
