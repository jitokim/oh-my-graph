### Added

- **`run` and `auto` take `--forbid-remote-changes`, which fails a run that
  leaves new state on the remote (exit 6).** With the flag, a leg whose own
  remote change record holds a `branch_created` or `pr_opened` labelled
  `run_commit` (a new branch or PR at a commit this run made), or a
  `remote_added`, ends with `new remote state observed during the run, and
  --forbid-remote-changes was set: …` on stderr and exit 6, whatever its nodes
  did: a node failure is reported with it, and `--continue-on-fail` cannot
  prune it. Nothing else fails it: `branch_moved` and `pr_updated` (a push
  onto an existing branch, `main` included) whatever their label,
  `remote_removed`, `remote_url_changed`, `branch_deleted`,
  `pr_no_longer_open` and every between-legs entry are reported only, as
  without the flag (ADR 0046). A leg that paused at a gate or on the session
  limit while holding a forbidden entry fails too: the pause is withdrawn, no
  pause hint is printed, and it continues with `resume --retry-failed`. Such
  a run reads `FAIL` in `runs list`, `show` and the live views even with every
  node `PASS`, because on a guarded run a closed leg whose outcome is
  `"failed"` reads `FAIL` before its node count. ADR 0023 §2.6's agreement is
  now exit 1 or 6 ↔ `FAIL`. Unguarded statuses are unchanged: a Ctrl-C during
  a passed leg's end remote snapshot still reads `PASS` while it exits 1. The flag is recorded as
  `forbid_remote_changes` and `resume` guards every later leg from it; a
  guarded `state.json` is stamped schema 5 (with or without conventions), so
  an older binary refuses to resume it rather than drop the guard, while an
  unguarded one stays 3 (4 with conventions). A guarded leg that cannot read a
  part of the remote still starts: it names the part at launch and again
  after the change record (`--forbid-remote-changes: not guarded …`, plus
  `PR part not guarded: <reason>` for the PR part) and judges only the parts
  it compared. In a goal loop a guarded failure is a failed cycle. Without the
  flag nothing else changes.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
