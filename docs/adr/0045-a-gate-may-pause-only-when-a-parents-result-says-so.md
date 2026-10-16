# ADR 0045 — A gate may pause only when a parent's recorded result says so, and a gate that does not pause is recorded as cleared

**Status:** Proposed.

Decision record only. It adds no YAML key, flag, event, schema value or new
seam, and no behaviour changes. The captain decides after review. If it is
accepted, the implementation is a separate change that owes the tests in §4.4,
and that change, not this record, updates DESIGN.md, `docs/RUN-FEED.md`,
`docs/EXAMPLES.md` and the two drift graphs.

**Where the addresses point.** Written against `50a4acf`, the `main` this
branch (`docs/adr-0045-conditional-gate`) left. Every `file:line` below was read at
that commit. The Decision describes code that does not exist yet, so it names
file **and** function: the line goes stale when the diff lands, and the name
does not. Constants this ADR chooses are marked *chosen*: they are design
choices with one named home each, not measurements.

**Date:** 2026-10-16

**Issue:** [#386](https://github.com/jitokim/oh-my-graph/issues/386). The
`drift-nego` fragment has to stop for a human when its ruling says the design
must change, and today the only way it can do that is by failing.

## 1. Context

### 1.1 Where the follow-up comes from

#386 asked for a drift review whose **amend-design** outcome "goes behind a
human gate, policy TBD". The issue itself does not ask for a conditional gate.
The need for one was found while building it, and PR #389 recorded it under
"Stopped: not expressible today, each needs an ADR": item 1, "The AMEND_DESIGN
stop is not a gate … It shows as FAIL, not PAUSED". The repository says the same
in four places: `graphs/fragments/drift-nego.yaml:25-33`,
`graphs/drift-review.yaml:33-41`, DESIGN.md:806-816 and
`docs/EXAMPLES.md:1459-1465`. Each one ends with "A conditional gate is engine
work and needs an ADR". This is that ADR.

### 1.2 What `rule` does today, and why

`drift-nego`'s `rule` node gives every drift item one outcome: fix-code,
amend-design or accept-deviation. It answers with one of three leading tokens
(`graphs/fragments/drift-nego.yaml:128-141`):

- `NO_DRIFT`: the report was clean;
- `RESOLVED:`: no item is amend-design, followed by a JSON array of
  `{"id","outcome","reason"}`;
- `AMEND_DESIGN:`: at least one item is amend-design, followed by the same
  array with every item in it.

Changing the design is a person's call. The natural shape is a `type: gate`
between `rule` and `fix`, but such a gate would stop every run, drift or not
(§1.4). So the fragment builds the stop out of a failure. `rule`'s check admits
only two of its own three tokens:

```yaml
success_check: { exit_zero: true, result_matches: '^[*_`\s]*(NO_DRIFT\b|RESOLVED[*_`\s]*:)' }
retry: { max: 1, on: [nonzero_exit] }
```

(`drift-nego.yaml:148-153`). An `AMEND_DESIGN:` reply fails the node. The
comment explains why there is no retry on `result_mismatch`: it "would re-roll
the ruling until it says something that passes — gaming the human stop".

### 1.3 What the operator sees today

Traced through the code at `50a4acf`:

1. `runNode` evaluates the success check before the verify step
   (`internal/schedule/scheduler.go:1010`). `evaluateSuccessCheck` returns a
   `*NodeCheckError{Predicate: "result_matches"}` (`scheduler.go:1910-1917`).
2. That error maps to `result_mismatch`. It is not in `rule`'s `retry.on`, so
   no retry happens. The reply is kept as `failed/<id>.out`
   (`Handoff.PersistFailure`, `internal/handoff/handoff.go:598`).
3. `recordFail` writes a FAIL row to the ledger and to the snapshot and emits
   `node_failed`. With halt-on-fail, `halt.note(id)` and `&HaltError{…}`
   cancel the run (`scheduler.go:757-758`).
4. The feed records `run_finished` with outcome `failed`, and the process exits
   with code 1 (`cmd/oh-my-graph/main.go:144-170`).

So **the run reads FAIL, not PAUSED**, and its exit code is the same as a
crash's. To tell the two apart, the operator opens
`~/.oh-my-graph/runs/<id>/failed/nego~rule.out` and reads its first token
(`graphs/drift-review.yaml:36-41`).

**`resume --approve` cannot continue the run.** `resumeGateLeg` refuses with
"run … is not paused (nothing to resume; a failed run is retried with
--retry-failed)" whenever `snap.Gate.PausedAt == ""`
(`cmd/oh-my-graph/resume.go:196-203`). `PausedAt` is set only by
`SnapshotRecorder.RecordDescribedPause` (`internal/runstate/recorder.go:127-133`),
and only a gate's pause calls it.

**The way the run can be continued is the one the retry comment warns
about.** `resume --retry-failed` clears FAILED records and re-runs those nodes
(DESIGN.md:2434-2447). It would re-run `rule` as a fresh session, and the new
ruling could come back `RESOLVED:`. The human stop then never reaches a human.
The retry policy closes this hole inside one leg, but not across legs.

### 1.4 Why a plain gate after `rule` does not work

DESIGN.md "Gate nodes and `resume` (v1.1)" (DESIGN.md:2011-2085) and ADR 0003
define a gate as "a planned stopping point. The run pauses by exiting"
(0003:29). `GateController.Evaluate(ctx, node)` returns approve, reject or pause
(`internal/gate/gate.go:28-48`). A fresh `run` uses `PauseController`, which
always returns `DecisionPause` (`gate.go:50-65`). `RecordedController` returns a
recorded or `--auto-approve`d decision, or `DecisionPause` when there is none
(`gate.go:74-96`). The controller receives the gate node and **nothing about
any upstream result**. Nothing on `graph.Node` (`internal/graph/graph.go:259-323`)
carries a condition.

So a gate placed after `rule` pauses every run. On a clean check that means an
exit code of 2 and a `resume --approve` that nobody needed. Naming the gate in
`--auto-approve` at launch removes the stop altogether, because the operator
has to decide before the ruling exists.

### 1.5 What the gate ADRs settled, and what they leave open

- **ADR 0003** (a gate is a clean stop): a pause ends the leg, drains running
  work, writes a snapshot and exits 2. It rejected "skip gates and let
  `--continue-on-fail` approximate them" because "it inverts the semantics"
  (0003:133-135). It also rejected pausing only a subtree (0003:139-141). This
  ADR keeps all of that. A conditional gate that matches pauses exactly as a
  gate does today.
- **ADR 0014** (the live view can decide a paused gate): a decision is valid
  only while `Gate.PausedAt` names that gate. That stays true, because a gate
  that does not pause is never in `PausedAt`.
- **ADR 0016** (build evidence) gave an approved gate the provenance
  `approved`, "the strongest provenance", a PASS with no cost or session. That
  is why a gate that passes without asking anyone must **not** reuse
  `approved` (§2.4).
- **ADR 0023** (a run has one status): `PAUSED` is read off the stream's
  `outcome` (0023:150-153), and PASS means `CompletedNodes == TotalNodes`
  (`internal/runstatus/runstatus.go:217`, counted at `:293-294`). So a gate that
  does not pause **must leave a PASS record**. Otherwise a clean drift run
  would read FAIL.
- **ADR 0033** (the run is the unit of evidence) kept `attachVerification`'s
  gate-sink skip and its refusal when every sink is a gate
  (`internal/coordinator/verifycmd.go:252,262`). Both are unchanged here.
- **ADR 0039** (a gate is authored, not attached): "A `type: gate` may only be
  written by the person who will be asked to approve it, in a graph they can
  see" (0039 §2.1). It also keeps `validatePlannedNodes`' refusal of planned
  gates. This ADR is the authored case. The condition is written by the same
  person, in the same file, next to the gate. Its falsifiers (0039 §7), a pause
  that does not end the leg and `--exit-in-flight` learning about PAUSED, are
  not touched. A conditional pause ends the leg like any other pause.

The open question is narrow. **Whether a gate pauses becomes a fact the engine
reads off the graph and a recorded result. It stops being a fact about which
controller the CLI wired.** No ADR has decided this yet.

## 2. Decision

### 2.1 The surface: `pause_when` on a gate

**Decision.** A `type: gate` node may declare one `pause_when` block:

```yaml
pause_when:
  node: <id of one of this gate's depends_on>
  result_matches: <RE2 pattern>
```

- `node` names **one direct parent** of the gate. It must be in the gate's own
  `depends_on`, not just an ancestor, and it must not itself be a gate.
- `result_matches` is a pattern over that parent's **recorded result**: the text
  `PersistOutput` wrote to `<run-dir>/<id>.out` when the parent passed
  (`scheduler.go:1016`). It is the same text the parent's own `result_matches`
  was judged against. The key reuses that name so that one key means one thing
  across the schema.
- With no `pause_when`, a gate is today's gate, byte for byte.
- The key is valid only on `type: gate`, like `description:`
  (`internal/graph/validate.go:186-199`).

Here is the drift-nego case rewritten. The implementation PR would make this
change to the fragment; this ADR does not.

```yaml
  - id: rule
    # … prompt, tools and permission_mode unchanged …
    # Every verdict the prompt allows now PASSES; a reply that starts with none
    # of them still fails. The stop for a human is the gate below, not a failure.
    success_check: { exit_zero: true, result_matches: '^[*_`\s]*(NO_DRIFT\b|RESOLVED[*_`\s]*:|AMEND_DESIGN[*_`\s]*:)' }
    retry: { max: 1, on: [nonzero_exit] }

  - id: amend-design
    type: gate
    depends_on: [rule]
    description: "The ruling has amend-design items. Amend the design (and its lock), then approve to apply the fix-code items, or reject to stop."
    # Pauses on the AMEND_DESIGN: verdict, or on any item ruled amend-design,
    # even under a RESOLVED: headline.
    pause_when:
      node: rule
      result_matches: '^[*_`\s]*AMEND_DESIGN[*_`\s]*:|"outcome"\s*:\s*"amend-design"'

  - id: fix
    depends_on: [amend-design]
    # {{ artifacts.rule | inline }} still resolves: rule is still an ancestor.
