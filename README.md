<p align="center">
  <img src="assets/icon-round.png" alt="oh-my-graph logo" width="128" />
</p>

<h1 align="center">oh-my-graph</h1>

<p align="center"><em>Describe the goal — it runs the graph, on your Claude subscription.</em></p>

<p align="center">
  <a href="https://github.com/jitokim/oh-my-graph/releases"><img src="https://img.shields.io/github/v/release/jitokim/oh-my-graph?include_prereleases&amp;label=release&amp;color=blue" alt="Latest release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&amp;logoColor=white" alt="Go 1.25" /></a>
  <img src="https://img.shields.io/badge/runs%20on-Claude%20subscription-ff8a65?logo=anthropic&amp;logoColor=white" alt="Runs on your Claude subscription" />
</p>

<p align="center">
  <img src="assets/hero.png" alt="oh-my-graph" width="100%" />
</p>

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

How oh-my-graph compares to its nearest neighbours — conductor, OMK,
open-multi-agent — is surveyed in [docs/PRIOR-ART.md](docs/PRIOR-ART.md).

## Bring your own login

oh-my-graph never ships credentials, never proxies auth, and never runs as a
shared service. It re-uses **your own** already-logged-in `claude` session — the
same standing as running `claude -p` yourself, or as
[claude-squad](https://github.com/smtg-ai/claude-squad). It is a personal, local
tool.

To keep that guarantee real, every node subprocess starts from your environment
with `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted** — those silently
switch `claude` to metered API billing. The scrub is unit-tested
(`internal/runner/claude_test.go`); oh-my-graph never uses `--bare` (which
disables OAuth) and never touches the Agent SDK. Full stance:
[SECURITY.md](SECURITY.md).

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

No `ANTHROPIC_API_KEY` needed — the smoke test runs on your logged-in `claude`
subscription; if the key (or `ANTHROPIC_AUTH_TOKEN`) is set in your shell,
it's deleted from each node's subprocess environment before that node runs
(see [Bring your own login](#bring-your-own-login) above).

While it runs you'll see a live line per node — `▶ write  running…`, then
`✓ write  PASS  $0.0091  4.2s` — the terminal is never silent during a
multi-node run. At the end you get a ledger: one row per node (session id,
cost, verdict, duration) and the total cost — see [Example](#example) below.

When stdout is a terminal, `run` and `auto` also serve the [web live
view](#usage) of the starting run on an ephemeral `127.0.0.1` port and open it
in your default browser; the server lives exactly as long as the run. In a
script, a pipe, or CI (stdout not a terminal) — or with `--no-web` — nothing
is served or opened and the output is unchanged.

### Zero-config: auto mode

Don't want to write YAML? Give `auto` a goal and it plans the graph for you —
one claude call (through the same subscription-auth, env-scrubbed runner) turns
the goal into a graph spec, which is validated and executed by the same engine:

```sh
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD
```

The plan is printed before it runs, and the generated spec is saved to
`~/.oh-my-graph/runs/<run-id>/graph.json` — since JSON is valid YAML you can
hand-edit it and re-run it with `oh-my-graph run`. A planned node can never use
`permission_mode: bypassPermissions`; custom YAML remains the path for precise
control. [docs/EXAMPLES.md](docs/EXAMPLES.md#zero-config-auto-mode-the-headline)
walks through the plan output, the tool ceiling, and the live node feed.

## Usage

```
oh-my-graph <run|auto|lint|chat|resume|runs|show|watch|serve|version> ...
```

| subcommand | purpose |
|---|---|
| `run <graph.yaml>` | Execute a hand-written DAG — the precise-control path. `--dry-run` validates, resolves `--input` interpolation, prints the plan, runs nothing. |
| `auto "<goal>"` | Plan a DAG from a plain-language goal, then execute it with the same engine — the zero-config default. |
| `lint <graph.yaml>` | Statically validate a graph file, reporting every problem at once. Read-only, zero cost. |
| `chat` | Interactive REPL (prototype): conversational turns are answered, task-shaped turns are planned into a graph and run. |
| `resume <run-id> (--approve \| --reject) <gate-id>` | Resume a run paused at a human-approval gate node. |
| `runs list` | List runs, newest first: graph name, node count, cost, verdict, plus a total. Read-only. |
| `show <run-id>` | Print one run's per-node ledger (session, cost, verdict, duration) and the total. Read-only. |
| `watch <run-id>` | Tail a run's event stream as plain text, `tail -f` style. Read-only. |
| `serve [<run-id>]` | Read-only web live view of a run, bound to `127.0.0.1` only (default port 8642, `--port` to change). |
| `version` | Print the tool version. |

`run` and `auto` share `--input k=v` (repeatable), `--concurrency N` (ceiling
10), and `--continue-on-fail`. Both print a live per-node feed as the graph
executes, then a cost ledger.

`lint` checks structure — DAG/cycle, unknown `depends_on` ids, the
session-handoff parent rule, verify blocks — and exits 0 when valid, 1 when
not. On a valid graph it also prints advisory `warning:` lines to stderr for
placeholder-like `{{ ... }}` tokens that won't resolve — a typoed filter
(`| inlin`), a singular `{{ artifact.x }}`, an undeclared input, or an
`artifacts.<id>` naming a node that doesn't exist or isn't an ancestor.
Warnings never change the exit code. At run time, malformed tokens pass
through verbatim (a prompt may legitimately contain literal `{{ }}` text),
while a well-formed reference to an undeclared input or unknown node fails
its node when interpolation runs.
`run --dry-run` shares that exit contract and the same warnings, and
additionally proves `{{ inputs.* }}` resolution against your actual
`--input` values. An
in-flight run shows in `runs list` as `RUNNING` (with `-` placeholders until
its first snapshot lands).

Every run persists to `~/.oh-my-graph/runs/<run-id>/` (set `OMG_HOME` to
relocate the base) — the same directory no matter where you invoke the tool
from: a versioned snapshot (`state.json`) and an append-only event stream
(`events.jsonl`), which `runs list` / `show` / `watch` / `serve` read back and
a consumer like fleetops can tail. The layout is a documented, stable
contract — see [docs/RUN-FEED.md](docs/RUN-FEED.md).

## Use it from Claude Code (plugin)

The CLI above is the product. To stay inside a Claude Code session instead,
[`plugin/`](plugin/) is a thin plugin adding a `/graph` slash command — it
shells out to the same `oh-my-graph` binary, no logic reimplemented — plus a
graph-engineering **agent** as the lower-friction entry point: add
`omg () { claude --agent oh-my-graph "$@"; }` to your shell rc, and `omg`
opens a session where every turn is graph-aware. Install and usage:
[plugin/README.md](plugin/README.md) ([agent section](plugin/README.md#the-oh-my-graph-agent-ambient-entry-point)).

## Example

The cheapest real end-to-end check: the [Quickstart](#quickstart) command,
using the shipped `graphs/haiku-smoke.yaml` (two nodes: `write` then
`critique`, wired by the default artifact handoff). What you'll see:

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

More walkthroughs — auto mode in depth, dogfooding, observing with fleetops,
ambient chat — plus per-feature recipes live in
[docs/EXAMPLES.md](docs/EXAMPLES.md).

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
    cwd: "{{ inputs.repo }}"  # a session child works in its parent's tree
    handoff: session          # e2e resumes dev's session — it already knows everything dev just did
    prompt: Run make local and report PASS or FAIL.
    success_check:
      exit_zero: true
      result_matches: "PASS"          # what the node said
      verify: { command: "make local" }  # what the engine saw
    retry: { max: 1, on: [nonzero_exit, verify_failed] }

  - id: review
    depends_on: [e2e]
    permission_mode: plan     # read-only
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

### Handoff — what a child inherits

Edges say *when* a node runs; `handoff` says *what* it inherits from its
parent.

|                    | `artifact` (default) | `session` |
|--------------------|----------------------|-----------|
| The child inherits | the parent's **final reply**, persisted to `~/.oh-my-graph/runs/<run-id>/<node-id>.out` and substituted wherever `{{ artifacts.<id> }}` appears — the file path by default, the reply text itself with the `\| inline` filter | the parent's **claude session**, resumed with `--resume`: everything the parent read, did and concluded, not just its reply. The conversation, not the configuration — `allowed_tools`, `permission_mode`, `agent`, `cwd` and `budget_usd` are always the child's own |
| Parents allowed    | any number — fan-in and fan-out belong to artifact | exactly one `claude-run` node (a root, a fan-in or a gate parent is rejected at load time), sharing the parent's `cwd`/`worktree` — `lint` warns on a mismatch |
| Session shape      | each node is a fresh claude session | a sequential chain continuing one conversation |

Why it matters: with `artifact`, context the parent didn't put into its final
reply is gone — the child starts cold. With `session`, the child picks up
mid-conversation, so a tight pipeline (implement, then test what you just
built) needs no re-explaining. Session children still write their own
`prompt` — what they inherit is the context, not the instructions.

Beyond the sample, a node can opt into (DESIGN.md is the authoritative spec):

- **`agent:`** — run the node as one of your own Claude Code subagents, with its
  system prompt, tools and model ([spec](DESIGN.md#node-as-subagent-agent-v11--hand-written-graphs-only) · [recipe](docs/EXAMPLES.md#running-a-node-as-your-own-subagent-agent)).
- **`worktree:`** — parallel edit lanes in managed git worktrees, one isolated
  checkout per lane name ([spec](DESIGN.md#worktree-isolation-worktree--hand-written-graphs-only) · [recipe](docs/EXAMPLES.md#parallel-edit-lanes-with-git-worktrees-worktree)).
- **`handoff`** — see [Handoff — what a child inherits](#handoff--what-a-child-inherits)
  above ([spec](DESIGN.md#handoff--artifact-default-session-opt-in-committed) · [recipe](docs/EXAMPLES.md#artifact-fan-out-vs-session-chain-handoff)).
- **`success_check` / `retry`** — evidence-grounded gating (`exit_zero`,
  `result_matches`, and the engine-run `verify` command) plus per-cause retry ([spec](DESIGN.md#success-checks--evidence-grounded-verification-v11)).
- **`budget_usd`** — a per-node cost cap, enforced live (`--max-budget-usd`) and
  post-hoc ([spec](DESIGN.md#execution-engine) · [recipe](docs/EXAMPLES.md#budgets-budget_usd)).
- **gates** — a `type: gate` node pauses the run for human approval, continued
  with `oh-my-graph resume` ([spec](DESIGN.md#gate-nodes-and-resume-v11)).

## Platform support

macOS and Linux are the supported targets; CI builds and tests on Linux.
**WSL is first-class**: a WSL build *is* a Linux build and takes the identical
code path — provided the `claude` CLI and `sh` live inside the distro. Native
Windows compiles and runs, best-effort (no Windows CI): `verify` runs under
`cmd /c` rather than `sh -c`, cancellation kills only the direct child, and
the env scrub is case-sensitive — prefer WSL. Full platform detail, known
limitations and the deferred list: [docs/LIMITATIONS.md](docs/LIMITATIONS.md).

## Development

```sh
make build      # build the binary
make test       # go test ./... -race
make vet        # go vet ./...
make fmt        # gofmt -w . (formats in place; always exits 0)
make fmt-check  # fails if any file is not gofmt-clean (the CI gate)
```

All engine logic is tested against a scripted `FakeRunner` — the test suite
never spawns a real `claude`. The real-claude smoke (`make smoke`) is a
**manual** step, never part of CI, so CI stays free.

## License

[MIT](LICENSE) © jitokim
