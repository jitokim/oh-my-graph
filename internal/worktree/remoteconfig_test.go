package worktree

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// A guarded after reads the before URLs inside the repository only while the
// repository's own config is what it was at the before: a node that breaks
// that config, or rewrites it to send the read elsewhere, gets the read moved
// to a fresh empty directory, where only the global and env config apply.

// configSecret is a config value a node writes beside its change; it must
// reach no argv, and no output of the engine's.
const configSecret = "omg-config-token-9876"

// seenCmd is one git command the manager started: its directory, argv and env.
type seenCmd struct {
	dir  string
	args []string
	env  []string
}

// recordCommands sets m's seam hook to keep every command it starts.
func recordCommands(m *GitManager) func() []seenCmd {
	var mu sync.Mutex
	var seen []seenCmd
	m.observe = func(cmd *exec.Cmd) {
		mu.Lock()
		defer mu.Unlock()
		seen = append(seen, seenCmd{dir: cmd.Dir, args: slices.Clone(cmd.Args[1:]), env: slices.Clone(cmd.Env)})
	}
	return func() []seenCmd {
		mu.Lock()
		defer mu.Unlock()
		return slices.Clone(seen)
	}
}

// beforeURLReads is every command of seen that read through, or checked the
// name of, the env-defined before remote.
func beforeURLReads(seen []seenCmd) []seenCmd {
	var reads []seenCmd
	for _, c := range seen {
		if slices.Equal(c.args, lsRemoteHeadsArgs(beforeSnapshotRemote)) ||
			slices.Equal(c.args, []string{"config", "--get-regexp", beforeSnapshotRemoteKeys}) {
			reads = append(reads, c)
		}
	}
	return reads
}

// appendFile appends text to path.
func appendFile(t *testing.T, path, text string) {
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

// lane is the run's own worktree over f, on its branch omg/test-run/lane,
// acquired once.
func (f remoteFixture) lane(t *testing.T) string {
	t.Helper()
	lane, err := f.m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	return lane
}

// pushRunCommit commits on the run's own branch (f.lane) and pushes it to
// origin's repository as feat/run, returning the commit: the node's own
// push, which the real §5.1 check attributes.
func pushRunCommit(t *testing.T, f remoteFixture) string {
	t.Helper()
	lane := f.lane(t)
	sha := commitIn(t, lane, "node work")
	gitIn(t, lane, "push", "-q", f.origin, "HEAD:refs/heads/feat/run")
	return sha
}

// guardedLeg is what guardedConfigLeg returns: the leg's diff and after, the
// run's commit, why the leg's attribution was unavailable ("" when it was
// not), and whether a §5.1 check could not be read.
type guardedLeg struct {
	diff        runstate.RemoteDiff
	after       runstate.RemoteSnapshot
	runCommit   string
	unavailable string
	checkErr    bool
}

// guardedConfigLeg is a guarded leg over f whose node runs during, its diff
// attributed by the real §5.1 check against f's base, as the engine's is.
func guardedConfigLeg(t *testing.T, f remoteFixture, during func() string) guardedLeg {
	t.Helper()
	ctx := context.Background()
	before, pin := TakeGuardedRemoteSnapshot(ctx, f.m, ghRepoPRs(), nil)
	runCommit := during()
	after, _ := TakeGuardedRemoteSnapshot(ctx, f.m, ghRepoPRs(), pin)
	check, failed := GuardedRunCommitCheck(ctx, f.m, f.base)
	diff := DiffGuardedRemoteSnapshots(before, after, check)
	return guardedLeg{diff: diff, after: after, runCommit: runCommit,
		unavailable: pin.AttributionUnavailable(f.m, failed()), checkErr: failed()}
}

// hasRunCommitBranch reports whether diff holds feat/run's run_commit
// branch_created.
func hasRunCommitBranch(diff runstate.RemoteDiff, runCommit string) bool {
	return hasBranchCreated(diff, runCommit, runstate.AttributionRunCommit)
}

// hasBranchCreated reports whether diff holds feat/run's branch_created at
// runCommit, attributed attr.
func hasBranchCreated(diff runstate.RemoteDiff, runCommit string, attr runstate.RemoteAttribution) bool {
	want := runstate.RemoteChangeEntry{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/run",
		NewSHA: runCommit, Attribution: attr}
	return slices.Contains(diff.Entries, want)
}

// configBreakages are the three ways a node can make the repository's config
// unreadable: a garbage line, a remote whose url has no value, and no read
// permission. checkReads is whether the local §5.1 check still reads the
// repository after it: git rejects a bare url only when it reads remotes.
var configBreakages = []struct {
	name       string
	checkReads bool
	damage     func(t *testing.T, repo string)
}{
	{"garbage line", false, func(t *testing.T, repo string) {
		appendFile(t, filepath.Join(repo, ".git", "config"), "this is not config\n")
	}},
	{"remote with a bare url", true, func(t *testing.T, repo string) {
		appendFile(t, filepath.Join(repo, ".git", "config"), "[remote \"x\"]\n\turl\n")
	}},
	{"chmod 000", false, func(t *testing.T, repo string) {
		if os.Getuid() == 0 {
			t.Skip("root reads a mode 000 file")
		}
		config := filepath.Join(repo, ".git", "config")
		if err := os.Chmod(config, 0); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = os.Chmod(config, 0o644) })
	}},
}

