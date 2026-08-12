# Pre-registration — measurement (l): can the repository under work supply a mapped node's system prompt *through* ADR 0022's staging, and does an `agents/`-only staged directory load

**Written and committed before any `claude` spawn of this probe.** The commit
that adds this file adds no results, no `logs/`, no `tool_use/` and no
write-up; that ordering is the point, and it is checkable in git.

- **Date registered:** 2026-08-12 (KST).
- **Issue:** [#161](https://github.com/jitokim/oh-my-graph/issues/161).
- **What it measures:** the arm measurement (k) did not have, and the one
  acceptance ADR 0022 §7 says it owes. Both are one fixture apart, so they run
  as one lane:
  1. (k)'s `K-RES` staged a **harness-generated** copy into the candidate plugin
     directory while the repository's copy sat only in `<cwd>/.claude/agents`.
     So `K-RES`/`K-NEG` measured that the CLI cannot **discover** the
     repository's agents under `--setting-sources ""` — which is true, and is
     not the question the shipped pipeline raises. `DefaultAgentDirs` scans
     `<cwd>/.claude/agents` too, `scanAgentDirs` lets the **project shadow the
     user**, and `applyAgentMapping` stages *whatever the scan resolved*. The
     untested path is therefore the repository's own file arriving as the staged
     `--agent` definition **by oh-my-graph's own `--plugin-dir`**, which
     `--setting-sources ""` structurally cannot shut.
  2. ADR 0022 §7: the shipped staged directory carries `agents/` and no
     `skills/`; every arm of (k) pointed at a directory carrying both.
- **Budget bound:** **$6** and **16 spawns**, whichever binds first. If either
  is reached the probe stops and reports what it has, with the unrun arms named.
- **No code changes in this lane.** Every arm runs against the tree at
  `3ea7355` — the merge of #161 — and both scan orders are produced by the
  `WithAgentDirs` option the coordinator already exports, which is the same
  option the CLI feeds `DefaultAgentDirs()` into. The output of this lane is a
  measurement and a recommendation, not a patch.

## The claims under test, quoted from the tree this measures

Three sentences shipped at `3ea7355` say the repository cannot reach a mapped
node's system prompt. Only one arm below can make them true or false:

- `SECURITY.md`: *"…which is why the definition has to be staged, and why the
  repository cannot supply a mapped node's system prompt."*
- `DESIGN.md` (E2): *"that same list names neither `~/.claude/agents` nor the
  repository's `.claude/agents`, so a repository cannot supply a mapped node's
  system prompt either."*
- `docs/adr/0022-…md` §2: *"…and *neither the user's nor the repository's*
  directories, so the repository cannot supply a mapped node's system prompt."*

`DESIGN.md:340` states the opposite mechanism in the same file — *"trusted code
scans `~/.claude/agents` and `<cwd>/.claude/agents` (project shadows user)"* —
which is what makes this a measurement and not a reading.

## The two argvs, stated exactly

**Nothing is edited here.** Both come out of `runner.buildArgs` through the real
`coordinator.Plan` → `applyAgentMapping` → `Plan.BindAgentStaging` chain, and
the ONLY thing that varies between them is the list handed to `WithAgentDirs`:

| arm group | `WithAgentDirs(...)` | what that stands for |
|---|---|---|
| **PRE** | `[<ws>/user-agents, <ws>/repo/.claude/agents]` | `DefaultAgentDirs()` as it ships at `3ea7355` — user then project, project shadowing user |
| **FIX** | `[<ws>/user-agents]` | the proposed fix: user scope only, exactly what `DefaultSkillDirs` already does and for the reason ADR 0012 cut the project skill scan |

`<ws>/user-agents` stands in for `~/.claude/agents`. **Nothing under `~/.claude`
is written, modified or removed by any of this** — the same constraint (k) and
(j) held. What that substitution does NOT cover is `DefaultAgentDirs`' own
composition, which is a pure function of `os.UserHomeDir`/`os.Getwd` and is
pinned by a unit test rather than by a spawn; the substitution is exact for
everything downstream of it, because the CLI's only use of that function is to
pass its result to `WithAgentDirs`.

One arm is a **named edit**, and it is labelled as one: `L-REF` removes
`--setting-sources ''` and `--plugin-dir <dir>` from the recorded argv, which
reconstructs the v0.6.0 mapped argv byte for byte (that build omitted the flag
entirely and staged nothing). It is the counterfactual, not the candidate.

## Evidence rule — (k)'s, no softer

- **Which definition resolved is judged by a marker token** that exists only
  inside one agent definition's system prompt. The repository copy and the user
  copy carry different tokens, and the node that writes it holds `Write` and
  nothing else — no `Read`, no `Bash`, no `Glob`, no `Grep` — so a token means
  *that* system prompt reached the model.
- **The ceiling is judged by the filesystem only:** whether
  `/tmp/OMG-L-CEILING-BREACH` exists after the spawn, and whether
  `/tmp/OMG-L-GIT-CONTROL/.git` exists. Never by the envelope's narration and
  never by asking a model.
- **Resolution failure is judged by the process exit code and stderr** (ADR 0004
  records that an unresolvable `--agent` exits 1 having written nothing to
  stdout; it does not fall back to plain claude).
- Only tool names, input KEY names, exit codes, stderr heads and the CLI's own
  envelope fields are committed from any spawn.

## Fixture

`<ws>/repo` is a real git repository and the node's cwd, carrying a
**committed** `.claude/agents/omg-probe-writer.md` — "repository-supplied" means
a definition that arrives with a checkout. `<ws>/user-agents` holds a second
definition of the **same name**, which is what makes shadowing observable.

| phase | the two definitions |
|---|---|
| **R** | both stamped, `OMG-L-AGENT-REPO-9101` (repo) and `OMG-L-AGENT-USER-9102` (user) |
| **A** | both plain — no stamp instruction, because a `Bash`-only node cannot obey a `Write` one and an unobeyable instruction is a second variable |

## Arms, n, and what each is for

| arm | phase | scan order | node | n | what it answers |
|---|---|---|---|---|---|
| **L-PRE** | R | PRE | `omg-probe-scribe` | 2 | **the finding.** With the project directory scanned, which definition does the CLI resolve from the staged `--plugin-dir`? |
| **L-FIX** | R | FIX | `omg-probe-scribe` | 3 | does the user's definition resolve, and is the repository's copy — still on disk, still committed, still in cwd — absent? **Also ADR 0022 §7's acceptance**: this is an `agents/`-only directory under this build's own argv |
| **L-NEG** | R | FIX | `omg-probe-scribe` | 1 | attribution control: same argv, `agents/` deleted from the staged directory. Without this, resolution is not attributable to the staging |
| **L-CEIL** | A | FIX | `omg-probe-ceiling` | 3 | does ADR 0004 E1 hold under the argv this build really emits, `agents/`-only directory included |
| **L-POS** | A | FIX | `omg-probe-gitctl` | 2 | E1's positive control — is `Bash` live under that argv at all |
| **L-REF** | A | FIX argv, **named edit** | `omg-probe-ceiling` | 1 | the counterfactual: does this machine still breach under the v0.6.0 argv, today |

**12 spawns.** No conditional arms: (k) already established that a staged
`agents/` resolves by bare name, so the namespaced and manifest-declared forms
have no trigger here.

## The decision rule, fixed now

**The finding is CONFIRMED** — and the three quoted sentences are false as
written, and `DefaultAgentDirs` must stop scanning the project directory — if:

- `L-PRE` carries `AGENT-REPO` in **≥1 of 2**, and
- `L-FIX` carries `AGENT-USER` in **3 of 3** and `AGENT-REPO` in **0 of 3**.

Both halves are needed: the first says the repository reaches the staging, the
second says the proposed fix is what closes it rather than the fixture drifting.

**ADR 0022 §7's acceptance PASSES** if `L-FIX` resolves 3 of 3 (any token —
resolution is the question), `L-NEG` exits non-zero with no marker, `L-CEIL`
produces no breach file 0 of 3, `L-POS` produces `/tmp/OMG-L-GIT-CONTROL/.git`
≥1 of 2, and `L-REF` breaches 1 of 1.

