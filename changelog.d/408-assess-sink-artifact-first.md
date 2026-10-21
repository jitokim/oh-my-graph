### Fixed

- **The final check node's artifact now reaches the assessor.** With six or
  more large artifacts, the goal assessor's 12000-byte artifact budget ran out
  in graph order before the last node, so the final check node's artifact
  could be dropped. Artifacts of sink nodes, which no other node depends on,
  are now budgeted first, in two passes. First the sinks together get at most
  half of the artifact budget, shared among them so each still gets at least
  2000 bytes when the half allows it. Then whatever is left is shared across
  every artifact's remaining length, the sinks included, so a single large
  sink can no longer push the other artifacts out. Every plan has at least one
  sink, because a DAG always has one; the no-sink path exists only for
  evidence built without `Sink` set (older callers, tests), which splits the
  whole budget as before. Artifacts still render in graph order, each cut
  leaving room for the pass-1 share of every sink still to render, and each
  one that does not fit is omitted loudly with its own
  `(artifact of node <id> omitted: material cap reached)` line, where its
  artifact would have been, instead of one line for all of them.
  ([#408](https://github.com/jitokim/oh-my-graph/issues/408))
