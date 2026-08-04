package serve

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// gateGraph is the shape every gate fixture here uses: one node, the gate that
// guards what follows, and the node it guards.
const gateGraph = `{"name":"gate-flow","nodes":[
	{"id":"a","prompt":"a"},
	{"id":"approve","type":"gate","depends_on":["a"]},
	{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`

// resumeCall is one recorded (run, gate, decision) the handler asked the
// resumer to apply — the whole of what serve is responsible for getting right,
// since the leg itself belongs to the CLI's resume.
type resumeCall struct {
	runID    string
	gateID   string
	decision gate.Decision
}

// fakeResumer stands in for the CLI's cliGateResumer. It records the call on a
// buffered channel: the handler starts the leg in a goroutine, so a test waits
// on the channel — never on a wall-clock sleep — for the call to arrive.
type fakeResumer struct {
	calls chan resumeCall
}

func newFakeResumer() *fakeResumer {
	return &fakeResumer{calls: make(chan resumeCall, 1)}
}

func (r *fakeResumer) Resume(_ context.Context, runID, gateID string, decision gate.Decision) error {
	r.calls <- resumeCall{runID: runID, gateID: gateID, decision: decision}
	return nil
}

// awaitCall returns the resume the handler started, or fails the test if none
// arrives — an assertion about what WAS asked for, not about nothing going
// wrong.
func (r *fakeResumer) awaitCall(t *testing.T) resumeCall {
	t.Helper()
	select {
	case call := <-r.calls:
		return call
	case <-time.After(5 * time.Second):
		t.Fatal("no resume was started; the handler answered without applying the decision")
		return resumeCall{}
	}
}

// requireNoCall asserts no leg was started. Paired with a status assertion, so
// the test cannot pass merely because nothing happened.
func (r *fakeResumer) requireNoCall(t *testing.T) {
	t.Helper()
	select {
	case call := <-r.calls:
		t.Fatalf("a refused request still started a resume: %+v", call)
	default:
	}
}

// pausedGateServer builds a server over a run whose snapshot says it is parked
// at gate "approve" — written through the real runstate.Write, so the fixture
// is exactly what a paused run leaves on disk.
func pausedGateServer(t *testing.T, resumer GateResumer) (*Server, string) {
	t.Helper()
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-gate",
		Graph: json.RawMessage(gateGraph),
		Nodes: map[string]runstate.NodeRecord{
			"a": {Verdict: runstate.VerdictPass, ArtifactPath: filepath.Join(dir, "a.out")},
		},
		Gate: runstate.GateState{PausedAt: "approve"},
	})
	s := newTestServer(dir, "run-gate")
	s.WithGateResumer(resumer)
	return s, dir
}

// postGate sends one gate decision the way the page does: a POST carrying the
// token as X-OMG-Token and the gate id in the body. An empty token sends no
// header at all.
func postGate(t *testing.T, s *Server, path, token, node string) *httptest.ResponseRecorder {
	t.Helper()
	return postGateFrom(t, s, path, token, node, "")
}

// postGateFrom is postGate with an explicit Origin — the header a browser
// stamps on every cross-origin POST it makes. An empty origin sends no header
// at all, as a non-browser client (curl, the CLI's own tests) does.
func postGateFrom(t *testing.T, s *Server, path, token, node, origin string) *httptest.ResponseRecorder {
	t.Helper()
	body := strings.NewReader(`{"node":"` + node + `"}`)
	req := httptest.NewRequestWithContext(context.Background(), "POST", "http://127.0.0.1:8642"+path, body)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("X-OMG-Token", token)
	}
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	return rec
}

// --- the token: the CSRF guard on the only mutating routes -------------------

func TestGateDecision_WithoutTheTokenNothingIsResumed(t *testing.T) {
	// A cross-origin page can reach a loopback POST (its Host header is
	// legitimate and the bind is not access control for a request the user's
	// own browser makes) — but it cannot read the token out of the page this
	// process served, so it cannot decide the gate.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	rec := postGate(t, s, "/api/gate/approve", "", "approve")

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 for a gate decision with no token", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "X-OMG-Token") {
		t.Errorf("the refusal must name the missing header, got %q", rec.Body.String())
	}
	resumer.requireNoCall(t)
}

func TestGateDecision_WrongTokenIsForbidden(t *testing.T) {
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	rec := postGate(t, s, "/api/gate/approve", "0123456789abcdef", "approve")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a gate decision carrying the wrong token", rec.Code)
	}
	resumer.requireNoCall(t)
}

