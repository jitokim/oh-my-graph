# oh-my-graph — Architecture & MVP Design (implementation spec)

> A graph-native multi-agent orchestrator whose node runtime is your own
> logged-in `claude` CLI (subscription auth), not the Anthropic API.

## Thesis
Graph engineering (wiring specialized agents as a DAG) currently forces you onto
the Anthropic API + Agent SDK + `ANTHROPIC_API_KEY` (metered). oh-my-graph runs
each DAG node as a raw `claude -p` subprocess on your existing subscription —
$0 marginal, inside your Max/Pro plan. Whitespace confirmed: no existing
graph-native orchestrator drives the subscription `claude` CLI (all go through
Agent SDK + API key). Personal/local, bring-your-own-login (ToS-compliant, like
claude-squad); NOT a redistributed hosted product.

## Language — Go (committed)
Go 1.25+. This tool *is* a subprocess scheduler: `errgroup` + buffered-channel
semaphore for the concurrency cap; `context` cancellation propagates to
`exec.CommandContext` and kills in-flight children on halt-on-fail (awkward in
Python). Single static binary (`go install`).
Deps: stdlib `os/exec`+`context`, `golang.org/x/sync/errgroup`, `gopkg.in/yaml.v3`,
stdlib `flag` (cobra optional/later).

## Node runtime mechanics (ground truth — use exactly)
A node = one subprocess:
```
claude -p "<rendered prompt>" --output-format json --permission-mode <mode> \
  [ --max-budget-usd <amount> ] \
  [ --setting-sources "" ] [ --agent <name> ] \
  [ --allowedTools "<comma,joined>" ] \
  [ --tools "<comma,joined>" ] [ --strict-mcp-config ] \
  [ --disallowedTools "<comma,joined>" ] \
  [ --resume <session_id> ] [ --session-id <uuid> ]
```

This is emission order, not just a flag inventory: `runner.buildArgs` appends
in exactly this sequence and `claude_test.go`'s `want` argv pins it
element-by-element, so a reordering is a test failure, not a style choice.
Note where `--max-budget-usd` sits — immediately after `--permission-mode`,
*before* the ceiling flags, because it is not one of them.

The bracketed tool-ceiling flags come from one `runner.ToolPolicy` per node and
are auto mode's alone (see "Auto mode"); a hand-written graph's policy carries
only `AllowedTools`, so its argv is the first line plus `--allowedTools`, then
`--resume` or `--session-id`, and `--max-budget-usd` when the node declared
`budget_usd` (that flag is not part of the ceiling — it rides on
`NodeInvocation.BudgetUSD`, which the scheduler passes for every node, planned
or hand-written).
Every fresh-session node gets `--session-id` with a UUID the
scheduler pre-assigned (`runner.NewSessionID`), so the id is published on
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
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`** from the child env (they
  silently switch to metered API billing). Enforced in code + asserted by a unit
  test on the built argv/env. NEVER `--bare` (disables OAuth). NEVER the Agent SDK.
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
  oh-my-graph applies these ONLY to coordinator-planned nodes — see "Auto mode".
- Hand-written graphs never carry `--setting-sources`, `--tools`,
  `--strict-mcp-config` or `--disallowedTools`: they are the user's own reviewed
  artifact and are *meant* to run under the user's own settings, hooks and MCP.
  `bypassPermissions` opt-in per node only, loud warning at load, never a graph default.

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

### Fragments — `use:`/`with:`, resolved by the file loader (ADR 0013)

A fragment is a **single-node definition file** with declared substitution
points — a proven node shape (the e2e gate, the security review) written
once, upstream, instead of copy-varied across graphs:

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
sibling (`filepath.Join` cleans, so an unconstrained name would). Resolution happens on a **path-aware load
stage** — `graph.LoadFile` (fail-fast; also returns the entry file's raw
bytes and one `FragmentResolution` per resolved `use:`) and its collect-all
counterpart `graph.LintFile` (every fragment issue plus every structural
issue of the resolved graph, plus advisories on their own channel) — which
`run`, `lint` and `run --dry-run` all load through. The resolved document
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
deep merge). A fragment may not declare wiring — `id`, `depends_on`, `cwd`,
`worktree`, `feedback` (load error) — nor `use:` itself (no nesting in v1) —
nor a **YAML alias or `<<:` merge key inside the `node:` block** (load error):
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

Downstream of the loader **no fragment concept exists**: `run` and `lint` both
print one disclosure line per resolved fragment (source file + the fragment's
own description + every overridden key) plus the same fragment advisories on
the warning channel (`run` discloses what it spliced, so it discloses the
drift smell too; the three *handoff* sweeps stay lint-only), the snapshot stores the re-encoded
**resolved** graph whenever any node resolved a fragment (so resume never
re-reads a fragment; `GraphSHA256` still hashes the entry file's bytes), and
scheduler, handoff, the event stream and every consumer reading it see
exactly the graphs they see today.
Shipped shapes live in `graphs/fragments/` and ship inside the binary
alongside the templates (`//go:embed *.yaml fragments/*.yaml`), so
`oh-my-graph init` unpacks a tree whose `use:` nodes resolve;
`internal/graph/testdata/golden/` holds the resolved goldens — one per
fragment-citing template (`self-dev`, `dev-review-pr`, `backlog-batch`) — that
turn any fragment edit into a reviewed multi-template diff.

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

