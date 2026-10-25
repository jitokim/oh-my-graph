package worktree

import (
	"context"
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
// told — the remotes could not be read, or two GitHub repositories with no
// gh default — the pull-request part is unavailable and gh is not asked. When
// gh does answer, the repository its PRs' URLs name must be the expected one,
// or the part is unavailable too.
//
// URLs, and the reasons that may quote them, have any credential in them
// replaced before they are recorded.
func TakeRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister) runstate.RemoteSnapshot {
	// The expected repository is recorded on the snapshot by a later #410
	// change; for now it only decides whether gh is asked.
	snap, _ := takeRemoteSnapshot(ctx, obs, prs)
	return snap
}

// takeRemoteSnapshot is TakeRemoteSnapshot, also returning the repository gh
// was expected to read (zero when gh was not asked).
func takeRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister) (runstate.RemoteSnapshot, ExpectedGHRepo) {
	snap, remotes, remotesErr := takeGitParts(ctx, obs)
	expected, unknown := expectGHRepo(ctx, obs, prs.RepoEnv(), remotes, remotesErr)
	if unknown != nil {
		snap.PullRequests = runstate.PullRequestsSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: unknown.Reason, Detail: remote.SummarizeReason(unknown.Detail)}
		return snap, ExpectedGHRepo{}
	}
	snap.PullRequests = takePullRequests(ctx, prs, expected)
	return snap, expected
}

// takeGitParts reads the remotes and branches parts, and hands back the
// remotes, or the error that made that part unavailable, for expectGHRepo.
func takeGitParts(ctx context.Context, obs RemoteObserver) (runstate.RemoteSnapshot, []Remote, error) {
	var snap runstate.RemoteSnapshot

	remotes, err := obs.Remotes(ctx)
	if err != nil {
		reason := remote.SummarizeReason(err.Error())
		snap.Remotes = runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: reason}
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

	var failures []string
	var refs []runstate.RemoteBranchRef
	for _, r := range remotes {
		heads, err := obs.LsRemoteHeads(ctx, r.Name)
		if err != nil {
			failures = append(failures, "ls-remote "+r.Name+": "+err.Error())
			continue
		}
		for _, h := range heads {
			refs = append(refs, runstate.RemoteBranchRef{Remote: r.Name, Branch: h.Branch, SHA: h.SHA})
		}
	}
	if len(failures) > 0 {
		snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
			Reason: remote.SummarizeReason(strings.Join(failures, "; "))}
		return snap, remotes, nil
	}
	snap.Branches = runstate.BranchesSnapshot{Status: runstate.RemotePartObserved, Refs: refs}
	return snap, remotes, nil
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
