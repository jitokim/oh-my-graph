### Fixed

- **`scripts/drift-baseline.sh` names a missing pinned file on macOS too.**
  It found a missing file only by GNU `sha256sum -c`'s `FAILED open or read`
  line on stdout, but macOS's `sha256sum` reports one only on stderr, so a
  deleted design or requirements file came out as `LOCK <lock> unreadable`.
  The script now checks each file the lock pins itself before the checker
  runs and prints `LOCK <file> missing` whichever `sha256sum` or `shasum` is
  installed; a changed file, a malformed lock and a passing lock read as
  before. ([#395](https://github.com/jitokim/oh-my-graph/issues/395))
