# ADR 0027 — The reusable unit is a loop, not a node

**Status:** Accepted — design settled, not yet implemented

**Date:** 2026-08-16

## Context

ADR 0013 gave this repo a reuse mechanism: a fragment file, cited with
`use:` + `with:`, spliced by the file loader before validation. It works,
it shipped, and it is used. It is also aimed one level below the thing
that actually repeats.

### What the corpus says, measured 2026-08-16

Two corpora: the eight graphs in `graphs/`, and the eighteen `w-*.yaml`
one-shot operator lanes in `oh-my-graph-hq/lanes/graphs/`.

**Shipped graphs (8).**

- `self-dev.yaml` and `dev-review-pr.yaml` have **identical node lists and
  identical edges**: `dev → e2e → {review-security, review-style} → pr`.
  Every divergence between the two files is a `with:` binding, a comment,
  or one `allowed_tools` entry.
- `backlog-batch.yaml` carries a four-node variant of that same lane
  **twice**: `dev-a → e2e-a → review-a → pr-a` and the same again with `-b`.
- `adr-driven-dev.yaml` writes a review/repair loop out **longhand**:
  `round1 → apply1 → round2 → apply2 → round3`. Five nodes spelling
  "review, then repair, three times".

**Operator lanes (18).**

- **12 of 18** end in `review → apply → pr`; **13 of 18** carry the
  `review → apply` pair. `w-0023.yaml` unrolls it three times, exactly as
  `adr-driven-dev` does.
- **12 of 18 cite `use: pr-publish`** — and *every one of those twelve
  hand-writes the `review → apply` pair immediately upstream of the node it
  cited.*

That last line is the measurement this ADR exists for. These authors were
not unaware of fragments; they used one, in the same file, on the adjacent
node. **Reuse is taken to exactly the depth the mechanism allows and stops
there.** The single node was citable, so it was cited. The two nodes in
front of it were not, so they were copied — thirteen times.

### One correction to the brief that commissioned this ADR

The brief stated "6 of 8 shipped graphs declare `feedback:` — they are
already loops." That is wrong and the corrected figure is more interesting.
Only **2 of 8** declare a `feedback:` edge (`backlog-batch` lane A,
`review-loop`). Six of eight *mention* feedback, and four of those mentions
are **comments explaining why the graph does not or cannot carry an arc** —
`self-dev` and `dev-review-pr` each spend a full paragraph on the ADR 0010
side-exit rule that refuses one in their shape. Across the operator lanes,
`feedback:` is declared **0 of 18** times.

The corrected numbers do not weaken the case; they sharpen what the case
*is*. The repeating loop is overwhelmingly **not** a declared `feedback:`
arc. It is a loop **written out longhand** — `review → apply`, or
`round1 → apply1 → round2` — by authors who either could not use an arc in
their shape or never reached for one. So the unit this ADR makes reusable
must be a **subgraph**, carrying whatever edges it declares, of which a
`feedback:` arc is one legal kind and a straight chain is another. "Loop"
is the motivating instance and the honest name for what people build; it is
not a new schema concept, and nothing below special-cases it.

### The prior decision this reverses, and the condition it set

ADR 0013 considered and **rejected** exactly this feature, under
"Graph-level includes (import a subgraph of nodes with its edges)":

> Rejected as over-scoped for the evidence. The corpus shows recurring
> *node shapes*, not recurring *subgraphs* … If a recurring multi-node
> motif ever shows up in a future corpus pass, that is its own ADR, and
> node fragments compose upward into it; the reverse is not true.

The rejection was conditional and named its own trigger. The condition is
now met and measured: 13 of 18 lanes, both identical shipped templates, and
two independent hand-unrollings of the same repair round. This ADR is the
"own ADR" that sentence promised, and it composes upward exactly as
predicted — a single-node fragment stays what it is, and a multi-node one
is the general case it was a special case of.

Copy-paste at this scale is not evidence of reuse. It is evidence that
reuse was unavailable at the size people actually work in.

## Decision

### Surface: a fragment may declare `nodes:` plus `exit:`

A fragment file gains two optional keys. The using site is **unchanged**: a
node with `use:` and `with:`.

```yaml
# graphs/fragments/qa-loop.yaml
fragment: qa-loop
description: implement -> verify -> review, with one repair round
substitutions: [task, checks_command]
exit: review
nodes:
  - id: impl
    type: claude-run
    prompt: "{{ with.task }}"
  - id: verify
    depends_on: [impl]
    success_check:
      verify: { command: "{{ with.checks_command }}", timeout: 5m }
  - id: review
    depends_on: [verify]
    feedback: { rerun: impl, max: 1 }
```

```yaml
# in a using graph
- id: qa-a
  use: qa-loop
  depends_on: [plan]
  worktree: lane-a
  with:
    task: "{{ inputs.task }}"
    checks_command: make local
```

A fragment declares **either** `node:` (today's single-node form, unchanged
in every respect) **or** `nodes:` — never both, and never neither. A file
carrying both is a load error naming the fragment.

### The invariant: ADR 0013 is generalized, never weakened

State it as one sentence:

> **A fragment may never name an id it does not itself declare.**

- A **single-node** fragment declares no ids at all. Therefore `id`,
  `depends_on` and `feedback` remain load errors for it — today's rule,
  *unchanged*, and it keeps every one of its tests. `cwd:` and `worktree:`
  likewise stay refused in both forms, for their own reason: they are
  invocation and lane choreography, not wiring among declared ids.
