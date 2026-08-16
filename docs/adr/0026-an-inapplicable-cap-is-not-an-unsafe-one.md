# ADR 0026 — An inapplicable cap is not an unsafe one

- Status: **Accepted — implemented 2026-08-16.**
- Date: 2026-08-16
- **Amends `0025-one-run-one-cli-runtime.md`.** ADR 0025 decided that "a Codex
  graph containing positive `budget_usd` or non-empty `agent:` is rejected
  during runtime preflight". This record splits that one sentence in two and
  changes the `budget_usd` half only. Everything else ADR 0025 decided — one
  runtime per run, one exec seam with two protocols, the sandbox mapping,
  honest accounting, the env scrub, the refusal of
  `auto --max-goal-budget-usd` — stands exactly as written.
- **Same shape as `0024-a-timeout-is-its-own-cause-not-a-run-error.md` and
  `0009-a-session-limit-is-a-pause-not-a-failure.md`:** each separated two
  outcomes a single verdict had been lumping together. Here the two are
  *the runtime cannot do this* and *the runtime has nothing to do this to*.

## 1. Context

`runner.ValidateGraphForRuntime` refused two declarations under
`--runtime codex` with the same sentence:

```
node "round1":  agent is supported only by the claude runtime
node "localrun": budget_usd is supported only by the claude runtime
```

One sentence, two different facts.

**`agent:` names a Claude Code subagent.** Running the node without it means
running it without that agent's system prompt. The node that executes is not
the node the graph declares — a semantic substitution the engine has no
authority to make silently. Refusing is correct and does not change here.

**`budget_usd` is a USD ceiling**, and Codex reports no USD at all
(`codexProtocol` starts every outcome at `CostUnknown`, and
`schedule.evaluateBudget` already returns nil for an unknown cost, so the cap
could never have been evaluated even if a node ran). There is no quantity to
bound. The declaration is **inapplicable, not unsafe** — and refusing to *load*
over it is what made five of this repository's eight shipped graphs unusable
under Codex.

### Measurement — `--runtime codex lint graphs/*.yaml`, 2026-08-16

| graph | before | after |
| --- | --- | --- |
| `adr-driven-dev` | refused: `agent:` ×3, `budget_usd` ×1 | refused: `agent:` ×3 (+1 budget warning) |
| `backlog-batch` | refused: `budget_usd` ×2 | valid (2 warnings) |
| `dev-review-pr` | refused: `budget_usd` ×1 | valid (1 warning) |
| `review-loop` | refused: `budget_usd` ×2 | valid (2 warnings) |
| `self-dev` | refused: `budget_usd` ×1 | valid (1 warning) |
| `apply-flags` | valid | valid |
| `haiku-smoke` | valid | valid |
| `merge-shepherd` | valid | valid |

Five refused → one. The one that remains is refused for the reason that
survives scrutiny. Three of the four freed graphs never declared a budget
themselves: they inherit it from `graphs/fragments/e2e-verify.yaml`, so a
single fragment's cap was disqualifying three separate pipelines.

### The runaway guard is already runtime-neutral, and already declared

The fragment that supplies most of those caps says what the cap is for, beside
the cap itself:

```yaml
  # Runaway insurance ONLY, never cost tuning: a tight budget kills a
  # nearly-done node at the threshold, and the salvage costs more than
  # letting it finish (a real $0.02-over kill cost ~6x the remaining work).
  # Keep this an order of magnitude above a plausible run. The hang guard
  # is `timeout:`, not the budget.
  budget_usd: 10.00
```

`timeout:` is runtime-neutral and always present: a node either declares one or
gets the `CLIRunner`'s 20m default, and `--runtime codex` changes neither. So a
Codex node whose USD cap cannot apply is still bounded in wall-clock — nothing
becomes unguarded, and the run is not being asked to trust a cap that isn't
there.

## 2. Decision

Preflight returns two verdicts instead of one, and the third case does not move:

| declaration | under codex | why |
| --- | --- | --- |
| `agent:` | **REFUSE** | dropping it changes what the node *is* |
| node `budget_usd` | **ACCEPT + say the cap cannot apply** | nothing to bound; `timeout:` still guards |
| `auto --max-goal-budget-usd` | **REFUSE** (unchanged) | the only bound on an iterating loop |

