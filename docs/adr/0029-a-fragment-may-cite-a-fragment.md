# ADR 0029 — A fragment may cite a fragment, bounded by a chain and a depth

**Status:** Accepted and implemented. The ADR preceded its code; the code
landed in the same change, with `backlog-batch`'s lane A as the one adopter §3
and Failure modes make mandatory. The central claim held under implementation:
no snapshot, feed or ledger file changed, and the only edit outside the loader's
own resolution path is one regex character in `validate.go`.

**Where the addresses point.** This ADR was written before its code, so its
addresses were pinned to the tree it measured: `00d0bd7`, the `main` this branch
left. Every one of them resolves exactly there, and the Context and Failure-mode
sections are ABOUT that tree — what the loader refused, what `backlog-batch`
looked like before lane A was converted — so they are left pinned to it and must
be read against it. The Decision sections describe the code this change shipped,
where a line number would have gone stale the moment the diff landed, so those
addresses are **symbolic**: a file and a function name, which survive the next
refactor and are what a reader greps for anyway. The shipped adopter is
`graphs/fragments/gated-lane.yaml`, cited from `backlog-batch`'s lane A.

**Date:** 2026-08-19

**Revised:** 2026-08-19, before any code existed, after design review. The
Context section's central diagnosis was **retracted and re-measured** (the
lanes' zero adoption of `repair-round` is a missing file, not a rejection —
[measurement](../measurements/0029-repair-round-was-never-in-reach-of-the-lanes.md)),
and five questions the first draft left to the implementer are decided here
instead: the resolution order and parameter pass-through (§1), where a nested
`use:` looks and the undeclared file dependency that creates (§1), what "3"
counts (§3), what the disclosure says about `Grants` and in what order (§5), and
what a single-node fragment's tokens mean when a multi-node fragment cites it
(§7).

## Context

### The non-goal this opens, and the sentence that closed it

ADR 0027 listed nesting among the four things it deliberately refused:

> **`use:` inside a fragment (nesting, closure).** Remains a load error, as
> ADR 0013 already makes it. Multi-node fragments make the temptation sharper
> and the cost higher: nesting needs cycle detection over fragment
> *resolution*, on files read before any validation runs, plus a policy for
> namespacing a namespaced id. A later stage, with its own ADR.

This is that ADR. The refusal named its own price — cycle detection over
resolution, and a namespacing policy — and both are paid below, in that order,
because they are the whole of what was deferred.

### ADR 0027's falsification condition fired, and this ADR's first reading of it was wrong

ADR 0027 registered a falsification condition: if shipped graphs do not adopt
the loop fragment, the primitive is at the wrong layer. It fired.
`repair-round` is cited by **1 of 8** shipped graphs (`adr-driven-dev`, twice —
the PR that built the feature converting itself) and by **0 of 28** operator
lanes.