```

A clean check now takes `rule` → the gate clears → `fix`, with no stop. An
amend-design ruling now **pauses** (exit 2, `PAUSED`). Approving it means "I
have amended the design, and the lock is mine to update, so apply the fix-code
items." Rejecting it prunes `fix` and exits 1 (DESIGN.md:2408-2410). `rule`'s
retry policy stays as it is. The implementation PR may revisit it, but not
inside this ADR.

**Rejected.**
- A list of conditions, or `any:`/`all:` combinators. One parent and one
  pattern covers the case that exists. RE2 alternation (`|`) already gives "any
  of these lines" within one parent. A second parent's condition belongs on a
  second gate.
- An ancestor rather than a direct parent. A direct parent's PASS is what made
  the gate ready, so its result is certain to exist and is the one just
  produced. An ancestor's result may be several hops old, and the YAML would no
  longer show the reader which edge the condition watches.
- A gate as `node`. A gate has no result: it spawns nothing, and `PersistOutput`
  is never called for it.
- The inverse, `pass_when`, which pauses unless a clean pattern matches. It is
  safer on paper, because a missing token would pause. But the same safety is
  already available at the right place: the parent's own `result_matches`
  admits only well-formed verdicts (§2.6), so a malformed reply fails the node
  that made it. Two directions would double the validation and recording
  surface for no case that needs it.
- A key named `when:` or `if:`. These read as "does this node run", which is
  #386's second follow-up (§2.8) and a different question. `pause_when` says
  what changes: whether the run stops, never whether the gate settles.

### 2.2 What the engine evaluates, and when

**Decision.** The condition is evaluated in `Scheduler.evaluateGate`
(`scheduler.go:1086`), **after** `node_started` and **before**
`s.gate.Evaluate` is called. At that moment the gate is ready, which means
every parent has a PASS record, `pause_when.node` included.

1. The engine reads the parent's result from the path `Handoff.ArtifactPath`
   gives (`internal/handoff/handoff.go:758`). In a fresh leg that is the path
   `PersistOutput` just recorded. In a resumed leg it is the path `Seed`
   restored from the snapshot (`handoff.go:384`). Neither is model-chosen.
2. The pattern is compiled with `regexp.Compile` and matched with
   `MatchString`, with **no flags added**, exactly as `evaluateSuccessCheck`
   does for `result_matches` (`scheduler.go:1910-1917`). It is RE2:
   case-sensitive, `^`/`$` anchor only at the start and end of the whole text
   (no implicit `(?m)`), `.` does not match a newline, and an unanchored
   pattern matches anywhere. An author who wants per-line anchoring writes
   `(?m)`, as they would for `result_matches`.
3. **No match → the gate clears** (§2.4). The controller is **not** consulted:
   no human was asked, so no decision exists to record.
4. **Match → the gate is a gate.** `s.gate.Evaluate` runs and its decision is
   applied exactly as today: approve, reject or pause, all on the existing
   paths.
5. **Unreadable result → the gate fails** as an ordinary node failure, with the
   path and the read error. This is the same treatment a controller error gets
   (`gate.go:42-48`). It never clears and never pauses. A missing artifact
   behind a PASS record breaks an engine invariant, and a pause would ask a
   human to decide about a result nobody can show them.

`GateController`'s signature does not change. The `internal/gate` package stays
ignorant of results and handoff. The "scheduler … never branches on 'am I
resuming'" rule (DESIGN.md:2053-2085) holds: every leg that reaches the gate
evaluates the condition the same way, over the same persisted file.

A parent that ran inside a feedback loop (ADR 0010) is evaluated over its last
PASS, which is the result its artifact holds. A gate can never be inside a
feedback body (`internal/graph/feedback.go:186-192`), so the gate itself is
never re-run by a feedback round.

### 2.3 Validation

`graph.Validate` refuses, at load time and before anything is paid for:

| case | refusal (wording indicative) |
| --- | --- |
| `pause_when` on a non-gate | `node %q declares pause_when, which is valid only on type: gate` |
| `pause_when.node` empty | `gate %q: pause_when.node is required` |
| `pause_when.node` names no node | `gate %q: pause_when.node %q: no such node` |
| names a node outside the gate's `depends_on` | `gate %q: pause_when.node %q is not a direct parent (depends_on: …)` |
| names a gate | `gate %q: pause_when.node %q is a gate, which has no result to match` |
| `pause_when.result_matches` empty | `gate %q: pause_when.result_matches is required` |
| pattern fails `regexp.Compile` | `gate %q: pause_when.result_matches: invalid regex %q: …` (same check as `validate.go:588-589`) |

Fragments: the splice namespaces `pause_when.node` exactly as it namespaces
`depends_on`, so the `rule` written inside `drift-nego` becomes `nego/rule`
after splicing. A `pause_when.node` that spells an authored `/` is refused by
`refuseAuthoredNamespaces` (`internal/graph/fragment.go:746`), as the keys it
already covers are.

`lint` adds one **warning**, not a refusal, when the named parent declares no
`result_matches` of its own: "gate %q pauses on %q's result, but %q requires no
token, so a reply without one passes and the gate clears" (§2.6). A parent
gated only by `verify:` is legitimate, so this is a warning and nothing more.

The graph decoder sets no `KnownFields`: `decode` is a plain `yaml.Unmarshal`
(`internal/graph/graph.go:483`), as the `Use` field's comment notes
(`graph.go:345`). An
older binary therefore drops `pause_when` without a word, and a misspelt key
(`pause_wen:`) is dropped the same way. Both fall back to today's
**always-pause** gate. That is the safe direction: a stop nobody needed, never
a stop skipped.

### 2.4 What is recorded: a gate that does not pause is *cleared*, and says why

The rule is that a gate whose condition is false must be visible and auditable,
and never silent.

**Decision.** A gate whose `pause_when` does not match **clears**. It settles
as a **PASS** with provenance **`cleared`**, zero cost and no session, and its
dependents proceed. PASS is required, not chosen: without it
`runstatus.Derive` reads the run as FAIL (§1.5, ADR 0023). `cleared` is a new
member of the provenance set (`internal/runfeed/runfeed.go:130-143`). It is
deliberately **not** `approved`: nobody approved anything, and ADR 0016 made
`approved` the strongest provenance in the set. In strength order `cleared`
sits with `self-reported`: an engine-evaluated predicate over what a model
said.

Each evaluation carries one record, the **condition**:

```json
"gate_condition": {
  "node": "nego/rule",
  "result_matches": "^[*_`\\s]*AMEND_DESIGN[*_`\\s]*:|\"outcome\"\\s*:\\s*\"amend-design\"",
  "matched": false,
  "line": "NO_DRIFT",
  "result_sha256": "…"
}
```

- `line` is the evidence line. On a match, it is the line of the result that
  holds the start of the leftmost match. On no match, it is the result's first
  non-blank line, because the leading token is what a verdict is. Either way it
  passes through `fence.SanitizeTerminalLine` and is cut by `fence.Truncate` to
  **200 bytes** (*chosen*, home: `schedule.gateConditionLineMax`).
- `result_sha256` is the hash of the exact bytes evaluated. It lets an auditor
  confirm, later, which artifact the decision was made over.

Where it goes:

| surface | cleared (no match) | matched |
| --- | --- | --- |
| `events.jsonl` | **new** `gate_cleared` event with `gate_condition`, then `node_passed` with `provenance: "cleared"` | `gate_paused` / `gate_approved` / `gate_rejected` as today, each with an optional `gate_condition` |
| `state.json` node record | PASS, `provenance: "cleared"`, new optional `gate_condition` | on a decision: as today, plus `gate_condition` (copied, like `gate_description`) |
| `state.json` `gate` | no `decisions` entry: nothing was decided | `decisions[id]` as today; while paused, new optional `paused_gate_condition` beside `paused_gate_description` (`internal/runstate/runstate.go:545-570`) |
| ledger row | PASS, $0, `detail`: `pause_when unmatched on <node>: "<line>"` | as today, `detail` adds `pause_when matched on <node>: "<line>"` |

**Schema.**
- The **stream schema goes from 3 to 4.** The event-type set is closed per
  schema version, and adding an event type bumps it (RUN-FEED.md:877-881),
  which is how 1 became 2 with the three `gate_*` events. The new provenance
  value travels in the same bump: RUN-FEED calls the set "a closed set of four"
  (RUN-FEED.md:633-641), and a fifth member is a change an existing reader
  could misread.
- The **snapshot schema does not move**, and that covers the new provenance
  value too. `gate_condition` and `paused_gate_condition` are optional fields
  old readers ignore (RUN-FEED.md:869-876). `provenance: "cleared"` in a node
  record is exempt for a different reason: `state.json` never declared its
  provenance a closed set. RUN-FEED's snapshot field list does not name it
  (RUN-FEED.md:180-188); the closed set of four is a property of the stream's
  `node_passed` (RUN-FEED.md:633-641). In the snapshot it is only carried
  forward (`resume.go:578`, `show.go:207`) and printed verbatim by
  `ledger.VerdictCell` (`internal/ledger/ledger.go:372-377`), so an old binary
  reading the file renders `PASS (cleared)`, which is true. Nothing derives a
  status from it: `runstatus` counts PASS by verdict alone. The implementation
  rewrites the comment on `NodeRecord.Provenance`
  (`internal/runstate/runstate.go:296-316`), which today says "one of
  runfeed's four qualifiers", to name the stream's set without a count. An old
  binary cannot produce such a run in any case, because it drops `pause_when`
  at decode (§2.3).

**Rejected.**
- No record for a cleared gate, or a "skipped" verdict. Either makes a clean
  run read FAIL (§1.5). Silence is also exactly what this section forbids.
- Reusing `node_passed` alone, with only a new provenance and no new event.
  That costs no bump, but every other gate outcome has its own `gate_*` event,
  and a consumer that watches `gate_*` to show "where the human stops are"
  would never see the gates that did not stop. That is the silent path again.
- Recording `approve` in `decisions` for a cleared gate. A resume replays
  `decisions` as human choices (`RecordedController`), and a cleared gate was
  never one.

### 2.5 `resume`, `--auto-approve`, `--continue-on-fail`

**Decision.**

- **`--auto-approve <gate>`** is still checked by `checkAutoApprove`
  (`cmd/oh-my-graph/autoapprove.go:46-62`), and a conditional gate is a gate. It
  is still listed at top-level `auto_approve`. If the condition **matches**, the
  `RecordedController` answers approve and the gate records exactly what an
  auto-approved gate records today: `decisions[id] = approve`, provenance
  `approved`, plus `gate_condition`. If it **does not match**, the controller
  is never asked, the gate clears, and nothing is added to `decisions`. A gate
  that did not pause needs no approval, so the pre-approval stays unused and
  the record says so: `auto_approve` lists the id, and the node's provenance is
  `cleared`.
- **`resume --approve|--reject <gate>`** is unchanged. A conditional gate can
  only be `PausedAt` after a match, so ADR 0014's precondition and the explicit
  gate id rule (DESIGN.md:2402-2407) carry over as they are. The resumed leg
  reaches the gate again, re-evaluates the condition over the same persisted
  artifact (a PASSED parent's record and artifact are kept), gets the same
  match, and then asks the `RecordedController`, which returns the new decision.
  If the artifact were edited by hand between the legs, the second evaluation
  is recorded on its own event with its own `result_sha256`. The disagreement
  is in the record, not hidden.
- **`resume --retry-failed`** never re-rolls the human stop again. The
  amend-design ruling now PASSES and pauses, and a gate-paused run is
  redirected to `--approve`/`--reject` (DESIGN.md:2434-2447). The cross-leg
  hole of §1.3 is closed by construction.
- **`--continue-on-fail` / `on_fail: continue`**: the condition is evaluated
  **only over a PASS**. A gate becomes ready only when every parent passed. If
  the named parent (or any other parent) fails, the gate is never ready and is
  pruned with the failed node's subtree, like every other dependent
  (`scheduler.go:744-751`). It gets no evaluation, no record and no event of its
  own, and the run ends with `*RunFailedError`, exit 1. The predicate never
  reads `failed/<id>.out`: a failed reply is not a result. Under halt-on-fail
  the run halts at the parent, as today. A pause, once it matches, stops the
  whole run whatever the setting (DESIGN.md:2036-2037). This ADR does not
  reopen ADR 0003's subtree rejection.

### 2.6 Safety: who chooses whether the run stops

**Decision.** The predicate is **authored in the graph** by the person who will
be asked to approve, and **evaluated by engine code** over a file the engine
wrote. No model chooses the pattern, the parent, or whether the gate is
evaluated. A planned graph cannot carry one at all (§2.7).

Said plainly: **a model can still avoid the pause by not emitting the token.**
That is the same exposure as today, where `rule` can dodge the failure by
writing `RESOLVED:` over an amend-design item. Two things limit it, and only
two:

1. **The parent's own positive `result_matches`.** If the parent must start
   with one of its well-formed verdicts, a reply with no token, a misplaced
   token or a malformed one **fails the parent**, so it cannot slip through as
   "no match". §2.3's lint warning flags any parent that lacks one. In the
   drift case, the rewritten `rule` admits only `NO_DRIFT`, `RESOLVED:` and
   `AMEND_DESIGN:`.
2. **A pattern that reads the body as well as the headline.** The example's
   second alternative, `"outcome"\s*:\s*"amend-design"`, pauses on any item
   ruled amend-design even under a `RESOLVED:` headline. A model that wants to
   avoid the stop has to misstate the ruling itself, and not only its summary.

Nothing in the engine can stop a model from ruling fix-code where a human would
have ruled amend-design. That is a judgement about the content, and a gate on
text cannot recover it. `rule` holding no write tool and running under
`permission_mode: plan` (`drift-nego.yaml:142-144`) limits what a wrong ruling
can *do*, and `fix`'s engine-run baseline re-check limits what the fix can
touch. Neither changes whether the run stops.

### 2.7 Planned graphs stay gate-free

**Decision.** `validatePlannedNodes`' refusal "planned node %q is a gate node,
which auto mode cannot run" (`internal/coordinator/coordinator.go:1238-1239`)
stays, and it covers conditional gates because they are gates. Its reason
(`coordinator.go:1049-1051`) still holds: a conditional gate that matches
parks an unattended `auto` run as surely as an unconditional one. A condition
also makes the park *less* predictable to the person who launched `auto`.
`pause_when` on a planned non-gate is already refused by §2.3's first row,
which runs on planned graphs as well. ADR 0039's stance, authored and never
attached, is unchanged.

### 2.8 #386's second follow-up: skipping `nego` on `NO_DRIFT`

PR #389's item 2 and DESIGN.md:817-822 say "Skipping `nego` on a clean check
needs conditional execution of a node or a `use:`, which needs an ADR — likely
the same one as the conditional gate". **This mechanism does not cover it**,
for three reasons:

- A cleared gate still **settles as PASS**, and its dependents still run. A
  skip means the node does *not* run, so it needs a recorded "did not run"
  that runstatus counts towards PASS (today it would read FAIL, §1.5), and a
  node vocabulary that does not exist (no `node_skipped`, no skip verdict).
- Every descendant of a skipped node interpolates `{{ artifacts.<id> }}` that
  was never written. A gate has no artifact anyone reads, so it never raises
  this question.
- A `use:` splices several nodes, so a condition on it is a condition on a
  subgraph, with an exit node whose artifact the citing graph reads.

What it can share is the **predicate shape**: `{ node, result_matches }` over
a direct parent, the same compile rules, and the same `gate_condition`-style
record. A later ADR for conditional execution should reuse that shape rather
than invent another. Scope here is the gate only.

### 2.9 How it is displayed

- **`lint`** (`lintGraphForRuntime`, `cmd/oh-my-graph/lint.go:83-116`): it
  reports §2.3's refusals and warning. On success, before `<path>: valid`, it
  prints one line per conditional gate:
  `gate nego/amend-design pauses only when nego/rule's result matches /…/`.
  An unconditional gate prints nothing, as today.
