// Package runstate owns state.json: the resumable snapshot of a run, written
// atomically to ~/.oh-my-graph/runs/<run-id>/state.json after every node so that a
// gate pause, a Ctrl-C, or a crash can all be continued by a later
// `oh-my-graph resume` (see DESIGN.md, "Gate nodes and resume", and ADR 0003).
//
// The snapshot is a versioned on-disk *contract*, not a live domain object, and
// that shapes every type here:
//
//   - It carries a Schema version. An incompatible snapshot is refused loudly by
//     Load rather than misread, so a format change is a visible failure, never a
//     silent corruption.
//   - Its field types are owned by this package, not imported from the runtime
//     packages they mirror (ledger.Verdict, gate.Decision and
//     runner.ToolPolicy). A persistence format must not change meaning because a
//     runtime type was renamed or re-shaped without anyone bumping Schema; owning
//     the types keeps Schema the single gate on the format. The small enums
//     (Verdict, GateDecision) and the tool-policy struct are therefore declared
//     locally, with string values chosen to match their runtime counterparts so
//     the resume path's conversion is trivial and obvious.
//   - The one exception is the graph itself, held opaquely as json.RawMessage.
//     The Node schema is large and still growing (verify, budget, tool ceiling),
//     and re-declaring it here would be a maintenance trap: add a field to
//     graph.Node, forget runstate, lose it on resume. Storing the normalized
//     graph as re-parseable JSON means new Node fields flow through untouched and
//     the resume path reconstructs a *graph.Graph via graph.Parse(snap.Graph) —
//     reusing the real parser, validator, and by-id index rather than a shadow
//     copy of them. runstate never interprets those bytes.
//
// runstate imports only the standard library on purpose: it is the serialization
// boundary of the engine and depends on none of the packages whose data it
// persists, so nothing about persistence leaks into their tests.
package runstate

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// Schema is the current state.json format version. Bump it whenever a change to
// the types below alters the on-disk bytes in a way an older reader could
// misinterpret; Load refuses any snapshot whose Schema does not equal this.
//
// Schema 2 added NodeRecord.BudgetUSD and NodeRecord.Detail —
// the gate/resume wiring PR that NodeRecord's original doc comment deferred
// them to. `resume` reconstructs a full ledger.Record per completed node so
// the resumed leg's end-of-run table and TOTAL COST are honest about the
// whole run, not just the leg that produced them; ledger.Record needs a
// budget (to compute BudgetDeltaUSD) and a detail string (the failing
// predicate, the retry count, or the budget headroom) that schema 1 had no
// field for. A schema-1 snapshot is refused by Load rather than silently
// read with these two fields zeroed, because a resumed leg's ledger would
// then understate a carried-forward node's budget without saying so.
//
// Schema 3 adds Snapshot.Runtime plus NodeRecord.CostUnknown and Usage. An old
// reader would otherwise resume a Codex run with Claude and render an
// unreported USD cost as a known $0, so this is not safely additive.
const Schema = 3

// Verdict is a node's terminal judgement as persisted in the snapshot. The
// string values match ledger.Verdict so the resume path can carry a record
// forward into a fresh ledger without a lookup table; it is redeclared here
// rather than imported so the on-disk format is owned by this package alone.
type Verdict string

const (
	// VerdictPass marks a node that completed and passed its success check. Only
	// passing nodes count as "completed" for resume topology: their outputs exist
	// on disk, so their dependents may proceed (see Snapshot.CompletedNodes).
	VerdictPass Verdict = "PASS"
	// VerdictFail marks a node that ran but failed a check, its budget, or the
	// runner. It is persisted for honest cost/verdict carry-forward, but a failed
	// node does not unblock its dependents, so it is not a completed node.
	VerdictFail Verdict = "FAIL"
)

