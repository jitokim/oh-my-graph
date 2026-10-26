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
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/childenv"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// The guarded leg's read through a before fetch URL (ADR 0046 §5.1): the
// URL is raw and may carry a credential, so beside reading what the by-name
// read reads, it must never put the URL in an error.

// testCredentialURL is a fetch URL with a credential in it, aimed at a local
// port nothing listens on, so a read of it fails at once without leaving the
// machine.
const testCredentialURL = "https://user:omg-test-token-0123@127.0.0.1:1/x.git"

func TestGitManager_LsRemoteHeadsURLListsBranchesAndShas(t *testing.T) {
	m, repo := newTestManager(t)
	bare := initBareRemote(t)
	gitIn(t, repo, "remote", "add", "origin", bare)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "feat/x")
	feat := commitIn(t, repo, "feat")
	gitIn(t, repo, "push", "-q", "origin", "feat/x")
	// The URL is read, not a remote: removing the remote changes nothing.
	gitIn(t, repo, "remote", "remove", "origin")

	want := []RemoteHead{{Branch: "feat/x", SHA: feat}, {Branch: "main", SHA: base}}
	for _, url := range []string{bare, "file://" + bare} {
		heads, err := m.LsRemoteHeadsURL(context.Background(), url)
		if err != nil {
			t.Fatalf("LsRemoteHeadsURL(%s): %v", url, err)
		}
		if !reflect.DeepEqual(heads, want) {
			t.Fatalf("LsRemoteHeadsURL(%s) = %+v, want %+v", url, heads, want)
		}
	}
}

func TestGitManager_LsRemoteHeadsURLRefusesAnOptionShapedURL(t *testing.T) {
	m, _ := newTestManager(t)
	for _, url := range []string{"", "--upload-pack=touch pwned", "-x"} {
		_, err := m.LsRemoteHeadsURL(context.Background(), url)
		if err == nil {
			t.Errorf("LsRemoteHeadsURL(%q) ran; want it refused", url)
			continue
		}
		if url != "" && strings.Contains(err.Error(), url) {
			t.Errorf("the refusal quotes the URL: %v", err)
		}
	}
}

// A URL that cannot be read fails — never "no branches" — and the error
// names neither the URL nor its credential, though git's own output does.
func TestGitManager_LsRemoteHeadsURLOfABadURLFailsWithoutEchoingIt(t *testing.T) {
	isolateGitConfig(t, "")
	m, _ := newTestManager(t)
	missing := filepath.Join(t.TempDir(), "omg-secret-path", "no-such-remote.git")
	for _, url := range []string{missing, testCredentialURL} {
		_, err := m.LsRemoteHeadsURL(context.Background(), url)
		if err == nil {
			t.Fatalf("ls-remote of %s must fail, not report no branches", url)
		}
		for _, leak := range []string{url, "omg-secret-path", "omg-test-token-0123", "127.0.0.1"} {
			if strings.Contains(err.Error(), leak) {
				t.Errorf("error %q contains %q", err, leak)
			}
		}
		if !strings.Contains(err.Error(), "<before fetch URL>") {
			t.Errorf("error %q does not say what was read", err)
		}
	}
}

