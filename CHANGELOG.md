# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

oh-my-graph is **alpha software**. The graph YAML schema, the CLI, and the
`NodeRunner` interface may change without notice before `v1.0.0`.

## [v0.3.1] - 2026-08-01

The hardening patch after the first CI flake: the test suite, CI, and the
shipped templates absorb the post-mortem's lessons, and static graph checks
learn to warn about things that are valid but will not behave as written —
without changing what any run does.

### Added

- **Advisory placeholder warnings in `lint` and `run --dry-run`.** Every
  `{{ ... }}` token that is placeholder-like but will not resolve — a typo
  or unknown filter that ships verbatim into a paid prompt, an input the
  graph never declares, an artifact of a node that is not an ancestor — now
  gets one warning, across a node's prompt/cwd and its verify block. Tokens
  that don't look like placeholders are left alone as deliberate literal
  text, and the strict-parse judgment comes from the same regex the runtime
  interpolates with, so lint and run can never drift. Warnings are advice
  only: runtime behavior and exit codes are
  unchanged. ([#73](https://github.com/jitokim/oh-my-graph/pull/73))
- **Session-handoff guards.** Three ways a `handoff: session` node could
  quietly resume nothing are now caught up front: a **gate parent is
  rejected at load time** (a gate spawns no subprocess and records no
  session to resume); lint warns when the child's **cwd/worktree differs
  from its session-parent's** (claude's session lookup is
  project-directory-scoped, so the resume may start cold or attach to the
  wrong project) and when the node also declares a **retry** (a retried
  attempt never resumes the parent session); and a retried session node's
  ledger detail now states outright that the retry **started fresh** —
  parent session not resumed. ([#75](https://github.com/jitokim/oh-my-graph/pull/75))
- **An advisory CI stress job, and a "Releasing" checklist.** When a change
  touches the concurrency-heavy packages (schedule, runner, runfeed,
  verify), CI now runs their tests under `-race -count=200` as a
  non-blocking job — a flaky test passes a single run, so determinism gets
  its own signal — and CONTRIBUTING gains the release checklist the v0.3.0
  post-mortem called for. ([#71](https://github.com/jitokim/oh-my-graph/pull/71))

### Changed

- **The shipped templates teach the post-mortem idioms.** In
  `dev-review-pr.yaml` and `self-dev.yaml`: dev nodes commit after each
  coherent step so a node timeout can never lose finished work, e2e nodes
  stress concurrency-touching diffs with `-race -count=300`, and
  style-review nodes check test doubles for unwired synchronization and for
  assertions an absent record would satisfy — the two shapes of the defect
  that survived review. ([#69](https://github.com/jitokim/oh-my-graph/pull/69))

### Fixed

- **The test suite is hardened against the flaky-test class found in the
  post-v0.3.0 audit.** The `haltRunner` double is deterministic — it fails
  only after the sibling has started, instead of racing
  it ([#68](https://github.com/jitokim/oh-my-graph/pull/68)); the same
  audit's sweep replaced timing-dependent doubles with deterministic
  synchronization, made absence assertions prove absence rather than pass
  on an empty record, and covered the real-writer fan-out
  paths ([#70](https://github.com/jitokim/oh-my-graph/pull/70)); and the
  structural gaps it exposed — CLI wiring, dispatch, and run-lock
  edges — are now pinned by
  tests ([#72](https://github.com/jitokim/oh-my-graph/pull/72)).

### Documentation

- **Handoff is a first-class README concept.** What was a buried one-liner
  is now a named section: an artifact-vs-session comparison table, explicit
  statements of what a resumed session does and does not inherit (the
  parent's conversation — never its tool grants, permission mode, or cwd),
  and handoff recipes in
  [docs/EXAMPLES.md](docs/EXAMPLES.md). ([#74](https://github.com/jitokim/oh-my-graph/pull/74))

## [v0.3.0] - 2026-08-01

The live view release: a read-only web view of a run, opened automatically
from an interactive `run`/`auto`, plus static graph tooling (`lint`,
`--dry-run`) and sharper failure reporting.

### Added

- **`serve` — a read-only web live view of one run.** `oh-my-graph serve
  [<run-id>] [--port N]` (default port 8642, newest in-flight run when no id
  is given) renders the run feed-first: a chronological narrative rebuilt
  from the SSE replay of `events.jsonl`, with each settled node's artifact
  inline (capped with a show-more expander) and the failure cause leading
  emphasized when a node fails. The DAG is a compact collapsible side map
  (vendored cytoscape.js 3.34.0 + dagre, MIT, SHA-256-pinned — zero runtime
  network dependencies, no build step). serve is strictly a consumer of the
  run-feed contract, binds to **127.0.0.1 only** (run directories hold
  prompts and session ids), and spawns nothing. ([#60](https://github.com/jitokim/oh-my-graph/pull/60))
- **`run`/`auto` auto-open the live view behind a TTY gate.** An interactive
  run embeds serve's listener on an ephemeral loopback port for exactly the
  run's duration, announces the URL, and opens the browser; `--no-web`,
  scripts and CI get nothing and byte-identical output. Browser-open is the
  **fourth exec seam** (`browser.ExecOpener`, env-scrubbed like every
  spawner, refusing any URL that is not plain http on a loopback host); see
  [ADR-0006](docs/adr/0006-browser-open-is-a-fourth-exec-seam.md). Live-view
  failures never fail the run. ([#65](https://github.com/jitokim/oh-my-graph/pull/65))
- **The `oh-my-graph` plugin agent — a one-word entry point.** The plugin
  now ships `agents/oh-my-graph.md`, a graph-engineering agent launched with
  `claude --agent oh-my-graph` (recommended shell function:
  `omg () { claude --agent oh-my-graph "$@"; }`) instead of typing
  `/oh-my-graph:graph` every turn. It drives the binary; it reimplements no
  graph logic. ([#57](https://github.com/jitokim/oh-my-graph/pull/57))
- **Chat confirms a planned graph before executing it.** A graph-worthy chat
  turn now asks `Run this plan? [y/N]` between printing the plan and running
  it. Default is No; declining prints "plan discarded." and serves the next
  turn; EOF ends the session gracefully. `auto` stays fully
  non-interactive. ([#58](https://github.com/jitokim/oh-my-graph/pull/58))
- **`runs list` renders an in-flight run as RUNNING.** In-flight is read
  from the run-feed contract (an open `run_started` leg in `events.jsonl`);
  a live run with no snapshot yet renders its honestly-known row with "-"
  placeholders instead of being skipped, and a partially-complete live run
  no longer renders FAIL. ([#59](https://github.com/jitokim/oh-my-graph/pull/59))
- **`lint <graph.yaml>`.** Statically report *every* structural issue in a
  graph file — zero nodes spawn, zero cost. `Validate()` is redefined as the
  first element of the new `Graph.Issues()`, so lint and run can never
  disagree about which graphs are valid. Exit 0 when valid, 1 when
  not. ([#50](https://github.com/jitokim/oh-my-graph/pull/50))
- **`run --dry-run`.** Validate and print the resolved plan without
  executing any node: the same lint pass, plus proof that every
  `{{ inputs.* }}` reference resolves against the bound `--input` values.
  Exit 0 when a real run would start, 1 when it would refuse. Artifact
  references stay unjudged — they materialize only while a run
  executes. ([#55](https://github.com/jitokim/oh-my-graph/pull/55))
- **Node failure details name the cause, not just the symptom.** A failed
  node's detail now carries the claude envelope's own error report (else the
  stderr tail), so a subscription session limit reads "exit code 1: You've
  hit your session limit" instead of "failed success_check exit_zero". A
  cancelled sibling reads `cancelled: run halted after node "X" failed`
  instead of "context canceled", deadline and cancel are distinguished, and
  one shared 240-rune cap bounds every detail so `events.jsonl` stays
  tailable. ([#64](https://github.com/jitokim/oh-my-graph/pull/64))
- **Node ids are restricted to a single safe path element at load time.** A
  node id becomes an artifact filename and a serve URL parameter; ids now
  pass the same `^[A-Za-z0-9][A-Za-z0-9._-]*$` rule as worktree names, with
  a load-time error naming the offending id. Planned specs inherit the rule
  through the same parser. ([#61](https://github.com/jitokim/oh-my-graph/pull/61))
- **Unknown `retry.on` causes are rejected at load time.** A typoed cause
  token silently never retried; the six tokens are now constants shared by
  the validator and the scheduler, and anything outside the set is rejected
  with a message listing every valid token. ([#54](https://github.com/jitokim/oh-my-graph/pull/54))
- **Planned commit nodes must stage with scoped `git add <path>`.** The
  auto-mode planner prompt now forbids `git add -A` / `.` / `-u`, so a
  planned node cannot sweep unrelated untracked files into its
  commit. ([#51](https://github.com/jitokim/oh-my-graph/pull/51))
- **The exec-seam invariant is enforced by a test.** An import-allowlist
  test walks `internal/` and `cmd/` and fails CI if any non-test file
  outside the documented seams imports `os/exec` — a new spawner (or a stale
  allowlist entry) is a red test pointing at the ADR requirement, not a
  review oversight. ([#52](https://github.com/jitokim/oh-my-graph/pull/52))

### Fixed

- **serve/runfeed hardening.** Requests whose Host header is not
  `127.0.0.1`/`localhost` are rejected with 403 (DNS-rebinding guard on top
  of the loopback bind); serve's lifecycle surfaces listener failures
  immediately, returns Shutdown's error, and derives request contexts from
  the command context so a cancel also ends open SSE streams; the two
  run-feed readers share one 1 MiB line cap and one framing (plus
  `runfeed.FollowWait` for streams that don't exist yet), pinned by
  package-local reader tests. ([#63](https://github.com/jitokim/oh-my-graph/pull/63))
- **Feed readability.** One feed entry per settled node (the terminal entry
  absorbs the started-line), single-line artifacts render inline in the
  entry head, and a node with no artifact renders no empty result
  block. ([#62](https://github.com/jitokim/oh-my-graph/pull/62))
- **Worktree cleanup survives a branch rename.** Cleanup now judges
  emptiness by the worktree's own HEAD against the recorded base instead of
  the stored branch name, so an empty lane whose branch was renamed is
  removed silently instead of noisily retained. ([#53](https://github.com/jitokim/oh-my-graph/pull/53))

### Documentation

- **README restructured into a front page.** Identity, quickstart, usage,
  one example and the graph model stay; the remaining walkthroughs and
  feature recipes moved to [docs/EXAMPLES.md](docs/EXAMPLES.md), platform
  detail and known gaps to [docs/LIMITATIONS.md](docs/LIMITATIONS.md), and
  the prior-art survey to
  [docs/PRIOR-ART.md](docs/PRIOR-ART.md). ([#66](https://github.com/jitokim/oh-my-graph/pull/66))
- Three DESIGN.md drifts fixed after a doc audit (the budgeted-node argv,
  the repo-layout map, chat's routing call vs "exactly one planner
  call") ([#56](https://github.com/jitokim/oh-my-graph/pull/56)); the
  `handoff: session` exactly-one-parent rule is now stated next to its
  definition in README and
  DESIGN.md ([#49](https://github.com/jitokim/oh-my-graph/pull/49)).

## [v0.2.0] - 2026-07-31

### Added

- **Per-node git worktree isolation — `worktree: <name>`.** A node can now run
  in a dedicated git worktree instead of the invocation directory, so a graph
  never mutates your checked-out working tree (no more sweeping in your
  untracked files, no branch surprises). Nodes that share a name share one
  worktree — a whole `dev → e2e → review → pr` lane works in a single isolated
  checkout — while nodes with **different** names get **different** worktrees
  and can edit files in parallel, without the shared-tree race that otherwise
  forces lanes to serialize. Nodes with no `worktree` field keep today's
  behaviour (fully backward compatible). A clean worktree is removed at run
  end; one with uncommitted changes is kept in place (with instructions) rather
  than losing work. `worktree:` is rejected on auto-planned nodes — an
  unreviewed plan must not spawn worktrees. Worktree provisioning is a third
  `os/exec` seam; see [ADR-0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md).

## [v0.1.1] - 2026-07-31

First patch after the public launch — run-feed observability and hardening.

### Added

- **`watch <run-id>`.** Tail a run's `events.jsonl` as a plain-text feed (the
  same `▶ / ✓ / ✗` shape as the live run), following until `run_finished` or
  interrupt. A lightweight, dependency-free way to observe a run from another
  terminal — deliberately not a TUI.
- **Gate events in the run feed.** `events.jsonl` now emits `gate_paused`,
  `gate_approved`, and `gate_rejected` from the same hook points as the
  progress feed, so a consumer (fleetops) can see gate state from the stream
  without reading `state.json`. Event-stream schema bumped to `2`.

### Fixed

- **Run-id collisions.** `newRunID` now stays unique for runs minted in the
  same second — a per-process atomic sequence plus a nanosecond timestamp — so
  concurrent or rapid runs no longer share a run directory.
- The snapshot JSON round-trip test now actually asserts the round-trip
  (marshal → parse → equal); `self-dev.yaml` is covered by the shipped-graph
  parse test; and a `!windows` build tag was aligned to `unix`.

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
