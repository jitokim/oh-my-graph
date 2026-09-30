# ADR 0041 — An operator's conventions reach a planned node as text, not as settings

**Status:** Accepted. Written before its code; §5 lists the tests the
implementation owes. The flag it names does not parse yet.

**Revision.** The first draft was reviewed before any code existed. The review
found that the draft did not fit the case #282 describes (§1.4). It also found
that the draft misstated how a retry works (§2.2), and that it had not weighed
the plan-time prompt edit that `skillstage.go` already makes (§4.1). This
revision answers every finding in place. §8 maps each finding to where it was
answered.

**Where the addresses point.** Written against `5d665d5`, the `main` this
branch (`feat/282-conventions-path`) left. Every `file:line` in §1, §2.2 and §4
was read at that commit. The Decision describes code that does not exist yet, so
it names file **and** function: the line goes stale when the diff lands, and the
function name does not.

**Date:** 2026-10-01

**Issue:** [#282](https://github.com/jitokim/oh-my-graph/issues/282). An
operator's coding conventions (their user `CLAUDE.md`, and the reference docs
it imports) have no path into an auto-planned node.

## 1. Context

### 1.1 The loss is deliberate, and it has a name

Ceiling layer 1 is `--setting-sources ""`, built by `toolPolicyFor`
(`internal/coordinator/coordinator.go:860-873`) from `isolatedSettingSources`
(`:882-885`). The comment on `toolPolicyFor` states the cost at `:831-833`:

> The cost, which belongs in the README and not in a surprise: a planned node
> also loses the user's CLAUDE.md, hooks and MCP servers.

On Codex the same bit renders `--ignore-user-config`, `--ignore-rules` and
`project_doc_max_bytes=0` (ADR 0032 §1.2), so a planned Codex node loses the
operator's `AGENTS.md` for the same reason.

Layer 1 exists because of the operator's **standing permission grants**. They
live in the same settings files, and with those files loaded a `Bash(*)`
matches before a node's narrower `Bash(git *)` (DESIGN.md, E1; ADR 0004).
`CLAUDE.md` rides on the same source list. It is not dropped because anyone
judged it dangerous. It is dropped because the CLI cannot load a settings source
without also loading the grants in it.

### 1.2 What the operator can do today, and what each option costs

| path | conventions arrive? | cost |
| --- | --- | --- |
| do nothing | **no** | Planned nodes write code blind to house style. The planner reads `CLAUDE.md` (it runs unisolated, ADR 0032 §1.4), but it can only restate conventions in each node prompt it writes. |
| `--accept-loaded-user-config` (ADR 0032) | yes, natively | Layers 1 **and** 4 drop. Standing grants return, so `allowed_tools` scope becomes a declaration again. Hooks and MCP servers load. Agent mapping (ADR 0022) and skill activation (ADR 0017) are forced **off**. |
| `auto --plan-only` then `run <graph.json>` | yes, natively | All five layers drop, and the goal loop is out of reach (ADR 0032 §2.1). |

Every existing door charges the full settings bill for a payload that is only
**text**. #282 asks for the text alone. The narrowest door that exists today
still costs the operator their grants enforcement, their staged agents and
their staged skills.

### 1.3 The precedent for crossing the ceiling with something that grants nothing

ADR 0037 let exactly one settings key, `model`, reach a planned node. It set the
test a crossing must pass (§3): it *"adds no tool, loads no file into the
context, runs no hook and grants no path"*, and the planner cannot select it.
It also said a second settings key needs its own ADR (§2.1). Conventions are
not a settings key. They do fail one clause of that test by design: they are
*text into the context*. §2.3 argues that this clause is where ADR 0037's
reasoning must be extended rather than simply reused.

### 1.4 The case the design has to fit

#282's operator does not keep their conventions in one file. They keep **five
reference docs, about 17k tokens in total, pulled into `CLAUDE.md` by `@`
imports**. At about 4 bytes per token that is roughly 68 KiB. The issue asks for
a path for *"files the operator lists"*: a list, not a single file.

The issue also records why the failure is total rather than partial. Its fact 3
is the #268 deny: a planned node that tries to read those docs from where they
live is refused, because the directory is outside every path the ceiling grants.
The node does not get a degraded copy of the conventions. It gets nothing.

The first draft of this ADR took one file, did not follow imports, and capped
it at 32 KiB (about 8k tokens). Under that design the motivating operator had
two moves, and both fail:

