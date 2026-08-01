# The run feed — oh-my-graph's consumer contract

oh-my-graph **executes**; it does not render. Everything an external consumer
(canonically [fleetops](https://github.com/jitokim/fleetops)) needs to observe
a run lives in that run's directory. oh-my-graph's own read-back commands —
`runs list`, `show`, `watch`, and the `serve` web live view — are in-repo
consumers of the very same files under the very same rules (via
`runfeed.InFlight` and `runfeed.Follow`/`FollowWait`), with no side channel:

```
~/.oh-my-graph/runs/<run-id>/
  state.json     versioned atomic SNAPSHOT  — whole-run state, overwritten after every node
  events.jsonl   versioned append-only STREAM — one line per lifecycle transition
  <node-id>.out  per-node artifacts (handoff: artifact)
  graph.json     the planned spec (auto runs only)
```

Run directories live under the user's home regardless of where oh-my-graph
was invoked; set `OMG_HOME` to relocate the base (`$OMG_HOME/runs/<run-id>/`).

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
`continue_on_fail`, `tool_policies` (auto runs only), `nodes` (map of node id →
terminal record: `verdict`, `session_id`, `cost_usd`, `budget_usd`, `duration`
in nanoseconds, `artifact_path`, `detail`), and `gate` (`paused_at`,
`decisions`).

Guarantees:

- The file is written via temp-file + fsync + atomic rename, so a reader
  always sees a complete, self-consistent document — never a partial write.
- It is rewritten after **every** node's terminal verdict, so it is at most
  one in-flight node stale.
- It carries only terminal state: a node currently running is absent from
  `nodes`. Live progress is what `events.jsonl` is for.

## `events.jsonl` — the stream

One JSON object per line, appended at each lifecycle transition, from the same
scheduler hook points that feed the human progress lines and the snapshot —
the three can never disagree about a transition. The Go source of truth is
`internal/runfeed` (the `Event` type and `Schema` constant).

### Common fields (every event)

| Field | Type | Meaning |
|---|---|---|
| `schema` | int | Event format version. Currently **2**. |
| `ts` | string | Emission time, RFC 3339 UTC with nanosecond precision. |
| `run_id` | string | The run this stream belongs to. |
| `event` | string | One of the event types below. |

### Event types and their extra fields

| `event` | Extra fields | Emitted when |
|---|---|---|
| `run_started` | — | A scheduler leg begins, before any node launches. |
| `node_started` | `node_id`, `session_id` *(optional)* | A node (claude node or gate) begins execution. |
| `node_passed` | `node_id`, `verdict` (`"PASS"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal PASS (including an approved gate). |
| `node_failed` | `node_id`, `verdict` (`"FAIL"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal FAIL (any check, the verifier, its budget, the runner, or a rejected gate). |
| `node_retried` | `node_id`, `retries` (1-based retry ordinal), `session_id` *(optional)* | A retry attempt begins after a failed one. |
| `run_finished` | `outcome` (`"passed"` \| `"failed"` \| `"paused"`), `detail` *(optional)* | The leg ends — every launch settled. A gate pause is `"paused"`, not `"failed"`. A subscription session-limit pause (ADR 0009) is also `"paused"`, distinguished by a `detail` naming the limited node(s) and the CLI's own limit message (an additive field — no schema bump; absent on every other outcome). The limited nodes carry **no** terminal node event: they are un-run, not FAILED, and re-run on `resume --retry-failed`. |
| `gate_paused` | `node_id` | *(schema 2)* A gate node decided to pause: no new work launches, in-flight siblings drain, and the leg closes with outcome `"paused"`. `node_id` is the gate a resume must decide. |
| `gate_approved` | `node_id` | *(schema 2)* A gate decision of approve was applied (a resumed leg replaying `--approve`); the gate's terminal `node_passed` follows. |
| `gate_rejected` | `node_id` | *(schema 2)* A gate decision of reject was applied (a resumed leg replaying `--reject`); the gate's terminal `node_failed` follows and its subtree is pruned. |

On terminal node events, `retries` is the number of retries that preceded the
terminal attempt (0 for a first-attempt verdict), `cost_usd` is the node's
reported spend, `session_id` is the claude session it ran under, and `detail`
is the same short note the run ledger records (the failure cause on a FAIL;
the retry/budget note, possibly empty, on a PASS). The producer caps `detail`
at one shared bound (240 runes, keeping the tail), so a line stays tailable
even when the underlying error was arbitrarily long. Zero/empty values are
**omitted** from the JSON — treat an absent `cost_usd`/`retries` as 0 and an
absent `session_id`/`detail` as none (e.g. a gate spawns no subprocess, so its
`node_passed` carries neither cost nor session).

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
  precedes its retries and its terminal event.
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
  `state.json`.
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
