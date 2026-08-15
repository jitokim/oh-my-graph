package serve

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// Canonical-UUID session ids, as the scheduler pre-assigns them.
const (
	sessionA = "6ba7b810-9dad-41d1-80b4-00c04fd430c8"
	sessionB = "7cb8c921-0ebe-42e2-91c5-11d15fe541d9"
)

// newTranscriptServer builds a test server whose transcript lookups hit a
// fixture projects directory — never the user's real ~/.claude/projects.
func newTranscriptServer(t *testing.T, dir string) (*Server, string) {
	t.Helper()
	projects := t.TempDir()
	s := newTestServer(dir, "run-1")
	s.projectsRoot = projects
	return s, projects
}

// writeTranscript drops a transcript fixture where the claude CLI would:
// <projectsRoot>/<some per-cwd dir>/<session-id>.jsonl.
func writeTranscript(t *testing.T, projectsRoot, sessionID string, lines ...string) {
	t.Helper()
	dir := filepath.Join(projectsRoot, "-some-munged-cwd")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create fixture project dir: %v", err)
	}
	content := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(filepath.Join(dir, sessionID+".jsonl"), []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture transcript: %v", err)
	}
}

func getTranscript(t *testing.T, s *Server, nodeID string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "http://127.0.0.1:8642/api/transcript?node="+nodeID, nil))
	return rec
}

func decodeTranscript(t *testing.T, rec *httptest.ResponseRecorder) transcriptPayload {
	t.Helper()
	var payload transcriptPayload
	if err := json.Unmarshal(rec.Body.Bytes(), &payload); err != nil {
		t.Fatalf("decode transcript payload: %v (body %q)", err, rec.Body.String())
	}
	return payload
}

// TestTranscript_RunningNodeServesTail is the happy path: a running node
// whose node_started published a session id gets its transcript's assistant
// text and tool-use names back — with user lines, unknown line shapes, and
// non-JSON damage all skipped, never fatal.
func TestTranscript_RunningNodeServesTail(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"user","message":{"role":"user","content":"the prompt"}}`,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"reading the file"}]}}`,
		`not json at all`,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Bash","input":{}},{"type":"text","text":""}]}}`,
		`{"type":"someday-a-new-type","whatever":true}`,
	)

	rec := getTranscript(t, s, "a")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	payload := decodeTranscript(t, rec)
	if payload.Node != "a" {
		t.Errorf("payload.Node = %q, want a", payload.Node)
	}
	want := []transcriptEntry{
		{Type: "text", Text: "reading the file"},
		{Type: "tool_use", Name: "Bash"},
	}
	if len(payload.Entries) != len(want) {
		t.Fatalf("entries = %+v, want %+v", payload.Entries, want)
	}
	for i := range want {
		if payload.Entries[i] != want[i] {
			t.Errorf("entry[%d] = %+v, want %+v", i, payload.Entries[i], want[i])
		}
	}
}

// TestTranscript_UnknownNodeIs404 pins the membership guard: an id the run's
// graph does not contain — a typo and a traversal probe alike — is a 404,
// before any transcript lookup.
func TestTranscript_UnknownNodeIs404(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
	)
	s, _ := newTranscriptServer(t, dir)

	for _, probe := range []string{"zzz", "..%2F..%2Fetc", ""} {
		if rec := getTranscript(t, s, probe); rec.Code != 404 {
			t.Errorf("node %q: status = %d, want 404", probe, rec.Code)
		}
	}
}

