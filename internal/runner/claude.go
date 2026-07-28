package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"
)

// defaultBinary is the claude executable name; defaultTimeout bounds a single
// node so one wedged child can never hang the whole graph (DESIGN: per-node
// context.WithTimeout ~20m).
const (
	defaultBinary  = "claude"
	defaultTimeout = 20 * time.Minute
)

// scrubbedEnvVars are the environment variables that silently switch the claude
// CLI from your logged-in subscription (OAuth) to metered API-key billing. They
// are DELETED from every child process env. This is the load-bearing
// subscription-auth guarantee of the whole project — asserted by a unit test on
// the built command (see claude_test.go).
var scrubbedEnvVars = []string{"ANTHROPIC_API_KEY", "ANTHROPIC_AUTH_TOKEN"}

// NodeOutputError marks a run whose subprocess produced output oh-my-graph could
// not turn into a NodeOutcome — non-JSON, a truncated envelope, or a spawn that
// never yielded a result. Never a silent zero outcome: an unreadable run is a
// node failure, and this names it as such.
type NodeOutputError struct {
	Reason string
	Output string
	Err    error
}

func (e *NodeOutputError) Error() string {
	if e.Err != nil {
		return fmt.Sprintf("node output error: %s: %v", e.Reason, e.Err)
	}
	return fmt.Sprintf("node output error: %s", e.Reason)
}

func (e *NodeOutputError) Unwrap() error { return e.Err }

// ClaudeCLIRunner runs a node as a real `claude -p ...` subprocess on the user's
// logged-in subscription. It is the ONLY object in oh-my-graph that imports
// os/exec; everything upstream depends on the NodeRunner interface, so the exec
// surface is a single, testable point.
type ClaudeCLIRunner struct {
	// binary is the claude executable name (a field, not a hardcoded literal, so
	// a test can point it at a stub without ever running real claude).
	binary string
	// timeout bounds one node run.
	timeout time.Duration
	// environ supplies the parent environment to scrub. A field (defaulting to
	// os.Environ) so a test can inject a parent env that DOES contain the API
	// keys and then assert they are gone from the built child env.
	environ func() []string
}

// ClaudeCLIOption configures a ClaudeCLIRunner at construction (functional
// options — the zero-config NewClaudeCLIRunner() is the production path).
type ClaudeCLIOption func(*ClaudeCLIRunner)

// WithBinary overrides the claude executable name.
func WithBinary(binary string) ClaudeCLIOption {
	return func(r *ClaudeCLIRunner) { r.binary = binary }
}

// WithTimeout overrides the per-node timeout.
func WithTimeout(d time.Duration) ClaudeCLIOption {
	return func(r *ClaudeCLIRunner) { r.timeout = d }
}

// withEnviron overrides the parent-environment source. Unexported: only tests
// need to inject a fake environ (production always reads the real os.Environ).
func withEnviron(environ func() []string) ClaudeCLIOption {
	return func(r *ClaudeCLIRunner) { r.environ = environ }
}

