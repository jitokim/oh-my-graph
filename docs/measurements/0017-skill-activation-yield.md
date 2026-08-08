# ADR 0017 measurement (i) — why activation fired once in seven

The acceptance runs (`0017-skill-activation-acceptance.md`,
`…-run-2.md`) left activation wired, disclosed and yielding **1 `Skill`
invocation across 7 activated planned nodes**, cause unknown. This is the
record that explains it, and the record that had to correct itself once its own
raw data was read column by column.

- **Round 1** — 38 spawns, $6.15, 2026-08-07 (KST 2026-08-08), `claude`
  **2.1.223**, macOS (darwin 22.6.0). Pre-registration: `probes/…/PREREG.md`.
- **Round 2** — 6 spawns, $1.13, 2026-08-08, `claude` **2.1.224**, same
  machine. Pre-registration: `probes/…/PREREG-2.md`, written before any round-2
  spawn, prediction recorded and **wrong** (below).
- **Together** — 44 spawns, 19 activations, **$7.28**.
- Everything under `probes/0017-skill-activation-yield/`: the two runner
  scripts, every prompt file verbatim, the fixtures both tasks read, and
  `results.jsonl` — one line per spawn, session id included, from which every
  count in this file is derived.

## What is being judged, and by what

Each spawn reproduces the argv an **activated planned node** receives, taken
from `runner.buildArgs`, `toolPolicyFor`/`narrowedToolsFor`/`disallowedToolsFor`
and `applySkillActivation`+`BindSkillStaging`, with the environment scrubbed per
`internal/childenv.Scrub`:

```sh
claude -p <prompt> --output-format json --permission-mode dontAsk
       --setting-sources "" --plugin-dir <staged> --allowedTools <declared>
       --tools <declared+Skill> --strict-mcp-config --disallowedTools <denied>
       --session-id <uuid>
```

That is a **hand-reconstruction** and is labelled one. What binds it to the
shipped code is a second, independent artifact: `skillactivation_manual_test.go`
routes a real spawn through `runner.NewClaudeCLIRunner` under a real `Plan`'s
own `ToolPolicy` and asserts the policy fields, so the shipped path is checked
by construction rather than by transcription.

**Verdict rule, fixed in round 1 and unchanged:** a raw
`{"type":"tool_use","name":"Skill",…}` object in the node's own JSONL
transcript under `~/.claude/projects/`, **with its `skill` input recorded**, or
a planted skill's marker file appearing on disk. A model sentence claiming skill
use is not evidence and is ignored even when present.

## The arms

Corpora: **full** = the user's 35 real skills. **fullhtml** / **fullprobe** =
those 35 plus one planted skill whose description is written as a trigger for
the arm's task. **htmlonly** = exactly one skill, the user's real
`html-artifact`, copied byte-for-byte from `~/.claude/skills`.

Tasks: **T1** = "read ARCHITECTURE.md, produce design.html, a standalone
self-contained HTML rendering", in the planner's own register (terse, fully
specified, prescribing its own tool: *"Write the file with the Write tool"*).
**T2/T3** = release notes, planner register and plain statement. **T4** = count
lines mentioning a flag (no skill fits). **T5** = a verification node's
PASS/FAIL-with-list output contract. **T6/T7** = the ceiling probe.

| arm | task | corpus | sentence | n | fired | which skill |
|---|---|---|---|---|---|---|
| **A** | T1 | full (35 real) | no | 9 | **0** | — |
| **B** | T1, byte-identical + sentence | full (35 real) | yes | 9 | **8** | `html-artifact` ×8 |
| **H** | T1, byte-identical to A | fullhtml (35 + planted fit) | no | 3 | **3** | planted ×3 |
| **L** | T1, byte-identical to A | htmlonly (1 real: `html-artifact`) | no | 3 | **0** | — |
| **LC** | names the skill | htmlonly | no | 1 | **1** | `html-artifact` |
| **C** | names the skill | full | no | 1 | **1** | `html-artifact` |
| **D1** | T2 (planner register) | fullprobe (35 + planted fit) | no | 3 | **3** | planted ×3 |
| **D2** | T3 (plain statement) | fullprobe | no | 2 | **2** | planted ×2 |
| **F** | names planted skill, asks for its `references/` token | fullprobe | no | 1 | **1** | planted |
| **J** | T4 (no skill fits) | full | yes | 3 | **0** | — |
| **J0** | T4 | full | no | 3 | **0** | — |
| **K** | T5 (output contract) | full | yes | 3 | **0** | — |
| **K0** | T5 | full | no | 1 | **0** | — |
| **G** | T7 out-of-scope `touch` | full | no | 1 | 0 | — |
| **G2** | T6 in-scope `git status` | full | no | 1 | 0 | — |

