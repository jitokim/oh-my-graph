# ADR 0025 — One run has one CLI runtime

**Status:** Accepted

**Date:** 2026-08-14

## Context

The execution boundary is named `NodeRunner`, but its only production
implementation, constructor, argv builder, output decoder, session lifecycle,
environment policy, documentation, and persisted state all assume Claude Code.
Adding Codex at each call site would create two process-spawning paths and make
resume or browser gate decisions guess which CLI owns an existing session.

Codex's non-interactive contract is materially different:

- it runs as `codex exec --json` and resumes as `codex exec ... resume <id>`;
- its stdout is JSONL, beginning with `thread.started`, ending with
  `turn.completed`, and carrying the final reply in an `item.completed`
  `agent_message`;
- it reports token usage but no USD total;
- it has sandbox modes instead of Claude's tool-name grant flags;
- it cannot pre-assign a thread id, attach a Claude `agent:`, or enforce
  `budget_usd`.

These claims are checked against Codex CLI 0.137.0 and the official OpenAI
developer command/configuration references on 2026-08-14.

## Decision

### Runtime selection

`oh-my-graph --runtime claude|codex <command>` selects one runtime for the
whole invocation. The default remains `claude`; nodes in one run may not mix
runtimes. `run`, `auto`, `chat`, and runtime-aware `lint` consume the selection.
Fresh run snapshots persist it. `resume` and browser gate resumes read it from
the snapshot, so a continuation cannot silently switch CLIs. `state.json`
schema 3 makes the field mandatory for compatible persisted runs; an empty
in-memory value still canonicalizes to Claude at the CLI boundary.

### One process seam, two protocols

Delete the Claude-specific production runner abstraction. Replace it with one
`CLIRunner`, still the first of the program's exactly four `os/exec` seams.
Claude and Codex protocol values beneath it own binary name, argv construction,
and output decoding. The scheduler continues to depend only on `NodeRunner`.

The runtime protocol owns when a session exists. Claude mints its UUID before
spawn; Codex learns its thread id from `thread.started`; resumed sessions are
already known. A `SessionStarted` callback on `NodeInvocation` publishes that
fact. The scheduler no longer mints Claude UUIDs or pretends every runtime can
know a session before launch.

### Codex policy mapping

Claude argv remains byte-for-byte compatible. Codex maps graph permission
modes onto sandbox ownership:

| Graph permission | Codex sandbox |
| --- | --- |
| `plan` | `read-only` |
| `acceptEdits`, `auto`, `dontAsk`, `manual`, or the default | `workspace-write` |
| `bypassPermissions` | `danger-full-access` |

Every Codex call fixes `approval_policy="never"`, because oh-my-graph is a
non-interactive scheduler and an approval prompt would hang a node. For an
auto-planned node or isolated assessor, the existing non-nil
`ToolPolicy.SettingSources` trigger owns isolation: Codex adds
`--ignore-user-config`, `--ignore-rules`, `project_doc_max_bytes=0`, and
`mcp_servers={}`. Hand-written graphs and the planner retain their configured
Codex context, matching the existing reviewed-graph/planner distinction.

Codex cannot reproduce Claude's scoped `allowed_tools` contract. Its sandbox
is the enforceable boundary and this limitation is printed and documented.
Claude agent mapping and Claude skill activation are not attempted for Codex
auto runs.

### Unsupported fields fail before node execution

A Codex graph containing positive `budget_usd` or non-empty `agent:` is
rejected during runtime preflight. No runner-side fallback drops either field.
`--max-goal-budget-usd` is likewise rejected for Codex before planning because
the runtime reports no USD value. The planner contract already excludes both
fields, but trusted preflight remains the contract.

### Honest accounting

Codex outcomes carry input, cached-input, output, and reasoning-output token
counts and mark USD cost unknown. Ledger, snapshot, events, `watch`, `show`, `runs`, and
the live view preserve that distinction. They may show a known Claude subtotal
beside unknown Codex spend, but never render Codex as `$0.0000`. Budgets are
never evaluated against an unknown cost.

Both persisted consumer contracts move to schema 3. An older reader ignoring
`cost_unknown` would turn unknown USD into a known zero, and an older snapshot
reader would resume with the wrong runtime; those are semantic
misinterpretations, not safely additive fields.

### Authentication environment

The shared child environment scrub removes both providers' API-key switches:
Claude's `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` and Codex's
`OPENAI_API_KEY` / `CODEX_API_KEY`. Both CLIs therefore use their saved login
by default. Prefixes and values containing those names remain untouched.

## Consequences

- Claude remains the no-flag compatibility path.
- Codex is available to hand-written graphs, auto planning/execution, chat,
  resume, and browser gate resumes without adding a fifth exec seam.
- A run has a durable runtime identity and token accounting can be honest even
  when a provider does not report USD.
- Codex auto mode has a coarser capability boundary than Claude auto mode;
  sandbox isolation replaces scoped tool-name grants and the CLI says so.
- `agent:` and USD budget fields remain valid graph syntax because Claude uses
  them; runtime preflight, not duplicated schemas, owns compatibility.
- **ADR 0009's session-limit pause does not apply.** It is a promise of the
  Claude runtime rather than of the engine — settled 2026-08-15, closing #171,
  and written into ADR 0009's own Decision. Its detection matches Claude's
  prose, so under `--runtime codex` a session limit is an ordinary node failure
  carrying the provider's message, salvageable with `resume --retry-failed`.
  **A new runtime therefore does not owe a session-limit signal.** The absence
  is disclosed before the run spends, not discovered in the ledger afterwards.
