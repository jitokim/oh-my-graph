package remote

import (
	"context"
	"sync"
)

// FakePRLister is the scripted PRLister — GHRunner's counterpart in tests, the
// mirror of worktree.FakeRemoteObserver. It answers from what a test set on
// it (no gh, no network). Setting a different list between two snapshots is
// how a test scripts a PR change.
type FakePRLister struct {
	mu     sync.Mutex
	result PRList
	env    RepoEnv
	calls  int
}

// NewFakePRLister builds a FakePRLister that answers an observed, empty list.
func NewFakePRLister() *FakePRLister {
	return &FakePRLister{result: ObservedPRs(nil)}
}

// Set scripts what ListOpenPRs returns from now on — ObservedPRs or
// UnavailablePRs.
func (f *FakePRLister) Set(l PRList) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.result = l
}

// SetRepoEnv scripts the GH_REPO and GH_HOST RepoEnv reports.
func (f *FakePRLister) SetRepoEnv(env RepoEnv) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.env = env
}

// RepoEnv returns the scripted GH_REPO and GH_HOST; both "" unless set.
func (f *FakePRLister) RepoEnv() RepoEnv {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.env
}

// ListOpenPRs returns a copy of the scripted list and counts the call. A
// context that has already ended answers ReasonCanceled, as GHRunner would.
func (f *FakePRLister) ListOpenPRs(ctx context.Context) PRList {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	if ctx.Err() != nil {
		return UnavailablePRs(ReasonCanceled, "")
	}
	out := f.result
	if out.PRs != nil {
		out.PRs = append([]OpenPR{}, out.PRs...)
	}
	return out
}

// Calls returns how many times ListOpenPRs was asked.
func (f *FakePRLister) Calls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

var _ PRLister = (*FakePRLister)(nil)
