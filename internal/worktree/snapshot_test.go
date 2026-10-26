package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// ghRepoPRs is a FakePRLister run with GH_REPO set to o/r, so gh is asked
// whatever the remotes are: with no GitHub remote it is not (#410).
func ghRepoPRs() *remote.FakePRLister {
	prs := remote.NewFakePRLister()
	prs.SetRepoEnv(remote.RepoEnv{Repo: "o/r"})
	return prs
}

// onGHRepo gives each of prs a URL on o/r, the repository the snapshot
// checks gh's answer against.
func onGHRepo(prs []remote.OpenPR) []remote.OpenPR {
	for i := range prs {
		prs[i].URL = "https://github.com/o/r/pull/" + strconv.Itoa(prs[i].Number)
	}
	return prs
}

// remoteFixture is an invocation repository whose origin is a bare repo in
// t.TempDir(), with main pushed, and the run's GitManager over it.
type remoteFixture struct {
	m      *GitManager
	repo   string
	origin string
	base   string
}

func newRemoteFixture(t *testing.T) remoteFixture {
	t.Helper()
	m, repo := newTestManager(t)
	origin := initBareRemote(t)
	gitIn(t, repo, "remote", "add", "origin", origin)
	gitIn(t, repo, "push", "-q", "origin", "main")
	return remoteFixture{m: m, repo: repo, origin: origin, base: gitIn(t, repo, "rev-parse", "HEAD")}
}

// leg takes a before snapshot, runs during, takes an after, and returns the
// leg's diff, attributed against the fixture's base commit.
func (f remoteFixture) leg(t *testing.T, during func()) runstate.RemoteDiff {
	t.Helper()
	ctx := context.Background()
	var rc runstate.RemoteChanges
	rc.BeginLeg(TakeRemoteSnapshot(ctx, f.m, ghRepoPRs()))
	during()
	if !rc.EndLeg(TakeRemoteSnapshot(ctx, f.m, ghRepoPRs()), RunCommitCheck(ctx, f.m, f.base)) {
		t.Fatal("EndLeg recorded nothing")
	}
	return *rc.Legs[0].Changes
}

func TestRemoteLeg_RunCommitPushedToANewBranchIsRunCommit(t *testing.T) {
	f := newRemoteFixture(t)
	var runCommit string
	diff := f.leg(t, func() {
		lane, err := f.m.Acquire(context.Background(), "lane")
		if err != nil {
			t.Fatalf("Acquire: %v", err)
		}
		runCommit = commitIn(t, lane, "node work")
		gitIn(t, lane, "push", "-q", "origin", "HEAD:feat/run")
	})
	want := []runstate.RemoteChangeEntry{{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/run",
		NewSHA: runCommit, Attribution: runstate.AttributionRunCommit}}
	if !reflect.DeepEqual(diff.Entries, want) {
		t.Fatalf("entries = %+v\nwant      %+v", diff.Entries, want)
	}
	if !reflect.DeepEqual(diff.Observed, []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches, runstate.RemotePartPullRequests}) {
		t.Errorf("observed = %v", diff.Observed)
	}
}

