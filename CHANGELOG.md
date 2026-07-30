# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

oh-my-graph is **alpha software**. The graph YAML schema, the CLI, and the
`NodeRunner` interface may change without notice before `v1.0.0`.

## [v0.1.0] - 2026-07-31

Initial MVP: a graph-native orchestrator that runs each DAG node as a real
`claude -p` subprocess on the user's own Claude subscription.

### Added

- **YAML graph model.** A graph file (`name`, `inputs`, `concurrency`,
  `nodes`) with inline `depends_on` edges — no separate edge list, so the
  topology has one source of truth. DAG/cycle validation at load time.
- **Ready-set concurrent scheduler.** Kahn-style topological execution that
  keeps a "ready set" running concurrently, capped by `concurrency` (ceiling
  10, default 4). Halt-on-fail by default; `--continue-on-fail` prunes only
  the failed subtree instead of stopping the whole run.
- **`ClaudeCLIRunner` with subscription-auth env scrub.** The node runtime is
  a raw `claude -p ... --output-format json` subprocess. Every child process's
  environment has `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` deleted so a
  node can never silently fall back to metered API billing — this is
  unit-tested against the built command. Never `--bare`, never the Agent SDK.
- **Artifact and session handoff.** `handoff: artifact` (default) persists
  each node's result to `~/.oh-my-graph/runs/<run-id>/<node-id>.out` and
  interpolates it into dependents via `{{ artifacts.<id> }}` (path by default,
  content with the `| inline` filter) — the only option for fan-in.
  `handoff: session` resumes a single parent's claude session
  (`--resume <session_id>`) for tight sequential continuation; rejected at
  load time on a node with more than one session-parent.
- **Success checks and retry.** `success_check` (`exit_zero`,
  `result_matches` regex) gates whether a node counts as passed. `retry`
  re-runs a failed node up to a flat `max`, always in a fresh session.
