package serve

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// GateResumer continues a run that is paused at a gate, applying exactly one
// decision to exactly one named gate. It is the seam that lets the live view
// decide a gate WITHOUT owning any gate logic: the implementation is built and
// injected by the CLI (cmd/oh-my-graph's cliGateResumer, which calls the same
// executeResume that `oh-my-graph resume --approve/--reject` calls), so there
// is one resume implementation in the repo and the browser path and the CLI
// path cannot drift.
//
// A nil resumer means this server cannot resume anything (the embedded live
// view of a run in flight — see WithGateResumer): its gate routes answer 409,
// they never half-apply a decision by writing state.json themselves.
type GateResumer interface {
	Resume(ctx context.Context, runID, gateID string, decision gate.Decision) error
}

// WithGateResumer injects the resume machinery the gate routes need and
// returns the Server, so it chains onto New. Only the standalone `serve`
// process wires a real one: it is the only process that can ever be looking at
// a paused run (ADR 0003 — the pause exits the run process, taking its
// embedded live view with it), and a run in flight holds the resume.lock its
// leg would need anyway.
func (s *Server) WithGateResumer(r GateResumer) *Server {
	s.resumer = r
	return s
}

// newGateToken mints this process's gate token: 32 bytes of crypto/rand as
// hex. It is generated once per Server (New), embedded in the served page
// (handleIndex), and demanded back on every gate POST — see requireGateToken
// for what it is and is not protecting against.
func newGateToken() string {
	buf := make([]byte, 32)
	// crypto/rand.Read never returns an error (it crashes the program if the
	// system source fails), so there is no error path to handle here.
	rand.Read(buf)
	return hex.EncodeToString(buf)
}

// gateRequest is a gate POST's body. The gate id is named by the client and
// never inferred by the server: ADR 0003's rule is that the decision is
// explicit, so a stale page cannot approve whichever gate the run happens to
// be sitting at now (the handler refuses any id that is not where the run is
// actually paused).
type gateRequest struct {
	Node string `json:"node"`
}

// maxGateBody caps a gate POST's body — it carries one node id and nothing
// else, so anything larger is not a request this server made.
const maxGateBody = 4 << 10

// requireGateToken reports whether the request carries this process's gate
// token in X-OMG-Token, answering the client itself when it does not.
//
// SECURITY: this is the CSRF guard, and it is needed precisely because the
// two guards that came before it do not cover a mutating route. The loopback
// bind keeps remote clients out and requireLoopbackHost stops DNS rebinding,
// but neither stops a page the user is already visiting from POSTing to
// http://127.0.0.1:8642/api/gate/approve: such a request carries a loopback
// Host header and passes both. The token closes that: it exists only inside
// the page this process served, so a cross-origin caller cannot read it, and
// sending it as a custom header (rather than a form field) means the browser
// must preflight the POST — which a cross-origin form cannot do at all. A
// missing token is 400 (the request is malformed for this API), a wrong one
// is 403; the comparison is constant-time.
func (s *Server) requireGateToken(w http.ResponseWriter, r *http.Request) bool {
	token := r.Header.Get("X-OMG-Token")
	if token == "" {
		http.Error(w, "missing X-OMG-Token: a gate decision must carry the token embedded in the page this server served", http.StatusBadRequest)
		return false
	}
	if subtle.ConstantTimeCompare([]byte(token), []byte(s.token)) != 1 {
		http.Error(w, "forbidden: X-OMG-Token does not match this live view's token", http.StatusForbidden)
		return false
	}
	return true
}

