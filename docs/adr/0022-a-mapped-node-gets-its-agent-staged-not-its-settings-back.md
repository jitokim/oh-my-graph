# ADR 0022 — A mapped node gets its agent STAGED, not its settings back

- Status: **Accepted.** `applyAgentMapping` no longer sets
  `policy.SettingSources = nil`. The matched agent definition is copied into a
  plugin directory oh-my-graph builds for the run, and the mapped node keeps
  ADR 0004's ceiling layer 1 at `""` like every other planned node. **Amended
  2026-08-12 by measurement (l)**: the definition is scanned from
  `~/.claude/agents` only, because copying a *scanned* definition into the
  node's `--agent` made the repository under work a supplier of system prompts
  (§2, `DefaultAgentDirs`); and §7's outstanding acceptance is run.
- Date: 2026-08-12
- Issues: [#161](https://github.com/jitokim/oh-my-graph/issues/161)
- Measurements: `docs/measurements/0017-staged-agent-restores-layer-1.md`
  (measurement **(k)** — 28 spawns, $2.4616, claude 2.1.228, macOS, one
  machine; pre-registered in its own commit before the first spawn) and
  `docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md`
  (measurement **(l)** — 12 spawns, $0.9332, same build and machine; the arm
  (k)'s own pre-registration made a stopping rule and no arm of (k) could
  report on).
- **Amends `0004-auto-mode-tool-ceiling-by-settings-isolation.md`**: E1's
  headline claim, which ADR 0017 §9 and v0.6.0 had to except agent-mapped nodes
  from, applies to them again. ADR 0004's decision text is unchanged and
  carries a dated pointer here. **E2 is not amended** — it is re-confirmed and
  widened (§2).
- **Amends `0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md`
  §9** in one direction only: §9's two shipped parts (the bounded disclosure,
  the per-agent escape) stand, its refusal to lift the skill exclusion **stands
  as a decision nobody has re-taken**, and the *ground* it was refused on is
  gone (§4).
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## 1. Context — the hole, and why one line held it open

Agent mapping (`internal/coordinator/agentmap.go`) maps a planned node onto one
of the user's own Claude Code agents when the node id and the agent name token-
match and the agent's frontmatter `tools:` fit inside the node's planned
`allowed_tools`. It shipped with one line that could not be removed:

```go
policy.SettingSources = nil   // --agent cannot resolve under layer 1 = ""
```

That line traded ADR 0004's entire ceiling for the mapping. Two measurements
priced it:

- **(j), 2026-08-12** — the shipped mapped argv, a node declaring
  `Bash(git *)`, unattended under `--permission-mode dontAsk`: an out-of-scope
  `touch` **ran**, `permission_denials: []`, while the same probe's isolated
  node denied the identical command. ADR 0004's E1 did not hold for a mapped
  node and had not since mapping shipped.
- **(j) again, and this is the larger half** — `nil` loads *the repository's*
  settings too. A `SKILL.md` committed to the repository under work fired
  **3 of 3** in a node whose prompt never mentions skills, its first act being
  the procedure's first instruction; a `.claude/settings.json` committed to
  that repository enabled a plugin of its own choosing into an unattended node,
  and that plugin's skill fired. That is ADR 0012's class — untrusted text
  becoming procedure a node obeys — reached through configuration rather than
  through a prompt.

ADR 0017 §9 kept the skill exclusion, shipped the disclosure and the per-agent
escape, and named one unmeasured direction in a single sentence: *a plugin
directory can carry `agents/` as well as `skills/`, so staging the matched
agent beside the corpus might let a mapped node keep layer 1 at `""`.*
**Nothing measures that**, it said, and proposed nothing.

## 2. What (k) measured

(k) is that sentence, run. The candidate is a named edit of the argv
`runner.buildArgs` really emits for a mapped node — two flag pairs appended,
nothing else moved:

```text
T-REF   ... --agent omg-probe-writer --allowedTools 'Bash(git *)' --tools Bash …
K-CEIL  ... the same, + --setting-sources '' + --plugin-dir <staged+agents/>
```

