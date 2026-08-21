# The #213 candidate predicate is wrong 110 times out of 114 to be right once — NOT A LINT

**Verdict: this predicate does not ship.** Over the 16 planned graphs on this
machine (71 nodes), the candidate — *"the prompt tells this node to run a
command its `allowed_tools` does not permit"* — produces **114 hits, of which a
hand-check of every one finds 4 real and 110 noise**. All 4 real hits are the
same command (`gofmt -l internal/serve`) in the same run, so the predicate found
**one** distinct defect. It does find the case that motivated #213 — recall is
not the problem — but it cannot tell a command from a noun, and 81 of the 110
noise hits are exactly that failure: a backticked *identifier* (a subcommand
name, a branch name, a filename, a JSON field, a Go token, a CI job name) read
as an invocation. The precedent this repo shipped a lint on, #154, was **61 real
of 62** (`internal/handoff/tool_grant_lint.go:23-30`). This is the inverse.

- **Date:** 2026-08-21 (KST), macOS (darwin 22.6.0), one machine. The newest run
  in the corpus is `20260820-162820.837039000-2`.
- **Corpus:** `~/.oh-my-graph/runs` (`OMG_HOME` unset) — 316 run directories, of
  which **16 are planned graphs** carrying **71 nodes**.
- **Method:** [`0213-tool-grant-predicate.go`](0213-tool-grant-predicate.go),
  standard library only, `encoding/json` walking the actual snapshot and graph
  objects. Never `grep` — the repo has a written scar for that
  (`0213-tool-grant-predicate.go:15-18`).
- **Cost:** zero `claude` spawns for the predicate run itself. 316 directory
  reads and a parse.
- **Machine-readable hits:** [`0213-hits.json`](0213-hits.json) — 114 entries,
  each with run id, node id, command, the full span, ~200 characters of
  surrounding prompt, the grants the node held, and an occurrence count.
- **The hand-check is a human reading, not a computation.** Its 114 verdicts
  were made by reading each hit-bearing `graph.json` directly. Reproduce the
  hits mechanically; the REAL/NOISE column is a judgement and is argued row by
  row below.

## 1. What was measured, and why

Issue #213 asks whether the observable half of #154 can be pushed one step
further. `handoff.LintToolGrants` today warns only when a node declares
**nothing** — no `allowed_tools` and no `success_check.verify`
(`internal/handoff/tool_grant_lint.go:54-73`). It says nothing about whether a
declared grant is *adequate* to what the prompt demands. The candidate predicate
is that missing check.

The motivating case is run **`20260819-223604.575080000-1`**
(`serve-port-holder-identity`, 5 nodes). Its `verify` node was told:

> `6. TESTS — `go test ./internal/serve/... -race -count=1` passes, and
> `gofmt -l internal/serve` prints nothing.`

— quoted from that node's prompt via `0213-hits.json` (entry
`20260819-223604.575080000-1 | verify | gofmt`,
`docs/measurements/0213-hits.json:1298-1312`). Its `allowed_tools` are
`Read, Grep, Bash(git *), Bash(go *), Bash(grep *)` — **neither
`Bash(gofmt *)` nor `Bash(make *)`**, so the check it was hired to perform was
not reachable. The graph that commissioned this measurement states the price:
*"the run failed after $8.35 of completed work"*
(`docs/measurements/0213-hits.json:1504`, the `handcheck` node's own prompt in
run `20260820-162555.890191000-1`). That figure is the operator's statement in
the measurement brief; it was **not** re-derived from the run ledger here, and
nothing below depends on it.

## 2. How to recompute it

From the repository root:

```sh
go run docs/measurements/0213-tool-grant-predicate.go
```

It prints sections A–C below to stdout and rewrites
`docs/measurements/0213-hits.json`. The `//go:build ignore` tag
(`0213-tool-grant-predicate.go:1`) keeps the file out of `go build ./...`,
`go vet ./...` and `go test ./...`; `go run` with an **explicit file argument**
does not apply build constraints, which is why the tag is safe and no `/tmp`
copy is needed (`0213-tool-grant-predicate.go:10-13`).

**The planned-run selection rule, stated precisely.** A run directory is
PLANNED exactly when its `state.json` field `graph_source_path`, after
`filepath.Clean` and `filepath.EvalSymlinks`, is **the same file** as that run's
own `graph.json` — compared with `os.SameFile`, i.e. by device+inode, so the
test survives `/var` vs `/private/var` and every other path spelling
(`0213-tool-grant-predicate.go:75-100`). A hand-written `run` points its
`graph_source_path` at a `.yaml` elsewhere in the filesystem; an `auto` run
points at the `graph.json` the planner wrote into the run directory itself.

**A caveat about that rule on this corpus, stated where the rule is and not
three paragraphs below it:** it did no work here. Of the 300 skipped
directories, **299 were skipped for having no `graph.json` at all**, which is
precisely what a hand-written `run` produces — so the skip bucket and the
hand-written population are the same set, and the `0` in "readable, has a
graph.json, but FAILS the planned test" is **not** evidence that the test
discriminates. No run ever reached that comparison and failed it. On this
corpus, "is planned" and "has a graph.json" are the same predicate.

## 3. The corpus (section A of the script's output)

```
run directories seen:            316
  skipped:                       300
      no state.json                                  1
      no graph.json (a hand-written run writes none) 299
      state.json or graph.json would not parse       0
  readable, has graph.json, but FAILS the planned test: 0
  PLANNED graphs:                                       16
```

