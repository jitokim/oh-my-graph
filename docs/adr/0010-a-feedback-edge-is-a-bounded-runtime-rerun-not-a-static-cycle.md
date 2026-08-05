# ADR 0010 — A feedback edge is a bounded runtime re-run, not a static cycle

- Status: Accepted
- Date: 2026-08-02

## Context

The graph vocabulary has nodes and one kind of edge: `depends_on`, the
success-flow edge — "run me after my parents passed". Failure has three
answers today, and all three are the wrong shape for the most common
iterative pattern in real pipelines:

- **`retry` (node × attempt)** re-runs *the same node* with the same inputs.
  It recovers transient faults; it cannot express "the *work an earlier node
  produced* was judged wrong, redo it".
- **`on_fail` (graph policy)** decides what the *run* does once a failure is
  final — halt or prune the subtree and continue. Both discard the branch;
  neither recovers it.
- **`resume` (run level)** continues a stopped run across processes. It is a
  boundary between legs, not a control-flow construct.

What none of them can say is the sentence every review-driven pipeline is
built around: *"if this node fails, re-run that earlier node with this
node's output as feedback, then come back — at most N times."*

The shipped `adr-driven-dev.yaml` template is the workaround made flesh: its
`round1 → apply1 → round2 → apply2 → round3` chain is a review loop
**statically unrolled** to a fixed depth. The unrolling has every defect of
manual loop unrolling: the iteration count is baked in (a clean first round
still pays for rounds two and three's sessions; a third round of findings has
nowhere to go), the apply prompts are near-duplicates, and the graph's length
hides its actual shape — a two-node loop — from the reader.

