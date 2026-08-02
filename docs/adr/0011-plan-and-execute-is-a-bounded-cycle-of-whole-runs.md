# ADR 0011 — Plan-and-execute: goal iteration is a bounded cycle of whole runs, each replanned and revalidated

- Status: Proposed
- Date: 2026-08-02

## Context

`auto` plans **once**. `coordinator.Plan` makes exactly one planner call, the
reply is validated (`validatePlannedNodes` + the ADR 0004 tool ceiling),
`planAndExecute` prints the topology and hands the graph to the scheduler —
and that is the end of the coordinator's involvement. Whatever the run
produces, nobody looks at it and asks the only question the goal ever posed:
*is the goal met, and if not, what remains?*

A real orchestrator closes that loop: plan, execute, **observe the result,
re-plan** — until the goal is met or governance stops it. Today the human is
that loop: read the ledger, read the artifacts, type a sharper goal, run
`auto` again. The machinery for everything *inside* one turn of the loop
already exists and is hardened:

- **Planning and validation** — `coordinator.Plan` treats the planner's reply
  as untrusted, parses it through the same `graph.Parse` as hand-written YAML,
  and enforces the field-disposition table plus the layered tool ceiling
  (ADR 0004) before anything spends.
- **Execution** — `planAndExecute` is the one home of the plan→save→print→
  execute sequence, shared verbatim by `auto` and a chat graph turn; chat adds
  the one permitted divergence, a `Run this plan? [y/N]` confirm hook between
  printing and executing.
- **Node-level iteration** — ADR 0010's feedback edges already let a planned
  graph loop a review segment *within* itself, bounded by a required `max`
  (and capped at `maxPlannedFeedbackRounds` = 3 for planned graphs).
- **Observation material** — the run ledger prices every execution, the
  snapshot records every verdict with its detail, and artifacts persist every
  node's result. The evidence an assessor needs is already engine-produced.

What is missing is only the turn of the loop itself: an assessment step that
reads engine-produced evidence, and a bounded re-entry into `Plan`. The
constraints that shaped everything below:

- **The loop runs on a paid runtime.** Unbounded iteration must be
  unrepresentable, not merely discouraged — the same posture as `retry.max`,
  `feedback.max` and the verify-timeout ceiling.
- **Iteration must not widen capability.** The planner's reply is untrusted
  every time it is produced; assessment adds a second untrusted verdict to
  the pipeline. Neither may cause a planned node to gain a tool, a path, or a
  field that a single-cycle plan could not have.
- **The consumer contract is load-bearing.** `state.json` holds one graph per
  run and resume derives the ready set from `graph × completed` (ADR 0003);
  `events.jsonl` legs bracket re-executions of *that same graph*. Whatever
  shape iteration takes must not quietly change what a run is.

## Decision

### 1. Surface: `auto --max-cycles N`, explicit opt-in, default 1, bound required

Iteration is a flag on `auto`, not a new subcommand and not the new default:

```sh
oh-my-graph auto "make the test suite green" --max-cycles 3 --max-budget-usd 5
```

- `--max-cycles 1` is the default and is byte-identical to today's behaviour:
  one plan, one run, no assessment call, no new files or fields. Existing
  invocations, scripts and tests are untouched.
- `--max-cycles N` for N ≥ 2 opts into the loop. The bound is structurally
  required: the flag *is* the bound, it has no unbounded spelling (0 and
  negatives are rejected at parse), and there is no config file or environment
  default that could turn iteration on behind the user's back.
- There is no upper cap on N. The flag is typed by the human at the shell —
  the same standing as a hand-written graph's `feedback.max`, which "may
  declare any bound it is willing to pay for" (ADR 0010). The unreviewed
  artifacts inside the loop get fixed ceilings; the human's own bound does
  not.

The loop lives in `planAndExecute`. That function is the sequence's "exactly
one home" today precisely so that `auto` and chat cannot drift; the cycle is
that sequence run at most N times, so it lives in the same home. Chat
inherits the loop for free, and chat's existing confirm hook is asked **per
cycle** — each cycle's plan is printed and gated by its own `[y/N]` before it
executes (see §3 for the non-interactive posture).

Why not the alternatives:

- **A new subcommand** (`oh-my-graph goal …`) would either duplicate
  plan→save→print→execute outside its one home or be a trivial wrapper that
  exists only to rename `auto`. `auto` already *is* "goal in, execution out";
  iterating is a degree of the same verb, not a different verb. A second
  subcommand would also fork the chat integration: the router classifies a
  turn as a goal, and "which subcommand's semantics does a chat goal get"
  should not be a question.
