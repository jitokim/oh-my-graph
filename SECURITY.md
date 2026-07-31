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

- **API-key scrub.** Every child process oh-my-graph spawns — a node
  subprocess, a `success_check.verify` command, the git commands behind a
  node's `worktree:` (a repo's own hooks may invoke claude), and the
  `open`/`xdg-open` launch of the `serve` URL (the URL handler it dispatches
  to is arbitrary user-configured code) — starts from your environment with
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted**. Those variables
  silently switch `claude` from your subscription (OAuth) to metered API
  billing. The scrub is asserted by a unit test at every call site
  (`internal/runner/claude_test.go`, `internal/verify/shell_test.go`,
  `internal/worktree/git_test.go`, `internal/browser/exec_test.go`) that sets
  both variables in the parent process and proves neither survives into the
  built child command.
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
  intended policy. Auto-planned graphs are the exception — see below, where
  `--setting-sources ""` turns the same declaration into a real limit.
- `permission_mode: bypassPermissions` is **opt-in per node** and prints a loud
  warning at load time. It is never a graph default. Parallel nodes that share a
  working directory should stay read-only (`permission_mode: plan`) to avoid
  racing edits.

## Auto-planned graphs (`oh-my-graph auto`)

A planned graph is untrusted LLM output executed unattended, so it gets bounds a
hand-written graph does not. Beyond the plan-time rejections (no
`bypassPermissions`, no `cwd`, no `success_check.verify`, no `agent`, no tool
outside a fixed allowlist), auto mode runs each planned node under a layered
execution ceiling.

`success_check.verify` is refused outright rather than constrained: it is a
shell command the *engine* runs, not a tool call, so no permission mode, tool
allowlist, deny list or `cwd` restriction applies to it. It is available to
hand-written graphs, which are your own reviewed artifact, and to nothing else.

The layers:

| layer | mechanism | closes |
|---|---|---|
| 1 isolation | `--setting-sources ""` | your standing grants; settings hooks |
| 2 grant | `--allowedTools` under `dontAsk` default-deny | **scoped Bash** |
| 3 narrowing | `--tools "<names declared>"` | tools the model can attempt at all |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` | anything the layers above got wrong |

Layer 1 is what makes the rest bind. Permission rules are matched from every
loaded source, so a standing `Bash(*)` in your own `~/.claude/settings.json` was
previously matching before a planned node's narrower `Bash(git *)` ever
mattered. Loading none of your user/project/local settings leaves oh-my-graph's
own argv as the only allow-rule source.

**Measured on claude 2.1.220** (2026-07-29), not inferred from `--help`: with a
settings.json granting `Bash(*)` and a node declaring `Bash(git *)`, an
out-of-scope shell command **ran** without Layer 1 and was **denied** with it,
while in-scope `git` kept working. So the previously-disclosed gap — *"a node
declaring any scoped `Bash(...)` pattern keeps the whole `Bash` tool"* — is
**closed for auto-planned nodes.** It remains accurate for hand-written graphs,
which run without layer 1's isolation: their declared `allowed_tools` is still
rendered as `--allowedTools` (layer 2 applies to every graph), but layers 1 and
3–5 are auto mode's alone by design.

Still a reduction, not a sandbox. What is **not** covered:

- **MCP closure is unverified.** `--strict-mcp-config` is passed because
  oh-my-graph never passes `--mcp-config`, so the flag costs nothing — but this
  was not measured against a real MCP server (DESIGN.md, E5). Do not read Layer
  4 as an observed guarantee.
- **Skill and slash-command surfaces** are still not enumerable by any of these
  mechanisms.
- **Enterprise policy settings are never dropped** by `--setting-sources ""` —
  which is deliberate: this cannot be used to step around a corporate policy.
  Conversely, on a machine with `allowManagedPermissionRulesOnly`,
  `--allowedTools` rules are ignored entirely and the ceiling is the managed
  policy, not ours.
- The ceiling rests on behaviour of a **specific CLI version**. A future claude
  release could change it; Layer 5 is retained precisely so a wrong assumption
  in Layers 1–4 degrades to the older, weaker ceiling rather than to nothing.

**Planned nodes are more isolated and less capable than they used to be.**
Dropping your settings also drops your CLAUDE.md, your hooks and your configured
MCP servers for those nodes. That is the intended direction, but it is a real
behaviour change: if your `auto` runs depended on an MCP server, they will stop.

Re-running a saved `graph.json` through `oh-my-graph run` drops the ceiling
entirely — that path assumes you reviewed the file. Treat `auto` as you would
any unattended agent: run it in a directory you are willing to have modified.

## Subagents (`agent:`)

A hand-written node may run as one of your own Claude Code subagents
(`agent: code-reviewer` → `claude -p --agent code-reviewer`). oh-my-graph does
not parse the subagent's definition and makes **no claim** about how that
subagent's own `tools:` combines with the node's `allowed_tools` — that is the
CLI's precedence, and for this path it is **unmeasured**. (DESIGN.md's E6 probed
a subagent against `--tools`, but `--tools` is emitted only by auto mode, which
forbids `agent:` — so that result does not cover the hand-written case.) If a
subagent grants tools the node did not, assume it gets them.

**Auto-planned nodes may not set `agent:` at all.** Letting an unreviewed plan
pick which of your subagents runs a node would hand it that subagent's system
prompt, tool grant and model, routing around Layers 0–3 in one word. It is
rejected at plan time, and a reflection-driven test over `graph.Node` fails the
build if any future schema field is added without an explicit decision like this
one.

## Reporting

This is a young project. If you find a security issue, please open an issue
describing it (omit any secrets) so it can be triaged in the open.
