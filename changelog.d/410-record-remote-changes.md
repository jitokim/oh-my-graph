### Added

- **`run`, `auto` and `resume` record what changed on the remote during each
  leg.** This is the record-only common part of the proposed ADR 0046: the
  engine snapshots the invocation repository's remotes and remote branch heads
  before and after each leg, stores the difference as `remote_changes` in
  `state.json` and prints it after the ledger table, running ssh in BatchMode
  unless you chose an ssh command, so a remote that would prompt is recorded
  `unavailable` instead. The record itself never changes a verdict or an exit
  code. Ctrl-C during either snapshot, start or end, ends the leg as
  interrupted (exit 1), never as paused at a gate; during the start snapshot
  it ends the run before any node starts, a resumed run staying paused at
  its gate for the same `resume --approve <gate>`, and an interrupted run records its
  end snapshot as `unavailable` ("interrupted") and prints that record.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
