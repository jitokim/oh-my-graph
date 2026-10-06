### Fixed

- **The placeholder warnings that `lint`, `run --dry-run` and the plan screen
  print now end two of their findings with the quoting hint: the one for an
  input the graph does not declare and the one for an artifact that names a
  node the graph does not have.** A prompt that only quotes or explains
  `{{ inputs.<name> }}` or `{{ artifacts.<id> }}` is resolved like any other,
  and that is the commonest way to write either mistake, yet the warning
  reported a wiring bug and left the reader to find the cause. The runtime
  diagnostics for the same mistakes already ended with this hint (#234); these
  warnings now do too: the rule that every `{{ ... }}` in a prompt is
  resolved, and the way out (break the braces apart, or pass the text in as an
  input or artifact). Only those two warnings change. They print the same way
  from `lint`, `run --dry-run` and the `auto`/`chat` plan screen, where only
  the undeclared-input one can appear, because a plan that names a missing
  node is refused before that screen prints. Auto mode's plan refusal and
  every other warning are unchanged, and no exit code changed.
  ([#313](https://github.com/jitokim/oh-my-graph/issues/313))
