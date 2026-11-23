### Changed

- **Under `--runtime codex`, the planner no longer plans commits.** Codex's
  `workspace-write` sandbox keeps `.git` read-only, so a node told to commit
  could not. `auto` and `chat` on Codex now tell the planner that no node can
  commit, stage or amend; the work stays in the working tree and the end-of-run
  `working tree:` line reports it. The Claude planner prompt is unchanged.
  docs/LIMITATIONS.md records this and the Go-cache limit with their measured
  workarounds. ([#457](https://github.com/jitokim/oh-my-graph/issues/457))
