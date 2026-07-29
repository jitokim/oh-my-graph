# oh-my-graph

> A graph-native multi-agent orchestrator whose node runtime is your own
> logged-in `claude` CLI — not the Anthropic API.
>
> **It executes; [fleetops](https://github.com/jitokim/fleetops) observes the
> same `~/.claude/projects` transcripts.**

## The whitespace

Graph engineering — wiring specialized agents together as a DAG — currently
forces you onto the Anthropic API, the Agent SDK, and a metered
`ANTHROPIC_API_KEY`. Every existing graph-native orchestrator bills per token.

There is no orchestrator that drives the **subscription** `claude` CLI. That is
the gap oh-my-graph fills: each DAG node runs as a raw `claude -p` subprocess on
the plan you already pay for. Marginal cost per node: `$0`, inside your Max/Pro
subscription.

## Bring your own login

oh-my-graph never ships credentials, never proxies auth, and never runs as a
shared service. It re-uses **your own** already-logged-in `claude` session — the
same standing as running `claude -p` yourself, or as
[claude-squad](https://github.com/smtg-ai/claude-squad). It is a personal, local
tool.

To keep that guarantee real, every node subprocess starts from your environment
with `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted** — those silently
switch `claude` to metered API billing. This scrub is unit-tested (see
`internal/runner/claude_test.go`). It never uses `--bare` (which disables OAuth)
and never touches the Agent SDK. See [SECURITY.md](SECURITY.md) for the full
stance.

## Pairs with fleetops

Nodes run in real working directories with session persistence **on**, so every
node shows up as an ordinary claude session in `~/.claude/projects`.
[fleetops](https://github.com/jitokim/fleetops) — the fleet cockpit — observes
those transcripts. oh-my-graph is the executor; fleetops is the dashboard. You
get the observability integration for free.

## Quickstart

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# The cheapest real smoke test (a few cents):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

At the end of a run you get a ledger: one row per node (session id, cost,
verdict, duration) and the total cost.

```
oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]
```

- `--input k=v` binds a graph input; repeatable.
- `--concurrency N` overrides the graph's ready-set width (ceiling 10).
- `--continue-on-fail` prunes only a failed node's subtree instead of halting.

### Zero-config: auto mode

Don't want to write YAML? Give `auto` a goal and it plans the graph for you —
one claude call (through the same subscription-auth, env-scrubbed runner) turns
the goal into a graph spec, which is validated and executed by the same engine:

```sh
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD
```

The plan is printed before it runs, and the generated spec is saved to
`.oh-my-graph/runs/<run-id>/graph.json` — since JSON is valid YAML you can
hand-edit it and re-run it with `oh-my-graph run`. A planned node can never use
`permission_mode: bypassPermissions`; custom YAML remains the path for precise
control.

## Use it from Claude Code (plugin)

The CLI above is the product. If you'd rather stay inside a Claude Code
session than drop to a shell, [`plugin/`](plugin/) is a thin Claude Code
plugin that adds a `/graph` slash command — it shells out to the same
`oh-my-graph` binary, no logic is reimplemented. See
[plugin/README.md](plugin/README.md) for install and usage.

## The graph model

A graph is YAML: a `name`, optional `inputs` and `concurrency`, and a list of
`nodes`. Each node is one `claude -p` subprocess. Edges are inline `depends_on`
ids — there is no separate edge list, so the topology has a single source of
truth. Parallelism is **emergent**: nodes that share a parent but don't depend
on each other run concurrently, up to the cap.

```yaml
name: dev-review-pr
inputs: [repo]
concurrency: 4
nodes:
  - id: dev
    cwd: "{{ inputs.repo }}"
    prompt: Implement the change and summarize what you did.
    allowed_tools: [Read, Edit, Write, "Bash(git *)"]
    permission_mode: dontAsk

  - id: e2e
    depends_on: [dev]
    handoff: session          # resume dev's session (tight sequential continuation)
    prompt: Run make local and report PASS or FAIL.
    success_check: { exit_zero: true, result_matches: "PASS" }
    retry: { max: 1, on: [nonzero_exit] }

  - id: review
    depends_on: [e2e]
    permission_mode: plan     # read-only
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

### Handoff — how a node receives its parent's work

- **`artifact` (default):** the engine persists every node's result to
  `.oh-my-graph/runs/<run-id>/<node-id>.out`; dependents read it via
  `{{ artifacts.<id> }}` (the file **path** by default, or the file **content**
  with the `| inline` filter). Robust, inspectable, parallel-safe — and the only
  option for a fan-in (many parents).
- **`session`:** the node resumes its single parent's claude session with
  `--resume`. For tight sequential continuation in the same working tree. A node
  with two or more parents may not use `session` (a session can't merge) — that
  is rejected at load time.

### Success checks and retry

`success_check` gates a node: `exit_zero` requires a clean exit, and
`result_matches` is a regex over the node's result text. An empty check means
"exit zero is enough". `retry` re-runs a failed node up to `max` times when the
failure cause is listed in `on` — always in a fresh session.

## Deferred (not in v0.1)

Called out honestly — these are **not** implemented yet:

- **`gate` / human pause + `oh-my-graph resume`** (v1.1). The `gate` node type is
  schema-reserved so graphs parse, but executing one is rejected with a clear
  "not yet implemented".
- retries beyond a flat `max`; parallel-group sugar / any DSL beyond `depends_on`.
- TUI / dashboard — that is [fleetops](https://github.com/jitokim/fleetops)'s job.
- budget enforcement of any kind: `budget_usd` is parsed onto the node and the
  RunLedger records each node's actual cost, but v0.1 does not enforce a cap —
  neither a post-hoc halt of subsequent nodes nor a mid-node kill. Both are
  deferred to v1.1.
- worktree auto-creation for parallel edits (parallel v0.1 nodes should be
  read-only reviews).

## Development

```sh
make build   # build the binary
make test    # go test ./... -race
make vet     # go vet ./...
make fmt     # gofmt -l . (fails if anything is unformatted)
```

All engine logic is tested against a scripted `FakeRunner` — the test suite
never spawns a real `claude`. The real-claude smoke (`make smoke`) is a **manual**
step, never part of CI, so CI stays free.

## License

[MIT](LICENSE) © jitokim
