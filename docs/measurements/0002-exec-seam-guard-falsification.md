# ADR 0002 §Guard — 12 probes at the exec-seam guard: 8 fired, 4 stayed silent, and the silent one that reaches the release binary is now red

**Measurement, 2026-08-22.** This file is a measurement, not a decision and not
a proposal. It records what happened when the guard in `internal/invariants`
was deliberately attacked with twelve compiling spawners, one at a time.

**Of the twelve probes, eight FIRED and four stayed SILENT. Of the four silent
ones, one is judged out of scope (B2-P9), two share a single root cause, and
that root cause is the only silent shape that links into the release binary
with an unscrubbed child environment. One test now closes it. The other two
silent shapes are left as findings.**

The starting condition of this exercise was that nobody had ever watched this
guard go red. Eight of twelve probes say it does.

## Provenance

| Input | Address |
|---|---|
| Guard audit (read-only) | run `20260822-085202.265887000-1`, `read-guard.out` |
| Probe batch 1 (direct `os/exec` shapes) | run `20260822-085202.265887000-1`, `probe-direct.out` |
| Probe batch 2 (indirect shapes) | run `20260822-085202.265887000-1`, `probe-indirect.out` |
| Guard under test | `internal/invariants/exec_seam_test.go`, `internal/invariants/prose_count_test.go` |

Both batches numbered their probes independently and both used the id `P5` for
different probes. They are disambiguated here as `B1-P5` and `B2-P5`.

Every `exec_seam_test.go` line number below at or under `:598` is unchanged by
the tightening in this exercise, which was inserted at `:600`. Addresses at
`:600` and beyond name the tightening, which lands in the commit AFTER the one
carrying this file.

---

## WHAT THE GUARD CHECKS

Six tests, two files. Each claim carries its address.

- **The walk root is the module root, and the walk is exhaustive, not a list.**
  `repoRoot := filepath.Join("..", "..")` at `internal/invariants/exec_seam_test.go:156`
  and `:226`; the walk itself is `filepath.WalkDir` at `:124`. Confirmed by
  measurement, not by reading: probe B1-P4 planted a spawner in `cmd/` — outside
  `internal/` — and the guard named it (`probe-direct.out` P4).
- **Skips are directory NAMES only.** `testdata`, `vendor`, `bin` at `:74-78`,
  plus dot- and underscore-prefixed directories at `:88`. Everything else,
  including `cmd/`, `docs/`, `graphs/` and the repo root, is walked.
- **Imports are parsed, not grepped.** `importLocalName` unquotes each import
  path and compares strings (`:604-619` pre-tightening, `:701` onward after),
  called at `:160`. So an alias is followed and a comment is not counted.
  Measured both ways: probe B1-P3 (`import xc "os/exec"`) FIRED and reported the
  normalised path `os/exec` (`probe-direct.out` P3); and four files mention
  `os/exec` in comments without being allowlisted while the suite stays green
  (`cmd/oh-my-graph/verifycmd.go:67`, `docs/measurements/0213b-compound-commands.go:48`,
  `internal/browser/browser.go:17`, `internal/childenv/childenv.go:18` —
  `read-guard.out` §WHAT IT CHECKS).
- **Build constraints are ignored; every file is parsed.**
  `parser.ParseFile(fset, path, nil, 0)` at `:143` — mode `0`, and `go/build` is
  not imported (`:9-19`). This is a strength, and it was measured: probe B2-P6
  hid an importer behind `//go:build probefifth`, `go list -tags probefifth`
  confirmed the file really joins the build, and the guard still named it
  (`probe-indirect.out` P6).
- **Exemption is per exact repo-relative path, not per package.**
  `allowedExecImporters` at `:28-42`, looked up by exact map key at `:167`.
  Measured: probe B1-P2 put a new file in the already-exempt `internal/runner`
  package and the guard named it (`probe-direct.out` P2).
- **The import test asserts presence, so it cannot pass on an empty scan.**
  The stale check at `:171-175`, with its reasoning at `:69-73`, requires every
  allowlisted path to have actually been FOUND.
- **A second predicate covers spawn primitives outside `os/exec`.**
  `directSpawnSelectors` at `:207-210` names `os.StartProcess` and
  `syscall.{ForkExec, StartProcess, Exec, CreateProcess}`; `directSpawnsIn`
  (`:274-333`) matches them against the file's own local name and flags a
  mere reference, not only a call. Measured red on both `os.StartProcess` and
  `syscall.ForkExec` (`probe-indirect.out` P7).