// TestGitManager_LsRemoteHeadsURLChildSeesScrubbedNoPromptEnv: the URL read
// runs on the by-name read's env — scrubbed, no prompt, ssh in BatchMode —
// proven from inside git's own child, an ext:: transport that dumps the
// environment it inherited.
func TestGitManager_LsRemoteHeadsURLChildSeesScrubbedNoPromptEnv(t *testing.T) {
	isolateGitConfig(t, "")
	m, repo := newTestManager(t)
	m.environ = envWithoutSSHChoice(
		"ANTHROPIC_API_KEY=sk-live-secret",
		"ANTHROPIC_AUTH_TOKEN=tok-secret",
		"OPENAI_API_KEY=sk-openai-secret",
		"CODEX_API_KEY=sk-codex-secret",
		"GIT_TERMINAL_PROMPT=1",
		"GIT_ASKPASS=/usr/bin/ksshaskpass",
	)
	dump := filepath.Join(t.TempDir(), "env.txt")
	gitIn(t, repo, "config", "protocol.ext.allow", "always")

	_, err := m.LsRemoteHeadsURL(context.Background(), "ext::sh -c env>"+dump)
	if err == nil {
		t.Fatal("the probe transport speaks no protocol; ls-remote should have failed")
	}
	if strings.Contains(err.Error(), dump) {
		t.Errorf("error %q quotes the URL", err)
	}
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the probe never ran: %v", err)
	}
	env := strings.Split(strings.TrimSpace(string(data)), "\n")
	assertScrubbedEnv(t, env)
	if got := lastValue(env, "GIT_TERMINAL_PROMPT"); got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q in ls-remote's child, want 0", got)
	}
	assertNoAskpass(t, env, "ls-remote's child")
	if got := lastValue(env, "GIT_SSH_COMMAND"); got != "ssh -o BatchMode=yes" {
		t.Errorf("GIT_SSH_COMMAND = %q in ls-remote's child, want ssh -o BatchMode=yes", got)
	}
}

// The URL read is under the manager's timeout, as the by-name read is, and
// says so without the URL.
func TestGitManager_LsRemoteHeadsURLTimesOut(t *testing.T) {
	m, repo := newTestManager(t)
	gitIn(t, repo, "config", "protocol.ext.allow", "always")
	m.timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := m.LsRemoteHeadsURL(context.Background(), "ext::sh -c sleep% 5")
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("LsRemoteHeadsURL past the manager's timeout: err = %v, want it to name the timeout", err)
	}
	if strings.Contains(err.Error(), "sleep") {
		t.Errorf("error %q quotes the URL", err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("the timeout did not cut the command short: it took %s", took)
	}
}

// guardedLeg is remoteFixture.leg on a guarded leg: the after read through
// the before's fetch URLs.
func (f remoteFixture) guardedLeg(t *testing.T, during func()) (runstate.RemoteDiff, runstate.RemoteSnapshot) {
	t.Helper()
	ctx := context.Background()
	var rc runstate.RemoteChanges
	before, urls := TakeGuardedRemoteSnapshot(ctx, f.m, ghRepoPRs(), nil)
	rc.BeginLeg(before)
	during()
	after, _ := TakeGuardedRemoteSnapshot(ctx, f.m, ghRepoPRs(), urls)
	if !rc.EndLeg(after, RunCommitCheck(ctx, f.m, f.base)) {
		t.Fatal("EndLeg recorded nothing")
	}
	return *rc.Legs[0].Changes, after
}

