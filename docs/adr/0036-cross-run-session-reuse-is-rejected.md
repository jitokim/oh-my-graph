# ADR 0036 — Cross-run session reuse is rejected: a resumed session is not a reused node

**Status:** Rejected. The question put to this record was whether a node may
`--resume` a session created by a **previous run**, where today `handoff:
session` resumes a parent's session only within the same run. The answer is
**no**. There is no implementation lane, no schema field, no flag, and no code
to owe tests for. Nothing in the engine changes: no graph key, no `state.json`
field, no new seam, no behaviour change. This record supersedes ADR 0008, which
recommended *defer*, and it rejects on two grounds ADR 0008 never reached — the
product identity test (§2.2) and the cost in run state (§2.3).

**Where the addresses point.** Written against `8658ebd` — the `main` this
branch left. Every `file:line` below was read at that commit and resolves
exactly there. Because this record decides *not* to build something, all of its
addresses point at code and documents that exist; none points at code that does
not exist yet. Two addresses are outside this repository — `notes/open.md` and
`notes/roadmap.md` in `oh-my-graph-hq` — and are marked as such where they
appear.

**Where the evidence comes from.** The investigation this decision rests on was
done before this record and is **not redone here**. Its verdict is recorded in
the backlog (§1.1). The prior CLI research is ADR 0008's and is quoted as ADR
0008's, un-re-verified (§1.2). The corpus figure the argument rests on, in
§2.4, is quoted by line from
`docs/measurements/0028-feedback-quote-corpus.md` and was not re-measured; so
are the three neighbouring censuses §2.4 sets beside it, each cited to its own
measurement. Everything this record did for itself is re-resolving the addresses
above at `8658ebd`, which is also what satisfies the staleness rule in §1.1.

**Date:** 2026-08-24

## 1. Context

### 1.1 The question, and the rule that nearly disqualified it

The backlog item, verbatim, at `oh-my-graph-hq` `notes/open.md:42`:

> `| 8 | **cross-run 세션 재사용 ADR** — 조사 완료(ergonomic이지 moat 아님) | 결합가치 판단 | 2026-08-01 |`

Three parts of that row are load-bearing here, and all three point the same way:

- the prior investigation's verdict is **already recorded** — *"ergonomic이지
  moat 아님"* — so this record is not being asked to discover whether the
  feature is pleasant. It is;
- the next action is **결합가치 판단** — judge the combined value — not
  "implement". A judgement whose honest outcome may be *no* is what was asked
  for;
- the `확인:` date is **2026-08-01**, and that file's own rule 2
  (`notes/open.md:10-11`, verbatim: *"**2주 지난 항목은 코드 대조 없이 결정에
  올리지 않는다.**"*) forbids putting it to a decision without a fresh code
  check. From 2026-08-01 to this record's date is **23 days**.

**That rule is satisfied, and this is the sentence that satisfies it:** every
code address in §1.3, §2.3 and §2.4 was resolved at `8658ebd` in the course of
writing this record, not carried over from the 08-01 investigation. What was
*not* re-done is the CLI-behaviour research (§1.2) and the corpus measurement
(§2.4) — both of which are cited to their own written addresses rather than to a
session.

### 1.2 The record this supersedes: ADR 0008

This question already has an ADR.
`docs/adr/0008-cross-run-session-reuse-is-deferred.md:3` reads *"Status:
Proposed (recommendation: **defer**)"*, dated `0008:4` to 2026-08-02. It
considered a concrete shape (`0008:24-28`): a `session: <name>` node field
naming a persistent identity, backed by a registry at
`~/.oh-my-graph/sessions.json` mapping name → `{session_id, project_path,
created_at, model}`.

Its evidence base is explicitly recorded research that ADR 0008 itself did not
re-verify (`0008:30-31`: *"recorded research against the real CLI (not
re-verified here; these are the facts this ADR reasons from)"*), and it is
**not re-verified here either**. Quoted as ADR 0008's, three of its five facts
are used below:

- `0008:33-35` — `claude -p --resume <session_id>` can reopen a finished run's
  session for roughly **30 days**, after which *"the transcript is
  garbage-collected and the id resolves to nothing."*
- `0008:38-41` — resume re-hydrates the full transcript: *"It is an *ergonomic
  continuity* feature, not a token saver."* This is the same word the backlog
  row uses, arrived at independently.
- `0008:42-43` — session lookup is **project-directory scoped**: *"the session
  is found relative to the cwd it was created in, not by id alone from
  anywhere."*

ADR 0008 named its own re-open conditions at `0008:97-102`: a CLI liveness or
expiry check, a contractual retention horizon, or lookup by id independent of
the creation directory — plus an answer for concurrent access to
`sessions.json`, *"a new piece of global mutable state; today the only cross-run
state is the run dirs themselves."*

**What this record changes about that.** ADR 0008 deferred on properties of the
**substrate** — the CLI's retention, its lookup scoping. Those may yet firm up.
This record rejects on two properties of **this product** that would not move if
they did: the identity test (§2.2), which the substrate cannot satisfy on the
feature's behalf, and the cost in run state (§2.3), which gets *worse*, not
better, if the substrate becomes reliable enough to build on. So the re-open
conditions at `0008:97-102` are no longer sufficient on their own; §5 states
what would be.

This record does not edit `0008`. Its own scope is one new file, so `0008`'s
status line still reads *Proposed (recommendation: defer)* at `8658ebd`; the
next editor of that file should mark it superseded by this one, and §3 records
that as owed.

### 1.3 What within-run session handoff is today

The promise, from the spec. `DESIGN.md:522` is the section heading and is itself
the shape of the promise — *"## Handoff — artifact default, session opt-in
(committed)"* — and `DESIGN.md:528-535` states the session half, including
*"dependent runs `--resume <session_id>` of its single session-parent (same
cwd/git scope)"* and *"a session node must have EXACTLY ONE parent … Rejected at
load time."* The schema surface is one line, `DESIGN.md:207`: `handoff: artifact
# artifact(default) | session`.

