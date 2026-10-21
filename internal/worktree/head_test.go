package worktree

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// isolateGitConfig pins git to a fixed identity and no user/system config, so
// these tests read the same on any machine. GIT_CEILING_DIRECTORIES stops
// discovery at the temp root, so a "plain dir" can never resolve to a
// repository that happens to enclose the system temp dir.
func isolateGitConfig(t *testing.T, ceiling string) {
	t.Helper()
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(t.TempDir(), "no-gitconfig"))
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_AUTHOR_NAME", "test")
	t.Setenv("GIT_AUTHOR_EMAIL", "test@example.com")
	t.Setenv("GIT_COMMITTER_NAME", "test")
	t.Setenv("GIT_COMMITTER_EMAIL", "test@example.com")
	t.Setenv("GIT_CEILING_DIRECTORIES", ceiling)
}

func TestGitManager_HeadCommitOfRepo(t *testing.T) {
	isolateGitConfig(t, "")
	repo := initRepo(t)
	m := NewGitManager("", "", "")

	got := m.HeadCommit(context.Background(), repo)
	if want := gitIn(t, repo, "rev-parse", "HEAD"); got != want {
		t.Fatalf("HeadCommit = %q, want %q", got, want)
	}
}

func TestGitManager_HeadCommitOfLinkedWorktree(t *testing.T) {
	isolateGitConfig(t, "")
	repo := initRepo(t)
	wt := filepath.Join(t.TempDir(), "lane")
	gitIn(t, repo, "worktree", "add", "-q", wt, "-b", "lane", "HEAD")
	gitIn(t, wt, "commit", "--allow-empty", "-q", "-m", "lane work")

	mainSHA := gitIn(t, repo, "rev-parse", "HEAD")
	wtSHA := gitIn(t, wt, "rev-parse", "HEAD")
	if mainSHA == wtSHA {
		t.Fatal("setup: the worktree commit must move its HEAD off the main checkout's")
	}

	if got := NewGitManager("", "", "").HeadCommit(context.Background(), wt); got != wtSHA {
		t.Fatalf("HeadCommit(worktree) = %q, want the worktree's %q (main is %q)", got, wtSHA, mainSHA)
	}
}

func TestGitManager_HeadCommitOutsideARepoIsEmpty(t *testing.T) {
	root := t.TempDir()
	isolateGitConfig(t, root)
	plain := filepath.Join(root, "plain")
	if err := os.Mkdir(plain, 0o700); err != nil {
		t.Fatal(err)
	}
	if got := NewGitManager("", "", "").HeadCommit(context.Background(), plain); got != "" {
		t.Fatalf("HeadCommit outside a repo = %q, want \"\"", got)
	}
}

func TestGitManager_HeadCommitOfMissingDirIsEmpty(t *testing.T) {
	isolateGitConfig(t, "")
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	if got := NewGitManager("", "", "").HeadCommit(context.Background(), missing); got != "" {
		t.Fatalf("HeadCommit of a missing dir = %q, want \"\"", got)
	}
}

func TestGitManager_HeadCommitOfUnbornHeadIsEmpty(t *testing.T) {
	isolateGitConfig(t, "")
	repo := t.TempDir()
	gitIn(t, repo, "init", "-q", "-b", "main")
	if got := NewGitManager("", "", "").HeadCommit(context.Background(), repo); got != "" {
		t.Fatalf("HeadCommit with no commits = %q, want \"\"", got)
	}
}

func TestIsFullSHA(t *testing.T) {
	cases := map[string]bool{
		"0123456789abcdef0123456789abcdef01234567":                         true,
		"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef": true,
		"0123456789ABCDEF0123456789ABCDEF01234567":                         true,
		"0123456789abcdef":                         false,
		"warning: x\n0123456789abcdef0123456789a":  false,
		"g123456789abcdef0123456789abcdef01234567": false,
		"": false,
	}
	for in, want := range cases {
		if got := isFullSHA(in); got != want {
			t.Errorf("isFullSHA(%q) = %v, want %v", in, got, want)
		}
	}
}