// TestTranscript_SettledNodeIs204 pins running-only: once the node's
// terminal event lands, the tail is gone (204), even though the transcript
// file still exists.
func TestTranscript_SettledNodeIs204(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, SessionID: sessionA},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"done"}]}}`)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("settled node: status = %d, want 204", rec.Code)
	}
	// A node that has not started at all is equally tail-less.
	if rec := getTranscript(t, s, "b"); rec.Code != 204 {
		t.Errorf("pending node: status = %d, want 204", rec.Code)
	}
}

// TestTranscript_ALaterLegClosesTheDeadLegsNode is the per-node instance of the
// bug the run-level derivation fixes: a leg died with `a` still running, so the
// stream carries a node_started for it with no terminal at all. A later leg —
// here a resume that ran only `b` — is a BOUNDARY: `a` is not running, and the
// dead leg's session id must not be served as "what it is doing right now",
// which is exactly what a reducer that only sees node events does, forever,
// across every later resume that does not re-run that node.
func TestTranscript_ALaterLegClosesTheDeadLegsNode(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
		// The leg dies here: no node terminal, no run_finished.
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "b", SessionID: sessionB},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"from the dead leg"}]}}`)
	writeTranscript(t, projects, sessionB,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"from the live leg"}]}}`)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("a node left open by a dead leg: status = %d, want 204 (body %q)", rec.Code, rec.Body.String())
	}
	// The leg that is actually running still serves its own tail: the boundary
	// clears state, it does not disable the endpoint.
	rec := getTranscript(t, s, "b")
	if rec.Code != 200 {
		t.Fatalf("the live leg's node: status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if payload := decodeTranscript(t, rec); len(payload.Entries) != 1 || payload.Entries[0].Text != "from the live leg" {
		t.Errorf("entries = %+v, want the live leg's own transcript", payload.Entries)
	}
}

// TestTranscript_PausedGateIs204 pins the third terminal. A pausing gate
// emits no node terminal at all, so gate_paused IS the last thing the stream
// says about that node in this leg: the reducer must settle it (running
// false, no session) exactly like node_passed/node_failed, or the paused
// node would read as running forever and keep serving a stale tail.
func TestTranscript_PausedGateIs204(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
		runfeed.Event{Type: runfeed.EventGatePaused, NodeID: "a"},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"waiting"}]}}`)

	state, err := readNodeFeedState(filepath.Join(dir, runfeed.FileName), "a")
	if err != nil {
		t.Fatalf("readNodeFeedState: %v", err)
	}
	if !state.seen {
		t.Error("seen = false, want true")
	}
	if state.running {
		t.Error("running = true, want false after gate_paused")
	}
	if state.session != "" {
		t.Errorf("session = %q, want empty after gate_paused", state.session)
	}

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("paused gate: status = %d, want 204", rec.Code)
	}
}

// TestTranscript_NoSessionIdIs204 pins the gate this endpoint sits behind: a
// running node whose node_started carried no session id (a session-handoff
// node — its transcript is the parent's session) has no tail to serve.
func TestTranscript_NoSessionIdIs204(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a"},
	)
	s, _ := newTranscriptServer(t, dir)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// TestTranscript_BeforeSnapshotFeedVouches pins the widening the handler
// documents: while state.json does not exist yet (the first node running IS
// the endpoint's main window), a node the run's own feed named in a
// node_started is served; an id the feed never named stays 404.
func TestTranscript_BeforeSnapshotFeedVouches(t *testing.T) {
	dir := t.TempDir()
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"assistant","message":{"content":[{"type":"tool_use","name":"Read"}]}}`)

	rec := getTranscript(t, s, "a")
	if rec.Code != 200 {
		t.Fatalf("feed-vouched node: status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if payload := decodeTranscript(t, rec); len(payload.Entries) != 1 || payload.Entries[0].Name != "Read" {
		t.Errorf("entries = %+v, want one tool_use Read", payload.Entries)
	}
	if rec := getTranscript(t, s, "zzz"); rec.Code != 404 {
		t.Errorf("unvouched node: status = %d, want 404", rec.Code)
	}
}

// TestTranscript_MissingTranscriptFileIs204: the session id is published on
// node_started before the claude subprocess has necessarily written anything,
// so a not-yet-existing transcript is "nothing to show yet", not an error.
func TestTranscript_MissingTranscriptFileIs204(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
	)
	s, _ := newTranscriptServer(t, dir)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}
}

// TestTranscript_RetryServesLatestSession pins the retry semantics the feed
// contract documents: node_retried publishes a NEW session id, and the tail
// follows it — the failed attempt's transcript is history, not "now doing".
func TestTranscript_RetryServesLatestSession(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
		runfeed.Event{Type: runfeed.EventNodeRetried, NodeID: "a", Retries: 1, SessionID: sessionB},
	)
	s, projects := newTranscriptServer(t, dir)
	writeTranscript(t, projects, sessionA,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"first attempt"}]}}`)
	writeTranscript(t, projects, sessionB,
		`{"type":"assistant","message":{"content":[{"type":"text","text":"second attempt"}]}}`)

	rec := getTranscript(t, s, "a")
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	payload := decodeTranscript(t, rec)
	if len(payload.Entries) != 1 || payload.Entries[0].Text != "second attempt" {
		t.Errorf("entries = %+v, want the retry attempt's text only", payload.Entries)
	}
}

