### Fixed

- **Verification output can no longer push the final check out of the
  assessor's view.** The engine's verification output tails render before
  every artifact and shared the goal assessor's 12000-byte material budget,
  so six 2500-byte tails, kept at 2000 bytes each, used up the whole budget
  and the final check node's artifact was omitted, even a four-byte `PASS`.
  The tails together now spend at most half of the budget, after the sink
  artifacts' minimum share is reserved. That share is split across the tails
  before any renders, the same order-free way as the artifacts, with failing
  checks first: which tails keep their output no longer depends on graph
  position, and a failing check's output is never dropped behind passing
  ones. A tail cut or omitted says so in its own line, while every block's
  command, status and exit-code lines still render. The artifacts split what the tails did not use exactly
  as before. ([#419](https://github.com/jitokim/oh-my-graph/issues/419))