- Point the flag at their `CLAUDE.md`. Every node receives the literal `@…`
  lines, which is 17k tokens of conventions replaced by a handful of paths the
  node is not allowed to read. That is silently useless.
- Concatenate the five docs into one file. That is about 68 KiB, and it is
  refused.

A design that cannot carry the case that motivated it does not close the issue.
§2.1 and §2.5 are therefore sized **from the issue's measured corpus**, and §6
lists "the motivating corpus doesn't fit" as a failure mode with a test (§5,
test 8).

## 2. Decision

### 2.1 An ordered list of operator-named files, typed on `auto`, prefixed to each fresh spawn

`auto` gains **`--conventions <path>`**. The flag is **repeatable** and off by
default. Each occurrence names one file. The order on the command line is the
order in the prompt. When at least one is given:

1. **At launch, before the planner call**, the CLI reads and validates every
   named file, and validates the list as a whole (§2.5). A refusal costs
   nothing: no planner call is made and no run directory is minted.
2. After planning, the concatenated text is **staged** into the run's own
   directory as `conventions.md`. This is the same pattern ADR 0017 uses to
   stage skills and ADR 0022 uses to stage agents. `state.json` records, per
   source file, the path, byte count and SHA-256, plus the SHA-256 of the staged
   file. From then on, every leg reads the staged copy and never a source path.
3. The scheduler prefixes the staged text to the prompt of **every spawn that
   resumes nothing** (§2.2). The text is added after `h.Interpolate`, so it is
   never template-processed: a literal `{{inputs}}` in someone's style guide
   stays literal.
4. The plan screen prints the lines in §2.4 through a `note*` sibling that
   `printPlanForRuntime` calls (`cmd/oh-my-graph/main.go:1214`). This is the
   same screen and the same slot family that ADR 0030 and ADR 0032 use.

The flag carries **text and nothing else**. `toolPolicyFor` is not touched, and
all five layers bind exactly as they do without the flag. Agent mapping and
skill activation stay **on**, because layer 1 stays `""`. That is the whole
difference from ADR 0032, and it is the reason this door exists next to that
one.

`@` imports are still not followed (§2.5). The operator who keeps their
conventions behind imports names the imported files directly, which is five
`--conventions` flags for #282's operator. A file that is **nothing but**
import lines is refused, with a message that names the import targets to list
instead (§2.5). This is what turns the "silently useless" move from §1.4 into
a loud and actionable one.

### 2.2 The prefix keys on the actual spawn, not on the node's handoff mode

The first draft said a node that resumes a session (`handoff: session`, and
"ADR 0020 retries on the same session") is not prefixed. The second half of that
was false, and the rule built on it would have dropped the conventions on
exactly the attempts that most need them.

A retry **never** resumes a session. `prepareRetry` and `startCold`
(`internal/schedule/scheduler.go:1193-1226`) rebuild the prompt from
`basePrompt` and set `invocation.ResumeSession = ""`. That holds for an in-leg
retry (`prepareRetry`, called at `:906` and `:979`). It also holds for the retry
of a node that failed in an earlier process (`startCold` at `:868`). Now take a
`handoff: session` node:

- Its first attempt resumes its parent, so a rule keyed on the handoff mode
  skips the prefix. That is correct: the parent's first turn already holds the
  conventions.
- It fails. The retry starts cold from `basePrompt`. If the prefix had been
  applied in `buildInvocation`, `basePrompt` never contained it, and the cold
  retry runs **without** the conventions, in a fresh session that has never
  seen them.

So the prefix is not applied in `buildInvocation`, and it never enters
`basePrompt`. It is applied in `(*Scheduler).runNode`, on the invocation handed
to `s.runner.Run` (`:886`). It is applied **to that call's copy only**, and only
when `invocation.ResumeSession == ""` at that point, which is after any
`startCold` or `prepareRetry` has run. Three consequences follow:

- The decision is made on the fact that matters: whether this subprocess
  inherits a conversation. It does not matter why it does or does not.
- `basePrompt` stays conventions-free. `retryPrompt` (`retryfeedback.go`) still
  rebuilds from it, so the prefix cannot stack across attempts, and the fenced
  prior-attempt quote still sits after the node's own prompt, where ADR 0020
  put it.
- Any later path that makes a spawn cold, such as an ADR 0010 feedback round,
  gets the conventions without a second rule to remember.

Test 4 and its partner, test 5, pin both directions.

### 2.3 Why this does not weaken the ceiling, and where ADR 0037's test must be extended

