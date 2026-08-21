# oh-my-graph — Architecture & MVP Design (implementation spec)

> A graph-native multi-agent orchestrator whose node runtime is your own
> logged-in `claude` or `codex` CLI, not a direct model API.

## Thesis
oh-my-graph runs each DAG node as a raw subprocess of the selected local model
CLI instead of integrating an Agent SDK or direct API. The run uses the saved
Claude or Codex login and its plan allowance; it never redistributes that login
as a hosted product. Personal/local, bring-your-own-login.

## Language — Go (committed)
Go 1.25+. This tool *is* a subprocess scheduler: `errgroup` + buffered-channel
semaphore for the concurrency cap; `context` cancellation propagates to
`exec.CommandContext` and kills in-flight children on halt-on-fail (awkward in
Python). Single static binary (`go install`).
Deps: stdlib `os/exec`+`context`, `golang.org/x/sync/errgroup`, `gopkg.in/yaml.v3`,
stdlib `flag` (cobra optional/later).

## Run-wide model CLI

One run uses exactly one runtime, selected by the global
`--runtime claude|codex` flag before the subcommand. Claude is the default.
The choice is persisted in `state.json`; resume and browser gate actions load
it, and an explicit mismatch is refused. The scheduler depends only on
`NodeRunner`; `CLIRunner` is its one model-process exec seam and delegates only
argv/session/output protocol details.

Provider session ownership is intentionally asymmetric. Claude assigns a UUID
before spawn because its CLI accepts `--session-id`; Codex publishes the
authoritative thread id in `thread.started`. The scheduler stores whichever id
the selected protocol reports and uses it for `handoff: session`.

Codex reports token usage but no USD total. `CostUnknown` is durable through
the ledger, `state.json`, `events.jsonl`, CLI history views and web UI; it must
never render as `$0`. `agent:` and the goal-level USD budget are refused at
preflight for Codex; a node's `budget_usd` is not (ADR 0026) — it loads with a
warning saying the cap cannot apply and naming the guard that still holds, that
node's own `timeout:` or the runner's default.

## Node runtime mechanics (ground truth — use exactly)

### Claude

A Claude node is one subprocess:
```
claude -p "<rendered prompt>" --output-format json --permission-mode <mode> \
  [ --max-budget-usd <amount> ] \
  [ --setting-sources "" ] [ --plugin-dir <dir> ]… [ --agent <name> ] \
  [ --allowedTools "<comma,joined>" ] \
  [ --tools "<comma,joined>" ] [ --strict-mcp-config ] \
  [ --disallowedTools "<comma,joined>" ] \
  [ --resume <session_id> ] [ --session-id <uuid> ]
```

This is emission order, not just a flag inventory: `runner.buildArgs` appends
in exactly this sequence and `claude_test.go`'s `want` argv pins it
element-by-element, so a reordering is a test failure, not a style choice.
Note where `--max-budget-usd` sits — immediately after `--permission-mode`,
*before* the ceiling flags, because it is not one of them. Note too where
`--plugin-dir` sits — after isolation and before the grant, one flag per
`ToolPolicy.PluginDirs` entry in order, because it restores instruction material
layer 1 withheld before any layer decides what may be done with it. It is what
carries a staged skill corpus (ADR 0017) or a mapped node's staged agent
definition (ADR 0022) into the node, and it is emitted for those nodes only.

The bracketed tool-ceiling flags come from one `runner.ToolPolicy` per node and
are auto mode's alone (see "Auto mode"); a hand-written graph's policy carries
only `AllowedTools`, so its argv is the first line plus `--allowedTools`, then
`--resume` or `--session-id`, and `--max-budget-usd` when the node declared
`budget_usd` (that flag is not part of the ceiling — it rides on
`NodeInvocation.BudgetUSD`, which the scheduler passes for every node, planned
or hand-written).
Every fresh-session node gets `--session-id` with a UUID the
Claude protocol assigned, so the id is published on
`node_started` while the node is still RUNNING and a live view can find its
transcript; a resuming node gets `--resume` instead — the two are mutually
exclusive (`claude --help`, verified 2026-08-02: `--session-id <uuid>`, "must
be a valid UUID").
run with `cwd` = node.cwd. JSON envelope → `session_id`, `result`, `total_cost_usd`.
On a failed run the runner also captures WHY as a one-line
`NodeOutcome.FailureCause` — the envelope's own error report (`errors` /
`is_error`), else the stderr tail on a non-zero exit — so the failure detail
downstream (ledger, events.jsonl, watch, serve) names the cause (a
subscription session limit, say) instead of only "exit code 1".
- **Subscription auth crux:** start from `os.Environ()` and **DELETE
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY` and
  `CODEX_API_KEY`** from the child env (they silently switch to metered API
  billing). One list, no runtime branch: a Claude node drops the OpenAI
  switches and a Codex node drops the Anthropic ones, so neither list can fall
  behind the other. Enforced in code + asserted by a unit
  test on the built argv/env. NEVER `--bare` (disables OAuth). NEVER an Agent SDK.
- Per-node `context.WithTimeout` (default ~20m) so a wedged child can't hang the graph.
- Non-JSON/parse failure = node failure (`NodeOutputError`), never a silent zero result.
- permission modes are a **closed, load-validated set** — the `claude` CLI's own
  `--permission-mode` choices, measured from `claude --help` (claude 2.1.221,
  2026-08-05): `acceptEdits`, `auto`, `bypassPermissions`, `dontAsk`, `manual`,
  `plan`. `dontAsk` is the unattended default the Scheduler substitutes when a
  node declares none; `plan` is read-only; `bypassPermissions` is the loud
  opt-in below. Anything else is a load-time `GraphValidationError` naming the
  node and listing the set, like an unknown `retry.on` cause — the value is
  passed through verbatim to argv, so an unvalidated typo (`dontask`) used to
  kill the node at spawn, mid-run, long after earlier nodes had spent money.
  The set is oh-my-graph's transcription of a third-party CLI's enum: a mode a
  future `claude` adds is refused until this list enumerates it.
- **The permission model is rules + mode, and the rules come from more places
  than the argv.** A tool call is matched against every loaded permission rule;
  if nothing matches, the call resolves to *ask*, and the mode decides what an
  unanswerable ask becomes — under `dontAsk` (our unattended default) it becomes
  a **deny**. So the CLI is already default-deny for us. The reason
  `--allowedTools` still cannot bound a node is not the flag: it is that the
  user's own `~/.claude/settings.json` is loaded as another rule source, and a
  standing `Bash(*)` grant there matches first. `--disallowedTools` subtracts
  and beats a prior allow, but only at bare-tool-name granularity (`Bash`); a
  scoped deny like `Bash(*)` matches a command literally starting with `*` and
  enforces nothing. Measured on claude 2.1.220.
- **Rule sources are selectable.** `--setting-sources` chooses which of
  `user`/`project`/`local` settings are loaded; `--settings` (flag settings) and
  enterprise policy settings are always loaded on top and cannot be dropped.
  `--setting-sources ""` therefore yields a child whose only allow rules are the
  ones on our argv — which is what finally makes a scoped `Bash(git *)` a real
  ceiling rather than a declaration. `--tools` narrows the built-in tool set
  itself (a separate axis from the rules), and `--strict-mcp-config` bounds MCP.
  oh-my-graph applies these ONLY to coordinator-planned nodes — see "Auto mode" —
  and applies the first and the last of them only to a planned run that did not
  type `--accept-loaded-user-config` (ADR 0032).
- Hand-written graphs never carry `--setting-sources`, `--tools`,
  `--strict-mcp-config` or `--disallowedTools`: they are the user's own reviewed
  artifact and are *meant* to run under the user's own settings, hooks and MCP.
  `bypassPermissions` opt-in per node only, loud warning at load, never a graph default.

### Codex

A Codex node uses the same `CLIRunner` process lifecycle with this protocol:

```text
codex exec --json --color never --skip-git-repo-check --sandbox <mode> \
  --config 'approval_policy="never"' [isolation options] \
  [resume <thread_id>] <rendered prompt>
```

`plan` maps to `read-only`, `bypassPermissions` to `danger-full-access`, and
the remaining permission modes to `workspace-write`. Planned nodes and the
assessor add `--ignore-user-config`, `--ignore-rules`,
`project_doc_max_bytes=0`, and `mcp_servers={}` — the assessor always, planned
nodes unless the run typed `--accept-loaded-user-config`, which omits all four
(ADR 0032; `--sandbox` and `approval_policy="never"` are appended outside that
branch and are unaffected). Hand-written nodes and the
planner keep normal Codex config. A `turn.completed` event supplies the final
token usage; the last completed `agent_message` is the node result. A
`turn.failed` event is a failed node even if the CLI process itself exits zero.
`--skip-git-repo-check` preserves the graph contract that a node `cwd` may be a
non-git directory; oh-my-graph already chooses that directory explicitly.

The sandbox is a NETWORK boundary as well as a filesystem one: under
`workspace-write` a node cannot reach the network (measured 2026-08-14,
`gh api rate_limit` → "error connecting to api.github.com", `git ls-remote` →
"Could not resolve host"), so under `--runtime codex` a graph halts at the
FIRST node that publishes. **Where that node sits varies, and it decides what
the failure costs** — the shipped graphs cover all three shapes:

| where | graphs | what a Codex run does first |
|---|---|---|
| last node | `adr-driven-dev` (`finalize`), every user of `graphs/fragments/pr-publish.yaml` (`self-dev`, `dev-review-pr`, `backlog-batch` ×2) | all the work, then fails on that node |
| first node | `apply-flags` (`dev` applies, commits and pushes; `verify` is `permission_mode: plan` and reads only) | fails immediately, having done nothing |
| several | `merge-shepherd` — `gh` in all seven model nodes, starting with `verify`'s `gh pr view`/`gh pr diff`/`git fetch` | fails at node 1, having done nothing |

So "does the work and then fails" is only the last-node case. A graph can also
publish from a node that is not its last (`apply-flags`), which is why the
disclosure names positions rather than a list of "graphs that publish".

Two remedies exist, both per node rather than per run, and both are the user's
call rather than something oh-my-graph selects: `permission_mode:
bypassPermissions` maps to `danger-full-access`, which is not a sandbox at all,
so such a node keeps network AND keyring; and Codex's
`sandbox_workspace_write.network_access=true` lifts the network block, which is
enough for `git push`/`git ls-remote` (what `pr-publish` and `adr-driven-dev`'s
`finalize` need) but not for `gh` **on a machine where a keyring is available** —
`gh` uses the OS keyring only then, and the sandbox denies it ("no oauth token
found for github.com", measured 2026-08-14 on macOS). Where there is no keyring
(a headless Linux box, say) `gh` reads `~/.config/gh/hosts.yml`, a plain file a
`workspace-write` sandbox restricts writes and network for but not reads — so
`network_access=true` probably does fix `gh` there. That case is unmeasured.
The run prints the limitation before any node spends
(`noteCodexRuntimePolicy`) and records it in
[docs/LIMITATIONS.md](docs/LIMITATIONS.md).

## Graph model — YAML (committed)
Edges are inline `depends_on` (no separate edges list — single source of truth).
Parallelism is **emergent**: nodes sharing `depends_on` that don't depend on each
other run concurrently (up to cap). No `parallel-group` type in v1.

Node schema:
```yaml
- id: e2e                     # one safe path element (alphanumerics, '.', '_', '-'; starts alphanumeric) — it becomes the <id>.out artifact filename and a serve URL parameter; same rule as `worktree`, rejected at load
  type: claude-run            # claude-run | gate (v1.1 — see "Gate nodes and resume")
  depends_on: [dev]           # fan-in: all must succeed first
  prompt: |                   # may interpolate {{ inputs.<name> }} and {{ artifacts.<id> }}
    Run make local PORT=8080 and report PASS or FAIL.
  cwd: "{{ inputs.repo }}"
  allowed_tools: [Read, "Bash(make *)", "Bash(git *)"]
  permission_mode: dontAsk
  agent: code-reviewer        # optional (v1.1): run as this Claude Code subagent — see "Node-as-subagent"
  worktree: lane              # optional: run in a managed git worktree shared by every node naming it — see "Worktree isolation"
  budget_usd: 0.50            # per-node cost cap: claude aborts mid-run (--max-budget-usd) + post-hoc FAIL (see Execution engine)
  timeout: 45m                # optional: wall-clock bound on the node's whole run (Go duration; default 20m, no ceiling) — ADR 0007
  handoff: artifact           # artifact(default) | session
  success_check:              # see "Success checks" — verify is the only evidence-grounded predicate
    exit_zero: true
    result_matches: '^[*_`\s]*PASS'  # self-report; never sufficient on its own — shape per "Verdict patterns"
    verify: { command: "make local PORT=8080", timeout: 5m }   # optional (v1.1)
  retry: { max: 1, on: [nonzero_exit] }   # optional
  feedback: { rerun: impl, max: 2 }       # optional (ADR 0010): on a judgment failure, re-run the depends_on path from `rerun` back to this node, at most `max` times — see "Execution engine"
```

Instead of an inline body, a node may cite a fragment (`use:` + `with:`,
resolved away at load time — see "Fragments" below):

```yaml
- id: e2e
  use: e2e-verify             # splice graphs-file-sibling fragments/e2e-verify.yaml
  depends_on: [dev]
  cwd: "{{ inputs.repo }}"
  with:                       # bindings for the fragment's declared substitution points
    checks: run `make local` (build + test + vet).
```

The cited fragment may declare **several** nodes and the edges among them — a
review/repair round, a QA loop — and the citing site is unchanged. It splices
as `<this id>/<the fragment's own id>`, so the node above would become
`e2e/review`, `e2e/apply`, and `depends_on: [e2e]` downstream still means
"after it" (ADR 0027, "Fragments" below). Those internal nodes may themselves
carry `use:`, composing the namespace one hop further (`e2e/review/gate`),
bounded by a citation chain of 3 (ADR 0029).

Graph file has `name`, `version`, `inputs: [..]`, `concurrency: N`,
`on_fail: halt | continue` (default halt — the graph's own failure policy;
see "Execution engine" step 4), `nodes: [..]`.
Full worked example (dev→e2e→parallel reviews→pr) ships as `graphs/dev-review-pr.yaml`;
the two-node bounded review loop ships as `graphs/review-loop.yaml`.

`feedback:` keeps the static graph a DAG: the arc lives outside `depends_on`
and must point strictly backward to a proper `depends_on`-ancestor
(load-validated, with the loop body side-exit-free, gate-free, disjoint from
other bodies, and `max` required ≥ 1 — an unbounded loop on a paid runtime is
unrepresentable). Iteration is a *runtime* phenomenon; full semantics in
ADR 0010 and under "Execution engine" below.

### Fragments — `use:`/`with:`, resolved by the file loader (ADR 0013, 0027, 0029)

A fragment is a **definition file** with declared substitution points — a
proven shape written once, upstream, instead of copy-varied across graphs. It
declares **either** one node's behavior (`node:`, below) **or** a whole
subgraph (`nodes:` + `exit:`, further below — the loop). The citing site is the
same for both: a node with `use:` and `with:`.

```yaml
# graphs/fragments/e2e-verify.yaml
fragment: e2e-verify        # REQUIRED, and must equal the filename (the name a use: resolves)
description: cold-safe e2e gate — session continuation, synchronous checks, verified verdict
substitutions: [checks, verify_command]
node:
  type: claude-run
  prompt: |
    Continue the work — … and {{ with.checks }} …
  allowed_tools: [Read, "Bash(make *)", "Bash(go build *)", "Bash(go test *)", "Bash(go vet *)", "Bash(git status*)", "Bash(git diff*)", "Bash(git log*)"]
  handoff: session
  success_check: { exit_zero: true, result_matches: '^[*_`\s]*PASS[*_`\s]*$', verify: { command: "{{ with.verify_command }}", timeout: 5m } }
  retry: { max: 1, on: [nonzero_exit, verify_failed] }
```

`fragment:` and `description:` are **checked, not decorative**. Both are
required: a `fragment:` disagreeing with the filename is a load error (the
filename is authoritative, so a mismatch is a typo no reader would catch), and
`description:` is what the disclosure line prints at every `run` and every
`lint`, so an empty one costs the reader of a run log the answer to "what is
this node".

**Lookup rule — one location, no search path:** `use: <name>` in
`/dir/graph.yaml` resolves to `/dir/fragments/<name>.yaml` and nowhere else;
resolution is a pure function of the entry file's path (no cwd dependence,
no shipped/embedded tier in v1). The name must be **bare** —
`^[A-Za-z0-9][A-Za-z0-9._-]*$`, refused before any file is opened — so a
separator, a leading dot or a `..` cannot walk out of the `fragments/`
sibling (`filepath.Join` cleans, so an unconstrained name would). The
operational consequence, which follows from the rule and is the thing authors
actually hit: **a graph file stored where no `fragments/` sits beside it can
cite no fragment at all** — a lane written to `/tmp/lane.yaml` has no reachable
fragment and never will, and the unresolved-fragment error says so with the two
fixes (author the graph inside a directory that has a `fragments/` sibling —
`oh-my-graph init <dir>` unpacks one at `<dir>/graphs/fragments/` — or put a
`fragments/` directory, or a symlink to one, beside the graph). Resolution happens on a **path-aware load
stage** — `graph.LoadFile` (fail-fast; also returns the entry file's raw
bytes and one `FragmentResolution` per resolved `use:`) and its collect-all
counterpart `graph.LintLoadFile` (every fragment issue plus every structural
issue of the resolved graph, plus advisories on their own channel, plus the
`LoadResult` itself) — which `run`, `lint` and `run --dry-run` all load
through. Returning the loaded graph from the *same* read is load-bearing, not
a convenience: `lint` and `run --dry-run` also sweep that graph for advisories,
and a path that is not a regular file — `lint <(…)`, `/dev/stdin`, a FIFO —
answers a second read with nothing, which decodes to an empty graph that
passes every check. `graph.LintFile` remains as a thin adapter that drops the
`LoadResult`; a caller that needs both must not read twice. The resolved document
feeds the exact same decode → `Validate` pipeline as a hand-written graph;
`Parse` stays fragment-blind, and `Validate` refuses any node still carrying
`use:`/`with:` as a backstop (the coordinator converts that refusal into a
`PlanError` — the planner may not emit fragments).

**Merge rules** (raw-YAML splice, judged by key presence, never Go zero
values): `id` is always the using node's, and required. `prompt:` alongside
`use:` is a load **error** — customization goes through declared
substitution points or it is a different shape. The behavior fields
(`allowed_tools`, `permission_mode`, `budget_usd`, `timeout`, `handoff`,
`success_check`, `retry`, `agent`, `type`) default from the fragment; a key
written in the using node overrides the **whole** top-level subtree (never a
deep merge). A **single-node** fragment may not declare wiring — `id`, `depends_on`, `cwd`,
`worktree`, `feedback` (load error) — nor a **YAML alias or `<<:` merge key
inside the `node:` block** (load error):
a spliced body has to be walkable in full, or a `{{ with.x }}` hiding behind
an alias would be neither declaration-checked nor substituted and would reach
the model verbatim. Anchors stay sanctioned everywhere else in a graph file,
and in the using graph a `with:` value written as `*ref` resolves normally.
Substitution tokens `{{ with.<name> }}` reuse the placeholder grammar but
resolve once, at load: typed replacement when the token is the entire scalar
(a bound list stays a list), textual when embedded (scalars only). A bound
value may itself carry `{{ inputs.x }}`/`{{ artifacts.y }}` — those survive
resolution and interpolate at run time as always. Unknown `with:` keys,
unbound declared points, undeclared body tokens, and `with:` without `use:`
are load errors — as is a body token that *claims* the `with` namespace
without obeying the grammar (`{{ with.checks | inline }}`, `{{ with. }}`,
`{{ With.checks }}`): those can never substitute, so without the refusal they
would survive resolution into a paid prompt verbatim. A
declared-but-unreferenced point and a stray `{{ with.x }}` in a plain node are
advisories.

**The multi-node form — a fragment that declares a LOOP (ADR 0027).** A
fragment file may declare `nodes:` (a list, each with its own `id`) plus a
required `exit:`, instead of `node:`. Never both, never neither.

```yaml
# graphs/fragments/repair-round.yaml
fragment: repair-round
description: one review/repair round — a review that never edits, then a gated apply
substitutions: [review_focus, review_agent, review_timeout, apply_scope, verify_command]
exit: apply
nodes:
  - id: review
    agent: "{{ with.review_agent }}"
    prompt: "{{ with.review_focus }} …"
    permission_mode: plan
  - id: apply
    depends_on: [review]
    prompt: "Apply this: {{ artifacts.review | inline }} …"
    success_check: { verify: { command: "{{ with.verify_command }}", timeout: 5m } }
```

The invariant is one sentence covering both forms: **a fragment may never name
an id it does not itself declare.** A single-node fragment declares none, so
`id`/`depends_on`/`feedback` stay load errors for it; a multi-node one declares
its own, so edges among *those* are legal and naming anything else — in
`depends_on`, in `feedback.rerun`, or in an `{{ artifacts.<id> }}` token — is a
load error charged to the fragment file. `cwd`/`worktree` stay refused in both:
they are the using node's location, not wiring among declared ids.

Resolution, in the order it happens:

- Spliced ids are **`<using-id>/<internal-id>`** — `round1/review`,
  `round1/apply`. `/` cannot appear in anything anyone writes: in an entry
  graph the loader refuses it in a node `id`, a `depends_on`, a
  `feedback.rerun` **and in any `{{ artifacts.<id> }}` / `{{ feedback.<id> }}`
  token in any scalar** (a binding included — the token is the spelling that
  would otherwise read a loop's internal output from outside, with every other
  check satisfied); a multi-node fragment file is held to the same rule by its
  declared-ids invariant, and a **single-node** one — whose tokens name the
  citing graph and so are checked against no such set — by a namespaced-token
  refusal of its own; `coordinator.validatePlannedNodeID` refuses it in a
  planner reply. So a spliced id can never collide with an authored one.
  `Validate` accepts the joined form as a backstop — it cannot tell a spliced
  graph from a resumed snapshot, and must not learn.
- A multi-node `use:` id may not **collide** with another node's id. The splice
  replaces the using node, so post-splice uniqueness would see only distinct
  ids while every downstream `depends_on: [qa]` and `{{ artifacts.qa }}` was
  rewritten to the loop's exit — past a node literally named `qa`.
- Each internal node is **namespaced before substitution**: its id, its
  `depends_on`, its `feedback.rerun` and every `{{ artifacts.<id> }}` /
  `{{ feedback.<id> }}` token it wrote. A value bound at the using site is
  inserted afterwards and is **never** rewritten — it belongs to the citing
  graph's namespace, and a bound artifact id that names no node there is a load
  error rather than a run-time surprise.
- **Entry nodes** (no internal parent) inherit the using node's `depends_on`;
  `cwd`/`worktree` on the using node **propagate to every** spliced node.
  Entry-hood is decided by the key's *presence*, so an empty `depends_on: []`
  inside a fragment node is a load error rather than a silent opt-out that
  would start the node at the top of the citing graph.
- From outside, the loop is one thing whose value is its exit's: both
  `depends_on: [round1]` and `{{ artifacts.round1 }}` resolve to
  `round1/<exit>`. `feedback: { rerun: round1 }` does **not** — it is a load
  error, because rewriting it to the exit would silently re-run one node for an
  author who asked to re-run a loop.
- `exit:` is **required and never inferred** from the unique sink: inference is
  right only while there is exactly one sink, and when it is wrong it is wrong
  silently. It may not lie strictly inside one of the fragment's own feedback
  bodies, so no citing graph's downstream edge can manufacture a side exit in a
  fragment whose author wrote nothing wrong.
- A multi-node `use:` may declare **wiring only** (`id`, `depends_on`, `cwd`,
  `worktree`, `with`): a behavior key on it is a load error naming the key,
  since there is no coherent way to overlay one node's `success_check` onto
  five. A loop needing different behavior needs a substitution point or a
  different fragment.

**A fragment may cite a fragment (ADR 0029).** A node *inside* a fragment file
may carry `use:`/`with:`, judged by exactly the rules a top-level one is —
`graphs/fragments/gated-lane.yaml` is the shipped instance, and
`backlog-batch`'s lane A is one node citing it. Resolution is recursive descent
carrying a **chain**: the ordered fragment names from the entry graph down to
the file being spliced.

