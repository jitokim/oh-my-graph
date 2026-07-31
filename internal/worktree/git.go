package worktree

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jitokim/oh-my-graph/internal/childenv"
)

// gitTimeout bounds each individual git command. Worktree add/remove are local
// operations that finish in well under a second; the bound keeps a wedged git
// (waiting on an index.lock, a slow hook) from stalling a node — or the run's
// cleanup — indefinitely.
const gitTimeout = 30 * time.Second

// GitManager is the production Provider: it materializes each declared
// worktree name as a real `git worktree add` off the invocation repo's HEAD,
// once per unique name, and tears everything down at run end without ever
// losing work. It is the only object in this package that spawns a process,
// and one of exactly three in the project (the others: runner.ClaudeCLIRunner
// and verify.ShellVerifier) — see docs/adr/0005.
//
// Construct it in cmd/oh-my-graph and inject it; the Scheduler never builds
// one, so no test picks up a real spawn by accident.
type GitManager struct {
	// repoDir is where git commands run — the invocation repository. Empty
	// means the process's own working directory, which is the production
	// value: a worktree is always created off the repo oh-my-graph was
	// invoked from.
	repoDir string
	// baseDir is the managed directory every worktree lives under —
	// <run-dir>/worktrees in production, NEVER the user's checked-out tree.
	baseDir string
	// branchPrefix namespaces the fresh branch each worktree is created on
	// ("omg/<run-id>"), so two runs' lanes can never collide on a ref and a
	// retained branch names the run it came from.
	branchPrefix string
	// environ supplies the parent environment to scrub. A field (defaulting
	// to os.Environ) so a test can inject a parent env that DOES carry the
	// API keys and then assert they are absent from the built child env.
	environ func() []string

	mu      sync.Mutex
	created map[string]*managedWorktree
}

// managedWorktree records what Acquire created for one name — everything
// Cleanup needs to tear it down without guessing.
type managedWorktree struct {
	path   string
	branch string
	// baseSHA is the commit the worktree's branch started at. Cleanup
	// compares the worktree's HEAD against it: a worktree still at its base
	// is provably empty and safe to delete; anything else carries commits and
	// its branch is retained.
	baseSHA string
}

// NewGitManager builds the production Provider. repoDir is the repository to
// create worktrees off ("" = the process's working directory); baseDir is the
// managed directory the worktrees live under; runID namespaces the branches.
func NewGitManager(repoDir, baseDir, runID string) *GitManager {
	return &GitManager{
		repoDir:      repoDir,
		baseDir:      baseDir,
		branchPrefix: "omg/" + runID,
		environ:      os.Environ,
		created:      make(map[string]*managedWorktree),
	}
}

// Acquire returns the path of the managed worktree for name, creating it —
// `git worktree add <baseDir>/<name> -b <branchPrefix>/<name> HEAD` — the
// first time the name is seen. The lock covers creation, so two nodes racing
// on the same name cannot double-create; the loser of the race just gets the
// same path back.
func (m *GitManager) Acquire(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if wt, ok := m.created[name]; ok {
		return wt.path, nil
	}

	baseSHA, err := m.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("worktree %q: resolve invocation repo HEAD: %w", name, err)
	}
	if err := os.MkdirAll(m.baseDir, 0o755); err != nil {
		return "", fmt.Errorf("worktree %q: create managed base dir: %w", name, err)
	}

	path := filepath.Join(m.baseDir, name)
	branch := m.branchPrefix + "/" + name
	if _, err := m.git(ctx, "worktree", "add", path, "-b", branch, "HEAD"); err != nil {
		return "", fmt.Errorf("worktree %q: %w", name, err)
	}

	m.created[name] = &managedWorktree{path: path, branch: branch, baseSHA: baseSHA}
	return path, nil
}

