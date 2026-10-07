package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
	"github.com/jitokim/oh-my-graph/internal/serve"
)

// tok spells a handoff placeholder for expr without a literal double-curly
// token in this file's source (#348).
func tok(expr string) string {
	return "{" + "{ " + expr + " }" + "}"
}

// twoDescribedGatesRun pauses a run whose two gates both carry a description:
// approve quotes the ticket input, final quotes ship's artifact. Approving the
// first gives a resumed leg that pauses again, at a gate with its own text
// (#348).
func twoDescribedGatesRun(t *testing.T, ticket string) (runID string, rec *capturingRunner) {
	t.Helper()
	g := mustParse(t, `{"name":"gate-flow","inputs":["ticket"],"nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"],"description":"ship `+tok("inputs.ticket")+`?"},
		{"id":"ship","prompt":"ship","depends_on":["approve"]},
		{"id":"final","type":"gate","depends_on":["ship"],"description":"release `+tok("artifacts.ship")+`?"}]}`)
	rec = &capturingRunner{}
	runID = "run-1"
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{"ticket": ticket}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "approve" {
		t.Fatalf("expected the run to pause at approve, got %T: %v", err, err)
	}
	return runID, rec
}

// TestPause_StoresDescriptionOnFreshRun_348: #348 — a fresh run's pause stores
// in state.json exactly the description its pause block printed.
func TestPause_StoresDescriptionOnFreshRun_348(t *testing.T) {
	isolateRunHome(t)
	runID, _, out := describedGateFlowRun(t, "T-42")
	shown := shownFlowDescription(runID, "T-42")

	raw, snap := loadRunSnapshot(t, runID)
	if snap.Gate.PausedAt != "approve" || snap.Gate.PausedGateDescription != shown {
		t.Fatalf("gate state = %+v, want paused at approve with %q", snap.Gate, shown)
	}
	if !strings.Contains(out, "Paused at gate \"approve\" ("+snap.Gate.PausedGateDescription+"). Resume with:\n") {
		t.Fatalf("the stored description is not what the pause printed\nstored: %q\nprinted:\n%s", snap.Gate.PausedGateDescription, out)
	}
	if !strings.Contains(raw, `"paused_gate_description"`) {
		t.Fatalf("state.json lacks the key:\n%s", raw)
	}
}

// TestPause_StoresDescriptionOnResumedLegThatPausesAgain_348: #348 — a resumed
// leg that pauses at a later gate stores that gate's description, and none of
// the gate it just decided.
func TestPause_StoresDescriptionOnResumedLegThatPausesAgain_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec := twoDescribedGatesRun(t, "T-42")
	if _, snap := loadRunSnapshot(t, runID); snap.Gate.PausedGateDescription != "ship T-42?" {
		t.Fatalf("first pause stored %q, want %q", snap.Gate.PausedGateDescription, "ship T-42?")
	}

	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "final" {
		t.Fatalf("expected the approved leg to pause at final, got %T: %v", err, err)
	}
	_, snap := loadRunSnapshot(t, runID)
	want := "release " + filepath.Join(runDirFor(runID), "ship.out") + "?"
	if snap.Gate.PausedAt != "final" || snap.Gate.PausedGateDescription != want {
		t.Fatalf("second pause gate state = %+v, want paused at final with %q", snap.Gate, want)
	}
	if !strings.Contains(out, "Paused at gate \"final\" ("+want+"). Resume with:\n") {
		t.Fatalf("the stored description is not what the resumed leg printed:\n%s", out)
	}
	if got := snap.Nodes["approve"].GateDescription; got != "ship T-42?" {
		t.Fatalf("decided gate recorded %q, want %q", got, "ship T-42?")
	}
}

// TestPause_FieldClearedWhenLegDoesNotPause_348: #348 — a leg that runs to
// completion leaves no paused_gate_description key behind.
func TestPause_FieldClearedWhenLegDoesNotPause_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec := twoDescribedGatesRun(t, "T-42")
	for _, gateID := range []string{"approve", "final"} {
		var err error
		captureStdout(t, func() {
			err = executeResume(parseResumeFlags(t, []string{runID, "--approve", gateID}), rec, nil)
		})
		if gateID == "final" && err != nil {
			t.Fatalf("final leg: %v", err)
		}
	}
	raw, snap := loadRunSnapshot(t, runID)
	if snap.Gate.PausedAt != "" || strings.Contains(raw, "paused_gate_description") {
		t.Fatalf("a completed run still carries a paused gate description:\n%s", raw)
	}
}

