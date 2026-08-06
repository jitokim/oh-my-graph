# ADR 0017 — A planned node gets Claude Code's own skill activation, from a plugin directory oh-my-graph stages

- Status: **Proposed — implemented as of 2026-08-07; still `Proposed`, and
  deliberately so.** Both blocking measurements named by the 2026-08-07 review
  were taken and independently re-run: **(g)** confirms the retraction —
  relaxing ceiling layer 1 forfeits the scope ceiling — and **(f)** shows
  `--plugin-dir` supplies the skill definitions with layer 1 left at `""`. The
  decision below is written around `--plugin-dir`, which is the mechanism that
  survived. **Layer 1 is not touched by this ADR.** What still gates `Accepted`
  is the acceptance test plus measurements (b) and (e), none of which the code
  landing changes: unlike ADR 0012, this record permits the code to be written
  and shipped behind its printed disclosure, not to be called done. ADR 0012
  shipped while `Proposed` with its own acceptance probes unrun, was measured at
  7% two days later, and nobody had ever established that an inlined body helps
  a node at all. Every number here is **claude 2.1.223, macOS, one machine**.
- Date: 2026-08-07
- Issues: [#130](https://github.com/jitokim/oh-my-graph/issues/130)
- Supersedes, **in code as of 2026-08-07**:
  `0012-skill-mapping-is-plan-time-inlining.md` in whole (§1–§5; §6's scan and
  its printed disclosure survive, re-pointed — see Decision §8). ADR 0012's
  decision text is unchanged; it carries a dated pointer here.
- Amends in part: `0004-auto-mode-tool-ceiling-by-settings-isolation.md` §1,
  **layer 3 only**, for coordinator-planned nodes. ADR 0004's decision text is
  unchanged; it carries a dated pointer here. **ADR 0004's E1 stands
  unamended** — measurement (g) re-ran it under this ADR's own argv and it
  held, which is why layer 1 stays `""`.
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

### The two layers, measured separately

ADR 0012's probe measured the composite and said so honestly: *"the probe
measures the composite stance end to end and does not isolate which layer cuts
the skills listing… if a future proposal ever adds `Skill` to
`plannedToolAllowlist`, the listing-vs-tool question must be measured
separately first."* That decomposition was done, with a probe that makes the
model **actually invoke** a planted skill and emit its marker — not one that
asks the model whether it can see skills, which was tried first and returned
contradictory answers to identical argv:

| argv | skill runs? |
|---|---|
| `--setting-sources ""` alone | no — the definitions are not loaded |
| `--tools Read` alone | no — there is no `Skill` **tool** |
| `--setting-sources ""` + `--tools Read,Skill` | no — naming the tool does not load the definitions |
| `--setting-sources user`, no `--tools` | **yes** |
| `--setting-sources user` + `--tools Read,Skill` | **yes** |

Two independent layers block skills for two different reasons: **layer 1
withholds the definitions, layer 3 withholds the tool.** `Skill(*)` in the
user's `settings.json` is irrelevant to either — a permission approves a tool,
it does not supply skill *definitions*.

That table's only route to the definitions was `--setting-sources user`, and
the whole of the first draft was built on it. It is dead. What replaces it is
a third source of definitions the first draft never considered.

### Measurement (g) — the layer-1 route forfeits the scope ceiling

**Recorded 2026-08-07, claude 2.1.223, macOS; re-run from scratch before this
rewrite.** ADR 0004's E1 shape: a node that **declares** `Bash(git *)`,
attempting an out-of-scope command, judged by **whether the file appears** —
not by what the model says about itself.

```text
claude -p "Run this exact shell command with the Bash tool: touch /tmp/OMG-G-PROBE-USER" \
  --setting-sources user --permission-mode dontAsk \
  --tools Read,Bash --allowedTools "Bash(git *)" --strict-mcp-config
  -> the model reports success; ls shows the file CREATED

same probe, --setting-sources ""
  -> "Bash is blocked in this session"; ls shows NO SUCH FILE
```

**`--tools` bounds tool NAMES, not SCOPES.** Layer 1 is the only thing holding
a declared `Bash(git *)` to git, because the measuring machine's
`~/.claude/settings.json` grants `Bash(*)` among 28 allow rules. Trace it:
`narrowedToolsFor` drops the scope so layer 3 emits `--tools Bash,…` and
**`Bash` exists**; `disallowedToolsFor` sees `Bash` declared so layer 5 is
inert; layer 2 emits `--allowedTools "Bash(git *)"`; and layer 1 = `"user"`
makes the user's `Bash(*)` a live allow rule **alongside** ours. The doc
comment on `toolPolicyFor` already said so:

> Layer 1 is the load-bearing one. Permission rules are matched from every
> loaded source, so a standing `Bash(*)` in the user's ~/.claude/settings.json
> was matching **before this node's own narrower `Bash(git *)` ever mattered —
> which is why layer 2 used to be a declaration rather than a limit.**

The earlier "ceiling survives" claim came from a malformed probe whose node
**declared no Bash at all**, so `--tools Read,Skill` omitted `Bash` and the
tool did not exist. It re-proved E4 — *an allow-rule approves a tool that
exists, it does not create one* — which nobody disputed. The ADR retracted
that claim on review; (g) confirms the retraction empirically.

**The concrete regression, stated plainly: under the layer-1 route an
unattended `dontAsk` planned node declaring `Bash(git *)` can run `rm -rf`
again.** That is the headline gap ADR 0004 shipped layer 1 to close. It is not
a price this project pays for skills, and §1 does not pay it.

### Measurement (f) — `--plugin-dir` supplies the definitions and keeps every layer

**Recorded 2026-08-07, claude 2.1.223, macOS; re-run from scratch before this
rewrite.** The plugin shape is two files:

```text
<dir>/.claude-plugin/plugin.json      {"name","version","description"}
<dir>/skills/<name>/SKILL.md
```

Four arms, all under `--setting-sources "" --plugin-dir <dir> --strict-mcp-config
--permission-mode dontAsk`:

| arm | argv detail | result |
|---|---|---|
| **skill invoked** | `--tools Skill` (no `Read`) | **YES** — the planted token `ZQ7X-PLUGINDIR-4413` came back. `Read` was withheld deliberately: the token is on disk, and with no file-reading tool the *only* provenance is the `Skill` tool. `--tools Read,Skill` returns it too. |
| **scope ceiling** | `--tools Read,Bash,Skill --allowedTools "Bash(git *)"`, out-of-scope `touch` | **DENIED** — file absent, and the CLI's own envelope carries `permission_denials: [{"tool_name":"Bash","tool_input":{"command":"touch /tmp/OMG-DENIAL-CHECK"}}]`. |
| **CLAUDE.md** | asked for two facts that appear only in `~/.claude/CLAUDE.md` | **NO** — `NO-CLAUDEMD`. **Positive control:** the identical prompt under `--setting-sources user` quoted both facts back. |
| **MCP** | asked to list `mcp__*` tools | **NONE** — `NO-MCP`. **Positive control:** `--setting-sources user` without `--strict-mcp-config` listed 14 `mcp__plugin_claude-mem_mcp-search__*` tools. |

Two of those controls matter more than the results they license, because the
first draft of this ADR was caught asserting an uncontrolled `NO` for MCP and
that is exactly the error being avoided here. Both arms now have a control
that fires.

One further isolation, since it changes what layer 4 is worth: `--plugin-dir`
+ `--setting-sources ""` **without** `--strict-mcp-config` also returns
`NO-MCP`. **Layer 1 is what bounds MCP for a planned node; layer 4 is
redundant belt-and-braces here.** It stays — the layers are deliberately
independent mechanisms (`deniableTools`' doc comment) — but the record should
not credit it with work layer 1 is doing.

Two more facts from the same session, both load-bearing below:

- **Layer 3 still binds.** `--plugin-dir <dir> --tools Read` (no `Skill`)
  returns `NO-SKILL`. The definitions are present and the tool is absent, so
  layer 3 is still a real ceiling row and §1 still has to move it.
- **Measurement (c) is settled, and the answer is no.** Under
  `--permission-mode dontAsk` with `--allowedTools "Bash(git *)"` — an allow
  list that does **not** name `Skill` — the skill ran and
  `permission_denials` was `[]`. **Layer 2 does not need `Skill`.** The first
  draft's §3 added it defensively against a deny it could not see coming; the
  deny does not come, so the grant is not made. See §3.

### What this settles, and what is now moot

The crux the first draft argued at length — *is it acceptable to read the
user's CLAUDE.md into an unattended node* — **is moot**, because the only path
that raised it is dead. Under `--plugin-dir` layer 1 stays `""`, so none of it
loads: no CLAUDE.md, no user permission rules, no hooks, no `model` pin, no
`env` block, no `additionalDirectories`, no `apiKeyHelper`, no `statusLine`.
The first draft's §4, §4a and §6, and its measurements (a), (d) and (h) and
open questions 6b and 6c, all existed to price that load. They are **struck as
unnecessary, not answered** — which is the correct disposition to record,
because a future proposal that reaches for `--setting-sources user` again
inherits every one of them plus (g).

The question is therefore no longer *what does activation cost the ceiling*.
It costs the ceiling nothing. The remaining questions are about the staged
directory itself: what goes in it, where it lives, whether it survives a
resume, what the plan can still promise, and what happens to the mechanism it
replaces. Those are §§1–8.

## Decision

### 1. oh-my-graph stages a plugin directory; layers 1, 2, 4 and 5 do not move

A **third post-validation mutation step** — `applySkillActivation(&plan)`,
placed immediately **after** `applySkillMapping`'s replacement — adjusts the
**policy**, never the graph:

- **layer 1: unchanged, `""`.** This is the decision, not an omission.
- layer 3: `"Skill"` is appended to `Tools`.
- **new argv, not a ceiling layer:** `--plugin-dir <staged-dir>`, carried on a
  new `runner.ToolPolicy.PluginDirs` field.
- layer 2: unchanged. Measurement (c) says `Skill` needs no allow rule.
- layers 0, 4, 5: byte-for-byte unchanged.

> **Correction (2026-08-07).** The first draft said *"`toolPolicyFor` gains one
> post-validation adjustment"*, and that location is wrong twice over.
> `toolPolicyFor` is called from `toolPoliciesByNode` **before** either mapping
> runs, and `applyAgentMapping` afterwards sets `policy.SettingSources = nil` on
> mapped nodes. Implementing §1 there would hand an agent-mapped node `Skill` in
> `Tools` **plus** `nil` setting sources — wider than anything decided here. It
> also cannot work: `toolPolicyFor` is a pure function of one `graph.Node` and
> cannot see `SkillScan`, which §1's own predicate requires. Hence a distinct
> step, ordered last, that reads the scan and skips agent-mapped nodes.

The relaxation is applied **only when the skill scan (`SkillScan`, kept from
ADR 0012 §6) found at least one usable definition.** A user with no
`~/.claude/skills` pays nothing for a capability there is nothing to exercise,
and the printed plan says which of the two worlds this run is in. This is a
per-*run* predicate over a filesystem fact, not a per-*node* predicate over a
guess about relevance — the difference is §Alternatives B.

Hand-written graphs are untouched: they never had layers 1 and 3, they get
skills from the user's own settings already, and they must not be handed a
staged directory that would shadow the real one.

### 2. `Skill` is injected after validation; layer 0 does not learn the word

`plannedToolAllowlist` is **not** extended. ADR 0016-build-evidence §1 (there
are two ADRs numbered 0016; every "ADR 0016" citation in this record means
`0016-build-evidence-is-a-user-supplied-engine-command.md`) narrowed what that
list answers — *"what class of tool is safe for unattended planner output at
all"* — and a planner that can name `Skill` in `allowed_tools` is a planner
that can select which of the user's local files gets loaded into a node it
authored. That is the hole `validatePlannedNodeAgent` closes for agents, and
it stays closed.

So the grant is a **policy-level act, invisible to the graph**: `node.
AllowedTools` never contains `Skill`, `validatePlannedNodeTools` never sees
it, the saved `graph.json` never carries it. The same posture as
`agentmap.go`'s `agent:` and ADR 0016-build-evidence §2's injected
verification: *choosing stays in trusted code, and what the planner may
declare does not move.*

The consequence, stated so it is not discovered: the durable record of the
grant is `state.json`'s `tool_policies`, not the graph. A reader holding only
`graph.json` cannot tell an activation-enabled run from an isolated one.

### 3. Layer 2 stays out of it — measured, not assumed

The first draft added `Skill` to `AllowedTools` as insurance: under `dontAsk`
a call whose rule evaluation lands on *ask* becomes a **deny** (ADR 0004,
Context), so a `Skill` invocation matching no allow rule could be denied
silently, and *"that failure is indistinguishable from success-without-
activation, which is exactly the shape this ADR must not ship blind."* The
reasoning was right and the insurance is unnecessary: measurement (c) ran the
skill under `dontAsk` with `--allowedTools "Bash(git *)"` and it fired, with
`permission_denials: []`.

So layer 2 does not move. Two things follow. First, a flag value that would
have been inert is not shipped. Second — and this is why (c) was worth the
`claude -p` — the *deny* channel is now known to be readable: the CLI's result
envelope carries `permission_denials` with `tool_name` and `tool_input`, so
acceptance criterion 4 has a real data source (`runner.claudeEnvelope` has to
gain the field; it currently parses `session_id`, `result`, `total_cost_usd`,
`subtype`, `is_error` and `errors` and nothing else).

### 4. The whole corpus is staged, not a selected subset

**Decision: every skill the scan found is staged.** The alternative deserves
the argument, because it is not obviously wrong.

**The case for a subset.** Staging all 35 puts all 35 descriptions in front of
every planned node. That is a cost and it is a steering surface: a description
in the system prompt is an invitation, and the corpus on the measuring machine
contains skills whose descriptions are paragraphs long and whose triggers are
broad (`agent-loop`, `babysit`, `arb-loop`). A node asked to write a test does
not benefit from being told about an arbitrage-detection loop. A subset is
cheaper and quieter.

**The case for everything, which wins.** Any rule good enough to decide *"this
node needs this skill"* at plan time **is a skill selector**, and the only
selector this project has ever measured is ADR 0012's name matcher: **7% of
node ids, with one of the five mappings it made semantically wrong.** Staging
a subset would cap activation's recall at the selector's — it would pay for a
mechanism that reads 35 descriptions and then hand it 3. Worse, it re-creates
the exact defect being deleted: `artifacts` → `html-artifact` was a *staging*
error in everything but name.

And the decisive point: **a description-based choice is precisely what
activation is.** The CLI's description gate is the selector, it runs at the
moment the node knows its own task, and it is bounded by a tool ceiling the
node cannot widen. Substituting a plan-time guess for it is choosing the worse
selector and paying for both.

**What it costs, measured.** Same trivial prompt, `--setting-sources ""`, with
and without the staged directory:

| staged | cold call (cache-creation tokens) | warm call (cache-read tokens) |
|---|---|---|
| none | 2,155 | 6,815 |
| 3 skills | 2,564 | — |
| 35 skills | 8,163 | 12,823 |

**+6,008 prompt tokens per node invocation for 35 skills** — the same delta at
both cache states, which is what makes it a prompt-size fact rather than a
caching artifact. Roughly **~172 tokens per skill** (the 3-skill arm is +409,
~136 each; long descriptions dominate). It is paid by every planned node, on
every retry and every feedback-edge re-run: a 5-node plan with one retry pays
it six times, ≈36k tokens. Observed dollar cost on this machine was $0.0041 →
$0.0071 for a warm call and $0.0246 → $0.0847 for a cold one, but cache state
dominates that spread and the token delta is the honest number.

The count and the token cost are both plan-time filesystem facts, so **both
are printed** (§7). The condition that would reopen this: a corpus large
enough that the description block starts steering rather than offering. That
is measurement (e)'s territory, and the printed per-call cost is what would
make a user notice before an ADR does.

### 5. The staged directory lives in the run directory and is re-materialized before every spawn

**Where:** `<OMG_HOME>/runs/<run-id>/skills-plugin/`, beside `state.json` —
default `~/.oh-my-graph/runs/<run-id>/`. Not the node's cwd (that is the
user's checkout, often a worktree, and is the one directory a node is
*expected* to write); not `TMPDIR` (reaped out from under a resumable run, and
on a shared machine it is listable by other users); not anywhere under
`~/.claude` (that is the user's, and oh-my-graph does not write there).
Created `0700`, files `0600`.

**Who can write it — the honest answer.** `Write` is in
`plannedToolAllowlist` **unscoped**. A node that declared it runs as the same
uid as oh-my-graph and can write any path this process can, `0700` included,
`chmod` included. **There is no location that answers the question by being
unwritable.** So the requirement — *a node must not be able to stage its own
skill for a later node* — is met by lifetime, not by location:

- At plan time, trusted code populates the directory from the scan and records
  a **manifest** in `state.json`: the ordered list of `(source path, staged
  path, sha256)` for every `SKILL.md` staged, **plus every file bundled beside
  it** (the `references/` tree included — §8 counts those as reachable through
  the CLI's progressive disclosure, so they are part of the corpus and are
  hashed with it).
- **Immediately before every node spawn, the directory is re-materialized from
  that manifest and verified.** Whatever a node wrote there is overwritten
  before the next node reads it. This is *prevention*, not detection — and it
  is strictly stronger than the first draft's §5 seal, which could only halt
  after the fact.
- A **source** file that changed since planning halts the run with the changed
  path named. A source file that vanished halts too (see §6 for why silence is
  not an option). Within a leg this is not advisory: a run whose instruction
  corpus changed under it should stop.

What the seal no longer has to cover, because layer 1 stays `""`:
`~/.claude/CLAUDE.md` and its transitive `@import`s, `~/.claude/settings.json`,
and every script a hook's `command` field points at. The first draft had to
seal all of them and conceded that resolving a hook `command` to a path is
best-effort. None of it loads now. **The corpus this ADR seals is one that
oh-my-graph created, fully enumerable, with no shell strings to parse.**

**Residuals, stated rather than closed.** The window between verification and
the CLI's own read is not closed (closing it would require the CLI to accept a
content hash, which it does not). And a node that writes has already written —
what it cannot do is have a later node read it.

**Cleanup** is the run directory's existing lifetime: the staged directory is
removed when the run reaches a terminal settled state per `runstatus`'s one
rule, and not at leg end, because a resumable run needs its manifest to still
mean something. An abandoned run's directory is swept by whatever sweeps run
directories today.

### 6. Resume re-materializes and verifies; it never rehydrates the path blindly

This is the sharpest new hazard, and it is measured:

```text
claude -p ... --plugin-dir /tmp/omg-does-not-exist-9931 --tools Read,Skill
  -> exit 0, no warning, no stderr, normal answer
```

**A nonexistent `--plugin-dir` is silently accepted.** An empty-but-valid
plugin directory is too. So a resumed leg whose staged directory is gone would
run with no skills, exit clean, and be **indistinguishable from an activation
run whose model chose not to use one** — this ADR's signature failure mode,
landing on the one code path that does not re-run planned-node validation.

`toRunnerToolPolicies` rehydrates `SettingSources` and `Tools` **verbatim**
from `state.json`, and that is exactly the wrong behaviour for a field naming
a directory. Therefore:

- **`PluginDirs` is not rehydrated verbatim.** Resume reads the manifest,
  re-materializes the directory, and verifies it. Three outcomes, all loud:
  **identical** → proceed; **a source changed** → halt naming the changed
  paths, releasable only by an explicit `resume --accept-changed-skills`,
  which prints them and re-seals; **a source is gone** → halt. Never proceed
  with a directory the run cannot vouch for.
- **`resume` gains `--no-skill-activation`**, applied as an override on the
  rehydrated policies (drop `Skill` from `Tools`, clear `PluginDirs`). The
  forward direction is already safe — an old run's `""` stays `""`, no old run
  is escalated — but without this an activation-enabled run could not be
  de-escalated on resume, which would have made the reversibility claim false
  for every resumed leg. **De-escalation only, never the reverse**, so a
  resume can never widen a run's ceiling.
- The first draft justified a weaker resume posture with *"a resumed leg may
  be days after a gate paused"*. **That run cannot exist**:
  `validatePlannedNodes` rejects gate nodes outright, so a planned graph has
  no gates. The real resume path for an auto run is `resume --retry-failed`,
  i.e. immediately after a node failed — the worst possible moment to stop
  checking.

### 7. What the printed plan can, and can no longer, tell the user

**What is lost, permanently.** Under inlining the plan printout could name
**which skill went into which node**, with size and hash, before anything ran
— a complete prospective account. Under activation the choice happens at run
time, inside the model, by description. **The plan cannot name it, and no
amount of printing will recover that.** Per-node prospective disclosure is
surrendered; the account moves to the session transcript.

**What staging gives back, and it is more than pure activation would.**
Because oh-my-graph *builds* the directory, the corpus offered to the nodes is
not "whatever the CLI finds" — it is an artifact this process created and can
enumerate exactly. So the printout names every staged skill with its size and
SHA-256, which is strictly more than ADR 0012's printout managed (it named
only the 7% that matched) and strictly more than reading `~/.claude/skills`
would license. The disclosure moves from *per-node choice* to *per-run
corpus*, and the per-call token cost is printable because it is a plan-time
filesystem fact.

