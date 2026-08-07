# ADR 0018 — Isolation stays scoped to the invocation repository; a second repository is disclosed, not provisioned

- Status: **Accepted — nothing is built.** Multi-repository managed worktrees
  are refused for now. What this record does produce is the *shape* the feature
  would have to take if the evidence ever arrives (§1's user-named `--repo`),
  the three questions that make it expensive (§2–§3), and a pre-registered
  measurement (§Falsification) whose failure converts this "no" into a "build
  §1". A decision not to build is only honest if it says what would change it.
- Date: 2026-08-07
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

#103 reported two symptoms of one root cause, from a real run
(`20260804-032843.062915000-1`) whose goal named a second local repository by
absolute path.

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
`validatePlannedNodeWorktree` both refuse the field, so every planned node runs
directly in the tree the user invoked from, editing and committing in it.
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
#103 open. Building (B) is the decision this record has to make, and (A) is
only worth making at the same time or not at all.

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
SECURITY.md and #129's warning put it. The four remaining sections say why, in
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
for, exactly as `--verify-cmd` names a shell command the engine runs (ADR 0016,
build evidence). The precedent is the point: *the engine does the dangerous
thing only when the user supplies it, and the plan is never granted anything.*
Under that form the plan-time reopening is bounded to a **selection, not a
string**: a planned node may carry `repo: <name from the user's own list>`,
validated against the flags the user typed, and anything else — a path, an
unknown name, an empty value — is refused by the same
`validatePlannedNodes` sweep that refuses `cwd:` today. The planner chooses
*which of the user's repositories* a node works in. It never chooses *that a
repository exists*.

That is the form. It is not being built, for the reasons in §2 and §3.

### 2. Partial failure would be a non-event, and only because provisioning stays lazy

If §1 is ever built: **no run-level provisioning phase.** `Acquire` is called
per node, on demand, the first time a node names a worktree, and that is what
keeps "repo A provisioned, repo B failed" from being a new state at all. Repo
B's failure fails repo B's nodes, exactly as a worktree failure fails one node
today; halt-on-fail and retry behave as they already do; the ledger needs no
new disposition; `state.json` records the same per-node verdicts and a resume
re-provisions from disk through `Acquire`'s existing disk-aware path.

The only addition worth making is **plan-time validation, not plan-time
provisioning**: each `--repo` is checked to resolve to a git checkout root
outside the invocation repository, by `stat` alone — `checkoutRootOf` already
does exactly this without spawning git — so a typo dies before anything spends
rather than at the first node that needs it.

An eager "provision every repository at run start" step is the tempting
alternative and it is wrong twice: it converts a per-node failure into a
run-level one, and it creates branches in the user's repositories for lanes a
halted run may never reach.

**This question is not what makes the feature expensive.** §3 is.

### 3. Cleanup across repositories is the reason the answer is "not now"

The directory is ours; the debris is not. `git` permits a worktree of
repository B to live anywhere on disk, so `<run-dir>/worktrees/<name>` stays
the managed path and the *filesystem* footprint stays inside the run directory.
Two things do not:

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
ship (a) cleanup notes that name the *repository* and not only the branch, and
(b) something that can enumerate and prune what a run left in a foreign
repository — which is a new command with its own record of what a run touched,
not a flag on an existing one.

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

**Turn #129's detection into a planner instruction, not into an actuation.**
When the plan's text names a checkout outside the invocation repository, the
warning today tells the *user* to go and reword their goal — *"If a node must
work in one, say in the goal that it has to create its own git worktree there
first and stay inside it."* That is a correct instruction addressed to the
wrong party: trusted code can say it directly to the planner, the way
`branchEvidenceRule` says the remote-state rule. It costs no field, no seam,
no reopening, and it is the same intervention that fixed #103's first half.

It is advisory — a node may ignore it — and it must be labelled that way
wherever it is described. **Whether it is obeyed is the measurement below**, and
it is the measurement that decides whether §1 gets built.

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
  invocation repository — i.e. nodes reached by a non-nil `UnisolatedScan`,
  which is already computed and printed.
- **Metric.** The fraction whose transcript records a `git worktree add` (or a
  clone) in that repository **before** any command that moves that repository's
  HEAD (`git checkout`, `git switch`, `git reset`, a branch-creating commit in
  the shared tree).
- **Threshold, fixed now.** Over **at least 10** such nodes: **≥ 80% →
  advisory works, this decision stands and hardens.** **< 50% → advisory has
  failed on its own terms, and §1's user-named `--repo` form is built**, with
  §2's lazy provisioning and §3's two cleanup obligations as blocking
  requirements rather than nice-to-haves. Between 50% and 80% → the sample is
  extended, not reinterpreted.
- **Confounder to record, not to explain away.** If the §6 planner instruction
  ships first, this measures the instruction and not the status quo, and the
  status quo's number will then never be known. Take a baseline of at least 5
  nodes before that instruction lands, or record that no baseline exists.

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
