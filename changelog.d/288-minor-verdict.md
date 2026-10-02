### Changed

- **A review that only found nits no longer fails a gate: the review
  fragments answer `CLEAN`, `MINOR:` or `FINDINGS:`, and a gate fails only
  `FINDINGS:`.** If you narrowed a review to `^CLEAN` (or copied `self-dev`'s
  `grep -q '^CLEAN'`), widen it to the gating pattern
  ``'^[*_`\s]*(MINOR[*_`\s]*:|CLEAN\b)'`` (and `grep -Eq '^(CLEAN|MINOR:)'`).
  Left as it is, it now gets **stricter**, because a borderline item can
  arrive as `MINOR:` and fire your arc. `review-style` and `review-security`
  grade each item by whether the reviewer would merge with it unfixed, and a
  re-run reviewer keeps a minor item minor unless the rework changed its code,
  so a nit cannot be promoted into another round. `gated-lane`'s `review` and
  `self-dev`'s `review-verdict` accept `MINOR:`, and both implementers now fix
  the blocking findings only; the minor items pass, stay in the review's
  `<run-id>/<node>.out`, and reach the PR body. Two other shapes need an edit:
  a node that restates the old either-verdict pattern now FAILs on `MINOR:`
  (drop the override, or add ``MINOR[*_`\s]*:``), and a downstream prompt or
  command that branches on `FINDINGS:` in a review artifact misses items that
  now arrive as `MINOR:` (mention `MINOR:` in the branch). `repair-round` and
  `adr-driven-dev` keep the two-valued grammar, which stays valid. The ledger
  does **not** tell a `MINOR:` pass from a `CLEAN` one — that half of
  proposal (b) is not delivered, so #288 stays open for it, and for measuring
  whether `{{ self.previous }}` made the gated loops converge. (ADR 0043,
  [#288](https://github.com/jitokim/oh-my-graph/issues/288))
