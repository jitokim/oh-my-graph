# Pre-registration, round 2 (2026-08-08) — the cell the first round did not run

Written BEFORE any round-2 spawn. claude 2.1.224 / macOS (round 1 was 2.1.223).

## What round 1's own data already settles, on re-reading the raw records

`logs/results.jsonl` records, per spawn, WHICH skill the `Skill` tool_use named.
That column was recorded and never reported. It says:

- arm B, 8 of 9 fired, and **all 8 named `html-artifact`** — one of the user's
  own 35 real skills, not a planted one.
- arm A is the same 35-skill corpus and the same prompt bytes minus one
  sentence: 0 of 9.

So the fitting REAL description was in arm A's corpus and was never consulted.
"The 1-of-7 is a FIT number" does not survive its own data.

## The remaining ambiguity, and the arm that closes it

`html-artifact` fired 8/8 **with** the sentence. It could still be that the
model only reaches a real description once the sentence lowers the threshold —
i.e. that this description is a marginal fit that the sentence rescues (the
shipped record's reading) — rather than a genuine fit that the planner register
suppresses consideration of (the reading the B-precision column suggests).

**Arm L.** Prompt A verbatim (no sentence). Corpus = exactly one skill, the
user's real `html-artifact`, copied byte-for-byte from `~/.claude/skills`.
n = 3. Everything else identical to arm A.

- L fires (≥2 of 3) → the real description is a genuine fit that the gate
  matches unaided. Then A = 0/9 is a CONSIDERATION failure, not a fit failure,
  and the sentence is the right remedy for the right reason.
- L does not fire (0 of 3) → the real description is at best marginal, and the
  shipped "fit" attribution stands.

**Prediction (recorded before the run): L fires 3 of 3.**

What L does NOT separate: within "consideration", whether A's zero is 34
non-fitting descriptions diluting attention or the planner register never
pausing to look. Both are consideration; neither is fit.

## Arm J0, extended

Round 1 bounded "the sentence does not manufacture a fit" with a **control of
n=1**. Two more J0 spawns (no sentence, no-fit task, full 35-skill corpus)
bring it to n=3 against J's n=3.

**Prediction: 0 of 2.**
