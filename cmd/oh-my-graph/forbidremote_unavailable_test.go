package main

import (
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// A guarded run with a part of the remote change record unavailable (ADR 0046
// §5.2): it still starts, its launch names each unavailable part, its verdict
// uses only the parts observed, and an unguardable PR part is said twice —
// at launch and beside the change record in the final summary.

// assertSaidAtLaunchAndInSummary checks that line is printed exactly twice:
// once before the first node's output and once after the change record.
func assertSaidAtLaunchAndInSummary(t *testing.T, out, line string) {
	t.Helper()
	if got := strings.Count(out, line+"\n"); got != 2 {
		t.Fatalf("%q printed %d times, want 2 (launch and summary):\n%s", line, got, out)
	}
	record := strings.LastIndex(out, "\nRemote changes")
	if first, last := strings.Index(out, line), strings.LastIndex(out, line); record < 0 || first > record || last < record {
		t.Errorf("%q must come once before the change record and once after it:\n%s", line, out)
	}
}

// ADR 0046 §8.21: gh unusable is said twice. Each reason the PR part can be
// unavailable with is named as it is, and the run starts and passes.
func TestRunGraph_AGuardedRunWithTheGHPartUnavailableSaysSoAtLaunchAndInTheSummary(t *testing.T) {
	for _, reason := range []string{remote.ReasonNotAuthenticated, remote.ReasonGHNotFound, remote.ReasonNonZeroExit} {
		t.Run(reason, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			prs.Set(remote.UnavailablePRs(reason, "gh says why"))
			ran := false
			out, err := runWithRemoteChange(t, obs, prs, func(_ *worktree.FakeRemoteObserver, _ *remote.FakePRLister) { ran = true }, "--forbid-remote-changes")
			if err != nil || exitCodeForError(err) != 0 {
				t.Fatalf("err = %v (exit %d), want nil and exit 0: an unavailable part never stops a guarded run", err, exitCodeForError(err))
			}
			if !ran {
				t.Fatal("the node never ran: the run must start")
			}
			runID := soleRunID(t)

			launch := "Running graph \"remote\" (run " + runID + ")\n\n" +
				"--forbid-remote-changes: not guarded (unavailable at this leg's start): pull_requests (" + reason + "); guarded: remotes, branches\n" +
				"PR part not guarded: " + reason + "\n\n"
			if !strings.HasPrefix(out, launch) {
				t.Errorf("launch = want prefix\n%s\ngot\n%s", launch, out)
			}
			summary := "--forbid-remote-changes: not guarded (not compared): pull_requests (" + reason + "); guarded: remotes, branches\n" +
				"PR part not guarded: " + reason + "\n"
			if !strings.HasSuffix(out, summary) {
				t.Errorf("summary = want suffix\n%s\ngot\n%s", summary, out)
			}
			assertSaidAtLaunchAndInSummary(t, out, "PR part not guarded: "+reason)
			assertRunReads(t, runID, runstatus.Pass, runfeed.OutcomePassed)
		})
	}
}

// The verdict uses only the parts observed (§5.2): with the PR part
// unavailable, a forbidden branch still fails the run with exit 6, and a PR
// opened at the run's own commit, which no snapshot saw, does not.
func TestRunGraph_AGuardedRunWithThePRPartUnavailableJudgesTheObservedPartsOnly(t *testing.T) {
	t.Run("a forbidden branch in the observed part fails it", func(t *testing.T) {
		isolateRunHome(t)
		obs, prs := guardedRemote(t, nil)
		prs.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
		out, err := runWithRemoteChange(t, obs, prs, pushBranch("feat/x", remoteRunSHA), "--forbid-remote-changes")
		var forbidden *RemoteChangeForbiddenError
		if !errors.As(err, &forbidden) || exitCodeForError(err) != 6 {
			t.Fatalf("err = %v (exit %d), want a *RemoteChangeForbiddenError and exit 6", err, exitCodeForError(err))
		}
		assertSaidAtLaunchAndInSummary(t, out, "PR part not guarded: "+remote.ReasonNotAuthenticated)
		assertRunReads(t, soleRunID(t), runstatus.Fail, runfeed.OutcomeFailed)
	})
	t.Run("a PR the unavailable part hid does not", func(t *testing.T) {
		isolateRunHome(t)
		obs, prs := guardedRemote(t, nil)
		prs.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
		hidden := func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
			openPR(11, "omg-out", remoteRunSHA)(o, p)
			p.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
		}
		out, err := runWithRemoteChange(t, obs, prs, hidden, "--forbid-remote-changes")
		if err != nil || exitCodeForError(err) != 0 {
			t.Fatalf("err = %v (exit %d), want nil and exit 0", err, exitCodeForError(err))
		}
		if entries := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries; len(entries) != 0 {
			t.Errorf("entries = %+v, want none: an unavailable part is never compared", entries)
		}
		assertSaidAtLaunchAndInSummary(t, out, "PR part not guarded: "+remote.ReasonNotAuthenticated)
		assertRunReads(t, soleRunID(t), runstatus.Pass, runfeed.OutcomePassed)
	})
}

