# 0037 — the first run, as a stranger meets it: an ordered walk from README to first node

**Nine friction points on the path a README reader walks. Four are executed
and quoted verbatim; five are read from source because this run has no shell
grant for a scratch `HOME`, a fresh directory, or a download. The three
loudest are: the top-level `--help` a newcomer types first EXITS 1 as an
unknown command (`cmd/oh-my-graph/main.go:245`); the README's first install
line is `go install` while `.goreleaser.yaml` publishes four prebuilt archives
(`README.md:48` vs `.goreleaser.yaml:18-35`); and NOTHING in the tree calls
`exec.LookPath`, so a missing provider CLI is discovered by a failed spawn
inside the run, five attempts and ~8 seconds deep.**

- **Date:** 2026-08-26 (KST), macOS (darwin 22.6.0), one machine, one worktree.
- **Tree:** `8af2732` (`git -C /private/tmp/w-onboard rev-parse --short HEAD`),
  branch `lane-onboard`, working tree clean
  (`git -C /private/tmp/w-onboard status --porcelain` → empty).
- **Toolchain:** `go version go1.26.5 darwin/amd64` (`go version`).
- **Cost:** zero spawns. No `auto`, no `run`, no `resume`, no model CLI was
  invoked; nothing was written under `~/.oh-my-graph`.
- **Numbering:** `0037` is the lowest number free in BOTH `docs/adr/` (which
  ends at `0036-cross-run-session-reuse-is-rejected.md`) and
  `docs/measurements/` (`ls docs/adr`, `ls docs/measurements`). This file
  serves no ADR; the prefix is only a sort key.
- **What this file is NOT:** it is JOB 1 (measure). Nothing here is fixed. Each
  step ends in a verdict, not a patch.

## The rule this walk applies

Every step's verdict answers one question and only that one:

> Could a newcomer act on this WITHOUT reading the source? **YES / NO**

"The message is technically accurate" is not a YES. A YES means the text on
screen names the next thing to type, or names the missing precondition
specifically enough that the next thing to type follows from it.

---

## Step 0 — the reader arrives at README.md

**What I ran**

```sh
grep -c "" README.md
```

**What appeared**

```text
266
```

The first line of the file that is a command a reader could type is
`README.md:48`. Everything above it is badges (`README.md:12-15`), two images
(`README.md:18-20`, `README.md:37-40`) and the "What it is" prose
(`README.md:25-35`). The release badge at `README.md:12` links to the Releases
page, but it is an image, not an instruction.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **YES.**
The Quickstart is 45 lines in and clearly headed (`README.md:45`).

---

## Step 1 — install

**What I ran**

NOT EXECUTED — no shell grant for a scratch `HOME` in this run, and no grant
for `curl` or `tar`. Neither `go install …@latest` (which writes to
`$GOBIN`/`$GOPATH/bin` outside this worktree) nor the prebuilt-binary path
could be exercised.

**What appeared**

source-read, not executed. The first install command in the README, verbatim,
`README.md:47-48`:

```sh
go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest
```

The prebuilt-binary path is not offered here. It is first named at
`README.md:118-119`, seventy lines below the Quickstart, as a question:

> Prefer a prebuilt binary, or want to know exactly what `init` writes and what
> it refuses to overwrite? [docs/INSTALL.md](docs/INSTALL.md).

Prebuilt binaries do exist. `.goreleaser.yaml:18-23` declares `goos: darwin,
linux` × `goarch: amd64, arm64`; `.goreleaser.yaml:27-35` makes each a
`tar.gz` named `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`;
`.goreleaser.yaml:41-42` adds `checksums.txt`. That is **4 archives + 1
checksum file = 5 published assets** — the arithmetic is mine, from those three
line ranges, not a read of any release page (no network grant in this run).
`docs/INSTALL.md:44` spells one out concretely:
`oh-my-graph_${VERSION}_${OS}_${ARCH}.tar.gz`.

Two further preconditions the Quickstart does not state:

