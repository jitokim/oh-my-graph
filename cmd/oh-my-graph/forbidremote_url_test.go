package main

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// A guarded leg reads its after through the fetch URLs its own before saw
// (ADR 0046 §5.1), so a node that pushes its own commit to a new branch and
// then removes or re-points the remote cannot hide the push. An unguarded
// leg still reads by name, as before.

// originURL is originObserver's fetch URL.
const originURL = "https://example.com/o.git"

// unreachableURL is where a node re-points origin.
const unreachableURL = "https://unreachable.invalid/x.git"

// errUnreachable is what a read of unreachableURL — or of origin by name,
// once it points there — answers.
var errUnreachable = errors.New("could not resolve host")

// pushThenRemoveOrigin pushes the run's commit to feat/x, then removes origin.
func pushThenRemoveOrigin(obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister) {
	pushBranch("feat/x", remoteRunSHA)(obs, prs)
	obs.SetRemotes(nil, nil)
}

// pushThenRepointOrigin pushes the run's commit to feat/x, then points
// origin at a URL nothing answers: by name, origin now cannot be read, while
// the repository the push went to still holds it.
func pushThenRepointOrigin(obs *worktree.FakeRemoteObserver, prs *remote.FakePRLister) {
	pushBranch("feat/x", remoteRunSHA)(obs, prs)
	pushed, _ := obs.LsRemoteHeads(context.Background(), "origin")
	obs.SetURLHeads(originURL, pushed, nil)
	obs.SetRemotes([]worktree.Remote{{Name: "origin", FetchURL: unreachableURL, PushURL: unreachableURL}}, nil)
	obs.SetHeads("origin", nil, errUnreachable)
	obs.SetURLHeads(unreachableURL, nil, errUnreachable)
}

const pushedEntry = "branch_created origin/feat/x at 333333333333 (observed during the run; points at a commit this run made)"

// repointedNotCompared is the piece a re-pointed origin leaves uncompared:
// the repository it now points at, which no before read.
const repointedNotCompared = "fetch URL changed: the new repository has no before refs to compare"

// Tests 1 and 2: the QA cases. Removing or re-pointing origin after the push
// no longer hides it: the guarded leg reads origin through its before URL,
// records the push as a run_commit branch_created beside the remote entry,
// and ends with exit 6. Removed, nothing went uncompared, so the end line
// claims nothing it did not compare; re-pointed, the new repository's
// branches were never read, so the end line names them as not guarded and
// leaves branches out of guarded.
func TestRunGraph_AGuardedLegReadsThroughTheBeforeURL(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  remoteChange
		kind    runstate.RemoteChangeKind
		skipped string
	}{
		{"push then remote remove origin", pushThenRemoveOrigin, runstate.RemoteRemoved, ""},
		{"push then remote set-url origin to an unreachable URL", pushThenRepointOrigin, runstate.RemoteURLChanged, repointedNotCompared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			out, err := runWithRemoteChange(t, obs, prs, tc.change, "--forbid-remote-changes")
			if got := exitCodeForError(err); got != 6 {
				t.Fatalf("err = %v (exit %d), want exit 6", err, got)
			}
			if !strings.Contains(err.Error(), pushedEntry) {
				t.Errorf("err = %v, want it to name %q", err, pushedEntry)
			}
			if calls := obs.URLCalls(); len(calls) != 1 || calls[0] != originURL {
				t.Errorf("URL reads = %v, want one, of origin's before URL", calls)
			}
			d := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
			if len(d.Entries) != 2 || d.Entries[0].Kind != tc.kind || d.Entries[1].Kind != runstate.BranchCreated ||
				d.Entries[1].Attribution != runstate.AttributionRunCommit {
				t.Errorf("entries = %+v, want %s, then the run_commit branch_created", d.Entries, tc.kind)
			}
			if want := tc.skipped; want == "" {
				if len(d.NotCompared) != 0 {
					t.Errorf("not_compared = %+v, want origin's refs compared", d.NotCompared)
				}
				if strings.Contains(out, "not guarded") || strings.Contains(out, "not compared") {
					t.Errorf("every part was compared; nothing may read as not guarded:\n%s", out)
				}
			} else {
				wantNC := []runstate.RemoteNotCompared{{Part: runstate.RemotePartBranches, Remote: "origin", Reason: want}}
				if !reflect.DeepEqual(d.NotCompared, wantNC) {
					t.Errorf("not_compared = %+v, want %+v", d.NotCompared, wantNC)
				}
				summary := "--forbid-remote-changes: not guarded (not compared): branches of origin (" + want + "); guarded: remotes, pull_requests\n"
				if !strings.HasSuffix(out, summary) {
					t.Errorf("summary = want suffix\n%s\ngot\n%s", summary, out)
				}
			}
			if want := "  branch_created origin/feat/x at 333333333333 — " + labelRunCommit + "\n"; !strings.Contains(out, want) {
				t.Errorf("stdout lacks %q:\n%s", want, out)
			}
		})
	}
}

