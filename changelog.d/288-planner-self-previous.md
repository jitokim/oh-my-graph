### Changed

- **`auto` now tells a planned review loop's reviewer to quote its own previous-round reply.** The planner prompt asks the reviewing node to quote `{{ self.previous }}` and check its earlier findings before raising new ones, so a re-run reviewer no longer judges the rework as a stranger; a plan that leaves it out is still accepted. ([#288](https://github.com/jitokim/oh-my-graph/issues/288))