The ceiling bounds **capability**: what a node may *do* (ADR 0037 §3). The
conventions text adds no tool, grants no path, runs no hook and loads no MCP
server. A node that holds `Read, Glob` still holds exactly `Read, Glob` when
its prompt opens with "use tabs, write table-driven tests". An instruction
inside the text that asks for more, such as "always run `make test`", meets the
same ceiling any planner-written instruction meets, and is denied the same way.

The clause it does fail, *"loads no file into the context"*, is failed on
purpose. Two properties keep that failure bounded:

- **The planner cannot select it.** The paths are operator argv, typed at
  launch. They are not a graph field and not a key the planner can write.
  ADR 0032 §2.1's argument against a per-node opt-in, and
  `validatePlannedNodeAgent`'s argument against a planner that chooses which
  local files load, still hold.
- **It is not fenced, and that is correct.** `internal/fence` marks *untrusted*
  text as data so that a model will not follow it. These are the operator's own
  instructions, and following them is the point. Fencing them would ask the
  node to ignore the one thing the flag exists to deliver. The header therefore
  names the text as the operator's conventions, not as quoted data.

Stated exactly:

> **The node's capability ceiling is unchanged. The texts the operator names
> cross it, by path, into the prompt, and nothing those files' neighbours hold
> comes with them.**

### 2.4 What the node sees, and what the operator sees

**The node's header carries no path and no hash.** The prefix is:

```text
The person who launched this run gave these conventions for every node. Follow them.

## Conventions 1/5: style.md
<bytes of the first file>

## Conventions 2/5: testing.md
<bytes of the second file>
...

---

<the node's own prompt>
```

Each file is named by its ordinal and its **basename only**. An absolute path
would write the operator's `$HOME` layout into every node prompt and so into
every session transcript under `~/.claude/projects`. The same holds for Codex's
session store. A hash is noise to a model. The ordinal is there so that two
files with the same basename (two `CLAUDE.md`s) stay distinguishable. A
basename that is itself identifying is the operator's own choice, and
LIMITATIONS says so.

**The operator's screen carries the full paths and hashes.** That screen is the
operator's own terminal, and it is where a path is useful for checking what was
named:

```text
  Planned nodes are prefixed with your conventions (5 files, 69,812 bytes, sha256 <12 hex>) — text only: no settings, grants, hooks or MCP servers come with it.
    1/5  <absolute path>  14,002 bytes  sha256 <12 hex>
    ...
```

The sha256 on the first line is the hash of the staged `conventions.md`. That
is what `resume` checks (§2.6). The per-file hashes are the sources.

**On `--plan-only`, the screen says the conventions do not carry over.** A
preview's natural next step is `run <graph.json>`, and the saved graph does not
hold the conventions (§4.1 explains why). A preview that printed only the line
above would promise something `run` does not deliver. On `--plan-only` the first
line is therefore replaced by:

```text
  Your conventions (5 files, 69,812 bytes) are NOT in the saved graph: `run <graph.json>` does not prefix them. Launch with `auto` to apply them.
```

This is followed by the same per-file lines, so the operator still sees what
was validated.

### 2.5 Refusals: every file is loaded whole or the launch is refused

`--conventions` refuses **at launch, before the planner call**, with a message
that names the path and the reason. It never prints a file's content. The
cases:

| input | outcome |
| --- | --- |
| each file readable, regular, valid UTF-8, non-blank; total ≤ 128 KiB | staged and prefixed |
| any path absent or unreadable | **refused** |
| any path a directory, or another non-regular file | **refused** |
| any file blank after whitespace trim | **refused**. A flag the operator typed that delivers nothing is a typo, not a choice. |
| any file whose every non-blank line is an `@` import | **refused**. The message lists the import targets and says to pass them as `--conventions` instead (§2.1). |
| the same file named twice (same resolved path) | **refused**, as a typo |
| total across all files > 128 KiB | **refused**, with each file's size, the total and the cap. **Nothing is ever truncated.** |
| any file not valid UTF-8 | **refused** |
| flag not given | nothing read, nothing staged. argv, prompts, screens and `state.json` are byte-identical to `5d665d5`. |

