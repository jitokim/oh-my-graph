### Added

- **The remote change record now covers the invocation repository's open
  pull requests.** Each snapshot, before and after every leg of `run`, `auto`
  and `resume`, also reads `gh pr list --state open --limit 1000` through a
  fifth exec seam, `remote.GHRunner` (ADR 0046), and the record gains
  `pr_opened`, `pr_updated` (the head sha moved; a comment, label or
  `updatedAt` alone is no change) and `pr_no_longer_open`. A PR opened or
  updated at a commit this run made reads "observed during the run; points at
  a commit this run made"; every other PR entry reads "observed, not
  attributable". With `gh` missing or not authenticated, a timeout (30 s), or
  exactly 1000 open PRs (`truncated`), the part is recorded `unavailable` and
  printed as `not compared: pull_requests: …`, never as an empty list, or
  when the repo gh read cannot be told or is not the one expected (`repo`,
  `repo_source`). Still a report only: no verdict or exit code changes, and
  the state and feed schemas stay 3.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
