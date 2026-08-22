# ADR 0033 — The run is the unit of evidence, not the node

**Status:** Proposed. The question put to this record was whether the engine
should require evidence from every node; the answer is **no**, so there is no
implementation lane and no code to owe tests for. The deliverable is
documentation (§3.1) and one owed measurement (§7). Nothing in the engine
changes: no flag, no schema field, no new seam, no behaviour change.

**Where the addresses point.** Written against `16f0ecc` — the `main` this
branch left. Every `file:line` below was read at that commit and resolves
exactly there. Because this record decides *not* to build something, its
addresses are all to existing code and existing documents; none of them point at
code that does not exist yet.

**Where the numbers come from.** Every corpus figure is the measurement's, cited
to `docs/measurements/0218-denied-nodes-that-passed.md` by line. The cost and
timing figures in §2.3 are the evidence brief's, cited by line to the run
artifact that holds them
(`~/.oh-my-graph/runs/20260821-153459.591480000-1/read-evidence.out`); a
wall-clock reading does not reproduce on re-running its command, so no figure
here rests on a session that has since ended. The one figure this record
measured for itself — 14 of the 81 planned nodes declare a `verify` — carries
the parse that produced it, over run directories that are still on disk.
Figures that could not be measured are marked `<!-- 미측정 -->` in §8 and are
never carried into an argument as though they had been.

**Date:** 2026-08-22

## 1. Context

### 1.1 The reading that prompted this

`docs/measurements/0218-denied-nodes-that-passed.md:3-7`, verbatim:

> **Of the 73 planned nodes on this machine whose transcript could be read, 53
> had at least one tool call denied by the CLI's permission layer. 49 of those
> 53 were recorded `PASS` anyway. Of those 49, exactly 5 held a
> `success_check.verify` — a command the engine itself ran. The other 44 passed
> on the node's own words (8) or on nothing but the subprocess exit status
> (36).**

The corpus is 81 planned nodes across 18 planned runs, 73 of them with a
readable transcript (`0218:19-22`, `:207`, `:210`, `:245`). All 185 denials were
of `Bash`; not one of `Read`, `Write`, `Glob`, `Grep` or `Edit`
(`0218:275-278`). The measurement cost zero spawns (`0218:31`).

The measurement's own reading of what it found is the sentence this ADR is
actually about (`0218:280-283`, verbatim):

> `verify` is scarce and structurally placed. Only **8 of the 73** nodes declare
> one at all, and each is the terminal `check` node of its run — a verify is a
> per-run gate in these graphs, never a per-node one. That is why 44 of the 49
> are uncovered: there is at most one covered node per run to begin with.

And its recommendation (`0218:428-429`, verbatim): *"Lean on
`success_check.verify`. Treat denial detection, by whatever transport, as an
advisory signal that is never an input to a verdict."* That is the starting
position of this record, not its conclusion.

### 1.2 What those numbers are not allowed to say

The measurement carries its caveats in the same file as its counts, and they do
not stop applying once the counts are quoted somewhere else. Each one below is
written as what it **forbids this ADR to conclude**, because that is the form in
which a caveat survives being cited.