- **Always-on with governance** (assess every auto run, re-plan by default,
  rely on budget flags to stop it) inverts the cost model silently: today
  `auto` costs one planner call plus one graph; always-on makes the *default*
  cost "up to N of each plus assessments", and the assessment call alone
  would add spend to every run including the ones that plainly succeeded.
  A tool whose per-invocation cost can multiply without a flag ever being
  typed fails the "legible worst case" bar that ADR 0010 set for `feedback`.

### 2. The cycle: plan → validate → execute → assess, with assessment as a third coordinator call

One cycle is, in order:

1. **Plan** — `coordinator.Plan`, verbatim. On cycle k ≥ 2 the planner prompt
   carries a continuation section: the original goal, a statement that a
   previous attempt ran, and the assessor's "what remains" text (bounded, see
   below). Nothing else changes about planning.
2. **Validate** — `validatePlannedNodes` and the full ADR 0004 ceiling, per
   cycle, no exceptions. Validation is inside `Plan`, so this is not a
   discipline to remember but a property of the only entry point: there is no
   code path in which a cycle's graph reaches the scheduler unvalidated, and
   no "the last plan was fine" caching. The cycle ordinal never appears in
   validation logic.
3. **Execute** — save the spec, print the plan (and ask the confirm hook when
   present), run the graph as an ordinary run (§4).
4. **Assess** — a new coordinator call class beside the planner and the chat
   router: same `coordinatorInvocation` stance (permission mode `plan`, no
   declared tools, the full deny list of a node that declared nothing), same
   `extractJSON` reply handling, same PlanError-style failure type.

The assessor is fed **only engine-produced material**, assembled by trusted
code:

- the goal text (the user's own words);
- the run's outcome and per-node results — verdict, detail, cost — as the
  ledger and snapshot recorded them;
- bounded excerpts of the run's artifacts: the engine reads each
  `<node-id>.out` and truncates to fixed per-artifact and total-material caps
  (named constants, keeping head and tail, like `truncate` does for planner
  replies today).

Explicitly **not** fed: the raw planner reply, chat history, prior cycles'
material, or anything from the user's settings. The assessor has no tools —
it cannot be lured into reading a file; it sees exactly the excerpts the
engine chose to show it.

**The assess contract** is JSON, like the planner's:

```json
{
  "goal_met": false,
  "remaining": "<plain-language statement of what remains>",
  "evidence": "<one short paragraph citing the material that decided it>"
}
```

`remaining` is required when `goal_met` is false; it is the *only* datum that
flows into the next cycle's `Plan` call, truncated to a fixed cap before it
enters the planner prompt. A reply with no JSON object, or JSON that does not
parse into this shape, is an assessment error and **stops the loop** with a
non-zero exit — the one thing the loop must never do is plan another paid
cycle on the strength of garbage.

**Where assessment lives, and why.** Two rejected homes, argued:

- **The final node's `success_check`** (let the plan's own regex decide).
  Rejected: a success check judges one node's output, not the goal, and its
  predicate is *planner-authored* — the untrusted plan would be grading its
  own homework, and a planner that learned to emit
  `result_matches: "(?s).*"` would declare every goal met.
- **An assessment node appended to the planned graph.** Rejected for the same
  reason one layer down: a node's prompt is the plan's to write. Even
  ceiling-bounded, the judge's instructions would be authored by the party
  being judged. Assessment must be a coordinator-owned prompt in trusted
  code, exactly as the planner prompt is — the coordinator plans the work and
  assesses the work; the work never writes its own reviews.

**Cycle termination.** After assess: `goal_met` → stop, exit 0 (even with
cycles remaining — a met goal spends nothing more). Not met with cycles
remaining and budget headroom → next cycle. Not met with cycles or budget
exhausted → stop, print the final `remaining`, exit 1: the *runs* may each
have passed, but `--max-cycles ≥ 2` makes the command's contract goal-level,
and "we stopped without meeting the goal" is a failure and must not exit 0.
Assessment runs after **every** cycle including the last (that is what
grounds the exit code and the final verdict the user reads) and after a
**failed** run too — a failed cycle's failure detail is precisely what the
next plan needs to route around. The one outcome that short-circuits
assessment is a **pause**: a planned graph cannot contain gates, so the only
pause an auto run can hit is a session limit (ADR 0009), and a session limit
means the subscription window is exhausted — there is no capacity to assess
or re-plan *with*. A paused cycle pauses the whole loop: print the standard
resume instructions, exit 2. The resumed run completes as an ordinary run;
re-entering the goal loop afterwards is deferred (Consequences).

