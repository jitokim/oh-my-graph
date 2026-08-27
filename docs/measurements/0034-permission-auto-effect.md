# 0034 — did the default permission mode move the denial number?

**This is a SELF-MEASUREMENT on a live corpus:** every run counted below was
produced by this project's own lanes on one machine (macOS, darwin 22.6.0),
measured 2026-08-28 (KST) by running, from the repository root,
`go run docs/measurements/0218-denied-nodes-that-passed.go` — the same command
`0218:32` names, over the same script, now split by the mode each run declared.

The command was run three times. Two of the runs were captured to files and
compared with `diff /tmp/remeasure-run2.txt /tmp/remeasure-run3.txt`, which
printed nothing: byte-identical. The command also overwrites
`docs/measurements/0218-denied-nodes-raw.json`, the per-node row evidence
behind every count here (its last line reports 278 planned nodes).

## 1. The before numbers

From [`0218-denied-nodes-that-passed.md`](0218-denied-nodes-that-passed.md),
measured 2026-08-21 (KST) on this same machine (`0218:15`), over a denominator
of 73 planned nodes with a readable transcript (`0218:245`):

| the before number | value | address |
| --- | --- | --- |
| (a) planned nodes denied ≥1 tool call | 53 of 73 | `0218:249` |
| (b) of those, recorded verdict `PASS` | 49 of 53 | `0218:250` |
| (c) of those 49, `verify` — the engine ran a command | 5 | `0218:252` |
| (c) of those 49, `result_matches` — the node's own words | 8 | `0218:253` |
| (c) of those 49, exit-only — nothing but the exit status | 36 | `0218:254` |

## 2. The two sides, as measured 2026-08-28

Both columns come from the single command above. Its whole-corpus header reads
361 run directories seen, 302 skipped (3 with no `state.json`, 299 with no
`graph.json`, 0 unparseable), 1 excluded as in flight, leaving 58 planned runs
and 278 planned nodes.

| | pre-change — bucket `dontAsk` | post-change — bucket `auto` |
| --- | --- | --- |
| planned runs | 51 | 7 |
| planned nodes | 241 | 37 |
| **denominator** — planned nodes with a readable transcript | **216** | **36** |
| **(a)** planned nodes denied at least one tool call | **73** of 216 | **0** of 36 |
| **(b)** of those, recorded verdict `PASS` anyway | **67** of 73 | **0** of 0 |
| **(c)** of those, holding an engine-run `verify` | **6** of 67 | **0** of 0 |
| (c) rest — `result_matches`, the node's own words | 10 of 67 | 0 of 0 |
| (c) rest — exit-only, nothing but the exit status | 51 of 67 | 0 of 0 |

Every cell is a line of that command's output; the `auto` side's `(b)` and `(c)`
rows read `0 of 0` because `(a)` found no node to split.

**The `auto` column's zero is an artifact of the discriminator, not a finding.**
The script's `denialCore`
(`docs/measurements/0218-denied-nodes-that-passed.go:378`) is the `dontAsk`
refusal sentence and nothing else, so a bucket whose runs did not run `dontAsk`
reads near zero by construction — the script prints that warning above its own
split. What the CLI writes under this default instead is quoted at `0034:563-565`.
Whether those `auto` runs were in fact denied any call is **unmeasured**: no
predicate in this tree matches the classifier phrasing yet.

## 3. Which boundary, and why

The boundary is the **`state.json` field `default_permission_mode`**
(`internal/runstate/runstate.go:430`), not a date. ADR 0034 requires exactly
that — "the population must be selected structurally, not by date"
(`0034:570-574`) — because a run started before the change and resumed after it
still runs the old mode, so a date would misfile it.

**An absent or empty field is bucketed as `dontAsk`, because that is what those
runs ran under.** The field is `omitempty`
(`internal/runstate/runstate.go:430`), so a run from before the change carries
no key at all; the script's `bucketOf`
(`docs/measurements/0218-denied-nodes-that-passed.go:145-150`) maps absent and
empty onto `dontAsk`, the mode named at
`docs/measurements/0218-denied-nodes-that-passed.go:136-137`.

The field proved usable, and was cross-checked by hand with a JSON parser
(`python3`, `json.load` over each `state.json` under
`/Users/imac/.oh-my-graph/runs`) rather than by `grep`: across all 361 run
directories, exactly 8 carry a non-empty `default_permission_mode`, and all 8
carry the literal `"auto"`. No run in this corpus declares any other value, so
no `auto` run reached the absent-field fallback. Those 8 are the 7 `auto` runs
in the table plus the in-flight run excluded in §4. Independently re-deriving
the planned test, the in-flight test and the bucketing in Python reproduced the
script's counts with no disagreeing run id.

The 7 `auto` runs, by id:
`20260825-123013.565158000-1`, `20260825-144133.799111000-2`,
`20260826-045726.186334000-1`, `20260826-053627.281082000-1`,
`20260826-061035.046887000-1`, `20260826-070622.430625000-2`,
`20260827-130646.161231000-1`.

