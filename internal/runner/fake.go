package runner

import (
	"context"
	"fmt"
	"sync"
)

// FakeRunner is the scripted NodeRunner that makes the Scheduler fully testable
// without ever spawning claude. It maps a node key to a canned NodeOutcome (or a
// canned error), records the order in which nodes were invoked, and honours
// context cancellation so halt-on-fail tests can prove in-flight siblings are
// cut off.
//
// The Scheduler passes no node id — NodeInvocation is opaque to it — so
// FakeRunner keys on a caller-supplied key derived from the invocation
// itself. Tests set KeyFn to map an invocation to its node id (default: the
// Prompt string).
type FakeRunner struct {
	// outcomes maps a node key to the outcome Run should return for it.
	outcomes map[string]NodeOutcome
	// errs maps a node key to an error Run should return instead of an outcome
	// (injected failures: spawn/parse/timeout-shaped errors).
	errs map[string]error
	// KeyFn extracts the node key from an invocation. Defaults to spec.Prompt.
	KeyFn func(spec NodeInvocation) string

	mu          sync.Mutex
	calls       []string
	invocations []NodeInvocation
	invokedN    map[string]int
}

// NewFakeRunner builds a FakeRunner from a scripted outcome map. The map is
// copied so a test can keep mutating its literal without affecting the runner.
func NewFakeRunner(outcomes map[string]NodeOutcome) *FakeRunner {
	copied := make(map[string]NodeOutcome, len(outcomes))
	for k, v := range outcomes {
		copied[k] = v
	}
	return &FakeRunner{
		outcomes: copied,
		errs:     make(map[string]error),
		invokedN: make(map[string]int),
	}
}

// InjectError scripts Run to return err for the given node key, simulating a
// spawn/parse/timeout failure the Scheduler must treat as a run error.
func (f *FakeRunner) InjectError(key string, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.errs[key] = err
}

// SetOutcome overrides (or adds) the scripted outcome for a node key.
func (f *FakeRunner) SetOutcome(key string, outcome NodeOutcome) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.outcomes[key] = outcome
}

// key resolves the node key for an invocation via KeyFn, defaulting to Prompt.
func (f *FakeRunner) key(spec NodeInvocation) string {
	if f.KeyFn != nil {
		return f.KeyFn(spec)
	}
	return spec.Prompt
}

// Run returns the scripted outcome (or error) for the invocation's node key,
// recording the call. It respects ctx: if the context is already cancelled it
// returns the context error, so a halt-on-fail test can assert that siblings
// launched after the failure never produced a real outcome.
func (f *FakeRunner) Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error) {
	if err := ctx.Err(); err != nil {
		return NodeOutcome{}, err
	}

	key := f.key(spec)

	f.mu.Lock()
	f.calls = append(f.calls, key)
	f.invocations = append(f.invocations, spec)
	f.invokedN[key]++
	injected := f.errs[key]
	outcome, ok := f.outcomes[key]
	f.mu.Unlock()

	if injected != nil {
		return NodeOutcome{}, injected
	}
	if !ok {
		return NodeOutcome{}, fmt.Errorf("fake runner: no scripted outcome for node %q", key)
	}
	startedID := outcome.SessionID
	if spec.ResumeSession != "" {
		startedID = spec.ResumeSession
	}
	if spec.SessionStarted != nil && startedID != "" {
		spec.SessionStarted(startedID)
	}
	return outcome, nil
}

// Calls returns the node keys in the order Run was invoked — the evidence a
// topological-order test asserts against. Returns a copy; never nil.
func (f *FakeRunner) Calls() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.calls))
	copy(out, f.calls)
	return out
}

// Invocations returns every NodeInvocation Run received, in order — what a
// test asserts against when the fact under test lives on the invocation
// itself (the cwd a worktree node was handed, say) rather than in the call
// order. Returns a copy; never nil.
func (f *FakeRunner) Invocations() []NodeInvocation {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]NodeInvocation, len(f.invocations))
	copy(out, f.invocations)
	return out
}

// InvocationCount reports how many times a node key was run — used to assert
// retry behaviour (a node retried once is invoked twice).
func (f *FakeRunner) InvocationCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.invokedN[key]
}
