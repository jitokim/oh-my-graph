# ADR 0028 — A feedback arc and its quote are one mechanism, and half of it loads clean today

**Status:** Accepted — implemented in the same PR (see "What implementation
found" below)

**Date:** 2026-08-17

## Context

ADR 0010 gave a graph a bounded repair loop, and it is a **pair**:

- `feedback: { rerun: R, max: N }` on a declarer `D` — the arc, which says
  *what re-runs when D fails for a judgment cause*;
- `{{ feedback.D }}` somewhere in the body — the quote, which says *where the
  re-run reads what was wrong*.

Neither half is useful alone, and until this ADR **either half loaded clean
alone**. `graph.validateFeedback` checks the arc's topology (seven rules:
backward target, bounded max, no gate in the body, no side exit, disjoint
bodies, session parents). `graph.validateFeedbackPlaceholders` checks that a
token that *is* written can ever resolve (right body, no filter, real
declarer). `graph.LintFeedbackReach` checks that the loop can reach the
producers the declarer fans in from. Every one of those judges a half that is
present. Nothing asks whether **both** halves are.

### The specimen, measured

Run `20260816-163759.091162000-1`, on the released v0.9.0 binary, three
parallel lanes of a two-node loop spliced from one fragment. Lane A, as it sits
in that run's `state.json`:

```yaml
- id: qa-a/build
  prompt: |
    Write a file at /tmp/t090/out-a.txt.
    Its ONLY content must be one word, chosen by this rule:
      - if a FEEDBACK section appears below, write: alpha
      - if there is no feedback section, write: draft
    Then reply with the bare word WROTE on the first line.

- id: qa-a/check
  depends_on: [qa-a/build]
  success_check:
    verify: { command: 'test "$(cat /tmp/t090/out-a.txt)" = "alpha"' }
  feedback: { rerun: qa-a/build, max: 1 }
```

The loop ran **exactly as designed**. `qa-a/check` failed its verify, the
engine wrote `feedback/qa-a~check.out`, and re-ran `qa-a/build`. And nothing
was repaired, because `qa-a/build`'s prompt never contains
`{{ feedback.qa-a/check }}`. The prompt *branches on feedback that can never
arrive*: no FEEDBACK section ever appears below, so the model wrote `draft` a
second time and the second round failed identically to the first.

```
qa-a/build   PASS   feedback round 1/1
qa-a/check   FAIL   feedback exhausted after 1 round of qa-a/build → qa-a/check
```

Twice the money, the same result, and `lint` said nothing — the graph is valid
under every rule above, because each rule judges a half.

**Where this sweep does and does not stand.** It is printed by `lint` and
`run --dry-run`, and by neither `run` nor `auto`'s own execution path
(`warnAdvisories` has exactly two callers: `cmd/oh-my-graph/lint.go` and
`cmd/oh-my-graph/dryrun.go`; `internal/graph/fragment.go` states the standing
convention outright — "`run` does not run the lint sweeps"). So the specimen
run, invoked as a plain `run`, would still have paid twice and still said
nothing after this change: what the sweep buys is that an operator who lints —
or a CI job that does — is told before the spend, not that the engine now stands
between the author and the bill. The one path where a blind loop is caught
without anyone choosing to look is auto mode, where decision 5 makes it a
refusal.

### Why this is invisible rather than merely missable

The three sibling failures this package already sweeps for are all *silent by
construction*, and this one is the quietest of them:

- the arc is correct, so no validator has anything to say;
- the token is absent, so the token validator has nothing to look at — an
  unwritten placeholder is not a malformed one;
- at run time a feedback payload that is never read produces **no error at
  all**. It is written to disk, and nobody opens it. (Compare a *misplaced*
  token, which ADR 0010 made a load error precisely because it would resolve
  to the empty string silently, forever. The unwritten token is that same
  silence, one step earlier.)
- the ledger prints `feedback round 1/1`, which reads like the mechanism
  working.

The only observable is the bill — and on a plain `run` it stays the only one
after this ADR, for the reason given above. What changes is that the fault is
*sayable* before the spend, to whoever asks.

