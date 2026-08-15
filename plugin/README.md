# oh-my-graph (Claude Code plugin)

A thin Claude Code plugin wrapper around the [`oh-my-graph`](../README.md) CLI.
It reimplements **no** graph logic — it just tells Claude to run the
`oh-my-graph` binary via Bash and report back the run ledger. The CLI is the
product; this plugin is a convenience surface for people who'd rather stay in
a Claude Code session than switch to a shell.

## What's in here

- `commands/graph.md` — the `/graph` slash command. This is the primary
  surface: you deliberately trigger it, either to run an existing graph, e.g.
  `/graph run graphs/dev-review-pr.yaml --input repo=.`, or to auto-plan one
  from a goal, e.g. `/graph auto "add a Platform support section to the README"`.
- `skills/run-graph/SKILL.md` — an optional, description-routed skill so
  Claude can also reach for `oh-my-graph` on a natural-language request
  ("run this graph for me") without you typing the slash command.
- `agents/oh-my-graph.md` — a graph-engineering **agent**. Launch a whole
  Claude Code session as this agent with `claude --agent oh-my-graph` and
  every turn is graph-aware — no `/oh-my-graph:graph` prefix needed. See
  [the agent section](#the-oh-my-graph-agent-ambient-entry-point) below;
  **prefer this over typing the slash command** when you're doing more than
  one graph operation.

The command is scoped to `allowed-tools: Bash(oh-my-graph run *), Bash(oh-my-graph auto *)`,
so any `oh-my-graph run ...` or `oh-my-graph auto ...` invocation runs without a
per-use permission prompt, but nothing outside those command prefixes is granted.

## Which entry point can reach the second runtime

oh-my-graph v0.8.0 added a second node runtime, selected with a global
`--runtime codex` that must precede the subcommand. **The two prefix-granted
surfaces do not pre-grant it:** `/graph` and the `run-graph` skill are scoped to
the command prefixes `oh-my-graph run ` and `oh-my-graph auto `, and
`oh-my-graph --runtime codex run ...` begins with neither, so it raises a
per-use permission prompt instead of running unprompted — what happens at that
prompt is then up to your own session permission rules, which is where a
standing `Bash(*)` would still match. There is no other spelling to fall back on
— the flag is rejected after the subcommand (`oh-my-graph run g.yaml --runtime
codex` exits with `flag provided but not defined: -runtime`). Through those two
surfaces, a node starts as a `claude` subprocess on your saved Claude login,
which is what the rest of this file describes.

**The agent is the exception.** `agents/oh-my-graph.md` names
`Bash(oh-my-graph *)` in its `tools` list, a pattern that does cover
`oh-my-graph --runtime codex run ...` — and that field does not restrict which
commands run in the first place (see the **Plugin-agent limits** bullet below).
So the agent — the surface this file recommends above — can reach Codex, and its
prompt says what to read before a Codex run rather than pretending the option
isn't there.

**The command grants are deliberately not widened, and that is a design note
rather than a limitation to fix.** A prefix grant matches literal argv, so
covering the flag means enumerating its spellings — `--runtime codex run`,
`--runtime=codex run`, and the same two for `auto` and for `claude` — six
entries to express one boolean, and the enumeration multiplies the day a second
global flag appears. A grant list nobody can read at a glance is worse than a
missing convenience, especially for the one list standing between a session and
an unprompted spend. Run Codex graphs from a shell (`oh-my-graph --runtime codex
run graph.yaml`); what `--runtime codex` changes about a run is in
[docs/EXAMPLES.md](../docs/EXAMPLES.md#what---runtime-codex-changes).

## `/graph` invocation UX

> **Namespacing:** when the plugin is installed via a marketplace, Claude Code
> namespaces its commands by plugin name, so the command is `/oh-my-graph:graph`,
> not a bare `/graph` (type `/oh-my-graph:` and let autocomplete fill it in). A
> bare `/graph` only works if some other source provides that exact name. The
> examples below use the short form for readability.

What you type in a Claude Code session:

```
/graph run graphs/dev-review-pr.yaml --input repo=.
/graph auto "add a Platform support section to the README"
```

What Claude runs via Bash (verbatim, `$ARGUMENTS` substituted):

```sh
oh-my-graph run graphs/dev-review-pr.yaml --input repo=.
oh-my-graph auto "add a Platform support section to the README"
```

Claude then reports the run ledger (session id, cost, verdict, detail per
node, plus total cost) back to you in the session. For `auto`, it also shows
the planned graph before the ledger.

## Prerequisite: `oh-my-graph` on `$PATH`

The plugin does not bundle the graph engine. Install it once:

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest
```

or, from a checkout of this repo:

```sh
make build   # from the repo root; binary lands in bin/oh-my-graph
```

**Alternative — bundle the binary in the plugin instead:** Claude Code adds
any executables in `plugin/bin/` to the Bash tool's `PATH` while the plugin is
enabled, so you could `make build` and copy the binary to `plugin/bin/` before
distributing the plugin, and skip the separate install step entirely. This
repo defaults to the plain `$PATH` assumption above since oh-my-graph is a
personal tool you're expected to already have installed — but bundling is a
reasonable option if you want a truly self-contained plugin.

## Installing the plugin

### Option A — dev/test (no marketplace)

From the repo root:

```sh
claude --plugin-dir ./plugin
```

This loads the plugin directly for the current session — the fastest way to
iterate while working on the plugin itself.

### Option B — local marketplace

This repo's root also carries `.claude-plugin/marketplace.json`, which lists
this plugin with `"source": "./plugin"`. From inside a Claude Code session:

```
/plugin marketplace add /path/to/oh-my-graph
/plugin install oh-my-graph@oh-my-graph-marketplace
```

(`oh-my-graph-marketplace` is the marketplace's `name` field, not the repo or
directory name — that's what `/plugin install <plugin>@<marketplace>` expects
after the `@`.) This is closer to how you'd actually keep the plugin enabled
across sessions.

## The oh-my-graph agent (ambient entry point)

The `/oh-my-graph:graph` command is per-turn: you type it every time you want
a graph operation. The plugin also ships an **agent**
(`agents/oh-my-graph.md`) that makes graph engineering the session's ambient
mode instead: launch Claude Code with it as the main agent and every turn
already knows the `oh-my-graph` CLI (run/auto/chat/resume/runs/show/watch/
lint, `run --dry-run`), the worktree/handoff/gate model, and the habit of
linting and dry-running YAML before spending money on real nodes. Prefer this
over typing `/oh-my-graph:graph` each turn.

### Setup

1. Install the plugin (either option in
   [Installing the plugin](#installing-the-plugin) above).
2. Add a one-word shell function to your shell rc (`~/.zshrc` / `~/.bashrc`):

   ```sh
   omg () { claude --agent oh-my-graph "$@"; }
   ```

   Mirroring the common `team () { claude --agent team ... }` pattern — add
   `--dangerously-skip-permissions` inside the function if that's how you
   run your own agents.
3. Type `omg` in a terminal. The startup header shows `@oh-my-graph` to
   confirm the agent is active.

### What the docs confirm (verified against code.claude.com/docs)

- **Plugins ship agents.** An `agents/` directory at the plugin root is a
  first-class plugin component, exactly like `commands/` and `skills/`.
- **`--agent` takes the bare name.** For a plugin-provided agent,
  `claude --agent oh-my-graph` works as-is — Claude Code finds it. The
  plugin-scoped form `claude --agent oh-my-graph:oh-my-graph`
  (`<plugin>:<agent>`) is only needed if *another* plugin ships an agent
  that is also named `oh-my-graph`.
- **Your normal config still loads.** `--agent` replaces only the default
  system prompt with the agent's prompt. `CLAUDE.md` files (user and
  project scope) and project memory still load normally, and the session
  can still invoke your project/user/plugin skills through the Skill tool.
  You keep Claude Code's TUI, streaming, and permission prompts for free.
- **Plugin-agent limits.** For security, plugin agents ignore the `hooks`,
  `mcpServers`, and `permissionMode` frontmatter fields; this agent uses
  none of them. Its `tools` list names the tools it works with
  (`Bash`, file read/edit/write and search, `Skill`, `Agent`).
  **Command-level scoping is NOT enforced by this field, though:** a
  smoke test showed the parenthesized form (`Bash(oh-my-graph *)`) launches
  fine but does not actually restrict which shell commands run — the agent
  could run an out-of-list `echo`. The parenthesized entries are kept as a
  statement of intent; real command restriction must come from your
  session-level permission rules (`--allowedTools` / settings), not the
  agent's `tools` field.

### Already have your own agents? Nothing conflicts

`--agent` selects one main agent **per session** — it is a launch-time flag,
not a global setting. Adding this plugin therefore only *adds* an entry
point:

- Your existing agents and shell functions (e.g.
  `team () { claude --agent team ... }`) are untouched; `team` still starts
  a `team` session, `omg` starts an oh-my-graph session, and plain `claude`
  still starts a default session.
- Name collisions are disambiguable: plugin agents also answer to the
  plugin-scoped form `claude --agent oh-my-graph:oh-my-graph`. If you ever
  *did* have your own agent named `oh-my-graph` in `.claude/agents/` or
  `~/.claude/agents/`, use that scoped form to select the plugin's
  explicitly. (Which one a bare `--agent oh-my-graph` resolves to on a
  collision — local or plugin — is not something we've verified; the scoped
  form removes the ambiguity.)
- A session whose main agent is oh-my-graph can still **delegate** to your
  other subagents: a main agent launched via `--agent` keeps the Agent
  tool, so all your project- and user-scope subagents remain available as
  delegation targets inside the `omg` session.

## A note on nesting: Claude running Claude

`/graph` shells out to `oh-my-graph`, which itself spawns child `claude -p`
subprocesses — one per graph node. That means a Claude Code session can end up
driving a tool that drives more Claude sessions.

This is safe and intentional, not recursion gone wrong:

- Each child `claude -p` subprocess is a fully isolated session with its own
  auth. `oh-my-graph` starts every child from a copy of the environment with
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY` and
  `CODEX_API_KEY` deleted (see the main
  [README](../README.md#bring-your-own-login) and
  [SECURITY.md](../SECURITY.md)), so there's no metered-billing leak and no
  auth conflict between parent and children.
- Child sessions do **not** inherit the parent Claude Code session's
  conversation, context, or open files. Each node's `prompt` in the graph YAML
  is the entire context it gets, plus whatever `handoff` wires in from its
  declared parent node — the parent's final reply with `handoff: artifact`,
  or the parent's resumed session (its conversation — not its tool grants,
  permission mode or cwd, which are always the child's own) with
  `handoff: session`. This is by
  design — it's what keeps node runs reproducible and inspectable — but it
  means "the graph knows what I'm looking at in this Claude Code session" is
  never true unless you put it in the graph's inputs or prompts yourself.

## Version dependence

This plugin was built against the Claude Code plugin system as documented at
the time of writing (`plugin.json` / `marketplace.json` schema, `/plugin`
subcommands, `commands/*.md` frontmatter, and the `plugin/bin/` → `$PATH`
behavior). If a future Claude Code release changes plugin manifest fields or
`/plugin` subcommand syntax, re-check against the current
[Claude Code plugin docs](https://code.claude.com/docs) before relying on the
exact commands above.