Note what the *default* half already assumes, at `DESIGN.md:523-527`: an
artifact lives at `~/.oh-my-graph/runs/<run-id>/<node-id>.out`. **The artifact
half of handoff is run-scoped in its very path.** Any cross-run session
mechanism would make the opt-in half the only half that crosses runs, which is
the asymmetry §2.3 prices.

The mechanism, in four parts:

- **Enforcement is at load time, in graph validation, not in handoff.**
  `internal/graph/validate.go:643` is
  `func (g *Graph) validateHandoffConstraints() []error`; the parent-count
  refusal is `internal/graph/validate.go:649-656` — `if len(n.DependsOn) != 1`
  → *"handoff: session with %d parents — a session-handoff node must resume
  exactly one parent's session; use handoff: artifact for a root node or for
  fan-in"*. A gate parent is refused at `:658-666` (*"a gate spawns no
  subprocess and records no session to resume"*). The rule is numbered in the
  package's own contract list at `internal/graph/validate.go:100`: *"5. a
  session-handoff node has exactly one parent — the session it resumes"*. A
  second, loop-scoped refusal lives at `internal/graph/feedback.go:197`: a body
  node may not name an out-of-body session parent, because *"a later round's
  --resume would continue a session an earlier round already continued"*.
- **The lookup is a map read in one process.**
  `internal/handoff/handoff.go:524` is `ResumeSessionFor(node graph.Node)`; the
  read is `internal/handoff/handoff.go:537` — `session, ok := h.sessions[parent]`
  — and the miss is `:539-544`, *"node %q cannot resume: parent %q has no
  recorded session id"*. The scheduler calls it at
  `internal/schedule/scheduler.go:1317` and carries the result into the
  invocation at `:1336`.
- **The invocation carries it to the CLI.** `internal/runner/runner.go:98` is
  the field `ResumeSession string`, documented at `:93` as *"empty unless this
  node is resuming a session-parent."* Claude renders it at
  `internal/runner/claude_protocol.go:62` — `args = append(args, "--resume",
  spec.ResumeSession)`; codex at `internal/runner/codex_protocol.go:46`.
- **One place deliberately erases it.** `startCold`
  (`internal/schedule/scheduler.go:1119`) sets `invocation.ResumeSession = ""`
  at `:1121`, for the reason stated at `:1116-1118`: *"Both are retries and both
  must be cold — a `handoff: session` node that resumed its parent here would be
  handed the quote's FRESH-SESSION paragraph as a lie."* That assignment is the
  only one in the tree, and it is reached from two callers: `prepareRetry`
  (`internal/schedule/scheduler.go:1105-1106`) for an in-leg retry, and the
  prior-leg retry at `internal/schedule/scheduler.go:763-764`. The feedback
  path clears a *different* field — `internal/schedule/feedback.go:263-265`
  blanks the `SessionID` of the emitted `node_retried` event, not any
  invocation's resume.

**Two writes put an id into that map, and both are same-process.**
`PersistOutput` (`internal/handoff/handoff.go:211`) writes at `:222`; `Seed`
(`internal/handoff/handoff.go:243`) writes at `:247`. `Seed`'s own doc, at
`internal/handoff/handoff.go:227-234`, names the exact gap a cross-run mechanism
would widen: it carries *"the session id (so a handoff: session child can
--resume the parent it never watched run in this process)"*. That parenthesis is
the whole of today's cross-**process** story, and §2.3 is about why crossing a
**run** is a different thing from crossing a process.

The only existing statement about *where* a resumed session may be looked up is
advisory: `internal/handoff/session_lint.go:64` warns when a session child's
worktree differs from its parent's, because *"claude's session lookup is
project-directory-scoped, so the resume may start cold or attach to the wrong
project"*.

**What would NOT be in the way, stated plainly so nobody re-derives it.** A
cross-run resume needs **no fifth exec seam**: `CLIRunner` already spawns
`--resume` (`internal/runner/claude_protocol.go:62`), so the four-seam invariant
(`CLAUDE.md`, ADR 0002, enforced by `internal/invariants`) is not approached,
let alone weakened. And run directories are already addressable by id —
`runsRoot()` and `runDirFor(runID)` at `cmd/oh-my-graph/main.go:1907` and
`:1913`, with the `OMG_HOME` override at `:1895` — with `cmd/oh-my-graph/goal.go:215`
already loading a snapshot by run id as the nearest precedent for addressing a
run other than the current one. Neither of these is an objection, and this
record does not pretend otherwise. The objections are §2.2, §2.3 and §2.4.

