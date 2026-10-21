### Fixed

- **The final check node's artifact now reaches the assessor.** With six or
  more large artifacts, the goal assessor's 12000-byte artifact budget ran out
  in graph order before the last node, so the final check node's artifact
  could be dropped. Artifacts of sink nodes, which no other node depends on,
  are now budgeted first, and the other artifacts split what they leave.
  Artifacts still render in graph order, and any that do not fit are still
  omitted loudly. ([#408](https://github.com/jitokim/oh-my-graph/issues/408))
