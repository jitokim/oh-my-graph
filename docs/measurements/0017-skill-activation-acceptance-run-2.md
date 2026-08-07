# ADR 0017 acceptance test, run 2 — pre-registered, verdict FAIL

Run 1 is `0017-skill-activation-acceptance.md`, same directory: same corpus,
different goal, 4 activated nodes, **1** `Skill` invocation. This is the second
attempt, run under a verdict rule fixed in writing before anything spent, and it
is the one ADR 0017's Status rests on.

- **Date:** 2026-08-07 (KST), `claude` **2.1.223**, macOS (darwin 22.6.0).
- **Binary:** `/private/tmp/sk/bin/oh-my-graph`, built from this worktree at
  `35a0f1e` via `make build`.
- **Corpus:** `~/.claude/skills`, 35 skills.
- **Scratch root:** `/tmp/skproof` (`OMG_HOME=/tmp/skproof/home`,
  cwd `/tmp/skproof/work`). **Deleted after the run** — see "Where the evidence
  survives" below, which is what makes the counts re-derivable without it.
- **Verdict:** **FAIL**, fixed before the repeat arm and not upgraded by it.

## Pre-registration (verbatim, written before any run)

Preserved at
`~/.claude/projects/-Users-imac-IdeaProjects-oh-my-graph/PREREG-skillactivation-20260807.md`.

**Goal, identical in both arms:**

> Write a short design note at ARCHITECTURE.md choosing between a single-process
> scheduler and a worker-pool for a small local job runner, then produce
> design.html — a standalone HTML version of that note.

**Expectation:** at least one planned node emits a `tool_use` record named
`Skill`. Specifically — the node that writes the HTML file → `html-artifact`
(primary); the node that writes the design note → `architecture-design`
(secondary). Predictions bound to the planner's real node ids the moment the
plan printed, misses reported verbatim.

**Evidence accepted, and nothing else:**

1. a raw `{"type":"tool_use","name":"Skill",…}` object in a node's JSONL
   transcript under `~/.claude/projects/`, with its `skill` input shown. **A
   model sentence claiming skill use is not evidence and is ignored even if
   present.**
2. `--plugin-dir <staged>` **and** `--setting-sources ""` both present in the
   real argv of a treatment spawn, captured by a PATH shim named `claude` that
   logs argv and `exec`s `/usr/local/bin/claude`. The staged dir must exist and
   hold the corpus.
3. zero of (1) in the `--no-skill-activation` control arm.

**Verdict rule:** PASS iff (1) ∧ (2) ∧ (3). Anything else FAIL. *"A treatment
arm that produces the HTML file but no `Skill` tool_use record is a FAIL, not a
partial pass."*

**Addendum, written after arm 1 + control + positive control and before the
repeat:** arm 1 produced zero, so the verdict is FAIL and now fixed; the repeat
runs for one additional fact — whether a planned node under a real planner
prompt ever activates, or never does — and cannot upgrade the verdict.

## Node binding, at plan-print time

| arm | plan | node | activation | bound prediction |
|---|---|---|---|---|
| treatment 1 | 3 nodes | `write-note` | **excluded — agent-mapped (`doc-writer`)** | `architecture-design` — **unbindable** |
| treatment 1 | | `make-html` | activated | `html-artifact` |
| treatment 1 | | `check` | activated | none |
| repeat | 2+ nodes | `write-note` | **excluded — agent-mapped** | `architecture-design` — **unbindable** |
| repeat | | `render-html` | activated | `html-artifact` |

The printout's own words for the exclusion: `excluded: write-note is
agent-mapped`. The secondary prediction was not testable in either plan.

## (2) argv — PASS

Recorded by the shim, treatment arm 1:

```text
SPAWN make-html: … --permission-mode dontAsk --setting-sources '' \
  --plugin-dir /tmp/skproof/home/runs/20260807-000630.375864000-1/skills-plugin \
  --allowedTools Read,Write --tools Read,Write,Skill --strict-mcp-config …
SPAWN check:     … --setting-sources '' --plugin-dir <same> \
  --allowedTools Read,Glob,Grep --tools Read,Glob,Grep,Skill --strict-mcp-config …
