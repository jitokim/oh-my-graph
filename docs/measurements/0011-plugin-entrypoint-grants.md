# 0011 — a wide prefix grant did not bind the verb, the narrow case has no instances, and no `oh-my-graph` grant was ever in force

Backlog item #11 — kept by the operator outside this repository, at
`~/IdeaProjects/oh-my-graph-hq/notes/open.md:51`, and therefore **not
addressable from inside it** — records a suspected inconsistency between the
plugin's three entry points: *the `/graph` command and the `run-graph` skill
cannot reach `--runtime codex`, while the agent can*, and its own next action is
**먼저 재현** — *reproduce first: can `Bash(oh-my-graph *)` really not restrict
which command runs? If that is true, the decision item itself changes.* This
document answers that, and only that. The three declarations were read from the
working tree at HEAD `8658ebd58f40a3cbf2f85b3ca473bc340b99d52d`
(`git log -1 --format=%H`), where `git status --short` printed one line, the
untracked measurement program this document cites. No product file and no file
under `plugin/` was edited: `git diff --stat -- plugin/` is empty at that HEAD.

## The three declarations

Verbatim, in the exact characters, copied from the files at the HEAD above. The
disagreement, if there is one, lives in these strings — so they are not
summarised and not normalised.

`plugin/commands/graph.md:4` — the `/graph` slash command:

```
allowed-tools: Bash(oh-my-graph run *), Bash(oh-my-graph auto *)
```

`plugin/skills/run-graph/SKILL.md:5` — the `run-graph` skill:

```
allowed-tools: Bash(oh-my-graph run *)
```

`plugin/agents/oh-my-graph.md:4` — the `oh-my-graph` agent. **Note the field is
`tools`, not `allowed-tools`:**

```
tools: Bash(oh-my-graph *), Bash(git *), Bash(gh *), Read, Edit, Write, Grep, Glob, Skill, Agent
```

| entry point | field | grant string | address |
| --- | --- | --- | --- |
| `/graph` | `allowed-tools` | `Bash(oh-my-graph run *), Bash(oh-my-graph auto *)` | `plugin/commands/graph.md:4` |
| `run-graph` skill | `allowed-tools` | `Bash(oh-my-graph run *)` | `plugin/skills/run-graph/SKILL.md:5` |
| agent | `tools` | `Bash(oh-my-graph *), Bash(git *), Bash(gh *), Read, Edit, Write, Grep, Glob, Skill, Agent` | `plugin/agents/oh-my-graph.md:4` |

Three declarations, three different shapes: the field name differs (twice
`allowed-tools`, once `tools`); the skill lacks the `auto` grant the command
has; only the agent uses the subcommand-less form. All three entry points named
by the item exist as files — `git ls-files plugin` returns exactly five paths,
these three plus `plugin/.claude-plugin/plugin.json` and `plugin/README.md`. The
manifest carries **no** permission field at all: its whole content is `name`,
`description`, `version` (`0.11.0`, `plugin/.claude-plugin/plugin.json:4`) and
`author`.

Two further strings in the same files bound what can be invoked, and they are
prose rather than grants. `plugin/commands/graph.md:7`:

```
Run `oh-my-graph $ARGUMENTS` via Bash. The two subcommands are:
```

`plugin/skills/run-graph/SKILL.md:25`:

```
2. Run via Bash: `oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]`
```

The command passes the user's whole argument string through; the skill's
template has `run` baked in and enumerates four optional flags, none of them
`--runtime`, with no pass-through slot. The codex section below turns on that
difference.

## Does a prefix grant distinguish subcommands?

**Re-run the whole thing with one command, from the repository root:**

```
go run docs/measurements/0011-plugin-entrypoint-grants.go
```

It takes no arguments, writes no files, and prints the entire report to stdout;
the section number (`§1`…`§7`) is named beside every figure below the first time
it appears. `go run` with an explicit file argument does not apply build
constraints, which is why the program's `//go:build ignore` tag keeps it out of
`go build ./...` and `go test ./...` while leaving it runnable — the convention
of [`0218-denied-nodes-that-passed.go`](0218-denied-nodes-that-passed.go) and
[`0213b-compound-commands.go`](0213b-compound-commands.go). It parses every
JSONL record with `encoding/json`; it never greps the corpus.

### The verdict, in three parts

