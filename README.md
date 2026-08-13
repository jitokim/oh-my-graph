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

## What it is

Graph engineering — wiring specialized agents together as a DAG — currently
forces you onto the Anthropic API, the Agent SDK, and a metered
`ANTHROPIC_API_KEY`. Every existing graph-native orchestrator bills per token.

oh-my-graph doesn't. You describe the work as a DAG in YAML, and each node runs
as a raw `claude -p` subprocess on the Max/Pro plan you already pay for. **That
is not the same as free.** It spends your subscription, a run has a real price,
and the ledger prints it per node — the claim is that there is no *second*,
metered bill, not that the work costs nothing. [Bring your own
login](#bring-your-own-login) is how that is enforced in code, and
[docs/PRIOR-ART.md](docs/PRIOR-ART.md) is how it compares to its nearest
neighbours — conductor, OMK, open-multi-agent.

<p align="center">
  <img src="assets/live-view.png" alt="Web live view of a real oh-my-graph run: node output feed on the left, DAG map with passed/running/pending nodes on the right, live cost and elapsed time in the header" width="100%" />
</p>
<p align="center"><em>The live view mid-run — a real dogfood run captured live: node output feed on the left, the DAG map on the right, cost and elapsed time in the header.</em></p>

<a id="quickstart"></a>
<a id="example"></a>

## Quickstart

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# Unpack the example graphs that ship inside the binary into ./graphs/:
oh-my-graph init

# Zero config — describe the goal and let auto plan the graph:
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD

# Or run a shipped graph — the cheapest real end-to-end check (a few cents):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

No `ANTHROPIC_API_KEY` needed — this runs on your logged-in `claude`
subscription, and if the key is set in your shell it is deleted from each
node's subprocess environment before that node runs. Add `--plan-only` to
`auto` to buy the plan and read it without executing a node.

You get a live line per node as the graph runs, then a cost ledger:

```text
Run 20260729-101532 — PASS, 2 node(s)
NODE             VERDICT              SESSION                   COST(USD)  DETAIL
---------------------------------------------------------------------------------
critique         PASS (exit-only)     a1b2c3d4-e5f6-47a8-9…        0.0034
write            PASS (verified)      f9e8d7c6-b5a4-4321-8…        0.0091
---------------------------------------------------------------------------------
TOTAL COST: $0.0125
```

Every `PASS` says **how** it was reached, because "the engine ran your build and
it exited 0" and "the model said the word PASS" are not the same claim and must
not print as the same word. `exit-only` above means nothing beyond the exit
status was checked. The closed set of four qualifiers, and what the engine
actually did for each, is in [Reading the
ledger](docs/EXAMPLES.md#reading-the-ledger--what-a-pass-says).

Prefer a prebuilt binary, or want to know exactly what `init` writes and what
it refuses to overwrite? [docs/INSTALL.md](docs/INSTALL.md).

## The graph is a file, not a transcript

The DAG lives in YAML you version, review in a pull request and replay — the
same topology, the same tool ceiling, the same prompts every time. That is the
opposite of an agent improvising a fresh plan on every invocation.

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
    cwd: "{{ inputs.repo }}"
    handoff: session          # e2e resumes dev's session — it already knows what dev did
    prompt: Run make local. Reply with the bare word PASS, or FAIL and what broke.
    success_check:
      exit_zero: true
      result_matches: '^[*_`\s]*PASS[*_`\s]*$'   # what the node said
      verify: { command: "make local" }          # what the engine saw
    retry: { max: 1, on: [nonzero_exit, verify_failed] }

  - id: review
    depends_on: [e2e]
    permission_mode: plan     # read-only
    allowed_tools: [Read, "Bash(git diff*)"]
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

Edges are inline `depends_on` ids — there is no separate edge list — and
parallelism is **emergent**: nodes that share a parent but don't depend on each
other run concurrently, up to a cap. `allowed_tools` is the node's own grant,
not a hint. And failure is a first-class grammar rather than glue code you
maintain: evidence checks, per-cause `retry`, graph-level `on_fail`, bounded
`feedback:` review loops, `type: gate` nodes that stop the run for a human
approval, and a subscription session limit that *pauses* the run so
`resume --retry-failed` can later finish exactly the work that never ran.

Every subcommand, every flag, and a recipe per node field — including `auto` in
depth, its goal cycles, and how it maps planned nodes onto your own Claude Code
agents and skills — are in [docs/EXAMPLES.md](docs/EXAMPLES.md). DESIGN.md is
the authoritative spec.

## Watching it, and what it leaves behind

`run`, `auto` and `resume` serve the live view above on `127.0.0.1` for as long
as the leg lasts and open it in your browser (`--no-web` opts out). `oh-my-graph
serve` with no run id is a **dashboard** of every run at once, one live mini-DAG
card each; `runs list` / `show` / `watch` are the plain-text reads afterward.

Every run persists to `~/.oh-my-graph/runs/<run-id>/` (`OMG_HOME` relocates the
base): a `state.json` snapshot and an append-only `events.jsonl` that any
external consumer can tail. That layout, and how a live run is told from one
whose process died, are a documented, stable contract —
[docs/RUN-FEED.md](docs/RUN-FEED.md); the six run statuses `runs list` prints
are listed with the rest of the command surface in
[docs/EXAMPLES.md](docs/EXAMPLES.md#the-command-surface). Nodes also run with
session persistence **on**, so every node is an ordinary claude session in
`~/.claude/projects` that any transcript reader can pick up.

## It ships itself

Dogfooding here is not a demo: this repository is built by the tool it contains.
Features, fixes, docs and releases are authored by its own graphs — a claude
node implements on a branch, sibling nodes run the checks and the reviews, and a
final node opens the draft PR. The templates in [`graphs/`](graphs/) are not
samples; `self-dev.yaml`, `adr-driven-dev.yaml` and `apply-flags.yaml` are those
pipelines. Don't take that on trust: the commit trailers that make it
countable, the one-line audit command, and its denominator are in
[CONTRIBUTING.md § Attribution](CONTRIBUTING.md#attribution), and a full
dogfooding run is walked through in
[docs/EXAMPLES.md](docs/EXAMPLES.md#dogfooding-developing-oh-my-graph-with-oh-my-graph).

## One boundary to read before you trust it

`auto` executes a plan an LLM wrote, unattended, on your machine. oh-my-graph
bounds what a planned node may call — no `permission_mode: bypassPermissions`,
no planner-authored engine shell, a fixed tool allowlist, and
`--setting-sources ""` so a node declaring `Bash(git *)` cannot borrow a
standing `Bash(*)` from your own settings (measured against a real `claude`, not
read off `--help`). **That is a reduction, not a sandbox.** MCP closure was
never measured, slash-command surface is not enumerable by any of these
mechanisms, and the whole ceiling rests on one CLI version's behaviour. Run
`auto` in a directory you are willing to have modified.

The layer-by-layer stance and every measurement behind it are in
[SECURITY.md](SECURITY.md); the rest of the honest gaps, the platform support
matrix (macOS and Linux supported, WSL first-class, native Windows best-effort)
and what is deliberately deferred are in
[docs/LIMITATIONS.md](docs/LIMITATIONS.md).

## Bring your own login

oh-my-graph never ships credentials, never proxies auth, and never runs as a
shared service. It re-uses **your own** already-logged-in `claude` session — the
same standing as running `claude -p` yourself, or as
[claude-squad](https://github.com/smtg-ai/claude-squad). It is a personal, local
tool. Nodes run inside the Max/Pro plan you already pay for, with no metered key
involved.

To keep that guarantee real, every node subprocess starts from your environment
with `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted** — those silently
switch `claude` to metered API billing. The scrub is one shared policy
(`internal/childenv`), unit-tested on its own and again at each of the four exec
seams; oh-my-graph never uses `--bare` (which disables OAuth) and never touches
the Agent SDK. Full stance: [SECURITY.md](SECURITY.md).

## Where the rest lives

| you want | read |
|---|---|
| every subcommand and flag, walkthroughs, a recipe per node field, `auto` in depth | [docs/EXAMPLES.md](docs/EXAMPLES.md) |
| prebuilt binaries, checksum verification, what `init` unpacks | [docs/INSTALL.md](docs/INSTALL.md) |
| the honest gaps, platform support, what is deferred | [docs/LIMITATIONS.md](docs/LIMITATIONS.md) |
| the run directory, `events.jsonl`, run statuses and liveness | [docs/RUN-FEED.md](docs/RUN-FEED.md) |
| the ToS stance, auto's tool ceiling, what was measured | [SECURITY.md](SECURITY.md) |
| the authoritative spec | [DESIGN.md](DESIGN.md) |
| building, testing, the attribution trailer, the release checklist | [CONTRIBUTING.md](CONTRIBUTING.md) |
| how it compares to conductor, OMK, open-multi-agent | [docs/PRIOR-ART.md](docs/PRIOR-ART.md) |
| driving it from inside a Claude Code session | [plugin/README.md](plugin/README.md) |

See also: [fleetops](https://github.com/jitokim/fleetops), a sibling project
that reads the same `~/.claude/projects` transcripts fleet-wide.

## License

[MIT](LICENSE) © jitokim
