# ADR 0021 — A wait is only honest over a self-resolving condition

- Status: **Accepted.** A polling node classifies what is blocking it before it
  polls and on every read. A **self-resolving** condition — one a machine
  already at work will clear with nobody doing anything — is what a poll is
  for. A **latched** condition — one only an ACT by a person clears — gets a
  verdict that names the act, not a share of the polling budget.
- Date: 2026-08-11
- Trigger: three incidents on `merge-shepherd`, and a fourth defect found while
  explaining them. See §1.
- **Amends nothing.** ADR 0019's rule (never relax a verdict's `^`; make the
  false FAIL cheap instead) is untouched — no pattern in this repo changes.
  ADR 0010's two constraints on feedback arcs are what keep this a prompt-and-
  token decision rather than a graph-shape one (§5).
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## 1. Context — three incidents, and the one that was not reported

`merge-shepherd` has two polling nodes. `ready-and-wait` waits for CI and
CodeRabbit before triage; `recheck` waits again on the final SHA after triage
may have pushed. Both were `sleep 30` loops around `gh pr view`, bounded by the
node's own timeout, and both were written on one assumption: *the thing we are
waiting for is on its way.*

Three incidents said otherwise.

**2026-08-04 — the rate limit.** CodeRabbit answered a review request with an
issue comment: `Review limit reached — next review in 14 minutes`.
`ready-and-wait` was polling for a submitted review, saw none, and kept
polling. It never saw the comment at all — its read projected `reviews` and
`statusCheckRollup`, and a rate-limit notice is neither. Fourteen minutes of
foreground polling, then a failure, over a review that was never coming: the
clock opens the WINDOW, but only a re-request opens the review.

**2026-08-10 and 2026-08-11 — four halts on an unresolved thread.** CodeRabbit
had `CHANGES_REQUESTED` on the head commit. `recheck` correctly answered
`BLOCKED <sha>`, correctly halted the run before the gate, and correctly named
the SHA and the review. Four times the operator read that artifact, and four
times performed the same three acts by hand: resolve the review threads,
re-request the review, re-launch the graph. The verdict said what was wrong. It
never said what would unstick it, and the cost of that omission was paid four
times in two days.

**The `merge` promise family (2026-08-04 … 2026-08-08).** Recorded in
ADR 0019's 2026-08-09 update, five runs, and the reason `recheck` exists. Only
one of the five was a latch (`20260807-144230`, PR #134, a new
`CHANGES_REQUESTED`); three were checks triage's own push had restarted, which
is exactly the self-resolving case `recheck` already fixed.

**And the fourth thing, which nobody reported because it did not hang.** Trace
a HUMAN reviewer's `CHANGES_REQUESTED` through the chain as it stood:

1. `ready-and-wait` passes — its condition was "the `test` check is SUCCESS and
   *coderabbitai* has a submitted review". A human is not in that sentence.
2. `triage` may or may not touch the comments; either way it cannot clear a
   reviewer's decision, which only a re-review or a dismissal clears.
3. `recheck` filters `reviews` with `select(.author.login == "coderabbitai")`.
   The human is not in the list. It answers **`RECHECKED <sha>` — green.**
4. The gate comment tells the operator that CI and review status *is no longer
   yours to confirm — that is what recheck is.*
5. `merge` runs `gh pr merge --squash`, and its prompt licenses `--admin`
   because "the review is complete by construction (verify passed, CI and
   CodeRabbit concluded, comments triaged)".

That construction contains no human. The outcome is not a hang; it is a
**merge**, past a blocking review, with the graph's own gate comment telling
the operator not to look. The same keyhole hid `mergeStateStatus`: a PR
conflicting with its base reached `merge`, failed, answered `WITHHELD` — which
PASSES — and ended the run green with nothing shipped and no red row anywhere.

## 2. Decision

**Two rules, one at the read and one at the verdict.**