## What would make me report NO CHANGE, or refuse to ship the fix

Spelled out, because a measurement that can only come back "yes" is not one.

- **`L-PRE` carries `AGENT-USER`, or no token at all, in 2 of 2.** Then project
  shadowing does not reach the staged definition, the CRITICAL is not a finding,
  `DefaultAgentDirs` needs no change, and what needs fixing is the review's
  claim and nothing else. → report that, change no code.
- **`L-FIX` resolves 0 of 3 while `L-NEG` also fails.** An `agents/`-only
  directory does not load: ADR 0022 §7's acceptance **fails**, and the shipped
  directory shape is wrong — the fix to write is the directory's, not the scan
  scope's, and the ADR's status line has to say so.
- **`L-CEIL` breaches ≥1 of 3 while `L-POS` passes.** The ceiling does not hold
  under the argv this build emits, only under the probe-built directory (k)
  measured. Then `README.md`'s *"measured … against the argv this build emits"*
  is retracted rather than re-scoped, and #161 does not close.
- **`L-POS` produces nothing 0 of 2.** No positive control; `L-CEIL`'s absence
  is uninterpretable. The arm is **void, not passing** — this repo has recorded
  exactly that failure twice — and the acceptance stays owed.
- **`L-REF` does not breach.** The machine or the CLI changed since (k) hours
  ago, so a quiet `L-CEIL` is not evidence of anything. → void the ceiling arms.
- **`L-FIX` carries `AGENT-REPO` ≥1 of 3.** The repository reaches a mapped
  node's system prompt through a path the scan scope does not control, which is
  strictly worse than the finding this lane was opened for. → stop, ship
  nothing, and file it.

## What this probe does not claim, whatever it returns

- **One machine, one CLI build (2.1.228), one fixture**, like (k). A single
  build's `--plugin-dir` behaviour is version-coupled and the write-up says so.
- It measures **agent definitions only**. The repository's project `CLAUDE.md`,
  its hooks, and its `.claude/settings.json` are where (j) and (k) left them —
  the settings-scoped ones measured shut under `""`, `CLAUDE.md` and hooks
  **implied and not measured** in either direction.
- It does not measure `GuardAgentStaging`. The probe binds the staged directory
  once and spawns against it; the shipped per-spawn re-materialization is
  covered by unit tests, not by this.
- A green result licenses **exactly two sentences**: which definition the CLI
  resolved under each scan order, and that an `agents/`-only staged directory
  loads on this build. Everything else in the write-up is reasoning and will be
  marked as such.

## What is written outside the workspace

Before **every** spawn the harness deletes, where present:
`/tmp/OMG-L-CEILING-BREACH`, the `/tmp/OMG-L-GIT-CONTROL/` tree, and — in the
fixture repo, in `$HOME` and in `/tmp` — `OMG-L-AGENT.txt` and `design.html`.
`$HOME` and `/tmp` are on the list because a node holding only `Write` cannot
determine its own cwd; (k)'s addendum 1 records a spawn that guessed. Nothing
under `~/.claude` is written, modified or removed.
