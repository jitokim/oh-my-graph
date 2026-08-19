# ADR 0031 — An unbounded loop is a bounded loop and a clock

- Status: **Proposed.** Nothing in §4 is implemented. This record exists to be
  argued with before any of it is built.
- Date: 2026-08-20

## 1. Context

The operator runs a loop that has never been a graph:

```
issues / backlog / roadmap  ─▶  pick one  ─▶  plan  ─▶  build, test, review, PR
        ▲                                                        │
        └──── file what was found ◀── rebuild ◀── merge when approved
```

Most of it already ships. `merge-shepherd.yaml` opens by naming itself *"the
operator's PR-shipping loop as a graph — what this replaces: the loop the human
operator runs by hand on every PR"*, and `dev-review-pr` / `self-dev` /
`gated-lane` are its middle. What has never been a graph is the two **ends** —
choosing what to work on, and harvesting what the work discovered — and the
**revolution** that joins them.

On 2026-08-20 the operator ran that outer loop by hand for a full session,
including hand-running the steps `merge-shepherd` exists to run, across three
PRs. That is the motivating observation: not that the loop is unwritten, but
that a loop which *is* written goes unused when nothing revolves it.

### 1.1 What already exists, and is not being re-litigated

| Piece | Where | What it settles |
|---|---|---|
| A session limit is a **pause**, not a failure | ADR 0009 | the run drains, records the limited node nowhere, and is resumable |
| A goal loop is **bounded** | ADR 0011 | `--max-cycles`, `--max-goal-budget-usd`; a loop that "kept going sensibly" is the quiet-spend failure |
| A human pause is a **gate** | ADR 0003 / ADR 0014 | exit 2, `resume --approve` / `--reject` |
| Interrupted work is **resumable** | `internal/runstate`, ADR 0015 | snapshot + lock; a leg holds an flock |
| A run's state is **observable** | `internal/runfeed`, `internal/runstatus`, `serve` | events.jsonl, the one settled/in-flight/abandoned rule |

This ADR adds a supervisor over those. It does not change any of them.

### 1.2 The three gaps, measured

**(a) The two pauses are indistinguishable from outside.** Both a gate pause
and a session-limit pause return **exit code 2** (`cmd/oh-my-graph/main.go`,
`exitCodeForError` — `*schedule.PausedError` → 2, `*schedule.LimitPausedError`
→ 2). They are opposite situations: one waits for a **human**, the other waits
for a **clock**.

The only signal that separates them today is an **absence**: a gate pause
writes `gate.paused_at` into `state.json` (`internal/runstate/recorder.go:119`)
and a limit pause writes nothing at all, because ADR 0009 deliberately records
the limited node nowhere. So "this is a limit pause" is currently expressed as
"no gate is recorded" — an assertion satisfiable by a record simply being
missing, which is the exact anti-pattern CONTRIBUTING.md forbids in tests:

> an assertion must not be satisfiable by a node or record simply being absent
> — state presence explicitly.

**(b) Nothing resumes a limited run.** ADR 0009 stops at *"a later `resume`
should pick up exactly where it stopped."* The later `resume` is a human. The
reset hint the CLI prints is carried as **prose** and deliberately never parsed
(`internal/runner/sessionlimit.go`, `SessionLimitReset`: *"never parsed into a
real clock time, because the CLI's wording, timezone and format are its
own"*). That caution is right and this ADR keeps it — see §4.3.

**(c) A budget cap cannot see into a nested run.** `budget_usd` is compared
against the node's own session cost (`internal/schedule/scheduler.go:1658`,
`outcome.TotalCostUSD`), *after* the node finishes. A node that spawns
`oh-my-graph auto` therefore has no enforced cap on what it spawns, and the
check is a post-hoc tripwire rather than a throttle.

### 1.3 The currency is not dollars

