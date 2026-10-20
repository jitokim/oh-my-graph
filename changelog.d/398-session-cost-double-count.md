### Fixed

- **A `handoff: session` node is no longer charged for its parent's spend.**
  claude reports a resumed session's cost as the whole session's running total,
  so a session child's row, the run and goal totals and its `budget_usd`
  verdict all counted its ancestors' spend. It is now booked at that total
  minus its session parent's, and the raw figure is kept as
  `reported_cost_usd` in `state.json` and terminal node events so the
  correction survives a `resume`. When the parent's figure is unknown, as in
  state from an older engine, the child's cost is recorded unknown with a
  note. Claude runtime only; no schema bump. Sibling session children of one
  node still over-count each other's spend (never hide it); see
  [#402](https://github.com/jitokim/oh-my-graph/issues/402).
  ([#398](https://github.com/jitokim/oh-my-graph/issues/398))
