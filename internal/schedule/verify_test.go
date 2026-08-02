package schedule

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// Every test here drives a FakeVerifier: the whole evidence path is exercised
// with zero real spawns, exactly as the rest of the engine is exercised through
// FakeRunner. verified() is the "the command ran and succeeded" script.

func verified() verify.Result { return verify.Result{ExitCode: 0, Output: "ok\n"} }

// newVerifyHarness wires a scheduler with both doubles injected.
func newVerifyHarness(t *testing.T, nodeRunner runner.NodeRunner, verifier verify.Verifier, opts Options) (*Scheduler, *handoff.Handoff, *ledger.RunLedger) {
	t.Helper()
	opts.Verifier = verifier
	return newHarness(t, nodeRunner, opts)
}

// --- failure cases first ----------------------------------------------------

// TestScheduler_FailedVerificationFailsTheNode is the feature in one test: the
// node did everything it was asked, exited 0 and said PASS — and the run still
// stops, because the command the ENGINE ran says otherwise. Self-report loses to
// evidence.
func TestScheduler_FailedVerificationFailsTheNode(t *testing.T) {
	g := mustGraph(t, `
name: evidence
nodes:
  - id: dev
    prompt: dev
    success_check:
      exit_zero: true
      result_matches: "PASS"
      verify: { command: "make test" }
  - { id: ship, prompt: ship, depends_on: [dev] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev":  pass("s-dev", 0.10),
		"ship": pass("s-ship", 0.10),
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 1, Output: "FAIL TestFoo: expected 2, got 1\n"},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if halt.NodeID != "dev" {
		t.Fatalf("halt named node %q, want dev", halt.NodeID)
	}
	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) {
		t.Fatalf("expected halt to wrap *NodeCheckError, got %T: %v", err, err)
	}
	if checkErr.Predicate != predicateVerify {
		t.Errorf("failed predicate = %q, want %q", checkErr.Predicate, predicateVerify)
	}
	if indexOf(fake.Calls(), "ship") != -1 {
		t.Errorf("dependent of an unverified node must never run; calls=%v", fake.Calls())
	}

	// The ledger must say WHY, not just that: the command, its exit code, and
	// what it printed. Without this the user re-runs the command by hand to
	// learn something the run already knew.
	rec, ok := findRecord(led, "dev")
	if !ok {
		t.Fatal("dev was never recorded in the ledger")
	}
	if rec.Verdict != ledger.VerdictFail {
		t.Errorf("dev verdict = %s, want FAIL", rec.Verdict)
	}
	for _, want := range []string{"make test", "exited 1", "expected 2, got 1"} {
		if !strings.Contains(rec.Detail, want) {
			t.Errorf("ledger detail %q missing %q", rec.Detail, want)
		}
	}
}

// TestScheduler_FailedVerificationPersistsNoArtifact pins the lifecycle ORDER
// where it is observable: verification runs BEFORE Handoff.PersistOutput, so an
// unverified node leaves no .out file for a dependent to read. (Contrast with an
// over-budget node, which is judged AFTER persisting and does keep its
// artifact — the two verdicts sit deliberately on opposite sides of handoff.)
func TestScheduler_FailedVerificationPersistsNoArtifact(t *testing.T) {
	g := mustGraph(t, `
name: no-artifact
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 1, Output: "no"},
	})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard, Verifier: verifier})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the unverified node to fail the run")
	}

	if _, err := os.Stat(filepath.Join(runDir, "dev.out")); !os.IsNotExist(err) {
		t.Errorf("an unverified node must not leave an artifact behind (stat err = %v)", err)
	}
}

