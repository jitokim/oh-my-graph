package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// dirtyObserver is a quiet fake whose working tree holds paths.
func dirtyObserver(paths ...string) *worktree.FakeRemoteObserver {
	obs := worktree.NewFakeRemoteObserver()
	obs.SetWorkingTree(paths, nil)
	return obs
}

// stateHasKey reports whether the run's raw state.json carries key at all.
func stateHasKey(t *testing.T, runID, key string) bool {
	t.Helper()
	raw, _ := loadRunSnapshot(t, runID)
	return strings.Contains(raw, `"`+key+`"`)
}

func TestWorkingTreeLine(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		want  string
	}{
		{"clean", nil, ""},
		{"one", []string{"a.go"}, "working tree: 1 file changed, uncommitted (a.go)"},
		{"two", []string{"b.go", "a.go"}, "working tree: 2 files changed, uncommitted (a.go, b.go)"},
		{"five", []string{"1", "2", "3", "4", "5"}, "working tree: 5 files changed, uncommitted (1, 2, 3, 4, 5)"},
		{"six", []string{"1", "2", "3", "4", "5", "6"}, "working tree: 6 files changed, uncommitted (1, 2, 3, 4, 5, …)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := workingTreeLine(runstate.NewWorkingTree(tc.paths)); got != tc.want {
				t.Errorf("workingTreeLine(%q) = %q, want %q", tc.paths, got, tc.want)
			}
		})
	}
}

// A run that passes with its work uncommitted says so after the ledger and
// records it in state.json; the verdict and the exit code are the clean
// run's.
func TestRunGraph_ADirtyWorkingTreeIsPrintedAndRecorded(t *testing.T) {
	for _, tc := range []struct {
		name  string
		paths []string
		line  string
	}{
		{"one file", []string{"README.md"}, "working tree: 1 file changed, uncommitted (README.md)\n"},
		{"many files", []string{"g.go", "f.go", "e.go", "d.go", "c.go", "b.go", "a.go"},
			"working tree: 7 files changed, uncommitted (a.go, b.go, c.go, d.go, e.go, …)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			useRemoteObserver(t, dirtyObserver(tc.paths...), "")
			out, err := runOneNodeGraph(t, passingWork())
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if code := exitCodeForError(err); code != 0 {
				t.Errorf("exit code = %d, want 0: a dirty tree changes no verdict", code)
			}
			if !strings.Contains(out, tc.line) {
				t.Errorf("stdout lacks %q:\n%s", tc.line, out)
			}
			if remote := strings.Index(out, "Remote changes"); remote < 0 || strings.Index(out, tc.line) < remote {
				t.Errorf("the working tree line precedes the remote record:\n%s", out)
			}
			runID := soleRunID(t)
			snap := remoteRunSnapshot(t, runID)
			want := slices.Sorted(slices.Values(tc.paths))
			if snap.WorkingTree == nil || snap.WorkingTree.Changed != len(want) || !slices.Equal(snap.WorkingTree.Paths, want) {
				t.Errorf("working_tree = %+v, want %d paths %q", snap.WorkingTree, len(want), want)
			}
			if snap.Nodes["work"].Verdict != runstate.VerdictPass {
				t.Errorf("node verdict = %q, want pass", snap.Nodes["work"].Verdict)
			}
		})
	}
}

// A clean tree, and one git could not read, print no line and record no
// working_tree key.
func TestRunGraph_ACleanOrUnreadableWorkingTreeLeavesNoTrace(t *testing.T) {
	for _, tc := range []struct {
		name string
		obs  *worktree.FakeRemoteObserver
	}{
		{"clean", worktree.NewFakeRemoteObserver()},
		{"unreadable", func() *worktree.FakeRemoteObserver {
			obs := worktree.NewFakeRemoteObserver()
			obs.SetWorkingTree(nil, errors.New("fatal: not a git repository"))
			return obs
		}()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			useRemoteObserver(t, tc.obs, "")
			out, err := runOneNodeGraph(t, passingWork())
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if strings.Contains(out, "working tree") {
				t.Errorf("stdout names the working tree:\n%s", out)
			}
			if stateHasKey(t, soleRunID(t), "working_tree") {
				t.Error("state.json carries working_tree")
			}
		})
	}
}

