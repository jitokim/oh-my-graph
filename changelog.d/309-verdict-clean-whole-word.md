### Fixed

- **`self-dev`'s review-verdict no longer passes a review that opens
  `CLEANUP` or `CLEANLY`: `CLEAN` must be the whole first word.** The
  `verify` command deleted every space before matching, so it could not see
  where the first word ended, and `grep -Eq '^(CLEAN|MINOR:)'` passed any
  review whose first word merely started with `CLEAN` — unlike the node's own
  `result_matches`, which reads `CLEAN\b`. It now drops `*` and backtick
  emphasis, folds line breaks into spaces and matches the first word whole,
  over the whole review rather than its first 256 bytes, so no cut can end a
  word early. It keeps `_`, a word character to `\b`, so `CLEAN_` is not
  `CLEAN` and underscore emphasis passes only as a pair (`_CLEAN_`,
  `__CLEAN__`):
  ``cat "$f" | tr -d '*`\r' | tr '\t\n' '  ' | LC_ALL=C grep -Eq '^ *((__CLEAN__|_CLEAN_|CLEAN)([^A-Za-z0-9_]|$)|_{0,2}MINOR_{0,2} *:)'``.
  The word class is ASCII under `LC_ALL=C`, as Go's `\b` is, so the shell's
  locale cannot judge a non-ASCII letter after `CLEAN` differently.
  If you copied the v0.16.0 command quoted under **Changed**, copy this one.
  ([#309](https://github.com/jitokim/oh-my-graph/issues/309))
