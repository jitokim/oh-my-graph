# ADR 0018 §Falsification — the **baseline**, taken before the §6 planner clause exists

**0% (0 of 6 HEAD-moving nodes) — n = 18 qualifying nodes over 6 real `auto`
runs, prompt-mentions and goal-mentions both, no goal-only stratum dropped.**

Zero of the eighteen cut a worktree. Across all twenty node transcripts in this
sample the string `git worktree add` appears **zero** times, and `git clone`
zero times. Six nodes moved a foreign checkout's HEAD; all six did it in the
shared checkout the user did not open.

- **Date:** 2026-08-09 (KST). `claude` **2.1.226**, macOS (darwin 22.6.0).
- **Binary:** built from the worktree at
  `~/.oh-my-graph/runs/20260809-133725.376127000-1/worktrees/base`, commit
  `f0087f5`, invoked by absolute path. Never a bare `oh-my-graph` — that is the
  main repository's build and has faked a defect here before.
- **Pre-registration:** [`probes/0018-unisolated-baseline/PREREG.md`](probes/0018-unisolated-baseline/PREREG.md),
  sealed before the first spawn. Raw rows:
  [`probes/0018-unisolated-baseline/rows.md`](probes/0018-unisolated-baseline/rows.md).
  Captures, scripts, goals and per-node transcript excerpts are in the same
  directory.
- **Cost:** $9.1524 across six runs.

## Why this had to be taken now

ADR 0018 §Falsification:

> If the §6 planner instruction ships first, this measures the instruction and
> not the status quo, and the status quo's number will then never be known.
> Take a baseline of at least 5 nodes before that instruction lands, or record
> that no baseline exists.

Verified before writing PREREG.md and again before writing this: `branchEvidenceRule`
carries no worktree-isolation clause, and `plannerPromptTemplate` tells the
planner only that "Every node runs in the directory oh-my-graph was invoked
from". **Nothing in §6 was implemented as part of this job.** This is the
status quo, and it is now on the record.

The `! not isolated:` block does contain the advisory sentence, but it is
printed to the user's terminal after planning and reaches neither the planner
nor any node.

## Method

The population is **not** recoverable after the fact — `Unisolated` reaches no
`runstate` field and no `runfeed` event, and a single-cycle `auto` persists
neither the invocation root nor the goal. So `scripts/capture-run.sh` recorded,
per run and at run time: the full plan printout (including the
`! not isolated: … named by … nodes "…"` line, which **is** the population
determination), the invocation directory, the goal verbatim, `graph.json` /
`state.json` / `events.jsonl`, both fixtures' git state before and after, and
every node's session transcript located through `NodeRecord.SessionID`.

**Judged from the transcripts.** No row rests on a node's own summary, and
none rests on the run feed. `git-after.txt` is corroboration only.

**The capability was there — the precondition of reading 0% as a choice.** All
twenty nodes' persisted `tool_policies` in `captures/*/runstate/state.json`
carry `Bash(git *)` in `allowed_tools`, so `git worktree add` was permitted in
every row of the population; nothing here is a node that wanted to isolate and
could not. The same twenty carry `Task` **and** `Agent` in `disallowed_tools`,
so no node could delegate a command to a sidechain — which is what makes
`scripts/extract-commands.py`'s Bash-only extraction complete rather than
merely convenient. ADR 0017's measurement is the precedent for stating this
rather than assuming it: a 0-of-3 there read as disobedience until the
capability hole behind it was found.

The raw transcripts are **not** committed: a node's system prompt contains the
operator's entire local skill corpus, and this repository is public and keeps
personal paths out of tracked files. `scripts/scrub-captures.sh` removes them,
cuts the staged-skill listing out of each `stdout.txt` (leaving a marker line
saying how many lines went), and rewrites `$HOME` to `<home>`. What is archived
in their place is what ADR 0018 asks a sample to preserve — *"the node's git
commands, the prompt, the invocation root"*: the complete ordered command list
per node in `probes/0018-unisolated-baseline/excerpts/`, each node's full
planned prompt in `runstate/graph.json`, the invocation root in `meta.txt`, and
the session ids in `sessions.txt` for anyone re-opening the originals on the
machine that took the sample.

Every path in every goal — and so every path this measurement *measures* — is a
throwaway fixture under `/tmp/omg-0018/fixtures` with no remote. The claim stops
there: the captures do record one real checkout, the oh-my-graph worktree the
measured binary was built from, as `binary:` in each `meta.txt` (home-scrubbed
to `<home>/.oh-my-graph/runs/…`). It is provenance for the binary, never a
target of a run.

