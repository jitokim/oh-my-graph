# Security & Terms-of-Service stance

oh-my-graph is a **personal, local** tool that re-uses **your own** saved
`claude` or `codex` login. This document states, honestly, what that means and
where the line is.

## What oh-my-graph is

- A subprocess scheduler that runs each DAG node through the selected local
  CLI on the machine you run it from, under an account **you** authenticated.
- The same standing as invoking `claude` or `codex` yourself: it drives a CLI
  you have already authenticated.

## What oh-my-graph is NOT

- **Not** a hosted or redistributed product that authenticates other people via
  subscription OAuth. Doing that would violate Anthropic's Terms of Service.
- **Not** a shared service. It never runs as a daemon serving other users.
- It **never ships credentials**, **never proxies auth**, and **never** stores or
  transmits your tokens.

## Saved-login guarantees (enforced in code)

- **API-key scrub.** Every child process oh-my-graph spawns — a node
  subprocess, a `success_check.verify` command, the git commands behind a
  node's `worktree:` (a repo's own hooks may invoke claude), and the
  `open`/`xdg-open` launch of the `serve` URL (the URL handler it dispatches
  to is arbitrary user-configured code) — starts from your environment with
  `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`, and
  `CODEX_API_KEY` **deleted**. Those variables can switch the selected CLI from
  its saved login to API-key authentication. Deletion matches the whole
  variable name without regard to case, on
  every platform, so it holds where environment lookups are case-insensitive
  (Windows) as well as where they are not — that rule is unconditional rather
  than platform-tagged so Linux CI executes the guarantee it states. Keys that
  merely *begin* with one of the names (`ANTHROPIC_API_KEY_BACKUP`) are left
  alone. The scrub is asserted by a unit test at every call site
  (`internal/runner/claude_test.go`, `internal/verify/shell_test.go`,
  `internal/worktree/git_test.go`, `internal/browser/exec_test.go`) that sets
  all four variables in the parent process and proves none survives into the
  built child command.
- **Never `--bare`.** On Claude that flag disables OAuth; oh-my-graph never
  passes it.
- **Never an Agent SDK / direct API.** The node runtime is exclusively the
  selected local CLI subprocess.

## Runtime selection and persistence

`--runtime claude|codex` is one choice for the whole run. The default is
Claude. Fresh runs persist it in `state.json`; terminal resume and browser gate
actions load that value. An explicit mismatch is refused, so a session never
changes provider or permission semantics halfway through.

Codex receives `--skip-git-repo-check` because a reviewed graph may explicitly
set `cwd` to a non-git directory. This removes Codex's repository-presence
precondition; it does not widen the selected filesystem sandbox.

Codex threads are accepted only from the `thread.started` event emitted by
`codex exec --json`. The same id is persisted for `handoff: session` and
`codex exec resume`. Claude continues to use its preassigned UUID and JSON
envelope. Neither id is invented by the scheduler.

## Least privilege per node

- Each node declares its own `allowed_tools` and `permission_mode`. Grant only
  what a node needs. **A node that declares no `permission_mode` runs under
  `auto`** (it was `dontAsk` before
  [ADR 0034](docs/adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md)):
  a tool call matching none of the loaded allow rules is put to the CLI's own
  model classifier, which approves or denies it, where before it was denied
  outright. Declare `permission_mode: dontAsk` on a node to get the older,
  stricter disposition back.
- **`allowed_tools` is a declaration, not a sandbox.** It is passed to the CLI as
  `--allowedTools`, which is *unioned* with the permissions your own
  `~/.claude/settings.json` already grants — it can never shrink them. If your
  settings carry a standing grant like `Bash(*)` or `Write(*)`, a node has it
  regardless of what the graph declares. For hand-written graphs this is by
  design: the graph is your own reviewed artifact and your settings are the
  intended policy. Auto-planned graphs are the exception — see below, where
  `--setting-sources ""` turns the same declaration into a real limit, unless
  that run typed `--accept-loaded-user-config` and asked for your settings back.
- `permission_mode: bypassPermissions` is **opt-in per node** and prints a loud
  warning at load time. It is never a graph default. Parallel nodes that share a
  working directory should stay read-only (`permission_mode: plan`) to avoid
  racing edits.

## Auto-planned graphs (`oh-my-graph auto`)

A planned graph is untrusted LLM output executed unattended, so it gets bounds a
hand-written graph does not. Beyond the plan-time rejections (no
`bypassPermissions`, no `cwd`, no planner-authored `success_check.verify`, no
`agent`, no tool outside a fixed allowlist, and a capped `retry.max` and
`feedback.max`), auto mode runs each planned node under a layered execution
ceiling.

A **planner-authored** `success_check.verify` is refused outright rather than
constrained: it is a shell command the *engine* runs, not a tool call, so no
permission mode, tool allowlist, deny list or `cwd` restriction applies to it.
It is available to hand-written graphs, which are your own reviewed artifact,
and to a command **you** supplied at invocation (`--verify-cmd`) — never to one
a plan authored.

That flag is [ADR 0016](docs/adr/0016-build-evidence-is-a-user-supplied-engine-command.md):
after a plan has been validated, trusted Go code attaches your command to the
graph's sink nodes, so the engine runs the build itself and judges its exit
code.