| planned run id | graph name | nodes |
| --- | --- | ---: |
| `20260802-125517.456024000-1` | two-files-two-runs | 2 |
| `20260802-125603.154344000-2` | finish-beta-file | 2 |
| `20260803-081608.190042000-1` | serve-gate-approval | 5 |
| `20260803-081635.836216000-1` | resume-noweb-liveview | 4 |
| `20260803-084651.244624000-1` | add-goreleaser-release | 4 |
| `20260803-084704.248072000-1` | embed-examples-init | 6 |
| `20260812-125543.322191000-1` | readme-note | 2 |
| `20260818-234944.646288000-1` | fix-200-help-swallowed | 6 |
| `20260819-154136.440217000-1` | serve-build-meta | 6 |
| `20260819-161543.550073000-1` | hello-file | 2 |
| `20260819-163447.441137000-2` | serve-build-meta-finish | 5 |
| `20260819-175025.348460000-1` | docs-drift-audit-v010 | 6 |
| `20260819-181003.413336000-2` | docs-drift-finish-v0-10 | 5 |
| `20260819-223604.575080000-1` | serve-port-holder-identity | 5 |
| `20260820-162555.890191000-1` | measure-tool-grant-predicate | 6 |
| `20260820-162820.837039000-2` | tool-grant-predicate-measurement | 5 |

**71 total nodes.** All 71 declare a **non-empty** `allowed_tools`; **0** declare
an empty `allowed_tools: []`; **0** omit the key. Therefore:

- **No-grant nodes excluded as already covered by `handoff.LintToolGrants`: 0.**
  The candidate and the shipped sweep do not overlap at all on this corpus — but
  the exclusion clause (`0213-tool-grant-predicate.go:449-451`) also never fired
  and is **untested here**.

## 4. What the corpus grants (section B)

```
grant shapes (per grant / nodes holding at least one):
  bash-unrestricted       0 grants     0 nodes
  bash-parameterised    137 grants    59 nodes
  non-bash              199 grants    68 nodes
```

Every distinct grant string in the corpus, with its count:

| grant | count | shape |
| --- | ---: | --- |
| `Bash(cat *)` | 15 | bash-parameterised |
| `Bash(gh pr *)` | 9 | bash-parameterised |
| `Bash(git *)` | 48 | bash-parameterised |
| `Bash(go *)` | 23 | bash-parameterised |
| `Bash(grep *)` | 7 | bash-parameterised |
| `Bash(ls *)` | 18 | bash-parameterised |
| `Bash(make *)` | 17 | bash-parameterised |
| `Edit` | 24 | non-bash |
| `Glob` | 39 | non-bash |
| `Grep` | 46 | non-bash |
| `Read` | 68 | non-bash |
| `Write` | 22 | non-bash |

The planner never emits a bare `Bash` or `Bash(*)`, so the whole
`bash-unrestricted` branch of the matcher (`0213-tool-grant-predicate.go:179`,
`:366-368`) was never exercised. The entire corpus is prefix grants drawn from a
**7-command vocabulary**: `cat`, `gh pr`, `git`, `go`, `grep`, `ls`, `make`.

## 5. The candidate predicate, written out to be reimplementable

Fixed before the first run and **not revised after seeing results**
(`0213-tool-grant-predicate.go:209-243`).

**Input.** Every node of every planned graph that declares at least one grant.
A node declaring nothing is skipped as already covered by
`handoff.LintToolGrants`.

**Extraction — two sources.**

- **S1.** Every single-backticked span on a line that is *not* inside a fenced
  block, matched per line by `` `([^`]+)` `` so a span never runs across a
  newline (`:246`, `:350-352`).
- **S2.** Every non-blank line inside a ``` ``` ``` fenced block (`:347-348`).
  A line whose trimmed form starts with ``` ``` ``` toggles fence state and is
  itself never considered (`:342-345`).

**Normalisation.** Trim whitespace; drop a span whose first character is `#` (a
shell comment or a markdown heading); strip a leading `$ ` shell-prompt marker;
take the first whitespace-separated field as the candidate command token
(`:311-320`).

**Three acceptance rules; the token is offered as a command only if all hold.**

- **R1.** It matches `^[a-z][a-z0-9_.+-]*$` — lowercase, no slashes, no
  punctuation a command name does not carry. This alone drops paths
  (`docs/measurements/*.md`), templates (`{{ artifacts.x }}`), flags,
  capitalised identifiers, and anything preceded by a space (`:245`, `:321-323`).
- **R2.** It is not in `englishStop`, a **fixed** 80-odd-word list of common
  English function words, modals and boolean/null literals, so prose backticked
  for emphasis is dropped (`:248-264`, `:324-326`). The list is frozen: "Do not
  extend it after seeing results" (`:248`).
- **R3.** It is not a field name of this corpus's own schema. That set is
  **derived mechanically, not hand-written**: it is every JSON object key
  appearing anywhere in any planned `graph.json` (`:276-288`, `:400-411`,
  `:327-329`). This is what kills `prompt`, `allowed_tools`, `depends_on`,
  `success_check` and `verify`.

**Coverage test.** A hit is a surviving span whose *full text* is not covered by
any grant the node holds (`:362-379`). Grant semantics: a bare `Bash` or
`Bash(*)` covers everything; a `Bash(<prefix> *)` covers an invocation equal to
`<prefix>` or beginning `<prefix> `; a non-Bash grant (`Read`, `Write`, `Glob`,
`Grep`, `Edit`) covers no shell command at all. The prefix is the text inside the
parentheses with a trailing `*` and any trailing ` ` or `:` removed, so
`Bash(git status:*)` covers `git status …` (`:195-203`).

