package worktree

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

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
// entry that reports what was done to origin.
func TestGuardedRemoteLeg_ARemovedOrRepointedRemoteCannotHideAPush(t *testing.T) {
	for _, tc := range []struct {
		name string
		hide func(t *testing.T, f remoteFixture)
		kind runstate.RemoteChangeKind
	}{
		{"remote removed", func(t *testing.T, f remoteFixture) { gitIn(t, f.repo, "remote", "remove", "origin") }, runstate.RemoteRemoved},
		{"remote re-pointed", func(t *testing.T, f remoteFixture) {
			gitIn(t, f.repo, "remote", "set-url", "origin", filepath.Join(t.TempDir(), "unreachable.git"))
		}, runstate.RemoteURLChanged},
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
			if len(diff.NotCompared) != 0 {
				t.Errorf("not_compared = %+v, want origin's refs compared", diff.NotCompared)
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
