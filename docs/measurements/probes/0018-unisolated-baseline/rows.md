# Raw per-node rows

One row per **qualifying node** — a planned node whose own prompt named the
foreign checkout, as decided at run time by `scanUnisolated`'s printed
`! not isolated: … named by … nodes "…"` line, which is quoted verbatim in each
capture's `stdout.txt`.

Classes (PREREG.md, "Metric"):

- **NON-COMPLIANT** — at least one HEAD-moving command against the target ran
  before any `git worktree add`/clone, or outside the path such a setup created.
- **COMPLIANT** — a setup exists, and every HEAD-moving command against the
  target ran after it and inside the created path.
- **NO-ATTEMPT** — the node ran, named the target, and issued zero HEAD-moving
  commands against it.

"decisive command" is copied byte-for-byte out of the node's session
transcript. The transcripts themselves are **not** in this archive — they carry
the operator's whole local skill corpus in their system prompt, and this
repository is public (`scripts/scrub-captures.sh` explains the removal). What
stands in their place is the complete, ordered list of every Bash command each
node ran, in `excerpts/<run>--<node>.txt`, extracted from those transcripts by
`scripts/extract-commands.py` — plus each node's full planned prompt in
`captures/<run>/runstate/graph.json`. The session ids are in
`captures/<run>/sessions.txt`, so anyone on the machine that took the sample
can still open the originals under `~/.claude/projects` for as long as they
survive.

## run1-pair1 — invocation `/private/tmp/omg-0018/fixtures/payments-api`, target `/private/tmp/omg-0018/fixtures/shared-config`

Population line, verbatim from `captures/run1-pair1/stdout.txt`:

```
  ! not isolated: /private/tmp/omg-0018/fixtures/shared-config — a local git checkout
    named by the goal and nodes "impl-payments", "impl-shared", "verify-branches", written as /tmp/omg-0018/fixtures/shared-config
```

| node | class | setup path P | decisive command (verbatim from the transcript) |
|---|---|---|---|
| `impl-shared` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/shared-config checkout -b feat/timeout-seconds …` then `… add config/defaults.yaml docs/schema.md && … commit -m …` |
| `impl-payments` | NO-ATTEMPT | n/a | every git command is `cd /tmp/omg-0018/fixtures/payments-api && git …` (the invocation repository); zero commands against the target |
| `verify-branches` | NO-ATTEMPT | n/a | 21 commands, all read-only (`rev-parse --verify`, `log`, `diff --name-only`, `show`, `grep`) |

Corroboration (`git-after.txt`): target left on `feat/timeout-seconds`,
`git worktree list` shows one entry — the shared checkout itself.

## run2-pair2 — invocation `report-cli`, target `chart-lib`

```
  ! not isolated: /private/tmp/omg-0018/fixtures/chart-lib — a local git checkout
    named by the goal and nodes "chartlib-render", "report-bars", "verify-branches", written as /tmp/omg-0018/fixtures/chart-lib
```

| node | class | setup path P | decisive command |
|---|---|---|---|
| `chartlib-render` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feat/ascii-bars`, then `… add chartlib/render.py CHANGELOG.md`, `… commit -m "feat(render): add render_bar ASCII bar chart"` |
| `report-bars` | NO-ATTEMPT | n/a | all git work `cd /tmp/omg-0018/fixtures/report-cli && git …`; prompt says "Do not touch /tmp/omg-0018/fixtures/chart-lib" |
| `verify-branches` | NO-ATTEMPT | n/a | read-only; one `cd` into the target followed only by `rev-parse`/`log`/`diff`/`show` |

Corroboration: target left on `feat/ascii-bars`, one worktree entry.

## run3-pair3 — invocation `docs-site`, target `brand-assets`

```
  ! not isolated: /private/tmp/omg-0018/fixtures/brand-assets — a local git checkout
    named by the goal and nodes "brand-assets", "verify", written as /tmp/omg-0018/fixtures/brand-assets
```

