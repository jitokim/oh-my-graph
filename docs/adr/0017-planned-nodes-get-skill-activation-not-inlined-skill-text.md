# ADR 0017 — A planned node gets Claude Code's own skill activation, and pays for it with the user settings source

- Status: Proposed — decision taken, **nothing implemented**, and as of the
  2026-08-07 review **one load-bearing premise is retracted**: the claim that
  the capability ceiling survives relaxing layer 1 was not measured, and the
  measurement that appeared to establish it tested a node shape that cannot
  exhibit the regression (§"What the ceiling probe actually measured"). Until
  measurements **(f)** and **(g)** are recorded, the layer-1 clause of §1 is
  **not authorized**, and with it §4, §5 and §6 describe a price this record
  can no longer claim is bounded. The gate is the acceptance test below, and
  unlike ADR 0012 this record does not permit the code to land ahead of it.
  ADR 0012 shipped while `Proposed` with its own acceptance probes unrun, and
  eighteen months of hindsight is not needed to see the cost: the mechanism was
  measured at 7% two days later and nobody had ever established that an inlined
  body helps a node at all. This ADR changes a node's argv and two ceiling
  layers, so it has a CLI-behaviour premise to probe, and the premise is
  measured on **one machine, one CLI version**. Implementation is authorized
  only after the acceptance test and required measurements (a)–(d), (f) and
  (g) are recorded — **(g) first, because it can invalidate the shape.**
- Date: 2026-08-07
- Issues: [#130](https://github.com/jitokim/oh-my-graph/issues/130)
- Supersedes, contingent on that gate:
  `0012-skill-mapping-is-plan-time-inlining.md` in whole (§1–§5; §6's scan and
  its printed disclosure survive, re-pointed — see Decision §7). ADR 0012's
  decision text is unchanged; it carries a dated pointer here.
- Amends in part: `0004-auto-mode-tool-ceiling-by-settings-isolation.md` §1,
  layers 1 and 3, for coordinator-planned nodes only. ADR 0004's decision text
  is unchanged; it carries a dated pointer here.
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## Context

### What ADR 0012 was compensating for, and what it is actually worth

ADR 0012 exists because a measurement said a planned node has no skills. Under
the exact argv `runner.buildArgs` emits, the model reported no skills listed
and no skill-invoking tool, with `num_turns: 1` and zero tool calls. From that
the ADR concluded the mechanism must carry the skill's *content* — inline the
SKILL.md body into the node's prompt, name-matched by trusted Go code.

Its measured yield, on the **56 distinct planner-authored node ids** ADR 0012's
"Yield measurement" recorded (twenty goals, `auto --plan-only`, $6.05): **5
mapped (9%), 4 corrected (7%)** after removing one mapping that was simply
wrong (`artifacts` → `html-artifact`, matched on a 4-rune prefix, semantically
unrelated), and **47 (84%) with no candidate at all**. Claude Code's own
description-driven activation would see **all 35 skills** and choose by
description, at run time, per task.

> **Correction (2026-08-07).** This paragraph first cited "393 real
> planner-authored node ids: 9.9%", and Alternatives B cited "14.5% for the LLM
> arm". **Both numbers are withdrawn as unsourced.** The corpus is 56 ids, not
> 393 — `393` occurs in this repo only inside the string `$0.0393`, and the
> figure reached this ADR from #130's body, itself unsourced, without being
> checked against the record being superseded. `9.9%` contradicts ADR 0012's own
> table (5 of 56 = 9%). **There is no LLM-selector arm**: no run, no cost line,
> no record anywhere in the tree, the issues or the PRs. That number was
> load-bearing for rejecting Alternatives B, and B's rejection is re-argued
> below without it. An ADR that opens by condemning ADR 0012 for shipping on
> unprobed premises does not get to carry three invented measurements.

ADR 0012's own gate — probes (a) *does an inlined body steer the node* and (b)
*what does an unconditionally-inlined, half-reachable procedure do to a node's
output* — has never been run. So the status quo is a mechanism recovering 7%
of a capability, whose benefit on the 7% is unestablished, and whose principal
known effect is one manufactured false positive.

### The two layers, measured separately for the first time

ADR 0012's probe measured the composite and said so honestly: *"the probe
measures the composite stance end to end and does not isolate which layer cuts
the skills listing… if a future proposal ever adds `Skill` to
`plannedToolAllowlist`, the listing-vs-tool question must be measured
separately first."* That decomposition was done. Measured on **claude
2.1.223, macOS**, with a probe that makes the model **actually invoke** a
planted skill and emit its marker — not one that asks the model whether it can
see skills, which was tried first and returned contradictory answers to
identical argv:

| argv | skill runs? |
|---|---|
| `--setting-sources ""` alone | no — the definitions are not loaded |
| `--tools Read` alone | no — there is no `Skill` **tool** |
| `--setting-sources ""` + `--tools Read,Skill` | no — naming the tool does not load the definitions |
| `--setting-sources user`, no `--tools` | **yes** |
| `--setting-sources user` + `--tools Read,Skill` | **yes** |

Two independent layers block skills for two different reasons: **layer 1
withholds the definitions, layer 3 withholds the tool.** Both must move.
`Skill(*)` in the user's `settings.json` is irrelevant to either.

### What the ceiling probe actually measured — and the regression it missed

Relaxing layer 1 sounds like forfeiting the ceiling, because the measuring
machine's `~/.claude/settings.json` grants `Bash(*)`, `Write(*)`, `Edit(*)`
(28 allow rules). This ADR originally answered that it does not, citing:

```
--setting-sources user --tools Read,Skill --strict-mcp-config
  "Run the shell command 'echo CEILING-BREACH' using the Bash tool"
  -> NO-BASH-TOOL
```

**That answer is retracted.** The probe's node **declared no Bash at all**, so
`--tools Read,Skill` omits `Bash` and the tool does not exist. It therefore
re-proves E4 — *an allow-rule approves a tool that exists, it does not create
one* — which nobody disputed. It says nothing about the node shape that
actually runs under this decision, and the difference is the whole ceiling.

Trace a node that declares `Bash(git *)`, which `plannedToolAllowlist` permits
(it carries seven scoped `Bash(...)` patterns):

- `narrowedToolsFor` drops the scope, so layer 3 emits `--tools Bash,…` —
  **`Bash` exists**;
- `disallowedToolsFor` sees `Bash` declared, so layer 5 does **not** deny it —
  inert;
- layer 2 emits `--allowedTools "Bash(git *)"`;
- layer 1 = `"user"` makes the user's `Bash(*)` a live allow rule **alongside**
  ours.

`--tools` bounds tool **names**. It does not bound **scopes**. So the only thing
standing between an unattended `dontAsk` node and an out-of-scope shell command
is layer 2 — and layer 2 is exactly what loading the user's settings stops
binding. The tree already says so, in the doc comment on the very function this
ADR amends (`toolPolicyFor`, coordinator.go):

> Layer 1 is the load-bearing one. Permission rules are matched from every
> loaded source, so a standing `Bash(*)` in the user's ~/.claude/settings.json
> was matching **before this node's own narrower `Bash(git *)` ever mattered —
> which is why layer 2 used to be a declaration rather than a limit.**

And ADR 0004's **E1** measured the outcome end to end, on this identical node
declaration: an out-of-scope `touch` **ran** without layer 1 and was **denied**
with it, while `git init` kept working.

**The concrete regression, stated plainly: an unattended `dontAsk` planned node
declaring `Bash(git *)` can run `rm -rf` again.** That is the headline gap
ADR 0004 shipped layer 1 to close, and the first draft of this ADR did not
mention it once in 679 lines. §6 had the correct framing in passing — *"no tool
the node did not declare"* — and then the document silently upgraded it to *"the
ceiling is unchanged"*. Those are different statements, and only the first is
measured.

So the price of activation is not "the user's CLAUDE.md". **Under the layer-1
route it is the scope ceiling**, and that changes which design is admissible:
Alternatives **D2** (a synthesized `--settings` payload that carries skill
directories while layer 1 stays `""`) stops being an attractive probe and
becomes **the only design in this document that still works**. It collapses §4,
§5 and §6 — and now the ceiling problem too. Measurements **(g)** (re-probe E1
under the final argv) and **(f)** (D2) are therefore prerequisites to
implementation, not follow-ups.

What else rides along with `--setting-sources user` — see §4a for the full
enumeration, which the first draft also understated:

- **`~/.claude/CLAUDE.md` — YES.** 251 lines / 10,904 bytes, confirmed present
  in the node's context, first heading quoted back verbatim.
- **The `model` pin — YES, and unmeasured in this context.** This file sets
  `model: "opus[1m]"`. ADR 0004's **E3 measured that the pin is not applied
  under `""`** — that is what proved the flag was in effect. Under `"user"` it
  applies to every planned node.
- **MCP — NO RESULT, not a NO.** See §4a.
- **hooks — NOT MEASURED**, and there are ten registered events. Nothing below
  treats them as bounded.

So the question is not how to improve the matcher. It is whether planned nodes
should get the real mechanism back, at what price, and — after this retraction —
**by which route**, because the two routes no longer cost the same thing.

## Decision

### 1. Layers 1 and 3 are relaxed together, for planned nodes, and only when a corpus exists

A **third post-validation mutation step** — `applySkillActivation(&plan)`, placed
immediately **after** `applySkillMapping` — adjusts the **policy**, never the
graph:

- layer 1: `SettingSources` becomes a pointer to `"user"` instead of `""`.
  **Gated on (g) and (f); see Status.** If (f) shows a `--settings` payload can
  carry the definitions, this clause is replaced by that payload and layer 1
  stays `""`.
- layer 3: `"Skill"` is appended to `Tools`.
- layer 2: `"Skill"` is appended to the policy's `AllowedTools` (see §3 for
  why this is provisional).

