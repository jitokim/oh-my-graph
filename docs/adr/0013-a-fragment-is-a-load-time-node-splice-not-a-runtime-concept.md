# ADR 0013 — A fragment is a load-time node splice, not a runtime concept

- Status: Accepted — implemented, with the deviations recorded inline as
  "amended at implementation" (`LoadResult`, `LintFile`'s advisory channel,
  the coordinator-side backstop conversion, the shipped fragment's
  `permission_mode`/`budget_usd`, and the review fragments' `evidence`
  substitution point)
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
  allowed_tools: [Read, "Bash(make *)", "Bash(go build *)", "Bash(go test *)", "Bash(go vet *)", "Bash(git status*)", "Bash(git diff*)", "Bash(git log*)"]
  permission_mode: dontAsk
  budget_usd: 10.00   # runaway insurance, an order of magnitude above a plausible run
  handoff: session
  success_check:
    exit_zero: true
    result_matches: "^PASS$"
    verify: { command: "make local", timeout: 5m }
  retry: { max: 1, on: [nonzero_exit, verify_failed] }
```

> **Update (2026-08-05):** the block above is the fragment **as drafted**, and
> the shipped `graphs/fragments/e2e-verify.yaml` has diverged from it in four
> places. It is left as drafted (this is the record of what was decided) with
> the divergences named here, because a quoted example that no file matches is
> worse than no example:
>
> | drafted above | shipped at v0.4.1 | why |
> |---|---|---|
> | `substitutions: [checks]` | `substitutions: [checks, verify_command]` | the Migration section below, *in this same ADR* |
> | `verify: { command: "make local" }` | `verify: { command: "{{ with.verify_command }}" }` | same |
> | `result_matches: "^PASS$"` | ``result_matches: '^[*_`\s]*PASS[*_`\s]*$'`` | #107 |
> | prompt: "Report exactly PASS … or FAIL with the failing step" | the bare-four-characters wording, naming `**PASS**` as wrong | #107 |
>
> The first two are not drift against a later decision — they contradict
> **this ADR's own Migration section**, which already records that
> backlog-batch's conversion made the command "join `checks` as a declared
> substitution point (`verify_command`)". The example was simply never swept
> when the section below was written. A reader who trusted the example over
> the prose would conclude the shipped fragment had regressed.
>
> The other two are #107: a model emitting `**PASS**` halted a real run, so
> the verdict pattern became markdown-tolerant and the prompt began saying
> so explicitly. Neither touches this ADR's decision — the merge rules, the
> resolution stage and the trust boundary are unaffected by what a proven
> shape's own `success_check` happens to contain.

Note what is *not* a substitution point: `allowed_tools` is the
fragment's own, because the grant is part of the proven shape (Semantics,
below). A using graph that needs a different grant overrides the key
explicitly — it does not get asked to supply one every time. (Amended at
implementation: the shipped fragment also carries `permission_mode` and
the runaway-insurance `budget_usd`, shown above — a gate that must run
non-interactively and its insurance ceiling are part of the proven shape
too, and the budget is the "`budget_usd` on one" convergence the
Migration section enumerates. The shipped grant is also *narrower* than
either pre-migration template's: a gate reports, it never fixes, so it
gets `make` plus read-only `go`/`git` verbs and not the `Bash(go *)` /
`Bash(git *)` wildcards — which would have let a report-only node
`go run` the tree under review and rewrite its history. A stack whose
check verbs differ overrides `allowed_tools` on the using node, where
the disclosure line announces it.)

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

- `graph.LoadFile(path) (*LoadResult, error)` (amended at
  implementation from the draft's `(*Graph, []byte, error)`: the
  disclosure line and the snapshot re-encode decision both need the
  per-fragment resolution record, so the three data travel as one
  result) — read the entry file, resolve every `use:` against the
  fragment location (splice, substitute, overlay, all on the raw YAML
  document), hand the resolved document to the same decode →
  `Validate` path as today, and return a `LoadResult` holding the
  validated graph, **the entry file's raw bytes** (the datum `run`
  needs for `GraphSHA256` and the snapshot), and one
  `FragmentResolution` per resolved `use:` — the disclosure line's
  input, and non-empty exactly when the snapshot must re-encode.
  Whether this replaces the test-only `Load` or sits beside it is
  implementation detail; the rewiring of `run`, `lint` and `--dry-run`
  is not.
- The fail-fast/collect-all duality is preserved, because the codebase
  already defines `Validate` as `Issues()[0]` precisely so the two can
  never disagree. `LoadFile` fail-fasts on the first resolution error; a
  collect-all counterpart (`LintFile(path) ([]error,
  []FragmentAdvisory, error)`, mirroring `Lint` — amended at
  implementation from the draft's `[]error`: the advisory findings this
  ADR names need a channel that is not the issue list, because advice
  must never affect an exit code, and an unreadable file is an I/O
  error with no list to collect) returns **every** fragment issue plus
  every structural issue of the resolved graph, first element identical
  to what `LoadFile` would have failed with. `lint` and `--dry-run`
  call the collect-all form — a migrated `graphs/self-dev.yaml` must
  lint as a whole list, not die on one "unresolved fragment
  reference".

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
- a fragment-body token that *claims* the `with` namespace without
  obeying the grammar — `{{ with.checks | inline }}` (a substitution
  point is bound, not filtered), `{{ with. }}`, `{{ With.checks }}` —
  **load error**, charged to the fragment file. (Amended at
  implementation: the draft judged body tokens with the strict token
  regex, which by construction can only see tokens that are already
  well-formed. A malformed one is *worse* than an undeclared point: it
  can never substitute, so it survives resolution into the spliced
  prompt and reaches the model verbatim — the failure the load-time /
  run-time token split exists to abolish. The body is therefore scanned
  loosely for every `{{ … }}` and each namespace-claiming token judged
  against the same regex `substituteWithTokens` replaces with. Tokens in
  other namespaces — `{{ inputs.x }}`, `{{ artifacts.y | inline }}` —
  and literal `{{ … }}` prose pass through untouched.)
- a declared substitution point never referenced in the fragment body —
  **advisory warning**: harmless at run time, but it is drift smell (the
  body moved and the declaration didn't). (Amended at implementation:
  `LoadResult` carries these too, not only `LintFile`, so `run` prints
  them on the same warning channel `lint` and `--dry-run` use. `run`
  already discloses which fragments it spliced; a file that warned under
  `lint` and went silent under the command that spends money was the
  disclosure contradicting itself. The two *handoff* sweeps stay
  lint-only by contrast: those judge the whole graph and are the
  pre-flight command's job, not a per-run disclosure.)
- a `{{ with.x }}` token in a plain (non-`use:`) node — **advisory
  warning** via `LintPlaceholders`: the runtime will pass it through
  verbatim into a paid prompt.

### Trust and scope

**One location, no search path: the graph file's own `fragments/`
sibling.** A `use:` in `/repo/graphs/foo.yaml` resolves to
`/repo/graphs/fragments/<name>.yaml`, and nowhere else. Resolution is a
pure function of the entry file's path — no cwd dependence.

*Amended at implementation (twice, both corrections to this paragraph).*
First: the name must be **bare**, matching
`^[A-Za-z0-9][A-Za-z0-9._-]*$` and refused before any file is opened.
"Nowhere else" is not self-enforcing — `filepath.Join` cleans lexically,
so an unconstrained `use: ../../evil` resolved to `<repo>/evil.yaml`, and
the file a `use:` names supplies the node's real prompt, tool grant and
`success_check.verify.command`. The constraint is what makes
`fragments/` the review boundary this section claims it is.

Second, a factual correction: this ADR originally cut a
shipped/embedded fragment tier partly on the grounds that "no such
vehicle exists (nothing `go:embed`s `graphs/`)". That was **wrong** —
`graphs/embed.go` embeds the shipped templates, and `oh-my-graph init`
unpacks them, which is how a `go install` user gets any graphs at all.
The consequence was a real regression: `//go:embed *.yaml` does not
descend into subdirectories, so the migrated templates shipped without
their fragments and died at load for every `go install` user. The
pattern is now `*.yaml fragments/*.yaml` and `init` unpacks the tree.
What stays cut is a fragment **search tier** — a precedence order in
which a `use:` not found locally falls back to an embedded copy — which
would still mean a search order, a shadowing lint rule and a local-wins
narrative for a need no graph has demonstrated. Unpacking is not
shadowing: the unpacked fragments are ordinary files in the user's own
`graphs/fragments/`, resolved by the one rule above. Adding a real
shipped tier later remains purely additive, with its own decision about
precedence when it earns one. The
resolver **prints one line per resolved fragment naming the source file,
the fragment's own `description:` and every key the using node
overrides** — the disclosure posture the agent-mapping design
established, extended to overrides so a hollowed `success_check` or
widened `allowed_tools` is announced at every run, and at every `lint`.
*Amended at implementation:* `fragment:` and `description:` were
initially read by nothing. Both are now required — the first must equal
the filename, since the filename is what a `use:` resolves and a
disagreement is a typo no reader would catch; the second is what makes
the disclosure line legible without opening the file. A schema key that
nothing checks is the same silent-mismatch class this ADR refuses
everywhere else.
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
coordinator-owned refusal, not only the structural one — because the
disposition harness demands it: `TestPlannedNodeRefusalsAreReal`
probes every `rejected` row and requires the probe to fail with a
`*PlanError`, while a bare `graph.Parse` refusal surfaces from the
coordinator wrapped as a plain `fmt.Errorf("generated graph is
invalid: %w", …)`, which would fail the harness, not satisfy it.
Mechanically that refusal cannot live in `validatePlannedNodes`
(amended at implementation): `Plan` parses the reply through
`graph.Parse` *first*, and `graph.Validate`'s backstop already refuses
any unresolved `use:`/`with:` there, so no fragment-carrying graph
ever reaches the per-node checks — a case there would be unreachable
code masquerading as a guard. So the backstop refusal is a **distinct
error type** (`graph.UnresolvedFragmentError`, embedding the ordinary
validation error), and the coordinator recognizes it at its
`graph.Parse` boundary and converts it into the `*PlanError`
("planned nodes may not reference fragments") naming the offending
node. The `Use`/`With` rows in the field-disposition table read
`rejected`, probed through that conversion — defense in depth with
the layers stated honestly: the structural backstop *is* the refusal;
the coordinator conversion is the layer the harness can see. The future path stays open and is *deferred, not rejected*: a
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

### Migration: every shipped template holding the shape — structure proven, prompts converged in the open

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
  real and stay declared). Amended at implementation, two points:
  `review-security` also declares `evidence` — the upstream gate's
  `{{ artifacts.<id> | inline }}` reference names a graph-local node
  id, which is wiring, so it binds in rather than being hardcoded into
  the fragment (and it exercises the compose rule: a bound runtime
  placeholder survives resolution untouched); `review-style` does not,
  its shape reading a diff rather than the gate's verdict. And
  `review-style`'s `focus` binds `""` in `dev-review-pr.yaml`, whose
  pipeline adds no extra paragraph — the honest spelling of "none"
  under v1's no-defaults rule.
