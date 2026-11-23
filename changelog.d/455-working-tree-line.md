### Added

- **A run that leaves its work uncommitted now says so.** At the end of every
  `run`, `auto` cycle and `resume` leg, a dirty working tree in the invocation
  repository prints one line after the ledger, `working tree: 3 files changed,
  uncommitted (a.go, b.go, c.go)` (the first five paths, then `, …`), and is
  recorded as `working_tree` (`changed`, `paths`) in `state.json`, which `show
  <run-id>` prints from. A clean tree prints nothing. No verdict, node status or
  exit code changes. ([#455](https://github.com/jitokim/oh-my-graph/issues/455))
