### Fixed

- **Without `--verify-cmd`, a done-list command nothing can run now fails
  the final check instead of passing it.** When `auto` runs without
  `--verify-cmd`, the planner prompt now tells the final check node to reply
  FAIL, naming any command in the goal's done list (e.g. `npm test`) that no
  allowed Bash pattern covers, instead of letting the check PASS over a done
  item nothing ran. A covered command is run once, and its PASS requires
  exit 0. ([#399](https://github.com/jitokim/oh-my-graph/issues/399))