- Convert **`self-dev.yaml`** and **`dev-review-pr.yaml`** — the e2e
  and review nodes; `dev` and `pr` stay inline for now (their prompts
  diverge more than they share; forcing them into fragments would mean
  substitution points bigger than the shared text, which is the smell
  that says "different shape").

  > **Update (2026-08-09): that was true of `dev` and false of `pr`,
  > and it was never measured.** A proposal to make `verdict:` a
  > first-class schema field raised the prior question — are the 22 of
  > 31 `result_matches` declarations that share a pattern outside
  > fragments because they *cannot* be, or because nobody tried? So
  > every shipped node was grouped by its verdict pattern and the
  > prompts compared word-for-word, longest common suffix, insensitive
  > to line rewrapping (the wrapping differs per file and is not a
  > divergence):
  >
  > | shared pattern | nodes | common suffix, all members | grants |
  > |---|---:|---|---|
  > | ``^[*_`\s]*PR[*_`\s:]*https?://\S`` | 5 | **83 words** (75% of the shortest prompt); 4 of the 5 pairwise 86-100 | 4 identical, 1 wider |
  > | ``^[*_`\s]*(FINDINGS[*_`\s]*:\|CLEAN\b)`` | 6 | 30 words (21%) | 2 kinds |
  > | ``^[*_`\s]*DONE\b`` | 6 | **0 words** | 4 kinds |
  > | ``^[*_`\s]*PASS`` | 5 | **0 words** (`apply1`/`apply2` pairwise 124) | 3 kinds |
  >
  > `pr` is one shape by any reading, so it was extracted:
  > `graphs/fragments/pr-publish.yaml`, cited by `self-dev`'s `pr`,
  > `dev-review-pr`'s `pr` and `backlog-batch`'s `pr-a`/`pr-b`. The
  > substitution point is `publish` — the graph's own instruction
  > (which branch, DRAFT or ready, and a body naming graph-local node
  > ids, which is wiring, the `evidence` precedent). Each binding
  > carries that head verbatim, so all four **resolved prompts are
  > byte-identical to the ones they replaced**: the blast-radius
  > goldens did not move, which is the proof. Nothing is overridden by
  > any using node.
  >
  > `adr-driven-dev`'s `finalize` shares the same 83 words and stays
  > inline: it also applies the last review's remaining flags and gates
  > on an engine-run `make local`, so it carries `Edit`/`Write` and a
  > `verify:`. A node with a second job is a different shape, and the
  > 83 words it shares are the same 83 an inline node is free to write.
  >
  > The three groups left are left for one reason, and it is the one
  > that decides the `verdict:` question. **They do not group by node.**
  > `DONE` covers a repo-specific implementer, a docs-lane implementer,
  > a feedback-loop implementer and an ADR-feedback applier: six nodes
  > of four kinds, with four different grants that agree on nothing
  > but the four characters they must emit — zero shared words. A
  > fragment is a node's behavior; it cannot be a paragraph, so it
  > cannot express "these six nodes share their last paragraph". The
  > closest sub-shapes are real but small: `apply1`/`apply2` share 124
  > words, and they live in ONE file, which is the intra-file case this
  > ADR's Alternatives hand to YAML anchors. The four
  > `FINDINGS:`/`CLEAN` rounds are that same intra-file case — all four
  > live in `adr-driven-dev.yaml` — and they share 21% with the review
  > fragments while carrying `Grep`/`Glob` the fragments deliberately
  > do not, so citing them would mean either narrowing four review
  > nodes' grants or overriding `allowed_tools` at every use — a
  > fragment whose grant every caller overrides is not a proven grant.
  >
  > **Update (2026-08-10): the same question asked of the operator's own
  > hand-written lanes — NO EXTRACTION, and the arrow points the other
  > way.** Full numbers:
  > [`docs/measurements/0013-lane-corpus-has-no-extractable-fragment.md`](../measurements/0013-lane-corpus-has-no-extractable-fragment.md).
  > This machine's run corpus holds **75 distinct lane graphs** built by
  > hand around one `review` → `apply` → `pr` scaffold, and the proposal
  > was to ship that scaffold as a fragment. It does not survive this
  > ADR's own standard, on three counts:
  >
  > - **What repeats is wiring.** `worktree:`, `cwd:`, the `depends_on`
  >   chain and the ids — the exact list Semantics makes a load error in
  >   a fragment. What is left to carry is `type` and a timeout.
  > - **The one prose candidate carries no proof.** The 35 `apply` nodes
  >   collapse to 9 texts whose top two share 82% of their words, which
  >   clears the bar `pr-publish` was extracted on. But they declare
  >   `result_matches` **0/35**, a verdict-first clause **0/35**, and
  >   `allowed_tools` **0/35** — so both halves of the verdict convention
  >   and the grant would have to be *authored*, not extracted. And their
  >   first sentence keys off a `NO FINDINGS` contract neither shipped
  >   review fragment emits (`CLEAN`/`FINDINGS:`), so shipping it beside
  >   them would manufacture the silent mismatch this repo keeps closing.
  >   No shipped template has an apply stage to cite it, either.
  > - **The corpus is a consumer, not a supplier.** Across those 75
  >   lanes, `result_matches` is declared by 0 of 35 `apply`, 0 of 47
  >   `review` and 0 of 75 `pr` nodes, and `use:` appears **zero** times.
  >   Every one of those lanes published a PR with nothing asserting the
  >   PR exists — the failure `pr-publish` was extracted to abolish. The
  >   fix for those lanes is `use: pr-publish`, not a new fragment.
  >
  > One methodological correction to the update above, from the same
  > measurement: the longest-common-**suffix** metric only works where
  > the shared text collects at the tail, which is true of shipped nodes
  > because their verdict clause is last. The `apply` nodes diverge
  > mid-body, so their suffix reads 8 words against 82% word agreement.
  > Suffix length understates convergence off this repo's own templates;
  > it is not a general similarity measure.
