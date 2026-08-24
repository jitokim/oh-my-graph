# 0034 — what `--permission-mode auto` does, read out of the shipped binary

**This measurement ran no `claude`.** Every line below was extracted from the
bytes of the installed CLI with `strings` and read. Nothing here is an
observation of a running node, and nothing here is quoted from documentation.
It is the evidence base for
[ADR 0034](../adr/0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md),
which changed the scheduler's default permission mode from `dontAsk` to `auto`.

It exists as a file because the extract it was originally read from
(`/tmp/permauto-evidence/claude-2.1.241.strings.txt`) is a session-lived path,
and an ADR is not. The 44 lines the ADR leans on are pinned here.

## The binary

| | |
| --- | --- |
| resolved from `PATH` | `/usr/local/bin/claude` → `../lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe` (`ls -la /usr/local/bin/claude`) |
| the file read | `/usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe` |
| size | 333,784,816 bytes |
| link count | 2 — the same inode as `…/claude-code/node_modules/@anthropic-ai/claude-code-darwin-x64/claude` |
| SHA-256 | `cf01b8cace66485ef5b476f14d96f69af61194a38c3df8412a80eb8f1316c10d` (`shasum -a 256 <path>`) |
| version | **2.1.241** — `/usr/local/lib/node_modules/@anthropic-ai/claude-code/package.json:3` |
| platform | macOS (darwin-x64), 2026-08-24 |

## How to reproduce

```sh
strings -n 6 /usr/local/lib/node_modules/@anthropic-ai/claude-code/bin/claude.exe \
  > claude-2.1.241.strings.txt          # 487,756 lines on the run recorded here
```

**Cite the text, not the line number.** The `L:` numbers in the tables below are
this extract's, and they depend on the `strings` implementation and its `-n`
argument as much as on the binary. Every row also carries the string verbatim,
which is what survives: `grep -n '<the string>'` re-finds it in any extract of
the same binary. A different `claude` build may not contain the string at all,
which is the finding to report rather than a line number that moved.

## A. The mode set, in the CLI's own words

One string enumerates all six modes, which is what makes the `dontAsk` / `auto`
difference readable rather than inferred.

| L | string |
| --- | --- |
| 277871 | `Permission mode for controlling how tool executions are handled. 'default' - Standard behavior, prompts for dangerous operations. 'acceptEdits' - Auto-accept file edit operations. 'bypassPermissions' - Bypass all permission checks (requires allowDangerouslySkipPermissions). 'plan' - Planning mode, no actual tool execution. 'dontAsk' - Don't prompt for permissions, deny if not pre-approved. 'auto' - Use a model classifier to approve/deny permission prompts.` |

**The whole of the difference:** `dontAsk` denies what is not pre-approved;
`auto` puts what is not pre-approved to a model classifier, which approves **or
denies** it. `auto` adds an allow path that `dontAsk` does not have. It does not
remove the deny path.

Two members of oh-my-graph's closed set (`internal/graph/graph.go:84-95`) are
not in that enumeration, and one of them resolves here:

| L | string |
| --- | --- |
| 138343 | `Default permission mode when Claude Code needs access ('manual' is accepted as an alias for 'default')` |

So `manual` **is** `default`, and `default` is `277871`'s *"Standard behavior,
prompts for dangerous operations"* — a prompting mode, which in a process with
no TTY lands on §B's `171823`. The set oh-my-graph enumerates was taken from
`claude --help` at 2.1.221 and carries `manual` but not `default`; the string
above is the mapping between the two vocabularies.

## B. Non-TTY: does an unapproved call block?

The load-bearing question — a node is a headless subprocess with no terminal to
answer a prompt on. Three independent strings say the CLI does not wait.

| L | string | what it rules out |
| --- | --- | --- |
| 171823 | `Action requires interactive approval and permission prompts are not available in this context` | The CLI carries a **denial reason** for "approval needed, no prompt available". Code that waited would not need this sentence. It sits in a block of denial reasons — `171801` `Current permission mode (` is its neighbour. |
| 171831 | `Agent aborted: too many classifier denials in headless mode` | The classifier **runs in headless mode** and its denials **accumulate**. A blocking denial cannot be counted. |
| 171832 | `Classifier denial limit exceeded, falling back to prompting: ` | The fallback to prompting is a **different branch** from the headless one: `171830` `headless` and `171829` `consecutive` sit adjacent, the headless side aborting and the other side prompting. |

Supporting:

| L | string |
| --- | --- |
| 171828 | `tengu_auto_mode_denial_limit_exceeded` |

**Verdict: on a non-TTY process, `auto` DENIES; it does not block.** An
unapproved call comes back as a refusal the node reads, and the node keeps
going — the same shape `dontAsk` produces today.

**The new failure mode this introduces:** `171831`'s headless abort. No
equivalent accumulation limit for `dontAsk` denials was found in this extract.
**The threshold number is not in the on-disk text — 미측정.**

## C. What `auto` adds beyond the classifier verdict

