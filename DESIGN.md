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
  --permission-mode <mode> --allowedTools "<comma,joined>" [ --resume <session_id> ]
```
run with `cwd` = node.cwd. JSON envelope → `session_id`, `result`, `total_cost_usd`.
- **Subscription auth crux:** start from `os.Environ()` and **DELETE
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`** from the child env (they
  silently switch to metered API billing). Enforced in code + asserted by a unit
  test on the built argv/env. NEVER `--bare` (disables OAuth). NEVER the Agent SDK.
- Per-node `context.WithTimeout` (default ~20m) so a wedged child can't hang the graph.
- Non-JSON/parse failure = node failure (`NodeOutputError`), never a silent zero result.
- permission modes: `dontAsk` (default unattended) / `acceptEdits` / `plan` (read-only).
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
  agent: code-reviewer        # optional, v0.2: run as this Claude Code subagent — see "Node-as-subagent" below
  budget_usd: 0.50            # v0.1: parsed onto the node and recorded in RunLedger; NOT enforced (no cap yet)
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

## Node-as-subagent (`agent:`, v0.2 skeleton)
A node may set `agent: <name>` (e.g. `agent: code-reviewer`) to run as the
user's OWN Claude Code subagent instead of plain `claude -p`. The mechanism is
one flag: `NodeInvocation.Agent` flows into `--agent <name>` on the `claude -p`
argv, which `claude` resolves against the user's existing `~/.claude/agents`
and `<cwd>/.claude/agents` definitions — the node then inherits that
subagent's system prompt, tools, and model, no oh-my-graph-side agent registry
needed. v0.1 does not validate the name exists (per-machine/per-repo, not a
property of the graph file); an unresolvable name is a runtime fallback to
plain claude, not a load-time error. **Follow-up (v0.3, not built here):** a
coordinator step that scans the user's `~/.claude/agents` + `.claude/agents`
and auto-assigns `agent:` per planned node by role, so hand-authoring this
field becomes optional.

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
  type NodeInvocation struct { Prompt, Cwd, PermissionMode, ResumeSession string; AllowedTools []string }
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
- **GateController** — pause/approve/reject for gate nodes (v1.1 stub in v0.1).
- **RunLedger** — record session_id/cost/verdict/timing; end-of-run table + total cost.

Node lifecycle: Scheduler ready → Handoff.ResolveInputs → NodeRunner.Run →
success_check → pass: Handoff.PersistOutput + RunLedger.Record + enqueue dependents;
fail: retry or Record(FAIL) + cancel if halt. Scheduler never knows if a real claude ran.

## MVP scope (v0.1) — smallest thing that runs a real multi-node graph
IN: YAML loader + DAG/cycle validation; {{inputs}}/{{artifacts}} interpolation;
concurrent ready-set scheduler + cap + halt-on-fail; ClaudeCLIRunner (exact argv,
ENV SCRUB, timeout, JSON parse); FakeRunner + full scheduler unit tests (no real
claude in CI); artifact handoff (default) + session handoff (simple one→one);
success_check (exit_zero + result_matches); RunLedger table + total cost;
CLI `oh-my-graph run <graph.yaml> --input k=v`; nodes run in real cwds with session
persistence ON (fleetops-observable — do NOT pass --no-session-persistence).

DEFERRED (say so in README): gate/human-pause + `oh-my-graph resume` (v1.1, schema
reserved); retries beyond flat max:1; parallel-group sugar / any DSL; TUI/dashboard
(fleetops's job); --continue-on-fail; mid-node budget kill; worktree auto-creation.

## Repo layout
```
cmd/oh-my-graph/{main,flags}.go      CLI: parse flags, load, inject ClaudeCLIRunner, run, print ledger
internal/graph/{graph,validate}.go + _test   Graph/Node value objects, YAML, DAG validation
internal/schedule/{scheduler,errors}.go + _test  ready-set engine (drives FakeRunner — keystone) + typed errors
internal/runner/{runner,claude,fake}.go + claude_test, envelope_test  interface + ClaudeCLIRunner(ENV SCRUB) + FakeRunner
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
opt-in per node with a loud warning, never a default.

## Open questions (decided defaults; refine in impl)
1. artifact interpolation: substitute file PATH by default, `| inline` filter for content.
2. budget: `budget_usd` is parsed onto the node and the RunLedger records each
   node's actual cost, but v0.1 does NOT enforce any budget — there is no cost
   cap yet. Enforcement (post-hoc halt and mid-node kill) is deferred to v1.1.
3. parallel nodes sharing one cwd can race edits → v0.1 parallel nodes should be
   read-only (plan) reviews (the captain's fan-out case); parallel edits want
   worktrees (deferred).
4. session-handoff + multi-parent fan-in conflict → validation rejects `handoff:
   session` on a node with 2+ session-parents; multi-parent must use artifact.

Seam patterns to mirror: fleetops `internal/control/spawncmd.go` (package-var fn seam),
`internal/control/control.go` (`runBounded`/`exec.CommandContext` bounded exec).
