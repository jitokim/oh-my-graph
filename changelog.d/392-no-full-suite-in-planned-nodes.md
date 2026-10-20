### Changed

- **With `--verify-cmd`, planned nodes no longer run the whole suite.** The
  engine already runs that command once after the final node, so the
  planner prompt now says no planned node runs the project's whole suite
  (`make local`, `make test`, `make check`, `go test ./...`) or the verify
  command itself, even when the goal names it. A node that changes code
  runs only the tests for what it changed (for Go, the changed packages),
  with the matching Bash pattern in its `allowed_tools`. Without
  `--verify-cmd` the prompt is unchanged.
  ([#392](https://github.com/jitokim/oh-my-graph/issues/392))
