# ADR 0030 — An unverified `auto` run is a choice, not a default

- Status: **Proposed — decision taken, nothing implemented.** This record is the
  design; no code in this commit. The implementation lane owes the refusal, the
  flag, the snapshot field, the two disclosure sites, the five pinned tests and
  the `## [Unreleased]` CHANGELOG entry named under Compatibility.
- Date: 2026-08-20
- Measurement: [`docs/measurements/0030-auto-runs-carry-no-build-evidence.md`](../measurements/0030-auto-runs-carry-no-build-evidence.md),
  re-derivable with
  `python3 docs/measurements/probes/0030-auto-build-evidence/count.py`.
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
in §2.4 and exits non-zero.

The refusal happens **at flag parse** — in `autoFlags.parse`
(`cmd/oh-my-graph/flags.go`), on the line after the existing
`checkVerifyFlags("auto", …)` call, where `--max-cycles`, `--max-goal-budget-usd`
and `--plan-only` are already validated for the same reason. That places it
before the planner call and therefore before any spend, which is load-bearing
twice over: a refusal that cost a planner call would be a worse version of the
notice, and this placement is what makes `--plan-only` refuse identically
(§2.6) without special-casing.

Deliberately **not** inside `checkVerifyFlags`, which `resumeFlags.parse` also
calls: that helper exists so the two subcommands cannot diverge on the flag
pair, and the gate is `auto`-only (§2.6). Sharing it would gate `resume` by
accident — the one change here that would strand a paused run.

The predicate itself is a pure function in `internal/coordinator/verifycmd.go`,
beside `VerifyAdvice` and `DetectBuildSignals`, returning a typed
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
working as designed; a flag that is easy to alias into a shell function is the
failure mode (§6), and no name prevents that — only the recording in §2.5 makes
it visible.

**With `--verify-cmd` it is a contradiction and is refused** — in
`VerifyCommand.Validate`, beside the blank-command and timeout-ceiling
refusals, so the value object stays the one place the pair's rules live. The
operator would be declaring an absence and supplying the thing whose absence
they declared; refusing beats picking a winner, because either winner silently
discards something they typed. (`resume` registers no opt-out flag, so the new
branch is unreachable from there and its refusals are unchanged.)

**With no build signal it is accepted and inert.** A script that always passes
it must not break when run in a directory with no build system. It records
nothing in that case (§2.5) — there was nothing to decide.

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

### 2.5 The run records the choice, in two places a reader meets

An absence that was chosen and an absence that was an accident look identical
in a finished run today. After this they do not.

**(a) The snapshot** — `internal/runstate`, an additive optional field:

```go
// NoBuildEvidence records that this run was launched with no engine-run build
// evidence in a directory where a build system was detected, and that a human
// said so. Absent means either that nothing was detected (nothing was decided)
// or that the run carries evidence — never that the question went unasked.
NoBuildEvidence *BuildEvidenceAbsence `json:"no_build_evidence,omitempty"`

type BuildEvidenceAbsence struct {
	// DeclaredBy is how the absence was stated: the flag spelling
	// ("--accept-no-build-evidence") for auto, or "chat-confirm" for a chat
	// graph turn whose [y/N] answered a plan screen that stated it (§2.6).
	DeclaredBy string `json:"declared_by"`
	// Signals are the marker files detected at launch, in the detection
	// table's order — what the human was told when they decided.
	Signals []string `json:"signals,omitempty"`
}
```

Written exactly when: no verification was attached, at least one signal was
detected, and the run proceeded by declaration. **Schema stays 3** — an absent
field is a run that predates this or a run with nothing to decide, and no
reader of either version can misread it (the same reasoning ADR 0025 used for
`runtime`, reaching the opposite conclusion on `omitempty` because here absence
is legitimately meaningful).

**What this field is NOT:** an input to anything. Nothing reads it to decide
behaviour, on this leg or on a resumed one. In particular it is not ADR 0016
§4's rejected mechanism (ii) wearing a new hat: it carries **no command**, only
the fact of an absence, so there is nothing in it a later leg could execute. The
run directory remains an inadmissible source of engine-run shell, on both legs.

**(b) The printed disclosure**, at both sites where a human meets the run:

