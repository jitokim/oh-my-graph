# An agent-mapped planned node cannot invoke a skill — the row ADR 0017 never ran

ADR 0017 §Compatibility excludes agent-mapped planned nodes from skill
activation and consoles itself that the exclusion is cheap:

> Note that under `nil` such a node already sees the user's real skills, so the
> exclusion costs it little.

That sentence is **false as a capability claim, and it is now measured false.**
An agent-mapped planned node holds no `Skill` tool, so it cannot invoke a skill
at all — the corpus is visible to the CLI and unreachable by the node. The
exclusion is a **capability hole**, not a corpus preference.

- **Date:** 2026-08-09 (KST). `claude` **2.1.226**, macOS (darwin 22.6.0), one
  machine. Note the version: every ADR 0017 number is 2.1.223/2.1.224, so this
  is a third build, and the two harness controls below are what license
  comparing across them.
- **Cost:** **$2.4118**, 10 spawns (8 pre-registered, plus the 2-spawn US arm
  added on review — see "The corpus was project scope"). Budget bound was $6.
- **Verdict:** **NO.** T fired 0 of 3; the one-token control C1 fired 3 of 3.
  With the corpus in the user's own `~/.claude/skills` and nowhere else: 0 of 1
  and 1 of 1.
- **Model:** `claude-opus-5[1m]` in every spawn's `modelUsage`, T and C1 alike —
  so the T/C1 difference is not a model difference.
- **Pre-registration:** `probes/0017-agent-mapped-skill-access/PREREG.md`,
  written before any `claude` spawn, with the prediction, the n, the arms and
  what each outcome would mean.
- **Scripts and raw evidence:** `probes/0017-agent-mapped-skill-access/`
  (`_harness/main.go`, `shim.sh`, `setup.sh`, `userscope.sh`, `replay.py`,
  `census.py`, `argv/`, `logs/`, `results.jsonl`, `plan-report.json`), and
  **`tool_use/`** — the raw `tool_use` records of all ten spawns, committed, so
  the verdict does not rest on a directory outside this repository.

## The argv came out of the code, not out of a shell script

The question is about one flag on one spawn, so transcribing the argv into a
probe script would have measured the transcription. `_harness/main.go`
(`//go:build ignore`, run with `go run`) drives the shipped objects instead:

```
coordinator.Plan            real validation, real applyAgentMapping,
                            real applySkillActivation
Plan.BindSkillStaging       the staged plugin dir, as `auto` binds it
buildInvocation             re-created field for field from
                            schedule/scheduler.go:1323
runner.CLIRunner.Run  runner.buildArgs — the thing under measurement
```

Exactly two things are substituted, and both are ends of that chain rather than
steps in it: the planner is a canned `NodeRunner` returning a fixed graph JSON,
and the claude binary is `shim.sh` (`runner.WithBinary`), which writes its own
argv and exits.

One plan, two nodes, identical prompts, both declaring `allowed_tools: [Write]`.
`omg-probe-writer` matches the planted agent under `agentmap`'s token rule and
is **agent-mapped**; `render-artifact` shares no token with it and is the
ordinary **activated** node.

**What `runner.buildArgs` emitted** (verbatim, `argv/*.argv.txt`):

| | agent-mapped `omg-probe-writer` | activated `render-artifact` |
|---|---|---|
| `--setting-sources` | **flag absent entirely** | `""` |
| `--plugin-dir` | absent | `<run>/skills-plugin` |
| `--agent` | `omg-probe-writer` | absent |
| `--allowedTools` | `Write` | `Write` |
| `--tools` | **`Write`** | `Write,Skill` |
| `--strict-mcp-config` | present | present |
| `--disallowedTools` | `Bash,Edit,MultiEdit,NotebookEdit,WebFetch,WebSearch,Task,Agent` | same |
| activation notice in `-p` | absent | present |