- A **multi-node** fragment declares its own ids in `nodes:`. Edges among
  *those* ids are therefore legal — and only those. An internal
  `depends_on` or `feedback.rerun` naming an id the fragment does not
  declare is a load error charged to the fragment file.

Nothing loosens. The old rule becomes the special case of the new one, and
falls out of it arithmetically: a fragment that declares zero ids may name
zero ids. `fragmentWiringFields` does not shrink; it is partitioned into
the fields that are always refused (`cwd`, `worktree`) and the fields
refused exactly when the fragment declares no ids to justify them (`id`,
`depends_on`, `feedback`).

### Namespacing: `<using-id>/<internal-id>`, and why the slash

A spliced node's id is the using node's id, a `/`, and the internal id:
`qa-a/impl`, `qa-a/verify`, `qa-a/review`.

`/` is chosen deliberately. `nodeIDPattern`
(`internal/graph/validate.go:320`) is `^[A-Za-z0-9][A-Za-z0-9._-]*$` — it
admits `.`, `_` and `-`, and **not** `/`. So a separator drawn from the
first three (`qa-a.impl`, `qa-a-impl`) is a separator a hand-written id can
already spell, and the collision it invites is a real one that some graph
will hit. A `/` cannot occur in an id anyone writes, so **a spliced id can
never equal a hand-written one** — that collision property is total, and it
is the reason for the choice.

Two things it is *not*, both discovered while writing the rest of this
section and both stated here so the choice is read at its true price:

- It does not make the **file** collision impossible on its own — the
  sanitizer has to be made injective for that, below.
- It does not make reaching into a loop's internals **unspellable**, because
  `nodeIDPattern` must widen to admit the joined form at all; it makes it a
  refusal, in two places. See charset change (3).