- **before the planner call**, `VerifyAdvice` gains a variant for the declared
  case — the same paragraph it prints today plus the sentence *"You said so with
  `--accept-no-build-evidence`; this run's `state.json` records it."* The
  un-signalled case's text is unchanged;
- **with the plan**, in the slot `noteVerifyAttachments` occupies
  (`printPlan`, `cmd/oh-my-graph/main.go`). That slot states either what the
  engine will run at each sink, or that nothing will — never neither. This is
  the screen `--plan-only` prints and the screen chat's `[y/N]` gates, which is
  what makes §2.6's chat answer work at all.

### 2.6 Which surfaces the gate applies to, pinned

| surface | gated? | why |
| --- | --- | --- |
| `auto` | **yes** | The defect's home. |
| `auto --plan-only` | **yes**, identically | A preview that refuses differently from the run it previews is its own defect. It falls out for free: the gate is at flag validation, which `--plan-only` passes through before it buys its planner call. It also *saves* the user money in the refused case, where today they would pay for a plan they then have to re-request with a flag. |
| `chat` | **no refusal**; disclosure and recording only | `chat` registers no verification flags at all, so a refusal there could only name a flag `chat` rejects — the exact dead end #198 was. What `chat` gets instead is the absence stated on the plan screen its `[y/N]` gates (§2.5b), and the run recorded with `declared_by: "chat-confirm"`. A human answering `y` to a screen that says the run carries no build evidence is the strongest declaration that surface has. |
| `run` | **no** | A hand-written graph carries its author's own `success_check.verify` and is a reviewed artifact. Out of scope by construction, as ADR 0016 §2 has it. |
| `resume` | **no** | The gate is a **launch-time** gate. The choice was made once, and the snapshot the resume loads records it. Re-asking would make a paused run un-resumable without re-typing a declaration already on file, which is ADR 0009's promise broken and #198's defect repeated. A run launched before this ADR resumes untouched. |

**Chat's answer is the weakest thing in this record, and it is named rather than
enjoyed.** One `y` covers two questions — *run this plan* and *accept that it
proves nothing* — where `auto`'s operator answers the second one separately.
The alternatives were a dead-end refusal (worse: it strands the user), a second
confirm prompt (a second `[y/N]` for one action, bought before anyone has
complained), or giving `chat` the full flag pair (a larger change than this ADR,
and ADR 0016 §2 already carries `chat --verify-cmd` as unshipped work). If the
recorded field later shows chat turns dominating the declared-absence rows,
that is the evidence for revisiting.

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
  node 2. It does not apply here, because detection happens **once, at flag
  validation, before the planner call** — nothing a node writes is ever
  detected, and there is no per-node evaluation to widen.
- **Under `--max-cycles`**, each cycle re-plans against a tree the previous
  cycle wrote, which §3 names as the reason plan-time restriction is not
  sufficient for a *grant*. It is sufficient for this: the gate is evaluated
  once per invocation, at flag validation, outside the cycle loop. A cycle that
  creates a `go.mod` does not retroactively gate its own run.

The monotone property is what makes the incomplete table acceptable in its new
job as well as its old one. A missing ecosystem means a run that is **not**
gated — today's behaviour, no regression — never a run that is gated wrongly
into something.

## 4. Tests the implementation lane owes

Five, and the third is the one that would otherwise be forgotten:

1. **build signal + no flag → refused**, and the message names *both* the
   detected signal (by ecosystem and file) and the opt-out flag. Asserting only
   "it refused" would pass on a bare `exit 1`.
2. **build signal + `--verify-cmd` → proceeds unchanged**, with the same
   attachments and the same disclosure as before this ADR.
3. **no build signal + no flag → proceeds.** The negative control. Without it
   the gate could widen to "always refuse" and every other test here still
   passes.
4. **build signal + `--accept-no-build-evidence` → proceeds AND the run records
   it** — `state.json` carries `no_build_evidence` with the declaring flag and
   the detected signals, and the plan screen states the absence.
5. **`--plan-only` refuses identically to the run it previews** (same message,
   same exit, no planner call bought), and **`chat` does not refuse** but its
   plan screen states the absence and its run records
   `declared_by: "chat-confirm"`.

