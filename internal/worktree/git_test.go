package worktree

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// initRepo creates a temp git repository with one commit and returns its path.
// Real git is fine here — like verify's shell tests, this package's contract
// IS the subprocess boundary; only claude spawns are banned from CI.
func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "test")
	run("commit", "--allow-empty", "-q", "-m", "base")
	return dir
}

// newTestManager builds a GitManager against a fresh repo with its managed
// base under a second temp dir, returning both.
func newTestManager(t *testing.T) (*GitManager, string) {
	t.Helper()
	repo := initRepo(t)
	m := NewGitManager(repo, filepath.Join(t.TempDir(), "worktrees"), "test-run")
	return m, repo
}

// gitIn runs a git command in dir and returns its trimmed output, failing the
// test on error.
func gitIn(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// branchExists reports whether the repo has the named branch.
func branchExists(t *testing.T, repo, branch string) bool {
	t.Helper()
	cmd := exec.Command("git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	cmd.Dir = repo
	return cmd.Run() == nil
}

func TestGitManager_AcquireCreatesOneWorktreePerName(t *testing.T) {
	m, repo := newTestManager(t)

	first, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("first Acquire: %v", err)
	}
	second, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("second Acquire: %v", err)
	}
	if first != second {
		t.Fatalf("same name resolved to two paths: %q vs %q", first, second)
	}
	if _, err := os.Stat(first); err != nil {
		t.Fatalf("worktree dir was not created: %v", err)
	}
	if got := gitIn(t, first, "rev-parse", "--abbrev-ref", "HEAD"); got != "omg/test-run/lane" {
		t.Errorf("worktree is on branch %q, want omg/test-run/lane", got)
	}
	// One creation, not two: git itself would refuse a second add on the same
	// branch, so the second Acquire returning cleanly proves the dedup.
	if !branchExists(t, repo, "omg/test-run/lane") {
		t.Error("worktree branch missing from the repo")
	}
}

func TestGitManager_DifferentNamesGetDifferentWorktrees(t *testing.T) {
	m, _ := newTestManager(t)

	a, err := m.Acquire(context.Background(), "lane-a")
	if err != nil {
		t.Fatalf("Acquire lane-a: %v", err)
	}
	b, err := m.Acquire(context.Background(), "lane-b")
	if err != nil {
		t.Fatalf("Acquire lane-b: %v", err)
	}
	if a == b {
		t.Fatalf("different names resolved to the same path %q", a)
	}
	// Both are real, independent checkouts: a file written in one is not in
	// the other — the no-shared-tree property parallel edit lanes rely on.
	if err := os.WriteFile(filepath.Join(a, "only-in-a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatalf("write in lane-a: %v", err)
	}
	if _, err := os.Stat(filepath.Join(b, "only-in-a.txt")); !os.IsNotExist(err) {
		t.Errorf("lane-b sees lane-a's file — the worktrees share a tree")
	}
}

func TestGitManager_CleanupRemovesEmptyWorktreeAndBranchSilently(t *testing.T) {
	m, repo := newTestManager(t)
	path, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	notes := m.Cleanup(context.Background())
	if len(notes) != 0 {
		t.Errorf("clean removal should be silent, got notes: %v", notes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after cleanup")
	}
	if branchExists(t, repo, "omg/test-run/lane") {
		t.Errorf("empty branch was not deleted")
	}
}

func TestGitManager_CleanupRetainsBranchThatCarriesCommits(t *testing.T) {
	m, repo := newTestManager(t)
	path, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, path, "add", "work.txt")
	gitIn(t, path, "commit", "-q", "-m", "node work")

	notes := m.Cleanup(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir with committed work should still be removed (the commits live on the branch)")
	}
	if !branchExists(t, repo, "omg/test-run/lane") {
		t.Fatalf("branch carrying commits was deleted — work lost")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "omg/test-run/lane") || !strings.Contains(notes[0], "retained") {
		t.Errorf("retention must be documented, got notes: %v", notes)
	}
}

func TestGitManager_CleanupAfterBranchRenameRemovesEmptyLaneSilently(t *testing.T) {
	m, repo := newTestManager(t)
	path, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	// The node renamed the branch Acquire created; the lane is still empty.
	gitIn(t, path, "branch", "-m", "feature/renamed")

	notes := m.Cleanup(context.Background())
	if len(notes) != 0 {
		t.Errorf("empty lane with a renamed branch should still be removed silently, got notes: %v", notes)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("worktree dir still exists after cleanup")
	}
	if branchExists(t, repo, "feature/renamed") {
		t.Errorf("empty renamed branch was not deleted")
	}
}

func TestGitManager_CleanupAfterBranchRenameRetainsCommits(t *testing.T) {
	m, repo := newTestManager(t)
	path, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	gitIn(t, path, "branch", "-m", "feature/renamed")
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, path, "add", "work.txt")
	gitIn(t, path, "commit", "-q", "-m", "node work")

	notes := m.Cleanup(context.Background())
	if !branchExists(t, repo, "feature/renamed") {
		t.Fatalf("renamed branch carrying commits was deleted — work lost")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "feature/renamed") || !strings.Contains(notes[0], "carries commits") {
		t.Errorf("retention must name the current branch and the reason, got notes: %v", notes)
	}
}

func TestGitManager_CleanupKeepsDirtyWorktreeInPlace(t *testing.T) {
	m, _ := newTestManager(t)
	path, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "uncommitted.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	notes := m.Cleanup(context.Background())
	if _, err := os.Stat(filepath.Join(path, "uncommitted.txt")); err != nil {
		t.Fatalf("uncommitted work was destroyed by cleanup: %v", err)
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "kept") {
		t.Errorf("a kept worktree must be documented, got notes: %v", notes)
	}
}

func TestGitManager_AcquireOutsideARepoFails(t *testing.T) {
	m := NewGitManager(t.TempDir(), filepath.Join(t.TempDir(), "worktrees"), "test-run")
	if _, err := m.Acquire(context.Background(), "lane"); err == nil {
		t.Fatal("Acquire outside a git repository must fail, not invent a worktree")
	}
}

// TestGitManager_ScrubsBillingVarsFromGitChildren asserts the call-site half
// of the subscription-auth guarantee for the third spawner: the billing-
// switching variables are deleted from every git child's env (a repo's own
// hooks run under it and may legitimately invoke claude).
func TestGitManager_ScrubsBillingVarsFromGitChildren(t *testing.T) {
	m := NewGitManager("", "", "test-run")
	m.environ = func() []string {
		return []string{
			"ANTHROPIC_API_KEY=sk-live-secret",
			"ANTHROPIC_AUTH_TOKEN=tok-secret",
			"HOME=/home/u",
		}
	}

	cmd := m.gitCmd(context.Background(), "status")
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") {
			t.Errorf("billing variable leaked into the git child env: %s", kv)
		}
	}
	found := false
	for _, kv := range cmd.Env {
		if kv == "HOME=/home/u" {
			found = true
		}
	}
	if !found {
		t.Error("scrub removed more than the billing variables")
	}
}
