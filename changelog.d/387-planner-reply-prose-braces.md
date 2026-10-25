### Fixed

- **A planner reply whose prose quotes template braces before the plan is no
  longer refused as not-loadable YAML.** The plan object used to be cut from
  the reply's first `{` to its last `}`, so a prose line quoting a template
  placeholder such as `<<name>>` in braces before the plan pulled that prose
  into the cut. The planner, the planned-node check and the router now pick
  out the one JSON object in the reply: the one `json` fenced block that
  holds a valid object, else the one `{`…`}` span that decodes as a complete
  object, with stray braces in prose skipped. A reply that holds more than
  one JSON object, as two objects or two valid `json` fences, is refused as
  ambiguous rather than guessed at; the planner is not retried, and the
  router falls back to converse. A cut-off plan still reaches the parser and
  reports its real JSON syntax error, never a plan built from one of its
  inner nodes, and a valid object followed by a cut-off one is now refused,
  since the reply may be truncated, rather than taken as the plan. The goal
  assessor keeps the strict first-`{`-to-last-`}` cut, since its reply
  quotes untrusted artifacts by design, so a quoted JSON object in its reply
  still fails loudly instead of being taken as the verdict.
  ([#387](https://github.com/jitokim/oh-my-graph/issues/387))
