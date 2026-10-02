### Fixed

- **The two shipped `dontAsk` fragments now tell their node which commands it
  holds.** Under `dontAsk` a call outside the grant is denied, not asked about.
  A node that did not know its grant gave up at the first denial instead of
  using a command it held. `repair-round`'s apply never tried `make`.
  `pr-publish`'s node found no permitted way to pass a PR body. Both prompts
  now state the grant and the exact command for the job. The apply runs
  `make` and is told to run its own evidence command (`verify_command`), so
  bind that to a `make` target. Commits use one-line `-m` flags. Every call is
  one program, `git -C`/`make -C` replaces `cd`, and a denied call is
  re-issued in a permitted form. The pr node writes the body to
  `.omg-pr-body.md` and runs
  `gh pr create --title '…' --body-file .omg-pr-body.md`. It is told never to
  commit that file. **`pr-publish`'s grant gains one rule**,
  `Edit(./.omg-pr-body.md)`, which permits writing that one path and nothing
  else. Without it no body could get through: an inline multi-line `--body`
  is denied even under a matching `gh` grant, a heredoc or `$(…)` is
  compound, and `.git/` is a file in a linked worktree. The rule's spelling
  follows Claude Code's documented syntax and has not been measured here. It
  reaches every `pr-publish` user: `self-dev`, `dev-review-pr` and
  `backlog-batch` ×2. Once the PR is open the node deletes the body file with
  `git clean -f -- .omg-pr-body.md`. If the file were left untracked,
  `git worktree remove` would refuse at run end, and every published
  `backlog-batch` lane would stay on disk as if it held uncommitted work.
  DESIGN.md records why the engine does not
  append a node's grant to its prompt generically.
  ([#294](https://github.com/jitokim/oh-my-graph/issues/294))