Two things this corrects about how the question was posed. **The agent-mapped
row is not `--setting-sources user`** — `applyAgentMapping` sets
`policy.SettingSources = nil` and `buildArgs` renders nil by *omitting the
flag*, so the CLI's own default (user **and** project **and** local) loads,
which is wider than `user`. And `Skill` is not in `deniableTools`, so layer 5
is not what is doing this; `--tools` is the only mechanism in play.

The code path, for a reader checking the claim rather than the argv:
`toolPolicyFor` sets `Tools: narrowedToolsFor(node, false)`
(`coordinator.go:619`); `applyAgentMapping` touches **only** `SettingSources`
(`agentmap.go:322-324`); `applySkillActivation` `continue`s past any node with
`node.Agent != ""` before reaching the `narrowedToolsFor(node, true)` line
(`skillstage.go:826-830`, in `applySkillActivation` — the +39-line comment block
this commit adds to that function moved it down from the 787-791 the
pre-registration cites). Nothing else can add the name.

## Evidence rule — a marker file and a raw record, never a sentence

The planted skill's step 1 is to write `OMG-PROBE-AGENTMAP-FIRED.txt`
containing `OMG-AGENTMAP-4417-ZK`. **The node's entire tool set is `Write`** —
no `Read`, no `Bash`, no `Glob`, no `Grep` — and the token exists only inside
the `SKILL.md` body, so no tool the node holds can read it. A marker file with
that token therefore means the skill's *body* reached the model, and the only
route left is the `Skill` tool.

The verdict signal is the count of raw `{"type":"tool_use","name":"Skill"}`
objects parsed out of each spawn's own `~/.claude/projects/**/<sid>.jsonl`. The
marker file corroborates. A model's reply claiming skill use counts for nothing
and is not parsed — which turned out to matter in the other direction here (see
the last section).

## Result

Every arm ran the **recorded** argv; an arm is a named single-token edit of it.