// A branch pushed at a commit the run did not make — one on no run branch,
// or the base the run started from — is recorded, and not attributable.
func TestRemoteLeg_ACommitTheRunDidNotMakeIsNotAttributable(t *testing.T) {
	f := newRemoteFixture(t)
	if _, err := f.m.Acquire(context.Background(), "lane"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	var foreign string
	diff := f.leg(t, func() {
		gitIn(t, f.repo, "checkout", "-q", "-b", "human")
		foreign = commitIn(t, f.repo, "a human's commit")
		gitIn(t, f.repo, "push", "-q", "origin", "human")
		gitIn(t, f.repo, "push", "-q", "origin", f.base+":refs/heads/at-base")
	})
	want := []runstate.RemoteChangeEntry{
		{Kind: runstate.BranchCreated, Remote: "origin", Branch: "at-base", NewSHA: f.base, Attribution: runstate.AttributionNotAttributable},
		{Kind: runstate.BranchCreated, Remote: "origin", Branch: "human", NewSHA: foreign, Attribution: runstate.AttributionNotAttributable},
	}
	if !reflect.DeepEqual(diff.Entries, want) {
		t.Fatalf("entries = %+v\nwant      %+v", diff.Entries, want)
	}
}

func TestRemoteLeg_MovedAndDeletedBranches(t *testing.T) {
	f := newRemoteFixture(t)
	gitIn(t, f.repo, "push", "-q", "origin", "main:refs/heads/doomed")
	lane, err := f.m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	var runCommit string
	diff := f.leg(t, func() {
		runCommit = commitIn(t, lane, "node work")
		gitIn(t, lane, "push", "-q", "origin", "HEAD:main")
		gitIn(t, f.repo, "push", "-q", "origin", "--delete", "doomed")
	})
	want := []runstate.RemoteChangeEntry{
		{Kind: runstate.BranchDeleted, Remote: "origin", Branch: "doomed", OldSHA: f.base, Attribution: runstate.AttributionNotAttributable},
		{Kind: runstate.BranchMoved, Remote: "origin", Branch: "main", OldSHA: f.base, NewSHA: runCommit, Attribution: runstate.AttributionRunCommit},
	}
	if !reflect.DeepEqual(diff.Entries, want) {
		t.Fatalf("entries = %+v\nwant      %+v", diff.Entries, want)
	}
}

func TestRemoteLeg_AddedRemoteAndChangedURL(t *testing.T) {
	f := newRemoteFixture(t)
	mirror, moved := initBareRemote(t), initBareRemote(t)
	diff := f.leg(t, func() {
		gitIn(t, f.repo, "remote", "add", "mirror", mirror)
		gitIn(t, f.repo, "remote", "set-url", "origin", moved)
	})
	want := []runstate.RemoteChangeEntry{
		{Kind: runstate.RemoteAdded, Remote: "mirror", NewFetchURL: mirror, NewPushURL: mirror, Attribution: runstate.AttributionNotAttributable},
		{Kind: runstate.RemoteURLChanged, Remote: "origin", OldFetchURL: f.origin, NewFetchURL: moved,
			OldPushURL: f.origin, NewPushURL: moved, Attribution: runstate.AttributionNotAttributable},
	}
	if !reflect.DeepEqual(diff.Entries, want) {
		t.Fatalf("entries = %+v\nwant      %+v", diff.Entries, want)
	}
	// origin's main is in neither repository's place any more; it is not
	// reported as deleted, and neither remote's refs are compared.
	if len(diff.NotCompared) != 2 {
		t.Errorf("not_compared = %+v, want mirror's and origin's refs", diff.NotCompared)
	}
}

func TestRemoteLeg_NothingChanged(t *testing.T) {
	f := newRemoteFixture(t)
	diff := f.leg(t, func() {})
	want := runstate.RemoteDiff{
		Observed:    []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches, runstate.RemotePartPullRequests},
		Entries:     []runstate.RemoteChangeEntry{},
		NotCompared: []runstate.RemoteNotCompared{},
	}
	if !reflect.DeepEqual(diff, want) {
		t.Fatalf("diff = %+v, want %+v", diff, want)
	}
}

// A remote whose URL leads nowhere makes ls-remote fail: the branches part
// is unavailable with git's reason, and the diff compares no branch — the
// push that did happen is not reported as anything, and nothing is
// reported as deleted.
func TestRemoteLeg_ABadRemoteURLMakesBranchesUnavailable(t *testing.T) {
	f := newRemoteFixture(t)
	ctx := context.Background()
	before := TakeRemoteSnapshot(ctx, f.m, ghRepoPRs())

	gitIn(t, f.repo, "remote", "add", "broken", filepath.Join(t.TempDir(), "no-such-remote.git"))
	gitIn(t, f.repo, "push", "-q", "origin", "main:refs/heads/pushed")
	after := TakeRemoteSnapshot(ctx, f.m, ghRepoPRs())

	if after.Branches.Status != runstate.RemotePartUnavailable || !strings.Contains(after.Branches.Reason, "ls-remote broken:") {
		t.Fatalf("branches = %+v, want unavailable naming the broken remote", after.Branches)
	}
	if len(after.Branches.Refs) != 0 {
		t.Errorf("an unavailable part kept refs: %+v", after.Branches.Refs)
	}
	if after.Remotes.Status != runstate.RemotePartObserved {
		t.Errorf("remotes = %+v; one bad URL must not blind the remotes part", after.Remotes)
	}

	diff := runstate.DiffRemoteSnapshots(before, after, RunCommitCheck(ctx, f.m, f.base))
	for _, e := range diff.Entries {
		if e.Kind != runstate.RemoteAdded {
			t.Errorf("entry %+v from an uncompared part", e)
		}
	}
	if len(diff.NotCompared) != 1 || diff.NotCompared[0].Part != runstate.RemotePartBranches ||
		!strings.Contains(diff.NotCompared[0].Reason, "unavailable after: ls-remote broken:") {
		t.Fatalf("not_compared = %+v, want branches with the ls-remote reason", diff.NotCompared)
	}
}