**Counting.** Raw uncovered **occurrences** are deduplicated to **hits** by
(run × node × command); the surviving row keeps the first occurrence's span and
context plus an `occurrences` count (`:458-471`).

**What the rule deliberately does not attempt.** Nothing distinguishes an
instruction from an example, a prohibition, or a quoted log line. Deciding that
is the hand-check's job, and it is the actual result of this measurement
(`:240-243`).

## 6. The result (section C) and the hand-check

```
nodes entering the predicate (declare >=1 grant):  71
nodes excluded (declare nothing; LintToolGrants):  0
uncovered command OCCURRENCES:                     139
HITS (distinct run x node x command):              114
nodes with at least one hit:                       44 of 71
```

Both numbers are reported: **139 raw occurrences, 114 hits after
deduplication.** 114 is the denominator of everything below.

**The motivating case is caught.** `20260819-223604.575080000-1 | verify |
gofmt` is in the list (`0213-hits.json:1298-1312`).

### Noise taxonomy

Four kinds were named in the brief; two more had to be named because together
they account for 85 of the 110 noise hits.

| kind | meaning | hits |
| --- | --- | ---: |
| (a) | EXAMPLE / illustration, not an instruction | 7 |
| (b) | the node is told NOT to run it | 5 |
| (c) | covered by a grant the matcher failed to associate | **0** |
| (d) | the prompt is QUOTING output, an error string, or document text | 13 |
| (e1) | IDENTIFIER-NOT-A-COMMAND — a backticked prose token | **81** |
| (e2) | COMMAND-AS-CONTENT — a command line the node must *write down* for someone else to run | 4 |

Sub-breakdown of the 81 (e1): subcommand or product name used as a noun 37; Go
identifier, source fragment or import path 15; branch name `main`/`master` 9;
filename or path 8; CI-job or make-target name 7; the verdict word `clean` 3;
`<meta>` tag name 2.

**Kind (c) is zero, and that is worth recording positively: the grant matcher
made no mistakes.** Every failure is in extraction, not in matching. This is
also why the "grants held" column below matches `0213-hits.json` for all 44
hit-bearing nodes — it was checked against each run's real `graph.json`.

### The full hand-check — one row per hit, all 114

Grant abbreviations: `go*` = `Bash(go *)`, and likewise `git*`, `make*`, `ls*`,
`cat*`, `grep*`, `gh pr*`. Non-Bash tools are written plainly.

