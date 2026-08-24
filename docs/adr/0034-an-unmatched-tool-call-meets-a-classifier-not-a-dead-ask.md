# ADR 0034 — An unmatched tool call meets a classifier, not a dead ask

**Status:** Proposed. Unlike ADR 0032, the code shipped before this record: the
default moved in `8a953ff` and the resume half in `3e6d030`, both on branch
`permission-mode-auto-default`. So the addresses below point at code that
exists, and §7's falsification is the only thing owed before Accepted. §6
carries the number that would revert it.

**Where the addresses point.** Written against `3e6d030`. Every `file:line`
resolves there. The line numbers in the evidence brief this record was written
from are one commit stale for `internal/coordinator/coordinator.go` — the two
implementation commits moved that file — and were re-read rather than carried.

**Where the numbers come from.** Two kinds, and they are not interchangeable:

- **Corpus counts**, cited by line to
  `docs/measurements/0213b-compound-commands-defeat-grants.md` and
  `docs/measurements/0218-denied-nodes-that-passed.md`. Both are readings of
  `~/.oh-my-graph/runs` on this machine, both cost zero spawns, and both carry
  caveats that §1.4 restates as prohibitions rather than as footnotes.
- **On-disk strings** from the shipped CLI, cited as `0034-strings §X` to
  `docs/measurements/0034-what-auto-mode-does-on-disk.md`, which pins them with
  the binary's SHA-256 and the `strings` command that produced them. **No
  `claude` was run for this record.** Nothing below is an observation of a
  running node under `auto`, and §8 says so once more where it matters.

**Date:** 2026-08-24

## 1. Context

### 1.1 What the default was, and where it lives

One constant decides what a node declaring no `permission_mode` runs under.
Before `8a953ff` it read `graph.PermissionDontAsk`; it now reads
`graph.PermissionAuto` (`internal/schedule/scheduler.go:66`). It is spelled by
`internal/graph`'s constant rather than a literal, so the default can never be a
mode the load-time validator would refuse a graph for.

It is resolved in exactly one place — `internal/schedule/scheduler.go:1357-1360`,
where a node's declared mode wins and an empty one falls back — and that place
serves **both** a hand-written `run` and a planned `auto`. §2.4 is about whether
it should.

The value is passed through verbatim as `claude --permission-mode <mode>`
(`internal/runner/claude_protocol.go:34`). oh-my-graph neither interprets it nor
translates it; the mode's meaning is the CLI's.

### 1.2 What `auto` is, in the CLI's own words

One string in `claude` 2.1.241 enumerates six modes at once, which is what makes
this a reading rather than an inference (`0034-strings §A`, verbatim):

> `'dontAsk' - Don't prompt for permissions, deny if not pre-approved. 'auto' -
> Use a model classifier to approve/deny permission prompts.`

**The whole of the difference is one path.** `dontAsk` denies whatever is not
pre-approved. `auto` puts whatever is not pre-approved to a model classifier,
which approves **or denies** it. `auto` adds an allow path `dontAsk` lacks; it
does not remove a deny path.

### 1.3 The load-bearing fact: a headless node is DENIED, not blocked

Every node is a `claude -p` subprocess with no terminal. If `auto` resolved an
unapproved call by waiting for a human, every such call would become a node
timeout, and this decision would be indefensible rather than debatable. Three
independent strings rule that out (`0034-strings §B`):

| address | string | what it rules out |
| --- | --- | --- |
| `0034-strings §B`, L171823 | `Action requires interactive approval and permission prompts are not available in this context` | The CLI carries a **denial reason** for exactly "approval needed, no prompt available". Code that waited would not need the sentence. |
| `0034-strings §B`, L171831 | `Agent aborted: too many classifier denials in headless mode` | The classifier **runs headless** and its denials **accumulate**. A denial that blocked could not be counted. |
| `0034-strings §B`, L171832 | `Classifier denial limit exceeded, falling back to prompting: ` | The prompt fallback is the **non-headless branch**; `headless` and `consecutive` sit adjacent (L171830, L171829) with the headless side aborting. |

**VERDICT (non-TTY): DENIES.** An unapproved call returns a refusal the node
reads, and the node continues — the same shape `dontAsk` produces today.

This verdict is the one fact this whole record rests on, and it is a reading of
bytes, not an observation of a run. §8 keeps it labelled as such.

### 1.4 What the two corpus measurements are, and what they forbid

| caveat | address | what it forbids here |
| --- | --- | --- |
| **No rate, ever.** 53 of 73 is 72.6% and *"that percentage should not be written down or quoted."* The unit of independence is the run: 6 of 18 runs are 100% denied and 6 are 0% denied, so *"the effective n is nearer 18 than 73."* | `0218:349-365` | No percentage of nodes appears in this ADR, and §6's threshold is a raw count over a named denominator, derived from the run as the unit. |
| **Denial has no reason code.** *"It is byte-identical for a compound call, a simple out-of-scope call and a sandbox refusal. Every class here is a correlation between command shape and denial, never a cause."* | `0213b:308-312` | §2.1 may not claim the classifier *will* approve class (A) and (C); only that those are the calls whose denial is not explained by the node reaching outside its grant. |
| **The matching rule is unimplemented here.** *"How Claude Code actually applies `Bash(go *)` lives in the Claude Code binary. **No source in this repository implements it.**"* | `0213b:286-287` | Every claim about what a grant matches is the CLI's behaviour, not this repository's, and can change under it. |
| **The (A)-class first-word tally counts occurrences, not calls.** 124 of 155 is *"the ceiling on it, not the value."* | `0213b:209-214` | The 84 in §2.1 is a count of denied calls, and is never converted into a count of calls a classifier would retire. |
| **The corpus's `claude` version is unknown.** No run record holds it. | `0213b:322-325` | The corpus may be two populations. §6's re-measurement therefore selects on a structural field this branch added, not on prose. |
| **It is a self-measurement**, of one operator's habits, in `dontAsk`, with this planner's `allowed_tools` conventions. | `0218:35-50` | Nothing here generalises past "these runs, made this way".|

