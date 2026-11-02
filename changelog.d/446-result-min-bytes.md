### Added

- **`success_check.result_min_bytes` fails a node whose final reply is too
  short.** A node's artifact is only its final assistant message, so a node
  that closes with "the report is above" hands its dependents one sentence,
  and a `result_matches` can still pass on it. `result_min_bytes: N` fails the
  node when that reply, the same string `result_matches` reads, is under `N`
  bytes. It runs after `exit_zero` and before `result_matches`, `0` or unset
  means no minimum, and a negative value is refused at load. A miss carries
  the `result_mismatch` retry cause, so `retry.on`, the retry's quote of the
  previous reply and `feedback:` loops treat it like a `result_matches` miss.
  ([#446](https://github.com/jitokim/oh-my-graph/issues/446))