// NewClaudeCLIRunner builds a production runner: real `claude`, 20m timeout,
// scrubbing the real process environment.
func NewClaudeCLIRunner(opts ...ClaudeCLIOption) *ClaudeCLIRunner {
	r := &ClaudeCLIRunner{
		binary:  defaultBinary,
		timeout: defaultTimeout,
		environ: os.Environ,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// buildCmd assembles the exact *exec.Cmd for an invocation: argv, cwd, and the
// scrubbed child environment. It is the unit under test — claude_test.go calls
// it directly to assert both the argv AND that ANTHROPIC_API_KEY /
// ANTHROPIC_AUTH_TOKEN are absent from cmd.Env even when the parent process has
// them set. Nothing here spawns; Run wires it to the OS.
func (r *ClaudeCLIRunner) buildCmd(ctx context.Context, spec NodeInvocation) *exec.Cmd {
	cmd := exec.CommandContext(ctx, r.binary, r.buildArgs(spec)...)
	cmd.Dir = spec.Cwd
	cmd.Env = scrubEnv(r.environ())
	return cmd
}

// buildArgs is the argv (excluding the binary) for a node:
//
//	-p <prompt> --output-format json --permission-mode <mode>
//	  --allowedTools "<comma,joined>" [--resume <session_id>]
//
// Never --bare (disables OAuth) and never --no-session-persistence (fleetops
// observes the transcripts). --allowedTools is added only when tools are
// configured; --resume only when resuming a session-parent.
func (r *ClaudeCLIRunner) buildArgs(spec NodeInvocation) []string {
	args := []string{
		"-p", spec.Prompt,
		"--output-format", "json",
		"--permission-mode", spec.PermissionMode,
	}
	if len(spec.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(spec.AllowedTools, ","))
	}
	if spec.ResumeSession != "" {
		args = append(args, "--resume", spec.ResumeSession)
	}
	return args
}

// scrubEnv returns parent with every scrubbedEnvVar removed — the subscription-
// auth guarantee. Matching is on the KEY (the text before '='), so a value that
// happens to contain the name is untouched.
func scrubEnv(parent []string) []string {
	out := make([]string, 0, len(parent))
	for _, kv := range parent {
		if isScrubbed(kv) {
			continue
		}
		out = append(out, kv)
	}
	return out
}

func isScrubbed(kv string) bool {
	key, _, _ := strings.Cut(kv, "=")
	for _, scrubbed := range scrubbedEnvVars {
		if key == scrubbed {
			return true
		}
	}
	return false
}

// claudeEnvelope is the JSON oh-my-graph reads from `claude --output-format
// json`: the session id, the final result text, and the reported cost.
type claudeEnvelope struct {
	SessionID    string  `json:"session_id"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
}

// Run executes one node under a per-node timeout, then parses its JSON envelope.
//
// A non-zero exit is NOT a Run error: claude still emits a JSON envelope, so the
// outcome (with its ExitCode) is returned and the Scheduler's success_check
// decides pass/fail. Run returns an error only when there is no usable outcome:
// a context cancellation/timeout, a spawn failure, or output that is not a
// parseable envelope (*NodeOutputError).
func (r *ClaudeCLIRunner) Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error) {
	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	cmd := r.buildCmd(ctx, spec)
	stdout, runErr := cmd.Output()

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			// Context cancel/timeout or a failure to spawn at all — there is no
			// envelope to salvage. Prefer the context error so the Scheduler can
			// tell a halt-cancellation from a genuine run failure.
			if ctxErr := ctx.Err(); ctxErr != nil {
				return NodeOutcome{}, fmt.Errorf("claude run: %w", ctxErr)
			}
			return NodeOutcome{}, fmt.Errorf("claude run: spawn failed: %w", runErr)
		}
		// The process ran and exited non-zero. claude still printed its envelope
		// on stdout, so fall through and parse it, carrying the exit code.
		exitCode = exitErr.ExitCode()
	}

	outcome, err := parseEnvelope(stdout)
	if err != nil {
		return NodeOutcome{}, err
	}
	outcome.ExitCode = exitCode
	return outcome, nil
}

// parseEnvelope decodes the claude JSON envelope into a NodeOutcome, returning
// *NodeOutputError on anything that is not a valid envelope. ExitCode is left
// zero here — Run stamps it from the actual process state.
func parseEnvelope(stdout []byte) (NodeOutcome, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return NodeOutcome{}, &NodeOutputError{Reason: "claude produced no output"}
	}
	var env claudeEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return NodeOutcome{}, &NodeOutputError{
			Reason: "claude output was not a JSON envelope",
			Output: trimmed,
			Err:    err,
		}
	}
	return NodeOutcome{
		SessionID:    env.SessionID,
		Result:       env.Result,
		TotalCostUSD: env.TotalCostUSD,
	}, nil
}