| caveat | address | what it forbids here |
| --- | --- | --- |
| **It is a self-measurement.** *"It measures **how this project uses oh-my-graph**, not how anyone else would."* The headline is a joint property of (i) this operator in `dontAsk` mode, (ii) this operator's `allowed_tools` habits — every planned node grants `Bash(git *)`, `Bash(go *)` and friends, never bare `Bash` — and (iii) the planner's habit of putting a `verify` only on a run's terminal `check` node. | `0218:35-50` | This ADR may not argue from "oh-my-graph nodes behave like this". It may argue only from "these 81 nodes, made this way, behaved like this" — and the third component of that joint property is *the very habit* §2 is deciding whether to change, which means the corpus is partly a picture of the status quo it is being used to judge. |
| **There is no rate, deliberately.** 53 of 73 is 72.6% and *"that percentage should not be written down or quoted."* The unit of independence is the run, not the node: 6 of the 18 runs are 100% denied and 6 are 0% denied, and *"the effective n is nearer 18 than 73"*. | `0218:349-365` | No percentage of nodes appears in this ADR's argument, and none should be derived from it. Where §2.3 divides one measured total by another, the result is labelled arithmetic over this corpus, never a rate over a population. |
| **The corpus is not stationary.** The 08-02 and 08-03 runs predate `--verify-cmd` entirely and could not have carried a `verify` even in principle; four of the 18 were made in the 24 hours before the measurement, by work aimed at this very issue. | `0218:361-364` | "8 of 73 declare a verify" is not a steady-state coverage figure. Part of the 65 is a feature that did not exist yet. |
| **The corpus grows while it is read.** *"The evidence file is a snapshot taken during run `20260820-174446.982693000-1`, not an invariant, and a re-run on a later day is expected to differ."* Independently visible: the run directory count has been read as 319 (`0218:19`), 328 (`CHANGELOG.md:24`) and 333 (the brief for this ADR, run `20260821-153459.591480000-1`). | `0218:375-378` | Any measurement §7 asks for will not reproduce these totals, and a mismatch is not a refutation. §6 is written so that its observations survive the corpus growing. |
| **Eight nodes are excluded from numerator and denominator both** — 2 with a session id but no transcript on disk, 3 with an empty `session_id`, 3 with no record in `state.nodes` at all. *"A node whose transcript cannot be read is not a node that was not denied; it is a node nothing is known about."* None of the 8 is a `PASS`. | `0218:309-340` | The 44 is safe from this exclusion — (b) and (c) are unaffected, and only the denial count could move, upward, to at most 55 of 75 (`0218:337-340`). But "81 planned nodes" and "73 nodes" are two different denominators and this ADR must not slide between them: §2.3's cost arithmetic is on the 81 planned nodes and the 78 records, §1.1's verdicts are on the 73. The `verify` count is where the slide is easiest, so §2.3 prints both sides of it — **14** of the 81 planned nodes declare one, **8** of the 73 readable ones do, and the six-node difference is itemised there — and never subtracts one from the other's denominator. |
| **The counts are a floor twice over.** The transcript glob matches only top-level session files, so a node whose subagent was denied is counted here as undenied (`0218:369-374`); and two real denial phrasings present in this same corpus are missed by the detector — class A, the rule/interactive denial, and class B, the auto-mode classifier, the latter *"newly relevant given ADR 0030's auto runs"*. | `0218:137-145`, `:369-374` | The 53 may be larger. This cuts *toward* acting, not away from it — but it also means the advisory signal §2.5 declines is under-reporting silently, which is the reason it is declined. |
| **A denial is not a defect.** Many of the 185 are an agent trying `gofmt` or `make fmt-check` outside its grant, being refused, and doing the right thing by another route. *"This measurement counts denials; it does not establish that any node's *work* was incomplete."* | `0218:379-384` | This ADR may not claim that any of the 44 nodes did bad work. The exposure it reasons about is epistemic — nobody asked — not a count of known failures. |

### 1.3 The structural fact this inherits: a planned node cannot carry a verify

`validatePlannedNodeVerify` refuses a planner-authored `success_check.verify`
outright (`internal/coordinator/coordinator.go:1248-1258`):

```go
return &PlanError{
    Reason: fmt.Sprintf(
        "planned node %q set success_check.verify (command %q); auto mode never runs a shell command from an unreviewed plan — exit_zero and result_matches are available instead",
        node.ID, node.SuccessCheck.Verify.Command,
    ),
}
```

The reason is in the function's own comment
(`internal/coordinator/coordinator.go:1237-1243`, verbatim): *"`success_check.verify`
is arbitrary shell run by the ENGINE, not by claude: it is not a tool call, so
it passes outside every guard this package builds — no permission mode, no
`allowed_tools`, no deny list, and not even the cwd restriction… A plan that may
write `verify: { command: "curl … | sh" }` has a hole straight through the rest
of this file."* Only the field is refused, not the whole check: `exit_zero` and
`result_matches` remain available to a planned node
(`internal/coordinator/coordinator.go:1245-1247`).

The refusal is pinned structurally, not just by that one call site:
`internal/coordinator/field_dispositions_test.go:185-190` records the
disposition as *"rejected when PLANNER-AUTHORED (this probe, unchanged),
settable by trusted code strictly AFTER validation from a string the user typed
at invocation (`--verify-cmd`, `attachVerifyCommand`)"*. And the same file
records why the probe exists at all
(`internal/coordinator/field_dispositions_test.go:19-22`): *"That has already
happened twice: `success_check.verify:` would have let a plan run arbitrary
shell outside every guard here."* This field has leaked in before.

**Consequence for everything below: the requirement this ADR was asked to
consider can never be "the planner writes a verify command."** That is not a
preference to be re-litigated; it is a guard with a test and a recurrence
history.

### 1.4 What ADR 0030 bought, and at what granularity

ADR 0030 decided that an `auto` invocation in a directory with a detected build
signal and no `--verify-cmd` **refuses** (`0030:146-151`). Three properties of
that decision matter here, and all three are about granularity:

- **It is a launch-time gate, once per invocation, before the planner call.**
  That is not incidental — it is the whole safety argument. `0030:544-548`,
  verbatim, answering the attack where a plan bootstraps its own signal: *"It
  does not apply here, because detection happens **once per invocation, before
  the planner call** — nothing a node writes is ever detected, and there is no
  per-node evaluation to widen."*
- **The command it buys attaches to sinks, not to nodes.**
  `attachVerification` walks the graph and sets `SuccessCheck.Verify` only where
  `isSink[node.ID]` holds and the node is not a gate
  (`internal/coordinator/verifycmd.go:250-260`); a graph whose sinks are all
  gates is refused rather than silently left unverified (`:261-263`). README
  already says this in the operator's words (`README.md:61-63`): *"the ENGINE
  runs your build command at each sink of the plan and judges its exit code
  itself."* So what ADR 0030 secured is **at least one engine-run command per
  run**. It never claimed one per node.
