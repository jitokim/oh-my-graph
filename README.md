<p align="center">English | <a href="README.ko.md">한국어</a></p>

<p align="center">
  <img src="assets/icon-round.png" alt="oh-my-graph logo" width="128" />
</p>

<h1 align="center">oh-my-graph</h1>

<p align="center"><em>Each node of a graph you write runs as a real subprocess of your own <code>claude</code> or <code>codex</code> CLI — your settings, your skills.</em></p>

<p align="center">
  <a href="https://github.com/jitokim/oh-my-graph/releases"><img src="https://img.shields.io/github/v/release/jitokim/oh-my-graph?include_prereleases&amp;label=release&amp;color=blue" alt="Latest release" /></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue" alt="MIT license" /></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/go-1.25-00ADD8?logo=go&amp;logoColor=white" alt="Go 1.25" /></a>
  <img src="https://img.shields.io/badge/runtime-Claude%20%7C%20Codex-ff8a65" alt="Claude and Codex runtimes" />
</p>

<p align="center">
  <img src="assets/hero.png" alt="oh-my-graph" width="100%" />
</p>

> Each node of a graph you write runs as a real subprocess of the `claude` or
> `codex` CLI you already have logged in — so it starts inside your own
> settings, CLAUDE.md, MCP servers and skills.

## What it is

