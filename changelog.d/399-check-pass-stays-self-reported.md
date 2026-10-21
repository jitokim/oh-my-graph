### Changed

- **Planner prompt: without `--verify-cmd`, a check node's PASS is described
  as self-reported even when it ran a done-list command.** The final-check
  paragraph now says that such a PASS reports what the node observed, not an
  engine verdict. Follow-up to #403.
  ([#399](https://github.com/jitokim/oh-my-graph/issues/399))
