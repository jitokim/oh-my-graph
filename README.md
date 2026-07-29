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
oh-my-graph <run|auto|runs|version> ...
```

- **`run <graph.yaml>`** — you write the DAG in YAML, oh-my-graph executes it.
  The precise-control path: exact prompts, tools, and handoffs per node.
- **`auto "<goal>"`** — you describe a goal in plain language; a coordinator
  plans the DAG for you, then the same engine executes the generated graph.
  The zero-config default.
- **`runs list`** — list past runs from `.oh-my-graph/runs/`, newest first:
  graph name, node count, cost, and overall verdict per run, plus a total.
  Read-only.
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
  - lint [tools: Bash(go *), Read]
  - summarize (after lint) [tools: Read]
  Planned nodes run isolated: none of your user/project/local settings load, so a declared
  scope like Bash(git *) is enforced rather than merely requested — and your CLAUDE.md,
  hooks and MCP servers are unavailable to them. See SECURITY.md for what this does not cover.

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
bypassPermissions`, never set its own `cwd`, never declare a
`success_check.verify` command (that is shell run by the engine, outside every
guard below), never run as one of your subagents (`agent:`), and may only name
tools from a fixed allowlist — the coordinator rejects all of those before
anything runs.

Declaring a narrow tool list is not the same as being held to it, so each
planned node also runs under a layered execution ceiling. The load-bearing part
is `--setting-sources ""`: your own `~/.claude/settings.json` is loaded as
another source of permission *rules*, so a standing `Bash(*)` there used to
match before a planned node's narrower `Bash(git *)` ever mattered. Loading none
of your settings leaves oh-my-graph's own argv as the only allow-rule source,
and under `dontAsk` anything unmatched is denied. On top of that, `--tools`
narrows the node's tool set to what it declared, `--strict-mcp-config` bounds
MCP, and the previous `--disallowedTools` list is kept as a backstop.

**Measured against a real `claude` 2.1.220, not read off `--help`:** with a
settings.json granting `Bash(*)` and a node declaring `Bash(git *)`, an
out-of-scope shell command ran without the isolation flag and was denied with
it, while in-scope `git` kept working. The gap this README used to disclose — a
node declaring a scoped `Bash(...)` pattern keeping the *whole* `Bash` tool — is
**closed for auto-planned nodes.**

Two things that come with it, both real:

- **Planned nodes are now more isolated and less capable.** They no longer see
  your CLAUDE.md, your hooks, or your configured MCP servers. If an `auto` run
  of yours depended on an MCP server, it will stop working.
- **It is still not a sandbox.** MCP closure is unverified (the flag is passed
  because it is free, not because it was measured); skill and slash-command
  surfaces remain unenumerable; and the whole thing is coupled to one CLI
  version's behaviour.

Re-running a saved `graph.json` through `oh-my-graph run` drops the ceiling
entirely — that path assumes you reviewed the file. See
[SECURITY.md](SECURITY.md). Hand-written YAML is unaffected by all of this: it
is your own reviewed artifact, it keeps your settings and hooks and MCP servers,
and it remains the path for precise control.

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

### Running a node as your own subagent (`agent:`)

Add `agent: <name>` to a node and it runs as one of your existing Claude Code
subagents instead of plain `claude -p` — the review node runs as *your*
`code-reviewer`, with its system prompt, its tools and its model:

```yaml
  - id: review
    depends_on: [e2e]
    permission_mode: plan
    agent: code-reviewer      # must exist in ~/.claude/agents or .claude/agents
    prompt: "Review the diff. e2e said: {{ artifacts.e2e | inline }}"
```

The name is resolved by `claude` itself against `~/.claude/agents` and
`<cwd>/.claude/agents`, so there is nothing to register with oh-my-graph and no
copy of your agent definitions to keep in sync.

Two things to know:

- **A name that doesn't resolve fails the node.** It does *not* quietly fall
  back to plain claude — a review node silently running as generic claude would
  produce a plausible-looking review that isn't the one you asked for. The
  failure carries `claude`'s own message, which lists the agents you *do* have.
- **oh-my-graph doesn't reconcile tools, and hasn't measured what does.** If the
  subagent's own `tools:` and the node's `allowed_tools` disagree, the CLI
  decides, and this project makes no claim about how — assume the subagent's
  grant wins. Both files are yours, so this is a usability question; it's why
  `agent:` is rejected on auto-planned nodes, where it would be a safety
  question instead.

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

`success_check` gates a node. It is a conjunction — **every** predicate you
configure must pass — and an empty check means "exit zero is enough".

| predicate | judges | trusts the node? |
|---|---|---|
| `exit_zero` | the subprocess exit code | no |
| `result_matches` | a regex over the node's own result text | **yes — self-report** |
| `verify` | a command **oh-my-graph** runs, by its own exit code and output | no |