The constraint that shaped everything below: **the static graph must stay a
DAG.** `Graph.ReadyGiven`, the three-colour cycle validator, the snapshot's
"graph × completed determines the ready set" resume rule (ADR 0003), and the
inline-adjacency design ("edges are not a separate list; each Node carries
its own `DependsOn`") all assume an acyclic `depends_on` relation.
Iteration is a *runtime* phenomenon and must be represented as one — the
file the validator walks stays acyclic and fully checkable at load time.

## Decision

### Surface: a node-level `feedback:` block, not a conditional edge, not a `loop:` construct

A node — typically a reviewer or verifier — declares the arc that fires when
it fails:

```yaml
- id: impl
  depends_on: [adr-apply]
  prompt: |
    Implement the ADR (task: {{ inputs.task }}).
    Review feedback from the previous round follows — empty on the
    first pass:
    {{ feedback.review }}
  ...

- id: localrun
  depends_on: [impl]
  ...

- id: review
  depends_on: [localrun]
  agent: code-reviewer-deep
  success_check: { exit_zero: true, result_matches: "ready to merge" }
  feedback:
    rerun: impl   # a proper depends_on-ancestor of this node
    max: 2        # REQUIRED — bounded iteration on a paid runtime
```

Reading: when `review` fails for a *judgment* cause (`verify_failed`,
`result_mismatch`, `nonzero_exit` — the fixed built-in trigger set; see
Semantics, infrastructure and spend faults fail final immediately), after
its own retries if any, the engine
re-runs the path `impl → localrun → review`, handing `review`'s output to
the re-run as `{{ feedback.review }}` — at most twice. If `review` still
fails after the second round, it fails for real. This subsumes the
adr-driven-dev unrolling: `round1/apply1/round2/apply2/round3` collapses to
one review node with `feedback: { rerun: impl, max: 2 }`, the apply prompts
folding into `impl`'s own prompt via the feedback placeholder — same maximum
depth, but a clean first round now costs one review session instead of five
nodes, and a graph reader sees the loop instead of reconstructing it.

> **Update (2026-08-05):** that collapse describes what the construct *can*
> express, not a conversion that was performed. `graphs/adr-driven-dev.yaml`
> at v0.4.1 still declares eleven node ids with `round1/apply1/round2/apply2/
> round3` intact and carries no `feedback:` block at all. See the note in
> Consequences for what shipped instead.

Why this shape over the alternatives (argued in full below):

- The declaration is **inline on a node**, like every other edge in this
  schema. `depends_on` already established that edges live on the node that
  needs them, not in a separate edge list; the failure edge lives on the node
  that closes the loop, with its bound adjacent to it.
- The static graph **stays untouched**. `feedback.rerun` is stored outside
  `DependsOn`, so `ReadyGiven`, `DependsOn`-walking, cycle validation, the
  JSON snapshot round-trip and every existing consumer are unchanged. The
  arc points strictly *backward* along already-validated `depends_on` paths
  (enforced at load), so it cannot introduce a static cycle even in
  principle — it annotates the DAG, it is not an edge in it.
- The name is **not** `on_fail`. Candidate (a) in the design discussion
  spelled this block `on_fail: { rerun: ..., max: N }`, but the graph-level
  `on_fail:` already means something on the *other side of the finality
  line*: what the run does once a failure is final. This block acts *before*
  finality — it is a recovery arc, kin to `retry`, not a post-mortem policy.
  One keyword meaning "give up policy" at one indentation and "don't give up
  yet" at another would be a standing misreading. `feedback` also names the
  payload: the failing node's output *is* the feedback, and the
  `{{ feedback.<id> }}` namespace ties the edge, the placeholder and the
  prompt narrative to one word.

Load-time validation (all in `internal/graph`, all before anything spends):

1. `rerun` names an existing node that is a **proper `depends_on`-ancestor**
   of the declaring node. This one check rules out self-loops, forward arcs,
   and arcs between unrelated branches, and guarantees the runtime arc
   closes over a path the static DAG already contains.
2. `max` is **required** and ≥ 1. There is no default: an unbounded loop on
   a paid runtime must be unrepresentable, not merely discouraged (same
   posture as the verify-timeout ceiling — refuse at load what would only be
   discovered as spend at run time).
3. The **loop body is side-exit free.** The body is the between-set — every
   node on any `depends_on` path from the target up to (and including) the
   declaring node. Every dependent of a body node other than the declarer
   must itself be in the body: a node outside the loop consuming an
   intermediate artifact would observe whichever iteration happened to have
   written last, a race with no right answer. Rejected at load, not
   discovered as nondeterminism. (Parents *feeding into* the body from
   outside are fine — they ran once, their artifacts are stable.)
4. **No gate in the body.** A gate records a standing human decision
   (ADR 0003); replaying it on iteration two via `RecordedController` would
   silently re-approve a round the human never saw. If a per-iteration human
   check is ever wanted, that is its own ADR.
5. A `{{ feedback.<id> }}` placeholder is only legal where it can ever
   resolve: on a node inside the body of a feedback edge that `<id>`
   declares. This is a **load error**, not an advisory lint —
   `LintPlaceholders` warnings never affect validity, and an out-of-body
   `{{ feedback.<id> }}` would otherwise resolve to the empty string
   silently, forever: the exact invariant-weakening this ADR rejects
   `| optional` for. The same change must teach the placeholder pattern
   and the interpolation kind table (`internal/handoff`) the new namespace
   *together*, or the token ships verbatim into a paid prompt.
6. A body node with `handoff: session` must name a session-parent that is
   itself **in the body**. An out-of-body session-parent would make round
   2's `--resume` continue a session round 1 already continued —
   contradicting the fresh-session rule in Semantics.
7. Feedback bodies are **disjoint**: no node may lie in the bodies of two
   feedback edges (a node already declares at most one arc — one `rerun`,
   one `max`). Overlapping or nested bodies multiply worst-case spend as
   `(1 + max₁) × (1 + max₂)` and give the re-arming path two owners for
   one node; v1 rejects the shape at load. Lifting this later is additive.

> **Update (2026-08-06) — there is no rule 8 for fan-in coverage; it is an
> advisory.** These seven rules all judge a *fan-in* declarer's arc valid even
> when its body excludes a producer the declarer judges. Issue #118 is that
> gap paid for: a reviewer fanning in from `qa-plan` and `load-script` with
> `rerun: load-script` found five defects, all in `QA-PLAN.md`, and re-ran the
> healthy branch to exhaustion — the file it needed to repair was not in the
> body, so the loop was structurally incapable of converging (~$14 of a $42
> run).
>
> The obvious eighth rule — *every producer the declarer depends on must be in
> the body* — was considered and **rejected as a load error**, because it is
> not sound:
>
> - It contradicts rule 3's own carve-out. "Parents feeding into the body from
>   outside are fine — they ran once, their artifacts are stable" describes a
>   legitimate and common shape: a reviewer reading a settled spec, criteria or
>   corpus node alongside the work under review. Rule 8 would refuse it.
> - It contradicts rule 4. A gate may never be in a body, so a fan-in declarer
>   with a gate parent could satisfy neither rule — an unsatisfiable pair, not
>   a safeguard.
> - It is not decidable in the direction that matters. The engine sees
>   `depends_on`; it cannot see which artifacts a prompt will judge. The #118
>   reviewer named both files by literal path (`stg-canary/QA-PLAN.md`), so
>   even a `{{ artifacts.<id> }}` scan would have stayed silent.
>
> What ships instead is `graph.LintFeedbackReach`: an advisory sweep, printed
> by `lint` and `run --dry-run`, that names the declarer, the rerun target, the
> unreachable producer and — when one exists *and still passes this
> validation* — the covering target to aim at (`rerun: scope` on #118's
> graph). It fires only on a fan-in declarer with an arc, which no shipped
> graph is; a single-parent declarer is covered by construction. Note that one
> half of the bug was always a load error and remains one: a producer left
> outside the body that *asks* for the payload via `{{ feedback.<id> }}` is
> refused by rule 5. The advisory covers the producer that never asks.
>
> The other half of the fix is not validation at all. The planner prompt
> (`internal/coordinator`) describes only the linear implement→review shape,
> so with several implementing nodes it picks one and the arc silently loses
> the rest; teaching it to aim at the nearest common ancestor when the
> reviewer fans in is where a mis-aimed arc stops being written in the first
> place.

### Semantics

**What re-runs: the path, not just the target.** When the arc fires, every
body node re-executes in dependency order — `impl`, then `localrun`, then
`review` re-judges. Re-running only the target would be cheaper but wrong
twice over: the intermediate nodes' artifacts would be stale (a `localrun`
verdict about iteration 1's code, feeding iteration 2's review), and the
declaring node itself must re-run or nothing re-judges the new work at all.
The between-set is also the *smallest correct* set: descendants of the
target that do not lead to the declarer are outside the loop (and rule 3
guarantees there are none inside the affected region), so the blast radius
is exactly the segment the graph author drew, not the target's whole
subtree.

