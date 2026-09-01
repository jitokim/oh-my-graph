# ADR 0037 — A planned node answers with the model the operator chose

**Status:** Accepted, and implemented in the same change that adds this record.

**Where the addresses point.** Written alongside its code, against the branch
that introduces `internal/usermodel`. Pre-change addresses (§1) were read on
`8658ebd`, the `main` this branch left; post-change addresses name the file and
the function, because the line moves and the function does not.

**Date:** 2026-08-24

**Renumbered 0034 → 0037 on 2026-08-29.** This record was written as 0034 on a
branch cut before `main` merged its own 0034
(`0034-an-unmatched-tool-call-meets-a-classifier-not-a-dead-ask.md`, added by
`7d6dd26`); merging `main` into this lane put two documents under one number.
`main`'s keeps 0034 and this one moved. A citation of "ADR 0034" dated before
this renumber — in commit messages, issues or PR #248 — may mean either
document; read it by subject, not by number. Its measurement companion keeps the
name it was written under, `docs/measurements/0034-planned-node-model.md`.

## 1. Context — the defect, confirmed rather than assumed

`--setting-sources ""` is ceiling layer 1 (`coordinator.isolatedSettingSources`,
`internal/coordinator/coordinator.go:777-780` on `8658ebd`). It withholds the
user, project and local settings from a planned node, which is what makes layer
2 a real limit instead of a declaration (DESIGN.md, E1). The operator's model
preference is a key of the very file it withholds — on the machine this was
measured on, `~/.claude/settings.json` → `model = "opus[1m]"`.

Nothing in this repository could put the choice back. Before this change there
was no `--model` in `claudeProtocol.buildArgs`, no `Model` field on
`runner.NodeInvocation`, no `model` key in the graph schema, and no `--model` in
DESIGN.md's authoritative argv block. `codexProtocol.buildArgs` carried the
identical hole by an explicitly documented mechanism: `--ignore-user-config`
("Do not load `$CODEX_HOME/config.toml`"), and that file is where a Codex
operator's `model` lives.

**Measured** (`docs/measurements/0034-planned-node-model.md`, program and raw
rows beside it):

| bucket | `claude-opus-5` | other | denominator |
|---|---|---|---|
| PLANNED | 181 | 6 (all `claude-sonnet-5`) | 187 of 195 node records |
| HAND-WRITTEN | 851 | 267 (all `claude-fable-5`) | 1118 of 1202 node records |

`claude-fable-5` occurs 267 times on hand-written nodes and **zero** times on a
planned node, same machine, same days. Hand-written nodes track the settings
file; planned nodes provably do not.

Two limits on that evidence, stated because the ADR is where they have to live:

- **It is a model-FAMILY census.** `message.model` does not record the
  context-window variant: the session that produced the measurement is reported
  by its own harness as `claude-opus-5[1m]` and writes `"claude-opus-5"` on
  every assistant record. So the honest claim is *"181 planned nodes ran a model
  nobody selected, which happened to land in the same family"* — not *"181
  planned nodes ran the wrong model"*.
- **All 6 outliers are agent-mapped**, and both definitions declare
  `model: sonnet` in their frontmatter. The planned bucket is two populations,
  not "181 correct plus 6 noise". §2.4 is that finding's consequence.

## 2. Decision

### 2.1 Read one key, at plan time, in the coordinator

`internal/usermodel` decodes `$CLAUDE_CONFIG_DIR/settings.json` (else
`~/.claude/settings.json`) into a struct with exactly one field, `Model string`,
and returns the value verbatim. `Coordinator.chosenModel` calls it ONCE per plan
and puts the answer on `Plan.Model`; the caller hands it to
`schedule.Options.Model`, which the scheduler forwards to every
`runner.NodeInvocation`.

The read is deliberately NOT in `internal/runner`: `CLIRunner` is an exec seam
and stays a pure argv/session/output protocol. The Coordinator only opens the
file when its constructor was told where (`WithUserSettingsPath`), exactly like
`agentDirs` and `skillDirs` — the library default reads nothing.

**Reading the whole file stays forbidden**, for a concrete reason: the same
document's `permissions.allow` holds the standing `Bash(*)`/`Write(*)`-class
grants layer 1 exists to withhold, and its `env` block holds live credentials. A
second key needs its own ADR.

