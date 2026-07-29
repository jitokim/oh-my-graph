# oh-my-graph — Architecture & MVP Design (implementation spec)

> A graph-native multi-agent orchestrator whose node runtime is your own
> logged-in `claude` CLI (subscription auth), not the Anthropic API.
> **It executes; [fleetops] observes the same `~/.claude/projects` transcripts.**

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
Python). Single static binary (`go install`), pairs 1:1 with fleetops idioms.
Deps: stdlib `os/exec`+`context`, `golang.org/x/sync/errgroup`, `gopkg.in/yaml.v3`,
stdlib `flag` (cobra optional/later).

## Node runtime mechanics (ground truth — use exactly)
A node = one subprocess:
```
claude -p "<rendered prompt>" --output-format json \
  --permission-mode <mode> --allowedTools "<comma,joined>" \
  [ --disallowedTools "<comma,joined>" ] [ --resume <session_id> ]
```
run with `cwd` = node.cwd. JSON envelope → `session_id`, `result`, `total_cost_usd`.
- **Subscription auth crux:** start from `os.Environ()` and **DELETE
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`** from the child env (they
  silently switch to metered API billing). Enforced in code + asserted by a unit
  test on the built argv/env. NEVER `--bare` (disables OAuth). NEVER the Agent SDK.
- Per-node `context.WithTimeout` (default ~20m) so a wedged child can't hang the graph.
- Non-JSON/parse failure = node failure (`NodeOutputError`), never a silent zero result.
- permission modes: `dontAsk` (default unattended) / `acceptEdits` / `plan` (read-only).
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
- id: e2e
  type: claude-run            # claude-run | gate (v1.1 — see "Gate nodes and resume")
  depends_on: [dev]           # fan-in: all must succeed first
  prompt: |                   # may interpolate {{ inputs.<name> }} and {{ artifacts.<id> }}
    Run make local PORT=8080 and report PASS or FAIL.
  cwd: "{{ inputs.repo }}"
  allowed_tools: [Read, "Bash(make *)", "Bash(git *)"]
  permission_mode: dontAsk
  agent: code-reviewer        # optional (v1.1): run as this Claude Code subagent — see "Node-as-subagent"
  budget_usd: 0.50            # post-hoc cap: node FAILS if its actual cost exceeds this (see Execution engine)
  handoff: artifact           # artifact(default) | session
  success_check:              # see "Success checks" — verify is the only evidence-grounded predicate
    exit_zero: true
    result_matches: "PASS"    # self-report; never sufficient on its own
    verify: { command: "make local PORT=8080", timeout: 5m }   # optional (v1.1)
  retry: { max: 1, on: [nonzero_exit] }   # optional
```
Graph file has `name`, `version`, `inputs: [..]`, `concurrency: N`, `nodes: [..]`.
Full worked example (dev→e2e→parallel reviews→pr) ships as `graphs/dev-review-pr.yaml`.

## Handoff — artifact default, session opt-in (committed)
- **artifact (default):** engine persists each node's `.result` to
  `.oh-my-graph/runs/<run-id>/<node-id>.out`; dependents read via
  `{{ artifacts.<id> }}` (substitute file path by default; `| inline` filter to
  inline content). Robust, inspectable, parallel-safe, one clean session per node.
  Use for fan-in / reviews (many→one conclusions).