| what | arm | result |
|---|---|---|
| the ceiling breaches without the fix | `T-REF` (shipped argv, same machine, same build, minutes apart) | **breached 2 of 2**, `permission_denials: []` |
| the ceiling holds with it | `K-CEIL` | **0 of 3**, the refused `Bash` call named in `permission_denials` each time |
| the tool still exists | `K-POS` (in-scope `git init`, candidate argv) | `.git` present **2 of 2** |
| `--agent` resolves, to the STAGED definition | `K-RES` (marker token in the system prompt) | **staged token 3 of 3**, never the repository's |
| ...*because of* the staging | `K-NEG` (identical argv, `agents/` removed) | **exit 1**, `--agent 'x' not found. Available agents: claude, Explore, general-purpose, Plan, statusline-setup` |
| the repository's skills stop reaching it | `K-REPO-D` | repository copy **0 of 3** (3 of 3 under `nil`) |
| ...and the model is not merely silent | `K-REPO-N`, `K-UPONLY` | the model called `Skill`; the CLI answered `Unknown skill: …`, `is_error: true` |
| the staged corpus keeps its own name under 3-way collision | `K-COLLIDE` | staged copy **3 of 3** |
| frontmatter cannot widen past `--tools` | `K-FM-GIT` | no `Bash` record, no `.git`, **0 of 2** |

`K-NEG` is why anything here is attributable. It does three jobs: the
resolution is the staged directory's doing; **E2 is re-confirmed at 2.1.228 and
widened** — under `--setting-sources ""` the CLI's own list of agents it can
see names five built-ins and *neither the user's nor the repository's*
directories, so the repository cannot supply a mapped node's system prompt **by
discovery**; and the failure is **loud** — a node whose agent cannot resolve
exits 1 with the CLI's complaint, which `runner.NodeOutputError` already
surfaces, rather than quietly running unmapped.

**Those three words are a correction, and they are the whole of what (l) found.**
This entry said "cannot supply a mapped node's system prompt" without them, and
generalized a result about *discovery* to the pipeline that ships. Staging is a
second channel, and it is one `--setting-sources ""` structurally cannot shut:
`DefaultAgentDirs` scanned `<cwd>/.claude/agents` too, the project shadowed the
user, and `applyAgentMapping` copied whatever the scan resolved into the
`--plugin-dir`. Measurement (l) ran the arm (k)'s own PREREG made a stopping
rule and no arm of (k) could report on — with the repository's definition
planted and committed, the marker carried the **REPO** token 2 of 2. The scan
scope is fixed in the same commit as this correction (`DefaultAgentDirs` is
`~/.claude/agents` only), and (l)'s `L-FIX` shows the user's definition
resolving 3 of 3 with the repository's copy still committed in the node's cwd.
`docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md`.

All nine of PREREG's conjuncts were met, and none of its five pre-registered
grounds for keeping `nil` fired. Two of them, though, were **not testable by any
arm (k) ran**, and this ADR read their silence as a pass: a repository-supplied
*agent* winning the name is the one (l) had to run, and it is why a stopping
rule needs an arm that can trip it, not only a sentence that describes it.

## 3. Decision

**Stage the matched agent definition into a plugin directory of this run's own,
and leave ceiling layer 1 at `""` on mapped nodes.**

1. **`applyAgentMapping` touches no tool policy.** The `SettingSources = nil`
   line is deleted. A mapped node's policy is `toolPolicyFor`'s, unmodified.
2. **`newAgentStaging` takes a manifest at plan time** — for each applied
   mapping's agent: the source path the scan read it from, and its SHA-256.
   Trusted Go code, from the filesystem, after validation.
3. **`Plan.BindAgentStaging(runDir)`** materializes
   `<run-dir>/agents-plugin/` — `.claude-plugin/plugin.json` and
   `agents/<name>.md` — and appends that directory to each mapped node's
   `PluginDirs`, which `runner.buildArgs` renders as `--plugin-dir`.
4. **`GuardAgentStaging` re-materializes before EVERY spawn**, not only a
   mapped node's: it deletes every path the manifest does not name and restores
   every path whose bytes no longer hash to plan time. The hazard is what the
   *previous* node wrote.
5. **A resumed leg maps nothing** (§5).
6. **Staging failure is no mapping, never a mapped node with nothing to
   resolve.** Every mapping that would have applied is recorded as skipped with
   the reason, the node stays an ordinary planned node, and the plan printout
   says so.
