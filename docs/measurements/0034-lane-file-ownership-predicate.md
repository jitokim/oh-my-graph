# Measurement — the "two lanes must not touch the same file" predicate

Date: 2026-08-22. Branch: `formalise-batch-lane`.

## The question

`graphs/backlog-batch.yaml`'s header states seven rules a batch of parallel
lanes follows. Rule 1 is *"lanes must not touch the same file"*. The batch-lane
survey memo triaged it and proposed one mechanical candidate (its section B(ii))
for turning that sentence into a check. **This document measures that candidate
before anything ships.** It decides nothing about the other six rules.

The bar is the one this repo already set: a new lint needs a measured noise rate
before it ships. `docs/measurements/0213-tool-grant-predicate.md` rejected a
candidate at 110 noise in 114 hits; `handoff.LintToolGrants` shipped at 1 noise
in 62. An unmeasured noise rate does not ship here.

## The predicate, stated mechanically

Fixed by the survey memo before the probe existed, and not revised after seeing
results. Source: `docs/measurements/0034-lane-file-ownership-predicate.go:50-70`.

For each **resolved** graph (fragment `use:` spliced, `with:` bindings
substituted):

- **P1.** Group nodes by `Node.Worktree`. Every distinct non-empty value is one
  **lane**. A node with an empty `Worktree` belongs to no lane.
- **P2.** For each node, apply the regex
  `[A-Za-z0-9_./-]+\.(md|go|yaml|yml|json|txt)` to `Node.Prompt`; every match is
  a candidate path for that node's lane. (After `graph.LoadFile`, `with:`
  bindings are already substituted into the prompt, so no separate pass over
  `with:` is needed — `Node.With` is "resolved away by the file loader" and
  "empty on every validated graph", `internal/graph/graph.go:336-339`.)
- **P3.** For each unordered pair of **distinct** lanes, intersect the two
  candidate sets. Each path in a non-empty intersection is one **hit**.

Comparison is only ever *between* lanes. Sharing a file inside one lane is the
definition of a lane (dev writes it, e2e reads it), not a finding.

## The corpus and how it was assembled

Two populations, both loaded through the repo's own loader — never grep, and
never source lines. This repo has a written scar
(`docs/measurements/0213-tool-grant-predicate.go:15-18`): a `grep -c` count
reached three documents and was wrong because grep counted comments and counted
per file.

1. **Shipped graphs** — every `graphs/*.yaml` (not `graphs/fragments/*.yaml`,
   which are fragments and carry no `nodes:` top level), loaded with
   `graph.LoadFile`, so every `use:` is spliced before any node is counted.
