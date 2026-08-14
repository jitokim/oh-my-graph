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
// and one of exactly four in the project (the others: runner.CLIRunner,
// verify.ShellVerifier and browser.ExecOpener) — see docs/adr/0005 and
// docs/adr/0006.
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

// managedWorktree records what Acquire provisioned for one name — everything
// Cleanup needs to tear it down without guessing.
type managedWorktree struct {
	path   string
	branch string
	// baseSHA is the commit the worktree's branch started at. Cleanup
	// compares the worktree's HEAD against it: a worktree still at its base
	// is provably empty and safe to delete; anything else carries commits and
	// its branch is retained. adoptedBaseSHA means the lane was adopted from
	// disk and has no base to prove emptiness against.
	baseSHA string
}

// adoptedBaseSHA marks a managedWorktree Acquire adopted from disk (a reused
// dir, or a retained branch re-attached by a resume leg) rather than created
// fresh. The lane predates this process, so there is no base commit to prove
// emptiness against — Cleanup retains its branch instead of ever deleting it.
const adoptedBaseSHA = ""

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

// Acquire returns the path of the managed worktree for name. Within one
// process the map makes it idempotent, but a resume leg is a FRESH process
// re-provisioning the same run directory, so idempotency must also hold
// against what an earlier leg left on disk (run 20260802-104005: the paused
// leg's cleanup removed the dir but retained the branch, and a blind
// `git worktree add -b` then died on the ref collision). Acquire is therefore
// disk-aware, in precedence order:
//
//   - the managed DIR exists: validate it is a worktree of the invocation
//     repo and reuse it — a foreign directory squatting on the managed path
//     is refused, never adopted;
//   - the run's BRANCH exists with no dir: attach (`git worktree add <path>
//     <branch>`, no -b) so the lane continues its committed state;
//   - neither exists: create fresh with -b, exactly as a first leg does.
//
// The lock covers the whole decision, so two nodes racing on the same name
// cannot double-provision; the loser of the race just gets the same path back.
func (m *GitManager) Acquire(ctx context.Context, name string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if wt, ok := m.created[name]; ok {
		return wt.path, nil
	}

	path := filepath.Join(m.baseDir, name)
	branch := m.branchPrefix + "/" + name

	if _, statErr := os.Stat(path); statErr == nil {
		if err := m.validateOwnWorktree(ctx, path); err != nil {
			return "", fmt.Errorf("worktree %q: %w", name, err)
		}
		m.created[name] = &managedWorktree{path: path, branch: branch, baseSHA: adoptedBaseSHA}
		return path, nil
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("worktree %q: stat managed dir: %w", name, statErr)
	}

	// Owner-only like the rest of the run directory this lives under
	// (<run-dir>/worktrees). What git then checks out inside it is the user's
	// own source at the user's own umask; this is only the container.
	if err := os.MkdirAll(m.baseDir, 0o700); err != nil {
		return "", fmt.Errorf("worktree %q: create managed base dir: %w", name, err)
	}

	if m.branchExists(ctx, branch) {
		if _, err := m.git(ctx, "worktree", "add", path, branch); err != nil {
			return "", fmt.Errorf("worktree %q: attach retained branch %s: %w", name, branch, err)
		}
		m.created[name] = &managedWorktree{path: path, branch: branch, baseSHA: adoptedBaseSHA}
		return path, nil
	}

	baseSHA, err := m.git(ctx, "rev-parse", "HEAD")
	if err != nil {
		return "", fmt.Errorf("worktree %q: resolve invocation repo HEAD: %w", name, err)
	}
	if _, err := m.git(ctx, "worktree", "add", path, "-b", branch, "HEAD"); err != nil {
		return "", fmt.Errorf("worktree %q: %w", name, err)
	}

	m.created[name] = &managedWorktree{path: path, branch: branch, baseSHA: baseSHA}
	return path, nil
}

// validateOwnWorktree confirms path is the root of a linked worktree of the
// invocation repository — the only thing Acquire may adopt. Both sides of the
// comparison come from git itself: the worktree's toplevel must be the path
// (not some repository-controlled ancestor of it), and its shared git
// directory must be the invocation repo's. Symlinks are resolved before
// comparing so a path under a symlinked location (macOS /var → /private/var)
// still matches.
//
// --git-common-dir may come back relative to where the command ran (".git"
// from inside the main repo); gitPathAbs anchors it there. Doing the
// anchoring here instead of passing --path-format=absolute keeps the check
// working on git < 2.31, where rev-parse does not know that option and
// echoes it as an extra output line, corrupting the parsed path.
func (m *GitManager) validateOwnWorktree(ctx context.Context, path string) error {
	top, err := m.git(ctx, "-C", path, "rev-parse", "--show-toplevel")
	if err != nil {
		return fmt.Errorf("existing dir %s is not a git worktree; refusing to adopt it: %w", path, err)
	}
	if resolvePath(top) != resolvePath(path) {
		return fmt.Errorf("existing dir %s is not a worktree root (toplevel %s); refusing to adopt it", path, top)
	}
	repoCommon, err := m.git(ctx, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve invocation repo git dir: %w", err)
	}
	wtCommon, err := m.git(ctx, "-C", path, "rev-parse", "--git-common-dir")
	if err != nil {
		return fmt.Errorf("resolve git dir of existing dir %s: %w", path, err)
	}
	if resolvePath(gitPathAbs(m.repoDir, repoCommon)) != resolvePath(gitPathAbs(path, wtCommon)) {
		return fmt.Errorf("existing dir %s belongs to another repository (%s); refusing to adopt it", path, wtCommon)
	}
	return nil
}

// gitPathAbs anchors a path git printed against dir, the directory the git
// command ran in ("" = the process's working directory, matching gitCmd's
// cmd.Dir), because git may print it relative to there. This is the portable
// stand-in for rev-parse's --path-format=absolute, which git < 2.31 lacks.
func gitPathAbs(dir, p string) string {
	if !filepath.IsAbs(p) {
		p = filepath.Join(dir, p)
	}
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}

// resolvePath normalizes a path for identity comparison, following symlinks
// when possible and falling back to a lexical clean when not.
func resolvePath(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	return filepath.Clean(p)
}

// branchExists reports whether the run's branch is already a ref in the
// invocation repo — the retained-branch signal Acquire attaches on. Any git
// failure reads as "no such branch": the fresh-create path that follows will
// then fail loudly with git's own diagnosis if the repo itself is broken.
func (m *GitManager) branchExists(ctx context.Context, branch string) bool {
	_, err := m.git(ctx, "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
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

	if wt.baseSHA == adoptedBaseSHA {
		// The lane predates this leg (Acquire adopted it from disk), so
		// there is no base to prove it empty against — retain the branch,
		// never guess.
		return fmt.Sprintf("worktree %q removed; branch %s retained — it predates this leg (merge or cherry-pick it, or delete it with `git branch -D %s`)",
			name, branch, branch)
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
// unit under test — git_test.go calls it directly to assert that all four
// scrubbed variables (ANTHROPIC_API_KEY, ANTHROPIC_AUTH_TOKEN, OPENAI_API_KEY,
// CODEX_API_KEY) are absent from cmd.Env. Nothing here spawns; git wires it to
// the OS.
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