// A node that pushes its own commit to a new branch and then breaks the
// repository's config does not hide the push: the before URL is read from
// outside the repository and the push is compared. The real §5.1 check reads
// the broken config too: where git still reads it the push is a run_commit;
// where it cannot, the check fails rather than answering, the entry is
// not_attributable and the leg's attribution unavailable, the config
// unreadable — what a guarded run reports its branches not guarded with.
func TestGuardedRemoteLeg_ABrokenRepoConfigCannotHideAPush(t *testing.T) {
	requireRealGit231(t)
	for _, tc := range configBreakages {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemoteFixture(t)
			seen := recordCommands(f.m)
			leg := guardedConfigLeg(t, f, func() string {
				sha := pushRunCommit(t, f)
				tc.damage(t, f.repo)
				return sha
			})
			want := runstate.AttributionNotAttributable
			if tc.checkReads {
				want = runstate.AttributionRunCommit
			}
			if !hasBranchCreated(leg.diff, leg.runCommit, want) {
				t.Fatalf("diff = %+v, want feat/run's %s branch_created", leg.diff, want)
			}
			wantUnavailable := runstate.AttributionConfigUnreadable
			if tc.checkReads {
				wantUnavailable = ""
			}
			if leg.unavailable != wantUnavailable || leg.checkErr == tc.checkReads {
				t.Errorf("unavailable = %q, check failed = %v; want %q, the check failed = %v",
					leg.unavailable, leg.checkErr, wantUnavailable, !tc.checkReads)
			}
			if !reflect.DeepEqual(leg.after.Branches.ReadThroughBeforeURL, []string{"origin"}) {
				t.Errorf("branches = %+v, want observed through origin's before URL", leg.after.Branches)
			}
			reads := beforeURLReads(seen())
			if len(reads) != 2 {
				t.Fatalf("before-remote commands = %+v, want the collision check and the ls-remote", reads)
			}
			for _, c := range reads {
				if c.dir == f.repo {
					t.Errorf("git %q ran in the repository whose config is broken", c.args)
				}
			}
		})
	}
}