```text
  skill activation: ENABLED on 3 of 3 planned node(s)
    35 skill(s) staged from /Users/you/.claude/skills into
    ~/.oh-my-graph/runs/<run-id>/skills-plugin (adds ~6,008 tokens to every node
    invocation, including retries and feedback re-runs)
    architecture-design  (4.2 KiB, sha256:ab12ab12ab12)
    … 34 more; --plan-only writes the full list beside the plan
  Which skill a node uses is chosen by the model at run time from those descriptions.
  It is NOT knowable here; each invocation is recorded in that node's session transcript.
  ceiling: UNCHANGED. Your settings, CLAUDE.md, hooks and MCP servers still do not load
    (ADR 0004 layer 1 stays ""); a declared scope like Bash(git *) is still enforced.
    The only change is that the Skill tool now exists for these nodes.
  The staged corpus is re-materialized and verified before every node spawn; a source
    skill that changed or vanished halts the run.
  Turn it off with --no-skill-activation.
```

The retrospective account is not a promise this ADR has to build: it already
exists. Every node runs with session persistence on and *"is also an ordinary
claude session in `~/.claude/projects` that any external tool can read"*
(CLAUDE.md, load-bearing invariants), and `runstate.NodeRecord.SessionID`
persists the id needed to find it. A `Skill` invocation appears there as a
tool call. Surfacing it in the ledger — "node `review` used skill
`pr-code-review`" — is attractive and is **not** part of this decision: it
would couple **shipped output a user reads** to a transcript format that is
not a documented contract, which is the mistake ADR 0004 caught `--help` prose
making once. Filed as a follow-up, with its own measurement.