// Cleanup tears down every worktree this manager created, in name order, and
// returns one human-readable note per outcome the user should see. The
// governing rule is that cleanup may remove directories but never work:
//
//   - a worktree git refuses to remove (uncommitted changes) is left in place
//     entirely — forcing the removal would discard the changes;
//   - a branch whose tip moved past its base carries commits: the worktree
//     dir is removed (the commits live in the repository's object store, not
//     the dir) and the branch is retained, named in a note so the user can
//     merge or cherry-pick it;
//   - only a branch still exactly at its base — provably empty — is deleted
//     along with its worktree dir, silently.
//
// Callers run it on a fresh context: the run's own context may already be
// cancelled (halt-on-fail, Ctrl-C), and a halted run is exactly when leftover
// worktrees still need tearing down.
func (m *GitManager) Cleanup(ctx context.Context) []string {
	m.mu.Lock()
	defer m.mu.Unlock()

	names := make([]string, 0, len(m.created))
	for name := range m.created {
		names = append(names, name)
	}
	sort.Strings(names)

	notes := make([]string, 0)
	for _, name := range names {
		if note := m.cleanupOne(ctx, name, m.created[name]); note != "" {
			notes = append(notes, note)
		}
		delete(m.created, name)
	}
	return notes
}

// cleanupOne tears down a single managed worktree per Cleanup's rules and
// returns the note the user should see, or "" for a silent clean removal.
// Emptiness is judged by the worktree's own HEAD, and the branch is addressed
// by whatever it is currently called: a node may rename the branch Acquire
// created (`git branch -m`), and the stored name going stale must not turn a
// provably-empty lane into a noisy retained one.
func (m *GitManager) cleanupOne(ctx context.Context, name string, wt *managedWorktree) string {
	// Ask the worktree itself, before the removal takes it away.
	head, headErr := m.git(ctx, "-C", wt.path, "rev-parse", "HEAD")
	branch, branchErr := m.git(ctx, "-C", wt.path, "rev-parse", "--abbrev-ref", "HEAD")
	if branchErr != nil || branch == "HEAD" {
		// Detached or unreadable — fall back to the name Acquire created.
		branch = wt.branch
	}

	if _, err := m.git(ctx, "worktree", "remove", wt.path); err != nil {
		// git refuses to remove a dirty worktree, which is the behaviour this
		// path relies on: uncommitted work stays on disk, and the user decides.
		return fmt.Sprintf("worktree %q kept at %s (%v); inspect it, then remove it with `git worktree remove --force %s`",
			name, wt.path, err, wt.path)
	}

	if headErr != nil {
		// The worktree's HEAD could not be read, so the branch cannot be
		// proven empty — keep it rather than guess.
		return fmt.Sprintf("worktree %q removed; branch %s retained (could not verify it is empty: %v)", name, branch, headErr)
	}
	if head != wt.baseSHA {
		return fmt.Sprintf("worktree %q removed; branch %s retained — it carries commits (merge or cherry-pick it, or delete it with `git branch -D %s`)",
			name, branch, branch)
	}

	if _, err := m.git(ctx, "branch", "-D", branch); err != nil {
		return fmt.Sprintf("worktree %q removed, but deleting its empty branch %s failed: %v", name, branch, err)
	}
	return ""
}

// git runs one git command against the invocation repository under the
// per-command timeout and returns its trimmed combined output. A failure
// carries that output in the error, because git explains itself on
// stderr and an exit status alone ("exit status 128") diagnoses nothing.
func (m *GitManager) git(ctx context.Context, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, gitTimeout)
	defer cancel()

	out, err := m.gitCmd(cmdCtx, args...).CombinedOutput()
	output := strings.TrimSpace(string(out))
	if err != nil {
		if output != "" {
			return "", fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, output)
		}
		return "", fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
	}
	return output, nil
}

// gitCmd assembles the exact *exec.Cmd for one git invocation: argv, the
// repository to run against, and the scrubbed child environment. It is the
// unit under test — git_test.go calls it directly to assert that
// ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN are absent from cmd.Env. Nothing
// here spawns; git wires it to the OS.
//
// The env scrub is not an optional nicety for git either: a repository's own
// hooks (post-checkout fires on `git worktree add`) are arbitrary user code
// that may legitimately invoke claude, and an unscrubbed child would run it
// on metered API billing.
func (m *GitManager) gitCmd(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.repoDir
	cmd.Env = childenv.Scrub(m.environ())
	return cmd
}