The first version of this ADR read that zero as a *choice* — "they can cite
`repair-round` today and do not" — and hung three arguments on it. **That
reading is retracted, by an `ls`.** `repair-round.yaml` is not in the directory
those 28 lanes resolve a `use:` against. Resolution is a pure function of the
entry file's path — `filepath.Join(filepath.Dir(entryPath), "fragments",
name+".yaml")` (`internal/graph/fragment.go`, `loadFragmentCached`), the
rule stated in that file's package comment — and that directory holds four files, none of them
`repair-round.yaml`. So `use: repair-round` in any of those lanes is a load
error today (`loadFragmentFile`'s missing-file branch). **The zero is a missing file.**

Nor are those lanes shy of fragments. The same parse counts **28 `use:`
citations across 17 of the 28 files**, naming all four fragments that *are* in
that directory and no name that is not:

| | |
|---|---:|
| lanes | 28 |
| lanes containing at least one `use:` | **17** |
| citations | **28** — `pr-publish` 18, `e2e-verify` 4, `review-style` 4, `review-security` 2 |
| citations of `repair-round` | **0**, and it could not have been otherwise |

That also falsifies the "one-shot graphs cite nothing" confound the first draft
offered as a qualification: these one-shot graphs cite 28 times. The fragment's
own header — *"authors who were not unaware of fragments, but for whom the two
nodes in front of the one they cited were not citable. Now they are."*
(`graphs/fragments/repair-round.yaml:5-9`) — describes exactly these authors,
and its last sentence is false for them, because the file was never put where
they look.

Numbers, the operational definitions behind them and a script that asserts
every one: [`docs/measurements/0029-repair-round-was-never-in-reach-of-the-lanes.md`](../measurements/0029-repair-round-was-never-in-reach-of-the-lanes.md).
This is the **second** time this mechanism has produced a zero that was read as
a preference — the first is [0013 — fragment reach is decided by where a graph
is saved](../measurements/0013-fragment-reach-is-decided-by-where-a-graph-is-saved.md),
which found 86 of 87 lanes stored where no `fragments/` sits beside them at all.
Neither pass was consulted when this ADR's Context was written. Before any
adoption number is read as a judgment about a fragment, check that the file is
reachable from the graphs being counted.

What survives, stated as three separate claims so none of them borrows the
others' evidence:

| claim | status |
|---|---|
| "nesting is what unblocks adoption" (the commissioning brief) | **untested.** Not *false*, as the first draft said — a lane that cannot load the fragment cannot demonstrate a need for anything deeper. |
| "the fixed tool grants are the blocker" (the first draft's replacement diagnosis) | **an untested hypothesis with a mechanism.** The mismatch below is real and re-derivable; no lane ever met it. |
| "this ADR ships with no independent adopter" | **stands, and is independent of both.** It rests on the declined conversion in Failure modes, not on any adoption number. |

The mismatch, re-measured with a stated definition (a **pair** is a
`permission_mode: plan` node that a node holding `Edit`/`Write` depends on).
Read it as a **prediction about what would happen if the file were in reach**,
not as an explanation of the zero:

| key | across the 22 hand-written pairs (17 lanes) | what `repair-round` does |
|---|---|---|
| halves that are `use:` citations | **0 / 44** | — |
| review `agent:` | `code-reviewer-deep`, 20/22 | substitution point — fits |
| review `timeout:` | `45m` ×12, `30m` ×9, absent ×1 | substitution point — fits |
| apply `timeout:` | `45m` ×18, `1h` ×2, `30m` ×1, absent ×1 | fixed `45m` — fits 18 |
| apply `success_check.verify` | present 20/22 | substitution point — fits |
| **review `allowed_tools`** | **9 distinct grants** | **fixed `[Read, Grep, Glob, Bash(git *)]` — matches 4 of 22** |
| **apply `allowed_tools`** | 17/22 grant `Bash(go *)`; 6 distinct grants | **fixed, and grants no `Bash(go *)`** |

Every knob these pairs vary is a substitution point except the one they vary
most, and a multi-node `use:` may declare **wiring only** (ADR 0027,
"Overriding"), so there is no override to fall back on. That is a real cost of
the fragment's current shape. It is not why adoption is zero.

Only two figures are unchanged from the first draft, and only because they do
not depend on the pair definition: **9 distinct review grants**, and **0**
halves that are citations. The rest of the first draft's numbers (15 lanes, 30
halves, "matches 1 of 15", "13 of 15") came from an unrecorded pass with no
stated definition and are superseded.

**The experiment that would settle it is one file copy.** Put
`graphs/fragments/repair-round.yaml` into the lanes' `fragments/` directory and
measure citation over the lanes written afterwards. Until that runs, neither
"nesting" nor "grants" is the measured blocker, and this ADR claims neither.
That experiment is a precondition for treating the grant follow-up as
evidence-backed — it is *not* a precondition for the mechanism decided below,
whose correctness does not depend on which hypothesis wins.

### Where nesting genuinely is the blocker — necessary, and not sufficient

`self-dev.yaml` and `dev-review-pr.yaml` have identical node lists, and in both
**4 of the 5 nodes are `use:` citations** (parser-verified: `e2e`,
`review-security`, `review-style`, `pr`; only `dev` is inline). The largest
sub-shape that can become a loop is `e2e → {review-security, review-style} →
pr`, exit `pr` — and all four of those are citations, so folding them requires
a fragment that cites fragments. ADR 0027 reached the same conclusion about
`backlog-batch`'s lane A and recorded it as finding 1: *a lane that already
uses fragments well is the hardest lane to convert, because nesting is exactly
what it would need.*

That makes nesting **necessary** for those conversions. It does not make it
sufficient: two further blockers sit behind it, both verified, both in Failure
modes below.

The case for this ADR is therefore narrower than the brief's, and is stated at
its true size: **nesting lifts a refusal that ADR 0013 and ADR 0027 both
recorded as deferred rather than wrong, and it is a precondition for converting
the shipped pair and for `backlog-batch`'s lane A. It is not shown to be what
unblocks adoption, and after the re-measurement above, nothing is.** The
candidate that would be worth testing first parameterizes a tool grant on
`repair-round` — named here as a follow-up with its own experiment attached,
rather than smuggled in or credited to this change.

### What was measured about the mechanism, re-checked rather than inherited

An operator probe on 2026-08-17 patched the two obvious blockers and ran a
depth-3 graph. All three of its findings were re-checked against the code on
2026-08-19:

1. **No schema change is needed, and this is the load-bearing claim of the
   whole change.** `internal/runstate`, `internal/runfeed` and
   `internal/ledger` hold a node id as an opaque string; nothing in those three
   trees splits on `/`. The only place an id becomes a path is
   `handoff.SanitizeNodeID` (`internal/handoff/handoff.go:581`), which replaces
   `/` and the platform separator with `~`, and every path computation is
   already routed through it — the three writers (`handoff.go:341`, `:413`,
   `:550`) and the two read-side call sites ADR 0027 had to fix
   (`internal/serve/serve.go:587`, `cmd/oh-my-graph/dryrun.go:96`). `serve`
   takes the node id as a **query parameter** (`serve.go:567`), never as a path
   segment. Injectivity holds at *any* depth for exactly the reason ADR 0027
   gave at depth 1: `/` and `~` are both outside the atom charset, so
   `a/b/c → a~b~c` and nothing else in the admitted domain lands there.

   **If the implementation turns out to need a snapshot, feed or ledger change,
   that falsifies this measurement, and this ADR must be reopened rather than
   stretched.**

2. **The grammar survives depth.** `nodeIDPattern`
   (`internal/graph/validate.go`) was `seg(?:/seg)?`; the change is `?` →
   `*`, and `nodeIDSegmentPattern` already exists for the checks that
   need a whole segment. ADR 0027 predicted this exact edit and said not to
   re-litigate the delimiter when the scope opened. It is not re-litigated.

3. **The real work is that resolution is single-pass.** `resolveFragments`
   (`internal/graph/fragment.go`) walks the entry document's `nodes:`
   sequence **once**, and `spliceLoop` deep-copies each internal node
   and appends it without ever re-entering `resolveNode`. Lift the refusal
   alone and a nested `use:` survives the splice into the resolved graph, where
   `Validate` reports "unresolved fragment reference"
   (`validate.go`, `validateFragmentsResolved`) — a
   correct error about a graph nobody wrote. `refuseNestedUse`'s own comment had
   this right from the beginning: *"single-pass resolution, no cycle detection
   needed"* (`fragment.go`, `refuseNestedUse`, since replaced). The earlier operator claim that this was "two
   lines" was wrong.

## Decision

### 1. Resolution becomes recursive descent, carrying a chain

A `use:` inside a fragment's node is resolved **at the splice, by the same code
path that resolves a top-level one, and the level's own namespacing happens
FIRST**, with a `chain` argument threaded down: the ordered list of fragment
names from the entry graph to the file currently being spliced.

**The order is top-down, and this is the one thing the implementation may not
get backwards.** For each internal node of a fragment being spliced at using id
`U`: (a) namespace that node against *this* level's declared ids, minting
`U/<internal>` — `namespaceNode`; (b) substitute *this*
level's bindings into it — `substituteBody`; (c) only then,
if the resulting node still carries a `use:`, descend with `U/<internal>` as the
next level's using id. The first draft of this ADR said "before that node is
namespaced", which is bottom-up and contradicts §4 ("the prefix at each level is
the id the level above already minted") and §6 ("a nested loop registered
`top/core` in the same map"). The code says which one is right twice over:
`namespaceNode`'s token rewrite is gated on `frag.declares`,
which holds only *this* level's un-namespaced atoms, so a level's rewrite has to
run while its ids are still atoms; and `out.loops` is keyed by the using id
(`resolveNode`'s multi-node branch), which §6 requires to be the fully minted `top/core`.
Descending first would leave a level's own `{{ artifacts.<sibling> }}` tokens
pointing at a key nobody registered — a graph that loads clean, is paid for, and
dies at run time, which is the failure class this ADR exists to keep closed.

**Parameter pass-through is supported, and it falls out of that order rather
than being added to it.** An inner `use:`'s `with:` values are ordinary text in
the outer fragment's file, so step (b) has already substituted the outer
bindings into them before step (c) reads them. This is not a nicety: it is what
makes the one conversion this ADR can honestly point at work. `backlog-batch`'s
lane A binds `{{ artifacts.e2e-a | inline }}` into `review-a`'s `with.focus`
(`backlog-batch.yaml:156`) and `{{ artifacts.review-a | inline }}` into `pr-a`'s
`with.publish` (`:195`); folded into a lane fragment those become the
fragment's own text, get namespaced at step (a) to `{{ artifacts.lane-a/e2e-a }}`,
and are substituted into `review-style` / `pr-publish` at the level below.

And the guarantee ADR 0027 pinned survives pass-through **exactly**, at every
level: a value bound at a using site is inserted by that level's step (b),
which is *after* that level's step (a); the level below rewrites only its own
file's text in *its* step (a), which runs before *its* step (b) inserts the
passed-through value. **A bound value is never id-rewritten by any level, at
any depth.** That is why the composition is an extension of the rule and not a
hole in it.

**A nested `use:` name must be a literal.** `use: "{{ with.which }}"` is a load
error, for the same reason `fragmentNamePattern` already
refuses a path: the chain, the cycle check and the depth bound are all decided
before the first byte of the cited file is read, and a citation whose target is
a bound value would make the citation graph depend on data. `with:` values pass
through; the `use:` name does not come from one.

**Lookup stays a pure function of the ENTRY file's path, at every depth — and
that creates a file dependency a fragment cannot declare.** `use: X` inside a
fragment resolves to `<dir of the entry graph>/fragments/X.yaml`, the same join
as at depth 1 (`loadFragmentCached`). The alternative — resolve relative to the
*citing fragment's* directory — is refused because a fragment already lives in
`fragments/`, so it would mean `fragments/fragments/`, inventing a second
location for the one thing ADR 0013 gave exactly one. The consequence is real
and must be said plainly: **a fragment that cites a fragment depends on a file
its own author cannot ship with it**, and a graph citing the outer one gets an
error naming a fragment it never wrote. `oh-my-graph init` is safe by
construction — it unpacks the whole embedded set at once (`graphs/embed.go`,
`//go:embed *.yaml fragments/*.yaml`) — but a hand-curated `fragments/` is not,
and the Context section above is a live instance of exactly that incompleteness.