### 1.5 The cost of `dontAsk`, as measured — and what it does NOT show

`docs/measurements/0213b-compound-commands-defeat-grants.md` is the measurement
that bears on this decision. Over 22 planned runs, 90 nodes with a parsed
transcript and 1368 `Bash` tool-use blocks, **246 Bash calls were denied inside a
planned node** (`0213b:132-142`). Their classification (`0213b:170-174`):

| class | count | what it is |
| --- | --- | --- |
| **(A)** | **64** | first sub-command granted, a later one not — the compound shape |
| **(C)** | **20** | every sub-command granted, denied anyway; **17 of the 20 are not compound at all** (`0213b:233`) |
| **(B)** | **162** | out of scope from the command's own first word |
| (D), (E) | 0, 0 | unsplittable / grant not recoverable |

**84 of 246 — (A) plus (C) — are calls where the node was not reaching outside
what it had been granted**, as far as the record can distinguish. It reached for
something it held and lost the call to a pipe, an appended `echo`, an argument
shape or a path outside the working directory (`0213b:234-236`: of the 20,
**8** reach outside cwd, **12** have no path arguments at all, **0** are wholly
inside). The occasion for that measurement was one `measure` node that spent
**$34.70 and produced no artifact** (`0213b:19-20`).

**`docs/measurements/0218-denied-nodes-that-passed.md` does not support this
change, and must not be cited as though it did.** Its headline is that 53 of 73
readable planned nodes were denied a call, 49 of those recorded `PASS`, and 44 of
the 49 held no engine-run check (`0218:3-7`, `:249-256`). Its own reading of that
(`0218:448-453`, verbatim):

> The exposure the numbers show is not really "denial". … only **8 of the 73**
> nodes had *any* engine-run check, denied or not. The denied-and-passed nodes
> did not pass because they were denied; they passed because nothing but their
> own word was ever asked of them.

Its recommendation is `verify` coverage (`0218:428-429`), not a permission mode.
`auto` will convert some denials into approvals; it will not make the engine
check anything. **The 44 is the cost of missing verification and is ADR 0033's
subject, not this one's.** 0218 enters this record for one other reason, in §6.

### 1.6 The five layers, before this change

`toolPolicyFor` (`internal/coordinator/coordinator.go:765`) assembles one planned
node's whole ceiling; its comment carries the layer table at `:711-715`. The
documented forms are `SECURITY.md:221-228` and `DESIGN.md:2426-2433`.

Layer 2's binding force was documented as resting on `dontAsk` in three places,
in those words: `internal/coordinator/coordinator.go` (the pre-`8a953ff` form of
`:717-726`), `DESIGN.md:106-110` and `SECURITY.md:225` — *"layer 2 grant —
`--allowedTools` under `dontAsk` default-deny"*. §2.2 is what those sentences
become.

## 2. Decision

### 2.1 `auto` is the right member of the closed set

**The default permission mode for a node declaring none is `graph.PermissionAuto`
(`internal/schedule/scheduler.go:66`).**

The maintainer asked for `auto`. **The measurement supports it**, with one
correction to what the support consists of, stated before the argument so it
cannot be lost inside it:

> The case for `auto` rests on `0213b`'s **84 of 246** and on §1.3's non-TTY
> verdict. It does **not** rest on `0218`'s 44, which is about verification and
> would be unmoved by any permission mode (§1.5). An argument for this change
> that cites the 44 is citing the wrong measurement.

The closed set has six members (`internal/graph/graph.go:84-95`). Each was
judged on what it does to a headless subprocess:

| member | disposition | why |
| --- | --- | --- |
| **`auto`** | **chosen** | The only member that can approve a call no rule matched. §1.3 establishes it denies rather than blocks without a TTY; §1.5 establishes there are 84 denied calls in the corpus that a boundary was not the reason for. |
| `dontAsk` | the status quo, rejected | It is not wrong — it is the safest member, and it is what the corpus was measured under. It denies all 84 along with the 162, and there is no path by which it can ever do otherwise: `deny if not pre-approved` is its whole definition (§1.2). Keeping it is choosing the friction the `$34.70` node paid. |
| `manual` | rejected on a reading | `0034-strings §A`, L138343: *"'manual' is accepted as an alias for 'default'"*, and `default` is *"Standard behavior, prompts for dangerous operations"*. It is a **prompting** mode handed to a process that cannot prompt, so it lands back on §1.3's L171823 refusal — the dead ask, without the classifier's allow path. Strictly worse than `dontAsk`, because what it does to the calls it does *not* consider dangerous is unmeasured. |
| `acceptEdits` | rejected | It auto-accepts file edits and prompts for everything else. It widens exactly the destructive axis with no adjudication at all, and it retires none of the measured friction: **every one of the 246 denials in `0213b` was of `Bash`** (`0213b:140-142`), as were **all 185** in `0218` (`0218:275-276`). It buys nothing measured and pays on something unmeasured. |
| `plan` | rejected | *"no actual tool execution"* (§1.2). A default of `plan` is a graph that does nothing. |
| `bypassPermissions` | rejected, and §2.3 keeps it refused | It auto-approves every call except explicit deny rules (`0034-strings §D`, L208371). It is the one member the project has always refused for planned nodes, and this change does not touch that. |

