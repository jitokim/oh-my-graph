# The rule-5 predicate fires once on this machine, and that once is wrong — DOES NOT SHIP

**Provenance.** The reasoning below was written on 2026-08-22 against the tree at
`48e32d2` (branch `lane-b2b`, never merged), where `graphs/backlog-batch.yaml`'s
rule 5 stood at `:34-37` and its `on_fail:` at `:84`. Every number in this
document has since been **re-derived on 2026-09-08 against `fea9003`**, and every
address has been re-opened against that same tree and corrected where it drifted.
The verdict did not change; the corpus grew from 51 graphs to 85 and the single
hit is the same graph. Read this as a historical measurement re-confirmed, not as
one taken today for the first time.

**Verdict: this predicate does not ship.** Over 85 resolved graphs (8 shipped +
77 planned, 423 resolved nodes) the candidate — *"the resolved graph's
`depends_on` undirected closure has two or more weakly connected components and
the graph does not declare `on_fail: continue`"* — produces **1 hit, and a
hand-check of it finds 0 real and 1 noise**. The single hit is a graph whose
components are not independent lanes at all but a **five-stage pipeline missing
its `depends_on` edges**; the run died of exactly that
(`cannot resolve {{ artifacts.corpus }}: artifact not available`), and the
warning this predicate would have printed — *declare `on_fail: continue`* —
is the opposite of the repair that graph needed. Worse, the defect was already
caught, and correctly named, by a sweep this repo has shipped for some time:
`handoff.LintPlaceholders` prints six warnings on that same file today.

The statistical case is thin — one hit is one hit — so the reason to reject is
**structural**, and it is stated in section 6:

1. on the planned half of the corpus the second conjunct is **vacuous** — the
   planner's reply schema has no graph-level `on_fail` key at all
   (`internal/coordinator/coordinator.go:1725-1737`), so 0 of 77 planned graphs
   can ever declare it and the predicate degenerates to "has ≥2 components";
2. on the shipped half **precision cannot be estimated**: exactly one shipped
   graph has ≥2 components, and it already complies;
3. the one firing is **already covered by a better-aimed sweep**.

- **Date:** first taken 2026-08-22 (KST) at `48e32d2`; re-derived 2026-09-08 at
  `fea9003`. macOS (darwin 22.6.0), one machine. The newest run in the corpus is
  `20260908-002834.820835000-1`; 391 run directories were seen (345 at the first
  taking).
- **Corpus:** `graphs/*.yaml` (8, fragments resolved) + `~/.oh-my-graph/runs`
  (`OMG_HOME` unset) — 391 run directories, of which **77 are planned graphs**
  carrying **382 resolved nodes**.
- **Method:** [`0034b-independent-lane-failure-predicate.go`](0034b-independent-lane-failure-predicate.go),
  run as `go run docs/measurements/0034b-independent-lane-failure-predicate.go`.
  Never `grep` — the repo has a written scar for that
  (`0213-tool-grant-predicate.go:15-18`).
- **Cost:** zero `claude` spawns. 391 directory reads, 85 graph loads, and one
  `go run ./cmd/oh-my-graph lint` invocation for the hand-check.
- **The hand-check is a human reading, not a computation.** Its one verdict was
  made by reading that run's `graph.json` and `state.json` directly. Reproduce
  the hit mechanically; the REAL/NOISE column is a judgement and is argued in
  section 5.

## 1. What was measured, and why

`graphs/backlog-batch.yaml`'s header states seven rules a batch of parallel
lanes follows. Rule 5 is *"independent lanes fail independently"* — a batch of
independent lanes should declare `on_fail: continue`, so one lane's failure does
not halt the others' unrelated work (`graphs/backlog-batch.yaml:71-74`).

The batch-lane survey memo triaged all seven and named rule 5 **the strongest
structural candidate** of the set: unlike rules 2 and 4 it reads no prompt prose
at all, only `Node.DependsOn` and `Graph.OnFail`. It then declined to build the
lint and stated one condition for reopening the question — *reuse
`0034-lane-file-ownership-predicate.go`'s corpus assembly, measure this
predicate's hit/noise over it, and decide on that document*. **This is that
document.** It decides nothing about the other six rules.

