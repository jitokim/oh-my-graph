// transcript.go is /api/transcript: the live "now doing" tail of a RUNNING
// node. The scheduler publishes a node's pre-assigned session id early, on
// node_started/node_retried (docs/RUN-FEED.md), precisely so a consumer can
// locate the session's transcript while the node still runs — this endpoint
// is that consumer, in-repo: it finds `<session-id>.jsonl` under the user's
// claude projects directory and serves the last few entries' human-relevant
// content (assistant text and tool-use names).

package serve

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
)

// transcriptTailEntries caps how many parsed entries a response carries: the
// tail is "what is it doing right now", not a transcript browser.
const transcriptTailEntries = 30

// transcriptTailBytes is how far back into the transcript file the tail
// reads. Transcripts grow to megabytes (tool results are inlined); reading
// only the last chunk keeps every poll cheap, and 256 KiB comfortably holds
// the last 30 human-relevant entries even between huge tool-result lines.
const transcriptTailBytes = 256 * 1024

// transcriptTextCap bounds one text entry, in runes: the tail shows what the
// node is doing, so the head of a long assistant message carries the signal.
const transcriptTextCap = 400

// defaultProjectsRoot is where the claude CLI keeps session transcripts:
// ~/.claude/projects/<per-cwd dir>/<session-id>.jsonl. Empty when the home
// directory is unknown, in which case no transcript can be located and
// /api/transcript honestly serves 204 for everything.
func defaultProjectsRoot() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude", "projects")
}

// nodeFeedState is what the run's event stream currently says about one
// node: whether any event named it at all, whether its latest attempt is
// still running (a node_started/node_retried with no node_passed, node_failed
// or gate_paused after it), and the pre-assigned session id that attempt
// published ("" when none was — a gate, or a session-handoff node running in
// its parent's session).
type nodeFeedState struct {
	seen    bool
	running bool
	session string
}

// readNodeFeedState reduces events.jsonl to one node's current state. Like
// every reader of the stream it skips a line that does not decode (the
// contract's tolerated truncated-final-line damage). A schema newer than
// this binary is NOT refused here — serve takes `watch`'s keep-rendering
// posture throughout (see handleEvents) — accepting that an unknown future
// event type could leave a node reading as running; the worst that yields is
// a tail served for a settled node, still one of the run's own sessions.
//
// run_started is a LEG BOUNDARY and is read before the node filter, because it
// carries no node id: a node whose leg died mid-run has a node_started with no
// terminal after it, and without this it would read as running — carrying its
// dead leg's session id — through every later resume that did not happen to
// re-run it, serving a stale transcript as "what it is doing right now". The
// boundary clears running and the session; the next leg's own
// node_started/node_retried is what makes either true again.
func readNodeFeedState(feedPath, nodeID string) (nodeFeedState, error) {
	file, err := os.Open(feedPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nodeFeedState{}, nil
		}
		return nodeFeedState{}, fmt.Errorf("open event stream %q: %w", feedPath, err)
	}
	defer file.Close()

	var state nodeFeedState
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		var event runfeed.Event
		if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
			continue
		}
		if event.Type == runfeed.EventRunStarted {
			// seen survives: the feed has still named this node, which is what
			// vouches for it while no snapshot exists (handleTranscript).
			state.running = false
			state.session = ""
			continue
		}
		if event.NodeID != nodeID {
			continue
		}
		switch event.Type {
		case runfeed.EventNodeStarted, runfeed.EventNodeRetried:
			state = nodeFeedState{seen: true, running: true, session: event.SessionID}
		case runfeed.EventNodePassed, runfeed.EventNodeFailed, runfeed.EventGatePaused:
			// gate_paused belongs here for the same reason the two node
			// terminals do: it is the last thing the stream says about that
			// node in this leg (a pausing gate emits no node terminal at all),
			// so leaving it out would read a paused gate as running forever.
			// The published rule in docs/RUN-FEED.md names all three.
			state = nodeFeedState{seen: true, running: false}
		default:
			state.seen = true
		}
	}
	if err := scanner.Err(); err != nil {
		return nodeFeedState{}, fmt.Errorf("read event stream %q: %w", feedPath, err)
	}
	return state, nil
}

// sessionIDSafe reports whether id has the exact shape of the session ids
// the engine pre-assigns (a canonical 8-4-4-4-12 hex UUID). The id comes
// from the run's own feed, not the URL — but it is about to become a
// filename, so a doctored or corrupt feed line must not be able to smuggle
// a path (separators, dots) into the lookup. Hex-and-hyphen-only makes that
// structurally impossible.
func sessionIDSafe(id string) bool {
	if len(id) != 36 {
		return false
	}
	for i, c := range id {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
				return false
			}
		}
	}
	return true
}