Every node runs on the user's own **subscription** (ADR 0001), so the binding
constraint on a long-running loop is not money — it is the plan's limit window.
`budget_usd` is a proxy borrowed from metered-API thinking; it is useful as a
runaway tripwire and misleading as a plan. A supervisor that budgets in dollars
is measuring the wrong quantity: the real question is *how many windows am I
willing to spend, and what happens at the end of one.*

## 2. The requirement, in the words it arrived in

> 결국 무한루프를 감독, 무한루프의 의도적 이터레이션 제한, 요금제 리밋 도달 시
> pause 상태였다가 토큰 리셋되면 재개 같은 장치가 필요한거잖아?

Three devices: **supervision** of a loop that conceptually never ends, a
**deliberate** bound on its iterations, and **pause-on-limit / resume-on-reset**.

## 3. Decision

**A supervisor is a leg that runs revolutions of a graph, bounded up front, and
whose only autonomous recovery is waiting.**

Five decisions, each of which can be argued with separately.

### 3.1 The supervisor is a leg, not a node type and not a shell loop

Adding `oh-my-graph loop <graph.yaml>` as a fourth executing leg (beside `run`,
`auto`, `resume`).

Rejected — **a node that re-invokes the graph.** Recursion puts the bound
inside the thing being bounded, which is how a loop becomes unbounded by
editing one prompt.