The manual regression test in Failure-modes reads that same undocumented
format, and that is not the same commitment: **the format is undocumented, so
the manual test is allowed to break on a CLI upgrade — that is its job**,
failing in front of a maintainer who ran it deliberately before a release.
**The ledger is not allowed to break, because a user cannot tell a changed
transcript format from a skill that was never activated.** See §"Review
findings not adopted".

### 8. ADR 0012's inlining is removed, in the same change, not kept as a fallback

*"A mechanism kept 'just in case' with no measured case is debt"* — and there
is no measured case. To keep inlining as a fallback, someone would have to
name a case activation cannot serve. The only candidate is *a machine where
`--plugin-dir` does not work*, and the honest response to that is the printed
count plus the manual regression test, **not a second mechanism silently
taking over** — a silent fallback is indistinguishable from the failure it
covers, which is the thing this ADR is most exposed to.

Activation covers 35 skills where inlining covered 7% of node ids; it is
chosen at run time where inlining was unconditional; it has no size cap, so
`pre-commit-checklist` (86.6 KiB — the skill that matched the *best* four
planner ids and was discarded from every one of them) becomes reachable; and
its bundled `references/` files are reachable by the CLI's own progressive
disclosure instead of being an acknowledged gap.

The one property inlining has that activation does not is the snapshot, and §5
replaces it with a re-materialized corpus rather than losing it silently. The
rest of ADR 0012's machinery — the 16 KiB cap, the `{{` neutralization, the
nonce fence around inlined bodies, the name-token matcher, the
ambiguity-is-silence rule — exists solely to make inlining safe and is deleted
with it.

