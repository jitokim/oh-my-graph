package worktree

import (
	"context"
	"errors"
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
// remote -v` printed it — any credential in it included — with a relative
// local path made absolute (ResolveFetchURL). A guarded leg keeps its
// before's in memory only, to read its after through (ADR 0046 §5.1): it is
// never recorded, printed or put in an error; the snapshot records the
// redacted URL as always.
type FetchURLs map[string]string

// GuardedBefore is what a guarded leg's before keeps in memory to read its
// after through: the FetchURLs, the remotes whose relative local-path URL
// could not be resolved (with why), the digest of the repository's own
// config (RepoConfigFingerprint) and that of its shallow file
// (ShallowFingerprint) — digests only, never a value.
type GuardedBefore struct {
	urls        FetchURLs
	unresolved  map[string]string
	config      string
	configRead  bool
	shallowPath string
	shallow     string
	shallowRead bool
	// configUnreadable is set by the after: its RepoConfigFingerprint failed.
	configUnreadable bool
}

// The reasons a guarded leg's branches and pull-request parts are not
// guarded when its §5.1 check could not run (AttributionUnavailable).
const (
	AttributionConfigUnreadable = "attribution unavailable: repository config unreadable"
	AttributionShallowChanged   = "attribution unavailable: shallow boundary changed"
)

// AttributionUnavailable is why the leg's §5.1 check, run after the after
// taken against b, could not attribute a commit, or "" when it could. The
// check reads the repository and its config: when that config could not be
// read at the after, or checkFailed — a check's own git read failed, not a
// proven false — it is AttributionConfigUnreadable. When the repository's
// shallow file is not what it was at the before — its digest differs, a
// file absent then exists now, or either read failed — it is
// AttributionShallowChanged: a shallow boundary a node wrote hides its
// commit's parents from the check. A repository shallow at the before, and
// unchanged, is attributed as any other.
//
// It changes no attribution and fails nothing: a guarded leg reports its
// branches and pull requests not guarded with the reason
// (NoteAttributionUnavailable), and only its attributable entries fail it.
// Nil-safe: "" for no before.
func (b *GuardedBefore) AttributionUnavailable(obs RemoteObserver, checkFailed bool) string {
	if b == nil {
		return ""
	}
	if checkFailed || b.configUnreadable {
		return AttributionConfigUnreadable
	}
	if !b.shallowRead {
		return AttributionShallowChanged
	}
	if shallow, err := obs.ShallowFingerprint(b.shallowPath); err != nil || shallow != b.shallow {
		return AttributionShallowChanged
	}
	return ""
}

// NoteAttributionUnavailable records reason, an AttributionUnavailable
// reason, on the last leg's diff: a not-compared item for each of the
// branches and pull-request parts that diff observed, since a new branch or
// pull request in them could not be attributed. The entries stay as they
// are, each with the attribution the check answered. Nothing for an empty
// reason or a leg with no diff.
func NoteAttributionUnavailable(c *runstate.RemoteChanges, reason string) {
	n := len(c.Legs)
	if reason == "" || n == 0 || c.Legs[n-1].Changes == nil {
		return
	}
	d := c.Legs[n-1].Changes
	for _, p := range d.Observed {
		if p == runstate.RemotePartBranches || p == runstate.RemotePartPullRequests {
			d.NotCompared = append(d.NotCompared, runstate.RemoteNotCompared{Part: p, Reason: reason})
		}
	}
}

// readThrough is every remote the before listed, sorted: each is read
// through its before URL at the after, never by name. Nil-safe.
func (b *GuardedBefore) readThrough() []string {
	if b == nil {
		return nil
	}
	names := slices.Collect(maps.Keys(b.urls))
	for name := range b.unresolved {
		names = append(names, name)
	}
	slices.Sort(names)
	return names
}

// readsThrough reports whether the after reads name through its before URL.
// Nil-safe.
func (b *GuardedBefore) readsThrough(name string) bool {
	if b == nil {
		return false
	}
	_, url := b.urls[name]
	_, unresolved := b.unresolved[name]
	return url || unresolved
}

// readOutsideRepo reports whether the after must read the before URLs from
// outside the repository (LsRemoteHeadsURLOutsideRepo): the repository's
// config may no longer name the servers the before read — its digest
// differs from the before's, or it could not be read at either, or `git
// remote -v` failed (remotesErr). Otherwise the read stays inside, where a
// repository credential helper still works. It also records whether the
// config could be read, for AttributionUnavailable. Nil-safe; false when
// there is no URL to read.
func (b *GuardedBefore) readOutsideRepo(ctx context.Context, obs RemoteObserver, remotesErr error) bool {
	if b == nil {
		return false
	}
	config, err := obs.RepoConfigFingerprint(ctx)
	b.configUnreadable = err != nil
	if len(b.urls) == 0 {
		return false
	}
	return remotesErr != nil || !b.configRead || err != nil || config != b.config
}

// TakeGuardedRemoteSnapshot is TakeRemoteSnapshot for a guarded leg. Taking
// the before (before nil), it also returns what the after reads through:
// every listed remote's raw fetch URL, a relative local path made absolute
// against the repository root — one that cannot be is kept as unresolved,
// and its read at the after fails with why — the digest of the
// repository's own config, and that of its shallow file. Taking the after, before is the leg's before's,
// and it returns nil: each remote in before is read with `ls-remote --heads
// <that URL>` rather than by name, whether it is still configured, removed
// or re-pointed, its refs recorded under its name and the name in
// Branches.ReadThroughBeforeURL, so removing or re-pointing a remote cannot
// hide a push to it. Those reads run inside the repository while its config
// digest is the before's and `git remote -v` works, and outside it
// (LsRemoteHeadsURLOutsideRepo) otherwise, so a node that rewrites the config
// (an insteadOf) or breaks it cannot send them elsewhere or fail them. A
// remote not in before — one added during the leg — is read by name. A read
// through a URL that fails makes the branches part unavailable, as any
// remote's failed read does, its reason naming the remote and never the URL.
// When the after cannot list the remotes, the remotes part is unavailable as
// always, but every remote in before is still read through its URL, so the
// branches part is still observed for them.
func TakeGuardedRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister, before *GuardedBefore) (runstate.RemoteSnapshot, *GuardedBefore) {
	snap, remotes, _ := takeRemoteSnapshotThrough(ctx, obs, prs, before)
	if before != nil {
		return snap, nil
	}
	pin := &GuardedBefore{urls: FetchURLs{}, unresolved: map[string]string{}}
	if path, err := obs.ShallowFile(ctx); err == nil {
		pin.shallowPath = path
		pin.shallow, err = obs.ShallowFingerprint(path)
		pin.shallowRead = err == nil
	}
	if len(remotes) == 0 {
		return snap, pin
	}
	config, err := obs.RepoConfigFingerprint(ctx)
	pin.config, pin.configRead = config, err == nil
	for _, r := range remotes {
		url, err := obs.ResolveFetchURL(ctx, r.FetchURL)
		if err != nil {
			pin.unresolved[r.Name] = err.Error()
			continue
		}
		pin.urls[r.Name] = url
	}
	return snap, pin
}