// A node that redirects the original URL to another, empty, repository with
// url.<base>.insteadOf — in .git/config, or in a file the config already
// included at the before — does not send the read there: the before URL is
// read from outside the repository, and the push is still compared.
func TestGuardedRemoteLeg_AnInsteadOfCannotRedirectTheRead(t *testing.T) {
	requireRealGit231(t)
	for _, viaInclude := range []bool{false, true} {
		name := "in .git/config"
		if viaInclude {
			name = "in an included file"
		}
		t.Run(name, func(t *testing.T) {
			f := newRemoteFixture(t)
			decoy := initBareRemote(t)
			target := filepath.Join(f.repo, ".git", "config")
			if viaInclude {
				target = filepath.Join(t.TempDir(), "included.config")
				appendFile(t, target, "")
				gitIn(t, f.repo, "config", "include.path", target)
			}
			seen := recordCommands(f.m)
			leg := guardedConfigLeg(t, f, func() string {
				sha := pushRunCommit(t, f)
				appendFile(t, target, "[url \""+decoy+"\"]\n\tinsteadOf = "+f.origin+"\n[omg]\n\ttoken = "+configSecret+"\n")
				return sha
			})
			if got := gitIn(t, f.repo, "remote", "get-url", "origin"); got != decoy {
				t.Fatalf("the insteadOf did not take: origin reads as %s", got)
			}
			if !hasRunCommitBranch(leg.diff, leg.runCommit) {
				t.Fatalf("diff = %+v, want feat/run's run_commit branch_created", leg.diff)
			}
			if leg.unavailable != "" || leg.checkErr {
				t.Errorf("unavailable = %q, check failed = %v; want the check to run on the readable config", leg.unavailable, leg.checkErr)
			}
			if leg.after.Branches.Status != runstate.RemotePartObserved {
				t.Errorf("branches = %+v, want observed", leg.after.Branches)
			}
			for _, c := range beforeURLReads(seen()) {
				if c.dir == f.repo {
					t.Errorf("git %q ran in the repository whose config was rewritten", c.args)
				}
			}
			for _, c := range seen() {
				for _, a := range c.args {
					for _, leak := range []string{configSecret, f.origin, decoy} {
						if strings.Contains(a, leak) {
							t.Errorf("git %q carries %q on its argv", c.args, leak)
						}
					}
				}
			}
		})
	}
}

// With the config as it was at the before, the after reads inside the
// repository exactly as before: a repository credential helper still works.
func TestGuardedRemoteLeg_AnUnchangedConfigIsReadInsideTheRepository(t *testing.T) {
	requireRealGit231(t)
	f := newRemoteFixture(t)
	seen := recordCommands(f.m)
	leg := guardedConfigLeg(t, f, func() string { return pushRunCommit(t, f) })
	if !hasRunCommitBranch(leg.diff, leg.runCommit) {
		t.Fatalf("diff = %+v, want feat/run's run_commit branch_created", leg.diff)
	}
	if leg.unavailable != "" {
		t.Errorf("an unchanged config left the leg's attribution unavailable: %q", leg.unavailable)
	}
	reads := beforeURLReads(seen())
	if len(reads) != 2 {
		t.Fatalf("before-remote commands = %+v, want the collision check and the ls-remote", reads)
	}
	for _, c := range reads {
		if c.dir != f.repo {
			t.Errorf("git %q ran in %s, want the repository %s", c.args, c.dir, f.repo)
		}
		if lastValue(c.env, "GIT_CEILING_DIRECTORIES") != "" {
			t.Errorf("git %q got a GIT_CEILING_DIRECTORIES inside the repository", c.args)
		}
	}
}

// With the config changed, every git of the read runs in a fresh directory
// outside the repository, removed afterwards, with the repository-locating
// variables gone and the ceiling at the directory's parent; the reads that
// stay in the repository keep them.
func TestGuardedRemoteLeg_AChangedConfigIsReadFromAnEmptyDirectoryOutsideTheRepository(t *testing.T) {
	requireRealGit231(t)
	f := newRemoteFixture(t)
	f.lane(t)
	gitDir := filepath.Join(f.repo, ".git")
	f.m.environ = envWithoutSSHChoice("GIT_DIR="+gitDir, "GIT_WORK_TREE="+f.repo, "GIT_COMMON_DIR="+gitDir,
		"GIT_INDEX_FILE="+filepath.Join(gitDir, "index"), "GIT_OBJECT_DIRECTORY="+filepath.Join(gitDir, "objects"),
		"GIT_ALTERNATE_OBJECT_DIRECTORIES="+filepath.Join(gitDir, "objects"))
	seen := recordCommands(f.m)
	leg := guardedConfigLeg(t, f, func() string {
		sha := pushRunCommit(t, f)
		gitIn(t, f.repo, "config", "omg.changed", "yes")
		return sha
	})
	if !hasRunCommitBranch(leg.diff, leg.runCommit) {
		t.Fatalf("diff = %+v, want feat/run's run_commit branch_created", leg.diff)
	}
	var outside []seenCmd
	for _, c := range seen() {
		if c.dir != f.repo {
			outside = append(outside, c)
			continue
		}
		if lastValue(c.env, "GIT_DIR") != gitDir {
			t.Errorf("git %q inside the repository lost GIT_DIR", c.args)
		}
	}
	if reads := beforeURLReads(outside); len(reads) != 2 {
		t.Fatalf("before-remote commands outside the repository = %+v, want the collision check and the ls-remote", reads)
	}
	for _, c := range outside {
		if c.dir == "" || strings.HasPrefix(c.dir, f.repo+string(filepath.Separator)) {
			t.Errorf("git %q ran in %q, want a directory outside the repository", c.args, c.dir)
		}
		for _, key := range repoLocatingEnv {
			if hasKey(c.env, key) {
				t.Errorf("git %q outside the repository got %s", c.args, key)
			}
		}
		if got := lastValue(c.env, "GIT_CEILING_DIRECTORIES"); got != filepath.Dir(c.dir) {
			t.Errorf("git %q: GIT_CEILING_DIRECTORIES = %q, want %q", c.args, got, filepath.Dir(c.dir))
		}
		assertScrubbedEnv(t, c.env)
		if got := lastValue(c.env, "GIT_TERMINAL_PROMPT"); got != "0" {
			t.Errorf("git %q: GIT_TERMINAL_PROMPT = %q, want 0", c.args, got)
		}
		if _, err := os.Stat(c.dir); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the directory %s outside the repository was left behind: %v", c.dir, err)
		}
	}
}

