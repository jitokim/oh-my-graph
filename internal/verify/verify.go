// Package verify is the evidence seam: it runs a node's success_check.verify
// command and reports what actually happened, so a node's success can be judged
// on something other than the model's own narration.
//
// It is deliberately NOT part of the NodeRunner seam. A verification command is
// not a claude invocation, and teaching NodeRunner to also run arbitrary shell
// would give it — and every FakeRunner in the test suite — two unrelated
// reasons to change. Instead verification gets its own, narrower interface with
// its own single exec-owning implementation. See docs/adr/0002.
//
// The project invariant this restates (it does not weaken it): exactly two
// objects in oh-my-graph may spawn a process — runner.ClaudeCLIRunner and
// verify.ShellVerifier — each behind its own injected interface, each scrubbing
// the child environment through internal/childenv. No other package imports
// os/exec.
package verify

import (
	"context"
	"errors"
	"time"
)

// Request is one verification: the command to run, where to run it, and how
// long it may take. It carries no node id — the Verifier's job is to run a
// command and report the facts, not to know what the result will be used for.
type Request struct {
	// Command is the shell command line, already interpolated by the caller.
	Command string
	// Cwd is the working directory. Empty means the process's own.
	Cwd string
	// Timeout bounds this one command. Non-positive means "use the
	// implementation's default".
	Timeout time.Duration
}

// Result is what a verification command actually did: the facts the caller
// judges. It is deliberately not a pass/fail — the expectations
// (expect_exit / output_matches) live with the node that declared them.
type Result struct {
	// ExitCode is the command's exit status.
	ExitCode int
	// Output is the combined stdout+stderr, bounded so a chatty test suite
	// cannot balloon the run's memory or the ledger's detail column.
	Output string
}

// Verifier runs a verification command. Verify returns a Result whenever the
// command RAN (including a non-zero exit — that is a fact, not an error) and an
// error only when there is no result to judge: a timeout, a cancellation, or a
// failure to spawn at all. A caller must treat that error as a node failure,
// never as a pass.
type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

// ErrNoVerifier is the sentinel behind RefusingVerifier's refusal, so a caller
// can match on it without string matching.
var ErrNoVerifier = errors.New(
	"no Verifier was injected, so success_check.verify cannot be evaluated; " +
		"wire verify.NewShellVerifier() into schedule.Options.Verifier",
)

// NotConfiguredError names the command a run tried to verify with no Verifier
// wired in. It unwraps to ErrNoVerifier.
type NotConfiguredError struct {
	Command string
}

func (e *NotConfiguredError) Error() string {
	return "cannot verify " + e.Command + ": " + ErrNoVerifier.Error()
}

func (e *NotConfiguredError) Unwrap() error { return ErrNoVerifier }

// RefusingVerifier is the Scheduler's default Verifier: it refuses every
// request instead of running anything. It mirrors the gate stub, and it exists
// so that forgetting to inject a real Verifier fails loudly and locally rather
// than silently spawning a process from a test — which is exactly how a suite
// that promises "zero real spawns" starts quietly breaking that promise.
//
// A graph with no success_check.verify never reaches a Verifier at all, so this
// default costs hand-written graphs nothing.
type RefusingVerifier struct{}

// NewRefusingVerifier returns the refuse-everything default Verifier.
func NewRefusingVerifier() RefusingVerifier { return RefusingVerifier{} }

// Verify always fails, naming the command it declined to run.
func (RefusingVerifier) Verify(_ context.Context, req Request) (Result, error) {
	return Result{}, &NotConfiguredError{Command: req.Command}
}