### 1.4 What this record did not determine

- Whether the CLI's retention horizon has changed since `0008:33-35` recorded
  it. Not re-verified — `<!-- 미측정 -->`
- Whether the schema-2 share of the run corpus has moved since
  `docs/measurements/0028-feedback-quote-corpus.md` measured it. Not
  re-measured — `<!-- 미측정 -->`
- How many operators have ever wanted this. The backlog row records a verdict,
  not a demand count; `0008:17-22` describes reader feedback about *positioning*
  and not about a count of requests — `<!-- 미측정 -->`
- How often a cross-run resume would in practice find a live id versus a
  garbage-collected one. Unknowable without spending resume attempts, per
  `0008:55-56` — `<!-- 미측정 -->`

## 2. Decision

### 2.1 The decision

**oh-my-graph does not ship cross-run session reuse. No node field, no
registry, and no reference from one run's state into another run's directory.
`handoff: session` remains what `DESIGN.md:528-535` says it is: a resume of a
single session-parent, within one run.**

The deliverable of this record is the decision and §3's owed documentation.

### 2.2 (a) It serves no identity item — and that is dispositive

The identity block, `oh-my-graph-hq` `notes/roadmap.md:27-28`, verbatim:

```
node 생성 · graph 생성 · node 재사용 · 노드가 스킬을 쓸 수도 있음
        — 명시적으로(YAML) or 자동으로(auto)
```

It is a test and not a slogan because of the sentence above it,
`notes/roadmap.md:21-22`, verbatim: *"**이 아래 모든 항목은 이 기준으로
판단한다.** … 어떤 제안이 이 기준에 안 걸리면, 좋은 아이디어여도 이 제품의
것이 아니다."* Taken item by item:

- **node 생성 — no.** A cross-run resume creates no node. It changes how an
  already-authored node starts.
- **graph 생성 — no.** It authors no graph and changes no graph's shape.
- **node 재사용 — no, and this is the item it would claim, so it is argued
  rather than asserted.** In this product "node 재사용" has a settled referent:
  reusing a node's **definition**. That is what a fragment is —
  `docs/adr/0013-a-fragment-is-a-load-time-node-splice-not-a-runtime-concept.md`
  says so in its title, and ADR 0027 (`docs/adr/0027-the-reusable-unit-is-a-loop-not-a-node.md:9-12`)
  says ADR 0013 *"gave this repo a reuse mechanism: a fragment file, cited with
  `use:` + `with:`, spliced by the file loader before validation"* —
  and ADR 0029 extended it. Cross-run session reuse reuses none of that. It
  reuses a **transcript**: conversation state that no author wrote, that no
  fragment can carry, and that ADR 0013's title expressly puts on the other side
  of the line it draws (*"not a runtime concept"*). Two things that both contain
  the word *reuse* are not the same item, and the identity names the one this
  repo already has three ADRs about.
- **노드가 스킬을 쓸 수도 있음 — no.** Untouched. `notes/roadmap.md:59-60` narrows
  that item to skills, and this is not one.

**And the axis fails too, which is the part that would still fail if one of the
four items were granted.** `notes/roadmap.md:31-32`, verbatim: *"마지막 줄이
축이다. **같은 네 가지를 두 가지 방식으로** 제공하는 것이지, 자동화가 별도
기능이 아니다."* A cross-run session reference cannot exist on the `auto` side of
that axis at all. The planner authors a graph **before** any of its runs exists;
a foreign run id or session name is not something a plan can invent, and
`internal/coordinator` has no source for one. So the feature would live in the
YAML half only — the first structural asymmetry the identity's own axis
forbids. This is not a matter of implementation order: there is no artifact the
planner could read that would make a prior run's session the right one to
continue.

One more promise it would strain, `notes/roadmap.md:58`: *"**기존의 Claude Code
셋팅에서 잘 돌아갑니다**"*. ADR 0008's shape requires a registry at
`~/.oh-my-graph/sessions.json` (`0008:25-27`) — new global mutable state that no
existing Claude Code setup has, and that `0008:100-102` already flagged as
needing a concurrency answer.

**Dispositive.** By `notes/roadmap.md:21-22`'s own words, a proposal that does not
land on the identity is not this product's, however good it is. The backlog's
recorded verdict — *"ergonomic이지 moat 아님"* (`notes/open.md:42`) — says the
same thing in the vocabulary of value rather than of identity: an ergonomic is a
convenience, and this product's four items are not conveniences. §2.3 and §2.4
are therefore not needed to reach the answer. They are here because a rejection
that names only a rule, and not a cost, is a rejection nobody can weigh later.

### 2.3 (b) What it costs in state: a datum becomes a reference

Today a session id is a **datum**. `internal/runstate/runstate.go:166` —
``SessionID string `json:"session_id"` `` — inside `NodeRecord`, and its doc at
`:162-165` is the most relevant sentence in the file:

> *"SessionID is the model CLI's session id. This is the one datum resume cannot
> recompute: without it a handoff: session child cannot --resume its parent on
> the second leg, because the id lived only in Handoff.sessions in memory on the
> first leg. Handoff.Seed reads it back out of here."*

