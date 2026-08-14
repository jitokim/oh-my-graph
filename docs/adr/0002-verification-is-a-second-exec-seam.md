# ADR 0002 — Evidence verification runs outside the `NodeRunner` seam

- Status: Accepted
- Date: 2026-07-29
- Issue: [#7](https://github.com/jitokim/oh-my-graph/issues/7)

## Context

`success_check` today is `exit_zero` plus an optional `result_matches` regex over
the node's own `.result` text. A node passes by *saying* "PASS". There is no
check against anything outside the model's narration, which is the single
substantive gap between oh-my-graph and evidence-verifying siblings like OMK —
and it is already disclosed as such in README's "Known limitations" and "Prior
art".

Closing it means the engine must run something itself: a command whose exit code
and output the engine judges, independent of what the node claims. That collides
with a load-bearing rule recorded in CONTRIBUTING.md and ADR 0001:

> `internal/runner.CLIRunner` is **the only object in this codebase that
> may import `os/exec`**.

Three ways to satisfy the feature:

1. Teach `CLIRunner` to also run verification commands.
2. Let the `Scheduler` exec the command directly.
3. Give verification its own, narrower seam in a new package.

## Decision

**Option 3.** Verification gets its own interface and its own single exec-owning
implementation, in a new `internal/verify` package:

```go
type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}
```

- `ShellVerifier` runs the command via `sh -c` under a per-verification timeout
  and the run's context. It is the only object in `internal/verify` that imports
  `os/exec`.
- `RefusingVerifier` is the `schedule.Options.Verifier` default, mirroring the
  existing gate stub: a scheduler test that forgets to inject one fails loudly
  instead of silently spawning a real process.
- `FakeVerifier` (scripted, keyed by command) is what tests inject, so the whole
  verify path stays spawn-free in CI.
- `cmd/oh-my-graph` injects `ShellVerifier` by constructor, next to
  `CLIRunner`. The `Scheduler` never constructs either.

The invariant is **restated, not weakened**:

> Exactly two objects in oh-my-graph may spawn a process —
> `runner.CLIRunner` and `verify.ShellVerifier` — each behind its own
> injected interface. No other package imports `os/exec`.

> **Update (2026-08-05):** superseded in part — the count only. The invariant
> was restated at **three** by [ADR 0005](0005-worktree-provisioning-is-a-third-exec-seam.md)
> (`worktree.GitManager` joined) and at **four** by
> [ADR 0006](0006-browser-open-is-a-fourth-exec-seam.md)
> (`browser.ExecOpener` joined); four is the current count, enforced by
> `internal/invariants`. Everything else above stands: each seam is still
> behind its own injected interface, no other package imports `os/exec`, and a
> fifth spawner still needs its own ADR.

The child-environment scrub applies to both. `ANTHROPIC_API_KEY` and
`ANTHROPIC_AUTH_TOKEN` are deleted from a verification command's environment too,
because `verify: { command: "claude -p ..." }` is a legal thing to write and
would otherwise run on metered API billing. The rule lives once, in a new leaf
package `internal/childenv`, imported by both spawners and asserted by a test on
each side.

Verification runs **last** in the success-check conjunction — after `exit_zero`
and `result_matches`, before `PersistOutput`. `result_matches` is retained and
composes by AND; it is documented and reported as a *secondary, self-reported*
signal that is never sufficient on its own for a node whose success is externally
observable.

A planned (`auto`) node may not set `success_check.verify` at all.

## Consequences

**Positive**

- The gap README already admits is closed with a real mechanism, not a stronger
  regex.
- Both purposes of the original invariant survive intact: the subscription-auth
  scrub still has exactly one home per spawner, and the whole engine is still
  testable with zero real spawns.
- `NodeRunner` keeps one reason to change. Teaching it to run arbitrary shell
  would have given `FakeRunner` two unrelated things to fake, degrading the
  testability keystone the project is built on.
- Verification failures are first-class: `*NodeCheckError{Predicate: "verify"}`
  with the exit code and a truncated output tail, and a `verify_failed` retry
  cause, so `retry: { on: [verify_failed] }` composes with the existing policy.

**Negative / trade-offs**

- The "one exec object" rule is now "two exec objects". That is a real loosening
  of a rule whose value came partly from its bluntness, and it must be policed:
  a third would be a design regression, and the CONTRIBUTING.md wording has to be
  updated in the same PR so reviewers enforce the new form, not the old one.
- `sh -c` means a verification command is arbitrary shell. For a hand-written
  graph that is the same standing as `allowed_tools` — the user's own reviewed
  artifact. For an auto-planned graph it would be a hole straight through every
  coordinator guard, which is why planned nodes are refused the field outright.
- Verification costs wall-clock time on the critical path of every node that
  declares it, and a badly-scoped command (a full test suite on every node) will
  dominate a run. Hence the default 2m timeout and 10m ceiling, validated at load
  rather than discovered mid-run.
- A `Verification` struct means loader, validator, shipped example graphs and
  tests must move together; `SuccessCheck.IsZero()` in particular must learn
  about the new field or an empty check silently changes meaning.

## Alternatives considered

- **Extend `CLIRunner`.** Rejected: it is the claude-invocation object.
  Verification is not a claude invocation, and merging them makes `FakeRunner`
  responsible for faking two unrelated subsystems.
- **Exec from the `Scheduler`.** Rejected outright: the Scheduler's defining
  property is that it never spawns anything and never learns whether a real
  claude ran. That property is why the engine is unit-testable at all.
- **Run the verification as another claude node.** Rejected: it puts the model
  back in the judgement loop, which is the exact thing this ADR exists to remove
  — and it costs money per check.
- **Replace `result_matches` with `verify` outright.** Rejected: it would break
  every existing graph and every shipped example for no safety gain.
  `result_matches` is a fine cheap filter; the fix is to stop treating it as
  evidence, in the docs and in the ledger's language.