The positive case, stated at the size the evidence carries: **for 84 of 246
denied calls in this corpus, the denial was not the ceiling doing its job.** The
node held the grant and lost the call to a shape the CLI's matcher did not
accept. `dontAsk` has no mechanism that could ever tell those apart from the
162; `auto` has one, and it is the CLI's own. Whether it *does* tell them apart
is §6.

What is bought is bounded, and the bound is stated: **`0213b`'s class (B) — 162
of 246, the calls out of scope from their own first word — is the risk surface,
and how `auto` disposes of them is 미측정.** If the classifier approves them,
layer 2 stops being an effective ceiling and §6 reverts this. That number is the
one this decision most wants and does not have.

### 2.2 What this does to the ceiling: five layers bind, one loosened

Every layer, with where it is built and where it is rendered, at `3e6d030`:

| layer | policy construction | argv rendering | after this change |
| --- | --- | --- | --- |
| **0** declaration | `internal/coordinator/coordinator.go:69` (`plannedToolAllowlist`), enforced at `:1421` (`validatePlannedNodeTools`) | none — the graph is refused before anything spawns | **STILL BINDS, untouched.** A plan naming `Bash`, `Bash(*)` or an unrestricted `WebFetch` never becomes a graph. This is a plan-time rejection and knows nothing about permission modes. |
| **1** isolation | `internal/coordinator/coordinator.go:769` (`SettingSources: isolatedSettingSources()`), value at `:787-789` | `internal/runner/claude_protocol.go:40-42` | **STILL BINDS, untouched.** It decides which *sources* supply allow rules, not what an unmatched call becomes. Its E1 half — that isolation stops a standing `Bash(*)` matching before the node's own grant — is mode-independent (§2.5). |
| **2** grant | `internal/coordinator/coordinator.go:767` (`AllowedTools: node.AllowedTools`) | `internal/runner/claude_protocol.go:49-51` | **LOOSENED — this is the only one.** The argv is byte-identical. What changed is the disposition of its complement: a call matching no allow rule was categorically denied and is now adjudicated. §2.2a. |
| **3** narrowing | `internal/coordinator/coordinator.go:768` (`Tools: narrowedToolsFor(node, false)`), function at `:813` | `internal/runner/claude_protocol.go:52-54` | **STILL BINDS, and binds harder.** `--tools` *"Specify the list of available tools from the built-in set"* (`0034-strings §D`, L245900) — it replaces the tool set rather than gating it. A tool that is absent cannot be called, and there is nothing for a classifier to adjudicate. |
| **4** MCP | `internal/coordinator/coordinator.go:770` (`StrictMCPConfig: true`) | `internal/runner/claude_protocol.go:55-57` | **STILL BINDS, untouched.** A separate axis; whether it closes MCP at all is unmeasured (DESIGN.md, E5) and is unchanged by this. |
| **5** residual | `internal/coordinator/coordinator.go:771` (`DisallowedTools: disallowedToolsFor(node)`), function at `:834` | `internal/runner/claude_protocol.go:58-60` | **STILL BINDS, and becomes load-bearing.** Deny is evaluated ahead of the ask/classifier stage (`0034-strings §D`, L128877 `isPreAskDeny`), is enumerated as `denied_by_rule` *separately from* `classifier` (L189479-189482), and survives even `bypassPermissions` (L208371) — *"Explicit ask/deny rules are always respected"* (L229125). §2.2b. |

Layers 1 and 4 remain the two an operator may decline together with
`--accept-loaded-user-config` (`internal/coordinator/coordinator.go:773-776`,
ADR 0032). That opt-in is unchanged by this record and is orthogonal to it.

#### 2.2a Layer 2 loosened in meaning, not in argv

Stated as one sentence, because three documents have to carry it:

> **A call matching none of layer 2's allow rules used to be a DENY. It is now a
> question put to the CLI's own classifier, which approves or denies it.**
> Default-deny became default-classifier.

The argv did not move. `--allowedTools` still carries exactly the node's
declaration, and layer 1 still makes that argv the only allow-rule source for a
planned node. What was a whitelist whose complement was refused is now a
whitelist whose complement is adjudicated — by a model, on the CLI's own rules
(`autoMode.{environment, allow, soft_deny, hard_deny}`, `0034-strings §C`,
L248452), with no oh-my-graph involvement and no record oh-my-graph writes.