**Read what the platform already computed, and read it at the grain the
verdict needs.** Both waits now project `reviewDecision` and `mergeStateStatus`
out of the same single `gh pr view` call they already made, and both drop the
`select(.author.login == "coderabbitai")` filter, projecting every review's
`{who, oid, state, at}`. The filter is what hid the human of §1; the fields are
what GitHub computes for exactly this question.

`reviewDecision` is *reported, not judged by*, and that is a correction to this
ADR's first draft, which gated on it. The field is PR-level, never scoped to a
SHA, and a `COMMENTED` review does not clear a `CHANGES_REQUESTED` — so it is
one word for two conditions that need different acts, and it stays set over a
review a later push superseded. Both failure modes were measured before this
was written:

- Gating `ready-and-wait` on it halts the graph's **normal path**. The field
  flips to `CHANGES_REQUESTED` the instant CodeRabbit submits one, which is the
  same instant that node's READY condition ("the bot has a completed review")
  is met, and `triage` is the node whose entire job is to action it. All four
  sampled PRs (#144, #145, #148, #153) opened with exactly that review.
- Gating `recheck` on it produces an **unclearable latch**. PR #145 merged
  correctly with `reviewDecision: CHANGES_REQUESTED`: the request sat on
  `386b669`, a commit the head no longer was, and the act the field would name
  — resolve the threads, re-request — had already been performed and answered
  `COMMENTED`, which leaves the decision where it was forever.

So the judgment is made per reviewer. The bot's rule is SHA-scoped (it reviews
every push, so its latest word on `head` is its current word); a person's is
not (theirs stands until that person approves or a maintainer dismisses it),
and each names a different act. Both waits also judge the **whole** check
rollup instead of an entry named `test`; `recheck` already did, and
`ready-and-wait` had been left behind by that fix (the repo this graph was
written in carries three rollup entries).

**Split the failing verdicts by the operator's next action.** Every polling
node writes three verdicts:

| verdict | means | passes? |
|---|---|---|
| the success token | the condition concluded | yes |
| `UNSETTLED` / `NOT READY` | time ran out while something was still MOVING | `recheck`: yes. `ready-and-wait`: no |
| `LATCHED <what>; unblock: <act>` | the clock will not clear this | **no** — the run halts |

`LATCHED` is a new token at both nodes and it replaces `recheck`'s `BLOCKED`.
It carries two payloads: what is stuck, and the act that unsticks it. Both are
on the first line, so the operator answers "do I act, or do I wait?" from the
first word of `<run-id>/failed/<node>.out` rather than from a paragraph.

The classification, as the prompts state it:

| condition | class | the act, when latched |
|---|---|---|
| check run `IN_PROGRESS`; status context `PENDING`; `mergeStateStatus: UNKNOWN`; a "review in progress" placeholder | self-resolving | — |
| a rollup entry finished bad (`FAILURE`, `CANCELLED`, `TIMED_OUT`, `STARTUP_FAILURE`, `STALE`, `state: ERROR`) | latched | read the job log; fix and push |
| a rollup entry `ACTION_REQUIRED` | latched | approve the run or deployment in the Actions tab |
| the bot's latest review **on head** is `CHANGES_REQUESTED` | latched at `recheck`; **not** at `ready-and-wait`, where it is the ordinary path into `triage` | read the review; resolve the threads or push a fix, then re-request (`@coderabbitai review`) |
| a reviewer other than the bot holds an open `CHANGES_REQUESTED` (no later `APPROVED` from that same person) | latched at both | that reviewer re-reviews with `APPROVED`, or a maintainer dismisses it — a push and a `COMMENTED` clear neither |
| the bot's last comment says the review limit is reached, **and no review is newer than it** | latched | wait the stated N minutes, **then** post `@coderabbitai review` |
| `mergeStateStatus: DIRTY` | latched | rebase or merge the base in, and push |
| `mergeStateStatus: DRAFT` | latched | `gh pr ready` |
| a rollup entry at `QUEUED` or `EXPECTED`; no bot review on head | **latched only if it never moves** | approve the workflow run; drop the dead required context; post `@coderabbitai review` |

