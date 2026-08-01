package verify

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The Verify tests below run real `sh` commands. That is not a hole in the
// project's "no real spawns in CI" rule — the rule exists so no test costs money
// or needs a claude login. `sh -c 'exit 3'` is free, offline and hermetic, and
// ShellVerifier's whole job is the exec behaviour (exit codes, timeouts,
// cancellation), which cannot be proven without a process. Every OTHER package
// verifies through FakeVerifier and spawns nothing.

const echoCommand = "echo hello"

// --- argv and environment (no spawn) ----------------------------------------

// TestBuildCmd_ScrubsSubscriptionAuthEnv is the verification half of the
// project's load-bearing guarantee. `verify: { command: "claude -p ..." }` is a
// legal thing to write, so a verification child that kept ANTHROPIC_API_KEY
// would run it on metered API billing — the exact failure the runner's scrub
// exists to prevent, arriving through the second spawner instead.
func TestBuildCmd_ScrubsSubscriptionAuthEnv(t *testing.T) {
	parentEnv := []string{
		"ANTHROPIC_API_KEY=sk-should-be-scrubbed",
		"ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed",
		"PATH=/usr/bin",
		"HOME=/home/dev",
	}
	v := NewShellVerifier(withEnviron(func() []string { return parentEnv }))

	cmd := v.buildCmd(context.Background(), Request{Command: "claude -p 'is it done?'"})

	for _, kv := range cmd.Env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "ANTHROPIC_API_KEY" {
			t.Errorf("ANTHROPIC_API_KEY leaked into the verification child env: %q", kv)
		}
		if key == "ANTHROPIC_AUTH_TOKEN" {
			t.Errorf("ANTHROPIC_AUTH_TOKEN leaked into the verification child env: %q", kv)
		}
	}
	// Surgical: a verification command needs the rest of the environment (PATH
	// above all) to run at all.
	if !containsEnv(cmd.Env, "PATH=/usr/bin") || !containsEnv(cmd.Env, "HOME=/home/dev") {
		t.Errorf("scrub removed benign env vars; child env = %q", cmd.Env)
	}
}

// TestBuildCmd_RunsThroughShellInRequestedCwd pins the invocation shape: the
// command is handed to the interpreter as ONE argument, so a graph can write an
// ordinary command line (pipes, &&, quoting) instead of an argv array. WHICH
// interpreter is per-OS and is pinned by shell_unix_test.go / shell_windows_test.go.
func TestBuildCmd_RunsThroughShellInRequestedCwd(t *testing.T) {
	v := NewShellVerifier()
	cmd := v.buildCmd(context.Background(), Request{
		Command: "go test ./... && echo ok",
		Cwd:     "/tmp/omg",
	})

	assertArgv(t, cmd.Args, []string{defaultShell, shellFlag, "go test ./... && echo ok"})
	if cmd.Dir != "/tmp/omg" {
		t.Errorf("cwd = %q, want /tmp/omg", cmd.Dir)
	}
}

// --- failure paths ----------------------------------------------------------

// TestVerify_NonZeroExitIsAResultNotAnError is the central contract: a command
// that ran and failed produced EVIDENCE. Returning an error here would collapse
// "the check says no" into "the check could not be run", and the caller could no
// longer report which one happened.
func TestVerify_NonZeroExitIsAResultNotAnError(t *testing.T) {
	result, err := NewShellVerifier().Verify(context.Background(), Request{Command: "exit 3"})
	if err != nil {
		t.Fatalf("a non-zero exit must not be an error: %v", err)
	}
	if result.ExitCode != 3 {
		t.Errorf("exit code = %d, want 3", result.ExitCode)
	}
}

// TestVerify_TimesOutRatherThanHanging proves a wedged verification cannot hold
// a node forever: its own timeout kills it and the caller gets a *TimeoutError,
// never a silent pass.
func TestVerify_TimesOutRatherThanHanging(t *testing.T) {
	_, err := NewShellVerifier().Verify(context.Background(), Request{
		Command: "sleep 30",
		Timeout: 50 * time.Millisecond,
	})

	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected *TimeoutError, got %T: %v", err, err)
	}
	if timeoutErr.Command != "sleep 30" {
		t.Errorf("timeout error command = %q, want the declared command", timeoutErr.Command)
	}
	if !strings.Contains(timeoutErr.Error(), "50ms") {
		t.Errorf("timeout error should state the bound it broke, got %q", timeoutErr.Error())
	}
}

// TestVerify_CancelledRunKillsTheChild proves the shared run context reaches the
// verification child: halt-on-fail cancels the run, and a verification in flight
// must die with it instead of outliving the run that started it. A cancellation
// is reported as an error, not as an exit code, because the command never
// reached a verdict.
func TestVerify_CancelledRunKillsTheChild(t *testing.T) {
	// The command touches `started` first so the test cancels only once the
	// child is provably running: cancelling after a bare sleep can, on a
	// stalled machine, land before the child even spawns — and then the test
	// proves nothing about killing a live process.
	started := filepath.Join(t.TempDir(), "started")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := NewShellVerifier().Verify(ctx, Request{
			Command: "touch '" + started + "'; sleep 30",
			Timeout: time.Minute,
		})
		done <- err
	}()

	waitForFile(t, started)
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Verify did not return after its context was cancelled")
	}
	if err == nil {
		t.Fatal("a cancelled verification must not report success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a context.Canceled error, got %T: %v", err, err)
	}
}