- **`run --dry-run`** (`printResolvedPlan`, `cmd/oh-my-graph/dryrun.go:91-102`):
  each gate line is marked, `  - nego/amend-design (after nego/rule) [gate]`,
  and a conditional one also shows its condition:
  `[gate: pauses when nego/rule matches /…/]`. This changes the dry-run output
  for every gate, which today is unmarked. That is intended: the plan is where
  the reader learns where the run can stop. `printAutoApproved`
  (`dryrun.go:104-113`) is unchanged.
- **`show`** (`cmd/oh-my-graph/show.go`): a cleared gate is a row like any
  other, verdict PASS, and its DETAIL is the ledger detail of §2.4
  (`pause_when unmatched on nego/rule: "NO_DRIFT"`), so the evaluation is on
  the screen without opening `state.json`. A matched-and-decided gate's
  detail carries the matched line. A *paused* gate still has no row: today's
  `show` prints no paused gate, and fixing that is not this ADR's change.
- **The live view** (`serve`): where it shows `paused_gate_description`, it
  shows `paused_gate_condition.line` as well, so the person deciding sees the
  line that stopped the run.

### 2.10 Decisions in one place

| question | decision |
| --- | --- |
| surface | `pause_when: { node, result_matches }`, on `type: gate` only |
| `node` | one direct parent, not a gate; namespaced by fragment splices |
| pattern | RE2, `regexp.Compile`, no added flags: identical to `result_matches` |
| when | in `evaluateGate`, after `node_started`, before the controller |
| no match | the gate **clears**: PASS, provenance `cleared`, `gate_cleared` event, no decision |
| match | an ordinary gate: approve / reject / pause, plus the condition record |
| unreadable result | the gate fails; it never clears and never pauses |
| record | `gate_condition` {node, pattern, matched, line ≤200 B, sha256} on event, node record, ledger detail |
| schemas | stream 3 → 4 (new event, fifth provenance); snapshot unchanged, `cleared` included |
| `--auto-approve` | used if it matches; unused and visible if it clears |
| `resume --approve` | unchanged; re-evaluates over the same artifact |
| `--retry-failed` | can no longer re-roll the human stop |
| `--continue-on-fail` | a failed parent prunes the gate; never evaluated over a FAIL |
| model influence | none over the predicate; can withhold the token, bounded by §2.6 |
| `auto` | planned gates still refused |
| skipping `nego` | not covered; shares the predicate shape later |