The bar is the one this repo already set: a new lint needs a measured noise rate
before it ships. `docs/measurements/0213-tool-grant-predicate.md` rejected a
candidate at 110 noise in 114 hits; `handoff.LintToolGrants` shipped at 1 noise
in 62 (`internal/handoff/tool_grant_lint.go:23-30`). An unmeasured noise rate
does not ship here.

**On the number in the filename.** `0034-lane-file-ownership-predicate.md`
(landed in `e8f0c39`, PR #237) measured rule 1 of the same seven from the same
memo. This is the second predicate out of that one survey, so it takes the `b`
suffix the repo already uses for exactly that
(`0213` → `0213b-compound-commands-defeat-grants.md`). It does **not** depend on
0034 having merged: the corpus assembly was copied into this file, not imported.

## 2. The predicate, stated mechanically

Fixed by the survey memo before the probe existed, and not revised after seeing
results. Source: `docs/measurements/0034b-independent-lane-failure-predicate.go:53-84`.

For each **resolved** graph (fragment `use:` spliced, `with:` bindings
substituted):

- **P1.** Take every node as a vertex and every `depends_on` entry as an edge,
  read **undirected** — a lane is one connected thing whichever way its arrows
  point (`Node.DependsOn`, `internal/graph/graph.go:252`).
- **P2.** Compute the weakly connected components of that closure (union-find).
- **P3.** **HIT** when `len(components) >= 2` **and** the graph does not declare
  `on_fail: continue` (`Graph.OnFail`, `internal/graph/graph.go:373`, queried
  via `Graph.ContinuesOnFail`, `:467`).

One hit is one **graph**, not one node and not one component: the warning this
predicate would emit is a single line about the graph's failure policy. That is
why the hit table below has one row and not five.

**Zero bytes of prompt prose are read.** That is the whole reason this candidate
was worth measuring where #213's was not: #213 failed because it could not tell
a command from a noun (110 of its 114 noise hits), and this predicate never goes
near the text where that distinction lives.

**The one exemption:** a graph whose closure has exactly one component cannot
hit. Rule 5 says nothing about such a graph — it has no independent lanes to
fail independently — so the premise is absent rather than satisfied.

**A limit that is NOT an exemption and that no static checker can remove:**
`--continue-on-fail` on the command line ORs with the graph's own value
(`effectiveContinueOnFail`, `internal/schedule/scheduler.go:1664-1666`). A
load-time warning can therefore be false about the run that actually happens.
Stated here rather than three sections down, because it bounds every number
below.

## 3. The corpus and how it was assembled

The assembly is `0034-lane-file-ownership-predicate.go`'s, reused unchanged —
which is what the memo asked for, so that the two rule measurements are
comparable rather than each defining its own population. Every graph goes
through the repo's **own** loader: `graph.LoadFile` for a `.yaml`, so every
fragment `use:` is spliced and every `with:` binding substituted before anything
is counted, and `graph.Parse` for a planned `graph.json` (JSON is YAML, and a
planned graph carries no `use:` to splice).

Counting resolved nodes is not a detail. `graphs/backlog-batch.yaml` lists 5
nodes in its `nodes:` block and resolves to **8**; lane A is one `use:
gated-lane` line that becomes four nodes. A count taken off the source file
would read this graph as 2 lanes of the wrong size.

```
=== A. corpus ===
shipped graphs (graphs/*.yaml, loaded via graph.LoadFile — fragments resolved):
    graphs/adr-driven-dev.yaml       11 resolved nodes   on_fail="halt"
    graphs/apply-flags.yaml           2 resolved nodes   on_fail="halt"
    graphs/backlog-batch.yaml         8 resolved nodes   on_fail="continue"
    graphs/dev-review-pr.yaml         5 resolved nodes   on_fail="halt"
    graphs/haiku-smoke.yaml           2 resolved nodes   on_fail="halt"
    graphs/merge-shepherd.yaml        6 resolved nodes   on_fail="halt"
    graphs/review-loop.yaml           2 resolved nodes   on_fail="halt"
    graphs/self-dev.yaml              5 resolved nodes   on_fail="halt"

operator corpus: /Users/imac/.oh-my-graph/runs
  run directories seen:            391
  skipped:                         314
      no state.json                                            3
      no graph.json (a hand-written run writes none)           311
  PLANNED graphs loaded:           77
  resolved nodes in them:          382
  planned graphs declaring on_fail: continue: 0

POPULATION: 85 graphs (8 shipped + 77 planned), 423 resolved nodes
```

**A caveat about the planned/hand-written split, stated where the split is.**
314 of 391 directories were skipped, and **311 of those for having no
`graph.json` at all** — which is precisely what a hand-written `run` produces.
So on this corpus "is planned" and "has a graph.json" are the same predicate,
and the split did no discriminating work. Three more had no `state.json`; none
failed to parse. This is the same caveat 0213 recorded
(`0213-tool-grant-predicate.md` section 2), and it has not changed.

**`on_fail="halt"` on a planned graph means undeclared, not chosen.** `decode`
normalizes an absent field to `OnFailHalt` (`normalizeOnFail`,
`internal/graph/graph.go:456-461`), so the string printed above cannot
distinguish "the author asked to halt" from "nobody said anything". Section 6
turns on this.

## 4. How many graphs can fire at all (section B)

```
graphs whose depends_on closure has TWO OR MORE components: 2 of 85
component-count distribution over the whole population:
     1 component(s):  83 graphs
     2 component(s):   1 graphs
     5 component(s):   1 graphs
the multi-component graphs (the only ones the predicate can fire on):
    graphs/backlog-batch.yaml         2 components,  8 nodes, on_fail="continue" (shipped)
    20260820-162555.890191000-1       5 components,  6 nodes, on_fail="halt"     (planned)
```

**83 of 85 graphs are a single connected thing.** The multi-lane batch is a rare
shape, and the predicate is silent on 98% of the corpus by construction — which
is a point in its favour on volume and, as section 6 argues, is also why its
precision cannot be established. Doubling the corpus did not move this: it was
49 of 51 at the first taking, 83 of 85 now.

`graphs/backlog-batch.yaml` — the graph whose header states the rule — is the
**only** shipped graph with two components (lane A: `lane-a/dev → lane-a/e2e →
lane-a/review → lane-a/pr`, spliced from `gated-lane`; lane B: `dev-b → e2e-b →
review-b → pr-b`, `graphs/backlog-batch.yaml:242`, `:254`, `:272`). It declares
`on_fail: continue` (`graphs/backlog-batch.yaml:155`), so it does not hit. This
**independently reproduces** the figure the survey memo carried as UNMEASURED:
**0 shipped hits**.

## 5. The hits, hand-checked (section C)

One hit. It is checked in full; no subset selection was needed.

| # | graph | components | verdict | why, and the address it rests on |
| --- | --- | --- | --- | --- |
| 1 | `20260820-162555.890191000-1` (`measure-tool-grant-predicate`, planned, 6 nodes) | 5: `{check, writeup}`, `{corpus}`, `{handcheck}`, `{precedent}`, `{predicate}` | **NOISE** | The five components are not independent lanes. They are one sequential pipeline whose data dependencies were written as `{{ artifacts.<id> }}` while the matching `depends_on` edges were **omitted**: `predicate`'s prompt opens *"The corpus scan is already done. Its report … is here: `{{ artifacts.corpus \| inline }}`"* and declares `depends_on: null`; `handcheck` quotes `{{ artifacts.predicate }}`; `writeup` quotes all four. The run's own `state.json` records the consequence — `predicate`: `"cannot resolve {{ artifacts.corpus }}: artifact not available (its producing node has not completed)"`, and the same for `writeup`. The predicate's warning would read *"independent components, declare `on_fail: continue`"*; following it would have made the run keep going with the artifacts still missing. The correct advice is *add the four missing edges*, which is a different warning entirely — and one the engine **already prints**: `go run ./cmd/oh-my-graph lint <that graph.json>` emits six `LintPlaceholders` warnings (and one unrelated `success_check` warning), e.g. *node "predicate": prompt: `{{ artifacts.corpus \| inline }}` references node "corpus", which is not an ancestor of this node — its artifact may not exist when this node runs* (`internal/handoff/placeholder_lint.go:181`, the sweep at `:96`). |

**Hits: 1. Real: 0. Noise: 1. Noise rate: 1/1.**

## 6. Why the verdict is DOES NOT SHIP, on structure and not on the 1/1

One hit is a weak sample and this document will not pretend otherwise. The
rejection rests on three findings that do not depend on the sample size.

**(a) On planned graphs the second conjunct is vacuous.** 0 of 77 planned graphs
declare `on_fail: continue`, and that is not a coincidence about this machine:
the planner is asked for a JSON object in an exact shape, and that shape has
**no graph-level key at all** — only `name`, `version` and `nodes`
(`internal/coordinator/coordinator.go:1725-1737`). An auto run therefore
*cannot* satisfy the conjunct. Over the planned half of the corpus — the half
the survey memo identified as the only place this predicate was measurable at
all — the predicate reduces to *"does this graph have ≥2 components"*, and every
multi-component planned graph is a hit **by construction**. A lint that a whole
class of graphs can never turn off is not advice; it is a constant. The 2026-09-08
re-derivation is the strongest single piece of evidence in this document: the
planned corpus nearly doubled, 43 → 77, and the count of planned graphs able to
satisfy the conjunct stayed at **zero**.

**(b) On shipped graphs precision is not estimable.** Exactly one shipped graph
can fire, and it already complies. 8 shipped graphs, 1 firing population, 0
hits: there is no ratio here to be good or bad. This is the same "too small to
measure" that killed the rule-1 candidate in 0034 — with the difference the memo
predicted, namely that the planned corpus *was* large enough to measure. It was,
and (a) is what it showed.

**(c) The one real firing is better served by an existing sweep.** Not "also
covered" — *better* covered. `LintPlaceholders` names the actual defect (a
missing ancestor edge), names it per node, and names it four times where this
predicate would emit one graph-level line pointing the author the wrong way. A
second sweep whose only observed firing duplicates a first sweep's territory
while degrading its diagnosis subtracts value.

