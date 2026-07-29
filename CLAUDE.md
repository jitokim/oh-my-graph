# oh-my-graph — instructions for Claude Code

This is the project-level Claude Code config for **oh-my-graph** itself
(public, committed — no personal or private settings live here).

## What this project is

oh-my-graph is a graph-native multi-agent orchestrator: it runs each node of
a YAML-defined DAG as a real `claude -p` subprocess on the user's own
logged-in Claude subscription (never the metered Anthropic API). It
**executes**; [fleetops](https://github.com/jitokim/fleetops) **observes**
the same `~/.claude/projects` session transcripts — the two projects pair
1:1 (executor + dashboard).

**DESIGN.md is the spec.** Read it before touching the scheduler, the graph
schema, the `NodeRunner` interface, or handoff. If code and DESIGN.md
disagree, treat that as a bug in one of them, and fix both together — don't
let them drift apart.

## Load-bearing invariants — do not weaken these

- **Subscription-auth env scrub.** Every child process's environment must have
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` deleted, via the shared
  `internal/childenv.Scrub` — used by BOTH spawners (a claude node and a
  `success_check.verify` command). This is what keeps the tool on subscription
  billing instead of silently falling back to metered API billing. It is
  unit-tested in `internal/childenv/childenv_test.go` and at each call site
  (`internal/runner/claude_test.go`, `internal/verify/shell_test.go`) — don't
  touch env construction without keeping those tests meaningful.
- **The two exec seams.** Exactly two objects may import `os/exec`:
  `runner.ClaudeCLIRunner` (a node's claude subprocess) and
  `verify.ShellVerifier` (a node's evidence command) — see
  `docs/adr/0002-verification-is-a-second-exec-seam.md`. Everything else
  (scheduler, CLI, handoff, ledger, coordinator) depends on the `NodeRunner`
  and `Verifier` interfaces only, so the whole engine is testable via the
  scripted `FakeRunner`/`FakeVerifier` with zero real spawns. A third spawner
  needs its own ADR.
- **Artifact handoff is the default.** `handoff: artifact` persists a node's
  result to `.oh-my-graph/runs/<run-id>/<node-id>.out`; `handoff: session`
  (resuming `--resume <session_id>`) is opt-in and only valid with exactly one
  session-parent.
- **Never the Agent SDK. Never `--bare`. Never `--no-session-persistence`.**
  The node runtime is exclusively the `claude` CLI subprocess, with OAuth
  intact and session persistence on (so fleetops can observe every node).

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
internal/childenv/     the shared child-env scrub policy (used by both spawners)
internal/handoff/      {{inputs}}/{{artifacts}} interpolation, artifact/session handoff
internal/ledger/       RunLedger (per-node + total cost/verdict summary)
internal/gate/         v1.1 stub for the (not-yet-implemented) gate node type
graphs/                shipped example graphs (haiku-smoke, dev-review-pr)
docs/adr/              architecture decision records
```
