# ADR 0016 — A retry carries the attempt it is repeating

- Status: Accepted — implemented.
- Date: 2026-08-06

## Context

Until now a retry was a byte-identical re-spawn. `runNode` interpolated the
prompt once, *outside* the attempt loop, and `prepareRetry` changed exactly two
things:

```go
invocation.ResumeSession = ""
invocation.SessionID = s.sessionIDs()
```

The argv of attempt N+1 differed from attempt N's in `--session-id` and nothing
else. The node was not told that it had already run, that a check had rejected
what it said, or what it had said. A `result_matches` miss re-ran a node that
had no way to know which of its choices the check faulted, and the run paid full
price for the same reasoning again.

Two facts made this worse than it sounds.

**The engine was already discarding the reply.** `PersistOutput` ran only on
`verdictErr == nil`; on a failure the node's whole reply went out of scope with
the stack frame, and the only thing that survived was a 240-rune `Detail`. For
the largest single class of failure — `result_matches` — that detail is
`"result did not match /<re>/"`, which carries **zero bytes** of the reply. A
measurement across 133 local runs found 36 `node_failed` events, of which 11
were `result_matches` misses carrying $21.33 of spend and no reply at all. The
preceding change (`Handoff.PersistFailure` → `<run-dir>/failed/<node-id>.out`)
stopped the discard. This ADR is what makes the kept record useful to the run
that paid for it, rather than only to a human reading it afterwards.

**The engine already does this for plans.** A planner reply refused by
validation is not thrown away: `repair.go` hands the validator's refusals back
to one corrected attempt, fenced with a per-call nonce, with the cost of the
rejected call disclosed. The asymmetry was stark:

| | planner rejection | node failure |
|---|---|---|
| output | rejected spec kept (`PlanRejection.Spec`) | reply discarded |
| feedback | refusals **fenced** into one bounded re-plan | identical prompt re-run |
| cost disclosure | `PlanRepair.RejectedCostUSD` | a FAIL row, no artifact |

## Decision

A retry's prompt carries **one** fenced quote of the immediately preceding
attempt's own reply, and nothing about the check that rejected it.

### 1. What is quoted, and what is not

The quote is the node's reply, verbatim (bounded — §3). It is introduced as the
node's own PRIOR ATTEMPT, which did not pass.

The success check is **not** quoted: not its expression, not its predicate name,
not the failure detail that embeds them. This is the design's answer to the
strongest argument against doing any of this (see Alternatives): a node told
that `result_matches: "^PASS"` rejected it has been handed the cheapest possible
way to pass, which is to print `PASS`. That is not hypothetical — DESIGN.md
already records a model writing `**PASS**` and *that exact reply* failing a
check. Withholding the predicate is what makes gaming it impossible; the node
is pointed back at the instructions above the quote, which are the only
statement of what the work is.

The prompt says so explicitly, and tells the node not to argue the verdict, not
to address the check, and not to write a report about the retry — because the
failure mode a rejection notice invites is re-litigation, and the instruction
against it is cheaper than the paid attempt that would discover it.

### 2. The bound: exactly one prior attempt

`priorAttemptsInPrompt = 1`. Attempt N+1 carries attempt N and no earlier one.

This is enforced structurally, not by convention: `runNode` keeps `basePrompt`,
the interpolated node prompt with nothing appended, and every retry is rebuilt
**from it**. There is no code path that appends a quote to a prompt that already
has one.

Why one:

- **Cost.** Accumulating makes a node's quoted material triangular in the
  attempt index — k attempts carry k(k+1)/2 replies where this carries k. At the
  byte cap below and `retry: {max: 3}`, that is roughly 80 KB of quoted model
  output across a node's attempts against roughly 32 KB, paid on every attempt
  of every leg. Retry exists to absorb a failure cheaply; a retry policy whose
  prompt cost grows quadratically in its own bound is not that.
- **Value.** Attempt N-1 is the one the check actually rejected last, and it
  already had N-2 in front of it. Older attempts are superseded drafts.
- **Trust surface.** The fence's guarantee is per block. More untrusted blocks
  is more room for a reading model to lose track of which region is data, not
  less.

### 3. The byte bound

`maxPriorReplyInPrompt = 8000` bytes, cut head-and-tail with the cut announced
(`fence.Excerpt`).

`failed/<node-id>.out` already caps what reaches disk at 256 KiB. That is the
right order for a file a human reads once and the wrong order entirely for text
re-sent on every attempt. 8000 bytes is roughly 2k tokens — four times what the
assessor allows one artifact (`maxAssessArtifactExcerpt = 2000`), because a
retry quotes exactly one thing where an assessment quotes a whole run.

