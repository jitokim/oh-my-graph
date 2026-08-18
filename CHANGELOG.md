# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

oh-my-graph is **alpha software**. The graph YAML schema, the CLI, and the
`NodeRunner` interface may change without notice before `v1.0.0`.

## [Unreleased]

### Fixed

- **`resume` learns `--verify-cmd` / `--verify-timeout`, so a run that paused on
  a session limit can actually be resumed** (#198, ADR 0016 §4 amended). A run
  started with `auto "<goal>" --verify-cmd '…'` and paused on a session limit
  could not be continued at all, and the tool said otherwise:

  ```console
  $ oh-my-graph resume <id> --retry-failed
  … the saved graph carries success_check.verify on node(s) report, which auto
  mode never accepts from a run directory; RE-SUPPLY IT WITH --verify-cmd …

  $ oh-my-graph resume <id> --retry-failed --verify-cmd '…'
  flag provided but not defined: -verify-cmd
  ```

  The refusal itself is the design — a resumed leg takes no engine-run shell
  from a run directory, since a planned node holds bare `Write`/`Edit` and could
  write one there — but its remedy named a flag only `auto` registered. ADR 0016
  §4 recorded that gap as a debt ("the flag lane owes `resume` the same two
  flags"), not as an exclusion, so this ships the half that was owed.

  What that cost was worth naming: ADR 0009's claim is that a session limit is a
  **pause**, with the work banked for a later leg. For any auto run following ADR
  0016's own advice that promise was not kept, and the user learned it only after
  following an instruction that could not work.

  `resume` now takes the same pair `auto` does, and the ceiling is unchanged by
  construction rather than by intention: the same `VerifyCommand` value object
  (one blank-command refusal, one 10-minute ceiling, pinned by a test that parses
  the same flags through both subcommands and requires identical refusals), the
  same trusted-code attachment at the same sinks after the same validation, the
  same run-wide serialization, the same engine-judged exit code, and no node
  granted anything. There is deliberately **no path only `resume` has**: `run`
  has no `--verify-cmd`, so `resume --verify-cmd` against a hand-written
  snapshot is an error rather than an attachment. A resume that supplies nothing
  while the snapshot carried a verification is still refused — with a message
  that now names a flag the command accepts, checked by a test against `resume`'s
  own FlagSet. Pause hints for such a run print the flag back with the command in
  it, so the copy-pasteable resume ADR 0009 promises stays copy-pasteable.

### Added

- **The splice disclosure names a tool grant that was assembled across two
  files** (#196, ADR 0013 amended). `FragmentResolution`'s own doc comment
  states the principle — "a hollowed-out `success_check` or a widened
  `allowed_tools` is announced at every run, not only visible to whoever reads
  the file" — and names two shapes: a key the using node overrides, and (ADR
  0027) the ids a multi-node splice minted. A third escaped both. A fragment
  may declare a substitution point inside its own grant:

  ```yaml
  # fragments/tools.yaml
  substitutions: [extra]
  node:
    allowed_tools: [Read, "{{ with.extra }}"]
  ```

  ```yaml
  - { id: x, use: tools, with: { extra: "Bash(go *)" } }
  ```

  This loads clean today and the token really is substituted. It is not an
  override — the citing node declares only wiring, exactly as the design
  requires — so the override list was empty and nothing was announced. The
  fragment file showed a slot, the citing graph showed a value, and the run log
  showed neither: the one grant that needed two files to read was the one no run
  could show.

  The line now carries the resolved grant, in both fragment forms:

  ```text
  fragment: node "x" spliced from "tools" (graphs/fragments/tools.yaml) — a parameterized gate — allowed_tools resolved from with: Read, Bash(go *)
  fragment: node "x" spliced from "lanes" (graphs/fragments/lanes.yaml) — two lanes — nodes: x/build, x/review — allowed_tools resolved from with: x/build: Read, Bash(go *)
  ```

  The multi-node form qualifies each grant by its minted id, because "which of
  the five" is the question there; a splice that parameterized SEVERAL nodes'
  grants gives each its own indented line instead, because inline they would
  nest `,` inside `;` and the boundary between two nodes' grants would be the
  weaker separator of the two:

  ```text
  fragment: node "x" spliced from "lanes" (graphs/fragments/lanes.yaml) — two lanes — nodes: x/build, x/review
    allowed_tools resolved from with:
      x/build: Read, Bash(go *)
      x/review: Read, Write
  ```

  What the line names is what the node runs with, down to the empty cases,
  where YAML's null and its empty string part company: an element bound null is
  dropped by the decode and so is dropped from the line, an element bound to
  `""` is kept by both, and a whole grant bound null prints `(none)` — the node
  has no grant. Read as text those look identical, and announcing one as the
  other is the drift this clause exists to prevent.

  Two bounds keep it readable. It fires only
  when substitution CONTRIBUTED to the field — a grant written verbatim in the
  fragment is already readable in one file, and a line per grant per spliced
  node is one nobody reads (the failure the Codex disclosure work documented:
  one line per difference and no more). And the judgment is a before/after
  comparison of that one field, never a scan of the fragment source for `{{`: a
  token can arrive through a nested structure, and a whole-list binding
  (`allowed_tools: "{{ with.grant }}"`) replaces the field's type as well as its
  text, so a source scan would drift from what substitution actually did.

  **Disclosure only — nothing that loaded before now refuses.** The two other
  options the issue recorded (refuse a `with:` token inside `allowed_tools`;
  amend the comment to claim less) were both declined. A grant the citing node
  *overrides* keeps being announced as the override it is and adds no second
  clause: the substituted one was discarded by the overlay, so naming it would
  name a grant no node runs with.

- **`lint` / `run --dry-run` warn when a feedback loop's repair node never
  quotes the feedback** (ADR 0028). `feedback: { rerun: R }` and
  `{{ feedback.<declarer> }}` are two halves of one mechanism, and until now
  either half loaded clean alone: the arc's topology is validated, a token that
  IS written is validated, the arc's aim is swept for — nothing asked whether
  both halves are present. Run `20260816-163759.091162000-1` is what that costs.
  A two-node loop declared its arc correctly, its build prompt said "if a
  FEEDBACK section appears below, write alpha", and no token existed to put one
  there. The engine wrote the payload, re-ran the build, got identical output,
  and the check failed identically. Twice the money, one round's worth of
  information, ledger reading `feedback round 1/1`, and `lint` silent.

  `handoff.LintFeedbackQuoting` is the sixth advisory sweep in that package,
  wired into the one `warnAdvisories` helper `lint` and `run --dry-run` share.
  The rule: for every node `D` declaring `feedback: { rerun: R }`, if no node in
  the loop body other than `D` itself quotes `{{ feedback.D }}` in its **prompt**,
  warn — on `R`, naming `D`, because `R`'s prompt is where the missing line
  goes. A middle body node counts (`build → refine → check` quoted at `refine`
  really does repair); the declarer's own quote does not (it is the judge, so
  its re-run repairs nothing). Only prompts are read: a payload on a verify
  command line is the fifth sweep's finding, and one in a `cwd` is a path.
  Matching uses the runtime's own placeholder pattern, so the sweep holds after
  fragment splicing — the specimen's real token was
  `{{ feedback.qa-a/check }}`, a namespaced id the loader wrote — and both
  shapes are tested. Advisory for a hand-written graph, never a load error: an
  absent token has one legitimate reading (a loop that repairs from the
  repository rather than from the reply), where the misplaced token ADR 0010
  made an error has none.

- **Auto mode refuses a planned feedback arc nothing in its loop body quotes**
  (ADR 0028 §5). The planner is *asked* for both halves in one prompt sentence —
  declare the arc on the reviewing node, and have the implementing node's prompt
  read `{{ feedback.<reviewing-node-id> }}` — and until now only the arc half was
  machine-checked (`coordinator.validatePlannedNodes` constrains a planned
  `feedback:`; it never refused one). So the blind loop's worst instance, the one
  with no author to read a warning, was the case left uncovered.
  `coordinator.validatePlannedFeedbackQuoting` escalates the sweep to a plan
  refusal the same way `validatePlannedFeedbackReach` escalates
  `graph.LintFeedbackReach`, reading the same predicate rather than re-deciding
  it. A refused plan buys one corrected re-plan carrying the refusal's text, and
  the correction — one placeholder, empty on the first pass — is harmless even
  when the refusal is wrong, which is why this one needs no
  only-when-actionable weakening.

  **It was measured before it shipped, and it has a control.** Over the shipped
  `graphs/*.yaml` (8 graphs, 2 declarers), a 26-lane operator corpus (2
  declarers) and 288 local run snapshots deduplicated to 201 distinct resolved
  graphs (11 declarers): **3 hits, all 3 real, 0 noise** — with the caveat
  attached to the number rather than to a later paragraph: the 3 are the three
  *lanes* of one specimen graph in one run, so the precision evidence is one
  distinct defective graph, not three independent ones. The same corpus holds
  that graph's repair, three minutes later, and the sweep is correctly silent on
  it. Of the 11 run-corpus declarers, **3 were planner-authored** (auto mode's `graph.json`)
  and all 3 quoted the payload correctly — the escalation above guards a shape
  the planner *can* write, not one it has been measured getting wrong. No shipped
  graph fires; nothing in `graphs/` needed fixing, and a test now walks
  `graphs/*.yaml` so that stays true. Full method, every number asserted rather
  than reported:
  [docs/measurements/0028-feedback-quote-corpus.md](docs/measurements/0028-feedback-quote-corpus.md).

  No runtime behaviour changed: feedback's semantics, payload file, round
  accounting and exit codes are untouched. The auto-mode refusal changes what
  `auto` accepts as a plan, not how a graph runs — and note the reach of the
  advisory half: `lint` and `run --dry-run` print it, a plain `run` does not, so
  an operator who does not lint first still pays for a blind loop.

### Fixed

- **`run --dry-run` prints the fragment disclosure at all.** It shared the
  advisory channel with `lint` and `run` but never called
  `printFragmentResolutions`, so the one command a reader uses to check what a
  graph WILL do before paying for it was the one that did not say which
  fragments it spliced, from which files, or with which overrides. Same defect
  class as #185's half-wired warning, and the reason the grant clause above is
  tested at all three call sites rather than at the one that happened to be
  found.

- **A repair prompt no longer loses whole refusals to a silent cut.** Auto mode
  hands a refused plan's refusals back to one corrected planner call, quoted into
  a fenced section bounded by `maxIssuesInPrompt`. That bound was applied by
  head-only truncation of the joined list, which on an over-long list left the
  last refusal it kept ending mid-sentence and dropped every later one with no
  trace — so the planner answered a prompt that never stated part of the fault,
  the corrected reply re-committed it, and the plan the user paid for was gone.
  The list is now packed in WHOLE refusals with the dropped count stated in the
  prompt, and the budget is sized (3000, from 2000) rather than picked.

  The fault was reachable because two refusal families are graph-level and both
  scale with the number of faulty arcs: a mis-aimed arc and a blind one can be
  the same arc (ADR 0028 §Failure modes), and two such declarers rendered 2541
  bytes into a 2000-byte budget before a single per-node refusal joined them.
  `coordinator.validatePlannedFeedbackQuoting` now compacts every blind arc into
  one sentence naming each pair — four arcs cost 762 bytes instead of 2368 —
  keeping the shared ~530-byte diagnosis out of the repeat, which brings the
  same two declarers to 1998. Every one of those figures is measured on one
  fixture and pinned by
  `TestGraphLevelRefusalFamiliesRenderTheirMeasuredSize`, so a reworded refusal
  fails a test instead of leaving a comment quietly false.

- **`{{ feedback.D | inline }}` no longer counts as quoting a feedback payload.**
  `handoff.LintFeedbackQuoting` read the placeholder pattern's kind and reference
  and ignored its filter group, where the runtime refuses a filtered feedback
  token outright (`graph.Validate` at load, `Handoff.Interpolate` at run). No
  graph that can be loaded today reaches it — both callers sweep a parsed
  graph — so nothing observable changes; the guard and its test exist so the one
  case where the sweep and the runtime could disagree cannot open up quietly.

- **A release's page can no longer come out blank**
  ([#193](https://github.com/jitokim/oh-my-graph/pull/193)). v0.9.0 published
  with an empty body — one newline — while every step reported success and the
  artifacts uploaded fine. The notes file was built correctly (the same script
  produces 143 lines under the runner's own ubuntu/dash/mawk, reproduced in a
  container), so goreleaser's `--release-notes` was not doing what it says
  alongside `changelog.disable`. The body is no longer goreleaser's job:
  `gh release edit` sets it from the same file, and the step then **reads it
  back and fails under 200 bytes**, because the failure that already happened
  was a green workflow over an unreadable release.

### Changed

- **The "did you write a changelog entry?" check moved from a Go test on `main`
  to a CI job on the pull request**
  ([#194](https://github.com/jitokim/oh-my-graph/pull/194)). The test asked the
  right question with the wrong trigger: it read `git log <lastTag>..HEAD`, so a
  PR's own merge commit did not exist while its CI was green and did exist the
  instant it landed — **every merge turned `main` red** until somebody wrote the
  entry afterwards. It caught five genuinely missing entries in v0.9.0, two of
  them user-visible fixes, and then cost five round trips to its own timing.
  Asked on the PR instead, it is answerable before the merge by the person who
  knows what changed. Skipping stays allowed and stays loud: `no-changelog` in
  the PR body.

## [v0.9.0] - 2026-08-17

**Minor because the graph schema grew keys you may type.** Compared by name
rather than by diff line: the seventeen long flags in `flags.go` are unchanged
and so are the eleven subcommands — the CLI surface is byte-identical to
v0.8.0. What grew is the FRAGMENT file schema, which gained `exit:` and made
`nodes:` usable where it was previously refused (ADR 0027). No node or graph
key was added, renamed or removed.

The headline is that **the reusable unit is a loop, not a node**: a fragment may
now carry several nodes and the edges among them, so a QA loop or a
review/repair round is citable the way a single node has been since ADR 0013.

Eight PRs: [#176](https://github.com/jitokim/oh-my-graph/pull/176),
[#177](https://github.com/jitokim/oh-my-graph/pull/177),
[#181](https://github.com/jitokim/oh-my-graph/pull/181),
[#182](https://github.com/jitokim/oh-my-graph/pull/182),
[#183](https://github.com/jitokim/oh-my-graph/pull/183),
[#184](https://github.com/jitokim/oh-my-graph/pull/184),
[#185](https://github.com/jitokim/oh-my-graph/pull/185),
[#186](https://github.com/jitokim/oh-my-graph/pull/186).

### Added

- **A fragment may declare a LOOP, not only a node**
  ([ADR 0027](docs/adr/0027-the-reusable-unit-is-a-loop-not-a-node.md),
  [#186](https://github.com/jitokim/oh-my-graph/pull/186)). A
  fragment file may now declare `nodes:` (several, with the edges among them)
  plus a required `exit:`, and be cited with the same `use:`/`with:` a
  single-node fragment is. Spliced ids are `<using-id>/<internal-id>`, which no
  author and no planner may write, so a spliced node can never collide with an
  authored one; entry nodes inherit the citing node's `depends_on`, `cwd:` and
  `worktree:` propagate from it, and `depends_on: [<loop>]` /
  `{{ artifacts.<loop> }}` from downstream both resolve to the loop's exit.
  `exit:` is never inferred from the unique sink — inference is right only
  while there is exactly one, and when it is wrong it is wrong silently.
  ADR 0013's rule is generalized, not weakened: **a fragment may never name an
  id it does not itself declare**, of which "a single-node fragment may declare
  no wiring at all" is now the special case, with every one of its tests kept.
  Measured on the shipped corpus: `adr-driven-dev`'s two hand-unrolled
  review/apply rounds became two `use:` of one fragment, **119 lines removed
  for 53**, and the one-direction discipline, both verdict contracts, the
  apply's tool grant, its evidence gate and its retry stopped being written out
  four times. Scheduler, snapshot, event feed and ledger are untouched — a
  spliced node is an ordinary node, and a consumer that wants the loop view
  groups by the `<using-id>/` prefix.

### Changed

- **A node's `budget_usd` no longer refuses a Codex graph**
  ([ADR 0026](docs/adr/0026-an-inapplicable-cap-is-not-an-unsafe-one.md),
  [#185](https://github.com/jitokim/oh-my-graph/pull/185)).
  Preflight had one sentence for two different facts: `agent:` names a subagent
  whose system prompt the node would otherwise lose (a different node — still
  refused), while `budget_usd` is a USD ceiling a runtime that reports no USD
  has nothing to bound. Inapplicable is not unsafe, so the graph now loads and
  warns per node, naming the guard still in force — that node's `timeout:`, or
  the runner's 20m default. Measured on `graphs/*.yaml`: **five refused under
  `--runtime codex` before, one after** (`adr-driven-dev`, for its `agent:`).
  `auto --max-goal-budget-usd` stays refused and that is not an inconsistency:
  it is checked only at a cycle boundary, so an unmeasurable ceiling would buy a
  whole cycle before stopping to say it cannot be checked, where an inapplicable
  node cap costs nothing extra. The loop stays bounded either way —
  `--max-cycles` is what bounds iterations.
  `internal/runner/shipped_graphs_runtime_test.go` now lints every shipped
  graph under both runtimes and asserts the verdict by name, so a graph that
  becomes unloadable under Codex fails `make test` instead of a user's run.
  **The Claude path is unchanged**: `ValidateGraphForRuntime` still returns on
  its first line for `RuntimeClaude`, warning nothing and refusing nothing.

### Fixed

- **The changelog guard no longer turns `main` red on its own maintenance**
  ([#189](https://github.com/jitokim/oh-my-graph/pull/189),
  [#191](https://github.com/jitokim/oh-my-graph/pull/191)). The guard added in
  #188 counted two kinds of commit against themselves. A **release cut** does not
  describe itself, so its own number is always absent from the section it just
  wrote — green on the release PR, red on `main` the instant it landed. And a
  **changelog-only** commit has no change to describe, so demanding an entry made
  the check eat its own tail: the PR adding a missing entry is itself missing
  one, and so is the PR adding that. Both are exempt now, recognised by what the
  commit touched rather than by how its subject is worded — a cut changes
  `CHANGELOG.md` and `version.go` together, which nothing else does; changelog-only
  means exactly one file. Each exemption is mutation-checked: disabling either
  turns the test red.

  Recorded because the trade is not free: the guard caught **five** genuinely
  missing entries in this release, two of them user-visible fixes that would
  have shipped a release page never mentioning them — and its edges then cost
  four round trips, every one of them on a commit whose subject was the
  changelog itself.

- **A Codex run's live view says there is no tail, instead of showing nothing**
  ([#182](https://github.com/jitokim/oh-my-graph/pull/182)). The view polled
  `/api/transcript` every three seconds per running node, and the endpoint looks
  for `<session-id>.jsonl` under `~/.claude/projects` and nowhere else — so on a
  Codex run every poll answered 204, for the whole run. The pointless polling
  was the smaller half: **an empty tail is indistinguishable from "the node
  hasn't printed anything yet"**, so the view looked broken with no way to learn
  it was working as designed. `/api/graph` now carries a note when the run's
  runtime keeps no per-node transcript, and the page renders it in place of the
  tail and stops asking. The endpoint gains no runtime branch.

- **`Snapshot.Runtime` is a property of the format, not of one writer**
  ([#181](https://github.com/jitokim/oh-my-graph/pull/181)). The field was
  `omitempty` and `docs/RUN-FEED.md` tells consumers an absent value means
  claude — safe only because `executeGraph` happened to canonicalize before
  writing. A future caller of `runstate.Write` could leave a **schema-3 snapshot
  with no runtime**, which every consumer then reads as claude when it was not,
  reopening the hole the schema-3 bump was taken to close. `Snapshot.MarshalJSON`
  now canonicalizes, so the key is always present whichever writer produced it.
  Reading is unchanged: an absent `runtime` in an existing file still means
  claude.

### Documented

- **ADR 0009's session-limit pause is the Claude runtime's promise, not the
  engine's** ([#184](https://github.com/jitokim/oh-my-graph/pull/184), closing
  [#171](https://github.com/jitokim/oh-my-graph/issues/171)). Detection matches
  Claude's own prose, so there is nothing for another runtime's message to
  match, and the `RuntimeClaude` gate in `CLIRunner` is the second layer rather
  than the cause — **deleting it would not add the pause under Codex, it would
  add a pause that can never fire.** So a new runtime does not owe a
  session-limit signal; what it owes is the honest degradation ADR 0009 already
  specifies. No behaviour changed.

- The **user-facing** documents now know about the second runtime
  ([#177](https://github.com/jitokim/oh-my-graph/pull/177)) — `docs/EXAMPLES.md`
  gains a section on what `--runtime codex` changes, `docs/RUN-FEED.md` tells
  consumers the live-output supplement is Claude-only and that a Codex
  `cost_usd` is `0` beside `cost_unknown: true` in the snapshot (present but not
  authoritative), and `plugin/README.md` says which of the three plugin entry
  points can reach the flag at all.

### Repository

- The **release body is now `CHANGELOG.md`'s own section** plus a Contributors
  line computed from `git log`, never goreleaser's commit-subject list
  ([#176](https://github.com/jitokim/oh-my-graph/pull/176)). A missing section
  fails the release.
- **`main` enforces a gate that can actually be met**
  ([#183](https://github.com/jitokim/oh-my-graph/pull/183)): `test` **and**
  `stress` required, administrators included, and no required-approval count —
  which was unsatisfiable for a solo maintainer and so was being bypassed with
  `--admin`, which bypasses the tests too. Strictly tighter than before.

## [v0.8.0] - 2026-08-15

**Minor because the CLI grew a token you may type.** Compared by name rather
than by diff line: the long flags registered in `flags.go` are the same
seventeen as at v0.7.0, the eleven subcommands are the same eleven, and the
global flags gained exactly one — `--runtime`. Nothing was renamed or removed.

The headline is a **second node runtime**, contributed by
[@minkichoe-lbox](https://github.com/minkichoe-lbox) in
[#170](https://github.com/jitokim/oh-my-graph/pull/170) — the project's first
outside contribution, and a 117-file one that kept every load-bearing invariant
intact, including the one most at risk: `childenv.Scrub` still takes no runtime
and has no branch.

Four PRs: [#170](https://github.com/jitokim/oh-my-graph/pull/170),
[#172](https://github.com/jitokim/oh-my-graph/pull/172),
[#173](https://github.com/jitokim/oh-my-graph/pull/173),
[#174](https://github.com/jitokim/oh-my-graph/pull/174).

### Fixed

- **A goal budget can no longer be measured against a number the loop does not
  have.** `RunGoal` compared `--max-goal-budget-usd` against known spend alone
  and accumulated only the known halves, so unknown spend counted as $0 and a
  capped loop could iterate under a ceiling it could no longer measure. ADR 0025
  states that guarantee as a property of the system, and the CLI refusing the
  runtime/budget combination up front is not the same thing — **a cost can also
  go unknown at runtime**, from a node killed before it reported or a garbled
  envelope. The check is ordered so the honest half still fires first: known
  spend is a *floor* on true spend, so "the known part already reaches the
  ceiling" stays sound however much went unreported and still stops with
  `StopBudgetExceeded`; only when the known part is under the ceiling does the
  unknown decide, and there the loop stops with the new
  `StopBudgetUnmeasurable` rather than continue.

  It is a stop reason and not an error, for the reason `StopDeclined` is: by
  then every cycle completed and was assessed. As an error it cost the
  `remaining:` line every clean stop prints, and — because the check runs before
  the next cycle's planning hook — made the goal summary bill a cycle that never
  existed. **Known blast radius, stated rather than discovered:** one ordinary
  20-minute node timeout also sets `CostUnknown`, so a single timed-out node can
  end a budgeted loop that ADR 0011 §2 would otherwise keep iterating.

- **A node's stderr is bounded again.** Collecting it by hand replaced
  `cmd.Output()`'s stdlib cap with an unbounded buffer — **paid by the Claude
  path too** — while the only consumer reads 500 bytes. Restored as a 32 KiB
  tail: the tail, because every consumer reads through `tailOf` and a CLI's
  fatal line is its last one; 32 KiB, because that is the ceiling
  `prefixSuffixSaver` kept rather than a tighter number invented here.

- **`make local` failed on every mac** after the runtime landed, while CI stayed
  green. macOS charges a security scan on the first exec of a newly written
  file, per file, so a stub written into a fresh `t.TempDir()` paid it every
  run: 384-1607 ms, against 6-15 ms to re-run the same file. Two tests raced
  that against a wall clock and lost before the code under test was involved.
  Fixed by warming the stub outside the timed window rather than by widening a
  deadline — with a cost spanning 4x, no constant is both tight enough to test
  the property and loose enough to stay green, so a bigger number would have
  turned a red suite into a flaky one.

### Changed

- **The README is a front page again** — 904 lines to 223
  ([#169](https://github.com/jitokim/oh-my-graph/pull/169)).

### Added

- **Codex is now a run-wide model CLI runtime.** Use
  `oh-my-graph --runtime codex <run|auto|chat|lint|resume|serve> ...`; Claude
  remains the default. One `CLIRunner` owns both protocols, runtime identity is
  persisted for resume and browser gate actions, Codex sandbox modes map from
  graph permission modes, and planned Codex nodes exclude user config,
  project rules/AGENTS files and MCP servers. Codex thread ids support session
  handoff. USD cost is explicitly unknown while provider token usage is
  preserved through the ledger, snapshot/feed contracts and web UI. Codex
  rejects Claude-only `budget_usd`, `agent:`, goal USD budgets, agent mapping
  and skill activation before execution. `state.json` and `events.jsonl` move
  to schema 3 so older readers cannot misread unknown cost or runtime identity.

- **The pre-run Codex disclosure now names the four differences a user
  otherwise met only after spending.** Alongside the filesystem sandbox and the
  documentary status of `allowed_tools`: a sandboxed node has **no network**
  (`gh`, `git push` and `git ls-remote` fail), so a graph halts at the FIRST
  node that publishes — and the disclosure names **where that node sits**,
  because it is not always the last one. Last in `adr-driven-dev` and every user
  of `graphs/fragments/pr-publish.yaml`; **first** in `apply-flags`, which
  pushes from `dev` and ends on a read-only `verify`; **every node** in
  `merge-shepherd`, which is `gh` end to end and so fails at node 1 having done
  nothing. Two per-node ways out are named with what each costs
  (`bypassPermissions` → `danger-full-access`, which is no sandbox at all; and
  Codex's `sandbox_workspace_write.network_access=true`, which does fix
  `git push` but not `gh` where a keyring holds its token);
  **USD cost is unknown for every node**, not merely unbudgetable;
  `approval_policy="never"` is passed unconditionally; and **ADR 0009's
  session-limit pause does not exist on Codex**
  ([#171](https://github.com/jitokim/oh-my-graph/issues/171)). The long form is
  in [docs/LIMITATIONS.md](docs/LIMITATIONS.md).

- **`lint` / `run --dry-run` warn when a `success_check.verify.command`
  splices a model's own text into the shell command line the engine runs.** A
  verify command interpolates exactly like a prompt — `resolveVerification`
  calls the same `Handoff.Interpolate` — and the result is handed to
  `verify.ShellVerifier`, the second exec seam, which runs it under your own
  shell. Two token shapes carry model-written text there:
  `{{ artifacts.<id> | inline }}`, which is the node's own reply, and
  `{{ feedback.<id> }}`, which always inlines the declarer's payload and takes
  no filter. Neither is malformed, so the existing placeholder sweep passed
  both in silence. `handoff.LintVerifyInlining` is the fifth advisory sweep in
  that package, wired into the one `warnAdvisories` helper `lint` and
  `run --dry-run` share.

  The message names the fix rather than the filter. For an artifact the fix is
  usually the DEFAULT: with no filter the token is the persisted `.out` file
  path, which the engine computes, so `grep -q '^PASS' "{{ artifacts.impl }}"`
  gets the same content without quoting a reply into argv. A feedback token has
  no filterless form, so it gets a different sentence: the payload belongs in
  the node's prompt. `{{ inputs.<name> }}` is deliberately never warned about —
  an input is bound from your own `--input` and has the standing the command
  line itself has, which is the shipped `backlog-batch.yaml` shape
  (`{{ inputs.checks_command }}`). Only `command` is swept: a verify `cwd`
  becomes `exec.Cmd.Dir`, which is not shell-interpreted, so no part of a reply
  there is parsed as syntax or becomes a command of its own.

  A token the placeholder sweep already condemns as unresolvable — a node's own
  artifact, or a node the graph does not have — is skipped here, so one token
  does not draw two lines where the second one's sentence would be false and
  its fix untakeable. A reference to a node that EXISTS but is not an ancestor
  still draws both: it may resolve, and if it does, the reply is on the command
  line.

  **It was measured before it shipped and it fires on nothing.** Over this
  repo's shipped graphs plus a 20-graph operator lane corpus — 93 nodes and 34
  `verify` blocks after fragment resolution — zero verify commands inline a
  model's text, and the only tokens in any verify command at all are the two
  `{{ inputs.checks_command }}` above, which the predicate correctly leaves
  alone. So this is documentation with a test attached, and it ships as that
  on purpose: it is the one sweep in the package whose subject is not a run
  that comes out wrong but a run that executes text nobody wrote, it is
  invisible by construction, and the test is what keeps the documentation true
  — if the default filter ever changes,
  `TestLintVerifyInlining_TheDefaultFilterIsAPath` fails.

  Advisory, never a load error, for the standing reason: only a person can
  write what it condemns. `validatePlannedNodeVerify` refuses a
  planner-authored `verify:`, so the only verification a planned graph carries
  is the `--verify-cmd` you supplied — advisory-eligible like any other command
  line, but your own string — and a hand-written `verify:` block is your own
  reviewed artifact.

## [v0.7.0] - 2026-08-13

**Minor because the schema grew a value you may type.** Compared by token name
rather than by diff line: `graph.RetryCauses()` registered six causes at v0.6.1
— `nonzero_exit`, `run_error`, `output_error`, `budget_exceeded`,
`verify_failed`, `result_mismatch` — and registers seven at this tag, the new
one being `timeout`. No existing token was renamed or removed. `flags.go` is
untouched in the range and the eleven subcommands are the same eleven, so
nothing else new is typed. One merged PR,
[#166](https://github.com/jitokim/oh-my-graph/pull/166).

Implements [ADR 0023](docs/adr/0023-a-run-has-one-status-and-planning-is-one-of-its-values.md),
closing [#163](https://github.com/jitokim/oh-my-graph/issues/163), and
[ADR 0024](docs/adr/0024-a-timeout-is-its-own-cause-not-a-run-error.md), found by
running this repository's own `adr-driven-dev` template against it.

**These are one story, not three items.** #163 reported that `auto`'s planner
call is invisible; fixing it meant deciding what a run's status *is*, which
turned up one value that was already being rendered wrong and one the
enumeration only had to keep; and running that work through this repository's
own ADR template is what exposed the template's `localrun` node asking for
stress it could not finish. One report, one thread.

### Added

- **New `retry.on` cause: `timeout`. This is user-facing schema surface** — a
  seventh value you may now write in a node's `retry.on`, accepted at load,
  named in the load error that rejects an unknown one, and advertised to the
  planner alongside the other six. A node killed by its own bound (the
  `timeout:` it declared, or the runner's 20-minute default) used to be
  classified `run_error`, the same token as a `claude` binary that never
  started — so `retry: { max: 1, on: [run_error] }` could not ask for one
  without the other, and the two want opposite policies. That narrowing of
  `run_error` is a behaviour change; it is under **Changed** below, with what
  to write if a graph relied on the old reading.
  **Auto-retrying timeouts was considered and refused** — a timeout is the one
  failure that always burns its full budget before dying, so a retry costs
  another whole timeout, and the engine cannot tell a slow machine from an
  instruction that cannot finish at any timeout. The author can, so the author
  decides ([ADR 0024 §3.1](docs/adr/0024-a-timeout-is-its-own-cause-not-a-run-error.md)).
  Only a deadline the runner minted for *that node* earns the token; a deadline
  inherited from the run's own context is still `run_error`.

- **`auto` is visible while it plans.** The run id is minted BEFORE the planner
  call, not after it, and the planning phase is a real leg: it takes the run
  lock and opens `events.jsonl` with a `run_started {phase: "planning"}`. For
  the whole of the longest single wait in the tool — and the first one a new
  user meets — `runs list` printed `No runs found.` and the `serve` dashboard
  showed no card, because there was no run directory for any reader to find.
  There is one now, and it reads `PLANNING`. The same applies per cycle of
  `--max-cycles N`, through a new `RunGoal` planning hook, because a status
  that depends on a flag is worse than no status.
- **A planner call whose process dies is `ABANDONED`**, with the correct
  recovery advice and the orphaned-subprocess warning — using ADR 0015's
  existing liveness machinery, with no second mechanism, no timer and no
  cleanup path.
- **`show` prints the run's status**, which it never had. Re-opening a paused
  run after the fact used to say nothing at all about it being paused, and a
  run directory that exists but has no snapshot is no longer reported as an
  unknown run.
- **`watch` and the run page name the phase a leg opened in.** An auto run's
  stream carries two `run_started`s, and both used to render as `▶ run started`
  with nothing to say why — so the `PLANNING`→`RUNNING` transition was invisible
  on the live human views of the stream. The run page's header chip reads
  `planning` for that leg too, instead of calling it `running` while the
  dashboard card that links to it says `planning` about the same bytes.
- **`watch` opens with the run's status, on a new first line.** Every
  `oh-my-graph watch <id>` now prints `run <id> is <STATUS>` to **stdout**
  before the first event — for *every* run it agrees to tail, a `RUNNING` or
  `PASS` run made by v0.6.1 included, not only a planning one. It is what tells
  a watcher whether the silence they are about to sit in is a planner call, a
  running node, or a stream that is already over; those three are
  indistinguishable from a tail alone. **`oh-my-graph watch <id> | head -1`
  therefore returns a status word where it used to return the first event.** A
  run directory whose stream has said nothing prints no such line — there is no
  status to announce — and an abandoned run's refusal on stderr is unchanged.

### Changed

- **`retry: { on: [run_error] }` no longer covers a node killed by its own
  timeout.** That is the other half of the new `timeout` cause above, and it is
  the one line an upgrading author needs: a graph listing `run_error` now
  retries strictly *less* than it did, in exactly the case where a retry costs
  the most — a full second bound. **To keep the old behaviour, write
  `on: [run_error, timeout]`.** No graph shipped in this repository lists
  `run_error`, but `run_error` is one of the causes advertised to the planner,
  so an auto-planned graph can and does write it: a saved plan spec you re-run
  is worth checking. A deadline inherited from the run's own context is still
  `run_error` — only the node's own bound earns the new token.
- **`graphs/adr-driven-dev.yaml`'s `localrun` stops asking for something it
  cannot finish.** It handed every repository the same literal
  `go test <affected packages> -race -count=300` under a 20-minute node — and on
  *this* repository 300 repetitions of `cmd/oh-my-graph` under `-race` take
  ~72 minutes, so the node was killed by its own timeout with the
  implementation already committed. A repetition count belongs to a repository,
  not to a template, so the node now derives one from a stated budget (its
  `timeout:`, declared explicitly rather than inherited, because the prompt
  quotes it) and **reports what it actually exercised** — package, count, wall
  time — so the verdict can be told from one that stressed nothing. It also
  now says to pass `-timeout`: `go test` kills a package after 10 minutes by
  default and prints `FAIL`, so a stress run that *did* fit the node's budget
  still reported a failure that never happened (measured: `-count=40
  ./cmd/oh-my-graph` "failed" at 601s), and `localrun`'s FAIL halts the whole
  pipeline. Its grant gains `Bash(go test *)` — narrow on purpose, never
  `Bash(go *)`, which would include `go run` — because the command the prompt
  asks for was never one the node was allowed to run. No other shipped graph
  changes.
- **A paused run stops reading as a failure, on every surface at once.** A
  shepherd stopped at its approval gate — exit 2, resumable, working exactly as
  designed — was listed as `FAIL` by `runs list`, which said so in its own
  words: *"a failed, paused, or interrupted run all render as FAIL"*. The
  dashboard had the same hole by a different route: it asked the snapshot's
  `gate.paused_at`, so ADR 0009's session-limit pause — which has no gate to
  point at — fell through and painted the card RED. Both now read `PAUSED`,
  derived from the leg's own `run_finished` outcome, which is the only
  formulation that covers both pause shapes.
- **One status, derived once.** `runstatus.Status` becomes the six-valued
  enumeration `PLANNING → RUNNING → { PASS | FAIL | PAUSED | ABANDONED }`, and
  the verdict half moves INSIDE it. `runs list`, the dashboard card, `watch`,
  `show`, `serve`'s `ResolveRun` and the single-run view had each been composing
  liveness with a verdict their own way — which is exactly what
  `internal/runstatus` was created to stop.
  **Two of the six are not new values, and it is worth being plain about
  which:** `ABANDONED` is not new — it shipped in v0.5.x as one of the
  three-valued liveness enum's values, and ADR 0015 already refused to call it
  `FAIL` on the grounds that a `FAIL` is a verdict about the work and an
  abandoned run never got one; the enumeration keeps that refusal rather than
  inventing it. `PAUSED` is the other, and it is a **defect users have been
  seeing**: a run stopped at a gate — resumable, exit 2, working as designed —
  was already being rendered `FAIL` by `runs list` and painted red by the
  dashboard, as the bullet above describes. Only `PLANNING` is genuinely a new
  value; `RUNNING`, `PASS` and `FAIL` are the words those surfaces already
  printed.
- **`runs list`'s column header is `STATUS`, not `VERDICT`.** `PLANNING`,
  `PAUSED` and `ABANDONED` are not verdicts, and leaving them under that header
  would keep the conflation in the one place a user reads it. A `PAUSED` row
  carries its resume command under the table, beside the `ABANDONED` hint.
- **A run directory that has said nothing yet has no status on any surface.**
  The lock creates the directory an instant before the first event reaches the
  file, and a directory whose stream could never be created stays that way for
  good. The dashboard has always kept `pending` there; `runs list` prints `-`
  and `show` omits the word, instead of the confident `FAIL` the derivation's
  default arm would otherwise have given them.
- **Three lines `auto`, the goal loop and `chat` print changed shape**, all
  because the run id now exists before the planner call rather than after it:
  - `Planning a graph for goal "…"...` gains the run id — `Planning a graph for
    goal "…" (run 20260813-…)...` — so the directory that wait is happening in
    can be opened in another terminal *while* it waits, which is the whole
    point of it existing. `auto --plan-only` mints no run id at any point and
    keeps the old line verbatim.
  - The goal loop's cycle banner moves AHEAD of that cycle's planner call and
    says what it is doing: `— goal cycle 1/3 (run 20260813-…), planning… —`. It
    used to be printed once the plan came back, i.e. after the wait it is the
    only announcement of.
  - `chat`'s accepted plan splits one header in two: the topology
    (`Planned graph "…" (4 nodes, planning cost $0.0412):`) is printed before
    the `[y/N]`, and the destination follows the answer on its own line
    (`plan accepted, saved to …`). Where the spec lands is not known until the
    answer is given — that is the same move as the bullet below.
- **`/api/cards`'s `state` value set changed.** `paused` and `planning` are new,
  and `gate-paused` no longer appears at the RUN level (it stays a node's
  state, on `nodes[].state`, unchanged). The dashboard's JSON is not a versioned
  contract the way `events.jsonl` is, but it is machine-readable and DESIGN.md
  documents it, so it is worth saying plainly: a reader matching the old value
  set drops every planning and paused run into whatever its default branch does.
- **A declined `chat` plan no longer manufactures a corrupt run.** The spec save
  moved after the `[y/N]`: answering `n` used to leave a `runs/<id>/` holding a
  `graph.json` and no `state.json`, which `runs list` reported as
  `WARNING: skipping run …` and the dashboard painted `unknown`. Its paid-for
  spec now goes to `plans/`, where a spec that never belonged to a run belongs.
- **A refused `auto` plan's `rejected.json` moves into its run directory**
  (`plans/<id>/rejected.json` → `runs/<id>/rejected.json`), for `auto` and for
  every goal cycle, and the cycle's lineage is now the run id itself rather than
  a `<first-run>-cycleK` name. `--plan-only` is unchanged: it mints no run id at
  any point, so its rejection stays under `plans/`.
- **`--plan-only` stays out of `runs/`**, and the reason changed. The old one
  was that a directory with no `state.json` reads as damage; such directories
  are now ordinary. The reason that survives is the enumeration: a preview has
  none of the six statuses and cannot be given one.

### Consumer contract (docs/RUN-FEED.md)

**No schema bump. `events.jsonl` stays 2 and `state.json` stays 2.** An
unmodified consumer improves without acting — through the planner call it sees
a leg that is open, where it previously saw nothing at all. Three things it may
want to know, all documented:

- `run_started` gains one optional additive field, `phase`, with one defined
  value, `"planning"`. **Count legs by `run_started` events with NO `phase`** —
  an auto run's stream now carries two.
- **A committed auto run has one close for two opens.** The planning open is
  closed by the untagged open, not by a `run_finished` of its own.
- **`run_finished {outcome: "failed"}` with ZERO node events** is a new shape: a
  refused plan. A consumer that assumes a failed leg names a failed node has to
  stop assuming it.
- **A run directory may hold neither `graph.json` nor `state.json`** — through
  a planning phase, and permanently for a refused plan. A v0.6.x binary reading
  one reports it through its `WARNING`+skip / `unknown` channels: the *stream*
  downgrades cleanly, the *directory shape* does not. The affected directories
  are ones an old binary calls damaged rather than misreports as healthy.
- **`run_started`'s timestamp moves earlier** for auto runs, so any elapsed
  clock derived from it — the dashboard's included — now covers the planner call
  too.

### Known limits

- `runs/` accumulates a directory per `auto` invocation, refused plans included,
  and there is still no `runs prune`. Those calls were paid for, so this is
  information rather than litter — but it is a visible change in what
  `runs list` accumulates.
- **Ctrl-C during a planner call now leaves a `FAIL` row.** Interrupting `auto`
  while it thinks used to leave nothing behind, because nothing existed. The
  planning leg is bracketed by a deferred close, and an interrupt runs it, so
  the run settles `FAIL` and exits 1. That is the deliberate choice over the
  alternative — leaving the leg open, which would read `ABANDONED` and print the
  orphaned-subprocess warning about a `claude` the interrupt just took down with
  it — but it is a row where there used to be none.
- The goal loop's ASSESSMENT call is still invisible: it happens after cycle
  *k*'s leg closed and before cycle *k+1*'s id exists, so it belongs to no run.
  Same shape as #163; fixing it needs a phase that belongs to the *goal*.
- The embedded live view still starts at execution — during planning there is
  no graph to render. A separately running `oh-my-graph serve` shows the
  `PLANNING` card immediately, which is the surface #163 names.
- A run that never wrote a snapshot shows `-` for its cost, a refused plan's
  real planner spend included. That figure is printed when it is incurred.

## [v0.6.1] - 2026-08-12

**Patch because nothing new is typed** — `git diff v0.6.0..HEAD --
cmd/oh-my-graph/flags.go` registers no flag; the only two lines it touches are
the help strings of `--no-agent-mapping` and `--no-agent`, rewritten because
what they decline changed. One merged PR, [#164](https://github.com/jitokim/oh-my-graph/pull/164),
closing the three findings issue #161 carried.

**Issue #161 is fixed, and the v0.6.0 disclosure below is now WRONG in the
opposite direction — read this section before you read that one.** v0.6.0 told
you that an agent-mapped planned node loads your settings, that its declared
scope is enforced only as far as your own settings enforce it, and that your
repository's `.claude/` configures it. All three were true and measured. None of
them is true of this build.

### Changed

- **An agent-mapped node keeps the whole tool ceiling.** `applyAgentMapping` no
  longer drops `--setting-sources`. The matched agent's definition is copied
  into `<run-dir>/agents-plugin/agents/<name>.md` — pinned by its plan-time
  SHA-256, re-verified before every spawn — and supplied with `--plugin-dir`,
  which reaches the node without reopening ceiling layer 1. Measured on one
  machine and CLI build (2.1.228) minutes apart: the argv v0.6.0 shipped ran an
  out-of-scope command with `permission_denials: []` **2 of 2**, the new argv was
  **denied 3 of 3** with the refusal recorded, an in-scope `git init` control
  still ran **2 of 2**, and `--agent` resolved to the **staged** definition
  **3 of 3** with removing the staging as the control (exit 1). 28 spawns,
  $2.4616, pre-registered in its own commit before the first spawn —
  `docs/measurements/0017-staged-agent-restores-layer-1.md`,
  [ADR 0022](docs/adr/0022-a-mapped-node-gets-its-agent-staged-not-its-settings-back.md).
- **The repository under work no longer supplies a mapped node's settings, its
  skills or its agent.** The wider
  half of the same fix: a `SKILL.md` committed to the repository fired 3 of 3
  under v0.6.0's argv and **0 of 3** under this one, and where the model did call
  `Skill` the CLI answered `Unknown skill: …` with `is_error: true`. A plugin
  enabled by that repository's own `.claude/settings.json` no longer loads
  either. `CLAUDE.md` and hooks arrive by the same source list and remain
  **implied, not measured**, in both directions.
- **…and the last way it still could is closed: only `~/.claude/agents` is
  scanned.** The sentence above was **false for one channel** when it was first
  written, and the channel was this fix's own: `DefaultAgentDirs` also scanned
  `<cwd>/.claude/agents`, the project shadowed the user on a name collision, and
  `applyAgentMapping` copies *whatever the scan resolved* into the node's
  `--agent` — a path `--setting-sources ""` structurally cannot shut, because
  oh-my-graph opens it. **Measured, 12 spawns, $0.9332**: with a definition
  committed to the fixture repository, the system prompt that ran an unattended
  `dontAsk` node was **the repository's, 2 of 2**; with the scan cut to the
  user's own directory and that copy still committed in the node's cwd, the
  definition that resolved was **the user's, 3 of 3**
  ([the record](docs/measurements/0022-repo-planted-agent-and-the-agents-only-dir.md)).
  It never breached the ceiling — layer 1 was `""` in both arms and the tools
  stayed bound, so the class is injection, not escalation. **It costs you a
  feature**: an agent kept in a project checkout no longer maps at all. Move it
  to `~/.claude/agents`.
- **The plan printout names the file every staged definition came from**, with
  its size and SHA-256, and says the repository's `./.claude/agents` is out of
  scope. "Auto-mapped onto **your own** agents" is a claim about a path on disk,
  and the measurement above is what it looks like when nothing on the screen
  carries the path.
- **What this costs you, plainly:** a mapped node no longer gets your standing
  permission grants — measured — nor your `CLAUDE.md` or your hooks, which
  arrive by the same source list and stay **implied, not measured**. It was the
  one planned node that did. MCP is not part of that change: `--strict-mcp-config`
  was already on a mapped node's argv, and whether it closes MCP is still
  unmeasured (E5) — nothing here should be read as measured MCP isolation.
  **`--no-agent <name>` and `--no-agent-mapping` do not give the old behaviour
  back.** They remove the mapping, and what you get is an ordinary planned node:
  its `Skill` tool returns, your environment does not. If your agents lean on
  your environment, no flag in this release reaches it.
- **A resumed leg maps nothing**, and says so — the rule ADR 0017 §6 already
  applies to skills, for the same reason: the only record a second process could
  re-stage a definition from lives in the run directory the previous leg's nodes
  could write. The node runs as an ordinary planned node under the same ceiling,
  without your agent's system prompt.
- **The plan printout, `README.md`, `README.ko.md`, `SECURITY.md`,
  `docs/LIMITATIONS.md` and
  `DESIGN.md` are rewritten with the code.** The v0.6.0 warnings are deleted
  rather than softened — a warning kept past its cause is still a false
  disclosure, and `cmd/oh-my-graph/wiring_test.go` asserts their absence.
  `README.ko.md` was missed on the first pass and carried the v0.6.0 text for
  one commit; a Korean reader was told their settings enforce a mapped node's
  scope while the English reader was told the opposite.

### Not fixed, and not claimed

- **The ADR 0017 §9 skill exclusion still stands.** An agent-mapped node still
  holds no `Skill` tool. What changed is the ground: §9 refused to lift it
  because a mapped node's settings loaded the repository's skill definitions,
  and they no longer load. It is now **a decision nobody has re-taken**, which
  the plan printout says in those terms.
- **The acceptance ADR 0022 §7 owed is run.** The staged directory this build
  writes carries `agents/` and no `skills/`, while the first measurement's
  carried both; the second measurement spawned this build's own argv against it
  — `--agent` resolved **3 of 3**, emptying the directory was the control
  (exit 1, `--agent 'x' not found`), the out-of-scope command was denied
  **0 of 3** and the in-scope control ran **2 of 2**, against a machine that
  still breached under the v0.6.0 argv the same hour.
- **The verify→read window is inherited from skill staging and is wider here.**
  `GuardAgentStaging` re-materializes before every spawn, but ready nodes run
  concurrently, so a sibling can rewrite the staged file between another node's
  check and the CLI's read. The node still runs under its own unmodified
  ceiling, so this is injection and not escalation — and an agent definition is
  a system prompt rather than a skill body the model has to choose. ADR 0022 §7.

## [v0.6.0] - 2026-08-12

**Minor because there is one new flag to type** — `--no-agent <name>`, the
surface v0.5.2 through v0.5.5 each went without. Behind it is a measurement that
**refused** the change it was run to make, plus the disclosure that refusal
forced.

ADR 0017's measurement (j) — 21 spawns, $4.16, claude 2.1.228, pre-registered in
its own commit — asked whether the agent-mapped skill exclusion could be lifted.
It cannot, and **not because the lift failed**: adding the `Skill` tool to those
nodes works, 3 of 3, and it costs the ceiling nothing. It stays because on a node
that loads your settings, a skill name resolves against definitions **the
repository you are working in can write** — a same-named `.claude/skills` file
committed to the fixture repo beat oh-my-graph's own staged corpus 3 of 3, and a
repository-committed `SKILL.md` fired 3 of 3 in a node whose prompt never
mentioned skills. So nothing was lifted, and `applySkillActivation`'s guard is
byte-untouched.

The finding nobody went looking for is the one that reaches users today: **an
agent-mapped node's declared scope is not enforced, in the code already
installed.** This release neither introduced that nor fixes it — it **discloses**
it, per node, and it is tracked as issue #161. The flag is the escape, not a
feature, and it is priced honestly: a declined node keeps its ceiling and its
skills by **giving up its agent**.

### Added

- **`--no-agent <name>` (repeatable, on `auto` and `chat`): decline ONE agent
  from auto-mapping and keep the rest.** `--no-agent-mapping` remains the
  all-or-nothing form. **What it costs is the agent**: a declined node is planned
  and run without it — no subagent persona, no agent-supplied prompt — and in
  exchange it keeps ceiling layer 1 (`--setting-sources ""`) and is activated
  like any other planned node, which is exactly the configuration (j) measured
  holding the scope ceiling and invoking the staged skill under an attributable
  name. This exists because measurement (j) changed what the opt-out is *for*: it
  used to be the remedy for a capability loss (a mapped node holds no `Skill`
  tool), and it is now also the only way to keep a node's declared scope
  enforced — an all-or-nothing switch carrying that weight prices one node's
  ceiling at every mapping the plan would have made. The **agent** is the unit
  because it is the only identifier that exists before the planner is paid: node
  ids are bought, agent names are your own files, and the plan prints the agent
  on the node line it took. The decline is applied after the single-candidate
  rule, never before it, so it can only ever remove a mapping — declining one of
  two ambiguous agents does not promote the other.

### Security

- **An agent-mapped node's declared scope is not enforced. This is shipped
  behavior, not something this release introduced or repaired.** Arm `G-T` ran
  the argv `runner.buildArgs` emits today for a mapped node — no staged plugin,
  no `Skill` in `--tools`, nothing this measurement proposes — and a node
  declaring `Bash(git *)`, unattended under `--permission-mode dontAsk`, ran an
  out-of-scope `touch` with `permission_denials: []`. The unmapped arm `G-ACT`
  denied the identical command and named it in its `permission_denials` record;
  the in-scope positive control `G-POS`, on the composite mapped argv, ran `git
  init` successfully, so this is a scope escape rather than the malformed
  ceiling probe this repo once mistook for a pass. **If you run `auto` with your
  own `~/.claude/agents`, this reaches the runs you have already made**: ADR
  0004's claim that an unattended `dontAsk` planned node declaring `Bash(git *)`
  cannot run an out-of-scope command does not hold for a mapped node, and has
  not since agent mapping shipped. What a mapped node can actually reach is
  **your own standing grants** — on the measuring machine `~/.claude/settings.json`
  allowed `Bash(*)` among 28 rules, which is what made the escape visible; a
  narrower settings file bounds it more narrowly. The engine's own guarantee is
  what is gone, not necessarily your machine's. It is not fixed here because `--agent` and
  layer 1 are mutually exclusive (ADR 0004 E2) — restoring the ceiling is a
  redesign of agent mapping, ADR 0017 §Compatibility's declined follow-up, now
  with a direct measurement behind it instead of an analogue. Tracked as **issue
  #161**; disclosed on the plan printout per mapped node, and in `README.md`,
  `docs/LIMITATIONS.md`, `SECURITY.md`, `DESIGN.md` and ADR 0017 (Decision §9).
- **The disclosure covers the whole surface a mapped node loads, not only the
  `Bash` scope half.** "Loads your settings" means the CLI's default sources —
  user, project **and local** — so the `.claude/` of the repository being worked
  in reaches such a node too. The same measurement showed a repository-committed
  `SKILL.md` invoked 3 of 3 unbidden and a repository-enabled plugin's skill
  firing; by the same loading, and marked **implied rather than measured**, that
  repository's project `CLAUDE.md` and its hooks reach it as well. Its **MCP
  servers do not**: `--strict-mcp-config` is a flag on the argv rather than a
  settings scope, and arrives with no `--mcp-config` beside it. The plan
  printout, `docs/LIMITATIONS.md` and `SECURITY.md` all say so, and the ceiling
  summary's other clause — "your CLAUDE.md, hooks and MCP servers are
  unavailable to them" — is now cancelled for mapped nodes in **two of its three
  parts**: their CLAUDE.md and hooks ARE available, the repository's included,
  while **MCP servers stay shut** on the argv's `--strict-mcp-config`. What is
  retired is a reassurance the argv does not keep, not the MCP guarantee it
  does.

### Changed

- **The plan printout no longer promises a mapped node's declared scope binds,
  and names what each mapped node lost, by node.** The retired clause was
  "(their declared tool list still binds)". Which tools *exist* is still bound;
  the scope inside them is not, because your standing permission grants load
  with your settings. In its place each mapped node gets its own line — no
  `Skill` tool, and a scope enforced only as far as your settings enforce it —
  the ceiling summary carries the same exception when the plan contains such a
  node, and the exclusion paragraph no longer calls lifting "unmeasured": it
  says lifting was measured and refused, and that the refusal was **not** about
  capability, since a user told "it does not work" would never think to check
  what their repository can supply.
  `docs/measurements/0017-lifting-the-agent-mapped-exclusion.md` is the record.
- **The remedy's own arm was run rather than inferred.** A review found
  `--no-agent`'s "attributable" claim rested on `ACT`, which ran only in the
  phase where the staged copy was the sole definition of its name. Arm `X-ACT`
  (3 spawns, $0.29, registered in its own commit before spawning and labelled
  post-hoc) re-ran that argv under the three-way collision: the staged copy won
  **3 of 3**, so `--setting-sources ""` bounds the definition search and the
  exposure is agent mapping's alone.

### Documentation

- **`README.ko.md` caught up with the measurement it was three claims behind
  on.** #160 rewrote the English README's agent-mapping and skills sections and
  left the Korean one describing the world before (j): it still promised
  *"선언된 도구 목록은 여전히 강제됩니다"* — the exact clause the plan printout
  retired — called the `--agent` + staged-plugin + settings combination *"아직
  한 번도 측정해보지 않은 조합"*, and told the reader *"노드별 opt-out은
  없습니다"* in the same release that ships `--no-agent`. All three now match
  `README.md`, including the measured breach and the repository-supplied
  configuration surface.

## [v0.5.5] - 2026-08-12

Nothing new to type here either. No flag, no command, no schema key. **Two** of
the three changes are one blind spot in different clothes: something a graph
could not see, and so could not be stopped by. `merge-shepherd` gains no node,
and no `result_matches` pattern in the repo changes; what changes is what its
two waits LOOK AT, and what they are allowed to call a timeout. They modelled
"is this PR ready" as one bot's opinion plus a check rollup and never read
`reviewDecision` or `mergeStateStatus`, so a **human** reviewer's
`CHANGES_REQUESTED` was invisible to the entire chain and the run **merged**
past it — a false GREEN rather than a hang, which is why it left no failed row
for anyone to report. `lint` gains the advisory for the same shape one level
down: a node declaring neither an `allowed_tools` grant nor a
`success_check.verify` has no mechanism of either kind that can observe a tool
denial, and measured over 164 nodes before it shipped it fired **62** times —
61 of them nodes reaching for tools they never declared, 23 of those `pr` nodes
told to `git push` and open a pull request while declaring nothing at all (the
62nd was this repo's own review node, fixed here rather than silenced). The
third change is the **mirror image**, not a third instance: nothing was hidden
from the graph, and everything was hidden from the author. A `use:` a graph
cannot reach fails loudly — it is a load error, not an unchosen option — and
still nothing in the product had ever said that where you save a graph decides
what it can cite. A fragment resolves against the graph file's own `fragments/`
sibling and nowhere else, so **86 of 87** lanes on this machine — 84 written
straight into `/tmp` — could cite nothing, and the corpus's zero adoption of
`use:` was reachability rather than preference. Reaching them cost **zero
lines** of resolution code: the boundary stays exactly as ADR 0013 wrote it,
`init` stops refusing a tree it could top up instead, and the rest is a
convention about where a graph is saved.

### Fixed

- **`merge-shepherd` no longer merges past a review it cannot see.** Both waits
  now project EVERY review with its author, and read `mergeStateStatus` and
  GitHub's `reviewDecision` alongside them. Until now
  `recheck` filtered the review list down to `coderabbitai`, so a **human**
  reviewer's `CHANGES_REQUESTED` was invisible to the entire chain:
  `ready-and-wait` passed (its condition named only the bot), `recheck`
  answered `RECHECKED <sha>` — green — the gate comment told the operator that
  review status was no longer theirs to confirm, and `merge` held a licence to
  `--admin` justified by a review that was "complete by construction", a
  construction with no human in it. The outcome was not a hang but a **merge**,
  which is why it never appeared in an incident log; it was found by tracing
  the chain (ADR 0021 §1). The same keyhole hid a conflicting PR:
  `mergeStateStatus: DIRTY` reached `merge`, failed there, answered `WITHHELD`
  — which PASSES — and ended the run green with nothing shipped.
  The review rules are written **per reviewer**, not over `reviewDecision`,
  which is reported rather than judged by: that field is PR-level, is never
  scoped to a SHA, and is not cleared by a `COMMENTED` review, so gating on it
  fails in both directions. At `ready-and-wait` it flips the instant CodeRabbit
  submits the `CHANGES_REQUESTED` that is this graph's ordinary path into
  `triage`, halting every normal run before its core automated step; at
  `recheck` it stays set over a review a later push superseded, naming an act
  that has already been performed and cannot clear it (PR #145, which merged
  correctly). So the bot's rule is SHA-scoped — its latest review on `head` is
  its current word — and a person's is not: theirs stands until that reviewer
  approves or a maintainer dismisses it, which is a different `unblock:` act.
- **`ready-and-wait` judged the check rollup by the name `test`.** `recheck`'s
  own comment has explained since 2026-08-09 why that is wrong — this repo
  carries three rollup entries, and a red one of the other two read as green —
  but the fix had only ever been applied to the newer node. Both waits now
  classify every entry.

### Not automated, deliberately — `merge-shepherd`

The graph names the act; it does not perform it. It does not resolve a review
thread (a GraphQL mutation needing `Bash(gh api graphql *)`, and a node closing
a reviewer's verdict on its own judgement — four times the operator read the
reason before closing, and once closing blind would have been wrong), it does
not post `@coderabbitai review`, and it does not sleep out a rate-limit window.
ADR 0021 §3 argues each.

### Added

- **`lint` and `run --dry-run` now warn when a node can observe no tool
  denial — it declares neither an `allowed_tools` grant nor a
  `success_check.verify`.** Advisory only: it never changes an exit code and
  never makes a graph invalid, for the standing reason a hand-written graph is
  your own reviewed artifact.

  The defect (issue #154) is that `allowed_tools` is the node's own grant, not
  a hint. A node that omits it inherits your Claude Code settings, so a tool
  you have not pre-authorised there is a tool the node cannot use — and
  nothing fails loudly. The subprocess explains the refusal in prose, exits 0,
  and a `result_matches` written for the happy path (`^DONE`) passes on that
  prose. The node is paid for, the ledger prints PASS, and the push, the `gh`
  call or the build never happened. Nobody who hits this learns anything from
  the run: the node "succeeded".

  The predicate is structural rather than a reading of the prompt. A node with
  a grant has authorised what it needs; a node with a `success_check.verify`
  has a check the *engine* runs, outside the node's own reply, so a denial
  that stops the work also fails the node. A node with neither has no
  mechanism of either kind. Two exemptions, because the warning's premise is
  false there rather than to quiet it: a gate node spawns no subprocess, and
  `permission_mode: bypassPermissions` grants every tool regardless.

  It was measured before it shipped, over the shipped graphs and fragments
  plus a 30-lane operator corpus — 164 claude nodes counted after fragment
  resolution, because that is the population the predicate is evaluated over —
  **62 hits**. Reading each one: 23 `pr` nodes told to `git push` and run
  `gh pr create`, 4 `push` nodes checked only for a `PUSHED|BLOCKED` token,
  9 measure/verify/accept nodes told to build a binary and write files, and
  25 review nodes whose prompts demand "verify by running commands, not by
  reading the diff". **61 of 62 were nodes reaching for tools they never
  declared**; noise was 1 in 62. Those 61 are the maintainer's own lanes (23 of
  them `pr` nodes that push and open pull requests while declaring nothing at
  all) — so the ratio is a statement about the graphs this project writes, not
  about somebody else's. The one noise hit was this repo's own
  `review-loop.yaml::review`, a pure judgment node — fixed in the graph rather
  than silenced, by declaring the read tools it reads the working tree with,
  because declaring is what the advisory asks of everyone else.

  One caveat the number does not carry: those 61 had never yet *failed* for
  want of a grant, because the machine that runs them pre-authorises broadly
  (`Bash(*)`). The finding is not that they were broken — it is that none of
  them could have told you if they had been.

  The warning names the fix, not just the absence: name the tools in
  `allowed_tools`, and where the work must be visible outside the node's own
  reply, add a `success_check.verify` command. A node that genuinely reaches
  for nothing declares that with an empty grant, `allowed_tools: []`, which
  the sweep reads as the author saying so (an absent key is no declaration).
  It is a declaration, not a sandbox: run-time behaviour is identical either
  way, since a hand-written node runs under your own settings by design.

### Changed

- **A latched condition is reported as itself, not as a timeout.** A poll is
  only honest over a *self-resolving* condition — one a machine already at work
  will clear with nobody doing anything. The other kind is **latched**: a review
  that requested changes, a workflow run awaiting a maintainer's approval, a
  required context with no reporter, a branch that conflicts with its base, a
  rate-limited bot that will not review again until asked. Both waits now
  classify before they poll, and answer `LATCHED <what>; unblock: <act>` at
  once. `LATCHED` fails its node on purpose, so the run halts and the act is on
  the first line of `<run-id>/failed/<node>.out`. ADR 0021 is the rule;
  `gh pr ready` in `ready-and-wait`'s step 1 was always its first instance.
- **`recheck`'s halting verdict is renamed** from `BLOCKED <sha>` to
  `LATCHED <sha> — <what>; unblock: <act>`, and re-defined. `BLOCKED` meant RED
  — "a judgment that something is wrong with the code" — which filed a workflow
  awaiting a click as a code defect, and filed four conditions that need a
  person as `UNSETTLED`, a word meaning *wait longer*. `triage` keeps its own
  `BLOCKED` for the different thing it means there ("I could not finish"), and
  the two verdicts no longer share a word. The pass/fail behaviour is
  unchanged: the token is absent from the pattern, which is how this graph
  halts on purpose.
- **`UNSETTLED` narrows** to "time ran out while something was still MOVING",
  and it still passes. Eight classes of condition that used to be able to reach
  it now halt instead.
- **`merge`'s `--admin` licence is narrowed and re-justified.** It may be
  reached only when the plain merge failed on protection or queue mechanics,
  `recheck` answered `RECHECKED` naming a mechanical `mergeable:` state
  (`BEHIND`, `BLOCKED`, `UNSTABLE`, `HAS_HOOKS`), AND that same line does not
  say `review_decision: REVIEW_REQUIRED` — the state where protection is
  holding the PR because nobody has approved it, which `mergeStateStatus`
  reports as `BLOCKED` like any other hold and over which `--admin` is exactly
  "a way past a review". `merge` answers `WITHHELD` there instead. The old
  justification — "the review is complete by construction (verify passed, CI
  and CodeRabbit concluded, comments triaged)" — is gone, because it was false
  about humans.
- **The diagnosis that survived is recorded where the next reader will look**:
  the graph's header, and ADR 0019 as a dated update — the ADR that was written
  about verdict grammar in 2026-08-04 while the second backlog item filed the
  same day, about a rate limit, held the general fact nobody generalized. It is
  narrower than the story it replaces, and that story is written down refuted
  rather than repeated: the three symptoms this was chased for do **not** share
  one cause (one rate limit that was never observed, one class of restarted
  checks `recheck` had already fixed and that was mostly self-resolving, and
  four halts whose verdict was correct and incomplete). What does hold is the
  keyhole above — both waits judged PR readiness by one bot's opinion plus a
  check rollup — and its worst outcome is none of those three, but the human
  review nobody found, which cost nothing anyone could count.
- **`graphs/review-loop.yaml`'s `review` node now declares
  `allowed_tools: [Read, Grep, Glob, "Bash(git diff*)", "Bash(git log*)"]`** —
  the same git reads the shipped review fragments grant for the same job.
  Behaviour is unchanged on a machine that already pre-authorises reading —
  `--allowedTools` adds a grant, it takes none away — but the node reads a
  working tree, so it says so.
- **README, README.ko and `docs/EXAMPLES.md` now say what `allowed_tools`
  buys you** at the first hand-written YAML a reader meets, and at the exact
  sentence in the auto/hand-written comparison where "keeps your settings"
  reads as a pure upside. It cuts both ways, and that is the half nobody was
  told. Their YAML examples declare grants too — a reader who copies the
  block above the paragraph should not then be warned about it.
- **`oh-my-graph init` tops a tree up instead of refusing it.** A target file
  that already exists is kept exactly as it is and reported as `kept`; only the
  payload files that are missing are written, and a second `init` over a
  complete tree writes nothing and exits 0. Before this, one existing file
  aborted the whole command — so a `go install` user who ran `init` between
  v0.4.1 and v0.5.2 had **no command at all** that could hand them
  `graphs/fragments/pr-publish.yaml`, which shipped at v0.5.3. The no-overwrite
  promise is stronger, not weaker: skipping modifies no existing file, so it
  cannot produce the half-replaced set the refusal existed to prevent; the
  write still uses `O_EXCL`; a failure still rolls back what that run created
  (`graphs/` included, when that run created it); and a kept edit is now named
  on stdout rather than inferred from a command that did nothing. Because a
  top-up can pair a file this release added with a kept copy of one it depends
  on, a kept file whose bytes differ from the binary's copy is marked
  `DIFFERS` and counted in the summary — otherwise the mismatch (a fresh
  template binding a `with:` key an older fragment does not declare) first
  appears as a load error naming a node, arbitrarily far from the `init` that
  assembled it. What refusing also bought, and is deliberately given up here,
  is a wrong-directory guard: `init` aimed at a directory that already held
  your own `graphs/` used to abort naming your file, and now writes the
  payload beside it and exits 0. The guard could not distinguish that mistake
  from the legitimate top-up it fires on, and the per-file `wrote` lines are
  the replacement — the list of what to delete is on screen.
- **Where a graph file is saved decides whether it can cite a fragment — and
  the product now says so.** A `use:` resolves against the graph's own
  `fragments/` sibling and nowhere else (ADR 0013 §Trust, unchanged), which
  means a graph written to a directory with no `fragments/` beside it — a bare
  `/tmp/lane.yaml` — can cite nothing. Measured against this machine's run
  corpus, that is **86 of 87** lanes, 84 of them directly in `/tmp`
  ([measurement](docs/measurements/0013-fragment-reach-is-decided-by-where-a-graph-is-saved.md)),
  so the corpus's zero adoption of `use:` was a reachability fact and not a
  preference. README, README.ko, DESIGN.md and the plugin agent brief state the
  consequence next to the rule, and the unresolved-fragment error now names the
  two fixes (author the graph in a directory that has a `fragments/` sibling —
  `oh-my-graph init <dir>`, then `<dir>/graphs/` — or put a `fragments/`, or a
  symlink to one, beside the graph) instead of restating the rule that already
  failed to help. Resolution itself is untouched: no search path, no flag, no
  embedded tier.

## [v0.5.4] - 2026-08-11

Nothing new to type. No flag, no command, no schema key — and both changes
here are the same question asked of two different bodies of graphs: what does a
graph owe the verdict its own nodes produce? The shipped review fragments pass
on **both** their verdicts, `CLEAN` and `FINDINGS:`, because a review's job is
to judge rather than to be clean — but nothing else in the engine reads a
verdict, so a `FINDINGS:` reply passed, the PR node below it opened anyway, and
the run ended green with the defect quoted in the PR body instead of fixed.
`backlog-batch`'s lane A now answers with mechanism instead of prose: its
review is narrowed to the clean verdict **and** carries the `feedback:` arc
that sends the findings back to the implementation, because a narrowed check
without an arc only turns the run where the reviewer did its job into the run
that reports FAIL. Lane B, `dev-review-pr` and `self-dev` keep the advisory
default — as a recorded decision now, said at each review node, and for the
last two also because their parallel review fan-out cannot hold an arc without
tripping ADR 0010's side-exit refusal. The same question, asked of a corpus
this repo does not ship: an operator's hand-written `review` → `apply` → `pr`
lane was proposed as a fifth entry in `graphs/fragments/`, and measured over
75 lanes against ADR 0013's own standard it does not survive. **Nothing was
extracted.** What the corpus does say about the shapes it copied *by hand* —
32 retyped verdict patterns, all 32 drifted — is the sharper finding, and the
better argument for `use:`.

### Changed

- **`backlog-batch`'s lane A now stops on review findings and re-runs the
  implementation with them. Lane B, `dev-review-pr` and `self-dev` behave
  exactly as before — deliberately, and each now says why at its own review
  node.** If you installed these graphs with `oh-my-graph init`, lane A is the
  one run you will see behave differently, and the new outcome is a
  `node_retried` event and a `feedback round 1/1` row rather than a failure.

  The defect (issue #151) was that a review's verdict reached nothing. The
  `review-style` and `review-security` fragments pass on **both** of their
  verdicts — `CLEAN` and `FINDINGS:` — because a review's job is to judge, not
  to be clean. But nothing else in the engine reads a verdict: `depends_on` is
  a success edge, and all three "the work was rejected" mechanisms — `retry`,
  `feedback`, `on_fail` — hang off *failure*. So a `FINDINGS:` reply passed,
  the PR node below it opened anyway, and the run ended green with the defect
  quoted in the PR body instead of fixed. No shipped graph had ever chosen
  otherwise, so the one graph shape the engine supports for this was
  undemonstrated.

  Gating is a **pair**, and lane A now ships both halves: `review-a`'s
  `success_check` is narrowed to the clean verdict
  (`result_matches: '^[*_`\s]*CLEAN\b'`, announced in the run's disclosure
  line like any override), and the same node declares
  `feedback: { rerun: dev-a, max: 1 }`, with `dev-a`'s prompt now reading
  `{{ feedback.review-a }}`. The arc is not decoration: a narrowed check
  *alone* would make the run where the reviewer did its job the run that
  reports FAIL, with the lane discarded and nothing repaired — the same
  mistake this project already closed for merge-shepherd's `WITHHELD`. With
  the arc, findings send the lane back through `dev-a → e2e-a → review-a`
  once, and a second round that comes back clean ends the lane **green with
  the defect fixed**. Only an exhausted loop FAILs, which is an honest report
  of a finding nobody repaired, and the graph's `on_fail: continue` keeps the
  other lane running. The price is in the header where the other rules are:
  worst case `(1 + max) × 3` = **6 node runs** for lane A, up from 3 — and
  that formula counts rounds, not attempts, so `e2e-a`'s inherited
  `retry: { max: 1 }` can add one run per round on top of it (8 in the true
  worst case). `max: 1`, not 2, because an unattended batch buys one repair
  round and the second belongs to a human who has read the first.

  Lane B is left advisory *as a decision* rather than as an unexamined
  default — a docs review's findings reach the draft PR body, which is where a
  human reads them, and a wording flag does not earn a second dev round — so
  one file now teaches both dispositions. `dev-review-pr` and `self-dev` stay
  advisory too, and for them it is also structural: their two reviews fan out
  of one `e2e`, so any feedback body reaching a review contains `e2e` while
  the sibling review depends on it from outside, and ADR 0010's side-exit rule
  refuses the graph at load (`feedback body has a side exit: "review-security"
  depends on body node "e2e" but is outside the loop`) — measured on both
  templates. Serializing the reviews to make room for one arc would spend the
  parallel fan-in those templates exist to demonstrate and still leave the
  other review ungated.

  What a green run of an advisory review still does **not** tell you is that
  the diff was clean; `docs/LIMITATIONS.md` ("A PASS row does not say which
  outcome passed") now names the review fragments as the asymmetric case, and
  says what a caller does about it. `internal/graph`'s
  `TestAGatingReviewCarriesItsRecoveryArc` keeps the pair from coming apart: it
  matches a realistic `FINDINGS:` reply against the *effective* pattern of
  every node that spliced a `review-*` fragment and fails any whose pattern
  rejects it without a `feedback:` arc — behaviour, not authoring form. ADR
  0010 carries the decision and the reasoning for why this is a test over
  `graphs/` rather than an eighth load rule.

### Documentation

- **The operator lane corpus has no extractable fragment — NO EXTRACTION,
  measured over 75 lanes (#150).** A prior pass proposed shipping the
  operator's hand-written lane scaffold (`review` → `apply` → `pr`, with a
  worktree) as a fifth entry in `graphs/fragments/`. Held to ADR 0013's own
  standard over this machine's run corpus — 210 run directories deduplicated
  by resolved graph JSON, of which **75 distinct lane graphs** carry a `pr`
  node and **33** carry the full three-role scaffold (40 have no `apply`, 28
  no `review`) — it does not survive, so **nothing was extracted** and the
  conclusion is what ships:
  `docs/measurements/0013-lane-corpus-has-no-extractable-fragment.md`, with
  ADR 0013 and DESIGN.md carrying the verdict. Three independent reasons, any
  one disqualifying. What repeats is *wiring* — `worktree`, `cwd`, the
  `depends_on` chain and the ids — which §Semantics makes a load error inside
  a fragment; what would be left to carry is `type` and a timeout. The one
  candidate whose *prose* repeats hard enough (35 `apply` nodes, 9 distinct
  texts, the top two accounting for 26 of 35 and sharing **82%** of their
  words by Jaccard) declares `result_matches` **0 of 35**, a verdict-first
  clause **0 of 35** and `allowed_tools` **0 of 35** — so both halves of the
  verdict convention *and* the grant would have been authored rather than
  extracted, which is designing a new node and calling it an extraction. Its
  second sentence keys off a `NO FINDINGS` contract neither shipped review
  fragment emits (they answer `CLEAN` / `FINDINGS:`), so it would have shipped
  the silent mismatch beside the fragments it sat next to, and no shipped
  template has an `apply` stage to cite it (`adr-driven-dev`'s three are the
  intra-file case, and overlap the corpus text by **13%** on the same Jaccard).
  And the corpus is a consumer, not a supplier: `result_matches` is declared
  by 0 of 35 `apply`, 0 of 47 `review` and 0 of 75 `pr` nodes, and `use:`
  appears **zero** times.

  Not innocent of the convention, though, which is the finding that decides
  it: **32** of those lanes' 349 nodes *do* declare a `result_matches` — all
  outside the three scaffold roles, every one a hand-retyped `e2e-verify`
  pattern, and **all 32 drifted** from the pattern that fragment ships. 19
  dropped to a bare `PASS`, which — since `result_matches` is a *search* —
  passes on *"the suite did not PASS"*; 7 kept the head anchor and lost the
  tail; 6 lost the emphasis class and take a false FAIL on `**PASS**`. A
  further 16 nodes are *named* after shipped fragments and carry no pattern at
  all. The shapes were not unknown to these lanes — they were retyped, and
  retyping lost the anchors, which is what `use:` exists to make impossible.
  Also recorded: a caveat on ADR 0013's own longest-common-suffix metric,
  which measures tail agreement and so understates convergence on prompts that
  diverge mid-body (8 words of suffix here against 82% word agreement) — the
  two metrics do not convert, and the document says so where it would
  otherwise have compared them.

- **`docs/LIMITATIONS.md` is re-stamped v0.5.3 → v0.5.4, and the tracker
  sentence v0.5.3 wrote is corrected: one gap now has an open issue behind
  it.** That release dropped a promise the file could not keep (*"each tracked
  as an issue"*) and replaced it with a measurement — `gh issue list --state
  open` *"returns nothing, and has since 2026-08-09"*. #151 opened
  2026-08-10, so the replacement went stale in a day, and the same principle
  applies to it: a version bump on a stale claim dates a lie forward. #151 is
  not an unrelated open issue, either — it is behind the review-fragment half
  of *"A PASS row does not say which outcome passed"*, the gap lane A's arc
  answers for one graph while `dev-review-pr` and `self-dev` stay advisory by
  decision, so the file now names the issue, the gap it sits under, and which
  part of it this release did not close. One `v0.5.3` in the file does **not**
  move: *"the fix ships in v0.5.3"* is provenance for the env-scrub fix,
  naming the release that first carried it.

## [v0.5.3] - 2026-08-10

Nothing new to type. No flag, no command, no schema key — every change here
corrects something already shipped. The one that matters most is the guarantee
CLAUDE.md leads with: `internal/childenv.Scrub` compared environment keys
exactly, and native Windows resolves them without regard to case, so a
lowercase `anthropic_api_key` walked straight through the scrub and billed the
run to the metered API that README and SECURITY.md told the user was inside
their subscription. Matching is `strings.EqualFold` now, unconditionally rather
than behind a `GOOS=windows` tag, so the Linux CI that runs every `make test`
exercises the rule that carries the promise. Beside it, `merge-shepherd` stops
asking the operator to perform the wait the graph itself skipped — a `recheck`
node polls the **final** SHA after triage pushes a fix, and answers in three
values so "still pending" is neither "green" nor "red". The plan printout stops
telling the reader of an agent-mapped node that it "already sees your real
skills": 10 real spawns say it carries no `Skill` tool at all and can therefore
invoke nothing, staged corpus or your own. `serve` names the build that is
answering. And "ADR 0016" resolves to one file again.

### Fixed

- **`merge-shepherd` waits for the checks its own fix restarted, instead of
  asking the operator to.** The chain was
  `verify → ready-and-wait → triage → approve-merge → merge`, with the CI and
  CodeRabbit wait sitting *before* triage. When triage pushed a fix, GitHub
  restarted the `test` check and sent CodeRabbit back for an incremental
  review — both after the wait had already gone by — so `merge` met pending
  checks on a SHA nothing had checked but triage's own `make ... local`. The
  graph knew: the comment above `approve-merge` named the gap and mitigated it
  by asking the human to *"confirm CI and review status on the FINAL SHA
  yourself before approving"*. That mitigation failed five times between
  2026-08-04 and 2026-08-08, and the loud shape was the rare one. Three times
  `merge` met the review triage's own push had restarted and answered
  `WITHHELD` — which *passes*, so the run ended **green having merged nothing**
  (run `20260804-170325`, PR #111, and `20260807-154947`, PR #137, both on a
  re-review still `PENDING` on the triage commit; `20260807-144230`, PR #134, on
  a *new* `CHANGES_REQUESTED` against it). Once it failed in this node's
  characteristic way — waited in the foreground, ran out of turn, and answered
  with a promise (*"checks are still running … waiting in the foreground until
  they conclude"*), which the anchored verdict correctly rejected, halting the
  run (`20260808-004132`, PR #137). And once the same promise **passed**,
  because it predates the anchor: `merge`'s `success_check` was still
  `exit_zero` alone (`20260804-143531`, PR #107, *"CodeRabbit's re-review is
  mid-flight … poller armed"*).

  The chain is now `triage → recheck → approve-merge → merge`. `recheck` polls
  synchronously under its own 20m timeout, exactly as `ready-and-wait` does and
  for the same reason (a node that backgrounds a poll has ended its turn, and
  its verdict is an interim report); the timeout outlasts the ~17 minutes of
  polling the prompt budgets, because a node killed by its own timeout produces
  no verdict at all and discards the run. It judges the **final** SHA and names
  it, reading everything from one `gh pr view --json
  headRefOid,statusCheckRollup,reviews` under a `--jq` projection — `headRefOid`
  is the final SHA, the rollup is that same commit's, and each review carries
  the `commit.oid` it was submitted against, so "CodeRabbit reviewed the final
  SHA" is a comparison rather than an inference. The projection is not
  cosmetic: raw `reviews` carries every review *body*, which measured 6.8 KB
  per read against 619 B projected, on the one node whose failure mode is a
  degraded interim reply. Two rules are stated over the data rather than over
  one check: green requires **every** entry of the rollup to be finished and
  good (`COMPLETED` with `SUCCESS`/`SKIPPED`/`NEUTRAL`, or a status context in
  `SUCCESS`) — this repo's own rollup has three entries, and naming one of them
  would have reported a red `stress` as green — and CodeRabbit's word is its
  **latest** review on that SHA by `submittedAt`, because a PR routinely
  collects several against one commit and a `CHANGES_REQUESTED` after an
  `APPROVED` is the bot changing its mind. `TRIAGED 0` short-circuits the wait
  only when that first read is *already green*: nothing was pushed, so no check
  will restart and polling would spend the whole timeout on a state that cannot
  change. A count of zero says nothing about the checks themselves, so pending
  or red entries — and a final SHA CodeRabbit has not reviewed yet — poll on
  exactly as they would after a push. Its grant is
  `Bash(gh pr view *)` and `Bash(sleep *)` — narrower than `ready-and-wait`'s
  `Bash(gh *)`, and there is no mutating form of `gh pr view` left to narrow
  away.

  **The verdict is three-valued, and that is the point.** `RECHECKED <sha>`
  (both concluded green) and `UNSETTLED <sha>` (still pending when the timeout
  arrived) pass; `BLOCKED <sha>` (concluded red) is written by the prompt and
  matched by nothing, so red checks halt the run *before* the gate — the same
  shape as `triage`'s `BLOCKED`. Collapsing the third outcome into either of
  the others lies: into `RECHECKED` it merges a PR nothing has checked, into
  `BLOCKED` it discards a paid pipeline over checks that were merely slow.
  `merge`'s `WITHHELD` is kept as the **fallback** for `UNSETTLED` alone,
  rather than the routine answer to pending checks (ADR 0019 §5).

  **What the third verdict costs, stated plainly**, because the shape of the
  win is easy to overstate: the case where the checks outlast the wait ends the
  run **green** via `UNSETTLED → WITHHELD`, having merged nothing — but only if
  the operator approves the gate over it; rejecting the gate is a ledger FAIL,
  so the green path is the approved one. That is not
  a state this change introduced — it is the state three of the five hits above
  already reached, by way of `merge`'s own `WITHHELD`, and the change is that
  the fact now has a name, a SHA and a place in the chain instead of being
  discovered at merge time. The green-run-merged-nothing outcome is made
  *rarer* and *visible*, not removed; the common case, where restarted checks
  conclude in a few minutes, is what genuinely improves — those runs now merge
  where they used to withhold. What is contained is the expensive half:
  `merge` answers `WITHHELD` to anything not beginning `RECHECKED`, so an
  `UNSETTLED` written without waiting costs an operator's glance and a refused
  merge, never a merge of unchecked code. Both graph header and DESIGN.md say
  this in the same words.

  **Why an ordinary node and not a `feedback:` arc.** Both halves were checked
  against the code, not just the ADRs. An arc fires only on a *judgment failure
  of the declaring node* (`schedule.judgeFeedback` returns early unless
  `node.Feedback != nil` and `isJudgmentFailure(cause)`) — but the event that
  must re-open the wait is triage **succeeding** with a push, and a PASS fires
  nothing. And ADR 0010's load rule 4 forbids a gate anywhere in a loop body
  (`graph.validateFeedback` rejects `member.Type == TypeGate`), so an arc aimed
  back past `approve-merge` would not load at all. A wait that must happen every
  time a fix lands is not failure recovery; it is a step, so it is a node.

  The gate comment no longer asks for the check — it states what `recheck`
  found and what to do about it (on `UNSETTLED`, rejecting is cheaper than
  approving into a `WITHHELD`). One seam is documented rather than closed: an
  incremental review of triage's own fix that merely `COMMENTED` passes
  `recheck` untriaged, since there is no arc back to `triage` — those comments
  reach the operator at the gate and nowhere else. `BLOCKED` halting while
  `UNSETTLED` passes is likewise now an argued decision rather than an
  omission: a red conclusion judges the code, and there is nothing to decide
  at a gate over it; a timeout judges nothing, so it is the operator's call.
  `TestRecheckVerdictIsThreeValued` pins the chain, the grant, the timeout
  against the prompt's loop budget, and all three verdicts, including that
  `BLOCKED` does *not* match and that neither passing token is reachable
  behind preamble.
  DESIGN.md's "Verdict patterns" gains the three-outcome shape and the rule it
  bends: a state word is admissible as a verdict only when the state is an
  outcome and something downstream bounds a premature one.

- **The env scrub now matches keys without regard to case, so the one
  load-bearing guarantee is true on every platform the binary runs on.**
  `internal/childenv.Scrub` compared keys exactly (`key == scrubbed`). Windows
  resolves environment variable names case-insensitively, so a child spawned
  there read `anthropic_api_key` as the same API-billing switch as
  `ANTHROPIC_API_KEY` — and that spelling walked straight through the scrub.
  The consequence was not cosmetic: a **metered API bill on a run README and
  SECURITY.md told the user was inside their subscription**, which is the exact
  failure the scrub exists to prevent. `docs/LIMITATIONS.md` disclosed it as a
  best-effort Windows caveat; a limitations bullet is not containment for the
  invariant CLAUDE.md leads with, so it is closed instead of documented.
  Matching is now `strings.EqualFold` on the whole key.

  **Unconditional, not `GOOS=windows`-tagged, deliberately.** A build-tagged
  variant would put the project's load-bearing guarantee on a code path this
  project's Linux CI can never execute — the same shape as the untested
  `shell_windows.go` / `procgroup_windows.go` paths, and how the hole survived
  in the first place. One rule that every `make test` on every platform
  exercises is worth more than a platform-exact one nobody runs. The cost on
  unix, where case genuinely distinguishes variables, is that a variable
  deliberately named as a case-variant of a billing key is also deleted;
  nothing reads it there, so nothing breaks.

  **Whole-key semantics are unchanged.** The doc comment's deliberate promise
  that `ANTHROPIC_API_KEY_BACKUP` survives still holds, and so does
  `anthropic_api_key_backup` — folding was applied to the key comparison, not
  loosened into a prefix match. No variable was added to the scrubbed set, and
  no exec seam's call site changed: all four already route their child env
  through `childenv.Scrub`, so all four inherit the fix.

  Asserted by `TestScrub_MatchesKeyCaseInsensitively`, which runs on Linux CI
  like every other test: revert `EqualFold` to `==` and its six case variants
  (lowercase, mixed, and partly-lowered spellings of both variables) survive
  the scrub and the test fails. `TestScrub_MatchesOnKeyNotSubstring` gained two
  case-folded survivors to pin that the prefix boundary did not move.
  `README.md`, `README.ko.md` and `SECURITY.md` keep their unconditional
  sentences — they are now true — and `docs/LIMITATIONS.md` drops the
  case-sensitivity bullet, leaving native Windows with two caveats (`cmd /c`
  verify syntax, no tree-kill) rather than three.

### Changed

- **`serve` states which build is answering.** Both pages carry the serving
  binary's identity in the footer — `v0.5.2 (cef30c6, built 2026-08-09 14:02)`
  — rendered once per process beside the gate token. The report behind it was
  *"serve dies silently and a stale binary holds the port"*, and **both halves
  are false**: `serve` reports a bind failure by name and points at `--port`,
  and a run's embedded live view binds port 0, so it cannot hold a fixed one.
  What actually happened is that a `serve` started two days earlier was still
  running, still serving the code it was compiled from, while
  `bin/oh-my-graph` had been rebuilt many times underneath it — which from a
  browser is indistinguishable from the new build misbehaving. Nothing was
  silent except the version. The label carries the VCS revision the toolchain
  stamped **and** the running executable's own mtime, because the version alone
  cannot separate two builds of the same tag and the revision is absent more
  often than it looks: `-buildvcs=false`, a proxy module build, and — measured
  here on go1.26.5 — a build from a linked git worktree, which is how this
  project's own graph lanes build. Read once per process, never per request:
  `go build -o` replaces the file at that path, so a later stat would report
  the build that replaced this one. That is `sync.OnceValue` inside
  `buildTime`, not a convention the two call sites keep — `Dashboard.serverFor`
  already constructs a server per request, one line away from reintroducing the
  confusion the label exists to end, and no test would have gone red. No
  dependency, no fifth exec seam, CSP unchanged.
- **The PR node is one shape, so it is a fragment now
  (`graphs/fragments/pr-publish.yaml`, ADR 0013).** A proposal to make
  `verdict:` a first-class schema key raised the prior question — before this
  change, 22 of the repo's 31 `result_matches` declarations repeated one of
  four patterns, and only three lived in fragments. Are the other 22 outside
  because they *cannot* be, or because nobody tried? Measured, by grouping
  every shipped node under its verdict pattern and comparing prompts
  word-for-word (longest common suffix, insensitive to line rewrapping): the
  five `PR <url>` nodes share **83 words, 75% of the shortest prompt**, and
  four of them declare the same grant, mode, handoff and `success_check`. The
  five-word claim in ADR 0013's Migration section — that `pr`, like `dev`,
  "diverges more than it shares" — was never measured and is wrong for `pr`.
  So `self-dev`'s `pr`, `dev-review-pr`'s `pr` and `backlog-batch`'s
  `pr-a`/`pr-b` now cite one fragment, binding a single substitution point
  (`publish`: which branch, DRAFT or ready, and a body naming graph-local node
  ids — wiring, the `evidence` precedent). **No shipped graph changes what it
  does:** every binding carries its graph's own head verbatim, so the four
  resolved prompts are byte-identical to the ones they replaced, no using node
  overrides a key, and the blast-radius goldens did not move.

  What was left, and why it decides the schema question: the other three
  repeated patterns do not group by *node*. The six nodes declaring
  ``^[*_`\s]*DONE\b`` — a repo-specific implementer, a docs-lane implementer, a
  feedback-loop implementer, an ADR-feedback applier — share **zero** words of
  prompt and carry four different tool grants; the `FINDINGS:`/`CLEAN` rounds
  in `adr-driven-dev` share 21% with the review fragments and hold `Grep`/`Glob`
  those fragments deliberately refuse. A fragment is a node's behavior, not a
  paragraph. `adr-driven-dev`'s `finalize` shares the PR node's 83 words and
  stays inline too: it also applies the last review's flags and gates on an
  engine-run `make local`, which is a second job and a wider grant.

- **An agent-mapped planned node cannot invoke a skill, and every place that
  said otherwise is corrected (ADR 0017, #130).** The plan printout used to
  close its exclusion line with *"that composite is unmeasured; it already sees
  your real skills"*. That is a capability claim and it is now measured false:
  **10 real spawns, $2.41, claude 2.1.226** (8 pre-registered, plus a 2-spawn
  user-scope arm added on review), judged only by a raw
  `{"type":"tool_use","name":"Skill"}` record in the node's own transcript and a
  planted skill's marker file. Told outright to use the skill, a node fired
  **0 of 3** under the argv `runner.buildArgs` really emits for an agent-mapped
  node; the same argv with `Skill` appended to `--tools` and nothing else
  changed fired **3 of 3**; with the skill in `~/.claude/skills` alone rather
  than in the project, 0 of 1 against 1 of 1; `permission_denials` was `[]` in
  all ten — the tool is not denied, it does not exist. So the exclusion is
  **total**: such a node reaches neither the staged corpus nor your own
  installed skills, even though its settings load. It is also concentrated,
  because agent mapping runs first and matches on the same signal activation
  would — the design, doc and review nodes are exactly the ones it takes. The
  printout now states that cost once per plan — including when *every* planned
  node is agent-mapped, the case where activation turns itself off and the hole
  is total — and names `--no-agent-mapping`, which gets a node out of the
  exclusion by turning agent mapping off for the whole run; there is no
  per-node switch. `docs/measurements/0017-agent-mapped-nodes-cannot-invoke-a-skill.md`.

  **The exclusion itself is kept**, deliberately and with the measurement in
  hand: the `--agent` + `--plugin-dir` + settings composite is still unmeasured,
  and the staged plugin's no-collision argument rests on layer 1 being `""`,
  which is false for precisely these nodes. Lifting it is gated on ADR 0017's
  new measurement **(j)** — the composite, run under the same discipline, with
  ADR 0004's E1 ceiling arm and a plugin-name collision arm — and no behaviour
  changes here beyond what the plan prints.

- **The #103 test fixtures carry neutral names (#140).** The unisolated-checkout
  tests reproduced the reported scenario using the reporter's own repository
  names, branch names, node ids and goal text. The shape is what those tests
  assert; the identifiers were the reporter's employer's and have no business in
  a public tree. Renamed to `deploy-config`/`search-index`,
  `deploy-impl`/`index-impl`, and a generic branch and goal. No assertion
  changed meaning — every one of them is about the scan's structure, not about
  what the strings say.

### Documentation

- **ADR 0018's compliance baseline is taken, before the clause it is a baseline
  for exists — 0 of 6 (#103).** §Falsification pre-registered a compliance
  number and warned that shipping §6's planner advisory first would destroy the
  status quo's version of it permanently. It is taken: over **6 real `auto`
  runs** and 18 qualifying nodes, **0 of 6** nodes that moved a foreign
  checkout's HEAD isolated themselves first — zero `git worktree add` and zero
  `git clone` across the whole sample, which is #103's collision shape six times
  out of six. All three pre-registered readings ship rather than the flattering
  one (headline **0/6**, strict-literal **0/18**, lenient-literal **12/18**, the
  last stated as explicitly *not* a compliance figure), because the metric is
  silent on a node that moves no HEAD; the goal-only stratum did not arise, so
  this is a prompt-mention population by construction, and the two nodes whose
  prompts never named the foreign checkout are excluded and said so.
  **§1 is not built and the decision does not move.** A baseline is
  advisory-free — there was no instruction here to disobey, so however low the
  number reads it cannot conclude that §6's advisory failed on its own terms.
  What the sample does establish is where to aim: in **6 of 6** the offending
  `checkout -b` was written into the node's prompt by the *planner*, not
  improvised by the node, and node compliance with an instruction never given
  was always going to read 0%. Raw transcripts are deliberately not committed —
  a node's system prompt carries the operator's whole local skill corpus and
  this repository is public — so what is archived is what the ADR asks a sample
  to preserve: per-node command lists, prompts, invocation roots, captures, the
  sealed pre-registration and the scripts, with `$HOME` scrubbed and every
  measured path a throwaway `/tmp` fixture.
  `docs/measurements/0018-unisolated-compliance-baseline.md`.
- **The retry ADR is renumbered 0016 → 0020, so "ADR 0016" resolves again.**
  v0.5.0 recorded the collision and deferred the repair — *"renumbering is its
  own change, and a link that resolves today should not be broken by a version
  bump … until one of them is renumbered"*. This is that change, and it is the
  cheaper of the two directions by a wide margin: the build-evidence record is
  cited by bare number dozens of times across the code, the retry record by
  four paths and a dozen prose mentions, so the retry record moves and every
  remaining bare "ADR 0016" is correct where it stands. Every citation was
  read, not pattern-replaced — the two records both have a §3 and a §6, and
  `scheduler.go`'s *"a retry starts cold … §6"* and `passProvenance`'s §6 are
  different documents. Renumbered:
  `docs/adr/0020-a-retry-carries-the-attempt-it-is-repeating.md`, carrying a
  dated note about where it came from; updated: README, README.ko, DESIGN.md,
  RUN-FEED.md, ADR 0010 and 12 Go files. ADR 0017 §2 and ADR 0018 §1, which
  each had to disambiguate mid-sentence in prose, now just cite the number.
  Earlier CHANGELOG entries are history and keep the number they were written
  with; the v0.5.0 bullet that recorded the collision now records its
  resolution too.
- **`docs/LIMITATIONS.md` is re-stamped v0.4.1 → v0.5.3, and stops promising a
  tracker it does not have.** The file said its gaps were *"each tracked as an
  issue rather than left as prose"*; `gh issue list --state open` returns
  nothing. The promise is dropped rather than replaced — this file *is* where
  they are tracked, and the issue numbers in it name the closed issue each gap
  was carved out of, which is provenance, not a tracker. Each stamped section's
  claims were re-read before the stamp moved rather than after, on the
  principle that a version bump on a stale claim dates a lie forward. Two moved:
  #103's now records the ADR 0018 baseline taken 2026-08-09 (**0 of 6**,
  pre-§6-clause) instead of describing that measurement as outstanding, and
  #11's *"skill and slash-command surfaces are not enumerable"* is scoped to the
  ceiling flags — v0.5.2 is the release that bounds skill surface by a different
  mechanism and prints the whole staged corpus, size and SHA-256, before the
  run. The same sentence in `SECURITY.md` is narrowed with it; it had drifted
  there too.
- **DESIGN.md points at `docs/LIMITATIONS.md`**, from the MVP DEFERRED list —
  the place a reader of the spec asks what is not here. Deliberately *not* a
  new `## Non-goals` section: the boundary already lives in three documents,
  and a fourth home for it is a drift generator.
- **`README.ko.md` carries #144's correction.** The Korean README still said
  the agent-mapped exclusion was the trade `agent:` has always made. It is not:
  an agent-mapped planned node holds no `Skill` tool, so it invokes no skill at
  all — not the staged corpus and not the user's own, even though its settings
  load. The measured paragraph (10 spawns, 0-of-3 vs 3-of-3, 0-of-1 vs 1-of-1
  at user scope), the concentration on design/doc/review nodes, and
  `--no-agent-mapping` as the run-wide switch are now in the translation, and
  the tool-ceiling parenthetical no longer implies those settings buy a skill.
- ADR 0013 gains the measurement above as a dated Migration update, and
  DESIGN.md's "Verdict patterns" gains the boundary it establishes: reuse
  deduplicates a verdict rule exactly as far as a node shape reaches, and no
  further — including for `coordinator.plannedVerdictPattern`, which is not in
  a graph at all but a Go string rendered into the planner's prompt, out of
  reach of any graph-side mechanism — and therefore pinned to the `e2e-verify`
  fragment's identical `result_matches` by a test instead. The clause sweep
  (`grep -c "Anything you need to qualify"`) is restated as what it counts:
  **26 declarations covering 33 runtime nodes**, since a fragment states the
  clause once for every node citing it. That sweep read 25/32 when the boundary
  was written; `merge-shepherd`'s new `recheck` node, above, is the 26th
  declaration, and DESIGN.md and the test that pins it ship the same pair.

## [v0.5.2] - 2026-08-08

Nothing new to type. No flag, no command, no schema key — every change here
corrects something v0.5.1 already shipped. Skill activation stops being a
mechanism with an unexplained yield: 44 real spawns say the 1-in-7 was a
threshold on how directly a description's trigger language matches the task,
not a verdict that nothing in the corpus fit, so an activated node's prompt now
carries one fixed sentence saying a corpus exists. The "fit" reading an earlier
draft of that entry attached to the same data is **retracted inside it**,
because the arm run to confirm it — a real, topical description alone in the
corpus — was pre-registered at 3 of 3 and came back 0 of 3. `lint` and
`run --dry-run` stop losing every advisory when the graph arrives over a pipe.
And every shipped verdict prompt now names where a caveat goes, after a `merge`
node reported FAIL over a merge that had landed: ADR 0019 keeps the anchor that
rejected it, and makes that one node's re-run cheap instead.

### Changed

- **Why v0.5.1's skill activation fired once in seven — measured, attributed,
  and answered with one sentence (ADR 0017 measurement (i), #130).** v0.5.1
  shipped activation wired, disclosed, and recording **1 `Skill` invocation
  across 7 activated planned nodes, 0 in the pre-registered run**, with the
  cause unknown. It is now known, from **44 real `claude -p` spawns** ($7.28,
  claude 2.1.223 then 2.1.224 / macOS) reproducing the argv an activated
  planned node receives — `-p`, `--output-format json`, `--permission-mode
  dontAsk`, `--setting-sources ""`, the staged `--plugin-dir`, `--allowedTools`
  (the node's own), `--tools` (those plus `Skill`), `--strict-mcp-config`,
  `--disallowedTools`, `--session-id` — and judged only by a `tool_use` record
  named `Skill` in the node's own transcript, **with the skill it names**, or by
  a planted skill's marker file appearing on disk. Never by asking a model what
  it can see. Every arm, every spawn, both pre-registrations, the runner
  scripts and the prompt files are in
  `docs/measurements/0017-skill-activation-yield.md` and the `probes/`
  directory beside it.

  | arm | task | corpus | sentence | fired |
  |---|---|---|---|---|
  | A | write `design.html` (planner register) | 35 **real** skills | no | **0 of 9** |
  | B | *the same prompt bytes* | 35 real | yes | **8 of 9** — all 8 chose `html-artifact` |
  | H | *the same prompt bytes* | 35 real + 1 **planted** trigger description | no | **3 of 3** |
  | L | *the same prompt bytes* | **1 real**: only the `html-artifact` B chose | no | **0 of 3** |
  | J | a task no skill fits | 35 real | yes | **0 of 3** (without: **0 of 3**) |
  | K | a `PASS`/`FAIL`+list output contract | 35 real | yes | **0 of 3** (without: 0 of 1) |

  A↔B varies one sentence, A↔H one added description, A↔L the corpus by
  subtraction, J and K the sentence alone. Other arms are **controls, not
  comparisons**: naming the skill fires 1 of 1 on both corpora, a planted
  trigger description fires 5 of 5 on its own task and corpus, the ceiling
  probe and its in-scope twin bracket layer 1.

  **Reach is settled and (i) is answered:** the staged descriptions do arrive
  at a `-p` node under layer 1 = `""` and are matched there (H, and 5 of 5 on a
  task whose planted description fits), so the ~6,008 tokens per invocation buy
  a block the model reads.

  **The cause is a threshold applied without deliberation, not a verdict that
  nothing fit.** An earlier draft of this entry called the 1-of-7 "a FIT
  number"; that is **retracted**. Reading which skill each spawn actually named
  killed it: all 8 of arm B's activations chose `html-artifact`, one of the
  user's **own** skills — the very one acceptance run 2 pre-registered as the
  expected match, sitting unconsulted in all 9 arm-A spawns. And arm L, run
  after the review that caught this, shows that same real description **alone
  in the corpus still does not fire unaided** — 0 of 3 against a
  **pre-registered prediction of 3 of 3**, recorded as wrong, with a 1-of-1
  positive control proving the corpus loaded. So a description written as the task's
  trigger clears the gate unaided; a real, genuinely topical, broader one does
  not — until one sentence asks for a deliberate look, at which point the model
  picks it unanimously. J bounds that: the sentence does not manufacture a fit
  where none exists.

  So `auto` now appends exactly one fixed sentence to an **activated** node's
  prompt, at the exact bytes measured:
  *"A corpus of procedures is available through the Skill tool; consult it if
  one fits this task."* It names no skill and no directory, so it announces
  **that** a corpus exists and never **which** one to use — the choosing stays
  in the node's own model, at run time, through the CLI's description gate.
  The `MEASURED YIELD` block `auto` prints before every run is rewritten to
  match: where v0.5.1 quoted the unexplained 1-in-7, it now cites these 44
  spawns — 0 of 9 unaided, 8 of 9 with the sentence — names the record file, and
  still says plainly that whether the **work** is better is not measured, at
  $0.205 a spawn against $0.134.

  **What this is still not known to buy, and what it costs.** On the one task
  where the deliverable could be checked mechanically, arms A and B are
  indistinguishable — 6 of 6 met every structural requirement of the node's own
  prompt and none wrote outside its cwd — while B's mean spawn cost **$0.205
  against A's $0.134**. That is ADR 0017's measurement (e), still unanswered;
  it was blocked on having nodes that activate at all, and this is what
  unblocks it. Arm K is where the sentence is measured **not** to work: a
  verification node's output contract moves nothing, with it or without.

  **Ruled out, with counts.** Tool starvation: 0 of 35 corpus skills declare
  `allowed-tools`, so no skill can request a tool at all here, and every
  comparison in the table holds the node's tools constant — A↔B varies one
  appended sentence, A↔H one corpus entry — so the 0-vs-fired splits are not
  tool differences. (`PREREG.md`'s cheaper version of that discriminator instead
  compared the one node that activated against two that did not; run 1's
  declared tools were never recorded, so **that** comparison is not established.
  The sealed pre-registration is left as written and the measurement record
  carries the erratum.) Corpus size: the gate fires
  against 36 staged skills — and arm L shows shrinking the corpus to 1 does not
  fire it, so size is not the lever in either direction. The agent-mapped
  exclusion: real, and it made `architecture-design` unbindable in both of run
  2's plans — but run 1 had **zero** exclusions and still scored 1 of 4, so it
  is not what drove the aggregate. **Not attributed:** whether the 6 non-firing
  nodes would have produced better work with a skill, whether A's zero is
  dilution or register (L points at register but changes the corpus to do it),
  and whether any of this reproduces on a second machine.

  The ceiling is re-verified under this argv by file existence — a node
  declaring `Bash(git *)` attempting an out-of-scope `touch` is denied and the
  file does not appear, while in-scope `git status` still runs — and re-run on
  2.1.224 through the shipped code path, output recorded in `probes/`. Layer 1
  is untouched. A staged plugin's bundled `references/` files **do** resolve
  (the token came back through `Skill` with no file-reading tool involved), but
  that is **one spawn with no control** and is listed as open, not closed.

- **`auto --verify-cmd` no longer writes the activation notice into
  `graph.json`.** The notice is deliberately not persisted — a saved graph is
  re-runnable through `run`, which has no staged plugin and no `Skill` tool, so
  an artifact carrying the sentence would promise a corpus its reader does not
  have. That held only when no verify command was supplied: `attachVerification`
  re-encodes the graph it is handed into the saved spec, and activation ran
  ahead of it, so the ordinary `auto "<goal>" --verify-cmd '…'` (activation is
  on by default) saved exactly the artifact the design forbids. Activation is
  now the **last** post-validation mutation, after every step that writes the
  spec, and the regression test covers both orderings — each suite previously
  covered one half.

- **The activation regression guard ADR 0017 owed is in the tree.**
  `internal/coordinator/skillactivation_manual_test.go` (`//go:build manual`,
  never CI, the `make smoke` posture) plants a fitting skill, spawns a real node
  under the real policy, and fails if the marker file does not appear — and
  asserts the argv alongside it, because the acceptance run was
  indistinguishable from silent absence from the outside. It carries the E1
  ceiling probe too. Its 2026-08-08 output on claude 2.1.224 (all PASS, $0.248)
  is recorded at
  `docs/measurements/probes/0017-skill-activation-yield/manual-guard-2.1.224.txt`,
  so "re-verified" is an artifact rather than an assertion about a test.

### Fixed

- **Every shipped verdict prompt now says where a caveat goes — and no anchor
  is relaxed to buy it (ADR 0019, #138).** Run `20260807-144514` ran all four
  steps of `merge-shepherd`'s `merge`, merged PR #135 for real (`83edfad` is on
  `main`), and was recorded **FAIL**: it had an exception to report — the PR had
  no local branch, so step 4's `git branch -d` had nothing to delete — and a
  prompt that said only what may *not* precede the verdict ("no markdown
  emphasis, no heading, no backticks, no preamble") and never said where a
  caveat may go instead. The caveat went on top and `MERGED <sha>` landed on
  line 3, under a pattern anchored at `^`. So all **28** prefix verdicts across
  `graphs/` and `graphs/fragments/` gain one clause — *anything you need to
  qualify goes AFTER the verdict, never before it* — written as one unbroken
  line, so `grep -c "Anything you need to qualify" graphs/*.yaml
  graphs/fragments/*.yaml` is a sweep that cannot silently miss a node. The
  four whole-reply pins (`haiku-smoke`'s `write`, the `e2e-verify` fragment,
  `apply-flags`'s `verify`, and `coordinator.plannedVerdictPattern`) keep the
  opposite instruction, *and nothing else*.

  **No pattern changes, and that is the measured decision, not the default
  one.** Every `result_matches` failure this project has on disk was replayed
  three ways: 187 runs, **218** verdict-bearing node executions (counted from
  each run's `events.jsonl`, because `state.json`'s `nodes` map keeps only the
  last record per node id and reads the same corpus as 211/18), **22**
  failures. 16 of the 22 were the check working — three of them the literal
  promise reply the pattern exists to reject — and 6 were pattern
  misjudgements, all in the same direction. Dropping the anchor admits 11
  replies, **8 of them among the 16 correct FAILs**, because `NOT READY`
  contains `READY`. `(?m)` admits 3 and none of the 16, which reads as a
  bargain and is not: only 2 of the 3 are later-line verdicts, the third is a
  `$`-pinned reply that `(?m)` admits by *deleting* the pin, and constructed
  against `merge`'s own pattern `(?m)` accepts five promise replies — one of
  them the prompt's own `MERGED 4f2a1c9` example quoted back — that `^` rejects
  all five of. Position is the lock, and `(?m)` remains unused in this repo.

  **The cost moves instead, at the one node where it was unusually expensive.**
  `merge`'s false FAIL was the only one in the repo a re-run could not safely
  correct — the re-run re-entered `gh pr merge` on an already-merged PR under a
  grant too narrow to look at what had happened. Its prompt gains a **step 0**
  that establishes the PR's state before changing it (already `MERGED` ⇒ do not
  re-enter step 1, take the SHA and report the merge that landed), and its
  `allowed_tools` gains the two read-only commands step 0 needs,
  `Bash(gh pr view *)` and `Bash(git merge-base *)`. The SHA is confirmed only
  *after* `origin/main` is refreshed, with `git merge-base --is-ancestor`: a
  confirmation read off a stale remote-tracking ref is how a re-run turns a real
  merge into a second false FAIL. The grant's purpose — the only thing this node
  may *change* is the merge itself — is intact, and the four-exec-seam invariant
  is untouched (these are tools inside a claude node, not a new spawner).
  `internal/graph/shipped_graphs_test.go` now pins the shipped pattern against
  the whole promise family **including the `(?m)` counterfactual**, so the
  measurement stops being a claim in a document.

- **`lint` and `run --dry-run` no longer lose every advisory when the graph is
  not a seekable file (#134).** Both read the path **twice** — once through
  `graph.LintFile` for the issue list, once more through `graph.LoadFile` for
  the graph the advisory sweeps run over — and two reads equal one only for a
  regular file. Served over a pipe (`lint <(…)`, `/dev/stdin`, a FIFO) the
  second read comes back empty, decodes to an empty graph that passes every
  check, and the command prints `valid` at exit 0 with every advisory silently
  gone. The *issues* survive, because they came from the first read; only the
  advice is lost, which is the half nothing else re-states. `graph.LintLoadFile`
  now returns the issue list, the fragment advisories and the `LoadResult` from
  **one** read, and `LintFile` becomes a thin adapter over it, so both commands
  sweep the graph they actually linted. Pinned by a FIFO regression test that
  fails on a deadline rather than on a wrong value, because a second read of a
  FIFO blocks instead of returning empty — and one whose writer reports its own
  open/write/close failures on a channel, so a setup failure cannot masquerade
  as the verdict.

  Recorded alongside it, in ADR 0010: **why the fan-in reach sweep stays
  advisory.** The escalation candidate for issue #118 — hard-fail when the
  declarer interpolates the unreachable producer's artifact — is not safe to
  make a load error. `{{ artifacts.P }}` proves the declarer *reads* P while the
  defect needs it to *judge* P, so `scope → {criteria, impl} → review` judging
  `{{ artifacts.impl }}` against `{{ artifacts.criteria }}` satisfies the
  predicate while being correct; and #118's own reviewer named its files by
  literal path, so the rule would have missed the motivating bug. Neither sound
  nor complete, refused in ADR 0010's alternatives, and pinned as a negative
  regression test.

### Documentation

- **ADR 0018 — isolation stays scoped to the invocation repository; a second
  repository is disclosed, not provisioned (#135,
  [#103](https://github.com/jitokim/oh-my-graph/issues/103)).** The second half
  of #103, the one #123 and #129 did not touch: a goal that names a *second*
  local repository gets no managed worktree there, so a node switching HEAD in a
  checkout another process is standing in will collide with it. The record is a
  decision **not to build** — and it says what would change that, which is the
  only way a "no" is honest. It writes down the shape any future proposal has to
  start from (a user-supplied `--repo`, never a planner-named or
  detector-derived path, because `validatePlannedNodeCwd` closed that surface
  deliberately), what partial failure and cross-repository cleanup would cost,
  and a pre-registered measurement whose failure converts the refusal into a
  build. Nothing in the engine moves: ADR 0005's single-repository scope is
  **affirmed** and carries a dated pointer here, ADR 0004's ceiling is
  untouched, and `validatePlannedNodeCwd`/`validatePlannedNodeWorktree` stay
  exactly as closed as they were. SECURITY.md and `docs/LIMITATIONS.md` now
  point at that record instead of saying only "would need their own ADR".

## [v0.5.1] - 2026-08-07

The reachable release. v0.5.0 shipped the whole of ADR 0016's build-evidence
engine and nothing that reached it: the attachment, the serialization, the
`retry.max` cap and the provenance qualifier were all in the tree, and no
`FlagSet` parsed `--verify-cmd`, so the binary a reader installed could not do
what that release's own README described. That gap is the reason to cut now
rather than wait for more: the flag is parsed, and issue #119 is closed by a
version you can actually install. Around it the planner stops getting two
things quietly wrong — it now aims a feedback arc at a node the loop reaches
every producer from, rather than at one producer while another sits outside the
body, and it names the repositories a goal reaches that `auto` creates no
worktree in and takes no lock on. And skill activation lands wired, disclosed,
and measured at 1 invocation across 7 activated planned nodes in aggregate,
with 0 in the pre-registered run: an activation-eligible planned node really is
spawned with a staged plugin directory and the `Skill` tool, ADR 0017's
acceptance test was run on 2026-08-07 under a pre-registered rule and
FAILED, and both of those sentences are in the entry below — shipping the first
without the second is the class of claim this project spent the week removing.

### Added

- **Planned nodes get Claude Code's own skill activation (ADR 0017, #130).**
  `auto` now stages your whole `~/.claude/skills` corpus into a plugin
  directory it owns (`~/.oh-my-graph/runs/<run-id>/skills-plugin/`), passes each
  **activation-eligible** planned node `--plugin-dir <that>`, and adds `Skill`
  to its tool list. Eligible means every planned node that is not agent-mapped,
  on a run where activation is on at all: a missing or empty `~/.claude/skills`,
  or a staging failure, turns it off for the whole run and says so on its own
  line. An agent-mapped node is excluded, gets neither half, and — as before
  ADR 0017 — trades layer 1 away to resolve the agent, so it loads your
  settings.
  Which skill a node uses is then left to the node's own model, at run time, by
  description — where the mechanism this replaces picked one at plan time by
  name and reached 7% of planner-authored node ids, one of its five matches
  being semantically wrong.

  **The delivery is proven and the adoption is not, and the feature is on by
  default.** The maintainer acceptance tests (2026-08-07) measured **1 skill
  invocation across 7 activated planned nodes** in aggregate — the one came from
  run 1, and both arms of the pre-registered second run measured **0**;
  ADR 0017's verdict is FAIL and its status is `Proposed`. The first leg's argv
  carries both halves of the mechanism, and a resumed leg's argv carries
  neither — both are pinned by tests at the argv layer, on the `auto` and
  `resume` paths respectively — but nothing yet shows a planned node
  choosing to use a skill. The measured yield is printed beside the token
  price before every run, and `--no-skill-activation` is the switch.

  **The tool ceiling does not move.** Layer 1 stays `--setting-sources ""`:
  measurement (g) showed that relaxing it lets a node that declared
  `Bash(git *)` run an out-of-scope command, because `--tools` bounds tool
  names and not scopes. For every activation-eligible node your settings,
  CLAUDE.md, hooks and MCP servers still do not load; an agent-mapped node is
  the pre-existing exception, and it is excluded from activation for exactly
  that reason. `--plugin-dir` is not a ceiling layer — it supplies definitions
  and grants nothing — and `plannedToolAllowlist` is unchanged, so a plan still
  may not DECLARE `Skill`.

  The staged directory is re-created and verified from a manifest before every
  node spawn of the leg that staged it, so a node cannot leave a skill behind
  for a later one. The nodes
  read the staged copy, so your own `~/.claude/skills` tree is read once, at
  staging: editing it mid-run neither changes the run nor stops it. Only a
  staged file that has to be restored while its source no longer holds the
  planned bytes fails a spawn, and the message says the fault is the engine's
  rather than the node's.

  **Only the first leg of a run activates skills.** A resumed leg is a fresh
  process with no in-memory manifest, so the only record it could re-stage from
  is the one inside the run directory — which the previous leg's own nodes
  could have rewritten, since they run as the same user and `Write` is
  unscoped. A forged record would not just add a skill; re-staging deletes what
  the record does not name, so it would replace the user's corpus outright.
  Rather than trust it, `resume` withholds the `Skill` tool and the staged
  directory from every node and prints one line saying why. Restoring
  activation on resume needs an integrity anchor outside the run directory,
  which ADR 0017 §6 specifies as a design and defers.

  The plan printout names every staged skill with its size and SHA-256, the
  nodes reached, the agent-mapped nodes excluded (that composite is
  unmeasured), and what the corpus adds to every activation-eligible node
  invocation of that leg — including retries and feedback re-runs. What it can
  no longer name is *which* skill a
  node will use; nothing knows that before the model does.

  `--no-skill-activation` turns it off, on `auto` and on `resume` alike
  (de-escalation only, so no resumed leg can widen a run's ceiling).

- **`auto --verify-cmd` / `--verify-timeout` — build evidence becomes reachable
  (ADR `0016-build-evidence-is-a-user-supplied-engine-command.md` §2, #119).**
  v0.5.0 shipped the whole engine for this and no way to
  reach it: the attachment, the serialization, the `retry.max` cap and the
  provenance qualifier were all in the tree, but no `FlagSet` parsed the flag,
  so every `auto` run still took the zero-config path. It parses now.
  `oh-my-graph auto "<goal>" --verify-cmd './gradlew build'` hands the engine a
  command it runs itself at every sink node of the plan, one at a time, after
  that node's own subprocess — and a sink that fails it fails the run, so a
  check node can no longer certify a branch that does not compile. No node is
  granted anything by it and no ceiling layer moves.
  `--verify-timeout` bounds one execution (10 minutes by default, which is also
  the ceiling — not the 2-minute default a hand-written check gets, because a
  cold Gradle or Cargo build is what that default was not sized for). The
  command is checked for runnability *before* the planner call when it is a
  plain program invocation, naming the path or the `PATH` that was searched,
  because a planner call is billed whether or not the plan turns out to be
  usable; one carrying shell syntax skips that check rather than have the
  pre-flight re-implement the shell. `--plan-only` prints the command and the
  sink nodes it will run at, so the flag that shows you a plan before you pay
  for it also shows you the shell the engine will run. Every cycle of a
  `--max-cycles` goal loop plans a new graph and every one of them carries it.
  With no `--verify-cmd`, `auto` now prints what the run will not check and, if
  it recognizes the project, the command that would change that (§3).
- **Known cost of the above:** an `auto` run started with `--verify-cmd` cannot
  be resumed. `resume` refuses every verification it finds in a run directory
  on an auto graph rather than replay engine-run shell on trust
  (ADR `0016-build-evidence-is-a-user-supplied-engine-command.md` §4),
  and it has no `--verify-cmd` of its own to re-supply the command with, so the
  refusal is terminal. Continuing such a run with the check silently dropped is
  the failure the mechanism exists to prevent; it stops instead. `chat` takes
  no `--verify-cmd` either.
- **`auto` warns at plan time when a goal reaches a repository it cannot
  isolate** (#103). If the goal or a planned prompt names an absolute path that
  resolves into a git checkout outside the invocation repository, the plan
  printout — the same one `auto --plan-only` renders — names that checkout, says
  whether the goal or which nodes named it, and states plainly that oh-my-graph
  creates no worktree there and takes no lock on it, so a node that switches a
  branch in it races whatever else is standing in that directory. It is a
  warning and never a refusal: a multi-repository goal is legitimate and the
  engine simply cannot isolate it. The rule requires the path to resolve into a
  real `.git` checkout (a clone or a linked worktree), so `/tmp` scratch paths,
  templated paths, files that do not exist and every path inside the invocation
  repository stay silent — verified against the graphs this repo ships — and it
  drops a checkout that is a tool installation rather than a work tree (rooted
  under `/usr`, `/opt`, `/Library`, `/System`, `/nix`, or under a dot-directory
  of `$HOME`), because Homebrew, nvm, oh-my-zsh, a plugin marketplace and a
  chezmoi-managed `~/.config` are all real clones and a warning about a HEAD
  nobody will switch is what teaches a reader to scroll past the block. It is
  computed on the planner's own prompts before any `SKILL.md` body is inlined,
  so a skill's documented paths are never blamed on the plan. The printed text
  states its own blind spots (a path built at run time, one arriving through
  `--input` or an artifact, a relative path, a tool installation's own clone),
  and says outright that in `auto` mode oh-my-graph isolates no checkout at all
  — not even the one it was invoked from, where planned nodes work directly in
  your tree — because a heuristic that reads as complete, or a boundary that
  reads as protection, is worse than none.

### Removed

- **ADR 0012's plan-time skill inlining, in the same change that replaced it
  (ADR 0017 §8).** The name-token matcher, the 16 KiB body cap, the `{{`
  neutralization, the nonce fence around inlined skill bodies, `SkillMapping`
  and the `skill mapped:` / `skill skipped:` printout lines are gone. The two
  mechanisms must never coexist in a shipped build: a node holding both would
  receive the same skill twice, pay for it twice, and become unattributable.
  The scan (`scanSkillDirs`, `Plan.SkillScan`) and its printed disclosure
  survive, re-pointed at activation. `--no-skill-mapping` still works as a
  deprecated alias for `--no-skill-activation`, with a printed notice.
  ADR 0012's required measurements (a) and (b) are voided, not discharged.

### Changed

- **`auto` no longer plans a feedback loop that cannot repair what it judges**
  (#118). When a planned reviewer fans in from several producers, the planner
  prompt now requires `feedback.rerun` to name a node the loop reaches every
  producer from — normally their nearest common ancestor — and
  `coordinator.validatePlannedFeedbackReach` refuses a plan that aims the arc at
  one producer while another sits outside the body, naming the covering target
  to aim at instead. A refused plan buys the usual one corrected re-plan, so the
  fix costs a planner call rather than the run. The rule reuses
  `graph.LintFeedbackReach` — the advisory `lint` prints for hand-written graphs
  — and fires only where that sweep found a covering target, so a shape no
  aiming of the arc could repair, and a stable-context parent upstream of the
  rerun target, both still plan (ADR 0010, amended).

### Fixed

- **A run deleted while the dashboard was sweeping came back as a card.** A
  sweep takes two observations of a tree that changes under it — the listing
  that decides membership, then the per-run stamp that decides content — and a
  run deleted between them stamped as empty, which was announced as a *changed*
  card: a tile, and a click that lands on a 404, for a run that is already gone,
  with its `card_removed` a whole tick behind it. The window is as long as the
  sweep, so it widened with every run on the dashboard. Confirming the run
  directory after the stamp settles which of the two an empty stamp was, and the
  same sweep's removal pass now speaks for a run whose listing was stale.
- **The session-limit pause hint pointed a `--verify-cmd` run at a resume that
  cannot work.** An `auto` run started with `--verify-cmd` cannot be resumed
  (see above) — but on a session-limit pause the engine still printed
  `oh-my-graph resume <id> --retry-failed`, and following it earned a refusal
  whose own remediation names a flag `resume` does not register. Two wrong
  instructions in a row, on precisely the long, expensive run someone pays for
  build evidence to protect. The hint now says the run cannot be resumed and
  why, instead of printing a command that exits 1.

### Documentation

- **The dashboard screenshot showed the defect v0.5.0 fixed (#127).** The
  shipped image predates ADR 0015 and has two runs sitting in IN FLIGHT that
  had in fact been dead for days — the exact thing the lock-derived `ABANDONED`
  rule exists to stop — and it predates the header's gate-paused chip. Retaken
  against a live v0.5.0 dashboard: four real runs in flight, one of them a
  fan-out captured while its three parallel nodes were mid-flight, so the
  mini-DAG card shows a graph shape rather than a straight line. They are real
  `oh-my-graph run` invocations against the shipped `haiku-smoke` example and a
  small fan-out graph; nothing in the frame is mocked. Both READMEs' alt text
  described the old counts and now describes what the new image shows.
- **CONTRIBUTING says how a local build becomes your `oh-my-graph` (#132).** It
  documented `make build` and stopped there, leaving the step every contributor
  improvises: README's `go install …@latest` fetches a *released* version, so
  following it while iterating runs someone else's binary against your edits.
  The symlink-onto-PATH recipe contributors were already using ad hoc is
  written down, `mkdir -p ~/.local/bin` included, since `ln -sf` fails without
  it on a machine that has never had one.

## [v0.5.0] - 2026-08-06

The evidence release. It is about the distance between what a run said and what
was actually observed. A `PASS` stops printing the same word for "the engine ran
your build and it exited 0" and "the model typed PASS": every terminal pass now
carries a provenance qualifier — `verified`, `self-reported`, `exit-only`,
`approved` — and the engine grows the machinery to attach a build command *you*
supplied to a planned graph's sink nodes, so a verification node can no longer
certify a branch that does not compile (ADR 0016). A run whose process died stops
reading `RUNNING` forever: liveness becomes the kernel's `flock(2)` on
`resume.lock`, `ABANDONED` is derived at read time, and the rule is stated once so
`runs list`, the dashboard, the single-run view and `watch` cannot disagree about
it (ADR 0015). A node that fails keeps its own account — its reply, not just the
engine's 240-rune summary of the failure — and the retry that repeats the attempt
is handed it back, nonce-fenced and bounded. And a plan the validator refuses buys
one corrected planner call instead of nothing at all. Around them: `auto
--plan-only` previews a plan before it spends, a passing node's spend reads against
its budget, `serve` refuses to be framed, run directories go owner-only, and the
docs get a sweep that corrects what they had been claiming.

### Added

- **A `PASS` now says how it was reached, and build evidence becomes a command
  you supply (ADR 0016, #119).** A user ran `auto` against a Kotlin repository:
  the planner produced a final verify node holding `Bash(git *)`, which checked
  the branch name, the commit list and a clean tree, replied `PASS` in 17
  seconds for $0.13 — and certified a branch that did not compile, after the
  apply node it was checking had spent $11.01. Every ledger row read `PASS`.
  Widening `plannedToolAllowlist` fixes neither half: a node holding the build
  tool can still run the build, watch it fail, and reply `PASS`. So two things
  land instead. **The ledger's terminal `PASS` gains a provenance qualifier**
  from a closed set of four — `verified` (the engine ran a `success_check.verify`
  and judged its exit code, plus `output_matches` when declared),
  `self-reported` (a `result_matches` pattern matched what the node *said*;
  nothing outside the model's narration was observed), `exit-only` (the
  subprocess exited 0 and no predicate beyond that was declared) and `approved`
  (a human released a `type: gate` — no subprocess, no predicate). It rides in
  the `VERDICT` column of `show` and the end-of-run table, and on the
  `node_passed` event. A `FAIL` carries none: it states its cause in `DETAIL`
  instead. `verified` means **measured, not correct** — `verify: { command:
  "true" }` yields it; the ledger reports how a verdict was reached, never
  whether the check was a good one. **And the engine learns to attach a build
  command to a plan's sinks**: `coordinator.VerifyCommand` is set by trusted Go
  code strictly *after* `validatePlannedNodes`, so a planner-authored `verify:`
  stays refused outright and the plan the validator saw never contained one; the
  ENGINE runs it through `verify.ShellVerifier` — the second exec seam, already
  `childenv.Scrub`'ed — and judges the exit code itself, granting the node
  nothing and leaving every ADR 0004 ceiling layer byte-for-byte as it was.
  Attachment is to sink nodes (a full build per node costs up to six serialized
  builds per run, and is wrong for every goal whose point is to *reach* green),
  it is disclosed with the plan the way agent and skill mappings are, it is
  snapshotted into `graph.json`, and a planned `retry.max` is capped so a sink
  cannot run your build 41 times. Repo detection (`gradlew`, `package.json`,
  `Cargo.toml`, …) only ever **prints a suggested command** — never a grant,
  because `Write` and `Edit` are in the allowlist and node 1 creating a
  `package.json` would otherwise widen node 2's set. `resume` refuses a
  snapshot-borne verification on an auto graph rather than replaying it on trust.
  **Stated exactly, because it is the part a user will reach for:** `auto` does
  **not** parse `--verify-cmd` / `--verify-timeout` in this release. The engine
  side is implemented and tested and the ADR is `Proposed`, not `Accepted`; the
  flag that reaches it from a command line is not in the tree, so every `auto`
  run still takes the zero-config path and the provenance qualifier is the half
  of this you can use today. SECURITY.md and ADR 0016 both say so in their own
  words. ([#123](https://github.com/jitokim/oh-my-graph/pull/123))
- **A failed node's own reply survives it, and the retry that repeats the
  attempt is told what it said.** A node that failed its `success_check` used
  to leave behind only the engine's account of the failure — a 240-rune
  `Detail` — and for the largest single class of failure, a `result_matches`
  miss, that detail is `result did not match /<re>/`: **zero bytes** of what
  the node actually said. Across 133 local runs, 11 of 36 `node_failed` events
  were exactly that, carrying $21.33 of spend and no reply at all. The reply is
  now persisted to `<run-dir>/failed/<node-id>.out` (atomic temp+rename, capped
  at 256 KiB head-and-tail with the cut stated in the file). It is deliberately
  **not** an artifact: no `{{ artifacts.<id> }}` resolves for a failed node and
  no `handoff: session` child can resume one — the file is the node's own
  account, in its own subdirectory, out of the flat artifact glob.

  On top of that record, a **retry now carries the attempt it is repeating**
  (ADR 0016). When a check judged the previous attempt, the retried attempt's
  prompt quotes that attempt's own reply as nonce-fenced data — the same
  per-call `crypto/rand` fence the planner's re-plan and the goal assessor use,
  now shared as `internal/fence` rather than hand-rolled twice. **Exactly one**
  prior attempt is carried and it never accumulates: every attempt's prompt is
  rebuilt from the interpolated node prompt, so the added cost is flat in the
  attempt index rather than triangular, and the quoted reply is bounded at 8000
  bytes. The success check is **not** quoted — not its expression, not the
  detail that embeds it — because feeding back a `result_matches` regex teaches
  the cheapest possible pass, which is to print whatever it matches; the node
  is told its attempt did not pass, told not to argue the verdict, and pointed
  back at its own instructions. Causes that rendered no verdict on the reply
  carry nothing: a spawn error, an interpolation error, `budget_exceeded`, and
  a verification that could not be *completed*. A `handoff: session` retry
  still starts **cold** — unchanged — and the quote now says so out loud.
  `resume --retry-failed` re-reads the reply off disk for each node it clears,
  which is why the file is written by one process and read by another; that
  re-execution is a retry too, so it starts cold on the same terms — a session
  node does not resume its parent there either, and the file it was seeded from
  is removed once this leg's execution passes.

  **This is on by default and it costs money:** up to roughly 2k tokens of
  quoted reply per retry attempt of a judged failure. It is bounded and flat,
  never compounding. Whether it raises retry pass rates is **unmeasured** —
  this repo has no harness for that, and the claim made is only the narrower
  one, that re-running a node with no knowledge of its own rejected attempt is
  strictly less informed than re-running it with one.

  `state.json` gains one additive optional field, `judged` (**no schema
  bump**): whether a FAIL was a verdict on the work or a fault in the
  machinery. It is what carries that distinction across the `resume` process
  boundary, and it answers a question that previously required reading `detail`
  as English.

  Not fixed here, and named rather than left silent: `{{ feedback.<id> }}`
  still inlines a full model reply into a body node's prompt with no fence and
  no length bound. That defect predates this change; fencing it alters what
  body nodes are shown, which is its own change.

- **A validation refusal buys one corrected plan, not a dead end.** Five of
  twenty measured `auto --plan-only` goals produced no graph at all: the
  planner emitted a spec the validator refuses, the call was paid for, and the
  user got nothing — not even the rejected spec, which was discarded before it
  ever reached disk. A refusal the planner's own output caused now hands the
  validator's exact refusals back to **one** fresh planner call. That reply is
  UNTRUSTED exactly like the first: same `graph.Parse`, same
  `validatePlannedNodes`, no shortcut for "it already failed once". The engine
  never rewrites the model's JSON into something legal — the plan that runs is
  one the model authored and the ceiling accepted. Every planned-node refusal
  is handed back, not just the first, so a repair cannot fix one rule and trip
  the next with its single retry spent. The bound is exactly one extra call
  per plan (so a goal loop's worst case is `2 × --max-cycles`), and it is
  spent only on refusals the reply's content caused — a runner error, a
  non-zero planner exit, a reply with no JSON in it and a reply whose JSON
  does not decode buy nothing, because re-running cannot repair them.
  **What is measured, stated exactly:** the five-of-twenty figure above is the
  BASELINE that motivated this, not a before/after comparison. The goals run
  since are a different and much smaller set, and none of them drew a refusal —
  so the re-plan path has **zero real-planner samples**, no refusal rate is
  claimed for it, and whether it converges is answered only by
  `TestManual_RepairPromptConvergesOnARealPlanner` (manual, real `claude`).
  The `re-planned:` line is what makes that column measurable once a refusal
  does happen; comparing rates needs the same twenty goals re-run.
- **A re-plan is never silent, and a rejected plan is never destroyed.**
  `Plan.CostUSD` is the SUM of every planner call a plan took, so the ledger's
  TOTAL COST and the goal loop's `--max-goal-budget-usd` check cannot
  undercount a repaired plan; `--plan-only`'s cost sentence names how many
  calls it is talking about; and the plan printout carries a `re-planned:`
  line with the refusals that caused it, so a doubled price is visible and the
  refusal rate stays measurable. A plan refused twice now keeps its spec at
  `$OMG_HOME/plans/<id>/rejected.json` (owner-only, under its own name so
  nothing mistakes it for a graph that was going to run) and the error names
  the path and the total spend — hand-edit it and `oh-my-graph run` it.
- **The planner prompt states three rules it was enforcing in silence.** Every
  measured refusal above was a rule `graph.Validate` enforces and the prompt
  never mentioned. `{{ feedback.<id> }}` now says it takes NO filter — with
  the wrong form marked WRONG, the shape that worked for `**PASS**` — where
  the placeholder is legal, and that `<id>` names the node DECLARING the
  feedback block; the do-not-set list now includes `success_check.verify`,
  which the same prompt was actively steering planners toward; and `retry.on`'s
  closed cause set is listed, rendered from `graph`'s own cause constants so
  the advertised set cannot drift from the enforced one. Guidance to an
  untrusted producer, never a replacement for enforcement.
- **`auto --plan-only` — preview a plan before letting it spend (#108).** It
  mirrors `run --dry-run` through the same argv path: plan, print the
  topology, every agent and skill mapping decision and the tool ceiling
  exactly as a real `auto` would, then stop before any node runs. Unlike
  `--dry-run` it is **not free** — there is no plan to inspect until one has
  been bought, so it still pays for the planner calls, prints what they cost
  — the sum of every call, the validation-repair call included — and keeps the
  spec. The spec goes to `$OMG_HOME/plans/<id>/graph.json`, not
  `runs/`: nothing ran, so it is not a run and no reader of `runs/` needs a
  special case for it. Written owner-only (dir `0700`, file `0600`), because
  an inlined skill body in a node prompt is the user's own private
  instructions. Rejected at parse with `--max-cycles >= 2`, since every cycle
  after the first is planned from the previous cycle's run.
- **The skill scan says what it did not scan (#108).** An `auto` run now
  records its skill scan on the plan — the directories read, in precedence
  order, and the count of usable definitions — and prints it above the
  mapping decisions, followed by its limit: plugin skills
  (`~/.claude/plugins`) and project skills (`./.claude/skills`) are out of
  scope. `Found: 0` is now diagnosable instead of looking identical to "never
  scanned". A `name:` collision inside one `~/.claude/skills` names its loser
  rather than silently moving the count.

### Changed

- **The run lock is the kernel's `flock(2)`, not the lock file's existence
  (ADR 0015 §1).** `runstate.AcquireLock` opens `resume.lock`
  `O_CREATE|O_RDWR` and takes `LOCK_EX|LOCK_NB`; only once the lock is held
  does it truncate and write its two lines — a format marker
  (`oh-my-graph-lock 1`) and the holder's pid. The kernel releases the lock
  when the holder dies, however it dies, so a held lock now means a live leg
  rather than possibly a corpse: `LockHeldError` on that arm says "a leg of
  this run is in flight (started by pid N); wait for it, or stop it" and
  **drops the old "delete it and retry: rm …" advice**, which under `flock`
  is an active double-spend footgun (unlinking does not release the live
  holder's lock, while the next leg takes an uncontended one on a fresh
  inode). For the same reason **release no longer unlinks** — a `resume.lock`
  is now a permanent, inert resident of every run directory, and nothing may
  read its existence as a state. A lock file with no marker was written by a
  pre-`flock` binary whose live leg holds no lock at all, so it keeps the old
  semantics — existence is the lock, a human decides, and the message names
  the exact path to delete — an arm that self-expires the moment such a lock
  is cleared. New: `runstate.ProbeLock` answers `held` / `free` / `unknown`
  for a reader, via a **shared** (`LOCK_SH`) lock on a read-only fd that
  creates, writes and removes nothing, gated on the run directory being on a
  known-local filesystem (on linux, `flock()` over NFS silently degrades to
  per-process record locks). A missing file, a non-local filesystem and any
  error alike answer `unknown`, which means the answer this tool gave before —
  a false *dead* would authorise a second scheduler over a live run, so nothing
  is ever called dead because a probe failed. Off darwin and linux a
  build-tagged stub reports `unknown` and `AcquireLock` keeps the pre-ADR
  `O_EXCL` behaviour in full.
- **A lock file written before the marker existed is read under a second,
  weaker rule (ADR 0015 §1, dated note).** Its writer took no `flock`, so the
  probe reads the only signal such a file carries — its pid line — in **one
  direction only**: a pid naming no process at all (`kill(pid, 0)` → `ESRCH`)
  is `free`, while a pid naming something (holder or recycled stranger,
  indistinguishable) and an unreadable pid are both `unknown`. Without it every
  run abandoned *before* this release would read `RUNNING` for the rest of
  time, since the marker will never appear on a file already on disk. It does
  not reopen the pid recycling the ADR refutes — that produced a false *alive*,
  and pid-alive is never read as evidence here — and the acquire path is
  unchanged, so an unmarked lock is still refused with a human deciding. The
  arm self-expires once such a lock is cleared, and docs/RUN-FEED.md's
  "Liveness" section states it for external consumers, who may skip it and
  treat every unmarked file as `unknown`.
- **A run whose process died now reads `ABANDONED` instead of `RUNNING`
  forever (ADR 0015 §2, §4).** The rule — *an open leg AND a held lock is in
  flight; an open leg AND an affirmatively free lock is abandoned; every doubt
  is in flight* — is stated once in the new `internal/runstatus` and shared by
  every surface that asks, so they cannot drift: `runs list` gains the verdict word
  `ABANDONED` beside `RUNNING` (deliberately not `FAIL` — the work never got a
  verdict) and its snapshot-less row widened from "in flight" to "in flight or
  abandoned", so a run killed before its first node settled is *labelled*
  rather than vanishing behind a `WARNING`; the dashboard card gains an
  `abandoned` state with its own muted token, stops spinning the nodes the dead
  leg left open (they tally as pending) and carries the recovery hint;
  `serve`'s `ResolveRun` no longer parks a live view on a corpse; and `watch`
  refuses to tail a stream that will never get another line. Nothing in either
  versioned file changed — no reader ever repairs the feed — but `resume.lock`
  leaves the internal set and gains a documented "Liveness" section in
  docs/RUN-FEED.md. `resume` on such a run warns first: the engine spawns each
  `claude` in its own process group, so the death that abandoned the run may
  have left a subprocess still spending, and that warning (on the row, in
  `watch`'s refusal, on `resume`'s stderr and on the card, whose gate button is
  one money-spending click) is the mitigation — ADR 0015 rejects probing for
  the orphan. A run that never wrote a snapshot has nothing to resume from, so
  its hint says "run the graph again" and `resume` fails on it with that
  sentence instead of a bare "no such file".
- **A passing node's spend now reads against its budget (#120, closes #115).** The
  `COST(USD)` column annotates each row with the share of `budget_usd` that
  spend used — `0.4900 (98%)` — so "one bad run from failing" is visible before
  the run that fails, not only in the FAIL detail afterward. Floored, never
  rounded, since a node that spent under its budget must not read 100% — a node
  that landed exactly on its budget passes and does read 100%. The share rides
  inside the cost cell rather than in a column of its own, and the whole field
  is a per-**run** decision: a graph where no node declares `budget_usd` pays
  nothing for the feature. To keep a budgeted run's table on an 80-column
  terminal — the shipped graphs that declare a budget are mixed runs, not
  budget-less ones — the `SESSION` stub narrowed from 20 characters to 18.
- **`lint` says when a fan-in reviewer's arc cannot reach a producer (#120,
  partially addressing #118).**
  A reviewer that fans in from several producers still names one node in
  `rerun`, so its loop body can exclude a producer whose artifact it judges;
  the loop then re-judges an unchanged file every round and halts with the
  defect untouched (~$14 of a $42 run). `lint` and `run --dry-run` now warn per
  excluded producer, naming the reviewer, the rerun target, the producer, and —
  when one exists and still validates — the covering target to aim at instead.
  It is **advisory only**: it never changes whether a graph is valid or what
  any command exits with. Parents that are gates, or that are upstream of the
  rerun target (the `spec → impl → review` shape), are not reported.

- **A negative `retry.max` is refused at load instead of discarded.** The
  scheduler adds `retry.max` to a node's attempt count only when it is
  positive, so `max: -1` was silently dropped and the node ran once — the exact
  quiet non-retry that `retry.on`'s closed cause set already exists to prevent,
  from a value no author can have meant. `Validate` now rejects it, naming the
  node and the bound (`validateRetryCauses` is renamed `validateRetry` to say
  it judges more than the causes). `max: 0` is untouched: it IS the
  extra-attempt count a node declaring no retry already has. **Behaviour
  change** — a graph carrying a negative `max` loads and runs today and stops
  loading now, and the refusal is retroactive the same way
  `permission_mode`'s is: `resume` and `run --retry-failed` re-parse the run
  snapshot, so an in-flight run authored with a negative bound becomes
  unresumable. No shipped graph, fragment or testdata graph uses one.

### Fixed

- **A node left running by a leg that died spun forever in `serve` (ADR 0015).**
  All THREE of `serve`'s stream reducers — the dashboard card's,
  `/api/transcript`'s, and the single-run page's own `apply()` in
  `ui/app.js` — switched only on node events, so they never
  saw `run_started` — and a node whose leg crashed mid-run has a `node_started`
  with no terminal after it. `/api/transcript` therefore kept serving that dead
  leg's session transcript as "what it is doing right now", and the dashboard
  card and the run page kept the dot spinning, across every later resume that
  did not happen to re-run that node. All three now treat **every `run_started`
  as a leg boundary**: a node the previous leg left open stops being running (and
  stops carrying its session id) the moment a new leg opens, and the new leg's own
  `node_started` is what makes it running again.
- **The single-run live view — the page the gate button is on — claimed a dead
  run was still running (ADR 0015 §4).** The ADR named four surfaces and missed
  the fifth: the dashboard card said `abandoned` and carried the recovery hint,
  while the page that card links to said `running`, spun its nodes, tailed the
  dead leg's transcript as "now doing", and offered the gate's approve button
  with nothing said — the exact inversion of the ADR's own rule that a button
  must say what it will allow *before* it is clicked. `/api/graph` now carries
  the answer as two additive keys, `abandoned` and `hint`, composed through
  `internal/runstatus` like every other surface and absent on every other run
  (the page cannot derive it — the answer needs the lock, and probing is
  server-side). The header says `ABANDONED`, the nodes stop spinning and drop
  their live tails, and the hint sits above the feed the button lives in. Still
  no new event type, field or verdict, and still no poll: the page re-asks on
  every leg boundary, so a run that dies while the page is already open keeps
  painting until then — `watch`'s accepted gap, for the same reason.
- **A typo'd `result_matches` cost a node before it was diagnosed.**
  `success_check.result_matches` had no load-time validation: its first and
  only compile happened inside the scheduler's success-check evaluation, which
  runs *after* the node has been spawned and paid for. Its sibling
  `verify.output_matches` has been rejected at load, naming the node, since the
  field existed. `Validate` now compiles both, so an uncompilable verdict
  pattern is a `GraphValidationError` that `lint` and `run --dry-run` refuse
  the graph on — quoting the pattern and wrapping `regexp`'s own message — and
  no node is spawned. Every shipped graph, fragment and testdata graph already
  passes; the pattern the repo ships (`` ^[*_`\s]*PASS[*_`\s]*$ ``, also the
  planner's) is pinned by name in the test table.
- **`output_matches` was judged against a truncated tail.** The verify seam
  handed the scheduler only the last 4 KiB of a command's output, prefixed with
  `…(earlier output truncated)…` — so an anchored pattern could **never** match
  a chatty command, and a `go test ./...` whose `ok  github…` summary scrolled
  past the cap failed a check its own evidence had passed. `verify.Result.Output`
  is now the FULL combined stdout+stderr; truncation moved to where output is
  rendered (the ledger's DETAIL column, already capped) and to `*TimeoutError`,
  which can outlive the call. No extra memory: `CombinedOutput` had already
  buffered the whole thing before any cut could apply. **Behaviour change** — a
  check that fails today only because its match sat beyond the cap starts
  passing. The reverse needs a pattern that matched something the cut
  manufactured — the `…(earlier output truncated)…` marker itself, or a `(?m)^`
  anchor that the marker's inserted newline created a line start for. Absent
  that, a longer subject can only add matches: these are unanchored RE2
  searches, with no lookbehind or backreference that could make more input
  remove one.
- **`permission_mode` was the one enum with no validation.** `type`, `handoff`,
  `on_fail` and `retry.on` are each rejected at load against a closed set;
  `permission_mode` was passed through to argv unchecked, so `dontask` (wrong
  case) killed the node at spawn — mid-run, after earlier nodes had already
  spent real money, and a long way from the typo. It is now validated at load
  like the other four, against the `claude` CLI's own choices measured from
  `claude --help` (2.1.221): `acceptEdits`, `auto`, `bypassPermissions`,
  `dontAsk`, `manual`, `plan`. DESIGN.md listed three of those six. **Behaviour
  change** — a graph carrying a mode outside that set is now refused by `run`
  and `lint` instead of failing one node; a mode a future `claude` adds is
  refused until oh-my-graph enumerates it. That refusal is **retroactive**:
  `resume`, `run --retry-failed`, `runs list` and `serve` rebuild the graph from
  the run snapshot through the same parser, so a run recorded by an earlier
  binary under a mode this one does not know stops being resumable, is skipped
  with a warning by `runs list`, and shows as a broken card in `serve`.
- **A verdict pattern must survive the markdown a model writes (#107).** A
  node that got exit 0 and opened its reply with `**PASS**` failed
  `result_matches: "^PASS"` and halted the run. Every shipped pattern now
  tolerates leading emphasis, whitespace and a code span (`` [*_`\s] ``) while
  **keeping** its anchor — relaxing the anchor would let a FAIL report that
  merely names the word pass. The planner prompt that *generates* graphs
  carries the same tolerant pattern, which matters more there: a planned node
  may not set `success_check.verify`, so `result_matches` is the whole gate.
- **A node that asks for a verdict must check that it got one (#110).**
  `merge-shepherd`'s `merge` node declined to merge past an unfinished
  re-review, ended its turn on "I'll proceed as soon as it lands", exited 0
  under `exit_zero: true`, and passed — the ledger recorded a successful merge
  step and nothing was merged. Swept the class: every node whose prompt names
  the answer it must give now carries an anchored verdict token whose payload
  only the finished work can produce (`MERGED <sha>` | `WITHHELD <reason>`,
  `TRIAGED <count>`, `PR <url>`, `ADR <path>.md`, `DONE`, `CLEAN`). `lint` and
  `--dry-run` now warn (`LintVerdicts`) on a prompt that demands a token with
  no `result_matches` to read it, and on a `result_matches` that dropped its
  exit-zero guard.

### Security

- **`serve` refuses to be framed, so the gate button cannot be clickjacked.**
  Every guard on the gate routes asks where a request came from — the loopback
  bind, the `Host` check, the `Origin` check, the constant-time token compare —
  and a clickjack answers all four honestly, because it never leaves this
  origin. A hostile page frames `http://127.0.0.1:8642/run/<id>/` (or the
  dashboard's `/`, which needs no run id at all), overlays it, and baits a
  click onto approve: the click lands inside the framed page, which reads its
  own token and is stamped with this server's own `Origin`, and a gate nobody
  read starts a leg that spends money. Both front-ends now send
  `frame-ancestors 'none'` + `X-Frame-Options: DENY`, plus `nosniff` and a full
  CSP written against what the shipped `ui/` assets actually do (`style-src`
  carries `'unsafe-inline'` for the `<style>` cytoscape injects at renderer
  init; `script-src` stays `'self'` with no `'unsafe-eval'`). Verified in a real
  headless Chrome: both pages render with zero violations, and a cross-origin
  framer is refused — against a deliberately unfixed build on a second port the
  same page framed the view successfully.
- **Run directories and their contents are written owner-only (`0700`/`0600`).**
  A run directory is the run's whole memory — every node's prompt and inputs
  (`state.json`), every node's full reply (`<node-id>.out`), feedback payloads,
  the event stream — and it was world-readable at `0755`/`0644` while `auto`'s
  saved plan spec had already been narrowed to `0700`/`0600` for the same
  reason. Existing run directories are **not** re-moded (`MkdirAll` does not
  chmod one), so `resume` and `serve` keep reading runs from older binaries
  unchanged; narrow them yourself with `chmod -R go-rwx ~/.oh-my-graph/runs` if
  you want to. What `oh-my-graph init` scaffolds into your own project is
  deliberately untouched at `0644`. **This narrows the at-rest exposure only —
  it does not close it:** a node's full prompt is still passed in argv, where
  `ps auxww` exposes it for the node's lifetime, and it also lands in the CLI's
  own session transcript under `~/.claude/projects`.
- **The argv exposure is documented rather than claimed closed.** SECURITY.md
  gains "What is exposed at rest" and "What is exposed while a node runs".
  Because `| inline` is used pervasively, a prompt in argv carries the content
  of upstream artifacts, not just its own text. Moving the prompt to stdin was
  considered and deliberately not done: it touches the seam with the most
  careful lifecycle handling in the repo (`waitDelay`, process-group kill,
  `--output-format json` parsing), adds a deadlock surface, has an unmeasured
  interaction with `--resume`, and would need a `make smoke` measurement against
  a live CLI before anyone should believe it.
- **The exec-seam invariant walks the whole repository instead of three named
  directories.** `internal/invariants` enumerated `internal`, `cmd` and `graphs`,
  so a new top-level Go package importing `os/exec` would have been invisible to
  the guard that enforces this project's core security property — and a guard
  that scans nothing fails silent-green. It is now a repo-root walk with a
  skip-list (dot/underscore directories, `testdata`, `vendor`, `bin`), which
  changes zero results today (every directory holding `.go` files is already
  under those three) and is self-verifying: a skip-list that broke the walk
  makes all eight allowlisted files report stale and the test go red. A new
  companion test rejects `os.StartProcess`/`syscall.ForkExec` and friends by AST
  selector — the spawn route an import allowlist cannot see — without an import
  check on `syscall`, which eight non-spawning files here import for signals,
  flock, filesystem-type and pid probing.

### Documentation

- **A spawner count written in prose now answers to the allowlist (#112).**
  "Exactly four objects may spawn a process" is hand-written in more than thirty
  sentences across package docs, README, DESIGN, CONTRIBUTING and a shipped
  graph prompt, and only the allowlist in `exec_seam_test.go` was checked by
  anything — so the prose drifted: copies still said "two" and "three" long
  after the seam set had grown, including in `internal/childenv`'s own package
  doc (the package that *owns* the rule) and in `graphs/self-dev.yaml`, which
  told a coding agent "the `NodeRunner` seam is the only `os/exec` touch point"
  — which is how a fifth spawner gets added by someone sincerely reporting it
  touched nothing. `TestProseSpawnerCountsMatchTheExecSeamAllowlist` now reads
  the number back out of the prose and holds it to the count **derived** from
  `allowedExecImporters`, with deliberately no second copy of the number (a
  guard carrying its own constant would be the same bug again). ADRs and this
  changelog are excluded as history — every "exactly two" in ADR 0002 was true
  when it was written. Alongside it, `errors.As` now reaches a fragment error:
  `UnresolvedFragmentError` embedded `GraphValidationError` by value and defined
  no `Unwrap`, so a caller doing exactly what `GraphValidationError`'s doc told
  it to do silently missed fragment errors.
- **The docs say what the code does, starting with the published contracts
  (#111, #113).** CONTRIBUTING's merge-review checklist listed three spawners
  and three call-site tests, contradicting its own table four lines up — a
  reviewer following it would have approved a browser-seam PR that dropped the
  env scrub. The run-feed contract told consumers a currently-running node is
  absent from `state.json`'s `nodes`, which a feedback loop falsifies: while
  `impl` runs round 2 the snapshot still reads its round-1 `PASS`, so a consumer
  using absence as the liveness test reads a running node as settled. RUN-FEED.md
  now names what to key on instead — a `node_started`/`node_retried` with no
  terminal event after it, in `events.jsonl`, never in the snapshot — and gains
  the `judged` field and the verdict-qualifier table. The CLI usage synopsis
  advertised a `serve --run` flag that does not exist and omitted `version`,
  `--no-web`, `--no-agent-mapping` and `--no-skill-mapping`; it is now one
  constant, and `usage_test.go` derives its checks from `run()`'s dispatch
  switch and each subcommand's real `FlagSet` in **both** directions, so an
  advertised-but-unregistered flag and a registered-but-undocumented one each
  fail. DESIGN.md absorbs a dozen smaller drifts (the `NodeInvocation` /
  `NodeOutcome` field lists, the argv paragraph, the planned-node disposition
  table's missing `use`/`with`, `permission_mode`'s full measured set).
- **ADR 0015 — an abandoned run is derived from the lock, not repaired into
  the feed (#109).** Two runs read RUNNING for over a day, because
  `runfeed.InFlight` calls a leg open when its last `run_started` has no
  `run_finished`, and a killed process never writes one. The pid in
  `resume.lock` cannot fix it — measured: a dead run's lock pid read ALIVE
  because an unrelated process had recycled it. Liveness becomes the kernel's
  `flock(2)` on `resume.lock`, ABANDONED is derived at read time by every
  reader, and no reader appends a terminal event on a dead run's behalf. Both
  file schemas stay 2 and neither file changes. Accepted as an ADR and since
  **implemented in full** — the lock (§1), the read-time derivation (§2) and
  every surface that renders it (§4) are all in Changed and Fixed above, and the
  two gaps the ADR accepts by design remain gaps: `watch` gains no idle-time
  probe, and nothing probes for an orphaned `claude`.
- **Two ADRs landed in parallel and both took the number 0016.**
  `0016-build-evidence-is-a-user-supplied-engine-command.md` (#119/#123) and
  `0016-a-retry-carries-the-attempt-it-is-repeating.md` (#124) were authored on
  separate lanes and merged without either seeing the other's number. Both are
  cited by that number throughout the code, the READMEs, DESIGN.md and this
  entry, so the collision is recorded here rather than repaired in the release
  commit: renumbering is its own change, and a link that resolves today should
  not be broken by a version bump. Cite ADR 0016 by **filename**, not by number,
  until one of them is renumbered. *(Resolved 2026-08-09, in its own change:
  the retry record became
  `0020-a-retry-carries-the-attempt-it-is-repeating.md` and the build-evidence
  record kept 0016. Released entries are history and keep the numbers they were
  written with, so an "ADR 0016" elsewhere in this file still means whichever
  record it meant at the time — in this release's own entries, the
  build-evidence one everywhere except the failed-reply/retry bullet above. The
  tree itself was swept; see the Unreleased entry.)*

## [v0.4.1] - 2026-08-04

The composition release. Graph authoring stops restating itself: a `use:`
splices a proven node shape in at load time (ADR-0013), three shipped fragments
carry the cold-safe e2e gate and the two review shapes, and the three templates
that had quietly drifted apart bind them instead of each keeping its own copy.
`serve` becomes one port for every run — `/` is a live dashboard of mini-DAG
cards and each run's own view is mounted at `/run/<id>/` — and it opens the
browser itself over the exec seam that already existed. The docs drop the
fleetops pairing for the durable fact underneath it. And the assessor's data
fences get a per-call nonce, so an artifact carrying a forged `---` can no
longer close its own block and address the judge from apparent outside it.

### Added

- **Fragments — a `use:` is a load-time node splice (ADR-0013).** A node may
  cite a single-node fragment file with `use: <name>` plus `with:` bindings,
  and the file loader splices it in *before* validation, so a resolved graph
  is indistinguishable from a hand-written one to every engine consumer and
  the runtime never learns the concept. Lookup is one location and no search
  path — the entry file's own `fragments/` sibling, with `use:` constrained to
  a bare name so `../evil` cannot reach out of it — overrides are judged by
  key presence and replace the whole top-level subtree (never a deep merge),
  a `{{ with.x }}` binding is typed when it is the entire scalar and textual
  when embedded, and an alias-carrying fragment body is refused rather than
  half-walked. `run` prints one disclosure line per resolved `use:` naming the
  source, its `description:` and every key the node overrides; a fragment run
  snapshots the *resolved* graph so resume survives; `lint` and `--dry-run`
  judge prompts after splicing; and any node still carrying `use:`/`with:` at
  `Parse` is refused structurally — which is also how a planner-emitted
  fragment is rejected. Three proven shapes ship under `graphs/fragments/`
  (`e2e-verify`, `review-security`, `review-style`), embedded in the binary
  and unpacked by `init` alongside the templates that cite
  them. ([#98](https://github.com/jitokim/oh-my-graph/pull/98))
- **One port for every run, and `serve` opens the browser itself.**
  `oh-my-graph serve` with no run id is now a dashboard: `/` renders one live
  mini-DAG card per run — in-flight first with state, elapsed, cost and node
  counts, settled runs collapsed below — and each run's own view is mounted at
  `/run/<id>/`, the existing single-run route set *mounted* rather than
  re-rooted, so every document-relative fetch and static asset is scoped with
  zero UI changes. `/api/cards/events` subscribes to the runs root the way a
  run view subscribes to its `events.jsonl`, re-sending only runs whose
  contract files changed; cards are derived through the existing readers only
  (`runfeed.InFlight`/`Walk`, `runstate.Load`, `graph.Parse`), so a card cannot
  disagree with `runs list` or `watch`, and a run directory this binary cannot
  read becomes an `unknown` card carrying the reason rather than being silently
  dropped. The run id from the URL is matched against the runs root's directory
  listing before any path is built from it, and the loopback bind, Host check
  and gate token are unchanged and cover the mounted runs. `serve` also stops
  merely printing a URL: when stdout is a terminal and `--no-open` was not
  passed it hands that URL to `browser.ExecOpener` over the fourth exec seam —
  the same gate the embedded live view takes, no new spawner — while a pipe, a
  script or CI still binds the port and serves, with byte-identical output and
  no browser. ([#100](https://github.com/jitokim/oh-my-graph/pull/100))

### Changed

- **Three shipped templates migrate onto the fragments.** `self-dev`,
  `dev-review-pr` and `backlog-batch` stop restating the cold-safe e2e gate and
  the two review shapes and bind them instead — the hand-swept drift that
  motivated ADR-0013 in the first place. The migration is gated twice: frozen
  `testdata/pre-migration/` fixtures prove `self-dev`'s and `dev-review-pr`'s
  resolved graphs are byte-identical to their pre-migration parse outside the
  deliberately converged fields (the prompt wording; the e2e grant reshaped into
  the fragment's narrowed check-gate one — `self-dev`'s `Bash(go *)` and both
  templates' `Bash(git *)` narrow to the three `go` verbs and the three
  read-only `git` ones, while `dev-review-pr` picks up `go build`/`go vet`;
  `dev-review-pr`'s two reviews gaining `Bash(git log*)`; `self-dev`'s e2e
  gaining the runaway-insurance `budget_usd`), and `testdata/golden/` captures
  all three post-migration resolved graphs so a future fragment edit surfaces as
  a three-template resolved diff in the PR. `backlog-batch` is the one with no
  equivalence fixture, because it does not claim equivalence: its gates gain a
  real engine-run
  `success_check.verify` — fed by the new required `checks_command` input, since
  the evidence command is what only the graph can know — where the node's own
  "PASS" was previously the only thing judging
  it. ([#98](https://github.com/jitokim/oh-my-graph/pull/98))
- **`serve` with no run id no longer resolves a run.** It used to pick the
  newest in-flight run (or just the newest) and fail with "no runs found" on a
  machine that had never run
  anything; it now renders the dashboard, empty at first, which fills in the
  moment something runs. `serve <run-id>` is
  unchanged. ([#100](https://github.com/jitokim/oh-my-graph/pull/100))

### Security

- **The assessor's data fences carry a per-call nonce.** `assessMaterial()`
  wrapped node details, artifact excerpts and the previous cycle's `remaining`
  — all of it raw model output — in fixed `---` markers, which the fenced text
  can predict and therefore emit: a prompt-injected artifact could close its
  own block and address the assessor from apparent outside the fence, forging a
  `goal_met: true` over work never done, or a `remaining` that was threaded
  into the next cycle's planning with no fence at all. The mechanism
  `skillmap.go` already used for inlined `SKILL.md` bodies is extracted into
  `fence.go` rather than a second one being invented: one `crypto/rand` nonce
  per `Assess` call, rendered into *both* markers of all three fence kinds,
  with the assess prompt stating the trust rule outright — a `---` line is a
  fence only when it carries that token. The planner's continuation quote of
  `remaining` gets its own per-call fence. Tests assert the property, not the
  marker text: hostile material carrying every marker the engine emits, built
  from a real rendering with only the nonce wrong, must not raise the count of
  nonce-bearing markers in the output. No exec seam, tool policy or call class
  changed. ([#105](https://github.com/jitokim/oh-my-graph/pull/105))
- **A gate decision whose `Origin` is not this view's own is refused.**
  Defence in depth on a guard that already held, and the record says so
  plainly: `requireGateToken` already answered an absent token 400 and a wrong
  one 403, both returning before the resumer is consulted, so no tokenless gate
  POST has ever started a leg — the absent-token status is left exactly as it
  was. What `requireSameOrigin` adds is provenance: a browser stamps `Origin`
  on every cross-origin POST, so a decision issued by a page this process did
  not serve is visible before the token is weighed at all, and independently of
  that token staying secret. It runs first in `handleGateDecision`, on every
  mutating route; an *absent* `Origin` is still allowed through deliberately,
  because curl and the CLI's own tests send none and for them the token remains
  the whole guard. The check narrows what a browser can do and widens nothing.
  ([#105](https://github.com/jitokim/oh-my-graph/pull/105))

### Documentation

- **The fleetops framing retires from the docs a user reads first.** Now that
  the tool ships its own observation surface (the `serve` live view, `runs
  list`/`show`/`watch`), the sibling-project pairing stops carrying its weight:
  both READMEs drop the "Pairs with fleetops" section and the masthead pairing
  line for the durable fact underneath it — nodes run with session persistence
  on, so every node is an ordinary claude session under `~/.claude/projects`
  that any transcript reader can pick up — DESIGN.md states the visibility
  stance as the run-feed contract itself (rendering gets no privileged access to
  the engine), `docs/RUN-FEED.md` generalizes from "canonically fleetops" to any
  external consumer, EXAMPLES.md swaps "Observe with fleetops" for "Watch a
  run", and LIMITATIONS.md's deferred item becomes a terminal TUI rather than a
  dashboard. fleetops survives as a one-line "See also"; ADRs and this changelog
  are left as history. ([#101](https://github.com/jitokim/oh-my-graph/pull/101))

## [v0.4.0] - 2026-08-04

The iteration release. The engine grows loops — `feedback` edges that re-run a
DAG path on a judgment failure (ADR-0010) and a plan → execute → assess goal
cycle for `auto` (ADR-0011) — and learns to pause-and-resume instead of
hard-failing: a subscription session limit becomes a pause, and
`resume --retry-failed` salvages a halted run (ADR-0009). Planned nodes map
onto your own Claude Code agents and skills, the live view gains gate approval
and a running-node transcript, and the tool becomes something you install
rather than build — prebuilt binaries and an `init` that writes the example
graphs. Meanwhile the project starts shipping itself, and says so.

### Added

- **`timeout:` per node, and a live transcript of the running node
  (ADR-0007).** A node may declare an execution `timeout:`, parsed and
  validated at load time — unparsable, zero, and negative values are all
  rejected, and undeclared nodes keep the runner's 20-minute default, so no
  path runs unbounded. The engine now pre-assigns each invocation a v4
  `--session-id` (`crypto/rand`, mutually exclusive with `--resume`) and
  publishes it on `node_started`/`node_retried`, and `serve` gains an
  `/api/transcript` tail so the live view shows a "now doing" line on the
  running node. `budget_turns` is documented as rejected-with-evidence rather
  than shipped as a dead field: `claude` exposes no `--max-turns` (verified
  against `claude --help`, 2026-08-02). ([#82](https://github.com/jitokim/oh-my-graph/pull/82))
- **`on_fail` policy, `resume --retry-failed`, and session limits as
  resumable pauses (ADR-0009).** Three failure-semantics promotions drawn
  from real 20-node batch incidents: a graph may declare `on_fail: continue`
  itself (ORed with `--continue-on-fail`, so the CLI can never *weaken* a
  graph's declared policy); `resume <run-id> --retry-failed` salvages a
  failed run by re-executing only its failed and cancelled nodes (an initial
  `state.json` is written at t=0 so even a first-node failure is resumable);
  and hitting the subscription session limit now *pauses* the run instead of
  failing it — in-flight siblings drain to completion, the limited node is
  recorded nowhere, the CLI exits 2 with a reset-time resume hint, and
  `resume --retry-failed` picks it back up. ([#83](https://github.com/jitokim/oh-my-graph/pull/83))
- **Feedback edges — bounded runtime re-runs of a DAG path (ADR-0010).** A
  node may declare a `feedback:` arc back at a `depends_on` ancestor; when it
  fails for a *judgment* cause (verify failed, nonzero exit, result mismatch
  — never an infrastructure fault or a blown budget), the scheduler re-arms
  the loop body and re-runs it up to a load-time-mandatory `max` rounds,
  exposing the declarer's output to the re-run through a new
  `{{ feedback.<id> }}` handoff namespace. All seven structural rules
  (mandatory positive `max`; the arc must target a proper ancestor, so
  self-loops, forward arcs and cross-branch arcs are rejected) are enforced
  before any spend; the loop is pure file I/O in the run directory and adds
  no spawner, and `graphs/review-loop.yaml` ships as a worked
  demo. ([#86](https://github.com/jitokim/oh-my-graph/pull/86))
- **Plan-and-execute goal iteration (ADR-0011).** `auto` gains a bounded
  plan → validate → execute → assess cycle loop: `assess` lands as a third
  coordinator call class with its own stricter, tool-less isolated stance,
  and `--max-cycles` (default 1) plus an optional `--max-goal-budget-usd`
  soft check at cycle boundaries govern it. Each cycle persists as a normal
  run with its own `assess.json`, the snapshot gains an optional additive
  `goal` lineage block that survives resume legs, and a single-cycle run
  stays byte-identical to before. ([#91](https://github.com/jitokim/oh-my-graph/pull/91))
- **Auto-map planned nodes onto your own Claude Code agents.** When the
  coordinator plans a graph, each planned node is matched by token-based name
  against the agents in `~/.claude/agents/` and `.claude/agents/`, and maps
  only when the agent's frontmatter `tools` are a subset of the node's
  allowed tools (itself under the fixed planner allowlist). Mapping runs
  strictly *after* `validatePlannedNodes`, so a planner-emitted `agent:`
  field stays rejected — candidates come only from your own agent files;
  every mapping and skip is disclosed in the plan printout, and
  `--no-agent-mapping` opts out. ([#81](https://github.com/jitokim/oh-my-graph/pull/81))
- **Skill mapping for planned nodes — plan-time inlining (ADR-0012).** The
  coordinator scans `~/.claude/skills/*/SKILL.md`, matches skill names
  against planned node IDs (the same rule as agent mapping), and on an
  unambiguous hit inlines the skill body into the node's prompt inside a
  nonce-fenced block — with `{{`-neutralization, a SHA-256 disclosure, and a
  16 KiB post-neutralization cap (skip, never truncate). Inlining rather than
  referencing is grounded in a measured probe: a planned node that merely
  *names* a skill is dead text, because the spawned `claude -p` never loads
  it. Agent-mapped nodes are refused (the composite is unmeasured),
  ambiguity maps nothing, and `--no-skill-mapping` disables the feature
  before any filesystem access, in both `auto` and
  `chat`. ([#97](https://github.com/jitokim/oh-my-graph/pull/97))
- **Gate approve/reject from the live view (token-guarded).** `serve` gains
  `POST /api/gate/{approve,reject}`, valid only while the viewed run is paused
  at a gate and reusing the CLI resume machinery, so a human can release a
  gate from the browser instead of the terminal. A per-process random token
  embedded in the page guards it as a CSRF check on top of the loopback bind
  and Host guard; the read-only-boundary change is documented in the handler
  and DESIGN.md, and the embedded (non-standalone) view still refuses gate
  decisions. ([#94](https://github.com/jitokim/oh-my-graph/pull/94))
- **`resume` gets the web live view.** A resumed leg now embeds the live view
  on the same terms a first leg does — `--no-web` on `resume` and TTY-gated
  auto-open over the fourth exec seam — closing a note ADR-0006 had deferred.
  An interactive resume serves the run on an ephemeral loopback port and opens
  it once (a pipe, CI, or `--no-web` yields nothing and byte-identical
  output), and the view shows the whole run's history, not just this
  leg. ([#93](https://github.com/jitokim/oh-my-graph/pull/93))
- **Resizable live view and a full-window layout.** The `serve` feed/map split
  gets a draggable handle (pointer-capture, minimum widths, chosen width
  persisted in `localStorage`), and maximizing the window now grows the map
  instead of empty margin — the map defaults to a viewport-proportional width
  and a debounced window-resize listener re-fits the cytoscape canvas that
  previously never re-measured. ([#79](https://github.com/jitokim/oh-my-graph/pull/79))
- **Prebuilt release binaries via goreleaser.** A `version: 2`
  `.goreleaser.yaml` builds `darwin`/`linux` × `arm64`/`amd64` with
  `CGO_ENABLED=0` and `-X main.Version={{.Version}}`, produces per-platform
  `tar.gz` archives (bundling README/LICENSE/CHANGELOG) plus `checksums.txt`,
  and a tag-triggered `release.yml` workflow publishes them — so users no
  longer have to `go install` from source. `Version` moves from `const` to
  `var` so the ldflags injection lands (the version↔changelog test still
  passes). ([#95](https://github.com/jitokim/oh-my-graph/pull/95))
- **`oh-my-graph init [dir]` writes the example graphs.** `go install` ships
  only the binary, so the README's first `run` command failed
  file-not-found on a fresh machine; the shipped graphs are now `go:embed`-ed
  and `init` writes them out — refusing to overwrite, listing what it wrote,
  and printing the next command. Quickstart becomes install → init →
  run. ([#96](https://github.com/jitokim/oh-my-graph/pull/96))
- **Two more shipped templates, plus batch idioms.**
  `graphs/adr-driven-dev.yaml` encodes the maintainer's own methodology — an
  eleven-node design-first ADR → implement → tests-green → three-round-review
  pipeline, with four engine-run `verify: make local` gates and a
  user-replaceable deep-review-agent slot
  ([#84](https://github.com/jitokim/oh-my-graph/pull/84));
  `graphs/merge-shepherd.yaml` turns the operator's by-hand PR-shipping loop
  (verify → ready-and-wait → triage → gate → merge) into a graph, where the
  merge node sits behind both a READY check and a human `type: gate` so it can
  only run once review has completed
  ([#88](https://github.com/jitokim/oh-my-graph/pull/88)); and
  `graphs/apply-flags.yaml` (a reusable apply-review-flags lane) and
  `graphs/backlog-batch.yaml` (the multi-lane batch skeleton) ship alongside
  them. ([#80](https://github.com/jitokim/oh-my-graph/pull/80))

### Changed

- **Shipped templates and the planner absorb the budget-policy lessons.**
  Across `dev-review-pr`, `self-dev`, `backlog-batch` and `apply-flags`, dev
  nodes default to no `budget_usd` and any remaining budget is a catastrophic
  runaway ceiling rather than a per-step limit (e.g. `dev-review-pr`'s e2e
  relaxed 0.50 → 10.00, not removed), commit-strategy guidance is upgraded,
  and 1-hour hang-guard timeouts are added; the planner prompt gains
  decomposition-by-responsibility and the same budget posture — a prompt-only
  change that keeps `budget_usd` do-not-set and widens no field
  disposition. ([#84](https://github.com/jitokim/oh-my-graph/pull/84))

### Fixed

- **Three dogfood-incident engine fixes.** Worktree `Acquire` is now
  disk-aware, so a resume leg survives a retained branch: it validates and
  reuses an existing managed dir, attaches to the run's branch without `-b`
  when only the branch survived, creates fresh otherwise, and refuses a
  foreign directory rather than adopting it. A per-node `timeout:` expiry is
  reported as `timed out after <dur> (node timeout)` instead of the raw
  `context deadline exceeded`, still wrapping `context.DeadlineExceeded` so
  scheduler classification is unchanged. And a node killed before it printed
  its cost envelope now reads `cost unknown (killed before reporting)` instead
  of an implied `$0.0000`. ([#90](https://github.com/jitokim/oh-my-graph/pull/90))
- **Placeholder lint and template-prompt polish.** The placeholder lint's
  leading-word gate lowercases before matching, so `{{ Inputs.repo }}`-style
  case variants get an advisory "did you mean lowercase?" warning (the runtime
  pattern is untouched); retried session-handoff prompts in
  `dev-review-pr`/`self-dev` read correctly on a cold resume; and the last
  wall-clock wait in `runfeed`'s reader test is replaced with an observed
  `fs.ErrNotExist` signal. ([#80](https://github.com/jitokim/oh-my-graph/pull/80))
- **The exec-seam invariant test now asserts the call sites actually scrub.**
  `TestExecSeamCallSitesScrubEnv` parses each of the four spawn-site files and
  fails CI unless the file has exactly one `exec.Command`/`exec.CommandContext`
  call and the function enclosing it assigns `cmd.Env = childenv.Scrub(...)` on
  the *same* `*exec.Cmd` receiver that constructor produced, before that
  receiver is run (`Run`/`Start`/`Output`/`CombinedOutput`) or returned —
  closing a defense-in-depth gap where the import allowlist guarded *which
  files* may spawn a process but never that they *actually scrub* the child
  environment, so a future second, unscrubbed spawn in an already-allowlisted
  file would have stayed green. Pinning the receiver and the order also rejects
  a scrub written onto an unrelated variable or placed after the process
  already ran. Not exploitable today (every call site already scrubs); found in
  this release's rotating security meta-review. ([#102](https://github.com/jitokim/oh-my-graph/pull/102))

### Documentation

- **README restructured around the gap it fills.** The front page now opens
  with what the tool is and the gap it fills that a shell script, a CI
  pipeline, or one long agent session does not — instead of leading with the
  `Marginal cost per node: $0` punchline and self-hosting trivia. Four H2s
  fold into the flow they belong to, the "exactly four exec seams" invariant
  is stated up front, and the subscription-auth story moves into a *Bring your
  own login* section. Reordering, not rewriting: all seven code blocks stay
  byte-identical. ([#99](https://github.com/jitokim/oh-my-graph/pull/99))
- **A Korean README (`README.ko.md`) with a language switcher.** A full
  Korean translation ships with an `English | 한국어` switcher at the top of
  both files, byte-identical code blocks, and an English-precedence notice in
  the translated file; CONTRIBUTING's Releasing checklist gains a bullet
  requiring the Korean README to be kept in
  sync. ([#77](https://github.com/jitokim/oh-my-graph/pull/77))
- **"It ships itself" — dogfooding as identity.** A README section states
  that the repo is built by its own graphs and backs it with a recomputable
  number (a `git log --grep` count of the graph-lane co-author trailer), and
  CONTRIBUTING documents that `Co-Authored-By: oh-my-graph
  <graphs@oh-my-graph.dev>` trailer as a transparency convention — an address
  that receives no mail and resolves to no user — with a graph-template sweep
  baking the synchronous-verdict idiom and the trailer instruction into the
  shipped graphs. ([#89](https://github.com/jitokim/oh-my-graph/pull/89))
- **Recurring-pipelines narrative.** A "write it once" README section
  (mirrored in `README.ko.md`) explains *why* a team adopts the tool for daily
  pipelines — the pipeline lives in the YAML once, runs on the subscription
  you already pay for, and gets consistency from pinned prompts plus
  `success_check`/`verify` gates — stated without a $0/free claim and with the
  run boundary in bold: runs do not remember each
  other. ([#85](https://github.com/jitokim/oh-my-graph/pull/85))
- **Live-view screenshot in the READMEs.** `assets/live-view.png` — a real
  dogfood run captured mid-run — is wired into both READMEs after the
  web-live-view paragraph, with an honest
  caption. ([#92](https://github.com/jitokim/oh-my-graph/pull/92))
- **The merge gate is codified in CONTRIBUTING.** A short Merging subsection
  records the existing rule: graph lanes open PRs as drafts, a PR merges only
  after CI is green and every CodeRabbit comment is triaged (applied within a
  one-to-two-line threshold or answered with a reason), and admin merge is for
  merge-queue mechanics, never for bypassing an unfinished
  review. ([#87](https://github.com/jitokim/oh-my-graph/pull/87))
- **ADR-0008: cross-run session reuse is deferred (Proposed).** A design
  record for "named persistent node sessions" that would reuse a claude
  session across runs — captured with its blockers, alternatives, and revisit
  conditions, recommending deferral; manual ledger-based resume remains
  available. ([#78](https://github.com/jitokim/oh-my-graph/pull/78))

## [v0.3.1] - 2026-08-01

The hardening patch after the first CI flake: the test suite, CI, and the
shipped templates absorb the post-mortem's lessons, and static graph checks
learn to warn about things that are valid but will not behave as written —
without changing what any run does.

### Added

- **Advisory placeholder warnings in `lint` and `run --dry-run`.** Every
  `{{ ... }}` token that is placeholder-like but will not resolve — a typo
  or unknown filter that ships verbatim into a paid prompt, an input the
  graph never declares, an artifact of a node that is not an ancestor — now
  gets one warning, across a node's prompt/cwd and its verify block. Tokens
  that don't look like placeholders are left alone as deliberate literal
  text, and the strict-parse judgment comes from the same regex the runtime
  interpolates with, so lint and run can never drift. Warnings are advice
  only: runtime behavior and exit codes are
  unchanged. ([#73](https://github.com/jitokim/oh-my-graph/pull/73))
- **Session-handoff guards.** Three ways a `handoff: session` node could
  quietly resume nothing are now caught up front: a **gate parent is
  rejected at load time** (a gate spawns no subprocess and records no
  session to resume); lint warns when the child's **cwd/worktree differs
  from its session-parent's** (claude's session lookup is
  project-directory-scoped, so the resume may start cold or attach to the
  wrong project) and when the node also declares a **retry** (a retried
  attempt never resumes the parent session); and a retried session node's
  ledger detail now states outright that the retry **started fresh** —
  parent session not resumed. ([#75](https://github.com/jitokim/oh-my-graph/pull/75))
- **An advisory CI stress job, and a "Releasing" checklist.** When a change
  touches the concurrency-heavy packages (schedule, runner, runfeed,
  verify), CI now runs their tests under `-race -count=200` as a
  non-blocking job — a flaky test passes a single run, so determinism gets
  its own signal — and CONTRIBUTING gains the release checklist the v0.3.0
  post-mortem called for. ([#71](https://github.com/jitokim/oh-my-graph/pull/71))

### Changed

- **The shipped templates teach the post-mortem idioms.** In
  `dev-review-pr.yaml` and `self-dev.yaml`: dev nodes commit after each
  coherent step so a node timeout can never lose finished work, e2e nodes
  stress concurrency-touching diffs with `-race -count=300`, and
  style-review nodes check test doubles for unwired synchronization and for
  assertions an absent record would satisfy — the two shapes of the defect
  that survived review. ([#69](https://github.com/jitokim/oh-my-graph/pull/69))

### Fixed

- **The test suite is hardened against the flaky-test class found in the
  post-v0.3.0 audit.** The `haltRunner` double is deterministic — it fails
  only after the sibling has started, instead of racing
  it ([#68](https://github.com/jitokim/oh-my-graph/pull/68)); the same
  audit's sweep replaced timing-dependent doubles with deterministic
  synchronization, made absence assertions prove absence rather than pass
  on an empty record, and covered the real-writer fan-out
  paths ([#70](https://github.com/jitokim/oh-my-graph/pull/70)); and the
  structural gaps it exposed — CLI wiring, dispatch, and run-lock
  edges — are now pinned by
  tests ([#72](https://github.com/jitokim/oh-my-graph/pull/72)).

### Documentation

- **Handoff is a first-class README concept.** What was a buried one-liner
  is now a named section: an artifact-vs-session comparison table, explicit
  statements of what a resumed session does and does not inherit (the
  parent's conversation — never its tool grants, permission mode, or cwd),
  and handoff recipes in
  [docs/EXAMPLES.md](docs/EXAMPLES.md). ([#74](https://github.com/jitokim/oh-my-graph/pull/74))

## [v0.3.0] - 2026-08-01

The live view release: a read-only web view of a run, opened automatically
from an interactive `run`/`auto`, plus static graph tooling (`lint`,
`--dry-run`) and sharper failure reporting.

### Added

- **`serve` — a read-only web live view of one run.** `oh-my-graph serve
  [<run-id>] [--port N]` (default port 8642, newest in-flight run when no id
  is given) renders the run feed-first: a chronological narrative rebuilt
  from the SSE replay of `events.jsonl`, with each settled node's artifact
  inline (capped with a show-more expander) and the failure cause leading
  emphasized when a node fails. The DAG is a compact collapsible side map
  (vendored cytoscape.js 3.34.0 + dagre, MIT, SHA-256-pinned — zero runtime
  network dependencies, no build step). serve is strictly a consumer of the
  run-feed contract, binds to **127.0.0.1 only** (run directories hold
  prompts and session ids), and spawns nothing. ([#60](https://github.com/jitokim/oh-my-graph/pull/60))
- **`run`/`auto` auto-open the live view behind a TTY gate.** An interactive
  run embeds serve's listener on an ephemeral loopback port for exactly the
  run's duration, announces the URL, and opens the browser; `--no-web`,
  scripts and CI get nothing and byte-identical output. Browser-open is the
  **fourth exec seam** (`browser.ExecOpener`, env-scrubbed like every
  spawner, refusing any URL that is not plain http on a loopback host); see
  [ADR-0006](docs/adr/0006-browser-open-is-a-fourth-exec-seam.md). Live-view
  failures never fail the run. ([#65](https://github.com/jitokim/oh-my-graph/pull/65))
- **The `oh-my-graph` plugin agent — a one-word entry point.** The plugin
  now ships `agents/oh-my-graph.md`, a graph-engineering agent launched with
  `claude --agent oh-my-graph` (recommended shell function:
  `omg () { claude --agent oh-my-graph "$@"; }`) instead of typing
  `/oh-my-graph:graph` every turn. It drives the binary; it reimplements no
  graph logic. ([#57](https://github.com/jitokim/oh-my-graph/pull/57))
- **Chat confirms a planned graph before executing it.** A graph-worthy chat
  turn now asks `Run this plan? [y/N]` between printing the plan and running
  it. Default is No; declining prints "plan discarded." and serves the next
  turn; EOF ends the session gracefully. `auto` stays fully
  non-interactive. ([#58](https://github.com/jitokim/oh-my-graph/pull/58))
- **`runs list` renders an in-flight run as RUNNING.** In-flight is read
  from the run-feed contract (an open `run_started` leg in `events.jsonl`);
  a live run with no snapshot yet renders its honestly-known row with "-"
  placeholders instead of being skipped, and a partially-complete live run
  no longer renders FAIL. ([#59](https://github.com/jitokim/oh-my-graph/pull/59))
- **`lint <graph.yaml>`.** Statically report *every* structural issue in a
  graph file — zero nodes spawn, zero cost. `Validate()` is redefined as the
  first element of the new `Graph.Issues()`, so lint and run can never
  disagree about which graphs are valid. Exit 0 when valid, 1 when
  not. ([#50](https://github.com/jitokim/oh-my-graph/pull/50))
- **`run --dry-run`.** Validate and print the resolved plan without
  executing any node: the same lint pass, plus proof that every
  `{{ inputs.* }}` reference resolves against the bound `--input` values.
  Exit 0 when a real run would start, 1 when it would refuse. Artifact
  references stay unjudged — they materialize only while a run
  executes. ([#55](https://github.com/jitokim/oh-my-graph/pull/55))
- **Node failure details name the cause, not just the symptom.** A failed
  node's detail now carries the claude envelope's own error report (else the
  stderr tail), so a subscription session limit reads "exit code 1: You've
  hit your session limit" instead of "failed success_check exit_zero". A
  cancelled sibling reads `cancelled: run halted after node "X" failed`
  instead of "context canceled", deadline and cancel are distinguished, and
  one shared 240-rune cap bounds every detail so `events.jsonl` stays
  tailable. ([#64](https://github.com/jitokim/oh-my-graph/pull/64))
- **Node ids are restricted to a single safe path element at load time.** A
  node id becomes an artifact filename and a serve URL parameter; ids now
  pass the same `^[A-Za-z0-9][A-Za-z0-9._-]*$` rule as worktree names, with
  a load-time error naming the offending id. Planned specs inherit the rule
  through the same parser. ([#61](https://github.com/jitokim/oh-my-graph/pull/61))
- **Unknown `retry.on` causes are rejected at load time.** A typoed cause
  token silently never retried; the six tokens are now constants shared by
  the validator and the scheduler, and anything outside the set is rejected
  with a message listing every valid token. ([#54](https://github.com/jitokim/oh-my-graph/pull/54))
- **Planned commit nodes must stage with scoped `git add <path>`.** The
  auto-mode planner prompt now forbids `git add -A` / `.` / `-u`, so a
  planned node cannot sweep unrelated untracked files into its
  commit. ([#51](https://github.com/jitokim/oh-my-graph/pull/51))
- **The exec-seam invariant is enforced by a test.** An import-allowlist
  test walks `internal/` and `cmd/` and fails CI if any non-test file
  outside the documented seams imports `os/exec` — a new spawner (or a stale
  allowlist entry) is a red test pointing at the ADR requirement, not a
  review oversight. ([#52](https://github.com/jitokim/oh-my-graph/pull/52))

### Fixed

- **serve/runfeed hardening.** Requests whose Host header is not
  `127.0.0.1`/`localhost` are rejected with 403 (DNS-rebinding guard on top
  of the loopback bind); serve's lifecycle surfaces listener failures
  immediately, returns Shutdown's error, and derives request contexts from
  the command context so a cancel also ends open SSE streams; the two
  run-feed readers share one 1 MiB line cap and one framing (plus
  `runfeed.FollowWait` for streams that don't exist yet), pinned by
  package-local reader tests. ([#63](https://github.com/jitokim/oh-my-graph/pull/63))
- **Feed readability.** One feed entry per settled node (the terminal entry
  absorbs the started-line), single-line artifacts render inline in the
  entry head, and a node with no artifact renders no empty result
  block. ([#62](https://github.com/jitokim/oh-my-graph/pull/62))
- **Worktree cleanup survives a branch rename.** Cleanup now judges
  emptiness by the worktree's own HEAD against the recorded base instead of
  the stored branch name, so an empty lane whose branch was renamed is
  removed silently instead of noisily retained. ([#53](https://github.com/jitokim/oh-my-graph/pull/53))

### Documentation

- **README restructured into a front page.** Identity, quickstart, usage,
  one example and the graph model stay; the remaining walkthroughs and
  feature recipes moved to [docs/EXAMPLES.md](docs/EXAMPLES.md), platform
  detail and known gaps to [docs/LIMITATIONS.md](docs/LIMITATIONS.md), and
  the prior-art survey to
  [docs/PRIOR-ART.md](docs/PRIOR-ART.md). ([#66](https://github.com/jitokim/oh-my-graph/pull/66))
- Three DESIGN.md drifts fixed after a doc audit (the budgeted-node argv,
  the repo-layout map, chat's routing call vs "exactly one planner
  call") ([#56](https://github.com/jitokim/oh-my-graph/pull/56)); the
  `handoff: session` exactly-one-parent rule is now stated next to its
  definition in README and
  DESIGN.md ([#49](https://github.com/jitokim/oh-my-graph/pull/49)).

## [v0.2.0] - 2026-07-31

### Added

- **Per-node git worktree isolation — `worktree: <name>`.** A node can now run
  in a dedicated git worktree instead of the invocation directory, so a graph
  never mutates your checked-out working tree (no more sweeping in your
  untracked files, no branch surprises). Nodes that share a name share one
  worktree — a whole `dev → e2e → review → pr` lane works in a single isolated
  checkout — while nodes with **different** names get **different** worktrees
  and can edit files in parallel, without the shared-tree race that otherwise
  forces lanes to serialize. Nodes with no `worktree` field keep today's
  behaviour (fully backward compatible). A clean worktree is removed at run
  end; one with uncommitted changes is kept in place (with instructions) rather
  than losing work. `worktree:` is rejected on auto-planned nodes — an
  unreviewed plan must not spawn worktrees. Worktree provisioning is a third
  `os/exec` seam; see [ADR-0005](docs/adr/0005-worktree-provisioning-is-a-third-exec-seam.md).

## [v0.1.1] - 2026-07-31

First patch after the public launch — run-feed observability and hardening.

### Added

- **`watch <run-id>`.** Tail a run's `events.jsonl` as a plain-text feed (the
  same `▶ / ✓ / ✗` shape as the live run), following until `run_finished` or
  interrupt. A lightweight, dependency-free way to observe a run from another
  terminal — deliberately not a TUI.
- **Gate events in the run feed.** `events.jsonl` now emits `gate_paused`,
  `gate_approved`, and `gate_rejected` from the same hook points as the
  progress feed, so a consumer (fleetops) can see gate state from the stream
  without reading `state.json`. Event-stream schema bumped to `2`.

### Fixed

- **Run-id collisions.** `newRunID` now stays unique for runs minted in the
  same second — a per-process atomic sequence plus a nanosecond timestamp — so
  concurrent or rapid runs no longer share a run directory.
- The snapshot JSON round-trip test now actually asserts the round-trip
  (marshal → parse → equal); `self-dev.yaml` is covered by the shipped-graph
  parse test; and a `!windows` build tag was aligned to `unix`.

## [v0.1.0] - 2026-07-31

Initial MVP: a graph-native orchestrator that runs each DAG node as a real
`claude -p` subprocess on the user's own Claude subscription.

### Added

- **YAML graph model.** A graph file (`name`, `inputs`, `concurrency`,
  `nodes`) with inline `depends_on` edges — no separate edge list, so the
  topology has one source of truth. DAG/cycle validation at load time.
- **Ready-set concurrent scheduler.** Kahn-style topological execution that
  keeps a "ready set" running concurrently, capped by `concurrency` (ceiling
  10, default 4). Halt-on-fail by default; `--continue-on-fail` prunes only
  the failed subtree instead of stopping the whole run.
- **`CLIRunner` with subscription-auth env scrub.** The node runtime is
  a raw `claude -p ... --output-format json` subprocess. Every child process's
  environment has `ANTHROPIC_API_KEY` and `ANTHROPIC_AUTH_TOKEN` deleted so a
  node can never silently fall back to metered API billing — this is
  unit-tested against the built command. Never `--bare`, never the Agent SDK.
- **Artifact and session handoff.** `handoff: artifact` (default) persists
  each node's result to `~/.oh-my-graph/runs/<run-id>/<node-id>.out` and
  interpolates it into dependents via `{{ artifacts.<id> }}` (path by default,
  content with the `| inline` filter) — the only option for fan-in.
  `handoff: session` resumes a single parent's claude session
  (`--resume <session_id>`) for tight sequential continuation; rejected at
  load time on a node with more than one session-parent.
- **Success checks and retry.** `success_check` (`exit_zero`,
  `result_matches` regex) gates whether a node counts as passed. `retry`
  re-runs a failed node up to a flat `max`, always in a fresh session.
- **Evidence-grounded `success_check.verify`.** A node can now be judged on
  something other than its own narration: `verify` declares a command
  (`command`, optional `cwd`, `timeout`, `expect_exit`, `output_matches`) that
  **oh-my-graph itself** runs through `sh -c` and judges by exit code and
  output. It composes by AND after `exit_zero`/`result_matches`, and runs after
  them but *before* the node's output is persisted — so a crashed node is never
  verified against the wreckage and an unverified node leaves no artifact.
  `command`/`cwd` interpolate like a prompt; a missing command, an unparseable
  or over-10m `timeout`, and an uncompilable `output_matches` are rejected at
  load time naming the node. A verification that times out or cannot spawn
  fails the node — never a silent pass. New retry cause token: `verify_failed`.
  `result_matches` is retained and unchanged, but is now documented as a
  secondary, self-reported signal. Auto-planned nodes may not declare `verify`
  (it is shell run outside every coordinator guard). ([#7](https://github.com/jitokim/oh-my-graph/issues/7))
- **A second, deliberate exec seam.** `internal/verify` adds a `Verifier`
  interface with `ShellVerifier` (production), `RefusingVerifier` (the
  scheduler's default, so a forgotten injection fails loudly instead of
  spawning) and `FakeVerifier` (tests). The project invariant is restated, not
  weakened: exactly two objects may spawn a process —
  `runner.CLIRunner` and `verify.ShellVerifier` — each behind its own
  injected interface, and the whole engine still runs its tests with zero real
  spawns. See `docs/adr/0002-verification-is-a-second-exec-seam.md`.
- **Shared child-environment scrub (`internal/childenv`).** The
  `ANTHROPIC_API_KEY` / `ANTHROPIC_AUTH_TOKEN` deletion moved out of
  `internal/runner` into a leaf package used by both spawners, because
  `verify: { command: "claude -p ..." }` is legal and would otherwise have run
  on metered API billing. Behaviour for claude nodes is unchanged.
- **Post-hoc `budget_usd` enforcement.** A node whose actual cost exceeds its
  declared `budget_usd` now fails exactly like a failed `success_check`
  (`NodeBudgetError`, ledger `FAIL` carrying budgeted-vs-actual, halt-on-fail by
  default) so its dependents never start. Output is persisted before the budget
  verdict, so an over-budget node keeps its artifact. Its retry cause token is
  `budget_exceeded`, distinct from `nonzero_exit` so an existing retry policy
  cannot re-spend a blown budget by accident. Enforcement is post-hoc only —
  see "Deferred" for why a mid-node kill isn't possible yet.
- **`RunLedger`.** End-of-run table (session id, cost, verdict, duration per
  node) plus the total cost across the run. Each record also carries the node's
  declared `budget_usd`, so the budget-vs-actual delta is derivable per node
  (`Record.BudgetDeltaUSD`); passing nodes report their remaining headroom in
  the `DETAIL` column.
- **CLI:** `oh-my-graph run <graph.yaml> [--input k=v ...] [--concurrency N] [--continue-on-fail]`.
- **Auto mode.** `oh-my-graph auto "<goal>" [--input k=v ...]` plans a graph
  from a plain-language goal instead of hand-written YAML: a coordinator makes
  one planner call through the same env-scrubbed `NodeRunner` seam, loads the
  JSON reply with the existing graph parser and validator, saves the spec to
  `~/.oh-my-graph/runs/<run-id>/graph.json` (re-runnable with `oh-my-graph run`),
  and executes it on the same scheduler. A planned node can never request
  `permission_mode: bypassPermissions`, set `cwd`, set `agent`, declare a
  `success_check.verify` command, or name a tool outside a fixed allowlist.
- **Layered tool ceiling for auto-planned nodes** (`runner.ToolPolicy`). Each
  planned node runs under settings-source isolation (`--setting-sources ""`),
  its declared allow rules, tool-set narrowing (`--tools`), `--strict-mcp-config`
  and a residual `--disallowedTools` backstop — carried as one value object per
  node so a caller cannot apply three quarters of a ceiling. Isolation is the
  load-bearing layer: it stops a standing `Bash(*)` in the user's own
  `settings.json` from out-matching a planned node's narrower `Bash(git *)`.
  Verified against a real `claude` 2.1.220 (an out-of-scope shell command runs
  without isolation and is denied with it), so the previously-documented
  scoped-Bash gap is **closed for planned nodes**. MCP closure remains
  unverified and is disclosed as such. Hand-written graphs get none of this and
  keep the user's settings, hooks and MCP servers.
- **`agent:` on a node** (hand-written graphs only) → `claude -p --agent <name>`,
  running that node as one of the user's own Claude Code subagents. An
  unresolvable name **fails the node** rather than falling back to plain claude,
  and the failure now carries the CLI's stderr, which lists the available
  agents.
- **Reflection-driven planned-node field dispositions.** A table-driven test
  over `reflect.VisibleFields` of `graph.Node` and `graph.SuccessCheck` fails
  the build if any field is added to the node schema without an explicit
  auto-mode disposition (allowed / constrained / rejected), and every
  non-allowed field is probed to prove its refusal actually fires and names
  that field. Adding a field without deciding what auto mode does with it is
  now a red test, not a review oversight — it caught `success_check.verify`
  on its first run against a schema it had not been written for.
- **Claude Code plugin surface.** A thin `plugin/` wrapper — a `/graph` slash
  command and a description-routed `run-graph` skill — that shells out to the
  `oh-my-graph` binary and reports back the run ledger. It reimplements no
  graph logic.
- **Shipped graphs.** `graphs/haiku-smoke.yaml` (the cheapest real end-to-end
  smoke, a few cents) and `graphs/dev-review-pr.yaml` (a worked
  dev → e2e → parallel review → PR example).

### Deferred (tracked, not in v0.1)

- `gate` node type / human-pause + `oh-my-graph resume` (schema-reserved,
  execution rejected with a clear "not yet implemented").
- Retry policies beyond a flat `max`; any graph DSL beyond `depends_on`.
- A TUI/dashboard — that's [fleetops](https://github.com/jitokim/fleetops)'s job.
- Mid-node budget kill. `budget_usd` is enforced post-hoc (an over-budget node
  fails and halts the run, so *subsequent* nodes never spend), but a node cannot
  be cancelled while it is still overspending — `claude` reports
  `total_cost_usd` only in the envelope it prints at exit. Doing it honestly
  needs streaming cost from the runner; a budget-derived wall-clock timeout was
  rejected as fake enforcement.
- Worktree auto-creation for parallel edits.
- Coordinator auto-mapping of `agent:` by role. Deferred on a design
  constraint, not on effort: a planned node may not carry `agent:` at all, and
  settings-source isolation disables agent discovery, so the two are mutually
  exclusive as built. An implicit scan of `~/.claude/agents` is rejected
  permanently — it would make an `auto` run depend on files the user forgot
  they had.

[Unreleased]: https://github.com/jitokim/oh-my-graph/compare/v0.9.0...HEAD
[v0.9.0]: https://github.com/jitokim/oh-my-graph/compare/v0.8.0...v0.9.0
[v0.8.0]: https://github.com/jitokim/oh-my-graph/compare/v0.7.0...v0.8.0
[v0.7.0]: https://github.com/jitokim/oh-my-graph/compare/v0.6.1...v0.7.0
[v0.6.1]: https://github.com/jitokim/oh-my-graph/compare/v0.6.0...v0.6.1
[v0.6.0]: https://github.com/jitokim/oh-my-graph/compare/v0.5.5...v0.6.0
[v0.5.5]: https://github.com/jitokim/oh-my-graph/compare/v0.5.4...v0.5.5
[v0.5.4]: https://github.com/jitokim/oh-my-graph/compare/v0.5.3...v0.5.4
[v0.5.3]: https://github.com/jitokim/oh-my-graph/compare/v0.5.2...v0.5.3
[v0.5.2]: https://github.com/jitokim/oh-my-graph/compare/v0.5.1...v0.5.2
[v0.5.1]: https://github.com/jitokim/oh-my-graph/compare/v0.5.0...v0.5.1
[v0.5.0]: https://github.com/jitokim/oh-my-graph/compare/v0.4.1...v0.5.0
[v0.4.1]: https://github.com/jitokim/oh-my-graph/compare/v0.4.0...v0.4.1
[v0.4.0]: https://github.com/jitokim/oh-my-graph/compare/v0.3.1...v0.4.0
[v0.3.1]: https://github.com/jitokim/oh-my-graph/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/jitokim/oh-my-graph/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/jitokim/oh-my-graph/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/jitokim/oh-my-graph/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/jitokim/oh-my-graph/releases/tag/v0.1.0