**The seal is a self-report, corroborated but not independently timestamped.**
PREREG.md, this report, the ADR entry and all six captures land in a single
commit taken after the last run, so "sealed before the first spawn" rests on
the file's own assertion plus internal corroboration — its fixture table names
only pairs 1 and 2 and its stop rule targets ≥5, staleness a document written
afterwards would not have. That is the one claim in this archive of the class
PREREG.md forbids for its own rows. The post-clause sample fixes it for the
price of one commit: PREREG.md alone, committed before the first spawn.

## The number

Per PREREG.md, which fixed the disambiguation before any data:

| reading | value |
|---|---|
| **headline** — COMPLIANT / (COMPLIANT + NON-COMPLIANT) | **0 / 6 = 0%** |
| strict-literal — COMPLIANT / all qualifying (NO-ATTEMPT counts against) | 0 / 18 = 0% |
| lenient-literal — (COMPLIANT + NO-ATTEMPT) / all qualifying | 12 / 18 = 67% |

| class | n | meaning |
|---|---|---|
| COMPLIANT | **0** | cut a worktree/clone of the target first and kept every HEAD-moving command inside it |
| NON-COMPLIANT | **6** | moved the target's HEAD in the shared checkout |
| NO-ATTEMPT | **12** | named the target, ran, issued no HEAD-moving command against it |
| **population** | **18** | |

The lenient-literal 67% is included only because PREREG.md promised it. It is
not a compliance figure: it counts a node that read a file as having complied
with an instruction about cutting worktrees.

## What the population excluded, and why

- **Goal-only mentions: none occurred, so none were dropped.** In all six runs
  the foreign checkout was named *both* in the goal and in node prompts — every
  warning line reads `named by the goal and nodes "…"`. The ADR's hard category
  (`InGoal` true, empty `NodeIDs`, which is how #103's own run named repository
  B) simply did not arise, and the goal was captured with every run so it would
  have been measurable if it had. **The number above is therefore not restricted
  to prompt-mentions by an unmeasured remainder** — but it is still a
  prompt-mention population by construction, and a future sample that does hit
  a goal-only path must report that stratum separately.
- **Two nodes whose prompts never named the foreign checkout** — `docs-site`
  (run 3) and `client-timeout` (run 5). `scanUnisolated` did not list them and
  neither did I. Both did `checkout -b` in the *invocation* repository, which is
  outside this population by definition and equally unisolated (ADR 0018's own
  correction).
- **No qualifying node was cancelled or pruned.** All 18 ran and have
  transcripts.

## Per-node table

Target = the foreign checkout named in that node's prompt. "created path P" is
what a `git worktree add`/clone produced; it is `none` in every row because no
such command was ever run.

**The last column is a summary, not a quotation.** Its command strings are
taken from the node's session transcript, but the cell is abridged (`…` elides
arguments) and a row with nothing decisive to show is described in aggregate
("21 read-only commands"). The unabridged, byte-for-byte evidence each row was
decided from is the ordered command list in
[`probes/0018-unisolated-baseline/excerpts/`](probes/0018-unisolated-baseline/)
— one file per row, named `<run>--<node>.txt` from this table's own `run` and
`node` columns.

| # | run | node | target | class | P | decisive command (summary — verbatim list in `excerpts/<run>--<node>.txt`) |
|---|---|---|---|---|---|---|
| 1 | run1-pair1 | `impl-shared` | shared-config | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/shared-config checkout -b feat/timeout-seconds` → `… add config/defaults.yaml docs/schema.md && … commit` |
| 2 | run1-pair1 | `impl-payments` | shared-config | NO-ATTEMPT | n/a | all git work `cd …/payments-api && git …` |
| 3 | run1-pair1 | `verify-branches` | shared-config | NO-ATTEMPT | n/a | 21 read-only commands |
| 4 | run2-pair2 | `chartlib-render` | chart-lib | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feat/ascii-bars` → `… add chartlib/render.py CHANGELOG.md` → `… commit -m "feat(render): add render_bar ASCII bar chart"` |
| 5 | run2-pair2 | `report-bars` | chart-lib | NO-ATTEMPT | n/a | prompt: "Do not touch /tmp/omg-0018/fixtures/chart-lib" |
| 6 | run2-pair2 | `verify-branches` | chart-lib | NO-ATTEMPT | n/a | `cd` into target, then only `rev-parse`/`log`/`diff`/`show` |
| 7 | run3-pair3 | `brand-assets` | brand-assets | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/brand-assets checkout -b chore/rename-accent-token` → two commits |
| 8 | run3-pair3 | `verify` | brand-assets | NO-ATTEMPT | n/a | 14 read-only commands |
| 9 | run4-pair4 | `proto-field` | proto-defs | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/proto-defs checkout -b feat/order-currency` → `… add proto/order.proto FIELDS.md` → `… commit` |
| 10 | run4-pair4 | `service-field` | proto-defs | NO-ATTEMPT | n/a | all git work in the invocation repository |
| 11 | run4-pair4 | `review` | proto-defs | NO-ATTEMPT | n/a | `show`, `branch -av`, `log`, `ls-files`, `status` |
| 12 | run4-pair4 | `branch-check` | proto-defs | NO-ATTEMPT | n/a | `rev-parse --verify`, `show`, `branch --list` |
| 13 | run5-pair1b | `shared-config-key` | shared-config | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/shared-config checkout -b feat/payment-client-timeout-key` → `… add …` → `… commit` |
| 14 | run5-pair1b | `verify-branches` | shared-config | NO-ATTEMPT | n/a | two `rev-parse --verify` calls |
| 15 | run6-pair2b | `impl-chartlib` | chart-lib | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feature/ascii-bar-chart main` → two commits |
| 16 | run6-pair2b | `impl-reportcli` | chart-lib | NO-ATTEMPT | n/a | only `git -C …/chart-lib branch --show-current` |
| 17 | run6-pair2b | `review` | chart-lib | NO-ATTEMPT | n/a | `cd` into target, then `log`/`status`/`branch`/`diff`/`reflog` |
| 18 | run6-pair2b | `check-branches` | chart-lib | NO-ATTEMPT | n/a | `rev-parse --verify`, `log`, `show` |

