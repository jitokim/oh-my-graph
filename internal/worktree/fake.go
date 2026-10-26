package worktree

import (
	"context"
	"errors"
	"maps"
	"path/filepath"
	"sort"
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

// FakeRemoteObserver is the scripted RemoteObserver — FakeManager's
// counterpart for the remote snapshot. It answers from what a test set on it
// (no git, no network): the remotes list or an error for it, each remote's
// heads or an error for them, the set of shas CommitAttributable passes, and
// the remotes marked gh's default.
// Mutating it between two snapshots is how a test scripts a change.
//
// A read by URL answers what SetURLHeads scripted for that URL, else what
// the remote that URL was ever set on (SetRemotes, as a fetch URL) answers by
// name: the repository behind a URL outlives a remote removed or re-pointed
// away from it.
type FakeRemoteObserver struct {
	mu          sync.Mutex
	remotes     []Remote
	remotesErr  error
	heads       map[string][]RemoteHead
	headsErr    map[string]error
	urlRemote   map[string]string
	urlHeads    map[string][]RemoteHead
	urlErr      map[string]error
	urlCalls    []string
	runCommits  map[string]bool
	attribCalls []string
	ghDefaults  map[string]string
	ghDefErr    error
	ghDefCalls  int
}

// NewFakeRemoteObserver builds a FakeRemoteObserver with no remotes.
func NewFakeRemoteObserver() *FakeRemoteObserver {
	return &FakeRemoteObserver{
		heads:      make(map[string][]RemoteHead),
		headsErr:   make(map[string]error),
		urlRemote:  make(map[string]string),
		urlHeads:   make(map[string][]RemoteHead),
		urlErr:     make(map[string]error),
		runCommits: make(map[string]bool),
	}
}

// SetRemotes scripts what Remotes returns; a non-nil err replaces the list.
func (f *FakeRemoteObserver) SetRemotes(remotes []Remote, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.remotes = append([]Remote(nil), remotes...)
	f.remotesErr = err
	for _, r := range remotes {
		f.urlRemote[r.FetchURL] = r.Name
	}
}

// SetURLHeads scripts what LsRemoteHeadsURL returns for url; a non-nil err
// replaces the list. It wins over the remote url was set on.
func (f *FakeRemoteObserver) SetURLHeads(url string, heads []RemoteHead, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlHeads[url] = append([]RemoteHead(nil), heads...)
	f.urlErr[url] = err
}

// SetHeads scripts what LsRemoteHeads returns for remote; a non-nil err
// replaces the list.
func (f *FakeRemoteObserver) SetHeads(remote string, heads []RemoteHead, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.heads[remote] = append([]RemoteHead(nil), heads...)
	f.headsErr[remote] = err
}

// SetRunCommit scripts CommitAttributable to pass for sha, with any base.
func (f *FakeRemoteObserver) SetRunCommit(sha string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.runCommits[sha] = true
}

// Remotes returns the scripted remotes, sorted by name, or the scripted error.
func (f *FakeRemoteObserver) Remotes(ctx context.Context) ([]Remote, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.remotesErr != nil {
		return nil, f.remotesErr
	}
	out := append([]Remote{}, f.remotes...)
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// LsRemoteHeads returns the scripted heads for remote, sorted by branch, or
// the scripted error. A remote nothing was scripted for advertises no heads.
func (f *FakeRemoteObserver) LsRemoteHeads(ctx context.Context, remote string) ([]RemoteHead, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.headsOf(remote)
}

// headsOf is remote's scripted heads, sorted, or its scripted error. f.mu is
// held.
func (f *FakeRemoteObserver) headsOf(remote string) ([]RemoteHead, error) {
	if err := f.headsErr[remote]; err != nil {
		return nil, err
	}
	out := append([]RemoteHead{}, f.heads[remote]...)
	sort.Slice(out, func(i, j int) bool { return out[i].Branch < out[j].Branch })
	return out, nil
}

// LsRemoteHeadsURL returns the heads SetURLHeads scripted for url, else those
// of the remote url was set on as a fetch URL, sorted by branch, or the
// scripted error. A URL neither names fails, as a repository nobody set up
// would; the error does not contain it. It records each URL it was asked.
func (f *FakeRemoteObserver) LsRemoteHeadsURL(ctx context.Context, url string) ([]RemoteHead, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.urlCalls = append(f.urlCalls, url)
	if heads, ok := f.urlHeads[url]; ok {
		if err := f.urlErr[url]; err != nil {
			return nil, err
		}
		out := append([]RemoteHead{}, heads...)
		sort.Slice(out, func(i, j int) bool { return out[i].Branch < out[j].Branch })
		return out, nil
	}
	if name, ok := f.urlRemote[url]; ok {
		return f.headsOf(name)
	}
	return nil, errors.New("fake: no repository at the URL")
}

// URLCalls returns every URL LsRemoteHeadsURL was asked, in order. Returns a
// copy; never nil.
func (f *FakeRemoteObserver) URLCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.urlCalls...)
}

// CommitAttributable passes exactly the shas SetRunCommit named, except the
// base itself, which §5.1 never counts. It records each sha it was asked
// about.
func (f *FakeRemoteObserver) CommitAttributable(_ context.Context, sha, baseSHA string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attribCalls = append(f.attribCalls, sha)
	return baseSHA != "" && sha != baseSHA && f.runCommits[sha]
}

// AttributionCalls returns every sha CommitAttributable was asked about, in
// order. Returns a copy; never nil.
func (f *FakeRemoteObserver) AttributionCalls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.attribCalls...)
}

// SetGHDefaultRemotes scripts what GHDefaultRemotes returns: each remote
// carrying gh-resolved mapped to its value, or a non-nil err in their place.
func (f *FakeRemoteObserver) SetGHDefaultRemotes(values map[string]string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ghDefaults = maps.Clone(values)
	f.ghDefErr = err
}

// GHDefaultRemotes returns a copy of the scripted values, or the scripted
// error. Nothing scripted is no default. It counts each call.
func (f *FakeRemoteObserver) GHDefaultRemotes(ctx context.Context) (map[string]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ghDefCalls++
	if f.ghDefErr != nil {
		return nil, f.ghDefErr
	}
	out := map[string]string{}
	maps.Copy(out, f.ghDefaults)
	return out, nil
}

// GHDefaultCalls returns how many times GHDefaultRemotes was asked.
func (f *FakeRemoteObserver) GHDefaultCalls() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.ghDefCalls
}

var _ RemoteObserver = (*FakeRemoteObserver)(nil)
