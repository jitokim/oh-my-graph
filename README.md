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

No `ANTHROPIC_API_KEY` needed — the smoke test runs on your logged-in
`claude` subscription. If the key (or `ANTHROPIC_AUTH_TOKEN`) happens to be
set in your shell, it's deleted from each node's subprocess environment
before that node runs (see [Bring your own login](#bring-your-own-login)
above), so this works either way.

While it runs you'll see a live line per node — `▶ write  running…`, then
`✓ write  PASS  $0.0091  4.2s` — the terminal is never silent during a
multi-node run. At the end you get a ledger: one row per node (session id,
cost, verdict, duration) and the total cost. See [Examples](#examples) below
for a full walkthrough of the output.

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
control. See [Examples](#examples) below for a full walkthrough of the plan
output and the live node feed.

## Usage

```
oh-my-graph <run|auto|version> ...
```

- **`run <graph.yaml>`** — you write the DAG in YAML, oh-my-graph executes it.
  The precise-control path: exact prompts, tools, and handoffs per node.
- **`auto "<goal>"`** — you describe a goal in plain language; a coordinator
  plans the DAG for you, then the same engine executes the generated graph.
  The zero-config default.
- **`version`** — print the tool version.

`run` and `auto` share `--input k=v` (repeatable), `--concurrency N` (ceiling
10), and `--continue-on-fail`. Both print a live per-node feed as the graph
executes, then a cost ledger — see the examples below for exactly what that
looks like.

Walk through them in order:

1. [Quickstart run](#1-quickstart-run) — the cheapest real smoke test.
2. [Zero-config: auto mode](#2-zero-config-auto-mode-the-headline) — the
   headline feature.
3. [Dogfooding](#3-dogfooding-developing-oh-my-graph-with-oh-my-graph) — using
   oh-my-graph to develop oh-my-graph.
4. [Observe with fleetops](#4-observe-with-fleetops) — watch nodes run in a
   sister tool.

## Examples

### 1. Quickstart run

The cheapest real end-to-end check, using the shipped
`graphs/haiku-smoke.yaml` (two nodes: `write` then `critique`, wired by the
default artifact handoff):

```sh
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

No API key is required — this runs on your `claude` subscription; if
`ANTHROPIC_API_KEY`/`ANTHROPIC_AUTH_TOKEN` is set in your shell it's scrubbed
from each node's subprocess before it runs. What you'll see:

```
Running graph "haiku-smoke" (run 20260729-101532)

▶ write  running…
✓ write  PASS  $0.0091  4.2s
▶ critique  running…
✓ critique  PASS  $0.0034  2.1s

Run 20260729-101532 — 2 node(s)
NODE             VERDICT    SESSION                     COST(USD)  DETAIL
------------------------------------------------------------------------------
critique         PASS       a1b2c3d4-e5f6-47a8-9c1…       0.0034
write            PASS       f9e8d7c6-b5a4-4321-8765…      0.0091
------------------------------------------------------------------------------
TOTAL COST: $0.0125
```

### 2. Zero-config: auto mode (the headline)

Don't want to write YAML? Give `auto` a goal in plain language and a
coordinator plans the DAG for you — one claude call (through the same
subscription-auth, env-scrubbed runner every node uses) turns the goal into a
graph spec, which is validated and executed by the same engine as a
hand-written graph:

```sh
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD
```

What you'll see — a plan, then the same live feed and ledger as any other run
(the planner is non-deterministic, so expect this shape rather than these
exact node names):

```
Planning a graph for goal "lint this repo and summarize the findings"...
Planned graph "lint-and-summarize" (2 nodes, planning cost $0.0021, saved to .oh-my-graph/runs/20260729-101600/graph.json):
  - lint [tools: Bash(golangci-lint *), Bash(go vet *)]
  - summarize (after lint) [tools: Read]

Running graph "lint-and-summarize" (run 20260729-101600)

▶ lint  running…
✓ lint  PASS  $0.0087  6.4s
▶ summarize  running…
✓ summarize  PASS  $0.0019  2.8s

Run 20260729-101600 — 2 node(s)
...
TOTAL COST: $0.0106
```

The generated spec is saved to `.oh-my-graph/runs/<run-id>/graph.json` —
since JSON is valid YAML, you can hand-edit it and re-run it directly with
`oh-my-graph run`. A planned node can never opt into `permission_mode:
bypassPermissions` (the coordinator rejects that before anything runs);
hand-written YAML remains the path for that and any other precise control.

**Custom YAML vs. auto, in one line:** reach for `graphs/*.yaml` when you know
exactly which tools each node should have and how they should hand off to
each other; reach for `auto` when you'd rather describe the outcome and let
the planner design the DAG.

### 3. Dogfooding: developing oh-my-graph with oh-my-graph

The shipped `graphs/self-dev.yaml` runs a dev → e2e → parallel reviews → PR
pipeline against *this* repo — the same shape as `dev-review-pr.yaml`, but it
also takes an explicit `task` input and opens the PR as a **draft** so
nothing lands unreviewed:

```sh
git checkout -b feat/my-thing
oh-my-graph run graphs/self-dev.yaml \
  --input repo="$PWD" \
  --input task="add a --dry-run flag to the run subcommand"
```

The `auto` equivalent — no hand-written graph, just the goal:

```sh
oh-my-graph auto "implement 'add a --dry-run flag to the run subcommand' in this repo, run make local to check it, review the diff for security and style, then open a draft PR" --input repo=$PWD
```

This isn't a hypothetical case study — oh-my-graph is built this way. Auto
mode itself was developed by dogfooding, and running graphs against this repo
has already caught two real bugs:

- **A validation gap.** An early `dev-review-pr` run against this repo used a
  root node with `handoff: session` (zero parents). It parsed clean and only
  failed at runtime inside the handoff resolver — the validator only rejected
  *more than one* session-parent, not zero. Fixed by tightening the load-time
  check to exactly one parent, so a graph like that now fails fast at load
  instead of mid-run.
- **The silent-terminal progress gap.** A multi-minute `dev → e2e → review →
  pr` run against this repo, with only a start banner and a final ledger,
  looked like a dead shell for most of its runtime. That's exactly why the
  live `▶ / ✓ / ✗ / ↻` feed shown throughout these examples exists.

### 4. Observe with fleetops

oh-my-graph executes; [fleetops](https://github.com/jitokim/fleetops) is a
sister tool that observes the same `~/.claude/projects` transcripts — no
coupling beyond that shared directory. Every node runs with session
persistence on, so it shows up as an ordinary claude session the moment it
starts.

Run fleetops in a second terminal tab while any of the examples above is
running, and you'll see each node appear in fleetops' fleet list as
oh-my-graph delegates to it — live, for free, with zero integration code.

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
