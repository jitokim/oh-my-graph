### Fixed

- **A `dontAsk` node granted only scoped Bash rules no longer has its own
  compound commands denied.** Under `dontAsk` a rule such as `Bash(gh *)`
  admits a command only when the whole command plainly matches it, so a
  node's `gh pr ready 379; echo "exit=$?"` was denied though it held `gh`.
  Such a node (a scoped Bash rule, and no bare `Bash` or `Bash(*)`) is now
  started with `--append-system-prompt`, telling it to run one command per
  Bash call: no `;`, `&&`, `||`, pipes, `$(...)`, backticks or redirections,
  `git -C <dir>` rather than `cd`, no `; echo $?`, and to report a denied
  command rather than work around it. A resumed invocation carries it too.
  The `-p` prompt is unchanged, and every other node's argv is byte-for-byte
  what it was. ([#380](https://github.com/jitokim/oh-my-graph/issues/380))