| run id | node | command | grants held | verdict | reason |
| --- | --- | --- | --- | --- | --- |
| `20260803-081635.836216000-1` | check | main | git* | NOISE (e1) | "is not the repository default branch (`main` or `master`)" — branch name |
| `20260803-081635.836216000-1` | check | master | git* | NOISE (e1) | same sentence — branch name |
| `20260803-081635.836216000-1` | impl | auto | Read,Glob,Grep,Edit,Write,go*,make*,git*,ls*,cat*,grep* | NOISE (e1) | "the same --no-web flag … that `run` and `auto` already have" — subcommand as noun |
| `20260803-081635.836216000-1` | impl | defer | (as above) | NOISE (e1) | "does `defer startLiveView(ctx, web, runID)()`" — Go source fragment |
| `20260803-081635.836216000-1` | impl | resume | (as above) | NOISE (e1) | "give the `resume` subcommand the same --no-web flag" — noun |
| `20260803-081635.836216000-1` | impl | run | (as above) | NOISE (e1) | "that `run` and `auto` already have" — noun |
| `20260803-081635.836216000-1` | impl | web | (as above) | NOISE (e1) | "takes a nil-for-none `web browser.Opener` parameter" — Go parameter declaration |
| `20260803-081635.836216000-1` | review | resume | Read,Grep,Glob,git*,go*,make*,grep*,cat* | NOISE (e1) | "A prior node wired the `resume` subcommand into the web live view" — noun |
| `20260803-084651.244624000-1` | config | const | Read,Glob,Grep,Edit,Write,go*,make*,git*,grep*,ls* | NOISE (e1) | "it currently declares `const Version = \"0.3.1\"`" — Go declaration |
| `20260803-084651.244624000-1` | config | goreleaser | (as above) | NOISE (b) | "`goreleaser check` could not be run in this sandbox … do NOT attempt to run goreleaser" |
| `20260803-084651.244624000-1` | config | var | (as above) | NOISE (e1) | "change it to `var Version = \"0.3.1\"`" — Go declaration |
| `20260803-084651.244624000-1` | docs | go | Read,Grep,Edit,git* | NOISE (d) | "find the install section (currently `go install`-based)" — describing README text |
| `20260803-084651.244624000-1` | ship | goreleaser | Read,Edit,make*,go*,git*,gh pr*,ls*,grep* | NOISE (b) | "an honest note that `goreleaser check` was not available in this environment" |
| `20260803-084651.244624000-1` | ship | main | (as above) | NOISE (e1) | "assert the output is exactly `auto/goreleaser` and is not `main`" — branch name |
| `20260803-084651.244624000-1` | workflow | release | Read,Glob,Write,git*,ls*,cat* | NOISE (e2) | "runs goreleaser via `goreleaser/goreleaser-action` … args `release --clean`" — YAML content for GitHub Actions |
| `20260818-234944.646288000-1` | changelog | resume | Read,Grep,Edit,git* | NOISE (e2) | "Add ONE entry … that says: … `resume --help` … now print their usage" — CHANGELOG prose to write |
| `20260818-234944.646288000-1` | check | main | Read,Grep,git*,make* | NOISE (e1) | "(use `master` if `main` does not resolve)" — branch name |
| `20260818-234944.646288000-1` | check | master | (as above) | NOISE (e1) | same sentence |
| `20260818-234944.646288000-1` | fix | load | Read,Grep,Edit,Write,go*,git* | NOISE (d) | "answers `load run \"--help\": … no such file or directory`" — quoted error output |
| `20260818-234944.646288000-1` | fix | oh-my-graph | (as above) | NOISE (d) | "The defect: `oh-my-graph resume --help` answers …" — quoting the reported defect |
| `20260818-234944.646288000-1` | fix | resume | (as above) | NOISE (d) | "issue #198's reporter … typed `resume --help`" — quoting what a user typed |
| `20260818-234944.646288000-1` | fix | serve | (as above) | NOISE (e1) | "(`show`, `watch`, `serve <run-id>`) are to be fixed together" — subcommand names |
| `20260818-234944.646288000-1` | fix | show | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | fix | watch | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | review | load | Read,Grep,git*,make* | NOISE (d) | "must print the resume usage instead of `load run \"--help\": …`" — quoted error |
| `20260818-234944.646288000-1` | review | main | (as above) | NOISE (e1) | "(fall back to `master` if `main` does not resolve)" — branch name |
| `20260818-234944.646288000-1` | review | master | (as above) | NOISE (e1) | same sentence |
| `20260818-234944.646288000-1` | review | oh-my-graph | (as above) | NOISE (d) | "The goal being judged: `oh-my-graph resume --help` must print …" |
| `20260818-234944.646288000-1` | review | serve | (as above) | NOISE (e1) | "(`show`, `watch`, `serve <run-id>`) must be fixed by the same mechanism" |
| `20260818-234944.646288000-1` | review | show | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | review | watch | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | survey | load | Read,Glob,Grep | NOISE (d) | "answers `load run \"--help\": …` instead of printing the resume flag usage" |
| `20260818-234944.646288000-1` | survey | oh-my-graph | (as above) | NOISE (d) | "GitHub issue #200: `oh-my-graph resume --help` answers …"; the node is a read-only survey |
| `20260818-234944.646288000-1` | survey | serve | (as above) | NOISE (e1) | "at least `show`, `watch`, and `serve <run-id>`" |
| `20260818-234944.646288000-1` | survey | show | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | survey | watch | (as above) | NOISE (e1) | same list |
| `20260818-234944.646288000-1` | tests | claude | Read,Grep,Edit,Write,go*,make*,git* | NOISE (b) | "`FakeRunner`-style fakes and no real `claude` spawn" |
| `20260818-234944.646288000-1` | tests | load | (as above) | NOISE (d) | "reports a flag error, not a `load run` error" — naming an error string |
| `20260819-154136.440217000-1` | impl | serve | Read,Grep,Glob,Edit,Write,go*,make*,git* | NOISE (e1) | "expose the build a running `serve` process is executing" — noun |
| `20260819-154136.440217000-1` | impl | v | (as above) | NOISE (e1) | "the release version with any leading `v` trimmed" — a character |
| `20260819-154136.440217000-1` | impl | v0.10.0 | (as above) | NOISE (d) | "Keep BuildLabel's published behaviour byte-for-byte: `v0.10.0 (56e64fb-dirty, built …)`" — quoted output |
| `20260819-154136.440217000-1` | review | serve | Read,Grep,Glob,git*,go*,make* | NOISE (e1) | "`serve` now exposes its build as `<meta>` tags" — noun |
| `20260819-154136.440217000-1` | ship | changelog | Read,Grep,Edit,Write,git*,make*,gh pr* | NOISE (e1) | "The CI job `changelog` requires an entry under `## [Unreleased]`" — job name |
| `20260819-154136.440217000-1` | ship | oh-my-graph | (as above) | NOISE (e2) | "add one entry … so a tool can compare a page against `oh-my-graph version`" — CHANGELOG text |
| `20260819-154136.440217000-1` | ship | omg-built-at | (as above) | NOISE (e1) | "`omg-revision` and `omg-built-at` on both the dashboard and the single-run view" — meta tag name |
| `20260819-154136.440217000-1` | ship | omg-revision | (as above) | NOISE (e1) | same sentence — meta tag name |
| `20260819-154136.440217000-1` | ship | serve | (as above) | NOISE (e1) | "a running `serve` now states its build" — noun |
| `20260819-154136.440217000-1` | survey | curl | Read,Glob,Grep | NOISE (a) | "`curl -s … \| grep omg-build` is as scriptable as an endpoint would be" — rationale to record; the node makes no edits |
| `20260819-154136.440217000-1` | survey | local | (as above) | NOISE (e1) | "Makefile — what the `local` target runs"; the node is told to *read* the Makefile |
| `20260819-154136.440217000-1` | survey | oh-my-graph | (as above) | NOISE (a) | "a tool holding the page can tell a stale server … by comparing against `oh-my-graph version`" — a future reader's action |
| `20260819-154136.440217000-1` | survey | serve | (as above) | NOISE (e1) | "make a running `serve`'s build machine-readable" — noun |
| `20260819-154136.440217000-1` | tests | serve | Read,Grep,Glob,Edit,Write,go*,make*,git* | NOISE (e1) | "`serve` now renders its build as `<meta>` tags" — noun |
| `20260819-163447.441137000-2` | apply | changelog | Read,Glob,Grep,Edit,Write,git*,go*,make* | NOISE (e1) | "The CI job `changelog` requires this entry" — job name |
| `20260819-163447.441137000-2` | apply | serve | (as above) | NOISE (e1) | "exposed machine-readably on BOTH `serve` surfaces" — noun |
| `20260819-163447.441137000-2` | audit | changelog | Read,Glob,Grep,git*,go* | NOISE (e1) | "a `## [Unreleased]` CHANGELOG entry (the CI job `changelog` requires one)" |
| `20260819-163447.441137000-2` | audit | oh-my-graph | (as above) | NOISE (a) | "so a reader with the page can compare it against `oh-my-graph version`" — illustration; node is read-only |
| `20260819-163447.441137000-2` | audit | serve | (as above) | NOISE (e1) | "asks that a running `serve` expose the build it was compiled from" — noun |
| `20260819-163447.441137000-2` | pr | serve | Read,git*,gh pr* | NOISE (e1) | "Summary: a running `serve` keeps executing the code it started with" — PR body prose |
| `20260819-175025.348460000-1` | apply-fixes | changelog | Read,Grep,Glob,Edit,git* | NOISE (e1) | "(the CI job `changelog` requires an entry there)" — job name |
| `20260819-175025.348460000-1` | audit-design | auto | Read,Grep,Glob | NOISE (e1) | "a launch-time gate on `auto` (build evidence required…)" — noun |
| `20260819-175025.348460000-1` | audit-design | codex | (as above) | NOISE (e1) | "a second runtime (`--runtime codex`, spawning `codex exec`…)" — describing product behaviour |
| `20260819-175025.348460000-1` | audit-design | resume | (as above) | NOISE (e1) | "`resume` gained two flags; `auto` gained one flag and a refusal" |
| `20260819-175025.348460000-1` | audit-limits-examples | auto | (as above) | NOISE (e1) | same context paragraph |
| `20260819-175025.348460000-1` | audit-limits-examples | resume | (as above) | NOISE (e1) | same context paragraph |
| `20260819-175025.348460000-1` | audit-readme | auto | (as above) | NOISE (e1) | same context paragraph |
| `20260819-175025.348460000-1` | audit-readme | codex | (as above) | NOISE (e1) | "spawning `codex exec`" — product behaviour |
| `20260819-175025.348460000-1` | audit-readme | resume | (as above) | NOISE (e1) | same context paragraph |
| `20260819-175025.348460000-1` | audit-security-contrib | auto | (as above) | NOISE (e1) | same context paragraph |
| `20260819-175025.348460000-1` | audit-security-contrib | changelog | (as above) | NOISE (e1) | "including the `changelog` job and any required checks" — job name |
| `20260819-175025.348460000-1` | audit-security-contrib | codex | (as above) | NOISE (e1) | "spawning `codex exec` on the user's own Codex login" |
| `20260819-175025.348460000-1` | audit-security-contrib | resume | (as above) | NOISE (e1) | same context paragraph |
| `20260819-181003.413336000-2` | audit-contributing | auto | Read,Glob,Grep,Edit,git* | NOISE (e1) | "a launch-time build-evidence gate on `auto`" — noun |
| `20260819-181003.413336000-2` | audit-contributing | clean | (as above) | NOISE (e1) | "the findings … or `clean`" — a verdict word to output |
| `20260819-181003.413336000-2` | audit-contributing | codex | (as above) | NOISE (e1) | "a second runtime `codex`" — noun |
| `20260819-181003.413336000-2` | check-landed | oh-my-graph | Read,Grep,git* | NOISE (d) | "shows `oh-my-graph <graphs@oh-my-graph.dev>` on every line" — expected *output* of a git command |
| `20260819-181003.413336000-2` | final-report | clean | Read,Grep,git* | NOISE (e1) | "or the single word `clean`" — verdict word |
| `20260819-181003.413336000-2` | fix-readme-examples | clean | Read,Glob,Grep,Edit,git* | NOISE (e1) | "any findings or `clean`" — verdict word |
| `20260819-181003.413336000-2` | fix-readme-examples | runs | (as above) | NOISE (e1) | "the code that actually produces that output — the `runs list` table" — naming a table |
| `20260819-181003.413336000-2` | verify-trailers-changelog | changelog | Read,Grep,Edit,git* | NOISE (e1) | "The CI job `changelog` requires an entry under `## [Unreleased]`" |
| `20260819-223604.575080000-1` | changelog | serve | Read,Grep,Edit,git*,cat* | NOISE (e1) | "when `serve` cannot take its port it now probes 127.0.0.1" — CHANGELOG prose |
| `20260819-223604.575080000-1` | impl | gofmt | Read,Grep,Glob,Edit,Write,go*,git*,make* | **REAL** (make-mitigated) | "VERIFY LOCALLY before committing: `gofmt -l internal/serve`, `go build ./...`, `go vet ./internal/serve/...`" — a direct instruction; `gofmt` is not grantable here. Holds `make*`, and `make fmt-check` runs `gofmt -l .` (`Makefile:24-28`), so a near-equivalent was reachable — but the prompt does not route it through `make` |
| `20260819-223604.575080000-1` | impl | serve | (as above) | NOISE (e1) | "Implement issue #204 option 2: when `serve` cannot bind its port" — noun |
| `20260819-223604.575080000-1` | review | gofmt | Read,Grep,Glob,go*,git*,grep* | **REAL** | "Run `go test …` and `go vet …` and `gofmt -l internal/serve` to see the current state for yourself" — no `make*`, no `gofmt` |
| `20260819-223604.575080000-1` | tests | gofmt | Read,Grep,Glob,Edit,Write,go*,git*,make* | **REAL** (make-mitigated) | "Also run `gofmt -l internal/serve`" — direct instruction; same `make` caveat as `impl` |
| `20260819-223604.575080000-1` | verify | gofmt | Read,Grep,git*,go*,grep* | **REAL** — the anchor | "6. TESTS — … and `gofmt -l internal/serve` prints nothing" — holds neither `Bash(gofmt *)` nor `Bash(make *)` |
| `20260820-162555.890191000-1` | check | go.mod | Read,git*,ls*,cat* | NOISE (e1) | "neither `go.mod` nor `go.sum`, appears in any of them" — filename |
| `20260820-162555.890191000-1` | check | go.sum | (as above) | NOISE (e1) | same sentence — filename |
| `20260820-162555.890191000-1` | check | main | (as above) | NOISE (e1) | "on `measure/tool-grant-predicate` and not on `main`" — branch name |
| `20260820-162555.890191000-1` | corpus | graph.json | Read,Glob,Grep,Write,Edit,go*,ls*,cat* | NOISE (e1) | "Each may contain `graph.json` and `state.json`" — filename |
| `20260820-162555.890191000-1` | corpus | graph_source_path | (as above) | NOISE (e1) | "its `state.json` field `graph_source_path`" — JSON field name |
| `20260820-162555.890191000-1` | corpus | grep | (as above) | NOISE (b) | "HARD RULE — PARSE, DO NOT GREP. … a `grep -c` count went into three documents and was wrong" |
| `20260820-162555.890191000-1` | corpus | os | (as above) | NOISE (e1) | "the standard library (`encoding/json`, `os`, `path/filepath`, `regexp`, `strings`)" — import path |
| `20260820-162555.890191000-1` | corpus | package | (as above) | NOISE (e1) | "then a blank line, then `package main`" — Go source line |
| `20260820-162555.890191000-1` | corpus | regexp | (as above) | NOISE (e1) | same import list |
| `20260820-162555.890191000-1` | corpus | state.json | (as above) | NOISE (e1) | filename |
| `20260820-162555.890191000-1` | corpus | strings | (as above) | NOISE (e1) | same import list |
| `20260820-162555.890191000-1` | handcheck | gofmt | Read,Glob,ls*,cat* | NOISE (d) | "its `verify` node was told to run `gofmt`, held neither `Bash(gofmt *)` nor …" — quoting another node's incident |
| `20260820-162555.890191000-1` | handcheck | make | (as above) | NOISE (a) | "(e.g. routed through `make`, or a grant pattern the matcher mis-parsed)" — an example inside a noise taxonomy |
| `20260820-162555.890191000-1` | precedent | tool_grant_lint_test.go | Read,Glob,Grep | NOISE (e1) | "look beside it in `internal/handoff/`, e.g. `tool_grant_lint_test.go`" — filename |
| `20260820-162555.890191000-1` | predicate | gofmt | Read,Glob,Write,Edit,go*,ls*,cat* | NOISE (a) | "backticked spans (`gofmt -l ...`, `make local`, `go test ./...`)" — literally examples of what to extract |
| `20260820-162555.890191000-1` | predicate | make | (as above) | NOISE (a) | same sentence |
| `20260820-162555.890191000-1` | predicate | sudo | (as above) | NOISE (a) | "after stripping an env-var prefix or a leading `$`/`sudo`" — a token to strip |
| `20260820-162555.890191000-1` | predicate | x | (as above) | NOISE (e1) | "a grant `Bash(x *)` permits a command whose first token is `x`" — metavariable |
| `20260820-162555.890191000-1` | writeup | go.mod | Read,Glob,Grep,Write,Edit,git*,go*,cat*,ls* | NOISE (e1) | "no change to `go.mod`" — filename, and a prohibition |
| `20260820-162555.890191000-1` | writeup | gofmt | (as above) | NOISE (d) | "its `verify` node told to run `gofmt`" — quoting the motivating incident |
| `20260820-162555.890191000-1` | writeup | graph.json | (as above) | NOISE (e1) | filename |
| `20260820-162555.890191000-1` | writeup | graph_source_path | (as above) | NOISE (e1) | JSON field name |
| `20260820-162555.890191000-1` | writeup | main | (as above) | NOISE (e1) | "NEVER commit on `main`" — branch name |
| `20260820-162555.890191000-1` | writeup | state.json | (as above) | NOISE (e1) | filename |
| `20260820-162820.837039000-2` | measure | graph_source_path | Read,Glob,Write,go*,ls*,cat* | NOISE (e1) | "JSON-parse state.json and read its `graph_source_path`" — field name |
| `20260820-162820.837039000-2` | measure | grep | (as above) | NOISE (b) | "HARD RULE — PARSE, DO NOT GREP … You have no grep tool here on purpose." |
| `20260820-162820.837039000-2` | measure | package | (as above) | NOISE (e1) | "then a blank line, then `package main`" — Go source line |
| `20260820-162820.837039000-2` | writeup | go | Read,Glob,Write,git*,ls*,cat* | NOISE (e2) | "the exact command (`go run docs/measurements/0213-tool-grant-predicate.go`)" — the command is document *content*; this node is told to write it down, not run it. Its sibling `writeup` in `20260820-162555.890191000-1`, which *was* told to run it, holds `go*` and correctly produced no hit |
| `20260820-162820.837039000-2` | writeup | graph_source_path | (as above) | NOISE (e1) | "the planned-run selection rule stated precisely (state.json `graph_source_path` …)" |