// A readable repository remote named omg-before-snapshot is refused while the
// read runs inside the repository, but outside it the repository's remotes
// are not read, so with the config changed the before URL is read.
func TestGuardedRemoteLeg_ARepositoryRemoteOfTheEnginesNameDoesNotCollideOutsideTheRepository(t *testing.T) {
	requireRealGit231(t)
	f := newRemoteFixture(t)
	gitIn(t, f.repo, "remote", "add", beforeSnapshotRemote, f.origin)
	leg := guardedConfigLeg(t, f, func() string {
		sha := pushRunCommit(t, f)
		gitIn(t, f.repo, "config", "omg.changed", "yes")
		return sha
	})
	if leg.after.Branches.Status != runstate.RemotePartObserved {
		t.Fatalf("branches = %+v, want observed", leg.after.Branches)
	}
	if !hasRunCommitBranch(leg.diff, leg.runCommit) {
		t.Errorf("diff = %+v, want feat/run's run_commit branch_created", leg.diff)
	}
}

// A relative-path origin (../origin.git) is resolved against the repository
// root at the before, so the after reads the same repository inside the
// repository and from outside it; the resolved path stays in memory.
func TestGuardedRemoteLeg_ARelativePathOriginIsReadInsideAndOutsideTheRepository(t *testing.T) {
	requireRealGit231(t)
	for _, changed := range []bool{false, true} {
		name := "config unchanged"
		if changed {
			name = "config changed"
		}
		t.Run(name, func(t *testing.T) {
			f := newRemoteFixture(t)
			rel, err := filepath.Rel(f.repo, f.origin)
			if err != nil || filepath.IsAbs(rel) {
				t.Fatalf("no relative path from %s to %s: %v", f.repo, f.origin, err)
			}
			gitIn(t, f.repo, "remote", "set-url", "origin", rel)
			before, pin := TakeGuardedRemoteSnapshot(context.Background(), f.m, ghRepoPRs(), nil)
			if got := before.Remotes.Configured[0].FetchURL; got != rel {
				t.Errorf("recorded fetch URL = %q, want the relative %q as git printed it", got, rel)
			}
			if got := pin.urls["origin"]; resolvePath(got) != resolvePath(f.origin) {
				t.Errorf("before URL = %q, want %s", got, f.origin)
			}
			runCommit := pushRunCommit(t, f)
			if changed {
				gitIn(t, f.repo, "config", "omg.changed", "yes")
			}
			after, _ := TakeGuardedRemoteSnapshot(context.Background(), f.m, ghRepoPRs(), pin)
			diff := DiffGuardedRemoteSnapshots(before, after, RunCommitCheck(context.Background(), f.m, f.base))
			if !hasRunCommitBranch(diff, runCommit) {
				t.Errorf("diff = %+v, want feat/run's run_commit branch_created", diff)
			}
		})
	}
}

