package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// TestMain puts every test in this package on a quiet FakeRemoteObserver (no
// remotes, every part observed — the PR lister's GH_REPO names the repo gh
// reads) and an empty run base, so no test that runs a leg reaches a real
// remote from the package directory's own repository. A test about the
// record swaps in its own through useRemoteObserver.
func TestMain(m *testing.M) {
	productionRemotePRListerFor = remotePRListerFor
	remoteObserverFor = func(string) worktree.RemoteObserver { return worktree.NewFakeRemoteObserver() }
	remotePRListerFor = func(string) remote.PRLister { return ghRepoPRLister() }
	runBaseCommit = func() string { return "" }
	os.Exit(m.Run())
}

// productionRemoteObserverFor is the wiring TestMain replaces, for the test
// that drives the real GitManager against a temp repository.
func productionRemoteObserverFor(runID string) worktree.RemoteObserver {
	return worktreeManagerFor(runID)
}

// productionRemotePRListerFor is the PR wiring TestMain replaces. It is only
// ever inspected, never asked: asking it would run a real gh.
var productionRemotePRListerFor func(string) remote.PRLister

// testGHRepo is the GH_REPO ghRepoPRLister sets: with it gh is asked
// whatever the remotes are, as it is not when no GitHub remote names a repo
// (#410).
const testGHRepo = "o/r"

// ghRepoPRLister is a FakePRLister run with GH_REPO set to testGHRepo.
func ghRepoPRLister() *remote.FakePRLister {
	prs := remote.NewFakePRLister()
	prs.SetRepoEnv(remote.RepoEnv{Repo: testGHRepo})
	return prs
}

// onGHRepo is an observed list of prs, each given a URL on testGHRepo, the
// repository the snapshot checks gh's answer against.
func onGHRepo(prs []remote.OpenPR) remote.PRList {
	for i := range prs {
		prs[i].URL = "https://github.com/" + testGHRepo + "/pull/" + strconv.Itoa(prs[i].Number)
	}
	return remote.ObservedPRs(prs)
}

// usePRLister makes every leg in this test read its open pull requests from
// prs, and records the run id each leg built its lister for.
func usePRLister(t *testing.T, prs remote.PRLister) *[]string {
	t.Helper()
	prev := remotePRListerFor
	t.Cleanup(func() { remotePRListerFor = prev })
	var runIDs []string
	remotePRListerFor = func(runID string) remote.PRLister {
		runIDs = append(runIDs, runID)
		return prs
	}
	return &runIDs
}

// useRemoteObserver makes every leg in this test read obs, with base as the
// run's base commit.
func useRemoteObserver(t *testing.T, obs worktree.RemoteObserver, base string) {
	t.Helper()
	prevObs, prevBase := remoteObserverFor, runBaseCommit
	t.Cleanup(func() { remoteObserverFor, runBaseCommit = prevObs, prevBase })
	remoteObserverFor = func(string) worktree.RemoteObserver { return obs }
	runBaseCommit = func() string { return base }
}

// hookRunner is a FakeRunner that calls before ahead of each node — where a
// test scripts what a node does to the remote while it runs.
type hookRunner struct {
	*runner.FakeRunner
	before func(spec runner.NodeInvocation)
}

func (h hookRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	if h.before != nil {
		h.before(spec)
	}
	return h.FakeRunner.Run(ctx, spec)
}

const (
	remoteBaseSHA = "1111111111111111111111111111111111111111"
	remoteMainSHA = "2222222222222222222222222222222222222222"
	remoteRunSHA  = "3333333333333333333333333333333333333333"
	remoteHumaSHA = "4444444444444444444444444444444444444444"
)

// originObserver is a fake with one remote, origin, advertising main.
func originObserver() *worktree.FakeRemoteObserver {
	obs := worktree.NewFakeRemoteObserver()
	obs.SetRemotes([]worktree.Remote{{Name: "origin", FetchURL: "https://example.com/o.git", PushURL: "https://example.com/o.git"}}, nil)
	obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}}, nil)
	return obs
}

// runOneNodeGraph runs a one-node graph through `run`'s real argv path and
// returns its stdout and error.
func runOneNodeGraph(t *testing.T, nodeRunner runner.NodeRunner) (string, error) {
	t.Helper()
	path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
	})
	return out, err
}

func passingWork() *runner.FakeRunner {
	return runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": {Result: "PASS", ExitCode: 0, TotalCostUSD: 0.01}})
}

func remoteRunSnapshot(t *testing.T, runID string) runstate.Snapshot {
	t.Helper()
	_, snap := loadRunSnapshot(t, runID)
	return snap
}

// lastLegChanges is the last leg's diff, failing the test when it has none.
func lastLegChanges(t *testing.T, snap runstate.Snapshot) runstate.RemoteDiff {
	t.Helper()
	if snap.RemoteChanges == nil || len(snap.RemoteChanges.Legs) == 0 {
		t.Fatalf("state.json has no remote_changes leg: %+v", snap.RemoteChanges)
	}
	leg := snap.RemoteChanges.Legs[len(snap.RemoteChanges.Legs)-1]
	if leg.After == nil || leg.Changes == nil {
		t.Fatalf("the last leg recorded no after snapshot: %+v", leg)
	}
	return *leg.Changes
}

func TestRunGraph_ABranchPushedDuringTheLegIsRecordedAndPrintedWithItsAttribution(t *testing.T) {
	isolateRunHome(t)
	obs := originObserver()
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) {
		obs.SetHeads("origin", []worktree.RemoteHead{
			{Branch: "main", SHA: remoteMainSHA},
			{Branch: "feat/x", SHA: remoteRunSHA},
			{Branch: "human", SHA: remoteHumaSHA},
		}, nil)
	}}

	out, err := runOneNodeGraph(t, nodeRunner)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"Remote changes (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):",
		"  branch_created origin/feat/x at 333333333333 — observed during the run; points at a commit this run made\n",
		"  branch_created origin/human at 444444444444 — observed, not attributable\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "made by") {
		t.Errorf("the record must never say an entry was made by the run:\n%s", out)
	}
	if ledger, record := strings.Index(out, "TOTAL"), strings.Index(out, "Remote changes"); ledger < 0 || record < ledger {
		t.Errorf("the record must print after the ledger table:\n%s", out)
	}

	snap := remoteRunSnapshot(t, soleRunID(t))
	if snap.RunBaseSHA != remoteBaseSHA {
		t.Errorf("run_base_sha = %q, want %q", snap.RunBaseSHA, remoteBaseSHA)
	}
	got := lastLegChanges(t, snap).Entries
	want := []runstate.RemoteChangeEntry{
		{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/x", NewSHA: remoteRunSHA, Attribution: runstate.AttributionRunCommit},
		{Kind: runstate.BranchCreated, Remote: "origin", Branch: "human", NewSHA: remoteHumaSHA, Attribution: runstate.AttributionNotAttributable},
	}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("entries = %+v\nwant %+v", got, want)
	}
}

func TestRunGraph_ARemoteAddedDuringTheLegIsRemoteAdded(t *testing.T) {
	isolateRunHome(t)
	obs := originObserver()
	useRemoteObserver(t, obs, remoteBaseSHA)
	nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) {
		obs.SetRemotes([]worktree.Remote{
			{Name: "origin", FetchURL: "https://example.com/o.git", PushURL: "https://example.com/o.git"},
			{Name: "mirror", FetchURL: "https://example.com/m.git", PushURL: "https://example.com/m.git"},
		}, nil)
	}}

	out, err := runOneNodeGraph(t, nodeRunner)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	for _, want := range []string{
		"  remote_added mirror https://example.com/m.git — observed, not attributable\n",
		"  not compared: branches of mirror: remote added: it has no before refs to compare\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	got := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries
	if len(got) != 1 || got[0].Kind != runstate.RemoteAdded || got[0].Remote != "mirror" || got[0].Attribution != runstate.AttributionNotAttributable {
		t.Errorf("entries = %+v, want one not_attributable remote_added for mirror", got)
	}
}

