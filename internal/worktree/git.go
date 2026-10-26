package worktree

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
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

// gitWaitDelay is how long a git command's output pipes may stay open after
// the command was killed — see gitCmd.
const gitWaitDelay = time.Second

// GitManager is the production Provider: it materializes each declared
// worktree name as a real `git worktree add` off the invocation repo's HEAD,
// once per unique name, and tears everything down at run end without ever
// losing work. It is the only object in this package that spawns a process,
// and one of exactly five in the project (the others: runner.CLIRunner,
// verify.ShellVerifier, browser.ExecOpener and remote.GHRunner) — see
// docs/adr/0005, docs/adr/0006 and docs/adr/0046.
//
// Its scope is the git commands the engine itself issues against the
// invocation repository: worktrees, and the remote snapshot (#410). The
// snapshot's reads — Remotes, LsRemoteHeads, LsRemoteHeadsURL and
// LsRemoteHeadsURLOutsideRepo (with their `git version` and collision
// checks), RepoConfigFingerprint, ResolveFetchURL, the local
// CommitAttributable check and the GHDefaultRemotes config read — go through
// the same m.gitRun/gitCmd path that m.git uses, so they inherit the one
// exec.Command, the env scrub and the timeout, and add only
// GIT_TERMINAL_PROMPT=0 and an empty GIT_ASKPASS — plus, for Remotes and the
// two ls-remote reads, GIT_SSH_COMMAND
// running ssh in BatchMode unless the user chose an ssh command (see
// snapshotEnv). That widens ADR 0005's "the git worktree commands behind a
// node's worktree:" to cover them; it adds no spawner.
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
	// timeout bounds each git command. A field (defaulting to gitTimeout) so
	// a test can trip the deadline in milliseconds instead of 30 seconds.
	timeout time.Duration

	mu      sync.Mutex
	created map[string]*managedWorktree

	// versionMu guards version, the `git version` LsRemoteHeadsURL asks once
	// per manager. A read that failed to run is not kept, so a cancelled
	// first call does not refuse every later one.
	versionMu sync.Mutex
	version   *gitVersion

	// observe, when set, is handed each command just before it runs: a test
	// hook, nil in production, so a test can assert the Dir and Env a real
	// git child got.
	observe func(*exec.Cmd)
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
		timeout:      gitTimeout,
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

// HeadCommit returns the full object name of HEAD in the repository
// containing dir, or "" — silently — when dir is in no repository, HEAD is
// unborn, or git cannot be run. `auto` and `design` call it once at launch and
// hand the result to coordinator.WithStartCommit, so every cycle of one goal
// measures scope from the same commit (#414).
//
// It lives here rather than in the CLI because reading HEAD means spawning
// git, and the exec-seam invariant allows exactly five spawners
// (docs/adr/0005, 0006 and 0046); running it through m.git keeps the env scrub
// and the per-command timeout. It addresses dir with -C and never touches
// repoDir, baseDir or the branch prefix, so a NewGitManager("", "", "") serves
// it. The output is combined stdout+stderr, so anything that is not a single
// full hex object name — a git warning riding along — is discarded rather than
// returned.
func (m *GitManager) HeadCommit(ctx context.Context, dir string) string {
	out, err := m.git(ctx, "-C", dir, "rev-parse", "--verify", "HEAD")
	if err != nil || !isFullSHA(out) {
		return ""
	}
	return out
}

// isFullSHA accepts a SHA-1 (40) or SHA-256 (64) object name in hex.
func isFullSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
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
	return m.gitEnv(ctx, nil, args...)
}

// gitEnv is git with extra variables appended after the scrub — the remote
// snapshot's noPromptEnv or snapshotEnv.
func (m *GitManager) gitEnv(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	output, err := m.gitRun(ctx, extraEnv, args...)
	if err != nil {
		return "", gitFailure(args, err, output)
	}
	return output, nil
}

