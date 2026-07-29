package verify

import (
	"context"
	"fmt"
	"sync"
)

// FakeVerifier is the scripted Verifier that keeps the whole verify path
// spawn-free in tests — the mirror of runner.FakeRunner, and the reason a
// scheduler test can prove "the node failed its evidence check" without a real
// process anywhere in CI.
//
// It is keyed by the command string, because that is what a graph declares and
// therefore the only thing a test can predict: script a Result for
// "go test ./..." and every node whose verify command interpolates to exactly
// that gets it. An unscripted command is an error, not a silent pass — a test
// that mistypes a command must fail loudly rather than accidentally proving
// nothing.
type FakeVerifier struct {
	// results maps a command to the Result Verify should return for it.
	results map[string]Result
	// errs maps a command to an error Verify should return instead of a Result
	// (injected timeouts / spawn failures).
	errs map[string]error
	// hooks maps a command to a function run before it is answered, so a test can
	// block a verification and prove the run context reaches it.
	hooks map[string]func(ctx context.Context)

	mu       sync.Mutex
	calls    []Request
	invokedN map[string]int
}

// NewFakeVerifier builds a FakeVerifier from a scripted result map. The map is
// copied so a test can keep mutating its literal without affecting the verifier.
func NewFakeVerifier(results map[string]Result) *FakeVerifier {
	copied := make(map[string]Result, len(results))
	for command, result := range results {
		copied[command] = result
	}
	return &FakeVerifier{
		results:  copied,
		errs:     make(map[string]error),
		hooks:    make(map[string]func(ctx context.Context)),
		invokedN: make(map[string]int),
	}
}

// InjectError scripts Verify to return err for the given command, simulating a
// timeout or a command that could not be spawned.
func (f *FakeVerifier) InjectError(command string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[command] = err
}

// OnVerify registers a hook run (outside the lock) before the given command is
// answered. A hook that blocks on ctx.Done() is how a test proves the shared run
// context reaches the verification, so halt-on-fail kills it like any child.
func (f *FakeVerifier) OnVerify(command string, hook func(ctx context.Context)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.hooks[command] = hook
}

// Verify returns the scripted result (or error) for the request's command,
// recording the call. It respects ctx: an already-cancelled run gets the context
// error rather than a verdict, exactly as ShellVerifier would.
func (f *FakeVerifier) Verify(ctx context.Context, req Request) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}

	f.mu.Lock()
	f.calls = append(f.calls, req)
	f.invokedN[req.Command]++
	hook := f.hooks[req.Command]
	injected := f.errs[req.Command]
	result, ok := f.results[req.Command]
	f.mu.Unlock()

	if hook != nil {
		hook(ctx)
	}
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	if injected != nil {
		return Result{}, injected
	}
	if !ok {
		return Result{}, fmt.Errorf("fake verifier: no scripted result for command %q", req.Command)
	}
	return result, nil
}

// Calls returns every request the verifier was asked to run, in order — the
// evidence a lifecycle test asserts against (that a crashed node's command was
// never run, that cwd was inherited, and so on). Returns a copy; never nil.
func (f *FakeVerifier) Calls() []Request {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]Request, len(f.calls))
	copy(out, f.calls)
	return out
}

// InvocationCount reports how many times a command was verified — used to assert
// retry behaviour (a node retried once verifies twice).
func (f *FakeVerifier) InvocationCount(command string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.invokedN[command]
}