**Which rows are comparable with which.** A↔B is single-factor: same corpus,
same prompt bytes, one appended sentence. A↔H is single-factor on the corpus:
same prompt bytes, one skill added. A↔L varies the corpus by subtraction
(35 real → 1 real, the one B chose). J↔J0 and K↔K0 are single-factor on the
sentence. D1↔D2 is single-factor on register. **C, LC, F and G/G2 are
controls, not comparisons** — they exist to show the corpus loads, the bundled
files resolve and the ceiling holds, and no row should be read against them.
**D shares neither task nor corpus with A** and is evidence only about itself.

Round-1 accounting, in full: 6 A + 3 Aout + 6 B + 3 Bout + 3 H + 1 C + 3 D1 +
2 D2 + 1 F + 3 J + 1 J0 + 3 K + 1 K0 + 1 G + 1 G2 = **38**. `Aout`/`Bout` are
the same arms run from an isolated working directory and are pooled into A and
B above; unpooled they are A 0/6 and 0/3, B 5/6 and 3/3. A 45th `.argv` file
exists in the raw logs for an invocation the CLI **refused before any model
call** (an empty prompt path — `Error: Input must be provided…`); it cost $0,
measured nothing, and is excluded.

**The ship decision was pre-committed, not chosen after the fact.**
`PREREG.md`'s stop rule, written before the first spawn: *change no code if a
genuinely fitting description activates without being named and B does not
materially beat A; change code only if B materially beats A — then the fix is
exactly the skill-agnostic sentence from trusted code, and nothing else.* B
beat A 8/9 against 0/9, and that second rule is what selected the sentence.
It does not, however, license the causal story the first version of this
record attached to it — see below.

## Finding 1 — the skill that fired in arm B is a real one, and it was in arm A

`results.jsonl` recorded the `skill` input of every `Skill` tool_use from the
first round. That column was never reported, and it is the one that matters:
**all 8 of arm B's activations named `html-artifact`** — one of the user's own
35 skills, whose description opens *"Use this skill when creating a standalone
HTML design document, research report, ADR, or plan as an 'artifact'…"*.

Arm A is the same corpus and the same prompt bytes. That description was
present, unmentioned and unconsulted, in all 9 of its spawns.

It is also the skill acceptance run 2 **pre-registered** as the expected match
for its HTML node, before spending anything. The prediction about *which* skill
fits was right the first time; what failed was that it was never weighed.

## Finding 2 — but the real description does not fire the gate unaided either

If B's unanimity meant `html-artifact` were a straightforward match, it should
fire without the sentence once the other 34 descriptions are gone. Round 2's
pre-registered prediction was that it would, 3 of 3.

**Arm L: 0 of 3.** Prompt byte-identical to A, no sentence, corpus reduced to
that one real skill. The prediction was wrong and is recorded as wrong.

Not a wiring failure: **arm LC**, the same one-skill corpus with a prompt that
names the skill, fired 1 of 1 and loaded the body. The corpus was there and
invocable; the gate did not open for it.

## What the two findings together do and do not license

They kill the clean version of both stories.

- **Not "the descriptions never arrive."** H, D1, D2, C, LC, F: they arrive,
  they are matched, and a planted trigger description fires the gate unaided
  from within a 36-skill corpus under the planner register. Reach and delivery
  are settled. ADR 0017's blocking measurement **(i) is answered**, and §4's
  ~6,008 tokens per invocation buy a block the model does read.
- **Not "the corpus simply had nothing that fits."** B's 8/8 on a real skill,
  and acceptance run 2's own pre-registration naming that same skill, say the
  corpus had a match for that task.
- **Not "the sentence merely made a good match visible."** L says the same
  real description, alone, still does not clear the gate unaided.

What survives is narrower and is the honest form of the claim: **the gate is a
threshold on how directly a description's trigger language matches the task,
and under the planner register it is applied without deliberation.** A
description written as a trigger for the task clears it unaided (H 3/3, D 5/5).
A real, genuinely topical, but broader description does not (A 0/9, L 0/3) —
and is then chosen unanimously the moment one sentence asks for a deliberate
look (B 8/9, all 8 the same skill).

So "the 1-of-7 is a fit number" is right only if *fit* means **marginal fit
that goes unconsidered**. It is wrong if it is read as "the corpus was
correctly judged to have nothing to offer" — which is how the phrase reads, and
which is what the previous version of this record implied. That reading is
retracted here.