Six of the sixteen planned graphs produced **no hits at all**:
`two-files-two-runs`, `finish-beta-file`, `serve-gate-approval`,
`embed-examples-init`, `readme-note`, `hello-file`.

## 7. The noise rate, as a fraction

The predicate **as run and unmodified** — this is the headline:

> **REAL 4/114. NOISE 110/114.** About one hit in 29.

Three other readings, each labelled with what was changed:

| reading | what changed | REAL | NOISE |
| --- | --- | --- | --- |
| **as run** (headline) | nothing | **4/114** | **110/114** |
| strict `make` reading | the two hits whose node holds `Bash(make *)` (`impl`, `tests` in `20260819-223604.575080000-1`) counted as noise, since `make fmt-check` runs `gofmt -l .` (`Makefile:24-28`) | 2/114 | 112/114 |
| self-measurement removed | the corpus's own two graphs (`20260820-162555.890191000-1`, `20260820-162820.837039000-2`, 29 hits, all noise) dropped | 4/85 | 81/85 |
| **argument-count narrowing — HAND-COUNT ESTIMATE, NOT A RE-RUN** | require the backticked span to contain at least one argument, i.e. drop bare single-token spans | ~4/41 | ~37/41 |

The headline uses the **as run** row because that is the predicate that was
specified and executed; the strict `make` reading is reported beside it because
neither of those two prompts routes `gofmt` through `make`, which is exactly the
distinction the rule was told not to blur.