**The cap is on the total, and it is 128 KiB.** The cap exists to put a
readable ceiling on a bill that every fresh spawn pays. It is set from the only
measurement available, which is #282's corpus. Five docs of about 17k tokens is
roughly 68 KiB, and 128 KiB is about 1.9× that, or about 32k tokens. That
headroom lets a conventions set grow without every growth becoming a refusal.
It still keeps the prefix to about a sixth of a 200k-token context. A per-file
cap would add no protection that the total does not give, and would refuse the
legitimate case of one long style guide. The first draft's 32 KiB could not hold
the corpus that motivated the ADR (§1.4), which is the failure this number is
corrected against. §7(4) states what would move it again.

**No truncation** is the row that needs an argument. The fence package's
head+tail bound is correct for *evidence*, where the ends carry the signal. It
is wrong for *instructions*: dropping the middle of a style guide silently
changes what the operator told the node, and in the worst case drops the
exception a rule depends on. A set that does not fit is a set the operator
must shorten on purpose.

`CLAUDE.md` `@path` imports are **not resolved**. The node sees a mixed file's
import lines literally. Following imports would read files the operator never
named, which is the reach this ADR withholds. The operator can name any of those
files, so the reach is never lost, only made explicit. A file that is *only*
imports is refused (above). A file that mixes text with imports is accepted,
because an import line is still valid text. For that file, the plan screen
appends `(N @-import lines not followed)` to the file's line, so the gap is
visible where the operator reads the hash.

### 2.6 Scope: `auto` only; `resume` inherits the staged copy; `run` and `chat` do not get it

- **`run`**: not registered. A hand-written node already loads the operator's
  `CLAUDE.md` natively (ADR 0032 §1.3), so the flag there would only
  double-deliver.
- **`chat`**: not registered, for ADR 0032 §2.7's reason. A flag that changes
  what every node of an unattended run is told belongs at a typed launch, not
  behind a `[y/N]`.
- **`resume`**: registers **no** flag. It reads the staged `conventions.md` and
  checks its SHA-256 against `state.json`. On a match it prefixes exactly as
  the first leg did and reprints the plan-screen lines. On a missing or changed
  file it **refuses** and names both hashes, rather than continuing a run whose
  nodes would be told something different from their siblings. A resumed leg
  may not re-point, add or drop the conventions. This is the same
  consistency rule behind ADR 0037 §2.7's refusal of a per-run model flag, and
  behind ADR 0017 §6.
- **An older binary resuming such a run**: refused (§6, compatibility). The
  consistency rule above would be empty if an older `resume` could carry on
  without the conventions.
- **The goal loop**: every cycle is one `auto` process, so every cycle prefixes
  and prints, as ADR 0032 test 8 requires of its own line.
- **The planner and the assessor**: untouched. The planner already loads the
  operator's configuration (ADR 0032 §1.4). The assessor is engine machinery
  whose reply is parsed (ADR 0037 §2.5).
- **With `--accept-loaded-user-config`**: allowed, and not refused. The two
  flags do not contradict each other, and ADR 0032 §2.5 declines to refuse
  mere redundancy. If `--conventions` names files the restored settings already
  load, the node sees them twice. That costs tokens, not correctness, and the
  plan screen shows both lines.

### 2.7 Prompt prefix, not a system-prompt flag

The prefix sits in the prompt, not in `--append-system-prompt`, for three
reasons:

1. **It is runtime-neutral with no new argv.** ADR 0025 allows one runtime per
   run, and ADR 0032 §2.2 argues that an operator's motive for their own
   configuration does not depend on which CLI is installed. A prompt prefix
   reaches `claude -p` and `codex exec` the same way. A system-prompt route
   would need a different flag on each CLI, the Codex one is unmeasured, and
   `runner.buildArgs` would gain a runtime-specific argument it does not have
   today.
2. **It does not touch a mapped node's system prompt.** ADR 0022 promises that
   a mapped node's system prompt comes from the definition oh-my-graph staged.
   Appending to that system prompt would make the staged definition no longer
   the whole of it. How `--append-system-prompt` and `--agent` compose is
   unmeasured in this repository, and this ADR does not stake a claim on it.
3. **It keeps the exec seam a pure protocol.** `CLIRunner` renders argv. It
   does not read operator files (ADR 0037 §2.1). The read belongs to the CLI
   layer, the staging to the run directory, and the prefix to the scheduler,
   which already owns the prompt.

The one thing given up is the system prompt's positional weight, and the ADR
does not claim otherwise. §7 names that trade as the observation that would
reverse it.

## 3. What does not change

- **`internal/childenv` is untouched.** This feature concerns prompt text, not
  the environment. A diff under `internal/childenv/` means the implementation
  went wrong.