Every word of that is scoped to one run: *this* snapshot, *this* run's second
leg, *this* run's `Handoff`. A cross-run mechanism converts it into a
**reference** — a pointer whose target is in a directory this snapshot does not
own and cannot see. Everything below follows from that one conversion.

**The state that has no place to put it.**

| what | address | why it is implicated |
| --- | --- | --- |
| `NodeRecord.SessionID` | `internal/runstate/runstate.go:166` | Keyed only by node id (`:156-157`: *"Its map key in Snapshot.Nodes is the node id"*). There is no field for which run an id belongs to. |
| `Snapshot.Nodes map[string]NodeRecord` | `internal/runstate/runstate.go:432` | The key is a node id local to **this** graph. A foreign run's session has no node id in this graph. |
| `Snapshot.RunID` | `internal/runstate/runstate.go:364` | Documented `:362-363` as the run *"this snapshot belongs to … so a snapshot is self-identifying if copied out of its directory."* It is the file's only run identity, and it is singular. |
| `const Schema = 3` | `internal/runstate/runstate.go:60` | Any new field is a schema question, and `Load` refuses a mismatch (`:610`, refusal at `:620-622`, `*SchemaMismatchError` at `:528`). |
| `Snapshot.MarshalJSON` | `internal/runstate/runstate.go:468` | The serialization boundary where `Runtime` is canonicalized so *"no writer can produce a snapshot without it"* (`:367-371`). A cross-run reference faces the same must-always-be-present question and has no comparable default. |
| `Write` | `internal/runstate/runstate.go:565` | Writes one snapshot into one run directory, creating it `0o700` at `:573-576`. A referenced run is a second directory this function does not touch and must not. |
| `Snapshot.CompletedNodes` / `SettledNodes` | `internal/runstate/runstate.go:492`, `:514` | Resume topology, `VerdictPass` only (`:159-160`). A foreign session's node belongs to neither set, and neither function has a way to say so. |
| `SnapshotRecorder.RecordNode` | `internal/runstate/recorder.go:70` | The only writer of a `NodeRecord`, and it is per-node-of-this-run. |
| `NewSnapshotRecorder(path, base)` | `internal/runstate/recorder.go:33` | One path, one base; `:30` says the base carries what *"an earlier leg"* knew — and "earlier leg" means the same run. |

**Every consumer that would have to learn about it.** Named individually,
because "the readers would need updating" is the sentence under which this cost
usually disappears.

1. **`resume`** — `cmd/oh-my-graph/resume.go:60` (`runResume`), the leg's
   snapshot load at `:143`, leg body `continueRun` at `:334`, recorder re-armed
   at `:553`. The rehydration itself is `cmd/oh-my-graph/resume.go:424` —
   `h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)` — reading `rec` out of
   **this** run's snapshot only, and `:507` carries the same id into the resumed
   leg's ledger. This is the single line that would have to learn the
   difference between "my parent's session" and "some other run's session", and
   `Seed`'s signature (`internal/handoff/handoff.go:243`) has no place to record
   which it got.
2. **`runs list`** — `cmd/oh-my-graph/runs.go:150` (`listRuns`), dispatched from
   `:26`, with the per-run read at `cmd/oh-my-graph/runs.go:249` inside
   `summarizeRun` (`:235`). A list of runs would become a list in which some
   rows depend on other rows' directories.
3. **`serve`** — `internal/serve/serve.go:314` (`New(runDir, runID string)`) —
   the constructor takes **one** run dir — with the snapshot read at `:513` and
   the gate write path taking this run's lock at `internal/serve/gate.go:252`.
   `internal/serve/serve.go:345` records the stance a cross-run read would
   test: the server *"creates, writes and removes nothing (ADR 0015 §1,
   runstate.ProbeLock)."*
   The dashboard card reads a snapshot per run at `internal/serve/card.go:158`,
   in the hot path noted at `:123-124`; `internal/serve/resolve.go:50`
   (`ResolveRun`) walks the runs root.
4. **The run feed** — `internal/runfeed/reader.go:69` (`Walk`), `:121`
   (`InFlight`), `:167` (`LastLeg`), `:31` (`ReadAccounting`); the writer is
   `internal/runfeed/runfeed.go:259` (`NewStreamWriter`), and a session id
   already reaches it via `internal/schedule/scheduler.go:780`.
   `internal/runfeed/reader.go:114` states the split the feed depends on:
   *"lock answers whether that leg's
   writer is alive (runstate.ProbeLock)"* — runfeed itself knows nothing of
   locks, and a cross-run reference would give it a third thing to know nothing
   about. The consumer contract is public (`docs/RUN-FEED.md`), so a new event
   or field is a contract change, not an internal one.
5. **`show`, and the ledger row behind it** — `cmd/oh-my-graph/show.go:177`
   (`showRecords`) converts each `NodeRecord` into a `ledger.Record`, taking the
   id at `:182`; its doc at `:173-176` says it is *"the same runstate→ledger
   conversion `resume` performs … so the two views cannot disagree about what a
   node record means."* The rendering is `internal/ledger/ledger.go:312`
   (`sessionCell`), in a table whose columns are one run's nodes and which has
   nowhere to say that a row's session belongs to a different run. The same
   conversion runs on the writing side —
   `internal/schedule/scheduler.go:1182` (`toNodeRecord`), the id at `:1189` —
   and the run-feed event carries it at `:1225`.
