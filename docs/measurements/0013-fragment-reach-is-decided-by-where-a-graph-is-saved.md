# Fragment reach is decided by where a graph is saved — 86 of 87 lanes cannot cite anything

**Verdict: the lane corpus's zero adoption of `use:` is not a preference, it
is a reachability fact.** ADR 0013 resolves a `use:` against the entry graph
file's own `fragments/` sibling and nowhere else. Measured against where the
lanes on this machine are actually stored, **86 of 87** distinct lanes sit in a
directory that has no `fragments/` beside it — 84 of them directly in `/tmp` —
so for those lanes `use: pr-publish` is not an unchosen option, it is a load
error. The single reachable lane is this repo's own shipped template.

- **Date:** 2026-08-11 (KST), macOS (darwin 22.6.0), one machine.
- **Corpus:** every `~/.oh-my-graph/runs/*/state.json` (223 run directories),
  deduplicated by the full resolved graph JSON — the same dedup rule as the
  2026-08-10 lane-corpus pass, so a re-run collapses to one row — kept to the
  lanes carrying a publishing node (`pr`, `pr-a`, …). **87 distinct lanes.**
- **Cost:** zero `claude` spawns. This is a corpus read plus a `stat`, not a
  probe.
- **Re-derivable:** the script is in "Method" below and every number quoted
  here is an `assert` in it, so it fails rather than reports if the corpus has
  moved.

## What the question was

The 2026-08-10 pass ([lane corpus has no extractable
fragment](0013-lane-corpus-has-no-extractable-fragment.md)) found `use:`
appearing **zero** times across the lane corpus and concluded: *"The fix for
those lanes is `use: pr-publish`, not a new fragment."* That prescription
assumes the lanes can execute it. This pass asked whether they can.

## Finding — the prescription does not reach the lanes it was written for

| | lanes |
|---|---:|
| distinct lanes with a publishing node | **87** |
| stored by absolute path | 86 |
| … with a `fragments/` sibling on disk | **0** |
| … with none | **86** |
| stored by relative path (this repo's checkout, `graphs/merge-shepherd.yaml`) | 1 — reachable |
| stored directly in `/tmp` | **84** |

Not one hand-written operator lane is stored where a `use:` could resolve. The
one reachable row is a shipped template run from the checkout, which is also
the only lane in the corpus that *does* cite fragments.

So the corpus reads as "a consumer that has not adopted the mechanism", and
the adoption number is real, but the cause is not authorial reluctance: a lane
written to `/tmp/w-thing.yaml` would fail to load the moment it said `use:`.
The advice to cite a fragment, given to that lane, is advice that does not run.

## What follows — and what deliberately does not

The boundary is not the defect. ADR 0013 §Trust makes `fragments/` the review
surface that a bare-name `use:` cannot escape, and it already cut a search
tier on its own merits; widening resolution to rescue these lanes would
reverse a decision this measurement gives no evidence against. What the
measurement shows is a **product gap around** the boundary, and it is closed
by moving graphs to fragments rather than fragments to graphs:

1. **Nothing in the product said so.** The rule was stated exactly (README,
   DESIGN.md, the error text), and its consequence — *"therefore a graph you
   save in `/tmp` can never cite a shipped shape"* — was stated nowhere, so an
   author's storage habit silently decided their access to reuse.
2. **`init` could not top a tree up.** The one supported way to get a
   `fragments/` directory is `oh-my-graph init <dir>`, and until the top-up
   landed (2026-08-12) a second `init` failed on the first file that already
   existed. A `go install`
   user who ran `init` between v0.4.1 and v0.5.2 therefore had **no command at
   all** that could hand them `fragments/pr-publish.yaml`, which shipped at
   v0.5.3.
3. **Zero code changes reach the lanes.** Verified by hand: `init` into a
   directory, author the lane at `<dir>/graphs/<lane>.yaml`, and `use:`
   resolves; a `fragments/` symlink beside an existing graph resolves too
   (resolution reads the path, and the symlink target is at the same trust
   level as the graph file that names it). The fix is a storage convention,
   documented — not a resolution change.

## Method

Zero spawns; run it against any machine's run corpus.

```python
#!/usr/bin/env python3
import glob, json, os, re, collections

runs = sorted(glob.glob(os.path.expanduser('~/.oh-my-graph/runs/*/state.json')))
lanes = {}
for f in runs:
    try:
        d = json.load(open(f))
    except Exception:
        continue
    g = d.get('graph')
    if not isinstance(g, dict):
        continue
    if not any(re.match(r'^pr(-|$)', n.get('id', '')) for n in (g.get('nodes') or [])):
        continue
    lanes.setdefault(json.dumps(g, sort_keys=True), []).append(d.get('graph_source_path', ''))

dirs = collections.Counter()
reachable, unreachable, relative = 0, 0, 0
for paths in lanes.values():
    p = paths[0]
    if not os.path.isabs(p):
        relative += 1          # a repo-checkout invocation: `graphs/<x>.yaml`
        continue
    d = os.path.dirname(p)
    dirs[d] += 1
    if os.path.isdir(os.path.join(d, 'fragments')):
        reachable += 1
    else:
        unreachable += 1

assert len(lanes) == 87, len(lanes)
assert relative == 1, relative
assert reachable == 0, reachable
assert unreachable == 86, unreachable
assert dirs['/tmp'] == 84, dirs['/tmp']
```

Two honesty notes on what this can and cannot see. The `stat` is of **today's**
filesystem, so a `fragments/` directory that existed when a lane ran and was
deleted since would read as unreachable — but nothing in the corpus writes a
`fragments/` into `/tmp`, and `init` is the only thing that writes one at all,
so the reading is safe. And `state.json` stores the **resolved** graph
(ADR 0013 §Versioning), which is why reach is measured from
`graph_source_path` rather than by looking for `use:` in the snapshot: by the
time a graph is snapshotted, a `use:` has been resolved away.
