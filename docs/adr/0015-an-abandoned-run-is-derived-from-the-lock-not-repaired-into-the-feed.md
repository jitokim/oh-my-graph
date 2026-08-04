# ADR 0015 — An abandoned run is derived from the lock, never repaired into the feed

- Status: Accepted
- Date: 2026-08-04

## Context

Two runs on the maintainer's machine have read `RUNNING` for over a day:

```text
t95b       20260803-125311.878285000-1   25:40 elapsed  $0.0000   1 pending
fragments  20260803-121317.978943000-1   26:19 elapsed  $28.9871  1 pending
```

Both processes died on 2026-08-03. Their event streams end mid-leg — the last
line of each is a `node_started` that never got a terminal event:

```text
20260803-125311…  run_started 13:08:39.87 → node_started dev  (session c258b0fd…)   [nothing after]
20260803-121317…  run_started 14:08:24.52 → node_started impl (session d990e3e0…)   [nothing after]
```

That is enough for `runfeed.InFlight` (`internal/runfeed/reader.go:89`) to
report them open, because its rule is purely the stream's: a run is in flight
exactly when its last leg is still open. A killed process never writes its
`run_finished`, so its leg stays open forever. The limitation is not a
surprise — `InFlight`'s own doc comment already confesses it ("Known
limitation, accepted for v1… there is no liveness probe here, deliberately,
to keep every caller a pure reader of the two contract files"). This ADR
retires that acceptance.

Everything downstream inherits the lie, because everything downstream was
built — correctly — to agree with `InFlight`:

- `runs list` renders verdict `RUNNING` (`cmd/oh-my-graph/runs.go:121,151`).
- The dashboard paints the card `running` (`internal/serve/card.go:118,138`),
  with the dead node still spinning.
- `serve` with no run id *prefers* an in-flight run, so it parks on a corpse
  instead of showing the newest real run (`internal/serve/resolve.go:56`).