`ValidateGraphForRuntime` therefore returns `([]string, error)`. The warning
names the node, the declared cap, why it cannot apply, **and which guard remains
in force — the node's own `timeout:` when it declares one, the runner's default
otherwise, quoted from the constant the `CLIRunner` actually applies** so the
message cannot drift from the bound. An acceptance that did not say this would
read as "this node is now unbounded", which is the one thing it must not mean.

Both verdicts are independent: a graph refused for `agent:` still reports its
inapplicable caps, because the reader who fixes the agent node next needs to
know what the budget will and will not do.

### Why the goal budget is not the same case

This is the part a later reader will otherwise "fix" into consistency, so it is
written down: **the asymmetry is deliberate.**

A per-node `budget_usd` is one node's runaway insurance, and that node has
another bound — its timeout — that survives the runtime change. A goal ceiling
is the **only** thing bounding an ITERATING `auto` loop: without it the loop's
sole stop condition is `--max-cycles`, and the ceiling is what a user reaches
for when cycles alone are not a spend they can predict. Under Codex it can never
be evaluated, so a loop that accepted it would stop at its first cycle boundary
with `StopBudgetUnmeasurable` (`internal/coordinator/goal.go`) — having bought
a whole cycle to learn what preflight can say for free, before anything spends.
Refusing up front is the honest answer.

The rule underneath both, stated once: **refuse a declaration when accepting it
would change what runs or spend money to discover it is unenforceable; warn
when the declaration simply has nothing to act on.**

### Every call site surfaces the warnings

There are five `ValidateGraphForRuntime` call sites — `run`, `executeGraph`,
`lint`, `run --dry-run`, and `resume`. All five print, through one
`warnRuntimePreflight` helper, on the advisory `warning:` channel that never
touches an exit code. `run` alone passes `io.Discard` into `executeGraph`, so
its single invocation prints the list once (in the pre-run Codex disclosure)
rather than twice; a nil writer there means stderr, never silence. A warning
nobody prints is the silent drop this whole change exists to avoid.

### A test that keeps it true

`internal/runner/shipped_graphs_runtime_test.go` lints every `go:embed`ed graph
under both runtimes and asserts the expected verdict **per named graph**, with
the reason recorded beside it — not a count, so a failure says *which* graph
changed. It also fails when a graph ships with no row at all. Preflight spawns
no process and needs no login, so this is free and deterministic, and it belongs
in `make test` rather than a CI shell step.

### The Claude path does not move

`ValidateGraphForRuntime` returns `(nil, nil)` for `RuntimeClaude` on its first
line, as before. No Claude behaviour, argv, message or default changes; the
warning channel is empty for every Claude graph, including one declaring both
`agent:` and `budget_usd`.

## 3. Consequences

- Seven of eight shipped graphs load under Codex; `adr-driven-dev` remains
  refused for `agent:`. Loading is not running: five of the seven still halt at
  their first publishing node, because the Codex sandbox is a network boundary
  (ADR 0025, `docs/LIMITATIONS.md`).
- A Codex user who declares `budget_usd` gets a warning per budgeted node
  instead of a refused graph, and is told the timeout is what still bounds it.
- The graph schema stays one schema. As in ADR 0025, runtime preflight owns
  compatibility — this record only changes what preflight *concludes*.
- `budget_usd` under Codex is now a declaration the engine loads and does not
  enforce. That is a real cost of the decision: it is disclosed at load, in the
  pre-run disclosure, in `docs/LIMITATIONS.md` and in `docs/EXAMPLES.md`, and it
  is why the warning must always name the surviving guard.

## 4. Falsification

This decision is wrong, and refusing `budget_usd` was right after all, if any of
these becomes true:

1. **Codex reports USD.** If a future `codex exec` publishes a cost figure, the
   cap becomes applicable and should simply be enforced — neither refused nor
   warned. (`CostUnknown` and its schema-3 consumer contract are the seam that
   would change.)
2. **The timeout stops being universal.** If a node can ever run with no
   wall-clock bound — a removed default, an opt-out flag — then accepting an
   unenforceable cap does leave a node unguarded, and the acceptance must be
   withdrawn with it.
3. **A `budget_usd` is measured to be load-bearing as cost tuning rather than
   runaway insurance** — i.e. a shipped graph is found whose declared cap is
   near its typical spend, so running it uncapped is a materially different
   proposition. Today's evidence is the opposite: `e2e-verify` says "an order of
   magnitude above a plausible run", in the file.
4. **A user is measured to read "warning: cannot apply" as "enforced".** The
   whole acceptance rests on the disclosure being read. If it is not, refusing
   is the safer default.
