# Pre-registration — can an agent-mapped planned node invoke a skill?

Written and committed to **before any `claude` spawn, and after the argv
recording** — the arm table below quotes the recorded argv verbatim, which only
exists once the harness has run its shim (which spawns no `claude`). The branch
is one squashed commit, so git carries no ordering evidence for that; it is
stated here rather than left to be inferred. claude **2.1.226**, macOS, one
machine. (ADR 0017's numbers are 2.1.223/2.1.224; this is a third version, and
that is stated rather than smoothed over.)

The **US arm** in the write-up (TU, C1U — the same rows with the corpus in
`~/.claude/skills` and nowhere else) is **not** covered by this
pre-registration: it was added after review, once the write-up's "the user's own
settings-sourced corpus" was found to describe a project-scope plant. Its
prediction is the T and C1 rows below, unchanged.

## The row nobody has run

ADR 0017 §Compatibility excludes agent-mapped planned nodes from skill
activation and consoles itself:

> Note that under `nil` such a node already sees the user's real skills, so the
> exclusion costs it little.

`applyAgentMapping` (`internal/coordinator/agentmap.go:322-324`) sets **only**
`policy.SettingSources = nil`. It never touches `policy.Tools`, which
`toolPolicyFor` left at `narrowedToolsFor(node, false)` — the node's declared
tool names and **no `Skill`**. `applySkillActivation`
(`internal/coordinator/skillstage.go:787-790`) then `continue`s past the node,
so nothing ever adds it.

ADR 0017's Context table has `--setting-sources user` with no `--tools`
(skills run) and `--setting-sources user --tools Read,Skill` (skills run). It
has **no row where the definitions come from the user's settings and `--tools`
omits `Skill`.** Measurement (f) established the analogous row for the *plugin*
route — `--plugin-dir <dir> --tools Read` returns `NO-SKILL` — and the open
question is whether that carries over when the definitions arrive from settings
instead of from a staged plugin.

**Prediction (the thing being tested, stated so it can lose): it carries over.
An agent-mapped planned node holds no `Skill` tool, therefore invokes no skill,
therefore the exclusion is a capability hole and not a corpus preference.**

## The argv is reconstructed from the code, not from this document

`_harness/main.go` (`//go:build ignore`, run with `go run`) drives the real
objects:

- a canned `runner.NodeRunner` stands in for the planner and returns a fixed
  graph JSON, so `Coordinator.Plan` runs its real validation, its real
  `applyAgentMapping` and its real `applySkillActivation`;
- `Plan.BindSkillStaging(<runDir>)` stages the corpus exactly as `auto` does;
- each node is turned into a `runner.NodeInvocation` the way
  `schedule.Scheduler.buildInvocation` does (prompt, `dontAsk`,
  `node.Agent`, `plan.ToolPolicies[id]`);
- it is executed by the real `runner.ClaudeCLIRunner`, with
  `runner.WithBinary(<shim>)`. The shim writes its own `argv` to disk and exits.

So the recorded argv comes out of `runner.buildArgs`, not out of a shell script
someone transcribed. `replay.py` then re-executes that recorded argv against the
real `claude`, changing exactly one thing per arm.

## Evidence rule — a marker file and a raw tool_use record, never a sentence

The planted skill `omg-probe-standalone-html` (project scope, in the probe
workspace's `.claude/skills/`, and staged into the plugin dir for the
activated arm) has as step 1:

> create `OMG-PROBE-AGENTMAP-FIRED.txt` containing `OMG-AGENTMAP-4417-ZK`

**The node's only tool is `Write`.** No `Read`, no `Bash`, no `Glob`, no
`Grep`. The token `OMG-AGENTMAP-4417-ZK` exists only inside the `SKILL.md`
body, and there is no tool in the node's set that can read that file. So a
marker file carrying the token means the skill's *body* reached the model, and
the only route left is the `Skill` tool.

Two independent signals are recorded per spawn and both are reported:

1. **`skill_tool_use`** — the count of raw
   `{"type":"tool_use","name":"Skill"}` objects parsed out of that spawn's own
   `~/.claude/projects/**/<session-id>.jsonl`. **This is the verdict signal.**
2. **`marker`** — whether `OMG-PROBE-AGENTMAP-FIRED.txt` appeared with the
   right token. Corroborating.

A model's reply saying it used the skill counts for nothing and is not parsed.
The result text is stored only so a reader can see what the model claimed.

## Arms

All arms run in the same cwd, with the same planted skill, and with a prompt
that **names the skill outright**. Naming it is deliberate: this asks whether
the node *can*, not whether the description gate fires. ADR 0017's arm C
establishes that naming the skill fires under the activated argv, so a zero
here cannot be blamed on the description threshold.

| arm | argv | n | prediction |
|---|---|---|---|
| **T** | the recorded agent-mapped argv, verbatim: no `--setting-sources` flag at all, `--agent omg-probe-writer`, `--tools Write`, `--strict-mcp-config`, `--disallowedTools …` | 3 | **0 of 3** |
| **C1** | **T with `Skill` appended to `--tools`, and nothing else changed** | 3 | ≥2 of 3 |
| **C0** | bare `claude -p <same prompt> --output-format json --permission-mode dontAsk` — no ceiling flags, no `--agent` | 1 | 1 of 1 |
| **ACT** | the recorded argv of the *non*-agent-mapped node in the same plan: `--setting-sources ""`, staged `--plugin-dir`, `Skill` in `--tools` | 1 | 1 of 1 |

**C1 is the positive control the question needs.** It differs from T by one
token in one flag. **C0 and ACT are harness controls**: C0 says the planted
skill is discoverable in this workspace at all, ACT says the whole
coordinator→runner path still produces a working activated node on 2.1.226. A
bare "T fired nothing" proves nothing without them — this project has already
once "passed" a ceiling probe that only re-proved an undeclared tool does not
exist.

## What each outcome means, fixed in advance

- **T = 0/3, C1 ≥ 2/3, C0 and ACT fire** → the answer is **NO**: an
  agent-mapped planned node cannot invoke a skill, and the cause is layer 3
  (`--tools` without `Skill`), isolated to one token by C1. ADR 0017's "costs
  it little" is wrong on the capability, not merely on the yield, and the
  §Compatibility Update's own correction is still too weak.
- **T ≥ 1/3** → the prediction is **refuted**: `--tools` does not bind for
  skill definitions that arrive from settings, ADR 0017's sentence is right,
  and (f)'s finding does not generalize. Record it as a refutation, do not
  re-run it away.
- **C1 = 0/3** → the harness is broken or `--agent` itself blocks the tool.
  Nothing about T is reportable; report the broken control and stop.
- **C0 = 0/1 while ACT fires** → project-scope skill discovery is not what this
  probe assumed. T's zero would then be uninterpretable; re-plant the skill in
  the user's own `~/.claude/skills` and re-run T/C1/C0, recording the change.

## Cost bound

8 spawns. Stop and report at $6 spent whatever the state.

## What this probe does not answer

Whether *lifting* the exclusion is safe. The composite `--agent` +
`--plugin-dir` + `SettingSources = nil` stays unmeasured here; this probe only
establishes what the shipped exclusion costs the node it excludes.