- **That matcher is itself proven against sources that do spawn.**
  `TestDirectSpawnScanFollowsTheImport` at `:341-453`, eleven synthetic cases.
- **The four call sites must scrub, in the right order.**
  `TestExecSeamCallSitesScrubEnv` at `:477-598` requires exactly one
  `exec.Command`/`CommandContext` per call-site file (`:508`), a
  `<recv>.Env = childenv.Scrub(...)` on that same receiver, placed after the
  constructor and before the first run or return.
- **Prose spawner counts are derived from the allowlist.**
  `TestProseSpawnerCountsMatchTheExecSeamAllowlist` at
  `internal/invariants/prose_count_test.go:156-223`.
- **`execSeamCallSites` is pinned to exactly one file per seam package.**
  `prose_count_test.go:173`. This constraint is why the tightening below is a
  new test rather than four new entries in that list — see §HOLE CLOSED.

## WHAT THE GUARD DOES NOT CHECK

- **`_test.go` is skipped wholesale.** `exec_seam_test.go:138`. Rationale at
  `:118-119`.
- **Transitive imports are not followed.** The verdict is each file's own direct
  import list (`:160`); `.Run()` is not in `directSpawnSelectors` (`:207-210`).
- **A spawn constructed inside an allowlisted file that is NOT a call site was
  unchecked.** `allowedExecImporters` (`:28-42`) exempts eight files;
  `execSeamCallSites` (`:105-110`) inspects four. **This is the hole closed by
  this exercise.**
- **`&exec.Cmd{...}` composite literals are not counted as spawns.**
  `isExecCommandCall` matches only `.Command`/`.CommandContext` (`:624-634`
  pre-tightening).
- **`golang.org/x/sys/unix.Exec` is not seen.** The selector map has two keys,
  `"os"` and `"syscall"` (`:207-210`). Unmeasured.
- **`syscall.Syscall(SYS_EXECVE, …)` is not seen.** Not in the name list
  (`:209`), and `syscall` itself is deliberately not import-gated (`:200-206`).
  Unmeasured.
- **cgo is structurally invisible.** `import "C"` plus `C.system()` leaves
  neither a Go import nor a selector to match (`:160`, `:207-210`). Unmeasured.
- **`internal/bin/` would be skipped although Go would compile it.** Skip rule
  `:74-78` and `:91`; `go help packages` names only `.`, `_` and `testdata` as
  toolchain-ignored. Unmeasured.
- **Symlinked directories: unverified.** `filepath.WalkDir` at `:124` does not
  follow them; the divergence from the toolchain was not measured.

---

## PROBES AND VERDICTS

One row per probe. "Builds" means `go build ./...` exited 0. Every verdict was
produced by the command in the last column, run with that probe — and only that
probe — live in the tree.

