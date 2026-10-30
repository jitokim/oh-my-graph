package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// The verdict of --forbid-remote-changes (ADR 0046 §5.1): which entries fail
// a guarded leg with exit 6, which never do, and how the failed run reads.

// remoteUpstream is the remote a node adds in the remote_added cases.
var remoteUpstream = worktree.Remote{Name: "upstream", FetchURL: "https://example.com/u.git", PushURL: "https://example.com/u.git"}

// remoteChange scripts one change onto obs and prs, made while a node runs.
type remoteChange func(obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister)

func pushBranch(branch, sha string) remoteChange {
	return func(obs *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
		obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}, {Branch: branch, SHA: sha}}, nil)
	}
}

func moveMain(sha string) remoteChange {
	return func(obs *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
		obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: sha}}, nil)
	}
}

func openPR(number int, head, sha string) remoteChange {
	return func(_ *worktree.FakeRemoteObserver, prs *remote.FakePRLister) {
		prs.Set(onGHRepo([]remote.OpenPR{{Number: number, HeadRefName: head, HeadRefOid: sha}}))
	}
}

func addUpstream(obs *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
	origin := worktree.Remote{Name: "origin", FetchURL: "https://example.com/o.git", PushURL: "https://example.com/o.git"}
	obs.SetRemotes([]worktree.Remote{origin, remoteUpstream}, nil)
}

// guardedRemote is originObserver plus a PR lister, with remoteRunSHA the
// run's own commit and remoteBaseSHA the run's base, wired into every leg.
func guardedRemote(t *testing.T, prsBefore []remote.OpenPR) (*worktree.FakeRemoteObserver, *remote.FakePRLister) {
	t.Helper()
	obs := originObserver()
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	prs := ghRepoPRLister()
	prs.Set(onGHRepo(prsBefore))
	usePRLister(t, prs)
	return obs, prs
}

// runWithRemoteChange runs a one-node graph through `run`'s argv, the node
// making change while it runs, and returns stdout and the error.
func runWithRemoteChange(t *testing.T, obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister, change remoteChange, extra ...string) (string, error) {
	t.Helper()
	path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
	nodeRunner := &hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) error { change(obs, prs); return nil }}
	var err error
	out := captureStdout(t, func() {
		err = runGraphWith(append([]string{path}, extra...), nodeRunner, browser.NewFakeOpener(), os.Stdout)
	})
	nodeRunner.mustSetUp(t)
	return out, err
}

// assertRunReads checks the status every reader derives for runID — the
// shared rule, `runs list`'s row and `show`'s status line — and that the
// leg's run_finished says outcome.
func assertRunReads(t *testing.T, runID string, want runstatus.Status, outcome string) {
	t.Helper()
	if status, err := runstatus.Of(runDirFor(runID)); err != nil || status != want {
		t.Errorf("status = %v (%v), want %v", status, err, want)
	}
	var list, warn strings.Builder
	if err := listRuns(&list, &warn, runsRoot(), false); err != nil {
		t.Fatalf("runs list: %v", err)
	}
	if row := lineContaining(t, list.String(), runID); !strings.Contains(row, want.String()) {
		t.Errorf("runs list row = %q, want %s", row, want)
	}
	var show strings.Builder
	if err := showRun(&show, runDirFor(runID), runID); err != nil {
		t.Fatalf("show: %v", err)
	}
	if line := "Run " + runID + " — " + want.String(); !strings.Contains(show.String(), line) {
		t.Errorf("show lacks %q:\n%s", line, show.String())
	}
	leg, err := runfeed.LastLeg(filepath.Join(runDirFor(runID), runfeed.FileName))
	if err != nil {
		t.Fatalf("runfeed.LastLeg: %v", err)
	}
	if leg.Open || leg.LastOutcome != outcome {
		t.Errorf("run feed leg = %+v, want closed with run_finished %q", leg, outcome)
	}
}

// assertAllNodesPassed checks that state.json holds every node PASS: the
// guarded failure is not a node failure.
func assertAllNodesPassed(t *testing.T, runID string, nodes ...string) {
	t.Helper()
	snap := remoteRunSnapshot(t, runID)
	for _, id := range nodes {
		if snap.Nodes[id].Verdict != runstate.VerdictPass {
			t.Errorf("node %s = %+v, want PASS", id, snap.Nodes[id])
		}
	}
}