// TestScheduler_CheaperPredicatesFailBeforeAnythingIsRun proves the conjunction
// is ordered: a node that already failed exit_zero or result_matches never pays
// for a verification command. Running `make test` against the wreckage of a node
// that crashed costs wall-clock time to learn nothing.
func TestScheduler_CheaperPredicatesFailBeforeAnythingIsRun(t *testing.T) {
	cases := []struct {
		name    string
		outcome runner.NodeOutcome
	}{
		{name: "exit_zero failed", outcome: runner.NodeOutcome{Result: "PASS", ExitCode: 1}},
		{name: "result_matches failed", outcome: runner.NodeOutcome{Result: "NOPE", ExitCode: 0}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			g := mustGraph(t, `
name: ordered
nodes:
  - id: dev
    prompt: dev
    success_check:
      exit_zero: true
      result_matches: "PASS"
      verify: { command: "make test" }
`)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": tc.outcome})
			verifier := verify.NewFakeVerifier(map[string]verify.Result{"make test": verified()})
			s, h, led := newVerifyHarness(t, fake, verifier, Options{})

			if err := s.Run(context.Background(), g, h, led); err == nil {
				t.Fatal("expected the node to fail")
			}
			if got := verifier.Calls(); len(got) != 0 {
				t.Errorf("verification ran after a cheaper predicate had already failed: %v", got)
			}
		})
	}
}

// TestScheduler_CrashedNodeIsNeverVerified is the same rule for the harder case:
// the runner returned an ERROR, so there is no outcome at all. A verification
// command run here would be judging a node that never produced anything — and,
// worse, could pass and mask the crash.
func TestScheduler_CrashedNodeIsNeverVerified(t *testing.T) {
	g := mustGraph(t, `
name: crashed
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{})
	fake.InjectError("dev", errors.New("claude run: spawn failed"))
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"make test": verified()})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the crashed node to fail the run")
	}
	if got := verifier.Calls(); len(got) != 0 {
		t.Errorf("a crashed node must not have its verification run: %v", got)
	}
}

// TestScheduler_UnrunnableVerificationIsNeverASilentPass covers the third
// outcome, which is the dangerous one: the command did not fail, it never
// reached a verdict (timeout, unspawnable). Treating "no answer" as "yes" would
// make the whole predicate worthless precisely when the machine is unhealthy.
func TestScheduler_UnrunnableVerificationIsNeverASilentPass(t *testing.T) {
	g := mustGraph(t, `
name: unrunnable
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test", timeout: 30s }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(nil)
	verifier.InjectError("make test", &verify.TimeoutError{Command: "make test", Timeout: 30 * time.Second})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)

	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) || checkErr.Predicate != predicateVerify {
		t.Fatalf("a verification that could not complete must fail the node as a verify failure, got %T: %v", err, err)
	}
	if !strings.Contains(checkErr.Detail, "timed out") {
		t.Errorf("detail should explain that the command never finished, got %q", checkErr.Detail)
	}
}

// TestScheduler_MissingVerifierRefusesLoudly proves the default Verifier is the
// refusing one. A caller that wires a graph with verifications but forgets to
// inject a Verifier must get a loud failure — the alternative (a default that
// spawns) would put real subprocesses into a test suite that promises none.
func TestScheduler_MissingVerifierRefusesLoudly(t *testing.T) {
	g := mustGraph(t, `
name: unwired
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	s, h, led := newHarness(t, fake, Options{}) // no Verifier injected

	err := s.Run(context.Background(), g, h, led)
	if err == nil {
		t.Fatal("expected the run to fail with no Verifier wired in")
	}
	if !strings.Contains(err.Error(), "no Verifier was injected") {
		t.Errorf("failure should name the missing wiring, got %v", err)
	}
}

// TestScheduler_UnmatchedOutputFailsTheNode covers the second half of the
// judgement: the command exited as expected but did not print what the graph
// demanded. A test runner that exits 0 having run nothing is exactly the case
// output_matches exists for.
func TestScheduler_UnmatchedOutputFailsTheNode(t *testing.T) {
	g := mustGraph(t, `
name: output
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "go test ./...", output_matches: "^ok\\s+github" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"go test ./...": {ExitCode: 0, Output: "testing: warning: no tests to run\n"},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)

	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) || checkErr.Predicate != predicateVerify {
		t.Fatalf("expected a verify failure, got %T: %v", err, err)
	}
	if !strings.Contains(checkErr.Detail, "no tests to run") {
		t.Errorf("detail should quote what the command actually printed, got %q", checkErr.Detail)
	}
}

