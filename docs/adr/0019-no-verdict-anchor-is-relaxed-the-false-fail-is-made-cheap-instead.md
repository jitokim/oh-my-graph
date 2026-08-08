# ADR 0019 — No verdict anchor is relaxed; the false FAIL is made cheap instead

- Status: **Accepted.** Every `result_matches` pattern in the repo keeps its
  reply anchor (`^`). Every verdict-bearing prompt in every shipped graph and
  fragment gains one clause: where a caveat goes. `merge-shepherd`'s `merge`
  gains a state-establishing step 0 and two read-only commands in its grant, so
  that the one false FAIL this repo cannot cheaply re-run becomes one it can.
- Date: 2026-08-08
- Trigger: run `20260807-144514.899280000-1`, which merged PR #135 for real
  (`83edfad` is on `main`) and recorded the `merge` node FAIL.
- **Amends nothing.** DESIGN.md's "Verdict patterns" rule — *never relax `^`
  into an unanchored match* — is affirmed, and §3 is the measurement that
  affirms it, including against the narrower relaxation (`(?m)`) that this ADR
  proposed to itself and then refused.
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## 1. Context — the mirror of the failure the anchor was built against

`merge-shepherd`'s header records why its last node's verdict is anchored at
`^`. A model once ended a real run of that graph green with nothing merged, by
replying *"CodeRabbit's re-review is mid-flight … Poller armed (30s interval,
12 min cap). I'll proceed as soon as it lands."* under a bare
`success_check: { exit_zero: true }` (run `20260804-143531.477040000-1`). The
fix was a pattern with an anchored alternation and a SHA payload, and it is
load-bearing.

Run `20260807-144514` is the same node failing the other way. It ran all four
steps, merged PR #135, and replied — the first line of the reply was Korean and
is translated here; only the wording changed, not where it sat:

```text
PR #135 had no local branch, so `git branch -d` had nothing to delete (only the remote was pruned).

MERGED 83edfad11db1ba0281cab9b615c4acadded38512 — ADR 0018 …
```

The verdict is there. The SHA is there, and it is real — `83edfad` is on
`main`. It is on line 3. The pattern is anchored at `^`, so the node was
recorded FAIL, the run ended `failed`, and the ledger carried a row that
disagreed with the repository.

The mechanism is worth naming precisely, because it is not "the model ignored
the prompt". The prompt tells this node to do four things; step 4 is
`git remote prune origin` and `git branch -d` *the PR's local branch if one
exists*. There was no such branch. The model had an exception to report and a
prompt that says only what may **not** precede the verdict — "no markdown
emphasis, no heading, no backticks, no preamble" — and never says where a
caveat may go instead. It went on top.

## 2. What was measured

`~/.oh-my-graph/runs/` holds 187 runs with a `state.json`. Joining each run's
`events.jsonl` (`node_passed` / `node_failed`) against the `success_check` in
that run's own graph snapshot gives **218 executions of a node that declares
`result_matches`**. Of those, **22 failed that predicate**.

*Method, because the number is not re-derivable without it.* The count comes
from the event stream, not from `state.json`'s `nodes` map: that map keeps only
the LAST record per node id, so retries and feedback rounds overwrite
themselves. Counting `state.json` alone gives 211 executions and 18 failures —
the same corpus, seven attempts and four failures short.

Every one of the 22 final replies was recovered — 5 from the preserved
`failed/<node>.out` artifact, and all 22 from the node's session transcript
under `~/.claude/projects/<cwd>/<session_id>.jsonl`, which exists because of the
"never `--no-session-persistence`" invariant. **Nothing in this corpus is
unattributable**; the preserved-reply feature being recent does not create a
blind spot here, because the transcript is the deeper record.

Each was then judged against the world where the world can answer:

| bucket | n | judged by |
|---|---:|---|
| the check working — work genuinely not done, or verdict genuinely absent | 16 | the reply says `FAIL` / `NOT READY` / `BLOCKED`, or promises future work |
| verdict first, rejected for markdown emphasis (`**PASS**` vs `^PASS`) | 1 | mechanical, from the reply |
| verdict first, rejected by a whole-reply `$` pin over trailing evidence | 3 | mechanical, from the reply |
| **verdict present, on a later line** | 2 | `git log` — `83edfad` is on `main` |

