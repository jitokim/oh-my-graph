// Package serve owns `oh-my-graph serve`: a web live view of ONE run — the
// DAG rendered in the browser with nodes colored live as they run (Airflow's
// Graph View, for this tool's runs). It is a consumer of the run-feed
// contract (docs/RUN-FEED.md): it reads state.json for the run's structure
// and tails events.jsonl for its progress. The one read outside the run
// directory is /api/transcript's: the transcript file of a RUNNING node's own
// session, under the user's claude projects dir (see handleTranscript's
// boundary note). Fleet-wide observation stays fleetops's job; this is one
// run, live, locally.
//
// It is no longer strictly read-only. Every route reads except the two gate
// routes (see handleGateDecision and ADR 0014): approving or rejecting the
// gate a run is paused at continues the run, which rewrites state.json,
// appends to events.jsonl and runs the nodes the gate was blocking. This
// package still owns no gate logic — it calls the injected GateResumer, which
// the CLI builds over the same code path `oh-my-graph resume` takes.
//
// SECURITY: the listener binds to 127.0.0.1 ONLY (see Listen), and every
// request's Host header must name loopback (see requireLoopbackHost) so a
// hostile page cannot reach /api/* by DNS-rebinding a domain it controls onto
// 127.0.0.1. Run directories contain node prompts, artifacts and session ids
// — and the sessions they name hold full transcripts — so the server must
// never be reachable from off-host. Access control is that pair plus, for the
// mutating gate routes only, TWO more checks in this order (handleGateDecision):
// requireSameOrigin refuses a decision whose Origin is not this server's own,
// and requireGateToken demands back a per-process random token embedded in the
// served page. Both exist because a loopback bind and a Host check do not stop
// a page the user is already visiting from POSTing to 127.0.0.1; the origin
// check refuses such a page on its provenance before its token is weighed at
// all. Widening the bind address would still need a real auth story first;
// neither of the two is a login.
//
// Those guards all answer "where did this request come from", and a clickjack
// answers all of them honestly by never leaving this origin: framed, overlaid
// and clicked, the page POSTs its own token from its own origin. So every
// response also carries frame-ancestors 'none' and X-Frame-Options: DENY —
// the one guard that refuses the framing itself rather than the request — plus
// nosniff and the rest of contentSecurityPolicy, stamped over both front-ends
// by securityHeaders.
//
// The server itself spawns no processes: it does not shell out to
// `open`/`xdg-open` to launch a browser, and it does not run nodes. Exactly
// four objects in this repo may spawn a process (internal/invariants, ADR
// 0002/0005/0006), and both processes this package's features imply belong to
// them — browser-open to browser.ExecOpener behind the browser.Opener seam
// (ADR 0006), and a resumed leg's nodes to runner.CLIRunner, reached
// only through the GateResumer the CLI injects (ADR 0014). The CLI decides
// both: `run`/`auto` embed this server for the run's duration, and the
// standalone `serve` subcommand serves either one run (Server) or all of them
// (Dashboard) and is the only one that injects a GateResumer. All of them hand
// the URL to the injected Opener under one gate — stdout is a terminal and the
// opt-out (--no-web, or `serve`'s --no-open) was not passed.
package serve

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// stateFileName and lockFileName are the two run-directory files this server
// reads beyond the event stream and node artifacts: the resumable snapshot
// (which says whether the run is paused at a gate) and the concurrent-leg
// guard the gate routes take before deciding anything.
const (
	// Both names are runstate's, for the same reason: the package that writes
	// the snapshot and takes the lock owns what they are called, and a
	// hand-spelled copy here is one more place the contract can drift.
	stateFileName = runstate.SnapshotFileName
	// The lock's name is runstate's, not this package's: since ADR 0015 §3 it
	// is contract surface, and the package that takes and probes it owns it.
	lockFileName = runstate.LockFileName
)

// DefaultPort is the port `serve` binds when --port is not given. It is an
// arbitrary high port chosen not to collide with the usual local-dev
// suspects (3000, 5173, 8000, 8080); --port overrides it when it is taken.
const DefaultPort = 8642

// defaultPoll is the SSE tail's end-of-stream re-read interval — the same
// cadence `watch` uses, for the same reason: slow enough not to spin, fast
// enough that an appended event reaches the browser effectively as it lands.
const defaultPoll = 200 * time.Millisecond

