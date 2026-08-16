# Limitations & platform notes

Detail moved out of the README: the full platform-support notes, the honest
gaps as of **v0.8.0**, and what is deliberately deferred. Where a gap has
already been closed on `main` but not in a tagged release, this file says so
in the paragraph that describes it rather than in the stamp.

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
best-effort. Two things to know before relying on it:

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

Not on that list any more: **the env scrub**. It used to match keys exactly,
which on Windows — where environment lookups are case-insensitive — meant a
lowercase `anthropic_api_key` reached the child and billed the run to the
metered API. [The scrub](../README.md#bring-your-own-login) now compares the
whole key without regard to case, on every platform, so the unconditional
sentence in README and SECURITY.md holds here too. It is not a Windows-tagged
code path: this project's CI is Linux, and a guarantee only Windows executes is
a guarantee nothing tests.

The fix ships in **v0.5.3**, so `go install …@latest` carries it. Any earlier
binary does not: v0.5.2 and every tag before it compare keys exactly, so on
native Windows they have the hole this paragraph describes.

On Windows, prefer WSL.

## Known limitations

Honest gaps as of v0.8.0. **This file is where they are tracked** — the issue
numbers below name the *closed* issue each gap was carved out of, which is
provenance, not a tracker: those issues asked for the feature that shipped,
and were closed when it did. What survived the feature is the paragraph, here.

One number is not provenance, and saying so is the point of this paragraph:
**#151 is open**, and it is behind the review-fragment half of *"A PASS row
does not say which outcome passed"* below — the issue says as much itself. It
asked for a gating shape for the review fragments; `backlog-batch`'s lane A
answers it in this release, and `dev-review-pr` and `self-dev` were left
advisory *as a decision*, so a green run of either is still not evidence the
diff was clean. That residual is the part of the gap this release did not
close, and the paragraph below is where it is described. Every other gap here
has no open issue behind it.

- **A `success_check` without `verify` is still self-report.** `success_check.verify`
  closes this for graphs that opt in: the engine runs a command of your choosing
  and judges its exit code and output, independent of anything the node claims.
  But it is opt-in per node — a check that configures only `exit_zero` and
  `result_matches` is exactly as self-reported as it was before, because
  `result_matches` regexes over the node's own claimed result text. Nothing
  forces a node to carry evidence, and for nodes whose work is not externally
  observable (a review, a summary) there is nothing to verify against.
  ([#7](https://github.com/jitokim/oh-my-graph/issues/7))
- **A PASS row does not say *which* outcome passed.** A node whose verdict is a
  two-valued alternation (DESIGN.md, "Verdict patterns") passes on either of
  its legitimate answers, and the ledger has one column for both. `merge-shepherd`
  ships two of them: `merge` answers `MERGED <sha>` or `WITHHELD <reason>` —
  refusing to merge past an unfinished review is the graph working — and
  `recheck` answers `RECHECKED <sha>` or `UNSETTLED <sha>`, which is the
  difference between checks that concluded green and checks that were still
  moving when the wait ran out. (Its third verdict, `LATCHED <sha>`, is not in
  this gap: it fails the node on purpose, so it shows up as a red row that
  names the act on its first line.) So a green run of that graph is **not** by
  itself evidence
  that anything landed, or even that anything was checked. The ledger prints
  `PASS` either way; only the node's artifact (`<run-id>/merge.out`,
  `<run-id>/recheck.out`) says which. Read it, or `git log`. The
  engine has no notion of a "partial" verdict to print instead, and inventing
  one would mean the engine parsing verdict semantics out of a regex it
  deliberately treats as opaque.
  The review fragments are the asymmetric case of the same gap, and worth
  naming separately: `review-style` and `review-security` answer `CLEAN` or
  `FINDINGS:`, both PASS, and unlike `WITHHELD` — where refusing to merge *is*
  the graph working — a `FINDINGS:` is the one signal in a run saying the diff
  has a defect. `dev-review-pr` and `self-dev` open a pull request downstream
  of exactly that, on purpose: the findings are interpolated into the PR body,
  which is where the human deciding the merge will read them. So a green run
  of either is not evidence the diff was clean, and their review nodes now say
  so at the node. What is *not* a limitation is the choice: a graph that wants
  findings to stop its pipeline narrows the review's `success_check` to the
  clean verdict and declares a `feedback:` arc on the same node, so the
  rejection re-runs the implementation with the findings instead of reading as
  a broken run (`backlog-batch`'s lane A does this; lane B advises). Both keys
  are the graph's — a fragment may not declare `feedback` at all (ADR 0013).
  The ledger still prints PASS for the advisory case, for the reason above —
  and note that "read the node's artifact" is the advisory remedy only: a
  gating review that found something FAILS, and a failed node writes no
  `<run-id>/<node>.out` at all. Its findings are in `<run-id>/feedback/<node>.out`
  while the loop is running (an engine payload, not a consumer contract), and
  in `<run-id>/failed/<node>.out` once the loop is exhausted — the copy meant
  for a human, whose path the run prints as it saves it
  (`✎ <node>  reply saved: …`).
- **`budget_usd` is enforced per node, but not sub-call or across nodes.**
  A positive budget is passed to claude as `--max-budget-usd`, so claude aborts a
  node the moment its own spend crosses the budget (a real mid-run kill), and the
  final cost is re-checked post-hoc as a backstop — a runaway node no longer
  spends unbounded to the wall-clock timeout. Two gaps remain: claude accounts
  *between* API calls, so the one in-flight call past the threshold can still
  overshoot before the abort lands; and each node's cap is independent — there is
  no whole-graph budget. Closing the first needs incremental cost
  (`--output-format stream-json`), a `NodeRunner`-contract change.
- **Codex has token accounting, not USD budgeting.** Under `--runtime codex`,
  USD cost is explicitly unknown and token usage is persisted and printed —
  `parseCodexJSONL` starts every outcome at `CostUnknown: true` and never
  writes a dollar figure, so per-node cost, the run total and any budget
  comparison are all absent for the whole run. That is not a gap in one
  reading: it is the trade the runtime makes, and the run says so before it
  starts (`noteCodexRuntimePolicy`).
  `agent:` and `--max-goal-budget-usd` are rejected before execution; a node's
  `budget_usd` is not ([ADR 0026](adr/0026-an-inapplicable-cap-is-not-an-unsafe-one.md)).
  The cap has nothing to bound, so the graph loads and prints one warning per
  budgeted node saying the cap cannot apply and which guard still holds — that
  node's `timeout:`, or the runner's 20m default. The goal ceiling differs
  because it is the only bound on an ITERATING loop: an unmeasurable one would
  stop at its first cycle boundary having bought a cycle to learn what preflight
  says for free. Claude Code agent mapping and skill activation do not run
  on Codex. Codex also cannot enforce Claude's granular `allowed_tools`
  patterns; its boundary is the selected filesystem sandbox, with user config,
  project rules/AGENTS files and MCP servers removed from planned invocations.
  Every Codex node also runs with `approval_policy="never"`: a non-interactive
  scheduler cannot answer a prompt, so nothing is escalated for approval.
  ([#8](https://github.com/jitokim/oh-my-graph/issues/8))
- **A Codex node has no network — and a graph halts at the FIRST node that
  needs it, which is not always the last one.**
  The Codex sandbox is a network boundary as well as a filesystem one. Measured
  2026-08-14 under `workspace-write`: `gh api rate_limit` → "error connecting
  to api.github.com" (the same call on the host returns 5000), `git ls-remote`
  → "Could not resolve host". Where the network node SITS is what decides the
  cost of the failure, and the shipped graphs cover all three positions:
  - **Last node** — `graphs/fragments/pr-publish.yaml` (used by `self-dev`,
    `dev-review-pr` and twice by `backlog-batch`) and `adr-driven-dev`'s
    `finalize`, which pushes the branch and opens the PR through its own
    `Bash(gh *)` grant. These are the runs that do all the work and then fail
    on the last node.
  - **First node** — `apply-flags`, whose `dev` node must have every flag
    "applied, committed and pushed BEFORE you reply" and whose verdict payload
    is the pushed SHA. Its LAST node, `verify`, is `permission_mode: plan` with
    `git diff`/`log`/`show` and needs no network at all. So a graph can publish
    without finishing on a network node.
  - **Several** — `merge-shepherd`, which runs `gh` in all five of its model
    nodes. Its FIRST node, `verify`, runs `gh pr view`, `gh pr diff` and
    `git fetch origin pull/<n>/head`, so a Codex run of it fails at node 1
    having done nothing. That is cheaper than a late failure, not worse — but
    it is not what "does all the work and then fails" describes, which is why
    this entry names positions instead.

  Three remedies, in order of bluntness. Use `--runtime claude` (the default)
  for a graph that publishes; or end the Codex graph before its network node
  and publish separately; or open the network for that ONE node, which the tool
  already ships two ways of doing:
  - `permission_mode: bypassPermissions` on that node maps to Codex's
    `danger-full-access`, which is **not a sandbox** — that node keeps both the
    network and the keyring. It is opt-in per node and prints a loud warning at
    load time, which is exactly the trade being made here.
  - Codex's `sandbox_workspace_write.network_access=true` lifts the network
    block. That IS the remedy for `git push` and `git ls-remote` — which is what
    `pr-publish` and `adr-driven-dev`'s `finalize` need — and it is **not** a
    remedy for `gh` on a machine with an OS keyring: `gh` stores its token there
    when one is available, the sandbox denies it, and `gh` fails with "no oauth
    token found for github.com" (measured 2026-08-14, macOS, `workspace-write`).
    On a machine with no keyring — a headless Linux box is the plausible one for
    an unattended graph — `gh` falls back to `~/.config/gh/hosts.yml`, a plain
    file the sandbox can READ (it restricts writes and network, not reads), so
    `network_access=true` probably does fix `gh` there. That case is
    **unmeasured**; the macOS result above is the only one this repo has run.
- **ADR 0009's session-limit pause does not exist on Codex.** A Claude node
  that hits a plan session limit becomes a resumable pause (exit 2). The
  matcher is Claude's own prose and `SessionLimited` is set only for the Claude
  runtime, so the same situation under `--runtime codex` is an ordinary node
  failure — recoverable with `resume --retry-failed`, but not the pause the ADR
  promises. **Settled 2026-08-15: that promise belongs to the Claude runtime,
  not to the engine**, so no runtime owes a session-limit signal and this
  entry describes a decision rather than a gap
  ([ADR 0009 "Scope"](adr/0009-a-session-limit-is-a-pause-not-a-failure.md),
  closing [#171](https://github.com/jitokim/oh-my-graph/issues/171)). Removing
  the runtime gate would not add the pause — the matcher is Claude's prose, so
  it would only add a pause that can never fire.
- **A `gate` always pauses a fresh run.** Gate nodes are implemented (pause /
  approve / reject, continued by `oh-my-graph resume`), but a fresh `run`/`auto`
  cannot pre-approve one: every gate stops the run with a resumable snapshot and
  exit code 2, and decisions are only supplied on resume.
  ([#9](https://github.com/jitokim/oh-my-graph/issues/9))
- **Auto mode's tool ceiling is a reduction, not a sandbox — and parts of it are
  unverified.** The isolation and scoped-Bash layers were measured against a
  real `claude` 2.1.220 and hold (see [SECURITY.md](../SECURITY.md)). MCP closure
  was **not** measured: `--strict-mcp-config` is passed because it costs
  nothing, not because it was observed to work. Slash-command surface is not
  enumerable by any of these mechanisms, and neither is skill surface *by these
  flags* — but since v0.5.2 it is bounded by a different one: an
  activation-eligible node reaches only the corpus `auto` stages for it, printed
  with each skill's size and SHA-256 before the run, and an agent-mapped node
  reaches no skill at all
  ([ADR 0017](adr/0017-planned-nodes-get-skill-activation-not-inlined-skill-text.md)).
  The whole
  ceiling is coupled to one CLI version's behaviour.
  ([#11](https://github.com/jitokim/oh-my-graph/issues/11))
- **`agent:` tool reconciliation is undefined and unmeasured for hand-written
  graphs.** When a hand-written node names a subagent, oh-my-graph does not
  reconcile that subagent's own `tools:` with the node's `allowed_tools` — the
  CLI decides, and this project makes no claim about how. If the subagent
  grants tools the node did not, assume it gets them. (An auto-MAPPED node is
  the exception: the coordinator refuses to map an agent whose frontmatter
  declares a tool outside the node's planned `allowed_tools`, and the node's
  `--tools` ceiling still binds — DESIGN.md, E6.)
- **An agent-mapped node loses your environment, and that is the change of
  2026-08-12.** Until then a mapped node dropped Layer 1 — `--agent` could not
  resolve without the user's settings loaded — and it was the one planned node
  that saw your standing permission grants, and with them your `CLAUDE.md` and
  your hooks (that second half arriving by the same source list, and **implied,
  not measured**, in both directions — as below).
  It no longer does: the matched agent definition is copied into the run's own
  directory and supplied with `--plugin-dir`, so Layer 1 stays `""` and a
  mapped node is as isolated, and as limited, as every other planned node
  ([ADR 0022](adr/0022-a-mapped-node-gets-its-agent-staged-not-its-settings-back.md)).
  If your agents lean on your environment, **no flag gives that back.**
  `--no-agent-mapping` (or `--no-agent <name>`) trades agent mapping for
  ordinary planned-node isolation: the node stops running under your agent's
  system prompt and gets its `Skill` tool back, and that is all it gets — an
  unmapped planned node has no more access to your settings, `CLAUDE.md`, hooks
  or grants than a mapped one does — on the same evidence scope: measured for
  settings, implied for `CLAUDE.md` and hooks.
  <br>What that fixes, measured on the same machine and CLI build minutes
  apart: the **shipped** mapped argv ran an out-of-scope `touch` with
  `permission_denials: []` **2 of 2**, the new one was **denied 3 of 3** with
  the refusal recorded, and an in-scope `git init` control still ran
  ([the record](measurements/0017-staged-agent-restores-layer-1.md)). It also
  shuts the wider half: a `SKILL.md` **committed to the repository under work**
  used to be invoked **3 of 3** by a mapped node whose prompt never mentioned
  skills, and a plugin enabled by that repository's own `.claude/settings.json`
  loaded into it; under the new argv the repository's copy fired **0 of 3**, and
  where the model did call `Skill` the CLI answered `Unknown skill: …` with
  `is_error: true`. The repository's project `CLAUDE.md` and its **hooks**
  arrive by the same default source list and are **implied, not measured**, in
  both directions.
  <br>**Only your own `~/.claude/agents` is scanned**, and the repository's
  `./.claude/agents` is not — since 2026-08-12, and this is the sharp edge of
  the change above. Because the matched definition is *copied* into the node,
  scanning the repository under work meant a file arriving with a `git clone`
  could write an unattended node's system prompt: measured, it did, **2 of 2**
  ([the record](measurements/0022-repo-planted-agent-and-the-agents-only-dir.md)).
  It never breached the tool ceiling — the class is injection, not escalation —
  but an agent you keep in a project checkout no longer maps. Move it to
  `~/.claude/agents`; the plan printout names the source file, size and hash of
  every definition it stages.
  <br>**What is not measured**: how many people keep agents in a project
  directory, so that cut was taken on the surface rather than on a number.
  ADR 0022 §7's directory-shape acceptance is no longer outstanding — this
  build's own argv resolved its agent from an `agents/`-only staged directory
  **3 of 3**, with the directory emptied as the control (exit 1,
  `--agent 'x' not found`), the out-of-scope command denied **0 of 3** and the
  in-scope control run **2 of 2**.
- **Isolation stops at the invocation repository.** `auto` provisions no
  managed worktree anywhere (`cwd:` and `worktree:` are both rejected at plan
  time), and a managed worktree — a hand-written-graph feature,
  [ADR 0005](adr/0005-worktree-provisioning-is-a-third-exec-seam.md) — always
  branches from the repository oh-my-graph was invoked from. A goal that names
  a *second* local repository gets no isolation there at all, so a node
  switching HEAD in a checkout some other process is standing in will collide
  with it. `auto` warns at plan time for the paths it can read
  ([SECURITY.md](../SECURITY.md)); that warning plus the node's own compliance
  is the whole protection.
  [ADR 0018](adr/0018-isolation-stays-scoped-to-the-invocation-repository.md)
  records why managed multi-repository worktrees are deferred, and the
  measurement that would convert that into a build. That measurement's
  **baseline** has since been taken (2026-08-09, 6 real `auto` runs, 18
  qualifying nodes): **0 of 6** nodes that moved a foreign checkout's HEAD
  isolated themselves first — #103's collision shape, six times out of six.
  The number is the status quo, not a verdict on the fix: it was taken before
  the §6 advisory clause the ADR proposes exists, which is what it is a
  baseline *for*
  ([the record](measurements/0018-unisolated-compliance-baseline.md)).
  ([#103](https://github.com/jitokim/oh-my-graph/issues/103))

See [Deferred](#deferred-not-implemented) below for the full out-of-scope list.

## Deferred (not implemented)

Called out honestly — these are **not** implemented as of v0.8.0:

- parallel-group sugar / any DSL beyond `depends_on`. (Retry is *not* on this
  list any more: a node's `retry` carries `max` **and** `on`, a per-cause
  filter over the closed cause set `nonzero_exit` / `run_error` / `timeout` /
  `output_error` / `budget_exceeded` / `verify_failed` / `result_mismatch`.)
- a terminal TUI — the shipped views are the `serve` web ones (the live view
  of one run, and the multi-run dashboard `serve` renders with no run id) and
  the plain-text `runs list` / `show` / `watch`.
- **sub-call / cross-node budget accounting.** Per-node budget is now enforced
  live (`--max-budget-usd` aborts a node mid-run) *and* post-hoc, so a runaway
  node no longer spends unbounded to the wall-clock timeout. Still deferred:
  catching the single in-flight call that overshoots before the abort lands
  (needs streaming cost via `--output-format stream-json`, a `NodeRunner`
  contract change) and any whole-graph budget across nodes. A wall-clock timeout
  derived from `budget_usd` was deliberately rejected — the $/minute rate would
  be invented, so it would look like a cap without being one.