### 2.2 The six cases

| case | behaviour |
|---|---|
| key present, non-empty | `--model <value>` **verbatim** — no normalisation, no case-folding, no `[1m]` stripping |
| key present but blank | treated as absent; the CLI itself rejects an empty value, so emitting it turns a config typo into a dead run |
| key absent | no flag; argv byte-identical to before this change |
| file absent | no flag, no warning — a machine with no settings file is supported |
| malformed / unreadable | no flag, **one warning per run** on stderr naming the path and the error, never the contents; the run proceeds — the argued exception to the rule below, see §2.8 |
| value unknown to this build | passed through verbatim — **no allowlist** |

The last row is the one worth arguing. An allowlist goes stale with the CLI's
release cadence, and the operator's current value is a bracketed variant an
allowlist author would plausibly not have anticipated; an allowlist that
rejected `opus[1m]` and fell back to the default would do **exactly the defect
under repair, with our name on it**. The failure is already loud without us: the
CLI exits non-zero having printed nothing on stdout, and `parseEnvelope` turns
that into a `*NodeOutputError` carrying the CLI's own message. **A wrong model
name must produce a dead node, never a different answer** — nothing may catch
that rejection and retry without the flag. `--fallback-model` is opt-in and
oh-my-graph never passes it.

*Measured, 2026-08-29.* The unknown-name path was executed end to end rather
than reasoned about, in
`TestModelResolvesFromSettingsToArgv/unknown_model_name_reaches_argv_verbatim_and_the_rejection_kills_the_node`
(`internal/runner/model_resolve_test.go`), run by:

```sh
go test ./internal/runner -run TestModelResolvesFromSettingsToArgv -v
```

From a scratch `HOME` whose real `settings.json` says
`claude-not-a-real-model-9`, `usermodel.Read` returned that string unchanged and
the argv `CLIRunner` built was:

```
claude -p <prompt> --output-format json --permission-mode dontAsk \
  --model claude-not-a-real-model-9 --setting-sources "" --allowedTools Read,Glob
```

A stub standing in for the CLI then refused it the way §2.2 claims a real one
does — nothing on stdout, a complaint on stderr, exit 1 — and `Run` returned a
`*NodeOutputError` naming the model. The stub echoes its own argv, so what is
asserted is that the flag reached the **child process**, not merely a `[]string`
in the test.

What that does **not** measure is the real CLI's exact stderr wording, since no
`claude` is spawned; the test scripts a refusal of that shape rather than
observing this build's own. The load-bearing half — the flag arrives verbatim,
and a refusal kills the node instead of being retried without the flag — is
measured. Recording the real message under `make smoke` remains owed (§5).

### 2.3 argv only; never the environment

`ANTHROPIC_MODEL` exists, and is rejected as the mechanism. It buys nothing —
the variable still needs a value, and the only place that value lives is the
same settings file, so the read does not go away — and it points the wrong way
on `internal/childenv.Scrub`, which is a DELETION policy over one list whose
entire value is that it has no exceptions. `--settings <file-or-json>` is
rejected outright: settings loaded that way are unioned on top and **cannot** be
dropped by `--setting-sources`, so pointing it at the operator's file would
restore their standing `Bash(*)` and demolish layer 1.

### 2.4 An agent-mapped node gets no `--model`

`--agent <name>` supplies its own model from the definition's frontmatter, and
6 of the 187 planned nodes measured rely on exactly that. The CLI tracks
`modelCli` and `agentSelectedByCli` as distinct model sources; which wins is its
business and is unmeasured here. We do not find out by shipping — when
`spec.Agent != ""`, `claudeProtocol.buildArgs` emits no `--model`. The operator
who wrote `model: sonnet` into an agent file chose that model as deliberately as
they chose the settings key, and more specifically. It costs nothing measurable:
181 of 187 planned nodes are un-mapped.

### 2.5 The planner and the assessor keep the CLI default

The planner sets no `SettingSources` at all, so it ALREADY loads the operator's
settings — deliberately, because isolating it would drop the user's CLAUDE.md
from the one call whose job is to understand this repository. There is nothing
to fix there. The assessor IS isolated, and still gets nothing, because **the
engine parses these replies**: the planner's must satisfy `graph.Parse`, the
assessor's a verdict grammar. Changing the model behind a parser is a
compatibility change wearing a preference's clothes, and it would be made
silently from a key the operator edited for a different purpose.

