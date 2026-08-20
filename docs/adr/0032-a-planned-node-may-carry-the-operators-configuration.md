# ADR 0032 — A planned node may carry the operator's configuration, if the operator says so

**Status:** Proposed. No code exists. This record decides the shape; §3 lists
the tests the implementation lane owes and §8 the one measurement owed before
Accepted. Nothing here is implemented, and the flag it names does not yet parse.

**Where the addresses point.** Written before its code, against `9590682` — the
`main` this branch left. Every `file:line` below was read at that commit and
resolves exactly there; the Context section is ABOUT that tree. The Decision
section describes code that does not exist yet, so its addresses are the call
sites to change, given as file, line **and** function name — the line will go
stale the moment the diff lands, the function name will not.

**Date:** 2026-08-21

## 1. Context

### 1.1 What the ceiling actually discards, read rather than assumed

`toolPolicyFor` (`internal/coordinator/coordinator.go:691`) is one planned
node's whole execution ceiling. Layer 1 is `SettingSources`, and its value is
`isolatedSettingSources()` — a pointer to `""`
(`internal/coordinator/coordinator.go:708-711`). The function's own comment
names the price without being asked to, at
`internal/coordinator/coordinator.go:687-690`:

> The cost, which belongs in the README and not in a surprise: a planned node
> also loses the user's CLAUDE.md, hooks and MCP servers. Planned nodes are
> more isolated and less capable than they were. That is the intended
> direction.

That comment is the whole of this ADR's subject. The direction is not disputed
here and is not reversed here; what is decided is whether the operator may
depart from it deliberately, for one run, out loud.

Two more facts from the same function, both load-bearing below:

- **Enterprise policy is not in the trade.**
  `internal/coordinator/coordinator.go:705-707`: *"Enterprise policy settings
  and `--settings` flag settings are unioned on top and cannot be dropped by
  this flag, so it can never be used to step around a corporate policy."* Layer
  1 could never drop managed policy, and nothing in §2 can grant it either —
  the mechanism decided below is the ABSENCE of an argument, and an absent
  argument cannot subtract from a union it never joined.
- **Layer 4 is a separate axis.** `StrictMCPConfig: true` is set at
  `internal/coordinator/coordinator.go:696`, next to layer 1 but independent of
  it. This matters in §2.3: turning layer 1 off alone would leave
  `--strict-mcp-config` on the argv, and any disclosure claiming "your MCP
  servers load" would then be a sentence nobody had measured. DESIGN.md:2919-2923
  is explicit that E5 was never measured and that *"no claim is made"* that the
  flag closes MCP.

### 1.2 How the two runtimes render the same field

The policy is runtime-blind; each protocol renders it.

**Codex** — `internal/runner/codex_protocol.go:28-48`. When
`spec.Policy.SettingSources != nil`, `buildArgs` appends exactly four things
(`:37-43`):

```go
if spec.Policy.SettingSources != nil {
        args = append(args,
                "--ignore-user-config",
                "--ignore-rules",
                "--config", "project_doc_max_bytes=0",
                "--config", "mcp_servers={}",
        )
}
```

The VALUE is never read — only its nil-ness. `--sandbox` and
`approval_policy="never"` are appended unconditionally above it
(`:29-35`), outside that branch.

**Claude** — `internal/runner/claude_protocol.go:30-67`. The Claude equivalent
of those four flags is one flag, and its exact spelling is
**`--setting-sources ""`** (`:40-42`):

```go
if policy.SettingSources != nil {
        args = append(args, "--setting-sources", *policy.SettingSources)
}
```

Here the value IS read. One flag on one side, four on the other, from one nil
check — which is why §2.2's symmetry question cannot be answered by
convenience: both sides already hang off the same bit.

### 1.3 `run` is unaffected — CONFIRMED, and the whole design rests on it

The claim was checked end to end rather than taken.

`internal/schedule/scheduler.go:1360-1369`, `(*Scheduler).policyFor`:

```go
if s.toolPolicies == nil {
        return runner.ToolPolicy{AllowedTools: node.AllowedTools}, nil
}
```

A `nil` map is the hand-written `run` path, stated at
`internal/schedule/scheduler.go:1346-1349`. The returned policy carries
`AllowedTools` and nothing else, so `SettingSources` is nil, `Tools` is nil,
`StrictMCPConfig` is false and `DisallowedTools` is empty. `Options.ToolPolicies`
is populated at exactly three sites, all of them `auto`/`resume`
(`cmd/oh-my-graph/main.go:922`, `cmd/oh-my-graph/resume.go:543` and `:607`);
the `run` path passes none. The behaviour is already pinned by
`TestScheduler_HandWrittenGraphGetsNoCeiling`
(`internal/schedule/scheduler_test.go:1679-1710`), whose assertions include:

