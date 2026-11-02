### Changed

- **The plan screen's `exit_zero` note names `result_min_bytes` too.** Trusted
  code also adds `exit_zero: true` beside a planned `result_min_bytes`, so the
  note now reads `exit_zero: true added beside its result_matches or
  result_min_bytes (a planned check is never weaker than the exit code)`.
  ([#447](https://github.com/jitokim/oh-my-graph/issues/447))
