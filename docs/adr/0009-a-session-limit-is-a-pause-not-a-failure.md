# ADR 0009 — A subscription session limit is a pause, not a failure

- Status: Accepted
- Date: 2026-08-02
- **Amended in place on 2026-09-02 (#222): the "Scope" section below is
  revised, and only it.** A Codex usage limit is now the same pause. Nothing in
  the Decision moved — what changed is one condition on
  `NodeOutcome.SessionLimited`, not one line of the pause it triggers. See
  "Amendment — 2026-09-02" at the end of Scope.

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

> **Amendment — 2026-09-02, closing #222: the pause carries to Codex, and the
> Scope above is wrong about why it could not.**
>
> It is left as written — a record that edits away what it decided teaches a
> future reader nothing (the convention ADR 0007 states). Two of its factual
> claims are now false. Its conclusion is not:
>
> - *"there is nothing for another runtime's message to match"* — there is.
>   `codex exec --json` reports its own limit as prose, recorded byte for byte
>   in `internal/runner/testdata/codex-usage-limit.jsonl` from run
>   `20260901-171816.016378000-1` on 2026-09-02: `You've hit your usage limit.
>   Upgrade to Plus to continue using Codex (https://chatgpt.com/explore/plus),
>   or try again at Sep 13th, 2026 10:04 PM.` (First written down with the
>   message elided; recovered in full from that run's own `state.json` and
>   `events.jsonl`, which stored the sentence as a `FailureCause` — and
>   `codex_protocol.go` builds a `FailureCause` from `turn.failed`'s
>   `error.message` and nothing else.)
> - *"removing that gate … would produce a pause that can never fire"* — it
>   fires. The gate was removed **and** a matcher added; the sentence only
>   considered the first half.
> - *"a second runtime does **not** owe a session-limit signal"* — **still true,
>   and kept.** #171's settlement is not reopened: this is a runtime
>   volunteering what it does not owe. A third runtime with no limit wording to
>   match implements `cliProtocol.isLimitCause` returning false and degrades
>   exactly as mitigation 2 above specifies.
>
> **The revisit condition this ADR wrote for itself was NOT met.** It reads:
>
> > Should a future runtime expose a **structured** limit signal, that is a
> > reason to revisit — and it would be a better foundation than this one, since
> > the whole mitigation list below exists to survive matching prose.
>
> Codex exposes no such thing, and #222's title — *"the one runtime whose limit
> signal is actually structured"* — is the belief this amendment corrects. There
> is a typed field (`turn.failed`'s `error.message`), but the type does no
> deciding: `turn.failed` is Codex's ONE terminal-failure record, and `"model
> unavailable"` (`internal/runner/codex_protocol_test.go`) and a stubbed refusal
> (`cmd/oh-my-graph/loadeduserconfig_cli_test.go`) arrive in exactly that shape
> carrying nothing but a different sentence. The field is typed; the value is
> prose. If anything Codex's signal is the **weaker** of the two — Claude at
> least wraps its limit in an `is_error` envelope, so Codex has one layer fewer,
> not one more. What actually changed is smaller and duller than a structured
> signal: the prose matching turned out to port.
>
> *(One loose end, recorded rather than acted on, 2026-09-02.* Codex does emit
> an enumerated `codex_error_info: "usage_limit_exceeded"` beside that message —
> in this machine's own records, at
> `~/.codex/sessions/2026/09/02/rollout-2026-09-02T02-18-16-01a05dfa-8c96-73a0-88ad-8cb71b780bc8.jsonl:12`,
> the rollout for the very session the limited node ran under (`node_started`'s
> `session_id` in that run's `events.jsonl`), and as `"other"` on an ordinary
> failure at `.../2026/08/14/rollout-2026-08-14T22-03-25-…jsonl:9`. **That is
> not evidence about the stream this engine parses.** The rollout is Codex's own
> on-disk session log, where the record is an `event_msg` payload of type
> `task_complete`; it holds no `turn.failed` record at all. Whether the field
> also rides `codex exec --json` **stdout**, which is the only surface
> `codex_protocol.go` reads, is **UNVERIFIED** — no stdout capture on disk
> records the key set of a limit record, and this amendment did not spawn Codex
> to find out. Nothing is built on the field either way; matching stays on the
> prose.)*
>
> **What is matched.** `(?i)hit your usage limit`, substring, against
> `NodeOutcome.FailureCause` — in `internal/runner/sessionlimit.go`, the one
> file mitigation 1 names, beside Claude's pattern and deliberately **not**
> folded into an alternation with it: a rewording on one runtime must not widen
> what the other matches. The plan name (*"Upgrade to Plus"*) and the reset date
> are left out of the pattern on purpose — both vary per account, and every
> extra word is another way a reworded message stops matching. The
> classification is still ONE call site (`CLIRunner.Run`), and it now names no
> runtime at all: `cliProtocol` grew an `isLimitCause` method, so the protocol
> that decoded the output is the one asked whether its own output is a limit.
>
> **The reset timestamp is NOT parsed into a clock, and Codex's
> more-machine-readable-looking one changes nothing.** `SessionLimitReset`
> returns `Sep 13th, 2026 10:04 PM` as the prose the CLI printed, exactly as it
> returns `5:20pm`. It carries no timezone, and a wrongly parsed instant is
> worse than none — this ADR already refused to sleep on the weaker `5:20pm` for
> that reason ("Alternatives considered"). A typed field around a value does not
> make the value typed.
>
> **The engine prints Codex's reset time, in Codex's own words.** The hint takes
> the same with-a-time branch a Claude limit takes: `Session limit reached
> (resets Sep 13th, 2026 10:04 PM). Resume after Sep 13th, 2026 10:04 PM with:
> oh-my-graph resume <run-id> --retry-failed`.
>
> *(Corrected 2026-09-02, hours after this amendment was first written. It said
> the opposite — that the engine prints no Codex reset time, because the `try
> again at …` sentence arrives only in a leading `{"type":"error", …}` record
> the parser does not decode, and the `turn.failed` the engine sees "is not
> known to carry the time". The hedge was honest and the conclusion was wrong:
> the capture the claim rested on had that record's message ELIDED after
> `usage limit.`, so nobody had looked. Run
> `20260901-171816.016378000-1` had: its `FailureCause` — which
> `codex_protocol.go` fills from `turn.failed`'s `error.message` and nothing
> else — is the whole sentence, reset clause included. The fixture is now
> unelided and `TestSessionLimitReset_CarriesCodexProseUntouched` asserts the
> time out of the parsed cause, not just out of the undecoded record.)*
>
> **Everything in the Decision below is unchanged, and is now asserted from both
> ends.** No ledger row, no snapshot record, no retry, drain-don't-cancel, a
> limit outranking continue-on-fail pruning, exit code 2 with the resume hint,
> the leg closing as `run_finished` outcome `"paused"` — every one of them runs
> against BOTH runtimes' scripted outcomes in
> `internal/schedule/sessionlimit_test.go`, plus `--runtime codex run` end to end
> against a shell stub (no `codex` spawned) in
> `cmd/oh-my-graph/sessionlimit_test.go`. The scheduler names no `Runtime`
> anywhere, which is why this was one condition and not one mechanism; the
> two-runtime table is what turns that blindness from an assumption into a test.
>
> **One sentence above went stale with this amendment, and is now corrected in
> the tree:** the claim that *"the pre-run disclosure names the absence"*. There
> is no absence left to name. `cmd/oh-my-graph/main.go` had gone on printing
> `No session-limit pause: ADR 0009's resumable pause is Claude-only …` before a
> Codex run that would in fact pause — printed, in the pre-fix run
> `20260901-171816.016378000-1`, by the same binary that would later print
> `⏸ … session limit reached — pausing run`. It now discloses the pause instead,
> and says in the same breath that detection is prose on both runtimes, since a
> line promising the pause without its brittleness would be the next stale
> sentence. The two tests pinned to the old wording
> (`cmd/oh-my-graph/wiring_test.go`, `cmd/oh-my-graph/planonly_test.go`) assert
> the new text and keep the old as a negative.
>
> `docs/EXAMPLES.md`, `DESIGN.md` (three passages), `README.md` and
> `docs/LIMITATIONS.md` were corrected with this amendment;
> `internal/schedule/errors.go`'s `LimitPausedError` docstring, which called the
> subprocess a claude one, followed with the disclosure.

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
