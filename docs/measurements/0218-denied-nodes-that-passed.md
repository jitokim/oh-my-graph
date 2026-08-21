# Issue #218 — 53 of 73 planned nodes were denied a tool call, 49 of those were recorded PASS, and 44 of the 49 held no engine-run check

**Of the 73 planned nodes on this machine whose transcript could be read, 53 had
at least one tool call denied by the CLI's permission layer. 49 of those 53 were
recorded `PASS` anyway. Of those 49, exactly 5 held a `success_check.verify` —
a command the engine itself ran. The other 44 passed on the node's own words
(8) or on nothing but the subprocess exit status (36).**

44 is the number this issue is about. A node that was denied a tool and then
passed a verify command is not a defect: the engine checked the world and the
world agreed. A node that was denied a tool and then passed because it emitted
the right sentence, or because `claude -p` exited 0, was judged by the same
process whose hands were tied.

- **Date:** 2026-08-21 (KST), macOS (darwin 22.6.0), one machine. The
  measurement pass itself ran as a node of run
  `20260820-174446.982693000-1`; this document transcribes that pass's frozen
  evidence file and does not re-run it.
- **Corpus:** `~/.oh-my-graph/runs`. 319 run directories, 318 of which carry a
  `state.json`; 19 also carry a `graph.json`; 18 pass the PLANNED test below,
  and 1 is excluded as in flight. 81 planned nodes, 73 of them with a readable
  transcript.
- **Method:** [`0218-denied-nodes-that-passed.go`](0218-denied-nodes-that-passed.go),
  which parses every snapshot and every transcript line with `encoding/json`.
  **Never `grep`** — see the discriminator's false-positive section; this
  repository's own sessions grep for the denial sentence, so the denial
  vocabulary appears inside ordinary Bash stdout.
- **Evidence:** [`0218-denied-nodes-raw.json`](0218-denied-nodes-raw.json), one
  row per planned node — denied or not, ran or not. Every count below is a
  count over those 81 rows and can be re-derived from them by hand.
- **Cost:** zero spawns. 318 snapshot reads and 73 transcript walks.
- **Re-derive:** `go run docs/measurements/0218-denied-nodes-that-passed.go`
  from the repository root.

## This is a SELF-MEASUREMENT

The corpus is this project's own oh-my-graph runs, on the maintainer's own
machine, under the maintainer's own permission settings — including runs made
within the last 24 hours, and including the runs that produced this very
document. It measures **how this project uses oh-my-graph**, not how anyone else
would.

Concretely, that means the headline is not a property of the tool. It is a joint
property of (i) this operator running in `dontAsk` mode, (ii) this operator's
`allowed_tools` habits — every planned node here grants `Bash(git *)`,
`Bash(go *)` and friends, and never bare `Bash` — and (iii) the planner's habit
of putting a `verify` only on a run's terminal `check` node. A user who runs
interactively, or who grants bare `Bash`, or who writes a `verify` on every
node, would produce different numbers from the same binary. Nothing here should
be quoted as "oh-my-graph nodes are denied *n*% of the time".

## The discriminator

Everything downstream rests on one predicate, so it goes first.

**The discriminator is PROSE, not structure.** Every `is_error: true`
`tool_result` block in the entire local corpus — permission denials and ordinary
`go test` failures alike — carries the identical key set
`{content, is_error, tool_use_id, type}`, and in every denial `content` is a
plain JSON string. There is no flag, no extra field, no distinct record type and
no distinct block type separating a denial from a command that exited 1. The
only thing that separates the two classes is the English sentence the CLI
injects.

Locate a record whose top-level `"type"` is `"user"`; decode `.message.content`
as an array of blocks; take blocks whose `"type"` is `"tool_result"` and whose
`"is_error"` is boolean `true`; render `.content` to prose. Then, quoted
literally from the measurement script (`:348-362`):

