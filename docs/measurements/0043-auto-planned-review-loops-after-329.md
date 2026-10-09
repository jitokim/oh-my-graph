# ADR 0043 / #288 — after #329, 8 of the next 10 `auto`-planned review loops passed; the 2 that ran out re-raised one real finding

**After #329 (35bfed5, 2026-10-06), which tells a planned reviewer to quote
`{{ self.previous }}`, the next 10 review loops that `auto` planned on this
machine ended like this: 8 passed (6 on the first pass, 2 after one feedback
round) and 2 ran out of rounds at `max: 2`. In both of those, the reviewer
re-raised the same real finding in each round instead of finding new minor
ones: the change needed a file that the goal's scope list had left out, and
no node in the run was allowed to change the scope list.**

That is the failure #288 describes (a fresh reviewer finding different minor
points each round) not showing up in these 10 loops. It is not a convergence
guarantee. The bar set before counting was "at most 1 of 10 runs out of
rounds", and 2 did. DESIGN.md "A CLEAN-only gate need not converge (#288)"
still stands.

- **Date:** loops from 2026-10-06 19:04Z to 2026-10-07 11:35Z (run ids are
  UTC), one machine (macOS), counted on 2026-10-07.
- **Corpus:** every node in an `auto`-planned graph (a run directory with
  `graph.json`) that declares a `feedback:` arc, in runs started after #329
  merged, made with a binary built from 35bfed5 or later.
- **Rule, fixed before counting:** take the next 10 such loops. A reviewer
  that passes on its first pass counts as converged with 0 rounds. A loop
  whose run stopped or crashed before the reviewer's last verdict is listed
  as UNFINISHED and does not count toward the 10.
- **Method:** read each run's `graph.json` (which node declares the arc, its
  `max`, and whether its prompt quotes `{{ self.previous }}`) and
  `events.jsonl` (rounds fired, final verdict). The planner's instruction to
  quote the token is guidance, not enforced, so a loop that doesn't quote it
  would still count, marked. All 10 quote it.
- **Cost:** zero spawns. File reads only.
- **Baseline before #329:** 14 loops in `auto`-planned graphs from
  2026-10-01 on, none quoting `{{ self.previous }}`: 3 ran out of rounds at
  `max: 2`, 11 passed.

## The rows

| # | run id | graph | declarer | rounds / max | outcome | quotes `self.previous` |
|---|---|---|---|---|---|---|
| 1 | `20261006-190419.309516000-2` | issue-292-self-timeout | review | 2 / 2 | **EXHAUSTED** — a scope limit, not new minor findings each round | yes |
| 2 | `20261006-200820.699602000-1` | record-no-baseline-328 | review | 0 / 2 | PASS (first pass) | yes |
| 3 | `20261007-001307.160996000-1` | merge-shepherd-pr-state-verify | review | 0 / 2 | PASS (first pass) | yes |
| 4 | `20261007-005303.249684000-1` | assessor-engine-verify-record | review | 1 / 2 | PASS (after 1 round) | yes |
| 5 | `20261007-015339.741778000-1` | adr-0038-fragment-reuse-menu | review | 0 / 2 | PASS (first pass) | yes |
| 6 | `20261007-031322.313672000-1` | issue-338-readonly-reuse-tools | review | 0 / 2 | PASS (first pass) | yes |
| 7 | `20261007-072608.684932000-1` | gate-description-346 | review | 0 / 2 | PASS (first pass) | yes |
| 8 | `20261007-095348.993191000-1` | gated-dev-graph | review | 0 / 2 | PASS (first pass) | yes |
| 9 | `20261007-105001.468309000-1` | gate-description-web-348 | review | 2 / 2 | **EXHAUSTED** — a scope limit again, the same finding re-raised | yes |
| 10 | `20261007-113516.065081000-1` | input-file-354 | review | 1 / 2 | PASS (after 1 round) | yes |
| — | `20261007-115728.478011000-2` | input-file-354-close-out | check | 2 / 2 | EXHAUSTED, outside the window (the 11th loop). Cause: the check's own wrong expectation of an "unknown key" error, not new minor findings | yes |
| — | `20261006-190128.104101000-1` | self-timeout-292 | review | 0 / 2 | UNFINISHED — the cycle failed at interpolation before the reviewer ran; not counted | yes |

**Count: 10 of 10. 2 exhausted** (#1 and #9).

## The two that ran out

- **Loop 1.** The reviewer's open finding was the same in both rounds, and it
  was real: the branch changed 3 files outside the goal's scope list
  (`cmd/oh-my-graph/dryrun.go`, its test, and
  `internal/docsclaims/claims_test.go`). The change needed them; the scope
  list had left them out by mistake. The archived earlier-round reply
  (`previous/review.out` in that run) re-raises the same finding as "still
  open", adds no new minor point, calls the edits "minimal and correct", and
  says only the owner of the scope list can close it. So the reviewer
  converged on its own finding. The loop ran out because no node in the run
  could close an item that belonged to the scope list's owner.
- **Loop 9.** Same shape: the goal's scope list omitted `docs/RUN-FEED.md`,
  which the change needed, and the reviewer re-raised that one finding.

Both are a limit of the goal as written, not of the reviewer. Under the rule
above they still count as exhausted.