Concretely, when the gate passes:

- **Deleted:** the inlining half of `internal/coordinator/skillmap.go` (§1's
  fenced append, §2's matcher, §3's cap, §4's neutralization), `SkillMapping`,
  and the `skill mapped:` / `skill skipped:` printout lines.
- **Kept and reused:** `scanSkillDirs` and `SkillScan` — the scan is what
  decides §1's predicate, what feeds the stager, what the printout names, and
  what §5's manifest hashes. ADR 0012 §6's disclosure paragraph survives,
  re-pointed at activation. `internal/fence` itself is untouched: it is the
  shared data fence for quoting untrusted text, and only ADR 0012's *use* of
  it for skill bodies goes.
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
once by activation — pay for it twice, and become unattributable. One PR,
mutually exclusive.

Until that PR lands, ADR 0012 is what ships, and its record says so.

## What was implemented, and the three places the code differs from this text

Landed 2026-08-07. Where the code and this record disagree, the record is
wrong and is corrected here rather than quietly: an ADR whose §-numbers no
longer name anything is worse than one that says which of its own sentences
did not survive contact.

- **`internal/coordinator/skillstage.go`** — `applySkillActivation` (the
  post-validation step §1 specifies, ordered after `applyAgentMapping`'s
  rebuild so `node.Agent` is authoritative), `SkillStaging` (the manifest and
  `Materialize`), `GuardStaging` (the `NodeRunner` decorator that
  re-materializes before every spawn), and `Plan.BindSkillStaging`.
- **`internal/coordinator/skillscan.go`** — what survives of `skillmap.go`:
  `scanSkillDirs`, `parseSkillFile`, `SkillScan`, `DefaultSkillDirs`. §8's
  deletions are done: the matcher, the 16 KiB cap, the `{{` neutralization, the
  fence usage and `SkillMapping` are gone, `internal/fence` itself is untouched
  (its call-site count drops from five to four), and
  `field_dispositions_test.go`'s `Prompt` disposition reverts.
- **Layer 3** moves inside `narrowedToolsFor`, which gains a
  `skillActivated bool` and stays the one function that builds that list.
  `plannedToolAllowlist` is unchanged.
- **`runner.ToolPolicy.PluginDirs`** renders as `--plugin-dir`, emitted
  between `--setting-sources` and `--agent`. `runstate.NodeToolPolicy` mirrors
  it, as §Compatibility said it must.

**Difference 1 — the directory is populated when the RUN ID exists, not at
plan time.** §5 says "at plan time, trusted code populates the directory". The
run id does not exist then: `auto` mints it after `Plan` returns, and the goal
loop mints one per cycle. So plan time takes the **manifest** — every source
path with its SHA-256, from the scan, after validation, by trusted Go code —
and `Plan.BindSkillStaging(runDir)` writes the bytes once the run directory is
known. Every property §5 claims is a property of the manifest, not of when the
copy happened: the corpus is fixed at plan time, a source that changed since
then halts, and `--plan-only` stages nothing, which is correct because a
preview never ran and gets no run directory. One visible consequence: §7's
sample printout names the staged path, and the real one cannot — `printPlan`
runs before the run id exists. It names the corpus, the count, the per-skill
size and hash, the nodes reached and the per-invocation cost, which is every
part of that disclosure that is knowable when the plan is printed.

**Difference 2 — the manifest is a sidecar in the run directory, not a field
of `state.json`.** §5 says the manifest is recorded in `state.json`.
`<run-dir>/skills-plugin.manifest.json` is written instead — beside the staged
directory, never inside it, since `Materialize` deletes everything the manifest
does not name and a manifest that deletes itself is a bad manifest. Two
reasons. The snapshot is a persisted consumer contract (`runstate`'s doc
comment) and a per-file hash list of the user's private instruction corpus does
not belong in the document `runs list`, `show`, `watch` and `serve` all parse.
And for the live leg the manifest is held **in memory** and never re-read, so
the guard cannot be steered by a node that rewrites a file — strictly stronger
than reading it back from disk each time. `state.json` still carries
`plugin_dirs`, which is what tells a resumed leg there is a corpus to verify.

**Difference 3 — `--no-skill-mapping` is rewritten at parse, not registered.**
§Compatibility says it is "accepted as a deprecated alias with a one-line
notice". Registering it would advertise a deleted mechanism in `--help` and in
the usage synopsis (which `usage_test.go` holds to the registered FlagSets), so
`rewriteDeprecatedSkillFlag` translates the spelling before `flag.Parse` and
prints the notice. Accepted, loud, and not advertised.

Two things §5 and §6 promise that are **not** implemented, named rather than
left to be discovered:

- **`resume --accept-changed-skills`** (§6). A resumed leg whose source corpus
  changed halts and names the paths; the release valve is
  `--no-skill-activation`, which the halt's own message names. Adding a second
  flag that re-seals a corpus a run did not plan against is a decision with its
  own hazard, and it is not made here.
- **Cleanup of the staged directory at terminal settled state** (§5). It is
  swept by whatever sweeps run directories today, like every other run
  artifact. Nothing depends on it being removed sooner.

## The acceptance test

The maintainer's, recorded here as the condition for calling this done:

> Plan the goal *"establish a fix proposal for this issue, review the
> proposal, and turn it into an HTML artifact"*, and check that each node
> loads the skill its job calls for.

Three jobs, three skills that exist in this corpus: `architecture-design`,
`pr-code-review`, `html-artifact`. It exercises the mechanism end to end and
fails visibly if activation is silently absent — which is the failure this
whole ADR is most exposed to.

**Method.** `auto` the goal against the real corpus on subscription OAuth, env
scrubbed per `childenv.Scrub`. For each executed node, read the session
transcript for that node's `runstate.NodeRecord.SessionID` (under
`~/.claude/projects`; locate the path, do not assume its shape) and extract
every `Skill` tool call by name. Record the node-id → skills-invoked table,
the CLI version, and the cost.