- `watch` tails forever; its own doc comment names the same gap ("A stream
  that ends without a `run_finished` … is indistinguishable from a slow
  node", `cmd/oh-my-graph/watch.go:49`).

This is not cosmetic. `assets/dashboard.png` — the screenshot in the README,
the project's shop window — shows those two runs claiming to be in flight. A
reader who notices `$0.0000` and one pending node after nine hours concludes
the tool leaks runs. And a real operator cannot tell a run that is thinking
from a run that is dead, which is the difference between waiting and
resuming.

### The pid in `resume.lock` is not a liveness test — measured

The obvious fix is the pid `internal/runstate/lock.go` already writes. It is
wrong on its own, and the refutation is on disk:

| run | lock pid | `ps -p <pid>` | truth |
|---|---|---|---|
| fragments | 99429 | dead | correctly diagnosed |
| t95b | 80834 | **ALIVE** | wrong — recycled |

`ps -o lstart,command -p 80834` showed a `/bin/zsh` polling loop started
2026-08-04 08:23, nearly twenty hours *after* that run began on 2026-08-03
12:53. The pid had been recycled by an unrelated process.

Re-measured for this ADR at 2026-08-04 23:35 KST: pid 80834 is now gone too
(the zsh loop has since exited), so the same probe against the same unchanged
run directory answers "alive" in the morning and "dead" at night. **That
non-determinism is the refutation**, and it is worse than a plain false
positive: a bare `ps -p` probe reports an abandoned run as live for as long as
the machine happens to hold that pid, which is a function of everything else
the machine started. Any design resting on the pid alone is already refuted.

### This ADR argues against a decision the code records

`lock.go`'s doc comment states the current position: "AcquireLock does not
check whether that pid is still alive; a stale lock is a human decision, per
`LockHeldError`." DESIGN.md says the same ("A stale lock is reported with the
exact path to delete"), and ADR 0003 recorded it as the mitigation for a
crashed gate leg.

That decision was right for the question it was answering, and this ADR does
not claim otherwise. When the only consumer of the lock was `AcquireLock`, the
question was *"may I start a second leg?"* — and for that question, refusing
conservatively and handing the judgement to a human is a good answer, because
the cost of guessing wrong is double-running paid nodes and the cost of
guessing right is one line of advice. What changed is that a **second**
question now rides on the same fact — *"is this run in flight?"* — asked by
pure readers (`runs list`, a dashboard tick every poll) where "ask the human"
is not an available answer, and where the conservative default is not
safe-but-annoying. It is simply wrong, visibly, for a day and counting. The
old decision is not overturned so much as out-scoped: it needs a mechanical
answer now, and the reason it did not have one was that the pid could not
give it.

## Decision

### 1. Liveness is the kernel's `flock(2)` on `resume.lock`, not the pid

**A leg holds an exclusive `flock(2)` on its run's `resume.lock` for its whole
duration. A run is in flight exactly when its last leg is open *and* that
flock is still held.** The kernel releases the lock when the holding process
dies, however it dies, so no pid, no start time, and no clock is consulted.

- `AcquireLock` opens the file `O_CREATE|O_RDWR` (**not** `O_EXCL`) and takes
  `LOCK_EX|LOCK_NB`. Failure with `EWOULDBLOCK` is `LockHeldError`. The
  flock — not the file's existence — *is* the lock.
- **Only once the flock is held** does the holder truncate the file and rewrite
  it: a format marker line (`oh-my-graph-lock 1`), then the pid. The truncation
  cannot be folded into the open flags — an `O_TRUNC` applied at open time runs
  *before* the lock is taken, so a caller that then loses the race would have
  already blanked the live holder's file. The pid stays informational, but its
  role inverts and improves: useless as a liveness *test*, it is trustworthy as
  a *label* once liveness has been established independently, so
  `LockHeldError` can name the process a user is waiting for.
- The read-time probe opens `O_RDONLY` (**no `O_CREATE`**), tries
  **`LOCK_SH|LOCK_NB`** (§"A shared probe, not an exclusive one" below), and on
  success immediately `LOCK_UN`s and closes. Readers therefore never create,
  write or remove anything — `runs list`'s documented read-only posture over
  run directories survives intact.
- **The release path unlocks and closes. It does not unlink** — this is a
  change from today, and it is load-bearing. See "Release must not unlink"
  below.
- **`ENOENT` is *unknown*, not free**, and so is a lock file whose first line
  is not the marker. Both are covered under "Absence is not evidence" below.

Measured on darwin 22.6.0 / go1.25.0 with a throwaway probe (not committed):

| measurement | result |
|---|---|
| `kill -9` the holder, then probe | **FREE** — the kernel released it, no cleanup path ran |
| holder `exec`s a child that outlives it (child pid confirmed running), holder exits, then probe | **FREE** — Go opens with `O_CLOEXEC`, so the lock fd does not reach the child |
| second `flock` from a **different fd in the same process** | **HELD** — flock conflicts per open file description, not per process |
| `flock` on an `O_RDONLY` fd, against a held lock / a free one | **HELD / FREE** — a read-only fd is a valid probe |

Two things the table does **not** cover, and that the implementation must pin
with tests rather than inherit from this ADR: `LOCK_SH` against a held
`LOCK_EX` (standard BSD semantics, but the probe now depends on it), and every
row above re-run on linux (see "What could not be determined" #1).

Judged on the axes that matter:

- **False "alive" — impossible by construction.** There is no name to recycle.
  The t95b failure cannot occur.
- **False "dead" — the dangerous direction — is enumerable, and every case now
  resolves to *unknown* rather than to "abandoned".** A leg started by a
  pre-ADR binary is caught by the marker; a lock file a human deleted, or a run
  directory predating the lock, is caught by `ENOENT`; a filesystem whose flock
  is not the flock this design assumes is caught by the filesystem gate. The
  three cases are worked through in "Absence is not evidence" and "Not every
  `flock` is this `flock`" below. This matters more than it did in the first
  draft of this ADR, because §4 removes the human from `resume`: a false-dead
  is no longer a wrong pixel that self-corrects on the next poll, it is a
  standing authorisation for a second scheduler to spend money on a live run.
  Each of these three had to become a mechanism, not a caveat.
- **Portability.** `.goreleaser.yaml` builds darwin and linux only, and
  `syscall.Flock` is defined on both; `syscall` is already imported across
  `cmd/oh-my-graph`. No new module dependency (the repo has exactly two:
  `golang.org/x/sync`, `gopkg.in/yaml.v3`). A build-tagged fallback for any
  other GOOS reports *unknown* and preserves today's answer, so the tree still
  builds where the project does not ship.
- **No process spawn.** Two syscalls on an already-open fd. The four exec
  seams (ADRs 0002, 0005, 0006) are untouched, and no fifth seam ADR is owed.
  This was a hard constraint, and it eliminates every `ps`-shaped design on
  its own.
- **Silence is never the criterion.** This reads a kernel fact, not a clock.
  A node legitimately silent for the full 1h timeout the shipped graphs allow
  is indistinguishable from a fast one here, because elapsed time is not an
  input.

**`flock(2)`, not `fcntl(F_SETLK)`.** POSIX record locks are per *process*:
the embedded live view a `run` starts in the very same process that holds the
lock would be granted the lock it is trying to probe, and would declare its
own live run abandoned. The measurement above ("second fd in the same
process → HELD") is exactly the property that makes flock safe here. Record
locks carry a second trap: they are dropped when *any* fd on that file is
closed by the process, so a long-lived reader that also opens the lock file
for its pid would silently release a lock it holds. `F_GETLK`'s ability to
report a conflicting holder without acquiring is genuinely nicer — and not
worth either hazard.

**Release must not unlink, and this is the sharpest edge in the design.** The
obvious release path — unlock, close, `os.Remove`, exactly what `lock.go` does
today — reintroduces the double-spend this ADR exists to prevent, without any
pid, any exotic filesystem, or any human:

Acquiring is two steps — open the path, then flock the fd — and an unlink
between them decouples the two:

1. Leg X holds the flock on inode I.
2. Leg A calls `AcquireLock`: it opens the path and gets an fd on I. It has not
   flocked yet.
3. X finishes: unlock, close, **unlink I**. The path now names nothing; A's fd
   still names I, which has no holder.
4. Leg C calls `AcquireLock`: `O_CREATE` makes a *new* inode I2, and `LOCK_EX`
   on it succeeds uncontended.
5. A flocks its fd on I. That also succeeds — nothing holds I.

**A and C both believe they hold the run's lock: two schedulers, one run, both
spending** — the exact failure this ADR exists to prevent, reachable with no
pid, no exotic filesystem and no human. So: **the release path unlocks and
closes, and the file stays.** A `resume.lock` becomes a permanent, inert
resident of every run directory, like `state.json` — it is a handle, not a
flag, and nothing anywhere reads its existence as a state. The alternative fix
(`fstat` the fd, `stat` the path, require matching dev/ino after every acquire)
works too and is strictly more code for the same guarantee; not unlinking
removes the failure rather than detecting it.

Note that nothing above depends on retrying a failed acquire. An earlier draft
of this decision proposed exactly that — a few `LOCK_EX` retries across a short
window, to smooth over the probe race §"A shared probe" now removes at the
source — and it would have made this far worse, by holding an fd on a doomed
inode for as long as the retry window lasts. It is gone, and no retry loop
appears anywhere in this decision.

This edge is not hypothetical in this codebase: `serve`'s `pausedSnapshot`
(`internal/serve/gate.go:244`) acquires and releases the lock on **every gate
POST**, so under an unlinking release it would churn the inode on the hot path.

**A shared probe, not an exclusive one.** The probe takes `LOCK_SH`, not
`LOCK_EX`. A shared lock conflicts with the holder's exclusive one — which is
the entire question the probe asks — but not with other shared locks, and that
difference decides three things:

- **No reader-vs-reader flicker.** Under an `LOCK_EX` probe, a reader that
  finds the lock free holds it for the two syscalls before releasing, and a
  concurrent reader in that window sees "held" — a transient false *alive*. Two
  dashboard ticks and a `runs list` are enough. `LOCK_SH` removes the case
  entirely rather than declaring it cosmetic.
- **No compounding false-busy.** An `LOCK_EX` probe blocks `AcquireLock`, and
  because §3 publishes the probe to third-party consumers, that contention
  would scale with the number of pollers. `LOCK_SH` probes overlap freely, so
  the only window that can refuse a starting leg is one reader's two syscalls,
  and it does not grow with the audience. What remains is still a false *busy*:
  the leg refuses to start, the safe direction, and exactly what the lock is
  for.
- **It is the honest primitive to put in a public contract.** Telling external
  consumers to take an exclusive lock in order to *read* invites them to
  interfere with the thing they are observing.

**Absence is not evidence.** A missing or unmarked lock file means *unknown* —
i.e. today's answer, an open leg reads as in flight — never "free". Since
`AcquireLock` creates the file (and, with the release path above, never removes
it), a run directory belonging to a post-ADR binary always has one. So:

- **`ENOENT`** means the directory predates this change, or a human deleted the
  file following the advice §4 retires. Both are live-run possibilities.
- **A first line that is not the marker** means a pre-ADR binary wrote it: a
  leg holding no flock, whose lock file is indistinguishable from a released
  one. This is what makes the upgrade safe, and this project needs it more than
  most — `graphs/self-dev` rebuilds and reinstalls this very binary while runs
  are in flight, so "a leg started by the old binary while the new one reads"
  is a routine local event, not a migration-day edge case. The marker costs one
  line in the writer and one comparison in the reader. **It governs the acquire
  path as well as the read path** — §4's `resume` bullet works that out, and it
  is the half that actually prevents the double-spend rather than merely
  declining to render it.

**Not every `flock` is this `flock`.** On linux, `flock()` over NFS is
emulated as whole-file POSIX record locks. That silently restores *both*
hazards this design rejects `fcntl` for, and the consequences land in the
dangerous direction:

- Record locks are per-process, so the embedded live view — which runs **in the
  same process that holds the lock** (`main.go:373` acquires, `startLiveView`
  at `main.go:419` serves in-process; `cmd/oh-my-graph/serve.go:141` states the
  relationship) — would be granted its own run's lock and paint a live run
  ABANDONED.
- Record locks are dropped when *any* fd on the file is closed by the process,
  so the probe closing its own read-only fd would **release the run's real
  lock**, letting a concurrent `resume` in.

Neither path returns an error, so the *unknown* fallback below never fires.
This is therefore an **implementation gate, not a contingency**: before probing,
the reader checks the run directory's filesystem type (`statfs`), and on
anything that is not known-local it reports *unknown* **and does not open the
lock file at all** — the second half matters, because on those filesystems the
open-and-close is itself the hazard. Acquisition is left alone: over NFS,
per-process record locks still give correct mutual exclusion between separate
oh-my-graph processes (that is what NLM is for), so `AcquireLock` keeps working
and only the *reading* of liveness degrades to today's answer.

**A probe that errors is *unknown*, and unknown means today's answer.**
`ENOTSUP`/`EOPNOTSUPP` from an exotic filesystem, a permission error, any
unexpected failure: the reader falls back to the pre-ADR interpretation (open
leg ⇒ in flight). A run is declared abandoned only on an affirmative "the
lock is free". Nothing is ever abandoned because a probe failed.

### 2. `ABANDONED` is derived at read time; no reader ever writes

**A reader never repairs the feed.** It derives a three-valued answer from
two facts it already has access to, and writes nothing:

| last leg | lock | run reads as |
|---|---|---|
| closed | (not consulted) | settled — `PASS`/`FAIL`/paused, as today |
| open | held | **in flight** — as today |
| open | *unknown* — absent, unmarked, unsupported filesystem, probe error | **in flight** — as today, and this is the arm every doubt falls into |
| open | free | **abandoned** |

Read the third row as the load-bearing one. `ABANDONED` requires **two
affirmative facts**, never the absence of one: an open leg in the stream, *and*
a `LOCK_SH` that actually succeeded against a marked lock file on a filesystem
whose flock means what §1 assumes. Everything else is today's answer.

The rejected alternative was to have the first reader that notices append a
terminal event on the dead writer's behalf. Five reasons it loses:

1. **It writes to an append-only published history.** docs/RUN-FEED.md sells
   `events.jsonl` to external consumers as a stream only the run's own
   scheduler appends to. A `run_finished` that no scheduler emitted is a
   forged line in someone else's audit trail.
2. **The line would have to lie about time.** Its `ts` would be the reader's
   clock — hours after the process died — for a transition that never
   happened. Every other event in the file is stamped at its emission.
3. **`runs list` would write to disk.** A read-only listing that mutates run
   directories fails on a read-only mount, on another user's runs root, and on
   any operator's reasonable expectation. `listRuns` documents itself as
   read-only ("never deleted or rewritten") and that property is worth more
   than the repair.
4. **Repair is irreversible; derivation is self-correcting.** A false-dead
   derivation is fixed by the next read the instant the truth changes. A
   false-dead *repair* permanently closes the leg of a run that is actually
   alive — and that run will later append its own genuine `run_finished`,
   leaving a stream with two closes for one open. Given that false-dead is the
   dangerous direction, the option that makes it permanent is the wrong one.
5. **It races.** `runs list`, a dashboard tick and a `serve` resolve can all
   notice the same corpse at the same moment and each append.

**Nothing needs repairing for `resume` to work anyway.** `InFlight` simply
toggles on `run_started`/`run_finished`, so a new leg appended after an
unclosed one self-heals the stream's leg state the moment that leg finishes.
The dead leg's open bracket is a permanent, accurate record that a leg
started and never reported an end — which is precisely what happened.

**Where the composition lives.** `runfeed` stays what it is: a pure,
stdlib-only reader of the stream that knows nothing about locks or processes,
so `InFlight` keeps its exact current meaning ("the last leg is open"). The
probe belongs to `internal/runstate`, which already owns the lock file. The
derivation rule — *in flight = open leg AND held lock* — is stated once and
shared, and the existing cross-surface agreement test
(`TestBuildCard_InFlightAgreesWithRunfeed`) is extended to judge the three
surfaces against the same rule, so `runs list`, the dashboard card and
`ResolveRun` cannot drift apart the way they would if each composed the two
facts by hand.

**The ordering invariant this derivation silently rests on, named.** *A leg
must hold the flock before it writes its first event, and must still hold it
after it writes its last.* Otherwise a starting run has an open leg and no
lock — abandoned — for the milliseconds between, and a finishing one has the
same gap at the other end. Today the code satisfies this by accident of
arrangement rather than by statement: `executeGraph` acquires at
`main.go:373`, opens the feed writer at `main.go:399`, and `defer` LIFO puts
`release()` after `feed.Close()`; `executeResume` does the same
(`resume.go:77` before the load and everything downstream). Moving the acquire
below the feed writer would make every run in the world read abandoned for its
first instants, and nothing would fail. The implementation states this as an
invariant and pins it with a test that asserts the lock is held across the
first and last event of a leg.

### 3. The consumer contract: no schema bump, but `resume.lock` leaves the internal set

**No new event type. No new field. No new verdict value in either file. Both
schemas stay 2.** `state.json` and `events.jsonl` are byte-identical to what
they are today, before and after this change.

So the answer to "what does an existing consumer that has never heard of
`ABANDONED` do when it arrives?" is: **nothing arrives.** It reads the same
bytes and derives what it derives today — an open leg, therefore in flight.
That is a *less precise* answer, not a wrong one, and it is exactly the answer
it already gets. Under docs/RUN-FEED.md's compatibility rule a bump is owed
when a change could be **misinterpreted**; nothing here can be, because
nothing in the versioned files changed. This is also why a new
`run_abandoned` event type was refused independently of §2: the event-type set
is closed per schema version, so it would force a schema bump on every
consumer — the same trade ADR 0009 declined for `node_limited`.

**But `resume.lock` stops being internal, and the document must say so.**
docs/RUN-FEED.md currently ends with "Anything in the run directory not listed
here (lock files, temp files) is internal and carries no compatibility
promise", and opens with the stronger promise that oh-my-graph's own read-back
commands read "exactly what they read … with no side channel". If oh-my-graph's
readers consult the lock and the document keeps calling it internal, that
promise becomes false — the tool would know something no external consumer
could derive. Honesty is cheaper than the alternative, so the implementation
**promotes `resume.lock` into the contract**: one short "Liveness" section
listing it beside `state.json` and `events.jsonl`, defining exactly what a
consumer may conclude from it —

- an exclusive `flock` is held on this file for as long as a leg is running, so
  a consumer probes with a **shared** (`LOCK_SH`) lock on a read-only fd and
  never writes, creates or removes anything;
- a *contended* probe beside an open leg means the writer is alive; a
  *succeeding* probe beside an open leg means the writer is gone;
- **a missing file, a file whose first line is not the marker, and any probe
  error all mean *unknown*, and unknown means the open-leg rule** — today's
  answer, and a safe one, which is also what a consumer that cannot or will not
  flock should use unconditionally.

The file's *contents* gain exactly one promise, the marker line that identifies
the format; everything after it, the pid included, stays explicitly
informational and explicitly not a liveness test.

### 4. The surfaces

- **`runs list`** gains the verdict word `ABANDONED` beside `RUNNING`.
  Deliberately not `FAIL`: a `FAIL` is a verdict about the *work*, and the
  work has no verdict — the same argument ADR 0009 used to refuse marking a
  session-limited node FAILED. `ABANDONED` is a statement about the process.
  The row is followed by the one-line recovery hint from §5.

  **And its snapshot-less arm must widen, or the change hides the runs it
  exists to reveal.** A run killed before its first node settles has no
  `state.json` at all, and `summarizeRun` excuses that only
  `if inFlight && errors.Is(err, fs.ErrNotExist)` (`cmd/oh-my-graph/runs.go:132`).
  The moment such a run derives abandoned, `inFlight` is false, the excuse
  lapses, and the directory becomes `WARNING: skipping run …` — it **vanishes
  from `runs list`**, where today it at least shows a RUNNING placeholder row.
  The guard's real intent was "not settled", of which in-flight was the only
  case; it becomes *in flight **or** abandoned*, rendering an ABANDONED row
  with the same `-` placeholders. `internal/serve/card.go:144` already handles
  the snapshot-less case on its own terms and needs no equivalent widening;
  this is a CLI-only regression, and it is the single easiest way to ship this
  ADR as a net loss.
- **The dashboard card** gains `stateAbandoned` with its own CSS token —
  muted, not red, since nothing failed. Nodes the stream left open in an
  abandoned run render abandoned rather than spinning forever, and tally as
  pending (`tally`'s existing default arm), so no count field changes shape.
- **`ResolveRun`** no longer prefers an abandoned run as "the run happening
  right now"; it falls through to the newest, which is what a user typing
  `oh-my-graph serve` meant.
- **`watch`** probes once at startup and refuses to tail an already-abandoned
  run, saying so and exiting, instead of hanging on a stream that will never
  get another line. It deliberately does *not* gain an idle-time probe for the
  run dying mid-watch: `runfeed.Follow` has no idle hook, and adding one would
  push liveness into the pure stream reader §2 keeps pure. The remaining hang
  — you were already watching when the process died — is accepted and stated.
- **`resume` stops asking the human, because it is no longer guessing.** A
  held flock now means a live holder, full stop, and the refusal stands
  exactly as today (this is the safe direction, and the whole point of the
  lock). A free lock on a **marked** file means there is no leg, so `resume`
  acquires it and proceeds, with no question to put to a human.

  **The marker selects the semantics, and this is what makes the upgrade
  safe.** A reader can report *unknown* on an unmarked file, but `AcquireLock`
  cannot: it would take an uncontended `LOCK_EX` against a live pre-ADR leg —
  which holds no flock at all — and double-run the run, and no amount of
  read-side caution prevents that. So the acquire path reads the file too, and
  branches: **marked ⇒ flock semantics** (the flock is the whole answer, no
  human, no `rm` advice); **unmarked or absent-but-present-before ⇒ legacy
  semantics** (the file's existence is the lock, the human decides, and the old
  `LockHeldError` with its exact path to delete is the correct message,
  because pre-ADR semantics are the only ones that lock file was written
  under). The legacy arm is self-expiring: the moment such a lock is cleared,
  the next acquire writes a marked one, and the run is on the new semantics
  forever. This costs one branch and retires the upgrade false-dead outright
  rather than listing it as an accepted trade.

  **The "delete it and retry" advice goes with it — on the flock arm.** Under
  flock semantics that advice becomes an active double-spend footgun: unlinking the
  file does *not* release the live holder's lock (it holds it on the now
  unlinked inode), while a second leg would happily create a fresh file and
  take an uncontended flock on the *new* inode — two schedulers, one run, both
  spending. It is the same mechanism as the release-path hazard in §1, reached
  by hand instead of by `defer`. `LockHeldError`'s message becomes "a leg of
  this run is in flight (started by pid N); wait for it, or stop it" — and the
  pid is worded as a **label, not a target**. With `O_EXCL` gone a lock file is
  reopened and reused, so the write must truncate (§1) or the message would
  quote a stale pid; and even a freshly written one is only trustworthy
  *because* the flock says its writer is alive, which is exactly the inference
  the Context refutes when made the other way round. "Stop it" is deliberately
  not "`kill N`".
- **`serve`'s gate button** is fixed: `pausedSnapshot`'s permanent 409 on a
  dead leg's stale lock ("a leg of run X is in flight, so it is not paused for
  a decision") disappears, because the kernel already released that lock. Note
  what this means for the hazard below — the button **starts a leg** (ADR 0014's
  one deliberate crossing of the live view's read-only boundary), so it is a
  second spender, not a reader.

**The residual hazard, named — and it is the common case, not the exotic one.**
The measurements show the lock fd is `O_CLOEXEC`, so a `claude` child does not
keep it alive. That is what makes the probe truthful about *oh-my-graph*, and
it means a death that takes the engine without taking its children leaves an
orphaned `claude` still running and still spending while the run reads
`ABANDONED`. A `resume --retry-failed` would then relaunch that node alongside
its own orphan.

An earlier draft of this ADR claimed those deaths were rare, because "a
`Ctrl-C`/SIGTERM stop signals the whole process group and takes the children
with it". **That is false against this code, and the correction is the honest
statement of the ADR's own worst consequence.** Both spawning seams set
`Setpgid: true` (`internal/runner/procgroup_unix.go`,
`internal/verify/procgroup_unix.go`) — every `claude` child is *deliberately*
in a process group of its own, precisely so the engine can kill it without
killing itself. A terminal SIGINT reaches only the foreground group, i.e. the
engine. What actually reaps the children is the engine's own in-process
`cmd.Cancel` → `killProcessGroup` (`internal/runner/claude.go:165`), driven by
`signal.NotifyContext`, which covers Interrupt and SIGTERM **only**
(`main.go:192,217`). So a closed terminal or dropped ssh (SIGHUP), a `kill -9`,
a panic and an OOM kill each orphan a spending `claude` — and those are the
ordinary ways a multi-hour run dies, which is to say they are the ordinary way
an abandoned run comes to exist in the first place. The population of runs this
ADR newly makes resumable is skewed *toward* the orphan case, not away from it.

Scope of the risk, corrected: no surface auto-resumes, and none is proposed,
but there are **two** deliberate spenders, not one — `resume` on the CLI, and
the dashboard's gate button, which §4 above un-blocks on exactly these runs and
which is one click. The recovery hint therefore has to reach both surfaces: the
`ABANDONED` row, `watch`'s refusal, `resume`'s own output, **and** the
dashboard card, which must say what it is about to allow before the button is
pressed. The hazard also exists today (the human deletes the stale lock just as
blindly); this ADR makes it materially easier to reach by removing the human
from both paths, and that is the cost being accepted here.

### 5. Recovery: nothing new

No `runs prune`, no `--force-finish`, no new command or flag.

- **The state needs no cleaning** — it was never wrong on disk, only misread.
  Fixing the derivation fixes both zombie runs and the screenshot with them.
- **Resuming already exists — for a run that got as far as a snapshot.**
  `resume <run-id> --retry-failed` is precisely the right command: ADR 0009
  already widened it from "retry failures" to "also launch un-recorded,
  launchable work", and an abandoned run's un-settled nodes are exactly that
  shape (the `dev` and `impl` nodes above have no terminal record at all). The
  surfaces print it; that is the whole recovery feature.
- **The one shape it does not cover, stated rather than implied.** A run killed
  before its first node settles has no `state.json`, and `executeResume` loads
  the snapshot before it branches (`cmd/oh-my-graph/resume.go:83`), so `resume`
  fails outright on it — `--retry-failed` included. This is not a regression
  and not a gap this ADR opens; it is simply the truth about the death shape
  most likely to need recovery, and the first draft asserted past it. Nor is it
  worth new machinery: such a run has no recorded graph, so there is nothing to
  resume *from* — the only honest recovery is to run the graph again. The
  requirement this puts on the implementation is therefore about words, not
  code: a snapshot-less `ABANDONED` run's hint must say "re-run the graph", not
  "resume", and `resume`'s own error on that run must say why rather than
  surfacing a bare "no such file". Making it visible in `runs list` at all is
  the separate §4 fix above.
- **`--force-finish` is §2's repair option wearing a flag.** Same forged
  event, same append-only violation, now with a human to blame.
- **`runs prune` is tomorrow's problem.** Deleting run directories is `rm -rf`;
  the tool owes no verb for it, and a prune that decides on a heuristic which
  paid-for artifacts to destroy is the kind of feature this project does not
  ship before someone asks for it.

## Consequences

**Positive**

- The two runs in the README screenshot start telling the truth, and so does
  every future one. The distinction an operator actually needs — thinking vs.
  dead — becomes mechanical.
- The lock answers both questions it is now asked with one primitive, so
  `AcquireLock`'s refusal and a reader's verdict can never disagree.
- A crashed leg stops blocking its own recovery: `resume` and the dashboard's
  gate button both work again on a run whose process died, with no `rm` and no
  guessing.
- Nothing in the versioned contract moves. External consumers need no change,
  and the ones that want the new precision get a documented, language-agnostic
  syscall rather than a private convention.
- No new dependency, no new exec seam, no new command, no background writer.

**Negative / trade-offs**

- **`resume.lock` becomes contract surface.** It was internal; it is now a
  documented file with two promised properties (an exclusive flock for a leg's
  duration, and a format marker as its first line). That is a real narrowing of
  future freedom — the lock's mechanism can no longer change silently.
- **`resume.lock` is never deleted**, so every run directory keeps one forever.
  A trivial cost in bytes, and the price of closing §1's release-path
  double-spend; it also means the file's presence stops carrying information,
  which is the property the derivation wants.
- **The orphaned-`claude` case in §4** is a real double-spend path behind a
  deliberate `resume` *or* a dashboard gate click, and it is reachable by the
  ordinary deaths (SIGHUP, `kill -9`, panic, OOM) rather than only by exotic
  ones. Mitigated by wording on four surfaces, not by code. This is the largest
  cost this ADR accepts.
- **`watch` still hangs** if the run dies while it is already tailing.
- **A snapshot-less abandoned run cannot be resumed at all** (§5) — pre-existing,
  now named and surfaced instead of silently failing.
- **Three false-dead conditions become *unknown* rather than mitigations on
  paper**, at the cost of three mechanisms that must actually be implemented:
  the marker line (pre-ADR legs), `ENOENT`-as-unknown (deleted or pre-ADR
  directories), and the filesystem gate (non-local flock). Skip any one of them
  and §4's removal of the human turns that case into an authorised double-run.
- **The lock has two semantics for one release cycle**, selected by the marker:
  legacy (existence, human, `rm` advice) for files a pre-ADR binary wrote, flock
  for everything after. Two code paths and two `LockHeldError` messages, in
  exchange for retiring the upgrade false-dead instead of accepting it. The
  legacy arm self-expires and can be deleted in a later release; the ADR does
  not schedule that removal, because nothing forces the choice now.
- **Windows** needs a build-tagged stub, following the existing
  `procgroup_{unix,windows}.go` pattern. The project does not ship it, so the
  stub reports unknown and preserves today's behaviour.

## What could not be determined

1. **Nothing was measured on linux.** Every measurement in this ADR is darwin
   22.6.0 / APFS. `syscall.Flock` is defined for linux and the semantics are
   the standard ones, but the three properties this design leans on — the lock
   fd not surviving `exec` (`O_CLOEXEC`), a second fd in the same process
   conflicting, and `LOCK_SH` conflicting with a held `LOCK_EX` — must be
   pinned by tests that run in CI on linux before this is trusted there. That
   is an implementation gate, not a hope. The converse is worth stating too:
   CI is ubuntu-only (`.github/workflows/test.yml:10`), so **darwin — where
   every measurement above was taken, and where the maintainer runs — will
   never be regression-tested**. The linux gate is the one that is available,
   not the one that covers the primary platform.
2. **flock over a network filesystem could not be measured** — no NFS or SMB
   home was available — but it no longer needs measuring to be handled, because
   the linux emulation is documented rather than unknown: over NFS, `flock()`
   becomes whole-file POSIX record locks, which is the `fcntl` design §1
   rejects, with both of its hazards and no error to trip the *unknown* path.
   §1's filesystem gate is the answer, and it is an implementation gate, not a
   contingency. What remains genuinely undetermined is the **exhaustiveness of
   the "known-local" list** — how many filesystem types must be enumerated
   before the default-to-unknown arm stops firing on ordinary setups (overlayfs
   in a container, ZFS, a FUSE home). An over-strict list costs precision, not
   safety: it reads as "in flight", which is today's answer.
3. **pid recycling speed on linux was not measured.** The observed trap was on
   darwin; linux `pid_max` defaults are typically smaller, so the direction is
   worse, not better. This only reinforces the rejection of pid-based designs.
4. **Whether anything parses `runs list`'s verdict column.** Unknown. Treated
   as not a contract, because docs/RUN-FEED.md promises the files and says
   nothing about the tables; a script matching `RUNNING` will simply stop
   matching abandoned runs, which is the point.
5. **Whether either zombie run left an orphaned `claude` child.** Unknowable
   now — the fragments run's $28.9871 was spent before the death, but nothing
   on disk says whether its `impl` node's subprocess outlived the scheduler.
   The §4 hazard is therefore reasoned, not observed.

## Implementation notes — DESIGN.md sections to update

DESIGN.md is owned by another lane right now and is deliberately untouched by
this ADR. When this is implemented, these sections need to move with it:

- **"Gate nodes and `resume`"**, the `resume.lock` bullet (DESIGN.md:789) —
  `O_EXCL` → flock, the file no longer being removed on release, and the
  removal of "A stale lock is reported with the exact path to delete".
- **`internal/runstate/lock.go`'s own doc comments** — every one of them states
  the decision this ADR out-scopes ("O_EXCL makes the create atomic",
  "AcquireLock does not check whether that pid is still alive; a stale lock is
  a human decision", "The returned release func removes the lock"). All three
  become false in the same commit, and `LockHeldError`'s doc comment carries
  the `rm` advice §4 retires.
- **"Web live view — `oh-my-graph serve`"** (DESIGN.md:825–880) — the card
  state vocabulary gains abandoned, and the held-`resume.lock` 409 rule
  (DESIGN.md:844) now means a live holder rather than possibly a corpse.
- **"Object design"** (DESIGN.md:1278) — the in-repo views read a run through
  `runfeed` *plus* the lock probe; name the seam that owns the probe.
- **"Repo layout"** (DESIGN.md:1321–1344) — only if the probe lands somewhere
  new; the intent is `internal/runstate`, which needs no layout change.
- **"Goal cycles"** (DESIGN.md:1162) mentions each cycle's own `resume.lock`;
  no semantic change, but it should not read as an internal detail once the
  file is contract surface.
- **docs/RUN-FEED.md** — the new "Liveness" section of §3, and the amendment
  of the closing "lock files … are internal" sentence.

## Alternatives considered

- **pid + `ps` liveness probe.** Rejected, refuted by measurement: t95b's pid
  was recycled and read alive for hours, non-deterministically. It also needs
  a process spawn, which the four-exec-seams invariant forbids outright.
- **pid + process start time.** Sound in principle — a recycled pid has a
  different start time — and it was the runner-up. Rejected because it buys a
  weaker guarantee at a higher price: on darwin it means `sysctl`
  `KERN_PROC_PID` and decoding `kinfo_proc` through `unsafe`, on linux
  `/proc/<pid>/stat` field 22 in jiffies plus a boot-time reference, i.e. two
  hand-rolled per-OS decoders and a new `golang.org/x/sys` dependency, whose
  clock/jiffy arithmetic errors would land in the false-*dead* direction. The
  kernel already tracks "is this process alive" for us, exactly, for free.
- **pid + process identity ("is it an oh-my-graph?").** Rejected: strictly
  weaker than start-time comparison, same portability cost, and it cannot
  even answer the question here — this machine runs many concurrent
  oh-my-graph processes, and a `run` leg does not carry its run id in argv
  (the id is minted after launch), so an identity check would happily accept
  the wrong oh-my-graph.
- **`fcntl(F_SETLK)` record locks, probed with `F_GETLK`.** Attractive because
  `F_GETLK` reports the holder without acquiring — no probe race. Rejected on
  two measured/documented hazards: record locks are per-process, so the
  embedded live view would be granted its own run's lock and declare it
  abandoned, and they are dropped when any fd on the file is closed by the
  process. The rejection is not as clean as it looks, and §1 says why: on a
  linux NFS mount `flock()` *is* implemented as these record locks, so this
  alternative is not merely rejected but must be actively detected and refused
  at runtime.
- **An exclusive (`LOCK_EX`) acquire-and-release as the probe**, matching what
  `serve`'s `pausedSnapshot` already does. It was this ADR's first choice, on
  the grounds that its race was a false *alive* and therefore cosmetic.
  Rejected in favour of `LOCK_SH` (§1): the flicker is real, it compounds with
  the number of pollers, and §3 publishes this probe to third-party consumers —
  putting a primitive in a public contract that makes every observer briefly
  block the thing it observes is the wrong contract. The retry loop that had
  been proposed to paper over the resulting false-*busy* went with it; a
  mechanism that needs a retry loop to be usable is the wrong mechanism, and
  the loop had a failure of its own (it widens the window in which a released
  and unlinked inode can be re-acquired — §1, "Release must not unlink").
- **Unlink on release, plus a `dev`/`ino` check after every acquire** —
  `fstat` the fd, `stat` the path, and refuse if they disagree, which detects
  the unlinked-inode double-spend rather than preventing it. Correct, and
  rejected as strictly more machinery for the same guarantee: not unlinking
  removes the failure mode outright, and the leftover file is the thing the
  derivation wants anyway (see "Absence is not evidence").
- **A heartbeat file in the run directory.** Rejected on the dangerous
  direction: a laptop suspended for eight hours, a `SIGSTOP`, or a loaded
  machine produces a stale stamp for a perfectly live run, so it manufactures
  false-deads out of ordinary conditions. It also needs a tunable threshold, a
  background writer goroutine, and periodic writes into a directory whose
  write discipline is documented as atomic-snapshot plus append-only.
- **Elapsed silence as the criterion** (no event for N minutes ⇒ abandoned).
  Rejected outright: shipped graphs allow node timeouts up to 1h, so long
  legitimate silence is normal and no threshold separates the cases.
- **A reader repairs the feed** by appending `run_finished` (or a new
  `run_abandoned`) on the dead run's behalf. Rejected — §2, five reasons, of
  which the decisive one is that it makes the dangerous error permanent and
  the harmless one impossible to undo.
- **Render an abandoned run as `FAIL`.** Rejected: it is a verdict about work
  that never got one, it would make `--retry-failed`'s vocabulary lie, and it
  is the same conflation ADR 0009 refused for session-limited nodes.
- **`runs prune` / `--force-finish`.** Rejected — §5. The smallest thing that
  works is an existing command and a printed hint.