**Still not separated, and not claimed:** whether A's zero is 34 descriptions
diluting attention or the register never pausing to look. L removes the
dilution and the zero survives, which points at the register — but L also
changes the corpus, so it is one arm and not a decomposition. No transcript in
A or L mentions a skill, a procedure or the corpus anywhere in its assistant
text; that is consistent with "never considered" and is not proof of it, since
nothing asked these nodes to narrate.

## Finding 3 — the sentence does not manufacture a fit

| | no sentence | sentence |
|---|---|---|
| T4, no skill fits | J0 **0 of 3** | J **0 of 3** |
| T5, output contract | K0 **0 of 1** | K **0 of 3** |

Round 1 ran the T4 control **once**; a bound carried by one spawn is not a
bound, so round 2 added two more. J0 is now 0 of 3 against J's 0 of 3. The
sentence lowers a threshold; it does not remove the gate.

T5 is the sharper limit, and it is a limit on the remedy rather than a
reassurance: on a node whose prompt is an output contract (*reply `PASS`, else
`FAIL` and a numbered list*), the sentence moves nothing — 0 of 3 with it, 0 of
1 without. Verification nodes are a large share of a planned graph. **K's
control is n=1 and is not strengthened here.**

## What it costs

The sentence is ~20 tokens, but a node that consults a skill then reads it and
follows it. Mean cost per spawn: **A $0.134, B $0.205** (+53%); in-cwd only,
$0.105 → $0.181. That is on top of §4's ~6,008 prompt tokens per invocation,
which is paid whether or not anything fires.

## What is still unmeasured

**ADR 0017 measurement (e) — does an activated skill improve the node's
output?** Unanswered, and deliberately not asserted. On the one task where the
deliverable could be checked mechanically, arms A and B were
indistinguishable: 6 of 6 met every structural requirement of the node's own
prompt and none wrote outside its cwd. The 8 `html-artifact` invocations bought
a procedure the node then followed; whether the resulting `design.html` is
better than arm A's was not graded, and the +53% is therefore a cost with no
measured benefit beside it. (e) was blocked behind (i) for want of nodes that
activate at all; that block is now gone.

**Not reproduced anywhere else.** One machine, one corpus, two adjacent CLI
builds. Nothing here is known to hold on a second.

**Erratum on `PREREG.md`'s free H1 discriminator.** That sealed document argues
tool starvation cannot explain the 1-of-7 because *"`artifact` (hit) and
`make-html` (miss) both had Write"* — while the table directly above it records
run 1's declared tools as `(unrecorded)`. No retained record establishes run 1's
tool lists, so that same-tools comparison is **not established**, and the
pre-registration is left as written rather than amended. Nothing in this record
rests on it: the arms reported here hold tools constant across the comparisons
they license — A↔B varies one appended sentence, A↔H one corpus entry — so the
0-vs-fired splits above are not tool differences whatever run 1's tools were.

**Measurement (b)** — whether a skill that executes in a subagent routes around
layer 5 — is untouched by this record.

## The ceiling, re-verified under this argv

Round 1: a node declaring `Bash(git *)` attempted `touch <path>` out of scope
(**G**); the file did not appear. The in-scope control (**G2**, `git status
--porcelain`) ran. Round 2 re-ran the shipped guard on 2.1.224 through the real
`ClaudeCLIRunner`, judged by file existence, output recorded verbatim at
`probes/0017-skill-activation-yield/manual-guard-2.1.224.txt`:
`TestManual_SkillActivationStillFires` (treatment and control) and
`TestManual_SkillActivationCeilingHolds`, all PASS, $0.248 for the three
spawns. Layer 1 is untouched.

## The `references/` question, stated at its real strength

Arm F named a planted skill that bundles `references/wording.md` containing a
token, under a policy granting no file-reading tool, and the token came back in
the reply. That is **one spawn with no control**: nothing establishes the token
could not have been reproduced another way, and the arm was never re-run. It is
evidence that bundled files resolve through the CLI's own progressive
disclosure, at n=1, and it is not a closed question.

## Per-spawn record

`skill` names are shown without the `oh-my-graph-staged-skills:` plugin prefix
every one of them carries. `marker` is the planted skill's audit file; it can
read `no` on a fired spawn when the skill was invoked but its first step was
not executed (D1 `dc7f283e`).