func TestTakeRemoteSnapshot_OutsideARepository(t *testing.T) {
	isolateGitConfig(t, filepath.Dir(t.TempDir()))
	snap := TakeRemoteSnapshot(context.Background(), NewGitManager(t.TempDir(), "", "test-run"), remote.NewFakePRLister())
	if snap.Remotes.Status != runstate.RemotePartUnavailable || !strings.Contains(snap.Remotes.Reason, "not a git repository") {
		t.Errorf("remotes = %+v, want unavailable: not a git repository", snap.Remotes)
	}
	if snap.Branches.Status != runstate.RemotePartUnavailable || snap.Branches.Reason == "" {
		t.Errorf("branches = %+v, want unavailable with a reason", snap.Branches)
	}
}

func TestTakeRemoteSnapshot_FromTheFake(t *testing.T) {
	ctx := context.Background()
	sha := strings.Repeat("a", 40)
	fake := NewFakeRemoteObserver()
	fake.SetRemotes([]Remote{
		{Name: "origin", FetchURL: "https://x-access-token:ghs_secret@github.com/o/r.git", PushURL: "https://ghp_secret@github.com/o/r.git"},
		{Name: "ssh", FetchURL: "ssh://git@example.com/r.git", PushURL: "ssh://git:hunter2@example.com/r.git"},
	}, nil)
	fake.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: sha}}, nil)

	snap := TakeRemoteSnapshot(ctx, fake, remote.NewFakePRLister())
	wantRemotes := []runstate.RemoteConfig{
		{Name: "origin", FetchURL: "https://***@github.com/o/r.git", PushURL: "https://***@github.com/o/r.git"},
		{Name: "ssh", FetchURL: "ssh://git@example.com/r.git", PushURL: "ssh://git:***@example.com/r.git"},
	}
	if !reflect.DeepEqual(snap.Remotes.Configured, wantRemotes) {
		t.Errorf("remotes = %+v\nwant      %+v", snap.Remotes.Configured, wantRemotes)
	}
	if want := []runstate.RemoteBranchRef{{Remote: "origin", Branch: "main", SHA: sha}}; !reflect.DeepEqual(snap.Branches.Refs, want) {
		t.Errorf("refs = %+v, want %+v", snap.Branches.Refs, want)
	}

	fake.SetHeads("ssh", nil, errors.New("git ls-remote --heads ssh: timed out after 30s: signal: killed: fatal: https://u:pw@example.com\nsecond line"))
	snap = TakeRemoteSnapshot(ctx, fake, remote.NewFakePRLister())
	if snap.Branches.Status != runstate.RemotePartUnavailable {
		t.Fatalf("branches = %+v, want unavailable", snap.Branches)
	}
	if r := snap.Branches.Reason; strings.Contains(r, "pw") || strings.Contains(r, "\n") || !strings.Contains(r, "timed out") {
		t.Errorf("reason = %q, want one redacted line naming the timeout", r)
	}

	fake.SetRemotes(nil, context.DeadlineExceeded)
	snap = TakeRemoteSnapshot(ctx, fake, remote.NewFakePRLister())
	if snap.Remotes.Status != runstate.RemotePartUnavailable || snap.Branches.Status != runstate.RemotePartUnavailable {
		t.Errorf("snapshot = %+v, want both parts unavailable", snap)
	}
}

func TestRunCommitCheck_AsksTheObserverAgainstTheBase(t *testing.T) {
	fake := NewFakeRemoteObserver()
	run, base := strings.Repeat("c", 40), strings.Repeat("d", 40)
	fake.SetRunCommit(run)
	fake.SetRunCommit(base)
	check := RunCommitCheck(context.Background(), fake, base)
	if !check(run) || check(base) || check(strings.Repeat("e", 40)) {
		t.Error("RunCommitCheck did not pass exactly the run commit")
	}
	if got := fake.AttributionCalls(); len(got) != 3 {
		t.Errorf("attribution calls = %v", got)
	}
	if RunCommitCheck(context.Background(), fake, "")(run) {
		t.Error("a check with no recorded base passed")
	}
}

