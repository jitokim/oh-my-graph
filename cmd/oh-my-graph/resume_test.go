package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// pausedGateFlowRun executes the standard a -> approve(gate) -> ship graph
// against a fresh capturingRunner and returns once it has paused at the gate,
// so a resume test can start from a known, on-disk snapshot. Callers must
// t.Chdir into a temp directory first — executeGraph resolves its run
// directory relative to the process cwd.
func pausedGateFlowRun(t *testing.T) (runID string, rec *capturingRunner) {
	t.Helper()
	g := mustParse(t, `{"name":"gate-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
	rec = &capturingRunner{}
	runID = "run-1"
	// rawSource is deliberately not valid JSON, forcing executeGraph onto the
	// json.Marshal(g) path a real `run` (hand-written YAML) takes — the
	// round-trip this whole feature depends on (internal/graph's json tags).
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"))
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the initial run to pause, got %T: %v", err, err)
	}
	if paused.GateID != "approve" {
		t.Fatalf("paused at %q, want approve", paused.GateID)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("ship must not run before the gate is decided")
	}
	return runID, rec
}

func parseResumeFlags(t *testing.T, args []string) *resumeFlags {
	t.Helper()
	f := newResumeFlags()
	if err := f.parse(args); err != nil {
		t.Fatalf("parse resume flags %v: %v", args, err)
	}
	return f
}

// --- approve continues the run to completion ---------------------------------

func TestResume_ApprovedGateContinuesToCompletion(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec)
	if err != nil {
		t.Fatalf("executeResume returned error: %v", err)
	}
	if rec.invocationFor("ship").Prompt == "" {
		t.Fatal("ship should have run once the gate was approved")
	}
}

// --- reject prunes the subtree and is reported as a failure, not a crash ----

func TestResume_RejectedGatePrunesSubtree(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--reject", "approve"}), rec)
	var runFailed *schedule.RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("ship must never run after the gate was rejected")
	}
}

// --- CLI contract: exactly one of --approve/--reject, gate id must match ----

func TestResume_BareInvocationNamesThePendingGate(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID}), rec)
	if err == nil {
		t.Fatal("a bare resume on a paused run must be an error")
	}
	if !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error should name the pending gate: %v", err)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("a bare resume must never silently approve the gate")
	}
}

func TestResume_MismatchedGateNameIsRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "some-other-gate"}), rec)
	if err == nil {
		t.Fatal("resuming a gate the run is not paused at must be an error")
	}
	if !strings.Contains(err.Error(), "some-other-gate") || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error should name both the requested and the actual gate: %v", err)
	}
}

func TestResume_BothApproveAndRejectIsRejected(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve", "--reject", "approve"}), rec)
	if err == nil {
		t.Fatal("--approve and --reject together must be rejected")
	}
}

// --- resuming a run that never paused ----------------------------------------

func TestResume_RunNotPausedErrors(t *testing.T) {
	t.Chdir(t.TempDir())
	g := mustParse(t, `{"name":"no-gate","nodes":[{"id":"a","prompt":"a"}]}`)
	rec := &capturingRunner{}
	runID := "run-complete"
	if err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "no-gate.yaml", []byte("name: no-gate\n")); err != nil {
		t.Fatalf("initial run should complete cleanly: %v", err)
	}

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "whatever"}), rec)
	if err == nil || !strings.Contains(err.Error(), "not paused") {
		t.Fatalf("expected a 'not paused' error, got %v", err)
	}
}

// --- resume.lock guards against a concurrent resume --------------------------

func TestResume_LockPreventsConcurrentResume(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	lockPath := filepath.Join(runDirFor(runID), lockFileName)
	release, err := runstate.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock ahead of the test's resume: %v", err)
	}
	defer release()

	err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec)
	var held *runstate.LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("expected *runstate.LockHeldError while the lock is held, got %T: %v", err, err)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("a lock-blocked resume must never have run any node")
	}
}

func TestResume_LockIsReleasedAfterAResume(t *testing.T) {
	t.Chdir(t.TempDir())
	runID, rec := pausedGateFlowRun(t)

	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec); err != nil {
		t.Fatalf("executeResume returned error: %v", err)
	}

	lockPath := filepath.Join(runDirFor(runID), lockFileName)
	if _, err := os.Stat(lockPath); !os.IsNotExist(err) {
		t.Fatalf("resume.lock left behind after a completed resume: stat err = %v", err)
	}
}

// --- multiple gates require multiple resumes ---------------------------------

// TestResume_MultipleGatesRequireMultipleResumes proves the shape DESIGN.md
// describes: "Multiple gates => multiple resumes: a resumed run advances to
// the next gate and pauses again."
func TestResume_MultipleGatesRequireMultipleResumes(t *testing.T) {
	t.Chdir(t.TempDir())
	g := mustParse(t, `{"name":"multi-gate","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"gate1","type":"gate","depends_on":["a"]},
		{"id":"b","prompt":"b","depends_on":["gate1"]},
		{"id":"gate2","type":"gate","depends_on":["b"]},
		{"id":"c","prompt":"c","depends_on":["gate2"]}]}`)
	rec := &capturingRunner{}
	runID := "run-multi"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "multi.yaml", []byte("name: multi-gate\n"))
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "gate1" {
		t.Fatalf("expected the initial run to pause at gate1, got %T: %v", err, err)
	}

	err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate1"}), rec)
	if !errors.As(err, &paused) || paused.GateID != "gate2" {
		t.Fatalf("expected the first resume to pause at gate2, got %T: %v", err, err)
	}
	if rec.invocationFor("c").Prompt != "" {
		t.Fatal("c must not run before gate2 is decided")
	}

	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate2"}), rec); err != nil {
		t.Fatalf("the final resume returned error: %v", err)
	}
	if rec.invocationFor("c").Prompt == "" {
		t.Fatal("c should have run once gate2 was approved")
	}
}

// --- GraphSHA256 drift warning -----------------------------------------------

// TestResume_WarnsWhenGraphSourceFileChanged proves the advisory-only check
// DESIGN.md assigns to GraphSHA256: it neither blocks the resume nor changes
// what runs (the snapshot's own Graph bytes are still what executes), it just
// tells the user their on-disk file no longer matches what was snapshotted.
func TestResume_WarnsWhenGraphSourceFileChanged(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	runID, rec := pausedGateFlowRun(t)

	// pausedGateFlowRun snapshots GraphSourcePath "gate-flow.yaml", which does
	// not exist on disk in this temp dir. Create it now with content that
	// deliberately does not hash to the snapshot's GraphSHA256 (computed over
	// the literal bytes []byte("name: gate-flow\n") passed at snapshot time).
	if err := os.WriteFile(filepath.Join(dir, "gate-flow.yaml"), []byte("name: gate-flow-edited\n"), 0o644); err != nil {
		t.Fatalf("write edited graph file: %v", err)
	}

	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		buf2 := make([]byte, 4096)
		for {
			n, readErr := r.Read(buf2)
			if n > 0 {
				buf.Write(buf2[:n])
			}
			if readErr != nil {
				break
			}
		}
		done <- buf.String()
	}()

	resumeErr := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec)

	os.Stderr = origStderr
	_ = w.Close()
	stderr := <-done
	_ = r.Close()

	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(stderr, "gate-flow.yaml") || !strings.Contains(stderr, "changed on disk") {
		t.Errorf("expected a stderr warning about the changed graph source, got %q", stderr)
	}
}

// --- exit code mapping (ADR 0003: 0 pass, 1 fail, 2 paused) ------------------

func TestMainExitCode_PauseMapsToExitCode2(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	graphPath := filepath.Join(dir, "gate-only.yaml")
	// A graph whose only node is a root gate never touches the NodeRunner, so
	// this drives the real `run` subcommand (and its real ClaudeCLIRunner)
	// end to end without spawning a claude subprocess.
	if err := os.WriteFile(graphPath, []byte("name: gate-only\nnodes:\n  - { id: approve, type: gate }\n"), 0o644); err != nil {
		t.Fatalf("write graph file: %v", err)
	}

	if code := mainExitCode([]string{"run", graphPath}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a paused run", code)
	}
}

func TestMainExitCode_MissingGraphFileMapsToExitCode1(t *testing.T) {
	if code := mainExitCode([]string{"run", "/no/such/file.yaml"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestMainExitCode_UnknownCommandMapsToExitCode1(t *testing.T) {
	if code := mainExitCode([]string{"bogus"}); code != 1 {
		t.Fatalf("exit code = %d, want 1", code)
	}
}

func TestMainExitCode_VersionMapsToExitCode0(t *testing.T) {
	if code := mainExitCode([]string{"version"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// --- continue-on-fail: a settled-failed node never re-runs or double-records ---

// scriptedRunner is capturingRunner's sibling for a test that needs one
// specific node to fail: it records how many times each prompt was invoked —
// so a test can prove a node was launched exactly once across BOTH legs, not
// merely that it was launched at all — and returns a scripted FAIL outcome
// (nonzero exit, carrying a cost) for any prompt named in failing, PASS for
// everything else.
type scriptedRunner struct {
	mu          sync.Mutex
	invocations map[string]int
	failing     map[string]float64 // prompt -> cost the failing attempt reports
}

func (r *scriptedRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invocations == nil {
		r.invocations = make(map[string]int)
	}
	r.invocations[spec.Prompt]++
	if cost, fails := r.failing[spec.Prompt]; fails {
		return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "FAIL", ExitCode: 1, TotalCostUSD: cost}, nil
	}
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

func (r *scriptedRunner) invocationCount(prompt string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invocations[prompt]
}

// TestResume_ContinueOnFail_SettledFailedNodeDoesNotRerunOrDoubleRecord is the
// failure-first regression test for the bug deep review found: under
// --continue-on-fail, a node that FAILED on leg 1 was absent from
// Snapshot.CompletedNodes (pass-only by design), so `resume` handed it right
// back to graph.ReadyGiven as ready and it RE-EXECUTED — and because
// executeResume also pre-seeds the ledger from every snap.Nodes entry
// (including the FAIL), the re-run added a SECOND ledger row for the same
// node, double-counting its cost in TOTAL COST.
//
// The graph pairs an independent gate (so this run is legitimately resumable
// at all) with a sibling branch where "flaky" fails and "orphan" — flaky's
// only dependent — must never run, on either leg.
func TestResume_ContinueOnFail_SettledFailedNodeDoesNotRerunOrDoubleRecord(t *testing.T) {
	t.Chdir(t.TempDir())
	g := mustParse(t, `{"name":"gate-continue","nodes":[
		{"id":"gate","type":"gate"},
		{"id":"flaky","prompt":"flaky"},
		{"id":"orphan","prompt":"orphan","depends_on":["flaky"]},
		{"id":"ship","prompt":"ship","depends_on":["gate"]}]}`)
	rec := &scriptedRunner{failing: map[string]float64{"flaky": 0.05}}
	runID := "run-continue"

	err := executeGraph(context.Background(), runID, g, rec,
		commonRunFlags{inputs: inputFlag{}, continueOnFail: true},
		nil, 0, "gate-continue.yaml", []byte("name: gate-continue\n"))
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "gate" {
		t.Fatalf("expected the initial run to pause at gate, got %T: %v", err, err)
	}
	if got := rec.invocationCount("flaky"); got != 1 {
		t.Fatalf("flaky invocation count after leg 1 = %d, want 1", got)
	}
	if got := rec.invocationCount("orphan"); got != 0 {
		t.Fatalf("orphan (flaky's dependent) invocation count after leg 1 = %d, want 0 — its subtree is pruned", got)
	}

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate"}), rec)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}

	if got := rec.invocationCount("flaky"); got != 1 {
		t.Fatalf("flaky invocation count after resume = %d, want still 1 — a settled-failed node must never re-run", got)
	}
	if got := rec.invocationCount("orphan"); got != 0 {
		t.Fatalf("orphan invocation count after resume = %d, want still 0 — its pruned subtree must stay pruned across the resume", got)
	}
	if got := rec.invocationCount("ship"); got != 1 {
		t.Fatalf("ship invocation count after resume = %d, want 1 — the approved gate's dependent should run", got)
	}

	if got, want := strings.Count(out, "\nflaky "), 1; got != want {
		t.Fatalf("resumed leg's ledger table shows %d row(s) for flaky, want exactly %d (no double-record):\n%s", got, want, out)
	}
	if !strings.Contains(out, "0.0500") {
		t.Fatalf("flaky's original $0.0500 cost must survive into the resumed leg's ledger, undoubled:\n%s", out)
	}
}
