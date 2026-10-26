package worktree

import (
	"context"
	"maps"
	"slices"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// TakeRemoteSnapshot reads the invocation repository's remote state (ADR
// 0046 §2.1): through obs the configured remotes, then `ls-remote --heads`
// for each, and through prs the open pull requests. It never fails: a read
// that errors — not a repository, a remote that cannot be reached, gh not
// on PATH or not logged in, a timeout — makes its part unavailable with the
// reason, and a run carries on exactly as it would have. The branches part
// is unavailable when the remotes part is, and when any one remote's refs
// could not be read, since a list missing one remote would compare as that
// remote's branches all gone.
//
// gh reads a GitHub repository, not a remote, and with more than one GitHub
// remote it may read the wrong one without failing. So before gh is asked,
// expectGHRepo works out which repository it will read; when that cannot be
// told — the remotes could not be read, no GitHub remote, or two GitHub
// repositories with no gh default — the pull-request part is unavailable and
// gh is not asked. When
// gh does answer, the repository its PRs' URLs name must be the expected one,
// or the part is unavailable too.
//
// The expected repository, when one was worked out, is recorded on the
// pull-request part as Repo and RepoSource, whatever gh then answered.
//
// URLs, and the reasons that may quote them, have any credential in them
// replaced before they are recorded.
func TakeRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister) runstate.RemoteSnapshot {
	snap, _ := takeRemoteSnapshot(ctx, obs, prs)
	return snap
}

// FetchURLs maps each remote a snapshot listed to its raw fetch URL, as `git
// remote -v` printed it — any credential in it included. A guarded leg keeps
// its before's in memory only, to read its after through (ADR 0046 §5.1): it
// is never recorded, printed or put in an error; the snapshot records the
// redacted URL as always.
type FetchURLs map[string]string

// TakeGuardedRemoteSnapshot is TakeRemoteSnapshot for a guarded leg, which
// also returns the raw fetch URL of every remote it listed. through is the
// leg's before's FetchURLs when taking its after (nil for the before): each
// remote in it is read with `ls-remote --heads <that URL>` rather than by
// name, whether it is still configured, removed or re-pointed, its refs
// recorded under its name and the name in Branches.ReadThroughBeforeURL, so
// removing or re-pointing a remote cannot hide a push to it. A remote not in
// through — one added during the leg — is read by name. A read through a URL
// that fails makes the branches part unavailable, as any remote's failed read
// does, its reason naming the remote and never the URL. When the after cannot
// list the remotes, the remotes part is unavailable as always, but every
// remote in through is still read through its URL, so the branches part is
// still observed for them.
func TakeGuardedRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister, through FetchURLs) (runstate.RemoteSnapshot, FetchURLs) {
	snap, remotes, _ := takeRemoteSnapshotThrough(ctx, obs, prs, through)
	urls := FetchURLs{}
	for _, r := range remotes {
		urls[r.Name] = r.FetchURL
	}
	return snap, urls
}

// takeRemoteSnapshot is TakeRemoteSnapshot, also returning the repository gh
// was expected to read (zero when gh was not asked).
func takeRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister) (runstate.RemoteSnapshot, ExpectedGHRepo) {
	snap, _, expected := takeRemoteSnapshotThrough(ctx, obs, prs, nil)
	return snap, expected
}

// takeRemoteSnapshotThrough is the snapshot, its git parts read through
// through's URLs (none: every remote by name), also returning the remotes
// listed and the repository gh was expected to read.
func takeRemoteSnapshotThrough(ctx context.Context, obs RemoteObserver, prs remote.PRLister, through FetchURLs) (runstate.RemoteSnapshot, []Remote, ExpectedGHRepo) {
	snap, remotes, remotesErr := takeGitParts(ctx, obs, through)
	expected, unknown := expectGHRepo(ctx, obs, prs.RepoEnv(), remotes, remotesErr)
	if unknown != nil {
		snap.PullRequests = runstate.PullRequestsSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: unknown.Reason, Detail: remote.SummarizeReason(unknown.Detail)}
		return snap, remotes, ExpectedGHRepo{}
	}
	snap.PullRequests = takePullRequests(ctx, prs, expected)
	if expected.Repo != "" {
		snap.PullRequests.Repo = expected.Host + "/" + expected.Repo
		snap.PullRequests.RepoSource = expected.Source
	}
	return snap, remotes, expected
}