- **The order at each level is namespace → substitute → descend**, and it is
  what makes **parameter pass-through** work rather than something added to it:
  an inner `use:`'s `with:` values are ordinary text in the outer fragment's
  file, so the outer bindings are already substituted into them before the inner
  `use:` is read, and the level below rewrites only its *own* file's text. A
  bound value is never id-rewritten by any level, at any depth.
- **A cycle is a repeat on the CURRENT chain** — a load error naming the cycle
  in order, charged to the file whose `use:` line closes it. Chain membership,
  not a global visited set: a *diamond* (two loops citing one leaf, or one loop
  citing a leaf twice) is the normal case and stays legal.
- **The chain is bounded at 3 citation HOPS** (`maxFragmentChain`), checked
  before the cited file is read, so a runaway is a message and never a hang or a
  stack overflow. Hops are not id segments: three multi-node hops mint a
  four-segment id, three single-node hops mint none, and an alias hop spends the
  budget anyway because what is bounded is how far the loader *walks*. It does
  **not** bound the resolved graph's size — three hops of five-node fragments is
  125 nodes from one `use:` line, and that cost lands on the reader of
  `--dry-run` and of the goldens.
- **Namespacing composes left-to-right by the same join**: `top` + `core` →
  `top/core`, then `+ make` → `top/core/make`. Decomposition stays unique
  because an atom cannot contain the delimiter, which is ADR 0027's property
  consumed at N joins rather than re-decided; the on-disk spelling is injective
  for the same reason (`handoff.SanitizeNodeID`, `a/b/c → a~b~c.out`). There is
  still no bound on an id's *length*.
- **`exit:` is transitive.** If a fragment's exit names an internal node that is
  itself a loop, the effective exit is *that* loop's exit, resolved recursively —
  so a loop still exposes exactly one value from outside at any depth.
  `depends_on` inheritance chains the same way, one level at a time.
- **A nested `use:` name must be a literal.** `use: "{{ with.which }}"` is a
  load error: the chain, the cycle check and the bound are all decided before the
  cited file is read, so a citation whose target came from a binding would make
  the citation graph depend on data.
- **Lookup stays a pure function of the ENTRY file's path at every depth**, so a
  fragment that cites a fragment depends on a file its own author cannot ship
  with it. There is no manifest and no pre-flight completeness check; what is
  owed instead is in the message — an error below depth 1 names **the chain**, so
  a reader who never wrote `e2e-verify` is told which fragment did.
- **A single-node fragment may not cite a multi-node one**: its body is spliced
  *onto* the citing node and declares no id, so there is no namespace to mint
  `<id>/<internal>` in. Citing another single-node fragment is an alias and is
  fine — except that an alias may not write its own `prompt:`: it RELAYS the
  cited fragment's behavior, and one that rewrites the prompt is claiming that
  fragment's name while replacing what it does, which is the same drift the
  citing-site rule refuses one file over. A single-node body cited from *inside*
  a fragment has its tokens namespaced against the citing fragment's declared
  ids, and a token naming an id that fragment does not declare is a load error
  charged to the citing site.
- **A fragment file's own `use:` is judged against the FILE.** The literal-name
  rule, the `prompt:`-alongside-`use:` refusal above and a dead `with:` (a
  binding with no `use:` to bind) are all facts about the file, decidable with no
  citing site in hand, so all three are reported once, against it. Left to splice
  time the same defect arrives charged to the citing node — about text in a file
  that node's author may never have opened.
- **A node that cites a multi-node fragment still cannot carry a `feedback:`
  arc** — wiring only, at every level — and that, not the depth bound, is the
  real ceiling on what nesting folds. An author who needs a gated nested loop
  moves the gate inside the nested fragment, where it is a key on an ordinary
  internal node.
- **One disclosure line per resolution, and a nested resolution is a
  resolution.** A nested line's `NodeID` is the already-namespaced id of the node
  that cited the fragment, so the ids alone say the shape of the tree; the
  parent's line is printed **before** the descent's. `Spliced` names only ids
  that exist in the resolved graph, so a parent line deliberately undercounts a
  subtree containing a nested loop — the lines below it answer "how big did this
  get". `Depth` carries the chain length, stated rather than inferred: a
  resolution's slash count is a *different* quantity, because a single-node hop
  mints no segment, so an alias chain two files deep is a nested resolution whose
  id has no slash at all.

Non-goals, refused rather than deferred quietly: `rerun:` over a whole loop,
loop-until-dry convergence (`max: N` stays the only one), dynamic fan-out over a
runtime-sized collection, a planner emitting `use:`, and per-node overrides on a
multi-node `use:` at any depth.

