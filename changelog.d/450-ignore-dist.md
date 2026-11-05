### Fixed

- **Release binaries' build info is now accurate: no more `+dirty` or
  `vcs.modified=true`.** goreleaser writes into an untracked `dist/` before it
  builds, and Go counts untracked files as a modified tree, so every release
  binary was marked modified even though its contents always matched the tag.
  ([#450](https://github.com/jitokim/oh-my-graph/issues/450))
