package main

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/remote"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/worktree"
)

// A guarded after reads the before URLs inside the repository only while the
// repository's own config is what it was at the before (#410). A node that
// pushes its own commit to a new branch and then breaks that config, or
// rewrites it to send the read to another repository, gets the read moved
// outside the repository, and the push still fails the run with exit 6.

// configToken is a config value the node writes beside its change; it must
// reach no file of the run, and no output.
const configToken = "omg-config-token-9876"

// appendTo appends text to path.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(text); err != nil {
		t.Fatal(err)
	}
}

// laneGraph is a one-node graph whose node runs in the engine's own worktree
// lane, on the run's branch omg/<run-id>/lane: a commit it makes is one the
// production §5.1 check attributes to the run.
const laneGraph = "name: remote\nnodes:\n  - { id: work, prompt: work, worktree: lane }\n"

// useRealRemoteObserver puts the leg on the production GitManager and the
// run base on the working directory's HEAD, as `run` wires them.
func useRealRemoteObserver(t *testing.T) {
	t.Helper()
	prevObs, prevBase := remoteObserverFor, runBaseCommit
	t.Cleanup(func() { remoteObserverFor, runBaseCommit = prevObs, prevBase })
	remoteObserverFor, runBaseCommit = productionRemoteObserverFor, launchCommitOfWorkingDir
}

// commitAndPushFromLane is a node's own push: it commits in the worktree the
// engine gave it (spec.Cwd) and pushes that commit to feat/x on origin, the
// repository at originPath.
func commitAndPushFromLane(t *testing.T, spec runner.NodeInvocation, originPath string) {
	t.Helper()
	if spec.Cwd == "" {
		t.Fatal("the node ran without its engine worktree")
	}
	launchCommitWork(t, spec.Cwd, "work.md")
	mustGit(t, spec.Cwd, "push", "-q", originPath, "HEAD:refs/heads/feat/x")
}

// originPath is the absolute path of dir's origin, a local bare repository.
func originPath(t *testing.T, dir string) string {
	t.Helper()
	origin := mustGit(t, dir, "remote", "get-url", "origin")
	if !filepath.IsAbs(origin) {
		origin = filepath.Join(dir, origin)
	}
	return origin
}

// gitArgvLog is a git on PATH that appends the argv of every git started
// while it is not paused to a log, then runs the real git: the engine's own
// git commands, which a cmd test cannot otherwise see. The test's own gits
// run paused. A git that git itself starts finds the real one first, on
// git's exec path, and is not logged.
type gitArgvLog struct{ dir string }

// logGitArgv puts a gitArgvLog first on PATH for the rest of the test,
// paused.
func logGitArgv(t *testing.T) gitArgvLog {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	l := gitArgvLog{dir: t.TempDir()}
	script := "#!/bin/sh\n[ -e '" + l.dir + "/paused' ] || printf '%s\\n' \"$*\" >> '" + l.dir + "/argv'\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(l.dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", l.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	l.pause(t)
	return l
}

func (l gitArgvLog) pause(t *testing.T) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(l.dir, "paused"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func (l gitArgvLog) resume(t *testing.T) {
	t.Helper()
	if err := os.Remove(filepath.Join(l.dir, "paused")); err != nil {
		t.Fatal(err)
	}
}

// read is every argv logged, one per line.
func (l gitArgvLog) read(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, "argv"))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

// realRun is what guardedRealRun saw: stdout, stderr, the argv of every git
// the engine started, and the run's error.
type realRun struct {
	out, stderr, argv string
	err               error
}