No manifest, and no pre-flight completeness check. A manifest is a second
source of truth about a directory listing and drifts from it silently; the
failure it would replace is already a load error with a one-line fix. What is
owed instead is in the message: the missing-file error raised below depth 1
must name **the chain** (§6), so a reader who never wrote `e2e-verify` is told
which fragment did, and it must say that the citing `use:` is in a fragment
file rather than in their graph. An error naming a stranger's name is
survivable; an error naming a stranger's name with no way to see who asked for
it is not.

The alternative shape — let the nested `use:` through, then re-walk the
resolved sequence until no `use:` remains — is refused, and the reason is the
one thing ADR 0027 pinned to the letter:

> *The namespace rewrite applies to the tokens written in the fragment file's
> own body, and it applies BEFORE substitution; a value bound at the using site
> is inserted afterwards and is never rewritten.*

After a splice, a node's text is an indistinguishable mixture of the fragment's
own words and the citing graph's bound words. A fixpoint sweep re-reads that
mixture and cannot tell them apart, so it would namespace a bound value — the
"working reference quietly aimed at someone else's node" ADR 0027 spent a whole
paragraph refusing, arriving one level down. A sweep also has no chain, so it
cannot name a cycle: it can only notice it is not converging.

Recursive descent keeps the rule true **per level**: at each level, that
level's own file text is rewritten first, then that level's bindings are
substituted in, and the result is opaque text to every level above.

The existing `cache map[string]*loadedFragment` is unchanged and keeps its job
— a fragment file's structural judgment is a fact about the file, identical for
every user, and its errors and advisories must still be reported exactly once
per resolution pass. **The chain is separate from the cache on purpose**: the
cache is memoisation keyed by name, the chain is a property of the path taken
to get here, and conflating them is what makes a cycle invisible.

Keeping the cache has one consequence at depth that the first draft left
unstated. `chargeTo` hands a file's errors to the
**first** using node and an empty slice to every later one, so a broken or
missing fragment file is reported once per pass, on whichever citation path
document order reached first. At depth, that means the chain printed in the
message is **a** path to the file, not necessarily the reader's: a second graph
node that reaches the same file down a different chain sees a message naming a
chain it did not write. This is accepted, and the wording follows from it — the
message says "reached via `a → b → c`", never "you reached it via", because the
chain is one witness rather than the reader's own. The alternative, reporting
the file's error once per distinct chain, re-creates the duplicated per-user
noise ADR 0013 removed when it made a file's judgment a fact about the file,
and it grows with the diamond count. One honest witness beats N copies.

### 2. The cycle rule: a repeat on the CURRENT CHAIN, not a global visit

**Before resolving `use: X` inside a fragment, if `X` already appears on the
current chain, that is a load error naming the whole cycle in order.**

```
fragment "qa-loop" cites "repair-round" cites "qa-loop" — a fragment citation
cycle has no fixed point: every resolution of it splices a further copy
```

It is charged to the fragment file that **closes** the cycle, because that is
the file whose `use:` line is the one a reader can delete. A fragment citing
itself is the degenerate case and gets the same message with a one-name chain.

**The check is chain membership, not global visitation, and the difference is
the whole rule.** A *diamond* — two different loops both citing
`review-style`, or one loop citing `e2e-verify` twice — is legal, common, and
exactly what fragments are for. A global "already seen this name" set would
refuse a diamond in the name of catching a cycle, which would make the second
citation of a shared leaf a load error for reasons its author cannot see. Only
a repeat on the *current path* is a cycle.

The check happens at the `use:` site, in document order, so `LoadFile`
(`errs[0]`) and `LintFile` (collect-all) still agree on which problem comes
first — the property ADR 0027 required of every pass in this file.

### 3. The depth bound is 3 citation hops, and the number is a projection, not a measurement

