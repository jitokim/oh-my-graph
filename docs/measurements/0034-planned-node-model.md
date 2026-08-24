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

**So: is the hand-written bucket contaminated? No — by construction.** It is
reached only through `state.json` node records, so a session no graph node ever
claimed cannot enter it. The contamination that a directory-wide census *would*
have introduced is measured rather than asserted, and its size is **1156
transcripts** claimed by no graph node (the `orphan` counter printed at
`docs/measurements/0034-planned-node-model.go:475`).

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

### Not measured — the explicit list

Each line below is something this corpus does **not** answer. None of these is a
weak or borderline result; they were never in the corpus to begin with.

- The `[1m]` context-window variant. `message.model` records `claude-opus-5` for
  a session its own harness names `claude-opus-5[1m]`, so opus and opus[1m] are
  indistinguishable in every figure above. <!-- 미측정 -->
- Which model a **codex** node ran. A codex node writes no
  `~/.claude/projects/*.jsonl`, so the join finds nothing: 12 records excluded
  under `note = "session_id present but no ... matched"` (3 planned, 9
  hand-written) — counted from the raw rows by the note aggregation in
  "Reproduction" below. <!-- 미측정 -->
- The 77 node records carrying **no `session_id`** (5 planned, 72 hand-written)
  and the 3 whose transcript held no assistant record naming a model. They are
  excluded from the denominators, not folded into the majority. <!-- 미측정 -->
- Whether the operator's `~/.claude/settings.json` held the **same `model` value
  across the whole corpus window** — earliest row `20260730-204118`, latest row
  `20260822-230615.538102000-1` (the run-id aggregation in "Reproduction"). The
  census reads transcripts only; the settings file keeps no history and none was
  reconstructed. <!-- 미측정 -->
- The **hand-written bucket split by `agent:`**. The agent join was run for the
  6 planned outliers only; `nodeRow` carries no agent field, so 851/267 is not
  decomposed into agent-mapped and plain nodes. <!-- 미측정 -->
- Whether ADR 0034's `--model` **fixes** this. Every run in the corpus predates
  the change; this measures the defect, never the repair. <!-- 미측정 -->

## Reproduction and independent check

Re-running the program earlier on 2026-08-24 reproduced every node-bucket figure
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

The exclusion notes and the corpus window come off the same rows:

```sh
python3 -c "
import json,collections
d=json.load(open('docs/measurements/0034-planned-node-model-raw.json'))
print(collections.Counter(r['note'] for r in d if r.get('note')))
ids=sorted({r['run_id'] for r in d}); print(ids[0], ids[-1])"
```

77 no-`session_id`, 12 no-transcript, 3 no-assistant-record; window
`20260730-204118` → `20260822-230615.538102000-1`.

### Later re-run, 2026-08-24 ~11:00 — the corpus has grown

Every figure in "Result" above is a snapshot of the corpus as it stood when
`0034-planned-node-model-raw.json` was written, and that file is their address.
The runs directory keeps growing, so a later re-run does not reproduce them. Run
from a scratch directory, because the program writes its raw output to
`docs/measurements/...` **relative to the working directory** and would otherwise
overwrite the committed file:

```sh
mkdir -p /tmp/w-model-rerun-0034
cd /tmp/w-model-rerun-0034 && go run /private/tmp/w-model/docs/measurements/0034-planned-node-model.go
```

That command exits cleanly and reports:

| cell | committed raw JSON | later re-run |
|---|---|---|
| run directories | 348 | 349 |
| PLANNED runs | 45 | 46 |
| HAND-WRITTEN runs | 299 | 299 |
| in-flight excluded | `20260824-010050.356583000-1` | `20260824-015702.576813000-2` |
| PLANNED node records / joined | 195 / 187 | 200 / 192 |
| **PLANNED `claude-opus-5`** | **181** | **186** |
| PLANNED `claude-sonnet-5` | 6 | 6 |
| HAND-WRITTEN records / joined | 1202 / 1118 | 1202 / 1118 |
| **HAND-WRITTEN opus / fable** | **851 / 267** | **851 / 267** |
| transcripts / unclaimed | 2505 / 1156 | 2511 / 1157 |
| raw rows written | 1397 | 1402 |

The whole delta is +5 planned node records, all `claude-opus-5`, from planned
runs this branch's own work started after the raw file was written. The
hand-written bucket does not move at all. The direction of the finding is
unchanged, and the numbers this document commits to are the committed-raw-JSON
column — the one with a durable address.