// A record may also carry NO verdict (the empty string): the non-terminal
// FEEDBACK MARKER a feedback declarer's record is rewritten to when its arc
// fires (ADR 0010) — round k, no verdict, spend carried. A marker is
// deliberately neither completed nor settled (both set derivations below
// test for the two real verdicts), which is exactly what makes a leg stopped
// mid-loop resume INTO the loop: the declarer relaunches, and its body's
// superseded rounds are recomputed from the marker's round by the resume
// path. A runstate FAIL written mid-loop instead would make the declarer
// settled the moment anything stopped the run, silently collapsing the loop
// into an ordinary failure on resume.

// GateDecision is a gate's recorded outcome. The values mirror the
// gate.Decision constants (approve / reject / pause) and are redeclared here for
// the same reason as Verdict: the snapshot is the source of truth for what a
// resumed run replays, and must not depend on a runtime type that is free to
// change shape without a Schema bump.
type GateDecision string

const (
	// GateApprove: the gate was approved, so its dependents may run on resume.
	GateApprove GateDecision = "approve"
	// GateReject: the gate was rejected, so its subtree is pruned on resume.
	GateReject GateDecision = "reject"
	// GatePause: the gate paused the run and is awaiting a human decision. This is
	// the value a fresh run records for the gate it stops at.
	GatePause GateDecision = "pause"
)

// NodeToolPolicy is the per-node execution ceiling for an auto-planned run,
// persisted so a resumed leg re-imposes the same isolation the first leg did.
// Resuming a planned graph without it would silently drop the Layer-1/2 guard
// that keeps an unreviewed plan inside its bounds (see DESIGN.md, "The tool
// ceiling"). The fields mirror runner.ToolPolicy one-for-one; the type is local
// because the snapshot format is a persisted contract and should not be defined
// by a runtime type that is free to change. That independence is deliberate,
// but it is also how the two drift: a sixth ceiling layer added to
// runner.ToolPolicy and not here would leave a resumed planned node running one
// layer weaker than the leg that started it, silently. TestNodeToolPolicy-
// MirrorsRunnerToolPolicy compares the two shapes by reflection and fails when
// they diverge, so the copy stays a decision rather than an oversight.
// A hand-written `run` records no tool policies at all (its nodes run under the
// user's own reviewed settings), so this appears only in Snapshot.ToolPolicies
// for auto runs.
type NodeToolPolicy struct {
	// AllowedTools renders as --allowedTools: the scoped grant under default-deny.
	AllowedTools []string `json:"allowed_tools,omitempty"`
	// DisallowedTools renders as --disallowedTools: the residual subtractive deny.
	DisallowedTools []string `json:"disallowed_tools,omitempty"`
	// Tools renders as --tools, narrowing the built-in tool set. A nil slice means
	// the flag was omitted.
	Tools []string `json:"tools,omitempty"`
	// SettingSources renders as --setting-sources. A nil pointer means the flag was
	// omitted; a pointer to "" means "load none of the user's settings files",
	// which is the load-bearing Layer-1 isolation. A pointer (not a bare string) so
	// "omitted" and "explicitly empty" stay distinguishable across a round-trip.
	SettingSources *string `json:"setting_sources,omitempty"`
	// StrictMCPConfig renders as --strict-mcp-config, bounding MCP servers.
	StrictMCPConfig bool `json:"strict_mcp_config,omitempty"`
	// PluginDirs renders as one --plugin-dir per entry: the staged skill
	// plugin a planned node activates skills from (ADR 0017). It is persisted
	// because it is the ONLY durable record that a run was activation-enabled
	// — the grant is deliberately invisible in graph.json (ADR 0017 §2) — but
	// it is NEVER rehydrated verbatim the way the five ceiling fields are. It
	// names a directory, and a --plugin-dir pointing at nothing is accepted
	// silently by the CLI, so a resumed leg trusting this path would run with
	// no skills and be indistinguishable from one whose model chose none.
	// So since 2026-08-07 `resume` re-stages nothing and verifies nothing: it
	// drops this field and `Skill` from every rehydrated policy and prints why
	// (ADR 0017 §6), reading the field only to know activation was on. An old
	// snapshot without the field rehydrates as an isolated run, which is the
	// correct default.
	PluginDirs []string `json:"plugin_dirs,omitempty"`
}