That last row is the honest one. Two conditions are indistinguishable from a
self-resolving one on a single read — a queued run that will start in ten
seconds looks exactly like a queued run awaiting a maintainer's approval. Those
are polled, and if the budget expires with them byte-for-byte unchanged, they
are latched, not slow. **A latch dominates:** if anything is latched, the
verdict is `LATCHED` even when other entries are legitimately still in flight.

`mergeStateStatus` is otherwise *reported, not judged*. `BEHIND`, `BLOCKED`,
`UNSTABLE` and `HAS_HOOKS` on an otherwise-green PR ride along on the
`RECHECKED` line as `mergeable: <state>`, next to `review_decision: <state>`,
and that line is what now licenses `--admin` at `merge` — replacing the
"complete by construction" clause, which was false about humans. `--admin` may
be reached only when the plain merge failed on protection or queue mechanics,
`recheck` named a mechanical state, AND that line does not say
`review_decision: REVIEW_REQUIRED` (§3).

**`gh pr ready` is the precedent, not the exception.** `ready-and-wait`'s
step 1 has always been an act, not a wait: a DRAFT PR is a latch, and this
graph resolved it by doing the thing rather than by polling for someone else to.
The rule here is that behavior generalized to the other eight latches.

## 3. What the graph deliberately does NOT do

**It does not resolve a review thread.** `resolveReviewThread` is a GraphQL
mutation, so a node that closed threads would need `Bash(gh api graphql *)` —
a grant that can do anything the API can do — and it would be a node closing a
reviewer's verdict on its own judgement. This project has not done that
anywhere, and the evidence says not to start: across the four hand-repairs, the
operator read the reason before closing, and in one of them (#148's
`build_test` finding) closing blind would have been wrong. The graph names the
act; the person performs it.

**It does not re-trigger the bot.** Posting `@coderabbitai review` is the second
of the operator's three acts, and automating it without the first — resolving
the threads — reproduces the same `CHANGES_REQUESTED` and buys a loop. That is
not a scheduling problem; it is the same "closing a reviewer's verdict"
objection wearing a cheaper hat.

**It does not sleep out a rate limit.** "Next review in 14 minutes" names a
window, not an event. Sleeping through it and re-polling finds nothing, because
nothing is queued behind the limit — the graph would have re-learned the
original defect at a fourteen-minute price.

**It does not `--admin` past `REVIEW_REQUIRED`.** `--admin` is narrowed to a
named mechanical merge state, and `REVIEW_REQUIRED` is excluded by name: when
`recheck`'s line says `review_decision: REVIEW_REQUIRED`, protection is holding
the PR because *nobody has approved it*, `mergeStateStatus` reports that as
`BLOCKED` like any other protection hold, and `--admin` over it is exactly "a
way past a review". `merge` answers `WITHHELD` naming the missing approval
instead. This repo's `main` requires one approving review and conversation
resolution, so the case is live rather than theoretical.

That exclusion is by decision, not by state name: `--admin` was *not* narrowed
all the way to `BEHIND` alone, which the diagnosis proposed. A repo whose
protection reports `BLOCKED` for a bot-only review would then fail its `merge`
node on every ordinary green PR, which trades a real defect for a daily one.
`BLOCKED` stays admissible; what is excluded is the one review state that says
the block IS the review. The `CHANGES_REQUESTED` a `RECHECKED` line may still
carry is a different thing — `recheck` judged it per reviewer and found it
spent (§2), and it says so on that line, so `merge` is admining past a
*superseded* review, not an open one.

## 4. Falsification

**The latch classification is wrong** if a condition this ADR calls latched
clears itself in the field — a `recheck` that answers `LATCHED` over a
`reviewDecision` or a rollup entry that then goes green with nobody acting.
One such run makes that row self-resolving; the fix is to move the row, not to
widen the poll.