> `hand-written node got settings isolation %q; its own settings are the intended policy`

**The claim is true.** A hand-written graph run with `run` already carries the
operator's settings, CLAUDE.md, hooks and MCP servers, on both runtimes, today,
with no flag. That is not a gap to be closed; it is the reference behaviour this
ADR makes reachable from `auto`.

### 1.4 The run already carries the operator's config — in its other call

`coordinatorInvocation` (`internal/coordinator/coordinator.go:357-363`) builds
the PLANNER's stance:

```go
Policy: runner.ToolPolicy{DisallowedTools: disallowedToolsFor(graph.Node{})},
```

`SettingSources` is nil. `internal/coordinator/assess.go:186-189` says why in
so many words — the planner's *"job includes reading this repository's
CLAUDE.md"*. So in every `auto` run today, one of the two kinds of call already
runs under the operator's full configuration and the other does not. The
ceiling is a **planned-node** ceiling, not a run ceiling, and this ADR does not
widen it to a place it never reached. (The ASSESSOR is the opposite case and
stays that way: `assess.go:199-206` gives it layer 1 because its input is
untrusted model output by design. §2.8.)

### 1.5 The two disclosure surfaces that exist, and their shapes

Both live on the plan screen printed by `printPlanForRuntime`
(`cmd/oh-my-graph/main.go:1096-1137`) — after planning, before any node spends,
two-space indented, reached by `auto`, `auto --plan-only`, every later cycle of
a goal loop (`cmd/oh-my-graph/goal.go:101`) and `chat`'s `[y/N]`.

- **Claude:** `noteCeiling` (`cmd/oh-my-graph/main.go:1597-1612`), called at
  `:1126`. It prints, unconditionally for every planned run:

  ```text
    Planned nodes run isolated: none of your user/project/local settings load, so a declared
    scope like Bash(git *) is enforced rather than merely requested — and your CLAUDE.md,
    hooks and MCP servers are unavailable to them. See SECURITY.md for what this does not cover.
  ```

- **Codex:** `noteCodexRuntimePolicy` (`cmd/oh-my-graph/main.go:1152-1182`),
  called at `:1122` with `isolated=true`. Its last line, guarded by that
  parameter (`:1179-1181`):

  ```text
    Auto-planned Codex nodes also ignore user configuration, repository rules, project instructions, and MCP servers.
  ```

  The same function is called with `isolated=false` from the `run` path
  (`cmd/oh-my-graph/main.go:352`), `--dry-run` (`cmd/oh-my-graph/dryrun.go:58`)
  and `lint` (`cmd/oh-my-graph/lint.go:94`) — so the parameter is ALREADY the
  "is layer 1 on" carrier, and already tells the truth about a hand-written
  graph.

The `--verify-cmd` advice is the OTHER seam, and it is a different one:
`coordinator.VerifyAdvice` (`internal/coordinator/verifycmd.go:504-540`) prints
unindented, BEFORE the planner call, because the flag it names must be typed at
launch. Its plan-screen half is `noteMissingBuildEvidence`
(`cmd/oh-my-graph/verifycmd.go:209-233`) — a `note*` sibling that ADR 0030 added
to `printPlanForRuntime` rather than opening a channel of its own. That is the
precedent §2.6 follows.

`noteCodexRuntimePolicy`'s own comment (`cmd/oh-my-graph/main.go:1149-1151`)
sets the budget this ADR must live inside:

> Deliberately one line per difference and no more. A disclosure long enough to
> scroll past is one nobody reads.

## 2. Decision

### 2.1 A flag is right, because the alternative in reach today is WIDER than the flag

The honest alternative was argued first, and it is real. It is also already
documented, at `cmd/oh-my-graph/main.go:1010-1014`:

> `saveGeneratedSpec` persists the planner's JSON spec into dir — the run's own
> directory for a run, `plans/<id>` for a `--plan-only` preview — so an auto
> plan stays inspectable and repeatable: **JSON is valid YAML, so the saved file
> can be hand-edited and re-run directly with `oh-my-graph run <path>`**.