- **Its amendment to ADR 0016 is directional, and the direction is the safety
  argument** (`0030:530-533`, verbatim):

  > **Build-signal detection may gate a refusal. It may never derive a grant.**
  > A repository file may cause oh-my-graph to *stop*; it may never cause
  > oh-my-graph to *run* something, to widen a tool set, or to attach a command.

Two more things from 0030 carry down here unchanged. The archive case from #119
(`0030:112-116`): the planner gave its verify node `Bash(git *)` only, so the
node checked that a branch existed, never compiled anything, and replied `PASS`
in 17 seconds for $0.13 — after the node before it spent $11.01. The real build
then failed on a compile error, and *"Every row in the ledger read `PASS`."* And
the limit on what any of this proves (`0030:892-896`): *"**`verified` is still
not `correct`**… a run that passes the gate by supplying `--verify-cmd 'true'`
carries 'evidence' that measures nothing, and a node holding `Edit` can still
edit the file the command runs."*

ADR 0030 is itself still **Proposed**: its §8 owes two measurements before
Accepted (`0030:954-999`).

### 1.5 What a per-node command would have had to run through

If this ADR had required something, the mechanism was already in the tree and
would not have needed anything new:

- `verify.Verifier` is the interface (`internal/verify/verify.go:53-60`): a
  `Result` whenever the command ran, *"including a non-zero exit — that is a
  fact, not an error"*, and an error only when there is nothing to judge, which
  *"a caller must treat as a node failure, never as a pass."*
- `Request` carries no node id (`internal/verify/verify.go:24-35`) — *"the
  Verifier's job is to run a command and report the facts, not to know what the
  result will be used for."* It is already node-agnostic; per-node use would
  have required no change to it.
- `ShellVerifier` is the second exec seam (ADR 0002, restated at
  `internal/verify/verify.go:5-15`). It builds the child at
  `internal/verify/shell.go:133`, scrubs the child environment at `:135`, and
  spawns at `:171`.

**So a fifth spawner was never in question, and this record states that once so
nobody has to re-derive it.** Anything per-node would have gone through
`ShellVerifier`; the four-seam invariant (`CLAUDE.md`, enforced by
`internal/invariants`) would have been untouched either way.

There is a second design — make the *node* run its own check through `Bash` —
and it is the one the seam distinction rules out cheaply. An engine-run
`verify` does not pass the permission layer at all
(`internal/coordinator/coordinator.go:1238-1241`, quoted in §1.3); a node's Bash
call does. `docs/measurements/0213b-compound-commands-defeat-grants.md:3-7`
measures what survives there, verbatim:

> **Of the 246 Bash calls denied inside a planned node in this corpus, 64
> (26.0%) are the #213 compound shape — the first sub-command granted, a later
> one not. 162 (65.9%) were out of scope from their own first word and would
> have been denied compound or not. 20 (8.1%) held a grant for every
> sub-command and were denied anyway, and 17 of those 20 are not compound at
> all.**

Its governing caveat is that none of this is causal
(`0213b:308-312`): *"the denial text carries no reason code. It is
byte-identical for a compound call, a simple out-of-scope call and a sandbox
refusal. Every class here is a **correlation between command shape and
denial**, never a cause."* And the matching rule the classification rests on is
an assumption — *"How Claude Code actually applies `Bash(go *)` lives in the
Claude Code binary. **No source in this repository implements it.**"*
(`0213b:286-287`). Taken together: a check a node runs itself would have to be
one program per call, no pipe, semicolon, `&&`, redirect or heredoc, short
arguments, paths inside the working directory — and even then 20 of 246 calls
that satisfied every grant were refused anyway, for reasons nobody can read off
the record.

## 2. Decision

### 2.1 The engine requires nothing per node

**The engine requires no evidence from an individual node. `--verify-cmd`
remains a per-run command attached to a run's sinks, and a planned non-sink
node's verdict remains `exit_zero` and/or `result_matches` — the subprocess
exit status and the node's own sentence.**

**This ADR declines to extend ADR 0030 one level down: ADR 0030 stands exactly
as written, its gate, its flag, its exit code and its recorded field are
untouched, and nothing here reopens any of them.**

The deliverable is documentation (§3.1) and the measurement in §7 that would
overturn this.

### 2.2 Why ADR 0030's argument does not descend one level

ADR 0030's argument is *an unverified run is a choice, not a default*. It is
tempting to read that as a schema — *an unverified X is a choice, not a
default* — and instantiate X at the node. It does not instantiate, for three
reasons, and the third is the one that decides it.

**(a) A run's verdict and a node's verdict are different kinds of object.** The
0030 gate asks the operator one answerable question about the run: does anything
the engine itself runs check the result. The operator can answer it, because
they chose the goal and therefore know what would prove it, and there is exactly
one place to attach the answer — the sink, which is where the run's result is
(`internal/coordinator/verifycmd.go:250-260`). A node's `PASS` is not that kind
of object. It is a model's sentence about its own work, and at the moment the
operator would have to author a check for it, **the node does not exist**: its
id, its prompt and its scope are all invented by the planner, later, from a
reply that §1.3 keeps untrusted. The run has one sink whose exit code the
operator chose in advance. The node has a verdict nobody chose in advance and
nobody could have.