- **session (`handoff: session`):** dependent runs `--resume <session_id>` of its
  single session-parent (same cwd/git scope). Use for tight sequential
  continuation (dev→e2e). Validation: a node may resume AT MOST ONE session parent
  (can't merge two sessions); multi-parent fan-in MUST use artifact.

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
   `--continue-on-fail` (opt-in) prunes only the failed subtree.
5. Done when ready+running are empty.

retry: flat re-run up to `max` on causes in `retry.on`, fresh session (never resume a failed one).

budget_usd (post-hoc): a node that passes its success_check is then judged
against its declared `budget_usd`. Actual cost strictly greater than the budget
→ `NodeBudgetError` (node id + budgeted + actual), which flows through the exact
same path as a failed success_check: ledger row FAIL with the overspend in
`Detail`, retry only if opted in, halt-on-fail by default. A non-positive
`budget_usd` means "no budget declared" and is never enforced. The budget is
judged *after* `Handoff.PersistOutput`, so a node that did useful work before
blowing its budget still leaves its artifact on disk — the budget changes the
verdict, never handoff semantics. Its retry cause token is `budget_exceeded`,
deliberately distinct from `nonzero_exit` so a pre-existing retry policy can
never re-spend an already-blown budget by accident.

This enforcement is **post-hoc only, and that is a hard limit of the runner
contract, not an oversight**: `claude --output-format json` reports
`total_cost_usd` once, in the envelope it prints as it exits, so the engine
first learns a node's cost after the money is spent. What the verdict buys is
everything downstream — dependents never start and, by default, the run halts.
A true mid-node cost kill would require the runner to stream incremental cost
(`--output-format stream-json` + parsing cost events + cancelling the node
context mid-run), which changes the `NodeRunner` output contract from "one
envelope" to "a stream" and belongs in its own ADR. Deriving a wall-clock
timeout from `budget_usd` via an assumed $/minute rate was considered and
rejected: the conversion rate would be fabricated, so it would look like
enforcement while enforcing nothing. The only real mid-node bound today remains
the per-node `context.WithTimeout` (~20m default), which is wall-clock, not cost.

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
  result_matches: "PASS"                 # optional, secondary
  verify:
    command: "go test ./... -run TestFoo" # required; run via `sh -c`
    cwd: "{{ inputs.repo }}"              # optional; default = the node's own cwd
    timeout: 2m                           # optional; Go duration, default 2m, ceiling 10m
    expect_exit: 0                        # optional; default 0
    output_matches: "^ok\\s+github"       # optional; regex over combined stdout+stderr
```

```go
type SuccessCheck struct {
	ExitZero      bool          `yaml:"exit_zero"`
	ResultMatches string        `yaml:"result_matches"`
	Verify        *Verification `yaml:"verify"` // nil = no evidence check
}

// Verification is a pointer field so "absent" and "zero" are distinguishable,
// and ExpectExit is a *int so an explicit 0 is expressible.
type Verification struct {
	Command       string `yaml:"command"`
	Cwd           string `yaml:"cwd"`
	Timeout       string `yaml:"timeout"` // parsed with time.ParseDuration at LOAD time
	ExpectExit    *int   `yaml:"expect_exit"`
	OutputMatches string `yaml:"output_matches"`
}
```

`SuccessCheck.IsZero()` must also test `Verify == nil`, and `Validate` must
reject an empty `command`, an unparseable `timeout`, a timeout over the ceiling,
and an uncompilable `output_matches` — at load, naming the node
(`GraphValidationError`), never mid-run. Changing this struct touches loader,
validator, shipped example graphs and tests together.

**Where it runs in the node lifecycle** (`schedule.runNode`):
`Handoff.ResolveInputs → NodeRunner.Run → exit_zero → result_matches → verify →
Handoff.PersistOutput → RunLedger.Record → enqueue dependents`. Ordering is
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
	Output   string // combined stdout+stderr, truncated for the ledger
}

type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}
```

- `ShellVerifier` (prod) is the only object in `internal/verify` that imports
  `os/exec`. Injected by `cmd/oh-my-graph`, never constructed by the scheduler.
- `RefusingVerifier` is the `Options.Verifier` default, mirroring the gate stub:
  a scheduler test that forgets to inject one gets a loud failure instead of a
  real spawn. `FakeVerifier` (scripted, keyed by command) is what tests inject,
  so the whole verify path stays spawn-free in CI.
- Selection is data-driven and the caller stays ignorant: the scheduler asks the
  injected `Verifier` and never learns which kind ran. A second verification kind
  (`file_exists:`, `git_clean:`) arrives as another `Verifier` behind a composite
  that dispatches on the declared kind — no scheduler change. v1.1 ships exactly
  one kind: minimal implementation, sufficient interface.

**This narrows the "only ClaudeCLIRunner touches `os/exec`" invariant, on
purpose.** The invariant's restated form: *exactly two objects may spawn a
process — `runner.ClaudeCLIRunner` and `verify.ShellVerifier` — each behind its
own injected interface, and no other package imports `os/exec`.* Both purposes
survive: the subscription-auth scrub still has exactly one home per spawner, and
the engine is still fully testable with zero spawns. See ADR 0002.

**The env scrub applies to verification commands too.** `verify: { command:
"claude -p ..." }` is legal and would otherwise run on metered API billing if
the key happened to be set. `ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` are
deleted from the verification child's env by the same shared policy the runner
uses (`internal/childenv`), asserted by its own unit test.

**Failure and retry.** A failed verification is a `*NodeCheckError` with
`Predicate: "verify"` and a detail carrying the exit code and a truncated tail of
the command's output — so the ledger says *why*, not just *that*. The retry
cause token is `verify_failed`, joining `nonzero_exit` / `result_mismatch` /
`output_error` / `run_error`, so `retry: { max: 1, on: [verify_failed] }` works.
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
3. Write the run snapshot atomically, record the gate as `PAUSED` in the ledger,
   print the exact `resume` command, and return a `*schedule.PausedError`.
4. `cmd/oh-my-graph` maps that to **exit code 2**. `0` = every node passed,
   `1` = the run failed, `2` = the run is paused and resumable. A pause is not a
   failure and must not be reported as one.

`--continue-on-fail` does not apply to gates: a pause always stops the whole
run, because approving "part of" a paused run later is not a coherent operation.

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

**What the snapshot must hold** — `.oh-my-graph/runs/<run-id>/state.json`,
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

**What the snapshot deliberately does NOT hold:** in-degree counts and the
ready set. Both are *derived* from `graph × completed`, so persisting them would
create a second source of truth that can go stale. `resume` recomputes them:
`Graph.ReadyGiven(done map[string]bool) []string` answers the topology question
(and `Roots()` becomes `ReadyGiven(nil)`), while the scheduler seeds each node's
in-degree as `len(DependsOn) − (parents already completed)`.

Snapshot writes happen after **every** node, not just at gates, so a Ctrl-C'd or
crashed run is resumable too. A snapshot write failure mid-run is non-fatal (the
ledger is the run's authority) and warned on the progress feed; a snapshot write
failure **at a gate pause is fatal**, because a pause whose state was not
persisted is an unrecoverable stop, and reporting it as a clean pause would lie.

**CLI contract:**
```
oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id>) [--concurrency N]
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
  semantic.
- Multiple gates ⇒ multiple resumes: a resumed run advances to the next gate and
  pauses again. The decision map makes batch approval a later, additive change.
- A `resume.lock` (`O_EXCL`, holding the pid) guards against two concurrent
  resumes of the same run id double-running nodes. A stale lock is reported with
  the exact path to delete.

**Auto-planned graphs still may not contain gates.** `validatePlannedNodes`
already rejects `type: gate` and continues to: an unattended run whose planner
decides where a human should be interrupted is not a feature, and it collides
with the deny-by-default field policy below.

## Auto mode — planned graphs, no hand-written YAML
`oh-my-graph auto "<goal>" [--input k=v ...]` is the zero-config path; custom
YAML stays the precise-control path. A **Coordinator** makes exactly ONE
planner call through the same NodeRunner seam every node uses (ClaudeCLIRunner:
env scrub, read-only `plan` permission mode, never the Agent SDK), asking
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
`Bash(git *)` finally means *git and nothing else*, and the honest gap "a node
declaring a scoped `Bash(...)` keeps the whole `Bash` tool" is closed.

Layer 1 also closes the settings-hook gap: a node that writes
`.claude/settings.local.json` into the invocation directory achieves nothing,
because no node in this run (or any later `auto` run) loads local settings.

Layers 3 and 5 are deliberate redundancy, not belt-and-braces theatre: they are
independent mechanisms, so a wrong assumption about any one layer degrades to
today's behaviour rather than to nothing. Keeping `--disallowedTools` is why
this change is safe to ship before every question in "Empirical verification
required" is answered.

Remaining honest gaps: skill/slash-command surfaces are still not enumerable;
and dropping user settings also drops the user's CLAUDE.md, hooks and MCP
servers for planned nodes — a behaviour change that makes planned nodes *more
isolated and less capable* than they are today, which is the intended direction
but must be stated in the README rather than discovered.

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
| `success_check.verify` | **rejected** (`exit_zero`/`result_matches` allowed) |
| `budget_usd`, `retry` | allowed |

Both mechanisms apply ONLY to coordinator-planned graphs; hand-written YAML
(`oh-my-graph run`) is human-authored/reviewed, passes a nil deny list, and is
not restricted by either. The generated spec is
saved to `.oh-my-graph/runs/<run-id>/graph.json` — being valid YAML it can be
hand-edited and re-run with `oh-my-graph run` — then executed by the same
Scheduler as any other graph.

## Object design (SRP; responsibilities → collaborations)
- **Graph** — validated nodes + adjacency; "is DAG?", "roots?", "dependents of X?". Pure data.
- **Node** — value object (id, type, prompt, cwd, tools, permission, budget, success_check, handoff, depends_on).
- Edge = implicit `Node.DependsOn []string` (no struct).
- **Scheduler** — drive DAG: ready/running sets, cap, context cancel, halt/continue;
  calls NodeRunner.Run, consults Graph, writes RunLedger, asks Handoff to resolve/persist.
- **NodeRunner (interface)** — THE claude-exec seam. Injected (constructor), not global.
  ```go
  type NodeRunner interface {
      Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
  }
  type NodeInvocation struct { Prompt, Cwd, PermissionMode, ResumeSession, Agent string; Policy ToolPolicy }
  type NodeOutcome struct { SessionID, Result string; TotalCostUSD float64; ExitCode int }
  ```
  - `ClaudeCLIRunner` (prod): builds argv, SCRUBS ANTHROPIC_API_KEY/AUTH_TOKEN,
    execs under context, parses JSON. The ONLY object touching os/exec.
  - `FakeRunner` (tests): scripted `map[key]NodeOutcome` keyed by the
    invocation (`NodeInvocation` has no id field; the key defaults to
    `spec.Prompt`, and tests set each node's prompt equal to its id so the
    "keyed by node id" shorthand reads true) — entire scheduler (topo,
    fan-out, fan-in, retry, halt, cost sum) unit-testable with ZERO real
    spawns. This is the testability keystone.
- **Verifier (interface, v1.1)** — THE evidence seam, separate from NodeRunner
  because a verification command is not a claude invocation. Injected the same
  way. `ShellVerifier` (prod, the only `os/exec` importer in its package),
  `RefusingVerifier` (default — a forgotten injection fails loudly),
  `FakeVerifier` (tests). See "Success checks".
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
- **RunLedger** — record session_id/cost/verdict/timing; end-of-run table + total cost.

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
persistence ON (fleetops-observable — do NOT pass --no-session-persistence).

DEFERRED (say so in README): retries beyond flat max:1; parallel-group sugar /
any DSL; TUI/dashboard (fleetops's job); worktree auto-creation; coordinator
auto-mapping of `agent:` by role (see "Node-as-subagent" — deferred on a design
constraint, not on effort); mid-node budget kill (post-hoc budget halt IS
enforced — see "Execution engine").

## v1.1 scope
IN: evidence-grounded `success_check.verify` (#7); `gate` execution +
`oh-my-graph resume` (#9); the layered tool ceiling for planned nodes and the
planned-node field dispositions (#11); node-level `agent:` for hand-written
graphs (PR #6). Each ships as its own PR — see "Implementation sequencing".

## Repo layout
```
cmd/oh-my-graph/{main,flags}.go      CLI: parse flags, load, inject ClaudeCLIRunner+ShellVerifier, run, print ledger
internal/graph/{graph,validate}.go + _test   Graph/Node value objects, YAML, DAG validation, ReadyGiven
internal/schedule/{scheduler,errors}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors
internal/runner/{runner,claude,fake}.go + claude_test, envelope_test  interface + ToolPolicy + ClaudeCLIRunner(ENV SCRUB) + FakeRunner
internal/verify/{verify,shell,fake}.go + _test v1.1: Verifier seam — ShellVerifier is this package's only os/exec importer
internal/childenv/childenv.go + _test          the shared "delete billing-switching vars" child-env policy (runner + verify)
internal/coordinator/coordinator.go + _test    auto mode: goal → planner call (NodeRunner seam) → validated graph + ToolPolicies
internal/handoff/handoff.go + _test            interpolation, artifact persist/resolve, session pick, Seed for resume
internal/gate/gate.go + _test                  v1.1: Decision + PauseController/RecordedController
internal/runstate/runstate.go + _test          v1.1: state.json snapshot — atomic write, schema version, resume load
internal/ledger/ledger.go + _test              RunLedger summary + total cost
graphs/haiku-smoke.yaml, graphs/dev-review-pr.yaml (+ internal/graph/shipped_graphs_test.go asserts they parse)
docs/adr/000{1..4}-*.md
README.md, SECURITY.md, LICENSE(MIT), go.mod, Makefile(build/test/lint)
```

## Verify MVP cheaply (real claude, cents)
`graphs/haiku-smoke.yaml`: node `write` ("write a 3-line haiku about graphs to
haiku.txt", acceptEdits) → node `critique` (depends_on write, reads
{{artifacts.write}}, plan). `mkdir -p /tmp/omg-smoke && oh-my-graph run graphs/haiku-smoke.yaml`.
Proves: subscription auth (succeeds with API key unset/scrubbed), sequential edge +
artifact handoff, JSON capture (2 session_ids + costs), RunLedger, and fleetops sees
both sessions (free integration). CI stays free — real-claude smoke is a manual
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
2. budget: **post-hoc halt is implemented and enforced** — a node whose actual
   cost exceeds its `budget_usd` fails exactly like a failed success_check
   (`NodeBudgetError`, ledger FAIL carrying budgeted-vs-actual, halt-on-fail by
   default), so its dependents never start. The RunLedger also carries the
   declared budget alongside actual cost, making the delta derivable per node
   (`Record.BudgetDeltaUSD`). **Mid-node kill remains deferred and is NOT
   enforced**: the runner learns `total_cost_usd` only from the JSON envelope
   printed at exit, so nothing can observe a runaway node's spend while it runs.
   Closing that needs a runner-level capability (streaming cost via
   `--output-format stream-json`, then cancelling the node context mid-run) —
   see "Execution engine" for why a budget-derived wall-clock timeout was
   rejected as fake enforcement.
3. parallel nodes sharing one cwd can race edits → v0.1 parallel nodes should be
   read-only (plan) reviews (the captain's fan-out case); parallel edits want
   worktrees (deferred).
4. session-handoff + multi-parent fan-in conflict → validation rejects `handoff:
   session` on a node with 2+ session-parents; multi-parent must use artifact.

Seam patterns to mirror: fleetops `internal/control/spawncmd.go` (package-var fn seam),
`internal/control/control.go` (`runBounded`/`exec.CommandContext` bounded exec).

## Empirical verification required before implementing the tool ceiling
`--help` prose is not ground truth. PR #5 already found the CLI's real deny/allow
precedence did not match what `--help` implied, so the layered ceiling above
separates what has been *read out of the shipped CLI* from what still needs a
real invocation. Verified against the claude 2.1.220 binary's own code (free, no
API call):

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

Still unmeasured — each needs one real `claude` invocation (a `make smoke`-style
manual step, never CI), and the implementer must run them **before** relying on
the behaviour, not after:

- **E1 (blocks Layer 1+2).** With a settings.json granting `Bash(*)`:
  `claude -p '<prompt>' --permission-mode dontAsk --setting-sources "" --allowedTools "Bash(git *)"`
  must allow `git status` and **deny** an out-of-scope command. Expected from V1+V2;
  confirm before deleting a word of the honest-gap disclosure in README/SECURITY.
- **E2 (blocks combining #11 with `agent:`).** Does `--setting-sources ""` also
  disable discovery of `~/.claude/agents`? If yes, Layer 1 and `agent:` are
  mutually exclusive and DESIGN must say so. (Planned nodes reject `agent:`
  anyway, so this only bites if Layer 1 is ever extended to hand-written graphs.)
- **E3 (load-bearing invariant).** `--setting-sources ""` must NOT affect
  subscription OAuth. Credentials do not live in settings files, so this should
  hold — but it is the project's #1 guarantee and gets its own smoke assertion.
- **E4.** `--tools` exhaustiveness: does omitting `Read` remove `Read`? Does
  `--tools ""` disable everything? Does it compose with `--allowedTools`, or
  replace it? Layer 3 is written as additive; if `--tools` turns out to be
  exclusive-of-allow, Layer 3 ships disabled and Layers 1+2 carry the ceiling.
- **E5.** Do project `.mcp.json` servers survive `--setting-sources ""`? If yes
  (likely — it is not a settings file), Layer 4's `--strict-mcp-config` is
  required, not optional.
- **E6.** `--agent` precedence: does a subagent's frontmatter `tools:` union
  with, override, or lose to `--allowedTools`/`--tools`? Until measured,
  DESIGN.md states no reconciliation rule — that is the honest position, and it
  is why coordinator auto-mapping of `agent:` is deferred rather than designed.

Until E1 lands, README/SECURITY.md keep their current wording about the Bash
gap. Do not narrow a published security claim ahead of the measurement.

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
