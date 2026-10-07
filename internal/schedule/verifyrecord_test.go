package schedule

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// Every test here pins #332: the engine's own record of a node's
// success_check.verify in state.json — FakeRunner and FakeVerifier only.

// retainedOutputBound is verify's byte bound on a retained output tail,
// restated so a test can assert the record honours it.
const retainedOutputBound = 4096

// runVerified runs a one-node graph whose node declares verifyYAML and
// returns the snapshot record the scheduler handed the Recorder for it.
func runVerified(t *testing.T, verifyYAML string, verifier verify.Verifier, opts Options) (runstate.NodeRecord, error) {
	t.Helper()
	g := mustGraph(t, "name: v\nnodes:\n  - id: dev\n    prompt: dev\n    success_check:\n      verify: "+verifyYAML+"\n")
	recorder := newFakeRecorder()
	opts.Recorder = recorder
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0.1)})
	s, h, led := newVerifyHarness(t, fake, verifier, opts)
	err := s.Run(context.Background(), g, h, led)
	rec, ok := recorder.nodes["dev"]
	if !ok {
		t.Fatalf("dev was never recorded in the snapshot (run err: %v)", err)
	}
	return rec, err
}

// TestVerificationRecord_VerifiedPassRecordsExitZeroAndTail (#332): a PASS
// reached on a verify leaves the evidence in state.json — the command as run,
// exit 0, the expected code, how long it took, and what it printed.
func TestVerificationRecord_VerifiedPassRecordsExitZeroAndTail(t *testing.T) {
	clock := &fakeClock{t: time.Date(2026, 10, 7, 12, 0, 0, 0, time.UTC)}
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"go test ./...": {ExitCode: 0, Output: "ok  \tgithub.com/x/y\t0.2s\nPASS\n"},
	})
	verifier.OnVerify("go test ./...", func(context.Context) { clock.Advance(3 * time.Second) })

	rec, err := runVerified(t, `{ command: "go test ./..." }`, verifier, Options{Now: clock.Now})

	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if rec.Verdict != runstate.VerdictPass {
		t.Fatalf("verdict = %s, want PASS", rec.Verdict)
	}
	v := rec.Verification
	if v == nil {
		t.Fatal("a verified PASS recorded no verification")
	}
	if v.Command != "go test ./..." || v.Status != runstate.VerificationPassed || v.ExpectedExitCode != 0 {
		t.Errorf("record = %+v, want command, status passed, expected 0", v)
	}
	if v.ExitCode == nil || *v.ExitCode != 0 {
		t.Errorf("exit code = %v, want a recorded 0", v.ExitCode)
	}
	if v.Duration != 3*time.Second {
		t.Errorf("duration = %s, want the 3s the command took", v.Duration)
	}
	if !strings.HasSuffix(v.OutputTail, "PASS\n") {
		t.Errorf("output tail = %q, want what the command printed", v.OutputTail)
	}
	if v.OutputTruncated {
		t.Error("an output kept whole was recorded as truncated (#332)")
	}
}

// TestVerificationRecord_JudgedFailRecordsTheNonZeroExit (#332): a verify
// judged failed on its exit code records the code it really exited with,
// next to the one the node expected.
func TestVerificationRecord_JudgedFailRecordsTheNonZeroExit(t *testing.T) {
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"make test": {ExitCode: 2, Output: "FAIL TestFoo\n"},
	})

	rec, err := runVerified(t, `{ command: "make test" }`, verifier, Options{})

	if err == nil {
		t.Fatal("expected the run to fail on the verify")
	}
	if rec.Verdict != runstate.VerdictFail || !rec.Judged {
		t.Fatalf("record = %+v, want a judged FAIL", rec)
	}
	v := rec.Verification
	if v == nil || v.Status != runstate.VerificationFailed {
		t.Fatalf("verification = %+v, want status failed", v)
	}
	if v.ExitCode == nil || *v.ExitCode != 2 || v.ExpectedExitCode != 0 {
		t.Errorf("exit code %v / expected %d, want 2 / 0", v.ExitCode, v.ExpectedExitCode)
	}
	if !strings.Contains(v.OutputTail, "FAIL TestFoo") {
		t.Errorf("output tail = %q, want the failing output", v.OutputTail)
	}
}

