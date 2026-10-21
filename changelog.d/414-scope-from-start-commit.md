### Fixed

- **`auto`, `design` and `chat` measure a planned node's scope from the commit
  the run started from.** A planned node could check "only these files" against main,
  so on a branch with earlier commits it could flag or revert files the goal
  never touched. All three commands now read the repository's HEAD once at launch
  and tell the planner that scope and changed-files checks use `<sha>..HEAD`,
  that no node reverts a file the goal did not ask it to change, and that
  files changed before that commit are left as they are. Every cycle of a goal
  uses the same commit, and so does every plan of a `chat` session.
  `auto --interview` and `design` read it before the interview starts, so a
  long interview does not move it. No planned check asserts that main has not moved since that
  commit; to show the run's work is not on main, a check asserts that
  `git merge-base --is-ancestor <a commit this run made> main` exits non-zero.
  Outside a git repository the prompt is unchanged.
  ([#414](https://github.com/jitokim/oh-my-graph/issues/414))