| ID | Shape | Exact path | Builds | Verdict | Command that produced the verdict |
|---|---|---|---|---|---|
| B1-P1 | new package, direct `import "os/exec"`, `exec.Command("say", msg).Run()` | `internal/notify/notify.go` (new) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B1-P2 | new file inside the already-exempt `runner` package, direct `os/exec` | `internal/runner/spawn_helper.go` (new) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B1-P3 | aliased import `xc "os/exec"`, `xc.Command(...).Run()` | `internal/notify/alias.go` (new) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B1-P4 | spawner outside `internal/`, `package main` | `cmd/oh-my-graph/pager.go` (new) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B1-P5 | second `exec.Command` in an allowlisted, non-call-site file; `.Env` unset | `internal/runner/procgroup_unix.go` (modified) | yes | **SILENT** | `go test ./internal/invariants/... -count=1` (also `go test ./internal/runner/... -count=1`, green) |
| B2-P5 | spawn in a `_test.go` of a non-seam package | `internal/ledger/spawn_probe_test.go` (new) | yes | **SILENT** | `go test ./internal/invariants/... -count=1` (compilation and execution confirmed by `go test ./internal/ledger/... -run TestProbeSpawn -count=1 -v`) |
| B2-P6 | importer hidden behind `//go:build probefifth` | `internal/notify/notify_tagged.go` (new) | yes (both tag states) | **FIRED** | `go test ./internal/invariants/... -count=1` (build membership by `go list -tags probefifth -f '{{.ImportPath}} files={{.GoFiles}} imports={{.Imports}}' ./internal/notify/`) |
| B2-P7a | `os.StartProcess` with no `os/exec` import | `internal/notify/notify.go` (new) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B2-P7b | `syscall.ForkExec` with no `os/exec` import | `internal/notify/notify.go` (new, overwrote P7a) | yes | **FIRED** | `go test ./internal/invariants/... -count=1` |
| B2-P8a | transitive: `os/exec` helper in a NEW package, caller imports only the helper | `internal/spawnutil/spawnutil.go` + `internal/notify/notify.go` (new) | yes | **FIRED**, names the helper only — the caller never appears in the message | `go test ./internal/invariants/... -count=1` |
| B2-P8b | same transitive chain with the helper moved INTO an allowlisted, non-call-site file | `internal/runner/procgroup_unix.go` (modified) + `internal/notify/notify.go` (new) | yes | **SILENT** | `go test ./internal/invariants/... -count=1` |
| B2-P9 | fifth spawner built by reusing `verify.ShellVerifier` on an arbitrary command | `internal/notify/notify.go` (new) | yes | **SILENT** | `go test ./internal/invariants/... -count=1` |

Every FIRED verdict named the offending file by path; none was vague. B2-P8a is
the one qualified FIRED: it went red because the helper file itself was a new
direct importer — the same reason as B1-P1 — not because the guard followed the
chain to the caller that decided to spawn.

---

## RANKED HOLES

Ranked by damage, which is not the same order as likelihood. Damage here means:
does an unscrubbed child reach a user's machine from a release binary?

### 1. A spawn constructed in an allowlisted file that is not a call site

Probes: **B1-P5** and **B2-P8b** — two probes, one root cause. `allowedExecImporters`
(`internal/invariants/exec_seam_test.go:28-42`) exempts eight files from the
import check; `execSeamCallSites` (`:105-110`) subjects four of them to the
scrub check. The four build-tagged procgroup files sit in the overlap, excused
by a claim written in the comment at `:99-104` — that they "never call
exec.Command/exec.CommandContext themselves, so they have no spawn to scrub" —
which nothing tested.

This is the only silent shape whose child is linked into the release binary. It
is also self-reinforcing: B2-P8a shows that putting a spawn helper in a new
package goes red immediately, which teaches a contributor to move the helper
somewhere already exempt — and B2-P8b shows that landing.

**CLOSED.** See §HOLE CLOSED.

### 2. A spawn in a `_test.go` of a non-seam package

Probe: **B2-P5**. `exec_seam_test.go:138` skips every `_test.go`. This is not
hypothetical: three `_test.go` files import `os/exec` today, and one of them,
`internal/coordinator/assessspawn_test.go:6`, is not in a seam package
(`probe-indirect.out` §HOLES ranks this first on likelihood, and gives the
enumerating command `rg '"os/exec"' --glob '*_test.go'`).

Damage is bounded and different in kind: test files are not linked into a
release binary, so this is not a user billing risk. It is a developer-machine
risk — CI and local runs really do spawn, and a developer machine may hold
`ANTHROPIC_API_KEY`.

**Left as a finding.** Cheapest known fix (`probe-indirect.out` §HOLES) is a
second thin allowlist plus an `includeTests bool` parameter threaded through
`walkRepoGoFiles` (`:120`), which changes shared walk machinery used by two
tests — larger than the fix taken below, and against a lesser damage.

### 3. The transitive caller is invisible even when the chain goes red

Probe: **B2-P8a**, as a qualified FIRED. The guard names the root file of a
spawn chain, never the caller that decided to spawn. Closing this means
tracking values and imports across packages, which is a rewrite of the guard's
premise, not a tightening of it.

**Left as a finding.**

### Out of scope, not counted as a hole: seam reuse

Probe: **B2-P9**. Silent, and it should stay silent. The invariant is that
exactly four objects spawn; here the spawning object is still
`verify.ShellVerifier` alone, and the protected property holds —
`ShellVerifier.buildCmd` (`internal/verify/shell.go:132`) routes the child env
through `childenv.Scrub` at `internal/verify/shell.go:135`, so
the arbitrary command runs without the provider API-auth variables. What B2-P9
exposes is an unrelated question (nothing restricts who may CALL a seam), and
forcing it into this guard would give no way to distinguish a legitimate
internal caller from an illegitimate one. Judgement recorded in
`probe-indirect.out` P9 §범위에 대한 판단.