// gitFailure is the one error shape of a failed git command: its argv, git's
// error and what it printed. It is the one place that shape lives, so the
// worktree and the remote-snapshot commands cannot drift apart on it.
func gitFailure(args []string, err error, output string) error {
	if output != "" {
		return fmt.Errorf("git %s: %w: %s", strings.Join(args, " "), err, output)
	}
	return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

// gitRun runs one git command under the per-command timeout and returns its
// trimmed combined output and git's bare error — the exit, the kill, the
// timeout — naming neither the argv nor the output. It is the one place the
// timeout lives: gitFailure dresses its error for every command but
// LsRemoteHeadsURL's, which must not echo the URL their env or config carries
// — the URL is never on the argv of the git process the engine starts.
func (m *GitManager) gitRun(ctx context.Context, extraEnv []string, args ...string) (string, error) {
	return m.gitRunIn(ctx, nil, extraEnv, args...)
}

// gitRunIn is gitRun, run outside the repository when out is set (see
// gitCmdIn).
func (m *GitManager) gitRunIn(ctx context.Context, out *outsideRepo, extraEnv []string, args ...string) (string, error) {
	cmdCtx, cancel := context.WithTimeout(ctx, m.timeout)
	defer cancel()

	cmd := m.gitCmdIn(cmdCtx, out, extraEnv, args...)
	if m.observe != nil {
		m.observe(cmd)
	}
	combined, err := cmd.CombinedOutput()
	output := strings.TrimSpace(string(combined))
	if err != nil && errors.Is(cmdCtx.Err(), context.DeadlineExceeded) && ctx.Err() == nil {
		err = fmt.Errorf("timed out after %s: %w", m.timeout, err)
	}
	return output, err
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
//
// extraEnv is appended AFTER the scrub, never instead of it, and nil for the
// worktree commands. An extra variable replaces the parent's own setting of
// it — the parent's entry is dropped, not left beside it — so cmd.Env holds
// each key once: LsRemoteHeadsURL's GIT_CONFIG_COUNT is the only one.
//
// WaitDelay makes the timeout hold: killing git does not kill what git
// spawned (an ext:: transport, a credential helper, a hook), and a
// grandchild still holding the output pipe would otherwise keep
// CombinedOutput waiting for as long as it lives.
func (m *GitManager) gitCmd(ctx context.Context, extraEnv []string, args ...string) *exec.Cmd {
	return m.gitCmdIn(ctx, nil, extraEnv, args...)
}

// gitCmdIn is gitCmd, with the command moved out of the repository when out
// is set: it runs in out.dir, a fresh empty directory, with the variables
// that name a repository (repoLocatingEnv) dropped after the scrub and
// GIT_CEILING_DIRECTORIES set to out.dir's parent, so git finds no
// repository — not the invocation one, and not one the temporary directory
// happens to sit in — and reads no repository config.
func (m *GitManager) gitCmdIn(ctx context.Context, out *outsideRepo, extraEnv []string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = m.repoDir
	cmd.Env = childenv.Scrub(m.environ())
	if out != nil {
		cmd.Dir = out.dir
		cmd.Env = withoutKeys(cmd.Env, repoLocatingEnv)
		extraEnv = append([]string{"GIT_CEILING_DIRECTORIES=" + filepath.Dir(out.dir)}, extraEnv...)
	}
	cmd.Env = append(withoutKeysOf(cmd.Env, extraEnv), extraEnv...)
	cmd.WaitDelay = gitWaitDelay
	return cmd
}

// outsideRepo is where a command runs when it must not read the invocation
// repository: dir, a fresh empty directory its caller made and removes.
type outsideRepo struct{ dir string }

// repoLocatingEnv is every variable that would point a git child run outside
// the repository back at one.
var repoLocatingEnv = []string{
	"GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
	"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
}

// withoutKeysOf is env less every entry whose key some entry of extra sets.
func withoutKeysOf(env, extra []string) []string {
	keys := make([]string, 0, len(extra))
	for _, kv := range extra {
		k, _, _ := strings.Cut(kv, "=")
		keys = append(keys, k)
	}
	return withoutKeys(env, keys)
}

// withoutKeys is env less every entry whose key is one of keys.
func withoutKeys(env, keys []string) []string {
	if len(keys) == 0 {
		return env
	}
	drop := make(map[string]bool, len(keys))
	for _, k := range keys {
		drop[k] = true
	}
	kept := make([]string, 0, len(env))
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); !drop[k] {
			kept = append(kept, kv)
		}
	}
	return kept
}