```go
const denialHead = "Permission to use "
const denialCore = " has been denied because Claude Code is running in don't ask mode."

// isDontAskDenial reports whether a rendered tool_result prose is a dontAsk
// permission denial, and names the tool the CLI said it denied.
func isDontAskDenial(prose string) (tool string, ok bool) {
	if !strings.HasPrefix(prose, denialHead) { // anchored at offset 0 — load-bearing
		return "", false
	}
	i := strings.Index(prose, denialCore)
	if i < 0 {
		return "", false
	}
	return prose[len(denialHead):i], true
}
```

The apostrophe in `don't` is ASCII U+0027, not U+2019, checked at the codepoint.

### Why it is trusted

- **The tail is invariant.** Across every anchored denial in the corpus there is
  exactly one distinct 705-byte tail string, not a family of paraphrases, so
  `head + toolName + tail` reconstructs the content byte-exactly.
- **It passes an independent internal consistency check.** The tool name the
  prose interpolates always equals the `name` of the `tool_use` block that the
  result's `tool_use_id` points at — zero mismatches across the corpus. The
  prose agrees with the structure even though the structure cannot decide the
  question.
- **It is disjoint from the ordinary-failure shape.** An ordinary Bash failure's
  content begins with the literal `"Exit code "` (an empty `grep` yields the
  content string exactly `"Exit code 1"`); no denial ever does.
- **A second method agrees.** An independent check re-derived the counts below
  without running the measurement script and returned `PASS`
  (`~/.oh-my-graph/runs/20260820-174446.982693000-1/crosscheck.out`).

### False positives — the trap is live in this repository

The dangerous confound is not `is_error` generically. It is that **this
repository's own development sessions grep for these very words**, so ordinary
Bash failures carry denial vocabulary inside their captured stdout — and so do
node prompts that quote the sentence in order to discuss it. Real examples, all
correctly rejected because the denial text is not at offset 0 (paths under
`/Users/imac/.claude/projects/`):

| address | prose opening |
| --- | --- |
| `-private-tmp-sk/0b16210b-0264-4790-8ef4-ff9d56db43c3.jsonl:50` | `Exit code 1\nI couldn't run it — Bash is blocked in this session.\n\nClaude Code is currently…` |
| `-private-tmp-sk/0b16210b-0264-4790-8ef4-ff9d56db43c3.jsonl:77` | `Exit code 1\npermission_denials: [{"tool_name": "Bash", …` |
| `-private-tmp-sk/429b0706-9db5-4140-ad51-278a4a0c6b62.jsonl:295` | `Exit code 1\ncost 0.155317\nresult I can't run it — Bash is denied in this session ("don't as…` |
| `-Users-imac-IdeaProjects-oh-my-graph/f85ea6fb-…/subagents/agent-ab20e098273cf1d9a.jsonl:36` | grep output `44:\tdefaultPermissionMode = "dontAsk"…` |
| `-Users-imac--oh-my-graph-runs-20260811-134126-205438000-1-worktrees-grant/4beb5ed7-473a-44ac-8969-05846e57a771.jsonl:122` | an `allowed_tools` lint warning in stdout |

**A `Contains`-based predicate counts all five as denials.** The `HasPrefix`
anchor rejects every one, and no real denial in the corpus carries the core
string at a non-zero offset. Two further anchoring notes from the independent
check: the top-level `toolUseResult` field duplicates every denial, so a walker
must read `message.content` blocks and not that field or it doubles the count;
and the nearest false positive found anywhere was `EACCES: permission denied,
posix_spawn 'rg'` — an OS error, correctly rejected. Residual FP risk: a tool
whose own error text *begins* with this exact sentence. Implausible, not
provably impossible.

### False negatives — four denial-ish classes deliberately excluded

Each is a different decision for #218, so each was addressed separately rather
than lumped in. The counts below are therefore a **floor** on denials, not a
total.