> **Correction (2026-08-07).** The first draft said *"`toolPolicyFor` gains one
> post-validation adjustment"*, and that location is wrong twice over.
> `toolPolicyFor` is called from `toolPoliciesByNode` **before** either mapping
> runs, and `applyAgentMapping` afterwards sets `policy.SettingSources = nil` on
> mapped nodes. Implementing §1 there would hand an agent-mapped node `Skill` in
> `Tools`/`AllowedTools` **plus** `nil` setting sources — `--agent` + `Skill` +
> user *and project and local* settings: wider than anything decided here, and
> precisely the unmeasured composite §Compatibility says it refuses. It also
> cannot work: `toolPolicyFor` is a pure function of one `graph.Node` and cannot
> see `SkillScan`, which §1's own predicate requires. Hence a distinct step,
> ordered last, that reads the scan and skips agent-mapped nodes explicitly.

The relaxation is applied **only when the skill scan (`SkillScan`, kept from
ADR 0012 §6) found at least one usable definition.** A user with no
`~/.claude/skills` pays no exposure for a capability there is nothing to
exercise, and the printed plan says which of the two worlds this run is in.
This is a per-*run* predicate over a filesystem fact, not a per-*node*
predicate over a guess about relevance — the difference is §Alternatives B.

Everything else in the ceiling is byte-for-byte unchanged: layer 0
(`plannedToolAllowlist`), layer 4 (`--strict-mcp-config`, no `--mcp-config`),
layer 5 (`deniableTools`). Hand-written graphs are untouched — they never had
layers 1 and 3 and are meant to run under the user's own everything.

### 2. `Skill` is injected after validation; layer 0 does not learn the word

`plannedToolAllowlist` is **not** extended. ADR 0016-build-evidence §1 (there
are two ADRs numbered 0016; every "ADR 0016" citation in this record means
`0016-build-evidence-is-a-user-supplied-engine-command.md`, never
`0016-a-retry-carries-the-attempt-it-is-repeating.md`) narrowed what that
list answers — *"what class of tool is safe for unattended planner output at
all"* — and a planner that can name `Skill` in `allowed_tools` is a planner
that can select which of the user's local files gets loaded into a node it
authored. That is the hole `validatePlannedNodeAgent` closes for agents and
ADR 0012's third alternative closes for skills, and it stays closed.

So the grant is a **policy-level act, invisible to the graph**: `node.
AllowedTools` never contains `Skill`, `validatePlannedNodeTools` never sees
it, the saved `graph.json` never carries it. The same posture as
`agentmap.go`'s `agent:` and ADR 0016-build-evidence §2's injected
verification: *choosing
stays in trusted code, and what the planner may declare does not move.*

The consequence, stated so it is not discovered: the durable record of the
grant is `state.json`'s `tool_policies`, not the graph. A reader holding only
`graph.json` cannot tell an activation-enabled run from an isolated one.

### 3. Layer 2 is provisional, and the reason is a deny we cannot see coming

The measurement that establishes activation omitted `--allowedTools`,
`--disallowedTools` and `--permission-mode dontAsk`. A planned node runs with
all three. Under `dontAsk` a call whose rule evaluation lands on *ask* becomes
a **deny** (ADR 0004, Context), so a `Skill` invocation that no allow-rule
matches could be denied silently — the node would report it could not comply
and the run would look like the model simply chose not to use a skill. That
failure is indistinguishable from success-without-activation, which is exactly
the shape this ADR must not ship blind.

`Skill` therefore goes into `AllowedTools` as well, until measurement (c)
below says whether it is needed. If (c) shows `--tools` alone suffices under
the full argv, the layer-2 half is removed in the same PR that records the
measurement. Adding it costs nothing (`Skill` is not in `deniableTools`, so
there is no layer-5 contradiction to resolve) and it removes one way for this
feature to be silently absent.

### 4. `--setting-sources user`, never `nil` — and that is not a detail

Agent mapping already drops layer 1, and it drops it *entirely*:
`applyAgentMapping` sets `policy.SettingSources = nil`, which omits the flag,
which loads **user, project and local** settings. So the exposure this ADR is
accused of introducing is already live in the tree for agent-mapped nodes, in
a wider form, with printed disclosure and an opt-out — and nobody has called
that a ceiling breach, because it is not one.

This ADR uses the explicit value `"user"`, which is strictly narrower, and the
difference is load-bearing rather than cosmetic. ADR 0004's Consequences claim
that layer 1 closes the settings-hook gap *"for free: writing a
`.claude/settings.local.json` into the invocation directory achieves nothing
when no node of this run — or of any later auto run — loads local settings."*
With `"user"`, **that claim survives**: the `local` and `project` sources stay
unloaded, so a write-capable node cannot plant settings in the checkout for a
later node to load. With `nil` it would not survive. The narrow value is the
whole reason this relaxation is admissible; it is pinned by measurement (d).

