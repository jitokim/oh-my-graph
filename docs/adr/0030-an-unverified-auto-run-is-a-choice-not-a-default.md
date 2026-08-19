# ADR 0030 — An unverified `auto` run is a choice, not a default

- Status: **Proposed — implemented 2026-08-20; §8's measurements are still owed
  before Accepted.** The implementation lane delivered everything this record
  called for: the refusal (`coordinator.RequireBuildEvidence` /
  `*MissingBuildEvidenceError`), the flag (`--accept-no-build-evidence`), the
  snapshot field (`runstate.BuildEvidence`), the two disclosure sites
  (`VerifyAdvice`'s declared variant and `noteMissingBuildEvidence` on the plan
  screen), exit 3, the pinned tests of §4
  (`cmd/oh-my-graph/buildevidence_test.go`), the documentation and plugin
  surfaces of §7, and the `## [Unreleased]` CHANGELOG entry. What is NOT done is
  §8: (a) the firing rate and the exits taken, and (b) the table's
  false-positive rate. Until those exist this stays Proposed.
- **Reviewed 2026-08-20, after implementation.** One defect and four coverage
  gaps, all at the seams between legs and surfaces rather than in the fresh
  `auto` leg: the resumed leg's recorder **erased** `build_evidence`, which is
  the one thing §2.6's `resume` row relies on (fixed; §2.6, §4.1(9)); `chat`'s
  launch, the goal loop's later cycles, Codex and the inherited `"."` hazard are
  now pinned (§4.1); §4's claim about `t.Chdir` was wrong about its own lane's
  output and is corrected in place; the probe now reports by default so the
  command this header cites works off this machine; and three trades that were
  being inherited rather than chosen are written down — chat's session-long
  staleness window and the quickstart's opt-out-first order (§6), and the fact
  that no finished-run surface reads the record back yet (§2.5b).
- **Reviewed again 2026-08-20, on the final state of the lane.** All gates green
  and nothing blocking; four findings, all in this record's prose or its
  honesty about its own numbers rather than in the mechanism. One was a real
  user-visible defect: both receipt sentences said *"this run's `state.json`
  records it"*, which `--plan-only` contradicts twice on one screen because it
  writes no `state.json` at all — the wording is now conditional at both sites
  and the missing `--plan-only` + opt-out cell of §4's matrix is pinned (§2.5b).
  The other three are limits this record was overstating past: §8(a)'s N counts
  launches that got a run directory, not launches, so the firing rate it yields
  is a **floor** and an abandoned refusal is invisible (§8a, §9); and the one
  glob-matched marker has a false negative the `os.Stat` markers cannot have
  (§6).
- Date: 2026-08-20
- **Revised 2026-08-20 after design review, before any code existed.** Six
  changes, each argued where it lands: the recording was **inverted** relative to
  the measurement it exists to enable and is now written on every `auto` launch,
  not only the declared one (§2.5, §8a); the gate **moved out of
  `autoFlags.parse`** to the site that already detects (§2.1); the contradiction
  refusal moved out of `VerifyCommand` to the FlagSet that registers both flags
  (§2.3); the chat answer is filed as a **disclosure, not a declaration** (§2.5,
  §2.6); the refusal's **channel and exit code** are specified (§2.4); and the
  greenfield exemption, the agent aliaser, the documented invocations that break
  and the authorize-the-suggestion alternative are written down rather than left
  to be discovered (§5, §6, §7).