Mechanically this is a fourth intercepted signal beside `pauseSignal`,
`limitSignal` and `rejectSignal`: the declarer's judgment failure, with
rounds remaining, surfaces as a feedback signal; the scheduler clears the body
nodes' completed status, re-seeds their in-degrees counting only in-body
parents (out-of-body parents stay satisfied; their artifacts are still on
disk and still interpolate), and launches the target. Independent branches
are untouched, and a pause elsewhere (gate or session limit) suppresses the
re-launch exactly as it suppresses dependents.

**What the re-run inherits — and the iteration-1 problem.** The declaring
node's output must be readable by the re-run, but on the *first* execution
of the target it does not exist yet — under today's rules
`{{ artifacts.review }}` in `impl`'s prompt would be an
`InterpolationError` before iteration 1 ever ran. The answer is a distinct
namespace with a documented empty default:

- `{{ feedback.<id> }}` **always inlines** the declaring node's feedback
  payload: its result text when the failing execution produced one, else
  its failure detail. There is no path form and no `| inline` filter — a
  path grammar would need an on-disk file to point at before any round has
  fired, and an empty string standing in for a path is exactly the kind of
  half-value the artifact contract exists to forbid. The engine persists
  the payload to `<run-dir>/feedback/<id>.out` (overwritten per round,
  latest wins) as an **internal** implementation file, not a documented
  contract — promoting it later is additive, demoting it would not be. The
  `.out` artifact keeps meaning "a *passed* node's result" and dependants'
  `{{ artifacts.<id> }}` contract is untouched.
