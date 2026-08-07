# ADR 0019 — A verdict's position anchor is bought per node, and the currency is what a false FAIL costs there

- Status: **Accepted.** One shipped node (`merge-shepherd`'s `merge`) moves
  from a reply-anchored verdict pattern to a line-anchored one (`(?m)`). Every
  other verdict pattern in the repo is unchanged. Every verdict-bearing prompt
  in every shipped graph and fragment gains one clause: where a caveat goes.
- Date: 2026-08-08
- Trigger: run `20260807-144514.899280000-1`, which merged PR #135 for real
  (`83edfad` is on `main`) and recorded the `merge` node FAIL.
- **Amends nothing.** DESIGN.md's "Verdict patterns" rule — *never relax `^`
  into an unanchored match* — is affirmed, and §3 below is the measurement
  that affirms it. This ADR adds a narrower rule underneath it and states the
  two conditions a node must meet to use it.
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## 1. Context — the mirror of the failure the anchor was built against

`merge-shepherd`'s header records why its last node's verdict is anchored at
`^`. A model once ended a real run of that graph green with nothing merged, by
replying *"CodeRabbit's re-review is mid-flight … Poller armed (30s interval,
12 min cap). I'll proceed as soon as it lands."* under a bare
`success_check: { exit_zero: true }`. The fix was a pattern with an anchored
alternation and a SHA payload, and it is load-bearing.

Run `20260807-144514` is the same node failing the other way. It ran all four
steps, merged PR #135, and replied:

```
PR #135의 로컬 브랜치는 존재하지 않아 `git branch -d` 대상이 없었습니다 (원격만 pruned).

MERGED 83edfad11db1ba0281cab9b615c4acadded38512 — ADR 0018 …
```

The verdict is there. The SHA is there, and it is real — `83edfad` is on
`main`. It is on line 3. The pattern is anchored at `^`, so the node was
recorded FAIL, the run ended `failed`, and the ledger now carries a row that
disagrees with the repository.

The mechanism is worth naming precisely, because it is not "the model ignored
the prompt". The prompt tells this node to do four things; step 4 is
`git remote prune origin` and `git branch -d` *the PR's local branch if one
exists*. There was no such branch. The model had an exception to report and a
prompt that says only what may **not** precede the verdict — "no markdown
emphasis, no heading, no backticks, no preamble" — and never says where a
caveat may go instead. It went on top.

## 2. What was measured

`~/.oh-my-graph/runs/` holds 187 runs with a `state.json`, containing **218
executions of a node that declares `result_matches`**. Of those, **22 failed
that predicate**. Every one of the 22 final replies was recovered — 5 from the
preserved `failed/<node>.out` artifact, and all 22 from the node's session
transcript under `~/.claude/projects/<cwd>/<session_id>.jsonl`, which exists
because of the "never `--no-session-persistence`" invariant. **Nothing in this
corpus is unattributable**; the preserved-reply feature being recent does not
create a blind spot here, because the transcript is the deeper record.

Each was then judged against the world where the world can answer:

| bucket | n | judged by |
|---|---:|---|
| the check working — work genuinely not done, or verdict genuinely absent | 16 | the reply says `FAIL` / `NOT READY` / `BLOCKED`, or promises future work |
| verdict first, rejected for markdown emphasis (`**PASS**` vs `^PASS`) | 1 | mechanical, from the reply |
| verdict first, rejected by a whole-reply `$` pin over trailing evidence | 3 | mechanical, from the reply |
| **verdict present, on a later line** | 2 | `git log` — `83edfad` is on `main` |