func TestGateDecision_TheTokenlessSimpleRequestIsRefused(t *testing.T) {
	// The exact request a hostile page can issue without a preflight: a CORS
	// "simple request" — Content-Type text/plain, no custom header — sent from
	// an origin this process did not serve, to a URL whose Host is the real
	// loopback target (so the bind and requireLoopbackHost both pass it).
	//
	// Neither guard has ever let this through: with no X-OMG-Token the token
	// check refuses it on its own (400, and no leg — see
	// TestGateDecision_AnAbsentTokenIsRefusedOnEveryMutatingRoute), and
	// requireSameOrigin now refuses it a step earlier on its Origin. The
	// status here therefore records which guard SPEAKS FIRST, not whether the
	// request would otherwise have been admitted; the invariant this test
	// exists for is requireNoCall. Pinned on both mutating routes so no future
	// edit to either guard can quietly admit the shape.
	for _, path := range []string{"/api/gate/approve", "/api/gate/reject"} {
		t.Run(path, func(t *testing.T) {
			resumer := newFakeResumer()
			s, _ := pausedGateServer(t, resumer)

			body := strings.NewReader(`{"node":"approve"}`)
			req := httptest.NewRequestWithContext(context.Background(), "POST", "http://127.0.0.1:8642"+path, body)
			req.Header.Set("Content-Type", "text/plain;charset=UTF-8")
			req.Header.Set("Origin", "http://evil.example")
			rec := httptest.NewRecorder()
			s.Handler().ServeHTTP(rec, req)

			if rec.Code != http.StatusForbidden {
				t.Errorf("status = %d, want 403 for a tokenless cross-origin simple request (body %q)", rec.Code, rec.Body.String())
			}
			resumer.requireNoCall(t)
		})
	}
}

func TestGateDecision_AnAbsentTokenIsRefusedOnEveryMutatingRoute(t *testing.T) {
	// The behaviour a security audit has already misread once: an absent
	// X-OMG-Token is a REFUSAL, not a shape complaint that falls through. It
	// is answered 400 — the request is malformed for this API, which is what a
	// non-browser client sending no header has actually done — and, decisively,
	// nothing is resumed. Pinned on both mutating routes so the guard cannot
	// come to hold on only one of them.
	for _, path := range []string{"/api/gate/approve", "/api/gate/reject"} {
		t.Run(path, func(t *testing.T) {
			resumer := newFakeResumer()
			s, _ := pausedGateServer(t, resumer)

			rec := postGate(t, s, path, "", "approve")

			if rec.Code != http.StatusBadRequest {
				t.Errorf("status = %d, want 400 for a gate decision with no token", rec.Code)
			}
			if !strings.Contains(rec.Body.String(), "X-OMG-Token") {
				t.Errorf("the refusal must name the missing header, got %q", rec.Body.String())
			}
			resumer.requireNoCall(t)
		})
	}
}

// --- Origin: hardening layered on the token, which already held --------------

func TestGateDecision_AForeignOriginIsForbidden(t *testing.T) {
	// A browser stamps Origin on every cross-origin POST it makes, so a
	// decision arriving from a page this process did not serve is refused on
	// its origin alone — even in the hypothetical where the token leaked out
	// of the served page, which is why this request carries the REAL token.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	rec := postGateFrom(t, s, "/api/gate/approve", s.token, "approve", "http://evil.example")

	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want 403 for a gate decision from a foreign origin", rec.Code)
	}
	resumer.requireNoCall(t)

	// The page's own origin — what the live view itself sends — still decides.
	ok := postGateFrom(t, s, "/api/gate/approve", s.token, "approve", "http://127.0.0.1:8642")
	if ok.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for a decision from this view's own origin (body %q)", ok.Code, ok.Body.String())
	}
	if got := resumer.awaitCall(t); got.gateID != "approve" {
		t.Errorf("resumed gate %q, want approve", got.gateID)
	}
}

func TestGateDecision_AnAbsentOriginStillDecides(t *testing.T) {
	// The origin check narrows what a BROWSER can do and nothing else: a
	// client that sends no Origin at all — curl, the CLI's own tests — is
	// unaffected, and the token remains the whole guard for it, exactly as it
	// was before this check existed.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	rec := postGateFrom(t, s, "/api/gate/approve", s.token, "approve", "")

	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for a tokened decision carrying no Origin (body %q)", rec.Code, rec.Body.String())
	}
	if got := resumer.awaitCall(t); got.gateID != "approve" {
		t.Errorf("resumed gate %q, want approve", got.gateID)
	}
}

func TestIndex_CarriesThisProcessesTokenAndItIsTheOneThatWorks(t *testing.T) {
	// The token only exists in the page this process rendered: read it back
	// out of the served HTML, and it is exactly the one the gate route
	// accepts. A second server mints a different one.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642/", nil)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	token := tokenFromPage(t, rec.Body.String())
	if token != s.token {
		t.Fatalf("page carries token %q, want this server's %q", token, s.token)
	}

	other, _ := pausedGateServer(t, newFakeResumer())
	if other.token == token {
		t.Error("two servers minted the same gate token; it must be per process")
	}

	if code := postGate(t, s, "/api/gate/approve", token, "approve").Code; code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202 for a decision carrying the page's own token", code)
	}
	if got := resumer.awaitCall(t); got.gateID != "approve" {
		t.Errorf("resumed gate %q, want approve", got.gateID)
	}
}

// tokenFromPage extracts the served page's omg-token meta content.
func tokenFromPage(t *testing.T, page string) string {
	t.Helper()
	match := regexp.MustCompile(`<meta name="omg-token" content="([^"]*)">`).FindStringSubmatch(page)
	if match == nil {
		t.Fatalf("the served page carries no omg-token meta tag:\n%s", page)
	}
	return match[1]
}

