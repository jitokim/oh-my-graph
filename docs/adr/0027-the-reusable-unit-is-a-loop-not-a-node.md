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
will hit. A `/` cannot occur in an id anyone writes. That makes the
property this ADR wants **impossible rather than unlikely**: a spliced id
can never equal a hand-written one, so nothing has to police the boundary,
and reaching into a loop's internals from outside is not a rule anyone
enforces — it is a thing nobody can type.

`handoff.sanitizeNodeID` (`internal/handoff/handoff.go:552`) already maps
`/` to `_`. **Verified, and verified at every writer**: all three artifact
path computations (`artifactPath`, `feedbackPath`, `FailedOutputPath`) go
through it, so `qa-a/impl` persists to `qa-a_impl.out` and the flat
`<node-id>.out` contract in `docs/RUN-FEED.md` is untouched at the write
side. One read-side re-spelling does **not** go through it; see Failure
modes.

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

   So the honest statement of the guarantee is: *a `/` id is unspellable by
   an author because the loader refuses it, and unspellable by the planner
   for the same reason* — not *because the id grammar has no slash in it*.
   That is one refusal instead of zero, and it is a load error with a
   message, which is the class of thing this repo already spends refusals
   on. The collision property itself is unchanged and total.
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

### Resolution

- **Entry nodes** are the internal nodes with no internal parent. They
  **inherit the using node's `depends_on`** verbatim. A fragment may have
  more than one entry; all of them inherit it.
- **`depends_on: [qa-a]`** from anywhere downstream resolves to
  `qa-a/<exit>`. The using id names the loop as a whole from outside, and
  from outside the loop's value is its exit.
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
  (ADR 0010). A spliced internal id may be named — `rerun: qa-a/impl` is an
  ordinary ancestor and works today's way — but the loop as a whole may
  not, because "rerun a set of nodes" is a different runtime concept with
  its own round accounting, its own body definition and its own
  side-exit rule. Not smuggled in under a namespacing change.
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
the artifact sits at `<runDir>/qa-a_impl.out`, and the live view would
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
had no internals. After it, it is a load error. That is a weaker guarantee
than the brief assumed, stated plainly so nobody later reads "unrepresentable"
and builds on it.

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

**A converted graph is less readable in one specific way, and it is the
way that matters most here.** Per-node comments in `self-dev.yaml` and
`dev-review-pr.yaml` carry *decisions*: why both reviews are advisory, why
that fan-out shape cannot carry a feedback arc at all, why one opens a
DRAFT PR and the other a ready one. A multi-node splice collapses five
nodes to one using site, and those comments have no node left to attach to.
This is called out here because the implementation stage is asked to
convert one shipped graph as proof, and **the identical pair is the case
where the loss is realest**: their node lists and edges are identical, but
their `dev` prompts are a repo-specific one and a generic one, and their
comments are not duplicates. If the conversion cannot keep those decisions
legible, the honest outcome is to report that and not force it — a finding
worth more than a smaller diff.

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
  `qa-a-impl` or `qa-a.impl` collides with a spliced one. The entire
  collision argument for `/` is that it is the one separator nobody can
  type, and these two throw it away for cosmetics.
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
  and encapsulation costs one load-time refusal instead of a policy.

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
  compensated by the loader's refusal and by routing the two unsanitized
  path computations through the sanitizer — but the compensation is now
  load-bearing, and a future path that builds a filename from a raw node id
  will be wrong in a way it would not have been before.
- DESIGN.md ("Fragments — `use:`/`with:`") and README's graph-authoring
  section must land in the same change that implements this, per the
  standing rule that code and DESIGN.md never drift apart.
