# ADR 0012 — Skill mapping for planned nodes is plan-time inlining, not prompt reference

- Status: Proposed — acceptance additionally gated on the two probes in
  "Required measurements before Accepted" below. **Implemented and shipped
  anyway** (#97, `internal/coordinator/skillmap.go`), while still `Proposed`:
  the gate below was not met before the code landed and is **still owed** as of
  2026-08-05. Neither the (a) steering probe nor the (b) misfire probe has been
  run or recorded — DESIGN.md, "Auto mode", says so out loud ("whether that
  helps or misfires is ADR 0012's required (a)/(b) probes, not assumed here").
  The status stays `Proposed` deliberately: it is the accurate word for a
  decision whose own acceptance criteria are unmet, and flipping it to
  `Accepted` because the code exists would be the drift this line exists to
  prevent. **Still `Proposed` after the 2026-08-05 yield measurement** ("Yield
  measurement", below): that probe paid off a different debt — the plugin
  alternative's condition (3), real planner-authored ids instead of the
  shipped-graph proxy — and it was never the gate. The gate is (a) and (b),
  which measure whether an inlined body *helps*, and neither has been run. A
  number existing is not the number the gate asks for; and this one argues
  against promotion rather than for it, at 5 of 56 ids mapped with one of the
  5 mapped wrongly.
- Date: 2026-08-03 (revised the same day after design review: cap
  recalibrated against the measured corpus, nonce fence + hash provenance,
  `{{` neutralization, `allowed-tools` ceiling-skip cut, user-dir-only scan,
  agent-mapped nodes excluded, disclosure claims corrected to what the code
  does); amended 2026-08-04 — plugin-provided skills measured and **deferred**,
  with the scan's scope now disclosed in the plan printout instead of read as
  silence (§6, Alternatives). The decision itself is unchanged: the scan is
  still `~/.claude/skills` only.
- Sibling of: the subagent auto-mapping design
  (`internal/coordinator/agentmap.go`, [#81]; ADR 0004 §4 update). This ADR
  reuses its matching rule, its disclosure-and-opt-out shape, and its
  choosing-stays-in-trusted-code posture. It does **not** claim the same
  trust surface — inlining copies file content where agent mapping only
  names a file; §5 states the difference instead of papering over it.

> **Update (2026-08-07) — superseded in whole by ADR 0017, contingent on that
> ADR's acceptance test.** The decision text below is unchanged and, until
> ADR 0017's gate passes and its implementation lands, **this is still what
> ships**. What changed is the premise. The 2026-08-03 measurement recorded
> here established that a planned node has neither a skills listing nor a
> `Skill` tool, and its own attribution-nuance paragraph flagged the
> decomposition it could not make: *"if a future proposal ever adds `Skill` to
> `plannedToolAllowlist`, the listing-vs-tool question must be measured
> separately first."* It was, on claude 2.1.223 (#130), with a probe that
> makes the model **actually invoke** a planted skill instead of reporting on
> its own visibility — the same self-reported-versus-verified distinction
> v0.5.0's provenance qualifier draws, and the self-report form returned
> contradictory answers to identical argv. The result: ceiling layer 1
> withholds the **definitions** and layer 3 withholds the **tool**,
> independently; relaxing both restores Claude Code's own description-driven
> activation over the whole corpus. **What an earlier version of this annotation
> also claimed — that the capability ceiling survives the relaxation — is
> retracted (2026-08-07).** E4 holds (`--tools` replaces the built-in set, so a
> tool the node never declared cannot appear), but the probe behind that claim
> used a node declaring no `Bash` and therefore never tested ADR 0004's E1 shape,
> where the user's `Bash(*)` becomes a live allow rule beside a node's narrower
> `Bash(git *)`. This ADR's third Alternative rejected that path as requiring
> *"weakening or decomposing Layer 1 and/or Layer 3… on an unmeasured
> listing-vs-tool attribution"*; the attribution is now measured, **but whether
> the decomposition costs only the user's CLAUDE.md or also the scope ceiling is
> ADR 0017's blocking measurement (g)** — so this Alternative's caution was
> better founded than ADR 0017's first draft allowed, and **until (g) reports,
> this mechanism ships and nothing replaces it.**
>
> Against the 7% this mechanism actually delivers (§"Yield measurement": **5 of
> 56 planner-authored ids = 9%** raw, 4 of 56 ≈ 7% corrected for the
> `artifacts` → `html-artifact` false positive — an earlier version of this
> annotation wrote "9.9%", which contradicts the table below; ADR 0017 also
> cited a "393 id" corpus that does not exist anywhere in this repo),
> activation sees all 35 skills, is conditional where inlining is
> unconditional, has no size cap — so `pre-commit-checklist`, the skill that
> matched the four **best** planner-authored ids and was discarded from every
> one of them, becomes reachable — and needs neither the matcher, the 16 KiB
> cap, the `{{` neutralization nor the nonce fence, all of which exist only to
> make inlining safe. ADR 0017 removes them with it, in one PR, because a node
> holding both mechanisms would receive the same skill twice and become
> unattributable.
>
> Two things survive: **§6's scan** (`scanSkillDirs`/`SkillScan` and its
> printed directories-and-count disclosure), reused to decide whether
> activation is enabled at all and to seal the corpus for a run; and **§5's
> honesty about the surface**, re-derived there for CLAUDE.md rather than for
> skill bodies. Two things are lost and named as losses: per-node prospective
> disclosure (nothing can know before the model does which skill a node will
> use), and the snapshot property (§6's *"a skill edited after planning does
> not silently change an in-flight run"*), for which ADR 0017 substitutes a
> plan-time seal that halts a run whose instruction sources changed under it.
>
> The gate below is **voided, not discharged**. (a) is superseded by
> ADR 0017's measurement (e), which asks the same question of the mechanism
> that replaces this one. (b) is voided because the mechanism it measures is
> gone — the misfire it was written to characterize, `artifacts` →
> `html-artifact`, is precisely the class of error activation is *expected* to
> avoid, and "expected to" has never been the standard this record holds
> itself to. See
> `0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md`.

> **Update (2026-08-07, later the same day) — ADR 0017 is settled, and this
> Alternative's caution was right about the route while being wrong about the
> outcome.** Both of ADR 0017's blocking measurements were taken. **(g)
> confirms the retraction above**: under `--setting-sources user` a node
> declaring `Bash(git *)` ran an out-of-scope `touch` — judged by the file
> appearing, not by self-report — and the identical probe under
> `--setting-sources ""` was denied. So the decomposition *would* have cost the
> scope ceiling, exactly as the caution recorded here suspected, and the
> layer-1 route is dead. **(f) supplies the definitions another way**:
> `--plugin-dir <dir>` (a `.claude-plugin/plugin.json` plus
> `skills/<name>/SKILL.md`) loads a staged corpus with layer 1 left at `""` —
> skill invoked, out-of-scope command still denied, no CLAUDE.md, no MCP, each
> with a positive control. ADR 0017's decision is now built on that, so
> **ceiling layer 1 does not move at all** and only layer 3 gains `Skill`;
> layer 2 does not move either, measured (a skill fires under `dontAsk` with an
> allow list naming only `Bash(git *)`, `permission_denials: []`).
>
> What this changes for the annotation above: the "whether the decomposition
> costs only CLAUDE.md or also the scope ceiling" question is answered — *the
> scope ceiling* — and is now moot, because no route taken loads the user's
> settings. The seal ADR 0017 substitutes for **§6's snapshot property** is
> correspondingly narrower and stronger than described above: it covers a
> corpus oh-my-graph itself staged, re-materialized and verified before every
> node spawn, so a node cannot plant instructions for its successors — a
> prevention rather than the halt-after-the-fact this note first recorded.
>
> **This mechanism still ships until ADR 0017's implementation lands**, and its
> gate is still voided rather than discharged. What is no longer true is that
> it ships *for want of an admissible replacement*: there is one, it is
> measured, and the cost is now a printed number rather than an open question —
> ~6,008 prompt tokens per node invocation to stage 35 skill descriptions,
> against the 7% of node ids this mechanism reaches. See
> `0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md`.

## Context

Users invest heavily in local Claude Code skills — this machine carries 35
under `~/.claude/skills/` (`coding-rules`, `pr-code-review`, `pr-preflight`,
…) — and today an `auto` run gets none of that. The planner does not know the
skills exist, and the planned-node tool ceiling (ADR 0004) was suspected to
block even a prompt-level reference to one. The gap, as stated by the
maintainer: *locally configured skills should be actively used when graphs
are generated; today the planner knows nothing of them and isolation may
block even references.*

Agent auto-mapping already solved the same shape of problem for subagents:
after plan validation, trusted Go code scans the user's own definition files,
matches conservatively on names, prints every decision, and offers an
opt-out. Skills invite the same treatment — but with one open mechanical
question that decides the delivery mechanism. An agent is applied by the CLI
itself (`--agent <name>` at spawn time). A skill is applied by the *model*,
which needs two surfaces: a skills listing injected into its context, and a
`Skill` tool to invoke one. Whether either surface survives the planned-node
stance was unmeasured — DESIGN.md lists "skill/slash-command surfaces are
still not enumerable" as an honest gap, and E2 measured agents discovery
only.

That question was put to a real CLI before this ADR was written; see
"Measurement outcome" below. The answer is unambiguous: **under the exact
argv a planned node runs with, both surfaces are gone. A skill reference in a
planned node's prompt is dead text.** The mechanism must therefore carry the
skill's *content*, not its name.

## Decision

### 1. Delivery is plan-time inlining by trusted Go code; v1 scans user skills only

After a plan has been validated, and after `applyAgentMapping` has run and
rebuilt the graph (its marshal → `graph.Parse` round trip), the coordinator
scans the user's skill directory (`~/.claude/skills/*/SKILL.md`), parses each
file's frontmatter (`name`, `description`), and maps planned nodes onto a
matching skill by **appending the skill's body to the node's prompt** inside
a nonce-fenced, attributed block:

```text
--- skill: coding-rules 7f3a91 (mapped by oh-my-graph from ~/.claude/skills/coding-rules/SKILL.md) ---
<the SKILL.md body, below its frontmatter, with `{{` neutralized per §4>
--- end skill: coding-rules 7f3a91 ---
```

`7f3a91` stands for a per-plan random nonce generated by trusted code and
present in both markers. The fence must carry entropy the fenced text cannot
predict, because an unfenced delimiter is forgeable by the very file it
delimits — 12 of the 35 measured bodies already contain a bare `---` line.
This is the same stance the goal loop already takes when fencing
run-originated text.

Each mapping records on the in-memory Plan (`SkillMappings`, the shape of
`AgentMappings`): the skill name and description, the source path, the
inlined byte count, and the **SHA-256 of the exact inlined text**. The hash
is printed (§6); like `AgentMappings`, the decisions themselves are not
persisted — the durable record is the saved `graph.json`, whose fenced
blocks carry the inlined text and its source path, from which the hash is
recomputable at any time. From this point on the saved `graph.json` mixes
planner-authored text and local-file text inside one `prompt` string; the
fence attribution plus the recomputable hash is what keeps the local-file
part machine-checkably attributable rather than folklore.

Project-dir scanning (`<cwd>/.claude/skills`) is **cut from v1** — it is
100% of the genuinely new injection surface for 0% measured yield; see
Alternatives.

Inlined text rides inside `-p` like the rest of the node prompt. It needs no
tool, no discovery, and no new ceiling layer — **every layer of the ADR 0004
ceiling stays exactly as it is**, and that claim is unscoped precisely
because the one composite where it would not hold is excluded: a node that
received an agent mapping (which drops Layer 1, E2) is never mapped a skill
(§2). This is the decisive advantage over the agent-mapping trade: a mapped
agent must drop Layer 1 so `--agent` can resolve; an inlined skill drops
nothing.

The planner LLM never picks skills. It has no field to name one in (and any
new `graph.Node` field would hit ADR 0004 §2's field-disposition rule before
it hit anything else), and it is never shown the skill inventory. The mapping
happens strictly after validation, from filesystem facts this process read
itself — the same *choosing* posture as `agentmap.go`: selection stays in
trusted code.

Mapping runs exactly once, on the plan path, after `applyAgentMapping`'s
rebuild — so the inlined text lands in the final `plan.Spec`/`graph.json` and
is never itself re-parsed through another mapping pass. `run` and `resume`
execute a saved `graph.json` and never invoke mapping, so an already-inlined
graph cannot be inlined twice; the recorded hashes make any accidental second
pass detectable rather than silent.

### 2. The matching rule is the agent-mapping rule verbatim; agent-mapped nodes are skipped

- Node ids and skill names are tokenized on `-` and `_`.
- A skill matches a node when some node-id token equals some skill-name
  token, or one is a prefix of the other with the prefix at least 4 runes
  long.
- Exactly one matching skill is a candidate; zero or two-plus matches mean
  no mapping. **Ambiguity is silence, not a guess.**

`description` is parsed and carried into the plan printout (so the user sees
*what* got mapped, not just its name) but plays no part in matching — the
rule stays name-only so it stays explainable, exactly as documented in
`agentmap.go`.

One divergence from the agent precedent, and from an earlier draft of this
ADR: skill and agent mapping are **not** independent decisions.
`applyAgentMapping` sets `policy.SettingSources = nil` for a mapped node
(`agentmap.go`) — Layer 1 is gone on that node — and the probe below
establishes "no skills listing" only under the full ceiling; its own
attribution-nuance paragraph records that the probe cannot say *which* layer
cuts the listing. On an agent-mapped node the composite is unmeasured, so v1
refuses it: `skipped: node is agent-mapped (composite with dropped Layer 1
unmeasured)`, recorded and printed like every other refusal. Lifting this
skip requires measuring the composite (a probe under `--agent` with user
settings loaded, asking for the skills listing), not asserting it.

One scale note the agent precedent never surfaced: at 35 skills, name-only
matching has a real ambiguity rate. Simulated over the 28 node ids in the
shipped `graphs/`, 3 are ambiguous (`pr`, `pr-a`, `pr-b` each match both
`pr-code-review` and `pr-preflight`) and go silent. That rate is folded into
§3's yield accounting; "the same trade as agent mapping" would understate it.

### 3. Size cap: 16 KiB, calibrated against the measured corpus; oversize skills are skipped, never truncated

The cap is set by measurement of the corpus that motivates this ADR, not by
assumption. Measured 2026-08-03 over this machine's
`~/.claude/skills/*/SKILL.md` (frontmatter stripped, n = 35):
p50 = 6.7 KiB, p90 = 17.0 KiB, max = 86.6 KiB. Fit against candidate caps:

| cap    | skills that fit |
|--------|-----------------|
| 4 KiB  | 2 / 35          |
| 8 KiB  | 19 / 35         |
| 12 KiB | 26 / 35         |
| 16 KiB | 30 / 35         |
| 32 KiB | 34 / 35         |

Simulating §2's matching rule over the 28 node ids in the shipped `graphs/`:
18 have no match, 3 are ambiguous (silence), 7 find a unique candidate. An
8 KiB cap would kill 6 of those 7 — every `review*` node maps to
`pr-code-review` (11.6 KiB) — leaving **1 of 28** node ids mapped. At 16 KiB
all 7 map, and the 86.6 KiB outlier (`pre-commit-checklist`) stays excluded.

The cap is therefore **16 KiB**. Measured yield, stated as the accepted
cost: 30 of 35 skills are mappable, 7 of 28 shipped node ids actually map,
and the 5 oversize skills are each skipped with a printed reason
(`skipped: body 86.6 KiB exceeds 16 KiB cap`). An earlier draft set 8 KiB
against an assumed "typical 1–4 KiB" body — false on this very corpus (2 of
35) — which would have delivered ~3.5% coverage on the exact setup that
motivates the feature. A cap must be recalibrated if the corpus changes
character; the number is an empirical fit, not a principle.

> **Update (2026-08-05):** every figure in this section is a **proxy** — it
> simulates the rule over shipped-graph node ids, which were named by someone
> who knew the skill names. Measured against 56 planner-authored ids the yield
> is 5 of 56, not the 7-of-28 rate implied here, and the cap is the *dominant*
> loss among ids that match at all (4 of 9), not the harmless exclusion of an
> unmatched outlier this fit assumed. See "Yield measurement (2026-08-05)".
> The 16 KiB number itself is not revised here; what changes is what is known
> about its cost, and revising it is a separate decision this record informs.

The body is paid for on every invocation of every mapped node (including
retries and feedback-edge re-runs), and `-p` is an argv, not a file — the cap
bounds that spend; it does not make it free.

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

### 4. `{{` in a skill body is neutralized at inline time

Inlined text lands in `node.Prompt`, and `node.Prompt` is a handoff template:
`handoff.Interpolate` resolves `{{ ... }}` tokens in it at run time. Without
an explicit disposition, mapping silently promotes skill prose into template
code, with three concrete consequences:

- A well-formed `{{ artifacts.<id> | inline }}` inside a body reads another
  node's artifact **file content** into context with no `Read` tool and no
  ceiling involvement — the inline filter is a plain file read in
  `handoff.go`, and `resolveLocked` does not enforce ancestry. That would
  falsify §5's "text cannot mint tools" bound.
- An unresolvable `{{ inputs.x }}` is a hard `InterpolationError` that kills
  the node at run time.
- A malformed token draws `LintPlaceholders` warnings attributed to the
  *node*, for text the node's author never wrote.

This is not hypothetical: 3 of the 35 measured bodies already carry
`{{ ... }}` tokens (`{{ github.sha }}` in a CI-convention skill,
prompt-template examples in two others).

Disposition: an inlined body is rewritten until no `{{` remains, to a form
`Interpolate` and `LintPlaceholders` cannot recognize (`{ {`), before the
block is appended — *until none remains*, not in one pass, because a single
non-overlapping rewrite lets an odd brace run re-form a live token (`{{{`
becomes `{ {{`). Templating is explicitly **not** a feature of inlined
skill text; neutralizing at the source keeps §5's "text cannot mint tools"
bullet true instead of forcing its deletion. The cosmetic damage to a
rendered `{{ github.sha }}` example inside skill prose is accepted and noted
here so nobody "fixes" it back.

### 5. The injection surface this opens, stated honestly

V1 inlines only the user's own files (`~/.claude/skills`) — the trust class
the design already accepts: the user's own artifacts on the user's own
machine, the same reasoning that keeps the planner call deliberately
non-isolated so it reads the user's CLAUDE.md (E7). Project-dir scanning
would have been genuinely new surface — a cloned repository shipping
instructions into unattended `dontAsk` nodes — and is cut from v1
(Alternatives).

Even user-only, this is **not** the agent-mapping trust story, and this ADR
does not claim parity. Agent mapping never reads a body: it names a file and
lets the CLI apply it under the CLI's own rules. Inlining reads and copies
file *content*, manufacturing prompt text the CLI would never have produced
(the probe below shows the CLI produces nothing here), and it bypasses
Claude Code's own `description`-driven activation gate — a
conditionally-applied procedure becomes unconditional instructions. That is
strictly more surface than the agent precedent. It is held down by these
bounds:

- Text cannot mint tools. Whatever the inlined body says, the node's tool
  set is decided by the ceiling (E4), which this mechanism never touches —
  and §4's neutralization closes the one text-only channel that existed
  (placeholder interpolation). A body may still *direct the model at* tools
  the node lacks, but that is bounded by the runtime ceiling, not by us: a
  tool absent from `--tools` does not exist (E4), so the worst case is the
  node reporting it could not comply — visible in its artifact, not a
  silent widening.
- Every mapping decision is printed before anything runs (§6 states exactly
  what is printed and where the full text lives), and the inlined text is
  snapshotted into the saved plan, where its hash stays recomputable (the
  hash itself is printed, not persisted — §6).
- `--no-skill-mapping` turns the whole mechanism off.
- **Residual:** within the node's declared tools, an inlined body steers
  behaviour — that is its purpose — including wrongly, when the skill does
  not fit the node (measurement (b) below). For user files this is the user
  steering their own run; it stays disclosed, not closed.

Two pieces of ADR 0004 bookkeeping this ADR owes:

- **ADR 0004 §4** rejected the implicit `~/.claude/agents` scan
  "permanently: it would make an `auto` run's behaviour depend on files the
  user forgot they had", and the 2026-08-02 update then relaxed that for
  agents under printed disclosure plus an opt-out flag. This ADR re-opens the
  same clause for skills, and must be honest about scale: with 35 skills and
  a 4-rune prefix rule, files-the-user-forgot is the **modal** case, not the
  edge case — nobody recites what a 35-skill scan will match. Disposition:
  the same relaxation with stronger disclosure — every mapping prints name,
  description, size and hash before the run; ambiguity is silence; the size
  cap and the agent-mapped skip shrink the match set; `--no-skill-mapping`
  restores the old world. ADR 0004 §4 gains a pointer to this ADR when this
  ships.
- **ADR 0004 §2**: no new `graph.Node` field is added, but `Prompt`'s
  disposition changes substance. `field_dispositions_test.go` currently
  records `Prompt` as constrained planner-authored text; after this ADR it is
  planner text *plus trusted-code-appended local file content*. The
  implementation must update that recorded `why` so the reflect-driven test
  keeps telling the truth.

### 6. Disclosure, opt-out, zero-config

Disclosure is specified against what the code actually does. `printPlan`
prints per-node topology, tools and agent, and names the saved spec file as
the place prompts live; it does not print prompts, and the gate path prints
no prompt at all. So the disclosure for skill mapping is: **one line per
decision** in the plan printout, alongside the agent-mapping note —

```text
skill mapped: coding-rules (6.7 KiB, sha256:ab12ab12ab12…) -> impl — "team coding rules for implementation and review"
skill skipped: pre-commit-checklist -> verify: body 86.6 KiB exceeds 16 KiB cap
skill skipped: pr-code-review -> review-a: node is agent-mapped (composite with dropped Layer 1 unmeasured)
```

(One decision format, `<skill> -> <node>: <reason>` for every refusal, so the
agent-mapped line names the refused skill too; sizes print with one decimal
and the hash prints a 12-hex-character prefix — the full hash lives on the
in-memory Plan only. Nothing persists it: the saved spec carries the inlined
text itself, from which the full hash is recomputable, and that recomputation
is the verification path. The known edge of not persisting decisions: a gated
run resumed later — possibly from another terminal — re-displays no mapping
lines, because the plan printout is the only place they exist; what such a
reader has is the saved spec's fenced, attributed blocks.)

— plus the full inlined text in the saved spec/`graph.json`, which the
printout names. A gated run adds no further display: the gate shows the
plan, not prompts. Anyone who wants to read the exact inlined text before
approving reads the named spec file; the printed hash is the integrity link
between the printout and that file. (An earlier draft claimed the gate
displays inlined text; it does not, and this ADR does not depend on it.)

Every decision — mapping made, candidate refused (size, agent-mapped node) —
is recorded on the Plan (`SkillMappings`) and printed before execution. An
ambiguous match is not a decision but silence (§2): no entry, no line —
exactly how agent mapping records it.

**Amendment, 2026-08-04 — the decision list is bracketed by the scan.** The
paragraph above specifies what a *decision* prints and is silent about the
case that turns out to be the majority one. Re-measured against the shipped
`graphs/` (32 node ids now, corpus n=35): 7 map, 3 are ambiguous, **22 find no
candidate at all**. A run with zero mappings therefore printed nothing — and
"scanned your skills, none matched" is indistinguishable in that output from
"never scanned anything", from "your `~/.claude/skills` is missing", and from
the case a real user hits, a corpus that lives somewhere this scan
deliberately does not go (a plugin). The user's only way to tell the
difference was to pay for a plan AND let the graph run, since the mapping
lines are reachable only through `printPlan`.

So a scan that ran now records itself on the Plan (`SkillScan`: the
directories read in scan order — later wins, so a later directory shadows an
earlier one of the same name — the count of usable definitions, and the path
of any definition that lost a name collision) and prints, above its decisions:

```text
  skill scan: 35 skill(s) from /home/you/.claude/skills
  Not scanned: plugin-provided skills (~/.claude/plugins) and project skills (./.claude/skills).
  Both are out of scope in v1 (ADR 0012), so a skill you installed through a plugin maps
  nothing here — that is a stated limit, not a failed match.
```

`Found: 0` is the diagnosable case and the reason the count is printed rather
than implied: the directory is named, so a missing tree, an empty one, or a
corpus in an unscanned location is one line to read instead of a guess.

The count is also the reason a **collision** gets its own line:

```text
  skill shadowed: /home/you/.claude/skills/old-babysit/SKILL.md — another definition declares the same name and wins
```

`Found` is the size of the deduped set, so two definitions sharing a `name:`
print as one and take the count down with them — "35 skill(s)" against 36
skill directories, with no explanation available anywhere. This is not the
future plugin-namespace collision that condition (2) below addresses; it is
available today inside a single `~/.claude/skills`, because `name:` need not
equal the directory it sits in, and there the winner is only whichever
`os.ReadDir` returned later. Deterministic is not the same as tellable.
Nothing about the failure posture changes — a scan that finds nothing is still
a silent no-mapping and never an error, and the note prints whether or not
anything mapped. The line is absent only when no scan happened at all
(`--no-skill-mapping`, or a Coordinator built with no skill directories):
someone who turned the mechanism off is not told about it twice.

`auto --plan-only` (same date) makes that printout reachable without executing
anything: it plans, prints exactly this, and stops. It is not free the way
`run --dry-run` is — there is no plan to inspect until one has been bought —
so it prints the planner call's cost and keeps the spec it paid for, under
`$OMG_HOME/plans/<id>/` rather than `runs/`: nothing ran, and a directory in
`runs/` with no `state.json` reads to `runs list` and `serve` as a broken run.
Opt out with `--no-skill-mapping` (mirroring
`--no-agent-mapping`, including on `chat`). Scan failures — missing
directories, unreadable files, broken frontmatter, a blank name — are silent
no-mapping, never an error: zero-config stays zero-config. Directories are
the caller's to choose (`WithSkillDirs`; tests pass temp dirs), so a
Coordinator built without them never touches the filesystem.

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
`coding-rules` — a skill genuinely installed on the machine, among 35.
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
policy bounds. The same unmeasured-composite discipline is why §2 skips
agent-mapped nodes: on those nodes Layer 1 is already dropped, so this
probe's condition 3 is not established there.

## Required measurements before Accepted

This ADR measures the mechanism it rejects; the mechanism it chooses has no
E-number yet, and "inlining works" is a load-bearing claim. Before Status
flips to Accepted, run and record (with cost and CLI version, like every
prior E-number):

- **(a) Steering probe.** One `claude -p` under the exact planned-node argv
  (`runner.buildArgs`, env scrubbed) whose prompt carries a nonce-fenced
  inlined body, showing the body actually steers the node's behaviour — the
  node follows a distinctive instruction from the body that the bare prompt
  would not produce.
- **(b) Misfire probe.** The same argv with an inlined body whose activation
  condition does not apply to the node's task, and/or whose procedure
  depends on bundled files the prompt does not carry — 4 of the 35 measured
  bodies point at `references/` paths, and `coding-rules` alone bundles 53
  rule files beside its SKILL.md. Record what an unconditionally-inlined,
  half-reachable procedure does to the node's output. This is the realistic
  failure mode of converting a conditionally-activated skill into
  unconditional instructions, and its cost lands on every mapped invocation.

**Status of this gate, 2026-08-05.** Unchanged: neither (a) nor (b) has been
run. The yield measurement recorded below is **not** one of them and does not
partially discharge either — it counts which nodes get a body, where (a) and
(b) ask what a body does to a node once it has one. What it changes is that
(b) now has a real mismapping to run on — `artifacts` → `html-artifact`, a
better subject than a constructed one, and one of the few ids the probe
preserved (see that section's closing note). It supplies (a) with nothing:
the four ids that matched well were discarded at the cap, and the remaining
planner-authored ids were not kept.

## Yield measurement (2026-08-05, claude 2.1.221, oh-my-graph 0.4.1)

Every yield figure this ADR has cited — §3's "7 of 28", the #108 amendment's
"7 of 32", the plugin table's three rows — is a **proxy**. All of them
simulate §2's rule over node ids taken from the shipped `graphs/`, and those
ids were written by an author who knew the skill names. The plugin
alternative's own condition (3) says so and demands the real thing: *decide
it against yield measured on planner-generated node ids, not the shipped-graph
proxy this table and §3 both use.*

That measurement was taken. Twenty goals unrelated to this repo, planned with
`auto --plan-only` (no node executed; the planner calls cost **$6.05** in
total), against this machine's unchanged 35-skill corpus. They produced **56
distinct planner-authored node ids** — the population the feature actually
faces:

| outcome | count | share |
|---|---|---|
| skill **mapped** | 5 | 9% |
| matched, then **discarded at the 16 KiB cap** | 4 | 7% |
| **no candidate at all** | 47 | 84% |
| ambiguous (two-plus matches → silence) | 0 | 0% |

For comparison, agent mapping over the same 56 ids mapped 6 (`test-coder` ×5,
`doc-writer` ×1).

| | proxy (#108, 32 shipped ids) | real (56 planner ids) |
|---|---|---|
| mapped | 7 (22%) | 5 (9%) |
| ambiguous | 3 (9%) | 0 |
| no candidate | 22 (69%) | 47 (84%) |

Three things this record has to be honest about.

**1. The real yield is less than half the proxy figure, and the gap is
structural, not noise.** 9% against 22%. The proxy ids and the skill names
came out of the same head: a maintainer naming a node `review-a` in a repo
whose skill directory contains `pr-code-review` is, without intending to,
measuring their own vocabulary against itself. The planner shares none of
that — it is never shown the inventory (§1), and it names nodes after the
goal's own domain. Every future yield claim about this mechanism must be made
against planner-authored ids; the shipped-graph simulation is not a
conservative estimate of them, it is a biased one, and biased upward. The
0-of-56 ambiguity rate is the same bias seen from the other side: the "3
ambiguous" of the 2026-08-04 amendment's 32 shipped ids (and the same figure
in Consequences) was an artifact of ids drawn from the skill vocabulary, and
on the real population the rule almost never has two candidates to choose
between because it usually has none.

**2. On the population that matches at all, the cap is doing more damage than
the matcher — and it is killing the best matches.** Nine of the 56 ids found a
unique candidate; **4 of those 9 died at the cap**, and they are the strongest
matches in the whole run: `check`, `final-check`, `check-speedup` and
`final-branch-check` each matched `pre-commit-checklist` — a verification
checklist landing on verification nodes, which is the feature working as
designed — and every one was dropped because that body is 86.6 KiB against a
16 KiB cap. (All four matched on §2's ≥4-rune **prefix** rule, `check` against
the skill's `checklist` token, not on token equality. Worth saying plainly,
because it is the same rule that produced the false positive in 3 below: the
prefix relaxation is what makes this mechanism land on nodes at all, and what
makes it land on the wrong ones.) §3 fit the cap on the proxy corpus
and reported the 86.6 KiB outlier as safely excluded; on that proxy the outlier
matched nothing, so the cap's expensive path never fired and its cost was never
in the fit. On real ids that path is 44% of all matches. §3's number is not
wrong for the corpus it was measured on; it was measured on a corpus that could
not exercise it.

**3. Name matching cannot tell `artifacts` from `html-artifact`.** One of the
5 mappings is wrong: a node id `artifacts` matched the `html-artifact` skill on
the 4-rune prefix rule (`artifact`), while meaning something else entirely — a
node collecting a run's outputs, mapped a skill about generating standalone
HTML documents. Corrected for it the real yield is **4 of 56 (7%)**. This is
not a tuning defect: no prefix length distinguishes these two strings, because
they are genuinely lexically related and semantically unrelated. §2 accepted
name-only matching as the price of an explainable rule and predicted misses
("`coding-rules` will never map onto a node named `implement-api`"); it did not
predict false *positives*, and the residual in §5 covers a skill steering a
node wrongly only as a user's own misfire. A misfire the mechanism itself
manufactures is a different claim. It is a limit of the mechanism as specified,
and it belongs in the record here rather than being tuned away.

What this measurement does **not** settle: it counts mappings, not their
effect. Whether an inlined body helps or harms a node it lands on is still
(a) and (b) below, and the mappings this probe produced are the kind of
population those probes should be run against instead of a synthetic one.

**What survived the probe, and what did not.** `--plan-only` executes no node,
so it writes nothing under `~/.oh-my-graph/runs`; the twenty goals and the
full 56-id list were held in the planning session and are **not preserved**.
What this record can vouch for is the aggregate table above and five ids,
quoted in 2 and 3 because they carry the argument: `check`, `final-check`,
`check-speedup`, `final-branch-check` (matched `pre-commit-checklist`, all
four discarded at the cap) and `artifacts` (matched `html-artifact`, wrongly).
The other 51 are counts only, and the run cannot be re-derived from this
document — a re-measurement means paying the planner again.

That is a defect in how the probe was run, not a caveat on its numbers, and it
is the direct counterpart of the rule stated in 1: **a yield claim has to be
made against planner-authored ids, so the ids have to be kept.** Any repeat of
this measurement should write the goal list and the full id → outcome table
into `docs/measurements/` as it goes.

## Consequences

**Positive**

- The user's skill investment reaches `auto` runs, zero-config, with the
  same explainable conservatism as agent mapping: name-token match,
  ambiguity is silence, every decision printed, one flag to turn it off.
  Measured against this machine's corpus and the shipped `graphs/`, 7 of 32
  node ids map under the 16 KiB cap. Whether the inlined text actually
  *improves* those nodes is measurements (a)/(b) — required before
  Accepted, not assumed here.
- **No ceiling layer is weakened**, and unlike the agent-mapping trade the
  claim is unscoped — the one composite where it would not hold (an
  agent-mapped node, Layer 1 dropped) is excluded from mapping (§2).
- The inlined text is snapshotted into the plan artifact with its source
  path (in the fence attribution), so what the user approved is what runs,
  including across resume — size and hash are printed at plan time and stay
  recomputable from the snapshot, machine-checkable provenance for the
  local-file part.
- A candidate that cannot be mapped — oversize, ambiguous, agent-mapped
  node — is refused with a printed reason instead of half-working.

**Negative / trade-offs**

- Token cost: a mapped node pays for the skill body on every invocation
  (including retries and feedback-edge re-runs). The 16 KiB cap is twice the
  earlier draft's — the price of covering the measured corpus — and it
  bounds the spend without making it free.
- Skills larger than the cap (5 of 35), and skills whose value lives in
  bundled reference files (4 of 35 point at `references/`), do not benefit —
  they are skipped with a note, which is honest but still a gap for exactly
  the heaviest skills.
- Inlining is unconditional where Claude Code's own activation is
  conditional: the CLI applies a skill when its description matches the
  task; an inlined body applies always. The misfire cost is measurement (b).
- Name-only matching misses semantically relevant skills (`coding-rules`
  will never map onto a node named `implement-api`) and goes silent on
  ambiguity at a measurable rate (3 of 32 shipped node ids). That is the
  price of an explainable rule, accepted knowingly.
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
- **Refuse skills whose frontmatter `allowed-tools` exceed the node's
  ceiling** (an earlier draft carried this as its §3, mirroring the agent
  ceiling-skip). Cut from v1 on measurement and on a false analogy. Measured:
  0 of 35 skills declare `allowed-tools` — zero yield. Analogy: for an agent,
  `tools:` is load-bearing (the CLI applies it at `--agent` resolution); for
  inlined text it is inert metadata with no mechanical effect, so refusing on
  it is not "the same posture" — it would penalize exactly the skills that
  documented themselves honestly while waving through undeclared bodies that
  demand absent tools (which the runtime ceiling already bounds, E4). If it
  ever returns, it is an *advisory relevance filter* ("this skill says it
  needs tools this node lacks, so it would not work"), never a safety
  control — the ceiling does not depend on it.
- **Scanning plugin-provided skills (`~/.claude/plugins/...`) in v1.**
  **Deferred on measurement, 2026-08-04** (claude 2.1.221, oh-my-graph 0.4.1),
  not on the project-scan reasoning below — which does not extend to plugins
  and should not be cited as if it did. `/plugin install` is an explicit,
  per-plugin, persisted act with a second `enabledPlugins` gate; a cloned
  repository's `.claude/skills` is drive-by. Trust class is the same as
  `~/.claude/skills`, which on the measuring machine is itself a symlink into
  a third-party dotfiles checkout.

  What decides it is yield, against this ADR's own standard for cutting the
  project scan ("new surface for 0% measured yield"). Simulating §2's rule
  over the 32 node ids in the shipped `graphs/`:

  | pool | mapped | ambiguous | no match |
  |---|---|---|---|
  | user skills only (35) — today | 7 | 3 | 22 |
  | + enabled plugin skills (54) | **7** | 3 | 22 |
  | plugin skills only (20) | 0 | 0 | 32 |

  **Zero additional mappings.** Plugin skill names are product nouns
  (`mem-search`, `wowerpoint`, `pathfinder`); node ids are workflow verbs.
  Shipping the scan today would add surface for the same 0% this ADR already
  refused it for once. The measurement also found a name collision that a
  bare-name pool would resolve *silently* (`babysit` exists in both
  `~/.claude/skills` and a plugin, so 35+20 becomes 54 and one skill
  disappears with no printed line) — silence being exactly what the §6
  amendment above is repairing.

  Conditions for a future yes, all three: (1) read the live set from
  `~/.claude/plugins/installed_plugins.json` ∩ `enabledPlugins` — never a
  glob, which on the measuring machine hits 127 `SKILL.md` files of which 20
  are live, the rest stale cached versions and raw marketplace checkouts —
  with the same silent-failure posture `scanSkillDirs` has, since that file
  carries `"version": 2` and is not a documented API; (2) key the map on
  `plugin:skill` so nothing shadows silently; (3) decide it against yield
  measured on **planner-generated** node ids, not the shipped-graph proxy this
  table and §3 both use. *(2026-08-05: such a population was measured once —
  56 ids, see "Yield measurement" — but only over the user-skill pool, and the
  ids themselves were not preserved past the planning session. Condition (3)
  therefore still costs a fresh planner spend, and the run that discharges it
  should record its goals and its full id list, per that section's closing
  note. What the measurement does establish for this row is that the proxy
  yields it argues from are biased upward, so a plugin-pool figure simulated
  the same way should not be believed either.)* The provenance asymmetry that
  remains — a plugin with
  `autoUpdate: true` can rewrite its bodies between runs, where
  `~/.claude/skills` changes only when the user changes it — is already
  answered by the machinery in §1 (printed SHA-256, snapshot into
  `graph.json`, source path in the fence), since a plugin path carries plugin
  and version. Until then the exclusion is **printed on every plan**, not
  left as an unexplained absence of mappings (§6 amendment).

  Plugin-provided *agents* are a separate and later decision: agent mapping
  drops ceiling Layer 1, and it is blocked on an unmeasured question (whether
  `--agent <plugin>:<name>` resolves at all), which needs a paid spawn.
- **Scanning `<cwd>/.claude/skills` in v1.** Cut. Project scanning is 100%
  of the genuinely new injection surface — a cloned repository shipping
  repo-authored instructions into unattended `dontAsk` nodes — for 0%
  measured yield: no `.claude/skills` directory exists in this repo or any
  local checkout measured. An earlier draft kept it "for asymmetry" with
  agent mapping's project-dir scan; that is an aesthetic argument set
  against a security one. Add project scanning when a real project skill
  exists to justify it, with its own measurement and README/SECURITY.md
  disclosure of the untrusted-checkout residual.
