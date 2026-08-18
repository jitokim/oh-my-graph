# ADR 0029 — A fragment may cite a fragment, bounded by a chain and a depth

**Status:** Accepted — not yet implemented (this ADR precedes its code)

**Date:** 2026-08-19

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

### ADR 0027's falsification condition fired, and the diagnosis it fired with is wrong

ADR 0027 registered a falsification condition: if shipped graphs do not adopt
the loop fragment, the primitive is at the wrong layer. Measured 2026-08-19
with a YAML parser rather than `grep`, over `graphs/*.yaml` and
`~/IdeaProjects/oh-my-graph-hq/lanes/graphs/*.yaml`. The reproduction is one
command, and it is written out here rather than summarised because the numbers
below decide what this ADR is allowed to promise:

```sh
python3 -c '
import yaml,glob,os
for f in sorted(glob.glob("graphs/*.yaml")):
    d=yaml.safe_load(open(f)); ns=d.get("nodes") or []
    print(os.path.basename(f), len(ns), [n["id"] for n in ns if "use" in n])'
```

| measurement | value |
|---|---|
| shipped graphs citing the multi-node fragment `repair-round` | **1 / 8** — `adr-driven-dev`, which is the PR that built the feature converting itself |
| operator lanes citing it | **0 / 28** |
| operator lanes that hand-write a `review → apply` pair | 15 / 28 |
| halves of those 15 pairs that are `use:` citations | **0 / 30** |

The last row is the one that matters, and it had not been measured before. The
brief that commissioned this ADR argued that the graphs whose shape repeats are
exactly the graphs that already use fragments well, so folding them needs a
fragment that cites fragments — "**nesting is what unblocks adoption**". For
the operator lanes that is **false**. All thirty halves of those fifteen pairs
are **inline** nodes; not one is a `use:`. Nesting would not move a single one
of them. They can cite `repair-round` today, with the mechanism ADR 0027
already shipped, and they do not.

So the honest question is what *does* stop them, and the same parse answers it:

| key | across the 15 hand-written pairs | what `repair-round` does |
|---|---|---|
| review `agent:` | `code-reviewer-deep`, 15/15 | substitution point — fits |
| review `timeout:` | `45m` ×10, `30m` ×5 | substitution point — fits |
| apply `timeout:` | `45m` ×13, `1h` ×1, absent ×1 | fixed `45m` — fits 13 |
| apply `success_check.verify` | present 14/15 | substitution point — fits |
| **review `allowed_tools`** | **9 distinct grants across 15 lanes** | **fixed `[Read, Grep, Glob, Bash(git *)]` — matches 1 of 15** |
| **apply `allowed_tools`** | 13/15 grant `Bash(go *)` alongside `make`/`git` | **fixed, and grants no `Bash(go *)`** |

Every knob those lanes vary is parameterized except the one they vary most.
Fourteen of fifteen reviews need a grant `repair-round` cannot give them;
thirteen of fifteen applies need a `Bash(go *)` it does not grant. And a
multi-node `use:` may declare **wiring only** (ADR 0027, "Overriding"), so
there is no override to fall back on either. **Adoption is 0 because the
fragment is unusable for those lanes, not because the lanes are one level too
deep.**

Two qualifications, so this is not read as more than it is. The operator lanes
are one-shot graphs written for a single task and discarded, which is an
independent reason to cite nothing, and this measurement cannot separate that
confound from the grant one. And the *shipped* corpus is a different story,
below — there nesting really is a blocker.

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
the shipped pair. On the measurement above it is not what unblocks adoption.**
The change that would unblock adoption parameterizes a tool grant on
`repair-round`; this ADR names it as the follow-up rather than smuggling it in.

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
   (`internal/graph/validate.go:344`) is `seg(?:/seg)?`; the change is `?` →
   `*`, and `nodeIDSegmentPattern` (`:333`) already exists for the checks that
   need a whole segment. ADR 0027 predicted this exact edit and said not to
   re-litigate the delimiter when the scope opened. It is not re-litigated.

