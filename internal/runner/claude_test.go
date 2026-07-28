package runner

import (
	"context"
	"strings"
	"testing"
)

// prompt/tool fixtures reused across the argv assertions.
const (
	testPrompt = "write a haiku about graphs"
)

// TestBuildCmd_Argv asserts the exact claude argv oh-my-graph builds, including
// the ordering of flags and the omission of optional flags when unset. This is
// the contract every real run depends on — if the argv drifts, the whole tool
// silently invokes claude wrong.
func TestBuildCmd_Argv(t *testing.T) {
	r := NewClaudeCLIRunner(WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		Cwd:            "/tmp/omg",
		PermissionMode: "acceptEdits",
		AllowedTools:   []string{"Read", "Bash(make *)"},
		ResumeSession:  "sess-123",
	})

	want := []string{
		"claude",
		"-p", testPrompt,
		"--output-format", "json",
		"--permission-mode", "acceptEdits",
		"--allowedTools", "Read,Bash(make *)",
		"--resume", "sess-123",
	}
	if got := cmd.Args; !equalArgs(got, want) {
		t.Fatalf("argv mismatch:\n got=%q\nwant=%q", got, want)
	}
	if cmd.Dir != "/tmp/omg" {
		t.Fatalf("cwd = %q, want /tmp/omg", cmd.Dir)
	}
}

// TestBuildCmd_OmitsOptionalFlags proves --allowedTools and --resume are absent
// when no tools and no resume session are configured (a fan-in node with a clean
// session and no tool grants).
func TestBuildCmd_OmitsOptionalFlags(t *testing.T) {
	r := NewClaudeCLIRunner()
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "plan",
	})

	joined := strings.Join(cmd.Args, " ")
	if strings.Contains(joined, "--allowedTools") {
		t.Errorf("expected no --allowedTools flag, got argv: %q", cmd.Args)
	}
	if strings.Contains(joined, "--resume") {
		t.Errorf("expected no --resume flag, got argv: %q", cmd.Args)
	}
}

// TestBuildCmd_NeverBareOrNoSessionPersistence is a guard against the two flags
// that would break the subscription-auth / fleetops-observability contract:
// --bare disables OAuth, and --no-session-persistence hides the run from
// fleetops. Neither must ever appear.
func TestBuildCmd_NeverBareOrNoSessionPersistence(t *testing.T) {
	r := NewClaudeCLIRunner()
	cmd := r.buildCmd(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	joined := strings.Join(cmd.Args, " ")
	for _, forbidden := range []string{"--bare", "--no-session-persistence"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("argv must never contain %s, got: %q", forbidden, cmd.Args)
		}
	}
}

// TestBuildCmd_ScrubsSubscriptionAuthEnv is the load-bearing test of the whole
// project: even when the PARENT process has ANTHROPIC_API_KEY and
// ANTHROPIC_AUTH_TOKEN set, the built child command's env must contain NEITHER —
// otherwise claude silently switches from the user's subscription (OAuth) to
// metered API billing.
//
// The parent env is injected via a fake environ so the assertion does not depend
// on the developer's real shell, and a benign variable is included to prove the
// scrub is surgical (it removes only the two auth keys, not the whole env).
func TestBuildCmd_ScrubsSubscriptionAuthEnv(t *testing.T) {
	parentEnv := []string{
		"ANTHROPIC_API_KEY=sk-should-be-scrubbed",
		"ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed",
		"PATH=/usr/bin",
		"HOME=/home/dev",
	}
	r := NewClaudeCLIRunner(withEnviron(func() []string { return parentEnv }))

	cmd := r.buildCmd(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})

	for _, kv := range cmd.Env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "ANTHROPIC_API_KEY" {
			t.Errorf("ANTHROPIC_API_KEY leaked into child env: %q", kv)
		}
		if key == "ANTHROPIC_AUTH_TOKEN" {
			t.Errorf("ANTHROPIC_AUTH_TOKEN leaked into child env: %q", kv)
		}
	}

	// Surgical: the benign variables must survive.
	if !containsEnv(cmd.Env, "PATH=/usr/bin") || !containsEnv(cmd.Env, "HOME=/home/dev") {
		t.Errorf("scrub removed benign env vars; child env = %q", cmd.Env)
	}
}

// TestScrubEnv_KeyMatchOnly proves the scrub matches on the env KEY, not on a
// substring — a variable whose VALUE happens to contain the key name is kept.
func TestScrubEnv_KeyMatchOnly(t *testing.T) {
	in := []string{
		"ANTHROPIC_API_KEY=secret",
		"MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere",
		"ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me",
	}
	out := scrubEnv(in)

	if containsEnv(out, "ANTHROPIC_API_KEY=secret") {
		t.Error("exact-key ANTHROPIC_API_KEY was not scrubbed")
	}
	if !containsEnv(out, "MY_NOTE=ANTHROPIC_API_KEY is scrubbed elsewhere") {
		t.Error("value-substring match wrongly scrubbed MY_NOTE")
	}
	if !containsEnv(out, "ANTHROPIC_AUTH_TOKEN_BACKUP=keep-me") {
		t.Error("prefix-only key ANTHROPIC_AUTH_TOKEN_BACKUP was wrongly scrubbed")
	}
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
