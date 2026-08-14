# ADR 0008 — Cross-run session reuse (named persistent node sessions) is deferred

- Status: Proposed (recommendation: **defer**)
- Date: 2026-08-02

## Context

`handoff: session` already covers continuity *within* a run: a dependent
node runs `--resume <session_id>` of its single session-parent, and DESIGN.md
plus the load-time guards (exactly one session-parent, no gate/root parent,
cwd-mismatch warnings) fence in where that works. What the engine has no
story for is continuity *across* runs: today every run starts every node
cold, and a user who wants tonight's review node to remember this morning's
review node has to fish the session id out of the ledger and resume it by
hand.

There is real demand for that story. External reader feedback on the
project's positioning consistently lands on the same point: session
continuity is the tool's most misunderstood value — readers who "get" that a
node is a real, resumable claude session immediately see what the tool is
for, and readers who don't, don't. A feature that makes continuity durable
and nameable is the obvious next step on that axis.

The proposed shape: a `session: <name>` node field naming a **persistent
identity**. A registry at `~/.oh-my-graph/sessions.json` maps name →
`{session_id, project_path, created_at, model}`. On run start, a named node
resumes its registered session instead of starting cold; on the node's
success, the registry entry is updated to the new session id.

The evidence base is recorded research against the real CLI (not
re-verified here; these are the facts this ADR reasons from):

- `claude -p --resume <session_id>` can reopen a **finished** run's session
  for roughly **30 days**; after that the transcript is garbage-collected
  and the id resolves to nothing.
- The id is stable and multi-resumable — resuming does not consume or
  rotate it.
- Resume **re-hydrates the full transcript**. It is an *ergonomic
  continuity* feature, not a token saver: every resumed turn re-carries the
  whole history, so a long-lived session costs more per turn as it ages,
  never less.
- Session lookup is **project-directory scoped**: the session is found
  relative to the cwd it was created in, not by id alone from anywhere.
- Config flags and permission mode are **not restored** by resume; the
  caller must re-supply them on every invocation.

## Decision

**Recommend defer.** The user value is real, but `session: <name>` promises
a *persistent identity* on top of a substrate that is temporary, pinned to a
directory, and monotonically more expensive — three properties the name
actively hides. Each concern, argued:

- **The 30-day horizon breaks the promise silently.** A registry entry is a
  claim that a name resolves to live context, but the CLI gives us no way to
  check liveness short of spending a resume attempt. A user who comes back
  to a named node after a month gets either a hard resume failure or a cold
  start wearing a warm session's name — and `sessions.json` has no honest
  answer to "is this entry still good?" beyond comparing `created_at` to a
  ceiling we don't control and the CLI doesn't document as contract. A
  feature whose steady state is *quiet expiry* is worse than no feature: it
  converts "I know this node starts cold" into "I believe this node
  remembers, and I'm sometimes wrong."
- **Directory scoping collides with worktrees.** Session lookup is scoped to
  the project directory the session was created in, and the engine's
  flagship isolation feature (ADR 0005) runs nodes in per-run checkouts at
  `<run-dir>/worktrees/<name>` — a path that *contains the run id* and never
  recurs. A named session created inside one run's worktree is unfindable
  from every subsequent run's worktree by construction. So the feature would
  work only on nodes that *don't* use `worktree:` — unusable exactly where
  the engine puts its edit lanes — or would require pinning `project_path`
  to the user's real checkout, reintroducing the shared-tree coupling ADR
  0005 exists to remove. The within-run `handoff: session` guards already
  warn on cwd divergence for one hop; a cross-run registry would have to
  police it across arbitrary time and repo drift.
- **Config re-supply is the one concern that is NOT a blocker.** Resume
  restores no flags or permission mode, but oh-my-graph never relied on
  restoration: `CLIRunner` builds the full argv — model, tools,
  permission mode — from the graph YAML on every invocation. The engine is
  *better* placed than a human at the raw CLI here. This weighs for the
  feature, and is why the recommendation is defer, not reject.
- **The cost curve inverts the name's suggestion.** "Persistent session"
  reads like an optimization; full-transcript re-hydration means the
  opposite — a named identity gets more expensive every run it survives,
  until day ~30 deletes it. Within a run that curve is short and bounded;
  across runs it is unbounded accretion ending in expiry, and per-node
  `budget_usd` would fail nodes for carrying history the user forgot they
  were carrying.
- **Doing nothing is not doing badly.** The within-run story exists and is
  guarded; the ledger records every node's `session_id`, so deliberate
  cross-run continuation is already possible by hand (`claude --resume`, or
  `resume` of the run itself) — with the human, who *can* judge whether the
  old context is still relevant, making the call. What the reader feedback
  most directly indicts is positioning, and documentation is the cheap,
  reversible fix for that.

Deferred, not rejected — revisit when any of these move: the CLI offers a
liveness/expiry check (or durable named sessions natively), the retention
horizon becomes contractual or configurable, or session lookup can address
an id independent of its creation directory. Any acceptance must also
answer concurrent-run access to `sessions.json` (a new piece of global
mutable state; today the only cross-run state is the run dirs themselves).

## Consequences

**Positive**

- No feature ships whose steady state is silent expiry; the engine keeps
  its property that every promise a graph makes is one the engine can keep.
- No new global mutable registry, no new load-time guard matrix
  (`session:` × `worktree:` × `cwd` × registry staleness) to police.
- The real, cheap win is unblocked: document the continuity story loudly
  (README/EXAMPLES already elevate handoff; extend that to "resume a node's
  session by hand from the ledger").

**Negative / trade-offs**

- The most-requested axis of the tool's value stays manual across runs; a
  user must copy a session id instead of writing a name. That friction is
  real and this ADR chooses it deliberately.
- Deferral costs a revisit: if the CLI later firms up retention and lookup,
  this ADR must be superseded rather than silently ignored.

## Alternatives considered

- **Accept as proposed (`session:` + `sessions.json`).** Rejected for now:
  every registry read is a bet on an undocumented 30-day horizon and on a
  directory-scoped lookup that per-run worktrees defeat by construction.
- **Accept with staleness heuristics (TTL on `created_at`, resume-probe
  fallback to cold start).** Rejected: the heuristic guesses at a GC policy
  the CLI doesn't publish, and "fell back cold, silently" is precisely the
  misleading state the feature must not have.
- **Accept but forbid `session:` on `worktree:` nodes.** Honest about the
  scoping collision, but it carves the feature out of the engine's edit
  lanes — the nodes with the most context worth keeping — leaving too
  little value to justify the registry.
- **Reject outright.** Too strong: the user-value evidence is the best
  signal the project has about what readers want, and the config-re-supply
  story shows the engine is structurally well placed if the substrate
  firms up. Defer with named re-open conditions.
