### Fixed

- **`node --eval=<code>` and `node --print=<expr>` no longer pin the file after
  them.** Node takes its long inline and runner options (`--eval`, `--print`,
  `--test`) in the `--opt=value` form too, but the verify pin matched only the
  bare form, so it pinned the test file that followed as the executed script
  and a goal that edited it got a false "verify script changed" fault. A long
  option now counts in either form; the short `-e`/`-p`, which node does not
  take as `-e=...`, are unchanged.
  ([#375](https://github.com/jitokim/oh-my-graph/issues/375))
