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
claude -p "<rendered prompt>" --output-format json --permission-mode <mode> \
  [ --setting-sources "" ] [ --agent <name> ] \
  --allowedTools "<comma,joined>" \
  [ --tools "<comma,joined>" ] [ --strict-mcp-config ] \
  [ --disallowedTools "<comma,joined>" ] [ --resume <session_id> ] \
  [ --session-id <uuid> ] [ --max-budget-usd <amount> ]
```
The bracketed tool-ceiling flags come from one `runner.ToolPolicy` per node and
are auto mode's alone (see "Auto mode"); a hand-written graph's policy carries
only `AllowedTools`, so its argv is the first two lines plus `--resume` or
`--session-id`. Every fresh-session node gets `--session-id` with a UUID the
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
    result_matches: "PASS"    # self-report; never sufficient on its own
    verify: { command: "make local PORT=8080", timeout: 5m }   # optional (v1.1)
  retry: { max: 1, on: [nonzero_exit] }   # optional
```
Graph file has `name`, `version`, `inputs: [..]`, `concurrency: N`, `nodes: [..]`.
Full worked example (dev→e2e→parallel reviews→pr) ships as `graphs/dev-review-pr.yaml`.

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

## Node-as-subagent (`agent:`, v1.1 — hand-written graphs only)
A node may set `agent: <name>` to run as one of the user's OWN Claude Code
subagents rather than as plain `claude -p`: the review node runs as *your*
`code-reviewer`, with its system prompt, its tools and its model. The mechanism
is one flag — `NodeInvocation.Agent` becomes `--agent <name>`, which `claude`
resolves against the existing `~/.claude/agents` and `<cwd>/.claude/agents`.
There is no oh-my-graph-side agent registry, and there must never be one: the
user's definitions are the single source of truth.