// noPromptEnv is appended to every remote-snapshot command (ADR 0046 §2.3).
// `git ls-remote` reaches the network, and git asking for a password would
// otherwise hold the snapshot until gitTimeout; with the variable set, git
// fails at once and the part is recorded unavailable. An empty GIT_ASKPASS
// does the same for an askpass helper: git then skips core.askPass and
// SSH_ASKPASS, so no helper — a GUI dialog, a credential popup — can prompt
// either. It stops only git's own prompts: ssh asks on /dev/tty by itself,
// which snapshotEnv covers.
var noPromptEnv = []string{"GIT_TERMINAL_PROMPT=0", "GIT_ASKPASS="}

// batchSSH is the ssh command snapshotEnv adds: BatchMode makes ssh fail at
// once on an unknown host key or a passphrase-protected key with no agent,
// instead of asking on /dev/tty until gitTimeout and reading what is typed.
const batchSSH = "GIT_SSH_COMMAND=ssh -o BatchMode=yes"

// snapshotEnv is the extra env of Remotes and the ls-remote reads: noPromptEnv,
// plus batchSSH unless the user chose an ssh command — GIT_SSH_COMMAND or
// GIT_SSH in the environment the git child receives (the scrubbed environ),
// or a non-empty core.sshCommand in the invocation repository's config. A
// user's choice is left untouched. `git config` exits non-zero for a key that
// is not set, and any failure to read it reads as unset: the safe direction
// is never to prompt.
func (m *GitManager) snapshotEnv(ctx context.Context) []string {
	return m.snapshotEnvIn(ctx, nil)
}

// snapshotEnvIn is snapshotEnv for a command run where out puts it: outside
// the repository, core.sshCommand is read there too, so only the global or
// env config's counts — the repository's is not what the child will read.
func (m *GitManager) snapshotEnvIn(ctx context.Context, out *outsideRepo) []string {
	for _, kv := range childenv.Scrub(m.environ()) {
		if k, _, _ := strings.Cut(kv, "="); k == "GIT_SSH_COMMAND" || k == "GIT_SSH" {
			return noPromptEnv
		}
	}
	if cmd, err := m.gitRunIn(ctx, out, noPromptEnv, "config", "core.sshCommand"); err == nil && cmd != "" {
		return noPromptEnv
	}
	return append(append([]string(nil), noPromptEnv...), batchSSH)
}

var _ RemoteObserver = (*GitManager)(nil)

// Remotes runs `git remote -v` in the invocation repository. A repository
// with no remotes returns an empty list and no error; one that is not a
// repository at all returns git's own diagnosis.
func (m *GitManager) Remotes(ctx context.Context) ([]Remote, error) {
	out, err := m.gitEnv(ctx, m.snapshotEnv(ctx), "remote", "-v")
	if err != nil {
		return nil, err
	}
	return parseRemotes(out)
}

// parseRemotes reads `git remote -v` lines: "<name>\t<url> (fetch)" and
// "<name>\t<url> (push)". The output is combined stdout+stderr, so a line of
// any other shape — a git warning riding along — is skipped; a remote listed
// with no fetch line is an error rather than a remote with an empty URL.
func parseRemotes(out string) ([]Remote, error) {
	byName := map[string]*Remote{}
	var pushes = map[string][]string{}
	for _, line := range strings.Split(out, "\n") {
		name, rest, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok || name == "" {
			continue
		}
		var url, kind string
		switch {
		case strings.HasSuffix(rest, " (fetch)"):
			url, kind = strings.TrimSuffix(rest, " (fetch)"), "fetch"
		case strings.HasSuffix(rest, " (push)"):
			url, kind = strings.TrimSuffix(rest, " (push)"), "push"
		default:
			continue
		}
		r, seen := byName[name]
		if !seen {
			r = &Remote{Name: name}
			byName[name] = r
		}
		if kind == "fetch" {
			r.FetchURL = url
		} else {
			pushes[name] = append(pushes[name], url)
		}
	}
	remotes := make([]Remote, 0, len(byName))
	for name, r := range byName {
		if r.FetchURL == "" {
			return nil, fmt.Errorf("git remote -v: remote %q has no fetch URL", name)
		}
		r.PushURL = strings.Join(pushes[name], ", ")
		if r.PushURL == "" {
			r.PushURL = r.FetchURL
		}
		remotes = append(remotes, *r)
	}
	sort.Slice(remotes, func(i, j int) bool { return remotes[i].Name < remotes[j].Name })
	return remotes, nil
}

