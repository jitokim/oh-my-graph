package main

import (
	"strings"
	"testing"
)

// --- reportWorktreeCleanup formatting -----------------------------------------
//
// reportWorktreeCleanup's input is the []string GitManager.Cleanup returns; by
// the time it runs, each note is already finished prose whose wording
// internal/worktree owns and tests against a real repository
// (git_test.go's Cleanup* tests). The honest seam at the cmd layer is
// therefore the notes parameter itself, not a fake manager: worktree.FakeManager
// never sits behind this function (executeGraph/executeResume call it on the
// concrete GitManager's Cleanup only), so driving one here would test a wiring
// that does not exist. These tests hand it note strings in the three shapes
// cleanupOne actually produces and pin what this function alone adds — one
// "worktree cleanup: "-prefixed line per note, in order, and not a byte when
// there is nothing to say.

func TestReportWorktreeCleanup_OnePrefixedLinePerNoteShape(t *testing.T) {
	// One note per real cleanupOne outcome that produces one: a dirty worktree
	// kept in place, a removed worktree whose branch is retained because it
	// carries commits, and a removed worktree whose empty-branch delete failed.
	notes := []string{
		`worktree "lane-a" kept at /runs/r1/worktrees/lane-a (git worktree remove: exit status 128); inspect it, then remove it with ` + "`git worktree remove --force /runs/r1/worktrees/lane-a`",
		`worktree "lane-b" removed; branch omg/r1/lane-b retained — it carries commits (merge or cherry-pick it, or delete it with ` + "`git branch -D omg/r1/lane-b`)",
		`worktree "lane-c" removed, but deleting its empty branch omg/r1/lane-c failed: exit status 1`,
	}

	var out strings.Builder
	reportWorktreeCleanup(&out, notes)

	lines := strings.Split(strings.TrimSuffix(out.String(), "\n"), "\n")
	if len(lines) != len(notes) {
		t.Fatalf("got %d lines for %d notes:\n%s", len(lines), len(notes), out.String())
	}
	for i, note := range notes {
		if want := "worktree cleanup: " + note; lines[i] != want {
			t.Errorf("line %d = %q, want %q", i, lines[i], want)
		}
	}
}

func TestReportWorktreeCleanup_SilentWhenNothingToSay(t *testing.T) {
	// Both ways Cleanup reports "nothing worth a note": the empty slice it
	// returns when every removal was silent, and nil for good measure — the
	// common case (no worktree nodes at all) must not print even a header.
	for _, notes := range [][]string{{}, nil} {
		var out strings.Builder
		reportWorktreeCleanup(&out, notes)
		if got := out.String(); got != "" {
			t.Errorf("reportWorktreeCleanup(%#v) wrote %q, want no output at all", notes, got)
		}
	}
}