Four of the 16 are the literal promise reply this graph exists to reject
("the stress test is still running, I'll report the verdict when it
finishes"; "poller armed"; "I'll wait for that waiter"; "4 planner processes
are running, awaiting the completion notice"). **The check earns its keep.**

Direction: **all six misjudgements point the same way** — a node that had done
its work, recorded FAIL. No anchored verdict in this corpus admitted a reply
whose work was not done.

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
evidence. The three shipped whole-reply pins (`haiku-smoke`'s `write`, the
`e2e-verify` fragment, `apply-flags`'s `verify`) already say *"and nothing
else"* in the prompt, so their pattern and their instruction agree.

## 3. The counter-experiment — why the naive fix is still refused

The 22 replies were replayed under three patterns: as shipped, with the
anchor **dropped**, and with the anchor **weakened to `(?m)`**.

| | admits | of which correct FAILs wrongly admitted |
|---|---:|---:|
| as shipped (`^`) | 0 | 0 |
| anchor dropped | 10 | **9** |
| `(?m)` | 3 | **0** |

Dropping the anchor admits nine of the sixteen correct FAILs, for the reason
DESIGN.md already gives: `NOT READY` contains `READY`, and a `FAIL` report
that discusses the run's `PASS` lines contains `PASS`. Nine false passes to
buy one true fix. Refused, as before.

`(?m)` admits exactly the three replies that carried a real verdict on a later
line, and none of the sixteen — including none of the four promise replies,
which have no SHA, URL or count to put on any line. Against the historical
promise reply quoted in §1, and against seven constructed variants (a
multi-paragraph promise; `MERGED` with no SHA, with and without a preamble;
`WITHHELD:` and `WITHHELD —` with the reason stripped; the token used mid
sentence inside a promise; a `## MERGED 4f2a1c9` heading), `(?m)` rejects
every one — the same set the reply-anchored pattern rejects. The two it newly
accepts are PR #135's actual reply and a legitimate `WITHHELD <reason>` written
under one line of housekeeping.

## 4. Decision

**Prompt side, everywhere.** Every verdict-bearing prompt in every shipped
graph and fragment now names where non-verdict text goes. Prefix verdicts get
*"anything you need to qualify goes AFTER the verdict, never before it"*; the
three whole-reply pins keep the opposite instruction, *"and nothing else"*.
This is the fix for the mechanism actually observed and it costs nothing. It
is also not sufficient: a model that ignores it fails identically, which is
what §5 is for.

**Pattern side, one node.** `merge-shepherd`'s `merge` becomes

```
(?m)^[*_`\s\-—]*(MERGED[*_`\s:\-—]*[0-9a-f]{7,40}\b|WITHHELD[*_`\s:\-—]*[[:alnum:]])
```

— the anchor is not dropped, only moved from the reply to the line, so a
verdict named mid-sentence still fails.

**The rule that decides who else may have it.** Both conditions, or neither:

1. **A payload a promise cannot produce.** A merge commit SHA does not exist
   until the squash lands, and this node reads it out of its own
   `git pull --ff-only` range — which is why that command is inside the node's
   otherwise minimal grant. Contrast `adr-driven-dev`'s `adr` node: a file path
   is nameable by a node that drafted an ADR and never committed it, so there
   the first-characters position *is* the assertion. `pr`'s URL is the marginal
   case and stays anchored, for the reason in condition 2.
2. **A false FAIL that is not a free re-run.** Everywhere else it costs one
   re-run, and the rejected reply is preserved at
   `~/.oh-my-graph/runs/<id>/failed/<node>.out`, so the operator can see what
   was rejected and why. `merge` is the exception the graph's own comment
   already names: its re-run re-enters `gh pr merge` on an already-merged PR
   under a grant too narrow to look at what happened.

For a bare-word verdict (`PASS`, `READY`, `DONE`, `CLEAN`, `ship it`)
condition 1 can never hold, so `(?m)` is never available to one: position is
the only lock it has, and a line-anchored `PASS` passes any line that opens
with the word.

## 5. Alternatives, and why they lost

**Prompt-side only.** The instruction it would add is already there, in the
strongest form the repo writes it — `merge`'s prompt says the token must be
*"the very FIRST characters of the reply, with no markdown emphasis, no
heading, no backticks, no preamble"*, and PR #135 happened anyway. Adding the
caveat clause is a real improvement over that (it offers a place instead of
only forbidding one), but a prompt is not a mechanism — the same sentence
DESIGN.md uses against the opposite failure. Taken alone this leaves the one
node whose failure is not a re-run defended by instruction only.

**Pattern-side everywhere.** Applying `(?m)` to every payload-bearing verdict
would be more uniform and less honest. It weakens `adr` for free (a drafted,
uncommitted ADR can be named on any line) and it buys tolerance nothing has
asked for: 46 `pr` executions, 22 `apply` executions and 30 `triage`
executions across this corpus, with zero occurrences of this failure. Every
character of anchor spent without evidence is the trade this repo refuses in
the other direction.

**Neither — document the asymmetry and move on.** The strongest alternative,
and the one this project has taken before. One occurrence in 26 `merge` runs
is rare; a false FAIL announces itself while a false PASS does not; the reply
is now preserved. It loses on one fact: at this specific node the asymmetry
that makes "leave it" safe does not hold. A false FAIL here also writes a
wrong ledger row — one that says nothing merged while `main` has moved — and
the standard correction for it, re-running the node, is the one correction
this graph cannot safely apply. Both directions write a wrong row; only one of
them is cheap to fix. So the asymmetry is documented (DESIGN.md, §"Where the
verdict may sit"), *and* the one node where it inverts is fixed.

## 6. Consequences

- A green `merge-shepherd` run still does not mean anything landed — the
  verdict is two-valued and `WITHHELD` passes. That was already true and the
  graph header already says it. `(?m)` widens where in the reply that sentence
  may be found, not what it may say.
- `(?m)` now appears in one shipped pattern, so the DESIGN.md sentence
  "no `(?m)`" is corrected: the *engine* adds no flags; a pattern may set one.
  `regexp.Compile` in `scheduler.go` and the load-time validation in
  `SuccessCheck.Validate` accept it unchanged; `handoff.LintVerdicts` does not
  inspect pattern shape, so nothing else moves.
- The prompt clause is now a convention. A new verdict-bearing node that omits
  it is inconsistent with every shipped graph, and the sweep that finds it is a
  grep for `Anything you need to qualify` over nodes whose pattern does not end
  in `$`.

## Falsification

This decision is wrong if either half of it is wrong, and each half has a
number attached.

**The narrow relaxation is wrong** if a `merge` node ever passes on a reply
whose PR was not merged and which did not deliberately withhold. The check is
mechanical and already routine: after a green `merge`, `git log` either
contains the SHA the artifact names or it does not. One such occurrence
retires `(?m)` from this node and returns it to `^`; the cost of that reversal
is one line.

**The narrow scope is wrong** if the later-line failure turns out not to be
specific to `merge`. The population is every `result_matches` failure recorded
after this ADR, and the measurement is the one in §2, re-run: recover the
reply, and classify it as verdict-absent, verdict-not-first, or work-not-done.
**If two or more `pr` / `apply` / `adr` / `triage` nodes fail with a real
payload-bearing verdict on a later line**, the prompt clause has failed at
those nodes as it failed at `merge`, and condition 2 should be re-examined
rather than the occurrences absorbed one at a time. Today's baseline is 0 in
46 / 22 / 10 / 30 executions respectively.

The corpus this ADR measured is on disk under `~/.oh-my-graph/runs/`, and the
per-node replies survive in `~/.claude/projects/` as ordinary session
transcripts. Neither is committed; the numbers in §2 and §3 are the durable
record, which is why they are stated with their denominators.