**PASS requires all six:**

1. **Grant present.** Every planned node's persisted policy carries `Skill` in
   `tools` and a `plugin_dirs` entry pointing inside the run directory, and
   `setting_sources` is still `""`. Read from the **`state.json` of the
   acceptance run itself** — *not* from `--plan-only`, which writes only a
   `graph.json` under `plans/<id>/` and deliberately produces **no run
   directory and no `state.json`** (*"a preview never ran, so it is not a
   run"*), and in which §2 makes the grant invisible by design.
2. **Activation alive, against a negative control.** At least one node's
   transcript records a `Skill` tool call **and** the same goal re-run with
   `--no-skill-activation` records **zero**. Without the control arm this
   criterion cannot distinguish the change from the baseline — a model
   competent without skills passes it either way, which is the exact
   silent-absence failure it exists to catch. Cost: a second run, recorded.
3. **The three skills, on the right jobs — assignment pre-registered.** Across
   the run, the invoked set includes `architecture-design`, `pr-code-review`
   and `html-artifact`, each invoked by the node whose job is the
   corresponding one. The node → job assignment is written down **from the
   plan printout, before any transcript is opened**, and that pre-registration
   is what the result is scored against. Read post-hoc the criterion is
   unfalsifiable by construction.
4. **Nothing was denied.** No `Skill` entry in the run's `permission_denials`.
   **The data source now exists and is measured** (§3): the CLI's result
   envelope carries `permission_denials` with `tool_name`, `tool_use_id` and
   `tool_input`. `runner.claudeEnvelope` must gain the field; the first draft
   recorded this criterion as unreadable, and it is now merely unimplemented.
5. **The ceiling held, on the shape that can break it.** A node declaring
   **`Bash(git *)`** — the E1 shape — under the final shipped argv, attempting
   an out-of-scope command, judged by whether the file appears. Measurement
   (f)'s second arm is this probe run by hand; the acceptance test runs it
   under the real `buildArgs`. Re-running the original `CEILING-BREACH` prompt
   against a node that declared no Bash would pass while a regression was
   live, which is how the first draft's version of this criterion would have
   certified the very hole (g) found.
6. **Billing intact.** The run's cost lands on subscription OAuth
   (`provider: "firstParty"`), asserted per node, not assumed.

**FAIL is recorded, not retried away.** Fewer than three skills invoked; a
wrong skill invoked (record which, and against which job — that is the direct
successor to ADR 0012's `artifacts` → `html-artifact` finding); a plan whose
shape has no such three jobs (re-plan **once**, record both plans and the cost
of both). A partial pass is a fail with a table attached.

**The ids are kept.** ADR 0012's yield measurement could not be re-derived
from its own record because `--plan-only` writes nothing and the ids lived
only in the planning session. This run writes its goal, its full node-id list,
and the node → skills table into `docs/measurements/` as it goes.

Note what a pass does **not** establish: that an activated skill made the
node's output *better*. It establishes that the right procedure was loaded for
the right job. Quality is measurement (e).

## Required measurements

Record each with cost and CLI version, as every prior E-number is.

**Recorded 2026-08-07 (claude 2.1.223, macOS), and re-run independently before
this rewrite:**

- **(g) Does layer 2 still bind under `--setting-sources user`?** **NO.** The
  out-of-scope `touch` ran. §Context, "Measurement (g)". The layer-1 route is
  dead; §1 does not take it.
- **(f) Can a plugin directory carry the definitions with layer 1 at `""`?**
  **YES**, and every ceiling layer plus the CLAUDE.md and MCP exclusions
  survive, both with positive controls. §Context, "Measurement (f)".
- **(c) Is `Skill` in `--allowedTools` necessary under `dontAsk`?** **NO** —
  it fired with `permission_denials: []` under an allow list naming only
  `Bash(git *)`. §3; layer 2 does not move.

**Struck as unnecessary** — each existed only to price the layer-1 route, and
none of it loads under `--plugin-dir`. A future proposal that reaches for
`--setting-sources user` inherits all of them, plus (g):

- **(a)** can a user hook DECIDE a planned node's tool call — hooks do not load.
- **(d)** does `--setting-sources user` exclude project and local — not used.
- **(h)** can a settings-file `env` block redirect the child's credentials —
  the file does not load. The subscription-billing invariant is untouched by
  this ADR, and the acceptance test still asserts it per node.

**Still owed before `Accepted`:**

- **(b) Does a skill that runs in a subagent route around layer 5?** Some
  skills execute in a subagent rather than loading instructions inline. `Task`
  and `Agent` are both in `deniableTools` and denied to a node that did not
  declare them. Plant such a skill in the staged directory, invoke it under
  the final argv, and record whether it spawns and what tool set the child
  holds. **A yes here is a ceiling finding, not a usability one**, and would
  force either a refusal of subagent-executing skills at staging time or its
  own ADR. This is the one ceiling question `--plugin-dir` does **not** answer,
  because it is about what a skill's *body* can reach, not about what loads.
- **(e) Does an activated skill improve the node's output?** The descendant of
  ADR 0012's voided (a). Same goal, same corpus, with and without
  `--no-skill-activation`, comparing artifacts. It does not gate the ceiling
  claims, only the value claim — but the value claim is the entire reason to
  pay §4's 6,008 tokens per invocation, and this ADR should not reach
  `Accepted` asserting it the way ADR 0012 asserted inlining's. (The control
  arm is shared with acceptance criterion 2 — one run serves both.)

## Failure modes

- **Silent absence on a future CLI.** Activation is one flag-semantics change
  away from yielding nothing, and unlike inlining there is no printed line
  that would look different — the plan says "ENABLED" either way, because the
  plan cannot see run-time choices (§7). Worse than under ADR 0012, where the
  7% floor at least printed itself. Mitigation: the acceptance test becomes a
  `//go:build manual` regression test beside `assess_manual_test.go` and
  `repair_manual_test.go`, run before each release, never in CI (it needs a
  real `claude` and costs cents — the `make smoke` posture).
- **A vanished staged directory.** Measured: `--plugin-dir` pointing at
  nothing exits 0 with no warning. This is silent absence with a trivial
  trigger, and `resume` is where it bites. §6 answers it by re-materializing
  and verifying rather than rehydrating a path; the failure mode remains the
  reason that answer is not optional.
- **A node stages its own skill.** `Write` is unscoped in
  `plannedToolAllowlist` and no directory is unwritable by a same-uid process,
  so this is answered by re-materializing before every spawn (§5), not by
  permissions. The first write is not prevented; its effect on later nodes is.
- **Cost with nothing to show.** Every planned node pays ~6,008 tokens for 35
  descriptions whether or not it activates anything, on every retry and
  feedback re-run. ADR 0012 measured 84% of planner ids as having no candidate
  at all; under §4's "stage everything" that former matcher-miss becomes a
  per-node tax instead of a silent skip. It is printed (§7) and it is the
  price of not re-introducing a 7% selector.
- **35 descriptions as a steering surface.** §4's rejected alternative. The
  descriptions are the user's own files and the ceiling bounds what any of
  them can do, but a node can be *distracted* within its declared tools. That
  is not measured; measurement (e)'s comparison is where it would show up.
- **Subagent-executing skills.** Measurement (b). If a skill's body spawns a
  subagent, layer 5's denial of `Task`/`Agent` is the thing being tested, and
  a yes is a ceiling finding.
- **Nondeterminism.** The same plan run twice may activate different skills.
  ADR 0012's reproducibility property (the approved text is the executed text)
  is gone; §5 preserves only that the *corpus* did not change under the run.
- **A corpus that grew between plan and run.** §1's predicate is evaluated at
  plan time; `Found: 0` disables activation for the whole run even if the user
  installs a skill mid-run. Correct — the policy is snapshotted — and it will
  read as a bug to whoever hits it, so the printout says the count is from
  plan time.
- **A machine with `allowManagedPermissionRulesOnly`.** Unchanged from
  ADR 0004: `--allowedTools` rules are ignored and the ceiling is the managed
  policy. Layer 3 still applies.

## Compatibility

- **No graph schema change, no new `graph.Node` field.** ADR 0004 §2's
  reflection test is unaffected in shape; one recorded `why` changes (§8:
  `Prompt` reverts).
- **A snapshot schema change, and this one is new.** `runner.ToolPolicy` gains
  `PluginDirs []string`, so `runstate.NodeToolPolicy` must mirror it or
  `TestNodeToolPolicyMirrorsRunnerToolPolicy` fails — which is the test doing
  its job. The first draft claimed "no snapshot schema change"; that was true
  of the layer-1 route (which reused two existing fields) and is false here.
  Old snapshots without the field rehydrate as an isolated run, which is the
  correct default.
- **`--setting-sources` is untouched**, so ADR 0004's E1, E3, E5 and E7 all
  stand unamended and layer 1's settings-hook closure (*"writing a
  `.claude/settings.local.json` into the invocation directory achieves
  nothing"*) survives without argument.
- **The kill switch reaches `resume`.** `--no-skill-activation` on both `auto`
  and `resume`, de-escalation only (§6). `--no-skill-mapping` is accepted as a
  deprecated alias with a one-line notice, because the user intent behind it
  ("keep skills out of my auto runs") is unchanged and the effect is now
  stronger, not weaker. Both spell the same thing on `chat`, mirroring
  `--no-agent-mapping`.
- **`plannedToolAllowlist` is unchanged**, so `plannedToolEffects` needs no new
  row and `TestDetectBuildSignals_NeverInfluencesTheCeiling`'s layer-0
  assertion is untouched (§2).
- **Agent-mapped nodes are excluded from activation**, and the exclusion is
  printed. They already run with layer 1 dropped to `nil`, so the composite
  (`--agent` + `--plugin-dir` + user settings) is a different, unmeasured
  configuration — the same unmeasured-composite refusal ADR 0012 §2 made, for
  the same reason. Lifting it requires its own probe. Note that under `nil`
  such a node already sees the user's real skills, so the exclusion costs it
  little.
- **A follow-up this ADR declines to decide:** `applyAgentMapping`'s
  `SettingSources = nil` is wider than anything decided here, and measurement
  (g) now gives that gap a number rather than a suspicion — an agent-mapped
  node declaring `Bash(git *)` is the shape (g) breached. It is a change to a
  shipped mechanism on a path this ADR does not otherwise touch, so it gets
  its own issue and its own measurement rather than riding along.
- **Hand-written graphs: no change.** They never carried layers 1 or 3, and
  they are not given a staged directory — the user's own skills already load.
- **The four exec seams are unchanged.** Nothing here spawns a process.
  Staging is `os.MkdirAll`/`os.ReadFile`/`os.WriteFile` plus `crypto/sha256`
  in the coordinator, deliberately not a shell out, so `internal/invariants`
  stays true.

## Alternatives considered

- **A — relax layer 1 to `"user"`.** The first draft's decision. **Rejected on
  measurement (g):** it forfeits the scope ceiling, so an unattended `dontAsk`
  node declaring `Bash(git *)` can run an out-of-scope command. It also drags
  in the user's CLAUDE.md, hooks, `model` pin, `env` block and
  `additionalDirectories`, which cost this record roughly 300 lines to price
  and which `--plugin-dir` avoids entirely. The whole of that pricing is
  preserved in the git history of this file for anyone who proposes it again.
- **B — relax only on nodes that would use a skill.** Mechanically trivial
  (`ToolPolicies` is already per-node). It fails on the predicate, not the
  plumbing: any rule good enough to decide *"this node needs a skill"* in
  advance **is** a skill selector, and the only selector this project has
  measured is the name matcher at **7%**, with a false positive in 1 of the 5
  mappings it made. Under `--plugin-dir` the price it was trying to avoid is
  gone, so B now buys nothing at all except a smaller staged corpus — which is
  §4's rejected subset, argued there on cost. *(The first draft rejected B
  partly on "14.5% for the LLM arm". That number is withdrawn — see the
  Correction in §Context.)* The one honest sliver of B survives as §1's
  predicate: condition on a filesystem fact (does a corpus exist), never on a
  guess about relevance.