- Convert **`backlog-batch.yaml`** too — added at implementation, and
  its omission would have been this ADR's founding complaint surviving
  its own fix: that template held **two more** copies of the cold-safe
  e2e shape (`e2e-a`/`e2e-b`) and two of the review shape
  (`review-a`/`review-b`), so the next cold-safe correction would still
  have been a partial hand sweep. Converting it honestly required one
  fragment change: `e2e-verify` hardcoded `verify: { command: "make
  local" }`, which is this repo's build and not every repo's, while
  backlog-batch is a skeleton you copy into any repo — which is exactly
  why its gates had no `verify:` at all. Overriding `success_check`
  from the using node would have produced what this ADR argues against:
  an unproven gate wearing a proven name. So the command joins `checks`
  as a declared substitution point (`verify_command`) — what only the
  graph can know — and the shape stays the fragment's. `self-dev` and
  `dev-review-pr` bind `make local`, so their resolved graphs are
  unchanged and the equivalence proof below is untouched.
  backlog-batch's conversion is deliberately **not** an equivalence
  claim: its gates gain a real engine-run `verify` (and the graph a
  required `checks_command` input to feed it), which is an upgrade —
  previously the node's own "PASS" was the only thing judging it. It
  therefore has no frozen pre-migration fixture, only a golden.