```sh
oh-my-graph auto "fix the failing spec" --verify-cmd './gradlew build'
```

The command is checked for runnability *before* the planner call when it is a
plain program invocation, so a typo costs nothing — one carrying shell syntax is
left for `sh -c` to resolve rather than parsed here; it is printed with the plan (`--plan-only` shows it, per sink
node, with its timeout); it is bounded by `--verify-timeout`, which defaults to
10 minutes and may not exceed it; and it is snapshotted into the run's saved
`graph.json`. Every cycle of a `--max-cycles` goal loop plans a new graph and
every one of them gets it.

`resume` never takes a verification from a run directory on an auto graph. A
`success_check.verify` is engine-run shell outside every ceiling layer, so a
snapshot that carries one — whether from your own `--verify-cmd` run or from an
edit to `graph.json` — is not something a resumed leg replays on trust, and a
resume cannot tell the two apart. **The practical consequence: an `auto` run
started with `--verify-cmd` must be given the command again on every resumed
leg** — `resume` registers the same `--verify-cmd` / `--verify-timeout` pair, so
the command comes from you on the resumed leg exactly as it did on the first,
and it attaches to the same sinks under the same ceiling with the same
engine-judged exit code. A resume that supplies nothing while the snapshot
carried a verification is refused, naming the node: continuing such a run with
the check silently dropped is precisely the failure this mechanism exists to
prevent. The flag pair is `auto`'s and `resume`'s only — `run` has none, so a
resumed leg cannot attach a check a fresh run could not, and `resume
--verify-cmd` on a hand-written snapshot is an error. Hand-written graphs are
otherwise unaffected: their `verify:` is your own reviewed artifact and
round-trips unchanged.

Since [ADR 0030](docs/adr/0030-an-unverified-auto-run-is-a-choice-not-a-default.md),
repository content can cause the tool to **refuse to start**: `auto` stops (exit
3, before any spend) when it detects a build marker in the invocation directory
and no `--verify-cmd` was supplied, unless `--accept-no-build-evidence` states
that the run carries none. Note the direction, because it is the whole of the
trust argument and a reader auditing the boundary should not have to derive it
from an ADR: **a repository file may cause oh-my-graph to stop; it may never
cause it to run, to widen a tool set, or to attach a command.** A hostile
checkout that plants a `Makefile` gets a refusal, before any spend, naming the
file it found; a checkout that hides its build system gets exactly today's
behaviour, an unverified run. Detection happens once per invocation, before the
planner call, so nothing a node writes is ever detected and no plan can
bootstrap its own signal. The suggested command in the refusal is prose the
human reads and retypes — it is never executed by the tool.

A planned node is granted nothing by `--verify-cmd` — no ceiling layer changes, and
the allowlist deliberately does **not** grow an entry per ecosystem, because
that would put this repository's toolchain inside every user's ceiling.
`--verify-cmd` is unbounded user shell with exactly the standing a hand-written
graph's `verify:` has had since ADR 0002, running on the same seam and
executing repo-authored code (`gradlew`, `Makefile`, `npm`) the way your own
terminal does. Stated, not closed: the difference from a repo-file-derived
grant is that you chose it.

### Codex planned-node isolation

Codex cannot express Claude's per-tool rule grammar. For a planned invocation —
unless the operator typed `--accept-loaded-user-config`, which is the one thing
that turns the four flags below off; see "The operator's opt-in" —
oh-my-graph instead passes `--ignore-user-config`, `--ignore-rules`,
`project_doc_max_bytes=0`, and an empty `mcp_servers` table, then maps the
graph permission mode to Codex's sandbox:

| graph permission mode | Codex sandbox |
|---|---|
| `plan` | `read-only` |
| ordinary unattended/edit modes | `workspace-write` |
| `bypassPermissions` | `danger-full-access` |

Approval policy is always `never`; a node cannot pause an unattended graph for
an interactive permission answer — and it stays `never`, with the sandbox
mapping above, under the opt-in too: both are argv outside the branch that flag
switches. The planner itself keeps the user's normal
Codex context because its input is the user's own goal. Planned nodes do not,
unless the operator opted in; the assessor never does, because its input is
untrusted model output and its isolation is not the operator's to trade. A
hand-written graph also keeps the user's normal config, matching the existing
reviewed-artifact boundary.

This is a filesystem sandbox stance, not granular enforcement of
`allowed_tools`. `agent:` and the goal-level USD budget flag are rejected for
Codex rather than silently ignored. A node's `budget_usd` is neither rejected
nor silently ignored: it is accepted with a warning that the cap cannot apply
and that the node's `timeout:` is the guard still in force (ADR 0026). Claude agent mapping
and skill activation are not attempted. Codex USD cost is recorded as unknown;
its provider-reported token counts are the accounting surface.

**It is also a NETWORK boundary, and that half applies to every Codex node —
hand-written as well as planned.** Measured 2026-08-14 under `workspace-write`:
`gh api rate_limit` → "error connecting to api.github.com", `git ls-remote` →
"Could not resolve host". `read-only` is narrower still. So under
`--runtime codex` a graph halts at the first node that needs the network,
wherever that node sits — see
[docs/LIMITATIONS.md](docs/LIMITATIONS.md) for which shipped graphs put it
first, last, or throughout, and note the security consequence of each way out:

- `danger-full-access` — which is what `permission_mode: bypassPermissions`
  maps to in the table above — removes the filesystem sandbox **and** the
  network boundary together. There is no mode that opens the network and keeps
  the filesystem restriction; that is the trade the loud load-time warning is
  about.
- Codex's `sandbox_workspace_write.network_access=true` opens the network while
  keeping the filesystem sandbox, which is the narrower of the two. It is the
  user's own Codex configuration, not something oh-my-graph sets or can see.
- Neither is a credential boundary in the direction a reader may assume. What
  the sandbox denies `gh` is the OS **keyring**, on a machine that has one; it
  does not deny reads of `~/.config/gh/hosts.yml`, so on a machine where `gh`
  fell back to that file, a `workspace-write` node can read the token out of it
  regardless of `network_access`. Treat a Codex node's read access to your home
  directory as unrestricted unless you set the sandbox narrower yourself.

Relatedly, oh-my-graph may *detect* build markers (`gradlew`, `package.json`,
`Cargo.toml`, …) in the invocation directory. Detection only ever prints a
suggested command. It never derives a grant, because a write-capable planned
node can create those files itself — a plan bootstrapping its own capability
with no attacker anywhere.

The layers:

| layer | mechanism | closes |
|---|---|---|
| 0 declaration | `coordinator.plannedToolAllowlist` | what a plan may name at all — plan time, before any node runs |
| 1 isolation | `--setting-sources ""` — on **every** planned node since 2026-08-12, agent-mapped included, **unless the run typed `--accept-loaded-user-config`** | your standing grants; settings hooks |
| 2 grant | `--allowedTools` — the allow rules themselves | **scoped Bash** |
| 3 narrowing | `--tools "<names declared>"` | tools the model can attempt at all |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` — **dropped together with layer 1 by that same opt-in** | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` | anything the layers above got wrong |

Layer 0 is the only plan-time layer: it is the fixed allowlist above, enforced
by `validatePlannedNodeTools` before anything runs, so a plan naming `Bash`,
`Bash(*)` or an unrestricted `WebFetch` never becomes a graph. Layers 1–5 then
bound what the surviving declaration is worth at run time.

**What the permission mode changes, and what it does not.** Since
[ADR 0034](docs/adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md)
a planned node runs under `--permission-mode auto` rather than `dontAsk`. That
moves exactly one thing, and it is worth being precise about which, because
"the ceiling loosened" would be the wrong summary:

| layer | after the change |
|---|---|
| 0 declaration | **still binds.** A plan-time rejection; it runs before anything spawns and knows nothing about permission modes. |
| 1 isolation | **still binds.** It decides which *sources* supply allow rules, not what an unmatched call becomes. |
| 2 grant | **loosened — the only one.** The argv is byte-identical. Its *complement* changed: a call matching no allow rule used to be denied outright and is now put to the CLI's own model classifier, which approves or denies it. |
| 3 narrowing | **still binds, and binds harder.** `--tools` replaces the built-in tool set rather than gating it. A tool that is absent cannot be called and there is nothing for a classifier to adjudicate. |
| 4 MCP | **still binds.** A separate axis, untouched. |
| 5 residual | **still binds, and now carries more weight.** An explicit deny is evaluated *before* the classifier is consulted and is honoured even under `bypassPermissions`. It is now the only layer that refuses a call categorically. |

So the honest one-line form is **default-deny became default-classifier**, at
layer 2 and nowhere else. Read-only operations — reading files, searching code —
do not reach the classifier at all, which is a widening that layer 3 still
bounds: a node whose `--tools` omits `Read` has no `Read` to widen.

Two consequences a reader should not have to derive. `auto` **ignores allow
rules it judges broad enough to bypass its own classifier**, which for a planned
node changes nothing (layer 0 forbids a plan from ever declaring `Bash` or
`Bash(*)`, so its grants are narrow patterns by construction) but may narrow a
hand-written graph running under your standing grants. And `auto` adds a **new
failure mode with no `dontAsk` equivalent**: the CLI aborts a headless agent that
accumulates too many classifier denials. The threshold is not published and has
not been measured here. What has been ruled out is a headless node *blocking* on
a permission prompt — the strings behind that, and the binary they came from, are
in [`docs/measurements/0034-what-auto-mode-does-on-disk.md`](docs/measurements/0034-what-auto-mode-does-on-disk.md).

**Codex runs are unaffected.** `codex exec` takes no `--permission-mode`; the
mode is translated only into a filesystem sandbox, and `auto` and `dontAsk` both
map to `workspace-write`. This is a Claude-runtime change.

**Layers 1 and 4 are the two an operator may decline**, together, off by
default and only by typing `--accept-loaded-user-config` at launch. Unless that
is said, everything in this section describes what runs; where it is said, the
differences are set out under "The operator's opt-in" below, and layers 0, 2, 3
and 5 are unchanged either way.

