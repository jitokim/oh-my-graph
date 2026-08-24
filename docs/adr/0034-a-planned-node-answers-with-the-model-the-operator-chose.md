# ADR 0034 — A planned node answers with the model the operator chose

**Status:** Accepted, and implemented in the same change that adds this record.

**Where the addresses point.** Written alongside its code, against the branch
that introduces `internal/usermodel`. Pre-change addresses (§1) were read on
`8658ebd`, the `main` this branch left; post-change addresses name the file and
the function, because the line moves and the function does not.

**Date:** 2026-08-24

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
| malformed / unreadable | no flag, **one warning per run** on stderr naming the path and the error, never the contents; the run proceeds |
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

*Not yet confirmed by hand:* the exact stderr an unknown name produces has not
been observed under `make smoke`; it is inferred from the CLI's error strings
plus the existing envelope handling. Recording the observed message is owed.

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
ADR 0026 for `budget_usd`); `docs/LIMITATIONS.md` states the absence where the
user meets it, and the research is written down in this section so the next
person does not re-derive it. The follow-up itself is carried in the operator's
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

- Confirm the unknown-model failure path once by hand under `make smoke` and
  record the observed message here (§2.2 is currently an inference).
- The Codex follow-up, with the mechanism already researched in §2.6. It is
  carried in the operator's private backlog (oh-my-graph-hq `notes/open.md`),
  not in the public tracker.
