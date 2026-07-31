// Package serve owns `oh-my-graph serve`: a read-only web live view of ONE
// run — the DAG rendered in the browser with nodes colored live as they run
// (Airflow's Graph View, for this tool's runs). It is strictly a consumer of
// the run-feed contract (docs/RUN-FEED.md): it reads state.json for the
// run's structure and tails events.jsonl for its progress, and never writes,
// rewrites, or deletes anything in a run directory. Fleet-wide observation
// stays fleetops's job; this is one run, live, locally.
//
// SECURITY: the listener binds to 127.0.0.1 ONLY (see Listen). Run
// directories contain node prompts, artifacts and session ids — and the
// sessions they name hold full transcripts — so the server must never be
// reachable from off-host. There is no auth in v1 precisely because the
// loopback bind is the access control; widening the bind address would need
// an auth story first.
//
// The server spawns no processes. In particular it does not shell out to
// `open`/`xdg-open` to launch a browser: exactly three objects in this repo
// may spawn a process (internal/invariants, ADR 0002/0005), and auto-opening
// the browser would be a fourth exec seam requiring its own ADR. The CLI
// prints the URL instead; auto-open is a deliberate follow-up, not an
// oversight.
package serve

import (
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
// pinned, vendored cytoscape.js (see ui/vendor/README.md for its version and
// license) — so the served page has zero runtime network dependencies: no
// CDN fetch, nothing leaves the host to render a run.
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
		return nil, fmt.Errorf("listen on %s: %w", addr, err)
	}
	return listener, nil
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
//	/            the embedded static UI (index.html, app.js, style.css, vendored cytoscape.js)
//	/api/graph   the run's DAG structure as JSON (node ids + depends_on edges)
//	/api/events  the run's event stream as SSE: replay events.jsonl, then follow
//
// Everything is read-only GETs over the run directory; there is no mutating
// route to guard.
func (s *Server) Handler() http.Handler {
	static, err := fs.Sub(uiFS, "ui")
	if err != nil {
		// Unreachable: "ui" is embedded at compile time.
		panic(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/graph", s.handleGraph)
	mux.HandleFunc("GET /api/events", s.handleEvents)
	mux.Handle("GET /", http.FileServerFS(static))
	return mux
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

// handleGraph serves the run's DAG structure, reconstructed from the
// snapshot's own Graph bytes via the same graph.Parse path `resume` and
// `runs list` trust — never by re-reading the source YAML, which may have
// been edited since. A snapshot that exists but cannot be read (corrupt, or
// a schema this binary does not understand — runstate.Load's loud refusal)
// is a 500 carrying the reason, not a silent empty graph.
func (s *Server) handleGraph(w http.ResponseWriter, r *http.Request) {
	snap, err := runstate.Load(filepath.Join(s.runDir, "state.json"))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSON(w, graphPayload{RunID: s.runID, Available: false})
			return
		}
		http.Error(w, fmt.Sprintf("load run snapshot: %v", err), http.StatusInternalServerError)
		return
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		http.Error(w, fmt.Sprintf("reconstruct graph: %v", err), http.StatusInternalServerError)
		return
	}

	payload := graphPayload{RunID: s.runID, Available: true, Name: g.Name}
	for _, node := range g.Nodes {
		payload.Nodes = append(payload.Nodes, graphNode{ID: node.ID, Type: node.Type, DependsOn: node.DependsOn})
	}
	writeJSON(w, payload)
}

// handleEvents streams the run's events.jsonl as Server-Sent Events: every
// line already on disk is replayed immediately, then appended lines follow
// as they land, via the same runfeed.Follow tail `watch` uses. Each event is
// forwarded as one SSE `data:` frame carrying the stream's own JSON line
// verbatim — the browser reads the run-feed contract, not a re-encoding of
// it.
//
// Per RUN-FEED.md's compatibility rule this consumer checks `schema` per
// event and refuses one newer than it understands — exactly like `runs
// list` — by sending a terminal `stream_error` frame and closing, rather
// than forwarding bytes it might be misrepresenting. A line that does not
// decode at all is skipped (the contract's tolerated truncated-final-line
// damage). The stream ends when the client disconnects; it deliberately does
// NOT end at run_finished, because a resumed leg appends to the same file
// and the viewer should see it.
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

	// A fresh run's directory can briefly exist before its stream does (and a
	// pre-runfeed directory has no stream at all): keep the connection open
	// and wait for the file rather than 404ing a healthy run, exactly as the
	// tail itself waits for appended lines.
	for {
		if _, err := os.Stat(feedPath); err == nil {
			break
		} else if !errors.Is(err, fs.ErrNotExist) {
			sendSSE(w, flusher, "stream_error", errorFrame(err.Error()))
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(s.poll):
		}
	}

	err := runfeed.Follow(r.Context(), feedPath, s.poll, func(line []byte) (bool, error) {
		var event runfeed.Event
		if err := json.Unmarshal(line, &event); err != nil {
			return false, nil
		}
		if event.Schema > runfeed.Schema {
			sendSSE(w, flusher, "stream_error", errorFrame(fmt.Sprintf(
				"event stream schema %d is newer than this binary understands (max %d)",
				event.Schema, runfeed.Schema)))
			return true, nil
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
// `message` event (the normal per-line frame); a named event (`stream_error`)
// is the terminal refusal the UI listens for separately. data must be a
// single line, which every frame here is: JSON encoding never contains a raw
// newline.
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