- When no round has fired yet, the placeholder resolves to the **empty
  string**. Prompts write around it naturally: "review feedback follows —
  empty on the first pass".

The alternative — an `| optional` filter on ordinary artifacts — was
rejected: "an unresolvable reference is an error, never a silent empty
substitution" is a load-bearing property of the artifact contract (it is
what turns a mis-wired graph into a loud failure), and weakening it
globally to serve one feature would trade a universal guarantee for a local
convenience. The feedback namespace confines empty-is-normal semantics to
the one place where "not there yet" is an expected state rather than a
wiring bug.

**What fires the arc: judgment causes only.** The point where the arc is
considered is where `recordFail` would otherwise be reached — after the
declarer's own retries are spent — but not every cause that lands there
may fire it. The built-in trigger set is fixed to the three *judgment*
causes: `verify_failed`, `result_mismatch` and `nonzero_exit` — the causes
that mean "the node ran and judged the work insufficient". Everything else
that reaches `recordFail` — an `InterpolationError`, a
`MissingToolPolicyError`, a runner spawn error, `budget_exceeded` — is an
infrastructure or spend fault that re-running the body cannot repair: a
mis-wired `{{ artifacts.x }}` in `impl` would otherwise re-run the whole
body `max` times with zero chance of success, at full cost — the opposite
of this ADR's own "refuse at load what would be discovered as spend"
posture. Those causes fail final immediately, feedback arc or no. A
user-facing `on:` filter stays deferred (Alternatives); the built-in set
is not configuration, it is the definition of what a feedback edge is for.

**Composition with retry, and exhaustion.** `retry` operates *inside* one
execution of a node, exactly as today; the feedback arc fires only after
the declarer's retries are spent, and only for the judgment causes above.
When the arc has fired `max` times and the declarer fails again — or when
it fails for a non-judgment cause at any point — the failure becomes
final: the node FAILs with a detail naming the spend
("feedback exhausted after 2 rounds of impl → review: " + the underlying
cause), and from there the existing story runs unchanged — graph `on_fail`
halts or prunes, and a later `resume --retry-failed` may still salvage the
run. Salvage means **re-arming the loop, not re-running the declarer
alone**: `partitionForRetry` learns the feedback shape, so clearing an
exhausted declarer's FAIL also clears its body's retained PASS records and
resets the rounds budget — explicit human intervention buys a fresh set of
rounds. Retaining the body's PASSes would relaunch the declarer alone
against unchanged artifacts, exactly the target-only shape Alternatives
rejects. The four mechanisms compose as one narrative: retry answers "try
*me* again", feedback answers "the *decision upstream* was wrong, redo the
segment with what we learned", `on_fail` answers "what does the run do when
we truly give up", `resume` answers "continue a stopped run later".