| arm | what varied from the recorded agent-mapped argv | n | `Skill` tool_use | marker | verdict |
|---|---|---|---|---|---|
| **T** | nothing — verbatim | 3 | **0** | 0 | fired nothing |
| **C1** | **`--tools Write` → `--tools Write,Skill`. Nothing else.** | 3 | **3** | 3 | fires |
| **C0** | all ceiling flags and `--agent` removed | 1 | 1 | 1 | fires |
| **ACT** | (the *activated* node's own recorded argv, verbatim) | 1 | 1 | 1 | fires |
| **TU** | nothing — verbatim, but the corpus is in `~/.claude/skills` only | 1 | **0** | 0 | fired nothing |
| **C1U** | **`--tools Write` → `--tools Write,Skill`**, same user-scope corpus | 1 | **1** | 1 | fires |

Full `tool_use` census per spawn, from the raw JSONL:

```text
ACT  9b7586ea  {'Skill': 1, 'Write': 2}   oh-my-graph-staged-skills:omg-probe-standalone-html
C0   1670de30  {'Skill': 1, 'Write': 2}   omg-probe-standalone-html
C1   d4e1dc53  {'Skill': 1, 'Write': 2}   omg-probe-standalone-html
C1   27f01af4  {'Skill': 1, 'Write': 2}   omg-probe-standalone-html
C1   35d39062  {'Skill': 1, 'Write': 2}   omg-probe-standalone-html
T    cf996342  {'Write': 1}
T    4907cba2  {'Write': 1}
T    9a4299e9  {'Write': 1}
TU   c44850e6  {'Write': 1}
C1U  d97154a1  {'Skill': 1, 'Write': 2}   omg-probe-standalone-html
```

That table is not transcribed by hand, and it no longer depends on a directory
this repo does not own. **The raw `tool_use` records are committed**, one file
per spawn, under `probes/0017-agent-mapped-skill-access/tool_use/` — the
verdict-bearing evidence itself, not a count derived from it. `T` files carry a
single `Write`; `C1` files carry a `Skill` naming the planted skill followed by
its writes. Anyone can read the finding off those ten files without this
machine.

`census.py` still re-derives the same table from `~/.claude/projects` while the
sessions survive, so the committed snapshot can be checked against its source
rather than trusted; it prints `gone` for a session that has aged out. The
first eight rows were recorded before `replay.py` carried a `tool_census`
field, and were backfilled from their transcripts on 2026-08-09 — each such row
says so in `tool_census_source`.

Only the `tool_use` objects are committed, with tool names and input KEY names
and nothing else: a full transcript would carry prompt and file content that
has no place in a public repository, and none of it is evidence for this
claim.

`permission_denials` was `[]` in all ten, T and TU included: the tool was never
**denied**, it did not **exist**. `num_turns` was 2 in every T spawn against 5
everywhere else — a node that wrote the file and stopped.

**Why the controls are the load-bearing half.** A bare "T fired nothing" proves
nothing; this project has already once "passed" a ceiling probe that only
re-proved an undeclared tool does not exist. C1 differs from T by six
characters in one flag value and fires 3 of 3, which isolates the cause to
layer 3 and rules out `--agent`, the permission mode, the deny list, the
prompt, the skill's description and the workspace — all of which C1 holds
constant. C0 says the planted skill is discoverable in this workspace at all.
ACT says the whole coordinator→runner activation path still works on 2.1.226,
which is what makes this comparable to ADR 0017's 2.1.223/224 numbers. The
`claude --version` behind that comparison is now recorded per spawn by
`replay.py` for TU and C1U; for the first eight it is this document's word,
taken from the same shell on the same day.

## The corpus was project scope — so the US arm ran it in user scope

`setup.sh` plants the skill in the probe workspace's `.claude/skills`
(**project** scope) and in the staging source. The agent-mapped argv omits
`--setting-sources` entirely, so the CLI default loads user **and** project
**and** local — the definition T could not reach was therefore a *project*-scope
one, and the first write-up of this measurement nonetheless called it "the
user's own settings-sourced corpus". Sloppy, and worth fixing by measurement
rather than by wording, for two reasons: ADR 0017's retracted sentence is
literally about "the user's real skills", and the C1 arm is being quoted as a
cheaper fix, which makes *which directory it resolved from* load-bearing.

**US arm (2026-08-09, added on review — not pre-registered).** `userscope.sh`
copies the same `SKILL.md` to `~/.claude/skills/omg-probe-standalone-html`,
**removes** the project-scope copy from the workspace, and reruns the recorded
agent-mapped argv (which carries no `--plugin-dir`, so settings are the only
possible source):

- **TU** — verbatim: `{'Write': 1}`, `skill_tool_use: 0`, no marker,
  `permission_denials: []`, `num_turns: 2`.
- **C1U** — the same one-token edit: `{'Skill': 1, 'Write': 2}`, resolving
  `omg-probe-standalone-html`, marker written with the token, `num_turns: 5`.

Same shape as T and C1 at n=1 each. The finding is now measured on the corpus
the retracted sentence was actually about. `userscope.sh clean` removes the
planted definition afterwards; nothing else in `~/.claude/skills` is touched.

## The answer

**No.** An agent-mapped planned node cannot invoke a skill. Measurement (f)'s
finding — *"without the name in `--tools` the definitions load and the skill
cannot run"* — carries over unchanged when the definitions arrive through the
node's nil layer 1 (user **and** project **and** local settings, the CLI's own
default) instead of from a staged plugin directory: measured at project scope
by T (0 of 3) and at user scope by TU (0 of 1). `--tools` bounds the tool set
the same way in every one of those worlds.

So the two mechanisms are not "activation versus the user's real corpus". They
are **activation versus nothing**, on exactly the nodes ADR 0017 §Context
identifies as the ones where a skill fits best. ADR 0017's 2026-08-07 Update to
that sentence already retracted "costs it little" on *yield* grounds — the
design/doc node was agent-mapped in both acceptance plans, so a pre-registered
skill could not be bound at all — but it kept the premise underneath it:

