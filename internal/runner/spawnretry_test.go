package runner

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

// updatingBinary is the bare command name the #298 tests resolve against a
// PATH holding only their own temp dir, so nothing installed on the machine can
// answer for it.
const updatingBinary = "omg-updating-claude"

const envelopeStub = `#!/bin/sh
printf '%s\n' '{"session_id":"s-retry","result":"done","total_cost_usd":0.01}'
`

// noPause is a spawn pause that returns at once, for tests that exercise the
// retry bound without waiting it out.
func noPause(context.Context, time.Duration) error { return nil }

// pauseCounter records each wait the retry asks for and runs onPause (if set)
// at the given wait, numbered from 1.
type pauseCounter struct {
	waits   []time.Duration
	at      int
	onPause func()
}

func (p *pauseCounter) pause(_ context.Context, d time.Duration) error {
	p.waits = append(p.waits, d)
	if p.onPause != nil && len(p.waits) == p.at {
		p.onPause()
	}
	return nil
}

func emptyPATH(t *testing.T) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	dir := t.TempDir()
	t.Setenv("PATH", dir)
	return dir
}

// The #298 case end to end through the real exec seam: the runtime binary is
// absent from PATH (an auto-update has removed it and not yet put it back), then
// reappears. The node must run, not halt.
func TestRun_RetriesABinaryMissingFromPATHUntilItReappears(t *testing.T) {
	dir := emptyPATH(t)
	pauses := &pauseCounter{at: 2, onPause: func() {
		if err := os.WriteFile(filepath.Join(dir, updatingBinary), []byte(envelopeStub), 0o755); err != nil {
			t.Fatalf("install binary: %v", err)
		}
	}}
	r := NewCLIRunner(RuntimeClaude, WithBinary(updatingBinary), withSpawnPause(pauses.pause))

	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("Run() = %v, want the node to start once the binary is back", err)
	}
	if outcome.Result != "done" {
		t.Errorf("Result = %q, want the reply of the attempt that started", outcome.Result)
	}
	if len(pauses.waits) != 2 {
		t.Errorf("waited %d times, want 2 — one per missing lookup before the binary returned", len(pauses.waits))
	}
	for i, d := range pauses.waits {
		if d != spawnRetryDelay {
			t.Errorf("wait %d = %s, want spawnRetryDelay (%s)", i+1, d, spawnRetryDelay)
		}
	}
}

// A binary that never comes back is reported exactly as before — a
// *NodeSpawnError carrying exec.ErrNotFound — after spawnAttempts lookups, not
// an unbounded wait.
func TestRun_GivesUpOnAMissingBinaryAfterTheBound(t *testing.T) {
	emptyPATH(t)
	pauses := &pauseCounter{}
	r := NewCLIRunner(RuntimeClaude, WithBinary(updatingBinary), withSpawnPause(pauses.pause))

	_, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	var spawnErr *NodeSpawnError
	if !errors.As(err, &spawnErr) {
		t.Fatalf("Run() = %v, want *NodeSpawnError", err)
	}
	if !errors.Is(err, exec.ErrNotFound) {
		t.Errorf("Run() = %v, want it to still unwrap to exec.ErrNotFound", err)
	}
	if got, want := len(pauses.waits), spawnAttempts-1; got != want {
		t.Errorf("waited %d times, want %d (spawnAttempts-1)", got, want)
	}
}