// The QA case, against a real repository: the node pushes its own commit to
// a new branch, then removes or re-points origin. Read through the before's
// URL, the push is still a run_commit branch_created, beside the remote
// entry that reports what was done to origin. Re-pointed, the repository
// origin now names was never read, so its branches are still not compared.
func TestGuardedRemoteLeg_ARemovedOrRepointedRemoteCannotHideAPush(t *testing.T) {
	for _, tc := range []struct {
		name    string
		hide    func(t *testing.T, f remoteFixture)
		kind    runstate.RemoteChangeKind
		skipped []runstate.RemoteNotCompared
	}{
		{"remote removed", func(t *testing.T, f remoteFixture) { gitIn(t, f.repo, "remote", "remove", "origin") }, runstate.RemoteRemoved, nil},
		{"remote re-pointed", func(t *testing.T, f remoteFixture) {
			gitIn(t, f.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unreachable.git"))
		}, runstate.RemoteURLChanged, []runstate.RemoteNotCompared{{Part: runstate.RemotePartBranches, Remote: "origin",
			Reason: "fetch URL changed: the new repository has no before refs to compare"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRemoteFixture(t)
			var runCommit string
			diff, after := f.guardedLeg(t, func() {
				lane, err := f.m.Acquire(context.Background(), "lane")
				if err != nil {
					t.Fatalf("Acquire: %v", err)
				}
				runCommit = commitIn(t, lane, "node work")
				gitIn(t, lane, "push", "-q", "origin", "HEAD:feat/run")
				tc.hide(t, f)
			})
			if len(diff.Entries) != 2 || diff.Entries[0].Kind != tc.kind {
				t.Fatalf("entries = %+v, want %s then the branch_created", diff.Entries, tc.kind)
			}
			want := runstate.RemoteChangeEntry{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/run",
				NewSHA: runCommit, Attribution: runstate.AttributionRunCommit}
			if !reflect.DeepEqual(diff.Entries[1], want) {
				t.Errorf("entry = %+v\nwant    %+v", diff.Entries[1], want)
			}
			if len(diff.NotCompared) != len(tc.skipped) || (len(tc.skipped) != 0 && !reflect.DeepEqual(diff.NotCompared, tc.skipped)) {
				t.Errorf("not_compared = %+v, want %+v", diff.NotCompared, tc.skipped)
			}
			if !reflect.DeepEqual(after.Branches.ReadThroughBeforeURL, []string{"origin"}) {
				t.Errorf("read_through_before_url = %v, want [origin]", after.Branches.ReadThroughBeforeURL)
			}
		})
	}
}

// An unguarded leg reads by name as it always did: the removed remote's
// refs are not compared, and nothing reads through a URL.
func TestRemoteLeg_UnguardedStillReadsByName(t *testing.T) {
	f := newRemoteFixture(t)
	diff := f.leg(t, func() {
		gitIn(t, f.repo, "push", "-q", "origin", "main:refs/heads/pushed")
		gitIn(t, f.repo, "remote", "remove", "origin")
	})
	if len(diff.Entries) != 1 || diff.Entries[0].Kind != runstate.RemoteRemoved {
		t.Errorf("entries = %+v, want only remote_removed", diff.Entries)
	}
	if len(diff.NotCompared) != 1 || diff.NotCompared[0].Remote != "origin" {
		t.Errorf("not_compared = %+v, want origin's refs", diff.NotCompared)
	}
}

// The before hands back each remote's raw fetch URL, credentials included,
// while the snapshot records the redacted one; the after reads every remote
// the before listed through its URL — gone or re-pointed — and one added
// since by name.
func TestTakeGuardedRemoteSnapshot_ReadsTheBeforesRemotesThroughTheirURLs(t *testing.T) {
	ctx := context.Background()
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{
		{Name: "gone", FetchURL: "https://example.com/g.git", PushURL: "https://example.com/g.git"},
		{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL},
	}, nil)
	obs.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: strings.Repeat("a", 40)}}, nil)
	obs.SetHeads("gone", []RemoteHead{{Branch: "g", SHA: strings.Repeat("b", 40)}}, nil)

	before, urls := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), nil)
	if want := (FetchURLs{"gone": "https://example.com/g.git", "origin": testCredentialURL}); !reflect.DeepEqual(urls, want) {
		t.Fatalf("before URLs = %v, want %v", urls, want)
	}
	if got := before.Remotes.Configured[1].FetchURL; got != "https://***@127.0.0.1:1/x.git" {
		t.Errorf("recorded fetch URL = %q, want it redacted", got)
	}
	if calls := obs.URLCalls(); len(calls) != 0 {
		t.Errorf("the before read through URLs %v; it reads by name", calls)
	}

	obs.SetRemotes([]Remote{
		{Name: "added", FetchURL: "https://example.com/a.git", PushURL: "https://example.com/a.git"},
		{Name: "origin", FetchURL: "https://example.com/elsewhere.git", PushURL: "https://example.com/elsewhere.git"},
	}, nil)
	obs.SetHeads("added", []RemoteHead{{Branch: "new", SHA: strings.Repeat("c", 40)}}, nil)
	after, _ := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), urls)

	if calls := obs.URLCalls(); !reflect.DeepEqual(calls, []string{"https://example.com/g.git", testCredentialURL}) {
		t.Errorf("URL reads = %v, want gone's and origin's before URLs", calls)
	}
	wantRefs := []runstate.RemoteBranchRef{
		{Remote: "added", Branch: "new", SHA: strings.Repeat("c", 40)},
		{Remote: "gone", Branch: "g", SHA: strings.Repeat("b", 40)},
		{Remote: "origin", Branch: "main", SHA: strings.Repeat("a", 40)},
	}
	if after.Branches.Status != runstate.RemotePartObserved || !reflect.DeepEqual(after.Branches.Refs, wantRefs) {
		t.Errorf("branches = %+v, want refs %+v", after.Branches, wantRefs)
	}
	if !reflect.DeepEqual(after.Branches.ReadThroughBeforeURL, []string{"gone", "origin"}) {
		t.Errorf("read_through_before_url = %v, want [gone origin]", after.Branches.ReadThroughBeforeURL)
	}
}

