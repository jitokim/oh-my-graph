# ADR 0013 — A fragment is a load-time node splice, not a runtime concept

- Status: Proposed
- Date: 2026-08-03

## Context

A mining pass over this machine's recorded run corpus measured what people
actually do with graph files: **62 runs, 326 node definitions, 58 distinct
graphs — and only 6 of those runs came from shipped templates.** The other
52 distinct graphs are not 52 inventions; they are copy-variations of about
a half-dozen recurring node shapes: the e2e/verify node, the security
review, the style review, the pr-publish node, the dev-implement node.
People copy a proven node out of a template, tweak two sentences, and ship
the copy.

The cost of that pattern is not hypothetical. When the cold-safe retry
wording was fixed — the sentence "Continue the work — or, on a retried
attempt, start from the branch's committed state" that every session-handoff
e2e node needs, because a retried attempt starts cold instead of resuming
its parent's session — it had to be **swept across every template by
hand**. It is character-identical in `graphs/dev-review-pr.yaml` and
`graphs/self-dev.yaml` today only because a human made it so, and it is
whatever it is in the 52 unshipped copies. A copied prompt is a fork with
no upstream: every fix to a proven node shape either gets re-applied
everywhere by hand or silently drifts.

What exists today for reuse is YAML anchors, and they genuinely work — but
only **within one file** (YAML defines no cross-document anchor). The
corpus's drift is **cross-file** by construction: the copies live in 58
different graphs. Anchors cannot even express the problem.

The constraint that shapes everything below is ADR 0010's, transposed:
**no new runtime concept.** The Scheduler, handoff, the snapshot, resume,
the event stream and fleetops must see exactly the graphs they see today.
Reuse is an *authoring-time* phenomenon and must be represented as one —
resolved away before validation, so a resolved graph is indistinguishable
from a hand-written one to every consumer downstream of `graph.Load`.

## Decision

### Surface: node-level `use:` with `with:` bindings, resolved by the file loader

A fragment is a **single-node definition file** under a `fragments/`
directory, with declared substitution points:

```yaml
# graphs/fragments/e2e-verify.yaml
fragment: e2e-verify
description: cold-safe e2e gate — session continuation, synchronous checks, verified verdict
substitutions: [checks, tools]
node:
  type: claude-run
  prompt: |
    Continue the work — or, on a retried attempt, start from the branch's
    committed state — and {{ with.checks }}
    Run every check SYNCHRONOUSLY and wait for it to finish — never in the
    background. Your FINAL reply is the verdict: never end the turn on an
    interim report. Report exactly PASS if everything succeeds, or FAIL
    with the failing step.
  allowed_tools: "{{ with.tools }}"
  handoff: session
  success_check:
    exit_zero: true
    result_matches: "^PASS$"
    verify: { command: "make local", timeout: 5m }
  retry: { max: 1, on: [nonzero_exit, verify_failed] }
```

A using graph splices it in with `use:`, binding the substitution points
and adding its own wiring:

```yaml
- id: e2e
  use: e2e-verify
  depends_on: [dev]
  cwd: "{{ inputs.repo }}"
  with:
    checks: |
      run `make local` (build + test + vet). ...
    tools: [Read, "Bash(make *)", "Bash(go *)", "Bash(git *)"]
```

Reading: the loader replaces this node with the fragment's `node:` block,
substitutes `{{ with.checks }}` and `{{ with.tools }}` with the bound
values, overlays the using node's own fields per the merge rules below,
and hands the result to the exact same decode → `Validate` pipeline every
graph already goes through. The cold-safe sentence — the one that had to
be hand-swept — now exists **once**, and the next fix to it is one edit.