Rejected — **`while :; do oh-my-graph run …; done` in the operator's shell.**
It is free and it works, and it is what should be used until this is built. But
it cannot do the two things this ADR exists for: it cannot tell a gate pause
from a limit pause (§1.2a), and it produces **no observable state** — a
sleeping shell loop and a dead one look identical, which is the failure this
repository already paid for once when a stale `serve` made a healthy run look
idle (#204). A supervisor that cannot be watched is not supervision.

### 3.2 Every bound is declared, and there is no unbounded default

`loop` requires an explicit stop condition and refuses without one — the shape
ADR 0030 established for `auto` and build evidence (refuse, exit 3, name the
flag that opts out). At least one of:

- `--revolutions N` — how many times round.
- `--until <RFC3339>` — wall-clock stop; the natural bound when the currency is
  a reset window (§1.3).

And unconditionally:

- **A gate stops the supervisor.** See §3.4.
- `--max-waits N` — how many limit windows it is willing to sit out (§3.3),
  defaulting low.

There is deliberately no `--forever`. "무한루프" is the *intent*; the
implementation is a bound the operator re-affirms with the previous
revolution's ledger in front of them. ADR 0011 exists because the alternative
is quiet spend, and a supervisor is that failure mode with a longer lever.

### 3.3 A limit pause is waited out by **polling, never by scheduling**

On exit 2 + a recorded limit pause, the supervisor sleeps a fixed interval and
issues `resume --retry-failed`. If the limit is still in force the run pauses
again and the supervisor sleeps again, up to `--max-waits`.

**It does not read the reset time.** `SessionLimitReset`'s prose ("5:20pm") is
a hint for a human and stays one: it has no date, no timezone, no locale
guarantee, and ADR 0009 already documents that the wording is the CLI's own and
may change. Polling is strictly more robust than parsing — it survives a
wording change that would silently break a scheduler, and its failure mode is
"woke up too early, paused again, slept again" rather than "slept until the
wrong hour."

This is the same reasoning ADR 0009 used to justify string matching as the
honest option, applied one layer out: *do not build a clock on top of prose.*

### 3.4 A gate stops the supervisor, and the supervisor never approves

The authority boundary, stated so it cannot drift: **a supervisor may wait, and
may retry. It may not approve.**

A gate exists because a human decided something needs a human. A supervisor
that approved gates would be granting itself the authority the gate was created
to withhold — and it would do so most eagerly exactly when the operator is
asleep, which is when supervision runs.

So on a gate pause the supervisor stops, reports the resume command, and exits.
Its exit code is the paused run's own 2: the revolution is unfinished and
resumable, which is true.

### 3.5 A limit pause is recorded, so nothing has to infer it from absence

`state.json` gains an explicit limit-pause record (the node ids, the captured
cause, when). This fixes §1.2a at the source rather than teaching every consumer
the same inference, and it is what lets `runstatus` name a third state.

**This is a change to ADR 0009's "recorded nowhere", and the narrowest possible
one.** ADR 0009's rule is about the *node*: a limited node must not be recorded
as FAILED or as completed, because it never ran and must re-launch on resume.
That stays exactly as it is. What is added is a record of the **pause**, beside
the gate pause that is already recorded — a different object, at the run level.

### 3.6 Waiting must be visible, because idle and dead look the same

A supervisor asleep between revolutions, a supervisor sitting out a limit
window, and a supervisor that died are three states that all present as
"nothing is happening." This repository has already lost a debugging session to
exactly that ambiguity (#204: a stale `serve` made a running lane look idle, and
the maintainer asked "why is in-flight 0"). `runstatus` is the one place the
settled/in-flight/abandoned rule lives (ADR 0015); the waiting state belongs
there and on the dashboard card, named — not inferred from a card that stopped
updating.

## 4. What this does not decide

- **The nesting question (§1.2c).** Whether a node may spawn `oh-my-graph` at
  all is a separate decision with its own hazard, and a supervisor removes most
  of the motivation for it: `loop` runs revolutions from the outside, so the
  graph does not need a node that runs the engine. If nesting is wanted anyway,
  it needs its own record and must answer the cap question.
- **What the loop graph contains.** `maintainer-loop`'s node list is a graph
  authoring question, not an engine one.
- **Auto-merge.** Out of scope by §3.4.

## 5. Consequences

**Good.** The operator's outer loop becomes a thing that runs unattended
overnight with a bound, and stops in exactly two situations that both deserve a
human: a gate, and a bound reached. A limit window costs a wait instead of a
dead run and a lost cycle. The three-way idle ambiguity gets a name.

**Costs.** A fourth executing leg is real surface: `loop` needs the run lock
discipline every leg has (ADR 0015), its own resume semantics, and its own
tests. §3.5 touches `state.json`, which is a schema change with a version bump
and a resume-compatibility obligation. And a supervisor makes it *easier* to
spend a whole window without a human present — which is why §3.2 refuses to run
without a declared bound rather than defaulting to one.

**The honest risk.** The value of this rests on an unmeasured premise: that the
loop's chosen work is worth doing unattended. Today's evidence is one hand-run
session. If `select` picks badly, a supervisor turns one bad choice into N.
That is what §6 measures before this moves past Proposed.

## 6. Falsification — what would make this wrong

1. **`select` quality.** Run the survey/select half alone (no `work` node) for
   ≥10 revolutions and compare its choice against what the operator would have
   picked. If it disagrees materially more than ~2 in 10, the supervisor is
   amplifying a bad chooser and §3 is premature. **This is cheap and gated on
   nothing** — no engine change is needed to measure it.
2. **Limit-pause frequency.** If, over a month of dogfooding, no run ever pauses
   on a limit, §3.3 is machinery for a situation that does not arise, and the
   simple shell loop of §3.1 is the right answer after all. Count them: the
   record added by §3.5 is what makes them countable, so this measurement
   cannot begin until that lands.
3. **Polling cost.** If waking to `resume --retry-failed` inside a still-closed
   window itself consumes the plan meaningfully, polling is the wrong device and
   the prose has to be parsed after all.

## 7. References

- ADR 0001 — subprocess, not SDK (why the currency is a subscription)
- ADR 0003 / ADR 0014 — the gate, and resuming one
- ADR 0009 — a session limit is a pause, not a failure
- ADR 0011 — the bounded goal loop
- ADR 0015 — the run lock and the one run-status rule
- ADR 0030 — refusing rather than defaulting, and exit 3
- `graphs/merge-shepherd.yaml` — the tail of this loop, already a graph
- #204 — idle and dead looking the same
