### Documented

ADR 0038 §9.2 now refuses a fragment with a nested `use:` in the first slice, because the nested file's bytes are not pinned, and the post-splice backstop refuses any `permission_mode`. ([#338](https://github.com/jitokim/oh-my-graph/issues/338))