- **Evidence-grounded `success_check.verify`.** A node can now be judged on
  something other than its own narration: `verify` declares a command
  (`command`, optional `cwd`, `timeout`, `expect_exit`, `output_matches`) that
  **oh-my-graph itself** runs through `sh -c` and judges by exit code and
  output. It composes by AND after `exit_zero`/`result_matches`, and runs after
  them but *before* the node's output is persisted — so a crashed node is never
  verified against the wreckage and an unverified node leaves no artifact.
  `command`/`cwd` interpolate like a prompt; a missing command, an unparseable
  or over-10m `timeout`, and an uncompilable `output_matches` are rejected at
  load time naming the node. A verification that times out or cannot spawn
  fails the node — never a silent pass. New retry cause token: `verify_failed`.
  `result_matches` is retained and unchanged, but is now documented as a
  secondary, self-reported signal. Auto-planned nodes may not declare `verify`
  (it is shell run outside every coordinator guard). ([#7](https://github.com/jitokim/oh-my-graph/issues/7))
- **A second, deliberate exec seam.** `internal/verify` adds a `Verifier`
  interface with `ShellVerifier` (production), `RefusingVerifier` (the
  scheduler's default, so a forgotten injection fails loudly instead of
  spawning) and `FakeVerifier` (tests). The project invariant is restated, not
  weakened: exactly two objects may spawn a process —
  `runner.ClaudeCLIRunner` and `verify.ShellVerifier` — each behind its own
  injected interface, and the whole engine still runs its tests with zero real
  spawns. See `docs/adr/0002-verification-is-a-second-exec-seam.md`.
- **Shared child-environment scrub (`internal/childenv`).** The
  `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` deletion moved out of
  `internal/runner` into a leaf package used by both spawners, because
  `verify: { command: "claude -p ..." }` is legal and would otherwise have run
  on metered API billing. Behaviour for claude nodes is unchanged.
- **Post-hoc `budget_usd` enforcement.** A node whose actual cost exceeds its
  declared `budget_usd` now fails exactly like a failed `success_check`
  (`NodeBudgetError`, ledger `FAIL` carrying budgeted-vs-actual, halt-on-fail by
  default) so its dependents never start. Output is persisted before the budget
  verdict, so an over-budget node keeps its artifact. Its retry cause token is
  `budget_exceeded`, distinct from `nonzero_exit` so an existing retry policy
  cannot re-spend a blown budget by accident. Enforcement is post-hoc only —
  see "Deferred" for why a mid-node kill isn't possible yet.
- **`RunLedger`.** End-of-run table (session id, cost, verdict, duration per
  node) plus the total cost across the run. Each record also carries the node's
  declared `budget_usd`, so the budget-vs-actual delta is derivable per node
  (`Record.BudgetDeltaUSD`); passing nodes report their remaining headroom in
  the `DETAIL` column.
- **CLI:** `oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]`.
- **Auto mode.** `oh-my-graph auto "<goal>" [--input k=v ...]` plans a graph
  from a plain-language goal instead of hand-written YAML: a coordinator makes
  one planner call through the same env-scrubbed `NodeRunner` seam, loads the
  JSON reply with the existing graph parser and validator, saves the spec to
  `~/.oh-my-graph/runs/<run-id>/graph.json` (re-runnable with `oh-my-graph run`),
  and executes it on the same scheduler. A planned node can never request
  `permission_mode: bypassPermissions`, set `cwd`, set `agent`, declare a
  `success_check.verify` command, or name a tool outside a fixed allowlist.
- **Layered tool ceiling for auto-planned nodes** (`runner.ToolPolicy`). Each
  planned node runs under settings-source isolation (`--setting-sources ""`),
  its declared allow rules, tool-set narrowing (`--tools`), `--strict-mcp-config`
  and a residual `--disallowedTools` backstop — carried as one value object per
  node so a caller cannot apply three quarters of a ceiling. Isolation is the
  load-bearing layer: it stops a standing `Bash(*)` in the user's own
  `settings.json` from out-matching a planned node's narrower `Bash(git *)`.
  Verified against a real `claude` 2.1.220 (an out-of-scope shell command runs
  without isolation and is denied with it), so the previously-documented
  scoped-Bash gap is **closed for planned nodes**. MCP closure remains
  unverified and is disclosed as such. Hand-written graphs get none of this and
  keep the user's settings, hooks and MCP servers.
- **`agent:` on a node** (hand-written graphs only) → `claude -p --agent <name>`,
  running that node as one of the user's own Claude Code subagents. An
  unresolvable name **fails the node** rather than falling back to plain claude,
  and the failure now carries the CLI's stderr, which lists the available
  agents.
- **Reflection-driven planned-node field dispositions.** A table-driven test
  over `reflect.VisibleFields` of `graph.Node` and `graph.SuccessCheck` fails
  the build if any field is added to the node schema without an explicit
  auto-mode disposition (allowed / constrained / rejected), and every
  non-allowed field is probed to prove its refusal actually fires and names
  that field. Adding a field without deciding what auto mode does with it is
  now a red test, not a review oversight — it caught `success_check.verify`
  on its first run against a schema it had not been written for.
- **Claude Code plugin surface.** A thin `plugin/` wrapper — a `/graph` slash
  command and a description-routed `run-graph` skill — that shells out to the
  `oh-my-graph` binary and reports back the run ledger. It reimplements no
  graph logic.
- **Shipped graphs.** `graphs/haiku-smoke.yaml` (the cheapest real end-to-end
  smoke, a few cents) and `graphs/dev-review-pr.yaml` (a worked
  dev → e2e → parallel review → PR example).

### Deferred (tracked, not in v0.1)

- `gate` node type / human-pause + `oh-my-graph resume` (schema-reserved,
  execution rejected with a clear "not yet implemented").
- Retry policies beyond a flat `max`; any graph DSL beyond `depends_on`.
- A TUI/dashboard — that's [fleetops](https://github.com/jitokim/fleetops)'s job.
- Mid-node budget kill. `budget_usd` is enforced post-hoc (an over-budget node
  fails and halts the run, so *subsequent* nodes never spend), but a node cannot
  be cancelled while it is still overspending — `claude` reports
  `total_cost_usd` only in the envelope it prints at exit. Doing it honestly
  needs streaming cost from the runner; a budget-derived wall-clock timeout was
  rejected as fake enforcement.
- Worktree auto-creation for parallel edits.
- Coordinator auto-mapping of `agent:` by role. Deferred on a design
  constraint, not on effort: a planned node may not carry `agent:` at all, and
  settings-source isolation disables agent discovery, so the two are mutually
  exclusive as built. An implicit scan of `~/.claude/agents` is rejected
  permanently — it would make an `auto` run depend on files the user forgot
  they had.