- **B′ — relax only on nodes that declared no mutating tool.** Rejected, and
  its motivation has evaporated: it existed to bound the CLAUDE.md
  contradiction hazard ("push when done" landing on a `Bash(gh pr *)` node),
  and no CLAUDE.md loads. On yield it was always backwards — the nodes that
  most want skills are the mutating ones.
- **C — keep the isolation, keep inlining.** Steelmanned in the first draft
  for four properties, and under `--plugin-dir` three of them stop being
  distinguishing. Inlining is deterministic, printed with name/size/SHA-256,
  snapshotted into `graph.json`, and puts no CLAUDE.md into a node. **The
  CLAUDE.md advantage is now shared** (layer 1 is unchanged either way). The
  printed-corpus advantage is largely shared (§7 prints every staged skill
  with size and hash; what is not shared is the per-node *choice*). The
  snapshot advantage is largely shared (§5 re-materializes a verified corpus).
  What is genuinely surrendered is per-node prospective disclosure and
  determinism-of-mechanism. Against that: inlining recovers 7% of the
  capability, with a measured false-positive rate of 1 in 5 among the mappings
  it makes; it kills its four best matches at a size cap it fit against a
  corpus that could not exercise that cap; and its claim to *help* the nodes it
  lands on has been unmeasured since the day it shipped. That is the trade,
  taken knowingly: **a complete account of a mechanism that recovers 7% is
  worth less than an incomplete account of the mechanism itself.**