// TestTranscript_TailIsCapped: only the last transcriptTailEntries entries
// come back — the endpoint is a "now doing" tail, not a transcript browser.
func TestTranscript_TailIsCapped(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: sessionA},
	)
	s, projects := newTranscriptServer(t, dir)
	var lines []string
	for i := 0; i < transcriptTailEntries+10; i++ {
		lines = append(lines, fmt.Sprintf(`{"type":"assistant","message":{"content":[{"type":"text","text":"step %d"}]}}`, i))
	}
	writeTranscript(t, projects, sessionA, lines...)

	payload := decodeTranscript(t, getTranscript(t, s, "a"))
	if len(payload.Entries) != transcriptTailEntries {
		t.Fatalf("len(entries) = %d, want %d", len(payload.Entries), transcriptTailEntries)
	}
	if got, want := payload.Entries[len(payload.Entries)-1].Text, fmt.Sprintf("step %d", transcriptTailEntries+9); got != want {
		t.Errorf("last entry = %q, want %q (the newest lines win)", got, want)
	}
}

// TestTranscript_UnsafeSessionIdIs204: the session id becomes a filename, so
// a feed line carrying anything but a canonical hex UUID — a doctored or
// corrupt stream smuggling path separators — is refused before any
// filesystem use. sessionIDSafe is also pinned directly.
func TestTranscript_UnsafeSessionIdIs204(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{RunID: "run-1", Graph: json.RawMessage(twoNodeGraph)})
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: "../../../../etc/passwd"},
	)
	s, _ := newTranscriptServer(t, dir)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("status = %d, want 204", rec.Code)
	}

	for id, want := range map[string]bool{
		sessionA:                                true,
		strings.ToUpper(sessionA):               true,
		"":                                      false,
		"../../../../etc/passwd":                false,
		"6ba7b810-9dad-41d1-80b4-00c04fd430c":   false, // one short
		"6ba7b810/9dad/41d1/80b4/00c04fd430c8":  false, // separators in hyphen slots
		"6ba7b810-9dad-41d1-80b4-00c04fd430cg":  false, // non-hex
		"6ba7b810-9dad-41d1-80b4-00c04fd430c8x": false, // one long
	} {
		if got := sessionIDSafe(id); got != want {
			t.Errorf("sessionIDSafe(%q) = %v, want %v", id, got, want)
		}
	}
}

// --- #178: a runtime that keeps no transcript says so -------------------------

// TestTranscriptTailNote_IsEmptyOnlyWhereATailCanExist pins the view's ONE
// runtime branch against the runtime vocabulary itself rather than a
// transcribed list: every runtime `runner` defines is asked, and exactly the
// claude ones (plus the empty snapshot value the run-feed contract reads as
// claude) may answer "a tail exists here". A third runtime added tomorrow
// lands in the default arm and gets the note — the safe direction, since the
// tail is claude's file.
func TestTranscriptTailNote_IsEmptyOnlyWhereATailCanExist(t *testing.T) {
	for _, value := range []string{"", string(runner.RuntimeClaude)} {
		if note := transcriptTailNote(value); note != "" {
			t.Errorf("transcriptTailNote(%q) = %q, want empty: claude is the runtime whose transcript "+
				"this view tails, and an absent runtime means claude (docs/RUN-FEED.md)", value, note)
		}
	}
	note := transcriptTailNote(string(runner.RuntimeCodex))
	if note == "" {
		t.Fatal("transcriptTailNote(codex) = \"\": a codex node publishes a thread, not a file under " +
			"~/.claude/projects, so the tail can never fill — and an empty tail is indistinguishable " +
			"from a node that has printed nothing yet, which is the whole of #178")
	}
	if !strings.Contains(note, string(runner.RuntimeCodex)) {
		t.Errorf("note = %q, want it to name the runtime it is about", note)
	}
}

