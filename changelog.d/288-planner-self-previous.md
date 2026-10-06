### Changed

- **`auto` now tells a planned review loop's reviewer to quote its own
  previous-round reply.** The planner prompt asks the reviewing node to quote
  `{{ self.previous }}` and check its earlier findings first, before raising
  new ones; this is guidance only, and a plan that leaves it out is still
  accepted. ([#288](https://github.com/jitokim/oh-my-graph/issues/288))
