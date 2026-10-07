# Measurement — the reuse menu adds 1,686 bytes to each planner prompt on this repository

- **Date:** 2026-10-07 (KST). go1.25.0, macOS, one machine. **No model call,
  no spawn, $0.**
- **Unit: BYTES, not tokens.** Every figure below is `len()` of the rendered
  prompt string in UTF-8 bytes. No tokenizer was run, and none of these numbers
  is a token count or should be quoted as one.
- **Measured at:** `e732bca` — the commit before the one that adds this note.
  That commit adds this note and the test below and nothing else, so the
  prompt-rendering code it measures is the code at `e732bca`.
- **Method (reproducible):**
  `go test ./internal/coordinator -run TestMeasureReuseMenuPromptSize -v -count=1`
  (`internal/coordinator/reusemenu_measure_test.go`).
- **Issue:** [#338](https://github.com/jitokim/oh-my-graph/issues/338).
  **Owed by** ADR 0038 §4 ("Prompt size — unmeasured") and §9.6.

## Why this note exists

ADR 0038 §4 left the menu's prompt cost `unverified` and said an Accepted
status should require the measurement, in the `docs/measurements/` form ADR
0022 used. Its only comparable was ADR 0017's skill corpus (+6,008 prompt
tokens per invocation for 35 skills), which is charged on every node
invocation. The menu is charged once per planning call. This note records what
it actually costs on this repository.

## What varied, and what did not

**One variable: `WithoutReuse()`**, the option `--no-reuse` maps onto. Each
row renders the prompt through the real `Coordinator.Plan` path, using the
golden harness's `plannerPrompts` (FakeRunner, the golden goal and input keys),
with `WithInvocationDir` set to this checkout's root. So the catalog scanned is
the real `graphs/fragments/` at `e732bca`:

- **offered (1):** `read-and-report`
- **skipped (6):**
  - non-prompt slot: 3 (`e2e-verify`, `gated-lane`, `repair-round`)
  - tool not in allowlist: 3 (`pr-publish`, `review-security`, `review-style`)

Each per-call fence nonce has a fixed length. The figures were identical
across three `-count=3` runs.

## Results

| planner call | menu on | menu off (`--no-reuse`) | delta |
|---|---:|---:|---:|
| first | 14,027 bytes | 12,341 bytes | **+1,686 bytes** (+13.7%) |
| repair (one validation refusal) | 15,432 bytes | 13,746 bytes | **+1,686 bytes** |
| goal-loop continuation | 14,749 bytes | 13,063 bytes | **+1,686 bytes** |

The 1,686 bytes are two pieces, and only two:

| piece | bytes |
|---|---:|
| the fenced menu block (`reuseMenuBlock`): the framing text, the citation example and one entry | 1,519 |
| the verdict-advice pointer at the `read-and-report` entry (`reportingShapePointer`), present only when that id is offered | 167 |

## Reading it

- **The cost is per planning call, not per node.** The cost is a flat
  +1,686 bytes on each planner call. That is at most `2 × N` calls for
  `--max-cycles N` (a first call plus one repair, per cycle). Nothing is added
  to any node's prompt: a spliced node carries the fragment's own prompt, as a
  hand-written `use:` would.
- **Most of the block is fixed framing.** Of the 1,519 bytes, only the entry
  and the citation example built from it depend on the catalog. An entry is
  four lines: id, contributes, binds, and a summary capped at 200 bytes. Each
  further admitted shape adds one more entry and nothing else. This note did
  not measure that growth, because this repository admits one shape.
- **With nothing admitted, the cost is zero bytes.** The block is omitted
  entirely (ADR 0038 §2.5), so a repository with no `graphs/fragments/`, or
  none admissible, pays nothing. That is the same as `--no-reuse`.
- **Not measured here:** token counts, and whether the menu changes what the
  planner writes. ADR 0038 §6's 20-goal citation count runs after merge. To
  measure tokens, feed the two rendered prompts to a tokenizer for the
  planner's model. The test prints both sizes, and the prompts it renders are
  the exact strings the planner receives.
