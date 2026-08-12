# ADR 0023 — A run has ONE status, and PLANNING is one of its values

- Status: **Accepted — decision taken, not yet implemented.** §9 lists what the
  implementation owes.
- Date: 2026-08-12
- Issues: [#163](https://github.com/jitokim/oh-my-graph/issues/163)
- **Extends `0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md`.**
  ADR 0015's liveness rule (in flight = an open leg AND a held lock) is the
  mechanism here too, unchanged and un-duplicated; its three-valued
  `runstatus.Status` becomes the six-valued enumeration below, and its
  `ABANDONED`-is-not-`FAIL` argument is the reason two of the six exist.
- **Extends `0009-a-session-limit-is-a-pause-not-a-failure.md`** on the reading
  side: ADR 0009 made a session limit a pause in the engine and in the stream,
  and every read-back surface then rendered it `FAIL`. That is the defect §1.2
  measures.
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## 1. Context

### 1.1 The report — the longest wait in the tool is the one nothing shows

A colleague who installed oh-my-graph this week ran `auto`, saw

```text
Planning a graph for goal "…"...
```

and then nothing. For the whole planner call — the single longest wait in the
tool, and the first one a new user meets — `runs list` printed `No runs found.`
and the `serve` dashboard showed no card. From outside the invoking terminal,
the state of the machine is indistinguishable from "nothing happened".

The cause is one line's position. In `cmd/oh-my-graph/main.go`, `planAndExecute`
reads:

```go
fmt.Fprintf(out, "Planning a graph for goal %q...\n", goal)
plan, err := coord.Plan(ctx, goal, inputKeys(flags.inputs))
if err != nil {
    return noteRejectedPlan(out, newRunID(), err)
}

runID := newRunID()
```

`newRunID()` runs **after** `coord.Plan` returns. During planning there is no
run id, therefore no run directory, therefore nothing on disk for any reader to
find — and every reader in this project is a reader of a run directory
(docs/RUN-FEED.md). The invisibility is not a rendering bug in any surface; it
is the absence of the thing all of them read.

### 1.2 The second value that is already wrong, and was not reported

While confirming §1.1, one more answer turned out to be wrong today, and it is
wrong in the same column. `cmd/oh-my-graph/runs.go` says so in its own words,
in the doc comment on `runSummary.verdict`:

> `verdict` is verdictRunning or verdictAbandoned for a run that is not settled,
> else PASS only when every node in the graph reached VerdictPass — **a failed,
> paused, or interrupted run all render as FAIL.**

So a shepherd stopped at its approval gate — exit code 2, resumable, working
exactly as designed — is listed as `FAIL`. `runs list` prints zero `PAUSED` rows
today, because it has no such word. The dashboard has the same hole by a
different route: `internal/serve/card.go`'s `runState` returns `stateGatePaused`
only when the snapshot carries `gate.paused_at`, so ADR 0009's session-limit
pause — which pauses without a gate — falls through to the `default` arm and
paints the card **red, as failed**.

This is not a new state being invented for the pleasure of it. It is a value the
engine already produces, that every read-back surface then destroys.

### 1.3 Why one enumeration, and not two more special cases

Today the answer a surface gives is composed, by that surface, from two
independent things: `runstatus.Status` (Settled / InFlight / Abandoned, ADR
0015) and a verdict string derived from the snapshot. `runs list` composes them
in `summarizeRun`; `internal/serve/card.go` composes them in `runState`;
`watch` composes a third of them; `show` composes none at all and prints no
run-level answer whatsoever, so re-opening a paused run after the fact tells you
nothing about it being paused.

`internal/runstatus` exists precisely to stop that: its package comment calls
itself "the ONE settled/in-flight/abandoned rule", and names the five surfaces
that ask. But it owns only *half* the question. The half it does not own — what
a settled run settled *as* — is the half that is wrong in §1.2, and it is wrong
differently in each surface, which is exactly the outcome the package was
created to prevent.

The maintainer's decision, which this ADR implements rather than reconsiders:

> **A run id identifies an oh-my-graph EXECUTION, not the moment a graph starts
> executing.** Planning is inside that scope, so the id is minted before the
> planner call and the planning phase is a real part of the run.
>
> **The status is one enumeration with six values:**
>
>     PLANNING → RUNNING → { PASS | FAIL | PAUSED | ABANDONED }

Six, and not four, because two of the terminal values are load-bearing and must
not collapse into `FAIL`:

- **ABANDONED** — ADR 0015 §4 refuses the collapse explicitly: *"Deliberately
  not FAIL — a FAIL is a verdict about the work, and the work never got one."*
  That refusal was bought with two real specimens on the maintainer's machine.
  Merging it into `FAIL` reverses an ADR.
- **PAUSED** — this one is a defect being fixed, not a state being added. It is
  §1.2.

## 2. Decision

### 2.1 One enumeration, six values, one derivation

`runstatus.Status` becomes the six-valued enumeration. It is the only run-level
status vocabulary in the codebase; `runs.go`'s `verdictWord` /
`unsettledVerdict` and `card.go`'s `runState` verdict logic are deleted, not
duplicated into it.

| value | means | derived from |
|---|---|---|
| `PLANNING` | a leg is open, its process is alive, and it is in its planner call — no graph exists yet | open leg whose latest `run_started` carries `phase: "planning"`, + lock not free |
| `RUNNING` | a leg is open, its process is alive, and the scheduler has it | open leg whose latest `run_started` carries no phase, + lock not free |
| `ABANDONED` | a leg is open and its process is gone | open leg + affirmatively free lock (ADR 0015 §2, unchanged) |
| `PAUSED` | the leg closed on a pause and the run is resumable | closed leg whose last `run_finished` outcome is `"paused"` |
| `PASS` | the leg closed and every node of the graph reached `PASS` | closed leg + snapshot `CompletedNodes() == len(g.Nodes)` |
| `FAIL` | the leg closed and it did not | everything else that has settled |

Precedence is top to bottom as written: an open leg answers before any snapshot
question, a free lock answers before the phase question, and `"paused"` answers
before the completed-count. Two properties are load-bearing:

- **`ABANDONED` still requires two affirmative facts** — an open leg *and* a
  `LOCK_SH` that actually succeeded — and every doubt (`ENOENT`, an unmarked
  lock, a non-local filesystem, a probe error) still lands in the in-flight arm.
  ADR 0015 §2's table is imported whole; adding `PLANNING` only splits which
  in-flight arm a run lands in, and the split is decided by an affirmative event
  field, never by the absence of node events.
- **`PAUSED` is read off the stream's own `outcome`, not off `gate.paused_at`.**
  That is what makes it cover both pause shapes: a gate pause (ADR 0003) and a
  session-limit pause (ADR 0009), which has no gate to point at and is the one
  the dashboard paints red today.

`Status` gains one predicate, `Settled() bool` (true for `PASS`/`FAIL`/`PAUSED`),
because three call sites currently spell "not settled" as `status !=
runstatus.Settled` and each would otherwise have to learn the new membership by
hand — including `summarizeRun`'s snapshot-less excuse, where getting it wrong
makes a run vanish from the listing behind a `WARNING` (ADR 0015 §4's own
easiest way to ship a change as a net loss).

`Derive` stays a pure function over the facts, `Of(runDir)` reads them, and
`Probe` keeps its shape for the dashboard's hot path — the card already walks
the stream once and loads the snapshot once for the graph and the cost, and must
not pay a second read for a status.

### 2.2 The planning phase is a leg: it takes the lock and it opens the stream

**ADR 0015's liveness rule stays the mechanism, and no second liveness mechanism
is introduced.** The planning phase does exactly what an execution leg does, in
the same order, with the same objects:

1. `runstate.AcquireLock` on `<run-dir>/resume.lock` — which creates the run
   directory `0o700` on the way, as it already does for a first leg.
2. `runfeed.NewStreamWriter` on `<run-dir>/events.jsonl`.
3. `run_started` with `phase: "planning"`.
4. …the planner call…
5. either the untagged `run_started` of §2.3 (the plan was committed) or
   `run_finished` (it was not).

This is `cmd/oh-my-graph/runlock.go`'s ordering invariant, unchanged and now
starting earlier: *a leg must hold the lock before it writes its first event and
must still hold it after its last.* One lock and one stream writer per leg, so
`executeGraph` **receives** them rather than acquiring them — a second
`AcquireLock` from the same process would take a `LOCK_EX` against an fd the
same process already holds and get `LockHeldError`, since flock conflicts per
open file description (ADR 0015 §1's measured table). `run` (a hand-written
graph) has no planning phase and keeps opening its leg at `executeGraph`.

What this buys is the hardest requirement, for free: **a planner call whose
process dies is `ABANDONED`**, because the kernel released the flock, and the
run reads that way from the next `runs list` with no cleanup path, no timer, and
no probe. `runstatus.Recovery`'s snapshot-less arm is already the correct advice
for it — *"it never wrote a snapshot, so there is nothing to resume from — run
the graph again"* — and the orphan warning is apt without a word changing: a
planner call is a `claude` subprocess spawned through the same seam, in its own
process group, and it can outlive the engine exactly as a node's can.

### 2.3 The transition is marked, not inferred: `run_started` gains an optional `phase`

**No new event type. No schema bump. `events.jsonl` stays schema 2 and
`state.json` stays schema 2.**

`run_started` gains one optional additive field, `phase`, with exactly one
defined value, `"planning"`. Absent — which is every `run_started` this tool has
ever written, and every one a `run` or a `resume` leg will ever write — means
the scheduler leg, byte-identical to today. An auto run's stream becomes:

```text
run_started {phase:"planning"}      ← the leg opens; PLANNING
run_started                         ← the plan was committed; RUNNING
node_started …                      ← as today
run_finished {outcome:"passed"}
```

Three reasons this shape, and not a new `run_planning` event type:

1. **The precedent runs the other way, twice.** ADR 0009 declined `node_limited`
   and ADR 0015 declined `run_abandoned`, both for the same stated reason: the
   event-type set is closed per schema version, so a new type forces a bump on
   every consumer. The additive-optional-field rule is what this project has
   used every time since — `detail`, `session_id`, `round` (ADR 0010),
   `provenance` (ADR 0016), `judged` (ADR 0020), the `goal` block (ADR 0011).
   A third ADR reversing that needs a reason the first two did not have.
2. **The reason ADR 0015 gave for refusing a new type does not apply to this
   fact, and that distinction is the whole justification for writing anything at
   all.** `ABANDONED` needed no event because it was derivable from bytes
   already on disk. `PLANNING` is derivable from nothing: a planner call in
   progress leaves no trace unless the engine writes one. So a fact must be
   written — the only question is whether it is a new type or a field, and the
   field is the cheaper of the two by the project's own rule.
3. **An unmodified external consumer gets #163's fix for free.** A reader that
   ignores the unknown `phase` field sees a leg that opens at planning and
   closes once — so `runfeed.InFlight`'s rule, implemented by anyone, reports
   the run in flight for the whole planner call. That is a *less precise* answer
   than `PLANNING`, not a wrong one, which is exactly ADR 0015 §3's standard for
   not bumping. Under a new event type the same consumer would skip the line and
   see nothing, i.e. keep the bug.

**Both directions are affirmative.** `PLANNING` is "the latest `run_started`
says planning"; `RUNNING` is "the latest `run_started` says nothing". Neither is
"no node has started yet", which would paint the first instants of every
hand-written run `PLANNING` and would repeat the derive-from-absence mistake ADR
0015 spends itself avoiding.

**The repetition of `run_started` inside one leg is not a new shape for this
stream.** docs/RUN-FEED.md already states that *every* `run_started` is a leg
boundary, and `internal/serve/card.go`'s `walkNodeStates` already implements it
— it is how a resumed leg re-opens after one that never closed. At the
planning→running boundary nothing is running, so the boundary's own effect
(`markAbandoned` over running nodes) is a no-op by construction.

**The cost, stated because a consumer can trip on it:** a reader that counts
`run_started` lines to count *legs* now counts one extra per auto run. The
disambiguator is the field, and docs/RUN-FEED.md must say so in the same
sentence that introduces it — count legs by `run_started` events with **no**
`phase`. This is the same class of change ADR 0010 made when a feedback round
began emitting a second `node_started` for one node inside one leg, and it was
shipped as an additive field with no bump for the same reason.

### 2.4 A run directory is created by the commitment to execute

The enumeration has six cells and **none of them is "planned but never run"**.
That is not an oversight to route around; it decides where a run directory may
be created.

`auto` is non-interactive: if the plan validates, it runs. The commitment
therefore exists *before* the planner call, and auto's planning phase
legitimately opens `runs/<id>/`. Two paths do not have that commitment, and
neither may create a run directory before it lands:

- **`auto --plan-only`** never executes at all (§3).
- **`chat`**, whose plan is gated behind a `[y/N]` a human may answer `n`.

For `chat`, the spec save moves *after* the confirmation, into the run
directory, and a declined plan's paid-for spec goes to `plans/<id>/` — where a
rejected plan's already goes. This also closes a defect that exists today and
was found while confirming §1.1: `planAndExecute` saves `graph.json` into
`runDirFor(runID)` **before** calling `confirm`, so every declined chat plan
leaves behind a `runs/<id>/` holding a `graph.json` and no `state.json` —
which `runs list` reports through `WARNING: skipping run …` and the dashboard
paints `unknown`. A declined plan manufactures a corrupt run today.

Forcing a declined plan into the enumeration instead was considered and refused:
`FAIL` for a run the user chose not to start is precisely the defect §1.2 exists
to fix, re-introduced in the same ADR that fixes it, and on a path that exits 0.

### 2.5 The corrupt-run channel is no longer reached by a healthy phase

The constraint this had to satisfy: *a run directory holding a `graph.json` and
no `state.json` currently reads as a corrupt run*, through the same
`WARNING`-and-skip channel as a damaged snapshot (`summarizeRun`'s excuse lapses
when a run is settled), and `serve` enumerates the same tree.

It is satisfied by the leg, not by a special case. Through a planning phase the
directory holds `resume.lock` and `events.jsonl` and neither `graph.json` nor
`state.json`; the open leg plus the held lock make the status `PLANNING`, which
is not settled, so `summarizeRun`'s existing excuse applies and the row renders
with the `-` placeholders it already renders for a snapshot-less run. `graph.json`
appears when the plan validates, and `state.json` follows within the same
function (`recorder.WriteInitial`, ADR 0009). **No window exists in which the
absence of a file is what decides the answer** — the leg and the lock decide,
and the only remaining `WARNING`+skip is a genuinely unreadable snapshot or
stream, which is what that channel is for.

### 2.6 The surfaces render the one value; exit codes are untouched

- **`runs list`** — the `VERDICT` column becomes the enumeration, so `PLANNING`
  and `PAUSED` are printable words for the first time. A `PAUSED` row carries
  its resume command under the table, beside the `ABANDONED` hint and for the
  same reason: it is a row a reader cannot act on without being told how.
- **The dashboard card** — a `planning` state token, and the run-level
  `gate-paused` widens to `paused` so it also covers the session-limit pause it
  paints red today. Node-level `gate-paused` is untouched.
- **The single-run live view** — `/api/graph` serves the same composed answer it
  already serves `abandoned` and `hint` through. It is named here because ADR
  0015's own §4 forgot it and needed a dated correction.
- **`watch`** — refuses an `ABANDONED` run as today, and prints the status word
  it is tailing toward.
- **`show`** — gains the run's status line above its per-node table. It has
  none today, which is why re-opening a paused run after the fact says nothing
  about it being paused.

**Exit codes keep meaning what they mean: 0 all passed, 1 failed, 2 paused and
resumable.** Nothing in this ADR touches `mainExitCode`. The distinction worth
stating: an exit code is the *writer's* answer about the process it just ran,
and the enumeration is a *reader's* answer about a directory on disk. They are
computed from different facts and must agree, so the implementation asserts the
agreement rather than assuming it — a run that exits 2 must read `PAUSED`, one
that exits 1 must read `FAIL`, one that exits 0 must read `PASS`. `PLANNING` and
`RUNNING` are not terminal and map to no exit code; `ABANDONED` is what a run
reads as when there was no exit code at all, which is the point of it.

## 3. The open question, decided: `--plan-only` stays out of `runs/`

`--plan-only` deliberately keeps its spec outside `runs/` today, on the argument
recorded at `planDirFor`: *a preview never ran, so it is not a run.* Under the
new definition of a run id — an oh-my-graph execution — that argument is no
longer obviously right, since a preview **is** an execution that paid for a
planner call.

**Decision: a preview is still not a run. `--plan-only` keeps `plans/<id>/`, and
mints no run directory at any point, including during its planner call.**

The reason is not the old one. The old one was a mechanism argument — a
directory under `runs/` with a `graph.json` and no `state.json` reads as damage
— and §2.5 dissolves exactly that mechanism. The reason that survives is the
enumeration itself:

**A preview has none of the six statuses, and cannot be given one.** It is not
`PLANNING` (it finished), not `RUNNING`, not `ABANDONED` (its process left on
purpose), and it has no verdict about work, because there was no work. Listing
it requires a seventh value — `PLANNED` — that would be load-bearing on exactly
one surface and would have to be threaded through `Derive`, both web surfaces,
`watch`, `show` and the exit-code agreement, to describe a thing that by
construction never does anything again. The maintainer fixed the enumeration at
six. The honest reading of that is that the unit `runs/` enumerates is a thing
which *has* one of those six statuses, and a preview does not.

**Against the reading this rejects.** The strongest case for moving previews
into `runs/` is the one this ADR is built on: a preview spends real money, holds
real time, and can be killed mid-planner-call, so under "a run id identifies an
execution" it looks like it qualifies. Three answers, in order of weight:

1. **Paying for something does not make it a run; producing one does.** The
   thing the id identifies is the thing the six values describe. If the id
   named "any execution that spent money", the assessment call of an iterated
   goal (ADR 0011) would need one too, and so would a `resume` that is refused
   before its first node — and neither has a status either.
2. **The invisibility argument, which is #163's, does not transfer.** #163 hurts
   because planning is invisible *and then a run follows that the operator
   intends to watch* — they open `runs list` or the dashboard because there will
   be something there. A preview has no such run: when the planner returns, the
   command is over and has printed everything it will ever print. Today a
   preview is invisible before and after; after this ADR it stays invisible
   before and after. That is coherent. What was incoherent — and what is fixed
   — is `auto` being invisible and then visible, with no way for the user to
   know which half they were in.
3. **The cost is real, small, and stated rather than hidden.** A preview's
   planner spend appears in no `runs list` total, before or after this change.
   It is printed at the moment it is incurred, by the `plan only:` block, which
   already names the figure and says in as many words that this is not a run.

The same argument decides the two adjacent cases, which is the test of whether
it is a principle or a preference: a **declined** chat plan takes the same door
(§2.4), and a **rejected** plan does not — a rejection is a real `FAIL`, exit 1,
diagnosed by the engine about material it judged, so it is representable and it
keeps its run directory, with `rejected.json` written there instead of into
`plans/`. `plans/` thereby narrows to one honest meaning: **specs that never
belonged to a run.**

## 4. What this deliberately does NOT do

**The `No build verification configured` warning is not carried here, and this
is the argument for declining it rather than an omission.** #163's secondary
point is right: that warning prints once to stdout and scrolls away, so the
single most consequential fact about a run — that nothing in it will compile or
test your code — is the least likely to still be on screen when it ends. But
"carry it into the run's own record" resolves, on inspection, into two different
requests and neither belongs in this ADR:

- **The record already has it.** ADR 0016 §6 made every `node_passed` carry
  `provenance`, and a run with no build evidence carries `self-reported` or
  `exit-only` on every one of its PASSes, in `state.json` and on the stream.
  "Nothing in this run gathered build evidence" is a one-line reader-side fold
  over records that are already persisted. A new run-level field would be a
  second, derivable source of a truth the record already holds — the exact
  duplication this ADR is removing one layer up, at the status.
- **What is missing is a rendering**, on three surfaces (the end-of-run ledger,
  `show`, the dashboard), and a rendering of an existing field is not an
  architectural decision. Folding it in here would mean either a seventh status
  value (refused in §3, and worse here — "unverified" is a qualifier on a
  verdict, not a status) or an unrelated snapshot field shipped under a status
  ADR.

The right shape is a separate issue: *render the run-level provenance summary
beside the ledger, in `show`, and on the card, derived from the records already
in `state.json`.* It is genuinely cheap, and it is cheap **because** it needs
nothing from this one.

**Three further windows stay invisible, named rather than implied:**

- **The goal loop's assessment call** (ADR 0011). It happens after cycle *k*'s
  leg closed and before cycle *k+1*'s id exists, so it belongs to no run and
  gets no status. It is the same shape as #163 and is not fixed here; fixing it
  needs a phase that belongs to the *goal* rather than to a run, which is a
  bigger question than this ADR's.
- **The embedded live view still starts at execution.** It renders a graph, and
  during planning there is no graph; ADR 0006's opener launches once, and
  launching it at a page that will be empty for the length of a planner call is
  worse than launching it late. A separately running `oh-my-graph serve` shows
  the `PLANNING` card immediately, which is the surface #163 actually names.
- **A run that never wrote a snapshot shows `-` for its cost**, including a
  `FAIL` produced by a rejected plan, whose planner call is real money. That is
  today's behaviour for every snapshot-less row and this ADR does not widen it;
  the figure is printed by `noteRejectedPlan` at the moment it is spent. Making
  the cost column read from two sources — the snapshot and the stream — is
  exactly the divergence this project keeps refusing.

## 5. Failure modes

- **A `PLANNING` run that never becomes anything.** A planner call killed with
  `SIGKILL` leaves an open leg, a freed flock and no snapshot: `ABANDONED`, with
  the "nothing to resume from — run the graph again" arm of `runstatus.Recovery`
  and the orphan warning about a planner subprocess that may still be spending.
  This is the intended behaviour and it is why the phase had to be a leg.
- **`PLANNING` forever on a platform without `flock(2)`.** The build-tagged
  fallback reports liveness *unknown*, unknown reads as in flight, and a dead
  planning phase therefore reads `PLANNING` indefinitely — the same standing
  cost ADR 0015 accepted for `RUNNING` there, now visible under one more word.
  No new mechanism, and no new mitigation.
- **More directories under `runs/`.** Every auto invocation now leaves one, a
  refused plan included. A user who repeatedly fails to get a plan past
  validation accumulates `FAIL` rows with `-` costs. This is information, not
  litter — those calls were paid for — but it is a visible change in what
  `runs list` accumulates, and there is still no `runs prune` (ADR 0015 §5).
- **A run whose `run_finished` write failed** reads `ABANDONED` once the process
  exits, instead of `PASS`/`FAIL`. Pre-existing (event writes are deliberately
  non-fatal), unchanged, and now reachable one phase earlier.
- **A `PAUSED` row prints a resume command that is refused.** A run started with
  `--verify-cmd` cannot be resumed at all (ADR 0016 §4), and the row cannot
  cheaply know that. The refusal itself carries the explanation, so the cost is
  one wasted command, not a wrong action; a row that printed nothing would be
  worse for every other paused run.
- **The status and the exit code disagree.** They are computed from different
  facts, so a divergence is possible in principle (a leg that pauses but fails
  to write its `run_finished` exits 2 and reads `ABANDONED`). The implementation
  pins the agreement with a test rather than asserting it in prose.

## 6. Compatibility

- **Neither file schema moves.** `events.jsonl` stays 2, `state.json` stays 2.
  The only change to the versioned bytes is one optional field on `run_started`
  that old readers ignore, under docs/RUN-FEED.md's additive rule.
- **An unmodified external consumer improves without acting.** During planning
  it sees an open leg and reports the run in flight, where today it sees
  nothing. It cannot distinguish `PLANNING` from `RUNNING`, which is a loss of
  precision it did not have before either.
- **The one thing a consumer can get wrong is leg counting** (§2.3). Documented
  in the same paragraph that introduces the field.
- **No downgrade cliff.** Because there is no bump, a v0.6.x binary still reads
  a run directory this version wrote. That mattered enough to be decisive: this
  repo's own `graphs/self-dev` rebuilds and reinstalls this binary while runs
  are in flight, so an old binary reading a new run directory is a routine local
  event here, and under a schema bump `runfeed.Walk` would refuse the whole
  stream — `WARNING`+skip in `runs list`, an `unknown` card on the dashboard.
- **`runs list`'s verdict column changes what it prints.** ADR 0015 already
  recorded that column as not a contract ("a script matching `RUNNING` will
  simply stop matching abandoned runs, which is the point"). The same applies
  twice over here: a script matching `FAIL` stops matching paused runs. That is
  the fix, and it is the one behaviour a user could have been depending on.
- **Where a declined plan's spec lands changes** (`runs/<id>/graph.json` →
  `plans/<id>/graph.json`) and where a rejected one lands changes
  (`plans/<id>/rejected.json` → `runs/<id>/rejected.json`). Both are additive to
  the documented run-directory listing in docs/RUN-FEED.md.

## 7. Alternatives, and why they lost

- **A new `run_planning` event type (stream schema 3).** The cleanest
  vocabulary, and rejected on three counts: it reverses ADR 0009's and ADR
  0015's stated refusals of exactly this trade; it forces every consumer to
  handle a bump for one fact; and it withholds the fix from the unmodified
  consumers who most need it, since they skip the unknown type and keep seeing
  nothing (§2.3).
- **Derive `PLANNING` with no new fact at all** — "a held lock and a stream with
  no `run_started`". Tempting because it needs nothing written, and refused
  because the fallback is fatal: a planner call that *dies* then leaves a free
  lock and no leg, which is indistinguishable from an empty directory, so the
  abandoned case would have to be recovered from the lock file's mere
  *existence*. ADR 0015 §1 forbids that in as many words — the file is never
  removed, "it is a handle, not a flag", and "nothing anywhere reads its
  existence as a state".
- **Infer `RUNNING` from the first `node_started`** instead of the untagged
  `run_started`. Rejected: it makes the transition an absence, so a run whose
  first node has not launched yet (staging, a snapshot write, a slow ready-set)
  reads `PLANNING` when it is not planning, and a planned run that errors
  between the plan and the first node reads `PLANNING` right up until it reads
  `ABANDONED`.
- **A separate `planning` marker file in the run directory.** A second liveness
  mechanism by the back door — it needs its own creation, its own removal, and
  its own answer to "what if the process died holding it", which is the question
  `flock(2)` already answers. The constraint was explicit: do not invent a
  second liveness mechanism.
- **Keep the run id late and show the planning phase some other way** (a spinner,
  a progress line, a `plans/` card). Rejected because it fixes the symptom for
  the invoking terminal only. Every reader in this project reads a run
  directory; a phase with no run id is a phase no reader can find, by
  construction.
- **Four values (PLANNING → RUNNING → PASS → FAIL), folding PAUSED and
  ABANDONED into FAIL.** Rejected: `ABANDONED`'s fold reverses ADR 0015 §4 and
  the two specimens behind it, and `PAUSED`'s fold *is* the defect in §1.2 —
  a run that stopped exactly as designed, exit 2, resumable, reported as a
  failure.
- **Seven values, with `PLANNED` for a preview.** Rejected in §3: it exists to
  make one surface list a thing that has no status, and it would have to be
  carried by `Derive`, both web surfaces, `watch`, `show` and the exit-code
  agreement to do it.
- **Leave the enumeration split (liveness in `runstatus`, verdict per surface)
  and just add `PAUSED` to `runs list`.** The smallest patch that closes §1.2,
  and rejected because it closes it on one surface: the dashboard's red
  session-limit card, `show`'s silence and `watch`'s vocabulary would each need
  their own patch, composed their own way — which is the condition
  `internal/runstatus` was created to end.

## 8. Consequences

**Positive**

- The first thing a new user waits for is the first thing they can see. `runs
  list`, the dashboard, and any external consumer report the run from the
  moment it starts, which is now the moment planning starts.
- A run that dies during its planner call is diagnosable, resumable-or-not with
  the correct advice, and cleaned up by the kernel — using machinery that
  already exists and is already tested.
- A paused run stops reading as a failure on every surface at once, including
  the session-limit pause the dashboard paints red today.
- One derivation in one package, with the verdict half finally inside it. Four
  surfaces lose their hand-rolled composition; a fifth (`show`) gains an answer
  it never had.
- A declined chat plan stops manufacturing a corrupt run directory.
- No schema bump, no new event type, no new exec seam, no new command or flag,
  no new dependency.

**Negative / trade-offs**

- **An auto run's stream now carries two `run_started` lines**, and a consumer
  counting legs must learn one field. This is the largest cost, it is paid by
  external consumers, and it is paid in exchange for not bumping the schema on
  all of them.
- **`run_started`'s timestamp moves earlier** for auto runs, so any elapsed
  clock derived from it — including the dashboard's — now includes the planner
  call. Under the maintainer's definition of a run that is the correct total,
  but it is a number that changes without the consumer doing anything.
- **`runs/` accumulates a directory per auto invocation**, refused plans
  included, and there is still no prune.
- **`executeGraph`'s signature changes**: it receives a leg (lock + feed) rather
  than opening one. That is a real widening of the CLI's internal seam and the
  ordering invariant now has two entry points to satisfy instead of one.
- **The goal loop needs a per-cycle hook** so the CLI can mint an id and open a
  leg before `coordinator.plan` runs (§9). Without it `PLANNING` would exist for
  single-cycle auto and not for iterated auto — a status that depends on a flag
  is worse than no status.
- **`--plan-only` and `chat` keep a planning phase nobody can see**, deliberately
  (§3), so the fix is not uniform across the three planner-calling paths. The
  ADR argues that is the honest boundary rather than an incomplete rollout.

## 9. Implementation notes — what the code owes

- `internal/runstatus` — the six-valued `Status`, `Settled()`, the widened
  `Derive`/`Probe`/`Of` over the added facts (leg phase, last outcome, snapshot
  gate and completion), the `PAUSED` resume wording beside `Hint`/`Recovery`.
  It gains the snapshot as an input; `Probe` must stay usable by a caller that
  already loaded it.
- `internal/runfeed` — `Event.Phase` (`json:"phase,omitempty"`), the
  `PhasePlanning` constant, and whatever `InFlight`'s callers need to learn the
  latest leg's phase without a second walk. `Schema` does **not** move.
- `cmd/oh-my-graph` — a leg value owning the lock and the stream writer; the
  planning phase in `planAndExecute` (and in the goal loop's per-cycle hook);
  `executeGraph` taking the leg instead of acquiring one; `runs.go` losing
  `verdictWord`/`unsettledVerdict`; `show` gaining the status line; the spec-save
  reordering of §2.4; `noteRejectedPlan` writing into the run directory and
  taking the id that already exists (`rejectedPlanID`'s `-cycleK` construction
  becomes unnecessary — the refused cycle has a run id of its own).
- `internal/coordinator` — `RunGoal` gains the per-cycle "planning begins" hook.
  It mints nothing itself: run ids stay the CLI's, as they are today.
- `internal/serve` — `runState` collapses to a mapping from the enumeration;
  `planning` state token and CSS; run-level `gate-paused` → `paused`;
  `/api/graph` serving the same composed answer.
- **Tests.** `TestRunLeg_LockBracketsTheEventStream` widens to the planning
  phase (the invariant now starts earlier and is easier to break). The
  cross-surface agreement test judges all six values, not the liveness half. One
  new test pins the exit-code/status agreement (§2.6). All of it against
  `FakeRunner` — the planner call is a `NodeRunner` call, so a planning phase is
  scriptable with no real spawn.
- **Docs.** docs/RUN-FEED.md: the `phase` field on `run_started`, the
  leg-counting sentence, `rejected.json` in the directory listing, and the
  liveness section gaining the sentence that a planning phase holds the lock
  like any other leg. DESIGN.md: "Repo layout" (unchanged), "Web live view —
  `oh-my-graph serve`" (the card vocabulary), "Goal cycles" (the per-cycle
  planning phase), and the `auto` sequence wherever it states that a run id is
  minted from a validated plan. CHANGELOG under the release that ships it.

## 10. What could not be determined

1. **Whether any external consumer counts `run_started` lines.** Unknown, and
   unknowable from here — the same answer ADR 0015 gave about the verdict
   column. The additive field makes the correct count derivable; nothing makes
   an existing incorrect count fail loudly.
2. **How long a planner call actually is, distributionally.** #163 calls it the
   longest single wait in the tool, and the maintainer's runs agree
   impressionistically, but nothing in this repo measures it. It matters only to
   how much the fix is worth, not to whether it is right.
3. **Whether `PLANNING` should end at plan validation or at the first node.**
   This ADR ends it at the untagged `run_started`, which is emitted between
   them, so a run reads `RUNNING` for the staging and snapshot work before its
   first node launches. Nothing measures whether users read that window as
   running or as still preparing; it is short, and the alternative (§7) is
   worse for a well-understood reason.
