# Security & Terms-of-Service stance

oh-my-graph is a **personal, local** tool that re-uses **your own** logged-in
`claude` session. This document states, honestly, what that means and where the
line is.

## What oh-my-graph is

- A subprocess scheduler that runs each DAG node as `claude -p ...` on the
  machine you run it from, under the account **you** are already logged into.
- The same standing as running `claude -p` yourself, or as tools like
  [claude-squad](https://github.com/smtg-ai/claude-squad): it drives a CLI you
  have already authenticated.

## What oh-my-graph is NOT

- **Not** a hosted or redistributed product that authenticates other people via
  subscription OAuth. Doing that would violate Anthropic's Terms of Service.
- **Not** a shared service. It never runs as a daemon serving other users.
- It **never ships credentials**, **never proxies auth**, and **never** stores or
  transmits your tokens.

## Subscription-auth guarantees (enforced in code)

- **API-key scrub.** Every child process oh-my-graph spawns — a node
  subprocess, a `success_check.verify` command, the git commands behind a
  node's `worktree:` (a repo's own hooks may invoke claude), and the
  `open`/`xdg-open` launch of the `serve` URL (the URL handler it dispatches
  to is arbitrary user-configured code) — starts from your environment with
  `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` **deleted**. Those variables
  silently switch `claude` from your subscription (OAuth) to metered API
  billing. The scrub is asserted by a unit test at every call site
  (`internal/runner/claude_test.go`, `internal/verify/shell_test.go`,
  `internal/worktree/git_test.go`, `internal/browser/exec_test.go`) that sets
  both variables in the parent process and proves neither survives into the
  built child command.
- **Never `--bare`.** That flag disables OAuth; oh-my-graph never passes it.
- **Never the Agent SDK / API.** The node runtime is exclusively the `claude`
  CLI subprocess.

## Least privilege per node

- Each node declares its own `allowed_tools` and `permission_mode`. Grant only
  what a node needs.
- **`allowed_tools` is a declaration, not a sandbox.** It is passed to the CLI as
  `--allowedTools`, which is *unioned* with the permissions your own
  `~/.claude/settings.json` already grants — it can never shrink them. If your
  settings carry a standing grant like `Bash(*)` or `Write(*)`, a node has it
  regardless of what the graph declares. For hand-written graphs this is by
  design: the graph is your own reviewed artifact and your settings are the
  intended policy. Auto-planned graphs are the exception — see below, where
  `--setting-sources ""` turns the same declaration into a real limit.
- `permission_mode: bypassPermissions` is **opt-in per node** and prints a loud
  warning at load time. It is never a graph default. Parallel nodes that share a
  working directory should stay read-only (`permission_mode: plan`) to avoid
  racing edits.

## Auto-planned graphs (`oh-my-graph auto`)

A planned graph is untrusted LLM output executed unattended, so it gets bounds a
hand-written graph does not. Beyond the plan-time rejections (no
`bypassPermissions`, no `cwd`, no `success_check.verify`, no `agent`, no tool
outside a fixed allowlist), auto mode runs each planned node under a layered
execution ceiling.

`success_check.verify` is refused outright rather than constrained: it is a
shell command the *engine* runs, not a tool call, so no permission mode, tool
allowlist, deny list or `cwd` restriction applies to it. It is available to
hand-written graphs, which are your own reviewed artifact, and to nothing else.

The layers:

| layer | mechanism | closes |
|---|---|---|
| 0 declaration | `coordinator.plannedToolAllowlist` | what a plan may name at all — plan time, before any node runs |
| 1 isolation | `--setting-sources ""` | your standing grants; settings hooks |
| 2 grant | `--allowedTools` under `dontAsk` default-deny | **scoped Bash** |
| 3 narrowing | `--tools "<names declared>"` | tools the model can attempt at all |
| 4 MCP | `--strict-mcp-config`, no `--mcp-config` | `mcp__<server>__<tool>` |
| 5 residual | `--disallowedTools` | anything the layers above got wrong |

Layer 0 is the only plan-time layer: it is the fixed allowlist above, enforced
by `validatePlannedNodeTools` before anything runs, so a plan naming `Bash`,
`Bash(*)` or an unrestricted `WebFetch` never becomes a graph. Layers 1–5 then
bound what the surviving declaration is worth at run time.

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
**closed for auto-planned nodes.** It remains accurate for hand-written graphs,
which run without layer 1's isolation: their declared `allowed_tools` is still
rendered as `--allowedTools` (layer 2 applies to every graph), but layers 1 and
3–5 are auto mode's alone by design.

Still a reduction, not a sandbox. What is **not** covered:

- **MCP closure is unverified.** `--strict-mcp-config` is passed because
  oh-my-graph never passes `--mcp-config`, so the flag costs nothing — but this
  was not measured against a real MCP server (DESIGN.md, E5). Do not read Layer
  4 as an observed guarantee.
- **Skill and slash-command surfaces** are still not enumerable by any of these
  mechanisms.
- **Enterprise policy settings are never dropped** by `--setting-sources ""` —
  which is deliberate: this cannot be used to step around a corporate policy.
  Conversely, on a machine with `allowManagedPermissionRulesOnly`,
  `--allowedTools` rules are ignored entirely and the ceiling is the managed
  policy, not ours.
- The ceiling rests on behaviour of a **specific CLI version**. A future claude
  release could change it; Layer 5 is retained precisely so a wrong assumption
  in Layers 1–4 degrades to the older, weaker ceiling rather than to nothing.

**Planned nodes are more isolated and less capable than they used to be.**
Dropping your settings also drops your CLAUDE.md, your hooks and your configured
MCP servers for those nodes. That is the intended direction, but it is a real
behaviour change: if your `auto` runs depended on an MCP server, they will stop.

Re-running a saved `graph.json` through `oh-my-graph run` drops the ceiling
entirely — that path assumes you reviewed the file. Treat `auto` as you would
any unattended agent: run it in a directory you are willing to have modified.

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
  stays `'self'` with no `'unsafe-eval'`. A cytoscape bump must re-verify the
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

A node's full prompt is passed to `claude` as an argv element (`-p <prompt>`, in
`internal/runner/claude.go`), so for the lifetime of that subprocess it is
readable from the process table — `ps auxww`, and on Linux
`/proc/<pid>/cmdline`, which is world-readable unless the machine sets `hidepid`.
Any process running as you can read it on any platform.

This is not an incidental leak of a short string. A prompt carries its
`{{ inputs.* }}` values, and because `| inline` is used pervasively across the
shipped graphs and fragments, it carries the **inlined content of upstream
artifacts** — that is, the text of earlier nodes' replies.

**Known, and not currently fixed.** The fix would be to feed the prompt on
stdin, and that is a change to the most lifecycle-sensitive seam in the repo:
`ClaudeCLIRunner` owns `waitDelay`, process-group kill and `--output-format
json` parsing, writing to a child's stdin adds a deadlock surface, and the
interaction with `--resume` is unmeasured. It would need a real `make smoke`
measurement against a live CLI before anyone should believe it. Until then it is
documented rather than claimed closed. On a machine where you do not trust the
other local users, treat a running node's prompt as visible to them.

## Reporting

This is a young project. If you find a security issue, please open an issue
describing it (omit any secrets) so it can be triaged in the open.