## 4. What was excluded, and never silently dropped

**Sessions with no transcript on disk — 3 in total, all on the `dontAsk` side:**

| bucket | count | node and session |
| --- | --- | --- |
| `auto` | 0 | — |
| `dontAsk` | 3 | `20260820-162555.890191000-1` / `corpus`, session `21f5446f-6b30-4b45-bcd0-05a290d7c9a2` |
| | | `20260820-162555.890191000-1` / `precedent`, session `a889a07f-fd17-4dd7-9513-516f6be5a53d` |
| | | `20260822-010107.356534000-1` / `file-findings`, session `a647b122-2e28-473e-b476-c667c069a3e2` |

All three are recorded `FAIL` and are in neither numerator nor denominator: a
node whose transcript cannot be read is not a node that was not denied
(`0218:320-322`).

The other reachability exclusions the same output reports, over the whole
corpus: 0 transcripts matched but unreadable, 5 nodes with no `session_id` on
the record, 18 nodes with no record in `state.nodes` because the run halted
before reaching them. 278 planned nodes minus those exclusions is the 252-node
whole-corpus denominator, which splits into the 216 and 36 above.

**In-flight run excluded, by id: `20260827-194830.890267000-1`** — bucket
`auto`, 6 graph nodes with 2 recorded and no record carrying `FAIL`, so the
script's in-flight test excludes it from every bucket. It is the run this
measurement itself executed in; its unfinished nodes' denials are not yet on
disk. Excluding it removes nodes from the `auto` denominator only.

## 5. Caveats

- **The post-change corpus is small and it is ours.** All 7 `auto` runs came
  from this project's own lanes, on one machine, in the days between the change
  and 2026-08-28. That is not a sample of users; it is this repo measuring
  itself, and nothing here generalises to how anyone else's nodes behave.
- **n is too small to support a rate.** 36 readable nodes across 7 runs — one of
  which, `20260827-130646.161231000-1`, has only 2 graph nodes — cannot carry a
  percentage, and setting a rate from that side beside the 216-node `dontAsk`
  side would show a contrast the evidence does not contain. No percentage
  appears in this document; every figure above is a raw count over a named
  denominator, which is also the only comparison `0218:349-365` permits.
- **The `auto` side's numerator is a floor of zero, by construction** (§2). It
  is not evidence that denials stopped.
- Unmeasured, and left so on purpose: whether `auto` was actually active in
  those 7 runs; whether the headless abort occurred anywhere in this corpus;
  the call-level split from `docs/measurements/0213b-compound-commands.go` over
  the `auto` population.

## 6. What this says about ADR 0034's falsification clause

[ADR 0034](../adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md)
§6, verbatim at `0034:580-581`:

> **If the denied-and-still-`PASS` count is 44 or higher, revert to `dontAsk`.**
> The baseline is **49 of 73** (`0218:250`).

The clause is scoped at `0034:576-578` to "the first **73 readable planned
nodes** in runs recorded `default_permission_mode: "auto"`".

**That number has not been measured, and this re-run did not measure it.** Two
things stand between this output and the clause:

1. **Precondition P1 is unmet.** `0034:561-568` requires the discriminator to
   match both phrasings and states the consequence if it does not: "**A re-run
   with the unmodified script reports approximately zero denials and that is an
   artifact, not a finding.**" The script is unmodified in exactly that respect
   (`docs/measurements/0218-denied-nodes-that-passed.go:378`), and it reported
   0 of 36. This is the outcome the ADR predicted for a re-run in this state.
2. **The population does not exist yet.** The clause asks for 73 readable
   planned `auto` nodes; the corpus holds 36 (§2). Whether the arrival rate of
   lane runs reaches 73 is unmeasured.

Precondition P2, by contrast, is met: the split above is on the structural field
(§3), not on a date.

**The distinction the ADR itself drew.** ADR 0034 claimed to move the denial
number and explicitly **not** the verification number. It says at `0034:113-114`
that `0218` "does not support this change, and must not be cited as though it
did", and at `0034:126-127` that "**The 44 is the cost of missing verification
and is ADR 0033's subject, not this one's.**" Its own positive case rests
elsewhere — "**84 of 246** denied calls" and the non-TTY verdict
(`0034:152-155`, `0034:169-171`). So column (c) is not this ADR's scoreboard,
and the `dontAsk` side's 6 of 67 engine-run `verify` is not a result about the
permission default. Only (a), and (b) beneath it, are what §6 watches — and the
one number §6 names, the denied-and-still-`PASS` count over 73 `auto` nodes,
remains unmeasured.

## 7. Verdict

**No — this measurement does not show the default change moving (a):** the
post-change side reads 0 of 36 under a discriminator that only matches the old
mode's sentence, which is an artifact ADR 0034 predicted rather than a movement
it can be judged by.