7. **`DefaultAgentDirs` scans `~/.claude/agents` only** (amended 2026-08-12 by
   (l)). Staging changed what a scan hit means: the resolved file becomes the
   node's system prompt through a channel `--setting-sources ""` cannot shut, so
   the scan has to stop where the trust boundary is. It is the scope
   `DefaultSkillDirs` has always had. Cutting the SCAN and not merely the
   STAGING is deliberate — a project file that is still scanned can shadow a
   user agent of the same name, or create the ambiguity that means "no mapping",
   and both are the repository configuring an unattended run.
8. **The plan printout names the source path, size and hash of every staged
   definition.** "Auto-mapped onto your own agents" is a claim about a path on
   disk; (l) is what it looks like when nothing on the screen carries the path.

**It is its own directory, not the skill corpus's**, for two reasons in order
of weight: the ADR 0017 §9 exclusion stays, so a mapped node has no `Skill` in
`--tools` and would be charged ADR 0017 §4's per-invocation prompt tax for
definitions it cannot invoke; and skill staging exists only when the user has
skills at all, so sharing it would make a mapped node's ceiling depend on
whether that user happens to own a `~/.claude/skills` — the invisible coupling
ADR 0004 §4 rejects. This is measurement (k)'s implementation item 2, and it is
also §7's outstanding measurement.

**The agent's own ceiling still binds, and binds harder.** `toolsBeyondCeiling`
reads the definition this process *scanned*; what the CLI reads is the staged
copy; the manifest makes them the same bytes. Before this ADR the CLI re-read
the user's live file at spawn time and any edit in that window was unchecked.
`TestAgentStaging_StagesTheScannedBytes` pins it.

## 4. What this does NOT decide

**The ADR 0017 §9 skill exclusion is not lifted.** §9 refused the lift because
a mapped node's `nil` layer 1 let a skill name resolve against definitions the
repository under work can write. That ground is gone — (k) measured those same
definitions failing to load under `""`. **The exclusion is therefore no longer
a refusal with a number behind it; it is a decision nobody has re-taken**, and
that is a third thing from both "refused" and "coming". Re-deciding it needs
its own record and its own arms, starting from the fact that `K-SKILL-POS`
showed `Skill` live and resolving the staged corpus under this argv, and
`K-COLLIDE` showed the staged copy keeping its name 3 of 3 under a three-way
collision. `noteExclusionCost` prints exactly this, because a user told
"measured and refused" would not think to ask whether the reason still applies.

**Nothing about `CLAUDE.md` or hooks is claimed.** (j) named the repository's
project `CLAUDE.md` and its hooks as *implied and not measured*; (k) did not
measure them either. What can be said is mechanical and narrower: they arrive
by the same default source list `--setting-sources ""` empties, and the two
members of that list this probe *did* plant both stopped loading.

## 5. What the user loses, and what a resumed leg does

**The loss is real and is not a rounding error.** `""` is what makes this work,
and it takes the user's own `CLAUDE.md`, hooks, MCP servers and standing
permission grants out of mapped nodes. Until 2026-08-12 a mapped node was the
one planned node that saw the user's environment — ADR 0004's "Negative /
trade-offs" first bullet, which mapped nodes were the single exception to.
Anyone whose `auto` runs lean on a mapped node reading their `CLAUDE.md` or
firing their hooks will see different behaviour, and `noteAgentMappings` says
so on the screen where the plan is approved. `--no-agent-mapping` and
`--no-agent <name>` are unchanged and are the way back.

**A resumed leg maps nothing**, and prints why. This is ADR 0017 §6's rule
applied to the second mechanism, for the same reason: a resumed leg is a second
process with no in-memory manifest, so the only record it could re-stage a
definition from lives in the run directory the previous leg's nodes could
write. An agent definition is worse to get wrong than a skill — it is the
node's **system prompt**, and it arrives without the model having to choose it.
So `continueRun` calls `coordinator.DropAgentMapping` on a planned graph, the
node runs as an ordinary planned node under the same unchanged ceiling, and
`noteAgentDeescalation` states it. The de-escalation only ever *removes*
capability, which is what makes it safe to do unconditionally.

A hand-written graph's `agent:` is untouched: it is the user's own reviewed
artifact, its node loads the user's settings by design, and it must keep
round-tripping. The discriminator is `len(snap.ToolPolicies) > 0`, the same one
ADR 0016 §4's verify refusal uses.

## 6. Consequences

