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

The only observable is the bill.

### What the corpus says, measured 2026-08-17

Full method and every number's derivation:
[docs/measurements/0028-feedback-quote-corpus.md](../measurements/0028-feedback-quote-corpus.md).
Loaded through the shipped loader and parser — never grep — so ids and tokens
are the spliced ones that actually ran.

| corpus | graphs | feedback declarers | hits |
|---|---:|---:|---:|
| shipped `graphs/*.yaml` | 8 | **2** (`review-loop::review`, `backlog-batch::review-a`) | **0** |
| `oh-my-graph-hq/lanes/graphs/*.yaml` | 26 | 2 (byte-identical copies of the two above) | **0** |
| `~/.oh-my-graph/runs/*/state.json`, deduped by resolved graph | 201 distinct | **11** | **3** |

All three hits are the specimen run's three lanes. **Noise: 0 of 3.** The
corpus also contains the repair: run `20260816-163954.329528000-1`, three
minutes later, is the same graph with the token added, and the sweep is
correctly silent on it. So the predicate separates the broken loop from its
fix on real data, not on a fixture.

The corrected shipped count is **2**, which matches the brief's guess and ADR
0027's own correction of the same figure. No shipped graph fires.

## Decision

**Add a sixth advisory sweep to `internal/handoff`: for every node `D`
declaring `feedback: { rerun: R }`, if no node in the loop body other than `D`
itself quotes `{{ feedback.D }}` in its prompt, warn — on `R`, naming `D`.**

`handoff.LintFeedbackQuoting`, wired into the one `warnAdvisories` helper
`lint` and `run --dry-run` share. Six decisions, each with its reason:

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
all 11 declarers get the same verdict either way, because in the two
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

### 5. Advisory, never a load error

The standing reason every sweep in this package has: **only a person can write
what it condemns.** `coordinator.validatePlannedNodes` refuses a
planner-authored `feedback:` outright, so every arc this sweep can see is in a
human's own reviewed file. And the shape is expressible on purpose, if barely
— a loop whose re-run reads the *repository* rather than the reply (a
formatter re-run after a linter judged the tree) needs no payload, and that
author should see the line once and move on.

### 6. It lives in `internal/handoff`, not next to `LintFeedbackReach`

`LintFeedbackReach` is pure topology — `depends_on` and the between-set — and
belongs where the graph is. This one reads **prompt text through the
interpolation pattern**, which is handoff's subject and where the other five
prompt-text sweeps and `placeholderPattern` already live. Putting it in
`internal/graph` would mean a second copy of the token pattern in the package
that already warns (in `feedbackTokenPattern`'s own docstring) that the two
patterns move together or not at all.

### No runtime behaviour changes

This is a lint. `internal/schedule`'s feedback path, the payload file, the
ledger's round accounting and the arc's semantics are untouched.

## Alternatives considered

**Make it a load error, like a misplaced token.** Rejected. ADR 0010 made the
*misplaced* token an error because it is never right — an unresolvable
placeholder has no legitimate reading. An *absent* token has one (decision 5),
and a load error would break working hand-written graphs to catch a mistake
that costs one extra round. The severity ladder in this repo is: refuse what
only a machine writes, advise what a person writes.

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

**Refuse the round at run time, when the arc fires.** Rejected. Everything
this check needs is knowable from the file, and a run that halts halfway
through on something `lint` could have said before a cent was spent is a worse
trade than a warning. It would also be a runtime behaviour change, which this
ADR's scope explicitly excludes.

**Auto-append the payload to `R`'s prompt when no token is present.**
Rejected, and firmly: it would silently rewrite a prompt the user pays for and
reviewed. ADR 0010 made the quote explicit on purpose — where the payload
lands in a prompt is a real authoring decision (the shipped graphs put it last,
after the verdict contract, precisely so it cannot displace the instructions).

**Extend `LintFeedbackReach` instead of adding a sixth sweep.** Rejected. They
answer different questions about the same arc — *is the loop aimed at the right
node* versus *can anything in it see the findings* — and a loop can be both
mis-aimed and blind. Merging them would force one message to state two
independent faults.

## Failure modes and compatibility consequences

**False positives, by construction.** A loop that repairs from the repository
state rather than from the payload is warned and should not be. Cost: one line
of advice, ignorable, on a shape the corpus contains zero of. This is the
exemption decision 5 declines to encode, because there is no way to tell it
apart from the specimen without reading the prompt's intent.

**False negatives this sweep accepts.** It sees a token, not comprehension. A
prompt that quotes `{{ feedback.D }}` in a place the model will ignore, or
under an instruction that overrides it, passes. A prompt that says "if a
FEEDBACK section appears below" and *does* carry the token but drops it in a
paragraph nobody reads, passes. Static analysis stops at "the payload is in the
text the model receives"; whether the text works is what the round itself
measures.

**A spliced node's fix lives in the fragment, not the graph.** The warning
names `qa-a/build` because that is the node that runs, but a citing graph
cannot edit a spliced prompt (ADR 0027) — the edit lands in the fragment file,
and it fixes every citation at once. That is what actually happened to the
specimen: the follow-up run's fix was one line in the fragment body. The
message deliberately does not guess which file to open; the id it names is
enough to find it, and guessing would be wrong for the hand-written majority.

**Interaction with `LintFeedbackReach`.** Both can fire on one arc, and are not
deduplicated. They are different faults with different fixes (re-aim the arc /
quote the payload), and an arc can carry both.

**Never fires on a planned graph.** The coordinator refuses a planner-authored
`feedback:` entirely, so — unlike `LintFeedbackReach`, which the coordinator
escalates from advice to a plan refusal — there is no auto-mode counterpart to
add. If planner-authored arcs are ever allowed, this sweep is a candidate for
the same escalation and that will be its own decision.

**Compatibility.** Additive and advisory: no exit code changes, no schema
changes, no runtime changes. Users whose graphs carry a blind loop will see one
new `warning:` line from `lint` and `run --dry-run` — which is the entire
point. Nothing in the run feed, ledger, snapshot or dashboard is touched.

## What implementation found

Two things worth recording.

**The corpus contains its own control.** The broken specimen and its repaired
re-run are three minutes apart in `~/.oh-my-graph/runs`, so the predicate could
be measured against a before *and* an after of the same graph rather than
against a fixture — 3 hits on the first, 0 on the second. That is a stronger
result than "it fires on nothing", which is what the fifth sweep could honestly
claim, and it is why this one ships as a detector rather than as documentation
with a test attached.

**The 288-run corpus is mostly unreadable through `runstate.Load`.** 261 of 288
snapshots on this machine are schema 2 and `Load` refuses them by design (they
cannot be resumed). The `graph` member is re-parseable JSON in both schemas, so
the measurement reads it directly and parses with `graph.Parse` — which is why
the denominator is 201 distinct graphs rather than the 25 a `Load`-based sweep
would have seen. Any future corpus pass over run history should do the same, or
it is silently measuring the last two weeks.