// LsRemoteHeads runs `git ls-remote --heads <remote>` in the invocation
// repository. It reaches the network, under gitTimeout and with prompting
// off — git's own and, unless the user chose an ssh command, ssh's. A remote
// name starting with "-" is refused without running anything: the name comes
// from the repository's config, and git would read it as an option
// (--upload-pack= names a program to run).
func (m *GitManager) LsRemoteHeads(ctx context.Context, remote string) ([]RemoteHead, error) {
	if remote == "" || strings.HasPrefix(remote, "-") {
		return nil, fmt.Errorf("refusing to ls-remote remote %q: not a usable remote name", remote)
	}
	args := lsRemoteHeadsArgs(remote)
	out, err := m.lsRemoteHeads(ctx, args)
	if err != nil {
		return nil, gitFailure(args, err, out)
	}
	return parseLsRemoteHeads(out), nil
}

// LsRemoteHeadsURL is LsRemoteHeads for a repository named by its URL rather
// than by a remote's name, on the same env, timeout and WaitDelay. A guarded
// leg reads each remote its before listed this way, through the fetch URL
// that before saw, so a remote removed or re-pointed during the leg cannot
// hide what was pushed to it (ADR 0046 §5.1).
//
// The URL is the raw one — it may carry a credential — so it is kept off the
// argv of the git process the engine starts, where any local user could read
// it: the child's env defines a temporary remote, beforeSnapshotRemote,
// through git's GIT_CONFIG_COUNT/KEY/VALUE (see beforeSnapshotEnv), and the
// argv is only `git ls-remote --heads omg-before-snapshot`. That covers the
// engine's own child only: git itself starts its http(s) transport helper
// with the URL on that helper's argv, which is outside the engine's control.
//
// The read is refused, without running ls-remote, when it cannot be done that
// way: an inherited GIT_CONFIG_COUNT that is not a count, git older than 2.31
// (no GIT_CONFIG_COUNT) or of a version that cannot be determined, and a
// config — the repository's or the inherited env's — that already has keys
// under remote.omg-before-snapshot, whose remote it would read instead. It
// never falls back to the URL on argv. No error ever names the URL: a URL
// starting with "-" is refused, as a name is, without quoting it, and a
// failed read reports git's exit, never its argv or its output, which may
// echo the URL.
func (m *GitManager) LsRemoteHeadsURL(ctx context.Context, url string) ([]RemoteHead, error) {
	return m.lsRemoteHeadsURL(ctx, url, nil)
}

// LsRemoteHeadsURLOutsideRepo is LsRemoteHeadsURL run outside the invocation
// repository: a guarded after's read once the repository's own config can no
// longer be trusted to name the same server — it changed since the before
// (RepoConfigFingerprint), or it, or `git remote -v`, could not be read. Read
// inside, a url.<base>.insteadOf a node added would send the read to another
// server, and a config a node broke would fail it.
//
// Every git it starts — the version ask, the core.sshCommand read, the
// collision check and the ls-remote — runs in a fresh empty temporary
// directory, removed afterwards, with the repository-locating variables
// dropped and GIT_CEILING_DIRECTORIES at the directory's parent (see
// gitCmdIn), so only the global and env config are read: a repository remote
// named omg-before-snapshot no longer collides, and a repository-only
// setting such as a credential helper is not used, so a server that needs it
// fails the read, unavailable. url must already be absolute if it is a local
// path (ResolveFetchURL).
func (m *GitManager) LsRemoteHeadsURLOutsideRepo(ctx context.Context, url string) ([]RemoteHead, error) {
	dir, err := os.MkdirTemp("", "omg-before-snapshot-")
	if err != nil {
		return nil, fmt.Errorf("cannot create an empty directory to read the before fetch URL from outside the repository: %w", err)
	}
	defer os.RemoveAll(dir)
	return m.lsRemoteHeadsURL(ctx, url, &outsideRepo{dir: dir})
}