// TestJudgeVerification_InvalidRegexIsAnInfrastructureFault pins the
// taxonomy for the one judgement-path error that is not a verdict: a
// malformed output_matches (unreachable for a loaded graph — Validate
// compiles it — but reachable from a hand-built Node) rendered no judgment
// on the work, and no re-run can repair the declaration, so it must carry
// the Infrastructure mark that keeps a feedback arc from firing on it.
// The fault must win even when the exit code also mismatches — a broken
// declaration judged after the exit code would surface as a judgment
// failure and let a feedback arc fire on it.
func TestJudgeVerification_InvalidRegexIsAnInfrastructureFault(t *testing.T) {
	cases := map[string]verify.Result{
		"matching exit code":   {ExitCode: 0, Output: "ok\n"},
		"mismatched exit code": {ExitCode: 1, Output: "ok\n"},
	}
	for name, result := range cases {
		t.Run(name, func(t *testing.T) {
			err := judgeVerification("dev", graph.Verification{OutputMatches: "("}, "go test ./...", result)

			var checkErr *NodeCheckError
			if !errors.As(err, &checkErr) || checkErr.Predicate != predicateVerify {
				t.Fatalf("expected a verify check error, got %T: %v", err, err)
			}
			if !checkErr.Infrastructure {
				t.Fatalf("an invalid regex must be an infrastructure fault, not a judgment: %+v", checkErr)
			}
		})
	}
}

// TestScheduler_UnexpectedExitCodeFailsTheNode proves expect_exit is judged as
// declared, not as "zero": a graph that expects a command to FAIL (grep finding
// nothing, a should-not-compile check) fails when it unexpectedly succeeds.
func TestScheduler_UnexpectedExitCodeFailsTheNode(t *testing.T) {
	g := mustGraph(t, `
name: expects-failure
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "grep -q TODO src", expect_exit: 1 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"grep -q TODO src": {ExitCode: 0, Output: ""},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)

	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) || checkErr.Predicate != predicateVerify {
		t.Fatalf("expected a verify failure, got %T: %v", err, err)
	}
	if !strings.Contains(checkErr.Detail, "want 1") {
		t.Errorf("detail should state the exit code the graph expected, got %q", checkErr.Detail)
	}
}

// --- retry ------------------------------------------------------------------

// TestScheduler_VerifyFailureIgnoresNonzeroExitRetryPolicy is the regression
// guard for the new retry cause: verify_failed is its own token, so a graph
// written before this feature — with retry: { on: [nonzero_exit] } — does not
// silently start re-running nodes because their evidence check failed. A retry
// costs another full claude node.
func TestScheduler_VerifyFailureIgnoresNonzeroExitRetryPolicy(t *testing.T) {
	g := mustGraph(t, `
name: no-accidental-retry
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test" }
    retry: { max: 2, on: [nonzero_exit, result_mismatch] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 1, Output: "nope"},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the unverified node to fail the run")
	}
	if got := fake.InvocationCount("dev"); got != 1 {
		t.Errorf("dev invoked %d times, want 1 — a verify failure must not be retried "+
			"under a nonzero_exit policy", got)
	}
}