**Distinct defects found: one.** All four REAL hits are `gofmt -l internal/serve`
inside `20260819-223604.575080000-1`.

The last row is **an estimate and must not be quoted as a measurement**: it is a
hand-count over the same 114 rows of `0213-hits.json`, not a second run of the
script. All 4 REAL hits survive that narrowing; the 37 surviving noise rows are
quoted output, quoted defect reports, Go source and prose — `load run "--help":
… no such file or directory`, `oh-my-graph resume --help`, `codex exec`,
`const Version = "0.3.1"`, `package main`, `grep -c`,
`v0.10.0 (56e64fb-dirty, built …)` — none of which an argument count can reach.

## 8. CORPUS CAVEAT

**A corpus of 16 planned graphs settles nothing on its own. Any fraction of
n = 16 is an indication, not a decision.** Specifically:

- **Two of the 16 are this measurement's own graphs**
  (`20260820-162555.890191000-1`, `20260820-162820.837039000-2`). The corpus
  partly measures itself, and those two contributed **29 of the 114 hits** — and
  they are unusually identifier-dense, because their prompts talk about JSON
  fields and Go source. Removing them gives 4/85 and does not rescue the
  predicate.
- **Six of the 16 produced no hits at all**, so the effective denominator of
  graphs that exercise the rule is 10.
