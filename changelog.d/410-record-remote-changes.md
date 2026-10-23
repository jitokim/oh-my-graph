### Added

- **`run`, `auto` and `resume` record what changed on the remote during each
  leg.** This is the record-only common part of the proposed ADR 0046: the
  engine snapshots the invocation repository's remotes and remote branch heads
  before and after each leg, stores the difference as `remote_changes` in
  `state.json` and prints it after the ledger table, running ssh in BatchMode
  unless you chose an ssh command, so a remote that would prompt is recorded
  `unavailable` instead. It never changes a verdict or an exit code. Ctrl-C
  during either snapshot, start or end, ends the leg as interrupted (exit
  1); during the start snapshot it ends the run before any node starts, and
  an interrupted run records its end snapshot as `unavailable`
  ("interrupted"). During the end snapshot only the exit code changes: the
  outcome in `state.json` and `run_finished` stays the one the nodes
  reached, so a leg that reached a gate stays paused and resumable with
  `resume <run-id> --approve|--reject <gate>`, and that hint still prints.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
