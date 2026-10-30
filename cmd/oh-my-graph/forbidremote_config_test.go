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
// pushes its own commit to a new branch and then rewrites that config to send
// the read to another repository gets the read moved outside the repository,
// and the push still fails the run with exit 6. One that makes the config
// unreadable, or moves the shallow boundary, leaves the §5.1 check unable to
// attribute the push: the run is not failed for it (only an attributable
// entry fails a run), and the end line names the branches and pull requests
// not guarded, with why.

// The reasons the end line names a part not guarded with, spelled out as an
// operator reads them, so a change to runstate's constants shows here.
const (
	configUnreadableReason = "attribution unavailable: repository config unreadable"
	checkReadFailedReason  = "attribution unavailable: attribution check read failed"
	shallowChangedReason   = "attribution unavailable: shallow boundary changed"
)

// configToken is a config value the node writes beside its change; it must
// reach no file of the run, and no output.
const configToken = "omg-config-token-9876"

// appendTo appends text to path.
func appendTo(t *testing.T, path, text string) {
	t.Helper()
	if err := appendFile(path, text); err != nil {
		t.Fatal(err)
	}
}

// appendFile is appendTo returning its error, for a node's before, where a
// test may not be failed.
func appendFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(text)
	return err
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
// repository at originPath. It runs in a node's before, so it returns its
// error.
func commitAndPushFromLane(spec runner.NodeInvocation, originPath string) error {
	if spec.Cwd == "" {
		return errors.New("the node ran without its engine worktree")
	}
	if err := commitFile(spec.Cwd, "work.md"); err != nil {
		return err
	}
	_, err := gitOutput(spec.Cwd, "push", "-q", originPath, "HEAD:refs/heads/feat/x")
	return err
}

// originPath is the absolute path of dir's origin, a local bare repository.
func originPath(t *testing.T, dir string) string {
	t.Helper()
	origin, err := originPathOf(dir)
	if err != nil {
		t.Fatal(err)
	}
	return origin
}

// originPathOf is originPath returning its error, for a node's before.
func originPathOf(dir string) (string, error) {
	origin, err := gitOutput(dir, "remote", "get-url", "origin")
	if err != nil {
		return "", err
	}
	if !filepath.IsAbs(origin) {
		origin = filepath.Join(dir, origin)
	}
	return origin, nil
}

