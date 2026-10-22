### Changed

- **With `--verify-cmd`, the planner is told to end the graph in a single
  sink.** The engine runs the verify command at every sink node, so a plan
  ending in two sinks ran the whole suite twice per cycle, while the
  planner prompt claimed it ran once after the final node. The prompt now
  asks for exactly one sink, the final check node, with every other node
  its ancestor, and says the command runs at each sink node — once, after
  that final check node. How the command is attached is unchanged, and
  without `--verify-cmd` the prompt is unchanged.
  ([#396](https://github.com/jitokim/oh-my-graph/issues/396))