// A before URL that cannot be read makes the after's branches part
// unavailable, naming the remote and never the URL.
func TestTakeGuardedRemoteSnapshot_AnUnreadableBeforeURLMakesBranchesUnavailable(t *testing.T) {
	ctx := context.Background()
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}, nil)
	_, urls := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), nil)

	obs.SetURLHeads(testCredentialURL, nil, errors.New("git ls-remote --heads <before fetch URL>: exit status 128"))
	after, _ := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), urls)
	if after.Branches.Status != runstate.RemotePartUnavailable || !strings.HasPrefix(after.Branches.Reason, "ls-remote origin: ") {
		t.Fatalf("branches = %+v, want unavailable naming origin", after.Branches)
	}
	if strings.Contains(after.Branches.Reason, "omg-test-token-0123") || strings.Contains(after.Branches.Reason, "127.0.0.1") {
		t.Errorf("reason %q quotes the URL", after.Branches.Reason)
	}
	if after.Branches.ReadThroughBeforeURL != nil {
		t.Errorf("an unavailable part names remotes read through: %v", after.Branches.ReadThroughBeforeURL)
	}
}

// A node that pushes its own commit to a new branch and then breaks `git
// remote -v` (a corrupted .git/config) does not hide the push from a guarded
// after: the remotes part is unavailable as always, but the before's remotes
// are still read through their before URLs, and their branches compared.
func TestTakeGuardedRemoteSnapshot_ComparesBranchesThroughTheBeforeURLsWhenTheRemotesCannotBeListed(t *testing.T) {
	ctx := context.Background()
	runCommit, base, main := strings.Repeat("c", 40), strings.Repeat("d", 40), strings.Repeat("a", 40)
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}, nil)
	obs.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: main}}, nil)
	obs.SetRunCommit(runCommit)
	before, urls := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), nil)

	obs.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: main}, {Branch: "feat/run", SHA: runCommit}}, nil)
	obs.SetRemotes(nil, errors.New("fatal: bad config line 1 in file .git/config"))
	after, _ := TakeGuardedRemoteSnapshot(ctx, obs, ghRepoPRs(), urls)

	if after.Remotes.Status != runstate.RemotePartUnavailable || after.Remotes.Reason != "fatal: bad config line 1 in file .git/config" {
		t.Errorf("remotes = %+v, want unavailable with git's reason", after.Remotes)
	}
	if calls := obs.URLCalls(); !reflect.DeepEqual(calls, []string{testCredentialURL}) {
		t.Errorf("URL reads = %v, want origin's before URL", calls)
	}
	if after.Branches.Status != runstate.RemotePartObserved || !reflect.DeepEqual(after.Branches.ReadThroughBeforeURL, []string{"origin"}) {
		t.Fatalf("branches = %+v, want observed, read through origin's before URL", after.Branches)
	}

	diff := runstate.DiffRemoteSnapshots(before, after, RunCommitCheck(ctx, obs, base))
	want := []runstate.RemoteChangeEntry{{Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/run",
		NewSHA: runCommit, Attribution: runstate.AttributionRunCommit}}
	if !reflect.DeepEqual(diff.Entries, want) {
		t.Errorf("entries = %+v\nwant      %+v", diff.Entries, want)
	}
	if !slices.Contains(diff.Observed, runstate.RemotePartBranches) || slices.Contains(diff.Observed, runstate.RemotePartRemotes) {
		t.Errorf("observed = %v, want branches and not remotes", diff.Observed)
	}
	if len(diff.NotCompared) != 1 || diff.NotCompared[0].Part != runstate.RemotePartRemotes {
		t.Errorf("not_compared = %+v, want only the remotes part", diff.NotCompared)
	}
}

