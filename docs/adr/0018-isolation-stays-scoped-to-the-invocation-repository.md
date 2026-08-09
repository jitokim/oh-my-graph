# ADR 0018 — Isolation stays scoped to the invocation repository; a second repository is disclosed, not provisioned

- Status: **Accepted — nothing is built.** Multi-repository managed worktrees
  are refused for now. What this record does produce is the *shape* the feature
  would have to take if the evidence ever arrives (§1's user-named `--repo`),
  the three questions that make it expensive (§2–§3), and a pre-registered
  measurement (§Falsification) whose failure converts this "no" into a "build
  §1". A decision not to build is only honest if it says what would change it.
- Date: 2026-08-07
- **Revised 2026-08-07**, same day, after a deep review of the accepted text.
  The decision does not move — every revision made the refused feature
  *more* expensive, not less. What changed: §1 answers
  `validatePlannedNodeWorktree`'s second reason instead of only its first;
  §2 retracts "a resume needs nothing new" and names what it needs; §2's
  typo check is corrected (a detector's path resolution is not an actuator's);
  §3 records a managed-path collision and an unwired cleanup fan-out; §6 names
  which of two forms the follow-up is, and why the other one cannot exist;
  §Falsification stops defining its population by a value nothing persists.
  Nothing in the review was rejected.
- **Baseline measured 2026-08-09: 0%** (0 of 6 HEAD-moving nodes; n = 18
  qualifying nodes over 6 real `auto` runs), taken before the §6 clause exists
  — see §Falsification, *"Result 2026-08-09"*. **The decision does not move and
  §1 is not built**: the threshold judges §6's advisory, and there was no
  advisory here to disobey. But the decision is **provisional** from this date,
  and that section says on what.
- Issues: [#103](https://github.com/jitokim/oh-my-graph/issues/103) — the
  second half, the one #123 and #129 did not touch.
- **Amends nothing.** ADR 0005's single-repository scope is *affirmed*, not
  narrowed or widened, and carries a dated pointer here. **ADR 0004 does not
  move**: no ceiling layer changes, and `validatePlannedNodeCwd` and
  `validatePlannedNodeWorktree` both stay exactly as closed as they are.
- Line and symbol citations are anchors for a reader, not addresses the code
  maintains: when one disagrees with the file, trust the named symbol.

## Context

### What was reported, and which half is still open

Issue #103 reported two symptoms of one root cause, from a real run
(`20260804-032843.062915000-1`) whose goal named a second local repository by
absolute path. That run directory is no longer on disk; the durable record of
it is the issue itself — see §Falsification, *"Where the evidence lives"*, which
is also why the measurement below has to capture its own.

1. **A false FAIL.** The plan's `final-check` node asserted
   `git -C <repo B> rev-parse --abbrev-ref HEAD`, a checkout no node had ever
   switched. Both pull requests existed and were correct; the run reported
   FAIL. **Fixed in #123**: `branchEvidenceRule` now makes the planner assert
   repository state (`gh pr list --head`, or a branch ref) and forbids the
   local-HEAD form by name, with or without `-C`.
2. **A real collision.** A node changed a shared checkout's HEAD while an
   unrelated concurrent process was standing in it; that process's early edits
   landed on the wrong branch. It noticed and recovered. **Nothing in the
   engine would have**, and #123 does not touch this. #129 made the limit
   visible — SECURITY.md states it, and `auto` prints a plan-time warning
   (`scanUnisolated` / `noteUnisolatedPaths`) naming every checkout outside the
   invocation repository that the goal or a planned prompt names.

This ADR is about the second one.

### The correction that reframes the whole question

The issue's title — *"cross-repo nodes have no worktree isolation"* — is true,
and it is true of same-repo planned nodes as well. **`auto` provisions no
managed worktree anywhere.** `validatePlannedNodeCwd` and
`validatePlannedNodeWorktree` both refuse the field, so every planned node
starts in the tree the user invoked from, with nothing placing it anywhere else
and nothing isolating it there. A node may still `cd` away or cut a worktree of
its own; that is the node's doing, and the engine neither provides it nor
enforces it.
`UnisolatedScan`'s own doc comment says this in the strongest available terms:

> THE BOUNDARY IS ONE OF OWNERSHIP, NOT OF PROTECTION. `auto` rejects `cwd:`
> and `worktree:` at plan time, so it provisions no managed worktree anywhere —
> the invocation repository included […] What the checkouts reported here have
> that it does not is that the user did not open them for this run.

So the reporter's repository A was as unisolated as repository B. The
difference between them is not a mechanism; it is consent. The reporter
believed `argo-impl` had run in "oh-my-graph's automatic worktree" in repo A —
it had not, and could not have. **Both** `impl` nodes improvised their own
worktrees, and the run's evidence is consistent with that: neither repository's
main checkout was on the feature branch at the end.

That correction splits #103's remaining half into two independent decisions,
which the issue conflates and which have nothing in common but the word
"worktree":

- **(A) May a *hand-written* graph declare a managed worktree in a repository
  other than the invocation one?** A widening of a feature only a trusted graph
  author can reach. It has a trust story today: the author who writes
  `worktree:` can already write `cwd: <anywhere>`, which is strictly more
  dangerous — an un-isolated claude subprocess in an arbitrary directory.
- **(B) May a *plan* cause provisioning, anywhere?** This is what #103 needs,
  because the reporter ran `auto`. It requires reopening
  `validatePlannedNodeWorktree`, which ADR 0005 closed for a reason that has
  not weakened: **provisioning is not a tool call**, so no permission mode, no
  allowlist and no ceiling layer ever sees it. An unreviewed plan that can name
  a directory can make the engine create checkouts and branches in it.

Building (A) alone would ship a capability the reporter cannot use and leave
issue #103 open. Building (B) is the decision this record has to make, and
(A) is only worth making at the same time or not at all.

### What the engine can enforce, and where that stops

A managed worktree isolates because the engine *places the node in it*: `cwd`
is set for the node, so its `git` commands land in the managed checkout by
default and the user's tree is never the playground. That property is real, and
it is the whole value of ADR 0005.

**`cwd` is singular.** A node can be placed in exactly one directory, so
isolation can be *enforced* for a node's primary repository and only
*requested* for any other. Two node shapes follow, and they are not alike:

- **One repository per node** (the reporter's `mem-impl` / `argo-impl`):
  enforcement is real. Each node has one primary repository; place it in a
  worktree of that repository and it is isolated by the same mechanism ADR 0005
  already ships.
- **A node spanning repositories** (the reporter's `final-check`): enforcement
  is impossible in principle. The second path can only reach the node as text
  in its prompt, and text is advisory. Notably, this shape no longer needs a
  checkout at all: after #123 a fan-in check asserts remote state, which every
  worktree of a repository shares.

So the honest ceiling on any multi-repository design is: it can deliver a
*path*, and it can enforce placement for the one repository a node is placed
in. It cannot stop a node from `cd`-ing somewhere else. The hazard #103
reported — a node running `git checkout` in a shared tree — is stopped by a
node's compliance, and provisioning changes only *what the node has to comply
with*, not *whether it complies*.

## Decision

### 0. Multi-repository worktree provisioning is not built

`worktree.GitManager` keeps its single `repoDir`, `<run-dir>/worktrees/` keeps
branching from the invocation repository's HEAD, `validatePlannedNodeCwd` and
`validatePlannedNodeWorktree` stay closed, and the boundary stays where
SECURITY.md and #129's warning put it. The six remaining sections say why, in
the order the questions were asked, and what the answer would have to be if
this is ever revisited.

### 1. If a second repository is ever named, it is named by the user — never by the planner, and never by the detector

Three candidate sources, one admissible.

**By the planner: refused, and this is the same refusal ADR 0005 already
made.** A planned field naming a path is the exact surface
`validatePlannedNodeCwd` closes. The argument is not about paths being scary;
it is that provisioning sits *outside every ceiling this project has*. Layers
0–5 (ADR 0004) bound what a node may *call*. `git worktree add` is not called
by the node — it is run by the engine, before the node exists, on trusted
code's authority. A plan that can steer it has made the engine act on
untrusted text with nothing between them. Every other planner-supplied
dangerous thing in this codebase has the same disposition: `success_check.verify`
is refused, `agent:` is refused, `Skill` is granted by trusted code and never
declared (ADR 0017 §2). Provisioning belongs in that list, and it is already in
it.

**That refusal has a second reason, and §1's own shape breaks it — so it is
argued here rather than skipped.** `validatePlannedNodeWorktree`'s doc gives
two: provisioning sits outside every ceiling (above), and *"the same locality
reason: a planned node always runs in the invocation's working directory,
never in a checkout of its own making."* A `--repo` shape violates the second
by construction. The defense is that locality is a **proxy for two properties,
and the proxy is the weaker statement of both**: that a planned node's writes
land where the user can see them, and that the node does not choose its own
location. A managed worktree named by the user keeps both — the checkout is
under `<run-dir>/worktrees/`, created by trusted code off a HEAD the user's
own repository already had, and the node is *placed* in it rather than
selecting it. It arguably improves on the status quo, where a planned node
edits and commits directly in the tree the user has open. So locality is not
what decides this record; the ceiling argument is, and §1's bounded-selection
form exists to answer the ceiling argument. A future proposal that thinks it
has beaten locality has beaten the easier half.

**By detection: refused, and this is the interesting one, because #129 shipped
a detector and "warn → provision" reads like a promotion.** It is not a
promotion; it is a change of trust class, and the detector was built for the
other class.

- **Its false-positive budget is a disclosure budget.** `isToolInstallation`
  exists because *"this warning is only worth printing while it is still
  read: one line of furniture in it and the user learns to scroll past the
  whole block"*. The cost of a wrong warning is a line of noise. The cost of a
  wrong *provision* is a branch and a registered worktree in a repository
  nobody asked about. The rule was tuned against the first cost and is
  correctly tuned for it.
- **It under-detects, by its own admission, and provisioning cannot.** The
  printed warning tells the user so: it cannot see a path a node builds at run
  time, one arriving through an `--input` or a parent's artifact, a repository
  reached by a relative path, or what a node does once it is there. A warning
  that covers 80% of cases is 80% useful. A provisioning rule that covers 80%
  of cases isolates some nodes and not others, with no signal telling the user
  which — strictly worse than isolating none, because the uncovered case now
  looks like the covered one.
- **And it reads planner-authored text.** `scanUnisolated` scans the goal *and
  every planned node's prompt*. Provisioning off a prompt-text match is
  planner-naming with a regular expression in between: the planner writes a
  path into a prompt, the detector finds it, the engine acts on it. The
  refusal above would be reintroduced by the mechanism built to disclose it.

**By the user: the only admissible form.** A repeatable flag —
`--repo <path>`, on `auto` and on `run` — naming a repository the user vouches
for, exactly as `--verify-cmd` names a shell command the engine runs
([ADR 0016, build evidence](0016-build-evidence-is-a-user-supplied-engine-command.md)
— not the same-numbered [0016 on retries](0016-a-retry-carries-the-attempt-it-is-repeating.md)). The precedent is the point: *the engine does the dangerous
thing only when the user supplies it, and the plan is never granted anything.*
Under that form the plan-time reopening is bounded to a **selection, not a
string**: a planned node may carry `repo: <name from the user's own list>`,
validated against the flags the user typed, and anything else — a path, an
unknown name, an empty value — is refused by the same
`validatePlannedNodes` sweep that refuses `cwd:` today. The planner chooses
*which of the user's repositories* a node works in. It never chooses *that a
repository exists*.

That is the form. It is not being built, for the reasons in §2 and §3.

### 2. Partial failure would be a non-event; resume would not be, and the first version of this section said it was

If §1 is ever built: **no run-level provisioning phase.** `Acquire` is called
per node, on demand, the first time a node names a worktree, and that is what
keeps "repo A provisioned, repo B failed" from being a new state at all. Repo
B's failure fails repo B's nodes, exactly as a worktree failure fails one node
today; halt-on-fail and retry behave as they already do, and the ledger needs
no new disposition. That much stands.

**Resume does not, and this is a correction.** The claim was that `state.json`
records the same per-node verdicts and a resumed leg re-provisions from disk
through `Acquire`'s existing disk-aware path. That path is per *manager* and
keyed by worktree **name** alone: it can re-adopt a lane, and `validateOwnWorktree`
makes it refuse a directory belonging to another repository — but it cannot
invent the manager for repository B. There is exactly one manager today.
`worktreeManagerFor` builds `NewGitManager("", <run-dir>/worktrees, runID)`,
with `repoDir` empty, meaning the process's working directory; `run`, `auto`
and `resume` share it deliberately, so that both legs manage the same location.
Under §1's shape the `name → path` binding for repository B lives **only in the
`--repo` flags the user typed on the first leg**. Nothing persists it. A
resumed leg would therefore either refuse every foreign-repo node, or silently
drop the binding and run that node in the invocation repository — the second
being the worse outcome, because it looks like success.

The precedent is already in the tree and it cuts against the original claim:
`runstate.NodeToolPolicy` is persisted *precisely* because "resuming a planned
graph without it would silently drop the Layer-1/2 guard that keeps an
unreviewed plan inside its bounds". A repository map is the same class of
datum — a trust decision the user made once, which the run must re-impose
rather than re-derive. So the shape needs one of exactly two things, chosen up
front:

- **A snapshot-level field**, mirroring `Snapshot.ToolPolicies`: the resolved
  `name → path` map, written when the plan is accepted and re-imposed on
  resume. A `--repo` on `resume` may then only *re-confirm* what the snapshot
  already names — a resumed leg able to introduce a repository would be the
  user editing the run's trust set mid-flight, with the earlier legs' nodes
  already judged under the old set.
- **An explicit refusal**: a multi-repository run is not resumable, stated
  when `resume` loads the snapshot, not discovered at the first foreign node.

Either is a blocking requirement. This section is the one whose job is to say
a cost is small, and it under-stated its own.

The other addition worth making is **plan-time validation, not plan-time
provisioning**: each `--repo` is checked, by `stat` alone and before anything
spends, to be a git checkout root outside the invocation repository. That
check composes what `scanUnisolated` already has — `checkoutRootOf` resolves
the root, `withinDir` decides "outside the invocation repository" — with one
difference that matters more than it looks. **The resolved root must equal the
path the user typed.** `checkoutRootOf` deliberately "walks rather than
requiring path itself to exist, so a goal naming a file a node has yet to
create still resolves to the repository that file would land in": correct for
a detector, wrong for an actuator. `--repo ~/IdeaProjects/oh-my-grpah` walks up
past the typo, binds to whatever checkout is above it, and the engine then
creates a branch and a `.git/worktrees/` entry — §3's debris exactly — in a
repository the user never named. Requiring `root == given path` is what makes a
typo die instead of bind.

And the check may **not** be `isForeignCheckout` reused wholesale: it composes
`isToolInstallation`, whose suppressions exist to keep a printed warning
readable. A user who types `--repo ~/.dotfiles` means it, and a detector tuned
to ignore that path is tuned for the other trust class — the one §1 refuses to
let reach actuation, in both directions.

An eager "provision every repository at run start" step is the tempting
alternative and it is wrong twice: it converts a per-node failure into a
run-level one, and it creates branches in the user's repositories for lanes a
halted run may never reach.

**This question is not what makes the feature expensive.** §3 is.

### 3. Cleanup across repositories is the reason the answer is "not now"

The directory is ours; the debris is not. `git` permits a worktree of
repository B to live anywhere on disk, so the *filesystem* footprint stays
inside the run directory — **though not at today's path.** `Acquire` keys its
lanes by name alone under one `baseDir`, so two repositories each declaring a
node named `impl` land on one directory; the second manager's
`validateOwnWorktree` refuses it ("belongs to another repository"). Loud rather
than silent, which is the right failure — but the run then dies on a name
coincidence between two graphs' node names. The managed path needs a
repository component (`<run-dir>/worktrees/<repo>/<name>`), which is a change
to the manager's `baseDir` contract and therefore to ADR 0005's. Branches are
already safe: `omg/<run-id>/<name>` is a ref inside each repository separately,
so two repositories cannot collide on one.

Two things do not stay inside the run directory at all:

- **The branch.** `omg/<run-id>/<name>` is a ref in repository B. ADR 0005's
  cleanup rule *retains* any branch that carries commits — deliberately, because
  cleanup may remove directories and never work — so a successful multi-repository
  run's normal outcome is a retained ref in a repository the user did not open.
- **The administrative entry.** Repository B's `.git/worktrees/<name>` points
  at a path under `~/.oh-my-graph/runs/`. If that directory is later removed,
  repository B carries a stale worktree entry until someone runs
  `git worktree prune` in it.

Now compose that with two facts this project has already established:

- **An abandoned run never cleans up.** ADR 0015: a run whose process died is
  *derived* as abandoned and never repaired. `Cleanup` runs at leg end in
  `cmd/oh-my-graph`; a killed process runs none of it. Today that leaves
  debris in the repository the user is standing in, where they will see it.
- **Nothing sweeps run directories.** Verified again for this record: outside
  tests, `os.RemoveAll` appears in exactly one place in the tree
  (`internal/coordinator/skillstage.go`, inside `Materialize`). No sweeper
  exists, so nothing will ever tidy a foreign repository's dangling entry
  either.

So the shipped cost of multi-repository provisioning is: **refs and worktree
registrations accumulating in repositories the user did not open for the run,
with no surface that lists them.** A stray worktree in `<run-dir>/worktrees/`
is ours to explain; a stray one in somebody else's working directory is
somebody else's problem arriving without warning. Any build must therefore also
ship (a) cleanup notes that name the *repository* and not only the branch,
(b) something that can enumerate and prune what a run left in a foreign
repository — which is a new command with its own record of what a run touched,
not a flag on an existing one, and (c) a **cleanup coordinator, which is not a
`Provider`**: ADR 0005 put `Acquire` on `worktree.Provider` and deliberately
left `Cleanup` off it, so the fan-out belongs to a separate object over the
concrete managers and widening the interface is not an option here. `Cleanup` is
invoked exactly once, on one manager, at leg end (`cmd/oh-my-graph/main.go`,
and again in `resume.go`). N managers need something that fans the call out to
all of them and merges their notes, or §3's own cleanup obligation is unwired — every repository but the invocation one would keep
everything, on the *successful* path, which is the one the user is least
looking at.

That is the cost, and against the evidence available (one report, recovered) it
is not paid today.

### 4. The seam was never the obstacle

Asked directly, so answered directly: **yes, this stays inside seam 3, and
trivially.** `GitManager.repoDir` is already a field, and every git invocation
already goes through one `gitCmd`. Multi-repository means one manager per
repository behind the same `worktree.Provider` interface, or one manager
holding a `repoDir` per lane — either way the only `os/exec` importer in
`internal/worktree` is still `GitManager`, `internal/invariants`'
`exec_seam_test.go` is unchanged, `childenv.Scrub` still has exactly one call
site per spawner, and `FakeManager` still keeps the scheduler spawn-free.
There is no fifth spawner here and no ADR is owed for one.

It is worth being explicit that this is the *cheap* question. The exec-seam
invariant is a good rule precisely because it is easy to check; it does not
detect the expensive problems, and in this case it correctly reports nothing
while §1, §3 and §5 report plenty.

### 5. A second repository can only reach a node as text, and for a planned node that is `cwd` by another name

Today a node in a managed worktree needs to do nothing differently: `cwd` is
set for it. With two repositories, `cwd` still holds one of them, so the other
has to arrive as an interpolated path in the prompt — `{{ worktrees.<name> }}`
or equivalent, a new template surface alongside `{{ inputs }}` and
`{{ artifacts }}`.

For a hand-written graph that is unremarkable: the author wrote both the
worktree declaration and the prompt that references it.

For a planned node it is the refused surface wearing a placeholder. A planner
that may write `{{ worktrees.b }}` into a prompt is a planner choosing which
repository a node works in — the choice `validatePlannedNodeCwd` exists to
deny — and the fact that the *path* comes from trusted code does not change who
made the choice. Under §1's bounded form that is tolerable only because the
placeholder can name nothing the user did not type, and only because the
resolution stays in trusted code. Any looser form — free-form paths, planner-
declared worktree names, a detector-derived list — reopens the surface for real.

And the shape that most wanted this is the one that no longer needs it: a
fan-in check node spanning both repositories has, since #123, no reason to be
in either checkout.

### 6. What is done instead

Nothing in the engine, and one follow-up that is not this ADR's to implement:

**Turn #129's detection into a planner instruction, not into an actuation —
and an unconditional one.** The warning today tells the *user* to go and reword
their goal — *"If a node must work in one, say in the goal that it has to
create its own git worktree there first and stay inside it."* That is a correct
instruction addressed to the wrong party: trusted code can say it directly to
the planner, the way `branchEvidenceRule` says the remote-state rule, and
`branchEvidenceRule`'s own paragraph is its home — that rule already carries
multi-repository text ("For a branch in a repository other than the one
oh-my-graph was invoked from…"), so the clause lands next to the one thing the
planner is already told about foreign repositories.

**Unconditional is not a stylistic preference; the detection-conditioned form
does not exist.** `scanUnisolated` runs strictly *after* `validatePlannedNodes`,
on the planner's own reply, and is fixed there deliberately — "computed HERE,
on the planner's own prompts […] so what it reads is what the planner actually
wrote". Its result cannot exist at the moment the planner prompt is built. An
instruction gated on it would have to be a second planner call: a re-plan
spending the one bounded repair retry that a validation refusal already claims,
on a plan that is valid. So the clause ships as a standing rule every planned
graph is written under. It costs one paragraph of prompt, no field, no seam, no
reopening, and it is the same intervention that fixed #103's first half.

It is advisory — a node may ignore it — and it must be labelled that way
wherever it is described. **Whether it is obeyed is the measurement below**, and
it is the measurement that decides whether §1 gets built.

**Owner and ordering.** This ADR does not implement it, and it needs a tracking
issue of its own; until that issue exists this is a note in a record and not
work anybody has taken. The ordering is a precondition on that issue, not a
detail it can discover later: the §Falsification baseline must be captured
*before* the clause lands, because afterwards the status quo's number can never
be taken again.

## Alternatives considered

- **Build (A) now — hand-written multi-repository worktrees only.** This is
  genuinely a real gap: a hand-written graph that must work in a second
  repository has only `cwd: <that repo>` today, which is the un-isolated
  shared checkout #103 collided in, and no way to ask for an isolated one.
  Rejected for now anyway: it does not close #103 (the reporter ran `auto`),
  it pays §3's cleanup cost in full for a case nobody has reported, and
  closing an issue with a feature its reporter cannot use is how a backlog
  learns to lie. It is the first thing to build if a hand-written multi-repo
  graph is ever reported.
- **Promote the #129 detector from warning to provisioning.** Rejected in §1.
  A detector whose false positives cost a line of text is not a detector whose
  false positives may cost a branch in someone's repository, and it reads
  planner-authored text.
- **Take an advisory lock in the foreign repository instead of provisioning
  one** (ADR 0015's `flock` applied to repo B, so a collision is at least
  *detected*). Rejected: a lock protects only against processes that take it,
  and the process that collided in #103 was an unrelated Claude Code subagent
  that will never look for one. It would add a mechanism whose failure mode is
  a false sense of exclusivity — the worst kind this project can ship, and the
  same shape ADR 0017 §8 refuses for a silent fallback.
- **Refuse a plan whose text names a foreign checkout.** Rejected, for the
  reason `UnisolatedScan`'s doc already gives: a goal spanning two repositories
  is a legitimate thing to ask for, and refusing it would break a working use
  case to fix a disclosure problem.
- **Document the limit better and stop there.** This is close to what is being
  decided, and it is not quite it — SECURITY.md and the plan-time warning
  already say it as clearly as prose can, and #103 stayed open anyway because
  the reporter read the documentation and still wanted the collision not to
  happen. Documentation is the floor this decision stands on, not the decision.

## Consequences

**Positive**

- The surface `validatePlannedNodeCwd` closed stays closed, and the ceiling
  argument in ADR 0004 keeps its shape: nothing an unreviewed plan writes can
  make the engine act on a directory.
- No new failure mode is added to the path that provisions the ordinary,
  single-repository case — which is every run this project has ever measured.
- No refs, no worktree registrations and no stale administrative entries
  accumulate in repositories the user did not open, which is the class of
  debris nothing in this tree cleans up.
- The `--repo` shape is written down. A future proposal starts from a form
  that has already been argued rather than from the convenience that reopens
  the surface.

**Negative — stated plainly, because they are the cost of this decision**

- **#103's second half stays open, and the hazard is real.** A node can still
  switch HEAD in a shared checkout and collide with an unrelated process. The
  user's protection is a plan-time warning plus a node's compliance, and
  compliance is exactly what has never been measured here.
- **A hand-written graph still has no isolated way to work in a second
  repository.** `cwd:` is the only tool, and it points at the shared checkout.
- **The recovery in #103 was luck and is being treated as tolerable.** The
  other agent noticed. Nothing in the engine did, and after this decision
  nothing in the engine will.
- The plan-time warning's honesty is now load-bearing: it is the only thing
  standing between a multi-repository goal and the collision, so any change
  that makes it quieter — a broader `isToolInstallation`, a narrower
  `pathMention` — is a change to a safety surface and must be reviewed as one.

## Falsification — the measurement that would convert this "no" into §1

Pre-registered, in the house form: written before the data, and a result that
goes the other way is recorded rather than retried away.

**The primary measurement is compliance, and the method already exists** (ADR
0017's acceptance test used it): every node runs with session persistence on
and `runstate.NodeRecord.SessionID` names its transcript under
`~/.claude/projects`, so a node's actual git commands are readable, which is
the exact thing #103's first triage comment could not read from the run feed.

- **Population.** Planned nodes whose prompt names a checkout outside the
  invocation repository. **Not simply "whatever `UnisolatedScan` reported":**
  the scan is computed, printed, and then dropped — `Unisolated` reaches no
  `runstate` field and no `runfeed` event, unlike ADR 0017's acceptance test,
  which could measure off `NodeRecord.SessionID` because the snapshot persists
  it. Recomputing the scan from `Snapshot.Graph`'s prompts is possible, but
  only against an invocation root the snapshot does not record either. So the
  population is defined by **what the collector captures at run time** — the
  plan printout, or the invocation directory alongside the snapshot — per run,
  as part of taking the sample. A population derived after the fact from
  `state.json` alone is not this population and does not count toward the ten.
- **What the population excludes, on the record rather than by accident.** A
  checkout named *only in the user's goal* — an `UnisolatedPath` with `InGoal`
  true and empty `NodeIDs`, which is exactly how #103's own run named
  repository B — is not reached by a prompt-based population at all, and the
  goal text itself persists only for goal-loop runs (`Snapshot.Goal` is nil for
  a single-cycle `auto`). Those nodes are in scope only when the collector
  records the goal with the run. A sample that drops them silently is measuring
  the half where the planner was explicit enough to write the path into a
  node's own prompt, which is not the same population and plausibly not the
  same compliance; if they cannot be collected, the result is reported as
  prompt-mentions only and says so in the number.
- **Metric.** The fraction whose transcript records a `git worktree add` (or a
  clone) in that repository **before** any command that moves that repository's
  HEAD (`git checkout`, `git switch`, `git reset`, a branch-creating commit in
  the shared tree), **and** whose every such HEAD-moving command then runs
  *inside the path that setup created* — so the created path is recorded
  alongside each command in the sample, and a node that cuts a worktree and then
  runs `git checkout` back in the shared checkout counts as non-compliant. Setup
  alone proves nothing: the collision this measures is a command landing in the
  shared tree, not the absence of a `worktree add` before it.
- **Threshold, fixed now.** Over **at least 10** such nodes: **≥ 80% →
  advisory works, this decision stands and hardens.** **< 50% → advisory has
  failed on its own terms, and §1's user-named `--repo` form is built**, with
  §2's lazy provisioning and §3's two cleanup obligations as blocking
  requirements rather than nice-to-haves. Between 50% and 80% → the sample is
  extended, not reinterpreted.
- **Confounder to record, not to explain away.** If the §6 planner instruction
  ships first, this measures the instruction and not the status quo, and the
  status quo's number will then never be known. Take a baseline of at least 5
  nodes before that instruction lands, or record that no baseline exists. §6
  carries this as a precondition on its own tracking issue, because "take the
  baseline first" is a race the measurement loses by default: the clause is one
  paragraph of prompt and the sample is ten runs.
- **Where the evidence lives.** The reported run
  (`20260804-032843.062915000-1`) is no longer under `~/.oh-my-graph/runs/`, so
  the factual base of §Context is not re-checkable from this machine; what
  survives is #103's issue body and its two triage comments, which quote the
  plan and the offending assertion. Nothing sweeps run directories (§3), but a
  user may remove one, and `~/.claude/projects` transcripts are the user's to
  prune. A sample is therefore only as durable as its captures: copy what is
  measured — the node's git commands, the prompt, the invocation root — into
  the measurement's own record when it is taken, rather than assuming the run
  directory and the transcript are both still readable when the tenth node
  arrives.

**Two things also falsify this decision, without any counting:**

- a second, independent report of a cross-repository collision; or
- a recurrence reported *after* the #129 warning was printed and read — that
  is the direct refutation of "disclosure is enough".

**What does not falsify it:** a multi-repository goal being awkward to write, a
user asking for the feature in the abstract, or a plan that would have been
tidier with two managed checkouts. Those are documentation and ergonomics
costs, and this record accepts them.

**What this decision does not claim:** that a collision is unlikely, or that
compliance is high. It claims that the cost in §3 is not yet paid for by the
evidence in hand, and it names the evidence that would pay for it.

### Result 2026-08-09 — the baseline, taken before the §6 clause exists: **0%**

**0 of 6 = 0%.** n = **18** qualifying nodes over **6** real `auto` runs, taken
**2026-08-09**. Across all twenty node transcripts in the sample the string
`git worktree add` appears zero times and `git clone` zero times; six nodes
moved a foreign checkout's HEAD and all six did it in the shared checkout the
user did not open, leaving all six fixtures standing on a new feature branch
with a single entry in `git worktree list`. #103's collision shape, six times
out of six. Cost $9.15.

**Where the raw rows live.** Report:
[`docs/measurements/0018-unisolated-compliance-baseline.md`](../measurements/0018-unisolated-compliance-baseline.md).
Sealed pre-registration, per-node rows, capture/extract/scrub scripts, the six
goals, per-run captures (plan printout, invocation root, goal, `graph.json` /
`state.json` / `events.jsonl`, both fixtures' git state before and after) and
the complete ordered git-command list per node:
[`docs/measurements/probes/0018-unisolated-baseline/`](../measurements/probes/0018-unisolated-baseline/).
Raw transcripts are deliberately **not** committed (a node's system prompt
carries the operator's whole local skill corpus, and this repository is
public); the excerpts and `sessions.txt` are what this section's *"a sample is
only as durable as its captures"* asks for. Every path measured is a throwaway
fixture under `/tmp/omg-0018/fixtures` with no remote — no real checkout
appears anywhere in it. §6 was verified unimplemented before and after the
sample (`branchEvidenceRule` carries only its worktree *caveat*), so this is
the status quo number, and the ≥5-node precondition above is met by 18.

**Population caveat — three parts, all of them load-bearing:**

- **The metric is silent on a node that moves no HEAD**, and both literal
  readings are degenerate (a read-only node is either non-compliant for having
  no `worktree add`, or compliant for free). PREREG.md fixed the disambiguation
  before any data and all three are reported: headline **0/6 = 0%** (denominator
  = nodes that actually moved the target's HEAD), strict-literal 0/18 = 0%,
  lenient-literal 12/18 = 67%. The last is not a compliance figure — it counts a
  node that read a file as having cut a worktree — and is carried only because
  the pre-registration promised it.
- **This is still a prompt-mention population by construction.** The goal-only
  stratum this section calls the harder half (`InGoal` true, empty `NodeIDs`,
  how #103's own run named repository B) did not arise: all six warnings read
  `named by the goal and nodes "…"`. The goal was captured with every run, so it
  would have been measurable had it occurred — the number is not truncated by an
  unmeasured remainder — but a future sample that does hit that path must report
  the stratum separately.
- **Two nodes were excluded** whose prompts never named the foreign checkout
  (`docs-site`, `client-timeout`). `scanUnisolated` did not list them. Both ran
  `checkout -b` in the *invocation* repository, which is outside this population
  by definition and, per this ADR's own correction, equally unisolated.

Three deviations from the sealed pre-registration are recorded in the report
rather than edited into PREREG.md; the largest is that runs 5–6 were added as a
post-hoc robustness arm with looser goal wording, which could only weaken the
finding and did not (runs 1–4 alone: 0/4 over a population of 12).

**What the number does to the threshold.** Read literally it is below the 50%
line, and the threshold above says that line builds §1. **§1 is not built, and
this record does not treat the trigger as met**: the threshold judges *whether
the §6 advisory is obeyed* — "Whether it is obeyed is the measurement below" —
and a baseline is advisory-free by construction, so "advisory has failed on its
own terms" cannot be concluded from it however low it is. The population-size
condition (18 ≥ 10) is met; the condition being tested is not. Sharpening this:
in **6 of 6** non-compliant rows the offending `checkout -b` in the shared
checkout was **written into the node's prompt by the planner**, not improvised
by the node. Compliance with an instruction never given was always going to read
0%, and §6 is aimed at exactly the party that failed.

**The decision is provisional from this date.** It does not move — it never
rested on compliance being high, it rests on §3's cleanup cost, and it already
said in Consequences that *"compliance is exactly what has never been measured
here."* What changes is that the blank is filled with a zero, so the third
Negative — *"the recovery in #103 was luck and is being treated as tolerable"* —
now stands on a measured base rate rather than on one report. What stays open is
that the decision's remaining support is a cost argument alone, and the sample
that would settle it has not been taken.

**What the full sample would settle, and nothing short of it does:**

1. **Whether the §6 clause moves this number at all** — the post-clause figure
   over **≥ 10** qualifying nodes, with the population defined and captured the
   same way (run-time capture of the plan printout, invocation root and goal;
   judged from transcripts). **< 50% there is the branch that builds §1's
   user-named `--repo`**, with §2's lazy provisioning, its snapshot-or-refuse
   resume answer, and §3's two cleanup obligations as blocking requirements.
   ≥ 80% hardens this decision. Between the two extends the sample.
2. **Which party actually fails.** The baseline cannot distinguish a planner
   that prescribes the wrong command from a node that ignores a right one,
   because only the first existed. A post-clause sample where the planner stops
   prescribing `checkout -b` in the shared tree and nodes still do not cut
   worktrees is the first real evidence of node-level disobedience — and it is
   the evidence that would make §1 the only remaining lever.
3. **Whether the goal-only stratum behaves the same**, which this sample could
   not observe.

A post-clause number at or near 0 of 6 is not a repeat of this row; it is the
< 50% branch, and this baseline is the floor it has to beat.