- **The corpus is monotonous.** All 71 nodes declare a non-empty
  `allowed_tools` drawn from a 7-command vocabulary, so (i) the no-grant
  exclusion clause never fired and is untested, and (ii) the
  `bash-unrestricted` branch of the matcher was never exercised at all.
- **Every graph in it is planner-authored**, by construction of the selection
  rule. Nothing here measures a hand-written graph.
- **The planned/hand-written discriminator did no work here** (§2): every run
  that had a `graph.json` passed it.

What the number *does* establish regardless of n: **110 hand-checked false
positives are 110 false positives**, however many more graphs exist. Small N can
hide real hits; it cannot un-find noise.

## 9. What the existing lint does, and the house style a future one must match

**The shipped predicate.** `handoff.LintToolGrants(g *graph.Graph) []Warning`
(`internal/handoff/tool_grant_lint.go:54`) warns on a node that is not exempt
**and** has `AllowedTools == nil` **and** `SuccessCheck.Verify == nil`
(`:56-65`). One warning per node maximum. The test is **non-nil, not
non-empty** (`:60-63`): yaml.v3 leaves an absent key nil and an explicit `[]`
empty-but-present, so `allowed_tools: []` is the documented, priced opt-out
(`:45-48`) while `allowed_tools:` with no value parses to nil and still warns.
Two structural exemptions, both because the warning's premise is false there:
gate nodes and `permission_mode: bypassPermissions` (`:57`, rationale `:32-37`).
It never reads the prompt, and it never checks grant *adequacy* — which is the
gap #213 names. Its own shipping measurement is in the docstring: 164 claude
nodes after fragment resolution, **62 hits, 61 real** (`:23-30`).

**A future lint in this package would have to match all of this:**

