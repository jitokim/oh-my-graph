# ADR 0016 — Build evidence for a planned node is a user-supplied engine command, not a wider tool grant

- Status: Proposed — decision taken, **engine implemented and now reachable:
  `auto` and `resume` parse `--verify-cmd` / `--verify-timeout`.** As of
  2026-08-06 the
  tree holds the injection (§2), the serialization, the `retry.max` cap, the
  detector and its advice line (§3), the planner-prompt split (§5), the verdict
  qualifier (§6), and the flags themselves — validated before the planner call,
  disclosed with the plan (so `--plan-only` shows what the sinks will run), and
  carried by the coordinator across every cycle of a `--max-cycles` goal loop.
  **Amended 2026-08-18 (#198): `resume` now registers the same pair**, closing
  §4's re-supply half — see the Disposition, which records that the absence was
  an omission and not an exclusion. One thing named in the Decision is still
  unshipped: `chat`, which §2 names alongside `auto`, has
  no flag. It needs no measurement
  gate: unlike ADR 0004 and ADR 0012, nothing here changes a node's argv, its
  tool set or any ceiling layer, so there is no CLI-behaviour premise to
  probe. (§2's `retry.max` cap tightens what a plan may declare; it grants
  nothing and loosens nothing, so it is a validation change, not a ceiling
  change.) What it does owe before Accepted is stated under "Required
  measurements".
- Date: 2026-08-06
- Line citations below are as of the implementation commit on this branch.
  They are anchors for a reader, not addresses the code maintains: when one
  disagrees with the file, trust the named symbol.
- Issues: [#119](https://github.com/jitokim/oh-my-graph/issues/119)
- Amends in part: `0004-auto-mode-tool-ceiling-by-settings-isolation.md` §1
  (what layer 0's allowlist is *for*) and §2 (the disposition of
  `success_check.verify`). ADR 0004's decision text is unchanged; it carries a
  dated pointer here.

## Context

A user on v0.4.1 ran `auto` against a Kotlin repository. The planner produced
a final verify node declaring `[tools: Bash(git *)]`. It checked the branch
name, the commit list and a clean working tree, replied `PASS` in 17 seconds
for $0.13 — and certified a branch that did not compile. The apply node it was
checking had spent $11.01. Every row in the run ledger read `PASS`.

### The shallow cause, confirmed

The reading in the report holds, and the code is unambiguous:

- `plannedToolAllowlist` (`internal/coordinator/coordinator.go:61`) is
  `Read, Glob, Grep, Edit, Write, Bash(git *), Bash(go *), Bash(make *),
  Bash(ls *), Bash(cat *), Bash(grep *), Bash(gh pr *)`.
- `validatePlannedNodeTools` (`coordinator.go:754`) tests each declared tool
  for **exact string membership** in that set — `plannedToolAllowlistSet[tool]`
  — so `Bash(./gradlew *)`, `Bash(npm *)`, `Bash(cargo *)` and
  `Bash(pytest *)` are not narrower spellings that get scoped down; they fail
  the plan outright with a `*PlanError`.
- The planner is told this before it tries. The prompt renders the allowlist
  verbatim and states: *"there is no other Bash pattern available, so a node
  needing a different shell command cannot be planned; break it into steps
  that fit the list above instead"* (`coordinator.go:945-952`).

So a Kotlin node does not fail loudly for want of `./gradlew`. It never asks.
It plans the check it *can* run — `git rev-parse --abbrev-ref HEAD`, which the
prompt explicitly mandates (`branchEvidenceRule`, `coordinator.go:863-892`)
— and that check passes.

### Why appending to the list is rejected

The report's three reasons stand, and the third is decisive:

- **It is this repository's toolchain.** `Bash(go *)` and `Bash(make *)` are
  there because oh-my-graph is a Go project built with make. That is the
  author's stack sitting inside every user's security ceiling.
- **A fixed list is never complete.** Kotlin, Node, Rust, Python, Ruby,
  Java/Maven, Swift, .NET, Elixir, Bazel, Nix — plus every private wrapper
  script a company builds. Each gap is another #119, and the gaps are
  unbounded because the set is open.
- **A superset loosens the ceiling for everyone.** A Go user would carry a
  standing declarable grant for `npm` and `cargo` on every planned node,
  forever, to fix a Kotlin user's problem. Layer 0 exists to be
  least-privilege; growing it monotonically is the one direction it must not
  move.

### The structural diagnosis — agreed, and one question is missing

The report's diagnosis is correct. `plannedToolAllowlist` conflates:

1. **what class of tool is safe for unattended planner output at all** — a
   project-wide policy question, correctly answered once, in trusted code, by
   a constant; and
2. **which build command *this* repository actually needs** — a per-repo fact
   the engine can observe or the user can state.

A single hardcoded list must answer (1), and therefore cannot answer (2)
without either being wrong for most repos or being widened until it stops
answering (1). Agreed without reservation. That is the reason extension is not
a smaller version of the right fix; it is the wrong axis.

**But #119 has a third conflation in it, and no answer to (1) or (2) touches
it:** *who observed the evidence.* The verify node's `PASS` was a
`result_matches` verdict — the planner prompt tells the check node to reply
with the four characters `PASS`, and `success_check.result_matches` judges
that reply. The code says what that is worth:

> `ResultMatches` … Self-reported: a node passes it by emitting the right
> words. (`internal/graph/graph.go:178`)

> `Verification` … the only `success_check` predicate that observes state
> outside the model's narration. (`internal/graph/graph.go:109`)

Grant `Bash(./gradlew *)` and the node can now run the build — and still reply
`PASS`, and still be graded on having replied `PASS`. The ledger would read
exactly the same. Every mechanism below is chosen against this: the fix for
#119 is not primarily a tool, it is **evidence the engine gathered itself**,
plus a ledger that does not print a self-report and a measurement in the same
word.

## Decision

### 1. `plannedToolAllowlist` is not extended, and its scope is stated

The list stays exactly as it is. It is hereby recorded as answering question
(1) **only**: the class of tool safe to hand unreviewed planner output running
unattended under `dontAsk`. It is not, and must never become, a statement
about what any repository needs to build. A future PR proposing to add an
ecosystem's build command to it should be closed with a pointer here.

This leaves an asymmetry that must be named rather than quietly enjoyed:
`Bash(go *)` and `Bash(make *)` remain declarable, so a Go or Makefile-bearing
repo keeps an in-node build capability that a Gradle repo has never had. This
ADR does not remove them — that is a capability regression for working users,
paid to settle a fairness argument, and it is out of scope here. It records
the hazard instead: on an untrusted checkout, `Bash(make *)` executes
repo-authored code (a `Makefile` is a program) unattended, which is the same
hazard §3 uses to refuse detection-derived grants, already shipped. If layer 0
is ever revisited, the direction is *removal plus §2*, never extension.

### 2. Build evidence is a user-supplied verification the ENGINE runs, injected after validation

A new invocation-scoped flag on `auto` (and on the goal loop and `chat`,
wherever a plan is produced):

```sh
oh-my-graph auto "fix the failing spec" --verify-cmd './gradlew build'
```

After `validatePlannedNodes` has passed, and alongside the existing
post-validation mutations (`applyAgentMapping`, then `applySkillMapping` —
ADR 0012 §1), trusted Go code attaches a `success_check.verify` carrying that
command to the planned graph. Specifics, each of which is part of the
decision:

- **`validatePlannedNodeVerify` is unchanged.** A planner-authored `verify:`
  is still rejected outright (`coordinator.go:623`), for exactly the reason
  it always was: it is engine-run shell outside every guard. What changes is
  that *trusted code* may set the field afterwards, from a string the user
  typed. The plan the validator saw never contained it.
- **Attachment: every sink node** — every node no other node depends on. A
  DAG always has at least one; all sinks get the check; every mutating node is
  a sink or an ancestor of one. `graph.Graph` has no `Sinks()` helper today
  (only `Roots()` and `DependentsOf`, `graph.go:491`/`:538`), so this is a
  derivation the implementation lane writes, not a call to something existing.

  The soundness argument has to be stated precisely, because the obvious
  version is false. A check does **not** observe the final tree by virtue of
  finishing last: `verifyEvidence` runs at the *start* of its node's
  settlement, before `PersistOutput` and `recordPass` (`scheduler.go:797`, `:801`,
  `:810`), so a concurrent node may still be writing while a check reads.
  What carries the claim is the attachment set itself, and it needs no
  serialization: a node's check runs after that node's own subprocess, every
  non-sink is an ancestor of a sink, and an ancestor ends before its
  descendant starts — so by the time the last-*starting* check begins, every
  node's subprocess has already ended. (An earlier draft of this section
  attributed the ordering to the serialization in the next bullet. It does not
  depend on it, and saying so would have let a future reader delete the mutex
  while fully satisfying the bar that bullet sets.)

  The failure half does not rest on halt-on-fail either. `on_fail: continue`
  is a graph-level field a plan may set, and `effectiveContinueOnFail` ORs it
  with the CLI flag — its own comment says "the flag cannot force a halt onto
  a graph that declared continue" (`scheduler.go:1423-1432`). The conclusion
  survives on a different mechanism: a continue-on-fail failure is still
  appended to `prunedFailures` (`scheduler.go:592`, `:598`), which still returns
  `RunFailedError` (`:689`). So a run's PASS implies the final tree passed
  the user's command **because a failed sink fails the run under either
  failure policy**, not because the scheduler halts. Sink attachment over
  per-node is deliberate — see Alternatives.
- **The injected checks are serialized run-wide, and that is load-bearing,
  not a nicety.** The immediate reason is flake: two concurrent
  `./gradlew build` invocations in one project directory contend for the
  build daemon's locks, and a flaky check is worse than a slow one. The
  second reason is that a build is not a read-only observer — it writes
  `build/`, `target/`, `node_modules/`, generated sources — so two concurrent
  builds of one directory can each fail on the other's half-written output,
  and neither result then describes the tree the user has. What it is *not*
  is the ordering argument above, which holds either way; anyone relaxing
  this owes a replacement argument for the write-interference half, not just
  a benchmark showing Gradle tolerates concurrency. There is no serialization
  primitive for verifications today, so this is new scheduler concurrency
  machinery rather than a knob.
- **A planned node's `retry.max` gains the bound `feedback.max` already has.**
  Nothing bounds `Retry.Max` anywhere today: `internal/graph/validate.go`
  checks only the cause tokens (`validateRetryCauses`, `:218-232`), and the
  sole planned cap is `maxPlannedFeedbackRounds = 3` (`coordinator.go:638`).
  `verify_failed` is a legal retry cause (`graph.go:204`) and the retry
  decision is taken on the verify verdict (`shouldRetry`, `scheduler.go:815`),
  so a planned sink declaring `retry: {max: 40, on: [verify_failed]}` would have
  the engine run the user's `./gradlew build` 41 times, each bounded only by
  the 10-minute verify ceiling, serialized, with up to 3 feedback rounds on
  top. This is the one way planner output influences the user-supplied
  command's execution — its *count*, never its content — and #119 is a cost
  story, so the count gets a ceiling: a small fixed cap in the same place and
  shape as `validatePlannedNodeFeedback`. It changes `retry`'s recorded
  disposition; see Compatibility.
- **Timeout: `maxVerifyTimeout` (10 minutes), not the 2-minute default**
  (`graph.go:103-104`). A cold Gradle or Cargo build is precisely the case
  the 2-minute default was not sized for. `--verify-timeout` overrides,
  bounded by the same 10-minute ceiling every verification has. A build that
  exceeds it fails as a `verifyFault` — `Infrastructure: true`, "could not
  verify", no feedback arc — which is the correct reading and is already how
  the seam classifies a timeout.
- **The command is snapshotted into the saved spec**, like ADR 0012's inlined
  skill text, so `run` on a saved `graph.json` replays the check the user
  approved, and `--plan-only` prints it. **"The check the user approved" is an
  assumption about that artifact, not a property the tree provides** — §4 names
  the assumption and resolves it for `resume`, which never replays a
  snapshot-borne command — it takes one from its own command line or refuses.
  (`run` on a hand-edited `graph.json` is
  outside that: at that point it is a hand-written graph the user is choosing
  to run, which has always been allowed to carry a `verify:`.) What such a
  replay does *not* get is the run-wide serialization above: the discriminator
  for serializing is the tool ceiling, which `run` never imposes, so a saved
  plan with more than one sink re-run through `run` runs its checks
  concurrently — the write-interference case this ADR calls load-bearing.
  Single-sink plans, the common shape, are unaffected.
- **It runs through `verify.ShellVerifier`** — the second exec seam, already
  `childenv.Scrub`ed. **No new spawner, no fifth seam, no new ADR owed.**

Why this is the right shape and not a detour around the tool question: it
answers (2) with the only source that is both complete and trusted — the user
— and it answers "who observed the evidence" at the same time, because the
engine ran the command and judged its exit code itself. The node is granted
nothing. Every layer of the ADR 0004 ceiling stays byte-for-byte as it is.

**A required companion, or this mechanism half-works.** When an injected
check fails on a node carrying a feedback arc, `judgeFeedback` builds the
re-run's payload as `outcome.Result` and only falls back to the failure cause
when that result is blank (`internal/schedule/feedback.go:197-203`). For this
exact failure the node's result is the string `PASS` — the narration the
verification just contradicted. The fixer node would be re-run and handed the
word `PASS` as its feedback. So: **when the cause is a verify failure, the
payload is the verification's evidence, not the node's narration.** The
payload's size bound is the implementation lane's to pick, and it is
explicitly *not* the 240-rune `maxDetailRunes`/`outputTail` cap
(`internal/schedule/scheduler.go:63`, `:1360-1388` — the scheduler's, not the
ledger's) — that bound exists to keep a table readable, and a compiler error
list is the payload's whole point.

### 3. Repo detection is a suggestion to the human, never a grant

The engine may inspect the invocation directory for build signals (`gradlew`,
`package.json`, `Cargo.toml`, `pom.xml`, `build.gradle{,.kts}`,
`pyproject.toml`, `Gemfile`, `Makefile`, `go.mod`, `mix.exs`, `*.csproj`,
`WORKSPACE`, `flake.nix`) for exactly one purpose: to print, when no
`--verify-cmd` was given,

```text
No build verification configured. Detected a Gradle project (./gradlew).
Nothing in this run will compile or test your code — a planned node cannot
run ./gradlew, and no node's PASS carries build evidence. Re-run with
  --verify-cmd './gradlew build'
to have the engine verify the result itself.
```

Detection informs; the human authorizes. The line prints whether or not a
signal was found — "detected nothing" is the diagnosable case, the same
reasoning ADR 0012 §6 used to print `Found: 0` rather than imply it.

**Reading a file to decide a grant is refused, and the sharpest form of the
argument needs no attacker at all.** `Write` and `Edit` are in the allowlist,
so node 1 of the *same run* can create `package.json`, `Cargo.toml` or a
`Makefile` as entirely legitimate work — "initialize a Node project" is a
normal goal — and a per-node detector would then widen node 2's set. That is a
plan bootstrapping its own grant, with no untrusted checkout and no malice
anywhere. Restricting detection to plan time does not close it either: under
`--max-cycles` (ADR 0011) each cycle re-plans against a tree the previous
cycle wrote. ADR 0004's own Context already names this exact shape — *"a
write-capable planned node can drop a `.claude/settings.local.json` for a
later node to load"*
(`0004-auto-mode-tool-ceiling-by-settings-isolation.md:20-22`) — which layer 1
closes by isolating settings sources, and for which a repo-signal detector has
**no analogue**: there is no `--detect-sources ""`.

The cloned-repo case is the weaker argument, and it also holds: a repository
could plant a `package.json` to unlock `Bash(npm *)`, and that matters even
though the node already runs in that repo, for three reasons:

- **Write is auditable; exec is not.** The node's existing grant lets it
  change files, and every change is a diff the user can read afterwards.
  `npm install` runs `postinstall`; `./gradlew` executes `build.gradle` at
  configuration time. Repo-authored code executing unattended under `dontAsk`
  leaves no diff. The ceiling's stated residual — "the worst case is the node
  reporting it could not comply — visible in its artifact" (ADR 0012 §5) —
  depends on planned nodes not having arbitrary execution.
- **The precedent is already set.** ADR 0012 cut `<cwd>/.claude/skills`
  because a cloned repository shipping instructions into unattended `dontAsk`
  nodes is "100% of the genuinely new injection surface". A repo file that
  unlocks an execution grant is the same move with a larger payoff.
- **The invariant is about untrusted producers, not about the planner
  specifically.** See §4.

And detection would not even close #119 for everyone. Monorepos raise several
signals at once, so the rule must either union them (widening the ceiling
again, per-run) or pick one (wrong, silently). Repos whose build is a private
wrapper script raise no signal any detector will ever know. Detection converts
*"the author's stack leaks into your ceiling"* into *"the author's list of
stacks leaks into your ceiling"*. Better; still the wrong axis.

### 4. The load-bearing property, restated

The guarantee layer 0 provided was: **the set a planned node is validated
against is never influenced by the planner.** (That is a reconstruction, not a
quotation — the sentence does not appear in ADR 0004; the only match in that
file is the note this ADR added to it. It is what a fixed constant buys.) A
fixed constant guarantees it trivially, and §1 keeps that constant, so it
holds here by construction.

But the property as worded is too narrow, and §3 is why: repo detection
satisfies its letter — the planner does not write `package.json` — while
handing the same influence to a different untrusted producer. Restated for
every future proposal:

> The set a planned node is validated against, and any grant derived from it,
> must never be influenced by **any untrusted producer** — not the planner,
> and not the repository the run was invoked in. Only trusted code and the
> user are admissible sources.

Under this wording, §2 is clean at plan time: the source is a string the user
typed at invocation. §3's detector is clean because it produces prose, not
policy.

**§2 nevertheless admits a source the wording does not name: the persisted
snapshot.** Today *"an auto snapshot contains no `success_check.verify`"* is a
true, cheap, checkable assertion. `resume` did not make it — it reconstructs
with `graph.Parse(snap.Graph)` (`resume.go:131`, `:239`), never re-runs
`validatePlannedNodes`, and hands the result a real `ShellVerifier` (`:375`).
§2 makes a verify legitimate in a planned snapshot and so **would foreclose
that assertion**, which is why the Disposition below makes `resume` assert it
explicitly instead of inheriting it. The
consequence is a change of kind, not degree: tampering with a run directory
goes from "confuse the scheduler" to "engine-run shell outside every ceiling"
— precisely what `validatePlannedNodeVerify` exists to prevent.

The windows are narrow but real. `graph.json` is written once
(`saveGeneratedSpec`, `cmd/oh-my-graph/main.go:599`) and never rewritten;
`state.json` is rewritten at every terminal verdict (`runstate` `RecordNode`),
so an intra-leg edit there loses the race — an accident of write cadence, not a
guard. And the writer need not be an outsider: a planned node may hold bare
`Write`/`Edit` (`coordinator.go:61-64`), rendered unscoped by `toolPolicyFor`
(`:429-437`), and `validatePlannedNodeCwd`'s own comment already concedes it
"does NOT make a write-capable node safe" (`:589-596`).

**Disposition — mechanism (i), both halves, shipped.** Two mechanisms were
available, in preference order: (i) `resume`
re-supplies `--verify-cmd` and **refuses** a snapshot-borne one on an auto
graph, which restores the checkable assertion exactly; (ii) the command lives
in a separately-keyed snapshot field `resume` validates against the plan it
accompanies.

The implementation took (i)'s refusal first, because it costs nothing and the
assertion it restores is the whole of what §2 foreclosed:
`ReattachVerifyCommand` strips every snapshot-borne verification from an auto
graph and returns a `*SnapshotVerifyError` when one was there, and `continueRun`
calls it on every planned snapshot (`resume.go:244-264`; the discriminator is
the snapshot's non-empty `ToolPolicies`, since a hand-written graph's `verify:`
is the user's own reviewed artifact and must round-trip untouched). So a
`graph.json` or `state.json` edit can no longer put engine-run shell into a
resumed leg — it stops the resume instead.

**Amendment, 2026-08-18 (#198): the re-supply half, and why its absence was an
omission rather than an exclusion.** The question a reader of this section has
to be able to answer is whether `resume` took no verification *flag* on purpose.
It did not, and this ADR is the evidence on both sides:

- what was decided deliberately is that the command must come from the human and
  not from disk — the restatement above admits exactly two sources, "trusted code
  and the user". A `--verify-cmd` on `resume` satisfies that wording exactly: the
  string is typed at the resumed invocation, by the same person, into the same
  value object, under the same ceiling. There is no reading of §4 under which the
  flag is the hazard; the run directory is;
- what was deliberate about the terminal refusal was only its *direction*.
  Resuming with strictly weaker checking than the leg being continued is the
  failure this ADR is about, so given a choice between dropping the command
  silently and stopping, stopping is right. That is an argument for refusing a
  resume that supplies nothing — not for refusing one that supplies something;
- this section already said so, in the sentence "it means the flag lane owes
  `resume` the same two flags, not just `auto`", and the Status header carried
  it as unshipped. An exclusion does not get written down as a debt.

The cost of leaving the debt open was larger than "a flag is missing", and it is
what #198 reported. ADR 0009's whole claim is that a subscription session limit
is a PAUSE, not a failure: the work is banked and a later leg picks it up. For
any `auto` run carrying build evidence — which is to say, any run following this
ADR's own advice — that promise was not kept. Worse, the user learned it by
following an instruction the tool printed: `SnapshotVerifyError` said "re-supply
it with `--verify-cmd`", and `resume` answered `flag provided but not defined`.
A message that sends someone down a dead end costs more than silence, because
the next message is not believed either.

So `resume` registers `--verify-cmd` and `--verify-timeout`, and the ceiling is
untouched by construction rather than by intention:

- the same value object (`coordinator.VerifyCommand`), so the blank-command
  refusal, the 10-minute ceiling and the resolved default are one implementation
  — pinned by a test that parses the same pair through both subcommands and
  requires the two refusals to be identical;
- the same trusted-code attachment at the same sinks, through the same
  `ReattachVerifyCommand`, after the same validation, disclosed the same way;
- the same serialization (`SerializedVerifyNodes`), so a resumed leg's checks do
  not interfere with each other any more than a fresh leg's do;
- **no path only `resume` has.** `run` takes no `--verify-cmd` — a hand-written
  graph writes `verify:` on the node it means — so `resume --verify-cmd` against
  a hand-written snapshot is an error, not an attachment. Were it accepted, a
  resumed leg could attach a check no fresh run could, which would be a hole and
  not a fix.

Mechanism (ii) stays unbuilt and stays unnecessary: the persisted artifact is
still refused rather than trusted, on both legs.

**A residual §2 creates that this does not touch**, and it is the sharpest one
here: a planned node holds bare `Write`/`Edit` and runs in the invocation
directory, so it can edit the very file the user's command runs — comment out
the failing test, relax the compiler flag, make `build.gradle`'s check a no-op
— and the engine will then run the command, see exit 0, and print
`PASS (verified)`. No ceiling layer is crossed and no snapshot is tampered
with; the node did what a node with `Edit` may do. This is #119's failure mode
re-entering through the mechanism built to close it, one level down: it does
not invalidate the design (the engine really did gather the evidence, which is
what `verified` claims and all it claims — see *"`verified` is not `correct`"*),
but nothing in §2 detects it, and a user reading a `verified` row should know
that it says who ran the command, not that the command still means what it
meant when they typed it.

### 5. What the planner prompt must say

The final-check paragraph (`coordinator.go:824-842`) mandates a branch
assertion and says nothing about building or testing anything. It must change
in two ways, differently depending on whether a `--verify-cmd` was supplied:

- **With a command.** Tell the planner that the engine independently runs a
  verification after the final node, that the check node must therefore not
  attempt to prove the code correct, and that its `PASS` is a statement about
  the specific things it checked.
- **Without one.** Keep the branch assertion, and require the check node's
  reply to **state the scope of its evidence** before the verdict — what it
  ran, and what it therefore does not cover. This is the reporter's fallback
  (b), and it is worth asking for even though nothing enforces it.

The prompt is not a mechanism, and this repository has learned that
repeatedly. Removing the word `PASS` from a check node's vocabulary is not
available: `plannedVerdictPattern` and `result_matches` are the whole gate on
that node when no verify command exists (`coordinator.go:809-817` says so).
So the prompt half is a hope. §6 is the part that is not.

### 6. A verdict records how it was reached

Nothing can force a planned node to build. Something *can* stop the ledger
from printing a self-report and a measurement as the same word, and that
something needs no model cooperation at all, because the engine knows exactly
which predicates it evaluated.

Every terminal `PASS` gains a **provenance qualifier**, derived in trusted
code from the predicates that actually ran, over a closed set:

| qualifier | meaning |
|---|---|
| `verified` | a `success_check.verify` command ran and the engine judged its exit code (and `output_matches`, when declared) |
| `self-reported` | the strongest predicate was `result_matches` — the node passed by emitting the right words |
| `exit-only` | a subprocess ran and exited 0, with no predicate beyond that |
| `approved` | a human approved a `type: gate` node — no subprocess ran and no predicate was evaluated |

`approved` is not a rounding-out of the table; without it the set is not
closed. An approved gate records a ledger `PASS` with no cost and no session
(`evaluateGate`, `internal/schedule/scheduler.go`) and emits a terminal
`node_passed` (`docs/RUN-FEED.md:251`), so under a three-member set it would
land on `exit-only` — wrong twice over, because no subprocess ran and because a
person deciding is the *strongest* provenance the system has, not the weakest.
Gates exist only in hand-written graphs (`validatePlannedNodes` rejects
`type: gate`), which is exactly why §6's reach beyond the auto path forces the
fourth member.

Rendered in the ledger's summary and emitted on `node_passed` as an additive
field, per `docs/RUN-FEED.md`'s additive rule (no schema bump; absent means a
producer that predates this). `ledger.Verdict` stays `PASS`/`FAIL` — the
qualifier sits beside it, so no consumer's verdict test changes.

This applies to hand-written graphs too. It is a property of the predicate,
not of the auto path, and a hand-written graph whose check node self-reports
has told the reader exactly as much as #119's did.

Had this shipped before v0.4.1, the reporter's ledger would have read
`PASS (self-reported)` on the verify node next to `PASS (self-reported)` on
the $11.01 apply node — which is not a fixed build, but it is not a false
certification either, and it is the difference between a tool that was wrong
and a tool that lied.

## Required measurements before Accepted

None of these gate correctness of the mechanism; they gate the claims made for
it. Record each with cost and CLI version, as every prior E-number is.

- **(a) Does the prompt change alter check-node behaviour at all?** Plan the
  same goal with and without §5's wording and compare what the check node
  claims. §5 is asserted to be a hope; if it is not even that, it should be
  cut rather than carried as decoration.
- **(b) What does a sink-attached check cost on a real repo?** One `auto` run
  in a non-Go repo with `--verify-cmd`, recording the check's wall-clock
  against the run's, to confirm the 10-minute default is the right side of
  the trade.

## Failure modes

- **A repository that was already broken before the run.** Every check fails,
  including the first. Sink-only attachment (§2) makes this correct rather
  than confusing: a goal of *"make the test suite green"* is supposed to have
  a failing baseline and a passing sink, and that is precisely what the gate
  measures. Per-node attachment would have failed node 1 of a 3-node fix for
  not finishing the job alone — one of the reasons it lost.
- **A break introduced mid-run is not caught at the node that caused it.**
  The user pays for the whole run and learns at the sink. #119's ask is fully
  met (the branch is not certified), the attribution is not. Accepted; the
  ledger's failing row carries the command's output tail, and the diff is
  still there to read.
- **Concurrency skew.** With a ready set wider than one, a sink's check may
  observe a tree another node is still writing — `verifyEvidence` runs before
  its own node's `PersistOutput`, so this is not an edge case of the check
  running late. It is also not only an operator's choice: `concurrency:` is a
  graph-level field a plan may set, resolved by `effectiveConcurrency`
  (`scheduler.go:446`, `:1407-1420`), so the skew is reachable on a default
  invocation with no flag passed. The run's verdict stays sound (§2's sink
  attachment puts the last check to START after every subprocess has ended,
  and all sinks must pass), but a *particular* node's check result is
  best-effort. Serialization removes interference between checks, not skew
  between a check and a still-running node — a node re-run by a retry or a
  feedback round spawns while another sink's check may be running.
- **A planned graph can multiply the injected build's cost, and §2 bounds it.**
  Retry count is the one planner-reachable lever on a user-supplied command;
  the cap in §2 is what keeps `retry: {max: 40, on: [verify_failed]}` from
  turning one flag into forty cold builds. Un-capped, that is the worst case.
- **`--verify-cmd` is unbounded user shell.** It has exactly the standing a
  hand-written graph's `verify:` has had since ADR 0002, runs on the same
  seam, and executes repo-authored code (`gradlew`, `Makefile`, `npm`) the
  same way the user's own terminal does. Stated, not closed. The provenance
  difference from §3 is the whole point: the user chose it.
- **The command is interpolated**, like every verification
  (`resolveVerification`, `scheduler.go:1251`). A user who writes
  `{{ artifacts.x | inline }}` into
  it is splicing model output into a shell command — available to
  hand-written graphs already, and on the user either way.
- **`verified` is not `correct`.** The qualifier means a command the user
  supplied exited as expected. `--verify-cmd 'true'` yields `verified`. The
  ledger reports provenance, never adequacy.
- **A wrong suggestion.** §3's detector may name the wrong command in a
  monorepo. The cost is one inaccurate line of printed prose; it grants
  nothing and runs nothing.

## Compatibility

- **No graph schema change**, and no new `graph.Node` field, so ADR 0004 §2's
  reflection test is unaffected in shape. Its *content* changes for one entry:
  `SuccessCheck.Verify`'s recorded disposition is today "rejected for planned
  nodes" and becomes "rejected when planner-authored; may be set by trusted
  code after validation from a user-supplied string". The recorded `why` in
  `field_dispositions_test.go` must be updated in the same PR, exactly as
  ADR 0012 §5 owed for `Prompt`. `retry`'s disposition changes in the same
  file for the same PR: **allowed → constrained**, per §2's cap.
- **DESIGN.md is the spec, and drift in it is a bug** (CLAUDE.md). Three
  places contradict this ADR the moment §2 lands: the disposition table row
  `| success_check.verify | **rejected** |` (`DESIGN.md:1348`) and the `retry`
  row beneath it (`:1349`); the deny-by-default prose at `:1325-1329`
  (*"`success_check.verify:` would let it run arbitrary shell outside every
  guard. Both are **rejected**"*); and the ceiling summary at `:1585-1590`
  (*"rejects a planned node whose … or that sets `cwd`, `agent`, or
  `success_check.verify`"*). All three take the same qualifier: rejected when
  planner-authored, settable by trusted code from a user-supplied string.
- **A run with no `--verify-cmd` behaves as it does today**, plus §3's printed
  line and §6's qualifier. No existing graph, plan or saved `graph.json`
  changes meaning.
- **Run feed:** additive fields only, no schema bump, per `docs/RUN-FEED.md`.
- **A new rule for layer 0, mirroring ADR 0004 §2:** every entry in
  `plannedToolAllowlist` must carry a recorded **read-only / mutating**
  disposition, table-tested so an entry added without one fails the build.
  Nothing in this ADR consumes it today — sink attachment does not need it —
  but it is the classification any future per-node attachment rule would
  stand on, and it is one line per entry now versus an archaeology exercise
  later.
- **SECURITY.md** needs the auto-mode section amended in **two** places, not
  one: the sentence at `:66-69` ("available to hand-written graphs … and to
  nothing else"), which becomes *available to hand-written graphs and to a
  command the user supplied at invocation, never to one a plan authored*; and
  the parenthetical list of plan-time rejections at `:61-62` ("no `cwd`, no
  `success_check.verify`, no `agent`"), which sits outside that sentence and
  would otherwise stay flatly wrong.

## Alternatives considered

- **Append `gradlew`/`npm`/`cargo`/`pytest` to `plannedToolAllowlist`.**
  Rejected — Context, and §1. Author's stack, never complete, loosens everyone.
- **A. Repo detection at plan time, as a grant.** Rejected — §3. Narrow per
  run and zero-config, which is genuinely attractive; but it lets an untrusted
  checkout unlock unattended execution of its own code by shipping a filename,
  it still hardcodes an ecosystem table (so Elixir, Bazel, Nix and every
  private wrapper still hit #119), and it is undecidable in a monorepo.
  Retained in the safe half: detection prints a suggestion.
- **B. User-declared, in a per-repo config file** (`.oh-my-graph.yaml` in the
  invocation directory). Rejected, and this is the cleanest kill in the set:
  it reintroduces exactly the surface ADR 0012 refused when it cut
  `<cwd>/.claude/skills` — a cloned repository shipping a file that authorizes
  its own execution. The trust argument for "the user declared it" evaporates
  when the declaration lives in the untrusted artifact.
- **B′. User-declared in a user-level config** (`$OMG_HOME/config.yaml`).
  Not rejected on principle — it is on the right side of the trust line and it
  survives `--setting-sources ""` untouched, because it is oh-my-graph's own
  file read by oh-my-graph, never a settings source claude loads. Cut from v1
  on simplicity: unkeyed it is the standing-grant superset problem again, and
  keyed by repo path it is a config format, a merge story and a precedence
  story bought before anyone has typed the flag twice. If `--verify-cmd`
  proves tedious in practice, this is where it goes, keyed by repo path.
- **C. Detection with user override.** Rejected as a composite of A's hazard
  with B's ergonomics: the override only helps the user who already knew to
  look, while the un-overridden default is still a repo-file-derived grant.
  What survives is the honest half of the combination — detect, print,
  let the user decide (§3).
- **D′. Grant `--allow-tool 'Bash(./gradlew *)'` as an escape hatch** —
  user-typed, invocation-scoped, planner-immune. Rejected for v1 despite
  being sound, because it answers the wrong half of #119. A node holding
  `Bash(./gradlew *)` can run the build, see it fail, and still reply `PASS`
  to a `result_matches` gate — the ledger reads identically. §2 gets the
  evidence without granting anything. The real cost of not shipping it: a
  node cannot *iterate* against the compiler, and a node whose job is
  "summarize the test failures" cannot exist. Revisit when there is a
  demonstrated need §2 cannot serve, with its own measurement — and note that
  §2's companion (verify output reaching the re-run) covers the iterate case
  without the grant.
- **Per-node attachment of the injected check** — every planned node that
  declared a mutating tool. Considered seriously, and it has the better
  property: it fails the node that broke the build, so #119's $11.01 apply
  node would have failed at its own boundary instead of a downstream node
  passing. It also has a nice planner-influence story (a plan can dodge the
  check only by giving up Edit/Write). It lost on two counts: it pays a full
  build per node — up to six per run, serialized — and it is *wrong* for
  every goal whose point is to reach green, failing intermediate nodes for
  not finishing the job alone. Sinks are cheaper and semantically correct;
  the attribution loss is the accepted price.
- **A run-level post-check** — the engine runs the command once, after the
  last node, outside the graph. Cleanest planner-immunity of all, since no
  topology decision touches it. Rejected for v1: it invents a run-level
  success concept the engine does not have, with its own verdict, its own
  ledger row, its own event and its own resume semantics. Sink attachment
  reuses `success_check.verify`, the scheduler's halt-on-fail and the ledger
  as they already are. If sink attachment proves inadequate under
  concurrency, this is the upgrade path.
- **Have the planner declare the build command and validate it against a
  pattern.** Rejected on the invariant (§4): it makes untrusted output the
  source of an engine-run shell command, which is what
  `validatePlannedNodeVerify` was written to prevent, with a regex standing
  where a trust boundary belongs.
- **Leave #119 alone and only ship §6's qualifier.** Rejected as insufficient
  but recorded because it is the honest minimum: it costs almost nothing,
  needs no new input from the user, and would already have stopped the
  reported run from *reading* as verified. If the implementation lane can
  only land one thing, land §6.

## Consequences

**Positive**

- #119's failure mode closes for any user willing to type one flag: the
  branch that did not compile fails the run, on evidence the engine gathered,
  with the compiler's own output in the ledger's detail.
- **No ceiling layer is touched, no tool is granted, and no exec seam is
  added.** The claim is unscoped: `plannedToolAllowlist`, layers 1–5 and the
  four-seam invariant are all exactly as ADR 0004 and ADR 0002 left them.
- The answer is complete by construction. Elixir, Bazel, Nix and a private
  `bin/verify-everything` wrapper are all one string, with no table to
  maintain and no ecosystem left out.
- The ledger stops printing a self-report and a measurement in the same word,
  for every graph, planned or hand-written.
- The zero-config path becomes self-teaching rather than silently weaker: it
  says what it did not check, and what one flag would buy.

**Negative / trade-offs**

- **Zero-config `auto` still cannot verify a Kotlin repo's code.** The
  project's identity is that `auto` works with no setup, and after this ADR it
  still runs with no setup — it just no longer claims more than it checked.
  This is a deliberate trade of capability for honesty, and a user who reads
  the printed line and moves on gets less than option A would have given them.
- One more flag on `auto`, and a user who does not know their own build
  command gets nothing.
- A sink check adds real wall-clock to every run that uses it, at the end,
  when the user is waiting.
- §5 is a prompt change, and prompt changes are hopes. The mechanism is §6
  and §2; if measurement (a) shows §5 does nothing, it should be cut.
- The verdict qualifier will make some existing runs look worse than their
  authors think they are. That is the point, and it will still read as a
  regression to someone.

## Review findings not adopted

Recorded here rather than in a commit message, so a later reader sees what was
considered and refused as well as what was taken.

- **"Graph-level fields are planner-authored policy inputs with no recorded
  disposition; a `graph.Graph` disposition table is owed alongside the
  read-only/mutating one."** Half accepted, half rejected. The half that was
  right is fixed above: §2's *reason* was wrong (it cited halt-on-fail, which
  a plan can flip with `on_fail: continue`), and the Failure-modes framing of
  concurrency as an operator choice was wrong. The claim that no disposition
  exists is not: `internal/coordinator/field_dispositions_test.go:30-39`
  already records graph-level scope as data with its rationale — the walk
  covers `graph.Node`/`graph.SuccessCheck` *deliberately*, because that is
  where capability-granting fields live, and it names the two fields with any
  teeth: `concurrency` is clamped by `effectiveConcurrency` to the same global
  cap a hand-written graph gets, and `on_fail` only picks between two failure
  policies the operator can already select, with unknown values rejected by
  `graph.Validate`. §2 does not weaken either statement: a failed sink lands in
  `prunedFailures` under *both* policies, so `on_fail: continue` cannot buy a
  plan a passing run. A second disposition table would therefore restate an
  existing one, and this ADR does not owe it. The read-only/mutating table
  under Compatibility stands on its own grounds.

## What could not be determined

- **Whether `./gradlew build` would have caught #119's specific failure.**
  The reporter's repository is not available; that the branch "did not
  compile" is taken from the report. The mechanism is designed against the
  reported shape, not against the reproduced defect.
- **How often this bites.** n = 1. There is no measurement of what fraction
  of `auto` invocations happen outside Go/Makefile repositories, and no
  instrumentation exists that would produce one.
- **Whether the prompt half changes anything** — measurement (a), unrun.
- **The right payload bound for verification output reaching a feedback
  re-run.** Enough compiler output to fix a build, against argv-borne prompt
  cost, is an empirical question nobody has asked. The ledger's 240-rune cap
  is ruled out; the replacement is not derived.
- **Whether serializing the injected checks is necessary for flake-freedom or
  merely conservative.** Concurrent `go build` is safe; concurrent Gradle
  contends for a daemon lock. Neither was measured. This is *not* an open
  question about whether serialization may simply be dropped on evidence:
  §2's soundness argument now rests on it, so a measurement showing Gradle
  tolerates concurrency buys a faster check only together with a replacement
  argument for "some check observed the final tree".
- **Whether the planner reliably produces a sink where a human would put
  one.** Sink attachment assumes the graph's terminal node is a sensible
  place for a final gate. Unmeasured over planner output.
