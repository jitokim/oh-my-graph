# 0011 — a wide prefix grant did not bind the verb, a narrow one could not be tested, and no `oh-my-graph` grant was ever in force

**Under `Bash(git *)` in this corpus, 13 distinct git verbs ran clean — including
`add`, `commit`, `push` and `clean` — and the 2 denials under that grant both
carry a non-verb explanation. So a WIDE prefix grant did not bind the verb.
Whether a NARROW grant (`Bash(gh pr *)`) denies a sibling verb is
**COULD NOT DETERMINE**: the corpus contains exactly one narrow-granted program
and no node ever attempted a verb that grant did not name. And no node session
in the corpus ever held an `oh-my-graph` grant at all, so every sentence in this
document about `Bash(oh-my-graph *)` specifically is `inference` from `git`,
`go`, `make` and `gh`.**

Every number below is read from the report printed by

```
go run docs/measurements/0011-plugin-entrypoint-grants.go
```

which writes no files — its stdout is the whole report — and the section number
(`§1`…`§7`) is named beside each figure the first time it appears. The
declarations in section 3 were re-read from the working tree at HEAD
`be13995e8e36362779653b024865e32000eb1353` (`git log -1 --format=%H`), with
`git status --short` empty.

**No CHANGELOG entry accompanies this measurement: it changes no product
behaviour, edits no file under `plugin/`, and ships nothing an operator can
observe.**

## 1. The question

Backlog item #11 — kept by the operator in a separate repository,
`~/IdeaProjects/oh-my-graph-hq/notes/open.md`, and therefore **not addressable
from inside this repository** — records a suspected inconsistency between the
plugin's three entry points: *the `/graph` command and the skill cannot reach
`--runtime codex`, while the agent can.* Its own next action is what this run
executed, quoted from the goal text of run `20260822-010107.356534000-1`
(`state.json` → `goal.text`, the retraceable address for the wording below):

> **먼저 재현**: `Bash(oh-my-graph *)`가 정말 어떤 명령이 실행되는지 제한 못 하는가.
> 사실이면 결정 항목 자체가 바뀐다.
>
> (*Reproduce first: can `Bash(oh-my-graph *)` really not restrict which command
> runs? If that is true, the decision item itself changes.*)

**This is a reproduction, not a fix.** Nothing was widened. No file under
`plugin/` was edited — `git diff --stat 8658ebd be13995 -- plugin/` is empty, and
the only files this lane added to the repository are the measurement program
(`docs/measurements/0011-plugin-entrypoint-grants.go`, commit `be13995`) and this
document. The deliverable is an answer with evidence.

The precise form of the question: a grant of the shape `Bash(<prog> *)`
authorises the *binary*. Does the matcher therefore authorise **every
subcommand** — `run`, `auto`, `resume`, `serve` — because it sees one program
with arguments and not a verb? If so, an entry point that grants
`Bash(oh-my-graph *)` to be helpful granted strictly more than it meant to, and
"the three entry points disagree" is not the problem to solve.

## 2. Does a prefix grant distinguish subcommands?

### The verdict, in two halves

| | question | answer |
| --- | --- | --- |
| (b) | does a **wide** `Bash(<prog> *)` bind the verb? | **No** — 13 verbs ran clean under one grant (§5) |
| (c) | does a **narrow** `Bash(<prog> <verb> *)` deny a sibling verb? | **COULD NOT DETERMINE** — the contrast case does not exist in this corpus (§6) |

Reproduce with the single command above, from the repository root. `go run` with
an explicit file argument does not apply build constraints, which is why the
program's `//go:build ignore` tag keeps it out of `go build ./...` and
`go test ./...` while leaving it runnable — the convention of
[`0218-denied-nodes-that-passed.go`](0218-denied-nodes-that-passed.go) and
[`0213b-compound-commands.go`](0213b-compound-commands.go).

### The corpus, and the filter that decides it

