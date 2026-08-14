package main

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// gateResumerFunc adapts a function to serve.GateResumer, so a test can wrap
// the real cliGateResumer to learn when its leg has ended. The handler starts
// the leg in a goroutine — the wrapper's channel is what the test waits on,
// never a sleep.
type gateResumerFunc func(ctx context.Context, runID, gateID string, decision gate.Decision) error

func (f gateResumerFunc) Resume(ctx context.Context, runID, gateID string, decision gate.Decision) error {
	return f(ctx, runID, gateID, decision)
}

// legOutcome is everything an approved gate changes about a run, read back
// from the same two places a user would: the persisted snapshot and whether
// the gated node actually ran.
type legOutcome struct {
	pausedAt  string
	decisions map[string]runstate.GateDecision
	verdicts  map[string]runstate.Verdict
	ranShip   bool
}

// readLegOutcome loads the run's snapshot through the real runstate.Load —
// what the next `resume`, `runs list` or live view would read.
func readLegOutcome(t *testing.T, runID string, rec *capturingRunner) legOutcome {
	t.Helper()
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("load snapshot of run %s: %v", runID, err)
	}
	verdicts := make(map[string]runstate.Verdict, len(snap.Nodes))
	for id, record := range snap.Nodes {
		verdicts[id] = record.Verdict
	}
	return legOutcome{
		pausedAt:  snap.Gate.PausedAt,
		decisions: snap.Gate.Decisions,
		verdicts:  verdicts,
		ranShip:   rec.invocationFor("ship").Prompt != "",
	}
}

// approveViaCLI pauses a run at its gate and approves it the way the terminal
// does: `oh-my-graph resume <run-id> --approve approve`.
func approveViaCLI(t *testing.T) legOutcome {
	t.Helper()
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	var resumeErr error
	// The leg prints its banner and ledger to os.Stdout; capture it so the
	// test output stays about the assertions.
	captureStdout(t, func() {
		runID, rec = pausedGateFlowRun(t)
		// nil Opener: no embedded live view, so this leg is byte-for-byte the
		// terminal's — which is exactly what the live-view leg is compared
		// against, and the live-view leg passes nil for its own reason (it is
		// already being watched by the serve process; see cliGateResumer).
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	return readLegOutcome(t, runID, rec)
}

// approveViaLiveView pauses the same run and approves it the way the browser
// does: POST /api/gate/approve, through the real serve handler with the token
// read out of the page that handler served.
func approveViaLiveView(t *testing.T) legOutcome {
	t.Helper()
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	captureStdout(t, func() { runID, rec = pausedGateFlowRun(t) })

	// The wrapper is only a rendezvous: the decision goes to the production
	// cliGateResumer, so this exercises the CLI's own resume, not a stub of it.
	legDone := make(chan error, 1)
	resumer := gateResumerFunc(func(ctx context.Context, id, gateID string, decision gate.Decision) error {
		err := cliGateResumer{runnerFor: func(string) (runner.NodeRunner, error) { return rec, nil }, errOut: io.Discard}.Resume(ctx, id, gateID, decision)
		legDone <- err
		return err
	})
	handler := serve.New(runDirFor(runID), runID).WithGateResumer(resumer).Handler()
	token := servedGateToken(t, handler)

	captureStdout(t, func() {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "http://127.0.0.1:8642/api/gate/approve", strings.NewReader(`{"node":"approve"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-OMG-Token", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Errorf("POST /api/gate/approve status = %d, want 202 (body %q)", w.Code, w.Body.String())
			return
		}
		select {
		case err := <-legDone:
			if err != nil {
				t.Errorf("the leg the live view started returned error: %v", err)
			}
		case <-time.After(30 * time.Second):
			t.Error("the leg the live view started never finished")
		}
	})
	return readLegOutcome(t, runID, rec)
}

// servedGateToken reads the gate token out of the page the handler serves —
// the same place the page's own buttons read it from, and the only place it
// exists.
func servedGateToken(t *testing.T, handler http.Handler) string {
	t.Helper()
	req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642/", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", w.Code)
	}
	match := regexp.MustCompile(`<meta name="omg-token" content="([^"]*)">`).FindStringSubmatch(w.Body.String())
	if match == nil || match[1] == "" {
		t.Fatal("the served page carries no gate token, so the buttons could never decide anything")
	}
	return match[1]
}

// TestLiveViewApprove_ProducesTheCLIsExactStateTransition is the whole point of
// the feature: the browser's approve button is a second front-end onto the one
// resume, not a second implementation of it. Both paths run the same paused
// graph against the same fake runner, and the state they leave behind — the
// cleared pause, the recorded decision, every node's verdict, and the gated
// node having actually run — must be identical.
func TestLiveViewApprove_ProducesTheCLIsExactStateTransition(t *testing.T) {
	viaCLI := approveViaCLI(t)

	// State the transition explicitly first, so the comparison below cannot be
	// satisfied by two identical no-ops.
	if viaCLI.pausedAt != "" {
		t.Fatalf("the CLI approval left the run paused at %q", viaCLI.pausedAt)
	}
	if viaCLI.decisions["approve"] != runstate.GateApprove {
		t.Fatalf("the CLI approval recorded %q for gate approve, want %q", viaCLI.decisions["approve"], runstate.GateApprove)
	}
	if viaCLI.verdicts["ship"] != runstate.VerdictPass || !viaCLI.ranShip {
		t.Fatalf("the CLI approval did not run the gated node: %+v", viaCLI)
	}

	viaLiveView := approveViaLiveView(t)
	if !reflect.DeepEqual(viaLiveView, viaCLI) {
		t.Errorf("approving from the live view left\n  %+v\nbut approving from the CLI left\n  %+v", viaLiveView, viaCLI)
	}
}

// TestLiveViewReject_RecordsTheRejectionLikeTheCLI covers the other button:
// a rejection prunes the gate's subtree and is recorded as a standing human
// decision, exactly as `resume --reject` does.
func TestLiveViewReject_RecordsTheRejectionLikeTheCLI(t *testing.T) {
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	captureStdout(t, func() { runID, rec = pausedGateFlowRun(t) })

	legDone := make(chan error, 1)
	resumer := gateResumerFunc(func(ctx context.Context, id, gateID string, decision gate.Decision) error {
		err := cliGateResumer{runnerFor: func(string) (runner.NodeRunner, error) { return rec, nil }, errOut: io.Discard}.Resume(ctx, id, gateID, decision)
		legDone <- err
		return err
	})
	handler := serve.New(runDirFor(runID), runID).WithGateResumer(resumer).Handler()
	token := servedGateToken(t, handler)

	captureStdout(t, func() {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "http://127.0.0.1:8642/api/gate/reject", strings.NewReader(`{"node":"approve"}`))
		req.Header.Set("X-OMG-Token", token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Errorf("POST /api/gate/reject status = %d, want 202 (body %q)", w.Code, w.Body.String())
			return
		}
		select {
		case <-legDone:
			// A rejected gate fails the run (*RunFailedError) — the honest
			// outcome of the decision, and cliGateResumer's job is only to
			// report it, so the error itself is not the assertion here.
		case <-time.After(30 * time.Second):
			t.Error("the leg the live view started never finished")
		}
	})

	got := readLegOutcome(t, runID, rec)
	if got.decisions["approve"] != runstate.GateReject {
		t.Errorf("recorded %q for gate approve, want %q", got.decisions["approve"], runstate.GateReject)
	}
	if got.pausedAt != "" {
		t.Errorf("the run is still paused at %q after a rejection", got.pausedAt)
	}
	if got.ranShip {
		t.Error("the gated node ran despite the gate being rejected")
	}
}