**What a fragment file may carry: behavior, never wiring.** The `node:`
block may declare `type`, `prompt`, `allowed_tools`, `permission_mode`,
`budget_usd`, `timeout`, `handoff`, `success_check`, `retry`, `agent` —
the fields that make the shape *proven*. It must NOT declare `id`,
`depends_on`, `cwd`, `worktree`, or `feedback` — those are graph-local
wiring (`depends_on` and `feedback.rerun` name ids that only exist in the
using graph; a worktree name is lane choreography; `cwd` is
invocation-specific). A fragment carrying a wiring field is a **load
error**: a fragment is a node's behavior, portable precisely because it
says nothing about where it sits. (`handoff` stays on the behavior side
deliberately: e2e-verify's session continuation *is* part of the proven
shape, and if a using graph gives such a node two parents, the existing
session-arity validation rejects the resolved graph exactly as it would a
hand-written one — loudly, after resolution.)

**Substitution tokens reuse the placeholder grammar with a new kind:**
`{{ with.<name> }}`. One token shape for graph authors (the ADR 0010
precedent — `feedback` joined the same grammar), but a different
*lifetime*: `with` resolves once, at load, and the runtime never sees it.
The kinds table in `internal/handoff` does not learn it; instead
`LintPlaceholders` learns to warn on a stray `{{ with.x }}` in a
non-fragment prompt (it would ship verbatim into a paid prompt — the
exact failure the loose-token lint exists to catch). A substituted value
may itself contain `{{ inputs.x }}` / `{{ artifacts.y }}`; those survive
resolution untouched and interpolate at run time as always — the two
layers compose because they fire at different times.

### Semantics: merge rules — argued field by field

- **`id`: always the using node's, and required.** An id is the graph's
  wiring vocabulary; a fragment has no business naming itself into
  someone else's namespace, and two uses of one fragment in one graph
  need two ids.
- **`prompt`: NEVER wholesale-replaced.** A using node declaring
  `prompt:` alongside `use:` is a load error. The prompt *is* the
  fragment — it is the thing that drifted, the thing the incident was
  about. A wholesale override recreates the copy-variation this ADR
  exists to kill, while still claiming the fragment's name in the file:
  worse than a copy, because it looks like reuse. Customization goes
  through declared substitution points or it does not happen; a shape
  that needs more divergence than its points allow is a *different
  shape*, and the honest spelling of that is a new fragment or an inline
  node.
- **`allowed_tools`, `success_check`, `retry` (and the other behavior
  fields): from the fragment unless explicitly overridden.** Each for
  its own reason:
  - `allowed_tools` is the grant the fragment's prompt was written
    against — a copy that silently narrows it breaks the prompt, one
    that silently widens it escalates it. So the fragment's grant is
    the default, and an override must be *written in the using file*,
    where review sees it.
  - `success_check` is half of what "proven" means — e2e-verify's value
    is precisely that its verdict is evidence-grounded
    (`verify: make local`), not self-reported. It travels with the
    fragment by default; a using graph may still override it (a repo
    whose gate is `npm test` exists), and the override is again visible
    where it is declared.
  - `retry` encodes the shape's known failure modes (e2e retries on
    `nonzero_exit`/`verify_failed` because a cold retry is *designed
    for* — the prompt's first sentence handles it). Default from the
    fragment, explicit override wins.

  "Explicitly overridden" is judged by **key presence in the raw YAML
  mapping**, not by Go zero values — the resolver splices at the
  `yaml.Node` level, before any decode, so `budget_usd: 0` written in
  the using node is an override and an absent key is not, with no
  ambiguity. This is also what makes the next section literally true.

### Resolution happens before validation; `Parse` stays fragment-blind

Resolution is a front half of `graph.Load`, operating on the raw YAML
document: read the entry file, resolve every `use:` (splice, substitute,
overlay), and hand the resolved document to the same decode → `Validate`
path as today. Consequences, in order of importance:

1. **A resolved graph validates exactly like a hand-written one.** Every
   invariant in `validate.go` — cycle check, session arity, verify
   timeouts, feedback bodies — runs on the resolved nodes. No validation
   learns what a fragment is.