**One preference crosses layer 1 by name, and grants nothing.** Every layer in
the table above bounds **capability** — which grants bind, which tools exist,
whose settings, hooks and `CLAUDE.md` load. Since
[ADR 0037](docs/adr/0037-a-planned-node-answers-with-the-model-the-operator-chose.md)
oh-my-graph reads exactly one key of your `~/.claude/settings.json` — `model` —
and hands it to a planned Claude node as `--model <value>`, so the node answers
with the model **you** chose rather than the CLI's own fallback default. A model
name adds no tool, loads no file, runs no hook and widens no grant: not one row
of the table moves, and the value reaches argv rather than a prompt, so no
planner output can select it (the graph schema has no `model` key). Nothing else
in that file is read — its `permissions` block holds the very standing grants
layer 1 exists to withhold, and a second key would need its own ADR.

Layer 1 is what makes the rest bind. Permission rules are matched from every
loaded source, so a standing `Bash(*)` in your own `~/.claude/settings.json` was
previously matching before a planned node's narrower `Bash(git *)` ever
mattered. Loading none of your user/project/local settings leaves oh-my-graph's
own argv as the only allow-rule source.

**Measured on claude 2.1.220** (2026-07-29), not inferred from `--help`: with a
settings.json granting `Bash(*)` and a node declaring `Bash(git *)`, an
out-of-scope shell command **ran** without Layer 1 and was **denied** with it,
while in-scope `git` kept working. So the previously-disclosed gap — *"a node
declaring any scoped `Bash(...)` pattern keeps the whole `Bash` tool"* — is
**closed for auto-planned nodes that get Layer 1.** It remains accurate for
hand-written graphs, which run without layer 1's isolation: their declared
`allowed_tools` is still rendered as `--allowedTools` (layer 2 applies to every
graph), but layers 1 and 3–5 are auto mode's alone by design.

**That measurement ran under `dontAsk`, and one half of it does not depend on
the mode.** The load-bearing half is that isolation stops your standing
`Bash(*)` from matching before the node's own narrower grant — layer 1, mode
independent. The *denial* half is precisely what the mode decides, so under
today's `auto` default the same out-of-scope `touch` is put to the classifier
instead of refused outright, and **whether it survives that has not been
measured.** The reading above is not withdrawn; it is dated, and its second half
is now a claim about `dontAsk` specifically.

**Through v0.6.0 it was NOT closed for an auto-planned node that oh-my-graph
mapped onto one of your own subagents, and since 2026-08-12 it is.** Those
nodes used to omit `--setting-sources` entirely, because `--agent` cannot
resolve without agent definitions loaded (DESIGN.md, E2) — so Layer 1 was not
weakened for them, it was absent, and everything in the table above that Layer 1
closes was open. **Measured on claude 2.1.228** (2026-08-12) against the argv
that build emitted: a mapped node declaring `Bash(git *)`, unattended under
`--permission-mode dontAsk`, ran an out-of-scope `touch` with
`permission_denials: []`, twice, while the same probe's unmapped node denied the
identical command.

What closed it is not a flag but a different way to supply the definition:
oh-my-graph copies the matched agent's file into the run's own directory and
passes it with `--plugin-dir`, which reaches the node **without** reopening
Layer 1. **Measured in the same session, on the same machine and build, minutes
apart**: the identical ceiling arm under the new argv was **denied 3 of 3**,
with the refusal recorded in the CLI's own `permission_denials`, while an
in-scope `git init` control still ran 2 of 2
([the record](docs/measurements/0017-staged-agent-restores-layer-1.md);
[ADR 0022](docs/adr/0022-a-mapped-node-gets-its-agent-staged-not-its-settings-back.md)).
The same measurement re-confirmed and widened E2: under `--setting-sources ""`
the CLI's own list of agents it can see is five built-ins, and **neither your
`~/.claude/agents` nor the repository's** — which is why the definition has to
be staged.

**That is a claim about DISCOVERY, and staging is a second channel.** A sentence
here said it covered both until 2026-08-12, and it did not: oh-my-graph scanned
`<cwd>/.claude/agents` as well as your own, a project file shadowed a user file
of the same name, and *whatever the scan resolved* was what got copied into the
node's `--plugin-dir` — which `--setting-sources ""` structurally cannot shut.
**Measured**: with a definition committed to the repository under work, the
system prompt that ran an unattended `dontAsk` node was the repository's, 2 of 2
([the record](docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md)).
It did not breach the ceiling — layer 1 was `""` and the tools stayed bound, so
the class is injection rather than escalation — but it is the repository
choosing an unattended node's instructions, which is what this section says it
prevents. **What closed it is the scan scope**: oh-my-graph reads
`~/.claude/agents` and nothing else, the same scope it has always used for
skills. With the repository's copy still committed in the node's own cwd, the
definition that resolved was yours, 3 of 3. Every staged definition is printed
before the run with the file it came from, its size and its SHA-256, so this is
checkable per run rather than taken on trust.

**The repository-configuration surface closed with it.** Loading your settings
used to mean loading **project** scope too — the `.claude/` of the repository the
node is working in — and that was the wider half of the problem: a `SKILL.md`
committed to that repository was invoked **3 of 3** by a mapped node whose
prompt never mentioned skills, and a plugin enabled by that repository's own
committed `.claude/settings.json` loaded and its skill fired. Under the new argv
the repository's copy fired **0 of 3**, and where the model did call `Skill` the
CLI answered `Unknown skill: …` with `is_error: true` — the CLI stating the
definition is not loaded, rather than an inference from silence. The
repository's project `CLAUDE.md` and its **hooks** arrive by the same default
source list `--setting-sources ""` empties; they are **implied rather than
measured** in both directions, and nothing here should be read as covering them.