func TestRunGraph_NothingChangedPrintsTheNoRemoteChangesLine(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, originObserver(), remoteBaseSHA)

	out, err := runOneNodeGraph(t, passingWork())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(out, "TOTAL COST: $0.0100\n\nRemote changes: none observed (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO))\n") {
		t.Errorf("stdout lacks the no-remote-changes line:\n%s", out)
	}
	d := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
	if len(d.Entries) != 0 || len(d.NotCompared) != 0 || len(d.Observed) != 3 {
		t.Errorf("changes = %+v, want all three parts observed and nothing else", d)
	}
}

func TestRunAuto_RecordsAndPrintsTheRemoteChanges(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "package.json")
	obs := originObserver()
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	nodeRunner := hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) {
		if strings.HasPrefix(spec.Prompt, "work") {
			obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}, {Branch: "feat/x", SHA: remoteRunSHA}}, nil)
		}
	}}

	var err error
	out := captureStdout(t, func() {
		err = runAutoWith([]string{"add a README section", "--accept-no-build-evidence",
			"--no-agent-mapping", "--no-skill-activation"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("auto: %v", err)
	}
	want := "  branch_created origin/feat/x at 333333333333 — observed during the run; points at a commit this run made\n"
	if !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
	got := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries
	if len(got) != 1 || got[0].Kind != runstate.BranchCreated || got[0].Attribution != runstate.AttributionRunCommit {
		t.Errorf("entries = %+v, want one run_commit branch_created", got)
	}
}

// remoteRepo makes a git repository whose origin is a local bare repository
// with main pushed to it, chdirs into it, and returns its path.
func remoteRepo(t *testing.T) string {
	t.Helper()
	bare := filepath.Join(t.TempDir(), "origin.git")
	mustGit(t, filepath.Dir(bare), "init", "-q", "--bare", "-b", "main", bare)
	dir, _ := launchRepo(t)
	mustGit(t, dir, "remote", "add", "origin", bare)
	mustGit(t, dir, "push", "-q", "origin", "main")
	t.Chdir(dir)
	return dir
}

// A remote whose URL cannot be read makes the branches part unavailable and
// changes nothing else: the same graph, with the same node outcome, returns
// the same error and exit code with and without it. The production wiring
// is used — the real GitManager against a temp repository — so this also
// proves the leg reads the invocation repository.
func TestRunGraph_AnUnreadableRemoteNeverChangesTheOutcome(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome runner.NodeOutcome
	}{
		{"node passes", runner.NodeOutcome{Result: "PASS", ExitCode: 0}},
		{"node fails", runner.NodeOutcome{Result: "boom", ExitCode: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(t *testing.T, broken bool) (string, error, runstate.RemoteDiff) {
				isolateRunHome(t)
				dir := remoteRepo(t)
				if broken {
					mustGit(t, dir, "remote", "add", "broken", filepath.Join(t.TempDir(), "no-such-repo.git"))
				}
				prevObs, prevBase := remoteObserverFor, runBaseCommit
				t.Cleanup(func() { remoteObserverFor, runBaseCommit = prevObs, prevBase })
				remoteObserverFor, runBaseCommit = productionRemoteObserverFor, launchCommitOfWorkingDir

				out, err := runOneNodeGraph(t, runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": tc.outcome}))
				return out, err, lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
			}

			var quietErr, brokenErr error
			var quietOut, brokenOut string
			var quiet, broken runstate.RemoteDiff
			t.Run("without", func(t *testing.T) { quietOut, quietErr, quiet = run(t, false) })
			t.Run("with", func(t *testing.T) { brokenOut, brokenErr, broken = run(t, true) })

			if (quietErr == nil) != (brokenErr == nil) || (quietErr != nil && quietErr.Error() != brokenErr.Error()) {
				t.Errorf("error with an unreadable remote = %v, without = %v", brokenErr, quietErr)
			}
			if a, b := exitCodeForError(quietErr), exitCodeForError(brokenErr); a != b {
				t.Errorf("exit code with an unreadable remote = %d, without = %d", b, a)
			}
			if !strings.Contains(quietOut, "Remote changes: none observed (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO))\n") {
				t.Errorf("the readable run lacks the no-remote-changes line:\n%s", quietOut)
			}
			if len(quiet.NotCompared) != 0 {
				t.Errorf("the readable run compared nothing it should have: %+v", quiet.NotCompared)
			}
			if len(broken.NotCompared) != 1 || broken.NotCompared[0].Part != runstate.RemotePartBranches ||
				!strings.Contains(broken.NotCompared[0].Reason, "ls-remote broken") {
				t.Errorf("not_compared = %+v, want the branches part, naming the broken remote", broken.NotCompared)
			}
			if want := []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartPullRequests}; !slices.Equal(broken.Observed, want) {
				t.Errorf("observed = %v, want %v", broken.Observed, want)
			}
			for _, want := range []string{
				"Remote changes: none observed in the parts compared (observed: remotes, pull_requests of github.com/o/r (GH_REPO))\n",
				"  not compared: branches: unavailable before (ls-remote broken: ",
			} {
				if !strings.Contains(brokenOut, want) {
					t.Errorf("stdout lacks %q:\n%s", want, brokenOut)
				}
			}
		})
	}
}

// A resumed leg takes its own before snapshot: a branch created while the
// run was paused is recorded between the legs and never attributed, even at
// a run commit, and the resumed leg's own entries are diffed against its own
// before. The base commit the first leg read is carried, not re-read.
func TestResume_RecordsAndPrintsTheRemoteChanges(t *testing.T) {
	isolateRunHome(t)
	obs := originObserver()
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":    {Result: "PASS", ExitCode: 0},
		"ship": {Result: "PASS", ExitCode: 0},
	})
	nodeRunner := hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) {
		if spec.Prompt == "ship" {
			obs.SetHeads("origin", []worktree.RemoteHead{
				{Branch: "main", SHA: remoteMainSHA}, {Branch: "paused", SHA: remoteRunSHA}, {Branch: "shipped", SHA: remoteRunSHA},
			}, nil)
		}
	}}
	g := mustParse(t, `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
	var err error
	firstOut := captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, nodeRunner, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the first leg to pause, got %v", err)
	}
	if !strings.Contains(firstOut, "Remote changes: none observed (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO))\n") {
		t.Errorf("the first leg lacks its no-remote-changes line:\n%s", firstOut)
	}

	// Pushed while no leg ran, at a commit the check would pass.
	obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}, {Branch: "paused", SHA: remoteRunSHA}}, nil)
	runBaseCommit = func() string { t.Error("a resumed leg re-read the run's base commit"); return "" }

	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), nodeRunner, nil)
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	for _, want := range []string{
		"Remote changes between leg 1 and leg 2, while no leg ran (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):\n" +
			"  branch_created origin/paused at 333333333333 — observed, not attributable\n",
		"Remote changes (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):\n" +
			"  branch_created origin/shipped at 333333333333 — observed during the run; points at a commit this run made\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}

	snap := remoteRunSnapshot(t, "run-1")
	if snap.RunBaseSHA != remoteBaseSHA {
		t.Errorf("run_base_sha = %q after resume, want the first leg's %q", snap.RunBaseSHA, remoteBaseSHA)
	}
	rc := snap.RemoteChanges
	if rc == nil || len(rc.Legs) != 2 || len(rc.BetweenLegs) != 1 {
		t.Fatalf("remote_changes = %+v, want two legs and one between-legs set", rc)
	}
	if between := rc.BetweenLegs[0]; between.FromLeg != 1 || between.ToLeg != 2 || len(between.Changes.Entries) != 1 ||
		between.Changes.Entries[0].Branch != "paused" || between.Changes.Entries[0].Attribution != runstate.AttributionNotAttributable {
		t.Errorf("between_legs = %+v, want leg 1→2's not_attributable branch_created paused", between)
	}
	if first := rc.Legs[0].Changes; first == nil || len(first.Entries) != 0 {
		t.Errorf("leg 1's changes = %+v, want none", first)
	}
	if second := lastLegChanges(t, snap).Entries; len(second) != 1 || second[0].Branch != "shipped" ||
		second[0].Attribution != runstate.AttributionRunCommit {
		t.Errorf("leg 2's entries = %+v, want only the run_commit branch_created shipped", second)
	}
}

// interruptingObserver is the user's Ctrl-C landing on the before snapshot.
// during: Remotes interrupts the run and answers with its error — an
// unreachable remote's git killed by the interrupt. Otherwise the last
// ls-remote answers in full and interrupts as it returns, so the snapshot
// comes back observed with the run already interrupted. Either way it waits
// for the run context to be cancelled before answering. It counts Remotes,
// one call per snapshot taken.
type interruptingObserver struct {
	*worktree.FakeRemoteObserver
	interrupt    func()
	during       bool
	remotesCalls int
}

func (o *interruptingObserver) Remotes(ctx context.Context) ([]worktree.Remote, error) {
	o.remotesCalls++
	if o.during {
		if err := o.interruptAndWait(ctx); err != nil {
			return nil, err
		}
		return nil, ctx.Err()
	}
	return o.FakeRemoteObserver.Remotes(ctx)
}

func (o *interruptingObserver) LsRemoteHeads(ctx context.Context, remote string) ([]worktree.RemoteHead, error) {
	heads, err := o.FakeRemoteObserver.LsRemoteHeads(ctx, remote)
	if waitErr := o.interruptAndWait(ctx); waitErr != nil {
		return nil, waitErr
	}
	return heads, err
}

func (o *interruptingObserver) interruptAndWait(ctx context.Context) error {
	o.interrupt()
	select {
	case <-ctx.Done():
		return nil
	case <-time.After(10 * time.Second):
		return errors.New("interruptingObserver: the interrupt never cancelled the run context")
	}
}

// sigint sends this process SIGINT, which the leg's signal.NotifyContext
// catches exactly as a Ctrl-C at the terminal: a resumed leg makes its own
// context, so a test cannot hand it a cancellable one.
func sigint(t *testing.T) func() {
	return func() {
		if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
			t.Errorf("SIGINT: %v", err)
		}
	}
}

// QA at bfee490: a Ctrl-C during the before snapshot ran the graph anyway and
// then waited out the after snapshot. An interrupt there, or one that lands
// as the snapshot returns, ends the leg before any node starts — the generic
// exit 1 on context.Canceled an interrupt before the first node takes — and
// the after snapshot is not taken but recorded unavailable, "interrupted".
func TestRunGraph_AnInterruptDuringTheRemoteSnapshotStartsNoNode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during bool
	}{
		{"cancelled while the before snapshot runs", true},
		{"cancelled as the before snapshot returns", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			obs := &interruptingObserver{FakeRemoteObserver: originObserver(), interrupt: cancel, during: tc.during}
			useRemoteObserver(t, obs, remoteBaseSHA)
			fake := passingWork()
			g := mustParse(t, `{"name":"interrupted","nodes":[
				{"id":"work","prompt":"work"},
				{"id":"approve","type":"gate","depends_on":["work"]}]}`)

			var err error
			out := captureStdout(t, func() {
				err = executeGraph(ctx, "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "interrupted.yaml", []byte("name: interrupted\n"), false, nil, nil, nil)
			})
			if !errors.Is(err, context.Canceled) || err.Error() != "context canceled" {
				t.Fatalf("err = %v, want the bare context.Canceled an interrupt before any node returns", err)
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if calls := fake.Calls(); len(calls) != 0 {
				t.Errorf("nodes ran after the interrupt: %v", calls)
			}
			if strings.Contains(out, "paused") {
				t.Errorf("an interrupted leg reached the gate:\n%s", out)
			}
			if obs.remotesCalls != 1 {
				t.Errorf("Remotes ran %d times, want once: the after snapshot must not be taken", obs.remotesCalls)
			}
			assertInterruptedRecordPrinted(t, out)

			rc := remoteRunSnapshot(t, "run-1").RemoteChanges
			if rc == nil || len(rc.Legs) != 1 || rc.Legs[0].After == nil {
				t.Fatalf("remote_changes = %+v, want one leg with an after", rc)
			}
			after := *rc.Legs[0].After
			if after.Remotes.Status != runstate.RemotePartUnavailable || after.Remotes.Reason != "interrupted" ||
				after.Branches.Status != runstate.RemotePartUnavailable || after.Branches.Reason != "interrupted" ||
				after.PullRequests.Status != runstate.RemotePartUnavailable || after.PullRequests.Reason != "interrupted" {
				t.Errorf("after = %+v, want all three parts unavailable, reason interrupted", after)
			}
			d := lastLegChanges(t, remoteRunSnapshot(t, "run-1"))
			if len(d.Entries) != 0 || len(d.Observed) != 0 || len(d.NotCompared) != 3 {
				t.Errorf("changes = %+v, want all three parts not compared, never none observed", d)
			}
		})
	}
}

// assertInterruptedRecordPrinted checks that a leg ended by an interrupt
// during its before snapshot printed its record, as the normal path does
// after the ledger: no part compared, and each part's line saying why.
func assertInterruptedRecordPrinted(t *testing.T, out string) {
	t.Helper()
	if !strings.Contains(out, "\nRemote changes: none observed in the parts compared (observed: none)\n") {
		t.Errorf("the early exit did not print its remote record:\n%s", out)
	}
	for _, part := range []string{"remotes", "branches", "pull_requests"} {
		if !regexp.MustCompile(`(?m)^  not compared: ` + part + `: .*after(: | \()interrupted\)?$`).MatchString(out) {
			t.Errorf("the early exit printed no not-compared line for %s, reason interrupted:\n%s", part, out)
		}
	}
}

// The same holds on a resumed leg: an interrupt during its own before
// snapshot, or as it returns, starts no node, takes no after snapshot and
// records the new leg's after unavailable, "interrupted", leaving the earlier
// legs' records as they were.
func TestResume_AnInterruptDuringTheRemoteSnapshotStartsNoNode(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during bool
	}{
		{"cancelled while the before snapshot runs", true},
		{"cancelled as the before snapshot returns", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a":    {Result: "PASS", ExitCode: 0},
				"ship": {Result: "PASS", ExitCode: 0},
			})
			g := mustParse(t, `{"name":"gate-flow","nodes":[
				{"id":"a","prompt":"a"},
				{"id":"approve","type":"gate","depends_on":["a"]},
				{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
			var err error
			captureStdout(t, func() {
				err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
			})
			var paused *schedule.PausedError
			if !errors.As(err, &paused) {
				t.Fatalf("expected the first leg to pause, got %v", err)
			}
			firstLegs := remoteRunSnapshot(t, "run-1").RemoteChanges
			if firstLegs == nil || len(firstLegs.Legs) != 1 {
				t.Fatalf("remote_changes after the first leg = %+v, want one leg", firstLegs)
			}
			firstCalls := len(fake.Calls())

			obs := &interruptingObserver{FakeRemoteObserver: originObserver(), interrupt: sigint(t), during: tc.during}
			useRemoteObserver(t, obs, remoteBaseSHA)
			out := captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
			})
			if !errors.Is(err, context.Canceled) || err.Error() != "context canceled" {
				t.Fatalf("err = %v, want the bare context.Canceled an interrupt before any node returns", err)
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if calls := fake.Calls(); len(calls) != firstCalls {
				t.Errorf("nodes ran after the interrupt: %v", calls[firstCalls:])
			}
			if obs.remotesCalls != 1 {
				t.Errorf("Remotes ran %d times on the resumed leg, want once: the after snapshot must not be taken", obs.remotesCalls)
			}
			assertInterruptedRecordPrinted(t, out)

			rc := remoteRunSnapshot(t, "run-1").RemoteChanges
			if rc == nil || len(rc.Legs) != 2 || rc.Legs[1].After == nil {
				t.Fatalf("remote_changes = %+v, want two legs, the resumed one with an after", rc)
			}
			if !reflect.DeepEqual(rc.Legs[0], firstLegs.Legs[0]) {
				t.Errorf("leg 1 = %+v after the interrupted resume, want it unchanged: %+v", rc.Legs[0], firstLegs.Legs[0])
			}
			after := *rc.Legs[1].After
			if after.Remotes.Status != runstate.RemotePartUnavailable || after.Remotes.Reason != "interrupted" ||
				after.Branches.Status != runstate.RemotePartUnavailable || after.Branches.Reason != "interrupted" ||
				after.PullRequests.Status != runstate.RemotePartUnavailable || after.PullRequests.Reason != "interrupted" {
				t.Errorf("leg 2's after = %+v, want all three parts unavailable, reason interrupted", after)
			}
			d := lastLegChanges(t, remoteRunSnapshot(t, "run-1"))
			if len(d.Entries) != 0 || len(d.Observed) != 0 || len(d.NotCompared) != 3 {
				t.Errorf("leg 2's changes = %+v, want all three parts not compared, never none observed", d)
			}
		})
	}
}

