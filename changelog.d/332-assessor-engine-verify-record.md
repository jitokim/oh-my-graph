### Added

- **`state.json` now records what a node's `success_check.verify` actually
  did, and the goal assessor reads it.** A node with a verify carries a
  `verification` record: the command as run, its exit code (absent, never
  faked, when it did not exit on its own or was killed by a signal), the
  expected exit code, its
  duration, a status (`passed`, `failed`, `timed out`, `cancelled`,
  `did not run`, `interpolation error`, `not judged`) and the last 4096 bytes
  of its output. The schema stays at 3, and a run without a verify writes the
  same file as before. In `auto --max-cycles`, the assessor now gets an
  engine-observed block for each such node, with the command, status and exit
  code outside the fence and the output tail fenced as data. It is told this
  observation outranks the node's own reply about the same check, so a node
  that only answered "PASS" is no longer judged on its word alone.
  ([#332](https://github.com/jitokim/oh-my-graph/issues/332))