| class | exact opening | address | verdict |
| --- | --- | --- | --- |
| **A. Rule/interactive denial naming the command** | `Permission to use Bash with command <full command> has been denied.` | `-Users-imac-IdeaProjects-fleetops/72b268e8-fbdd-4569-8c09-1a50f6c09332.jsonl:5123`, `:5137` | Real denial, **missed**. Shares the head, lacks the core. |
| **B. Auto-mode classifier** | `Permission for this action was denied by the Claude Code auto mode classifier. Reason: Blocked by classifier.` | `-Users-imac-IdeaProjects-oh-my-graph/f85ea6fb-f3a0-4cd8-8b05-7c86a570fbae.jsonl:16600` | Real denial, **missed**. Newly relevant given ADR 0030's auto runs. |
| **C. Interactive user rejection** | `The user doesn't want to proceed with this tool use.` | `-Users-imac-IdeaProjects-fleetops/e6d7c2ff-…/subagents/agent-a548e637bff74de00.jsonl:37` | Correctly excluded — needs a TTY, cannot occur in a `claude -p` node. |
| **D. Tool never granted at all** | `<tool_use_error>Error: No such tool available: Write. …</tool_use_error>` | `-oh-my-graph-runs-20260809-134812-…-worktrees-rc/b0f8b889-85f1-4634-8fe1-4e65b30c92b4.jsonl:93` | Correctly excluded — grant-time absence, i.e. #154's class, not a runtime denial. |

A fifth trap, excluded and not a denial at all: `<tool_use_error>Blocked: sleep
30 followed by: …` is a Bash-tool-internal guard. It is the bulk of the 83 Bash
error results that are neither `"Exit code "`-prefixed nor denials, and any
predicate keyed on the words "blocked" or "denied" swallows it.

### Addresses the predicate was derived from

**Real denials** — the confirmed session, in which every `is_error` result is a
denial and no ordinary failure occurs:
`/Users/imac/.claude/projects/-Users-imac-IdeaProjects-oh-my-graph/13f6c560-8204-4606-807e-183bb316a849.jsonl`,
results at lines 21, 44, 46, **52**, 59, 65 (the `tool_use` at `:50`, command
`gofmt -l internal/serve`, is the confirmed one). Denials of other tools,
proving the name is interpolated and the tail invariant:
`…-worktrees-planner/362e3c7b-950c-4c54-be92-2ccf68a780c5.jsonl:71` (Monitor),
`-Users-imac-IdeaProjects-oh-my-graph-hq/feff4cb8-7a8c-409e-ad72-09cf1cc3a290.jsonl:61` (Write),
`-Users-imac--claude-mem-observer-sessions/79cb9622-709c-4aff-a328-2e9256a6efc1.jsonl:526` (Workflow) and `:2768` (Skill).

**Real ordinary failures** — 9 of them, on every one of which the predicate
returns false, verified against parsed JSON:
`…-worktrees-portable/d3530b48-cd88-49e9-8fac-a8ed942d3885.jsonl:78` and `:81`
(`go test` exits 1);
`…-worktrees-grants/f061c05b-e5f1-4921-a3c7-a636c4f66cb0.jsonl:128` and `:165`
(empty `grep`, content exactly `Exit code 1`);
`…-worktrees-lane/f5e09b39-5bec-4485-a641-a0c71613f071.jsonl:143` and `:178`
(`ls`, missing file);
`…-worktrees-lane/b34d2c0a-e75b-4eef-aa5a-c4841804569f.jsonl:117` and
`…-worktrees-lane/ebaead3d-7105-4c57-a80b-b5139058a02c.jsonl:21` (Read, missing
file); `-Users-imac/8e1bbdd4-…/subagents/workflows/wf_2502afed-b54/agent-a8a6fcbc74abc8b90.jsonl:10`
(stale-read Write).

Full evidence: `~/.oh-my-graph/runs/20260820-174446.982693000-1/discriminator.out`.

### What "prose, not structure" means for any future detector

It means a detector built on this cannot be made reliable by better
engineering. It is pinned to one CLI version's wording; classes A and B above
are *already* real denials in this corpus that this exact sentence does not
match; and there is nothing in the record — no field, no type, no flag — that a
stricter parser could key on instead. Any product feature that treats "was this
node denied?" as a decidable question is building on a sentence that a CLI
release note could change without warning, and would do so silently, in the
direction of reporting no denials.