```

Repeat arm: `--plugin-dir` present, `--tools Read,Grep,Write,Edit,Skill`.
The staged directory existed and held all 35 skills. Layer 1 stayed `''`.

## (1) treatment `Skill` tool_use — **0, twice**

Every `tool_use` name in each node's transcript, by parsing the raw JSONL:

| arm | node | session | tool_use counts |
|---|---|---|---|
| treatment 1 | `make-html` | `fbc50f0f…` | `{'Read': 2, 'Write': 1}` |
| treatment 1 | `check` | `6eda4400…` | `{'Glob': 1, 'Read': 2, 'Grep': 4}` |
| treatment 1 | `write-note` (excluded) | `8adec3ba…` | `{'Glob': 3, 'Grep': 1, 'Write': 1}` |
| repeat | `render-html` | `025228fd…` | `{'Read': 5, 'Grep': 10, 'Write': 1}` |
| repeat | `write-note` (excluded) | `a057032f…` | `{'Glob': 2, 'Grep': 1, 'Write': 1}` |

`ARCHITECTURE.md` and `design.html` were produced and every node returned PASS.
**No `Skill` record in any of them.** `make-html` and `render-html` had one job
— produce an HTML file — with `html-artifact` staged, reachable, and described
for that task; both wrote the file directly with `Write`.

## (3) negative control — PASS

`--no-skill-activation`, same goal, separate `OMG_HOME` and working tree:

- no skill-scan or activation lines in the printout at all,
- no `--plugin-dir` in any control argv, no `Skill` in any `--tools`,
- raw `grep` for `Skill` across every control transcript: **0**.

Independently re-derived here: the four control sessions parse to
`{}`, `{'Bash': 2, 'Read': 2}`, `{'Read': 2, 'Write': 1, 'Edit': 3}`,
`{'Write': 1}` — no `Skill`. **The two arms are genuinely different.**

## Positive control — the mechanism is live

Same policy argv, against the treatment run's *live* staged directory, with a
prompt that names the skill:

```json
{"type":"tool_use","id":"toolu_01SXJpyhf7hme5uGtNvT3WFD","name":"Skill",
 "input":{"skill":"oh-my-graph-staged-skills:html-artifact"},"caller":{"type":"direct"}}
```

Run 1's P1/P2 probes agree, P2 under `propose`'s exact
tools/allowed/disallowed lists. **Delivery works; activation is what did not
reproduce.**

**What this control does not establish**, and ADR 0017 now treats as the open
question: a prompt naming the skill proves the tool exists and the staged
definition loads. It does **not** prove the model ever saw the 35 descriptions.
That is ADR 0017's measurement (i).

## Aggregate across both acceptance runs

| run | activated planned nodes | `Skill` invocations |
|---|---|---|
| run 1 (`…-acceptance.md`) | 4 | 1 (`artifact` → `html-artifact`) |
| run 2, treatment arm 1 | 2 | 0 |
| run 2, repeat | 1 | 0 |
| **total** | **7** | **1** |

Different goals, so this is a count of observed activated nodes, **not a rate to
extrapolate**. It is reported as an aggregate because reporting only the run
that produced a one is the failure mode ADR 0017 was written against.

## Cost

| item | USD |
|---|---|
| treatment arm 1 | 0.99 |
| control arm | 0.96 |
| repeat | 1.09 |
| positive control probe | 0.17 |
| **total** | **3.21** |

## Where the evidence survives

`/tmp/skproof` was deleted. The transcripts were deliberately kept, and every
count in this file was re-derived from them after the fact:

```text
~/.claude/projects/-private-tmp-skproof-work/        treatment arm 1 (4 sessions)
~/.claude/projects/-private-tmp-skproof-work2/       repeat            (3 sessions)
~/.claude/projects/-private-tmp-skproof-work-ctl/    negative control  (4 sessions)
~/.claude/projects/-private-tmp-skproof-work-probe/  positive control  (1 session)
```

These live outside the repository and outlive it only as long as the machine
keeps them; the tables above are the durable record, which is the point of this
directory existing.
