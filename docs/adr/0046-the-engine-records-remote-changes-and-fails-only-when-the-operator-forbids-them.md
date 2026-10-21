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
it: "ONLY when the goal's own nodes push the branch and open the pull request,
assert what the remote says", and for a goal that says not to push, open or
edit a pull request, or create a remote, "Such a check node never asserts
remotes, pushes or pull-request state." That fixed honest runs whose goal said
"do not push" failing on "not pushed" or "`git remote -v` prints nothing".

### 1.2 What it leaves uncovered

Planned nodes may still hold the tools to change remote state:
`plannedToolAllowlist` (`internal/coordinator/coordinator.go:71–74`) contains
both `Bash(git *)` and `Bash(gh pr *)` (:73), enforced by
`validatePlannedNodeTools` (:1825). A planned node can legally run `git push`,
`git remote add`, `gh pr create` or `gh pr edit`. Before #409 a check node
asserting "no PR exists" caught that only by accident, with the assertion that
failed honest runs. After #409 nothing does.

### 1.3 A check node is the wrong place for this

A check node is model output judging the run it is part of; its PASS is
self-reported (`plannedVerdictPattern`, `coordinator.go:2312`). The engine can
read remote state directly, outside any model, as it already does for three
records:

- **The `--verify-cmd` baseline (#315):** `cmd/oh-my-graph/main.go:614–630`;
  red is `*BaselineRedError` (`cmd/oh-my-graph/baseline.go:73`), exit 5.
- **Verify pins (#363):** `runstate.VerifyPin`
  (`internal/runstate/runstate.go:549`), `verify_pins` at :757.
- **Verification records (#332):** `runstate.VerificationRecord`
  (`runstate.go:396`), shown to the assessor as `ENGINE-OBSERVED verification
  of node %s` (`internal/coordinator/assess.go:434`).

This ADR adds a fourth record of the same kind: what changed on the remote
while the run ran.

## 2. What the engine observes

### 2.1 The snapshot

The engine takes a **remote snapshot** (`runstate.RemoteSnapshot`, *chosen*)
twice per leg: a *before* when the leg starts, before its first node, and an
*after* when its last node ends (§3.1 says why each leg gets its own). It
covers the **invocation repository only**: the repository `auto` or `run` was
started in, the same `repoDir` the worktree manager is built with
(`cmd/oh-my-graph/main.go:2357`).

A snapshot holds three parts:

1. **Configured remotes.** `git remote -v`: each remote's name and its fetch
   and push URLs.
2. **Remote branch refs.** `git ls-remote --heads <remote>` for each configured
   remote: branch name and sha.
3. **Open pull requests.** `gh pr list --state open --limit 1000 --json
   number,headRefName,headRefOid`, run in the invocation repository, so the
   list is the PRs whose base is this repository's GitHub repo. Only when `gh`
   is on `PATH` and authenticated. `gh pr list` returns 30 PRs unless told
   otherwise, so two snapshots of a busy repository could list different
   PRs that never changed. The limit is fixed at 1000 (`prListLimit`,
   *chosen*), and a result holding exactly that many is recorded as
   `unavailable` with reason `truncated` (*chosen*), never compared.

Each part is recorded with its own status (`observed` or `unavailable`,
*chosen*), and an `unavailable` part keeps the reason the engine saw (`gh` not
found, not authenticated, non-zero exit, timeout, `truncated`). An
unavailable part is **never guessed**: it is not treated as empty, and it is not compared.

### 2.2 The change record

The difference between the two snapshots is the **remote change record**
(`runstate.RemoteChanges`, *chosen*), with entries of these kinds (*chosen*):

- `remote_added`, `remote_removed`, `remote_url_changed`;
- `branch_created`, `branch_moved` (old and new sha), `branch_deleted`;
- `pr_opened`, `pr_updated` (same number, head sha `headRefOid` moved),
  `pr_no_longer_open`.

`pr_updated` is keyed on the head sha, not `updatedAt`: GitHub moves
`updatedAt` on comments, labels, reviews and bot activity, which §2.4 says
the snapshot does not see, and a bot comment must not fail a guarded run.

**Attribution** (*chosen* field `attribution`). Each entry also records
whether it is attributable to this run, decided by §5.1's local check on the
entry's **new sha**: a `branch_created` or `branch_moved` entry's new tip, a
`pr_opened` or `pr_updated` entry's `headRefOid`. A PR entry is attributable
exactly when its head sha is. The field takes two values (*chosen*):

- `run_commit`: the new sha passes §5.1's check. The entry also records which
  condition held (`attribution_via`, `run_branch` or `created_this_leg`,
  *chosen*). `run_branch` means the sha is reachable from one of the run's
  own `omg/<<runID>>/` branches **and not** from the run's base commit
  (`run_base_sha`, §5.1), so a commit that was already in the invocation
  repository's history when the run started never counts. It is labelled **"observed during the run; points at a commit
  this run made"** (*chosen*).
- `not_attributable`: every other entry. That includes every entry with no new
  sha (`remote_added`, `remote_removed`, `remote_url_changed`,
  `branch_deleted`, `pr_no_longer_open`) and every between-legs entry (§3.1).
  It is labelled **"observed, not attributable"** (*chosen*).

A part unavailable on either side yields no entries and a `not_compared` line
naming it, so "no change" and "could not see" never print the same.

The record is stored as `remote_changes` (`Snapshot.RemoteChanges`, *chosen*)
with each leg's two snapshots and entries and the between-legs entries
(§3.1), and printed after the ledger table,
beside `led.Print(os.Stdout)` (`cmd/oh-my-graph/main.go:1277` for `run`/`auto`,
`cmd/oh-my-graph/resume.go:816` for `resume`). No changes prints one line
saying so and which parts were observed.

**Schema.** The record is additive and optional, and Schema stays 3
(`runstate.go:67`). The rule is `runstate.go:827–831`: "a record moves the
stamp when an older reader that ignored it would run the rest of the run
differently." An older reader that drops `remote_changes` loses a report and
nothing else, as with `verification` (`runstate.go:344–347`), `verify_pins`
(:544) and `baseline` (:516–520). The forbid flag of §4 is different; its
stamp is decided in §5.3.

**Feed.** No new `events.jsonl` event type: the set is closed per feed schema
version (`docs/RUN-FEED.md:884–902`), and a new type would move
`internal/runfeed/runfeed.go:46` from 3 to 4. Feed consumers read `state.json`.

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
0005's scope**: that ADR and the `CONTRIBUTING.md:181` table row define the
seam as "the `git worktree` commands behind a node's `worktree:`"; it becomes
"the `git` commands the engine itself issues against the invocation
repository: worktrees, and the remote snapshot". The table row is updated with
the code.

`git ls-remote` reaches the network under the 30s `gitTimeout` (`git.go:21`),
and nothing sets `GIT_TERMINAL_PROMPT`, so a credential helper could block
until it (inferred, not measured); the snapshot methods add
`GIT_TERMINAL_PROMPT=0` (*chosen*). They sit on `worktree.RemoteObserver`
(*chosen*), beside the Acquire-only `worktree.Provider` (`worktree.go:34`).

**gh is a fifth spawner, and this ADR names it.** No seam runs `gh` today.
`verify.ShellVerifier` (`internal/verify/shell.go:133`) is rejected because
its contract (ADR 0002) is the *user's* `success_check.verify` or
`--verify-cmd`, not engine-authored argv; `GitManager` because running `gh`
would change what the seam *is*, not which git subcommands it issues.

So the `gh` call is a **fifth seam**: `internal/remote.GHRunner` (*chosen*), in
`internal/remote/gh.go` (*chosen*), with exactly one
`exec.CommandContext(ctx, "gh", args...)`, `cmd.Dir` set to the invocation
repository, `cmd.Env = childenv.Scrub(…)` plus `GH_PROMPT_DISABLED=1`
(*chosen*), and its own timeout (`ghTimeout = 30s`, *chosen*, matching
`gitTimeout`). `childenv.Scrub` removes the four model keys
(`internal/childenv/childenv.go:30–36`) and leaves `GH_TOKEN`, so `gh`
authenticates as the operator. Its argv is fixed by the engine (`pr list
--state open --limit 1000 --json number,headRefName,headRefOid`); no model
text reaches it. `GHRunner` satisfies `remote.PRLister` (*chosen*), the
interface the snapshot code takes, so tests use a fake.

ADR 0002 (lines 58–64), 0005 and 0006 say a fifth spawner needs its own ADR;
this is that ADR for one fixed, read-only `gh` invocation, nothing wider. The
implementation adds the file to `allowedExecImporters` and `execSeamCallSites`
and updates `CONTRIBUTING.md` and the `childenv` doc ("FOUR spawners",
`childenv.go:4–21`); `prose_count_test` then derives 5. If review refuses the
fifth seam, the PR part is `unavailable` with reason `no gh seam` (*chosen*)
and the git parts ship alone.

### 2.4 What it does not see

- **Other repositories.** A node that pushes to a clone under `/tmp`, or runs
  `gh pr create --repo other/repo`, changes nothing this snapshot reads.
- **A change reverted before the end.** A branch pushed and deleted, or a PR
  opened and closed (`pr_no_longer_open` only shows PRs open at the *before*
  snapshot), leaves the two snapshots equal.
- **Concurrent human changes.** A human who force-pushes an unrelated branch, or
  updates a PR, while the run runs looks like a node doing it. §5.1's check
  can only say whether the sha the ref now points at is one this run made.
- **Anything outside branches, remotes and open PRs:** tags, releases, PR
  comments, issue comments, CI state.

A ref change cannot be attributed to a node: the snapshot sees state, not
actors. §5.1 attributes a sha, not an act. So every entry is labelled
**"observed during the run"** (*chosen* summary wording), never "made by the
run", in the summary, in `state.json` and in the assessor block. An
attributable entry adds only "points at a commit this run made", since a
human could have pushed the run's commit. Every other entry reads "observed,
not attributable".

## 3. Option A: a forbidden change fails the run; Option B: it is only reported

Both options write the same record; they differ only in whether a forbidden
change (how "forbidden" is known is §4) moves the verdict.

### 3.1 Option A — fail

**What the operator sees.** The ledger table, the change record, and a
failure naming the forbidden entries ("remote changes observed during the run
that point at commits this run made, and `--forbid-remote-changes` was set:
branch_created origin/feat/x …"), with a non-zero exit. The run fails only on
attributable entries (§5.1). Every other entry is printed as "observed, not
attributable" and does not change the verdict.

**False positive** (a change no node made fails the run). A shared
repository is the normal case, not the exception: on jitokim/oh-my-graph,
several agents, merges and bots move refs and open PRs all day. If any entry
failed a guarded run, an unrelated `branch_moved` or `pr_opened` would fail
almost every guarded run that lasts more than a few minutes. That is why only
an attributable entry fails. An unrelated push points at a commit that is
either missing from the invocation repository, or reachable from the run's
base commit (for example a branch pushed at the current `main` tip, or a PR
whose head is already on `main`), or on none of the run's branches and not
created in this leg. Any of these makes it not attributable (§5.1), so it is
reported and does not fail the run. What is left is narrow:
a human pushes a commit this run made, or someone other than a node makes or
fetches a commit into the invocation repository during the leg (§5.1,
condition (b)). The record names the sha, so the cost is a re-run, bounded and
visible.

**False negative** (a change outside §2.4's coverage, an `unavailable` part,
an entry with no new sha such as `remote_added`, or a pushed commit that fails
§5.1's check): the run passes, as it does today, so A is never worse.

**Resume.** Each leg takes a fresh *before* snapshot when it starts
(*chosen*). That includes a resumed leg after a paused gate (`resumeGateLeg`,
`cmd/oh-my-graph/resume.go:196`) and a `--retry-failed` leg (`resumeRetryLeg`,
:241). Each leg's *after* is diffed against that leg's own *before*. One
*before* kept across a gate pause would fail a guarded run for a push that a
human approved during the pause. Changes made between legs are still
reported. The engine diffs the previous leg's *after* against the new leg's
*before* and stores the result as the `between_legs` set (*chosen*). Those
entries are reported only. They are always `not_attributable`, whatever their
sha, and they never fail the run. §5.1's condition (b) is bounded by the
current leg's *before*, so a commit made during a pause is not "created this
leg". A commit on the run's own branches from an earlier leg still passes
condition (a) if a node in a later leg pushes it.

**`--continue-on-fail`** (`cmd/oh-my-graph/flags.go:107`,
`effectiveContinueOnFail`, `internal/schedule/scheduler.go:1911`) prunes a
failed node's subtree. A forbidden change is not a node failure and cannot be
pruned; it is checked after `scheduler.Run` (`scheduler.go:556`) returns,
whatever it returned, and both failures are reported.

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

**False positive:** a line to read. **False negative:** the same gap as A.
**The real cost of B** is #410's case: the goal said "do not push", a node
pushed, and the run exits 0. A reader of the summary sees it; a script on the
exit code does not. B makes the violation visible, not enforced.

**Resume, `--continue-on-fail`, assessor:** as in A, without a verdict change;
the assessor may still judge the goal unmet on the record. **Merge-shepherd-like
graphs:** unaffected; their pushes appear in the record, which is correct.

## 4. How "forbidden" is known

**(i) Parse the goal text** for "do not push", "don't open a PR", "no remote".
Rejected: fragile (negation, quoting, "push back on the design"),
language-dependent, and a model-written goal (a cycle-2 continuation, an agent
calling `auto`) can omit the phrase while meaning it. Nothing else in the
engine reads intent out of prose.

**(ii) An explicit operator flag on `auto` and `run`:**
`--forbid-remote-changes` (*chosen*). Machine-readable, nothing guessed, said
by the person who owns the remote. Persisted like `continue_on_fail`
(`runstate.go:677`) as `forbid_remote_changes` (*chosen*), so `resume` carries
it.

**(iii) A graph-level YAML field** (`remote: forbid`, *chosen*, not adopted).
Deferred, not rejected: a graph's author is not always its operator, and for a
planned graph the planner would write the field, which is (i) with a model in
the middle. `run` takes (ii), so hand-written graphs are covered without it.

**Decision: (ii).** "Forbidden" means the operator passed
`--forbid-remote-changes`. Any **attributable** entry in the change record
(§5.1) is then a forbidden change. Every other entry is reported and never
forbidden. The flag does not take a list of kinds (*chosen*): one switch; it
can fail the run only on the kinds that carry a new sha (`branch_created`,
`branch_moved`, `pr_opened`, `pr_updated`); the other §2.2 kinds are reported
only (§3.1 false negative, §9 item 2).

## 5. Recommendation

### 5.1 The position

The coordinator's recommendation, recorded from Cindy: **fail when the goal
explicitly forbids it, otherwise report only.** That is Option A behind §4's
flag and Option B without it:

- **Without `--forbid-remote-changes`:** Option B. The snapshot is taken, the
  record is written and printed, the assessor sees it, and the verdict is
  today's. Merge-shepherd and every publishing graph run unchanged.
- **With `--forbid-remote-changes`:** Option A. Any attributable entry
  (below) fails the run with
  `RemoteChangeForbiddenError` (*chosen*, in `cmd/oh-my-graph`), exit code 6
  (*chosen*; 1–5 are taken, `exitCodeForError`, `cmd/oh-my-graph/main.go:144`),
  so a script can tell it apart from a node failure. Unlike exits 3 and 5,
  which create no run directory (`cmd/oh-my-graph/main.go:163–168, 180–188`),
  a guarded failure has one, and every node in it may have passed. Its
  `state.json` is written with status **FAIL**, so `runs list` and `show`
  read FAIL. This ADR therefore **amends ADR 0023 §2.6's single-run
  agreement** from "exit 1 ↔ FAIL" to "exit 1 or 6 ↔ FAIL"; exit 0 ↔ PASS and
  exit 2 ↔ PAUSED are unchanged. An entry labelled "observed, not
  attributable" is recorded and reported and never fails the run.

**The attribution check** (*chosen*). Let S be the entry's new sha (§2.2).
`(*GitManager).CommitAttributable` (*chosen*, seam 3, §2.3) runs these
commands in the invocation repository, in this order:

1. `git cat-file -e S^{commit}`: S must be a commit in the invocation
   repository's object store, which the run's worktrees share. A sha that
   someone else pushed and that no node fetched fails here, and the check
   stops.
2. Then one of two conditions must hold:
   - **(a) `run_branch`:** `git merge-base --is-ancestor S B` **fails**, and
     `git merge-base --is-ancestor S R` succeeds for some ref R listed by
     `git for-each-ref --format='%(refname)' refs/heads/omg/<<runID>>/`.
     Those refs are the run's own branches, named by `branchPrefix`
     (`internal/worktree/git.go:81`). B is the run's base commit: the
     invocation repository's `HEAD` read at the start of the first leg and
     persisted in `state.json` as `run_base_sha` (*chosen*), so resumed legs
     keep the same B. The exclusion is needed because each run branch is
     created off the invocation repository's `HEAD` (`Acquire`, `git.go:140`)
     and a branch that carries commits is kept (`cleanupOne`, `git.go:261`),
     so every commit already on `main` when the run started is an ancestor of
     some run ref. Without it, a branch someone pushes at the `main` tip
     would count as the run's. A resumed run whose `state.json` has no
     `run_base_sha` cannot pass (a), only (b).
   - **(b) `created_this_leg`:** `git show -s --format=%ct S` is at or after
     the current leg's *before* snapshot time. This covers a commit a node made
     on a branch it renamed, or, without `worktree:`, on the invocation
     repository's own branch.

An entry with no new sha is never attributable. The commands are local: they
read the object store and refs, never the network. Passing the check means only
that S points at a commit this run made, never that the run moved the ref or
opened the PR, and the label says no more than that. Condition (b) trusts the
committer date, which leaves two gaps. A commit that a human makes in the same
checkout during the leg, or that a node fetches from someone who committed
during the leg, passes it. A node that backdates `GIT_COMMITTER_DATE` escapes it
unless (a) catches the commit. §9 watches both.

"Explicitly" is made machine-checkable by the flag. **The engine never parses
the goal text.**

The remote change is **always** recorded and reported, whether or not the flag
is set. The record costs the same either way, and an operator who did not set
the flag still learns what changed.

### 5.2 The planner

With the flag set, the planner prompt gains one sentence (*chosen* wording,
settled in the golden): the operator forbade remote changes, the engine
observes the remote itself, and no node should push, add a remote or open or
edit a pull request. It is a hint; the snapshot is the enforcement, and it
agrees with #409's rule that check nodes never assert remote state. Without
the flag the prompt is byte-identical to today's.

With the flag and an unavailable part, the run still starts and the launch
line names the part (*chosen*): a repository with no GitHub remote has no PRs
to observe, and refusing would make the flag unusable there. The verdict uses
only the observed parts, and the summary says which were not. When the PR
part is unavailable because `gh` is unusable, the notice `PR part not guarded:
<<reason>>` (*chosen*) is printed twice: at launch and again in the final run
summary beside the change record. An operator who reads only the end of the
output still sees it.

### 5.3 Schema stamp of the flag

`forbid_remote_changes` is not like `remote_changes`: an older binary that
dropped it on resume would finish without the check, so it "would run the rest
of the run differently" (`runstate.go:827–831`). By the conditional-stamp rule
of ADR 0041 §6 and ADR 0044, a guarded snapshot stamps
`SchemaWithRemoteGuard = 5` (*chosen*), which Load (`runstate.go:1051`)
accepts; an unguarded one stays at 3 (or 4 with conventions), and a guarded
one with conventions stamps 5. An older binary refuses to resume a guarded
run rather than silently drop the guard.

## 6. Alternatives considered

- **Keep asserting remote state in check nodes** (the pre-#409 rule). Rejected:
  it failed honest runs, and it is a model judging its own run (§1.3).
- **Option A always on.** Fails every publishing graph (§3.1).
- **Option B only, no flag.** Leaves #410's case at exit 0 (§3.2).
- **Remove `Bash(git *)` and `Bash(gh pr *)` from `plannedToolAllowlist`.**
  Breaks every goal that legitimately commits or opens a PR, and a narrower
  grant (no `git push *`) is not enforceable against bare `git` argv shapes.
- **`ShellVerifier` for the snapshot; git only; a feed event per change.**
  Rejected in §2.3 and §2.2; git-only is the fallback if the fifth seam is
  refused, but PR creation is half of #410.

## 7. Consequences and implementation outline

For the operator: every `run`, `auto` and `resume` summary and the assessor
gain the remote record, and `--forbid-remote-changes` fails on any
attributable entry (exit 6).

### 7.1 Cost

Per leg, a *before* and an *after* snapshot: one `git remote -v`, one `git ls-remote --heads` per remote, and one
`gh pr list` each. For a single-remote repository that is two to four extra
git/gh calls per snapshot, each under a 30s timeout, off the per-node path,
against a run that spends minutes in model calls.

The attribution check (§5.1) runs only for entries with a new sha, and a quiet
run has none. Each such entry costs one `git cat-file -e`, one `git show -s`
and at most one `git merge-base --is-ancestor` against the run's base
commit B plus one per run branch, plus one `git for-each-ref` per leg.
Reading B costs one `git rev-parse HEAD`, once per run. All of these are local, use no network and take
milliseconds. The check runs whether or not the flag is set, because the label
is part of the record.

### 7.2 What it does not fix

Everything in §2.4. It notices a push afterwards and does not stop it;
prevention would need a narrower tool grant, which §6 rejects for now.

### 7.3 Implementation outline

None of these exist at `ac7391c`; every name is *chosen* and given in §2.3,
§4 and §5. By file:

- `internal/worktree/git.go`, `worktree.go`: the git snapshot methods,
  `CommitAttributable` (§5.1) and `RemoteObserver` with a fake (§2.3).
- `internal/remote/gh.go`, `snapshot.go` (new): `GHRunner` (the fifth seam),
  and a pure `Take`/`Diff` over `RemoteObserver` and `PRLister`.
- `internal/runstate/runstate.go`: the §2.2 records, the persisted flag and
  `SchemaWithRemoteGuard` (§5.3).
- `cmd/oh-my-graph/flags.go`, `main.go`, `resume.go`: the flag; *before* and
  *after* around `scheduler.Run` in `executeGraph` (:1118) and at the end of
  `continueRun` (:371); printing beside `led.Print`; the §5.1 error, FAIL
  status and exit code in `exitCodeForError` (:144).
- `cmd/oh-my-graph/goal.go`, `internal/coordinator/assess.go`: `cycleEvidence`
  (:239) copies the record and `assessPrompt` (:331) renders an
  `ENGINE-OBSERVED remote changes` block (*chosen* label);
  `internal/coordinator/coordinator.go`: the §5.2 sentence.
- `internal/invariants/exec_seam_test.go`, `CONTRIBUTING.md`, the `childenv`
  doc, then DESIGN.md's auto section (beside `DESIGN.md:3933–3934`) and
  `docs/RUN-FEED.md`.

## 8. Tests the implementation owes

Unit tests use a fake `RemoteObserver` and a fake `PRLister` (the
`FakeManager`/`FakeRunner` pattern), with no real spawn and no network.

1. **No change, no flag: byte-identical verdict.** Equal fake snapshots give an
   empty record, one "no remote changes observed" line, exit 0, and a planner
   prompt equal to today's golden.
2. **Each change kind is detected.** One table case per §2.2 kind, including
   `branch_moved` carrying both shas and `pr_updated` keyed on a moved
   `headRefOid`; a PR whose `updatedAt` moved but head sha did not yields no
   entry.
3. **Unavailable is never empty.** "not authenticated" on *after* only
   yields `not_compared` for PRs and no `pr_no_longer_open` entries.
4. **Report only without the flag.** A `branch_created` entry with no flag:
   the run's exit is today's, the record is in `state.json` and the summary.
5. **Fail with the flag.** The same entry, attributable through condition
   (a), with `--forbid-remote-changes`:
   `RemoteChangeForbiddenError`, exit 6, the entry named in the message.
   With every node PASS, `state.json`'s status is FAIL and `runs list` and
   `show` read FAIL (ADR 0023 §2.6 as amended in §5.1).
6. **The label.** No rendering of the record (summary, assessor block) contains
   "made by". Each entry carries "observed during the run", an attributable
   one adds "points at a commit this run made", and every other one reads
   "observed, not attributable".
7. **Each leg takes a fresh *before*.** A two-leg run paused at a gate,
   where the fake moves a branch during the pause to a commit created in that
   pause. The second leg takes its own *before*. The move appears only in the
   `between_legs` set, as `not_attributable`. With the flag, the run does not
   fail on it. Leg 2's entries are diffed against leg 2's *before*.
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
13. **A truncated PR list is not compared.** A fake `PRLister` returning
    exactly `prListLimit` PRs records the part `unavailable`, reason
    `truncated`, and yields `not_compared`, with or without the flag.
14. **No new feed event.** The feed schema constant is still 3 and no event
    type is added.
15. **An unrelated push is reported, not failed.** With the flag, the fake
    shows a `branch_moved` to a sha absent from the object store (`cat-file -e`
    fails), plus a `pr_opened` whose head sha exists but is on no run branch
    and predates the leg. Both are recorded `not_attributable` and printed
    "observed, not attributable". The exit is the nodes' exit (0 when all
    pass) and the status is not FAIL.
16. **A push of the run's own commit fails.** With the flag, a
    `branch_created` whose new sha is an ancestor of
    `refs/heads/omg/<<runID>>/<<worktree>>` and not of `run_base_sha` is
    `run_commit` via `run_branch`, and
    the run exits 6. The same holds via `created_this_leg` for a commit made
    after the leg's *before* on a branch outside the prefix.
17. **No-sha entries are never attributable.** `remote_added`,
    `branch_deleted` and `pr_no_longer_open` are `not_attributable` and never
    fail a guarded run.
18. **The run's base commit is not attributable.** With the flag, a
    `branch_created` whose new sha is the run's `run_base_sha` (an ancestor of
    every `omg/<<runID>>/` branch) is `not_attributable`, and the run does not
    exit 6.
19. **gh unusable is said twice.** With the flag and a fake `PRLister` that
    returns "not authenticated", the run starts, and both the launch line and
    the final summary carry `PR part not guarded`.

## 9. Falsification

1. **Guarded runs fail mostly on changes no node made.** Attribution should
   make this rare. If most of the first twenty `--forbid-remote-changes`
   failures still point at commits that no node pushed, most likely through
   condition (b) (a human committing in the same checkout, or a fetched
   commit), the check is too wide. Drop (b) and keep (a) before dropping the
   flag.
2. **Node pushes are labelled not attributable.** If a guarded run's node is
   found to have pushed and the entry read "observed, not attributable" (for
   example a backdated commit off the run's branches, or a `remote_added`,
   which has no sha), the check is too narrow. Widen it (say, to the reflog
   of the run's worktrees) rather than counting every entry again, because
   shared repositories are the normal case (§3.1).
3. **Forbidden pushes still land unflagged.** If a guarded run is found to have
   pushed here and the record shows nothing, either a part was `unavailable`
   (the launch warning should become a refusal) or §2.4's reverted-change gap
   is hit in practice (a per-node snapshot is owed).
4. **Operators never pass the flag.** If "do not push" goals keep running
   without it, reconsider (iii) or make the flag more visible; never (i).
5. **The snapshot costs more than a node.** If snapshots routinely approach
   their timeouts, drop to remotes and refs only, with PRs observed on demand.
6. **The fifth seam is refused.** Then §2.3's fallback stands: git parts only,
   PRs `unavailable` with reason `no gh seam`, #410 half-answered until a seam
   is agreed.
