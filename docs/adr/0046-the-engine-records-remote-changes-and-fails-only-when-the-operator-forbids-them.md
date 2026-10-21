# ADR 0046 — The engine records the remote changes it observes during a run, and fails the run only when the operator forbids them

**Status:** Proposed. No code exists. This record decides the shape; §8 lists
the tests the implementation lane owes and §9 what would show the decision
wrong. Nothing here is implemented, and the flag it names does not yet parse.

**Where the addresses point.** Written against `ac7391c`, the `main` this
branch (`docs/adr-0046-remote-changes`) left. Every `file:line` below was read
at that commit. PR #409 (#388) was open and unmerged at that commit, so its
wording is cited by PR, never by line; the `branchEvidenceRule` lines given
here are the text before #409. The Decision describes code that does not exist
yet, so it names file **and** function: the line goes stale when the diff
lands, and the name does not. Every constant, field, flag, type or function
this ADR chooses is marked *chosen*.

**Date:** 2026-10-21

**Issue:** [#410](https://github.com/jitokim/oh-my-graph/issues/410). A run can
push a branch, add a remote, or open or edit a pull request while its goal
forbids it, and since #388 nothing in the run says so.

## 1. Context

### 1.1 What #388 took away from the check nodes

`branchEvidenceRule` (`internal/coordinator/coordinator.go:2198`, doc comment
from 2157) is the branch half of the planner's final-check paragraph. At
`ac7391c` it tells a final check node to "Prefer what the remote says: 'gh pr
list --head <branch> --state open'" (:2203), and only for a goal that "never
reaches a pull request" to assert the branch ref instead (:2209–2210).

PR #409 (open, unmerged; branch `fix/388-check-node-forbidden-state`) narrows
that rule. The remote bullet becomes "ONLY when the goal's own nodes push the
branch and open the pull request, assert what the remote says". The ref bullet
gains "including any goal that says not to push, not to open or edit a pull
request, or not to create a remote … Such a check node never asserts remotes,
pushes or pull-request state." And a scope rule is added: every planned check
node "asserts only what this run's own nodes produce or change … never asserts
the ABSENCE of something the goal only forbids creating".

That fixed a real failure: honest runs whose goal said "do not push" failed on
"not pushed" or "`git remote -v` prints nothing".

### 1.2 What it leaves uncovered

Planned nodes may still hold the tools to change remote state.
`plannedToolAllowlist` (`internal/coordinator/coordinator.go:71–74`) contains
both `Bash(git *)` and `Bash(gh pr *)` (:73), enforced by
`validatePlannedNodeTools` (:1825). A planned node can therefore run
`git push`, `git remote add`, `gh pr create` or `gh pr edit`, and its grant is
legal.

Before #409, a check node that asserted "no PR exists" caught such a push, but
only by accident: it was the same assertion that failed honest runs. After
#409, nothing does. The run's verdict is silent on a push the goal forbade.

### 1.3 A check node is the wrong place for this

A check node is model output judging the run of which it is part. Its PASS is
self-reported (`plannedVerdictPattern`, `coordinator.go:2312`). If a node
pushed, a check node run by the same planner, under the same allowlist, is the
weakest available witness. Remote state, by contrast, is something the engine
can read directly, outside any model, and write down from its own record.

The engine already keeps three records of that kind:

- **The `--verify-cmd` baseline (#315).** Taken at
  `cmd/oh-my-graph/main.go:614–630` before anything is spent; a red baseline
  is `*BaselineRedError` (`cmd/oh-my-graph/baseline.go:90`) and exits 5
  (`main.go:189–192`). Only a skip is stored, as `runstate.Baseline`
  (`internal/runstate/runstate.go:521`, field at :750). Documented at
  `DESIGN.md:3017`.
- **Verify pins (#363).** `pinVerifyCommand` (`cmd/oh-my-graph/verifypins.go:24`)
  hashes the script a `--verify-cmd` executes; stored as `runstate.VerifyPin`
  (`runstate.go:550`, field `verify_pins` at :757). Documented at
  `DESIGN.md:3053`.
- **Verification records (#332).** `internal/schedule/verifyrecord.go` builds
  a `runstate.VerificationRecord` (`runstate.go:396`) for each
  `success_check.verify` the engine ran; it lands on the node at
  `runstate.go:348` and reaches the assessor as an `ENGINE-OBSERVED
  verification of node %s` block (`internal/coordinator/assess.go:434`).

This ADR adds a fourth record of the same kind: what changed on the remote
while the run ran.

## 2. What the engine observes

### 2.1 The snapshot

The engine takes a **remote snapshot** (`runstate.RemoteSnapshot`, *chosen*)
twice: once before the first node starts and once after the last node ends. It
covers the **invocation repository only**: the repository `auto` or `run` was
started in, the same `repoDir` the worktree manager is built with
(`cmd/oh-my-graph/main.go:2357`).

A snapshot holds three parts:

1. **Configured remotes.** `git remote -v`: each remote's name and its fetch
   and push URLs.
2. **Remote branch refs.** `git ls-remote --heads <remote>` for each configured
   remote: branch name and sha.
3. **Open pull requests.** `gh pr list --state open --json
   number,headRefName,updatedAt`, run in the invocation repository, so the
   list is the PRs whose base is this repository's GitHub repo. Only when `gh`
   is on `PATH` and authenticated.

Each part is recorded with its own status (`observed` or `unavailable`,
*chosen*), and an `unavailable` part keeps the reason the engine saw (`gh` not
found, not authenticated, non-zero exit, timeout). An unavailable part is
**never guessed**: it is not treated as empty, and it is not compared.

### 2.2 The change record

The difference between the two snapshots is the **remote change record**
(`runstate.RemoteChanges`, *chosen*). Its entries are of these kinds
(*chosen*):

- `remote_added`, `remote_removed`, `remote_url_changed`;
- `branch_created`, `branch_moved` (old and new sha), `branch_deleted`;
- `pr_opened`, `pr_updated` (same number, later `updatedAt`), `pr_no_longer_open`.

A part compared against an unavailable part on either side yields no entries
and a `not_compared` line naming the part, so "no change" and "could not see"
never print the same.

It is stored on the snapshot as `remote_changes` (`Snapshot.RemoteChanges
*RemoteChanges`, *chosen*), holding the before snapshot, the after snapshot and
the entries. It is printed in the run summary after the ledger table, beside
`led.Print(os.Stdout)` (`cmd/oh-my-graph/main.go:1277` for `run`/`auto`,
`cmd/oh-my-graph/resume.go:816` for `resume`). A run with no changes prints one
line saying so and which parts were observed.

**Schema.** The record is additive and optional, and Schema stays 3
(`runstate.go:67`). The rule is `runstate.go:827–831`: "a record moves the
stamp when an older reader that ignored it would run the rest of the run
differently." An older reader that drops `remote_changes` loses a report and
nothing else, as with `verification` (`runstate.go:344–347`), `verify_pins`
(:544) and `baseline` (:516–520). The forbid flag of §4 is different; its
stamp is decided in §5.3.

**Feed.** No new `events.jsonl` event type. The event-type set is closed per
feed schema version (`docs/RUN-FEED.md:884–902`), and a new type would move
`internal/runfeed/runfeed.go:46` from 3 to 4. The record lives in `state.json`
and the summary; a feed consumer reads it from `state.json`.

### 2.3 Which seam runs the commands

Every process the engine spawns goes through one of exactly four seams
(`internal/invariants/exec_seam_test.go:27–42`, `CONTRIBUTING.md:172–216`),
each with exactly one `exec.Command` and a `childenv.Scrub` env
(`TestExecSeamCallSitesScrubEnv`, `exec_seam_test.go:477`).

**git runs on seam 3, `worktree.GitManager`.** Two new methods,
`(*GitManager).Remotes` and `(*GitManager).LsRemoteHeads` (*chosen*), call the
existing helper `m.git(ctx, …)` (`internal/worktree/git.go:304`), which reaches
the single spawn in `gitCmd` (`git.go:330–334`) with
`cmd.Env = childenv.Scrub(m.environ())`. No `exec.Command` is added, so the
one-per-file invariant holds and the scrub is inherited. This **amends ADR
0005's scope**: that ADR and the `CONTRIBUTING.md:180` table row define the
seam as "the `git worktree` commands behind a node's `worktree:`"; it becomes
"the `git` commands the engine itself issues against the invocation
repository: worktrees, and the remote snapshot". The table row is updated with
the code.

`git ls-remote` reaches the network under the existing 30s `gitTimeout`
(`git.go:21`), and nothing sets `GIT_TERMINAL_PROMPT` today, so a credential
helper could block until that timeout (inferred from the code, not measured).
The snapshot methods add `GIT_TERMINAL_PROMPT=0` (*chosen*) to the scrubbed
env. They sit on a narrow interface, `worktree.RemoteObserver` (*chosen*),
beside the Acquire-only `worktree.Provider` (`internal/worktree/worktree.go:34`).

**gh is a fifth spawner, and this ADR names it.** No seam runs `gh` today.
The two existing seams that could were rejected:

- `verify.ShellVerifier` (`internal/verify/shell.go:133`) runs a shell string,
  and its contract (ADR 0002) is the *user's* `success_check.verify` or
  `--verify-cmd`. An engine-authored command there makes "what ran in the
  verifier" no longer the user's words, and puts the engine's own argv through
  shell parsing.
- `worktree.GitManager` runs `git`, by name, in `gitCmd`. Making it run `gh`
  changes what the seam *is*, not just which git subcommands it issues.

So the `gh` call is a **fifth seam**: `internal/remote.GHRunner` (*chosen*), in
`internal/remote/gh.go` (*chosen*), with exactly one
`exec.CommandContext(ctx, "gh", args...)`, `cmd.Dir` set to the invocation
repository, `cmd.Env = childenv.Scrub(…)` plus `GH_PROMPT_DISABLED=1`
(*chosen*), and its own timeout (`ghTimeout = 30s`, *chosen*, matching
`gitTimeout`). `childenv.Scrub` removes the four model keys
(`internal/childenv/childenv.go:30–36`) and leaves `GH_TOKEN`, so `gh`
authenticates as the operator. Its argv is fixed by the engine (`pr list
--state open --json number,headRefName,updatedAt`); no model text reaches it.

ADR 0002 (lines 58–64), ADR 0005 and ADR 0006 say a fifth spawner needs its
own ADR. This is that ADR for `GHRunner`, and nothing wider: it admits one
fixed, read-only `gh` invocation. The implementation must add the file to
`allowedExecImporters` and `execSeamCallSites`, update `CONTRIBUTING.md` and the
`childenv` package doc ("FOUR spawners", `childenv.go:4–21`), and
`prose_count_test` will then derive 5. If the review of this ADR refuses a
fifth seam, the PR part is recorded as `unavailable` with the reason
`no gh seam` (*chosen*) and the git parts ship alone; §2.1 already makes that
honest.

### 2.4 What it does not see

- **Other repositories.** A node that pushes to a clone under `/tmp`, or runs
  `gh pr create --repo other/repo`, changes nothing this snapshot reads.
- **A change reverted before the end.** A branch pushed and deleted, or a PR
  opened and closed (`pr_no_longer_open` only shows PRs open at the *before*
  snapshot), leaves the two snapshots equal.
- **Concurrent human changes.** A human who force-pushes an unrelated branch, or
  updates a PR, while the run runs is indistinguishable from a node doing it.
- **Anything outside branches, remotes and open PRs:** tags, releases, PR
  comments, issue comments, CI state.

A ref change cannot be attributed to a node: the snapshot sees state, not
actors. Every entry is labelled **"observed during the run"** (*chosen* summary
wording), never "made by the run", in the summary, in `state.json` and in the
assessor block.

## 3. Option A: a forbidden change fails the run; Option B: it is only reported

Both options take the same snapshots and write the same record. They differ
only in whether a change the operator forbade moves the verdict. (How
"forbidden" is known is §4; this section assumes it is known.)

### 3.1 Option A — fail

**What the operator sees.** The ledger table, then the change record, then a
failure naming the entries that were forbidden: "remote changes observed
during the run, and `--forbid-remote-changes` was set: branch_created
origin/feat/x …". The exit code is non-zero.

**False positive** (a human pushed during the run, or a remote the operator
did not mean was listed): an honest run fails. The cost is a re-run or a
reading of the record, and the record says exactly what changed, so the
operator can tell their own push from a node's. It is bounded and visible.

**False negative** (a change outside §2.4's coverage, or an `unavailable`
part): the run passes while a forbidden change happened. That is today's
behaviour, so A is never worse than now.

**Resume.** The *before* snapshot is the one taken before the first node of
the first leg, persisted in `state.json`; a resume leg never takes a new
*before*. Each leg ends with an *after* snapshot that replaces the previous
one, and the change record is recomputed against the original *before*. So
the record covers the whole run, pauses included: a human who pushes while a
gate is paused (`resumeGateLeg`, `cmd/oh-my-graph/resume.go:196`) is seen.
That is the "observed during the run" label doing its work. A `--retry-failed`
leg (`resumeRetryLeg`, `resume.go:241`) is the same run and is covered the same
way.

**`--continue-on-fail`** (`cmd/oh-my-graph/flags.go:107`,
`effectiveContinueOnFail`, `internal/schedule/scheduler.go:1911`) is about
pruning a failed node's subtree. A forbidden remote change is not a node
failure and cannot be pruned; it is checked after `scheduler.Run`
(`scheduler.go:556`) returns, whatever it returned. If the run already failed,
both failures are reported.

**The goal assessor.** In a goal loop, a failed run is already fed to the
assessor as `RunPassed=false` (`cycleEvidence`, `cmd/oh-my-graph/goal.go:239`,
called at :156). The change record is added to `CycleEvidence`
(`internal/coordinator/assess.go:99`) as an engine-observed block either way.

**Graphs whose job is to push.** In `graphs/merge-shepherd.yaml`, `triage`
pushes (:515), `ready-and-wait` runs `gh pr ready` (:285) and `merge` runs `gh
pr merge` (grant at :1181–1188). The `pr-publish` fragment
(`graphs/fragments/pr-publish.yaml:43`), `gated-dev.yaml` (:241),
`adr-driven-dev.yaml` (:349) and `apply-flags.yaml` (:61) push too. With A as
the default, each would fail on success, so A is acceptable only opt-in.

### 3.2 Option B — report only

**What the operator sees.** The same record after the ledger table, and the
same exit code as today. A change appears as a report line and nothing more.

**False positive:** a line to read. **False negative:** the same coverage gap
as A. **The real cost of B** is the case #410 is about: the goal said "do not push",
a node pushed, and the run exits 0. An operator who reads the summary sees it;
an operator or agent who scripts on the exit code does not. B makes the
violation visible, not enforced.

**Resume, `--continue-on-fail`, assessor:** as in A, without a verdict change.
The assessor may still judge the goal unmet on the record; that is its
judgement of evidence, not the engine's verdict. **Merge-shepherd-like
graphs:** unaffected; their pushes appear in the record, which is correct.

## 4. How "forbidden" is known

Three mechanisms were compared.

**(i) Parse the goal text** for "do not push", "don't open a PR", "no remote".
Rejected. It is fragile (negation, quoting, "push back on the design"), it is
language-dependent, and a model-written goal (a cycle-2 continuation, an agent
calling `auto`) can omit the phrase while meaning it. It would also make the
engine read intent out of prose, which nothing else in the engine does: the
verdict is built from records and declared fields.

**(ii) An explicit operator flag on `auto` and `run`:**
`--forbid-remote-changes` (*chosen*). Machine-readable, nothing guessed, and
said by the person who owns the remote. It is persisted like
`continue_on_fail` (`runstate.go:677`) as `forbid_remote_changes`
(*chosen*), so `resume` carries it and cannot drop it.

**(iii) A graph-level YAML field** (`remote: forbid`, *chosen* name, not
adopted). It would let a hand-written graph declare itself remote-free. It is
deferred, not rejected, for two reasons: the graph's author is not always the
operator running it, and for a planned graph the field would be written by the
planner, which is (i) again with a model in the middle. `run` takes the flag
of (ii), so a hand-written graph is covered without it.

**Decision: (ii).** "Forbidden" means the operator passed
`--forbid-remote-changes`. Any entry in the change record is then a forbidden
change, whatever kind; the flag does not take a list of kinds (*chosen*: one
switch, all of §2.2).

## 5. Recommendation

### 5.1 The position

The coordinator's recommendation, recorded from Cindy: **fail when the goal
explicitly forbids it, otherwise report only.** That is Option A behind §4's
flag and Option B without it:

- **Without `--forbid-remote-changes`:** Option B. The snapshot is taken, the
  record is written and printed, the assessor sees it, and the verdict is
  today's. Merge-shepherd and every publishing graph run unchanged.
- **With `--forbid-remote-changes`:** Option A. Any entry fails the run with
  `RemoteChangeForbiddenError` (*chosen*, in `cmd/oh-my-graph`), exit code 6
  (*chosen*; 1–5 are taken, `exitCodeForError`, `cmd/oh-my-graph/main.go:144`),
  so a script can tell it apart from a node failure.

"Explicitly" is made machine-checkable by the flag. **The engine never parses
the goal text.**

The remote change is **always** recorded and reported, whether or not the flag
is set. The record costs the same either way, and an operator who did not set
the flag still learns what changed.

### 5.2 The planner

With the flag set, the planner prompt gains one sentence (*chosen* wording to
be settled in the golden): the operator forbade remote changes, the engine
observes the remote itself, and no node should push, add a remote or open or
edit a pull request. This is a hint, not the enforcement; the enforcement is
the snapshot. It also agrees with #409's rule that check nodes never assert
remote state: the engine does that now. Without the flag the prompt is
byte-identical to today's.

With the flag set and an unavailable part, the run starts anyway and the
launch line names the part (*chosen*): a repository with no GitHub remote
legitimately has no PRs to observe, and refusing would make the flag unusable
there. The verdict is then built only from the parts observed, and the summary
says which were not.

### 5.3 Schema stamp of the flag

`forbid_remote_changes` is not like `remote_changes`. An older binary resuming
a run that dropped it would finish without the check: it "would run the rest
of the run differently" (`runstate.go:827–831`). So, by the conditional-stamp
rule of ADR 0041 §6 and ADR 0044, a snapshot with the flag set stamps
`SchemaWithRemoteGuard = 5` (*chosen*), and Load (`runstate.go:1051`) accepts
it; a run without the flag stays at 3 (or 4 with conventions). A snapshot with
both conventions and the flag stamps 5, and a reader of 5 understands 4's
record. An older binary then refuses to resume a guarded run instead of
silently dropping the guard.

## 6. Alternatives considered

- **Keep asserting remote state in check nodes** (the pre-#409 rule). Rejected:
  it failed honest runs, and it is a model judging its own run (§1.3).
- **Option A always on.** Fails every publishing graph (§3.1).
- **Option B only, no flag.** Leaves #410's case at exit 0 (§3.2).
- **Remove `Bash(git *)` and `Bash(gh pr *)` from `plannedToolAllowlist`.**
  Breaks every goal that legitimately commits or opens a PR, and a narrower
  grant (say, no `git push *`) is not enforceable against bare `git` argument
  shapes. The allowlist stays as it is; observation is enforcement-independent.
- **`ShellVerifier` for the snapshot; git only, skipping PRs; a feed event per
  change.** Rejected in §2.3 and §2.2; git-only remains the fallback if the
  fifth seam is refused, but PR creation is half of #410.

## 7. Consequences and implementation outline

For the operator: every `run`, `auto` and `resume` summary gains a
remote-change section, the assessor gains an engine-observed remote block,
and `--forbid-remote-changes` fails a run on any observed change with exit 6.

### 7.1 Cost

Per leg, two snapshots: one `git remote -v`, one `git ls-remote --heads` per
remote, and one `gh pr list`. For the usual single-remote repository that is
two to four extra git/gh calls before and after, each bounded by a 30s
timeout, and the two network calls dominate. A run already spends minutes in
model calls; the snapshot is not on the per-node path.

### 7.2 What it does not fix

Everything in §2.4. And it does not stop a push, it notices one afterwards;
preventing it would mean a narrower tool grant, which §6 rejects for now.

### 7.3 Implementation outline

File and function names only; none of these exist at `ac7391c`.

- `internal/worktree/git.go`: `(*GitManager).Remotes` and
  `(*GitManager).LsRemoteHeads` (*chosen*), via `m.git`; `GIT_TERMINAL_PROMPT=0`
  on their env. `internal/worktree/worktree.go`: the `RemoteObserver`
  interface (*chosen*) and a fake beside the existing ones.
- `internal/remote/gh.go` (new, *chosen*): `GHRunner` and `(*GHRunner).OpenPRs`,
  the fifth seam. `internal/remote/snapshot.go` (new, *chosen*):
  `Take(ctx, RemoteObserver, PRLister) RemoteSnapshot` and
  `Diff(before, after) RemoteChanges`, pure apart from the two interfaces.
- `internal/runstate/runstate.go`: `RemoteSnapshot`, `RemoteChanges`,
  `Snapshot.RemoteChanges` and `Snapshot.ForbidRemoteChanges` (all *chosen*);
  `SchemaWithRemoteGuard` and its acceptance in Load.
- `cmd/oh-my-graph/flags.go`: `--forbid-remote-changes` (*chosen*) on `run`
  and `auto`.
- `cmd/oh-my-graph/main.go`: in `executeGraph` (:1118), take *before* ahead of
  `scheduler.Run` (:1270) and *after* behind it; print after `led.Print`
  (:1277); `RemoteChangeForbiddenError` and its exit code in
  `exitCodeForError` (:144).
- `cmd/oh-my-graph/resume.go`: carry *before* and the flag forward; take
  *after* at the end of `continueRun` (:371); print after `led.Print` (:816).
- `cmd/oh-my-graph/goal.go`: `cycleEvidence` (:239) copies the record;
  `internal/coordinator/assess.go`: `CycleEvidence` gains it and
  `assessPrompt` (:331) renders an `ENGINE-OBSERVED remote changes` block
  (*chosen* label).
- `internal/coordinator/coordinator.go`: the §5.2 sentence, only with the flag.
- `internal/invariants/exec_seam_test.go`, `CONTRIBUTING.md`,
  `internal/childenv/childenv.go` doc: the fifth seam and seam 3's wider role.
- Documentation when it lands: DESIGN.md's auto section (beside the
  engine-verification block at `DESIGN.md:3933–3934`), `docs/RUN-FEED.md`
  beside `baseline` and `verify_pins`, and the usage text.

## 8. Tests the implementation owes

Unit tests use a fake `RemoteObserver` and a fake `PRLister` (the
`FakeManager`/`FakeRunner` pattern), with no real spawn and no network.

1. **No change, no flag: byte-identical verdict.** Equal fake snapshots give an
   empty record, one "no remote changes observed" line, exit 0, and a planner
   prompt equal to today's golden.
2. **Each change kind is detected.** One table case per §2.2 kind, including
   `branch_moved` carrying both shas and `pr_updated` keyed on `updatedAt`.
3. **Unavailable is never empty.** A `PRLister` returning "not authenticated"
   on *after* only yields `not_compared` for PRs and no `pr_no_longer_open`
   entries.
4. **Report only without the flag.** A `branch_created` entry with no flag:
   the run's exit is today's, the record is in `state.json` and the summary.
5. **Fail with the flag.** The same entry with `--forbid-remote-changes`:
   `RemoteChangeForbiddenError`, exit 6, the entry named in the message.
6. **The label.** No rendering of the record (summary, assessor block) contains
   "made by"; each entry carries "observed during the run".
7. **Resume keeps the first *before*.** A two-leg run where the fake changes
   state between legs: the second leg takes no *before*, and the record diffs
   leg-2 *after* against leg-1 *before*.
8. **`--continue-on-fail` does not prune it.** A pruned node failure plus a
   forbidden change reports both, and the exit is 6.
9. **The flag survives resume and stamps the schema.** A guarded snapshot is
   written with schema 5; an unguarded one with 3 (or 4 with conventions);
   a schema-3 snapshot written before this change, with no `remote_changes`,
   still loads and resumes.
10. **The assessor sees it.** `cycleEvidence` copies the record and the
    assessor prompt carries the engine-observed remote block, with and without
    the flag.
11. **The planner hint.** With the flag, the planner prompt contains the §5.2
    sentence; without it, the golden is unchanged.
12. **Seams.** `TestOnlyTheFourExecSeamsImportOsExec` (renamed for five) and
    `TestExecSeamCallSitesScrubEnv` pass with `internal/remote/gh.go` listed;
    the git snapshot methods add no `exec.Command` to `git.go`.
13. **No new feed event.** The feed schema constant is still 3 and no event
    type is added.

## 9. Falsification

1. **Guarded runs fail mostly on changes no node made.** If, over the first
   twenty `--forbid-remote-changes` failures, most are human pushes or
   unrelated refs, then the invocation-repository scope is too wide. Narrow
   the guarded set (for example, to new branches and new PRs only) before
   dropping the flag.
2. **Forbidden pushes still land unflagged.** If a run with the flag set is
   later found to have pushed to this repository and the record shows nothing,
   either a part was `unavailable` or §2.4's reverted-change gap is being hit
   in practice. The first means the launch warning is too quiet and should
   become a refusal; the second means a per-node snapshot is owed.
3. **Operators never pass the flag.** If goals that say "do not push" keep
   running without it, the explicit-flag choice of §4 is not reaching the
   people who need it. Then reconsider (iii) for hand-written graphs, or make
   the flag more visible in the usage text; never reconsider (i).
4. **The snapshot costs more than a node.** If the two snapshots routinely
   approach their timeouts on real repositories, the before/after shape is
   wrong for the network, and the snapshot should drop to remotes and refs
   only, with PRs observed on demand.
5. **The fifth seam is refused.** Then §2.3's fallback stands: git parts only,
   PRs `unavailable` with reason `no gh seam`, and #410 stays half-answered
   until a seam is agreed.