// A remote whose relative local-path URL the before could not resolve is
// read through nothing else — not by name — and leaves the branches part
// unavailable, saying why and never quoting the URL.
func TestTakeGuardedRemoteSnapshot_AnUnresolvedLocalPathMakesBranchesUnavailable(t *testing.T) {
	ctx := context.Background()
	const rel = "../omg-secret-origin.git"
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{{Name: "origin", FetchURL: rel, PushURL: rel}}, nil)
	obs.SetResolvedURL(rel, "", errors.New(unresolvedLocalURL+": git rev-parse --show-toplevel: exit status 128"))
	_, pin := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), nil)
	after, _ := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), pin)

	if after.Branches.Status != runstate.RemotePartUnavailable ||
		!strings.HasPrefix(after.Branches.Reason, "ls-remote origin: "+unresolvedLocalURL) {
		t.Fatalf("branches = %+v, want unavailable naming origin and why", after.Branches)
	}
	if strings.Contains(after.Branches.Reason, "omg-secret-origin") {
		t.Errorf("reason %q quotes the URL", after.Branches.Reason)
	}
	if calls := obs.URLCalls(); len(calls) != 0 {
		t.Errorf("URL reads = %v, want none", calls)
	}
}

// The fake's scripted digest drives where the after reads: unchanged inside,
// changed or unreadable outside, and a failed `git remote -v` outside too.
// Only an unreadable config makes the leg's attribution unavailable with the
// config unreadable; a commit check whose own read fails, the config still
// readable, makes it unavailable with the check's read failed.
func TestTakeGuardedRemoteSnapshot_ReadsOutsideTheRepositoryOnlyWhenItsConfigMoved(t *testing.T) {
	for _, tc := range []struct {
		name        string
		change      func(obs *FakeRemoteObserver)
		outside     bool
		unavailable string
		// checkFailed is the reason when the commit check's own read fails.
		checkFailed string
	}{
		{"unchanged", func(*FakeRemoteObserver) {}, false, "", runstate.AttributionCheckReadFailed},
		{"changed", func(obs *FakeRemoteObserver) { obs.SetRepoConfig("other", nil) }, true, "", runstate.AttributionCheckReadFailed},
		{"unreadable", func(obs *FakeRemoteObserver) { obs.SetRepoConfig("", errors.New("exit status 128")) }, true,
			runstate.AttributionConfigUnreadable, runstate.AttributionConfigUnreadable},
		{"remote -v fails", func(obs *FakeRemoteObserver) { obs.SetRemotes(nil, errors.New("exit status 128")) }, true, "",
			runstate.AttributionCheckReadFailed},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			obs := NewFakeRemoteObserver()
			obs.SetRemotes([]Remote{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}, nil)
			obs.SetRepoConfig("pinned", nil)
			_, pin := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), nil)
			tc.change(obs)
			TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), pin)
			if got := len(obs.OutsideURLCalls()) == 1; got != tc.outside {
				t.Errorf("outside reads = %v, want outside = %v", obs.OutsideURLCalls(), tc.outside)
			}
			if got := pin.AttributionUnavailable(obs, false); got != tc.unavailable {
				t.Errorf("AttributionUnavailable = %q, want %q", got, tc.unavailable)
			}
			obs.SetAttributionErr(errors.New("signal: killed"))
			check, failed := GuardedRunCommitCheck(ctx, obs, strings.Repeat("d", 40))
			if check(strings.Repeat("e", 40)) || !failed() {
				t.Fatal("the scripted check read did not fail")
			}
			if got := pin.AttributionUnavailable(obs, failed()); got != tc.checkFailed {
				t.Errorf("AttributionUnavailable after a failed check read = %q, want %q", got, tc.checkFailed)
			}
			if calls := obs.URLCalls(); len(calls) != 1 {
				t.Errorf("URL reads = %v, want one", calls)
			}
		})
	}
}

