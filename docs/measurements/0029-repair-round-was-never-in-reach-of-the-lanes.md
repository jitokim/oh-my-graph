# `repair-round` was never in reach of the lanes — 0 of 28 is a missing file, not a rejected fragment

**Verdict: the lane corpus's zero adoption of the `repair-round` fragment is
not a judgment about the fragment. The file is not in the directory those
lanes resolve against.** A `use:` resolves against the ENTRY graph file's own
`fragments/` sibling and nowhere else (`internal/graph/fragment.go:1017`, the
rule stated at `fragment.go:24-26`). The lanes' `fragments/` directory holds
four files, and `repair-round.yaml` is not one of them — so `use: repair-round`
in any of those 28 lanes is a load error today (`fragment.go:1035`), not an
unchosen option.

The same 28 lanes cite fragments **28 times**, in 17 of 28 files, using every
one of the four names that *are* in that directory. They are not a corpus that
declines to cite; they are a corpus that cited everything it could reach.

- **Date:** 2026-08-19 (KST), macOS (darwin 22.6.0), one machine.
- **Corpus:** `~/IdeaProjects/oh-my-graph-hq/lanes/graphs/*.yaml` (28 files)
  and this repo's `graphs/*.yaml` (8 files), parsed with PyYAML — not `grep`.
- **Cost:** zero `claude` spawns. Two directory reads and a parse.
- **Re-derivable:** the script under "Method" asserts every number quoted
  here, so it fails rather than reports if the corpus has moved.

This is the second time the same mechanism has produced a zero that was read
as a preference. See [fragment reach is decided by where a graph is
saved](0013-fragment-reach-is-decided-by-where-a-graph-is-saved.md), which
found 86 of 87 lanes stored where no `fragments/` sits beside them at all.
That pass answered "can these lanes cite anything?"; this one answers "can
they cite *this*?"

## Finding 1 — the fragment is absent from the directory the lanes resolve against

| in `~/IdeaProjects/oh-my-graph-hq/lanes/graphs/fragments/` | |
|---|---|
| `e2e-verify.yaml`, `pr-publish.yaml`, `review-security.yaml`, `review-style.yaml` | present |
| **`repair-round.yaml`** | **absent** |

In this repo's `graphs/fragments/` all five are present, and `oh-my-graph init`
unpacks the whole embedded set at once (`graphs/embed.go`, `//go:embed *.yaml
fragments/*.yaml`). The lanes' directory is hand-curated, and it is incomplete.

`repair-round.yaml`'s own header says of the lane authors: *"authors who were
not unaware of fragments, but for whom the two nodes in front of the one they
cited were not citable. **Now they are.**"* For the lanes measured here, that
last sentence is false — the file the sentence is about was never put where
they look.

## Finding 2 — those lanes cite fragments avidly, and only the reachable ones

| | |
|---|---:|
| lane files | 28 |
| lanes containing at least one `use:` | **17** |
| `use:` citations across the corpus | **28** |
| … `pr-publish` | 18 |
| … `e2e-verify` | 4 |
| … `review-style` | 4 |
| … `review-security` | 2 |
| … `repair-round` | **0** |

Every cited name is a file that exists in that directory. The count of citations
of names that are *not* in that directory is zero — which is what a corpus of
load-error-free graphs must look like.

This also falsifies the "one-shot graphs cite nothing" confound as an
explanation of the zero: these one-shot graphs cite 28 times.

## Finding 3 — the grant mismatch is real, and it is a *prediction*, not the explanation

Operational definition, so the number is re-derivable rather than eyeballed: a
**review → apply pair** is a node with `permission_mode: plan` (a review that
cannot edit) that a node holding `Edit`/`Write` depends on (its apply).

| | across the corpus | what `repair-round` does |
|---|---|---|
| lanes with ≥1 such pair | 17 / 28 | |
| pairs | **22** | |
| halves of those pairs that are `use:` citations | **0 / 44** | |
| review `agent: code-reviewer-deep` | 20 / 22 | substitution point — fits |
| review `timeout:` | `45m` ×12, `30m` ×9, absent ×1 | substitution point — fits |
| apply `timeout:` | `45m` ×18, `1h` ×2, `30m` ×1, absent ×1 | fixed `45m` — fits 18 |
| apply `success_check.verify` | present 20 / 22 | substitution point — fits |
| **review `allowed_tools`** | **9 distinct grants** | **fixed `[Read, Grep, Glob, Bash(git *)]` — matches 4 of 22** |
| **apply `allowed_tools`** | 17 / 22 grant `Bash(go *)`; 6 distinct grants | **fixed, and grants no `Bash(go *)`** |

Every knob these pairs vary is a substitution point except the one they vary
most. **But no lane ever met that mismatch**, because no lane could load the
fragment in the first place (finding 1). This table says what would happen if
the file were put in reach; it does not explain the zero, and nothing measured
here does except the file's absence.

### Discrepancy with the numbers ADR 0029 shipped with