`result_matches` is a cheap, useful filter, but a node passes it by *saying*
"PASS" — that is narration, not evidence. `verify` is the predicate that looks
at the world instead: oh-my-graph runs the command itself (through `sh -c`, in
the node's working directory unless told otherwise) and judges the result, so
a node whose success is externally observable can be made to prove it.

```yaml
  - id: e2e
    prompt: Run the suite and report PASS or FAIL.
    success_check:
      exit_zero: true
      result_matches: "PASS"                  # optional, secondary
      verify:
        command: "go test ./... -run TestFoo" # required
        cwd: "{{ inputs.repo }}"              # optional; default = the node's own cwd
        timeout: 2m                           # optional; Go duration, default 2m, max 10m
        expect_exit: 0                        # optional; default 0
        output_matches: "^ok\\s+github"       # optional; regex over stdout+stderr
```

The verification runs **after** the cheap predicates and **before** the node's
output is persisted, so a node that crashed never has a command run against the
wreckage, and a node that failed verification leaves no artifact for its
dependents. `command` and `cwd` interpolate `{{ inputs.* }}` /
`{{ artifacts.* }}` like a prompt. A missing command, an unparseable or
over-ceiling `timeout`, and an uncompilable `output_matches` are all rejected
when the graph loads, naming the node — never mid-run. A verification that
times out or cannot be started **fails** the node; it is never a silent pass.

Auto-planned graphs (`oh-my-graph auto`) may not declare `verify` at all — it is
arbitrary shell that would run outside every guard the coordinator imposes.

`retry` re-runs a failed node up to `max` times when the failure cause is listed
in `on` — always in a fresh session. Retry causes: `nonzero_exit`,
`result_mismatch`, `verify_failed`, `output_error`, `run_error`,
`budget_exceeded`.

### Budgets

`budget_usd` caps what a node may cost. Once the node finishes, its actual cost
is compared against the budget; spending more than it declared fails the node
exactly like a failed `success_check` — the ledger row reads `FAIL` with the
budgeted-vs-actual overage, and by default the run halts so no dependent spends
on top of it. Omit `budget_usd` (or set it to 0) and nothing is enforced.

```yaml
  - id: e2e
    prompt: Run the suite and report PASS or FAIL.
    budget_usd: 0.50
```

`budget_usd` is enforced two ways. **Live:** it is passed to the node as `claude
--max-budget-usd`, so claude aborts the run itself the moment its own spend
crosses the budget — a real mid-run kill, per node. **Post-hoc backstop:** that
abort can only stop the *next* call (one in-flight turn can still overshoot), so
the final cost is re-checked at exit and an over-budget node fails the run. A
post-hoc-overspent node's output is persisted *before* the verdict, so it still
leaves its `.out` artifact to inspect; a live-killed node was interrupted before
a result existed, so it leaves none. Budget failures are **not** retried unless
you explicitly ask (`retry: { on: [budget_exceeded] }`) — retrying an
over-budget node spends that money again, so it is never implicit. Passing nodes
show their remaining headroom in the ledger's `DETAIL` column.

What remains is sub-call and cross-node accounting — see
[Known limitations](#known-limitations).

## Platform support

| platform | status |
|---|---|
| **macOS, Linux** | fully supported |
| **WSL** | fully supported — a WSL build *is* a Linux build |
| **native Windows** | compiles and runs, best-effort — no Windows CI |

macOS and Linux are the supported targets, and CI builds and tests on Linux.
WSL needs no special handling: it is `GOOS=linux`, so it takes the identical
code path — provided the `claude` CLI and `sh` live inside the distro, since
every path and every spawn is WSL-side.

Native Windows compiles and a cancelled node still kills its child, but it is
best-effort. Three things to know before relying on it:

- **`verify` uses each OS's own interpreter.** Build tags select it at compile
  time: `sh -c` on unix (`internal/verify/shell_unix.go`), `cmd /c` on native
  Windows (`shell_windows.go`), each pinned by a build-tagged unit test. What
  still differs is shell *syntax* — `/c` and `-c` share the "run this command
  line and exit" contract, but a `success_check.verify` command written for `sh`
  will not necessarily run unchanged under `cmd`. That portability is the
  graph's to state, not the engine's. CI builds and tests on Linux only; the
  Windows path has never been exercised end-to-end.
- **No tree-kill.** Cancelling or timing out a verification signals the whole
  process group on unix (`internal/verify/procgroup_unix.go`); the Windows
  build (`procgroup_windows.go`) keeps stock `os/exec` behaviour and kills only
  the direct child, so descendants can outlive the run that spawned them.
- **The env scrub is case-sensitive.** Windows treats environment variable names
  as case-insensitive, but [the scrub](#bring-your-own-login) matches keys
  exactly — a lowercase `anthropic_api_key` would survive it and reach the
  child. The guarantee holds as written only where names are case-sensitive.

On Windows, prefer WSL.

## Known limitations

Honest gaps in v0.1, each tracked as an issue rather than left as prose:

- **A `success_check` without `verify` is still self-report.** `success_check.verify`
  closes this for graphs that opt in: the engine runs a command of your choosing
  and judges its exit code and output, independent of anything the node claims.
  But it is opt-in per node — a check that configures only `exit_zero` and
  `result_matches` is exactly as self-reported as it was before, because
  `result_matches` regexes over the node's own claimed result text. Nothing
  forces a node to carry evidence, and for nodes whose work is not externally
  observable (a review, a summary) there is nothing to verify against.
  ([#7](https://github.com/jitokim/oh-my-graph/issues/7))
- **`budget_usd` is enforced per node, but not sub-call or across nodes.**
  A positive budget is passed to claude as `--max-budget-usd`, so claude aborts a
  node the moment its own spend crosses the budget (a real mid-run kill), and the
  final cost is re-checked post-hoc as a backstop — a runaway node no longer
  spends unbounded to the wall-clock timeout. Two gaps remain: claude accounts
  *between* API calls, so the one in-flight call past the threshold can still
  overshoot before the abort lands; and each node's cap is independent — there is
  no whole-graph budget. Closing the first needs incremental cost
  (`--output-format stream-json`), a `NodeRunner`-contract change.
  ([#8](https://github.com/jitokim/oh-my-graph/issues/8))
- **`gate` (human pause/approve) is not implemented.** The node type is
  schema-reserved and rejected at execution time; no `oh-my-graph resume` yet.
  ([#9](https://github.com/jitokim/oh-my-graph/issues/9))
- **Auto mode's tool ceiling is a reduction, not a sandbox — and parts of it are
  unverified.** The isolation and scoped-Bash layers were measured against a
  real `claude` 2.1.220 and hold (see [SECURITY.md](SECURITY.md)). MCP closure
  was **not** measured: `--strict-mcp-config` is passed because it costs
  nothing, not because it was observed to work. Skill and slash-command surfaces
  are not enumerable by any of these mechanisms, and the whole ceiling is
  coupled to one CLI version's behaviour.
  ([#11](https://github.com/jitokim/oh-my-graph/issues/11))
- **`agent:` tool reconciliation is undefined and unmeasured.** When a node
  names a subagent, oh-my-graph does not reconcile that subagent's own `tools:`
  with the node's `allowed_tools` — the CLI decides, and this project makes no
  claim about how. If the subagent grants tools the node did not, assume it
  gets them.

See [Deferred](#deferred-not-in-v01) below for the full out-of-scope list.

## Deferred (not in v0.1)

Called out honestly — these are **not** implemented yet:

- **`gate` / human pause + `oh-my-graph resume`** (v1.1). The `gate` node type is
  schema-reserved so graphs parse, but executing one is rejected with a clear
  "not yet implemented".
- retries beyond a flat `max`; parallel-group sugar / any DSL beyond `depends_on`.
- TUI / dashboard — that is [fleetops](https://github.com/jitokim/fleetops)'s job.
- **sub-call / cross-node budget accounting.** Per-node budget is now enforced
  live (`--max-budget-usd` aborts a node mid-run) *and* post-hoc, so a runaway
  node no longer spends unbounded to the wall-clock timeout. Still deferred:
  catching the single in-flight call that overshoots before the abort lands
  (needs streaming cost via `--output-format stream-json`, a `NodeRunner`
  contract change) and any whole-graph budget across nodes. A wall-clock timeout
  derived from `budget_usd` was deliberately rejected — the $/minute rate would
  be invented, so it would look like a cap without being one.
- worktree auto-creation for parallel edits (parallel v0.1 nodes should be
  read-only reviews).
- **coordinator auto-mapping of `agent:` by role.** Having `auto` scan your
  `~/.claude/agents` and assign a reviewer node your `code-reviewer` sounds like
  the natural next step; it is deferred on a design constraint, not on effort.
  A planned node may not carry `agent:` at all (it would route around the tool
  ceiling), and settings-source isolation disables agent discovery anyway, so
  the two features are mutually exclusive as built. An implicit scan is also
  rejected permanently: it would make an `auto` run's behaviour depend on files
  you forgot you had. See `docs/adr/0004-*.md` §4.

## Prior art

- **[microsoft/conductor](https://github.com/microsoft/conductor)** — same
  philosophy: the graph is declared in YAML and the LLM is kept out of the
  orchestration loop, only executing nodes. It doesn't drive the subscription
  `claude` CLI specifically — that's oh-my-graph's differentiator.
- **[OMK](https://github.com/dmae97/omk)** — the closest sibling. Runs coding
  agents (including Claude Code) in scoped DAG lanes, and verifies node
  success against external evidence rather than the node's own self-report.
  oh-my-graph now has an evidence predicate of its own — `success_check.verify`
  runs a command the engine judges — but it is opt-in per node rather than
  intrinsic to the model, so a graph that doesn't declare one is still passing
  nodes on their own word ([#7](https://github.com/jitokim/oh-my-graph/issues/7)).
- **[open-multi-agent](https://github.com/open-multi-agent/open-multi-agent)**
  — the opposite axis: a coordinator plans the task DAG at runtime from a goal
  description, instead of a human-authored graph. Static, declared graphs
  (oh-my-graph, conductor) win on reproducibility and auditability — the graph
  is a diffable, git-reviewable artifact; runtime-planned graphs win on
  adapting to goals whose procedure isn't known in advance. oh-my-graph's
  unreleased `auto` mode partially borrows the latter idea (goal → LLM-planned
  graph) but still runs the result through the same deterministic scheduler —
  a hybrid, not a full runtime-replanning system.

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