**What this costs, and what is still unmeasured.** A mapped node no longer gets
your standing grants — that half is measured — and no longer gets your
`CLAUDE.md` or your hooks, which arrive by the same source list `--setting-sources ""`
empties and are, as above, **implied rather than measured**. It was the one
planned node that did. **Its model comes from neither of those places**: an
agent-mapped node is the one planned node that gets no `--model`, because the
agent definition you wrote declares one and that is the more specific choice —
the route 6 of the 187 planned nodes measured for ADR 0037 already took. **MCP is not on that list in either direction**: layer 4 is a
flag rather than a settings scope, so `--strict-mcp-config` was already on a
mapped node's argv before this change and still is, and whether that flag
actually closes MCP is the one thing here nobody has observed (E5) — read it as
a statement about the argv, not as measured isolation. Every mapping is printed
before the run, on the node's own line, with that cost named. `--no-agent-mapping`
turns mapping off run-wide and `--no-agent <name>` declines one agent; what
either buys is an **ordinary planned node** — it gets its `Skill` tool back and
nothing else. Neither restores the settings, `CLAUDE.md`, hooks or environment
access a mapped node used to have: no planned node of an isolated run has those
any more — measured
for settings, skills and agent discovery, implied for `CLAUDE.md` and hooks. The
one thing that does hand them back is the operator's own
`--accept-loaded-user-config`, and it turns agent mapping off as it does so, so
**no flag gives a MAPPED node its environment back** — the two never combine.
An agent kept in the repository's own
`.claude/agents` no longer maps at all — move it to `~/.claude/agents` if you
want it — and how many people that costs is not measured; the decision was taken
on the surface, not on a number. The staged directory this build writes carries
`agents/` and no `skills/` while the first measurement's carried both, and that
acceptance is now run: this build's own argv resolved its agent from an
`agents/`-only directory 3 of 3, denied the out-of-scope command 0 of 3, and ran
the in-scope control 2 of 2, against a machine that still breached under the
v0.6.0 argv the same hour.

Still a reduction, not a sandbox. What is **not** covered:

- **MCP closure is unverified.** `--strict-mcp-config` is passed because
  oh-my-graph never passes `--mcp-config`, so the flag costs nothing — but this
  was not measured against a real MCP server (DESIGN.md, E5). Do not read Layer
  4 as an observed guarantee. Under `--accept-loaded-user-config` the flag is
  not passed at all: layer 4 drops with layer 1 precisely so that the disclosure
  *"your MCP servers load"* does not rest on an unmeasured flag.
- **Slash-command surface** is still not enumerable by any of these mechanisms,
  and neither is **skill surface** *by these flags* — but since v0.5.2 it is
  bounded by a different one: an activation-eligible node reaches only the
  corpus `auto` stages for it, printed with each skill's size and SHA-256 before
  the run, and an agent-mapped node holds no `Skill` tool at all
  ([ADR 0017](docs/adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md)).
- **Enterprise policy settings are never dropped** by `--setting-sources ""` —
  which is deliberate: this cannot be used to step around a corporate policy.
  Nor by omitting that flag: enterprise and managed settings are unioned on top
  of the source list, so `--accept-loaded-user-config` cannot subtract from a
  union it never joined, and it is no more a route around a corporate policy
  than the isolated default is.
  Conversely, on a machine with `allowManagedPermissionRulesOnly`,
  `--allowedTools` rules are ignored entirely and the ceiling is the managed
  policy, not ours.
- The ceiling rests on behaviour of a **specific CLI version**. A future claude
  release could change it; Layer 5 is retained precisely so a wrong assumption
  in Layers 1–4 degrades to the older, weaker ceiling rather than to nothing.

**Planned nodes are more isolated and less capable than they used to be.**
Dropping your settings also drops your CLAUDE.md, your hooks and your configured
MCP servers for those nodes. That is the intended direction, but it is a real
behaviour change: if your `auto` runs depended on an MCP server, they will stop
— and `--accept-loaded-user-config` is the narrow door out of that, per run,
typed at launch, at the price set out below.
**The one thing they do not lose is your model choice**: that key is read on its
own and passed as `--model`, per the paragraph above, so "less capable" is about
tools, files and hooks and never about which model does the thinking. On a
`--runtime codex` run they lose that too — `--ignore-user-config` withholds
`~/.codex/config.toml` and oh-my-graph does not read it, so a planned Codex node
runs the model `codex` itself defaults to
([docs/LIMITATIONS.md](docs/LIMITATIONS.md); the Codex follow-up is carried in
the operator's private backlog — oh-my-graph-hq `notes/open.md` — not in the
public tracker).
**Through v0.6.0 agent-mapped nodes were the exception in both directions** — no
*settings* were dropped for them, so your CLAUDE.md and hooks, and the
repository's, did load, and they were correspondingly less isolated, not more.
**Since 2026-08-12 (KST) they are not**: the staged-definition change above keeps
Layer 1 at `""` for a mapped node too, so it drops the same things every other
planned node drops (measured for settings, skills and agent discovery; implied
for `CLAUDE.md` and hooks). **MCP was never part of that exception.** Layer 4 is
a flag, not a settings scope: `--strict-mcp-config` has always shipped on a
mapped node's argv too, with no `--mcp-config` beside it, so an `auto` run that
depended on an MCP server is *expected* to stop there as well. That expectation
is read off the argv, not measured: closure against a real MCP server was never
tested and remains unverified.