6. **The abandoned-run rule of ADR 0015** —
   `docs/adr/0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md`.
   This is where the cost stops being additive and becomes structural, so it
   gets its own paragraph.

**Why ADR 0015 is the one that refuses.** The rule is derived from exactly two
facts, both inside one run directory: `internal/runstatus/runstatus.go:207`
(`Derive`) with the decisive arm at `:209-210` — `case f.OpenLeg && lock ==
runstate.LivenessFree: return Abandoned` — over `Gather(runDir string)` at
`internal/runstatus/runstatus.go:278`, which `:267` describes as *"reads the two
files a run directory persists"*, plus `ProbeLock` at
`internal/runstate/lock.go:365` over the one flock per run directory taken by
`AcquireLock` (`internal/runstate/lock.go:207`, `LockFileName = "resume.lock"`
at `:18`). ADR 0015 states the rule once, at `0015:400-402`: *"The derivation
rule — *in flight = open leg AND held lock* — is stated once and shared"*, and
the sharing is the point: `internal/runstatus/runstatus.go:13-16` records that
**six** surfaces ask it — `runs list`, the dashboard card, `serve`'s
`ResolveRun`, `serve`'s single-run view, `watch` and `show` — *"and a rule
composed by hand six times is a rule that will be composed six different
ways."*

A cross-run session reference makes a run's readable state depend on a **third**
fact, in **another** directory, that none of those six surfaces gathers. Two
properties of ADR 0015 then break rather than bend:

- **Affirmativeness has no analogue.** `0015:347-348`: *"A run is declared
  abandoned only on an affirmative "the lock is free". Nothing is ever abandoned
  because a probe failed."* There is no affirmative reading for "the referenced
  session is dead". Per `0008:55-56`, the CLI *"gives us no way to check
  liveness short of spending a resume attempt"* — so the analogous fact is not
  merely unread, it is **unreadable**, and every doubt would have to fall into
  an arm ADR 0015's truth table (`0015:355-360`) does not have.
