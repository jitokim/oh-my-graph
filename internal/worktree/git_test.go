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

// TestGitManager_AcquireReusesExistingWorktreeDir pins the disk-aware half of
// Acquire's idempotency: a fresh process (a resume leg) re-declaring a name
// whose managed dir survived on disk must reuse it, not die trying to
// re-create it — and its cleanup must retain the adopted lane's branch, since
// nothing proves the lane empty.
func TestGitManager_AcquireReusesExistingWorktreeDir(t *testing.T) {
	m1, repo := newTestManager(t)
	first, err := m1.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("first-leg Acquire: %v", err)
	}

	// A fresh process: same repo, same managed base, same run id, empty map.
	m2 := NewGitManager(repo, m1.baseDir, "test-run")
	second, err := m2.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("resume-leg Acquire must reuse the existing dir: %v", err)
	}
	if first != second {
		t.Fatalf("resume leg resolved a different path: %q vs %q", first, second)
	}
	if got := gitIn(t, second, "rev-parse", "--abbrev-ref", "HEAD"); got != "omg/test-run/lane" {
		t.Errorf("reused worktree is on branch %q, want omg/test-run/lane", got)
	}

	notes := m2.Cleanup(context.Background())
	if !branchExists(t, repo, "omg/test-run/lane") {
		t.Fatalf("adopted lane's branch was deleted at cleanup — it was never provably empty")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "retained") {
		t.Errorf("adopted-lane retention must be documented, got notes: %v", notes)
	}
}

// TestGitManager_AcquireAttachesRetainedBranch replays run 20260802-104005:
// a paused leg's cleanup removed the worktree dir but retained the branch
// (it carries commits), and the resume leg re-declares the name. Acquire must
// ATTACH to the retained branch — continuing the lane's committed state —
// instead of colliding on the ref with -b.
func TestGitManager_AcquireAttachesRetainedBranch(t *testing.T) {
	m1, repo := newTestManager(t)
	path, err := m1.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("first-leg Acquire: %v", err)
	}
	if err := os.WriteFile(filepath.Join(path, "work.txt"), []byte("work"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	gitIn(t, path, "add", "work.txt")
	gitIn(t, path, "commit", "-q", "-m", "paused-leg work")
	m1.Cleanup(context.Background())
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("precondition: cleanup should have removed the worktree dir")
	}
	if !branchExists(t, repo, "omg/test-run/lane") {
		t.Fatal("precondition: cleanup should have retained the branch")
	}

	m2 := NewGitManager(repo, m1.baseDir, "test-run")
	resumed, err := m2.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("resume-leg Acquire must attach the retained branch: %v", err)
	}
	if resumed != path {
		t.Fatalf("resume leg resolved a different path: %q vs %q", resumed, path)
	}
	if got := gitIn(t, resumed, "rev-parse", "--abbrev-ref", "HEAD"); got != "omg/test-run/lane" {
		t.Errorf("attached worktree is on branch %q, want omg/test-run/lane", got)
	}
	// The lane continues its committed state — the paused leg's work is there.
	if _, err := os.Stat(filepath.Join(resumed, "work.txt")); err != nil {
		t.Fatalf("attached worktree does not carry the paused leg's committed work: %v", err)
	}

	notes := m2.Cleanup(context.Background())
	if !branchExists(t, repo, "omg/test-run/lane") {
		t.Fatalf("attached lane's branch was deleted at cleanup — the paused leg's commits lost")
	}
	if len(notes) != 1 || !strings.Contains(notes[0], "retained") {
		t.Errorf("attached-lane retention must be documented, got notes: %v", notes)
	}
}