### What the corpus says, measured 2026-08-17

Full method and every number's derivation:
[docs/measurements/0028-feedback-quote-corpus.md](../measurements/0028-feedback-quote-corpus.md).
Loaded through the shipped loader and parser — never grep — so ids and tokens
are the spliced ones that actually ran.

| corpus | graphs | feedback declarers | hits |
|---|---:|---:|---:|
| shipped `graphs/*.yaml` | 8 | **2** (`review-loop::review`, `backlog-batch::review-a`) | **0** |
| `oh-my-graph-hq/lanes/graphs/*.yaml` | 26 | 2 (byte-identical copies of the two above) | **0** |
| `~/.oh-my-graph/runs/*/state.json`, deduped by resolved graph | 201 distinct | **11** (3 planner-authored, 8 hand-written) | **3, all in one graph** |

**Noise: 0 of 3 — and the 3 are one defect counted three times.** They are the
`qa-a`, `qa-b` and `qa-c` lanes of one fragment cited three times in one graph,
in one run; the dedup key is the resolved-graph JSON, which collapses re-runs
but not lanes inside a graph. So the precision evidence is **one distinct
defective graph and one distinct control**, not three independent confirmations.
That is enough to ship an advisory and not enough to call the predicate
corpus-validated, and the caveat belongs on the number rather than three
paragraphs after it.

The control: run `20260816-163954.329528000-1`, three minutes later, is the same
graph with the token added, and the sweep is correctly silent on it. So the
predicate separates the broken loop from its fix on real data, not on a fixture
— once.

**Three of the 11 run-corpus declarers were written by the planner**, not by a person (their
runs' `graph_source_path` is auto mode's own `graph.json`), and all three quote
their payload correctly. That split is what decides decision 5, and drafting this
ADR without it produced a false premise there; the measurement doc carries the
derivation.

The corrected shipped count is **2**, which matches the brief's guess and ADR
0027's own correction of the same figure. No shipped graph fires — pinned by
`TestLintFeedbackQuoting_ShippedGraphsAreClean`, which walks `graphs/*.yaml`
through `graph.LoadFile` and this sweep, so the claim fails in the test suite
rather than decaying quietly the first time a template or a cited fragment grows
a loop without the token.

## Decision

**Add a sixth advisory sweep to `internal/handoff`: for every node `D`
declaring `feedback: { rerun: R }`, if no node in the loop body other than `D`
itself quotes `{{ feedback.D }}` in its prompt, warn — on `R`, naming `D`.**

**And refuse the same shape in a planned graph**, where nobody reads a warning
(decision 5).

`handoff.LintFeedbackQuoting`, wired into the one `warnAdvisories` helper
`lint` and `run --dry-run` share, plus
`coordinator.validatePlannedFeedbackQuoting` reading the same predicate through
`handoff.FeedbackQuoteFindings`. Six decisions, each with its reason:

### 1. The warning goes on the rerun target, not the declarer

`D` is where the *fault is declared*; `R` is where the *fix goes*. A warning
that names the reviewer and asks the reader to work out that the missing line
belongs in the implementer's prompt is a warning that gets read twice. The
message names `D` in its text so the pair is legible from either end, and it
prints the exact token to paste.

### 2. Any body node except the declarer counts as quoting it

The brief's rule was "`R`'s prompt must contain the token". On a two-node loop
those are the same statement. On `build → refine → check` with `rerun: build`,
they are not: a token in `refine`'s prompt means round two genuinely reads the
findings and produces something different — the loop repairs, just not at its
first node — and warning on `build` there would be advice whose premise is
false.

The declarer is excluded on the opposite ground. It is the **judge**; its own
re-run repairs nothing. A loop where only `D` quotes the payload re-judges
unchanged artifacts while being reminded of its own prior findings, which is
not a repair round — it is asking a reviewer to change its mind on no new
evidence. That is the specimen's failure with an extra step, not an exemption
from it.