**What would change this verdict.** A corpus containing hand-written multi-lane
graphs that genuinely forgot `on_fail: continue`. This machine has none: the
only hand-written multi-lane graph in reach is the one that documents the rule.
Until such graphs exist to measure, the check belongs where the survey memo put
it — a test over `graphs/` inside this repo, where a false positive costs a CI
fix by the rule's own author instead of reaching a user who deliberately wanted
a fail-fast batch. That is where it now lives:
`TestIndependentLanesFailIndependently`
(`internal/graph/shipped_graphs_test.go:682`) landed on `main` in `e8f0c39`,
PR #237, and `graphs/backlog-batch.yaml:75-82` records it as rule 5's
disposition. This document is the evidence for the half that header states only
as a conclusion — that a *lint* was measured and rejected, and why.

## 7. How to recompute it

From the repository root:

```sh
go run docs/measurements/0034b-independent-lane-failure-predicate.go
```

It prints sections A–D to stdout. The `//go:build ignore` tag
(`0034b-independent-lane-failure-predicate.go:1`) keeps the file out of
`go build ./...`, `go vet ./...` and `go test ./...`; `go run` with an
**explicit file argument** does not apply build constraints, which is why the
tag is safe and no `/tmp` copy is needed.

The hand-check's two supporting commands:

```sh
go run ./cmd/oh-my-graph lint ~/.oh-my-graph/runs/20260820-162555.890191000-1/graph.json
python3 -c 'import json;print(json.load(open("...state.json"))["nodes"])'
```

**One departure from the letter of the brief, stated rather than buried.** The
brief asked for a standard-library program. This one imports
`github.com/jitokim/oh-my-graph/internal/graph` — because the predicate is
defined over the **resolved** node set, and resolving `use:` fragments by hand
would mean reimplementing the splicer inside a measurement, which is the exact
class of error the "parse, do not grep" scar warns about. 0034 made the same
call for the same reason
(`0034-lane-file-ownership-predicate.go:15-20`, `:47`, `e8f0c39` / PR #237).
Nothing else is imported, and the file ships no behaviour.