**Sessions and worktrees.** Every re-executed body node starts a **fresh
claude session** — the cold-start rule `retry` already documents ("retry
started fresh — parent session not resumed") applies verbatim to feedback
re-runs, and the same ledger detail note marks it. An in-body
`handoff: session` child still resumes its parent's session *of the current
round* (the parent re-ran first and recorded its new id), so session chains
inside the body keep working; no re-execution ever resumes a superseded
round's session — and validation rule 6 guarantees the parent is in the
body, so no node exists whose round-2 `--resume` would target a session an
earlier round already continued. Re-runs **share the lane's worktree**:
`Acquire` is idempotent per name, so iteration 2's `impl` lands in the same
checkout with iteration 1's commits present — which is the point; the
feedback round amends the work, it does not restart it. That sharing is a
**within-leg** claim: `GitManager`'s created-set is in-process state. A run
stopped mid-loop and resumed provisions worktrees exactly as resume always
has — fresh checkouts, with a branch an earlier leg retained colliding
loudly on the ref rather than being silently reset
(`cmd/oh-my-graph/resume.go`); a feedback body changes nothing about that
rule.

**events.jsonl narrates rounds additively — and `node_failed` stays
terminal.** No new event type (the event-type set is closed per schema
version; a bump for this would tax every consumer for what existing
vocabulary conveys — the ADR 0009 precedent). But no existing type's
*meaning* moves either: RUN-FEED's own rules count a meaning change as a
schema bump, and `node_failed` is documented as terminal. The
"multiple terminal events per node id, latest authoritative" precedent
from retry is a *cross-leg* rule; the in-leg vocabulary for "a failed
attempt that is not final" already exists — `node_retried` — and a
feedback round reuses it:

- The declarer's non-final judgment failure emits **`node_retried`**, not
  `node_failed`, with a `detail` of the form "feedback round 1/2:
  re-running impl → review", so a tailing consumer sees the loop turn
  without correlating anything. `node_failed` appears at most once per
  declarer per leg: when the failure is final.
- Node events emitted by a feedback re-execution carry an optional `round`
  field (1-based round ordinal; absent on the initial pass — absent means
  0, like every other omitted zero value). Body nodes therefore emit one
  full started→terminal sequence per round within a leg; RUN-FEED gains a
  sentence extending latest-authoritative to in-leg rounds, in the same
  change that adds `round`.

**The ledger prices every execution; the snapshot records only what is
real.** Each round's execution of each body node appends its own ledger
row (with "feedback round k/N" in the detail), and the run total is the
sum. Aggregating rounds into one row was rejected because the ledger's one
job is the cost story, and the entire risk of a loop construct on a paid
runtime *is* the multiplier: an operator tuning `max` needs to see that
round 2 cost what round 1 cost, per node, in the same table that shows
everything else.

The snapshot is where non-final and final part ways. `recordFail` today
does four things at once — progress line, ledger row, snapshot FAIL
record, `node_failed` event — and a non-final round keeps only the first
two: a runstate FAIL written for a declarer mid-loop would make it
`settled` the moment anything stops the run, silently collapsing the loop
into an ordinary failure on resume. Instead:

- Snapshot node records gain an optional `round` field (additive; absent
  means 0). Every body-node record written during round k carries
  `round: k`, and a node's recorded spend accumulates across its rounds —
  a superseded record's cost carries into the record that replaces it — so
  `state.json`'s per-node figures sum to the same total the ledger shows.
  "Cost stays honest" holds for the contract fleetops reads, not just the
  local table.
- Arc-fire is itself durably recorded: in the same step that re-arms the
  scheduler, the declarer's record is rewritten as a non-terminal
  *feedback marker* — round k, no verdict. A leg stopped mid-loop
  therefore resumes into the loop, not out of it: the marker plus the
  graph (the body is derivable from `rerun` → declarer) tells resume that
  body records with `round < k` are superseded and drop out of the
  completed set, records with `round = k` are retained, and `max − k`
  rounds remain. `ReadyGiven` then relaunches exactly the unfinished
  remainder of round k. No separate declarer→rounds-spent map is needed —
  the recorded position *is* the counter (Alternatives).

### Guardrails

- **`max` is required**, ≥ 1, no default, refused at load (above). The
  worst-case execution count is legible from the file:
  `(1 + max) × |body|` node runs **per arc** — and because bodies are
  disjoint (rule 7), multiple arcs *add* their worst cases; the
  `(1 + max₁) × (1 + max₂)` blow-up of overlapping loops is
  unrepresentable. Each run is under its own declared `timeout`,
  `budget_usd` and tool policy per execution — the ceilings apply per
  attempt, so the multiplier is bounded but real, and the docs must say so
  the way adr-driven-dev's header prices its rounds today.
- **Planned (auto) graphs: `feedback` is allowed, with `Retry`'s standing.**
  The field-disposition table (`coordinator/field_dispositions_test.go`)
  already forces this decision mechanically — adding the field to
  `graph.Node` fails the completeness test until a row exists. The row
  reads like `Retry`'s ("bounded re-runs of an already-ceilinged node"),
  because the disposition framework judges *capability*, not spend: a
  feedback arc grants a planned node no tool, no path, no shell — it only
  repeats nodes that are already inside every ceiling layer, and the
  required `max` plus the load validations (backward-only, side-exit-free,
  no gates) hold for a planned graph exactly as for a hand-written one.
  Going stricter (rejecting it) would forbid the planner the one construct
  this ADR exists to provide while permitting the same spend shape via
  `retry: { max: N }` — an inconsistency, not a safeguard. So planned
  `feedback` receives the same treatment, decomposition and budget
  posture as `Retry`: the planner prompt gains guidance (use it for
  review loops; keep `max` small) instead of the field being banned.
  The "keep it small" guidance is also enforced:
  `coordinator.validatePlannedNodeFeedback`
  rejects a planned `feedback.max` above `maxPlannedFeedbackRounds` (3) —
  a hand-written graph may declare any bound it is willing to pay for,
  but an unreviewed plan gets a fixed ceiling.
- **Worktrees:** re-runs share the lane's worktree (stated above; the
  idempotent `Acquire` makes it free).
