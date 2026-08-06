# ADR 0003 — A gate is a clean stop with a persisted snapshot, not a blocking wait

- Status: Accepted
- Date: 2026-07-29
- Issue: [#9](https://github.com/jitokim/oh-my-graph/issues/9)

## Context

The `gate` node type is schema-reserved and `gate.StubController` always refuses,
so a graph containing a gate halts instead of pausing for approval. Implementing
it means answering one question first: **what does "pause" physically mean for a
process?**

Two shapes were available:

1. **Block in place.** The `run` process stays alive at the gate, waiting on a
   TTY prompt, a file, or a signal, then continues in the same process.
2. **Stop cleanly and resume later.** The process persists enough state, exits,
   and a separate `oh-my-graph resume <run-id>` invocation continues the run.

The surrounding constraints are strong. SECURITY.md states oh-my-graph "never
runs as a daemon" and is a personal, local tool. Every node carries a 20-minute
`context.WithTimeout`. Halt-on-fail works by cancelling a shared context, which
kills in-flight children. And a human approval is measured in hours or days, not
seconds.

## Decision

**Option 2. A gate is a planned stopping point.** The run pauses by exiting.

- `GateController.Evaluate` returns a `Decision` (`approve` / `reject` /
  `pause`) rather than only an error.
- On `pause` the Scheduler stops *launching* new work but **drains** what is
  already in flight instead of cancelling it, so sibling nodes' results are
  persisted and do not have to be re-run and re-paid for. This is the one place
  the halt path deliberately does not cancel the shared context.
- The run snapshot is written atomically (temp file + `rename`) to
  `~/.oh-my-graph/runs/<run-id>/state.json`, carrying a `schema` version.
- `Scheduler.Run` returns a `*PausedError`; `cmd/oh-my-graph` maps it to **exit
  code 2**. `0` = all nodes passed, `1` = the run failed, `2` = paused and
  resumable. A pause is not a failure and must not be reported as one.
- `oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id>)`
  continues. Exactly one of the two flags is required — a resume must never
  silently approve — and the gate id is explicit so resuming an old run cannot
  approve a gate the user was not looking at.
- Which controller answers is chosen once, at the CLI boundary, from the
  invocation: `PauseController` for `run`/`auto`, `RecordedController` (backed by
  the snapshot's decision map) for `resume`. The Scheduler asks the same question
  either way and never branches on "am I resuming".

**The snapshot holds** the normalized graph (plus source path and SHA-256), the
run's inputs and meaning-changing flags, the per-node tool policies for an auto
run, per-node completion records **including the claude session id**, the gate
decisions so far, and which gate the run is paused at.

**The snapshot deliberately does not hold** in-degree counts or the ready set.
Both are derived from `graph × completed` and are recomputed on resume via
`Graph.ReadyGiven(done)`; persisting them would create a second source of truth
that can go stale against the first.

Snapshots are written after **every** node, not only at gates, so the file
always reflects an interrupted or crashed run's progress too. Resuming that
file is out of scope for v1.1, though: `resume` only continues a run whose
snapshot actually recorded a gate pause, and refuses anything else (an
interrupted or crashed run's snapshot included) — see DESIGN.md, "Gate nodes
and resume". A snapshot write failure mid-run is non-fatal, but not cosmetic:
a dropped write leaves that node absent from the persisted state, so a later
resume would not know it ran and would re-execute it, a real cost. It is
warned on the progress feed for that reason; a snapshot write failure **at a
gate pause is fatal**, because reporting a clean pause whose state was not
persisted would be a lie.

## Consequences

**Positive**

- Matches what the tool actually is: a stateless CLI you run, that exits. No
  daemon, no supervised process, no ToS-relevant change of shape.
- Survives the thing that actually happens — closing the terminal, sleeping the
  laptop, going home. A blocked process would not.
- Per-node timeouts stay meaningful. Under option 1 a node's 20-minute bound
  would have had to coexist with a multi-hour parked parent.
- Persisting the session id per node is what makes `handoff: session` work across
  a resume at all; today it exists only in `Handoff.sessions` in memory. The same
  record makes a Ctrl-C'd run resumable for free.
- Exit code 2 gives scripts and CI a way to tell "waiting for a human" from
  "broken", which a single non-zero code could not.

**Negative / trade-offs**

- The run's cost ledger now spans two or more processes. The resumed leg must
  carry forward the earlier leg's per-node costs, or the reported total
  understates what the graph actually spent.
- `state.json` is a new persistence format, therefore a new compatibility
  surface. It gets a `schema` field so an incompatible snapshot is refused
  loudly rather than misread; that is a cost, not a free win.
- Two concurrent `resume` invocations on the same run id would double-run nodes.
  Mitigated with an `O_EXCL` `resume.lock` holding the pid, whose stale-lock
  failure mode has to be explained to the user with the exact path to delete.

  > **Update (2026-08-05):** the "delete it and retry" half of this mitigation is
  > reversed by [ADR 0015](0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md),
  > which names it "an active double-spend footgun": under the flock semantics
  > 0015 decides on, unlinking the file does not release a live holder's lock,
  > so a second leg creates a fresh inode and takes an uncontended lock on it —
  > two schedulers, one run, both spending. Do not follow the advice above.
  >
  > **Update (2026-08-06):** 0015 is now **implemented**, so the mechanism
  > described here is no longer what the binary does either: `AcquireLock` takes
  > an exclusive `flock(2)` on `resume.lock`, and release unlocks without
  > unlinking. The `O_EXCL` description survives in exactly two places, and the
  > "delete it and retry" advice with it — a lock file an *older* binary left
  > behind (refused under these semantics, because they are the only ones its
  > writer knew, an arm that self-expires the moment such a lock is cleared),
  > and a platform with no `flock(2)`, where `AcquireLock` keeps this mechanism
  > in full. A reader can now also tell a live holder from a corpse without a
  > human: see 0015 §1 and docs/RUN-FEED.md's "Liveness" section.
- Resuming re-reads the graph from the snapshot, not the YAML file. That is the
  correct behaviour (artifacts already on disk were produced by the old graph),
  but it will surprise someone who edits the YAML and expects a resume to pick it
  up. The stored SHA-256 exists so the resume can say so out loud.
- Draining rather than cancelling at a pause means a gate does not stop the run
  instantly; a long-running sibling delays the exit by up to its own timeout.
  That is the right trade (work is preserved) but it is a visible latency.

## Alternatives considered

- **Block on a TTY prompt.** Rejected: `oh-my-graph` runs unattended under
  `dontAsk` by design, is routinely piped, and would deadlock with no terminal.
- **Block on a sentinel file / signal.** Rejected: it is a daemon with extra
  steps — the process still has to survive for the human's whole decision, and
  everything about per-node timeouts and context cancellation gets worse.
- **Skip gates and let `--continue-on-fail` approximate them.** Rejected: it
  inverts the semantics. A gate exists to stop *before* the dependents run;
  continue-on-fail is about proceeding after something already went wrong.
- **Persist the ready set and in-degrees.** Rejected: derived state. The graph
  plus the completed set already determines them, and two sources of truth for
  topology is the bug class the inline `depends_on` design was chosen to avoid.
- **Let a gate pause only a subtree under `--continue-on-fail`.** Rejected:
  approving "part of" a paused run later is not a coherent operation, so a pause
  always stops the whole run.