Downstream of the loader **no fragment concept exists**: `run`, `lint` and
`run --dry-run` all print one disclosure line per resolved fragment (source
file + the fragment's own description + every overridden key, or — for a
multi-node splice, which overrides nothing — the ids it spliced; plus, when a
fragment parameterized its own `allowed_tools` and the citing node bound it,
the **resolved** grant of each spliced node substitution touched. A grant
written verbatim in the fragment adds no clause: one line per difference and
no more — ADR 0013's #196 amendment) plus the same fragment advisories on
the warning channel (`run` discloses what it spliced, so it discloses the
drift smell too; the six *handoff* sweeps stay lint-only), the snapshot stores the re-encoded
**resolved** graph whenever any node resolved a fragment (so resume never
re-reads a fragment; `GraphSHA256` still hashes the entry file's bytes), and
scheduler, handoff, the event stream and every consumer reading it see
exactly the graphs they see today.
Shipped shapes live in `graphs/fragments/` and ship inside the binary
alongside the templates (`//go:embed *.yaml fragments/*.yaml`), so
`oh-my-graph init` unpacks a tree whose `use:` nodes resolve — and re-running
it tops that tree up with payload files it does not have yet (a fragment added
by a later release), keeping every file already there;
`internal/graph/testdata/golden/` holds the resolved goldens — one per
fragment-citing template (`self-dev`, `dev-review-pr`, `backlog-batch`,
`adr-driven-dev`) — that turn any fragment edit into a reviewed multi-template
diff. A multi-node fragment multiplies that blast radius by its node count, on
purpose: one edit to `repair-round` moves four nodes in `adr-driven-dev`'s
golden, and the reviewer sees all four. Nesting adds a hop to that radius: an
edit to `e2e-verify` now moves a node in `backlog-batch`, which never names it —
it cites `gated-lane`, which does. The mitigation is the same goldens, and it
gets harder rather than easier, because the reviewer's diff is two files away
from the file that changed.

## Handoff — artifact default, session opt-in (committed)
- **artifact (default):** engine persists each node's `.result` to
  `~/.oh-my-graph/runs/<run-id>/<node-id>.out`; dependents read via
  `{{ artifacts.<id> }}` (substitute file path by default; `| inline` filter to
  inline content). Robust, inspectable, parallel-safe, one clean session per node.
  Use for fan-in / reviews (many→one conclusions).
- **session (`handoff: session`):** dependent runs `--resume <session_id>` of its
  single session-parent (same cwd/git scope). Use for tight sequential
  continuation (dev→e2e). Validation: a session node must have EXACTLY ONE parent
  — a root node has no session to resume, and a fan-in can't merge sessions;
  both must use artifact. Rejected at load time. A gate parent is likewise
  rejected at load (a gate records no session to resume), and `lint` /
  `run --dry-run` warn when a session child's cwd/worktree differs from its
  parent's.
- **feedback (`{{ feedback.<id> }}`, ADR 0010):** a third namespace beside
  inputs and artifacts, resolving to the feedback declarer `<id>`'s latest
  payload — its result text when the failing execution produced one, else its
  failure detail. It **always inlines** (no path form, no `| inline` filter)
  and resolves to the **empty string until a round has fired**, the one place
  "not there yet" is an expected state rather than a wiring bug; prompts write
  around it ("review feedback follows — empty on the first pass"). A token
  outside the body of `<id>`'s feedback edge is a load **error**, not a lint —
  it would otherwise be silently empty forever. The engine persists the
  payload to `<run-dir>/feedback/<id>.out` (overwritten per round, latest
  wins) so a mid-loop resume can re-seed it — an **internal** file, not a
  consumer contract, in its own directory so it can never collide with an
  artifact (node ids allow dots, so a node named `x.feedback` is legal); the
  `.out` artifact keeps meaning "a *passed* node's result".
  The arc and the token are two halves of one mechanism, so `lint` /
  `run --dry-run` warn when a loop declares the first and never writes the
  second (`handoff.LintFeedbackQuoting`, ADR 0028): if nothing in the body
  except the declarer itself quotes `{{ feedback.<declarer> }}` in its
  **prompt**, the re-run is handed the prompt it already ran, produces the same
  output, and the declarer fails again for the same reason — twice the money,
  the same result, and nothing else in the engine has anything to say about it.
  The warning lands on the rerun target, because that is where the missing line
  goes. Advisory, not a load error: a loop whose re-run reads the repository
  rather than the reply is a legitimate, if rare, shape. For **planner output**
  it is a plan **refusal** instead (`coordinator.validatePlannedFeedbackQuoting`
  — the same advisory-here/refusal-there split `LintFeedbackReach` has): the
  planner is asked for the arc and the quote in one prompt sentence, nobody runs
  `lint` on a graph `auto` planned and ran in the same breath, and the
  correction — one placeholder, empty on the first pass — costs nothing even
  when the refusal is wrong.
  Note the reach of *any* of these sweeps: they are printed by `lint` and
  `run --dry-run` only. A plain `run` loads the graph and starts spending
  without them, which is why a defect this sweep can see is still paid for by an
  operator who did not lint first.

## Node-as-subagent (`agent:` — hand-written graphs, plus coordinator auto-mapping)
A node may set `agent: <name>` to run as one of the user's OWN Claude Code
subagents rather than as plain `claude -p`: the review node runs as *your*
`code-reviewer`, with its system prompt, its tools and its model. The mechanism
is one flag — `NodeInvocation.Agent` becomes `--agent <name>`, which `claude`
resolves against the existing `~/.claude/agents` and `<cwd>/.claude/agents`.
There is no oh-my-graph-side agent registry, and there must never be one: the
user's definitions are the single source of truth.

**Load-time validation rejects only surrounding whitespace** — both the
whitespace-only `agent: "  "` and the padded `agent: " reviewer "`, which would
otherwise be handed to `claude` verbatim and fail to resolve for a reason the
YAML does not show (`validateAgentNames`). Nothing else about the name is
checked: whether it *resolves* depends on the machine and the checkout, not on
the graph file, so a graph valid on one machine would otherwise be invalid on
another.

**An unresolvable name is a node FAILURE, not a fallback.** An earlier draft of
this section claimed a missing agent degrades to plain claude. That was measured
and is false: `claude -p --agent <unknown>` writes
`--agent 'x' not found. Available agents: …` to **stderr**, exits **1**, and
prints **nothing at all on stdout** — so there is no envelope, and the node fails
as a `*NodeOutputError`. The design keeps that failure rather than implementing
the fallback, for two reasons. Detecting it would mean string-matching a CLI
error message, which is version-coupled in exactly the way ADR 0001 warns about;
and a node the graph asked to run as your reviewer, silently running as generic
claude instead, is a *different node* producing a plausible-looking review. A
loud failure is the smaller harm. What the runner does add is the CLI's own
stderr to the error (`NodeOutputError.Stderr`), because that message names every
agent that IS available — turning a dead end into a fix.

**No longer mutually exclusive with the auto-mode tool ceiling's Layer 1**
(ADR 0022, 2026-08-12). `--setting-sources ""` still disables *discovery* of the
user's agent definitions (E6's neighbour, E2, re-confirmed at CLI 2.1.228), so a
bare `--agent <name>` cannot resolve under it. A raw plan still rejects `agent:`
outright; the one path that puts the field on a planned node — coordinator
auto-mapping, below — no longer pays for it by dropping Layer 1. It stages the
matched definition into the run's own directory and supplies it with
`--plugin-dir`, which reaches the node without reopening `--setting-sources`, so
a mapped node **keeps every ceiling layer**. Until 2026-08-12 it dropped Layer 1
and was measured to lose its declared scope with it. The plan printout says what
each mapping costs before anything runs. Layer 1 still cannot be extended to
hand-written graphs without dropping `agent:` with it: nothing stages a
hand-written graph's definition.

**Tool reconciliation: a claim only where there is a measurement.** For a
hand-written graph, oh-my-graph does not parse the subagent's frontmatter and
does not reconcile its `tools:` with the node's `allowed_tools` — that path
never passes `--tools`, so E6's result does not cover it and no claim is made;
both files are the user's own artifacts, so it is a usability question there.
A coordinator-MAPPED node is different: it runs `--agent` *plus* the full
`--tools`/`--allowedTools` ceiling — exactly E6's measured configuration, where
frontmatter tools did not widen past `--tools` — and on top of that the
coordinator refuses to map any agent whose frontmatter declares a tool outside
the node's own planned `allowed_tools` (the skip and its reason are printed).

**Coordinator auto-mapping (`auto` and chat graph turns).** After a plan
validates — never before, and never by the planner LLM, which keeps getting its
`agent:` rejected — trusted code scans `~/.claude/agents` — **the user's own
directory only, never the repository's**, since a scanned definition is now
copied into the node's `--agent` (ADR 0022 §3.7, measurement (l)) — and maps
planned nodes onto the
user's own agents by a deliberately conservative name-token rule: exact token
or ≥4-rune prefix between node id and agent name, exactly one candidate or
nothing (ambiguity is silence, not a guess; no fuzzy scoring, no description
matching). Scan failures are silent so zero-config stays zero-config;
`--no-agent-mapping` turns the whole thing off, `--accept-loaded-user-config`
turns it off too (the staging guarantee rests on Layer 1 — "The operator's
opt-in"), and `--no-agent <name>` declines
one agent while the rest still map; every decision made — including a decline —
is shown in the printed plan before execution, with what each mapped node gave
up named on its own line. ADR 0004 §4 originally deferred this
pending E6 and a tool bound — both now hold for the mapped configuration (see
the previous paragraph); its third condition, an explicit opt-in flag, was
traded for the printed-disclosure-plus-opt-out above, accepting that an `auto`
run's behaviour may now depend on agent files the user forgot they had —
visibly, in the plan printout, not silently at run time.

## Worktree isolation (`worktree:` — hand-written graphs only)
By default every node runs in the tree oh-my-graph was invoked from (or its
`cwd`), which is fine for read-only fan-out but broken for parallel EDITS:
lanes serialize on one checkout, a node's commit can sweep in the user's own
untracked files, and a node can switch branches under the user's feet (the
auto-branch bug). `worktree: <name>` is the root fix:

- The engine creates the worktree ONCE per unique name per run —
  `git worktree add ~/.oh-my-graph/runs/<run-id>/worktrees/<name>
  -b omg/<run-id>/<name> HEAD` — off the invocation repo's HEAD. The managed
  path lives under the run directory, NEVER inside the user's checked-out
  tree.
- ALL nodes sharing a name run in the SAME worktree (a lane's
  dev → e2e → review → pr shares one isolated checkout); nodes with
  DIFFERENT names get DIFFERENT worktrees and edit in parallel with no
  shared-tree race. A node's `success_check.verify` inherits the worktree as
  its default cwd, so evidence is gathered where the work happened.
- A node with NO worktree field keeps today's exact behaviour — fully
  backward compatible. `worktree` and `cwd` are mutually exclusive, and the
  name must be a single safe path element (it becomes both a directory and a
  branch segment); both are load errors (`validateWorktrees`).
- Cleanup at run end (`cmd/oh-my-graph`, after `Scheduler.Run`, on a fresh
  context so a halted run still cleans up) never loses work: a worktree
  holding uncommitted changes is left in place entirely (git refuses to
  remove it; forcing would discard the changes), a branch carrying commits
  beyond its base is retained after its worktree dir is removed, and only a
  branch provably still at its base is deleted. Every retention is reported
  as a one-line note. A retained branch is a resume contract, not a dead
  end: `Acquire` is disk-aware, so a `resume`d leg (`--retry-failed`
  included) re-declaring the name reuses the managed worktree dir when it
  still exists (validated as a worktree of the invocation repo), else
  re-attaches the retained branch (`git worktree add <path> <branch>`, no
  `-b`) so the lane continues its committed state, and only creates fresh
  with `-b` when neither survives. A lane adopted either way is never
  judged empty at cleanup — its branch is always retained. What still fails
  loudly is a foreign directory squatting on the managed path: adopting an
  unknown checkout could mix or reset work that is not the run's own, which
  is exactly what ADR 0005's work-preservation rule forbids.
- Handoff artifacts still persist to `~/.oh-my-graph/runs/<run-id>/` exactly
  as before — the worktree isolates the node's WORKING TREE, not its result.
- **Auto-planned nodes may not set `worktree`.** Provisioning is not a tool
  call, so no permission mode or ceiling layer ever sees it;
  `validatePlannedNodes` rejects the field like `cwd`.

**Who runs git — a third exec seam, not the NodeRunner.** Provisioning is
neither a claude invocation nor an evidence command, so it gets its own seam
in `internal/worktree`: a `Provider` interface (`Acquire(ctx, name) (path,
error)`, idempotent per name), with `GitManager` (prod — the third of the
program's exactly four process-spawning objects (ADR 0006 added the fourth),
env-scrubbed via
`internal/childenv` because `git worktree add` fires the repo's own hooks),
`RefusingProvider` (the `schedule.Options.Worktrees` default: a forgotten
injection fails loudly) and `FakeManager` (tests — the scheduler's worktree
path stays spawn-free in CI). Cleanup is deliberately not on the interface:
the Scheduler only asks where a node runs; teardown is the CLI's job against
the concrete `GitManager`. See ADR 0005.

## Execution engine
Scheduler = Kahn on `depends_on`, but maintains a **ready set** run concurrently:
1. Validate on load: all depends_on ids exist; no cycles (DFS colour); fail fast
   with `GraphValidationError` naming the node.
2. in-degree per node; seed ready set with in-degree 0.
3. Launch every ready node as a goroutine under `errgroup`, gated by a buffered
   semaphore (size = min(graph.concurrency, globalCap=10), default 4). On each
   SUCCESS, decrement dependents' in-degree; newly-0 join ready set.
4. Node failure → retry policy; still failed → **halt (default)**: cancel shared
   context → kills in-flight children → exit non-zero naming the failing node.
   A sibling the halt cancelled is recorded with the causal story —
   `cancelled: run halted after node "X" failed` — not the raw Go
   "context canceled" its cancellation surfaces as.
   `--continue-on-fail` (opt-in) prunes only the failed subtree. A graph can
   opt in itself with graph-level `on_fail: continue` (default `halt`; any
   other value is a load-time `GraphValidationError` naming the valid set,
   like an unknown retry cause) — what a batch of independent lanes declares
   so one lane's failure can't cancel every other lane's in-flight work,
   without the operator remembering the flag. Precedence is an OR, resolved
   in the scheduler (`effectiveContinueOnFail`, next to
   `effectiveConcurrency`) so `run`/`auto`/`resume` all share it: either the
   flag or the graph saying continue means continue; the flag cannot force a
   halt onto a graph that declared continue, nor the graph cancel a flag the
   operator passed.
5. Done when ready+running are empty.

One outcome is exempt from step 4 entirely: a subprocess killed by the
subscription's **session limit** is a pause, not a node failure (ADR 0009).
The runner classifies it (`NodeOutcome.SessionLimited`); the scheduler then
stops launching, drains in-flight siblings instead of cancelling them, records
the limited node nowhere, and returns `*LimitPausedError` → exit code 2 with a
`resume --retry-failed` hint. Full semantics under "Gate nodes and `resume`"
below — the limit rides the same pause/drain machinery a gate does. Claude only:
under `--runtime codex` nothing sets `SessionLimited`, so the same situation is
an ordinary node failure — settled: the pause is a promise of the Claude
runtime, not of the engine, so no runtime owes a session-limit signal
(ADR 0009 "Scope", closing #171).

**Feedback edges (ADR 0010) — the fourth intercepted signal.** A node may
declare `feedback: { rerun: <ancestor>, max: N }`: when it fails for a
*judgment* cause (`verify_failed`, `result_mismatch`, `nonzero_exit` — the
fixed built-in trigger set; infrastructure and spend faults such as an
interpolation error or `budget_exceeded` fail final immediately), after its
own retries are spent, and rounds remain, the arc **fires** instead of
failing: the scheduler persists the declarer's output as the feedback
payload, writes the non-final trio (a ledger row pricing the failing
execution, a non-terminal *marker* snapshot record — round k, no verdict —
and `node_retried` on the stream; never `node_failed`, which stays terminal),
then re-arms the loop **body** — every node on any `depends_on` path from
`rerun` up to and including the declarer — by re-seeding in-body in-degrees
(out-of-body parents ran once and stay satisfied) and relaunching the target.
The signal rides the same interception seam as `pauseSignal`, `limitSignal`
and `rejectSignal`: a pause elsewhere suppresses the relaunch exactly as it
suppresses dependents, and the durable marker means a later resume re-enters
the loop at round k (body records below the marker's round are superseded
and re-run; `max − k` rounds remain). Re-runs start fresh sessions (the
retry cold-start rule) and share the lane's worktree within a leg. When the
rounds are spent the next judgment failure is final — the FAIL's detail
reads `feedback exhausted after N rounds of <target> → <declarer>: <cause>`
— and the existing story runs unchanged: `on_fail` halts or prunes, and
`resume --retry-failed` salvages by re-arming the loop (clearing the
declarer's FAIL also clears its body's retained PASSes and resets the rounds
budget — never by re-running the declarer alone against unchanged
artifacts). Worst case is legible from the file: `(1 + max) × |body|` runs
per arc — rounds, not attempts, so a body node that declares `retry` is
charged on top of every round it runs in — each under its own
timeout/budget/tool ceiling; the ledger prices
every execution with a `feedback round k/N` note.

**A review gates only when its caller pairs a narrowed check with an arc
(issue #151).** The shipped review fragments pass on *both* their verdicts
(`CLEAN` and `FINDINGS:`) — judging, not being clean, is the job — and nothing
else in the engine reads a verdict: `depends_on` is a success edge, and
`retry`, `feedback` and `on_fail` all hang off failure. So a passing
`FINDINGS:` gates nothing and every node below the review runs anyway, a `pr`
node included. *Stopping* on findings is therefore one shape, and it is a
**pair**: the using
node narrows `success_check.result_matches` to the clean verdict *and*
declares `feedback: { rerun: <implementing node>, max: N }`. The narrowing
alone would make the run where the reviewer did its job the run that reports
FAIL with nothing repaired; the arc turns that same failure into a repair
round carrying the findings in `{{ feedback.<id> }}`, so only an exhausted loop
is final. Both keys belong to the calling graph — ADR 0013 forbids a fragment
from declaring `feedback`, so a fragment cannot gate on its caller's behalf —
and `internal/graph`'s `TestAGatingReviewCarriesItsRecoveryArc` holds the
shipped graphs to the pair by matching a real `FINDINGS:` reply against each
review node's *effective* pattern rather than reading how it was spelled.
`backlog-batch`'s lane A gates (`rerun: dev-a, max: 1`, body of 3, so 6 body
runs over its 2 rounds — and 8 executions worst case, because `e2e-a` inherits
`retry: { max: 1 }` from `e2e-verify` and a retry is charged on top of its
round: 2 `dev-a`, 4 `e2e-a`, 2 `review-a`); lane B, `dev-review-pr` and
`self-dev` stay advisory by recorded
choice — the last two also because their parallel review fan-out cannot hold
an arc at all without tripping rule 3's side-exit refusal, each review's
sibling sitting outside any body that contains `e2e`. *Repairing* findings has
a second shape that is not this one and does not stop anything:
`adr-driven-dev`'s unconditional apply node after each review round, which
fixes what the round found without a verdict ever failing — it costs a node on
every clean run, where an arc costs nothing unless the review fails.

**A fan-in reviewer's arc reaches one branch — `lint` says which
(`graph.LintFeedbackReach`, advisory).** When the declarer fans in from
several producers, `rerun` still names one node, so the body may exclude a
producer whose artifact the declarer judges: the loop then re-judges an
unchanged file every round and halts with the defect untouched (issue #118 —
five defects, all in the excluded branch, ~$14 of re-running the healthy
one). `lint` and `run --dry-run` warn for each `depends_on` producer outside
the body, naming the declarer, the rerun target, the unreachable producer and
— when one exists and still validates — the covering target to aim at
instead. Two parents are skipped: a **gate**, which rule 4 forbids a body from
ever containing, and a parent that is an **ancestor of the rerun target**,
which is rule 3's carve-out in topology — it sits upstream of the whole loop,
its output already flows into the body, and the loop re-runs its consumers.
That second skip is what keeps `spec → impl → review` with `rerun: impl`
quiet: warning there would advise `rerun: spec`, which re-runs the acceptance
criteria every round and re-judges the implementation against criteria that
just moved. #118's producer is a *sibling* of the target, on no path to it,
and still warns.

It stays an **advisory**, never an eighth load rule. The skips narrow the
sweep to the shapes rules 3 and 4 do not bless, but neither is a proof of
intent: a sibling *corpus* root is topologically identical to #118's sibling
*work*, so a legitimate graph can still be warned about, and refusing it would
break working hand-written graphs to catch a planner's mistake. What the sweep
sees is `depends_on`; which files a prompt actually judges it cannot see —
issue #118's reviewer named its two artifacts by literal path, not through
`{{ artifacts.<id> }}` — and the printed advisory says so itself rather than
asserting the defect as fact. A producer left outside the body that *asks* for
the payload with `{{ feedback.<id> }}` is already a load error, not an advisory
(the placeholder rule above).

**Auto mode refuses what `lint` only warns about.** The same sweep is a
*refusal* for planner output (`coordinator.validatePlannedFeedbackReach`), and
the planner prompt carries the rule so the ordinary plan never draws it: when a
reviewing node depends on more than one producer, `rerun` must name a node the
loop reaches all of them from — normally their nearest common ancestor — and a
parent that is only stable context belongs *upstream* of the rerun target, not
beside it. Neither reason for the advisory's restraint survives the change of
author: unreviewed output has no author to weigh a warning, and a refused plan
is not a lost run — the reply already faces the whole field-disposition ceiling,
and a refusal buys one corrected re-plan (`repair.go`) carrying the validator's
own sentence. The rule is deliberately narrower than "every producer must be in
the body": it fires **only when the sweep found a covering target**, so the
refusal always arrives with the edit that fixes it, and the two-independent-roots
shape — where the corpus-versus-work indistinguishability actually bites — is
never refused, because no aiming of the arc could repair it. The topology is
computed once, in `graph.LintFeedbackReach`; the coordinator reads its
advisories and decides only what to do with them.

retry: flat re-run up to `max` on causes in `retry.on`, fresh session (never
resume a failed one). For a `handoff: session` node this means a retried
attempt does not resume the parent session either — it starts cold, which
`lint` warns about up front and the passing attempt's ledger detail states.
A retry is no longer a byte-identical re-spawn, though: when the attempt it
repeats **was judged** — a failed `success_check`, or a verification that ran
and said no — the retried attempt's prompt carries that attempt's own reply,
quoted as nonce-fenced data (ADR 0020, `internal/schedule/retryfeedback.go`).
Exactly **one** prior attempt is ever carried and it never accumulates: every
attempt's prompt is rebuilt from the interpolated node prompt, so the added
cost is flat in the attempt index rather than triangular, bounded at 8000 bytes
of reply, cut head-and-tail with the cut announced. The check itself is **not**
quoted — not its expression, not the detail that embeds it — because feeding
back a `result_matches` regex teaches the cheapest possible pass, which is to
print whatever it matches; the node is told its attempt did not pass, told not
to argue the verdict, and pointed back at its own instructions. Causes that
rendered no verdict on the reply carry nothing: a spawn error, an interpolation
error, `budget_exceeded`, and a verification that could not be *completed*, the
same `isJudgmentFailure` split a feedback arc uses. A `handoff: session` retry
still starts cold — unchanged — and the quote says so out loud, because the
text it hands back is the node's own words out of a conversation it can no
longer open. Across processes, `resume --retry-failed` re-reads the reply from
`<run-dir>/failed/<node-id>.out` for each node it clears, gated on the
snapshot's `judged` flag: the FAIL record (and with it the ledger row and the
capped detail) is dropped by the retry leg, so that file is the only account of
the attempt that survives the boundary. A seeded execution is a retry like any
other and starts cold on the same terms — a `handoff: session` node does not
resume its parent there either, and the row that prices it says so — and the
`failed/` file it was seeded from is removed once that execution passes, so the
directory never holds a losing reply beside a winning artifact. The causes are a closed set — `nonzero_exit`,
`run_error`, `timeout`, `output_error`, `budget_exceeded`, `verify_failed`,
`result_mismatch` (the `graph.Cause*` constants) — and an unknown cause is a
load-time `GraphValidationError`: it would match no failure the scheduler ever
produces and silently mean "never retry". A **negative `max`** is refused at
load for the same reason: the scheduler adds `max` to the attempt count only
when it is positive, so `max: -1` is discarded and the node runs once — the
identical quiet non-retry, from a value no author can have meant. `max: 0` is
legal and untouched: it IS the extra-attempt count a node declaring no retry
already has.

`timeout` is the newest of the seven and is **not** part of `run_error`
(ADR 0024). A node killed by its own bound — the `timeout:` it declared, or the
runner's 20-minute default — used to be classified as a run error alongside a
spawn that never started, so one token covered two failures that want opposite
policies: a failed spawn is worth an immediate cheap re-attempt, while a timeout
is the one failure that always burns its *whole* budget before dying, so
retrying it costs another full bound. The engine does not decide which of those
you have, because it cannot: a timed-out node is either a slow machine or an
instruction that cannot finish at any timeout, and nothing in the error tells
them apart. It gives the token; `retry: { max: 1, on: [timeout] }` is the
author's opt-in, and a graph that says nothing retries nothing, exactly as
before. The boundary is whose clock ran out: only a deadline the runner minted
for THIS node earns the token (a `*runner.NodeTimeoutError`), while a deadline
inherited from the run's own context stays `run_error` — retrying inside an
already-expired context would burn every attempt against a deadline that has
passed. A graph that listed `run_error` and *wanted* timeouts covered writes
`on: [run_error, timeout]`; that token narrowed, and it is the one thing here
that is not purely additive. The other clock is not this one: a
`success_check.verify` command that times out arrives through the verify seam
and is `verify_failed`, not `timeout` — splitting it too is deliberately not
decided (ADR 0024 §5).

budget_usd (post-hoc verdict — the backstop layer): a node that passes its
success_check is then judged against its declared `budget_usd`. Actual cost strictly greater than the budget
→ `NodeBudgetError` (node id + budgeted + actual), which flows through the exact
same path as a failed success_check: ledger row FAIL with the overspend in
`Detail`, retry only if opted in, halt-on-fail by default. A non-positive
`budget_usd` means "no budget declared" and is never enforced. The budget is
judged *after* `Handoff.PersistOutput`, so a node that did useful work before
blowing its budget still leaves its artifact on disk — the budget changes the
verdict, never handoff semantics. Its retry cause token is `budget_exceeded`,
deliberately distinct from `nonzero_exit` so a pre-existing retry policy can
never re-spend an already-blown budget by accident.

The verdict above is the **post-hoc backstop layer**. On top of it there is now
a **native mid-run kill**: a positive `budget_usd` is passed to the node's
subprocess as `claude --max-budget-usd`, and the CLI aborts the run itself the
moment its own running spend crosses the budget. Verified against claude 2.1.220
(free — a couple of trivial `-p` probes, no metered call): the abort exits
non-zero yet still prints a parseable `--output-format json` envelope whose
`subtype` is `error_max_budget_usd`; the runner maps that to
`NodeOutcome.BudgetExhausted`, and the scheduler raises the same
`*NodeBudgetError` the post-hoc check raises, so a native kill fails as
`budget_exceeded` — not the generic `nonzero_exit` its exit code alone would
imply — keeping the retry contract intact (a bare `nonzero_exit` policy never
re-spends a budget-killed node). The bound is **per `claude -p` invocation**,
which is exactly one oh-my-graph node: a resumed session does *not* re-count its
parent's spend (verified empirically), so it fits a per-node budget even under
`handoff: session`. A natively-killed node persists **no** artifact — the run
was interrupted before a result existed — whereas a post-hoc overspend completed
and keeps its output; the two failure shapes are deliberately distinct.

Both layers are kept because they cover different overshoots. `--max-budget-usd`
stops the *next* API call once cumulative spend crosses the budget, but cannot
un-spend a call already in flight, so a single expensive turn can still land
over budget — the post-hoc verdict is what catches that at exit. What remains
outside both layers: the overshoot of that one in-flight call (the CLI accounts
*between* calls, not sub-call), and any cross-node or whole-graph budget — each
node's cap is independent. Finer-grained, sub-call cost observation would still
need `--output-format stream-json` + incremental parsing, an ADR-level change to
the one-envelope `NodeRunner` contract; that alone stays deferred — mid-node
kill itself no longer does. Deriving a wall-clock timeout from `budget_usd` via
an assumed $/minute rate was still considered and rejected: the conversion rate
would be fabricated, so it would look like enforcement while enforcing nothing.
The per-node `context.WithTimeout` (20m default) remains as a wall-clock bound
orthogonal to cost — and since ADR 0007 a node may replace the default with its
own `timeout:` (a Go duration, validated at load like the verify timeout but
with no ceiling: the node timeout IS the critical path, and raising it is the
point of declaring it). An undeclared timeout keeps the 20m default, so no
node is ever unbounded.

A **turn-denominated budget** (`budget_turns:` → `claude -p --max-turns N`) was
proposed as a supplement to `budget_usd` — dollars are a hard cost ceiling but
a poor scoping unit (hard to estimate per task), while turns are a unit humans
can estimate — and is **rejected for now**: the installed CLI's `claude --help`
documents no `--max-turns` flag (verified 2026-08-02), so the engine could ship
only the schema, not the enforcement. See ADR 0007 for the recorded design and
the revisit condition.

## Success checks — evidence-grounded verification (v1.1)
`success_check` is a conjunction of predicates, cheapest first, evaluated only
after the runner returns an outcome. **All configured predicates must pass.**

| predicate | judges | trusts the node? |
|---|---|---|
| `exit_zero` | the subprocess exit code | no |
| `result_matches` | a regex over the node's `.result` text | **yes — self-report** |
| `verify` | a command the ENGINE runs, by its own exit code/output | no |

An entirely empty `success_check` still means "exit zero is enough". The v0.1
contract is unchanged for every existing graph; `verify` is purely additive.

`result_matches` is kept — it is a cheap, useful filter — but it is demoted in
the docs and in the ledger's language to a **secondary signal**: a node passes
by emitting "PASS", which is narration, not evidence. `verify` is the only
predicate that observes state outside the LLM's own account of itself, and it is
what a node whose success is externally observable should carry.

```yaml
success_check:
  exit_zero: true
  result_matches: '^[*_`\s]*PASS'        # optional, secondary — see "Verdict patterns"
  verify:
    command: "go test ./... -run TestFoo" # required; run via the platform shell (`sh -c` on unix, `cmd /c` on Windows)
    cwd: "{{ inputs.repo }}"              # optional; default = the node's own cwd
    timeout: 2m                           # optional; Go duration, default 2m, ceiling 10m
    expect_exit: 0                        # optional; default 0
    output_matches: "^ok\\s+github"       # optional; regex over the FULL combined stdout+stderr — no length bound, so an anchored pattern still means what it reads as
```

**`command` and `cwd` interpolate exactly like a prompt** — the scheduler runs
them through the same `Handoff.Interpolate` — and the resolved `command` is
then what the second exec seam runs through your shell. So the placeholder you
choose decides whether a MODEL's text lands on that command line.
`{{ artifacts.<id> }}` is safe by default: with no filter it is the persisted
`.out` FILE PATH, which the engine computes, and a command that reads the file
gets the same content without ever quoting it into argv. The two shapes that
do splice model text are `{{ artifacts.<id> | inline }}` — the node's own reply
— and `{{ feedback.<id> }}`, which always inlines and has no filterless form;
`handoff.LintVerifyInlining` warns on both — in `command` only, since a `cwd`
becomes `exec.Cmd.Dir` rather than a command line, and in no prompt, where
inlining is the designed use. It is an advisory, not a load error, for the
reason every sweep in that package is one: only a person can write what it
condemns. A hand-written graph is the user's own reviewed artifact, and a
planned graph carries no verification a MODEL wrote either —
`validatePlannedNodeVerify` refuses a planner-authored `verify:`, so the only
one it can carry is the `--verify-cmd` the user supplied, which is
advisory-eligible like any other command line but is still the user's own
string.

```go
type SuccessCheck struct {
	ExitZero      bool          `yaml:"exit_zero" json:"exit_zero,omitempty"`
	ResultMatches string        `yaml:"result_matches" json:"result_matches,omitempty"`
	Verify        *Verification `yaml:"verify" json:"verify,omitempty"` // nil = no evidence check
}

// Verification is a pointer field so "absent" and "zero" are distinguishable,
// and ExpectExit is a *int so an explicit 0 is expressible.
type Verification struct {
	Command       string `yaml:"command" json:"command,omitempty"`
	Cwd           string `yaml:"cwd" json:"cwd,omitempty"`
	Timeout       string `yaml:"timeout" json:"timeout,omitempty"` // parsed with time.ParseDuration at LOAD time
	ExpectExit    *int   `yaml:"expect_exit" json:"expect_exit,omitempty"`
	OutputMatches string `yaml:"output_matches" json:"output_matches,omitempty"`

	timeout time.Duration // Timeout parsed once at load; read via TimeoutDuration
}
```

**Both tag sets are load-bearing, and a new field needs both.** The `yaml` tag
is how the field is authored; the `json` tag is how it survives a run. The
snapshot stores the normalized graph as re-parseable JSON (`Snapshot.Graph`),
so a field with no `json` tag round-trips under Go's default key name at best,
and a field tagged `json:"-"` would silently vanish from every resumed run —
the graph the second leg executes would quietly differ from the one the first
leg did. `graph.go` states this at the struct itself; copy the pair, not just
the `yaml` half.

`SuccessCheck.IsZero()` must also test `Verify == nil`, and `Validate` must
reject an uncompilable `result_matches`, an empty `command`, an unparseable
`timeout`, a timeout over the ceiling, and an uncompilable `output_matches` — at
load, naming the node (`GraphValidationError`), never mid-run. Changing this
struct touches loader, validator, shipped example graphs and tests together.

### Verdict patterns — `result_matches` reads raw markdown

`result_matches` is a Go regexp matched against the model's **raw final reply**
(`outcome.Result`, the CLI's `result` field), with no normalization whatsoever:
no trimming, no markdown stripping, no case folding, and no flags the engine
adds of its own. `^` and `$` therefore anchor to the start and end of the whole
reply text unless the pattern itself opens with `(?m)`, which no shipped
pattern does — and "Where the verdict may sit" below is the measurement of why
not.

The pattern is **compiled at load**, exactly like `verify.output_matches`: a
pattern that does not compile is a `GraphValidationError` naming the node and
quoting the pattern, so `lint` and `run --dry-run` refuse the graph and no node
is spawned. A verdict pattern is a declaration, and a broken declaration is
knowable from the file alone — it used to be diagnosed only inside the
scheduler's evaluation, which runs after its own node has been paid for.

Models emit markdown. A prompt that says "begin your reply with PASS" leaves
the model free to write `**PASS**`, and it does — that exact reply has failed
`^PASS` and halted a real run of a shipped graph, twice in one release, on a
node whose suite had actually passed. The graph author sees the flake as luck,
because earlier runs of the same graph passed.

So a verdict pattern is written in two halves, and both are load-bearing:

- **The prompt is the instruction.** Demand the bare token as the very first
  characters of the reply, and say that markdown emphasis is wrong — name the
  wrong shape (`` `**PASS**` is WRONG ``) rather than only describing the right
  one. Then say **where everything else goes**, because "no preamble" only
  forbids; it does not offer. A node that finishes its work and has one
  exception to report — a step that found nothing to do, a caveat — will put
  that sentence somewhere, and if the prompt never names a place, it goes on
  top. That is how run 20260807-144514 opened `merge`'s reply with "no local
  branch to delete" and was recorded FAIL over a merge that had landed. Every
  shipped prefix verdict carries the offer: *anything you need to qualify
  goes AFTER the verdict, never before it* — as one unbroken line, so
  `grep -c "Anything you need to qualify" graphs/*.yaml graphs/fragments/*.yaml`
  is a sweep that cannot silently miss a node. That sweep counts **24
  declarations, covering 33 runtime nodes** — a fragment states the clause
  once and every node citing it gets it, which is the point: six of the 24
  live in `graphs/fragments/`, in five files, and carry fifteen of the nodes
  between them.
  The gap widened by two when `adr-driven-dev`'s two repair rounds became two
  `use:` of one multi-node fragment (ADR 0027): the same 33 nodes, four fewer
  places to correct the sentence in. The
  four whole-reply pins
  (`haiku-smoke`'s `write`, the `e2e-verify` fragment, `apply-flags`'s
  `verify`, and `coordinator.plannedVerdictPattern`) say the opposite and must —
  their reply is the token *and nothing else*, so for them the answer to "where
  does the caveat go" is "nowhere, and a caveat means this is not the verdict
  you have".
- **The pattern is the backstop.** Wrap the token in the decoration class
  ``[*_`\s]`` — emphasis, code span, whitespace — while keeping the anchor:
  ``'^[*_`\s]*PASS'`` for a prefix verdict, ``'^[*_`\s]*PASS[*_`\s]*$'`` when
  the whole reply must be the verdict and nothing else,
  ``'^[*_`\s]*APPLIED[*_`\s]*:'`` when the token carries punctuation a model
  may wrap (`**APPLIED:**` and `**APPLIED**:` both match).
  Single-quote the YAML scalar so the regex reaches the engine byte-for-byte.

The class is exactly the decoration a model wraps around a bare token it was
told to emit alone: `**PASS**`, `` `PASS` ``, a stray leading or trailing
newline. It deliberately stops there. Block structure that changes the *shape*
of the reply — a `## PASS` heading, a `- PASS` list item, a sentence of
preamble — is the prompt's job to prevent, not the pattern's to absorb; every
character added to the class buys tolerance with a little of the anchor's
meaning. The class is also **case-sensitive**, like the whole match: a node
told to reply `ship it` fails on `Ship it`, so a lowercase or mixed-case token
needs the prompt to say the casing is literal.

Never relax the `^` into an unanchored match to fix a markdown failure: an
unanchored `PASS` also passes a FAIL report that mentions the word, which turns
a cheap filter into a check that cannot fail. The tolerance belongs in the
character class, not in dropping the anchor. Every shipped graph and fragment
follows this shape, and so does the pattern the auto-mode planner hands its
branch-assertion check node (`coordinator.plannedVerdictPattern`, where a
planned node may not set `verify` and the pattern is the whole gate); a new
verdict token joins it.

#### Where the verdict may sit — measured, not assumed

That rule is not a guess. Every `result_matches` failure this project has on
disk was replayed against the anchor, the anchor dropped, and the anchor
weakened to `(?m)` (per line rather than per reply). The corpus: 187 runs, 218
verdict-bearing node executions, **22 result_matches failures**. Count the
executions from each run's `events.jsonl`, not from `state.json`'s `nodes` map —
that map keeps only the last record per node id, so retries and feedback rounds
overwrite themselves and the same corpus reads as 211 / 18.

Of those 22, **16 were the check working** — three of them the literal promise
reply ("I'll report when the stress test finishes", "I'll wait for that
waiter", "4 planner processes are running") and the rest honest
`FAIL`/`NOT READY`/`BLOCKED` reports. The remaining six were *pattern*
misjudgements, all in the same direction — a reply rejected for where its
verdict sat, or for how it was decorated, and not for what it said. Whether the
work behind each was done is a separate question, and ADR 0019 keeps its
buckets apart: one is world-confirmed (`merge`, PR #135), one is a synthetic
fixture, and four are read-only nodes nothing on disk can confirm today:

| what went wrong | count | still reachable? |
|---|---|---|
| `**PASS**` against a bare `^PASS` | 1 | no — the decoration class fixed it |
| verdict first, `$` pin, evidence trailing | 3 | not in a shipped graph; all three were ad-hoc graphs whose prompt said "reply with exactly X" *and* asked for the evidence |
| verdict present, on a later line | 2 | yes — one fixture, one real (`merge`, PR #135) |

The 196 passes were not audited as a body, so "nothing was wrongly admitted" is
not a claim this table can make. What was audited is the node the question is
about: `merge`'s 18 patterned PASSes are 16 `MERGED <sha>` — every SHA an
ancestor of `main` today — and 2 `WITHHELD <reason>`. The corpus's one known
false PASS is `merge` too, and it is the reply in the paragraph below: it ran
under `{ exit_zero: true }`, before this pattern existed.

Replaying the same 22 with the anchor **dropped** (leading `^` stripped, any
`$` kept) admits 11 replies — and 8 of those 11 are among the 16 correct FAILs,
because `NOT READY` contains `READY` and a `FAIL` report discussing the run's
`PASS` lines contains `PASS`. Eight false passes to buy three true fixes is the
trade this section already refuses.

Replaying them with `(?m)` admits 3 and none of the 16, which looks like a
bargain and is not. **Both halves of that number mislead:**

- Only 2 of the 3 are later-line verdicts. The third (`^COVERED$`) has its
  verdict on the *first* line; `(?m)` admits it by releasing the `$`. Applied
  to a whole-reply pin, `(?m)` does not move the anchor — it deletes the pin,
  and "and nothing else" stops meaning anything.
- "None of the 16" is the wrong population. The 22 are replies that already
  failed; the promise reply is not among them, because before the pattern
  existed it **passed**. Constructed against `merge`'s own pattern, `(?m)`
  accepts a plan that lists both verdicts as bullets, an indented code block
  quoting the instruction, and a sentence carrying the PR head SHA — five
  promise replies in all, one of them the prompt's own `MERGED 4f2a1c9` example
  quoted back. `^` rejects all five.

The payload is not the lock. A SHA is seven hex characters, and a model that
has merged nothing can type them; what `^` actually rejects is the *preamble* a
promise cannot do without — the sentence saying why the verdict cannot be given
yet, which must come before the quote of what it will say. **Position is the
lock, at every node, for a payload verdict exactly as for a bare word.** So no
pattern in this repo is line-anchored, and `(?m)` appears in none of them.

Where a false FAIL is unusually expensive, make the re-run cheap instead of
widening the check. `merge-shepherd`'s `merge` was the one node whose re-run was
not safe — it re-entered `gh pr merge` on an already-merged PR under a grant too
narrow to look — so it gained a step 0 that establishes PR state first and two
read-only commands (`gh pr view`, `git merge-base`) to do it with, the ancestry
check reading `origin/main` only after the pull that refreshes it. ADR 0019
records the decision, the refused relaxation, and what would overturn either.

**A verdict nothing checks is not a verdict.** A prompt that asks for one and
a `success_check` of `{ exit_zero: true }` is the same "a prompt is not a
mechanism" failure as the markdown one above, and it fails in the more
expensive direction. `merge-shepherd`'s last node was asked for the merge
commit SHA and replied "CodeRabbit's re-review is mid-flight, so I'm waiting
… Poller armed (30s interval, 12 min cap). I'll proceed as soon as it lands."
The claude process exited 0, nothing matched anything, the node passed, the
ledger recorded a successful merge step, and nothing was merged; the operator
found out because `git pull` did not move. A false FAIL costs a retry, so it
announces itself — a false PASS writes a wrong row in the ledger and stays
there. So: **if a node's prompt names the answer it must give, its
`success_check` must be able to tell whether it got one.**

The reply that has to be rejected is rarely a wrong answer — it is a promise
of future work. A node's turn ends when it replies, so "I'll follow up once X
lands" is the node reporting work that will never happen. Two halves again:
the prompt says the turn ends at the reply and demands the state that is true
*as it replies*, never a plan to continue; the token carries a **payload the
node could not name before doing the work** — a commit SHA, a PR URL, a file
path, a count of comments actioned.
``MERGED[*_`\s:]*[0-9a-f]{7,40}\b`` is an assertion; `MERGED` alone is a word a
model writes about a merge it intends.

Be precise about what that buys, because it is easy to over-read: a payload
bounds **promises**, not **lies**. `result_matches` still reads only the
node's own reply (LIMITATIONS.md #7), and a determined model can copy a SHA
out of an upstream artifact. What it removes is the *honest* failure — the
reply a model writes when it has genuinely not finished and is telling you so
— which is the one that actually happened, and the only one a pattern can
catch. Bounding lies takes `success_check.verify`, where the engine runs a
command of its own.

The payload must also be **shaped**, not merely present. Three of these
patterns first shipped with a payload that any prose satisfies, which is the
same hole one refactor further in: ``ADR[*_`\s:]*\S`` accepts `ADR: pending`;
``TRIAGED[*_`\s:]*[0-9]`` accepts `TRIAGED 3 of 7 so far, the rest to
follow`, because a partial count is a count; `APPLIED:` carries no payload at
all. Ask of the class what reply it *lets through*, not what reply it was
written for: a path needs its extension (``\S+\.md\b``), a count needs the
rest of its line to be empty (``[0-9]+[*_` \t]*(\n|$)``, and a prompt that
says the first line holds the count and nothing else), a SHA needs its length
(``[0-9a-f]{7,40}\b``). A payload that a qualifier can survive next to is not
a payload — it is a decoration on the same promise.

**Two legitimate outcomes, one pattern.** Some nodes have two right answers —
`merge` either merged or deliberately did not, and refusing to merge past an
unfinished review is the graph working, not failing. That is an anchored
alternation, not a relaxed anchor:
``'^[*_`\s]*(MERGED[*_`\s:]*[0-9a-f]{7,40}\b|WITHHELD[*_`\s:]*[[:alnum:]])'``.
Both outcomes pass, everything else fails, and the anchor still means what it
meant. Note that the shaping rule binds the negative branch too: closing it
with `\S` lets the separator class hand its own last character back as the
reason, so `WITHHELD:` and `WITHHELD —` both pass carrying no reason at all —
hence `[[:alnum:]]`, which the decoration a real reason is written behind
(`**WITHHELD**: CodeRabbit has not concluded`) still reaches. Two rules keep
it honest. Pick tokens where neither is a prefix of the
other and neither contains a separator a model may render differently —
`WITHHELD` over `NOT-MERGED`, because a model told to write `NOT-MERGED` will
sometimes write `NOT MERGED` and the decoration class deliberately does not
absorb that. And pick a word for the negative outcome that names a **decision**
rather than a state (`WITHHELD`, not `PENDING`), so the token itself cannot be
honestly used for "not yet" — otherwise the alternation re-admits the promise
it exists to reject. A graph whose green run can mean either outcome must say
so in its header: the ledger says the node passed, and only its artifact says
which.

**Three outcomes, two of which pass — and the token that is deliberately
absent.** A node that *waits* has one more outcome than a node that decides:
the thing it waited for concluded well, it is stuck on something no waiting
reaches, or it had not concluded when the timeout arrived.
`merge-shepherd`'s `recheck` — the re-wait for the check rollup and the review
after triage may have pushed a fix — is the shipped case, and it writes all
three. The pattern is
``'^[*_`\s]*(RECHECKED|UNSETTLED)[*_`\s:]+[0-9a-fA-F]{7,40}\b'``; the stuck
outcome is `LATCHED <sha> — <what>; unblock: <act>`, which appears in the
prompt and **not** in the pattern. Three rules, none of them new:

- **Leaving a token out of the pattern is how a graph halts on purpose.** A PR
  only a person can unstick must not reach the human gate, so `LATCHED` is
  written by the prompt, matched by nothing, and fails the node — the same
  shape as `triage`'s `BLOCKED`. The absent token is part of the grammar; a
  reader who sees only the regex sees two thirds of it, which is why the prompt
  names all three. It also bounds what the two-halves rule can deliver: a
  pattern cannot *shape* a token it must reject, so `LATCHED`'s second half —
  the `unblock:` act — is prompt-side only, and what holds it is a test
  (`internal/graph/shipped_graphs_test.go`), not a regex.
- **Split the failing outcomes by the operator's next ACTION, not by
  severity.** This node used to call its halting verdict `BLOCKED` and define
  it as RED — "a judgment that something is wrong with the code". That filed
  things wrongly in both directions: a workflow run awaiting a maintainer's
  click halted as if the code were bad, while four conditions that need a
  person and nothing else — a required context with no reporter, a queued run
  nobody approved, a rate-limited bot that will not review again until asked, a
  branch that conflicts with its base — were polled for the full timeout and
  then passed to the gate as `UNSETTLED`, a word that means *wait longer*. The
  axis that decides a wait's honesty is LATCHED vs SELF-RESOLVING (ADR 0021),
  so that is the axis the tokens split on, and the halting one carries the act.
- **Factor the payload, don't weaken a branch.** Both passing branches share
  one shaped payload rather than each arguing for its own, because the SHA is
  what the node is *for*: a verdict about "the checks" that cannot say which
  commit they ran against is precisely the ambiguity the node was added to
  remove. Contrast `merge`, whose two branches assert different things and so
  need `MERGED`'s SHA and `WITHHELD`'s `[[:alnum:]]` separately.
- **A state word is admissible only when the state is an outcome.** The rule
  above prefers a decision over a state, and `UNSETTLED` is a state — the one
  exception, and it is bought, not free. It passes, so a run whose checks were
  merely slow reaches the gate instead of discarding a paid pipeline; and a
  node that writes it without waiting is not caught by the pattern. What
  bounds that is what lies downstream: a gate the operator must still open,
  and a `merge` whose answer to `UNSETTLED` is `WITHHELD`. A premature
  `UNSETTLED` costs a glance and a refused merge; it can never cost a merge of
  unchecked code. Where a premature answer *would* be expensive — `RECHECKED`
  — the token names a completed check, not a state. The rest of that price is
  paid in the graph's terminal state, and it should be said out loud: an
  `UNSETTLED` the operator approves over ends the run GREEN, having merged
  nothing. That outcome is not what the state word bought — before `recheck`
  existed, `merge` answered `WITHHELD` to those same unfinished reviews, which
  also passes, and three of the five hits that motivated the node ended exactly
  there. Only two of those three were slow: a re-review still `PENDING` on the
  triage commit (PR #111, PR #137). The third (PR #134) was a *new*
  `CHANGES_REQUESTED` against it — a latch, which `recheck` answers `LATCHED`,
  halting before the gate rather than reaching it as an `UNSETTLED`. Admitting
  a state word makes the green-run-merged-nothing class *rarer* — the common
  case, where restarted checks conclude in minutes, now merges — but it does
  not remove it, and a graph that buys this exception owes its header that
  sentence. The exception is also narrower than it was: since 2026-08-11
  `UNSETTLED` may only be written over something that was still MOVING, so the
  latched conditions that used to reach it now halt instead.

Widening the separator class between a token and its payload is the one
tolerance worth buying node by node, and the currency is what a false FAIL
costs there. `merge` admits `-` and `—` (`MERGED — 4f2a1c9`) because its retry
re-enters `gh pr merge` on an already-merged PR under a grant too narrow to
look at what happened, so a false FAIL is an operator's morning, not a
re-run; the SHA still carries the assertion, so nothing is given up. Every
other node pays a retry, and keeps the narrow class.

**What reuse can and cannot deduplicate here.** A `use:`/`with:` fragment
(ADR 0013) puts a verdict rule in one place exactly when the whole NODE is one
shape. Six shipped fragments carry their own — `e2e-verify`, the two review
shapes, `pr-publish`, `repair-round` and `gated-lane` — and the three prefix
verdicts among the first four cover ten runtime nodes between them. It reaches no further, and ADR 0013's Migration update is
the measurement: the patterns still repeated in the templates are shared by
nodes that share nothing else. The six nodes declaring ``^[*_`\s]*DONE\b`` have
**zero** words of prompt in common and four different tool grants. A fragment
is a node's behavior, not a paragraph, so no mechanism hands those six one
clause — and none should, because each writes its own token, its own payload
and its own "if it is not finished" branch. The fourth whole-reply pin is
further out of reach still: `coordinator.plannedVerdictPattern` is not in a
graph at all but a Go string rendered into the planner's prompt for the planner
to copy character-for-character into JSON, so nothing graph-side can share it.
What keeps the two spellings of that one idiom from drifting is therefore a
test, not this paragraph: `TestPlannedVerdictPatternMatchesE2EVerifyFragment`
(`internal/coordinator`) reads `graphs/fragments/e2e-verify.yaml` out of the
embedded payload and fails unless its `result_matches` is byte-identical to the
constant. Reuse could not deduplicate them; a `go test` can still pin them.

The same limit holds outside this repo's templates, and it was asked once more
of a hand-written corpus: 75 lane graphs on one machine ending in a `pr` node,
33 of them carrying a full `review` → `apply` → `pr` scaffold, proposed as a
fifth fragment. **Nothing was extracted** (ADR 0013's 2026-08-10 update;
`docs/measurements/0013-lane-corpus-has-no-extractable-fragment.md`). The
repeated part is `worktree`/`cwd`/`depends_on`/`id`, which a fragment may not
carry; the one candidate with enough shared prose declares neither half of the
verdict convention — `result_matches` 0 of 35, verdict clause 0 of 35 — so both
halves would have been *written* rather than extracted, which is the silent
mismatch this section exists to prevent, not an instance of reuse. Nor is the
corpus merely innocent of the convention: no `apply`, `review` or `pr` node in
it declares `result_matches`, `use:` appears zero times, and the 32 nodes that
do declare one (in other roles) are hand-retyped `e2e-verify` patterns of which
**all 32 drifted** — 19 to a bare `PASS`, which, since `result_matches` is a
search, passes on "the suite did not PASS". That is what a corpus that adopted
the shapes by copy-paste instead of by `use:` looks like from the inside, and
it is the argument for the fix being to cite the four shipped shapes.

The rule is stated here and swept for by `lint` and `run --dry-run`
(`handoff.LintVerdicts`), because a rule only DESIGN.md knows is the same
"a prompt is not a mechanism" shape one level up. Two advisories, both cheap
and both drawn from a real miss: a prompt that demands a token (`START your
reply with…`) under a `success_check` with no `result_matches`, and a
`result_matches` declared without `exit_zero` — which silently deletes the
exit-code guard a node had for free while it declared no check at all
(`SuccessCheck.IsZero`), so a "fix" that adds a predicate can remove one.
They stay advisories: neither can judge whether a payload is shaped tightly
enough to reject a promise, which is the part that still needs a reader.

The engine's matching semantics stay deliberately dumb — normalizing the reply
in Go (trim, strip emphasis, case-fold) would change what every existing
`result_matches` in the wild means, so it is an ADR-scale question, not a patch.
Until such an ADR exists, the idiom above is the fix.

**Where it runs in the node lifecycle** (`schedule.runNode`):
`Handoff.ResolveInputs → NodeRunner.Run → exit_zero → result_matches → verify →
Handoff.PersistOutput → budget_usd → RunState.RecordNode + RunLedger.Record →
enqueue dependents` (the same order `schedule.runNode`'s own doc comment
fixes). Ordering is
deliberate: the in-memory predicates are free, the verification command is not,
and a node that crashed should not have a command run against the wreckage.

**Who runs it — a second exec seam, not the NodeRunner.** A verification command
is not a claude invocation, so it does not belong behind `NodeRunner` (that
interface exists so `FakeRunner` can stand in for claude; teaching it to also
run arbitrary shell would give it two reasons to change). It gets its own,
narrower seam in `internal/verify`:

```go
type Request struct {
	Command string
	Cwd     string
	Timeout time.Duration
}

type Result struct {
	ExitCode int
	Output   string // the FULL combined stdout+stderr — never truncated
}

type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}
```

- `ShellVerifier` (prod) is the second of the program's exactly four
  process-spawning seams (ADR 0002; the third is `worktree.GitManager`, ADR
  0005; the fourth is `browser.ExecOpener`, ADR 0006) and the only object in
  `internal/verify` that spawns anything. Injected by `cmd/oh-my-graph`, never constructed by
  the scheduler.
- `RefusingVerifier` is the `Options.Verifier` default:
  a scheduler test that forgets to inject one gets a loud failure instead of a
  real spawn. `FakeVerifier` (scripted, keyed by command) is what tests inject,
  so the whole verify path stays spawn-free in CI.
- **`Result.Output` is judged, so it is not truncated.** `output_matches` is
  applied to everything the command printed. Truncation is a presentation
  concern and lives where the output is retained or rendered — the ledger's
  DETAIL column caps it (`schedule.capDetail`), and a `*TimeoutError`, which
  can be wrapped and held, keeps only a marked tail. Bounding the judged value
  instead would silently narrow the graph's predicate: the seam prepends
  `…(earlier output truncated)…` when it cuts, so an anchored pattern like
  `^ok\s+github` could never match a chatty command. Memory is not the trade it
  looks like — `CombinedOutput` has already buffered the whole thing before any
  cut could apply.
- Selection is data-driven and the caller stays ignorant: the scheduler asks the
  injected `Verifier` and never learns which kind ran. A second verification kind
  (`file_exists:`, `git_clean:`) arrives as another `Verifier` behind a composite
  that dispatches on the declared kind — no scheduler change. v1.1 ships exactly
  one kind: minimal implementation, sufficient interface.

**This narrows the "only CLIRunner touches `os/exec`" invariant, on
purpose.** The invariant's restated form: *exactly four objects may spawn a
process — `runner.CLIRunner`, `verify.ShellVerifier`,
`worktree.GitManager` (see "Worktree isolation") and `browser.ExecOpener`
(ADR 0006) — each behind its own injected interface, and no other package
imports `os/exec`.* Both purposes survive: the subscription-auth scrub still
has exactly one home per spawner, and the engine is still fully testable with
zero spawns. See ADR 0002, ADR 0005 and ADR 0006.

**The env scrub applies to verification commands too.** `verify: { command:
"claude -p ..." }` is legal and would otherwise run on metered API billing if
the key happened to be set. All four provider API-key switches are
deleted from the verification child's env by the same shared policy the runner
uses (`internal/childenv`), asserted by its own unit test.

**Failure and retry.** A failed verification is a `*NodeCheckError` with
`Predicate: "verify"` and a detail carrying the exit code and a truncated tail of
the command's output — so the ledger says *why*, not just *that*. The retry
cause token is `verify_failed`, joining `nonzero_exit` / `result_mismatch` /
`output_error` / `run_error` / `timeout` / `budget_exceeded` — the full closed set of
`graph.Cause*` constants `retry.on` accepts — so
`retry: { max: 1, on: [verify_failed] }` works.
A verification that times out or cannot spawn is a node failure, never a silent
pass. Cancellation propagates: the shared run context reaches the verification
child, so halt-on-fail kills it like any claude child.

**Auto-planned nodes may not set `verify`.** The command is arbitrary shell that
runs outside every guard the coordinator builds — no permission mode, no tool
ceiling, no cwd restriction. `validatePlannedNodes` rejects it. See "Auto mode".

## Gate nodes and `resume` (v1.1)
A `gate` node is a **clean stopping point, not a blocking wait.** oh-my-graph is
a stateless CLI with no daemon (SECURITY.md), so parking a process for hours
holding a shared context, in-flight 20-minute claude children and an `errgroup`
would be a worse version of "exit and come back". A gate therefore stops the run
by design, persists everything a second leg needs, and exits.

**Lifecycle at a gate:**
1. The gate node's `GateController.Evaluate` returns a decision.
2. `pause` → stop *launching* new work, but **drain** what is already in flight
   rather than cancelling it, so sibling nodes' results are persisted and do not
   have to be re-run (and re-paid for). This is the one place the halt path does
   not cancel the context.
3. Write the run snapshot atomically — the pause itself is carried by the
   snapshot (`runstate.SnapshotRecorder.RecordPause`) and by the run feed's
   `paused` outcome, not by a ledger row (the ledger has no `PAUSED` verdict) —
   print the exact `resume` command, and return a `*schedule.PausedError`.
4. `cmd/oh-my-graph` maps that to **exit code 2**. `0` = every node passed,
   `1` = the run failed, `2` = the run is paused and resumable, `3` = `auto`
   refused to start for want of build evidence (ADR 0030 — nothing ran, nothing
   is resumable, nothing was billed, and no run directory exists). A pause is not
   a failure and must not be reported as one; nor is a refusal.

`--continue-on-fail` does not apply to gates: a pause always stops the whole
run, because approving "part of" a paused run later is not a coherent operation.

**Pause vs. a real failure draining alongside it, under default halt-on-fail:**
draining (step 2) means an in-flight sibling can still fail for real — not a
gate `pause`/`reject`, an ordinary node error — after another node has already
paused the run. Under the default (`--continue-on-fail` off), that real
failure takes precedence: it becomes a `*schedule.HaltError`, which cancels the
shared context and is what `grp.Wait()` returns, so `Run` returns it directly
and never reaches the pause bookkeeping below — the already-recorded pause is
discarded, no snapshot pause is written, and the CLI reports exit code 1, not
2. This is intended, not a race to fix: halt-on-fail's contract — a real node
failure stops the whole run — does not become conditional on whether some
other branch happened to pause first. `--continue-on-fail` avoids the
conflict entirely, since a real failure under it prunes a subtree instead of
halting, so it never competes with a pause for how `Run` returns.

**GateController — the decision source is data, not a DI swap:**
```go
type Decision string

const (
	DecisionApprove Decision = "approve"
	DecisionReject  Decision = "reject"
	DecisionPause   Decision = "pause"
)

type GateController interface {
	Evaluate(ctx context.Context, node graph.Node) (Decision, error)
}
```
- `PauseController` — injected by `run`/`auto`. Always `DecisionPause`: a fresh
  run cannot carry an approval.
- `RecordedController` — injected by `resume`, wrapping the snapshot's decision
  map. Returns the recorded decision for a gate already decided, `DecisionPause`
  for the next undecided one.

The scheduler asks the same question either way and never branches on "am I
resuming"; which controller answers is chosen once, at the CLI boundary, from
the invocation. Adding a future `--auto-approve` policy or an interactive TTY
controller is another implementation, not a scheduler change.

**What the snapshot must hold** — `~/.oh-my-graph/runs/<run-id>/state.json`,
written temp-file + `rename` (atomic), with a `schema` version so an
incompatible snapshot is refused rather than misread:

- **the normalized graph itself** (plus the source path and its SHA-256), so a
  resume does not silently depend on the YAML not having been edited. `auto`
  already writes `graph.json`; `run` must now snapshot the same normalized form.
- **the run's inputs and the flags that change meaning**: `--input` bindings,
  `continue_on_fail`, and — critically — the per-node tool policies for an auto
  run. Resuming a planned graph without its ceiling would silently drop the
  entire Layer-1/2 guard; the snapshot carries it for the same reason
  `executePlan` takes a whole `Plan` instead of two arguments.
- **per-node completion records**: verdict, **session id**, cost, duration,
  artifact path. The session id is the one thing resume needs that today exists
  only in `Handoff.sessions` in memory — without it a `handoff: session` child
  cannot `--resume` its parent on the second leg. The `.out` artifact files stay
  exactly as they are; they remain the `{{ artifacts.<id> }}` target.
- **gate decisions so far**, and which gate the run is paused at.

**One field the snapshot holds but `resume` does not trust**: an auto graph's
`success_check.verify`. A verification is a command the ENGINE runs, outside
every layer of the auto ceiling — which is why `validatePlannedNodeVerify`
refuses a planner-authored one at plan time — so a resumed leg reconstructing a
planned graph strips any it finds (`coordinator.ReattachVerifyCommand`, ADR
0016 §4). The discriminator is the snapshot's tool policies, non-empty exactly
for a planned graph: a hand-written graph's `verify:` is the user's own reviewed
artifact and round-trips untouched. What replaces the stripped command is
`resume --verify-cmd` — the same flag pair `auto` takes, the same value object,
the same 10-minute ceiling, attached to the same sinks by the same trusted code
— so the command comes from the human on the resumed leg exactly as it did on
the first, and the run directory is an admissible source on neither. A resume
that supplies nothing while the snapshot carried one is **refused**, naming the
nodes: resuming with strictly weaker checking than the leg being continued is
the failure ADR 0016 is about. The flag pair is registered on `resume` and on
`auto` only — a hand-written graph writes `verify:` on whichever node it means,
`run` has no such flag, and a resumed leg must not be able to attach a check a
fresh run could not, so `resume --verify-cmd` against a hand-written snapshot is
an error rather than an attachment.

**What the snapshot deliberately does NOT hold:** in-degree counts and the
ready set. Both are *derived* from `graph × completed`, so persisting them would
create a second source of truth that can go stale. `resume` recomputes them:
`Graph.ReadyGiven(done map[string]bool) []string` answers the topology question
(and `Roots()` becomes `ReadyGiven(nil)`), while the scheduler seeds each node's
in-degree as `len(DependsOn) − (parents already completed)`.

Snapshot writes happen after **every** node, not just at gates, so the file on
disk always reflects everything finished so far, including a Ctrl-C'd or
crashed run's progress. Two resume modes read that file: the gate mode only
continues a run whose snapshot actually recorded a gate pause
(`Gate.PausedAt != ""`) and refuses anything else with "run is not paused"
(see `executeResume`'s guard), while `--retry-failed` continues a run whose
snapshot recorded failures (see the CLI contract below). A Ctrl-C'd or
crashed run that recorded neither a pause nor a failure is still neither
mode's business — resuming that is future work. A snapshot
write failure mid-run is non-fatal, but its cost is not merely a gap in the
printed ledger: the dropped write means that node is absent from the persisted
state, so a later resume would not know it ran and would re-execute it — a
real cost, not a cosmetic one — which is why it is warned on the progress feed
rather than silently swallowed; a snapshot write failure **at a gate pause is
fatal**, because a pause whose state was not persisted is an unrecoverable
stop, and reporting it as a clean pause would lie.

**Two front-ends, one resume.** A gate decision reaches the run through
`executeResume` and nothing else. `oh-my-graph resume` parses it from flags;
the web live view POSTs it from the browser (`POST /api/gate/approve`
|`/reject` → `serve.GateResumer` → `cliGateResumer` → the same
`executeResume`, ADR 0014). The lock, the snapshot load, the explicit-gate-id
check, the `RecordedController` and the leg itself are shared, so the two
front-ends cannot drift; only the standalone `serve` process wires the
browser one.

**CLI contract:**
```
oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--verify-cmd 'CMD'] [--verify-timeout D] [--concurrency N] [--no-web] [--no-skill-activation]
```
- Exactly one of `--approve`/`--reject` is **required** when the run is paused at
  a gate. A bare `resume <run-id>` on a paused run is an error naming the pending
  gate — a resume must never silently approve.
- The gate id is explicit, not implied, so resuming an old run cannot approve a
  gate the user was not looking at. A mismatch (`--approve x` while paused at
  `y`) is an error.
- `--reject <gate-id>` prunes the gate's subtree; the run finishes the
  independent branches and exits `1`, naming the rejected gate. It is not a
  crash, but the graph did not complete as declared.
- `--input` on `resume` is **rejected**. Inputs come from the snapshot; changing
  one mid-run would make the already-persisted artifacts inconsistent with the
  prompts that produced them. `--concurrency` may be overridden — it is not
  semantic. `--no-web` is accepted with `run`/`auto`'s exact meaning: a
  resumed leg embeds the same live view under the same TTY gate (see "Web
  live view"), and this opts out.
- `--verify-cmd`/`--verify-timeout` are accepted for the opposite reason `--input`
  is refused: build evidence is the one thing a resumed leg must take from the
  human rather than from the run directory (ADR 0016 §4). An auto run started
  with `--verify-cmd` needs it supplied again on every resumed leg — without it
  the resume is refused, with it the command attaches to the same sinks under the
  same ceiling and the engine judges its exit code, exactly as on the first leg.
  A pause hint for such a run prints the flags back with the command in it, so
  the promised copy-pasteable resume stays copy-pasteable (ADR 0009) — and
  quoted, since a hint that pastes as a different command is the defect #198
  reported, one step downstream. Supplied where the flag has nothing to attach
  to — a hand-written graph — it is an error even when the leg would have been a
  no-op, rather than being accepted and ignored.
- Multiple gates ⇒ multiple resumes: a resumed run advances to the next gate and
  pauses again. The decision map makes batch approval a later, additive change.
- `--retry-failed` salvages a failed run instead of deciding a gate; combining
  it with `--approve`/`--reject` is a flag error (a retry leg replays prior
  gate decisions unchanged and must never sneak a new one in). It keeps every
  PASSED node's record and artifact as-is — dependents interpolate them
  exactly as after a gate resume — clears the FAILED records, and runs the
  graph again, so only the cleared nodes plus the nodes an earlier leg
  cancelled or never reached execute. A REJECTED gate is a standing human
  decision, not a failure to salvage: its record is retained, it is never
  retried, and its subtree stays pruned. A run with no retryable failure
  reports "no failed nodes to retry" and exits 0 without spawning anything or
  opening a new leg on the event stream — unless the run holds launchable
  nodes with no record at all (a session-limit pause leaves exactly that
  state — ADR 0009), in which case the leg runs them as "running unfinished
  nodes"; a gate-paused run is still redirected to `--approve`/`--reject`.
- **A subscription session limit is a pause, not a failure (ADR 0009) — on
  Claude.** The
  runner classifies the CLI's limit message (`NodeOutcome.SessionLimited`,
  matcher pinned in `internal/runner/sessionlimit.go` against Claude's own
  prose, and set only for the Claude runtime), so a `--runtime codex` run
  reaches no SESSION-LIMIT pause: the limit is an ordinary node failure — a
  retry first only if that node declares a `retry:` block, since `shouldRetry`
  returns false when `node.Retry` is nil — which `resume --retry-failed` still
  salvages. Only that ONE pause is runtime-shaped: a `gate` node is an engine
  construct with no runtime branch, so a Codex run pauses at a gate exactly as
  a Claude run does (`merge-shepherd`, the graph named above, has one).
  Whether ADR 0009 is a promise of the
  engine or of the Claude runtime is SETTLED as the runtime's (ADR 0009
  "Scope", closing #171). The scheduler then
  stops launching new work but drains in-flight siblings (which may
  themselves limit and join the paused set), records the limited node
  NOWHERE (un-run, not FAILED — no ledger row, snapshot record, or terminal
  event), and returns `*LimitPausedError` → exit code 2 with a
  best-effort-parsed "resume after <reset time> with: `resume <run-id>
  --retry-failed`" hint — carrying `--verify-cmd '<the command>'` when this
  run's sinks hold one (POSIX-quoted, so a command containing a quote pastes as
  itself, plus `--verify-timeout D` when the bound is not the default), since a
  resumed leg re-supplies it rather than reading it back off disk. A gate pause outranks a limit; a limit outranks
  continue-on-fail pruned failures. The leg closes on the stream as outcome
  `"paused"` with a distinguishing `detail`. A retry leg's worktree
  provisioning is disk-aware: a retried node re-declaring a name reuses the
  lane's surviving dir or re-attaches the branch a paused leg retained, so
  the lane continues its committed state instead of dying on the ref
  collision (see "Worktree isolation").
- A `resume.lock` guards against two concurrent legs of the same run id
  double-running nodes: the `run`/`auto` first leg holds it for its whole
  duration, and every `resume` takes the same lock — so a
  `resume --retry-failed` raced against a still-in-flight run fails on the
  lock instead of double-spawning. **The lock is the kernel's exclusive
  `flock(2)` on that file, not the file's existence** (ADR 0015): the kernel
  releases it when the holder dies, however it dies, so a held lock means a
  live leg and there is nothing stale to report. The file's first line is a
  format marker (`oh-my-graph-lock 1`) and its second is the holder's pid —
  informational, a label on an already-established liveness, never a liveness
  test (a pid was measured being recycled by an unrelated process). Release
  unlocks and closes but does **not** unlink: acquiring is open-then-flock,
  and an unlink between those steps would let one leg hold the lock on an
  orphaned inode while another took it uncontended on a fresh one. A lock file
  with no marker was written by a pre-`flock` binary, whose live leg holds no
  flock at all; on the **acquire** path it keeps the old semantics, where
  existence is the lock, a human decides, and the refusal names the exact path
  to delete. A reader can ask the same file whether a leg is still alive
  without creating, writing or removing anything (`runstate.ProbeLock`, a
  shared-lock probe on a read-only fd); a missing file, a non-local filesystem
  and any error alike answer *unknown*, which means the answer this tool gave
  before ADR 0015.
- **A pre-`flock` lock file is read under a second, weaker liveness rule**, and
  it is the reason an old run resolves differently from a new one. The flock is
  silent about a file whose writer never took one, so folding "unmarked" into
  *unknown* would leave every run abandoned before the upgrade reading
  `RUNNING` for the rest of time — there is no later moment at which such a
  file becomes readable, because the only thing that can change is a pid and
  the marker will never appear. Its single pid line is the only signal it
  carries, and it is read in **one direction only**: a pid that names no
  process at all (`kill(pid, 0)` → `ESRCH`) means the leg that wrote the file
  has exited — *free*; a pid that names something is a holder or a recycled
  stranger, indistinguishable — *unknown*; an unreadable pid — *unknown*. This
  does not reopen the pid recycling ADR 0015 refutes, which produced a false
  *alive*: pid-alive is never read as evidence here, so recycling can only move
  an answer to *unknown*, never onto a live run. And the acquire path is
  deliberately unchanged, so no answer derived this way can start a second leg
  by itself. The residual false-dead that cannot be mechanised away is a
  pre-`flock` leg inside a container read from outside it, where a
  namespace-local pid means nothing and the format records no namespace to
  check. The arm self-expires, but **not by itself**: the acquire path still
  refuses an unmarked lock under legacy semantics, so recovering such a run is
  two steps — the surfaces derive `ABANDONED` and print `resume … --retry-failed`,
  that resume is refused with the exact `rm` for the stale file, and only then
  does the retry take (and write) a marked lock, after which the flock alone
  decides forever. The human on that path is ADR 0015 §4's deliberate choice,
  not an oversight; what the hint cannot do is promise the second step away.
- **An abandoned run is derived from that probe, never repaired into the feed
  (ADR 0015 §2).** One rule, stated once in `internal/runstatus` and shared by
  every surface: *in flight = an open leg AND a held lock; abandoned = an open
  leg AND an affirmatively free lock; everything else is settled, and every
  doubt reads as in flight.* No reader writes anything — `events.jsonl` keeps
  only lines a scheduler emitted.

  **Since ADR 0023 that rule produces ONE SIX-VALUED STATUS rather than a
  liveness answer each surface then combines with a verdict of its own
  making:**

      PLANNING → RUNNING → { PASS | FAIL | PAUSED | ABANDONED }

  `PLANNING` is an open leg whose latest `run_started` carries
  `phase: "planning"` — an `auto` run inside its planner call, which now has a
  run id because the id identifies an EXECUTION, not the moment a graph starts
  executing (#163). `PAUSED` is read off the closed leg's `run_finished`
  outcome, never off the snapshot's gate block, which is what makes it cover
  ADR 0009's gate-less session-limit pause as well as a gate pause; both used to
  render `FAIL`. `PASS` is the one value that requires the snapshot, so a
  settled run without one cannot claim it. `Status.Settled()` and
  `Status.InFlight()` are both predicates, because `PLANNING` splits the
  in-flight side — `ResolveRun` keys on `InFlight()` and would silently stop
  preferring a planning run if written as an equality test.

  Two absences are NOT statuses and must not become them: a missing
  `state.json` is a fact about the run (normal at `PLANNING`, permanent for a
  refused plan) and never damage, while an unreadable snapshot or stream is a
  fact about the READER — the `WARNING`+skip row and the `unknown` card. Only
  the second may make a row disappear.

  A third case has no status at all rather than one of the six: a directory
  whose stream has said NOTHING — the instant between the run taking its lock
  (which creates the directory) and its first event, and permanently for a
  directory whose stream could never be created. `Derive` is total, so its
  default arm would call that `FAIL`; every surface that renders a word asks
  `runstatus.Spoken` first and falls back to the placeholder it already uses for
  what is not known yet (`pending` on the card, `-` in the table, an omitted
  word in `show`). A lone `run_finished` does not count as having spoken: a
  close with no open before it is damage, not a leg.

  `runs list` renders the six words under a `STATUS` header (renamed from
  `VERDICT`, which would have kept the very conflation the enumeration
  removes), `serve`'s `ResolveRun` stops preferring a corpse as "the run happening
  right now", `watch` refuses to tail a stream that will never get another
  line, the dashboard paints the card abandoned, and the single-run live view —
  the surface that carries the gate button — reads the same answer off
  `/api/graph` (additive `abandoned`/`hint` keys, absent on every other run):
  its header says `ABANDONED`, the nodes its dead leg left open stop spinning
  and stop tailing that leg's transcript, and the hint sits above the feed the
  button lives in. That page's own stream reducer applies the same leg boundary
  the two Go reducers do — every `run_started` ends the previous leg's running
  nodes — and it renders that event's `phase` rather than a word of its own, so
  the header chip reads `planning` for a planner leg exactly as the card linking
  to it does, and the feed marker names the phase exactly as `watch` does. The
  leg that opens a run
  must hold the lock **before** its first event and **after** its last, or a
  starting run would read abandoned for its first instants; that ordering is
  stated at `acquireRunLock` and pinned by
  `TestRunLeg_LockBracketsTheEventStream`.
- **The residual hazard is an orphaned `claude`, and the mitigation is
  wording.** Every child is spawned with `Setpgid`, so a death that took the
  engine alone (SIGHUP, `kill -9`, a panic, an OOM kill) leaves a subprocess
  still running and still spending while the run reads abandoned. There are two
  deliberate spenders on such a run — `resume` and the gate button — so the
  recovery hint reaches every surface either is reachable from: the `ABANDONED`
  row, `watch`'s refusal, `resume`'s own stderr, the card, and the run page the
  card links to, which is where the button actually is. Both must say what the
  click would allow *before* it is pressed. ADR 0015 rejects probing for the orphan;
  that would be a fifth exec seam.
- **Recovery is `resume --retry-failed`, and nothing new** — except for a run
  killed before its first node settled, which has no `state.json` and therefore
  nothing to resume from: its hint says "run the graph again", and `resume`
  itself fails on it with that sentence instead of a bare "no such file".

**Auto-planned graphs still may not contain gates.** `validatePlannedNodes`
already rejects `type: gate` and continues to: an unattended run whose planner
decides where a human should be interrupted is not a feature, and it collides
with the deny-by-default field policy below.

## Web live view — `oh-my-graph serve`
`serve [<run-id>] [--port N] [--no-open]` is two views on one port,
depending on its one optional argument:

- **`serve <run-id>`** is the live view of ONE run: a chronological run feed
  — what each node produced, why something failed — as the main surface,
  with the DAG as a compact collapsible side map (GitHub Actions' log-first
  layout, not Airflow's graph-first one: for this tool's runs the substance
  is in the node output, not the topology).
- **`serve` with no run id** is the **dashboard**: `/` renders one live
  mini-DAG card per run — in-flight first (state colour, elapsed, cost, node
  counts), settled runs in a collapsed list below — and each run's own live
  view is mounted at `/run/<id>/`, which is where a card click goes.
  Watching four concurrent runs is one process, one port and one tab
  instead of four of each.

The tool therefore renders its own runs: `serve` is the live view, `runs
list` / `show` / `watch` the terminal ones.

Both pages state, in the footer, **which build is serving them** —
`v0.5.2 (cef30c6, built 2026-08-09 14:02)`, the `Label` field of the one
`serve.Build` both pages are rendered with, put into the page once per process
alongside the gate token. A `serve` process outlives the tree it was built
from: it holds its port for as long as it runs and keeps serving the code it
was compiled from while `bin/oh-my-graph` is rebuilt underneath it, which from
a browser is indistinguishable from the new build misbehaving. The version cannot settle that on its own (every build
between two tags carries the same one), so the label also carries the VCS
revision the toolchain stamped *and* the running executable's own mtime — the
second because the first is absent from a `-buildvcs=false` build, a proxy
module build, and a build from a linked git worktree, which is how this
project's own graph lanes build.

The same three atoms are in each page's **head**, machine-readable, beside the
gate token: `<meta name="omg-version">`, `<meta name="omg-revision">` and
`<meta name="omg-built-at">` (the executable's mtime in RFC3339, from the same
single stat the footer's label reads). The label settles the question for a
reader and no one else — a stale server renders a stale label, so anything
comparing a server against the build it expects had to parse prose out of the
body. All three tags are always emitted, empty content meaning "unknown"; a tag
that is *absent* means a server older than this change, which is itself an
answer. They are disclosure only — no route, no state, nothing the page acts on
— and both surfaces are rendered from one `serve.Build` value, so a run view
mounted on the dashboard cannot name a different binary than the dashboard that
linked to it.

The structural rule is that rendering gets no privileged access to the
engine: `serve` is a **consumer of the run-feed contract**
(docs/RUN-FEED.md) in everything but its two gate routes, living in-repo
but on the same footing as an out-of-repo consumer — reading `state.json`
for structure and tailing `events.jsonl` for progress through the same
readers `runs list` and `watch` use (`runfeed.InFlight`, `runfeed.Follow` —
serve via its `FollowWait` wait-for-create variant, since a viewer may
connect before the stream exists). That is also why the contract is
documented and versioned rather than treated as an internal detail: the
in-repo views hold themselves to it, so any other reader of a run directory
gets the same guarantees. A stream schema newer than the
binary takes `watch`'s posture, not `runs list`'s: one non-terminal
warning frame, then keep forwarding (a list can skip one run; a live view
going blank would make a routine schema bump fatal, which RUN-FEED.md's
compatibility rule forbids).

**serve is deliberately no longer strictly read-only** (ADR 0014). Every
route reads except two: `POST /api/gate/approve` and `POST /api/gate/reject`
decide the gate a run is paused at, which continues the run — rewriting
`state.json`, appending to `events.jsonl`, and running the nodes the gate was
blocking. The boundary that moved is what `serve` *is*, not who may reach it:
the package still owns no gate logic (it calls the injected `GateResumer`,
which the CLI builds over `executeResume` — see "Gate nodes and resume"), it
still imports no `os/exec`, and a decision is valid ONLY while the viewed run
is genuinely paused at the named gate. A held `resume.lock` (a leg in
flight), a missing snapshot, `Gate.PausedAt == ""`, a gate id that is not the
pending one, and a view with no resumer injected are each **409**. Only the
standalone `serve` process injects a resumer: a paused run's process has
already exited (ADR 0003), so an embedded live view can never be looking at
one, and it answers 409 like any other view that cannot resume.

- **Run resolution:** `serve <run-id>` resolves that id (`serve.ResolveRun`;
  a mistyped id is a clear error, raised before the listener binds). With no
  id there is nothing to resolve — the dashboard is the view of *every* run,
  including the ones that do not exist yet, so an empty (or absent) runs
  root is an empty dashboard that fills in when something runs, not an
  error. **No production caller reaches `ResolveRun`'s in-flight-then-newest
  branches**: `runServe` calls it only inside `if flags.runID != ""`, and the
  embedded live view already owns the id it was mounted under, so it never
  asks. Those branches are kept, not dead-stripped — they are the package's
  answer to "which single run would a caller with no id mean", exercised by
  `TestResolveRun` and depended on by `cmd/oh-my-graph`'s `--plan-only` test
  (which asserts a preview left nothing under `runs/` for an id-less resolve
  to land on). `ResolveRun`'s own doc comment says the same; do not read them
  as the no-argument CLI path, which is the dashboard.
- **The dashboard subscribes to the runs ROOT** the way a run view
  subscribes to its `events.jsonl`: `/api/cards/events` sweeps the root at
  the same poll cadence and streams one `card` frame per run that is new or
  changed, one `card_removed` per deleted directory, and `cards_ready` after
  the first sweep; `/api/cards` is the same data in one read. "Changed" is
  the size-and-modtime of the two contract files, so an idle dashboard with
  forty settled runs costs two stats per run per tick and re-sends nothing.
  A card is derived through the existing readers — ONE `runfeed.Walk` (the
  one-shot counterpart to `Follow`) for per-node state and the leg
  boundaries, plus `runstate.Load` + `graph.Parse` for structure and cost.
  It does **not** call `runfeed.InFlight`: one walk already carries the leg
  state, and a card is rebuilt for every changed run on every tick, so a
  second read of the same stream was doubling the I/O on the hot path.
  `buildCard` therefore folds the leg state (`runfeed.Leg`'s two booleans — the
  last leg is open, a `run_started` was seen at all) off its own walk, keeping
  it distinct from the two boundary TIMESTAMPS the same walk collects for the
  elapsed clock, and then hands it to the SHARED derivation
  (`runstatus.Probe`), which composes it with the lock exactly as `runs
  list`, `ResolveRun`, `watch` and the single-run view's `/api/graph` do — the
  composition is stated once, not five times (ADR 0015 §2). The one duplicated half, the leg rule itself, is
  held by an enforced agreement rather than a structural one:
  `TestBuildCard_AgreesWithTheSharedRule` judges the inline leg derivation
  against `runfeed.InFlight`, and the card's state and `ResolveRun`'s
  preference against `runstatus.Of`, which is what keeps a card from
  disagreeing with `runs list`, `watch` or the run's own view about the same
  run. A card's state is a MAPPING from the shared enumeration and nothing
  else since ADR 0023 (`runState` used to compose its own answer from three
  facts, which is how a session-limit pause fell through to the default arm and
  painted red). Its vocabulary is the node vocabulary plus three: **`planning`**
  — a run inside its planner call, with no graph to draw yet, pulsing beside the
  running ones because that wait is the one #163 reported as invisible;
  **`paused`** — the RUN-level pause, wider than a node's `gate-paused` because
  it covers a session-limit pause too; and **`abandoned`** — a leg the stream
  left open whose lock is free, i.e. whose process is gone (muted, never red:
  nothing failed, the work simply has no verdict). A directory whose stream has
  said NOTHING keeps `pending`, on that affirmative fact rather than on "has not
  settled" — under six values `FAIL` is settled, so the old guard would have
  left every refused plan reading `pending` forever. Nodes that leg left running render abandoned
  rather than spinning forever and tally as pending, and the card carries the
  recovery hint, because the page it links to has a gate button that starts a
  leg with one click. Cost is the snapshot's
  per-node total, the same accounting `runs list` prints. A run directory
  this binary cannot read renders as an `unknown` card carrying the reason
  rather than being dropped: `runs list` can skip a broken run with a
  warning because a table can, but a dashboard that silently omitted one
  would be lying about what is on the machine. The token itself is a contract
  between four files with no compiler between them — `card.go` chooses the
  word, `dashboard.css` paints the tile's stripe, `style.css` owns the colour
  token and the chip's dot, `dashboard.js` decides whether a card carrying it
  is live and whether it is counted at all — so `internal/serve/assets_test.go`
  derives the whole token set from the enumeration and reads the three embedded
  assets back, rather than leaving the agreement to comments. That is also what
  holds the page's `LIVE_STATES` to `Status.InFlight()`: a page with no build
  step cannot import the predicate, and a hand-written equality test over the
  in-flight values is the exact shape ADR 0023 removed from the Go side.
- **`/run/<id>/` mounts the single-run view unchanged.** Endpoints are
  path-scoped (`/run/<id>/api/...`) rather than query-scoped because that is
  by far the smaller diff: the page already fetches with document-relative
  URLs (`api/graph`, `api/events`, `api/result`, `api/transcript`,
  `api/gate/*`) and links `style.css`/`app.js` the same way, so mounting the
  existing route set (`Server.routes`) under the prefix reaches every
  endpoint AND every asset with zero UI changes. A `?run=<id>` scheme would
  have to be threaded through every fetch and the `EventSource`, and would
  not scope the static assets at all. **The run id in the URL is matched
  against the runs root's DIRECTORY LISTING before any path is built from
  it** (`runInRoot`) — exactly as strict as `/api/result`'s node-id check,
  one level up: a typo and a traversal probe are the same 404, reached
  without a single path join. The gate token is the serving *process*'s, so
  the dashboard page and every run view mounted under it carry the same one.
- **127.0.0.1 only** (`serve.Listen`, default port 8642): run directories
  hold prompts, artifacts and session ids, so the server must never be
  reachable off-host. The loopback bind IS the access control for reading;
  widening it would need an auth story first. Covered by a test on the bound
  listener address, not just config. Because the bind is the access control,
  requests whose Host header is not `127.0.0.1`/`localhost` are rejected with
  403 (`requireLoopbackHost`) — otherwise a hostile page could DNS-rebind a
  domain it controls onto 127.0.0.1 and read `/api/*` through the victim's
  own browser. Neither guard covers a *mutating* route — a page the user is
  already visiting can POST to `http://127.0.0.1:8642/` with a perfectly
  legitimate Host — so the two gate POSTs additionally require a per-process
  random token (`crypto/rand`, hex), minted in `serve.New`, rendered into the
  served page (the one asset not shipped byte-for-byte) and sent back as
  `X-OMG-Token`: missing is 400, mismatched is 403, compared in constant
  time. Both are refusals — no shape of a gate POST reaches the resumer
  without the token. It is a CSRF guard, not a login — a custom header also
  forces a preflight, which a cross-origin form POST cannot satisfy. Layered
  in front of it, as hardening rather than as a fix: a POST whose `Origin`
  names anything but this server's own origin is 403 (`requireSameOrigin`),
  so a decision from a page this process did not serve is refused on its
  provenance before its token is weighed, and independently of the token
  staying secret. An absent `Origin` is allowed through — curl and the CLI's
  own tests send none, and the token remains the whole guard there — so the
  check only narrows what a browser can do.
- **Zero runtime network dependencies:** one static page embedded with
  `go:embed` — hand-written JS/CSS plus a pinned, vendored cytoscape.js
  (`internal/serve/ui/vendor/README.md` records its version and MIT license).
  No build step, no npm, no CDN.
- **Spawns nothing itself.** The `internal/serve` package imports no
  `os/exec` and starts no process; the two processes its features imply are
  reached through injected seams the CLI builds — a resumed leg's nodes
  through `runner.CLIRunner` behind `serve.GateResumer` (ADR 0014), so
  a gate decided in the browser spawns exactly what `oh-my-graph resume`
  would. The server itself never shells out to
  `open`/`xdg-open`; browser-open lives behind its own seam —
  `browser.Opener`, the fourth exec seam (ADR 0006) — and only the CLI wires
  it. A leg whose stdout is a terminal — a fresh `run`/`auto`, or a
  `resume` of either — embeds this server (same
  `serve.Listen`/handler/`serveRun` lifecycle, ephemeral loopback port) for
  exactly that leg's duration, prints the URL as `serve` does, and opens it
  through the injected `ExecOpener`; `--no-web` opts out, a non-terminal
  stdout (scripts, CI) gets no server, no browser, and byte-identical
  output. A resumed leg's view reads the same run directory the first leg's
  did, so it shows the whole run's history, not just this leg's. A chat
  graph turn stays un-wired (ADR 0006). The standalone `serve` subcommand
  takes the SAME gate: it prints the URL and, when stdout is a terminal and
  `--no-open` was not passed, hands it to the injected `ExecOpener` —
  `--no-open` is `serve`'s name for `--no-web`'s opt-out, and a non-terminal
  stdout reaches no Opener at all, leaving its output byte-identical to
  before.
- The graph structure appears when it is known, and that is **before the first
  node**, not after it: `run`/`auto` writes `state.json` up front
  (`runstate.SnapshotRecorder.WriteInitial`) and again after every terminal
  verdict. `/api/graph` reports the structure unavailable only while a run
  legitimately has no snapshot at all — an `auto` run still inside its planner
  call, or one whose planner reply was refused (docs/RUN-FEED.md). The UI polls;
  events stream from the start.
- **Node results:** `/api/result?node=<id>` serves that node's handoff
  artifact (`<run-dir>/<node-id>.out`) as text/plain for the feed's settled
  entries —
  the id is matched against the snapshot's own node set before any
  filesystem use (unknown id → 404; known node without an artifact → 204
  "no result yet").
- **Live transcript tail:** `/api/transcript?node=<id>` serves a RUNNING
  node's "now doing" — the last ~30 human-relevant entries (assistant text +
  tool-use names) of the session transcript named by the pre-assigned
  `session_id` the feed published on `node_started`/`node_retried`
  (docs/RUN-FEED.md); the UI polls it onto the node's open feed line every
  few seconds and drops it on settle. This is serve's ONE read outside the
  run directory — into the user's own `~/.claude/projects` — and only for
  the run's own sessions: the file read is named by the feed-published,
  shape-checked UUID (found by session-id filename, not by reproducing the
  CLI's undocumented cwd-to-dirname mangling), never by URL input. The node
  id gets `/api/result`'s membership guard, with one widening: while no
  snapshot exists, the run's own feed vouches instead. (Defensive rather than
  load-bearing: a run with a graph writes `state.json` before its first node
  starts, so the snapshot-less shapes — an `auto` run inside its planner call,
  or one whose plan was refused — are the ones running no node at all.) Not
  running / no session id (a gate, a
  session-handoff node) / no transcript yet → 204. That transcript is
  claude's file, so the tail is a Claude-runtime supplement: on a run whose
  snapshot names a runtime that keeps no such file, `/api/graph` carries a
  `transcript_note` and the page renders that one sentence in the tail's slot
  instead of polling an endpoint that could only 204 (#178). That field is the
  view's ONE runtime branch — computed server-side in
  `serve.transcriptTailNote`, so the page itself stays runtime-unaware — and
  the honest line is deliberate: an empty tail is indistinguishable from a node
  that has not printed yet, which is the harm that made the silence a bug.
- **Deciding the paused gate** is the view's one action: the gate the run is
  parked at carries approve/reject buttons on its feed entry and nowhere
  else. They are derived from the stream like the rest of the feed — a
  `gate_paused` with no later `gate_approved`/`gate_rejected` for that node
  is the actionable state — so a reconnect's full replay puts them back on
  exactly the gate still waiting. A POST is answered **202** the moment the
  leg starts (a leg runs for minutes; the run feed is the progress report,
  streaming into the SSE connection the page already holds open — which is
  why `/api/events` deliberately does not end at `run_finished`), and the
  leg is detached from the request so closing the tab does not kill it. The
  `oh-my-graph resume` command stays on the entry as secondary text: it is
  still the way in from an embedded view.
- **Scope:** the dashboard is a card wall over the run directories and the
  single-run view behind it — no history browsing beyond what is on disk, no
  login (the gate token is a CSRF guard, not auth), no config file, no
  WebSocket (SSE over the append-only stream and the runs root is the whole
  transport). A card's mini-DAG is hand-drawn SVG (layered by depth, one dot
  per node, one line per `depends_on`) rather than a cytoscape instance per
  card: a dashboard can hold dozens of cards, and this is orientation at a
  glance — the real map is one click away.

## Auto mode — planned graphs, no hand-written YAML
`oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--accept-no-build-evidence] [--accept-loaded-user-config] [--input k=v ...]` is the
zero-config path; custom
YAML stays the precise-control path.

**Zero-config is not zero-evidence in a repository that builds.** Because a
planned node can carry neither `success_check.verify` (the planner's reply is
untrusted input) nor a build tool (`plannedToolAllowlist` is exact-matched), an
`auto` run without `--verify-cmd` has *by construction* no engine-run evidence:
the only terminal predicate left is `result_matches` on a node's own reply, and
that node wrote the reply. So since ADR 0030 the question is asked at launch,
once, before the planner call: if `DetectBuildSignals(".")` finds a marker and no
`--verify-cmd` was supplied, `auto` **refuses** — exit 3, on stdout, naming the
marker and a suggested command — unless `--accept-no-build-evidence` states that
this run carries no build evidence. A directory with **no** build signal is not
gated: there is nothing for an evidence command to be evidence about, and
requiring a flag there would be friction with no defect behind it. The answer —
`attached`, `declared`, `disclosed` or `none-detected`, with the markers detected
— is written to the run's `state.json` (`build_evidence`) and stated on the plan
screen, so a reader of a finished run can tell a chosen absence from an
accidental one. `run` (hand-written graphs) and `resume` are not gated; `chat`
asks and never refuses, because it registers no verification flag a refusal could
name (ADR 0030 §2.6). This amends ADR 0016 §3 in one direction only: a repository
file may now cause the tool to *stop*; it may still never cause it to run, widen
or attach anything. Planning a graph is ONE
planner call through the same NodeRunner seam every node uses (CLIRunner:
env scrub, read-only `plan` permission mode, never the Agent SDK) — the
Coordinator makes exactly that one call per PLAN — per cycle, not per `auto`
run — plus at most one more for that same plan:
a plan refused by validation buys ONE corrected call (`maxPlanRepairAttempts`,
`internal/coordinator/repair.go`), so the planner-call ceiling is 2 per plan
and `2 × N` for `--max-cycles N`. `--plan-only` stops
the sequence immediately after the topology print, so that one call is all it
makes and no node runs — the inspection path for the mappings and the ceiling,
and deliberately NOT free the way `run --dry-run` is: there is no plan to
inspect until one has been bought, which is why the stop line prints its cost
and the paid-for spec is kept — in `$OMG_HOME/plans/<id>/graph.json`, never
under `runs/`. The reason for that is no longer the mechanism one (a directory
with no `state.json` reading as damage — ADR 0023 §2.5 dissolved exactly that,
and such directories are now ordinary): **a preview has none of the six statuses
and cannot be given one.** It is not `PLANNING` (it finished), not `RUNNING`,
not `ABANDONED` (its process left on purpose), and it has no verdict about
work, because there was no work. So `--plan-only` mints no run id at ANY point,
its planner call included. It is rejected with `--max-cycles ≥ 2`, since
every cycle after the first is planned from the previous cycle's run. (Interactive `chat`
reuses the same Coordinator but adds a routing call per turn before planning;
see "Ambient chat".) The planner asks
claude to reply with a graph spec as a JSON object (name / nodes / depends_on /
prompt / allowed_tools / handoff). JSON is a YAML subset, so the reply is
loaded through the existing parser, normalization, and DAG validation — a
VALIDATION-REFUSED plan buys the one corrected call above, and if that reply is
refused too the whole step fails before anything runs, with what it paid for
kept as `rejected.json` (its own name, so nothing walking the tree for
`graph.json` mistakes it for a graph the engine would run). WHERE it is kept
follows one rule (ADR 0023 §3.1): **into the run directory when a run leg
already exists, and into `$OMG_HOME/plans/<id>/` when none does.** `auto` and
every goal cycle have committed to execute before their planner call, so their
run id exists and the refusal is recorded beside it, where it reads `FAIL` — the
engine judged the material it was given and diagnosed it. `--plan-only` has the
diagnosis without the commitment, so its rejection goes to `plans/`, which
thereby keeps one honest meaning: specs that never belonged to a run. The
corrected reply is UNTRUSTED exactly like the first: same `graph.Parse`, same
`validatePlannedNodes`, same mapping order — there is no shortcut for "it
already failed once". Only a refusal the reply's own CONTENT caused is
retryable: a runner error, a non-zero planner exit, a reply with no JSON
object, a reply whose JSON does not decode, and any other non-content failure
stop the step with no second planner call, because there is nothing precise to
hand back and a blind retry on a paid runtime is not a repair.
Auto-specific guards, enforced in
`coordinator.validatePlannedNodes` (not just requested in the planner prompt):
a planned node may NOT request `permission_mode: bypassPermissions`
(hand-written YAML may opt in per node because the user reviewed it; an
unreviewed plan may not); a planned node may NOT set `cwd` (it always runs in
the invocation's working directory, so an unreviewed plan cannot reach into an
unrelated checkout or a path under `$HOME`; this bounds *where* it can act and
does not by itself make a write-capable node safe); and every planned node's
`allowed_tools` must be a non-empty list drawn only from
`coordinator.plannedToolAllowlist` (Read, Glob, Grep, Edit, Write, and a small
set of scoped `Bash(<prefix> *)` patterns) — anything else (bare `Bash`,
`Bash(*)`, an un-scoped `Bash(rm -rf *)`, unrestricted `WebFetch`/`WebSearch`,
an empty list) fails `Plan` with a `*PlanError` naming the node and the
offending tool.

After validation — and only after — the coordinator may map planned nodes onto
the user's own Claude Code agents (`internal/coordinator/agentmap.go`): a scan
of `~/.claude/agents` only — never a project directory, the same cut skill
staging has always had and for the sharper reason measurement (l) recorded — a
conservative name-token match between node id and agent name (exactly one
candidate or nothing), and a refusal to map any agent whose frontmatter tools
exceed the node's own `allowed_tools`. A mapped node runs `--agent <name>` and
**keeps every ceiling layer, Layer 1 included** (ADR 0022, 2026-08-12): the
matched definition is copied into `<run-dir>/agents-plugin/agents/<name>.md`,
pinned by its plan-time SHA-256, and supplied with `--plugin-dir`, which reaches
the node without reopening `--setting-sources`. Until then a mapped node dropped
Layer 1 and was measured to lose its scope ceiling with it. Every decision is
shown in the printed plan, and `--no-agent-mapping` turns it off run-wide while
`--no-agent <name>` declines a single agent. So does
`--accept-loaded-user-config`, which sets `agentMappingOff` itself: the staging
guarantee holds only while Layer 1 is `""`, so a run that carries the operator's
settings maps no node at all and the CLI discovers their agents natively (ADR
0032 §2.4, below). The full rule and what it now costs
the node live in "Node-as-subagent"; the raw plan itself still may not carry
`agent:`.

Strictly after agent mapping, the coordinator stages the user's own Claude
Code skills for planned nodes (`internal/coordinator/skillstage.go`, ADR 0017).
The mechanism is the CLI's own description-driven activation, not a plan-time
choice: trusted code scans `~/.claude/skills/*/SKILL.md` only (never a project
directory and never a plugin's — both surfaces stay cut, and the plan printout
names them as out of scope on every run), copies **the whole corpus** into
`<run-dir>/skills-plugin/` as a Claude Code plugin, and gives each planned node
`--plugin-dir <staged>` plus `Skill` in its `--tools` list. Two layers and only
two: `--plugin-dir` is not a ceiling layer at all (it supplies definitions and
grants nothing), and `Skill` enters at **Layer 3**, through the one function
that builds that list. Staging happens for a run that stayed isolated;
`--accept-loaded-user-config` sets `skillActivationOff` for the same reason it
sets `agentMappingOff` (below), so an opted-in run stages no corpus.
**Layer 1 stays `--setting-sources ""`** — ADR 0017's
measurement (g) showed that relaxing it lets a node that declared `Bash(git *)`
run an out-of-scope command, because `--tools` bounds tool NAMES and not
SCOPES. Layer 0 does not move either: `plannedToolAllowlist` never learns the
word, so a plan may not DECLARE `Skill`, and the grant is invisible in
`graph.json` — its durable record is `state.json`'s `tool_policies`.

The staged directory is not protected by its location. `Write` is unscoped in
`plannedToolAllowlist` and a node runs as the same uid, so no path this process
creates is unwritable by it. The requirement — *a node must not be able to
stage a skill for a later node* — is met **within a leg** by lifetime: a
manifest of every staged file with its source SHA-256 is taken at plan time and
held in memory, and the directory is **re-materialized from that manifest
immediately before every node spawn** (a `NodeRunner` decorator,
`coordinator.GuardStaging`), deleting whatever the manifest does not name and
restoring whatever no longer hashes to it. The nodes read the staged copy, so a
source skill edited or deleted mid-run changes nothing and stops nothing; only
a staged file that must be restored while its source no longer holds the
planned bytes fails a spawn, and the message attributes that to the engine
rather than to the node. **Across legs there is no claim, so there is no
activation either**: the only manifest a resumed leg could re-stage from is the
sidecar in a run directory a node can write, and its per-file SHA-256 does not
close that, because one actor authoring both `source` and `sha256` satisfies its
own check. Closing it needs an integrity anchor outside the run directory, which
this build does not have, so since 2026-08-07 `resume` drops `Skill` and
`--plugin-dir` from every rehydrated policy and prints why (ADR 0017 §6). It
never rehydrates the directory path verbatim — a `--plugin-dir` pointing at
nothing is accepted by the CLI with exit 0, so absence is indistinguishable from
a model that chose no skill, which is exactly why the de-escalation is
disclosed on the leg that makes it.

What is lost, permanently: the plan can no longer say WHICH skill a node will
use, because the model chooses at run time by description. What replaces it is
a per-run corpus disclosure — every staged skill with size and SHA-256, the
nodes reached, the agent-mapped nodes excluded, and the per-invocation prompt
cost, which every node pays on every retry and every feedback re-run.
`--no-skill-activation` turns it off, on `auto` and on `resume` alike
(de-escalation only, so no resumed leg can widen a run's ceiling);
`--no-skill-mapping` is accepted as a deprecated alias with a printed notice.
ADR 0012's plan-time inlining — the name-token matcher, the 16 KiB cap, the
`{{` neutralization and the nonce fence around skill bodies — is deleted in the
same change: the two must never coexist, or a node would receive the same skill
twice and become unattributable.

**What is delivered and what is used are not the same thing, and only the first
is established.** ADR 0017's acceptance test (2026-08-07,
`docs/measurements/0017-skill-activation-acceptance{,-run-2}.md`) confirmed on
real spawns, by an argv-recording shim, that every activated node is launched
with `--setting-sources ""`, `--plugin-dir <staged>` and `Skill` in `--tools`,
against a clean `--no-skill-activation` control. It also recorded **1 `Skill`
invocation across 7 activated planned nodes**, and zero across the three nodes
of the pre-registered run, under prompts the planner itself wrote — while a
prompt that names a skill fires reliably.

**One sentence is why that number is no longer the number** (2026-08-08,
44 spawns, `docs/measurements/0017-skill-activation-yield.md`). Trusted code
appends a fixed, skill-agnostic sentence — *"A corpus of procedures is
available through the Skill tool; consult it if one fits this task."* — to the
prompt of every node it activates, and only those: an agent-mapped node, which
is excluded from activation, is never told a corpus it does not have exists.
It names no skill and no directory, so it announces THAT a corpus exists and
never WHICH one to use; the choosing stays in the node's own model, at run
time, through the CLI's description gate. The same planner prompt scores 0 of 9
activations without it and 8 of 9 with it, all 8 reaching for the same real
skill of the user's own corpus. It is **not** written into the saved
`graph.json` — that artifact is re-runnable through `run`, which has no staged
plugin and no `Skill` tool — which is why activation is the LAST post-validation
mutation, after every step that writes the spec.

The yield is real; the benefit is still not measured. On the one task where the
deliverable could be checked mechanically the two arms were indistinguishable,
and a node whose prompt is an output contract (a verification node's
`PASS`/`FAIL`) does not activate with the sentence or without it. ADR 0017
stays `Proposed` for that reason and this section describes a mechanism whose
yield is measured and whose value is not. One further
consequence to know when reading the two mapping steps above: **they are
mutually exclusive.** Agent mapping runs first and an agent-mapped node is
excluded from activation, so the nodes whose job matches a named role most
cleanly — the design and doc nodes — are the ones a skill cannot reach.
**That exclusion is total, and it is measured** (2026-08-09, 10 spawns,
`docs/measurements/0017-agent-mapped-nodes-cannot-invoke-a-skill.md`): an
excluded node's `--tools` never carries `Skill`, so it invokes no skill by any
route — the definitions its `nil` setting sources load (user, project and local
alike; measured at both project and user scope) are visible to the CLI and
unreachable by it, and `permission_denials` is empty because the tool is absent
rather than denied. The exclusion is kept anyway — the `--agent` +
`--plugin-dir` + settings composite is unmeasured, and a staged plugin would
meet the user's own plugins there for the first time — but the plan printout
now states the cost and names `--no-agent-mapping` (which turns agent mapping
off for the whole plan), and lifting it is gated
on ADR 0017's measurement (j) rather than on the argument that it costs little.
**(j) was run on 2026-08-12 — 21 spawns, $4.16, claude 2.1.228 — and the
exclusion stays** (`docs/measurements/0017-lifting-the-agent-mapped-exclusion.md`).
Not because the composite fails: `--agent` + `--plugin-dir` + `Skill` invoked
the staged skill 3 of 3, and it costs the ceiling nothing because an
agent-mapped node's ceiling is already breached without it. It stays because
`Skill` on these nodes resolves against a corpus the **repository under work**
can write: with the staged copy and a repository-committed `.claude/skills` copy
loaded under one name, the bare name resolved to the repository's 3 of 3, and a
repository-committed `SKILL.md` was invoked 3 of 3 by a prompt that never
mentions skills. Both candidate fixes carry that, since both keep Layer 1 at
`nil` — so the thing to change is `applyAgentMapping`'s `SettingSources`, not
the exclusion.

**That change was measured and shipped on 2026-08-12 (ADR 0022), and it moves
the ground under this paragraph without moving the exclusion.** A mapped node's
Layer 1 is now `""` and its agent arrives from a staged `--plugin-dir`, so the
repository-supplied definitions the paragraph above rests on no longer load: the
repository's `.claude/skills` copy fired 0 of 3, and where the model called
`Skill` the CLI answered `Unknown skill: …`, `is_error: true`
(`docs/measurements/0017-staged-agent-restores-layer-1.md`). The exclusion is
still in force — `applySkillActivation` still skips an agent-mapped node — but
it is now **a decision nobody has re-taken** rather than a refusal with a number
behind it, and re-deciding it needs its own record. The other half of the
paragraph is unchanged and still true: an excluded node's `--tools` carries no
`Skill`, so it invokes none by any route.

**What shipped instead of the lift is disclosure and a cheaper way out.** The
exclusion's cost — no `Skill` tool — is printed **per node**, by name, on the
plan screen; and `--no-agent <name>` (`WithoutAgentsNamed`) declines a single
agent while every other mapping stands. (Until ADR 0022 that per-node line
carried a second cost, *a declared scope enforced only as far as the user's own
settings enforce it*, and `noteCeiling` carried a matching ceiling exception.
Both are gone with the fact they described — a warning kept past its cause
teaches a reader to discount the next one — and `wiring_test.go` asserts their
absence.) The agent is the unit because
it is the only identifier that exists before the planner is paid: node ids are
bought, agent names are the user's own files, and the plan names the agent on
the node line it took. The decline is applied **after** `candidateFor` picks a
single candidate, never by removing the definition before matching, so it can
only ever remove a mapping — dropping it earlier would let declining one of two
ambiguous agents promote the other and *create* a mapping. A declined node keeps
Layer 1 and is therefore activated like any other planned node: exactly the
configuration (j)'s `ACT`/`G-ACT` arms measured, which held the ceiling and
invoked the staged skill under an attributable name — and, in the post-hoc
`X-ACT` arm, kept that name **3 of 3 under the same three-way collision that
beat it on a mapped node**, so "attributable" here is measured against
competition rather than in its absence.

The last thing computed with a plan is a warning rather than a decision. If the
goal or a planned prompt names an absolute path that resolves into a git
checkout **outside the invocation repository**, `Plan.Unisolated`
(`internal/coordinator/unisolated.go`) carries it and the plan printout says —
before anything spends, and in the same output `--plan-only` renders — that
oh-my-graph isolates nothing there: no worktree, no lock, so a node working in
that checkout must create its own worktree or race whatever else is standing in
it (#103). The boundary is one of **ownership, not of protection**: `auto`
rejects `cwd:` and `worktree:` at plan time, so it provisions no managed
worktree anywhere — the invocation repository included, where planned nodes edit
and commit directly — and what the reported checkouts have that it does not is
that the user did not open them for this run. The printed text may not read as
if the invocation repository were the isolated one. The rule is deliberately
narrow: a mention must resolve into a `.git` checkout (an ordinary clone or a
linked worktree, both of which have their own HEAD), which is what keeps `/tmp`
scratch paths, templated paths, files that do not exist and every path *inside*
the invocation repository silent; a checkout that is a **tool installation**
rather than a work tree is dropped as well (rooted under `/usr`, `/opt`,
`/Library`, `/System`, `/nix`, or under a dot-directory of `$HOME` — Homebrew,
nvm, oh-my-zsh, a plugin marketplace and a chezmoi-managed `~/.config` are all
real clones, and a warning about a HEAD nobody will switch is the line that
teaches the reader to scroll past the block); and one warning is emitted per
checkout rather than per path, since the hazard is a shared HEAD. It is computed on the planner's own
prompts, so absolute paths a local `SKILL.md` happens to document — which now
reach a node through the staged plugin rather than through its prompt — are
never attributed to the plan. It is a WARNING and never
a refusal — a multi-repository goal is legitimate, and the engine simply cannot
isolate it — and the printed text states its own blind spots (a path built at
run time, one arriving through `--input` or a parent's artifact, a repository
reached by a relative path, a tool installation's own clone, and what a node
actually does once it is there), because a heuristic that reads as complete is
worse than no heuristic at all.

### The tool ceiling — one layered policy, not a list of flags (v1.1)
The allowlist above bounds what a plan may **declare**. Execution is bounded by
a separate, layered policy carried on `Plan.ToolPolicies` (one
`runner.ToolPolicy` per node id), handed to `schedule.Options.ToolPolicies` and
rendered by the runner. One value object rather than parallel maps, so a caller
cannot pass three quarters of a ceiling:

```go
// internal/runner
type ToolPolicy struct {
	AllowedTools    []string // --allowedTools
	DisallowedTools []string // --disallowedTools
	Tools           []string // --tools  (nil = flag omitted)
	SettingSources  *string  // --setting-sources ("" = load none; nil = flag omitted)
	StrictMCPConfig bool     // --strict-mcp-config
	PluginDirs      []string // one --plugin-dir <dir> per entry, in order
}
```

| layer | mechanism | closes |
|---|---|---|
| 0 declaration | `plannedToolAllowlist`, plan-time rejection | a plan asking for `Bash(*)` |
| 1 **isolation** | `--setting-sources ""` (nil, flag omitted, under `--accept-loaded-user-config`) | the user's standing grants; settings hooks |
| 2 grant | `--allowedTools "Read,Bash(git *)"` + `dontAsk` default-deny | **scoped Bash** |
| 3 narrowing | `--tools "<bare names declared>"` | tools the model can even attempt |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` (false, flag omitted, under the same opt-in) | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` (PR #5's list, retained) | anything the above missed |

The table is the default — the run that types nothing. Layers 1 and 4 are the
two an operator may decline together, at launch, and nothing else in it moves;
"The operator's opt-in" below is the whole of that difference.

`PluginDirs` is the sixth field and **not** a sixth layer: it ADDS definitions —
a staged skill corpus (ADR 0017) or the one agent a node was mapped onto
(ADR 0022) — and grants no capability, since whatever it supplies still runs
inside the five layers above (a `Skill` tool exists only when layer 3's `Tools`
names it, and a staged agent's frontmatter does not widen past `--tools`, E6).
It exists because layer 1 stays `""`: `--setting-sources ""` withholds the
DEFINITIONS along with the settings, and a plugin directory is the one source of
definitions that does not reopen it. Empty omits the flag, which is every
hand-written graph, every opted-in planned run (which neither activates nor
maps, since layer 1 is what makes staging worth anything) and every planned run
that neither activated nor mapped; a
resumed leg is emptied deliberately, since rehydrating the path would trust a
directory the previous leg's nodes could write (ADR 0017 §6, below).

Layer 1 is the load-bearing change. Rules from `~/.claude/settings.json` are why
`--allowedTools` could never bind: they are matched alongside ours and a standing
`Bash(*)` wins. `--setting-sources ""` loads none of user/project/local settings,
leaving our argv as the only allow-rule source; enterprise policy settings are
still loaded and still cannot be escaped — nor can they be escaped by omitting
the flag, which is why the opt-in below can widen everything it widens and still
not reach a managed policy. Combined with `dontAsk` — under which
an unmatched call resolves to *ask* and an unanswerable ask becomes a **deny** —
`Bash(git *)` means *git and nothing else*. **Measured, not inferred** (E1): the
identical node declaration ran an out-of-scope `touch` without Layer 1 and had
it denied with Layer 1, while in-scope `git` kept working. The gap "a node
declaring a scoped `Bash(...)` keeps the whole `Bash` tool" is closed for
planned nodes — **including the agent-mapped ones, since 2026-08-12.** Those
used to drop Layer 1 to `nil` so `--agent` could resolve (E2), and for them it
was therefore not closed at all: on 2026-08-12, claude 2.1.228, the argv
`runner.buildArgs` then emitted for an agent-mapped planned node declaring
`Bash(git *)` ran the out-of-scope `touch` under `dontAsk` with
`permission_denials: []`, twice, while the same probe's non-mapped node denied
the identical command. ADR 0022 closed it by changing where the definition
comes from rather than which flags bind: the matched agent is staged into
`<run-dir>/agents-plugin/` and supplied with `--plugin-dir`, which reaches the
node with Layer 1 still `""`. Same machine, same build, minutes apart, the
identical arm was **denied 3 of 3** with the refusal named in
`permission_denials`, and the in-scope `git init` control ran 2 of 2
(`docs/measurements/0017-staged-agent-restores-layer-1.md`). That measurement
re-confirms E2 and widens it: under `--setting-sources ""` the CLI's own list of
agents it can see is five built-ins and neither the user's nor the repository's
directories, which is why the definition has to be staged at all. **Staging is
then a channel of its own**, and the scan feeding it is `~/.claude/agents` only:
while it also read `<cwd>/.claude/agents`, a definition committed to the
repository under work was the one staged, and its system prompt ran the node 2
of 2 (`docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md`).

Layer 1 also closes the settings-hook gap: a node that writes
`.claude/settings.local.json` into the invocation directory achieves nothing,
because no node in this run (or any later `auto` run) loads local settings —
unless that run typed `--accept-loaded-user-config`, where local settings are
loaded on purpose and this gap is open again by the operator's own statement.

Layer 3 is a genuine *replacement* of the built-in tool set, not an addition to
it (E4): a tool omitted from `--tools` does not exist for the node, and naming
it in `--allowedTools` does not bring it back. So the two compose as an
intersection — `--tools` decides what exists, `--allowedTools` what is
permitted — and Layer 3 must list every tool the node needs.

Layers 3 and 5 are deliberate redundancy, not belt-and-braces theatre: they are
independent mechanisms, so a wrong assumption about any one layer degrades to
the previous behaviour rather than to nothing.

Remaining honest gaps, unchanged by this work: skill/slash-command surfaces are
still not enumerable — though ADR 0012 and then ADR 0017 measured the
planned-node case layer by layer: Layer 1 withholds the skill DEFINITIONS and
Layer 3 withholds the `Skill` TOOL, which is why activation needs both a staged
`--plugin-dir` and `Skill` in `--tools`. Layer 1's half of that is now measured
under competition rather than assumed: with a same-named skill in the working
repository's `.claude/skills` **and** in a plugin that repository's own
`settings.json` enables, an activated node still resolved the bare name to the
STAGED copy 3 of 3 (measurement (j), arm `X-ACT`), while a mapped node — nil
Layer 1 — lost it to the repository 3 of 3. **Layer 4 is unverified** (E5 —
`--strict-mcp-config` ships because it is free, not because MCP closure was
observed); and dropping user settings also drops the user's CLAUDE.md, hooks and
MCP servers for planned nodes — a behaviour change that makes planned nodes
*more isolated and less capable* than they were, which is the intended direction
but must be stated in the README rather than discovered, and which the operator
may decline for one run with the opt-in below. **Through v0.6.0
agent-mapped nodes inverted that last sentence for the SETTINGS half**: nothing
was dropped for them, so the user's CLAUDE.md and hooks loaded — and so did the
working repository's project scope, which is where the repository-supplied skill
above came from. **Since 2026-08-12 (ADR 0022) they do not**: the mapping is
carried by a staged definition and a `--plugin-dir` instead of by nil settings,
so a mapped node drops exactly what every other planned node drops and is
measured under the same E1. What was never part of that difference is layer 4:
it is a flag rather than a settings scope, so `--strict-mcp-config` was on a
mapped node's argv exactly as on any other planned node's, with no `--mcp-config`
beside it, before the change and after it (measurement (j),
`argv/omg-probe-writer.argv.txt`). Whether that flag actually closes MCP is E5,
and E5 is unmeasured — the sentence above about MCP servers is a statement about
the argv, not an observed closure.

#### The operator's opt-in — `--accept-loaded-user-config` (ADR 0032)
The ceiling above is what a planned node gets by default. `auto` takes one flag
by which the **operator** — never the plan — states that this run's planned
nodes carry their own CLI configuration. It takes no value, defaults to OFF, and
a run that does not type it is byte-for-byte the run that shipped in v0.10.0:
same argv, same screens, same `state.json`.

It is **runtime-neutral**, because the mechanism is one bit both protocols
already branch on (`spec.Policy.SettingSources != nil` in
`codex_protocol.go`, `policy.SettingSources != nil` in `claude_protocol.go`).
`toolPolicyFor` builds:

| layer | field | default | with the opt-in |
|---|---|---|---|
| 1 isolation | `SettingSources` | `&""` | **nil** (flag omitted) |
| 2 grant | `AllowedTools` | node's declaration | unchanged |
| 3 narrowing | `Tools` | `narrowedToolsFor(node, …)` | unchanged |
| 4 MCP | `StrictMCPConfig` | `true` | **false** (flag omitted) |
| 5 residual | `DisallowedTools` | `disallowedToolsFor(node)` | unchanged |

So **an opted-in planned node's setting/config posture is exactly a hand-written
`run` node's, and its tool posture is exactly a planned node's.** Layer 4 moves
with layer 1 deliberately: E5 is unmeasured, so leaving `--strict-mcp-config` on
would make the disclosure's *"your MCP servers load"* a sentence nobody had
checked. That layers 3 and 5 still bind under restored settings is ADR 0032 §8's
required measurement, which does not exist — the ADR is **Proposed** until it
does, and this paragraph's last claim is a projection.

`WithLoadedUserConfig()` sets `agentMappingOff` and `skillActivationOff` **in the
Option**, not at the call site, so no later composition of options reopens them:
both ADR 0017's and ADR 0022's guarantees are held by layer 1 being `""`, and
measurement (j) arm `X` resolved a three-way name collision to the repository's
own copy 3 of 3 under `nil`. The CLI discovers the operator's agents and skills
natively instead; what is given up is the staged copy's attributable name and
its shadow-proofness.

The choice is disclosed on the plan screen through a `note*` sibling of
`noteCeiling` and `noteMissingBuildEvidence` — **one slot that prints the
isolated sentence or the loaded one, never both and never neither**, with a
different literal per runtime because the bill differs (on Claude the operator's
standing grants come back with their settings, so a declared `Bash(git *)` is a
declaration again; on Codex `--sandbox` and `approval_policy="never"` are argv
outside the branch and the flag widens neither). There is **no new snapshot
field**: inside a non-empty `ToolPolicies` map an absent `setting_sources` is
unambiguous, so the disclosure predicate reads the policies about to be spawned
and cannot drift from the argv. `resume` therefore inherits the choice and
reprints the line before the banner while registering **no** flag of its own — a
resumed leg's flags may only de-escalate (ADR 0017 §6, one direction over).

Out of scope in every direction: `run` is untouched (`Options.ToolPolicies` is
nil there and `TestScheduler_HandWrittenGraphGetsNoCeiling` passes unmodified);
`chat` parses no such flag, so every chat-planned node stays isolated; the
planner already ran unisolated and the assessor keeps layer 1 unconditionally,
because its input is untrusted model output; the graph schema gains nothing, so
no planner can request this per node; enterprise and managed settings are
unioned on top and cannot be dropped by an argument's absence; and
`internal/childenv` is untouched — one list, no runtime branch, still scrubbing
all four API-key variables from every child. A restored configuration is not a
restored API key.

### Planned-node fields are deny-by-default
`agent:` on a planned node would let an unreviewed plan choose which of the
user's subagents — and therefore which system prompt, tool grant and model —
runs the node, routing around Layers 0–3 entirely. A **planner-authored**
`success_check.verify:` would let it run arbitrary shell outside every guard.
Both are **rejected** in `validatePlannedNodes`, alongside `bypassPermissions`,
`cwd` and `type: gate`. What is rejected there is the planner's authorship, not
the field: trusted code may attach a `verify:` strictly *after* validation, from
the user-supplied `--verify-cmd` string, and does
(`coordinator.attachVerifyCommand`, ADR 0016 §2).

The general rule, because this class of hole recurs every time the schema grows:
**every field on `graph.Node` must have an explicit disposition in
`validatePlannedNodes` — allowed, constrained, or rejected.** Adding a field to
`Node` without adding a case is a review-blocking defect, not a nit. A
table-driven test over `reflect.VisibleFields(reflect.TypeOf(graph.Node{}))`
that fails on any field name the coordinator has no recorded disposition for
turns that rule into a build failure. Current dispositions:

| field | planned-node disposition |
|---|---|
| `prompt`, `depends_on`, `handoff` | allowed (prompt must be non-empty) |
| `id` | constrained — no `/`: the splice namespace is minted by the loader alone (`validatePlannedNodeID`, ADR 0027) |
| `type` | constrained — `claude-run` only; `gate` rejected |
| `allowed_tools` | constrained — non-empty, `plannedToolAllowlist` only |
| `permission_mode` | constrained — `bypassPermissions` rejected |
| `cwd` | rejected |
| `agent` | **rejected** |
| `worktree` | **rejected** (the engine would run `git worktree add` on an unreviewed plan's say-so — see "Worktree isolation") |
| `success_check.verify` | **rejected when planner-authored** (`exit_zero`/`result_matches` allowed); trusted code may set it strictly after validation, from the user-supplied `--verify-cmd` string (`coordinator.attachVerifyCommand`, ADR 0016 §2) |
| `use` | **rejected** — a planner-emitted `use:` would let unreviewed output pick which local file's prompt text, tool grant and verify command get spliced in, and a fragment file in the run's repo is attacker-influencable whenever the repo is untrusted (ADR 0013: trusted code resolves files, the planner never names local resources). Refused at the coordinator's `graph.Parse` boundary |
| `with` | **rejected** — `use`'s substitution bindings, on the same grounds: dead without a `use:`, and a `with:` on a planned node means the plan tried to reference a fragment at all |
| `budget_usd`, `timeout` | allowed |
| `retry` | constrained — bounded re-runs of an already-ceilinged node, but a planned `max` above `maxPlannedRetries` (3) is rejected: `verify_failed` is a legal cause, so retry count is the one lever planner output still has on an injected evidence command's execution (ADR 0016 §2) |
| `feedback` | constrained — `retry`'s standing one level up: bounded re-runs of body nodes already inside every ceiling, granting no tool, no path, no shell; the load validations hold for a planned graph exactly as for a hand-written one, but two things they leave open are closed here (ADR 0010). **max**: only `max` ≥ 1 is required at load and a plan has no human reviewer for the upper bound, so a planned `max` above `maxPlannedFeedbackRounds` (3) is rejected. **Reach**: an arc on a fan-in declarer may name a target whose body excludes a producer the declarer judges — valid, and unable to converge (#118) — so `validatePlannedFeedbackReach` refuses it whenever `graph.LintFeedbackReach` found a covering target, naming that target in the refusal. **The quote**: an arc whose loop body never quotes `{{ feedback.<declarer> }}` re-runs a prompt that cannot have changed, for every round of `max` — valid, and ADR 0028's specimen — so `validatePlannedFeedbackQuoting` refuses it, naming the token to paste and the prompt it belongs in |

Both mechanisms apply ONLY to coordinator-planned graphs; hand-written YAML
(`oh-my-graph run`) is human-authored/reviewed, passes a nil deny list, and is
not restricted by either. The generated spec is
saved to `~/.oh-my-graph/runs/<run-id>/graph.json` — being valid YAML it can be
hand-edited and re-run with `oh-my-graph run` — then executed by the same
Scheduler as any other graph. A `--plan-only` plan is saved the same way but
to `~/.oh-my-graph/plans/<id>/graph.json`, because it has no run to belong to.
So is a **declined chat plan's**: chat's commitment to execute does not exist
until a human answers its `[y/N]`, so the spec save happens after that answer
and a `n` leaves no run directory at all (ADR 0023 §2.4). It used to save
before the prompt, which meant declining manufactured a `runs/<id>/` holding a
`graph.json` and no `state.json` — a corrupt run produced by saying no.

### Goal cycles — `auto --max-cycles N` (ADR 0011)
`auto` plans once by default; `--max-cycles N` (N ≥ 2) opts into the bounded
goal loop: **plan → validate → execute → assess**, at most N cycles, each
cycle a whole ordinary run of a freshly planned graph. The flag *is* the
bound — 0 and negatives are rejected at parse, there is no config or env
default — and `--max-cycles 1` (the default) is byte-identical to today: one
plan, one run, no assessment call, no new files or fields.

The cycle engine is `coordinator.RunGoal`; `planAndExecute` is its only
caller, supplying the save→print→confirm→execute sequence as the
`ExecuteCycle` callback — so `auto` and chat cannot drift, and the loop is
unit-testable against `FakeRunner` with zero real spawns. Chat stays
single-cycle in v1: it calls `planAndExecute` with `singleCycle`
(`commonRunFlags` carry no cycle count). Per cycle:

- **Plan/validate**: `coordinator.Plan` verbatim — the full field-disposition
  table and layered ceiling, every cycle, no caching. On cycle k ≥ 2 the
  planner prompt gains a continuation section carrying the previous
  assessment's `remaining` (truncated, quoted as context, never as a rule
  change). **The cycle's run id and leg are opened BEFORE this call**, through
  `GoalOptions.OnCyclePlanning` — the CLI mints and owns both, the loop takes on
  no closing obligation — so each cycle's planner call reads `PLANNING` exactly
  as a single-cycle `auto`'s does (ADR 0023). Without the hook the status would
  exist for `auto` and not for `auto --max-cycles 3`, and a status that depends
  on a flag is worse than no status. A cycle whose plan is REFUSED keeps that
  directory: it holds `rejected.json`, no snapshot, and reads `FAIL`.
- **Execute**: an ordinary run — own run id, directory, `graph.json`,
  `state.json`, `events.jsonl`, `resume.lock`. The snapshot additionally
  carries the additive `goal` block (`text`, `cycle`, `max_cycles`,
  `first_run_id` — the group key; no schema bump, absent on single-cycle
  runs, preserved across resume legs). The browser live-view launch fires
  for cycle 1 only; later cycles still serve their own view and print its
  URL. `serve` stays a per-run view and shows the goal block in its header
  (`/api/graph` carries it beside the DAG); a goal-level view that follows
  the chain is additive later.
- **Assess**: `coordinator.Assess`, the third coordinator call class, under
  its own stricter stance — `--tools ""`, settings-isolated, strict MCP,
  deny list extended with Read/Glob/Grep (measured: E8, ADR 0011's
  Measurement outcome — a read-this-file lure in an artifact did not reach
  the verdict) — judging only engine-assembled
  material: the goal, the run outcome, per-node verdict/detail/cost from the
  snapshot (the loop re-reads `state.json` after `executeGraph` returns —
  the observation seam), bounded head+tail artifact excerpts, and the one
  cross-cycle line (the previous `remaining`). All three are raw model
  output, so every block rides in a **nonce-fenced** marker pair — one
  6-hex-character nonce per `Assess` call, in the opening AND closing
  marker, with the prompt telling the assessor that only markers bearing it
  are real (`internal/fence`; ADR 0012's inlined skill bodies were its first
  caller and are gone, the mechanism is not).
  Fixed markers would be forgeable by the very material they fence: an
  injected artifact could close its own block and speak from apparent
  outside it. The next cycle's planner prompt fences the `remaining` it
  quotes the same way. The verdict is a hard JSON
  contract; garbage is an `*AssessError` that stops the loop. Each verdict
  is printed the moment it returns (`GoalOptions.OnCycleAssessed`) and
  persisted as `assess.json` in that cycle's run directory (`goal_met`,
  `remaining`, `evidence`, `assess_cost_usd` — the assessment cost the
  cycle's ledger cannot include, since the ledger prints before assessment).

Termination and exit follow ADR 0011 §2's (outcome × verdict) precedence:
`goal_met` always stops the loop, but exit 0 additionally requires the final
cycle's run to have **passed** — the untrusted judge can stop spending, never
flip an engine-reported failure. Unmet-and-exhausted (or the optional
`--max-goal-budget-usd` soft ceiling tripping at a cycle boundary, or a
declined confirm on a later cycle) exits 1 with the final `remaining`; a
session-limit pause pauses the whole loop (exit 2, standard resume hint —
the resumed run completes as an ordinary run; the goal loop does not
re-enter). The ceiling is rejected at parse without `--max-cycles ≥ 2` — a
flag that could never fire would read as a bound that isn't one. When the
loop ends, the **goal summary** prints below the final ledger: one line per
cycle (run id, outcome, run total with planning included, assessment cost,
verdict) and the grand total — the multiplier is printed, never derivable.
A cycle that ended the loop mid-flight (pause, planning failure, garbage
verdict) prints as an explicit incomplete cycle — a failed assessment's own
cost still counted (`AssessError.CostUSD`), the run spend pointed at its own
ledger — so the summary never under-counts silently.

## Object design (SRP; responsibilities → collaborations)
- **Graph** — validated nodes + adjacency; "is DAG?", "roots?", "dependents of X?". Pure data.
- **Node** — value object (id, type, prompt, cwd, tools, permission, budget, timeout, success_check, retry, handoff, depends_on, agent, worktree, feedback, and the load-time-only use/with).
- Edge = implicit `Node.DependsOn []string` (no struct).
- **Scheduler** — drive DAG: ready/running sets, cap, context cancel, halt/continue;
  calls NodeRunner.Run, consults Graph, writes RunLedger, asks Handoff to resolve/persist.
- **NodeRunner (interface)** — THE claude-exec seam. Injected (constructor), not global.
  ```go
  type NodeRunner interface {
      Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
  }
  type NodeInvocation struct { Prompt, Cwd, PermissionMode, ResumeSession, SessionID, Agent string; BudgetUSD float64; Timeout time.Duration; Policy ToolPolicy }
  type NodeOutcome struct { SessionID, Result string; TotalCostUSD float64; ExitCode int; FailureCause string; BudgetExhausted, SessionLimited bool }
  ```
  - `CLIRunner` (prod): builds argv, SCRUBS ANTHROPIC_API_KEY/AUTH_TOKEN,
    execs under context, parses JSON. One of the exactly four objects that
    spawn a process (the others: `ShellVerifier`, `worktree.GitManager`,
    `browser.ExecOpener`).
  - `FakeRunner` (tests): scripted `map[key]NodeOutcome` keyed by the
    invocation (`NodeInvocation` has no id field; the key defaults to
    `spec.Prompt`, and tests set each node's prompt equal to its id so the
    "keyed by node id" shorthand reads true) — entire scheduler (topo,
    fan-out, fan-in, retry, halt, cost sum) unit-testable with ZERO real
    spawns. This is the testability keystone.
- **Verifier (interface, v1.1)** — THE evidence seam, separate from NodeRunner
  because a verification command is not a claude invocation. Injected the same
  way. `ShellVerifier` (prod, the only object in its package that spawns),
  `RefusingVerifier` (default — a forgotten injection fails loudly),
  `FakeVerifier` (tests). See "Success checks".
- **Worktree Provider (interface)** — THE worktree-provisioning seam: resolves
  a node's `worktree: <name>` to its managed checkout, creating it on first
  use (idempotent per name). `GitManager` (prod — the third spawner, ADR
  0005), `RefusingProvider` (default — a forgotten injection fails loudly),
  `FakeManager` (tests). Run-end cleanup is the CLI's job against the
  concrete `GitManager`, never the Scheduler's.
- **Browser Opener (interface)** — THE browser-open seam: opens a URL (the
  `serve` live view) in the user's default browser. `ExecOpener` (prod — the
  fourth spawner, ADR 0006: `open`/`xdg-open`/`cmd /c start` behind build
  tags), `RefusingOpener` (injection safety — code that must hold an Opener
  without ever opening fails loudly), `FakeOpener` (tests). Wired at the
  `run`/`auto`/`resume` call sites only, behind the TTY-and-not-`--no-web`
  gate (see "Web live view"); everywhere else the Opener is nil — the live
  view is off entirely and no Opener is consulted.
- **Handoff** — interpolate {{artifacts/inputs}}, persist outputs, pick --resume
  session. Gains `Seed(nodeID, artifactPath, sessionID)` so a resumed run can
  rehydrate a previous leg's artifacts and session ids without Handoff having to
  know what a snapshot is.
- **Coordinator** — auto mode: one planner NodeRunner call → JSON graph spec →
  existing Parse/Validate (+ planned-node field dispositions). Owns the tool
  ceiling policy (`Plan.ToolPolicies`). Never runs the graph itself.
- **GateController (interface, v1.1)** — answers approve/reject/pause for a gate
  node. `PauseController` for fresh runs, `RecordedController` (snapshot-backed)
  for `resume`; chosen at the CLI boundary, invisible to the Scheduler.
- **RunState (v1.1)** — owns `state.json`: the resumable snapshot (graph, inputs,
  flags, tool policies, per-node completion incl. session id, gate decisions).
  Written atomically after every node. The Scheduler talks to a `Recorder`
  interface and defaults to a no-op, so nothing about persistence leaks into the
  engine's tests.
- **RunFeed** — owns `events.jsonl`: the append-only, schema-versioned stream of
  node lifecycle events (run_started/node_started/node_passed/node_failed/
  node_retried/gate_paused/gate_approved/gate_rejected/run_finished), one JSON
  line per transition, fsynced per line.
  Emitted from the same scheduler hook points as the progress line and the
  snapshot, via an `EventSink` interface defaulting to a no-op — the third
  destination next to `Recorder`, same seam pattern. `node_started` and
  `node_retried` publish the attempt's pre-assigned session id (see
  `--session-id` above), so a consumer can locate a running node's transcript
  before the terminal event. This is the stable consumer contract — the
  in-repo views (`runs list`, `show`, `watch`, `serve`) read a run through it
  like any external consumer would, which is what keeps it honest; the
  full contract, including how it versions alongside `state.json`, is
  docs/RUN-FEED.md.
- **RunStatus** (`internal/runstatus`) — the one composition of the two facts
  those views need and neither owner has: the stream's leg state (RunFeed's
  `InFlight`) and the run's liveness (RunState's `ProbeLock`). It answers
  settled / in flight / abandoned, spawns nothing and writes nothing, and it
  owns the recovery wording every surface prints. RunFeed stays a pure
  stdlib reader of the stream and RunState keeps the lock file it already
  owned; this is only their meeting point (ADR 0015 §2).
- **RunLedger** — record session_id/cost/verdict/timing, plus auto mode's one
  planning-call cost; end-of-run table + total cost (planning cost included, so
  an auto run's total is honest; a hand-written `run` records no planning cost).
  Every PASS row is qualified by **how** the verdict was reached — `verified` /
  `self-reported` / `exit-only` / `approved`, a closed set derived in trusted
  code from the predicates the engine actually evaluated (ADR 0016 §6). The
  qualifier sits beside `Verdict`, never replacing it, so nothing that tests for
  PASS/FAIL changes; it is carried into `state.json` and onto `node_passed` from
  the same `ledger.Record`, so the table, the snapshot and the feed cannot
  disagree about it. A FAIL carries none — its cause is in `DETAIL`.

Node lifecycle: Scheduler ready → Handoff.ResolveInputs → NodeRunner.Run →
exit_zero → result_matches → Verifier.Verify → pass: Handoff.PersistOutput →
budget_usd check → pass: RunState.RecordNode + RunLedger.Record + enqueue
dependents; fail (any check, Verifier, OR budget): retry or Record(FAIL) +
cancel if halt; gate pause: drain in flight, snapshot, exit 2. Output is
persisted before the budget verdict so an over-budget node's artifact
survives. Scheduler never knows if a real claude ran, or what actually ran
the verification.

## MVP scope (v0.1) — smallest thing that runs a real multi-node graph
IN: YAML loader + DAG/cycle validation; {{inputs}}/{{artifacts}} interpolation;
concurrent ready-set scheduler + cap + halt-on-fail; CLIRunner (exact argv,
ENV SCRUB, timeout, JSON parse); FakeRunner + full scheduler unit tests (no real
claude in CI); artifact handoff (default) + session handoff (simple one→one);
success_check (exit_zero + result_matches); RunLedger table + total cost;
CLI `oh-my-graph run <graph.yaml> --input k=v` and `oh-my-graph auto "<goal>"`
(planned graphs — see Auto mode); nodes run in real cwds with session
persistence ON (so every node stays an ordinary, readable claude session in
`~/.claude/projects` — do NOT pass --no-session-persistence).

DEFERRED (say so in README): retries beyond flat max:1 (the per-cause filter
`retry.on`, over the closed cause set, shipped later — see "Execution
engine"); parallel-group sugar /
any DSL; a terminal TUI (the web views shipped later — both the single-run
live view and the multi-run dashboard; see "Web live view"); worktree
auto-creation (opt-in
per-node `worktree:` shipped later — see "Worktree isolation"); sub-call /
cross-node budget accounting (per-node
mid-node kill via `--max-budget-usd` and post-hoc budget halt ARE both enforced
— see "Execution engine").

That list is what was never built. The other half — what IS built and still
falls short of what a reader might assume from this spec — is
[docs/LIMITATIONS.md](docs/LIMITATIONS.md): the platform notes, and the
per-feature gaps each shipped mechanism left behind (a `success_check` with no
`verify` is still self-report; a PASS row does not say which of a two-valued
verdict passed; `budget_usd` is per node only; isolation stops at the
invocation repository). This spec says what the design intends; that file says
where the shipped thing does not reach it, and the two are kept in step
deliberately rather than merged — a boundary stated once, in the document
whose subject it is.

## v1.1 scope
IN: evidence-grounded `success_check.verify` (#7); `gate` execution +
`oh-my-graph resume` (#9); the layered tool ceiling for planned nodes and the
planned-node field dispositions (#11); node-level `agent:` for hand-written
graphs (PR #6). Each ships as its own PR — see "Implementation sequencing".

## Repo layout
```
cmd/oh-my-graph/{main,flags,argslot,init,resume,gateresume,runs,show,watch,serve,chat,goal,lint,dryrun,liveview,verifycmd,runleg,runlock,version}.go + _test  CLI: parse flags, load, inject CLIRunner+ShellVerifier, init/run/auto/resume/runs/show/watch/serve/chat, the `auto --max-cycles` goal loop (goal.go — ADR 0011) and the GateResumer serve's gate routes call back through (gateresume.go — ADR 0014), the `--verify-cmd` pre-flight, shared by `auto` and `resume`, its two disclosures and the build-evidence gate one directory scan feeds (verifycmd.go — ADR 0016, ADR 0030), print ledger
internal/graph/{graph,validate,feedback,feedback_reach,fragment}.go + _test + testdata/{pre-migration,golden}/  Graph/Node value objects, YAML, DAG validation, ReadyGiven, feedback edges + the advisory sweep for an arc that misses a fan-in producer (feedback_reach.go — advisory on purpose; ADR 0010's alternatives record why the escalation is neither sound nor complete), and the load-time fragment resolver (LoadFile/LintLoadFile, one read per path — ADR 0013)
internal/schedule/{scheduler,errors,feedback,retryfeedback}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors + the bounded runtime re-run of a feedback edge (ADR 0010) + the fenced, one-deep quote of the attempt a retry repeats (retryfeedback.go — ADR 0020)
internal/runner/{runner,runtime,cli,claude_protocol,codex_protocol,preflight,sessionlimit,fake}.go + build-tagged procgroup_{unix,windows}.go + _test  interface + ToolPolicy + CLIRunner(ENV SCRUB) + the one runtime selection (runtime.go — ADR 0025) + the two protocols beneath it, each owning binary/argv/session/output (claude_protocol.go mints the session id before spawn, codex_protocol.go learns its thread id from thread.started) + the per-runtime graph preflight (preflight.go) + the subscription session-limit recognizer (sessionlimit.go — ADR 0009, Claude-shaped by decision, not by omission) + FakeRunner
internal/verify/{verify,shell,fake}.go + build-tagged {shell,procgroup}_{unix,windows}.go + _test  Verifier seam — ShellVerifier is the second of the four exec seams (ADR 0002)
internal/worktree/{worktree,git,fake}.go + _test  worktree Provider seam — GitManager is the third exec seam (ADR 0005): per-run managed checkouts + work-preserving cleanup
internal/browser/{browser,exec,fake}.go + build-tagged argv_{darwin,unix,windows}.go + _test  browser Opener seam — ExecOpener is the fourth exec seam (ADR 0006): default-browser launch, wired behind run/auto's TTY gate
internal/invariants/exec_seam_test.go          test-only: asserts only the four exec seams' files import os/exec — 8 files, since a seam's platform-specific procgroup files belong to it (a ninth importer fails CI — ADR 0002/0005/0006). A separate, shorter list names the 4 spawn CALL SITES (one per seam, procgroup files excluded — they mutate an already-built *exec.Cmd) and asserts each scrubs its child env through internal/childenv
internal/childenv/childenv.go + _test          the shared "delete billing-switching vars" child-env policy (all four spawners)
internal/fence/fence.go + _test                the shared data fence: a per-call crypto/rand nonce for both markers of any quote of untrusted text into a prompt, plus the head+tail bound on the quoted material. Its callers live in coordinator and schedule, and their number is stated in fence.go alone — internal/invariants counts the real ones repo-wide against that one sentence, so a second copy here would be a number nothing checks
internal/coordinator/{coordinator,router,agentmap,agentstage,skillscan,skillstage,goal,assess,repair,verifycmd,unisolated}.go + _test  auto mode: goal → planner call (NodeRunner seam) → validated graph + ToolPolicies; chat routing; post-validation subagent mapping with its definition staged (agentmap.go/agentstage.go — ADR 0022) and skill activation over a staged plugin directory (skillscan.go/skillstage.go — ADR 0017, superseding ADR 0012's inlining); the shared nonce fence (internal/fence, used by Assess and by the re-plan); the bounded plan→execute→assess goal loop (goal.go/assess.go — ADR 0011); the bounded re-plan a validation refusal buys (repair.go)
internal/handoff/{handoff,placeholder_lint,session_lint,verdict_lint,tool_grant_lint,verify_inline_lint,feedback_quote_lint}.go + _test  interpolation, artifact persist/resolve, session pick, Seed for resume — plus the advisory lint sweeps `lint`/`run --dry-run` print — and a plain `run` does NOT (unresolvable {{placeholders}}, session-handoff `--resume` that may not deliver the parent conversation, a prompt demanding a verdict token no `result_matches` reads, a `result_matches` that silently dropped the node's exit-code guard, a node that declares neither an `allowed_tools` grant nor a `success_check.verify` and so can observe no tool denial — #154 — a `success_check.verify.command` splicing a model's own text into the shell command line the engine runs: `{{ artifacts.<id> | inline }}`, whose filterless form would be the engine's own file path, or `{{ feedback.<id> }}`, which has no filterless form — and a feedback loop whose body never quotes `{{ feedback.<declarer> }}`, so the re-run repairs nothing: ADR 0028)
internal/gate/gate.go + _test                  Decision + PauseController/RecordedController
internal/runstate/{runstate,recorder,lock}.go + build-tagged flock_{unix,other}.go, pidprobe_{unix,other}.go and fstype_{darwin,linux,other}.go + _test  state.json snapshot — atomic write, schema version, resume load — plus the run lock: an flock(2) a leg holds for its duration (AcquireLock) and a reader may probe without writing anything (ProbeLock — ADR 0015 §1)
internal/runfeed/{runfeed,reader}.go + _test   events.jsonl append-only lifecycle event stream — the consumer contract (docs/RUN-FEED.md) — plus the in-repo consumer readers (InFlight, Follow)
internal/runstatus/runstatus.go + _test        the one shared rule (ADR 0015 §2): open leg AND held lock ⇒ in flight, open leg AND free lock ⇒ abandoned — composed once for `runs list`, the dashboard card, ResolveRun, the single-run view's /api/graph and `watch`, plus the recovery wording those surfaces print
internal/serve/{serve,dashboard,card,resolve,transcript,gate,build}.go + ui/ + _test  `serve`: 127.0.0.1-only web views — the dashboard (`dashboard.go`/`card.go`: one live mini-DAG card per run, run views mounted at /run/<id>/) and the live view of one run — embedded static UI (go:embed) + vendored cytoscape.js; a run-feed consumer with token-guarded gate actions — every route reads the contract (plus the live transcript tail of a running node's own session) except the mutating pair (`gate.go`: approve/reject the paused gate through the injected GateResumer — ADR 0014); `build.go` names the build answering the page, stat'd once per process
internal/ledger/ledger.go + _test              RunLedger summary + total cost
graphs/haiku-smoke.yaml, graphs/dev-review-pr.yaml, graphs/self-dev.yaml, … + graphs/embed.go  the shipped pipelines, embedded with `//go:embed *.yaml fragments/*.yaml` (globs, so a new template or fragment ships automatically; the second pattern is required because `*.yaml` does not descend, and a template citing `use:` needs its fragments/ sibling on disk) — `oh-my-graph init [dir]` walks that payload and unpacks it into <dir>/graphs/, nested paths included (dir defaults to `.`), never overwriting: an existing target is kept untouched and reported as `kept` while the missing ones are written (so a re-run delivers a payload addition and an edited template survives it), a kept file whose bytes differ from the payload is marked `DIFFERS` and counted (a top-up can pair a freshly written template with a kept older fragment, which fails at load, not at `init`), and a failure partway through removes the files AND directories it created — `graphs/` itself included when that run made it
graphs/fragments/{e2e-verify,review-security,review-style,pr-publish,repair-round,gated-lane}.yaml  the shipped node shapes and loops the templates cite with use: (ADR 0013, ADR 0027, ADR 0029); cited by self-dev.yaml, dev-review-pr.yaml, backlog-batch.yaml and adr-driven-dev.yaml (+ internal/graph/shipped_graphs_test.go asserts every shipped graph loads BOTH from the checkout and from the binary's own unpacked payload — the second is what proves `init` emits graphs that load)
docs/adr/00{01..30}-*.md                       (0020 is the retry ADR, renumbered from the 0016 it collided on; the build-evidence ADR kept 0016, so every bare "ADR 0016" in the tree resolves)
docs/measurements/{*.md,probes/<adr>-<name>/}  the raw record behind a measured claim: pre-registrations written before the first spawn, the runner scripts, every prompt file verbatim, and one line per spawn — so a number in an ADR or a CHANGELOG entry is re-derivable rather than quotable
README.md, SECURITY.md, LICENSE(MIT), go.mod, Makefile(build/test/lint)
```

## Verify MVP cheaply (real claude, cents)
`graphs/haiku-smoke.yaml`: node `write` ("write a 3-line haiku about graphs to
haiku.txt", acceptEdits) → node `critique` (depends_on write, reads
{{artifacts.write}}, plan). `mkdir -p /tmp/omg-smoke && oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke`.
Proves: subscription auth (succeeds with API key unset/scrubbed), sequential edge +
artifact handoff, JSON capture (2 session_ids + costs), RunLedger, and both
sessions landing in `~/.claude/projects`. CI stays free — real-claude smoke is a manual
`make smoke`, never in CI (all logic tested via FakeRunner).

## SECURITY / ToS stance (state honestly in SECURITY.md)
Personal/local tool re-using YOUR OWN logged-in claude session (same standing as
claude-squad / running `claude -p` yourself). NOT a hosted/redistributed product
authenticating others via subscription OAuth (that violates ToS). Never ships
credentials, never proxies auth, never runs as a shared service. Scrubs
ANTHROPIC_API_KEY/AUTH_TOKEN plus OPENAI_API_KEY/CODEX_API_KEY from every child
(one list, no runtime branch; unit-tested). Never --bare, never an Agent SDK. Least privilege per node (allowed_tools + permission_mode); bypassPermissions
opt-in per node with a loud warning, never a default. For auto-planned graphs
(untrusted LLM output run unattended under `dontAsk`), least privilege is not
just a prompt convention and not just a declaration check:
`coordinator.validatePlannedNodes` rejects a planned node whose `allowed_tools`
is empty or names a tool outside the fixed allowlist, or that sets `cwd`,
`agent`, or a planner-authored `success_check.verify` (trusted code attaches the
user's `--verify-cmd` after validation, ADR 0016 §2); and `Plan.ToolPolicies`
imposes a per-node
execution ceiling (settings-source isolation + scoped allow under default-deny +
tool narrowing + strict MCP + residual denies) so the user's own standing tool
grants cannot widen an unreviewed plan — unless that user says at launch that
they should, which is `--accept-loaded-user-config` and drops the first and
fourth of those five. All of it, and the gaps that remain, are
in "Auto mode" above.

## Open questions (decided defaults; refine in impl)
1. artifact interpolation: substitute file PATH by default, `| inline` filter for content.
2. budget: **enforced at two layers.** Post-hoc — a node whose actual cost
   exceeds its `budget_usd` fails exactly like a failed success_check
   (`NodeBudgetError`, ledger FAIL carrying budgeted-vs-actual, halt-on-fail by
   default), so its dependents never start; the RunLedger carries the declared
   budget alongside actual cost, making the delta derivable per node
   (`Record.BudgetDeltaUSD`). **Mid-node kill is now real too**, not deferred: a
   positive `budget_usd` is passed to the subprocess as `claude
   --max-budget-usd`, and the CLI aborts the node the moment its own spend
   crosses the budget (verified on claude 2.1.220 — a parseable JSON envelope
   with `subtype: error_max_budget_usd`, mapped to `BudgetExhausted` and raised
   as the same `budget_exceeded` `*NodeBudgetError`). The kill is **per `claude
   -p` invocation = one node** (a resumed session does not re-count its parent's
   spend), so it bounds a runaway node without a fabricated $/minute heuristic.
   Still deferred: sub-call cost observation (the one in-flight call past the
   threshold can overshoot, which the post-hoc layer backstops) and any
   cross-node/whole-graph budget — those would need `--output-format
   stream-json` incremental parsing, an ADR-level change to the one-envelope
   `NodeRunner` contract. See "Execution engine" for the full two-layer detail
   and why a budget-derived wall-clock timeout was rejected as fake enforcement.
3. parallel nodes sharing one cwd can race edits → v0.1 parallel nodes should be
   read-only (plan) reviews (the motivating fan-out case); parallel edits want
   worktrees — now available as per-node `worktree:` (see "Worktree
   isolation").
4. session-handoff + multi-parent fan-in conflict → validation rejects `handoff:
   session` on a node with 2+ session-parents; multi-parent must use artifact.

## Empirical verification of the tool ceiling
`--help` prose is not ground truth. PR #5 already found the CLI's real deny/allow
precedence did not match what `--help` implied, so the layered ceiling above
separates what has been *read out of the shipped CLI* from what took a real
invocation to settle. Every claim below is one or the other; nothing here rests
on documentation.

### Read out of the binary (free, no API call), claude 2.1.220

- **V1.** `--setting-sources` parses `user`/`project`/`local`; `""` yields the
  empty list; anything else is a hard error. `flagSettings` (`--settings`) and
  `policySettings` (enterprise) are unioned on top and cannot be dropped. So
  `--setting-sources ""` = "load none of the user's settings files".
- **V2.** Under `dontAsk`, a tool call whose rule evaluation lands on *ask* is
  converted to `{behavior: "deny", decisionReason: {type: "mode", mode:
  "dontAsk"}}`. The CLI is already default-deny for our unattended nodes.
- **V3.** `--tools` feeds a distinct permission-rule source labelled
  "CLI tool narrowing", i.e. it narrows the tool set rather than adding an allow.
- **V4.** An enterprise `allowManagedPermissionRulesOnly` policy causes
  `--allowedTools` rules to be *ignored entirely*. On such a machine the ceiling
  is the managed policy, not ours. Worth a line in SECURITY.md.

### Measured, 2026-07-29, claude 2.1.220

Run as one-off manual invocations against a real machine whose
`~/.claude/settings.json` grants `Bash(*)`, `Write(*)`, `WebFetch(*)` — the
exact power-user configuration the ceiling exists for. Evidence is the
filesystem and the envelope's own `permission_denials` array, not the model's
narration of what it was allowed to do. These are NOT part of `make test`; the
automated suite stays spawn-free.

- **E1 — CONFIRMED. Layers 1+2 are a real ceiling.** Same node declaration
  (`--allowedTools "Bash(git *)"`, `--permission-mode dontAsk`), one variable:
  - *without* `--setting-sources ""`: `touch <path>` **ran** (file created,
    `permission_denials: []`). This is the documented gap, reproduced.
  - *with* `--setting-sources ""`: the same command was **denied**
    (`permission_denials: [{tool_name: "Bash", …}]`, no file on disk).
  - *with* `--setting-sources ""`, in-scope `git init <path>`: **allowed**.

  So the scoped pattern binds — `Bash(git *)` means git and nothing else —
  rather than being a declaration. This is what licenses narrowing the Bash-gap
  wording in README/SECURITY.md **for planned nodes only**.
- **E2 — ANSWERED: yes for DISCOVERY, and that is not the same as `--agent`.**
  `--setting-sources ""` disables discovery of the user's agent definitions.
  `--agent code-reviewer` under isolation fails at startup with *"not found.
  Available agents: claude, Explore, general-purpose, Plan, statusline-setup"* —
  only built-ins survive. **Re-confirmed at claude 2.1.228 on 2026-08-12, and
  widened**: that same list names neither `~/.claude/agents` nor the
  repository's `.claude/agents`, so a repository cannot supply a mapped node's
  system prompt **by discovery**
  (`docs/measurements/0017-staged-agent-restores-layer-1.md`, arm `K-NEG`;
  re-run against this build's own argv as (l)'s `L-NEG`, with the repository's
  definition committed in the node's cwd).

  **Staging is a second channel and this entry does not cover it.** The
  sentence above said "cannot supply a mapped node's system prompt" without the
  last two words until 2026-08-12, which generalized a discovery result onto the
  pipeline that ships: a scanned definition is COPIED into a `--plugin-dir`, and
  `DefaultAgentDirs` scanned `<cwd>/.claude/agents` with the project shadowing
  the user. Measured, the repository's definition was the system prompt that ran,
  2 of 2. The scan is now `~/.claude/agents` only
  (`docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md`).

  **What E2 does NOT say is that Layer 1 and `agent:` cannot be combined**, and
  that inference — which this entry drew, and which cost the ceiling on every
  mapped node until 2026-08-12 — is refuted by the same measurement.
  A `--plugin-dir` supplies definitions without reopening Layer 1, and a plugin
  directory can carry `agents/`: with the matched definition staged there,
  `--agent` resolved **3 of 3** under `--setting-sources ""`, attributed by a
  marker token, with removing `agents/` from an otherwise identical directory as
  the control (exit 1). So a coordinator-MAPPED node keeps Layer 1 and stages
  its agent (ADR 0022) rather than trading the ceiling for the mapping. The
  constraint that remains is narrower and still real: Layer 1 cannot be extended
  to a HAND-WRITTEN graph's `agent:`, whose definition oh-my-graph does not
  stage and whose node is the user's own reviewed artifact.
- **E3 — CONFIRMED SAFE. `--setting-sources ""` does NOT affect subscription
  OAuth.** With `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` absent from the
  environment, `claude -p '…' --output-format json --permission-mode plan
  --setting-sources ""` returned a normal envelope (`is_error: false`, a
  `result`, `provider: "firstParty"`). Since no API key existed to fall back to,
  a successful call can only have resolved OAuth. Credentials are not settings,
  and dropping settings does not touch them. Independently, the run confirmed
  the flag was in effect: the `model` pin from the user's settings.json was not
  applied. **The project's #1 invariant is intact.**
- **E4 — CONFIRMED: `--tools` REPLACES, and `--allowedTools` cannot resurrect an
  omitted tool.** `--tools "Glob" --allowedTools "Read"`, asked to read a file:
  the model reported having no read tool and made zero tool calls
  (`num_turns: 1`, `permission_denials: []` — it never got as far as a
  permission decision). Layer 3 is therefore a genuine narrowing and must list
  every tool the node needs; it is not additive with Layer 2. The two axes
  compose as an intersection: `--tools` decides what exists, `--allowedTools`
  decides what is permitted.
- **E5 — NOT MEASURED.** No project `.mcp.json` was available to test whether
  MCP servers survive `--setting-sources ""`. `--strict-mcp-config` ships as
  Layer 4 regardless, since oh-my-graph never passes `--mcp-config` and the flag
  is therefore free; but **no claim is made** that MCP is closed, and
  SECURITY.md says so rather than implying coverage that was not observed.
- **E6 — MEASURED, and now load-bearing for one real path.** With
  `--agent code-reviewer` (frontmatter `tools: Read, Grep, Glob, Bash`) plus
  `--tools "Read"`, the node could not run a shell command: zero tool calls, no
  permission denial. So a resolved subagent's frontmatter does not widen past
  `--tools`.

  A coordinator-MAPPED planned node (see "Node-as-subagent") runs exactly this
  configuration — `--agent` plus the node's `--tools`/`--allowedTools` — so E6
  is the measurement that bounds it, with the coordinator's own
  frontmatter-subset check on top. **The result still does not transfer to the
  hand-written path**, which never passes `--tools` at all: there the precise
  composition between a subagent's `tools:` and a node's `allowed_tools`
  remains unmeasured, and oh-my-graph states **no reconciliation rule** for it.
- **E7 — CONFIRMED: `--setting-sources ""` drops the project CLAUDE.md.** In a
  directory whose `CLAUDE.md` defined a codeword, a plain `claude -p` returned
  the codeword and the same call with `--setting-sources ""` returned
  "NOCODEWORD". This was measured rather than assumed because a design decision
  rests on it (the planner call is deliberately NOT isolated, so it keeps the
  user's CLAUDE.md — see `coordinator.Plan`) and because it is the concrete cost
  the README promises to disclose. Settings *hooks* were not separately
  measured: they are defined inside the settings files that V1 established are
  not loaded, so "no settings, no hooks" follows from V1 rather than being an
  independent claim.

Two corrections these measurements force on text written before them: the
Bash-gap disclosure is now false **for planned nodes** and must be narrowed
rather than repeated, and the claim that an unresolvable `--agent` "falls back
to plain claude" was wrong — see "Node-as-subagent".

## Implementation sequencing (v1.1, four PRs, shared hot files)
`internal/schedule/scheduler.go`, `internal/graph/graph.go`,
`internal/runner/*.go` and `internal/coordinator/coordinator.go` are shared. The
constraint that matters is not "same file", it is **same function**.

| work | owns | scheduler footprint |
|---|---|---|
| #8 budget | `ledger.go`, `flags.go` | `runNode` post-outcome, `Options` |
| #11 + PR #6 | `runner/*.go`, `coordinator.go` | `buildInvocation`, `Options` |
| #7 verify | `verify/*`, `childenv/*` | `runNode` post-outcome, `Options` |
| #9 gate+resume | `runstate/*`, `gate.go`, `handoff.go` | `Run` seeding + `runNode` gate branch, `Options` |

- **#11 + PR #6 runs in parallel with #8, starting now.** Its scheduler change is
  confined to `buildInvocation`; #8's is confined to `runNode`. They collide only
  on adding a field to `schedule.Options` and a line to `main.go` wiring —
  textual, not semantic. One coordination point: #11 owns the
  `NodeInvocation`/`buildArgs` refactor into `ToolPolicy`, so if #8 implements a
  mid-node kill via `--max-budget-usd` it must add a plain scalar field and not
  restructure the invocation type.
- **#7 waits for #8 to merge.** Both rewrite the same ~20-line post-outcome
  region of `runNode`, and both introduce a "the node returned, but the run
  should stop" concept. Sequencing them stops two developers inventing two halt
  vocabularies that then have to be reconciled.
- **#9 goes last**, after #8 and #7. It restructures `Run`'s ready-set seeding
  and adds the drain-don't-cancel pause path — merging that under two in-flight
  `runNode` changes is how a halt path gets quietly broken. It also has to
  snapshot the *final* per-node record shape, which #8 (cost) and #7 (verify
  verdict) both change; designing `state.json` against a moving target buys a
  schema migration on day one.
- **#9 can be split to start immediately.** `#9a` — `internal/runstate`,
  `handoff.Seed` + session-id persistence, `graph.ReadyGiven` — is all new files
  plus two additive methods, touches no shared function, and is mergeable at any
  time. `#9b` — the scheduler pause path, `GateController` decisions, the
  `resume` subcommand and exit code 2 — is the part that must go last.
