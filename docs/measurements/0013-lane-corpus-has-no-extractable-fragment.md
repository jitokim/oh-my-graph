# The operator lane corpus has no extractable fragment — NO EXTRACTION, measured

**Verdict: nothing was extracted.** The shape that repeats across the lane
corpus is *wiring* ADR 0013 refuses to let a fragment carry, and the one
shape whose *prose* repeats hard enough to qualify — the `apply` node —
declares **neither half** of this repo's verdict convention, has **zero**
shipped callers, and keys its first sentence off a review contract no
shipped fragment emits. Extracting it would have shipped a fragment whose
proof did not exist yet.

- **Date:** 2026-08-10 (KST), macOS (darwin 22.6.0), one machine.
- **Corpus:** every `~/.oh-my-graph/runs/*/state.json` on this machine
  (210 run directories), deduplicated by the full resolved graph JSON —
  so a lane re-run collapses to one row and two lanes differing by one
  prompt word are two rows. **75 distinct lane graphs** carry a node with
  id `pr`; those are the operator lanes this question is about.
- **Cost:** zero `claude` spawns. This is a corpus read, not a probe.
- **Re-derivable:** the script is in "Method", below. It reads only
  `state.json` files, which hold the **resolved** graph (ADR 0013
  §Versioning), so what it measures is what actually ran.

## What the question was

A prior pass observed that an operator lane scaffold — `review` → `apply`
→ `pr`, with a worktree — had been hand-written some thirty times, and
proposed extracting it into `graphs/fragments/`. ADR 0013 exists for
exactly that complaint (52 of 58 corpus graphs forking a half-dozen
shapes), so the proposal deserved the ADR's own standard rather than a
sympathetic reading.

## Finding 1 — what repeats is wiring, and a fragment may not carry it

The repeated part of the scaffold is `worktree:`, `cwd: {{ inputs.wt }}`,
the `depends_on` chain and the ids. Those five keys are precisely the list
ADR 0013 §Semantics and DESIGN.md make a **load error** in a fragment's
`node:` block. A fragment is portable because it says nothing about where
it sits; the scaffold is nothing *but* where things sit.

`use:` cannot smuggle them either. `prompt:` may not be wholesale
overridden ("the prompt *is* the fragment"), so a fragment's payload is
its prose — and a fragment carrying only `type` and `timeout: 45m` costs
the reader a second file to open and returns two lines.

## Finding 2 — the `apply` node's prose does repeat, and still fails

Among the 75 lanes, **35** carry a node with id `apply`. Their prompts
collapse to **9 distinct texts**; the top two account for 26 of 35 and
share **82%** of their words (Jaccard) — the divergence is one sentence
saying the same thing two ways. By word count that clears the bar ADR
0013's 2026-08-09 update used to extract `pr-publish` (83 shared words =
75% of the shortest prompt). So the prose case is real. It fails on four
other counts, any one of which is disqualifying:

| what a shipped fragment needs | what the 35 `apply` nodes declare |
|---|---:|
| `result_matches` (the verdict pattern) | **0 / 35** |
| a verdict-first prompt clause | **0 / 35** |
| `allowed_tools` (the grant the prompt was written against) | **0 / 35** |
| a shipped caller | **0** |

**a. It carries neither half of the verdict convention.** DESIGN.md
"Verdict patterns" makes the convention load-bearing in two halves — the
pattern and the clause — and a fragment carrying one without the other
creates the silent mismatch this repo keeps closing. These nodes carry
*neither*. Their entire `success_check` is
`{exit_zero: true, verify: {command: "make local", timeout: 10m}}`,
identical in all 35. To ship this as a fragment one would have to author
the pattern and the clause from scratch — which is designing a new node
and calling it an extraction. The corpus would be the excuse, not the
evidence.

**b. Its grant would have to be invented too.** All 35 declare no
`allowed_tools` at all. A fragment must ship the grant its prompt was
written against (ADR 0013 §Semantics), so the fragment author would pick
one that no lane ever proved. Constraint: a grant every caller overrides
is not a proven grant — and a grant *no* caller ever declared is not even
that.