// Test 3: a before URL nothing answers at the after leaves origin's branches
// not compared. The end line says so and never calls branches guarded, and
// the leg exits as its nodes did — the guard fails nothing it did not see.
func TestRunGraph_AGuardedLegWhoseBeforeURLIsUnreachable(t *testing.T) {
	for _, tc := range []struct {
		name    string
		outcome runner.NodeOutcome
		exit    int
	}{
		{"node passes", runner.NodeOutcome{Result: "PASS", ExitCode: 0}, 0},
		{"node fails", runner.NodeOutcome{Result: "boom", ExitCode: 1}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			change := func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
				pushBranch("feat/x", remoteRunSHA)(o, p)
				o.SetURLHeads(originURL, nil, errUnreachable)
			}
			path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
			nodeRunner := hookRunner{FakeRunner: runner.NewFakeRunner(map[string]runner.NodeOutcome{"work": tc.outcome}),
				before: func(runner.NodeInvocation) { change(obs, prs) }}
			var err error
			out := captureStdout(t, func() {
				err = runGraphWith([]string{path, "--forbid-remote-changes"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
			})
			if got := exitCodeForError(err); got != tc.exit {
				t.Fatalf("err = %v (exit %d), want exit %d", err, got, tc.exit)
			}
			summary := "--forbid-remote-changes: not guarded (not compared): branches (unavailable after: ls-remote origin: could not resolve host); guarded: remotes, pull_requests\n"
			if !strings.HasSuffix(out, summary) {
				t.Errorf("summary = want suffix\n%s\ngot\n%s", summary, out)
			}
		})
	}
}

// Test 4: an unguarded leg reads by name as it always did. Removing origin
// leaves its refs not compared, re-pointing it makes the branches part
// unavailable; nothing is read through a URL, nothing names a read through,
// and the run passes.
func TestRunGraph_AnUnguardedLegStillReadsByName(t *testing.T) {
	const observed = "(observed: remotes, branches, pull_requests of github.com/o/r (GH_REPO))"
	for _, tc := range []struct {
		name   string
		change remoteChange
		record string
	}{
		{"push then remote remove origin", pushThenRemoveOrigin,
			"\nRemote changes " + observed + ":\n" +
				"  remote_removed origin (was https://example.com/o.git) — observed, not attributable\n" +
				"  not compared: branches of origin: remote removed: it has no after refs to compare\n"},
		{"push then remote set-url origin to an unreachable URL", pushThenRepointOrigin,
			"\nRemote changes (observed: remotes, pull_requests of github.com/o/r (GH_REPO)):\n" +
				"  remote_url_changed origin https://example.com/o.git -> https://unreachable.invalid/x.git — observed, not attributable\n" +
				"  not compared: branches: unavailable after: ls-remote origin: could not resolve host\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs, prs := guardedRemote(t, nil)
			out, err := runWithRemoteChange(t, obs, prs, tc.change)
			if err != nil {
				t.Fatalf("err = %v, want nil", err)
			}
			if !strings.HasSuffix(out, tc.record) {
				t.Errorf("record = want suffix\n%s\ngot\n%s", tc.record, out)
			}
			if calls := obs.URLCalls(); len(calls) != 0 {
				t.Errorf("an unguarded leg read through URLs %v", calls)
			}
			raw, _ := loadRunSnapshot(t, soleRunID(t))
			if strings.Contains(raw, "read_through_before_url") {
				t.Errorf("an unguarded state.json names a read through a before URL:\n%s", raw)
			}
		})
	}
}

// credentialURL and its redacted form, as state.json records it. The
// token is a fixed test value.
const (
	credentialToken    = "omg-test-token-0123"
	credentialURL      = "https://user:" + credentialToken + "@example.com/x.git"
	credentialRedacted = "https://***@example.com/x.git"
	movedToken         = "omg-test-token-4567"
	movedURL           = "https://user:" + movedToken + "@example.com/y.git"
)

