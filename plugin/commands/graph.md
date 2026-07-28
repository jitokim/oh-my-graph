---
description: Run an oh-my-graph DAG workflow and report the run ledger.
argument-hint: "run <graph.yaml> --input key=value ..."
allowed-tools: Bash(oh-my-graph run *)
---

Run `oh-my-graph $ARGUMENTS` via Bash.

When it finishes, report the run ledger back to the user: one line per node
(node id, session id, cost, verdict, duration) and the total cost. If a node
failed, surface its failure reason. If `oh-my-graph` is not found on `$PATH`,
tell the user to install it — see `plugin/README.md` for install options —
and stop; do not try to reimplement graph execution yourself.