// TestVerificationRecord_OutputMismatchRecordsTheZeroExit (#332): a verify
// that exited as expected but whose output did not match is still a judged
// failure, and its exit code — 0 — is recorded, not dropped.
func TestVerificationRecord_OutputMismatchRecordsTheZeroExit(t *testing.T) {
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"check": {ExitCode: 0, Output: "verdict: DRIFT\n"},
	})

	rec, _ := runVerified(t, `{ command: "check", output_matches: "verdict: OK" }`, verifier, Options{})

	v := rec.Verification
	if v == nil || v.Status != runstate.VerificationFailed {
		t.Fatalf("verification = %+v, want status failed", v)
	}
	if v.ExitCode == nil || *v.ExitCode != 0 {
		t.Errorf("exit code = %v, want a recorded 0", v.ExitCode)
	}
}

// TestVerificationRecord_ExpectedExitIsRecorded (#332): a node that expects
// a non-zero exit records that expectation, so a reader can judge the exit
// code without opening the graph.
func TestVerificationRecord_ExpectedExitIsRecorded(t *testing.T) {
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"probe": {ExitCode: 3}})

	rec, err := runVerified(t, `{ command: "probe", expect_exit: 3 }`, verifier, Options{})

	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	v := rec.Verification
	if v == nil || v.Status != runstate.VerificationPassed || v.ExpectedExitCode != 3 || v.ExitCode == nil || *v.ExitCode != 3 {
		t.Fatalf("verification = %+v, want passed with exit 3 of expected 3", v)
	}
	if v.OutputTail != "" {
		t.Errorf("a command that printed nothing recorded tail %q", v.OutputTail)
	}
}

// TestVerificationRecord_TimeoutRecordsNoExitCode (#332): a verify that
// timed out reached no verdict and never exited, so the record says
// `timed out`, carries NO exit code, and keeps the bounded output the
// timeout captured.
func TestVerificationRecord_TimeoutRecordsNoExitCode(t *testing.T) {
	verifier := verify.NewFakeVerifier(nil)
	verifier.InjectError("make e2e", &verify.TimeoutError{Command: "make e2e", Timeout: time.Minute, Output: "waiting for server…"})

	rec, err := runVerified(t, `{ command: "make e2e" }`, verifier, Options{})

	if err == nil {
		t.Fatal("expected a timed-out verify to fail the node")
	}
	if rec.Judged {
		t.Error("a timed-out verify rendered no verdict, but the record is judged")
	}
	v := rec.Verification
	if v == nil || v.Status != runstate.VerificationTimedOut {
		t.Fatalf("verification = %+v, want status timed out", v)
	}
	if v.ExitCode != nil {
		t.Errorf("a timed-out command recorded exit code %d", *v.ExitCode)
	}
	if v.OutputTail != "waiting for server…" {
		t.Errorf("output tail = %q, want the timeout's captured output", v.OutputTail)
	}
	if v.OutputTruncated {
		t.Error("a timeout's whole captured output was recorded as truncated (#332)")
	}
	encoded, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(encoded), `"exit_code"`) {
		t.Errorf("a timed-out verify wrote an exit_code key: %s", encoded)
	}
}