// takeGitParts reads the remotes and branches parts, and hands back the
// remotes, or the error that made that part unavailable, for expectGHRepo.
// Each remote in through is read through its URL there, the rest by name.
//
// When the remotes cannot be listed, the branches part is unavailable too —
// unless through names remotes, as a guarded after's does: their URLs are
// already in hand, so each is still read through its URL and the branches
// part records those remotes' refs, so a node that breaks `git remote -v`
// (a corrupted .git/config) cannot hide a push behind it.
func takeGitParts(ctx context.Context, obs RemoteObserver, through FetchURLs) (runstate.RemoteSnapshot, []Remote, error) {
	var snap runstate.RemoteSnapshot

	remotes, err := obs.Remotes(ctx)
	if err != nil {
		reason := remote.SummarizeReason(err.Error())
		snap.Remotes = runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: reason}
		if len(through) > 0 {
			snap.Branches = takeBranches(ctx, obs, nil, through)
			return snap, nil, err
		}
		snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: "the remotes part is unavailable: " + reason}
		return snap, nil, err
	}
	snap.Remotes.Status = runstate.RemotePartObserved
	for _, r := range remotes {
		snap.Remotes.Configured = append(snap.Remotes.Configured, runstate.RemoteConfig{
			Name: r.Name, FetchURL: remote.RedactURLCredentials(r.FetchURL), PushURL: remote.RedactURLCredentials(r.PushURL),
		})
	}
	snap.Branches = takeBranches(ctx, obs, remotes, through)
	return snap, remotes, nil
}

// takeBranches reads the branches part: each remote in through through its
// URL there, then each of remotes not in through by name.
func takeBranches(ctx context.Context, obs RemoteObserver, remotes []Remote, through FetchURLs) runstate.BranchesSnapshot {
	var failures []string
	var refs []runstate.RemoteBranchRef
	read := func(name string, heads []RemoteHead, err error) {
		if err != nil {
			failures = append(failures, "ls-remote "+name+": "+err.Error())
			return
		}
		for _, h := range heads {
			refs = append(refs, runstate.RemoteBranchRef{Remote: name, Branch: h.Branch, SHA: h.SHA})
		}
	}
	readThrough := slices.Sorted(maps.Keys(through))
	for _, name := range readThrough {
		heads, err := obs.LsRemoteHeadsURL(ctx, through[name])
		read(name, heads, err)
	}
	for _, r := range remotes {
		if _, ok := through[r.Name]; ok {
			continue
		}
		heads, err := obs.LsRemoteHeads(ctx, r.Name)
		read(r.Name, heads, err)
	}
	if len(failures) > 0 {
		return runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: remote.SummarizeReason(strings.Join(failures, "; "))}
	}
	branches := runstate.BranchesSnapshot{Status: runstate.RemotePartObserved, Refs: refs}
	if len(readThrough) > 0 {
		// Read in two passes, so back into remote order.
		sort.SliceStable(refs, func(i, j int) bool { return refs[i].Remote < refs[j].Remote })
		branches.ReadThroughBeforeURL = readThrough
	}
	return branches
}

// takePullRequests records what prs answered. The limit rule is applied
// here, whatever the lister: a list holding the whole --limit is truncated,
// never compared. Anything that is not an observed list is unavailable —
// never empty — with its reason, and its detail redacted once more, so a
// lister that skipped the hygiene still records nothing raw.
//
// An observed list is then checked against expected: PRs whose URLs name
// another repository — or more than one — were read from the wrong place,
// so the part is unavailable rather than recorded as this repository's.
func takePullRequests(ctx context.Context, prs remote.PRLister, expected ExpectedGHRepo) runstate.PullRequestsSnapshot {
	l := remote.CheckTruncation(prs.ListOpenPRs(ctx))
	if l.Observed() {
		if reason, ok := checkGHRepoRead(expected, l.PRs); !ok {
			l = remote.UnavailablePRs(reason, "")
		}
	}
	if !l.Observed() {
		reason := l.Reason
		if reason == "" {
			reason = "no reason given"
		}
		return runstate.PullRequestsSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: remote.SummarizeReason(reason), Detail: remote.SummarizeReason(l.Detail)}
	}
	var open []runstate.OpenPullRequest
	for _, pr := range l.PRs {
		open = append(open, runstate.OpenPullRequest{Number: pr.Number, HeadRefName: pr.HeadRefName, HeadRefOid: pr.HeadRefOid})
	}
	sort.Slice(open, func(i, j int) bool { return open[i].Number < open[j].Number })
	return runstate.PullRequestsSnapshot{Status: runstate.RemotePartObserved, Open: open}
}

// RunCommitCheck adapts obs's §5.1 check to the shape
// runstate.RemoteChanges.EndLeg takes, against the run's recorded base.
func RunCommitCheck(ctx context.Context, obs RemoteObserver, baseSHA string) func(sha string) bool {
	return func(sha string) bool { return obs.CommitAttributable(ctx, sha, baseSHA) }
}