// NodeRecord is one completed node's entry in the snapshot: exactly the fields
// a resume needs that are not derivable from the graph, plus (as of schema 2)
// what `resume` needs to reconstruct a faithful ledger.Record for a node
// carried forward from an earlier leg. Its map key in Snapshot.Nodes is the
// node id, so the id is not repeated inside the record.
type NodeRecord struct {
	// Verdict is the node's terminal judgement. Only VerdictPass nodes are treated
	// as completed for resume topology.
	Verdict Verdict `json:"verdict"`
	// SessionID is the model CLI's session id. This is the one datum
	// resume cannot recompute: without it a handoff: session child cannot --resume
	// its parent on the second leg, because the id lived only in Handoff.sessions
	// in memory on the first leg. Handoff.Seed reads it back out of here.
	SessionID string `json:"session_id"`
	// CostUSD is the node's reported spend, carried forward so the resumed leg's
	// total does not understate what the run has already cost across processes.
	CostUSD float64 `json:"cost_usd"`
	// CostUnknown distinguishes an unreported USD amount from a free call
	// (schema 3), while Usage preserves the runtime's token accounting.
	CostUnknown bool       `json:"cost_unknown,omitempty"`
	Usage       TokenUsage `json:"usage,omitzero"`
	// BudgetUSD is the node's declared budget_usd, or 0 when it declared none
	// (schema 2). Mirrors ledger.Record.BudgetUSD so `resume` can rebuild a
	// Record that reports the same budget-vs-actual delta the original leg's
	// ledger did, instead of a carried-forward row silently losing it.
	BudgetUSD float64 `json:"budget_usd,omitempty"`
	// Duration is the node's wall-clock run time. It serializes as an integer
	// nanosecond count (time.Duration's underlying type), so it round-trips
	// losslessly even though it is not human-readable in the file.
	Duration time.Duration `json:"duration"`
	// ArtifactPath is the on-disk path of the node's persisted .result — the same
	// file {{ artifacts.<id> }} targets. The .out files are not copied into the
	// snapshot; the snapshot only remembers where they are, and Handoff.Seed
	// rehydrates the path so a resumed dependent still resolves it.
	ArtifactPath string `json:"artifact_path"`
	// Detail is the node's ledger.Record.Detail carried forward verbatim
	// (schema 2): the failing predicate, the retry count, or the budget
	// headroom note. Without it a resumed leg's end-of-run table would show a
	// blank DETAIL column for every node from an earlier leg.
	Detail string `json:"detail,omitempty"`
	// Round is the feedback-round ordinal this record was written during
	// (ADR 0010): 1-based, absent (0) on any execution outside a feedback
	// loop — an additive field, no schema bump. On a feedback declarer's
	// MARKER record (no verdict) it is the round the arc has re-armed; on a
	// body node's terminal record it is the round that execution belonged
	// to. The recorded position IS the loop's resume state: body records
	// with Round below the declarer's marker round are superseded, records
	// at it are retained, and max − round rounds remain — no separate
	// rounds-spent counter exists to drift from it.
	Round int `json:"round,omitempty"`
	// Judged marks a FAIL that a check rendered a verdict ON, as opposed to
	// one the machinery caused: a failed success_check or a verification that
	// ran and said no, never a spawn error, an interpolation error, a blown
	// budget, or a verification that could not be completed. It is ADR 0010's
	// judgment-vs-infrastructure split (schedule.isJudgmentFailure) made
	// durable, and absent (false) on every PASS and on every marker record —
	// an additive field, no schema bump.
	//
	// It exists because that split is the gate on quoting a failed node's own
	// reply back into the prompt that retries it (ADR 0020, "a retry carries
	// the attempt it is repeating"), and a `resume --retry-failed` decides
	// that in a different PROCESS from the one that judged it. The alternative
	// was re-deriving the cause by parsing Detail's prose, which would make a
	// wording change silently move a trust boundary. Consumers get the same
	// thing for free: "the work was wrong" and "the machinery broke" stop
	// being distinguishable only by reading English.
	Judged bool `json:"judged,omitempty"`
	// Provenance is HOW this node's PASS was reached — one of runfeed's four
	// qualifiers (ADR 0016 §6, "build evidence is a user-supplied engine
	// command") — carried forward so a resumed leg's end-of-run
	// table qualifies an earlier leg's rows the same way it qualifies its own.
	// Without it a resume would print a table whose earlier rows read a bare
	// `PASS` beside this leg's `PASS (self-reported)`, and a reader would have
	// to know that the blank means "written by an earlier leg" rather than
	// "nothing was measured".
	//
	// Additive and optional, exactly like Round: absent on every FAIL (which
	// has no verdict to qualify) and on any snapshot written before this
	// field existed, so today's snapshots stay readable and there is NO
	// schema bump.
	//
	// It is the PASS-side counterpart of Judged, and the two are disjoint by
	// construction: a record qualifies its PASS or it classifies its FAIL,
	// never both. A node whose engine-run evidence command said no is a FAIL
	// with Judged true and no Provenance — "verified" would claim a
	// verification the node never survived — and it earns its `verified`
	// qualifier only on the attempt that finally passes.
	Provenance string `json:"provenance,omitempty"`
}

