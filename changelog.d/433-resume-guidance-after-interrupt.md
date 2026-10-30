### Fixed

- **`runs list`, `show` and `resume --approve` point to `--retry-failed` after
  an interrupt that came after the approval was saved.** A Ctrl-C landing once
  `resume --approve <gate>` had saved the approval left a run that reads
  `FAIL` and is no longer paused, yet nothing said how to continue it, and
  `--approve` again only answered that the run is not paused. `runs list` and
  `show` now say the gate was already approved and to continue the run with
  `oh-my-graph resume <id> --retry-failed`, and `resume --approve` on such a
  run says the same (still exit 1), or `(nothing to resume)` when a retry
  would run nothing.
  ([#433](https://github.com/jitokim/oh-my-graph/issues/433))