// guardedRealRun runs a one-node guarded graph against dir, the production
// GitManager reading it and attributing through the real §5.1 check, whose
// node commits on its run branch, pushes the commit to feat/x on origin,
// then runs change.
func guardedRealRun(t *testing.T, dir string, change func()) realRun {
	t.Helper()
	useRealRemoteObserver(t)
	origin := originPath(t, dir)
	argv := logGitArgv(t)
	nodeRunner := hookRunner{FakeRunner: passingWork(), before: func(spec runner.NodeInvocation) {
		argv.pause(t)
		defer argv.resume(t)
		commitAndPushFromLane(t, spec, origin)
		change()
	}}
	path := writeGraphFile(t, laneGraph)
	var run realRun
	argv.resume(t)
	run.stderr, run.err = captureStderr(t, func() error {
		var runErr error
		run.out = captureStdout(t, func() {
			runErr = runGraphWith([]string{path, "--forbid-remote-changes"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
		})
		return runErr
	})
	argv.pause(t)
	run.argv = argv.read(t)
	if !strings.Contains(run.argv, "ls-remote --heads omg-before-snapshot") {
		t.Fatalf("the argv log missed the engine's before-URL read:\n%s", run.argv)
	}
	return run
}

// assertGuardedPushFailed fails unless err is exit 6 naming feat/x's
// branch_created as a run_commit, and the record agrees.
func assertGuardedPushFailed(t *testing.T, err error) {
	t.Helper()
	assertGuardedPushForbidden(t, err, runstate.AttributionRunCommit)
}

// assertGuardedPushForbidden fails unless err is exit 6 naming feat/x's
// branch_created, which the record attributes attr: a run_commit, or a
// not_attributable one forbidden because the leg's commit check could not be
// trusted, which the error then says.
func assertGuardedPushForbidden(t *testing.T, err error, attr runstate.RemoteAttribution) {
	t.Helper()
	if got := exitCodeForError(err); got != 6 {
		t.Fatalf("err = %v (exit %d), want exit 6", err, got)
	}
	want := "(" + labelNotAttributable + "; " + unprovenAttribution + ")"
	if attr == runstate.AttributionRunCommit {
		want = "(" + labelRunCommit + ")"
	}
	if !strings.Contains(err.Error(), "branch_created origin/feat/x") || !strings.Contains(err.Error(), want) {
		t.Errorf("err = %v, want it to name the branch_created of origin/feat/x with %q", err, want)
	}
	found := false
	for _, e := range lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries {
		found = found || e.Kind == runstate.BranchCreated && e.Branch == "feat/x" && e.Attribution == attr
	}
	if !found {
		t.Errorf("state.json lacks feat/x's %s branch_created", attr)
	}
}

// assertNotWritten fails if any of leaks is in a file of the run — state.json
// and events.jsonl among them — in the run's output or error, or on the argv
// of a git the engine started.
func assertNotWritten(t *testing.T, run realRun, leaks ...string) {
	t.Helper()
	streams := run.out + run.stderr
	if run.err != nil {
		streams += run.err.Error()
	}
	for _, leak := range leaks {
		if strings.Contains(run.argv, leak) {
			t.Errorf("a git the engine started carries %q on its argv", leak)
		}
	}
	seen := 0
	walkErr := filepath.WalkDir(runDirFor(soleRunID(t)), func(path string, d fs.DirEntry, err error) error {
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
		for _, leak := range leaks {
			if strings.Contains(string(data), leak) {
				t.Errorf("%s holds %q", path, leak)
			}
		}
		return nil
	})
	if walkErr != nil {
		t.Fatal(walkErr)
	}
	if seen != 2 {
		t.Errorf("checked %d of state.json and events.jsonl, want both", seen)
	}
	for _, leak := range leaks {
		if strings.Contains(streams, leak) {
			t.Errorf("the output or the error holds %q", leak)
		}
	}
}

// Breaking the repository's config after the push — a garbage line, a remote
// whose url has no value, no read permission — no longer ends the run with
// exit 0 and the branches read as not guarded. The real §5.1 check reads that
// config too: a bare url leaves it readable and the push is a run_commit;
// garbage or no permission fails the check itself, the push is recorded
// not_attributable, and the guard fails closed on it.
func TestRunGraph_ABrokenRepoConfigCannotHideAGuardedPush(t *testing.T) {
	for _, tc := range []struct {
		name  string
		attr  runstate.RemoteAttribution
		apply func(t *testing.T, config string)
	}{
		{"garbage line", runstate.AttributionNotAttributable, func(t *testing.T, config string) { appendTo(t, config, "this is not config\n") }},
		{"remote with a bare url", runstate.AttributionRunCommit, func(t *testing.T, config string) { appendTo(t, config, "[remote \"x\"]\n\turl\n") }},
		{"chmod 000", runstate.AttributionNotAttributable, func(t *testing.T, config string) {
			if os.Getuid() == 0 {
				t.Skip("root reads a mode 000 file")
			}
			if err := os.Chmod(config, 0); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.Chmod(config, 0o644) })
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			dir := remoteRepo(t)
			origin := originPath(t, dir)
			run := guardedRealRun(t, dir, func() { tc.apply(t, filepath.Join(dir, ".git", "config")) })
			assertGuardedPushForbidden(t, run.err, tc.attr)
			if strings.Contains(run.out, "not guarded (not compared): branches") {
				t.Errorf("the branches read as not guarded:\n%s", run.out)
			}
			if strings.Contains(run.argv, origin) {
				t.Errorf("a git the engine started carries the before URL %q on its argv", origin)
			}
		})
	}
}

