# ADR 0011 — The live view can decide a paused gate

- Status: Accepted
- Date: 2026-08-03

## Context

A gate pause is a clean stop, not a blocking wait (ADR 0003): the run
persists a snapshot, prints the exact `oh-my-graph resume` command, and its
process exits with code 2. The embedded live view `run`/`auto` starts (ADR
0006) dies with that process, so the *only* view a paused run can be looked at
through is a standalone `oh-my-graph serve` attached to it afterwards.

That view showed the pause and then told the user to go elsewhere: the feed's
`gate_paused` entry carried the `resume --approve <gate>` line as text to
retype in a terminal. The one moment the run genuinely needs a human is the
one moment the human-facing surface could not help — and the decision itself
is a single bit that the page already knows the gate id for.

`internal/serve` was, by construction and by its own doc comment, strictly
read-only: it never wrote, rewrote or deleted anything in a run directory, and
its whole access-control story was "the loopback bind is the access control"
plus a Host check against DNS rebinding. Adding a decision route breaks both
of those sentences, so it is a design decision, not an implementation detail.

Three ways to do it were on the table:

1. Have the handler write the decision into `state.json` itself (set
   `Gate.Decisions[id]`, clear `Gate.PausedAt`) and let some later `resume`
   pick it up.
2. Extract the CLI's resume leg into an importable package and call it from
   `internal/serve`.
3. Invert the dependency: define the resume as an interface in
   `internal/serve`, implement it in `cmd/oh-my-graph` over the existing
   `executeResume`, and inject it.

## Decision

**Option 3, and serve stops being read-only — deliberately and in exactly one
place.**

```go
// internal/serve
type GateResumer interface {
	Resume(ctx context.Context, runID, gateID string, decision gate.Decision) error
}
```

- `POST /api/gate/approve` and `POST /api/gate/reject` are the only mutating
  routes. They own no gate logic whatsoever: they validate that the viewed run
  is paused at the named gate and hand `(runID, gateID, decision)` to the
  injected `GateResumer`.
- The production implementation is `cmd/oh-my-graph`'s `cliGateResumer`, which
  builds the `resumeFlags` a `oh-my-graph resume <run-id> --approve <gate-id>`
  invocation would parse and calls **`executeResume`** — the same function
  `runResume` calls. The lock, the snapshot load, the explicit-gate-id check,
  the merged decision map, the `RecordedController` and the leg are shared,
  not copied. `resume` and the browser are two front-ends onto one resume.
- Only the standalone `serve` process injects a resumer. `startLiveView`
  passes nil and its gate routes answer 409: that view belongs to a run in
  flight, which is not paused and holds the `resume.lock` a leg would need.
- A decision is valid ONLY while the run is genuinely paused at that gate.
  `Gate.PausedAt` on disk is the single source of truth, read under the
  `resume.lock`; a held lock, a missing snapshot, an unpaused run, a
  mismatched gate id, and a missing resumer are each **409**. The client must
  name the gate — ADR 0003's rule that "the gate id is explicit so resuming an
  old run cannot approve a gate the user was not looking at" applies to a
  stale browser tab exactly as it applies to a stale terminal.
- The response is **202 Accepted**: the leg runs for minutes, and the run feed
  is the progress report (the page's SSE connection is already open and
  `/api/events` deliberately does not end at `run_finished`). The leg's
  context is detached from the request, so closing the tab does not kill a leg
  that is spending money.

**Access control gains a token.** The loopback bind and the Host guard do not
protect a mutating route: a page the user is already visiting can POST to
`http://127.0.0.1:8642/api/gate/approve` with a perfectly legitimate Host
header. So every gate POST must carry a per-process random token (32 bytes
from `crypto/rand`, hex), minted in `serve.New`, rendered into the served page
— the one asset no longer shipped byte-for-byte — and compared in constant
time: missing is 400, wrong is 403. Sending it as a custom header rather than
a form field also forces a CORS preflight a cross-origin form cannot satisfy.
It is a CSRF guard, not a login; widening the bind address would still need a
real auth story first.

**The exec-seam invariant is untouched.** `internal/serve` imports no
`os/exec` and starts no process; the resumed leg's nodes run through
`runner.ClaudeCLIRunner` — seam 1 — constructed in `cmd/oh-my-graph` and
injected, exactly as every other collaborator in this repo is. There is no
fifth spawner. What did change is *which process* may spawn: a `serve` process
now can, where before it never did. That is the boundary this ADR records.

## Consequences

**Positive**

- The one moment a run needs a human is answerable where the human is already
  looking, with the gate id filled in by the page rather than retyped.
- No forked gate logic can exist: `internal/serve` has no way to write a
  decision, and the CLI's `executeResume` is the only implementation. A change
  to how a resume works changes both front-ends at once.
- The read-only-ness that remains is now precise rather than sweeping: one
  route pair mutates, everything else reads, and the doc comments, DESIGN.md
  and this ADR all say which.

**Negative / trade-offs**

- "serve is read-only" was a sentence a reader could trust completely, and it
  is gone. Every place that stated it had to be corrected in the same change,
  and future review must keep the exception at exactly one route pair.
- A `serve` process can now spawn `claude` children and spend money. It is
  the same spend the printed `resume` command would have made, initiated by
  the same human, but it happens under a long-lived server process rather than
  a foreground command.
- 202 means the response cannot report how the leg ended. The feed carries the
  outcome to the browser and the resumer prints failures to the serve
  process's stderr; a viewer who closes the tab learns the result from
  `runs list` or the next `serve`.
- The token lives in the page, so a page kept open across a `serve` restart
  stops being able to decide (the new process minted a new token). A reload
  fixes it; the refusal says why.

## Alternatives considered

- **Write the decision into `state.json` from the handler.** Rejected: it is
  the fork this ADR exists to prevent. It duplicates what
  `runstate.SnapshotRecorder` owns, and it leaves a run whose pause was
  cleared but whose gated nodes nobody ever ran — a state no CLI path can
  produce and none knows how to read.
- **Extract the resume leg into an importable package so `internal/serve` can
  call it.** Rejected as unnecessary surgery for this change: the dependency
  inversion above already yields exactly one implementation, while moving
  `continueRun` and its five helpers out of `cmd/` would touch the whole
  resume/retry/feedback path — including the fresh-run reporting it shares —
  to serve one new caller. The seam is where the repo already puts seams; the
  leg stays where its collaborators are wired.
- **Give the embedded live view a resumer too.** Rejected: a run in flight is
  not paused and holds its own `resume.lock`, so the button could only ever
  fail. 409 with a reason is the honest answer, and ADR 0003 means the case
  cannot arise in the first place.
- **No token; rely on the loopback bind and the Host check.** Rejected: both
  pass for a cross-origin POST from a page the user is visiting. A mutating
  route that can start a paid, repo-touching leg is not something to leave on
  a guard that was only ever designed to stop reads.
- **Keep the CLI line as the only way.** Rejected as the end state — it is
  friction at the feature's most important moment — but kept as a first-class
  fallback: the command stays on the paused entry, and it remains the only way
  in from an embedded view.