## 3. Alternatives considered

**(a) A gate whose pause depends on a declared predicate over a named parent's
result.** This is §2, and it is **recommended**.
- *Costs:* one key, one evaluation step in `evaluateGate`, one new event and
  provenance value, and so a stream-schema bump. Every feed consumer has to
  learn `gate_cleared`.
- *Gains:* it reuses every gate path there is (pause, resume, the live view's
  decision, `--auto-approve`, reject-prunes). The stop reads PAUSED and exits
  2. It is resumable with the command the run prints, and a gate is visible in
  the topology. It closes the `--retry-failed` re-roll.

**(b) A `success_check` outcome that pauses instead of failing**, for example
`pause_matches:` on `rule` itself, with no gate node.
- *Costs:* every gate mechanism is keyed on a gate id. `Gate.PausedAt` names a
  gate, `checkAutoApprove` refuses non-gates, ADR 0014's POST decides a gate,
  and the liveness rule counts `gate_paused` as ending *a gate's* turn
  (RUN-FEED.md:440-447). A paused `claude-run` node has spent money and holds a
  session and a reply. "Approve" would have to mean "accept this reply as a
  PASS", which the ledger, with no `PAUSED` verdict, cannot express. That is a
  new kind of node settlement, not a new kind of gate.
- *Also:* the human stop disappears from the topology into a check on one
  node. It also reopens ADR 0039 from the other side: any node, including a
  planned one, could carry a pause unless every planned-graph refusal learned
  the new key. Its interplay with `retry.on: [result_mismatch]` needs its own
  rules too.