// Review of #410: a resume --approve interrupted during its before snapshot,
// or as it returns, wrote the approval and cleared the pause first, so the
// run read not paused and the same command was refused. The run must stay
// paused at the gate — in state.json and as runs list and show derive it —
// and the same resume --approve must then pass the gate.
func TestResume_AnInterruptDuringTheRemoteSnapshotLeavesTheRunPausedAtTheGate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during bool
	}{
		{"cancelled while the before snapshot runs", true},
		{"cancelled as the before snapshot returns", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a":    {Result: "PASS", ExitCode: 0},
				"ship": {Result: "PASS", ExitCode: 0},
			})
			g := mustParse(t, `{"name":"gate-flow","nodes":[
				{"id":"a","prompt":"a"},
				{"id":"approve","type":"gate","depends_on":["a"]},
				{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
			var err error
			captureStdout(t, func() {
				err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
			})
			var paused *schedule.PausedError
			if !errors.As(err, &paused) {
				t.Fatalf("expected the first leg to pause, got %v", err)
			}
			pausedGate := remoteRunSnapshot(t, "run-1").Gate

			useRemoteObserver(t, &interruptingObserver{FakeRemoteObserver: originObserver(), interrupt: sigint(t), during: tc.during}, remoteBaseSHA)
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
			})
			if code := exitCodeForError(err); !errors.Is(err, context.Canceled) || code != 1 {
				t.Fatalf("err = %v (exit %d), want context.Canceled, exit 1", err, code)
			}
			if gate := remoteRunSnapshot(t, "run-1").Gate; gate.PausedAt != "approve" || !reflect.DeepEqual(gate, pausedGate) {
				t.Errorf("gate = %+v after the interrupted resume, want the pause as it was: %+v", gate, pausedGate)
			}
			status, err := runstatus.Of(runDirFor("run-1"))
			if err != nil {
				t.Fatalf("runstatus.Of: %v", err)
			}
			if status != runstatus.Paused {
				t.Errorf("status = %v after the interrupted resume, want %v", status, runstatus.Paused)
			}
			var list, warn strings.Builder
			if err := listRuns(&list, &warn, runsRoot(), true); err != nil {
				t.Fatalf("listRuns: %v", err)
			}
			if row := lineContaining(t, list.String(), "run-1"); !strings.Contains(row, "PAUSED") {
				t.Errorf("runs list row = %q, want PAUSED", row)
			}

			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
			})
			if err != nil {
				t.Fatalf("the same resume --approve after the interrupt: %v", err)
			}
			if !slices.Contains(fake.Calls(), "ship") {
				t.Errorf("calls = %v, want ship run past the approved gate", fake.Calls())
			}
			snap := remoteRunSnapshot(t, "run-1")
			if snap.Gate.PausedAt != "" || snap.Gate.Decisions["approve"] != runstate.GateApprove {
				t.Errorf("gate = %+v, want approve approved and no pause", snap.Gate)
			}
			if snap.Nodes["ship"].Verdict != runstate.VerdictPass {
				t.Errorf("ship = %+v, want PASS", snap.Nodes["ship"])
			}
		})
	}
}

// readOnlyRunDirObserver makes the run directory read-only once the before
// snapshot's ls-remote has answered, so every later state.json write of the
// leg fails — RecordLegGate's first among them. It counts Remotes, one call
// per snapshot taken.
type readOnlyRunDirObserver struct {
	*worktree.FakeRemoteObserver
	t            *testing.T
	runDir       string
	remotesCalls int
}

func (o *readOnlyRunDirObserver) Remotes(ctx context.Context) ([]worktree.Remote, error) {
	o.remotesCalls++
	return o.FakeRemoteObserver.Remotes(ctx)
}

func (o *readOnlyRunDirObserver) LsRemoteHeads(ctx context.Context, remote string) ([]worktree.RemoteHead, error) {
	if o.remotesCalls == 1 {
		if err := os.Chmod(o.runDir, 0o500); err != nil {
			o.t.Errorf("chmod run dir: %v", err)
		}
	}
	return o.FakeRemoteObserver.LsRemoteHeads(ctx, remote)
}

// failedPauseWrite runs a fresh gate-only run whose run directory turns
// read-only after its before snapshot, so the scheduler's pause write — a
// sibling state.json write a leg cannot run without — fails, and returns the
// error that run ends with. Gate-only, so no node's output write fails first.
func failedPauseWrite(t *testing.T, fake *runner.FakeRunner) error {
	t.Helper()
	g := mustParse(t, `{"name":"gate-only","nodes":[{"id":"approve","type":"gate"}]}`)
	runDir := runDirFor("run-pause-write")
	t.Cleanup(func() { _ = os.Chmod(runDir, 0o700) })
	useRemoteObserver(t, &readOnlyRunDirObserver{FakeRemoteObserver: originObserver(), t: t, runDir: runDir}, remoteBaseSHA)
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-pause-write", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-only.yaml", []byte("name: gate-only\n"), false, nil, nil, nil)
	})
	if err == nil || !strings.Contains(err.Error(), `persist pause snapshot for gate "approve"`) {
		t.Fatalf("the read-only run's pause write: got %v, want it to fail", err)
	}
	return err
}

// Review of #410: a resume --approve whose gate decisions could not be
// written after its before snapshot warned and ran the graph anyway, so a
// later record could persist the run as no longer paused while the approval
// itself was never on disk. The leg now ends there as a failure: no node
// starts, exit 1 with the failed write and the gate named, its record
// printed, and state.json still paused at the gate — so the same command,
// retried, passes the gate.
func TestResume_AFailedGateDecisionWriteStartsNoNodeAndLeavesTheRunPaused(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores the read-only run directory this test relies on")
	}
	isolateRunHome(t)
	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":    {Result: "PASS", ExitCode: 0},
		"ship": {Result: "PASS", ExitCode: 0},
	})
	g := mustParse(t, `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the first leg to pause, got %v", err)
	}
	pausedGate := remoteRunSnapshot(t, "run-1").Gate
	firstCalls := len(fake.Calls())

	runDir := runDirFor("run-1")
	t.Cleanup(func() { _ = os.Chmod(runDir, 0o700) })
	obs := &readOnlyRunDirObserver{FakeRemoteObserver: originObserver(), t: t, runDir: runDir}
	useRemoteObserver(t, obs, remoteBaseSHA)
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	})
	if err == nil {
		t.Fatal("resume returned nil after its gate decisions could not be written")
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1, the code of every other state.json write a leg cannot run without", code)
	}
	if code, sibling := exitCodeForError(err), exitCodeForError(failedPauseWrite(t, fake)); code != sibling {
		t.Errorf("exit code = %d, want %d, the code of the scheduler's failed pause write", code, sibling)
	}
	// What mainExitCode prints for an exit-1 error, the one stderr line the
	// user sees; mainExitCode itself is not driven, since resume through run()
	// would build the real runtime.
	stderr := "oh-my-graph: " + err.Error()
	for _, want := range []string{"oh-my-graph: record this leg's gate decisions in state.json: ", `still paused at gate "approve"`, "the same resume command can be retried"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("stderr = %q, want it to say %q", stderr, want)
		}
	}
	if calls := fake.Calls(); len(calls) != firstCalls {
		t.Errorf("nodes ran after the failed write: %v", calls[firstCalls:])
	}
	if obs.remotesCalls != 2 {
		t.Errorf("Remotes ran %d times on the resumed leg, want twice: the leg's after snapshot is taken", obs.remotesCalls)
	}
	if !strings.Contains(out, "\nRemote changes") {
		t.Errorf("the failed leg did not print its remote record:\n%s", out)
	}
	if strings.Contains(out, "Paused at gate") {
		t.Errorf("the failed leg printed a pause hint:\n%s", out)
	}

	if err := os.Chmod(runDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if gate := remoteRunSnapshot(t, "run-1").Gate; !reflect.DeepEqual(gate, pausedGate) {
		t.Errorf("gate = %+v after the failed write, want the pause as it was: %+v", gate, pausedGate)
	}
	status, err := runstatus.Of(runDir)
	if err != nil {
		t.Fatalf("runstatus.Of: %v", err)
	}
	if status != runstatus.Paused {
		t.Errorf("status = %v after the failed write, want %v", status, runstatus.Paused)
	}

	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("the same resume --approve after the failed write: %v", err)
	}
	if !slices.Contains(fake.Calls(), "ship") {
		t.Errorf("calls = %v, want ship run past the approved gate", fake.Calls())
	}
}