// Every unavailable part is named, not only the PR one (§5.2): with the
// remotes unreadable, neither git part is guarded, the launch names both,
// and no PR notice prints while the PR part is observed.
func TestRunGraph_AGuardedRunNamesEveryUnavailablePartAtLaunch(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	obs.SetRemotes(nil, errors.New("not a git repository"))
	out, err := runWithRemoteChange(t, obs, prs, addUpstream, "--forbid-remote-changes")
	if err != nil || exitCodeForError(err) != 0 {
		t.Fatalf("err = %v (exit %d), want nil and exit 0: the remotes part was never compared", err, exitCodeForError(err))
	}
	launch := lineContaining(t, out, "unavailable at this leg's start")
	for _, want := range []string{"remotes (", "branches (", "; guarded: pull_requests"} {
		if !strings.Contains(launch, want) {
			t.Errorf("launch line %q lacks %q", launch, want)
		}
	}
	summary := lineContaining(t, out, "not guarded (not compared)")
	for _, want := range []string{"remotes (", "branches (", "; guarded: pull_requests"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary line %q lacks %q", summary, want)
		}
	}
	if strings.Contains(out, "PR part not guarded") {
		t.Errorf("the PR part was observed; no PR notice:\n%s", out)
	}
	assertRunReads(t, soleRunID(t), runstatus.Pass, runfeed.OutcomePassed)
}

// A part that was observed at launch and lost by the end prints no launch
// notice — there was nothing to say yet — and the summary says it was not
// guarded, and why.
func TestRunGraph_AGuardedRunWhosePRPartIsLostByTheEndSaysSoInTheSummaryOnly(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	lost := func(_ *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
		p.Set(remote.UnavailablePRs(remote.ReasonTimeout, ""))
	}
	out, err := runWithRemoteChange(t, obs, prs, lost, "--forbid-remote-changes")
	if err != nil {
		t.Fatalf("err = %v, want nil", err)
	}
	if strings.Contains(out, "unavailable at this leg's start") {
		t.Errorf("every part was read at launch; no launch notice:\n%s", out)
	}
	if got := strings.Count(out, "PR part not guarded: unavailable after: timeout\n"); got != 1 {
		t.Errorf("the summary must say the PR part was not guarded, once:\n%s", out)
	}
}

// An unguarded run prints and behaves as before (§5.2): the unavailable PR
// part is in the record's own not_compared line and nowhere else.
func TestRunGraph_AnUnguardedRunWithAnUnavailablePartPrintsNoGuardNotice(t *testing.T) {
	isolateRunHome(t)
	obs, prs := guardedRemote(t, nil)
	prs.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
	out, err := runWithRemoteChange(t, obs, prs, pushBranch("feat/x", remoteRunSHA))
	if err != nil || exitCodeForError(err) != 0 {
		t.Fatalf("err = %v (exit %d), want nil and exit 0", err, exitCodeForError(err))
	}
	for _, never := range []string{"not guarded", "--forbid-remote-changes"} {
		if strings.Contains(out, never) {
			t.Errorf("an unguarded run printed %q:\n%s", never, out)
		}
	}
	if !strings.HasPrefix(out, "Running graph \"remote\" (run "+soleRunID(t)+")\n\n") {
		t.Errorf("launch changed:\n%s", out)
	}
	if !strings.Contains(out, "  not compared: pull_requests: unavailable before (not authenticated) and after (not authenticated)\n") {
		t.Errorf("the record must still say the PR part was not compared:\n%s", out)
	}
	assertRunReads(t, soleRunID(t), runstatus.Pass, runfeed.OutcomePassed)
}

// A resumed guarded leg takes its own before snapshot (§3.1) and says what it
// starts without, as a first leg does: gh logged out during the gate pause.
func TestResume_AGuardedLegStartingWithoutThePRPartSaysSoTwice(t *testing.T) {
	isolateRunHome(t)
	_, prs := guardedRemote(t, nil)
	fake := gateFlowFake()
	firstLeg := captureStdout(t, func() {
		pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true})
	})
	if strings.Contains(firstLeg, "not guarded") {
		t.Errorf("leg 1 read every part; no notice:\n%s", firstLeg)
	}

	prs.Set(remote.UnavailablePRs(remote.ReasonNotAuthenticated, ""))
	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), runner.NodeRunner(fake), nil)
	})
	if err != nil || exitCodeForError(err) != 0 {
		t.Fatalf("resume = %v (exit %d), want nil and exit 0", err, exitCodeForError(err))
	}
	if !strings.Contains(out, "--forbid-remote-changes: not guarded (unavailable at this leg's start): pull_requests (not authenticated); guarded: remotes, branches\n") {
		t.Errorf("the resumed leg's launch must name the PR part:\n%s", out)
	}
	assertSaidAtLaunchAndInSummary(t, out, "PR part not guarded: not authenticated")
	assertRunReads(t, "run-1", runstatus.Pass, runfeed.OutcomePassed)
}
