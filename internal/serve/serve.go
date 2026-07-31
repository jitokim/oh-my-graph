// Package serve owns `oh-my-graph serve`: a read-only web live view of ONE
// run — the DAG rendered in the browser with nodes colored live as they run
// (Airflow's Graph View, for this tool's runs). It is strictly a consumer of
// the run-feed contract (docs/RUN-FEED.md): it reads state.json for the
// run's structure and tails events.jsonl for its progress, and never writes,
// rewrites, or deletes anything in a run directory. Fleet-wide observation
// stays fleetops's job; this is one run, live, locally.
//
// SECURITY: the listener binds to 127.0.0.1 ONLY (see Listen), and every
// request's Host header must name loopback (see requireLoopbackHost) so a
// hostile page cannot reach /api/* by DNS-rebinding a domain it controls onto
// 127.0.0.1. Run directories contain node prompts, artifacts and session ids
// — and the sessions they name hold full transcripts — so the server must
// never be reachable from off-host. There is no auth in v1 precisely because
// the loopback bind is the access control; widening the bind address would
// need an auth story first.
//
// The server spawns no processes. In particular it does not shell out to
// `open`/`xdg-open` to launch a browser: exactly three objects in this repo
// may spawn a process (internal/invariants, ADR 0002/0005), and auto-opening
// the browser would be a fourth exec seam requiring its own ADR. The CLI
// prints the URL instead; auto-open is a deliberate follow-up, not an
// oversight.
package serve

import (
	"bytes"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"time"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
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

// Server serves one run's live view out of its run directory. It holds no
// state beyond the paths: every request re-reads the contract files, so the
// view is always as fresh as the disk and the server survives the run's
// files appearing after it started (a fresh run's state.json window).
type Server struct {
	runDir string
	runID  string
	poll   time.Duration
}

// New builds a Server for one run directory. runID is the directory's name —
// echoed to the UI so the page can identify the run even before any file
// exists to read it from.
func New(runDir, runID string) *Server {
	return &Server{runDir: runDir, runID: runID, poll: defaultPoll}
}

// Handler returns the server's routes:
//
//	/            the embedded static UI (index.html, app.js, style.css, vendored libraries)
//	/api/graph   the run's DAG structure as JSON (node ids + depends_on edges)
//	/api/events  the run's event stream as SSE: replay events.jsonl, then follow
//	/api/result  one node's handoff artifact as text/plain (?node=<id>)
//
// Everything is read-only GETs over the run directory; there is no mutating
// route to guard. Every route sits behind requireLoopbackHost, the
// DNS-rebinding guard.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// Unreachable: "ui" is embedded at compile time.
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/graph", s.handleGraph)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.HandleFunc("GET /api/result", s.handleResult)
	mux.Handle("GET /", http.FileServerFS(static))
	return requireLoopbackHost(mux)
}

// graphPayload is /api/graph's response body. Available is false during the
// window where the run exists but its snapshot does not yet: state.json is
// written only after each node's terminal verdict (docs/RUN-FEED.md), so a
// fresh run's structure is honestly "not known yet" until its first node
// completes — the UI polls until Available flips true rather than the server
// inventing a structure from anywhere else.
type graphPayload struct {
	RunID     string      `json:"run_id"`
	Available bool        `json:"available"`
	Name      string      `json:"name,omitempty"`
	Nodes     []graphNode `json:"nodes,omitempty"`
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
	snap, err := runstate.Load(filepath.Join(s.runDir, "state.json"))
	if err != nil {
		return nil, err
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return nil, fmt.Errorf("reconstruct graph: %w", err)
	}
	return g, nil
}

// handleGraph serves the run's DAG structure. A snapshot that exists but
// cannot be read (corrupt, or a schema this binary does not understand —
// runstate.Load's loud refusal) is a 500 carrying the reason, not a silent
// empty graph.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	g, err := s.loadRunGraph()
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, graphPayload{RunID: s.runID, Available: false})
			return
		}
		http.Error(w, fmt.Sprintf("load run graph: %v", err), http.StatusInternalServerError)
		return
	}

	payload := graphPayload{RunID: s.runID, Available: true, Name: g.Name}
	for _, node := range g.Nodes {
		payload.Nodes = append(payload.Nodes, graphNode{ID: node.ID, Type: node.Type, DependsOn: node.DependsOn})
	}
	writeJSON(w, payload)
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