// GuardedRunCommitCheck answers as RunCommitCheck does, and says whether any
// check it answered could not be read.
func TestGuardedRunCommitCheck_SaysWhenACheckCouldNotBeRead(t *testing.T) {
	fake := NewFakeRemoteObserver()
	run, base, other := strings.Repeat("c", 40), strings.Repeat("d", 40), strings.Repeat("e", 40)
	fake.SetRunCommit(run)
	check, failed := GuardedRunCommitCheck(context.Background(), fake, base)
	if !check(run) || check(other) || failed() {
		t.Fatalf("proven answers: run %v, other %v, failed %v", check(run), check(other), failed())
	}
	fake.SetAttributionErr(errors.New("git rev-parse: exit status 128"))
	if !check(run) || failed() {
		t.Error("a passing check read as failed")
	}
	if check(other) || !failed() {
		t.Error("a check that could not be read did not say so")
	}
	if RunCommitCheck(context.Background(), fake, base)(other) {
		t.Error("RunCommitCheck passed a check that could not be read")
	}
}

// failingNthCheck is a FakeRemoteObserver whose CommitAttributable fails
// with err on the nth time it is asked about sha, and answers as the fake
// does otherwise.
type failingNthCheck struct {
	*FakeRemoteObserver
	sha string
	nth int
	err error
	n   int
}

func (f *failingNthCheck) CommitAttributable(ctx context.Context, sha, baseSHA string) (bool, error) {
	ok, err := f.FakeRemoteObserver.CommitAttributable(ctx, sha, baseSHA)
	if sha == f.sha {
		if f.n++; f.n == f.nth {
			return false, f.err
		}
	}
	return ok, err
}

// A guarded leg whose after could not list the remotes diffs its snapshots
// more than once, and only the last diff is kept. Within one EndGuardedLeg
// each sha is checked once, so a failure in what was a discarded check — the
// second check of the PR's head — can no longer make the attribution
// unavailable.
func TestEndGuardedLeg_ChecksEachSHAOnce(t *testing.T) {
	ctx := context.Background()
	run, base := strings.Repeat("c", 40), strings.Repeat("d", 40)
	origin := []runstate.RemoteConfig{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}
	before := runstate.RemoteSnapshot{
		Remotes:      runstate.RemotesSnapshot{Status: runstate.RemotePartObserved, Configured: origin},
		Branches:     runstate.BranchesSnapshot{Status: runstate.RemotePartObserved},
		PullRequests: runstate.PullRequestsSnapshot{Status: runstate.RemotePartObserved},
	}
	after := runstate.RemoteSnapshot{
		Remotes: runstate.RemotesSnapshot{Status: runstate.RemotePartUnavailable, Reason: "fatal: bad config"},
		Branches: runstate.BranchesSnapshot{Status: runstate.RemotePartObserved, ReadThroughBeforeURL: []string{"origin"},
			Refs: []runstate.RemoteBranchRef{{Remote: "origin", Branch: "feat/run", SHA: run}}},
		PullRequests: runstate.PullRequestsSnapshot{Status: runstate.RemotePartObserved,
			Open: []runstate.OpenPullRequest{{Number: 1, HeadRefName: "feat/run", HeadRefOid: run}}},
	}

	fake := NewFakeRemoteObserver()
	fake.SetRemotes([]Remote{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}, nil)
	fake.SetRunCommit(run)
	_, pin := TakeGuardedRemoteSnapshot(ctx, fake, ghRepoPRs(), nil)
	obs := &failingNthCheck{FakeRemoteObserver: fake, sha: run, nth: 2, err: context.DeadlineExceeded}

	var c runstate.RemoteChanges
	c.BeginLeg(before)
	check, failed := GuardedRunCommitCheck(ctx, obs, base)
	if !EndGuardedLeg(&c, after, check) {
		t.Fatal("EndGuardedLeg recorded nothing")
	}
	if calls := fake.AttributionCalls(); !reflect.DeepEqual(calls, []string{run}) {
		t.Errorf("attribution calls = %v, want %s once", calls, run)
	}
	if got := pin.AttributionUnavailable(obs, failed()); got != "" {
		t.Errorf("AttributionUnavailable = %q, want none", got)
	}
	for _, e := range c.Legs[0].Changes.Entries {
		if e.Attribution != runstate.AttributionRunCommit {
			t.Errorf("entry %+v, want run_commit", e)
		}
	}
	if n := len(c.Legs[0].Changes.Entries); n != 2 {
		t.Errorf("entries = %+v, want the branch and the PR", c.Legs[0].Changes.Entries)
	}
}