**The split is not worth its token** if the next four halts still cost a
hand-repair each. The measurement is direct: for each `LATCHED` artifact, did
the operator perform the act named on line 1, or a different one? Baseline: 4
halts on 2026-08-10 and 2026-08-11, 4 hand-derived repairs, 0 verdicts naming
an act.

**The keyhole story is wrong** if a `merge-shepherd` run merges past a blocking
review after this change, or ends green over a `DIRTY` PR. Baseline for the
first is **unmeasured, not zero**: the false GREEN of §1 was found by reading
the chain, not in the corpus, and it leaves no failed row to count — which is
exactly why nobody reported it. What can be checked afterwards is the artifact:
a `RECHECKED` line now states `mergeable:` and the review decision it saw.

**What is NOT claimed.** That the three historical incidents share one cause.
They do not, and §1 says which is which — one rate limit that was never
observed, one class of restarted checks that `recheck` already fixed, and four
halts whose verdict was correct and incomplete. What they share is narrower and
is the reason they are in one ADR: every one of them is a wait, or a merge,
made over a **proxy** for "is this PR ready" — one bot's opinion plus a rollup
— while GitHub was computing the real answer in two fields nobody read.

## 5. Alternatives, and why they lost

**Keep `BLOCKED` and only add the remedy text.** The smallest change, and it
fixes the four-times cost. It does not fix the other four rows: `EXPECTED`, an
unapproved `QUEUED`, a rate-limited bot and a missing review would still be
polled for the full timeout and then answered `UNSETTLED`, a word that tells the
operator to wait. Severity was never the axis; *who clears it* is.

**A new token only at `ready-and-wait`, a sentence inside `UNSETTLED` at
`recheck`.** Rejected on the requirement it was supposed to satisfy: a sentence
inside a verdict is a paragraph to read, and two words for one class across two
nodes is a grammar the reader has to learn twice.

**A `feedback:` arc back to `triage` on a latch.** Unavailable, for the two
reasons `recheck` itself is a node rather than an arc: an arc fires only on a
judgment failure of the DECLARING node (`schedule.judgeFeedback` returns early
unless `node.Feedback != nil` and `isJudgmentFailure(cause)`), and ADR 0010's
load rule 4 forbids a gate inside a loop body (`graph.validateFeedback`), which
an arc spanning `recheck` would pull `approve-merge` into. Also, and
independently: the repair for most latches is not another triage round.

**Make `LATCHED` pass and let the gate decide.** A gate over a latch is a
question with one answer — nothing lands until someone acts — and it would put
the graph's most expensive node (`merge`) downstream of a PR that cannot merge.
Halting is what leaves the artifact where the operator reads it.

## 6. Consequences

- `merge-shepherd`'s two waits share one read, one classification and one
  vocabulary. A third polling node in this repo is expected to state the same
  three verdicts; the shape is in DESIGN.md's "Verdict patterns".
- `recheck` gains no grant. `reviewDecision`, `mergeStateStatus` and `comments`
  are `gh pr view --json` fields, and `--jq` runs inside it, so
  `Bash(gh pr view *)` still covers the whole read —
  `internal/graph/shipped_graphs_test.go` pins that grant exactly.
- The halting half of a verdict grammar cannot be pattern-enforced. A regex
  that must REJECT a token cannot also shape its payload, so `LATCHED`'s
  `unblock:` clause is held by a test over the shipped prompt instead. Any
  future halting verdict inherits that limit.
- `UNSETTLED` narrows: it may now be written only over something that was
  moving. It still passes, and an `UNSETTLED` the operator approves over still
  ends a run green with nothing merged (ADR 0019 §6) — but the population that
  can reach it is smaller by eight rows of §2's table.
- No `result_matches` pattern in the repo changed. ADR 0019 holds.