Read-only operations do not even reach the classifier
(`0034-strings §C`, L125730: *"reading files, searching code, and other
read-only operations do not require the classifier"*), which is a widening that
layer 3 still bounds — a node whose `--tools` omits `Read` has no `Read`.

**One narrowing comes with it, and it is not the direction anyone expects.**
`auto` discards allow rules it considers broad enough to bypass the classifier:
*"Ignoring dangerous permission … (bypasses classifier)"* (`0034-strings §E`,
L182321-182323) and *"These permissions.allow entries in your user settings are
broad enough that auto mode either ignores them at runtime, or auto-approves
destructive commands with no check"* (L218996). For a **planned** node this
changes nothing — layer 0 forbids a plan from ever declaring `Bash` or `Bash(*)`
(`internal/coordinator/coordinator.go:69`), so its grants are narrow patterns by
construction, and a narrow allow is not routed through the classifier unless a
setting whose *"Default: false"* says so is turned on (L263701). For a
**hand-written** node it may matter a great deal, and §2.4 is where that lands.

#### 2.2b Layer 5 is now the only categorical refusal

Before this change, two mechanisms could refuse a planned node's call outright:
the deny list, and the mode. Now there is one. `disallowedToolsFor`
(`internal/coordinator/coordinator.go:834`) was already described as the layer
that survives an assumption about the others turning out wrong
(`internal/coordinator/coordinator.go:74-79`); it is now the layer that survives
a classifier judging differently than the operator would.

**This is a consequence, not a new mechanism.** No entry is added to
`deniableTools` here, and none is removed. What changes is how much weight it
carries, which is a thing to write down rather than to discover later.

### 2.3 `bypassPermissions` stays refused, and stays loud — unchanged

Nothing in this record touches either half, and both are restated with the
address that enforces them so no reader has to re-derive it:

- **A planned node requesting it is refused at plan time.**
  `internal/coordinator/coordinator.go:959-963`: *"planned node %q requested
  permission_mode %s, which auto mode never grants"*. Pinned by
  `TestPlan_RejectsBypassPermissions`
  (`internal/coordinator/coordinator_test.go:579`), which asserts a `*PlanError`
  whose reason names the constant. That test passes unmodified across this
  change — it was not rewritten, and its passing is the evidence.
- **A hand-written graph that opts in is warned about, loudly, per node.**
  `warnBypassPermissions` (`cmd/oh-my-graph/main.go:1881-1890`) writes to stderr
  for every node declaring it: *"WARNING: node %q uses permission_mode:
  bypassPermissions — it can act without prompting. Review the graph before
  running."*
- **One constant so the refusal and the warning cannot disagree on the
  spelling** — `internal/graph/graph.go:91`, whose comment at `:87-90` states
  the rule this record leaves in place: *"It is never a default."*

**And it is still never a default**, which is now a property of one line
(`internal/schedule/scheduler.go:66`) rather than of two.

**A gap this made visible, not one it created.** The plan-time refusal keys on
the node's **declared** field, so it does not constrain the default: a node that
declares nothing has `PermissionMode == ""` and sails past
`internal/coordinator/coordinator.go:959` regardless of what the scheduler
substitutes afterwards at `internal/schedule/scheduler.go:1357-1360`. That was
equally true under `dontAsk` and is not a defect of this change, but it means
*"auto mode never grants `bypassPermissions`"* is guaranteed by the value of one
constant and by nothing that would fail if it changed. §5 carries it as a
failure mode; §3.2 owes it a test.

### 2.4 Hand-written `run` and planned `auto` get the SAME default — argued, not assumed

They are not the same thing and this record does not pretend they are. The
difference is in `policyFor` (`internal/schedule/scheduler.go:1395-1404`): a
hand-written node gets `runner.ToolPolicy{AllowedTools: node.AllowedTools}` and
nothing else, so `SettingSources` is nil and the operator's user, project and
local settings all load — pinned by `TestScheduler_HandWrittenGraphGetsNoCeiling`
(`internal/schedule/scheduler_test.go:1738`). A planned node gets all five
layers. So the flip does **different things** to them:

| | planned `auto` node | hand-written `run` node |
| --- | --- | --- |
| allow-rule sources | oh-my-graph's argv only (layer 1 = `""`) | argv **plus** the operator's own settings |
| what an unmatched call was | DENY | DENY — but far fewer calls were unmatched, because a standing `Bash(*)` matched them |
| what it is now | classifier | classifier |
| direction of the change | **wider** — the complement of a narrow argv grant is now adjudicated instead of refused | **possibly narrower** — `auto` discards broad allow rules at runtime (§2.2a, `0034-strings §E`), and a standing `Bash(*)` is exactly the shape it names |

That last cell is not hypothetical in shape, though it is unquantified here.
`SECURITY.md:67-71` already states the standing-grant case in the operator's
terms — *"If your settings carry a standing grant like `Bash(*)` or `Write(*)`,
a node has it regardless of what the graph declares"* — and `Bash(*)` is exactly
the shape `0034-strings §E`'s L218996 calls *"broad enough that auto mode either
ignores them at runtime, or auto-approves destructive commands with no check."*
**Whether `auto` discards any particular rule in any particular settings file is
미측정**, and no count of how often that situation arises in this corpus is
published in this repository; the point stands on the mechanism, not on a
frequency.

**Parity is chosen.** Four reasons, in order of weight:

1. **A split would put oh-my-graph's own permission policy above the CLI's, on
   the artifact where the project's standing position is the opposite.** A
   hand-written graph is the operator's reviewed artifact and their settings
   *are* the intended policy (`SECURITY.md:71-73`). Under `auto` that stays
   true, except that the CLI declines to honour rules it judges broad enough to
   bypass its own classifier — which is the CLI's policy about the operator's
   settings, and is exactly what the operator would get by typing `claude
   --permission-mode auto` themselves. Passing the mode through is not
   oh-my-graph making that choice. **Withholding** it from hand-written graphs
   would be.
2. **The direction of the difference is unmeasured, so a split would have to
   pick a side blind.** Giving `run` a different default means asserting either
   "hand-written nodes need protecting from the widening" (which for them may
   not be a widening at all) or "they need protecting from the narrowing" (which
   nobody has observed). Neither assertion has a number behind it.
3. **The resolution site is one branch, and splitting it would be the first
   place the engine's permission semantics differ by graph provenance.** The
   ceiling already differs by provenance, and deliberately — but the ceiling is
   a set of *bounds oh-my-graph imposes*. The mode is a *value it passes
   through*. The project's most load-bearing policy is one list with no branch
   (`internal/childenv/childenv.go`), for the reason that the half which falls
   behind fails silently; the same shape of argument applies to a second
   resolution path that nobody would exercise.
4. **The hand-written author has a per-node override and uses it.** `run` graphs
   declare `permission_mode:` freely — this repository's own README example does
   (`README.md:136`) — so an author who wants `dontAsk` types it, per node, and
   the default never reaches them. The **`auto` operator has no such lever**,
   which is the asymmetry worth naming (§3.3) — but it argues for giving `auto`
   a flag, not for giving `run` a different default.

### 2.5 What is unchanged, stated so nobody re-derives it

- **The closed set is unchanged.** `auto` was already a member
  (`internal/graph/validate.go:66-72`, `internal/graph/graph.go:86`), measured
  from `claude --help` at 2.1.221. No mode was added, none removed, and the
  retroactive-cost problem the set's comment warns about
  (`internal/graph/graph.go:72-83`) is not engaged: an older snapshot naming
  `auto` parses fine, because this binary always enumerated it.
- **The Codex runtime is untouched, and its argv does not change by one byte.**
  `codexProtocol.buildArgs` has no `--permission-mode`
  (`internal/runner/codex_protocol.go:28-48`); the mode is translated only into
  a filesystem sandbox by `CodexSandbox`
  (`internal/runner/codex_protocol.go:55-64`), which branches on `plan` and
  `bypassPermissions` and sends everything else to `workspace-write`. `auto` and
  `dontAsk` both take the `default` arm and produce the identical string.
  Approval policy is hardcoded separately (`:35`, `approval_policy="never"`).
  **This is a Claude-runtime change.** `cmd/oh-my-graph/main.go:1192`'s
  user-facing sentence about the mapping stays true and was not edited.
- **`internal/childenv` is untouched.** One list, no runtime branch. A diff
  under it would mean this took a wrong turn.
- **No fifth exec seam, no new spawner, no new flag, no graph-schema field.**
- **Layer 0's allowlist is unchanged** — no tool was added to
  `plannedToolAllowlist` on the strength of the classifier now existing, and
  none should be. §5.
- **E1 is not invalidated, and half of it never depended on the mode.** The
  measurement (DESIGN.md, E1, claude 2.1.220) ran under `dontAsk`: an
  out-of-scope `touch` ran without layer 1 and was denied with it. The half that
  is mode-independent is the load-bearing one — isolation is what stops the
  operator's `Bash(*)` from matching first. The *denial* half is precisely what
  the mode decides, so under `auto` the same `touch` goes to the classifier
  instead, and **whether it survives that is 미측정**. This is written into the
  code comment at `internal/coordinator/coordinator.go:728-735` as well as here,
  because the code comment is where the next reader will be.

### 2.6 A run keeps the mode it was launched under

The default is not only a fallback; it is a fact about a run that outlives the
process. `state.json` recorded the *declared* mode only — inside the graph
snapshot, with `omitempty`, so an undeclared node had no key at all
(`internal/graph/graph.go:256`, pinned by
`TestParse_UndeclaredPermissionModeStaysEmpty`,
`internal/graph/validate_test.go:876`) — and recorded the *resolved* one nowhere.
`resume` rebuilds the graph through the same `Parse` and re-resolves, so an old
run would have silently continued under whatever the current binary's default
had become.

`3e6d030` closes that with one optional snapshot field,
`runstate.Snapshot.DefaultPermissionMode` (`internal/runstate/runstate.go:430`,
contract at `:410-429`), written at launch
(`cmd/oh-my-graph/main.go:1034`) and read on resume with **absent meaning
`dontAsk`** (`cmd/oh-my-graph/resume.go:560-562`), then written back resolved
(`:580`) so the question is open exactly once per run.

Three sub-decisions, made explicitly:

1. **Absent reads as `dontAsk`.** Every snapshot written before `8a953ff` ran
   `dontAsk`; reading absence as the *current* default would resume old runs
   under a mode they never ran. This does create two populations — old runs stay
   `dontAsk` forever — and that is the correct outcome: a resumed leg must run
   what the first leg ran.
2. **Not canonicalized in `MarshalJSON`, unlike `Runtime`.** `Runtime` could be
   stamped at the write boundary because one value (`claude`) was right for
   every file that omitted it (`internal/runstate/runstate.go:365-378`). Here the
   right value for an old file (`dontAsk`) is not the right value for a new run
   (`auto`), so there is no single value the boundary could write that would be
   true of both. The pattern was available and was deliberately not used.
3. **Schema stays 3.** The field is additive and optional; an older binary
   ignores a key it does not know and resolves the mode exactly as it did.

### 2.7 The name collision, named once

**This project already calls something "auto mode".** `oh-my-graph auto` is the
planner path, and `internal/coordinator/coordinator.go:961` refuses a node *"which
auto mode never grants"* — meaning oh-my-graph's auto, not the permission mode.
As of this change, `auto` is also a `claude --permission-mode` value, and the two
are unrelated: a planned node under `oh-my-graph auto` gets permission mode
`auto` by default, and a hand-written `run` node gets it too.

Every document touched by this change writes **"permission mode `auto`"** or
**"`--permission-mode auto`"**, never a bare "auto mode", where the permission
value is meant. This is a naming hazard with no fix available — neither name is
oh-my-graph's to change — so it is disclosed rather than resolved.

## 3. Consequences

### 3.1 The documentation this decision owes

Each of these stated layer 2's binding force as resting on `dontAsk`, and each is
false as written after `8a953ff`:

- **`SECURITY.md:225`** — the layer table's row 2, *"`--allowedTools` under
  `dontAsk` default-deny"*. SECURITY.md is what tells a user what a planned node
  can do, so §2.2's answer has to live there and not only here: the table says
  which layers still bind, and the prose says what layer 2's complement now
  meets.
- **`DESIGN.md:106-110`** — *"the mode decides what an unanswerable ask becomes
  — under `dontAsk` (our unattended default) it becomes a **deny**. So the CLI
  is already default-deny for us."* Also `DESIGN.md:98` (`dontAsk` named as the
  substituted default), `:2430` (the layer table), `:2459-2460`, `:2900`.
