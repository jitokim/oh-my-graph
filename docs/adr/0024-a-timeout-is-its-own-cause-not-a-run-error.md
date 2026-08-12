# ADR 0024 — A timeout is its own cause, not a run error

- Status: **Accepted — implemented 2026-08-13.**
- Date: 2026-08-13
- **Amends `0007-per-node-execution-limits.md`.** ADR 0007 shipped the
  declarable `timeout:` and said, of the seam contract it left unchanged, that
  "the Scheduler still classifies it as a `run_error`". That sentence is what
  this record changes; nothing else in ADR 0007 moves. The bound, the load-time
  validation, the process-tree kill and the auto-mode disposition are all
  exactly as decided there.
- **Same shape as `0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md`,
  `0009-a-session-limit-is-a-pause-not-a-failure.md`** and #156's `LATCHED`.
  Each of those separated an outcome that is *not a verdict about the work*
  from one that is. ADR 0015: "a FAIL is a verdict about the work, and the work
  never got one." ADR 0009: a session limit is a pause, not a failure. #156: a
  latch is not a timeout. This is the fourth, and the first to say it about the
  timeout itself.
- Found by: running this repository's own `graphs/adr-driven-dev.yaml` template
  against it.

## 1. Context

`retry.on` is a filter over a closed set of failure-cause tokens. Six of them
shipped, and one of the six was a bucket:

```go
// causeFromRunError maps a runner error to a retry cause token. An unparseable
// output is "output_error"; anything else (spawn failure, context) is
// "run_error".
```

"Anything else" is two failures that have nothing in common but the shape of
the return value:

| the failure | what it says | what a retry costs |
|---|---|---|
| the binary never started (`exec: "claude": executable file not found`) | the environment is wrong, or was, for a moment | ~nothing: it fails again immediately, or it works |
| the node's `timeout:` expired | we did not wait long enough | **another whole timeout** — a timeout is the one failure that always burns its full budget before dying |

Because both were `run_error`, they shared one retry token. A graph could not
ask for the cheap re-attempt a failed spawn deserves without also signing up to
spend a second full timeout on every node that runs long, and it could not opt
into retrying a slow node without also retrying every spawn failure. The two
want opposite policies and had one switch.

This was not noticed in review. It was noticed by running
`graphs/adr-driven-dev.yaml` on this repository: its `localrun` node was killed
by its own 20-minute bound, the run halted with the implementation already
committed, and the only retry vocabulary available to express "that one is
worth waiting for again" was the token that also means "the binary is missing."

## 2. Decision

**A node killed by its own timeout gets its own cause: `timeout`.**

1. `internal/runner` returns a typed `*NodeTimeoutError` — carrying the bound
   that expired, unwrapping to `context.DeadlineExceeded` — from the one branch
   that already distinguished this case: a deadline the runner **minted
   itself**, with the parent context still live. It is the same shape
   `verify.ShellVerifier` has used on the other exec seam since ADR 0002
   (`verify.TimeoutError`), for the same reason: a command that never reached a
   verdict must not be reported as one that reached a bad one.

2. `internal/schedule`'s `causeFromRunError` classifies that type as
   `graph.CauseTimeout`. Everything else it used to call `run_error` it still
   calls `run_error`.

3. `graph.CauseTimeout = "timeout"` joins the `Cause*` block, and
   `retryCauses` — the load-time validator's list — grows to seven. The set
   lives in one place precisely so the validator and the scheduler cannot
   disagree on a spelling, and adding a token means adding it there. It is also
   added to `coordinator.plannerRetryCauses`, so a planned graph may use what a
   hand-written one may.

A graph that wants the old behaviour writes `on: [run_error, timeout]`. Nothing
else changes: this is additive to the closed set, and no existing `retry.on`
list means anything different than it did.

### 2.1 The boundary: whose clock was it

`NodeTimeoutError` is minted **only** for a deadline this runner set. A deadline
inherited from the caller's context — a whole-run bound, a caller's own — stays
a plain context error and stays `run_error`. That distinction already existed in
the runner (it is what lets a halt-cancellation read differently from a genuine
run failure); this ADR only gives its already-separated branch a type.

The reason to keep it is concrete: `retry: { on: [timeout] }` re-runs the node,
and re-running inside a context that has already expired burns every remaining
attempt against a deadline that has passed. A retry token is a promise that
another attempt could go differently, and only the node's own clock can make
that promise.

### 2.2 What a timeout still is not

