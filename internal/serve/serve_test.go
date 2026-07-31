package serve

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// testPoll keeps the SSE tail's end-of-stream sleep short so follow tests
// finish in milliseconds rather than at the production interval.
const testPoll = 5 * time.Millisecond

const twoNodeGraph = `{"name":"demo","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b","depends_on":["a"]}]}`

// newTestServer builds a Server over dir with the short test poll.
func newTestServer(dir, runID string) *Server {
	s := New(dir, runID)
	s.poll = testPoll
	return s
}

// writeSnapshot persists a snapshot through the real runstate.Write, so the
// fixture's bytes are exactly what a real run leaves on disk — never
// hand-built JSON that could drift from the snapshot contract.
func writeSnapshot(t *testing.T, dir string, snap runstate.Snapshot) {
	t.Helper()
	if err := runstate.Write(filepath.Join(dir, "state.json"), snap); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
}

// writeEvents persists events through the real runfeed.StreamWriter, for the
// same reason: the fixture stream is byte-for-byte what a real run emits.
func writeEvents(t *testing.T, dir, runID string, events ...runfeed.Event) {
	t.Helper()
	w, err := runfeed.NewStreamWriter(filepath.Join(dir, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open fixture event stream: %v", err)
	}
	defer w.Close()
	for _, e := range events {
		if err := w.Emit(e); err != nil {
			t.Fatalf("emit fixture event %q: %v", e.Type, err)
		}
	}
}

// --- security: the listener is loopback-only ---------------------------------

func TestListen_BindsLoopbackOnly(t *testing.T) {
	listener, err := Listen(0)
	if err != nil {
		t.Fatalf("Listen returned error: %v", err)
	}
	defer listener.Close()

	addr, ok := listener.Addr().(*net.TCPAddr)
	if !ok {
		t.Fatalf("listener address is %T, want *net.TCPAddr", listener.Addr())
	}
	// The run directory holds prompts and session ids, so the bound address —
	// not just intent — must be 127.0.0.1: reachable from this host only.
	if got := addr.IP.String(); got != "127.0.0.1" {
		t.Errorf("listener bound to %q, want 127.0.0.1 only", got)
	}
}

// --- /api/graph --------------------------------------------------------------

func TestHandleGraph_ServesDAGFromSnapshot(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})

	rec := httptest.NewRecorder()
	newTestServer(dir, "run-1").Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}

	var payload graphPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if !payload.Available || payload.RunID != "run-1" || payload.Name != "demo" {
		t.Errorf("payload header = %+v, want available run-1/demo", payload)
	}
	if len(payload.Nodes) != 2 {
		t.Fatalf("nodes = %+v, want a and b", payload.Nodes)
	}
	if payload.Nodes[0].ID != "a" || payload.Nodes[1].ID != "b" {
		t.Errorf("node ids = %+v, want [a b]", payload.Nodes)
	}
	if got := payload.Nodes[1].DependsOn; len(got) != 1 || got[0] != "a" {
		t.Errorf("b's depends_on = %v, want [a] — the edge the UI draws", got)
	}
}

func TestHandleGraph_NoSnapshotYetIsHonestlyUnavailable(t *testing.T) {
	// A fresh run's window: the directory exists (the event stream is opened
	// at run start) but state.json is written only after the first node's
	// terminal verdict — the structure is not known yet, and the endpoint
	// must say so rather than 404 or invent one.
	dir := t.TempDir()

	rec := httptest.NewRecorder()
	newTestServer(dir, "run-fresh").Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/graph", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var payload graphPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if payload.Available || payload.RunID != "run-fresh" || len(payload.Nodes) != 0 {
		t.Errorf("payload = %+v, want an unavailable run-fresh with no nodes", payload)
	}
}

