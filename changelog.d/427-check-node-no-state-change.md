### Changed

- **`auto`'s check nodes no longer change repository state.** A planned check
  node observes and judges: it runs no `git checkout`, `switch`, `reset`,
  `restore`, `stash`, `commit`, `merge` or `rebase`, creates or deletes no
  branch and creates no tag, so it never moves HEAD in a checkout other people
  or runs may be using. It reads another ref with `git show <ref>:<path>` or
  `git diff <a>..<b>`, and a build, test or linter that must run against the
  work belongs to the node that made it or to `--verify-cmd`. Planner prompt
  only, with and without `--verify-cmd`. ([#427](https://github.com/jitokim/oh-my-graph/issues/427))
