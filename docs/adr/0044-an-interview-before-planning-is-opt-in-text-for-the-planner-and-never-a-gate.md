# ADR 0044 — An interview before planning is opt-in text for the planner, and never a gate

**Status:** Proposed. Decision record only — no flag, no subcommand, no schema
key, no new seam, no behaviour change. The captain decides after review. If it
is accepted, the implementation is a separate change that owes the tests in §5.

**Where the addresses point.** Written against `228ba1a`, the `main` this
branch (`docs/adr-0044-interview`) left. Every `file:line` below was read at
that commit. The Decision describes code that does not exist yet, so it names
file **and** function: the line goes stale when the diff lands, and the name
does not. Constants this ADR chooses (§2.3) are marked *chosen*: they are
design choices with one named home each, not measurements.

**Date:** 2026-10-06

**Issue:** [#319](https://github.com/jitokim/oh-my-graph/issues/319). `auto`
plans from a one-line goal and nothing else; the operator has no point at which
the planner is told what the goal left out.

## 1. Context

### 1.1 The captain's direction

On 2026-10-06 the captain said he no longer uses `auto` himself.
He first runs an interview in his own harness, Socratic questions that surface
hidden assumptions and edge cases, then writes the DAG by hand, and he says the
design comes out more thorough that way. His direction, which this ADR takes as
its constraint:

- both entry points are good: an interview in front of `auto`, and an interview
  that ends in a graph a human reviews;
- **if `auto` gets an interview there MUST be an off mode**, so that agents can
  still run `auto` lightly and non-interactively.

The off mode is not a courtesy. `auto` is non-interactive by contract: a plan
that validates runs (`planAndExecute`, `cmd/oh-my-graph/main.go:681`), and `auto` reads nothing from a
keyboard today. Agents call `auto` from scripts and
from other agents. An interview that is on by default turns every one of those
calls into a wait on a keyboard nobody is at.

### 1.2 What the planner receives today

The planner prompt is the goal, the `--input` keys and, from cycle 2 of a goal
loop, a fenced quote of the assessor's `remaining`. `(*Coordinator).plan`
(`internal/coordinator/coordinator.go:494`) builds it once as `base`
(`:507`, `plannerPromptFor` at `:556`). A bounded repair attempt resends
`base` plus the validator's refusals (`:542`). The continuation is appended
inside a nonce fence (`:571`, `plannerContinuationTemplate` at `:2022`).

Nothing the operator knows and did not write in the goal reaches it. The
operator's conventions do not reach it either: `--conventions` is a prefix on a
planned **node's** prompt (`withConventions`,
`internal/schedule/scheduler.go:1244`; wired at `cmd/oh-my-graph/main.go:1066`),
not on the planner's.

### 1.3 The planner call is unisolated, on purpose

`coordinatorInvocation` (`internal/coordinator/coordinator.go:464`) gives the
planner read-only permission mode, no tools and the deny list of a node that
declared nothing, and sets no `SettingSources`. ADR 0032 §1.4 records why: the
planner's job includes reading the repository's `CLAUDE.md`, so it runs under
the operator's configuration. Under `--accept-loaded-user-config` the planned
nodes join it; without that flag the planner is the one unisolated call that
writes the plan. An interviewer is another coordinator-owned call, so it inherits
this stance and widens nothing.

### 1.4 What the closest precedent settled, and what it did not

ADR 0041 settled how operator-supplied **text** reaches a planned node without
becoming settings: a staged prefix, hashed in `state.json`, re-checked on
`resume`, with no grants (`internal/conventions`, `MaxStagedBytes` at
`internal/conventions/conventions.go:46`). It treats that text as trusted: the
header names it as instructions and it is deliberately not fenced
(`conventions.go:54`). It reaches nodes, not the planner.

Interview answers differ on both counts: they are for the planner, and they are
**untrusted** (§2.2). They take 0041's staging and record, and 0043-style
grammar for the interviewer's replies, and replace its trust model with the
fence the planner's other quotes already use.

### 1.5 The 1.0.0 gate's two conditions

The captain's 1.0.0 gate has two conditions. Condition (1) is a hands-on
acceptance test in three parts: ① a graph made of graphs, ② produced by `auto`
and not hand-written YAML, ③ judged on the captain's real work, both its output
and its process. Condition (2) is the captain's confirmation. Any new way of
producing a graph has to say which side of part ② it is on (§2.7).

## 2. Decision

### 2.1 One interview component, two entry points

**Decision.** There is one component, `internal/interview`, and two callers.

**(a) `auto --interview`, off by default.** Without the flag `auto` behaves as
it does today, byte for byte, in what reaches the planner: the interview prefix
is the empty string and `plannerPromptFor` output is unchanged (test 1, §5).
With the flag, after the existing refusals that cost nothing (flag parse, the
`--conventions` load at `main.go:479`, the build-evidence question, the CLI
check) and before the first model call, `runAutoWithRuntime` (`main.go:464`)
runs the interview, then plans as usual.