- **No fifth exec seam.** The reads are plain file reads in the CLI layer and
  spawn nothing. `internal/invariants` must pass unmodified.
- **`toolPolicyFor` is untouched**, and so is every ceiling test. If one needs
  an edit, the implementation moved the ceiling and is wrong.
- **The graph schema is unchanged.** There is no new node field and no new key
  a planner may write. The planned graph, `graph.json` and every node's
  `Prompt` field are byte-identical with and without the flag (§4.1).
- **No provider SDK, no `--bare`, no `--no-session-persistence`.** The prefix
  is prompt text inside the same CLI subprocess.

## 4. Alternatives considered

### 4.1 Append the conventions to each planned node's `Prompt` at plan time

This is the strongest alternative, and it has a precedent in this repository.
`applySkillActivation` already edits planned prompts after validation:
`plan.Graph.Nodes[i].Prompt = node.Prompt + "\n\n" + activationNotice`
(`internal/coordinator/skillstage.go:898`), followed by a re-parse
(`rebuildWithNotices`, `:944`). Doing the same for conventions, re-encoding
`plan.Spec` so the text persists into `graph.json`, would buy real things:

- `resume` would get the conventions from the saved graph, with no staged file,
  no `state.json` record and no hash-mismatch refusal.
- `--plan-only` followed by `run` would carry them.
- The scheduler would need no new input, and §2.2's spawn-time rule would be
  unnecessary, because `basePrompt` would already hold the text.

That is four moving parts fewer. It loses anyway, on four counts, the first two
of which are decisive:

1. **A node's `Prompt` is a template, and operator prose is not.** Everything
   in `Prompt` is read by `Interpolate`, by `graph.Validate` on the re-parse,
   and by the lint sweeps (`LintPlaceholders` judges every `{{ … }}` token whose
   first word is placeholder-like, `placeholder_lint.go:31-55`). A style guide
   that documents this project's own template syntax, such as
   `{{ artifacts.build }}` or `{{ feedback.review }}`, would be *resolved* at
   run time, fail the plan's re-validation (a feedback token outside a loop
   body is refused at load), or raise lint warnings about text the planner never
   wrote. The template has no escape syntax (`handoff.go:74-76`), and the one
   neutralizer that existed was ADR 0012's skill-inlining one, which went away
   when ADR 0017 superseded that inlining. So "escape `{{`" means
   rewriting the operator's bytes. The node would then be told something other
   than what the operator named, and the hash on the plan screen would no longer
   describe it. The spawn-time prefix sits after `Interpolate` and outside the
   graph, so it never meets any of the three.
2. **Persisting the text into `graph.json` is the wrong carry for `run`.**
   `rebuildWithNotices` deliberately does *not* write the skill notice into
   `plan.Spec`. `graph.json` is re-runnable through `run`, which has a
   different context source, and a persisted notice would tell those nodes
   something untrue (`skillstage.go:926-933`). Conventions are the same shape
   in the other direction: a `run` node loads the operator's `CLAUDE.md`
   natively (ADR 0032 §1.3). Carrying the prefix into `run` double-delivers
   whatever that `CLAUDE.md` already loads, which is the reason §2.6 keeps the
   flag off `run`. Persisting would ship that double delivery through the back
   door. That comment also records that an unrelated later step once leaked
   the notice into `graph.json` by re-encoding (`:935-943`). A design whose
   correctness depends on every future post-plan step's ordering is a design
   that has already broken once.
3. **The graph stops being reviewable.** Up to 128 KiB copied into every node's
   `Prompt` makes `graph.json` mostly conventions text. The graph is the artifact a
   human approves, and burying each node's actual instruction under the same
   boilerplate is a cost to the thing the plan screen exists for.
4. **The retry rule is only moved, not removed.** A `handoff: session` node
   would carry the conventions a second time on its first turn, because the
   parent's turn already held them. That costs tokens, not correctness, but it
   is 17k–32k tokens per session child.

What this alternative gets right is kept: resume consistency. It is delivered
by the staged copy and the hash check (§2.6), which is ADR 0017's and ADR 0022's
existing pattern, not a new mechanism. The `--plan-only`/`run` gap it would have
closed is stated on the screen instead (§2.4). If §7(3) ever shows that
operators need conventions under `run`, the fix is a `run` flag with its own
double-delivery argument, not a template edit.

### 4.2 The other directions #282 lists, and the ones this ADR adds

