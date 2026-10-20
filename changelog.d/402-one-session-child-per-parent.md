### Changed

- **A parent may have at most one `handoff: session` child.** A graph where one
  node has two or more `handoff: session` children is now refused at load,
  because the children would share one session: the later one inherits its
  sibling's conversation, or both write one session file. To fix it, switch
  the other children to `handoff: artifact`, or chain them so each continues
  the previous child's session. A run started before this change whose graph
  gives one parent two `handoff: session` children cannot be resumed; start a
  new run with a fixed graph instead.
  ([#402](https://github.com/jitokim/oh-my-graph/issues/402))