- **A reader would be tempted to repair.** `0015:352-353`: *"**A reader never
  repairs the feed.** It derives a three-valued answer from two facts it already
  has access to, and writes nothing."* A dangling cross-run reference is exactly
  the shape of thing a reader repairs — dropping it, rewriting it, marking it
  stale — and the discipline that survived six surfaces would be under pressure
  at all six. Cross-surface agreement is pinned by a two-armed test:
  `TestBuildCard_AgreesWithTheSharedRule`
  (`internal/serve/dashboard_test.go:350`, which its own comment at `:351-353`
  calls *"The cross-surface agreement test … judging ALL SIX values since ADR
  0023"*) and its CLI arm `TestListRuns_StatusAgreesWithTheSharedRule`
  (`cmd/oh-my-graph/abandoned_test.go:166`, named as that arm at `:160-162`).
  ADR 0015 calls it `TestBuildCard_InFlightAgreesWithRunfeed` at `0015:403`;
  that name is not in the tree at `8658ebd` and the citation is kept here only
  to say so. A third fact that only some surfaces gather is precisely what
  those two exist to catch, and neither has a way to catch a disagreement about
  a directory it does not open.

That is the price: not a field, but a second address space inside a rule whose
entire value is that it is derived from two facts one directory already holds.

### 2.4 (c) What breaks when the referenced run is gone

Three ways a target disappears, and they do not fail alike.

**Deleted or pruned.** A run directory is an ordinary directory under
`runsRoot()` (`cmd/oh-my-graph/main.go:1907`). Nothing in this repository
promises it survives, and nothing stops an operator from removing one. A
reference into it dangles with no event anywhere: the referencing run's own
`state.json` is untouched and reads as perfectly valid, because the reference is
a string and the string is still there.

**Written by an incompatible schema.** `runstate.Load`
(`internal/runstate/runstate.go:610`) refuses a foreign schema at `:620-622`
with `*SchemaMismatchError` (`:528`). This is not hypothetical, and it is the
common case rather than the edge case.
`docs/measurements/0028-feedback-quote-corpus.md:222-223`, verbatim:

> **Anything about runs older than the local corpus.** 261 of the 288 snapshots
> are schema 2, which `runstate.Load` refuses by design.

and its consequence at `:224-227`: the measurement bypassed `Load` and parsed
the `graph` member directly, *"which is why the denominator is 201 distinct
graphs and not the 25 a `Load`-based pass would have seen. A future corpus pass
that uses `runstate.Load` is silently measuring the last two weeks."* The same
fact is recorded independently at
`docs/adr/0028-a-feedback-arc-and-its-quote-are-one-mechanism.md:540-543`.

Three caveats travel with that figure and are carried here rather than dropped:
it is a **snapshot-schema** count, not a count of corrupt or missing
directories — the two adjacent corpus measurements report 0 for their own
unreadable category (`docs/measurements/0213b-compound-commands-defeat-grants.md:135`,
*"`state.json` present but unparseable | 0"*, and
`docs/measurements/0218-denied-nodes-that-passed.md:330`), and
`docs/measurements/0213-tool-grant-predicate.md:97-98` records a different skip
census (`skipped: 300`, of which `no state.json 1`). Those are neighbouring
censuses on the same machine, not one population: the three passes walked
different totals — 288 snapshots here, 316 run directories at
`docs/measurements/0213-tool-grant-predicate.md:96`, and 323 at
`docs/measurements/0213b-compound-commands-defeat-grants.md:132`. It is dated
to its own measurement and was taken on the operator's machine; and it was
**not re-measured for this record**. What it establishes is narrow and
sufficient: *most run directories on the machine that has the most of them
cannot be read through the loader a cross-run reference would have to use.*

**Alive but empty.** The one that decides this subsection. The run directory can
be present, current-schema and perfectly readable while the session id inside it
resolves to nothing, because the transcript does not live in the run directory
at all — it is the CLI's, garbage-collected on the CLI's schedule
(`0008:33-35`). And per `0008:42-43` the id is found relative to the directory
the session was created in, which for a `worktree:` node is a path containing
the run id and therefore never recurring — the collision `0008:64-75` sets out
between a directory-scoped lookup and ADR 0005's per-run checkouts.

**So what would such a feature do when the target cannot be read?** There are
three options and each is independently disqualifying:

1. **Fail the node.** A run fails because of housekeeping in an unrelated
   directory — a `rm -rf` of an old run, or simply the passage of ADR 0008's
   ~30 days. The engine would be making a promise whose keeping depends on
   something it neither owns nor observes, which is the property `CLAUDE.md`'s
   invariants exist to keep it from acquiring.
2. **Start cold, silently.** `0008:54-63` already rejected this in terms this
   record adopts without amendment: a feature whose steady state is *quiet
   expiry* *"converts "I know this node starts cold" into "I believe this node
   remembers, and I'm sometimes wrong.""*
3. **Refuse at load time by probing the target.** This is the option that looks
   rigorous and is the worst of the three. `validateHandoffConstraints`
   (`internal/graph/validate.go:643`) is today a pure function over a parsed
   graph; making it consult foreign run directories would put the filesystem
   inside graph validation, so the same YAML would validate on one machine and
   fail on another. And **the probe cannot answer the question anyway**: a
   readable target directory does not mean a live session id, per "alive but
   empty" above. It would buy the loss of purity and still leave case 1 or 2 to
   handle at run time.

There is no fourth option, because there is no fact to test. That is the
difference between this and every other liveness question the engine answers:
ADR 0015's rule works because a lock is a fact on disk that `ProbeLock`
(`internal/runstate/lock.go:365`) can affirmatively read. A cross-run session's
liveness is a fact in a system that publishes no reading of it.

### 2.5 What is NOT weakened

**This decision does not weaken, relax, or reopen `handoff: session`'s rule of
exactly one session-parent.** The refusal at
`internal/graph/validate.go:649-656` — `if len(n.DependsOn) != 1` → *"handoff:
session with %d parents — a session-handoff node must resume exactly one
parent's session; use handoff: artifact for a root node or for fan-in"* — stands
exactly as written, as do the gate-parent refusal at `:658-666`, the contract
line at `internal/graph/validate.go:100`, and the loop-scoped refusal at
`internal/graph/feedback.go:197`. `DESIGN.md:528-535` remains accurate word for
word, and `CLAUDE.md`'s statement of the same invariant is untouched.

Also untouched, so that no reader has to derive it: `startCold`
(`internal/schedule/scheduler.go:1119-1121`) still erases a resume on every
retry, from both of its callers (`internal/schedule/scheduler.go:763-764` and
`:1105-1106`) — this record adds nothing it would have to erase. `Handoff.Seed`
(`internal/handoff/handoff.go:243`) keeps its current meaning, and its doc at
`:227-234` stays true: the id it carries is the one *"a handoff: session child
can --resume"* within its own run, across processes. The advisory worktree sweep
at `internal/handoff/session_lint.go:64` keeps its current one-hop scope. No
exec seam is added; `internal/invariants` is not approached.

## 3. Consequences

**Positive**

- No reference from one run's `state.json` into another run's directory. The
  six surfaces at `internal/runstatus/runstatus.go:13` keep deriving a run's
  status from the two facts `Gather` (`internal/runstatus/runstatus.go:278`)
  reads out of one directory, and ADR 0015's affirmativeness constraint
  (`0015:347-348`) keeps having a fact to be affirmative about.
- No new global mutable state. `0008:100-102`'s open question about concurrent
  access to `sessions.json` does not need answering, because the file is not
  created.
- No schema move. `const Schema = 3` (`internal/runstate/runstate.go:60`) stays,
  and no run written today becomes unreadable to a reader built tomorrow — which
  matters more than usual given §2.4's reading of what a schema bump costs
  historically.
- Nothing changes for any existing invocation of `run`, `auto`, `resume`,
  `serve`, `watch`, `show` or `runs`. Every graph valid at `8658ebd` remains
  valid, and no CHANGELOG-visible behaviour moves.

**Negative / trade-offs, stated rather than softened**

- **Continuity across runs stays manual, and that is a real cost this record
  chooses.** An operator who wants tonight's node to remember this morning's
  must read the id off `show` (`cmd/oh-my-graph/show.go:182`, `:264`, via
  `sessionOrDash`) and resume it by hand. `0008:89-95` argues that the human is
  better placed to judge whether the old context is still relevant; this record
  agrees, and also concedes that a human doing it every time is friction.
- **A recorded verdict of "ergonomic" is being answered with "no".** The
  backlog row (`notes/open.md:42`) is not wrong that the feature would be
  pleasant. This record says pleasant is not the test
  (`notes/roadmap.md:21-22`), and a reader who wanted the feature is owed that
  sentence plainly rather than buried in §2.2.

**Owed**

- ADR 0008's status line (`docs/adr/0008-cross-run-session-reuse-is-deferred.md:3`)
  still reads *Proposed (recommendation: defer)*. It should be marked superseded
  by this record. This record did not edit it — its scope was one new file — so
  the edit is owed, and this bullet is the address for it.
- Backlog item 8 (`oh-my-graph-hq` `notes/open.md:42`) should move out of the
  open table, which is what that file's rule 1 (`notes/open.md:8-9`, the 15-item
  cap that *"상한이 트리아지를 강제하는 장치다"*) asks a decided item to do.
