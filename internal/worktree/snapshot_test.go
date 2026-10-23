package worktree

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

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
	rc.BeginLeg(TakeRemoteSnapshot(ctx, f.m))
	during()
	if !rc.EndLeg(TakeRemoteSnapshot(ctx, f.m), RunCommitCheck(ctx, f.m, f.base)) {
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
	if !reflect.DeepEqual(diff.Observed, []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches}) {
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
		Observed:    []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches},
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
	before := TakeRemoteSnapshot(ctx, f.m)

	gitIn(t, f.repo, "remote", "add", "broken", filepath.Join(t.TempDir(), "no-such-remote.git"))
	gitIn(t, f.repo, "push", "-q", "origin", "main:refs/heads/pushed")
	after := TakeRemoteSnapshot(ctx, f.m)

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
	snap := TakeRemoteSnapshot(context.Background(), NewGitManager(t.TempDir(), "", "test-run"))
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

	snap := TakeRemoteSnapshot(ctx, fake)
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
	snap = TakeRemoteSnapshot(ctx, fake)
	if snap.Branches.Status != runstate.RemotePartUnavailable {
		t.Fatalf("branches = %+v, want unavailable", snap.Branches)
	}
	if r := snap.Branches.Reason; strings.Contains(r, "pw") || strings.Contains(r, "\n") || !strings.Contains(r, "timed out") {
		t.Errorf("reason = %q, want one redacted line naming the timeout", r)
	}

	fake.SetRemotes(nil, context.DeadlineExceeded)
	snap = TakeRemoteSnapshot(ctx, fake)
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

func TestSummarizeReasonIsBounded(t *testing.T) {
	got := summarizeReason(strings.Repeat("é", 400))
	if len(got) > maxReasonBytes+len(" …") || !strings.HasSuffix(got, " …") {
		t.Errorf("reason of %d bytes not bounded: %q", len(got), got)
	}
	if strings.ContainsRune(strings.TrimSuffix(got, " …"), '�') {
		t.Error("the cut split a rune")
	}
}
