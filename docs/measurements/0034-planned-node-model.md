# 0034 — which model actually answered, planned vs hand-written

**Question.** A planned node runs with `--setting-sources ""` (ceiling layer 1),
which withholds `~/.claude/settings.json` — the file holding the operator's
`model` key. Does that show up in what actually ran?

**Program.** `docs/measurements/0034-planned-node-model.go` (build tag `ignore`;
nothing in the engine calls it).

```sh
go run docs/measurements/0034-planned-node-model.go
```

Raw per-node rows: `docs/measurements/0034-planned-node-model-raw.json`
(1397 rows, one per node record, rewritten on every run).

**Corpus and the join.** Lifted unchanged from
`docs/measurements/0218-denied-nodes-that-passed.go` so the two share one
definition of "planned" rather than two that drift: a run is PLANNED exactly
when its snapshot's `graph_source_path` is the SAME FILE (device+inode,
`os.SameFile`, after `EvalSymlinks`) as that run's own `graph.json`. The model
is read by joining each node record's `session_id` to
`~/.claude/projects/*/<session_id>.jsonl` **by filename** — the project slug is
never reconstructed — and taking `message.model` off the assistant records.

Two corrections 0218 did not need:

1. 0218's `scan()` drops a run with **no** `graph.json` into "skipped", which is
   right for 0218 and would have discarded all 299 hand-written runs here.
   `handwritten` is therefore "readable `state.json` AND (no `graph.json` OR not
   the same file)", reported split (299 / 0).
2. The in-flight run is excluded, so a measurement cannot count itself.

## Result (2026-08-24)

348 run directories → 45 planned, 299 hand-written, 3 with no `state.json`,
1 planned run excluded as in flight (`20260824-010050.356583000-1`).

| bucket | `claude-opus-5` | other | joined denominator |
|---|---|---|---|
| **PLANNED** | **181** | **6** (all `claude-sonnet-5`) | 187 of 195 node records |
| **HAND-WRITTEN** | **851** | **267** (all `claude-fable-5`) | 1118 of 1202 node records |

Exclusions are reported, not swallowed: planned lost 5 records with no
`session_id` and 3 with no transcript file; hand-written lost 72, 9, and 3 with
no assistant record.

`claude-fable-5` occurs **267 times on hand-written nodes and zero times on a
planned node**, on the same machine in the same days. Hand-written nodes track
the settings file; planned nodes provably do not.

### The hand-written bucket is a join, not a directory census

`~/.claude/projects` holds a transcript for every ordinary interactive session
on this machine. The program runs that contaminated census too, side by side,
so the gap is visible instead of assumed away:

```
transcript files:                              2505
claimed by SOME graph node's state.json:       1262
claimed by NO graph node (ordinary sessions):  1156   <-- would-be contamination
model census over ALL transcripts (NOT a node census):
    claude-opus-5 2029 | claude-fable-5 313 | claude-haiku-4-5-20251001 62
    claude-sonnet-5 12 | claude-opus-4-8 2
```

`claude-haiku-4-5-20251001` and `claude-opus-4-8` appear ONLY in the
directory-wide census and on no graph node at all — that is exactly what a
contaminated measurement would have added. The 851/267 figures carry none of it.

### The 6 planned outliers are agent-mapped, every one

Joining each planned node record to its graph node's `agent:` field:

```
181  agent=(none)       -> claude-opus-5
  5  agent=test-coder   -> claude-sonnet-5
  1  agent=doc-writer   -> claude-sonnet-5
```

No exceptions in either direction, and both agent definitions declare their
model (`~/.claude/agents/test-coder.md:4` and `doc-writer.md:4`, each
`model: sonnet`). So the planned bucket is not "181 correct + 6 noise": it is
two populations, one taking the CLI default and one taking a model the operator
chose in a different file. That is what makes ADR 0034 suppress `--model` for an
agent-mapped node.

## What this corpus CANNOT tell you

`message.model` does not record the context-window variant. The operator's
settings hold `model = "opus[1m]"`; the session that produced this measurement
is reported by its own harness as `claude-opus-5[1m]` and records
`"claude-opus-5"` on every assistant record. So:

> Every number here is a **model-family census**. The `[1m]` half of the defect —
> a node answering on the 200k variant when the operator chose the 1M one — is
> **not measurable from this corpus**, and is neither confirmed nor refuted by
> the 181.

Which is why the claim ADR 0034 rests on is *"181 planned nodes ran a model
nobody selected, which happened to land in the same family"*, and not *"181
planned nodes ran the wrong model"*.

## Reproduction and independent check

Re-running the program on 2026-08-24 reproduced every node-bucket figure
above exactly. The only cell that moved was the contamination census
(2504 → 2505 transcripts, 1155 → 1156 unclaimed), because the session doing the
re-run wrote a transcript of its own — which is itself the reason the node
counts come from a `state.json` join and not from that directory.

The aggregates were also recomputed a second way, straight off the raw rows
rather than through the program's own counters:

```sh
python3 -c "
import json,collections
d=json.load(open('docs/measurements/0034-planned-node-model-raw.json'))
c=collections.Counter((r['planned'], r.get('model','(excluded)')) for r in d)
print(*sorted(c.items(), key=lambda x:-x[1]), sep='\n')"
```

Both methods agree: 181 / 6 planned, 851 / 267 hand-written.
