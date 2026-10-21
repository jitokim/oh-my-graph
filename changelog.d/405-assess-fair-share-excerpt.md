### Fixed

- **A long artifact beside short ones now reaches the assessor whole.** Each
  artifact used to be cut to a fixed 2000 bytes even when most of the
  12000-byte assessor budget sat unused. The budget is now split across all
  artifacts before any renders: short ones render whole, and what they leave
  is split evenly among the longer ones, never less than 2000 bytes each. The
  split does not depend on graph order, so a long artifact renders whole
  whether it comes before or after the short ones. Once the budget is spent,
  later artifacts are still omitted loudly. Verification output tails stay
  capped at 2000 bytes each.
  ([#405](https://github.com/jitokim/oh-my-graph/issues/405))
