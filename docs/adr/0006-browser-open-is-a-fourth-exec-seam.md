# ADR 0006 — Browser-open runs behind a fourth exec seam

- Status: Accepted
- Date: 2026-08-01

## Context

`oh-my-graph serve` binds a loopback-only live view and prints its URL. It
deliberately does not open the browser: launching one means shelling out to
`open`/`xdg-open`/`cmd /c start` — a subprocess — and the invariant
CONTRIBUTING.md, ADR 0002 and ADR 0005 police says a new spawner needs an ADR
first. Print-URL-only was therefore the honest interim state, and the code
said so in three places (DESIGN.md's serve section, the `internal/serve`
package doc, and `runServe`'s doc comment): *auto-open is a deliberate
follow-up, not an oversight*.

The follow-up is now due. Watching a run is the whole point of `serve`, and
"copy the URL out of your terminal" is friction on the feature's only path.
This is that ADR. The options mirror ADR 0005's:

1. Teach an existing spawner (`ShellVerifier` already runs arbitrary shell)
   to also launch the browser.
2. Let `cmd/oh-my-graph` exec the launcher directly.
3. Give browser-open its own, narrower seam in a new package.

## Decision

**Option 3.** Browser-open gets its own interface and its own single
exec-owning implementation, in a new `internal/browser` package:

```go
type Opener interface {
	Open(ctx context.Context, url string) error
}
```

- `ExecOpener` runs the platform's default-browser launcher — `open <url>` on
  macOS, `xdg-open <url>` on other unixes, `cmd /c start "" <url>` on Windows
  — selected by build-tagged `openArgv` files; only `exec.go` imports
  `os/exec`. The URL is always a verbatim argv element, never interpolated
  into a shell line. Each launch is bounded by a timeout so a wedged launcher
  cannot stall the caller.
- `RefusingOpener` mirrors `worktree.RefusingProvider` for any code that
  must hold an Opener without ever opening: a forgotten real injection fails
  loudly instead of silently popping a browser from a test. The CLI's
  disabled paths (non-TTY, `--no-web`) are stricter still — they carry a nil
  Opener, which turns the live view off entirely, so no Opener is consulted
  at all.
- `FakeOpener` (records URLs in order, scriptable failure) is what tests
  inject, so every auto-open path stays spawn-free in CI.
- The interface carries no policy: deciding WHETHER to open is the caller's
  job. Phase 2 wires `ExecOpener` behind a **TTY gate** — open only when the
  CLI is talking to an interactive terminal, so a scripted or CI invocation
  never launches a browser — plus an opt-out flag. This ADR ships the seam;
  the wiring lands with that gate. (Phase 2 outcome: the gate landed on
  `run`/`auto`, which embed the serve live view for the run's duration and
  open it — `--no-web` opts out; `resume` was wired next, through the same
  gate and the same `--no-web` flag, so a resumed leg is watchable exactly
  as a first leg is; a chat graph turn stays un-wired; the standalone
  `serve` subcommand keeps printing the URL.)

  > **Update (2026-08-05):** the last clause of that Phase 2 outcome note is no
  > longer true, and it is a status note rather than part of the decision. The
  > standalone `serve` subcommand was wired in #100: `serveFlags.autoOpener`
  > (`cmd/oh-my-graph/serve.go`) hands the URL to the injected `ExecOpener`
  > through `webOpener` — the same TTY-and-not-opted-out gate `run`, `auto` and
  > `resume` use — and `--no-open` is `serve`'s name for `--no-web`'s opt-out.
  > It still prints the URL; it no longer *only* prints it. The decision this
  > ADR records (the seam, the interface, the caller-owns-the-policy split) is
  > unaffected — this is that policy being exercised by one more caller.

The invariant is **restated, not weakened**:

> Exactly four objects in oh-my-graph may spawn a process —
> `runner.CLIRunner`, `verify.ShellVerifier`, `worktree.GitManager` and
> `browser.ExecOpener` — each behind its own injected interface. No other
> package imports `os/exec`.

`internal/invariants`' exec-seam test moves from three allowed importers to
four — it adds exactly `internal/browser/exec.go` and must still fail on a
fifth — and every "exactly three" claim in the docs and doc comments is swept
to four, citing this ADR.

> **Update (2026-08-05):** "three allowed importers to four" is the wrong
> denominator for what that test actually holds. Its `allowedExecImporters`
> map is keyed by **file**, not by seam, and the runner and verify seams each
> carry two build-tagged `procgroup_*.go` files that import `os/exec` only to
> mutate an already-built `*exec.Cmd`. The map therefore went from **seven
> entries to eight** — it gained exactly one, `internal/browser/exec.go`, which
> is the part of the sentence that was right.
>
> The decision is unaffected: the invariant is over **spawner objects**, and
> that count is three → four exactly as written. The file count is an
> implementation detail of how the invariant is enforced, and the two were
> conflated here. The sentence is left standing rather than rewritten, for the
> same reason as in ADR 0010: a reader checking this ADR against
> `internal/invariants` should find the discrepancy explained, not erased.

The child-environment scrub applies to the launcher too. The URL handler it
dispatches to is arbitrary user-configured code (a `.desktop` entry, a
registry association) that inherits the child environment and may
legitimately invoke claude, so `internal/childenv.Scrub` is applied to every
launcher child and asserted by a unit test on the built `cmd.Env`, exactly
like the other three spawners.

**The seam's tests never spawn.** The git and shell seams run their real
binaries in tests because those are safe to exercise in CI; a real launcher
would pop a browser window on whoever runs the suite. The unit under test is
therefore the built `*exec.Cmd` — argv and scrubbed env — the same
assertion-on-the-command pattern the other seams use for their scrub tests.

## Consequences

**Positive**

- `serve` can meet its user at the feature's only path: phase 2 opens the
  live view the moment the server is up, gated so it only ever happens in an
  interactive terminal.
- Both purposes of the original invariant survive: the subscription-auth
  scrub still has exactly one home per spawner, and the whole engine is still
  testable with zero real spawns (`browser.FakeOpener`).
- The seam is reusable: anything else that ever wants to show the user a URL
  (a future `runs --web`, a docs link) injects the same Opener instead of
  growing a fifth spawner.

**Negative / trade-offs**

- "Three exec objects" is now "four". The rule's bluntness erodes a little
  more each time; the CONTRIBUTING.md wording moves to "a **fifth** needs an
  ADR" and reviewers must enforce the new form.
- The launcher's success only means the handoff to the desktop succeeded —
  `xdg-open` can exit 0 with no browser actually appearing (a misconfigured
  handler). Accepted: the URL is still printed, so auto-open failing quietly
  degrades to exactly the previous behaviour.
- Until phase 2 lands, `ExecOpener` has no production caller: the seam is
  deliberately ahead of its wiring so the invariants test, the docs sweep and
  the ADR land as one reviewable unit, with the behaviour change isolated in
  its own change.

  > **Update (2026-08-05):** historical — this held only until phase 2. The
  > wiring landed: `run`, `auto`, `resume` and the standalone `serve`
  > subcommand all hand the URL to `ExecOpener` behind the TTY gate (see the
  > Phase 2 outcome note above). `ExecOpener` has production callers today.

## Alternatives considered

- **Extend `ShellVerifier` (it already runs arbitrary shell).** Rejected: it
  is the evidence object; opening a browser is not evidence, and the
  composite would give `FakeVerifier` two unrelated subsystems to fake — the
  exact degradation ADR 0002 refused for `NodeRunner` and ADR 0005 refused
  again for worktrees.
- **Exec from `cmd/oh-my-graph` directly.** Rejected outright: a spawn
  outside every injected seam is outside the env scrub's tested call sites,
  and the CLI's serve path would become untestable without popping real
  browser windows.
- **A pure-Go "open browser" library.** Rejected: the existing libraries are
  thin wrappers around exactly these three launcher commands; the dependency
  buys nothing the three-line argv files don't, and puts the spawn inside
  code this repo's invariants test cannot see.
- **Keep print-URL-only forever.** Rejected as the end state (friction on
  `serve`'s only path) but kept as the fallback: the URL is always printed,
  whether or not the launcher succeeds, and phase 2's TTY gate means
  non-interactive callers keep exactly today's behaviour.