// TestVerificationRecord_SpawnFailureAndCancellation (#332): a verify that
// could not start, or that the run cancelled, broke before a verdict too —
// each says which, and neither records an exit code.
func TestVerificationRecord_SpawnFailureAndCancellation(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want runstate.VerificationStatus
	}{
		{"could not start", errors.New("exec: \"sh\": not found"), runstate.VerificationDidNotRun},
		{"no verifier", &verify.NotConfiguredError{Command: "build"}, runstate.VerificationDidNotRun},
		{"cancelled", context.Canceled, runstate.VerificationCancelled},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verifier := verify.NewFakeVerifier(nil)
			verifier.InjectError("build", tc.err)

			rec, _ := runVerified(t, `{ command: "build" }`, verifier, Options{})

			v := rec.Verification
			if v == nil || v.Status != tc.want || v.ExitCode != nil || v.Command != "build" {
				t.Fatalf("verification = %+v, want status %q, command build, no exit code", v, tc.want)
			}
		})
	}
}

// TestVerificationRecord_InterpolationErrorRecordsTheDeclaredCommand (#332):
// a command that did not interpolate ran nothing; the record names the
// declared text and the interpolation error, with no exit code.
func TestVerificationRecord_InterpolationErrorRecordsTheDeclaredCommand(t *testing.T) {
	node := graph.Node{ID: "dev", Prompt: "dev", SuccessCheck: graph.SuccessCheck{
		Verify: &graph.Verification{Command: "make {{ artifacts.ghost }}"},
	}}
	s, h, _ := newVerifyHarness(t, runner.NewFakeRunner(nil), verify.NewFakeVerifier(nil), Options{})

	v, err := s.verifyEvidence(context.Background(), node, h, "")

	if err == nil {
		t.Fatal("expected an unresolvable command to fail")
	}
	if v == nil || v.Status != runstate.VerificationInterpolationError || v.ExitCode != nil {
		t.Fatalf("verification = %+v, want status interpolation error, no exit code", v)
	}
	if v.Command != "make {{ artifacts.ghost }}" {
		t.Errorf("command = %q, want the declared text", v.Command)
	}
}

// TestVerificationRecord_UnjudgeableOutputRecordsTheObservedExit (#332): a
// command that ran but whose output_matches could not be compiled (a
// hand-built node, past graph validation) reached no verdict — yet it did
// exit, and the record keeps the exit code the engine observed.
func TestVerificationRecord_UnjudgeableOutputRecordsTheObservedExit(t *testing.T) {
	node := graph.Node{ID: "dev", Prompt: "dev", SuccessCheck: graph.SuccessCheck{
		Verify: &graph.Verification{Command: "check", OutputMatches: "("},
	}}
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"check": {ExitCode: 1, Output: "boom"}})
	s, h, _ := newVerifyHarness(t, runner.NewFakeRunner(nil), verifier, Options{})

	v, err := s.verifyEvidence(context.Background(), node, h, "")

	if err == nil || isJudgmentFailure(err) {
		t.Fatalf("err = %v, want an infrastructure fault", err)
	}
	if v == nil || v.Status != runstate.VerificationNotJudged {
		t.Fatalf("verification = %+v, want status not judged", v)
	}
	if v.ExitCode == nil || *v.ExitCode != 1 {
		t.Errorf("exit code = %v, want the observed 1", v.ExitCode)
	}
}

// TestVerificationRecord_NoVerifyWritesNothing (#332): a node without a
// verify gets no record at all, so its snapshot entry encodes without a
// verification key — the byte-identical-state.json half of the rule.
func TestVerificationRecord_NoVerifyWritesNothing(t *testing.T) {
	g := mustGraph(t, "name: p\nnodes:\n  - { id: plain, prompt: plain }\n  - { id: bad, prompt: bad, depends_on: [plain], success_check: { result_matches: \"^SHIPPED$\" } }\n")
	recorder := newFakeRecorder()
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"plain": pass("s1", 0), "bad": pass("s2", 0)})
	s, h, led := newVerifyHarness(t, fake, verify.NewRefusingVerifier(), Options{Recorder: recorder})

	_ = s.Run(context.Background(), g, h, led)

	for _, id := range []string{"plain", "bad"} {
		rec, ok := recorder.nodes[id]
		if !ok {
			t.Fatalf("%s was not recorded", id)
		}
		if rec.Verification != nil {
			t.Errorf("%s has no verify but recorded %+v", id, rec.Verification)
		}
		encoded, err := json.Marshal(rec)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if strings.Contains(string(encoded), "verification") {
			t.Errorf("%s encodes a verification key: %s", id, encoded)
		}
	}
}