// Review of #410: a resume --retry-failed seeded its recorder with the
// retained records only, so a Ctrl-C during its before snapshot persisted
// state.json with the failed node's record and cost dropped, and the same
// command then found no failed node to retry. state.json must keep every
// record exactly as loaded, and the same resume --retry-failed must then
// re-run the failed node.
func TestResume_RetryFailedInterruptedDuringTheRemoteSnapshotKeepsTheFailedRecords(t *testing.T) {
	for _, tc := range []struct {
		name   string
		during bool
	}{
		{"cancelled while the before snapshot runs", true},
		{"cancelled as the before snapshot returns", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a": {Result: "PASS", ExitCode: 0, TotalCostUSD: 0.25},
				"b": {Result: "FAIL", ExitCode: 1, TotalCostUSD: 0.5},
			})
			// The retry quotes b's failed reply after its own prompt (ADR 0020).
			fake.KeyFn = func(spec runner.NodeInvocation) string { return nodePromptOf(spec.Prompt) }
			g := mustParse(t, `{"name":"retry-flow","nodes":[
				{"id":"a","prompt":"a"},
				{"id":"b","prompt":"b","depends_on":["a"]}]}`)
			var err error
			captureStdout(t, func() {
				err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "retry-flow.yaml", []byte("name: retry-flow\n"), false, nil, nil, nil)
			})
			if err == nil {
				t.Fatal("expected the first leg to fail at b")
			}
			loaded := remoteRunSnapshot(t, "run-1").Nodes
			if loaded["b"].Verdict != runstate.VerdictFail || loaded["b"].CostUSD == 0 || loaded["a"].CostUSD == 0 {
				t.Fatalf("first leg recorded %+v, want a PASS and b FAIL, each with a cost", loaded)
			}
			firstCalls := len(fake.Calls())

			useRemoteObserver(t, &interruptingObserver{FakeRemoteObserver: originObserver(), interrupt: sigint(t), during: tc.during}, remoteBaseSHA)
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
			})
			if code := exitCodeForError(err); !errors.Is(err, context.Canceled) || code != 1 {
				t.Fatalf("err = %v (exit %d), want context.Canceled, exit 1", err, code)
			}
			if calls := fake.Calls(); len(calls) != firstCalls {
				t.Errorf("nodes ran after the interrupt: %v", calls[firstCalls:])
			}
			if nodes := remoteRunSnapshot(t, "run-1").Nodes; !reflect.DeepEqual(nodes, loaded) {
				t.Errorf("nodes = %+v after the interrupted resume, want them as loaded: %+v", nodes, loaded)
			}

			fake.SetOutcome("b", runner.NodeOutcome{Result: "PASS", ExitCode: 0, TotalCostUSD: 0.75})
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			out := captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
			})
			if err != nil {
				t.Fatalf("the same resume --retry-failed after the interrupt: %v", err)
			}
			if !strings.Contains(out, "retrying failed nodes: b") {
				t.Errorf("the same resume --retry-failed did not retry b:\n%s", out)
			}
			if calls := fake.Calls()[firstCalls:]; !reflect.DeepEqual(calls, []string{"b"}) {
				t.Errorf("calls after the interrupt = %v, want b re-run alone", calls)
			}
			if b := remoteRunSnapshot(t, "run-1").Nodes["b"]; b.Verdict != runstate.VerdictPass {
				t.Errorf("b = %+v, want PASS", b)
			}
		})
	}
}