| | count | section |
| --- | --- | --- |
| jsonl files matched under `~/.claude/projects` | 2442 | §1 |
| excluded — this lane's own project dir `-private-tmp-w-b3` | 4 (2 were node sessions) | §1 |
| jsonl files read / lines parsed / lines that would not decode | 2438 / 204963 / **0** | §1 |
| Bash `tool_use` blocks parsed | 28395 | §2 |
| …policy-denied | 400 | §2 |
| …skipped as COMPOUND (`\|`, `;`, `&&`) — measured separately by 0213b | 23494 | §2 |
| simple Bash calls in the tables | 4901 | §2 |
| …of those, inside a node session | 1024 calls in 141 transcripts, 78 denials | §2 |

Two confounds decide the result, and the program prints both rather than
assuming past them.

The first: `/Users/imac/.claude/settings.json:4` declares `Bash(*)` (§3). In any
session that loads it, one rule allows every Bash call ever made.

The second is the one that matters, and it is why §7's whole-corpus tally is
**not** the evidence. `internal/runstate/runstate.go:130-134` and
`internal/runner/runner.go:55-60`: `setting_sources` is a `*string`, a pointer to
`""` renders `--setting-sources ""` and **nil omits the flag**, so the user's
settings load as usual. The field is `omitempty`, so an *absent*
`setting_sources` on disk means the `Bash(*)` above was in force for that node.
§4 splits the corpus on exactly that:

| node policies | transcripts | calls | denials | |
| --- | --- | --- | --- | --- |
| **ISOLATED** (`setting_sources == ""`) | 95 | 507 | **78** | the measured population |
| LOADED user settings (absent) | 46 | 517 | **0** | excluded |

106 policies isolated, 63 loaded (§4). The isolated population *does* get
denied, which is the check that its `--allowedTools` list was really in force —
not an assumption. Counting the other 63 would have produced the headline "a node
holding only `Bash(gh pr *)` ran `gh issue create`": true, and evidence of
nothing.

### (b) One wide grant, many verbs — the verdict

From §5, on isolated node sessions only:

| grant | clean verbs | denied | calls | verbs that ran clean |
| --- | --- | --- | --- | --- |
| `Bash(git *)` (17 sites, first `graphs/adr-driven-dev.yaml:76`, §3) | **13** | 2 | 197 | add, branch, clean, commit, config, diff, grep, ls-remote, push, remote, rev-parse, show, tag |
| `Bash(go *)` (3 sites, first `graphs/backlog-batch.yaml:110`) | 5 | 1 | 43 | build, doc, test, version, vet |
| `Bash(make *)` (9 sites, first `graphs/adr-driven-dev.yaml:175`) | 4 | **0** | 24 | build, fmt-check, test, vet |
| `Bash(ls *)` | 5 | 2 | 7 | (five distinct paths) |
| `Bash(cat *)` | 1 | 1 | 2 | (one path) |

The writing verbs are the point: `git add` (7 allowed), `git commit` (7),
`git push` (1), `git clean` (1) all ran under a grant that named none of them.
`make` is the cleanest row — four verbs, twenty-four calls, not one denial.

Every denial printed under a wide grant carries a non-verb explanation, labelled
beside it by the program, so the verb is never left as the only hypothesis:

- `git log --oneline -1 --grep $'probe ansi-c quoting'` — `[ansi-c]`, node `20260819-163447.441137000-2/pr`
- `git status --porcelain > /tmp/omg-204-probe.txt` — `[redirect+abs-path]`, same node
- `go run docs/measurements/0218-denied-nodes-that-passed.go > /tmp/0218-run2.txt` — `[redirect+abs-path]`, node `20260820-174446.982693000-1/script`
- `ls /Users/imac`, `ls /Users/imac/.oh-my-graph/runs` — `[abs-path]`, node `20260820-195627.390122000-1/survey`
- `cat /Users/imac/.oh-my-graph/runs/20260803-084651.244624000-1/config.out` — `[abs-path]`, node `20260803-084651.244624000-1/workflow`