**(b) There is no supplier that keeps the planner's reply untrusted.** §4
enumerates six candidates. Candidate 6 — the node running a check itself,
through its own `Bash` grant — is not in this paragraph's count at all, because
it supplies nothing the *engine* runs: it is prompt text, judged by the same
node whose hands the denial tied, and §4 rejects it on `0213b`'s measurement
rather than on trust. Of the five that would supply an engine-run command, two
are excluded by guards that already exist and are not this ADR's to relax:
an engine-derived per-node default (3) makes a
repository file the source of an engine-run command, which `0030:530-533`
forbids in exactly those words and `0030:692-697` already rejected once; the
goal loop or assessor choosing the string (4) is the same class as the planner
choosing it, since `internal/coordinator/coordinator.go:1237-1243`'s reason is
about *any* model reply becoming engine-run shell, and validating it by pattern
is *"A regex standing where a trust boundary belongs"* (`0030:760-762`). That
leaves the operator, in two forms, and nobody (5). The operator per node (1)
hits a chicken-and-egg: **node ids come
from the planner, so a per-node mapping cannot be typed before the plan
exists.** The version that does work — read the plan, then author the
per-node checks — is `--plan-only` followed by `run` on a reviewed graph. That
path exists today and `0030:486` already places it out of scope: *"A
hand-written graph carries its author's own `success_check.verify` and is a
reviewed artifact."* The operator's one command on every node (2) escapes the
chicken-and-egg and is the only candidate that survives this paragraph — which
is why (c) below is the reason that actually decides.

**(c) The one command an operator *can* supply before the plan exists is one
command, and one command on every node is not per-node evidence.** This is the
decisive reason, and it is checkable rather than argued. Run
`20260820-163530.563884000-1` has five planned nodes — `adr`, `implement`,
`review`, `docs`, `check` — and its `graph.json` gives `success_check` to
`check` alone, whose verify is `make local`; the other four carry an empty
`success_check`. `make local` is `fmt-check vet build test` (`Makefile:44`).
Attaching it to `adr` and `docs` would run the Go test suite over source those
nodes never touched: a pass would say nothing about whether the ADR got
written, and a failure would blame a documentation node for the tree it
inherited. Attaching it to `review` would re-measure what `implement` left,
which is what the sink already measures, one node later. That is not five
pieces of evidence. It is the run's one piece of evidence, sampled five times,
at five moments, charged five times.

`0030:892-896`'s *"`verified` is still not `correct`"* gets sharper here rather
than softer. At the sink, a mismatched command at least measures the thing the
run was for. On an interior node it measures something the node was not asked to
do, and a green result there is a stronger false assurance than no result — the
ledger would read `PASS (verified)` on a node nothing verified.

**(d) And the feasibility premise is unmeasured.** The measurement names the
threshold itself (`0218:467-472`, verbatim): *"**Evidence that per-node `verify`
is impractical.** The case rests on being able to raise coverage above today's 8
of 73. If an attempt to write a verify on non-terminal nodes shows that most
node kinds have no cheap checkable postcondition — say, coverage stalls below a
third of nodes — then the uncovered majority needs *some* signal, and a prose
match as an advisory becomes the primary deliverable rather than a garnish."*
Nobody has attempted it. The number is `<!-- 미측정 -->` (§8). Requiring
something of every node while the premise that most nodes *can* satisfy it is
unmeasured is the shape of mistake this repository has a standing rule against,
so the honest disposition is **not now, and here is the measurement that would
change it** (§7) — not *never*.

That last clause is quoted in full deliberately, because it is a condition on
§2.5 and not only on this subsection: the measurement makes a low census result
the trigger that *promotes* the denial advisory from garnish to primary
deliverable. §6's F5 carries it.

### 2.3 The cost of the cheapest version, measured — and why it does not decide this

The cheapest thing that could have been built is §4's candidate 2: widen
`attachVerification`'s sink filter so the operator's one `--verify-cmd` attaches
to every node. Its cost is quantifiable from this corpus, so it is quantified
rather than guessed.

Measured inputs:

| quantity | value | address |
| --- | --- | --- |
| planned nodes in the corpus | 81 | `0218:210` |
| planned runs | 18 | `0218:207` |
| node records carrying both `cost_usd` and `duration` | 78 (the 3 missing are `0218:329`'s "no record in `state.nodes`") | `read-evidence.out:305-309` |
| **of the 73 with a readable transcript**, nodes declaring a `verify` | 8 | `0218:280` |
| **of the 81 planned nodes**, nodes declaring a `verify` | 14 | the 18 runs' `graph.json`, parsed — command below |
| total node model spend over the 78 records | $159.2653 | `read-evidence.out:310` |
| mean model spend per node record | $2.0419 | `read-evidence.out:311` |
| total node wall-clock over the 78 records | 17,244.1 s ≈ 4.79 h | `read-evidence.out:312` |
| `make local`, warm cache, this worktree | **47.82 s** | `read-evidence.out:317-321` |
| `go build ./...`, warm cache, this worktree | **1.61 s** | `read-evidence.out:317-321` |

`read-evidence.out` is `~/.oh-my-graph/runs/20260821-153459.591480000-1/read-evidence.out`,
the evidence brief this record was written from — a run artifact on disk, which
is why it is quoted by line rather than as "measured in a session". Both timings
are warm; cold-cache figures are `<!-- 미측정 -->` (§8), as the brief itself
records at `read-evidence.out:343`.

**The two `verify` counts are two denominators, and the gap between them is
accounted for exactly.** Parsing `success_check.verify` out of the 18 runs'
`graph.json` — the same parse yields 81 total nodes, agreeing with `0218:210` —
finds **14**:

```py
import json, os
RUNS = [...]   # the 18 run ids, verbatim from the per-run table at 0218:289-306
base = os.path.expanduser("~/.oh-my-graph/runs")
hits = [(r, n["id"]) for r in RUNS
        for n in json.load(open(os.path.join(base, r, "graph.json")))["nodes"]
        if (n.get("success_check") or {}).get("verify")]
# len(hits) == 14; every command is "make local"
```

Six of the 14 are outside the 73: five in `20260820-162555.890191000-1`
(`corpus`, `precedent`, `predicate`, `handcheck`, `check`), the one run with
zero readable transcripts (`0218:303`), and `check` in
`20260819-154136.440217000-1`, which has no record in `state.nodes`
(`0218:234`, `:329`). 14 − 6 = 8, reconciling exactly with `0218:280`. The
81-node arithmetic below therefore subtracts **14**, never the 8; §1.1's
verdicts use the 8, never the 14.

Arithmetic over those inputs — **arithmetic, not measurement**:

| computation | result |
| --- | --- |
| 81 nodes × `make local` | 3,873 s (64.6 min) — `read-evidence.out:332` |
| the increment over the 14 already covered: 67 × `make local` | 3,204 s (53.4 min) |
| 78 records × `make local`, against their measured 17,244.1 s | **21.6%** added wall-clock — `read-evidence.out:336` |
| 78 records × `go build ./...`, against the same | **0.73%** added wall-clock — `read-evidence.out:337` |
| dollar cost of the added engine verifications | **$0 model spend.** `ShellVerifier` spawns a shell (`internal/verify/shell.go:171`, over the command line built at `:133`) and never a model CLI, so there is no token-billing path. This is a fact about the code with an address, not a reading of a bill. |

One honest note on the increment row: the brief's own version of it
(`read-evidence.out:331`, `:333`) subtracts 8 from 81 and reports 73 × `make
local`. That is the denominator slide §1.2 forbids, and the row above is the
corrected arithmetic — 67, not 73. The three rows carrying a
`read-evidence.out` address are unaffected by the correction, because none of
them involves the subtraction.

**And the cost is not why §2.1 decided as it did.** At `go build ./...` prices
the overhead is under 1% and is an argument against nothing; even at `make
local` prices, 21.6% against a corpus that spent $159 and 4.8 hours is real but
not prohibitive. §2.1 rests on §2.2, and this is stated plainly so that a later
reader who finds a cheap command does not read it as having repealed the
decision. What the cost table does establish is the narrower point in §2.2(c):
the expensive version of "one command everywhere" buys a fifth of the run's
wall-clock in exchange for re-running one check at four extra moments.

### 2.4 What stays exactly as it is

Stated positively, with addresses, so no reader has to derive any of it:

- `validatePlannedNodeVerify` is untouched
  (`internal/coordinator/coordinator.go:1248`). **The planner's reply stays
  untrusted, and nothing decided here lets a planned node choose what the engine
  runs on its behalf.**
- `attachVerification`'s sink filter is untouched
  (`internal/coordinator/verifycmd.go:250-260`), as is its refusal when every
  sink is a gate (`:261-263`).
- **No new exec seam.** `verify.ShellVerifier` remains the second seam (ADR
  0002); had anything been required it would have gone through it (§1.5). The
  four-seam invariant is not approached, let alone weakened.
- **`run` is not touched.** A hand-written graph writes `verify:` on whichever
  node its author means (`DESIGN.md:1583`), and that is the supported way to get
  interior coverage today. `0030:486` already put `run` out of scope; this
  record says so again rather than leaving the next reader to infer it.
- **`resume` and the goal loop are not touched, and cannot be.** This decision
  adds no field and attaches nothing, so there is nothing for a resumed leg's
  recorder to carry forward or erase — the failure mode 0030 hit at that seam,
  where a rewritten snapshot *"does not fail to add one, it erases the first
  leg's"* (`0030:487`) — and nothing for a cycle of the goal loop to re-derive
  against a tree the previous cycle wrote.
- **ADR 0030's per-invocation gate is untouched**, which is what keeps
  `0030:544-548`'s bootstrapping defence true: there is still no per-node
  evaluation of anything, so there is still nothing a node can write that widens
  what a later node gets.

### 2.5 The denial advisory is not adopted either, and that is not §1.3 of ADR 0030 repeating itself

`0218:455-458` offers a fallback: *"if a denial signal is wanted, ship it as a
warning line in the run report — 'this node was denied *n* tool calls and passed
on `result_matches`' — where a wording drift degrades to a missing warning
rather than a wrong verdict."* ADR 0030 says something that reads as the
opposite (`0030:136-141`): *"A sentence that describes a defect and then commits
it is the weakest instrument this repository has; it costs one screen of
scrollback and buys a feeling of having been warned. **The change this ADR makes
is not to the wording. It is to the control flow.**"*

They are reconciled by asking what the reader of the sentence can *do*. ADR
0030's notice stood where an action existed and was one flag away — printing it
instead of taking it was the whole defect. Here, by §2.2(b), no action exists
for the reader to take: there is no per-node flag to name, and the honest advice
the warning could give ("write a hand-written graph") is documentation, not a
per-run alert.

**Not adopted, and not comfortably** — because the signal is also weak on its
own measurement's terms. Its discriminator is prose with no structural marker
anywhere in the corpus (`0218:433-439`); it already misses two real denial
phrasings present in this same corpus, class A and class B, the latter *"newly
relevant given ADR 0030's auto runs"* (`0218:137-145`); and the transcript walk
that feeds it misses subagent and workflow sessions entirely
(`0218:369-374`). A warning built on that under-reports silently, in the
direction of "no denials" — so its *absence* would carry no information, which
is the property that makes a warning worth printing.

It becomes worth building the moment the CLI record grows a structural marker
for a denial (`0218:473-476`), and it should then be adopted **as a signal,
never as a verdict** — which is the one part of the measurement's
recommendation this record adopts wholesale.

## 3. Consequences

### 3.1 The documentation this decision owes

This is the entire deliverable, and it is reference rather than warning: it
tells an operator what the engine does and does not check, and where to go if
they want more.

- **`README.md`, at the `--verify-cmd` example (`README.md:61-63`).** The
  positive half is already there — *"the ENGINE runs your build command at each
  sink of the plan"*. The complement is nowhere: **a planned non-sink node's
  `PASS` is its subprocess exit status and its own sentence, by construction and
  not by omission**, because `validatePlannedNodeVerify` refuses a
  planner-authored check (§1.3) and `--verify-cmd` attaches at sinks (§1.4).
- **`DESIGN.md`, at the passage that already explains the refusal
  (`DESIGN.md:1018-1023`).** It says why a planned graph carries no
  model-written verification; add what that costs in coverage — at most the
  sinks are covered, so an interior node's verdict is its own word — and name
  `run` with a hand-written graph as the supported route to interior coverage
  (`DESIGN.md:1583`).
- Neither addition may be written as a warning with no action attached. Each
  must name the route: `run`, on a reviewed graph, with the author's own
  `verify:` on whichever nodes they mean.

### 3.2 What this costs, stated rather than softened

The 44 keeps happening. Interior planned nodes will keep reporting `PASS` on
their own word, denied or not, and this record decides that the engine will not
ask them for more. It does so because no mechanism to ask exists that keeps the
planner untrusted (§2.2b) and because the mechanism that could be built cheaply
would not be asking about the node's work (§2.2c) — not because the exposure is
small. How large it is at any real rate is a question `0218:349-365` forbids
this document to answer, and §6 is written to be falsifiable without one.

### 3.3 Compatibility

Nothing changes. No flag is added or removed, no exit code moves, no
`state.json` field appears and the snapshot schema stays where ADR 0030 left it.
Every existing invocation of `auto`, `run`, `chat` and `resume` behaves exactly
as it did at `16f0ecc`.

The CHANGELOG entry that accompanies this record therefore says what was
*decided*, not what was measured — `0213b:13-15` is the standing rule that a
changelog recording readings rather than changes *"stops being a changelog"* —
and it is filed under `## [Unreleased]` (`CHANGELOG.md:11`), which
`scripts/changelog-entry-check.sh` checks by name.

## 4. Alternatives considered

Every candidate supplier of a per-node command, with the two columns that
decide: does the planner's reply stay untrusted, and what would it cost in
mechanism.

| # | supplier | mechanism it would need | planner's reply stays untrusted? | verdict |
| --- | --- | --- | --- | --- |
| 1 | **Operator, per node** (`--verify-cmd-for <node>=<cmd>`) | Flag parsing plus a mapping through `attachVerification`; no new seam, no new trust boundary — the string is typed by a human at invocation, the same shape `internal/coordinator/field_dispositions_test.go:185-190` already licenses. | ✅ fully | **Rejected: chicken-and-egg.** Node ids are invented by the planner, so the mapping cannot be authored before the plan exists, and the plan is the untrusted artifact. The version that works is `--plan-only`, review, then `run` — which exists and is out of scope (`0030:486`). |
| 2 | **Operator, one command on every node** (widen the sink filter at `internal/coordinator/verifycmd.go:250-260`) | The smallest change in this table: delete a condition. | ✅ fully | **Rejected on §2.2(c) and §2.3.** Cheapest to build and buys the least: the run's one check, re-run at every node, measuring work those nodes were not asked to do, for 21.6% more wall-clock at `make local` prices. |
| 3 | **Engine-derived per-node default** from the detection table | `DetectBuildSignals` plus attachment. | ⚠️ the planner is bypassed, but the **repository** becomes the source of an engine-run command — a worse direction, not a better one. | **Rejected, and already forbidden.** `0030:530-533`: *"Build-signal detection may gate a refusal. It may never derive a grant… it may never cause oh-my-graph to run something, to widen a tool set, or to attach a command."* `0030:692-697` rejected the run-level form of this already. Per-node evaluation would also void `0030:544-548`'s bootstrapping defence outright. |
| 4 | **Goal loop / assessor** picks the command between cycles | A validator over a model-authored string. | ❌ the assessor's reply is a model reply | **Rejected.** `internal/coordinator/coordinator.go:1237-1243`'s reason is about the *string's producer*, not about which model produced it: it would still be arbitrary shell run by the engine, outside every guard. Validating it by pattern is *"A regex standing where a trust boundary belongs"* (`0030:760-762`). |
| 5 | **Nobody** — status quo plus documentation | None. | ✅ nothing new is executed | **Chosen (§2.1).** |
| 6 | **The node checks itself**, running the command through its own `Bash` grant | Prompt engineering only; nothing engine-run. | ✅ nothing new is engine-run | **Rejected on measurement.** It moves the check behind the permission layer that an engine `verify` bypasses by construction (§1.5), where `0213b:3-7` measures 246 denied calls, 20 of which held a grant for every sub-command and were refused anyway, 17 of those not compound at all — for reasons no record states (`0213b:308-312`). And the check would be graded by the same node whose hands the denial tied, which is the defect `0218` is about. |

Also considered and rejected: **ship the denial advisory as the deliverable**
instead of documentation — §2.5.

## 5. Failure modes

- **The 44 stays 44.** Named in §3.2 rather than filed here as a surprise.
- **This record read as a blessing.** "The engine asks nothing of an interior
  node" can be quoted as "an interior node needs nothing". It does not: it needs
  a reviewed graph run through `run` if the operator wants interior coverage,
  which is why §3.1 requires the route to be named beside the fact.
- **`verified` is still not `correct`, at the sink either.** Unchanged from
  `0030:892-896` and from ADR 0016. This record makes no new claim about whether
  the sink's command is adequate; it declines to add more of the same thing.
- **The cheapest exit.** `0030:766-776` names the realistic aliaser as an agent,
  not a human. An agent reading this ADR could quote §2.1 as licence to stop
  supplying `--verify-cmd` at all. It is not: ADR 0030's launch-time gate still
  refuses that invocation, and §2.4 leaves the gate exactly where it is.
- **Deferral becomes permanence.** "Not now, pending §7" is a real disposition
  only if §7 is actually run. If §7 is still unmeasured when the next
  denied-and-passed reading lands, that is evidence about this process, not
  about the decision.

## 6. Falsification — what would make this wrong

Each of these is something a later measurement over the run corpus could
actually see, and each survives the corpus growing (`0218:375-378`).

**F1 — per-node coverage turns out to be feasible.** Take the 81 planned nodes
of the 18 runs (`0218:210`, `:287-307`) and, by hand, write for each the
cheapest command whose exit code would have distinguished that node's work being
done from not done — or record explicitly that none exists. If **more than a
third of the 81** admit one — the threshold `0218:470` names, taken here from
the other side — then §2.2(c) is not the whole story. Node *kinds* recur across
runs (`adr`, `implement`, `review`, `docs`, `check` in
`20260820-163530.563884000-1` are not unique to it), so a mapping keyed on kind
rather than on planner-chosen id becomes authorable before the plan exists, and
§4's candidate 1 loses its chicken-and-egg. This ADR would then be wrong, and
its successor should widen `attachVerification`.

**F2 — a denied node that passed its `verify` but whose work was in fact
incomplete.** `0218:462-466`: currently 0 observed out of 5, *"but 5 is far too
few to have looked, and nobody has checked those 5 against their goals."* That
premise is load-bearing here in a way it was not in the measurement, because
§2.1 rests an entire run's evidence on the sink's command. One such node
falsifies the sufficiency claim directly.

**F3 — the failures are in the interior.** Over runs whose sink `verify`
PASSED, look for a later run or issue whose goal is repairing an *interior*
node's output from the earlier run. If those exist in quantity, then "the run is
the unit of evidence" is false as an empirical claim and not merely
under-supported: the run's one check passed and the run still shipped wrong
work, at a node nobody asked anything of. This is observable from the corpus
alone — run directories, their graphs, and the goals of subsequent runs — with
no new instrumentation.

**F4 — a structural denial marker appears in the CLI record** (`0218:473-476`).
This does not falsify §2.1, but it falsifies §2.5: the advisory becomes cheap
and reliable and should be built at once, as a signal and never as a verdict.

**F5 — the census comes back below F1's threshold.** F1 and F5 are the two
sides of one measurement, and §7's census decides between them: if fewer than a
third of the 81 admit a cheap command, §2.1 stands — a per-node requirement most
nodes cannot satisfy is confirmed as friction — but §2.5 falls, because
`0218:470-472` names exactly that outcome as the one that makes *"a prose match
as an advisory … the primary deliverable rather than a garnish"*. So §2.5's
refusal is conditional on a number nobody has yet taken, and a low census
obliges building the advisory despite §2.5's objections to it — which would then
have to be answered on their own terms (its discriminator's known misses,
`0218:137-145`, `:369-374`) rather than used to decline again.

## 7. Required measurement before Accepted

**The coverage census F1 and F5 describe** — one census, and whichever side of
the threshold it lands on falsifies something here. It is the one number this decision is
missing, and it is the number `0218:467-472` already nominated. Scope: all 81
planned nodes across the 18 runs, one hand-written candidate command per node or
an explicit "none cheap", published as `docs/measurements/0033-…md` with the
per-node table so the count can be re-derived by hand. It costs zero spawns —
the corpus is on disk, as `0218:31` demonstrates for the same walk. Until it
exists, this record stays **Proposed**.

Inherited, and worth stating: ADR 0030's own §8 measurements are still
outstanding (`0030:954-999`), so the record this one declines to extend is not
itself Accepted. That is a reason for caution about extending it, not a reason
to extend it faster.

## 8. What could not be determined

- How many of the 81 nodes have a cheap checkable postcondition — F1's number,
  the premise §2.2(d) rests on being unknown — `<!-- 미측정 -->`
- How many of the 44 uncovered nodes would have been flipped to `FAIL` by a
  per-node check. `0218:379-384` calls establishing this *"a separate and much
  larger job"* — `<!-- 미측정 -->`
- Cold-cache timings for `make local` and `go build ./...`. Both figures in §2.3
  are warm — `<!-- 미측정 -->`
- Verify-command timings on any machine other than this one, or any repository
  other than this one — `<!-- 미측정 -->`
- What share of a `check` node's recorded duration (194.6 s, 61.0 s and 98.3 s
  in the three runs the brief inspected) was the verify command itself.
  `state.json` records node duration and does not separate it — `<!-- 미측정 -->`
