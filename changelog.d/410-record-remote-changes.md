### Added

- **`run`, `auto` and `resume` record what changed on the remote during each
  leg.** This is the record-only common part of the proposed ADR 0046: the
  engine snapshots the invocation repository's remotes and remote branch heads
  before and after each leg, stores the difference as `remote_changes` in
  `state.json` and prints it after the ledger table, running ssh in BatchMode
  unless you chose an ssh command, so a remote that would prompt is recorded
  `unavailable` instead. It never changes a verdict or an exit code.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