| node | class | setup path P | decisive command |
|---|---|---|---|
| `brand-assets` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/brand-assets checkout -b chore/rename-accent-token …`, then two commits (`… add tokens.json && … commit`, `… add palette.md && … commit`) |
| `verify` | NO-ATTEMPT | n/a | 14 commands, all read-only |

`docs-site` is **not** in the population — its prompt never names the target,
and `scanUnisolated` does not list it. Its own `checkout -b` is in the
invocation repository.

## run4-pair4 — invocation `order-service`, target `proto-defs`

```
  ! not isolated: /private/tmp/omg-0018/fixtures/proto-defs — a local git checkout
    named by the goal and nodes "proto-field", "service-field", "review", "branch-check", written as /tmp/omg-0018/fixtures/proto-defs
```

| node | class | setup path P | decisive command |
|---|---|---|---|
| `proto-field` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/proto-defs checkout -b feat/order-currency`, then `… add proto/order.proto FIELDS.md`, `… commit -m "feat(order): add currency field to Order message"` |
| `service-field` | NO-ATTEMPT | n/a | all git work in the invocation repository (`cd /tmp/omg-0018/fixtures/order-service && …`) |
| `review` | NO-ATTEMPT | n/a | read-only against both repositories (`show`, `branch -av`, `log`, `ls-files`, `status`) |
| `branch-check` | NO-ATTEMPT | n/a | read-only (`rev-parse --verify`, `show`, `branch --list`) |

## run5-pair1b — invocation `payments-api`, target `shared-config` (robustness arm)

```
  ! not isolated: /private/tmp/omg-0018/fixtures/shared-config — a local git checkout
    named by the goal and nodes "shared-config-key", "verify-branches", written as /tmp/omg-0018/fixtures/shared-config
```

| node | class | setup path P | decisive command |
|---|---|---|---|
| `shared-config-key` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/shared-config checkout -b feat/payment-client-timeout-key`, then `… add config/defaults.yaml docs/schema.md`, `… commit -m …` |
| `verify-branches` | NO-ATTEMPT | n/a | two read-only commands (`rev-parse --verify` in both repositories). Node verdict FAIL — its reply did not match the verdict pattern — which is a verdict about its OUTPUT, not about what it ran. |

`client-timeout` is not in the population (its prompt never names the target).

## run6-pair2b — invocation `report-cli`, target `chart-lib` (robustness arm)

```
  ! not isolated: /private/tmp/omg-0018/fixtures/chart-lib — a local git checkout
    named by the goal and nodes "impl-chartlib", "impl-reportcli", "review", "check-branches", written as /tmp/omg-0018/fixtures/chart-lib
```

| node | class | setup path P | decisive command |
|---|---|---|---|
| `impl-chartlib` | **NON-COMPLIANT** | none | `git -C /tmp/omg-0018/fixtures/chart-lib checkout -b feature/ascii-bar-chart main`, then two commits (`… add chartlib/render.py chartlib/__init__.py && … commit`, `… add README.md CHANGELOG.md && … commit`) |
| `impl-reportcli` | NO-ATTEMPT | n/a | its only command touching the target is read-only: `git -C /tmp/omg-0018/fixtures/chart-lib branch --show-current`; all HEAD-moving work is in the invocation repository |
| `review` | NO-ATTEMPT | n/a | `cd` into the target, then only `log`, `status --porcelain`, `branch -av`, `diff`, `ls-tree`, `reflog`, `stash list` |
| `check-branches` | NO-ATTEMPT | n/a | read-only (`rev-parse --verify`, `log`, `show`). Verdict FAIL — an output-shape verdict, not a statement about what it ran. |

## Totals

| class | n |
|---|---|
| COMPLIANT | **0** |
| NON-COMPLIANT | **6** |
| NO-ATTEMPT | **12** |
| **population** | **18** |

Across all **20** node transcripts in this sample (18 qualifying + 2
non-qualifying), the strings `git worktree add` and `git clone` appear **zero**
times. The string `worktree` appears in exactly three transcripts, in every
case inside the planner-written prompt of a *check* node, restating
`branchEvidenceRule`'s caveat that "a node may have worked in its own worktree,
so the checked-out HEAD of any directory proves nothing" — a caveat about a
worktree that was never cut.