// TestScheduler_VerifyFailureRetriesWhenOptedIn proves the flip side: a graph
// that lists verify_failed does re-run the node — runner and verification both —
// and still fails once the attempts are exhausted.
func TestScheduler_VerifyFailureRetriesWhenOptedIn(t *testing.T) {
	g := mustGraph(t, `
name: opted-in-retry
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test" }
    retry: { max: 1, on: [verify_failed] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 1, Output: "nope"},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected failure after the retry also failed verification")
	}
	if got := fake.InvocationCount("dev"); got != 2 {
		t.Errorf("dev invoked %d times, want 2 (initial + 1 opted-in retry)", got)
	}
	if got := verifier.InvocationCount("make test"); got != 2 {
		t.Errorf("verification ran %d times, want 2 — every attempt must be re-verified", got)
	}
}

// --- cancellation -----------------------------------------------------------

// TestScheduler_HaltCancelsAnInFlightVerification proves the shared run context
// reaches the verification child. Without it, halt-on-fail would kill every
// claude subprocess and leave a `make test` running past the end of the run it
// belonged to.
//
// The two nodes are choreographed so the race cannot go the other way: `boom`
// does not fail until `slow` is already inside its verification.
func TestScheduler_HaltCancelsAnInFlightVerification(t *testing.T) {
	g := mustGraph(t, `
name: cancel
nodes:
  - { id: boom, prompt: boom, success_check: { exit_zero: true } }
  - id: slow
    prompt: slow
    success_check:
      verify: { command: "sleep forever" }
`)
	verifying := make(chan struct{})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"sleep forever": verified()})
	observedCancel := make(chan error, 1)
	verifier.OnVerify("sleep forever", func(ctx context.Context) {
		close(verifying)
		<-ctx.Done()
		observedCancel <- ctx.Err()
	})

	fake := &blockingRunner{
		outcomes: map[string]runner.NodeOutcome{
			"boom": {Result: "boom", ExitCode: 1},
			"slow": {Result: "PASS", ExitCode: 0},
		},
		waitBefore: map[string]chan struct{}{"boom": verifying},
	}
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)

	var halt *HaltError
	if !errors.As(err, &halt) || halt.NodeID != "boom" {
		t.Fatalf("expected a halt at boom, got %T: %v", err, err)
	}
	select {
	case ctxErr := <-observedCancel:
		if !errors.Is(ctxErr, context.Canceled) {
			t.Errorf("verification saw %v, want context.Canceled", ctxErr)
		}
	default:
		t.Fatal("the in-flight verification never saw the run's cancellation")
	}
}

// --- success path -----------------------------------------------------------

// TestScheduler_VerifiedNodePassesAndEnqueuesDependents is the happy path: the
// evidence holds, so the node passes, its artifact is persisted, and its
// dependents start — the verification changed nothing else about the lifecycle.
func TestScheduler_VerifiedNodePassesAndEnqueuesDependents(t *testing.T) {
	g := mustGraph(t, `
name: verified
nodes:
  - id: dev
    prompt: dev
    success_check:
      exit_zero: true
      result_matches: "PASS"
      verify: { command: "make test" }
  - { id: ship, prompt: ship, depends_on: [dev] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev":  pass("s-dev", 0.10),
		"ship": pass("s-ship", 0.10),
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"make test": verified()})
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard, Verifier: verifier})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if rec, ok := findRecord(led, "dev"); !ok || rec.Verdict != ledger.VerdictPass {
		t.Errorf("dev record = %+v (present=%v), want a PASS record", rec, ok)
	}
	if indexOf(fake.Calls(), "ship") == -1 {
		t.Errorf("dependent of a verified node should run; calls=%v", fake.Calls())
	}
	if _, err := os.Stat(filepath.Join(runDir, "dev.out")); err != nil {
		t.Errorf("a verified node's artifact should be persisted: %v", err)
	}
}

