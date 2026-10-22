### Changed

- **A planned review loop no longer fails a round over a cosmetic finding.**
  The planner prompt now tells the node declaring a feedback arc to start its
  reply with `CLEAN`, `MINOR:` or `FINDINGS:`, graded by whether it would merge
  the item unfixed — an untrue claim or a behaviour or correctness change is
  `FINDINGS:`, never `MINOR:` — and gives it the gating `success_check` the shipped review
  fragments use: `CLEAN` and `MINOR:` pass, with minor items recorded in the
  reply, and only `FINDINGS:` fails and fires the arc. The planned final
  check keeps its whole-reply `PASS` pin.
  ([#288](https://github.com/jitokim/oh-my-graph/issues/288))
