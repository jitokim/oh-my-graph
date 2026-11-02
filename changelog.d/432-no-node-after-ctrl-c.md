### Fixed

- **After Ctrl-C the scheduler no longer starts a node on the
  already-cancelled run.** A ready node waiting for a concurrency slot raced
  the slot against the run's cancellation, and when both were ready Go picked
  one at random, so a node could still start, and spawn a model process,
  after the interrupt. The launch now checks the run's context before and
  after taking the slot, and gives the slot back without starting the node
  once the run is cancelled.
  ([#432](https://github.com/jitokim/oh-my-graph/issues/432))