// waitForFile polls for path until it exists, failing the test at a deadline.
// It is the start signal for the real-spawn cancellation test above.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("child never signalled it started (no %s)", path)
}

// TestVerify_UnspawnableCommandIsAnError covers the third no-verdict case: the
// interpreter could not run the command at all. It must be an error, not exit
// code 0.
func TestVerify_UnspawnableCommandIsAnError(t *testing.T) {
	v := NewShellVerifier(WithShell("definitely-not-a-real-shell-binary"))

	_, err := v.Verify(context.Background(), Request{Command: echoCommand})
	if err == nil {
		t.Fatal("expected an error when the command cannot be spawned")
	}
	if !strings.Contains(err.Error(), echoCommand) {
		t.Errorf("spawn error should name the command, got %q", err.Error())
	}
}

// --- success path and output -------------------------------------------------

// TestVerify_CapturesCombinedOutput proves stderr is captured too — a failing
// build or test suite says why on stderr, and a Result that dropped it would
// leave the ledger with nothing useful to report.
func TestVerify_CapturesCombinedOutput(t *testing.T) {
	result, err := NewShellVerifier().Verify(context.Background(), Request{
		Command: "echo to-stdout; echo to-stderr 1>&2",
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	for _, want := range []string{"to-stdout", "to-stderr"} {
		if !strings.Contains(result.Output, want) {
			t.Errorf("output %q missing %q", result.Output, want)
		}
	}
}

// TestVerify_RunsInTheRequestedCwd proves the working directory is honoured, so
// a graph can verify a checkout other than the one it was invoked from.
func TestVerify_RunsInTheRequestedCwd(t *testing.T) {
	dir := t.TempDir()

	result, err := NewShellVerifier().Verify(context.Background(), Request{
		Command: "test -d .",
		Cwd:     dir,
	})
	if err != nil {
		t.Fatalf("Verify returned error: %v", err)
	}
	if result.ExitCode != 0 {
		t.Errorf("exit code = %d, want 0 — the command did not run in %s", result.ExitCode, dir)
	}
}

// TestVerify_ZeroTimeoutFallsBackToTheDefault proves a Request that declares no
// timeout still gets one. Graphs always carry a timeout (the loader defaults it),
// so this only guards direct API use — but an unbounded command would wedge a
// node for the runner's full 20 minutes.
func TestVerify_ZeroTimeoutFallsBackToTheDefault(t *testing.T) {
	v := NewShellVerifier(WithDefaultTimeout(50 * time.Millisecond))

	_, err := v.Verify(context.Background(), Request{Command: "sleep 30"})

	var timeoutErr *TimeoutError
	if !errors.As(err, &timeoutErr) {
		t.Fatalf("expected the default timeout to apply, got %T: %v", err, err)
	}
}

// --- output truncation -------------------------------------------------------

// TestTailBytes_KeepsTheTailAndMarksTheCut pins which end survives: a failing
// command explains itself at the END, so the head is what gets dropped, and the
// cut is marked so nobody reads a fragment as the whole output.
func TestTailBytes_KeepsTheTailAndMarksTheCut(t *testing.T) {
	long := strings.Repeat("a", maxOutputBytes) + "FAIL: the last line"

	got := tailBytes(long, maxOutputBytes)

	if !strings.HasSuffix(got, "FAIL: the last line") {
		t.Errorf("truncation dropped the tail: %q", got[max(0, len(got)-40):])
	}
	if !strings.HasPrefix(got, truncationMarker) {
		t.Errorf("truncated output must be marked as such, got prefix %q", got[:40])
	}
}

// TestTailBytes_ShortOutputIsUntouched keeps the common case honest: nothing is
// marked as truncated when nothing was cut.
func TestTailBytes_ShortOutputIsUntouched(t *testing.T) {
	if got := tailBytes("ok\n", maxOutputBytes); got != "ok\n" {
		t.Errorf("short output was altered: %q", got)
	}
}

// TestTailBytes_CutsOnARuneBoundary proves the tail is always valid UTF-8: a
// byte-exact cut through a multi-byte character would put a replacement
// character into the ledger.
func TestTailBytes_CutsOnARuneBoundary(t *testing.T) {
	// Three-byte runes, so a byte cut lands mid-rune for two of every three
	// possible offsets.
	long := strings.Repeat("한", 100)

	got := strings.TrimPrefix(tailBytes(long, 50), truncationMarker)

	if !strings.HasPrefix(got, "한") {
		t.Errorf("tail starts mid-rune: %q", got[:6])
	}
}

// --- the refusing default ----------------------------------------------------

// TestRefusingVerifier_FailsLoudly proves the default Verifier refuses instead
// of running anything. A scheduler that forgot to inject a real one must fail
// where the mistake is, not silently spawn a process from a unit test.
func TestRefusingVerifier_FailsLoudly(t *testing.T) {
	_, err := NewRefusingVerifier().Verify(context.Background(), Request{Command: echoCommand})

	if !errors.Is(err, ErrNoVerifier) {
		t.Fatalf("expected ErrNoVerifier, got %T: %v", err, err)
	}
	var notConfigured *NotConfiguredError
	if !errors.As(err, &notConfigured) || notConfigured.Command != echoCommand {
		t.Errorf("refusal should name the command it declined to run, got %v", err)
	}
}

// assertArgv compares a built command's argv against the expected one.
func assertArgv(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("argv = %q, want %q", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("argv = %q, want %q", got, want)
		}
	}
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