Plus two that fall out of §2.3 and §2.4: `--verify-cmd` together with
`--accept-no-build-evidence` is refused as a contradiction; and the refusal text
names only flags `auto` registers, checked against the real `FlagSet` the way
`TestSnapshotVerifyRefusal_NamesOnlyFlagsResumeRegisters` does for `resume`.

All of it runs against `FakeRunner`. No test here needs a real spawn: the gate
fires before the planner call, which is the whole point.

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

- **The opt-out becomes muscle memory.** The realistic failure: an operator
  aliases `auto` to always pass `--accept-no-build-evidence`, and this ADR has
  bought one screen of prose and a longer command line. Nothing prevents it, and
  no flag name prevents it. What §2.5 buys is that the habit is **visible and
  countable** — every such run carries `no_build_evidence` in its snapshot with
  the signals it ignored, which is a measurement the notice never permitted.
  That is the honest claim: this converts an invisible default into a visible
  habit.
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
  incomplete because incompleteness fails **open** (§3).
- **A build too slow for the ceiling.** `--verify-cmd` is bounded by 10 minutes,
  and a build that exceeds it fails as an Infrastructure fault ("could not
  verify"). The operator's route is the opt-out, and the refusal offers it. The
  ceiling itself is ADR 0016's and is not revisited here.
- **The refusal is the first thing a new user meets.** `auto "…"` in their own
  repository, and the tool declines. The text in §2.4 is written for exactly
  that reader — it explains why, offers both exits, and says nothing was
  charged — but it is still a worse first impression than a run that starts.
  Accepted deliberately: the run that starts is the one that produced #119.
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
  exit non-zero today. No release and no tag in this lane.
- **Scripts and CI calling `auto` break loudly** — non-zero exit, before any
  spend, with the two flags named — rather than silently continuing to produce
  unverified runs. That is the intended direction of the break.
- **`run`, `lint`, `serve`, `watch`, `show` and every shipped graph are
  untouched.** So is `resume`, including every already-paused run (§2.6).
- **Snapshot: one additive optional field, schema stays 3.** Every existing
  `state.json` stays readable and unchanged in meaning; every consumer that does
  not know the field ignores it.
- **Run feed: no change, no schema bump** (§5, deferred).
- **`internal/coordinator` gains an exported predicate and error type**
  (`MissingBuildEvidenceError`). No existing exported signature changes;
  `VerifyCommand` gains one field for the opt-out, and `Validate` gains the
  contradiction refusal, so its blank/ceiling refusals are untouched.
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
  invocations after this ships, from the snapshots themselves: how many
  directories raised a signal, how many runs answered with `--verify-cmd`, and
  how many with `--accept-no-build-evidence`. The baseline is 1 of 8, with its
  address in `docs/measurements/0030-…`. This is the measurement the recorded
  field exists to make possible, and it cannot be taken before the field exists
  — the current corpus cannot even say which of its 7 unverified runs would have
  been gated (no snapshot records an invocation directory).
- **(b) The false-positive rate of the table.** Over this machine's repositories,
  how many directories that raise a signal have no build command worth running
  for a typical goal. If it is high, the friction estimate in §6 is wrong and the
  table needs narrowing, not the gate.

## 9. What could not be determined

- **Whether the gate changes outcomes or only paperwork.** An operator refused
  once may type the real build command, or may type the opt-out forever. Nothing
  in the corpus predicts which, and (a) is the only thing that will tell us.
- **How many of the 7 unverified runs would have been gated.** Unknowable: no
  snapshot and no feed event records the invocation directory. Stated in the
  measurement file, not inferred around.
- **Whether notices are weak in general.** The 42%→33% figure has no address in
  this repository (§1.1) and is carried as reported. The argument here does not
  need it: the reason to prefer a gate is that a notice cannot stop a run, which
  is a property of the mechanism, not a finding about a corpus.
- **Whether `chat`'s single `y` is an adequate declaration** (§2.6). It is the
  strongest one that surface has today; whether it is enough is exactly what the
  `declared_by` column will show.
- **Whether the detection table's priority order suggests the right command in a
  monorepo.** Unchanged from ADR 0016, unmeasured then and now. The refusal says
  "so the command below is a guess" rather than pretending otherwise.
