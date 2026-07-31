# ADR 0005 — Worktree provisioning runs behind a third exec seam

- Status: Accepted
- Date: 2026-07-31

## Context

Every claude-run node executes in the working tree oh-my-graph was invoked
from (or its declared `cwd`). That single shared tree is a real defect for
graphs that *edit*: parallel lanes are forced to serialize (two nodes editing
one checkout race each other), a node's `git add`/commit can sweep in the
user's own untracked files, and it caused the auto-branch bug — a node
switching branches under the user's feet. Backlog #4 names per-node git
worktree isolation as the root fix: give each edit lane its own checkout,
created and torn down by the engine.

Creating a worktree means running `git worktree add` — a subprocess. That
collides with the invariant CONTRIBUTING.md and ADR 0002 police:

> Exactly two objects in oh-my-graph may spawn a process —
> `runner.ClaudeCLIRunner` and `verify.ShellVerifier` — each behind its own
> injected interface. No other package imports `os/exec`. A **third** spawner
> needs an ADR first.

This is that ADR. The options mirror ADR 0002's:

1. Teach `ClaudeCLIRunner` (or `ShellVerifier`) to also run git.
2. Let the `Scheduler` or the CLI exec git directly.
3. Give worktree provisioning its own, narrower seam in a new package.

## Decision

**Option 3.** Worktree provisioning gets its own interface and its own single
exec-owning implementation, in a new `internal/worktree` package:

```go
type Provider interface {
	Acquire(ctx context.Context, name string) (string, error)
}
```

- `GitManager` runs `git worktree add <run-dir>/worktrees/<name> -b
  omg/<run-id>/<name> HEAD` once per unique name (a mutex-guarded map makes
  Acquire idempotent), off the invocation repo's HEAD. It is the only object
  in `internal/worktree` that spawns a process. The managed path lives under
  the run directory — never inside the user's checked-out tree.
- `RefusingProvider` is the `schedule.Options.Worktrees` default, mirroring
  `RefusingVerifier`: a scheduler test that forgets to inject one fails
  loudly instead of silently spawning git.
- `FakeManager` (scripted, keyed by name, deterministic paths) is what tests
  inject, so the scheduler's whole worktree path stays spawn-free in CI.
- `cmd/oh-my-graph` constructs `GitManager` next to `ClaudeCLIRunner` and
  `ShellVerifier` and calls its `Cleanup` after the run. Cleanup is
  deliberately NOT on the `Provider` interface: the Scheduler only ever asks
  where a node runs; run-end teardown is the CLI's job.

The invariant is **restated, not weakened**:

> Exactly three objects in oh-my-graph may spawn a process —
> `runner.ClaudeCLIRunner`, `verify.ShellVerifier` and `worktree.GitManager`
> — each behind its own injected interface. No other package imports
> `os/exec`.

The child-environment scrub applies to git too. A repository's own hooks
(`post-checkout` fires on `git worktree add`) are arbitrary user code that
may legitimately invoke claude, so `internal/childenv.Scrub` is applied to
every git child and asserted by a unit test on the built `cmd.Env`, exactly
like the other two spawners.

**Cleanup never loses work.** At run end (on a fresh context, so a halted or
interrupted run still cleans up): a worktree git refuses to remove — it holds
uncommitted changes — is left in place entirely; a branch whose tip moved
past its base carries commits, so the worktree dir is removed (commits live
in the object store, not the dir) and the branch is retained; only a branch
provably still at its base is deleted. Every non-silent outcome is reported
as a one-line note naming the branch or path.

**Auto-planned nodes may not set `worktree`.** Provisioning is not a tool
call, so no permission mode, allowlist or ceiling layer ever sees it — an
unreviewed plan must not be able to make the engine create checkouts and
branches in the user's repository. `validatePlannedNodes` rejects the field,
with its disposition recorded in the coordinator's field table, alongside
`cwd`, `agent` and `success_check.verify`.

## Consequences

**Positive**

- Parallel edit lanes are real: nodes with different `worktree:` names edit
  concurrently with no shared-tree race, and a lane's dev → e2e → review →
  pr shares one isolated checkout (same name, same worktree).
- The user's own working tree is never the node's playground: untracked
  files can't be swept into a node's commit, and no node switches branches
  under the user's feet.
- Both purposes of the original invariant survive: the subscription-auth
  scrub still has exactly one home per spawner, and the whole engine is
  still testable with zero real spawns (`worktree.FakeManager`).
- Handoff semantics are untouched: artifacts still persist to
  `~/.oh-my-graph/runs/<run-id>/` — the worktree isolates a node's WORKING
  TREE, not its result.

**Negative / trade-offs**

- "Two exec objects" is now "three". The rule's bluntness erodes a little
  more each time; the CONTRIBUTING.md wording moves to "a **fourth** needs an
  ADR" and reviewers must enforce the new form.
- A retained branch (`omg/<run-id>/<name>`) outlives the run by design. A
  `resume`d leg re-declaring the same worktree name then fails loudly on the
  ref collision — safe (nothing is reset) but manual: the user merges or
  deletes the branch first. Accepted over auto-suffixing branch names, which
  would silently multiply refs.
- `git` becomes a runtime dependency of graphs that use the field (only of
  those — a graph with no `worktree:` never spawns git). A node declaring a
  worktree outside a git repository fails loudly at that node.

## Alternatives considered

- **Extend `ShellVerifier` (it already runs arbitrary shell).** Rejected: it
  is the evidence object; provisioning is not evidence, and the composite
  would give `FakeVerifier` two unrelated subsystems to fake — the exact
  degradation ADR 0002 refused for `NodeRunner`.
- **Exec from the `Scheduler` or the CLI.** Rejected outright: the
  Scheduler's defining property is that it never spawns and never learns
  what ran; the CLI execing directly would put a spawn outside every
  injected seam and outside the env scrub's tested call sites.
- **A pure-Go git implementation (go-git).** Rejected: worktree support is
  partial, the dependency is heavy, and behaviour would diverge from the git
  the user's own hooks and config define. The real git is already the
  contract the user's repo speaks.
- **Auto-creating a worktree for every node.** Rejected: read-only fan-out
  (the common case) gains nothing and would lose access to the user's real
  tree state. Isolation is opt-in per node, named so lanes can share.