| arm | session | Skill calls | skill | marker | $ |
|---|---|---|---|---|---|
| A | `3d2269a6` | 0 | — | no | 0.1701 |
| A | `3dec364d` | 0 | — | no | 0.1609 |
| A | `96a8139f` | 0 | — | no | 0.0737 |
| A | `a3899c0a` | 0 | — | no | 0.0754 |
| A | `e70b571b` | 0 | — | no | 0.0762 |
| A | `e9bd1a2c` | 0 | — | no | 0.0721 |
| Aout | `248c1a07` | 0 | — | no | 0.1940 |
| Aout | `39c5ece7` | 0 | — | no | 0.1887 |
| Aout | `97a2ce32` | 0 | — | no | 0.1951 |
| B | `50fce47d` | 1 | html-artifact | no | 0.2339 |
| B | `533b5eb9` | 1 | html-artifact | no | 0.1494 |
| B | `6fe4a194` | 1 | html-artifact | no | 0.2359 |
| B | `dcde52d3` | 1 | html-artifact | no | 0.1329 |
| B | `ef14c264` | 1 | html-artifact | no | 0.1447 |
| B | `ef556c9a` | 0 | — | no | 0.1879 |
| Bout | `26a3a2f6` | 1 | html-artifact | no | 0.2480 |
| Bout | `2e602e66` | 1 | html-artifact | no | 0.2505 |
| Bout | `60c768b5` | 1 | html-artifact | no | 0.2591 |
| H | `081f6866` | 1 | omg-probe-standalone-html | yes | 0.1920 |
| H | `4d291c48` | 1 | omg-probe-standalone-html | yes | 0.1099 |
| H | `9b935712` | 1 | omg-probe-standalone-html | yes | 0.1101 |
| L | `0a23c5f9` | 0 | — | no | 0.1622 |
| L | `1b2f7d0f` | 0 | — | no | 0.1084 |
| L | `a1b793e3` | 0 | — | no | 0.1035 |
| LC | `0f701698` | 1 | html-artifact | no | 0.7191 |
| C | `3975ac47` | 1 | html-artifact | no | 0.8432 |
| D1 | `3732f16a` | 1 | omg-probe-release-notes | yes | 0.1506 |
| D1 | `6a392216` | 1 | omg-probe-release-notes | yes | 0.0545 |
| D1 | `dc7f283e` | 1 | omg-probe-release-notes | no | 0.0709 |
| D2 | `4f6a24b6` | 1 | omg-probe-release-notes | yes | 0.0659 |
| D2 | `688c795d` | 1 | omg-probe-release-notes | yes | 0.1480 |
| F | `2be31dab` | 1 | omg-probe-release-notes | no | 0.1167 |
| J | `08ed9a6f` | 0 | — | no | 0.0216 |
| J | `4caa2e68` | 0 | — | no | 0.0208 |
| J | `f1b5983f` | 0 | — | no | 0.1004 |
| J0 | `382cebc7` | 0 | — | no | 0.1430 |
| J0 | `2b8557cc` | 0 | — | no | 0.0157 |
| J0 | `71cae263` | 0 | — | no | 0.0189 |
| K | `2712e0cd` | 0 | — | no | 0.1281 |
| K | `73c32cc5` | 0 | — | no | 0.2254 |
| K | `a25ed3ca` | 0 | — | no | 0.1052 |
| K0 | `a28bea17` | 0 | — | no | 0.2221 |
| G | `60138ad1` | 0 | — | no | 0.1778 |
| G2 | `588a75c1` | 0 | — | no | 0.0983 |

## Reproducing this

The transcripts live in the ordinary place — every spawn ran with session
persistence on, so `~/.claude/projects/-private-tmp-omg-yield-*/<session>.jsonl`
is a normal Claude Code session transcript. The skill a spawn invoked:

```sh
jq -r 'select(.type=="assistant") | .message.content[]?
       | select(.type=="tool_use" and .name=="Skill") | .input.skill' <session>.jsonl
```

The scratch tree (`/tmp/omg-yield`) is not durable. What is durable is
`probes/0017-skill-activation-yield/`: `probe.sh` / `probe2.sh` (the second
takes the working directory as `$7` — that is the only difference, and it is
what the `out` arms used), every `prompt*.txt` byte-for-byte as spawned, the
`ARCHITECTURE.md` and `CHANGES.txt` the tasks read, both pre-registrations, and
`results.jsonl`. The corpora are rebuilt from `~/.claude/skills` plus the two
planted definitions under `planted-skills/` (and its `references/wording.md`,
which is arm F's whole subject) — each dropped at
`<plugin>/skills/<name>/SKILL.md` beside a `.claude-plugin/plugin.json`
identical to the one `pluginManifestJSON` writes.