// ADR 0046 §8.5, §8.16 (first half), §8.19 (remote_added): each forbidden
// kind fails a guarded run with RemoteChangeForbiddenError and exit 6, the
// entry named with the record's own label, every node PASS and the run
// reading FAIL everywhere. §8.4 and §8.19's last sentence: the same change
// unguarded is reported only, and the run exits 0 and reads PASS.
func TestRunGraph_AForbiddenRemoteChangeFailsOnlyAGuardedRun(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change remoteChange
		named  string
	}{
		{"branch_created at a run commit", pushBranch("feat/x", remoteRunSHA),
			"branch_created origin/feat/x at 333333333333 (observed during the run; points at a commit this run made)"},
		{"pr_opened at a run commit", openPR(11, "omg-out", remoteRunSHA),
			"pr_opened #11 omg-out at 333333333333 (observed during the run; points at a commit this run made)"},
		{"remote_added", addUpstream,
			"remote_added upstream https://example.com/u.git (observed, not attributable)"},
	} {
		t.Run(tc.name+", guarded", func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			out, err := runWithRemoteChange(t, obs, prs, tc.change, "--forbid-remote-changes")

			var forbidden *RemoteChangeForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("err = %v, want a *RemoteChangeForbiddenError", err)
			}
			if forbidden.Run != nil || len(forbidden.Entries) != 1 {
				t.Errorf("forbidden = %+v, want one entry and no scheduler error", forbidden)
			}
			if code := exitCodeForError(err); code != 6 {
				t.Errorf("exit code = %d, want 6", code)
			}
			msg := err.Error()
			if !strings.Contains(msg, "--forbid-remote-changes was set: "+tc.named) {
				t.Errorf("message does not name the entry %q:\n%s", tc.named, msg)
			}
			if strings.Contains(msg, "made by") || strings.Contains(out, "made by") {
				t.Errorf("never 'made by':\n%s\n%s", msg, out)
			}
			if !strings.Contains(out, "Remote changes (observed:") {
				t.Errorf("the record must still print:\n%s", out)
			}
			runID := soleRunID(t)
			assertAllNodesPassed(t, runID, "work")
			assertRunReads(t, runID, runstatus.Fail, runfeed.OutcomeFailed)
		})
		t.Run(tc.name+", unguarded", func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			out, err := runWithRemoteChange(t, obs, prs, tc.change)
			if err != nil || exitCodeForError(err) != 0 {
				t.Fatalf("err = %v, want nil and exit 0: an unguarded run only reports", err)
			}
			if entries := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries; len(entries) != 1 {
				t.Errorf("entries = %+v, want the change recorded", entries)
			}
			if !strings.Contains(out, "Remote changes (observed:") {
				t.Errorf("the record must print:\n%s", out)
			}
			assertRunReads(t, soleRunID(t), runstatus.Pass, runfeed.OutcomePassed)
		})
	}
}