// TestVerificationRecord_UnreachedVerifyRecordsNothing (#332): a node that
// declares a verify but failed an earlier predicate never ran its command,
// so it records none — a record would claim a command ran.
func TestVerificationRecord_UnreachedVerifyRecordsNothing(t *testing.T) {
	g := mustGraph(t, "name: p\nnodes:\n  - { id: dev, prompt: dev, success_check: { result_matches: \"^SHIPPED$\", verify: { command: build } } }\n")
	recorder := newFakeRecorder()
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"build": verified()})
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s1", 0)})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{Recorder: recorder})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the result_matches miss to fail the node")
	}
	if verifier.InvocationCount("build") != 0 {
		t.Fatal("the verify ran after an earlier predicate failed")
	}
	if v := recorder.nodes["dev"].Verification; v != nil {
		t.Errorf("an unreached verify recorded %+v", v)
	}
}

// TestVerificationRecord_LongOutputKeepsTheTailWithinTheBound (#332): the
// record keeps the END of a long log, marks the cut, drops the head, and
// stays within the byte bound — the whole log is never stored.
func TestVerificationRecord_LongOutputKeepsTheTailWithinTheBound(t *testing.T) {
	long := "HEAD-SENTINEL\n" + strings.Repeat("=== RUN TestX\n--- PASS: TestX\n", 2000) + "FAIL github.com/x/y 1.2s\n"
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"go test ./...": {ExitCode: 1, Output: long}})

	rec, _ := runVerified(t, `{ command: "go test ./..." }`, verifier, Options{})

	tail := rec.Verification.OutputTail
	if len(tail) > retainedOutputBound {
		t.Errorf("tail is %d bytes, want at most %d", len(tail), retainedOutputBound)
	}
	if !strings.HasSuffix(tail, "FAIL github.com/x/y 1.2s\n") {
		t.Errorf("tail lost the verdict line: %q", tail[max(0, len(tail)-60):])
	}
	if strings.Contains(tail, "HEAD-SENTINEL") {
		t.Error("the head of the log survived")
	}
	if !strings.HasPrefix(tail, "…(earlier output truncated)…") {
		t.Errorf("the cut is unmarked: %q", tail[:40])
	}
	// #332: the record SAYS it holds only a tail, from the engine's own
	// measurement, so the assessor can be told without reading the marker.
	if !rec.Verification.OutputTruncated {
		t.Error("a cut output was not recorded as truncated")
	}
}

// sequenceVerifier answers each call with the next scripted result, so a
// retried node can fail its verify once and pass it the second time.
type sequenceVerifier struct {
	mu      sync.Mutex
	results []verify.Result
	calls   int
}

func (v *sequenceVerifier) Verify(_ context.Context, _ verify.Request) (verify.Result, error) {
	v.mu.Lock()
	defer v.mu.Unlock()
	result := v.results[min(v.calls, len(v.results)-1)]
	v.calls++
	return result, nil
}

// TestVerificationRecord_RetryKeepsTheFinalAttempt (#332): a node retried
// on verify_failed records the attempt whose verdict it kept — the passing
// second one — not the first attempt's failing exit.
func TestVerificationRecord_RetryKeepsTheFinalAttempt(t *testing.T) {
	g := mustGraph(t, "name: p\nnodes:\n  - id: dev\n    prompt: dev\n    success_check: { verify: { command: build } }\n    retry: { max: 1, on: [verify_failed] }\n")
	recorder := newFakeRecorder()
	verifier := &sequenceVerifier{results: []verify.Result{
		{ExitCode: 1, Output: "first attempt broke\n"},
		{ExitCode: 0, Output: "second attempt ok\n"},
	}}
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s1", 0)})
	fake.KeyFn = nodePromptKey
	s, h, led := newVerifyHarness(t, fake, verifier, Options{Recorder: recorder})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if verifier.calls != 2 {
		t.Fatalf("verify ran %d times, want 2", verifier.calls)
	}
	v := recorder.nodes["dev"].Verification
	if v == nil || v.Status != runstate.VerificationPassed || v.ExitCode == nil || *v.ExitCode != 0 {
		t.Fatalf("verification = %+v, want the passing final attempt", v)
	}
	if !strings.Contains(v.OutputTail, "second attempt ok") || strings.Contains(v.OutputTail, "first attempt") {
		t.Errorf("tail = %q, want only the final attempt's output", v.OutputTail)
	}
}