---

## SHAPES THAT HELD

| Probe | Shape | Test that caught it | Message quality |
|---|---|---|---|
| B1-P1 | new package with direct `os/exec` | `TestOnlyTheFourExecSeamsImportOsExec` (`exec_seam_test.go:180`) | names the file path |
| B1-P2 | new file in an already-exempt package | same (`:180`) | names the file path; proves exemption is per-file |
| B1-P3 | aliased `os/exec` import | same (`:180`) | names the file, reports the normalised path |
| B1-P4 | spawner outside `internal/`, in `cmd/` | same (`:180`) | names the file path |
| B2-P6 | importer behind a build tag | same (`:180`) | names the file path |
| B2-P7a | `os.StartProcess`, no `os/exec` import | `TestNoDirectProcessSpawns` (`:244`) | names file and symbol |
| B2-P7b | `syscall.ForkExec`, no `os/exec` import | same (`:244`) | names file and symbol |
| B2-P8a | `os/exec` helper in a new package | `TestOnlyTheFourExecSeamsImportOsExec` (`:180`) | names the helper; the caller is absent |

Two of these are worth naming as design strengths rather than luck. B2-P6 is
caught because `parser.ParseFile(fset, path, nil, 0)` (`:143`) never evaluates a
build context, so the guard sees files `go build` does not. B2-P7 is caught
because a second predicate was written for exactly that bypass (`:207-210`).

---

## HOLE CLOSED

Hole 1 — a spawn constructed in an allowlisted, non-call-site file.

### Why this one, and why this shape of fix

It is the only silent shape that ships. And the cheapest fix for it turned out
NOT to be the one `probe-indirect.out` §HOLES proposed. That proposal was to add
the four procgroup files to `execSeamCallSites` and relax the exactly-one count.
It cannot be done cheaply: `prose_count_test.go:173` asserts `execSeamCallSites`
holds **exactly one file per seam package**, and derives the prose spawner count
from that agreement. Four new entries make that test fatal, so the fix would
have to edit a second test's invariant as well — a bigger decision than this
task allows.

The change taken instead adds one test and modifies nothing. It asks the
question from the other end: rather than trusting a list of files to inspect,
walk the repo for every non-test file that CONSTRUCTS an `*exec.Cmd` and require
that set to be exactly `execSeamCallSites`. Both lists, the shared walk, the
shared matcher and every existing test are untouched.

Address: `internal/invariants/exec_seam_test.go:600-695`, test
`TestOnlyTheFourCallSitesConstructCommands` at `:630`. It reuses
`walkRepoGoFiles` (`:120`), `importLocalName` (`:701`) and `isExecCommandCall`
(`:721`).

### The presence assertion

The whole guard is about absence, so a walk that reached nothing would report no
constructors and pass. The presence half therefore runs first and is fatal, in
three steps: files were parsed at all (`:652`), at least one constructor was
found (`:657`), and each of the four known call sites was seen **by its own
path** (`:665`). Only then does the absence half run (`:685`).

### Proof that the new assertion fires

Probe B1-P5 re-created verbatim in `internal/runner/procgroup_unix.go` —
`reapStragglers`, `pkill := exec.Command("pkill", "-9", "-g", strconv.Itoa(cmd.Process.Pid))`,
`.Env` never set:

```
$ go build ./...
(no output, exit 0)

$ go test ./internal/invariants/... -count=1
--- FAIL: TestOnlyTheFourCallSitesConstructCommands (0.03s)
    exec_seam_test.go:685: internal/runner/procgroup_unix.go calls exec.Command/exec.CommandContext but is not one of the four exec-seam call sites [internal/runner/cli.go internal/verify/shell.go internal/worktree/git.go internal/browser/exec.go], so TestExecSeamCallSitesScrubEnv never checks that its child environment goes through childenv.Scrub — and an unscrubbed child inherits the provider API-auth variables that silently move the tool off subscription billing onto the metered API. Being in allowedExecImporters does not cover this: that list says which files may IMPORT os/exec, and the procgroup files are on it precisely because they only mutate an already-built *exec.Cmd. Route the spawn through the seam's existing builder, or write the ADR for a new seam (docs/adr/0002, 0005, 0006) and add its single call site to execSeamCallSites.
FAIL
FAIL	github.com/jitokim/oh-my-graph/internal/invariants	2.459s
FAIL
```

