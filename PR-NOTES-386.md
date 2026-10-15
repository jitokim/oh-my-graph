# PR notes — #386 drift-check / drift-nego

The fragments and `graphs/drift-review.yaml` ship without any engine change.
Three parts of the request can't be written in today's graph language. Each
was stopped and worked around rather than built, and each needs an ADR before
engine work starts.

## Stopped

1. **The AMEND_DESIGN stop can't be a gate.** The asked-for shape is a human
   gate that pauses only when `rule` answers `AMEND_DESIGN:`. A `type: gate`
   node can't be conditional: it always pauses unless `--auto-approve` names
   it (DESIGN.md, "Gate nodes and `resume`"). A gate after `rule` would stop every run,
   with or without drift.
   *Workaround:* `rule`'s `result_matches` admits only `NO_DRIFT` and
   `RESOLVED:`. An `AMEND_DESIGN:` reply fails the node, and halt-on-fail stops
   the run before `fix`. `rule` retries only on `nonzero_exit`, so a retry
   can't turn the stop into a pass. The run shows as FAIL, not PAUSED, and
   can't be continued with `resume --approve`.
   *Needs:* a conditional gate, which is engine work and needs an ADR.

2. **`nego` can't be skipped when the check finds no drift.** The node
   schema has no conditional field, and a `use:` node can't carry one either:
   a multi-node use takes wiring keys only.
   *Workaround:* every `drift-nego` prompt answers a `NO_DRIFT` report at once
   (`fix` with `PASS`). That still costs three short sessions and one run of
   the `verify` command per clean check.
   *Needs:* conditional execution of a node or `use:`, which needs an ADR.
   It is likely the same ADR as item 1.

3. **`head` can't default to `HEAD`.** Graphs have no per-graph input
   defaults (DESIGN.md, "Binding inputs"). An unbound input fails
   `run --dry-run` and fails the node at run time.
   *Workaround:* `head` is a required input. The graph header,
   docs/EXAMPLES.md and the changelog all say to pass `--input head=HEAD`.
   *Needs:* input defaults are a change to the graph language and need an
   ADR.

## Open, not stopped

- **The interview is not the author's own session.** `interview` is a fresh
  session reading the author's commits and messages. Resuming the real author
  session is #386's open question, and it needs an ADR.
