package runner

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/childenv"
)

const (
	defaultTimeout   = 20 * time.Minute
	maxStderrInError = 500
	waitDelay        = 2 * time.Second
)

// NodeOutputError marks CLI output that could not be decoded into an outcome.
type NodeOutputError struct {
	Runtime string
	Reason  string
	Output  string
	Stderr  string
	Err     error
}

func (e *NodeOutputError) Error() string {
	msg := fmt.Sprintf("node output error: %s", e.Reason)
	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}
	if e.Stderr != "" {
		runtime := e.Runtime
		if runtime == "" {
			runtime = string(RuntimeClaude)
		}
		msg += fmt.Sprintf(" (%s stderr: %s)", runtime, flattenLines(e.Stderr))
	}
	return msg
}

func (e *NodeOutputError) Unwrap() error { return e.Err }

// NodeTimeoutError reports the bound owned by the CLI runner expiring.
type NodeTimeoutError struct {
	Runtime string
	Timeout time.Duration
	Err     error
}

func (e *NodeTimeoutError) Error() string {
	runtime := e.Runtime
	if runtime == "" {
		runtime = string(RuntimeClaude)
	}
	return fmt.Sprintf("%s run: timed out after %s (node timeout)", runtime, e.Timeout)
}

func (e *NodeTimeoutError) Unwrap() error { return e.Err }

type cliProtocol interface {
	runtime() Runtime
	binary() string
	buildArgs(NodeInvocation) []string
	parse(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error)
}

// CLIRunner is the single model-CLI os/exec seam. Provider protocols own only
// argv and output decoding; cancellation, process groups, timeouts, exit codes,
// and environment policy stay identical.
type CLIRunner struct {
	protocol cliProtocol
	binary   string
	timeout  time.Duration
	environ  func() []string
}

// CLIOption configures a CLIRunner.
type CLIOption func(*CLIRunner)

// WithBinary overrides the selected protocol's executable.
func WithBinary(binary string) CLIOption {
	return func(r *CLIRunner) { r.binary = binary }
}

// WithTimeout overrides the default per-node timeout.
func WithTimeout(d time.Duration) CLIOption {
	return func(r *CLIRunner) { r.timeout = d }
}

func withEnviron(environ func() []string) CLIOption {
	return func(r *CLIRunner) { r.environ = environ }
}

// NewCLIRunner builds the production runner for one run-wide runtime.
func NewCLIRunner(runtime Runtime, opts ...CLIOption) *CLIRunner {
	var protocol cliProtocol = claudeProtocol{}
	if runtime == RuntimeCodex {
		protocol = codexProtocol{}
	}
	r := &CLIRunner{
		protocol: protocol,
		binary:   protocol.binary(),
		timeout:  defaultTimeout,
		environ:  os.Environ,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Compatibility aliases keep existing callers compiling while runtime
// selection is moved to the CLI boundary in the next task.
type ClaudeCLIRunner = CLIRunner
type ClaudeCLIOption = CLIOption

func NewClaudeCLIRunner(opts ...CLIOption) *CLIRunner {
	return NewCLIRunner(RuntimeClaude, opts...)
}

func (r *CLIRunner) buildCmd(ctx context.Context, spec NodeInvocation) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.binary, r.buildArgs(spec)...)
	cmd.Dir = spec.Cwd
	cmd.Env = childenv.Scrub(r.environ())
	setProcessGroup(cmd)
	cmd.Cancel = func() error { return killProcessGroup(cmd) }
	cmd.WaitDelay = waitDelay
	return cmd
}

func (r *CLIRunner) buildArgs(spec NodeInvocation) []string {
	return r.protocol.buildArgs(spec)
}

// Run executes and decodes one provider CLI invocation.
func (r *CLIRunner) Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error) {
	timeout := r.timeout
	if spec.Timeout > 0 {
		timeout = spec.Timeout
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := r.buildCmd(runCtx, spec)
	stdout, runErr := cmd.Output()

	exitCode := 0
	var stderr []byte
	if runErr != nil {
		if ctxErr := runCtx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) && ctx.Err() == nil {
				return NodeOutcome{}, &NodeTimeoutError{Runtime: string(r.protocol.runtime()), Timeout: timeout, Err: ctxErr}
			}
			return NodeOutcome{}, fmt.Errorf("%s run: %w", r.protocol.runtime(), ctxErr)
		}
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			return NodeOutcome{}, fmt.Errorf("%s run: spawn failed: %w", r.protocol.runtime(), runErr)
		}
		exitCode = exitErr.ExitCode()
		stderr = exitErr.Stderr
	}

	outcome, err := r.protocol.parse(stdout, stderr, spec.SessionStarted)
	if err != nil {
		return NodeOutcome{}, err
	}
	outcome.ExitCode = exitCode
	if exitCode != 0 && outcome.FailureCause == "" {
		outcome.FailureCause = flattenLines(tailOf(stderr, maxStderrInError))
	}
	if r.protocol.runtime() == RuntimeClaude {
		outcome.SessionLimited = isSessionLimitCause(outcome.FailureCause)
	}
	return outcome, nil
}

func flattenLines(s string) string {
	fields := make([]string, 0, 4)
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return strings.Join(fields, " / ")
}

func tailOf(b []byte, n int) string {
	trimmed := strings.TrimSpace(string(b))
	if len(trimmed) <= n {
		return trimmed
	}
	tail := trimmed[len(trimmed)-n:]
	for len(tail) > 0 && !utf8.RuneStart(tail[0]) {
		tail = tail[1:]
	}
	return "…(truncated) " + tail
}