What survives intact is the property the design actually needs: no spliced
node ever silently *becomes* another node, in memory or on disk. What is
paid for it is a handful of load errors instead of a grammar that could not
express the mistake. That price, against the cheaper design that does not
pay it, is weighed under Alternatives ("The author supplies the internal
ids").

#### The on-disk spelling: the sanitizer must become injective, and `_` is not

`handoff.sanitizeNodeID` (`internal/handoff/handoff.go:552`) already maps
`/` to `_`, and all three artifact path computations (`artifactPath`,
`feedbackPath`, `FailedOutputPath`) go through it. An earlier draft of this
ADR concluded from that pair of facts that the flat `<node-id>.out` contract
in `docs/RUN-FEED.md` was untouched. **That conclusion was wrong, and the
reason is worth stating exactly, because it is the same reason two of those
three writers exist.**

`sanitizeNodeID` is **not injective**. It was the identity function until
now — no valid id contains `/` — so nothing depended on it being one. Mint
`/` ids and it stops being the identity and starts colliding:

- using node `a` + internal id `b_c` → `a/b_c` → `a_b_c.out`
- using node `a_b` + internal id `c` → `a_b/c` → **the same `a_b_c.out`**
- a hand-written `qa-a_impl` (legal today, legal after) and a spliced
  `qa-a/impl` → **the same file**

The ids differ, so `validateNodesUnique` passes and the in-memory artifact
map is fine. What collides is the **file**. Whichever node finishes last
overwrites, and `{{ artifacts.x | inline }}` inlines another node's output
into a paid prompt with nothing failing. `feedback/` and `failed/` collapse
the same way — and those two directories exist *precisely* to dodge this
class of collision, with the reason written into the code
(`handoff.go:327-343`: "node ids allow dots, so a suffix scheme like
`<id>.feedback.out` would let a node literally named `x.feedback` produce an
artifact at node `x`'s payload path"). By this repo's own recorded standard,
a silently-wrong file is not a thing this ADR may ship.

**Decision: the namespace separator's on-disk spelling is `~`, not `_`.**
`sanitizeNodeID` maps `/` — and, for its existing cross-platform reason,
`os.PathSeparator` — to `~`. Over the domain of ids the loader admits, that
map is injective: `~` is outside `nodeIDPattern`'s character class in both
the old form and the new one, so no authored segment can contain it and no
two distinct ids can sanitize to the same name. `qa-a/impl` persists to
`qa-a~impl.out`.

Two properties this buys, both load-bearing:

- **Every existing file is byte-identical.** Sanitize was the identity on
  every id that can exist today and remains the identity on all of them; only
  a spliced id, which no run has ever had, spells differently. No migration,
  no resume hazard, no consumer break.
- **The contract in `docs/RUN-FEED.md` becomes statable and stays flat.** It
  is `<sanitized-node-id>.out`, one file per node in one directory, where
  sanitize is a documented rule a consumer applies in one line: replace `/`
  (and this platform's own separator) with `~`. Injective over every id the
  loader admits, since none of those three characters can appear in one.
  That doc sentence changes in this ADR's implementation; the directory
  layout does not.

**The alternative was to keep `_` and add a load error on duplicate
sanitized ids.** Cheap, loud, and the repo's usual move — and rejected here
on this ADR's own criterion. It would make whether a fragment resolves
depend on what *else* the host graph contains (a `qa-a` use collides with a
hand-written `qa_a_impl` three screens away), which is verbatim the reason
flat internal ids are rejected under Alternatives: "a fragment could be
citable in one file and a load error in another for reasons its author
cannot see." An injective spelling removes the failure instead of reporting
it, at the cost of one character in one function.

One read-side path still re-spells the artifact path without the sanitizer;
see Failure modes.

#### The charset changes are four, not one — and one of them is a loosening

The brief named `placeholderPattern` as "the one charset change". It is
not. The full audit, since a missed one of these fails silently:

1. **`handoff.placeholderPattern`** (`internal/handoff/handoff.go:55`) —
   **must gain `/`** in its reference class. A spliced prompt contains
   `{{ artifacts.qa-a/impl }}`; without the change the token does not match,
   the runtime passes it through verbatim, and a paid prompt ships a literal
   placeholder. As named in the brief.
2. **`graph.feedbackTokenPattern`** (`internal/graph/feedback.go:225`) —
   **must gain `/` too**, and this is the dangerous one the brief did not
   name. Its own comment states the invariant it would break: *"The
   reference character class matches the one internal/handoff's
   placeholderPattern resolves, so what load accepts and what the runtime
   substitutes can never drift apart."* Change (1) alone breaks that
   sentence. Concretely: `{{ feedback.qa-a/review }}` in a spliced body
   would interpolate at run time (via the widened `placeholderPattern`)
   while being **invisible** to `validateFeedbackPlaceholders`, which is the
   load-time check that confines a feedback token to the body of the arc
   that declares it. The feedback namespace resolves to the empty string
   when nothing has fired, so an unconfined token does not fail — it is
   silently empty forever. The two patterns move together or not at all.
3. **`graph.nodeIDPattern`** (`internal/graph/validate.go:320`) — **must
   admit the joined form**, because ADR 0013's first consequence is that a
   resolved graph validates exactly like a hand-written one, and
   `validateNodeIDs` runs on the resolved nodes. Without it, every
   multi-node splice fails its own load. The pattern becomes
   `^<seg>(/<seg>)?$` where `<seg>` is today's whole pattern: at most one
   slash, each side an otherwise-valid id.

   **This is a deviation from the brief's reasoning and it is flagged
   rather than taken quietly.** The brief argued that a spliced id "can
   never collide — impossible, not merely unlikely" *because* `/` is
   outside `nodeIDPattern`. Once `nodeIDPattern` admits `/`, that argument
   no longer holds on its own terms: `id: qa-a/impl` becomes a spellable
   id. The property is worth keeping, so it is restored one layer up
   instead of abandoned: **the loader refuses a `/` in any id written in a
   file** — in an entry graph's `nodes:`, and in a fragment's `nodes:`.
   Only the splicer may mint one. `Validate`, which cannot tell a spliced
   graph from a hand-written one and must not learn, accepts the joined
   form as the backstop it is.

   **The refusal is two refusals, not one, and the planner needs its own.**
   An earlier draft wrote "unspellable by the planner for the same reason".
   That is false: a planner reply never goes through `LoadFile`. `Plan`
   parses it with `graph.Parse` (`internal/coordinator/coordinator.go:516`)
   and then applies `validatePlannedNodes` (`:832`), which checks prompts,
   gates, `permission_mode`, cwd, verify, agent, worktree, feedback, retry
   and tools — and **nothing about the shape of an id**. The only thing
   keeping `a/b` out of a planned graph today is `nodeIDPattern` itself, and
   widening it is this ADR. Left there, auto mode could mint a `/` id
   legitimately and carry the whole namespace question — collisions, serve's
   204, encapsulation — into a path where no loader ever runs.

   So the guarantee is **three-way**, and the implementation owes all three:

   1. **The loader refuses `/` in any id read from a file** — an entry
      graph's `nodes:`, a fragment's `nodes:`. Load error with a message.
   2. **The coordinator refuses `/` in any id the planner produced** — a new
      per-node `*PlanError` alongside its siblings, so it joins the
      collect-all list the bounded re-plan repairs against, and is pinned by
      `TestPlannedNodeRefusalsAreReal`
      (`internal/coordinator/field_dispositions_test.go:317`) like every
      other planner refusal.
   3. **`Validate` accepts the joined form**, as the backstop it is. The
      refusal cannot live there: `Validate` cannot tell a spliced graph from
      a hand-written one and must not learn, and a resumed leg re-parses a
      snapshot that already holds joined ids.

   Stated honestly, then: *a `/` id is unspellable by an author because the
   loader refuses it, and unspellable by the planner because the coordinator
   refuses it* — not *because the id grammar has no slash in it*. That is two
   refusals instead of zero, both load-time errors with messages, which is
   the class of thing this repo already spends refusals on. The collision
   property itself is unchanged and total.
4. **`graph.withTokenPattern`** (`internal/graph/fragment.go:209`) — **no
   change**. It matches substitution-point names, which are chosen by the
   fragment author and are never namespaced. Widening it would admit
   `{{ with.a/b }}`, which can name nothing.

Checked and found **not** to need `/`, as the brief asked:

- **`handoff.leadingWordPattern`** (`placeholder_lint.go:35`) and its twin
  **`graph.tokenLeadingWord`** (`fragment.go:224`), both `^[A-Za-z0-9_]+`:
  no change. Each is applied to a token body and matches only the **leading
  word** — `inputs`, `artifacts`, `feedback`, `with` — halting at the `.`
  that follows. The reference after the dot never reaches them. Their job
  is "does this token claim a placeholder namespace at all", and that
  question has no ids in it.
- **`looseTokenPattern`**, in both packages (`\{\{[^{}]*\}\}`): no change,
  no id class.
- **The lint sweeps in `placeholder_lint.go`**: no charset change, and they
  inherit (1) for free — `judgeToken` re-parses the token with
  `placeholderPattern` itself rather than re-spelling the grammar, which is
  precisely why. `ancestorsOf` is string-keyed and namespace-blind. One
  behavioural note worth recording: `LintPlaceholders`' "references node
  `x`, which is not an ancestor" finding becomes **more** valuable after
  this change, because it is exactly what catches an internal
  `{{ artifacts.<internal> }}` that resolution failed to rewrite.
- **`worktreeNamePattern`** (`validate.go:283`): no change. `worktree:`
  stays on the using node and propagates *by value*, so no worktree name is
  ever namespaced.
- **`fragmentNamePattern`** (`fragment.go:236`): no change. Fragment names
  are not ids.

### `exit:` is REQUIRED whenever a fragment declares 2+ nodes

A multi-node fragment with no `exit:` is a load error naming the fragment.
A single-node fragment keeps working with no `exit:` and may not declare
one.

**It is not inferred from the unique sink**, and the reason is the whole
argument. Inference is correct only for as long as there is exactly one
sink. The day someone adds a second terminal node — a notification, a
cleanup, a second reviewer — inference either picks one or gives up, and
the graph it wires is not the graph the author meant. **When inference is
wrong, it is wrong silently.** Nothing fails. The run proceeds. Downstream
nodes depend on a node the author never chose, and the author finds out
after the run, from output that does not match the shape in their head, and
spends an afternoon reading a wiring they did not write. A tool that wastes
your afternoon without telling you is one you stop using. One required key
costs a line per fragment file, once, and buys a load error instead of a
lost afternoon. That trade is not close.

#### `exit:` may not sit inside one of the fragment's own feedback bodies

A second rule on `exit:`, and it exists to keep a loop fragment's validity
from depending on the graph that cites it — the property this ADR demands of
flat ids and must therefore honour itself.

The body-shaped feedback checks (`internal/graph/feedback.go:186-213`) run
**after** resolution, over the whole host graph: gate-in-body, disjoint
bodies, out-of-body session parent, and the side exit. The side exit is the
one this ADR's own wiring can manufacture. Downstream `depends_on: [qa-a]`
resolves to `qa-a/<exit>`; if `<exit>` is a body node that is *not* its arc's
declarer, that downstream edge is a node outside the loop depending on a node
inside it — a side exit, refused, in a graph whose fragment author wrote
nothing wrong and cannot see the citing file.

So: **`exit:` must name the declarer of any feedback arc whose body contains
it — equivalently, `exit:` may not lie strictly inside a body.** A fragment
with no arc is unconstrained; a fragment with an arc exits at its declarer or
downstream of it. This is checkable **fragment-locally**, at load, from the
fragment's own `nodes:` alone, and charged to the fragment file. With it, no
using site's downstream wiring can create a side exit, because the only
spliced node it can name is one whose external dependents were always legal
(`feedback.go` skips the declarer's own dependents for exactly this reason:
"they consume its settled, final result").

The other three body checks stay where they are, and the honest enumeration
of who they can be charged to after a splice is:

| check | can a host graph trigger it? | error names |
|---|---|---|
| side exit | **no**, given the rule above | — |
| gate in body | no — both nodes are the fragment's | spliced ids |
| out-of-body session parent | only for an **entry** node, whose parent the using site supplies — the same inherited-arity hazard as session handoff below | spliced id, which locates the using site |
| disjoint bodies | yes — a host arc that spans the whole loop overlaps the fragment's own body | both spliced and host ids |

The last two are inherent to splicing an arc into someone else's graph and
are not new in kind: ADR 0013 already settled that a resolved graph validates
exactly like a hand-written one and that the error names the spliced id,
which is enough to find the using site. They are listed rather than left
implicit so nobody later reads "no new checks" as "no new failures".

### Resolution

- **Entry nodes** are the internal nodes with no internal parent. They
  **inherit the using node's `depends_on`** verbatim. A fragment may have
  more than one entry; all of them inherit it.
- **`depends_on: [qa-a]`** from anywhere downstream resolves to
  `qa-a/<exit>`. The using id names the loop as a whole from outside, and
  from outside the loop's value is its exit.
- **`{{ artifacts.qa-a }}` resolves to `{{ artifacts.qa-a/<exit> }}`,
  symmetrically**, filter and all. An earlier draft defined the edge rewrite
  and said nothing about the data one, which would have left the corpus
  conversion this ADR promises impossible: `backlog-batch`'s `pr-a` inlines
  `{{ artifacts.review-a | inline }}`, and `review-a` is exactly the node
  that becomes a loop internal. Without the symmetry the token names a node
  that no longer exists, and the failure is not a load error — **`run` does
  not run the handoff lint sweeps** (`handoff.LintPlaceholders`' only caller
  is `cmd/oh-my-graph/lint.go:112`), so the graph loads, the upstream nodes
  are paid for, and the citing node dies on an `InterpolationError`. That is
  the same wasted-afternoon shape this ADR refuses `exit:` inference over,
  and it would have been introduced by the very change that refuses it.
- **A loop exposes exactly one value to the outside: its exit's artifact.**
  That is the direct consequence of the two rules above plus the invariant,
  and it is a real constraint on what can be converted, not a slogan. A
  downstream node that needs *two* internal results cannot get them; the
  loop must either end at a node that carries both, or grow to include the
  consumer. See Failure modes for what that costs the promised conversion.
  `{{ feedback.qa-a }}` gets no such rewrite and needs none: a feedback token
  is legal only inside the body of the arc that declares it (ADR 0010), so
  from outside the loop it was already a load error and stays one.
- **`worktree:` and `cwd:` stay on the using node and propagate to every
  spliced node** — which is exactly what `backlog-batch.yaml` writes by
  hand today, once per lane node. They remain refused inside a fragment
  file. `cwd:` propagates as a template string and interpolates per node at
  run time as always.
- **Internal `depends_on`, `feedback.rerun`, and `{{ artifacts.<internal> }}`
  are rewritten to the namespaced ids.** The artifact rewrite is over the
  fragment body's own tokens, against the fragment's own declared id set: a
  token naming an id the fragment does not declare is the invariant's load
  error, not a rewrite.
- Substitution (`{{ with.x }}`) happens as it does today, per ADR 0013's
  merge mechanics — typed when the token stands alone, textual when
  embedded — and is unchanged by any of this.
- **Order is fixed, in one sentence, because two implementations would
  otherwise read it differently.** *The namespace rewrite applies to the
  tokens written in the fragment file's own body, and it applies BEFORE
  substitution; a value bound at the using site is inserted afterwards and
  is never rewritten.* ADR 0013 pinned merge mechanics to this degree of
  literalness for the same reason, and this is not a hypothetical: `self-dev`
  today binds `evidence: "{{ artifacts.e2e | inline }}"` into a fragment —
  a using graph handing a fragment a token that names a **graph-local** id.
  Substitute-then-rewrite would let that bound token be silently
  re-pointed whenever the using graph's id happened to match one the fragment
  declares (`e2e` is not a far-fetched internal name for a QA loop), which is
  the worst available outcome: a working reference quietly aimed at someone
  else's node. Rewrite-then-substitute keeps the two namespaces apart —
  fragment text resolves in the fragment's namespace, bound text in the using
  graph's — which is what an author of either file expects to be reading.
- **A bound value that names an id nothing declares is a load error**,
  charged to the using node and naming the binding key. It is the residue of
  the rule above: a using author who writes `with: { evidence: "{{
  artifacts.impl | inline }}" }` meaning the fragment's internal `impl` gets
  a token that is not rewritten, names nothing in their own graph, and would
  otherwise survive load and fail after spend for the reason in the
  `{{ artifacts.qa-a }}` bullet. The loader knows the resolved id set and it
  knows exactly which strings the using node bound, so this costs one
  existence check over a set it already has. Bound tokens whose id *does*
  exist keep today's semantics exactly, advisory ancestry lint included.

**Overriding, for the multi-node form.** ADR 0013's merge rules are
per-key over one node. There is no coherent way to overlay a using node's
`success_check` onto *five* spliced nodes, and no reading of "the whole
top-level key, subtree replacement" that survives the ambiguity. So for a
multi-node fragment the using node may declare only **`id`, `use:`,
`with:`, `depends_on`, `cwd` and `worktree`** — the wiring — and any
behavior key (`prompt`, `allowed_tools`, `success_check`, `retry`,
`handoff`, `timeout`, `budget_usd`, `agent`, `permission_mode`, `type`) on
a multi-node `use:` is a **load error** naming the key. A loop that needs a
different gate needs a substitution point or a different fragment; that is
ADR 0013's "declare it upstream or fork honestly", applied where the
alternative is not a stricter rule but an incoherent one. The single-node
form's override semantics are untouched, and its per-fragment disclosure
line continues to name overridden keys; a multi-node resolution's
disclosure line names the fragment, its description, its source, and the
ids it spliced.

**An internal node may declare `type: gate`.** ADR 0013 already lets a
fragment's `node:` declare `type`, so `nodes:` inherits it, and the question
is only whether to carve out an exception. It is not carved out: a spliced
gate is an ordinary gate, and all three of its consequences already have
owners. `resume --approve qa-a/approve` takes the spliced id as the opaque
string it always was. A gate inside one of the fragment's own feedback
bodies is refused by the existing gate-in-body check, fragment-locally, per
the table above. Auto mode refuses every gate already
(`validatePlannedNodes`), and auto mode cannot cite fragments anyway. The
one thing worth saying out loud is that the id a human is asked to approve is
a *minted* one the using author never typed — which is why the pause must
surface the spliced id verbatim, as the node id it is.

### Ledger, feed, snapshot, scheduler: no schema change

Flattened nodes are ordinary nodes with ordinary ids. The scheduler sees a
DAG; the snapshot stores the resolved graph, as ADR 0013 already requires
whenever any node resolved a fragment; the event feed emits `qa-a/impl` in
the `node` field it already has. **No field is added anywhere.** A consumer
that wants the loop view groups by the `<using-id>/` prefix and gets it for
free; a consumer that does not, does not notice.

## Explicit non-goals

Four things this change deliberately does not do. Each is a real request
that will be made, each is refused here so the refusal is on record rather
than re-litigated per pull request.

- **`feedback: { rerun: <using-id> }` re-running a whole loop.** A feedback
  edge re-runs one ancestor node and the body between it and the declarer
  (ADR 0010). "Rerun a set of nodes" is a different runtime concept with its
  own round accounting, its own body definition and its own side-exit rule,
  and it is not smuggled in under a namespacing change. Two spellings, and
  an earlier draft got the first of them backwards by blessing
  `rerun: qa-a/impl` as "an ordinary ancestor that works today's way" — which
  the Failure modes section, correctly, refuses:
  - **`rerun: qa-a/impl` written by hand is a load error.** `rerun:` can only
    be written in a graph file, and the loader refuses a `/` in *any* id a
    file spells, in a `depends_on` or a `rerun` alike. Reaching into a loop
    is refused wherever it is spelled; there is no key that is an exception.
  - **`rerun: qa-a` — naming the loop — is also a load error**, and this one
    has to be *written down* rather than left to fall out. The symmetry with
    `depends_on: [qa-a]` is a trap: rewrite it to the exit and the author who
    asked to re-run their loop silently gets one node re-run instead. That is
    a request half-granted without a word, which is precisely the failure
    shape this ADR spends a required `exit:` key to avoid. `depends_on:
    [qa-a]` means "after the loop", and the exit expresses that exactly;
    `rerun: qa-a` means "again, from the top", and the exit expresses the
    opposite. So the resolver does not rewrite it — it refuses it, naming the
    using id and saying that a loop is not a rerun target.
- **`use:` inside a fragment (nesting, closure).** Remains a load error, as
  ADR 0013 already makes it. Multi-node fragments make the temptation
  sharper and the cost higher: nesting needs cycle detection over fragment
  *resolution*, on files read before any validation runs, plus a policy for
  namespacing a namespaced id. A later stage, with its own ADR.
- **Loop-until-dry convergence.** `max: N` stays the only convergence.
  Making the loop reusable does not make it smarter, and a fragment that
  loops until a model says it is done is a different decision about who
  bounds spend.
- **Dynamic fan-out over a runtime-sized collection.** A different axis
  entirely: this ADR is about *authoring-time* reuse of a known shape,
  resolved away before the scheduler exists. Fan-out is a runtime concept
  and would breach the constraint that makes this feature cost the engine
  zero.

## Failure modes and compatibility consequences

**One read-side path re-spells the artifact path and must be routed
through the sanitizer.** `internal/serve/serve.go:579` computes
`filepath.Join(s.runDir, nodeID+".out")` directly, not via
`handoff.sanitizeNodeID`. Today that is harmless *only because*
`nodeIDPattern` forbids `/`. After this change,
`/api/result?node=qa-a/impl` would look for `<runDir>/qa-a/impl.out` while
the artifact sits at `<runDir>/qa-a~impl.out`, and the live view would
render **204 "no result yet" for a node that has a result** — a silent
wrong answer, not an error. `cmd/oh-my-graph/dryrun.go:84` seeds the same
unsanitized spelling and would print a path a real run never writes. The
fix is to export the sanitizer from `internal/handoff` and route both call
sites through it, so the path is computed in one place.

This is worth stating precisely against the task's constraint. Scheduler,
snapshot, event feed and ledger do **not** change — the constraint holds.
But "no consumer schema changes" is not the same claim as "no code outside
the loader changes", and this ADR does not make the wider one. Two call
sites outside the loader duplicate a computation that ADR 0013's own
artifact contract says belongs in one place; the change surfaces the
duplication rather than creating it.

**Encapsulation is a refusal, not an impossibility.** Restating the
consequence of charset change (3): after the loosening, `depends_on:
[qa-a/impl]` written by hand in a using graph is *syntactically*
representable and is refused by the loader with a message. Before this
ADR, reaching into a fragment's internals was impossible because fragments
had no internals. After it, it is a load error — in a `depends_on`, in a
`feedback.rerun`, in an entry graph or a fragment file, and in a planner
reply by the coordinator's own refusal. That is a weaker guarantee than the
brief assumed, stated plainly so nobody later reads "unrepresentable" and
builds on it. What is *not* weakened is the collision property: distinct ids
now also mean distinct files, by the injective sanitizer, and that one is
structural.

**Session handoff across the splice boundary.** A fragment's internal node
may declare `handoff: session` and works normally when it has exactly one
internal parent. An **entry** node with `handoff: session` inherits the
using node's `depends_on`, whose arity the fragment cannot know — so a
using site with two parents produces a session node with two parents. That
is refused by the existing session-arity validation, after resolution,
loudly, exactly as ADR 0013 arranged for the single-node case ("if a using
graph gives such a node two parents, the existing session-arity validation
rejects the resolved graph exactly as it would a hand-written one"). No new
check; the error names the spliced id, which is enough to find the using
site.

**A fragment naming an undeclared id fails at load, charged to the
fragment file**, once per resolution pass, whichever graph first cites it.
Two uses of one fragment in one graph produce `qa-a/impl` and `qa-b/impl`
and cannot collide. Both get tests.

**Blast radius grows with the unit.** ADR 0013 already warns that a
fragment edit is a multi-graph change and that goldens must be read rather
than rubber-stamped. A multi-node fragment multiplies that by its node
count: one edit to `qa-loop` moves five nodes in every citing graph's
golden. The mitigation is the existing one (checked-in resolved-graph
goldens, regenerated in the PR that causes the move) and it does not get
easier — reviewers face bigger diffs, on purpose.

**The conversion proof is `backlog-batch.yaml`, and the two graphs that
motivated this ADR are the wrong targets — for two reasons that both had to
be found before implementation, not during it.**

*Reason one: the identical pair is frozen shut by ADR 0013's equivalence
gate.* `internal/graph/migration_test.go` holds `self-dev.yaml` and
`dev-review-pr.yaml` byte-identical to `testdata/pre-migration/` outside a
narrow per-field mask — **ids and edges included** — and the mask is keyed
by node id, with `t.Fatalf("mask names node %q, which the graph does not
contain")` for a key that misses (`:132`). A multi-node splice renames every
converted node to `<using>/<internal>`. The gate does not *maybe* break; it
breaks structurally, and no mask entry can express the break, because the
thing that changed is the key. So converting that pair is not a diff-size
question — it is a proposal to retire ADR 0013's one-time frozen evidence
that its own migration changed nothing. **This ADR declines to retire it.**
That freeze is cheap to keep and can only be produced once; spending it to
make a demonstration prettier is a bad trade, and if a later change genuinely
needs those two graphs converted, retiring the freeze is that change's
decision to record, with the pre-migration fixtures deleted deliberately
rather than as a side effect.

*Reason two: the pair cannot be converted anyway, and the blocker is data
flow, not comments.* `self-dev`'s `pr` inlines **both**
`{{ artifacts.review-security }}` and `{{ artifacts.review-style }}`, and
`review-security` binds `evidence: "{{ artifacts.e2e | inline }}"`. Fold
`dev → e2e → {review-security, review-style}` into a fragment and all three
become internals; a loop exposes exactly one value (see Resolution), so `pr`
cannot see two of them. The only conversions that type-check are to pull `pr`
inside the fragment too — which hoists the *real* difference between the two
files (DRAFT vs ready) into a substitution point and makes the fragment the
graph — or not to convert. An earlier draft predicted the conversion risk as
"the comments lose their node to attach to". That risk is real and stated
below, but it is the second-order one; the first-order one is that the wiring
does not fit through the boundary.

`backlog-batch.yaml` fits, and fits for reasons that make it the better
proof: it is in `goldenTemplates` but deliberately **not** in
`migratedTemplates` (its conversion was never an equivalence claim), so no
frozen fixture is at stake; its lane A is `dev-a → e2e-a → review-a` with a
real `feedback: { rerun: dev-a, max: 1 }` — a loop, the motivating shape,
not a chain; the same lane appears **twice**, which is the property flat ids
were rejected for and the one a namespace has to demonstrate; and the only
value crossing the boundary is `pr-a`'s
`{{ artifacts.review-a | inline }}`, where `review-a` is the arc's declarer
and therefore the `exit:` — the exit-rewrite rule carries it, and the
`{{ feedback.review-a }}` inside `dev-a`'s prompt becomes
`{{ feedback.qa-a/review }}`, exercising charset change (2) in the same
conversion. `adr-driven-dev.yaml`'s `round1 → apply1 → round2 → apply2 →
round3` is the second candidate, under no gate at all, if a chain-shaped
proof is wanted alongside the loop-shaped one.

**A converted graph still loses per-node comment sites.** The comments in
these files carry *decisions* — why both reviews are advisory, why that
fan-out shape cannot carry a feedback arc, why one PR opens as a draft. A
splice collapses N nodes to one using site and those comments have no node
left to attach to; the fragment file can hold the ones that are about the
loop, and the ones that are about *this graph's* use of it have nowhere to
go but the using node. If a conversion cannot keep a decision legible, the
honest outcome is to report that and not force it — a finding worth more
than a smaller diff.

**Resume and running legs are unaffected.** The snapshot stores the
resolved graph (ADR 0013), so a resumed leg reads namespaced ids from JSON
and never re-resolves. `GraphSHA256` still hashes the entry file only, and
its known gap — a fragment edited mid-pause does not trip the "changed on
disk" courtesy warning — is unchanged in kind, wider in reach by exactly
the number of nodes a fragment now carries.

**Old graphs are unaffected, bit for bit.** Every existing fragment
declares `node:`; every existing using site cites one. No existing file
changes meaning, and the single-node path keeps its tests as the
regression proof of that.

## Alternatives considered

- **Do nothing; keep single-node fragments and let people copy the
  wiring.** This is the status quo and it is what the corpus measured:
  twelve lanes that cite `pr-publish` and hand-copy the two nodes in front
  of it. The status quo does not fail loudly — it produces working graphs —
  which is why it survived three ADR revisions. It loses on the same
  argument that carried ADR 0013, at a larger unit: a copied subgraph is a
  fork with no upstream, and the next `review → apply` correction is a hand
  sweep across thirteen files with a miss rate.
- **Infer `exit:` from the unique sink.** Rejected, argued above. Its
  failure mode is silent and expensive, and it saves one line.
- **Flat internal ids with a collision check** (splice `impl` as `impl`,
  refuse if the graph already has one). Rejected twice over: it makes two
  uses of one fragment in one graph impossible — which is exactly
  `backlog-batch`'s measured shape — and it makes whether a fragment
  resolves depend on what *else* is in the host graph, so a fragment could
  be citable in one file and a load error in another for reasons its author
  cannot see.
- **`<using-id>-<internal-id>` or `<using-id>.<internal-id>`.** Rejected.
  Both `-` and `.` are inside today's `nodeIDPattern`, so a hand-written
  `qa-a-impl` or `qa-a.impl` collides with a spliced one, and — since the
  separator survives sanitization unchanged — so does its artifact file. The
  collision argument for `/` is that it is the one separator nobody can type
  and the one that sanitizes to a character nobody can type; these two throw
  both halves away for cosmetics.
- **The author supplies the internal ids: an id map beside `use:`, or a
  declared `prefix:`.** Weighed properly here, because it is the cheapest
  design on the table and the earlier draft never put it on the scale. Its
  case is strong and should be stated at full strength: `nodeIDPattern` does
  not move, so no loader refusal and no coordinator refusal are needed; the
  sanitizer stays the identity and `serve`/`dryrun` are untouched; collisions
  are caught by the duplicate-id check that already exists, by name. Against
  `/`'s three new refusals plus a sanitizer change, that is roughly a hundred
  lines of implementation and four failure modes it never has. **Rejected
  anyway, on encapsulation** — which after finding-1's correction is the only
  argument left for `/`, and is enough on its own:
  - Author-chosen ids are *ordinary* ids. `qa-a-impl` is spellable, legal,
    and nothing refuses a downstream `depends_on: [qa-a-impl]`. The loop
    stops being a unit the moment its internals are addressable, and the
    concrete cost is not aesthetic: an external edge onto a body node is a
    **side exit**, which is a load error the *fragment author* cannot
    prevent — the exact host-dependence this ADR spends a required `exit:`
    rule to eliminate. With `/`, reaching in is refused at the one place it
    can be typed. With a prefix, reaching in is the path of least
    resistance.
  - The id-map variant additionally makes a fragment's internal ids part of
    its public interface: adding a node to `qa-loop` breaks every using site
    that must now name it (or needs defaults — which is auto-prefixing again,
    with its collisions back). ADR 0013's blast radius is a resolved-graph
    diff; this would make it a schema break.
  - The cost is not zero either: one naming line per use site (per node, for
    the map variant), and a loop's internals spelled differently in every
    graph that cites it, so `grep qa-loop` stops finding the nodes it made.
  - Honest bottom line: the `/` design is **more expensive** and buys exactly
    one thing — the internals are unnameable-by-refusal rather than
    nameable-by-convention. This ADR's whole claim is that the loop, not the
    node, is the unit; a design in which the unit is transparent from outside
    is not that claim implemented more cheaply, it is a different claim.
- **A runtime subgraph concept — the engine expands `use:` when the node
  becomes ready.** Rejected on ADR 0013's grounds, which multi-node makes
  stronger: it puts a file read and a failure mode in the scheduler's hot
  path, and forces the snapshot to either store unresolved nodes (breaking
  "resume never re-reads files") or store resolved ones anyway (conceding
  the point). Load-time resolution is why this feature costs the engine
  zero, and that is the whole constraint.
- **Per-node overrides on a multi-node `use:`** (address a spliced node's
  keys from the using site, e.g. `override: { review: { retry: {max: 2} } }`).
  Rejected for v1. It reintroduces exactly what the namespace is designed to
  forbid: reaching into a loop's internals from outside. It is also the
  short path back to ADR 0013's rejected wholesale-`prompt:` override, one
  indirection removed. Substitution points are the sanctioned knob.
- **A `loop:` schema keyword — make the loop a first-class graph object
  rather than a fragment shape.** Rejected as the wrong layer. It would be
  a new runtime concept for something the DAG already expresses (ADR 0010's
  feedback edge is the loop), and the measured problem is not that loops
  are inexpressible — `review-loop.yaml` expresses one in ten lines — but
  that an expressed one cannot be *cited*. Citation is a loader concern.
- **Graph-level includes as ADR 0013 rejected them.** This ADR is that
  feature, admitted under the condition ADR 0013 itself set. What has
  changed is not the argument but the evidence: the "recurring multi-node
  motif" that ADR was waiting on is now 13 of 18 lanes and both identical
  shipped templates. The hard problems it listed are answered rather than
  waved past — id namespacing by the one separator that cannot collide,
  collision policy by construction, edge splicing by entry-inherits and
  exit-represents, and "what `depends_on` may point across the boundary" by
  the invariant that a fragment may name only ids it declares.

## Consequences

**Positive**

- The unit of reuse matches the unit of work. A QA loop or a review loop is
  citable by the same `use:` a node is, and the thirteen hand-copied
  `review → apply` pairs get an upstream.
- Zero new runtime concept, again. Scheduler, snapshot, event feed and
  ledger are untouched, and a consumer that wants loop grouping gets it
  from the id prefix with no schema at all.
- ADR 0013's rule survives intact as the special case of a rule that reads
  in one sentence, which is a simplification of the schema's story rather
  than an addition to it.
- Collisions between spliced and authored ids are structurally impossible,
  **in the id space and in the file space both** — the latter only because
  the sanitizer is made injective in the same change. Encapsulation costs
  two load-time refusals (loader, coordinator) instead of a policy.

**Negative / trade-offs**

- The loader grows an id-rewriting pass over `depends_on`, `feedback.rerun`
  and artifact tokens — a class of transformation it did not previously
  perform, and one whose bugs are wrong-wiring bugs rather than load
  errors. The goldens are the defence.
- Four regex classes must move together across two packages, and one of
  them (`feedbackTokenPattern`) fails *silently* if forgotten. That
  coupling is stated in code comments today and must stay stated.
- A converted graph loses per-node comment sites, and with them a real
  documentation surface. See Failure modes.
- `nodeIDPattern` is loosened, which is a widening of a validator that
  exists to keep node ids safe as path elements and URL parameters. It is
  compensated by two refusals (loader, coordinator) and by routing the two
  unsanitized path computations through the sanitizer — but the compensation
  is now load-bearing, and a future path that builds a filename from a raw
  node id will be wrong in a way it would not have been before.
- **The refusals are three parties deep and the design only holds if all
  three land.** A loader refusal without the coordinator's lets auto mode
  mint what authors cannot write; either refusal without the injective
  sanitizer lets two nodes share a file. This is the largest single cost of
  choosing `/` over an author-supplied prefix (see Alternatives), and it is
  paid deliberately, for encapsulation.
- A loop is opaque from outside by design, so a graph whose downstream node
  needs two of a loop's internal results cannot be converted without moving
  that consumer inside the loop. `self-dev` is exactly that graph. The
  boundary that makes the unit a unit is also the boundary that decides what
  can be a unit.
- DESIGN.md ("Fragments — `use:`/`with:`"), README's graph-authoring section
  and `docs/RUN-FEED.md`'s artifact-filename sentence (now
  `<sanitized-node-id>.out`, with the rule spelled out) must land in the same
  change that implements this, per the standing rule that code and DESIGN.md
  never drift apart.