Re-running a saved `graph.json` through `oh-my-graph run` drops the ceiling
entirely — that path assumes you reviewed the file. Treat `auto` as you would
any unattended agent: run it in a directory you are willing to have modified.

### The operator's opt-in — `--accept-loaded-user-config`

Everything above describes the run that types nothing, which is the default.
Since [ADR 0032](docs/adr/0032-a-planned-node-may-carry-the-operators-configuration.md)
an operator may state at launch that this run's planned nodes carry **their
own** configuration. The flag is **off by default**; it is `auto`'s alone —
`chat` parses no such flag, and `resume` registers none, because a resumed leg's
own flags may only de-escalate (it inherits the first leg's choice from the
snapshot's policy map and reprints the same disclosure). A run that does not
type it is byte-for-byte the run that shipped in v0.10.0.

**What it changes: layers 1 and 4, together, for the whole run.** Layer 1's
`--setting-sources ""` is omitted, so your user/project/local settings load on
Claude; on Codex none of `--ignore-user-config`, `--ignore-rules`,
`project_doc_max_bytes=0` or `mcp_servers={}` is passed, so `~/.codex/config.toml`,
your repository's rules and `AGENTS.md` files load. Layer 4's
`--strict-mcp-config` goes with it, deliberately — see the MCP bullet above.
With your settings come your `CLAUDE.md`, your hooks and, on Claude, **your
standing permission grants**: a `Bash(*)` in `~/.claude/settings.json` matches
before a node's narrower `Bash(git *)`, so that declared scope is a declaration
again and not a limit. That is the bill, and it is printed with the plan rather
than left to be discovered.

**What it does not change.** Layers 0, 2, 3 and 5 are untouched: the plan-time
allowlist still rejects `Bash(*)`, and each node's `--allowedTools`, `--tools`
set and `--disallowedTools` are the isolated run's for the same graph. (That the
last two still bind under restored settings is ADR 0032's own falsifier 1, and
its §8 measurement has not been run — read it as a projection.) Agent mapping
and skill activation turn **off** with the opt-in, in the coordinator option
rather than at the call site, because both rest on layer 1 being `""`: with it
gone, a same-named definition in the repository under work beat the staged copy
3 of 3 (measurement (j), arm `X`). On Codex the sandbox mapping and
`approval_policy="never"` are argv outside the branch this flag switches, so the
filesystem sandbox and the network boundary are exactly as above.
**Enterprise and managed settings are unioned on top and cannot be dropped by
it** — this is not a route around a corporate policy. And **the environment
scrub is untouched**: `internal/childenv.Scrub` is one list with no runtime
branch, deleting `ANTHROPIC_API_KEY`, `ANTHROPIC_AUTH_TOKEN`, `OPENAI_API_KEY`
and `CODEX_API_KEY` from every child whether the run opted in or not. A restored
configuration is not a restored API key.

So the saved-login guarantee at the top of this document stays in exactly its
narrow form, and the opt-in does not widen it: oh-my-graph never passes `--bare`
and always scrubs, so nothing **it** does switches a CLI off its saved login. It
still cannot guarantee that the login is subscription OAuth — that is your
machine's authentication state, which this tool neither sets nor inspects.

### Isolation stops at the invocation repository

Everything above bounds what a planned node may *call*. It says nothing about
*where* the node works, and the answer there is narrower than people assume:

- **Every planned node runs in the directory you invoked `oh-my-graph` from**,
  in the checkout you had open. `cwd:` and `worktree:` are both rejected at
  plan time (`validatePlannedNodeCwd`, `validatePlannedNodeWorktree`), so
  `auto` provisions **no** managed worktree of its own — a planned node edits
  and commits in your working tree unless it arranges otherwise itself.