- **Ship nothing, and point at `--accept-loaded-user-config`.** This loses
  because the bill does not fit the payload (§1.2). An operator who wants their
  style guide gives up grants enforcement, hooks isolation, MCP isolation,
  staged agents and staged skills. ADR 0032 §2.1 already rejected the same
  reasoning one level up: refusing a narrow door sends everyone through a wide
  one.
- **Let a planned node read the conventions directory, lifting the #268 deny.**
  This is the direct fix for #282's fact 3, and it loses because it is a
  **capability** crossing, not a text one. Granting a `Read` path outside the
  work directory is exactly the *"grants no path"* clause of ADR 0037 §3. That
  is the clause this ADR keeps intact while it bends the text clause (§2.3). It
  also leaves delivery at the node's discretion: the node must decide to read
  them, as with a skill (below). And the grant reaches everything else in that
  directory, not only the files that are conventions.
- **Agent frontmatter `skills:` preload for mapped nodes.** This loses on
  three counts. First, it reaches only agent-mapped nodes. Every unmapped node,
  which is the default, still gets nothing, so it is half a fix at best. Second,
  it works by changing the content of the definition oh-my-graph stages, and
  ADR 0022 promises that the staged definition is the operator's own and nothing
  else. Third, whether a frontmatter preload takes effect under
  `--setting-sources ""` with a staged `--plugin-dir` is unmeasured here, and
  this ADR will not rest on an unmeasured composition.