// TokenUsage is provider-reported token accounting persisted with a node.
type TokenUsage struct {
	InputTokens           int64 `json:"input_tokens,omitempty"`
	CachedInputTokens     int64 `json:"cached_input_tokens,omitempty"`
	OutputTokens          int64 `json:"output_tokens,omitempty"`
	ReasoningOutputTokens int64 `json:"reasoning_output_tokens,omitempty"`
}

func (u *TokenUsage) add(other TokenUsage) {
	u.InputTokens += other.InputTokens
	u.CachedInputTokens += other.CachedInputTokens
	u.OutputTokens += other.OutputTokens
	u.ReasoningOutputTokens += other.ReasoningOutputTokens
}

// GoalRef links an iterated auto run — one cycle of a goal loop (ADR 0011) —
// to its goal group. It is an additive optional block under RUN-FEED's own
// rule: absent entirely on single-cycle runs, so today's snapshots stay
// byte-identical and there is NO schema bump. FirstRunID is the stable group
// key (equal to the run's own id on cycle 1); the rest of the chain is
// derivable from it plus Cycle, which is why no previous_run_id is stored —
// a derivable field is a field that can contradict its derivation.
type GoalRef struct {
	// Text is the goal in the user's own words, identical across the group.
	Text string `json:"text"`
	// Cycle is this run's 1-based ordinal within the goal loop.
	Cycle int `json:"cycle"`
	// MaxCycles is the --max-cycles bound the loop was launched with.
	MaxCycles int `json:"max_cycles"`
	// FirstRunID is cycle 1's run id — the goal group's stable key.
	FirstRunID string `json:"first_run_id"`
}

