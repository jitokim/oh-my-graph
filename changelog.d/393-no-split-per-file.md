### Changed

- **The planner no longer splits a medium goal into one node per file.** Every
  planned node is a fresh session that re-reads the code it works on, so the
  planner prompt now makes one doing node the default for a small or medium
  goal (a few files plus their tests and docs). It splits work only where a
  part is a reviewable, committable slice on its own and the parts do not
  re-read the same code, never one node per file or per numbered change, and
  a change's tests and docs stay in the node that makes it. The advice to
  prefer more, smaller nodes now applies only to substantial implementation
  (several packages or several independent features). Prompt text only; the
  validator and schema are unchanged.
  ([#393](https://github.com/jitokim/oh-my-graph/issues/393))
