### Fixed

- **The final check node's artifact now reaches the assessor.** With six or
  more large artifacts, the goal assessor's 12000-byte artifact budget ran out
  in graph order before the last node, so the final check node's artifact
  could be dropped. Every artifact's allowance is now fixed before any
  renders, the allowances never sum to more than the budget, and the split
  does not depend on graph order. Artifacts of sink nodes, which no other node
  depends on, are budgeted first: the sinks together get at most half of the
  budget, and what is left is shared across every artifact's remaining
  length, so a single large sink can no longer push the other artifacts out.
  When the budget cannot give every artifact a 2000-byte floor, non-sinks get
  it before sinks, then shorter artifacts first. Artifacts of the same kind
  and the same length are one group. A group that cannot all get the floor
  shares what is left equally, so equal artifacts get equal shares, but only
  when each share is at least 1000 bytes, half the floor; node ID then only
  hands out the few leftover bytes that do not split evenly. Below that, as
  many members as fit get the floor in node-ID order and the rest of the
  group is omitted (when not even one fits a floor, the first by node ID
  takes what is left), and the budget left below one floor is shared among
  the artifacts that render, so the allowances end at or near the budget. Each
  artifact left with nothing is omitted loudly with its own
  `(artifact of node <id> omitted: material cap reached)` line, where its
  artifact would have been, instead of one line for all of them.
  ([#408](https://github.com/jitokim/oh-my-graph/issues/408))