So an operator who wants their own configuration can, today, with no new code:
`auto --plan-only`, read the plan, then `run plans/<id>/graph.json`. By §1.3
that second command carries their settings, CLAUDE.md, hooks and MCP. They do
not even lose the planning — the plan is bought and saved.

That path is not deprecated by this ADR and does not change. But it is not a
substitute, for two reasons, and the second is decisive:

1. **The goal loop is out of reach.** `run` executes one graph once.
   `auto`'s plan → execute → assess → re-plan cycle (`--max-cycles`,
   `cmd/oh-my-graph/goal.go`) re-plans from the assessment of the previous
   cycle, and there is no two-step equivalent: a human cannot hand-carry cycle
   *k*'s assessment into cycle *k+1*. An operator whose nodes need their MCP
   servers therefore cannot use the goal loop at all.
2. **The workaround drops the ENTIRE ceiling, not just layer 1.** By §1.3, a
   node run through `run` gets no `--tools` narrowing, no `--disallowedTools`
   and no binding `--allowedTools`. The flag decided below drops layers 1 and 4
   and keeps layers 2, 3 and 5. So refusing to build it does not preserve the
   ceiling — it routes every operator who wants their config through the one
   door that removes all five layers, and removes the plan screen's disclosure
   with them, because a hand-written graph is by definition the user's own
   reviewed artifact and is disclosed as such.

Choosing NO FLAG would mean choosing the wider escape hatch on the grounds that
it is the one that already exists. **A flag ships.** Its justification is not
capability; it is that the narrower door should exist beside the wider one, and
should say what it costs.

**One flag, for the whole run, on `auto` only.** Not per node: a per-node
opt-in would have to come from the plan, and the plan is the planner's
untrusted output. That is the hole `validatePlannedNodeAgent` closes —
`internal/coordinator/coordinator.go:726-729` names it exactly, *"a planner that
could name `Skill` in allowed_tools would be a planner selecting which of the
user's local files loads into a node it authored"* — and it stays closed. A
planner that could ask for the operator's settings would be worse than one that
could ask for a skill.

### 2.2 The opt-in is RUNTIME-NEUTRAL

`toolPolicyFor` isolates settings for Claude and Codex alike; the price named at
`coordinator.go:687-690` is paid on both. A Codex-only escape hatch would be a
claim that the ceiling is worth less on Codex than on Claude — a claim no
measurement in this repository supports, and one that would be made silently,
by a flag's scope, rather than argued.

Three further reasons, in order of weight:

1. **The mechanism is one bit, already shared.** §1.2: both protocols branch on
   `SettingSources != nil`. A runtime-neutral opt-in is the absence of a
   pointer. A Codex-only one needs a runtime branch in the coordinator, which
   would be the first thing in `toolPolicyFor` that knows which CLI it is
   building for — and this project's most load-bearing policy is deliberately
   `ONE list, NO runtime branch` (`internal/childenv/childenv.go:1-25`). The
   cheap thing and the right thing coincide here, which is exactly the case in
   which it is worth writing down that the cheapness was not the reason.
2. **The operator's motive is runtime-independent.** Wanting one's own
   CLAUDE.md/AGENTS.md, hooks and MCP servers is not a fact about which CLI is
   installed.
3. **The CONSEQUENCES differ, and that is a disclosure problem, not a scope
   problem.** On Codex the sandbox floor survives (§1.2: `--sandbox` and
   `approval_policy="never"` sit outside the branch). On Claude, restored
   settings restore the operator's standing permission grants, and
   `coordinator.go:672-680` records what that does: a standing `Bash(*)` in
   `~/.claude/settings.json` matches before the node's narrower `Bash(git *)`,
   so layer 2 goes back to being a declaration rather than a limit. Same flag,
   different bill. §2.6 therefore prints a different sentence per runtime — and
   that is the correct place for the asymmetry, because the asymmetry is in what
   is true, not in who may ask.

### 2.3 What the opt-in changes, exactly: layers 1 and 4 off, layers 2, 3 and 5 unchanged

With the flag on, `toolPolicyFor` builds:

| layer | field | default | with the opt-in |
| --- | --- | --- | --- |
| 1 isolation | `SettingSources` | `&""` | **nil** |
| 2 grant | `AllowedTools` | node's declaration | unchanged |
| 3 narrowing | `Tools` | `narrowedToolsFor(node, …)` | unchanged |
| 4 MCP | `StrictMCPConfig` | `true` | **false** |
| 5 residual | `DisallowedTools` | `disallowedToolsFor(node)` | unchanged |

