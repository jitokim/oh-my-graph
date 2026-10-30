### Changed

- **Ctrl-C during a resumed leg's start remote snapshot now exits 2 and says
  the run is still paused.** An interrupt that lands before `resume
  --approve`, `--reject` or `--retry-failed` has saved its decision leaves the
  run paused at its gate; the command used to exit 1. It now exits 2, prints
  `interrupted before the approval was saved; still paused at gate "<id>"`
  (or rejection, or retry) and the gate's own hint, so the same `resume` can
  be run again. A resumed run not paused at a gate, and a fresh `run` or
  `auto`, still exit 1.
  ([#433](https://github.com/jitokim/oh-my-graph/issues/433))