// decoyToken is the credential in credentialDecoy, an insteadOf target that
// carries one; credentialDecoyRedacted is it as `git remote -v` prints it
// and the record redacts it.
const (
	decoyToken              = "omg-decoy-token-5432"
	credentialDecoy         = "https://user:" + decoyToken + "@omg-decoy.invalid/x.git"
	credentialDecoyRedacted = "https://***@omg-decoy.invalid/x.git"
)

// Redirecting origin's URL with url.<base>.insteadOf — in .git/config, or in
// a file .git/config already included — to another, empty, repository, or to
// a URL carrying a credential, does not send the after's read there: the
// push is still a run_commit, attributed by the real §5.1 check, and exit 6.
//
// What is recorded is checked exactly. The after lists origin as `git remote
// -v` prints it, through the insteadOf, so state.json and the change record
// show the redirected URL as a remote_url_changed — redacted, as every listed
// URL always is, guarded or not, and as an unguarded run records it too
// (TakeRemoteSnapshot): that is the remotes part reporting what the node did,
// not the guarded read leaking. So the redacted form is allowed, and only
// there. Never written or put on an engine git's argv: the credential, the
// insteadOf target in its raw form, or any config value the node wrote. The
// before URL — a local path, so its redacted form is itself — is recorded
// only as the before's `git remote -v` printed it, and never reaches an
// engine git's argv: the read through it passes it in env.
func TestRunGraph_AnInsteadOfCannotRedirectAGuardedRead(t *testing.T) {
	for _, viaInclude := range []bool{false, true} {
		for _, credentialed := range []bool{false, true} {
			name := "in .git/config"
			if viaInclude {
				name = "in an included file"
			}
			name += map[bool]string{false: "/to an empty repository", true: "/to a URL with a credential"}[credentialed]
			t.Run(name, func(t *testing.T) {
				isolateRunHome(t)
				dir := remoteRepo(t)
				origin := originPath(t, dir)
				decoy, recorded := filepath.Join(t.TempDir(), "decoy.git"), ""
				if credentialed {
					decoy, recorded = credentialDecoy, credentialDecoyRedacted
				} else {
					mustGit(t, filepath.Dir(decoy), "init", "-q", "--bare", "-b", "main", decoy)
					recorded = decoy
				}
				target := filepath.Join(dir, ".git", "config")
				if viaInclude {
					target = filepath.Join(t.TempDir(), "included.config")
					appendTo(t, target, "")
					mustGit(t, dir, "config", "include.path", target)
				}
				run := guardedRealRun(t, dir, func() {
					appendTo(t, target, "[url \""+decoy+"\"]\n\tinsteadOf = "+origin+"\n[omg]\n\ttoken = "+configToken+"\n")
				})
				if got := mustGit(t, dir, "remote", "get-url", "origin"); got != decoy {
					t.Fatalf("the insteadOf did not take: origin reads as %s", got)
				}
				assertGuardedPushFailed(t, run.err)

				leaks := []string{configToken, origin}
				if credentialed {
					leaks = append(leaks, decoyToken, credentialDecoy)
				} else {
					leaks = append(leaks, decoy)
				}
				for _, leak := range leaks {
					if strings.Contains(run.argv, leak) {
						t.Errorf("a git the engine started carries %q on its argv", leak)
					}
				}
				never := []string{configToken}
				if credentialed {
					never = append(never, decoyToken, credentialDecoy)
				}
				assertNotWritten(t, run, never...)

				leg := remoteRunSnapshot(t, soleRunID(t)).RemoteChanges.Legs[0]
				if got := leg.Before.Remotes.Configured; len(got) != 1 || got[0].FetchURL != origin || got[0].PushURL != origin {
					t.Errorf("before remotes = %+v, want origin as git remote -v printed it", got)
				}
				if got := leg.After.Remotes.Configured; len(got) != 1 || got[0].FetchURL != recorded || got[0].PushURL != recorded {
					t.Errorf("after remotes = %+v, want origin's redirected URL redacted, %q", got, recorded)
				}
				urlEntry := runstate.RemoteChangeEntry{Kind: runstate.RemoteURLChanged, Remote: "origin",
					OldFetchURL: origin, NewFetchURL: recorded, OldPushURL: origin, NewPushURL: recorded,
					Attribution: runstate.AttributionNotAttributable}
				if !slices.Contains(leg.Changes.Entries, urlEntry) {
					t.Errorf("entries = %+v, want %+v", leg.Changes.Entries, urlEntry)
				}
			})
		}
	}
}

