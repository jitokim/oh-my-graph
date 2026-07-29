# The run feed — oh-my-graph's consumer contract

oh-my-graph **executes**; it does not render. Everything an external consumer
(canonically [fleetops](https://github.com/jitokim/fleetops)) needs to observe
a run lives in that run's directory:

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
| Version field | top-level `schema` (source of truth: `internal/runstate.Schema`, currently **2**) | per-event `schema` (source of truth: `internal/runfeed.Schema`, currently **1**) |

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
| `schema` | int | Event format version. Currently **1**. |
| `ts` | string | Emission time, RFC 3339 UTC with nanosecond precision. |
| `run_id` | string | The run this stream belongs to. |
| `event` | string | One of the event types below. |

### Event types and their extra fields

| `event` | Extra fields | Emitted when |
|---|---|---|
| `run_started` | — | A scheduler leg begins, before any node launches. |
| `node_started` | `node_id` | A node (claude node or gate) begins execution. |
| `node_passed` | `node_id`, `verdict` (`"PASS"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal PASS (including an approved gate). |
| `node_failed` | `node_id`, `verdict` (`"FAIL"`), `cost_usd`, `session_id`, `retries`, `detail` | A node reaches a terminal FAIL (any check, the verifier, its budget, the runner, or a rejected gate). |
| `node_retried` | `node_id`, `retries` (1-based retry ordinal) | A retry attempt begins after a failed one. |
| `run_finished` | `outcome` (`"passed"` \| `"failed"` \| `"paused"`) | The leg ends — every launch settled. A gate pause is `"paused"`, not `"failed"`. |

On terminal node events, `retries` is the number of retries that preceded the
terminal attempt (0 for a first-attempt verdict), `cost_usd` is the node's
reported spend, `session_id` is the claude session it ran under, and `detail`
is the same short note the run ledger records (the failure cause on a FAIL;
the retry/budget note, possibly empty, on a PASS). Zero/empty values are
**omitted** from the JSON — treat an absent `cost_usd`/`retries` as 0 and an
absent `session_id`/`detail` as none (e.g. a gate spawns no subprocess, so its
`node_passed` carries neither cost nor session).

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
  same stream, bracketed by its own `run_started`/`run_finished`. A run that
  paused at a gate therefore contains one bracket pair per leg; the run as a
  whole is finished when the latest `run_finished` outcome is not `"paused"`.

## Version / compatibility rule

Both files follow the same rule:

- The `schema` value identifies the format. It is bumped whenever a change
  could be **misinterpreted** by an existing reader — a field renamed,
  retyped, or changed in meaning, or (for the stream) an existing event type's
  semantics changed.
- Purely **additive** changes — a new optional field old readers ignore, or a
  new event type — do **not** bump the schema. Consumers must therefore ignore
  unknown fields and skip unknown event types rather than fail.
- oh-my-graph itself refuses to resume a `state.json` whose schema it does not
  exactly match (`runstate.Load`). Consumers of `events.jsonl` should check
  `schema` per event and surface (not silently misread) a version they do not
  understand.

Anything in the run directory not listed here (lock files, temp files) is
internal and carries no compatibility promise.
