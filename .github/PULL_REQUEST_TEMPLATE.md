## What / why

<!-- What does this change, and why is it needed? -->

## Tests run

<!-- e.g. `make test`, `make vet`, `make fmt-check`. If you touched the -->
<!-- NodeRunner/scheduler, confirm the FakeRunner suite still covers the -->
<!-- new behavior. Only mention `make smoke` if you ran it manually — it -->
<!-- must never run in CI. -->

- [ ] `make test` passes
- [ ] `make vet` / `make fmt-check` pass

## DESIGN.md alignment

- [ ] This PR does not change the execution model, graph schema, or
      `NodeRunner` interface, **or** DESIGN.md is updated in this PR to match
- [ ] No invariant from [CONTRIBUTING.md](../CONTRIBUTING.md#invariants-a-contribution-must-preserve)
      (env scrub, no Agent SDK, no `--bare`, session persistence stays on) is weakened
