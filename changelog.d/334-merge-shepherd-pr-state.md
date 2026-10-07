### Fixed

- **`merge-shepherd` no longer ends green over a merge that never happened.**
  When the permission mode denied `gh pr merge`, `merge` answered `WITHHELD`,
  its pattern accepted it, and the run exited 0 with the PR still open. The
  `merge` node now also carries a `success_check.verify` that reads
  `recheck`'s recorded verdict and the PR's live state from `gh pr view`:
  after `RECHECKED <sha>` it passes only if the PR is merged at exactly that
  40-hex SHA, and after `UNSETTLED` only if the PR is still open. A gh
  failure, unexpected output or a queued auto-merge fails. A `WITHHELD` after
  a `RECHECKED` recheck (a denied merge, a refused `--admin`) now fails the
  run; `oh-my-graph resume <run-id> --retry-failed` re-runs `merge`, and its
  step 0 makes that safe. A green run now means the PR landed at the SHA
  `recheck` judged, or was deliberately left open after an `UNSETTLED`
  recheck. ([#334](https://github.com/jitokim/oh-my-graph/issues/334))
