# ADR 0015 — An abandoned run is derived from the lock, never repaired into the feed

- Status: Accepted
- Date: 2026-08-04

## Context

Two runs on the maintainer's machine have read `RUNNING` for over a day:

```
t95b       20260803-125311.878285000-1   25:40 elapsed  $0.0000   1 pending
fragments  20260803-121317.978943000-1   26:19 elapsed  $28.9871  1 pending
```

Both processes died on 2026-08-03. Their event streams end mid-leg — the last
line of each is a `node_started` that never got a terminal event:

```
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
  flock — not the file's existence — *is* the lock; the file's existence
  carries no meaning at all.
- The pid stays in the file, still informational. Its role inverts and
  improves: useless as a liveness *test*, it is trustworthy as a *label* once
  liveness has been established independently, so `LockHeldError` can name the
  process a user has to wait for.
- The read-time probe opens `O_RDONLY` (**no `O_CREATE`**), tries
  `LOCK_EX|LOCK_NB`, and on success immediately `LOCK_UN`s and closes.
  `ENOENT` means no lock, i.e. free. Readers therefore never create, write or
  remove anything — `runs list`'s documented read-only posture over run
  directories survives intact.
- The release path unlocks, closes and removes the file, as today.

Measured on darwin 22.6.0 / go1.25.0 with a throwaway probe (not committed):

| measurement | result |
|---|---|
| `kill -9` the holder, then probe | **FREE** — the kernel released it, no cleanup path ran |
| holder `exec`s a child that outlives it (child pid confirmed running), holder exits, then probe | **FREE** — Go opens with `O_CLOEXEC`, so the lock fd does not reach the child |
| second `flock` from a **different fd in the same process** | **HELD** — flock conflicts per open file description, not per process |
| `flock` on an `O_RDONLY` fd, against a held lock / a free one | **HELD / FREE** — a read-only fd is a valid probe |

Judged on the axes that matter:

- **False "alive" — impossible by construction.** There is no name to recycle.
  The t95b failure cannot occur.
- **False "dead" — the dangerous direction — is enumerable**, and each case is
  handled below in §4 and §"What could not be determined": a leg started by a
  pre-ADR binary (no flock taken), a filesystem that does not implement flock,
  and a lock file a human deleted by following today's advice.
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

**The probe's own race, and why both directions are safe.** A reader that
finds the lock free holds it for the two syscalls between acquire and
release. In that window, a leg calling `AcquireLock` gets `EWOULDBLOCK` and a
concurrent reader sees "held". The first is a false *busy* — the leg refuses
to start, which is the safe direction and is exactly what the lock exists to
do — and it is removed anyway by having `AcquireLock` retry the flock a few
times across a short bounded window before declaring it held. The second is a
transient false *alive*, the cosmetic direction, self-correcting on the next
poll. This acquire-and-release-as-a-probe pattern is not new to the codebase:
`serve`'s `pausedSnapshot` (`internal/serve/gate.go:244`) already does it and
already documents the window as "not a correctness hole".

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
| open | free | **abandoned** |

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
listing it beside `state.json` and `events.jsonl`, defining exactly one fact
about it (an exclusive `flock` is held for as long as a leg is running; a free
or absent lock beside an open leg means the writer is gone), and telling a
consumer that cannot or will not flock to fall back to the open-leg rule —
today's answer, and a safe one. The file's *contents* remain uncontracted; the
pid stays explicitly informational.

### 4. The surfaces

- **`runs list`** gains the verdict word `ABANDONED` beside `RUNNING`.
  Deliberately not `FAIL`: a `FAIL` is a verdict about the *work*, and the
  work has no verdict — the same argument ADR 0009 used to refuse marking a
  session-limited node FAILED. `ABANDONED` is a statement about the process.
  The row is followed by the one-line recovery hint from §5.
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
  lock). A free lock means there is no leg, so `resume` acquires it and
  proceeds. There is no third case, so there is no question to put to a human.

  **The "delete it and retry" advice must be deleted with it.** Under flock
  semantics that advice becomes an active double-spend footgun: unlinking the
  file does *not* release the live holder's lock (it holds it on the now
  unlinked inode), while a second leg would happily create a fresh file and
  take an uncontended flock on the *new* inode — two schedulers, one run, both
  spending. `LockHeldError`'s message becomes "a leg of this run is in flight
  (pid N); wait for it, or stop that process", and never advises `rm`.
- **`serve`'s gate button** is fixed for free: `pausedSnapshot`'s permanent
  409 on a dead leg's stale lock ("a leg of run X is in flight, so it is not
  paused for a decision") disappears, because the kernel already released that
  lock.

**The residual hazard, named.** The measurements show the lock fd is
`O_CLOEXEC`, so a `claude` child does not keep it alive. That is what makes
the probe truthful about *oh-my-graph*, and it means a `kill -9` of the parent
alone leaves an orphaned `claude` still running and still spending while the
run reads `ABANDONED`. A `resume --retry-failed` would then relaunch that node
alongside its own orphan. Scope of the risk: readers never launch anything, so
only the deliberate `resume` can spend — no surface auto-resumes, and none is
proposed. The hazard also exists today (the human deletes the stale lock just
as blindly); this ADR makes it easier to reach by removing the human, so the
abandoned-run hint says so in one clause. Note that a `Ctrl-C`/SIGTERM stop —
the ordinary way runs die — signals the whole process group and takes the
children with it; the orphan case needs a `kill -9` aimed at the parent.

### 5. Recovery: nothing new

No `runs prune`, no `--force-finish`, no new command or flag.

- **The state needs no cleaning** — it was never wrong on disk, only misread.
  Fixing the derivation fixes both zombie runs and the screenshot with them.
- **Resuming already exists.** `resume <run-id> --retry-failed` is precisely
  the right command: ADR 0009 already widened it from "retry failures" to
  "also launch un-recorded, launchable work", and an abandoned run's un-settled
  nodes are exactly that shape (the `dev` and `impl` nodes above have no
  terminal record at all). The surfaces print it; that is the whole recovery
  feature.
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
  documented file with one promised property. That is a real narrowing of
  future freedom — the lock's mechanism can no longer change silently.
- **A leg started by a pre-ADR binary holds no flock**, so a new binary reads
  a genuinely live pre-upgrade run as abandoned — a false-dead confined to
  runs in flight across the upgrade, resolvable only by not resuming a run you
  know is running. Not mitigated in code; a `run` started after the upgrade is
  never affected.
- **A human who deletes the lock file of a live run** now gets a run that
  reads abandoned (today they get one that refuses to resume). The removal of
  the advice that told them to do it is the mitigation.
- **The orphaned-`claude` case in §4** is a real double-spend path behind a
  deliberate `resume`.
- **`watch` still hangs** if the run dies while it is already tailing.
- **Windows** needs a build-tagged stub. The project does not ship it, so the
  stub reports unknown and preserves today's behaviour.

## What could not be determined

1. **Nothing was measured on linux.** Every measurement in this ADR is darwin
   22.6.0 / APFS. `syscall.Flock` is defined for linux and the semantics are
   the standard ones, but the two properties this design leans on — the lock
   fd not surviving `exec` (`O_CLOEXEC`), and a second fd in the same process
   conflicting — must be pinned by tests that run in CI on linux before this
   is trusted there. That is an implementation gate, not a hope.
2. **flock over a network filesystem is untested** — no NFS or SMB home was
   available. On some configurations flock is local-only, which would let two
   machines sharing a home directory each see the other's lock as free: a
   false-dead, the dangerous direction. Unquantified. If it ever matters the
   fix is a filesystem check feeding the *unknown* path from §1, not a
   redesign.
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
  `O_EXCL` → flock, and the removal of "A stale lock is reported with the
  exact path to delete".
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
  process.
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
