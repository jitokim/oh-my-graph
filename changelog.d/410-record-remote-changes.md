### Added

- **`run`, `auto` and `resume` now record what changed on the remote during
  each leg.** This is the record-only common part of the proposed ADR 0046:
  the engine snapshots the invocation repository's configured remotes and
  remote branch heads before each leg's first node and after its last, stores
  the difference as `remote_changes` (with `run_base_sha`) in `state.json`,
  schema still 3, and prints it after the ledger table. Each branch entry
  whose new commit is reachable from the run's own branches and not from the
  run's base commit is labelled "observed during the run; points at a commit
  this run made"; every other entry, and every change made between legs, is
  "observed, not attributable". A part that could not be read is recorded as
  unavailable and printed as not compared. The record never changes a
  verdict or an exit code.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