One secondary signal was tested as a corroborator and **fails**: "in a session
with ≥1 denial of tool T, does T ever succeed?" Of 114 session×tool pairs
containing a denial, 88 are mixed — the tool is denied sometimes and succeeds
most of the time in the same session. The confirmed session is one of them
(Bash denied 6 times and succeeded 6 times). **Denial is per-command, not
per-tool**: `Bash` is granted, but `gofmt …` and `make fmt-check` match no
allow rule. Any option chosen for #218 must key on the command, and #154's
`handoff.LintToolGrants` does not corroborate here.

## The corpus

- **Run directories seen:** 319; 318 carry a `state.json`. This number drifts —
  the corpus is live and grows while it is measured (the ADR 0030 probe read 294
  on 2026-08-20).
- **Run directories carrying a `graph.json`:** 19. This set has not grown since
  the measurement pass, so the planned corpus below is complete and stable even
  though the total is not.
- **PLANNED runs (the corpus):** 18.
- **Excluded as IN FLIGHT:** 1 — `20260820-174446.982693000-1`, the run this
  measurement is itself a node of.
- **Planned nodes:** 81.

**How PLANNED was decided.** A run is planned exactly when its snapshot's
`graph_source_path`, after `filepath.Clean` and symlink resolution, is the same
file as that run's own `graph.json`, tested with `os.SameFile` (device + inode,
so it survives `/var` vs `/private/var` and any other path spelling). A
hand-written run points at a `.yaml` elsewhere in the filesystem; a planned run
points at the `graph.json` the planner wrote into the run directory. The test is
copied unchanged from `0213-tool-grant-predicate.go` so the two measurements
share one corpus definition rather than two that drift.

**Why the in-flight exclusion exists, and why it is not "has an unfinished
node".** A script that reads `$OMG_HOME/runs` while running under the engine
reads its own half-written `state.json`: its own record is not written until it
exits, so its own node counts as "never ran" and **its own denials vanish from
the numerator**. The first pass of this script did exactly that and published
54 of 74 / 50 of 54 — numbers nobody, including a re-run of the script, could
reproduce. The test now used is: at least one graph node has no entry in
`snap.Nodes` **and** no record carries `FAIL`. Under `on_fail: halt` a run that
stopped early always leaves a FAIL behind; a run still executing has not. Three
planned runs hold a node with no record, and the two tests disagree on them:

| run | node with no record | has a FAIL? | outcome |
| --- | --- | --- | --- |
| `20260819-154136.440217000-1` | `ship`, `check` | yes | halted — **kept** |
| `20260820-162555.890191000-1` | `check` | yes | halted — **kept** |
| `20260820-174446.982693000-1` | `report commit …` | no | in flight — **excluded** |

"Has a node with no record" on its own would wrongly drop the two legitimately
halted runs and three measurable nodes with them. A record carrying the *empty*
verdict is not a FAIL and must not be read as one: `runstate` documents the
empty string as ADR 0010's non-terminal feedback marker.

## (a), (b), (c)

Denominator: the **73** planned nodes with a readable transcript.

| | count |
| --- | --- |
| **(a) planned nodes denied ≥1 tool call** | **53** of 73 |
| **(b) of those, recorded verdict `PASS`** | **49** of 53 |
| **(c) of those 49, `success_check` declared…** | |
|   `verify` — the engine ran a command | **5** |
|   `result_matches` — the node's own words | **8** |
|   exit-only — nothing but the subprocess exit status | **36** |

**(c) is the number that matters, and it reads 5 against 44.** The split is
decided by the graph's `success_check`, not by the node's own report: `verify`
wins whenever both are declared, because an engine-run command is the stronger
evidence and is exactly what #218 asks whether the node had. The `PASS` string
is matched exactly; `internal/runstate` declares only `PASS`, `FAIL` and the
empty string, and no unexpected verdict string occurred in this corpus.

