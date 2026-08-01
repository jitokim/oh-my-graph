# Limitations & platform notes

Detail moved out of the README: the full platform-support notes, the honest
gaps in v0.1, and what is deliberately deferred.

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
  as case-insensitive, but
  [the scrub](../README.md#bring-your-own-login) matches keys
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
- **A `gate` always pauses a fresh run.** Gate nodes are implemented (pause /
  approve / reject, continued by `oh-my-graph resume`), but a fresh `run`/`auto`
  cannot pre-approve one: every gate stops the run with a resumable snapshot and
  exit code 2, and decisions are only supplied on resume.
  ([#9](https://github.com/jitokim/oh-my-graph/issues/9))
- **Auto mode's tool ceiling is a reduction, not a sandbox — and parts of it are
  unverified.** The isolation and scoped-Bash layers were measured against a
  real `claude` 2.1.220 and hold (see [SECURITY.md](../SECURITY.md)). MCP closure
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
- **coordinator auto-mapping of `agent:` by role.** Having `auto` scan your
  `~/.claude/agents` and assign a reviewer node your `code-reviewer` sounds like
  the natural next step; it is deferred on a design constraint, not on effort.
  A planned node may not carry `agent:` at all (it would route around the tool
  ceiling), and settings-source isolation disables agent discovery anyway, so
  the two features are mutually exclusive as built. An implicit scan is also
  rejected permanently: it would make an `auto` run's behaviour depend on files
  you forgot you had. See `docs/adr/0004-*.md` §4.
