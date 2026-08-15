# ADR 0009 — A subscription session limit is a pause, not a failure

- Status: Accepted
- Date: 2026-08-02

## Context

oh-my-graph runs every node on the user's own logged-in claude subscription —
that is the whole point of the tool (ADR 0001). Subscriptions have session
limits, and a batch that is wide or long enough will hit one: the CLI's
request is refused (HTTP 429 underneath) and the subprocess dies printing an
error envelope whose message reads, verbatim as observed on claude 2.1.220:

```
You've hit your session limit · resets 5:20pm
```

Until now the engine treated this like any other non-zero exit: the node
FAILED, halt-on-fail cancelled its in-flight siblings, and the run reported
itself broken. Every part of that is a misreading. The node did nothing wrong
— its prompt never ran. The failure is not final — the limit resets at a
known time. And killing the siblings throws away work that was already paid
for. Repeated batch kills from exactly this shape are what motivated this
ADR. The failure-cause surfacing (#64) already carries the CLI's message into
`NodeOutcome.FailureCause`; this ADR makes the engine act on it.

**Detection is string matching, and that is the honest option.** The CLI
offers no structured signal for a session limit — unlike a `--max-budget-usd`
abort, which has the `error_max_budget_usd` envelope subtype, the limit
arrives only as prose (an `is_error` envelope whose `result` carries the
message, or a stderr tail). Matching prose is brittle: a wording change in
the CLI silently stops the match. The design accepts that with two
mitigations rather than pretending otherwise:

1. **The matcher lives in exactly one place** —
   `internal/runner/sessionlimit.go` — pinned by a test carrying the real
   message shape, so a CLI wording change is a one-line fix with a failing
   test to point at it, not a hunt across packages.
2. **A missed match degrades safely.** An unrecognized limit falls back to
   the old behaviour — the node FAILs with the message in its detail — and
   `resume <run-id> --retry-failed` (the same command the pause path
   recommends) still salvages the run. The brittleness costs convenience,
   never correctness.

## Decision

**A session limit pauses the run the way a gate does** (ADR 0003), because it
is the same situation: the run cannot usefully continue right now, nothing is
broken, and a later `resume` should pick up exactly where it stopped.

**Scope — this is a promise of the CLAUDE runtime, not of the engine**
(settled 2026-08-15, closing #171; the question did not exist when this ADR was
written, because there was only one runtime). ADR 0025 later made the runtime
selectable, and the pause does not carry across:

- the detection above is prose matching against **Claude's** wording, so there
  is nothing for another runtime's message to match;
- `CLIRunner` gates the classification on `RuntimeClaude`, which is the second
  layer, not the cause — removing that gate would not produce a pause under
  Codex, it would produce a pause that can never fire, which is worse than an
  absence somebody wrote down.

So a second runtime does **not** owe a session-limit signal, and adding one is
not a prerequisite for adding a runtime. What a runtime owes is the honest
degradation this ADR already specifies below: the node FAILs carrying the
message, and `resume --retry-failed` salvages the run. The cost of that
narrowing is real and is stated where a user meets it — the pre-run disclosure
names the absence, and `docs/LIMITATIONS.md` carries the long form.

Should a future runtime expose a **structured** limit signal, that is a reason
to revisit — and it would be a better foundation than this one, since the whole
mitigation list below exists to survive matching prose.

- **The runner classifies.** `CLIRunner.Run` sets
  `NodeOutcome.SessionLimited` when the captured `FailureCause` (envelope
  error report, else stderr tail) matches the limit's message shape — the
  same typed-flag pattern `BudgetExhausted` established, and the matcher's
  only call site.
- **The limited node is recorded nowhere.** No ledger row, no snapshot
  record, no terminal event, no retry attempt (an immediate re-spawn meets
  the same limit). It mirrors how a gate pause records the paused gate: the
  node is un-run, not FAILED — which is precisely what makes
  `resume --retry-failed` re-launch it later, since it is in neither the
  completed nor the settled seed set.
- **The scheduler drains.** On the first `SessionLimited` outcome the run
  stops launching new work but lets in-flight siblings finish — never
  cancels them. A draining sibling may itself hit the limit; it then joins
  the paused set rather than failing. A limit outranks continue-on-fail
  pruning (the run is unfinished-and-resumable, not finished-and-failed; the
  retained FAIL records still tell the failures' story). A gate pause
  outranks a limit — the gate needs its human decision first, and the gate
  leg re-runs the un-recorded limited nodes anyway. Unlike a gate pause,
  no `RecordPause` is needed: every settled node was already persisted per
  node, and the limited nodes' absence from the snapshot *is* their state.
- **The run exits resumable.** `Scheduler.Run` returns `*LimitPausedError`;
  `cmd/oh-my-graph` maps it to **exit code 2** — the existing "paused and
  resumable" code — and prints the resume hint. The hint parses the
  message's `resets <time>` best-effort and prints
  `Resume after 5:20pm with: oh-my-graph resume <run-id> --retry-failed`;
  when the time cannot be extracted the hint prints without one rather than
  inventing a clock time from prose the CLI owns.
- **`resume --retry-failed` finishes the run.** A limit-paused run may hold
  zero FAILED records (only PASSes plus never-run nodes), so the retry mode
  now also launches when there is un-recorded, launchable work — "running
  unfinished nodes" — while a run with genuinely nothing to launch (all
  passed, or blocked behind a rejected gate's standing decision) still
  exits 0 with no leg, and a gate-paused run is still redirected to
  `--approve`/`--reject`.
- **The event stream stays schema-stable.** The leg closes with the existing
  `run_finished` outcome `"paused"`; a `detail` field — already defined on
  the event shape, previously unused on `run_finished` — names the limited
  nodes and cause. Additive under docs/RUN-FEED.md's compatibility rule: no
  new event type, no new field, no schema bump.

## Consequences

**Positive**

- A batch that hits the limit stops cleanly with everything already earned
  persisted, and one copy-pasteable command (with the reset time when known)
  finishes it later. This converts the most common overnight batch death
  from "re-run and re-pay" into "resume".
- Draining preserves in-flight siblings' work exactly as a gate pause does,
  and siblings that also limit join the pause instead of racing to fail.
- Exit code 2 keeps its single meaning — resumable pause — so scripts
  watching for it need no change.

**Negative / trade-offs**

- Prose matching will eventually miss a reworded message. Accepted: the
  degradation is the pre-ADR behaviour, and the fix is one pinned pattern.
- A limit that arrives with *no parseable envelope at all* (rare; the
  observed shape is an error envelope) surfaces as a runner error and still
  fails the node — same safe degradation.
- `--retry-failed` gains a second meaning ("finish unfinished work", not
  only "retry failures"). Accepted over a new flag: the operator story is
  one command that salvages a stopped run, and the exit hint promises
  exactly it.
- Nothing in `state.json` says "limit-paused"; the signal lives in the exit
  code, the hint, and the stream's `run_finished` detail. A consumer wanting
  it durable reads events.jsonl — the file that exists for history.

## Alternatives considered

- **A new retry cause (`retry: { on: [session_limit] }`).** Rejected:
  retrying into a standing limit spends nothing but achieves nothing; the
  limit resets on the clock, not on attempts.
- **Sleep in-process until the reset time.** Rejected for the same reason a
  gate does not block (ADR 0003): oh-my-graph is a stateless CLI, not a
  daemon, and the parse of "5:20pm" is far too weak to sleep on.
- **Mark the node FAILED but exit 2.** Rejected: a FAIL record is a verdict
  about the node, and it would be false — and `--retry-failed`'s
  clear-FAILED semantics would conflate real failures with limit victims in
  every surface that renders verdicts.
- **A new event type (`node_limited`) or outcome token.** Rejected: the
  event-type set is closed per schema version, so this forces a schema bump
  on every consumer for what one additive `detail` field already conveys.
