### Changed

- **With `--verify-cmd`, planned nodes no longer run the whole suite.** The
  engine already runs that command once after the final node, so the
  planner prompt now says no planned node runs the verify command itself,
  even when the goal names it, and a node that changes code runs only the
  tests for what it changed (for Go, the changed packages), with the
  matching Bash pattern in its `allowed_tools`. The planner now sees the
  verify command's exact text, quoted, and when the goal's done list names
  a whole-suite command (`make local`, `make test`, `make check`,
  `go test ./...`) that the verify command does not cover, the final check
  node runs it once and its PASS requires exit 0. The match is by exact
  text: a wrapper that runs the suite inside it (e.g. `sh v.sh` whose
  script runs `make local`) does not match, so the suite runs twice — the
  safe direction, and deliberate. Without `--verify-cmd` the prompt is
  unchanged.
  ([#392](https://github.com/jitokim/oh-my-graph/issues/392))
