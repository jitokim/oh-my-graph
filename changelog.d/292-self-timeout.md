### Added

- **A prompt, `cwd` or verify command can now quote its node's own timeout as
  `{{ self.timeout }}`.** It renders the bound the node is actually killed at —
  its `timeout:`, or the runner's default when it declares none — in Go
  duration form (`20m0s`, `1h30m0s`), the same on every attempt, so a fragment
  can budget against its using node's bound instead of restating a number.
  ([#292](https://github.com/jitokim/oh-my-graph/issues/292))