// gitArgvLog is a git on PATH that appends the argv of every git started
// while it is not paused to a log, and to a second log the same argv after
// the directory it ran in and a tab, then runs the real git: the engine's own
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
	script := "#!/bin/sh\n[ -e '" + l.dir + "/paused' ] || { printf '%s\\n' \"$*\" >> '" + l.dir + "/argv'; printf '%s\\t%s\\n' \"$(pwd -P)\" \"$*\" >> '" + l.dir + "/cwd'; }\nexec '" + real + "' \"$@\"\n"
	if err := os.WriteFile(filepath.Join(l.dir, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", l.dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	l.pause(t)
	return l
}

func (l gitArgvLog) pause(t *testing.T) {
	t.Helper()
	if err := l.setPaused(true); err != nil {
		t.Fatal(err)
	}
}

func (l gitArgvLog) resume(t *testing.T) {
	t.Helper()
	if err := l.setPaused(false); err != nil {
		t.Fatal(err)
	}
}

// setPaused is pause or resume returning its error, for a node's before.
func (l gitArgvLog) setPaused(paused bool) error {
	if paused {
		return os.WriteFile(filepath.Join(l.dir, "paused"), nil, 0o644)
	}
	return os.Remove(filepath.Join(l.dir, "paused"))
}

// read is every argv logged, one per line.
func (l gitArgvLog) read(t *testing.T) string {
	t.Helper()
	return l.readLog(t, "argv")
}

// readLog is the log named name, empty if nothing was logged.
func (l gitArgvLog) readLog(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(l.dir, name))
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

// realRun is what guardedRealRun saw: stdout, stderr, the argv of every git
// the engine started, the same with the directory each ran in, and the run's
// error.
type realRun struct {
	out, stderr, argv, cwd string
	err                    error
}

// guardedRealRun runs a one-node guarded graph against dir, the production
// GitManager reading it and attributing through the real §5.1 check, whose
// node commits on its run branch, pushes the commit to feat/x on origin,
// then runs change. change runs on the node's goroutine, so it returns its
// setup error, and the test fails at it before anything about the run is
// asserted.
func guardedRealRun(t *testing.T, dir string, change func() error) realRun {
	t.Helper()
	useRealRemoteObserver(t)
	origin := originPath(t, dir)
	argv := logGitArgv(t)
	nodeRunner := &hookRunner{FakeRunner: passingWork(), before: func(spec runner.NodeInvocation) (err error) {
		if err := argv.setPaused(true); err != nil {
			return err
		}
		defer func() {
			if resumeErr := argv.setPaused(false); err == nil {
				err = resumeErr
			}
		}()
		if err := commitAndPushFromLane(spec, origin); err != nil {
			return err
		}
		return change()
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
	nodeRunner.mustSetUp(t)
	argv.pause(t)
	run.argv = argv.read(t)
	run.cwd = argv.readLog(t, "cwd")
	if !strings.Contains(run.argv, "ls-remote --heads omg-before-snapshot") {
		t.Fatalf("the argv log missed the engine's before-URL read:\n%s", run.argv)
	}
	return run
}

// assertGuardedPushFailed fails unless err is exit 6 naming feat/x's
// branch_created as a run_commit, and the record agrees.
func assertGuardedPushFailed(t *testing.T, err error) {
	t.Helper()
	if got := exitCodeForError(err); got != 6 {
		t.Fatalf("err = %v (exit %d), want exit 6", err, got)
	}
	if want := "branch_created origin/feat/x"; !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "("+labelRunCommit+")") {
		t.Errorf("err = %v, want it to name the %s as a run commit", err, want)
	}
	if !hasBranchCreated(t, "feat/x", runstate.AttributionRunCommit) {
		t.Error("state.json lacks feat/x's run_commit branch_created")
	}
}

// hasBranchCreated reports whether the run's last leg recorded a
// branch_created of branch, attributed attr.
func hasBranchCreated(t *testing.T, branch string, attr runstate.RemoteAttribution) bool {
	t.Helper()
	for _, e := range lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).Entries {
		if e.Kind == runstate.BranchCreated && e.Branch == branch && e.Attribution == attr {
			return true
		}
	}
	return false
}

// assertAttributionUnavailable fails unless the guarded run passed (exit 0)
// with feat/x's branch_created recorded not_attributable, the end line names
// the branches and pull-request parts not guarded with reason and never as
// guarded, and the record keeps reason on both parts.
func assertAttributionUnavailable(t *testing.T, run realRun, reason string) {
	t.Helper()
	if got := exitCodeForError(run.err); got != 0 {
		t.Fatalf("err = %v (exit %d), want exit 0", run.err, got)
	}
	if !hasBranchCreated(t, "feat/x", runstate.AttributionNotAttributable) {
		t.Error("state.json lacks feat/x's not_attributable branch_created")
	}
	var line string
	for _, l := range strings.Split(run.out, "\n") {
		if strings.HasPrefix(l, "--forbid-remote-changes: not guarded: ") {
			line = l
		}
	}
	notGuarded, guarded, _ := strings.Cut(line, "; guarded: ")
	for _, part := range []string{"branches", "pull_requests"} {
		if want := part + " (" + reason + ")"; !strings.Contains(notGuarded, want) {
			t.Errorf("the end line lacks %q:\n%s", want, run.out)
		}
		if strings.Contains(guarded, part) {
			t.Errorf("the end line claims %s as guarded:\n%s", part, run.out)
		}
	}
	nc := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t))).NotCompared
	for _, part := range []runstate.RemotePart{runstate.RemotePartBranches, runstate.RemotePartPullRequests} {
		if !slices.Contains(nc, runstate.RemoteNotCompared{Part: part, Reason: reason}) {
			t.Errorf("state.json's not compared = %+v, want %s with %q", nc, part, reason)
		}
	}
}

