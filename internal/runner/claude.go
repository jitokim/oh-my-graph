package runner

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/childenv"
)

// defaultBinary is the claude executable name; defaultTimeout bounds a single
// node so one wedged child can never hang the whole graph (DESIGN: per-node
// context.WithTimeout ~20m).
const (
	defaultBinary  = "claude"
	defaultTimeout = 20 * time.Minute
)

// maxStderrInError caps how much of a failed child's stderr an error message
// carries — enough to show the CLI's own complaint without flooding the
// terminal with a long transcript.
const maxStderrInError = 500

// NodeOutputError marks a run whose subprocess produced output oh-my-graph could
// not turn into a NodeOutcome — non-JSON, a truncated envelope, or a spawn that
// never yielded a result. Never a silent zero outcome: an unreadable run is a
// node failure, and this names it as such.
type NodeOutputError struct {
	Reason string
	Output string
	// Stderr is the tail of what the child wrote to stderr, present only when
	// the child exited NON-ZERO (that is the one case exec.ExitError hands it
	// to us; a clean exit with unusable stdout carries no diagnosis). It turns
	// "claude produced no output" from a dead end into something actionable:
	// the CLI reports its own startup refusals there and exits before printing
	// an envelope. An unresolvable `agent:` is the case this was added for —
	// claude writes "--agent 'x' not found. Available agents: ..." to stderr,
	// exits 1, and prints nothing at all on stdout.
	Stderr string
	Err    error
}

// Error is deliberately ONE LINE. It is rendered into the scheduler's live
// progress feed and into the ledger's fixed-width DETAIL column, both of which
// a multi-line string would corrupt — so the captured stderr is flattened
// rather than block-quoted.
func (e *NodeOutputError) Error() string {
	msg := fmt.Sprintf("node output error: %s", e.Reason)
	if e.Err != nil {
		msg += fmt.Sprintf(": %v", e.Err)
	}
	if e.Stderr != "" {
		msg += fmt.Sprintf(" (claude stderr: %s)", flattenLines(e.Stderr))
	}
	return msg
}

// flattenLines collapses a multi-line string onto one line, joining non-empty
// lines with " / " so nothing is lost and no caller's layout is broken.
func flattenLines(s string) string {
	fields := make([]string, 0, 4)
	for _, line := range strings.Split(s, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return strings.Join(fields, " / ")
}

func (e *NodeOutputError) Unwrap() error { return e.Err }

// ClaudeCLIRunner runs a node as a real `claude -p ...` subprocess on the user's
// logged-in subscription. It is one of exactly TWO objects in oh-my-graph that
// may import os/exec (the other is verify.ShellVerifier, which runs a
// success_check.verify command — see docs/adr/0002); everything upstream
// depends on the NodeRunner interface, so the claude-exec surface stays a
// single, testable point.
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
	cmd.Env = childenv.Scrub(r.environ())
	return cmd
}