`timeout` is a retry cause. It is **not** a new node verdict, not a new run
status (ADR 0023's six are untouched), and not a feedback trigger: ADR 0010's
arc fires on *judgment* causes — `verify_failed`, `result_mismatch`,
`nonzero_exit` — and a timeout is the opposite of a judgment. A node that ran
out of time has said nothing for a reviewer to feed back.

## 3. Alternatives considered

### 3.1 Raise the timeouts and retry automatically — REJECTED

The obvious reading of the incident is "20 minutes was too short, and the engine
should have tried again." Both halves are refused, and this is the argument, put
on the record because the fix that was NOT chosen is the one a future reader
will propose again.

**A timeout is the one failure that always burns its full budget before dying.**
Every other retryable cause fails fast: a bad spawn returns in milliseconds, a
non-zero exit returns when the work stopped, an unparseable envelope returns
when the process did. A timeout, by construction, returns only after the whole
bound has elapsed. So an automatic retry is not "one more cheap attempt" — it is
a guaranteed doubling of the node's worst case, silently, on a bound the author
chose deliberately. The `localrun` node that prompted this would have gone from
losing 20 minutes to losing 40, and the second 20 would have been spent learning
what the first already established.

**And the engine cannot tell which case it is in.** A timed-out node is either

- a slow machine, a cold cache, an unlucky scheduler — where a retry works, or
- an instruction that cannot finish at any timeout — a `-count=300` stress run
  that needs 72 minutes, a poll with no exit condition, a command waiting on
  input that will never come — where **no** number of retries works, and each
  one costs the full bound.

Nothing in the error distinguishes them. The author knows; the engine does not.
Raising the default has the same problem one level up: it makes every wedged
node more expensive to discover, and it still guesses — ADR 0007 already
recorded that the 20-minute value "is a guess, and it has already guessed
wrong," which is the reason `timeout:` became declarable rather than
re-guessed.

So the engine gives the *vocabulary* and the author gives the *policy*.
`retry: { max: 1, on: [timeout] }` is available to a graph whose author knows
their node is flaky-slow rather than unbounded; the default is unchanged, and a
graph that says nothing retries nothing. This is the same disposition
`budget_exceeded` got: its own token precisely so that retrying it has to be an
explicit, informed opt-in that no pre-existing graph can trip into.

### 3.2 Fold it into `budget_exceeded` — REJECTED

Superficially similar (both are "the node hit a declared ceiling"), and wrong in
the way that matters to a reader of the ledger: `budget_exceeded` is a statement
about money already spent, and a timed-out node's spend is *unknown* — the child
is killed before it prints the envelope carrying `total_cost_usd`, which is why
the row says "cost unknown (killed before reporting)" rather than `$0.0000`.
Reusing the money token for the clock would make that row unreadable.

### 3.3 Leave it, and let authors match on the error text — REJECTED

`retry.on` matches tokens, not strings; there is no hook for it. The error text
is a diagnostic, deliberately re-worded once already (ADR 0007's
"timed out after 20m (node timeout)" replaced the raw
"context deadline exceeded"), and pinning a policy to it would freeze a
diagnostic into an interface.

## 4. Consequences

- **`timeout` is new user-facing surface.** It is a value users may now write in
  `retry.on`, validated at load like the other six, listed in the load error
  that rejects an unknown one, advertised to the planner, and documented in
  DESIGN.md, README and `docs/LIMITATIONS.md`. The CHANGELOG says so in those
  terms.
- **Nothing existing changes meaning.** No graph in the repository lists
  `run_error` in a `retry.on`; a graph that does now retries strictly less than
  before, in exactly the case where a retry costs the most.
- **The ledger and the stream are untouched.** A timed-out node still fails, is
  still recorded with the timeout named in its detail, and `on_fail: halt` still
  halts. The only thing that reads the new token is `shouldRetry`.
- **The default is still 20 minutes** for a node that declares no `timeout:`,
  and a node that declares none still earns the `timeout` cause — a graph should
  not have to write the field to be able to name the failure.

## 5. What this does NOT decide

- **Whether any shipped graph should retry timeouts.** None does. Adding
  `on: [timeout]` to a shipped template is a separate judgment about that
  template's node, and it should be made by someone who has measured that
  node's spread rather than by this record.
- **Any change to the default bound.** Deliberately not revisited here; §3.1
  says why re-guessing it is not the fix.
- **A cause for a verification command that times out.** `verify.TimeoutError`
  already exists and already fails the node, but it arrives through
  `causeFromCheck` as `verify_failed`, and it stays there: the argument for
  splitting it is the same argument made above, but the evidence is not — no
  run has been lost to it, and this project does not add closed-set tokens on
  symmetry alone.
