# 0034 — did the default permission mode move the denial number?

**This is a SELF-MEASUREMENT on a live corpus.** Every run counted below was
produced by this project's own lanes, on one machine (macOS, darwin 22.6.0),
under the maintainer's own settings. Measured **2026-08-28 (KST)**.

**Reproduction — the whole table comes from one command, run from the
repository root:**

```sh
go run docs/measurements/0218-denied-nodes-that-passed.go
```

That is the command `0218:32` names, over the same script, now (i) split by the
`default_permission_mode` each run declared and (ii) carrying a discriminator
that matches the auto-mode classifier's denial sentence as well as the
`dontAsk` one (§2.1). The script was run twice and the two stdout captures were
compared with `diff`, which printed nothing. It also rewrites
`docs/measurements/0218-denied-nodes-raw.json`, the per-node row evidence behind
every count here — 284 rows, one per planned node, denied or not.

The script and the evidence file are at commit `d640025`; the `file:line`
citations into them below resolve there.

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

Those were measured under one mode, before the default moved. They are the
prior reading, not the `dontAsk` column of §2: the corpus has grown since, and
§2's `dontAsk` column is a fresh reading of a larger population.

## 2. The two sides, as measured 2026-08-28

The command's whole-corpus header reads: 362 run directories seen; 302 skipped
(3 with no `state.json`, 299 with no `graph.json`, 0 that would not parse); 0
readable-with-`graph.json` runs failing the planned test; 1 excluded as in
flight (§4); leaving 59 planned runs and 284 planned nodes. No hand-written run
enters the corpus, because a hand-written run writes no `graph.json`.

| | pre-change — bucket `dontAsk` | post-change — bucket `auto` |
| --- | --- | --- |
| planned runs | 51 | 8 |
| planned nodes | 241 | 43 |
| **denominator** — planned nodes with a readable transcript | **216** | **42** |
| **(a)** planned nodes denied at least one tool call | **73** of 216 | **0** of 42 |
| **(b)** of those, recorded verdict `PASS` anyway | **67** of 73 | **0** of 0 |
| **(c)** of those, holding an engine-run `verify` | **6** of 67 | **0** of 0 |
| (c) rest — `result_matches`, the node's own words | 10 of 67 | 0 of 0 |
| (c) rest — exit-only, nothing but the exit status | 51 of 67 | 0 of 0 |

Every cell is a line of that command's output. The `auto` side's `(b)` and `(c)`
rows read `0 of 0` because `(a)` found no node for them to split.

**The `auto` side's zero now survives a predicate that can see auto denials.**
The previous pass of this document reported `(a) 0 of 36` under a discriminator
that knew only the `dontAsk` sentence, and that zero was an artifact — the
absence of a string those runs never emit. The predicate has since been extended
(§2.1), and the re-run over 42 readable nodes in 8 `auto` runs still reads 0.
That is a real negative. What it is not is a rate, or a comparison: see §5.

Independently cross-checked. The same six cells were re-derived from
`docs/measurements/0218-denied-nodes-raw.json` by a separate Python pass —
grouping the 284 rows on `permission_bucket`, excluding in order `ran == false`,
empty `session_id`, `transcript == "missing"` and `unreadable == true`, then
counting `denied`, `verdict == "PASS"` and `success_check_kind` — and every
number agreed with the Go program's, including the exclusion counts in §4.

### 2.1 What changed in the predicate, and the address behind each accepted sentence

`isDontAskDenial` became `isDenial`
(`docs/measurements/0218-denied-nodes-that-passed.go:413`) and gained a second
prefix-anchored arm. The `dontAsk` arm is unchanged. Each accepted string was
read off a real transcript rather than off a comment naming it:

| accepted prefix | constant | transcript address |
| --- | --- | --- |
| `"Permission to use "` … `" has been denied because Claude Code is running in don't ask mode."` | `denialHead` / `denialCore`, `…-that-passed.go:381-382` | `~/.claude/projects/-private-tmp-auto-b1/f17f7543-7f89-497e-b894-a868b45e7c3f.jsonl` line 9 — record carries `toolDenialKind: "permission-rule"` |
| `"Permission for this action was denied by the Claude Code auto mode classifier."` | `autoHead`, `…-that-passed.go:400` | `~/.claude/projects/-Users-imac-IdeaProjects-oh-my-graph/f85ea6fb-f3a0-4cd8-8b05-7c86a570fbae.jsonl` line 16600 — `toolDenialKind: "automode-blocked"`, `is_error: true`, `tool_use_id toolu_018K6eTvdepwbE7oeUq2qqDz`, joining the assistant `Bash` tool_use on line 16599 |

The classifier's sentence does not name the tool it blocked, unlike the
`dontAsk` sentence which carries the tool between its two halves. The row
therefore records `autoTool` — the literal
`"(not named by the auto-mode classifier)"`
(`docs/measurements/0218-denied-nodes-that-passed.go:406`) — rather than
inventing a name or joining one in.

**Why those two and no third.** A census walked every `*.jsonl` under
`~/.claude/projects`, decoded each record, and tallied every `toolDenialKind`
value it found:

```py
import collections, glob, json, os

kinds, files = collections.Counter(), glob.glob(
    os.path.expanduser("~/.claude/projects/**/*.jsonl"), recursive=True)

def walk(o):
    if isinstance(o, dict):
        for k, v in o.items():
            if k == "toolDenialKind" and isinstance(v, str):
                kinds[v] += 1
            walk(v)
    elif isinstance(o, list):
        for v in o:
            walk(v)

for f in files:
    with open(f, errors="replace") as fh:
        for line in fh:
            if line.strip():
                walk(json.loads(line))

print(len(files), "files", dict(sorted(kinds.items())))
```

Run 2026-08-28 it prints:
`2797 files {'automode-blocked': 1, 'permission-rule': 406, 'user-rejected': 6}`,
with no undecodable line. This corpus is live and grows under the measurement,
so the file total and the `user-rejected` tally drift between passes; the two
figures the predicate rests on do not. The census run when the predicate was
extended, hours earlier, found the same lone `automode-blocked` record at the
same address — its message records that (`git show d640025 --format=%B --no-patch`),
and the same address is pinned in the source comment at
`docs/measurements/0218-denied-nodes-that-passed.go:391`. `user-rejected` needs
a TTY and so cannot occur in a `claude -p` node; it stays excluded, as `0218`
excluded it. No fourth value occurs in this corpus.

**Why both arms stay anchored at offset 0.** The same census surfaces ordinary
Bash failures whose captured stdout quotes the denial vocabulary — this
repository's own sessions grep for these sentences while measuring them. Two,
both in `~/.claude/projects/-private-tmp-sk/0b16210b-0264-4790-8ef4-ff9d56db43c3.jsonl`:
line 50 begins `Exit code 1\nI couldn't run it — Bash is blocked in this
session.\n\nClaude Code is currently in "don't ask" permission mode…`, and line
77 begins `Exit code 1\npermission_denials: [{"tool_name": "Bash", …`. A
`Contains` predicate counts both as denials; `HasPrefix` rejects both.

Because the predicate accepts only these two sentences, every count in §2 is a
**floor** on denials, not a total — the grant-time "No such tool available"
class and the command-naming rule phrasing are still outside it, as `0218`
recorded.

## 3. Which boundary field, and why

The boundary is the **`state.json` field `default_permission_mode`**
(`internal/runstate/runstate.go:430`), not a date. ADR 0034's precondition P2
requires exactly that — "the population must be selected structurally, not by
date" (`0034:570-574`) — because a run started before the change and resumed
after it still runs the old mode, so a date would misfile it.

**An absent or empty field is bucketed as `dontAsk`, because that is what those
runs ran under.** The field is `omitempty`
(`internal/runstate/runstate.go:430`), so a snapshot written before the change
carries no key at all; `bucketOf`
(`docs/measurements/0218-denied-nodes-that-passed.go:145`) maps absent and empty
onto `dontAsk`. Absence here is not missing data — it is the old default,
recorded by not being recorded.

The field proved usable, and was cross-checked by hand with a JSON parser
rather than by `grep` — `json.load` over each `~/.oh-my-graph/runs/*/state.json`,
tallying `default_permission_mode`. Over 362 run directories that prints
`{'None': 350, '<no state.json>': 3, "'auto'": 9}`: the only non-empty value
that occurs anywhere in this corpus is the literal `"auto"`, so no `auto` run
reached the absent-field fallback, and no run declares a third value. The 9 are
the 8 in the table plus the in-flight run excluded in §4. The `auto` bucket's 8
runs, by id, as the command prints them:

`20260825-123013.565158000-1`, `20260825-144133.799111000-2`,
`20260826-045726.186334000-1`, `20260826-053627.281082000-1`,
`20260826-061035.046887000-1`, `20260826-070622.430625000-2`,
`20260827-130646.161231000-1`, `20260827-194830.890267000-1`.

## 4. What was excluded, and never silently dropped

**Sessions with a `session_id` but no transcript file on disk — 3 in total, all
on the `dontAsk` side:**

| bucket | count | run / node and session |
| --- | --- | --- |
| `auto` | **0** | — |
| `dontAsk` | **3** | `20260820-162555.890191000-1` / `corpus`, session `21f5446f-6b30-4b45-bcd0-05a290d7c9a2` |
| | | `20260820-162555.890191000-1` / `precedent`, session `a889a07f-fd17-4dd7-9513-516f6be5a53d` |
| | | `20260822-010107.356534000-1` / `file-findings`, session `a647b122-2e28-473e-b476-c667c069a3e2` |

All three are recorded `FAIL`, and all three are **excluded from both numerator
and denominator** — not counted as undenied. A node whose transcript cannot be
read is not a node that was not denied (`0218:320-322`).

The other exclusions, with whole-corpus totals from the same output and the
per-side split re-derived from the rows of
`docs/measurements/0218-denied-nodes-raw.json`:

| exclusion | `dontAsk` | `auto` |
| --- | --- | --- |
| no record in `state.nodes` (the run halted before reaching the node) | 17 | 1 |
| no `session_id` on the record | 5 | 0 |
| transcript matched but unreadable | 0 | 0 |
| `session_id` matching more than one project dir | 0 | 0 |

241 − 17 − 5 − 3 = 216 and 43 − 1 = 42, which are the two denominators in §2.
Zero transcript lines in the whole corpus failed to decode as JSON.

**One run excluded as in flight, by id: `20260827-201645.027951000-2`** — bucket
`auto`, 5 graph nodes with 1 recorded and no record carrying `FAIL`, so the
script's in-flight test keeps it out of every bucket. It is the run this
document was written inside; its unfinished nodes' denials are not yet on disk.
Excluding it removes nodes from the `auto` side only, and is the reason the
`auto` denominator is 42 rather than larger.

## 5. Caveats

- **The post-change corpus is small, and it is ours.** All 8 `auto` runs came
  from this project's own lanes, on one machine, in the days between the change
  and 2026-08-28. That is not a sample of users; it is this repository measuring
  itself, under this operator's `allowed_tools` habits and this planner's
  conventions — `0218:35-50`'s self-measurement caveat applies unchanged.
- **n is too small to support a rate.** 42 readable nodes across 8 runs cannot
  carry a percentage, and the run — not the node — is the unit that varies
  independently (`0218:349-365`). No percentage appears in this document. Every
  figure above is a raw count over a named denominator, which is the only
  comparison `0218:349-365` permits.
- **The machine holds one `automode-blocked` record in total** (§2.1), and it is
  not from an engine node — it is from an interactive session in this
  repository's own project directory. So the observations that would populate
  the `auto` side have not accumulated yet, whatever the classifier does.
- **`0 of 42` beside `73 of 216` is not a contrast.** The two columns differ in
  population size, in date range and in which lanes made them, not only in the
  permission mode.
- Unmeasured, and left so deliberately: whether `auto` was in fact active in
  those 8 runs (ADR 0034 §3.3's third bullet); whether the headless
  classifier-denial abort occurred anywhere in this corpus; and the call-level
  class split from `docs/measurements/0213b-compound-commands.go` re-run over
  the `auto` population, which is the companion reading ADR 0034 §6 asks for.

## 6. What this says about ADR 0034's falsification clause

[ADR 0034](../adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md)
§6 names one threshold, verbatim at `0034:580-581`:

> **If the denied-and-still-`PASS` count is 44 or higher, revert to `dontAsk`.**
> The baseline is **49 of 73** (`0218:250`).

and scopes it at `0034:576-578` to "the first **73 readable planned nodes** in
runs recorded `default_permission_mode: "auto"`".

**That population does not exist in this corpus, so the clause cannot be
evaluated here. Its status is UNMEASURED.** The clause asks for 73 readable
planned `auto` nodes; the corpus holds 42 (§2). The shortfall is a population
that has not arrived, not a result.

**`0 of 0` must not be read as clearing the threshold.** The `auto` side's
denied-and-still-`PASS` cell is `0 of 0` — arithmetically below 44, and
meaningless: it is 0 over a denominator that is itself 0, inside a population
of 42 readable nodes where the clause names 73. Reporting it as "well under 44, no
revert indicated" would be reporting the absence of a measurement as a pass.

The two preconditions, however, now stand differently than they did:

- **P1 is met.** `0034:561-568` required the discriminator to match both
  phrasings, and stated the consequence otherwise: "**A re-run with the
  unmodified script reports approximately zero denials and that is an artifact,
  not a finding.**" The script now carries both sentences, each with a
  transcript address (§2.1), so the `auto` side's `(a) 0 of 42` is a negative
  observation rather than that artifact. ADR 0034 §7's item 2 is done.
- **P2 is met.** The split is on the structural field (§3), not on a date.

What remains owed to §6 is arrival: more `auto` runs, until 73 readable planned
nodes exist. The companion readings §6 attaches — the `0213b` class split over
the `auto` population, and any occurrence of the headless abort — are likewise
unmeasured (§5).

**The distinction ADR 0034 itself drew, kept here.** The ADR's positive case
rests on `0213b`'s "**84 of 246**" denied calls and on §1.3's non-TTY verdict
(`0034:105`, `0034:152`). It states that `0218` "does not support this change,
and must not be cited as though it did" (`0034:113-114`) and that "**The 44 is
the cost of missing verification and is ADR 0033's subject, not this one's**"
(`0034:126`). So column (c) is not this ADR's scoreboard: the `dontAsk` side's
6-of-67 engine-run `verify` is a fact about verification coverage, not about the
permission default. Only (a), and (b) beneath it, are what §6 watches.

## 7. Verdict

**No — this measurement does not show the default change moving (a):** the
post-change side reads `0 of 42`, which is now a genuine negative under a
predicate that can see the classifier's own denial sentence, but it sits on a
population of 8 of this repository's own runs over a few days — too small to
support a rate, and short of the 73 readable planned nodes ADR 0034 §6 requires
before its threshold can be applied at all.