- ADR 0004's E1 claim holds for every planned node again. `noteCeiling`'s
  agent-mapped **exception is deleted**, not softened — a warning kept past its
  cause teaches readers to discount the next one — and `wiring_test.go` asserts
  its absence as well as the new text's presence.
- The v0.6.0 disclosures that said a mapped node loads the user's settings are
  false as of this change and are rewritten in the same commit:
  `noteAgentMappings`, `noteCeiling`, `noteExclusionCost`, `README.md`,
  `docs/LIMITATIONS.md`, `SECURITY.md`, `DESIGN.md`. **Leaving them would be
  the same defect in the other direction**: a disclosure that under-promises is
  still wrong, and a user who reads "your repository can configure this node"
  makes decisions about what they check in.
- A run directory gains `agents-plugin/` and `agents-plugin.manifest.json`
  whenever a mapping applied. Like the skill sidecar, the manifest is a
  **record and not evidence** — nothing reads it back to decide what a node may
  load.
- No new exec seam. Staging writes files; the four spawners are unchanged and
  `internal/invariants` still passes.
- One more per-spawn reconcile (one file), on runs that mapped anything.

## 7. What is NOT measured, and the acceptance this ADR owed

- **The shipped directory carries `agents/` and no `skills/`; (k)'s carried
  both — and that acceptance is now RUN.** (k) built its candidate by *copying*
  the materialized skill directory and adding `agents/`, because
  `SkillStaging.Materialize` prunes what its manifest does not name. This
  implementation instead stages the agent through its own manifest into its own
  directory — which is what §3 argues for, and which is a directory shape no arm
  of (k) spawned. **Measurement (l) spawned it**: the recorded argv of this
  build, pointing at an `agents/`-only directory under the plugin name
  `oh-my-graph-staged-agents`, resolved `--agent` **3 of 3** (`L-FIX`), with
  `agents/` removed as the control (`L-NEG`, exit 1) — and, under that same
  directory, ADR 0004's E1 held **0 of 3** (`L-CEIL`) against a live `Bash`
  (`L-POS` 2 of 2) and a machine that still breaches under the v0.6.0 argv
  (`L-REF` 1 of 1). Both of §7's remaining deltas from (k)'s argv — the plugin's
  name and the flag position — were carried by those spawns rather than reasoned
  about.
- **The verify→read window is inherited, and its impact is larger here than for
  a skill.** `GuardAgentStaging` re-materializes immediately before every spawn,
  but the scheduler runs ready nodes concurrently: a sibling node can rewrite
  `agents-plugin/agents/<name>.md` between another node's `Materialize()` and the
  CLI's read of it. That is `skillstage.go`'s residual, unchanged, and it is
  bounded the same way — the node still runs under its own unmodified ceiling, so
  `K-FM-GIT`'s result applies and the class is injection, not escalation. What is
  *not* the same is what gets injected: a skill body is opt-in and model-chosen,
  while an agent definition is the system prompt and arrives unasked. §5's resume
  de-escalation draws exactly that distinction across legs and this does not draw
  it within one; closing it needs a per-spawn private directory or a serialized
  materialize, and neither is in this ADR.
- **One machine, one CLI build (2.1.228), one fixture** for everything in §2 and
  for (l). A CLI update could change that a `--plugin-dir` auto-discovers
  `agents/`; if it does, the failure is loud.
- **A project-scoped agent no longer maps at all**, which is a capability cut and
  not only a safety one. Nothing measures how many users keep agents in
  `<repo>/.claude/agents` — the yield ADR 0012 had for the equivalent skill cut
  does not exist here, and the decision was taken on the surface rather than on a
  number. Moving the file to `~/.claude/agents` restores the mapping, and the
  plan printout names the source path of every definition it stages.
- **Ambiguity and built-in collisions are untested.** (k)'s eight mapped nodes
  all matched one agent. A staged agent named like one of the five built-ins
  `K-NEG` printed (`claude`, `Explore`, `general-purpose`, `Plan`,
  `statusline-setup`) is a case nothing has tried, under `nil` or under `""`.
- **No positive control exists for frontmatter widening itself** — the same
  limit ADR 0004 records for E6. `K-FM-GIT`'s 0 of 2 is layers 3 and 5 jointly,
  because that is what `buildArgs` really emits for a `Write`-only node.