func TestHandleGraph_RefusesIncompatibleSnapshot(t *testing.T) {
	// A snapshot schema this binary does not understand must be refused
	// loudly (runstate.Load's rule), never rendered as an empty or partial
	// graph. Hand-built on purpose: only a hand-built file can carry a schema
	// today's runstate.Write cannot stamp.
	dir := t.TempDir()
	raw := []byte(`{"schema":99,"run_id":"run-new","graph":{}}`)
	if err := os.WriteFile(filepath.Join(dir, "state.json"), raw, 0o644); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}

	rec := httptest.NewRecorder()
	newTestServer(dir, "run-new").Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/api/graph", nil))
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 (body %q)", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "schema") {
		t.Errorf("the refusal must name the schema problem, got %q", rec.Body.String())
	}
}

// --- /api/events -------------------------------------------------------------

// sseStream is a live client of /api/events: one goroutine reads the
// response body for the connection's whole life (so no line is ever consumed
// by an abandoned reader), and readFrame assembles frames from its lines.
type sseStream struct {
	lines chan string
	errs  chan error
}

// sseClient opens /api/events against a real HTTP server (streaming needs a
// real connection, not a recorder) and returns the live stream plus a cancel
// that tears the request down.
func sseClient(t *testing.T, s *Server) (*sseStream, context.CancelFunc) {
	t.Helper()
	httpServer := httptest.NewServer(s.Handler())
	t.Cleanup(httpServer.Close)

	ctx, cancel := context.WithCancel(context.Background())
	req, err := http.NewRequestWithContext(ctx, "GET", httpServer.URL+"/api/events", nil)
	if err != nil {
		cancel()
		t.Fatalf("build SSE request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		cancel()
		t.Fatalf("open SSE stream: %v", err)
	}
	t.Cleanup(func() { resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		cancel()
		t.Fatalf("SSE status = %d, want 200", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		cancel()
		t.Fatalf("Content-Type = %q, want text/event-stream", got)
	}

	stream := &sseStream{lines: make(chan string), errs: make(chan error, 1)}
	go func() {
		reader := bufio.NewReader(resp.Body)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				stream.errs <- err
				return
			}
			stream.lines <- line
		}
	}()
	return stream, cancel
}

// readFrame reads one SSE frame (up to its blank-line terminator) and returns
// its event name ("" for the default message event) and data line.
func (s *sseStream) readFrame(t *testing.T) (name, data string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for an SSE frame (have name %q data %q)", name, data)
		case err := <-s.errs:
			t.Fatalf("read SSE frame: %v (have name %q data %q)", err, name, data)
		case line := <-s.lines:
			line = strings.TrimRight(line, "\n")
			switch {
			case strings.HasPrefix(line, "event: "):
				name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				data = strings.TrimPrefix(line, "data: ")
			case line == "" && data != "":
				return name, data
			}
		}
	}
}

// expectEOF asserts the server has closed the stream: the reader goroutine's
// next read fails rather than delivering another line.
func (s *sseStream) expectEOF(t *testing.T) {
	t.Helper()
	select {
	case <-time.After(5 * time.Second):
		t.Errorf("timed out waiting for the stream to close")
	case line := <-s.lines:
		t.Errorf("stream delivered %q after it should have closed", line)
	case err := <-s.errs:
		if err != io.EOF {
			t.Errorf("stream closed with %v, want EOF", err)
		}
	}
}

func TestHandleEvents_ReplaysThenFollowsAppendedEvents(t *testing.T) {
	dir := t.TempDir()
	const runID = "run-live"
	writeEvents(t, dir, runID,
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
	)

	stream, cancel := sseClient(t, newTestServer(dir, runID))
	defer cancel()

	// Replay: both already-written events arrive, in order, as the stream's
	// own JSON lines.
	for _, want := range []runfeed.EventType{runfeed.EventRunStarted, runfeed.EventNodeStarted} {
		name, data := stream.readFrame(t)
		var event runfeed.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("frame data is not one stream event: %v (%q)", err, data)
		}
		if name != "" || event.Type != want {
			t.Fatalf("frame = (%q, %s), want a plain message carrying %s", name, event.Type, want)
		}
	}

	// Follow: an event appended after the client connected streams out too.
	writeEvents(t, dir, runID,
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUSD: 0.25, SessionID: "s-a"},
	)
	name, data := stream.readFrame(t)
	var event runfeed.Event
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatalf("appended frame data is not one stream event: %v (%q)", err, data)
	}
	if name != "" || event.Type != runfeed.EventNodePassed || event.CostUSD != 0.25 || event.SessionID != "s-a" {
		t.Errorf("appended frame = (%q, %+v), want the node_passed event verbatim", name, event)
	}
}