- **A Go toolchain of a specific minimum.** `go.mod:3` is `go 1.25.0`. The
  README badge at `README.md:14` says "go 1.25", but the badge sits above the
  Quickstart and links to `go.mod`, not to an instruction.
- **`$GOPATH/bin` on `PATH`.** `go install` puts the binary there and says
  nothing; every subsequent README line types a bare `oh-my-graph`. The README
  never mentions it:

```sh
grep -n "PATH\|GOPATH\|GOBIN" README.md
```

```text
(no matches)
```

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **NO.**
A reader without Go installed, or with Go older than 1.25.0, or without
`$GOPATH/bin` on `PATH`, gets no instruction from the Quickstart and no
signpost to the four prebuilt archives that would have skipped the toolchain
entirely; the signpost exists 70 lines later, past the point where the reader
has already typed the command.

---

## Step 2 — `oh-my-graph --help`

This is the step the walk was written to catch: the first thing a stranger
types after an install is almost never the second line of a Quickstart.

**What I ran**

```sh
go run ./cmd/oh-my-graph --help
```

**What appeared**

```text
oh-my-graph: unknown command "--help" (want init, run, auto, lint, resume, runs, show, watch, serve, chat, or version)
exit status 1
```

And the same for the other spelling:

```sh
go run ./cmd/oh-my-graph help
```

```text
oh-my-graph: unknown command "help" (want init, run, auto, lint, resume, runs, show, watch, serve, chat, or version)
exit status 1
```

The source: `parseCommandLine` (`cmd/oh-my-graph/main.go:252-285`) consumes
only `--runtime` before the subcommand and returns everything else to the
dispatch switch, whose `default` arm is
`cmd/oh-my-graph/main.go:244-246`. There is no `help` case in that switch
(`cmd/oh-my-graph/main.go:205-247`), and no top-level `-h`/`--help` handling —
`isHelpToken` (`cmd/oh-my-graph/argslot.go:34-36`) is reached only through a
SUBCOMMAND's positional slot (`cmd/oh-my-graph/argslot.go:86-91`,
`:99-108`).

Running with NO arguments does print the full synopsis — but also exits 1:

```sh
go run ./cmd/oh-my-graph
```

```text
oh-my-graph: usage: oh-my-graph [--runtime claude|codex] <command> ...
commands:
       oh-my-graph init [dir]
       oh-my-graph run <graph.yaml> [--dry-run] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web]
       oh-my-graph auto "<goal>" [--plan-only] [--verify-cmd 'CMD'] [--verify-timeout D] [--accept-no-build-evidence] [--accept-loaded-user-config] [--max-cycles N] [--max-goal-budget-usd X] [--input k=v ...] [--concurrency N] [--continue-on-fail] [--no-web] [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
       oh-my-graph lint <graph.yaml>
       oh-my-graph resume <run-id> (--approve <gate-id> | --reject <gate-id> | --retry-failed) [--verify-cmd 'CMD'] [--verify-timeout D] [--concurrency N] [--no-web] [--no-skill-activation]
       oh-my-graph runs list [--show-skipped] [--exit-in-flight]
       oh-my-graph show <run-id>
       oh-my-graph watch <run-id>
       oh-my-graph serve [<run-id>] [--port N] [--no-open]   (no run id: the dashboard over every run)
       oh-my-graph chat [--no-agent-mapping] [--no-agent <name> ...] [--no-skill-activation]
       oh-my-graph version
exit status 1
```

