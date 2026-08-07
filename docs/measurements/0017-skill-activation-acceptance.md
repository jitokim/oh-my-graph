# ADR 0017 acceptance test, run 1 — run record

> **This is the first of two acceptance runs.** It scored 1 of 3 on criterion 3
> and its per-node table below shows a single `Skill` invocation across four
> activated nodes. The second run —
> `0017-skill-activation-acceptance-run-2.md` — was pre-registered with a
> verdict rule fixed in writing, recorded **zero** `Skill` invocations across
> two treatment plans, and is what ADR 0017's Status rests on. Read the two
> together: 7 activated planned nodes, 1 invocation.

The ADR requires this file: *"This run writes its goal, its full node-id list,
and the node → skills table into `docs/measurements/` as it goes"*, because
ADR 0012's yield measurement could not be re-derived from its own record.

- **Date:** 2026-08-07 (KST), `claude` **2.1.223**, macOS (darwin 22.6.0).
- **Binary:** built from this worktree, `make build` → `bin/oh-my-graph`.
- **Corpus:** `~/.claude/skills`, 35 skills, 0 shadowed.
- **Goal (both arms):** `establish a fix proposal for this issue, review the
  proposal, and turn it into an HTML artifact`
- **Working tree given to the nodes:** a scratch git repo holding one
  `ISSUE.md` (a Python `parse_port` that accepts out-of-range ports).

## Pre-registration

Written before the treatment run was launched; node ids appended from the plan
printout, before any transcript was opened.

| node id    | job in the goal               | pre-registered skill  |
|------------|-------------------------------|-----------------------|
| `propose`  | establish a fix proposal      | `architecture-design` |
| `review`   | review the proposal           | `pr-code-review`      |
| `artifact` | turn it into an HTML artifact | `html-artifact`       |
| `check`    | planner-added 4th node        | none; not scored      |

## Treatment arm — run `20260806-233849.761728000-1`

Plan `issue-fix-proposal-artifact`, 4 nodes. Skill activation ENABLED on 4 of
4 nodes; 35 skills staged; disclosed estimate ~6,020 prompt tokens per
invocation.

Skill tool calls extracted from each node's session transcript
(`~/.claude/projects/-private-tmp-omg-accept-work/<session_id>.jsonl`), by
`tool_use` block with `name == "Skill"`:

| node       | session (prefix) | Skill calls | skill invoked                              | pre-registered | match |
|------------|------------------|-------------|--------------------------------------------|----------------|-------|
| `propose`  | `ffb6dee8`       | 0           | —                                          | `architecture-design` | NO |
| `review`   | `447f6731`       | 0           | —                                          | `pr-code-review`      | NO |
| `artifact` | `65a2776d`       | 1           | `oh-my-graph-staged-skills:html-artifact`  | `html-artifact`       | YES |
| `check`    | `446694d2`       | 0           | —                                          | (none)                | n/a |

**Total Skill tool calls: 1.**

The one invocation is not a self-report: the transcript carries the `tool_use`
block, its `tool_result` (`Launching skill: …`), and then the skill's own body
text prefixed with `Base directory for this skill:
<run-dir>/skills-plugin/skills/html-artifact` — i.e. the body was read out of
the staged plugin directory this run built.

## Control arm — `--no-skill-activation`, run `20260806-234007.239397000-1`

Same goal, separate `OMG_HOME` and working tree. The planner produced a
different 4-node shape (`investigate`, `propose`, `review`, `artifact`).

- No `skill scan:` line in the printout at all — the scan never runs.
- No `skills-plugin/` directory in the run directory.
- Every node's persisted policy: `plugin_dirs: null`, `Skill` absent from
  `tools`, `setting_sources: ""`.
- **Total Skill tool calls across all node transcripts: 0.**

## Criterion-by-criterion

1. **Grant present** — PASS. All four treatment policies in `state.json` carry
   `Skill` in `tools`, `plugin_dirs` = `<run-dir>/skills-plugin`, and
   `setting_sources` still `""`.
2. **Activation alive against a negative control** — PASS. Treatment 1,
   control 0, and the control differs structurally (no scan, no staged dir, no
   grant).
3. **The three skills on the right jobs** — **FAIL, 1 of 3.** `artifact` hit
   its pre-registered skill; `propose` and `review` invoked nothing.
4. **Nothing was denied** — **UNREADABLE.** `runner.claudeEnvelope` still has
   no `permission_denials` field, so the run records nothing to check. The ADR
   marks this "merely unimplemented"; it is still unimplemented.
5. **The ceiling held on the E1 shape** — PASS. A node given
   `--setting-sources "" --plugin-dir <staged> --allowedTools 'Bash(git *)'
   --tools Read,Bash,Skill --strict-mcp-config` was told to `touch
   /tmp/omg-ceiling-probe.txt`. Judged by the filesystem: the file does not
   exist.
6. **Billing intact** — **UNREADABLE per node.** The envelope's `provider`
   field is not captured either, so this cannot be asserted from the run
   record. Env scrub is unit-tested; the claim this criterion wanted is not.

## Positive controls (that the plumbing works at all)

Both run by hand against the *live* staged directory of the treatment run,
with the same argv shape `buildArgs` emits:

| probe | policy shape | result | cost |
|-------|--------------|--------|------|
| P1 | `--tools Read,Skill`, `--allowedTools Read` | `Skill` fired → `oh-my-graph-staged-skills:html-artifact` | $0.158 |
| P2 | `propose`'s exact tools/allowed/disallowed lists | `Skill` fired → `oh-my-graph-staged-skills:architecture-design` | $0.144 |

P2 matters for the diagnosis: `architecture-design` **is** reachable under the
exact policy `propose` ran with. The two misses are the model declining to
activate, not the grant failing to arrive.

## Cost

| item | USD |
|------|-----|
| treatment planning | 0.3695 |
| treatment nodes (4) | 1.3371 |
| **treatment total** | **1.7066** |
| control planning | 0.3301 |
| control nodes (4) | see control ledger |
| probes P1 + P2 + ceiling | 0.457 |
