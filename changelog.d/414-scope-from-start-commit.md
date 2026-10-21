### Fixed

- **`auto` and `design` measure a planned node's scope from the commit the run
  started from.** A planned node could check "only these files" against main,
  so on a branch with earlier commits it could flag or revert files the goal
  never touched. Both commands now read the repository's HEAD once at launch
  and tell the planner that scope and changed-files checks use `<sha>..HEAD`,
  that no node reverts a file the goal did not ask it to change, and that
  files changed before that commit are left as they are. Every cycle of a goal
  uses the same commit. Outside a git repository the prompt is unchanged.
  ([#414](https://github.com/jitokim/oh-my-graph/issues/414))