- The retry and re-planning cost a per-node check would add, or *save* by
  halting a doomed run early under halt-on-fail — `<!-- 미측정 -->`
- Whether the $2.0419 mean model spend per node differs for verified nodes. The
  three verified nodes the brief read cost $0.234, $0.419 and $0.984, all below
  the mean, but n=3 and all three are terminal `check` nodes, which confounds it
  completely — `<!-- 미측정 (causal) -->`
- Which `claude` version produced the corpus. No run record holds it
  (`0213b:322-325`), so the corpus may quietly be two populations —
  `<!-- 미측정 -->`

## 9. References

- `docs/measurements/0218-denied-nodes-that-passed.md` — 53 of 73 planned nodes
  denied a tool call, 49 of those recorded `PASS`, 44 of the 49 holding no
  engine-run check; the source of every corpus figure and every caveat in §1.
  Issue [#218](https://github.com/jitokim/oh-my-graph/issues/218).
- ADR 0030 — *An unverified auto run is a choice, not a default*
  (`docs/adr/0030-an-unverified-auto-run-is-a-choice-not-a-default.md`). The
  record this one declines to extend one level down (§2.1), and the source of
  the per-invocation granularity argument (§1.4), the refusal-not-grant
  direction (`0030:530-533`), and #119.
- ADR 0002 — *Verification is a second exec seam*
  (`docs/adr/0002-verification-is-a-second-exec-seam.md`). Why nothing
  considered here would have needed a fifth spawner (§1.5).
- `docs/measurements/0213b-compound-commands-defeat-grants.md` — a compound
  command defeats a prefix grant 64 times in 246. Why a check the *node* runs
  through its own `Bash` grant is not a substitute for an engine-run one (§1.5,
  §4 candidate 6). Issue
  [#213](https://github.com/jitokim/oh-my-graph/issues/213).
- ADR 0016 — *Build evidence is a user-supplied engine command*
  (`docs/adr/0016-build-evidence-is-a-user-supplied-engine-command.md`). The
  invariant §1.3's refusal enforces.
- The evidence brief this record was written from:
  `~/.oh-my-graph/runs/20260821-153459.591480000-1/read-evidence.out` (run
  `20260821-153459.591480000-1`, node `read-evidence`) — the address, by line,
  for every cost, duration and command-timing figure in §2.3, and for the
  warm-cache caveat on the last two (`:343`).