- **Managed worktrees (`worktree:`, [ADR 0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md))
  are a hand-written-graph feature, and they only ever branch from the
  invocation repository.** `worktree.GitManager` holds a single repo directory
  (the process's own), and every checkout it creates lives under
  `$OMG_HOME/runs/<run-id>/worktrees`.

So **any other local repository gets no isolation from oh-my-graph at all** —
including one your goal names by absolute path. If a node `cd`s there, switches
that checkout's HEAD, or creates a worktree of its own, that is the node's
improvisation, not a guarantee the engine offers or can undo. A shared checkout
is the concrete hazard: a node changing HEAD in a repository some other process
is working in will collide with it, and the run's feed records the node's
result, not the git commands it chose to run.

**`auto` now says this at plan time, for the repositories it can see.** When
the goal or a planned prompt names an absolute path that resolves into a git
checkout outside the invocation repository, the plan printout — the one
`--plan-only` also renders — names that checkout, says where the plan named it,
and states that nothing there is isolated, before any node spends. It is a
warning and never a refusal: a multi-repository goal is legitimate, the engine
just cannot isolate it. Treat it as a floor, not a guarantee. It is a heuristic
read of the plan's text and it cannot see a path a node builds at run time, one
arriving through an `--input` or a parent's artifact, a repository reached by a
relative path, or what a node actually does once it is there — so a plan that
warns about nothing is not a plan that touches nothing outside this repository.

Two consequences worth planning around: keep a goal that spans repositories to
one that expects each node to isolate *itself* in the repositories it does not
own, and never verify such work by asserting a local HEAD — an assertion like
`git -C <other-repo> rev-parse --abbrev-ref HEAD` encodes an assumption about
where the work happened and fails on work that succeeded. The planner is told
to assert remote state (`gh pr list --head <branch>`) for exactly this reason.
Managed multi-repository worktrees are not implemented, and
[ADR 0018](docs/adr/0018-isolation-stays-scoped-to-the-invocation-repository.md)
records the decision **not** to build them — the surface they need is the one
`validatePlannedNodeCwd` closed deliberately, the cost that decides it is
cleanup debris left in a repository you did not open, and the shape any future
proposal has to start from (a user-supplied `--repo`, never a planner-named or
detector-derived path) is written down there. That record also names the
measurement that would reverse it, so the warning above is the protection for
as long as this section says it is.

## Subagents (`agent:`)

A hand-written node may run as one of your own Claude Code subagents
(`agent: code-reviewer` → `claude -p --agent code-reviewer`). oh-my-graph does
not parse the subagent's definition and makes **no claim** about how that
subagent's own `tools:` combines with the node's `allowed_tools` — that is the
CLI's precedence, and for this path it is **unmeasured**. (DESIGN.md's E6 probed
a subagent against `--tools`, but `--tools` is emitted only by auto mode, which
forbids `agent:` — so that result does not cover the hand-written case.) If a
subagent grants tools the node did not, assume it gets them.

**Auto-planned nodes may not set `agent:` at all.** Letting an unreviewed plan
pick which of your subagents runs a node would hand it that subagent's system
prompt, tool grant and model, routing around Layers 0–3 in one word. It is
rejected at plan time, and a reflection-driven test over `graph.Node` fails the
build if any future schema field is added without an explicit decision like this
one.

## The web live view (`oh-my-graph serve`)

`serve <run-id>` renders one run; `serve` with no id renders a **dashboard over
every run directory** under `$OMG_HOME/runs`, with each run's own view mounted
at `/run/<id>/` — one process, one port, all your runs. A fresh `run`/`auto`/
`resume` whose stdout is a terminal embeds the same server for that leg's
duration (`--no-web` opts out; a non-terminal stdout gets no server at all).

Run directories hold node prompts, artifacts and session ids, so:

- **Loopback only.** The listener binds `127.0.0.1` (default port 8642), and a
  test asserts the *bound listener address*, not just the config. Additionally
  every request's `Host` header must name `127.0.0.1` or `localhost` or it is
  **403** — otherwise a hostile page could DNS-rebind a domain it controls onto
  127.0.0.1 and read `/api/*` through your own browser.
- **Paths come from listings, not from URLs.** A `/run/<id>/` id is matched
  against the runs root's directory listing before any path is built from it,
  and a `?node=<id>` against the snapshot's own node set; a typo and a traversal
  probe are the same 404. The one read outside the run directory — the live
  transcript tail into your own `~/.claude/projects` — is named by the
  feed-published, shape-checked session UUID, never by URL input.
- **Nothing served is sniffable or framable.** Every response of both
  front-ends carries `X-Content-Type-Options: nosniff` and a Content-Security
  Policy — which matters most on `/api/result`, where a node's raw reply is
  served as `text/plain` and must never be re-interpreted as HTML. The same
  policy's `frame-ancestors 'none'` is the gate guard described below.
- **`serve` spawns nothing.** The package imports no `os/exec`; both processes
  its features imply belong to the exec seams above.

**It is not read-only.** Since the gate routes landed (ADR 0014), two POSTs —
`/api/gate/approve` and `/api/gate/reject` — decide the gate a run is paused at,
which continues the run: rewriting `state.json`, appending to `events.jsonl`,
and running the nodes the gate was blocking. Four guards weigh the request —
the loopback bind, the `Host` check, an `Origin` check and a per-process token —
and one guard weighs the *page*, because all four of the others ask where a
request came from, and a clickjacked click answers every one of them honestly:

- **Token (CSRF).** 32 bytes of `crypto/rand`, minted per serving process,
  rendered into the served page and demanded back in `X-OMG-Token` — missing is
  400, mismatched is 403, compared in constant time. No shape of a gate POST
  reaches the resumer without it. It is a CSRF guard, **not a login**.
- **Origin.** A POST whose `Origin` names anything but this server's own origin
  is 403, so a decision from a page this process did not serve is refused on its
  provenance before its token is weighed. An **absent** `Origin` is allowed
  through — curl and the CLI's own tests send none, and the token remains the
  whole guard there. This narrows what a browser can do; it is hardening layered
  in front of the token, not the closing of a hole.