You describe the work as a DAG in YAML — or hand `auto` a goal and let it plan
the graph through the same validator — and each node runs as one subprocess of
the CLI login you already use: `claude` by default, `codex` when selected for
the run. Your own logged-in CLI, not a metered API key. **That is not the same
as free.** It spends your plan allowance. [Bring your own
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
# Prebuilt binary, no Go toolchain (exact commands: docs/INSTALL.md) — from https://github.com/jitokim/oh-my-graph/releases take
# oh-my-graph_<version>_<os>_<arch>.tar.gz (darwin/linux × amd64/arm64), verify it against the
# checksums.txt beside it, unpack, put it on your PATH. Or from source, with Go 1.25+ and $(go env GOPATH)/bin on PATH:
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest

# Unpack the example graphs that ship inside the binary into ./graphs/:
oh-my-graph init

# Zero config — describe the goal and let auto plan the graph.
# This goal reads the repo and writes a summary: there is nothing to build,
# and --accept-no-build-evidence states that. In a directory where a build
# system IS detected, `auto` refuses to start without it or --verify-cmd,
# because a planned node cannot run a build and its PASS would be its own
# word for it:
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence

# An implementation goal takes the other exit — the ENGINE runs your build
# command at each sink of the plan and judges its exit code itself:
oh-my-graph auto "fix the failing test" --input repo=$PWD --verify-cmd 'go build ./...'

# Codex is a run-wide opt-in; the global flag must precede the subcommand:
oh-my-graph --runtime codex auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence

# Or run a shipped graph — the cheapest real end-to-end check (a few cents):
mkdir -p /tmp/omg-smoke
oh-my-graph run graphs/haiku-smoke.yaml --input dir=/tmp/omg-smoke
```

Engine-run evidence is established per RUN, not per node: `--verify-cmd`
attaches your command to the sinks of the plan (ADR 0030), and a planned
non-sink node carries no engine-run verify at all — its `PASS` is the
subprocess exit status and its own sentence, by construction and not by
omission. If you want an interior node checked, write your own `verify:` on it
in a hand-written graph and run it with `run` (ADR 0033).

Log in once with the selected CLI (`claude` or `codex login`). No API key is
needed: Anthropic and OpenAI API-key variables are deleted from child process
environments so the CLI uses its saved login. The default remains Claude;
`--runtime codex` applies to the whole run, is saved in `state.json`, and is
reused by `resume` and browser gate actions. Add `--plan-only` to `auto` to buy
the plan and read it without executing a node.

Codex maps `permission_mode: plan` to its read-only sandbox, ordinary modes to
`workspace-write`, and `bypassPermissions` to `danger-full-access`. That sandbox
is also a network boundary, so a Codex graph halts at its first node that
pushes or calls `gh`. Codex does
not report USD or implement Claude's `agent:` selector, so `agent:` and
`--max-goal-budget-usd` are rejected before a Codex run spends anything; the
ledger labels its USD cost `unknown` rather than inventing `$0`. A
node's `budget_usd` is not rejected: with no USD to bound it simply cannot
apply, so the graph loads and one warning per node says so and names the
`timeout:` that still guards it. Claude Code agent mapping and skill
activation are Claude-only; Codex `auto` runs use Codex sandbox isolation
instead.

You get a live line per node as the graph runs, then a cost ledger:

```text
Run 20260729-101532 — 2 node(s)
NODE             VERDICT              SESSION           COST(USD)  DETAIL
-------------------------------------------------------------------------
critique         PASS (exit-only)     a1b2c3d4-e5f6-4…     0.0034
write            PASS (verified)      f9e8d7c6-b5a4-4…     0.0091
-------------------------------------------------------------------------
TOTAL COST: $0.0125
```

Every `PASS` says **how** it was reached, because "the engine ran your build and
it exited 0" and "the model said the word PASS" are not the same claim and must
not print as the same word. `exit-only` above means nothing beyond the exit
status was checked. The closed set of four qualifiers, and what the engine
actually did for each, is in [Reading the
ledger](docs/EXAMPLES.md#reading-the-ledger--what-a-pass-says).

## The graph is a file, not a transcript

The DAG lives in YAML you version, review in a pull request and replay — the
same topology, the same prompts every time. That is the opposite of an agent
improvising a fresh plan on every invocation.

```yaml
name: dev-review-pr
inputs: [repo]
concurrency: 4
nodes:
  - id: dev
    cwd: "{{ inputs.repo }}"
    prompt: Implement the change and summarize what you did.
    allowed_tools: [Read, Edit, Write, "Bash(git *)"]
    permission_mode: dontAsk  # optional; undeclared is `auto`, this asks for stricter

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
approval, and a Claude subscription session limit that *pauses* the run so
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
session persistence **on**. Claude nodes remain ordinary sessions in
`~/.claude/projects`; Codex nodes persist and resume the thread id emitted by
`codex exec --json`.

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
bounds planned execution using the selected runtime's mechanism. Claude uses
the measured layered tool ceiling described below; Codex discards user config,
project rules/AGENTS files and MCP servers for planned nodes, then applies its
read-only or workspace sandbox. **These are reductions, not a security
boundary around the repository you launched from.** Run `auto` only in a
directory you are willing to have modified. Both are scoped to planned nodes:
a hand-written graph you launch with `run` keeps your user config, project
rules/AGENTS files, hooks and MCP servers, and `--accept-loaded-user-config`
states out loud that you want them under `auto` too.

One preference crosses that line by name, and only that one: the `model` key of
your `~/.claude/settings.json` is read on its own and passed to a planned Claude
node as `--model <value>`, so it answers with the model **you** chose instead of
whatever the CLI falls back to when its settings are withheld (measured: 181 of
187 planned nodes ran a model nobody selected —
[ADR 0034](docs/adr/0034-a-planned-node-answers-with-the-model-the-operator-chose.md)).
The node's capability ceiling is unchanged by it: a model name grants no tool,
loads no file and runs no hook. Nothing else in that file is read, and under
`--runtime codex` nothing is.

One thing inside that ceiling changed recently and is worth knowing before you
read the layers. A node that declares no `permission_mode` runs under
`--permission-mode auto`, where it used to run `dontAsk`: a tool call matching
none of the node's allow rules is now put to the CLI's own model classifier,
which approves or denies it, rather than being denied outright. The tool grants
themselves did not move — a declared `Bash(git *)` is still exactly what is
passed, an explicit deny still wins ahead of the classifier, and a tool left out
of the node's set still does not exist. Write `permission_mode: dontAsk` on a
node to get the stricter disposition back. The reasoning, and the measurement
that would reverse it, are in
[ADR 0034](docs/adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md).

The layer-by-layer stance and every measurement behind it are in
[SECURITY.md](SECURITY.md); the rest of the honest gaps, the platform support
matrix (macOS and Linux supported, WSL first-class, native Windows best-effort)
and what is deliberately deferred are in
[docs/LIMITATIONS.md](docs/LIMITATIONS.md).

## Bring your own login

oh-my-graph never ships credentials, never proxies auth, and never runs as a
shared service. It re-uses **your own** saved `claude` or `codex` login. It is a
personal, local tool with the same standing as invoking the selected CLI
yourself.

To keep that guarantee real, every node subprocess starts from your environment
with `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`, and
`CODEX_API_KEY` **deleted** — those can switch the selected CLI away from its
saved login. The scrub is one shared policy
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