// Unguarded, the same failure leaves the branches part unavailable, as it
// always did: nothing is read through a URL, and nothing is compared.
func TestTakeRemoteSnapshot_UnguardedBranchesAreUnavailableWhenTheRemotesCannotBeListed(t *testing.T) {
	ctx := context.Background()
	runCommit, main := strings.Repeat("c", 40), strings.Repeat("a", 40)
	obs := NewFakeRemoteObserver()
	obs.SetRemotes([]Remote{{Name: "origin", FetchURL: testCredentialURL, PushURL: testCredentialURL}}, nil)
	obs.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: main}}, nil)
	obs.SetRunCommit(runCommit)
	before := TakeRemoteSnapshot(ctx, obs, ghRepoPRs())

	obs.SetHeads("origin", []RemoteHead{{Branch: "main", SHA: main}, {Branch: "feat/run", SHA: runCommit}}, nil)
	obs.SetRemotes(nil, errors.New("fatal: bad config line 1 in file .git/config"))
	after := TakeRemoteSnapshot(ctx, obs, ghRepoPRs())

	want := runstate.BranchesSnapshot{Status: runstate.RemotePartUnavailable,
		Reason: "the remotes part is unavailable: fatal: bad config line 1 in file .git/config"}
	if !reflect.DeepEqual(after.Branches, want) {
		t.Errorf("branches = %+v, want %+v", after.Branches, want)
	}
	if calls := obs.URLCalls(); len(calls) != 0 {
		t.Errorf("an unguarded snapshot read through URLs %v", calls)
	}
	diff := runstate.DiffRemoteSnapshots(before, after, RunCommitCheck(ctx, obs, strings.Repeat("d", 40)))
	if len(diff.Entries) != 0 || slices.Contains(diff.Observed, runstate.RemotePartBranches) || slices.Contains(diff.Observed, runstate.RemotePartRemotes) {
		t.Errorf("diff = %+v, want neither git part compared", diff)
	}
}

// fakeGitLog is what a fake git, from writeFakeGit, recorded of one
// invocation: its argv after "git", and the environment it received.
type fakeGitLog struct {
	args []string
	env  []string
}

// fakeHeadSHA is the one head the fake git's ls-remote prints.
var fakeHeadSHA = strings.Repeat("a", 40)

// writeFakeGit puts a fake git first on PATH — exec resolves "git" from the
// test process's PATH — that records each invocation's argv and environment
// under a log dir, answers `git version` with version, `config --get-regexp`
// with a key under remote.omg-before-snapshot if collide (otherwise exit 1,
// no match), any other `config` read with exit 1 (unset), and ls-remote with
// one head. It returns readFakeGitLogs's argument.
func writeFakeGit(t *testing.T, version string, collide bool) string {
	t.Helper()
	bin, logs := t.TempDir(), t.TempDir()
	regexp := "exit 1"
	if collide {
		regexp = "echo 'remote.omg-before-snapshot.url /elsewhere.git'; exit 0"
	}
	script := `#!/bin/sh
log="` + logs + `/$$"
printf '%s\n' "$@" > "$log.args"
/usr/bin/env > "$log.env"
case "$1 $2" in
"version "*) echo "git version ` + version + `" ;;
"config --get-regexp") ` + regexp + ` ;;
"config "*) exit 1 ;;
"ls-remote "*) printf '` + fakeHeadSHA + `\trefs/heads/main\n' ;;
*) exit 2 ;;
esac
`
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin)
	return logs
}

// readFakeGitLogs reads every invocation writeFakeGit's git recorded.
func readFakeGitLogs(t *testing.T, logs string) []fakeGitLog {
	t.Helper()
	argFiles, err := filepath.Glob(filepath.Join(logs, "*.args"))
	if err != nil {
		t.Fatal(err)
	}
	var calls []fakeGitLog
	for _, f := range argFiles {
		args, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		env, err := os.ReadFile(strings.TrimSuffix(f, ".args") + ".env")
		if err != nil {
			t.Fatal(err)
		}
		calls = append(calls, fakeGitLog{
			args: strings.Split(strings.TrimSuffix(string(args), "\n"), "\n"),
			env:  strings.Split(strings.TrimSpace(string(env)), "\n"),
		})
	}
	return calls
}