Measured, the two predicates are **indistinguishable on every corpus row**:
all 11 run-corpus declarers get the same verdict either way, because in the two
three-node bodies that exist (`dev-a → e2e-a → review-a`, `impl → docs →
verify`) the quote is at the rerun target anyway. So this is a judgement about
a shape the corpus does not yet contain, taken in the direction that cannot
produce a false accusation.

### 3. Only `prompt` is scanned

`validateFeedbackPlaceholders` permits the token in `cwd` and in a verify
command too. Neither is read by a **model**: a payload on a command line is
sweep five's finding (`LintVerifyInlining`, which says to move it to a prompt),
and a payload in a `cwd` is a path. This sweep's subject is precisely whether
the re-run's model can see what was wrong.

### 4. It matches with the runtime's own `placeholderPattern`

Not by formatting `"{{ feedback." + id + " }}"`. Two reasons, one of which the
specimen proves: the token that must be found is `{{ feedback.qa-a/check }}` —
a namespaced id the loader wrote during fragment splicing (ADR 0027) — and
whitespace inside a token is free. Matching with the pattern
`Handoff.Interpolate` substitutes with means **what this sweep counts as a
quote and what the runtime actually splices cannot drift apart.** A sweep that
only worked on hand-written ids would have missed the very run that motivated
it, so both shapes are tested.

Sharing the pattern is necessary and not sufficient, and the first
implementation showed where: the pattern has a THIRD group, the `| inline`
filter, and reading only kind and reference counted `{{ feedback.D | inline }}`
as a satisfied quote. The runtime does the opposite — `resolveLocked` returns an
error on a filtered feedback token and the node fails — so that one token was
the sweep and the runtime disagreeing in the worst direction: the runtime
refuses to run the loop, the sweep calls it wired. `bodyQuotesFeedback` now
requires the filter group to be empty. Nothing can reach that guard today
(`graph.Validate` refuses the token at load and both callers sweep a parsed
graph), which is itself the thing pinned:
`TestLintFeedbackQuoting_NeverSeesAFilteredFeedbackToken` asserts the loader's
refusal, so if that ever relaxes the guard becomes load-bearing with a test
saying so rather than silently.

### 5. Advisory for a hand-written graph, a plan refusal for a planned one

The first draft of this decision said "only a person can write what it condemns,
because `coordinator.validatePlannedNodes` refuses a planner-authored
`feedback:` outright". **That is false, and it was false when it was written.**
The coordinator *constrains* a planned `feedback:` — `validatePlannedNodeFeedback`
bounds `max`, `validatePlannedFeedbackReach` refuses a mis-aimed arc, and the
field-disposition list records it as constrained, never rejected. Worse for the
claim, the planner is **instructed to write exactly this pair**:

> Declare a feedback arc on the reviewing node instead: `"feedback": {"rerun":
> …, "max": 2}` … and have the implementing node's prompt read
> `{{ feedback.<reviewing-node-id> }}`

— one sentence, two halves, of which only the first was machine-checked. The
corpus confirms the planner acts on it: 3 of the 11 run-corpus declarers
measured are planner-authored. So the blind loop's worst instance — no human reviewer, no
`lint` (the coordinator never calls `warnAdvisories`; nobody lints a graph
`auto` planned and ran in the same breath), money already committed — was the
one case this sweep left uncovered.

So the standing is two-part, and each half has its own reason:

**Advisory for a hand-written graph, never a load error.** Not because no
machine writes the shape, but because an **absent** token has a legitimate
reading where a **misplaced** one has none (ADR 0010 made that an error for
exactly that asymmetry). A loop whose re-run reads the *repository* rather than
the reply — a formatter re-run after a linter judged the tree — needs no
payload. That author should see the line once and move on.

**A plan refusal for planner output** (`coordinator.validatePlannedFeedbackQuoting`,
shipped here). This is the escalation the ADR's severity ladder argues for and
the first draft never weighed, and its precedent sits one function above the
false claim: `validatePlannedFeedbackReach` escalates `graph.LintFeedbackReach`
from advice to a refusal, for a fault of the same class from the same author.
The predicate is not recomputed — `handoff.FeedbackQuoteFindings` is the single
definition and the disposition lives at each caller, the same split
`LintFeedbackReach` already has.