| | question | answer |
| --- | --- | --- |
| wide | does `Bash(<prog> *)` bind the second word? | **No** — 20 distinct second words ran under one grant, 1 denial in the whole population unexplained by anything already measured (§3) |
| narrow | does `Bash(<prog> <verb> *)` deny a sibling verb? | **I could not establish it from the evidence available** — the contrast case has zero instances (§3) |
| `oh-my-graph` | what did the matcher do to an `oh-my-graph` grant? | **Nothing to read** — no such grant was ever in force on this machine (§2) |

### The corpus

Read from §1, from the run transcribed below. **These particular counts are not
stable and the program says so:** `~/.claude/projects` holds the transcript of
the session running the program, so the file/line/`tool_use` figures grow while
it reads. Measured, not assumed — a second run minutes later reported 2714 files
/ 232655 lines / 55357 `tool_use` / 33195 Bash `tool_use`, against the 2713 /
232323 / 55279 / 33142 below.

**What is stable is everything the verdict rests on.** Diffing the two runs from
the line `§3 (b)` onward leaves exactly two differing lines, both raw intake
counters that include the reader's own session (`Bash calls examined` 33142 →
33195, `session has no run record, excluded` 9727 → 9780). The eligible
population, every per-grant table, and §4–§7 are byte-identical, because they are
restricted to sessions holding an isolated `tool_policies` record and a session
reading the corpus is not one of those.

| | count |
| --- | ---: |
| transcript files under `~/.claude/projects`, parsed / errored mid-read | 2713 / **0** |
| JSONL lines parsed / lines that would not decode | 232323 / **0** |
| `tool_use` blocks / Bash `tool_use` blocks | 55279 / 33142 |
| `tool_result` blocks / naming no `tool_use` | 55276 / **0** |
| run directories / no `state.json` / unparseable | 344 / 3 / **0** |
| planned / hand-written / in-flight (excluded, each named in §1) | 42 / 299 / 8 |
| `tool_policies` records, all runs | 198 |
| …ISOLATED (`setting_sources` present) / unisolated | 106 / 92 |

The 106/92 split is the load-bearing filter, not a detail. `setting_sources` is
`omitempty` (`internal/runstate/runstate.go:130-134`), so an *absent* key means
the node loaded the user's own settings — and this machine's
`~/.claude/settings.json:4` declares `Bash(*)`, one rule that allows every Bash
call ever made. Counting
those 92 would produce headlines like "a node holding only `Bash(gh pr *)` ran
`gh issue create`": true, and evidence of nothing.

### `oh-my-graph` itself: 79 calls, and none of them was judged by an `oh-my-graph` grant

From §2, which came back identical across both runs (`diff` of the two outputs
between `§2 (a)` and `§3 (b)`: empty). Bash calls whose command's own first word
is `oh-my-graph`: **79**.

| subcommand | permitted | denied |
| --- | ---: | ---: |
| `resume` | 29 | 0 |
| `lint` | 25 | 0 |
| `runs` | 12 | 0 |
| `run` | 8 | 0 |
| `show` | 3 | 0 |
| `init` | 1 | 0 |
| `version` | 1 | 0 |

**All 79 ran in a session with no run record** (§2: `isolated 0 | unisolated 0 |
no run record 79`) — operator sessions, not engine nodes. And §2's closing line
is the one that decides this section:

> grants naming oh-my-graph in ANY tool_policies record on this machine: NONE — so no call above was judged against one

§7 lists all **12** distinct grant strings across every policy; not one of them
names `oh-my-graph`. So the table above is 79 rows of what a shell allowed, not
one row of what a grant matched. Two neighbouring populations are counted apart
in §2 rather than folded in, because a token prefix could never match them
anyway: **856** calls where `oh-my-graph` is a *later* sub-command
(`cd x && oh-my-graph run …` — 0213b measured that a later piece can defeat a
grant) and **188** path-spelled calls (`./bin/oh-my-graph …`).

### THE ANALOGY — labelled as an analogy, because it is one

**This is not a measurement of `oh-my-graph`.** It is how a prefix grant *of the
same shape* behaved, on the programs this corpus does hold in volume. Every
sentence carried from here to `Bash(oh-my-graph *)` is inference.

Population (§3): 33142 Bash calls examined → 4113 sidechain excluded (a sub-agent
runs under its own policy), 9727 with no run record, 17859 unisolated, 0 whose
policy held a wide `Bash`/`Bash(*)` → **1443 eligible**, of which **683** had
their program named by an in-force grant, and **0** matched more than one grant.
The first two of those figures are the two that drift with the reader's own
session, as above; every figure from `unisolated` onward held identical across
runs.

