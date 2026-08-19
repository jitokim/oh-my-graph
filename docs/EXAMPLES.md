# Examples

The [README's Quickstart](../README.md#quickstart) walks through the first run
— the cheapest real smoke test. This file is the rest of the manual: the
command surface, how to read a ledger, the remaining walkthroughs, then
per-feature recipes for the graph model's optional fields.

Reference:

- [The command surface](#the-command-surface) — every subcommand and the
  flags `run`/`auto` share.
- [What `--runtime codex` changes](#what---runtime-codex-changes) — the one
  place this file is not about the default runtime.
- [Reading the ledger](#reading-the-ledger--what-a-pass-says) — what a `PASS`
  actually says it did.

Walkthroughs, in order:

1. [Zero-config: auto mode](#zero-config-auto-mode-the-headline) — the
   headline feature, and [`auto` in depth](#auto-in-depth--goal-cycles-agents-skills).
2. [Dogfooding](#dogfooding-developing-oh-my-graph-with-oh-my-graph) — using
   oh-my-graph to develop oh-my-graph.
3. [Watch a run](#watch-a-run) — the live view, the dashboard, the text tail,
   and the ledger after the fact.
4. [Ambient chat](#ambient-chat-prototype) — talk; each turn routes to a
   reply or a graph (prototype).

## The command surface

```text
oh-my-graph [--runtime claude|codex] <init|run|auto|lint|chat|resume|runs|show|watch|serve|version> ...
```

`--runtime` is a global, run-wide selector and must appear before the
subcommand — `oh-my-graph run g.yaml --runtime codex` is a hard error (`flag
provided but not defined: -runtime`), not a second spelling. It defaults to
`claude`. It applies to `run`, `auto`, `lint`,
`chat`, `resume`, and `serve`; read-only history commands need no selector.
Every fresh run persists the choice. `resume` and a live view's gate buttons
read that persisted runtime; passing a different explicit value is an error.

| subcommand | purpose |
|---|---|
| `init [dir]` | Write the example graphs embedded in the binary to `<dir>/graphs/` (`dir` defaults to `.`), including the `fragments/` subdirectory the templates cite with `use:`, listing each file as `wrote` or `kept`. Never overwrites — see [docs/INSTALL.md](INSTALL.md#what-oh-my-graph-init-unpacks). |
| `run <graph.yaml>` | Execute a hand-written DAG — the precise-control path. `--dry-run` validates, resolves `--input` interpolation, prints the plan, runs nothing. |
| `auto "<goal>"` | Plan a DAG from a plain-language goal, then execute it with the same engine — the zero-config default. `--plan-only` prints the plan, its agent mappings, its staged skill corpus and the tool ceiling, then stops without running a node (it still pays for at least one planner call, and a validation refusal buys one corrected call on top of it — unlike `run --dry-run`, it is not free). `--max-cycles N` iterates plan→run→assess up to N times — a validation-refused plan buys one corrected planner call, so the planner-call worst case is `2 × N` (`--max-goal-budget-usd` adds a soft spend ceiling between cycles; requires `--max-cycles` of 2 or more). `--verify-cmd 'CMD'` attaches your own build command to the plan's sink nodes for the ENGINE to run and judge, so a check node cannot certify a branch that does not build; `--verify-timeout D` bounds one execution (default and ceiling 10m). A run started with `--verify-cmd` must re-supply it on every `resume`. It is **not optional in a build-bearing directory**: where a build system is detected and no `--verify-cmd` is given, `auto` refuses to start (exit 3, before any spend) unless `--accept-no-build-evidence` states that this run carries none — which is then recorded in the run's `state.json` and printed with the plan (ADR 0030). Where no build signal is detected, neither flag is required. |
| `lint <graph.yaml>` | Statically validate a graph file, reporting every problem at once. Read-only, zero cost. |
| `chat` | Interactive REPL (prototype): conversational turns are answered, task-shaped turns are planned into a graph and run. |
| `resume <run-id> ((--approve \| --reject) <gate-id> \| --retry-failed)` | Resume a run: decide the gate it is paused at, or `--retry-failed` to salvage a failed run — passed nodes' results are kept and only the failed and cancelled nodes re-execute. Takes `--concurrency N` and `--no-web`. An auto run started with `--verify-cmd 'CMD'` takes it here too (with `--verify-timeout D`): the resumed leg's build evidence comes from you, never from the run directory, and a resume without it is refused. |
| `runs list` | List runs, newest first: graph name, node count, cost, status (`PLANNING`, `RUNNING`, `PASS`, `FAIL`, `PAUSED`, `ABANDONED`), plus a total. Read-only. |
| `show <run-id>` | Print one run's status and its per-node ledger (session, cost, verdict, duration) with the total. Read-only. |
| `watch <run-id>` | Tail a run's event stream as plain text, `tail -f` style. Read-only. |
| `serve [<run-id>]` | Web live view, bound to `127.0.0.1` only (default port 8642, `--port` to change). With **no run id** it is a dashboard: one live mini-DAG card per run, each card opening that run's view at `/run/<id>/`. With a run id it goes straight to that run. Opens in your browser when stdout is a terminal; `--no-open`, a pipe, or CI prints the URL and serves it without opening anything. Read-only except for one thing: a run paused at a gate can be approved or rejected from the page. |
| `version` | Print the tool version. |

`run` and `auto` share `--input k=v` (repeatable), `--concurrency N` (ceiling
10), `--continue-on-fail`, and `--no-web` (do not serve or open the web live
view for this run). Both print a live per-node feed as the graph executes, then
a cost ledger. A graph can also declare the failure policy itself with
graph-level `on_fail: continue` (default `halt`) — the right setting for a batch
of independent lanes, where one lane's failure should not cancel the others'
in-flight work. The flag ORs with the field: either saying continue means
continue.

**What those six statuses mean, and how a live run is told from one whose
process died**, is the run feed's own rule — one shared derivation behind `runs
list`, the dashboard, the single-run view and `watch`, so they cannot disagree.
It is documented for external consumers too:
[docs/RUN-FEED.md § Liveness](RUN-FEED.md#liveness--resumelock-adr-0015), with
the decision in
[ADR 0015](adr/0015-an-abandoned-run-is-derived-from-the-lock-not-repaired-into-the-feed.md).
Three of them are worth stating here. An `auto` run appears as **`PLANNING`**
from the moment it starts thinking — its run id is minted before the planner
call, because a run id names an oh-my-graph execution, not the moment a graph
starts executing — so the longest single wait in the tool is visible in `runs
list` and on the dashboard instead of being silence (#163). A run stopped at an
approval gate or on your subscription's session limit reads **`PAUSED`** and
exits 2, where every read-back surface used to render it `FAIL`. And a run whose
process was killed reads **`ABANDONED`** rather than `RUNNING` forever — which
is deliberately not `FAIL`, because the work never got a verdict. Note the
distinction on the planning leg: a command that *dies* mid-planner-call reads
`ABANDONED` like any other dead leg, while a planner that returns an error
normally closes its leg with a verdict and the run reads `FAIL`.

`ABANDONED` carries a warning
worth reading before you act on it — the engine spawns each model CLI in its own
process group, so the death that abandoned the run may have left a subprocess
still running and still spending. Check before you resume, or you pay for the
same node twice.

### What `--runtime codex` changes

Everything else in this file describes the default, Claude. Under Codex:

- **The sandbox replaces the tool grants.** `permission_mode: plan` maps to
  `read-only`, ordinary modes to `workspace-write`, `bypassPermissions` to
  `danger-full-access`. A node's `allowed_tools` list is **not** translated into
  Codex permissions — it stays graph documentation, and the run says so before
  it starts. Everything `lint` advises below about tool grants and tool denials
  is therefore about the Claude runtime.
- **No network.** The sandbox blocks it, so `gh`, `git push` and `git ls-remote`
  fail and a graph halts at its **first** publishing node, wherever that sits.
  The measurements, the shipped graphs at each of the three positions, and the
  per-node remedies are in [LIMITATIONS](LIMITATIONS.md#known-limitations).
- **No USD, anywhere.** Codex counts tokens and never reports dollars, so every
  node that *invokes* it has a `COST(USD)` cell reading `unknown`, the run prints
  `TOTAL COST: unknown` above a `TOKEN USAGE:` line, and there is no spend to
  compare a budget against. (A row for a node that spawned nothing — an approved
  gate, a node that died before its subprocess started — still reads `0.0000`,
  which is what it cost.)
- **Two declarations are refused before anything spends:** `agent:` and
  `auto --max-goal-budget-usd`. A node's `budget_usd` is the third case and it
  is not refused ([ADR 0026](adr/0026-an-inapplicable-cap-is-not-an-unsafe-one.md)):
  without USD there is nothing to bound, so the graph loads and one warning per
  budgeted node says the cap cannot apply and names the `timeout:` still
  guarding that node. The goal ceiling is refused because it is checked only at
  a cycle boundary — accepting an unmeasurable one buys a whole cycle to learn
  that — not because it is a dollar figure; `--max-cycles` bounds the loop
  either way. Claude agent mapping
  and staged skill activation are not refused but not attempted — a planned
  Codex run prints one line saying so.
- **A session limit is an ordinary failure**, not the resumable pause of
  [ADR 0009](adr/0009-a-session-limit-is-a-pause-not-a-failure.md). The detection
  is gated on the runtime, not on wording, so a Codex limit can never be read as
  a pause; `resume --retry-failed` still salvages it
  (scoped to the Claude runtime by
  [ADR 0009](adr/0009-a-session-limit-is-a-pause-not-a-failure.md), so no
  runtime owes one).
- **The live view shows no per-node transcript tail.** Node states, verdicts and
  the settled per-node result render as they do for a Claude run, with the cost
  figure carrying `unknown` as above — see [Watch a run](#watch-a-run).

**Which shipped graphs run.** Refusal is at load, so it costs nothing to ask:
`oh-my-graph --runtime codex lint <graph>`. Run against `graphs/`, all eight
lint clean under Claude and **one is refused under Codex** — `adr-driven-dev`,
for the `agent:` on its three review nodes. Four of the seven that load warn
that a `budget_usd` cannot apply: `review-loop` (its own), and `dev-review-pr`,
`self-dev` and `backlog-batch` (inherited from the `e2e-verify` fragment).
Before ADR 0026 those four were refused as well, which is what made five of
eight unloadable. Of the seven that load, `apply-flags`, `merge-shepherd`,
`dev-review-pr`, `self-dev` and `backlog-batch` all publish, so they hit the
network wall — leaving `haiku-smoke` and `review-loop` as the shipped graphs
with no node that needs the network. The expected verdict for every shipped
graph under both runtimes is asserted in
`internal/runner/shipped_graphs_runtime_test.go`, so a graph that stops loading
under Codex fails `make test` by name.

### What `lint` checks

`lint` checks structure — DAG/cycle, unknown `depends_on` ids, the
session-handoff parent rule, verify blocks — and exits 0 when valid, 1 when
not. On a valid graph it also prints advisory `warning:` lines to stderr for
placeholder-like `{{ ... }}` tokens that won't resolve — a typoed filter
(`| inlin`), a singular `{{ artifact.x }}`, an undeclared input, or an
`artifacts.<id>` naming a node that doesn't exist or isn't an ancestor —
plus, for a `handoff: session` node, a `cwd`/`worktree` that differs from
its session-parent's, or a `retry` block (a retried attempt starts cold) —
plus, for any node, a verdict nothing reads: a prompt that demands a token
(`START your reply with …`) under a `success_check` with no `result_matches`,
or a `result_matches` written without `exit_zero` (which drops the exit-code
guard a node has for free only while it declares no check at all) —
plus, for a node that declares neither `allowed_tools` nor a
`success_check.verify`, that nothing it does can observe a tool denial: a tool
your Claude Code settings do not pre-authorise is refused, the node says so in
prose, and its own `result_matches` is free to pass on that prose. Name the
tools the node needs (`allowed_tools: []` if it needs none), or give it a
`verify` command the engine runs itself. (A gate node and a
`permission_mode: bypassPermissions` node are exempt: neither can be denied
anything.)

Last, it warns for a `success_check.verify.command` that splices a model's own
text into the shell command line the engine runs. A verify command interpolates
exactly like a prompt, so `{{ artifacts.<id> | inline }}` there puts a node's
free-form reply on that line — and `{{ feedback.<id> }}`, which always inlines
and takes no filter, does the same. Neither is malformed and nothing else says
a word. The fix for the first is usually the default: with no filter the token
is the artifact's *file path*, so write
`grep -q '^PASS' "{{ artifacts.impl }}"` and let the command read the file.

Warnings never change the exit code. At run time, malformed tokens pass
through verbatim (a prompt may legitimately contain literal `{{ }}` text),
while a well-formed reference to an unbound input or unknown node fails
its node when interpolation runs. `run --dry-run` shares that exit contract and
the same warnings, and additionally proves `{{ inputs.* }}` resolution against
your actual `--input` values.

## Reading the ledger — what a `PASS` says

Every `PASS` row carries a qualifier, because "the engine ran your build and it
exited 0" and "the model said the word PASS" are not the same claim and must not
print as the same word:

```text
critique         PASS (exit-only)     a1b2c3d4-e5f6-47a8-9…        0.0034
write            PASS (verified)      f9e8d7c6-b5a4-4321-8…        0.0091
```

`write` declares a `success_check.verify`, so its row is `verified`; `critique`
declares only `exit_zero`, so nothing beyond the process's exit status was
checked and its row says so. The four qualifiers are a closed set:

| qualifier | what the engine actually did |
|---|---|
| `verified` | ran a `success_check.verify` command and judged its exit code (and `output_matches`, when declared) |
| `self-reported` | matched a `result_matches` pattern against what the node *said* — no state outside the model's narration was observed |
| `exit-only` | the subprocess exited 0, and no predicate beyond that was declared |
| `approved` | a human approved a `type: gate` node — no subprocess, no predicate |

`verified` means *measured*, not *correct*: `verify: { command: "true" }` yields
it. The ledger reports how a verdict was reached, never whether the check was a
good one. A `FAIL` carries no qualifier — it states its cause in `DETAIL`
instead. The same closed set is the `provenance` field on a `node_passed` event,
so an external consumer reads exactly what the terminal prints
([docs/RUN-FEED.md](RUN-FEED.md#event-types-and-their-extra-fields)). The
qualifier is the engine's own answer and does not move with the runtime; the
money column does ([What `--runtime codex`
changes](#what---runtime-codex-changes)).

**Which qualifier a node can earn depends on the path.** A hand-written graph
earns `verified` by declaring `success_check.verify` — your own reviewed
artifact, your own command. A **planned** node cannot: a planner-authored
`verify:` is engine-run shell outside every ceiling layer, so it is refused
outright, which is why `auto`'s check nodes have only ever been able to reach
`self-reported`. That is the gap
[ADR 0016](adr/0016-build-evidence-is-a-user-supplied-engine-command.md)
closes — a build command **you** supply at invocation, attached by trusted code
to the plan's sink nodes *after* validation and run by the engine, so a
verification node cannot pass a branch that does not build:

```sh
oh-my-graph auto "fix the failing spec" --verify-cmd './gradlew build'
```

The engine runs that at every sink node, one at a time, after the node's own
subprocess — and a sink that fails it fails the run. Your nodes are granted
nothing by it: the command is yours, the engine runs it on its own verify seam,
and it judges the exit code itself. `--verify-timeout` bounds one execution (10
minutes by default, which is also the ceiling — not the 2-minute default a
hand-written check gets, because a cold Gradle or Cargo build is exactly what
that default was not sized for). A plain program invocation that cannot run is
refused **before** the planner call, so a typo costs nothing; a command carrying
shell syntax (a pipe, an `&&`, a substitution) skips that check rather than have
the pre-flight re-implement the shell. `--plan-only` prints the command and the
sink nodes it will run at, and every cycle of a `--max-cycles` goal loop plans a
new graph that carries it. With no `--verify-cmd` in a directory where a build
system **is** detected, `auto` no longer merely prints what it is not checking —
since [ADR 0030](adr/0030-an-unverified-auto-run-is-a-choice-not-a-default.md)
it **refuses to start** (exit 3, before the planner call, so nothing is spent),
naming the marker it found and a suggested command. The other exit is
`--accept-no-build-evidence`, which states that this run carries no build
evidence and is recorded in the run's `state.json` and printed with the plan, so
a later reader learns the absence was a choice. A directory with no build signal
is not gated at all and prints exactly the notice it always did. One thing worth
knowing up front: `resume` takes no verification from a
run directory, so a run started with `--verify-cmd` needs the command **supplied
again on every resumed leg** — `oh-my-graph resume <run-id> --retry-failed
--verify-cmd './gradlew build'`, which is what the pause hint prints for you. A
resume without it is refused rather than run with weaker checking than the leg
it continues. [SECURITY.md](../SECURITY.md) has the standing such a command has.

## Zero-config: auto mode (the headline)

Don't want to write YAML? Give `auto` a goal in plain language and a
coordinator plans the DAG for you — one planner call (through the same
subscription-auth, env-scrubbed runner every node uses) turns the goal into a
graph spec, which is validated and executed by the same engine as a
hand-written graph:

```sh
oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence
```

This goal reads the repo and writes a summary — there is nothing to build, and
the flag says so. Drop it and `auto` refuses to start in any directory where it
detects a build system (ADR 0030); an implementation goal takes the other exit,
`--verify-cmd 'CMD'`, and has the engine check the result itself.

What you'll see — a plan, then the same live feed and ledger as any other run
(the planner is non-deterministic, so expect this shape rather than these
exact node names):

```
Planning a graph for goal "lint this repo and summarize the findings"...
Planned graph "lint-and-summarize" (2 nodes, planning cost $0.0021, saved to ~/.oh-my-graph/runs/20260729-101600/graph.json):
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

The generated spec is saved to `~/.oh-my-graph/runs/<run-id>/graph.json` —
since JSON is valid YAML, you can hand-edit it and re-run it directly with
`oh-my-graph run`. A planned node can never opt into `permission_mode:
bypassPermissions`, never set its own `cwd`, never declare a
`success_check.verify` command (that is shell run by the engine, outside every
guard below), never run as one of your subagents (`agent:`), and may only name
tools from a fixed allowlist — the coordinator rejects all of those before
anything runs.

Declaring a narrow tool list is not the same as being held to it, so each
planned node also runs under a layered execution ceiling. **The ceiling below is
the Claude runtime's**; a planned Codex node is bounded by its filesystem
sandbox instead, with user config, project rules/AGENTS files and MCP servers
dropped ([SECURITY.md](../SECURITY.md#codex-planned-node-isolation)). The
load-bearing part of the Claude ceiling is `--setting-sources ""`: your own `~/.claude/settings.json` is loaded as
another source of permission *rules*, so a standing `Bash(*)` there used to
match before a planned node's narrower `Bash(git *)` ever mattered. Loading none
of your settings leaves oh-my-graph's own argv as the only allow-rule source,
and under `dontAsk` anything unmatched is denied. On top of that, `--tools`
narrows the node's tool set to what it declared, `--strict-mcp-config` bounds
MCP, and the previous `--disallowedTools` list is kept as a backstop.

**Measured against a real `claude` 2.1.220, not read off `--help`:** with a
settings.json granting `Bash(*)` and a node declaring `Bash(git *)`, an
out-of-scope shell command ran without the isolation flag and was denied with
it, while in-scope `git` kept working. The gap this project used to disclose — a
node declaring a scoped `Bash(...)` pattern keeping the *whole* `Bash` tool — is
**closed for auto-planned nodes.**

Two things that come with it, both real:

- **Planned nodes are now more isolated and less capable.** They no longer see
  your CLAUDE.md, your hooks, or your configured MCP servers. If an `auto` run
  of yours depended on an MCP server, it will stop working.
- **It is still not a sandbox.** MCP closure is unverified (the flag is passed
  because it is free, not because it was measured); which skill a node actually
  activates is not knowable before the model chooses it, and slash-command
  surfaces remain unenumerable; and the whole thing is coupled to one CLI
  version's behaviour.

Re-running a saved `graph.json` through `oh-my-graph run` drops the ceiling
entirely — that path assumes you reviewed the file. See
[SECURITY.md](../SECURITY.md). Hand-written YAML is unaffected by all of this:
it is your own reviewed artifact, it keeps your settings and hooks and MCP
servers, and it remains the path for precise control. Inheriting your settings
cuts both ways, which is why `allowed_tools` is the node's grant rather than a
hint: a hand-written node that omits it can use only what you have already
pre-authorised, and a tool it lacks is refused in prose the node then finishes
on — a `result_matches: '^DONE'` will pass on that prose. Declare each node's
tools (an empty `allowed_tools: []` declares one that needs none), and put
anything that must be true outside the reply in a `success_check.verify`
command, which the engine runs itself.

**Custom YAML vs. auto, in one line:** reach for `graphs/*.yaml` when you know
exactly which tools each node should have and how they should hand off to
each other; reach for `auto` when you'd rather describe the outcome and let
the planner design the DAG.

### `auto` in depth — goal cycles, agents, skills

Goal cycles (minus `--max-goal-budget-usd`), the plan preview and the one-repair
bound run on either runtime. **Agent mapping and skill activation do not: they
are Claude Code mechanisms, and a Codex run attempts neither** — it prints one
line saying so, and each planned node's sandbox policy instead.

**Goal cycles.** Want `auto` to keep going until the goal is actually met?
`--max-cycles N` (default 1) turns one invocation into a bounded loop of up to N
whole plan→run→assess cycles: after each run, a tool-stripped assessor judges
the goal against the run's own recorded evidence, and if work remains, the next
cycle replans around it — every cycle re-validated under the same tool ceiling,
every plan and verdict printed as it happens, and a goal summary totalling each
cycle's spend at the end. Exit 0 requires both a goal-met verdict and a passed
final run. `--max-goal-budget-usd X` adds an optional soft spend ceiling checked
between cycles; it requires `--max-cycles` of at least 2, since a single-cycle
run has no cycle boundary to check it at, and the flag is rejected at parse
otherwise. Stated honestly: `auto` is non-interactive, so an unattended
`--max-cycles 5` may spend **up to ten** planner calls — a validation refusal
buys one corrected plan, so each cycle's planner-call worst case is 2, and
`--max-cycles` itself has no upper bound — five graphs and five assessments with
nobody watching. The governance is the bound you typed, the per-cycle
validation, and the printed record, not a confirmation prompt
([ADR 0011](adr/0011-plan-and-execute-is-a-bounded-cycle-of-whole-runs.md)).

**Reading the plan first.** `auto --plan-only` plans, prints the graph with
every agent mapping, the staged skill corpus and the tool ceiling, and stops —
no node is executed. It is the `auto` counterpart to `run --dry-run` with one
honest difference: a dry run reads a file you already wrote and costs nothing,
while there is no plan to inspect until one has been bought, so `--plan-only`
still pays for the planner call and prints what it cost. The plan it paid for is
kept — under `~/.oh-my-graph/plans/<id>/graph.json`, not in `runs/`, because
nothing ran: a preview is not a run, so `runs list` and `serve` never see it.
Run it later with `oh-my-graph run <that path>`. It previews one cycle by
definition — `--plan-only` with `--max-cycles` above 1 is rejected at parse,
since every cycle after the first is planned from the previous cycle's run and
so does not exist yet to be shown.

**One planner call is the normal case, and two is the bound.** If the planner's
reply describes a graph the validator refuses, oh-my-graph hands those exact
refusals back and buys **one** corrected attempt, held to the identical ceiling
— no auto-editing of what the model wrote, and no third try. The printed price
is the sum of both calls and the re-plan is disclosed on its own line, with the
refusals that caused it. If the corrected reply is refused too, the rejected
spec is still kept — as `rejected.json`, in the run's own directory for `auto`
(the run id exists before the planner call, and such a run reads `FAIL`) and
under `~/.oh-my-graph/plans/<id>/` for `--plan-only`, which never mints one — so
a paid-for plan is never destroyed by being invalid
([docs/RUN-FEED.md](RUN-FEED.md)).

**Agent mapping.** If you have your own Claude Code agents (`~/.claude/agents` —
**your own directory only**, never the repository's `./.claude/agents`), `auto`
also maps planned nodes onto them when a node's id clearly matches an agent's
name — your review node runs as *your* `code-reviewer`. The match is
deliberately conservative (one clear candidate or nothing, and an agent wanting
tools beyond the node's planned allowlist is skipped with a note), every mapping
is shown in the printed plan before anything runs, and `--no-agent-mapping`
turns it off.

The project directory was scanned until 2026-08-12 (KST), and the reason it stopped is
worth a sentence. A matched definition is now *copied* into the run and handed to
the node as its system prompt, so scanning the repository under work meant a file
that arrives with a `git clone` could write the instructions an unattended node
runs under. Measured: it did, 2 of 2
([the record](measurements/0022-repo-planted-agent-and-the-agents-only-dir.md)).
Move an agent you want mapped into `~/.claude/agents`; the plan printout names
the file every staged definition came from, with its size and hash.

**The trade, stated up front and measured rather than described.** A mapped node
runs as settings-isolated as any other planned node: oh-my-graph copies that
agent's definition into the run's own directory and hands the node that copy, so
`--agent` resolves without your settings loading. Its declared scope binds — a
node declaring `Bash(git *)` cannot run a non-git command even if your settings
would allow one, measured on 2026-08-12 (KST) against the argv this build emits,
staged directory and all: denied 3 of 3, against a breach for the argv shipped
through v0.6.0 on the same machine the same hour, with an in-scope control still
running 2 of 2
([the record](measurements/0022-repo-planted-agent-and-the-agents-only-dir.md),
following [the one that made the change](measurements/0017-staged-agent-restores-layer-1.md)).
What it costs the node is real and is the other half of the same sentence — your
standing grants are unavailable to it, your `CLAUDE.md` and your hooks arrive by
the same source list and are implied rather than measured, and it holds no
`Skill` tool, so it can invoke no skill at all. (Its argv also carries
`--strict-mcp-config`, as every planned node's always has; whether that closes
MCP is unmeasured, so read it as a flag rather than a result.) **Through v0.6.0
a mapped node was the one exception to that isolation; from 2026-08-12 (KST) it is
not.** The agent file is read once, at plan time, and pinned by hash, so editing
it mid-run changes nothing; a resumed leg maps nothing at all and says so. If
you want one node's `Skill` tool back, `--no-agent <name>` declines that one
agent — the node then runs as an ordinary planned node, which is the whole of
what declining buys: it does **not** hand the node your environment back,
because no planned node gets that any more. `--no-agent-mapping` remains the
all-or-nothing form
([ADR 0022](adr/0022-a-mapped-node-gets-its-agent-staged-not-its-settings-back.md)).

**Skill activation.** Your Claude Code skills (`~/.claude/skills` only) reach
`auto` runs too, and they reach them through Claude Code's own activation rather
than through a guess made for the node: `auto` copies your whole skill corpus
into a plugin directory it owns
(`~/.oh-my-graph/runs/<run-id>/skills-plugin/`), passes each
**activation-eligible** planned node `--plugin-dir <that>` and adds `Skill` to
its tool list, so the node's own model *can* pick the skill its task calls for,
by description, at run time. Eligible means a planned node that is not
agent-mapped, on a run where activation is on at all — an empty or missing
`~/.claude/skills`, or a staging failure, turns it off for the whole run and
says so on its own line.

An **agent-mapped node is excluded** and gets neither half. That exclusion was
measured on 2026-08-12 (KST) and kept, because a mapped node then loaded your settings
and a skill name resolved against definitions the repository you are working in
can write — and it *still stands*, though the ground under it is gone: since ADR
0022 such a node loads no settings and those definitions no longer reach it
(the same repository copy fired **0 of 3**, and where the model did call `Skill`
the CLI answered `Unknown skill: …` —
[the record](measurements/0017-staged-agent-restores-layer-1.md)). Nobody has
re-taken the decision, and until someone does, an excluded node holds no `Skill`
tool.

**What that exclusion costs is not small, and the plan printout says so.** An
excluded node invokes **no skill at all** — not the staged corpus, and not your
own installed skills. Measured 2026-08-09 (KST) on 10 real spawns: told outright to
use a skill it fired 0 of 3 under the argv oh-my-graph really sends, and 3 of 3
with `Skill` added to that argv's `--tools` and nothing else changed — and 0 of
1 against 1 of 1 when the skill sat in `~/.claude/skills` rather than in the
project
([the record](measurements/0017-agent-mapped-nodes-cannot-invoke-a-skill.md)).
And the exclusion is not spread evenly: agent mapping runs first and matches on
the same signal, so it takes the design, doc and review nodes — the ones a
procedure fits best. If you would rather those nodes kept the skill surface than
gained a subagent, `--no-agent-mapping` turns agent mapping off for the whole
run, and `--no-agent <name>` declines a single agent so the price is the nodes
that one agent would have taken rather than every mapping in the plan.

Lifting the exclusion was measured on 2026-08-12 (KST) and **refused** — 21 spawns,
$4.16, pre-registered in its own commit
([the record](measurements/0017-lifting-the-agent-mapped-exclusion.md)). Not
because adding the tool fails: it works, 3 of 3. It was refused because on those
nodes, as they then ran, a skill name resolved against a corpus **the repository
under work can write**: a same-named `SKILL.md` committed to the target repo beat
oh-my-graph's own staged corpus 3 of 3. Under `--setting-sources ""` — every
non-mapped node — the same three-way collision resolved to the staged copy 3 of
3, so this was agent mapping's exposure and not activation's. ADR 0022 has since
closed that exposure without lifting the exclusion; the exclusion stands because
nothing has re-decided it, and the printout says so in those words rather than
implying the refusal still has a reason behind it.

**Whether skills get used is now measured; whether the result is worth the
tokens is not, and the feature is on by default.** v0.5.1 shipped this recording
**1 skill invocation across 7 activated planned nodes**, cause unknown. 44 real
spawns against the exact argv an activated node receives say why: under the
planner's own prompt, verbatim, a node reached for a skill **0 times in 9** —
not because nothing fit, but because the gate is a threshold on how directly a
description's trigger language matches the task, applied without deliberation.
So `auto` now appends one fixed sentence to every **activated** node's prompt,
naming no skill and no directory: *"A corpus of procedures is available through
the Skill tool; consult it if one fits this task."* The same prompt bytes with
that sentence fired **8 of 9**, and all 8 chose the same real skill of the
user's own corpus. It is prompt text and not a grant, and it is deliberately
**not** written into a saved `graph.json` — that artifact re-runs through `run`,
which has no staged corpus to promise.

That number is a probe, and it is not a claim that the work got better. On the
one task where the deliverable could be checked mechanically the two arms were
indistinguishable, while the arm with the sentence cost **$0.205 a spawn against
$0.134**; and a node whose prompt is an output contract (a verification node's
`PASS`/`FAIL`) does not activate with it or without it. ADR 0017 is `Proposed`
for that reason, and the numbers are printed with the price before every run. If
you would rather not pay a per-invocation token tax for a capability whose value
is still unmeasured, `--no-skill-activation` is the switch
(`--no-skill-mapping` is the deprecated alias, and still works with a printed
notice).

**The tool ceiling does not move for any of it.** Planned nodes load none of
your settings (measured; your CLAUDE.md and hooks arrive by that same source
list, so their absence is implied rather than measured), they run under
`--strict-mcp-config` (whether that closes MCP is not something anyone has
measured), and a declared scope like `Bash(git *)` is enforced — the only change
activation makes is that the `Skill` tool exists for the nodes it reaches. What
that costs is printed before the run: every staged skill with its size and
SHA-256, and the prompt tokens the corpus adds to **every** activation-eligible
node invocation of that leg, including retries and feedback re-runs. What the
plan can no longer tell you is *which* skill a node will use — nothing knows
that before the model does. The printout says so, and each invocation is
recorded in that node's ordinary session transcript. The staged directory is
re-created and verified from a manifest before every node spawn of the leg that
staged it, so a node cannot leave a skill behind for a later one; your own
`~/.claude/skills` tree is read once, when the corpus is staged.

**A resumed leg never activates skills.** Only the first leg of a run does. A
resumed leg is a fresh process with no in-memory manifest, so the only thing it
could re-stage from is the record inside the run directory — which the previous
leg's own nodes could have rewritten, since they run as you and `Write` is
unscoped. Until there is somewhere outside the run directory to anchor that
record, `resume` withholds the `Skill` tool and the staged directory instead of
trusting one, and prints one line saying so
([ADR 0017 §6](adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md)).

Two places a skill can live are **out of scope** and are not staged: skills
provided by a **plugin** (`~/.claude/plugins/...`) and **project** skills
(`./.claude/skills`). Both are stated limits, not failures, so the plan printout
says so on every run — `skill scan: 35 skill(s) from /home/you/.claude/skills`
followed by the not-scanned note — and a scan that finds nothing still names the
directory it looked in, so "I have skills but `auto` sees none" is one line to
diagnose rather than a guess. See
[ADR 0017](adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md)
for the measurements behind all of this, and
[ADR 0012](adr/0012-skill-mapping-is-plan-time-inlining.md) for the plan-time
inlining it replaced.

## Dogfooding: developing oh-my-graph with oh-my-graph

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

The shipping side of that loop is a graph too: `graphs/merge-shepherd.yaml`
takes a PR number, verifies its head locally in a throwaway worktree, marks
it ready, waits for CI and CodeRabbit, triages the review comments, waits out
the checks its own fix restarted, pauses at a human approval gate, and merges
— the operator's by-hand PR-shepherding loop, pinned in YAML. That second wait
(`recheck`) is why the gate is a decision rather than a chore: it judges the
FINAL SHA and says which one, reading every reviewer's own review rather than
one bot's — a human's `CHANGES_REQUESTED` counts, and does not clear on a push
— so nobody is asked to re-derive CI or review status by hand. Neither wait polls a condition a clock cannot clear: a review
that requested changes, a run awaiting approval, a conflicting branch and a
rate-limited bot are reported as `LATCHED <what>; unblock: <act>`, which fails
the node at once instead of spending the timeout (ADR 0021).
Its merge verdict is deliberately two-valued: the node
passes on `MERGED <sha>` and equally on `WITHHELD <reason>`, because declining
to merge past an unfinished review is the graph working. So a green run of
this one graph is not proof that anything landed — read `merge`'s artifact
(see [LIMITATIONS](LIMITATIONS.md#known-limitations)).

The `auto` equivalent — no hand-written graph, just the goal:

```sh
oh-my-graph auto "implement 'add a --dry-run flag to the run subcommand' in this repo, run make local to check it, review the diff for security and style, then open a draft PR" --input repo=$PWD --verify-cmd 'make local'
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

## Watch a run

Any of the examples above can be watched while it runs, from three angles.

**The live view.** When stdout is a terminal, `run`, `auto` and `resume`
already serve the web live view of the leg they are starting on an ephemeral
`127.0.0.1` port and open it in your browser — the node output feed on the
left, the DAG map colored as nodes pass, cost and elapsed time in the header.
A node that is still running shows the tail of its own claude transcript, so you
can read what it is doing rather than waiting for a verdict — that one panel is
Claude-only, and a Codex node's line carries its state and result but no tail.
`--no-web` turns it off for a run.

To open it yourself — from a second terminal tab, or after the fact — `serve`
takes an optional run id. With no id it is a **dashboard** of every run: one
live mini-DAG card each, in-flight runs first with their state, elapsed, cost
and node counts, settled runs collapsed below. Cards appear and settle live, so
a run started after you opened the page shows up on it, and clicking a card
opens that run's own view at `/run/<id>/`. With an id it goes straight to that
run.

```sh
oh-my-graph serve                    # dashboard of every run, http://127.0.0.1:8642
oh-my-graph serve 20260729-101600    # straight to one run, --port to move it
```

<p align="center">
  <img src="../assets/dashboard.png" alt="oh-my-graph dashboard: a LIVE header with 4 running / 1 gate-paused / 126 passed / 30 failed run chips and a cumulative spend total, an IN FLIGHT row of four live run cards each drawing its own mini-DAG with per-node states — one of them a fan-out whose three parallel nodes are mid-flight — and a collapsed SETTLED group of 159 runs" width="100%" />
</p>
<p align="center"><em>A real dogfood board, captured 2026-08-06 (KST) — a historical snapshot, not today's numbers: every card is a real run of this repository's own development. The <code>$906.1948</code> in the header was cumulative subscription usage across the project's whole development at that moment — not a per-run price, and not free.</em></p>

It binds to loopback only. It is read-only except for one thing: a run paused
at a human gate can be approved or rejected from the page. Unlike the live view
`run`/`auto` embed, `serve` is the thing you asked for: in a script, a pipe or
CI it still binds the port and serves — it just opens no browser
(`--no-open` opts out on a terminal too), and its output is unchanged.

**The text tail.** No browser, `tail -f` style — useful over ssh or piped
into something else:

```sh
oh-my-graph watch 20260729-101600
```

**After it finishes.** `runs list` is every run newest-first with its cost and
verdict; `show` is one run's per-node ledger:

```sh
oh-my-graph runs list
oh-my-graph show 20260729-101600
```

All four read the same per-run artifacts under `~/.oh-my-graph/runs/<run-id>/`
— a `state.json` snapshot and an append-only `events.jsonl` — which are a
documented, stable contract ([docs/RUN-FEED.md](RUN-FEED.md)), so anything
else you want to point at a run can read them too. Separately, every node runs
with session persistence on: a claude node is an ordinary session in
`~/.claude/projects` from the moment it starts, and a Codex node's `codex exec`
thread id — the `SESSION` column, and `session_id` in both files — is what
`codex exec resume` takes.

## Ambient chat (prototype)

`chat` turns the whole tool into an interactive front end: you talk, and each
turn is *routed* — a conversational turn is answered inline, a task-shaped turn
is planned into a graph and run, exactly like `auto`.

```
$ oh-my-graph chat
> what is the capital of France? answer in one word
Paris
> exit
```

Ask it to *do* something ("add a --version flag and open a draft PR") instead
of asking a question, and that turn is planned into a graph and executed with
the same live `▶ / ✓ / ✗` feed and cost ledger as `auto`. This is an early
prototype of the direction where oh-my-graph is the host and plain language is
the input — type `exit` or Ctrl-D to leave.

# Feature recipes

User-facing recipes for the graph model's optional per-node fields — the full
list of what a node can declare beyond the README's sample. The authoritative
spec for each lives in [DESIGN.md](../DESIGN.md) — linked per recipe.

## Recurring pipelines — write it once

A graph file is your prompt engineering, saved. The careful goal/format/rules
prompt you would otherwise re-type into a chat every morning lives in the YAML
once, and `oh-my-graph run pipeline.yaml` replays it identically on demand —
daily analysis, weekly triage, release checks — on the subscription you already
pay for. Within one run, `handoff: session` keeps the chain's context flowing,
so downstream prompts stay one-liners instead of restating the goal and the
format.

```yaml
name: daily-triage
nodes:
  - id: collect             # the careful goal/format/rules prompt lives here, once
    # what it collects with — undeclared is undelivered, and the two reads it
    # needs are all it gets: no mutating `gh` form is in reach
    allowed_tools: ["Bash(gh issue list *)", "Bash(gh pr checks *)"]
    prompt: >
      Collect today's open issues and failing checks; list each with a
      one-line status.
  - id: analyze
    depends_on: [collect]
    handoff: session        # continues collect's conversation
    allowed_tools: []       # reasons over what collect already put in the session
    prompt: Analyze what you just collected and rank by urgency.
  - id: report
    depends_on: [analyze]
    handoff: session        # the chain keeps flowing
    allowed_tools: []       # the reply IS the report
    prompt: Write the ranked findings up as a short report.
```

One boundary, stated plainly: **runs do not remember each other.** Each run
starts fresh by design
([ADR 0008](adr/0008-cross-run-session-reuse-is-deferred.md) records why
cross-run session reuse is deferred) — day-to-day consistency comes from the
pinned prompts and the `success_check` / `verify` gates, not from Claude
remembering yesterday.

## Running a node as your own subagent (`agent:`)

Add `agent: <name>` to a node and it runs as one of your existing Claude Code
subagents instead of plain `claude -p` — the review node runs as *your*
`code-reviewer`, with its system prompt, its tools and its model. It is a Claude
Code mechanism, so a graph declaring it is refused at load under `--runtime
codex`, before anything spends:

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

Spec:
[DESIGN.md § Node-as-subagent](../DESIGN.md#node-as-subagent-agent--hand-written-graphs-plus-coordinator-auto-mapping).

## Parallel edit lanes with git worktrees (`worktree:`)

By default every node runs in the working tree you invoked oh-my-graph from —
fine for read-only fan-out, but nodes that *edit* would race each other there
(and could sweep your own untracked files into their commits). Give each edit
lane a worktree name and the engine isolates it:

```yaml
nodes:
  - id: dev-a
    worktree: lane-a          # created once per run, off your repo's HEAD
    prompt: Implement feature A and commit.
    allowed_tools: [Read, Edit, Write, "Bash(git *)"]

  - id: review-a
    depends_on: [dev-a]
    worktree: lane-a          # same name -> the same checkout dev-a edited
    permission_mode: plan
    allowed_tools: [Read, "Bash(git diff*)"]
    prompt: Review the diff in this worktree.

  - id: dev-b
    worktree: lane-b          # different name -> its own checkout, edits in parallel
    prompt: Implement feature B and commit.
    allowed_tools: [Read, Edit, Write, "Bash(git *)"]
```

- Each unique name becomes one `git worktree add` under
  `~/.oh-my-graph/runs/<run-id>/worktrees/<name>`, on a fresh branch
  `omg/<run-id>/<name>` off the invocation repo's HEAD — never inside your
  checked-out tree. All nodes sharing the name share that checkout (a lane's
  dev → e2e → review runs in one place); different names edit fully in
  parallel. A node's `success_check.verify` runs in its worktree too.
- Nodes without `worktree:` behave exactly as before. `worktree` and `cwd`
  are mutually exclusive (rejected at load), and the name must be a single
  safe path element — it doubles as a directory and a branch segment.
- At run end the engine removes what it created **without ever losing work**:
  a branch that gained commits is kept (only the worktree directory is
  removed, and the retention is printed), and a worktree holding uncommitted
  changes is left in place entirely. Pick up a lane's result with
  `git merge omg/<run-id>/<name>`, cherry-pick it, or open a PR from the
  branch.
- Auto-planned (`auto`) nodes may not set `worktree:` — an unreviewed plan
  doesn't get to create checkouts and branches in your repository.

Spec:
[DESIGN.md § Worktree isolation](../DESIGN.md#worktree-isolation-worktree--hand-written-graphs-only).

## Artifact fan-out vs session chain (`handoff`)

Edges say *when* a node runs; `handoff` says *what* it inherits from its parent.

|                    | `artifact` (default) | `session` |
|--------------------|----------------------|-----------|
| The child inherits | the parent's **final reply**, persisted to `~/.oh-my-graph/runs/<run-id>/<node-id>.out` and substituted wherever `{{ artifacts.<id> }}` appears — the file path by default, the reply text itself with the `\| inline` filter | the parent's **session**, resumed by the selected CLI (claude `--resume`, or the parent's thread through `codex exec resume`): everything the parent read, did and concluded, not just its reply. The conversation, not the configuration — `allowed_tools`, `permission_mode`, `agent`, `cwd` and `budget_usd` are always the child's own |
| Parents allowed    | any number — fan-in and fan-out belong to artifact | exactly one `claude-run` node (a root, a fan-in or a gate parent is rejected at load time), sharing the parent's `cwd`/`worktree` — `lint` warns on a mismatch |
| Session shape      | each node is a fresh session | a sequential chain continuing one conversation |

Why it matters: with `artifact`, context the parent didn't put into its final
reply is gone — the child starts cold. With `session`, the child picks up
mid-conversation, so a tight pipeline (implement, then test what you just built)
needs no re-explaining. Session children still write their own `prompt` — what
they inherit is the context, not the instructions.

Note the split: the graph *file* is what you reuse **across** runs, while
`session` carries context **within** one run. Runs never remember each other —
every run starts every node fresh by design
([ADR 0008](adr/0008-cross-run-session-reuse-is-deferred.md)). The two
shapes side by side:

```yaml
  # artifact: fan-out — both reviewers read dev's final reply, in parallel
  - id: dev
    prompt: Implement the change and summarize what you did.
  - id: review-security
    depends_on: [dev]                 # handoff: artifact is the default
    permission_mode: plan             # read-only: parallel nodes share one tree
    prompt: "Security-review this summary: {{ artifacts.dev | inline }}"
  - id: review-style
    depends_on: [dev]
    permission_mode: plan             # read-only: parallel nodes share one tree
    prompt: "Style-review this summary: {{ artifacts.dev | inline }}"
```

```yaml
  # session: a chain — each child continues the same conversation
  - id: dev
    prompt: Implement the change.
  - id: e2e
    depends_on: [dev]
    handoff: session                  # resumes dev's session
    prompt: Now test what you just built and report PASS or FAIL.
  - id: summarize
    depends_on: [e2e]
    handoff: session                  # the chain continues
    prompt: Summarize what was built and how the tests went.
```

A `handoff: session` node must have **exactly one** parent, and that parent
must be a `claude-run` node — a root has no session to resume, a fan-in
can't merge sessions, and a gate never records one; all three are rejected
at load time (use `artifact` there). And although two siblings each
resuming the same parent *validates* — the one-parent rule is checked per
child — that forks one conversation into two parallel continuations, which
is a footgun, not a pattern: fan-out belongs to `artifact`.

Two more truths of the chain shape, both surfaced by `lint`: a **retried**
session node does not resume — `retry` always starts the attempt fresh, the
retried attempt's ledger detail — when it passes — says `retry started
fresh — parent session not resumed`, and `lint` warns on the combination — so either write the
child's prompt to still make sense cold, or keep `retry` off a session
chain. And a session child belongs in its parent's `cwd`/`worktree` —
claude's session lookup is project-directory-scoped, so `lint` warns on a
mismatch.

Spec:
[DESIGN.md § Handoff](../DESIGN.md#handoff--artifact-default-session-opt-in-committed).

## Budgets (`budget_usd`)

`budget_usd` caps what a node may cost. Once the node finishes, its actual cost
is compared against the budget; spending more than it declared fails the node
exactly like a failed `success_check` — the ledger row reads `FAIL` with the
budgeted-vs-actual overage, and by default the run halts so no dependent spends
on top of it. Omit `budget_usd` (or set it to 0) and nothing is enforced. A
positive one is refused at load under `--runtime codex`, which reports no USD to
enforce it against.

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

The same fact is also on the other scale, in `COST(USD)`. In a run where any
node declares a budget, each budgeted node's cost cell states the **share** of
its budget the spend used — `0.4900 (98%)` — so "one bad run from failing" is
scannable down the column on the run that *passed*, not only in the FAIL detail
on the run after it. An absolute delta cannot be scanned that way: $0.02 left
is one bad run against a $2.00 budget and barely started against a $200 one.
The share is floored, never rounded, so a node that came in under budget never
reads 100%; it is printed regardless of verdict, so a node that failed its
`success_check` at 40% of budget still shows where its money went. A graph
where no node declares a budget pays nothing for the feature: no annotation,
and no blank column.

What remains is sub-call and cross-node accounting — see
[Known limitations](LIMITATIONS.md#known-limitations).

Spec: [DESIGN.md § Execution engine](../DESIGN.md#execution-engine).

## Evidence and retry (`success_check` / `retry`)

`success_check` is evidence-grounded gating: `exit_zero`, `result_matches` (a
regex over what the node *said*) and `verify` (a command the **engine** runs and
judges). Which of those a node declares is what decides the qualifier its `PASS`
row earns — see [Reading the
ledger](#reading-the-ledger--what-a-pass-says).

**A node that fails keeps its own account of why.** The engine's summary of a
failure is one capped line — and for the commonest failure of all, a
`result_matches` miss, that line is `result did not match /<re>/`: zero bytes of
what the node actually said, after you paid for it. The node's full reply is
persisted to `<run-dir>/failed/<node-id>.out` (head-and-tail capped, with the
cut stated in the file). It is deliberately **not** an artifact — no
`{{ artifacts.<id> }}` resolves for a failed node and no `handoff: session`
child can resume one; it is the node's own account, in its own subdirectory.

A retried attempt is then not a blind re-spawn: when a check judged the previous
attempt, the retry's prompt carries that attempt's own reply — one attempt deep,
never accumulating, nonce-fenced and byte-bounded, and never quoting the check
itself (feeding a `result_matches` regex back would teach the cheapest possible
pass, which is to print whatever it matches). A cause that rendered no verdict
on the reply — a spawn error, a blown budget, a verification that could not be
*completed* — carries nothing, and a `handoff: session` retry still starts cold
and says so. This is on by default and it costs money: up to roughly 2k tokens
of quoted reply per retry of a judged failure, bounded and flat, never
compounding
([ADR 0020](adr/0020-a-retry-carries-the-attempt-it-is-repeating.md)).

`retry.on` filters over a closed set of seven causes — `nonzero_exit`,
`run_error`, `timeout`, `output_error`, `budget_exceeded`, `verify_failed`,
`result_mismatch` — rejected at load if you misspell one. `timeout` is separate
from `run_error` on purpose: a node killed by its own bound is the one failure
that always spends its whole budget before dying, so retrying it costs another
full timeout, and that must be a decision you make rather than one you inherit
by asking for spawn-failure retries
([ADR 0024](adr/0024-a-timeout-is-its-own-cause-not-a-run-error.md)).

Spec:
[DESIGN.md § Success checks](../DESIGN.md#success-checks--evidence-grounded-verification-v11).

## Per-node timeouts (`timeout`)

`timeout` is a per-node wall-clock bound replacing the 20-minute default, for
nodes whose legitimate work runs long. A node killed by it fails with cause
`timeout`, which `retry.on` treats as its own cause for the reason above.

Spec: [DESIGN.md § Execution engine](../DESIGN.md#execution-engine) ·
[ADR 0007](adr/0007-per-node-execution-limits.md).

## Bounded review loops (`feedback:`)

A review loop without unrolling it: when a reviewer node fails its judgment,
`feedback: { rerun: impl, max: 2 }` re-runs the path from `impl` back to the
reviewer, handing the findings to the re-run as `{{ feedback.review }}` (empty
on the first pass) — at most `max` times, every round priced in the ledger.

```yaml
  - id: review
    depends_on: [impl]
    permission_mode: plan
    allowed_tools: [Read, "Bash(git diff*)"]
    prompt: "Review the diff. Reply CLEAN, or FINDINGS: and what is wrong. {{ feedback.review }}"
    success_check: { result_matches: '^CLEAN' }
    feedback: { rerun: impl, max: 2 }
```

Demo: `graphs/review-loop.yaml`. Spec:
[DESIGN.md § Execution engine](../DESIGN.md#execution-engine) ·
[ADR 0010](adr/0010-a-feedback-edge-is-a-bounded-runtime-rerun-not-a-static-cycle.md).

## Reusable node shapes (`use:` fragments)

A node says `use: e2e-verify` and is spliced, at load time, from a single-node
fragment file in the graph's own `fragments/` sibling directory, binding the
fragment's declared substitution points with `with:` — the proven prompt, tool
grant and `success_check` live once, upstream, so the next fix to a shared shape
is one edit instead of a hand-sweep across every copy. The resolved graph is
indistinguishable from a hand-written one. Shipped shapes are in
`graphs/fragments/`.

That single location is the whole rule, so **where you keep a graph file decides
whether it can cite anything** — spelled out in
[docs/INSTALL.md](INSTALL.md#what-oh-my-graph-init-unpacks).

Spec:
[ADR 0013](adr/0013-a-fragment-is-a-load-time-node-splice-not-a-runtime-concept.md).

## Human gates and failure salvage

- **`type: gate`** — the node spawns nothing and pauses the run for human
  approval, continued with `oh-my-graph resume <run-id> --approve <gate-id>`
  (or `--reject`), from the terminal or straight from the live view. A fresh
  `run`/`auto` cannot pre-approve one: every gate stops the run with a resumable
  snapshot and exit code 2.
- **`resume <run-id> --retry-failed`** — re-executes only a failed run's failed
  and cancelled nodes, keeping every passed node's artifact for its dependents.

Spec: [DESIGN.md § Gate nodes and resume](../DESIGN.md#gate-nodes-and-resume-v11).

## Session limits pause, not fail

This one is the Claude runtime's ([What `--runtime codex`
changes](#what---runtime-codex-changes)).

When your Claude subscription hits its session limit mid-run, the limited node is not
marked failed: the run stops launching new work, lets in-flight nodes finish,
and exits with code 2 and a hint like `Resume after 5:20pm with: oh-my-graph
resume <run-id> --retry-failed` — which later finishes exactly the work that
never ran. If the run carries build evidence the hint appends `--verify-cmd
'<your command>'`, because a resumed leg re-supplies it instead of reading it
back off disk; the printed command is still the whole command — quoted so that
pasting it runs what it says, and followed by `--verify-timeout D` if you bound
the check with one. Detection is
honest string-matching on the CLI's message (it offers no structured signal), so
an unrecognized wording safely degrades to an ordinary failure that the same
command still salvages.

Spec: [ADR 0009](adr/0009-a-session-limit-is-a-pause-not-a-failure.md).
