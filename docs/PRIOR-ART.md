# Prior art

How oh-my-graph compares to its nearest neighbours. The differentiator in one
line: no existing graph-native orchestrator runs its nodes as the **provider's
own CLI on that CLI's saved login** — `claude` by default, `codex` under
`--runtime codex` ([ADR 0025](adr/0025-one-run-one-cli-runtime.md)) —
they all bill per token through an API.

- **[microsoft/conductor](https://github.com/microsoft/conductor)** — same
  philosophy: the graph is declared in YAML and the LLM is kept out of the
  orchestration loop, only executing nodes. It doesn't run its nodes as a
  provider's own logged-in CLI — that's oh-my-graph's differentiator.
- **[OMK](https://github.com/dmae97/omk)** — the closest sibling. Runs coding
  agents (including Claude Code) in scoped DAG lanes, and verifies node
  success against external evidence rather than the node's own self-report.
  oh-my-graph now has an evidence predicate of its own — `success_check.verify`
  runs a command the engine judges — but it is opt-in per node rather than
  intrinsic to the model, so a graph that doesn't declare one is still passing
  nodes on their own word ([#7](https://github.com/jitokim/oh-my-graph/issues/7)).
- **[open-multi-agent](https://github.com/open-multi-agent/open-multi-agent)**
  — the opposite axis: a coordinator plans the task DAG at runtime from a goal
  description, instead of a human-authored graph. Static, declared graphs
  (oh-my-graph, conductor) win on reproducibility and auditability — the graph
  is a diffable, git-reviewable artifact; runtime-planned graphs win on
  adapting to goals whose procedure isn't known in advance. oh-my-graph's
  `auto` mode partially borrows the latter idea (goal → LLM-planned
  graph) but still runs the result through the same deterministic scheduler —
  a hybrid, not a full runtime-replanning system.