// assertBeforeURLReadOutside fails unless every ls-remote through the before
// URL the engine started ran in a directory outside the repository at dir.
func assertBeforeURLReadOutside(t *testing.T, run realRun, dir string) {
	t.Helper()
	repo, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}
	reads := 0
	for _, l := range strings.Split(run.cwd, "\n") {
		cwd, argv, _ := strings.Cut(l, "\t")
		if !strings.HasPrefix(argv, "ls-remote --heads omg-before-snapshot") {
			continue
		}
		reads++
		if cwd == repo || strings.HasPrefix(cwd, repo+string(filepath.Separator)) {
			t.Errorf("git %s ran in %s, inside the repository whose config changed", argv, cwd)
		}
	}
	if reads == 0 {
		t.Errorf("no before-URL read in the cwd log:\n%s", run.cwd)
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

// Breaking the repository's config after the push. A remote whose url has no
// value fails only `git remote -v`: the config stays readable, the after reads
// origin through its before URL from outside the repository, the real §5.1
// check attributes the push, and the run exits 6. A garbage line or no read
// permission fails the check itself: the push is recorded not_attributable,
// the run passes (exit 0), and the end line names the branches and pull
// requests not guarded, the config unreadable.
func TestRunGraph_ABrokenRepoConfigCannotHideAGuardedPush(t *testing.T) {
	for _, tc := range []struct {
		name     string
		readable bool
		apply    func(t *testing.T, config string) error
	}{
		{"garbage line", false, func(_ *testing.T, config string) error { return appendFile(config, "this is not config\n") }},
		{"remote with a bare url", true, func(_ *testing.T, config string) error { return appendFile(config, "[remote \"x\"]\n\turl\n") }},
		{"chmod 000", false, func(t *testing.T, config string) error {
			t.Cleanup(func() { _ = os.Chmod(config, 0o644) })
			return os.Chmod(config, 0)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.name == "chmod 000" && os.Geteuid() == 0 {
				t.Skip("root reads a mode 000 file")
			}
			isolateRunHome(t)
			dir := remoteRepo(t)
			origin := originPath(t, dir)
			run := guardedRealRun(t, dir, func() error { return tc.apply(t, filepath.Join(dir, ".git", "config")) })
			if tc.readable {
				assertGuardedPushFailed(t, run.err)
				if strings.Contains(run.out, configUnreadableReason) {
					t.Errorf("a readable config read as unreadable:\n%s", run.out)
				}
				assertBeforeURLReadOutside(t, run, dir)
			} else {
				assertAttributionUnavailable(t, run, configUnreadableReason)
			}
			if strings.Contains(run.argv, origin) {
				t.Errorf("a git the engine started carries the before URL %q on its argv", origin)
			}
		})
	}
}

// An unreadable config with someone else's branch pushed beside the node's
// own: neither is attributed, and the run passes, reporting both.
func TestRunGraph_AnUnreadableConfigWithSomeoneElsesBranchIsReportedOnly(t *testing.T) {
	isolateRunHome(t)
	dir := remoteRepo(t)
	origin := originPath(t, dir)
	other := filepath.Join(t.TempDir(), "other")
	run := guardedRealRun(t, dir, func() error {
		if _, err := gitOutput(filepath.Dir(other), "clone", "-q", origin, other); err != nil {
			return err
		}
		if err := commitFile(other, "theirs.md"); err != nil {
			return err
		}
		if _, err := gitOutput(other, "push", "-q", "origin", "HEAD:refs/heads/feat/theirs"); err != nil {
			return err
		}
		return appendFile(filepath.Join(dir, ".git", "config"), "this is not config\n")
	})
	assertAttributionUnavailable(t, run, configUnreadableReason)
	if !hasBranchCreated(t, "feat/theirs", runstate.AttributionNotAttributable) {
		t.Error("state.json lacks feat/theirs's not_attributable branch_created")
	}
	if want := "branch_created origin/feat/theirs"; !strings.Contains(run.out, want) {
		t.Errorf("stdout lacks %q:\n%s", want, run.out)
	}
}

// ancestryOf is what the guardedRealRun node's change needs to rewrite the
// ancestry the §5.1 check reads: the run's base, dir's HEAD, and the node's
// own commit, origin's feat/x. It runs in that change, so it returns its
// error.
func ancestryOf(dir string) (base, own string, err error) {
	if base, err = gitOutput(dir, "rev-parse", "HEAD"); err != nil {
		return "", "", err
	}
	origin, err := originPathOf(dir)
	if err != nil {
		return "", "", err
	}
	own, err = gitOutput(origin, "rev-parse", "refs/heads/feat/x")
	return base, own, err
}

// A node that pushes its own commit and then makes the base read as its
// child — a replace ref for the base whose parent is its commit, or the same
// in .git/info/grafts — does not get the push past the check: it runs with
// both turned off, attributes the push, and the run exits 6.
func TestRunGraph_ARewrittenAncestryCannotHideAGuardedPush(t *testing.T) {
	for _, tc := range []struct {
		name    string
		rewrite func(dir, base, own string) error
	}{
		{"git replace", func(dir, base, own string) error {
			fake, err := gitOutput(dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
				"commit-tree", base+"^{tree}", "-p", own, "-m", "fake")
			if err != nil {
				return err
			}
			_, err = gitOutput(dir, "replace", "-f", base, fake)
			return err
		}},
		{".git/info/grafts", func(dir, base, own string) error {
			return appendFile(filepath.Join(dir, ".git", "info", "grafts"), base+" "+own+"\n")
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			dir := remoteRepo(t)
			run := guardedRealRun(t, dir, func() error {
				base, own, err := ancestryOf(dir)
				if err != nil {
					return err
				}
				if err := tc.rewrite(dir, base, own); err != nil {
					return err
				}
				// The rewrite took: to a plain git, the node's commit is now
				// in the base's history (the setup fails on exit 1).
				_, err = gitOutput(dir, "merge-base", "--is-ancestor", own, base)
				return err
			})
			assertGuardedPushFailed(t, run.err)
		})
	}
}

// A node that pushes its own commit, moves its run branch to a child of it
// and writes that child into .git/shallow hides the commit's ancestry from
// the check, and no variable turns the shallow file off. The guarded leg's
// before pinned the file's digest, so the moved boundary is seen: the run
// passes, and the end line names the branches and pull requests not guarded,
// the shallow boundary changed.
func TestRunGraph_AShallowBoundaryWrittenDuringTheLegIsNotGuarded(t *testing.T) {
	isolateRunHome(t)
	dir := remoteRepo(t)
	run := guardedRealRun(t, dir, func() error {
		_, own, err := ancestryOf(dir)
		if err != nil {
			return err
		}
		child, err := gitOutput(dir, "-c", "user.name=t", "-c", "user.email=t@example.com", "-c", "commit.gpgsign=false",
			"commit-tree", own+"^{tree}", "-p", own, "-m", "child")
		if err != nil {
			return err
		}
		lane, err := gitOutput(dir, "for-each-ref", "--format=%(refname)", "refs/heads/omg/")
		if err != nil {
			return err
		}
		if _, err := gitOutput(dir, "update-ref", lane, child); err != nil {
			return err
		}
		return appendFile(filepath.Join(dir, ".git", "shallow"), child+"\n")
	})
	assertAttributionUnavailable(t, run, shallowChangedReason)
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
				run := guardedRealRun(t, dir, func() error {
					return appendFile(target, "[url \""+decoy+"\"]\n\tinsteadOf = "+origin+"\n[omg]\n\ttoken = "+configToken+"\n")
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
	run := guardedRealRun(t, dir, func() error {
		return appendFile(filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
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
			run := guardedRealRun(t, dir, func() error {
				if !changed {
					return nil
				}
				return appendFile(filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
			})
			assertGuardedPushFailed(t, run.err)
			if strings.Contains(run.out, "not guarded") {
				t.Errorf("every part was compared; nothing may read as not guarded:\n%s", run.out)
			}
			assertNotWritten(t, run, configToken, origin)
		})
	}
}

// A guarded leg is failed only by an attributable entry, whatever happened to
// the repository's config: a new branch at a commit the check did not
// attribute passes the run (exit 0), changed, unreadable or with `git remote
// -v` failing. Where the check could not run — the config unreadable at the
// after, a check's own git read failed with the config still readable, or the
// shallow boundary moved — the end line and the record name the branches and
// pull requests not guarded with why; everywhere else, and on an unguarded
// run, no such reason is given.
func TestRunGraph_AGuardedLegNeverFailsOnAnUnattributedPushWhateverTheConfig(t *testing.T) {
	const foreign = "branch_created origin/feat/x at 444444444444"
	// byNameUnseen: unguarded, the after reads by name, so with the remotes
	// unlisted it sees no branch at all.
	for _, tc := range []struct {
		name         string
		during       func(obs *worktree.FakeRemoteObserver)
		reason       string
		byNameUnseen bool
	}{
		{"config unchanged", func(*worktree.FakeRemoteObserver) {}, "", false},
		{"config changed", func(obs *worktree.FakeRemoteObserver) { obs.SetRepoConfig("changed", nil) }, "", false},
		{"config unreadable", func(obs *worktree.FakeRemoteObserver) { obs.SetRepoConfig("", errors.New("exit status 128")) },
			configUnreadableReason, false},
		{"git remote -v fails", func(obs *worktree.FakeRemoteObserver) { obs.SetRemotes(nil, errors.New("exit status 128")) }, "", true},
		{"the commit check fails", func(obs *worktree.FakeRemoteObserver) { obs.SetAttributionErr(errors.New("signal: killed")) },
			checkReadFailedReason, false},
		{"the commit check and the config read fail", func(obs *worktree.FakeRemoteObserver) {
			obs.SetAttributionErr(errors.New("exit status 128"))
			obs.SetRepoConfig("", errors.New("exit status 128"))
		}, configUnreadableReason, false},
		{"the shallow boundary moved", func(obs *worktree.FakeRemoteObserver) { obs.SetShallow("moved", nil) },
			shallowChangedReason, false},
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
				if err != nil {
					t.Fatalf("err = %v (exit %d), want nil", err, exitCodeForError(err))
				}
				changes := lastLegChanges(t, remoteRunSnapshot(t, soleRunID(t)))
				for _, part := range []runstate.RemotePart{runstate.RemotePartBranches, runstate.RemotePartPullRequests} {
					for _, reason := range []string{configUnreadableReason, checkReadFailedReason, shallowChangedReason} {
						noted := slices.Contains(changes.NotCompared, runstate.RemoteNotCompared{Part: part, Reason: reason})
						if want := guarded && reason == tc.reason; noted != want {
							t.Errorf("%s not compared with %q = %v, want %v: %+v", part, reason, noted, want, changes.NotCompared)
						}
						if want := guarded && reason == tc.reason; strings.Contains(out, string(part)+" ("+reason+")") != want {
							t.Errorf("stdout names %s not guarded with %q: %v, want %v:\n%s", part, reason, !want, want, out)
						}
					}
				}
				if !guarded && tc.byNameUnseen {
					return
				}
				if want := "  " + foreign + " — " + labelNotAttributable + "\n"; !strings.Contains(out, want) {
					t.Errorf("stdout lacks %q:\n%s", want, out)
				}
				if !hasBranchCreated(t, "feat/x", runstate.AttributionNotAttributable) {
					t.Error("state.json lacks the not_attributable branch_created")
				}
			})
		}
	}
}
