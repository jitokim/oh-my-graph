### Changed

- `auto --verify-cmd` now runs the command once on the starting tree before spending anything; if it already fails there, `auto` stops with a "baseline red" message quoting the tail of its output and exits 5, with no model call and no run directory. ([#315](https://github.com/jitokim/oh-my-graph/issues/315))