// countCalls counts the recorded invocations whose first argument is sub.
func countCalls(calls []fakeGitLog, sub string) int {
	n := 0
	for _, c := range calls {
		if len(c.args) > 0 && c.args[0] == sub {
			n++
		}
	}
	return n
}

// fakeGitManager is a manager on the fake git, its parent env carrying one
// GIT_CONFIG_COUNT entry of its own.
func fakeGitManager(t *testing.T) *GitManager {
	t.Helper()
	m := NewGitManager(t.TempDir(), t.TempDir(), "test-run")
	m.environ = func() []string {
		return []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.foo", "GIT_CONFIG_VALUE_0=bar"}
	}
	return m
}

const fakeCredentialURL = "https://user:s3cret@example.invalid/r.git"

// TestGitManager_LsRemoteHeadsURLIsOffTheArgvOfTheGitProcessTheEngineStarts:
// the before fetch URL reaches git only as GIT_CONFIG_VALUE_1 of the env-
// defined remote, after the parent's own GIT_CONFIG entry, and no argv of any
// git the engine started holds it. (git's own transport helper, which the
// fake never starts, is outside this.)
func TestGitManager_LsRemoteHeadsURLIsOffTheArgvOfTheGitProcessTheEngineStarts(t *testing.T) {
	logs := writeFakeGit(t, "2.43.0", false)
	m := fakeGitManager(t)

	heads, err := m.LsRemoteHeadsURL(context.Background(), fakeCredentialURL)
	if err != nil {
		t.Fatalf("LsRemoteHeadsURL: %v", err)
	}
	if want := []RemoteHead{{Branch: "main", SHA: fakeHeadSHA}}; !reflect.DeepEqual(heads, want) {
		t.Fatalf("heads = %+v, want %+v", heads, want)
	}

	calls := readFakeGitLogs(t, logs)
	var lsRemote *fakeGitLog
	for i, c := range calls {
		for _, a := range c.args {
			if strings.Contains(a, "s3cret") || strings.Contains(a, "example.invalid") {
				t.Errorf("git %q carries the URL on its argv", c.args)
			}
		}
		if c.args[0] == "ls-remote" {
			lsRemote = &calls[i]
			continue
		}
		for _, kv := range c.env {
			if strings.Contains(kv, "s3cret") || strings.Contains(kv, "example.invalid") {
				t.Errorf("git %q got the URL in its env as %q; only ls-remote's GIT_CONFIG_VALUE_1 may hold it", c.args, kv)
			}
		}
	}
	if lsRemote == nil || countCalls(calls, "ls-remote") != 1 {
		t.Fatalf("want exactly one ls-remote, got calls %+v", calls)
	}
	if want := []string{"ls-remote", "--heads", "omg-before-snapshot"}; !reflect.DeepEqual(lsRemote.args, want) {
		t.Errorf("ls-remote args = %q, want %q", lsRemote.args, want)
	}
	env := lsRemote.env
	holders := 0
	for _, kv := range env {
		if !strings.Contains(kv, "s3cret") && !strings.Contains(kv, "example.invalid") {
			continue
		}
		holders++
		if kv != "GIT_CONFIG_VALUE_1="+fakeCredentialURL {
			t.Errorf("the URL is in env as %q, want it only as GIT_CONFIG_VALUE_1", kv)
		}
	}
	if holders != 1 {
		t.Errorf("%d env entries hold the URL, want exactly GIT_CONFIG_VALUE_1", holders)
	}
	for key, want := range map[string]string{
		"GIT_CONFIG_COUNT":    "2",
		"GIT_CONFIG_KEY_0":    "core.foo",
		"GIT_CONFIG_VALUE_0":  "bar",
		"GIT_CONFIG_KEY_1":    "remote.omg-before-snapshot.url",
		"GIT_CONFIG_VALUE_1":  fakeCredentialURL,
		"GIT_TERMINAL_PROMPT": "0",
		"GIT_SSH_COMMAND":     "ssh -o BatchMode=yes",
	} {
		if got := lastValue(env, key); got != want {
			t.Errorf("%s = %q in ls-remote's env, want %q", key, got, want)
		}
	}
	assertNoAskpass(t, env, "ls-remote's env")
}

