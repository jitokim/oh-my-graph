<p align="center">English | <a href="README.ko.md">한국어</a></p>

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

## It ships itself

Dogfooding here is not a demo: this repository is built by the tool it
contains. Features, fixes, docs and releases are authored by its own graphs —
a claude node implements on a branch, sibling nodes run the checks and the
reviews, and a final node opens the draft PR. The verifiable part, as of
2026-08-02: 23 of the 80 pull requests merged into `main` carry a Claude
co-author trailer in their squash commit — the receipt that a claude session
wrote them. Count them yourself:
`git log main --first-parent -i --grep="co-authored-by: claude"`
(24 matches: those 23 squash commits plus the initial commit). That trailer
names the model, not the pipeline, so from 2026-08-02 on, commits authored by
a graph lane also carry `Co-Authored-By: oh-my-graph <graphs@oh-my-graph.dev>`
— a transparency convention, not proof of authorship; see
[CONTRIBUTING.md](CONTRIBUTING.md#attribution).

The templates in [`graphs/`](graphs/) are not samples: `self-dev.yaml`,
`adr-driven-dev.yaml` and `apply-flags.yaml` are the pipelines this repo
ships itself with. A full dogfooding run is walked through in
[docs/EXAMPLES.md](docs/EXAMPLES.md#dogfooding-developing-oh-my-graph-with-oh-my-graph).

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
cost, verdict, detail) and the total cost — see [Example](#example) below.

When stdout is a terminal, `run`, `auto` and `resume` also serve the [web live
view](#usage) of the leg they are starting on an ephemeral `127.0.0.1` port and
open it in your default browser; the server lives exactly as long as that leg.
In a script, a pipe, or CI (stdout not a terminal) — or with `--no-web` —
nothing is served or opened and the output is unchanged.

<p align="center">
  <img src="assets/live-view.png" alt="Web live view of a real oh-my-graph run: node output feed on the left, DAG map with passed/running/pending nodes on the right, live cost and elapsed time in the header" width="100%" />
</p>
<p align="center"><em>The live view mid-run — a real dogfood run (the ADR-0012 skill-mapping graph) captured live: node output feed on the left, the DAG map on the right, cost and elapsed time in the header.</em></p>

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
control.

Want `auto` to keep going until the goal is actually met? `--max-cycles N`
(default 1) turns one invocation into a bounded loop of up to N whole
plan→run→assess cycles: after each run, a tool-stripped assessor judges the
goal against the run's own recorded evidence, and if work remains, the next
cycle replans around it — every cycle re-validated under the same tool
ceiling, every plan and verdict printed as it happens, and a goal summary
totalling each cycle's spend at the end. Exit 0 requires both a goal-met
verdict and a passed final run. `--max-goal-budget-usd X` adds an optional
soft spend ceiling checked between cycles; it requires `--max-cycles` of at
least 2, since a single-cycle run has no cycle boundary to check it at, and
the flag is rejected at parse otherwise. Stated honestly: `auto` is
non-interactive, so an unattended `--max-cycles 5` may spend five planner
calls, five graphs and five assessments with nobody watching — the
governance is the bound you typed, the per-cycle validation, and the printed
record, not a confirmation prompt.

If you have your own Claude Code agents (`~/.claude/agents`, `./.claude/agents`
— project wins), `auto` also maps planned nodes onto them when a node's id
clearly matches an agent's name — your review node runs as *your*
`code-reviewer`. The match is deliberately conservative (one clear candidate or
nothing, and an agent wanting tools beyond the node's planned allowlist is
skipped with a note), every mapping is shown in the printed plan before
anything runs, and `--no-agent-mapping` turns it off. The trade, stated
up front: a mapped node loads your settings so the agent can resolve, instead
of running fully settings-isolated — its declared tool list still binds. [docs/EXAMPLES.md](docs/EXAMPLES.md#zero-config-auto-mode-the-headline)
walks through the plan output, the tool ceiling, and the live node feed.

## Usage

```
oh-my-graph <run|auto|lint|chat|resume|runs|show|watch|serve|version> ...
```

| subcommand | purpose |
|---|---|
| `run <graph.yaml>` | Execute a hand-written DAG — the precise-control path. `--dry-run` validates, resolves `--input` interpolation, prints the plan, runs nothing. |
| `auto "<goal>"` | Plan a DAG from a plain-language goal, then execute it with the same engine — the zero-config default. `--max-cycles N` iterates plan→run→assess up to N times (`--max-goal-budget-usd` adds a soft spend ceiling between cycles; requires `--max-cycles` of 2 or more). |
| `lint <graph.yaml>` | Statically validate a graph file, reporting every problem at once. Read-only, zero cost. |
| `chat` | Interactive REPL (prototype): conversational turns are answered, task-shaped turns are planned into a graph and run. |
| `resume <run-id> ((--approve \| --reject) <gate-id> \| --retry-failed)` | Resume a run: decide the gate it is paused at, or `--retry-failed` to salvage a failed run — passed nodes' results are kept and only the failed and cancelled nodes re-execute. Takes `--concurrency N` and `--no-web`. |
| `runs list` | List runs, newest first: graph name, node count, cost, verdict, plus a total. Read-only. |
| `show <run-id>` | Print one run's per-node ledger (session, cost, verdict, duration) and the total. Read-only. |
| `watch <run-id>` | Tail a run's event stream as plain text, `tail -f` style. Read-only. |
| `serve [<run-id>]` | Web live view of a run, bound to `127.0.0.1` only (default port 8642, `--port` to change). Read-only except for one thing: a run paused at a gate can be approved or rejected from the page. |
| `version` | Print the tool version. |

`run` and `auto` share `--input k=v` (repeatable), `--concurrency N` (ceiling
10), and `--continue-on-fail`. Both print a live per-node feed as the graph
executes, then a cost ledger. A graph can also declare the failure policy
itself with graph-level `on_fail: continue` (default `halt`) — the right
default for a batch of independent lanes, where one lane's failure should
not cancel the others' in-flight work. The flag ORs with the field: either
saying continue means continue.

`lint` checks structure — DAG/cycle, unknown `depends_on` ids, the
session-handoff parent rule, verify blocks — and exits 0 when valid, 1 when
not. On a valid graph it also prints advisory `warning:` lines to stderr for
placeholder-like `{{ ... }}` tokens that won't resolve — a typoed filter
(`| inlin`), a singular `{{ artifact.x }}`, an undeclared input, or an
`artifacts.<id>` naming a node that doesn't exist or isn't an ancestor —
plus, for a `handoff: session` node, a `cwd`/`worktree` that differs from
its session-parent's, or a `retry` block (a retried attempt starts cold).
Warnings never change the exit code. At run time, malformed tokens pass
through verbatim (a prompt may legitimately contain literal `{{ }}` text),
while a well-formed reference to an unbound input or unknown node fails
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

### Recurring pipelines — write it once

A graph file is your prompt engineering, saved. The careful
goal/format/rules prompt you would otherwise re-type into a chat every
morning lives in the YAML once, and `oh-my-graph run pipeline.yaml` replays
it identically on demand — daily analysis, weekly triage, release checks —
on the subscription you already pay for. Within one run, `handoff: session`
keeps the chain's context flowing, so downstream prompts stay one-liners
instead of restating the goal and the format — see
[Handoff](#handoff--what-a-child-inherits) below.

```yaml
name: daily-triage
nodes:
  - id: collect             # the careful goal/format/rules prompt lives here, once
    prompt: >
      Collect today's open issues and failing checks; list each with a
      one-line status.
  - id: analyze
    depends_on: [collect]
    handoff: session        # continues collect's conversation
    prompt: Analyze what you just collected and rank by urgency.
  - id: report
    depends_on: [analyze]
    handoff: session        # the chain keeps flowing
    prompt: Write the ranked findings up as a short report.
```

One boundary, stated plainly: **runs do not remember each other.** Each run
starts fresh by design
([ADR 0008](docs/adr/0008-cross-run-session-reuse-is-deferred.md) records
why cross-run session reuse is deferred) — day-to-day consistency comes
from the pinned prompts and the `success_check` / `verify` gates, not from
Claude remembering yesterday.

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
- **`timeout`** — a per-node wall-clock bound replacing the 20-minute default,
  for nodes whose legitimate work runs long ([spec](DESIGN.md#execution-engine) · [ADR 0007](docs/adr/0007-per-node-execution-limits.md)).
- **`feedback:`** — a bounded review loop without unrolling it: when a
  reviewer node fails its judgment, `feedback: { rerun: impl, max: 2 }`
  re-runs the path from `impl` back to the reviewer, handing the findings to
  the re-run as `{{ feedback.review }}` (empty on the first pass) — at most
  `max` times, every round priced in the ledger
  ([spec](DESIGN.md#execution-engine) · [ADR 0010](docs/adr/0010-a-feedback-edge-is-a-bounded-runtime-rerun-not-a-static-cycle.md) · demo: `graphs/review-loop.yaml`).
- **gates** — a `type: gate` node pauses the run for human approval, continued
  with `oh-my-graph resume` ([spec](DESIGN.md#gate-nodes-and-resume-v11)).
- **failure salvage** — `resume <run-id> --retry-failed` re-executes only a
  failed run's failed and cancelled nodes, keeping every passed node's
  artifact for its dependents ([spec](DESIGN.md#gate-nodes-and-resume-v11)).
- **session limits pause, not fail** — when your subscription hits its
  session limit mid-run, the limited node is not marked failed: the run stops
  launching new work, lets in-flight nodes finish, and exits with code 2 and
  a hint like `Resume after 5:20pm with: oh-my-graph resume <run-id>
  --retry-failed` — which later finishes exactly the work that never ran.
  Detection is honest string-matching on the CLI's message (it offers no
  structured signal), so an unrecognized wording safely degrades to an
  ordinary failure that the same command still salvages
  ([ADR 0009](docs/adr/0009-a-session-limit-is-a-pause-not-a-failure.md)).

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
