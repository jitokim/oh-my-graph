### Fixed

- **With `--verify-cmd`, every done-list command now reaches the final
  check node, not only whole-suite ones.** The planner's done-list rule
  named only whole-suite commands (`make local`, `go test ./...`), so a
  done-list script, linter or single-package test slipped past it, the
  planner wrote a stand-in check and the goal looped "not met". Any
  done-list command that is not the verify command, character for
  character, is now run once by the final check node when an allowed Bash
  pattern covers it, and is otherwise a FAIL naming it. Both planner
  prompts, with and without `--verify-cmd`, now also say a different
  command that checks the same thing never satisfies a done item: only
  the named command running counts.
  ([#333](https://github.com/jitokim/oh-my-graph/issues/333))