### 4. The fence

The quote is untrusted model output entering a paid prompt, so it is fenced the
way every other such quote in this codebase is: a per-call nonce from
`crypto/rand`, carried in **both** markers, minted after the reply is already
fixed so the reply cannot contain it.

`internal/coordinator/fence.go` moved to its own package, `internal/fence`, for
this: the fifth caller is not a coordinator, and hand-rolling a second nonce
minter is precisely the divergence a shared fence exists to prevent. The doc
comment counting the call sites moved with it, and the test that holds that
count honest (`invariants.TestFenceCallSiteCountMatchesTheCode`) now counts
`fence.Nonce` **repo-wide** rather than within one package — package-scoped, it
would have missed this caller and let the sentence go quietly stale by one.

A nonce that cannot be minted **drops the quote** and says so on the progress
feed. It never falls back to fixed markers, which would be a fence in name only.
This is a different judgement from the coordinator's, which aborts, and the
difference is what is left to fall back on: a planner call whose material cannot
be fenced has no un-fenced version worth making, while a retry without the quote
is exactly the retry this engine ran before this ADR — complete, valid, and
merely less informed.

### 5. Only judgment failures

The quote fires only when `isJudgmentFailure` holds — ADR 0010's
judgment-vs-infrastructure split, reused rather than re-invented. A spawn error,
an interpolation error, a blown budget and a verification that could not be
*completed* rendered no verdict on the reply; quoting it back would ask the node
to repair something nothing found fault with. A run error is the sharpest case:
the attempt it repeats produced no reply at all, so the rebuild from `basePrompt`
actively **drops** a quote an earlier attempt was carrying.

### 6. `handoff: session` retries start cold — unchanged

