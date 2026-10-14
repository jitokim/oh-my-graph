### Added

- **Every run now records which oh-my-graph build wrote it, and `show`
  prints it.** `state.json` and each `run_started` event carry
  `engine_version`, plus `engine_commit` (the full VCS revision, absent
  when the build has none) and `engine_modified` (only `true`, only for a
  dirty tree with a commit). Every leg stamps the binary running it, so
  `state.json` names the last writer and each leg's own build stays on its
  `run_started`. `show` prints an `engine:` line under the run header, adds
  an `engines by leg:` list when legs were run by different builds, and
  says `version not recorded` for an older run instead of guessing. The
  fields are optional and absent on older runs, which still load, show and
  resume; the schema stays 3.
  ([#373](https://github.com/jitokim/oh-my-graph/issues/373))