Note that `result_matches` and exit-only are not equally weak, and the weaker
one is the larger. 8 nodes passed by emitting the right sentence. 36 passed
because `claude -p` returned 0 — which it does whether or not the work was
done, and whether or not half the commands were refused. A denial does not make
a node fail: the CLI returns a `tool_result` and the node keeps going, so an
`exit-only` gate lets a PASS survive an arbitrary number of denials.

The 4 denied nodes that were **not** recorded PASS were all `FAIL`
(`20260803-084704…/check`, `20260819-154136…/review`,
`20260819-175025…/check-landed`, `20260819-223604…/verify`); two of the four
held a `verify`.

**All denials in this corpus were of `Bash`** — 185 of them across the 53 nodes,
from 1 to 17 in a single node (`20260819-163447.441137000-2/pr`). Not one
denial of Read, Write, Glob, Grep or Edit occurred, which is the per-command
finding again from the other side: the tool was granted, the command was not.

`verify` is scarce and structurally placed. Only **8 of the 73** nodes declare
one at all, and each is the terminal `check` node of its run — a verify is a
per-run gate in these graphs, never a per-node one. That is why 44 of the 49 are
uncovered: there is at most one covered node per run to begin with.

### Per run

| run | nodes | with transcript | denied | denied & PASS | of those, `verify` |
| --- | --- | --- | --- | --- | --- |
| `20260802-125517.456024000-1` | 2 | 2 | 0 | 0 | 0 |
| `20260802-125603.154344000-2` | 2 | 2 | 0 | 0 | 0 |
| `20260803-081608.190042000-1` | 5 | 5 | 3 | 3 | 0 |
| `20260803-081635.836216000-1` | 4 | 4 | 3 | 3 | 0 |
| `20260803-084651.244624000-1` | 4 | 4 | 4 | 4 | 0 |
| `20260803-084704.248072000-1` | 6 | 6 | 6 | 5 | 0 |
| `20260812-125543.322191000-1` | 2 | 2 | 0 | 0 | 0 |
| `20260818-234944.646288000-1` | 6 | 6 | 5 | 5 | 1 |
| `20260819-154136.440217000-1` | 6 | 4 | 2 | 1 | 0 |
| `20260819-161543.550073000-1` | 2 | 2 | 0 | 0 | 0 |
| `20260819-163447.441137000-2` | 5 | 5 | 5 | 5 | 1 |
| `20260819-175025.348460000-1` | 6 | 6 | 2 | 1 | 0 |
| `20260819-181003.413336000-2` | 5 | 5 | 5 | 5 | 1 |
| `20260819-223604.575080000-1` | 5 | 5 | 5 | 4 | 0 |
| `20260820-162555.890191000-1` | 6 | 0 | 0 | 0 | 0 |
| `20260820-162820.837039000-2` | 5 | 5 | 4 | 4 | 1 |
| `20260820-163530.563884000-1` | 5 | 5 | 5 | 5 | 1 |
| `20260820-172509.298782000-2` | 5 | 5 | 4 | 4 | 0 |
| **total** | **81** | **73** | **53** | **49** | **5** |

## Missing transcripts

**2 nodes had a session id on the record but no transcript on disk** — no
`~/.claude/projects/*/<session_id>.jsonl` matched. Rotated, or never written;
the record does not say which.

| run | node | session | verdict |
| --- | --- | --- | --- |
| `20260820-162555.890191000-1` | `corpus` | `21f5446f-6b30-4b45-bcd0-05a290d7c9a2` | FAIL |
| `20260820-162555.890191000-1` | `precedent` | `a889a07f-fd17-4dd7-9513-516f6be5a53d` | FAIL |

**They are excluded from the numerator and the denominator both.** A node whose
transcript cannot be read is not a node that was not denied; it is a node
nothing is known about, and counting it as a negative would understate (a).
Two further exclusion buckets, on the same principle:

| bucket | count | why |
| --- | --- | --- |
| missing transcript (above) | 2 | no file matched |
| record present, `session_id` empty | 3 | no transcript addressable at all |
| no record in `state.nodes` | 3 | the run halted before the node was reached |
| transcript matched but unreadable | 0 | — |

81 − 2 − 3 − 3 = 73, the denominator. The transcript walker also reports 0
lines that would not decode as JSON and 0 sessions matching more than one
project directory, so nothing was silently dropped inside a file that *was*
read.

**None of the 8 excluded nodes is a `PASS`** — the five that ran are all `FAIL`,
and the three with no record never ran. So the exclusions cannot be hiding a
denied-and-passed node: (b) and (c) are unaffected by them, and only (a) could
move, and only upward, to at most 55 of 75.

Transcripts are located by filename glob on the session id and never by
reconstructing the project slug, because the slug is a lossy encoding of a
working directory (worktrees, `/private/var`, symlinks) and rebuilding it is how
a measurement invents a file that is not there.

## Caveats

**There is no rate in this document, and that is deliberate.** 53 of 73 is
72.6%, and that percentage should not be written down or quoted. Reasons, in
order of weight:

1. **The unit of independence is the run, not the node.** The 73 nodes come
   from 18 runs, and denial within a run is driven by one `allowed_tools`
   ceiling written by one planner for one operator in one session. Six of the
   18 runs are 100% denied and six are 0% denied; that bimodality is the
   signature of a per-run cause, not 73 independent trials. The effective n is
   nearer 18 than 73, and 18 self-selected runs support raw counts, not a rate.
2. **It is a self-measurement** (see above). A rate implies a population. The
   population here is one maintainer's habits.
3. **The corpus is not stationary.** The runs of 08-02 and 08-03 predate
   `--verify-cmd` entirely, so they could not have carried a `verify` even in
   principle; and four of the 18 were made in the 24 hours before the
   measurement, by work aimed at this very issue.
4. **The numbers are a floor.** See the next two bullets.

Further caveats:

- **The transcript glob misses subagent and workflow transcripts.**
  `~/.claude/projects/*/<session_id>.jsonl` matches only top-level session
  files, but denials also occur in `…/<session>/subagents/*.jsonl` and
  `…/workflows/*/`. A node whose subagent was denied while its own top-level
  session was not is counted here as undenied. (a) is a floor for this reason as
  well as for the two missed denial classes.
- **The corpus grows while it is read.** Totals drift between passes because
  sessions are appended during the measurement. The evidence file is a snapshot
  taken during run `20260820-174446.982693000-1`, not an invariant, and a re-run
  on a later day is expected to differ.
- **A denial is not a defect.** Many of the 185 are an agent trying `gofmt` or
  `make fmt-check` outside its grant, being refused, and doing the right thing
  by another route. This measurement counts denials; it does not establish that
  any node's *work* was incomplete. Establishing that would require reading the
  49 nodes' outputs against their goals, which is a separate and much larger
  job.
- **`provenance` was carried as a corroborator only.** The (c) split is decided
  by the graph's `success_check`, which is what #218 asks about; the recorded
  provenance agrees with it row by row in the evidence file.
- **One stray file, and it is on topic.** The discriminator node wrote
  `/tmp/omg-denials/scan.go` into the repository root before moving its scratch
  work to `/tmp/scratch-218/`, and **its `rm` of that file was itself denied,
  twice**, so the file is still there. It is untracked and carries a
  `//go:build ignore` tag so it cannot affect a build. It wants deleting by
  hand. The phenomenon under measurement leaving litter in the repository
  because the cleanup was refused, and the node reporting success anyway, is
  this issue in miniature.

## How to recompute

From the repository root:

```
go run docs/measurements/0218-denied-nodes-that-passed.go
```

`go run` with an explicit file argument does not apply build constraints, which
is why the file's `//go:build ignore` tag is safe: it keeps the measurement out
of `go build ./...` and `go test ./...` while leaving it runnable. Same
convention as `docs/measurements/0213-tool-grant-predicate.go`.

