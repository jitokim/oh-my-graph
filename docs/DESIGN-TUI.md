# DESIGN-TUI — a standalone oh-my-graph TUI (experiment)

> **Status: experiment on the long-lived `feat/tui` branch. Not merged to main.**
> This is a design sketch, not a committed feature. We accumulate the experiment
> here and decide later whether to merge (whole, in pieces, or not at all).

## Why this branch exists

Two entry points already exist and are cheaper than a TUI:

- **The plugin `/oh-my-graph:graph`** — a slash command inside a Claude Code
  session.
- **The `omg` agent** (`plugin/agents/oh-my-graph.md`) — `omg () { claude
  --agent oh-my-graph; }`, a graph-aware Claude Code session that inherits the
  user's whole `.claude` setup and Claude Code's TUI/streaming/permissions for
  free. This is the low-friction ambient entry point as of #57.

So the honest first question for a standalone TUI is **not "how do we build it"
but "what gap does it fill that `omg` does not?"** If the answer is "none," this
branch stays an experiment.

## The candidate gap

`omg` gives you a *conversation* that can drive the CLI. What it does **not**
give you is a **spatial, always-on view of a graph** — the DAG as a shape you
edit and watch, not a transcript you scroll. Concretely a TUI could own:

1. **Authoring** — a node/edge editor for graph YAML with live validation
   (reusing `graph.Lint` / `--dry-run`), instead of hand-editing YAML in a
   buffer.
2. **Running + watching in one place** — launch a run and see the live DAG
   (node states, the ready-set frontier, per-node cost) update in place,
   consuming the run-feed contract (`~/.oh-my-graph/runs/<id>/events.jsonl`,
   the same stream `watch` tails and fleetops will render).
3. **History** — browse past runs (`runs list` / `show` data) as a navigable
   pane, not a printed table.

That is a different mode of interaction from a chat transcript. Whether it earns
its cost is the thing this branch is meant to find out.

## Boundary with fleetops (must not blur)

fleetops is the **observer** — it renders fleet/session state and (soon) the run
DAG by consuming the run-feed. oh-my-graph is the **executor**. A standalone
oh-my-graph TUI must not become a second observer:

- The TUI's job is **authoring + launching + driving** graphs (the executor's
  own cockpit), reusing the run-feed as a *local* live view of *the run it just
  started*.
- Long-lived, cross-run, multi-tool observation stays fleetops's job.
- Both consume the same versioned run-feed contract; neither reimplements the
  other. If a feature could belong to either, it belongs to fleetops.

## Stack

Bubble Tea + Bubbles + Lip Gloss, pinned to the same versions fleetops uses
(bubbletea v1.3.10, bubbles v1.0.0, lipgloss v1.1.0) so the two siblings share a
look and idioms. Terminal DAG layout has no mature Go library, so the "graph
view" is a layered/topological node-status list with status glyphs — the same
honest constraint recorded in the visibility roadmap, not a real flowchart.

## What inheriting "the user's claude setup" means here

The user's imagined pitch was "a TUI that inherits my claude skills/tools/prompt
files." Note that **each graph node is already `claude -p`, so it already
inherits the user's `.claude` environment** (agents, skills, CLAUDE.md — see the
v0.3 environment-inheritance research). The TUI does not need to re-implement
that inheritance; it gets it for free through the node runtime. The TUI's value
is the *spatial graph cockpit*, not the inheritance (which is already solved).

## Open questions (decide before building much)

- **Is the authoring editor worth it, or is `omg` + an editor + `lint` enough?**
  The cheapest test: try living on `omg` for real graph work first, and only
  build the TUI pane whose absence actually hurt.
- **Launch model:** does the TUI shell out to the `oh-my-graph` binary (thin,
  consistent with the plugin's philosophy) or link the engine as a library?
  Leaning shell-out, to keep one execution path.
- **Where does it live?** `cmd/oh-my-graph-tui/` (separate binary) vs. an
  `oh-my-graph tui` subcommand. Separate binary keeps the core CLI dependency
  from pulling in Bubble Tea.

## Plan (incremental, on this branch)

1. This design note (here).
2. Live-on-`omg` shakedown — record which missing pane actually hurt.
3. Smallest useful pane first (likely: run-feed live DAG view of a launched
   run — it reuses an existing contract and has the clearest payoff).
4. Authoring editor only if step 2 justifies it.

Nothing here merges to main until a pane proves it earns its cost.
