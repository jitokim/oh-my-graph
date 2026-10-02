# ADR 0043 — A minor finding passes the gate and stays in the record; only a blocking one fires the arc

**Status:** Accepted. Written before its code; §5 lists the tests the
implementation owes, and each lands in the same PR.

**Where the addresses point.** Written against `9352a79`, the `main` this
branch (`feat/288-minor-verdict`) left. Every `file:line` below was read at
that commit. Code that does not exist yet is named by file **and** node or
function, because the line goes stale when the diff lands and the name does
not.

**Date:** 2026-10-03

**Issue:** [#288](https://github.com/jitokim/oh-my-graph/issues/288),
proposal (b). Proposals (a) — `{{ self.previous }}`, so a re-run reviewer
sees its own last round — and (c) — DESIGN.md stating that a CLEAN-only gate
has no convergence guarantee — shipped in #290 (CHANGELOG v0.15.0).

## 1. Context

### 1.1 A gate whose only passing value is silence

The shipped review fragments answer in a two-valued grammar
(`graphs/fragments/review-style.yaml:42-46`,
`graphs/fragments/review-security.yaml:34-38`):

- `CLEAN` — nothing worth changing;
- `FINDINGS:` — a list, worst first.

Their own `success_check` passes both (`review-style.yaml:72`,
`review-security.yaml:61`): a review fragment is **advisory** by default. A
graph that wants findings to stop its pipeline narrows the check to
``'^[*_`\s]*CLEAN\b'`` and pairs it with a `feedback:` arc. Three shipped places
do, and they are the whole of this ADR's gating surface:

| where | what narrows | what the arc re-runs |
| --- | --- | --- |
| `graphs/fragments/gated-lane.yaml:126` (`review`, cited by `backlog-batch` lane A) | the cited `review-style`'s check | `dev`, `max: 1` (`:140`) |
| `graphs/self-dev.yaml:173` (`review-verdict`) | a fan-in node over both reviews, plus a `verify` that greps each review artifact for `^CLEAN` (`:174-179`) | `dev`, `max: 2` (`:180`) |
| `TestAGatingReviewCarriesItsRecoveryArc` (`internal/graph/shipped_graphs_test.go:831`) | — it pins the pair on every node that splices a `review-*` fragment | — |

In all three, the only reply that passes is "I have nothing to say". A
reviewer with one naming nit has exactly one token for it — `FINDINGS:` — and
that token fails the gate and buys a full repair round: `(1 + max) × 3` nodes
in `gated-lane` (`:134-139`), `(1 + max) × 5` in `self-dev` (`:13-17`).

### 1.2 What #290 fixed, and what it left

DESIGN.md "A CLEAN-only gate need not converge (#288)" (`DESIGN.md:1276-1302`)
records the failure: a fresh-session reviewer re-reviewing a reworked diff
cannot tell "closed" from "never raised", finds a new nit per round, and the
loop runs to `max` and FAILs a lane whose real findings were all fixed.

(a) gave the reviewer its memory. It narrows the drift; it does not give the
gate a **severity floor**. Even a reviewer that remembers everything is still
forced to report a genuine but non-blocking observation as `FINDINGS:` — the
only token it has — and so to fail the gate on it. DESIGN.md says so in as
many words and names the fix this ADR takes: "A verdict with a third value —
`MINOR:` findings that pass and ride along, so only a blocking finding fires
the arc — is the structural fix" (`DESIGN.md:1299-1302`).

Whether (a) alone made the shipped loops converge **has not been measured**
(CHANGELOG v0.15.0, `CHANGELOG.md:35-38`). This ADR does not measure it
either, and does not claim (b) makes the measurement unnecessary (§7).

### 1.3 What the engine knows about a verdict: nothing

The engine judges a reply with one regex search (`success_check.result_matches`)
and an optional command (`verify`). It has no verdict vocabulary: `CLEAN`,
`FINDINGS:` and every other token exist only in YAML. The ledger has two
verdicts, `PASS` and `FAIL` (`internal/ledger/ledger.go:31-34`), and
`docs/LIMITATIONS.md:110-126` already records, as a decision, that "a PASS row
does not say *which* outcome passed" — "inventing one would mean the engine
parsing verdict semantics out of a regex it deliberately treats as opaque".

So the question "how is MINOR recorded" has a constraint before it has an
answer: any recording that needs the scheduler to recognize the word `MINOR`
reverses that decision.

### 1.4 Who else speaks the two-valued grammar

The grammar has been copied, inside this repo and outside it:

- **In this repo, outside the two fragments:** `graphs/fragments/repair-round.yaml:51-73`
  (`review`), and `graphs/adr-driven-dev.yaml:105-121` (`adr-review`) and
  `:319-334` (`round3`). All three pass both verdicts, and each feeds a node
  that addresses every item it is handed (`repair-round.yaml:82-83` "Address
  every finding"; `adr-driven-dev.yaml:128-129`, `:344-345`). None gates.
- **Outside it:** a user graph may (i) cite a fragment and keep its default
  check; (ii) cite it and narrow the check to `^CLEAN` with an arc — the shape
  `review-style.yaml:65` and `review-security.yaml:55` tell them to write;
  (iii) cite it and restate the old either-verdict pattern on the node; (iv)
  copy the fragment's two-valued prompt inline; (v) copy `self-dev`'s
  `review-verdict` and its `grep -q '^CLEAN'`; (vi) read a review artifact
  downstream with a prompt or command that branches on `FINDINGS:`.

Auto mode is not on this list: no planned node can cite a review fragment
today (ADR 0038's admitted set is empty, and names `review-style` as one of the
fragments that fails its precondition, `0038-…md:229-233`).

## 2. Decision

**The two single-node review fragments answer in a three-valued grammar —
`CLEAN`, `MINOR:`, `FINDINGS:` — classified by whether an item blocks a merge,
not by how small its fix is. A gating node accepts `CLEAN` and `MINOR:` and
fails only `FINDINGS:`, so only a blocking finding fires the arc. `MINOR:` is
recorded where every passing reply already is — the node's artifact in the run
directory — and surfaced where the shipped graphs already surface a review: the
PR body. The engine, the ledger and the run feed are not changed.**

### 2.1 The grammar

Both `review-style` and `review-security` offer exactly three tokens, as the
very first characters of the reply, with the existing "no emphasis, no
heading, no preamble; anything else goes AFTER the token" contract:

- **`CLEAN`** — you reviewed the whole diff and have nothing at all to raise.
- **`MINOR:`** — nothing you raise blocks a merge; followed by the items, worst
  first.
- **`FINDINGS:`** — at least one item you would not merge without fixing;
  followed by the blocking items, worst first. Minor items may follow them
  under a `Minor:` line.

The severity rule is stated in the prompt as **"judge each item by whether you
would merge with it unfixed, not by how small the fix is"**, with per-fragment
anchors: for `review-style`, a defect, wrong behaviour, or a changed behaviour
with no test is blocking, while naming, wording and simplification are minor;
for `review-security`, an exploitable issue is blocking, while hardening with
no concrete path to exploit is minor.

The verdict of a mixed reply is its worst item. There is no fourth token and no
"minor-only FINDINGS".

**The prompt still says nothing about the gate.** `gated-lane.yaml:105-110`
is the standing rule — "a reviewer told that findings will fail it is a
reviewer biased toward CLEAN" — and the same bias would push items from
`FINDINGS:` to `MINOR:`. The severity definition is about mergeability, which
is the reviewer's own question; what a citing graph does with the answer stays
invisible to it.

### 2.2 The severity ratchet across rounds

The `{{ self.previous }}` paragraph (`review-style.yaml:30-37`,
`review-security.yaml:22-29`) gains one rule and changes its last sentence:

- **An item you rated minor last round stays minor unless the rework changed
  the code it is about.** Without this, a re-run reviewer can promote last
  round's nit to a blocking finding and buy another round over code nobody
  touched — the #288 drift through a new door.
- "If every earlier finding is closed and nothing new meets that bar, the
  verdict is CLEAN" becomes "… the verdict is CLEAN, or `MINOR:` if only minor
  items remain open". A still-open minor item no longer forces a verdict that
  fails the gate.

The ratchet runs one way only. A blocking item stays blocking while it is
open; demoting it needs the same justification a new finding needs.

### 2.3 The patterns

Two spellings, one per disposition, both in the existing decoration class
(DESIGN.md "Verdict patterns"):

| disposition | pattern | `CLEAN` | `MINOR:` | `FINDINGS:` | anything else |
| --- | --- | --- | --- | --- | --- |
| advisory (the fragments' default) | ``'^[*_`\s]*(FINDINGS[*_`\s]*:\|MINOR[*_`\s]*:\|CLEAN\b)'`` | pass | pass | pass | fail |
| gating (with a `feedback:` arc) | ``'^[*_`\s]*(MINOR[*_`\s]*:\|CLEAN\b)'`` | pass | pass | **fail → arc** | fail |

`MINOR` carries a colon like `FINDINGS`, and the class sits on both sides of
it, so `**MINOR:**` and `**MINOR**:` both match. `MINOR` without a colon,
`Minor:`, and `MINORITY:` do not: the match is case-sensitive and the colon is
part of the token, as it already is for `FINDINGS:`.

A malformed reply — neither token, an interim "still reviewing", a token on
a later line — fails **both** patterns, exactly as today. On a gating node a
`result_mismatch` is a judgment failure and fires the arc; this ADR does not
change that, and `TestAGatingReviewCarriesItsRecoveryArc`'s comment already
names its cost (`shipped_graphs_test.go:804-809`).

### 2.4 The three gating places

**`gated-lane`'s `review`** narrows to the gating pattern instead of
`^…CLEAN\b`. Nothing else in the fragment changes: its `pr` node already quotes
`{{ artifacts.review | inline }}` into the PR body (`gated-lane.yaml:146-148`),
so a lane that passes on `MINOR:` opens its PR with the minor items in it.

**`self-dev`'s `review-verdict`** becomes three-valued itself, and its two
halves move together:

- **the prompt** offers `CLEAN` (both reviews start with `CLEAN`), `MINOR:`
  (neither starts with `FINDINGS:` and at least one starts with `MINOR:` —
  followed by every minor item, verbatim, under `Security:`/`Style:`) and
  `FINDINGS:` (either starts with `FINDINGS:` — followed by every **blocking**
  item, verbatim, labelled, worst first);
- **the `verify` command** accepts a review whose stripped head starts with
  `CLEAN` **or** `MINOR:` (`grep -Eq '^(CLEAN|MINOR:)'` in place of
  `grep -q '^CLEAN'`), and prints only the reviews that do not. So a model
  that answers `CLEAN` over a `FINDINGS:` review still fails here, and a model
  that answers `FINDINGS:` over two `MINOR:` reviews fails `result_matches`
  first — a false repair round is still possible from the model's side, never
  a false pass.

**The arc carries what fires it.** A `FINDINGS:` reply carries the blocking
items only; minor items are not the repair round's business and are not added
to `dev`'s payload. They are not lost: they remain in each review's artifact,
and the reviewer's own `{{ self.previous }}` carries them into its next round,
where the ratchet (§2.2) keeps them minor. When `verify` is what fails, the
payload is its output, which prints the head of each open review — and that
head may include a `Minor:` tail. Accepted: a few extra lines in a payload
`dev` was going to read anyway.

**`repair-round` is not a gating place**, although #288 lists it beside the
other two, and it stays two-valued. Its review passes both verdicts
(`repair-round.yaml:73`), a citing graph cannot narrow it (a multi-node `use:`
declares wiring only — ADR 0027), and its apply "addresses every finding"
(`:82-83`). A severity label there changes nothing about what happens next,
while it would hand the apply a new token to interpret and invite it to skip
items it is today told to address. The same holds for `adr-driven-dev`'s two
inline reviews. Both stay as they are, and are the in-repo proof of §6's
compatibility claim (iv): the two-valued grammar remains valid.

### 2.5 Advisory callers

`dev-review-pr` and `backlog-batch` lane B inherit the widened advisory
default unchanged. Both already put each review artifact in a PR body
(`dev-review-pr.yaml:123-130`, `backlog-batch.yaml:273-281`), so `MINOR:`
reaches the human the same way `FINDINGS:` does. Their comments that say the
fragment passes on "`CLEAN` and `FINDINGS:`" (`dev-review-pr.yaml:76-77`,
`backlog-batch.yaml:86-97`) are updated to name all three. The comments in
both fragments that tell a caller how to gate (`review-style.yaml:53-71`,
`review-security.yaml:45-60`) name the gating pattern of §2.3, not `^CLEAN`.

### 2.6 How MINOR is recorded and surfaced

- **Recorded: the node's artifact.** A `MINOR:` reply passes, so it is
  persisted as `<run-id>/<node>.out` like any passing reply (`handoff:
  artifact` on both fragments). That is the opposite of a gating `FINDINGS:`,
  which fails and writes no `.out` (`docs/LIMITATIONS.md:141-147`). In
  `self-dev`, `review-verdict.out` additionally restates the minor items; the
  record is the reviews' own artifacts, the restatement a convenience — a
  verdict model that answers `CLEAN` over a `MINOR:` review passes, and the
  items are then in `review-*.out` and the PR body only.
- **Surfaced: the PR body**, in every shipped graph that cites a review
  fragment (§2.4, §2.5), because every one of them ends in a PR node that
  inlines the review.
- **Not surfaced: the ledger.** The row is `PASS`, as it is for an advisory
  `FINDINGS:` today. The gap in `docs/LIMITATIONS.md` "A PASS row does not say
  which outcome passed" is widened to say so for gating reviews too: on a
  gating review a PASS now means `CLEAN` **or** `MINOR:`, and only the
  artifact says which.

### 2.7 What does not change

- **No engine code.** `internal/schedule`, `internal/ledger`, `internal/runfeed`,
  the snapshot, the graph schema and every exec seam are untouched.
- **No new graph field.** Everything above is prompt text, a regex, and one
  shell command in a YAML file.
- **The two-valued grammar is not deprecated.** A node whose prompt offers two
  tokens and whose pattern accepts those two is as correct after this ADR as
  before it.

## 3. Alternatives considered

### 3.1 Record MINOR in the ledger

A `PASS (minor)` cell, or a DETAIL note, for a pass whose reply starts with
`MINOR:`. Rejected, for the reason `docs/LIMITATIONS.md:124-126` already gives
for `WITHHELD` and `UNSETTLED`: the scheduler would have to recognize a word,
and the engine treats verdict tokens as opaque on purpose. A token
vocabulary in Go would be a second definition of the grammar beside the YAML,
free to drift from it, for one fragment family.

The general form — a new `success_check` field naming a pattern whose match
annotates a PASS — is the honest version and is **not** rejected on merit. It
is schema growth with its own questions (the snapshot, the run feed's consumer
contract, `show`, the dashboard, and how three existing two-valued
alternations would use it), and nothing measured says a minor item has been
lost for want of it: every shipped citer already puts the review in a PR body.
It is the follow-up if §7's second clause fires.

### 3.2 Spell the third value so old patterns accept it

`CLEAN with minor:` or `CLEAN (minor):` — a token starting with `CLEAN`, so
every `^…CLEAN\b` gate and every `grep '^CLEAN'` written against the old
grammar passes it with no edit. The most compatible option, and rejected for
that reason: it makes every user's gate **more lenient without their
consent**. An author who narrowed to `CLEAN` chose "nothing at all"; this
spelling would silently start passing work with items in it, and `CLEAN` would
mean two things in every artifact, PR body and `grep` that reads it. §6 keeps
every old gate exactly as strict as it was; this would loosen them all at
once, invisibly.

### 3.3 Count findings instead of grading them

Pass when the list is short (`FINDINGS: 1`). Rejected: one blocking defect is
one item, and five naming nits are five. Count measures volume; the question a
gate asks is whether to merge.

### 3.4 Let the gate grade instead of the reviewer

Keep the reviewer two-valued and give the gating node (or `review-verdict`)
the job of deciding which findings block. Rejected: it moves the severity call
to a model that did not read the diff — `review-verdict` is told explicitly not
to review it (`self-dev.yaml:155-157`) — and in `gated-lane` there is no
second node to move it to without adding one to every round.

### 3.5 Widen `repair-round` and `adr-driven-dev` too

Uniformity: one grammar in every shipped review. Rejected in §2.4: no gate
reads those verdicts, and every consumer of them already addresses every item.
A third token there is cost with no gate to spend it on, plus a new chance for
an apply to skip what it is told to fix.

### 3.6 Default the fragments to gating

Make the fragments' own check the gating pattern, now that minor items no
longer fail it. Rejected: a fragment cannot carry the `feedback:` arc (ADR
0013), so a gating default would be the "narrowed check with no arc" that
`TestAGatingReviewCarriesItsRecoveryArc` refuses — a reviewer that did its job
failing the run.

### 3.7 Measure (a) first, then decide (b)

Defensible: (a) might already make the loops converge, and then (b) is a
grammar change with no failure behind it. Not taken, because (b) answers a
different failure. (a) stops the reviewer re-reviewing as a stranger; (b) stops
a reviewer that *remembers correctly* from having to fail the gate over an
item it would itself merge. The second is a property of the two-valued
grammar, not of memory, and DESIGN.md already names it as the structural fix.
Measuring (a) stays owed either way (§7).

## 4. Implementation outline

Owned by the same PR, in this order:

1. `graphs/fragments/review-style.yaml`, `review-security.yaml`: the
   three-token prompt (§2.1), the ratchet (§2.2), the advisory pattern (§2.3),
   the caller comments (§2.5).
2. `graphs/fragments/gated-lane.yaml`: `review`'s gating pattern and comment.
3. `graphs/self-dev.yaml`: `review-verdict`'s prompt, pattern and `verify`
   command (§2.4); the header comment's "a FINDINGS: from either reviewer".
4. `graphs/dev-review-pr.yaml`, `graphs/backlog-batch.yaml`: comments only.
5. The golden `internal/graph/testdata/golden/*.resolved.json` files that
   resolve the changed fragments (`dev-review-pr`, `self-dev`,
   `backlog-batch`) are regenerated; `adr-driven-dev`'s must **not** change
   (§2.4), and that it does not is part of the check.
6. DESIGN.md "Verdict patterns": the three-valued review grammar and its two
   patterns beside the existing examples; "A CLEAN-only gate need not
   converge" ends on what this ADR shipped instead of "left as a follow-up".
7. `docs/LIMITATIONS.md` "A PASS row does not say which outcome passed":
   the gating-review sentence of §2.6.
8. `changelog.d/288-minor-verdict.md` (ADR 0042), under `### Changed`, naming
   the compatibility cases of §6 that need an edit.

## 5. Tests the implementation owes

Each verdict class is exercised as a real reply would arrive — bare, with
emphasis on either side of the colon, and after leading blank lines.

1. **The fragments' advisory pattern** (resolved through `graph.LoadFile`, not
   read from the YAML text): `CLEAN`, `**CLEAN**`, `MINOR: - x`,
   `**MINOR:** - x`, `**MINOR**: - x`, `FINDINGS: - x` pass; malformed replies
   fail — `Still reviewing`, `MINOR - x` (no colon), `Minor: - x`,
   `MINORITY: x`, `## MINOR:`, a verdict on line 2, the empty reply.
2. **`gated-lane`'s gating pattern**: `CLEAN` and every `MINOR:` spelling
   pass; every `FINDINGS:` spelling fails; the same malformed set fails.
3. **`TestAGatingReviewCarriesItsRecoveryArc` grows a third reply.** Beside
   `findingsVerdict`, a `minorVerdict` (`**MINOR:**\n\n- …`). Every node that
   splices a `review-*` fragment must partition the three replies into one of
   the two dispositions of §2.3 and no other: advisory (all three pass, no
   arc) or gating (`CLEAN` and `MINOR:` pass, `FINDINGS:` fails, arc
   present). A gating node that fails `MINOR:` is refused with a message
   naming #288 — it re-runs the implementer over a nit — and an advisory node
   that fails `MINOR:` is refused as a reviewer offered a token its check
   rejects.
4. **`TestSelfDevGatesOnBothReviews`**: the pattern accepts `CLEAN` and
   `MINOR:` and rejects `FINDINGS:`.
5. **`TestSelfDevVerdictCommandJudgesTheReviewArtifacts`**, through the real
   `ShellVerifier`: exit 0 for CLEAN+CLEAN, CLEAN+MINOR, MINOR+MINOR,
   `**MINOR**:` with leading blank lines; exit 1 for FINDINGS+MINOR — printing
   the findings review and **not** the minor one; exit 1 for a review starting
   `MINOR - x` (no colon), `Minor:`, or empty. The existing cases stay.
6. **The ratchet and the grammar are in the prompt, before the verdict rule.**
   Both fragments' resolved prompts carry the three tokens, the "whether you
   would merge with it unfixed" rule and the minor-stays-minor rule, and
   `TestAGatingReviewSeesItsOwnPreviousRound`'s ordering assertion still holds
   (the `{{ self.previous }}` quote precedes "START the reply with exactly one
   of").
7. **The two-valued places are untouched.** `repair-round`'s `review` and
   `adr-driven-dev`'s `adr-review`/`round3` still reject a `MINOR:` reply and
   accept `CLEAN`/`FINDINGS:` — pinned, so a future sweep that "makes it
   uniform" has to change a test and say why.

No test spawns a model: patterns are judged by `regexp`, the command by
`ShellVerifier` against written files, the graphs by the loader — the
`FakeRunner` discipline of CLAUDE.md, with no runner needed at all.

## 6. Failure modes and compatibility

### Failure modes

- **Severity laundering.** The reviewer grades its own items, and a real
  defect called `MINOR:` passes the gate. This is the price of a severity
  floor, and the mitigations are bounded, not total: the rule is about
  mergeability rather than size (§2.1), the prompt does not tell the reviewer
  what the gate does (§2.1), and every shipped gating graph still puts the
  item, word for word, in a PR body a human reads before merge. A graph with no
  human downstream — one that merges itself on a passing gate — is trusting a
  model's grading, and should not use the gating pattern. `merge-shepherd`
  does not cite a review fragment, and nothing shipped does this today.
- **The ratchet can pin a mis-graded item.** A blocking item rated minor in
  round one stays minor while the code it is about is untouched — which,
  because it passed, it will be. The ratchet trades a second chance at
  grading for convergence; the item is still in the PR body.
- **`MINOR:` from a model on a two-valued node.** A prompt that offers two
  tokens can still get the third, now that it is in the fragments a model may
  have seen in context. It fails `result_matches` as any unoffered token does
  — loud, a `result_mismatch` FAIL with the reply saved under `failed/`.
- **Mixed replies that lead with the minor items.** A `FINDINGS:` whose first
  bullets are minor still fails the gate (the token decides), but the
  worst-first order is the prompt's to enforce, not the pattern's. The
  repair round then reads nits first. Cost: attention, not correctness.
- **A verdict model that says `CLEAN` over a `MINOR:` review** passes
  `self-dev`'s gate with `review-verdict.out` silent about the items (§2.6).
  They remain in the review artifacts and the PR body.

### Compatibility, by the user graph shapes of §1.4

| shape | after this ADR | direction |
| --- | --- | --- |
| (i) cites a fragment, default check | reviewer may answer `MINOR:`; it passes | same disposition (advisory) |
| (ii) cites a fragment, narrowed to `^…CLEAN\b`, with an arc | `MINOR:` fails and fires the arc — exactly what the same item did as `FINDINGS:` before | **no regression, no benefit**; widen to the gating pattern to get (b) |
| (iii) cites a fragment, restates the old either-verdict pattern on the node | a `MINOR:` reply is a `result_mismatch` **FAIL** | **breaks, loudly**; drop the override or add ``MINOR[*_`\s]*:`` |
| (iv) copied the two-valued prompt inline | unchanged: the prompt never offers `MINOR:` | none |
| (v) copied `self-dev`'s `review-verdict` (`grep -q '^CLEAN'`) over the fragments | a `MINOR:` review fails `verify` and fires the arc — today's strictness | no regression, no benefit; copy the new command |
| (vi) a downstream prompt or command branches on `FINDINGS:` in a review artifact | items that used to arrive as `FINDINGS:` may now arrive as `MINOR:` and miss the branch | **silent, toward leniency** |

Every row but two moves toward strictness or not at all, and (iii) fails
loudly on the first `MINOR:` it sees. **Row (vi) is the one silent change**,
and nothing in the engine can detect it — the branch is prose in a user's
prompt. It is named in the changelog fragment, with the fix (mention
`MINOR:` in the branch). No shipped graph has that shape: every shipped
consumer of a review artifact inlines it whole (§2.5).

No `lint` rule is added for (iii). Detecting it needs either regex inclusion
between a fragment's pattern and an override — not computable for regexes in
general — or a sweep keyed on the fragment name `review-*`, which would put a
fragment family's vocabulary into the engine's lint package (§3.1's objection
again). The failure it would predict is loud and costs one run.

**Golden files.** The resolved goldens of `dev-review-pr`, `self-dev` and
`backlog-batch` change, because the fragment prompts and patterns they inline
change. That is the review surface for this ADR's grammar, not churn.

**Auto mode.** Unaffected: no planned node can cite a review fragment (§1.4).

## 7. Falsification, and what #288 still owes

1. **Convergence of (a) is still unmeasured, and (b) does not change that.**
   The measurement #288 is open for — gating runs of `self-dev`/`gated-lane`
   since v0.15.0, counted by rounds to a passing verdict and by exhausted
   loops, with each round's `previous/<review>.out` read for re-raised versus
   new items — stays owed. With (b) in, it can also be split: an exhausted
   loop whose last review was `FINDINGS:` with only re-graded minor items is
   the ratchet failing; one with fresh blocking items is (a) failing.
2. **A minor item is found lost** — a `MINOR:` pass whose items reached no PR
   body and no human, in a shipped graph. Then §3.1's annotating field is
   owed, and "the artifact is the record" was not enough.
3. **Severity laundering is observed**: a merged PR whose review passed on
   `MINOR:` with an item a human later called blocking. Then the grading rule
   in §2.1 is the thing to change, before the grammar.
4. **Gating loops end on `MINOR:` no more often than they ended on `CLEAN`.**
   Then the severity floor is not what was missing, and (a)'s measurement is
   the whole story.
