### Fixed

- **`self-dev`'s review-verdict no longer passes a review that opens
  `CLEANUP` or `CLEANLY`: `CLEAN` must be the whole first word.** The
  `verify` command deleted every space before matching, so it could not see
  where the first word ended, and `grep -Eq '^(CLEAN|MINOR:)'` passed any
  review whose first word merely started with `CLEAN` — unlike the node's own
  `result_matches`, which reads `CLEAN\b`. It now drops markdown emphasis,
  folds line breaks into spaces and matches the first word whole, over the
  whole review rather than its first 256 bytes, so no cut can end a word early:
  ``cat "$f" | tr -d '*_`\r' | tr '\t\n' '  ' | grep -Eq '^ *(CLEAN([^[:alnum:]]|$)|MINOR *:)'``.
  If you copied the v0.16.0 command quoted under **Changed**, copy this one.
  ([#309](https://github.com/jitokim/oh-my-graph/issues/309))
