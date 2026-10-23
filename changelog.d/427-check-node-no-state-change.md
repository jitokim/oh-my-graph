### Changed

- **`auto`'s check nodes no longer change repository state.** A planned check
  node observes and judges: it runs no `git checkout`, `switch`, `reset`,
  `restore`, `stash`, `commit`, `merge` or `rebase`, creates or deletes no
  branch and creates no tag, so it never moves HEAD in a checkout other people
  or runs may be using. It reads another ref with `git show <ref>:<path>` or
  `git diff <a>..<b>`. A command that would need another ref checked out to
  run belongs to the node that made the work or to `--verify-cmd`; the check
  node runs its own commands, done-list ones included, in the tree as it
  finds it. Planner prompt only, with and without `--verify-cmd`. ([#427](https://github.com/jitokim/oh-my-graph/issues/427))