That `git log` and `git status` appear as "denied verbs" while `git push`
appears as a clean one is the whole shape of the finding: the denials track
quoting and paths, not the verb.

### (c) The contrast case does not exist — COULD NOT DETERMINE

§6 prints exactly one row. The only isolated program held under narrow grants
only is `gh`:

| program | verb | allowed | denied | named by a narrow grant? |
| --- | --- | --- | --- | --- |
| `gh` | `pr` | 17 | 3 | **yes** |

All three denials are the same shape — `gh pr create --dry-run --base main
--head fix/204-build-meta --title 'probe G/H/I' --body '## one …'` at node
`20260819-163447.441137000-2/pr` — and the program labels each `[multiline]`.
That is a verb the grant *did* name, denied for a reason that is not the verb.

**No node in this corpus ever attempted a gh verb its narrow grant did not
name.** The row that would decide (c) — printed as `NO  <- out of grant` — has
no instances. So this corpus cannot say whether narrowing a grant binds the
verb, and this document does not claim it does. That is the finding, not a gap
to be filled by reasoning.

### The control that weakens every ALLOW in this document

§4b counts isolated-node calls whose program appears in **no** grant the node
held: **25 denied, 24 ALLOWED**. The allowed ones are `git` (7), `ls` (7), `pwd`
(4), `echo` (2), `find`, `printf`, `true`, `which` (1 each) — programs no grant
named, which ran anyway. The matcher is therefore not a pure rule lookup, and
throughout this document **an ALLOW is weaker evidence than a DENIAL**. The (b)
verdict rests on 13 verbs across 197 calls rather than on any single allow for
that reason.

### The grant the question is actually about was never in force

§4c: the four `oh-my-graph` grants declared in `plugin/` are declarations only —

> node sessions in this corpus that HELD such a grant: **0** (isolated: 0)

so every statement here about `Bash(oh-my-graph *)` versus
`Bash(oh-my-graph run *)` is `inference` transferred from `git`, `go`, `make`
and `gh`, and is labelled as such wherever it is used below.

## 3. The three declarations

Read from the working tree at HEAD `be13995e8e36362779653b024865e32000eb1353`
with `git status --short` empty; `git diff --stat 8658ebd be13995 -- plugin/`
prints nothing, so these bytes are also those of the previous commit. Verbatim,
in the exact characters — the disagreement, if any, is in the strings.

`plugin/commands/graph.md:4`

```
allowed-tools: Bash(oh-my-graph run *), Bash(oh-my-graph auto *)
```

`plugin/skills/run-graph/SKILL.md:5`

```
allowed-tools: Bash(oh-my-graph run *)
```

`plugin/agents/oh-my-graph.md:4` — note the field is `tools`, not
`allowed-tools`:

```
tools: Bash(oh-my-graph *), Bash(git *), Bash(gh *), Read, Edit, Write, Grep, Glob, Skill, Agent
```

| entry point | field | grant string | address |
| --- | --- | --- | --- |
| `/graph` | `allowed-tools` | `Bash(oh-my-graph run *), Bash(oh-my-graph auto *)` | `plugin/commands/graph.md:4` |
| `run-graph` skill | `allowed-tools` | `Bash(oh-my-graph run *)` | `plugin/skills/run-graph/SKILL.md:5` |
| agent | `tools` | `Bash(oh-my-graph *), Bash(git *), Bash(gh *), Read, Edit, Write, Grep, Glob, Skill, Agent` | `plugin/agents/oh-my-graph.md:4` |

Three declarations, three different things: the field name differs (twice
`allowed-tools`, once `tools`); the skill lacks the `auto` grant the command
has; only the agent uses the subcommand-less form. §4c of the report finds the
same four strings by parsing the frontmatter with `yaml.v3` rather than by line
match.