Two things make this escalation cheaper than that one, and they are worth
stating because they cut in the opposite direction to its evidence:

- **The warrant is weaker.** The reach refusal answered a *measured* planner
  failure (#118, ~$14 of a $42 run). This one has none: all 3 planner-authored
  arcs in the corpus quote correctly. It guards a shape the planner can write,
  not one it has been caught writing.
- **The price of being wrong is lower than anywhere else in that validator.** A
  refused plan buys one corrected re-plan (`repair.go`) carrying the refusal's
  text, and the correction it demands — one placeholder in the rerun target's
  prompt — is **harmless even when the refusal is wrong**, because the feedback
  namespace resolves to empty on the first pass by design. The repository-reading
  loop above loses nothing by carrying a token it ignores. Contrast the reach
  refusal, whose wrong correction pulls a context node into the loop and pays for
  it every round — which is why that one fires only when it can name a covering
  target, and why this one needs no such weakening.

The refusal names the declarer, the rerun target and the exact token, and tells
the planner not to make the work conditional on a feedback section appearing —
without that clause, a planner "fixing" the refusal can write the specimen's own
prompt back.

### 6. It lives in `internal/handoff`, not next to `LintFeedbackReach`

`LintFeedbackReach` is pure topology — `depends_on` and the between-set — and
belongs where the graph is. This one reads **prompt text through the
interpolation pattern**, which is handoff's subject and where the other five
prompt-text sweeps and `placeholderPattern` already live. Putting it in
`internal/graph` would mean a second copy of the token pattern in the package
that already warns (in `feedbackTokenPattern`'s own docstring) that the two
patterns move together or not at all.

### No runtime behaviour changes

`internal/schedule`'s feedback path, the payload file, the ledger's round
accounting and the arc's semantics are untouched. Nothing about how a graph
*runs* changes; what changes is what `lint`/`run --dry-run` print, and what
`auto` accepts as a plan (decision 5).

## Alternatives considered

**Make it a load error, like a misplaced token.** Rejected. ADR 0010 made the
*misplaced* token an error because it is never right — an unresolvable
placeholder has no legitimate reading. An *absent* token has one (decision 5),
and a load error would break working hand-written graphs to catch a mistake
that costs one extra round. The severity ladder in this repo is: refuse what a
machine writes, advise what a person writes — which is what decision 5 now does
at both ends, once the false premise about which of the two writes this shape
was corrected.

**Warn on the declarer instead.** Rejected — see decision 1. The finding is
about `R`'s prompt; the fix is an edit to `R`'s prompt.

**The brief's literal rule (`R`'s prompt specifically).** Rejected as very
slightly too wide — see decision 2 — while noting honestly that the corpus
does not distinguish them, so this is reasoning, not measurement.

**Scan `cwd` and verify commands too.** Rejected — decision 3. It would
double-report the one shape sweep five already condemns, with contradictory
advice.

**Count `{{ artifacts.D }}` in a body node as an equivalent wiring.** Rejected
as unsound, and this is worth recording because it *looks* equivalent: a
re-run reading the declarer's persisted reply is the same information. But it
does not work on the first pass — `D` has not run, so the token raises an
`InterpolationError` and fails the node — and the feedback namespace exists
precisely because it resolves to empty instead. So a graph wired that way is
broken in a different, louder way, and treating it as a quote would suppress
a true finding.

**Leave auto mode alone — advisory everywhere.** Rejected, and it should have
been the first alternative on this list rather than an assertion that there was
"no auto-mode counterpart to add" (see decision 5 for the false premise that
produced it). The argument for it is real: no planner failure of this shape has
been measured, and a refusal on an unmeasured shape is a prediction. What
defeats it is that the alternative is not "warn instead" but "say nothing" — the
coordinator never calls `warnAdvisories`, so silence is the only other option —
and that the correction a wrong refusal asks for costs nothing. A prediction
whose false-positive price is one re-plan and one ignorable placeholder is worth
making; that is the same trade `validatePlannedNodeFeedback`'s `max` ceiling
already makes with no measured planner failure behind it either.

**Refuse the round at run time, when the arc fires.** Rejected. Everything
this check needs is knowable from the file, and a run that halts halfway
through on something `lint` could have said before a cent was spent is a worse
trade than a warning.

**Report it at run time, without refusing anything** — a ledger or run-feed note
when a feedback round fires and no re-run node's `Interpolate` consumed
`h.feedback[declarer]`. **Out of scope here, and NOT the same alternative as the
refusal above**, which the first draft folded it into by answering "everything
is knowable from the file". Knowable is not the same as *told*: the sweeps are
printed by `lint` and `run --dry-run` only, so on the path where the money is
actually spent — a plain `run` — nothing says anything, before or after this
change. A run-time observation has properties no static rule can match: it has
**zero false positives** (it reports what did happen, so the repository-reading
loop of decision 5 is never accused), it needs no prediction about
comprehension, and it fires on the run that is spending. What keeps it out of
this ADR is scope, not merit: it is a runtime behaviour change touching the
ledger and the run-feed consumer contract, it needs its own decision about
whether a note becomes a halt, and the static sweep is what a graph author can
act on *before* paying. It is the natural successor to this sweep, not a
rejected idea.

**Auto-append the payload to `R`'s prompt when no token is present.**
Rejected, and firmly: it would silently rewrite a prompt the user pays for and
reviewed. ADR 0010 made the quote explicit on purpose — where the payload
lands in a prompt is a real authoring decision (the shipped graphs put it last,
after the verdict contract, precisely so it cannot displace the instructions).

**Extend `LintFeedbackReach` instead of adding a sixth sweep.** Rejected. They
answer different questions about the same arc — *is the loop aimed at the right
node* versus *can anything in it see the findings* — and a loop can be both
mis-aimed and blind. Merging them would force one message to state two
independent faults. Their subjects are not wholly disjoint, though: the
topological false negative in the failure modes below (a quote on a body node
whose output the declarer never reads) is reachability reasoning, and that is
the one seam where a future rule might belong to either sweep.

## Failure modes and compatibility consequences

**False positives, by construction.** A loop that repairs from the repository
state rather than from the payload is warned and should not be. Cost in a
hand-written graph: one line of advice, ignorable, on a shape the corpus
contains zero of. Cost in a **planned** graph, since decision 5: one re-plan,
after which the loop carries a placeholder it does not read — which is why the
refusal is affordable at all. This is the exemption decision 5 declines to
encode, because there is no way to tell it apart from the specimen without
reading the prompt's intent.

**No way to silence it, and it is the sixth of six.** The exemption decision 5
admits — the loop that repairs from the repository — has exactly one remedy
available to its author: read the line, every `lint`, forever. No advisory in
this package is suppressible per node, per graph or per sweep. For one sweep
that is a fair trade against the alternative (a suppression syntax is a schema,
a validator and a way to silence a true finding); across six accumulating
sweeps it is a question that gets harder each time, and this ADR records it
rather than deciding it silently a sixth time. The next sweep that admits a
legitimate exception inherits the question with the count at seven; if
suppression is ever added, it should be added once for the whole package, not
per sweep.

**False negatives this sweep accepts.** It sees a token, not comprehension. A
prompt that quotes `{{ feedback.D }}` in a place the model will ignore, or
under an instruction that overrides it, passes. A prompt that says "if a
FEEDBACK section appears below" and *does* carry the token but drops it in a
paragraph nobody reads, passes. Static analysis stops at "the payload is in the
text the model receives"; whether the text works is what the round itself
measures.

**The other false negative is topological, and it is the one seam where this
sweep's subject touches `LintFeedbackReach`'s.** Decision 2 counts a quote in
any body node, and every body node is a `depends_on`-ancestor of the declarer —
but ancestry is not data dependency. A quote sitting on a body node whose output
the declarer's verdict never reads satisfies the predicate while repairing
nothing: the loop re-runs, one node genuinely reads the findings, and the node
whose artifact is actually being judged is untouched. That is reachability
reasoning of exactly the kind `LintFeedbackReach` already does over producers,
and it is why the "the two sweeps are independent" claim elsewhere in this ADR
is about their *findings*, not about their subjects being disjoint. Not closed here: deciding
which body node's output a verdict reads means reading prompts for intent, which
is the line this package's static sweeps stop at.

**A spliced node's fix lives in the fragment, not the graph.** The warning
names `qa-a/build` because that is the node that runs, but a citing graph cannot
reach *into* a spliced loop to edit it (ADR 0027: `{{ feedback.qa-a/check }}` in
a citing graph is refused as a namespaced id no author may write, and
`{{ feedback.check }}` dies at `validateFeedbackPlaceholders`) — so the edit
lands in the fragment file, and it fixes every citation at once. That is what
actually happened to the specimen: the follow-up run's fix was one line in the
fragment body. The narrow claim is the one that holds: **a citing graph cannot
reach inside a loop splice.** It is not true that a citing graph can never
influence a spliced prompt at all — `substitutions:`/`with:` exist precisely so
it can, and a fragment that declares a substitution point inside its repair
prompt can be fixed (or broken) at the citing site. The message deliberately
does not guess which file to open; the id it names is enough to find it, and
guessing would be wrong for the hand-written majority.

**A loop fragment cannot be linted at all, and the warning reaches the wrong
author.** Measured: `oh-my-graph lint graphs/fragments/repair-round.yaml` exits 1
with `invalid timeout "{{ with.review_timeout }}"` — a fragment file is not an
entry graph, its unbound substitutions are not valid field values, and no sweep
ever runs on it. A loop fragment is therefore judged only through a graph that
cites it, which inverts the ownership: the citing author gets the warning and
cannot fix it — not unless the fragment happens to have declared a substitution
point inside that prompt (previous point) — while the fragment author, who can
fix it once for every citation, never sees it unless they happen to cite their
own fragment from a graph and lint that. Meanwhile the defect is replicated into every
citation, which is the specimen's exact shape: one fragment, three lanes, three
identical hits.

Consequences, stated rather than fixed here:

- **Who is expected to lint a published fragment:** its author, by linting a
  graph that cites it — there is no other way today. For this repo that is
  automatic, since `TestLintFeedbackQuoting_ShippedGraphsAreClean` walks
  `graphs/*.yaml` with `use:` resolved, and `adr-driven-dev.yaml` cites
  `repair-round` twice. A fragment published outside a repo whose graphs are
  swept has no such backstop.
- **The corpus inherits the hole.** "shipped `graphs/*.yaml`, 8 entry graphs"
  excludes `graphs/fragments/*.yaml`, and the measurement's original
  parenthetical ("excluded by the loader, not by hand") read as rigour where it
  was blindness. The 0 still stands — parsed, not grepped, all five shipped
  fragments declare no `feedback:` — but it means "no shipped fragment declares
  an arc today", not "fragments were swept".
- **The fix is a linting mode for fragments, not a rule change**: judging a
  fragment on its own needs a way to load one with its substitutions unbound.
  That is its own decision (it touches `LintLoadFile`, not this sweep), and this
  ADR does not take it.

**Interaction with `LintFeedbackReach`.** Both can fire on one arc, and are not
deduplicated. They are different faults with different fixes (re-aim the arc /
quote the payload), and an arc can carry both. The two corrections COMPOSE:
re-aiming an arc at an earlier target keeps the old rerun target inside the
body, so the token the quoting refusal asks for stays valid wherever it was
pasted. Nothing has to be applied in an order.

What does not compose is their SIZE, and review caught that before merge. Both
refusals are graph-level, both lead the issue list `validatePlannedNodes`
builds, and both scale with the number of declarers — so on a graph with two
faulty arcs they crowd each OTHER rather than being crowded by the per-node
refusals the ordering was written to outrank. Measured on the two-lane fixture
(`twoBrokenLanesSpec`), and pinned by
`TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize` so these numbers keep an
address: 677 + 677 for reach and 592 + 592 for the quoting refusal as it was
written then, one sentence per arc — **2541 bytes** joined, against what was a
2000-byte `maxIssuesInPrompt`. Compacted (below) the same pair renders **1997**,
inside 2000 by three bytes, which the shortest per-node refusal beside it
spends; the specimen graph of this ADR, a three-lane one, renders 2735 and
would have blown the old budget on its own. Past the cut the repair prompt lost a whole family silently, and the
one re-plan a refused plan buys (ADR 0011's bound) was spent on a fault it was
never told about. Three changes bound it, all in this branch:

- the quoting refusal is ONE sentence for every blind arc in the graph, not one
  per declarer — the ~530-byte diagnosis is shared and only the ids repeat
  (four arcs: 761 bytes, against 2368 before);
- `maxIssuesInPrompt` is sized from the two families rather than picked: 3000
  holds three declarers faulty both ways (2735) with room for per-node refusals
  beside them;
- past the budget, `issuesForPrompt` drops WHOLE refusals from the tail and
  states how many it dropped, instead of cutting one mid-sentence and losing
  the rest without saying so. Half a refusal names a node and stops before the
  correction, which is an instruction to guess.

**On a planned graph it is a refusal, not a warning.** An earlier draft of this
section claimed the sweep "never fires on a planned graph" because the
coordinator "refuses a planner-authored `feedback:` entirely". It does not, the
planner is asked for these arcs, and 3 of the 11 run-corpus declarers measured are its own
— see decision 5, which takes the escalation that error hid. The failure mode
that remains is the refusal's own: a planned loop that legitimately repairs from
the repository is refused rather than warned, and pays one re-plan for it. A
planner that answers the refusal by making its work conditional on a feedback
section is the specimen written back, which is why the refusal's sentence names
that trap explicitly.

**Compatibility.** Additive: no exit code changes for `lint`/`run --dry-run`
(advisory lines only), no schema changes, no changes to how a graph runs.
Nothing in the run feed, ledger, snapshot or dashboard is touched. The one
behaviour change is in `auto`: a plan whose feedback arc nothing quotes is now
refused and re-planned once, so a goal that previously produced a blind loop now
either produces a wired one or fails to plan. That is the intended trade, and it
is the only place a user can observe this ADR without running `lint`.

## What implementation found

Three things worth recording — the third one found by review, after the first
draft of this ADR was written.

**The corpus contains its own control — once.** The broken specimen and its
repaired re-run are three minutes apart in `~/.oh-my-graph/runs`, so the
predicate could be measured against a before *and* an after of the same graph
rather than against a fixture — 3 hits on the first, 0 on the second. That is
more than "it fires on nothing", which is what the fifth sweep could honestly
claim, and it is why this one ships as a detector rather than as documentation
with a test attached. The claim stops there: **one** defective graph and **one**
control, the 3 being three lanes of the same fragment citation rather than three
independent graphs.

**Drafting it produced the exact fault the repo's first rule is about.** The
premise of decision 5 — "the coordinator refuses a planner-authored `feedback:`"
— was written from memory of the sibling rule, was stated twice, and was
contradicted by a function one screen above the one it cited. It survived
because the ADR asserted a code fact without an address anyone re-checked, and
because the corpus was reported as 11 declarers without the one split
(planner-authored versus hand-written) that would have falsified it in a line.
Both are recorded here so the correction is the artifact, not just the corrected
text.

**The 288-run corpus is mostly unreadable through `runstate.Load`.** 261 of 288
snapshots on this machine are schema 2 and `Load` refuses them by design (they
cannot be resumed). The `graph` member is re-parseable JSON in both schemas, so
the measurement reads it directly and parses with `graph.Parse` — which is why
the denominator is 201 distinct graphs rather than the 25 a `Load`-based sweep
would have seen. Any future corpus pass over run history should do the same, or
it is silently measuring the last two weeks.
