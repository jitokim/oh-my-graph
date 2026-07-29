// Package runfeed owns events.jsonl: the append-only stream of node lifecycle
// events written to .oh-my-graph/runs/<run-id>/events.jsonl as a run executes,
// so an external consumer (fleetops) can tail live progress and reconstruct a
// run's history without parsing the human progress feed or polling state.json.
//
// Like runstate, this package is an on-disk *contract*, not a live domain
// object, and the same rules apply:
//
//   - Every event carries a Schema version. A consumer checks it per event and
//     refuses (or warns about) a version it does not understand, so a format
//     change is a visible incompatibility, never a silent misread.
//   - The field types are owned by this package and import nothing from the
//     runtime packages they mirror (ledger.Verdict et al.), so renaming a
//     runtime type can never change the persisted format without a deliberate
//     Schema bump. The verdict strings are chosen to match ledger/runstate.
//   - runfeed imports only the standard library: it is a serialization
//     boundary, and nothing about persistence leaks into the engine's tests.
//
// The full consumer-facing contract — both this stream and the state.json
// snapshot — is documented in docs/RUN-FEED.md.
package runfeed

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Schema is the current events.jsonl format version, stamped into every event.
// Handled exactly like runstate.Schema: bump it whenever a change to Event
// alters the on-disk bytes in a way an older reader could misinterpret. Purely
// additive fields (a new omitempty key an old reader ignores) do not require a
// bump; renaming, retyping, or changing the meaning of an existing field does.
const Schema = 1

// FileName is the event stream's file name inside a run directory, next to
// state.json — .oh-my-graph/runs/<run-id>/events.jsonl.
const FileName = "events.jsonl"

// EventType names one node/run lifecycle transition. The set is closed per
// schema version: adding a type is a compat-relevant change a consumer must be
// able to detect, so it comes with at least a documented note in
// docs/RUN-FEED.md (consumers must skip unknown types rather than fail).
type EventType string

const (
	// EventRunStarted opens one scheduler leg. A resumed run appends a second
	// run_started to the same stream, so a consumer sees legs, not just runs.
	EventRunStarted EventType = "run_started"
	// EventNodeStarted marks a node beginning execution (claude node or gate).
	EventNodeStarted EventType = "node_started"
	// EventNodePassed is a node's terminal PASS, carrying verdict, cost,
	// session id and retry count.
	EventNodePassed EventType = "node_passed"
	// EventNodeFailed is a node's terminal FAIL, same payload as a pass plus
	// the failure detail.
	EventNodeFailed EventType = "node_failed"
	// EventNodeRetried marks one retry attempt beginning after a failed one.
	EventNodeRetried EventType = "node_retried"
	// EventRunFinished closes the leg run_started opened, with its outcome.
	EventRunFinished EventType = "run_finished"
)

// Verdict strings for terminal node events; the values match ledger.Verdict
// and runstate.Verdict, redeclared here so the stream format is owned by this
// package alone.
const (
	VerdictPass = "PASS"
	VerdictFail = "FAIL"
)

// Outcome strings for run_finished: how the leg ended.
const (
	// OutcomePassed: every node the leg launched passed.
	OutcomePassed = "passed"
	// OutcomeFailed: the leg failed (halt, pruned failures, or a rejected gate).
	OutcomeFailed = "failed"
	// OutcomePaused: the leg stopped at a gate and is resumable.
	OutcomePaused = "paused"
)

// Event is one line of events.jsonl. Schema, Timestamp and RunID are stamped
// by StreamWriter.Emit, so the engine only fills the transition-specific
// fields. Fields that do not apply to an event type are omitted from its JSON
// (a consumer treats an absent cost/retries as zero — see docs/RUN-FEED.md for
// the per-type field table).
type Event struct {
	// Schema is the format version (the Schema constant at write time).
	Schema int `json:"schema"`
	// Timestamp is the emission time, RFC 3339 (nanosecond precision, UTC).
	Timestamp string `json:"ts"`
	// RunID is the run this stream belongs to — repeated per line so any single
	// event is self-identifying when filtered out of the file.
	RunID string `json:"run_id"`
	// Type is the lifecycle transition this event records.
	Type EventType `json:"event"`
	// NodeID is the node the event concerns; empty on run_* events.
	NodeID string `json:"node_id,omitempty"`
	// Verdict is PASS or FAIL on terminal node events (node_passed/node_failed).
	Verdict string `json:"verdict,omitempty"`
	// CostUSD is the node's reported spend on terminal node events (omitted
	// when zero — e.g. a gate, which spawns no subprocess).
	CostUSD float64 `json:"cost_usd,omitempty"`
	// SessionID is the claude session the node ran under, on terminal node
	// events; empty when no session exists (a gate, or a spawn failure).
	SessionID string `json:"session_id,omitempty"`
	// Retries is the 1-based retry ordinal on node_retried, and the number of
	// retries that preceded the terminal attempt on node_passed/node_failed
	// (omitted when zero).
	Retries int `json:"retries,omitempty"`
	// Detail is the short human note the ledger records for the same event —
	// the failure cause on node_failed, the retry/budget note on node_passed.
	Detail string `json:"detail,omitempty"`
	// Outcome is how the leg ended, on run_finished only.
	Outcome string `json:"outcome,omitempty"`
}

// StreamWriter appends events to one run's events.jsonl. It is the concrete,
// disk-backed implementation of schedule.EventSink (matched structurally —
// this package imports nothing from schedule, keeping the dependency
// one-directional, exactly as runstate.SnapshotRecorder does for
// schedule.Recorder). Safe for concurrent Emit calls: parallel nodes emit from
// separate goroutines, and the mutex keeps their lines whole.
type StreamWriter struct {
	path  string
	runID string

	mu   sync.Mutex
	file *os.File
}

// NewStreamWriter opens (creating if needed) the stream at path in append
// mode, stamping runID into every event it emits. Opening for append — never
// truncate — is what makes a resumed leg continue the same stream the first
// leg started, and what makes the file safe for a concurrent tail -f reader.
func NewStreamWriter(path, runID string) (*StreamWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create run dir %q: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open event stream %q: %w", path, err)
	}
	return &StreamWriter{path: path, runID: runID, file: file}, nil
}

// Emit stamps e (Schema, RunID, Timestamp) and appends it as one JSON line.
// The whole line lands in a single Write followed by an fsync, so a crash
// leaves at most one truncated final line and every fully-written line is
// durable — a tailing consumer must tolerate a partial last line, nothing
// else (docs/RUN-FEED.md).
func (w *StreamWriter) Emit(e Event) error {
	e.Schema = Schema
	e.RunID = w.runID
	e.Timestamp = time.Now().UTC().Format(time.RFC3339Nano)

	line, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("encode event: %w", err)
	}
	line = append(line, '\n')

	w.mu.Lock()
	defer w.mu.Unlock()
	if _, err := w.file.Write(line); err != nil {
		return fmt.Errorf("append event to %q: %w", w.path, err)
	}
	if err := w.file.Sync(); err != nil {
		return fmt.Errorf("sync event stream %q: %w", w.path, err)
	}
	return nil
}

// Close closes the underlying file. Emit after Close returns an error; the
// stream itself remains valid on disk for a later leg to reopen and append to.
func (w *StreamWriter) Close() error {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.file.Close()
}