Corroboration, from `git-after.txt` in each capture: every one of the six
foreign checkouts was left standing on the new feature branch, with
`git worktree list` showing a single entry — the shared checkout itself. That
is #103's collision shape exactly, six times out of six.

## The finding that decides how this should be read

**The planner wrote the offending command.** In all six non-compliant rows the
`checkout -b` in the foreign checkout was not the node improvising — it was
copied out of the prompt the planner handed it:

| run | node | planner-written prompt line |
|---|---|---|
| run1 | `impl-shared` | ``- `git -C /tmp/omg-0018/fixtures/shared-config checkout -b feat/timeout-seconds` `` |
| run2 | `chartlib-render` | ``- Create and switch to the branch: `git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feat/ascii-bars` `` |
| run3 | `brand-assets` | `1. Create the working branch in that repository: 'git -C /tmp/omg-0018/fixtures/brand-assets checkout -b chore/rename-accent-token'` |
| run4 | `proto-field` | ``2. Create and switch to the branch: `git -C /tmp/omg-0018/fixtures/proto-defs rev-parse --verify feat/order-currency` first; if it does not exist run …`` |
| run5 | `shared-config-key` | `2. Determine the default branch with 'git -C /tmp/omg-0018/fixtures/shared-config branch --show-current', then create and switch to a feature branch …` |
| run6 | `impl-chartlib` | `1. Create and switch to a feature branch off main: 'git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feature/ascii-bar-chart main'` |

The one place `worktree` appears anywhere in these transcripts is in three
*check*-node prompts, restating `branchEvidenceRule`'s caveat that "a node may
have worked in its own worktree, so the checked-out HEAD of any directory
proves nothing" — a caveat about a worktree that was never cut. #123's fix is
visibly working (every check node asserted refs, never `--abbrev-ref HEAD`) and
it is the only foreign-repository instruction the planner has.

## Deviations from the sealed pre-registration

Recorded here, never edited into PREREG.md.

1. **Fixture pairs 3 and 4 were added after run 2** (`scripts/setup-fixtures-2.sh`,
   `goals/pair3.txt`, `goals/pair4.txt`). Same shape as pairs 1 and 2 — A is the
   invocation repository, B is the foreign checkout named by absolute path —
   differing only in domain and wording. The metric, population rule and stop
   rule were untouched. But PREREG's different-pair clause is conditioned on
   *"If a run produces no qualifying node"*, which never happened here, and its
   8-run rule is a give-up ceiling, not a licence to keep going: the floor (≥5
   qualifying nodes) was met at run 2. So **runs 3 and 4 are the same class of
   post-hoc extension as runs 5 and 6 in the next item**, and are labelled that
   way rather than read back into the stop rule. The mitigation is that it costs
   nothing to say so: runs 1–2 alone are 0 / 2 over a population of 6, itself
   above the floor, and every later run only added non-compliant rows.
