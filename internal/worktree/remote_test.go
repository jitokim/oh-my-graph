package worktree

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"
)

// initBareRemote creates an empty bare repository to push to — a remote that
// lives in t.TempDir(), so ls-remote never leaves the machine.
func initBareRemote(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "remote.git")
	gitIn(t, t.TempDir(), "init", "-q", "--bare", "-b", "main", dir)
	return dir
}

// commitIn makes one empty commit in dir and returns its sha.
func commitIn(t *testing.T, dir, msg string) string {
	t.Helper()
	gitIn(t, dir, "commit", "--allow-empty", "-q", "-m", msg)
	return gitIn(t, dir, "rev-parse", "HEAD")
}

func TestGitManager_RemotesOfARepoWithNone(t *testing.T) {
	m, _ := newTestManager(t)
	remotes, err := m.Remotes(context.Background())
	if err != nil {
		t.Fatalf("Remotes: %v", err)
	}
	if len(remotes) != 0 {
		t.Fatalf("Remotes = %+v, want none", remotes)
	}
}

func TestGitManager_RemotesListsNameFetchAndPushURL(t *testing.T) {
	m, repo := newTestManager(t)
	bare := initBareRemote(t)
	gitIn(t, repo, "remote", "add", "origin", bare)
	gitIn(t, repo, "remote", "add", "mirror", "https://example.invalid/m.git")
	gitIn(t, repo, "remote", "set-url", "--push", "mirror", "ssh://example.invalid/m.git")

	remotes, err := m.Remotes(context.Background())
	if err != nil {
		t.Fatalf("Remotes: %v", err)
	}
	want := []Remote{
		{Name: "mirror", FetchURL: "https://example.invalid/m.git", PushURL: "ssh://example.invalid/m.git"},
		{Name: "origin", FetchURL: bare, PushURL: bare},
	}
	if !reflect.DeepEqual(remotes, want) {
		t.Fatalf("Remotes = %+v, want %+v", remotes, want)
	}
}

func TestGitManager_RemotesOutsideARepoFails(t *testing.T) {
	isolateGitConfig(t, filepath.Dir(t.TempDir()))
	m := NewGitManager(t.TempDir(), "", "test-run")
	if _, err := m.Remotes(context.Background()); err == nil {
		t.Fatal("Remotes outside a repository must fail, not report no remotes")
	}
}

func TestGitManager_LsRemoteHeadsListsBranchesAndShas(t *testing.T) {
	m, repo := newTestManager(t)
	bare := initBareRemote(t)
	gitIn(t, repo, "remote", "add", "origin", bare)
	base := gitIn(t, repo, "rev-parse", "HEAD")
	gitIn(t, repo, "push", "-q", "origin", "main")
	gitIn(t, repo, "checkout", "-q", "-b", "feat/x")
	feat := commitIn(t, repo, "feat")
	gitIn(t, repo, "push", "-q", "origin", "feat/x")

	heads, err := m.LsRemoteHeads(context.Background(), "origin")
	if err != nil {
		t.Fatalf("LsRemoteHeads: %v", err)
	}
	want := []RemoteHead{{Branch: "feat/x", SHA: feat}, {Branch: "main", SHA: base}}
	if !reflect.DeepEqual(heads, want) {
		t.Fatalf("LsRemoteHeads = %+v, want %+v", heads, want)
	}
}

func TestGitManager_LsRemoteHeadsOfAnEmptyRemote(t *testing.T) {
	m, repo := newTestManager(t)
	gitIn(t, repo, "remote", "add", "origin", initBareRemote(t))
	heads, err := m.LsRemoteHeads(context.Background(), "origin")
	if err != nil {
		t.Fatalf("LsRemoteHeads: %v", err)
	}
	if len(heads) != 0 {
		t.Fatalf("LsRemoteHeads = %+v, want none", heads)
	}
}

func TestGitManager_LsRemoteHeadsOfABadURLFails(t *testing.T) {
	m, repo := newTestManager(t)
	gitIn(t, repo, "remote", "add", "origin", filepath.Join(t.TempDir(), "no-such-remote.git"))
	if _, err := m.LsRemoteHeads(context.Background(), "origin"); err == nil {
		t.Fatal("ls-remote of a missing repository must fail, not report no branches")
	}
}

func TestGitManager_LsRemoteHeadsRefusesAnOptionShapedName(t *testing.T) {
	m, _ := newTestManager(t)
	for _, name := range []string{"", "--upload-pack=touch pwned", "-x"} {
		if _, err := m.LsRemoteHeads(context.Background(), name); err == nil {
			t.Errorf("LsRemoteHeads(%q) ran; want it refused", name)
		}
	}
}