// Review of #410: the uninterrupted retry leg still drops the cleared
// records once its before snapshot has completed — state.json holds exactly
// the retained records, their cost kept, before any node starts — so a
// retry leg that halts at another node first leaves no stale failure record
// for the cleared node it never reached. The cleared node is an exhausted
// feedback declarer, review: salvaging it clears its body, impl, too (ADR
// 0010), and the retry leg halts at impl, which review depends on.
func TestResume_RetryFailedWritesTheRetainedRecordsOnceTheRemoteSnapshotCompletes(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, originObserver(), remoteBaseSHA)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":                {Result: "PASS", ExitCode: 0, TotalCostUSD: 0.125},
		"impl: ":           {Result: "draft-v1", ExitCode: 0, TotalCostUSD: 0.25},
		"review: draft-v1": {Result: "needs work", ExitCode: 0, TotalCostUSD: 0.25},
		"impl: needs work": {Result: "draft-v2", ExitCode: 0, TotalCostUSD: 0.25},
		"review: draft-v2": {Result: "still bad", ExitCode: 0, TotalCostUSD: 0.25},
	})
	fake.KeyFn = func(spec runner.NodeInvocation) string { return nodePromptOf(spec.Prompt) }
	g := mustParse(t, `{"name":"loop","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"impl","prompt":"impl: {{ feedback.review }}","depends_on":["a"]},
		{"id":"review","prompt":"review: {{ artifacts.impl | inline }}","depends_on":["impl"],
		 "success_check":{"result_matches":"ship it"},
		 "feedback":{"rerun":"impl","max":1}}]}`)
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "loop.yaml", []byte("name: loop\n"), false, nil, nil, nil)
	})
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected the first leg to halt on the exhausted loop, got %v", err)
	}
	loaded := remoteRunSnapshot(t, "run-1").Nodes
	if loaded["a"].Verdict != runstate.VerdictPass || loaded["impl"].Verdict != runstate.VerdictPass || loaded["review"].Verdict != runstate.VerdictFail {
		t.Fatalf("first leg recorded %+v, want a and impl PASS, review FAIL", loaded)
	}
	firstCalls := len(fake.Calls())

	fake.SetOutcome("impl: ", runner.NodeOutcome{Result: "FAIL", ExitCode: 1})
	var atFirstNode map[string]runstate.NodeRecord
	hooked := hookRunner{FakeRunner: fake, before: func(runner.NodeInvocation) {
		if atFirstNode == nil {
			atFirstNode = remoteRunSnapshot(t, "run-1").Nodes
		}
	}}
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), hooked, nil)
	})
	if !errors.As(err, &halted) {
		t.Fatalf("expected the retry leg to halt at impl, got %v", err)
	}
	if !strings.Contains(out, "retrying failed nodes: impl, review") {
		t.Errorf("the retry leg did not clear impl and review:\n%s", out)
	}
	if calls := fake.Calls()[firstCalls:]; !reflect.DeepEqual(calls, []string{"impl: "}) {
		t.Fatalf("retry leg calls = %v, want it to halt at impl before reaching review", calls)
	}
	retained := map[string]runstate.NodeRecord{"a": loaded["a"]}
	if !reflect.DeepEqual(atFirstNode, retained) {
		t.Errorf("state.json nodes when the retry leg's first node started = %+v, want the retained records %+v", atFirstNode, retained)
	}
	nodes := remoteRunSnapshot(t, "run-1").Nodes
	if review, stale := nodes["review"]; stale {
		t.Errorf("state.json still holds review's cleared record %+v, a node the retry leg never reached", review)
	}
	if !reflect.DeepEqual(nodes["a"], loaded["a"]) || nodes["impl"].Verdict != runstate.VerdictFail {
		t.Errorf("nodes = %+v, want a as loaded and impl FAIL", nodes)
	}
}