- No documentation change is owed in this repository. `DESIGN.md:522-535` and
  `CLAUDE.md` already describe the behaviour this record preserves, and a
  document that announces a feature it does not have would be worse than
  silence.

## 4. Alternatives considered

| # | shape | what it would need | verdict |
| --- | --- | --- | --- |
| 1 | **ADR 0008's `session: <name>` + `~/.oh-my-graph/sessions.json`** (`0008:24-28`) | A new node field, a new global registry, and a load-time guard matrix over `session:` × `worktree:` × cwd × staleness (`0008:110-111`) | **Rejected.** Fails §2.2 on all four identity items and on the axis; incurs the whole of §2.3; and §2.4's three options are its only answers when a name resolves to nothing. It is also the shape ADR 0008 itself declined. |
| 2 | **Same, with staleness heuristics** — a TTL over `created_at`, resume-probe with cold fallback | A guess at an unpublished GC policy | **Rejected**, on `0008:129-132`'s reasoning, which this record adopts: the heuristic guesses at a policy the CLI does not publish, and *"fell back cold, silently"* is exactly the state §2.4 option 2 forbids. |
| 3 | **Operator supplies the id at invocation** — e.g. a flag naming a session for one node, never persisted as a reference | Flag parsing and one field on the invocation, which `internal/runner/runner.go:98` already has | **Rejected — and this is the strongest of the four, so it is argued.** It genuinely voids §2.3: nothing is written to `state.json`, no consumer learns anything, and ADR 0015's rule is untouched, because the id is typed by a human and lives only in one process. What it does not void is §2.2, which is dispositive on its own: it creates no node, authors no graph, reuses no node **definition**, and — because the operator must type an id that only exists after a prior run — it cannot exist on the `auto` side of `notes/roadmap.md:31-32`'s axis. §2.4's "alive but empty" also survives it: a typed id can be a dead id, and the engine still has only the three bad answers. It is a smaller version of the same feature, not a different one. |
| 4 | **Forbid cross-run reuse on `worktree:` nodes and allow it elsewhere** | Everything in 1, plus one more validation rule | **Rejected**, on `0008:133-136`: honest about the directory-scoping collision, but it carves the feature out of the engine's edit lanes — *"the nodes with the most context worth keeping"* — leaving too little value for the machinery. |
| 5 | **Nothing — the status quo, plus §3's owed status edit on ADR 0008** | None | **Chosen (§2.1).** |

Also considered: **defer again**, as `0008` did. Declined. Deferral was the
right call in 2026-08-02 because the argument on the table was entirely about
the substrate, and substrates change. The two arguments this record adds do not
depend on the substrate — §2.2 depends on what this product is, and §2.3 depends
on how this repository derives run status — so re-deferring would mean holding
open a question whose answer no external event can change. §5 states what would.

## 5. Falsification — what would make this wrong

Each of these is something a later reader can actually go and check, and each
names where to look.

**F1 — the identity list changes.** §2.2 is dispositive only for as long as
`oh-my-graph-hq` `notes/roadmap.md:27-28` reads as it does at this record's
date. If that block acquires an item about continuity, memory, or state that
outlives a run, then §2.2 is void on its face and this record must be
re-argued from §2.3 and §2.4 alone — which are costs, not vetoes. **Check:** read
that block and compare it to the four items quoted in §2.2. This is also the
falsification most likely to fire, because it is a decision the captain can make
at any time.

**F2 — a design appears that adds no state and reaches `auto`.** §4's candidate
3 fails §2.2 because a session id can only be typed by a human after the fact. If
some artifact the planner *can* read comes to identify a prior run's session —
for instance if a goal-loop cycle could name the run it is continuing, in a way
`internal/coordinator` produces rather than an operator types — then the axis
objection in §2.2 falls, and only §2.4 remains. **Check:** whether anything in
`internal/coordinator` ever gains a legitimate source for a prior run id.
`cmd/oh-my-graph/goal.go:215` already loads a snapshot by run id, so the machinery
is closer than it looks; what does not exist is a reason for a *plan* to name one.