3. **The real work is that resolution is single-pass.** `resolveFragments`
   (`internal/graph/fragment.go:468`) walks the entry document's `nodes:`
   sequence **once**, and `spliceLoop` (`:879`) deep-copies each internal node
   and appends it without ever re-entering `resolveNode`. Lift the refusal
   alone and a nested `use:` survives the splice into the resolved graph, where
   `Validate` reports "unresolved fragment reference" (`validate.go:620`) — a
   correct error about a graph nobody wrote. `refuseNestedUse`'s own comment had
   this right from the beginning: *"single-pass resolution, no cycle detection
   needed"* (`fragment.go:1451`). The earlier operator claim that this was "two
   lines" was wrong.

## Decision

### 1. Resolution becomes recursive descent, carrying a chain

A `use:` inside a fragment's node is resolved **at the splice, by the same code
path that resolves a top-level one, before that node is namespaced**, with a
`chain` argument threaded down: the ordered list of fragment names from the
entry graph to the file currently being spliced.

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

### 3. The depth bound is 3, and the number has an argument

**A fragment citation chain may be at most 3 fragments long.** Depth 0 is the
entry graph; depth 1 is a fragment it cites (today's only legal depth); depth 3
is a fragment cited by a fragment cited by a fragment, and a node at depth 3
may not carry a `use:`. Exceeding it is a load error naming the chain and
stating the bound.

The number is 3 because **the measured need is 2 and the headroom is 1**:

- The conversion this ADR's implementation performs needs chain length **2** —
  an entry graph cites a lane loop, which cites `e2e-verify` / `review-style` /
  `pr-publish`. That is the deepest shape in either corpus, measured, not
  imagined.
- One level of headroom covers the shape nobody has built yet: a *lane*
  fragment citing a *loop* fragment citing a *leaf* fragment.

**A bound nobody can reach teaches nothing.** Setting it at 16 or 32 — the
usual instinct, and cheap — would mean no graph ever hits it, so the constant
could never be shown to be wrong and would sit in the code forever as an
unexamined number. This repo refuses arbitrary constants elsewhere for that
reason. A bound of 3 is **deliberately falsifiable**: if a real shape needs 4,
the load error is the evidence, and raising the number is a one-character change
with a measurement attached to it. That is the same discipline ADR 0027 applied
to its own adoption condition, turned on this ADR.

Two properties fall out and are worth naming:

- **The bound is reachable only by distinct fragments.** A repeat on the chain
  is already a cycle error (rule 2), so a chain of length 4 means four
  different files. Hitting the bound requires deliberately building four
  layers, not accidentally looping.
- **The bound also bounds the recursion.** Descent is capped at 3 before the
  first byte is read, so no fragment arrangement can produce a stack overflow;
  the cycle rule and the depth rule are independent guards and both are load
  errors with messages, never a hang.

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
added** to `FragmentResolution`.

The line count is bounded, and the bound is the answer to "do not let it grow
unboundedly": one line per `use:` site actually resolved, which can never
exceed one line per node in the resolved graph the reader is already looking
at. Depth cannot inflate it — depth only lengthens the ids.

Two consistency rules the implementation owes:

- **`Spliced` names ids that exist in the resolved graph.** If an internal node
  is itself a loop, it is not in the final graph, so it does not appear;
  its own resolution line lists its expansion instead. A list naming a node the
  graph does not contain is precisely the latent crash ADR 0027 found as
  finding 3 (`TestAGatingReviewCarriesItsRecoveryArc` looking up a using id
  that is not a node).
- **A resolution's `NodeID` may not be a node, at any depth.** That was already
  true of a top-level multi-node resolution; nesting makes it true more often.
  Finding 3's fix — consumers of `Resolutions` must tolerate the id's absence —
  generalises unchanged, and every sweep that looks one up must keep its skip.

### 6. The six semantics at depth ≥ 2 — each a decision, not a detail

- **`exit:` is transitive.** A multi-node fragment still declares exactly one
  `exit:`, required, never inferred. If that exit names an internal node that
  is itself a loop, the fragment's effective exit is *that* loop's exit,
  resolved recursively. So `out.loops` records the **fully resolved** exit id,
  computed bottom-up, and a loop still exposes exactly one value to the outside
  at any depth: its transitive exit's artifact.

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
  ADR 0013 refused. Note that this non-goal is load-bearing for the adoption
  finding above: it is *why* an unparameterized grant cannot be worked around
  at the using site, and the fix is to parameterize the grant, not to reopen
  this.

## Failure modes and compatibility consequences

**No schema file changes, and that claim is the falsification condition.**
Scheduler, snapshot (`internal/runstate`), event feed (`internal/runfeed`) and
ledger take a node id as an opaque string, and the sanitizer is injective at
any depth. The only edit outside `internal/graph` is `nodeIDPattern`'s `?` →
`*` in `validate.go`. If implementation finds a place that must learn about
depth, this ADR is falsified on its central claim.

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
   advises. Lane A alone converts, which is one adopter, not a shared one.

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
of DESIGN.md gains the chain, the bound and the two refusals.

## Alternatives considered

- **Keep the refusal.** The status quo, and the strongest case against this ADR
  now that the adoption measurement is in: nesting is measurably *not* what
  keeps the operator lanes from citing `repair-round`, so this change buys none
  of the adoption it was commissioned to buy. It loses on a narrower claim than
  the brief's: the shipped pair's conversion requires it (4 of 5 nodes are
  citations), ADR 0013 and ADR 0027 both recorded the refusal as **deferred**
  rather than as right, and the deferral named the exact price — cycle
  detection and a namespacing policy — which turned out to be a chain argument
  and a bound rather than a new concept. Shipping it while being explicit that
  it does not move adoption is more honest than shipping it under a claim the
  measurement refuses.
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
  constant. 3 is measured need plus one, and it is *meant* to be hit if the
  measurement was wrong.
- **Allow nesting in only one direction** — multi-node fragments may cite,
  single-node ones may not (or the reverse). Rejected as arbitrary: the
  measured need is a multi-node loop citing single-node leaves (the shipped
  pair), and the next plausible shape is a lane fragment citing a loop
  fragment. Neither half can be closed on evidence, and the one restriction
  that *is* derivable — a single-node fragment cannot cite a multi-node one,
  having no id to namespace with — falls out of the existing rules instead of
  being imposed.
- **Parameterize `repair-round`'s tool grants instead of building nesting.**
  This is what the adoption measurement actually points at: 14 of 15 lane
  reviews and 13 of 15 lane applies need a grant the fragment fixes and a
  multi-node `use:` may not override. It is not an alternative to this ADR —
  the two are independent — but it is almost certainly the *cheaper and more
  valuable* change, and recording it here as a named follow-up rather than
  letting this ADR take credit for adoption it will not produce is the point of
  the Context section above. It needs its own ADR only if exposing a grant as a
  substitution point turns out to interact with ADR 0004's tool ceiling; PR
  #197 already made a grant arriving through a binding visible in the run log,
  which is what makes it honest rather than hidden.

## Consequences

**Positive**

- The last structural limit on fragment reuse is gone: any shape a graph can
  express, a fragment can now hold, and a loop that needs a gate or a
  publish step can cite the fragments that already ship those.
- Zero new runtime concept, for the third ADR running. Scheduler, snapshot,
  feed and ledger are untouched; the only edit outside the loader is one regex
  character.
- ADR 0027's two structural properties — unique decomposition and an injective
  on-disk spelling — are *consumed* at arbitrary depth rather than re-derived,
  which is evidence they were stated at the right generality.
- Both deferred costs turned out to be small and local: a chain argument for
  cycles and a bound with a reason, neither of which touches a second package.

**Negative / trade-offs**

- **It does not deliver the adoption it was commissioned to deliver**, and the
  measurement says so plainly. That is recorded in Context rather than
  softened, and it makes this ADR's own falsification condition sharper than
  ADR 0027's: *if, six months on, no shipped graph or lane carries a chain of
  length 2, nesting was a mechanism built for a blocker that was somewhere
  else.*
- Blast radius now reaches graphs that do not name the edited file, and the
  golden diff a reviewer must read is two hops from the change.
- The promised conversion of the shipped pair is declined, so this ADR ships
  with **no independent adopter at all** — the same hole ADR 0027 shipped with,
  for a different reason. An implementation may convert `backlog-batch`'s lane
  A (under no freeze, and needing exactly this feature), which is one adopter
  and is honest about being one.
- Error messages get longer at depth, because a chain has to appear in them for
  "charged to the fragment file" to keep locating a file.
- The recursion is bounded by a constant chosen to be small, so the first
  author who genuinely needs four layers meets a refusal rather than a feature.
  That is deliberate, and the refusal says the number so raising it is a
  recorded decision rather than a silent one.