// buildArgs is the argv (excluding the binary) for a node:
//
//	-p <prompt> --output-format json --permission-mode <mode>
//	  [--max-budget-usd <amount>] [--setting-sources <sources>] [--agent <name>]
//	  [--allowedTools "<comma,joined>"] [--tools "<comma,joined>"]
//	  [--strict-mcp-config] [--disallowedTools "<comma,joined>"]
//	  [--resume <session_id>]
//
// The order above is the order emitted — keep the two in step, since
// TestBuildCmd_Argv pins the exact argv. Never --bare (disables OAuth) and
// never --no-session-persistence (fleetops observes the transcripts).
//
// Every bracketed flag is added only when its field is set, so a hand-written
// graph — whose ToolPolicy carries only AllowedTools — produces exactly the
// argv it always did. --max-budget-usd appears when a positive budget_usd is
// declared (claude then kills the run itself once its own spend crosses it —
// see NodeInvocation.BudgetUSD). The five ceiling flags are rendered in
// ToolPolicy's own layer order (isolate, grant, narrow, bound MCP, subtract) so
// a printed argv reads as the policy it enforces.
func (r *ClaudeCLIRunner) buildArgs(spec NodeInvocation) []string {
	args := []string{
		"-p", spec.Prompt,
		"--output-format", "json",
		"--permission-mode", spec.PermissionMode,
	}
	if spec.BudgetUSD > 0 {
		// 'f' with -1 precision renders the minimal round-tripping decimal
		// (0.5 -> "0.5", 0.001 -> "0.001") and never scientific notation, which
		// a very small budget would otherwise produce.
		args = append(args, "--max-budget-usd", strconv.FormatFloat(spec.BudgetUSD, 'f', -1, 64))
	}
	policy := spec.Policy
	if policy.SettingSources != nil {
		args = append(args, "--setting-sources", *policy.SettingSources)
	}
	if spec.Agent != "" {
		args = append(args, "--agent", spec.Agent)
	}
	if len(policy.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(policy.AllowedTools, ","))
	}
	if policy.Tools != nil {
		args = append(args, "--tools", strings.Join(policy.Tools, ","))
	}
	if policy.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
	if len(policy.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(policy.DisallowedTools, ","))
	}
	if spec.ResumeSession != "" {
		args = append(args, "--resume", spec.ResumeSession)
	}
	return args
}

// budgetExhaustedSubtype is the envelope subtype claude prints when its
// --max-budget-usd cap aborts a run mid-flight (observed on claude 2.1.220:
// exit code 1, a full JSON envelope with this subtype, total_cost_usd at or
// above the budget, and no result). parseEnvelope maps it to
// NodeOutcome.BudgetExhausted so the Scheduler can name it a budget failure
// rather than a generic non-zero exit.
const budgetExhaustedSubtype = "error_max_budget_usd"

// tailOf returns the LAST n bytes of b, trimmed, marking the cut. The tail
// rather than the head because the CLI prints startup warnings first and its
// actual complaint last, so a head-truncated stderr can drop the only line
// worth reading. The cut is advanced to the next rune boundary so a truncated
// non-ASCII message does not open with a replacement character.
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

// claudeEnvelope is the JSON oh-my-graph reads from `claude --output-format
// json`: the session id, the final result text, the reported cost, and the
// terminal subtype (used only to detect a --max-budget-usd abort).
type claudeEnvelope struct {
	SessionID    string  `json:"session_id"`
	Result       string  `json:"result"`
	TotalCostUSD float64 `json:"total_cost_usd"`
	Subtype      string  `json:"subtype"`
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
	var stderr []byte
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
		// The process ran and exited non-zero. claude usually still printed its
		// envelope on stdout, so fall through and parse it, carrying the exit
		// code — and keep stderr, which is the only explanation available when
		// it printed no envelope at all (cmd.Output captures it here because
		// cmd.Stderr is nil).
		exitCode = exitErr.ExitCode()
		stderr = exitErr.Stderr
	}

	outcome, err := parseEnvelope(stdout, stderr)
	if err != nil {
		return NodeOutcome{}, err
	}
	outcome.ExitCode = exitCode
	return outcome, nil
}

// parseEnvelope decodes the claude JSON envelope into a NodeOutcome, returning
// *NodeOutputError on anything that is not a valid envelope. stderr is carried
// into that error (never into a success) so a child that refused to start at
// all explains itself instead of surfacing as a bare "no output". ExitCode is
// left zero here — Run stamps it from the actual process state.
func parseEnvelope(stdout, stderr []byte) (NodeOutcome, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return NodeOutcome{}, &NodeOutputError{
			Reason: "claude produced no output",
			Stderr: tailOf(stderr, maxStderrInError),
		}
	}
	var env claudeEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return NodeOutcome{}, &NodeOutputError{
			Reason: "claude output was not a JSON envelope",
			Output: trimmed,
			Stderr: tailOf(stderr, maxStderrInError),
			Err:    err,
		}
	}
	return NodeOutcome{
		SessionID:       env.SessionID,
		Result:          env.Result,
		TotalCostUSD:    env.TotalCostUSD,
		BudgetExhausted: env.Subtype == budgetExhaustedSubtype,
	}, nil
}
