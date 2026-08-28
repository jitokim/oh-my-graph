# ADR 0023 — A run has ONE status, and PLANNING is one of its values

- Status: **Accepted — implemented 2026-08-13, and reviewed three times since.**
  §9 listed what the implementation owed and it is delivered; §12 records the two
  notes the implementation learned, §13 the code review that changed behaviour in
  two places (§2.1.1, §2.7), and §14 the fresh-eyes round that found no
  correctness defect and closed eight unguarded copies and record gaps instead.
  What ships is the six-valued enumeration derived once in `internal/runstatus`
  and rendered by the six surfaces §2.6 names, with the planning phase a leg that
  holds the lock and opens the stream, `rejected.json` inside the run directory,
  and `--plan-only` still minting no run id.
- Date: 2026-08-12
- **Revised 2026-08-12 after a design review, before any code existed.** The
  decision did not change; the specification did, in ten places recorded in §11.
  Two of them were contradictions that would have surfaced as a red test
  (§2.1.1, §2.6) and one was a new false-`ABANDONED` mode (§2.7).
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
| `FAIL` | the leg closed and it did not | everything else that has settled, **a settled run with no snapshot at all included** (§2.1.1) |

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

#### 2.1.1 The enumeration is total, including the cell §3 makes common — and a read failure is not a seventh value

Two combinations are named here rather than left to the diff, because a reader
will look for them and because one of them is manufactured by this very ADR.

**(closed leg, no `state.json` at all) is `FAIL`.** Today that combination is
nearly unreachable; §3 makes it the shape of *every refused plan*, whose
directory holds `resume.lock`, a two-line `events.jsonl`, `rejected.json`, and
neither `graph.json` nor `state.json`. The closed leg and the last outcome
decide the status; the missing snapshot decides nothing, and in particular it
must not decide *whether the row exists*. `PASS` is the one value that needs the
snapshot, so a settled run without one cannot read `PASS` — see §5 for the one
way that is wrong.

**A directory whose stream has said nothing at all has no status yet** — no
`run_started`, no node event, which is the window between `AcquireLock` creating
the directory and the first event reaching the file. It is not `FAIL`; the
dashboard keeps `pending` there, on that affirmative fact (§2.5).

