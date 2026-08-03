# ADR 0012 — User-skill mapping for planned nodes is plan-time inlining, not prompt reference

- Status: Proposed
- Date: 2026-08-03
- Sibling of: the subagent auto-mapping design
  (`internal/coordinator/agentmap.go`, [#81]; ADR 0004 §4 update). This ADR
  deliberately reuses its trust posture, matching rule, disclosure and opt-out
  shape, and diverges only where skills are mechanically different from
  agents.

## Context

Users invest heavily in local Claude Code skills — this machine carries 30+
under `~/.claude/skills/` (`coding-rules`, `code-review`, `pr-preflight`, …) —
and today an `auto` run gets none of that. The planner does not know the
skills exist, and the planned-node tool ceiling (ADR 0004) was suspected to
block even a prompt-level reference to one. The gap, as stated by the
maintainer: *locally configured skills should be actively used when graphs
are generated; today the planner knows nothing of them and isolation may
block even references.*

Agent auto-mapping already solved the same shape of problem for subagents:
after plan validation, trusted Go code scans the user's own definition files,
matches conservatively on names, refuses anything wider than the node's tool
ceiling, prints every decision, and offers an opt-out. Skills invite the same
treatment — but with one open mechanical question that decides the delivery
mechanism. An agent is applied by the CLI itself (`--agent <name>` at spawn
time). A skill is applied by the *model*, which needs two surfaces: a skills
listing injected into its context, and a `Skill` tool to invoke one. Whether
either surface survives the planned-node stance was unmeasured — DESIGN.md
lists "skill/slash-command surfaces are still not enumerable" as an honest
gap, and E2 measured agents discovery only.

That question was put to a real CLI before this ADR was written; see
"Measurement outcome" below. The answer is unambiguous: **under the exact
argv a planned node runs with, both surfaces are gone. A skill reference in a
planned node's prompt is dead text.** The mechanism must therefore carry the
skill's *content*, not its name.

## Decision

### 1. Delivery is plan-time inlining by trusted Go code

After a plan has been validated — the same post-validation slot where agent
mapping runs — the coordinator scans the user's skill directories
(`~/.claude/skills/*/SKILL.md` and `<cwd>/.claude/skills/*/SKILL.md`, project
winning over user on a name collision, mirroring `DefaultAgentDirs`), parses
each file's frontmatter (`name`, `description`, optional `allowed-tools`),
and maps planned nodes onto a matching skill by **appending the skill's body
to the node's prompt** inside a clearly-attributed delimiter block:

```
--- skill: coding-rules (auto-inlined by oh-my-graph from ~/.claude/skills/coding-rules/SKILL.md) ---
<the SKILL.md body, below its frontmatter>
--- end skill: coding-rules ---
```

Inlined text rides inside `-p` like the rest of the node prompt. It needs no
tool, no discovery, and no new ceiling layer — **every layer of the ADR 0004
ceiling stays exactly as it is.** This is the decisive advantage over the
agent-mapping trade: a mapped agent must drop Layer 1 so `--agent` can
resolve (E2); an inlined skill drops nothing.

The planner LLM never picks skills. It has no field to name one in (and any
new `graph.Node` field would hit ADR 0004 §2's field-disposition rule before
it hit anything else), and it is never shown the skill inventory. The mapping
happens strictly after validation, from filesystem facts this process read
itself — the same trust posture as `agentmap.go`.

### 2. The matching rule is the agent-mapping rule, verbatim

- Node ids and skill names are tokenized on `-` and `_`.
- A skill matches a node when some node-id token equals some skill-name
  token, or one is a prefix of the other with the prefix at least 4 runes
  long.
- Exactly one matching skill is a candidate; zero or two-plus matches mean
  no mapping. **Ambiguity is silence, not a guess.**

`description` is parsed and carried into the plan printout (so the user sees
*what* got inlined, not just its name) but plays no part in matching — the
rule stays name-only so it stays explainable, exactly as documented in
`agentmap.go`. Skill and agent mapping are independent decisions: a node may
get both (`agent:` decides *who* runs it; the inlined skill text says *how*),
and both are printed.

### 3. Tool-ceiling interaction: a skill needing tools the ceiling denies is skipped

If a candidate skill's frontmatter declares `allowed-tools` (parsed with the
same scalar-or-sequence normalization as agent `tools:`), every entry must
already be present, by exact rule string, in the node's own planned
`allowed_tools` — the list that becomes `--tools`/`--allowedTools`. Any
entry beyond that set refuses the mapping, and the refusal is recorded and
printed (`skipped: tools exceed ceiling: Bash, WebFetch`) — the same posture,
wording and code shape as the agent ceiling-skip (`toolsBeyondCeiling`).

A skill declaring no `allowed-tools` is mappable as-is. Its *body* may still
direct the model at tools the node lacks, but that is bounded by the runtime
ceiling, not by us: a tool absent from `--tools` does not exist (E4), so the
worst case is the node reporting it could not comply — visible in its
artifact, not a silent widening.

### 4. Size cap: oversize skills are skipped, never truncated

A skill body larger than **8 KiB** produces no inlining; the skip is recorded
and printed (`skipped: body 41 KiB exceeds 8 KiB inline cap`). 8 KiB is
generous against typical SKILL.md bodies (1–4 KiB) while keeping per-node
prompt bloat bounded — the body is paid for on every invocation of every
mapped node, and `-p` is an argv, not a file.

Truncating to fit is **rejected**: a skill body is instructions, and
instructions cut mid-way can invert meaning (a "never do X, unless…" clause
severed before its exception becomes an absolute prohibition the author never
wrote). A skill's bundled reference files (`references/*.md` etc.) are never
inlined either — Claude Code's own progressive-disclosure contract makes
SKILL.md the designed standalone layer, and inlining an unbounded file tree
has no principled cap. No claim is made about whether a mapped node can
`Read` those bundled files at run time; if its declared tools happen to allow
it, that is the ceiling working as specified, not a mechanism this ADR
relies on.

### 5. The injection surface this opens, stated honestly

Inlining moves file content into the prompt of an unattended `dontAsk` node.
For **user** skills (`~/.claude/skills`) this is the trust class the design
already accepts: the user's own artifacts on the user's own machine — the
same reasoning that keeps the planner call deliberately non-isolated so it
reads the user's CLAUDE.md (E7).

For **project** skills (`<cwd>/.claude/skills`) it is genuinely new surface:
a cloned repository can ship a skill directory, and running `oh-my-graph
auto` inside that checkout would inline repo-authored instructions into
unattended nodes. Three bounds apply, and one residual does not close:

- Text cannot mint tools. Whatever the inlined body says, the node's tool
  set is decided by the ceiling (E4), which this mechanism never touches.
- Every inlining is disclosed in the plan printout before anything runs, and
  a gated run shows it at the gate.
- `--no-skill-mapping` turns the whole mechanism off.
- **Residual:** within the node's declared tools, a hostile body can still
  steer behaviour (a node holding `Write` + `Bash(git *)` can be steered
  into writing something the goal never asked for). This is the same
  residual the project already carries for a hostile project CLAUDE.md on
  the hand-written path and for project-dir agents in `agentmap.go`'s scan
  of `<cwd>/.claude/agents` — disclosed, not closed. Anyone running `auto`
  in an untrusted checkout is trusting that checkout's `.claude/` directory;
  README must say so where it says the rest.

### 6. Disclosure, opt-out, zero-config

Every decision — mapping made, candidate refused (ceiling, size) — is
recorded on the Plan (`SkillMappings`, the shape of `AgentMappings`) and
printed before execution, alongside the agent-mapping note. Opt out with
`--no-skill-mapping` (mirroring `--no-agent-mapping`, including on `chat`).
Scan failures — missing directories, unreadable files, broken frontmatter, a
blank name — are silent no-mapping, never an error: zero-config stays
zero-config. Directories are the caller's to choose (`WithSkillDirs`; tests
pass temp dirs), so a Coordinator built without them never touches the
filesystem.

