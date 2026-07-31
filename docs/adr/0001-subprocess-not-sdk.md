# ADR 0001 — Node runtime is a raw `claude -p` subprocess, not the Agent SDK

- Status: Accepted
- Date: 2026-07-29

## Context

oh-my-graph orchestrates a DAG of agent nodes. Each node needs to invoke Claude
with a prompt, a working directory, a permission mode, and a tool allow-list,
and to return a result plus a session id and cost. There are two ways to make
that call:

1. The **Anthropic API / Agent SDK**, authenticated with `ANTHROPIC_API_KEY`.
2. The **`claude` CLI** (`claude -p ... --output-format json`), authenticated by
   the user's existing subscription login (OAuth).

The whole thesis of the project is that graph engineering should not force you
onto metered API billing when you already pay for a Max/Pro subscription. Every
existing graph-native orchestrator takes path 1.

## Decision

The node runtime is **exclusively** a raw `claude -p` subprocess (path 2).

- One node = one `exec` of `claude -p <prompt> --output-format json
  --permission-mode <mode> --allowedTools <csv> [--resume <session>]`, run with
  `cwd` set to the node's working directory.
- The child environment is `os.Environ()` with `ANTHROPIC_API_KEY` and
  `ANTHROPIC_AUTH_TOKEN` **deleted** — those switch `claude` to metered API
  billing. This scrub is the load-bearing guarantee and is unit-tested.
- Never `--bare` (it disables OAuth). Never the Agent SDK. Never
  `--no-session-persistence` (fleetops needs the transcript).
- The JSON envelope's `session_id`, `result`, `total_cost_usd`, and `subtype`
  (to detect a `--max-budget-usd` abort) are the outputs the engine reads.

All of this lives behind a single `NodeRunner` interface. `ClaudeCLIRunner` is
one of exactly three objects in the codebase that import `os/exec` (the others
are `verify.ShellVerifier` — see ADR 0002 — and `worktree.GitManager` — see
ADR 0005); everything upstream (the scheduler) depends on the interface, and a
`FakeRunner` makes the whole engine testable without spawning claude.

## Consequences

**Positive**

- `$0` marginal cost per node — runs inside the user's subscription.
- ToS-clean: the tool re-uses the user's own login, ships no credentials, and is
  not a redistributed hosted product (see SECURITY.md).
- Free observability: real working directories + session persistence mean every
  node is an ordinary claude session that
  [fleetops](https://github.com/jitokim/fleetops) already observes.
- The exec surface is one small, testable object.

**Negative / trade-offs**

- We depend on the `claude` CLI's flags and JSON envelope, which can change
  between CLI versions. The single `NodeRunner` seam localizes that risk.
- No structured streaming of intermediate tokens; the engine consumes the final
  JSON envelope per node.
- Cost is reported by claude in the final JSON envelope, so the *engine* learns a
  node's total only after it finishes. `budget_usd` is therefore checked post-hoc
  — an over-budget node fails and, by default, halts the run before its
  dependents spend anything. A mid-node kill, however, does **not** require
  abandoning this one-envelope contract: the `claude --max-budget-usd` flag makes
  the CLI abort a node's own run the moment its spend crosses the budget, and it
  still prints one parseable JSON envelope at exit (verified on claude 2.1.220 —
  `subtype: error_max_budget_usd`, non-zero exit). oh-my-graph wires `budget_usd`
  into that flag as a real per-node (per-`claude -p`-invocation) mid-run kill on
  top of the post-hoc check. What *would* still need streaming cost
  (`--output-format stream-json`) is finer-grained, sub-call accounting —
  catching the single in-flight call that overshoots before the abort lands —
  which stays out of scope for this ADR's envelope contract.

## Alternatives considered

- **Agent SDK + `ANTHROPIC_API_KEY`.** Rejected: metered billing is exactly the
  cost model the project exists to avoid, and it would make the tool an API
  client rather than a subscription-native orchestrator.
- **Wrapping the CLI but keeping the API key in the child env "just in case".**
  Rejected: the key silently overrides OAuth, so its mere presence defeats the
  subscription-auth guarantee. It must be scrubbed, not left dormant.