> Under `nil` an agent-mapped node sees the user's real skills *as a corpus*,
> but it is not the node this ADR is reasoning about

Half of that is now measured false. The node sees the corpus in the sense that
the CLI has loaded the definitions; it cannot reach a single one of them.

**What this does not settle.** Whether lifting the exclusion is safe. The
composite `--agent` + `--plugin-dir` + `SettingSources = nil` is still
unmeasured, and this probe deliberately did not touch it — it measured what the
shipped exclusion costs the node it excludes, which is the question that had
never been asked. Note also that a *fix* need not be that composite: C1 is
`--agent` + no `--plugin-dir` + `Skill` in `--tools`, measured to fire 3 of 3
over a project-scope corpus and 1 of 1 (C1U) over the user's own
`~/.claude/skills`.

**That alternative is cheaper, not free, and the residual is exactly the scope
its nil layer 1 makes reachable.** The same nil that lets it see
`~/.claude/skills` also loads **project** and **local** — and in production the
cwd is the target repository, not a probe workspace. So handing these nodes a
`Skill` tool makes a `SKILL.md` *committed to the repository being worked on*
invocable procedure text, on precisely the nodes measurement (g) already showed
lose the scope ceiling when layer 1 relaxes. C1/C1U measure that the mechanism
works; they measure nothing about whether that is safe. Sizing it as a change
belongs to whoever writes the follow-up, with its own ceiling re-verification
and a decision about repository-supplied definitions — dropping layer 1 is what
(g) breached, and nothing here re-opens that.

## One thing the evidence rule caught pointing the other way

Two of the three T spawns *volunteered* that the skill was missing —
*"`omg-probe-standalone-html` was never loaded and I had no way to call it. I
followed no procedure"* — and all three produced a plausible `design.html`
anyway. Had this probe scored the model's own account it would have reached the
right answer here by luck; had it scored the artifact, it would have reached
the wrong one, since every T spawn shipped the deliverable. Neither was
scored. (One T spawn also claimed it had folded in a "prior decision from your
session context" that was in no input this probe supplied. Unexplained, not
attributable to any tool it held, and not load-bearing for anything above.)

## Re-deriving this without the scratch directory

```sh
bash docs/measurements/probes/0017-agent-mapped-skill-access/setup.sh /tmp/omg-agentmap-skill
WS=/tmp/omg-agentmap-skill
AM=$(ls $WS/argv/omg-probe-writer/* | head -1)
ACT=$(ls $WS/argv/render-artifact/* | head -1)
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS ACT "$ACT" 1 verbatim
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS C0  "$AM"  1 bare
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS C1  "$AM"  3 add_skill
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS T   "$AM"  3 verbatim

# the US arm — the corpus in ~/.claude/skills and nowhere else
bash docs/measurements/probes/0017-agent-mapped-skill-access/userscope.sh plant $WS
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS TU  "$AM" 1 verbatim
python3 docs/measurements/probes/0017-agent-mapped-skill-access/replay.py $WS C1U "$AM" 1 add_skill
bash docs/measurements/probes/0017-agent-mapped-skill-access/userscope.sh clean

# the per-spawn tool_use census, read back out of the transcripts
python3 docs/measurements/probes/0017-agent-mapped-skill-access/census.py
```

`setup.sh` re-runs the harness, so the argv is re-derived from the code each
time rather than replayed from this record. The ten session ids in
`probes/0017-agent-mapped-skill-access/results.jsonl` are the ones the counts
above come from; their transcripts are under `~/.claude/projects/` for as long
as that directory keeps them, and `logs/` holds each spawn's argv and envelope
independently of it. `replay.py` records `claude --version` and the envelope's
`modelUsage` keys per row from now on; for the first eight, the version is this
document's word and the model is in each envelope under `logs/`.
