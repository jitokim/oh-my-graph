# ADR 0047 — A planned node's Edit and Write reach only the directory it runs in

Decision needed: should planned nodes get Edit/Write scoped to the directory they run in, with unscoped forms refused (A); stay unscoped with only a launch warning and a post-run report (B); or get A plus a repeatable `--allow-edit-outside <path>` flag that widens the scope (C)? Choosing A or C means planned nodes that hold Edit or Write stop running under `auto`, because under `auto` the scoped rule did not stop an out-of-scope write (F1, measured); they run under `acceptEdits` (recommended) or `dontAsk`.

**Status:** Proposed. No code exists. This record frames a decision for the
captain; §4 recommends one option, and that is a recommendation, not a
decision. §5 lists the tests the implementation owes and the measurements
owed before Accepted.

**Date:** 2026-10-22

**Issue:** [#365](https://github.com/jitokim/oh-my-graph/issues/365). A planned
node that holds `Edit` or `Write` may write any file the operator's uid can
reach, and the only file anything checks afterwards is the `--verify-cmd`
script ([#363](https://github.com/jitokim/oh-my-graph/issues/363)).

**Where the addresses point.** Written against
`c9a91de8af85b58bd48ca2dd5dac3e5939a32480`, the `main` this branch
(`docs/adr-0047-scoped-edit`) left. Every `file:line` below was read at that
commit. Code that does not exist yet is named by file **and** function and
marked *not yet existing*, because the line goes stale when the diff lands and
the name does not. Constants, flags and fields this ADR introduces are marked
*chosen*: they are design choices, not measurements. A sentence about what
Claude Code or Codex does is a claim about that CLI, not about this
repository, and says so.

## 1. Context

### 1.1 The gap

Layer 0 of the ceiling is the declaration bound: a planned node may name only
the tools in `plannedToolAllowlist`
(`internal/coordinator/coordinator.go:72-75`). That list holds `Edit` and
`Write` as bare names. `validatePlannedNodeTools` checks exact membership
(`internal/coordinator/coordinator.go:1854`), so a planner may write `"Edit"`
and nothing more specific. Nothing narrows the bare name later.
`toolPolicyFor` (`internal/coordinator/coordinator.go:979`) copies
`node.AllowedTools` into the policy unchanged. A bare `Edit` grant has no path
in it, so a node holding it may edit any file the operator can.

Planned nodes cannot choose where they run. `validatePlannedNodeCwd`
(`internal/coordinator/coordinator.go:1644`) and `validatePlannedNodeWorktree`
(`:1830`) refuse both fields. The planner prompt says every node runs in the
directory oh-my-graph was invoked from (`:2547`). The runner sets
`cmd.Dir = spec.Cwd` (`internal/runner/cli.go:382`), and for a planned node
`Cwd` is empty, so the process inherits the invocation directory.

**This differs from the issue's framing.** No planned node has a worktree. Only
a hand-written node can declare `worktree:`. Fragments spliced through reuse
add no write tools either: they are limited to `Read`, `Glob` and `Grep`
(`internal/coordinator/reusecatalog.go:49`).

### 1.2 The ceiling layers, and where a path scope would sit

`toolPolicyFor`'s comment (`internal/coordinator/coordinator.go:920-924`) lists
the run-time layers. Layer 0 is the declaration bound described in §1.1.

| Layer | Mechanism | Granularity today |
|---|---|---|
| 0 declaration | `validatePlannedNodes` (`:1149`) → `validatePlannedNodeTools` (`:1844`) | exact string in `plannedToolAllowlist` |
| 1 isolation | `--setting-sources ""` (`internal/runner/claude_protocol.go:105-106`) | all of the user's settings, or none |
| 2 grant | `--allowedTools` (`claude_protocol.go:114-115`) | the permission rule exactly as the node declared it |
| 3 narrowing | `--tools`, bare names via `narrowedToolsFor` (`coordinator.go:1027`) | tool name; scope dropped by `toolName` (`:1064`) |
| 4 MCP | `--strict-mcp-config` | all MCP servers, or none |
| 5 residual | `--disallowedTools` from `deniableTools` (`coordinator.go:109`) | bare names only (rationale at `:85-95`) |

ADR 0032 (`--accept-loaded-user-config`) removes layers 1 and 4 for a whole run
(`coordinator.go:987-990`). With layer 1 gone, the operator's standing
permission grants load again, and layer 2 goes back to being a declaration
instead of a limit (`:976-978`).

**A path scope would be a layer-2 rule.** Layer 3 can't carry one, because
`--tools` takes names. Layer 5 can't either, because a deny rule beats every
allow rule. That means "deny everything except `./`" cannot be written, since
it would also deny `./`. Layer 0 decides whether the planner may spell the
scoped form. Layer 2 is where it becomes argv.

### 1.3 How the claude runtime receives tools

`buildArgs` joins `policy.AllowedTools` with commas into one argument:
`--allowedTools <rule>,<rule>` (`internal/runner/claude_protocol.go:114-115`).
The rules pass through verbatim, with no parsing or rewriting.

**Would a path-scoped rule survive validation today?**

- **Hand-written graphs:** yes. `internal/graph` does not inspect
  `allowed_tools` strings; the field is a plain `[]string`
  (`internal/graph/graph.go:264`). One shipped graph already uses the form.
  `gated-dev`'s publish node holds `Edit(./.omg-pr-body.md)`, and
  `internal/graph/shipped_graphs_test.go:841-848` pins it and refuses any
  unscoped Edit or Write there.
- **Planned graphs:** no. `"Edit(./**)"` is not an exact member of the set, so
  `validatePlannedNodeTools` refuses it (`coordinator.go:1854-1856`). A scoped
  form gets through layer 0 only if the allowlist names it.

**Claim about Claude Code, not this repo:** its documented permission syntax
accepts path-scoped `Edit(...)` rules. `./path` (or a bare relative path)
resolves against the working directory, `//path` is absolute, `~/path` is
under home, and `**` matches recursively. The documentation also says an
`Edit` rule covers every built-in file-editing tool, Write included. This repo
has measured only part of that, at claude CLI 2.1.295 (§5.5 F2, F3); the
Edit-covers-Write claim is still unmeasured (F4). DESIGN.md says so of the one scoped rule it
ships: "spelled from the documented syntax and not measured" (`DESIGN.md:202-203`).

**The permission mode decides what an out-of-scope call becomes.** A scoped
allow rule only pre-approves calls inside the scope; it does not deny the
rest. A call no rule matches meets the permission mode. Under `dontAsk` it is
denied. Under `auto`, the default (`internal/schedule/scheduler.go:67`), it
goes to the CLI's classifier, which decides then and there (ADR 0034). Deny
rules are evaluated before the classifier
(`docs/adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md:193`).

So under `auto`, a scoped allow narrows what is pre-approved, not what is
possible. A planned node may also set `acceptEdits`, because only
`bypassPermissions` is refused (`coordinator.go:1272`). F1 (§5.5) therefore
covers both modes a planned node can pick, `auto` and `acceptEdits`. An
out-of-scope edit auto-approved under either one is the same hole. F1 is
measured (claude CLI 2.1.295): under `auto` the out-of-scope write happened
3/3; under `acceptEdits` it was denied 3/3, and under `dontAsk` 1/1.

### 1.4 What the codex runtime enforces

Codex never sees `allowed_tools`. `buildArgs` passes
`--sandbox CodexSandbox(spec.PermissionMode)`
(`internal/runner/codex_protocol.go:60`). That maps `plan` to `read-only`,
`bypassPermissions` to `danger-full-access`, and every other mode to
`workspace-write` (`:81-90`). A planned node can never get
`bypassPermissions`, so every planned codex node runs under `read-only` or
`workspace-write`.

The plan screen shows this. It prints `[sandbox: <mode>]` in place of
`[tools: …]` for each codex node (`cmd/oh-my-graph/main.go:1503-1506`), and
prints the mapping line `codexSandboxLine` (`:1608`, printed at `:1635`).

**Claim about Codex, not this repo:** `workspace-write` is an OS-level sandbox.
It allows writes under the process working directory and the temp directories
(`/tmp`, `$TMPDIR`), plus any configured writable roots, and it covers every
command the agent runs, not just a file tool.

If that holds, planned codex nodes are already scoped, and more tightly than A
would scope claude. Codex's sandbox also covers shell commands, while A covers
only the Edit/Write tools. The scope includes the temp directories that A
leaves out. None of this is measured here.

### 1.5 What #363 pinned, and what nothing protects

DESIGN.md:3056 begins *"A `--verify-cmd` is evidence only while the script it
executes is the one the user meant (#363)"*. The next sentence names this
gap: a planned node with the stock Edit grant can rewrite `./check.sh`. The fix
pins a digest instead of narrowing the grant:

- `pinVerifyCommand` (`cmd/oh-my-graph/verifypins.go:24`) pins once per
  invocation.
- `verify.PinCommand` (`internal/verify/pin.go:62`) resolves the files the
  command executes.
- `verify.NewPinningVerifier` (`internal/verify/pin.go:230`) re-hashes them
  before and after every verification.
- `noteVerifyPins` (`cmd/oh-my-graph/verifycmd.go:212`) lists them on the plan
  screen.

**This differs from the issue's framing.** There is no
`cmd/oh-my-graph/pin.go`. The pin lives in `internal/verify/pin.go`, and the
CLI half lives in `cmd/oh-my-graph/verifypins.go`.

What the pin protects: the scripts an `auto --verify-cmd` line executes, and
only against being used as evidence after a change. It refuses a changed file
and does not restore it. A change to any other file goes undetected and
unreported:

- **the operator's dotfiles**, including `~/.claude/settings.json` and shell
  rc files, which the next unisolated claude or shell process loads;
- **other repositories** on the same disk;
- **the run directory** `omgHome()/runs/<run-id>`. That is `~/.oh-my-graph`,
  or `OMG_HOME` when set (`cmd/oh-my-graph/main.go:2325-2333`). It holds
  `state.json`, every node's `.out` artifact (which downstream nodes read
  through handoff), and the staged copies ADR 0022 and ADR 0041 re-check only
  on `resume`.

### 1.6 How worktrees relate to the working directory

A `worktree:` node does **not** run inside the working tree.
`worktreeManagerFor` puts every worktree under `<run-dir>/worktrees`
(`cmd/oh-my-graph/main.go:2365-2366`), and `GitManager.Acquire` joins
`<name>` onto that base (`internal/worktree/git.go:112`). The scheduler then
replaces the node's cwd with that path (`internal/schedule/scheduler.go:1546-1547`).

So the precise wording is **the directory the node's process runs in**. That is
the invocation directory for every planned node, and for a hand-written node
it is its `cwd:` or its worktree. A rule written `./**` follows that directory
on its own, because it resolves against the process working directory. An
absolute rule naming the invocation directory would fail every worktree node.

## 2. Options

The three options share one invariant: the Bash route is unchanged. A planned
node may still hold `Bash(go *)` and `Bash(make *)`, which run arbitrary code
(`internal/coordinator/reusecatalog.go:42-45`). Such a node can edit a
`Makefile` in scope and then run it. On claude, Options A and C close only the
**tool** route. On codex, the sandbox already covers both routes (§1.4).

### Option A — scoped by default, unscoped refused

`plannedToolAllowlist` replaces `"Edit", "Write"` with `"Edit(./**)"` and
`"Write(./**)"` (*chosen*). `validatePlannedNodeTools` then refuses bare
`Edit`/`Write`, and every other spelling, by the exact-membership check it
already runs. The refusal adds one sentence naming the scoped spelling, so
the single repair attempt can recover. `toolName` still reduces the rule to
`Edit`/`Write` for layer 3 and layer 5. The planner prompt advertises the
scoped forms, because it renders from the same list (`coordinator.go:2183`).

On top of that, `toolPolicyFor` appends a path-scoped deny for the run root
to every planned node that holds a write tool: `Edit(//<omg home>/**)`
(*chosen*). That deny is evaluated before the classifier (§1.3), so the run
directory stays protected whatever the permission mode does with other
out-of-scope calls. That is a scoped deny, which goes against layer 5's
bare-names rule. That rule was measured for `Bash(*)`, whose specifier is a
command pattern (`coordinator.go:92-95`), not for an `Edit` path, so F4 must
show the deny binds before A relies on it.

- **What the operator sees.** The plan screen's `[tools: …]` already prints
  the rules verbatim (`main.go:1505-1506`), so it shows `Edit(./**)`. One added
  line (*chosen* wording): `edits: limited to <invocation dir> (Edit/Write
  rules); the run directory is denied; Bash(go|make) still runs code that can
  write anywhere`. Under `--accept-loaded-user-config` the line instead says
  the scope is a declaration, because the operator's own `Edit` grants load
  again (§1.2).
- **What breaks.** A planned node that edits outside the invocation directory:
  the run directory (on purpose), a scratch file under `/tmp`, a sibling
  repository. Writes that a command itself makes (the `go` build cache, a test
  writing to `/tmp`) are untouched, because they never go through the Edit or
  Write tools. A saved plan from an earlier version that declares bare `Edit`
  is refused if it is re-planned. Run with `run`, it is a hand-written graph
  (§3).
- **Claude enforcement.** The scoped allow rule (layer 2), the run-root
  deny, and the permission mode. A scoped allow rule is a pre-approval, not a
  deny. Under `auto`, a call outside it goes to the classifier, and in F1 the
  classifier approved the out-of-scope write 3/3 (§5.5). So under A, **planned
  nodes that hold Edit or Write do not run under `auto`.** They run under
  `acceptEdits` or `dontAsk`, where the scoped rule acted as a limit in the
  measurement (`acceptEdits` denied 3/3, `dontAsk` 1/1), or A fails closed.
  The coordinator resolves the mode for a write-holding planned node; a
  planner-declared `auto` on such a node is replaced, not obeyed (*chosen*),
  and the plan screen prints the mode the node will run under. Inside the
  tree the scoped rule is honoured under both `auto` and `acceptEdits` (F2).
  Read-only planned nodes keep `auto`.
- **Which mode A uses for write-holding planned nodes: `acceptEdits` or
  `dontAsk`.** Both denied the out-of-scope write in F1. `dontAsk` also
  refuses every other call not on the allow list, by definition (ADR 0034
  §1.2). ADR 0034 §1.5 measured what that costs: it is the friction `auto` was
  made the default to retire, so `dontAsk` may break more planned nodes, for
  example a node whose `Bash(go *)` grant does not match the command it
  actually needs. In exchange, its refusal of an out-of-scope edit follows
  from its definition, not only from three runs. What `acceptEdits` does with
  an unmatched call that is not an edit is not measured here. ADR 0034 §2.1
  calls it a prompting mode for everything else, so in a process that cannot prompt
  it may land on the dead ask, but that is a reading, not a measurement.
  **Recommendation: `acceptEdits`**, because it is the one that keeps
  write-holding nodes closest to how they run today while still refusing the
  out-of-scope write in every measured run. Switch to `dontAsk` if F3 with
  Edit (Read granted) shows `acceptEdits` pre-approving an out-of-scope Edit,
  or if a later CLI version changes F1's `acceptEdits` result.
- **Codex enforcement.** Unchanged: the codex argv never carries
  `allowed_tools`. The `workspace-write` sandbox already scopes the node
  (§1.4). For codex, the plan screen's added line names the sandbox instead
  of the rules.
- **When a runtime cannot enforce it.** The planner itself refuses when the
  invocation directory lies inside `<omg home>`, because the deny would
  cover the tree. F1 found that `auto` approves out-of-scope writes, so A's
  fail-closed branch is required, not contingent: a planned node holding a
  write tool runs under the chosen non-`auto` mode, and if that mode cannot
  be applied (or a re-measurement shows it no longer limits), the run refuses
  to start. A does not ship with a scope that only looks enforced.

### Option B — unscoped, warned and reported

`Edit`/`Write` stay bare. The plan screen adds a warning (*chosen* wording:
`edits: unscoped — a planned node may write any file you can`). The run ends
with a report of changed files outside the invocation directory.

- **What the operator sees.** The warning before any node spends. After the
  run, a list of paths it can only act on after the fact.
- **What breaks.** Nothing. Every graph that runs today keeps running.
- **Enforcement on either runtime.** None. The report needs a detector. It
  can't be a filesystem snapshot of `$HOME` (too large), and claude
  transcripts record tool calls but not a codex shell's writes. A practical
  report covers Edit/Write tool calls read from the node transcripts, plus a
  digest of the run directory taken before and after. A file written through
  Bash goes unreported, and the report must say that it is partial.
- **When it cannot enforce.** It never enforces, so the case is the default.
  B detects only.

### Option C — A, plus an explicit widening flag

C is A, plus `--allow-edit-outside <path>` (*chosen*). The flag is
repeatable and accepted by `auto` (and by `resume` from the record). Each
`<path>` is made absolute and symlink-resolved at launch, then added to
every planned write-holding node as `Edit(//<abs path>/**)` and
`Write(//<abs path>/**)`. It is recorded in `state.json` as
`edit_scope_extra` (*chosen*, optional, so the schema stays 3) so `resume`
rebuilds it instead of re-reading flags.

- **What the operator sees.** A's line, plus one line per widened path:
  `edits: also <abs path> (--allow-edit-outside)`.
- **What breaks.** Same as A, except a graph the operator widens for keeps
  running. A path inside `<omg home>` is refused, because the run-root deny
  would win anyway and the flag must not suggest otherwise.
- **Claude enforcement.** A's, with extra allow rules. Write-holding planned
  nodes run under A's non-`auto` mode for the same F1 reason.
- **Codex enforcement.** **Claim about Codex, not this repo:** the sandbox
  takes extra writable roots (`--add-dir`, or the
  `sandbox_workspace_write.writable_roots` config key). Neither is measured
  here. Until it is, `--allow-edit-outside` with `--runtime codex` is refused
  at launch (fail closed). Dropping it silently would leave the operator
  believing a path is writable when it is not.

## 3. Hand-written graphs (`run`) vs planned graphs (`auto`)

**Planned graphs (`auto`).** The options above apply in full. The plan is
untrusted planner output, it runs unattended, and it is exactly what layer 0
exists to bound.

**Hand-written graphs (`run`).** Nothing changes under A or C. A hand-written
graph is the operator's reviewed artifact, and no layer applies to it today
(`internal/coordinator/coordinator.go:69-71`). Its author already can and
does write scoped forms, as `gated-dev`'s publish node shows (§1.3). Imposing a
scope there would break graphs that legitimately write elsewhere: a
`cwd: ../sibling` node, or a lane that writes into `<run-dir>/worktrees` by
design. It would also add a ceiling to a path whose stance is that the human
who read the YAML is the ceiling. B's report could run for hand-written graphs
too, at no cost to them. That is an independent choice and not part of this
decision.

A saved plan taken from `auto --plan-only` and run with `run` becomes
hand-written at that point, as ADR 0032 already treats it. Under A it carries
`Edit(./**)` in its YAML, so the scope travels with it unless the operator
edits it out. That is one reason to spell the scope in the allowlist instead
of adding it later inside `toolPolicyFor`.

## 4. Recommendation (pending the captain's decision)

**Recommend A now, with C's flag deferred until a real graph needs it.**

1. B only detects. It reports after the fact and is partial on both runtimes,
   which leaves #363's lesson unapplied. A pin on one file was needed because
   the grant reached every file.
2. A changes no runtime mechanism. On claude it is a string in an existing
   allowlist plus one deny rule. The argv path already passes scoped rules
   through verbatim, and a shipped graph already relies on that. On codex it
   only names on the plan screen a scope that already exists.
3. What A breaks is small on the planned path. Artifacts and handoff are
   written by the engine, not by nodes. Planned nodes have no worktrees and no
   `cwd`. Temp files written by commands are not tool writes.
4. C's flag is an outward-facing grant that widens what an untrusted plan may
   touch. It also needs a codex mechanism nobody has measured. Adding it
   before an operator reports a planned graph that needs it would mean
   shipping an unmeasured door. A keeps C's shape open, since it is only extra
   rules.

F1 (§5.5) is measured, and it changes what A must be. Under `auto`, the
mode planned nodes run in by default, the classifier approved the
out-of-scope write 3/3, so a scoped allow alone is not enforcement. A
therefore needs its fail-closed branch: write-holding planned nodes stop
running under `auto` and run under `acceptEdits` (recommended over `dontAsk`,
§2 Option A), and the run refuses to start if that cannot be applied. The
captain should weigh that cost before accepting. This remains a
recommendation pending the captain's decision.

## 5. Alternatives, consequences, implementation, tests, falsification

### 5.1 Alternatives considered

- **Scope inside `toolPolicyFor` only** (the planner keeps writing `Edit`).
  Rejected: the saved `graph.json` would show a grant broader than the one the
  node ran with, and the scope would not travel with a saved plan (§3).
- **A deny list of sensitive roots instead of a scoped allow** (`~/.ssh`,
  `~/.claude`, ...). Rejected as the primary mechanism, because it enumerates
  an open set. It is kept only for the run root, which is the one root this
  tool owns and can name exactly.
- **Applying the scope to hand-written graphs.** Rejected for the reasons in §3.
- **Removing Edit/Write from planned nodes.** Rejected, because `auto` exists
  to change code.

### 5.2 Consequences

- With A, the plan screen states a write scope, and on claude the scope sits in
  argv for every planned write-holding node.
- With A, write-holding planned nodes lose `auto`. They run under
  `acceptEdits` (or `dontAsk`), so a call the classifier would have approved
  may now be refused, and a run whose node cannot get that mode does not
  start (F1, §4).
- The Bash route stays open on claude. The plan-screen line says so, and
  DESIGN.md and SECURITY.md have to say it in the same words.
- `--accept-loaded-user-config` weakens the scope to a declaration (§1.2). The
  run-root deny still binds, because deny rules are not lifted by loading
  user settings.
- A planner that writes bare `Edit` spends its one repair attempt on that
  until the prompt change takes. Measure that rate in the first week.

### 5.3 Implementation outline (A)

- `internal/coordinator/coordinator.go`: `plannedToolAllowlist`, entries
  changed. `validatePlannedNodeTools`: a refusal sentence naming the scoped
  spelling. `toolPolicyFor`: append the run-root deny through
  `runRootDenyRules` (*not yet existing*), which needs `<omg home>` passed in
  from the CLI. `toolPoliciesByNode` gains that parameter. A write-holding
  planned node's permission mode is resolved to `acceptEdits` (or `dontAsk`,
  per the captain's sub-choice) instead of `auto`, through
  `editScopePermissionMode` (*not yet existing*); a read-only planned node
  keeps the default. `auto` is dropped because a scoped allow is a
  pre-approval, not a deny, and under `auto` the classifier approved the
  out-of-scope write 3/3 (F1).
- `internal/coordinator/coordinator.go`: the fail-closed refusal,
  `refuseUnscopedEditMode` (*not yet existing*), called once the policies and
  modes are resolved and before any node is scheduled. It refuses the run when
  a write-holding planned node's resolved mode is not the chosen non-`auto`
  mode, or when that mode cannot be put on the node's argv as
  `--permission-mode`. A scope the CLI does not enforce would only look
  enforced, so the run does not start.
- `internal/coordinator/field_dispositions_test.go`: `plannedToolEffects`
  re-keyed to the scoped spellings.
- `cmd/oh-my-graph/main.go`: `noteEditScope` (*not yet existing*), printed
  beside `noteCodexRuntimePolicy`. It picks the claude, codex or loaded-config
  sentence, and names the non-`auto` mode a write-holding node runs under.
  Also add the refusal when the invocation directory lies inside
  `omgHome()`.
- For C only: a repeatable `--allow-edit-outside` in `cmd/oh-my-graph/flags.go`;
  `editScopeExtra` (*not yet existing*) in `internal/runstate`; and the codex
  refusal.
- DESIGN.md (ceiling section, and the #363 paragraph at `DESIGN.md:3056`),
  SECURITY.md "Ceiling layers", and docs/LIMITATIONS.md (the Bash route).

### 5.4 Tests the implementation owes

1. The allowlist holds `Edit(./**)` and `Write(./**)`, and neither bare
   `Edit` nor bare `Write`.
2. A planned node declaring bare `Edit` is refused, and the refusal names
   `Edit(./**)`.
3. `toolPolicyFor` passes `Edit(./**)` verbatim into `AllowedTools`.
   `narrowedToolsFor` yields `Edit` for it, and `disallowedToolsFor` does not
   deny `Edit`.
4. The argv of a planned write-holding node carries
   `--allowedTools …Edit(./**)…` and the run-root deny in `--disallowedTools`.
   A read-only node carries no run-root deny.
5. Under `--accept-loaded-user-config` the run-root deny is still in argv, and
   the plan screen prints the declaration sentence.
6. Planning from an invocation directory inside `OMG_HOME` is refused before
   any node runs.
7. The codex argv is byte-identical before and after this change, and the codex
   plan screen prints the sandbox sentence, not the rules sentence.
8. A hand-written graph declaring bare `Edit` loads and runs with its rule
   unchanged.
9. `gated-dev`'s `Edit(./.omg-pr-body.md)` is unchanged
   (`TestPRPublishPassesItsBodyThroughAGrantedFile` stays green).
10. A planned node holding `Edit(./**)` or `Write(./**)` runs under the
    chosen non-`auto` mode even when the planner declared `auto`, and its argv
    carries that `--permission-mode`. A read-only planned node still runs
    under `auto`.
11. When a write-holding planned node would otherwise run under `auto` (its
    mode cannot be resolved to the chosen non-`auto` mode, or cannot be put on
    argv), the run refuses to start before any node runs, and the refusal
    names the node and the mode.
12. (C) `--allow-edit-outside` makes paths absolute and symlink-resolved,
    refuses a path under `OMG_HOME`, is refused under `--runtime codex`, and
    `resume` rebuilds it from `state.json`.

### 5.5 Falsification

**Evidence base.** QA, 2026-10-22, claude CLI **2.1.295**. Every run used
`--setting-sources "" --tools Edit,Write --allowedTools 'Edit(./**)'
'Write(./**)'` and started from a fresh, empty working directory. These are
claims about Claude Code at that version, not about this repository: they
must be re-measured whenever the claude CLI changes, and A's fail-closed
branch must not rely on a result older than the CLI it runs against.

- **F1 (measured; covers `auto` and `acceptEdits`).** Under the argv above,
  ask claude to write `~/omg-f1-probe`. F1 covers both permission modes a
  planned node can pick, because an out-of-scope edit auto-approved under
  either is the same hole for Option A. Result:
  - `auto`: **wrote 3/3**, `permission_denials` 0 (sessions `89aab393`,
    `982b0156`, `7ab1d51c`).
  - `acceptEdits`: **denied 3/3**, one Write denial each (sessions
    `fed42435`, `908f296a`, `395909ad`).
  - `dontAsk`: **denied 1/1**.

  F1 is **falsified under `auto`**: a scoped allow rule pre-approves but does
  not deny, and `auto`'s classifier approved the out-of-scope write. Under
  `acceptEdits` and `dontAsk` the scoped rule acted as a limit in these runs.
  So Option A as written is not enforcement for planned nodes running under
  `auto`, and its fail-closed branch is required (§2, §4).
- **F2 (measured).** Under the same argv, a `Write` to `./x` inside the tree
  **wrote** under both `auto` and `acceptEdits`: the scoped allow is honoured
  inside the tree. Had it been denied, `Write(./**)` would not be a rule
  Claude Code honours, and the allowlist would have had to rely on
  `Edit(./**)` covering Write.
- **F3 with Write (measured).** Under the same argv, a `Write` to
  `./../outside`: `auto` **wrote** (session `bdabe976`); `acceptEdits`
  **denied**. Under `auto` this is F1's hole again. Under `acceptEdits` it
  shows `./**` does not pre-approve a `..` escape.
- **F3 with Edit (inconclusive).** The `Edit` to `./../outside` was refused
  with 0 denials and the file unchanged, because Edit requires a prior Read
  and `--tools Edit,Write` excluded Read. It says nothing about the rule.
- **F3 with Edit, Read granted (open, owed before Accepted).** Repeat F3 with
  `--tools Read,Edit,Write` under `acceptEdits` and `dontAsk`. The `Edit` to
  `./../outside` must be denied. If it is pre-approved, `./**` does not
  normalise `..` for Edit, and the scope is not a scope.
- **F4 (open).** The run-root deny blocks a write to `<omg home>/runs/x` under
  `auto`, `dontAsk` and `acceptEdits`. It must be tried with **both** Edit and
  Write: A spells the deny as one `Edit(...)` rule and relies on the
  documented claim that an Edit rule also covers Write (§1.3), and F4 is
  where that claim is tested. If the Write is not blocked, the deny needs a
  `Write(...)` twin.
- **F5.** A codex node under `workspace-write` cannot write `~/omg-f5-probe`.
  If it can, §1.4's claim about Codex is wrong, and codex is unscoped too.