- **D1 — relax layer 3 only, keep layer 1, no plugin dir.** Measured dead:
  `--setting-sources "" --tools Read,Skill` does not run the skill. Naming the
  tool does not load the definitions. Layer 3 must still move (measured
  again in (f): `--plugin-dir` + `--tools Read` yields `NO-SKILL`), it is just
  not sufficient alone.
- **D2 — a synthesized `--settings` payload carrying skill directories.** The
  first draft's promoted survivor. **Measured dead on the way to (f):** a
  `--settings` payload granting `Skill(*)` does nothing, because a permission
  approves a *tool* and does not supply skill *definitions*. `--plugin-dir` is
  what D2 was reaching for; this decision is D2's shape with the working
  mechanism substituted.
- **D3 — run planned nodes under a synthetic `HOME` holding only skills.**
  Rejected without measurement. Subscription OAuth credentials live under
  `HOME`; moving it risks the one invariant the whole project is built on
  (ADR 0001, `childenv.Scrub`). `--plugin-dir` achieves the same isolation by
  addition rather than by relocation, which is why it dominates.
- **D4 — let the planner pick skills and declare them.** Rejected, still, and
  for the reason ADR 0012 gave: the planner is an untrusted producer, and
  letting its output select which local file loads into a node is the hole
  `validatePlannedNodeAgent` closes. Note carefully that **activation is not
  this**: the choosing model is the node's own, at run time, over the user's
  own files, through the description gate the CLI designed for exactly that,
  and bounded by a tool ceiling it cannot widen (E4, re-confirmed). "Untrusted
  choice" does not transfer from a producer choosing for *other* nodes ahead
  of validation to a node choosing for *itself* under the ceiling.