**c. Its first sentence is coupled to a contract this repo does not
ship.** All 35 begin: *"If it said exactly NO FINDINGS, change nothing and
reply NO CHANGES."* That keys off the lanes' own hand-written `review`
node. The shipped review fragments — `review-security`, `review-style` —
emit `CLEAN` / `FINDINGS:`, never `NO FINDINGS`. A fragment placed in
`graphs/fragments/` beside them would instruct a stranger's apply node to
watch for a token its upstream fragment cannot produce: the exact silent
mismatch, shipped as a template, wrong on the day it lands.

**d. No shipped graph would cite it.** `self-dev`, `dev-review-pr` and
`backlog-batch` have no apply stage at all — their reviews land findings
in the PR body. The only shipped apply nodes are `adr-driven-dev`'s
`adr-apply` / `apply1` / `apply2`, all in **one file**, which is the
intra-file case ADR 0013 §Alternatives hands to YAML anchors, and which
that ADR's 2026-08-09 update already ruled on by name. The corpus `apply`
text overlaps `apply1` by ~17% — a different shape, not a caller.

## Finding 3 — the corpus opted out of the discipline it would be donating

This is the finding that decides it, and it was not in the proposal.
Across the 75 lanes:

- `apply` nodes: 35, with `result_matches` — **0**
- `review` nodes: 47, with `result_matches` — **0**
- `pr` nodes: 75, with `result_matches` — **0**
- nodes using `use:` at all — **0**

Every one of those 75 lanes ran its `pr` node with no assertion that a PR
exists. The shipped `pr-publish` fragment's entire reason for existing is
that the URL is the payload that turns the verdict into an assertion —
"without it the last node of the pipeline passed on a promise, which is
how merge-shepherd's merge node once ended a run green with nothing
merged."

So the extraction arrow points the other way. This corpus is not a supply
of proven shapes for `graphs/` to adopt; it is 75 graphs that never
adopted the four proven shapes `graphs/` already ships. **The
lane author's fix is `use: pr-publish`, not a new fragment.**

## Finding 4 — a caveat on ADR 0013's own metric, worth keeping

ADR 0013's 2026-08-09 update compares prompts by **longest common suffix**
(line-wrapping-insensitive). That metric was chosen because the shipped
nodes put their verdict clause last, so shared text collects at the tail.
It does not transfer to a corpus that carries no verdict clause: the
`apply` nodes' one divergent sentence sits mid-body, so their common
suffix reads 8 words while their word-level agreement is 82%. Suffix
length **understates** convergence whenever the divergence is not at the
end.

That does not change this verdict — Findings 2 and 3 are independent of
any similarity metric — but the next person who reuses the suffix number
on a non-shipped corpus should know it measures tail agreement, not
agreement.

## What would change the verdict

Concrete and falsifiable, so this does not have to be re-argued from
taste:

1. A shipped template grows an apply stage (findings applied rather than
   handed to the PR body), giving the shape a caller inside `graphs/`; and
2. that stage declares both halves of the verdict convention, so the
   fragment carries a proven verdict rather than an invented one; and
3. its grant is one an actual caller declared and did not override.

Until then the honest spelling of this shape is an inline node in the
operator's own lane directory, or — better — those lanes citing the four
fragments that already ship.

## Method

Run from `~/.oh-my-graph/runs`:

```python
import json, glob, collections
seen = {}
for p in sorted(glob.glob('*/state.json')):
    try:
        g = json.load(open(p)).get('graph')
    except Exception:
        continue
    if isinstance(g, str):
        g = json.loads(g)
    if isinstance(g, dict) and any(n.get('id') == 'pr' for n in g.get('nodes', [])):
        seen.setdefault(json.dumps(g, sort_keys=True), g)   # dedupe re-runs
lanes = list(seen.values())
ap = [n for g in lanes for n in g['nodes'] if n.get('id') == 'apply']
print(len(lanes), len(ap))
print(sum(1 for n in ap if (n.get('success_check') or {}).get('result_matches')))
print(sum(1 for n in ap if n.get('allowed_tools')))
print(collections.Counter(' '.join(n['prompt'].split()) for n in ap).most_common(2))
```

`state.json` stores the resolved graph, so no fragment resolution or
`{{ inputs }}` interpolation is re-done here — the numbers are what ran.
Node roles are identified by exact id (`apply`, `review`, `pr`), the
naming every lane in this corpus uses; a lane that named the role
differently is not counted, which can only *shrink* the numbers above and
never inflate them.
