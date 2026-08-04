---
name: run-graph
description: Use when the user asks to run, execute, or kick off an oh-my-graph DAG/graph workflow (a graph.yaml file) from within a Claude Code session, instead of dropping to a separate shell. Triggers on "run this graph", "run <name>.yaml with oh-my-graph", "kick off the graph workflow".
disable-model-invocation: false
allowed-tools: Bash(oh-my-graph run *)
---

# Run an oh-my-graph workflow

oh-my-graph is a standalone Go CLI that runs a DAG of `claude -p` subprocess
nodes on the user's own Claude subscription. This skill is a thin wrapper: it
does not reimplement any graph logic, it just invokes the binary.

## Steps

1. Confirm the graph file path and any `--input k=v` values with the user if
   not already given.
2. Run via Bash: `oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]`
3. Report the run ledger from stdout back to the user: one row per node — its
   five printed columns are node id, verdict, session id, cost, and a short
   detail — plus the total cost.
4. If `oh-my-graph` is not found on `$PATH`, tell the user to install it (see
   `plugin/README.md`) and stop — do not attempt to run the graph nodes
   yourself in-session.

The same behavior is also available as the `/graph` slash command
(`plugin/commands/graph.md`) for users who prefer to trigger it explicitly
rather than rely on description-based routing.
