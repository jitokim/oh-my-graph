### Added

- **Every run now records which oh-my-graph build wrote it, and `show`
  prints it.** `state.json` and each `run_started` event carry
  `engine_version`, plus `engine_commit` (the full VCS revision, absent
  when the build has none) and `engine_modified` (only `true`, only for a
  dirty tree with a commit). The commit is the build info's `vcs.revision`,
  or, only when there is none, the link-time `-X main.Commit` (dirty when
  `-X main.Modified` is `"true"`); a stale `-X` never overrides a real
  revision. `make build` now stamps both from git, so builds in a linked git
  worktree, where Go embeds no VCS info, still record their commit, and the
  build still succeeds with both empty when git is unavailable. Release
  builds via goreleaser carry VCS info and are unchanged. Every leg stamps
  the binary running it, so `state.json` names the last writer and each
  leg's own build stays on its `run_started`. `show` prints an `engine:`
  line under the run header, adds a `distinct engines, in first-seen leg
  order:` list when legs were run by different builds, prints
  `engine: oh-my-graph <version> (commit unknown)` for a version stamped
  without a commit, and prints `engine: version not recorded` for an older
  run instead of guessing. The
  fields are optional and absent on older runs, which still load, show and
  resume; the schema stays 3.
  ([#373](https://github.com/jitokim/oh-my-graph/issues/373))
