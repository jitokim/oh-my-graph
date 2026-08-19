# ADR 0030 §Context — 1 of 8 `auto` runs carried engine-run build evidence, and the denominator is mostly ineligible

**Of this machine's 8 `auto` runs, exactly 1 carries a `success_check.verify`
in its snapshot — the run of 2026-08-18, where the operator passed
`--verify-cmd`. Seven do not. But six of those seven started BEFORE
`--verify-cmd` existed, so the honest post-flag figure is 1 of 2, and 1 of 2
measures nothing.**

The headline number is true and is the reason ADR 0030 was opened. It is not
evidence that the printed notice fails, and this file exists so that nobody
reads it as if it were.

- **Date:** 2026-08-20 (KST), macOS (darwin 22.6.0), one machine.
- **Corpus:** every `~/.oh-my-graph/runs/*/state.json` — 294 runs, of which 8
  are `auto` runs and 286 are hand-written `run`s.
- **Method:** [`probes/0030-auto-build-evidence/count.py`](probes/0030-auto-build-evidence/count.py),
  which parses each snapshot with the `json` module and asserts every number
  quoted here. It exits 1 rather than reporting if the corpus has moved. Never
  `grep`: `verify:` appears inside prompt strings, and a line count would count
  those.
- **Cost:** zero spawns. 294 file reads.
- **Re-derive:** `python3 docs/measurements/probes/0030-auto-build-evidence/count.py`

## The rows

| run id | nodes | node carrying `success_check.verify` |
| --- | --- | --- |
| `20260802-125517.456024000-1` | 2 | — |
| `20260802-125603.154344000-2` | 2 | — |
| `20260803-081608.190042000-1` | 5 | — |
| `20260803-081635.836216000-1` | 4 | — |
| `20260803-084651.244624000-1` | 4 | — |
| `20260803-084704.248072000-1` | 6 | — |
| `20260812-125543.322191000-1` | 2 | — |
| `20260818-234944.646288000-1` | 6 | `check` |

Definitions, both read from the snapshot and from nothing else:

- **an `auto` run** is one whose snapshot has a non-empty `tool_policies`. That
  is the same auto/hand-written discriminator `resume` uses to decide whether a
  snapshot-borne verification is admissible (`ReattachVerifyCommand`,
  `internal/coordinator/verifycmd.go`): a planned graph carries a per-node
  ceiling and a hand-written graph never does.
- **engine-run build evidence** is any node of the snapshot's graph carrying
  `success_check.verify`. On a planned graph that field can only have come from
  `--verify-cmd` — `validatePlannedNodeVerify` refuses a planner-authored one
  and trusted code attaches the user's afterwards (ADR 0016 §2). So the column
  is exactly "did the operator pass the flag".

## What the number does NOT support

**`--verify-cmd` and the "No build verification configured" notice both shipped
on 2026-08-06** (ADR 0016's implementation). Six of the eight runs — the two of
08-02 and the four of 08-03 — predate that date. They could not have carried
evidence and never saw the notice. The strata:

| stratum | runs | with evidence |
| --- | --- | --- |
| before the flag existed (`< 20260806`) | 6 | 0 (impossible) |
| after (`>= 20260806`) | 2 | 1 |

So the *eligible* corpus is **n = 2**, and it splits 1/1. Nothing about the
notice's effectiveness, the planner's quality, or an operator's habits can be
measured on it. The archive holds a precedent for exactly this mistake:
[0017-skill-activation-yield](0017-skill-activation-yield.md) reached a
conclusion from an arm that had not been controlled, and its own "What the two
findings together do and do not license" section retracts that reading once the
controlling arms were run ("That reading is retracted here", `:158-162`). This
file is written to keep ADR 0030 from repeating it.

What the corpus *does* establish, and it is enough to act on:

1. **The zero-evidence run is the normal shape, not an outlier.** Seven runs of
   eight ended with every judgement made by the model about its own work, on a
   `result_matches` gate the node passes by emitting the right word
   (`internal/graph/graph.go`, `ResultMatches`: "Self-reported").
2. **The one run that carries evidence carries it because a human typed a
   flag.** There is no path by which a planned run acquires it otherwise —
   which is the structural fact ADR 0030 acts on, and it needs no corpus at
   all.

## What could not be determined from this corpus

- **How many of the seven would have been gated.** A snapshot records no
  invocation directory (`state.json` has `graph_source_path`, which points into
  the run directory itself, and the run feed's `run_started` carries no cwd
  either — checked on runs from 08-02, 08-12 and 08-18). So whether each of
  these ran where a `go.mod`/`gradlew`/`package.json` sat is unknown, and the
  gate's true firing rate cannot be back-derived. ADR 0030's snapshot field is
  what makes the next version of this measurable: it records the detected
  signals alongside the declaration.
- **Whether the notice changed anyone's behaviour.** n = 2 eligible runs, one
  of each. Not measurable here, and not measurable by adding runs from a
  machine whose operator has since read the ADR.
- **Whether an operator who is refused will pass `--verify-cmd` or the
  opt-out.** That is the only question that would tell us whether ADR 0030
  works, and it can only be answered after it ships, over the field it adds.