`plugin/.claude-plugin/plugin.json` carries no permission field at all — the
whole file is `name`, `description`, `version` (`0.11.0`,
`plugin/.claude-plugin/plugin.json:4`) and `author`.

Two more strings in the same files constrain what can be invoked, and they are
not grants. `plugin/commands/graph.md:7`:

```
Run `oh-my-graph $ARGUMENTS` via Bash. The two subcommands are:
```

`plugin/skills/run-graph/SKILL.md:25`:

```
2. Run via Bash: `oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]`
```

The command passes the user's whole argument string through; the skill's
template has `run` baked in and no slot for a global flag. Section 4 turns on
that difference.

## 4. The codex claim at HEAD

The claim: *`/graph` and the skill cannot reach `--runtime codex`, while the
agent can.* It **holds in direction at HEAD**, with two corrections — one to the
word "cannot", one to the framing that treats the two blocked surfaces as the
same case.

**`/graph` — blocked by the GRANT (`inference`) and, separately, by the PROMPT.**
`oh-my-graph --runtime codex run …` begins with neither prefix at
`plugin/commands/graph.md:4`, and `plugin/README.md:36-39` states the
consequence: it "raises a per-use permission prompt instead of running
unprompted". That a prefix mismatch produces a prompt is not measured here —
section 2 could not determine that a narrow grant binds anything, so this rests
on the README's claim and is `inference`. What is *not* in doubt is the second
blocker, which is prose: `plugin/commands/graph.md:20-24` tells the model the
grants do not cover it, that a Codex run "has to be started from a shell", and
"do not try to work around the grants". **The template is not a blocker at all**
— `graph.md:7` passes `$ARGUMENTS` through verbatim, so a user typing
`/graph --runtime codex run g.yaml` produces a correct command line.

**The skill — blocked by the GRANT and by the PROMPT TEMPLATE, independently.**
Its grant is strictly narrower than the command's: `Bash(oh-my-graph run *)`
alone (`SKILL.md:5`), not even `auto`. And `SKILL.md:25` hard-codes the `run`
verb with four optional flags enumerated, none of them `--runtime`, and no
pass-through slot. These are two separate defects: widening the grant leaves the
template still writing `oh-my-graph run …`, and fixing the template leaves the
grant still not matching. **A prescription that repairs `/graph` by touching only
the grant does not repair the skill.** The skill states the situation about
itself at `SKILL.md:14-19`.

**The agent — not blocked, twice over.** `Bash(oh-my-graph *)`
(`plugin/agents/oh-my-graph.md:4`) covers the flag by pattern
(`plugin/README.md:45-48`), and section 2's (b) finding says a wide grant of that
shape did not bind the verb in any comparable case (`inference` for
`oh-my-graph`, measured for `git`/`go`/`make`). Independently of the pattern,
`plugin/README.md:193-199` records a smoke test in which the `tools` field "does
not actually restrict which shell commands run — the agent could run an
out-of-list `echo`". Its prose points *toward* the second runtime rather than
away: `plugin/agents/oh-my-graph.md:16-24` describes what `--runtime codex`
changes and says to read `docs/EXAMPLES.md` first. The CLI-surface block at
`plugin/agents/oh-my-graph.md:28-44` lists 11 invocations and `--runtime` appears
on none of them — an omission in the enumeration, not a blocker.

**Correction to "cannot".** On the README's own account the effect of a prefix
mismatch is a per-use prompt, and `plugin/README.md:37-39` says what happens next
— "up to your own session permission rules, which is where a standing `Bash(*)`
would still match". The accurate sentence is *cannot reach it **unprompted***.

## 5. What the decision item should now be

A wide prefix grant did not bind the verb where this corpus could test it. Carry
that to `plugin/agents/oh-my-graph.md:4` (`inference`, per §4c: no `oh-my-graph`
grant was ever in force) and the consequence is plain:

**An entry point that granted `Bash(oh-my-graph *)` to be helpful granted
strictly more than it meant to — every subcommand, `serve` and `resume` and
`chat` included — and "the three entry points disagree" is the wrong framing.
The three entry points do not disagree about a policy. The grant string does not
express what anyone thought it expressed.**

So the decision item stops being *reconcile the three surfaces on `--runtime
codex`* and becomes:

1. **Establish what a grant string binds, by experiment rather than by
   argument.** The corpus cannot answer it (§6). One probe answers it: a node
   granted only `Bash(oh-my-graph run *)` attempting `oh-my-graph serve
   --no-open`, and a second granted `Bash(oh-my-graph *)` attempting the same,
   under `--setting-sources ""` so the user's `Bash(*)` cannot decide it. Two
   runs, two denials-or-allows, and the item is settled.
2. **Then, and only then, decide whether the agent's wide grant should be
   narrowed** — the direction of travel is narrowing, not widening.
3. **Fix the documentation asymmetry regardless of (1).** `plugin/README.md:27`
   asserts that for `/graph` "nothing outside those command prefixes is
   granted". This measurement does not disprove that sentence; it establishes
   that nothing in the corpus supports it either, while
   `plugin/README.md:193-199` records the opposite outcome for the agent's
   field. One of the two claims is load-bearing for an operator deciding which
   entry point to install.

**Recommend that nothing be widened.** The `/graph` and skill grants are already
documented as deliberately narrow (`plugin/README.md:53-62`: "deliberately not
widened, and that is a design note rather than a limitation to fix"), and the
finding here cuts the other way — the surface that needs looking at is the one
that is too wide, not the two that are too narrow. Codex runs from a shell cost
nothing but a shell.

## 6. Limits

- **Nothing here measures `oh-my-graph` grants.** 0 node sessions in the corpus
  held one (§4c); every claim about `Bash(oh-my-graph *)` is `inference` from
  `git`, `go`, `make` and `gh`.
- **(c) is undetermined, not negative.** The absence of a `NO <- out of grant`
  row in §6 means the experiment was never run, not that narrowing fails.
- **An ALLOW is weak evidence.** 24 calls ran under no grant naming their
  program (§4b), so the matcher is not a pure rule lookup and any single allow
  proves little.
- **A denial carries no reason code.** The text is byte-identical for "no rule
  matched", an explicit deny, and a sandbox refusal (established by
  [0213b](0213b-compound-commands-defeat-grants.md)); the `[abs-path]`,
  `[redirect]`, `[ansi-c]` and `[multiline]` labels in §5 and §6 are the
  program's reading of the command string, not the CLI's own account —
  `inference`.
- **Compound commands are excluded by construction.** 23494 calls contain `|`,
  `;` or `&&` and were skipped (§2); what a grant does across a pipe is measured
  in [0213b](0213b-compound-commands-defeat-grants.md), not here.
- **The CLI version that produced the corpus is unknown** — the report says so
  in its own preamble, above §1: no run record carries it, so if grant matching
  changed across versions the corpus is two populations — 미측정.
- **The corpus is live and self-measured.** These figures are a snapshot; a
  re-run on a later day is expected to differ, and this lane's own 4 transcripts
  are excluded by name (§1) so the measurement does not move under its own feet.
- **Whether a prefix mismatch produces a prompt rather than a hard refusal is
  quoted, not reproduced** — `plugin/README.md:36-39`, and the
  `flag provided but not defined: -runtime` exit at `plugin/README.md:40-41`,
  were both cited from the README rather than executed here — 미측정.
- **The strength of a prompt instruction is not measurable from files.** Whether
  `plugin/commands/graph.md:24` actually stops a model attempting a Codex run is
  `inference`; nothing in `plugin/` claims it is enforced.
- **Denial is not defect, and allow is not correctness.** This document counts
  matcher decisions; it says nothing about whether any node's work was right.