*Amended after round 2:* the question is `runstatus.Spoken(Facts)`, shared by
every surface that renders a word, and not a guard the dashboard holds alone.
`Derive` is total, so its default arm answers `FAIL` for that cell; while only
`card.go` guarded it, one directory read `pending` on the card and a confident
`FAIL` in `runs list` — surfaces disagreeing about the same bytes, which is what
this package exists to end. `Spoken` is affirmative like every other fact (a
`run_started` that was written, or a snapshot that is there — a *lone*
`run_finished` is a close with no open before it, which is damage, not a leg),
and it stays OUT of `Derive`: it is the question of whether there is a status to
derive, not one of the six. Each surface then renders it in the vocabulary it
already has for "not known yet" — the card's `pending`, the `-` that `runs
list`'s three other columns already use, and `show`'s omitted word.

**An unreadable snapshot or a refused stream is NOT a seventh status value.** It
is a failure to read the directory, and the surfaces keep the channels they have
for it: `WARNING`+skip in `runs list`, `stateUnknown` on the card. The
distinction the implementation must hold onto is that *the absence of a file is a
fact about the run, while the unreadability of one is a fact about the reader* —
the first is derivable, the second is an error, and only the second may make a
row disappear. "One enumeration, one derivation" is a claim about statuses, not
a claim that a directory can always be read.

**This is where the change is easiest to ship as a net loss, so the two guards
are specified here.** Both `summarizeRun` and `card.go` currently decide what a
*missing* snapshot means by asking whether the run has **settled** — and under
six values `FAIL` is settled, so a mechanical rewrite makes a refused plan's row
vanish behind a `WARNING` and its card read `pending` forever. Neither guard may
keep keying on settledness:

- `summarizeRun` excuses a missing snapshot **on the error alone** —
  `errors.Is(err, fs.ErrNotExist)`, at any status. That is the honest predicate
  independently of this ADR: `state.json` is written atomically after a node's
  terminal verdict, so its *absence* never means damage at any status, while a
  corrupt or schema-incompatible one always does and keeps the `WARNING`+skip
  path. This also **breaks the ordering hazard** — the excuse no longer needs to
  know the status, so the status is free to take the snapshot as an input
  (§9) without a cycle between them.
- `card.go` keeps `statePending` on the affirmative "the stream has said
  nothing", not on "not settled", so a spoken-for directory always renders its
  derived status.

`Status` gains **two** predicates, and the second is not optional:

- `Settled() bool` — true for `PASS`/`FAIL`/`PAUSED`. Three call sites spell
  "not settled" as `status != runstatus.Settled` today and would otherwise each
  learn the new membership by hand.
- `InFlight() bool` — true for `PLANNING`/`RUNNING`. The value this ADR adds
  splits the **in-flight** side, so a predicate on only the settled side is
  asymmetric with the split it introduces, and the asymmetry has a concrete
  victim: `serve.ResolveRun` picks the newest run that is `InFlight` for the
  single-run view, and rewritten as `== RUNNING` it would **stop preferring a
  planning run** — silently withholding the state this ADR exists to make
  visible, on the surface #163 actually names.

The membership choice at each in-flight call site, written down because the
compiler cannot check it — every one of these is an equality test against a
value that ceases to exist, so the *breakage* is found for you and the *wrong
answer* is not:

| call site | today | becomes |
|---|---|---|
| `serve.ResolveRun` | `== InFlight` | `InFlight()` — a `PLANNING` run is preferred, like a running one |
| `serve/card.go`'s `runState` | `== InFlight` | splits: `PLANNING` → `planning`, `RUNNING` → `running` |
| `summarizeRun`'s excuse | `!= Settled` | gone — keyed on the error (above) |
| `watch`'s refusal, `/api/graph`'s hint | `== Abandoned` | unchanged |

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
   **`run_finished` with `outcome: "failed"`** (it was not).

**The closing outcome is `"failed"`, and the choice is load-bearing rather than
arbitrary.** The outcome set is closed (`passed`/`failed`/`paused`) and §2.1's
precedence reads `PAUSED` straight off this token, so `"paused"` would make a
refused plan read `PAUSED` and print a resume command for a run with nothing to
resume — the §1.2 defect inverted. `"failed"` is also the true statement: the
engine judged the material it was given and diagnosed it (§3), which is what a
`FAIL` is. Every way a planning phase closes without committing a plan — a
rejection, a validation failure, a cancelled context — closes it this way.

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

**That disambiguator fixes the opening side only, and the ADR says so rather
than letting a consumer discover it.** `run_finished` carries no `phase` and
gains none, so two further stream shapes are new and must be documented in
docs/RUN-FEED.md alongside the field:

- **A consumer that pairs opens with closes sees an imbalance on a committed
  auto run**: `run_started{phase:"planning"}` → `run_started` → `run_finished`,
  one close for two opens. This is harmless for the reading that matters —
  `runfeed.InFlight` asks only whether the *last* leg is open, and the last
  `run_started` is the untagged one, so it answers correctly throughout — but a
  consumer keeping a stack of legs must know that the planning open is closed by
  the untagged open, not by a `run_finished` of its own. Adding a `phase` to
  `run_finished` would not help: the close that ends a planning phase on the
  committed path is not a `run_finished` at all.
- **`run_finished{outcome:"failed"}` with ZERO node events** is a shape no
  stream has ever had (a refused plan, §2.2). A consumer that assumes a failed
  leg names a failed node has to stop assuming it.

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
(Wording since #230/`16f0ecc`: `WARNING: run "<id>" could not be read in full
(<class>): <err>` — `internal/runstatus/skipped.go:201`. The quote above is
kept as this record wrote it.)

Forcing a declined plan into the enumeration instead was considered and refused:
`FAIL` for a run the user chose not to start is precisely the defect §1.2 exists
to fix, re-introduced in the same ADR that fixes it, and on a path that exits 0.

**This widens an invariant `planAndExecute` states in its own doc comment, and
the widening is recorded rather than absorbed.** That comment reads: *"a graph
started from chat is indistinguishable on disk (saved spec, `state.json`,
`events.jsonl`) from one started at the shell"*, with `confirm` named as **the
one permitted divergence**. Under the rule above, auto opens its leg before
planning and chat cannot — chat's commitment does not exist until a human
answers the prompt, which happens *after* the planner call — so an auto run's
`events.jsonl` carries two `run_started` lines where a chat-started run's carries
one. That is a second divergence, in one of the three files the invariant names.

It is accepted, and it is accepted because it is not an independent divergence:
it is `confirm` itself, observed on disk. The commitment boundary is what §2.4
decides, `confirm`'s presence is what moves it, and the stream is where a moved
boundary is visible. Restructuring the shared sequence so the boundary became an
explicit parameter was considered and judged the worse trade — it would make one
sequence carry two commitment models to preserve a sentence, when the honest
repair is to fix the sentence. So the doc comment is updated (§9) to say the
invariant it can still keep: **identical on disk given the same commitment
point**, with `confirm` as the one thing that moves that point and the extra
`run_started` as its recorded consequence. An invariant weakened in silence is
how the next reader gets misled; this one is weakened in writing.

### 2.5 The corrupt-run channel is no longer reached by a healthy phase

The constraint this had to satisfy: *a run directory holding a `graph.json` and
no `state.json` currently reads as a corrupt run*, through the same
`WARNING`-and-skip channel as a damaged snapshot (`summarizeRun`'s excuse lapses
when a run is settled), and `serve` enumerates the same tree.

It is satisfied by the leg, not by a special case. Through a planning phase the
directory holds `resume.lock` and `events.jsonl` and neither `graph.json` nor
`state.json`; the open leg plus the held lock make the status `PLANNING`, and the
row renders with the `-` placeholders it already renders for a snapshot-less run.
`graph.json` appears when the plan validates, and `state.json` follows within the
same function (`recorder.WriteInitial`, ADR 0009).

**The claim this satisfies is narrower than "no window exists in which a missing
file decides the answer", and stating it too broadly would be false in this
ADR's own newest cell.** A refused plan settles with no snapshot at all (§2.1.1),
so the question "is a missing `state.json` excusable here" does not disappear —
it is asked about a *settled* run for the first time. What this ADR guarantees is
the pair:

1. **No status is decided by the absence of a file.** Every one of the six is
   decided by affirmative facts — an open leg, a probed lock, a `phase` field, a
   `run_finished` outcome — and the snapshot only ever *refines* `PASS`.
2. **No healthy run reaches the `WARNING`+skip channel**, at any status, because
   the surfaces stop asking the status for permission to excuse a missing
   snapshot and ask the error instead (§2.1.1). A missing file is excusable
   always; an unreadable one is damage always.

The second half is the part §2.1.1 had to specify, and without it this ADR would
close the corrupt-run channel for the `PLANNING` window it set out to fix while
newly routing every refused plan into it. The only remaining `WARNING`+skip is a
genuinely unreadable snapshot or stream, which is what that channel is for.

### 2.6 The surfaces render the one value; exit codes are untouched

- **`runs list`** — the column becomes the enumeration, so `PLANNING` and
  `PAUSED` are printable words for the first time, and **the header is renamed
  `VERDICT` → `STATUS`**. §1.3's whole diagnosis is that liveness and verdict
  were being conflated; leaving `PLANNING`/`PAUSED`/`ABANDONED` under a header
  that says `VERDICT` would preserve that conflation in the one place a user
  reads it. ADR 0015 already recorded this column as not a contract, so the
  rename costs nothing that the content change did not already cost.
  A `PAUSED` row carries its resume command under the table, beside the
  `ABANDONED` hint and for the same reason: it is a row a reader cannot act on
  without being told how.
- **The dashboard card** — a `planning` state token, and the run-level
  `gate-paused` widens to `paused` so it also covers the session-limit pause it
  paints red today. Node-level `gate-paused` is untouched.
- **The single-run live view** — `/api/graph` serves the same composed answer it
  already serves `abandoned` and `hint` through. It is named here because ADR
  0015's own §4 forgot it and needed a dated correction.
- **`serve`'s `ResolveRun`** — which run the single-run view opens on when no id
  is given. Named for the same reason, and against the same mistake — an earlier
  draft of this section listed five surfaces and left this one out, which is how
  ADR 0015 came to need its dated correction in the first place. It is one of the
  five askers `runstatus`'s own package comment names, it keys on in-flight rather
  than on abandoned, and it is the one surface where a mechanical rewrite silently
  loses `PLANNING` (§2.1.1). It prefers an in-flight run, and a `PLANNING` run is
  one.
- **`watch`** — refuses an `ABANDONED` run as today, and prints the status word
  it is tailing toward.
- **`show`** — gains the run's status line above its per-node table. It has
  none today, which is why re-opening a paused run after the fact says nothing
  about it being paused.

**Exit codes keep meaning what they mean: 0 all passed, 1 failed, 2 paused and
resumable.** Nothing in this ADR touches `mainExitCode`. The distinction worth
stating: an exit code is the *writer's* answer about the process it just ran,
and the enumeration is a *reader's* answer about a directory on disk. They are
computed from different facts, so where they can be made to agree the
implementation asserts the agreement rather than assuming it.

**Where they can be made to agree is narrower than "always", and the narrow
version is the one the test pins.** For a **single-run invocation** — `run`,
`resume`, single-cycle `auto` — the invocation's exit code and that run's status
agree: exit 2 ↔ `PAUSED`, exit 1 ↔ `FAIL`, exit 0 ↔ `PASS`. That is the
invariant, and it is the whole invariant. It does **not** extend to the goal
loop, for two independent reasons, either of which alone would break a blanket
claim:

1. **The goal loop's exit code is goal-level, not run-level** (ADR 0011 §2, and
   `main.go`'s own package doc says so in as many words): *"stopping unmet (cycles
   exhausted, budget ceiling, a declined later cycle) exits 1 even when every run
   passed."* `goalExit` returns an error for `StopCyclesExhausted`, so the process
   exits 1 while every one of its cycles' directories correctly reads `PASS`.
   Asserting agreement there would not find a bug; it would assert that ADR 0011
   is wrong.
2. **It is not a function.** One iterated invocation produces N run directories
   against one exit code, so there is no mapping to assert in either direction.

An earlier draft of this section claimed the agreement unconditionally. That was
false when written, and it mattered because §9 turns this paragraph into a test:
an implementer following the broad wording would have written a red test and then
had to relitigate the ADR to find out which half was wrong. `PLANNING` and
`RUNNING` are not terminal and map to no exit code; `ABANDONED` is what a run
reads as when there was no exit code at all, which is the point of it.

### 2.7 The leg is a value with an idempotent `Close`, and its bracket is restored one scope up — not enumerated

Moving the leg's open earlier has a consequence that is not about status at all,
and it is the most dangerous thing in this ADR because its failure mode is a
*false alarm on a healthy exit*.

Today the ordering invariant is enforced by **shape**: `acquireRunLock`, `defer
release()` and `defer feed.Close()` all sit inside `executeGraph`, and
`TestRunLeg_LockBracketsTheEventStream` guards it. Under §2.2 the open moves into
a per-cycle hook the CLI hands to `coordinator.RunGoal`, while the close stays
with `executeGraph` on the happy path. `defer` cannot bracket that: the leg's
lifetime now runs *through another package's control flow*. Between the hook and
`execute`, `RunGoal` can return on `c.plan` failing, on a cancelled context, and
on any early return a future change adds there. (The budget check is *before*
`c.plan`, so `StopBudgetExceeded` does not leak a leg — that one is already safe.)

**An open leg whose process then exits normally is `ABANDONED`, and `ABANDONED`
prints `runstatus.OrphanWarning`** — *"a `claude` subprocess started by the dead
leg may still be running and spending"*. A leaked leg therefore does not merely
mislabel a row: it raises a **false double-spend alarm on a clean exit**, which
degrades the one and only mitigation ADR 0015 accepted for its largest cost. A
warning that cries wolf on healthy runs is worth less on the runs that need it.

**Decision: the leg becomes an explicit value with `Close(outcome)`, and the
bracket is restored lexically in the CLI one scope up — `planAndExecuteCycles`
defers a close-if-still-open over the *entire* `RunGoal` call.**

- `Close` is **idempotent**: `executeGraph`'s normal close on the happy path and
  the deferred sweep compose without the sweep needing to know what happened. The
  sweep closes with `"failed"` (§2.2) only if the leg is still open.
- The sweep is in the package that **creates** the leg and that holds
  `TestRunLeg_LockBracketsTheEventStream`, so the invariant stays testable where
  it is already tested.
- **No enumeration of `RunGoal`'s exits is required, which is the point.**
  Enumerating them in this ADR was the alternative and it is the weaker fix: the
  list would be correct only until the next change to `coordinator/goal.go`, and
  it would put the obligation on a maintainer's memory in a package that cannot
  see the leg. Hoisting the plan call out of `RunGoal` was the other alternative,
  and it is worse still — it would break ADR 0011's own invariant that *"there is
  no code path in which a cycle's graph reaches the caller unvalidated"*, trading
  a correct validation guarantee for a lifetime one that a `defer` already buys.

§8's earlier framing — "the ordering invariant now has two entry points to satisfy
instead of one" — understated this. It is **one open and three exits, one of them
in another package**, and the reason it is nevertheless safe is the sweep, not
diligence at each exit.

*Amended after round 2, which found the one exit the sweep could not reach:
`executeGraph`'s own.* The sweep is disarmed by the very idempotence it relies
on. `executeGraph` defers `Close("")` — empty, because the scheduler writes this
leg's `run_finished` itself — and an empty close still marks the leg CLOSED, so
a return from above the scheduler (a `state.json` that cannot be written) closed
the leg *value* without closing the leg on the *stream* and left the outer sweep
with nothing to do. The auto path's `run_started` is already on disk at that
point, so the directory read `ABANDONED` after a clean exit 1: exactly the false
alarm this section closes "by construction rather than accepted". Two rules
restore it:

- **The empty close is registered BELOW the recorder's first write**, so every
  exit above it is still the sweep's; and `executeGraph` defers its own
  `Close("failed")` above that, so the guarantee does not depend on which caller
  it was reached from.
- **An outcome is emitted only for a leg that announced ITSELF** (`beginPlanning`).
  A `run` opens its leg here and the scheduler is what announces it, so a sweep
  that emitted unconditionally would replace a false `ABANDONED` with a lone
  `run_finished` — a close with no open before it, which §2.1.1 calls damage
  rather than a leg. Leaving that stream silent is the honest close.

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

1. **Paying for something does not make it a run; a commitment to execute does**
   (§2.4). The thing the id identifies is the thing the six values describe, and
   what brings such a thing into existence is the commitment, not the spend. If
   the id named "any execution that spent money", the assessment call of an
   iterated goal (ADR 0011) would need one too, and so would a `resume` that is
   refused before its first node — and neither has a status either.
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

### 3.1 Two premises, not one, and neither of them circular

The adjacent cases are the test of whether the above is a principle or a
preference, and they only come out cleanly once the argument is split in two.
A single "is it representable?" test does not survive contact with them: a
rejection produced no graph and no execution either, so if *producing a run* were
the whole test it would fail the same test a preview fails — and answering "but a
rejection is a real `FAIL`, so it is representable" argues that it is a run
because the enumeration can hold it, which is the same circularity this ADR
correctly discards when it retires the old mechanism argument (§3's opening).

The two premises that do the work are independent, and each is checkable without
reference to the other:

1. **A commitment to execute is what creates a run** (§2.4). It is what makes a
   run directory legitimate, and it exists before the planner call or not at all.
2. **A diagnosis about judged material is what gives a run a verdict.** The
   engine was handed material, judged it, and produced a finding about it. That
   is a `FAIL` in the same sense a failed node is — unlike a preview, which
   produced nothing to judge and therefore has nothing to be right or wrong
   about.

The three cases then fall out with no appeal to the enumeration's shape:

| case | commitment? | diagnosis? | result |
|---|---|---|---|
| **rejected `auto` plan** (incl. any goal cycle) | yes | yes | a run directory, `FAIL`, `rejected.json` inside it |
| **`--plan-only` rejection** | no | yes | no run; the diagnosis prints, the spec goes to `plans/` |
| **declined `chat` plan** | no | no (a human declined; nothing was judged) | no run; the spec goes to `plans/` |

**So `noteRejectedPlan` takes either a run leg or a plan id, and this is the
under-specified branch a reader would otherwise hit at implementation time.** The
earlier wording — "`noteRejectedPlan` writing into the run directory and taking
the id that already exists" — has no branch for the path where no id exists, and
`--plan-only` is exactly that path: it mints no run id at any point, so a
rejection there has no run directory to write into. Without the split, the same
failure would land in two places depending on a flag with nothing in the ADR
saying why. With it, the rule is one sentence: **`rejected.json` goes into the
run directory when a run leg already exists, and into `plans/` when none does.**

`plans/` thereby keeps one honest meaning: **specs that never belonged to a
run** — a preview's, a preview rejection's, and a declined plan's.

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
  `--verify-cmd` needs that flag re-supplied on the resumed leg (ADR 0016 §4,
  amended 2026-08-18 — before that amendment such a run could not be resumed at
  all), and the row cannot cheaply know it is such a run. The refusal itself
  carries the explanation and names the flag to add, so the cost is one wasted
  command, not a wrong action; a row that printed nothing would be worse for
  every other paused run.
- **The status and the exit code disagree.** They are computed from different
  facts, so a divergence is possible in principle (a leg that pauses but fails
  to write its `run_finished` exits 2 and reads `ABANDONED`). The implementation
  pins the agreement with a test for the single-run invocation, which is the only
  shape in which agreement is even well-defined (§2.6).
- **A leg leaked between the planning hook and `execute` would read `ABANDONED`
  on a clean exit** — a false orphan-subprocess warning, degrading the mitigation
  ADR 0015 accepted for its largest cost. Closed by construction rather than
  accepted: the leg is a value with an idempotent `Close` and
  `planAndExecuteCycles` defers a close-if-open over the whole `RunGoal` call
  (§2.7), so no exit path in another package has to remember.
- **A passed leg whose snapshot write was lost reads `FAIL`, not `PASS`.** `PASS`
  is the one value that requires the snapshot (§2.1.1), so a settled run without
  one cannot read it. This is a broken directory either way — `state.json` is
  written atomically after every node's terminal verdict, so a leg that passed
  every node wrote it — and the mislabel is the conservative direction: claiming
  `PASS` from a stream token while the record that proves it is missing is the
  worse error. Same class as the `run_finished`-write failure above, and named
  because §3 makes snapshot-less settled runs ordinary.

## 6. Compatibility

- **Neither file schema moves.** `events.jsonl` stays 2, `state.json` stays 2.
  The only change to the versioned bytes is one optional field on `run_started`
  that old readers ignore, under docs/RUN-FEED.md's additive rule.
- **An unmodified external consumer improves without acting.** During planning
  it sees an open leg and reports the run in flight, where today it sees
  nothing. It cannot distinguish `PLANNING` from `RUNNING`, which is a loss of
  precision it did not have before either.
- **The one thing a consumer can get wrong is leg counting** (§2.3). Documented
  in the same paragraph that introduces the field, together with the two shapes
  the field does *not* disambiguate: one `run_finished` closing two
  `run_started`s on a committed auto run, and a `run_finished{outcome:"failed"}`
  carrying zero node events on a refused one.
- **A run directory this version writes may hold no `graph.json` and no
  `state.json`** — through the planning phase, and permanently for a refused plan
  (§2.1.1). A **v0.6.x** binary reading one renders it through its
  `WARNING`+skip / `unknown` channels, because the guards that excuse a missing
  snapshot are the ones this ADR fixes and the old binary does not have the fix.
  This is the one place the "no downgrade cliff" claim is qualified: the *stream*
  downgrades cleanly, the *directory shape* does not. It is accepted because the
  affected directories are ones an old binary reports as damaged rather than
  misreports as healthy, and because the local scenario that made the no-bump
  decision decisive (`graphs/self-dev` reinstalling this binary mid-run) leaves
  its runs' snapshots in place.
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
  `plans/<id>/graph.json`), and where a rejected one lands changes **only when a
  run leg exists** (`plans/<id>/rejected.json` → `runs/<id>/rejected.json` for
  `auto` and each goal cycle; unchanged under `plans/` for `--plan-only`, which
  has no run id — §3.1). Both are additive to the documented run-directory
  listing in docs/RUN-FEED.md.

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
- **One package, two composed values: liveness stays the three-valued `Status`,
  and the verdict MOVES INTO `runstatus` as a second value composed by one
  function there.** This is the strongest alternative to the flattening, and it is
  distinct from the previous bullet — it achieves §1.3's stated goal verbatim
  ("one derivation in one place"; all four surfaces lose their hand-rolled
  composition), while:
  - leaving `ResolveRun`, `watch` and `summarizeRun`'s snapshot-less excuse
    semantically untouched, so findings of the §2.1.1 and §2.6 kind largely do
    not arise;
  - keeping `Derive` the pure two-fact function its doc comment advertises,
    instead of one that must also be told the leg's phase, the last outcome, and
    the completed/total node counts;
  - still printing one word per surface, because the rendering can flatten what
    the derivation keeps separate.

  **Rejected, and the honest reason is that the maintainer fixed the vocabulary
  at one enumeration with six values** (§1.3), which this ADR implements rather
  than reconsiders. The supporting reason, which stands on its own: two composed
  values means every surface still decides *how* to flatten them into the one word
  it prints, and "each surface composes the answer its own way" is precisely
  §1.3's defect — it would be moved one level up (from deriving to rendering)
  rather than removed. Flattening in the derivation is what makes the six words a
  vocabulary instead of a convention.

  **What the flattening costs, recorded because it is a real cost and it is the
  flattening — not the new `PLANNING` value — that causes it:** `runstatus` grows
  from a two-fact composition over `runfeed` + `runstate` into one that also needs
  the snapshot and the graph's node count, i.e. a new `runstatus → internal/graph`
  dependency edge and new error modes on a package whose current contract is "a
  missing stream is not an error". The edge is acceptable — `internal/graph` is a
  pure value-object package with no infra dependencies, so a composition layer
  depending on it inverts nothing — and §9 confines it: `Derive` stays pure by
  taking the counts as plain integers, so only `Of` (which must read the directory
  anyway) acquires the snapshot load and the `graph.Parse`, and `Probe` keeps
  taking facts its caller already has so the dashboard's hot path pays no second
  read. Every in-flight call site also has to re-choose its membership, which is
  the §2.1.1 table's whole reason for existing.

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
  than opening one. That is a real widening of the CLI's internal seam, and the
  leg's lifetime stops being lexical at its point of use: **one open, three
  exits, one of them in another package.** The mitigation is structural, not
  procedural — an idempotent `Close` plus a deferred close-if-open over the whole
  `RunGoal` call (§2.7) — because the failure mode is a false `ABANDONED`, i.e. a
  false double-spend warning on a clean exit.
- **The goal loop needs a per-cycle hook** so the CLI can mint an id and open a
  leg before `coordinator.plan` runs (§9). Without it `PLANNING` would exist for
  single-cycle auto and not for iterated auto — a status that depends on a flag
  is worse than no status.
- **`runstatus` gains two dependencies** — the snapshot and `internal/graph` (for
  the node total) — and a function that could not previously fail on anything but
  the stream. That is the price of flattening liveness and verdict into one
  vocabulary, not of adding `PLANNING`; §7 records the alternative that avoids it
  and why the maintainer's constraint outranks it.
- **`planAndExecute`'s "indistinguishable on disk" invariant weakens** to
  "identical given the same commitment point", because auto's stream carries a
  planning `run_started` that chat's cannot (§2.4).
- **`--plan-only` and `chat` keep a planning phase nobody can see**, deliberately
  (§3), so the fix is not uniform across the three planner-calling paths. The
  ADR argues that is the honest boundary rather than an incomplete rollout.

## 9. Implementation notes — what the code owes

- `internal/runstatus` — the six-valued `Status`, **both** `Settled()` and
  `InFlight()` (§2.1.1), and the widened `Derive`/`Probe`/`Of` over the added
  facts: **leg phase, last `run_finished` outcome, and the completed/total node
  counts.** The snapshot's `Gate.PausedAt` is deliberately **NOT** one of them —
  `PAUSED` is read off the stream's outcome (§2.1), which is the only formulation
  that covers ADR 0009's gate-less session-limit pause, and handing
  `snap.Gate.PausedAt` back into the derivation is the precise re-entry point for
  the §1.2 defect. `card.go`'s `runState` loses that argument entirely rather than
  passing it through. `Derive` stays pure by taking the counts as integers; `Of`
  is where the snapshot load and the `graph.Parse` for the node total live; `Probe`
  must stay usable by a caller that already loaded the snapshot. The `PAUSED`
  resume wording joins `Hint`/`Recovery`.
- `internal/runfeed` — `Event.Phase` (`json:"phase,omitempty"`), the
  `PhasePlanning` constant, and whatever `InFlight`'s callers need to learn the
  latest leg's phase without a second walk. `Schema` does **not** move.
- `cmd/oh-my-graph` — a leg value owning the lock and the stream writer, with an
  **idempotent `Close(outcome)`** (§2.7); the planning phase in `planAndExecute`
  (and in the goal loop's per-cycle hook); `planAndExecuteCycles` **deferring a
  close-if-still-open over the entire `RunGoal` call**; `executeGraph` taking the
  leg instead of acquiring one; `runs.go` losing `verdictWord`/`unsettledVerdict`
  and **excusing a missing snapshot on `fs.ErrNotExist` alone, not on the status**
  (§2.1.1); the `VERDICT` → `STATUS` header (§2.6); `show` gaining the status
  line; the spec-save reordering of §2.4; `planAndExecute`'s doc comment restated
  as "identical given the same commitment point" with the extra `run_started`
  named as `confirm`'s recorded consequence (§2.4); `noteRejectedPlan` taking
  **either a run leg or a plan id** and writing `rejected.json` into the run
  directory only in the first case (§3.1) — for `auto` and every goal cycle the
  run id already exists, so `rejectedPlanID`'s `-cycleK` construction becomes
  unnecessary there, while `--plan-only` keeps minting a plan id.
- `internal/coordinator` — `RunGoal` gains the per-cycle "planning begins" hook.
  It mints nothing itself: run ids stay the CLI's, as they are today, and so does
  the leg — the hook hands `RunGoal` no closing obligation, which is what lets
  §2.7's sweep cover exits `RunGoal` may grow later.
- `internal/serve` — `runState` collapses to a mapping from the enumeration and
  **loses its `paused` parameter**; `planning` state token and CSS; run-level
  `gate-paused` → `paused`; the `pending` guard rekeyed onto "the stream has said
  nothing" (§2.1.1); `ResolveRun` on `InFlight()`; `/api/graph` serving the same
  composed answer.
- **Tests.**
  - `TestRunLeg_LockBracketsTheEventStream` widens to the planning phase (the
    invariant now starts earlier and is easier to break).
  - The cross-surface agreement test judges all six values, not the liveness half.
  - **A refused plan's directory renders on every surface** — a `FAIL` row in
    `runs list` (not a `WARNING`+skip) and a `failed` card (not `pending`), from
    a fixture holding `resume.lock` + a two-line `events.jsonl` + `rejected.json`
    and no snapshot. This is the §2.1.1 regression, and it is the one that would
    otherwise ship as a net loss.
  - **An unreadable snapshot still `WARNING`s and still renders `unknown`** at
    every status — the other half of §2.1.1, which is what keeps the excuse from
    swallowing real damage.
  - **A `c.plan` failure mid-goal-loop leaves no open leg**, so the cycle's run
    reads `FAIL` and not `ABANDONED`, and no orphan warning is printed (§2.7).
  - **`ResolveRun` prefers a `PLANNING` run** over an older settled one (§2.1.1's
    silent-loss case).
  - **`PAUSED` covers the session-limit pause** — derived from
    `run_finished{outcome:"paused"}` with no `gate.paused_at` anywhere, which is
    the §1.2 dashboard defect.
  - One test pins the exit-code/status agreement **for a single-run invocation
    only**, and — because the broad claim was the trap — a second asserts the
    goal-loop case it must not claim: `StopCyclesExhausted` exits 1 while every
    cycle's directory reads `PASS` (§2.6).
  - All of it against `FakeRunner` — the planner call is a `NodeRunner` call, so a
    planning phase is scriptable with no real spawn.
- **Docs.** docs/RUN-FEED.md: the `phase` field on `run_started`, the
  leg-counting sentence, the two shapes the field does not disambiguate (one
  close for two opens; a failed leg with zero node events — §2.3),
  `rejected.json` in the directory listing **with the two places it can land**
  (§3.1), the run directory that legitimately holds neither `graph.json` nor
  `state.json`, and the liveness section gaining the sentence that a planning
  phase holds the lock like any other leg. DESIGN.md: "Repo layout" (unchanged), "Web live view —
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

## 11. Review round 1 (2026-08-12) — disposition

A design review of this ADR against the code it plans to change, run before any
implementation existed. Ten findings, all incorporated; none rejected. Recorded
per finding because several of them are *why a section says what it says*, and a
later reader who cannot see the deleted wording would otherwise be free to
"simplify" it back.

| # | finding | disposition |
|---|---|---|
| 1 | **The enumeration was not total: "settled leg, no snapshot" was undefined — and §3 makes it the common case.** Both `summarizeRun`'s excuse and `card.go`'s `pending` guard key on *settledness*, so a refused plan's `FAIL` would vanish behind a `WARNING` and its card would read `pending` forever — the exact channel §2.5 claimed to have closed. | **Incorporated**, and it took the most: new **§2.1.1** (the cell is `FAIL`, an unreadable directory is not a seventh value, both guards rekeyed off the status, the excuse/derivation circularity broken by keying on the error), **§2.5** rewritten to claim only what is true, plus §5 and the §9 regression tests. |
| 2 | **§2.6's exit-code/status agreement was already false**, and §9 ordered a test pinning it: `StopCyclesExhausted` exits 1 while every cycle reads `PASS`, and one invocation makes N runs against one exit code. | **Incorporated.** §2.6 narrows the invariant to a single-run invocation and states both reasons it cannot extend to the goal loop; §9 pins the narrow claim and adds a test asserting the goal-loop case it must *not* claim. |
| 3 | **The planning leg's lifetime becomes non-lexical across a package boundary** → a leaked leg reads `ABANDONED` on a clean exit, i.e. a false double-spend alarm, degrading ADR 0015's only mitigation. | **Incorporated** as new **§2.7**: an idempotent `Close`, and the bracket restored lexically one scope up (a deferred close-if-open over the whole `RunGoal` call) instead of enumerating `RunGoal`'s exits — with both rejected alternatives recorded. §8 corrected from "two entry points" to one open and three exits. |
| 4 | **The predicate set was asymmetric with the split it introduces**: `PLANNING` splits the in-flight side, but only `Settled()` was specified — and `ResolveRun` rewritten as `== RUNNING` silently stops preferring a planning run. | **Incorporated.** `InFlight()` added in §2.1.1 with a per-call-site membership table (the compiler finds the breakage, not the wrong answer), and `ResolveRun` named in §2.6. |
| 5 | **`planAndExecute`'s "indistinguishable on disk" invariant breaks unrecorded** (auto's stream gains a second `run_started`, chat's cannot), and **`--plan-only`'s rejection path has no run directory to write `rejected.json` into.** | **Incorporated.** §2.4 records the weakening as `confirm` observed on disk and restates the invariant it can keep; §3.1 makes `noteRejectedPlan` take either a run leg or a plan id, with the one-sentence rule. |
| 6 | **§9 contradicted §2.1 on where `PAUSED` comes from**, listing "snapshot gate" among `Derive`'s added facts — the re-entry point for the §1.2 defect. | **Incorporated.** Deleted from §9, with the reason stated and `runState` losing the argument rather than forwarding it. |
| 7 | **The strongest simpler design was not weighed**: liveness stays three-valued and the verdict moves *into* `runstatus` as a second composed value. | **Incorporated as a recorded alternative** in §7 — obeyed constraint, honest cost: the flattening (not `PLANNING`) is what grows `runstatus` two dependencies and forces every in-flight site to re-choose membership, and §9 confines the new `internal/graph` edge to `Of`. |
| 8 | **The closing half of a planning leg was unspecified**, and the `phase` field disambiguates only the opening side. | **Incorporated.** §2.2 fixes `outcome: "failed"` and says why `"paused"` would be actively wrong; §2.3 and §6 carry the two new stream shapes (one close for two opens; a failed leg with zero node events). |
| 9 | **§3's own principle was circular** — "it is a run because the enumeration can hold it" is the argument the ADR retires one page earlier. | **Incorporated.** §3.1 splits it into two independent premises — commitment creates a run, diagnosis gives it a verdict — and derives all three cases from them in a table. |
| 10 | **The `VERDICT` column would carry non-verdicts.** | **Incorporated.** Renamed to `STATUS` in §2.6; free, since ADR 0015 already recorded the column as not a contract. |

The review also confirmed four of this ADR's load-bearing claims against the
code, which is why they are unchanged: `PAUSED`-from-`outcome` genuinely covers
both pause shapes (`runOutcome` maps `*PausedError` and `*LimitPausedError` to
the same token), §2.3's stream-shape claim holds for `runfeed.InFlight` and
`walkNodeStates`, the refusal to derive `PLANNING` from an absence is right for
ADR 0015 §1's reason, and the budget check sitting *before* `c.plan` means
`StopBudgetExceeded` leaks no leg.

## 12. Implementation notes (2026-08-13)

The implementation followed §9 without reopening the decision. Two things it
learned that a later reader should not have to rediscover:

1. **`show` needed a boundary move §2.6 did not name.** The section says `show`
   "gains the run's status line", which is true, but `show` used to fail with
   `unknown run "<id>"` whenever `state.json` was missing — and §3 makes
   snapshot-less run directories ordinary (a planning phase, permanently a
   refused plan). Calling one of those "unknown" would be a lie about a
   directory the same binary just derived a status for, so the error moved to
   the RUN DIRECTORY's absence, which is what a mistyped id actually produces.
   A directory with no snapshot prints its status and a sentence saying why
   there is no per-node record yet.
2. **The exit-code mapping was extracted from `mainExitCode`.** §2.6's narrow
   invariant is turned into a test by §9, and a test that had to go through
   `run()` and `os.Args` could not assert it. `exitCodeForError` is that
   mapping alone; `mainExitCode` is it plus the stderr line and `os.Exit`.
   Nothing about the contract moved — 0 all passed, 1 failed, 2 paused and
   resumable.

The `PAUSED` hint names BOTH resume shapes (`--retry-failed` and
`--approve`/`--reject`) rather than picking one, because the reading surfaces
genuinely cannot tell a gate pause from a session-limit pause without asking the
snapshot's gate block — which §9 forbids as the re-entry point for the §1.2
defect. Naming the wrong one costs a command that refuses itself; naming neither
costs the reader the whole row.

## 13. Review round 2 (2026-08-13) — a code review of the implementation

The first review of the *code* rather than of this document. Seven findings; six
applied, one rejected. The three that changed behaviour are recorded above where
they belong (§2.1.1, §2.7) rather than only here.

| # | finding | disposition |
|---|---|---|
| 1 | **`executeGraph`'s `defer leg.Close("")` disarmed §2.7's sweep.** An infra failure before the scheduler (`WriteInitial`) closed the leg value, wrote nothing to the stream, and made the outer sweep a no-op — a clean exit 1 reading `ABANDONED` and printing the orphan warning. | **Applied**, and §2.7 amended: the empty close moves below the recorder, `executeGraph` defers its own failed-close above it, and an outcome is emitted only for a leg that announced itself. `TestPlanAndExecute_AFailedSnapshotWriteLeavesNoOpenLeg` reproduces the false alarm against the pre-fix code; `TestExecuteGraph_AFailedSnapshotClosesTheLegWithoutInventingOne` pins the second rule. |
| 2 | **The "stream has said nothing" cell read `FAIL` everywhere except the dashboard**, permanently for a directory whose stream could not be created. | **Applied** as `runstatus.Spoken`, shared by `runs list`, `show`, `watch` and the card; §2.1.1 amended. The fact it rests on is new (`runfeed.Leg.Started`), because a lone `run_finished` must not count as having spoken. |
| 3 | **`show` asserted a `rejected.json` that need not exist**: every settled snapshot-less `FAIL` got the refused-plan sentence, including the shape finding 1 produces. | **Applied**: the arm asks the directory. The fixture that asserted the unconditional sentence now writes the file, and a second case pins the `FAIL` that refused no plan. |
| 4 | **`runs list` and `show` read the snapshot and re-parse the graph twice** (once inside `Of`, once for the graph name and the per-node costs). | **Rejected, with the cost written at the call site.** De-duplicating it means either a third hand-composition of the rule in the CLI — `card.go`'s exists only for the dashboard's per-tick hot path and is pinned by an agreement test — or turning `runstatus` into a loader that hands back a snapshot and a graph. One shared rule is worth a second read on a command that runs once per invocation. |
| 5 | **`watch` rendered both of an auto run's `run_started`s as `▶ run started`**, so the `PLANNING`→`RUNNING` transition was invisible on the one live human view of the stream. | **Applied**: the line names the phase when the event carries one; an untagged leg's line is unchanged. |
| 6 | **`card.go` re-implements `runfeed.Leg`'s facts** off its one walk. | **Kept, deliberately** — the walk is the dashboard's hot path and `TestBuildCard_AgreesWithTheSharedRule` judges it against `runstatus.Of` on every fixture. A `Leg.Apply(Event)` reducer folded by both is the better shape and is worth an issue, not this branch. |
| 7 | **`runstate.SnapshotFileName` had not replaced the spellings it was added for** (`resume.go`, `serve/serve.go` each declared their own `"state.json"`). | **Applied**: both now alias the constant, as `lockFileName` already did. |

The review also confirmed, against the code, that `Derive` is the only
composition (`runs list`'s and the card's verdict logic are deleted, not moved),
that `ABANDONED`/`PAUSED` collapse into `FAIL` on none of the six surfaces, that
the exit-code mapping is untouched, and that `--plan-only` mints no run id at any
point.

## 14. Review round 3 (2026-08-13) — the record and the unguarded copies

A fresh-eyes round over the same implementation. It found no correctness defect
— it confirmed that `Derive` is total and pure, that its precedence is tested,
that the leg's lifetime is bracketed by an idempotent `Close` with tests for the
pre-scheduler exits, and that the exit-code/status agreement is asserted rather
than claimed. Every one of its eight findings is about something a reader is
promised and nothing checks: either the CHANGELOG not naming a change a v0.6.1
user will SEE, or a copy of a rule living somewhere no compiler and no test can
reach it. All eight are applied.

| # | finding | disposition |
|---|---|---|
| W1 | **`watch` prints a NEW first line for every run**, not only a planning one — recorded in §2.6 and in `Status.String`'s docstring, but the CHANGELOG's `watch` bullets covered only `▶ run started (planning)`. `watch <id> \| head -1` now returns a status word where it returned an event. | **Applied**: its own **Added** bullet, naming it as a new first line on stdout for *all* runs, with the pipeline hazard spelled out. |
| W2 | **The Go card-state constants ↔ CSS/JS token agreement was guarded by comments only.** Renaming a token left a card with no stripe, no dot and no chip, and a green test run — `dashboard_test.go` asserts the Go strings and never opens the assets. Worse, `dashboard.js`'s `LIVE_STATES` is a hand-copy of `Status.InFlight()`, the predicate `resolve.go` was rewritten to use *because* an equality test silently drops a new in-flight value: Go was made safe and the page was left filing a future in-flight run under settled. | **Applied** as `internal/serve/assets_test.go`, in `prose_count_test.go`'s shape: the token set is DERIVED (walk the enumeration until `String` falls through, map each value through `runState`, add the card's own `pending`/`unknown`) and the three embedded assets are read back. It found two live gaps in the shipped page — `style.css` had no `.dot.unknown`, so an `unknown` run's header chip drew an invisible dot, and `paintCounts` omitted `pending` entirely, so a run in the one state §2.1.1 exists for was counted in no chip at all. Both fixed; `paintCounts`'s literal is now a named `COUNT_ORDER` the test can find. |
| W3 | **`runState`'s docstring contradicted its body** — *"It has no default arm on purpose … a new value must come here and be given a colour rather than silently inheriting one"*, in a function with a `default:` returning `stateUnknown`. A seventh value did silently inherit one. | **Applied**, by making the promise real rather than by softening it: `TestRunState_CoversEveryDerivableStatus` walks the enumeration and fails on any value reaching the default (and on two values sharing a token). The docstring now says the switch is backstopped by a test, and names it. |
| I1 | `/api/cards`'s `state` value set changed (`gate-paused` gone at run level, `paused`/`planning` new) and the CHANGELOG described it only in colour terms. | **Applied**: a **Changed** bullet, scoped honestly — the dashboard's JSON is not a versioned contract, but it is machine-readable and DESIGN.md documents it. |
| I2 | **`buildCard` re-derived `OpenLeg`/`AnyLeg` from TIMESTAMP strings** (`walked.started != ""`), a second spelling of the leg rule that agrees with `runfeed.Leg` only because `Emit` always stamps `ts`. | **Applied**: `walkedStream` carries `open`/`anyLeg` as booleans, folded over the same events `runfeed.LastLeg` folds them over, leaving the two timestamps to the elapsed clock alone. This does not reopen §13's finding 6 — the walk stays local to the dashboard's hot path, and `TestBuildCard_AgreesWithTheSharedRule` still judges it against `runstatus.Of` — it removes the accidental coupling of a *status* to a *display* field. |
| I3 | `dashboard.js` called the state word *"the enumeration's own, lower-cased by the server"*, which is false for `PASS`→`passed` / `FAIL`→`failed`, and for the two tokens no status maps to. | **Applied**: the comment now names `serve.runState` as the chooser and says the page translates nothing. |
| I4 | **Four stdout lines changed shape and none was written down**: `Planning a graph for goal %q` gained the run id, the goal-cycle banner moved ahead of the plan and gained `, planning… —`, and `chat` split one header into a topology line before the `[y/N]` and a destination line after it. | **Applied**: a **Changed** bullet with all three (the fourth, the declined-plan spec path, was already its own bullet). |
| I5 | **Ctrl-C during a planner call now settles the run `FAIL`** (the deferred `Close(OutcomeFailed)`; §2.7 states it deliberately). Defensible — the alternative is a false `ABANDONED` plus an orphan warning about a `claude` the interrupt just killed — but interrupting `auto` while it thinks used to leave nothing and now leaves a row. | **Applied**: a **Known limits** bullet beside the `runs/` accumulation note, stating the choice and its alternative. |

The round's own summary is worth keeping as the shape of what was left: after
two reviews of the code, what remained was not logic but **unguarded copies of
it** — a predicate hand-copied into a file Go cannot see, a docstring stating an
invariant the switch below it did not hold, and four user-visible lines the
record did not mention. Two of the three were found only by asking what checks
each claim; the first two guards written to answer that question failed on the
first run, against shipped code.
