### Fixed

- **A run of `auto --interview` whose plan is refused now records the
  interview's cost in its planning cost.** Before, the run's record showed
  only the planner calls and under-reported what planning spent. As with an
  accepted plan, a goal loop counts the interview once, on cycle 1, and
  `--plan-only` still prints the interview's cost and the planner's separately.
  ([#322](https://github.com/jitokim/oh-my-graph/issues/322))