// lsRemoteHeadsURL is the read of LsRemoteHeadsURL, in the repository when
// out is nil and where out puts it otherwise.
func (m *GitManager) lsRemoteHeadsURL(ctx context.Context, url string, out *outsideRepo) ([]RemoteHead, error) {
	if url == "" || strings.HasPrefix(url, "-") {
		return nil, errors.New("refusing to ls-remote the before fetch URL: not a usable URL")
	}
	n, err := inheritedConfigCount(childenv.Scrub(m.environ()))
	if err != nil {
		return nil, err
	}
	if err := m.requireConfigEnvGit(ctx, out); err != nil {
		return nil, err
	}
	env := m.snapshotEnvIn(ctx, out)
	if err := m.checkBeforeSnapshotRemoteFree(ctx, out, env); err != nil {
		return nil, err
	}
	env = append(env, beforeSnapshotEnv(n, url)...)
	heads, err := m.gitRunIn(ctx, out, env, lsRemoteHeadsArgs(beforeSnapshotRemote)...)
	if err != nil {
		if out != nil {
			return nil, fmt.Errorf("git ls-remote --heads <before fetch URL> (%s): %w", readOutsideRepo, err)
		}
		return nil, fmt.Errorf("git ls-remote --heads <before fetch URL>: %w", err)
	}
	return parseLsRemoteHeads(heads), nil
}

// readOutsideRepo is what a failed read outside the repository says about
// where it ran, so an operator whose server needs a repository-only setting
// can tell why it was not used.
const readOutsideRepo = "read outside the repository: its config changed or could not be read"

// beforeSnapshotRemote is the remote LsRemoteHeadsURL defines in its child's
// env to read a before fetch URL through.
const beforeSnapshotRemote = "omg-before-snapshot"

// noUnexposedRead closes every refusal of LsRemoteHeadsURL that keeps the URL
// off the engine's git argv rather than read it there.
const noUnexposedRead = "cannot read the before fetch URL without exposing it"

// inheritedConfigCount is the GIT_CONFIG_COUNT of env, the environment the git
// child receives — its last value, as os/exec hands it on — and 0 when unset.
// Anything but a non-negative integer is an error: git would reject it, and
// the next free index is unknown.
func inheritedConfigCount(env []string) (int, error) {
	value, set := "", false
	for _, kv := range env {
		if k, v, _ := strings.Cut(kv, "="); k == "GIT_CONFIG_COUNT" {
			value, set = v, true
		}
	}
	if !set {
		return 0, nil
	}
	n, err := strconv.Atoi(value)
	if err != nil || n < 0 || strings.HasPrefix(value, "+") {
		return 0, errors.New("the inherited GIT_CONFIG_COUNT is not a count: " + noUnexposedRead)
	}
	return n, nil
}

// beforeSnapshotEnv defines remote.omg-before-snapshot.url as url at index n,
// the next one after every GIT_CONFIG_KEY_/VALUE_ pair the child inherits,
// which all stay as they are.
func beforeSnapshotEnv(n int, url string) []string {
	return []string{
		fmt.Sprintf("GIT_CONFIG_COUNT=%d", n+1),
		fmt.Sprintf("GIT_CONFIG_KEY_%d=remote.%s.url", n, beforeSnapshotRemote),
		fmt.Sprintf("GIT_CONFIG_VALUE_%d=%s", n, url),
	}
}

// beforeSnapshotRemoteKeys matches every config key of beforeSnapshotRemote.
const beforeSnapshotRemoteKeys = `^remote\.omg-before-snapshot\.`