- **Sessions:** a re-run is a fresh session, like a retry (stated above;
  the already-documented cold-start rule applies).

## Consequences

**Positive**

- The review loop — the single most common iterative shape in real
  pipelines — becomes one declared arc instead of a hand-unrolled chain.
  adr-driven-dev's eleven nodes reduce to seven with *greater* fidelity: a
  clean first round stops early, and every round's findings actually flow
  into the re-implementation instead of only rounds the unrolling
  anticipated.

  > **Update (2026-08-05):** this consequence is written in the accomplished
  > tense and the conversion it describes **was never performed.** At v0.4.1
  > `graphs/adr-driven-dev.yaml` still has all eleven node ids — the
  > `round1 → apply1 → round2 → apply2 → round3` unrolling this ADR's Context
  > names as its founding complaint is intact — and the file contains no
  > `feedback:` block. Nothing reduced to seven.
  >
  > What actually shipped is the arc itself, in a **new** template:
  > `graphs/review-loop.yaml` declares `feedback: { rerun: impl, max: 2 }` on
  > its review node, which is the shape this bullet predicted, demonstrated on
  > a graph written for it rather than migrated to it. The mechanism is
  > therefore proven; the migration of the pre-existing template is
  > outstanding work, not a delivered consequence.
  >
  > The prediction is left standing rather than rewritten — it is the
  > decision's reasoning, and it may still be carried out. But a reader
  > comparing this ADR against the repo would otherwise conclude the ADR or
  > the template had silently regressed, when neither did: the conversion was
  > simply never done.
- The static graph remains a DAG and every existing consumer —
  validation, `ReadyGiven`, resume, the snapshot round-trip, fleetops —
  is untouched or extended only by optional fields. No schema bumps.
- The failure-handling story becomes complete and layered: retry (same
  node), feedback (path), on_fail (run policy), resume (across legs) — each
  answers a different question, none overlaps.
- Cost stays honest: bounded by construction, priced per execution in the
  ledger, narrated per round on the stream.

**Negative / trade-offs**

- A third failure keyword (`retry`, `feedback`, `on_fail`) is real surface
  area; the docs owe the reader the one-paragraph layering above, or the
  three will read as synonyms.
- The side-exit-free rule rejects some legitimate-looking graphs (a metrics
  node hanging off `impl`, say) that would be fine in practice most of the
  time. Accepted: "most of the time" is what nondeterministic artifact
  races look like from the outside, and the fix (move the tap after the
  declarer) is mechanical.
- The scheduler grows its first re-arming path — clearing completed status
  and re-seeding in-degrees mid-leg. It is the largest engine change since
  gates, and it must be built and stress-tested against `FakeRunner`
  fixtures (loops racing pauses, limits mid-round, continue-on-fail
  around a looping branch) before it can ship.
- Feedback payloads persist failed-execution output to disk
  (`feedback/<id>.out`) — a new file under the run directory, **internal**
  for v1 (Semantics): promoting it to a consumer contract later is
  additive, demoting it would not be.
- The documentation lands with the code, not after it: DESIGN.md (the new
  `feedback:` keyword, the fourth intercepted signal, the new run-dir
  file) and `internal/ledger`'s package doc — whose "one row per node"
  becomes "one row per execution" — must be updated in the same change
  that implements this ADR; code and DESIGN.md drifting apart is a bug in
  both.
