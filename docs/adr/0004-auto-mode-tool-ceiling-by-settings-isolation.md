# ADR 0004 — Auto mode's tool ceiling is settings-source isolation, and planned-node fields are deny-by-default

- Status: Accepted — the ceiling (§1) and the field-disposition rule (§2) are
  implemented and empirically confirmed; see "Measurement outcome" below
- Date: 2026-07-29
- Issues: [#11](https://github.com/jitokim/oh-my-graph/issues/11),
  [PR #6](https://github.com/jitokim/oh-my-graph/pull/6)

## Context

PR #5 gave auto mode a real execution ceiling: `coordinator.deniableTools` +
`Plan.DisallowedTools` → `--disallowedTools`. It correctly stops a user's
standing `Bash(*)`/`Write(*)`/`WebFetch(*)` grant from reaching a tool an
unreviewed planned node never declared. It cannot close one gap, disclosed
honestly in README.md, SECURITY.md and DESIGN.md today:

> a node declaring any scoped `Bash(...)` pattern keeps the **whole** `Bash`
> tool, because a deny cannot express "all Bash except these prefixes"

Two related gaps ride along: `mcp__<server>__<tool>` names are not enumerable in
a deny list, and settings *hooks* are not tool calls at all, so a write-capable
planned node can drop a `.claude/settings.local.json` for a later node to load.

DESIGN.md named `--tools` and `--strict-mcp-config` as the structurally better
primitives. Reading the shipped claude 2.1.220 binary's own code showed the
actual mechanism is a third flag, and that the model was misdiagnosed:

- Under `--permission-mode dontAsk`, a tool call whose rule evaluation lands on
  *ask* is converted to `{behavior: "deny", decisionReason: {type: "mode", mode:
  "dontAsk"}}`. **The CLI is already default-deny for our unattended nodes.**
- `--allowedTools` therefore failed to bind not because of the flag, but because
  the user's `~/.claude/settings.json` is loaded as *another rule source* whose
  `Bash(*)` matches first.
- `--setting-sources` selects which of `user`/`project`/`local` settings load.
  `""` yields none. `--settings` (flag settings) and enterprise policy settings
  are unioned on top and cannot be dropped.

Separately, PR #6 adds a node-level `agent:` field (`claude -p --agent <name>`).
The architecture review of PR #5 already flagged that `validatePlannedNodes` has
no `Agent` case, so a planned node could set `agent:` and inherit an arbitrary
subagent's system prompt, tools and model — routing around the whole guard.

## Decision

### 1. The ceiling becomes a layered policy, carried as one value object

`Plan.DisallowedTools map[string][]string` becomes
`Plan.ToolPolicies map[string]runner.ToolPolicy`, handed to
`schedule.Options.ToolPolicies` and rendered by the runner:

```go
type ToolPolicy struct {
	AllowedTools    []string // --allowedTools
	DisallowedTools []string // --disallowedTools
	Tools           []string // --tools  (nil = flag omitted)
	SettingSources  *string  // --setting-sources ("" = load none; nil = flag omitted)
	StrictMCPConfig bool     // --strict-mcp-config
}
```

One object, one map, so a caller cannot pass three quarters of a ceiling. The
layers, for coordinator-planned nodes only:

| layer | mechanism | closes |
|---|---|---|
| 0 declaration | `plannedToolAllowlist`, plan-time rejection | a plan asking for `Bash(*)` |
| 1 **isolation** | `--setting-sources ""` | the user's standing grants; settings hooks |
| 2 grant | `--allowedTools` + `dontAsk` default-deny | **scoped Bash** |
| 3 narrowing | `--tools "<bare names declared>"` | tools the model can even attempt |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` (PR #5's list, **retained**) | anything above that is wrong |

Layer 1 is the load-bearing change; layer 5 is why shipping it is safe before
every empirical question is answered, because a wrong assumption in layers 1–4
degrades to today's behaviour rather than to nothing.

Hand-written graphs get layer 2 only — a node's declared `allowed_tools` is
still rendered as `--allowedTools` — and **none** of layers 1, 3, 4 and 5. They
are the user's own reviewed artifact and are *meant* to run under the user's
settings, hooks and MCP servers.

> **Update (2026-08-06):** amended in part by ADR 0016. Layer 0's
> `plannedToolAllowlist` is **unchanged**, and the decision above stands — but
> what that list is *for* is now narrowed and stated out loud: it answers only
> "what class of tool is safe for unattended planner output at all", never
> "which build command this repository needs". [#119] is the cost of the
> conflation: on a Kotlin repo no planned node can name `./gradlew`, so `auto`
> planned a verify node that checked branch and commits, replied PASS, and
> certified a branch that did not compile. ADR 0016 **rejects extending this
> list** (the entries are this repository's own toolchain, a fixed list is
> never complete, and a superset loosens the ceiling for every user) and
> routes build evidence elsewhere: a user-supplied command the ENGINE runs,
> injected after validation, granting the node nothing and leaving layers 0–5
> byte-for-byte as they are. It also restates §1's load-bearing property in
> wider terms — the validated set must be influenced by **no untrusted
> producer**, the repository included, not merely by the planner — which is
> why repo detection is admitted as a printed suggestion and refused as a
> grant. See
> `0016-build-evidence-is-a-user-supplied-engine-command.md`.

> **Update (2026-08-07, superseded by the note below):** amended in part by
> ADR 0017, for layers **1** and
> **3**, on coordinator-planned nodes only, and only when the user has a skill
> corpus at all. The decision above stands and layers 0, 4 and 5 are
> untouched — what moves is the *value* of two rows in the table. #130
> decomposed for the first time what this ADR's E-series measured only as a
> composite: layer 1 withholds the skill **definitions** and layer 3 withholds
> the `Skill` **tool**, independently, so restoring Claude Code's own skill
> activation needs both (measured, claude 2.1.223, with a probe that makes the
> model actually invoke a planted skill rather than report on its own
> visibility). ADR 0017 relaxes layer 1 from `""` to the explicit value
> `"user"` and appends `Skill` to layer 3. **Read ADR 0017's Status first: as of
> the 2026-08-07 review its layer-1 clause is NOT authorized, because the claim
> that this ADR's ceiling survives the relaxation was retracted.** The
> corrected accounting:
> **E4 survives and is re-confirmed, but only for what it says:** with the
> measuring machine's `settings.json` granting `Bash(*)`, a node whose
> `--tools` omits `Bash` still reports `NO-BASH-TOOL` — an allow-rule approves
> a tool that exists, it does not create one. **E1 does NOT survive the layer-1
> relaxation, and an earlier version of this annotation wrongly left it
> standing.** E1 measured this ADR's actual node shape — a node declaring
> `Bash(git *)` whose out-of-scope `touch` **ran** without layer 1 and was
> **denied** with it. `--tools` bounds tool *names*, not *scopes*, so relaxing
> layer 1 makes the user's `Bash(*)` a live allow rule beside the node's
> narrower one and layer 2 stops binding — which is why layer 2 *"used to be a
> declaration rather than a limit"* (`toolPolicyFor`). ADR 0017's probe did not
> test that shape; re-testing it is its blocking measurement (g). Until (g)
> reports, **treat this ADR's ceiling as intact and ADR 0017's layer-1 route as
> unauthorized.** Consequences/Positive #1 ("a real ceiling, not a
> declaration") therefore also stands unamended for now. **Layer 0 survives
> untouched:**
> `Skill` is injected into the *policy* after validation by trusted code,
> never into `plannedToolAllowlist`, so a planner still cannot name it — the
> ADR 0016 §2 posture. **The hooks-gap closure in Consequences survives**,
> because `"user"` is not `nil`: the `project` and `local` sources stay
> unloaded, so a write-capable node still cannot plant a
> `.claude/settings.local.json` for a later node to load. What does **not**
> survive is the first bullet of "Negative / trade-offs": a planned node no
> longer loses the user's CLAUDE.md, and user hooks now load (whether they
> *fire*, and whether a `PreToolUse`/`PermissionRequest` hook can **approve a
> call `--allowedTools` denies**, is unmeasured and is ADR 0017's measurement
> (a)). **E3's model pin also stops holding:** a `settings.json` setting
> `model:` now applies to every planned node, where E3 measured it as *not*
> applied under `""`. **MCP: E5's "NOT MEASURED" stands.** An earlier version
> of this annotation said `--strict-mcp-config` *"was measured to hold"*; it was
> not — the measuring machine has no user-scoped MCP servers, so there was no
> positive control, and the result was a self-report.
> Reversible with one flag on `auto` **and on `resume`**:
> `--no-skill-activation` restores this ceiling bit-for-bit (ADR 0017's first
> draft omitted the resume half, which made the reversibility claim false for
> every resumed leg). See
> `0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md`.

> **Update (2026-08-07, later the same day) — the note above is superseded;
> layer 1 is NOT amended.** ADR 0017's two blocking measurements were taken and
> the layer-1 route died on the first. **(g)** re-ran **E1** under ADR 0017's
> own argv — a node declaring `Bash(git *)`, `--setting-sources user`,
> `--allowedTools "Bash(git *)"`, `--tools Read,Bash`, `dontAsk`,
> `--strict-mcp-config` — attempting an out-of-scope `touch`, judged by whether
> the file appeared. **It appeared.** The identical probe under
> `--setting-sources ""` was denied and the file was absent. So **E1 stands
> unamended and this ADR's layer 1 is untouched**: `--tools` bounds tool
> *names*, not *scopes*, and layer 1 is the only thing holding a declared
> `Bash(git *)` to git while the user's `settings.json` grants `Bash(*)`.
>
> **(f)** found the definitions elsewhere: `--plugin-dir <dir>` — a
> `.claude-plugin/plugin.json` plus `skills/<name>/SKILL.md` — loads a staged
> skill corpus with layer 1 left at `""`. Measured with positive controls on
> claude 2.1.223: the skill fires (with `--tools Skill` and no `Read`, so the
> planted token's only provenance is the `Skill` tool); an out-of-scope `touch`
> is still **denied**, visible in the CLI's own `permission_denials`; the
> user's `CLAUDE.md` is **absent** (control: `--setting-sources user` quotes it
> back); MCP is **absent** (control: `--setting-sources user` without
> `--strict-mcp-config` lists 14 `mcp__*` tools). One isolation worth
> recording: `--plugin-dir` + `--setting-sources ""` *without*
> `--strict-mcp-config` also yields no MCP, so **layer 1 is what bounds MCP for
> a planned node and layer 4 is redundant here** — it stays, because the layers
> are deliberately independent mechanisms, but it should not be credited with
> layer 1's work.
>
> **The corrected accounting for this ADR.** Amended in part by ADR 0017, for
> **layer 3 only**: `Skill` is appended to `--tools` on coordinator-planned
> nodes, and only when the user has a skill corpus at all. Layers **0, 1, 2, 4
> and 5 are byte-for-byte unchanged**, so **E1, E3, E4, E5 and E7 all stand as
> written** — including E3's model pin (no `settings.json` loads, so no `model:`
> key applies) and layer 1's settings-hook closure (*"writing a
> `.claude/settings.local.json` into the invocation directory achieves
> nothing"*). The first bullet of "Negative / trade-offs" **also stands**: a
> planned node still loses the user's CLAUDE.md, hooks and MCP servers. Layer 3
> is still a real ceiling row, re-measured: `--plugin-dir` with `--tools Read`
> and no `Skill` yields no skill. **Layer 0 survives untouched:** `Skill` is
> injected into the *policy* after validation by trusted code, never into
> `plannedToolAllowlist`, so a planner still cannot name it. Reversible with one
> flag on `auto` **and on `resume`**: `--no-skill-activation`.
>
> One thing this ADR should carry forward rather than leave to ADR 0017: (g)
> gives `applyAgentMapping`'s `SettingSources = nil` a measured number instead
> of a suspicion. An **agent-mapped** node declaring `Bash(git *)` is the exact
> shape (g) breached, and it loads *user, project and local* settings rather
> than just user. That gap is live in the tree today and is filed as its own
> issue with its own measurement.

### 2. Every `graph.Node` field has an explicit planned-node disposition

`agent:` and `success_check.verify:` are **rejected** in `validatePlannedNodes`,
joining `bypassPermissions`, `cwd` and `type: gate`.

The general rule, because this hole recurs every time the schema grows: **every
field on `graph.Node` must have an explicit disposition in
`validatePlannedNodes` — allowed, constrained, or rejected.** Adding a field
without adding a case is a review-blocking defect. A table-driven test over
`reflect.VisibleFields(reflect.TypeOf(graph.Node{}))` that fails on any field
name the coordinator has no recorded disposition for turns that from a
convention into a build failure.

> **Update (2026-08-06):** ADR 0016 changes one recorded disposition in
> substance without changing this rule or the rejection itself.
> `success_check.verify` stays **rejected** when it is planner-authored —
> `validatePlannedNodeVerify` is untouched — and gains a second clause: it may
> be set by trusted code *after* validation, from a command string the user
> supplied at invocation, the same posture `agentmap.go` and ADR 0012's skill
> inlining already take. The `why` recorded in `field_dispositions_test.go`
> must say so, exactly as ADR 0012 §5 owed for `Prompt`.

### 3. `agent:` is not reconciled, and that non-claim is stated

For hand-written graphs, `agent:` works as PR #6 built it. oh-my-graph does not
parse the subagent's frontmatter and makes **no claim** about whether the
subagent's own `tools:` unions with, overrides, or loses to the node's
`allowed_tools` — that is the CLI's precedence and it has not been measured
(DESIGN.md, E6). For a hand-written graph this is a usability question: the graph
and the agent file are both the user's own artifacts. For a planned graph it
would be a safety question, and the answer there is rejection.

### 4. Coordinator auto-mapping of `agent:` is deferred, not merely unbuilt

PR #6 imagined the coordinator scanning `~/.claude/agents` and auto-assigning
`agent:` by role. It falls out as impossible: a planned node may not carry the
field. Re-entry requires all three of — (a) E6 measured, (b) a CLI-side mechanism
that bounds a resolved subagent's tools, and (c) the mapping source being an
explicit opt-in such as `--agent-map review=code-reviewer`. The implicit scan is
rejected permanently: it would make an `auto` run's behaviour depend on files the
user forgot they had.

> **Update (2026-08-02):** superseded in part. Auto-mapping shipped
> (`internal/coordinator/agentmap.go`) once (a) and (b) held for the mapped
> configuration: a mapped node runs `--agent` plus the full `--tools` ceiling —
> exactly E6's measured setup, where frontmatter tools did not widen past
> `--tools` — and the coordinator additionally refuses agents whose frontmatter
> exceeds the node's `allowed_tools`. Condition (c) was relaxed from an explicit
> opt-in flag to printed disclosure of every mapping in the plan output plus a
> `--no-agent-mapping` opt-out; the mapped node drops Layer 1 (E2) and the
> printout says so. See DESIGN.md, "Node-as-subagent".
>
> **Update (2026-08-03):** ADR 0012 re-opens the same "no implicit scan"
> clause for the user's skills (`~/.claude/skills`), under the same
> relaxation with stronger disclosure: plan-time inlining of SKILL.md bodies
> into matching planned nodes' prompts, every decision printed — each mapping
> with its inlined size and SHA-256 prefix, each refusal with its reason — a
> 16 KiB skip-not-truncate cap, agent-mapped nodes excluded, and
> `--no-skill-mapping` as the opt-out. See
> `0012-skill-mapping-is-plan-time-inlining.md`.

## Measurement outcome (added at implementation, claude 2.1.220)

The decision above was taken from reading the shipped binary. Before shipping,
the six open questions were put to a real CLI on a machine whose settings grant
`Bash(*)`. Full detail in DESIGN.md, "Empirical verification of the tool
ceiling"; what it changed here:

- **E3, the gate on the whole ADR — PASSED.** `--setting-sources ""` does not
  affect subscription OAuth. With both billing-switching variables absent from
  the environment, an isolated `claude -p` returned a normal envelope
  (`provider: "firstParty"`); with no API key to fall back to, it can only have
  resolved OAuth. Had this failed, the decision would have been abandoned and
  the deny-list-only ceiling kept.
- **E1 — CONFIRMED.** The headline claim holds end to end: identical node
  declaration, out-of-scope shell command allowed without Layer 1 and denied
  with it, in-scope `git` still working. Evidence was the filesystem and the
  envelope's `permission_denials`, not the model's narration.
- **E4 — CONFIRMED, with a correction to §1's wording.** `--tools` REPLACES the
  built-in set; a tool omitted from it is unavailable even when `--allowedTools`
  names it. Layer 3 is therefore an intersection with Layer 2, not an additive
  narrowing, and must enumerate every tool the node needs. It does.
- **E2 — ANSWERED, and it reinforces §4.** `--setting-sources ""` also disables
  discovery of `~/.claude/agents`, so Layer 1 and `agent:` are mutually
  exclusive. Auto-mapping of `agent:` was already impossible because a planned
  node may not carry the field; this is a second, independent reason.
- **E6 — MEASURED IN A CONFIGURATION THIS TOOL NEVER EMITS, so §3's non-claim
  STANDS and got stronger.** A subagent's frontmatter `tools:` did not widen
  past `--tools` — but `--tools` is emitted only by auto mode, which rejects
  `agent:`, so the result does not cover the hand-written path where `agent:` is
  legal. There is no measured tool bound for the real case, and the docs now say
  so plainly rather than citing E6 as partial reassurance.
- **E5 — NOT MEASURED.** No MCP server was available to test Layer 4 against.
  `--strict-mcp-config` ships because it is free, and SECURITY.md/README say
  explicitly that MCP closure is unverified rather than implying coverage.
- **E7 (new, not in the original list) — CONFIRMED.** `--setting-sources ""`
  drops the project CLAUDE.md, measured with a codeword file. Added because a
  design decision leans on it: the planner call is deliberately NOT isolated so
  that it keeps the user's CLAUDE.md, and that would have been an unverified
  premise. Hooks follow from V1 (they live in the settings files that are not
  loaded) rather than being an independent claim.

One thing the design did not anticipate, found while finishing PR #6: the claim
that an unresolvable `--agent` falls back to plain claude is **false**. The CLI
exits 1 having written nothing to stdout and its complaint to stderr. The
implementation keeps the failure (a node silently running as generic claude
instead of the reviewer the graph named is a different node) and surfaces the
CLI's stderr in `runner.NodeOutputError`, since that message lists the agents
that do exist. See DESIGN.md, "Node-as-subagent".

## Consequences

**Positive**

- The headline gap closes. With no user settings loaded, `--allowedTools
  "Bash(git *)"` under `dontAsk` means *git and nothing else* — a real ceiling,
  not a declaration.
- The hooks gap closes as a side effect: writing `.claude/settings.local.json`
  achieves nothing when no node in this run, or any later `auto` run, loads local
  settings.
- The MCP gap closes via `--strict-mcp-config` with no `--mcp-config`.
- Enterprise policy settings still load and still cannot be escaped, so this
  cannot be used to step around a corporate policy.
- The field-disposition rule converts a recurring class of hole (`agent:` today,
  `verify:` tomorrow) into a compile-time-adjacent guard, instead of relying on a
  reviewer remembering.

**Negative / trade-offs**

- Planned nodes become **more isolated and less capable**: no user CLAUDE.md, no
  user hooks, no configured MCP servers. That is the intended direction, but it
  is a real behaviour change for anyone whose `auto` runs currently depend on
  their MCP servers, and it must be in README rather than discovered.
- The claim rests on behaviour read out of a minified binary, not on a published
  contract. It is more reliable than `--help` prose (which PR #5 already proved
  wrong once) but it is version-coupled: a CLI update could change it. ADR 0001
  already accepts that coupling; this widens the surface.
- On a machine with `allowManagedPermissionRulesOnly`, `--allowedTools` rules are
  ignored entirely and the ceiling is the managed policy, not ours.
- Refactoring `NodeInvocation` into `ToolPolicy` touches `runner`, `schedule`,
  `coordinator` and `cmd` in one PR, on files another workstream is editing.
- `agent:` ships useful only for hand-written graphs, so PR #6's headline payoff
  — the coordinator picking *your* `code-reviewer` — does not arrive in v1.1.

## Alternatives considered

- **Adopt `--tools` alone, as issue #11 proposed.** Rejected as the primary
  mechanism: `--tools` narrows the *tool set*, while "Bash but only `git *`" is a
  *permission rule* question. `--tools Bash` still leaves the user's `Bash(*)`
  allow rule matching every command. Kept as layer 3, defence in depth.
- **Keep extending `deniableTools`.** Rejected: it is an enumeration over an open
  set. It can never express "all Bash except these prefixes", and every new
  built-in or MCP tool silently widens the hole.
- **Inject a `--settings` JSON policy with explicit deny rules.** Rejected for
  v1.1: it needs a temp file or inline JSON per node, and it is unnecessary once
  the competing rule sources are simply not loaded. The flag remains available if
  a future need arises — flag settings survive `--setting-sources ""`.
- **Parse the subagent's frontmatter and intersect its tools with the node's.**
  Rejected: it re-implements the CLI's own resolution, which will drift, and it
  would be built on an unmeasured precedence — exactly the mistake PR #5 made
  once already.
- **Apply layers 1–4 to hand-written graphs too, for uniformity.** Rejected: it
  would silently disable the user's own settings, hooks and MCP servers in the
  path whose entire purpose is precise user control.