// A readable repository remote named omg-before-snapshot, with the config
// changed, no longer stops the before URL being read: outside the
// repository its remotes are not read.
func TestRunGraph_ARepositoryRemoteOfTheEnginesNameWithAChangedConfig(t *testing.T) {
	isolateRunHome(t)
	dir := remoteRepo(t)
	mustGit(t, dir, "remote", "add", "omg-before-snapshot", mustGit(t, dir, "remote", "get-url", "origin"))
	run := guardedRealRun(t, dir, func() {
		appendTo(t, filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
	})
	assertGuardedPushFailed(t, run.err)
	if strings.Contains(run.out, "not guarded (not compared): branches") {
		t.Errorf("the branches read as not guarded:\n%s", run.out)
	}
	assertNotWritten(t, run, configToken)
}

// A relative-path origin is read correctly, inside the repository and, with
// the config changed, from outside it; the absolute path it was resolved to
// stays in memory.
func TestRunGraph_ARelativePathOriginIsReadByAGuardedAfter(t *testing.T) {
	for _, changed := range []bool{false, true} {
		name := "config unchanged"
		if changed {
			name = "config changed"
		}
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			dir := remoteRepo(t)
			origin := mustGit(t, dir, "remote", "get-url", "origin")
			rel, relErr := filepath.Rel(dir, origin)
			if relErr != nil {
				t.Fatal(relErr)
			}
			mustGit(t, dir, "remote", "set-url", "origin", rel)
			run := guardedRealRun(t, dir, func() {
				if changed {
					appendTo(t, filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
				}
			})
			assertGuardedPushFailed(t, run.err)
			if strings.Contains(run.out, "not guarded") {
				t.Errorf("every part was compared; nothing may read as not guarded:\n%s", run.out)
			}
			assertNotWritten(t, run, configToken, origin)
		})
	}
}

// The guard fails closed only where the leg's commit check could not be
// trusted: a new branch at a commit the check did not attribute is forbidden
// when the repository's config changed or could not be read by the after, or
// `git remote -v` failed, or the check's own git read failed — and allowed,
// as always, when none of that happened. The record keeps what the check
// answered, not_attributable, and an unguarded run is untouched by any of it.
func TestRunGraph_AGuardedLegFailsClosedOnAnUnprovenCommitCheck(t *testing.T) {
	const foreign = "branch_created origin/feat/x at 444444444444"
	breakConfig := func(obs *worktree.FakeRemoteObserver) { obs.SetRepoConfig("changed", nil) }
	// byNameUnseen: unguarded, the after reads by name, so with the remotes
	// unlisted it sees no branch at all.
	for _, tc := range []struct {
		name         string
		during       func(obs *worktree.FakeRemoteObserver)
		forbidden    bool
		byNameUnseen bool
	}{
		{"config unchanged", func(*worktree.FakeRemoteObserver) {}, false, false},
		{"config changed", breakConfig, true, false},
		{"config unreadable", func(obs *worktree.FakeRemoteObserver) { obs.SetRepoConfig("", errors.New("exit status 128")) }, true, false},
		{"git remote -v fails", func(obs *worktree.FakeRemoteObserver) { obs.SetRemotes(nil, errors.New("exit status 128")) }, true, true},
		{"the commit check fails", func(obs *worktree.FakeRemoteObserver) { obs.SetAttributionErr(errors.New("exit status 128")) }, true, false},
	} {
		for _, guarded := range []bool{true, false} {
			name := tc.name + map[bool]string{true: "/guarded", false: "/unguarded"}[guarded]
			t.Run(name, func(t *testing.T) {
				isolateRunHome(t)
				obs, prs := guardedRemote(t, nil)
				change := func(o *worktree.FakeRemoteObserver, p *remote.FakePRLister) {
					pushBranch("feat/x", remoteHumaSHA)(o, p)
					tc.during(o)
				}
				var extra []string
				if guarded {
					extra = []string{"--forbid-remote-changes"}
				}
				out, err := runWithRemoteChange(t, obs, prs, change, extra...)
				if tc.forbidden && guarded {
					if got := exitCodeForError(err); got != 6 {
						t.Fatalf("err = %v (exit %d), want exit 6", err, got)
					}
					want := foreign + " (" + labelNotAttributable + "; " + unprovenAttribution + ")"
					if !strings.Contains(err.Error(), want) {
						t.Errorf("err = %v, want it to name %q", err, want)
					}
				} else if err != nil {
					t.Fatalf("err = %v, want nil", err)
				}
				if !guarded && tc.byNameUnseen {
					return
				}
				if want := "  " + foreign + " — " + labelNotAttributable + "\n"; !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
				found := false
				for _, e := range lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries {
					found = found || e.Kind == runstate.BranchCreated && e.Attribution == runstate.AttributionNotAttributable
				}
				if !found {
					t.Error("state.json lacks the not_attributable branch_created")
				}
			})
		}
	}
}
