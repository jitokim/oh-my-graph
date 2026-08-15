---
description: Run an oh-my-graph DAG workflow (run a graph, or auto-plan one from a goal) and report the run ledger.
argument-hint: 'run <graph.yaml> --input key=value ...  |  auto "<goal>"'
allowed-tools: Bash(oh-my-graph run *), Bash(oh-my-graph auto *)
---

Run `oh-my-graph $ARGUMENTS` via Bash. The two subcommands are:

- `run <graph.yaml> [--input key=value ...]` — execute an existing graph file.
- `auto "<goal>"` — let the coordinator plan a graph from the goal, then run it.

Nodes run on the user's saved Claude login. oh-my-graph's other runtime is
selected by a global `--runtime codex` placed BEFORE the subcommand, which this
command's grants do not cover — if the user asks for a Codex run, say that it
has to be started from a shell (`oh-my-graph --runtime codex run <graph.yaml>`)
and do not try to work around the grants.

When it finishes, report the run ledger back to the user: one line per node
(node id, verdict, session id, cost, detail) and the total cost. If a node
failed, surface its failure reason. For `auto`, also show the planned graph
(node ids and their dependencies) before the ledger so the user can see what
was generated. If `oh-my-graph` is not found on `$PATH`, tell the user to
install it — see `plugin/README.md` for install options — and stop; do not
try to reimplement graph execution yourself.