| convention | address |
| --- | --- |
| signature `func Lint<Subject>(g *graph.Graph) []Warning`, no `error`, nil slice when clean | `tool_grant_lint.go:54`, `verdict_lint.go:46`, `verify_inline_lint.go:82`, `feedback_quote_lint.go:64` |
| the `Warning` type: three strings, **no severity field and no code** — severity is the type's identity | `internal/handoff/placeholder_lint.go:17-21`, doc `:11-16` |
| rendering: `node %q: %s: %s` | `placeholder_lint.go:23-25` |
| `Detail` shape: what is missing — what goes wrong at run time — semicolon — the imperative fix, naming every option | `tool_grant_lint.go:69` |
| registration is manual and single-point: append into `warnAdvisories` | `cmd/oh-my-graph/lint.go:119-130` |
| advisory standing restated at the call site — warnings never affect an exit code | `cmd/oh-my-graph/lint.go:118`, `placeholder_lint.go:14-16` |
| exactly two call sites: `lint` and `run --dry-run`; a plain `run` prints nothing | `cmd/oh-my-graph/lint.go:92`, `cmd/oh-my-graph/dryrun.go:49` |
| docstring is the deliverable: predicate in prose, what it warns about, what it leaves to a neighbouring sweep, **a measurement-before-shipping paragraph with counts**, and why it is a warning and not a load error | `tool_grant_lint.go:7-53` (the measurement is `:23-30`; the standing reason `:43-45`) |
| tests: table-driven, fixtures as **raw YAML through `parseGraph`** and never struct literals (the nil-vs-empty distinction is only expressible through the real decoder) | `tool_grant_lint_test.go:17-87`, helper `placeholder_lint_test.go:12-19` |
| 4 of the 7 table rows are negative cases, each stating in its `name` field why it must stay silent (`:38-41`, `:50-53`, `:55-58`, `:60-63`); the two rows that turn on the nil-vs-empty distinction additionally carry an explanatory comment | `tool_grant_lint_test.go:33-63` (comments at `:34-37` and `:43-44`) |
| assertions: exact warning count, exact `NodeID`/`Field`, and a `strings.Contains` pinning the **advice** rather than the complaint's wording | `tool_grant_lint_test.go:69-84` |
| a `*_ShippedGraphsAreClean` sweep over `../../graphs/*.yaml` loaded with `graph.LoadFile` so `use:` fragments are judged too | `tool_grant_lint_test.go:114-130` |
| touching six other places when a sweep is added: the append chain, `warnAdvisories`' own count ("the six handoff sweeps"), the `lintGraph` docstring, the `Warning` doc list, the `DESIGN.md` inventory line, and the user prose | `cmd/oh-my-graph/lint.go:111`, `:120-124`, `placeholder_lint.go:11-13`, `DESIGN.md:2756`, `docs/EXAMPLES.md:162-169` |

One observation made in passing: the `Warning` doc list at
`placeholder_lint.go:11-13` names five sweeps and omits `LintFeedbackQuoting`
(`internal/handoff/feedback_quote_lint.go:64`), so it is already stale by one.

**Why no advisory/refusal split would be needed.** `LintFeedbackQuoting` exports
its predicate separately (`feedback_quote_lint.go:78-88`) so the coordinator can
escalate to a plan refusal without recomputing the rule — "Two computations of
one rule drift" (`:83-84`). `LintToolGrants` needs no such split, and a #213 lint
would not need one in the same way: a planner-authored node's `allowed_tools`
must be a non-empty list drawn from `coordinator.plannedToolAllowlist`
(`DESIGN.md:2159-2164`), so the auto path is already bounded — though note that
this measurement's corpus is **entirely** planner-authored, so it says nothing
about hand-written graphs at all.

## 10. RECOMMENDATION

**Not a lint — do not ship this predicate, and do not tune it.** The number
that decides it is 110 noise in 114 hits over 16 graphs: a sweep built on this
rule would put roughly 110 spurious `warning:` lines in front of a user for one
real finding, on a channel whose whole value is that it is currently believed
(`handoff.LintToolGrants` shipped at 61 real of 62 —
`internal/handoff/tool_grant_lint.go:23-30`). The failure is structural rather
than a tuning accident: prompts in this corpus use backticks overwhelmingly for
**nouns** — subcommands, branches, filenames, JSON fields, Go identifiers, CI
job names — and no lexical rule can separate "`serve`, the subcommand" from
"`serve`, the command to run", because they are the same string. R3 could kill
`prompt` and `allowed_tools` because they are `graph.json` keys; it could never
touch `graph_source_path`, which is a `state.json` key. The narrowing that
attacks the biggest noise class (require an argument) is estimated at 37 noise in
41 and is still unusable. **If anyone wants to carry #213 forward, the named
study is this: re-run this same script with one changed extraction rule — require
the span to be governed by an instruction verb in the same sentence ("Run",
"run", "VERIFY", "Also run") — over a corpus extended with hand-written graphs,
since all 16 here are planner-authored. All 4 REAL hits satisfy that condition
and most of the (d) and (e2) noise does not; that is a hypothesis with a testable
shape, not a result, and no number should be quoted for it until it has been
run.** Until then, the honest prescription for the `gofmt` incident is not a lint
at all: it is the direction the repo already took in ADR 0030 — an engine-run
`success_check.verify`, which fails a node whose tool was denied without needing
to read the prompt at all (`internal/handoff/tool_grant_lint.go:19-23`).

## 11. No CHANGELOG entry

**None was added, deliberately.** This measurement ships no behaviour: one
document, one `//go:build ignore` script nothing in the engine calls, and one
JSON data file. A user upgrading between releases sees no difference, which is
what a changelog entry is for. The precedent is the repo's own: the two prior
measurement-only commits, `5fe70d9` ("the operator lane corpus has no
extractable fragment") and `77030a0` ("ADR 0018's compliance baseline"), touched
`CHANGELOG.md` in neither case, while `36ec8c3` — which shipped a measurement
*and* a change to `cmd/oh-my-graph/main.go` — did.