// uiFS embeds the whole static UI — page, hand-written JS/CSS, and the
// pinned, vendored libraries (cytoscape.js, dagre, cytoscape-dagre; see
// ui/vendor/README.md for versions and licenses) — so the served page has
// zero runtime network dependencies: no CDN fetch, nothing leaves the host
// to render a run.
//
//go:embed ui
var uiFS embed.FS

// indexTemplate is the single-run page, parsed once for the process. It is the
// ONE asset not shipped byte-for-byte (it carries the serving process's gate
// token), and template.Must is honest about the only way it can fail: the file
// is embedded at compile time, so a parse error is a build-time bug.
var indexTemplate = template.Must(template.ParseFS(uiFS, "ui/index.html"))

// Listen binds the live-view listener to 127.0.0.1 on the given port.
//
// SECURITY: the loopback bind is a requirement, not a default — run
// directories contain prompts, artifacts and session ids, so the server must
// never be reachable off-host. This constructor is the single place the bind
// address is chosen, precisely so no caller can widen it to 0.0.0.0 by
// passing a different host; TestListen_BindsLoopbackOnly pins the behavior.
// Port 0 asks the OS for a free port (tests use this).
func Listen(port int) (net.Listener, error) {
	addr := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen on %s: %w (if the port is taken, pick another with --port)", addr, err)
	}
	return listener, nil
}