- Gate the PR twice, on two different fixtures with two different jobs:
  1. **Structure and non-converged behavior fields: byte-identical,
     tested.** The pre-migration files are checked in as
     `testdata/pre-migration/*.yaml` — the actual old files, the
     one-time equivalence evidence. A test asserts that each migrated
     template's resolved graph (`json.Marshal` of the loaded `*Graph`,
     deterministic — the same bytes a snapshot would store) is
     byte-identical to the parsed pre-migration file **after masking
     exactly the fields the PR converges** (`prompt` on the three
     shapes; `allowed_tools` on four — the three review/gate shapes,
     plus `self-dev`'s gate once the fragment's grant was narrowed;
     `budget_usd` on one). Ids,
     edges, handoff, success_check, retry, timeouts — everything else
     is byte-for-byte, or the PR does not merge.
  2. **The converged fields: an explicitly reviewed diff.** The masked
     fields are enumerated in the PR description with their old→new
     values per template, reviewed as the deliberate behavior change
     they are — not laundered through a "zero behavior change" claim.
- The **ongoing blast-radius golden** (Versioning, above) is a separate
  fixture with a separate job: it captures the *post-migration*
  resolved graphs of **all three** fragment-citing templates (a wider
  set than the two-template equivalence mask, on purpose) and fails on
  any future fragment edit until regenerated. One fixture cannot honestly be both the equivalence
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
  them — with one narrow exception added at implementation: a fragment
  file's `node:` block may not contain an alias or a `<<:` merge key
  (load error). That block is walked twice, to check body tokens against
  `substitutions:` and to substitute them, and an alias hides its
  scalars from both walks — so `prompt: &p "{{ with.x }}"` / `other: *p`
  would ship a literal `{{ with.x }}` into a paid prompt: the exact
  silent-verbatim failure the load-time/run-time token split exists to
  abolish. Descending into the alias instead would mean inlining its
  target to substitute into it (the target lives in the cached fragment
  tree that every using node shares), and inlining nested aliases is an
  exponential-expansion bomb on a file read before any validation runs.
  Anchors remain untouched everywhere else, including in a using graph's
  `with:` values. But the measured problem is *cross-file*: 58 distinct graphs
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
