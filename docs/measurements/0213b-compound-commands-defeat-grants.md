# 0213b — a compound command defeats a prefix grant 64 times in 246, and it is not why most calls were denied

**Of the 246 Bash calls denied inside a planned node in this corpus, 64 (26.0%)
are the #213 compound shape — the first sub-command granted, a later one not.
162 (65.9%) were out of scope from their own first word and would have been
denied compound or not. 20 (8.1%) held a grant for every sub-command and were
denied anyway, and 17 of those 20 are not compound at all.**

Every figure in this document is read from
[`0213b-results.json`](0213b-results.json), which the program below writes. The
JSON key is named beside each number the first time it appears.

**No CHANGELOG entry accompanies this measurement: it changes no product
behaviour, ships nothing an operator can observe, and a changelog that records
readings rather than changes stops being a changelog.**

## What was asked, and what prompted it

A lane running the `measure` node of run `20260820-194234.260120000-2` burned
**$34.70 and produced no deliverable**. It held `Bash(go *)` and `Bash(ls *)`
among its grants and was denied on every Bash call it made. The suspicion, filed
as #213, was structural rather than local: the node wrote

```
go build ./... 2>&1 | head -50; echo "BUILD_EXIT=$?"
```

— a command whose *first* sub-command is exactly what the grant covers, and
whose later sub-commands (`head`, `echo`) were never granted. If that shape is
what denies planned nodes in general, the defect is not a planner that grants
too little; it is nodes writing shell that no reasonable grant list can match.

The question put to this measurement: **across the whole local run corpus, how
many denied Bash calls in planned nodes have that shape, and how many have some
other explanation?** Every denied call is classified into exactly one of:

- **(A)** first sub-command granted, a later one not — the #213 compound shape;
- **(B)** the command's own first word was never granted — out of scope from the start;
- **(C)** every sub-command granted, denied anyway — unexplained by the grant list;
- **(D)** command empty or unsplittable — no sub-command to judge;
- **(E)** the node's grant list is not recoverable from `graph.json`.

(D) and (E) exist so that a call this program cannot judge is *counted* rather
than absorbed into a bucket it did not earn. Both are **0**
(`classes.D`, `classes.E`).

### Both hand-known calls came back as expected

The `measure` node above has **exactly two** denied-call records in the JSON,
and they are the two that were known by hand before the program ran:

| command | class | what the splitter found |
| --- | --- | --- |
| `go build ./... 2>&1 \| head -50; echo "BUILD_EXIT=$?"` | **A** | 3 sub-commands: `go build ./... 2>&1` **granted**, `head -50` not, `echo "BUILD_EXIT=$?"` not |
| `ls -d ~/.oh-my-graph/runs` | **C** | 1 sub-command, `ls` **granted**, `path_scope: reaches-outside-cwd` |

One correction to the brief, which changes neither verdict: the node's recorded
grants are the superset `[Read, Grep, Bash(go *), Bash(ls *), Bash(grep *)]`,
not `Bash(go *)` and `Bash(ls *)` alone. `state.tool_policies` agrees with
`graph.json` for this node (`tool_policies_agrees_with_graph_json: true`) and no
bare `Bash` deny is present (`bare_bash_in_disallowed_tools: false`).

## The join method

**A denial is joined to its command by `tool_use_id`, never by adjacency.** A
`tool_result` block names the `tool_use` block it answers; the program builds an
id → call map and looks each result up in it. Adjacency would be wrong on this
data specifically: the CLI splits one assistant message across several JSONL
lines, and interleaves attachment / last-prompt / ai-title records between a
call and its result, so "the command on the line above" is frequently not the
command that was denied. A `tool_result` whose id matches no `tool_use` is a
data defect, counted and reported rather than dropped —
`tool_results_with_no_matching_tool_use: 0`, of which denial-shaped: **0**
(`tool_results_with_no_matching_tool_use_that_were_denials`).

Two further discriminations sit on top of the join:

- **`is_error` is not denial.** An ordinary tool failure also sets `is_error`
  but is wrapped in `<tool_use_error>`. A permission denial is unwrapped and
  opens with `"Permission to use "`, matched **at offset 0** — never by
  substring-searching for "denied", which also matches model prose quoting a
  denial back into an artifact, and matches this repo's own sessions grepping
  for the denial sentence.
- **The corpus is parsed, never grepped.** `encoding/json` over every JSONL
  record, standard library only, no `os/exec`, no file cap.

### The planned-run predicate, stated exactly

> A run is **planned** exactly when its `state.json`'s `graph_source_path`,
> after `filepath.Clean` and `filepath.EvalSymlinks`, is **the same file** as
> that run's own `graph.json`, compared by `os.SameFile` (device + inode).

A hand-written `run` points at a `.yaml` elsewhere in the filesystem; a planned
`auto` run points at the `graph.json` the planner wrote into the run directory.
Comparing by device+inode rather than by string survives `/var` vs
`/private/var` and every other spelling of the same path.

**Provenance: this predicate was NOT newly written for this measurement.** The
`sameFile` function is copied verbatim, as agreed in the brief, from two files
where it is byte-identical — and they sit on **two different branches**:

| file | commit | branch |
| --- | --- | --- |
| `docs/measurements/0213-tool-grant-predicate.go` | `b1a55ba` (#213) | `measure/tool-grant-predicate` |
| `docs/measurements/0218-denied-nodes-that-passed.go` | `0736635` (#218) | `measure/denied-nodes` |

Neither commit is an ancestor of this branch's HEAD, which is why globbing
`docs/measurements/*.go` in the working tree reports both files absent; both
were read out of the object store. The three measurements therefore share **one**
corpus definition rather than three that drift apart. (An earlier draft of the
program header placed both commits on `measure/denied-nodes`. That was false,
and `git branch -a --contains b1a55ba` disproves it in one command.)

Grants are read from each run's **own `graph.json`**, as briefed.
`state.json`'s `tool_policies` is read alongside purely as a cross-check, so
that the weaker of the two records is never trusted silently. They disagree
nowhere: `nodes_where_tool_policies_and_graph_json_disagree: 0`,
`nodes_with_no_tool_policies_entry: 0`.

## The corpus snapshot

> **This is a SELF-MEASUREMENT on a live corpus.** The runs directory it reads
> is the same one this lane and every other lane on this machine are still
> writing into. Every number below is a **snapshot**, taken at the timestamp in
> `snapshot_taken_at`, and **will move** on the next run of the program. It is
> not an invariant, and a later re-run disagreeing with this document is the
> expected outcome, not a defect.

| | | JSON key |
| --- | --- | --- |
| Snapshot taken at | **2026-08-20T20:49:04Z** | `snapshot_taken_at` |
| Run directories walked | **323** | `counts.run_directories` |
| Excluded — this lane's own in-flight run | **1** (`20260820-195627.390122000-1`) | `counts.excluded_own_run` |
| No `state.json` at all | 1 | `counts.runs_with_no_state_json` |
| `state.json` present but unparseable | 0 | `counts.runs_with_unparseable_state_json` |
| NOT planned (hand-written `run`) | 299 | `counts.runs_not_planned` |
| **PLANNED runs — the measured population** | **22** | `counts.runs_planned` |
| Node records in those planned runs | 95 | `counts.node_records_in_planned_runs` |
| … with a transcript parsed | 90 | `counts.nodes_with_transcript_parsed` |
| Bash `tool_use` blocks seen | 1368 | `counts.bash_tool_use_blocks` |
| `tool_result` blocks seen | 2706 | `counts.tool_result_blocks` |
| **DENIED Bash calls in planned nodes** | **246** | `counts.denied_bash_calls_policy` |

The excluded run is this lane's own (`compound-commands-defeat-grants`). It was
in flight while the program ran, so including it would have made the corpus
measure the measurement and report a figure changing under its own feet. It is
also the **lexically newest run id**, which is the trap: a "skip the newest run"
heuristic selects exactly the run that must go, and nothing else.

All 246 are policy denials. Counted separately and both zero: refusals by an
**interactive human** (`denied_bash_calls_user_rejection: 0` — a planned node
runs unattended under `dontAsk` with nobody to press "no", which is why this is
counted rather than assumed away), denials of a **non-Bash** tool
(`denials_of_a_non_bash_tool: 0`), and denials inside a **sub-agent sidechain**
(`denials_inside_a_subagent_sidechain: 0` — a sidechain runs under its own
policy, not the node's grant list, so it cannot be judged against that list).

Derived from the `denied_calls` records rather than printed by the program: the
246 calls are spread over **69 distinct `(run_id, node_id)` pairs in 17 of the
22 planned runs**, counted by enumerating those two fields across all 246
records.

## The classification

Denominator = **246** (`denominator_denied_bash_calls_in_planned_nodes`); the
counts are `classes.A` … `classes.E`.

| class | count | share | what it means |
| --- | --- | --- | --- |
| **(A)** | **64** | **26.0%** | The first sub-command matched a grant and a later one did not — the node meant to run something it was allowed to run, and lost the call to a filter or an `echo` appended to it. The #213 shape. |
| **(B)** | **162** | **65.9%** | The command's own first word matched no grant the node held. Out of scope from the first token; splitting or not splitting changes nothing about it. |
| **(C)** | **20** | **8.1%** | Every sub-command matched some grant, and the call was denied anyway. Neither the grant list nor the compound hypothesis explains it. |
| (D) | 0 | 0.0% | Command empty or unsplittable — no sub-command to judge. |
| (E) | 0 | 0.0% | Node's grant list not recoverable from `graph.json` — cannot classify. |
| **total** | **246** | 100.0% | buckets sum to the denominator |

Independently of bucket, **169 of 246 (68.7%)** of the denied commands were
compound — more than one sub-command after splitting
(`denied_commands_that_were_compound`). Being compound is therefore common; it
is just not what decided most of these denials.

## The class-(A) offending sub-commands — a SHORT tail

25 distinct first words turned an otherwise-granted command into (A), across 155
offending occurrences in the 64 calls
(`class_a_offending_first_words`):

| n | first word | | n | first word |
| ---: | --- | --- | ---: | --- |
| 62 | `echo` | | 2 | `cat` |
| 19 | `tail` | | 2 | `printf` |
| 18 | `wc` | | 2 | `sed` |
| 13 | `grep` | | 2 | `stat` |
| 12 | `head` | | 1 each | `./tmp_measure/omg`, `/tmp/omg-ldflags-check`, `awk`, `command`, `diff`, `gh`, `go`, `gofmt`, `ls`, `rm`, `sleep`, `uniq` |
| 3 | `n=` | | | |
| 3 | `read` | | | |
| 3 | `sort` | | | |
| 2 | `/tmp/omg200` | | | |

**Judgement: SHORT.** Five output filters — `echo`, `tail`, `wc`, `grep`,
`head` — account for **124 of the 155** offending occurrences (80%), and `echo`
alone is 62 of them (40%). The twelve singletons are a thin scatter, and several
of them (`go`, `gofmt`, `ls`, `gh`) are not unfamiliar tools at all but
narrower-grant misses — a node holding `Bash(gh pr *)` writing some other `gh`
subcommand, and similar. That distribution is the practical finding: a small
fixed set of output filters covers most of what class (A) trips over, without
teaching the matcher anything about shell grammar.

**The 124 is a count of occurrences, not of calls.** A call is only released by
granting those five if *every* one of its offenders is among them, and a call
with a sixth offender stays denied. The program prints the occurrence tail and
not the per-call figure, so the number of class-(A) calls that five grants would
actually retire is **not measured here**; 124/155 is the ceiling on it, not the
value.

## The class-(C) reckoning — compound-ness is not the whole story

Twenty calls held a grant for every sub-command and were denied anyway. **They
must not be assumed away**, and the known case is the reason: the same
`measure` node that produced the class-(A) exemplar also ran

```
ls -d ~/.oh-my-graph/runs
```

— one sub-command, first word `ls`, holding `Bash(ls *)` — and it was denied
with **byte-identical** text to the compound call. Whatever denied it was not
compound-ness, because there was nothing to compound.

Read against the JSON, class (C) does not split the way the compound hypothesis
predicts:

- **17 of the 20 are not compound at all** (`compound: false`). Only 3 are.
- By path scope (`class_c_by_path_scope`): **8** reach outside the node's
  working directory, **12** have no path arguments whatsoever, and **0** have
  all their paths inside the working directory.

The eight outside-cwd ones are, in full, every one of them simple:
`cat /Users/imac/.oh-my-graph/runs/…/config.out` under `Bash(cat *)`;
`ls -la ~/.git-hooks`, `ls -1 /Users/imac/.claude/projects`,
`ls -1 /Users/imac/.oh-my-graph/runs`,
`ls -la /Users/imac/.oh-my-graph/runs/20260820-194234.260120000-2` and
`ls -d ~/.oh-my-graph/runs` under `Bash(ls *)`; and two whose *redirect target*
is the only thing outside the directory —
`git status --porcelain > /tmp/omg-204-probe.txt` under `Bash(git *)` and
`go run docs/measurements/0218-denied-nodes-that-passed.go > /tmp/0218-run2.txt`
under `Bash(go *)`.

The twelve with no path arguments are nine calls from a single node
(`20260819-163447.441137000-2`/`pr`) retrying `gh pr create` under
`Bash(gh pr *)` with a multi-kilobyte `--body` full of backticks, newlines and
`$'…'` quoting; and three `git add X && git commit -q -F - <<'EOF' … EOF &&
git log --oneline -1` calls under `Bash(git *)` in which **every** sub-command
is `git`.

**What (C)'s size implies.** At 20 of 246 it is small, but it is not noise, and
its internal shape is the point: the discriminator inside (C) is not the number
of sub-commands but the **argument text and path scope**. A giant quoted body, a
heredoc, or a path outside the working directory each defeats a prefix grant on
its own. A change that only taught the matcher to split on `|` and `;` would
leave all twenty of these still denied.

That is the second of two reasons compound-ness is not the whole story. The
first is larger: **two thirds of every denial in this corpus (class B, 162/246)
was out of scope from its first word** and would have been denied whatever its
shape.

The program's own cross-tab makes the confound explicit — path scope moves
*with* compound-ness across the whole denominator (`path_scope_cross_tab`):

| path args | compound | simple |
| --- | ---: | ---: |
| reaches-outside-cwd | 85 | 29 |
| all-paths-inside-cwd | 24 | 6 |
| no-path-args | 60 | 42 |

Because the two factors move together, an A-vs-B ratio on its own cannot
separate them. Path scope here is a heuristic on argument **shape** — a token
starting with `/`, `~`, `./` or `../`, resolved against the node's cwd and
compared by path segment — not a resolution of what the shell would really open.

## Caveats

### 1. The grant-matching rule is an ASSUMPTION, and the uncertainty is named

How Claude Code actually applies `Bash(go *)` lives in the Claude Code binary.
**No source in this repository implements it.** The rule the whole
classification rests on is therefore assumed, stated in full in the JSON under
`assumed_grant_matching_rule`, and implemented in exactly one function
(`grantMatches`) so a reviewer who disagrees can change it in one place:

- `Bash(P)` applies only to Bash calls; `Read`, `Grep`, `Glob`, `Skill` … never
  match a sub-command.
- `P` ending in `" *"` is a **token prefix**: `Bash(go *)` matches a
  sub-command whose first token is `go`; `Bash(gh pr *)` requires the first two
  tokens to be `gh` then `pr`. The `*` is not matched character-wise.
- `P == "*"` matches everything. A bare `Bash` with no parentheses matches
  everything. `P` with no `*` is an exact whole-command token match.
- Matching is on tokens, never the raw string.

**What the rule deliberately does not model, because it is unknown:** whether
the real matcher splits a compound command at all or matches the raw string as
one unit; whether an unmatched later sub-command denies the whole call; whether
a working-directory or sandbox constraint applies to path arguments *in
addition* to the pattern; and whether a given denial arose from
failure-to-match, an explicit deny rule, or a sandbox check.

That last one is the governing caveat of the entire document, and it is the
first paragraph of both output files: **the denial text carries no reason
code.** It is byte-identical for a compound call, a simple out-of-scope call and
a sandbox refusal. Every class here is a **correlation between command shape and
denial**, never a cause.

What the repository does pin down, measured rather than read from source, is
that a planned node is **default-deny** (`DESIGN.md:106-110`: an unmatched call
resolves to *ask*, and under `dontAsk` an unanswerable ask becomes a deny), so
"denied" means "no rule matched" — indistinguishable on disk from the other two
causes. `DESIGN.md:113-116` records that a bare `Bash` in `disallowed_tools`
beats every allow a node holds; `denials_under_a_bare_bash_in_disallowed_tools`
is **0**, so no call here is explained that way.

Finally: **the `claude` version that produced this corpus is unknown.** No run
record carries it, and asking this machine would report today's version rather
than the corpus's. If grant matching changed across versions, the corpus is
silently two populations.

The sub-command splitter has its own stated limits (`sub_command_splitter` in
the JSON) — process substitution, a bare `&`, `$(( ))`, `$'…'` quoting, and
wrapper commands like `xargs`/`sh -c`/`find -exec` are all declared as ways a
command may be misclassified. One limit was a live defect and is worth naming:
the first cut of the program scanned **heredoc bodies as if they were shell**,
and class (A)'s offender table filled with `Co-Authored-By:`, `EOF`, `package`,
`import` and loose prose — commit messages and Go source shredded into imaginary
sub-commands, every one of them inflating the very bucket the measurement exists
to size. Heredoc bodies are now consumed as data:
`heredoc_bodies_consumed_as_data: 19`, `pieces_dropped_as_pure_shell_grammar: 16`.

### 2. Every missing-data bucket, reported as a count

None of these was dropped silently.

| bucket | count | JSON key |
| --- | ---: | --- |
| Nodes with a session id but **no transcript on disk** | **2** | `counts.nodes_with_session_id_but_no_transcript_file` |
| Nodes with **no session id at all** | **3** | `counts.nodes_with_no_session_id` |
| Nodes sharing a session with an earlier node | 0 | `counts.nodes_sharing_a_session_with_an_earlier_node` |
| Node records absent from their own `graph.json` | 0 | `counts.node_records_absent_from_graph_json` |
| Graph nodes that never ran | 6 | `counts.graph_nodes_with_no_state_record` |
| Runs with **no `graph.json`** | 299 | `counts.runs_with_no_graph_json` |
| Runs whose `graph.json` would not parse | 0 | `counts.runs_with_unparseable_graph_json` |
| Transcripts that could not be read | 0 | `counts.transcript_files_unreadable` |
| JSONL lines that were not valid JSON | 0 | `counts.jsonl_lines_that_were_not_valid_json` |
| `tool_result` with no matching `tool_use` | 0 | `counts.tool_results_with_no_matching_tool_use` |
| **Errored results with an UNRECOGNISED wording** | **12** | `counts.errored_results_with_an_unrecognised_wording` |

All five affected nodes in the first two rows are in one run,
`20260820-162555.890191000-1` (`corpus` and `precedent` have session ids with no
file; `handcheck`, `predicate` and `writeup` have no session id). The 299 runs
with no `graph.json` are every hand-written `run` — a hand-written run writes
none, which is also why none of them can satisfy the planned predicate.

**The 12 unrecognised wordings were all read**, because a third denial template
would silently shrink the denominator. None of them is a denial: 7 are ordinary
`Bash: Exit code 1` failures (`cat: illegal option`, `ls: no such file`, a Go
build error), 2 are `Glob:` errors, 1 is a `Grep:` error, and 2 are
`Read: File does not exist`. Two of them —
`Glob: EACCES: permission denied, posix_spawn 'rg'` and its `Grep:` twin — *are*
sandbox refusals in a different shape, but they are refusals of **non-Bash**
tools and so fall outside this denominator either way. **The 246 is not
understated.**

One cross-check is reported and deliberately changes no count: #218's general
in-flight predicate (a graph node with no `state.nodes` entry, and no `FAIL`
anywhere) flags **1** run, `20260820-202530.976203000-2`
(`missing_data.in_flight_cross_check_reported_only`). This measurement's corpus
is decided by run id, as briefed, so that run stays in.

### 3. What this replaces — the superseded crude scan

An earlier, crude scan of the same question reported **43 compound vs 8 simple**
denied commands. It is superseded by this document and should not be cited. It
was wrong in three ways at once, and each one inflated the compound share:

| | superseded scan | this measurement |
| --- | --- | --- |
| join | **by adjacency** — the command nearest the denial | **by `tool_use_id`** |
| population | **unrestricted** — hand-written runs included | **planned runs only**, by the shared `sameFile` predicate |
| coverage | **capped at 400 transcript files** | **no cap** — every run directory, every transcript |
| result | 43 compound vs 8 simple (n = 51) | **169 compound vs 77 simple (n = 246)** |
| compound share | 84.3% | **68.7%** |

The corrected denominator is nearly five times larger, and the compound share
falls by 16 points. More to the point, the crude scan produced *no* A/B/C split
at all, so it could not distinguish a compound command that lost a grant (64)
from one that never had it (162) — which is the distinction the whole question
turns on.

## How to recompute

```
go run docs/measurements/0213b-compound-commands.go
```

It takes **no arguments** and writes its own two output files, so a caller under
a tool grant that forbids shell redirection needs none:

- [`docs/measurements/0213b-results.json`](0213b-results.json) — every count
  above, one record per denied call, and the wall-clock snapshot timestamp.
- [`docs/measurements/0213b-results.txt`](0213b-results.txt) — the human
  summary, also printed to stdout.

The `.txt` and stdout are **deterministic**: no timestamps, no map iteration
order, every list sorted. Two runs over an unchanged corpus produce
byte-identical files, so re-running and diffing the `.txt` is a real check on
the corpus. The timestamp lives only in the `.json`, which is what keeps that
byte-comparison available. The program carries an `ignore` build tag, so it
stays out of `go build ./...` and `go test ./...`; `go run` on an explicit file
path does not apply build constraints. Set `OMG_HOME` to point it at a different
runs root.

**Expect different numbers.** Any lane that has run since 2026-08-20T20:49:04Z
has added to this corpus.

## Recommendation

**Fix class (A) cheaply, and do not act on (C) yet.** (A) is real at 64 calls
and 26.0% of the denominator, but it does *not* dominate — (B) is two and a half
times its size, and (B) is the ordinary case of a node reaching for a tool it
was never given, which is a planner-scope question and not a shell-parsing one.
Within (A) the tail is short enough to attack by hand: granting `echo`, `tail`,
`wc`, `grep` and `head` to every planned node covers 124 of the 155 offending
occurrences, at a cost of five read-mostly filters and no change to how grants
are matched — worth doing, with the caveat above that occurrences are not calls
and the number of calls it releases was not measured. That is the whole of what
this measurement supports. It does **not** support the stronger #213 reading
that planned nodes are systematically writing shell no reasonable grant list can
match: most of them are simply writing commands they were not granted. Class (C)
is where the honest answer is **not enough data** — 20 calls, 17 of them not
compound, split between a giant-argument shape, a heredoc shape and a
path-scope shape, with a denial string that will never say which mechanism
fired. Neither "teach the matcher to split compound commands" nor "grant the
nodes more" is supported by those twenty, and settling them needs an experiment
that varies one factor at a time against a live CLI — not more reading of this
corpus, which cannot answer it however long it grows.