// The open-PR part is read beside the git parts, recorded sorted by number,
// and diffed with the same §5.1 check the branch entries use.
func TestTakeRemoteSnapshot_PullRequestsFromTheFake(t *testing.T) {
	ctx := context.Background()
	run, them := strings.Repeat("a", 40), strings.Repeat("b", 40)
	obs := NewFakeRemoteObserver()
	obs.SetRunCommit(run)
	prs := ghRepoPRs()
	prs.Set(remote.ObservedPRs(onGHRepo([]remote.OpenPR{{Number: 7, HeadRefName: "fix", HeadRefOid: them}})))

	var rc runstate.RemoteChanges
	before := TakeRemoteSnapshot(ctx, obs, prs)
	if before.PullRequests.Status != runstate.RemotePartObserved {
		t.Fatalf("pull requests = %+v, want observed", before.PullRequests)
	}
	rc.BeginLeg(before)

	prs.Set(remote.ObservedPRs(onGHRepo([]remote.OpenPR{
		{Number: 9, HeadRefName: "omg/feat", HeadRefOid: run},
		{Number: 8, HeadRefName: "theirs", HeadRefOid: them},
	})))
	after := TakeRemoteSnapshot(ctx, obs, prs)
	want := []runstate.OpenPullRequest{{Number: 8, HeadRefName: "theirs", HeadRefOid: them}, {Number: 9, HeadRefName: "omg/feat", HeadRefOid: run}}
	if !reflect.DeepEqual(after.PullRequests.Open, want) {
		t.Errorf("open = %+v, want %+v", after.PullRequests.Open, want)
	}
	rc.EndLeg(after, RunCommitCheck(ctx, obs, strings.Repeat("0", 40)))

	got := rc.Legs[0].Changes.Entries
	wantEntries := []runstate.RemoteChangeEntry{
		{Kind: runstate.PRNoLongerOpen, PRNumber: 7, HeadRefName: "fix", OldSHA: them, Attribution: runstate.AttributionNotAttributable},
		{Kind: runstate.PROpened, PRNumber: 8, HeadRefName: "theirs", NewSHA: them, Attribution: runstate.AttributionNotAttributable},
		{Kind: runstate.PROpened, PRNumber: 9, HeadRefName: "omg/feat", NewSHA: run, Attribution: runstate.AttributionRunCommit},
	}
	if !reflect.DeepEqual(got, wantEntries) {
		t.Errorf("entries = %+v\nwant      %+v", got, wantEntries)
	}
	if prs.Calls() != 2 {
		t.Errorf("lister asked %d times, want once per snapshot", prs.Calls())
	}
}

// Every way gh can fail is an unavailable part with its reason, never an
// empty list, and gh's detail is recorded redacted.
func TestTakeRemoteSnapshot_PullRequestsUnavailable(t *testing.T) {
	cases := []struct {
		name   string
		list   remote.PRList
		reason string
	}{
		{"gh not on PATH", remote.UnavailablePRs(remote.ReasonGHNotFound, ""), "gh not found"},
		{"not authenticated", remote.UnavailablePRs(remote.ReasonNotAuthenticated, "To get started with GitHub CLI, please run: gh auth login"), "not authenticated"},
		{"non-zero exit", remote.UnavailablePRs(remote.ReasonNonZeroExit, "exit status 1: could not resolve https://ghp_secret@github.com/o/r"), "non-zero exit"},
		{"timeout", remote.UnavailablePRs(remote.ReasonTimeout, "gh pr list timed out after 30s"), "timeout"},
		{"unparsable output", remote.UnavailablePRs(remote.ReasonUnparsable, "not a JSON list"), "unparsable output"},
		{"a raw detail from another lister", remote.PRList{Status: runstate.RemotePartUnavailable, Reason: remote.ReasonNonZeroExit,
			Detail: "fatal: https://x-access-token:ghs_secret@github.com/o/r\nsecond line"}, "non-zero exit"},
		{"no status at all", remote.PRList{}, "no reason given"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			prs := ghRepoPRs()
			prs.Set(tc.list)
			got := TakeRemoteSnapshot(context.Background(), NewFakeRemoteObserver(), prs).PullRequests
			if got.Status != runstate.RemotePartUnavailable || got.Reason != tc.reason || got.Open != nil {
				t.Errorf("pull requests = %+v, want unavailable %q with no list", got, tc.reason)
			}
			if strings.Contains(got.Detail, "secret") || strings.Contains(got.Detail, "\n") {
				t.Errorf("detail = %q, want one redacted line", got.Detail)
			}
		})
	}
}

