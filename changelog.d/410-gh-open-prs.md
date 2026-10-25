### Added

- **The remote change record now covers the invocation repository's open
  pull requests.** Each snapshot, before and after every leg of `run`, `auto`
  and `resume`, also reads `gh pr list --state open --limit 1000` through a
  fifth exec seam, `remote.GHRunner` (ADR 0046), and the record gains
  `pr_opened`, `pr_updated` (the head sha moved; a comment, label or
  `updatedAt` alone is no change) and `pr_no_longer_open`. A PR opened or
  updated at a commit this run made reads "observed during the run; points at
  a commit this run made"; every other PR entry reads "observed, not
  attributable". With `gh` missing or not authenticated, a timeout (30 s),
  exactly 1000 open PRs (`truncated`), or a repo gh read that cannot be told
  or is not the one expected (`repo`, `repo_source`; a `gh repo set-default`
  naming a repo that is no remote counts, and with no GitHub remote and no
  `GH_REPO` gh is not asked), the part is recorded `unavailable` and printed
  as `not compared: pull_requests: …`, never as an empty list. Still a report
  only: no verdict or exit code changes, and the state and feed schemas
  stay 3.
  ([#410](https://github.com/jitokim/oh-my-graph/issues/410))