Note the asymmetry, and note that it is deliberate for the SUBCOMMANDS and
merely absent at the top: `cmd/oh-my-graph/main.go:87-97` routes a
`*usageRequest` to **stdout, exit 0** — the fix #200 made
(`cmd/oh-my-graph/argslot.go:49-61`) — and `exitCodeForError` repeats it at
`cmd/oh-my-graph/main.go:131-136` with the comment "Asked for and answered:
`resume --help` is a successful invocation, not a failed one (#200)". The
top-level `--help` never becomes a `usageRequest`, so it takes the
`oh-my-graph: %v` stderr line and exit 1 at
`cmd/oh-my-graph/main.go:109-111` instead.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **NO.**
The message names the eleven subcommands, which is real information — but it
calls the universal help flag an *unknown command*, prints to stderr, and exits
1. A reader in a shell with `set -e`, or reading a CI log, sees a failure where
they asked a question. The bare-invocation path answers correctly and still
exits 1.

---

## Step 3 — `oh-my-graph init`

**What I ran**

```sh
go run ./cmd/oh-my-graph init --help
```

(Bare `init` was NOT EXECUTED — it writes `graphs/` into the working tree, and
this worktree already carries the real `graphs/` under version control.)

**What appeared**

```text
usage: oh-my-graph init [dir]
```

Exit 0, stdout. That is the whole answer: `init` registers no `FlagSet`, so
`helpRequest` is called with `set: nil`
(`cmd/oh-my-graph/argslot.go:99-105`, via `cmd/oh-my-graph/init.go:31-33`) and
`usageRequest.print` writes the synopsis line and nothing more
(`cmd/oh-my-graph/argslot.go:75-81`). The synopsis itself is
`cmd/oh-my-graph/main.go:181`.

What `init` actually does is source-read, not executed. It unpacks every file
embedded in the binary into `<dir>/graphs/`
(`cmd/oh-my-graph/init.go:93-169`, `initGraphsDir = "graphs"` at
`cmd/oh-my-graph/init.go:22`), one line per file:

- `cmd/oh-my-graph/init.go:144` — `fmt.Fprintf(&listing, "wrote %s\n", path)`
- `cmd/oh-my-graph/init.go:127` — `fmt.Fprintf(&listing, "kept  %s (%s)\n", path, note)`
- `cmd/oh-my-graph/init.go:151` — `"%d file(s) written to %s\n"`
- `cmd/oh-my-graph/init.go:153` — `"%d file(s) were already there and were left untouched — to take the shipped copy of one, move yours aside and run \`init\` again\n"`
- `cmd/oh-my-graph/init.go:156` — `"%d of those differ from this binary's copy (marked DIFFERS above) — your own edit, or a file left from an older release; the second kind can fail a template this run just wrote, at load time rather than here\n"`

And it DOES say what to type next — `cmd/oh-my-graph/init.go:162-164`,
verbatim:

```go
if smoke := filepath.Join(target, smokeGraphFile); payloadCarries(names, smokeGraphFile) {
    fmt.Fprintf(&listing, "next: mkdir -p /tmp/omg-smoke && oh-my-graph run %s --input dir=/tmp/omg-smoke\n", smoke)
}
```

with `smokeGraphFile = "haiku-smoke.yaml"` (`cmd/oh-my-graph/init.go:223`).
That file is in the payload: `ls graphs` lists `haiku-smoke.yaml` among nine
entries.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **YES,
for bare `init`; NO for `init --help`.** The command that runs prints a `next:`
line naming the exact next command — the single best piece of onboarding in
the tree. The command that ASKS what it will do answers with fourteen
characters of synopsis and says nothing about writing files into the current
directory. A cautious reader who checks before running learns less than one who
does not.

---

## Step 4 — the first command that produces output costs money

**What I ran**

NOT EXECUTED — running `auto` or `run` is out of scope for a measurement node,
and both spawn a model CLI.

**What appeared**

source-read, not executed. The Quickstart's four runnable commands after
`init` are `README.md:59`, `:63`, `:66` and `:70`. Every one of them spawns a
provider CLI and spends plan allowance; `README.md:68` says so of the last one
("the cheapest real end-to-end check (a few cents)").

Two zero-cost commands exist and appear nowhere in the Quickstart. `lint`
validates a graph with "no node spawns, no run directory is created, zero
cost" (`cmd/oh-my-graph/lint.go:33-39`), and `run --dry-run` "stops after
validation and the plan print — nothing is wired and no node runs"
(`cmd/oh-my-graph/main.go:317-320`). Their absence from the README is
measurable:

```sh
grep -n "lint\|dry-run\|plan-only" README.md
```

```text
59:oh-my-graph auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence
66:oh-my-graph --runtime codex auto "lint this repo and summarize the findings" --input repo=$PWD --accept-no-build-evidence
84:reused by `resume` and browser gate actions. Add `--plan-only` to `auto` to buy
```

Lines 59 and 66 are the word "lint" inside a GOAL STRING, not the subcommand.
Line 84 offers `--plan-only`, which is not a free step either. Its own flag
description, from the executed `go run ./cmd/oh-my-graph auto --help` above,
reads: "plan the graph, print it with every agent/skill mapping and the tool
ceiling, then exit without running any node — NOT free, unlike run --dry-run:
it still pays for at least one real planner call, and a validation refusal buys
one corrected call on top of it". The run-time message says the same at
`cmd/oh-my-graph/main.go:720-721`: "The %s still paid for (%s) — unlike `run
--dry-run`, this is not free".

I did execute the free path, on the graph the README tells the reader to run:

```sh
go run ./cmd/oh-my-graph lint graphs/haiku-smoke.yaml
```

```text
graphs/haiku-smoke.yaml: valid
```

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **NO.**
The Quickstart has no step between "install" and "spend money". A reader who
wants to confirm the binary works on their machine before billing their plan
has no command to type, and the two that would do it are documented elsewhere.
(`version` exists and exits 0 — see Step 6 — but it proves only that a binary
is on `PATH`.)

---

## Step 5 — `auto` can refuse the first thing the reader types (ADR 0030)

**What I ran**

NOT EXECUTED — `auto` is out of scope for this node. The refusal happens
before any spawn (`cmd/oh-my-graph/main.go:442-457`, and the error text itself
says so), but the instruction for this node is not to invoke `auto` at all.

**What appeared**

source-read, not executed. The gate is `answerBuildEvidence`, called with `"."`
— the invocation directory — at `cmd/oh-my-graph/main.go:454`, upstream of
`planAndExecute`. It calls `coordinator.DetectBuildSignals`
(`cmd/oh-my-graph/verifycmd.go:268`; the function is
`internal/coordinator/verifycmd.go:469-484`) over a 13-entry table
(`internal/coordinator/verifycmd.go:438-452`) plus one `.csproj` glob
(`internal/coordinator/verifycmd.go:476-482`), then
`coordinator.RequireBuildEvidence`
(`internal/coordinator/verifycmd.go:650-662`), whose last arm returns
`&MissingBuildEvidenceError{Signals: signals}`
(`internal/coordinator/verifycmd.go:661`).

The exit code is 3: `exitCodeForError` matches
`*coordinator.MissingBuildEvidenceError` and returns 3
(`cmd/oh-my-graph/main.go:152-155`), and `mainExitCode` prints the error's own
text to **stdout**, unprefixed (`cmd/oh-my-graph/main.go:103-107`). The
package doc states the same mapping at `cmd/oh-my-graph/main.go:27-30`.

The text, quoted from `MissingBuildEvidenceError.Error`
(`internal/coordinator/verifycmd.go:688-690`) and `.Print`
(`internal/coordinator/verifycmd.go:693-725`):

```text
auto: this directory has a build system, and this run would check none of it.
```

then, for exactly one detected signal
(`internal/coordinator/verifycmd.go:701-703`):

```text
Detected %s (%s).
```

or, for more than one (`internal/coordinator/verifycmd.go:704-709`):

```text
Detected several build signals (%s), so the command below
is a guess.
```

then, unconditionally (`internal/coordinator/verifycmd.go:710-724`):

```text
A planned node cannot carry a build command: the planner's reply is untrusted
input, so success_check.verify is refused from a plan, and no allowed tool
runs a build. A check node's PASS is words it emitted, not a build that ran.
Without --verify-cmd, every judgement in this run is the model's, about its
own work.

Re-run with ONE of:

  --verify-cmd '<the first detected signal's suggested command>'
      the ENGINE runs that command at each sink node of the plan and judges
      its exit code itself. No node is granted anything.

  --accept-no-build-evidence
      run anyway, on the record: this run carries no build evidence. The
      choice is written to the run's state.json and printed with the plan.

Nothing has been spent — this is refused before the planner call.
```

(The `--verify-cmd` line's argument is `e.suggestedCommand()`,
`internal/coordinator/verifycmd.go:717` and `:731-736` — the first detected
signal's `SuggestedCommand`, or the literal `<your build command>` when none
was recorded.)

**Would it fire in THIS repository?** `ls -a` at the repo root shows both
`go.mod` and `Makefile`. Those are table entries
`internal/coordinator/verifycmd.go:448` (`go.mod` → `go build ./...`) and
`:449` (`Makefile` → `make`); no other table entry's file is present at the
root. `DetectBuildSignals` walks the table in order
(`internal/coordinator/verifycmd.go:471-475`), so `signals` would be
`[go.mod, Makefile]` — the two-signal branch, whose suggested command is
`go build ./...`. This is a source-read prediction; **I did not execute it.**

**Does the README's own first `auto` refuse?** No — `README.md:59` already
carries `--accept-no-build-evidence`, and `README.md:54-58` explains why in
five lines of comment. The refusal is reached by the reader who types
`oh-my-graph auto "<goal>"` from the synopsis instead of pasting the README
line, in any directory holding one of the 14 markers.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **YES.**
This is the best-constructed message on the walk: it names the marker it
detected, offers two exits with what each buys, names only flags `auto`
actually registers, and closes the "have I been billed?" question explicitly.
The friction here is not the message — it is that a first interaction can be a
refusal at all, and that the exit code is 3, which no README line mentions (the
exit-code table lives at `cmd/oh-my-graph/main.go:25-39` and in
`docs/EXAMPLES.md`, per `README.md:251`).

---

## Step 6 — `oh-my-graph version`

**What I ran**

```sh
go run ./cmd/oh-my-graph version
```

**What appeared**

```text
oh-my-graph 0.12.0
```

Exit 0. The string comes from `var Version = "0.12.0"`
(`cmd/oh-my-graph/version.go:9`), overwritten at release time by
`-X main.Version={{.Version}}` (`.goreleaser.yaml:25`).

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **YES.**
It answers, on stdout, exit 0. It is also the only command on this walk that
does so.

Worth recording alongside it, because it bears on Step 1: a binary produced by
`go install` gets NO `-X` injection and therefore reports whatever
`cmd/oh-my-graph/version.go:9` last said. A `go install …@latest` user and a
release-archive user of the same version print the same string here, but a
`go install` user tracking `main` between tags prints the LAST released
number, not their commit. Not measured — I did not run `go install`.

---

## Step 7 — the provider CLI is missing from `PATH`

**What I ran**

NOT EXECUTED — no shell grant for a scratch `HOME` or a modified `PATH` in
this run, so neither a missing `claude` nor a logged-out one could be staged.

**What appeared**

source-read, not executed. Three findings:

**(a) There is no pre-flight.** A whole-tree search finds no PATH probe at all:

```sh
grep -rn "LookPath" .
```

```text
(no matches)
```

The only pre-flight that exists, `runner.ValidateGraphForRuntime`
(`internal/runner/preflight.go:42-61`), returns `nil, nil` immediately for the
Claude runtime (`internal/runner/preflight.go:43-45`) and otherwise judges only
`agent:` and `budget_usd`. It never asks whether the binary exists.

**(b) The failure is a spawn error raised inside the run.** The one exec seam
is `exec.CommandContext(ctx, r.binary, ...)` at
`internal/runner/cli.go:252`. When `cmd.Run()` fails with anything that is not
an `*exec.ExitError`, the runner returns
`&NodeSpawnError{Runtime: ..., Err: runErr}` (`internal/runner/cli.go:306-311`),
whose message is (`internal/runner/cli.go:150-156`):

```go
return fmt.Sprintf("%s run: spawn failed: %v", runtime, e.Err)
```

The `%v` is Go's own `exec` error. For `auto`, the planner call wraps it once
more — `fmt.Errorf("planner run: %w", err)`,
`internal/coordinator/coordinator.go:540` — and, being non-repairable, becomes
a `PlanRejection` (`internal/coordinator/coordinator.go:462-463`) whose
`Error()` is the wrapped error unchanged when no repair ran
(`internal/coordinator/repair.go:217-220`). With no spec and no cost,
`noteRejectedPlan` prints nothing extra and returns the error as-is
(`cmd/oh-my-graph/main.go:1500-1507`), so it lands on **stderr** behind
`oh-my-graph: ` with exit 1 (`cmd/oh-my-graph/main.go:108-111`).

The exact composed sentence — `oh-my-graph: planner run: claude run: spawn
failed: exec: "claude": executable file not found in $PATH` — is my
composition of `cmd/oh-my-graph/main.go:110`,
`internal/coordinator/coordinator.go:540` and `internal/runner/cli.go:155`
over Go's standard `exec.Error` text. **I did not execute it.** The three
oh-my-graph fragments are quoted from those lines; the trailing clause is
Go's, not this repo's.

**(c) It takes about eight seconds to say so.** `auto`'s planner call goes
through `runAssessorWithSpawnRetry`
(`internal/coordinator/assess.go:243-259`), which retries a `*NodeSpawnError`
up to `assessorSpawnAttempts = 5` (`internal/coordinator/assess.go:214`),
separated by `assessorSpawnRetryDelay = 2 * time.Second`
(`internal/coordinator/assess.go:219`). The comment at
`internal/coordinator/assess.go:212-213` states the consequence itself: "a
machine that genuinely lacks the CLI still fails, just eight seconds later,
and says the same thing it said before."

**(d) A run directory is left behind.** For `auto`, the leg is opened and
`beginPlanning()` called BEFORE the planner call
(`cmd/oh-my-graph/main.go:634-650`), so a spawn failure at planning time
closes a real run with outcome `failed`
(`cmd/oh-my-graph/main.go:663-666`). A newcomer whose `claude` is not
installed therefore acquires a FAILED run in `runs list` on their first
attempt.

For `run` (a hand-written graph, e.g. the README's `haiku-smoke.yaml`), there
is no planner call: the spawn error surfaces per node through
`s.recordFail(led, h, node, outcome, ..., runErr)`
(`internal/schedule/scheduler.go:848`), i.e. in the ledger's DETAIL column and
in `events.jsonl`, and the run fails.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **NO.**
The text does name the binary and does say "executable file not found in
$PATH", which is actionable in isolation — but it arrives (i) after an
eight-second silence, (ii) on stderr under a `planner run:` prefix that
implies a planning problem, and (iii) having already created a failed run
directory. Nothing at any point says "install `claude` and log in", which is
the sentence the reader needs; `docs/INSTALL.md:17-23` says it, and no error
path points there.

---

## Step 8 — the provider CLI is present but LOGGED OUT

**What I ran**

NOT EXECUTED — no shell grant for a scratch `HOME` in this run, so a
logged-out CLI state could not be staged without disturbing the operator's own
`~/.claude` / `~/.codex`.

**What appeared**

source-read, not executed. A logged-out CLI **starts**, so it does not take
the `NodeSpawnError` branch at `internal/runner/cli.go:306-311` at all: it
returns an `*exec.ExitError`, `exitCode` is set at
`internal/runner/cli.go:312`, and the diagnosis becomes whatever the CLI wrote
to stderr, flattened into `outcome.FailureCause`
(`internal/runner/cli.go:322-324`, via `flattenLines`/`tailOf` at
`:331-351`). For `auto`, that path produces `PlanError{Reason:
fmt.Sprintf("planner exited with code %d", outcome.ExitCode)}`
(`internal/coordinator/coordinator.go:543-548`).

The one branch anywhere that inspects a CLI's own failure text is the session
limit — `outcome.SessionLimited = isSessionLimitCause(outcome.FailureCause)`,
`internal/runner/cli.go:325-327`, Claude only. There is no login branch:

```sh
grep -rn "logged out|login|authenticat" cmd/
```

```text
(no matches)
```

and in `internal/` the only hits are prose in comments and one unrelated
`jq` fragment (`internal/childenv/childenv.go:3` and `:31`,
`internal/serve/serve.go:31`, `internal/verify/shell_test.go:16`,
`internal/runner/shipped_graphs_runtime_test.go:41`,
`internal/graph/shipped_graphs_test.go:367`,
`internal/serve/ui/index.html:23`) — none of them an error path.

**Therefore: 'logged out' cannot be distinguished from any other spawn failure
without an actual login.** More precisely, and this is the sharper form: a
logged-out CLI is not a spawn failure at all — it is an ordinary non-zero exit,
and oh-my-graph cannot tell it from a bad prompt, a rate limit that is not the
session limit, or a crashed CLI, except by whatever prose the provider happened
to print into `FailureCause`. Confirming which prose that is requires a real
logged-out CLI, which this run has no grant to produce.

**Verdict** — Could a newcomer act on this WITHOUT reading the source? **NO —
and this one is not fixable by better wording alone.** The engine has no
signal to word. Whatever the reader sees is the provider CLI's own stderr tail,
relayed under an oh-my-graph prefix, with the same shape as every other
non-zero exit.

---

## The three starting hypotheses

### H1 — README leads with `go install` while prebuilt binaries already exist

**CONFIRMED.**

- The lead: `README.md:47-48`, `go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest`, the first command in the Quickstart (heading at `README.md:45`).
- The binaries: `.goreleaser.yaml:18-23` (darwin/linux × amd64/arm64) and `.goreleaser.yaml:27-35` (`tar.gz`, `{{ .ProjectName }}_{{ .Version }}_{{ .Os }}_{{ .Arch }}`), with `checksums.txt` at `.goreleaser.yaml:41-42`.
- The gap: the prebuilt path is first named at `README.md:118-119`, 70 lines below, and only as a link to `docs/INSTALL.md`.
- The unstated preconditions: `go.mod:3` (`go 1.25.0`) and `PATH`, the latter with zero README mentions (`grep -n "PATH\|GOPATH\|GOBIN" README.md` → no matches).

### H2 — The first command a newcomer types can refuse (ADR 0030, exit 3)

**CONFIRMED on structure; the firing condition is source-read, not executed.**

- The refusal exists and is reached before any spend: `cmd/oh-my-graph/main.go:454` (`answerBuildEvidence(os.Stdout, verifyCommand, flags.buildDeclaration(), ".")`), `internal/coordinator/verifycmd.go:661`.
- The exit code is 3: `cmd/oh-my-graph/main.go:152-155`, documented at `cmd/oh-my-graph/main.go:27-30`.
- The trigger is 13 filename markers plus a `.csproj` glob: `internal/coordinator/verifycmd.go:438-452`, `:476-482`.
- **Refuted in one narrow sense:** the README's OWN first `auto` line does not refuse — `README.md:59` already passes `--accept-no-build-evidence`, explained at `README.md:54-58`. The refusal is reached by a reader who types the command from the synopsis (`cmd/oh-my-graph/main.go:183`), which advertises the flag but not the consequence of omitting it.
- **Not executed:** I did not run `auto`, so "this repository would refuse with `Detected several build signals (go.mod, Makefile)`" is a prediction from `ls -a` against `internal/coordinator/verifycmd.go:448-449` and the walk order at `:471-475`, not a transcript.

### H3 — A missing or logged-out CLI fails deep in a run rather than up front

**CONFIRMED for missing. CONFIRMED-and-worse for logged out.**

- No pre-flight of any kind: `grep -rn "LookPath" .` → no matches; `internal/runner/preflight.go:43-45` returns early for Claude.
- Missing CLI: the failure originates at the exec seam (`internal/runner/cli.go:252`), is typed at `internal/runner/cli.go:306-311`, and for `auto` arrives ~8 s later after 5 attempts (`internal/coordinator/assess.go:214`, `:219`, and the comment at `:212-213`).
- It arrives AFTER a run directory exists: the leg opens and `beginPlanning()` runs before the planner call (`cmd/oh-my-graph/main.go:634-650`); the failure closes it as `failed` (`cmd/oh-my-graph/main.go:663-666`).
- Logged out is worse than "deep": it is not a distinguishable state at all. There is no login branch in `cmd/` (`grep -rn "logged out|login|authenticat" cmd/` → no matches) and the only stderr-text inspection anywhere is the Claude session limit (`internal/runner/cli.go:325-327`).

---

## What this measurement does NOT cover

Every step below is UNMEASURED, with its reason. An unmeasured step reported as
unmeasured is worth more than an invented one.

1. **`go install github.com/jitokim/oh-my-graph/cmd/oh-my-graph@latest`** — not
   executed. No shell grant for a scratch `HOME` in this run; it also writes
   outside this worktree.
2. **The prebuilt-binary path** (`curl` the archive, `shasum -c`, `tar xzf`,
   `./oh-my-graph version` — `docs/INSTALL.md:42-50`) — not executed. No grant
   for `curl` or `tar`, and no network.
3. **The published asset NAMES on a real release page** — not read. The names
   in this file are rendered from `.goreleaser.yaml:31-35` and cross-checked
   against `docs/INSTALL.md:44`; no release page was fetched.
4. **Bare `oh-my-graph init` in a fresh directory** — not executed. No grant to
   create a scratch directory, and running it here would write into a
   version-controlled `graphs/`. Its output is quoted from
   `cmd/oh-my-graph/init.go:127`, `:144`, `:151-156` and `:162-164`, not from a
   transcript. In particular: **the exact number of files `init` writes was not
   measured** — `ls graphs` shows nine top-level entries, one of which
   (`embed.go`) is Go source and one (`fragments`) a directory, and the payload
   is whatever the `//go:embed *.yaml fragments/*.yaml` directive at
   `graphs/embed.go:26` resolves to at build time.
5. **`oh-my-graph auto` in any form, including `--plan-only`** — not executed,
   per this node's scope. Therefore the ADR 0030 refusal text has never been
   seen on a screen in this measurement; it is transcribed from
   `internal/coordinator/verifycmd.go:688-725`.
6. **`oh-my-graph run graphs/haiku-smoke.yaml`** — not executed. It spawns a
   model CLI and spends plan allowance. "First successful node" is therefore
   the ONE step of the brief this walk never reached: **no node was run, so no
   first success was observed.** What was observed is that the graph the README
   names loads and validates (`go run ./cmd/oh-my-graph lint
   graphs/haiku-smoke.yaml` → `graphs/haiku-smoke.yaml: valid`).
7. **The missing-CLI error as a composed sentence** — not executed. Assembled
   from `cmd/oh-my-graph/main.go:110`,
   `internal/coordinator/coordinator.go:540` and
   `internal/runner/cli.go:155`; the final clause is Go's `exec.Error` text,
   not this repo's.
8. **The eight-second retry delay** — not timed. Derived as 4 × 2 s from
   `internal/coordinator/assess.go:214` and `:219`, and corroborated by the
   comment at `:212-213`, which states "eight seconds" itself.
9. **The logged-out CLI's actual stderr text** — not obtainable. Staging a
   logged-out state needs a scratch `HOME`, and there is no shell grant for one
   in this run.
10. **The live view / browser open** (`README.md:172-175`) — not executed. It
    is the fourth exec seam and requires a real run.
11. **Windows and Linux** — not measured. One machine, darwin 22.6.0.
12. **Whether any of this friction actually stops a real newcomer** — not
    measurable here by construction. This is a source-and-transcript walk by a
    reader who already knows the codebase; it establishes that the friction
    EXISTS and where, not what it costs a stranger.