### 3. Trust boundary and governance

Stated hard, because iteration composes three untrusted artifacts:

- **Artifacts are untrusted model output.** They were produced by planned
  nodes and may contain anything, including prompt injection aimed at
  whoever reads them. The assessor reads them (excerpted, bounded, toolless);
  the *planner* then reads the assessor's `remaining`. Both readers are
  read-only, deny-listed coordinator calls that emit JSON.
- **The planner's output is untrusted and hits the same validation every
  cycle.** An injected artifact that steers the assessor, which steers the
  planner, still produces a plan that must survive `validatePlannedNodes`,
  the allowlist, the disposition table and the layered ceiling — the same
  gauntlet a first-cycle plan faces. Iteration adds **zero** new plannable
  fields and touches neither the allowlist nor the disposition table; the
  reuse of `coordinator.Plan` verbatim is what makes that claim structural
  rather than aspirational.
- **The assess verdict can waste money but never widen capability.** The
  complete authority of `goal_met`/`remaining` is: stop the loop early, or
  cause at most `max-cycles − k` further cycles, each individually bounded
  exactly as cycle 1 was. A false "met" wastes the goal (and the user reads
  the printed verdict and evidence); a false "not met" burns cycles up to the
  bound; a poisoned `remaining` steers a plan that is ceilinged anyway.
  Money, not capability — and the money has two independent bounds:

