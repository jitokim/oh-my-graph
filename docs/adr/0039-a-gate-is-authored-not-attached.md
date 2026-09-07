# ADR 0039 — A gate is authored, not attached

**Status:** Proposed. Decision record only — no flag, no schema key, no new
seam, no behaviour change. The question put to this record is
[#265](https://github.com/jitokim/oh-my-graph/issues/265)'s shape 1, an
`auto --gate-before-sink` (or `--gate`) that lets trusted code splice a
`type: gate` into a planned graph after validation the way `--verify-cmd`
splices a `success_check.verify`. The answer is **no**, so there is no
implementation lane and no code to owe tests for. Shapes 2–4 of that issue are
different questions and §6 says where they go.

**Where the addresses point.** Written on branch `lane-reports` at `a14c24f` —
the tip this record was added to. Every `file:line` below was re-opened at that
commit rather than copied from the brief that preceded it; where a line and a
symbol could disagree, the symbol is the address that keeps.

**Date:** 2026-09-07

**Number.** `git ls-tree --name-only main docs/adr/` has maximum `0038`, so
`0039` is the next number. `0035` is still empty and still never assigned:
`git log --all --oneline --name-only --diff-filter=AD -- 'docs/adr/0035*'`
returns nothing, which is the same check ADR 0038's header records.

**What this record does not measure.** #265's field figures — 18 runs, 71 node
executions, 2 of them carrying engine-run evidence, ≈$402 spent of which ≈$195
in runs that ended FAIL — are the reporter's, from a corpus that is not on this
machine and was not re-derived here. Nothing below rests on them. The
observation they support — that the ergonomic path is the one with no evidence
and no gate — is accepted as the motivation for the issue and is not in dispute;
the refusal is about mechanism, not about whether the want is real.

---

## 1. Context

### 1.1 The ask, stated as the issue states it

`auto` gets a goal and writes the graph. A user who wants *the engine checks the
work, and a human approves before it publishes* can have the first half —
`--verify-cmd` is a user-supplied command that trusted code attaches to every
sink strictly after validation (ADR 0016 §2, `attachVerifyCommand`,
`internal/coordinator/verifycmd.go:198`) — and cannot have the second, because
`validatePlannedNodes` refuses a planned gate outright
(`internal/coordinator/coordinator.go:1051-1053`):

> `planned node %q is a gate node, which auto mode cannot run`

The issue does not dispute that refusal. It observes that the refusal is about
*the planner choosing where a human is interrupted*, and asks for the other
thing: the **user** asking for a gate at a position the **engine** picks. That
is a fair reading of the standing reason, which is written as a reason about the
planner (`coordinator.go:957-959`):

> no planned node may be a gate — every gate pauses a fresh run for a human
> decision, so an unreviewed auto-plan could park the run awaiting an approval
> nobody knows to give, forever

### 1.2 Where the parallel with `--verify-cmd` holds, and where it stops

It holds further than a quick reading suggests, and this record says so before
refusing it. `--verify-cmd` does not name a node: the operator supplies one
string, and trusted code finds the attachment points itself — `sinkNodeIDs`,
then the loop at `internal/coordinator/verifycmd.go:250-259`. So the
chicken-and-egg that killed the per-node form in ADR 0033 §4 candidate 1
(`docs/adr/0033-the-run-is-the-unit-of-evidence-not-the-node.md:501`:
*"Node ids are invented by the planner, so the mapping cannot be authored before
the plan exists"*) does **not** apply to `--gate-before-sink` as #265 poses it.
A structural attachment point needs no node id. Any refusal that leans on
chicken-and-egg is refusing a proposal the issue did not make. (It does apply,
unchanged, to a `--gate-before <node>` form, and that form is rejected here for
that reason alone.)

Where it stops is what the two attachments *are*.

`--verify-cmd` **sets a field on nodes that already exist**. The graph's shape
is untouched: same nodes, same edges, same sinks, and the re-parse at
`verifycmd.go:269-272` is there to re-derive a parsed timeout and re-check the
timeout ceiling, not to re-decide whether the graph is legal.

A gate **changes the topology**, and it changes it into a shape the loader has
two standing opinions about (§2.2(b)). It also changes what the *run* is: a gate is not
a node that runs and reports. Per ADR 0003 (`docs/adr/0003-gate-is-a-clean-stop-not-a-blocking-wait.md:29`)
and DESIGN.md:1532-1537, a gate does not block the scheduler waiting for a
human — oh-my-graph is a stateless CLI with no daemon, so it **ends the leg**:
`Scheduler.Run` drains in-flight siblings, writes the pause snapshot and returns
`*PausedError` (`internal/schedule/scheduler.go:720-730`), which the CLI maps to
exit 2 (`cmd/oh-my-graph/main.go:138-141`). Attaching a gate is therefore not
"one more field trusted code may set". It is trusted code deciding that this
run stops, on a graph nobody read.

## 2. Decision

### 2.1 The decision in one sentence

**A `type: gate` may only be written by the person who will be asked to approve
it, in a graph they can see — so `auto` gains no flag that splices one, and
`validatePlannedNodes`' refusal at `internal/coordinator/coordinator.go:1051`
stays exactly as it is.**

### 2.2 What breaks if a gate is attached post-validation the way `--verify-cmd` is

Concretely, in the tree as it stands at `a14c24f`. Two placements are possible
and neither survives — (a) and (b) — and then six runtime interactions,
(c) through (h), that hold whichever placement is chosen.

**(a) Placement A — the gate becomes the sink.** A terminal gate depending on
the old sinks is the natural "a human approves before it publishes" shape, and
it is refused by the code #265 cites as the precedent. `attachVerification`
skips a gate sink (`internal/coordinator/verifycmd.go:252`), and when every sink
is a gate it refuses the whole graph (`:262`):

> `graph %q ends in gate node(s) only, so the verify command has nowhere to
> attach: a gate's PASS is a human decision with no subprocess for an evidence
> command to be evidence about`

So under placement A the two halves #265 wants together are **mutually
exclusive today**: supply `--verify-cmd` and the run is refused; drop it and the
gate is the only check.

**(b) Placement B — a gate immediately upstream of every sink**, which is the
issue's own wording. This survives the sink filter and dies on the loader
instead, in two ways that a planned graph can reach:

- A sink with `handoff: session` gets a gate as its single parent, and
  `internal/graph/validate.go:659-666` refuses it: *"a gate spawns no subprocess
  and records no session to resume; use handoff: artifact"*. The planner is
  told it may write `handoff: session` — `internal/coordinator/coordinator.go:1731-1733`
  — so this is a plan the planner is invited to produce.
- A sink that declares a feedback arc puts the injected gate inside the loop
  body (`Graph.FeedbackBody` / `betweenSet`, `internal/graph/feedback.go:47-83`:
  a node is in the body when it is an ancestor of the declarer and a descendant
  of the target, which an inserted parent of the declarer is), and
  `internal/graph/feedback.go:188-193` refuses it: *"replaying its recorded
  decision on a later round would silently re-approve work the human never
  saw"*. The planner is invited to declare feedback arcs too
  (`coordinator.go:1760-1763`), and `coordinator.go:1409` already reasons about
  a sink inside a feedback loop as an ordinary shape.

Both refusals would fire at the post-injection re-parse — the step
`verifycmd.go:269-272` performs for the field case — which is **after the
planner call has been paid for**. Today a plan that reaches injection has
already passed `validatePlannedNodes`; a topology mutation reintroduces
"the graph became invalid after it was accepted", which is the failure mode
`attachVerification`'s re-parse comment (`verifycmd.go:221-226`) exists to keep
narrow.

**(c) `resume`.** A gate pause is continued by
`oh-my-graph resume <run-id> --approve <gate-id>`
(`internal/schedule/errors.go:98-103` prints exactly that). Under a
`--gate-before-sink` the `<gate-id>` is an identifier **trusted code minted**,
in a graph the user never wrote — the first time they meet it is in the pause
message. Worse for the combination the issue is actually about: a run started
with `--verify-cmd` carries that command in its snapshot, and the resumed leg
refuses to replay it from disk. `ReattachVerifyCommand`
(`internal/coordinator/verifycmd.go:378`, called at
`cmd/oh-my-graph/resume.go:387`) drops every snapshot-borne verification and,
when this invocation supplies none, fails with `*SnapshotVerifyError`
(`verifycmd.go:349-358`) demanding `--verify-cmd` be re-supplied. So every gate
resume of an evidence-carrying auto run needs *two* flags, one of which the
printed hint does not name: `runstatus.PausedHint`
(`internal/runstatus/runstatus.go:383-388`) prints the `--approve` form only,
and its own comment (`:378-381`) already records that it cannot cheaply know
whether this is such a run. Attaching gates turns that acknowledged rough edge
from a rare case into the standard path.

**(d) `internal/runstate` and `state.json`.** The pause is durable: the
scheduler calls `SnapshotRecorder.RecordPause` once, after the drain
(`internal/schedule/scheduler.go:726`), which sets `Gate.PausedAt` and records
the `pause` decision (`internal/runstate/recorder.go:116-122`,
`internal/runstate/runstate.go:323-328`). Two consequences. First, the snapshot
carries `snap.Graph` — the injected topology — so a resumed leg replays a shape
the user never authored, and `continueRun` rebuilds from exactly that
(`cmd/oh-my-graph/resume.go:339`). Second, the recorder rewrites the WHOLE
snapshot on every `RecordNode`, so anything the resumed leg fails to carry
forward is ERASED by the first settling node — the hazard
`cmd/oh-my-graph/resume.go:591-613` documents field by field. An injected gate
adds a class of snapshot content whose only author is the engine.

**(e) The run lock.** Nothing holds it while the human is away, and that is
correct rather than a bug: a leg must hold `resume.lock` before its first event
and still hold it after its last (`cmd/oh-my-graph/runlock.go:5-18`,
`internal/runstate.AcquireLock`), and a pause closes the leg, so the lock is
released. `runstatus.Derive` then reads leg-closed + `paused` outcome as
`Paused` (`internal/runstatus/runstatus.go:215-216`). The consequence to name is
that **nothing on disk marks a gate-paused run as occupied** — which is fine
when a human is watching and is the whole problem when nobody is (see (g)).

**(f) The `serve` live view.** The browser can decide a paused gate (ADR 0014),
but only from the standalone `serve` process: `WithGateResumer`
(`internal/serve/gate.go:34-41`) is wired by that process alone, *"the only
process that can ever be looking at a paused run (ADR 0003 — the pause exits the
run process, taking its embedded live view with it)"*. A run's own embedded live
view — the one `auto` opens by default — dies at the exact moment the gate
becomes decidable, and its gate routes answer 409 (`internal/serve/gate.go:27-29`).
So `auto --gate-before-sink` would hand the average user a page that goes dark
when it becomes useful, unless they separately started `serve`.

**(g) An unattended run nobody is watching.** This is the reason
`coordinator.go:957-959` already gives, and the tree makes it sharper than the
comment does. A paused run is **settled**: `Status.Settled` includes `Paused`
(`internal/runstatus/runstatus.go:124-132`) and `Status.InFlight` is
`Planning || Running` only (`:143-145`). The supervisor contract built on that
is documented at `cmd/oh-my-graph/main.go:29-34`:

> `until oh-my-graph runs list --exit-in-flight >/dev/null; do sleep 30; done`

That loop **returns immediately** for a run parked at a gate. Nothing lies —
PAUSED is exactly what happened, and `PausedHint` says how to continue — but an
operator who scripted "wait until my runs are done" is told they are done while
one waits for an approval nobody knows to give. Under `run <graph.yaml>` that is
the author's own gate, in their own file. Under an attached one it is a stop the
engine chose.

**(h) The goal loop, `--max-cycles ≥ 2`.** #265 asks whether a gate pause ends
the cycle or suspends it, and asks for the answer to be recorded rather than
discovered. Recorded: **it ends the whole loop.** `ExecuteCycle`'s contract is
that a non-nil error stops the loop and propagates verbatim
(`internal/coordinator/goal.go:68-81`), and the parenthesis at
`internal/coordinator/goal.go:75` states today's premise outright —
*"a session-limit pause is the only pause an auto run can hit"*. That sentence
is true because of the refusal this record keeps. Attaching gates would falsify
it, in a comment nothing compiles, and put a second pause class into an
unattended multi-cycle loop that stops at cycle 1 with exit 2.

### 2.3 Whether ADR 0033 bears on this — it does, in both directions

Stated plainly because the question was asked directly.

**It bears against the combination, at the sink.** ADR 0033 §2.4
(`docs/adr/0033-the-run-is-the-unit-of-evidence-not-the-node.md:392-394`) keeps
`attachVerification`'s sink filter *and* its refusal when every sink is a gate,
by name and on purpose. Placement A above is not blocked by an accident of
implementation; it is blocked by a decision that record already took. "Evidence AND a
gate" cannot be had by putting a gate where the evidence attaches — one of the
two has to move, and moving either is a new decision, not a flag.

**It bears in the reporter's favour on one point, and this record concedes it.**
ADR 0033 chose "nobody" for per-node evidence (§2.1, `0033:207-222`), so the
evidence half of #265's want was already answered at run granularity. The
issue's underlying observation — that the ergonomic path is the one with the
least evidence — is the same observation `0033:29-58` opens with, from a
different corpus.

**It does not bear via chicken-and-egg**, and §1.2 says why: `0033:501` and
`0033:258-261` are about a per-node mapping keyed on planner-invented ids. A
structural attachment escapes that argument, exactly as `--verify-cmd` does.
Citing it against `--gate-before-sink` would be a refusal aimed at the wrong
proposal.

### 2.4 What stays exactly as it is

- `validatePlannedNodes`' gate refusal — `internal/coordinator/coordinator.go:1051-1053`,
  with its reason at `:957-959`.
- `attachVerification`'s gate-sink skip and all-gate refusal —
  `internal/coordinator/verifycmd.go:252` and `:262`, as ADR 0033 §2.4 already
  kept them.
- The loader's two gate rules — `internal/graph/validate.go:659-666` (no gate
  as a session parent) and `internal/graph/feedback.go:188-193` (no gate in a
  feedback body).
- `--verify-cmd` on `auto`, unchanged, on both legs (ADR 0016 §4).
- **No new exec seam.** A gate spawns nothing; the four-seam invariant is not
  approached.

## 3. The path that exists today, with its cost stated

The combination is reachable, in three steps, and this record names the friction
rather than presenting it as free:

1. `oh-my-graph auto "<goal>" --plan-only` — the planner runs, no node does, and
   the spec is kept (`notePlanOnlyPreview`, `cmd/oh-my-graph/main.go:759-772`).
2. Edit that spec: add `type: gate` with its `depends_on` (three lines — the
   shipped example is `approve-merge`, `graphs/merge-shepherd.yaml:764-766`, the
   only `type: gate` in `graphs/`), and put `success_check.verify` on whichever
   nodes you actually want the engine to check.
3. `oh-my-graph run <path>` — the printed command at `main.go:766-769`.

Two frictions, both real:

- **The saved spec is `graph.json`, not YAML** (`generatedSpecFileName`,
  `cmd/oh-my-graph/main.go:1135`). The path exists, but step 2 is editing
  generated JSON, which is precisely the ergonomic gradient #265 measured. This
  is the strongest argument in the issue and it survives this refusal intact.
- **`resume` refuses `--verify-cmd` on a hand-written graph**
  (`checkVerifyCommandApplies`, `cmd/oh-my-graph/resume.go:306-322`). Nothing is
  lost — the graph's own `verify:` round-trips untouched — but a user who
  learned the flag on `auto` will meet a refusal on the resumed leg of the graph
  they were just told to write.

## 4. Consequences

- A user who wants evidence and a human gate together writes a graph. That is
  the same answer `0030:486` and `0033:261-263` give, so no new asymmetry is
  introduced.
- `internal/coordinator/goal.go:75`'s premise — one pause class in `auto` —
  stays true, and the goal loop keeps one exit shape to reason about.
- `runs list --exit-in-flight` keeps meaning what `cmd/oh-my-graph/main.go:29-34`
  says it means for `auto` runs: nothing an auto run does parks it in a state
  the loop reports as finished while a human is owed a decision.
- The cost is borne by the user #265 describes, and it is not zero. This record
  does not claim the want is unreasonable; it claims the mechanism is wrong.

## 5. Alternatives considered

| # | shape | verdict |
| --- | --- | --- |
| 1 | **`auto --gate-before-sink`** — inject upstream of every sink | **Rejected.** §2.2(b): collides with `validate.go:659-666` and `feedback.go:188-193` on plans the planner is invited to write, after the planner call is paid; plus §2.2(c)–(h). |
| 2 | **`auto --gate` at the sink** — a terminal gate | **Rejected.** §2.2(a): `verifycmd.go:262` refuses the graph as soon as `--verify-cmd` is also supplied, which is the pairing the issue exists for. |
| 3 | **`--gate-before <node>`** — operator names the position | **Rejected on chicken-and-egg**, `0033:501` verbatim: the node ids are the planner's, invented after the flag is typed. |
| 4 | **Relax `validatePlannedNodes` and let the planner write gates** | **Rejected, and not asked for.** `coordinator.go:957-959` is the standing reason and #265 explicitly does not dispute it. |
| 5 | **Nobody — `--plan-only`, edit, `run`** | **Chosen (§2.1, §3).** |

## 6. What this record does not decide

#265 lists four shapes; only shape 1 is answered above. The other three are
live, cheap and unblocked by this refusal:

- **Shape 2 — ship a gated graph, not only a fragment.** Worth filing, with one
  correction to its premise: `graphs/fragments/gated-lane.yaml` does *not*
  contain a `type: gate`. Its "GATE PAIR" (`gated-lane.yaml:15-20`) is a
  narrowed `success_check` plus a `feedback:` arc — a different mechanism with
  a colliding name. `type: gate` occurs exactly once in `graphs/`, and the
  count is two readings agreeing rather than one grep: `grep -rn "type: gate"
  graphs/` returns `graphs/merge-shepherd.yaml:765`, and enumerating EVERY
  `type:` declaration in the tree (`rg -n '^\s*type:' graphs/`, 28 lines)
  returns that same one line and 27 `claude-run`s. A gate cannot be declared by
  omission — the default is `claude-run` — so the enumeration is exhaustive.
  "No shipped graph pairs a human gate with a review lane" is therefore true,
  and truer than the issue states it.
- **Shape 3 — `--input-file`, or per-graph input defaults.** Orthogonal to
  gates entirely, and it addresses the reason the issue gives for preferring
  `auto` in the first place. Not decided here.
- **Shape 4 — say the tradeoff at the point of choice.** `auto` already refuses
  for want of build evidence (ADR 0030) and could name what it cannot express.
  Cheapest of the four; needs its own decision about where the sentence goes,
  because `auto`'s startup output is already dense.

## 7. Falsification — what would show this wrong

- **A planned graph shape that provably cannot hit either loader refusal.** If
  `handoff: session` and `feedback:` were both refused for planned graphs, §2.2(b)'s
  two collisions would be unreachable and placement B would cost only the
  runtime interactions. They are not: `coordinator.go:1731-1733` and `:1760-1763`
  invite both.
- **A pause that does not end the leg.** If a gate ever became a blocking wait,
  (c)–(h) would each need re-deriving. ADR 0003 rejected that shape on
  "no daemon", and SECURITY.md:18 still says so.
- **`--exit-in-flight` learning about PAUSED.** If a supervisor could wait on
  "settled AND not awaiting a human", (g) would lose most of its force. That is
  a change to `internal/runstatus`'s enumeration (ADR 0023), and the cheap
  version of it is already forbidden: telling a gate pause from a limit pause
  means reading the snapshot's `gate.paused_at`, which `Facts` deliberately
  excludes as *"the precise re-entry point for the §1.2 defect"*
  (`internal/runstatus/runstatus.go:153-156`). It would be a prerequisite for
  revisiting this record — not a consequence of it.

## 8. References

- [#265](https://github.com/jitokim/oh-my-graph/issues/265) — the report this
  record answers, including the four shapes and the field figures this record's
  header declines to restate as measured.
- **ADR 0016** — `docs/adr/0016-build-evidence-is-a-user-supplied-engine-command.md`.
  §2 is the "trusted code attaches after validation" shape #265 asks to extend;
  §4 is the resume half that (c) turns on.
- **ADR 0033** — `docs/adr/0033-the-run-is-the-unit-of-evidence-not-the-node.md`.
  `:392-394` keeps the gate-sink filter and the all-gate refusal (§2.2(a),
  §2.3); `:501` and `:258-261` are the chicken-and-egg §1.2 declines to borrow;
  `:261-263` is the `--plan-only` → `run` path §3 names.
- **ADR 0003** — `docs/adr/0003-gate-is-a-clean-stop-not-a-blocking-wait.md:29`.
  A gate stops the run by exiting; everything in §2.2(c)–(h) follows from that
  and from DESIGN.md:1532-1553.
- **ADR 0014** — `docs/adr/0014-the-live-view-can-decide-a-paused-gate.md`. The
  browser path §2.2(f) is about, and the reason only a standalone `serve` has it.
- **ADR 0015** — `docs/adr/0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md`.
  The lock-and-leg rule §2.2(e) reads a paused run through.
- **ADR 0023** — `docs/adr/0023-a-run-has-one-status-and-planning-is-one-of-its-values.md`.
  PAUSED as a status, and the derivation §7's third falsifier would have to
  reopen.
- **The refusal itself** — `internal/coordinator/coordinator.go:1051-1053`, with
  its reason at `:957-959`. Kept.