// TestScheduler_VerificationInheritsNodeCwdAndInterpolates pins what reaches the
// Verifier. The default cwd is the NODE's — a verification is about the work the
// node just did, so repeating the directory would be noise — and both fields
// interpolate, because a graph parameterised by {{ inputs.repo }} would
// otherwise have to hardcode the path in exactly one place.
func TestScheduler_VerificationInheritsNodeCwdAndInterpolates(t *testing.T) {
	g := mustGraph(t, `
name: cwd
inputs: [repo]
nodes:
  - id: inherits
    prompt: inherits
    cwd: "{{ inputs.repo }}"
    success_check:
      verify: { command: "go test ./{{ inputs.pkg }}/..." }
  - id: overrides
    prompt: overrides
    cwd: "{{ inputs.repo }}"
    success_check:
      verify: { command: "make lint", cwd: "{{ inputs.repo }}/tools" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"inherits":  pass("s-a", 0),
		"overrides": pass("s-b", 0),
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"go test ./engine/...": verified(),
		"make lint":            verified(),
	})
	h := handoff.New(t.TempDir(), map[string]string{"repo": "/src/omg", "pkg": "engine"})
	led := ledger.New("test")
	s := NewScheduler(fake, Options{ProgressWriter: io.Discard, Verifier: verifier})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	requests := make(map[string]verify.Request, 2)
	for _, req := range verifier.Calls() {
		requests[req.Command] = req
	}
	inherited, ok := requests["go test ./engine/..."]
	if !ok {
		t.Fatalf("the verification command was not interpolated; calls=%+v", verifier.Calls())
	}
	if inherited.Cwd != "/src/omg" {
		t.Errorf("verification cwd = %q, want the node's own interpolated cwd", inherited.Cwd)
	}
	if got := requests["make lint"].Cwd; got != "/src/omg/tools" {
		t.Errorf("declared verification cwd = %q, want /src/omg/tools", got)
	}
}

// TestScheduler_VerificationCarriesTheDeclaredTimeout proves the load-time
// parse reaches the Verifier: the graph says 45s, the request says 45s. A
// dropped timeout would silently fall back to a different bound than the graph
// declared.
func TestScheduler_VerificationCarriesTheDeclaredTimeout(t *testing.T) {
	g := mustGraph(t, `
name: timeout
nodes:
  - id: dev
    prompt: dev
    success_check:
      verify: { command: "make test", timeout: 45s }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"make test": verified()})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	calls := verifier.Calls()
	if len(calls) != 1 {
		t.Fatalf("expected exactly one verification, got %+v", calls)
	}
	if calls[0].Timeout.String() != "45s" {
		t.Errorf("verification timeout = %s, want 45s", calls[0].Timeout)
	}
}

// TestScheduler_GraphWithoutVerifyNeverTouchesTheVerifier is the
// backward-compatibility guard: v0.1 graphs are untouched by this feature, and
// nothing is spawned on their behalf. It is the reason the refusing default is
// safe to ship.
func TestScheduler_GraphWithoutVerifyNeverTouchesTheVerifier(t *testing.T) {
	g := mustGraph(t, `
name: legacy
nodes:
  - { id: a, prompt: a, success_check: { exit_zero: true, result_matches: "PASS" } }
  - { id: b, prompt: b, depends_on: [a] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a": pass("s-a", 0), "b": pass("s-b", 0),
	})
	verifier := verify.NewFakeVerifier(nil)
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := verifier.Calls(); len(got) != 0 {
		t.Errorf("a graph declaring no verification must never reach the Verifier: %v", got)
	}
}

// --- test doubles -----------------------------------------------------------

// blockingRunner is a FakeRunner-shaped double that can be made to wait on a
// channel before answering, so a test can choreograph which node reaches which
// lifecycle stage first instead of racing them.
//
// The wait blocks on the scripted channel or ctx.Done() only — deliberately no
// wall-clock fallback. A genuine deadlock is go test's own timeout's job, which
// dumps every goroutine naming the stuck line; a give-up arm would instead
// silently degrade the choreography into an unsynchronized race under CI load
// and then fail with a message blaming the product.
type blockingRunner struct {
	outcomes   map[string]runner.NodeOutcome
	waitBefore map[string]chan struct{}
}

func (r *blockingRunner) Run(ctx context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	if wait, ok := r.waitBefore[spec.Prompt]; ok {
		select {
		case <-wait:
		case <-ctx.Done():
			return runner.NodeOutcome{}, ctx.Err()
		}
	}
	outcome, ok := r.outcomes[spec.Prompt]
	if !ok {
		return runner.NodeOutcome{}, errors.New("blocking runner: no scripted outcome for " + spec.Prompt)
	}
	return outcome, nil
}