Two known confounds are separated before any denial counts as evidence, each
with its own prior measurement: `cmp` = the command was compound
([0213b](0213b-compound-commands-defeat-grants.md)), `out` = a path argument
reaches outside the node's cwd (0213b §9). `!!` = neither, and `!!` is the only
shape a grant refusing a subcommand could look like.

**`Bash(git *)` — token prefix `[git]`, 435 calls in 52 isolated sessions (§3):**

| second word | permitted | denied | cmp | out | !! |
| --- | ---: | ---: | ---: | ---: | ---: |
| `log` | 107 | 7 | 6 | 1 | **1** |
| `diff` | 75 | 4 | 4 | 0 | 0 |
| `show` | 49 | 0 | – | – | – |
| `status` | 33 | 1 | 0 | 1 | 0 |
| `add` | 31 | 4 | 4 | 0 | 0 |
| `rev-parse` | 23 | 5 | 5 | 1 | 0 |
| `commit` | 22 | 0 | – | – | – |
| `grep` | 13 | 0 | – | – | – |
| `branch` | 9 | 0 | – | – | – |
| `push` | 4 | 0 | – | – | – |
| `config` | 3 | 1 | 1 | 0 | 0 |
| `remote` | 3 | 1 | 1 | 0 | 0 |
| `tag` | 3 | 0 | – | – | – |
| `ls-remote` | 2 | 1 | 1 | 0 | 0 |
| `checkout` | 2 | 0 | – | – | – |
| `clean` | 1 | 0 | – | – | – |
| `ls-tree` | 1 | 0 | – | – | – |
| `rev-list` | 0 | 3 | 3 | 0 | 0 |
| (flags, 2 distinct) | 23 | 0 | – | – | – |

**20 distinct values at the position this grant's `*` covers, 11 of them with
zero denials, and exactly 1 denial in the entire `git` population unexplained by
compound shape or by leaving the cwd** — and that one is
`git log --oneline -1 --grep $'probe ansi-c quoting'` (§4), an ANSI-C quoting
case, not a verb. The writing verbs are the point: `add` (31), `commit` (22),
`push` (4), `tag` (3), `checkout` (2), `clean` (1) all ran under a grant that
names none of them.

The other wide grants have the same shape (§3): `Bash(go *)` 6 second words / **0**
unexplained denials; `Bash(make *)` 5 / **0**; `Bash(ls *)` 19 / **0**;
`Bash(grep *)` 3 / **0**; `Bash(cat *)` 4 / **0**. Across every grant in §3, §5
totals **27 distinct bare verbs, 520 permitted, 61 denied, of which 9
unexplained**.

**The narrow grant, `Bash(gh pr *)` — token prefix `[gh, pr]`, 34 calls in 6
isolated sessions (§3).** At token index 2 — the first position this grant's `*`
covers — four distinct values: `create` (11 permitted / 8 denied), `view` (5/4),
`list` (5/0), `edit` (1/0). **No isolated node ever attempted a `gh` verb this
grant does not name.** §3 prints the decisive line for every grant it reports,
this one included:

```
0 call(s) fall outside this grant's own token prefix.
```

The row that would decide whether narrowing binds has **zero instances**. So:
**I could not establish from the evidence available whether a narrow
`Bash(<prog> <verb> *)` binds the verb.** That is the finding, not a gap to be
closed by reasoning.

### The 9 unexplained denials are not about a verb

§4 prints all **9** in full. They are all from one node,
`20260819-163447.441137000-2/pr`, grants `Read, Bash(git *), Bash(gh pr *)`: 8
`gh pr create` and 1 `git log --grep $'…'`. §4's same-verb contrast is what
rules the verb out:

```
"gh pr" — permitted 22, denied 12, other 0, all under a grant whose token
  prefix reaches this exact call.
    DENIED    len=65    nl=false ansiC=false backtick=false
    DENIED    len=1872  nl=true  ansiC=false backtick=false
    DENIED    len=3347  nl=false ansiC=true  backtick=true
    DENIED    len=6377  nl=false ansiC=true  backtick=true    (… 12 rows)
    PERMITTED len=252   nl=true  ansiC=false backtick=true
    PERMITTED len=3272  nl=false ansiC=false backtick=true    (… 6 rows)
    PERMITTED with none of those shapes: 16 call(s), len 19..3514
```

Same program, same verb, both outcomes, differing only in argument text.

### Reconciliation with the two prior measurements

Neither is contradicted.

