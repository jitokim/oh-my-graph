# Examples

The [README's Example](../README.md#example) walks through the quickstart run
— the cheapest real smoke test. This file collects the rest: the remaining
walkthroughs, then per-feature recipes for the graph model's optional fields.

Walkthroughs, in order:

1. [Zero-config: auto mode](#zero-config-auto-mode-the-headline) — the
   headline feature.
2. [Dogfooding](#dogfooding-developing-oh-my-graph-with-oh-my-graph) — using
   oh-my-graph to develop oh-my-graph.
3. [Observe with fleetops](#observe-with-fleetops) — watch nodes run in a
   sister tool.
4. [Ambient chat](#ambient-chat-prototype) — talk; each turn routes to a
   reply or a graph (prototype).

## Zero-config: auto mode (the headline)

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
it, while in-scope `git` kept working. The gap this project used to disclose — a
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
[SECURITY.md](../SECURITY.md). Hand-written YAML is unaffected by all of this:
it is your own reviewed artifact, it keeps your settings and hooks and MCP
servers, and it remains the path for precise control.

**Custom YAML vs. auto, in one line:** reach for `graphs/*.yaml` when you know
exactly which tools each node should have and how they should hand off to
each other; reach for `auto` when you'd rather describe the outcome and let
the planner design the DAG.

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

## Observe with fleetops

Run fleetops next to any example and watch each node appear as an ordinary
claude session.

oh-my-graph executes; [fleetops](https://github.com/jitokim/fleetops) is a
sister tool that observes the same `~/.claude/projects` transcripts — no
coupling beyond that shared directory. Every node runs with session
persistence on, so it shows up as an ordinary claude session the moment it
starts.

Run fleetops in a second terminal tab while any of the examples above is
running, and you'll see each node appear in fleetops' fleet list as
oh-my-graph delegates to it — live, for free, with zero integration code.

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

User-facing recipes for the graph model's optional per-node fields. The
authoritative spec for each lives in [DESIGN.md](../DESIGN.md) — linked per
recipe.

## Running a node as your own subagent (`agent:`)

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

Spec:
[DESIGN.md § Node-as-subagent](../DESIGN.md#node-as-subagent-agent-v11--hand-written-graphs-only).

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

`handoff` decides what a child inherits from its parent: `artifact` (the
default) hands over the parent's final reply via `{{ artifacts.<id> }}`;
`session` resumes the parent's claude session, so the child inherits
everything the parent read, did and concluded — the conversation, not the
configuration: `allowed_tools`, `permission_mode`, `agent`, `cwd` and
`budget_usd` are always the child's own. The two shapes side by side:

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

A `handoff: session` node must have **exactly one** parent — a root has no
session to resume, and a fan-in can't merge sessions; both are rejected at
load time (use `artifact` there). The one parent must also be a `claude-run`
node — a gate has no session to resume, which surfaces at run time, not load
time. And although two siblings each resuming the same parent *validates* —
the one-parent rule is checked per child — that forks one conversation into
two parallel continuations, which is a footgun, not a pattern: fan-out
belongs to `artifact`.

Two more run-time truths the chain shape hides: a **retried** session node
does not resume — `retry` always starts the attempt fresh (DESIGN
§ Execution engine) — so either write the child's prompt to still make
sense cold, or keep `retry` off a session chain; and a session child belongs
in its parent's `cwd`/`worktree` — the loader does not check that for you.

Spec:
[DESIGN.md § Handoff](../DESIGN.md#handoff--artifact-default-session-opt-in-committed).

## Budgets (`budget_usd`)

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
[Known limitations](LIMITATIONS.md#known-limitations).

Spec: [DESIGN.md § Execution engine](../DESIGN.md#execution-engine).
