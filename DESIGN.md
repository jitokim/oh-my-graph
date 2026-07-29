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
- **`--allowedTools` adds, `--disallowedTools` subtracts.** `--allowedTools` is
  unioned with the user's own `~/.claude/settings.json` grants — it can never
  shrink them, so it bounds what a node is *asked* to use, not what it *can* do.
  Of the two flags oh-my-graph passes, only `--disallowedTools` beats a prior
  allow, and only at bare-tool-name granularity (`Bash`); a scoped deny like
  `Bash(*)` matches a command literally starting with `*` and enforces nothing.
  Measured on claude 2.1.220. (That CLI also has `--tools`, which replaces the
  built-in set outright — a stronger primitive, deliberately not adopted yet;
  see "Auto mode".) `--disallowedTools` is emitted ONLY when a caller imposes a
  ceiling (auto mode does); hand-written graphs never carry the flag.
  `bypassPermissions` opt-in per node only, loud warning at load, never a graph default.

## Graph model — YAML (committed)
Edges are inline `depends_on` (no separate edges list — single source of truth).
Parallelism is **emergent**: nodes sharing `depends_on` that don't depend on each
other run concurrently (up to cap). No `parallel-group` type in v1.

Node schema:
```yaml
- id: e2e
  type: claude-run            # claude-run | gate(v1.1, schema-reserved)
  depends_on: [dev]           # fan-in: all must succeed first
  prompt: |                   # may interpolate {{ inputs.<name> }} and {{ artifacts.<id> }}
    Run make local PORT=8080 and report PASS or FAIL.
  cwd: "{{ inputs.repo }}"
  allowed_tools: [Read, "Bash(make *)", "Bash(git *)"]
  permission_mode: dontAsk
  budget_usd: 0.50            # post-hoc cap: node FAILS if its actual cost exceeds this (see Execution engine)
  handoff: artifact           # artifact(default) | session
  success_check: { exit_zero: true, result_matches: "PASS" }
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

success_check: `exit_zero` AND `result_matches` (regex over .result) if specified;
empty ⇒ exit_zero only. Failed check → `NodeCheckError` (node id + predicate).
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

The allowlist bounds what a plan may **declare**; it cannot bound execution,
because it is rendered as `--allowedTools`, which only adds to the user's own
settings grants (see "Node runtime mechanics"). The execution bound is a second,
separate mechanism: `Plan.DisallowedTools` carries a per-node deny list —
every tool in `coordinator.deniableTools` (`Bash`, `Edit`, `Write`, `MultiEdit`,
`NotebookEdit`, `WebFetch`, `WebSearch`, `Task`, `Agent`) whose bare name the
node did not declare — which the caller hands to `schedule.Options.DisallowedTools`
and the runner renders as `--disallowedTools`. The planner call itself declares
no tools and therefore carries the full deny list. That is what stops a user's
standing `Bash(*)`/`Write(*)`/`WebFetch(*)` grant from leaking into an
unattended, unreviewed plan.

**Known gaps** — the deny list is an enumeration over an open set, so this is a
reduction, not a sandbox: (1) a node declaring any scoped `Bash(...)` pattern
keeps the whole `Bash` tool, since a deny cannot say "all Bash except these
prefixes"; (2) `mcp__<server>__<tool>` and skill surfaces are not enumerable
here and are not covered; (3) settings *hooks* are not tool calls, so a
write-capable node can still drop a `.claude/settings.local.json` in the
invocation directory. The CLI's `--tools` (replace the built-in set) and
`--strict-mcp-config` are structurally better primitives that would close (1)
and (2); adopting them changes what every node can do and is deferred as a
product decision, not a bug fix.

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
  type NodeInvocation struct { Prompt, Cwd, PermissionMode, ResumeSession string; AllowedTools, DisallowedTools []string }
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
- **Handoff** — interpolate {{artifacts/inputs}}, persist outputs, pick --resume session.
- **Coordinator** — auto mode: one planner NodeRunner call → JSON graph spec →
  existing Parse/Validate (+ bypassPermissions refusal). Never runs the graph itself.
- **GateController** — pause/approve/reject for gate nodes (v1.1 stub in v0.1).
- **RunLedger** — record session_id/cost/verdict/timing; end-of-run table + total cost.

Node lifecycle: Scheduler ready → Handoff.ResolveInputs → NodeRunner.Run →
success_check → Handoff.PersistOutput → budget_usd check → pass: RunLedger.Record
+ enqueue dependents; fail (check OR budget): retry or Record(FAIL) + cancel if
halt. Output is persisted before the budget verdict so an over-budget node's
artifact survives. Scheduler never knows if a real claude ran.

## MVP scope (v0.1) — smallest thing that runs a real multi-node graph
IN: YAML loader + DAG/cycle validation; {{inputs}}/{{artifacts}} interpolation;
concurrent ready-set scheduler + cap + halt-on-fail; ClaudeCLIRunner (exact argv,
ENV SCRUB, timeout, JSON parse); FakeRunner + full scheduler unit tests (no real
claude in CI); artifact handoff (default) + session handoff (simple one→one);
success_check (exit_zero + result_matches); RunLedger table + total cost;
CLI `oh-my-graph run <graph.yaml> --input k=v` and `oh-my-graph auto "<goal>"`
(planned graphs — see Auto mode); nodes run in real cwds with session
persistence ON (fleetops-observable — do NOT pass --no-session-persistence).

DEFERRED (say so in README): gate/human-pause + `oh-my-graph resume` (v1.1, schema
reserved); retries beyond flat max:1; parallel-group sugar / any DSL; TUI/dashboard
(fleetops's job); --continue-on-fail; mid-node budget kill (post-hoc budget halt
IS enforced — see "Execution engine"); worktree auto-creation.

## Repo layout
```
cmd/oh-my-graph/{main,flags}.go      CLI: parse flags, load, inject ClaudeCLIRunner, run, print ledger
internal/graph/{graph,validate}.go + _test   Graph/Node value objects, YAML, DAG validation
internal/schedule/{scheduler,errors}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors
internal/runner/{runner,claude,fake}.go + claude_test, envelope_test  interface + ClaudeCLIRunner(ENV SCRUB) + FakeRunner
internal/coordinator/coordinator.go + _test    auto mode: goal → planner call (NodeRunner seam) → validated graph
internal/handoff/handoff.go + _test            interpolation, artifact persist/resolve, session pick
internal/gate/gate.go                          v1.1 stub interface
internal/ledger/ledger.go + _test              RunLedger summary + total cost
graphs/haiku-smoke.yaml, graphs/dev-review-pr.yaml (+ internal/graph/shipped_graphs_test.go asserts they parse)
docs/adr/0001-subprocess-not-sdk.md
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
is empty or names a tool outside the fixed allowlist, or that sets `cwd`; and
`Plan.DisallowedTools` imposes a per-node `--disallowedTools` ceiling at
execution time so the user's own standing tool grants cannot widen an
unreviewed plan. Both, plus the known Bash-granularity gap, are described in
"Auto mode" above.

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