// --- valid only while the run is paused at that gate -------------------------

func TestGateDecision_NotPausedIsConflict(t *testing.T) {
	// The snapshot of a run that is not parked at a gate: Gate.PausedAt is the
	// single source of truth, so there is nothing to decide.
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-live", Graph: json.RawMessage(gateGraph)})
	resumer := newFakeResumer()
	s := newTestServer(dir, "run-live").WithGateResumer(resumer)

	rec := postGate(t, s, "/api/gate/approve", s.token, "approve")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a run that is not paused", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "not paused") {
		t.Errorf("the refusal must say the run is not paused, got %q", rec.Body.String())
	}
	resumer.requireNoCall(t)
}

func TestGateDecision_AGateTheRunIsNotPausedAtIsConflict(t *testing.T) {
	// ADR 0003's explicit-id rule: the client names the gate, and a page that
	// is out of date must not approve whichever gate the run reached since.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)

	rec := postGate(t, s, "/api/gate/approve", s.token, "some-other-gate")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a gate the run is not paused at", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "some-other-gate") || !strings.Contains(body, "approve") {
		t.Errorf("the refusal must name both the requested and the actual gate, got %q", body)
	}
	resumer.requireNoCall(t)
}

func TestGateDecision_NoSnapshotIsConflict(t *testing.T) {
	resumer := newFakeResumer()
	s := newTestServer(t.TempDir(), "run-fresh").WithGateResumer(resumer)

	rec := postGate(t, s, "/api/gate/approve", s.token, "approve")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 for a run with no snapshot yet", rec.Code)
	}
	resumer.requireNoCall(t)
}

func TestGateDecision_ALegInFlightIsConflict(t *testing.T) {
	// The resume.lock is held for a run's whole duration, so a held lock means
	// a leg is running: the run is not paused, whatever a stale snapshot says.
	resumer := newFakeResumer()
	s, dir := pausedGateServer(t, resumer)
	if err := os.WriteFile(filepath.Join(dir, lockFileName), []byte("1234\n"), 0o644); err != nil {
		t.Fatalf("write fixture lock: %v", err)
	}

	rec := postGate(t, s, "/api/gate/approve", s.token, "approve")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 while a leg of the run is in flight", rec.Code)
	}
	resumer.requireNoCall(t)
	// The probe must leave the other leg's lock exactly where it found it.
	if _, err := os.Stat(filepath.Join(dir, lockFileName)); err != nil {
		t.Errorf("the refused decision removed another leg's resume.lock: %v", err)
	}
}

func TestGateDecision_WithoutAResumerIsConflict(t *testing.T) {
	// The embedded live view of a run in flight: no resumer is injected, so
	// the honest answer is that this view cannot decide, never a half-applied
	// decision written straight into state.json.
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID: "run-gate",
		Graph: json.RawMessage(gateGraph),
		Gate:  runstate.GateState{PausedAt: "approve"},
	})
	s := newTestServer(dir, "run-gate")

	rec := postGate(t, s, "/api/gate/approve", s.token, "approve")

	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d, want 409 from a view with no resume machinery", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "resume") {
		t.Errorf("the refusal must point at `oh-my-graph resume`, got %q", rec.Body.String())
	}
}

// --- the decision reaches the CLI's resume machinery unchanged ---------------

func TestGateDecision_HandsTheNamedGateAndDecisionToTheResumer(t *testing.T) {
	for _, tc := range []struct {
		path string
		want gate.Decision
	}{
		{"/api/gate/approve", gate.DecisionApprove},
		{"/api/gate/reject", gate.DecisionReject},
	} {
		t.Run(tc.path, func(t *testing.T) {
			resumer := newFakeResumer()
			s, _ := pausedGateServer(t, resumer)

			rec := postGate(t, s, tc.path, s.token, "approve")

			// 202: the leg runs for minutes and reports itself through the run
			// feed the page is already streaming.
			if rec.Code != http.StatusAccepted {
				t.Fatalf("status = %d, want 202 (body %q)", rec.Code, rec.Body.String())
			}
			want := resumeCall{runID: "run-gate", gateID: "approve", decision: tc.want}
			if got := resumer.awaitCall(t); got != want {
				t.Errorf("resumed %+v, want %+v", got, want)
			}
		})
	}
}

func TestGateDecision_GetDecidesNothing(t *testing.T) {
	// The routes are method-scoped POSTs, so the shapes that reach a URL
	// without a form — a link, an <img> src, a prefetch — cannot decide a
	// gate. Such a GET falls through to the static file server, which has no
	// asset by that name: 404, and no leg.
	resumer := newFakeResumer()
	s, _ := pausedGateServer(t, resumer)
	req := httptest.NewRequestWithContext(context.Background(), "GET", "http://127.0.0.1:8642/api/gate/approve", nil)
	req.Header.Set("X-OMG-Token", s.token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /api/gate/approve status = %d, want 404 (no such asset, and no decision)", rec.Code)
	}
	resumer.requireNoCall(t)
}