// credentialObserver is originObserver with credentialURL as origin's URL.
func credentialObserver(t *testing.T) *worktree.FakeRemoteObserver {
	t.Helper()
	obs := originObserver()
	obs.SetRemotes([]worktree.Remote{{Name: "origin", FetchURL: credentialURL, PushURL: credentialURL}}, nil)
	obs.SetRunCommit(remoteRunSHA)
	useRemoteObserver(t, obs, remoteBaseSHA)
	prs := ghRepoPRLister()
	prs.Set(onGHRepo(nil))
	usePRLister(t, prs)
	return obs
}

// Test 5: a resumed guarded leg takes its own before and reads its after
// through that before's raw URL — never the redacted one state.json saved,
// nor the one the first leg saw. origin is re-pointed while the run is
// paused, and removed by the resumed leg's node after its push.
func TestResume_AGuardedLegReadsThroughItsOwnBeforesRawURL(t *testing.T) {
	isolateRunHome(t)
	obs := credentialObserver(t)
	fake := gateFlowFake()
	nodeRunner := hookRunner{FakeRunner: fake, before: func(spec runner.NodeInvocation) {
		if spec.Prompt == "ship" {
			obs.SetHeads("origin", []worktree.RemoteHead{{Branch: "main", SHA: remoteMainSHA}, {Branch: "feat/x", SHA: remoteRunSHA}}, nil)
			obs.SetRemotes(nil, nil)
		}
	}}
	pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true})
	if calls := obs.URLCalls(); len(calls) != 1 || calls[0] != credentialURL {
		t.Fatalf("leg 1's URL reads = %v, want one, of its before's raw URL", calls)
	}

	obs.SetRemotes([]worktree.Remote{{Name: "origin", FetchURL: movedURL, PushURL: movedURL}}, nil)
	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), nodeRunner, nil)
	})
	if got := exitCodeForError(err); got != 6 {
		t.Fatalf("resume = %v (exit %d), want exit 6", err, got)
	}
	if calls := obs.URLCalls(); len(calls) != 2 || calls[1] != movedURL {
		t.Errorf("URL reads = %v, want the resumed leg's one of its own before's raw URL", calls)
	}
	for _, url := range obs.URLCalls() {
		if strings.Contains(url, "***") {
			t.Errorf("an after read the redacted URL %q", url)
		}
	}
	raw, snap := loadRunSnapshot(t, "run-1")
	legs := snap.RemoteChanges.Legs
	if len(legs) != 2 {
		t.Fatalf("legs = %d, want 2", len(legs))
	}
	if got := legs[0].Before.Remotes.Configured[0].FetchURL; got != credentialRedacted {
		t.Errorf("leg 1's recorded fetch URL = %q, want %q", got, credentialRedacted)
	}
	if got := legs[1].Before.Remotes.Configured[0].FetchURL; got != "https://***@example.com/y.git" {
		t.Errorf("leg 2's recorded fetch URL = %q, want it redacted", got)
	}
	for _, token := range []string{credentialToken, movedToken} {
		if strings.Contains(raw, token) || strings.Contains(out, token) || strings.Contains(err.Error(), token) {
			t.Errorf("the credential %q leaked into state.json, stdout or the error", token)
		}
	}
}