- **No TTY on stdin.** `--interview` with a stdin that is not a character device
  is refused with an error that names the flag and the way out ("drop
  `--interview`, or run it from a terminal"). The check is the stdlib one
  `isTerminal` already uses for stdout (`cmd/oh-my-graph/liveview.go:31-34`),
  applied to stdin, and it runs before any model call, so a refusal costs
  nothing and never hangs. There is no `--interview=force`. `/dev/null` is a
  character device and passes that check; so an end-of-input before the first
  answer, with no explicit stop, is refused the same way (§2.3), never read as
  "no questions needed".
- **With `--plan-only`.** Allowed. `--plan-only` exists so that what it shows
  is what a real run would show (`planAndExecute`'s comment, `main.go:662-680`),
  and a preview that skipped the interview would show a different plan from the
  run. The interview runs, the planner runs once with its prefix, the plan
  prints and the process stops. The staged answers go beside the saved spec
  under `plans/<id>/` (ADR 0023 §3), not under `runs/`. `run <graph.json>` does
  not carry them and does not need to: the plan has already absorbed them, the
  same way ADR 0041 §2.6 says a `--plan-only` graph does not carry
  conventions. The existing refusal of `--plan-only` with `--max-cycles` above
  `1` stands (`cmd/oh-my-graph/flags.go:266`).

**(b) `oh-my-graph design "<goal>" --out <file>`.** The interview, then the
planner, then a graph written to `<file>` as YAML, then the same lint `oh-my-graph lint`
runs (`lintGraphForRuntime`, `cmd/oh-my-graph/lint.go:80`) on the file just
written. It **never runs the graph.** No node spawns, no run directory exists,
and the human reviews the YAML and runs it with `run`. `design` has no off
mode: the interview is the command. It requires a TTY on stdin on the same
terms as (a). `--out` is required and an existing file is refused, so no
default path can overwrite a graph the captain wrote. A lint failure leaves the
file in place, names it, and exits non-zero, so the human can see what the
planner wrote. `design` accepts no `--conventions`: it spawns no node, so
nothing would carry them, and a flag that silently does nothing is refused
(ADR 0041 §2.6 does the same for `run` and `chat`).

A file that `design` writes and the human then runs goes through `run`, which
holds a hand-written graph to the hand-written rules, not the planned-node
ceiling. That is the existing `--plan-only` then `run` path (ADR 0041 §1.2) and
this ADR does not widen it: the human's review of the file is the control, and
the lint output is what that review reads.

**(c) The same component for both.** `internal/interview` takes:

| input | what it is |
| --- | --- |
| goal | the operator's goal text |
| asker | a function `(ctx, prompt) -> reply text, accounting`, supplied by the coordinator so the package imports no runner |
| in, out | the terminal's reader and writer (the seam `runChatWith` already takes, `cmd/oh-my-graph/chat.go:84`) |
| limits | the constants of §2.3 |

and returns a `Result`: the ordered question and answer pairs, the reason it
ended (`enough`, `cap`, `operator`, `repeat`, `malformed`, `eof`), counts of
asked, answered and skipped, the summed cost and usage, and a method that
renders the staged text (§2.2). It does not call the planner and does not know
whether it was started by `auto` or by `design`.

**Who asks.** One interviewer call per question, each a fresh, stateless
`coordinatorInvocation` (`coordinator.go:464`): read-only permission mode, no
tools, the deny list, **unisolated** (no `SettingSources`), the same stance as
the planner (§1.3). Unisolated because the interviewer has to know what the
repository already says (its `CLAUDE.md`, its conventions) so that it does not
ask what the repo answers; isolating it would drop that context for the one
call whose job is to find what the context does not contain. This is the
reasoning `attemptPlan`'s comment gives for the planner. Each call carries the
goal and the fenced transcript so far (§2.2), so no session is resumed and a
failed call costs one call. The ceiling is the planner's: a coordinator-owned
call with no grant, and `--accept-loaded-user-config` does not touch it. Its
cost is summed and reported with the plan, never dropped from the ledger
(`Plan.CostUSD`, `coordinator.go:160`, is the figure that exists to be the sum).

**Reply grammar.** The interviewer's reply is parsed by trusted code against a token grammar of the kind ADR 0043 gave
review verdicts, matched as whole words as the #309 fix (`87d17ed`) does for `CLEAN`:
`QUESTION: <one question>` or `ENOUGH`. Anything else ends the interview as
`malformed`. The engine does not retry a malformed reply: a model that cannot
keep to a two-token grammar is out of useful questions.

**Rejected.**
- *Interview on by default with a `--no-interview` off switch.* It changes
  every existing `auto` invocation, which breaks "byte for byte" (ADR 0032 §6
  holds the same rule for a run that types nothing) and turns every script and
  agent call into a hang or a refusal.
- *Interview only as `design`.* It drops the captain's other entry point and
  the goal loop's ability to run what the interview shaped.
- *Interview only as `auto --interview`.* It leaves the captain's actual
  workflow (interview, review the DAG, run) without an artifact to review.
- *The planner asks its own questions mid-call.* `-p` is one non-interactive
  turn, the planner's reply must be a JSON object that `graph.Parse`
  (`internal/graph/graph.go:406`) accepts, and nothing could bound, dedupe or
  record the questions.
- *The interviewer as an isolated call (layer 1, like the assessor).* It would
  ask what `CLAUDE.md` answers.

### 2.2 How answers reach the planner: as text, fenced, and recorded

**Decision.** The answers reach the planner as **text only**: a staged prompt
prefix in the same shape as ADR 0041's conventions. They are never settings,
tool grants, paths or permissions. `toolPolicyFor` is not touched, and neither
is `coordinatorInvocation`.

**Untrusted.** A human typed an answer, perhaps pasting a log or a web page;
the interviewer's question is model output. Both are untrusted input, so the
prefix is **fenced**, not trusted as 0041's conventions are. Enforcement is the
mechanism the planner's other quotes already use: a per-call nonce from
`fence.Nonce` (`internal/fence/fence.go:61`) carried in both markers, minted
after the text is fixed, so no answer can contain it; the quoted block is
labelled DATA, not instructions; and the header, which is engine-authored and
outside the fence, says the answers are context and the rules after the block
govern. `plannerContinuationTemplate` (`coordinator.go:2022`) is the shape to
copy. The only text outside the fence is engine-authored: the header and, when
the interview ended early, the line that says so (§2.3). Each question, which
is model output, is bounded with `fence.Truncate` (`fence.go:147`) at the bound
the `remaining` quote already uses (`maxRemainingInPrompt`, `assess.go:34`).
An answer is held to the same bound (`internal/coordinator/assess.go:34`), but
is never truncated: one over it is refused at the prompt (§2.3), because a
human's text silently cut is a premise the human did not give.

**Where it sits.** Before the planner instruction, as 0041 puts conventions
before a node's prompt, so the engine's own rules and the JSON-only reply
requirement stay the last words the planner reads. It is applied to `base` in
`(*Coordinator).plan` (`coordinator.go:507`), through a coordinator option of
the kind `WithVerifyCommand` is, so a repair attempt (`:542`) and a cycle-2
continuation (`:571`) carry the same prefix: a repair that re-planned without
the answers would answer a different question from the one refused. The
interview text never enters a planned node's prompt. The planner turns what
matters into the graph and into node prompts, and a node is not handed text a
human pasted and a model did not vet.

**Recorded for resume.** The answers are staged into the run directory as
`interview.md`, owner-only, through the pattern `(*Set).Stage`
(`internal/conventions/conventions.go:320`) and `LoadStaged` (`:333`) already
are. `state.json` records a block beside `conventions`
(`runstate.Conventions`, `internal/runstate/runstate.go:562`): the SHA-256 of
the staged file, the counts, the reason it ended and the interview's cost. It
does **not** record the answer text: the recorder rewrites the whole snapshot on
every node settle and a feed consumer reads it, so the text lives in the staged
file and only its hash is in the snapshot. A run with no interview writes
nothing new, which keeps its snapshot as it is today; how a record moves the
schema stamp follows `SchemaWithConventions`
(`runstate.go:69-77`), and this ADR changes no schema.

**How a resumed run re-uses them instead of asking.** `resume` never plans:
`cmd/oh-my-graph/resume.go` makes no planner call, so the interview has nothing
to ask for. What a resumed leg owes is the record. It carries the block forward
unchanged, as it does for conventions (`resume.go:641`; the recorder rewrites
the whole snapshot, so an omitted field is erased), and it re-checks the staged
file against the hash (`resumedConventions`, `resume.go:415` is the pattern),
refusing a missing or altered copy rather than carrying a record whose text no
longer matches. Within one process, every cycle of a goal loop gets the same
bytes from memory (§2.4); it never re-reads a terminal.

**Rejected.**
- *Trust the answers, as conventions are trusted.* The operator wrote their
  conventions on purpose, in a file, before launch. An answer is typed live in
  response to a model's words and may be a paste.
- *Put the answers into every node's prompt like conventions.* It is a bigger
  trust crossing than the one 0041 weighed (unvetted text into every spawn) for
  no benefit the planner does not already give.
- *Record the text in `state.json`.* See above.
- *Ask again on resume.* A resume never plans, and re-asking would give the
  resumed leg a different premise from the leg it continues.

### 2.3 Ending the interview

**Decision.** Four ways to end, all explicit, and all leave a record of why.

- **Hard cap: five questions** (*chosen*; one named constant in `internal/interview`, not a flag like `--max-cycles`, `flags.go:193`).
  Each question is one paid call before the planner has produced anything; the
  cap makes the interview's worst case, five interviewer calls, a number printed up front, as `--max-cycles` prints the planner's (`flags.go:193`).
  Five is also the bound the captain's deep-interview skill sets on its first phase ("at most five" questions under "Phase 1: understanding the context"), the part of it that matters here.
  After the fifth answer no sixth call is made; test 6 (§5) asserts exactly five calls.
  There is no flag to raise it: a knob that unbounds a paid loop needs its own
  ADR, the way `--max-cycles` needed one (ADR 0011).
- **"Good enough, stop" at any time.** At every prompt, including the first,
  the operator can type a line that is exactly `/done`. The interview ends with
  the answers collected so far. `/skip` skips this one question: it counts
  against the cap and is recorded as skipped.
- **The interviewer's own `ENOUGH`.** It ends the interview as `enough`. It is
  a judgement by a model, so it is a stop and never a pass: nothing is waiting
  on it (§2.5).
- **End of input.** Ctrl-D after at least one answer ends it as `eof`. Before
  any answer it is refused (§2.1(a)). Ctrl-C is the ordinary interrupt: no run
  directory exists yet, nothing is recorded and nothing resumes.

**No question is asked twice.** Detection is exact match on a normalised form:
the question lower-cased, everything that is not a letter or digit dropped and
whitespace collapsed, compared with every earlier question in this interview.
That is deliberately cheap and deterministic: no similarity model, which would
be one more call to trust. A repeat ends the interview as `repeat` instead of
costing another call; a model that repeats itself has run out of questions. The
interviewer's prompt also carries the transcript and says not to repeat; the
match is the backstop for when it does. It follows that the interviewer makes
at most as many calls as the cap allows, five, never one per repeat (test 7).

**Answer size.** An answer over the per-answer bound of 2000 bytes (`maxRemainingInPrompt`, `assess.go:34`) is refused at the prompt
with its size and asked for again; nothing is truncated (the rule
`--conventions` already holds, `flags.go:198`).
The staged text is capped at 24 KiB (*chosen*): the worst case is five questions and five answers of at most 2000 bytes each (`assess.go:34`), `5 × 2 × 2000` = 20000 bytes before the header and fence lines, which fits with margin.
The cap sits below the 96 KiB of `MaxStagedBytes` (`conventions.go:46`) and the 131072-byte single-argv-string limit that constant is sized against (`conventions.go:36-45`), so the planner prompt keeps its room.

**What the planner receives when it is cut short.** The answers collected so
far, and an engine-authored line in the header, outside the fence, naming the
reason (`cap`, `operator`, `repeat`, `malformed`, `eof`) and how many questions
were asked and answered, so the planner does not read a partial interview as a
complete one. With no answers at all (the operator typed `/done` first), the
prefix is the empty string and the planner prompt is byte-identical to a run
without `--interview`; `state.json` still records that an interview was asked
and ended `operator` with zero answers, so the measurement in §2.5 can count it.

**Rejected.**
- *A confirmation step after the interview ("is this summary right?").* It is
  one more round and one more wait; the interview's output is printed once
  before the planner call so the operator sees what the planner will receive,
  and Ctrl-C is the abort.
- *No cap, stop only by `/done`.* A paid, model-driven loop with a human as its
  only bound.
- *A model-judged "same question" test.* One more call to trust, and nondeterministic.

### 2.4 How it meets the existing contracts

**`--conventions` (ADR 0041).** The two prefixes reach different calls: the
interview prefix reaches the planner, the conventions prefix reaches planned
nodes (`withConventions`, `scheduler.go:1244`), so in today's code they never
concatenate and there is no shared size limit. Each keeps its own: conventions
the 96 KiB of `MaxStagedBytes` (`conventions.go:46`), the interview the 24 KiB
of §2.3. If a later ADR sends conventions to the planner too, the order is
conventions first and interview second: standing operator text outermost, this
run's untrusted text innermost, nearest the fence that contains it; and the
two must then share the single-argv-string limit that `MaxStagedBytes` is sized
against (`conventions.go:36-45`), which that ADR would have to account for.
Their combined ceiling is 96 KiB + 24 KiB = 120 KiB of that 131072-byte (128 KiB) element (`conventions.go:36-46`), leaving 8 KiB for the planner's own prompt, so that ADR must lower one of the two caps rather than add them.
This ADR does not make that change. `auto --interview --conventions a.md` works
and both are staged; `design` refuses `--conventions` (§2.1(b)).

**`--max-cycles` and the goal loop.** The interview runs **once per goal**,
before cycle 1, not once per cycle. Cycle k ≥ 2 (`internal/coordinator/goal.go:219`) already has a better question
answered for it than a human could: the assessor's `remaining`, grounded in a
run that happened (`c.plan(ctx, goal, opts.InputKeys, remaining)`). Re-asking at each boundary would repeat
questions the operator already answered (§2.3 forbids that) and would put an
unattended loop back on a keyboard at its second cycle. Every cycle's planner
prompt gets the same prefix from memory, and every cycle's run directory stages
its own copy, as `main.go:891-899` does for conventions. The interview's cost
counts toward the goal's spend, so `--max-goal-budget-usd` (`flags.go:194`) is
checked against a total that includes it: the goal summary must print the
multiplier, not leave it derivable (ADR 0011 §4).

**The gate (ADR 0039).** The interview is not a gate and adds none. ADR 0039
holds that a gate is authored, never attached: no `type: gate` node is spliced
by the engine, and `auto` refuses a planned gate. The interview is not a node
of the planned graph: it runs before planning, in the CLI, and what the planner
writes is validated and run exactly as it is without it. It does not pause a
run, does not block a plan from running and does not score readiness. A
deep-interview-style "ambiguity score" that decides whether work may begin
would be exactly an attached gate, and is dropped (§2.6).

**The 1.0.0 gate.** A graph from `oh-my-graph design` does **not** count as
"`auto` produced it" for condition (1), part ② (`auto` produces it, not
hand-written YAML). `design` ends in a file a human
reviews, may edit and then runs with `run` (§2.1(b)), so the engine cannot say
afterwards whose graph it is, and the condition asks for the opposite of a
reviewed-and-edited file: `auto`, on real work, "not hand-written YAML". A graph
that `auto --interview` plans and runs does count: the planner wrote it and a
human supplied only text, which is the case this ADR was written to enable. The
captain's judgement on his own work (condition (1) part ③ and condition (2)) is
unchanged; `design` adds a way to reach good graphs and takes nothing from, or
gives nothing to, part ②.

### 2.5 Measurement

The question is whether an interview lowers plan rework and the verify fail
rate per cycle. The baseline is the 2026-10-05 baseline of auto cycles on this
Mac (14:00Z-23:59Z, its header line, `grep -m1 '^#'`): one row per cycle with
the run id, the run outcome, the assessor's verdict and the goal's first line.
It has 26 rows (`grep -vc '^#'`) for 18 goals (`grep -c '^[0-9.-]*-1 |'`) (a `-1` run id is a goal's first cycle).
Each row's run directory is still on this Mac under `~/.oh-my-graph/runs/<run-id>/`,
named with the same `-1`/`-2` suffix, holding `state.json`, `events.jsonl`,
`graph.json` and, where the assessor ran, `assess.json`. Where the row summary
is not enough, the figures below are read from those records, and each says so.
These are the only numbers in this ADR that come from a run.

**What the row summary gets wrong.** Read against run state, two of its
columns do not mean what its header says, so neither is used on its own:

- The verdict column is blank for "not `true`", not for "no `assess.json`": 20 of 26 run directories hold an `assess.json` (`ls ~/.oh-my-graph/runs/<run-id>/assess.json`), 6 with `goal_met: true` (`grep -c '| true |'`) and 14 with `goal_met: false`.
- The 6 of 26 without an `assess.json` are run ids 20261005-183114.023251000-1, 20261005-200252.888896000-1, 20261005-210007.410977000-1, 20261005-210211.990080000-1, 20261005-210721.943676000-1 and 20261005-210954.405845000-1.
- The outcome column records whether a `failed/` marker directory exists, not the outcome the engine recorded, and they differ on 1 of 26 rows: run id 20261005-200252.888896000-1 is PASSED in the column, but its `events.jsonl` ends in `run_finished` with `outcome: failed` (a node killed by `context canceled` before it replied, so no marker was written).
- So the engine's own run-level count is 10 of 26 failed (`grep -c '| FAILED |'` gives 9, plus run id 20261005-200252.888896000-1).

**Which goals can show rework.** `state.json`'s `goal` block (`GoalRef`,
`internal/runstate/runstate.go:341`, `max_cycles` at `:347`) says how each goal ran:

- 13 of 18 goals ran a goal loop with `max_cycles: 2`, first-cycle run ids 20261005-141210.629450000-1, 20261005-173231.808441000-1, 20261005-175230.634206000-1, 20261005-183113.286701000-1, 20261005-192555.894236000-1, 20261005-193759.110385000-1, 20261005-195025.097992000-1, 20261005-200252.888896000-1, 20261005-204449.000862000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1, 20261005-231953.292617000-1 and 20261005-234213.753108000-1.
- 5 of 18 ran as one plain cycle with no `goal` block, run ids 20261005-183114.023251000-1, 20261005-210007.410977000-1, 20261005-210211.990080000-1, 20261005-210721.943676000-1 and 20261005-210954.405845000-1.

A plain cycle cannot show a second cycle, so rework is read over the goal
loops listed in the first bullet, never over every goal in the baseline.

**Plan rework: cycles needed to reach "goal met"** (the goal loops above,
each read from its cycles' `assess.json`):

- Met at cycle 1: 4 of 13, run ids 20261005-141210.629450000-1, 20261005-192555.894236000-1, 20261005-195025.097992000-1 and 20261005-234213.753108000-1.
- Met at cycle 2: 2 of 13, run ids 20261005-230922.472429000-2 and 20261005-233354.068044000-2.
- Not met after both cycles: 6 of 13, cycle-2 run ids 20261005-174039.129694000-2, 20261005-175609.054167000-2, 20261005-183911.261245000-2, 20261005-194338.550661000-2, 20261005-205711.011394000-2 and 20261005-211849.181048000-2.
- No verdict at all: 1 of 13, run id 20261005-200252.888896000-1, killed before its assessor ran.
- Needed a second cycle: 8 of 13 (`grep -c '^[0-9.-]*-2 |'`), the 2 met at cycle 2 and the 6 not met, run ids as in the two bullets above.
- Needed a third cycle: 0 (`grep -c '^[0-9.-]*-[3-9] |'`), because the cap of 2 allowed none; the 6 not met are censored at the cap (run ids 20261005-174039.129694000-2, 20261005-175609.054167000-2, 20261005-183911.261245000-2, 20261005-194338.550661000-2, 20261005-205711.011394000-2 and 20261005-211849.181048000-2), not "met at 3".

**Verify fail rate per cycle.** Computed from run state, not from the row
summary. Every cycle's `graph.json` carries exactly one node with a
`success_check.verify` (every run id listed below, under the three outcomes), and
that node's terminal record is the cycle's verify outcome. The engine writes it in a form that can be read back:

- A pass is `node_passed` with `provenance: verified` (`ProvenanceVerified`, `internal/runfeed/runfeed.go:130`, set by `passProvenance`, `internal/schedule/scheduler.go:1159`).
- A fail is `node_failed` with the detail `node "<id>" failed success_check verify: …` (`(*NodeCheckError).Error`, `internal/schedule/errors.go:54`), and `verdict: FAIL`, `judged: true` on the node in `state.json` (`Judged`, `internal/runstate/runstate.go:295`).

The figures:

- Verify failed: 8 of 26 cycles, run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1, 20261005-205711.011394000-2, 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1 and 20261005-231953.292617000-1.
- Verify passed: 16 of 26, run ids 20261005-141210.629450000-1, 20261005-175230.634206000-1, 20261005-175609.054167000-2, 20261005-183113.286701000-1, 20261005-183114.023251000-1, 20261005-183911.261245000-2, 20261005-192555.894236000-1, 20261005-194338.550661000-2, 20261005-195025.097992000-1, 20261005-210211.990080000-1, 20261005-210721.943676000-1, 20261005-210954.405845000-1, 20261005-211849.181048000-2, 20261005-230922.472429000-2, 20261005-233354.068044000-2 and 20261005-234213.753108000-1.
- Verify never ran: 2 of 26, run id 20261005-174039.129694000-2 (its check node failed `result_matches` first, and the in-memory predicates run before the verification, `internal/schedule/scheduler.go:962-964`) and run id 20261005-200252.888896000-1 (killed before its verify node started).
- Verify fail rate among cycles whose verify ran: 8 of 24, run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1, 20261005-205711.011394000-2, 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1 and 20261005-231953.292617000-1 (the 24 exclude run ids 20261005-174039.129694000-2 and 20261005-200252.888896000-1).
- By cycle: cycle 1 failed verify in 7 of 18 (the 18 from `grep -c '^[0-9.-]*-1 |'`), run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1, 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1 and 20261005-231953.292617000-1.
- Cycle 2 failed verify in 1 of 8 (the 8 from `grep -c '^[0-9.-]*-2 |'`), run id 20261005-205711.011394000-2.
- 4 of the 8 failures carry `failed success_check verify` in full, run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1 and 20261005-205711.011394000-2.
- The other 4 are verify failures by elimination, run ids 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1 and 20261005-231953.292617000-1: their detail lost the head that names the predicate, because a detail keeps its last 240 runes (`maxDetailRunes` and `capDetail`, `internal/schedule/scheduler.go:82-89`).
- The elimination for those 4 (run ids in the bullet above): each is `judged: true`, which excludes a spawn error, a blown budget and a verification that could not complete (`internal/runstate/runstate.go:279-295`); none declares `exit_zero` or `budget_usd`; two declare no `result_matches` (run ids 20261005-210007.410977000-1 and 20261005-211717.221410000-1) and two replied a `PASS` that matches theirs (run ids 20261005-230005.700138000-1 and 20261005-231953.292617000-1, their `failed/<node>.out`); and `evaluateSuccessCheck` runs before `verifyEvidence` (`scheduler.go:962-964`), so the verification is the only predicate left that can have said no. Each kept tail is the verify command's own output.

The interview arm needs nothing new recorded to be read the same way: its
cycles write the same `graph.json`, `events.jsonl` and `state.json`, and the
comparison applies this same rule, including the elimination, to both arms.
This ADR adds no field for it.

**The comparison.** Two arms on the same kind of goals: interview off (the
baseline's shape) and `--interview` on, both with `--max-cycles 2`, as all 13 of the baseline's goal loops ran (`grep -l '"max_cycles": 2' ~/.oh-my-graph/runs/20261005-*-1/state.json | wc -l`).
The captain answers the interview arm, inside his real work. He already interviews in his own harness before he writes a DAG (§1.1).
For this comparison he uses `auto --interview` in place of that harness, on real goals, so the arm adds no extra experiments.
Only `auto --interview` goals count toward the interview arm's target of at least 5 goals, collected opportunistically as his real work produces them.
A goal taken through `oh-my-graph design` feeds only the captain's qualitative reading of the graphs, which §6 judges separately: `design` runs nothing, and the `run` its file then goes through has no goal loop and no assessor, so it records no cycles to goal met and no per-cycle verify rate.
Answering costs him about 10 minutes per goal, an estimate and not a measurement — `<!-- 미측정 -->`
The interview arm's model cost is not estimated in advance. It is read from each interview run's own records (the interview block in its `state.json`, §2.2), tied to that run's run id.
Agents never fake a TTY to answer an interview. Agents run only the off arm.
The off arm keeps the baseline's count: at least 13 goal loops (`grep -l '"max_cycles": 2' ~/.oh-my-graph/runs/20261005-*-1/state.json | wc -l`).
For each goal record: cycles to goal met (1, 2, or not met within the cap);
the verify fail rate among cycles whose verify ran, by the rule above; the cost per goal met,
interview calls included (§2.4); and, per interview, how it ended (`enough`,
`cap`, `operator`, `repeat`, `malformed`, `eof`) and how many answers were
recorded. The baseline is confounded (different repositories, two
runtimes, 14 `claude` and 12 `codex` (`grep -v '^#' | cut -d' ' -f1 | while read i; do grep -o '"runtime": "[a-z]*"' ~/.oh-my-graph/runs/$i/state.json; done | sort | uniq -c`), and 5 rows with no recorded goal, `grep -c 'goal not recorded'`), so a pair of arms run now
decides, and the numbers above are the reference they are read against.

### 2.6 Prior art: the captain's deep-interview skill

**Kept.**
- *Ask before building.* The premise: a wrong plan costs more than a few
  questions.
- *Questions that surface hidden assumptions, ambiguity and edge cases.* The
  interviewer prompt asks for exactly those, and for the assumption it would
  otherwise make, so that "I would assume X" is a question the operator can
  answer with a word.
- *One question at a time, short.* One `QUESTION:` per call.
- *A hard bound.* The skill bounds the interview; §2.3 does too.
- *A light mode.* The skill has a short mode on request; here it is `/done`
  at any prompt and, for agents, simply not passing `--interview`.
- *A fixed record of what was agreed.* The skill locks its seed spec as
  immutable. `interview.md` is staged once, hashed in `state.json` and
  refused if altered on resume.
- *Say what is uncertain.* The planner is told what was left unanswered when
  the interview was cut short (§2.3).

**Dropped, because an engine run is not a chat.**
- *Open-ended back-and-forth, derived follow-ups and push-back.* Each question
  is a stateless call with a transcript, not a conversation, so cost and
  resume are bounded and testable. The operator cannot argue with the
  interviewer, only answer, skip or stop.
- *Phases and the ambiguity score.* A model's self-scored readiness that
  decides "ready to code" is a verdict gating work, which §2.4 refuses. One
  stream of questions, a cap and `/done` replace it.
- *The 1-page summary with a final approval round.* See §2.3 (rejected).
- *Writing a spec file into the operator's repository.* The engine writes under
  its own run directory, or to the one path `--out` names.
- *Auto-triggering the interview.* Off is the default (§2.1(a)).

### 2.7 Decisions in one place

| question | decision |
| --- | --- |
| default | off; `auto` unchanged byte for byte |
| no TTY on stdin | refuse before any model call |
| `--plan-only` | allowed; interview, one planner call, stop |
| `oh-my-graph design` | interview, planner, YAML file, lint; never runs it |
| component | `internal/interview`, shared |
| asker | per-question coordinator call, unisolated like the planner |
| trust | untrusted; nonce-fenced text only |
| record | staged `interview.md`; hash and counts in `state.json` |
| cap | five questions; `/done` anytime; repeat ends it |
| goal loop | once per goal |
| gate | none added |
| 1.0.0 condition (1), part ② (`auto` produces it, not hand-written YAML) | `auto --interview` counts; `design` does not |

## 3. Alternatives considered

Each of the main alternatives sits beside its decision in §2.1 to §2.4. The
rest:

- **Answers from a file or a flag (`--answers`), for non-interactive use.** It
  would let an agent run an interview without a TTY, but it is a way to put
  arbitrary text in front of the planner and needs its own trust and sizing
  decision. The captain asked that agents keep a light, non-interactive `auto`,
  which is the off mode, not a scripted interview. Left for a later ADR.
- **Interview once per cycle.** See §2.4.
- **Feed answers to the assessor too.** The assessor judges evidence, not
  intent, and its input is already untrusted by design (ADR 0032 §1.4); it
  gains nothing from a transcript.
- **A new top-level `interview` command that only prints a spec.** It produces
  nothing the engine consumes, and the captain's workflow ends in a graph.
- **Make the interview a planned node (a first node whose output feeds the
  rest).** A node cannot ask a human: the planned-gate refusal exists because
  an `auto` run is non-interactive, and a node that waits on stdin would be a
  gate by another name.

## 4. Implementation outline

File and function names only; no code lands with this ADR.

- `internal/interview/` (new): `Run` (the loop of §2.1(c)), `Result`,
  `parseReply` (the grammar), `normalise` (the repeat test), `(*Result).Render`
  (the fenced staged text, via `fence.Nonce` and `fence.Truncate`),
  `(*Result).Stage` and `LoadStaged` (the pattern of
  `internal/conventions/conventions.go:320` and `:333`), and the constants of
  §2.3.
- `internal/coordinator/`: an option beside `WithVerifyCommand` that carries the
  rendered prefix; `(*Coordinator).plan` applies it to `base`; a helper that
  builds the interviewer's call from `coordinatorInvocation`.
- `cmd/oh-my-graph/flags.go`: `--interview` on `autoFlags`; the `auto` flag
  usage string and the usage line in `main.go` name it.
- `cmd/oh-my-graph/main.go`: `runAutoWithRuntime` takes a stdin seam beside its
  stdout one, performs the TTY refusal and calls the interview before
  `planAndExecute`; `executePlan` stages and records beside conventions.
- `cmd/oh-my-graph/design.go` (new): the `design` subcommand, wired in `run`'s
  dispatch beside `lint`, reusing `lintGraphForRuntime`.
- `cmd/oh-my-graph/resume.go`: carry the interview record forward and re-check
  the staged copy, beside the conventions lines.
- `internal/runstate/runstate.go`: the interview block on `Snapshot`.
- Documentation to update when it lands: README, DESIGN.md, `docs/EXAMPLES.md`,
  and the usage text. This record changes none of them.

## 5. Tests the implementation owes

1. **Off is byte-identical.** The planner prompt for a goal with no
   `--interview` equals today's golden prompt exactly, including a repair
   attempt and a cycle-2 continuation.
2. **No TTY refuses before any model call.** A `FakeRunner` records no call and
   no run directory exists; the error names `--interview`.
3. **End of input before the first answer is refused,** not treated as no
   questions.
4. **`--plan-only --interview`:** one planner call, the prefix present, the
   answers under `plans/<id>/`, no node run.
5. **`/done` at the first prompt** ends the interview, and the planner prompt is
   byte-identical to test 1's.
6. **The cap:** a fake interviewer that never says `ENOUGH` is called five times
   and no more; the plan receives the cut-short line naming `cap`.
7. **A repeated question** (differing only in case and punctuation) ends the
   interview as `repeat` with no further call.
8. **A malformed reply** ends it as `malformed`; `QUESTIONS:` and `ENOUGHX` are
   not tokens (whole-word).
9. **An over-long answer** is refused at the prompt and nothing is truncated.
10. **The fence:** an answer containing a forged marker line and the words
    "ignore the rules above" stays inside the fence; the nonce differs between
    calls; a nonce failure abandons the call rather than fencing with fixed
    markers.
11. **Text only:** the planner's `NodeInvocation` is the same as without the
    flag except for `Prompt`: same tool policy, same permission mode, same
    settings sources.
12. **Goal loop:** with `--max-cycles` of 2 the interviewer is called only before
    cycle 1; both planner prompts carry the same prefix; both run directories
    hold the staged file; the interview's cost is in the goal's spend.
13. **Resume** carries the record forward and refuses a missing or altered
    staged file, and makes no interviewer call.
14. **`--conventions` with `--interview`:** both are staged, the planner prompt
    holds the interview prefix only, and a node prompt holds the conventions
    only.
15. **`design`:** writes the file, runs the lint on it, never spawns a node,
    creates no run directory, refuses an existing `--out`, refuses
    `--conventions`, and on a lint failure keeps the file and exits non-zero.
16. **The record:** `state.json` for an interviewed run holds the hash, counts,
    ending reason and cost, and not the answer text; a run without an
    interview has none of it.

## 6. Falsification

The captain keeps, changes or drops the feature on the §2.5 comparison.
The Keep / Change / Drop test below is directional: it is a judgement aid, with no statistical significance at these sample sizes, and the captain's own reading of the graphs takes precedence over it.

- **Keep** if, over at least 5 `auto --interview` goals in the interview arm (§2.5) and at least 13 goal loops in the off arm (the baseline's count, `grep -l '"max_cycles": 2' ~/.oh-my-graph/runs/20261005-*-1/state.json | wc -l`), both at `--max-cycles 2`, all three hold against the off arm of the same comparison:
  - the interview arm needs a second cycle on a smaller share of its goal loops (baseline 8 of 13, `grep -c '^[0-9.-]*-2 |'`, run ids 20261005-174039.129694000-2, 20261005-175609.054167000-2, 20261005-183911.261245000-2, 20261005-194338.550661000-2, 20261005-205711.011394000-2, 20261005-211849.181048000-2, 20261005-230922.472429000-2, 20261005-233354.068044000-2);
  - its verify fail rate among cycles whose verify ran, read by the §2.5 rule, is lower (baseline 8 of 24, the 24 excluding run ids 20261005-174039.129694000-2 and 20261005-200252.888896000-1, failed run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1, 20261005-205711.011394000-2, 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1, 20261005-231953.292617000-1);
  - its cost per goal met, interview calls included, is not higher.

  Both entry points then stay.
- **Change** if rework falls but the verify fail rate does not, or the reverse, or
  the cost per goal met rises: the questions are doing something but not the
  right thing. Change what is asked (the interviewer prompt, the cap) before
  deciding the idea is wrong. Also change if the interviews mostly end by
  `repeat`, `malformed` or `/done` at the first prompt: the interview is not
  earning its place and the grammar or the prompt is at fault.
- **Drop `auto --interview`** if, over that same sample, neither measure
  improves. Keep `design` if the captain still prefers its file over his own
  harness; judge it by whether he stops writing DAGs by hand, not by the §2.5
  figures, because `design` runs nothing.
- **Drop both** if the interview arm's graphs are no better on his own reading
  than the off arm's, whatever the figures say: the figures are a proxy for
  that judgement and it governs.
- **This ADR's reading of the baseline is wrong** if any of the four verify failures §2.5 classifies by elimination proves to have failed on another predicate (run ids 20261005-210007.410977000-1, 20261005-211717.221410000-1, 20261005-230005.700138000-1, 20261005-231953.292617000-1).
  The baseline rate then lies between 4 of 24 (run ids 20261005-173231.808441000-1, 20261005-193759.110385000-1, 20261005-204449.000862000-1, 20261005-205711.011394000-2) and 8 of 24, and the Keep test is read against the corrected figure.
  It is also wrong if a goal-loop row turns out to have run with a different `max_cycles` (`internal/runstate/runstate.go:347`; all 13 read `2`, `grep -l '"max_cycles": 2' ~/.oh-my-graph/runs/20261005-*-1/state.json | wc -l`): rework would then not be comparable across goals, and the arms must be read only against each other.
  Neither changes the decision rule: the arms are compared with each other, and the baseline is the reference they are read against.