A separate consequence follows, and it is the strongest argument against this
ADR that neither #130 nor its comments names: **the user source is writable by
a planned node.** `Write` is in `plannedToolAllowlist`, unscoped. A node that
declared it can write `~/.claude/settings.json` — where **hooks** live, and
hooks are not tool calls, so no ceiling layer bounds them — or plant
`~/.claude/skills/x/SKILL.md` for a later node to activate. Under layer 1 =
`""` both writes are inert. Under `"user"` they are live for every node
spawned afterwards. §5 answers it.

### 4a. Everything that actually rides along, enumerated from the format

The first draft enumerated three things — `{CLAUDE.md YES, MCP NO, hooks
UNMEASURED}` — and reasoned from *this machine's file* rather than from what a
`settings.json` may contain. That is the wrong basis: the decision has to hold
for a user whose file has keys this one does not. Corrected:

- **`model`.** Set to `"opus[1m]"` here. ADR 0004's **E3 explicitly measured
  that the pin is not applied under `""`**, and cited that as proof the flag was
  in effect. Under `"user"` it applies to every planned node, on every retry and
  every feedback re-run — silently changing the model, the cost, and how
  `--max-budget-usd` and `MaxGoalBudgetUSD` behave. A run's cost ceiling is
  computed by oh-my-graph against a model the user's file may override.
- **`env`.** Claude Code settings accept an `env` block. An
  `env.ANTHROPIC_API_KEY` or `env.ANTHROPIC_BASE_URL` applied from a file the
  CLI loads **internally** is not reachable by `childenv.Scrub`, which cleans
  the child process's environment and nothing else. Under `""` this was
  structurally impossible; under `"user"` it is a live path to this project's
  **#1 invariant — subscription billing**. This machine has no `env` key, which
  is why the first draft missed it; the format permits one, so the decision must
  argue it. **It currently cannot**, which is measurement (h).
- **`permissions.additionalDirectories`** — widens the node's *reach*, which
  `--tools` does not bound at all. A ceiling expressed in tool names says
  nothing about which paths those tools may touch.
- **`permissions.defaultMode`**, **`apiKeyHelper`** and **`statusLine`** (the
  last two execute shell), **`enableAllProjectMcpServers`**, and
  **`skipDangerousModePermissionPrompt`** (true here).

And the MCP claim, corrected:

- **MCP — the `NO` is not a result.** This machine has **no user-scoped MCP
  servers at all**: `~/.claude/settings.json` has no `mcpServers` key and
  `~/.claude.json` has `mcpServers: []`. A negative result with nothing to leak
  measures nothing — there was no positive control. It is also a **self-report**
  (*"the node reported `NO-MCP`"*), thirty lines after this ADR disqualifies
  self-reports for returning contradictory answers to identical argv. #130's own
  closing comment says `--strict-mcp-config` *"is supposed to bound this;
  **unverified**"*. ADR 0004's E5 said NOT MEASURED honestly, and this ADR's
  annotation should never have overwritten it. Restated: **unmeasured, pending a
  probe on a machine with a user-scoped server configured.**

### 5. The user source is sealed for the duration of the run

At plan time, trusted code records a SHA-256 over the user-source set it is
about to make live:

- `~/.claude/CLAUDE.md`, **plus every file it `@import`s, transitively**
- `~/.claude/settings.json`
- every `SKILL.md` the scan found (as an ordered list of `(path, sha256)`
  pairs, so an addition, a removal and an edit are each a change)
- **every file bundled beside a scanned `SKILL.md`** — the `references/` tree
  included
- **every script a hook in `settings.json` points at**, resolved from the
  `command` field

The last three were absent from the first draft, and each defeated the seal
outright:

1. **Hook target scripts.** `settings.json` pins e.g.
   `"command": "… ~/.orca/agent-hooks/claude-hook.sh"`. A `Write`-capable node
   edits *the script*; `settings.json`'s hash is unchanged; the seal stays
   green. §5's stated purpose — catching *"a run that rewrote its own
   instruction source and kept going"* — was defeated **without touching a
   sealed byte**. Resolving `command` to a path is best-effort (it is a shell
   string, not a path field); what cannot be resolved is **named in the printout
   as unsealed**, never silently skipped.
2. **`references/` files.** §8 *celebrates* that bundled references become
   reachable through the CLI's progressive disclosure, while `scanSkillDirs`
   reads only `<dir>/*/SKILL.md`. §5 and §8 contradicted each other about what
   the corpus is. If §8 counts them as reachable content, §5 seals them.
3. **CLAUDE.md `@imports`.** An import is instruction text at one remove.

This widens what the scan must walk. That cost is accepted: a seal that covers
the easy half of the corpus is worse than no seal, because it reads as coverage.

Before each planned node spawns, the seal is recomputed and compared. A
mismatch **halts the run** with a named error identifying the changed path.
Within a leg this is not advisory: a run that rewrote its own instruction
source and kept going would make every claim in §4 conditional on nobody
having tried.

This is not the ADR 0012 snapshot property. Inlining froze the *text* into
`graph.json`; a seal cannot, because activation reads the files at run time
inside the CLI. What it converts is the failure class: *"a node can rewrite
the user source for the nodes after it"* becomes *"a run whose user source
changed under it stops."* Two residuals, stated rather than closed:

- **TOCTOU.** The window between the seal check and the CLI's own read is not
  closed. Closing it would require the CLI to accept a content hash, which it
  does not.
- **First-write escalation is detected, not prevented.** The node that writes
  has already written; what it cannot do is have a later node read it.

On `resume`, the seal is **re-checked and still enforced as a halt**, and the
halt is releasable only by an explicit new flag
(`resume --accept-changed-sources`), which prints the changed paths and
re-seals. A user editing their own CLAUDE.md between legs is ordinary, not an
attack — so the exception exists; it is a **typed human act**, not a default.

> **Correction (2026-08-07).** The first draft downgraded the resume seal from a
> halt to a banner, justified by *"a resumed leg may be days after a gate
> paused"*. **That run cannot exist.** `validatePlannedNodes` rejects gate nodes
> outright (*"planned node %q is a gate node, which auto mode cannot run"*), so
> a planned graph has no gates. The real resume path for an auto run is
> `resume --retry-failed` — i.e. **immediately after a node failed**, which is
> the worst possible moment to stop enforcing. §4 names *"a node writes
> `~/.claude/settings.json` or plants a `SKILL.md`"* as the strongest argument
> against this ADR and §5 answers it with a halt; the first draft then handed
> the exception to exactly the leg that follows a failure. The banner-only form
> is what a `--no-web`-style convenience flag looks like, applied to the one
> control that answers the ADR's own strongest objection.

This still mirrors the precedent that `resume` re-runs
`warnBypassPermissions` — *"a resume may be far from the terminal session that
saw the first one"* — but that precedent re-prints a **warning about a choice
already made**, not a check on whether the run's instruction sources changed
under it. The analogy justifies re-reporting, never dropping enforcement.

### 6. The user's CLAUDE.md enters an unattended node, and that reasoning does extend

This is the crux, so it gets the argument rather than an assertion.

