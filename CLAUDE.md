# oh-my-graph — instructions for Claude Code

This is the project-level Claude Code config for **oh-my-graph** itself
(public, committed — no personal or private settings live here).

## What this project is

oh-my-graph is a graph-native multi-agent orchestrator: it runs each node of
a YAML-defined DAG as a real `claude -p` subprocess on the user's own
logged-in Claude subscription (never the metered Anthropic API). Nodes run
with session persistence on, so every node is also an ordinary claude session
in `~/.claude/projects` that any external tool can read.

**DESIGN.md is the spec.** Read it before touching the scheduler, the graph
schema, the `NodeRunner` interface, or handoff. If code and DESIGN.md
disagree, treat that as a bug in one of them, and fix both together — don't
let them drift apart.

## Load-bearing invariants — do not weaken these

- **Subscription-auth env scrub.** Every child process's environment must have
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` deleted, via the shared
  `internal/childenv.Scrub` — used by ALL spawners (a claude node, a
  `success_check.verify` command, the git commands behind a node's
  `worktree:`, and the browser launch of the `serve` URL). This is what keeps
  the tool on subscription billing instead of silently falling back to
  metered API billing. It is unit-tested in
  `internal/childenv/childenv_test.go` and at each call site
  (`internal/runner/claude_test.go`, `internal/verify/shell_test.go`,
  `internal/worktree/git_test.go`, `internal/browser/exec_test.go`) — don't
  touch env construction without keeping those tests meaningful.
- **The four exec seams.** Exactly four objects may spawn a process:
  `runner.ClaudeCLIRunner` (a node's claude subprocess),
  `verify.ShellVerifier` (a node's evidence command),
  `worktree.GitManager` (the `git worktree` commands behind a node's
  `worktree:`) and `browser.ExecOpener` (the `open`/`xdg-open` launch of the
  `serve` URL) — see `docs/adr/0002-verification-is-a-second-exec-seam.md`,
  `docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md` and
  `docs/adr/0006-browser-open-is-a-fourth-exec-seam.md`.
  Everything else (scheduler, CLI, handoff, ledger, coordinator) depends on
  the `NodeRunner`, `Verifier`, `worktree.Provider` and `browser.Opener`
  interfaces only, so the whole engine is testable via the scripted
  `FakeRunner`/`FakeVerifier`/`FakeManager`/`FakeOpener` with zero real
  spawns. A fifth spawner needs its own ADR.
- **Artifact handoff is the default.** `handoff: artifact` persists a node's
  result to `~/.oh-my-graph/runs/<run-id>/<node-id>.out` (base overridable via
  `OMG_HOME`); `handoff: session`
  (resuming `--resume <session_id>`) is opt-in and only valid with exactly one
  session-parent.
- **Never the Agent SDK. Never `--bare`. Never `--no-session-persistence`.**
  The node runtime is exclusively the `claude` CLI subprocess, with OAuth
  intact and session persistence on (so every node stays observable as an
  ordinary session transcript).

See [SECURITY.md](SECURITY.md) for the full ToS/security stance and
[CONTRIBUTING.md](CONTRIBUTING.md) for how these invariants are enforced in
review.

## Build / test / smoke

```sh
make build      # go build -o bin/oh-my-graph ./cmd/oh-my-graph
make test       # go test ./... -race -count=1 — FakeRunner only, no real claude
make vet        # go vet ./...
make fmt-check  # gofmt -l . (CI gate)
make smoke      # MANUAL ONLY: real claude, a few cents, never in CI
```

All engine logic (scheduler, DAG validation, handoff, retry, halt-on-fail) is
covered by `internal/runner.FakeRunner` fixtures. If you add or change
scheduling/runtime behavior, write the test against `FakeRunner`, not a real
`claude` spawn.

## Repo layout (see DESIGN.md "Repo layout" for the authoritative version)

```
cmd/oh-my-graph/       CLI entrypoint and flag parsing
internal/graph/        Graph/Node value objects, YAML load, DAG validation
internal/schedule/     ready-set scheduler (the engine's core)
internal/runner/       NodeRunner interface, ClaudeCLIRunner, FakeRunner
internal/verify/       Verifier interface, ShellVerifier, RefusingVerifier, FakeVerifier
internal/worktree/     worktree Provider seam: GitManager (third exec seam), RefusingProvider, FakeManager
internal/browser/      browser Opener seam: ExecOpener (fourth exec seam), RefusingOpener, FakeOpener
internal/childenv/     the shared child-env scrub policy (used by all four spawners)
internal/invariants/   test-only: asserts exactly the four exec-seam files import os/exec
internal/coordinator/  auto mode: goal → planner call → validated graph + ToolPolicies; agent/skill mapping; the goal loop
internal/handoff/      {{inputs}}/{{artifacts}} interpolation, artifact/session handoff, the advisory lint sweeps
internal/runstate/     state.json snapshot — atomic write, run lock, resume load
internal/runfeed/      events.jsonl append-only event stream (consumer contract, docs/RUN-FEED.md)
internal/serve/        `serve`: 127.0.0.1-only dashboard + per-run live view, a run-feed consumer
internal/ledger/       RunLedger (per-node + total cost/verdict summary)
internal/gate/         gate Decision + PauseController/RecordedController (human pause/approve)
graphs/                shipped example graphs + graphs/fragments/, embedded with //go:embed *.yaml fragments/*.yaml
docs/adr/              architecture decision records
```
