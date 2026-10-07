### Documented

ADR 0031 accepts its §3.5 alone: a limit pause will be recorded in `state.json` at the run level, beside the gate pause, with no new `runstatus` value and no schema bump; how often runs pause on a limit is counted from the event stream before the `loop` supervisor (§3.1–§3.4, still Proposed) is decided. ([#358](https://github.com/jitokim/oh-my-graph/issues/358))