**Three** of the 16 are the literal promise reply this graph exists to reject
("the stress test is still running, I'll report the verdict when it finishes";
"I'll wait for that waiter"; "4 planner processes are running, awaiting the
completion notice"). **The check earns its keep.**

Not four. The fourth reply this project has quoted in that role — *"poller
armed (30s interval)"* — is `merge`'s reply in run `20260804-143531`, and that
node **passed**: it ran under `success_check: { exit_zero: true }` with no
pattern at all. It is the corpus's one known false PASS, it is the reason §1's
pattern exists, and it is not one of the 22.

Direction: **all six are pattern misjudgements pointing the same way** — a
reply the pattern rejected for where its verdict sat, or for how it was
decorated, and not for what the reply said. Whether the work behind each was
actually done is the separate question the "what could not be attributed"
paragraph below keeps in its own buckets: one world-confirmed, one a synthetic
fixture, four unconfirmable.

**The pass side, and how far it was audited.** The other 196 executions passed,
and they were not audited as a body; "no anchored verdict admitted a reply whose
work was not done" is not a claim this ADR can make about all of them. What was
audited is the node the decision is about. `merge` has 25 PASSes in the corpus,
18 of them under the anchored pattern: 16 `MERGED <sha>` and 2
`WITHHELD <reason>`. All 16 SHAs are ancestors of `main` today
(`git merge-base --is-ancestor`, checked one by one). The pattern's pass side is
clean at this node, world-confirmed, 18 for 18. The 7 unpatterned `merge`
PASSes include the false PASS above.

**What could not be attributed, stated plainly.** "Was the work done?" is
answerable from the world only for nodes that change it. Of the six
misjudgements, exactly one is world-confirmed: PR #135's merge commit is on
`main`. One is a synthetic fixture (`release-check`, a graph whose prompts are
literally *"Think briefly about … then reply with exactly: OK binaries"*) where
the question is meaningless. For the other four — read-only `verify`/`e2e`
nodes in ad-hoc graphs — the reply's *shape* is checkable and its *claim* is
not: nothing on disk today can confirm that a suite passed in a throwaway
worktree in August. They are counted as pattern misjudgements, which is
mechanically true, and **not** counted as "work was done", which would be a
guess. One of the four is partly checkable (the commit it claimed to have
pushed, `a0ad221`, does exist in the repository).

Two of the three `$`-pin misjudgements and the one emphasis misjudgement are
not reachable from a shipped graph today: the emphasis case is what bought the
`` [*_`\s] `` decoration class, and all three `$` cases were ad-hoc graphs
whose prompts said *"reply with exactly PASS"* while also asking for the
evidence. The shipped whole-reply pins already say *"and nothing else"* in the
prompt, so their pattern and their instruction agree. There are four of them,
not three: `haiku-smoke`'s `write`, the `e2e-verify` fragment, `apply-flags`'s
`verify`, and — outside `graphs/` — `coordinator.plannedVerdictPattern`, the
whole-reply pin the auto-mode planner hands its branch-assertion check node,
whose prompt paragraph already carries the same "and nothing else".

## 3. The counter-experiment — why both relaxations are refused

The 22 replies were replayed against **their own node's pattern** under three
variants: as shipped, with the anchor **dropped** (the leading `^` stripped, any
`$` left in place), and with the anchor **weakened to `(?m)`** (the flag
prefixed, both anchors left in place).

| | admits | of which correct FAILs wrongly admitted |
|---|---:|---:|
| as shipped (`^`) | 0 | 0 |
| anchor dropped | 11 | **8** |
| `(?m)` | 3 | 0 of the 22 |

Dropping the anchor admits eight of the sixteen correct FAILs, for the reason
DESIGN.md already gives: `NOT READY` contains `READY`, and a `FAIL` report that
discusses the run's `PASS` lines contains `PASS`. Eight false passes to buy
three true fixes. Refused, as before.

`(?m)` admits three of the 22 and none of the 16 correct FAILs — and that table
row is where this ADR first went wrong, in two ways.

**It is not three later-line verdicts.** Only two of the three are:
`release-check`'s fixture, and PR #135. The third — `pe-flags`'s `verify`,
pattern `^COVERED$` — has `COVERED` on the **first** line. `(?m)` admits it by
releasing the `$`, converting a whole-reply pin into a per-line pin. That is a
side effect worth stating on its own: applied to a `$`-pinned pattern, `(?m)`
does not move the anchor, it deletes the pin. Every "and nothing else" verdict
in this repo would stop meaning "and nothing else".

**And "none of the 22" is the wrong population.** The 22 are replies that
already failed. The question a relaxation has to answer is what it lets in that
the shipped pattern keeps out, and the promise reply is not in the corpus of
failures — it is in the corpus of *passes*, because before the pattern existed
it passed. Constructed against `merge`'s own pattern with `(?m)` prefixed, all
five of these pass:

| constructed reply (nothing merged in any of them) | `^` | `(?m)^` |
|---|---|---|
| "…I will merge when it concludes.\n My final reply will be:\n`` `MERGED 4f2a1c9 — ADR 0018` `` " | reject | **accept** |
| a plan listing both verdicts as bullets: "- `` `MERGED 4f2a1c9` `` — the squash merge has landed" | reject | **accept** |
| "The instructions say the reply must be:\n\n    MERGED 4f2a1c9\n\nand I cannot produce that until the squash lands." | reject | **accept** |
| "Steps remaining: 1. gh pr merge…\nThen:\nMERGED 83edfad — I will fill in the real SHA." | reject | **accept** |
| the PR head SHA on its own line: "MERGED 2be7c58 will be the result once I squash it." | reject | **accept** |

The proposed condition for the relaxation was *"a payload a promise cannot
produce"*, and it is **false**. The pattern does not require a merge commit; it
requires **seven hex characters**. `merge-shepherd.yaml` hands the model the
literal string `MERGED 4f2a1c9` as the format example, and the PR head SHA also
exists before the squash. What actually keeps the promise out under `^` is that
a promise is preamble-first by construction: the sentence explaining why the
verdict cannot be given yet comes before the quote of what it will say. `(?m)`
makes that preamble free.

So the payload is not the lock. **Position is the lock**, for `merge` exactly as
for a bare-word `PASS`.

`^` is not a proof either, and this ADR does not claim it is: a reply that
*opens* `MERGED 2be7c58 will be the result once I squash it` passes it. What `^`
buys is that the promise has to be written in a shape no model reaching for one
has ever written — verdict first, hedge second. Every promise reply in this
corpus explains itself before it quotes itself. The prompt's *"'Not yet' is not
a third verdict"* is what covers the rest, and it is an instruction, not a
mechanism; the honest statement is that this check is a cheap filter over a
prompt, which is what `graph.SuccessCheck` already says about `result_matches`.

## 4. Decision

**Prompt side, everywhere.** Every verdict-bearing prompt in every shipped
graph and fragment now names where non-verdict text goes. Prefix verdicts get
*"anything you need to qualify goes AFTER the verdict, never before it"* — 28
nodes; the four whole-reply pins keep the opposite instruction, *"and nothing
else"*. This is the fix for the mechanism actually observed and it costs
nothing. It is also not sufficient: a model that ignores it fails identically.

**Pattern side, nothing.** No pattern in the repo changes. `merge` keeps

```regex
^[*_`\s\-—]*(MERGED[*_`\s:\-—]*[0-9a-f]{7,40}\b|WITHHELD[*_`\s:\-—]*[[:alnum:]])
```

**Cost side, one node.** The reason `merge` looked like it needed the
relaxation was never that the pattern misjudged it — the pattern judged
PR #135's reply correctly, by the rule the prompt states. It was that this
node's false FAIL was the one in the repo a re-run could not safely correct: the
re-run re-entered `gh pr merge` on an already-merged PR, under a grant too
narrow to look at what had happened. So that is what changes:

- `merge`'s prompt gains **step 0** — establish the PR's state before changing
  it; if it is already `MERGED`, do not re-enter step 1, take the SHA and report
  the merge that landed. The SHA is confirmed only *after* step 2 refreshes
  `origin/main`, with `git merge-base --is-ancestor`: a confirmation read off a
  stale remote-tracking ref is how a re-run turns a real merge into a second
  false FAIL.
- `merge`'s `allowed_tools` gains `Bash(gh pr view *)` and
  `Bash(git merge-base *)`. Both are read-only. The grant's purpose — that the only thing this node may
  *change* is the merge itself — is intact, and DESIGN.md's four-exec-seam
  invariant is untouched (these are tools inside a claude node, not a new
  spawner).

A false FAIL at `merge` now costs exactly what it costs everywhere else: one
re-run, over a preserved reply.

**The rule this leaves behind.** A verdict's `^` is not tradeable against a
payload, because a payload a model can *type* is a payload a promise can
produce, and every payload this repo has is one a model types. Where a false
FAIL is unusually expensive, make the re-run cheap; do not buy tolerance in the
pattern. `(?m)` remains unused in this repo.

## 5. Alternatives, and why they lost

**`(?m)` at `merge`.** Rejected in §3: it re-admits the exact failure the
pattern exists to reject, in five constructed forms, one of which is the
prompt's own example quoted back.

**`(?m)` at `merge` plus a world check.** `success_check` is a **conjunction**
(`graph.SuccessCheck`: all configured predicates must pass), so adding
`verify: { command: gh pr view … }` can only make the check stricter — it
cannot rescue the false FAIL that motivated the relaxation, and it cannot be
written correctly anyway: `WITHHELD` is a legitimate PASS whose PR is *not*
merged, and the verify command cannot see which verdict the reply carried.

**Pattern-side everywhere.** Applying `(?m)` to every payload-bearing verdict is
strictly worse than applying it at one node, and there is no evidence asking for
it — see the falsification section for what the denominators actually are.

**Prompt-side only, and leave the cost alone.** The strongest alternative, and
what this ADR would reduce to without the step-0 change. It loses on the same
fact the relaxation was reaching for: at this one node the standard correction
for a false FAIL was not safe to apply. Making it safe is a smaller, more honest
change than widening what the check accepts.

## 6. Consequences

- A green `merge-shepherd` run still does not mean anything landed — the
  verdict is two-valued and `WITHHELD` passes. That was already true and the
  graph header already says it.
- DESIGN.md's "no flags the engine adds" sentence stands, and so does "no
  `(?m)` in this repo": a pattern *may* set a flag, and none does.
- `merge` can now read PR state and test ancestry with `git merge-base`. Any
  future narrowing of that grant must keep step 0 executable, or it re-creates
  the expensive false FAIL.
- The prompt clause is a convention across 28 nodes. A new verdict-bearing node
  that omits it is inconsistent with every shipped graph. The sweep that finds
  it is `grep -c "Anything you need to qualify" graphs/*.yaml
  graphs/fragments/*.yaml` — which required reflowing two prompts
  (`adr-driven-dev`'s `impl`, `merge-shepherd`'s `triage`) where the phrase
  wrapped across lines and the sweep silently missed them.
- `internal/graph/shipped_graphs_test.go` now pins the shipped `merge` pattern
  against the promise family, including the `(?m)` counterfactual, so the
  measurement in §3 stops being a claim in a document.

## Falsification

**The refusal is wrong** if a `merge` node fails with a real, payload-bearing
verdict on a later line **again** after the prompt clause and step 0 are in.
Baseline: 1 occurrence in **19** patterned `merge` executions (26 `merge`
executions exist; 7 predate the pattern). Two more retire the refusal and
re-open `(?m)` — with the promise family in §3 as the acceptance test any
replacement pattern has to pass, since position is the only thing currently
rejecting them.

**The narrow scope is wrong** if this failure turns out not to be specific to
`merge`. Here the honest statement is that **there is almost no baseline**, and
the previous draft of this ADR claimed one it did not have. Patterned
executions per node, counted the way §2 describes:

| node | executions | of which under a `result_matches` |
|---|---:|---:|
| `pr` | 49 | **0** |
| `apply` | 21 | **0** |
| `adr` | 10 | **0** |
| `triage` | 30 | 23 |
| `merge` | 26 | 19 |

"Zero occurrences at `pr` / `apply` / `adr`" is vacuous — none of those
executions ran under a pattern that could have recorded one. Any falsification
keyed to those nodes cannot fire until they are running patterned, and this ADR
does not claim evidence it has none of. The live denominators are `triage` (23,
1 failure, a `BLOCKED` — the check working) and `merge` (19, 1 failure, the one
in §1).

The measurement to re-run is the one in §2: recover the reply, and classify it
as verdict-absent, verdict-not-first, or work-not-done. The corpus is on disk
under `~/.oh-my-graph/runs/`, and the per-node replies survive in
`~/.claude/projects/` as ordinary session transcripts. Neither is committed;
the numbers in §2 and §3 are the durable record, which is why they are stated
with their denominators and with the method that produced them.
