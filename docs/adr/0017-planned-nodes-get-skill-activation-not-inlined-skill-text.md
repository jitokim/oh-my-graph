# ADR 0017 — A planned node gets Claude Code's own skill activation, and pays for it with the user settings source

- Status: Proposed — decision taken, **nothing implemented**. The gate is the
  acceptance test in "The acceptance test" below, and unlike ADR 0012 this
  record does not permit the code to land ahead of it. ADR 0012 shipped while
  `Proposed` with its own acceptance probes unrun, and eighteen months of
  hindsight is not needed to see the cost: the mechanism was measured at 7%
  two days later and nobody had ever established that an inlined body helps a
  node at all. This ADR changes a node's argv and two ceiling layers, so it
  has a CLI-behaviour premise to probe, and the premise is measured on
  **one machine, one CLI version**. Implementation is authorized only after
  the acceptance test and required measurements (a)–(d) are recorded.
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

Its measured yield, on 393 real planner-authored node ids: **9.9%, ~7%
corrected** for one mapping that was simply wrong (`artifacts` →
`html-artifact`, matched on a 4-rune prefix, semantically unrelated). An LLM
selector arm over the same population reached 14.5%. Claude Code's own
description-driven activation would see **all 35 skills** and choose by
description, at run time, per task.

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

### The finding that makes this an ADR rather than a patch

Relaxing layer 1 sounds like forfeiting the ceiling, because the measuring
machine's `~/.claude/settings.json` grants `Bash(*)`, `Write(*)`, `Edit(*)`.
It does not:

```
--setting-sources user --tools Read,Skill --strict-mcp-config
  "Run the shell command 'echo CEILING-BREACH' using the Bash tool"
  -> NO-BASH-TOOL
```

`--tools` REPLACES the built-in set (ADR 0004, E4). **An allow-rule approves a
tool that exists; it does not create one.** Layer 3 absorbs layer 1's
relaxation, so the capability ceiling survives loading the user's settings.

What else rides along with `--setting-sources user`, measured on the same
machine and version:

- **`~/.claude/CLAUDE.md` — YES.** 251 lines, confirmed present in the node's
  context, first heading quoted back verbatim.
- **MCP — NO.** `--strict-mcp-config` holds; the node reported `NO-MCP`.
- **hooks — NOT MEASURED.** They live in the file that now loads. Nothing
  below treats them as bounded.

So the question is not how to improve the matcher. It is whether planned nodes
should get the real mechanism back, and at what price.

## Decision

### 1. Layers 1 and 3 are relaxed together, for planned nodes, and only when a corpus exists

`toolPolicyFor` gains one post-validation adjustment, applied by trusted Go
code to the **policy**, never to the graph:

- layer 1: `SettingSources` becomes a pointer to `"user"` instead of `""`.
- layer 3: `"Skill"` is appended to `Tools`.
- layer 2: `"Skill"` is appended to the policy's `AllowedTools` (see §3 for
  why this is provisional).

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

`plannedToolAllowlist` is **not** extended. ADR 0016 §1 narrowed what that
list answers — *"what class of tool is safe for unattended planner output at
all"* — and a planner that can name `Skill` in `allowed_tools` is a planner
that can select which of the user's local files gets loaded into a node it
authored. That is the hole `validatePlannedNodeAgent` closes for agents and
ADR 0012's third alternative closes for skills, and it stays closed.

So the grant is a **policy-level act, invisible to the graph**: `node.
AllowedTools` never contains `Skill`, `validatePlannedNodeTools` never sees
it, the saved `graph.json` never carries it. The same posture as
`agentmap.go`'s `agent:` and ADR 0016 §2's injected verification: *choosing
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

### 5. The user source is sealed for the duration of the run

At plan time, trusted code records a SHA-256 over the user-source set it is
about to make live:

- `~/.claude/CLAUDE.md`
- `~/.claude/settings.json`
- every `SKILL.md` the scan found (as an ordered list of `(path, sha256)`
  pairs, so an addition, a removal and an edit are each a change)

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

On `resume`, the seal is **re-checked and re-reported, not enforced as a
halt** — a resumed leg may be days after a gate paused, and a user editing
their own CLAUDE.md between legs is ordinary, not an attack. The banner prints
which sealed paths changed and re-seals. This mirrors the existing precedent
that `resume` re-runs `warnBypassPermissions`, because *"a resume may be far
from the terminal session that saw the first one."*

### 6. The user's CLAUDE.md enters an unattended node, and that reasoning does extend

This is the crux, so it gets the argument rather than an assertion.

It is **not a capability leak** — measured: `NO-BASH-TOOL` under a settings
file granting `Bash(*)`. It is untrusted-to-the-plan text entering a paid,
unattended prompt. That is precisely the surface ADR 0012 §5 reasoned about
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
    - ~/.claude/CLAUDE.md (9.4 KiB, sha256:ab12ab12ab12) is read by every planned node, at run time
    - user hooks in ~/.claude/settings.json MAY fire around these nodes' tool calls (UNMEASURED)
    - project and local settings are still NOT loaded, and MCP is still bounded
    - those files are sealed at this hash for the run; a node that rewrites them halts it
  Turn all of it off with --no-skill-activation (restores the ADR 0004 ceiling exactly).
