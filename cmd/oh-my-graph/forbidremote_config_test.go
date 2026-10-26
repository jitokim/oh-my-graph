package main

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
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

// guardedRealRun runs a one-node guarded graph against dir, the production
// GitManager reading it, whose node commits, pushes the commit to feat/x on
// origin, then runs change. It returns stdout, stderr and the run's error.
func guardedRealRun(t *testing.T, dir string, change func()) (string, string, error) {
	t.Helper()
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
		change()
	}}
	path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
	var out string
	stderr, err := captureStderr(t, func() error {
		var runErr error
		out = captureStdout(t, func() {
			runErr = runGraphWith([]string{path, "--forbid-remote-changes"}, nodeRunner, browser.NewFakeOpener(), os.Stdout)
		})
		return runErr
	})
	return out, stderr, err
}

// assertGuardedPushFailed fails unless err is exit 6 naming feat/x's
// run_commit branch_created.
func assertGuardedPushFailed(t *testing.T, err error) {
	t.Helper()
	if got := exitCodeForError(err); got != 6 {
		t.Fatalf("err = %v (exit %d), want exit 6", err, got)
	}
	if !strings.Contains(err.Error(), "branch_created origin/feat/x") {
		t.Errorf("err = %v, want it to name the branch_created of origin/feat/x", err)
	}
}

// assertNotWritten fails if any of leaks is in a file of the run — state.json
// and events.jsonl among them — or in the run's output or error.
func assertNotWritten(t *testing.T, streams string, err error, leaks ...string) {
	t.Helper()
	if err != nil {
		streams += err.Error()
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
// exit 0 and the branches read as not guarded.
func TestRunGraph_ABrokenRepoConfigCannotHideAGuardedPush(t *testing.T) {
	for _, tc := range []struct {
		name  string
		apply func(t *testing.T, config string)
	}{
		{"garbage line", func(t *testing.T, config string) { appendTo(t, config, "this is not config\n") }},
		{"remote with a bare url", func(t *testing.T, config string) { appendTo(t, config, "[remote \"x\"]\n\turl\n") }},
		{"chmod 000", func(t *testing.T, config string) {
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
			out, stderr, err := guardedRealRun(t, dir, func() { tc.apply(t, filepath.Join(dir, ".git", "config")) })
			assertGuardedPushFailed(t, err)
			if strings.Contains(out, "not guarded (not compared): branches") {
				t.Errorf("the branches read as not guarded:\n%s", out)
			}
			assertNotWritten(t, out+stderr, err, configToken)
		})
	}
}

// Redirecting origin's URL to another, empty, repository with
// url.<base>.insteadOf — in .git/config, or in a file .git/config already
// included — does not send the after's read there.
func TestRunGraph_AnInsteadOfCannotRedirectAGuardedRead(t *testing.T) {
	for _, viaInclude := range []bool{false, true} {
		name := "in .git/config"
		if viaInclude {
			name = "in an included file"
		}
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			dir := remoteRepo(t)
			origin := mustGit(t, dir, "remote", "get-url", "origin")
			decoy := filepath.Join(t.TempDir(), "decoy.git")
			mustGit(t, filepath.Dir(decoy), "init", "-q", "--bare", "-b", "main", decoy)
			target := filepath.Join(dir, ".git", "config")
			if viaInclude {
				target = filepath.Join(t.TempDir(), "included.config")
				appendTo(t, target, "")
				mustGit(t, dir, "config", "include.path", target)
			}
			out, stderr, err := guardedRealRun(t, dir, func() {
				appendTo(t, target, "[url \""+decoy+"\"]\n\tinsteadOf = "+origin+"\n[omg]\n\ttoken = "+configToken+"\n")
			})
			if got := mustGit(t, dir, "remote", "get-url", "origin"); got != decoy {
				t.Fatalf("the insteadOf did not take: origin reads as %s", got)
			}
			assertGuardedPushFailed(t, err)
			assertNotWritten(t, out+stderr, err, configToken)
		})
	}
}

// A readable repository remote named omg-before-snapshot, with the config
// changed, no longer stops the before URL being read: outside the
// repository its remotes are not read.
func TestRunGraph_ARepositoryRemoteOfTheEnginesNameWithAChangedConfig(t *testing.T) {
	isolateRunHome(t)
	dir := remoteRepo(t)
	mustGit(t, dir, "remote", "add", "omg-before-snapshot", mustGit(t, dir, "remote", "get-url", "origin"))
	out, stderr, err := guardedRealRun(t, dir, func() {
		appendTo(t, filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
	})
	assertGuardedPushFailed(t, err)
	if strings.Contains(out, "not guarded (not compared): branches") {
		t.Errorf("the branches read as not guarded:\n%s", out)
	}
	assertNotWritten(t, out+stderr, err, configToken)
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
			out, stderr, err := guardedRealRun(t, dir, func() {
				if changed {
					appendTo(t, filepath.Join(dir, ".git", "config"), "[omg]\n\ttoken = "+configToken+"\n")
				}
			})
			assertGuardedPushFailed(t, err)
			if strings.Contains(out, "not guarded") {
				t.Errorf("every part was compared; nothing may read as not guarded:\n%s", out)
			}
			assertNotWritten(t, out+stderr, err, configToken, origin)
		})
	}
}