| L | string |
| --- | --- |
| 125730 | `Note: reading files, searching code, and other read-only operations do not require the classifier and can still be used.` |
| 128872 | `READ_ONLY_AUTO_ALLOW_REASON` |
| 125726 | `Permission for this action was denied by the Claude Code auto mode classifier. Reason: ` |
| 125727 | `Permission for this action has been denied. Reason: ` |
| 125731 | `, so auto mode cannot determine the safety of ` |
| 202188 | `. This is not a judgment that the action is unsafe. ` |
| 248452 | `Add rules to your settings file under autoMode.{allow, soft_deny, hard_deny, environment}.` |
| 126932 | `- **soft_deny**: Destructive/irreversible actions the classifier should block unless clear user intent authorizes them` |
| 126933 | `- **hard_deny**: Security-boundary actions the classifier should block unconditionally (user intent does not clear these)` |
| 194904 | `Got error trying Sonnet 5 as auto mode classifier, using ` |

A classifier that cannot decide still **denies** (`125727` + `125731`), and says
so without calling the action unsafe (`202188`).

**The contrast case**, and the string `docs/measurements/0218-denied-nodes-that-passed.go`
uses as its discriminator:

| L | string |
| --- | --- |
| 202184 | ` has been denied because Claude Code is running in don't ask mode. ` |

## D. What still binds under `auto`

| L | string | binds |
| --- | --- | --- |
| 189479–189482 | `denied_by_rule`, `classifier`, `auto-mode`, `denied_by_auto_mode` | `denied_by_rule` is enumerated **separately from** `classifier` — a rule denial is not a classifier outcome. |
| 128877 | `isPreAskDeny` | deny is evaluated before the ask/classifier stage. |
| 208371 | `canUseTool will not be invoked: permissionMode 'bypassPermissions' auto-approves every tool call (except explicit deny rules) before the callback is consulted…` | deny rules survive even `bypassPermissions`. |
| 229125 | `…Explicit ask/deny rules are always respected.` | stated outright. |
| 245900 | `Specify the list of available tools from the built-in set. Use "" to disable all tools, "default" to use all tools, or specify tool names (e.g. "Bash,Edit,Read").` | `--tools` replaces the tool set; a tool that is absent cannot be called under any mode. |
| 138466 | `When true (and set in managed settings), only permission rules (allow/deny/ask) from managed settings are respected. User, project, local, and CLI argument permission rules are ignored.` | managed policy can void even argv rules. |

## E. What `auto` ignores or turns off

| L | string |
| --- | --- |
| 182321–182323 | `Ignoring dangerous permission ` / ` from ` / ` (bypasses classifier)` |
| 125525 | `#### permissions.allow entries auto mode ignores (classifier-bypassing, in your user settings)` |
| 218996 | `These permissions.allow entries in your user settings are broad enough that auto mode either ignores them at runtime, or auto-approves destructive commands with no check. Removing one means matching commands prompt again outside auto mode too.` |
| 263701 | `When true, every Bash/PowerShell allow rule is suspended while auto mode is active so all shell commands are routed through the classifier (higher safety, more classifier calls). Default: false.` |

So a **wide** allow rule may be discarded at runtime under `auto`; a **narrow**
one is not, and is not routed through the classifier unless the setting at
`263701` is turned on, whose default is off.

## F. Availability gates — `auto` can silently become `default`

| L | string |
| --- | --- |
| 182396 | `auto mode disabled by settings` |
| 182397 | `auto mode is unavailable for your plan` |
| 182398 | `auto mode requires CLAUDE_CODE_ENABLE_AUTO_MODE=1` |
| 182399 | `auto mode unavailable for this model` |
| 182400 | `auto mode is unavailable right now` |
| 182411 | `auto mode disabled: disableAutoMode in settings` |
| 263705 | `Disable auto mode` |
| 182423 | `[auto-mode] kickOutOfAutoIfNeeded applying: ctx.mode=` |

When a gate fires, `kickOutOfAutoIfNeeded` puts the mode back to `default`
(`182423`, whose neighbouring strings are the mode names). In a headless
process, `default` lands back on `171823`'s refusal — so the fallback narrows
rather than widens. **Whether any gate fires on this account is 미측정.**

## G. What could not be determined from the on-disk text

- **Whether `auto` is available on this account, plan and model.** Six gates in
  §F, all silent — 미측정.
- **Whether `-p` (headless) requires a prior interactive opt-in.** `--bg` has an
  explicit one — `222938` `--bg with auto mode requires opting in first. Run
  \`claude --permission-mode auto\` once interactively.` — and no `-p`
  equivalent was found. **Absence in an extract is not evidence of absence in
  the binary** — 미측정.
- **The headless classifier-denial threshold** (§B) — 미측정.
- **What `--setting-sources ""` does to the `autoMode.*` rules.** Those rules
  live in the settings files layer 1 withholds (`248452`), and `249199` `Print
  the default auto mode environment, allow, soft_deny, and hard_deny rules as
  JSON` shows built-in defaults exist. `128885` `AUTO_MODE_TRUSTED_SOURCES`
  suggests a separate trust restriction on them — 미측정.
- **Cost and latency of a classifier call** — 미측정.
- **How the classifier disposes of any actual call.** Everything above is what
  the binary *says* it does. No call was put to it — 미측정.