- *Rejected.*

**(c) Keep today's deliberate failure.**
- *Costs:* §1.3 in full. FAIL reads like a crash, and exit 1 cannot be told
  apart from a real failure without opening `failed/*.out`. `resume --approve`
  refuses, and `--retry-failed` can re-roll the ruling until the stop vanishes.
- *Gains:* no engine change, no schema bump.
- *Rejected* as the end state. It stays the right answer **until** (a) lands,
  and the fragment's header already says so.

**(d) A results-aware `GateController`**: widen `Evaluate` to receive upstream
results and let a new controller decide.
- *Costs:* the controller is chosen **per invocation** at the CLI boundary
  (ADR 0003:46-49), while a condition is **per gate** graph data. Making the
  choice per gate would mean dispatching controllers per node, or making every
  controller learn conditions. It also drags handoff into `internal/gate`.
- *Rejected* in favour of §2.2's step before the controller. That leaves the
  interface and both implementations unchanged.

**(e) A new node type** (`type: conditional-gate`).
- *Costs:* every rule written against `TypeGate`, including the session-parent
  refusal (`validate.go:729-736`), the feedback-body refusal, the planned-gate
  refusal, the verify sink skip and `checkAutoApprove`, would have to be
  re-stated for a second type, and missing one is a silent hole. A field on
  the existing type inherits them all.
