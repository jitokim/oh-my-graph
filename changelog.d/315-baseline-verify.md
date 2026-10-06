### Changed

- **`auto --verify-cmd` now checks the starting tree before it spends
  anything.** It runs the command once on the directory as it is; if it already
  fails there, `auto` stops with a "baseline red" message quoting the tail of
  its output and exits 5 — no model call, no cycle, no run directory — instead
  of learning it at the end of a paid cycle.
  ([#315](https://github.com/jitokim/oh-my-graph/issues/315))
