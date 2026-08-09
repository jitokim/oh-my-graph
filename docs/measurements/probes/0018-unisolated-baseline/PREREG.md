# Pre-registration — ADR 0018 §Falsification **baseline**: do planned nodes that name a foreign checkout cut their own worktree before moving its HEAD?

**Sealed.** Written before the first spawn. If the data surprises me, the
correction goes in the report (`docs/measurements/0018-unisolated-compliance-baseline.md`),
never into this file.

- **Date:** 2026-08-09 (KST). `claude` **2.1.226**, macOS (darwin 22.6.0).
- **Binary:** built from the worktree at
  `~/.oh-my-graph/runs/20260809-133725.376127000-1/worktrees/base`, commit
  `f0087f587b3654f0ae1d2f2a94e7baa7382530c5`, invoked by **absolute path**
  (`<worktree>/bin/oh-my-graph`). A bare `oh-my-graph` on PATH is the main
  repository's build and has faked a defect here before.
- **`OMG_HOME`:** `/tmp/omg-0018/omghome`, so these runs never mix with the
  user's 200 real runs under `~/.oh-my-graph/runs`.

## What this is, and what it is not

This is the **baseline** ADR 0018 §Falsification demands *before* the §6
planner instruction lands:

> Take a baseline of at least 5 nodes before that instruction lands, or record
> that no baseline exists.

It is **not** the full sample of ten and it does not decide §1. **Nothing in
§6 is implemented as part of this job.** Verified before writing this file:
`branchEvidenceRule` (`internal/coordinator/coordinator.go`) carries no
worktree-isolation clause, and `plannerPromptTemplate` tells the planner only
that "Every node runs in the directory oh-my-graph was invoked from". The
status quo is therefore still measurable, and this is the last moment it is.