Because the inlined text becomes part of the node prompt inside `plan.Spec`
and the saved `graph.json`, a resumed or re-run plan keeps exactly the text
it was approved with — a skill edited after planning does not silently change
an in-flight run. That snapshot behaviour is a feature (reproducibility, and
the printed plan is the truth), and it is the same property agent mapping
bought by re-parsing the rebuilt graph.

## The rejected mechanism: prompt reference

The alternative was to write "apply your `coding-rules` skill" into the node
prompt and let the CLI's own skill machinery resolve it. It is rejected on
measurement, not on taste. Under the planned-node stance the reference is
dead text with two failure modes, the second worse than the first: best
case the model ignores it; worst case the model **improvises what it thinks
that skill says** — a hallucination trigger wearing the name of a real,
trusted, user-authored artifact. Three independent kill conditions, any one
sufficient:

1. **Layer 0** — `Skill` is not in `coordinator.plannedToolAllowlist`
   (coordinator.go), so a planned node cannot even declare it;
   `validatePlannedNodeTools` rejects the plan.
2. **Layer 3** — `narrowedToolsFor` renders `--tools` as exactly the declared
   bare names, and E4 established `--tools` REPLACES the built-in set. An
   undeclared tool does not exist; `--allowedTools` cannot resurrect it.
3. **Measured** — the skills *listing* is also absent from the node's context
   (below), so the model does not even know the skill exists to ask about.

## Measurement outcome (2026-08-03, claude 2.1.220)

Conditions 1–2 above are code-visible. Condition 3 — whether the
context-injected skills listing survives `--setting-sources ""` — was not:
E2 measured agents discovery only, and `--help`'s skill prose all hangs off
`--bare`/`--safe-mode`, flags this tool never passes (and ADR 0004 already
caught `--help` prose being wrong once). So it was measured: one `claude -p`
invocation (subscription OAuth, `provider: "firstParty"`, $0.0393), argv
exactly as `runner.buildArgs` emits for a planned node that declared `Read`,
env scrubbed per `childenv.Scrub`:

```sh
claude -p '<probe>' --output-format json --permission-mode dontAsk \
  --setting-sources "" --allowedTools "Read" --tools "Read" \
  --strict-mcp-config \
  --disallowedTools "Bash,Edit,Write,MultiEdit,NotebookEdit,WebFetch,WebSearch,Task,Agent"
```

The probe asked the model to list its tools, list every skill it can see
(literally `NO SKILLS LISTED` if none), and actually attempt to apply
`coding-rules` — a skill genuinely installed on the machine, among 30+.
Verbatim result:

> 1. Tools available: `Read` (that is the complete set).
> 2. NO SKILLS LISTED
> 3. I cannot apply a coding-rules skill: no such skill is visible in my
>    context, and I have no skill-invoking tool (no Skill/AgentTool/Bash)
>    available — only `Read`.

Envelope evidence beyond the narration: `num_turns: 1` with zero tool calls
(E4's signature for a nonexistent tool — the model never even attempted an
invocation) and `permission_denials: []` (nothing was denied; the surfaces
simply do not exist).

**Attribution nuance, recorded for a future reader:** the probe measures the
composite stance end to end and does not isolate *which* layer cuts the
skills listing (Layer 1 by analogy with E2's agents result is the plausible
one, but the listing could also be tied to the Skill tool's presence, which
Layer 3 removes). This ADR does not need the decomposition — both layers are
load-bearing parts of the ceiling, so under any attribution the reference
stays dead. But if a future proposal ever adds `Skill` to
`plannedToolAllowlist`, the listing-vs-tool question must be measured
separately first — and note that `Skill` would be a semantic hole in the
ceiling regardless: a skill body is arbitrary instructions, which no tool
policy bounds.

## Consequences

**Positive**

- The user's skill investment finally reaches `auto` runs, zero-config, with
  the same explainable conservatism as agent mapping: name-token match,
  ambiguity is silence, every decision printed, one flag to turn it off.
- **No ceiling layer is weakened.** Unlike agent mapping (which drops Layer 1
  per mapped node, disclosed), inlining changes nothing about isolation,
  grants, narrowing, MCP or the residual deny list.
- The inlined text is snapshotted into the plan artifact, so what the user
  approved is what runs, including across resume.
- A skill demanding more tools than the node's ceiling is refused with a
  printed reason instead of half-working — the same failure honesty as the
  agent ceiling-skip.

**Negative / trade-offs**

- Token cost: a mapped node pays for the skill body on every invocation
  (including retries and feedback-edge re-runs). The 8 KiB cap bounds this
  but does not make it free.
- Skills larger than the cap, and skills whose value lives in bundled
  reference files, do not benefit — they are skipped with a note, which is
  honest but still a gap for exactly the heaviest skills.
- Name-only matching misses semantically relevant skills: `coding-rules`
  will never map onto a node named `implement-api`. That is the price of an
  explainable rule, accepted knowingly (same trade as agent mapping).
- Project-dir scanning adds a prompt-injection surface for untrusted
  checkouts (§5). Bounded by the ceiling and disclosure, but the residual —
  in-ceiling steering — is real and must be documented in README/SECURITY.md
  when this ships.
- Inlined text is a fork of the skill: improvements to the SKILL.md after
  plan time do not reach an already-planned run. Correct for
  reproducibility, occasionally surprising.

## Alternatives considered

- **Prompt reference ("apply your X skill").** Rejected on measurement — see
  "The rejected mechanism" above. Dead text at best, a hallucination trigger
  wearing a trusted name at worst.
- **Add `Skill` to `plannedToolAllowlist` and let nodes invoke skills
  natively.** Rejected. It requires weakening or decomposing Layer 1 and/or
  Layer 3 for mapped nodes, on an unmeasured listing-vs-tool attribution;
  and even measured, `Skill` reintroduces unbounded instructions inside the
  ceiling by design. Inlining gets the content with none of that.
- **Give the planner the skill inventory and let it choose.** Rejected — the
  planner is an untrusted producer, and letting its output select which
  local file gets injected into node prompts is the exact hole
  `validatePlannedNodeAgent` closes for agents. Choosing is the trusted
  code's job.
- **Description or LLM-based semantic matching.** Rejected for the same
  reason `agentmap.go` matches name-only: fuzzy scoring is unexplainable in
  a printed plan, and an LLM matcher is untrusted choice again by another
  door.
- **Truncate oversize skills to the cap.** Rejected — severed instructions
  can invert meaning; no skill is safer than half a skill.
- **Inline bundled reference files along with SKILL.md.** Rejected —
  unbounded size against an argv-borne prompt, and SKILL.md is already the
  format's designed standalone layer.
- **Scan user skills only, excluding `<cwd>/.claude/skills`.** Considered as
  an injection-surface reduction, rejected for asymmetry: project agents are
  already scanned by agent mapping, project skills are how teams share
  procedure, and the surface is bounded by the ceiling plus printed
  disclosure. Revisit if a real incident shows the disclosure is not enough.