// Every entry §5.1 does not list is reported and never fails a guarded run:
// ADR 0046 §8.15, §8.16 (second half), §8.17, §8.18, §8.19 and §8.20.
func TestRunGraph_AGuardedRunNeverFailsOnAnEntryThatIsNotForbidden(t *testing.T) {
	origin := worktree.Remote{Name: "origin", FetchURL: "https://example.com/o.git", PushURL: "https://example.com/o.git"}
	prMain := []remote.OpenPR{{Number: 7, HeadRefName: "feat/seven", HeadRefOid: remoteMainSHA}}
	for _, tc := range []struct {
		name      string
		prsBefore []remote.OpenPR
		change    remoteChange
		want      []runstate.RemoteChangeEntry
	}{
		{
			// §8.15: a sha absent from the object store, and a PR whose head
			// is on no run branch — the fake's not-a-run-commit answer for
			// both, as rev-parse --verify or the ancestry check gives it.
			name: "an unrelated branch and PR",
			change: func(obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister) {
				pushBranch("human", remoteHumaSHA)(obs, prs)
				openPR(12, "human-pr", remoteHumaSHA)(obs, prs)
			},
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.BranchCreated, Remote: "origin", Branch: "human", NewSHA: remoteHumaSHA, Attribution: runstate.AttributionNotAttributable},
				{Kind: runstate.PROpened, PRNumber: 12, HeadRefName: "human-pr", NewSHA: remoteHumaSHA, Attribution: runstate.AttributionNotAttributable},
			},
		},
		{
			// §8.16's second half: a commit reachable only from a branch
			// outside omg/<<runID>>/ fails §5.1's check, so the entry is
			// not_attributable (the check itself is GitManager's, tested in
			// internal/worktree).
			name:   "a branch at a commit outside the run's prefix",
			change: pushBranch("feat/y", remoteHumaSHA),
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/y", NewSHA: remoteHumaSHA, Attribution: runstate.AttributionNotAttributable},
			},
		},
		{
			// §8.17 and §8.18: main moved to a commit that passes the check —
			// a maintainer's merged commit or the run's own push — is
			// run_commit, reported, and never fails.
			name:   "branch_moved to a run commit",
			change: moveMain(remoteRunSHA),
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.BranchMoved, Remote: "origin", Branch: "main", OldSHA: remoteMainSHA, NewSHA: remoteRunSHA, Attribution: runstate.AttributionRunCommit},
			},
		},
		{
			// §8.18: the same for a pr_updated whose new head is the run's.
			name:      "pr_updated to a run commit",
			prsBefore: prMain,
			change:    openPR(7, "feat/seven", remoteRunSHA),
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.PRUpdated, PRNumber: 7, HeadRefName: "feat/seven", OldSHA: remoteMainSHA, NewSHA: remoteRunSHA, Attribution: runstate.AttributionRunCommit},
			},
		},
		{
			// §8.19: the no-sha kinds other than remote_added.
			name: "remote_url_changed and pr_no_longer_open",
			prsBefore: []remote.OpenPR{
				{Number: 7, HeadRefName: "feat/seven", HeadRefOid: remoteMainSHA},
			},
			change: func(obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister) {
				moved := origin
				moved.FetchURL, moved.PushURL = "https://example.com/moved.git", "https://example.com/moved.git"
				obs.SetRemotes([]worktree.Remote{moved}, nil)
				prs.Set(onGHRepo(nil))
			},
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.RemoteURLChanged, Remote: "origin", OldFetchURL: origin.FetchURL, NewFetchURL: "https://example.com/moved.git", OldPushURL: origin.PushURL, NewPushURL: "https://example.com/moved.git", Attribution: runstate.AttributionNotAttributable},
				{Kind: runstate.PRNoLongerOpen, PRNumber: 7, HeadRefName: "feat/seven", OldSHA: remoteMainSHA, Attribution: runstate.AttributionNotAttributable},
			},
		},
		{
			name: "branch_deleted",
			change: func(obs *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
				obs.SetHeads("origin", nil, nil)
			},
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.BranchDeleted, Remote: "origin", Branch: "main", OldSHA: remoteMainSHA, Attribution: runstate.AttributionNotAttributable},
			},
		},
		{
			name: "remote_removed",
			change: func(obs *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
				obs.SetRemotes(nil, nil)
			},
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.RemoteRemoved, Remote: "origin", OldFetchURL: origin.FetchURL, OldPushURL: origin.PushURL, Attribution: runstate.AttributionNotAttributable},
			},
		},
		{
			// §8.20: the run's base commit is an ancestor of every run branch
			// and still never counts.
			name:   "a branch at the run's base commit",
			change: pushBranch("feat/base", remoteBaseSHA),
			want: []runstate.RemoteChangeEntry{
				{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/base", NewSHA: remoteBaseSHA, Attribution: runstate.AttributionNotAttributable},
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, tc.prsBefore)
			out, err := runWithRemoteChange(t, obs, prs, tc.change, "--forbid-remote-changes")
			if err != nil || exitCodeForError(err) != 0 {
				t.Fatalf("err = %v (exit %d), want nil and exit 0: no entry here is forbidden", err, exitCodeForError(err))
			}
			got := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries
			for _, want := range tc.want {
				found := false
				for _, e := range got {
					if e == want {
						found = true
					}
				}
				if !found {
					t.Errorf("entries = %+v, lack %+v", got, want)
				}
			}
			if strings.Contains(out, "made by") {
				t.Errorf("never 'made by':\n%s", out)
			}
			assertRunReads(t, soleRunID(t), runstatus.Pass, runfeed.OutcomePassed)
		})
	}
}

