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
from a hand-written one to every consumer downstream of the file loader.

## Decision

### Surface: node-level `use:` with `with:` bindings, resolved by the file loader

A fragment is a **single-node definition file** under a `fragments/`
directory, with declared substitution points:

```yaml
# graphs/fragments/e2e-verify.yaml
fragment: e2e-verify
description: cold-safe e2e gate — session continuation, synchronous checks, verified verdict
substitutions: [checks]
node:
  type: claude-run
  prompt: |
    Continue the work — or, on a retried attempt, start from the branch's
    committed state — and {{ with.checks }}
    Run every check SYNCHRONOUSLY and wait for it to finish — never in the
    background. Your FINAL reply is the verdict: never end the turn on an
    interim report. Report exactly PASS if everything succeeds, or FAIL
    with the failing step.
  allowed_tools: [Read, "Bash(make *)", "Bash(go *)", "Bash(git *)"]
  handoff: session
  success_check:
    exit_zero: true
    result_matches: "^PASS$"
    verify: { command: "make local", timeout: 5m }
  retry: { max: 1, on: [nonzero_exit, verify_failed] }
```

Note what is *not* a substitution point: `allowed_tools` is the
fragment's own, because the grant is part of the proven shape (Semantics,
below). A using graph that needs a different grant overrides the key
explicitly — it does not get asked to supply one every time.

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
```

Reading: the loader replaces this node with the fragment's `node:` block,
substitutes `{{ with.checks }}` with the bound value, overlays the using
node's own fields per the merge rules below,
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
Two tables enforce that split, and they move in opposite directions. The
runtime's `placeholderPattern` (`internal/handoff`, what `Interpolate`
matches) must **not** learn `with` — the token must be gone before the
runtime looks. Lint's `placeholderKinds` table
(`internal/handoff/placeholder_lint.go`) **must** learn it, because that
table is the gate deciding whether `LintPlaceholders` warns about a token
at all: today a stray `{{ with.x }}` is in neither table and ships
verbatim into a paid prompt with no warning. The `with` entry carries a
dedicated message ("resolved at load time — outside a fragment this
ships verbatim") rather than the generic malformed-token one. A substituted value
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
    where it is declared. The same argument that kills a `prompt:`
    override applies in weakened form here — `use: e2e-verify` plus a
    hollowed-out `success_check` is an unproven gate wearing a proven
    name — which is why the resolver's per-fragment disclosure line
    (Trust, below) **names every overridden key**, so the hollowing-out
    is announced at every run, not only visible to whoever reads the
    file.
  - `retry` encodes the shape's known failure modes (e2e retries on
    `nonzero_exit`/`verify_failed` because a cold retry is *designed
    for* — the prompt's first sentence handles it). Default from the
    fragment, explicit override wins.

  "Explicitly overridden" is judged by **key presence in the raw YAML
  mapping**, not by Go zero values — the resolver splices at the
  `yaml.Node` level, before any decode, so `budget_usd: 0` written in
  the using node is an override and an absent key is not, with no
  ambiguity. This is also what makes the next section literally true.

  **Merge mechanics, pinned down** (so two implementations cannot read
  this section differently):

  - **Override granularity is the whole top-level key — subtree
    replacement, never deep merge.** A using node writing
    `retry: {max: 2}` gets exactly `{max: 2}`: the fragment's `on:`
    does not survive. An overridden `success_check` replaces the whole
    block, `verify:` included. Deep merge is rejected because it makes
    the resolved value a function of two files' internal structure —
    the reader of the using file could no longer tell what the node
    does without mentally zipping mappings. If you override a block,
    you own the block; the disclosure line names it.
  - **Substitution is typed when the token stands alone, textual when
    embedded.** If a `{{ with.x }}` token is the *entire* scalar value
    (`checks: "{{ with.x }}"`), the bound YAML node replaces it
    wholesale, preserving type — a bound list stays a list, a mapping a
    mapping. If the token is embedded inside a longer string (the
    `prompt:` case), it is string substitution, and the bound value
    must be a scalar; binding a list or mapping into an embedded token
    is a load error, not a Go-side `fmt.Sprintf` coercion.
  - **Every scalar in the fragment's `node:` block is scanned for
    tokens, recursively** — no per-field whitelist. `prompt:` is the
    common case, not a special one.
  - **The token grammar is exactly `placeholderPattern`'s** — same
    whitespace rules, same body shape, with `with` as the leading word.
    No second grammar to keep in sync.
  - **The resolved document is decoded from the spliced `*yaml.Node`
    directly** (`yaml.Node.Decode` into the same `rawGraph` that
    `decode` fills today), never re-marshaled to bytes and re-parsed —
    a serialize/reparse round-trip is a place for anchors, tags and
    styles to shift, and it buys nothing.

### Resolution happens before validation; `Parse` stays fragment-blind

Resolution cannot be "a front half of `graph.Load`", because `graph.Load`
is on no product path: its only callers are tests
(`internal/graph/shipped_graphs_test.go`). All three real entry points
read bytes themselves — `run` does `os.ReadFile` + `graph.Parse`
(deliberately, to keep the raw bytes for `GraphSHA256`), `lint` calls
`graph.Lint(data)`, `--dry-run` the same. A resolver specified on `Load`
would never run, and every fragment graph would fail at exactly the
commands that matter.

So resolution is a new **path-aware load stage** that those three entry
points are rewired onto:

- `graph.LoadFile(path) (*Graph, []byte, error)` — read the entry file,
  resolve every `use:` against the fragment location (splice,
  substitute, overlay, all on the raw YAML document), hand the resolved
  document to the same decode → `Validate` path as today, and return
  the validated graph **plus the entry file's raw bytes**, which is the
  datum `run` needs for `GraphSHA256` and the snapshot. Whether this
  replaces the test-only `Load` or sits beside it is implementation
  detail; the rewiring of `run`, `lint` and `--dry-run` is not.
- The fail-fast/collect-all duality is preserved, because the codebase
  already defines `Validate` as `Issues()[0]` precisely so the two can
  never disagree. `LoadFile` fail-fasts on the first resolution error; a
  collect-all counterpart (`LintFile(path) []error`, mirroring
  `Lint`) returns **every** fragment issue plus every structural issue
  of the resolved graph, first element identical to what `LoadFile`
  would have failed with. `lint` and `--dry-run` call the collect-all
  form — a migrated `graphs/self-dev.yaml` must lint as a whole list,
  not die on one "unresolved fragment reference".

This also closes a hole that exists **today**: `decode` unmarshals with
plain `yaml.Unmarshal` and no `KnownFields(true)`, so an unknown `use:`
key is silently dropped — a fragment node handed to the current binary
decodes to a node with an **empty prompt** and spends real money running
garbage. Adding `Use`/`With` to `graph.Node` makes the key decode
instead of vanish, and `Validate` refuses it (below); the refusal is the
backstop that turns today's silent smuggle into a loud error on any
path that somehow bypasses resolution.

Consequences, in order of importance:

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
   them empty. One snapshot seam must change to keep that true on disk:
   `newRunRecorder` currently reuses `rawSource` verbatim whenever
   `json.Valid(rawSource)` — and re-running a saved JSON spec with
   `run <path>` is an advertised flow — so a JSON-*authored* graph
   carrying `use:` would snapshot **unresolved** bytes and `resume`'s
   `graph.Parse(snap.Graph)` would hit the refusal above. Therefore the
   snapshot **must store `json.Marshal` of the resolved graph whenever
   any node resolved a fragment**; the rawSource-verbatim shortcut is
   only legal for a document resolution never touched. With that,
   resume works on fragment-free material by construction.
3. **The engine has no fragment concept.** Scheduler, handoff, ledger,
   runfeed, serve, fleetops: unchanged, not even by an optional field.

Lint behavior (`oh-my-graph lint` renders the whole list, as always;
errors first, advisories after — every fragment "load error" this ADR
names, in this list and in Semantics, fail-fasts `LoadFile` and is
*collected* by `LintFile`, the same `Validate`-is-`Issues()[0]`
contract the structural checks live under):

- `use:` naming a fragment with no file at the fragment location —
  **load error**. Nothing can be judged about a node that did not
  resolve.
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
- a `{{ with.x }}` token in a plain (non-`use:`) node — **advisory
  warning** via `LintPlaceholders`: the runtime will pass it through
  verbatim into a paid prompt.

### Trust and scope

**One location, no search path: the graph file's own `fragments/`
sibling.** A `use:` in `/repo/graphs/foo.yaml` resolves to
`/repo/graphs/fragments/<name>.yaml`, and nowhere else. Resolution is a
pure function of the entry file's path — no cwd dependence. A
shipped/embedded fragment tier is **cut from v1**: the earlier draft had
it "riding the same embedding vehicle the example graphs use", and no
such vehicle exists (nothing `go:embed`s `graphs/`; only
`internal/serve`'s UI embeds anything) — a second tier would mean
building an embed pipeline, a search order, a shadowing lint rule and a
local-wins narrative for a need no graph has yet demonstrated. Cutting
it costs the migration nothing (`graphs/fragments/` is already the
shipped templates' sibling, so the shipped templates resolve like any
other graph), and adding a shipped tier later is purely additive — with
its own decision about precedence and shadowing when it earns one. The
resolver **prints one line per resolved fragment naming the source file
and every key the using node overrides** — the disclosure posture the
agent-mapping design established, extended to overrides so a hollowed
`success_check` or widened `allowed_tools` is announced at every run.
For a hand-written graph this adds no new trust surface at all: the
fragment lives in the same repo, at the same trust level, under the
same review as the graph file that names it — a repo that could plant a
hostile fragment could just as easily plant the hostile node inline.

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
the planner LLM never names local resources.** Enforcement is a real
`validatePlannedNodes` case, not only the structural refusal — because
the disposition harness demands it: `TestPlannedNodeRefusalsAreReal`
probes every `rejected` row and requires the probe to fail with a
`*PlanError`, while a `graph.Parse` refusal surfaces from the
coordinator wrapped as a plain `fmt.Errorf("generated graph is
invalid: %w", …)`, which would fail the harness, not satisfy it. So
`Use`/`With` get explicit `validatePlannedNodes` cases returning
`*PlanError` ("planned nodes may not reference fragments"), their rows
in the field-disposition table read `rejected`, and `graph.Validate`'s
unresolved-`use:` refusal remains the structural backstop *behind* the
coordinator check — defense in depth, with the layer the tests can see
in front. The future path stays open and is *deferred, not rejected*: a
plan-time mapping pass in trusted Go code that resolves
**shipped-embedded fragments only** (not attacker-influencable; the
binary vouches for them) would fit the agentmap/skillmap mold exactly —
its own ADR, with ADR 0012's measurement discipline, if the
citing-proven-fragments value case is ever pursued; note it presupposes
the shipped fragment tier that v1 also defers (Trust, above).

**Versioning: a fragment edit changes every user — say so, and make the
blast radius reviewable.** That multiplication is the *feature*: the
cold-safe-wording sweep becomes one edit. But it must be honest. Three
facts govern it:

1. **Running and resumed runs are immune.** The snapshot stores the
   **resolved** graph (`Snapshot.Graph`, the JSON `graph.Parse`
   re-reads), and resume reconstructs from it — it never re-reads the
   entry file, let alone a fragment. This holds only because of the
   `newRunRecorder` requirement above: whenever any node resolved a
   fragment, the snapshot is the re-encoded resolved graph, never the
   raw entry bytes — without that, a JSON-authored fragment graph
   would snapshot unresolved and this claim would be false. A fragment
   edited mid-pause cannot alter the leg that resumes. No fragment
   hash is needed in the snapshot for *correctness*: the resolved
   bytes themselves are already there, which is strictly stronger than
   a hash of them.
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
   graph that must not follow a fragment's evolution forks it under a
   new fragment name — or goes back to an inline node, honestly.

### Migration: two shipped templates in the same PR — structure proven, prompts converged in the open

The proof that the machinery is sound is shipped with the machinery.
One honesty correction first, because the measurement demands it:
**"byte-identical resolved graphs" is not achievable for these
templates, and claiming it would be the ADR's own stated smell.**
Measured across `self-dev.yaml` and `dev-review-pr.yaml`, the
`review-security` and `review-style` prompts share **zero identical
lines** (they are paraphrases of one shape, not variants of one text),
the `e2e` prompts share about one line in ten, `allowed_tools` differs
on all three shapes (`Bash(go *)` vs `Bash(go test *)`), and
`budget_usd` on one. A substitution point that made the resolved output
byte-identical would have to swallow essentially the whole prompt — the
exact reason `dev`/`pr` are left inline. So the migration does not
*preserve* the templates' wording; it **converges** it: the fragment's
prompt (and default grant) becomes the single upstream, and both
templates change wording to match. That is the feature — it is the
cold-safe sweep, done once, on purpose, in review. In the same PR:

- Extract `e2e-verify`, `review-security` and `review-style` fragments
  from the shipped templates, with substitution points covering the
  spans that *should stay* per-graph (what checks to run, which diff to
  review, which extra focus paragraphs apply — those divergences are
  real and stay declared).
- Convert **`self-dev.yaml`** and **`dev-review-pr.yaml`** — the e2e
  and review nodes; `dev` and `pr` stay inline for now (their prompts
  diverge more than they share; forcing them into fragments would mean
  substitution points bigger than the shared text, which is the smell
  that says "different shape").
- Gate the PR twice, on two different fixtures with two different jobs:
  1. **Structure and non-converged behavior fields: byte-identical,
     tested.** The pre-migration files are checked in as
     `testdata/pre-migration/*.yaml` — the actual old files, the
     one-time equivalence evidence. A test asserts that each migrated
     template's resolved graph (`json.Marshal` of the loaded `*Graph`,
     deterministic — the same bytes a snapshot would store) is
     byte-identical to the parsed pre-migration file **after masking
     exactly the fields the PR converges** (`prompt` on the three
     shapes; `allowed_tools` on three; `budget_usd` on one). Ids,
     edges, handoff, success_check, retry, timeouts — everything else
     is byte-for-byte, or the PR does not merge.
  2. **The converged fields: an explicitly reviewed diff.** The masked
     fields are enumerated in the PR description with their old→new
     values per template, reviewed as the deliberate behavior change
     they are — not laundered through a "zero behavior change" claim.
