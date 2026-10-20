### Changed

- **`auto` now plans a node that keeps editing its parent's files as a
  continuation of the parent's session.** The planner prompt said only that
  `handoff: session` continues a single parent's session, never when to use
  it, so every planned node started fresh and re-read everything. It now
  tells the planner to set `"handoff": "session"` for a node that continues
  the same work its single parent just did: it edits the same files, or adds
  the tests or docs for the code its parent wrote. Review and check nodes,
  root nodes, nodes with more than one parent, and every node inside a
  feedback loop stay on artifact handoff. A session node still needs exactly
  one parent, and validation is unchanged.
  ([#391](https://github.com/jitokim/oh-my-graph/issues/391))