// checkBeforeSnapshotRemoteFree fails when the config the read would see —
// the repository's, or the env's — already has a key under
// remote.omg-before-snapshot: git would merge it into the remote the read
// defines, and read the user's remote instead. Run where out puts it,
// outside the repository, it sees only the global and env config, the only
// ones the read there sees. `git config --get-regexp` exits 1 when no key
// matches, the clean case; any other failure is an error. Its output, which
// may hold a URL, is never quoted.
func (m *GitManager) checkBeforeSnapshotRemoteFree(ctx context.Context, out *outsideRepo, env []string) error {
	_, err := m.gitRunIn(ctx, out, env, "config", "--get-regexp", beforeSnapshotRemoteKeys)
	var exitErr *exec.ExitError
	switch {
	case errors.As(err, &exitErr) && exitErr.ExitCode() == 1:
		return nil
	case err == nil && out != nil:
		return errors.New("the global or env config already has a remote named " + beforeSnapshotRemote + ": " + noUnexposedRead)
	case err == nil:
		return errors.New("the repository already has a remote named " + beforeSnapshotRemote + ": " + noUnexposedRead)
	default:
		return fmt.Errorf("git config --get-regexp remote.%s: %w", beforeSnapshotRemote, err)
	}
}

// gitVersion is the major.minor of `git version`; ok is false when the
// output did not parse.
type gitVersion struct {
	major, minor int
	ok           bool
}

// configEnvMinor is the git 2.x that first reads GIT_CONFIG_COUNT.
const configEnvMinor = 31

// requireConfigEnvGit fails unless git reads GIT_CONFIG_COUNT (2.31 or
// later). The version is asked once per manager, where out puts it, and
// kept, unless the ask itself failed to run.
func (m *GitManager) requireConfigEnvGit(ctx context.Context, out *outsideRepo) error {
	m.versionMu.Lock()
	defer m.versionMu.Unlock()
	if m.version == nil {
		printed, err := m.gitRunIn(ctx, out, noPromptEnv, "version")
		if err != nil {
			return fmt.Errorf("cannot determine git version: %w: %s", err, noUnexposedRead)
		}
		v := parseGitVersion(printed)
		m.version = &v
	}
	switch v := *m.version; {
	case !v.ok:
		return errors.New("cannot determine git version: unrecognised `git version` output: " + noUnexposedRead)
	case v.major < 2 || v.major == 2 && v.minor < configEnvMinor:
		return fmt.Errorf("git older than 2.%d (found %d.%d): %s", configEnvMinor, v.major, v.minor, noUnexposedRead)
	}
	return nil
}

// parseGitVersion reads "git version 2.43.0", "git version 2.39.3 (Apple
// Git-145)" or "git version 2.45.0.windows.1": the major and minor of the
// first word after "git version".
func parseGitVersion(out string) gitVersion {
	rest, ok := strings.CutPrefix(strings.TrimSpace(out), "git version ")
	if !ok {
		return gitVersion{}
	}
	word, _, _ := strings.Cut(strings.TrimSpace(rest), " ")
	parts := strings.Split(word, ".")
	if len(parts) < 2 {
		return gitVersion{}
	}
	major, err := strconv.Atoi(parts[0])
	if err != nil || major < 0 {
		return gitVersion{}
	}
	minor, err := strconv.Atoi(parts[1])
	if err != nil || minor < 0 {
		return gitVersion{}
	}
	return gitVersion{major: major, minor: minor, ok: true}
}

// RepoConfigFingerprint is the SHA-256, in hex, of the invocation
// repository's own config as `git config --list --show-origin --show-scope
// --includes` prints it there, read with prompting off: every line but those
// git scopes system, global or command — so the local and worktree entries,
// those of a file an include.path or includeIf reaches from them included,
// and any line git does not scope at all (a continuation of a value holding a
// newline), which keeps every change to them a change to the digest. A
// guarded leg pins it at its before and compares it at its after
// (TakeGuardedRemoteSnapshot). Only the digest leaves here: the values may
// hold URLs or credentials, so a failure reports git's exit, never its
// output.
func (m *GitManager) RepoConfigFingerprint(ctx context.Context) (string, error) {
	out, err := m.gitRun(ctx, noPromptEnv, "config", "--list", "--show-origin", "--show-scope", "--includes")
	if err != nil {
		return "", fmt.Errorf("git config --list --show-origin --show-scope --includes: %w", err)
	}
	return repoConfigFingerprint(out), nil
}