func TestParseLsRemoteHeadsSkipsWhatIsNotAHead(t *testing.T) {
	sha := strings.Repeat("a", 40)
	out := "warning: redirecting to https://example.invalid/\n" +
		sha + "\trefs/heads/main\n" +
		sha + "\trefs/tags/v1\n" +
		"not-a-sha\trefs/heads/evil\n"
	got := parseLsRemoteHeads(out)
	if want := []RemoteHead{{Branch: "main", SHA: sha}}; !reflect.DeepEqual(got, want) {
		t.Fatalf("parseLsRemoteHeads = %+v, want %+v", got, want)
	}
}

// TestGitManager_CommitAttributable covers ADR 0046 §5.1's check against a
// real repository: a commit on the run's own branch passes; the base commit,
// a commit only on a branch outside the run's prefix, a sha the object store
// does not hold, and any check with no base do not.
func TestGitManager_CommitAttributable(t *testing.T) {
	m, repo := newTestManager(t)
	base := gitIn(t, repo, "rev-parse", "HEAD")

	lane, err := m.Acquire(context.Background(), "lane")
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	runCommit := commitIn(t, lane, "node work")

	gitIn(t, repo, "checkout", "-q", "-b", "elsewhere")
	foreign := commitIn(t, repo, "not on a run branch")

	other := NewGitManager(repo, "", "other-run")
	ctx := context.Background()
	cases := []struct {
		name string
		m    *GitManager
		sha  string
		base string
		want bool
	}{
		{"run commit", m, runCommit, base, true},
		{"the base commit", m, base, base, false},
		{"a commit outside the run's branches", m, foreign, base, false},
		{"a sha not in the object store", m, strings.Repeat("b", 40), base, false},
		{"no recorded base", m, runCommit, "", false},
		{"not a sha", m, "HEAD", base, false},
		{"another run's manager", other, runCommit, base, false},
		{"a manager with no run id", NewGitManager(repo, "", ""), runCommit, base, false},
	}
	for _, tc := range cases {
		if got := tc.m.CommitAttributable(ctx, tc.sha, tc.base); got != tc.want {
			t.Errorf("%s: CommitAttributable = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestGitManager_RemoteSnapshotCommandsDisablePrompting pins the env of the
// snapshot's commands: the four billing variables scrubbed exactly as for a
// worktree command, plus GIT_TERMINAL_PROMPT=0 overriding a parent that set
// it otherwise.
func TestGitManager_RemoteSnapshotCommandsDisablePrompting(t *testing.T) {
	m := NewGitManager("", "", "test-run")
	m.environ = func() []string {
		return []string{
			"ANTHROPIC_API_KEY=sk-live-secret",
			"ANTHROPIC_AUTH_TOKEN=tok-secret",
			"OPENAI_API_KEY=sk-openai-secret",
			"CODEX_API_KEY=sk-codex-secret",
			"GIT_TERMINAL_PROMPT=1",
			"HOME=/home/u",
		}
	}

	cmd := m.gitCmd(context.Background(), noPromptEnv, "ls-remote", "--heads", "origin")
	assertScrubbedEnv(t, cmd.Env)
	if got := lastValue(cmd.Env, "GIT_TERMINAL_PROMPT"); got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q in the snapshot command's env, want 0 to win", got)
	}
	if got := lastValue(m.gitCmd(context.Background(), nil, "status").Env, "GIT_TERMINAL_PROMPT"); got != "1" {
		t.Errorf("a worktree command's env changed: GIT_TERMINAL_PROMPT = %q, want the parent's 1", got)
	}
}

// TestGitManager_LsRemoteHeadsChildSeesScrubbedNoPromptEnv proves the real
// method — not just gitCmd — runs with that env: an ext:: remote's transport
// command is git's own child, and dumps the environment it inherited.
func TestGitManager_LsRemoteHeadsChildSeesScrubbedNoPromptEnv(t *testing.T) {
	m, repo := newTestManager(t)
	m.environ = func() []string {
		return append(os.Environ(),
			"ANTHROPIC_API_KEY=sk-live-secret",
			"ANTHROPIC_AUTH_TOKEN=tok-secret",
			"OPENAI_API_KEY=sk-openai-secret",
			"CODEX_API_KEY=sk-codex-secret",
			"GIT_TERMINAL_PROMPT=1",
		)
	}
	dump := filepath.Join(t.TempDir(), "env.txt")
	gitIn(t, repo, "config", "protocol.ext.allow", "always")
	gitIn(t, repo, "remote", "add", "probe", "ext::sh -c env>"+dump)

	if _, err := m.LsRemoteHeads(context.Background(), "probe"); err == nil {
		t.Fatal("the probe transport speaks no protocol; ls-remote should have failed")
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
}

// envWithoutSSHChoice is the test process's environment minus any ssh
// command the developer running the tests chose, so the cases below start
// from "nothing set" and add only what they test.
func envWithoutSSHChoice(extra ...string) func() []string {
	return func() []string {
		var env []string
		for _, kv := range os.Environ() {
			if k, _, _ := strings.Cut(kv, "="); k != "GIT_SSH_COMMAND" && k != "GIT_SSH" {
				env = append(env, kv)
			}
		}
		return append(env, extra...)
	}
}

// probeLsRemoteEnv runs the real LsRemoteHeads against an ext:: remote whose
// transport dumps the environment it inherited, and returns that env.
func probeLsRemoteEnv(t *testing.T, m *GitManager, repo string) []string {
	t.Helper()
	dump := filepath.Join(t.TempDir(), "env.txt")
	gitIn(t, repo, "config", "protocol.ext.allow", "always")
	gitIn(t, repo, "remote", "add", "probe", "ext::sh -c env>"+dump)
	if _, err := m.LsRemoteHeads(context.Background(), "probe"); err == nil {
		t.Fatal("the probe transport speaks no protocol; ls-remote should have failed")
	}
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the probe never ran: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

// hasKey reports whether env carries key at all, whatever its value.
func hasKey(env []string, key string) bool {
	for _, kv := range env {
		if k, _, _ := strings.Cut(kv, "="); k == key {
			return true
		}
	}
	return false
}

// TestGitManager_LsRemoteHeadsRunsSSHInBatchMode: with no ssh command chosen
// anywhere, ls-remote's child runs ssh in BatchMode, so an unknown host key or
// a locked key fails at once instead of asking on /dev/tty — and git's own
// prompt stays off beside it.
func TestGitManager_LsRemoteHeadsRunsSSHInBatchMode(t *testing.T) {
	isolateGitConfig(t, "")
	m, repo := newTestManager(t)
	m.environ = envWithoutSSHChoice()

	env := probeLsRemoteEnv(t, m, repo)
	if got := lastValue(env, "GIT_SSH_COMMAND"); got != "ssh -o BatchMode=yes" {
		t.Errorf("GIT_SSH_COMMAND = %q in ls-remote's child, want ssh -o BatchMode=yes", got)
	}
	if got := lastValue(env, "GIT_TERMINAL_PROMPT"); got != "0" {
		t.Errorf("GIT_TERMINAL_PROMPT = %q in ls-remote's child, want 0", got)
	}
}

// TestGitManager_LsRemoteHeadsKeepsTheUsersSSHChoice: a GIT_SSH_COMMAND or
// GIT_SSH in the environment, or a core.sshCommand in the repository, is the
// user's choice of ssh, and the snapshot adds no GIT_SSH_COMMAND over it.
func TestGitManager_LsRemoteHeadsKeepsTheUsersSSHChoice(t *testing.T) {
	t.Run("GIT_SSH_COMMAND set", func(t *testing.T) {
		isolateGitConfig(t, "")
		m, repo := newTestManager(t)
		m.environ = envWithoutSSHChoice("GIT_SSH_COMMAND=ssh -i /keys/deploy")
		env := probeLsRemoteEnv(t, m, repo)
		if got := lastValue(env, "GIT_SSH_COMMAND"); got != "ssh -i /keys/deploy" {
			t.Errorf("GIT_SSH_COMMAND = %q in ls-remote's child, want the user's own unchanged", got)
		}
	})
	t.Run("GIT_SSH set", func(t *testing.T) {
		isolateGitConfig(t, "")
		m, repo := newTestManager(t)
		m.environ = envWithoutSSHChoice("GIT_SSH=/usr/local/bin/my-ssh")
		env := probeLsRemoteEnv(t, m, repo)
		if hasKey(env, "GIT_SSH_COMMAND") {
			t.Errorf("GIT_SSH_COMMAND = %q added over the user's GIT_SSH", lastValue(env, "GIT_SSH_COMMAND"))
		}
		if got := lastValue(env, "GIT_SSH"); got != "/usr/local/bin/my-ssh" {
			t.Errorf("GIT_SSH = %q in ls-remote's child, want the user's own unchanged", got)
		}
	})
	t.Run("core.sshCommand configured", func(t *testing.T) {
		isolateGitConfig(t, "")
		m, repo := newTestManager(t)
		m.environ = envWithoutSSHChoice()
		gitIn(t, repo, "config", "core.sshCommand", "ssh -i /keys/deploy")
		env := probeLsRemoteEnv(t, m, repo)
		if hasKey(env, "GIT_SSH_COMMAND") {
			t.Errorf("GIT_SSH_COMMAND = %q added over the repository's core.sshCommand", lastValue(env, "GIT_SSH_COMMAND"))
		}
	})
}

// TestGitManager_RemotesRunsSSHInBatchMode pins that Remotes shares the
// snapshot env, and that the decision is read from the scrubbed environ.
func TestGitManager_RemotesRunsSSHInBatchMode(t *testing.T) {
	isolateGitConfig(t, "")
	m, repo := newTestManager(t)
	m.environ = envWithoutSSHChoice()
	if got := lastValue(m.snapshotEnv(context.Background()), "GIT_SSH_COMMAND"); got != "ssh -o BatchMode=yes" {
		t.Errorf("snapshotEnv GIT_SSH_COMMAND = %q, want ssh -o BatchMode=yes", got)
	}
	gitIn(t, repo, "config", "core.sshCommand", "")
	if got := lastValue(m.snapshotEnv(context.Background()), "GIT_SSH_COMMAND"); got != "ssh -o BatchMode=yes" {
		t.Errorf("an empty core.sshCommand counts as unset: GIT_SSH_COMMAND = %q, want ssh -o BatchMode=yes", got)
	}
	if _, err := m.Remotes(context.Background()); err != nil {
		t.Fatalf("Remotes: %v", err)
	}
}

// TestGitManager_WorktreeCommandsGetNoSSHCommand: the BatchMode env is the
// snapshot's alone. A post-checkout hook — a real child of Acquire's
// `git worktree add` — dumps its env, which carries no GIT_SSH_COMMAND.
func TestGitManager_WorktreeCommandsGetNoSSHCommand(t *testing.T) {
	isolateGitConfig(t, "")
	m, repo := newTestManager(t)
	m.environ = envWithoutSSHChoice()
	dump := filepath.Join(t.TempDir(), "env.txt")
	hook := filepath.Join(repo, ".git", "hooks", "post-checkout")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\nenv > '"+dump+"'\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	if _, err := m.Acquire(context.Background(), "lane"); err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	data, err := os.ReadFile(dump)
	if err != nil {
		t.Fatalf("the post-checkout hook never ran: %v", err)
	}
	env := strings.Split(strings.TrimSpace(string(data)), "\n")
	if hasKey(env, "GIT_SSH_COMMAND") {
		t.Errorf("a worktree command's env gained GIT_SSH_COMMAND = %q", lastValue(env, "GIT_SSH_COMMAND"))
	}
	if hasKey(m.gitCmd(context.Background(), nil, "status").Env, "GIT_SSH_COMMAND") {
		t.Error("gitCmd with no extra env added GIT_SSH_COMMAND")
	}
}

// TestGitManager_TimeoutIsNamedOnlyWhenItsOwn pins the error gitEnv returns
// when a git command outlives the manager's own deadline: "timed out after",
// the reason the snapshot records. A deadline the caller's context carried
// is the caller's, not a git timeout, and must not be reported as one.
func TestGitManager_TimeoutIsNamedOnlyWhenItsOwn(t *testing.T) {
	m, repo := newTestManager(t)
	gitIn(t, repo, "config", "protocol.ext.allow", "always")
	gitIn(t, repo, "remote", "add", "slow", "ext::sh -c sleep% 5")

	m.timeout = 200 * time.Millisecond
	start := time.Now()
	_, err := m.LsRemoteHeads(context.Background(), "slow")
	if err == nil || !strings.Contains(err.Error(), "timed out after 200ms") {
		t.Fatalf("LsRemoteHeads past the manager's timeout: err = %v, want it to name the timeout", err)
	}
	if took := time.Since(start); took > 4*time.Second {
		t.Errorf("the timeout did not cut the command short: it took %s", took)
	}

	m.timeout = 10 * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err = m.LsRemoteHeads(ctx, "slow")
	if err == nil {
		t.Fatal("LsRemoteHeads under an expired caller context succeeded")
	}
	if strings.Contains(err.Error(), "timed out after") {
		t.Errorf("the caller's own deadline was reported as a git timeout: %v", err)
	}
}

func assertScrubbedEnv(t *testing.T, env []string) {
	t.Helper()
	for _, kv := range env {
		for _, key := range []string{"ANTHROPIC_API_KEY=", "ANTHROPIC_AUTH_TOKEN=", "OPENAI_API_KEY=", "CODEX_API_KEY="} {
			if strings.HasPrefix(kv, key) {
				t.Errorf("billing variable leaked into the git child env: %s", kv)
			}
		}
	}
}

// lastValue is the value os/exec hands the child for key: the last one wins.
func lastValue(env []string, key string) string {
	val := ""
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok && k == key {
			val = v
		}
	}
	return val
}
