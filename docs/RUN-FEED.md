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
  <node-id>.out  per-node artifact — EVERY non-gate node that passes, whatever its handoff
  graph.json     the planned spec (auto runs only)
  rejected.json  a REFUSED planner reply, kept because the call was paid for (ADR 0023 §3.1)
  assess.json    the goal-cycle assessment verdict (iterated auto runs only — ADR 0011)
  failed/        per-node reply of a node that FAILED (ADR 0020) — never an artifact
  feedback/      INTERNAL — feedback-arc payloads (ADR 0010); not this contract
  worktrees/     INTERNAL — per-node git worktrees; not this contract
```

`<node-id>.out` is written for every non-gate node that reaches a PASS, not only for
`handoff: artifact` nodes: `handoff` selects what a *child* inherits, not
whether the parent's result is persisted (`Handoff.PersistOutput` is called on
the one passing path, with no handoff branch). A consumer must not skip the
`.out` beside a `handoff: session` node — it is there, and it holds that
node's real result. A gate node spawns nothing and so has no `.out`.

**A run directory may legitimately hold NEITHER `graph.json` NOR `state.json`,
and this is not damage** (ADR 0023). Two shapes reach it. While an `auto` run is
inside its planner call, its directory holds only `resume.lock` and a one-line
`events.jsonl` — the run id is minted before the call, so the phase is visible
from the moment it starts. And a run whose planner reply was REFUSED keeps that
shape permanently, plus `rejected.json`: the call was paid for, the engine
judged the material it was given and diagnosed it, and nothing ever ran. A
consumer must not read a missing snapshot as a broken run at any status — the
ABSENCE of a file is a fact about the run, while the UNREADABILITY of one is a
fact about the reader, and only the second is damage.

`rejected.json` lands in the run directory when a run exists (`auto`, and every
cycle of an iterated one) and under `~/.oh-my-graph/plans/<id>/` when none does
(`auto --plan-only`, which mints no run id at any point — a preview is not a
run). It is deliberately NOT named `graph.json`: nothing walking the tree for a
graph the engine would run may pick it up.

`~/.oh-my-graph/plans/<id>/` therefore holds BOTH shapes a paid-for plan can
take outside a run, under the two names that keep them apart. A REFUSED planner
reply from a command that minted no run id — `auto --plan-only`, and `chat`,
whose plan is gated behind a `[y/N]` a human may answer `n` — is kept as
`rejected.json`. A VALID plan that never ran — a `--plan-only` preview, or a
`chat` plan DECLINED at that prompt — is kept as `graph.json`, because it IS a
graph the engine would run: `oh-my-graph run <path>` on it is how you change
your mind later. Neither is a run, and no `plans/` directory is ever reported by
`runs list`.

The converse does not hold: a `.out` is not proof of a PASS. `PersistOutput`
runs *before* the post-hoc budget check, deliberately, so a node that did its
work and then blew its `budget_usd` FAILS with its artifact already on disk.
The verdict lives in `state.json` and in the terminal event; the file's
existence is not a verdict. Nor is every `<node-id>.out` in the run tree an
artifact: a feedback arc's payload is written to
`<run-dir>/feedback/<node-id>.out`, and a FAILED node's own reply to
`<run-dir>/failed/<node-id>.out` — the same basename shape, one directory
down (ADR 0010, ADR 0020). Artifacts are the flat ones.

`failed/<node-id>.out` holds the words of a node that failed, bounded at
256 KiB with any cut announced in the file, and is part of this contract:
`Handoff.FailedOutputPath` computes it, a `resume --retry-failed` leg re-reads
it to quote a `judged` failure back into the retry, and a consumer may read it
for the analysis the ledger's capped `detail` does not carry. Its existence is
a claim about *this* node's *last* outcome and is kept true in both directions:
a node that passes has no such file (one an earlier leg wrote is removed when
this leg's execution passes), and a node whose reply was empty leaves none.
One node can hold both a flat artifact and a `failed/` reply — a post-hoc
`budget_usd` failure persists its output first, as above — and there the
verdict in `state.json` is FAIL and `judged` is absent.

Run directories live under the user's home regardless of where oh-my-graph
was invoked; set `OMG_HOME` to relocate the base (`$OMG_HOME/runs/<run-id>/`).

One thing reaches outside this contract, and it is not something an external
consumer needs: `serve` tails a running node's own claude transcript from
`~/.claude/projects` to stream its live output. That is a *supplement* to the
feed, not a substitute — the transcript is claude's file, on claude's schema,
and the run-feed events (`node_started`/`node_retried` publishing the attempt's
session id) are what let any consumer locate it.

**That supplement exists for Claude runs only, and there is no Codex
equivalent.** `/api/transcript` looks for `<session-id>.jsonl` under
`~/.claude/projects` and nowhere else, so for a Codex node — whose published id
is a `codex exec` thread rather than a file there — it answers 204, and that is
unchanged: 204 is the honest "nothing to show" the endpoint already gives any
node with no transcript, and it takes no runtime branch to say it.

What the live view does with that answer IS runtime-aware, in exactly one
place. `/api/graph` reads the snapshot's `runtime` and, for a runtime that
keeps no per-node transcript, carries a `transcript_note` — one sentence saying
so, which the page renders in the tail's slot on each running node's feed line
instead of polling an endpoint that could only 204. The key is **absent** on a
Claude run, where the tail works as before. This is the view's ONE runtime
branch, and it is the narrowest true statement of it: nothing else about the
view takes a second code path — node states, verdicts, cost and the settled
per-node result all come from the two files this document contracts, and the
cost figure differs only because the data does, reading `cost unknown` off the
`cost_unknown` flag described below. The page itself still holds no runtime
knowledge: it renders whatever string that field hands it.

A consumer building its own live output for Codex has no in-repo reader to
follow.

A third file, `resume.lock`, is **not** internal any more: it is how a reader
tells a run that is thinking from one whose process is gone. See "Liveness —
`resume.lock`" below.

Two in-repo readers also apply this contract's rules by hand instead of
through `internal/runfeed`, so follow `internal/runfeed` rather than them:
`serve`'s dashboard card re-derives `runfeed.InFlight`'s leg rule inline off a
walk it already does (the two are held together by
`TestBuildCard_AgreesWithTheSharedRule`; the *composition* with the lock is not
duplicated — every in-repo surface goes through `internal/runstatus`), and
`serve`'s `/api/transcript` hand-rolls its own scanner without the
newer-`schema` refusal `runfeed.Walk` makes. Everything this document versions
and guarantees is `state.json` and `events.jsonl`.

`state.json` and `events.jsonl` together are a **stable consumer API**. The
files answer complementary questions:

| | `state.json` | `events.jsonl` |
|---|---|---|
| Shape | one JSON document, overwritten atomically | JSON Lines, append-only |
| Answers | "where is the run *now*?" (resume reads this) | "what *happened*, in order, live?" |
| Write discipline | temp file + fsync + rename after every node | one `write` + fsync per line |
| Reader pattern | re-read the whole file | `tail -f` / seek to last offset |
| Version field | top-level `schema` (source of truth: `internal/runstate.Schema`, currently **3**) | per-event `schema` (source of truth: `internal/runfeed.Schema`, currently **3**) |

The two files version independently — a snapshot format change does not bump
the event schema, and vice versa.

## `state.json` — the snapshot

The authoritative field-by-field documentation is the doc comments in
`internal/runstate/runstate.go` (the `Snapshot`, `NodeRecord`, and `GateState`
types); this section is the consumer-facing summary.

Top-level fields: `schema`, `run_id`, `runtime` (`"claude"` or `"codex"`),
`planning_cost_usd`, `planning_cost_unknown`, `planning_usage`,
`graph_source_path`, `graph_sha256`,
`graph` (the normalized DAG as re-parseable JSON), `inputs`,
`continue_on_fail`, `tool_policies` (auto runs only), `goal` (iterated auto
runs only — see "Goal cycles" below), `nodes` (map of node id →
terminal record: `verdict`, `session_id`, `cost_usd`, `cost_unknown`, `usage`
(`input_tokens`, `cached_input_tokens`, `output_tokens`,
`reasoning_output_tokens`), `budget_usd`, `duration`
in nanoseconds, `artifact_path`, `detail`, `judged` — for executions inside a
feedback loop (ADR 0010) — `round`, the 1-based round ordinal, absent on any
execution outside one), and `gate` (`paused_at`, `decisions`).

`runtime` is the run-wide model CLI (ADR 0025). It is omitted when empty and
**absent means `claude`** — the CLI canonicalizes an unset runtime before
writing, so every snapshot the CLI writes carries it. Read that as the rule for
reading, not as a guarantee about writers: the field is `omitempty` and the
canonicalization lives on the CLI's path rather than in `runstate.Write`, so a
caller of the package could still leave it out
([#179](https://github.com/jitokim/oh-my-graph/issues/179)). Either way the
absent case has one meaning and it is `claude`.

It is also the **only** place the runtime is recorded: no event carries it
(`runfeed.Event` has no such field), so a consumer that only tails the stream
cannot tell a Codex run from a Claude one and must read the snapshot to know.

The consequence most likely to matter is accounting: **a Codex invocation
reports no USD at all.** Every such record and event carries `cost_unknown`, and
what you find beside it differs by file — which is exactly where a consumer goes
wrong:

| | `cost_unknown` | `cost_usd` |
|---|---|---|
| `events.jsonl` (`runfeed.Event`) | `true` | **omitted** (`omitempty`) |
| `state.json` (`runstate.NodeRecord`) | `true` | **present, and it reads `0`** |

So the snapshot hands you the very thing this document tells you not to infer.
`cost_unknown: true` makes the number beside it non-authoritative: it is not a
measurement, and a `0` there is filler, not a free call. **Never sum `cost_usd`
across records without checking `cost_unknown` first** — over a Codex run that
sum is $0.00 for work that was paid for.

`judged` (additive, ADR 0020 — no schema bump) marks a FAIL a check rendered a
verdict **on**, as opposed to one the machinery caused: a failed `success_check`
or a verification that ran and said no, never a spawn error, an interpolation
error, a blown budget, or a verification that could not be completed. It is
absent (false) on every PASS and on every marker record. It is the engine's own
answer to "was the work wrong, or did the machinery break?" — a question that
otherwise requires reading `detail` as English — and it is what a
`resume --retry-failed` reads to decide whether to quote a failed node's reply
back into the prompt that retries it.

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
- It carries settled state, **not liveness** — and absence from `nodes` is
  not the complement of "running". Two records break that reading:
  - A node re-running inside a feedback loop (ADR 0010) keeps its
    **previous round's terminal record** for the whole time it is re-running.
    A fired arc re-arms and relaunches the body without clearing any member's
    record (`internal/schedule`'s feedback re-arm), and the recorder *depends*
    on the old record still being there — it folds the superseded round's
    spend into its replacement
    (`runstate.SnapshotRecorder.RecordNode`/`supersedesRound`). So while
    `impl` executes round 2, `nodes` still reads
    `"impl": {"verdict": "PASS", "round": 1}`: a PASS-looking record for a
    node that is at that moment in flight. It is not stale data — it is the
    settled truth about round 1, which is a different question from what
    `impl` is doing now.
  - The feedback declarer's marker (`round` k, empty `verdict`) is present
    and non-terminal, as described above.

  So: a node absent from `nodes` has certainly not settled, but a node
  present in `nodes` is **not** thereby finished. **Key liveness off
  `events.jsonl`, never off presence in `state.json`:** within the current
  leg, a node is running when its latest `node_started`/`node_retried` is not
  followed by a `node_passed`, `node_failed` **or `gate_paused`** for the same
  `node_id`. All three terminate the node's turn on the stream: a gate node
  emits `node_started` and then, if it pauses, `gate_paused` and no node
  terminal at all — read only the two node terminals and a paused gate reads
  as running forever. (oh-my-graph's own dashboard goes one step further and
  keeps `gate-paused` as a distinct state, because "paused, waiting on you" is
  a different thing to show a user than "running"; a consumer that only needs
  liveness can fold it into "not running".) Under feedback the rule needs no
  special case — a body node emits a full `node_started` → terminal sequence
  per round, so the round-2 `node_started` with no terminal after it is exactly
  the signal the snapshot cannot give you.

  The same caveat `runfeed.InFlight` carries at the run level applies here at
  the node level: **the stream records intent, not process liveness.** A
  crashed or killed oh-my-graph leaves its last `node_started` unterminated,
  so by the stream alone such a node reads as running forever. Two things
  bound that, and a consumer wanting to match oh-my-graph's own views should
  apply both: **every `run_started` is a leg boundary** — a node left running
  by an earlier leg is not running, whatever the current leg does — and the
  lock answers whether the *current* leg's writer is alive at all (see
  "Liveness" below). The stream itself stays free of process liveness: no
  reader ever appends to it, and nothing in these two files changed.

  Use `state.json` for *what has settled and at what cost*, and the stream for
  *what is happening now*.

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

They can still disagree, and a consumer should know which way each hole runs.
For a **terminal verdict** the two are meant to agree, and both writes are
deliberately non-fatal, so either side can be the one that is missing:

- **A failed event write does not fail the run.** A consumer's feed must
  never kill the run it is watching, so the error is surfaced as a `⚠`
  warning on the progress feed and the scheduler continues — and by that
  point the node's ledger row and its snapshot record are already written.
  The stream is missing a transition the snapshot holds.
- **A failed snapshot write does not fail the run either.** It gets its own
  non-fatal `⚠ … snapshot write failed` line and the node stays absent from
  `state.json`, while its `node_passed`/`node_failed` still goes out. The
  stream carries a transition the snapshot lacks.

Outside terminal verdicts the two are not meant to agree at all. The snapshot
carries settled state only, so `run_started`, `node_started` and
`node_retried` have no snapshot counterpart by design; and the gate events
lead the snapshot — `gate_approved` is emitted before the gate's snapshot
write, and a `gate_paused` writes no snapshot at all (see "each pausing gate
emits its own `gate_paused`" below).

So: use `events.jsonl` for order and liveness, and treat `state.json` as
authoritative for settled state. A consumer that must not miss a terminal
verdict (a billing or audit reader) should reconcile the two rather than
trust either alone — the snapshot is rewritten after every terminal verdict,
so a dropped event is recoverable from it, and the rarer dropped snapshot
write leaves its event on the stream and its `⚠` on the progress feed.

### Common fields (every event)

| Field | Type | Meaning |
|---|---|---|
| `schema` | int | Event format version. Currently **3**. |
| `ts` | string | Emission time, RFC 3339 UTC with nanosecond precision. Not a sort key — see "Ordered per emission". |
| `run_id` | string | The run this stream belongs to. |
| `event` | string | One of the event types below. |

### Event types and their extra fields

| `event` | Extra fields | Emitted when |
|---|---|---|
| `run_started` | `phase`, `cost_usd`, `cost_unknown`, `usage` *(optional)* | A leg begins. With no `phase`, a scheduler leg, before any node launches — this is every `run_started` written before ADR 0023 and every one a `run` or `resume` leg will ever write. When an auto plan commits, this unphased boundary carries the planner call's accounting. With `phase: "planning"`, the leg an `auto` run's PLANNER CALL runs inside, before any graph exists; see "Two `run_started`s per auto run" below. |
| `node_started` | `node_id`, `session_id` *(optional)* | A node (model-CLI node or gate) begins execution. |
| `node_passed` | `node_id`, `verdict` (`"PASS"`), `cost_usd`, `cost_unknown`, `usage`, `session_id`, `retries`, `detail`, `provenance` *(optional)* | A node reaches a terminal PASS (including an approved gate). `provenance` says **how** — see below. |
| `node_failed` | `node_id`, `verdict` (`"FAIL"`), `cost_usd`, `cost_unknown`, `usage`, `session_id`, `retries`, `detail` | A node reaches a terminal FAIL (any check, the verifier, its budget, the runner, or a rejected gate). |
| `node_retried` | `node_id`, `retries` (1-based retry ordinal), `session_id`, `round`, `cost_usd`, `cost_unknown`, `usage` *(optional)* | A retry attempt begins after a failed one — or a feedback arc fires (ADR 0010): the declarer's non-final judgment failure re-arms its loop body, with `round` the 1-based round now beginning, `retries` 0, no `session_id` (the re-run's ids arrive on its own `node_started`s), and a `detail` of the form `feedback round 1/2: re-running impl → review`. That feedback form carries the completed declarer attempt's accounting because no terminal event is emitted for it. |
| `run_finished` | `outcome` (`"passed"` \| `"failed"` \| `"paused"`), `detail`, `cost_usd`, `cost_unknown`, `usage` *(optional)* | The leg ends — every launch settled. A plan rejected before a graph exists carries the paid planner call's accounting here. A gate pause is `"paused"`, not `"failed"`. A subscription session-limit pause (ADR 0009) is also `"paused"`, distinguished by a `detail` naming the limited node(s) and the CLI's own limit message (an additive field — no schema bump; absent on every other outcome). The limited nodes carry **no** terminal node event: they are un-run, not FAILED, and re-run on `resume --retry-failed`. |
| `gate_paused` | `node_id` | *(schema 2)* A gate node decided to pause: no new work launches, in-flight siblings drain, and the leg closes with outcome `"paused"`. `node_id` is the gate a resume must decide. |
| `gate_approved` | `node_id` | *(schema 2)* A gate decision of approve was applied (a resumed leg replaying `--approve`); the gate's terminal `node_passed` follows. |
| `gate_rejected` | `node_id` | *(schema 2)* A gate decision of reject was applied (a resumed leg replaying `--reject`); the gate's terminal `node_failed` follows and its subtree is pruned. |

Accounting appears exactly once per paid invocation: on a terminal node event,
on a feedback `node_retried` when the prior judgment is deliberately
non-terminal, on the unphased `run_started` that commits a successful plan, or
on `run_finished` when planning is rejected before a graph exists. Consumers
may therefore sum these fields over the stream without runtime-specific logic.

On terminal node events, `retries` is the number of retries that preceded the
terminal attempt (0 for a first-attempt verdict), `cost_usd` is the node's
reported spend, `cost_unknown` distinguishes an unreported USD value from a
free call, `usage` carries input/cached-input/output/reasoning-output token
counts, `session_id` is the selected runtime's session/thread, and `detail`
is the same short note the run ledger records (the failure cause on a FAIL;
the retry/budget note, possibly empty, on a PASS). The producer caps the
*cause* text at one shared bound (240 runes, keeping the tail), so a line
stays tailable even when the underlying error was arbitrarily long. That bound
is not a hard bound on the field: a body node inside a feedback loop gets its
short round note (`; feedback round k/N`) appended *after* the cap, so such a
`detail` runs a few tens of runes over 240. Size `detail` as "short, bounded
by roughly 300 runes", never as "exactly ≤ 240" — and rely on the 1 MiB
per-line cap below for the actual hard limit. Zero/empty values are
**omitted** from the JSON. Under schema 3, treat absent `cost_unknown` as false
and absent token members as zero; never infer that absent `cost_usd` means a
known zero when `cost_unknown` is true — under Codex that is every paid
invocation of the run, so a consumer summing `cost_usd` over such a stream
totals zero dollars for work that was paid for. A gate spawns no subprocess, so its
`node_passed` carries no cost, usage, or session.

`node_passed` additionally carries `provenance`: **how** the PASS was reached
(ADR 0016 §6), one of a closed set of four —

| `provenance` | the PASS was reached by |
|---|---|
| `verified` | a `success_check.verify` command the ENGINE ran and whose exit code — and `output_matches`, when declared — it judged. |
| `self-reported` | `result_matches` — the node emitted the right words. Nothing outside the node observed anything. |
| `exit-only` | a subprocess ran and exited 0, with no predicate over what it did. |
| `approved` | a human approved a `type: gate` node — no subprocess, and the strongest provenance in the set. |

It reports **provenance, never adequacy**: `verified` says the engine gathered
the evidence, not that the evidence was sufficient. It is an optional additive
field, **no schema bump** — absent means the run predates it (or, on
`node_failed`, that it never applies: only a PASS has a provenance). A consumer
that has never heard of it parses these events exactly as before.

Node events emitted by a feedback re-execution (ADR 0010) additionally carry
`round`, the 1-based feedback round the execution belongs to — an optional
additive field, **no schema bump**; absent means 0, the initial pass. Body
nodes therefore emit one full `node_started` → terminal sequence per round
*within* a leg. The declarer's non-final judgment failure is a
`node_retried`, never a `node_failed` — `node_failed` stays terminal and
appears at most once per declarer per leg, when the failure is final (its
`detail` then names the spend: `feedback exhausted after 2 rounds of impl →
review: …`).

On `node_started` and `node_retried`, `session_id` appears when the selected
runtime owns it. Claude pre-assigns a UUID and publishes it before spawn; Codex
publishes the `thread.started` id after spawn. A gate has none. A
session-handoff node resumes its parent's id. Each cold retry gets a fresh id,
so `node_retried` supersedes the failed attempt's transcript/thread reference.
The in-repo transcript-tail consumer currently understands Claude's
`~/.claude/projects` layout; Codex session ids remain useful for CLI resume and
external consumers even though that live tail is unavailable.

The gate decision events (`gate_paused`, `gate_approved`, `gate_rejected`,
added in schema **2**) mark the decision itself; an approved/rejected gate
still gets its terminal `node_passed`/`node_failed` immediately after, so a
consumer that only understands node events keeps working — with the one
exception that a **paused** gate gets no node terminal at all, which is why
the liveness rule above counts `gate_paused` as terminating the node's turn.
`gate_paused` is
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
- **Two `run_started`s per auto run, and one `run_finished` closing them**
  (ADR 0023). An `auto` run's stream opens with
  `run_started {phase:"planning"}` while its planner call runs, and the plan
  committing is marked by the ordinary untagged `run_started` — same leg, same
  lock, no `run_finished` between them. Three consequences a consumer must
  know, because the `phase` field disambiguates only the first:
    - **Count legs by `run_started` events with NO `phase`.** A reader that
      counts every `run_started` now counts one extra per auto run.
    - **A committed auto run has one close for two opens.** A consumer keeping
      a stack of legs must know the planning open is closed by the untagged
      open, not by a `run_finished` of its own. `run_finished` carries no
      `phase` and gains none: on the committed path the close that ends a
      planning phase is not a `run_finished` at all.
    - **`run_finished {outcome:"failed"}` with ZERO node events** is a shape no
      stream had before: a refused plan. A consumer that assumes a failed leg
      names a failed node has to stop assuming it.

  Two shapes are NOT legs and oh-my-graph's own readers decline to count them,
  which a consumer matching its views should do too: a run directory whose
  stream is absent or empty has said nothing at all — it exists because the run
  took its lock, an instant before its first event — and a `run_finished` with
  no `run_started` before it is damage rather than a closed leg. Neither has a
  status; oh-my-graph renders both as "not known yet" rather than as failed.

  A reader that simply ignores the unknown field still improves: it sees a leg
  that opens at planning and closes once, so "the last leg is open" reports the
  run in flight for the whole planner call, where it previously saw no run
  directory at all. That is a less precise answer than distinguishing planning
  from running, not a wrong one — which is why this is an additive optional
  field and **`schema` stays 2**.
- **Short lines.** Every event line the writer emits is small (a handful of
  short fields; well under a few kilobytes even with a long `detail`). The
  in-repo readers enforce a shared 1 MiB per-line cap and refuse — with an
  error, never a silent truncation — a stream whose line exceeds it, treating
  it as corrupt or foreign; an external consumer may assume the same bound.

## Liveness — `resume.lock` (ADR 0015)

`events.jsonl` says a leg *started*; it cannot say whether that leg's process
is still alive, because a killed process writes no `run_finished`. The run
directory's `resume.lock` answers that, and it is documented here — rather than
left internal — because oh-my-graph's own read-back commands consult it, and
this document promises they read what any consumer can read, with no side
channel.

- **A leg holds an exclusive `flock(2)` on `resume.lock` for its whole
  duration**, taken before it writes its first event and still held after its
  last. The kernel releases it when the holder dies, however it dies. (True on
  darwin and linux, the platforms with `flock(2)` — the only ones the two
  promises below hold on. Elsewhere a build-tagged fallback keeps the pre-ADR-0015
  scheme, where the file's *existence* is the lock and release removes it; a
  consumer there gets nothing from this section and should use the open-leg rule
  unconditionally, which is what the "unknown" arm below already prescribes.)
- **A consumer probes with a SHARED lock (`LOCK_SH|LOCK_NB`) on a read-only
  fd**, and unlocks immediately. A shared probe conflicts with the holder's
  exclusive lock — which is the question — but not with other probes, so
  observers never block each other or flicker one another into a false
  "alive". **Never probe with an exclusive lock**: that would briefly block
  the very leg you are observing. A reader creates, writes and removes
  nothing.
- **Beside an OPEN leg** (the stream's last `run_started` has no
  `run_finished` after it): a *contended* probe means the writer is alive —
  the run is in flight; a *succeeding* probe means the writer is gone — the
  run is **abandoned**, and no event will ever be appended to it. Beside a
  closed leg the lock says nothing and is not worth probing.
- **A PLANNING phase holds the lock like any other leg** (ADR 0023). It takes
  the flock before writing its `run_started {phase:"planning"}` and still holds
  it after its last event, so everything above applies unchanged: a planner call
  in progress is in flight, and one whose process died is abandoned, cleaned up
  by the kernel with no repair path. There is no second liveness mechanism, and
  a consumer needs no new rule — only the `phase` field, if it wants to tell
  "thinking about a graph" from "running one".
- **A missing file, a filesystem whose `flock` is not the kernel's own (linux
  emulates it over NFS as POSIX record locks), and any probe error all mean
  *unknown*, and unknown means the open-leg rule** — the answer this tool gave
  before ADR 0015, and a safe one. It is also what a consumer that cannot or
  will not `flock` should use unconditionally. Nothing is ever concluded
  abandoned from the absence of evidence.
- **The marker is the file's first line, and it is exactly
  `oh-my-graph-lock 1`** — a bare version-tagged token, no trailing fields,
  terminated by a newline. It is stated here as a literal because the whole
  section forks on it: a consumer that cannot tell a marked file from an
  unmarked one cannot implement either rule. Everything after that line is
  internal (see below). The `1` is the *lock file's* own format version, not
  either file schema, and it is what a future format would bump; a first line
  that is neither this literal nor absent is not a marked file, and takes the
  unmarked arm.
- **A lock file whose first line is not the marker predates this contract, and
  resolves under a different, weaker rule.** It was written by a binary that
  took no `flock` at all, so probing it says nothing about its writer. What it
  does carry is one line holding that writer's pid, and oh-my-graph reads that
  pid in **one direction only**: a pid that names no process at all
  (`kill(pid, 0)` → `ESRCH`) means the leg has exited, so beside an open leg
  the run reads **abandoned**; a pid that names some process proves nothing (it
  may be the holder, it may be an unrelated process that inherited a recycled
  pid — exactly what was measured) and is *unknown*; an unreadable or
  malformed pid is *unknown*. This rule exists only so runs abandoned before
  the upgrade are diagnosable at all, and it self-expires — the next leg to
  take such a run's lock writes a marked file, after which the `flock` alone
  decides forever. **That next leg needs a human, though**: oh-my-graph's own
  acquire path still refuses an unmarked lock under pre-`flock` semantics
  (existence is the lock), naming the file to delete, so the sequence on such a
  run is *derive abandoned → the offered `resume` is refused → delete the named
  file → resume again*. Nothing expires on its own timer, and a consumer must
  not read "self-expires" as "will eventually stop being unmarked without
  anyone acting". **A consumer is free not to implement it:** treating every
  unmarked file as *unknown* is always safe, and differs only in reporting a
  pre-upgrade corpse as in flight.

The file's *contents* carry exactly one promise: the marker line. In a file
that **has** it, everything after — the holder's pid — is explicitly
informational and explicitly **not a liveness test**: a pid in a lock file was
measured being recycled by an unrelated process, reading "alive" for hours.
That measurement is also why the pre-marker rule above reads a pid only in the
one direction recycling cannot corrupt. The file is **never removed** (on the
`flock` platforms; the fallback noted above is the exception, and there its
presence is the whole signal), so its presence carries no information either; it
is a handle, not a flag.

Two things this deliberately does not do: no reader ever repairs the stream by
appending a terminal event on a dead writer's behalf (the history stays exactly
what the schedulers wrote), and no new event type, field or verdict is
introduced anywhere. A consumer that has never heard of "abandoned" reads the
same bytes and derives what it always did.

Finally, the honest caveat about what a free lock does and does not prove: it
proves the *oh-my-graph* leg is gone, not that its children are. The engine
spawns each selected model CLI in its own process group, so a death that took only the
engine can leave a subprocess still running and still spending. Recovering such
a run (`oh-my-graph resume <run-id> --retry-failed`) may therefore run a node
alongside its own orphan; oh-my-graph's surfaces warn about this rather than
probing for it.

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

Anything in the run directory not listed here (temp files) is internal and
carries no compatibility promise. `resume.lock` is listed here — see
"Liveness" — and carries exactly the two promises stated there (an exclusive
`flock` for a leg's duration, and the format marker as its first line);
everything else about it, its pid line included, remains internal. The
pre-marker rule under "Liveness" is not a third promise: it describes lock
files already on disk, written before this contract existed, and promises
nothing about the ones this binary writes.