// afterSnapshotInterrupter is the user's Ctrl-C landing on the after
// snapshot of a leg whose nodes have finished. Its first Remotes (the before
// snapshot) answers in full; its second (the after snapshot) interrupts the
// run and then blocks like an unreachable remote until its own ctx is
// cancelled — or, if the interrupt never reaches it, answers in full after
// a delay longer than the test allows.
type afterSnapshotInterrupter struct {
	*worktree.FakeRemoteObserver
	interrupt    func()
	remotesCalls int
}

func (o *afterSnapshotInterrupter) Remotes(ctx context.Context) ([]worktree.Remote, error) {
	o.remotesCalls++
	if o.remotesCalls < 2 {
		return o.FakeRemoteObserver.Remotes(ctx)
	}
	o.interrupt()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(30 * time.Second):
		return o.FakeRemoteObserver.Remotes(ctx)
	}
}

// QA at 2198b74: a Ctrl-C while the after snapshot ran was ignored — the
// snapshot waited out the git timeout and the leg ended paused at its gate
// (exit 2). The interrupt now reaches the snapshot's git: the leg ends
// promptly, as interrupted, with both after parts recorded unavailable,
// "interrupted" — and everything says so alike: exit 1, an ordinary Ctrl-C's;
// no gate pause in state.json; run_finished "failed", so the run reads FAIL;
// and no pause hint printed. Like any interrupted leg it resumes with
// --retry-failed, its gate pausing again when reached.
func TestRunGraph_AnInterruptDuringTheAfterSnapshotEndsTheLegInterrupted(t *testing.T) {
	gateGraph := `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]},
		{"id":"release","type":"gate","depends_on":["ship"]}]}`
	for _, tc := range []struct {
		name string
		// gate is the gate the leg's nodes stopped at before the interrupt.
		gate string
		leg  func(t *testing.T, fake *runner.FakeRunner, obs *afterSnapshotInterrupter) (string, error)
	}{
		{"a first leg", "approve", func(t *testing.T, fake *runner.FakeRunner, obs *afterSnapshotInterrupter) (string, error) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			obs.interrupt = cancel
			useRemoteObserver(t, obs, remoteBaseSHA)
			var err error
			out := captureStdout(t, func() {
				err = executeGraph(ctx, "run-1", mustParse(t, gateGraph), fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
			})
			return out, err
		}},
		{"a resumed leg", "release", func(t *testing.T, fake *runner.FakeRunner, obs *afterSnapshotInterrupter) (string, error) {
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			var err error
			captureStdout(t, func() {
				err = executeGraph(context.Background(), "run-1", mustParse(t, gateGraph), fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
			})
			var paused *schedule.PausedError
			if !errors.As(err, &paused) {
				t.Fatalf("expected the first leg to pause, got %v", err)
			}
			// The resumed leg reaches the next gate; the interrupt lands on its
			// after snapshot.
			obs.interrupt = sigint(t)
			useRemoteObserver(t, obs, remoteBaseSHA)
			out := captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
			})
			return out, err
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a":    {Result: "PASS", ExitCode: 0},
				"ship": {Result: "PASS", ExitCode: 0},
			})
			obs := &afterSnapshotInterrupter{FakeRemoteObserver: originObserver()}

			start := time.Now()
			out, err := tc.leg(t, fake, obs)
			if elapsed := time.Since(start); elapsed > 5*time.Second {
				t.Errorf("the leg took %s to end after the interrupt, want well under the git timeout", elapsed)
			}
			if obs.remotesCalls != 2 {
				t.Fatalf("Remotes ran %d times on the leg, want twice: the interrupt must land on the after snapshot", obs.remotesCalls)
			}
			if !errors.Is(err, context.Canceled) || err.Error() != "context canceled" {
				t.Fatalf("err = %v, want the bare context.Canceled of an interrupted leg", err)
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d, want 1, an ordinary Ctrl-C's", code)
			}
			snap := remoteRunSnapshot(t, "run-1")
			if snap.Gate.PausedAt != "" || snap.Gate.Decisions[tc.gate] == runstate.GatePause {
				t.Errorf("state.json gate = %+v, want no pause at %q: the leg ended interrupted", snap.Gate, tc.gate)
			}
			leg, legErr := runfeed.LastLeg(filepath.Join(runDirFor("run-1"), runfeed.FileName))
			if legErr != nil {
				t.Fatalf("runfeed.LastLeg: %v", legErr)
			}
			if leg.Open || leg.LastOutcome != runfeed.OutcomeFailed {
				t.Errorf("run feed leg = %+v, want closed with run_finished %q", leg, runfeed.OutcomeFailed)
			}
			if status, statusErr := runstatus.Of(runDirFor("run-1")); statusErr != nil || status != runstatus.Fail {
				t.Errorf("status = %v (%v), want %v, as the exit says", status, statusErr, runstatus.Fail)
			}
			if strings.Contains(out, "Paused at gate") || strings.Contains(out, "--approve "+tc.gate) {
				t.Errorf("an interrupted leg printed a pause hint:\n%s", out)
			}

			rc := snap.RemoteChanges
			if rc == nil || len(rc.Legs) == 0 || rc.Legs[len(rc.Legs)-1].After == nil {
				t.Fatalf("remote_changes = %+v, want the leg with an after", rc)
			}
			after := *rc.Legs[len(rc.Legs)-1].After
			if !reflect.DeepEqual(after, interruptedRemoteSnapshot) {
				t.Errorf("after = %+v, want all three parts unavailable, reason interrupted", after)
			}
			d := lastLegChanges(t, snap)
			if len(d.Entries) != 0 || len(d.Observed) != 0 || len(d.NotCompared) != 3 {
				t.Errorf("changes = %+v, want all three parts not compared, never none observed", d)
			}

			// Resumable like any interrupted leg: --retry-failed runs on to
			// the same gate, which pauses again.
			useRemoteObserver(t, originObserver(), remoteBaseSHA)
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{"run-1", "--retry-failed"}), fake, nil)
			})
			var paused *schedule.PausedError
			if !errors.As(err, &paused) || paused.GateID != tc.gate {
				t.Errorf("resume --retry-failed = %v, want a pause at %q again", err, tc.gate)
			}
		})
	}
}