// BuildEvidence is how one auto-mode launch answered the build-evidence
// question (ADR 0030 §2.5a). An absence that was chosen and an absence that was
// an accident look identical in a finished run without it — and, equally, a run
// that was never ASKED is distinguishable from both, which is what makes the
// firing rate a measurement rather than a count of one stratum.
//
// It is an additive optional block and the schema stays 3: an absent field is a
// run that predates this, or a `run` of a hand-written graph, and no reader of
// either version can misread it.
//
// What it is NOT is an input to anything. Nothing reads it to decide behaviour,
// on this leg or on a resumed one, and it carries no command — Signals are
// marker FILENAMES, not the suggested commands the detection table holds beside
// them — so there is nothing in it a later leg could execute. The run directory
// remains an inadmissible source of engine-run shell, on both legs (ADR 0016 §4).
type BuildEvidence struct {
	// Answer is one of four values, and the set is closed:
	//   "attached"      — --verify-cmd was supplied; the engine runs it at each
	//                     sink. Signals may be empty or not; the attachment
	//                     itself is in the graph.
	//   "declared"      — signals were detected and a human typed
	//                     --accept-no-build-evidence, answering this question
	//                     and no other.
	//   "disclosed"     — signals were detected and a chat [y/N] approved a plan
	//                     screen that stated the absence. ONE keystroke covered
	//                     two questions; this is weaker than "declared" and is
	//                     filed apart from it, never summed with it.
	//   "none-detected" — the directory raised no signal, so no gate applied and
	//                     nothing was declared. Every greenfield run lands here.
	Answer string `json:"answer"`
	// DeclaredBy is the exact spelling of what the human did, for the two
	// answers a human gives: "--accept-no-build-evidence" or "chat-confirm".
	// Empty for "attached" and "none-detected". It records that SOMETHING typed
	// the flag, not that a human did — this repository ships `auto` to an agent,
	// and nothing in argv distinguishes the two — so the declared stratum is an
	// upper bound on human declarations (ADR 0030 §9).
	DeclaredBy string `json:"declared_by,omitempty"`
	// Signals are the marker files detected at launch, in the detection table's
	// order — what the human was told when they answered. Empty is meaningful
	// and is the whole point of writing this block on every launch: "how many
	// directories raised a signal" is the rows whose list is non-empty, counted
	// across all four strata, including the attached ones.
	Signals []string `json:"signals,omitempty"`
}

// GateState records the run's progress through its gates: what has been decided
// and where, if anywhere, the run is currently parked.
type GateState struct {
	// PausedAt is the id of the gate the run is paused at, or "" when the snapshot
	// was written at a point that is not a gate pause (after an ordinary node, or
	// on a completed run). resume reports this gate when a bare `resume` gives it
	// no --approve/--reject to apply.
	PausedAt string `json:"paused_at,omitempty"`
	// Decisions is every gate decision made so far, keyed by gate node id. The
	// resume path's RecordedController replays these; a gate absent from the map is
	// still undecided. nil when no gate has been decided yet.
	Decisions map[string]GateDecision `json:"decisions,omitempty"`
}

// RuntimeClaude is the default runtime, and the value an absent `runtime` has
// always meant (docs/RUN-FEED.md, "state.json — the snapshot"). It is declared
// here rather than imported from runner for the same reason Verdict and
// GateDecision are: the on-disk vocabulary belongs to the persistence format,
// so Schema stays the single gate on what the bytes mean. Its string value
// matches runner.RuntimeClaude.
const (
	RuntimeClaude = "claude"
	// RuntimeCodex is declared beside it so the on-disk vocabulary this package
	// owns is complete. Only RuntimeClaude is load-bearing — it is what an empty
	// value canonicalizes to — but half a vocabulary is worse than either whole
	// option: it invites the pair to be written as one constant and one string
	// literal, which is how a value set stops being a value set.
	RuntimeCodex = "codex"
)