// The parent's GIT_CONFIG_COUNT=1 is replaced by ours, not left beside it: the
// exact env the engine hands its ls-remote child holds one GIT_CONFIG_COUNT,
// 2, with the parent's pair at index 0 and ours at 1.
func TestGitManager_LsRemoteHeadsURLReplacesTheInheritedConfigCount(t *testing.T) {
	m := fakeGitManager(t)
	n, err := inheritedConfigCount(childenv.Scrub(m.environ()))
	if err != nil || n != 1 {
		t.Fatalf("inheritedConfigCount = %d, %v; want 1", n, err)
	}
	env := append(append([]string(nil), noPromptEnv...), beforeSnapshotEnv(n, fakeCredentialURL)...)
	cmd := m.gitCmd(context.Background(), env, lsRemoteHeadsArgs(beforeSnapshotRemote)...)

	var counts []string
	for _, kv := range cmd.Env {
		if strings.HasPrefix(kv, "GIT_CONFIG_COUNT=") {
			counts = append(counts, kv)
		}
	}
	if want := []string{"GIT_CONFIG_COUNT=2"}; !reflect.DeepEqual(counts, want) {
		t.Errorf("GIT_CONFIG_COUNT entries = %q, want %q", counts, want)
	}
	for key, want := range map[string]string{
		"GIT_CONFIG_KEY_0":   "core.foo",
		"GIT_CONFIG_VALUE_0": "bar",
		"GIT_CONFIG_KEY_1":   "remote.omg-before-snapshot.url",
		"GIT_CONFIG_VALUE_1": fakeCredentialURL,
	} {
		if got := lastValue(cmd.Env, key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	if want := []string{"git", "ls-remote", "--heads", "omg-before-snapshot"}; !reflect.DeepEqual(cmd.Args, want) {
		t.Errorf("argv = %q, want %q", cmd.Args, want)
	}
}

// Before 2.31 git does not read GIT_CONFIG_COUNT, and a version that will not
// parse may be such a git: the read is refused, never moved back onto argv,
// and no ls-remote runs. The version is asked once per manager.
func TestGitManager_LsRemoteHeadsURLNeedsGit231(t *testing.T) {
	for _, tc := range []struct {
		version string
		ok      bool
		reason  string
	}{
		{"2.30.9", false, "git older than 2.31"},
		{"two", false, "cannot determine git version"},
		{"2.31.0", true, ""},
		{"2.43.0", true, ""},
	} {
		t.Run(tc.version, func(t *testing.T) {
			logs := writeFakeGit(t, tc.version, false)
			m := fakeGitManager(t)
			for range 2 {
				_, err := m.LsRemoteHeadsURL(context.Background(), fakeCredentialURL)
				if tc.ok && err != nil {
					t.Fatalf("LsRemoteHeadsURL on git %s: %v", tc.version, err)
				}
				if !tc.ok && (err == nil || !strings.Contains(err.Error(), tc.reason) ||
					!strings.Contains(err.Error(), noUnexposedRead)) {
					t.Fatalf("LsRemoteHeadsURL on git %s: err = %v, want %q and %q", tc.version, err, tc.reason, noUnexposedRead)
				}
				if err != nil && strings.Contains(err.Error(), "s3cret") {
					t.Errorf("error %q quotes the URL", err)
				}
			}
			calls := readFakeGitLogs(t, logs)
			if n := countCalls(calls, "version"); n != 1 {
				t.Errorf("git version asked %d times across two reads, want once", n)
			}
			want := 0
			if tc.ok {
				want = 2
			}
			if n := countCalls(calls, "ls-remote"); n != want {
				t.Errorf("%d ls-remote spawned, want %d", n, want)
			}
		})
	}
}

func TestParseGitVersion(t *testing.T) {
	for out, want := range map[string]gitVersion{
		"git version 2.43.0":                 {2, 43, true},
		"git version 2.39.3 (Apple Git-145)": {2, 39, true},
		"git version 2.45.0.windows.1\n":     {2, 45, true},
		"git version 3.0":                    {3, 0, true},
		"git version two":                    {},
		"git version 2":                      {},
		"hub version 2.43.0":                 {},
		"":                                   {},
	} {
		if got := parseGitVersion(out); got != want {
			t.Errorf("parseGitVersion(%q) = %+v, want %+v", out, got, want)
		}
	}
}

// A GIT_CONFIG_COUNT the next index cannot be read from refuses the read
// before any git runs.
func TestGitManager_LsRemoteHeadsURLRefusesABadInheritedConfigCount(t *testing.T) {
	logs := writeFakeGit(t, "2.43.0", false)
	for _, count := range []string{"x", "-1", "1.5", ""} {
		m := fakeGitManager(t)
		m.environ = func() []string { return []string{"PATH=" + os.Getenv("PATH"), "GIT_CONFIG_COUNT=" + count} }
		if _, err := m.LsRemoteHeadsURL(context.Background(), fakeCredentialURL); err == nil {
			t.Errorf("GIT_CONFIG_COUNT=%q: the read ran; want it refused", count)
		}
	}
	if calls := readFakeGitLogs(t, logs); len(calls) != 0 {
		t.Errorf("git ran %d times on a bad GIT_CONFIG_COUNT, want none", len(calls))
	}
}

// A config that already has remote.omg-before-snapshot keys would make the
// read the user's remote: it is refused, with no ls-remote and no URL in the
// error.
func TestGitManager_LsRemoteHeadsURLRefusesACollidingRemote(t *testing.T) {
	logs := writeFakeGit(t, "2.43.0", true)
	m := fakeGitManager(t)
	_, err := m.LsRemoteHeadsURL(context.Background(), fakeCredentialURL)
	if err == nil || !strings.Contains(err.Error(), "already has a remote named omg-before-snapshot") {
		t.Fatalf("err = %v, want the collision named", err)
	}
	for _, leak := range []string{fakeCredentialURL, "s3cret", "example.invalid", "/elsewhere.git"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("error %q contains %q", err, leak)
		}
	}
	if n := countCalls(readFakeGitLogs(t, logs), "ls-remote"); n != 0 {
		t.Errorf("%d ls-remote spawned past the collision, want none", n)
	}
}

// requireRealGit231 skips a test on a local git that has no GIT_CONFIG_COUNT.
func requireRealGit231(t *testing.T) {
	t.Helper()
	out, err := exec.Command("git", "version").Output()
	v := parseGitVersion(string(out))
	if err != nil || !v.ok || v.major < 2 || v.major == 2 && v.minor < configEnvMinor {
		t.Skipf("local git %q has no GIT_CONFIG_COUNT (needs 2.31)", strings.TrimSpace(string(out)))
	}
}

// Against real git: the env-defined remote reads a local bare origin's heads,
// beside a parent GIT_CONFIG entry, and a repository remote of the engine's
// name is refused rather than read.
func TestGitManager_LsRemoteHeadsURLReadsThroughTheEnvDefinedRemote(t *testing.T) {
	requireRealGit231(t)
	m, repo := newTestManager(t)
	m.environ = envWithoutSSHChoice("GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=core.foo", "GIT_CONFIG_VALUE_0=bar")
	bare := initBareRemote(t)
	gitIn(t, repo, "remote", "add", "origin", bare)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "push", "-q", "origin", "main")

	heads, err := m.LsRemoteHeadsURL(context.Background(), bare)
	if err != nil {
		t.Fatalf("LsRemoteHeadsURL: %v", err)
	}
	if want := []RemoteHead{{Branch: "main", SHA: base}}; !reflect.DeepEqual(heads, want) {
		t.Fatalf("heads = %+v, want %+v", heads, want)
	}

	gitIn(t, repo, "remote", "add", "omg-before-snapshot", bare)
	if _, err := m.LsRemoteHeadsURL(context.Background(), bare); err == nil ||
		!strings.Contains(err.Error(), "already has a remote named omg-before-snapshot") {
		t.Errorf("a repository remote named omg-before-snapshot: err = %v, want the collision named", err)
	}
}
