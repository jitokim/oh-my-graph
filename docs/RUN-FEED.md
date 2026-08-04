# The run feed — oh-my-graph's consumer contract

`state.json` and `events.jsonl` in a run's own directory are the contract for
run-feed views — the run's state and progress are never visible only from
inside the process that ran it. oh-my-graph's own read-back commands — `runs
list`, `show`, `watch`, and the `serve` web views — are in-repo consumers of
the very same files, and an external consumer reads exactly what they read:

```
~/.oh-my-graph/runs/<run-id>/
  state.json     versioned atomic SNAPSHOT  — whole-run state, overwritten after every node
  events.jsonl   versioned append-only STREAM — one line per lifecycle transition
  <node-id>.out  per-node artifact — EVERY node that passes, whatever its handoff
  graph.json     the planned spec (auto runs only)
  assess.json    the goal-cycle assessment verdict (iterated auto runs only — ADR 0011)
```

`<node-id>.out` is written for every node that reaches a PASS, not only for
`handoff: artifact` nodes: `handoff` selects what a *child* inherits, not
whether the parent's result is persisted (`Handoff.PersistOutput` is called on
the one passing path, with no handoff branch). A consumer must not skip the
`.out` beside a `handoff: session` node — it is there, and it holds that
node's real result. A gate node spawns nothing and so has no `.out`.

Run directories live under the user's home regardless of where oh-my-graph
was invoked; set `OMG_HOME` to relocate the base (`$OMG_HOME/runs/<run-id>/`).

Two reads reach outside this contract, both in `serve`, and neither is
something an external consumer needs:

- `serve` tails a running node's own claude transcript from
  `~/.claude/projects` to stream its live output. That is a *supplement* to
  the feed, not a substitute — the transcript is claude's file, on claude's
  schema, and the run-feed events (`node_started`/`node_retried` publishing
  the attempt's session id) are what let any consumer locate it.
- `serve`'s gate endpoint takes and releases the run's `resume.lock` before it
  accepts a decision — holding it is how "no leg is in flight" is established.
  The lock is an internal file with no compatibility promise (see the last
  line of this document); it is how a *writer* coordinates, not how a reader
  reads.

Two in-repo readers also apply this contract's rules by hand instead of
through `internal/runfeed`, so follow `internal/runfeed` rather than them:
`serve`'s dashboard card re-derives `runfeed.InFlight`'s rule inline off a
walk it already does (the two are held together by
`TestBuildCard_InFlightAgreesWithRunfeed`), and `serve`'s `/api/transcript`
hand-rolls its own scanner without the newer-`schema` refusal `runfeed.Walk`
makes. Everything this document versions and guarantees is `state.json` and
`events.jsonl`.

`state.json` and `events.jsonl` together are a **stable consumer API**. The
files answer complementary questions:

| | `state.json` | `events.jsonl` |
|---|---|---|
| Shape | one JSON document, overwritten atomically | JSON Lines, append-only |
| Answers | "where is the run *now*?" (resume reads this) | "what *happened*, in order, live?" |
| Write discipline | temp file + fsync + rename after every node | one `write` + fsync per line |
| Reader pattern | re-read the whole file | `tail -f` / seek to last offset |
| Version field | top-level `schema` (source of truth: `internal/runstate.Schema`, currently **2**) | per-event `schema` (source of truth: `internal/runfeed.Schema`, currently **2**) |

The two files version independently — a snapshot format change does not bump
the event schema, and vice versa.

## `state.json` — the snapshot

The authoritative field-by-field documentation is the doc comments in
`internal/runstate/runstate.go` (the `Snapshot`, `NodeRecord`, and `GateState`
types); this section is the consumer-facing summary.

Top-level fields: `schema`, `run_id`, `graph_source_path`, `graph_sha256`,
`graph` (the normalized DAG as re-parseable JSON), `inputs`,
`continue_on_fail`, `tool_policies` (auto runs only), `goal` (iterated auto
runs only — see "Goal cycles" below), `nodes` (map of node id →
terminal record: `verdict`, `session_id`, `cost_usd`, `budget_usd`, `duration`
in nanoseconds, `artifact_path`, `detail`, and — for executions inside a
feedback loop (ADR 0010) — `round`, the 1-based round ordinal, absent on any
execution outside one), and `gate` (`paused_at`, `decisions`).

One record is not terminal: a feedback declarer's record mid-loop is a
non-terminal **marker** — `round` k with an **empty** `verdict` — written the
moment its arc fires, so a run stopped mid-loop resumes into the loop. The
`verdict` key is always *present*: `NodeRecord.Verdict` carries no
`omitempty`, so a marker serializes as `"verdict": ""`, not as a record
missing the key. A consumer deriving "settled" from `nodes` must therefore
test the *value* (`verdict` neither `"PASS"` nor `"FAIL"` ⇒ still in flight);
testing key-absence misclassifies every marker as terminal. (The *stream* is
the other way round: `runfeed.Event.Verdict` does carry `omitempty`, so an
absent `verdict` there means none — see "Event types" below.) A node's
recorded `cost_usd` accumulates across its rounds (a superseded round's spend
carries into the record that replaces it), so the per-node figures still sum
to the run's true total.

Guarantees:

- The file is written via temp-file + fsync + atomic rename, so a reader
  always sees a complete, self-consistent document — never a partial write.
- It is rewritten after **every** node's terminal verdict, so it is at most
  one in-flight node stale.
- It carries settled state only: a node currently running is absent from
  `nodes`. The one non-terminal record is the feedback marker above —
  `round` with an empty `verdict` — which is a settled fact about the loop,
  not live progress. Live progress is what `events.jsonl` is for.

## Goal cycles (ADR 0011) — the `goal` block and `assess.json`

An iterated auto run (`oh-my-graph auto "<goal>" --max-cycles N`, N ≥ 2)
executes each cycle as **its own ordinary run** under everything documented
here — fresh run id, own directory, own snapshot and stream. Two additive
artifacts link and judge the cycles; both follow the additive rule (**no
schema bump**), and a consumer that ignores them sees exactly N well-formed,
unrelated runs.

- **`goal`** — an optional top-level `state.json` block, absent entirely on
  single-cycle runs (today's snapshots are byte-identical):

  ```json
  "goal": {
    "text": "make the test suite green",
    "cycle": 2,
    "max_cycles": 3,
    "first_run_id": "<cycle 1's run id>"
  }
  ```

  `first_run_id` is the stable group key (equal to the run's own id on cycle
  1); the chain is derivable from it plus `cycle`, so no previous-run pointer
  is stored. The block survives resume legs (a session-limit-paused cycle
  keeps its lineage when finished by `resume --retry-failed`).

- **`assess.json`** — the cycle's assessment verdict, written into the
  assessed run's directory the moment assessment returns: `goal_met` (bool),
  `remaining` (what is left; omitted when empty), `evidence` (the material
  that decided it; omitted when empty), and `assess_cost_usd` — the
  assessment call's own spend, recorded here because the run's ledger prints
  before the assessment that judges it has run. Absent on single-cycle runs,
  and absent on a cycle that never got a verdict (a pause, a declined plan).

Stated as the price of "no schema bump": **goal lineage is snapshot-only.**
No `events.jsonl` event carries the goal group — the stream's event-type set
is closed per schema version, and each cycle's stream is simply that run's
ordinary `run_started` … `run_finished` bracket. A consumer that only tails
feeds must read the `goal` block from `state.json` to group cycles.

## `events.jsonl` — the stream

One JSON object per line, appended at each lifecycle transition, from the same
scheduler hook points that feed the human progress lines and the snapshot, so
the three describe the same transitions in the same terms. The Go source of
truth is `internal/runfeed` (the `Event` type and `Schema` constant).

They can still disagree in exactly one direction, and a consumer should know
it: **a failed event write does not fail the run.** A consumer's feed must
never kill the run it is watching, so a write error is surfaced as a `⚠`
warning on the progress feed and the scheduler continues — and by that point
the node's ledger row and its snapshot record are already written. The stream
can therefore be *missing* a transition the snapshot holds; it never carries
one the snapshot lacks. So: use `events.jsonl` for order and liveness, and
treat `state.json` as authoritative for settled state. A consumer that must
not miss a terminal verdict (a billing or audit reader) should reconcile
against `state.json` rather than trusting the stream to be complete — the
snapshot is rewritten after every terminal verdict, so a gap is recoverable.

### Common fields (every event)

| Field | Type | Meaning |
|---|---|---|
| `schema` | int | Event format version. Currently **2**. |
| `ts` | string | Emission time, RFC 3339 UTC with nanosecond precision. Not a sort key — see "Ordered per emission". |
| `run_id` | string | The run this stream belongs to. |
| `event` | string | One of the event types below. |

### Event types and their extra fields

| `event` | Extra fields | Emitted when |
|---|---|---|
| `run_started` | — | A scheduler leg begins, before any node launches. |
| `node_started` | `node_id`, `session_id` *(optional)* | A node (claude node or gate) begins execution. |
| `node_passed` | `node_id`, `verdict` (`"PASS"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal PASS (including an approved gate). |
| `node_failed` | `node_id`, `verdict` (`"FAIL"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal FAIL (any check, the verifier, its budget, the runner, or a rejected gate). |
| `node_retried` | `node_id`, `retries` (1-based retry ordinal), `session_id` *(optional)*, `round` *(optional)* | A retry attempt begins after a failed one — or a feedback arc fires (ADR 0010): the declarer's non-final judgment failure re-arms its loop body, with `round` the 1-based round now beginning, `retries` 0, no `session_id` (the re-run's ids arrive on its own `node_started`s), and a `detail` of the form `feedback round 1/2: re-running impl → review`. |
| `run_finished` | `outcome` (`"passed"` \| `"failed"` \| `"paused"`), `detail` *(optional)* | The leg ends — every launch settled. A gate pause is `"paused"`, not `"failed"`. A subscription session-limit pause (ADR 0009) is also `"paused"`, distinguished by a `detail` naming the limited node(s) and the CLI's own limit message (an additive field — no schema bump; absent on every other outcome). The limited nodes carry **no** terminal node event: they are un-run, not FAILED, and re-run on `resume --retry-failed`. |
| `gate_paused` | `node_id` | *(schema 2)* A gate node decided to pause: no new work launches, in-flight siblings drain, and the leg closes with outcome `"paused"`. `node_id` is the gate a resume must decide. |
| `gate_approved` | `node_id` | *(schema 2)* A gate decision of approve was applied (a resumed leg replaying `--approve`); the gate's terminal `node_passed` follows. |
| `gate_rejected` | `node_id` | *(schema 2)* A gate decision of reject was applied (a resumed leg replaying `--reject`); the gate's terminal `node_failed` follows and its subtree is pruned. |

On terminal node events, `retries` is the number of retries that preceded the
terminal attempt (0 for a first-attempt verdict), `cost_usd` is the node's
reported spend, `session_id` is the claude session it ran under, and `detail`
is the same short note the run ledger records (the failure cause on a FAIL;
the retry/budget note, possibly empty, on a PASS). The producer caps the
*cause* text at one shared bound (240 runes, keeping the tail), so a line
stays tailable even when the underlying error was arbitrarily long. That bound
is not a hard bound on the field: a body node inside a feedback loop gets its
short round note (`; feedback round k/N`) appended *after* the cap, so such a
`detail` runs a few tens of runes over 240. Size `detail` as "short, bounded
by roughly 300 runes", never as "exactly ≤ 240" — and rely on the 1 MiB
per-line cap below for the actual hard limit. Zero/empty values are
**omitted** from the JSON — treat an absent `cost_usd`/`retries` as 0 and an
absent `session_id`/`detail` as none (e.g. a gate spawns no subprocess, so its
`node_passed` carries neither cost nor session).

Node events emitted by a feedback re-execution (ADR 0010) additionally carry
`round`, the 1-based feedback round the execution belongs to — an optional
additive field, **no schema bump**; absent means 0, the initial pass. Body
nodes therefore emit one full `node_started` → terminal sequence per round
*within* a leg. The declarer's non-final judgment failure is a
`node_retried`, never a `node_failed` — `node_failed` stays terminal and
appears at most once per declarer per leg, when the failure is final (its
`detail` then names the spend: `feedback exhausted after 2 rounds of impl →
review: …`).

On `node_started` and `node_retried`, `session_id` is the **pre-assigned**
session id the engine hands claude via `--session-id` (a UUID minted before
the subprocess spawns), published early so a consumer can locate a *running*
node's transcript instead of waiting for the terminal event; the terminal
events' `session_id` stays envelope-sourced (the same id, as claude reported
it back). It is **absent** on a gate's `node_started` (a gate spawns no
subprocess) and on a session-handoff node's `node_started` (that node resumes
its parent's session, whose id the parent's own terminal event already
carried). Each retried attempt gets a *fresh* id — the failed attempt's id
names the failed attempt's transcript — so `node_retried`'s `session_id`
supersedes the one published before it. This is an optional field existing
readers ignore, added under the additive rule below: **no schema bump**.
The in-repo reference consumer is `serve`'s `/api/transcript`: it reduces
this stream to "is the node running, and under which session", then serves
that session's transcript tail as the live view's "now doing" line.

The gate decision events (`gate_paused`, `gate_approved`, `gate_rejected`,
added in schema **2**) mark the decision itself; an approved/rejected gate
still gets its terminal `node_passed`/`node_failed` immediately after, so a
consumer that only understands node events keeps working. `gate_paused` is
emitted the moment the gate decides to pause — in-flight siblings drain
*after* it, so their node events may legally appear between it and the leg's
`run_finished` (outcome `"paused"`). If several gates evaluate concurrently,
each pausing gate emits its own `gate_paused`; the gate the run is actually
resumable at is the one `state.json` records in `gate.paused_at` (the first
to pause).

### Guarantees

- **Append-only.** Lines are only ever appended, never rewritten; a consumer
  may safely remember a byte offset and resume tailing from it.
- **Crash-safe.** Each line lands in a single `write` followed by an fsync: a
  crash leaves at most one truncated final line, and every complete line is
  durable. A consumer must tolerate (skip or re-poll) a partial last line.
- **Ordered per emission.** Lines appear in the order events were emitted.
  Nodes running in parallel interleave; per node, `node_started` always
  precedes its retries and its terminal event. **File order is the ordering —
  `ts` is not.** The writer stamps `ts` before it takes the append lock, so
  two events emitted concurrently by parallel nodes may land in the file in
  the opposite order to their timestamps. `ts` is a good-enough wall clock for
  display and for durations; never sort a stream by it, and never infer
  "happened before" from it. Read the file in the order it is written.
- **Legs, not just runs.** A resumed run (`oh-my-graph resume`) appends to the
  same stream, bracketed by its own `run_started`/`run_finished` — a gate
  resume and a `--retry-failed` leg alike. A run that paused at a gate
  therefore contains one bracket pair per leg; the run as a whole is finished
  when the latest `run_finished` outcome is not `"paused"` — though a later
  `resume --retry-failed` may reopen a `"failed"` run with a new bracket
  pair. Because a retry leg re-executes previously failed nodes, one node id
  may carry terminal events in more than one leg (a `node_failed` in an
  earlier one, a fresh `node_started` and terminal in the retry leg); the
  latest terminal event per node is the authoritative one, matching
  `state.json`. Feedback rounds (ADR 0010) extend the same rule *within* a
  leg: a body node emits one terminal event per round, and the latest —
  the highest `round` — is authoritative.
- **Short lines.** Every event line the writer emits is small (a handful of
  short fields; well under a few kilobytes even with a long `detail`). The
  in-repo readers enforce a shared 1 MiB per-line cap and refuse — with an
  error, never a silent truncation — a stream whose line exceeds it, treating
  it as corrupt or foreign; an external consumer may assume the same bound.

## Version / compatibility rule

Both files follow the same rule:

- The `schema` value identifies the format. It is bumped whenever a change
  could be **misinterpreted** by an existing reader — a field renamed,
  retyped, or changed in meaning, or (for the stream) an existing event type's
  semantics changed.
- A new **optional field** old readers ignore does **not** bump the schema;
  consumers must ignore unknown fields.
- For the stream, the **event-type set is closed per schema version**: adding
  an event type bumps `schema` (this is how stream schema 1 became 2, adding
  the three `gate_*` events), so a consumer can detect that the stream may
  carry transitions its version did not define. Consumers must still skip
  unknown event types rather than fail — the bump makes the change visible,
  not fatal.
- oh-my-graph itself refuses to resume a `state.json` whose schema it does not
  exactly match (`runstate.Load`). Consumers of `events.jsonl` should check
  `schema` per event and surface (not silently misread) a version they do not
  understand.

Anything in the run directory not listed here (lock files, temp files) is
internal and carries no compatibility promise.