1. [`0213b-compound-commands-defeat-grants.md:249-254`](0213b-compound-commands-defeat-grants.md)
   already names this exact cluster — *"nine calls from a single node
   (`20260819-163447.441137000-2`/`pr`) retrying `gh pr create` under
   `Bash(gh pr *)` with a multi-kilobyte `--body`"* — and at `:256-261`
   concludes that *"the discriminator inside (C) is not the number of
   sub-commands but the argument text and path scope"*. §4 reproduces that set
   exactly, by a different filter (isolated-policy sessions across all runs, not
   planned runs only). One correction to its prose, not to its finding: the
   ninth is `git log --grep $'…'`, not a ninth `gh pr create` (§4).
2. An earlier `docs/measurements/0011-plugin-entrypoint-grants.md` exists at
   commit `4e6c6fc` on branch `lane-b3`, which is **not an ancestor of `main`**
   (`git merge-base --is-ancestor 4e6c6fc HEAD` exits 1) and so is absent from
   this working tree. Its verdict is this one, reached independently: *"a WIDE
   prefix grant did not bind the verb … Whether a NARROW grant denies a sibling
   verb is COULD NOT DETERMINE … no node session in the corpus ever held an
   `oh-my-graph` grant at all."* Its raw counts differ for a stated corpus
   reason, not a disagreement: **that run excluded compound commands entirely**
   (its §2: `skipped as COMPOUND … 23494`), this one includes them and separates
   them into a column — hence `Bash(git *)` 197 calls / 2 denials there against
   435 / 27 here. Its two `git` denials (`git log --grep $'…'`,
   `git status --porcelain > /tmp/…`) are precisely this document's 1 `!!` plus
   1 `out`: same two calls, same explanation, different label.

## The codex claim at HEAD

The claim in item #11: *`/graph` and the skill cannot reach `--runtime codex`,
while the agent can.* Resting only on the strings quoted above, it splits into
one half that holds and one that is undetermined.

**The agent half holds.** The invocation is `oh-my-graph --runtime codex run
<graph.yaml>` — the flag is global and must precede the subcommand
(`plugin/agents/oh-my-graph.md:17-18`). The literal part of the agent's grant is
`oh-my-graph ` (`plugin/agents/oh-my-graph.md:4`), and that invocation begins
with exactly those characters, so on the string alone the grant contains it. This
half does not depend on the undetermined question below: the grant names no verb,
so nothing has to be assumed about how `*` treats a second word. Two further
things point the same way and are cited as claims of the files that make them,
not as things re-verified here: `plugin/README.md:45-48` says the pattern "does
cover `oh-my-graph --runtime codex run ...`", and `plugin/README.md:193-199`
records a smoke test in which the `tools` field "does not actually restrict which
shell commands run — the agent could run an out-of-list `echo`". The agent's
prose points toward the second runtime rather than away
(`plugin/agents/oh-my-graph.md:16-24`), though its own CLI-surface block
(`plugin/agents/oh-my-graph.md:28-44`) lists 11 invocations — one line each for
`init`, `run`, `auto`, `lint`, `resume`, `runs list`, `show`, `watch`, `serve`,
`chat`, `version` — and `--runtime` appears on none of them.

**The `/graph` and skill half is UNDETERMINED.** Neither declaration contains the
characters `--runtime` or `codex` (`plugin/commands/graph.md:4`,
`plugin/skills/run-graph/SKILL.md:5`), and the literal part of each grant —
`oh-my-graph run `, `oh-my-graph auto ` — does not begin the codex invocation; it
appears mid-string, after `--runtime codex`. Whether that mismatch blocks the
call is a property of the matcher, which lives in the Claude Code binary and not
in this repository, and **the measurement above could not establish it from the
evidence available**: §3 reports `0 call(s) fall outside this grant's own token
prefix` for every grant in the corpus, so no record exists of what happens when a
call falls outside one. The files assert the blocking themselves
(`plugin/commands/graph.md:20-24` "which this command's grants do not cover";
`plugin/skills/run-graph/SKILL.md:14-19` "matches the prefix `oh-my-graph run`
… only"; `plugin/README.md:31-38`), but a file's claim about a matcher is not a
reading of one.

**Two corrections that do not depend on the matcher.** First, "cannot" is too
strong on the README's own account: a prefix mismatch "raises a per-use
permission prompt instead of running unprompted — what happens at that prompt is
then up to your own session permission rules, which is where a standing `Bash(*)`
would still match" (`plugin/README.md:35-40`). The accurate wording is **cannot
reach it unprompted**. Second, the two blocked surfaces are not the same case.
`/graph` passes the user's whole argument string through (`$ARGUMENTS`,
`plugin/commands/graph.md:7`), so its only obstacles are the grant and the prose;
the skill additionally hard-codes `run` in its own instruction
(`plugin/skills/run-graph/SKILL.md:25`) with no slot for a global flag. Widening
the grant alone would leave the skill still writing `oh-my-graph run …`.