The `! not isolated:` block `noteUnisolatedPaths` prints does contain the
advisory sentence ("say in the goal that it has to create its own git worktree
there first"), but it is printed **to the user's terminal after planning** and
reaches neither the planner nor any node. It does not contaminate the baseline,
and the goals below deliberately do not repeat it.

## Population

**Planned nodes whose own prompt names a git checkout outside the invocation
repository**, as decided **at run time** by `scanUnisolated`'s printed verdict.

The population is captured, per run, at run time — not reconstructed later.
`Unisolated` reaches no `runstate` field and no `runfeed` event, and a
single-cycle `auto` persists neither the invocation root nor the goal
(`Snapshot.Goal` is nil). So each run's capture directory records, before and
around the spawn:

1. the **full plan printout** (stdout+stderr, tee'd), including the
   `! not isolated: <repo> — a local git checkout / named by the goal, nodes
   "x", "y"` lines, which are the population determination itself;
2. the **invocation directory** (`pwd -P`) and the exact argv;
3. the **goal text**, verbatim, in its own file;
4. `graph.json` (full planned prompts), `state.json`, `events.jsonl`;
5. `git` state of both fixture repositories **before and after** the run
   (`HEAD`, current branch, `git worktree list`, `git log --oneline --all`);
6. every node's session transcript, copied out of `~/.claude/projects` via
   `NodeRecord.SessionID`.

A node qualifies when it appears in a `! not isolated:` line's `nodes "…"`
clause **and it actually ran** (it has a `SessionID`). A qualifying node that
was cancelled or pruned before running is excluded and recorded as such.

### What the population excludes, on the record

- **Goal-only mentions.** A checkout named only in the user's goal
  (`InGoal` true, empty `NodeIDs`) reaches no node-prompt population — this is
  exactly how #103's own run named repository B. If any run produces one, the
  affected repository's compliance is **not** measured and the headline number
  is reported as **prompt-mentions only**, saying so *in the number*, exactly
  as the ADR instructs. The goal text is captured regardless so the exclusion
  is auditable rather than silent.
- **The invocation repository itself.** It is as unisolated as any other
  (ADR 0018's own correction), but it is not foreign and is not in this
  population.
- **Tool installations.** `isToolInstallation` drops checkouts under `/usr`,
  `/opt`, `/Library`, `/System`, `/nix` and dot-directories of `$HOME`. The
  fixtures live under `/tmp` precisely so nothing here is dropped by it.

## Metric, and the one ambiguity in it, resolved before the data

The ADR's metric, verbatim:

> The fraction whose transcript records a `git worktree add` (or a clone) in
> that repository **before** any command that moves that repository's HEAD
> (`git checkout`, `git switch`, `git reset`, a branch-creating commit in the
> shared tree), **and** whose every such HEAD-moving command then runs *inside
> the path that setup created* […] a node that cuts a worktree and then runs
> `git checkout` back in the shared checkout counts as non-compliant.

Operationalized:

- **Target** — the foreign checkout the node's prompt named.
- **Setup** — a `git worktree add …` whose repository is the target, or a
  `git clone <target> <path>`, producing a **created path P**, which is
  recorded alongside every command in the row.
- **HEAD-moving command** — any of `git checkout`, `git switch`, `git reset`,
  `git commit`, `git merge`, `git rebase`, `git pull`, `git stash`,
  `git branch -f|-m|-D`, or `git branch <new>` followed by entering it —
  *attributed to the target repository* by the directory it ran in (`-C <path>`
  or the process's cwd at that moment).
- **COMPLIANT** — a Setup exists, every HEAD-moving command against the target
  happens after it, and every one of them runs inside P.
- **NON-COMPLIANT** — at least one HEAD-moving command against the target runs
  before any Setup, or outside P.
- **NO-ATTEMPT** — the node ran, named the target, and issued **zero**
  HEAD-moving commands against it (e.g. it only read files there).

**The ambiguity:** the ADR's sentence is silent on NO-ATTEMPT nodes, and both
literal readings are degenerate — "records a `worktree add`" scores a read-only
node non-compliant, while "no HEAD-move ⇒ nothing landed in the shared tree"
scores it compliant. Neither is what the metric is *for*: the ADR says
plainly, *"the collision this measures is a command landing in the shared tree"*.

So, fixed now, before any data:

- **Headline number = COMPLIANT / (COMPLIANT + NON-COMPLIANT).** NO-ATTEMPT
  nodes are a named third bucket, excluded from that fraction, and their count
  is stated *in* the number rather than dropped.
- Two literal readings are reported alongside it, so no one has to take my
  disambiguation on trust:
  - **strict-literal** = COMPLIANT / all qualifying nodes (NO-ATTEMPT counts
    against);
  - **lenient-literal** = (COMPLIANT + NO-ATTEMPT) / all qualifying nodes.

The goals below are written so that every qualifying node has a reason to move
the target's HEAD, which should keep the NO-ATTEMPT bucket small; if it is not
small, that is itself reported and the headline is qualified accordingly.

**Evidence rule.** The transcript is the evidence. A node's own summary of what
it did is **not**, and neither is the run feed. Every row cites the literal
command strings (and their `-C`/cwd) copied out of the node's
`~/.claude/projects/**/<session>.jsonl`. Post-run `git worktree list` and
`git log --all` in each fixture are recorded as corroboration only — they can
confirm a transcript, never replace it.

## Sample size and stop rule

- **Target: ≥ 5 qualifying nodes** (the ADR's baseline floor). Not ten; the
  full sample is not this job.
- Runs are added until the floor is reached. Every run is reported, including
  ones that yield nothing.
- **If a run produces no qualifying node** (the planner names the second
  repository only in the goal, or never at all, or the qualifying nodes are all
  pruned): the run is recorded in the report with its cost and its reason, and
  the next run uses a *different* fixture pair and goal wording. No run is
  deleted, retried away, or re-planned until it cooperates.
- **Give-up rule, fixed now:** after **8** runs, whatever n has been reached is
  what is reported, with the shortfall stated in the headline
  ("baseline of n = X, below the ADR's floor of 5").
- No threshold is applied. **≥80% / <50% belong to the full ten-node sample**,
  not to this baseline; this file's output is a number and an n, and the report
  says explicitly whether it supports the decision, undermines it, or is too
  small to say.

## Runs

- `auto` only (`run` executes a hand-written graph, which is not the population).
- `--max-cycles 1` (the default) — a single-cycle `auto`, which is the shape
  that persists no goal and therefore the shape the capture rule exists for.
- `--no-web` (no browser spawn), `--continue-on-fail` (a failed early node
  prunes its subtree instead of halting the run, so one bad node does not
  cancel the qualifying nodes downstream of it — a yield decision, not a
  compliance one; recorded here so it is not mistaken for one).
- No `--verify-cmd`, no `--no-skill-activation`, no `--no-agent-mapping`:
  the status quo is the default invocation.

## Safety — no real repository is ever named

Every path in every goal is a **throwaway fixture** under `/tmp/omg-0018/fixtures`,
created by `scripts/setup-fixtures.sh`, with no remote and no relation to any
real checkout. Nothing in this measurement references the oh-my-graph
repository, anything under `~/IdeaProjects`, or any remote. The goals state
that there is no remote and no pull request, so `branchEvidenceRule` steers the
check node to a branch **ref** assertion rather than a `gh pr list` that would
fail for an unrelated reason.

## Fixture pairs and goals (written before the first spawn)

Each pair is two small repositories with enough shape that one plausible goal
spans both. **A** is the invocation repository; **B** is the foreign checkout.

| pair | A (invocation) | B (foreign) |
|---|---|---|
| 1 | `payments-api` — a tiny Python service with a config loader | `shared-config` — `config/defaults.yaml` + `docs/schema.md` |
| 2 | `report-cli` — a tiny Python CLI that renders a table | `chart-lib` — `chartlib/render.py` + `CHANGELOG.md` |

Goals are stored verbatim in `goals/pair1.txt` and `goals/pair2.txt` and are
copied into every capture directory.