// requireLoopbackHost rejects any request whose Host header does not name
// this machine's loopback — 127.0.0.1 or localhost, with or without a port.
//
// SECURITY: this is the DNS-rebinding guard. The loopback bind keeps remote
// clients out, but a hostile page can point a domain it controls at 127.0.0.1
// and have the victim's own browser issue same-origin requests to /api/* —
// arriving over loopback, yet carrying the attacker's hostname. Matching the
// Host header against the only names a legitimate local viewer uses closes
// that hole; anything else is 403.
func requireLoopbackHost(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		host := r.Host
		if h, _, err := net.SplitHostPort(host); err == nil {
			host = h
		}
		if host != "127.0.0.1" && host != "localhost" {
			http.Error(w, "forbidden: the live view answers only loopback hosts (127.0.0.1 or localhost)", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// contentSecurityPolicy is the policy every response carries. It is written
// against what the shipped ui/ assets actually do, directive by directive:
//
//   - default-src 'none'  — nothing is allowed that is not named below, so a
//     directive this page does not need (font-src, media-src, worker-src,
//     manifest-src) denies rather than inherits something permissive.
//   - script-src 'self'   — index.html and dashboard.html load four and one
//     same-origin <script src> respectively and carry no inline script, no
//     on*= handler and no javascript: URL. No 'unsafe-eval': none of the three
//     vendored libraries calls eval(, and the only Function-constructor call in
//     any of them is lodash's root fallback, `freeGlobal || freeSelf ||
//     Function("return this")()`, in BOTH cytoscape.min.js and dagre.min.js.
//     That call is eval-equivalent and script-src would block it — but in a
//     browser `freeSelf` (self && self.Object === Object && self) is truthy, so
//     the third operand is never evaluated. It is dead code here, not an
//     absence; a version that reached it would need 'unsafe-eval'.
//   - style-src 'self' 'unsafe-inline' — the two same-origin stylesheets, PLUS
//     the one inline stylesheet this page cannot avoid: cytoscape injects a
//     <style> element holding ".__________cytoscape_container { position:
//     relative; }" (renderer init in cytoscape.min.js). That rule is what makes
//     the container a positioning context for the absolutely-positioned layers
//     it then draws into, so blocking it does not degrade the map, it
//     mis-places it. 'unsafe-inline' is therefore on STYLE only; script-src
//     stays strict, which is where an injection actually costs something.
//   - img-src 'self'      — the pages reference no image at all (the DAG is a
//     <canvas>, and style.css's only background-image is a gradient, which no
//     directive governs), but a browser still probes /favicon.ico against
//     img-src, and 'self' keeps that an ordinary 404 rather than a console
//     violation. Nothing loads a data: image, so data: is not listed.
//   - connect-src 'self'  — every fetch() and EventSource in app.js and
//     dashboard.js is a document-relative path under this same origin.
//   - base-uri 'none' and form-action 'none' — neither page has a <base> or a
//     <form>, so both are denied outright rather than left to default-src.
//   - frame-ancestors 'none' — the clickjacking guard; see securityHeaders.
//
// A cytoscape bump must re-verify this policy (ui/vendor/README.md says so):
// the inline <style> above is the vendored code's behavior, not a contract, and
// a version that also needed eval or a Worker would break the page silently.
const contentSecurityPolicy = "default-src 'none'; " +
	"script-src 'self'; " +
	"style-src 'self' 'unsafe-inline'; " +
	"img-src 'self'; " +
	"connect-src 'self'; " +
	"base-uri 'none'; " +
	"form-action 'none'; " +
	"frame-ancestors 'none'"

// securityHeaders stamps every response of BOTH front-ends with the headers
// that constrain what a browser may do with what this server returns. It wraps
// requireLoopbackHost rather than sitting inside it, so even a 403 refusal
// carries them.
//
// SECURITY: frame-ancestors 'none' (with X-Frame-Options: DENY for the same
// answer to anything that predates CSP) is the clickjacking guard, and it
// closes a hole that EVERY other guard in this package leaves open — because
// every other guard asks where a request came from, and a clickjack never
// leaves this origin. A hostile page the user is already visiting frames
// http://127.0.0.1:8642/run/<id>/, overlays it, and baits a click onto the
// approve button: the click lands INSIDE the framed page, so the page's own
// JavaScript reads its own <meta name="omg-token"> and the browser stamps this
// server's own Origin. requireLoopbackHost sees a loopback Host,
// requireSameOrigin sees its own origin, requireGateToken sees the real token
// in constant time — all four pass, correctly, and a gate the user never read
// is approved, starting a leg that spends money (ADR 0014). The only place to
// refuse that is at the framing itself, which is what this header does.
//
// X-Content-Type-Options: nosniff matters most on /api/result, which serves raw
// model output as text/plain: without it a browser may sniff attacker-shaped
// node output into HTML and render it as a document on this origin.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := w.Header()
		header.Set("Content-Security-Policy", contentSecurityPolicy)
		header.Set("X-Frame-Options", "DENY")
		header.Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

// Server serves one run's live view out of its run directory. It holds no
// state ABOUT THE RUN beyond the paths: every request re-reads the contract
// files, so the view is always as fresh as the disk and the server survives
// the run's files appearing after it started (a fresh run's state.json
// window). Past the two paths (runDir, and projectsRoot for /api/transcript)
// and the run's own id, the fields are this process's own: the poll interval,
// the gate token it mints, and the resume machinery the CLI injects. The page
// template that carries the token is deliberately NOT among them — it is
// parsed once per process, not once per served run (indexTemplate above).
type Server struct {
	runDir string
	runID  string
	poll   time.Duration
	// projectsRoot is where /api/transcript looks for the claude CLI's
	// session transcripts (the user's ~/.claude/projects). A field, not a
	// call site, so tests point it at a fixture directory and never touch
	// the real one.
	projectsRoot string
	// token is this process's CSRF token for the gate routes, minted once in
	// New, rendered into the served page and demanded back on every gate POST
	// (see requireGateToken).
	token string
	// resumer continues a run paused at a gate; nil (the default) means this
	// view answers 409 to every gate decision. See WithGateResumer.
	resumer GateResumer
	// build names the binary serving this page, rendered into its footer so a
	// long-lived `serve` cannot be mistaken for the build that replaced it
	// (BuildLabel). Empty — the default — renders nothing at all, which is what
	// every test and any caller that does not care gets.
	build string
}

// New builds a Server for one run directory. runID is the directory's name —
// echoed to the UI so the page can identify the run even before any file
// exists to read it from. The returned Server has no GateResumer: a live view
// is read-only until the CLI injects one (WithGateResumer).
func New(runDir, runID string) *Server {
	return &Server{
		runDir:       runDir,
		runID:        runID,
		poll:         defaultPoll,
		projectsRoot: defaultProjectsRoot(),
		token:        newGateToken(),
	}
}

// Handler returns the server's routes:
//
//	/            the served page, rendered with this process's gate token
//	/index.html  the same page (the FileServer's own name for it)
//	/api/graph   the run's DAG structure as JSON (node ids + depends_on edges,
//	             plus the goal-lineage block when the run is a goal cycle, plus
//	             the abandoned answer and its recovery hint when the run's open
//	             leg has no process left behind it — ADR 0015 §4, plus the
//	             transcript-tail note when this run's runtime keeps no per-node
//	             transcript to tail — #178, transcriptTailNote)
//	/api/events  the run's event stream as SSE: replay events.jsonl, then follow
//	/api/result  one node's handoff artifact as text/plain (?node=<id>)
//	/api/transcript  a RUNNING node's live transcript tail as JSON (?node=<id>)
//	POST /api/gate/approve  decide the gate the run is paused at (?node in the body)
//	POST /api/gate/reject   the same, rejecting
//	/*           the rest of the embedded static UI (app.js, style.css, vendored libraries)
//
// Every GET reads only: the run directory, plus — for /api/transcript — the
// one transcript file the run's own feed names (see handleTranscript's
// boundary note). /api/graph's liveness answer keeps that property: the lock
// probe behind it takes a SHARED, non-blocking lock on a read-only fd and
// creates, writes and removes nothing (ADR 0015 §1, runstate.ProbeLock).
// The two gate POSTs are the mutating routes (ADR 0014): they
// continue the paused run through the injected GateResumer, and they carry
// their own CSRF guard on top of the ones every route gets. Every route sits
// behind requireLoopbackHost, the DNS-rebinding guard; the method-scoped
// patterns mean a GET of a gate route decides nothing without any code — it
// does not 405, because the "GET /" subtree catch-all below matches it and the
// static file server answers 404 for an asset that does not exist. Either way
// no leg is resumed, which is the property that matters
// (TestGateDecision_GetDecidesNothing pins it).
//
// Every route also carries securityHeaders — the frame guard that stops the
// gate buttons below from being clickjacked, and the CSP the shipped assets
// were audited against.
//
// This is the whole live view of one run as a standalone site, rooted at "/":
// what `oh-my-graph serve <run-id>` serves. The Dashboard serves the SAME
// route set — routes(), without the guards it re-applies once for every route
// it owns — under /run/<id>/, which is why the page's own fetches are
// document-relative (see routes).
func (s *Server) Handler() http.Handler {
	return securityHeaders(requireLoopbackHost(s.routes()))
}

// routes is Handler's route set without the loopback guard, so it can be
// mounted: at "/" by Handler (the standalone single-run server) and under
// /run/<id>/ by the Dashboard, which applies requireLoopbackHost once across
// everything it serves.
//
// Nothing in the route set or the page knows which of the two it is under:
// every URL the page fetches is document-relative ("api/graph", "app.js"), so
// the same bytes address /api/graph under one mount and /run/<id>/api/graph
// under the other.
func (s *Server) routes() *http.ServeMux {
	static, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// Unreachable: "ui" is embedded at compile time.
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/graph", s.handleGraph)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/result", s.handleResult)
	mux.HandleFunc("GET /api/transcript", s.handleTranscript)
	mux.HandleFunc("POST /api/gate/approve", s.handleGateDecision(gate.DecisionApprove))
	mux.HandleFunc("POST /api/gate/reject", s.handleGateDecision(gate.DecisionReject))
	// The page is rendered per process (it carries the gate token); every
	// other asset is still shipped byte-for-byte off the embedded FS. `{$}`
	// matches the root path exactly, so the bare "GET /" below stays the
	// subtree catch-all for the JS, CSS and vendored libraries.
	mux.HandleFunc("GET /{$}", s.handleIndex)
	mux.HandleFunc("GET /index.html", s.handleIndex)
	mux.Handle("GET /", http.FileServerFS(static))
	return mux
}

// WithBuild sets the build label this view's footer states, and returns the
// Server so it chains onto New the way WithGateResumer does. The CLI is the
// only caller: the release version lives in package main, and the answer to
// "which binary is this" belongs to the process, not to the run it is showing.
func (s *Server) WithBuild(label string) *Server {
	s.build = label
	return s
}

// handleIndex serves the live view's page with this process's gate token
// rendered into its <meta name="omg-token">, which is where the page's
// approve/reject buttons read it from. The token is per Server and never
// written to disk, so it dies with the process that minted it — a page left
// open from a previous `serve` cannot decide this run's gate.
//
// It carries the build label out on the same render, for the same reason and
// with the opposite audience: the token identifies this process to the server,
// the label identifies it to the reader (BuildLabel).
func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	var page bytes.Buffer
	if err := indexTemplate.Execute(&page, struct{ Token, Build string }{s.token, s.build}); err != nil {
		http.Error(w, fmt.Sprintf("render page: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Write(page.Bytes())
}

// graphPayload is /api/graph's response body. Available is false while the run
// exists but its snapshot does not — which is NOT "until the first node
// settles": `run`/`auto` writes state.json up front, before the scheduler
// starts (runstate.SnapshotRecorder.WriteInitial). The snapshot-less shapes are
// the ones docs/RUN-FEED.md names as legitimate: an `auto` run still inside its
// planner call, and one whose planner reply was refused (ADR 0023). There the
// structure is honestly "not known yet" — the UI polls until Available flips
// true rather than the server inventing a structure from anywhere else.
type graphPayload struct {
	RunID     string      `json:"run_id"`
	Available bool        `json:"available"`
	Name      string      `json:"name,omitempty"`
	Nodes     []graphNode `json:"nodes,omitempty"`
	// Goal is the run's goal-lineage block when this run is one cycle of an
	// iterated auto goal (ADR 0011 §4: serve stays a per-run view and shows
	// the goal block in its header). Absent on every single-cycle run.
	Goal *goalPayload `json:"goal,omitempty"`
	// Abandoned and Hint carry the one run-level fact the page cannot derive
	// from the stream it is handed: a leg the stream leaves open whose process
	// is gone (ADR 0015 §2). Deriving it needs the run lock, and only this side
	// can probe that. Both are absent on every other run, so a payload for a
	// healthy run is byte-identical to before.
	//
	// The page is the surface that carries the gate button, and that button
	// starts a leg that spends money (ADR 0014), so it is the surface that most
	// needs the sentence — the same argument that puts Hint on the dashboard
	// card that links here (ADR 0015 §4, the residual-hazard paragraph).
	Abandoned bool   `json:"abandoned,omitempty"`
	Hint      string `json:"hint,omitempty"`
	// TranscriptNote is the other run-level fact the page cannot derive: this
	// run's runtime keeps no per-node session transcript, so the live tail
	// cannot exist here (transcriptTailNote, #178). It carries the sentence to
	// show in the tail's place, and its ABSENCE means the tail works — so a
	// claude run's payload is byte-identical to before.
	//
	// It rides on /api/graph, not on /api/transcript, because it is the answer
	// that lets the page stop asking: knowing it up front is what retires a
	// per-running-node request every 3 seconds that could never succeed. It is
	// a run-level field because the runtime is run-wide (ADR 0025), and it is
	// available whenever the snapshot is — which, since state.json is written
	// before the first node starts (Available above), means the page has the
	// answer in its FIRST fetch on every run that has a graph at all. The
	// snapshot-less shapes carry no answer because the runtime is snapshot-only,
	// carried by no event; they are also the shapes where no node is running, so
	// there is no tail to be wrong about.
	TranscriptNote string `json:"transcript_note,omitempty"`
}

// goalPayload is runstate.GoalRef re-encoded for the UI rather than embedded,
// so serve's response shape cannot silently change when the snapshot's does.
type goalPayload struct {
	Text       string `json:"text"`
	Cycle      int    `json:"cycle"`
	MaxCycles  int    `json:"max_cycles"`
	FirstRunID string `json:"first_run_id"`
}

// graphNode is one node of the DAG as the UI needs it: identity, its
// depends_on edges, and the type (so a gate can be drawn as a gate).
type graphNode struct {
	ID        string   `json:"id"`
	Type      string   `json:"type,omitempty"`
	DependsOn []string `json:"depends_on,omitempty"`
}

// loadRunGraph reconstructs the run's graph from the snapshot's own Graph
// bytes via the same graph.Parse path `resume` and `runs list` trust — never
// by re-reading the source YAML, which may have been edited since. A missing
// snapshot surfaces as fs.ErrNotExist for the caller to translate (each
// endpoint's honest answer differs); any other failure is a load/parse error
// worth a 500 carrying the reason.
func (s *Server) loadRunGraph() (*graph.Graph, error) {
	g, _, err := s.loadRunGraphAndSnapshot()
	return g, err
}

// loadRunGraphAndSnapshot is loadRunGraph plus the snapshot it was
// reconstructed from, for the one endpoint (/api/graph) that renders more of
// it than the DAG: the goal-lineage block and the runtime behind the
// transcript-tail answer. Kept as one load so every field of a payload comes
// from the same snapshot read.
func (s *Server) loadRunGraphAndSnapshot() (*graph.Graph, runstate.Snapshot, error) {
	snap, err := runstate.Load(filepath.Join(s.runDir, stateFileName))
	if err != nil {
		return nil, runstate.Snapshot{}, err
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return nil, runstate.Snapshot{}, fmt.Errorf("reconstruct graph: %w", err)
	}
	return g, snap, nil
}

// handleGraph serves the run's DAG structure, plus the run-level liveness
// answer the page cannot reach by itself. A snapshot that exists but cannot be
// read (corrupt, or a schema this binary does not understand — runstate.Load's
// loud refusal) is a 500 carrying the reason, not a silent empty graph.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	g, snap, err := s.loadRunGraphAndSnapshot()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, s.withRunStatus(graphPayload{RunID: s.runID, Available: false}))
			return
		}
		http.Error(w, fmt.Sprintf("load run graph: %v", err), http.StatusInternalServerError)
		return
	}

	payload := graphPayload{
		RunID:     s.runID,
		Available: true,
		Name:      g.Name,
		// Empty on a claude run, and then omitted: the tail works and the page
		// polls it exactly as it always has.
		TranscriptNote: transcriptTailNote(snap.Runtime),
	}
	if goal := snap.Goal; goal != nil {
		payload.Goal = &goalPayload{Text: goal.Text, Cycle: goal.Cycle, MaxCycles: goal.MaxCycles, FirstRunID: goal.FirstRunID}
	}
	for _, node := range g.Nodes {
		payload.Nodes = append(payload.Nodes, graphNode{ID: node.ID, Type: node.Type, DependsOn: node.DependsOn})
	}
	writeJSON(w, s.withRunStatus(payload))
}

// withRunStatus attaches the abandoned answer and its recovery hint, through
// internal/runstatus — the ONE composition of "an open leg AND a free lock"
// that `runs list`, the dashboard card, ResolveRun and `watch` also go through,
// never a second copy of the rule (ADR 0015 §2).
//
// Available doubles as "this run has a readable snapshot", which is the fact the
// hint splits on: with no snapshot there is nothing to resume from, so the
// honest advice is to run the graph again. That is the same split the card makes.
//
// A stream this binary refuses to read (a newer schema) answers "not
// abandoned": serve's posture there is to keep rendering rather than refuse
// (handleEvents), and a false abandoned would invite an operator to resume a
// live run — the direction ADR 0015 never takes.
//
// The answer is the one the request is served at, and it is not re-pushed: the
// page re-asks on every leg boundary it sees, and a run that dies while the
// page is already open keeps painting until then. That is the accepted gap
// ADR 0015 §4 states for `watch` — no idle-time probe — reached here for the
// same reason: the probe belongs to a reader asking a question, not to the pure
// stream follower.
func (s *Server) withRunStatus(payload graphPayload) graphPayload {
	status, err := runstatus.Of(s.runDir)
	if err != nil || status != runstatus.Abandoned {
		return payload
	}
	payload.Abandoned = true
	payload.Hint = runstatus.Hint(s.runID, payload.Available)
	return payload
}

// handleResult serves one node's handoff artifact (`<run-dir>/<node-id>.out`,
// the file artifact handoff persists) as text/plain — WHAT the node did, the
// thing a human clicks a node for.
//
// SECURITY: the node id taken from the URL is matched against the run's own
// node-id set — from the snapshot's graph, the same source /api/graph serves
// — BEFORE any filesystem use. URL input is never sanitized-and-joined into
// a path; an id the graph does not contain (a typo and a traversal probe
// alike) is a 404. While no snapshot exists the node set is unknown, so no
// id can be vouched for and every id is a 404.
//
// A KNOWN node whose artifact file does not exist is 204 No Content — "no
// result yet" (still running, a gate, `handoff: session`), deliberately
// distinct from 404's "no such node" so the UI can render each honestly.
func (s *Server) handleResult(w http.ResponseWriter, r *http.Request) {
	g, err := s.loadRunGraph()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			http.NotFound(w, r)
			return
		}
		http.Error(w, fmt.Sprintf("load run graph: %v", err), http.StatusInternalServerError)
		return
	}

	nodeID := r.URL.Query().Get("node")
	known := false
	for _, node := range g.Nodes {
		if node.ID == nodeID {
			known = true
			break
		}
	}
	if !known {
		http.NotFound(w, r)
		return
	}

	data, err := os.ReadFile(filepath.Join(s.runDir, nodeID+".out"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, fmt.Sprintf("read node artifact: %v", err), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Write(data)
}

// handleEvents streams the run's events.jsonl as Server-Sent Events: every
// line already on disk is replayed immediately, then appended lines follow
// as they land, via runfeed.FollowWait — the wait-for-create variant of the
// same tail `watch` uses, because a fresh run's directory can briefly exist
// before its stream does (and a pre-runfeed directory has no stream at all),
// and the connection must be held open rather than 404ing a healthy run.
// Each event is forwarded as one SSE `data:` frame carrying the stream's own
// JSON line verbatim — the browser reads the run-feed contract, not a
// re-encoding of it.
//
// Per RUN-FEED.md's compatibility rule a schema bump must be visible, not
// fatal: on the first event stamped with a schema newer than this binary the
// handler sends one non-terminal `stream_warning` frame and KEEPS forwarding
// — the same warn-once-and-keep-rendering posture `watch` takes, with the UI
// skipping event types it does not know. (`runs list` refuses instead, but a
// list can skip one run; a live view going permanently blank on a routine
// bump would make the bump fatal.) A line that does not decode at all is
// skipped (the contract's tolerated truncated-final-line damage), and so is
// a decodable line containing a raw \r or \n, which sendSSE could not carry
// in one frame. The stream ends when the client disconnects; it deliberately
// does NOT end at run_finished, because a resumed leg appends to the same
// file and the viewer should see it.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}
	feedPath := filepath.Join(s.runDir, runfeed.FileName)

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	warnedSchema := false
	err := runfeed.FollowWait(r.Context(), feedPath, s.poll, func(line []byte) (bool, error) {
		var event runfeed.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return false, nil
		}
		if bytes.ContainsAny(line, "\r\n") {
			// A line can decode and still hold a bare \r between JSON tokens
			// (legal JSON whitespace, though our writer never emits it) — and
			// an SSE data field is one line, so forwarding it would split the
			// frame. Skipped, like an undecodable line.
			return false, nil
		}
		if event.Schema > runfeed.Schema && !warnedSchema {
			warnedSchema = true
			sendSSE(w, flusher, "stream_warning", errorFrame(fmt.Sprintf(
				"event stream schema %d is newer than this binary understands (max %d); some events may render generically",
				event.Schema, runfeed.Schema)))
		}
		sendSSE(w, flusher, "", string(line))
		return false, nil
	})
	if err != nil {
		// The client is the only audience left, and it may already be gone;
		// best-effort report, then close the stream.
		sendSSE(w, flusher, "stream_error", errorFrame(err.Error()))
	}
}

// errorFrame renders a stream_error payload through the JSON encoder, so the
// escaping is exact — fmt's %q is Go string quoting, which diverges from JSON
// on invalid UTF-8.
func errorFrame(msg string) string {
	b, _ := json.Marshal(struct {
		Error string `json:"error"`
	}{msg})
	return string(b)
}

// sendSSE writes one Server-Sent Event frame. An empty name is the default
// `message` event (the normal per-line frame); named events are the two the
// UI listens for separately — `stream_warning` (non-terminal, e.g. a newer
// schema) and `stream_error` (terminal). data must be a single line; sendSSE
// does not split or escape, so the caller guarantees it — handleEvents skips
// any stream line carrying a raw \r or \n, and errorFrame's output comes
// from json.Marshal, which escapes control characters.
func sendSSE(w http.ResponseWriter, flusher http.Flusher, name, data string) {
	if name != "" {
		fmt.Fprintf(w, "event: %s\n", name)
	}
	fmt.Fprintf(w, "data: %s\n\n", data)
	flusher.Flush()
}

// writeJSON writes v as a JSON response body.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
	}
}
