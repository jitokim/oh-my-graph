### Fixed

- **A long artifact beside short ones now reaches the assessor whole.** Each
  artifact used to be cut to a fixed 2000 bytes even when most of the
  12000-byte assessor budget sat unused. Each now gets a fair share of what
  remains: the remaining budget divided by the artifacts still to render,
  never less than 2000 bytes. A short artifact spends only its own length,
  and once the budget is spent later artifacts are still omitted loudly.
  Verification output tails stay capped at 2000 bytes each.
  ([#405](https://github.com/jitokim/oh-my-graph/issues/405))