// The production wiring reads open pull requests through the fifth seam's
// GHRunner — never asked here, so no test runs a real gh.
func TestRemotePRListerFor_IsTheGHRunner(t *testing.T) {
	if _, ok := productionRemotePRListerFor("run-1").(*remote.GHRunner); !ok {
		t.Errorf("remotePRListerFor builds %T, want *remote.GHRunner", productionRemotePRListerFor("run-1"))
	}
}

// Each PR entry kind is printed with its number, head branch and short sha,
// under the attribution label its head sha earns (ADR 0046 §8.6), and no
// line says the run made it.
func TestRunGraph_PullRequestsChangedDuringTheLegArePrintedWithTheirAttribution(t *testing.T) {
	isolateRunHome(t)
	obs := originObserver()
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	prs := ghRepoPRLister()
	prs.Set(onGHRepo([]remote.OpenPR{
		{Number: 7, HeadRefName: "feat/seven", HeadRefOid: remoteMainSHA},
		{Number: 8, HeadRefName: "feat/eight", HeadRefOid: remoteMainSHA},
		{Number: 9, HeadRefName: "feat/nine", HeadRefOid: remoteMainSHA},
		{Number: 10, HeadRefName: "gone", HeadRefOid: remoteMainSHA},
	}))
	usePRLister(t, prs)
	nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) {
		prs.Set(onGHRepo([]remote.OpenPR{
			{Number: 7, HeadRefName: "feat/seven", HeadRefOid: remoteRunSHA},
			{Number: 8, HeadRefName: "feat/eight", HeadRefOid: remoteHumaSHA},
			{Number: 9, HeadRefName: "feat/nine", HeadRefOid: remoteMainSHA},
			{Number: 11, HeadRefName: "omg-out", HeadRefOid: remoteRunSHA},
			{Number: 12, HeadRefName: "human", HeadRefOid: remoteHumaSHA},
		}))
	}}

	out, err := runOneNodeGraph(t, nodeRunner)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if code := exitCodeForError(err); code != 0 {
		t.Errorf("exit code = %d, want 0: the record is a report", code)
	}
	for _, want := range []string{
		"Remote changes (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):\n",
		"  pr_updated #7 feat/seven 222222222222 -> 333333333333 — observed during the run; points at a commit this run made\n",
		"  pr_updated #8 feat/eight 222222222222 -> 444444444444 — observed, not attributable\n",
		"  pr_no_longer_open #10 gone (was 222222222222) — observed, not attributable\n",
		"  pr_opened #11 omg-out at 333333333333 — observed during the run; points at a commit this run made\n",
		"  pr_opened #12 human at 444444444444 — observed, not attributable\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "#9") {
		t.Errorf("an unchanged PR was printed:\n%s", out)
	}
	if strings.Contains(out, "made by") {
		t.Errorf("the record must never say an entry was made by the run:\n%s", out)
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "  pr_") && !strings.Contains(line, "observed") {
			t.Errorf("PR entry %q does not say it was observed", line)
		}
	}
	if calls := prs.Calls(); calls != 2 {
		t.Errorf("the PR list was read %d times, want the before and the after", calls)
	}
	got := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries
	if len(got) != 5 {
		t.Fatalf("entries = %+v, want five PR entries", got)
	}
}

// prListLimit copies internal/remote's unexported --limit (ADR 0046 §2.1),
// which stays unexported, so this is the literal 1000.
// internal/remote's TestPrListLimitIsTheValueOtherPackagesCopy pins that value,
// so a change to it in either direction fails there before this copy goes stale.
const prListLimit = 1000

// A PR part that cannot be read on either side prints a not_compared line
// naming why, never "none observed" for PRs, and changes neither the error
// nor the exit code of the same run with a readable list.
func TestRunGraph_AnUnreadablePRListIsNotComparedAndNeverChangesTheOutcome(t *testing.T) {
	limit := make([]remote.OpenPR, prListLimit)
	for i := range limit {
		limit[i] = remote.OpenPR{Number: i + 1, HeadRefName: "b", HeadRefOid: remoteMainSHA}
	}
	for _, tc := range []struct {
		name          string
		before, after remote.PRList
		want          string
	}{
		{"not authenticated after", remote.ObservedPRs(nil), remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""),
			"  not compared: pull_requests: unavailable after: not authenticated"},
		{"gh not found before", remote.UnavailablePRs(remote.ReasonGHNotFound, ""), remote.ObservedPRs(nil),
			"  not compared: pull_requests: unavailable before: gh not found"},
		{"truncated", remote.ObservedPRs(limit), remote.ObservedPRs(limit),
			"  not compared: pull_requests: unavailable before (truncated) and after (truncated)"},
	} {
		for _, outcome := range []runner.NodeOutcome{{Result: "PASS", ExitCode: 0}, {Result: "boom", ExitCode: 1}} {
			t.Run(tc.name+"/"+outcome.Result, func(t *testing.T) {
				run := func(t *testing.T, unreadable bool) (string, error) {
					isolateRunHome(t)
					useRemoteObserver(t, originObserver(), remoteBaseSHA)
					prs := ghRepoPRLister()
					usePRLister(t, prs)
					after := remote.ObservedPRs(nil)
					if unreadable {
						prs.Set(tc.before)
						after = tc.after
					}
					return runOneNodeGraph(t, hookRunner{
						FakeRunner: runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": outcome}),
						before:     func(runner.NodeInvocation) { prs.Set(after) },
					})
				}
				var quietErr, err error
				var out string
				t.Run("readable", func(t *testing.T) { _, quietErr = run(t, false) })
				t.Run("unreadable", func(t *testing.T) { out, err = run(t, true) })

				if (quietErr == nil) != (err == nil) || (err != nil && err.Error() != quietErr.Error()) {
					t.Errorf("error with an unreadable PR list = %v, without = %v", err, quietErr)
				}
				if a, b := exitCodeForError(quietErr), exitCodeForError(err); a != b {
					t.Errorf("exit code with an unreadable PR list = %d, without = %d", b, a)
				}
				for _, want := range []string{
					"Remote changes: none observed in the parts compared (observed: remotes, branches)\n",
					tc.want + "\n",
				} {
					if !strings.Contains(out, want) {
						t.Errorf("stdout lacks %q:\n%s", want, out)
					}
				}
				if strings.Contains(out, "  pr_") {
					t.Errorf("an unreadable PR part produced entries:\n%s", out)
				}
			})
		}
	}
}

