### Fixed

- **An unguarded run interrupted during its end-of-leg remote snapshot now
  reads `FAIL`, matching its exit 1.** When a Ctrl-C landed while the leg's
  after remote snapshot ran and every node had already passed, the command
  exited 1 and the leg closed `"failed"`, but `runs list`, `show` and the
  dashboard read `PASS`, because a run without `--forbid-remote-changes`
  counted its passed nodes before its closing outcome. A closed leg whose
  outcome is `"failed"` now reads `FAIL` whatever its node count, guarded or
  not, so exit 1 ↔ `FAIL` holds for every single-run invocation (ADR 0023
  §2.6). `resume --retry-failed` on such a run is accepted: it launches no
  node, the leg closes `"passed"`, and the run reads `PASS`.
  ([#434](https://github.com/jitokim/oh-my-graph/issues/434))