// Snapshot is the whole resumable state of a run — everything a second
// `oh-my-graph` process needs to continue where the first left off, and nothing
// that can be recomputed from it. In particular it does NOT hold in-degree counts
// or the ready set: both are derived from graph × completed nodes and are
// recomputed on resume via graph.ReadyGiven, so persisting them would create a
// second source of truth that could go stale (DESIGN.md, "What the snapshot must
// hold"; ADR 0003, "deliberately does not hold").
type Snapshot struct {
	// Schema is the format version. Write stamps it to the current Schema constant,
	// so a caller need not set it; Load refuses a snapshot whose value differs.
	Schema int `json:"schema"`
	// RunID is the run this snapshot belongs to (the <run-id> in its path). Held in
	// the file too so a snapshot is self-identifying if copied out of its directory.
	RunID string `json:"run_id"`
	// Runtime is the run-wide model CLI (ADR 0025): RuntimeClaude or
	// RuntimeCodex.
	// It is ALWAYS present in the file. An empty in-memory value is
	// canonicalized to RuntimeClaude by Snapshot.MarshalJSON — the type's own
	// serialization boundary, so no writer can produce a snapshot without it
	// (see that method, and the tag: deliberately NOT omitempty).
	//
	// Reading is unchanged and stays where it was: an absent `runtime` on a
	// snapshot written before this — a schema-3 file from v0.8.0 — decodes to
	// the empty string, which every reader already treats as claude. Load does
	// not rewrite it, so no file on disk changed meaning; what changed is only
	// that no NEW file can leave the question open. That is also why Schema
	// stays 3: the field went from "absent, meaning claude" to "present,
	// saying claude", which no reader of either version can misread.
	Runtime string `json:"runtime"`
	// Planning accounting is the coordinator call that produced an auto run's
	// graph. It is top-level because it belongs to the run, not to any node.
	PlanningCostUSD     float64    `json:"planning_cost_usd,omitempty"`
	PlanningCostUnknown bool       `json:"planning_cost_unknown,omitempty"`
	PlanningUsage       TokenUsage `json:"planning_usage,omitzero"`

	// GraphSourcePath is where the graph was originally loaded from — the .yaml for
	// a hand-written `run`, or the generated graph.json for an auto run. It is
	// informational: resume reconstructs the graph from Graph below, NOT by
	// re-reading this path, because the artifacts already on disk were produced by
	// the graph as it was, not as the file may since have been edited.
	GraphSourcePath string `json:"graph_source_path,omitempty"`
	// GraphSHA256 is the hex SHA-256 of the original source bytes. It exists so a
	// resume can warn out loud when GraphSourcePath has changed on disk since the
	// snapshot was taken, rather than silently ignoring the edit (ADR 0003).
	GraphSHA256 string `json:"graph_sha256,omitempty"`
	// Graph is the normalized DAG as re-parseable JSON (a YAML subset). runstate
	// stores it opaquely and never interprets it; the resume path reconstructs a
	// *graph.Graph with graph.Parse(snap.Graph). Held as bytes rather than a typed
	// field so that adding a field to graph.Node never forces a change here.
	Graph json.RawMessage `json:"graph"`

	// Inputs is the run's --input bindings. Persisted because resume rejects
	// --input on the command line: changing an input mid-run would make the prompts
	// that produced the already-persisted artifacts inconsistent with the run.
	Inputs map[string]string `json:"inputs,omitempty"`
	// ContinueOnFail is the --continue-on-fail flag as the run was launched with it.
	// It changes what a node failure means (prune a subtree vs. halt), so a resume
	// must honor the same choice the first leg ran under, not a fresh default.
	ContinueOnFail bool `json:"continue_on_fail,omitempty"`
	// ToolPolicies is the per-node execution ceiling for an auto run, keyed by node
	// id. nil for a hand-written `run`, whose nodes run under the user's own
	// settings. Resuming an auto run without it would drop the whole planned-node
	// guard (see NodeToolPolicy).
	ToolPolicies map[string]NodeToolPolicy `json:"tool_policies,omitempty"`
	// Goal links an iterated auto run to its goal group (ADR 0011). nil —
	// and absent from the JSON — on every single-cycle run, which keeps
	// today's snapshots byte-identical; see GoalRef.
	Goal *GoalRef `json:"goal,omitempty"`
	// BuildEvidence records the launch-time build-evidence question and its
	// answer: what was detected in the invocation directory, and how the run
	// answered (ADR 0030 §2.5). Written on every auto-mode launch — `auto` and
	// chat's graph turns — including the ones that answered by attaching a
	// command and the ones where there was nothing to answer. Absent means a run
	// that predates this field, or a `run` of a hand-written graph, which never
	// asks the question. See BuildEvidence.
	BuildEvidence *BuildEvidence `json:"build_evidence,omitempty"`

	// Nodes is the per-node completion record, keyed by node id. Every node that
	// has reached a terminal verdict on any leg so far appears here; CompletedNodes
	// derives the pass-only "unblocks dependents" set from it, and SettledNodes
	// derives the pass-or-fail "must never relaunch" set from the same map.
	Nodes map[string]NodeRecord `json:"nodes,omitempty"`
	// Gate is the run's gate progress: decisions so far and the gate it is paused
	// at, if any.
	Gate GateState `json:"gate"`
}