// repoConfigFingerprint digests the lines of a scoped `git config --list`
// that RepoConfigFingerprint keeps.
func repoConfigFingerprint(list string) string {
	h := sha256.New()
	for _, line := range strings.Split(list, "\n") {
		switch scope, _, _ := strings.Cut(line, "\t"); scope {
		case "system", "global", "command":
			continue
		}
		h.Write([]byte(line + "\n"))
	}
	return hex.EncodeToString(h.Sum(nil))
}

// unresolvedLocalURL opens the error of a relative local-path fetch URL
// ResolveFetchURL could not make absolute.
const unresolvedLocalURL = "the before fetch URL is a relative local path, and the repository root it is relative to could not be read"

// ResolveFetchURL returns url as a guarded leg's before keeps it to read its
// after through: unchanged, unless it is a relative local path (../origin.git),
// which git reads relative to the repository root and a read outside the
// repository would read relative to somewhere else — that is made absolute
// against `git rev-parse --show-toplevel`. A URL with a scheme, an scp-like
// host:path, an absolute path and a <transport>::<address> are left as they
// are, as is everything `git remote -v` already rewrote through insteadOf.
// The error never names the URL.
func (m *GitManager) ResolveFetchURL(ctx context.Context, url string) (string, error) {
	if !isRelativeLocalPath(url) {
		return url, nil
	}
	top, err := m.gitRun(ctx, noPromptEnv, "rev-parse", "--show-toplevel")
	if err != nil {
		return "", fmt.Errorf("%s: git rev-parse --show-toplevel: %w", unresolvedLocalURL, err)
	}
	if strings.Contains(top, "\n") || !filepath.IsAbs(top) {
		return "", errors.New(unresolvedLocalURL + ": git rev-parse --show-toplevel printed no absolute path")
	}
	return filepath.Join(top, url), nil
}

// isRelativeLocalPath is git's own test for a local path (connect.c's
// url_is_local_not_ssh: no scheme, and no colon or a slash before the first
// one), less the absolute paths.
func isRelativeLocalPath(url string) bool {
	if url == "" || strings.Contains(url, "://") || filepath.IsAbs(url) {
		return false
	}
	colon, slash := strings.IndexByte(url, ':'), strings.IndexByte(url, '/')
	return colon < 0 || slash >= 0 && slash < colon
}

func lsRemoteHeadsArgs(repository string) []string {
	return []string{"ls-remote", "--heads", repository}
}

// lsRemoteHeads is LsRemoteHeads's run: snapshotEnv, under gitRun's timeout.
// It returns git's output beside its bare error, for the caller to word.
func (m *GitManager) lsRemoteHeads(ctx context.Context, args []string) (string, error) {
	return m.gitRun(ctx, m.snapshotEnv(ctx), args...)
}

// parseLsRemoteHeads reads "<sha>\trefs/heads/<branch>" lines. A line that is
// not a full object name followed by a refs/heads/ ref is not a head — the
// output is combined, so it may be a warning — and is skipped. The sha comes
// from the remote, so isFullSHA is also what keeps it safe to hand back to
// git as an argument later.
func parseLsRemoteHeads(out string) []RemoteHead {
	heads := make([]RemoteHead, 0)
	for _, line := range strings.Split(out, "\n") {
		sha, ref, ok := strings.Cut(strings.TrimRight(line, "\r"), "\t")
		if !ok || !isFullSHA(sha) {
			continue
		}
		branch, ok := strings.CutPrefix(ref, "refs/heads/")
		if !ok || branch == "" {
			continue
		}
		heads = append(heads, RemoteHead{Branch: branch, SHA: strings.ToLower(sha)})
	}
	sort.Slice(heads, func(i, j int) bool { return heads[i].Branch < heads[j].Branch })
	return heads
}