// The digest covers the local and worktree entries and the files they
// include, and none of the global ones; a config git cannot read fails
// without quoting a value it printed before failing.
func TestGitManager_RepoConfigFingerprint(t *testing.T) {
	global := filepath.Join(t.TempDir(), "gitconfig")
	appendFile(t, global, "[omg]\n\tglobal = "+configSecret+"\n")
	t.Setenv("GIT_CONFIG_GLOBAL", global)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	m, repo := newTestManager(t)
	ctx := context.Background()
	fingerprint := func() string {
		t.Helper()
		fp, err := m.RepoConfigFingerprint(ctx)
		if err != nil {
			t.Fatalf("RepoConfigFingerprint: %v", err)
		}
		if len(fp) != 64 || strings.Contains(fp, configSecret) {
			t.Fatalf("fingerprint = %q, want a SHA-256 in hex", fp)
		}
		return fp
	}
	first := fingerprint()
	if fingerprint() != first {
		t.Fatal("the same config gave two fingerprints")
	}
	appendFile(t, global, "[omg]\n\tother = 1\n")
	if fingerprint() != first {
		t.Error("a global change moved the repository's fingerprint")
	}
	included := filepath.Join(t.TempDir(), "included.config")
	appendFile(t, included, "")
	gitIn(t, repo, "config", "include.path", included)
	withInclude := fingerprint()
	if withInclude == first {
		t.Error("a local change left the fingerprint as it was")
	}
	appendFile(t, included, "[omg]\n\tincluded = 1\n")
	if fingerprint() == withInclude {
		t.Error("a change in an included file left the fingerprint as it was")
	}
	appendFile(t, filepath.Join(repo, ".git", "config"), "this is not config\n")
	_, err := m.RepoConfigFingerprint(ctx)
	if err == nil {
		t.Fatal("a broken config gave a fingerprint")
	}
	if strings.Contains(err.Error(), configSecret) {
		t.Errorf("error %q quotes a config value", err)
	}
}

func TestIsRelativeLocalPath(t *testing.T) {
	for url, want := range map[string]bool{
		"../origin.git":               true,
		"origin.git":                  true,
		"./a:b":                       true,
		"/abs/origin.git":             false,
		"file:///abs/origin.git":      false,
		"https://example.com/x.git":   false,
		"git@example.com:o/r.git":     false,
		"ext::sh -c env>/tmp/x":       false,
		"":                            false,
		"ssh://git@example.com/x.git": false,
	} {
		if got := isRelativeLocalPath(url); got != want {
			t.Errorf("isRelativeLocalPath(%q) = %v, want %v", url, got, want)
		}
	}
}

// isAttributionCommand reports whether args is one of CommitAttributable's
// git commands.
func isAttributionCommand(args []string) bool {
	switch {
	case len(args) == 0:
		return false
	case args[0] == "merge-base", args[0] == "for-each-ref":
		return true
	default:
		return len(args) > 2 && args[0] == "rev-parse" && args[1] == "--quiet" && args[2] == "--verify"
	}
}

// The §5.1 check's commands — rev-parse --verify, merge-base and
// for-each-ref — and no other git the engine starts turn off replace refs and
// grafts, on top of the scrub.
func TestGitManager_OnlyTheAttributionCommandsTurnOffReplaceAndGrafts(t *testing.T) {
	requireRealGit231(t)
	f := newRemoteFixture(t)
	f.m.environ = func() []string {
		return append(os.Environ(),
			"ANTHROPIC_API_KEY=sk-live-secret",
			"ANTHROPIC_AUTH_TOKEN=tok-secret",
			"OPENAI_API_KEY=sk-openai-secret",
			"CODEX_API_KEY=sk-codex-secret",
		)
	}
	seen := recordCommands(f.m)
	leg := guardedConfigLeg(t, f, func() string {
		sha := pushRunCommit(t, f)
		gitIn(t, f.repo, "config", "omg.changed", "yes")
		return sha
	})
	if !hasRunCommitBranch(leg.diff, leg.runCommit) {
		t.Fatalf("diff = %+v, want feat/run's run_commit branch_created", leg.diff)
	}
	attribution := map[string]int{}
	for _, c := range seen() {
		assertScrubbedEnv(t, c.env)
		on := isAttributionCommand(c.args)
		if on {
			attribution[c.args[0]]++
		}
		for _, kv := range []string{"GIT_NO_REPLACE_OBJECTS=1", "GIT_GRAFT_FILE=" + os.DevNull} {
			k, v, _ := strings.Cut(kv, "=")
			switch {
			case on && lastValue(c.env, k) != v:
				t.Errorf("git %q: %s = %q, want %q", c.args, k, lastValue(c.env, k), v)
			case !on && hasKey(c.env, k):
				t.Errorf("git %q is not an attribution command and got %s", c.args, k)
			}
		}
	}
	for _, cmd := range []string{"rev-parse", "merge-base", "for-each-ref"} {
		if attribution[cmd] == 0 {
			t.Errorf("no attribution %s ran; ran %v", cmd, attribution)
		}
	}
}