// A repository whose remotes git cannot read is not asked about its pull
// requests unless GH_REPO names the repository (#410: which repository gh
// would read is unknown), and an unavailable PR part leaves the git parts
// observed.
func TestTakeRemoteSnapshot_PullRequestsStandApartFromGit(t *testing.T) {
	obs := NewFakeRemoteObserver()
	obs.SetRemotes(nil, errors.New("fatal: not a git repository"))
	prs := ghRepoPRs()
	snap := TakeRemoteSnapshot(context.Background(), obs, prs)
	if snap.Remotes.Status != runstate.RemotePartUnavailable || snap.PullRequests.Status != runstate.RemotePartObserved || prs.Calls() != 1 {
		t.Errorf("snapshot = %+v (lister calls %d), want git unavailable and PRs observed", snap, prs.Calls())
	}

	prs.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
	snap = TakeRemoteSnapshot(context.Background(), NewFakeRemoteObserver(), prs)
	if snap.Remotes.Status != runstate.RemotePartObserved || snap.Branches.Status != runstate.RemotePartObserved {
		t.Errorf("snapshot = %+v, want the git parts observed", snap)
	}
}

// prListLimit copies internal/remote's unexported --limit (ADR 0046 §2.1),
// which stays unexported, so this is the literal 1000. internal/remote's
// TestPrListLimitIsTheValueOtherPackagesCopy pins that value, so a change to
// it in either direction fails there before this copy goes stale.
const prListLimit = 1000

// ADR 0046 §8.13: a lister answering exactly remote's prListLimit PRs — here
// the fake, which applies no limit of its own — records the part unavailable,
// "truncated", and the diff does not compare it; one fewer is observed.
func TestTakeRemoteSnapshot_AListOfTheWholeLimitIsTruncated(t *testing.T) {
	full := make([]remote.OpenPR, prListLimit)
	for i := range full {
		full[i] = remote.OpenPR{Number: i + 1, HeadRefName: "b", HeadRefOid: strings.Repeat("c", 40)}
	}
	onGHRepo(full)
	prs := ghRepoPRs()
	prs.Set(remote.ObservedPRs(full[:prListLimit-1]))
	before := TakeRemoteSnapshot(context.Background(), NewFakeRemoteObserver(), prs)
	if before.PullRequests.Status != runstate.RemotePartObserved || len(before.PullRequests.Open) != prListLimit-1 {
		t.Fatalf("one under the limit: status %q with %d PRs, want observed", before.PullRequests.Status, len(before.PullRequests.Open))
	}

	prs.Set(remote.ObservedPRs(full))
	after := TakeRemoteSnapshot(context.Background(), NewFakeRemoteObserver(), prs)
	if after.PullRequests.Status != runstate.RemotePartUnavailable || after.PullRequests.Reason != remote.ReasonTruncated || after.PullRequests.Open != nil {
		t.Fatalf("pull requests = status %q reason %q with %d PRs, want unavailable truncated",
			after.PullRequests.Status, after.PullRequests.Reason, len(after.PullRequests.Open))
	}

	d := runstate.DiffRemoteSnapshots(before, after, func(string) bool { return true })
	for _, e := range d.Entries {
		t.Errorf("entry %+v from a truncated list", e)
	}
	nc := d.NotCompared
	if len(nc) != 1 || nc[0].Part != runstate.RemotePartPullRequests || nc[0].Reason != "unavailable after: truncated" {
		t.Errorf("not_compared = %+v, want pull_requests: unavailable after: truncated", nc)
	}
}
