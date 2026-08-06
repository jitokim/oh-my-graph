// Package runfeed owns events.jsonl: the append-only stream of node lifecycle
// events written to ~/.oh-my-graph/runs/<run-id>/events.jsonl as a run executes,
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
// bump; renaming, retyping, or changing the meaning of an existing field does —
// and so does adding an event TYPE, because the type set is closed per schema
// version (see EventType) and a consumer must be able to detect that the
// stream now carries transitions its version did not define.
//
// History: 1 — the initial six node/run lifecycle types; 2 — added the gate
// decision events (gate_paused, gate_approved, gate_rejected).
const Schema = 2

// FileName is the event stream's file name inside a run directory, next to
// state.json — ~/.oh-my-graph/runs/<run-id>/events.jsonl.
const FileName = "events.jsonl"

// EventType names one node/run lifecycle transition. The set is closed per
// schema version: adding a type is a compat-relevant change a consumer must be
// able to detect, so it comes with a Schema bump and a documented note in
// docs/RUN-FEED.md (consumers must still skip unknown types rather than fail).
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
	// EventNodeRetried marks a failed attempt that is not final: one retry
	// attempt beginning after a failed one, or — since the additive `round`
	// field (ADR 0010) — a feedback declarer's non-final judgment failure
	// re-arming its loop body (detail "feedback round k/N: re-running
	// <target> → <declarer>", round k, no session id: the re-run's ids are
	// minted at each body node's own node_started). node_failed stays
	// terminal either way: it appears at most once per declarer per leg,
	// when the failure is final.
	EventNodeRetried EventType = "node_retried"
	// EventRunFinished closes the leg run_started opened, with its outcome.
	EventRunFinished EventType = "run_finished"
	// EventGatePaused marks a gate node deciding to pause the run: no new work
	// launches after it, in-flight siblings drain, and the leg closes with
	// outcome "paused". NodeID names the gate a resume must decide.
	EventGatePaused EventType = "gate_paused"
	// EventGateApproved marks a gate decision of approve being applied (a
	// resumed leg replaying --approve); the gate's terminal node_passed follows.
	EventGateApproved EventType = "gate_approved"
	// EventGateRejected marks a gate decision of reject being applied (a
	// resumed leg replaying --reject); the gate's terminal node_failed follows
	// and its subtree is pruned.
	EventGateRejected EventType = "gate_rejected"
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

// Provenance strings for node_passed: HOW a terminal PASS was reached, derived
// in trusted code from the predicates that actually ran (ADR 0016 §6).
//
// The set is closed and it is ordered by strength. It exists because a ledger
// that prints a self-report and a measurement as the same word cannot be read:
// in the run behind #119 a check node replied `PASS` in 17 seconds, was graded
// on having replied `PASS`, and certified a branch that did not compile — next
// to a $11.01 node whose row said `PASS` too. Nothing can force a planned node
// to build; this stops the record from claiming it did.
//
// It is a property of the PREDICATE, not of auto mode, so a hand-written
// graph's nodes carry it as well — a hand-written check node that self-reports
// has told its reader exactly as much as #119's did.
const (
	// ProvenanceVerified: a success_check.verify command ran and the engine
	// judged its exit code (and output_matches, when declared). Note that
	// `verified` is not `correct`: `--verify-cmd true` yields it. The feed
	// reports provenance, never adequacy.
	ProvenanceVerified = "verified"
	// ProvenanceSelfReported: the strongest predicate was result_matches — the
	// node passed by emitting the right words.
	ProvenanceSelfReported = "self-reported"
	// ProvenanceExitOnly: a subprocess ran and exited 0, with no predicate
	// beyond that.
	ProvenanceExitOnly = "exit-only"
	// ProvenanceApproved: a human approved a type: gate node — no subprocess
	// ran and no predicate was evaluated. Without it the set is not closed: an
	// approved gate records a PASS with no cost and no session, so under a
	// three-member set it would land on exit-only, which is wrong twice over —
	// no subprocess ran, and a person deciding is the STRONGEST provenance the
	// system has, not the weakest.
	ProvenanceApproved = "approved"
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
	// SessionID is the claude session the node runs under. On node_started and
	// node_retried it is the PRE-ASSIGNED id the engine hands claude via
	// --session-id, published early so a consumer can locate a RUNNING node's
	// transcript; on terminal node events it is the id the claude envelope
	// reported (the same id, envelope-sourced). Empty when no session exists or
	// none was pre-assigned: a gate, a spawn failure, or a session-handoff
	// node's start (its transcript is the parent session, already published on
	// the parent's terminal event).
	SessionID string `json:"session_id,omitempty"`
	// Retries is the 1-based retry ordinal on node_retried, and the number of
	// retries that preceded the terminal attempt on node_passed/node_failed
	// (omitted when zero).
	Retries int `json:"retries,omitempty"`
	// Detail is the short human note the ledger records for the same event —
	// the failure cause on node_failed, the retry/budget note on node_passed.
	// The producer caps it at one shared bound (240 runes), so a line stays
	// tailable even when the underlying error was arbitrarily long.
	Detail string `json:"detail,omitempty"`
	// Round is the feedback-round ordinal on node events emitted by a
	// feedback re-execution (ADR 0010): 1-based, absent on the initial pass
	// (absent means 0, like every other omitted zero value). Body nodes
	// therefore emit one full started→terminal sequence per round within a
	// leg; the latest terminal event per node stays authoritative, in-leg
	// rounds included (docs/RUN-FEED.md). An additive optional field — no
	// schema bump.
	Round int `json:"round,omitempty"`
	// Provenance is how a terminal PASS was reached, on node_passed only —
	// one of the Provenance* constants above. An additive optional field, no
	// schema bump: absent means a producer that predates ADR 0016 (or a
	// node_failed, which has no verdict to qualify). Verdict stays PASS/FAIL,
	// so no consumer's verdict test changes.
	Provenance string `json:"provenance,omitempty"`
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
//
// Owner-only (0o700 / 0o600), like the rest of the run directory: the stream
// carries node results and session ids, and "a consumer may tail this file"
// (docs/RUN-FEED.md) has always meant a consumer running as you — the same
// assumption the 127.0.0.1-only live view makes. An existing directory keeps
// whatever mode it had; MkdirAll does not chmod one.
func NewStreamWriter(path, runID string) (*StreamWriter, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("create run dir %q: %w", dir, err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
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