2. **`Parse` rejects what it cannot resolve.** `Parse` operates on bytes
   with no file context, so it cannot resolve fragments — and therefore
   a node still carrying `use:`/`with:` when it reaches `Validate` is a
   load error ("unresolved fragment reference — fragments are resolved
   by the file loader"). `graph.Node` gains the two fields (with the
   mandatory matching json tags), but a *validated* graph always has
   them empty, so they never appear in a snapshot: the JSON round-trip
   stores and restores only resolved nodes, and resume works on
   fragment-free material by construction.
3. **The engine has no fragment concept.** Scheduler, handoff, ledger,
   runfeed, serve, fleetops: unchanged, not even by an optional field.

Lint behavior (`oh-my-graph lint` renders the whole list, as always;
errors first, advisories after):

- `use:` naming a fragment not found on the search path — **load
  error**. Nothing can be judged about a node that did not resolve.
- `with:` on a node without `use:` — **load error** (dead keys are a
  wiring bug, not a style choice).
- a `with:` key the fragment does not declare in `substitutions:` —
  **load error**: a typoed key would otherwise silently not substitute,
  the same silent-mismatch class as a typoed `retry.on` cause, refused
  on the same grounds.
- a declared substitution point left unbound by the using node — **load
  error**. There are no defaults in v1 (Alternatives), so unbound means
  the fragment's prompt ships with a hole in it.
- a `{{ with.x }}` token in the fragment body where `x` is not in
  `substitutions:` — **load error**, charged to the fragment file: an
  undeclared point is an authoring bug in the fragment, found the first
  time any graph resolves it.