// TestHandleGraph_CodexRunCarriesTheTranscriptNote: the page cannot derive the
// runtime — no event carries it (docs/RUN-FEED.md) — so the one read that can,
// /api/graph, hands it the sentence to render in the tail's place.
func TestHandleGraph_CodexRunCarriesTheTranscriptNote(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID:   "run-1",
		Runtime: string(runner.RuntimeCodex),
		Graph:   json.RawMessage(twoNodeGraph),
	})

	payload, _ := graphOf(t, dir, "run-1")
	if payload.TranscriptNote != transcriptTailNote(string(runner.RuntimeCodex)) {
		t.Errorf("transcript_note = %q, want the codex note — without it the live view polls a tail "+
			"that can never fill and tells the reader nothing", payload.TranscriptNote)
	}
}

// TestHandleGraph_ClaudeRunOmitsTheTranscriptNote: on the runtime whose
// transcripts this view really does tail, the payload is byte-identical to
// before — the KEY is absent, not an empty string the page might render as a
// blank line. The absent-runtime snapshot takes the same arm, since absent
// means claude.
func TestHandleGraph_ClaudeRunOmitsTheTranscriptNote(t *testing.T) {
	for _, runtime := range []string{"", string(runner.RuntimeClaude)} {
		dir := t.TempDir()
		writeSnapshot(t, dir, runstate.Snapshot{
			RunID:   "run-1",
			Runtime: runtime,
			Graph:   json.RawMessage(twoNodeGraph),
		})

		payload, body := graphOf(t, dir, "run-1")
		if payload.TranscriptNote != "" {
			t.Errorf("runtime %q: transcript_note = %q, want empty — this run's tail works", runtime, payload.TranscriptNote)
		}
		if strings.Contains(body, "transcript_note") {
			t.Errorf("runtime %q: payload carries a transcript_note key: %s", runtime, body)
		}
	}
}

// TestTranscript_CodexRunningNodeIs204AndTheNoteIsTheCue pins the split this
// fix rests on, in one run: /api/transcript keeps answering 204 for a codex
// node — the honest "nothing to show" it already gave, and the behaviour
// docs/RUN-FEED.md publishes — while /api/graph carries the cue that stops the
// page asking and tells the reader why. The endpoint gains no second runtime
// branch.
func TestTranscript_CodexRunningNodeIs204AndTheNoteIsTheCue(t *testing.T) {
	dir := t.TempDir()
	writeSnapshot(t, dir, runstate.Snapshot{
		RunID:   "run-1",
		Runtime: string(runner.RuntimeCodex),
		Graph:   json.RawMessage(twoNodeGraph),
	})
	// A codex thread id is not a UUID naming a file under ~/.claude/projects;
	// this is what the run's own feed publishes for a running codex node.
	writeEvents(t, dir, "run-1",
		runfeed.Event{Type: runfeed.EventRunStarted},
		runfeed.Event{Type: runfeed.EventNodeStarted, NodeID: "a", SessionID: "01998f2c-thread-not-a-file"},
	)
	s, _ := newTranscriptServer(t, dir)

	if rec := getTranscript(t, s, "a"); rec.Code != 204 {
		t.Errorf("running codex node: status = %d, want 204 (body %q)", rec.Code, rec.Body.String())
	}
	if payload, _ := graphOf(t, dir, "run-1"); payload.TranscriptNote == "" {
		t.Error("the 204 above is silent by itself: /api/graph must carry the note that explains it")
	}
}

// TestTranscriptNote_IsRenderedByTheServedPage holds the wiring no compiler
// checks: the field name the server emits, the page that reads it, and the CSS
// rule that styles it are three files with no build step between them. A rename
// on any one side leaves a codex run silent again — the exact defect — with a
// green Go test run.
func TestTranscriptNote_IsRenderedByTheServedPage(t *testing.T) {
	app := readAsset(t, "app.js")
	style := readAsset(t, "style.css")

	if !strings.Contains(app, "transcript_note") {
		t.Error("ui/app.js never reads payload.transcript_note: the server computes the sentence and " +
			"nothing renders it, so a codex run's live view is silent again (#178)")
	}
	// The page must also stop polling on that answer — the note is only half
	// the fix; the other half is retiring a request every 3s that can only 204.
	if !strings.Contains(app, "if (transcriptNote) return;") {
		t.Error("ui/app.js's pollLiveTail no longer short-circuits on transcriptNote: the tail is " +
			"polled every few seconds per running node, and on such a run every poll can only 204")
	}
	if !strings.Contains(style, ".live-tail.tail-note") {
		t.Error("ui/style.css has no `.live-tail.tail-note` rule: the note renders as monospace tail " +
			"content, reading as words the node itself printed")
	}
}