// Nothing but exec.ErrNotFound is retried. A binary that exists but cannot be
// executed is a different start failure, and a process that ran and exited
// non-zero is a reply — neither earns a second attempt.
func TestRun_RetriesNothingButAMissingBinary(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	t.Run("not executable", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "claude-not-executable")
		if err := os.WriteFile(path, []byte(envelopeStub), 0o644); err != nil {
			t.Fatalf("write stub: %v", err)
		}
		pauses := &pauseCounter{}
		r := NewCLIRunner(RuntimeClaude, WithBinary(path), withSpawnPause(pauses.pause))

		_, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
		var spawnErr *NodeSpawnError
		if !errors.As(err, &spawnErr) {
			t.Fatalf("Run() = %v, want *NodeSpawnError", err)
		}
		if errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("Run() = %v, a non-executable file is not exec.ErrNotFound", err)
		}
		if len(pauses.waits) != 0 {
			t.Errorf("waited %d times, want 0 — only a missing binary is retried", len(pauses.waits))
		}
	})
	t.Run("non-zero exit", func(t *testing.T) {
		stub := writeStub(t, envelopeStub+"exit 1\n")
		pauses := &pauseCounter{}
		r := NewCLIRunner(RuntimeClaude, WithBinary(stub), withSpawnPause(pauses.pause))

		outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
		if err != nil {
			t.Fatalf("Run() = %v, want the non-zero exit as an outcome", err)
		}
		if outcome.ExitCode != 1 {
			t.Errorf("ExitCode = %d, want 1", outcome.ExitCode)
		}
		if len(pauses.waits) != 0 {
			t.Errorf("waited %d times, want 0 — a reply is never retried", len(pauses.waits))
		}
	})
}

// Cancellation outranks the bound: a caller that gave up during a retry pause
// gets its cancellation back at once, not the remaining attempts.
func TestRun_CancellationDuringARetryPauseStopsTheRetry(t *testing.T) {
	emptyPATH(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pauses := 0
	r := NewCLIRunner(RuntimeClaude, WithBinary(updatingBinary), withSpawnPause(func(ctx context.Context, d time.Duration) error {
		pauses++
		cancel()
		return sleepCtx(ctx, d)
	}))

	_, err := r.Run(ctx, NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run() = %v, want context.Canceled", err)
	}
	if pauses != 1 {
		t.Errorf("paused %d times, want 1 — the retry must stop at the cancellation", pauses)
	}
}

// The preflight's "command exists" check tolerates the same window, so a run
// started while the CLI is mid-update is not refused for a binary that is back a
// second later.
func TestCheckCLIAvailableToleratesABriefMissingWindow(t *testing.T) {
	restore := lookPath
	defer func() { lookPath = restore }()
	lookups := 0
	lookPath = func(name string) (string, error) {
		lookups++
		if lookups < 3 {
			return "", &exec.Error{Name: name, Err: exec.ErrNotFound}
		}
		return "/usr/local/bin/" + name, nil
	}
	pauses := &pauseCounter{}

	if err := NewCLIRunner(RuntimeClaude, withSpawnPause(pauses.pause)).CheckCLIAvailable(); err != nil {
		t.Fatalf("CheckCLIAvailable() = %v, want nil once the binary resolves", err)
	}
	if lookups != 3 || len(pauses.waits) != 2 {
		t.Errorf("lookups = %d, waits = %d; want 3 lookups and 2 waits", lookups, len(pauses.waits))
	}
}

// ...and it still refuses, with the same error, once the bound is spent — and
// does not retry a lookup that failed for any other reason.
func TestCheckCLIAvailableGivesUpAfterTheBound(t *testing.T) {
	restore := lookPath
	defer func() { lookPath = restore }()

	for _, tc := range []struct {
		name        string
		cause       error
		wantLookups int
	}{
		{"not found is retried to the bound", exec.ErrNotFound, spawnAttempts},
		{"any other lookup failure is not retried", exec.ErrDot, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups := 0
			lookPath = func(name string) (string, error) {
				lookups++
				return "", &exec.Error{Name: name, Err: tc.cause}
			}

			err := NewCLIRunner(RuntimeClaude, withSpawnPause(noPause)).CheckCLIAvailable()
			var notFound *CLINotFoundError
			if !errors.As(err, &notFound) {
				t.Fatalf("CheckCLIAvailable() = %v, want *CLINotFoundError", err)
			}
			if !errors.Is(err, tc.cause) {
				t.Errorf("CheckCLIAvailable() = %v, want it to unwrap to %v", err, tc.cause)
			}
			if lookups != tc.wantLookups {
				t.Errorf("lookups = %d, want %d", lookups, tc.wantLookups)
			}
		})
	}
}
