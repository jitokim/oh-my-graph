# The operator lane corpus has no extractable fragment — NO EXTRACTION, measured

**Verdict: nothing was extracted.** The shape that repeats across the lane
corpus is *wiring* ADR 0013 refuses to let a fragment carry, and the one
shape whose *prose* repeats hard enough to qualify — the `apply` node —
declares **neither half** of this repo's verdict convention, has **zero**
shipped callers, and keys its second sentence off a review contract no
shipped fragment emits. Extracting it would have shipped a fragment whose
proof did not exist yet.

- **Date:** 2026-08-10 (KST), macOS (darwin 22.6.0), one machine.
- **Corpus:** every `~/.oh-my-graph/runs/*/state.json` on this machine
  (210 run directories), deduplicated by the full resolved graph JSON —
  so a lane re-run collapses to one row and two lanes differing by one
  prompt word are two rows. **75 distinct lane graphs** carry a node with
  id `pr`; those are the operator lanes this question is about. **33** of
  them carry all three of `review` / `apply` / `pr` — 40 have no `apply`
  node and 28 no `review` node, and they run 2–9 nodes each. So "the
  scaffold" below means the shape 33 lanes carry in full, not all 75.
- **Cost:** zero `claude` spawns. This is a corpus read, not a probe.
- **Re-derivable:** the script is in "Method", below, and every number
  quoted here is an `assert` in it, so it fails rather than reports if the
  corpus has moved. Its corpus input is `state.json`, which holds the
  **resolved** graph (ADR 0013 §Versioning), so what it measures is what
  actually ran; the two comparisons against shipped shapes read
  `graphs/adr-driven-dev.yaml` and `graphs/fragments/e2e-verify.yaml`.

## What the question was

A prior pass observed that an operator lane scaffold — `review` → `apply`
→ `pr`, with a worktree — had been hand-written some thirty times, and
proposed extracting it into `graphs/fragments/`. ADR 0013 exists for
exactly that complaint (52 of 58 corpus graphs forking a half-dozen
shapes), so the proposal deserved the ADR's own standard rather than a
sympathetic reading.

## Finding 1 — what repeats is wiring, and a fragment may not carry it

The repeated part of the scaffold is `worktree:`, `cwd: {{ inputs.wt }}`,
the `depends_on` chain and the ids — four of the five keys
(`id`, `depends_on`, `cwd`, `worktree`, `feedback`;
`internal/graph/fragment.go`, `fragmentWiringFields`) that ADR 0013
§Semantics and DESIGN.md make a **load error** in a fragment's
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
saying the same thing two ways. That is agreement of the order ADR 0013's
2026-08-09 update extracted `pr-publish` on, but it is **not the same
yardstick**: that bar was a longest-common-**suffix** count (83 shared
words = 75% of the shortest prompt), and the two metrics do not convert
(Finding 4). So the prose case is real on its own metric. It fails on four
other counts, any one of which is disqualifying:

| what a shipped fragment needs | what the 35 `apply` nodes declare |
|---|---:|
| `result_matches` (the verdict pattern) | **0 / 35** |
| a verdict-first prompt clause (`handoff.demandsVerdict`) | **0 / 35** |
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

**c. Its second sentence is coupled to a contract this repo does not
ship.** All 35 carry, as their **second** sentence: *"If it said exactly
NO FINDINGS, change nothing and reply NO CHANGES."* (The first sentence is
`The deep review said: {{ artifacts.review | inline }}` in 32 of 35, with
three one-off rewordings — so the contract is universal, its position is
second, not first.) That keys off the lanes' own hand-written `review`
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
text overlaps `apply1` by **13%** (Jaccard, the same measure as the 82%
above — 54 words against 161) — a different shape, not a caller.

## Finding 3 — the corpus opted out of the discipline it would be donating

This is the finding that decides it, and it was not in the proposal.
Across the 75 lanes:

- `apply` nodes: 35, with `result_matches` — **0**
- `review` nodes: 47, with `result_matches` — **0**
- `pr` nodes: 75, with `result_matches` — **0**
- nodes using `use:` at all — **0**

The corpus is not *innocent* of the convention, which is the sharper
version of this finding. Of the 349 nodes in those 75 lanes, **32 do**
declare `result_matches` — all outside the three scaffold roles (`e2e` 24,
`measure` 4, `verify` 3, `accept` 1), and every one of them a hand-typed
`PASS` verdict patterned after the shipped `e2e-verify`. **All 32 drifted
from the pattern that fragment ships** (``^[*_`\s]*PASS[*_`\s]*$``):

| what the copy says | nodes | how it fails |
|---|---:|---|
| `PASS` | 19 | unanchored — `result_matches` is a **search** (`regexp.MatchString`, `internal/schedule/scheduler.go`), so *"the suite did not PASS"* passes |
| ``^[*_`\s]*PASS`` | 7 | no tail anchor — passes on `PASS is what we did not get` |
| `^PASS$` | 6 | emphasis-intolerant — a false FAIL on `**PASS**` |

And 16 nodes *named* after shipped fragments (`review-security` 8,
`review-style` 8) carry no `result_matches` at all: the fragment's name
copied without the pattern that makes the name mean anything.

That is the whole argument in one measurement. The shapes were not
unknown to these lanes — they were retyped, and retyping lost the anchors.
`use:` is the mechanism that would have made the loss impossible, and it
appears zero times.

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

Run from `~/.oh-my-graph/runs`, with this repo checked out at `REPO`. Every
number quoted above is an `assert` here, so the script fails loudly rather
than printing a plausible table if the corpus has moved under it. It reads
three inputs: the corpus `state.json` files, `graphs/adr-driven-dev.yaml`
(the 13% comparison and the shipped-caller count) and
`graphs/fragments/e2e-verify.yaml` (the drift baseline). `PyYAML` is needed
only for those last two.

```python
import collections, glob, json, os, yaml

REPO = os.path.expanduser('~/IdeaProjects/oh-my-graph')
norm = lambda s: ' '.join(s.split())
def jaccard(a, b):                       # the measure behind 82% and 13%
    A, B = set(a.split()), set(b.split())
    return len(A & B) / len(A | B)

seen, unreadable = {}, []
for p in sorted(glob.glob('*/state.json')):
    try:
        with open(p, encoding='utf-8') as f:
            g = json.load(f).get('graph')
        if isinstance(g, str):
            g = json.loads(g)            # same diagnostic path as the load above
    except (OSError, ValueError) as exc:
        unreadable.append(f'{p}: {exc}')
        continue
    if isinstance(g, dict) and any(n.get('id') == 'pr' for n in g.get('nodes', [])):
        seen.setdefault(json.dumps(g, sort_keys=True), g)   # dedupe re-runs
# A skipped input silently shrinks every count below, so report before totals.
assert not unreadable, 'unreadable corpus inputs:\n' + '\n'.join(unreadable)

lanes = list(seen.values())
nodes = [n for g in lanes for n in g['nodes']]
rm = lambda n: (n.get('success_check') or {}).get('result_matches')
role = lambda r: [n for n in nodes if n.get('id') == r]
apply_, review, pr = role('apply'), role('review'), role('pr')
ids = [set(n['id'] for n in g['nodes']) for g in lanes]
sizes = [len(g['nodes']) for g in lanes]

# The corpus (header bullet): 75 lanes, 349 nodes, 33 carrying all three roles.
assert (len(lanes), len(nodes)) == (75, 349)
assert sum(1 for s in ids if {'review', 'apply', 'pr'} <= s) == 33
assert sum(1 for s in ids if 'apply' not in s) == 40
assert sum(1 for s in ids if 'review' not in s) == 28
assert (min(sizes), max(sizes)) == (2, 9)

# Finding 2 — the apply prose repeats: 9 texts, top two 26 of 35, 82% agreement.
texts = collections.Counter(norm(n['prompt']) for n in apply_)
top = texts.most_common(2)
assert (len(apply_), len(texts)) == (35, 9)
assert top[0][1] + top[1][1] == 26
assert round(jaccard(top[0][0], top[1][0]), 2) == 0.82

# Finding 2, the four-row table — every cell is 0 of 35. `DEMANDS` is the
# verdictDemands list from internal/handoff/verdict_lint.go, which is what
# `handoff.demandsVerdict` matches on.
DEMANDS = ('start your reply with', 'start the reply with', 'begin your reply with',
           'your reply must start', 'your reply is exactly',
           'first characters of the reply', 'reply with exactly')
assert sum(1 for n in apply_ if rm(n)) == 0
assert sum(1 for n in apply_ if n.get('allowed_tools')) == 0
assert sum(1 for n in apply_ if any(d in n['prompt'].lower() for d in DEMANDS)) == 0
# (a) one success_check, identical in all 35; (c) the contract is universal
# and sits second, behind a first sentence 32 of the 35 share.
assert len({json.dumps(n.get('success_check'), sort_keys=True) for n in apply_}) == 1
assert apply_[0]['success_check'] == {
    'exit_zero': True, 'verify': {'command': 'make local', 'timeout': '10m'}}
assert sum(1 for n in apply_ if 'NO FINDINGS' in n['prompt']) == 35
assert sum(1 for n in apply_ if norm(n['prompt']).startswith('The deep review said:')) == 32

# Finding 3 — the roles opt out, and the 32 that do declare a pattern drifted.
assert (len(review), len(pr)) == (47, 75)
assert sum(1 for n in review + pr if rm(n)) == 0
assert sum(1 for n in nodes if n.get('use')) == 0
assert sum(1 for n in pr if (n.get('success_check') or {}).get('verify')) == 0
declared = [n for n in nodes if rm(n)]
assert len(declared) == 32
assert collections.Counter(n['id'] for n in declared) == {
    'e2e': 24, 'measure': 4, 'verify': 3, 'accept': 1}
patterns = collections.Counter(rm(n) for n in declared)
assert patterns == {'PASS': 19, r'^[*_`\s]*PASS': 7, '^PASS$': 6}
named = [n for n in nodes if n['id'] in ('review-security', 'review-style')]
assert (len(named), sum(1 for n in named if rm(n))) == (16, 0)

# The two shipped-file comparisons.
shipped = lambda rel: yaml.safe_load(open(os.path.join(REPO, rel)))
e2e = shipped('graphs/fragments/e2e-verify.yaml')['node']['success_check']
assert e2e['result_matches'] == r'^[*_`\s]*PASS[*_`\s]*$'
assert all(p != e2e['result_matches'] for p in patterns)   # 32 of 32 drifted

adr = shipped('graphs/adr-driven-dev.yaml')['nodes']
apply1 = norm([n for n in adr if n['id'] == 'apply1'][0]['prompt'])
assert round(jaccard(top[0][0], apply1), 2) == 0.13    # a different shape
assert (len(top[0][0].split()), len(apply1.split())) == (54, 161)

# Finding 2d — the shipped-caller count: one file holds every apply stage
# `graphs/` ships, which is the intra-file case ADR 0013 hands to anchors.
stages = {os.path.basename(f): [n['id'] for n in yaml.safe_load(open(f))['nodes']
                                if 'apply' in n['id']]
          for f in glob.glob(os.path.join(REPO, 'graphs', '*.yaml'))}
assert {f: s for f, s in stages.items() if s} == {
    'adr-driven-dev.yaml': ['adr-apply', 'apply1', 'apply2']}
print('all assertions hold')
```

`state.json` stores the resolved graph, so no fragment resolution or
`{{ inputs }}` interpolation is re-done here — the numbers are what ran.
Node roles are identified by exact id (`apply`, `review`, `pr`), the
naming every lane in this corpus uses; a lane that named the role
differently is not counted, which can only *shrink* the numbers above and
never inflate them.