// TestVerificationRecord_ResumedLegKeepsAnEarlierLegsRecord (#332): a
// resumed leg's recorder is seeded with the earlier leg's records; the
// scheduler leaves the completed node alone, so the rewritten state.json
// still carries that node's verification, unchanged.
func TestVerificationRecord_ResumedLegKeepsAnEarlierLegsRecord(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	exit := 0
	earlier := &runstate.VerificationRecord{
		Command: "build", ExitCode: &exit, Duration: time.Second,
		Status: runstate.VerificationPassed, OutputTail: "built\n",
	}
	recorder := runstate.NewSnapshotRecorder(path, runstate.Snapshot{
		RunID: "r1",
		Graph: json.RawMessage(`{}`),
		Nodes: map[string]runstate.NodeRecord{"build": {Verdict: runstate.VerdictPass, Verification: earlier}},
	})
	g := mustGraph(t, "name: p\nnodes:\n  - { id: build, prompt: build, success_check: { verify: { command: build } } }\n  - { id: ship, prompt: ship, depends_on: [build] }\n")
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"ship": pass("s2", 0)})
	verifier := verify.NewFakeVerifier(nil)
	s, h, led := newVerifyHarness(t, fake, verifier, Options{
		Recorder:       recorder,
		CompletedNodes: map[string]bool{"build": true},
	})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("resumed leg failed: %v", err)
	}
	if len(verifier.Calls()) != 0 {
		t.Fatalf("the resumed leg re-ran the completed node's verify: %+v", verifier.Calls())
	}
	snap, err := runstate.Load(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	got := snap.Nodes["build"].Verification
	if got == nil || got.Command != "build" || got.ExitCode == nil || *got.ExitCode != 0 || got.OutputTail != "built\n" || got.Duration != time.Second {
		t.Errorf("earlier leg's verification = %+v, want it carried unchanged", got)
	}
	if snap.Nodes["ship"].Verification != nil {
		t.Errorf("verify-free ship recorded %+v", snap.Nodes["ship"].Verification)
	}
}

// TestVerificationRecord_SignalKilledRecordsNoExitCode (#332): a command ended
// by a signal comes back from verify as exit code -1, which is not an exit
// code. The record leaves exit_code absent — never -1 — and keeps the failed
// verdict the node really got, so the assessor reads "exit code: none".
func TestVerificationRecord_SignalKilledRecordsNoExitCode(t *testing.T) {
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"sh -c 'kill -9 $$'": {ExitCode: -1, Output: "partial output\n"},
	})

	rec, err := runVerified(t, `{ command: "sh -c 'kill -9 $$'" }`, verifier, Options{})

	if err == nil {
		t.Fatal("expected the run to fail on the signal-killed verify")
	}
	v := rec.Verification
	if v == nil || v.Status != runstate.VerificationFailed {
		t.Fatalf("verification = %+v, want status failed", v)
	}
	if v.ExitCode != nil {
		t.Errorf("exit code = %d, want absent for a command that never exited", *v.ExitCode)
	}
	if !strings.Contains(v.OutputTail, "partial output") {
		t.Errorf("output tail = %q, want what the command printed before it died", v.OutputTail)
	}
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), `"exit_code"`) {
		t.Errorf("state.json record = %s, want no exit_code", raw)
	}
}
