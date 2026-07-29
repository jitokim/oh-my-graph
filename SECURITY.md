# Security & Terms-of-Service stance

oh-my-graph is a **personal, local** tool that re-uses **your own** logged-in
`claude` session. This document states, honestly, what that means and where the
line is.

## What oh-my-graph is

- A subprocess scheduler that runs each DAG node as `claude -p ...` on the
  machine you run it from, under the account **you** are already logged into.
- The same standing as running `claude -p` yourself, or as tools like
  [claude-squad](https://github.com/smtg-ai/claude-squad): it drives a CLI you
  have already authenticated.

## What oh-my-graph is NOT

- **Not** a hosted or redistributed product that authenticates other people via
  subscription OAuth. Doing that would violate Anthropic's Terms of Service.
- **Not** a shared service. It never runs as a daemon serving other users.
- It **never ships credentials**, **never proxies auth**, and **never** stores or
  transmits your tokens.

## Subscription-auth guarantees (enforced in code)

- **API-key scrub.** Every node subprocess starts from your environment with
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted**. Those variables
  silently switch `claude` from your subscription (OAuth) to metered API
  billing. The scrub is asserted by a unit test
  (`internal/runner/claude_test.go`) that sets both variables in the parent
  process and proves neither survives into the built child command.
- **Never `--bare`.** That flag disables OAuth; oh-my-graph never passes it.
- **Never the Agent SDK / API.** The node runtime is exclusively the `claude`
  CLI subprocess.

## Least privilege per node

- Each node declares its own `allowed_tools` and `permission_mode`. Grant only
  what a node needs.
- **`allowed_tools` is a declaration, not a sandbox.** It is passed to the CLI as
  `--allowedTools`, which is *unioned* with the permissions your own
  `~/.claude/settings.json` already grants — it can never shrink them. If your
  settings carry a standing grant like `Bash(*)` or `Write(*)`, a node has it
  regardless of what the graph declares. For hand-written graphs this is by
  design: the graph is your own reviewed artifact and your settings are the
  intended policy.
- `permission_mode: bypassPermissions` is **opt-in per node** and prints a loud
  warning at load time. It is never a graph default. Parallel nodes that share a
  working directory should stay read-only (`permission_mode: plan`) to avoid
  racing edits.

## Auto-planned graphs (`oh-my-graph auto`)

A planned graph is untrusted LLM output executed unattended, so it gets bounds a
hand-written graph does not. Beyond the plan-time rejections (no
`bypassPermissions`, no `cwd`, no tool outside a fixed allowlist), auto mode
passes each node an explicit `--disallowedTools` ceiling — a deny beats a prior
allow, so it is the only part that binds at runtime.

Stated honestly, this is a reduction and not a sandbox. Known gaps, also
recorded in DESIGN.md and in `coordinator.deniableTools`:

- a node declaring any scoped `Bash(...)` pattern keeps the **whole** `Bash`
  tool, because a deny cannot express "all Bash except these prefixes";
- the deny list enumerates built-in tool names, so `mcp__<server>__<tool>` for
  any MCP server you have configured is **not** covered;
- settings *hooks* are not tool calls and no deny reaches them, so a
  write-capable node can still drop a `.claude/settings.local.json` in the
  invocation directory for a later node or future run to load.

Treat `auto` as you would any unattended agent: run it in a directory you are
willing to have modified, not with a broad standing grant you rely on elsewhere.

## Reporting

This is a young project. If you find a security issue, please open an issue
describing it (omit any secrets) so it can be triaged in the open.