The first draft opened this section *"It is **not a capability leak** — measured:
`NO-BASH-TOOL` under a settings file granting `Bash(*)`."* **Struck.** That
probe measured a node with no Bash declared (see §"What the ceiling probe
actually measured"), and under the layer-1 route the same file's `Bash(*)`
becomes a live allow rule for a node that *did* declare `Bash(git *)`. So
CLAUDE.md is the *smaller* half of what layer 1 admits, and the section below
argues only that smaller half. **The capability question is open and is
measurement (g).** If (g) confirms the regression, this section's whole
cost-benefit is moot on the layer-1 route and survives only under D2, where
CLAUDE.md never loads at all.

What follows therefore stands as the argument about **text**, not about
capability: untrusted-to-the-plan text entering a paid, unattended
prompt. That is precisely the surface ADR 0012 §5 reasoned about
for skill bodies and accepted, on the grounds that it is the user's own file
on the user's own machine, *"the same reasoning that keeps the planner call
deliberately non-isolated so it reads the user's CLAUDE.md (E7)."*

The reasoning extends, and it extends **a fortiori** — CLAUDE.md is a weaker
claim than the one already accepted, on three counts:

1. **The user knows they have it.** ADR 0012 §5 had to concede that with 35
   skills and a 4-rune prefix rule, *"files-the-user-forgot is the modal
   case."* Nobody forgets their own CLAUDE.md; it is one file, at a path they
   typed, that they edit by hand.
2. **It is applied by the CLI's own mechanism.** Inlining manufactures prompt
   text the CLI would never have produced and bypasses the description gate —
   ADR 0012 §5's own words, *"a conditionally-applied procedure becomes
   unconditional instructions"*, and *"strictly more surface than the agent
   precedent."* Loading CLAUDE.md is the CLI doing what it does everywhere
   else.
3. **The same file already steers the run.** E7 is not incidental: the planner
   call is deliberately non-isolated, so this exact file already shapes the
   graph, the node ids, the prompts and the tool declarations. Admitting it to
   the nodes makes the run *more* internally consistent, not less. A user
   whose CLAUDE.md says "respond in Korean" currently gets a Korean-planned
   graph executed by nodes that never heard of the instruction.

Where it does **not** extend, and what is genuinely new:

- **Repetition and price.** Inlined text was paid by mapped nodes only (7% of
  them). CLAUDE.md is paid by *every* planned node, on every retry and every
  feedback-edge re-run. On this machine that is 251 lines per invocation.
- **Contradiction.** A CLAUDE.md is written for an interactive session with a
  human present. Real directives on the measuring machine include "push →
  auto-create PR" and a git workflow section. A node that declared
  `Bash(gh pr *)` and reads "when the work is done, push and open a PR" is
  being steered by an instruction whose author assumed a person was watching.
  Bounded by the ceiling — no tool the node did not declare — but inside the
  declared set it is real, and it is the same residual ADR 0012 §5 accepted
  ("within the node's declared tools, an inlined body steers behaviour — that
  is its purpose — including wrongly"), now applying to more nodes.

**What the plan printout owes the user for it:** the file, its size, its
SHA-256, and the fact that it will be read again at run time rather than
frozen. Naming the mechanism is not enough; ADR 0012 established that a
provenance line for injected text is the price of injecting it, and this is
injected text that oh-my-graph asked the CLI to inject.

**And CLAUDE.md is not the largest text channel.** `SessionStart` and
`UserPromptSubmit` hooks **inject their output into the node's context** — this
machine registers both. That is arbitrary program output entering an unattended
prompt, unbounded in size and not a file anyone can hash in advance. The first
draft's disclosure obligation covered CLAUDE.md and missed it entirely. So the
obligation extends: the printout enumerates **which hook events are registered
and which of them inject text**, by name, from the file it is already hashing.

### 7. What the printed plan can, and can no longer, tell the user

This is the sharpest thing given up, and it is given up permanently.

Under inlining, the plan printout could name **which skill went into which
node**, with size and hash, before anything ran — a complete prospective
account. Under activation, the choice happens at run time, inside the model,
by description. **The plan cannot name it, and no amount of printing will
recover that.** Prospective disclosure becomes retrospective evidence.

What the printout prints instead:

```text
  skill activation: ENABLED on 3 of 3 planned node(s) — 35 skill(s) from /Users/you/.claude/skills
  Which skill a node uses is chosen by the model at run time from those descriptions.
  It is NOT knowable here; each invocation is recorded in that node's session transcript.
  ceiling: layer 1 goes from "load nothing" to "load your USER settings only" on these nodes:
    - ~/.claude/CLAUDE.md (10.65 KiB, sha256:ab12ab12ab12) is read by every planned node, at run time
    - your settings.json pins model=opus[1m], which OVERRIDES this run's model for these nodes
    - 10 hook events are registered; PreToolUse and PermissionRequest can DECIDE a tool call,
      SessionStart and UserPromptSubmit INJECT text into these nodes' context
    - your scoped rules are no longer the only ones: settings.json grants Bash(*) (28 allow rules)
    - project and local settings are still NOT loaded; MCP is bounded by --strict-mcp-config (unverified)
    - sealed for the run (CLAUDE.md + @imports, settings.json, hook scripts, 35 SKILL.md + bundled
      files); a node that rewrites any of them halts the run
  Turn all of it off with --no-skill-activation (restores the ADR 0004 ceiling exactly).
```

The retrospective account is not a promise this ADR has to build: it already
exists. Every node runs with session persistence on and *"is also an ordinary
claude session in `~/.claude/projects` that any external tool can read"*
(CLAUDE.md, load-bearing invariants), and `runstate.NodeRecord.SessionID`
persists the id needed to find it. A `Skill` invocation appears there as a
tool call. Surfacing it in the ledger — "node `review` used skill
`pr-code-review`" — is attractive and is **not** part of this decision: it
would couple **shipped output a user reads** to a transcript format that is not
a documented contract, which is the mistake ADR 0004 caught `--help` prose
making once. Filed as a follow-up, with its own measurement.

The manual regression test in Failure-modes reads that same undocumented format,
and that is not the same commitment: **the format is undocumented, so the manual
test is allowed to break on a CLI upgrade — that is its job**, failing in front
of a maintainer who ran it deliberately before a release. **The ledger is not
allowed to break, because a user cannot tell a changed transcript format from a
skill that was never activated** — which is this ADR's signature failure mode
(§Failure modes, "silent absence"). See §"Review findings not adopted".

### 8. ADR 0012's inlining is removed, in the same change, not kept as a fallback

*"A mechanism kept 'just in case' with no measured case is debt"* — and there
is no measured case. Activation covers 35 skills where inlining covered 7% of
node ids; it is conditional where inlining was unconditional; it has no size
cap, so `pre-commit-checklist` (86.6 KiB — the skill that matched the *best*
four planner ids and was discarded from every one of them) becomes reachable;
and its bundled `references/` files are reachable by the CLI's own
progressive disclosure instead of being an acknowledged gap.

The one property inlining has that activation does not is the snapshot (§5),
and §5 replaces it with a seal rather than losing it silently. The rest of
ADR 0012's machinery — the 16 KiB cap, the `{{` neutralization, the nonce
fence around inlined bodies, the name-token matcher, the ambiguity-is-silence
rule, the agent-mapped skip — exists solely to make inlining safe and is
deleted with it.

Concretely, when the gate passes:

- **Deleted:** the inlining half of `internal/coordinator/skillmap.go` (§1's
  fenced append, §2's matcher, §3's cap, §4's neutralization), `SkillMapping`,
  and the `skill mapped:` / `skill skipped:` printout lines.
- **Kept and reused:** `scanSkillDirs` and `SkillScan` — the scan is what
  decides §1's predicate, what the printout names, and what §5 seals. ADR 0012
  §6's disclosure paragraph survives, re-pointed at activation.
- **Reverted:** `field_dispositions_test.go`'s recorded `why` for `Prompt`,
  which ADR 0012 §5 changed to "planner text plus trusted-code-appended local
  file content", goes back to constrained planner-authored text.
- **Voided:** ADR 0012's required measurements (a) and (b). (b) is voided
  because the mechanism it measures is gone, not because it was answered —
  and it should be recorded that way, since the misfire it was written to
  measure (`artifacts` → `html-artifact`) is exactly the class of error
  activation is expected to avoid, and "expected to" is not "measured to".

**The two mechanisms must never coexist in a shipped build.** A node holding
both would receive the same skill twice — once as unconditional fenced text,
once by activation — pay for it twice, and become unattributable: the fence's
claim about where its text came from stays true while the node's behaviour
stops being explained by it. One PR, mutually exclusive.

Until that PR lands, ADR 0012 is what ships, and its record says so.

## The acceptance test

The maintainer's, recorded here as the condition for calling this done:

> Plan the goal *"establish a fix proposal for this issue, review the
> proposal, and turn it into an HTML artifact"*, and check that each node
> loads the skill its job calls for.

Three jobs, three skills that exist in this corpus: `architecture-design`,
`pr-code-review`, `html-artifact`. It exercises the mechanism end to end and
fails visibly if activation is silently absent — which is the failure this
whole ADR is most exposed to.

**Method.** `auto` the goal against the real corpus on subscription OAuth,
env scrubbed per `childenv.Scrub`. For each executed node, read the session
transcript for that node's `runstate.NodeRecord.SessionID` (under
`~/.claude/projects`; locate the path, do not assume its shape) and extract
every `Skill` tool call by name. Record the node-id → skills-invoked table,
the CLI version, and the cost.

**PASS requires all six:**

1. **Grant present.** Every planned node's persisted policy carries
   `setting_sources: "user"` and `Skill` in `tools`. Read from the **`state.json`
   of the acceptance run itself** — *not* from `--plan-only`, which writes only a
   `graph.json` under `plans/<id>/` and deliberately produces **no run directory
   and no `state.json`** (*"a preview never ran, so it is not a run"*), and in
   which §2 makes the grant invisible by design. The first draft's *"readable
   from `--plan-only` plus `state.json`; costs one planner call"* named an
   artifact that one planner call cannot produce.
2. **Activation alive, against a negative control.** At least one node's
   transcript records a `Skill` tool call **and** the same goal re-run with
   `--no-skill-activation` records **zero**. Without the control arm this
   criterion cannot distinguish the change from the baseline — a model competent
   without skills passes it either way, which is the exact silent-absence
   failure it exists to catch. Cost: a second run, recorded.
3. **The three skills, on the right jobs — assignment pre-registered.** Across
   the run, the invoked set includes `architecture-design`, `pr-code-review` and
   `html-artifact`, each invoked by the node whose job is the corresponding one.
   The node → job assignment is written down **from the plan printout, before any
   transcript is opened**, and that pre-registration is what the result is scored
   against. Read post-hoc — "the node whose job is the corresponding one",
   decided after seeing which skill fired — the criterion is unfalsifiable by
   construction.
4. **Nothing was denied.** No `Skill` entry appears in the CLI's own denial
   report. **This criterion currently has no data source and building one is in
   scope for the implementation:** `permission_denials` is parsed **nowhere in
   this codebase** — `runner.claudeEnvelope` carries `session_id`, `result`,
   `total_cost_usd`, `subtype`, `is_error` and `errors`, and nothing else. Either
   the envelope gains the field (and the acceptance test reads it) or this check
   is performed by reading the node's session transcript directly and says so.
   As written in the first draft it was unreadable.
5. **The ceiling held, on the shape that can break it.** The re-probe is
   **measurement (g)**: a node declaring **`Bash(git *)`** — the E1 shape — under
   the final shipped argv, attempting an out-of-scope command. Re-running the
   original `CEILING-BREACH` prompt against a node that declared no Bash returns
   `NO-BASH-TOOL` and **passes while the regression is live**, which is how the
   first draft's version of this criterion would have certified the very hole
   §"What the ceiling probe actually measured" describes.
6. **Billing intact.** The run's cost lands on subscription OAuth
   (`provider: "firstParty"`), asserted per node, not assumed — because §4a
   makes a settings-file `env` block a live path to that invariant.

**FAIL is recorded, not retried away.** Fewer than three skills invoked; a
wrong skill invoked (record which, and against which job — that is the direct
successor to ADR 0012's `artifacts` → `html-artifact` finding); a plan whose
shape has no such three jobs (re-plan **once**, record both plans and the
cost of both). A partial pass is a fail with a table attached.

**The ids are kept.** ADR 0012's yield measurement could not be re-derived
from its own record because `--plan-only` writes nothing and the ids lived
only in the planning session. Per that section's closing note, this run writes
its goal, its full node-id list, and the node → skills table into
`docs/measurements/` as it goes.

Note what a pass does **not** establish: that an activated skill made the
node's output *better*. It establishes that the right procedure was loaded for
the right job. Quality is measurement (e).

## Required measurements before Accepted

Record each with cost and CLI version, as every prior E-number is.

- **(a) Can a user hook DECIDE a planned node's tool call?** Not merely *"do
  hooks fire"* — that was the first draft's question and it is too weak. Hooks
  do not only observe: a **`PreToolUse`** or **`PermissionRequest`** hook can
  **approve a call the permission system would deny**, and this machine
  registers both (`PermissionRequest → fleetops hook permission`,
  `PreToolUse → ~/.orca/agent-hooks/claude-hook.sh`). So the probe is: **plant a
  hook that approves a call `--allowedTools` denies, under the final argv, and
  see whether the call runs.** A yes makes hooks a **sixth ceiling bypass** — it
  is measurement (g) with execution attached, not a usability note. Sub-probes:
  (a1) does a `PreToolUse` marker hook fire at all; (a2) do `SessionStart` /
  `UserPromptSubmit` hooks inject their stdout into the node's context (§6);
  (a3) does the hook's own child process inherit the scrubbed env (it should —
  it descends from a `childenv.Scrub`ed process — but the subscription-billing
  invariant is not something to infer).
  **Note that the registered event list is a free plan-time filesystem fact**:
  the printout is already hashing the file that lists them, so
  `MAY fire (UNMEASURED)` understates what is knowable before the run — hence
  §7's printout enumerates the events by name whatever (a) returns.
- **(b) Does a skill that runs in a subagent route around layer 5?** Some
  skills execute in a subagent rather than loading instructions inline.
  `Task` and `Agent` are both in `deniableTools` and denied to a node that did
  not declare them. Plant such a skill, invoke it under the final argv, and
  record whether it spawns and what tool set the child holds. **A yes here is
  a ceiling finding, not a usability one**, and would force either a refusal
  of subagent-executing skills or its own ADR.
- **(c) Is `Skill` in `--allowedTools` necessary under `dontAsk`?** The full
  `runner.buildArgs` argv, with and without the layer-2 half, comparing
  `permission_denials`. Settles §3 and removes a flag value that may be inert.
- **(d) Does `--setting-sources user` really exclude project and local?** A
  `.claude/settings.local.json` granting `Bash(*)` and a project `CLAUDE.md`
  carrying a codeword, both in the node's cwd; assert the shell is still
  absent and the codeword unknown. §4's entire case for `"user"` over `nil`
  rests on this, and it is currently an inference from the flag's name.
- **(e) Does an activated skill improve the node's output?** The descendant of
  ADR 0012's voided (a). Same goal, same corpus, with and without
  `--no-skill-activation`, comparing artifacts. It does not gate the ceiling
  claims, only the value claim — but the value claim is the entire reason to
  pay §6's price, and this ADR should not reach `Accepted` asserting it the
  way ADR 0012 asserted inlining's. (The control arm is shared with acceptance
  criterion 2 — one run serves both.)

**Blocking, added by the 2026-08-07 review. These gate implementation itself,
not `Accepted`:**

- **(g) Does layer 2 still bind under `--setting-sources user`?** ADR 0004's E1,
  re-run under the **final** argv: a node declaring `Bash(git *)`, with
  `--setting-sources user --allowedTools "Bash(git *)" --tools Bash,Read,Skill
  --permission-mode dontAsk --strict-mcp-config`, attempting an out-of-scope
  command (E1's `touch`). **If the command runs, the layer-1 route forfeits the
  scope ceiling** and §1's layer-1 clause is dead — the decision's shape survives
  only via D2. This is one `claude -p` and it is the first thing to run.
- **(f) Can a `--settings` payload carry the skill definitions with layer 1 at
  `""`?** Alternatives D2, promoted from "highest-value probe" to **the design
  that survives if (g) confirms**. One `claude -p`.
- **(h) Can a settings-file `env` block reach the child's credentials?** §4a. A
  `~/.claude/settings.json` with `env.ANTHROPIC_BASE_URL` set to an unroutable
  host; run a planned node under the final argv; record whether the call is
  redirected and whether `provider` stays `firstParty`. `childenv.Scrub` cleans
  the environment it constructs and cannot reach a value the CLI applies
  internally. **This touches the project's #1 invariant, so a yes blocks the
  layer-1 route outright**, independently of (g).

## Failure modes

- **Silent absence on a future CLI.** Activation is one flag-semantics change
  away from yielding nothing, and unlike inlining there is no printed line
  that would look different — the plan says "ENABLED" either way, because the
  plan cannot see run-time choices (§7). Worse than under ADR 0012, where the
  7% floor at least printed itself. Mitigation: the acceptance test becomes a
  `//go:build manual` regression test beside `assess_manual_test.go` and
  `repair_manual_test.go`, run before each release, never in CI (it needs a
  real `claude` and costs cents — the `make smoke` posture).
- **A CLAUDE.md that contradicts the plan.** §6. Bounded by the ceiling,
  unbounded inside the declared tools, and paid on every invocation. The
  worst realistic case is a workflow directive with a side effect —
  "when done, push and open a PR" — landing on a node that declared
  `Bash(gh pr *)`.
- **A node rewrites the user source.** §4/§5. Detected and halted within a
  leg; announced and re-sealed across a resume; the first write is not
  prevented.
- **The scope ceiling, if (g) says the regression is real.** The worst case is
  not hypothetical and it is the reason this ADR is not authorized: an
  unattended `dontAsk` node that declared `Bash(git *)`, executing an
  out-of-scope destructive command because the user's own `Bash(*)` matched
  first. Mitigation is not a mitigation — it is D2, or no layer-1 relaxation.
- **Hooks, if (a) says yes.** A user hook is shell that fires around an
  unattended node's tool calls, outside every ceiling layer. It is the user's
  own file, so it is the same trust class as everything else here — but it is
  the only element of that class with execution, and it must be disclosed by
  capability rather than by "MAY fire". If (a) shows a `PreToolUse` or
  `PermissionRequest` hook can **approve** a call `--allowedTools` denies, the
  hook is a **sixth ceiling bypass** and needs its own ADR, not a bullet here.
- **The model pin silently changes the run.** §4a. A user's
  `model: "opus[1m]"` overrides the model every planned node runs on, so a cost
  ceiling oh-my-graph computed against one model is enforced against another.
  Cheap to disclose (the printout reads the key it already hashes); not cheap to
  discover from a ledger that says only that the run cost more than expected.
- **Nondeterminism.** The same plan run twice may activate different skills.
  ADR 0012's reproducibility property (the approved text is the executed text)
  is gone; §5's seal preserves only that the *corpus* did not change under the
  run. A gated plan approved on Monday and resumed on Friday activates against
  Friday's skills, and the resume banner is the only place that is said.
- **A corpus that grew between plan and run.** §1's predicate is evaluated at
  plan time; `Found: 0` disables activation for the whole run even if the user
  installs a skill mid-run. Correct — the policy is snapshotted — and it will
  read as a bug to whoever hits it, so the printout says the count is from
  plan time.
- **Cost with nothing to show.** A planned node with no relevant skill pays
  CLAUDE.md on every invocation and activates nothing. That is the modal case
  by construction, since ADR 0012 measured 84% of planner ids as having no
  candidate at all — a matcher's miss becomes, here, a per-node tax.
- **A machine with `allowManagedPermissionRulesOnly`.** Unchanged from
  ADR 0004: `--allowedTools` rules are ignored and the ceiling is the managed
  policy. §3's layer-2 half is inert there; §1's layer-3 half is not.

## Compatibility

- **No graph schema change, no new `graph.Node` field.** ADR 0004 §2's
  reflection test is unaffected in shape; one recorded `why` changes (§8:
  `Prompt` reverts).
- **No snapshot schema change.** `runstate.NodeToolPolicy` already carries
  `SettingSources` as a `*string` and `Tools` as a slice, precisely so
  "omitted" and "explicitly empty" survive a round trip;
  `TestNodeToolPolicyMirrorsRunnerToolPolicy` keeps passing untouched. A
  resumed leg therefore re-imposes `"user"` + `Skill` from the file, with no
  re-scan and no re-validation — which is the correct behaviour for
  reproducibility and also the reason §5's seal has a resume clause.
- **The kill switch must reach `resume`, and today it cannot.**
  `toRunnerToolPolicies` rehydrates `SettingSources` and `Tools` **verbatim**
  from `state.json`, and `resume` accepts only
  `--approve | --reject | --retry-failed | --concurrency | --no-web`. So the
  answer to *"if the tool policy changes shape, what does a resumed leg
  execute?"* is: **whatever the planning binary wrote, forever.** The forward
  direction is safe (an old run's `""` stays `""` — no old run is escalated).
  The unsafe direction is that **an activation-enabled run cannot be
  de-escalated on resume**, so §Compatibility's claim that the pre-0017 ceiling
  *"stays reachable by one flag"* was false for every resumed leg. The first
  draft noticed the seal needed a resume clause and did not notice the switch
  did. Therefore `resume` gains `--no-skill-activation` too, applied as an
  override on the rehydrated policies (drop `Skill` from `Tools` and
  `AllowedTools`, force `SettingSources` back to `""`) — de-escalation only,
  never the reverse, so a resume can never widen a run's ceiling.
- **`plannedToolAllowlist` is unchanged**, so `plannedToolEffects` needs no
  new row and `TestDetectBuildSignals_NeverInfluencesTheCeiling`'s layer-0
  assertion is untouched (§2).
- **The pre-0017 ceiling stays reachable by one flag, on `auto` and on
  `resume` alike** (see the bullet below — the resume half is new, and its
  absence was a defect in the first draft).
  `--no-skill-activation` restores `SettingSources = ""` and drops `Skill`
  from `--tools`, reproducing ADR 0004's ceiling bit-for-bit.
  `--no-skill-mapping` is accepted as a deprecated alias with a one-line
  notice, because the user intent behind it ("keep skills out of my auto
  runs") is unchanged and the effect is now stronger, not weaker. Both spell
  the same thing on `chat`, mirroring `--no-agent-mapping`.
- **Agent-mapped nodes are excluded from activation**, and the exclusion is
  printed. They already run with layer 1 dropped to `nil`, so the composite
  (`--agent` + `Skill` + user settings) is a different, unmeasured
  configuration — the same unmeasured-composite refusal ADR 0012 §2 made, for
  the same reason. Lifting it requires its own probe.
- **A follow-up this ADR declines to decide:** `applyAgentMapping`'s
  `SettingSources = nil` is wider than anything decided here, and by §4's
  argument it should be `"user"` — that would close the plant-a-local-settings
  gap on mapped nodes too. It is a change to a shipped mechanism on a path
  this ADR does not otherwise touch, so it gets its own issue and its own
  measurement rather than riding along.
- **Hand-written graphs: no change.** They never carried layers 1 or 3.
- **The four exec seams are unchanged.** Nothing here spawns a process. §5's
  seal is `os.ReadFile` plus `crypto/sha256` in the coordinator, deliberately
  not a shell out, so `internal/invariants` stays true.

## Alternatives considered

- **B — relax layer 1 only on nodes that would use a skill.** Mechanically
  trivial (`ToolPolicies` is already per-node; that is how agent mapping drops
  layer 1 on some nodes and not others). It fails on the predicate, not the
  plumbing. Any rule good enough to decide *"this node needs a skill"* in
  advance **is** a skill selector, and the only selector this project has
  measured is the name matcher: **7%**, with a false positive in 1 of the 5
  mappings it made. So B pays A's full price on the nodes it picks — CLAUDE.md,
  hooks, a live user source, and (pending (g)) the scope ceiling — while capping
  activation's recall at the selector's, which is the worst cell of the matrix:
  **A's price with C's yield.** It is also strictly less explainable than
  either, because the printout would have to say why *this* node got the
  relaxation and that one did not, from a rule already measured to be wrong 1
  time in 5 when it fires at all.
  *(The first draft rejected B partly on "14.5% for the LLM arm". That number is
  withdrawn — see the Correction in §Context. B's rejection does not need it:
  the argument is that any advance predicate **is** a selector and inherits
  whatever a selector's recall turns out to be, which holds without knowing an
  LLM arm's number. If someone wants to argue B on the grounds that an LLM
  selector beats 7%, that is now an unmeasured claim and would need its own
  arm, run and costed.)*
  The one honest sliver
  of B survives as §1's predicate: condition on a filesystem fact (does a
  corpus exist), never on a guess about relevance.
- **B′ — relax only on nodes that declared no mutating tool.** A coarse,
  explainable predicate that genuinely bounds §6's worst case (no
  `gh pr`-holding node reads a "push when done" directive) and §4's
  write-the-user-source hole outright. Rejected on yield: the nodes that most
  want skills are the mutating ones — implement, review, produce the artifact
  — and the acceptance test's own three jobs are two mutating nodes and one
  arguable. It would ship a capability aimed at the nodes least able to use
  it. Worth re-opening if (a) finds hooks fire, because the hazard changes
  class.
- **C — keep the isolation, keep inlining.** Steelmanned, because it has four
  properties activation gives up and one of them is real. Inlining is
  **deterministic** (the same plan produces the same prompt); it is
  **printed** in the plan with a name, a size and a SHA-256, so a human can
  read exactly what will be injected before approving; it is **snapshotted**
  into `graph.json`, so a skill edited after planning cannot change an
  in-flight run; and it puts **no CLAUDE.md** into an unattended node. Against
  that: it recovers 7% of the capability, with a measured false-positive rate
  of 1 in 5 among the mappings it does make; it kills its four best matches at
  a size cap it fit against a corpus that could not exercise that cap; its
  claim to *help* the nodes it lands on has been unmeasured since the day it
  shipped; and its determinism is determinism about a mechanism, not about an
  outcome. Of its four properties, one is preserved (§5's seal keeps the
  corpus from changing under a run), one is partly preserved (the printout
  still names the corpus, the directories and the count — it can no longer
  name the per-node choice, §7), and two are genuinely surrendered
  (per-node prospective disclosure; no CLAUDE.md). That is the trade, taken
  knowingly: **a complete account of a mechanism that recovers 7% is worth
  less than an incomplete account of the mechanism itself.**
- **D1 — relax layer 3 only, keep layer 1.** Measured dead:
  `--setting-sources "" --tools Read,Skill` does not run the skill. Naming the
  tool does not load the definitions.
- **D2 — synthesize a `--settings` payload that carries skills and nothing
  else. Promoted by the 2026-08-07 review from "most attractive alternative" to
  the only design that survives if (g) confirms the layer-2 regression** — and
  it is still unmeasured. ADR 0004's own
  Alternatives note that *"flag settings survive `--setting-sources \"\"`"*
  and keep the flag *"available if a future need arises"*. If a settings key
  can point the CLI at skill directories, then oh-my-graph could load the
  definitions while keeping layer 1 at `""` — **no CLAUDE.md, no hooks, no
  user permission rules**, and §4, §5 and §6 all collapse to nothing. That
  would dominate this decision on price while leaving its shape intact,
  because the shape (activation over the whole corpus, chosen by description
  at run time, instead of 7% name-matched inlining) is independent of how the
  definitions arrive. It is not chosen because it is unmeasured and this ADR
  will not repeat ADR 0012's mistake of designing around an unprobed premise.
  Disposition: it is measurement **(f)** — cheap, one `claude -p` — and if it
  works, the implementation swaps §1's layer-1 clause for a `--settings`
  payload, deletes §5 and §6, and nothing else in this record changes.
  **It now also collapses the ceiling problem**, which is what promotes it: with
  layer 1 at `""` the user's `Bash(*)` never becomes a live allow rule, so
  layer 2 keeps binding, E1 stands unamended, and the retraction at the head of
  this document does not apply. §4a's `model`-pin and `env` exposures go with it.
  D2 is therefore run **before** any implementation, alongside (g), and if (g)
  confirms while (f) fails, **this ADR has no admissible mechanism** and the
  honest outcome is to record that rather than ship the layer-1 route with a
  known scope regression.
- **D3 — run planned nodes under a synthetic `HOME` holding only skills.**
  Rejected without measurement. Subscription OAuth credentials live under
  `HOME`; moving it risks the one invariant the whole project is built on
  (ADR 0001, `childenv.Scrub`), and it would relocate the skill corpus along
  with everything else. A billing-path gamble to avoid reading a text file is
  the wrong trade.
- **D4 — let the planner pick skills and declare them.** Rejected, still, and
  for the reason ADR 0012 gave: the planner is an untrusted producer, and
  letting its output select which local file loads into a node is the hole
  `validatePlannedNodeAgent` closes. Note carefully that **activation is not
  this**: the choosing model is the node's own, at run time, over the user's
  own files, through the description gate the CLI designed for exactly that,
  and bounded by a tool ceiling it cannot widen (E4, re-confirmed). "Untrusted
  choice" does not transfer from a producer choosing for *other* nodes ahead
  of validation to a node choosing for *itself* under the ceiling.

## Consequences

**Positive**

- A planned node gets the real mechanism: 35 skills instead of a 7% lexical
  substitute, selected by description instead of by a 4-rune prefix that
  cannot tell `artifacts` from `html-artifact`. The skills that matched
  planner ids best and were discarded at the cap — `pre-commit-checklist` on
  four verification nodes — become reachable, along with their bundled
  `references/` files, through the CLI's own progressive disclosure.
- ~~The capability ceiling is unchanged and re-confirmed by measurement.~~
  **Retracted 2026-08-07.** What is re-confirmed is narrower and was never in
  dispute: **no tool a node did not declare becomes available** (E4 — `--tools`
  replaces the built-in set). What is **not** established is that a node's
  declared-but-scoped tool stays scoped: under the layer-1 route the user's
  `Bash(*)` becomes a live allow rule beside the node's `Bash(git *)`, which is
  the regression E1 measured. Layers 0, 4 and 5 are byte-for-byte identical and
  layer 2 gains one bare name — but layer 2's *binding force* is exactly what is
  in question, and it is measurement (g). Under D2 this bullet returns intact.
- ADR 0004's hook gap stays closed against the run's own output, because
  `"user"` is not `nil` (§4) — the property agent mapping gave up and this
  keeps.
- ~750 lines of ADR and a matcher, a cap, a neutralizer and a nonce fence
  leave the tree with the mechanism they existed to protect (§8).
- The run gains a property it never had: the instruction sources it depends on
  are sealed for its duration, and a run whose sources changed under it stops
  (§5).

**Negative / trade-offs**

- **Per-node prospective disclosure is gone.** The plan can no longer say
  which skill a node will use, because nothing knows before the model does
  (§7). The account moves to the session transcript.
- **The user's CLAUDE.md is read by every planned node, on every invocation.**
  Not a capability leak; a cost, a contradiction risk, and text the plan
  approved only by hash (§6).
- **Hooks are unmeasured and load anyway**, which is the least comfortable
  sentence in this document. Measurement (a) exists to end it, and it must be
  run before `Accepted`, not before shipping — because there is no shipping
  before `Accepted` (Status).
- **Reproducibility drops.** Same plan, same corpus, potentially different
  skills. The seal bounds the corpus, not the choice.
- **The user source becomes writable-and-live**, mitigated by detection rather
  than prevention (§5).
- **One machine, one CLI version.** Every number in this record is claude
  2.1.223 on darwin, on a settings file that grants `Bash(*)` — which is the
  adversarial case for the ceiling claim and the *easy* case for a machine
  with no grants at all. A machine with a restrictive `settings.json` may find
  layer 3 doing nothing observable, and that is not the same as it being
  unnecessary.

## Review findings not adopted

The 2026-08-07 deep review raised nine blocking items. Eight are adopted above,
and each is marked in place rather than quietly rewritten, because a record that
edits away its own retracted claims teaches a future reader nothing. One is
adopted only in part.

- **"§7 contradicts the mitigation" — adopted in part, rejected in substance.**
  The finding: §7 declines to surface skill invocations in the ledger because
  that *"would couple oh-my-graph to a transcript format that is not a
  documented contract"*, while Failure-modes commits the acceptance test as a
  `//go:build manual` regression test in-tree — which couples committed code to
  that same undocumented format. *"Pick one."*

  The distinction is real and is kept. What §7 refuses is coupling **shipped
  product behaviour** to an undocumented format: a ledger line that users read,
  that other tools parse, and that silently degrades to wrong output when the
  transcript shape changes — the mistake ADR 0004 caught `--help` prose making.
  A `//go:build manual` test is the opposite failure mode by construction: it is
  never compiled into a release, no user depends on it, and when the format
  changes it **fails loudly in front of a maintainer running it deliberately
  before a release**, which is precisely the signal wanted. `make smoke`,
  `assess_manual_test.go` and `repair_manual_test.go` already occupy exactly
  this position in the tree.

  What is adopted: §7's wording invited the reading, so the criterion is now
  stated as *"the format is undocumented, so the manual test is allowed to break
  on a CLI upgrade — that is its job; the ledger is not, because a user cannot
  tell a changed format from an absent skill."*

## What could not be determined

Named with the measurement that would settle each, so a future reader is not
left guessing which of these is an opinion.

0. **Whether layer 2 still binds once the user's settings load.** → (g). The
   decision-changing unknown, and the one the first draft believed it had
   answered. Everything below is secondary to it.
1. **Whether user hooks fire, and whether they can DECIDE a tool call.** → (a).
   The largest remaining unknown; it is the only element of the relaxed set that
   executes, and a hook that can approve a denied call is a ceiling bypass
   rather than an exposure note.
2. **Whether a subagent-executing skill escapes layer 5.** → (b). If yes, it
   is a ceiling finding, not a feature note.
3. **Whether layer 2 must name `Skill` under `dontAsk`.** → (c). §3 pays for
   the uncertainty with a redundant grant.
4. **Whether `--setting-sources user` excludes project and local settings.**
   → (d). Inferred from the flag's name; §4's entire case rests on it.
5. **Whether activation improves node output.** → (e). ADR 0012 shipped
   without answering the equivalent question. This one should not.
6. **Whether the definitions can be loaded without the settings source at
   all.** → (f), the `--settings` payload. If it works, half this ADR is
   unnecessary — which is a good enough reason to run it before writing any
   code.
6b. **Whether `--strict-mcp-config` actually bounds MCP under
   `--setting-sources user`.** The first draft recorded this as measured `NO`.
   It is not: the measuring machine has no user-scoped MCP servers, so there was
   nothing to leak and no positive control, and the result was a self-report
   besides. Needs a machine with a server configured — #130 says the same thing
   and calls it unverified.
6c. **Whether a settings-file `env` block can redirect the child's API
   credentials.** → (h). The one unknown on this list that touches the
   subscription-billing invariant, and the only one that can block the layer-1
   route on its own.
7. **Whether any of this reproduces off this machine.** No probe settles it;
   it needs a second machine, ideally one whose `settings.json` grants
   nothing, and one on a different CLI version. #130 already asks for exactly
   that, and the invocation-form probes in its first comment are the right
   ones to send.
8. **Whether a given user's CLAUDE.md is safe to feed an unattended node.**
   Unmeasurable in principle — it is a different file for every user. That is
   why §6's answer is disclosure with a hash rather than a check, and why
   `--no-skill-activation` has to exist.