// Every leg builds its PR lister for the run and reads it at its before and
// its after: the first leg of run and auto, and a resumed leg, whose before
// is a fresh read and whose between-legs diff compares PRs too.
func TestEveryLegReadsTheOpenPullRequests(t *testing.T) {
	t.Run("run", func(t *testing.T) {
		isolateRunHome(t)
		useRemoteObserver(t, originObserver(), remoteBaseSHA)
		prs := ghRepoPRLister()
		runIDs := usePRLister(t, prs)
		if _, err := runOneNodeGraph(t, passingWork()); err != nil {
			t.Fatalf("run: %v", err)
		}
		if want := []string{soleRunID(t)}; !slices.Equal(*runIDs, want) || prs.Calls() != 2 {
			t.Errorf("lister built for %v and read %d times, want %v and twice", *runIDs, prs.Calls(), want)
		}
	})

	t.Run("auto", func(t *testing.T) {
		isolateRunHome(t)
		inBuildDir(t, "package.json")
		useRemoteObserver(t, originObserver(), remoteBaseSHA)
		prs := ghRepoPRLister()
		runIDs := usePRLister(t, prs)
		fake := newCycleFake(map[string]runner.NodeOutcome{
			"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
			"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
		})
		var err error
		out := captureStdout(t, func() {
			err = runAutoWith([]string{"add a README section", "--accept-no-build-evidence",
				"--no-agent-mapping", "--no-skill-activation"}, fake, browser.NewFakeOpener(), os.Stdout)
		})
		if err != nil {
			t.Fatalf("auto: %v", err)
		}
		if want := []string{soleRunID(t)}; !slices.Equal(*runIDs, want) || prs.Calls() != 2 {
			t.Errorf("lister built for %v and read %d times, want %v and twice", *runIDs, prs.Calls(), want)
		}
		if !strings.Contains(out, "(observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO))") {
			t.Errorf("auto's record does not name the PR part:\n%s", out)
		}
	})

	t.Run("resume", func(t *testing.T) {
		isolateRunHome(t)
		obs := originObserver()
		obs.SetRunCommit(remoteRunSHA)
		useRemoteObserver(t, obs, remoteBaseSHA)
		prs := ghRepoPRLister()
		runIDs := usePRLister(t, prs)
		fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
			"a":    {Result: "PASS", ExitCode: 0},
			"ship": {Result: "PASS", ExitCode: 0},
		})
		nodeRunner := hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) {
			if spec.Prompt == "ship" {
				prs.Set(onGHRepo([]remote.OpenPR{
					{Number: 1, HeadRefName: "paused", HeadRefOid: remoteRunSHA},
					{Number: 2, HeadRefName: "shipped", HeadRefOid: remoteRunSHA},
				}))
			}
		}}
		g := mustParse(t, `{"name":"gate-flow","nodes":[
			{"id":"a","prompt":"a"},
			{"id":"approve","type":"gate","depends_on":["a"]},
			{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
		var err error
		captureStdout(t, func() {
			err = executeGraph(context.Background(), "run-1", g, nodeRunner, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
		})
		var paused *schedule.PausedError
		if !errors.As(err, &paused) {
			t.Fatalf("expected the first leg to pause, got %v", err)
		}

		// Opened while no leg ran, at a commit the check would pass.
		prs.Set(onGHRepo([]remote.OpenPR{{Number: 1, HeadRefName: "paused", HeadRefOid: remoteRunSHA}}))
		out := captureStdout(t, func() {
			err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), nodeRunner, nil)
		})
		if err != nil {
			t.Fatalf("resume: %v", err)
		}
		if code := exitCodeForError(err); code != 0 {
			t.Errorf("exit code = %d, want 0", code)
		}
		if want := []string{"run-1", "run-1"}; !slices.Equal(*runIDs, want) || prs.Calls() != 4 {
			t.Errorf("lister built for %v and read %d times, want %v and four times", *runIDs, prs.Calls(), want)
		}
		for _, want := range []string{
			"Remote changes between leg 1 and leg 2, while no leg ran (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):\n" +
				"  pr_opened #1 paused at 333333333333 — observed, not attributable\n",
			"Remote changes (observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO)):\n" +
				"  pr_opened #2 shipped at 333333333333 — observed during the run; points at a commit this run made\n",
		} {
			if !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		}
		if strings.Contains(out, "made by") {
			t.Errorf("the record must never say an entry was made by the run:\n%s", out)
		}
	})
}

// githubObserver is a fake whose remotes are GitHub repos, one per name,
// each advertising main.
func githubObserver(repos map[string]string) *worktree.FakeRemoteObserver {
	obs := worktree.NewFakeRemoteObserver()
	var remotes []worktree.Remote
	for name, repo := range repos {
		url := "https://github.com/" + repo + ".git"
		remotes = append(remotes, worktree.Remote{Name: name, FetchURL: url, PushURL: url})
		obs.SetHeads(name, []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}}, nil)
	}
	slices.SortFunc(remotes, func(a, b worktree.Remote) int { return strings.Compare(a.Name, b.Name) })
	obs.SetRemotes(remotes, nil)
	return obs
}

// The summary line names the repo gh read for the pull-request part, and
// where that repo was worked out from; state.json records both.
func TestRunGraph_TheRemoteChangesLineNamesThePRRepo(t *testing.T) {
	isolateRunHome(t)
	obs := githubObserver(map[string]string{"origin": "me/cli", "upstream": "cli/cli"})
	obs.SetGHDefaultRemotes(map[string]string{"origin": "base"}, nil)
	useRemoteObserver(t, obs, remoteBaseSHA)
	prs := remote.NewFakePRLister()
	prs.Set(remote.ObservedPRs([]remote.OpenPR{
		{Number: 7, HeadRefName: "feat/seven", HeadRefOid: remoteMainSHA, URL: "https://github.com/me/cli/pull/7"},
	}))
	usePRLister(t, prs)

	out, err := runOneNodeGraph(t, passingWork())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if want := "Remote changes: none observed (observed: remotes, branches, pull_requests of github.com/me/cli (gh default))\n"; !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
	leg := remoteRunSnapshot(t, soleRunID(t)).RemoteChanges.Legs[0]
	if got := leg.After.PullRequests; got.Repo != "github.com/me/cli" || got.RepoSource != "gh default" {
		t.Errorf("after pull requests recorded repo %q (%q), want github.com/me/cli (gh default)", got.Repo, got.RepoSource)
	}
}

// Two GitHub remotes and no gh default: gh is not asked, and the summary
// prints the pull-request part not compared with the reason, never as none
// observed.
func TestRunGraph_AnAmbiguousGHRepoPrintsThePRPartNotCompared(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, githubObserver(map[string]string{"origin": "me/cli", "upstream": "cli/cli"}), remoteBaseSHA)
	prs := remote.NewFakePRLister()
	usePRLister(t, prs)

	out, err := runOneNodeGraph(t, passingWork())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	reason := worktree.ReasonAmbiguousGHRepo
	for _, want := range []string{
		"Remote changes: none observed in the parts compared (observed: remotes, branches)\n",
		"  not compared: pull_requests: unavailable before (" + reason + ") and after (" + reason + ")\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout lacks %q:\n%s", want, out)
		}
	}
	if prs.Calls() != 0 {
		t.Errorf("gh asked %d times, want never when its repo cannot be told", prs.Calls())
	}
}