// findTranscript locates <sessionID>.jsonl one level under projectsRoot and
// returns its path, or "" when it is not there (yet).
//
// Deliberately a search by session-id FILENAME, not a reconstruction of the
// claude CLI's cwd-to-directory-name mangling: that mangling is undocumented
// and version-dependent, and this process cannot always know a node's
// realized cwd anyway (an empty `cwd:` means the ENGINE process's working
// directory, which a standalone `serve` does not share; an interpolated or
// worktree cwd is resolved at spawn time). The session id is globally unique
// and engine-generated, so the filename alone identifies the transcript.
func findTranscript(projectsRoot, sessionID string) string {
	if projectsRoot == "" {
		return ""
	}
	entries, err := os.ReadDir(projectsRoot)
	if err != nil {
		return ""
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(projectsRoot, entry.Name(), sessionID+".jsonl")
		if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
			return path
		}
	}
	return ""
}

// transcriptEntry is one human-relevant item of the tail: an assistant text
// ("text", with Text) or a tool invocation ("tool_use", with Name).
type transcriptEntry struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
	Name string `json:"name,omitempty"`
}

// transcriptPayload is /api/transcript's 200 response body.
type transcriptPayload struct {
	Node    string            `json:"node"`
	Entries []transcriptEntry `json:"entries"`
}

// readTranscriptTail reads the last transcriptTailBytes of the transcript at
// path and parses out the trailing human-relevant entries.
func readTranscriptTail(path string) ([]transcriptEntry, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	offset := info.Size() - transcriptTailBytes
	if offset < 0 {
		offset = 0
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	data, err := io.ReadAll(io.LimitReader(file, transcriptTailBytes+1))
	if err != nil {
		return nil, err
	}
	if offset > 0 {
		// The seek almost certainly landed mid-line; drop the partial line.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			data = data[i+1:]
		} else {
			data = nil
		}
	}
	return parseTranscriptTail(data), nil
}

// parseTranscriptTail extracts the human-relevant content of transcript
// JSONL: each line is decoded just far enough to pull assistant text and
// tool-use names out; a line that is not JSON, not an assistant message, or
// shaped in any way this minimal reading does not expect is skipped, never
// an error — the transcript format belongs to the claude CLI and this is a
// best-effort peek, not a parser of record. Only the last
// transcriptTailEntries entries are kept, and each text is capped at
// transcriptTextCap runes.
func parseTranscriptTail(data []byte) []transcriptEntry {
	entries := []transcriptEntry{}
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record struct {
			Type    string `json:"type"`
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		}
		if err := json.Unmarshal(line, &record); err != nil || record.Type != "assistant" {
			continue
		}
		var content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
			Name string `json:"name"`
		}
		if err := json.Unmarshal(record.Message.Content, &content); err != nil {
			continue
		}
		for _, item := range content {
			switch item.Type {
			case "text":
				if text := capRunes(item.Text, transcriptTextCap); text != "" {
					entries = append(entries, transcriptEntry{Type: "text", Text: text})
				}
			case "tool_use":
				if item.Name != "" {
					entries = append(entries, transcriptEntry{Type: "tool_use", Name: item.Name})
				}
			}
		}
	}
	if len(entries) > transcriptTailEntries {
		entries = entries[len(entries)-transcriptTailEntries:]
	}
	return entries
}

// capRunes truncates s to at most n runes, marking the cut with an ellipsis.
func capRunes(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

// handleTranscript serves the live tail for one RUNNING node (?node=<id>):
// 200 with a JSON entry list, 204 when there is nothing to show (the node is
// not running, published no session id, or its transcript is not on disk
// yet), 404 for a node this run cannot vouch for.
//
// BOUNDARY: this endpoint deliberately widens serve's read surface beyond
// the run directory, into the user's own claude projects dir — but only for
// the run's OWN sessions. The one file it reads is named by the session id
// the run's own event stream published (an engine-generated UUID, shape-
// checked by sessionIDSafe before any filesystem use); nothing URL-supplied
// is ever joined into a path.
//
// The node id from the URL is matched against the run's node set exactly
// like /api/result — with one widening /api/result does not need: while no
// snapshot exists (state.json lands only at the first terminal verdict, and
// the first node RUNNING is this endpoint's whole purpose), a node the run's
// own feed has named in a node_started is vouched for by the feed instead.
// An id neither source knows is a 404 either way.
func (s *Server) handleTranscript(w http.ResponseWriter, r *http.Request) {
	nodeID := r.URL.Query().Get("node")

	g, err := s.loadRunGraph()
	snapshotMissing := errors.Is(err, fs.ErrNotExist)
	if err != nil && !snapshotMissing {
		http.Error(w, fmt.Sprintf("load run graph: %v", err), http.StatusInternalServerError)
		return
	}
	if !snapshotMissing {
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
	}

	state, err := readNodeFeedState(filepath.Join(s.runDir, runfeed.FileName), nodeID)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if snapshotMissing && !state.seen {
		http.NotFound(w, r)
		return
	}
	if !state.running || !sessionIDSafe(state.session) {
		w.WriteHeader(http.StatusNoContent)
		return
	}

	path := findTranscript(s.projectsRoot, state.session)
	if path == "" {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	entries, err := readTranscriptTail(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.Error(w, fmt.Sprintf("read transcript: %v", err), http.StatusInternalServerError)
		return
	}
	writeJSON(w, transcriptPayload{Node: nodeID, Entries: entries})
}
