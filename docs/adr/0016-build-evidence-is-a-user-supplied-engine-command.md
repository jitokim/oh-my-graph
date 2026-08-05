# ADR 0016 — Build evidence for a planned node is a user-supplied engine command, not a wider tool grant

- Status: Proposed — decision taken, **nothing implemented.** No flag, no
  injection, no verdict qualifier and no detector exists in the tree as of
  2026-08-06; read the Decision as the shape the code is meant to take. It
  needs no measurement gate: unlike ADR 0004 and ADR 0012, nothing here
  changes a node's argv, its tool set or any ceiling layer, so there is no
  CLI-behaviour premise to probe. What it does owe before Accepted is stated
  under "Required measurements".
- Date: 2026-08-06
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
- `validatePlannedNodeTools` (`coordinator.go:686`) tests each declared tool
  for **exact string membership** in that set — `plannedToolAllowlistSet[tool]`
  — so `Bash(./gradlew *)`, `Bash(npm *)`, `Bash(cargo *)` and
  `Bash(pytest *)` are not narrower spellings that get scoped down; they fail
  the plan outright with a `*PlanError`.
- The planner is told this before it tries. The prompt renders the allowlist
  verbatim and states: *"there is no other Bash pattern available, so a node
  needing a different shell command cannot be planned; break it into steps
  that fit the list above instead"* (`coordinator.go:782-788`).

So a Kotlin node does not fail loudly for want of `./gradlew`. It never asks.
It plans the check it *can* run — `git rev-parse --abbrev-ref HEAD`, which the
prompt explicitly mandates (`coordinator.go:823-835`) — and that check passes.

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
  is still rejected outright (`coordinator.go:592`), for exactly the reason
  it always was: it is engine-run shell outside every guard. What changes is
  that *trusted code* may set the field afterwards, from a string the user
  typed. The plan the validator saw never contained it.
- **Attachment: every sink node** — every node no other node depends on. A
  DAG always has at least one; all sinks get the check; every mutating node is
  a sink or an ancestor of one; and the **last** sink to complete observes the
  final tree. Since the run fails if any sink's check fails (halt-on-fail),
  a run's PASS implies the final tree passed the user's command. This is
  chosen over per-node attachment deliberately — see Alternatives.
- **The injected checks are serialized run-wide.** Two concurrent
  `./gradlew build` invocations in one project directory contend for the
  build daemon's locks; a flaky check is worse than a slow one.
- **Timeout: `maxVerifyTimeout` (10 minutes), not the 2-minute default**
  (`graph.go:103-104`). A cold Gradle or Cargo build is precisely the case
  the 2-minute default was not sized for. `--verify-timeout` overrides,
  bounded by the same 10-minute ceiling every verification has. A build that
  exceeds it fails as a `verifyFault` — `Infrastructure: true`, "could not
  verify", no feedback arc — which is the correct reading and is already how
  the seam classifies a timeout.
- **The command is snapshotted into the saved spec**, like ADR 0012's inlined
  skill text, so `run`/`resume` on a saved `graph.json` keeps the check the
  user approved, and `--plan-only` prints it.
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
when that result is blank (`internal/schedule/feedback.go:212-215`). For this
exact failure the node's result is the string `PASS` — the narration the
verification just contradicted. The fixer node would be re-run and handed the
word `PASS` as its feedback. So: **when the cause is a verify failure, the
payload is the verification's evidence, not the node's narration.** The
payload's size bound is the implementation lane's to pick, and it is
explicitly *not* the ledger's 240-rune `outputTail` cap — that bound exists to
keep a table readable, and a compiler error list is the payload's whole point.

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

**Reading a file to decide a grant is refused, and the report's own question
answers it.** Yes, a cloned repository could plant a `package.json` to unlock
`Bash(npm *)`. And yes, that matters even though the node already runs in that
repo, for three reasons:

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

ADR 0004's guarantee was: **the set a planned node is validated against is
never influenced by the planner.** A fixed constant guarantees it trivially,
and §1 keeps that constant, so it holds here by construction.

But the property as worded is too narrow, and §3 is why: repo detection
satisfies its letter — the planner does not write `package.json` — while
handing the same influence to a different untrusted producer. Restated for
every future proposal:

> The set a planned node is validated against, and any grant derived from it,
> must never be influenced by **any untrusted producer** — not the planner,
> and not the repository the run was invoked in. Only trusted code and the
> user are admissible sources.

Under this wording, §2 is clean: the source is a string the user typed at
invocation. §3's detector is clean because it produces prose, not policy.

### 5. What the planner prompt must say

The final-check paragraph (`coordinator.go:823-835`) mandates a branch
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
that node when no verify command exists (`coordinator.go:731-744` says so).
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
| `exit-only` | no predicate beyond the subprocess exiting 0 |

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
- **Concurrency skew.** Under `--concurrency > 1` a sink's check may observe
  a tree another branch is still writing. The run's verdict is still sound
  (the last sink to finish sees the final tree, and all sinks must pass), but
  a *particular* node's check result is best-effort. Serialization (§2)
  removes the interference, not the skew.
- **`--verify-cmd` is unbounded user shell.** It has exactly the standing a
  hand-written graph's `verify:` has had since ADR 0002, runs on the same
  seam, and executes repo-authored code (`gradlew`, `Makefile`, `npm`) the
  same way the user's own terminal does. Stated, not closed. The provenance
  difference from §3 is the whole point: the user chose it.
- **The command is interpolated**, like every verification
  (`scheduler.go:1159`). A user who writes `{{ artifacts.x | inline }}` into
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
  ADR 0012 §5 owed for `Prompt`.
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
- **SECURITY.md** needs the auto-mode section amended: planned nodes may now
  carry a `success_check.verify`, which that document currently states is
  "available to hand-written graphs … and to nothing else". The sentence
  becomes: available to hand-written graphs and to a command the user supplied
  at invocation, never to one a plan authored.

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
- **Whether serializing the injected checks is necessary or merely
  conservative.** Concurrent `go build` is safe; concurrent Gradle contends
  for a daemon lock. Neither was measured; serialization is chosen as the
  cautious default and can be relaxed on evidence.
- **Whether the planner reliably produces a sink where a human would put
  one.** Sink attachment assumes the graph's terminal node is a sensible
  place for a final gate. Unmeasured over planner output.
