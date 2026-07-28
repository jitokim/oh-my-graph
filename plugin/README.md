# oh-my-graph (Claude Code plugin)

A thin Claude Code plugin wrapper around the [`oh-my-graph`](../README.md) CLI.
It reimplements **no** graph logic — it just tells Claude to run the
`oh-my-graph` binary via Bash and report back the run ledger. The CLI is the
product; this plugin is a convenience surface for people who'd rather stay in
a Claude Code session than switch to a shell.

## What's in here

- `commands/graph.md` — the `/graph` slash command. This is the primary
  surface: you deliberately trigger it, e.g. `/graph run graphs/dev-review-pr.yaml --input repo=.`
- `skills/run-graph/SKILL.md` — an optional, description-routed skill so
  Claude can also reach for `oh-my-graph` on a natural-language request
  ("run this graph for me") without you typing the slash command.

Both are scoped to `allowed-tools: Bash(oh-my-graph run *)`, so any
`oh-my-graph run ...` invocation runs without a per-use permission prompt, but
nothing outside that command prefix is granted.

## `/graph` invocation UX

What you type in a Claude Code session:

```
/graph run graphs/dev-review-pr.yaml --input repo=.
```

What Claude runs via Bash (verbatim, `$ARGUMENTS` substituted):

```sh
oh-my-graph run graphs/dev-review-pr.yaml --input repo=.
```

Claude then reports the run ledger (session id, cost, verdict, duration per
node, plus total cost) back to you in the session.

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

## A note on nesting: Claude running Claude

`/graph` shells out to `oh-my-graph`, which itself spawns child `claude -p`
subprocesses — one per graph node. That means a Claude Code session can end up
driving a tool that drives more Claude sessions.

This is safe and intentional, not recursion gone wrong:

- Each child `claude -p` subprocess is a fully isolated session with its own
  auth. `oh-my-graph` starts every child from a copy of the environment with
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` deleted (see the main
  [README](../README.md#bring-your-own-login) and
  [SECURITY.md](../SECURITY.md)), so there's no metered-billing leak and no
  auth conflict between parent and children.
- Child sessions do **not** inherit the parent Claude Code session's
  conversation, context, or open files. Each node's `prompt` in the graph YAML
  is the entire context it gets, plus whatever `handoff: artifact` or
  `handoff: session` wires in from its declared parent node. This is by
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