**Say what is counted first, because two different quantities were called "3"
in the first draft.** The bound counts **citation hops** — fragment files on the
chain — and nothing else. Depth 0 is the entry graph; depth 1 is a fragment it
cites (today's only legal depth); depth 3 is a fragment cited by a fragment
cited by a fragment, and a node at depth 3 may not carry a `use:`. Exceeding it
is a load error naming the chain and stating the bound.

A hop count is **not** an id segment count. A chain of 3 multi-node fragments
mints ids of **4** segments (`entry/a/b/c`), and a chain of 3 *single-node*
fragments mints **1** — a single-node splice creates no namespace at all. The
grammar is `seg(?:/seg)*` and bounds neither; the citation hop is the only
budget, and §5's "depth-3 id" in the first draft meant segments and is corrected
here to say hops.

**An alias hop spends the budget even though it mints nothing.** A single-node
fragment citing a single-node fragment is a chain of length 2 with a one-segment
id. That is intended rather than an oversight: the bound guards *resolution* —
recursive descent, file reads, error-message length — and an alias chain costs
all three exactly as much as a namespacing chain does. The bound is on how far
the loader walks, not on how long an id gets.

The number is 3 because **the projected need is 2 and the headroom is 1** — and
"projected" is the honest word, because **no chain of length 2 exists in either
corpus today.** It cannot: nesting is a load error, so the deepest observed
chain is 1, everywhere, necessarily. The 2 comes from two candidate conversions,
neither of which is a shape anyone has written:

- `backlog-batch`'s lane A — `dev-a` inline, then `e2e-a` → `review-a` → `pr-a`,
  each already a `use:` of a single-node fragment (`backlog-batch.yaml:78-196`).
  Folding lane A into a fragment makes those three citations *nested* ones:
  entry graph → lane-A fragment → `e2e-verify` / `review-style` / `pr-publish`.
  Chain length 2.
- The shipped pair (`self-dev`, `dev-review-pr`), which needs the same shape and
  is **declined** below for a reason unrelated to depth.

The first draft cited the second of those as "measured, not imagined" while
declining it in the same document, which made the number rest on a shape that
would not exist. **So the bound is grounded on lane A instead, and lane A stops
being optional**: an implementation of this ADR that converts nothing leaves
both the "2" and this ADR's own falsification condition untestable — which is
precisely the charge this ADR levels at a bound of 16 or 32.

**A bound nobody can reach teaches nothing.** Setting it at 16 or 32 — the
usual instinct, and cheap — would mean no graph ever hits it, so the constant
could never be shown to be wrong and would sit in the code forever as an
unexamined number. This repo refuses arbitrary constants elsewhere for that
reason. A bound of 3 is **deliberately falsifiable**: if a real shape needs 4,
the load error is the evidence, and raising the number is a one-character change
with a measurement attached to it. That is the same discipline ADR 0027 applied
to its own adoption condition, turned on this ADR.

Three properties fall out and are worth naming, and the third is a limit on
what this rule buys:

- **The bound is reachable only by distinct fragments.** A repeat on the chain
  is already a cycle error (rule 2), so a chain of length 4 means four
  different files. Hitting the bound requires deliberately building four
  layers, not accidentally looping.
- **The bound also bounds the recursion.** Descent is capped at 3 before the
  first byte is read, so no fragment arrangement can produce a stack overflow;
  the cycle rule and the depth rule are independent guards and both are load
  errors with messages, never a hang.
- **It does NOT bound the size of the resolved graph, and the runaway it was
  reached for is a product, not a sum.** Three legal hops of 5-node fragments
  is 5 × 5 × 5 = 125 nodes from one `use:` line, and a diamond — two internal
  nodes citing the same fragment, which rule 2 deliberately keeps legal —
  multiplies it again. The chain bound caps how far the loader walks; nothing
  here caps how much it emits. That is accepted rather than fixed: a 125-node
  graph is one an author may already write by hand, `Validate` judges the
  resolved graph identically either way, and a node-count cap would be a second
  arbitrary constant guarding a cost the first one does not create. What it
  changes is where the cost lands — on the reader of `--dry-run` and of the
  checked-in goldens, which is the same place ADR 0027 put it and is called out
  again under "Blast radius" below. §5's line-count bound is about *disclosure*
  lines and stays true; the thing that grows is the graph those lines describe.

### 4. Namespacing an already-namespaced id

`top` + `core` → `top/core`; that node cites a multi-node fragment declaring
`make`, so `top/core` + `make` → `top/core/make`. **The prefix at each level is
the id the level above already minted** — composition is left-to-right by the
same join, applied to the already-spliced id.

**Decomposition stays unique at any depth, and this is ADR 0027's property
extended, not re-decided.** An atom is `[A-Za-z0-9][A-Za-z0-9._-]*` and cannot
contain the delimiter, so splitting `a/b/c` on `/` recovers exactly `[a b c]`
and there is no reading under which `a/b` was an atom. The on-disk form is
injective for the same reason, at the same depths: `~` is as unwritable in an
atom as `/` is, so `a/b/c → a~b~c.out` collides with nothing. ADR 0027 stated
both properties in a form that never mentioned a count of joins; this ADR
consumes them at N joins rather than restating them.

**There is still no bound on an id's LENGTH, and this ADR does not add one.**
`nodeIDSegment` (`validate.go`) is `[A-Za-z0-9][A-Za-z0-9._-]*` — unbounded
— so an id whose sanitized form exceeds a filesystem's 255-byte name limit is
expressible, and `<id>.out` would fail to write. That is a property of the
grammar, not of nesting: one 300-character segment does it at depth 0 today.
Depth adds segments (at most 4 under rule 3) rather than a new failure, so
closing it is a separate one-line decision about the grammar and is deliberately
not smuggled in here. Recorded so the next reader knows it was seen and left,
not missed: **injectivity is proven, writability is not.**

The three authorship refusals are **unchanged**, and no new one is needed. Each
tests for the presence of a `/`, never for how many:
`graph.refuseAuthoredNamespaces` for an entry graph's `nodes:`, the
declared-ids invariant plus the single-node namespaced-token refusal for a
fragment file's own body, and `coordinator.validatePlannedNodeID` for a planner
reply. `Validate` accepts the joined form as the backstop it is, at any depth,
for the reason it accepted it at one: it cannot tell a spliced graph from a
resumed snapshot and must not learn.

### 5. What the disclosure says at depth

**One line per resolution, and a nested resolution is a resolution.** ADR
0027's rule is kept literally rather than extended.

What makes it legible at depth is that a nested resolution's `NodeID` is the
**already-namespaced** id of the node that cited the fragment, and that id *is*
the chain:

```
top: spliced dev-review-loop (…) → top/e2e, top/review-security, top/review-style, top/pr
top/e2e: spliced e2e-verify (…)
top/review-security: spliced review-security (…)
```

A reader learns from three lines that one `use:` became a tree, and learns the
shape of the tree from the ids alone, without opening a file. **No new field is
added** to `FragmentResolution` for the printed line's sake.

*Amended after review.* One field was added, and not for the printed line:
`Depth`, the chain length this resolution stands at the end of. The draft
assumed "did anything nest" could be read back off the ids, and the repo's own
falsification test was written that way — counting a `/` in `NodeID`. That is a
different quantity. A single-node hop mints no segment, so an alias chain two
files deep is a genuine nested resolution with no slash in its id at all, and
the slash count would have read a nesting repo as a flat one — the ADR's own
counter announcing a retreat that never happened. A consumer asking about
nesting must be able to ask for it, so the resolution states it.

The line count is bounded, and the bound is the answer to "do not let it grow
unboundedly": one line per `use:` site actually resolved, which can never
exceed one line per node in the resolved graph the reader is already looking
at. Depth cannot inflate it — depth only lengthens the ids.

Four consistency rules the implementation owes:

- **A parent's line is appended BEFORE the descent, not after.** `resolveNode`
  appends its resolution *after* `spliceLoop` returns.
  Left as is, a nested resolution performed inside the splice would append its
  line first and the output above would print bottom-up — children before the
  parent that explains them. The line order is the whole legibility argument, so
  the parent's `FragmentResolution` is appended, then the descent runs.
- **`Spliced` names ids that exist in the resolved graph.** If an internal node
  is itself a loop, it is not in the final graph, so it does not appear;
  its own resolution line lists its expansion instead. A list naming a node the
  graph does not contain is precisely the latent crash ADR 0027 found as
  finding 3 (`TestAGatingReviewCarriesItsRecoveryArc` looking up a using id
  that is not a node). The cost is deliberate and worth saying out loud: the
  parent's line then **undercounts** its own subtree — a `use:` that became 2
  nodes plus a nested loop of 5 reports 2, not 7. `Spliced` answers "which ids
  exist because of this line", which is the question a consumer can act on;
  "how big did this get" is answered by reading the lines below it, which the
  ordering rule above guarantees are there.
- **A resolution's `NodeID` may not be a node, at any depth.** That was already
  true of a top-level multi-node resolution; nesting makes it true more often.
  Finding 3's fix — consumers of `Resolutions` must tolerate the id's absence —
  generalises unchanged, and every sweep that looks one up must keep its skip.
- **`Grants` is announced by the level whose substitution produced it, and its
  `NodeID` is the fully namespaced one.** The first draft omitted this field
  entirely, which was the worst omission in it: `ResolvedGrant` (PR #197) exists
  precisely to make a tool grant assembled *across files* visible in the run
  log, and depth is where a grant is assembled across three. The rule is the
  existing one, unmoved: `spliceLoop` fingerprints the grant after namespacing
  and before/after substitution, so each level announces
  the grants **its own** bindings changed, tagged with the id of the node that
  will actually run with them. Pass-through (§1) means a value the entry graph
  bound can surface on a line two levels down; what connects them is the id
  prefix, which is why the grant line's `NodeID` must be the namespaced form and
  not the internal one. A reader sees `top: spliced lane-a …`, then
  `top/pr: spliced pr-publish … grants [Bash(gh *)]`, and the prefix says who
  asked. **This is weaker than #197's guarantee at depth 1**, where the granting
  line named an id the graph's author had written; at depth the id is minted,
  and the disclosure is legible only because the lines above it are present and
  ordered. That is the price of nesting on the disclosure, and it is paid here
  rather than discovered later.

### 6. The seven semantics at depth ≥ 2 — each a decision, not a detail

- **`exit:` is transitive.** A multi-node fragment still declares exactly one
  `exit:`, required, never inferred. If that exit names an internal node that
  is itself a loop, the fragment's effective exit is *that* loop's exit,
  resolved recursively. So `out.loops` records the **fully resolved** exit id,
  and a loop still exposes exactly one value to the outside at any depth: its
  transitive exit's artifact. Descent is top-down (§1) and the exit *value*
  travels back up as each level returns — the two are not in tension, and the
  first draft's "computed bottom-up" was describing the return, not the walk.

  ADR 0027's second rule on `exit:` — it may not lie strictly inside one of the
  fragment's own feedback bodies — **stays fragment-local at every level, and
  composes**. Each level's own rule already forbids the only shape that could
  manufacture a side exit for the level above it, so no level has to look
  inside another. That composition is the reason the rule was made
  fragment-local rather than graph-level in the first place, and it is the
  single strongest argument that this ADR is an extension rather than a new
  design.

- **`depends_on` inheritance chains.** An entry node is one with no *internal*
  parent, and it inherits its using node's `depends_on` verbatim. At depth, the
  using node may itself be an entry node that inherited from the level above,
  so a chain of entry nodes carries the entry graph's `depends_on` all the way
  down. Nothing new is decided; the existing rule is simply applied at each
  level in turn.

- **`{{ artifacts.<id> }}` is rewritten per level, then resolved once.** Each
  level's namespace rewrite runs against **that level's own declared id set**,
  over that level's own file text. A token naming an id the level does not
  declare remains the invariant's load error, charged to that file. After all
  splicing, `resolveLoopReferences` runs once over the fully resolved sequence
  and is depth-blind: it maps a using id to its recorded exit, and a nested
  loop registered `top/core` in the same map, so one pass serves every depth.

- **`feedback.rerun` naming a nested loop is a load error.** ADR 0027 refused
  `rerun: <loop-id>` in an entry graph — rewriting it to the exit would
  silently half-grant "again, from the top" by re-running one node. The same
  refusal applies to a fragment's internal `feedback.rerun` naming an internal
  node that cites a multi-node fragment. It is the same rule one level in, but
  it needs its own placement: `judgeInternalReference` checks only that the id
  is *declared*, and loop-ness is not known until the nested file is loaded, so
  **this check moves after nested resolution at that level.** Naming a node
  that cites a *single-node* fragment stays legal: that is an ordinary node.

- **`worktree:` / `cwd:` propagate through every level, by value.** They stay
  refused inside a fragment file at any depth, for their unchanged reason —
  they are the using node's location, not the fragment's wiring. Propagation
  composes for free: each level copies its using node's values onto the nodes
  it emits, and at the next level down those values are already on the using
  node. `cwd:` remains a template string interpolated per node at run time.

- **An internal node that cites a multi-node fragment cannot carry the level
  above's `feedback:` arc — and that, not the depth bound, is the real ceiling
  on what nesting folds.** `multiNodeUsingKeys` (checked in `spliceLoop`) admits `id`, `use`, `with`, `depends_on`, `cwd`, `worktree` and
  nothing else, at every level. So a fragment may hold a nested loop, or it may
  wrap that node in a feedback arc of its own, never both. This is the same
  constraint that kills the `backlog-batch` shared-fragment fallback in Failure
  modes — *a substitution point can bind a value, it can never make a key
  present or absent* — reappearing one level in, and it is unchanged rather than
  newly imposed: nesting simply gives it a second place to bite. An author who
  needs a gated nested loop moves the gate inside the nested fragment, where it
  is a key on an ordinary internal node. Recorded here because "what can I fold
  into a fragment?" is answered by this sentence far more often than by rule 3.

- **A fragment file's error at depth must name the chain.** ADR 0013's rule was
  "charged to the fragment file", with the first using node's id attached so a
  reader could find the citing site. At depth 3 the id names only the outermost
  node and the file may be three hops away, so the id alone stops locating
  anything. **A `FragmentError` raised below depth 1 carries the chain**
  (`self-dev → qa-loop → e2e-verify`), and that is what makes "charged to the
  file" continue to mean something.

### 7. A single-node fragment may cite, but may not cite a multi-node one

A nested `use:` is judged by **exactly the rules a top-level one is** — the
"generalized, never weakened" form ADR 0027 used for its own invariant. An
internal node carrying `use:` is a using node: it obeys the single-node
override semantics or the multi-node wiring-only rule, whichever the cited
fragment's shape selects, and `refuseNestedUse` is *narrowed* to the one case
that cannot work rather than deleted.

That case is derivable, not chosen: **a single-node fragment may not cite a
multi-node fragment.** A single-node fragment's body is spliced onto the using
node and declares no `id` of its own — ADR 0013 refuses `id:` there — while a
multi-node splice needs an id to mint `<id>/<internal>` from. There is no
namespace to mint in, so it is a load error naming the fragment. A single-node
fragment citing another single-node fragment is fine: it is an alias, it mints
nothing, and it is bounded by rule 3 like everything else.

#### The reverse direction is legal and needs its own decision, which "it is an ordinary node" does not supply

A multi-node fragment whose internal node cites a **single-node** fragment is
the shape lane A converts into, so it is the first shape this ADR will actually
build — and it makes one existing sentence ambiguous. The rule today is
explicit: *"a single-node fragment declares no ids, so its tokens deliberately
name the USING graph's nodes"* (`loadFragmentFile`'s single-node branch), bounded by exactly one
thing — the token's id may not contain a `/`. When the using graph is
itself a fragment, that sentence has two readings and the first draft chose
neither:

- **Leave the spliced body's tokens alone.** Then a `{{ artifacts.impl }}` in
  the inner file names a bare `impl` in a graph where the node is `top/impl`. It
  loads, `run` does not run the advisory sweeps, and it dies after the upstream
  nodes are paid for.
- **Rewrite them against the citing fragment's declared ids.** Then the inner
  file's text is rewritten by the outer level.

**The decision is the second, and it is the existing rule read literally rather
than a new one.** "Its tokens name the using graph's nodes" is unchanged; what
changed is that the using graph's nodes are now namespaced, so a token naming
one must be namespaced with it or it names nothing. This is not the thing §1
refuses either: §1 refuses a sweep that cannot distinguish **file text** from a
**bound value**, and here the distinction is trivially available — the inner
body is pure inner-file text at that moment, because its own substitution has
not run yet. So the order is the same order, one level in: **namespace the
spliced single-node body against the citing fragment's `declares` with the
citing level's minted prefix, then substitute the inner bindings.** A bound
value still never gets rewritten, at any level, by anyone.

That leaves one case the rewrite cannot cover, and it is a load error rather
than a silence: **a token in the inner file naming a ref the citing fragment
does not declare.** At depth 1 such a token legitimately names a node of the
citing *graph* and stays advisory (`handoff.LintPlaceholders`). Inside a
fragment there is no citing graph to name — it would resolve against whichever
graph happened to cite the outer fragment, which is precisely the leak the
multi-node invariant refuses in `loadFragmentFile`. It is charged to the
**citing site** — the internal node's `use:`, with the chain — not to the inner
file, which is legal in isolation and may be cited perfectly well from a plain
graph. The three fragments this will first apply to make no such reference:
`e2e-verify`, `review-style` and `pr-publish` contain no `{{ artifacts.… }}` in
their own bodies at all — they take them through `with:`, which is pass-through
(§1) and never rewritten. So the first adopter exercises the safe path, and the
error exists for the shape that comes after it.

An alias hop mints nothing and therefore rewrites nothing: it passes the
enclosing namespace straight through to the next single-node body, which is
judged against the same `declares` as its citer.

**An alias may not write its own `prompt:` — decided here, not derived.** This
draft said an alias "is fine" and left it there, which left one shape undecided:
a fragment file whose `node:` declares both a `prompt:` and a `use:`. Nothing in
the mechanism forces an answer — the body would splice perfectly well, the
prompt simply winning over the cited fragment's. The answer is **refused**, for
the reason the citing-site rule already gives one file over: a wholesale prompt
override recreates the copy-variation drift fragments exist to kill while still
claiming the cited fragment's name. An alias RELAYS behavior; one that rewrites
the prompt is not relaying it. Customize through the cited fragment's declared
substitution points, or drop the `use:` and write the node out.

It is judged **against the file**, in `judgeFragmentUse`, not at the citing site.
That placement is the whole point of recording this: the first implementation
caught the same shape at splice time, where an alias's re-entry saw the fragment
body's keys already overlaid onto the reader's node and reported a `prompt:` two
files away as if the reader had written it. A fragment file's judgment is a fact
about the file (ADR 0013), and this is one.

The same sentence settles the shape the ADR-0029 split dropped by accident: a
fragment file whose node declares a `with:` with no `use:`. The pre-0029
`refuseNestedUse` fired on **either** key, so it was a file error; the successor
returned early on a nil `use:` and it became a splice-time error charged to the
citing node — and, for a single-node fragment, only *after* the body had been
overlaid. `judgeFragmentUse` refuses it at file level again, at both the
single-node and the internal-node site. What is left in `resolveNode` is the
entry graph's own dead `with:`, at depth 0, which is what keeps
`FragmentError.Chain`'s "its last element is `Fragment`" true rather than
aspirational.

## Explicit non-goals

Stated so the next reader does not reopen them by accident.

- **Dynamic fan-out over a runtime-sized collection.** A different axis
  entirely, and refused for the reason ADR 0027 gave: this is *authoring-time*
  reuse of a known shape, resolved away before the scheduler exists. Fan-out is
  a runtime concept and would breach the constraint that makes fragments cost
  the engine zero. Nesting does not bring it closer; it deepens a tree that is
  still fully known at load.
- **Loop-until-dry convergence.** `max: N` stays the only convergence, and this
  is now measured rather than asserted. Of the three loops observed on
  2026-08-17 to exhaust their rounds, **two were the missing-quote bug ADR 0028
  closed** and one was a review that genuinely needed a second pass. **Zero
  were "a counter could not express this."** The case for a smarter loop rests
  on evidence that has not appeared.
- **A planner emitting `use:`.** Still refused. Auto mode's graph is validated,
  not loaded from a file, and a planner that could cite fragments would carry
  the whole namespace question into a path where no loader runs — which is
  exactly why `coordinator.validatePlannedNodeID` exists. Depth changes nothing
  about that argument.
- **Per-node overrides on a multi-node `use:`, at any depth.** Unchanged from
  ADR 0027. It remains the short path back to the wholesale-`prompt:` override
  ADR 0013 refused. Note that this non-goal is load-bearing for the grant
  hypothesis above: it is *why* an unparameterized grant cannot be worked around
  at the using site, and the candidate fix is to parameterize the grant, not to
  reopen this.

## Failure modes and compatibility consequences

**No schema file changes, and that claim is the falsification condition.**
Scheduler, snapshot (`internal/runstate`), event feed (`internal/runfeed`) and
ledger take a node id as an opaque string, and the sanitizer is injective at
any depth. If implementation finds a place that must learn about depth, this
ADR is falsified on its central claim.

Said precisely, because the first draft said it wrongly: the one-character
`nodeIDPattern` edit (`?` → `*`) is in `validate.go`, which is **inside**
`internal/graph` — the sentence claiming it as "the only edit outside
`internal/graph`" contradicted itself and hid something. What is outside the
loader is the *reading* of `Resolutions`: `internal/serve` and
`cmd/oh-my-graph/dryrun.go` need no code change (ADR 0027 already made them
tolerate a `NodeID` that is not a node), but what they **print** changes — ids
grow segments, one `use:` now yields several lines, and the parent line
undercounts its subtree (§5). No consumer breaks; every consumer's output moves,
and the goldens are where a reviewer sees it.

**The conversion promised by the brief is declined, for two verified reasons.**
The brief asked that `self-dev` and `dev-review-pr` be folded into one shared
loop they both cite. The *shape* fits — 4 of 5 nodes fold into `e2e →
{review-security, review-style} → pr` with exit `pr`, and `evidence:
"{{ artifacts.e2e | inline }}"` stops being a binding in both files and becomes
fragment-internal text, which is genuine reuse. It is declined anyway:

1. *ADR 0013's equivalence freeze forbids it, and this ADR will not spend it.*
   `internal/graph/migration_test.go:41-72` holds both files byte-identical to
   `testdata/pre-migration/` outside a mask **keyed by node id**, with ids and
   edges inside the frozen set. `maskConvergedFields` calls `t.Fatalf("mask
   names node %q, which the graph does not contain")` (`:138`) when a masked id
   is absent, and the splice renames all four masked nodes to
   `<using>/<internal>`; `len(post.Resolutions) != len(masks)` (`:164`) would
   additionally see one resolution where the mask names four. The gate does not
   bend — it breaks structurally, and no mask entry can express the break,
   because the thing that changed is the key. ADR 0027 declined to retire that
   freeze on the grounds that it is one-time evidence, cheap to keep, and
   impossible to reproduce. Nothing in this ADR changes that argument, so the
   answer is the same. Retiring it is a decision some later change may record
   deliberately, with the pre-migration fixtures deleted on purpose rather than
   as a side effect of a demonstration.

2. *`backlog-batch`'s two lanes — the obvious fallback — are not one shape.*
   Lane A **gates** and lane B **advises**, and the file spends rule 6
   (`backlog-batch.yaml:38-56`) explaining why each does what it does. Lane A's
   gating lives in two keys on the using node: a narrowed `success_check` and a
   `feedback: { rerun: dev-a, max: 1 }` arc. A multi-node `use:` may declare
   wiring only, so both would have to move inside the fragment — and a
   substitution point can bind a *value*, never make a `feedback:` key present
   or absent. One fragment cannot serve a lane that gates and a lane that
   advises.

   **Lane A alone converts, and this ADR now requires it rather than offering
   it.** As a lane-A-only fragment the two gating keys stop being a problem:
   they ride on `review-a`, which becomes an *internal* node of that fragment
   and a node citing a *single-node* fragment, where ADR 0027 already allows
   both keys. The result is one adopter, not a shared one — and it is the shape
   the depth bound is grounded on (§3), the shape that exercises pass-through
   (§1), and the shape that exercises a multi-node fragment citing single-node
   ones (§7). An implementation that ships the mechanism without it ships three
   decisions no graph has ever executed.

The instruction that governs this is the brief's own: *if the conversion loses
something, say so and do not force it.* What would be lost here is ADR 0013's
one-time equivalence evidence, in exchange for a demonstration — a bad trade,
made twice now for the same reason.

**Blast radius grows again, and further from the edit.** ADR 0013 made a
fragment edit a multi-graph change; ADR 0027 multiplied that by a fragment's
node count. Nesting adds a hop: an edit to `e2e-verify` now moves nodes in
graphs that never name it, because they cite a loop that cites it. The
mitigation is the existing one — checked-in resolved-graph goldens in
`testdata/golden/`, regenerated in the PR that causes the move and **read**
rather than rubber-stamped — and it gets harder, not easier, because the
reviewer's diff is now two files away from the file that changed. This is the
largest real cost of the change and it is paid deliberately.

**Comment sites keep disappearing, one level faster.** ADR 0027 warned that a
splice collapses N nodes to one using site and the per-node comments carrying
*decisions* have nowhere to attach. At depth the fragment file that could hold
them is itself cited by another fragment, so a decision about "this graph's use
of this loop" has one fewer place to live. The `review-security` comments in
both shipped graphs — each a paragraph on why that fan-out shape cannot carry a
feedback arc under ADR 0010's side-exit rule — are the concrete instance, and
they are part of why the conversion above is declined rather than forced.

**A cycle and an over-deep chain are load errors, never hangs.** Both are
checked before the file at the far end is spliced, both name the chain, and
neither can be reached at run time: resolution happens entirely at load, as
ADR 0013 requires and ADR 0027 restated.

**Old graphs are unaffected, bit for bit.** No existing fragment carries a
nested `use:`, no existing id gains a second `/`, and `SanitizeNodeID` is
untouched. Every single-node and every depth-1 multi-node path keeps its tests
as the regression proof.

**DESIGN.md, README's graph-authoring section and `docs/RUN-FEED.md` land in
the same change as the code**, per the standing rule that code and DESIGN.md
never drift apart. `docs/RUN-FEED.md`'s `<sanitized-node-id>.out` sentence needs
no edit — the rule it states is already depth-blind — but the fragment section
of DESIGN.md gains the chain, the bound counted in hops, the two refusals, the
lookup rule at depth (entry-file-relative, and the file dependency it creates)
and the sentence about `feedback:` on a node that cites a multi-node fragment.
`docs/measurements/` gains the availability experiment's result when it is run.

## Alternatives considered

- **Keep the refusal.** The status quo, and the standing case against this ADR:
  nesting is not *shown* to be what keeps the operator lanes from citing
  `repair-round` — nothing is, now that the zero turned out to be a missing file
  — so this change cannot claim the adoption it was commissioned to buy. It
  wins on a narrower claim than the brief's: two conversions require it
  (`backlog-batch` lane A, which this ADR commits to, and the shipped pair,
  which it declines), and ADR 0013 and ADR 0027 both recorded the refusal as
  **deferred** rather than as right, with the deferral naming the exact price —
  cycle detection and a namespacing policy — which turned out to be a chain
  argument and a bound rather than a new concept. Shipping it while being
  explicit that it does not move adoption is more honest than shipping it under
  a claim no measurement supports.
- **Do the grant follow-up first, and let nesting wait for observed demand.**
  The sequencing question the first draft skipped by calling the two
  "independent", which they are — but independence is about coupling, not about
  order, and order is a real choice. The case for it is strong: put
  `repair-round` where the lanes can reach it, parameterize its grants, and the
  next lanes either cite it or do not. Either outcome is evidence, and if they
  start citing it, *then* the depth a lane actually needs becomes something one
  can watch instead of project. **Partially adopted.** The availability
  experiment and the grant work are named as the follow-up with a stated test
  (Context), and this ADR stops claiming any adoption result. Nesting is still
  decided now, for one reason: the cost the reviewer of this alternative
  correctly identified — that building a mechanism with no adopter pushes the
  undecided questions onto whoever implements it — is a cost of *leaving them
  undecided*, not of deciding them. §1's ordering and pass-through, §5's grant
  disclosure, §7's token rule and §3's terminology were exactly those questions;
  they are settled above, and settling them is cheaper while the code does not
  exist than after. What this ADR does concede to the alternative is its own
  ambition: the mechanism ships with one committed adopter (lane A) and an
  explicitly untested blocker hypothesis, not with an adoption story.
- **A fixpoint sweep instead of recursive descent** — let the nested `use:`
  through the splice and re-walk the resolved sequence until none remain. This
  is the "two lines" design, and it is wrong twice: after a splice the
  fragment's own text and the citing graph's bound values are indistinguishable,
  so the sweep would namespace a bound value (ADR 0027's worst outcome, one
  level down); and a sweep has no chain, so it cannot name a cycle, only fail to
  converge.
- **A global visited-set for cycle detection instead of a chain.** Cheaper and
  wrong: it refuses a *diamond*. Two loops citing `review-style`, or one loop
  citing `e2e-verify` twice, are the normal case fragments exist for, and a
  visited-set would make the second citation a load error its author cannot
  explain. A cycle is a repeat on the current path; nothing else is.
- **Bottom-up pre-resolution** — flatten every fragment file to a single-level
  form once, then splice the flattened form. Rejected: the namespace prefix is
  not known until the using site is, so the flattened form would have to be
  re-namespaced at each site anyway, and by then the rewrite-then-substitute
  ordering is unrecoverable for the same reason the fixpoint sweep fails.
- **No depth bound, relying on cycle detection alone.** Cycle detection does
  guarantee termination, so this is not unsafe — but a legal chain of forty
  fragments is a runaway that a load error should prevent, and it would arrive
  as a bewildering resolved graph rather than as a message.
- **A large depth bound (16, 32).** Rejected on falsifiability: a bound no
  graph can reach can never be shown to be wrong, so it becomes an unexamined
  constant. 3 is projected need plus one, and it is *meant* to be hit if the
  projection was wrong. The same objection lands on 3 unless something reaches
  it, which is why lane A's conversion is a requirement of this ADR and not a
  suggestion (§3, Failure modes): a bound of 3 over a corpus whose deepest chain
  stays 1 is exactly the unexamined constant this bullet rejects, wearing a
  smaller number.
- **Allow nesting in only one direction** — multi-node fragments may cite,
  single-node ones may not (or the reverse). Rejected as arbitrary: the shape
  both candidate conversions need is a multi-node loop citing single-node
  leaves, and the next plausible shape is a lane fragment citing a loop
  fragment. Neither half can be closed on evidence, and the one restriction
  that *is* derivable — a single-node fragment cannot cite a multi-node one,
  having no id to namespace with — falls out of the existing rules instead of
  being imposed.
- **Parameterize `repair-round`'s tool grants instead of building nesting.**
  The change the grant mismatch points at: **18 of 22** lane reviews hold a
  grant other than the fragment's fixed one, and **17 of 22** applies need a
  `Bash(go *)` it does not grant — and a multi-node `use:` may not override
  either. It is not an alternative to this ADR — the two
  are independent, and the *sequencing* question is its own bullet above — but
  it is almost certainly the cheaper change, and recording it here as a named
  follow-up rather than letting this ADR take credit for adoption it will not
  produce is the point of the Context section. It comes with the one-file-copy
  experiment attached, because without that its own premise is untested too. It
  needs its own ADR only if exposing a grant as a substitution point turns out
  to interact with ADR 0004's tool ceiling; PR #197 already made a grant
  arriving through a binding visible in the run log, which is what makes it
  honest rather than hidden.

## Consequences

**Positive**

- The last structural limit on fragment reuse is gone: any shape a graph can
  express, a fragment can now hold, and a loop that needs a gate or a
  publish step can cite the fragments that already ship those.
- Zero new runtime concept, for the third ADR running. Scheduler, snapshot,
  feed and ledger are untouched, and the only code edit outside the loader's own
  resolution path is one regex character in `validate.go` — which is itself
  inside `internal/graph`. What moves outside is output, not code: the
  `Resolutions` consumers print more lines and longer ids.
- ADR 0027's two structural properties — unique decomposition and an injective
  on-disk spelling — are *consumed* at arbitrary depth rather than re-derived,
  which is evidence they were stated at the right generality.
- Both deferred costs turned out to be small and local: a chain argument for
  cycles and a bound with a reason, neither of which touches a second package.

**Negative / trade-offs**

- **It does not deliver the adoption it was commissioned to deliver**, and no
  measurement now says anything else does either. That is recorded in Context
  rather than softened, and it makes this ADR's own falsification condition
  sharper than ADR 0027's: *if, six months on, no shipped graph or lane carries
  a chain of length 2 that this ADR's own implementation did not write, nesting
  was a mechanism built for a blocker that was somewhere else.* The clause about
  authorship is the fix for the first draft's version, which lane A's conversion
  would have satisfied on its own and which was therefore a condition designed
  to pass.
- **This ADR's Context was wrong once already, in the direction of certainty.**
  It read a zero as a judgment when it was a missing file, and did so with a
  parser and a printed reproduction — which is to say the wrongness survived the
  usual defences. The mitigation is procedural and belongs in the record: an
  adoption number about a fragment now requires showing that the fragment is
  reachable from the graphs being counted, and this is the second measurement in
  this repo to be corrected by that same check.
- Blast radius now reaches graphs that do not name the edited file, and the
  golden diff a reviewer must read is two hops from the change. It also grows
  multiplicatively (§3), because a chain bound is not a size bound.
- The promised conversion of the shipped pair is declined, so this ADR ships
  with exactly **one** adopter — `backlog-batch`'s lane A, now required rather
  than offered (Failure modes). ADR 0027 shipped with none; one is not much
  better, and it is honest about being one.
- Error messages get longer at depth, because a chain has to appear in them for
  "charged to the fragment file" to keep locating a file.
- The recursion is bounded by a constant chosen to be small, so the first
  author who genuinely needs four layers meets a refusal rather than a feature.
  That is deliberate, and the refusal says the number so raising it is a
  recorded decision rather than a silent one.