- **`README.md`** — its ceiling paragraph (`:201-212`) delegates the layer detail
  to SECURITY.md and needs the default named once, in the operator's terms.
  `README.md:136`'s `permission_mode: dontAsk` stays: it is a hand-written
  graph's explicit declaration, which is still valid and is now a deliberate
  departure from the default rather than a restatement of it.
- **`CHANGELOG.md`**, under `## [Unreleased]`, naming the old default and the
  new one and pointing here. No release, no tag, no version bump.

`DESIGN.md`'s V2 and E1 passages (`:2955-2957`, `:2973-2983`) are records of what
was measured on claude 2.1.220 under `dontAsk`. **They are not edited** — they
were true when taken and remain true of that mode. What they get is the note
that the mode they measured is no longer the default (§2.5).

**`docs/measurements/*` are not edited at all.** They are dated readings; the
corpus really was `dontAsk`.

### 3.2 Tests this decision rests on, and the one it owes

Shipped with `8a953ff` and `3e6d030`, all `FakeRunner`-based with zero real
spawns:

| test | address | asserts |
| --- | --- | --- |
| `TestScheduler_UndeclaredPermissionModeRunsUnderTheDefault` | `internal/schedule/scheduler_test.go:1644` | an undeclared node's invocation carries `graph.PermissionAuto` — presence, not absence |
| `TestScheduler_DeclaredPermissionModeWinsOverTheDefault` | `internal/schedule/scheduler_test.go:1668` | a declared mode wins, over a table including `dontAsk`, so the old behaviour is requestable by name |
| `TestPlan_RejectsBypassPermissions` | `internal/coordinator/coordinator_test.go:579` | §2.3's refusal, passing **unmodified** across the change |
| `TestResume_SnapshotWithoutDefaultPermissionModeStaysDontAsk` | `cmd/oh-my-graph/resume_test.go:476` | §2.6: a snapshot with the key deleted resumes `dontAsk`, and the resumed leg writes the resolved value back rather than erasing it |
| `TestScheduler_HandWrittenGraphGetsNoCeiling` | `internal/schedule/scheduler_test.go:1738` | §2.4's premise, unmodified |