- **A skill whose body is the checklist (ADR 0017's corpus).** This loses
  because activation is at the model's discretion, and conventions must always
  apply. It also loses on reach: `applySkillActivation` excludes every
  agent-mapped node (`skillstage.go:887-890`), so the nodes that write the most
  code would miss the skill entirely.
- **Let the planner inline the conventions into each node's prompt.** #282
  notes that this grants nothing, and that is true: it passes ADR 0037 §3's
  capability test outright. It loses on **fidelity**, not capability. The
  planner restates operator text in its own words, per node, with nothing to
  check it against. There is no hash of what any node was told. A 17k-token
  corpus restated across N nodes also spends the planner's output budget on
  copying. And the planner already *can* do this today (it reads the
  operator's `CLAUDE.md`, ADR 0032 §1.4). #282 exists because, in practice, it
  does not do it reliably. This is ADR 0017's reason for superseding ADR 0012,
  and it now stands on the fidelity point rather than on trust.
- **Read `~/.claude/CLAUDE.md` automatically, with no flag.** This loses on
  three counts. It silently changes every `auto` run's prompts on upgrade,
  which contradicts the *"a run that types nothing is byte-for-byte"* rule
  ADR 0032 §6 holds. A user `CLAUDE.md` is often more than conventions: team
  identifiers, ops runbooks and tool instructions that make no sense inside a
  sandboxed node. And on Codex the matching file is a different path in a
  different product. The operator knows which text is *conventions*, and the
  flag lets them say so.
- **Resolve `@` imports.** This loses because it reads files the operator never
  named, transitively. With a repeatable flag the operator can name every file
  an import would have reached, so the only thing lost is implicitness. The
  import-only refusal (§2.5) turns the silent version of this gap into a loud
  one.
- **`--setting-sources user` (load only the user source).** This loses because
  the user's standing grants live in that same `~/.claude/settings.json`.
  Loading it brings `Bash(*)` back, which is exactly the layer-2 bill of
  ADR 0032 without that ADR's disclosure.
- **`--append-system-prompt-file` on Claude, and Codex `developer_instructions`.**
  This loses on §2.7: each runtime needs its own flag, the Codex half is
  unmeasured, and it touches the staged agent's system prompt that ADR 0022
  promises is the definition's alone.
- **A per-node `conventions:` graph field.** This loses on ADR 0032 §2.1: the
  planner writes the graph, and a planner that picks which operator files load
  into a node is the hole `validatePlannedNodeAgent` closes.
- **A single file with a 32 KiB cap (this ADR's first draft).** This loses on
  §1.4: it cannot carry the case that motivated it.
- **Truncate an oversize set with the fence's head+tail bound.** This loses on
  §2.5: instructions are not evidence.

## 5. Tests the implementation owes

Unit tests use `FakeRunner`, with no real spawn.

1. **Default off.** With no flag, every node's `NodeInvocation.Prompt` equals
   its interpolated prompt byte for byte, nothing is staged, and `state.json`
   has no conventions record and keeps `schema: 3`.
2. **Prefix shape.** With two files, a fresh spawn's prompt is the §2.4 header,
   file 1 under `1/2` and its basename, file 2 under `2/2`, the separator, and
   then the interpolated node prompt, unchanged, in that order. The prompt
   contains neither source's absolute path nor any hash.
3. **Not interpolated, not linted.** A conventions file that contains
   `{{ inputs.x }}` and `{{ feedback.y }}` reaches the prompt literally. The
   plan validates, and `LintPlaceholders` raises nothing about it.
4. **A session-resuming first attempt is not prefixed.** A `handoff: session`
   node's first invocation (non-empty `ResumeSession`) has no header.
5. **A cold retry of that same node *is* prefixed.** The FakeRunner fails the
   session node's first attempt. The retried invocation has
   `ResumeSession == ""` and starts with the header. The same holds for the
   cross-leg case: a snapshot whose session node failed in the prior leg
   (`startCold` path), resumed, is prefixed on its first spawn in this leg.
6. **No stacking across attempts.** On a third attempt the header appears
   exactly once, and the ADR 0020 prior-attempt quote follows the node prompt.
7. **The ceiling does not move.** Every planned node's `ToolPolicy` is identical
   with and without the flag. That includes `SettingSources == &""` and
   `StrictMCPConfig == true`, plus an agent-mapped node that keeps its
   `--agent`/`--plugin-dir`. `graph.json` is byte-identical with and without
   the flag.
8. **The motivating corpus fits.** Five files totalling 70 KiB (the shape of
   #282's corpus) are accepted, staged in order and prefixed.
9. **Refusals.** One case each for: a missing path, a directory, a blank file,
   an import-only file (the message names the import targets), the same file
   twice, a total of 128 KiB + 1 byte split across two files (the message names
   each size and the total), and invalid UTF-8 in the second of two files. Each
   refuses before the planner is called (the fake planner records zero calls).
   No message contains any file's content.
10. **Exactly at the cap is accepted.** A total of exactly 128 KiB is accepted
    and staged.
11. **The staged copy is authoritative.** Editing a source file after launch
    does not change what later nodes receive.
12. **Resume.** A matching staged copy is prefixed and the lines are reprinted.
    A missing or altered staged copy refuses and names both hashes. `resume`
    parses no `--conventions` flag.
13. **Downgrade is refused.** A snapshot written with conventions carries
    `schema: 4`. A reader whose `Schema` is 3 refuses it with
    `SchemaMismatchError`. The current reader loads both 3 and 4.
14. **The plan screen prints the lines verbatim**, on a normal launch and on a
    later goal-loop cycle. The test asserts the whole literal. A mixed file's
    line carries its `(N @-import lines not followed)` suffix.
15. **`--plan-only` says the conventions do not carry.** The preview prints the
    §2.4 *NOT in the saved graph* line, and not the *prefixed* line.
16. **The environment is untouched.** A spawn under the flag still scrubs all
    four variables.

## 6. Failure modes and compatibility

**Failure modes.**

- **The motivating corpus doesn't fit.** This was the first draft's failure,
  and it is now a tested one (§5, test 8). The two ways it could come back are
  a cap sized below the corpus, or an operator whose conventions sit behind
  imports and who names only the importing file. The cap is sized from the
  corpus (§2.5). The import-only refusal turns the second into a message that
  names the files to list. A *mixed* file whose real content sits behind
  imports is still accepted, and the plan screen's `@-import lines not
  followed` suffix is the only signal. That residue is documented in
  LIMITATIONS.
- **A corpus that outgrows 128 KiB.** It is refused whole and never truncated.
  The operator shortens or splits it on purpose. §7(4) is what would move the
  cap.
- **The operator reads the flag as "my `CLAUDE.md` works now".** Their hooks,
  MCP servers, grants and `@` imports still do not arrive. The plan-screen line
  says *text only* for this reason, and LIMITATIONS says it again where the
  operator meets the gap.
- **The conventions ask for a tool the node does not hold.** The node tries,
  and the ceiling denies the call as it would deny any prompt-borne request.
  This is visible as a permission denial, not as a silent skip.
- **The operator names a repository file (`./CLAUDE.md` of a fresh clone).**
  Then the repository's author writes into every node's prompt, which is the
  injection class ADR 0022 closed for agent definitions. That ADR closed it
  because the scan was **automatic**. Here the operator types the path, and the
  plan screen prints the path and hash before any node spends. It is
  documented, not refused: refusing paths under the cwd would also refuse the
  legitimate case of a project's own checked-in style guide.
- **Token cost.** Up to about 32k tokens on every fresh spawn, retries
  included. The cap bounds it, and the byte count on the plan screen shows it.
- **Positional weight.** Prompt text may be weighted below a system prompt.
  This is accepted in §2.7, and §7(1) is the falsifier.
- **A `--plan-only` preview run with `run`** gets no conventions. The preview
  says so (§2.4). This is a stated gap, not a silent one.

**Compatibility.**

- **No flag: identical everywhere.** Same argv, prompts, screens, `graph.json`
  and `state.json`, including `schema: 3`.
- **`state.json`, forward.** The conventions record is a new `omitempty`
  object. A current reader resumes an old snapshot with no conventions, which
  is correct because that run had none.
- **`state.json`, downgrade.** An `omitempty` record alone would let an
  **older** binary resume a newer run and silently carry on without the
  conventions. That is the "siblings told different things" outcome §2.6
  refuses on the newer binary. So it is **blocked, not accepted as a gap**: a
  snapshot that carries the conventions record is written with `schema: 4`. An
  older reader's strict equality check (`runstate.go:622-638`,
  `SchemaMismatchError`) then refuses it by name. The current reader accepts 3
  and 4, and a run without the flag keeps writing 3, so the bump costs nothing
  to anyone who does not use the flag. The `Schema` doc comment records why
  4 is conditional, because this is the first conditional stamp.
- **Graph schema, `run`, `chat`, `lint`, `--dry-run`: untouched.**
- **Documentation owed in the same PR:** README (the isolation boundary
  paragraph), `docs/LIMITATIONS.md` (text only, no imports and the mixed-file
  residue, the total cap, basenames reach transcripts, the repo-path caveat, and
  no carry into `run`), `docs/RUN-FEED.md`'s `state.json` section (the record
  and schema 4), and a `## [Unreleased]` entry in CHANGELOG. DESIGN.md's
  ceiling section should name the flag as the one thing that crosses beside
  `model`.

## 7. Falsification

1. **Planned nodes ignore prefixed conventions where the same text loaded as
   `CLAUDE.md` is followed.** If a measurement shows this (same task, same file,
   prefix against native load, 3 spawns per arm), then the prompt position is
   the wrong carrier. The system-prompt route in §4.2 would then have to be
   reopened, along with the ADR 0022 measurement it owes.
2. **Operators keep reaching for `--accept-loaded-user-config` just to get
   conventions.** Then the narrow door failed to be found, and the defect is in
   the docs or the flag's name, not in the mechanism.
3. **A request for per-node conventions, or for conventions under `run`, that
   §4 cannot answer** would show that whole-`auto`-run granularity is the wrong
   unit.
4. **A second measured corpus above 128 KiB from an operator whose conventions
   cannot reasonably be shortened.** The cap was set from one corpus, #282's.
   A second data point above it is what would move the number, and the move
   goes through this section, not through a quiet constant change.

## 8. Review findings and where each is answered

| # | finding | answer |
| --- | --- | --- |
| 1 | Critical: the design does not fit #282's five docs, ~17k tokens behind `@` imports | Incorporated. Repeatable flag (§2.1), total cap of 128 KiB sized from the corpus (§2.5), import-only refusal (§2.5), a failure-mode entry (§6) and test 8. |
| 2 | High: retries never resume, so the prefix rule dropped conventions on a cold retry | Incorporated. The prefix keys on `ResumeSession == ""` at the `s.runner.Run` call, after `startCold` (§2.2). Tests 4, 5 and 6. |
| 3 | High: the plan-time prompt edit (`skillstage.go:898`) was not weighed | Weighed and rejected, with its gains stated (§4.1). The template, lint and validate collision, and the `run` double delivery, decide it. |
| 4 | Medium: `--plan-only` promised a prefix `run` does not deliver | Incorporated. The preview prints a *NOT in the saved graph* line instead (§2.4). Test 15. |
| 5 | Medium: other directions in the issue were not weighed | Incorporated. Lifting the #268 deny, `skills:` preload, a checklist skill, and the planner-inlining "grants nothing" point are each answered (§4.2). |
| 6 | Low: an older binary resumes a newer run without conventions | Incorporated as a block, not a gap. The snapshot is stamped `schema: 4` only when it carries conventions (§6). Test 13. |
| 7 | Low: the header's contents were unspecified | Incorporated. Ordinal and basename only, with no path and no hash in the node prompt. Paths and hashes appear on the operator's screen only (§2.4). Test 2. |
