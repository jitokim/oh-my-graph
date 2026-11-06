### Documented

ADR 0045 (Proposed) records the decision to let a `type: gate` pause only when a named parent's recorded result matches a declared `pause_when` pattern, so `drift-nego`'s amend-design ruling can stop the run as PAUSED instead of failing it. A gate whose pattern does not match passes and is recorded as cleared. Nothing changes in behaviour yet: no key, event or schema value is added. ([#386](https://github.com/jitokim/oh-my-graph/issues/386))
