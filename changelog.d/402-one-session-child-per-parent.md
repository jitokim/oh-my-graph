### Changed

- **A parent may have at most one `handoff: session` child.** A graph where one
  node has two or more `handoff: session` children is now refused at load,
  because the children would share one session: the later one inherits its
  sibling's conversation, or both write one session file. To fix it, switch
  the other children to `handoff: artifact`, or chain them so each continues
  the previous child's session.
  ([#402](https://github.com/jitokim/oh-my-graph/issues/402))