- **Symlinking the staged directory at `~/.claude/skills` instead of copying.**
  Rejected: it would make the run's corpus change under it whenever the user
  edits a skill, which is precisely what §5's manifest exists to catch, and it
  would put a node's write directly into the source of truth for later nodes.
  Copying is what makes re-materialization a prevention rather than a
  detection.

## Consequences

**Positive**

- A planned node gets the real mechanism: 35 skills instead of a 7% lexical
  substitute, selected by description instead of by a 4-rune prefix that
  cannot tell `artifacts` from `html-artifact`. The skills that matched
  planner ids best and were discarded at the cap — `pre-commit-checklist` on
  four verification nodes — become reachable, along with their bundled
  `references/` files, through the CLI's own progressive disclosure.
- **The capability ceiling is unchanged, and this time it is measured on the
  shape that can break it.** Layer 1 stays `""`; ADR 0004's E1 was re-run
  under this ADR's argv and the out-of-scope command was denied, with the
  denial visible in the CLI's own `permission_denials`. E4 stands too. The
  first draft claimed this and had to retract it; the claim is now earned
  rather than assumed, and it is earned by *not* touching layer 1.
- **The user's CLAUDE.md, hooks, `model` pin, `env` block and permission rules
  stay out of unattended nodes** — the entire exposure the first draft spent
  §4, §4a and §6 pricing, avoided rather than accepted. MCP stays out too,
  with a positive control behind the claim for the first time.
- ~750 lines of ADR and a matcher, a cap, a neutralizer and a fence usage
  leave the tree with the mechanism they existed to protect (§8).
- The run gains a property it never had: the skill corpus it depends on is
  re-materialized and verified before every node spawn, so a node cannot stage
  instructions for its successors.

**Negative / trade-offs**

- **Per-node prospective disclosure is gone.** The plan can no longer say
  which skill a node will use, because nothing knows before the model does
  (§7). The account moves to the session transcript. The *corpus* is still
  fully disclosed.
- **+6,008 prompt tokens per node invocation** on this corpus, on every node,
  every retry and every feedback re-run, whether or not anything activates
  (§4). Measured, printed, and the price of not shipping a 7% selector.
- **A new silent-absence trigger.** A `--plugin-dir` pointing at nothing exits
  0. §6 makes resume verify rather than trust, and the manual regression test
  is the standing guard, but the CLI will not help.
- **Reproducibility drops.** Same plan, same corpus, potentially different
  skills. §5 bounds the corpus, not the choice.
- **A snapshot schema change** (`PluginDirs`), where the first draft needed
  none.
- **One machine, one CLI version.** Every number here is claude 2.1.223 on
  darwin. #130 asks for a second machine and the probes in this record are the
  ones to send — with the note that (g)'s and (f)'s arms must be judged by
  whether the file appears and whether the token comes back, never by what the
  model says about itself.

## Review findings not adopted

The 2026-08-07 deep review raised nine blocking items. Eight are adopted, and
each is marked in place rather than quietly rewritten, because a record that
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
  transcript shape changes. A `//go:build manual` test is the opposite failure
  mode by construction: it is never compiled into a release, no user depends on
  it, and when the format changes it **fails loudly in front of a maintainer
  running it deliberately before a release**. `make smoke`,
  `assess_manual_test.go` and `repair_manual_test.go` already occupy exactly
  this position in the tree.

  What is adopted: §7's wording invited the reading, so the criterion is now
  stated as *"the format is undocumented, so the manual test is allowed to break
  on a CLI upgrade — that is its job; the ledger is not, because a user cannot
  tell a changed format from an absent skill."*

## What could not be determined

Named with the measurement that would settle each.

1. **Whether a subagent-executing skill escapes layer 5.** → (b). The one
   ceiling question `--plugin-dir` does not answer, because it is about what a
   skill's body reaches rather than about what loads. If yes, it is a ceiling
   finding and needs its own ADR or a staging-time refusal.
2. **Whether activation improves node output.** → (e). ADR 0012 shipped
   without answering the equivalent question. This one should not.
3. **Whether 35 descriptions steer a node that needed none of them.** The
   distraction half of §4's rejected subset argument. It would show up in
   (e)'s comparison; no probe isolates it today.
4. **Whether a staged plugin's skills can shadow or be shadowed.** The probe
   invoked a staged skill by bare name and it resolved, but plugin skills are
   addressable as `<plugin>:<skill>` elsewhere in the CLI and nothing here
   measured a name collision between the staged plugin and another loaded one.
   Under layer 1 = `""` no other plugin loads, so this is latent rather than
   live — and it becomes live the moment anyone lifts the agent-mapped
   exclusion.
5. **Whether any of this reproduces off this machine.** No probe settles it;
   it needs a second machine, ideally one whose `settings.json` grants
   nothing, and one on a different CLI version. #130 already asks for exactly
   that.
6. **What the layer-1 route would have cost.** Deliberately unanswered: (a),
   (d), (h) and the MCP control question were struck when the route died. A
   future proposal to load the user's settings into planned nodes inherits all
   of them, and (g) besides.