**F3 — the substrate publishes a liveness reading.** §2.4's central claim is
that there is no fact to test — that per `0008:55-56` liveness cannot be checked
*"short of spending a resume attempt"*. If the CLI grows a way to ask whether a
session id resolves, without spending one, then §2.4's three-bad-options
argument collapses into a fourth: refuse at run time with an honest reason.
§2.2 and §2.3 would still stand, so this alone does not overturn the decision —
but it removes the argument this record leans on hardest. **Check:** the CLI's
own documentation for a session-liveness or session-list command; this is ADR
0008's first re-open condition (`0008:97-99`) and it is inherited unchanged.

**F4 — the corpus stops being mostly unreadable.** §2.4 quotes 261 of 288
snapshots being schema-refused
(`docs/measurements/0028-feedback-quote-corpus.md:222-223`). That corpus rolls
forward, and old runs age out. If a later pass finds the schema-refused share
small, the "incompatible schema" third of §2.4 weakens considerably — the
"deleted" and "alive but empty" thirds do not, so this is a partial
falsification, and it should be read as one. **Check:** re-run the census that
measurement describes, over the current contents of `runsRoot()`
(`cmd/oh-my-graph/main.go:1907`), and publish it rather than quoting a session.

**F5 — the demand shows up in the graphs.** This record concedes friction (§3)
and asserts it is worth paying. If graphs in this repo start carrying prompts
that paste a prior run's session id or artifact path into a node's text, or if
issues appear asking for it repeatedly, that is demand the decision underweighed
and it is visible without new instrumentation. **Check:** `graphs/*.yaml`,
`graphs/fragments/*.yaml`, and the `graph.json` of run directories under
`runsRoot()`, for a hard-coded run id or session id inside a prompt.

**F6 — the one-parent rule is weakened by something else.** §2.5's promise is
that this record leaves `internal/graph/validate.go:649-656` alone. If a future
change removes or relaxes that refusal, §2.5 becomes a false statement about the
tree even though it was true when written, and any reader citing it must
re-check. **Check:** that the `len(n.DependsOn) != 1` refusal is still in
`validateHandoffConstraints` (`internal/graph/validate.go:643`).

## 6. References

- **ADR 0008** — *Cross-run session reuse (named persistent node sessions) is
  deferred* (`docs/adr/0008-cross-run-session-reuse-is-deferred.md`). The record
  this one supersedes; the source of every CLI-behaviour fact used here
  (`0008:33-43`), of the quiet-expiry argument (`0008:54-63`), of the
  worktree-scoping collision (`0008:64-75`), and of alternatives 2 and 4
  (`0008:129-136`). Its re-open conditions (`0008:97-102`) are inherited by §5's
  F3 and are, after this record, no longer sufficient on their own.
- **ADR 0015** — *An abandoned run is derived from the lock, not repaired into
  the feed*
  (`docs/adr/0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md`).
  The rule a cross-run reference would break: the derivation stated once
  (`0015:400-402`), the truth table (`0015:355-360`), the affirmativeness constraint
  (`0015:347-348`), and *"A reader never repairs the feed"* (`0015:352-353`).
  Implemented at `internal/runstatus/runstatus.go:207`, shared by the six
  surfaces named at `internal/runstatus/runstatus.go:13`.
- **ADR 0013** — *A fragment is a load-time node splice, not a runtime concept*
  (`docs/adr/0013-a-fragment-is-a-load-time-node-splice-not-a-runtime-concept.md`),
  with **ADR 0027** (`docs/adr/0027-the-reusable-unit-is-a-loop-not-a-node.md:9-12`)
  and **ADR 0029** (`docs/adr/0029-a-fragment-may-cite-a-fragment.md`). Together
  they are what "node 재사용" already means in this product — §2.2's third bullet.
- **ADR 0005** — *Worktree provisioning is a third exec seam*
  (`docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md`). The per-run
  checkout paths that `0008:64-75` shows a directory-scoped session lookup
  cannot find twice.
- `docs/measurements/0028-feedback-quote-corpus.md:222-227` — 261 of 288
  snapshots schema-refused by `runstate.Load`; the one corpus figure this
  record's argument rests on, quoted and not re-measured. Restated at
  `docs/adr/0028-a-feedback-arc-and-its-quote-are-one-mechanism.md:540-543`.
  Its caveat neighbours, cited in §2.4:
  `docs/measurements/0213b-compound-commands-defeat-grants.md:135`,
  `docs/measurements/0218-denied-nodes-that-passed.md:330`, and
  `docs/measurements/0213-tool-grant-predicate.md:97-98`.
- `DESIGN.md:207`, `:522-535` — the handoff promise this record preserves
  verbatim, including the run-scoped artifact path at `:523-527`.
- `oh-my-graph-hq` `notes/roadmap.md:21-32`, `:56-61` — the identity block, the
  sentence that makes it a test, the two-modes axis, and the three promises.
  `oh-my-graph-hq` `notes/open.md:8-11`, `:42` — the backlog's cap and staleness
  rules, and item 8 with its recorded verdict. **Both files are in a separate,
  private repository** (`~/IdeaProjects/oh-my-graph-hq`) and are quoted here
  rather than linked.