> The operator's model choice governs the nodes that do the work, not the
> engine's own machinery.

Both call sites say so in place (`coordinatorInvocation`, `assessorInvocation`),
so the silence is legible rather than accidental.

### 2.6 Codex: documented asymmetry, no code

The mechanism exists (`codex exec` takes a model flag, and `-c model="..."`
overrides the same key), and the defect is identical. It is still not
implemented, for one reason above all: **no codex node's model is observable in
this repository's corpus** — a codex thread writes no `~/.claude/projects`
transcript — so it would be a fix for a population nobody has measured, which is
what this repository's first rule exists to prevent. It also needs a second
foreign schema in a second format (TOML; the repo has no dependency for one).
Codex is already the documented-asymmetry runtime (ADR 0009 for session limits,
ADR 0026 for `budget_usd`) — though ADR 0009 stopped being an example of that
on 2026-09-02, when its amendment made the resumable pause cover both runtimes
([#222](https://github.com/jitokim/oh-my-graph/issues/222)); the model key of
this section and `budget_usd` are the asymmetries that remain.
`docs/LIMITATIONS.md` states the absence where the user meets it, and the
research is written down in this section so the next person does not re-derive
it. The follow-up itself is carried in the operator's
private backlog (oh-my-graph-hq `notes/open.md`), not in the public tracker.

### 2.7 No per-run flag

One surface: the settings file. A flag would need persisting in `state.json` and
re-applying on `resume`, or a resumed leg would silently run a different model
than its first — **the same class of bug being fixed here**. Nothing in the
corpus evidences the demand, per-node override already exists via `agent:`, and
`/model` in the operator's own CLI is session-scoped and one keystroke away. It
is easier to add a flag later than to remove one.

A resumed PLANNED leg re-reads the settings file instead (`runResume`), for the
same reason: with no read, its isolated nodes would answer with the CLI's
default — the defect, one leg late. Re-reading means a leg running now honours
the answer the operator would give now, exactly as a fresh run started now would.

### 2.8 A malformed settings file warns and proceeds — the argued exception

This ADR's rule is **a wrong model name must not become a silent fallback**
(§2.2). A malformed settings file proceeds without `--model`, so the node
answers with a model the operator did not select, and the row in §2.2's table
therefore needs an argument rather than an assertion. Held deliberately, on
three grounds.

**1. Nothing is substituted for a known choice, because no choice is known.**
Case 1 and case 4 look alike and are not. In case 1 oh-my-graph *knows* the
operator's string, transmits it verbatim, and the CLI kills the node — measured
above. Swallowing that rejection and retrying without the flag would be
substitution, and is what §2.2 forbids. In case 4 the document does not parse,
so oh-my-graph knows nothing: it cannot tell an operator who set `model` from
one who never did. That is the same epistemic state as *key absent* and *file
absent*, both of which are settled as ordinary, and the outcome is the same one
every run made before this ADR existed.

**2. A hard failure would make correctness depend on a weak parse of a foreign
schema, which ADR 0009 forbids.** `settings.json` is the Claude CLI's document,
not oh-my-graph's, and it may change under us. Refusing to run on a parse
failure turns an *optional preference* into a *mandatory precondition*: the day
that CLI accepts something `encoding/json` does not, every `auto`, `chat` and
`resume` run on that machine dies — including runs whose operator never set
`model` and never wanted this feature. The node itself does not need the file:
it runs under `--setting-sources ""` and never reads it, so a parse failure here
says nothing about whether the node can run correctly. Failing loudly would also
throw away a paid planner call, since the read happens after it
(`internal/coordinator/coordinator.go:680`).

**3. The fallback is announced at every entry point, and both announcements are
now measured.** Not "loud" as a claim about wording — loud as an assertion in
the suite, because there are exactly two places a planned node is spawned and
both were required to say so:

| entry point | code | test |
|---|---|---|
| first leg (`auto`, `chat`) | `cmd/oh-my-graph/main.go:859` prints `Plan.ModelWarning` once | `TestExecutePlan_ModelWarningIsPrintedOnce` |
| resumed leg (`resume`) | `resumedPlannedModel`, `cmd/oh-my-graph/resume.go` | `TestResumedPlannedModel_MalformedSettingsWarnsAndNamesThePath` |

```sh
go test ./cmd/oh-my-graph -run 'ModelWarning|ResumedPlannedModel' -v
```

Both name the settings path and the decode error, and both assert the planted
credential in the fixture is **not** echoed. The resumed leg's warning was an
inline block inside `continueRun` until 2026-08-29 and no test could reach it;
it was extracted for no other reason than to make this row a measurement.
The warning's content is checked in `internal/usermodel/usermodel_test.go` and
`internal/coordinator/usermodel_test.go` as well.

**What this exception does not cover.** The warning goes to stderr only. It is
not in `state.json` or `events.jsonl`, so a run read afterwards through `serve`
or a run-feed consumer cannot see that its nodes fell back — an operator
watching the dashboard rather than the terminal is not reached. That is a real
gap in the word "announced", it is recorded in §5, and it is a reason to carry
the warning into the run record later, not a reason to kill the run now.

## 3. Why this does not weaken the ceiling

The auto ceiling is a bound on **capability**: layer 1 controls which settings,
hooks and CLAUDE.md load; layer 2 which grants bind; layer 3 which tools exist
at all; layer 4 MCP; layer 5 the residual denies. Every layer answers "what may
this node *do*". `--model` answers "which model does the thinking". It adds no
tool, loads no file into the context, runs no hook and grants no path. A node
holding `Read, Glob` holds exactly `Read, Glob` whether opus or fable answers.

Three checks against that acceptance:

1. **The value carries no payload.** It reaches argv, not a prompt, so it is not
   fenced text and `internal/fence` does not apply — and it does not need to: it
   comes from the operator's own file. The planner cannot set it (no `model` key
   in the graph schema), so "untrusted output may not select what loads into a
   node" is intact.
2. **The read leaks nothing.** One field is decoded. `permissions`, `hooks` and
   `env` are never touched, never logged, never rendered, and the
   malformed-file warning prints the path and the decode error only.
3. **No billing angle.** The env scrub is untouched, no `--bare`, no
   `--no-session-persistence`, no provider SDK. A different model on the same
   saved login is the same subscription.

The real cost is truth in advertising: `toolPolicyFor`'s own comment and the
README promised that a planned node loses the user's CLAUDE.md, hooks and MCP
servers, and one key of that file now reaches it. Both are amended in the same
change. Word it exactly:

> **The node's capability ceiling is unchanged; one preference now crosses it,
> by name, and only that one.**

## 4. Consequences

- A planned Claude node runs the operator's chosen model; 181 nodes' worth of
  silent default-taking stops.
- An agent-mapped planned node is unchanged, by design.
- The planner, the assessor and the chat router are unchanged, by design.
- Under `--runtime codex`, a planned node runs the model `codex` itself defaults
  to; `--ignore-user-config` withholds `~/.codex/config.toml`'s `model` key, and
  oh-my-graph does not read it. Measured: no codex node's model is observable in
  this repository's corpus.
- A broken `settings.json` costs one stderr line per run and nothing else.
- `oh-my-graph` now parses a file another product owns. When that schema
  changes, the failure is bounded: a renamed or moved key yields no flag (the
  pre-change behaviour), a changed type yields one warning and no flag, and a
  value this CLI build rejects fails the node with the provider's own message.
  None of those invents anything, which is the ADR 0009 test this passes and
  parsing a prose reset time did not.

## 5. Owed

- Observe the real CLI's stderr for an unknown model name once by hand under
  `make smoke` and record it in §2.2. The path itself is no longer an inference
  — it is executed by `TestModelResolvesFromSettingsToArgv` — but the refusal it
  meets there is a scripted stub, not this build's own message.
- Carry the malformed-settings warning into the run record (`state.json` /
  `events.jsonl`), so a run watched through `serve` or read by a run-feed
  consumer can see that its nodes fell back. §2.8 holds the exception on the
  stderr announcement alone and names this as its gap.
- The Codex follow-up, with the mechanism already researched in §2.6. It is
  carried in the operator's private backlog (oh-my-graph-hq `notes/open.md`),
  not in the public tracker.