// takeRemoteSnapshot is TakeRemoteSnapshot, also returning the repository gh
// was expected to read (zero when gh was not asked).
func takeRemoteSnapshot(ctx context.Context, obs RemoteObserver, prs remote.PRLister) (runstate.RemoteSnapshot, ExpectedGHRepo) {
	snap, _, expected := takeRemoteSnapshotThrough(ctx, obs, prs, nil)
	return snap, expected
}

// takeRemoteSnapshotThrough is the snapshot, its git parts read through
// through's URLs (nil: every remote by name), also returning the remotes
// listed and the repository gh was expected to read.
func takeRemoteSnapshotThrough(ctx context.Context, obs RemoteObserver, prs remote.PRLister, through *GuardedBefore) (runstate.RemoteSnapshot, []Remote, ExpectedGHRepo) {
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
// (a corrupted .git/config) cannot hide a push behind it — the reads then run
// outside the repository, whose config is what broke.
func takeGitParts(ctx context.Context, obs RemoteObserver, through *GuardedBefore) (runstate.RemoteSnapshot, []Remote, error) {
	var snap runstate.RemoteSnapshot

	remotes, err := obs.Remotes(ctx)
	outside := through.readOutsideRepo(ctx, obs, err)
	if err != nil {
		reason := remote.SummarizeReason(err.Error())
		snap.Remotes = runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: reason}
		if len(through.readThrough()) > 0 {
			snap.Branches = takeBranches(ctx, obs, nil, through, outside)
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
	snap.Branches = takeBranches(ctx, obs, remotes, through, outside)
	return snap, remotes, nil
}

// takeBranches reads the branches part: each remote in through through its
// URL there — from outside the repository when outside is set — then each of
// remotes not in through by name.
func takeBranches(ctx context.Context, obs RemoteObserver, remotes []Remote, through *GuardedBefore, outside bool) runstate.BranchesSnapshot {
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
	readThrough := through.readThrough()
	for _, name := range readThrough {
		if reason, ok := through.unresolved[name]; ok {
			read(name, nil, errors.New(reason))
			continue
		}
		var heads []RemoteHead
		var err error
		if outside {
			heads, err = obs.LsRemoteHeadsURLOutsideRepo(ctx, through.urls[name])
		} else {
			heads, err = obs.LsRemoteHeadsURL(ctx, through.urls[name])
		}
		read(name, heads, err)
	}
	for _, r := range remotes {
		if through.readsThrough(r.Name) {
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
// runstate.RemoteChanges.EndLeg takes, against the run's recorded base. A
// check that could not be read is a false, as it always was.
func RunCommitCheck(ctx context.Context, obs RemoteObserver, baseSHA string) func(sha string) bool {
	check, _ := GuardedRunCommitCheck(ctx, obs, baseSHA)
	return check
}

// GuardedRunCommitCheck is RunCommitCheck for a guarded leg, with failed
// beside it: whether any check it answered so far could not be read (a git
// read failed), so that its false was not proven. The check is asked
// sequentially, by EndGuardedLeg.
func GuardedRunCommitCheck(ctx context.Context, obs RemoteObserver, baseSHA string) (check func(sha string) bool, failed func() bool) {
	readFailed := false
	check = func(sha string) bool {
		ok, err := obs.CommitAttributable(ctx, sha, baseSHA)
		if err != nil {
			readFailed = true
		}
		return ok
	}
	return check, func() bool { return readFailed }
}

// EndGuardedLeg is runstate.RemoteChanges.EndLeg for a guarded leg: it
// records after the same way, then replaces the leg's diff with
// DiffGuardedRemoteSnapshots's, so branches read through the before URLs are
// compared even when the after could not list the remotes.
func EndGuardedLeg(c *runstate.RemoteChanges, after runstate.RemoteSnapshot, isRunCommit func(sha string) bool) bool {
	if !c.EndLeg(after, isRunCommit) {
		return false
	}
	leg := &c.Legs[len(c.Legs)-1]
	diff := DiffGuardedRemoteSnapshots(leg.Before, after, isRunCommit)
	leg.Changes = &diff
	return true
}

// DiffGuardedRemoteSnapshots is runstate.DiffRemoteSnapshots for a guarded
// leg. When the after could not list the remotes but read the before's
// through their before fetch URLs (TakeGuardedRemoteSnapshot), those refs are
// the same repositories' as the before's, so the branches part is compared as
// if the remotes were the before's, while the remotes part stays not
// compared, with its own reason. Otherwise it is runstate.DiffRemoteSnapshots.
func DiffGuardedRemoteSnapshots(before, after runstate.RemoteSnapshot, isRunCommit func(sha string) bool) runstate.RemoteDiff {
	d := runstate.DiffRemoteSnapshots(before, after, isRunCommit)
	if before.Remotes.Status != runstate.RemotePartObserved || after.Remotes.Status == runstate.RemotePartObserved ||
		after.Branches.Status != runstate.RemotePartObserved || len(after.Branches.ReadThroughBeforeURL) == 0 {
		return d
	}
	standIn := after
	standIn.Remotes = before.Remotes
	branches := runstate.DiffRemoteSnapshots(before, standIn, isRunCommit)

	diff := runstate.RemoteDiff{Observed: []runstate.RemotePart{}, Entries: branches.Entries, NotCompared: []runstate.RemoteNotCompared{}}
	for _, p := range branches.Observed {
		if p != runstate.RemotePartRemotes {
			diff.Observed = append(diff.Observed, p)
		}
	}
	for _, nc := range d.NotCompared {
		if nc.Part == runstate.RemotePartRemotes {
			diff.NotCompared = append(diff.NotCompared, nc)
		}
	}
	diff.NotCompared = append(diff.NotCompared, branches.NotCompared...)
	return diff
}
