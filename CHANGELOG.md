# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/).

oh-my-graph is **alpha software**. The graph YAML schema, the CLI, and the
`NodeRunner` interface may change without notice before `v1.0.0`.

## [Unreleased]

### Added

- **`auto --verify-cmd` / `--verify-timeout` — build evidence becomes reachable
  (ADR 0016 §2, #119).** v0.5.0 shipped the whole engine for this and no way to
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
  on an auto graph rather than replay engine-run shell on trust (ADR 0016 §4),
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
  until one of them is renumbered.

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
- **`ClaudeCLIRunner` with subscription-auth env scrub.** The node runtime is
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
  `runner.ClaudeCLIRunner` and `verify.ShellVerifier` — each behind its own
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

[Unreleased]: https://github.com/jitokim/oh-my-graph/compare/v0.5.0...HEAD
[v0.5.0]: https://github.com/jitokim/oh-my-graph/compare/v0.4.1...v0.5.0
[v0.4.1]: https://github.com/jitokim/oh-my-graph/compare/v0.4.0...v0.4.1
[v0.4.0]: https://github.com/jitokim/oh-my-graph/compare/v0.3.1...v0.4.0
[v0.3.1]: https://github.com/jitokim/oh-my-graph/compare/v0.3.0...v0.3.1
[v0.3.0]: https://github.com/jitokim/oh-my-graph/compare/v0.2.0...v0.3.0
[v0.2.0]: https://github.com/jitokim/oh-my-graph/compare/v0.1.1...v0.2.0
[v0.1.1]: https://github.com/jitokim/oh-my-graph/compare/v0.1.0...v0.1.1
[v0.1.0]: https://github.com/jitokim/oh-my-graph/releases/tag/v0.1.0