// liveView is the real serve handler over a paused run, wired to the
// production cliGateResumer, and a channel its leg reports on (#348).
type liveView struct {
	handler http.Handler
	legDone chan error
}

func newLiveView(runID string, rec *capturingRunner) liveView {
	legDone := make(chan error, 1)
	resumer := gateResumerFunc(func(ctx context.Context, id, gateID string, decision gate.Decision) error {
		err := cliGateResumer{runnerFor: func(string) (runner.NodeRunner, error) { return rec, nil }, errOut: io.Discard}.Resume(ctx, id, gateID, decision)
		legDone <- err
		return err
	})
	return liveView{handler: serve.New(runDirFor(runID), runID).WithGateResumer(resumer).Handler(), legDone: legDone}
}

// pausedGateJSON is the paused_gate object the page reads from GET
// /api/graph, plus the raw body it came in.
type pausedGateJSON struct {
	ID          string `json:"id"`
	Description string `json:"description"`
}

func (v liveView) graph(t *testing.T) (gate *pausedGateJSON, body string) {
	t.Helper()
	w := httptest.NewRecorder()
	v.handler.ServeHTTP(w, httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642/api/graph", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("GET /api/graph status = %d (body %q)", w.Code, w.Body.String())
	}
	var payload struct {
		PausedGate *pausedGateJSON `json:"paused_gate"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode /api/graph: %v", err)
	}
	return payload.PausedGate, w.Body.String()
}

// decide POSTs the page's approve or reject for gateID and waits for the leg
// it starts to end. A rejection fails the run, so the leg's error is not
// asserted here.
func (v liveView) decide(t *testing.T, verb, gateID string) {
	t.Helper()
	token := servedGateToken(t, v.handler)
	captureStdout(t, func() {
		req := httptest.NewRequestWithContext(context.Background(), "POST", "http://127.0.0.1:8642/api/gate/"+verb, strings.NewReader(`{"node":"`+gateID+`"}`))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("X-OMG-Token", token)
		w := httptest.NewRecorder()
		v.handler.ServeHTTP(w, req)
		if w.Code != http.StatusAccepted {
			t.Errorf("POST /api/gate/%s status = %d, want 202 (body %q)", verb, w.Code, w.Body.String())
			return
		}
		select {
		case <-v.legDone:
		case <-time.After(30 * time.Second):
			t.Error("the leg the live view started never finished")
		}
	})
}

// TestLiveView_PausedGateJSONCarriesSanitizedDescription_348: #348 — the page's
// /api/graph names the paused gate with the description the pause printed,
// an ESC sequence and a bidi override (U+202E) already removed.
func TestLiveView_PausedGateJSONCarriesSanitizedDescription_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec, pauseOut := describedGateFlowRun(t, "T-42\x1b[2J\u202e")
	shown := shownFlowDescription(runID, "T-42")

	paused, _ := newLiveView(runID, rec).graph(t)
	if paused == nil {
		t.Fatal("/api/graph carries no paused_gate for a run paused at a described gate")
	}
	if paused.ID != "approve" || paused.Description != shown {
		t.Fatalf("paused_gate = %+v, want approve with %q", *paused, shown)
	}
	if strings.ContainsAny(paused.Description, "\x1b\u202e") {
		t.Fatalf("served description carries a raw control: %q", paused.Description)
	}
	if !strings.Contains(pauseOut, "\"approve\" ("+paused.Description+")") {
		t.Fatalf("served description is not what the pause printed:\n%s", pauseOut)
	}
}

// TestLiveView_HTMLInDescriptionIsLiteralJSONText_348: #348 — a script tag
// and an img tag with an onerror attribute in a description reach the page
// as JSON-escaped text that decodes to the literal characters, never markup.
func TestLiveView_HTMLInDescriptionIsLiteralJSONText_348(t *testing.T) {
	isolateRunHome(t)
	ticket := `<script>alert(1)</script><img src=x onerror="alert(2)">`
	runID, rec, _ := describedGateFlowRun(t, ticket)

	paused, body := newLiveView(runID, rec).graph(t)
	if strings.Contains(body, "<script") || strings.Contains(body, "<img") {
		t.Fatalf("raw /api/graph body carries unescaped markup: %s", body)
	}
	if !strings.Contains(body, `\u003cscript\u003e`) || !strings.Contains(body, `\u003cimg src=x onerror=\"alert(2)\"\u003e`) {
		t.Fatalf("raw /api/graph body does not JSON-escape the markup: %s", body)
	}
	_, snap := loadRunSnapshot(t, runID)
	if paused == nil || paused.Description != snap.Gate.PausedGateDescription || paused.Description != shownFlowDescription(runID, ticket) {
		t.Fatalf("decoded description = %+v, want the literal %q", paused, shownFlowDescription(runID, ticket))
	}
}