// ADR 0046 §8.7: a guarded two-leg gate run. A push of the run's own commit
// to a new branch and a new remote, both made during the pause, land in
// between_legs, not_attributable; leg 2 is diffed against its own before, so
// neither fails it, and the run passes.
func TestResume_ForbiddenKindsMadeDuringTheGatePauseAreBetweenLegsOnly(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	fake := gateFlowFake()
	pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true})

	pushBranch("feat/x", remoteRunSHA)(obs, prs)
	addUpstream(obs, prs)

	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	})
	if err != nil || exitCodeForError(err) != 0 {
		t.Fatalf("resume = %v (exit %d), want nil and exit 0", err, exitCodeForError(err))
	}
	rc := remoteRunSnapshot(t, "run-1").RemoteChanges
	if rc == nil || len(rc.Legs) != 2 || len(rc.BetweenLegs) != 1 {
		t.Fatalf("remote_changes = %+v, want two legs and one between-legs set", rc)
	}
	between := rc.BetweenLegs[0].Changes.Entries
	if len(between) != 2 {
		t.Fatalf("between_legs entries = %+v, want the branch and the remote", between)
	}
	kinds := map[runstate.RemoteChangeKind]bool{}
	for _, e := range between {
		kinds[e.Kind] = true
		if e.Attribution != runstate.AttributionNotAttributable {
			t.Errorf("between-legs entry %+v is attributed; it must be not_attributable", e)
		}
	}
	if !kinds[runstate.BranchCreated] || !kinds[runstate.RemoteAdded] {
		t.Errorf("between_legs kinds = %v, want branch_created and remote_added", kinds)
	}
	if leg2 := rc.Legs[1].Changes; leg2 == nil || len(leg2.Entries) != 0 {
		t.Errorf("leg 2 changes = %+v, want none: it is diffed against its own before", leg2)
	}
	if !strings.Contains(out, "Remote changes between leg 1 and leg 2") {
		t.Errorf("the between-legs record must print:\n%s", out)
	}
	assertRunReads(t, "run-1", runstatus.Pass, runfeed.OutcomePassed)
}

// The guard is carried to a resumed leg (slice 1), and a forbidden entry of
// that leg's own fails it with exit 6.
func TestResume_AForbiddenRemoteChangeOnAResumedLegFailsIt(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	fake := gateFlowFake()
	pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true})

	nodeRunner := &hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) error {
		if spec.Prompt == "ship" {
			pushBranch("feat/x", remoteRunSHA)(obs, prs)
		}
		return nil
	}}
	var err error
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), nodeRunner, nil)
	})
	nodeRunner.mustSetUp(t)
	var forbidden *RemoteChangeForbiddenError
	if !errors.As(err, &forbidden) || exitCodeForError(err) != 6 {
		t.Fatalf("resume = %v (exit %d), want a *RemoteChangeForbiddenError and exit 6", err, exitCodeForError(err))
	}
	assertAllNodesPassed(t, "run-1", "a", "ship")
	assertRunReads(t, "run-1", runstatus.Fail, runfeed.OutcomeFailed)
}