// TestGitManager_AcquireRefusesForeignDir pins the fail-loudly remainder: a
// directory squatting on the managed path that is NOT a worktree of the
// invocation repo is refused, never adopted — adopting it could mix or reset
// work that is not the run's own.
func TestGitManager_AcquireRefusesForeignDir(t *testing.T) {
	t.Run("plain directory", func(t *testing.T) {
		m, _ := newTestManager(t)
		path := filepath.Join(m.baseDir, "lane")
		if err := os.MkdirAll(path, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(path, "stray.txt"), []byte("x"), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}

		if _, err := m.Acquire(context.Background(), "lane"); err == nil {
			t.Fatal("Acquire must refuse to adopt a non-worktree directory")
		}
		if _, err := os.Stat(filepath.Join(path, "stray.txt")); err != nil {
			t.Errorf("the refused directory's contents were touched: %v", err)
		}
	})

	t.Run("worktree of another repository", func(t *testing.T) {
		m, _ := newTestManager(t)
		other := initRepo(t)
		path := filepath.Join(m.baseDir, "lane")
		if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
			t.Fatalf("mkdir base: %v", err)
		}
		gitIn(t, other, "worktree", "add", "-q", path, "-b", "other-branch", "HEAD")

		if _, err := m.Acquire(context.Background(), "lane"); err == nil {
			t.Fatal("Acquire must refuse to adopt another repository's worktree")
		}
	})
}

// TestGitPathAbs pins the portable stand-in for rev-parse's
// --path-format=absolute (git < 2.31): a path git printed relative is
// anchored to the directory the command ran in, an absolute one passes
// through, and "" anchors to the process working directory — matching
// gitCmd's cmd.Dir semantics.
func TestGitPathAbs(t *testing.T) {
	if got := gitPathAbs("/repo", ".git"); got != "/repo/.git" {
		t.Errorf("relative path not anchored to the command's dir: %q", got)
	}
	if got := gitPathAbs("/repo", "/elsewhere/.git"); got != "/elsewhere/.git" {
		t.Errorf("absolute path must pass through untouched: %q", got)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if got := gitPathAbs("", ".git"); got != filepath.Join(wd, ".git") {
		t.Errorf("empty dir must anchor to the process working directory: %q", got)
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
			"OPENAI_API_KEY=sk-openai-secret",
			"CODEX_API_KEY=sk-codex-secret",
			"HOME=/home/u",
		}
	}

	cmd := m.gitCmd(context.Background(), "status")
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "ANTHROPIC_API_KEY=") || strings.HasPrefix(kv, "ANTHROPIC_AUTH_TOKEN=") ||
			strings.HasPrefix(kv, "OPENAI_API_KEY=") || strings.HasPrefix(kv, "CODEX_API_KEY=") {
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

// TestGitManager_AcquireCreatesTheManagedBaseOwnerOnly pins the at-rest mode of
// the managed base dir. It lives under the run directory and holds each lane's
// checkout of the user's own source, so it gets the same owner-only treatment
// as the rest of the run directory.
func TestGitManager_AcquireCreatesTheManagedBaseOwnerOnly(t *testing.T) {
	m, _ := newTestManager(t)
	if _, err := m.Acquire(context.Background(), "lane"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	info, err := os.Stat(m.baseDir)
	if err != nil {
		t.Fatalf("stat managed base dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o700 {
		t.Errorf("managed base dir mode = %v, want 0700.", got)
	}
}

// TestGitManager_AcquireLeavesAnExistingManagedBaseAlone is the compatibility
// half: MkdirAll does not chmod a directory that already exists, so a resume
// leg adopts the base an older binary created without re-moding it. Nothing
// about resuming a run that predates the narrowing changes.
func TestGitManager_AcquireLeavesAnExistingManagedBaseAlone(t *testing.T) {
	m, _ := newTestManager(t)
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		t.Fatalf("seed legacy managed base dir: %v", err)
	}
	// MkdirAll's mode is umask-masked, so without this the fixture would be the
	// developer's umask rather than the 0755 this test is about. chmod(2) is not
	// masked.
	if err := os.Chmod(m.baseDir, 0o755); err != nil {
		t.Fatalf("chmod legacy managed base dir: %v", err)
	}

	if _, err := m.Acquire(context.Background(), "lane"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}

	info, err := os.Stat(m.baseDir)
	if err != nil {
		t.Fatalf("stat managed base dir: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o755 {
		t.Errorf("legacy managed base dir mode = %v, want it untouched at 0755.", got)
	}
}
