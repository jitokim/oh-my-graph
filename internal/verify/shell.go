package verify

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"time"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/childenv"
)

// Shell invocation defaults. The interpreter itself (defaultShell/shellFlag) is
// per-OS — see shell_unix.go and shell_windows.go — because `sh` is not on PATH
// on native Windows. defaultTimeout mirrors the graph loader's default for
// success_check.verify and exists only so a hand-built Request with no timeout
// still gets a bound: a verification with no deadline could wedge a node until
// its 20m runner timeout.
const defaultTimeout = 2 * time.Minute

// maxOutputBytes bounds what a verification hands back. A verification command
// is often a test suite, and its full output is neither useful in the ledger nor
// worth retaining for the rest of the run — the tail is where a failure explains
// itself, so the tail is what survives.
const maxOutputBytes = 4096

// truncationMarker prefixes an output that was cut, so nobody reads a partial
// tail as the whole story.
const truncationMarker = "…(earlier output truncated)…\n"

// waitDelay bounds how long Wait will keep waiting after the child was killed.
// Killing the process group normally ends everything at once, so this is the
// last resort for the one case it cannot cover: a descendant that escaped the
// group (it set its own) still holds the inherited output pipe open, and Wait
// blocks on the pipe rather than on the process. A cancelled verification must
// return promptly even then.
const waitDelay = 2 * time.Second

// TimeoutError is a verification command that was still running when its own
// timeout expired. It is deliberately distinct from a non-zero exit: the command
// never reached a verdict, and reporting it as "failed the check" would claim
// evidence that was never gathered. Either way the node fails — a verification
// that could not be completed is never a silent pass.
type TimeoutError struct {
	Command string
	Timeout time.Duration
	// Output is whatever the command had printed before it was killed.
	Output string
}

func (e *TimeoutError) Error() string {
	return fmt.Sprintf("verification command %q timed out after %s", e.Command, e.Timeout)
}

// ShellVerifier runs verification commands as real subprocesses. It is the only
// object in this package that spawns a process, and one of exactly two in the
// project (the other is runner.ClaudeCLIRunner) — see docs/adr/0002.
//
// Construct it in cmd/oh-my-graph and inject it; the Scheduler never builds one,
// so no test picks up a real spawn by accident.
type ShellVerifier struct {
	// shell is the interpreter (a field, not a literal, so a test can build the
	// command against a stub without running anything).
	shell string
	// timeout is the fallback bound for a Request that declares none.
	timeout time.Duration
	// environ supplies the parent environment to scrub. A field (defaulting to
	// os.Environ) so a test can inject a parent env that DOES carry the API keys
	// and then assert they are absent from the built child env.
	environ func() []string
}

// ShellOption configures a ShellVerifier at construction (functional options —
// the zero-config NewShellVerifier() is the production path).
type ShellOption func(*ShellVerifier)

// WithShell overrides the interpreter used to run the command.
func WithShell(shell string) ShellOption {
	return func(v *ShellVerifier) { v.shell = shell }
}

// WithDefaultTimeout overrides the fallback timeout for requests that declare
// none. A request's own Timeout always wins.
func WithDefaultTimeout(d time.Duration) ShellOption {
	return func(v *ShellVerifier) { v.timeout = d }
}

// withEnviron overrides the parent-environment source. Unexported: only tests
// need to inject a fake environ (production always reads the real os.Environ).
func withEnviron(environ func() []string) ShellOption {
	return func(v *ShellVerifier) { v.environ = environ }
}

// NewShellVerifier builds the production Verifier: `<shell> <flag> <command>`
// with the interpreter for this OS, scrubbing the real process environment.
func NewShellVerifier(opts ...ShellOption) *ShellVerifier {
	v := &ShellVerifier{
		shell:   defaultShell,
		timeout: defaultTimeout,
		environ: os.Environ,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// buildCmd assembles the exact *exec.Cmd for a request: argv, cwd, the scrubbed
// child environment, and the cancellation behaviour. It is the unit under test —
// shell_test.go calls it directly to assert both the argv AND that
// ANTHROPIC_API_KEY / ANTHROPIC_AUTH_TOKEN are absent from cmd.Env. Nothing here
// spawns; Verify wires it to the OS.
//
// The env scrub is not an optional nicety for verification: `verify: { command:
// "claude -p ..." }` is a legal thing to write, and an unscrubbed child would
// run it on metered API billing.
func (v *ShellVerifier) buildCmd(ctx context.Context, req Request) *exec.Cmd {
	cmd := exec.CommandContext(ctx, v.shell, shellFlag, req.Command)
	cmd.Dir = req.Cwd
	cmd.Env = childenv.Scrub(v.environ())

	// The direct child is the interpreter, but the work is in its descendants —
	// the test suite, the build, the `sleep`. exec's own cancellation kills only
	// the shell, so a cancelled or timed-out verification would leave that work
	// running (and, since the descendants inherit the output pipe, would leave
	// Wait blocked on the pipe until they finished on their own — the run
	// outliving the context that bounded it). Give the child its own process
	// group and cancel by killing the group.
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = waitDelay
	return cmd
}

// Verify runs the command under BOTH the caller's context and its own timeout,
// and reports what happened.
//
// A non-zero exit is not an error: the command ran, and its exit code is exactly
// the evidence the caller asked for. Verify errors only when there is no verdict
// to report — the run was cancelled (halt-on-fail killing this child like any
// other), the command's own timeout expired, or it could not be spawned at all.
func (v *ShellVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	timeout := req.Timeout
	if timeout <= 0 {
		timeout = v.timeout
	}
	// Derived from the caller's context, so cancelling the run cancels the
	// verification child too.
	cmdCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	combined, runErr := v.buildCmd(cmdCtx, req).CombinedOutput()
	output := tailBytes(string(combined), maxOutputBytes)
	if runErr == nil {
		return Result{ExitCode: 0, Output: output}, nil
	}

	// Order matters: a killed process also surfaces as an *exec.ExitError, so
	// cancellation and timeout must be ruled out before an exit code is read —
	// otherwise a run cancelled mid-verification would be reported as a real
	// (and misleading) exit status.
	if err := ctx.Err(); err != nil {
		return Result{}, fmt.Errorf("verification command %q cancelled: %w", req.Command, err)
	}
	if errors.Is(cmdCtx.Err(), context.DeadlineExceeded) {
		return Result{}, &TimeoutError{Command: req.Command, Timeout: timeout, Output: output}
	}

	var exitErr *exec.ExitError
	if !errors.As(runErr, &exitErr) {
		return Result{}, fmt.Errorf("verification command %q failed to run: %w", req.Command, runErr)
	}
	return Result{ExitCode: exitErr.ExitCode(), Output: output}, nil
}

// tailBytes keeps the last maxBytes of s, marking the cut. The tail is kept
// rather than the head because a failing command explains itself at the end.
// The cut is moved forward to the next rune boundary so the result is always
// valid UTF-8.
func tailBytes(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}
	cut := s[len(s)-maxBytes:]
	for len(cut) > 0 && !utf8.RuneStart(cut[0]) {
		cut = cut[1:]
	}
	return truncationMarker + cut
}
