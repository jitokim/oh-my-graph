package runner

import (
	"bytes"
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

// DefaultTimeout is the per-attempt wall-clock bound the CLIRunner applies to a
// node that declares no `timeout:`. Read it through EffectiveTimeout, the one
// place a node's configured timeout becomes the applied one.
const DefaultTimeout = 20 * time.Minute

// EffectiveTimeout maps a node's configured per-attempt timeout to the bound
// the runner applies: the configured value when there is one, DefaultTimeout
// otherwise. It is the single source the CLIRunner's kill, the preflight's
// runaway-guard message and {{ self.timeout }} (#292) all read, so the value a
// prompt quotes and the time the node is killed cannot drift apart.
func EffectiveTimeout(configured time.Duration) time.Duration {
	return applyTimeout(configured, DefaultTimeout)
}

// applyTimeout is EffectiveTimeout with the fallback left open, for the one
// caller that may hold a different one: a CLIRunner built WithTimeout (tests
// only — production builds it without, so its fallback is DefaultTimeout).
func applyTimeout(configured, fallback time.Duration) time.Duration {
	if configured > 0 {
		return configured
	}
	return fallback
}

const (
	maxStderrInError = 500
	// maxStderrRetained bounds what one node's stderr may cost in memory. A
	// node can stream progress to stderr for twenty minutes, so collecting it
	// whole to read 500 bytes of it is unbounded growth for a fixed need. The
	// cap is deliberately far above maxStderrInError, the only amount any
	// consumer reads today: a human handed a crash wants the surrounding
	// context, not one truncated line, and 32 KiB is exactly the tail
	// cmd.Output's prefixSuffixSaver kept before stderr was collected by hand
	// here — restoring the old ceiling rather than inventing a tighter one.
	maxStderrRetained = 32 << 10
	waitDelay         = 2 * time.Second
	// spawnAttempts and spawnRetryDelay bound the one retry this seam owns: the
	// runtime binary not resolving on PATH (exec.ErrNotFound), which is what an
	// auto-update looks like from outside while it swaps `claude` or `codex` in
	// place (#298). Five lookups one second apart cover a ~4s window. A binary
	// that is genuinely absent costs those four seconds before the same error
	// surfaces as before, and preflight has normally said so already.
	spawnAttempts   = 5
	spawnRetryDelay = time.Second
)

// tailBuffer is an io.Writer that retains at most the last limit bytes written
// to it. The tail, not the head, is what survives: every consumer of a node's
// stderr reads it through tailOf, and a CLI's fatal line is its last one.
//
// Build one with newTailBuffer; the zero value is not usable.
type tailBuffer struct {
	limit int
	buf   []byte
}

// newTailBuffer returns a buffer retaining the last limit bytes. The
// constructor exists so the window cannot be forgotten: a zero limit makes
// `n >= b.limit` true for every write, so a `tailBuffer{}` that compiles fine
// would silently discard a whole node's stderr — the same reason MaxCycles has
// no unbounded spelling. A non-positive limit is a programmer error at a call
// site that passes a constant, so it fails loudly at the first exercise rather
// than quietly at a crash nobody can then explain.
func newTailBuffer(limit int) *tailBuffer {
	if limit < 1 {
		panic(fmt.Sprintf("newTailBuffer: retention limit must be at least 1, got %d", limit))
	}
	return &tailBuffer{limit: limit}
}

func (b *tailBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if n >= b.limit {
		b.buf = append(b.buf[:0], p[n-b.limit:]...)
		return n, nil
	}
	b.buf = append(b.buf, p...)
	// Trim in bulk at twice the limit rather than on every write, so a node
	// writing many small lines pays one copy per limit bytes, not one per
	// write. Peak retention is therefore 2*limit, still a constant.
	if len(b.buf) > 2*b.limit {
		b.buf = append(b.buf[:0], b.buf[len(b.buf)-b.limit:]...)
	}
	return n, nil
}

// Bytes returns the retained tail, never more than limit bytes.
//
// The result ALIASES the buffer's storage and both trim paths rewrite that
// storage in place, so a slice taken here changes under the caller after the
// next Write — unlike the bytes.Buffer this replaced, whose Bytes() stayed
// valid until the next append reallocated. Read it after the process has
// exited, or copy it. Every call site today is safe on both counts: Run touches
// stderr only after cmd.Run has returned, and tailOf copies by converting to
// string. Anyone holding a tail ACROSS writes must copy first.
func (b *tailBuffer) Bytes() []byte {
	if len(b.buf) > b.limit {
		return b.buf[len(b.buf)-b.limit:]
	}
	return b.buf
}

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

// NodeSpawnError reports that the provider's CLI never started: the executable
// could not be found or executed, so no model was reached and no reply exists
// to judge.
//
// It is typed for the same reason NodeTimeoutError above it is — a caller that
// must tell "the CLI answered badly" from "the CLI never ran" cannot do it on a
// message string. The distinction was already made here (the non-*exec.ExitError
// branch), and was already load-bearing; it just had no name a caller could
// match on, so the only way to act on it was to match prose.
//
// The case that named it: an npm update replaced `claude` on PATH mid-run, and
// the goal loop's assessor spawn failed on a binary that had existed twenty
// minutes earlier and existed again a second later (#214). Retrying a spawn is
// safe in a way that retrying a reply is NOT — see coordinator.Assess.
type NodeSpawnError struct {
	Runtime string
	Err     error
}

func (e *NodeSpawnError) Error() string {
	runtime := e.Runtime
	if runtime == "" {
		runtime = string(RuntimeClaude)
	}
	return fmt.Sprintf("%s run: spawn failed: %v", runtime, e.Err)
}

func (e *NodeSpawnError) Unwrap() error { return e.Err }

type cliProtocol interface {
	runtime() Runtime
	binary() string
	prepareSession(*NodeInvocation) string
	sessionFromLine([]byte) string
	buildArgs(NodeInvocation) []string
	parse(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error)
	// isLimitCause answers whether a failure cause THIS protocol produced is
	// its provider's subscription limit — implemented in sessionlimit.go, where
	// both runtimes' wording lives together (ADR 0009).
	isLimitCause(cause string) bool
}

// protocolOutput retains stdout for terminal decoding while publishing any
// session-creation event as soon as its complete line arrives. The protocol,
// not the scheduler, decides which line creates a session.
type protocolOutput struct {
	protocol cliProtocol
	report   func(string)
	all      bytes.Buffer
	pending  []byte
}

func (w *protocolOutput) Write(p []byte) (int, error) {
	n, err := w.all.Write(p)
	w.pending = append(w.pending, p...)
	for {
		newline := bytes.IndexByte(w.pending, '\n')
		if newline < 0 {
			break
		}
		w.observe(w.pending[:newline])
		w.pending = w.pending[newline+1:]
	}
	return n, err
}

func (w *protocolOutput) finish() {
	if len(w.pending) > 0 {
		w.observe(w.pending)
		w.pending = nil
	}
}

func (w *protocolOutput) observe(line []byte) {
	if id := w.protocol.sessionFromLine(bytes.TrimSuffix(line, []byte{'\r'})); id != "" {
		w.report(id)
	}
}

// CLIRunner is the single model-CLI os/exec seam. Provider protocols own only
// argv and output decoding; cancellation, process groups, timeouts, exit codes,
// and environment policy stay identical.
type CLIRunner struct {
	protocol cliProtocol
	binary   string
	timeout  time.Duration
	environ  func() []string
	// pause waits between missing-binary retries; a test swaps it for a hook
	// that installs the binary or counts the waits.
	pause func(context.Context, time.Duration) error
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

func withSpawnPause(pause func(context.Context, time.Duration) error) CLIOption {
	return func(r *CLIRunner) { r.pause = pause }
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
		timeout:  DefaultTimeout,
		environ:  os.Environ,
		pause:    sleepCtx,
	}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// lookPath resolves a command name against PATH. It is a variable rather than a
// direct call so a test can drive both branches without depending on which CLIs
// happen to be installed on the machine running the suite.
var lookPath = exec.LookPath

// CheckCLIAvailable answers, without spawning anything, whether the provider CLI
// this runner would spawn can be found at all. It is a PATH lookup and nothing
// else, which is why it lives here beside the spawn it speaks for and adds no
// new process-starting object to the program.
//
// What it CANNOT answer is whether that CLI is signed in. A signed-out CLI is on
// PATH and starts perfectly well; it fails afterwards as an ordinary non-zero
// exit carrying the provider's own words, and no check short of running it tells
// the two apart. CLINotFoundError's text is written to claim only the narrower
// thing.
//
// A lookup that reports exec.ErrNotFound is retried on the same bound as a spawn
// (retryMissingBinary), so a CLI caught mid auto-update does not refuse a run
// that would have started a second later (#298).
func (r *CLIRunner) CheckCLIAvailable() error {
	err := r.retryMissingBinary(context.Background(), func() error {
		_, err := lookPath(r.binary)
		return err
	})
	if err != nil {
		return &CLINotFoundError{Runtime: r.protocol.runtime(), Binary: r.binary, Err: err}
	}
	return nil
}

// retryMissingBinary calls attempt until it returns anything other than
// exec.ErrNotFound, or spawnAttempts calls in a row have all returned it, or ctx
// ends during a pause. It returns attempt's last error unchanged.
//
// Only exec.ErrNotFound is retried, because it is the one failure that proves no
// process ran: the name did not resolve on PATH, so nothing was started, nothing
// was billed, and asking again asks for the first time. A non-zero exit, an
// unparseable reply, a timeout or any other start failure (a binary that exists
// but is not executable, a missing working directory) still fails on the first
// try. The coordinator's own NodeSpawnError retry (runAssessorWithSpawnRetry)
// sits on top of this one, so the two bounds multiply for a planner or assessor
// call; both are finite.
func (r *CLIRunner) retryMissingBinary(ctx context.Context, attempt func() error) error {
	for n := 1; ; n++ {
		err := attempt()
		if !errors.Is(err, exec.ErrNotFound) || n >= spawnAttempts {
			return err
		}
		if r.pause(ctx, spawnRetryDelay) != nil {
			return err
		}
	}
}

// sleepCtx waits d, or until ctx ends, whichever comes first.
func sleepCtx(ctx context.Context, d time.Duration) error {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
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
	timeout := applyTimeout(spec.Timeout, r.timeout)
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	reportedSession := false
	sessionID := ""
	reportSession := func(id string) {
		if reportedSession || id == "" {
			return
		}
		sessionID = id
		reportedSession = true
		if spec.SessionStarted != nil {
			spec.SessionStarted(id)
		}
	}
	if id := r.protocol.prepareSession(&spec); id != "" {
		reportSession(id)
	}

	stdout := protocolOutput{protocol: r.protocol, report: reportSession}
	stderr := newTailBuffer(maxStderrRetained)
	// A fresh Cmd per attempt: exec.Command resolves the binary when it is
	// built, and a Cmd runs once. The writers are shared safely because a
	// retried attempt never started a process, so it wrote nothing to them.
	runErr := r.retryMissingBinary(runCtx, func() error {
		cmd := r.buildCmd(runCtx, spec)
		cmd.Stdout = &stdout
		cmd.Stderr = stderr
		return cmd.Run()
	})
	stdout.finish()

	exitCode := 0
	if runErr != nil {
		if ctxErr := runCtx.Err(); ctxErr != nil {
			if errors.Is(ctxErr, context.DeadlineExceeded) && ctx.Err() == nil {
				return NodeOutcome{SessionID: sessionID, CostUnknown: true}, &NodeTimeoutError{Runtime: string(r.protocol.runtime()), Timeout: timeout, Err: ctxErr}
			}
			return NodeOutcome{SessionID: sessionID, CostUnknown: true}, fmt.Errorf("%s run: %w", r.protocol.runtime(), ctxErr)
		}
		var exitErr *exec.ExitError
		if !errors.As(runErr, &exitErr) {
			// Same branch, same message — now carrying a type a caller can
			// match on instead of a sentence it would have to parse.
			return NodeOutcome{SessionID: sessionID}, &NodeSpawnError{Runtime: string(r.protocol.runtime()), Err: runErr}
		}
		exitCode = exitErr.ExitCode()
	}

	outcome, err := r.protocol.parse(stdout.all.Bytes(), stderr.Bytes(), reportSession)
	if err != nil {
		return NodeOutcome{SessionID: sessionID, CostUnknown: true}, err
	}
	if exitCode != 0 {
		outcome.ExitCode = exitCode
	}
	if exitCode != 0 && outcome.FailureCause == "" {
		outcome.FailureCause = flattenLines(tailOf(stderr.Bytes(), maxStderrInError))
	}
	// One classification site (ADR 0009), and no runtime branch at it: the
	// protocol that decoded this output is the one asked whether it is a limit,
	// so which prose belongs to which CLI stays in sessionlimit.go and nothing
	// downstream of NodeOutcome.SessionLimited ever sees a runtime-specific
	// sentence it would have to re-interpret.
	outcome.SessionLimited = r.protocol.isLimitCause(outcome.FailureCause)
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
