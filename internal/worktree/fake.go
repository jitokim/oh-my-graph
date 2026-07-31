package worktree

import (
	"context"
	"path/filepath"
	"sync"
)

// FakeManager is the scripted Provider that keeps the scheduler's worktree
// path spawn-free in tests — the mirror of runner.FakeRunner and
// verify.FakeVerifier. It resolves every name to a deterministic path under a
// fake base directory (no git, no filesystem), records each Acquire, and can
// inject an error per name so a test can prove a failed provisioning fails
// the node.
type FakeManager struct {
	// base is the fake directory every resolved path lives under.
	base string
	// errs maps a worktree name to an error Acquire should return instead of
	// a path (injected git failures: not a repo, branch collision).
	errs map[string]error

	mu    sync.Mutex
	calls []string
}

// NewFakeManager builds a FakeManager resolving names under a fixed fake base.
func NewFakeManager() *FakeManager {
	return &FakeManager{
		base: "fake-worktrees",
		errs: make(map[string]error),
	}
}

// InjectError scripts Acquire to return err for the given worktree name.
func (f *FakeManager) InjectError(name string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[name] = err
}

// PathFor is the path Acquire resolves name to, exposed so a test can assert
// an invocation's cwd without hardcoding the fake's layout.
func (f *FakeManager) PathFor(name string) string {
	return filepath.Join(f.base, name)
}

// Acquire returns the deterministic fake path for name (or the scripted
// error), recording the call. Same name, same path — exactly the sharing
// contract GitManager provides — without ever touching git or the disk.
func (f *FakeManager) Acquire(ctx context.Context, name string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	f.mu.Lock()
	f.calls = append(f.calls, name)
	injected := f.errs[name]
	f.mu.Unlock()

	if injected != nil {
		return "", injected
	}
	return f.PathFor(name), nil
}

// Calls returns every worktree name Acquire was asked for, in order — the
// evidence a test asserts against (that a no-worktree graph never asked, that
// a lane asked per node). Returns a copy; never nil.
func (f *FakeManager) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}