```

The retrospective account is not a promise this ADR has to build: it already
exists. Every node runs with session persistence on and *"is also an ordinary
claude session in `~/.claude/projects` that any external tool can read"*
(CLAUDE.md, load-bearing invariants), and `runstate.NodeRecord.SessionID`
persists the id needed to find it. A `Skill` invocation appears there as a
tool call. Surfacing it in the ledger — "node `review` used skill
`pr-code-review`" — is attractive and is **not** part of this decision: it
would couple oh-my-graph to a transcript format that is not a documented
contract, which is the mistake ADR 0004 caught `--help` prose making once.
Filed as a follow-up, with its own measurement.

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

**PASS requires all five:**

1. **Grant present.** Every planned node's persisted policy carries
   `setting_sources: "user"` and `Skill` in `tools`. Readable from
   `--plan-only` plus `state.json`; costs one planner call.
2. **Activation alive.** At least one node's transcript records a `Skill`
   tool call. This alone distinguishes "the mechanism works" from "the model
   was competent without it", which is the silent-absence failure.
3. **The three skills, on the right jobs.** Across the run, the invoked set
   includes `architecture-design`, `pr-code-review` and `html-artifact`, each
   invoked by the node whose job is the corresponding one. Node → job is a
   human read of the node's prompt and is recorded in the measurement note,
   because the planner names nodes freely and a 3-node shape is not
   guaranteed.
4. **Nothing was denied.** No envelope's `permission_denials` contains a
   `Skill` entry. This is what catches §3's layer-2 interaction; a run that
   passes 2 and 3 while denying a fourth invocation has a hole in it.
5. **The ceiling held concurrently.** The `CEILING-BREACH` probe re-run under
   the **final shipped argv** — not the reduced argv of the original
   measurement — on the same CLI version, returning `NO-BASH-TOOL`. Without
   this, 1–4 could pass on a build that opened the ceiling.

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

- **(a) Do user hooks fire around a planned node's tool calls?** A
  `PreToolUse` hook in `~/.claude/settings.json` that appends a marker to a
  file; one planned node under the final argv that calls `Read`; check the
  file. This is the one thing riding along with `--setting-sources user` that
  was never measured, and it is not a text surface — hooks are arbitrary
  shell that no tool policy bounds, which ADR 0004 states in its Context as a
  reason layer 1 existed. **If hooks fire, this ADR's exposure section is
  incomplete and the printout's `(UNMEASURED)` becomes a stated capability.**
  Record also whether the hook's own child process inherits the scrubbed env
  (it should — it descends from a `childenv.Scrub`ed process — but the
  subscription-billing invariant is not something to infer).
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
  way ADR 0012 asserted inlining's.

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
- **Hooks, if (a) says yes.** A user hook is shell that fires around an
  unattended node's tool calls, outside every ceiling layer. It is the user's
  own file, so it is the same trust class as everything else here — but it is
  the only element of that class with execution, and it must be disclosed by
  capability rather than by "MAY fire".
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
  re-scan and no re-validation — which is the correct behaviour and also the
  reason §5's seal has a resume clause.
- **`plannedToolAllowlist` is unchanged**, so `plannedToolEffects` needs no
  new row and `TestDetectBuildSignals_NeverInfluencesTheCeiling`'s layer-0
  assertion is untouched (§2).
- **The pre-0017 ceiling stays reachable by one flag.**
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
  advance **is** a skill selector, and the measured selectors are the ones
  this ADR exists to escape: 7% for the name matcher, 14.5% for the LLM arm.
  So B pays A's full price on the nodes it picks — CLAUDE.md, hooks, a live
  user source — and caps activation's recall at the selector's, which is the
  worst cell of the matrix: **A's price with C's yield.** It is also strictly
  less explainable than either, because the printout would have to say why
  *this* node got the relaxation and that one did not, from a rule already
  measured to be wrong 1 time in 5 when it fires at all. The one honest sliver
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
  else.** The most attractive alternative, and unmeasured. ADR 0004's own
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
  payload, deletes §5 and §6, and nothing else in this record changes. That
  is the single highest-value probe named anywhere in this document.
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
- The capability ceiling is unchanged and re-confirmed by measurement:
  `--tools` replaces the built-in set, so a settings file granting `Bash(*)`
  still yields `NO-BASH-TOOL`. Layers 0, 4 and 5 are byte-for-byte identical
  and layer 2 gains one bare name.
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

## What could not be determined

Named with the measurement that would settle each, so a future reader is not
left guessing which of these is an opinion.

1. **Whether user hooks fire, and what they can do.** → (a). The single
   largest unknown; it is the only element of the relaxed set that executes.
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
7. **Whether any of this reproduces off this machine.** No probe settles it;
   it needs a second machine, ideally one whose `settings.json` grants
   nothing, and one on a different CLI version. #130 already asks for exactly
   that, and the invocation-form probes in its first comment are the right
   ones to send.
8. **Whether a given user's CLAUDE.md is safe to feed an unattended node.**
   Unmeasurable in principle — it is a different file for every user. That is
   why §6's answer is disclosure with a hash rather than a check, and why
   `--no-skill-activation` has to exist.