- The **ongoing blast-radius golden** (Versioning, above) is a separate
  fixture with a separate job: it captures the *post-migration*
  resolved graphs and fails on any future fragment edit until
  regenerated. One fixture cannot honestly be both the equivalence
  proof and the drift tripwire; `testdata/pre-migration/` is frozen
  history, the golden is living state.

## Consequences

**Positive**

- The half-dozen proven node shapes get an upstream. The next
  cold-safe-wording-class fix is one edit plus a regenerated golden,
  not a hand sweep with a miss rate.
- Copies become citations: a graph that says `use: e2e-verify` tells
  its reader "this is the proven gate" instead of making them diff two
  40-line prompts to discover it is *almost* the proven gate.
- Zero new runtime concept. Everything downstream of the loader —
  scheduler, handoff, snapshot, resume, events, fleetops — is
  bit-for-bit unaffected; the snapshot proves it by construction, and
  the migration proves structure and non-converged behavior fields
  byte-for-byte while putting the converged prompts into a reviewed
  diff.
- The trust posture stays one sentence long: trusted code resolves
  files; the planner still never names local resources.

**Negative / trade-offs**

- A graph file is no longer self-contained: reading a `use:` node means
  opening a second file. The disclosure line and `lint` soften this;
  they do not remove it. (Anchors have the opposite trade — see
  Alternatives — and remain available for the single-file case.)
- The resolver is new load-path machinery: raw-YAML splicing, a
  path-aware load stage rewiring all three entry points, seven new lint
  rules. It is justified by the 52-of-58 corpus evidence, and it would
  not be justified without it.
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
  file schema, the lookup rule) and the README's graph-authoring
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
  measurement discipline rather than a paragraph here — noting it also
  presupposes the shipped fragment tier that v1 defers (Trust).
- **A fragment hash (or version pin) in the snapshot.** Rejected: the
  snapshot already stores the resolved graph itself, which subsumes
  any hash of the inputs to resolution; resume never re-resolves, so
  there is nothing for a pin to protect. The only thing a hash would
  add is a sharper resume-time "sources changed on disk" warning, and
  that courtesy (extending `GraphSHA256`'s check to fragment files) is
  additive whenever it earns its keep.