The script prints the summary to stdout and rewrites
`docs/measurements/0218-denied-nodes-raw.json` with one row per planned node.
**Run it twice and compare: every printed number must be identical.** That check
is what catches the in-flight class of defect described above, and it is the
reason the first pass's unreproducible 54-of-74 was caught rather than
published.

Two facts about re-running it, both expected:

- Run it **outside** the engine. Run it as a node of a graph and that run is
  correctly excluded as in flight, so the run you are watching contributes
  nothing.
- The totals will not match this document on any later day. The corpus is live.
  What should still match is the 18-run planned corpus, unless new planned runs
  have happened since.

## Recommendation

**Lean on `success_check.verify`. Treat denial detection, by whatever transport,
as an advisory signal that is never an input to a verdict.**

From the numbers actually in hand:

- **Rule out stream-json on the evidence, not on taste.** The discriminator
  established that there is *no structural marker* — every `is_error`
  `tool_result` in the corpus has the identical key set. `--output-format
  stream-json` changes when the engine sees the blocks, not what is in them. It
  would deliver the same prose, live, and would buy the engine nothing it
  cannot already get by reading the file afterwards. This is the one option the
  measurement positively excludes.
- **Reading the node's transcript and matching on prose are the same option.**
  Reading the transcript is a transport; the thing that interprets what it reads
  is the prose predicate. So the real choice is two-way: make a prose match
  load-bearing, or make the engine's own command load-bearing.
- **A prose match must not be load-bearing.** It is pinned to one CLI version's
  wording, and it already misses two real denial phrasings present in this
  corpus (classes A and B). A verdict that silently stops firing on a CLI
  upgrade is worse than no verdict, because nobody will notice.
- **The exposure the numbers show is not really "denial".** 44 of 49 is
  arresting, but so is the underlying figure: only 8 of the 73 nodes had *any*
  engine-run check, denied or not. The denied-and-passed nodes did not pass
  because they were denied; they passed because nothing but their own word was
  ever asked of them. Denial made that visible. Fixing the visibility does not
  fix it; extending `verify` past the terminal `check` node does.

So: put the effort into `verify` coverage, and if a denial signal is wanted, ship
it as a warning line in the run report — "this node was denied *n* tool calls
and passed on `result_matches`" — where a wording drift degrades to a missing
warning rather than a wrong verdict.

**What would change this recommendation:**

- **A denied node that passed its `verify` but whose work was in fact
  incomplete.** Currently 0 observed out of 5 — but 5 is far too few to have
  looked, and nobody has checked those 5 against their goals. Even one such node
  breaks the premise that a verify is sufficient, and the recommendation would
  become "verify *and* an advisory denial signal, both".
- **Evidence that per-node `verify` is impractical.** The case rests on being
  able to raise coverage above today's 8 of 73. If an attempt to write a verify
  on non-terminal nodes shows that most node kinds have no cheap checkable
  postcondition — say, coverage stalls below a third of nodes — then the
  uncovered majority needs *some* signal, and a prose match as an advisory
  becomes the primary deliverable rather than a garnish.
- **A structural marker appearing in the CLI record.** If a future version emits
  a typed field for a permission denial, "read the transcript" becomes cheap and
  reliable, and it should be adopted immediately — as a signal, still not as a
  verdict.
- **A much lower (b).** If a re-measure on a corpus with better verify coverage
  showed denied-and-passed nodes to be rare in absolute terms, the issue drops
  in priority. It currently reads 49, so it does not.

## CHANGELOG

**No entry.** This is a measurement. No product code changed, no behaviour
changed, and nothing here is observable to a user of the tool — the only new
files are this document, its evidence JSON and the script that produced them,
all under `docs/measurements/`, and the script is excluded from the build by
`//go:build ignore`. The recommendation above may lead to a change that warrants
an entry; that entry belongs to that change, not to this reading.