// A replace ref or a graft that makes the base's parent the node's own
// commit does not hide that commit from the §5.1 check.
func TestGitManager_CommitAttributableIgnoresReplaceRefsAndGrafts(t *testing.T) {
	for _, name := range []string{"replace", "grafts"} {
		t.Run(name, func(t *testing.T) {
			f := newRemoteFixture(t)
			run := pushRunCommit(t, f)
			if name == "replace" {
				fake := gitIn(t, f.repo, "commit-tree", f.base+"^{tree}", "-p", run, "-m", "fake")
				gitIn(t, f.repo, "replace", "-f", f.base, fake)
			} else {
				appendFile(t, filepath.Join(f.repo, ".git", "info", "grafts"), f.base+" "+run+"\n")
			}
			// The rewrite took: to a plain git, the run's commit is now in
			// the base's history (gitIn fails the test on exit 1).
			gitIn(t, f.repo, "merge-base", "--is-ancestor", run, f.base)
			ok, err := f.m.CommitAttributable(context.Background(), run, f.base)
			if !ok || err != nil {
				t.Errorf("CommitAttributable = %v, %v; want the run's commit attributed", ok, err)
			}
		})
	}
}

// The shallow file in the common git dir is pinned at the before: unchanged —
// absent both times, or shallow from the start — the attribution stands; one
// written during the leg, or changed, makes it unavailable.
func TestGuardedBefore_AShallowBoundaryChangedMakesAttributionUnavailable(t *testing.T) {
	for _, tc := range []struct {
		name          string
		start, during string
		want          string
	}{
		{"absent", "", "", ""},
		{"shallow from the start", "base", "", ""},
		{"written during the leg", "", "run", runstate.AttributionShallowChanged},
		{"changed during the leg", "base", "run", runstate.AttributionShallowChanged},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemoteFixture(t)
			shallow := filepath.Join(f.repo, ".git", "shallow")
			if path, err := f.m.ShallowFile(context.Background()); err != nil || resolvePath(path) != resolvePath(shallow) {
				t.Fatalf("ShallowFile = %q, %v; want %s", path, err, shallow)
			}
			if tc.start != "" {
				appendFile(t, shallow, f.base+"\n")
			}
			leg := guardedConfigLeg(t, f, func() string {
				sha := pushRunCommit(t, f)
				if tc.during != "" {
					lane := f.lane(t)
					appendFile(t, shallow, commitIn(t, lane, "child")+"\n")
				}
				return sha
			})
			if leg.unavailable != tc.want {
				t.Errorf("unavailable = %q, want %q", leg.unavailable, tc.want)
			}
		})
	}
}

// NoteAttributionUnavailable adds its reason for the branches and
// pull-request parts the leg observed, and leaves the entries as they were.
func TestNoteAttributionUnavailable_NamesTheObservedRunBranchParts(t *testing.T) {
	entry := runstate.RemoteChangeEntry{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/x",
		NewSHA: strings.Repeat("a", 40), Attribution: runstate.AttributionNotAttributable}
	c := runstate.RemoteChanges{Legs: []runstate.RemoteLeg{{Changes: &runstate.RemoteDiff{
		Observed: []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches},
		Entries:  []runstate.RemoteChangeEntry{entry},
		NotCompared: []runstate.RemoteNotCompared{
			{Part: runstate.RemotePartPullRequests, Reason: "gh not logged in"},
		},
	}}}}
	NoteAttributionUnavailable(&c, "")
	if got := len(c.Legs[0].Changes.NotCompared); got != 1 {
		t.Fatalf("an empty reason added not-compared items: %d", got)
	}
	NoteAttributionUnavailable(&c, runstate.AttributionConfigUnreadable)
	d := c.Legs[0].Changes
	want := []runstate.RemoteNotCompared{
		{Part: runstate.RemotePartPullRequests, Reason: "gh not logged in"},
		{Part: runstate.RemotePartBranches, Reason: runstate.AttributionConfigUnreadable},
	}
	if !reflect.DeepEqual(d.NotCompared, want) {
		t.Errorf("not compared = %+v, want %+v", d.NotCompared, want)
	}
	if !reflect.DeepEqual(d.Entries, []runstate.RemoteChangeEntry{entry}) {
		t.Errorf("entries = %+v, want them unchanged", d.Entries)
	}
}