// ADR 0046 §8.8: --continue-on-fail prunes a failed node's subtree, not a
// forbidden change. Both are reported and the exit is 6.
func TestRunGraph_ContinueOnFailReportsANodeFailureAndAForbiddenChange(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	path := writeGraphFile(t, "name: both\nnodes:\n"+
		"  - { id: bad, prompt: bad, success_check: { exit_zero: true } }\n"+
		"  - { id: after-bad, prompt: after-bad, depends_on: [bad] }\n"+
		"  - { id: push, prompt: push }\n")
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"bad":  {Result: "nope", ExitCode: 1},
		"push": {Result: "PASS", ExitCode: 0},
	})
	nodeRunner := &hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) error {
		if spec.Prompt == "push" {
			pushBranch("feat/x", remoteRunSHA)(obs, prs)
		}
		return nil
	}}
	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path, "--forbid-remote-changes", "--continue-on-fail"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
	})
	nodeRunner.mustSetUp(t)

	var forbidden *RemoteChangeForbiddenError
	if !errors.As(err, &forbidden) {
		t.Fatalf("err = %v, want a *RemoteChangeForbiddenError", err)
	}
	var failed *schedule.RunFailedError
	if !errors.As(forbidden.Run, &failed) || len(failed.FailedNodes) != 1 || failed.FailedNodes[0] != "bad" {
		t.Errorf("forbidden.Run = %v, want the pruned failure of bad", forbidden.Run)
	}
	if code := exitCodeForError(err); code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	for _, want := range []string{
		"branch_created origin/feat/x at 333333333333 (observed during the run; points at a commit this run made)",
		"the leg also failed: run completed with failed node(s): bad",
	} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("message lacks %q:\n%s", want, err.Error())
		}
	}
	if isRunFailure(err) != true {
		t.Error("a goal loop must read the guarded failure as a failed run")
	}
	assertRunReads(t, soleRunID(t), runstatus.Fail, runfeed.OutcomeFailed)
}

// A leg that pauses ends there, and its after snapshot is taken at that end
// (ADR 0046 §2.1, §3.1), so a forbidden entry made before the pause is the
// leg's own and is checked "whatever scheduler.Run returned" (§3.1). The ADR
// does not say what becomes of the pause; the leg fails with exit 6 as an
// interrupt on the after snapshot fails one: the pause is withdrawn from
// state.json, run_finished says failed, no pause hint prints, and the run
// reads FAIL. Both pause shapes, a gate and a session limit.
func TestRunGraph_AForbiddenRemoteChangeOnAPausedLegFailsItAndWithdrawsThePause(t *testing.T) {
	for _, tc := range []struct {
		name    string
		graph   string
		fake    *runner.FakeRunner
		pausing string
	}{
		{"a gate pause", gateFlowGraph, gateFlowFake(), "paused at gate \"approve\""},
		{"a session-limit pause",
			`{"name":"limit","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b","depends_on":["a"]}]}`,
			runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a": {Result: "PASS", ExitCode: 0},
				"b": {ExitCode: 1, FailureCause: "usage limit reached", SessionLimited: true},
			}),
			"paused on the session limit at b"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			nodeRunner := &hookRunner{FakeRunner: tc.fake, before: func(spec runner.NodeInvocation) error {
				if spec.Prompt == "a" {
					pushBranch("feat/x", remoteRunSHA)(obs, prs)
				}
				return nil
			}}
			var err error
			out := captureStdout(t, func() {
				err = executeGraph(context.Background(), "run-1", mustParse(t, tc.graph), nodeRunner,
					commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true}, nil, 0, "g.yaml", []byte("name: g\n"), false, nil, nil, nil)
			})
			nodeRunner.mustSetUp(t)
			var forbidden *RemoteChangeForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("err = %v, want a *RemoteChangeForbiddenError", err)
			}
			if code := exitCodeForError(err); code != 6 {
				t.Errorf("exit code = %d, want 6, not a pause's 2", code)
			}
			var paused *schedule.PausedError
			var limited *schedule.LimitPausedError
			if errors.As(err, &paused) || errors.As(err, &limited) {
				t.Errorf("err %v still reads as a pause", err)
			}
			if !strings.Contains(err.Error(), tc.pausing+", and that pause is withdrawn") {
				t.Errorf("message does not say the pause was withdrawn:\n%s", err.Error())
			}
			snap := remoteRunSnapshot(t, "run-1")
			if snap.Gate.PausedAt != "" || snap.Gate.Decisions["approve"] == runstate.GatePause || snap.LimitPause != nil {
				t.Errorf("state.json gate = %+v, limit pause = %+v; want the pause withdrawn", snap.Gate, snap.LimitPause)
			}
			if strings.Contains(out, "Paused at gate") || strings.Contains(out, "--approve approve") || strings.Contains(out, "session limit") {
				t.Errorf("a failed leg printed a pause hint:\n%s", out)
			}
			leg, legErr := runfeed.LastLeg(filepath.Join(runDirFor("run-1"), runfeed.FileName))
			if legErr != nil {
				t.Fatalf("runfeed.LastLeg: %v", legErr)
			}
			if leg.LastOutcome != runfeed.OutcomeFailed {
				t.Errorf("run_finished = %q, want %q", leg.LastOutcome, runfeed.OutcomeFailed)
			}
			if status, statusErr := runstatus.Of(runDirFor("run-1")); statusErr != nil || status != runstatus.Fail {
				t.Errorf("status = %v (%v), want FAIL", status, statusErr)
			}
		})
	}
}