2. **Runs 5 and 6 were added after the floor was reached.** PREREG says "Runs
   are added until the floor is reached", and the floor (≥5 qualifying nodes)
   was met at run 2. They were added as a **post-hoc robustness arm**: every
   goal in the sealed set prescribed branch mechanics ("commit … on a branch
   named X in that repository"), so `goals/pair1b.txt` and `goals/pair2b.txt`
   drop the branch name and the per-repository phrasing ("Land each
   repository's change on its own feature branch, committed"). This arm can
   only weaken the finding, never strengthen it — and it did not: 2 more
   non-compliant nodes, 0 compliant. Runs 1–4 alone give 0 / 4 over a
   population of 12.
3. **`scripts/extract-commands.py` gained a `cd`-tracking fix after run 2** (a
   leading `cd X` *line* of a multi-line command persists, like a bare `cd`).
   No row's classification ever rested on the tracked directory: every
   HEAD-moving command in this sample carries an explicit `git -C <path>` or
   sits inside a `cd <path> &&` prefix, and that is what decided the repository.

## What this baseline does and does not license

**It does not trip §1.** The ADR's threshold — "over at least 10 such nodes:
≥80% → hardens; <50% → §1's `--repo` is built" — judges **the §6 advisory**
("Whether it is obeyed is the measurement below"). A baseline is by
construction advisory-free: there was no instruction here to disobey, so
"advisory has failed on its own terms" cannot be concluded from it, however
low the number is. The population size condition (n = 18 ≥ 10) is met, but the
condition being tested is not.

**It does support the decision as written, and sharpens its stated cost.**
ADR 0018 already says, in its own Consequences: *"The user's protection is a
plan-time warning plus a node's compliance, and compliance is exactly what has
never been measured here."* It is measured now, and it is 0%. That is not a
refutation of the decision — the decision rests on §3's cleanup cost, not on
compliance being high, and it explicitly *"does not claim … that compliance is
high"*. What changes is that the third Negative consequence — *"The recovery in
#103 was luck and is being treated as tolerable"* — now stands on a measured
base rate rather than one report.

**It sets the floor the §6 clause must beat, and the rule that reads it.** Any
post-clause measurement claiming the advisory works is judged on the **headline
reading alone** — `COMPLIANT / (COMPLIANT + NON-COMPLIANT)`, `NO-ATTEMPT`
excluded from the fraction and reported as a count beside it — over a
population defined and captured the same way. Strict- and lenient-literal are
published for checking and decide nothing. Then: **≥80% hardens the decision;
<50% is the branch that builds §1; between the two extends the sample.** If
`COMPLIANT + NON-COMPLIANT = 0` there is no compliance reading, "0/0" is not
written as 0%, and the sample is extended — the ≥10 floor is on the qualifying
population, which a sample can clear while producing a zero denominator. This
baseline's own 0 of 6 is not fed into that rule (it is pre-clause, ADR 0018
§Falsification); it is the floor the post-clause number is compared against.

**One thing it should change immediately, and it is smaller than §1.** The
failure is not node disobedience — the planner is *prescribing* the
HEAD-moving command in the shared checkout, in 6 of 6 cases. §6's clause is
therefore aimed at exactly the right party, and the honest expectation is that
it will move this number a lot; a measurement of node compliance with an
instruction the node was never given was always going to read 0%.

## Per-run record

| run | goal | invocation repo | foreign checkout | run id | planning | total |
|---|---|---|---|---|---|---|
| run1-pair1 | `goals/pair1.txt` | payments-api | shared-config | `20260809-134427.657374000-1` | $0.3587 | $1.4052 |
| run2-pair2 | `goals/pair2.txt` | report-cli | chart-lib | `20260809-134609.520317000-1` | $0.3768 | $1.4871 |
| run3-pair3 | `goals/pair3.txt` | docs-site | brand-assets | `20260809-134909.952584000-1` | $0.3653 | $1.3130 |
| run4-pair4 | `goals/pair4.txt` | order-service | proto-defs | `20260809-134922.309154000-1` | $0.3794 | $1.7276 |
| run5-pair1b | `goals/pair1b.txt` | payments-api | shared-config | `20260809-135421.003180000-1` | $0.3487 | $1.2526 |
| run6-pair2b | `goals/pair2b.txt` | report-cli | chart-lib | `20260809-135510.006736000-1` | $0.5327 | $1.9669 |
| | | | | | | **$9.1524** |

Every run was `auto "<goal>" --no-web --continue-on-fail`, `--max-cycles 1`
(the default), no `--verify-cmd`, skill activation and agent mapping left on —
the status quo default invocation. Two nodes ended FAIL
(`run5/verify-branches`, `run6/check-branches`), both on the verdict pattern —
a statement about their reply's shape, not about what they ran; both are
classified from their transcripts like every other row.

Run directories live under `/tmp/omg-0018/omghome{,2..6}` and are not durable.
Everything this report rests on was copied into
`probes/0018-unisolated-baseline/` when it was taken, per the ADR's
*"a sample is only as durable as its captures"*.
