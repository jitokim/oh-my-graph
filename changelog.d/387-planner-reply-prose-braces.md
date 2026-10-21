### Fixed

- **A planner reply whose leading prose quotes template braces is no longer
  refused as not-loadable YAML.** The plan object used to be cut from the
  reply's first `{` to its last `}`, so a prose line quoting a placeholder
  such as `<<name>>` in braces before the plan pulled that prose into the cut.
  The plan is now found by decoding: the first `json` fenced block that holds
  a JSON object, else the first `{` that decodes as one complete JSON object,
  with any text after it ignored.
  ([#387](https://github.com/jitokim/oh-my-graph/issues/387))