- **Frame refusal (clickjacking).** Every response of both front-ends carries
  `frame-ancestors 'none'` and `X-Frame-Options: DENY`, so no other page may
  embed this one. This is the one guard above that refuses the *framing* rather
  than the request, and it closes a hole the other four leave open by
  construction: a hostile page you are already visiting frames
  `http://127.0.0.1:8642/run/<id>/` — the port is the documented default, and
  the dashboard's `/` needs no run id at all — overlays it, and baits a click
  onto the approve button. The click lands **inside** the framed page, so that
  page reads its own token and the browser stamps this server's own `Origin`.
  Host loopback, Origin matching, token valid, constant-time compare passed: a
  gate nobody read is approved, and the run spends money. `'self'` and
  `SAMEORIGIN` would both still permit this, so the values are pinned by test.
- A decision is valid only while the viewed run is genuinely paused at the named
  gate; a held `resume.lock`, a missing snapshot, a non-pending gate id, or a
  view with no resumer injected are each 409. Only the standalone `serve`
  process injects a resumer.

**What this does not give you:** there is no authentication. The loopback bind
*is* the read access control, so **any process or user on the same machine can
read every run** — prompts, artifacts, session ids and transcript tails —
and, holding a token from a served page, decide a paused gate. This is a
single-user local tool and is scoped accordingly; widening the bind address
would need a real auth story first.

The frame refusal and the CSP are **browser-side guards only**, and it is worth
being exact about what that leaves:

- They stop a page from embedding this one and stealing a click. They do **not**
  stop that page from navigating your top-level window (or a popup) to the view:
  the gate would then be on screen, on this origin, with you looking at it. That
  is a nuisance, not a silent approval.
- They stop nothing that is not a browser. Any local process running as you can
  read a served page, take its token, and POST a gate decision — no header a
  server sends constrains a program that ignores headers. The loopback bind and
  the machine's own user boundary are still the whole story there.
- The CSP is written against what the shipped `ui/` assets actually do, and one
  directive is loose by necessity: `style-src` carries `'unsafe-inline'` because
  vendored cytoscape injects a `<style>` element at renderer init. `script-src`
  stays `'self'` with no `'unsafe-eval'` — nothing shipped calls eval, and the
  one Function-constructor call in the vendored libraries (lodash's
  `Function("return this")()` root fallback, in cytoscape and dagre alike) is
  short-circuited before it in a browser, so it never runs.
  A cytoscape bump must re-verify the
  policy by hand (`internal/serve/ui/vendor/README.md` says so), because no Go
  test can execute vendored JavaScript.

## What is exposed at rest

A run directory under `$OMG_HOME/runs/<run-id>` is the run's whole memory: every
node's prompt and the values interpolated into it (`state.json`), every node's
full reply (`<node-id>.out`), loop feedback payloads, the event stream
(`events.jsonl`), and — for `auto` — the planner's saved spec (`graph.json`),
which inlines the body of any mapped local `SKILL.md`.

Those files are written **owner-only**: `0700` directories, `0600` files. That
is the stance `auto`'s saved plan spec already took, now applied to the rest of
the run.

One thing inside a run directory is not ours to mode: `worktrees/<name>` is a
checkout of *your own source*, and `git` writes it at your umask. Only the
`worktrees/` container is `0700` — which is enough, since a `0700` directory
denies traversal to everyone else regardless of what is under it.

Two things this deliberately does not do:

- **It does not re-mode existing run directories.** `MkdirAll` returns success
  on a directory that already exists without touching its mode, so a run from an
  older binary keeps its `0755`, and `resume` and `serve` read it exactly as
  before. If you want the old ones narrowed, that is a `chmod -R go-rwx
  ~/.oh-my-graph/runs` you run yourself. The same applies per-file: `O_CREATE`'s
  mode only applies when the file is created, so a resumed leg appends to an
  older `events.jsonl` without re-moding it.
- **It does not touch what `oh-my-graph init` scaffolds.** Those files are
  written into *your own project*, where `0644` is what a source file should be.

**This narrows the at-rest exposure; it does not close the exposure of prompts.**
The same prompt text is in the node's argv while it runs (below), and, because
session persistence is on by design, in the CLI's own session transcript under
`~/.claude/projects`, whose permissions are the CLI's to set, not ours.

## What is exposed while a node runs

A node's full prompt is passed to the selected CLI as an argv element (`-p
<prompt>` for `claude`, a positional argument for `codex exec`, both built in
`internal/runner/`), so for the lifetime of that subprocess it is
readable from the process table — `ps auxww`, and on Linux
`/proc/<pid>/cmdline`, which is world-readable unless the machine sets `hidepid`.
Any process running as you can read it on any platform.

This is not an incidental leak of a short string. A prompt carries its
`{{ inputs.* }}` values, and because `| inline` is used pervasively across the
shipped graphs and fragments, it carries the **inlined content of upstream
artifacts** — that is, the text of earlier nodes' replies.

**Known, and not currently fixed.** The fix would be to feed the prompt on
stdin, and that is a change to the most lifecycle-sensitive seam in the repo:
`CLIRunner` owns `waitDelay`, process-group kill and `--output-format
json` parsing, writing to a child's stdin adds a deadlock surface, and the
interaction with `--resume` is unmeasured. It would need a real `make smoke`
measurement against a live CLI before anyone should believe it. Until then it is
documented rather than claimed closed. On a machine where you do not trust the
other local users, treat a running node's prompt as visible to them.

## Reporting

This is a young project. If you find a security issue, please open an issue
describing it (omit any secrets) so it can be triaged in the open.