func TestHandleEvents_WaitsForAStreamThatDoesNotExistYet(t *testing.T) {
	// A fresh run's directory can exist before events.jsonl does: the SSE
	// endpoint must hold the connection and deliver the stream when it
	// appears, not 404 a healthy run.
	dir := t.TempDir()
	const runID = "run-fresh"

	stream, cancel := sseClient(t, newTestServer(dir, runID))
	defer cancel()

	writeEvents(t, dir, runID, runfeed.Event{Type: runfeed.EventRunStarted})
	name, data := stream.readFrame(t)
	var event runfeed.Event
	if err := json.Unmarshal([]byte(data), &event); err != nil {
		t.Fatalf("frame data is not one stream event: %v (%q)", err, data)
	}
	if name != "" || event.Type != runfeed.EventRunStarted {
		t.Errorf("frame = (%q, %s), want the run_started event", name, event.Type)
	}
}

func TestHandleEvents_WarnsOnceOnASchemaNewerThanThisBinary(t *testing.T) {
	// Hand-built on purpose: only hand-built lines can carry a schema
	// today's StreamWriter cannot stamp. Per RUN-FEED.md a schema bump must
	// be visible but NOT fatal: the handler warns once with a non-terminal
	// stream_warning frame and keeps forwarding — the same posture `watch`
	// takes — so a live view survives a routine bump instead of going blank.
	dir := t.TempDir()
	raw := `{"schema":99,"ts":"2026-08-01T00:00:00Z","run_id":"run-new","event":"run_started"}` + "\n" +
		`{"schema":99,"ts":"2026-08-01T00:00:01Z","run_id":"run-new","event":"node_started","node_id":"a"}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, runfeed.FileName), []byte(raw), 0o644); err != nil {
		t.Fatalf("write raw fixture event stream: %v", err)
	}

	stream, cancel := sseClient(t, newTestServer(dir, "run-new"))
	defer cancel()

	name, data := stream.readFrame(t)
	if name != "stream_warning" {
		t.Fatalf("frame = (%q, %q), want a stream_warning first", name, data)
	}
	if !strings.Contains(data, "schema 99") {
		t.Errorf("the warning must name the offending schema, got %q", data)
	}
	// The warning is non-terminal: both events still arrive, and the warning
	// is not repeated for the second one.
	for _, wantEvent := range []runfeed.EventType{runfeed.EventRunStarted, runfeed.EventNodeStarted} {
		name, data = stream.readFrame(t)
		var event runfeed.Event
		if err := json.Unmarshal([]byte(data), &event); err != nil {
			t.Fatalf("frame data %q is not an event line: %v", data, err)
		}
		if name != "" || event.Type != wantEvent {
			t.Errorf("frame = (%q, %s), want the forwarded %s event", name, event.Type, wantEvent)
		}
	}
}

// --- the embedded UI ---------------------------------------------------------

func TestHandler_ServesEmbeddedUIWithVendoredCytoscape(t *testing.T) {
	handler := newTestServer(t.TempDir(), "run-1").Handler()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", rec.Code)
	}
	page := rec.Body.String()
	// The page must reference only embedded, relative assets — a CDN URL here
	// would be a runtime network dependency the design forbids.
	for _, want := range []string{
		"vendor/cytoscape.min.js", "vendor/dagre.min.js", "vendor/cytoscape-dagre.js",
		"app.js", "style.css",
	} {
		if !strings.Contains(page, want) {
			t.Errorf("index.html must reference embedded asset %q:\n%s", want, page)
		}
	}
	if strings.Contains(page, "http://") || strings.Contains(page, "https://") {
		t.Errorf("index.html must not reference any remote URL:\n%s", page)
	}

	// Every vendored library must be served byte-for-byte identical to the
	// published build it was pinned from — size is not provenance for code
	// compiled into every binary of a no-CDN tool. The expected hashes are
	// recorded (with their fetch URLs) in ui/vendor/README.md; an upgrade
	// updates both together.
	for path, wantSHA256 := range map[string]string{
		"/vendor/cytoscape.min.js":   "9c2a3bf2592e0b14a1f7bec07c03a54f16dedf32af9cd0af155c716aa6c87bc3",
		"/vendor/dagre.min.js":       "62eb9787ccfdbdf4148d4d99d31dbf9ee4770eafee81e637d759b52aac22cd51",
		"/vendor/cytoscape-dagre.js": "bf70fe402991dcbff33e05a7e4a5271c78020bb75e85d1c80ab7538e4157112e",
	} {
		rec = httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s status = %d, want 200", path, rec.Code)
		}
		sum := sha256.Sum256(rec.Body.Bytes())
		if got := hex.EncodeToString(sum[:]); got != wantSHA256 {
			t.Errorf("vendored %s sha256 = %s, want %s (see ui/vendor/README.md)", path, got, wantSHA256)
		}
	}
}

// --- run resolution ----------------------------------------------------------

// settledEvents is a closed leg; openEvents is a leg still in flight.
var (
	settledEvents = []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	}
	openEvents = []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodeStarted, NodeID: "a"},
	}
)

func TestResolveRun(t *testing.T) {
	cases := []struct {
		name     string
		streams  map[string][]runfeed.Event // run id -> its events (nil = a dir with no stream)
		explicit string
		want     string
		wantErr  string
	}{
		{
			name:     "explicit id wins even over an in-flight run",
			streams:  map[string][]runfeed.Event{"20250101-000000": nil, "20250102-000000": openEvents},
			explicit: "20250101-000000",
			want:     "20250101-000000",
		},
		{
			name:     "explicit id that does not exist is an unknown-run error",
			streams:  map[string][]runfeed.Event{"20250101-000000": nil},
			explicit: "20250199-000000",
			wantErr:  "unknown run",
		},
		{
			name: "an in-flight run is preferred over a newer settled one",
			streams: map[string][]runfeed.Event{
				"20250101-000000": openEvents,
				"20250102-000000": settledEvents,
			},
			want: "20250101-000000",
		},
		{
			name: "the newest of several in-flight runs wins",
			streams: map[string][]runfeed.Event{
				"20250101-000000": openEvents,
				"20250102-000000": openEvents,
			},
			want: "20250102-000000",
		},
		{
			name: "no in-flight run falls back to the newest directory",
			streams: map[string][]runfeed.Event{
				"20250101-000000": settledEvents,
				"20250102-000000": nil, // pre-runfeed dir: no stream at all
			},
			want: "20250102-000000",
		},
		{
			name:    "no runs at all is a clear error",
			streams: map[string][]runfeed.Event{},
			wantErr: "no runs found",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			for runID, events := range tc.streams {
				dir := filepath.Join(root, runID)
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create fixture run dir: %v", err)
				}
				if len(events) > 0 {
					writeEvents(t, dir, runID, events...)
				}
			}

			got, err := ResolveRun(root, tc.explicit)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveRun returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("ResolveRun = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestResolveRun_MissingRootIsANoRunsError(t *testing.T) {
	_, err := ResolveRun(filepath.Join(t.TempDir(), "never-created"), "")
	if err == nil || !strings.Contains(err.Error(), "no runs found") {
		t.Fatalf("err = %v, want the no-runs error", err)
	}
}
