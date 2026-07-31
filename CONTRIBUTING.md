# Contributing to oh-my-graph

Thanks for considering a contribution. oh-my-graph is a small, opinionated
project — please read [DESIGN.md](DESIGN.md) first. It is the architecture
source of truth: object responsibilities, the execution model, and the MVP
scope boundary all live there. If a PR and DESIGN.md disagree, DESIGN.md wins
until it is updated in the same PR.

## Build, test, smoke

```sh
make build   # go build -o bin/oh-my-graph ./cmd/oh-my-graph
make test    # go test ./... -race -count=1
make vet     # go vet ./...
make fmt     # gofmt -w .
make fmt-check  # gofmt -l . ; fails if anything is unformatted (CI gate)
```

`make test` runs the entire suite against a scripted `FakeRunner`
(`internal/runner/fake.go`) — no test in the repo spawns a real `claude`
process. That is intentional: the ready-set scheduler, DAG validation,
handoff, retry, and halt-on-fail logic are all exercised through
`map[nodeID]NodeOutcome` fixtures, so CI never needs a `claude` login and
never costs money.

```sh
make smoke   # builds the binary, then runs graphs/haiku-smoke.yaml for real
```

`make smoke` is the one command that spawns a **real** `claude` subprocess. It
costs a few cents on your own subscription and requires you to be logged in.
It is a manual, local-only step — **never** add it to CI, and don't submit a
PR that wires it into a workflow.

## Branch and PR conventions

- Branch names: `feat/...`, `fix/...`, `chore/...`, `docs/...` — matching the
  change type.
- Commit messages: [Conventional Commits](https://www.conventionalcommits.org/)
  (`feat:`, `fix:`, `docs:`, `chore:`, optionally scoped, e.g.
  `fix(graph): ...`), matching the existing history.
- Keep PRs scoped to one change. If a PR touches the execution model (the
  scheduler, the `NodeRunner` interface, handoff, or the graph schema), update
  DESIGN.md in the same PR — don't let the doc drift from the code.
- New architectural decisions (not just implementation detail) belong in
  `docs/adr/`, following the style of `docs/adr/0001-subprocess-not-sdk.md`.

## The exec seams — the one rule that matters most

**Exactly four objects in this codebase may spawn a process**, each behind
its own injected interface:

| object | runs | interface |
|---|---|---|
| `internal/runner.ClaudeCLIRunner` | a node's `claude -p` subprocess | `runner.NodeRunner` |
| `internal/verify.ShellVerifier` | a node's `success_check.verify` command | `verify.Verifier` |
| `internal/worktree.GitManager` | the `git worktree` commands behind a node's `worktree:` | `worktree.Provider` |
| `internal/browser.ExecOpener` | the `open`/`xdg-open` launch of the `serve` URL | `browser.Opener` |

```go
type NodeRunner interface {
	Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
}

type Verifier interface {
	Verify(ctx context.Context, req Request) (Result, error)
}

type Provider interface {
	Acquire(ctx context.Context, name string) (string, error)
}

type Opener interface {
	Open(ctx context.Context, url string) error
}
```

Every other package — the scheduler, the CLI, handoff, ledger, coordinator —
depends only on those interfaces. That is what keeps the entire engine testable
via `FakeRunner`, `FakeVerifier`, `worktree.FakeManager` and
`browser.FakeOpener` with zero real spawns, and it keeps the
subscription-auth env scrub (`internal/childenv`) to exactly one call site
per spawner.

If your change needs to run a subprocess, it belongs behind one of the four
existing seams. A PR that spawns a process (via `os/exec` or any other way of
shelling out) outside `runner.ClaudeCLIRunner`, `verify.ShellVerifier`,
`worktree.GitManager` and `browser.ExecOpener` should be treated as a design
regression, not a normal review comment — a **fifth** spawner needs an ADR
first, the way the second, third and fourth got
[ADR 0002](docs/adr/0002-verification-is-a-second-exec-seam.md),
[ADR 0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md) and
[ADR 0006](docs/adr/0006-browser-open-is-a-fourth-exec-seam.md). (The invariant
is about *objects that spawn*, not `os/exec` imports per se: the runner and
verify seam packages also carry build-tagged process-group helpers that import
`os/exec` solely to kill a cancelled child's tree — they never start one.)

## Invariants a contribution must preserve

These are the load-bearing guarantees the whole project exists to make (see
[SECURITY.md](SECURITY.md) for the full stance). A PR that weakens any of them
without an explicit, discussed design change should not be merged:

- **Subscription-auth env scrub.** Every child process oh-my-graph spawns —
  a claude node, a `success_check.verify` command, AND the git commands behind
  a node's `worktree:` — has `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN`
  deleted from its environment by the shared `internal/childenv.Scrub`. Those
  variables silently switch `claude` to metered API billing;
  `verify: { command: "claude -p ..." }` is a legal thing to write, and a
  repo's own git hooks may invoke claude too, so every spawner must apply it.
  It is asserted in `internal/childenv/childenv_test.go` (the policy) and at
  each call site (`internal/runner/claude_test.go`,
  `internal/verify/shell_test.go`, `internal/worktree/git_test.go`) — if you
  touch env construction, make sure those tests still prove the scrub.
- **Never the Agent SDK.** The node runtime is exclusively the `claude` CLI
  subprocess (`claude -p ... --output-format json`). Don't introduce an
  Anthropic API/Agent SDK dependency as an alternate or default runtime path.
- **Never `--bare`.** That flag disables OAuth and would break subscription
  auth. Don't add it to the built argv.
- **Never `--no-session-persistence`.** Nodes run with session persistence on
  so every node shows up as an ordinary session in `~/.claude/projects` —
  that's the free integration with [fleetops](https://github.com/jitokim/fleetops).
  Don't add a flag or option that turns it off by default.
- **Every field on `graph.Node` has an explicit auto-mode disposition.** A
  planner reply is untrusted input, so `coordinator.validatePlannedNodes` must
  allow, constrain, or reject every field a plan can set — this class of hole
  recurs each time the schema grows. It is enforced, not just requested:
  `internal/coordinator/field_dispositions_test.go` walks `graph.Node` by
  reflection and fails on any field with no recorded disposition. **If you add
  a field to `Node`, that test will fail until you decide what auto mode does
  with it.** Recording it as `rejected` also requires a probe proving the
  rejection actually fires.
- **The auto-mode tool ceiling is auto mode's alone.** Hand-written graphs
  (`oh-my-graph run`) must keep running under the user's own settings, hooks
  and MCP servers — they are the user's reviewed artifact, and that path exists
  for precise control. Don't extend `--setting-sources ""`, `--tools`,
  `--strict-mcp-config` or `--disallowedTools` to it.
- **Don't narrow a published security claim ahead of a measurement.** The
  scoped-`Bash` wording in README/SECURITY.md was only softened after a real
  `claude` invocation demonstrated the behaviour (DESIGN.md, "Empirical
  verification of the tool ceiling"). `--help` prose has already been wrong
  once. If you can't measure it, say it's unverified — an honest partial
  mechanism beats an overclaimed complete one.

## Scope

Before proposing a feature, check DESIGN.md's "MVP scope" and "Deferred"
sections. Things like a graph DSL beyond `depends_on`, retry policies beyond
a flat `max`, and a TUI are deliberately out of scope
for v0.1 — that's not an oversight, it's a documented boundary. If you want to
build one of those, open an issue to discuss the design first rather than
sending a large PR.

## Questions

Open an issue. This is a young project — there's no separate chat/forum yet.