2. **Operator corpus** — every run directory under `~/.oh-my-graph/runs`, walked
   with `os.ReadDir`. A run is taken as **planned** exactly when it wrote a
   `graph.json` (the planner's output); a hand-written `run` writes none. Each
   `graph.json` goes through `graph.Parse` — JSON is YAML, so the repo's own
   parser decodes *and validates* it, and a planned graph carries no `use:` left
   to splice.

The directory existed on this machine; nothing here is a stand-in.

| | count | address |
|---|---:|---|
| shipped graphs loaded | 8 | probe §A |
| run directories seen | 339 | probe §A |
| skipped: no `state.json` | 3 | probe §A |
| skipped: no `graph.json` (hand-written run) | 299 | probe §A |
| **planned graphs loaded** | **37** | probe §A |
| resolved nodes in the planned graphs | 175 | probe §A |
| **POPULATION** | **45 graphs (8 shipped + 37 planned), 216 resolved nodes** | probe §A |

Outside the population, and reported so the edge of the measurement is visible
rather than guessed at: the 299 hand-written runs came from **185 distinct
`.yaml` paths** (probe §A), most of them `/tmp` scratch files that no longer
exist. They were not loaded.

## The command that reproduces every number

```
go run docs/measurements/0034-lane-file-ownership-predicate.go
```

Run from the repository root. It reads `$OMG_HOME/runs`, or
`~/.oh-my-graph/runs` when `OMG_HOME` is unset — the same rule the engine uses
to place a run directory. It writes nothing. "probe §A", "§B", "§C", "§D" below
name that command's four printed sections.

## How many graphs can fire at all

A single-lane graph can produce no hit by construction, so this is the real
denominator behind the hit count.

| | count | address |
|---|---:|---|
| graphs declaring at least one lane | **1 of 45** | probe §B |
| graphs declaring two or more distinct lanes | **1 of 45** | probe §B |
| resolved nodes belonging to some lane | **8 of 216** | probe §B |

The one multi-lane graph is `graphs/backlog-batch.yaml`, lanes `lane-a` and
`lane-b` (probe §B). **Not one of the 37 planned graphs declares a lane** — the
predicate's firing population came entirely from the 8 shipped graphs. That is
not an accident of this corpus: `validatePlannedNodeWorktree` rejects any
planned node carrying a `worktree:`, so an unreviewed plan cannot create
checkouts or branches (`internal/coordinator/coordinator.go:1398-1405`, its
reasoning at `:1388-1397`). The 37 planned graphs enlarge the
corpus and can never enlarge the firing population.

## The hits, with the hand-check

One hit (probe §C). Hand-checked by opening the graph and reading both nodes.

| # | graph | lane / node | lane / node | shared path | verdict |
|---|---|---|---|---|---|
| 1 | `graphs/backlog-batch.yaml` | `lane-a` / `lane-a/dev` | `lane-b` / `dev-b` | `CONTRIBUTING.md` | **NOISE** |

**Hit 1 hand-check.** The token comes from one sentence, present once in each
lane, saying the same thing:

- `lane-a/dev` — spliced from `graphs/fragments/gated-lane.yaml:63-64`: "End
  every commit message with the trailer `Co-Authored-By: …` (the attribution
  convention in CONTRIBUTING.md)."
- `dev-b` — `graphs/backlog-batch.yaml:147-148`: the identical sentence.

Neither lane edits `CONTRIBUTING.md`; both **cite** it, as the address of a
commit-trailer convention. The files each lane owns are elsewhere entirely — in
`{{ inputs.task_a }}` and `{{ inputs.task_b }}` (`graphs/backlog-batch.yaml:106`,
`:141`).

Noise sub-class: **① a path every lane quotes by convention** — one of the four
sub-classes named in the survey memo *before* the measurement, alongside
② a directory rather than a file, ③ a path in fragment prose rather than lane
prose, ④ a run-varying input placeholder. The lexical rule cannot distinguish a
name that is *quoted* from a file that is *touched*, which is precisely the
failure `docs/measurements/0213-tool-grant-predicate.md` rejected a predicate
for.

## Noise rate

> **1 noise in 1 hit.**

Precision 0/1. For contrast, from the same measurements directory:
`handoff.LintToolGrants` shipped at 1 noise in 62 hits
(`docs/measurements/0213-tool-grant-predicate.md`,
`internal/handoff/tool_grant_lint.go:23-30`); the #213 candidate was rejected at
110 noise in 114.

**Say plainly how small this is.** One hit is not a noise rate in any
statistical sense — it is a single example. The predicate's firing population is
**one graph** (probe §B), so no reasonable-looking number could have come out of
this corpus in either direction. That smallness is itself the finding, and it is
structural rather than a sampling accident: planned graphs cannot declare
`worktree:` at all, so no amount of additional operator history would grow the
firing population by even one graph.

## The load-bearing reason, independent of the number

Even at 0 noise the predicate would be judging text it cannot see. Which files a
lane owns lives in `{{ inputs.task_a }}` / `{{ inputs.task_b }}`
(`graphs/backlog-batch.yaml:106`, `:141`), supplied at run time by `--input`
(`:75-79`). `lint` takes exactly one positional argument and rejects both a
second argument and any dash-prefixed one
(`cmd/oh-my-graph/lint.go:19-30`) — it has no `--input` and never will without a
CLI change. A lint built on this predicate would run with the ownership text
absent, scoring only the incidental paths that survived into prose.

## Verdict

Any narrowing that would clear this hit — exempting `CONTRIBUTING.md`, or
"repo-root convention documents", or requiring a write verb near the token —
would be fitted to the single hit in the corpus and re-measured against the same
single graph, i.e. it would measure the tuning. A narrowing that cannot be
re-measured is not a narrowing. `SHIP-AS-TEST-OVER-SHIPPED-GRAPHS-ONLY` fails
for the same reason from the other side: the test would be **red today** on
`graphs/backlog-batch.yaml`, and the only way to green it is that same untestable
exemption.

**VERDICT: DO-NOT-SHIP**

Justification, by the number: 1 noise in 1 hit (precision 0/1), over a firing
population of 1 of 45 graphs (probe §B, §C). Rule 1 stays prose. The survey
memo's separate residual fragment — "in a graph where any node declares
`worktree:`, a node with an empty `worktree` runs in the user's real tree and so
shares files with every lane" — reads only `Node.Worktree`, extracts no path,
and is not this predicate; it is out of scope for this document and is measured,
if at all, on its own.