// Test 6: the raw URL never leaves memory. A guarded run whose origin URL
// carries a credential, in each of the cases above, writes it to no file of
// the run — state.json and events.jsonl included — nor to stdout or stderr.
func TestRunGraph_AGuardedLegNeverWritesTheRawURL(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change remoteChange
	}{
		{"push then remove", func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
			pushBranch("feat/x", remoteRunSHA)(o, p)
			o.SetRemotes(nil, nil)
		}},
		{"push then re-point", func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
			pushBranch("feat/x", remoteRunSHA)(o, p)
			o.SetRemotes([]worktree.Remote{{Name: "origin", FetchURL: unreachableURL, PushURL: unreachableURL}}, nil)
		}},
		{"before URL unreachable", func(o *worktree.FakeRemoteObserver, _ *remote.FakePRLister) {
			o.SetURLHeads(credentialURL, nil, errors.New("fatal: unable to access '"+credentialURL+"'"))
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			obs := credentialObserver(t)
			var out string
			stderr, err := captureStderr(t, func() error {
				var runErr error
				out = captureStdout(t, func() {
					path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
					nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) { tc.change(obs, nil) }}
					runErr = runGraphWith([]string{path, "--forbid-remote-changes"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
				})
				return runErr
			})
			if calls := obs.URLCalls(); len(calls) != 1 || calls[0] != credentialURL {
				t.Fatalf("URL reads = %v, want one, of the raw before URL", calls)
			}
			streams := out + stderr
			if err != nil {
				streams += err.Error()
			}
			if strings.Contains(streams, credentialToken) {
				t.Errorf("the credential reached stdout, stderr or the error:\n%s", streams)
			}
			if !strings.Contains(out, "origin") {
				t.Errorf("no record printed:\n%s", out)
			}
			runDir := runDirFor(soleRunID(t))
			seen := 0
			walkErr := filepath.WalkDir(runDir, func(path string, d fs.DirEntry, err error) error {
				if err != nil || d.IsDir() {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				if name := d.Name(); name == stateFileName || name == "events.jsonl" {
					seen++
				}
				if strings.Contains(string(data), credentialToken) {
					t.Errorf("%s holds the credential", path)
				}
				return nil
			})
			if walkErr != nil {
				t.Fatal(walkErr)
			}
			if seen != 2 {
				t.Errorf("checked %d of state.json and events.jsonl, want both", seen)
			}
			raw, _ := loadRunSnapshot(t, soleRunID(t))
			if !strings.Contains(raw, credentialRedacted) {
				t.Errorf("state.json lacks the redacted URL %q", credentialRedacted)
			}
		})
	}
}

// attributingObserver is the real GitManager with §5.1's check answered for
// one sha, so a test need not build the run's own branches.
type attributingObserver struct {
	worktree.RemoteObserver
	runCommit *string
}

func (o attributingObserver) CommitAttributable(_ context.Context, sha, _ string) bool {
	return sha == *o.runCommit
}

// The QA cases against the production GitManager and a local bare origin:
// the node commits, pushes to a new branch, then removes or re-points
// origin. Guarded, the leg exits 6; unguarded, it passes and reports only
// what it saw by name.
func TestRunGraph_TheBeforeURLReadAgainstARealRepository(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hide    []string
		skipped int
	}{
		{"remote remove", []string{"remote", "remove", "origin"}, 0},
		{"remote set-url", []string{"remote", "set-url", "origin", "https://127.0.0.1:1/unreachable.git"}, 1},
	} {
		for _, guarded := range []bool{true, false} {
			name := tc.name + "/unguarded"
			if guarded {
				name = tc.name + "/guarded"
			}
			t.Run(name, func(t *testing.T) {
				isolateRunHome(t)
				dir := remoteRepo(t)
				var runCommit string
				prevObs, prevBase := remoteObserverFor, runBaseCommit
				t.Cleanup(func() { remoteObserverFor, runBaseCommit = prevObs, prevBase })
				remoteObserverFor = func(runID string) worktree.RemoteObserver {
					return attributingObserver{RemoteObserver: productionRemoteObserverFor(runID), runCommit: &runCommit}
				}
				runBaseCommit = launchCommitOfWorkingDir

				nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(runner.NodeInvocation) {
					launchCommitWork(t, dir, "work.md")
					runCommit = mustGit(t, dir, "rev-parse", "HEAD")
					mustGit(t, dir, "push", "-q", "origin", "HEAD:refs/heads/feat/x")
					mustGit(t, dir, tc.hide...)
				}}
				path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
				args := []string{path}
				if guarded {
					args = append(args, "--forbid-remote-changes")
				}
				var err error
				captureStdout(t, func() {
					err = runGraphWith(args, nodeRunner, browser.NewFakeOpener(), os.Stdout)
				})
				d := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
				pushed := false
				for _, e := range d.Entries {
					pushed = pushed || (e.Kind == runstate.BranchCreated && e.Branch == "feat/x" && e.Attribution == runstate.AttributionRunCommit)
				}
				if guarded {
					if got := exitCodeForError(err); got != 6 {
						t.Fatalf("err = %v (exit %d), want exit 6", err, got)
					}
					if !pushed || len(d.NotCompared) != tc.skipped {
						t.Errorf("diff = %+v, want the run_commit branch_created and %d uncompared", d, tc.skipped)
					}
					return
				}
				if err != nil {
					t.Fatalf("unguarded: err = %v, want nil", err)
				}
				if pushed || len(d.NotCompared) == 0 {
					t.Errorf("unguarded diff = %+v, want origin's refs not compared, as by name", d)
				}
			})
		}
	}
}
