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
// The project invariant this restates (it does not weaken it): exactly four
// objects in oh-my-graph may spawn a process — runner.CLIRunner,
// verify.ShellVerifier, worktree.GitManager and browser.ExecOpener (ADR 0006)
// — each behind its own injected interface, each scrubbing the child
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