- *Rejected.*

**(f) Orchestrate outside the engine**: a wrapper that reads `rule`'s artifact
and then runs a second graph, or passes `--auto-approve`.
- *Costs:* `--auto-approve` is fixed at launch, before the ruling exists. A
  two-graph split loses the single run as the unit of evidence (ADR 0033), and
  the decision happens in shell nobody records.
- *Rejected.*

**(g) A model call that decides whether to pause.**
- *Costs:* it puts the stop in a model's hands, which is exactly what §2.6
  forbids, and it pays a session to read a token a regex reads for free.
- *Rejected.*

## 4. Consequences

### 4.1 What changes for the operator, once implemented

- A drift run with amend-design items reads **PAUSED**, exits **2**, prints a
  `resume … --approve nego/amend-design` command, and can be decided from the
  live view.
- A clean drift run reads **PASS** with one more row in `show`: a cleared gate,
  with its evidence line.
- `run --dry-run` marks every gate, conditional or not.
- Feed consumers see stream schema 4, one new event type and one new
  provenance value. Per RUN-FEED's rule they skip what they do not know rather
  than fail.

### 4.2 What it costs, and what it does not fix

- The stream bump is real. Any consumer pinned to schema 3 surfaces the
  change (RUN-FEED.md:882-885).
- The safety bound is §2.6 and no more. A model that misstates its own ruling
  is not caught by a text gate.