A retried `handoff: session` node does not resume its parent's session. It did
not before this ADR (`prepareRetry` clears `ResumeSession`; `lint` warns about
it up front and the passing attempt's ledger detail states it), and it does not
now. Nothing here is special-cased for session handoff.

What changes is that the cold start is no longer empty-handed. The quoted text
is the node's own words torn out of a conversation it can no longer see, which
is a strange thing to hand someone without saying so — so the prompt says it
outright ("You are a FRESH claude session. That attempt is not in your context
and neither is any conversation it belonged to; the quote is all of it you get").
Without that sentence, "improve on your previous attempt" asks the node to
recall a thread it cannot open.

Quoting into a cold start is, if anything, more defensible than quoting into a
warm one: there is no context for the quote to contradict.

### 7. Across the process boundary

`resume --retry-failed` is a different process, and it *drops* the FAIL record
before it runs anything — with it the ledger row and the capped detail. The
reply on disk is the only account of the failed attempt that crosses that
boundary, which is the entire reason it is written by one process and read by
another.

`Handoff.SeedPriorReply` re-reads `failed/<node-id>.out` for each cleared node;
`TakePriorReply` hands it to that node's first execution in the new leg and
forgets it, so a later feedback round in the same leg is not handed a reply from
before the loop re-armed. A read failure is a **warning**, matching the write
side (`keepFailedReply`): the leg's job is to re-run the node, and losing the
quote costs context, not correctness. (Contrast `SetFeedback`, whose failure is
fatal — a feedback re-run without its payload is a paid lie about what the body
was told.)

The gate has to cross the boundary too. A snapshot carries no cause, only its
rendered detail, and re-deriving "was this judged?" by parsing English prose
would make a wording change silently move a trust boundary. So the answer is
recorded: `runstate.NodeRecord.Judged` (`json:"judged,omitempty"`), written by
`recordFail` from `isJudgmentFailure`, absent on every PASS and every marker.
It is additive under RUN-FEED's own rule — **no schema bump**, precedent
`round` and `budget_usd` — and it is read from the *unpartitioned* snapshot,
because the record carrying it is one of the ones the retry leg drops.

Without it the two halves of one feature would disagree about their own
trigger: a budget-killed node, whose reply no check ever faulted, would be told
across a process boundary that a check rejected it. The field has value beyond
this too — "the work was wrong" and "the machinery broke" stop being
distinguishable only by reading prose.

### 8. Not opt-in

The retry quote is on by default for judgment failures. This deviates from the
diagnosis that preceded this ADR, which recommended a flag defaulting off, and
the deviation is deliberate on two grounds.

The flag was hedging the gaming risk, and §1 disarms that risk at the design
level rather than at the configuration level: with the predicate withheld there
is no assertion to game. What is left of the risk — untrusted text in a paid
prompt — is answered by the fence and the bound, which are not optional.

And a knob defaulting off is a fix that never reaches a single existing graph.
Every graph in this repository and every graph a user has already written would
keep re-running failed nodes blind until someone read a release note. The change
is not neutral in cost, so it is disclosed in the CHANGELOG and in DESIGN.md's
retry section rather than hidden behind a default.

## Consequences

**Positive**

- The commonest judgment failure stops re-running blind. A retried node knows
  what it said and that it was not accepted.
- A run's own record becomes useful to the run: `failed/<node-id>.out` now has a
  machine reader as well as a human one, and it earns its atomic-write
  discipline.
- One fence implementation, five call sites, one guard over the count — instead
  of a second hand-rolled nonce minter in a second package.
- `state.json` gains a machine-readable answer to "was this failure a verdict on
  the work?", which today requires reading `detail` as English.

**Negative / trade-offs**

- **Untrusted model text now enters a node's paid prompt.** That is a real trust
  boundary change and the fence is what holds it. It is also, honestly, a
  boundary this project already crossed less carefully: `{{ feedback.<id> }}`
  inlines a full model reply into a body node's prompt with **no fence and no
  length bound at all** (`handoff.resolveLocked`). That defect predates this
  change and is **not fixed here** — fencing it changes what body nodes are
  shown, which is a behavioural change to a shipped feature with its own ADR,
  and mixing it into this commit would make both unreviewable. It is the next
  thing to do.
- **Every retry attempt costs more.** Up to ~2k tokens of quoted reply, once per
  attempt. Bounded and flat, but not free.
- **No measurement.** Whether feeding the reply back actually raises retry pass
  rates is unmeasured: this repo has no harness for it (`make smoke` is a manual
  single graph), and building one was not in this change's scope. The claim made
  here is narrower and does not need the harness — that re-running a node with
  no knowledge of its own rejected attempt is strictly less informed than
  re-running it with one.
- **Prompt-keyed test fixtures broke.** A retry's invocation no longer carries
  the node's prompt verbatim, so any fake keyed by prompt sees an unknown key.
  `schedule.RetryQuoteHeader` is exported as the seam to cut at, so no fixture
  transcribes the wording.
- **Two imprecisions are accepted**, both requiring a disk failure to reach: a
  node whose checks *passed* but whose `PersistOutput` failed is recorded
  unjudged (correct), so it is never quoted; and a reply kept for a human on any
  other unjudged path is likewise never quoted. The gate is `Judged`, so the
  sentence "the check did not accept it" is true wherever the quote appears.

## Alternatives considered

**Do nothing (steelmanned).** The argument is genuinely strong and it is the
reason §1 has the shape it does:

1. A failed node's reply is a reaction to *the check*. Telling the model that
   `result_matches: "^PASS"` rejected it teaches the cheapest pass, which is to
   emit `PASS`. DESIGN.md records exactly that reply failing exactly that kind
   of check.
2. Retry's original purpose is transient faults (`run_error`, `output_error`),
   where the previous reply is noise.
3. Untrusted text into a trusted prompt, in a project whose existing
   `{{ feedback }}` path is unfenced.

Where it breaks: (1) argues against quoting the *check*, not against quoting the
*reply* — so the check is not quoted. (2) is answered by gating on
`isJudgmentFailure`, which excludes every transient cause. (3) is answered by
the fence and the bound, and its unfenced sibling is named as outstanding work
rather than used as cover.

**Accumulate every attempt.** Rejected on cost (§2) and on trust surface. The
question "does the fourth attempt see three failures?" has one answer: no.

**Feed back the failure detail as well as the reply.** Rejected: the detail for
`result_matches` *is* the regex, and for `verify` it is the check command's
output. Both are the check's internals, which §1 withholds.

**Keep the fence inside `internal/coordinator` and mint a second nonce in
`internal/schedule`.** Rejected outright. Two hand-rolled implementations of a
security property diverge; the whole reason the fence is one function with one
doc comment and one count guard is so it cannot.

**Reconstruct the previous attempt from `~/.claude/projects/<session>.jsonl`.**
Rejected. `serve/transcript.go` already reads those files and shows exactly why
this is not the answer: claude's file, claude's schema, a best-effort parser
that skips any line it does not recognize, and the ~30-day GC ADR 0008 recorded.
RUN-FEED already calls the transcript a supplement, never a substitute.

**A cross-run findings store.** Rejected, on ADR 0008's own argument against
global mutable state — with a worse liveness story, because findings expire when
the code changes and nothing can detect that.