- **`--max-cycles` is required by construction** (§1).
- **`--max-budget-usd` is an optional cross-cycle ceiling**, checked at the
  cycle boundary: before planning cycle k+1, if the accumulated spend of all
  cycles (run totals, planner calls, assessments) has reached the ceiling,
  the loop stops with the last assessment's verdict printed. It deliberately
  does **not** kill a cycle mid-execution — the planner prompt already
  encodes why ("a tight budget kills a nearly-done node at the threshold and
  loses its finished work"), and a cycle boundary is the clean stopping
  point, the same posture as ADR 0009's pause-not-kill. Consequence, stated
  honestly: actual spend can overshoot the ceiling by up to one cycle plus
  one assessment. The flag is only legal with `--max-cycles ≥ 2`: at one
  cycle it could only ever be checked after everything had already run — a
  no-op masquerading as a cap.

**The printed plan per cycle, and the non-interactive posture.** Every
cycle's plan is printed before it executes — topology, tools, agent mappings,
spec path — exactly as today. In chat, the existing `[y/N]` confirm gate is
asked per cycle, so interactive use has a human approving each plan. `auto`
stays what it has always been: fully non-interactive, `confirm == nil`,
plans printed but not gated. That is stated plainly rather than dressed up:
an unattended `auto --max-cycles 5` may spend five planner calls, five
graphs and five assessments with nobody watching. The governance for that
posture is the pair of bounds the human typed, the per-cycle validation and
ceiling, and the printed record of every plan — not a hidden prompt that
would deadlock the unattended case (the same reasoning that made a gate a
clean stop, ADR 0003, and rejected blocking on a TTY). A user who wants a
human decision per cycle uses chat; a per-cycle pause-and-resume gate for
non-interactive use would need goal-level resume and is deferred with it.

### 4. Run shape: a new run per cycle, linked by an additive lineage block

Each cycle is **its own ordinary run** — fresh run id, own directory, own
`graph.json`, `state.json`, `events.jsonl`, artifacts, own `resume.lock`
held for the cycle's duration. The loop is a sequence of runs in one
process, not one run with N graphs.

The rejected shape — one run whose stream gains a leg per cycle — looks
attractive because legs exist (RUN-FEED brackets each resumed leg with its
own `run_started`/`run_finished`) and gate-resume is the precedent. But the
precedent cuts the other way: **every existing leg re-executes the same
graph.** A gate leg, a `--retry-failed` leg and a feedback round all replay
the one graph the snapshot records; `state.json` holds exactly one `graph`,
one `graph_sha256`, one `nodes` map keyed by that graph's node ids, and
resume's entire correctness rests on "graph × completed determines the ready
set" (ADR 0003). A cycle *replans* — a different graph, with freely
colliding node ids whose `<node-id>.out` artifacts would overwrite each
other in a shared directory, and whose `nodes` records would be meaningless
against the snapshot's recorded graph. Making one snapshot hold N graphs is
a meaning change to nearly every field — a schema bump on both files and a
rewrite of resume — to buy a grouping that a lineage pointer provides
additively. Legs answer "the same run, continued"; a cycle is "a new plan
for the same goal", and the run boundary is exactly where that difference
belongs. A nested layout (`runs/<goal>/cycles/<n>/`) was also rejected: it
moves run directories, breaking every consumer that globs
`runs/<run-id>/` — fleetops included — for zero semantic gain.

**Linkage** is an optional `goal` block in each cycle's `state.json` —
additive optional fields, so **no schema bump** under RUN-FEED's own rule:

```json
"goal": {
  "text": "make the test suite green",
  "cycle": 2,
  "max_cycles": 3,
  "first_run_id": "<cycle 1's run id>",
  "previous_run_id": "<cycle 1's run id>"
}
```

Absent entirely on single-cycle runs (today's snapshots are byte-identical).
`first_run_id` is the stable group key (equal to the run's own id on cycle
1); `previous_run_id` is the chain. The assessment verdict is persisted as
`assess.json` in the assessed cycle's run directory — a documented consumer
file beside `graph.json`, present only on iterated auto runs — so the
observation step leaves the same kind of on-disk trace the planning step
does.

**Observability.** `runs list` groups runs sharing a `first_run_id` and
narrates the goal: each cycle's line gains `cycle 2/3` and the group is
titled by the goal text — an iterated goal reads as a story, not as N
unrelated runs. `serve` stays a per-run view (each cycle is a complete,
self-describing run) and shows the `goal` block in its header; a goal-level
view that follows the chain is additive later. fleetops needs nothing: every
cycle is a well-formed run under the existing contract, and a consumer that
ignores the `goal` block and `assess.json` sees exactly what it sees today.

**Ledger honesty.** Each cycle's run prints its own ledger — one row per
execution, its own planner call as the planning-cost line, per the existing
contract. The assessment call's cost is recorded against the cycle it
assessed (a second CLI-recorded entry beside `RecordPlanningCost`, and a
field in that cycle's `assess.json`). When the loop ends, a **goal summary**
prints below the final cycle's ledger: one line per cycle — run id, run
total, assessment cost — and the accumulating grand total, which is the same
number the `--max-budget-usd` check reads at each boundary. Nothing is
averaged or hidden: cycle 2 costing what cycle 1 cost is visible in the same
place, for the same reason ADR 0010 refused to aggregate feedback rounds
into one row — the entire risk of a loop on a paid runtime is the
multiplier, and the multiplier must be printed, not derivable.

**Cross-cycle handoff is the working tree, not artifacts.** `{{ artifacts.* }}`
is a per-run namespace and stays one: cycle 2's nodes cannot reference cycle
1's `.out` files, and the planner prompt does not learn to. What actually
carries between cycles is what carries between two `auto` invocations today
— the state of the invocation directory (files written, commits made) — plus
the one informational datum this ADR adds: the assessor's `remaining` text
in the next planner prompt. Feeding prior artifacts forward as inputs is
deferred until a real goal needs it.

### 5. Layering with ADR 0010: feedback edges iterate within a plan; cycles iterate across plans

Feedback edges and plan-and-execute answer different questions and compose
rather than compete. A feedback edge is **node-level** iteration inside one
planned graph: a declared reviewer judges a declared segment against a
declared `success_check`, and the graph author (human or planner) knew at
planning time both the loop's shape and its bound — "this reviewer sends
this work back, at most twice". Plan-and-execute is **goal-level**
iteration across graphs: the judge is the assessor, the criterion is the
goal itself, and what varies between iterations is *the graph* — the tool
for "the plan was the wrong plan", not "the work needs another round".
Use a feedback edge when the loop is knowable inside one plan (review →
re-implement); use `--max-cycles` when only the goal is knowable and the
next graph depends on what the last one produced. The planner **keeps** its
ability to emit feedback edges inside each cycle's graph, under the exact
ceiling that exists today — `validatePlannedNodeFeedback` and
`maxPlannedFeedbackRounds` (3) apply per cycle, untouched by this ADR — so
worst-case spend composes multiplicatively and legibly:
`max-cycles × (per-cycle worst case, feedback rounds included)`, every
factor of which was typed by a human or fixed in trusted code.

## Consequences

**Positive**

- `auto` becomes an actual orchestrator loop — plan, execute, observe,
  re-plan — while `--max-cycles 1` keeps every existing invocation
  byte-identical, and the loop's every dangerous degree of freedom (cycle
  count, budget, per-cycle validation, per-cycle printing) is bounded or
  visible by construction.
- The consumer contract is untouched: no schema bumps, one additive optional
  snapshot block, one new documented file. Every cycle is an ordinary run;
  fleetops and the in-repo readers work unmodified.
- Assessment is trusted-code-owned end to end: coordinator prompt, engine-
  assembled evidence, toolless read-only invocation, hard JSON contract,
  stop-on-garbage. The untrusted parties (plan, artifacts, verdict) can
  waste bounded money and nothing else.
- The failure/iteration story now has all four layers, each with its own
  bound and its own question: `retry` (same node), `feedback` (same graph
  segment, ADR 0010), `--max-cycles` (same goal, new graph), `resume`
  (same run, later). The one-paragraph layering in §5 is owed to the docs.

**Negative / trade-offs**

- Assessment is a paid call per cycle that can be wrong in both directions —
  a false "met" under-delivers the goal, a false "not met" burns a cycle.
  The verdict, evidence and material caps are printed and persisted so a
  human can audit the judge, but the judge is still a model.
- A paused cycle (session limit) pauses the loop with **no way to re-enter
  it**: after `resume` completes that run, the goal loop is over — the user
  re-invokes `auto` with a sharpened goal by hand. Goal-level resume (and
  with it any per-cycle non-interactive gate) is real future work, deferred
  because it needs cross-run lineage in the resume path, not just in `runs
  list`.
- `--max-budget-usd` is a boundary check, not a guillotine: overshoot by up
  to one cycle plus one assessment is possible and documented. Anyone
  needing a hard cap must size `--max-cycles` accordingly.
- The non-interactive posture accepts that unattended iteration spends with
  nobody watching, governed by bounds rather than approvals. That is
  consistent with what `auto` already is, but it widens how much an
  unattended invocation can spend from one graph to N.
- `planAndExecute` grows from a sequence into a loop with termination
  analysis (met / exhausted / budget / pause / assess-error), and the
  assessor adds a third coordinator call class to keep in stance-lockstep
  with the planner and router. DESIGN.md (the cycle, the `goal` block,
  `assess.json`, the goal summary) and RUN-FEED.md must land in the same
  change that implements this ADR.

## Alternatives considered

- **A new subcommand instead of `--max-cycles`.** Rejected: duplicates or
  trivially wraps the sequence whose whole point is having exactly one home,
  and forks the chat-routing question. Argued in §1.
- **Always-on iteration with governance flags.** Rejected: silently
  multiplies the default cost model and adds an assessment charge to runs
  that plainly succeeded. Iteration is spend and spend is opt-in. Argued
  in §1.
- **Assessment via the final node's `success_check` or an appended
  assessment node.** Rejected: both put the judge's authorship inside the
  untrusted plan — the work grading its own homework. Assessment is a
  coordinator-owned prompt over engine-chosen evidence. Argued in §2.
- **Feeding the assessor the raw run directory (or giving it Read).**
  Rejected: a toolless judge over engine-excerpted material cannot be lured
  into reading arbitrary paths, and bounded excerpts cap both spend and
  injection surface. If excerpts prove too thin, widening the *material* is
  a product change that does not require widening the *capability*.
- **One run with a leg per cycle.** Rejected: every existing leg replays the
  snapshot's one recorded graph; a cycle replans. N graphs per snapshot is a
  meaning change to both consumer files (schema bumps) and a rewrite of
  resume's `graph × completed` rule, to buy what an additive lineage block
  provides. Argued in §4.
- **Nested run directories per goal.** Rejected: moves run dirs out of
  `runs/<run-id>/`, breaking every existing consumer's glob for zero
  semantic gain. Argued in §4.
- **Clamping instead of stopping on a budget-exceeded or garbage-assess
  reply.** Rejected: a loop that "kept going sensibly" past its ceiling or
  on an unparseable verdict is exactly the quiet-spend behaviour every bound
  in this codebase exists to make unrepresentable. Stops are loud and carry
  the evidence.
- **A cross-cycle artifact namespace (`{{ cycles.* }}`).** Deferred, not
  rejected: no goal yet needs cycle 2 to read cycle 1's artifacts (the
  working tree carries the real product), and adding a namespace is additive
  later; adding it now would be speculative surface on the interpolation
  contract.
- **An upper cap on `--max-cycles` (mirroring `maxPlannedFeedbackRounds`).**
  Rejected: that ceiling exists because an *unreviewed plan* declares
  `feedback.max`; `--max-cycles` is typed by the human at the shell, the
  same trust standing as a hand-written graph's own bounds, which are
  deliberately uncapped.