- `show` still prints no paused gate (§2.9). That is a separate change.
- `nego` still runs on a clean check (§2.8).

### 4.3 Implementation outline

File and function names only. No code lands with this ADR.

- `internal/graph/graph.go`: a `PauseWhen *GateCondition` field on `Node`
  (`yaml:"pause_when"`), and the `GateCondition{Node, ResultMatches}` type.
- `internal/graph/validate.go`: the §2.3 refusals, next to the `description:`
  rule.
- `internal/graph/fragment.go`: namespace `pause_when.node` in the splice, and
  cover it in `refuseAuthoredNamespaces`.
- `internal/schedule/scheduler.go`: `evaluateGateCondition`, called from
  `evaluateGate` before `s.gate.Evaluate`; `recordGateCleared` beside
  `recordGateApprove`; `passProvenance` learns `cleared`;
  `gateConditionLineMax`.
- `internal/runfeed/runfeed.go`: `EventGateCleared`, `ProvenanceCleared`,
  `Schema = 4`, and the optional `GateCondition` on the event.
- `internal/runstate/`: `NodeRecord.GateCondition`,
  `GateState.PausedGateCondition`, and recording both in
  `RecordDescribedPause` and on decision.
- `cmd/oh-my-graph/`: `lint.go` (summary line and warning), `dryrun.go`
  (`printResolvedPlan` gate marks), `resume.go` (carry the paused condition
  into the decided gate's record, as `gate_description` is carried), and the
  `serve` live view.
- `internal/coordinator/`: no code change. A test pins that the planned-gate
  refusal still fires for a conditional gate.
- Documentation to update when it lands: DESIGN.md ("Gate nodes and `resume`",
  the drift section at :806-822), `docs/RUN-FEED.md` (event table, provenance
  set, schema history), `docs/EXAMPLES.md`, `graphs/fragments/drift-nego.yaml`
  and `graphs/drift-review.yaml` (§2.1's rewrite), and a changelog fragment.
  This record changes none of them.

### 4.4 Tests the implementation owes

All engine tests use `FakeRunner`. No real model is spawned.

1. **No match clears.** A parent whose reply is `NO_DRIFT` gives a gate PASS
   with provenance `cleared`, $0, no session. Dependents run, the controller is
   never called (a recording fake controller sees zero calls), and the run
   status is PASS.
2. **Match pauses.** An `AMEND_DESIGN:` reply under `PauseController` gives
   `*PausedError` and exit 2. `gate.paused_at` and `paused_gate_condition` are
   set, the dependents have not run, and the status is PAUSED.
3. **Body match.** A `RESOLVED:` reply whose JSON holds
   `"outcome": "amend-design"` pauses under §2.1's pattern.
4. **Regex rules match `result_matches`.** For the same pattern and reply,
   `pause_when` and `result_matches` agree: case-sensitive, `^` only at the
   start of the text without `(?m)`, unanchored matches anywhere. The test is
   table-driven and shared between the two.
5. **Validation.** Each row of §2.3 gives its refusal: non-gate, empty node,
   unknown node, non-parent ancestor, gate parent, empty pattern, bad regex.
   The refusal comes before any runner call.
6. **Fragments.** `pause_when.node: rule` inside a fragment resolves to
   `nego/rule` after splicing, and an authored `/` in it is refused.
7. **Lint.** A conditional gate prints its summary line. A parent with no
   `result_matches` draws the warning and still passes lint.
8. **Dry run.** Every gate line is marked `[gate]`, and a conditional one shows
   its condition. The output is a golden file.
9. **`events.jsonl`.** A cleared gate emits `node_started`, `gate_cleared`
   (with `gate_condition`: node, pattern, `matched: false`, line, sha256) and
   then `node_passed` with `provenance: "cleared"`. A matched gate's `gate_*`
   event carries `matched: true` and the matched line. Every event has
   `schema: 4`.
10. **`state.json`.** The cleared gate's node record carries `gate_condition`
    and `provenance: "cleared"`, and `gate.decisions` has no entry for it. The
    snapshot schema is unchanged by both. A resumed leg that loads the record
    prints `PASS (cleared)` for it, and `runstatus.Derive` counts it as
    completed.
11. **Evidence line.** The line is the one holding the match start, or the
    first non-blank line when nothing matches. It is sanitised (an escape
    sequence in the reply does not reach the record) and truncated at 200
    bytes on a rune boundary.
12. **`show`.** A cleared gate's row is PASS, and its DETAIL holds the
    unmatched line.
13. **`resume --approve`** on a conditional pause re-evaluates, matches,
    approves, and runs the dependents. The decided record carries the
    condition. **`--reject`** prunes and exits 1.
14. **`--auto-approve`, matched:** no pause. `decisions[id] = approve`,
    provenance `approved`, and the condition is recorded.
15. **`--auto-approve`, unmatched:** the gate clears. `auto_approve` still
    lists the id, `decisions` has no entry, and the provenance is `cleared`.
16. **`--retry-failed`** on a run paused at a conditional gate is redirected to
    `--approve`/`--reject` and spawns nothing. `rule` is not re-run.
17. **`--continue-on-fail`.** The named parent fails, so the gate has no
    record and no `gate_*` event, an independent branch still finishes, and the
    exit code is 1. The same holds for `on_fail: continue`.
18. **Unreadable result.** A parent PASS whose artifact is removed before the
    gate is evaluated (a fake handoff seam) fails the gate. The gate never
    clears or pauses.
19. **No `pause_when`, no change.** The existing gate tests pass unchanged, and
    a gate without the key emits exactly today's events.
20. **The coordinator still refuses gates.** A planned graph holding a gate
    with `pause_when` is refused with the planned-gate refusal, and a planned
    non-gate carrying `pause_when` is refused by validation.

## 5. Falsification

- **Wrong if a cleared gate can hide a stop that was owed.** Seek out drift runs
  whose `rule` artifact contains an amend-design item but whose gate cleared.
  One such run means the pattern in §2.1 is too narrow. Two such runs with
  different shapes mean a text predicate is the wrong tool for this ruling, and
  the fallback is (c).
- **Wrong if operators route around the gate.** If conditional gates are
  routinely named in `--auto-approve` at launch, the condition is not doing
  the deciding, and the surface buys nothing over an unconditional gate.
- **Wrong if the bump costs more than the event earns.** If no consumer reads
  `gate_cleared` and the provenance already tells them everything, the event
  was a schema bump for nothing, and `node_passed` with `cleared` alone would
  have done.
- **Wrong if conditional execution arrives with a different predicate shape.**
  If the follow-up of §2.8 cannot reuse `{ node, result_matches }`, the schema
  ends up with two ways to say "on this parent's result", and one of them
  should be retired.
