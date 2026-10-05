### Changed

- **`e2e-verify` gives its stress run a budget instead of letting it pick
  one that outlives the node.** The fragment now states its own bound,
  `timeout: 20m` (the runner's default, so nothing is killed sooner), and its
  prompt tells the node to spend at most half of it on stress: time one
  `-count=1` repetition, derive a count that fits, and pass a `-timeout`
  between the budget and the bound (`go test <pkg> -race -count=<n> -timeout
  12m`), since `go test`'s own 10-minute default prints a FAIL that never
  happened. `self-dev`, `dev-review-pr` and `backlog-batch` no longer hand
  down `-count=300`, which ran a dogfood e2e node into its 20-minute timeout
  with no verdict. If your graph overrides `timeout:` on a node that uses
  `e2e-verify`, its prompt still says 20 minutes.
  ([#292](https://github.com/jitokim/oh-my-graph/issues/292))