// CommitAttributable runs ADR 0046 §5.1's check for sha against baseSHA, the
// run's base commit (run_base_sha), in this order:
//
//  1. `git cat-file -e <sha>^{commit}`: sha must be a commit in the invocation
//     repository's object store, which the run's worktrees share;
//  2. `git merge-base --is-ancestor <sha> <base>` must fail with exit 1, so a
//     commit already in the history the run started from (base itself
//     included) never counts;
//  3. `git merge-base --is-ancestor <sha> <ref>` must succeed for some ref
//     `git for-each-ref refs/heads/omg/<run-id>/` lists — the run's own
//     branches.
//
// Every command is local; none reaches the network. Anything that cannot be
// proven reads as false: a sha or base that is not a full object name (a run
// with no recorded run_base_sha cannot pass), a manager built without a run
// id, and any git failure other than merge-base's "not an ancestor". Passing
// means only that the commit is reachable from the run's branches and not
// from the base, never that the run moved a ref.
func (m *GitManager) CommitAttributable(ctx context.Context, sha, baseSHA string) bool {
	if !isFullSHA(sha) || !isFullSHA(baseSHA) || m.branchPrefix == "omg/" {
		return false
	}
	if _, err := m.gitEnv(ctx, noPromptEnv, "cat-file", "-e", sha+"^{commit}"); err != nil {
		return false
	}
	if ancestor, err := m.isAncestor(ctx, sha, baseSHA); err != nil || ancestor {
		return false
	}
	out, err := m.gitEnv(ctx, noPromptEnv, "for-each-ref", "--format=%(refname)", "refs/heads/"+m.branchPrefix+"/")
	if err != nil {
		return false
	}
	for _, ref := range strings.Split(out, "\n") {
		if !strings.HasPrefix(ref, "refs/heads/"+m.branchPrefix+"/") {
			continue
		}
		if ancestor, err := m.isAncestor(ctx, sha, ref); err == nil && ancestor {
			return true
		}
	}
	return false
}

// isAncestor runs `git merge-base --is-ancestor a b`. Exit 0 is true and
// exit 1 is false; any other outcome (a bad object, a timeout) is an error,
// so a broken check is never read as "not an ancestor".
func (m *GitManager) isAncestor(ctx context.Context, a, b string) (bool, error) {
	_, err := m.gitEnv(ctx, noPromptEnv, "merge-base", "--is-ancestor", a, b)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, err
}

// ghResolvedKey is the config variable `gh repo set-default` writes on the
// remote it chose, and ghResolvedBase the value that names that remote's own
// repository; any other value is the OWNER/REPO gh chose instead.
const (
	ghResolvedKey  = "gh-resolved"
	ghResolvedBase = "base"
)

// GHDefaultRemotes reads remote.<name>.gh-resolved from the invocation
// repository's config, a local read with prompting off like
// CommitAttributable's, and maps each remote carrying it to its value. `git
// config --get-regexp` exits 1 when no key matches, which is no default, not a
// failure; any other failure is returned.
func (m *GitManager) GHDefaultRemotes(ctx context.Context) (map[string]string, error) {
	out, err := m.gitEnv(ctx, noPromptEnv, "config", "--get-regexp", `^remote\..*\.`+ghResolvedKey+`$`)
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return map[string]string{}, nil
		}
		return nil, err
	}
	return parseGHResolved(out), nil
}

// parseGHResolved reads `git config --get-regexp` lines, "<key> <value>".
// git prints the section and variable lowercased and the remote name as
// written, which may itself hold dots. A line of any other shape — combined
// output may carry a warning — is skipped, as is an empty value.
func parseGHResolved(out string) map[string]string {
	values := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		key, value, ok := strings.Cut(strings.TrimRight(line, "\r"), " ")
		if !ok || value == "" {
			continue
		}
		name, ok := strings.CutPrefix(key, "remote.")
		if !ok {
			continue
		}
		name, ok = strings.CutSuffix(name, "."+ghResolvedKey)
		if !ok || name == "" {
			continue
		}
		values[name] = value
	}
	return values
}