**Layer 4 moves with layer 1 and this is not a detail.** §1.1: `--strict-mcp-config`
is an independent axis, and DESIGN.md:2919-2923 says nobody has measured whether
it closes MCP. If it were left on, the disclosure in §2.6 would be asserting
something unmeasured; if it were left on and it DOES close MCP, the disclosure
would be false. Dropping it makes the sentence true by construction. The
resulting rule is one line and testable:

> **An opted-in planned node's setting/config posture is exactly a hand-written
> `run` node's, and its tool posture is exactly a planned node's.**

The residual layers are worth keeping and are not decoration: `--tools`
replaces the built-in tool set outright and is independent of settings (E4,
DESIGN.md:2911-2918 — *"a tool left out of `--tools` is absent even when
`--allowedTools` names it"*), and `--disallowedTools` is subtractive. Both
therefore still bind under restored settings. That is precisely what makes this
door narrower than §2.1's workaround, and it is the claim §8 requires measured
before this ADR is Accepted.

### 2.4 Agent mapping and skill activation turn OFF with it, for a measured reason

Both mechanisms rest on layer 1 being `""`. Measurement (j)
(`docs/measurements/0017-lifting-the-agent-mapped-exclusion.md:110,119`) is
direct, and the two arms differ only in that field:

- arm `X-ACT` — activated node's argv, `--setting-sources ""`, under a
  three-way name collision: resolved to **the STAGED copy, 3 of 3**.
- arm `X` — same fixture, `SettingSources = nil`: resolved to **the REPO copy,
  3 of 3**.

So under the opt-in a same-named `.claude/skills` definition in the working
repository shadows the corpus oh-my-graph staged, and the same reasoning reaches
`--agent`: ADR 0022's guarantee is that a mapped node's system prompt comes from
a directory oh-my-graph staged rather than from discovery, and that guarantee is
held by layer 1.

Rather than let those two guarantees become quietly false, `WithLoadedUserConfig()`
sets `agentMappingOff` and `skillActivationOff` itself — in the Option, not at
the call site, so no future composition of options can reopen it. The CLI prints
the reason where it prints the other de-escalations.

This is a change of provider, not a loss: with the operator's settings loaded,
the selected CLI discovers the operator's own agents and skills natively, which
is what a hand-written `run` node has always done. What is given up is the
staged copy's *attributable name* and its shadow-proofness — stated, not
absorbed.

### 2.5 The name is `--accept-loaded-user-config`

No value. Default **OFF** — the ceiling stays the default, and a run that types
nothing is byte-for-byte the run that shipped in v0.10.0.

ADR 0030 §2.3 is the precedent and it is a rule about grammar: a flag names what
the operator IS ACCEPTING, in a sentence they say, not a knob they turn.
Candidates were judged on what typing it asserts:

| candidate | what typing it asserts | verdict |
| --- | --- | --- |
| `--allow-user-config` | "permit the config plumbing" | **rejected.** It describes the mechanism (`SettingSources`) from the engine's side and grants a permission to the machine, not a statement by the operator. It is also silent about the bill — an operator reads it as pure capability, which is the one reading §2.6 exists to prevent. Precedent rejects exactly this shape. |
| `--no-config-isolation` | ambiguous — a switch or a statement | rejected. It joins the `--no-agent-mapping` / `--no-skill-activation` family, which are genuine feature switches and are all DE-escalations. This flag is the only one in `auto` that widens; family resemblance is precisely the wrong signal. It also names what is skipped rather than what the run then carries. |
| `--accept-standing-grants` | "I accept that my standing permission grants apply" | close, and rejected on discoverability. It names the sharpest half of the bill (§2.2(3)) and nothing of what the operator came for; nobody looking for *"how do my MCP servers reach a planned node"* would ever find it, and on Codex, where the grants story is different, it barely parses. |
| `--accept-loaded-user-config` | "I accept that this run's planned nodes load my user configuration" | **chosen.** Subject, verb, object. `loaded` states what the run now carries and is the one word that distinguishes it from today; `accept` marks it as a bill the operator signs, not a feature they enable; `user-config` is the term both CLIs use for the thing, and is what an operator would search for. It reads correctly in the three places it will be read — on the command line, on the plan screen, and in a CHANGELOG line skimmed a year from now. |

It is long on purpose, and being annoying to type once per run is the design
working. No name prevents a shell alias; only the disclosure in §2.6 and the
record in §2.7 make the choice visible after the fact.

**No flag-vs-flag refusal.** Unlike ADR 0030 §2.3's contradiction with
`--verify-cmd`, this flag contradicts nothing an operator can also type:
`--no-agent-mapping` and `--no-skill-activation` are already implied by it
(§2.4) and passing them alongside is redundant, not inconsistent. Refusing a
redundancy would cost a real script for no gain.

**Help text** (one line, `cmd/oh-my-graph/flags.go`, `newAutoFlags`):

```text
state that this run's planned nodes load YOUR CLI configuration, and run anyway (ADR 0032):
user/project/local settings on Claude, ~/.codex/config.toml plus repository rules and AGENTS.md
on Codex, and with them your CLAUDE.md, your hooks and your MCP servers. This is not only a
capability — your standing permission grants load too, so on Claude a node's declared scope like
Bash(git *) stops being enforced and is a declaration again; each node's --tools set and deny list
still bind, and enterprise/managed policy is unaffected and cannot be widened by this flag. Agent
mapping and skill activation are turned OFF for the run, because a staged definition is shadowed by
a same-named one your restored settings discover. The choice is printed with the plan and readable
in this run's state.json
```

### 2.6 What the disclosure says, verbatim

Printed on the plan screen of §1.5 — the seam that already exists — through a
`note*` sibling of `noteCeiling` and `noteMissingBuildEvidence`, called from
`printPlanForRuntime`. No second channel: not stderr, not a pre-planner line,
not a new writer. One slot, which says the isolated sentence or the loaded one
and never both and never neither, exactly as ADR 0030 §2.5b requires of the
build-evidence slot.

**Claude**, replacing `noteCeiling`'s paragraph for the run that opted in:

```text
  Planned nodes run with YOUR configuration (--accept-loaded-user-config): your user, project
  and local settings load, and with them your CLAUDE.md, your hooks and your MCP servers.
  Your standing permission grants load too, so a declared scope like Bash(git *) is a
  declaration again and not a limit — a call your own settings allow will run. Each node's
  --tools set and its deny list still bind. Enterprise and managed policy are unaffected by
  this flag and cannot be widened by it. Agent mapping and skill activation are off for this
  run: a staged definition would be shadowed by a same-named one your settings discover.
```

**Codex**, replacing the `isolated` line at
`cmd/oh-my-graph/main.go:1180` for the run that opted in:

```text
  Planned nodes run with YOUR configuration (--accept-loaded-user-config): ~/.codex/config.toml,
  your repository's rules and AGENTS.md files, and your MCP servers all load. The filesystem
  sandbox above and approval_policy="never" are argv on every node, so this flag widens neither.
```

Both are `fmt.Fprint` of that literal, two-space indented like every other line
on the screen, and both end with a newline. The Codex line stays inside
`noteCodexRuntimePolicy`'s one-line-per-difference budget by replacing a line
rather than adding one; the Claude paragraph replaces `noteCeiling`'s and is one
line longer than it, which is the price of the grants sentence and is paid
knowingly.

### 2.7 The record is the policy itself, and the resumed leg inherits it and says so

No new snapshot field. `runstate.NodeToolPolicy.SettingSources` is already a
`*string` for exactly this reason
(`internal/runstate/runstate.go:129-134`): *"A pointer (not a bare string) so
'omitted' and 'explicitly empty' stay distinguishable across a round-trip."* And
a hand-written `run` records **no** tool policies at all
(`internal/runstate/runstate.go:119-121`), so inside a snapshot whose
`ToolPolicies` map is non-empty, an absent `setting_sources` is unambiguous: an
opted-in `auto` run. The answer is derivable; a second field could only ever
disagree with the argv.

It follows that the disclosure predicate reads **the policies about to be
spawned**, not the flag. One helper over `map[string]runner.ToolPolicy`,
answering "does any planned node carry the operator's config", serves both
`auto` and `resume`, and cannot drift from what is executed.

`resume` therefore inherits the choice with no new flag —
`toRunnerToolPolicies` copies `SettingSources` verbatim
(`cmd/oh-my-graph/resume.go:742`) and the recorder writes it back
(`cmd/oh-my-graph/resume.go:543`) — and **must print the same line**, in the
slot where the leg's other de-escalations already print
(`cmd/oh-my-graph/resume.go:528-530`), before the banner.

`resume` registers **no** opt-in flag, for ADR 0017 §6's reason one direction
over: a resumed leg's own flags may only de-escalate, so there must be no flag
by which a second process widens a ceiling the first leg chose. Inheriting the
first leg's choice is not `resume` choosing.

**`chat` does not get the opt-in.** `chat` builds its coordinator at
`cmd/oh-my-graph/chat.go:120` and registers no such option, so every chat-planned
node stays isolated. ADR 0030 §2.6's reasoning applies with more force here: one
`[y/N]` keystroke already covers two questions, and a flag that widens an
unattended run must be typed at a launch, not implied by a `y`.

### 2.8 What does not change

- **`internal/childenv` is untouched.** One list, no runtime branch, deleting
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY` and
  `CODEX_API_KEY` from every child (`internal/childenv/childenv.go:31-38`).
  This feature is about argv and setting sources; it is not about environment,
  and a restored config is not a restored API key. **A diff under
  `internal/childenv/` means the implementation took a wrong turn** and should
  be stopped rather than reviewed.
- **No fifth exec seam.** Exactly four objects spawn a process — `runner.CLIRunner`,
  `verify.ShellVerifier`, `worktree.GitManager`, `browser.ExecOpener`
  (`internal/childenv/childenv.go:5-13`), enforced by
  `internal/invariants/exec_seam_test.go`. This ADR adds no spawner and moves no
  spawn.
- **Enterprise/managed policy stays unbypassable.** §1.1. The opt-in's mechanism
  is the absence of `--setting-sources`; enterprise settings are unioned on top
  and were never subtractable by that flag in either direction. The disclosure
  says so, and §3(11) records why that sentence is a documentation claim rather
  than a test.
- **`run` is unchanged in every respect** — §1.3. `Options.ToolPolicies` stays
  nil there, `policyFor` keeps its early return, and
  `TestScheduler_HandWrittenGraphGetsNoCeiling` must pass untouched.
- **The graph schema is unchanged.** No new node field, no new top-level key,
  nothing new a planner may write. The opt-in exists only as an operator's argv
  and, derivatively, as a shape in `state.json`.
- **The planner and the assessor keep their stances.** The planner already runs
  unisolated (§1.4) and this flag does not reach it. The assessor keeps layer 1
  unconditionally (`internal/coordinator/assess.go:199-206`): its input is
  untrusted model output by design, so its isolation answers a different
  question and is not the operator's to trade.
- **`--dry-run` and `lint` are unchanged.** They call
  `noteCodexRuntimePolicy(..., false)` about hand-written graphs
  (`cmd/oh-my-graph/dryrun.go:58`, `cmd/oh-my-graph/lint.go:94`) and already
  tell the truth.

## 3. Tests the implementation lane owes

1. **Default is OFF, both runtimes.** An `auto` run with no flag builds
   `SettingSources = &""` and `StrictMCPConfig = true` for every planned node —
   a regression pin on today's behaviour, asserted on the policy map, not the
   argv.
2. **The opt-in's policy shape.** With the option set, every planned node has
   `SettingSources == nil` and `StrictMCPConfig == false`, while `AllowedTools`,
   `Tools` and `DisallowedTools` are byte-identical to the isolated run's for the
   same graph. The table in §2.3 is the assertion.
3. **Codex argv.** `buildArgs` with `SettingSources == nil` emits none of
   `--ignore-user-config`, `--ignore-rules`, `project_doc_max_bytes=0`,
   `mcp_servers={}` — and still emits `--sandbox <mode>` and
   `approval_policy="never"`. The second half is the point: it pins §2.6's Codex
   sentence.
4. **Claude argv.** `buildArgs` with `SettingSources == nil` and
   `StrictMCPConfig == false` emits neither `--setting-sources` nor
   `--strict-mcp-config`, and still emits `--allowedTools`, `--tools` and
   `--disallowedTools`. This is the test that makes §2.3's "exactly a
   hand-written node's config posture, exactly a planned node's tool posture"
   checkable.
5. **`WithLoadedUserConfig` forces the de-escalations.** A plan built with the
   option has no `node.Agent`, no `PluginDirs`, no `Skill` in any node's
   `Tools`, and carries the printed reason — asserted through the Option alone,
   with no CLI flags, so the guarantee is the coordinator's and not the call
   site's (§2.4).
6. **The disclosure slot says one or the other, never both, never neither.**
   Four cases: {Claude, Codex} × {off, on}. Off prints today's strings verbatim
   (§1.5); on prints §2.6's strings verbatim; the other never appears. Assert
   the whole literal, not a substring — a disclosure test that matches a prefix
   is how a sentence loses its second half.
7. **`--plan-only` prints it.** Same screen, same strings, no run minted.
8. **The goal loop's later cycles print it.** Cycle 2 of a `--max-cycles` run
   re-enters `printPlanForRuntime` via `cmd/oh-my-graph/goal.go:101`; the line
   must appear on every cycle, since every cycle plans afresh.
9. **`resume` inherits and discloses.** A snapshot with a non-empty
   `ToolPolicies` map whose entries omit `setting_sources` resumes with
   `SettingSources == nil` on the spawned argv AND prints §2.6's line before the
   banner; a snapshot with `setting_sources: ""` does neither.
10. **The resumed leg does not ERASE the choice.** After a resumed leg settles a
    node, the rewritten `state.json` still omits `setting_sources` for every
    planned node. This is the exact defect ADR 0030 hit on `build_evidence`
    (0030's header, "the resumed leg's recorder **erased** it"), on a recorder
    that writes the whole snapshot on every `RecordNode`. Write this test first.
11. **The child environment is untouched by the flag.** A spawn under an
    opted-in policy still has all four variables of §2.8 scrubbed. This is the
    guard that turns "we did not mean to touch `childenv`" into an assertion.
    (Its neighbour claim — that enterprise/managed policy survives — gets **no**
    test: no CI machine here has a managed settings file, and a test that cannot
    fail is worse than a stated claim. §8 does not require it either; it is
    inherited from the flag's absence, not from new code.)
12. **`chat` cannot reach it.** A chat-planned graph is isolated, and no chat
    surface parses the flag.
13. **`run` is untouched.** `TestScheduler_HandWrittenGraphGetsNoCeiling` passes
    unmodified.

## 4. Alternatives considered

- **NO FLAG; document `--plan-only` + `run`.** Argued in §2.1 and rejected
  because the workaround it blesses is wider than the flag it avoids, and
  because it puts the goal loop permanently out of reach for anyone whose nodes
  need their own MCP servers. It remains documented and supported; it is simply
  not the only door.
- **Codex-only.** Rejected in §2.2. It would have been the smaller diff and would
  have said, without arguing it, that the Claude ceiling is worth more than the
  Codex one.
- **Per-node opt-in in the graph schema.** Rejected in §2.1: the planner writes
  the graph, and a planner able to request the operator's settings is a sharper
  form of the hole `validatePlannedNodeAgent` exists to close.
- **Layer 1 off, layer 4 left on.** Rejected in §2.3: the disclosure would then
  claim MCP servers load while `--strict-mcp-config` sat on the argv, over an
  unmeasured E5. A disclosure whose truth depends on an unmeasured flag is not a
  disclosure.
- **Keep agent mapping and skill activation on under the opt-in.** Rejected in
  §2.4 against measurement (j): arm `X` shows the repository's same-named copy
  winning 3 of 3 under `nil`. Shipping both would leave ADR 0017's and ADR
  0022's guarantees written down and false.
- **A new `state.json` field recording the choice.** Rejected in §2.7: the policy
  map already answers the question unambiguously, and a second source of truth
  about the argv can only ever disagree with the argv.
- **Let `resume --accept-loaded-user-config` widen a leg.** Rejected in §2.7: a
  resumed leg's flags de-escalate only.

## 5. Failure modes

- **The operator reads it as pure capability.** They wanted their CLAUDE.md and
  did not notice that on Claude their standing `Bash(*)` came back with it, so a
  planned node does something its declared scope appeared to forbid. This is the
  most likely failure and the whole reason §2.6's Claude line spends a sentence
  on grants and the name spends a word on `accept`. It is mitigated, not closed.
- **The flag becomes the default in practice.** An agent that keeps hitting a
  denied tool reaches for the widest flag it can find; a human aliases it. §7
  makes this the falsifier rather than pretending a name prevents it.
- **A resumed leg widens silently.** Covered by §2.7 and tests 9 and 10; without
  the resume-side disclosure, a second process would spawn config-carrying nodes
  with nothing on screen saying so. This is the failure mode most likely to be
  missed, because the first leg's screen is long gone.
- **A repository shadows a staged skill or agent.** Prevented rather than
  disclosed, by §2.4's forced de-escalation. If a later change reopens the
  combination, measurement (j) arm `X` is the pre-written proof that it is
  wrong.
- **The `--strict-mcp-config` drop is read as a security regression.** It is a
  widening, and it is chosen; it is bounded to opted-in planned nodes and stated
  on screen. Every other node in the project keeps layer 4.

## 6. Compatibility

- **Behaviour with no flag: identical, everywhere.** Same argv, same screens,
  same `state.json`. Every existing test of the ceiling must pass unmodified —
  if one needs editing, the default moved and the change is wrong.
- **`state.json` is forward and backward readable.** `setting_sources` is
  already `omitempty` on a `*string`; an old reader sees an absent field and an
  old snapshot round-trips unchanged.
- **The graph schema and `run` are untouched**, so no saved plan, fragment or
  template needs a change and no `graphs/*.yaml` in this repository is affected.
- **Documentation surfaces owed by the implementation lane** (not by this node):
  README.md:194-203 — the "One boundary to read before you trust it" paragraph
  whose sentence *"Codex discards user config, project rules/AGENTS files and MCP
  servers for planned nodes"* (README.md:198-199) becomes conditional and must
  say so on both runtimes; SECURITY.md's layer table (SECURITY.md:220) and its
  layer-4 note (SECURITY.md:327); DESIGN.md's ceiling section (DESIGN.md:2380-2391);
  `docs/LIMITATIONS.md`; and a `## [Unreleased]` CHANGELOG entry. No release, no
  tag, no version bump belongs to this decision.

## 7. Falsification

This decision is wrong if any of these is observed.

1. **The residual layers do not survive restored settings.** If a measurement
   shows that with settings loaded a node's `--tools` narrowing or its
   `--disallowedTools` also stops binding, then the opted-in node is not
   narrower than §2.1's `--plan-only` + `run` workaround, and the flag's entire
   justification collapses — it would then be a second spelling of a door that
   already exists, and should be deleted in favour of documenting the first one.
   This is §8's required measurement.
2. **It becomes the normal way to run `auto`.** Concretely: the flag appears in
   any `graphs/*.yaml` lane's documented invocation, in the README quickstart,
   or in this repository's own dogfooding commands. That would mean the ceiling
   is off by default in practice, and the honest response would be to renegotiate
   the ceiling in the open rather than keep a default nobody uses.
3. **An issue reports an opted-in node doing something the operator believed
   `allowed_tools` forbade.** The name and §2.6's Claude sentence failed to
   carry the grants half of the bill; the disclosure, not the mechanism, is the
   defect.
4. **Nobody uses it, and operators keep being pointed at `--plan-only` + `run`.**
   The flag bought nothing and should be removed rather than maintained.
5. **A request arrives for per-node granularity that §2.1's reasoning cannot
   answer** — e.g. one node legitimately needing an MCP server while the rest of
   the run must stay isolated. Whole-run granularity would then be the wrong
   unit, and the untrusted-planner argument would need a mechanism (an operator
   annotation applied after validation) rather than a refusal.

## 8. Required measurement before Accepted

One arm, on both runtimes, and it is falsifier 1:

> A planned node whose `--tools` omits `Write` and whose `--disallowedTools`
> names `Bash`, run with `SettingSources = nil` under a user settings file
> granting `Bash(*)` and `Write`, asked to do both. **Expected:** neither runs —
> layers 3 and 5 bind independently of settings — while the same node's
> `Bash(git *)` scope is observably NOT enforced, which is the bill §2.6
> discloses.

Recorded under `docs/measurements/` with argv committed, pre-registered
outcomes, and 3 spawns per arm, matching the shape of measurement (j). Until it
exists, §2.3's central claim is a projection and this ADR stays **Proposed**.

## 9. What could not be determined

- **Whether Codex's restored `~/.codex/config.toml` can weaken the sandbox
  floor.** `--sandbox` and `--config approval_policy="never"` are on the argv
  and argv beats config file, which is why §2.6's Codex line says what it says;
  but no measurement in this repository has fed a hostile `config.toml` to that
  argv. The claim is read from `codex_protocol.go:29-35`, not observed.
- **Whether `--strict-mcp-config` closes MCP at all** — E5, unmeasured
  (DESIGN.md:2919-2923). §2.3 routes around the question rather than answering
  it; dropping the flag makes the disclosure true either way.
- **What Claude's default source set is when `--setting-sources` is omitted.**
  This ADR asserts only what this repository can warrant: the flag is absent,
  which is exactly what the planner call (§1.4) and every hand-written `run`
  node (§1.3) already get, and what
  `TestScheduler_HandWrittenGraphGetsNoCeiling` describes as *"the user's own
  settings, hooks, MCP servers and tool permissions"*. The precise set is the
  CLI's to define and may change under it.
