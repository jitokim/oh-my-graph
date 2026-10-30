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
- **Ctrl-C during a resumed leg's start remote snapshot no longer exits 1
  while the run is still paused.** An interrupt that lands before `resume
  --approve`, `--reject` or `--retry-failed` has saved its decision leaves the
  run paused at its gate, yet the command exited 1. It now exits 2, says the
  run is still paused at its gate, and prints the gate's own hint, so the
  same `resume` can be run again. A resumed run not paused at a gate, and a
  fresh `run` or `auto`, still exit 1.
  ([#433](https://github.com/jitokim/oh-my-graph/issues/433))