**Load-time validation rejects only a blank/whitespace-only name.** Whether a
name resolves depends on the machine and the checkout, not on the graph file, so
a graph valid on one machine would otherwise be invalid on another.

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
(E6's neighbour, E2), so the two cannot be combined. Planned nodes reject
`agent:` outright, so nothing collides today — but Layer 1 can never be extended
to hand-written graphs without dropping `agent:` with it.

**oh-my-graph makes NO claim about tool reconciliation, and has no measurement
to lean on here.** It does not parse the subagent's frontmatter, and it does not
reconcile that subagent's own `tools:` with the node's `allowed_tools`. E6 found
that a subagent's tools do not widen past `--tools` — but `--tools` is emitted
only by auto mode, which rejects `agent:`, so that result says nothing about the
hand-written path where `agent:` is legal. For a hand-written graph this is a
usability question, and both files are the user's own artifacts. For a planned
graph it would be a safety question, and the answer there is rejection.

**Coordinator auto-mapping is deferred on a design constraint, not on effort.**
See ADR 0004 §4: an implicit scan of `~/.claude/agents` would make an `auto`
run's behaviour depend on files the user forgot they had, and a planned node may
not carry the field at all.

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
  as a one-line note. A retained branch also means a `resume`d leg
  re-declaring the name fails loudly on the ref collision instead of
  resetting retained work.
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
   `--continue-on-fail` (opt-in) prunes only the failed subtree.
5. Done when ready+running are empty.

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
  result_matches: "PASS"                 # optional, secondary
  verify:
    command: "go test ./... -run TestFoo" # required; run via the platform shell (`sh -c` on unix, `cmd /c` on Windows)
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

- `ShellVerifier` (prod) is the second of the program's exactly four
  process-spawning seams (ADR 0002; the third is `worktree.GitManager`, ADR
  0005; the fourth is `browser.ExecOpener`, ADR 0006) and the only object in
  `internal/verify` that spawns anything. Injected by `cmd/oh-my-graph`, never constructed by
  the scheduler.
- `RefusingVerifier` is the `Options.Verifier` default:
  a scheduler test that forgets to inject one gets a loud failure instead of a
  real spawn. `FakeVerifier` (scripted, keyed by command) is what tests inject,
  so the whole verify path stays spawn-free in CI.
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

**What the snapshot deliberately does NOT hold:** in-degree counts and the
ready set. Both are *derived* from `graph × completed`, so persisting them would
create a second source of truth that can go stale. `resume` recomputes them:
`Graph.ReadyGiven(done map[string]bool) []string` answers the topology question
(and `Roots()` becomes `ReadyGiven(nil)`), while the scheduler seeds each node's
in-degree as `len(DependsOn) − (parents already completed)`.

Snapshot writes happen after **every** node, not just at gates, so the file on
disk always reflects everything finished so far, including a Ctrl-C'd or
crashed run's progress. Resuming that file is future work, though: `resume`
(v1.1) only continues a run whose snapshot actually recorded a gate pause
(`Gate.PausedAt != ""`) and refuses anything else — a Ctrl-C'd or crashed run
included — with "run is not paused" (see `executeResume`'s guard). A snapshot
write failure mid-run is non-fatal, but its cost is not merely a gap in the
printed ledger: the dropped write means that node is absent from the persisted
state, so a later resume would not know it ran and would re-execute it — a
real cost, not a cosmetic one — which is why it is warned on the progress feed
rather than silently swallowed; a snapshot write failure **at a gate pause is
fatal**, because a pause whose state was not persisted is an unrecoverable
stop, and reporting it as a clean pause would lie.

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

## Web live view — `oh-my-graph serve`
`serve [<run-id>] [--port N]` is a read-only web live view of ONE run: a
chronological run feed — what each node produced, why something failed — as
the main surface, with the DAG as a compact collapsible side map (GitHub
Actions' log-first layout, not Airflow's graph-first one: for this tool's
runs the substance is in the node output, not the topology). It changes
nothing about the visibility
stance: oh-my-graph executes and does not render *for the fleet* — serve is
just another **consumer of the run-feed contract** (docs/RUN-FEED.md), living
in-repo, reading `state.json` for structure and tailing `events.jsonl` for
progress through the same readers `runs list` and `watch` use
(`runfeed.InFlight`, `runfeed.Follow` — serve via its `FollowWait`
wait-for-create variant, since a viewer may connect before the stream
exists). A stream schema newer than the
binary takes `watch`'s posture, not `runs list`'s: one non-terminal
warning frame, then keep forwarding (a list can skip one run; a live view
going blank would make a routine schema bump fatal, which RUN-FEED.md's
compatibility rule forbids). fleetops's fleet-wide role is unchanged;
serve is one run, live, locally.

- **Run resolution:** an explicit id wins; otherwise the newest in-flight run
  (the leg-walking `runs list` uses for RUNNING); otherwise the newest run
  directory (`serve.ResolveRun`).
- **127.0.0.1 only** (`serve.Listen`, default port 8642): run directories
  hold prompts, artifacts and session ids, so the server must never be
  reachable off-host. The loopback bind IS the access control; widening it
  would need an auth story first. Covered by a test on the bound listener
  address, not just config. Because the bind is the access control, requests
  whose Host header is not `127.0.0.1`/`localhost` are rejected with 403
  (`requireLoopbackHost`) — otherwise a hostile page could DNS-rebind a
  domain it controls onto 127.0.0.1 and read `/api/*` through the victim's
  own browser.
- **Zero runtime network dependencies:** one static page embedded with
  `go:embed` — hand-written JS/CSS plus a pinned, vendored cytoscape.js
  (`internal/serve/ui/vendor/README.md` records its version and MIT license).
  No build step, no npm, no CDN.
- **Spawns nothing.** The server itself never shells out to
  `open`/`xdg-open`; browser-open lives behind its own seam —
  `browser.Opener`, the fourth exec seam (ADR 0006) — and only the CLI wires
  it. A fresh `run`/`auto` whose stdout is a terminal embeds this server
  (same `serve.Listen`/handler/`serveRun` lifecycle, ephemeral loopback
  port) for exactly the run's duration, prints the URL as `serve` does, and
  opens it through the injected `ExecOpener`; `--no-web` opts out, a
  non-terminal stdout (scripts, CI) gets no server, no browser, and
  byte-identical output. A chat graph turn and a `resume` leg stay un-wired
  (ADR 0006), and the standalone `serve` subcommand still just prints the
  URL.
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
- **v1 scope is the single-run live view ONLY:** no run list page, no history
  browsing, no auth, no config file, no WebSocket (SSE over the append-only
  stream is the whole transport).

## Auto mode — planned graphs, no hand-written YAML
`oh-my-graph auto "<goal>" [--input k=v ...]` is the zero-config path; custom
YAML stays the precise-control path. Planning a graph is ONE
planner call through the same NodeRunner seam every node uses (ClaudeCLIRunner:
env scrub, read-only `plan` permission mode, never the Agent SDK) — the
Coordinator makes exactly that one call per `auto` run. (Interactive `chat`
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
still not enumerable; **Layer 4 is unverified** (E5 — `--strict-mcp-config`
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
| `budget_usd`, `timeout`, `retry` | allowed |

Both mechanisms apply ONLY to coordinator-planned graphs; hand-written YAML
(`oh-my-graph run`) is human-authored/reviewed, passes a nil deny list, and is
not restricted by either. The generated spec is
saved to `~/.oh-my-graph/runs/<run-id>/graph.json` — being valid YAML it can be
hand-edited and re-run with `oh-my-graph run` — then executed by the same
Scheduler as any other graph.

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
  type NodeInvocation struct { Prompt, Cwd, PermissionMode, ResumeSession, Agent string; Policy ToolPolicy }
  type NodeOutcome struct { SessionID, Result string; TotalCostUSD float64; ExitCode int; FailureCause string; BudgetExhausted bool }
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
  `run`/`auto` call sites only, behind the TTY-and-not-`--no-web` gate (see
  "Web live view"); everywhere else the Opener is nil — the live view is off
  entirely and no Opener is consulted.
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
  node_retried/run_finished), one JSON line per transition, fsynced per line.
  Emitted from the same scheduler hook points as the progress line and the
  snapshot, via an `EventSink` interface defaulting to a no-op — the third
  destination next to `Recorder`, same seam pattern. `node_started` and
  `node_retried` publish the attempt's pre-assigned session id (see
  `--session-id` above), so a consumer can locate a running node's transcript
  before the terminal event. This is the stable
  consumer contract fleetops tails (oh-my-graph executes, never renders); the
  full contract, including how it versions alongside `state.json`, is
  docs/RUN-FEED.md.
- **RunLedger** — record session_id/cost/verdict/timing, plus auto mode's one
  planning-call cost; end-of-run table + total cost (planning cost included, so
  an auto run's total is honest; a hand-written `run` records no planning cost).

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
any DSL; TUI/dashboard (fleetops's job); worktree auto-creation (opt-in
per-node `worktree:` shipped later — see "Worktree isolation"); coordinator
auto-mapping of `agent:` by role (see "Node-as-subagent" — deferred on a design
constraint, not on effort); sub-call / cross-node budget accounting (per-node
mid-node kill via `--max-budget-usd` and post-hoc budget halt ARE both enforced
— see "Execution engine").

## v1.1 scope
IN: evidence-grounded `success_check.verify` (#7); `gate` execution +
`oh-my-graph resume` (#9); the layered tool ceiling for planned nodes and the
planned-node field dispositions (#11); node-level `agent:` for hand-written
graphs (PR #6). Each ships as its own PR — see "Implementation sequencing".

## Repo layout
```
cmd/oh-my-graph/{main,flags,resume,runs,show,watch,serve,chat,lint,dryrun,liveview,version}.go + _test  CLI: parse flags, load, inject ClaudeCLIRunner+ShellVerifier, run/resume/runs/show/watch/serve/chat, print ledger
internal/graph/{graph,validate}.go + _test   Graph/Node value objects, YAML, DAG validation, ReadyGiven
internal/schedule/{scheduler,errors}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors
internal/runner/{runner,claude,fake}.go + build-tagged procgroup_{unix,windows}.go + claude_test, envelope_test  interface + ToolPolicy + ClaudeCLIRunner(ENV SCRUB) + FakeRunner
internal/verify/{verify,shell,fake}.go + build-tagged {shell,procgroup}_{unix,windows}.go + _test  Verifier seam — ShellVerifier is the second of the four exec seams (ADR 0002)
internal/worktree/{worktree,git,fake}.go + _test  worktree Provider seam — GitManager is the third exec seam (ADR 0005): per-run managed checkouts + work-preserving cleanup
internal/browser/{browser,exec,fake}.go + build-tagged argv_{darwin,unix,windows}.go + _test  browser Opener seam — ExecOpener is the fourth exec seam (ADR 0006): default-browser launch, wired behind run/auto's TTY gate
internal/invariants/exec_seam_test.go          test-only: asserts exactly the four exec-seam files import os/exec (a fifth importer fails CI — ADR 0002/0005/0006)
internal/childenv/childenv.go + _test          the shared "delete billing-switching vars" child-env policy (all four spawners)
internal/coordinator/{coordinator,router}.go + _test  auto mode: goal → planner call (NodeRunner seam) → validated graph + ToolPolicies; chat routing
internal/handoff/handoff.go + _test            interpolation, artifact persist/resolve, session pick, Seed for resume
internal/gate/gate.go + _test                  Decision + PauseController/RecordedController
internal/runstate/{runstate,recorder,lock}.go + _test  state.json snapshot — atomic write, schema version, run lock, resume load
internal/runfeed/{runfeed,reader}.go + _test   events.jsonl append-only lifecycle event stream — the consumer contract (docs/RUN-FEED.md) — plus the in-repo consumer readers (InFlight, Follow)
internal/serve/{serve,resolve}.go + ui/ + _test  `serve`: read-only, 127.0.0.1-only web live view of one run — embedded static UI (go:embed) + vendored cytoscape.js; a consumer of the run-feed contract
internal/ledger/ledger.go + _test              RunLedger summary + total cost
graphs/haiku-smoke.yaml, graphs/dev-review-pr.yaml, graphs/self-dev.yaml (+ internal/graph/shipped_graphs_test.go asserts they parse)
docs/adr/000{1..6}-*.md
README.md, SECURITY.md, LICENSE(MIT), go.mod, Makefile(build/test/lint)
```

## Verify MVP cheaply (real claude, cents)
`graphs/haiku-smoke.yaml`: node `write` ("write a 3-line haiku about graphs to
haiku.txt", acceptEdits) → node `critique` (depends_on write, reads
{{artifacts.write}}, plan). `mkdir -p /tmp/omg-smoke && oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke`.
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

Seam patterns to mirror: fleetops `internal/control/spawncmd.go` (package-var fn seam),
`internal/control/control.go` (`runBounded`/`exec.CommandContext` bounded exec).

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
  Layer 1 and `agent:` therefore cannot be combined. This costs nothing today
  (planned nodes reject `agent:`, hand-written graphs get no Layer 1) but it is
  a hard constraint on ever extending Layer 1 to hand-written graphs, and a
  second, independent reason coordinator auto-mapping of `agent:` is impossible
  rather than merely unbuilt.
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
- **E6 — MEASURED ONLY IN A CONFIGURATION THIS TOOL NEVER EMITS.** With
  `--agent code-reviewer` (frontmatter `tools: Read, Grep, Glob, Bash`) plus
  `--tools "Read"`, the node could not run a shell command: zero tool calls, no
  permission denial. So a resolved subagent's frontmatter does not widen past
  `--tools`.

  **That result does not transfer to any path oh-my-graph actually produces**,
  and saying otherwise would be the overclaim this section exists to prevent.
  `--tools` is emitted only by auto mode, and auto mode rejects `agent:`; the
  one path where `agent:` is legal — hand-written graphs — never passes
  `--tools` at all. So for the real `agent:` case there is **no measured tool
  bound**, and the precise composition between a subagent's `tools:` and a
  node's `allowed_tools` is unknown. oh-my-graph states **no reconciliation
  rule**, and coordinator auto-mapping stays deferred.
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