**What would settle it:** one node, run with `--setting-sources ""` so the user's
`Bash(*)` cannot decide the outcome, holding only `Bash(oh-my-graph run *)` and
attempting `oh-my-graph --runtime codex run <graph.yaml> --dry-run`; and a second
holding only `Bash(oh-my-graph *)` attempting the same command. Two runs, two
records in `~/.oh-my-graph/runs/*/`, and the question is closed. The same probe
in cheaper form — a node holding only `Bash(oh-my-graph run *)` attempting
`oh-my-graph serve --no-open` — settles the narrow-grant question on its own. One
denial answers it; this corpus never will.

## What the decision item should now be

A wide prefix grant did not bind the second word anywhere this corpus could test
it (§3, §5). Carried to `plugin/agents/oh-my-graph.md:4` — as inference, because
no `oh-my-graph` grant was ever in force (§2) — that makes the agent's grant a
grant of every subcommand: `serve`, `resume`, `chat`, `init`, all of them, not
just the codex flag the item noticed. So item #11 stops being *reconcile the
three surfaces on `--runtime codex`* and becomes:

1. **Run the two-node probe in the section above.** It is the item's own next
   action ("먼저 재현"), it is the only thing that settles the narrow half, and
   the corpus has already proved it cannot substitute for it.
2. **Then decide whether the agent's wide grant should be narrowed.** The
   direction of travel is narrowing, not widening: the surface that needs
   looking at is the one that grants more than it meant to, not the two that
   grant less.
3. **Widen nothing in the meantime.** `plugin/README.md:53-62` already records
   why the command grants are deliberately narrow — covering the flag by pattern
   means enumerating six argv spellings for one boolean — and nothing measured
   here cuts against that. Codex runs start from a shell.
4. **Restate the claim in the files that make it.** "Cannot reach" should read
   "not pre-granted" wherever it appears (`plugin/commands/graph.md:20-24`,
   `plugin/skills/run-graph/SKILL.md:14-19`, `plugin/README.md:31-38`), because
   the effect of a mismatch is a prompt and the session's own rules decide it.

## What could not be established

- **Why any denial happened.** The denial text carries no reason code — §7 says
  it is byte-identical for an out-of-scope command, a compound command whose
  later piece was ungranted, and a path-sensitive refusal. Everything above is a
  correlation between command shape and outcome, never a causal reading.
- **Whether narrowing a grant binds the verb.** Unmeasured, not disproven.
- **What the 8 `gh pr create` denials were really about.** §4 shows length,
  newlines, `$'…'` quoting and backticks track them better than the verb does,
  but permitted rows carry those same shapes (`len=3272 backtick=true`
  permitted; `len=65` plain denied), so no single flag is the rule. Fitting a
  threshold to §4's 12 denied and 22 permitted `gh pr` rows would measure the
  fitting.
- **An ALLOW is weak evidence here.** §6: **760** eligible calls ran (or were
  denied) whose program *no* in-force grant named — `cd` 361, `grep` 114, `sed`
  101, `git` 16, `go` 11 — with 166 denials. The matcher is not a pure lookup
  over the grant list the engine passed, which is why the verdict rests on 20
  second words across 435 calls rather than on any single allow.
- **The `claude` version that produced this corpus.** No run record carries it
  and asking this machine reports today's (§7). If grant matching changed across
  versions, this is two populations.
- **Calls hidden behind a leading `cd`.** §3 counts only calls whose own first
  word is the granted program; the 361 `cd`-headed calls in §6 contain `git` and
  `go` invocations these tables never see.
- **Whether `oh-my-graph run g.yaml --runtime codex` is rejected by the CLI.**
  `plugin/README.md:40-41` says it exits with
  `flag provided but not defined: -runtime`. No binary was run for this
  measurement; that is the README's claim, quoted as such.

**No CHANGELOG entry accompanies this measurement: it changes no product
behaviour, edits no file under `plugin/`, and ships nothing an operator can
observe.**
