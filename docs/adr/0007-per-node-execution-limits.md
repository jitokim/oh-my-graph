# ADR 0007 — Per-node execution limits: a declarable `timeout:`; turn budgets rejected for now

- Status: Accepted (`timeout:`) / Rejected for now (`budget_turns:`)
- Date: 2026-08-02

## Context

Every node runs under a wall-clock bound the graph author cannot see or
change: `runner.defaultTimeout`, hardcoded to 20 minutes, applied as a
`context.WithTimeout` around the whole `claude -p` subprocess. The bound
itself is load-bearing — one wedged child must never hang the whole graph
(ADR 0001's subprocess model depends on it) — but the *value* is a guess, and
it has already guessed wrong: a real dev node doing legitimate ~20-minute
work was killed mid-flight by its own engine. The only `timeout:` in the
schema today is verify-only — it bounds a `success_check.verify` command
(default 2m, ceiling 10m) — so a graph can bound a thirty-second evidence
check precisely while its actual work runs under a limit it cannot even
mention.

A second limit was proposed alongside it: budgeting a node in **turns**
rather than dollars. `budget_usd` is a hard cost ceiling and stays — but as a
*scoping* tool it is nearly unusable, because dollars per task are hard to
estimate up front: a real $0.30 budget killed its run in three seconds. An
agentic turn is a unit a human can estimate ("a review is a handful of turns;
a feature is dozens"), and the claude CLI was believed to expose exactly that
knob as `claude -p --max-turns N`, which would slot into the runner the same
way `--max-budget-usd` already does: a native mid-run bound, detected from
the result envelope, reported as a named failure.

**The evidence check came back negative.** Verified 2026-08-02 against the
installed CLI (help text only, no metered spawn): `claude --help` documents
`--max-budget-usd <amount>` but **no `--max-turns`**, and no other
turn-bounding option appears anywhere in its output. The flag this half of
the proposal depends on does not exist here.

## Decision

**`timeout:` joins the node schema. `budget_turns:` is rejected for now.**

1. A node may declare `timeout: <Go duration>` (`45m`, `1h30m`), bounding its
   whole subprocess run. Validation happens at load, exactly like the verify
   timeout: unparseable or non-positive durations are a
   `GraphValidationError` naming the node, and the string is parsed once —
   `Node.TimeoutDuration()` — never re-parsed mid-run. Unlike the verify
   timeout there is **no ceiling**: the verify ceiling exists because a
   verification rides on its node's critical path, whereas the node timeout
   *is* the critical path, and raising it is the whole point of declaring
   it. An undeclared timeout keeps today's 20-minute default, so every
   existing graph runs exactly as before.

   The value threads through `runner.NodeInvocation.Timeout` to the runner,
   which uses it for the per-run `context.WithTimeout` in place of its
   default. The seam contract is unchanged: a timed-out node still surfaces
   as a context error, still kills its whole process tree, and the Scheduler
   still classifies it as a `run_error`.

   Planned (auto) nodes may set it, with the same disposition as
   `budget_usd` (allowed): both are bounds on one already-ceilinged node's
   execution, not grants of capability — recorded in the coordinator's
   field-dispositions table like every other field.

2. `budget_turns:` — a positive integer rendered as `--max-turns N`, failing
   the node with a detail naming the turn limit on exhaustion, mirroring how
   `--max-budget-usd` exhaustion is detected and reported — is **rejected for
   now**, on the evidence above: the CLI flag it would render to does not
   exist in the installed CLI's documented surface, so the engine could ship
   only the schema, not the enforcement, and a declared-but-unenforced limit
   is the exact "looks like enforcement while enforcing nothing" failure
   DESIGN.md already rejected once (the fabricated $/minute conversion).

   The design intent is recorded so the revisit is cheap: turns would be a
   **supplement** to `budget_usd`, never a replacement — USD stays as the
   hard cost ceiling; turns are for predictable scoping in a unit humans can
   estimate. Revisit when `claude --help` documents a turn cap; the runner
   already has the pattern to copy (`BudgetUSD` → flag → envelope subtype →
   named failure).

## Consequences

**Positive**

- The class of failure that motivated this — a legitimate long-running node
  killed by an invisible engine constant — is fixed by one schema line, and
  the fix is per-node, so every other node keeps the tight default bound.
- A malformed duration is a load error naming the node, not a mid-run
  surprise after upstream nodes were already paid for — the same move every
  other validation in this repo makes.
- The turns investigation is recorded honestly instead of half-shipped:
  the schema slot, the enforcement pattern, and the reason it is blocked are
  all in one place.

**Negative / trade-offs**

- A graph can now declare a very long timeout, and a wedged node holds its
  concurrency slot for all of it. Accepted: the author asked for it in a
  reviewed file, and halt-on-fail plus the process-group kill still apply
  when it does expire.
- Scoping work remains dollar-denominated until the CLI grows a turn cap —
  the unusably-unpredictable `budget_usd`-as-scoping problem is documented,
  not solved.
- The verify `timeout:` and the node `timeout:` now share a key name at
  different nesting levels with different ceilings. Accepted: same unit,
  same validation language, and the alternative (a second name like
  `max_duration:`) would make the schema less guessable, not more.

## Alternatives considered

- **Raise `defaultTimeout` instead of adding a field.** Rejected: it trades
  one guess for another — the 20m default is *right* for most nodes, and a
  global raise weakens the wedge-protection for all of them to fix one.
- **Derive the timeout from `budget_usd` via a $/minute rate.** Already
  rejected in DESIGN.md's budget section: the conversion rate would be
  fabricated, so it would look like enforcement while enforcing nothing.
- **Count turns engine-side** (parse `--output-format stream-json`
  incrementally, abort at N turns). Rejected for the same reason sub-call
  cost observation stays deferred: it is an ADR-level change to the
  one-envelope `NodeRunner` contract, and a kill from *outside* the CLI
  lands mid-turn instead of at a turn boundary — strictly worse than the
  native flag whenever that exists.
- **Prompt-level turn budgets** ("finish within N turns" injected into the
  node prompt). Rejected: narration, not enforcement — the same standing as
  `result_matches`, which the docs already demote to a secondary signal.
- **`budget_turns:` as a replacement for `budget_usd`.** Rejected even for
  the future revisit: they bound different things (spend vs. scope), and
  the hard cost ceiling must survive any scoping feature.