Same probe, same command, before the tightening: `ok
github.com/jitokim/oh-my-graph/internal/invariants 2.444s` (`probe-direct.out`
P5). SILENT → FIRED.

Reverted — B1-P5 modifies a tracked file, so `git checkout --` is the revert,
not `git clean -f`:

```
$ git checkout -- internal/runner/procgroup_unix.go
(no output)

$ go test ./internal/invariants/... -count=1
ok  	github.com/jitokim/oh-my-graph/internal/invariants	2.516s
```

### Proof that the presence assertion is not decoration

A second, separate probe: `"internal": true` temporarily added to `skippedDirs`
(`exec_seam_test.go:74`), blinding the walk to every seam. A test asserting only
absence would have passed.

```
$ go test ./internal/invariants/... -count=1 -run TestOnlyTheFourCallSitesConstructCommands
--- FAIL: TestOnlyTheFourCallSitesConstructCommands (0.01s)
    exec_seam_test.go:659: the repo walk parsed 23 Go file(s) but found no exec.Command/exec.CommandContext call anywhere, when the four exec seams each contain one. Either the walk is not reaching internal/, or isExecCommandCall stopped matching — either way this test can no longer see a spawn being constructed.
FAIL
FAIL	github.com/jitokim/oh-my-graph/internal/invariants	0.405s
FAIL
```

The 23 files are what remains of the walk with `internal/` cut out; the number
is reported by the assertion itself and is reproducible by the command above
with that one line restored.

### Gates

```
$ make test
go test ./... -race -count=1
ok  	github.com/jitokim/oh-my-graph/cmd/oh-my-graph	31.264s
?   	github.com/jitokim/oh-my-graph/graphs	[no test files]
ok  	github.com/jitokim/oh-my-graph/internal/browser	2.461s
ok  	github.com/jitokim/oh-my-graph/internal/childenv	1.427s
ok  	github.com/jitokim/oh-my-graph/internal/coordinator	4.704s
ok  	github.com/jitokim/oh-my-graph/internal/docsclaims	5.010s
ok  	github.com/jitokim/oh-my-graph/internal/fence	4.199s
ok  	github.com/jitokim/oh-my-graph/internal/gate	2.860s
ok  	github.com/jitokim/oh-my-graph/internal/graph	5.213s
ok  	github.com/jitokim/oh-my-graph/internal/handoff	4.518s
ok  	github.com/jitokim/oh-my-graph/internal/invariants	34.720s
ok  	github.com/jitokim/oh-my-graph/internal/ledger	3.697s
ok  	github.com/jitokim/oh-my-graph/internal/runfeed	3.071s
ok  	github.com/jitokim/oh-my-graph/internal/runner	13.300s
ok  	github.com/jitokim/oh-my-graph/internal/runstate	3.526s
ok  	github.com/jitokim/oh-my-graph/internal/runstatus	3.764s
ok  	github.com/jitokim/oh-my-graph/internal/schedule	6.044s
ok  	github.com/jitokim/oh-my-graph/internal/serve	12.116s
ok  	github.com/jitokim/oh-my-graph/internal/verify	3.831s
ok  	github.com/jitokim/oh-my-graph/internal/worktree	8.068s

$ make vet
go vet ./...
(no findings)

$ make fmt-check
(no output — no file needs gofmt)
```

---

## REVERTED — TREE CLEAN

Every probe in both batches, and both probes re-created here, were removed. The
tree carries only the two deliverables of this exercise: this file and the
tightening in `internal/invariants/exec_seam_test.go`.

The command that proves it, run after the last probe was reverted and before
either deliverable was committed:

```
$ git status --porcelain
 M internal/invariants/exec_seam_test.go
?? docs/measurements/0002-exec-seam-guard-falsification.md
```

Two lines, two deliverables, no probe. The `M` is the tightening; the `??` is
this file. Nothing from either probe batch survived into this working tree
either: the same command run at the very start of this session, before anything
was written, printed nothing at all.
