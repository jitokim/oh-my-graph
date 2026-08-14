package runstate

import "sync"

// SnapshotRecorder is the concrete, disk-backed implementation of
// schedule.Recorder (matched structurally — this package imports nothing from
// schedule, keeping the dependency one-directional: schedule -> runstate,
// never back). It owns the mutable half of one run's Snapshot (Nodes and
// Gate) on top of a caller-supplied, otherwise-static base (graph, inputs,
// flags, tool policies), and writes the whole snapshot atomically to path
// after every call, so the file on disk after any RecordNode/RecordGateDecision/
// RecordPause is always a complete, self-consistent Snapshot rather than a
// partial update (DESIGN.md, "Snapshot writes happen after every node").
//
// The Scheduler never sees this type — it depends only on the narrower
// schedule.Recorder interface — so a scheduler test can inject a fake with no
// import of this package, exactly as it does for gate.GateController and
// verify.Verifier.
type SnapshotRecorder struct {
	path string

	mu   sync.Mutex
	snap Snapshot
}

// NewSnapshotRecorder builds a SnapshotRecorder that writes to path, seeded
// with base — the parts of the snapshot fixed for the whole run (RunID,
// GraphSourcePath, GraphSHA256, Graph, Inputs, ContinueOnFail, ToolPolicies).
// base.Nodes and base.Gate carry forward whatever a resume's caller already
// knows from an earlier leg (empty for a fresh run); NewSnapshotRecorder
// copies base.Nodes so the caller's own map is never mutated out from under
// it.
func NewSnapshotRecorder(path string, base Snapshot) *SnapshotRecorder {
	nodes := make(map[string]NodeRecord, len(base.Nodes))
	for id, rec := range base.Nodes {
		nodes[id] = rec
	}
	base.Nodes = nodes
	return &SnapshotRecorder{path: path, snap: base}
}

// WriteInitial persists the snapshot exactly as seeded, before any node has
// run. A fresh `run`/`auto` calls it once, up front: every later write only
// happens at a node's terminal verdict, so without this a run whose FIRST
// node pauses on a session limit (ADR 0009) would leave no state.json at all
// — and the very `resume <run-id> --retry-failed` its exit hint recommends
// would find nothing to load. An initial snapshot with zero Nodes is the
// honest state: nothing has settled yet.
func (r *SnapshotRecorder) WriteInitial() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Write(r.path, r.snap)
}

// RecordNode records one node's terminal result (or a feedback declarer's
// non-terminal marker) and writes the snapshot. It is called after EVERY
// node — pass or fail, gate or claude-run — not only at a gate pause, so a
// Ctrl-C'd or crashed run is resumable too.
//
// When the incoming record supersedes an earlier feedback round of the same
// node — its Round is higher, or it replaces a marker of the same round —
// the superseded record's CostUSD is folded into it before it is stored
// (ADR 0010): a node's recorded spend accumulates across its rounds, so
// state.json's per-node figures sum to the same total the ledger's
// one-row-per-execution table shows. "Cost stays honest" is a property of
// the contract fleetops reads, not just the local table. An ordinary
// overwrite (both records round 0 — e.g. a --retry-failed leg re-running a
// cleared node, whose cleared record is already gone) accumulates nothing,
// exactly as before.
func (r *SnapshotRecorder) RecordNode(nodeID string, rec NodeRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.snap.Nodes == nil {
		r.snap.Nodes = make(map[string]NodeRecord)
	}
	if prior, ok := r.snap.Nodes[nodeID]; ok && supersedesRound(prior, rec) {
		rec.CostUSD += prior.CostUSD
		rec.CostUnknown = rec.CostUnknown || prior.CostUnknown
		rec.Usage.add(prior.Usage)
	}
	r.snap.Nodes[nodeID] = rec
	return Write(r.path, r.snap)
}

// supersedesRound reports whether rec replaces prior as a later (or
// completing) feedback round whose spend must carry forward: a strictly
// higher round, or a terminal record landing on the marker of its own round
// (the marker priced the declarer's failing execution; the terminal record
// prices the re-run — the node paid for both).
func supersedesRound(prior, rec NodeRecord) bool {
	if rec.Round > prior.Round {
		return true
	}
	return rec.Round == prior.Round && prior.Verdict == "" && rec.Verdict != ""
}

// RecordGateDecision records an approve/reject decision (or a pause — see
// RecordPause, which also sets this) against a gate node, independent of
// RecordNode: a reject's NodeRecord is VerdictFail, from which "rejected"
// (as opposed to any other node failure) could not be recovered later, so the
// decision itself needs its own durable slot (GateState.Decisions).
func (r *SnapshotRecorder) RecordGateDecision(gateNodeID string, decision GateDecision) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.setDecisionLocked(gateNodeID, decision)
	return Write(r.path, r.snap)
}

// RecordPause records that the run has stopped launching new work at
// gateNodeID and persists the snapshot. It is called once per Run(), after
// in-flight siblings have drained — not from inside the gate's own
// evaluation, which runs concurrently with them. A failure here must be
// treated as fatal by the caller: a pause whose state was not persisted is an
// unrecoverable stop (DESIGN.md, "a snapshot write failure at a gate pause is
// fatal").
func (r *SnapshotRecorder) RecordPause(gateNodeID string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.snap.Gate.PausedAt = gateNodeID
	r.setDecisionLocked(gateNodeID, GatePause)
	return Write(r.path, r.snap)
}

// setDecisionLocked writes decision into Gate.Decisions. Caller must hold mu.
func (r *SnapshotRecorder) setDecisionLocked(gateNodeID string, decision GateDecision) {
	if r.snap.Gate.Decisions == nil {
		r.snap.Gate.Decisions = make(map[string]GateDecision)
	}
	r.snap.Gate.Decisions[gateNodeID] = decision
}