- a declared substitution point never referenced in the fragment body —
  **advisory warning**: harmless at run time, but it is drift smell (the
  body moved and the declaration didn't).
- a repo fragment shadowing a shipped fragment name — **advisory
  warning** (Trust, below).
- a `{{ with.x }}` token in a plain (non-`use:`) node — **advisory
  warning** via `LintPlaceholders`: the runtime will pass it through
  verbatim into a paid prompt.

### Trust and scope

**Search path: the graph file's own `fragments/` sibling first, then the
shipped set.** A `use:` in `/repo/graphs/foo.yaml` looks in
`/repo/graphs/fragments/<name>.yaml`, then in the fragments shipped with
the binary (this repo's `graphs/fragments/`, riding the same embedding
vehicle the example graphs use). Resolution is a pure function of the
entry file's path — no cwd dependence. Local-wins is deliberate: it is
how a repo pins or patches a shipped fragment without waiting on a
release. The resolver **prints one line per resolved fragment naming the
winning source file** — the disclosure posture the agent-mapping design
established — and a local file shadowing a shipped name additionally
draws the lint warning above, so a shadow is a disclosed decision, never
a silent one. For a hand-written graph this adds no new trust surface at
all: the fragment lives in the same repo, at the same trust level, under
the same review as the graph file that names it — a repo that could
plant a hostile fragment could just as easily plant the hostile node
inline.

**Planned (auto) graphs: the planner may not emit `use:` in v1 — and
mechanically cannot.** The value case is real and worth stating: a
planner that *cites proven fragments* instead of inventing prompts from
scratch would inherit exactly the verification discipline this repo's
templates encode. But the trust case decides against it for v1. A
fragment file in the run's repo is attacker-influencable whenever the
repo is untrusted, and a planner-emitted `use:` would let unreviewed
plan output pick which local file's prompt text, tool grant and verify
command run — the same line the subagent-mapping design and the
skill-mapping ADR (0012) refused to cross: **trusted Go code injects;
the planner LLM never names local resources.** Enforcement is
structural, not a coordinator check: planner output goes through
`graph.Parse`, and Parse rejects any unresolved `use:` (above), so
there is nothing for a disposition rule to miss — though the
field-disposition completeness test still forces the row for the new
`Node` fields, and the row reads "rejected structurally by
`graph.Validate` before disposition is consulted". The future path
stays open and is *deferred, not rejected*: a plan-time mapping pass in
trusted Go code that resolves **shipped-embedded fragments only** (not
attacker-influencable; the binary vouches for them) would fit the
agentmap/skillmap mold exactly — its own ADR, with ADR 0012's
measurement discipline, if the citing-proven-fragments value case is
ever pursued.

**Versioning: a fragment edit changes every user — say so, and make the
blast radius reviewable.** That multiplication is the *feature*: the
cold-safe-wording sweep becomes one edit. But it must be honest. Three
facts govern it:

1. **Running and resumed runs are immune.** The snapshot stores the
   **resolved** graph verbatim (`Snapshot.Graph`, the JSON
   `graph.Parse` re-reads), and resume reconstructs from it — it never
   re-reads the entry file, let alone a fragment. A fragment edited
   mid-pause cannot alter the leg that resumes. No fragment hash is
   needed in the snapshot for *correctness*: the resolved bytes
   themselves are already there, which is strictly stronger than a
   hash of them.
2. **One honest gap:** `GraphSHA256` hashes the *entry file's* source
   bytes, and its only job is the resume-time "this file changed on
   disk since the snapshot" warning. A fragment edit during a pause
   will not trip that warning, even though re-*running* the graph would
   now produce different nodes. Accepted for v1 — the warning is a
   courtesy, the snapshot is the authority — and fixing it later
   (hashing per resolved source file) is additive.
3. **At rest, the blast radius is a reviewed diff, not folklore.** The
   migration below checks in golden resolved-graph fixtures for the
   shipped templates; any edit to a shipped fragment fails that test
   until the goldens are regenerated, which puts every affected
   template's resolved change *into the PR diff*. A fragment edit is a
   multi-graph change and CI makes it look like one. Fragment files
   carry no version field in v1: git history is the version, and a
   graph that must not follow a fragment's evolution copies it local
   (local-wins exists for exactly this).

### Migration: two shipped templates in the same PR, byte-identical

The proof that the machinery preserves behavior is shipped with the
machinery. In the same PR:

- Extract `e2e-verify`, `review-security` and `review-style` fragments
  from the shipped templates, with substitution points covering exactly
  the spans where `self-dev.yaml` and `dev-review-pr.yaml` legitimately
  diverge today (what checks to run, which diff to review, which extra
  focus paragraphs apply — the divergences are real and stay declared).
- Convert **`self-dev.yaml`** and **`dev-review-pr.yaml`** — the e2e
  and review nodes; `dev` and `pr` stay inline for now (their prompts
  diverge more than they share; forcing them into fragments would mean
  substitution points bigger than the shared text, which is the smell
  that says "different shape").
- Gate the PR on **byte-comparable resolved graphs**: a golden test
  beside `shipped_graphs_test.go` asserts that the resolved, normalized
  JSON (`json.Marshal` of the loaded `*Graph` — the same bytes a
  snapshot would store) of each migrated template is byte-identical to
  a fixture captured from today's hand-written files. The migration
  changes zero runtime behavior or it does not merge.

## Consequences

**Positive**

- The half-dozen proven node shapes get an upstream. The next
  cold-safe-wording-class fix is one edit plus a regenerated golden,
  not a hand sweep with a miss rate.
- Copies become citations: a graph that says `use: e2e-verify` tells
  its reader "this is the proven gate" instead of making them diff two
  40-line prompts to discover it is *almost* the proven gate.
- Zero new runtime concept. Everything downstream of `graph.Load` —
  scheduler, handoff, snapshot, resume, events, fleetops — is
  bit-for-bit unaffected; the snapshot proves it by construction, the
  migration proves it byte-for-byte.
- The trust posture stays one sentence long: trusted code resolves
  files; the planner still never names local resources.

**Negative / trade-offs**

- A graph file is no longer self-contained: reading a `use:` node means
  opening a second file. The disclosure line and `lint` soften this;
  they do not remove it. (Anchors have the opposite trade — see
  Alternatives — and remain available for the single-file case.)
- The resolver is new load-path machinery: raw-YAML splicing, a search
  path, eight new lint rules. It is justified by the 52-of-58 corpus
  evidence, and it would not be justified without it.
- Substitution points are a straitjacket by design. Shapes will
  occasionally want one more knob, and the answer "declare it upstream
  or fork honestly" costs a PR to the fragment where a copy cost
  nothing. That friction is the mechanism working, but it will be felt
  as friction.
- A shipped-fragment edit now ripples through every shipped template's
  golden in one PR — bigger diffs, deliberately. Reviewers must read
  regenerated goldens rather than rubber-stamp them, or the visibility
  mechanism decays into noise.
- DESIGN.md (graph loading, the new `use:`/`with:` keys, the fragment
  file schema, the search path) and the README's graph-authoring
  section must land in the same change that implements this ADR — code
  and DESIGN.md drifting apart is a bug in both.

## Alternatives considered

- **YAML anchors and merge keys (`&`, `*`, `<<:`) — already work
  today.** Kept, honestly credited, and insufficient. Anchors are the
  right tool for intra-file repetition and nothing in this ADR touches
  them. But the measured problem is *cross-file*: 58 distinct graphs
  forking a half-dozen shapes, and YAML defines no cross-document
  anchor, so anchors cannot express the fix at all. They also lack the
  two properties the incident demands: declared substitution points
  (an anchor override replaces wholesale — exactly the drift vector)
  and any lint surface. If the corpus had shown one big graph with
  internal repetition, anchors would win and this ADR would not exist;
  it showed the opposite, and cross-file reuse is the whole
  justification for the machinery.
- **Graph-level includes (import a subgraph of nodes with its edges).**
  Rejected as over-scoped for the evidence. The corpus shows recurring
  *node shapes*, not recurring *subgraphs* — the wiring around the
  shapes is precisely what varies per graph. Includes would import all
  of the hard problems node fragments avoid (id namespacing across
  files, collision policy, how imported edges splice into the host
  DAG, what `depends_on` may point across the boundary) to serve a
  pattern the data does not contain. If a recurring multi-node motif
  ever shows up in a future corpus pass, that is its own ADR, and node
  fragments compose upward into it; the reverse is not true.
- **Wholesale-overridable `prompt:` (fragment as a mere default).**
  Rejected: recreates the copy-variation drift under a name that
  claims reuse — the incident, with better camouflage. Argued in
  Semantics.
- **Runtime resolution (the engine resolves `use:` when the node
  becomes ready).** Rejected. It puts a file read and a failure mode
  into the scheduler's hot path, makes the snapshot either store
  unresolved nodes (breaking "resume never re-reads files") or store
  resolved ones anyway (conceding the point), and buys nothing:
  fragments have no runtime inputs. Load-time resolution is the entire
  reason this feature costs the engine zero.
- **Default values on substitution points.** Deferred, not rejected —
  purely additive later (`substitutions: [{name, default}]`). Cut from
  v1 because the migration needs no defaults (both templates bind
  every point), and an unbound-means-default rule weakens the loudest
  lint in the set (unbound-means-error) before any evidence asks for
  it.
- **Fragments referencing fragments.** Rejected for v1: a fragment's
  `node:` block may not carry `use:` (load error). Single-pass
  resolution, no cycle detection, no partially-resolved intermediate
  states. Nesting is additive later if composition evidence appears.
- **The planner may emit `use:` for shipped-embedded fragments.**
  Deferred, not rejected — the one alternative with a real value case
  (plans that cite proven shapes instead of inventing prompts). It
  stays out of v1 because it crosses the planner-never-names line, and
  the safe variant (trusted Go code maps planned nodes onto shipped
  fragments, agentmap-style) deserves its own ADR with ADR 0012's
  measurement discipline rather than a paragraph here.
- **A fragment hash (or version pin) in the snapshot.** Rejected: the
  snapshot already stores the resolved graph verbatim, which subsumes
  any hash of the inputs to resolution; resume never re-resolves, so
  there is nothing for a pin to protect. The only thing a hash would
  add is a sharper resume-time "sources changed on disk" warning, and
  that courtesy (extending `GraphSHA256`'s check to fragment files) is
  additive whenever it earns its keep.