**Owed, and not yet written** (§2.3's gap): an assertion that
`schedule.DefaultPermissionMode` is not `graph.PermissionBypass`. It is one line,
it can never fail today, and it is worth having precisely because the guarantee
*"auto mode never grants `bypassPermissions`"* currently rests on the value of a
constant that no test reads.

### 3.3 What this costs, stated rather than softened

- **A new failure mode with no `dontAsk` equivalent.** *"Agent aborted: too many
  classifier denials in headless mode"* (`0034-strings §B`, L171831). Today a
  denied node keeps going; under `auto` a node with enough denials is killed.
  **The threshold is not in the on-disk text — 미측정.** In a corpus where 162 of
  246 denied calls are out of scope from their first word (`0213b:171`), a low
  threshold would kill nodes mid-run. It is a *louder* failure than today's
  "denied and PASSed anyway", which is not obviously worse — but it is different,
  and nobody has seen it happen.
- **A model call per unmatched tool call.** `auto` adds a classifier invocation
  where `dontAsk` added nothing (`0034-strings §C`, L194904 names the classifier
  model; L263701 calls *"more classifier calls"* an explicit trade). Against
  `0213b`'s 1368 `Bash` tool-use blocks (`:140`), the effect on run wall-clock is
  unknown. **This project runs on a subscription, so the pressure point is the
  usage limit, not dollars.** 미측정.
- **A silent downgrade path.** Six availability gates can turn `auto` off —
  settings, plan, model, `CLAUDE_CODE_ENABLE_AUTO_MODE`, a circuit breaker, and
  a managed `disableAutoMode` (`0034-strings §F`) — after which
  `kickOutOfAutoIfNeeded` puts the mode back to `default` (L182423). Nothing
  fails, nothing prints, and the run's `state.json` still says `auto` because
  oh-my-graph records what it *passed*. The direction is narrowing (headless
  `default` lands on §1.3's refusal), so the failure is loud in its effect and
  silent in its cause. **Whether any gate fires on this account is 미측정, and
  it is the first thing to measure.**
- **Two populations of runs.** §2.6(1). A run launched before `8a953ff` resumes
  `dontAsk` forever. This is correct and it means the corpus is not
  homogeneous — §6's re-measurement selects on the snapshot field for exactly
  this reason.
- **The operator of `auto` cannot choose the mode.** There is no flag; the
  planner authors the graph and does not declare a mode; the default is the only
  lever and it is a compile-time constant. A hand-written author has a per-node
  override (§2.4(4)) and the `auto` operator has nothing. Naming it, not fixing
  it — a flag is a separate decision with its own disclosure question.

### 3.4 Compatibility

- **`state.json` is forward- and backward-readable.** The new key is
  `omitempty` and additive; schema stays 3 (§2.6(3)). An older binary reading a
  newer snapshot ignores it and resolves as before.
- **No graph needs changing.** No `graphs/*.yaml`, fragment or saved plan is
  affected; a graph that declares a mode keeps it, and one that declares none
  changes what it resolves to — which is the change.
- **Codex runs are byte-identical** (§2.5).
- **`resume` on a pre-`8a953ff` run is byte-identical** (§2.6).

## 4. Alternatives considered

| # | alternative | verdict |
| --- | --- | --- |
| 1 | **Keep `dontAsk`.** | Rejected in §2.1. The safest option and the one the corpus was measured under; it also permanently denies the 84 of 246 that a boundary was not the reason for, with no mechanism that could ever distinguish them. |
| 2 | **`manual` / `acceptEdits` / `plan` / `bypassPermissions`.** | Rejected in §2.1's table, each on a specific address rather than on taste. |
| 3 | **`auto` for planned nodes, `dontAsk` for hand-written ones.** | Rejected in §2.4. It would pick a side of an unmeasured difference, and would have oh-my-graph withhold from the operator's own reviewed artifact a mode their own CLI would give them. |
| 4 | **An `--permission-mode` flag on `auto` instead of moving the default.** | Not rejected — **deferred**, §3.3's last bullet. It is a strictly larger change (a flag, a disclosure line, a resume-inheritance rule) and it does not answer the question asked, which is what an undeclared node should do. A flag with today's default still denies the 84. |
| 5 | **Move the default and leave `state.json` alone.** | Rejected in §2.6. Old runs would silently resume under a mode they never ran, and no field in the snapshot could have said otherwise. |
| 6 | **Canonicalize the new field in `MarshalJSON`, as `Runtime` does.** | Rejected in §2.6(2): no single value is true of both an old file and a new run. |
| 7 | **Widen `plannedToolAllowlist` now that a classifier exists** — e.g. allow bare `Bash` and let the classifier sort it out. | Rejected, and not merely deferred. It would make the classifier the *first* line rather than the last, and layer 0's whole point is that it runs before anything spawns. §5. |

## 5. Failure modes

- **The classifier approves class (B).** 162 of 246 denied calls were out of
  scope from their first word (`0213b:171`). If those are approved, layer 2 is no
  longer an effective ceiling for planned nodes and this decision is wrong. It is
  §6's first falsifier and the single largest unknown here.
- **The headless abort kills nodes.** §3.3, first bullet. Unmeasured threshold,
  no `dontAsk` equivalent.
- **`auto` is silently unavailable** and every run has been `default` all along.
  §3.3, third bullet. The measurements in §6 would then be measuring `default`,
  not `auto`, and would say so nowhere.
- **This record read as a licence to widen layer 0.** Alternative 7. "A model
  checks it now" is the argument that would move `Bash` into
  `plannedToolAllowlist`, and it inverts the ordering: layer 0 is a plan-time
  refusal that costs nothing, and the classifier is a per-call judgement that
  costs a model call and can be wrong. The cheap guard runs first.
- **`0218`'s re-measurement reports a false improvement.** Its discriminator is
  the `dontAsk` sentence (`docs/measurements/0218-denied-nodes-that-passed.go:349`),
  which under this default matches nothing. §6 makes this a precondition rather
  than a footnote, because `0218:186-188` predicted exactly this shape of miss:
  *"a sentence that a CLI release note could change without warning, and would do
  so silently, in the direction of reporting no denials."*
- **The default drifts to `bypassPermissions`.** §2.3's gap: the plan-time
  refusal keys on the declared field and would not fire. §3.2 owes the one-line
  test.

## 6. Falsification — the number that reverts this

The candidate is a **re-run of `docs/measurements/0218-denied-nodes-that-passed.go`**,
and it has two preconditions without which its output is not evidence:

**P1 — the discriminator must match both phrasings.** `denialCore` at
`docs/measurements/0218-denied-nodes-that-passed.go:349` is the `dontAsk`
sentence (`0034-strings §C`, L202184). Under this default the CLI writes
*"Permission for this action was denied by the Claude Code auto mode classifier.
Reason: "* instead (L125726) — a phrasing `0218` already found in this very
corpus and already counted as a known miss (`0218:143`, class B). **A re-run with
the unmodified script reports approximately zero denials and that is an
artifact, not a finding.**

**P2 — the population must be selected structurally, not by date.** A run counts
only if its `state.json` carries `default_permission_mode: "auto"`
(`internal/runstate/runstate.go:430`). Date is not a proxy: a pre-`8a953ff` run
resumed after it still runs `dontAsk` (§2.6), and `0213b:322-325` already warns
that this corpus mixes CLI versions with nothing recording them.

**The number.** Over the first **73 readable planned nodes** in runs recorded
`default_permission_mode: "auto"` — 73 exactly, so the count is comparable to
`0218`'s as a raw count, which is the only comparison `0218:349-365` permits:

> **If the denied-and-still-`PASS` count is 44 or higher, revert to `dontAsk`.**
> The baseline is **49 of 73** (`0218:250`).

Why 44 and not some other number. It is a decision threshold, not a prediction —
what the classifier does to any given call is 미측정 (§2.1), so no prediction is
available. It is derived from the unit of independence rather than from an
expected effect: the 73 nodes come from 18 runs (`0218:207`, `:245`), a mean of
4.06 nodes per run, and `0218:353-358` establishes that the run — not the node —
is what varies independently, with 6 of 18 runs wholly denied and 6 wholly
undenied. **A movement smaller than one run's worth of nodes is not evidence of
anything.** One run's worth is 5 (4.06 rounded up), and 49 − 5 = 44. So a count
at or above 44 means the flip did not move the outcome by even one run's worth,
having bought a classifier call per unmatched call, a new headless-abort failure
mode, and a widened layer 2. That is a bad trade and the honest response is to
revert.

Two companion readings, which do not by themselves revert but which decide what
a revert would mean:

- **The call-level split, re-run from `docs/measurements/0213b-compound-commands.go`
  over the same auto-default population.** The prediction this decision is making
  is that class (A) 64 and class (C) 20 shrink and class (B) 162 does not. **If
  class (B)'s share of denied calls FALLS**, the classifier is approving calls
  that were out of scope from their first word, layer 2 is not an effective
  ceiling for planned nodes, and this is wrong for a reason worse than
  ineffectiveness. That is §5's first failure mode, and it reverts this
  regardless of what the node count says.
- **Any occurrence of the headless abort** (`0034-strings §B`, L171831) in a
  planned run. One occurrence obliges measuring the threshold. **If it settles
  any run's outcome in more than 1 of the first 18 auto-default runs**, the new
  failure mode is costing more than the old friction and this reverts.

Two further falsifiers with no number attached, stated so they are not mistaken
for having been covered:

- **`auto` turns out never to have been active** (§3.3, third bullet). Then every
  count above measures `default`, and the whole exercise restarts.
- **An issue reports a planned node doing something its `allowed_tools`
  appeared to forbid.** Under `dontAsk` that could only have come from layer 1
  being absent; under `auto` it can come from the classifier, and the two are
  distinguishable in the transcript by which denial sentence is *absent*.

## 7. Required before Accepted

1. **Confirm `auto` is actually active on this account, plan and model** — the
   six gates of §3.3. This is one real `claude -p` invocation and it is the
   cheapest thing on this list. Until it is done, every claim about what runs is
   a claim about what was *passed*.
2. **Update the `0218` discriminator** to match both phrasings (P1). Without it,
   the next re-measurement reports a false improvement, and this is the item most
   likely to be skipped because everything appears to work.
3. **The §6 count**, over 73 readable planned nodes of auto-default runs.
4. **The one-line test of §3.2** that the default is not `bypassPermissions`.

Until 1 and 3 exist, this record stays **Proposed**. It is worth being explicit
that the code has already shipped on this branch and the measurement has not —
the ordering is the reverse of ADR 0032's, and it means the falsifier is owed
against behaviour that is already running, not against a design.

## 8. What could not be determined

- **How the classifier disposes of `0213b`'s class (B), 162 of 246** — the
  central unknown of this decision — 미측정
- **How much of class (A) 64 and class (C) 20 it approves.** §2.1's case is that
  those are calls a boundary did not explain, not that they will be allowed —
  미측정
- **The headless classifier-denial abort threshold** — 미측정
- **Whether `auto` is available on this account/plan/model at all** — 미측정
- **Whether `-p` requires a prior interactive opt-in.** `--bg` has an explicit
  one (`0034-strings §G`); no `-p` equivalent was found in the extract, and
  **absence in an extract is not absence in the binary** — 미측정
- **What `--setting-sources ""` does to the classifier's own
  `autoMode.{allow,soft_deny,hard_deny,environment}` rules.** They live in the
  settings files layer 1 withholds, so a planned node's classifier may be
  judging on built-in defaults with none of the operator's tuning — 미측정
- **The cost and latency a classifier call adds**, in a project whose pressure
  point is a subscription usage limit — 미측정
- **Whether `auto` narrows a hand-written node** by discarding a standing
  `Bash(*)` (§2.4) — 미측정, and it is the direction nobody expects
- **Anything at all about a node actually running under `auto`.** No `claude`
  was run for this record. Every statement about the mode's behaviour is a
  reading of `docs/measurements/0034-what-auto-mode-does-on-disk.md`.

## 9. References

- `docs/measurements/0034-what-auto-mode-does-on-disk.md` — 46 strings from
  `claude` 2.1.241, pinned with the binary's SHA-256 and the `strings` command.
  The source of §1.2, §1.3 and every `0034-strings` citation.
- `docs/measurements/0213b-compound-commands-defeat-grants.md` — 246 denied Bash
  calls in planned nodes: (A) 64, (B) 162, (C) 20. The measurement that supports
  this change, and the source of §6's second reading. Issue
  [#213](https://github.com/jitokim/oh-my-graph/issues/213).
- `docs/measurements/0218-denied-nodes-that-passed.md` — 53 of 73 planned nodes
  denied, 49 `PASS`, 44 with no engine-run check. The measurement that does
  **not** support this change (§1.5), and the script §6 re-runs. Issue
  [#218](https://github.com/jitokim/oh-my-graph/issues/218).
- ADR 0004 — *Auto mode tool ceiling by settings isolation*
  (`docs/adr/0004-auto-mode-tool-ceiling-by-settings-isolation.md`). **The record
  this one amends.** It is where the layer table came from and where the sentence
  removed from `SECURITY.md` and `DESIGN.md` originated — its own layer 2 row
  reads *"`--allowedTools` + `dontAsk` default-deny"* (`0004:68`), and it states
  *"The CLI is already default-deny for our unattended nodes"* (`0004:28-30`) and
  that *"`--allowedTools "Bash(git *)"` under `dontAsk` means git and nothing
  else"* (`0004:361`). **It is not edited**, in the same way ADR 0033 did not
  edit ADR 0030: it was true of the default it was written under, and an ADR is a
  dated decision rather than a live document. The amendment is one sentence —
  **layer 2's complement is adjudicated rather than refused, and the four other
  layers ADR 0004 established are untouched** (§2.2).
- ADR 0033 — *The run is the unit of evidence, not the node*. Where the 44
  belongs, and why it is not this record's argument.
- ADR 0032 — *A planned node may carry the operator's configuration, if the
  operator says so*. Layers 1 and 4's opt-in, orthogonal to this and unchanged.
- ADR 0022 — *A mapped node gets its agent staged, not its settings back*. Why
  layer 1 stays `""` for agent-mapped nodes, unchanged here.
- ADR 0025 — *One run, one CLI runtime*. Why §2.5's Codex paragraph is a
  statement about one runtime rather than about the engine.
- `SECURITY.md` "Auto-planned graphs" — the layer table this record edits, and
  the document a user reads to learn what a planned node can do.