- Rounds multiply spend by design. `max` bounds it, but a carelessly large
  `max` on an expensive body is the closest thing this tool has to a
  foot-gun; the shipped templates must model small values (2, not 10).

## Alternatives considered

- **Node-level `on_fail: { rerun, max }` (candidate a's spelling).**
  Rejected on the name alone; the mechanics are what this ADR adopts. The
  graph-level `on_fail` is a post-finality policy (halt/continue); reusing
  the keyword for a pre-finality recovery arc would make one word span both
  sides of the finality line, differing by indentation.
- **Edge-level conditions (`depends_on` entries with `when:`).** Rejected.
  It turns `DependsOn []string` into a list of objects, churning every
  consumer of the field (ready-set computation, dependents walking,
  session-arity validation, the JSON snapshot round-trip) for a feature
  none of them needs; and it decomposes the one sentence being expressed —
  "go back there, with this, at most N times" — into conditions scattered
  across edges that the reader must reassemble into a loop. Conditional
  *success* routing may someday want edge conditions; failure recovery is
  not that feature.
- **A `loop:` block declaring a bounded subgraph cycle.** Rejected. It
  introduces a second topology construct that overlaps the first: nodes
  would belong to both a `depends_on` DAG and a loop membership list, and
  validation would have to prove the two agree — precisely the two-sources-
  of-truth bug class the inline-adjacency design exists to avoid (ADR 0003
  rejected persisting ready sets on the same grounds). It also misstates
  the phenomenon: the engine is a ready-set scheduler, not a structured-
  control-flow interpreter, and iteration here is a *failure-recovery arc*,
  not a while-loop. A `loop:` block's natural evolution (conditions,
  `while:`) points directly at the unbounded iteration this design makes
  unrepresentable.
- **Re-run only the target, not the path.** Rejected: leaves intermediate
  artifacts stale and never re-judges the new work; argued in Semantics.
- **`| optional` on ordinary artifact placeholders instead of a feedback
  namespace.** Rejected: globally weakens the "unresolvable is loud"
  artifact invariant to serve one local need; argued in Semantics.
- **Aggregate rounds into one ledger row.** Rejected: hides the loop's cost
  multiplier — the one number the bound exists to control; argued in
  Semantics.
- **A new `node_feedback` event type.** Rejected: the event-type set is
  closed per schema version, so it forces a schema bump on every consumer
  for what one additive optional field (`round`) plus the existing `detail`
  already convey — the same reasoning that rejected `node_limited` in
  ADR 0009.
- **A non-final `node_failed` on the event stream.** Rejected: RUN-FEED
  documents `node_failed` as terminal and counts a meaning change against
  the schema version; the retry precedent for repeated terminal events is
  cross-leg only. `node_retried` already means "a failed attempt that is
  not final" in-leg; argued in Semantics.
- **A separate declarer → rounds-spent map in `state.json`.** Rejected in
  favour of the per-record `round` field: a counter records how many times
  the arc fired but not where the loop *stood*, which is what a mid-loop
  resume actually needs — and once records carry their round, the position
  implies the counter, so the map is redundant surface.
- **Path-form `{{ feedback.<id> }}` (artifact grammar with `| inline`).**
  Rejected for v1: before any round fires there is no file for a path to
  name, and an empty string standing in for a path is a half-value the
  placeholder contract exists to forbid. Always-inline confines the
  empty-is-normal semantics to prompt text; a path form can be added later
  if a payload ever outgrows a prompt.
- **A retry-style `on:` cause filter on `feedback`.** Deferred, not
  rejected: v1 hard-codes the trigger to the three judgment causes
  (Semantics), and a user-facing filter narrowing *within* that set is a
  purely additive refinement (same closed cause-token spelling as
  `retry.on`) if a real graph ever needs "loop on verify_failed but fail
  fast on nonzero_exit". Widening beyond the judgment set — looping on
  `budget_exceeded` — stays unrepresentable: re-running a body cannot
  repair a spend fault.