The first version of ADR 0029 quoted "15 / 28 lanes", "0 / 30 halves",
"9 distinct grants … matches 1 of 15" and "13 of 15 applies grant `Bash(go *)`"
from an unrecorded pass with no stated pair definition. Under the definition
above the counts are 17 lanes / 22 pairs / 44 halves / **4** of 22 / **17** of
22. Only the two figures that do not depend on the pair definition survive
unchanged: **9 distinct review grants**, and **0** halves that are citations.
The other numbers in this document supersede the ADR's, because these have a
definition and a script attached and those did not.

## The experiment this leaves open

Whether the grant mismatch is what *would* stop those lanes is untested and
cheap to test: copy `graphs/fragments/repair-round.yaml` into the lanes'
`fragments/` directory, then measure citation over the next lanes written. Until
that runs, "the grants are the blocker" is a hypothesis with a mechanism, not a
measurement — and "nesting is the blocker" is equally untested, since a lane
that cannot load the fragment cannot demonstrate a need for anything deeper.

## Method

```python
#!/usr/bin/env python3
"""Re-derives every number in this document. Asserts, so it fails if stale."""
import yaml, glob, os, collections

LANES = os.path.expanduser("~/IdeaProjects/oh-my-graph-hq/lanes/graphs")
REPO = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

# Finding 1 — what is in the two fragments/ directories.
lane_frags = sorted(os.path.basename(p) for p in glob.glob(LANES + "/fragments/*.yaml"))
repo_frags = sorted(os.path.basename(p) for p in glob.glob(REPO + "/graphs/fragments/*.yaml"))
assert lane_frags == ["e2e-verify.yaml", "pr-publish.yaml",
                      "review-security.yaml", "review-style.yaml"], lane_frags
assert "repair-round.yaml" in repo_frags and "repair-round.yaml" not in lane_frags

# Finding 2 — citation counts.
lanes = sorted(glob.glob(LANES + "/*.yaml"))
assert len(lanes) == 28, len(lanes)
uses, citing = collections.Counter(), 0
for f in lanes:
    nodes = (yaml.safe_load(open(f)) or {}).get("nodes") or []
    cited = [n["use"] for n in nodes if "use" in n]
    citing += bool(cited)
    uses.update(cited)
assert citing == 17, citing
assert sum(uses.values()) == 28, uses
assert uses == {"pr-publish": 18, "e2e-verify": 4, "review-style": 4,
                "review-security": 2}, uses
assert uses["repair-round"] == 0

# Finding 3 — review -> apply pairs, by the definition stated above.
WRITE = {"Edit", "Write", "MultiEdit", "NotebookEdit"}
pairs, lanes_with_pair = [], set()
for f in lanes:
    nodes = (yaml.safe_load(open(f)) or {}).get("nodes") or []
    for review in nodes:
        if review.get("permission_mode") != "plan":
            continue
        for apply_ in nodes:
            if review.get("id") in (apply_.get("depends_on") or []) and \
               any(t in WRITE for t in (apply_.get("allowed_tools") or [])):
                pairs.append((review, apply_))
                lanes_with_pair.add(f)
                break
assert len(lanes_with_pair) == 17 and len(pairs) == 22, (len(lanes_with_pair), len(pairs))
assert sum(("use" in r) + ("use" in a) for r, a in pairs) == 0

grant = lambda n: tuple(sorted(n.get("allowed_tools") or []))
assert len({grant(r) for r, _ in pairs}) == 9
assert len({grant(a) for _, a in pairs}) == 6
FIXED = {"Read", "Grep", "Glob", "Bash(git *)"}          # repair-round's review
assert sum(1 for r, _ in pairs if set(grant(r)) == FIXED) == 4
assert sum(1 for _, a in pairs if "Bash(go *)" in grant(a)) == 17
assert sum(1 for r, _ in pairs if r.get("agent") == "code-reviewer-deep") == 20
assert sum(1 for _, a in pairs if (a.get("success_check") or {}).get("verify")) == 20
assert collections.Counter(r.get("timeout") for r, _ in pairs) == \
       collections.Counter({"45m": 12, "30m": 9, None: 1})
assert collections.Counter(a.get("timeout") for _, a in pairs) == \
       collections.Counter({"45m": 18, "1h": 2, "30m": 1, None: 1})

# The fragment's own fixed keys, for the right-hand column.
rr = yaml.safe_load(open(REPO + "/graphs/fragments/repair-round.yaml"))
by_id = {n["id"]: n for n in rr["nodes"]}
assert set(by_id["review"]["allowed_tools"]) == FIXED
assert "Bash(go *)" not in by_id["apply"]["allowed_tools"]
assert by_id["apply"]["timeout"] == "45m"
print("all assertions hold")
```

The block is the whole method — save it beside this file and run it, or paste
it. `REPO` is derived from this document's own path, so it holds wherever the
checkout lives; `LANES` is this machine's lane directory and is the one thing a
reader on another machine must repoint.