// MarshalJSON encodes the snapshot with Runtime canonicalized: an empty value
// is written as RuntimeClaude, never omitted and never as "". Everything else
// is encoded exactly as the struct tags say, so the bytes are unchanged for
// every snapshot that already named its runtime.
//
// This lives on the type, not on Write and not on the CLI's path, because the
// hole it closes is not one caller's bug. `runtime` was `omitempty`, and the
// only thing keeping it in the file was that the CLI happened to canonicalize
// an unset runtime before writing. A future writer calling Write — or
// marshaling a Snapshot by any other route — would have persisted a schema-3
// snapshot with NO runtime, which every consumer then reads as claude even
// when the run was Codex — reopening the hole the schema-3 bump was taken to
// close (see the Runtime field's comment for that argument, stated once).
//
// The two lesser fixes were rejected for being weaker in the same way. Merely
// dropping `omitempty` still lets a caller persist `"runtime": ""` — the same
// missing answer wearing a different shape. Canonicalizing inside Write covers
// Write and nothing else, so the guarantee would again be a property of one
// code path rather than of the format. Go cannot make the zero value
// unrepresentable in memory (there is no non-empty string zero value, and an
// unexported field behind a constructor would break every Snapshot literal in
// the repo), but the bad state that matters is the one on DISK, and this makes
// that one impossible: the value is canonicalized at the boundary where the
// contract is actually produced.
//
// The receiver is a value, so canonicalization is local to the encoding and
// never rewrites the caller's snapshot — SnapshotRecorder's base keeps whatever
// it was seeded with. One Go footgun to know about: a struct that EMBEDS
// Snapshot promotes this method and would marshal as a bare snapshot. Hold a
// Snapshot in a named field instead.
func (s Snapshot) MarshalJSON() ([]byte, error) {
	// A local defined type strips Snapshot's method set, so json.Marshal below
	// uses the struct tags instead of recursing into this method.
	type snapshot Snapshot
	if s.Runtime == "" {
		s.Runtime = RuntimeClaude
	}
	return json.Marshal(snapshot(s))
}

// CompletedNodes returns the set of node ids that have completed successfully —
// the ones whose dependents may proceed — in the shape graph.ReadyGiven expects.
// A node counts as completed only when its record's verdict is VerdictPass: a
// failed node ran but did not unblock anything downstream, so it must not seed
// the next leg's ready set. Never nil.
//
// This is the pass-only view: it answers "whose dependents may proceed", not
// "what must never run again" — a FAIL node is deliberately absent, because a
// failed node's own completion must not satisfy its dependents' dependency
// (that is what keeps a --continue-on-fail subtree pruned across a resume).
// Scheduler.Options.CompletedNodes and graph.ReadyGiven's `done` parameter both
// want exactly this pass-only meaning. A caller that instead needs "every node
// that must not be relaunched" — resume's own ready-set seeding — wants
// SettledNodes, not this.
func (s Snapshot) CompletedNodes() map[string]bool {
	done := make(map[string]bool, len(s.Nodes))
	for id, rec := range s.Nodes {
		if rec.Verdict == VerdictPass {
			done[id] = true
		}
	}
	return done
}

// SettledNodes returns every node id that reached ANY terminal verdict in an
// earlier leg — VerdictPass or VerdictFail — the set that must never be
// launched again on resume, regardless of whether it unblocked its
// dependents. Without this, a node that FAILED under --continue-on-fail is
// absent from CompletedNodes (by design — see its doc comment), so
// graph.ReadyGiven would see it as neither done nor blocked and hand it back
// to resume as ready, re-running (and re-paying for) a failure that
// --continue-on-fail already decided was final for this run. Scheduler
// callers pass this as Options.SettledNodes purely to gate re-launch; it must
// NOT be used anywhere a "did this satisfy its dependents" answer is wanted,
// or a failed node's pruned subtree would wrongly un-prune (use
// CompletedNodes for that). Never nil.
func (s Snapshot) SettledNodes() map[string]bool {
	settled := make(map[string]bool, len(s.Nodes))
	for id, rec := range s.Nodes {
		if rec.Verdict == VerdictPass || rec.Verdict == VerdictFail {
			settled[id] = true
		}
	}
	return settled
}