// addingRemoteObserver adds remoteUpstream once its before snapshot has been
// read: a change made during a leg that runs no node.
type addingRemoteObserver struct {
	*worktree.FakeRemoteObserver
	calls int
}

func (o *addingRemoteObserver) Remotes(ctx context.Context) ([]worktree.Remote, error) {
	o.calls++
	if o.calls == 2 {
		addUpstream(o.FakeRemoteObserver, nil)
	}
	return o.FakeRemoteObserver.Remotes(ctx)
}

// Through the real exit path: mainExitCode maps the guarded failure to 6 and
// prints it on stderr, like exit 1's error line.
func TestMainExitCode_AForbiddenRemoteChangeExits6AndPrintsWhy(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, &addingRemoteObserver{FakeRemoteObserver: originObserver()}, remoteBaseSHA)
	graphPath := writeGraphFile(t, "name: gate-only\nnodes:\n  - { id: approve, type: gate }\n")

	var code int
	var stderr string
	captureStdout(t, func() {
		stderr, _ = captureStderr(t, func() error {
			code = mainExitCode([]string{"run", graphPath, "--auto-approve", "approve", "--forbid-remote-changes"})
			return nil
		})
	})
	if code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	if want := "oh-my-graph: new remote state observed during the run, and --forbid-remote-changes was set: remote_added upstream"; !strings.Contains(stderr, want) {
		t.Errorf("stderr lacks %q:\n%s", want, stderr)
	}
}

// exitCodeForError reads the guarded failure before the pauses: a
// withdrawn pause it carries never maps to 2.
func TestExitCodeForError_RemoteChangeForbiddenIs6WhateverItCarries(t *testing.T) {
	for _, run := range []error{nil, &schedule.PausedError{GateID: "g"}, &schedule.LimitPausedError{NodeIDs: []string{"n"}}, &schedule.RunFailedError{FailedNodes: []string{"n"}}} {
		err := &RemoteChangeForbiddenError{Entries: []runstate.RemoteChangeEntry{{Kind: runstate.RemoteAdded, Remote: "u"}}, Run: run}
		if code := exitCodeForError(err); code != 6 {
			t.Errorf("Run %v: exit code = %d, want 6", run, code)
		}
	}
}

// remoteEntryForbidden is §5.1's list exactly: every kind, both attributions.
func TestRemoteEntryForbidden_IsTheADRsListAndNothingElse(t *testing.T) {
	forbidden := map[runstate.RemoteChangeKind]map[runstate.RemoteAttribution]bool{
		runstate.BranchCreated: {runstate.AttributionRunCommit: true},
		runstate.PROpened:      {runstate.AttributionRunCommit: true},
		runstate.RemoteAdded:   {runstate.AttributionRunCommit: true, runstate.AttributionNotAttributable: true},
	}
	for _, kind := range []runstate.RemoteChangeKind{
		runstate.RemoteAdded, runstate.RemoteRemoved, runstate.RemoteURLChanged,
		runstate.BranchCreated, runstate.BranchMoved, runstate.BranchDeleted,
		runstate.PROpened, runstate.PRUpdated, runstate.PRNoLongerOpen,
	} {
		for _, attr := range []runstate.RemoteAttribution{runstate.AttributionRunCommit, runstate.AttributionNotAttributable} {
			got := remoteEntryForbidden(runstate.RemoteChangeEntry{Kind: kind, Attribution: attr})
			if want := forbidden[kind][attr]; got != want {
				t.Errorf("%s/%s forbidden = %v, want %v", kind, attr, got, want)
			}
		}
	}
}