// handleGateDecision answers POST /api/gate/approve and POST /api/gate/reject
// with the given decision.
//
// BOUNDARY: this is where serve stops being read-only. Every other route
// reads; these two continue the run — the resumed leg writes state.json,
// appends to events.jsonl, and (through the injected GateResumer, which is
// built in cmd/) spawns the node processes the gate was blocking. That is a
// deliberate change of what `serve` is, recorded in ADR 0011 and in DESIGN.md's
// serve section; it is guarded by requireLoopbackHost (Host), the loopback
// bind (reachability) and requireGateToken (CSRF), in that order.
//
// A decision is valid ONLY while the viewed run is actually paused at the
// named gate; everything else is 409, so the button can never start a leg the
// user did not see a pause for:
//
//   - resume.lock held → a run or another resume is in flight
//   - no snapshot yet → the run has not completed a node, so it cannot be paused
//   - Gate.PausedAt empty → the run is not paused
//   - the named gate is not the one the run is paused at (ADR 0003's explicit-id
//     rule: a stale page must not approve a gate the user was not looking at)
//   - no GateResumer injected → this view cannot resume anything
//
// The leg itself is started in a goroutine and the response is 202 Accepted,
// because a resume runs nodes for minutes: the run feed (docs/RUN-FEED.md) is
// the progress report, streaming into the /api/events connection the page
// already holds open — handleEvents deliberately does not end at run_finished
// for exactly this reason. The leg's context is deliberately NOT the request's:
// a viewer closing the tab must not kill a leg that is spending money.
func (s *Server) handleGateDecision(decision gate.Decision) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !s.requireGateToken(w, r) {
			return
		}
		if s.resumer == nil {
			http.Error(w, "conflict: this live view cannot decide gates (an embedded view of a run in flight); decide it with `oh-my-graph resume`", http.StatusConflict)
			return
		}

		var req gateRequest
		if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxGateBody)).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("read gate decision: %v", err), http.StatusBadRequest)
			return
		}
		if req.Node == "" {
			http.Error(w, `bad request: name the gate to decide, as {"node":"<gate-id>"}`, http.StatusBadRequest)
			return
		}

		snap, err := s.pausedSnapshot()
		if err != nil {
			writeGateRefusal(w, err)
			return
		}
		if req.Node != snap.Gate.PausedAt {
			// resume's own wording (resumeDecision), for the same reason.
			http.Error(w, fmt.Sprintf("conflict: named gate %q but the run is paused at %q", req.Node, snap.Gate.PausedAt), http.StatusConflict)
			return
		}

		// Detached from the request on purpose (see the doc comment): the leg
		// outlives the POST that started it. Cancellation of the leg belongs to
		// the serve process's own signal handling, which the injected resumer
		// installs exactly as the CLI does.
		legCtx := context.WithoutCancel(r.Context())
		gateID := req.Node
		go func() {
			// The leg reports itself where the UI is already looking: its events
			// land in events.jsonl and stream into the open SSE connection, and
			// the injected resumer owns terminal reporting of a failure. There is
			// no response left to write an error to.
			_ = s.resumer.Resume(legCtx, s.runID, gateID, decision)
		}()

		w.WriteHeader(http.StatusAccepted)
		fmt.Fprintf(w, "resuming run %s: gate %s %s\n", s.runID, gateID, decision)
	}
}

// gateRefusal is a refusal to decide a gate that the client should see as a
// specific status: the run is not in a state where a decision means anything.
type gateRefusal struct {
	status int
	msg    string
}

func (e *gateRefusal) Error() string { return e.msg }

// writeGateRefusal answers a pausedSnapshot failure: a gateRefusal carries its
// own status, anything else is a 500 carrying the reason (a corrupt snapshot,
// or a schema this binary refuses to read — runstate.Load's loud refusal —
// must never read as "not paused").
func writeGateRefusal(w http.ResponseWriter, err error) {
	var refusal *gateRefusal
	if errors.As(err, &refusal) {
		http.Error(w, refusal.msg, refusal.status)
		return
	}
	http.Error(w, fmt.Sprintf("load run state: %v", err), http.StatusInternalServerError)
}

// pausedSnapshot loads the run's snapshot and returns it only if the run is
// genuinely stopped at a gate right now, as *the disk* says — the single
// source of truth is Gate.PausedAt (docs/RUN-FEED.md; runstate.GateState).
//
// The resume.lock is taken first and released again before returning: holding
// it is how "no leg is in flight" is established (the run process holds it for
// its whole duration), and releasing it is what lets the resumer take the same
// lock for real, exactly as the CLI's resume does. The window between the two
// is not a correctness hole — the resumer re-checks both the lock and
// Gate.PausedAt under the lock and refuses the leg if either changed — it only
// means such a loss shows up in the feed rather than in this response.
func (s *Server) pausedSnapshot() (runstate.Snapshot, error) {
	release, err := runstate.AcquireLock(filepath.Join(s.runDir, lockFileName))
	if err != nil {
		var held *runstate.LockHeldError
		if errors.As(err, &held) {
			return runstate.Snapshot{}, &gateRefusal{
				status: http.StatusConflict,
				msg:    fmt.Sprintf("conflict: a leg of run %q is in flight, so it is not paused for a decision", s.runID),
			}
		}
		return runstate.Snapshot{}, err
	}
	snap, loadErr := runstate.Load(filepath.Join(s.runDir, stateFileName))
	if relErr := release(); relErr != nil {
		return runstate.Snapshot{}, fmt.Errorf("release resume lock: %w", relErr)
	}
	if loadErr != nil {
		if errors.Is(loadErr, fs.ErrNotExist) {
			return runstate.Snapshot{}, &gateRefusal{
				status: http.StatusConflict,
				msg:    fmt.Sprintf("conflict: run %q has no snapshot yet, so it is not paused at a gate", s.runID),
			}
		}
		return runstate.Snapshot{}, loadErr
	}
	if snap.Gate.PausedAt == "" {
		// resume's own wording (resumeGateLeg), so the two front-ends refuse
		// the same non-pause the same way.
		return runstate.Snapshot{}, &gateRefusal{
			status: http.StatusConflict,
			msg:    fmt.Sprintf("conflict: run %q is not paused (nothing to decide)", s.runID),
		}
	}
	return snap, nil
}