// SchemaMismatchError is returned by Load when a snapshot's Schema does not match
// the running binary's Schema constant. It names the path and both versions so
// the CLI can tell the user exactly why an old run cannot be resumed by a newer
// (or older) build, instead of failing on a confusing downstream decode error.
type SchemaMismatchError struct {
	Path  string
	Found int
	Want  int
}

func (e *SchemaMismatchError) Error() string {
	return fmt.Sprintf(
		"snapshot %q has schema version %d, but this build understands version %d; "+
			"it was written by an incompatible version of oh-my-graph and cannot be resumed",
		e.Path, e.Found, e.Want,
	)
}

// Write persists s to path atomically: it marshals first, writes the bytes to a
// temp file in the destination directory, fsyncs and closes it, then renames it
// over path. A rename within one directory is atomic on POSIX, so a reader of
// path always sees either the previous good snapshot or the complete new one,
// never a half-written file — which is what makes a snapshot safe to overwrite
// after every node, and what makes an interrupted write non-destructive.
//
// Marshaling happens before anything on disk is touched, so a snapshot that
// cannot be encoded (e.g. a Graph holding invalid JSON) fails without disturbing
// an existing good file at path. Write stamps s.Schema to the current Schema
// constant, so the caller cannot accidentally persist a wrong version, and the
// marshal itself canonicalizes an unset runtime (Snapshot.MarshalJSON), so the
// caller cannot accidentally persist a snapshot that does not say which CLI ran
// it either. Both stamps are on the value copy: the caller's snapshot is
// untouched.
//
// A snapshot is owner-only at rest, which matters because Graph carries every
// node's prompt verbatim and Inputs carries the values interpolated into them.
// The file mode is 0o600 without a mode argument here — os.CreateTemp creates
// at 0o600 and the rename carries that over — and the run directory is now
// created 0o700 above, so the enclosing directory no longer hands a co-tenant
// the rest of the run. This is at rest ONLY: while a node runs, the same prompt
// text is in its argv (SECURITY.md, "What is exposed while a node runs").
func Write(path string, s Snapshot) error {
	s.Schema = Schema

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("encode snapshot: %w", err)
	}

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("create run dir %q: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp snapshot in %q: %w", dir, err)
	}
	tmpName := tmp.Name()
	// Best-effort cleanup of the temp file on any failure before the rename; after
	// a successful rename there is nothing left to remove.
	defer os.Remove(tmpName)

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("write temp snapshot %q: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("sync temp snapshot %q: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp snapshot %q: %w", tmpName, err)
	}

	if err := os.Rename(tmpName, path); err != nil {
		return fmt.Errorf("commit snapshot to %q: %w", path, err)
	}
	return nil
}

// Load reads and decodes the snapshot at path. It refuses a snapshot whose Schema
// does not match this build's Schema constant with a *SchemaMismatchError, so an
// incompatible format is a clear, named failure rather than a misread. A missing
// file or malformed JSON is returned wrapped, never as a zero Snapshot with a nil
// error.
func Load(path string) (Snapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Snapshot{}, fmt.Errorf("read snapshot %q: %w", path, err)
	}

	var s Snapshot
	if err := json.Unmarshal(data, &s); err != nil {
		return Snapshot{}, fmt.Errorf("decode snapshot %q: %w", path, err)
	}
	if s.Schema != Schema {
		return Snapshot{}, &SchemaMismatchError{Path: path, Found: s.Schema, Want: Schema}
	}
	return s, nil
}
