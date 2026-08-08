# Pre-registration — why 1 of 7 activated planned nodes invoked a skill

Written before any probe was spent. claude 2.1.223, macOS, one machine.
Corpus: `~/.claude/skills`, 35 skills, 0 with `allowed-tools:` frontmatter.

## The observation being explained

ADR 0017 acceptance runs 1+2: **7 activated planned nodes, 1 `Skill` tool_use**.

| run | node | declared tools | pre-registered skill | Skill calls |
|---|---|---|---|---|
| 1 | propose | (unrecorded) | architecture-design | 0 |
| 1 | review | (unrecorded) | pr-code-review | 0 |
| 1 | artifact | (unrecorded) | html-artifact | **1** |
| 1 | check | (unrecorded) | none | 0 |
| 2 t1 | make-html | Read,Write | html-artifact | 0 |
| 2 t1 | check | Read,Glob,Grep | none | 0 |
| 2 rpt | render-html | Read,Grep,Write,Edit | html-artifact | 0 |

## Hypotheses and discriminators

- **H1 tool starvation** — the node lacks the tools a skill's procedure needs.
  - Discriminator (free): 0 of 35 corpus skills declare `allowed-tools`, so a
    skill cannot request tools at all here. `artifact` (hit) and `make-html`
    (miss) both had Write. If the hit and the misses hold the same tools, H1
    cannot explain the difference.
  - Discriminator (paid): arm C fires under `Read,Write,Skill`.
- **H2 the prompt leaves no room** — a planner prompt is a fully specified
  procedure that even prescribes the tool ("Write the file with the Write
  tool"), so it does not read as a request for help.
  - **A** (n=3) planner prompt verbatim, 35-skill corpus → expect 0.
  - **B** (n=3) A + ADR 0017's skill-agnostic sentence → support if ≥2/3 fire.
  - **D1 vs D2** (n=3 each) hold description-fit CONSTANT at a planted skill
    whose description exactly triggers the task, and vary only prompt shape:
    D1 planner-style imperative, D2 plain task statement. D2 fires and D1 does
    not ⇒ H2 confirmed under control.
- **H3 descriptions never arrive, or 35 dilutes** —
  - **D** (35+planted) silent AND **E** (planted only, n=2) firing ⇒ dilution.
  - D and E both silent while C fires ⇒ descriptions do not reach the gate.
  - D firing ⇒ H3 dead.
  - **F** (n=1) does a bundled `references/` file resolve from a staged plugin.
- **H4 the eligible set is wrong** — agent mapping eats the nodes a skill fits.
  - Discriminator (free): run 1 had **0 exclusions** and still 1 of 4. So the
    exclusion cannot be the primary cause of the aggregate, whatever it costs
    run 2.
- **H5 base rate** — most planned nodes have no fitting skill.
  - Discriminator: per-node fit adjudication against the real descriptions,
    plus D. If D fires, the gate works when fit is real and the misses are
    fit/prompt-shape, not mechanism.

## Stop rule, written before measuring

**Change no code if:** D fires (a genuinely fitting description activates
without being named) AND B does not materially beat A (fewer than 2 of 3).
That would say reach works, the gate works, and neither a nudge nor a wider
eligible set is supported by evidence — 1/7 is fit, and the honest output is a
null result.

**Change code only if:** B materially beats A (≥2/3 vs 0/3) — then the fix is
exactly the skill-agnostic sentence from trusted code, and nothing else.

**Report and stop (no code) if:** D and E are both silent while C fires —
activation cannot serve planned nodes as designed; that is an ADR-level
decision, not a patch.

## Ceiling re-verification

Arm **G**: ADR 0004 E1 shape under this argv — node declares `Bash(git *)`,
attempts an out-of-scope `touch`, judged by whether the file appears.