// TestLiveView_UndescribedGateHasNoPausedGate_348: #348 — a gate without a
// description gives the page no paused_gate at all.
func TestLiveView_UndescribedGateHasNoPausedGate_348(t *testing.T) {
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	captureStdout(t, func() { runID, rec = pausedGateFlowRun(t) })

	paused, body := newLiveView(runID, rec).graph(t)
	if paused != nil || strings.Contains(body, "paused_gate") {
		t.Fatalf("an undescribed gate served paused_gate: %s", body)
	}
	if raw, _ := loadRunSnapshot(t, runID); strings.Contains(raw, "paused_gate_description") {
		t.Fatalf("an undescribed pause stored a description:\n%s", raw)
	}
}

// TestLiveView_DecisionRecordsTheServedDescriptionByteForByte_348: #348 — a
// decision made from the page records, as the gate's gate_description, exactly
// the bytes /api/graph served the page, for approve and for reject.
func TestLiveView_DecisionRecordsTheServedDescriptionByteForByte_348(t *testing.T) {
	for _, verb := range []string{"approve", "reject"} {
		t.Run(verb, func(t *testing.T) {
			isolateRunHome(t)
			runID, rec, _ := describedGateFlowRun(t, "T-42 <b>&\u202e\x1b[1m")
			view := newLiveView(runID, rec)
			paused, _ := view.graph(t)
			if paused == nil || paused.Description == "" {
				t.Fatal("/api/graph served no description to decide by")
			}

			view.decide(t, verb, "approve")
			_, snap := loadRunSnapshot(t, runID)
			if got := snap.Nodes["approve"].GateDescription; got != paused.Description {
				t.Fatalf("recorded gate_description %q, page was served %q", got, paused.Description)
			}
		})
	}
}

// overwriteStoredDescription rewrites runID's state.json with the paused
// gate's stored description replaced by description (#348).
func overwriteStoredDescription(t *testing.T, runID, description string) {
	t.Helper()
	_, snap := loadRunSnapshot(t, runID)
	snap.Gate.PausedGateDescription = description
	if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), snap); err != nil {
		t.Fatalf("rewrite state.json: %v", err)
	}
}

// TestResume_DecisionRecordsStoredNotReRenderedDescription_348: #348 — the
// decision records the stored copy, not a fresh render: a sentinel written in
// its place is what the page serves, what the echo prints, and what is
// recorded.
func TestResume_DecisionRecordsStoredNotReRenderedDescription_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42")
	const sentinel = "the stored copy, not a re-render"
	overwriteStoredDescription(t, runID, sentinel)

	view := newLiveView(runID, rec)
	if paused, _ := view.graph(t); paused == nil || paused.Description != sentinel {
		t.Fatalf("/api/graph served %+v, want the stored %q", paused, sentinel)
	}
	view.decide(t, "approve", "approve")
	if _, snap := loadRunSnapshot(t, runID); snap.Nodes["approve"].GateDescription != sentinel {
		t.Fatalf("recorded %q, want the stored %q", snap.Nodes["approve"].GateDescription, sentinel)
	}
}

// TestResume_PreFieldSnapshotFallsBackToRender_348: #348 — a snapshot written
// before the field existed still echoes and records the description, rendered
// from the snapshot exactly as before.
func TestResume_PreFieldSnapshotFallsBackToRender_348(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42")
	overwriteStoredDescription(t, runID, "")
	shown := shownFlowDescription(runID, "T-42")

	if paused, _ := newLiveView(runID, rec).graph(t); paused != nil {
		t.Fatalf("a pre-#348 snapshot served paused_gate %+v", *paused)
	}
	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "final" {
		t.Fatalf("expected the approved leg to pause at final, got %T: %v", err, err)
	}
	if want := "approved gate approve: " + shown + "\n"; !strings.Contains(out, want) {
		t.Fatalf("approve echo missing\nwant:\n%s\ngot:\n%s", want, out)
	}
	if _, snap := loadRunSnapshot(t, runID); snap.Nodes["approve"].GateDescription != shown {
		t.Fatalf("recorded %q, want the rendered %q", snap.Nodes["approve"].GateDescription, shown)
	}
}