// A failing node fails the run with the same error and exit code whether or
// not the tree is dirty.
func TestRunGraph_ADirtyWorkingTreeNeverChangesAFailure(t *testing.T) {
	failing := func() *runner.FakeRunner {
		return runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": {Result: "boom", ExitCode: 1}})
	}
	run := func(t *testing.T, obs *worktree.FakeRemoteObserver) (string, error) {
		isolateRunHome(t)
		useRemoteObserver(t, obs, "")
		return runOneNodeGraph(t, failing())
	}
	var cleanErr, dirtyErr error
	var dirtyOut string
	t.Run("clean", func(t *testing.T) { _, cleanErr = run(t, worktree.NewFakeRemoteObserver()) })
	t.Run("dirty", func(t *testing.T) { dirtyOut, dirtyErr = run(t, dirtyObserver("half-done.go")) })
	if cleanErr == nil || dirtyErr == nil || cleanErr.Error() != dirtyErr.Error() {
		t.Errorf("error dirty = %v, clean = %v; want the same failure", dirtyErr, cleanErr)
	}
	if a, b := exitCodeForError(cleanErr), exitCodeForError(dirtyErr); a != b {
		t.Errorf("exit code dirty = %d, clean = %d", b, a)
	}
	if !strings.Contains(dirtyOut, "working tree: 1 file changed, uncommitted (half-done.go)\n") {
		t.Errorf("a failed run lacks the working tree line:\n%s", dirtyOut)
	}
}

// An auto cycle's run prints and records the line as a `run` does.
func TestRunAuto_ADirtyWorkingTreeIsPrintedAndRecorded(t *testing.T) {
	isolateRunHome(t)
	inBuildDir(t, "package.json")
	useRemoteObserver(t, dirtyObserver("README.md", "docs/a.md"), "")
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
	if want := "working tree: 2 files changed, uncommitted (README.md, docs/a.md)\n"; !strings.Contains(out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, out)
	}
	if wt := remoteRunSnapshot(t, soleRunID(t)).WorkingTree; wt == nil || wt.Changed != 2 {
		t.Errorf("working_tree = %+v, want 2 paths", wt)
	}
}

// A resumed leg reads the tree at its own end: the first leg's record is
// replaced, and a tree committed in between leaves none.
func TestResume_ReadsTheWorkingTreeAtItsOwnEnd(t *testing.T) {
	isolateRunHome(t)
	obs := dirtyObserver("first.go")
	useRemoteObserver(t, obs, "")
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":    {Result: "PASS", ExitCode: 0},
		"ship": {Result: "PASS", ExitCode: 0},
	})
	g := mustParse(t, `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
	var err error
	firstOut := captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the first leg to pause, got %v", err)
	}
	if code := exitCodeForError(err); code != 2 {
		t.Errorf("paused leg exit = %d, want 2", code)
	}
	if !strings.Contains(firstOut, "working tree: 1 file changed, uncommitted (first.go)\n") {
		t.Errorf("the paused leg lacks its working tree line:\n%s", firstOut)
	}

	obs.SetWorkingTree([]string{"second.go", "first.go"}, nil)
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if want := "working tree: 2 files changed, uncommitted (first.go, second.go)\n"; !strings.Contains(out, want) {
		t.Errorf("resume stdout lacks %q:\n%s", want, out)
	}
	if wt := remoteRunSnapshot(t, "run-1").WorkingTree; wt == nil || !slices.Equal(wt.Paths, []string{"first.go", "second.go"}) {
		t.Errorf("working_tree after resume = %+v, want first.go, second.go", wt)
	}
}

// show prints the line from the record, never from the tree as it is now:
// the tree committed since the run (the fake now clean) changes nothing.
func TestShow_PrintsTheRecordedWorkingTreeLine(t *testing.T) {
	isolateRunHome(t)
	obs := dirtyObserver("a.go", "b.go")
	useRemoteObserver(t, obs, "")
	out, err := runOneNodeGraph(t, passingWork())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	line := "working tree: 2 files changed, uncommitted (a.go, b.go)\n"
	if !strings.Contains(out, line) {
		t.Fatalf("run stdout lacks %q:\n%s", line, out)
	}
	runID := soleRunID(t)

	obs.SetWorkingTree(nil, nil)
	remoteObserverFor = func(string) worktree.RemoteObserver {
		t.Error("show consulted a live observer")
		return obs
	}
	var shown bytes.Buffer
	if err := showRun(&shown, runDirFor(runID), runID); err != nil {
		t.Fatalf("show: %v", err)
	}
	if !strings.Contains(shown.String(), "\n"+line) {
		t.Errorf("show lacks %q:\n%s", line, shown.String())
	}
}

// show of a run whose tree was clean prints no line.
func TestShow_ACleanRunPrintsNoWorkingTreeLine(t *testing.T) {
	isolateRunHome(t)
	useRemoteObserver(t, worktree.NewFakeRemoteObserver(), "")
	if _, err := runOneNodeGraph(t, passingWork()); err != nil {
		t.Fatalf("run: %v", err)
	}
	runID := soleRunID(t)
	var shown bytes.Buffer
	if err := showRun(&shown, runDirFor(runID), runID); err != nil {
		t.Fatalf("show: %v", err)
	}
	if strings.Contains(shown.String(), "working tree") {
		t.Errorf("show of a clean run names the working tree:\n%s", shown.String())
	}
}
