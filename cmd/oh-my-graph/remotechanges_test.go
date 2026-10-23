package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// TestMain puts every test in this package on a quiet FakeRemoteObserver (no
// remotes, every part observed) and an empty run base, so no test that runs
// a leg reaches a real remote from the package directory's own repository.
// A test about the record swaps in its own through useRemoteObserver.
func TestMain(m *testing.M) {
	remoteObserverFor = func(string) worktree.RemoteObserver { return worktree.NewFakeRemoteObserver() }
	runBaseCommit = func() string { return "" }
	os.Exit(m.Run())
}

// productionRemoteObserverFor is the wiring TestMain replaces, for the test
// that drives the real GitManager against a temp repository.
func productionRemoteObserverFor(runID string) worktree.RemoteObserver {
	return worktreeManagerFor(runID)
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
		"Remote changes (observed: remotes, branches):",
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
	if !strings.Contains(out, "TOTAL COST: $0.0100\n\nRemote changes: none observed (observed: remotes, branches)\n") {
		t.Errorf("stdout lacks the no-remote-changes line:\n%s", out)
	}
	d := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
	if len(d.Entries) != 0 || len(d.NotCompared) != 0 || len(d.Observed) != 2 {
		t.Errorf("changes = %+v, want both parts observed and nothing else", d)
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
			if !strings.Contains(quietOut, "Remote changes: none observed (observed: remotes, branches)\n") {
				t.Errorf("the readable run lacks the no-remote-changes line:\n%s", quietOut)
			}
			if len(quiet.NotCompared) != 0 {
				t.Errorf("the readable run compared nothing it should have: %+v", quiet.NotCompared)
			}
			if len(broken.NotCompared) != 1 || broken.NotCompared[0].Part != runstate.RemotePartBranches ||
				!strings.Contains(broken.NotCompared[0].Reason, "ls-remote broken") {
				t.Errorf("not_compared = %+v, want the branches part, naming the broken remote", broken.NotCompared)
			}
			if len(broken.Observed) != 1 || broken.Observed[0] != runstate.RemotePartRemotes {
				t.Errorf("observed = %v, want only remotes", broken.Observed)
			}
			for _, want := range []string{
				"Remote changes: none observed in the parts compared (observed: remotes)\n",
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
	if !strings.Contains(firstOut, "Remote changes: none observed (observed: remotes, branches)\n") {
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
		"Remote changes between leg 1 and leg 2, while no leg ran (observed: remotes, branches):\n" +
			"  branch_created origin/paused at 333333333333 — observed, not attributable\n",
		"Remote changes (observed: remotes, branches):\n" +
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
// during: Remotes cancels the run context and answers with its error — an
// unreachable remote's git killed by the interrupt. Otherwise the last
// ls-remote answers in full and cancels as it returns, so the snapshot comes
// back observed with the run already interrupted. It counts Remotes, one
// call per snapshot taken.
type interruptingObserver struct {
	*worktree.FakeRemoteObserver
	cancel       context.CancelFunc
	during       bool
	remotesCalls int
}

func (o *interruptingObserver) Remotes(ctx context.Context) ([]worktree.Remote, error) {
	o.remotesCalls++
	if o.during {
		o.cancel()
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return o.FakeRemoteObserver.Remotes(ctx)
}

func (o *interruptingObserver) LsRemoteHeads(ctx context.Context, remote string) ([]worktree.RemoteHead, error) {
	heads, err := o.FakeRemoteObserver.LsRemoteHeads(ctx, remote)
	o.cancel()
	return heads, err
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
			obs := &interruptingObserver{FakeRemoteObserver: originObserver(), cancel: cancel, during: tc.during}
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

			rc := remoteRunSnapshot(t, "run-1").RemoteChanges
			if rc == nil || len(rc.Legs) != 1 || rc.Legs[0].After == nil {
				t.Fatalf("remote_changes = %+v, want one leg with an after", rc)
			}
			after := *rc.Legs[0].After
			if after.Remotes.Status != runstate.RemotePartUnavailable || after.Remotes.Reason != "interrupted" ||
				after.Branches.Status != runstate.RemotePartUnavailable || after.Branches.Reason != "interrupted" {
				t.Errorf("after = %+v, want both parts unavailable, reason interrupted", after)
			}
			d := lastLegChanges(t, remoteRunSnapshot(t, "run-1"))
			if len(d.Entries) != 0 || len(d.Observed) != 0 || len(d.NotCompared) != 2 {
				t.Errorf("changes = %+v, want both parts not compared, never none observed", d)
			}
		})
	}
}
