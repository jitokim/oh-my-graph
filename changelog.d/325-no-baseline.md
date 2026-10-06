### Added

- `auto --no-baseline` skips the starting-tree baseline for a `--verify-cmd` that is an acceptance test of the goal, red before the change on purpose; every sink still runs the command. It requires `--verify-cmd`, and the "baseline red" message names it. ([#325](https://github.com/jitokim/oh-my-graph/issues/325))