- Measurement: [`docs/measurements/0030-auto-runs-carry-no-build-evidence.md`](../measurements/0030-auto-runs-carry-no-build-evidence.md),
  re-derivable with
  `python3 docs/measurements/probes/0030-auto-build-evidence/count.py`, which
  reports the reader's own corpus. Its `--check` mode pins the frozen numbers in
  that file and is expected to report `CORPUS MOVED` on any other machine, and
  on this one from the next `auto` run onward. That probe measures ADR 0016's
  definition (a `success_check.verify` in the snapshot's graph) and **cannot
  answer §8(a)**, whose strata are read from the `build_evidence` block this ADR
  adds; a second probe is part of what §8 owes.
- Issues: [#119](https://github.com/jitokim/oh-my-graph/issues/119) is the
  precedent this acts on; this record opens no new one.
- **Amends `0016-build-evidence-is-a-user-supplied-engine-command.md` §3** in
  exactly one direction: build-signal detection, which §3 confined to producing
  prose, may now decide a **refusal**. It still may never decide a grant. §3.5
  below states the amended rule and why the direction is the whole of the
  safety argument. Everything else ADR 0016 decided — the untouched
  `plannedToolAllowlist`, sink attachment, the serialization, the retry cap,
  the `resume` re-supply rule, the verdict provenance qualifier — stands
  exactly as written.
- Line citations are anchors for a reader, not addresses the code maintains.
  When one disagrees with the file, trust the named symbol.

## 1. Context

### 1.1 What the corpus says, and what it is not allowed to say

Of this machine's **8 `auto` runs, 1 carries engine-run build evidence** — the
run of 2026-08-18, where the operator passed `--verify-cmd`. Seven ended with
every judgement in the run made by the model, about its own work, on a
`result_matches` gate a node passes by emitting the right word
(`internal/graph/graph.go`, `ResultMatches`: *"Self-reported"*).

The measurement file above carries the rows, the definitions and the script.
It also carries the correction that must travel with the number: **six of those
seven predate `--verify-cmd`,** which shipped 2026-08-06 with ADR 0016. The
eligible corpus is n = 2 and splits 1/1. So:

- **"7 of 8 auto runs verified nothing" is established.** It is the shape of a
  normal run on this machine.
- **"the printed notice does not work" is NOT established** by it, and this ADR
  does not rest on it. Six of the seven never saw the notice.

One further figure was supplied with this task — that an advisory which fired
30 times moved the behaviour it warned about from 42% to 33% (n = 43+43, no
significance test). **No artifact in this repository records it**, so it is
carried here as reported and un-reproduced, and nothing below depends on it. It
is a reason to *doubt* notices, not a finding about this one.

### 1.2 The structural fact, which needs no corpus at all

This is what the decision actually rests on, and it is checkable in the tree
today:

- a **planned node cannot carry `success_check.verify`**. A planner reply is
  untrusted input, and `validatePlannedNodeVerify` refuses the field outright
  (`internal/coordinator/coordinator.go`);
- a planned node **cannot declare a build tool** either. `plannedToolAllowlist`
  is exact-string-matched, so `Bash(./gradlew *)`, `Bash(npm *)` and
  `Bash(cargo *)` fail the plan (ADR 0016 §1, kept);
- therefore, **without `--verify-cmd` an `auto` run has, by construction, no
  engine-run evidence at all.** The only terminal predicate left is
  `result_matches` on a node's own reply — and that node wrote the reply.

The archive records what that costs, from #119: the planner gave its verify
node `Bash(git *)` only, so the node checked that a branch existed, never
compiled anything, and replied `PASS` in 17 seconds for $0.13 — after the node
before it spent $11.01. The real build then failed on a compile error. Every
row in the ledger read `PASS`.

ADR 0016 built the remedy and made it **opt-in**. The remaining defect is not
that the remedy is missing; it is that its absence is **silent by default** in
a repository where a build plainly exists.

### 1.3 The notice already exists, and the run walks past it

`coordinator.VerifyAdvice` (`internal/coordinator/verifycmd.go`) already prints,
before the planner call:

```text
No build verification configured. Detected a Go module (go.mod).
Nothing in this run will compile or test your code — a planned node cannot
run a build command, and no node's PASS carries build evidence.
Re-run with
  --verify-cmd 'go build ./...'
to have the engine verify the result itself.
```

Then the run proceeds. Every word of it is true, and it changes nothing about
what happens next. A sentence that describes a defect and then commits it is
the weakest instrument this repository has; it costs one screen of scrollback
and buys a feeling of having been warned.

**The change this ADR makes is not to the wording. It is to the control flow.**

## 2. Decision

### 2.1 A build signal with no `--verify-cmd` is a refusal

When `auto` is invoked, `DetectBuildSignals(".")` finds at least one marker,
and no `--verify-cmd` was supplied, **`auto` refuses**: it prints the message
in §2.4 to stdout and exits 3 (§2.4).

The refusal happens **where the detection already happens**: in
`runAutoWithRuntime` (`cmd/oh-my-graph/main.go`), at the line that printed the
advice — the one place in this CLI that scans the invocation directory for build
markers today. As shipped that line is
`answerBuildEvidence(os.Stdout, verifyCommand, flags.buildDeclaration(), ".")`,
the helper §4 names, which does the scan and calls `noteVerifyAdvice` with its
result.
One
`DetectBuildSignals(".")` call in that scope now feeds three consumers: the
advice line it already fed, the gate, and the recording (§2.5).

That placement is before the planner call and therefore before any spend, which
is load-bearing twice over: a refusal that cost a planner call would be a worse
version of the notice, and it is what makes `--plan-only` refuse identically
(§2.6) without special-casing — `planOnly` is passed *into* `planAndExecute`
(`main.go`), which is downstream of this line.

**Deliberately not in `autoFlags.parse`, and the first draft of this ADR was
wrong to put it there.** The parse placement satisfies every property claimed
above — before the planner call, before any spend, `--plan-only` gated for free,
`resume`/`chat`/`run` untouched, `auto --help` still answered first — so the
argument has to be made against *this* site, not, as the first draft did,
against `checkVerifyFlags`. It loses on three counts:

- **`parse` is a pure function of `args`** (`cmd/oh-my-graph/flags.go`), and
  every other check in it is flag-vs-flag consistency. Adding an environment
  probe makes every existing and future `parse` test's result depend on the
  directory the test binary runs in — passing today only by the accident that
  `cmd/oh-my-graph/` holds no marker while the repo root holds `go.mod`. Testing
  the gate would then need `t.Chdir`, which forecloses `t.Parallel` for that
  test.
- **It would be two `DetectBuildSignals` sweeps per invocation**, at two call
  sites that must agree on the directory, with nothing pinning the two `dir`
  arguments together — the refusal-message tests pin what is *said*, not where it
  was looked for, so a gate scanning one directory while the advice line scanned
  another would pass all of them. Here there is one sweep and the question does
  not arise; §4's test 8 pins that it stays one.
- **The detected signals are needed downstream.** §2.5 records them, which means
  they must reach `newRunRecorder` (`main.go`) — already 9 parameters, reached
  through `commonRunFlags`, which `run` also uses. Detected at `parse`, they
  arrive there through a struct `run` shares; detected here, they are already in
  the scope that builds the coordinator options and calls `planAndExecute`.

`answerBuildEvidence` takes `dir string` precisely so this is testable without
process-global state (`cmd/oh-my-graph/verifycmd.go`), and the gate inherits
that.

Deliberately **not** inside `checkVerifyFlags` either, which `resumeFlags.parse`
also calls: that helper exists so the two subcommands cannot diverge on the flag
pair, and the gate is `auto`-only (§2.6). Sharing it would gate `resume` by
accident — the one change here that would strand a paused run.

The predicate itself is a pure function in `internal/coordinator/verifycmd.go`,
beside `VerifyAdvice` and `DetectBuildSignals`, taking the supplied command, the
human's declaration (§2.3) and the detected signals, and returning both the
recorded outcome (§2.5) and — when the answer is a refusal — a typed
`*MissingBuildEvidenceError` carrying the detected signals. **It is not called
from `Coordinator.Plan`.** Putting it in the library would gate `chat` too, and
`chat` has no flag to name — see §2.6 and #198's lesson about instructions the
tool cannot honour.

### 2.2 No build signal is not a gate

If `DetectBuildSignals` finds nothing, `auto` runs exactly as it does today,
and prints exactly the notice it prints today ("Detected no build signal in
this directory"). No flag is required and none is suggested.

This is not a softening; it is the boundary of the defect. #119 is a repository
that had a build and did not run it. A directory with no build system has
nothing for an evidence command to be evidence about, and demanding a flag
there is friction with no defect behind it. The existing notice already
distinguishes the two cases (`VerifyAdvice`'s `case 0`), and this ADR keeps
that distinction rather than inventing one.

It is also the **negative control** the test list pins (§4): without a test
asserting that the un-signalled case still runs, the gate could widen to "always
refuse" and every other test would still pass.

### 2.3 The opt-out is `--accept-no-build-evidence`

One flag, on `auto`. It takes no value.

**Why that name.** The flag's job is to make the operator state a true thing
about the run they are about to start — *this run carries no build evidence* —
rather than to switch something off. Names were judged on what the operator is
made to say, not on brevity:

| candidate | what typing it asserts | verdict |
| --- | --- | --- |
| `--no-verify` | "turn verification off" | **rejected.** There is nothing to turn off: no verification was ever going to run. It describes a feature the run does not have, and it is the spelling every other tool uses for "skip the check I would otherwise have done" (`git commit --no-verify`), which is a different act. |
| `--skip-verification` | "skip the check" | rejected, same defect. Nothing is skipped; nothing exists. |
| `--no-build-evidence` | ambiguous — a switch or a statement | rejected. Reads as the `--no-agent-mapping` / `--no-skill-activation` family, which really are feature switches. Family resemblance is exactly the wrong signal here. |
| `--unverified` | "this run is unverified" | close, and rejected only on ambiguity: unverified by whom, and of what. It does not say *build*. |
| `--accept-no-build-evidence` | "I accept that this run carries no build evidence" | **chosen.** Subject, verb, object. It is a sentence the operator says, not a knob they turn, and it names the exact thing that is absent. It reads correctly in the three places it will be read: on the command line, in the snapshot, and in a CHANGELOG line someone skims a year from now. |

The name is deliberately long. A flag that is annoying to type once per run is
working as designed; a flag that gets aliased into a shell function — or, more
realistically, reached for by an agent that cannot satisfy the other exit (§6) —
is the failure mode, and no name prevents either. Only the recording in §2.5
makes it visible.

**With `--verify-cmd` it is a contradiction and is refused** — in
`autoFlags.parse` (`cmd/oh-my-graph/flags.go`), beside the `--plan-only` /
`--max-cycles` refusal, because that is exactly what it is: a flag-vs-flag
consistency check over the one FlagSet that registers both flags
(`newAutoFlags`). The operator would be declaring an absence and supplying the
thing whose absence they declared; refusing beats picking a winner, because
either winner silently discards something they typed.

**Not in `VerifyCommand.Validate`, and the first draft of this ADR was wrong to
put it there.** That value object is shared with `resume`, through both
`checkVerifyFlags` and `ReattachVerifyCommand`
(`internal/coordinator/verifycmd.go`), so storing the opt-out there would create
a field `resume` can never set and a branch `resume` can never reach. Worse, it
contradicts this ADR's own reasoning twice over: §2.1 refuses to share
`checkVerifyFlags` *because* `resume` shares it, and §2.3's table spends its
whole length arguing that the opt-out is **not** a verification switch — then
the first draft stored it inside the type whose doc comment says it is "the
whole of `--verify-cmd` / `--verify-timeout`".

The stated driver was §7's "no existing exported signature changes." That was a
self-imposed constraint bought at the price of a cohesion violation:
`internal/coordinator` has no consumers outside this module, so a signature
there is free to change. `VerifyCommand` therefore gains **no field**; the
declaration is passed as an argument to the predicate of §2.1 and to
`VerifyAdvice`'s declared variant (§2.5b), which is where a run-launch fact
belongs. (`resume` registers no opt-out flag, and now has no field for one
either; its refusals are unchanged in both spelling and reachability.)

**With no build signal it is accepted and inert.** A script that always passes
it must not break when run in a directory with no build system. The run is then
recorded as `none-detected` rather than `declared` (§2.5) — the flag answered a
question that was never put, and filing it as a declaration would inflate
exactly the stratum §8(a) exists to count.

### 2.4 What the refusal says, verbatim

One detected signal:

```text
auto: this directory has a build system, and this run would check none of it.

Detected a Gradle project (gradlew).

A planned node cannot carry a build command: the planner's reply is untrusted
input, so success_check.verify is refused from a plan, and no allowed tool
runs a build. A check node's PASS is words it emitted, not a build that ran.
Without --verify-cmd, every judgement in this run is the model's, about its
own work.

Re-run with ONE of:

  --verify-cmd './gradlew build'
      the ENGINE runs that command at each sink node of the plan and judges
      its exit code itself. No node is granted anything.

  --accept-no-build-evidence
      run anyway, on the record: this run carries no build evidence. The
      choice is written to the run's state.json and printed with the plan.

Nothing has been spent — this is refused before the planner call.
```

Several signals swap one line, matching `VerifyAdvice`'s existing three-way
shape (the suggestion is the first signal in the table's priority order, which
is why the wrapper beats the file it wraps):

```text
Detected several build signals (gradlew, package.json), so the command below
is a guess.
```

Four properties of this text are part of the decision, not of its prose:

1. **It names what was detected**, by ecosystem and by file, so an operator who
   thinks the detection is wrong can see the exact marker and say so.
2. **It names both exits and what each buys**, so it is actionable in one read.
   A refusal with one exit is a wall; a refusal with two is a question.
3. **It names only flags `auto` registers.** This is #198's rule, and the
   implementation lane owes the same automated check `resume` has
   (`TestSnapshotVerifyRefusal_NamesOnlyFlagsResumeRegisters`) — a refusal that
   sends someone to a flag the tool rejects costs more than silence, because the
   next message is not believed either.
4. **It says nothing was spent.** The single most likely reading of a refusal
   from a tool that bills is "I have been charged for this"; saying otherwise is
   one line.

**Verbatim means a channel and a prefix, and "returns an `error`" gives it
neither.** An error out of a subcommand reaches `mainExitCode`
(`cmd/oh-my-graph/main.go`), which prints
`fmt.Fprintf(os.Stderr, "oh-my-graph: %v\n", err)` — so the first line above
would arrive as `oh-my-graph: auto: this directory has a build system…`, double
prefixed, with the other 19 lines and their indentation on stderr, where the
notice this replaces goes to **stdout**. The precedent for the fix is three
lines up in the same function: `usageRequest` is matched with `errors.As`,
**prints itself** to stdout, and the `oh-my-graph:` prefix is suppressed.
`*MissingBuildEvidenceError` takes exactly that shape — matched by `errors.As`
in `mainExitCode`, printing its own text to **stdout**, unprefixed, so §2.4 is
achievable as written. Its `Error()` string stays the first line alone, which is
what a wrapping caller and a test assertion want.

**The exit code is 3**, and it is new. 1 is a failed run and 2 is a paused one
(`exitCodeForError`), and a refusal is neither: nothing ran, nothing is
resumable, nothing was billed. §7 says CI calling `auto` will "break loudly" —
that is only true if the break is *distinguishable*, and exit 1 would make a
refusal indistinguishable from the failing build the operator is trying to
catch. A script can then branch on 3 to add a flag rather than to page someone.
Note what 3 does not disturb: ADR 0023 §2.6 asserts that an exit code agrees
with the run's derived status, and a refused invocation **creates no run
directory**, so it is outside that assertion rather than a new case within it.

### 2.5 The run records the question and its answer, in two places a reader meets

An absence that was chosen and an absence that was an accident look identical in
a finished run today. After this they do not — and, equally, a run that was
never *asked* is distinguishable from both, which is what makes §8(a) a
measurement rather than a count of one stratum.

**(a) The snapshot** — `internal/runstate`, an additive optional field, written
on **every** auto-mode launch:

```go
// BuildEvidence records the launch-time build-evidence question and its answer:
// what was detected in the invocation directory, and how the run answered.
// Written on every auto-mode launch (auto and chat's graph turns), including
// the ones that answered by attaching a command and the ones where there was
// nothing to answer. Absent means a run that predates this field, or a `run` of
// a hand-written graph, which never asks the question (§2.6).
BuildEvidence *BuildEvidence `json:"build_evidence,omitempty"`

type BuildEvidence struct {
	// Answer is one of four values, and the set is closed:
	//   "attached"       — --verify-cmd was supplied; the engine runs it at
	//                      each sink. Signals may be empty or not; the
	//                      attachment itself is in the graph.
	//   "declared"       — signals were detected and a human typed
	//                      --accept-no-build-evidence, answering this
	//                      question and no other.
	//   "disclosed"      — signals were detected and a chat [y/N] approved a
	//                      plan screen that stated the absence. ONE keystroke
	//                      covered two questions; this is weaker than
	//                      "declared" and is filed apart from it (§2.6).
	//   "none-detected"  — the directory raised no signal, so no gate applied
	//                      and nothing was declared. The greenfield run lands
	//                      here (§6).
	Answer string `json:"answer"`
	// DeclaredBy is the exact spelling of what the human typed, for the two
	// answers a human gives: "--accept-no-build-evidence" or "chat-confirm".
	// Empty for "attached" and "none-detected".
	DeclaredBy string `json:"declared_by,omitempty"`
	// Signals are the marker files detected at launch, in the detection
	// table's order — what the human was told when they answered. Empty is
	// meaningful and is the whole point of writing this on every run.
	Signals []string `json:"signals,omitempty"`
}
```

**Why the wider version, and why the narrow one was a bug in this ADR.** The
first draft wrote the field only when *"no verification was attached, at least
one signal was detected, and the run proceeded by declaration"* — the single
path that was **already** visible, because the operator had typed a declaration.
Every silent path stayed silent: a run that passed with `--verify-cmd` recorded
no `signals` (the attachment is in `graph.json`; the *detection* was nowhere), a
run in a signal-free directory recorded nothing at all. So §8(a) — "how many
directories raised a signal, how many answered with `--verify-cmd`, how many
with the opt-out" — had only its third number, and its **denominator was exactly
as unrecoverable after shipping as it is today.** The measurement file names
that same blind spot as the reason the current corpus cannot say which of its 7
unverified runs would have been gated: *"A snapshot records no invocation
directory"* (`docs/measurements/0030-auto-runs-carry-no-build-evidence.md`).
Shipping a field that reproduces the blind spot it was written to close would
make §8(a) a wish. Recording the detection outcome on every launch is one
field's worth of extra writing and is the difference.

Nothing in ADR 0016 §4 blocks the wider version: the argument below — that the
field is inert and carries no command — holds verbatim for `attached` and
`none-detected` rows too.

**Schema stays 3.** An absent field is a run that predates this, or a `run` of a
hand-written graph; no reader of either version can misread it (the same
reasoning ADR 0025 used for `runtime`, reaching the opposite conclusion on
`omitempty` because here absence is legitimately meaningful).

**What this field is NOT:** an input to anything. Nothing reads it to decide
behaviour, on this leg or on a resumed one. In particular it is not ADR 0016
§4's rejected mechanism (ii) wearing a new hat: it carries **no command** — the
`Signals` are marker filenames, not the suggested commands the detection table
holds beside them — so there is nothing in it a later leg could execute. The run
directory remains an inadmissible source of engine-run shell, on both legs.

**(b) The printed disclosure**, at both sites where a human meets the run:

- **before the planner call**, `VerifyAdvice` gains a variant for the declared
  case — the same paragraph it prints today plus the sentence *"You said so with
  `--accept-no-build-evidence`; a run launched this way records it in
  `state.json`."* It takes the declaration as a new argument (§2.3), not as a
  field on `VerifyCommand`. The un-signalled case's text is unchanged;
- **with the plan**, in the slot `noteVerifyAttachments` occupies
  (`printPlan`, `cmd/oh-my-graph/main.go`). That slot states either what the
  engine will run at each sink, or that nothing will — never neither. This is
  the screen `--plan-only` prints and the screen chat's `[y/N]` gates, which is
  what makes §2.6's chat answer work at all. Its declared line reads *"you said
  so with `--accept-no-build-evidence`; a run started from this plan records it
  in `state.json`."*

**Neither receipt sentence says "this run", and that is load-bearing.** Both
are printed before anything knows a run follows: `--plan-only` reaches the same
gate and the same plan screen (§2.6) and then mints no run id at any point, so
it writes no `state.json` for the sentence to be about — and the preview's own
last paragraph goes on to say exactly that ("it gets no run directory"). The
first implementation of both sentences said *"this run's `state.json` records
it"*, which made `auto --plan-only --accept-no-build-evidence` contradict itself
twice on one screen. The disclosed line was already phrased conditionally
("approving this plan accepts that"); the declared ones now match it. Pinned by
`TestRunAutoWith_PlanOnlyDeclaredPromisesNoRecordItDoesNotWrite`, which asserts
the promise's absence *and* that no run directory follows — the text alone would
keep passing if the preview started writing snapshots, and the directory alone
would keep passing while the screen lied about it.

**Both printed sites are before the run, and that is the whole of what this ADR
ships.** Nothing reads the field back afterwards: `show`, `runs`, the dashboard
and the run feed are all untouched (§2.7, §7), so a reader of a **finished** run
gets the record by opening `~/.oh-my-graph/runs/<id>/state.json`. Recorded here
as a deliberate limit rather than left to be discovered: the field exists for
§8(a) first and for a human reader second, `show` is the natural home for the
second, and adding a surface is a change to a command this ADR otherwise does
not touch. It is follow-up work, not part of this decision.

### 2.6 Which surfaces the gate applies to, pinned

| surface | gated? | why |
| --- | --- | --- |
| `auto` | **yes** | The defect's home. |
| `auto --plan-only` | **yes**, identically | A preview that refuses differently from the run it previews is its own defect. It falls out for free: the gate is upstream of `planAndExecute`, which `--plan-only` is passed *into*, so it is reached before the preview buys its planner call. It also *saves* the user money in the refused case, where today they would pay for a plan they then have to re-request with a flag. |
| `chat` | **no refusal**; disclosure and recording only | `chat` registers no verification flags at all, so a refusal there could only name a flag `chat` rejects — the exact dead end #198 was. What `chat` gets instead is the absence stated on the plan screen its `[y/N]` gates (§2.5b), and the run recorded with `answer: "disclosed"`, `declared_by: "chat-confirm"` — filed **apart from** `auto`'s declarations, never merged into them. |
| `run` | **no**, and no recording | A hand-written graph carries its author's own `success_check.verify` and is a reviewed artifact. Out of scope by construction, as ADR 0016 §2 has it — it never asks the question, so it writes no `build_evidence` field either (§2.5a). |
| `resume` | **no** | The gate is a **launch-time** gate. The choice was made once, and the snapshot the resume loads records it — **and the resumed leg's recorder base carries `build_evidence` forward**, which is what makes the previous clause true after the leg as well as before it. `SnapshotRecorder` rewrites the whole snapshot on every settled node, so omitting the field there does not fail to add one, it *erases* the first leg's (`cmd/oh-my-graph/resume.go`, `TestResume_CarriesTheDeclarationIntoTheSecondLeg`). Re-asking, meanwhile, would make a paused run un-resumable without re-typing a declaration already on file, which is ADR 0009's promise broken and #198's defect repeated. A run launched before this ADR resumes untouched. |
| `--runtime codex` (ADR 0025) | **yes**, identically to Claude | Not derived, stated: a Codex `auto` reaches the same `runAutoWithRuntime`, so it meets the gate at the same line, by construction rather than by intention. It is worth stating because the *reason* for the gate reads differently there — a Codex node's ceiling is a filesystem sandbox, not a tool allowlist, so §1.2's "`plannedToolAllowlist` refuses `Bash(./gradlew *)`" is not the argument on that runtime. The argument that does carry over is the one that matters: `validatePlannedNodeVerify` refuses `success_check.verify` from a plan on **either** runtime, so a Codex `auto` has no engine-run evidence without `--verify-cmd` either, and a sandbox that permits a build command still leaves the verdict to the node that ran it. The refusal text, flag and recording are runtime-independent. |

**Chat's answer is the weakest thing in this record, and it is filed as what it
is.** One `y` covers two questions — *run this plan* and *accept that it proves
nothing* — where `auto`'s operator answers the second one separately. The first
draft of this ADR put both into one `declared_by` column under a field whose own
doc said it recorded *"that a human said so"*, which files a non-declaration
under the name of a declaration: every future reader of §8(a) and §9 would have
had to *remember* to segregate them, with no structural help, while the chat
rows dominated by volume on any interactive machine — exactly the confound the
column exists to resolve. So the **`Answer` values are split at the source**:
`declared` for a flag typed at the question, `disclosed` for a plan screen that
stated it and was not challenged. The plan-screen disclosure stands on its own
and does not need to borrow the word.

The alternatives to disclosure were a dead-end refusal (worse: it strands the
user), a second confirm prompt (a second `[y/N]` for one action, bought before
anyone has complained), or giving `chat` the full flag pair (a larger change
than this ADR, and ADR 0016 §2 already carries `chat --verify-cmd` as unshipped
work). If the `disclosed` rows come to dominate, that is the evidence for
revisiting — and now it is a row count, not a recollection.

### 2.7 What does not change

- **The planner prompt is untouched.** The corpus is 8 runs; nothing about
  planner quality is measurable on it, and ADR 0016 §5 already records that
  prompt changes here are hopes. Not in this change.
- **No ceiling layer moves, no tool is granted, no seam is added.**
  `plannedToolAllowlist`, ADR 0004's layers 1–5 and the four-spawner invariant
  are exactly as they were. The gate reads directory entries and decides whether
  to *stop*.
- **`--verify-cmd`'s behaviour is unchanged** in every respect: same value
  object, same sink attachment, same serialization, same ceiling, same
  disclosure.

## 3. §3.5 — the amendment to ADR 0016 §3, and why the direction carries it

ADR 0016 §3 says detection *"produces PROSE, never policy"* and that the
detection table *"is ALLOWED to be incomplete"* precisely because of that. This
ADR makes detection decide a refusal, which is policy. The amendment must be
stated in the form a future reader can apply:

> **Build-signal detection may gate a refusal. It may never derive a grant.**
> A repository file may cause oh-my-graph to *stop*; it may never cause
> oh-my-graph to *run* something, to widen a tool set, or to attach a command.

The direction is the entire safety argument, and it survives the sharp forms of
the attack ADR 0016 §3 and §4 were written against:

- **A hostile checkout plants a `Makefile`.** Result: `auto` refuses, before any
  spend, naming the file it found. The operator adds one flag. Compare the
  grant version, which ADR 0016 refused: unattended execution of repo-authored
  code under `dontAsk`, leaving no diff.
- **A checkout hides its build system.** Result: no gate; the run proceeds
  unverified — *exactly today's behaviour*. A repo can decline the new
  protection; it cannot use the mechanism to obtain anything.
- **A plan bootstraps its own signal.** ADR 0016 §3's sharpest case: node 1
  legitimately writes a `package.json`, and a per-node detector would then widen
  node 2. It does not apply here, because detection happens **once per
  invocation, before the planner call** — nothing a node writes is ever
  detected, and there is no per-node evaluation to widen.
- **Under `--max-cycles`**, each cycle re-plans against a tree the previous
  cycle wrote, which §3 names as the reason plan-time restriction is not
  sufficient for a *grant*. It is sufficient for this: the gate is evaluated
  once per invocation, before the first planner call, outside the cycle loop. A
  cycle that creates a `go.mod` does not retroactively gate its own run.

**Once-per-invocation is a safety property in this direction and a coverage hole
in the other, and the two must be counted separately.** The three bullets above
are the safety half. The cost is stated in §6 as a named false-negative class:
a goal that *creates* the build system is never gated, because the marker did
not exist when the question was asked.

The monotone property is what makes the incomplete table acceptable in its new
job as well as its old one. A missing ecosystem means a run that is **not**
gated — today's behaviour, no regression — never a run that is gated wrongly
into something.

## 4. Tests the implementation lane owes

Eight, and the third is the one that would otherwise be forgotten.

**Corrected 2026-08-20, after implementation, because the sentence that stood
here was wrong about its own lane's output.** It claimed that each of these
names a temp directory explicitly — the gate's helper takes a `dir` (§2.1) — so
*"none of these needs `t.Chdir` and none of them forecloses `t.Parallel`"*. The
helper does take a `dir`; the **production call site passes `"."`**
(`runAutoWithRuntime`), so every test that goes through `auto`, `--plan-only`,
`chat` or `run` has to *be* in the directory under test, and the shipped ones do
that with `inBuildDir`, which is `t.Chdir`. Only test 8 — which calls
`answerBuildEvidence` directly, and is the one that pins the seam rather than a
message — reaches the `dir` parameter. Harmless as it stands (package `main` has
no `t.Parallel` and already used `t.Chdir` before this lane), and recorded
because a design record that is wrong about what shipped is a bug in one of the
two.

1. **build signal + no flag → refused**, and the message names *both* the
   detected signal (by ecosystem and file) and the opt-out flag. Asserting only
   "it refused" would pass on a bare `exit 1`.
2. **build signal + `--verify-cmd` → proceeds unchanged**, with the same
   attachments and the same disclosure as before this ADR, **and records
   `answer: "attached"` with the detected signals.** The recording half is what
   makes §8(a)'s denominator exist; without it this test passes on the first
   draft's inverted field.
3. **no build signal + no flag → proceeds**, and records
   `answer: "none-detected"`. The negative control, in both halves: without the
   first the gate could widen to "always refuse" and every other test here still
   passes; without the second the greenfield run stays invisible (§6) and §8(a)
   loses its third stratum.
4. **build signal + `--accept-no-build-evidence` → proceeds AND the run records
   it** — `state.json` carries `build_evidence` with `answer: "declared"`, the
   declaring flag and the detected signals, and the plan screen states the
   absence.
5. **`--plan-only` refuses identically to the run it previews** (same message,
   same exit code, no planner call bought), and **`chat` does not refuse** but
   its plan screen states the absence and its run records `answer: "disclosed"`
   with `declared_by: "chat-confirm"` — asserted as `disclosed`, distinct from
   test 4's `declared`, so the two kinds cannot be merged later without a test
   going red (§2.6).
6. **The refusal reaches stdout, unprefixed, all 20 lines** (§2.4) — asserted
   through `mainExitCode` rather than through the subcommand, since the channel
   and the prefix are that function's behaviour, not the error's.
7. **The refusal exits 3**, distinct from a failed run's 1 and a paused run's 2,
   asserted in `exitCodeForError`'s own table beside them.
8. **The gate and the advice line scan the same directory.** One
   `DetectBuildSignals` call feeds both (§2.1); this pins that they cannot
   diverge, which is the failure the parse placement would have made possible
   and untested.

Plus two that fall out of §2.3 and §2.4: `--verify-cmd` together with
`--accept-no-build-evidence` is refused as a contradiction **in
`autoFlags.parse`**, with no directory involved (it is a pure flag-pair test);
and the refusal text names only flags `auto` registers, checked against the real
`FlagSet` the way `TestSnapshotVerifyRefusal_NamesOnlyFlagsResumeRegisters` does
for `resume`.

All of it runs against `FakeRunner`. No test here needs a real spawn: the gate
fires before the planner call, which is the whole point.

### 4.1 Five more, owed after review (2026-08-20)

The eight above cover the **fresh `auto` leg** thoroughly and cover nothing
else, and the first item below is a real defect that shipped through the gap
rather than a coverage wish. All five are in
`cmd/oh-my-graph/buildevidence_test.go`.

9. **`resume` carries the record into the second leg**
   (`TestResume_CarriesTheDeclarationIntoTheSecondLeg`). §2.6's `resume` row
   relies on the snapshot recording the choice; `SnapshotRecorder` rewrites the
   whole snapshot from a base the resumed leg builds field by field, so the
   omitted field was *erased* by the first node that settled. Declared runs that
   paused — the interactive class — silently left all four strata of §8(a).
10. **`chat`'s launch declares its own kind**
    (`TestRunChatWith_TheLaunchItselfDeclaresAChatConfirmDisclosure`). Test 5
    constructs the outcome and hands it to `chatLoop`; the launch line between
    them was unpinned, so `DeclaredByFlag` there would have filed every chat run
    under `declared` — the merge §2.6 and §8(a) forbid — with every test green.
    `runChatWith` is the seam `runAutoWith` already is for `auto`.
11. **Every cycle of a goal loop records the one answer**
    (`TestRunAutoWith_EveryCycleOfAGoalLoopRecordsTheOneAnswer`). Each cycle
    mints its own run id and recorder; a cycle that recorded nothing would drop
    out of §8(a) exactly as a resumed leg did.
12. **Codex meets the same gate**
    (`TestRunAutoWithRuntime_CodexMeetsTheSameGate`). §2.6 *states* this rather
    than deriving it, and a stated property with no case is one a
    runtime-specific early return breaks silently.
13. **The package directory raises no build signal**
    (`TestDetectBuildSignals_ThisPackageDirectoryRaisesNone`). The hazard §2.1
    held against `autoFlags.parse` — a result that depends on the directory the
    test binary runs in — is **inherited** by the chosen site, because its
    production call passes `"."`. A `Makefile` or a stray `go.mod` in
    `cmd/oh-my-graph/` fails dozens of unrelated tests with an unrelated
    message; this converts that cascade into one failure that says what to do.

### 4.2 One more, owed after the final review (2026-08-20)

The `--plan-only` row of the matrix above had two cells and one test. Test 5
covers the preview met by **silence** — the refusal — and nothing covered the
preview met by the **opt-out**, which is the only combination where the two
disclosures can lie, because both are written for the run that normally follows
and a preview mints no run id at all. They did lie, in exactly that cell.

14. **A declared preview promises no record it does not write**
    (`TestRunAutoWith_PlanOnlyDeclaredPromisesNoRecordItDoesNotWrite`). Asserts
    that `auto --plan-only --accept-no-build-evidence` in a build-bearing
    directory proceeds, states the absence with its signal, does **not** print
    "this run's state.json", and leaves no run directory behind. Both halves are
    needed: the text alone would keep passing if the preview started writing
    snapshots, and the directory alone would keep passing while the screen lied
    about it (§2.5b).

## 5. Alternatives considered

- **Keep the notice and make it louder** (colour, a blank line, an
  "ARE YOU SURE" banner). Rejected. The notice is already accurate and already
  early; the run proceeds regardless, and the thing being changed is the control
  flow, not the volume. Cheap to try and the cheapest thing to have to undo
  later, which is the honest argument *for* it — it lost because the defect it
  addresses is that nothing stops.
- **A confirmation prompt** ("no build command detected — type one now"), the
  `chat` shape applied to `auto`. Rejected: `auto` is the unattended path. It
  runs under `dontAsk`, in CI, from cron, and from another agent's `Bash` tool;
  a prompt there hangs a run that has no keyboard. A flag is answerable by all
  of those. `chat` already has the interactive form, which §2.6 uses.
- **Infer the command from the detected signal and run it** — zero config, and
  the detection table already holds the suggestion. Rejected on ADR 0016 §3/§4:
  it makes a repository file the source of an engine-run shell command, which is
  the untrusted-producer invariant, with the added twist that the *first* thing
  the tool would do in a fresh checkout is execute its build. The suggestion
  stays a suggestion.
- **Authorize the printed suggestion in one token** —
  `--accept-suggested-verify-cmd`, or a `[y/N]` on the refusal for the
  interactive case. The operator does not retype `./gradlew build`; they accept
  the string the refusal just showed them. This is the middle option between the
  two the bullets above and below cover, and it is the one that most directly
  moves §9's odds ratio: the whole worry there is that a refused operator types
  the opt-out forever, and this makes the *verified* exit the cheapest thing on
  the screen.

  It is also worth naming the tension it exposes. §2.4's refusal already hands
  the operator a repo-derived command to paste, so "detection may never derive a
  grant" (§3.5) is preserved right now by a **clipboard round-trip** — the human
  reads the string, can edit it, and retypes it as their own. That round-trip is
  thin, and this alternative is the honest test of whether it is load-bearing.

  **Rejected, and not comfortably.** It is load-bearing, for two reasons. First,
  what the round-trip buys is not ceremony but *reading*: the detection table is
  a guess by construction — §2.4 says so out loud in the multi-signal case, and
  §9 records that its priority order is unmeasured in a monorepo — and a one-key
  accept is an authorization given without knowledge of what was authorized.
  When the guess is wrong, the result is worse than no evidence: it is a run
  carrying "evidence" that measured the wrong module, with the tool's own
  fingerprints on the choice, which is §6's `--verify-cmd 'true'` failure
  arriving by the tool's suggestion rather than the user's. Second, §6's
  strongest failure mode is now an **unattended agent** picking the cheapest
  exit; adding an exit cheaper than the opt-out does not fix that, it just
  changes which repo-derived command an unattended process runs without a human
  ever seeing the string — which is precisely the shape ADR 0016 §4 refused.
  Revisit if §8(a) shows the opt-out dominating and §8(b) shows the table's
  suggestions are accurate; the second condition is what this alternative needs
  and does not have.
- **Gate every run, signalled or not.** Rejected — §2.2. Friction with no defect
  behind it, and it discards a distinction the existing notice already makes
  correctly.
- **No opt-out at all: refuse, full stop.** Seriously considered, because a gate
  with an exit is a gate people learn to walk through. Rejected on three counts:
  `auto`'s identity is that it works with no setup; there are legitimate
  unverified runs (a documentation goal in a build-bearing repo, a build that
  exceeds the 10-minute verify ceiling, a repo whose build needs credentials the
  run does not have); and a gate with no exit is worked around out of band — an
  alias, a wrapper, a `touch`-and-`rm` dance — which produces the same unverified
  run with **no record of the choice**. The recorded exit is strictly better
  evidence than a bypass nobody can see.
- **`--no-verify` / `--skip-verification` as the opt-out.** Rejected — §2.3.
  They describe switching off a check that was never going to run.
- **Record the absence in the ledger's verdict column instead of the snapshot.**
  Rejected as a substitute, kept as a complement: ADR 0016 §6's provenance
  qualifier (`self-reported`) already says, per node and after the fact, how each
  verdict was reached. That is a different question from "was the absence
  chosen, and what was the human told when they chose it", which is a property
  of the *run* at *launch*. Both, not either.
- **Emit the choice on the run feed as well.** Not rejected, deferred. The feed
  is a consumer contract with its own additive discipline (`docs/RUN-FEED.md`),
  and the reader this ADR is written for reads a finished run, which means the
  snapshot. One additive field on `run_started` is the obvious follow-up if a
  feed consumer asks.
- **Put the rule in a user-level config** (`$OMG_HOME/config.yaml`, ADR 0016's
  alternative B′) so an operator can set their own default. Deferred for the
  same reason B′ was: it buys a config format, a merge story and a precedence
  story before anyone has typed the flag twice. Note that the direction matters
  here — a config that *lowers* the gate is exactly the alias problem in a file,
  and if this is ever built the recorded field must record that source too.
- **Have the planner declare the build command, validated against a pattern.**
  Rejected on ADR 0016 §4's invariant. A regex standing where a trust boundary
  belongs.

## 6. Failure modes

- **The opt-out becomes muscle memory — and the realistic aliaser is an agent,
  not a shell alias.** The human version is familiar: an operator aliases `auto`
  to always pass `--accept-no-build-evidence`, and this ADR has bought one screen
  of prose and a longer command line. But this repository ships
  `Bash(oh-my-graph auto *)` to a Claude Code agent
  (`plugin/agents/oh-my-graph.md`, `plugin/commands/graph.md`,
  `plugin/README.md`), and an LLM that meets a refusal naming two exits will take
  the one it can satisfy **without knowing the repository's build command** —
  the opt-out — very close to every time. That driver is stronger than any human
  habit, it is unattended by construction, and it is the shipped invocation, not
  a hypothetical one.

  So the plugin's invocation gets a **considered answer rather than a discovered
  one**, and it is documentation, because the tool has no way to tell an agent's
  argv from a human's: `plugin/agents/oh-my-graph.md` gains an explicit rule that
  on a build-evidence refusal the agent **surfaces the refusal to the human and
  asks which exit**, and never passes `--accept-no-build-evidence` on its own
  initiative — the flag says *a human accepts this*, and an agent typing it is a
  false statement in the snapshot. `plugin/commands/graph.md` gains the same
  sentence where it documents `auto`. See §7 for the full list of surfaces.

  What that rule cannot do is enforce itself, and §9 records the consequence:
  `declared_by: "--accept-no-build-evidence"` does not distinguish a human's
  keystroke from an agent's argv, so §8(a)'s declared rows are an upper bound on
  human declarations, not a count of them.

  What §2.5 buys against all three versions is that the habit is **visible and
  countable** — every such run carries `build_evidence` in its snapshot with the
  signals it ignored, which is a measurement the notice never permitted. That is
  the honest claim: this converts an invisible default into a visible habit.
- **The greenfield run is exempt, and it is the highest-risk unverified run
  there is.** `auto "scaffold a new Go service"` in an empty directory raises no
  signal, so it is not gated. Nothing about that is an oversight of the safety
  argument in §3 — it is the same once-per-invocation evaluation, read from the
  other side — but it is a coverage hole and this ADR states it rather than
  leaving it as a side effect: **a goal whose whole purpose is to create the
  build system is never gated, and the build it creates is never run.** Under
  `--max-cycles N` it compounds: cycle 1 writes the build system, and cycles
  2..N run against a build that now exists and is still never executed, because
  the question was asked once, before cycle 1.

  Accepted, with two things carrying it. First, re-detecting per cycle is not a
  free fix: it would make a file a *node wrote* decide policy, which is the
  bootstrapping shape §3 spends four bullets keeping out, and the amendment's
  refusal-only direction makes it survivable but not obviously worth the
  complication before anyone has hit it. Second — and this is new since the
  first draft — the greenfield run is no longer *invisible*: §2.5's widened
  recording files it as `answer: "none-detected"`, so §8(a) can count how large
  this class actually is instead of arguing about it. If it turns out to be the
  common shape of an `auto` run, that is the evidence for re-detecting at cycle
  boundaries, and it will be a number rather than this paragraph.
- **`chat` asks once per SESSION, and a session outlives a launch.** The
  question is put at REPL startup (`runChatWith`) and its answer serves every
  graph turn until the process ends, which is the same once-per-invocation rule
  `auto` follows and is required by the same argument (§3.5: re-asking would let
  a file a *turn wrote* gate a later turn — the bootstrapping shape, one scope
  up). The cost is that chat's staleness window is materially larger than the
  greenfield case above it: a session whose turn 1 scaffolds a `package.json`
  still records `none-detected` on turn 9, and an `auto` launch at least ends.
  The direction is the safe one — a stale answer never widens anything, it only
  fails to gate — so this is accepted, stated in `chat.go` at the call site, and
  countable: those rows are `none-detected` with an empty `signals` list, so if
  the class matters it will show up in §8(a) rather than in an argument.
- **A false positive.** A `Makefile` that only builds documentation, a
  `package.json` with no test script, a `go.mod` in a repo whose goal is a
  README edit. The gate refuses a run that genuinely had nothing to verify. Cost:
  one flag, one time, before any spend. This is the friction the decision
  accepts, and its size is the detection table's accuracy.
- **A false negative.** A private wrapper (`bin/verify-everything`), a build
  system not in the table, or a monorepo whose build files live in
  subdirectories — `DetectBuildSignals` reads the invocation directory only, not
  recursively. No gate fires; the run proceeds unverified exactly as today. The
  gate is exactly as complete as the table, and the table is allowed to be
  incomplete because incompleteness fails **open** (§3). One more member of this
  class comes from the table's shape rather than its contents: `*.csproj` is the
  single marker matched as a **pattern** (`filepath.Glob`) rather than by
  `os.Stat`, so an invocation directory whose own path contains `[`, `?` or `*`
  produces a valid-but-wrong pattern that matches nothing, silently — `Glob`
  errors only on a malformed pattern, never on a well-formed one that finds
  nothing. A .NET repository under such a path loses its only signal. Same
  direction as the rest of the bullet, so it is recorded rather than fixed. The
  created-during-the-run case above is a different and larger class than these
  and is listed separately for that reason: those are gaps in the *table*,
  and it is a gap in the *timing*, which no table entry closes.
- **A build too slow for the ceiling.** `--verify-cmd` is bounded by 10 minutes,
  and a build that exceeds it fails as an Infrastructure fault ("could not
  verify"). The operator's route is the opt-out, and the refusal offers it. The
  ceiling itself is ADR 0016's and is not revisited here.
- **The refusal is the first thing a new user meets — and the shipped quickstart
  is itself an instance of the false positive above.** `README.md` and
  `README.ko.md` tell a new user to run
  `auto "lint this repo and summarize the findings" --input repo=$PWD` in their
  own repository: a **read-only analysis goal in a build-bearing directory**,
  which is the exact class the bullet above accepts friction for. So the first
  thing a new user meets is not merely "a refusal" in the abstract; it is a
  refusal of the command the README just gave them, for a goal that genuinely
  had nothing to build. That is worse than the first draft of this ADR noticed.

  The fix is in the documentation, not the gate, and §7 lists every file: the
  read-only quickstart gains `--accept-no-build-evidence` **with one sentence
  saying why** ("this goal reads the repo and writes a summary; there is nothing
  to build, and the flag states that"), while the implementation-shaped examples
  gain `--verify-cmd`. Both halves matter — a quickstart that only ever shows
  the opt-out teaches the escape as the normal answer, which is the failure mode
  two bullets up, and one that shows neither is a command that no longer works.
  The residual cost stays accepted for the same reason as before: the run that
  starts unverified is the one that produced #119. §8(a) is what will say
  whether the teaching order took.

  **What that leaves, named rather than inherited:** the very first `auto` a new
  user copies now carries `--accept-no-build-evidence`, so the paste-the-flag
  habit lands on the human this whole ADR is written to protect. Three things
  carry the trade, and it is a trade rather than a fix. The goal genuinely has
  nothing to build and the sentence beside it says so, which is the difference
  between teaching an exit and teaching a lie. The implementation-shaped
  `--verify-cmd` example follows immediately, so the flag is never the last word
  a reader sees (`README.md`, `README.ko.md`, `docs/EXAMPLES.md`). And the
  agent-facing surfaces forbid taking that exit unilaterally
  (`plugin/agents/oh-my-graph.md`), which is where the aliaser bullet says the
  realistic version of this failure actually lives. If §8(a)'s `declared` rows
  arrive dominated by first runs, the answer is to reorder the quickstart so
  `--verify-cmd` is the first thing shown, not to remove the flag from a command
  that would otherwise be refused.
- **A run gated by a hostile checkout.** A repository can plant a marker to make
  `auto` refuse in it. Cost: one flag. See §3 — the direction is what makes this
  a nuisance rather than a hole.
- **`verified` is still not `correct`.** Unchanged from ADR 0016: a run that
  passes the gate by supplying `--verify-cmd 'true'` carries "evidence" that
  measures nothing, and a node holding `Edit` can still edit the file the
  command runs. This ADR moves the default; it makes no new claim about
  adequacy.

## 7. Compatibility

- **This is a behaviour change to the headline command, and the CHANGELOG entry
  under `## [Unreleased]` must say so plainly:** `auto` now **refuses to start**
  in a directory where a build system is detected unless `--verify-cmd` or
  `--accept-no-build-evidence` is passed. An invocation that ran yesterday can
  exit 3 today. No release and no tag in this lane.
- **Scripts and CI calling `auto` break loudly** — **exit 3** (§2.4), before any
  spend, with the two flags named on stdout — rather than silently continuing to
  produce unverified runs. That is the intended direction of the break, and the
  distinct code is what lets a script tell "add a flag" from "the build failed".
  1 and 2 keep their current meanings exactly.
- **`run`, `lint`, `serve`, `watch`, `show` and every shipped graph are
  untouched.** So is `resume`, including every already-paused run (§2.6).
- **Snapshot: one additive optional field, schema stays 3.** Every existing
  `state.json` stays readable and unchanged in meaning; every consumer that does
  not know the field ignores it. It is now written on every auto-mode launch
  (§2.5a), which is more rows than the first draft, but not a different shape.
- **Run feed: no change, no schema bump** (§5, deferred).
- **`internal/coordinator` gains an exported predicate and error type**
  (`MissingBuildEvidenceError`), and **`VerifyAdvice` gains a parameter** for the
  declaration (§2.3). That is an exported signature change, deliberately: the
  first draft avoided one by putting the opt-out field on `VerifyCommand`, which
  bought signature stability in a package with **no consumers outside this
  module** at the price of a field `resume` can never set. `VerifyCommand` is
  therefore unchanged — no new field, and `Validate` keeps exactly its blank,
  timeout and ceiling refusals.
- **The documented invocations that now refuse.** Every place this repository
  tells a user to type `auto` in a build-bearing directory must be updated in the
  same lane as the gate, or the repo ships instructions the tool rejects — #198's
  defect, from the documentation side:
  - `README.md` (quickstart) and `README.ko.md` (quickstart) — the read-only
    goal; gains `--accept-no-build-evidence` plus the one-sentence why (§6).
  - `docs/EXAMPLES.md`: the `auto` row of the subcommand table, which today
    describes `--verify-cmd` as purely optional; the read-only quickstart repeat;
    and the implementation-shaped example, which gains `--verify-cmd`. The one
    example that already passes `--verify-cmd` is correct as it stands and is the
    model for the others.
  - `plugin/README.md` (both the `/graph auto` and bare-`auto` forms),
    `plugin/agents/oh-my-graph.md` (its synopsis lists no `--verify-cmd` at all,
    and it is the file that gains §6's rule against an agent declaring on a
    human's behalf), and `plugin/commands/graph.md`.
  - `usageLines` in `cmd/oh-my-graph/main.go` must gain the flag on the `auto`
    line — and this one fails loudly rather than silently, because
    `usage_test.go` pins each synopsis line's flags against that subcommand's
    real `FlagSet`. Named here anyway so the lane does not learn it from a red
    test.
- **DESIGN.md is the spec and drift in it is a bug** (CLAUDE.md). The auto-mode
  section must gain the gate: today it describes `--verify-cmd` as purely
  optional, which stops being true for a build-bearing directory.
- **ADR 0016 §3 carries a dated pointer here**, in the same style ADR 0004
  carries one to ADR 0016 — its "never policy" sentence is now "never a grant".
- **SECURITY.md** needs one sentence: repository content can now cause the tool
  to refuse to start, and cannot cause it to run, grant or attach anything. A
  reader auditing the trust boundary should not have to derive that from an ADR.

## 8. Required measurements before Accepted

Neither gates correctness of the mechanism; both gate the claims made for it.
Record each with cost and CLI version, as every prior E-number is.

- **(a) The firing rate and the exits taken.** Over the first N `auto`
  invocations **that produced a run directory** after this ships, counted from
  the snapshots themselves — one row per such launch, four mutually exclusive
  strata, summing to N:

  | stratum | `build_evidence.answer` | what it answers |
  | --- | --- | --- |
  | attached | `attached` | the run carries engine-run evidence |
  | declared | `declared` | a signal was met and a flag was typed at it |
  | disclosed | `disclosed` | a signal was met and a chat `[y/N]` passed over it |
  | none detected | `none-detected` | no signal — including every greenfield run (§6) |

  The **denominator is the point**: "how many directories raised a signal" is
  simply the rows whose `signals` list is non-empty, counted across all four
  strata — including the `attached` ones, which is why `signals` is recorded even
  when a command was supplied. It is recoverable only because §2.5 writes the
  field on every launch. The first
  draft recorded stratum 2 alone, which would have left the firing rate exactly
  as unknowable after shipping as it is today — the same blind spot the
  measurement file names when it says the current corpus cannot say which of its
  7 unverified runs would have been gated (no snapshot records an invocation
  directory). The baseline is 1 of 8, with its address in
  `docs/measurements/0030-…`. Report `declared` and `disclosed` separately and
  never summed (§2.6), and read the `declared` count as an **upper bound on
  human declarations** — an agent's argv is indistinguishable from a human's
  keystroke in that column (§6, §9).

  **N is not "launches"; it is launches that got a run directory**, and the two
  classes it excludes both matter to this section's own headline. A **refused**
  invocation writes nothing anywhere — no run directory, no feed event, no
  snapshot, by design (§2.2, `TestRunAutoWith_RefusesABuildBearingDirectoryWithNoEvidenceCommand`)
  — and a **`--plan-only`** one writes no `state.json` at all (§2.5b). So an
  operator who was refused and then *overrode* leaves a `declared` row, while one
  who was refused and *walked away* leaves nothing: the firing rate readable from
  snapshots is a **floor**, not the rate. Getting the true rate needs a surface
  this ADR does not add (§9). Do not report the four strata as the denominator of
  refusals.
- **(b) The false-positive rate of the table.** Over this machine's repositories,
  how many directories that raise a signal have no build command worth running
  for a typical goal. If it is high, the friction estimate in §6 is wrong and the
  table needs narrowing, not the gate.

## 9. What could not be determined

- **Whether the gate changes outcomes or only paperwork.** An operator refused
  once may type the real build command, or may type the opt-out forever. Nothing
  in the corpus predicts which, and (a) is the only thing that will tell us.
- **How often the gate fires, as opposed to how often it fires and is then
  answered.** A refusal leaves no artifact — that is the point of refusing before
  a run directory exists (§2.2) — so an abandoned refusal is invisible to §8(a),
  which counts only launches that got a run directory. The `declared` and
  `disclosed` strata therefore measure the **answered** firings and put a floor
  under the real rate; nothing here measures the numerator. Closing it means
  writing something on the refusal path, which would undo the property ADR 0023
  §2.6 depends on (a refused invocation is not a run and gets no status), so it
  is not a small follow-up. Named here rather than left for a future reader to
  find by dividing by the wrong N.
- **How many of the 7 unverified runs would have been gated.** Unknowable: no
  snapshot and no feed event records the invocation directory. Stated in the
  measurement file, not inferred around.
- **Whether notices are weak in general.** The 42%→33% figure has no address in
  this repository (§1.1) and is carried as reported. The argument here does not
  need it: the reason to prefer a gate is that a notice cannot stop a run, which
  is a property of the mechanism, not a finding about a corpus.
- **Whether `chat`'s single `y` is an adequate disclosure** (§2.6). It is the
  strongest thing that surface has today; whether it is enough is what the
  `disclosed` stratum will show — kept apart from `declared` at the source
  precisely so the question stays askable.
- **Whether a declaration was a human's.** `declared_by:
  "--accept-no-build-evidence"` records the flag, and the flag records that
  *something* typed it. This repository ships `auto` to an agent (§6), and the
  tool cannot tell that agent's argv from a keystroke — nothing in argv, the
  environment or the snapshot distinguishes them, and inventing a
  `--declared-by` value the caller supplies would just move the honesty problem
  one flag along. So the `declared` stratum is an upper bound on human
  declarations. The plugin rule in §6 is documentation, and this is the price of
  its being documentation.
- **How large the greenfield class is** (§6). Not knowable from the current
  corpus for the same reason as the row above it — no snapshot records the
  invocation directory — and knowable after this ships, from the
  `none-detected` stratum. It is the input to whether per-cycle re-detection is
  worth its complication.
- **Whether the detection table's priority order suggests the right command in a
  monorepo.** Unchanged from ADR 0016, unmeasured then and now. The refusal says
  "so the command below is a guess" rather than pretending otherwise.
