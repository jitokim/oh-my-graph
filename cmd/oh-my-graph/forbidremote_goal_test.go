package main

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// A guarded goal loop (ADR 0046 §5.1 under ADR 0011): the final cycle's
// forbidden remote change is the goal's exit 6, named as `run` names it; a
// non-final cycle's is a failed run the loop iterates past.

// forbiddenBranchNamed is how `run` names pushBranch("feat/x", remoteRunSHA).
const forbiddenBranchNamed = "new remote state observed during the run, and --forbid-remote-changes was set: " +
	"branch_created origin/feat/x at 333333333333 (observed during the run; points at a commit this run made)"

// runGuardedGoal runs `auto` for a two-cycle goal whose cycle pushCycle's
// node pushes feat/x at the run's own commit, the assessor answering
// assess1 and assess2, and returns stdout, the error and the goal's run ids
// in cycle order.
func runGuardedGoal(t *testing.T, pushCycle int, assess1, assess2 string, extra ...string) (string, error, []string) {
	t.Helper()
	isolateRunHome(t)
	inBuildDir(t, "package.json")
	obs, prs := guardedRemote(t, nil)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1":   {Result: cycleSpec},
		"work-1":   {SessionID: "s-1", Result: "PASS", ExitCode: 0},
		"assess-1": {Result: assess1},
		"plan-2":   {Result: cycleSpec},
		"work-2":   {SessionID: "s-2", Result: "PASS", ExitCode: 0},
		"assess-2": {Result: assess2},
	})
	works := 0
	nodeRunner := hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) {
		if !strings.HasPrefix(spec.Prompt, "work") {
			return
		}
		works++
		if works == pushCycle {
			pushBranch("feat/x", remoteRunSHA)(obs, prs)
		}
	}}
	args := append([]string{"add a README section", "--accept-no-build-evidence",
		"--no-agent-mapping", "--no-skill-activation"}, extra...)
	var err error
	out := captureStdout(t, func() {
		err = runAutoWith(args, nodeRunner, browser.NewFakeOpener(), os.Stdout)
	})
	var ids []string
	for _, snap := range goalSnapshots(t) {
		ids = append(ids, snap.RunID)
	}
	return out, err, ids
}

// The final cycle's forbidden change exits 6 whatever the assessor said,
// and the error mainExitCode prints as the last line names the entry.
func TestRunAuto_AForbiddenRemoteChangeInTheFinalCycleExits6NamingIt(t *testing.T) {
	for _, tc := range []struct {
		name   string
		assess string
		want   func(runID string) string
	}{
		{"assessor judged met", cycleAssessMet, func(runID string) string {
			return "the assessor judged the goal met, but the final cycle's run FAILED (run " + runID +
				") — a verdict never overrides the engine's own outcome: " + forbiddenBranchNamed
		}},
		{"cycles exhausted", cycleAssessNotMet, func(runID string) string {
			return "goal not met after 2 cycle(s); remaining: add the README section; the final cycle's run FAILED (run " + runID +
				"): " + forbiddenBranchNamed
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err, ids := runGuardedGoal(t, 2, cycleAssessNotMet, tc.assess, "--max-cycles", "2", "--forbid-remote-changes")
			if len(ids) != 2 {
				t.Fatalf("run ids = %v, want two cycles", ids)
			}
			var forbidden *RemoteChangeForbiddenError
			if !errors.As(err, &forbidden) {
				t.Fatalf("err = %v, want a *RemoteChangeForbiddenError inside it", err)
			}
			if code := exitCodeForError(err); code != 6 {
				t.Errorf("exit code = %d, want 6", code)
			}
			if got, want := err.Error(), tc.want(ids[1]); got != want {
				t.Errorf("last line =\n%q\nwant\n%q", got, want)
			}
		})
	}
}

// A forbidden change in cycle 1 only: the loop iterates past it as a failed
// run, and the final cycle decides the exit — 0 on a met goal over a passed
// run, 1 on an unmet one, its error never naming cycle 1's change.
func TestRunAuto_AForbiddenRemoteChangeInANonFinalCycleLeavesTheExitToTheFinalCycle(t *testing.T) {
	t.Run("final cycle met", func(t *testing.T) {
		_, err, _ := runGuardedGoal(t, 1, cycleAssessNotMet, cycleAssessMet, "--max-cycles", "2", "--forbid-remote-changes")
		if err != nil || exitCodeForError(err) != 0 {
			t.Fatalf("err = %v (exit %d), want nil and exit 0", err, exitCodeForError(err))
		}
	})
	t.Run("final cycle unmet", func(t *testing.T) {
		_, err, _ := runGuardedGoal(t, 1, cycleAssessNotMet, cycleAssessNotMet, "--max-cycles", "2", "--forbid-remote-changes")
		if code := exitCodeForError(err); code != 1 {
			t.Errorf("exit code = %d, want 1", code)
		}
		if want := "goal not met after 2 cycle(s); remaining: add the README section"; err == nil || err.Error() != want {
			t.Errorf("err = %v, want %q", err, want)
		}
	})
}

// Unguarded, the same final-cycle push is reported only: the goal's exit
// and its error are the loop's own.
func TestRunAuto_AnUnguardedFinalCycleChangeKeepsTheLoopsExit(t *testing.T) {
	_, err, _ := runGuardedGoal(t, 2, cycleAssessNotMet, cycleAssessNotMet, "--max-cycles", "2")
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if want := "goal not met after 2 cycle(s); remaining: add the README section"; err == nil || err.Error() != want {
		t.Errorf("err = %v, want %q", err, want)
	}
}

// --max-cycles 1 is the single-cycle path, which returns the leg's own error.
func TestRunAuto_AForbiddenRemoteChangeInASingleCycleStillExits6(t *testing.T) {
	_, err, _ := runGuardedGoal(t, 1, cycleAssessMet, cycleAssessMet, "--forbid-remote-changes")
	if code := exitCodeForError(err); code != 6 {
		t.Errorf("exit code = %d, want 6", code)
	}
	if err == nil || err.Error() != forbiddenBranchNamed {
		t.Errorf("err = %v, want %q", err, forbiddenBranchNamed)
	}
}
