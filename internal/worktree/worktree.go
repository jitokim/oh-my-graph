// Package worktree is the isolation seam: it provisions the managed git
// worktree a node declaring `worktree: <name>` runs in, so parallel edit
// lanes each get their own checkout instead of racing each other — and the
// user's own working tree — in the directory oh-my-graph was invoked from.
//
// It is deliberately NOT part of the NodeRunner seam and not the Verifier's
// job either. Provisioning a worktree is neither a claude invocation nor an
// evidence command; teaching either existing seam to also run git would give
// it — and its fake — two unrelated reasons to change. Instead worktrees get
// their own, narrower interface with their own single exec-owning
// implementation. See docs/adr/0005.
//
// The same seam also reads the invocation repository's remotes and remote
// branch refs for the run's remote snapshot (#410), behind RemoteObserver
// beside Provider. Those are git commands the engine issues against the same
// repository, so they run on GitManager rather than on a new spawner.
//
// The project invariant this restates (it does not weaken it): exactly five
// objects in oh-my-graph may spawn a process — runner.CLIRunner,
// verify.ShellVerifier, worktree.GitManager, browser.ExecOpener (ADR 0006) and
// remote.GHRunner (ADR 0046) — each behind its own injected interface, each scrubbing the child
// environment through internal/childenv. No other package imports os/exec.
package worktree

import (
	"context"
	"errors"
)

// Provider resolves a declared worktree name to the directory the node must
// run in, creating the worktree the first time the name is seen in a run.
// Every later Acquire with the same name returns the same path — that is what
// makes all nodes sharing a `worktree:` name share one checkout, while nodes
// naming different worktrees edit in parallel with no shared tree at all.
//
// Cleanup is deliberately not on this interface: the Scheduler only ever asks
// where a node runs, and tearing worktrees down at run end is the CLI's job
// against the concrete GitManager (see GitManager.Cleanup).
type Provider interface {
	Acquire(ctx context.Context, name string) (string, error)
}

// ErrNoProvider is the sentinel behind RefusingProvider's refusal, so a caller
// can match on it without string matching.
var ErrNoProvider = errors.New(
	"no worktree Provider was injected, so worktree: cannot be provisioned; " +
		"wire worktree.NewGitManager(...) into schedule.Options.Worktrees",
)

// NotConfiguredError names the worktree a run tried to provision with no
// Provider wired in. It unwraps to ErrNoProvider.
type NotConfiguredError struct {
	Name string
}

func (e *NotConfiguredError) Error() string {
	return "cannot provision worktree " + e.Name + ": " + ErrNoProvider.Error()
}

func (e *NotConfiguredError) Unwrap() error { return ErrNoProvider }

// RefusingProvider is the Scheduler's default Provider: it refuses every
// request instead of running anything. It exists so that forgetting to inject
// a real Provider fails loudly and locally rather than silently spawning git
// from a test — which is exactly how a suite that promises "zero real spawns"
// starts quietly breaking that promise.
//
// A graph with no worktree fields never reaches a Provider at all, so this
// default costs existing graphs nothing.
type RefusingProvider struct{}

// NewRefusingProvider returns the refuse-everything default Provider.
func NewRefusingProvider() RefusingProvider { return RefusingProvider{} }

// Acquire always fails, naming the worktree it declined to provision.
func (RefusingProvider) Acquire(_ context.Context, name string) (string, error) {
	return "", &NotConfiguredError{Name: name}
}

// Remote is one configured remote of the invocation repository as
// `git remote -v` prints it. PushURL is the push URL git reports, which is the
// fetch URL when none is set; a remote with several push URLs has them joined
// with ", " in the order git printed them.
type Remote struct {
	Name     string
	FetchURL string
	PushURL  string
}

// RemoteHead is one branch ref a remote advertises: the branch name without
// its refs/heads/ prefix, and the full object name it points at.
type RemoteHead struct {
	Branch string
	SHA    string
}

// RemoteObserver is what the remote snapshot reads through (ADR 0046 §2.3),
// beside the Acquire-only Provider. GitManager is the production
// implementation; FakeRemoteObserver is what tests inject, so code that takes
// snapshots stays spawn-free in CI.
//
// Every method reads; none writes to the repository or to a remote. A failure
// is returned as an error for the snapshot to record as unavailable — no
// caller may treat it as a reason to stop the run.
//
// It reads git only. The snapshot's third part, the open pull requests, is
// read through remote.PRLister (the fifth seam, gh) beside it: a caller
// passes both to TakeRemoteSnapshot, for every before and every after.
type RemoteObserver interface {
	// Remotes lists the invocation repository's configured remotes, sorted
	// by name.
	Remotes(ctx context.Context) ([]Remote, error)
	// LsRemoteHeads lists the branch refs the named remote advertises,
	// sorted by branch.
	LsRemoteHeads(ctx context.Context, remote string) ([]RemoteHead, error)
	// CommitAttributable is ADR 0046 §5.1's local check: whether sha is a
	// commit in the invocation repository reachable from one of this run's
	// own branches and not from baseSHA, the run's base commit.
	CommitAttributable(ctx context.Context, sha, baseSHA string) bool
	// GHDefaultRemotes names, sorted, the remotes `gh repo set-default`
	// marked as gh's default: remote.<name>.gh-resolved set to "base" in
	// the invocation repository's config. None is an empty list, not an
	// error.
	GHDefaultRemotes(ctx context.Context) ([]string, error)
}