**Mutually exclusive with the auto-mode tool ceiling's Layer 1.**
`--setting-sources ""` also disables discovery of the user's agent definitions
(E6's neighbour, E2), so the two cannot be combined. A raw plan still rejects
`agent:` outright; the one path that puts the field on a planned node —
coordinator auto-mapping, below — pays for it by dropping Layer 1 on exactly
the mapped nodes, and the plan printout says so before anything runs. Layer 1
can still never be extended to hand-written graphs without dropping `agent:`
with it.

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
`agent:` rejected — trusted code scans `~/.claude/agents` and
`<cwd>/.claude/agents` (project shadows user) and maps planned nodes onto the
user's own agents by a deliberately conservative name-token rule: exact token
or ≥4-rune prefix between node id and agent name, exactly one candidate or
nothing (ambiguity is silence, not a guess; no fuzzy scoring, no description
matching). Scan failures are silent so zero-config stays zero-config;
`--no-agent-mapping` turns the whole thing off; every decision made is shown
in the printed plan before execution. ADR 0004 §4 originally deferred this
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
below — the limit rides the same pause/drain machinery a gate does.

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
per arc, each under its own timeout/budget/tool ceiling; the ledger prices
every execution with a `feedback round k/N` note.

retry: flat re-run up to `max` on causes in `retry.on`, fresh session (never
resume a failed one). For a `handoff: session` node this means a retried
attempt does not resume the parent session either — it starts cold, which
`lint` warns about up front and the passing attempt's ledger detail states. The causes are a closed set — `nonzero_exit`,
`run_error`, `output_error`, `budget_exceeded`, `verify_failed`,
`result_mismatch` (the `graph.Cause*` constants) — and an unknown cause is a
load-time `GraphValidationError`: it would match no failure the scheduler ever
produces and silently mean "never retry".

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
reject an empty `command`, an unparseable `timeout`, a timeout over the ceiling,
and an uncompilable `output_matches` — at load, naming the node
(`GraphValidationError`), never mid-run. Changing this struct touches loader,
validator, shipped example graphs and tests together.

### Verdict patterns — `result_matches` reads raw markdown

`result_matches` is a Go regexp matched against the model's **raw final reply**
(`outcome.Result`, the CLI's `result` field), with no normalization whatsoever:
no trimming, no markdown stripping, no case folding, no `(?m)`. `^` and `$`
therefore anchor to the start and end of the whole reply text.

Models emit markdown. A prompt that says "begin your reply with PASS" leaves
the model free to write `**PASS**`, and it does — that exact reply has failed
`^PASS` and halted a real run of a shipped graph, twice in one release, on a
node whose suite had actually passed. The graph author sees the flake as luck,
because earlier runs of the same graph passed.

So a verdict pattern is written in two halves, and both are load-bearing:

- **The prompt is the instruction.** Demand the bare token as the very first
  characters of the reply, and say that markdown emphasis is wrong — name the
  wrong shape (`` `**PASS**` is WRONG ``) rather than only describing the right
  one.
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

Widening the separator class between a token and its payload is the one
tolerance worth buying node by node, and the currency is what a false FAIL
costs there. `merge` admits `-` and `—` (`MERGED — 4f2a1c9`) because its retry
re-enters `gh pr merge` on an already-merged PR under a grant too narrow to
look at what happened, so a false FAIL is an operator's morning, not a
re-run; the SHA still carries the assertion, so nothing is given up. Every
other node pays a retry, and keeps the narrow class.

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

**This narrows the "only ClaudeCLIRunner touches `os/exec`" invariant, on
purpose.** The invariant's restated form: *exactly four objects may spawn a
process — `runner.ClaudeCLIRunner`, `verify.ShellVerifier`,
`worktree.GitManager` (see "Worktree isolation") and `browser.ExecOpener`
(ADR 0006) — each behind its own injected interface, and no other package
imports `os/exec`.* Both purposes survive: the subscription-auth scrub still
has exactly one home per spawner, and the engine is still fully testable with
zero spawns. See ADR 0002, ADR 0005 and ADR 0006.

**The env scrub applies to verification commands too.** `verify: { command:
"claude -p ..." }` is legal and would otherwise run on metered API billing if
the key happened to be set. `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` are
deleted from the verification child's env by the same shared policy the runner
uses (`internal/childenv`), asserted by its own unit test.

**Failure and retry.** A failed verification is a `*NodeCheckError` with
`Predicate: "verify"` and a detail carrying the exit code and a truncated tail of
the command's output — so the ledger says *why*, not just *that*. The retry
cause token is `verify_failed`, joining `nonzero_exit` / `result_mismatch` /
`output_error` / `run_error` / `budget_exceeded` — the full closed set of
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
   `1` = the run failed, `2` = the run is paused and resumable. A pause is not a
   failure and must not be reported as one.

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
planned graph strips any it finds and **refuses the resume**, naming the nodes
(`coordinator.ReattachVerifyCommand`, ADR 0016 §4). The discriminator is the
snapshot's tool policies, non-empty exactly for a planned graph: a hand-written
graph's `verify:` is the user's own reviewed artifact and round-trips
untouched. Today the refusal is terminal, since only `auto` will parse
`--verify-cmd`; a `resume` that re-supplies the command is what makes such a
run resumable.

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
oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--concurrency N] [--no-web]
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
- **A subscription session limit is a pause, not a failure (ADR 0009).** The
  runner classifies the CLI's limit message (`NodeOutcome.SessionLimited`,
  matcher pinned in `internal/runner/sessionlimit.go`); the scheduler then
  stops launching new work but drains in-flight siblings (which may
  themselves limit and join the paused set), records the limited node
  NOWHERE (un-run, not FAILED — no ledger row, snapshot record, or terminal
  event), and returns `*LimitPausedError` → exit code 2 with a
  best-effort-parsed "resume after <reset time> with: `resume <run-id>
  --retry-failed`" hint. A gate pause outranks a limit; a limit outranks
  continue-on-fail pruned failures. The leg closes on the stream as outcome
  `"paused"` with a distinguishing `detail`. A retry leg's worktree
  provisioning is disk-aware: a retried node re-declaring a name reuses the
  lane's surviving dir or re-attaches the branch a paused leg retained, so
  the lane continues its committed state instead of dying on the ref
  collision (see "Worktree isolation").
- A `resume.lock` (`O_EXCL`, holding the pid) guards against two concurrent
  legs of the same run id double-running nodes: the `run`/`auto` first leg
  holds it for its whole duration, and every `resume` takes the same lock — so
  a `resume --retry-failed` raced against a still-in-flight run fails on the
  lock instead of double-spawning. A stale lock is reported with the exact
  path to delete.

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
  `buildCard` therefore *re-implements* `InFlight`'s rule (`started != "" &&
  ended == ""`) inline. That is a duplicated rule, so the agreement is
  enforced rather than structural: `TestBuildCard_InFlightAgreesWithRunfeed`
  judges the inline derivation against `runfeed.InFlight` itself, which is
  what keeps a card from disagreeing with `runs list`, `watch` or the run's
  own view about the same run. Cost is the snapshot's
  per-node total, the same accounting `runs list` prints. A run directory
  this binary cannot read renders as an `unknown` card carrying the reason
  rather than being dropped: `runs list` can skip a broken run with a
  warning because a table can, but a dashboard that silently omitted one
  would be lying about what is on the machine.
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
  through `runner.ClaudeCLIRunner` behind `serve.GateResumer` (ADR 0014), so
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
- The graph structure appears when it is known: `state.json` is written only
  after each node's terminal verdict, so a fresh run's `/api/graph` honestly
  reports the structure unavailable until the first node completes (the UI
  polls); events stream from the start.
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
  id gets `/api/result`'s membership guard, with one widening: before the
  first snapshot exists (exactly when the first node is running), the run's
  own feed vouches instead. Not running / no session id (a gate, a
  session-handoff node) / no transcript yet → 204.
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
`oh-my-graph auto "<goal>" [--plan-only] [--input k=v ...]` is the zero-config
path; custom
YAML stays the precise-control path. Planning a graph is ONE
planner call through the same NodeRunner seam every node uses (ClaudeCLIRunner:
env scrub, read-only `plan` permission mode, never the Agent SDK) — the
Coordinator makes exactly that one call per `auto` run. `--plan-only` stops
the sequence immediately after the topology print, so that one call is all it
makes and no node runs — the inspection path for the mappings and the ceiling,
and deliberately NOT free the way `run --dry-run` is: there is no plan to
inspect until one has been bought, which is why the stop line prints its cost
and the paid-for spec is kept — in `$OMG_HOME/plans/<id>/graph.json`, never
under `runs/`, since a directory there holding no `state.json` is what a
broken run looks like to `runs list` and to `serve`'s newest-run resolution. It is rejected with `--max-cycles ≥ 2`, since
every cycle after the first is planned from the previous cycle's run. (Interactive `chat`
reuses the same Coordinator but adds a routing call per turn before planning;
see "Ambient chat".) The planner asks
claude to reply with a graph spec as a JSON object (name / nodes / depends_on /
prompt / allowed_tools / handoff). JSON is a YAML subset, so the reply is
loaded through the existing parser, normalization, and DAG validation — an
invalid plan fails before anything runs. Auto-specific guards, enforced in
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
of `~/.claude/agents` and `<cwd>/.claude/agents` (project shadows user), a
conservative name-token match between node id and agent name (exactly one
candidate or nothing), and a refusal to map any agent whose frontmatter tools
exceed the node's own `allowed_tools`. A mapped node runs `--agent <name>` and
drops ceiling Layer 1 (agent resolution needs the user's settings loaded; the
other layers stay), every decision is shown in the printed plan, and
`--no-agent-mapping` turns it off. The full rule and its trade live in
"Node-as-subagent"; the raw plan itself still may not carry `agent:`.

Strictly after agent mapping, the coordinator may also map the user's own
Claude Code skills onto planned nodes (`internal/coordinator/skillmap.go`,
ADR 0012) — by a different mechanism, because measurement (claude 2.1.220)
shows both model-side skill surfaces (the skills listing and the `Skill` tool)
are absent under the planned-node argv, so a prompt reference would be dead
text. Instead, trusted code scans `~/.claude/skills/*/SKILL.md` only (never a
project directory and never a plugin's — both surfaces are cut from v1, and
the plan printout names them as out of scope on every run rather than mapping
nothing in silence), matches by the same
conservative name-token rule, and **appends the skill's body to the node's
prompt** in a nonce-fenced, attributed block with `{{` neutralized until none
remains (the prompt is a handoff template; skill prose must not become
template code, and a single pass would let odd brace runs re-form tokens). No
ceiling layer is touched — an agent-mapped node (Layer 1 dropped) is refused a
skill outright, because that composite is unmeasured. Bodies over 16 KiB are
skipped, never truncated; every decision prints one line — a mapping with the
inlined size and a SHA-256 prefix, a refusal with its reason (there is no
inlined text to measure or hash) — the full text lands in the saved
`graph.json`, and
`--no-skill-mapping` turns it off. The decisions are bracketed by the scan
that produced them (`Plan.SkillScan`: the directories read, the count, and
`Shadowed` — every definition that lost a name collision, which gets its own
printed disclosure line so a shadowed skill is never silently the loser),
because a name-only rule leaves most node ids unmatched and an empty decision
list would otherwise read the same as "mapping never ran". Honest cost: a mapped node pays for the
body on every invocation, and inlining is unconditional where Claude Code's
own `description`-driven activation is conditional — whether that helps or
misfires is ADR 0012's required (a)/(b) probes, not assumed here.

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
}
```

| layer | mechanism | closes |
|---|---|---|
| 0 declaration | `plannedToolAllowlist`, plan-time rejection | a plan asking for `Bash(*)` |
| 1 **isolation** | `--setting-sources ""` | the user's standing grants; settings hooks |
| 2 grant | `--allowedTools "Read,Bash(git *)"` + `dontAsk` default-deny | **scoped Bash** |
| 3 narrowing | `--tools "<bare names declared>"` | tools the model can even attempt |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` (PR #5's list, retained) | anything the above missed |

Layer 1 is the load-bearing change. Rules from `~/.claude/settings.json` are why
`--allowedTools` could never bind: they are matched alongside ours and a standing
`Bash(*)` wins. `--setting-sources ""` loads none of user/project/local settings,
leaving our argv as the only allow-rule source; enterprise policy settings are
still loaded and still cannot be escaped. Combined with `dontAsk` — under which
an unmatched call resolves to *ask* and an unanswerable ask becomes a **deny** —
`Bash(git *)` means *git and nothing else*. **Measured, not inferred** (E1): the
identical node declaration ran an out-of-scope `touch` without Layer 1 and had
it denied with Layer 1, while in-scope `git` kept working. The gap "a node
declaring a scoped `Bash(...)` keeps the whole `Bash` tool" is closed for
planned nodes.

Layer 1 also closes the settings-hook gap: a node that writes
`.claude/settings.local.json` into the invocation directory achieves nothing,
because no node in this run (or any later `auto` run) loads local settings.

Layer 3 is a genuine *replacement* of the built-in tool set, not an addition to
it (E4): a tool omitted from `--tools` does not exist for the node, and naming
it in `--allowedTools` does not bring it back. So the two compose as an
intersection — `--tools` decides what exists, `--allowedTools` what is
permitted — and Layer 3 must list every tool the node needs.

Layers 3 and 5 are deliberate redundancy, not belt-and-braces theatre: they are
independent mechanisms, so a wrong assumption about any one layer degrades to
the previous behaviour rather than to nothing.

Remaining honest gaps, unchanged by this work: skill/slash-command surfaces are
still not enumerable — though ADR 0012 has since measured the planned-node case
(both the skills listing and the `Skill` tool are absent under this argv, which
is why skill mapping inlines content at plan time instead of referencing it); **Layer 4 is unverified** (E5 — `--strict-mcp-config`
ships because it is free, not because MCP closure was observed); and dropping
user settings also drops the user's CLAUDE.md, hooks and MCP servers for planned
nodes — a behaviour change that makes planned nodes *more isolated and less
capable* than they were, which is the intended direction but must be stated in
the README rather than discovered.

### Planned-node fields are deny-by-default
`agent:` on a planned node would let an unreviewed plan choose which of the
user's subagents — and therefore which system prompt, tool grant and model —
runs the node, routing around Layers 0–3 entirely. `success_check.verify:` would
let it run arbitrary shell outside every guard. Both are **rejected** in
`validatePlannedNodes`, alongside `bypassPermissions`, `cwd` and `type: gate`.

The general rule, because this class of hole recurs every time the schema grows:
**every field on `graph.Node` must have an explicit disposition in
`validatePlannedNodes` — allowed, constrained, or rejected.** Adding a field to
`Node` without adding a case is a review-blocking defect, not a nit. A
table-driven test over `reflect.VisibleFields(reflect.TypeOf(graph.Node{}))`
that fails on any field name the coordinator has no recorded disposition for
turns that rule into a build failure. Current dispositions:

| field | planned-node disposition |
|---|---|
| `id`, `prompt`, `depends_on`, `handoff` | allowed (prompt must be non-empty) |
| `type` | constrained — `claude-run` only; `gate` rejected |
| `allowed_tools` | constrained — non-empty, `plannedToolAllowlist` only |
| `permission_mode` | constrained — `bypassPermissions` rejected |
| `cwd` | rejected |
| `agent` | **rejected** |
| `worktree` | **rejected** (the engine would run `git worktree add` on an unreviewed plan's say-so — see "Worktree isolation") |
| `success_check.verify` | **rejected** (`exit_zero`/`result_matches` allowed) |
| `use` | **rejected** — a planner-emitted `use:` would let unreviewed output pick which local file's prompt text, tool grant and verify command get spliced in, and a fragment file in the run's repo is attacker-influencable whenever the repo is untrusted (ADR 0013: trusted code resolves files, the planner never names local resources). Refused at the coordinator's `graph.Parse` boundary |
| `with` | **rejected** — `use`'s substitution bindings, on the same grounds: dead without a `use:`, and a `with:` on a planned node means the plan tried to reference a fragment at all |
| `budget_usd`, `timeout`, `retry` | allowed |
| `feedback` | constrained — `retry`'s standing one level up: bounded re-runs of body nodes already inside every ceiling, granting no tool, no path, no shell; the load validations hold for a planned graph exactly as for a hand-written one, but load validation only requires `max` ≥ 1 and a plan has no human reviewer for the upper bound, so a planned `max` above `maxPlannedFeedbackRounds` (3) is rejected (ADR 0010) |

Both mechanisms apply ONLY to coordinator-planned graphs; hand-written YAML
(`oh-my-graph run`) is human-authored/reviewed, passes a nil deny list, and is
not restricted by either. The generated spec is
saved to `~/.oh-my-graph/runs/<run-id>/graph.json` — being valid YAML it can be
hand-edited and re-run with `oh-my-graph run` — then executed by the same
Scheduler as any other graph. A `--plan-only` plan is saved the same way but
to `~/.oh-my-graph/plans/<id>/graph.json`, because it has no run to belong to.

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
  change).
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
  are real (the skill-inlining fence's mechanism, `internal/coordinator/fence.go`).
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
- **Node** — value object (id, type, prompt, cwd, tools, permission, budget, timeout, success_check, handoff, depends_on).
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
  - `ClaudeCLIRunner` (prod): builds argv, SCRUBS ANTHROPIC_API_KEY/AUTH_TOKEN,
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
concurrent ready-set scheduler + cap + halt-on-fail; ClaudeCLIRunner (exact argv,
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

## v1.1 scope
IN: evidence-grounded `success_check.verify` (#7); `gate` execution +
`oh-my-graph resume` (#9); the layered tool ceiling for planned nodes and the
planned-node field dispositions (#11); node-level `agent:` for hand-written
graphs (PR #6). Each ships as its own PR — see "Implementation sequencing".

## Repo layout
```
cmd/oh-my-graph/{main,flags,init,resume,gateresume,runs,show,watch,serve,chat,goal,lint,dryrun,liveview,version}.go + _test  CLI: parse flags, load, inject ClaudeCLIRunner+ShellVerifier, init/run/auto/resume/runs/show/watch/serve/chat, the `auto --max-cycles` goal loop (goal.go — ADR 0011) and the GateResumer serve's gate routes call back through (gateresume.go — ADR 0014), print ledger
internal/graph/{graph,validate,feedback,fragment}.go + _test + testdata/{pre-migration,golden}/  Graph/Node value objects, YAML, DAG validation, ReadyGiven, feedback edges, and the load-time fragment resolver (LoadFile/LintFile — ADR 0013)
internal/schedule/{scheduler,errors,feedback}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors + the bounded runtime re-run of a feedback edge (ADR 0010)
internal/runner/{runner,claude,session,sessionlimit,fake}.go + build-tagged procgroup_{unix,windows}.go + _test  interface + ToolPolicy + ClaudeCLIRunner(ENV SCRUB) + pre-assigned session ids (session.go) + the subscription session-limit recognizer (sessionlimit.go — ADR 0009) + FakeRunner
internal/verify/{verify,shell,fake}.go + build-tagged {shell,procgroup}_{unix,windows}.go + _test  Verifier seam — ShellVerifier is the second of the four exec seams (ADR 0002)
internal/worktree/{worktree,git,fake}.go + _test  worktree Provider seam — GitManager is the third exec seam (ADR 0005): per-run managed checkouts + work-preserving cleanup
internal/browser/{browser,exec,fake}.go + build-tagged argv_{darwin,unix,windows}.go + _test  browser Opener seam — ExecOpener is the fourth exec seam (ADR 0006): default-browser launch, wired behind run/auto's TTY gate
internal/invariants/exec_seam_test.go          test-only: asserts only the four exec seams' files import os/exec — 8 files, since a seam's platform-specific procgroup files belong to it (a ninth importer fails CI — ADR 0002/0005/0006). A separate, shorter list names the 4 spawn CALL SITES (one per seam, procgroup files excluded — they mutate an already-built *exec.Cmd) and asserts each scrubs its child env through internal/childenv
internal/childenv/childenv.go + _test          the shared "delete billing-switching vars" child-env policy (all four spawners)
internal/coordinator/{coordinator,router,agentmap,skillmap,fence,goal,assess}.go + _test  auto mode: goal → planner call (NodeRunner seam) → validated graph + ToolPolicies; chat routing; post-validation subagent mapping (agentmap.go) and skill inlining (skillmap.go — ADR 0012) over the shared nonce fence (fence.go, also used by Assess); the bounded plan→execute→assess goal loop (goal.go/assess.go — ADR 0011)
internal/handoff/{handoff,placeholder_lint,session_lint,verdict_lint}.go + _test  interpolation, artifact persist/resolve, session pick, Seed for resume — plus the advisory lint sweeps `lint`/`run` print (unresolvable {{placeholders}}, session-handoff `--resume` that may not deliver the parent conversation, a prompt demanding a verdict token no `result_matches` reads, a `result_matches` that silently dropped the node's exit-code guard)
internal/gate/gate.go + _test                  Decision + PauseController/RecordedController
internal/runstate/{runstate,recorder,lock}.go + _test  state.json snapshot — atomic write, schema version, run lock, resume load
internal/runfeed/{runfeed,reader}.go + _test   events.jsonl append-only lifecycle event stream — the consumer contract (docs/RUN-FEED.md) — plus the in-repo consumer readers (InFlight, Follow)
internal/serve/{serve,dashboard,card,resolve,transcript,gate}.go + ui/ + _test  `serve`: 127.0.0.1-only web views — the dashboard (`dashboard.go`/`card.go`: one live mini-DAG card per run, run views mounted at /run/<id>/) and the live view of one run — embedded static UI (go:embed) + vendored cytoscape.js; a run-feed consumer with token-guarded gate actions — every route reads the contract (plus the live transcript tail of a running node's own session) except the mutating pair (`gate.go`: approve/reject the paused gate through the injected GateResumer — ADR 0014)
internal/ledger/ledger.go + _test              RunLedger summary + total cost
graphs/haiku-smoke.yaml, graphs/dev-review-pr.yaml, graphs/self-dev.yaml, … + graphs/embed.go  the shipped pipelines, embedded with `//go:embed *.yaml fragments/*.yaml` (globs, so a new template or fragment ships automatically; the second pattern is required because `*.yaml` does not descend, and a template citing `use:` needs its fragments/ sibling on disk) — `oh-my-graph init [dir]` walks that payload and unpacks it into <dir>/graphs/, nested paths included (dir defaults to `.`), never overwriting: one existing target aborts the whole command, writing nothing, and a failure partway through removes the files AND subdirectories it created
graphs/fragments/{e2e-verify,review-security,review-style}.yaml  the shipped node shapes the templates cite with use: (ADR 0013); cited by self-dev.yaml, dev-review-pr.yaml and backlog-batch.yaml (+ internal/graph/shipped_graphs_test.go asserts every shipped graph loads BOTH from the checkout and from the binary's own unpacked payload — the second is what proves `init` emits graphs that load)
docs/adr/00{01..15}-*.md
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
ANTHROPIC_API_KEY/AUTH_TOKEN from every child (unit-tested). Never --bare, never
Agent SDK. Least privilege per node (allowed_tools + permission_mode); bypassPermissions
opt-in per node with a loud warning, never a default. For auto-planned graphs
(untrusted LLM output run unattended under `dontAsk`), least privilege is not
just a prompt convention and not just a declaration check:
`coordinator.validatePlannedNodes` rejects a planned node whose `allowed_tools`
is empty or names a tool outside the fixed allowlist, or that sets `cwd`,
`agent`, or `success_check.verify`; and `Plan.ToolPolicies` imposes a per-node
execution ceiling (settings-source isolation + scoped allow under default-deny +
tool narrowing + strict MCP + residual denies) so the user's own standing tool
grants cannot widen an unreviewed plan. All of it, and the gaps that remain, are
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
- **E2 — ANSWERED: yes, mutually exclusive.** `--setting-sources ""` also
  disables discovery of the user's agent definitions. `--agent code-reviewer`
  under isolation fails at startup with *"not found. Available agents: claude,
  Explore, general-purpose, Plan, statusline-setup"* — only built-ins survive.
  Layer 1 and `agent:` therefore cannot be combined. This is a hard constraint
  on ever extending Layer 1 to hand-written graphs, and it is why a
  coordinator-MAPPED node (see "Node-as-subagent") drops Layer 1 on exactly
  that node — the trade the plan printout discloses — rather than combining
  the two and failing at startup.
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
