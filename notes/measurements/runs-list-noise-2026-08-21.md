# `runs list` skip noise — measured 2026-08-21

Branch `fix/runs-list-noise`, at `9590682` (`feat(auto): an unverified auto run is a
choice, not a default (ADR 0030) (#206)`). Corpus: the measuring user's own
`~/.oh-my-graph/runs`. `OMG_HOME` was **unset**, so the root resolved through
`omgHome()`/`runsRoot()` (`cmd/oh-my-graph/main.go:1743`, `:1756`) to
`/Users/imac/.oh-my-graph/runs`; every command below is written so that setting
`OMG_HOME` moves it with the tool.

Every number here carries the command that produced it, and every number was
produced twice — once by reading the files as text, once by walking them through
the repo's own readers. Where the two disagree, the disagreement is stated.

## 0. One correction to the brief before any number

The snapshot's schema key is **`schema`**, not `schema_version`
(`runstate.Snapshot.Schema`, `internal/runstate/runstate.go:361`:
`Schema int \`json:"schema"\``). A `grep` for `schema_version` over this corpus
matches nothing and would have reported zero old snapshots. The current constant
is `runstate.Schema = 3` (`internal/runstate/runstate.go:60`); the run feed
carries its own, unrelated, `runfeed.Schema = 3`
(`internal/runfeed/runfeed.go:46`).

## 1. Headline

| fact | value | source |
|---|---|---|
| run directories under the runs root | **320** | M1, M2 |
| directories `runs list` skips with a WARNING | **261** (81.6%) | M1, M2, M3 |
| directories `runs list` renders as a row | **59** | M2, M3 |
| distinct skip reasons actually present | **1** | M2 |
| total lines `oh-my-graph runs list` prints | **326** | M3 |
| of those, the repeated WARNING | **261** (80.1%) | M3 |

The reported shape ("~310 lines of which ~261 are one repeated WARNING") is
**right about the warning and low about the total**: 261 is exact, the total is
326, not ~310. 80.1% of what the command prints is one sentence with a different
run id in it.

## 2. Method 1 — text

The shell in the measuring session was sandboxed to the repo working directory,
so `ls`/`grep` against `$HOME` were refused. The counts below were taken with the
ripgrep/glob tooling instead, which reads the same bytes with the same engine.
Both spellings are given: the shell command a reader should run to reproduce, and
the tool call that actually produced the number here.

**Run directories (320).** Every run directory has an `events.jsonl` (the stream
is created with the run lock, before anything else), so counting streams counts
directories:

```sh
ls -1 "${OMG_HOME:-$HOME/.oh-my-graph}/runs" | wc -l
ls -1 "${OMG_HOME:-$HOME/.oh-my-graph}/runs"/*/events.jsonl | wc -l
```

Produced here by `Glob(pattern="*/events.jsonl", path="/Users/imac/.oh-my-graph/runs")`
→ **320 matching files**.

**Snapshots present (319), and the one directory with none (1).**

```sh
ls -1 "${OMG_HOME:-$HOME/.oh-my-graph}/runs"/*/state.json | wc -l
```

Produced here by `Glob(pattern="*/state.json", …)` → **319 matching files**.
320 − 319 = **1 run directory with no `state.json` at all.**

**Schema distribution (261 × v2, 58 × v3).** Note the key correction in §0; the
file is `json.MarshalIndent`-ed, so the literal text is `  "schema": 2,`:

```sh
grep -ho '"schema": [0-9]*' "${OMG_HOME:-$HOME/.oh-my-graph}/runs"/*/state.json \
  | sort | uniq -c
```

Produced here by two counting greps over `*/state.json`:

- `Grep(pattern='"schema": 2,', output_mode=count)` → **261 occurrences across 261 files**
- `Grep(pattern='"schema": 3,', output_mode=count)` → **58 occurrences across 58 files**

261 + 58 = 319 = every `state.json` on disk. No snapshot is missing the key, none
carries any other version, and no file carries the key twice.

## 3. Method 2 — the repo's own readers

A throwaway `tmp_measure/` (still on disk, untracked — see §7) walked the same
root through `runstatus.Gather` → `runstate.Load` → `graph.Parse`, which is
exactly `summarizeRun`'s control flow (`cmd/oh-my-graph/runs.go:159-226`), and
bucketed every directory by what the real code does with it.

```sh
go run ./tmp_measure
```

```
root                : /Users/imac/.oh-my-graph/runs
entries (all)       : 320
run directories     : 320
non-directory files : 0
listed (rows)       : 59
skipped (WARNINGs)  : 261
rows with no status : 0
dirs with NO state.json (raw stat): 1
--- skip reasons ---
   261  snapshot schema 2 != 3 (runstatus.go:301 <- runstate.go:621)
--- raw state.json schema distribution ---
   261  schema=2
    58  schema=3
--- status of listed rows ---
     1  ABANDONED
    13  FAIL
    43  PASS
     2  RUNNING
```

**Agreement with Method 1:** 320 = 320 directories, 319 = 319 snapshots (261 + 58),
1 = 1 snapshot-less directory, 261 = 261 skips. Nothing to reconcile.

The 59 rows are the 58 schema-3 snapshots plus the one snapshot-less directory,
which keeps its row on purpose: the absence of `state.json` is excused on the
*error* and never on the status (`cmd/oh-my-graph/runs.go:147-158`, ADR 0023
§2.1.1). That directory is `20260818-143934.603290000-1`, and it is the corpus's
one ABANDONED run — the run an operator most needs to see, which is precisely why
the excuse exists.

## 4. Method 3 — the shipped command, end to end

Built and run **from this worktree**, not from `$PATH` (a bare `oh-my-graph`
would run a different build):

```sh
go run ./cmd/oh-my-graph runs list 2>&1 | wc -l                     # 326
go run ./cmd/oh-my-graph runs list 2>&1 | grep -c '^WARNING: skipping run'   # 261
go run ./cmd/oh-my-graph runs list 2>&1 \
  | grep -c 'has schema version 2, but this build understands version 3'     # 261
```

All 261 WARNING lines are the same sentence. The line budget reconciles exactly:

```
326 total
= 261 WARNING lines                                   (stderr, runs.go:108)
+   1 header + 1 rule + 59 rows + 1 rule + 1 footer    (stdout, runs.go:258-299)
+   1 blank + 1 ABANDONED hint                         (stdout, runs.go:321)
```

The warnings go to `warnW` = `os.Stderr` and the table to `os.Stdout`
(`cmd/oh-my-graph/runs.go:41`), so `runs list 2>/dev/null` already yields a clean
65-line table today. That is worth knowing before designing the fix, and it is
not a fix: it also silences the one channel that reports genuine damage.

The dashboard was measured the same way — the real `serve.NewDashboard(root).Handler()`
answering the real `GET /api/cards` (source in §7):

```sh
go run ./tmp_measure/cards
```

```
GET /api/cards -> 200 application/json
cards total          : 320
cards with error text: 261
  of those, schema 2 : 261
--- card states ---
     1  abandoned
    13  failed
    44  passed
     1  running
   261  unknown
response bytes       : 191884
```

**The one discrepancy in this note.** Method 2 counted 43 PASS / 2 RUNNING;
`/api/cards` counted 44 passed / 1 running. The two programs ran minutes apart
against a **live** corpus, and one in-flight run settled between them. Both agree
on 59 non-skipped and on every skip number; only the liveness split moved, and it
moved by exactly one run in the direction time runs. No number in §1 depends on it.

## 5. Every distinct reason a run can be skipped or dropped

`runs list` has exactly **one** emitter — `cmd/oh-my-graph/runs.go:108` — but the
error it prints arrives from seven distinct code sites, plus two ways a directory
leaves the list with no warning at all. Only the first is present in this corpus.

| # | reason | code site | in corpus |
|---|---|---|---|
| 1 | snapshot schema ≠ this build's `runstate.Schema` | `internal/runstate/runstate.go:620-621` → `internal/runstatus/runstatus.go:301` → `cmd/oh-my-graph/runs.go:108` | **261** |
| 2 | snapshot exists but will not JSON-decode | `internal/runstate/runstate.go:617-618` → `runstatus.go:301` | 0 |
| 3 | snapshot exists but cannot be READ (permissions, EISDIR, I/O) | `internal/runstate/runstate.go:612-613` → `runstatus.go:301` | 0 |
| 4 | snapshot loads but its embedded graph will not parse | `internal/runstatus/runstatus.go:288-290` | 0 |
| 5 | event stream cannot be opened for a reason other than absence | `internal/runfeed/reader.go:70-72` → `runstatus.go:281` | 0 |
| 6 | event stream carries an event schema newer than this build | `internal/runfeed/reader.go:85-86` → `runstatus.go:281` | 0 |
| 7 | event stream unreadable mid-scan, incl. a line > 1 MiB | `internal/runfeed/reader.go:92-93` → `runstatus.go:281` | 0 |
| 8 | accounting re-walk of the stream fails, on the snapshot-less path | `cmd/oh-my-graph/runs.go:176-179` | 0 |
| 9 | *(race)* snapshot damaged between `Gather` and the second `Load` | `cmd/oh-my-graph/runs.go:190`, `:194` | 0 |
| — | **not a skip:** a non-directory entry under the runs root is dropped **silently**, with no warning | `cmd/oh-my-graph/runs.go:103-105` | 0 (0 non-dir entries) |
| — | **not a skip:** a missing `state.json` is a FACT, keeps its row | `cmd/oh-my-graph/runs.go:175-189` | 1 (rendered) |
| — | **not a skip:** an unreadable ROOT fails the whole command | `cmd/oh-my-graph/runs.go:96-99` | 0 |

Reason 1 is 261 of 261. Every other channel is dark in this corpus, which means a
fix aimed only at reason 1 removes 100% of today's noise — and means the other
eight have no field evidence and must not be quietly folded into whatever
summarizing treatment reason 1 gets.

## 6. Surface inventory

The repo's own scar: fixing one surface and leaving the others shouting. Every
surface that reads a run directory, with the site where the incompatible-snapshot
answer reaches a user.

| surface | file:line | emits the incompatible-snapshot answer? | machine-readable? |
|---|---|---|---|
| `runs list` — warnings | `cmd/oh-my-graph/runs.go:108` (`warnW` = stderr, `:41`) | **YES** — one full sentence per run, ×261 | no (human text) |
| `runs list` — table | `cmd/oh-my-graph/runs.go:285` | no — the run simply has no row | no; ADR 0015 promises nothing to `awk` |
| `show <run-id>` | `cmd/oh-my-graph/show.go:110` | **YES — FATAL.** `oh-my-graph show 20260730-204118` exits 1 printing the same sentence; there is no partial view | no |
| `show` — status-only failure | `cmd/oh-my-graph/show.go:113` | **YES** (`WARNING: this run's status could not be derived: …`) — reachable only when the snapshot loads but the stream does not; dark here | no |
| `watch <run-id>` — status line | `cmd/oh-my-graph/watch.go:95` | **no — swallowed.** `err == nil &&` drops the Gather error, so the status line is silently omitted and the tail proceeds | no |
| `watch` — stream schema | `cmd/oh-my-graph/watch.go:126` | no — a *stream* schema warning, unrelated to `state.json`; warns once and keeps rendering | no |
| `serve` dashboard — HTML card | `internal/serve/card.go:263` → `internal/serve/ui/dashboard.js:253` (`.card-error`) | **YES** — the full prose sentence painted on each of 261 `unknown` cards | no (HTML) |
| `serve` dashboard — `GET /api/cards` | `internal/serve/dashboard.go:230`; card built at `card.go:158,177` | **YES** — prose in the JSON `error` field, 261×; 191,884-byte response | **YES — JSON** |
| `serve` dashboard — `GET /api/cards/events` (SSE `card`) | `internal/serve/dashboard.go:318` | **YES** — same prose, re-sent whenever a run's stamp moves | **YES — JSON over SSE** |
| `serve` run view — `GET /api/graph` | `internal/serve/serve.go:514` | **YES** — HTTP 500, prose as a `text/plain` body | **YES — JSON endpoint, non-JSON error body** |
| `serve` run view — `GET /api/result` | `internal/serve/serve.go:586` | **YES** — HTTP 500 + prose | partly (artifact bytes) |
| `serve` run view — `GET /api/transcript` | `internal/serve/transcript.go:362` | **YES** — HTTP 500 + prose | **YES — JSON** |
| `serve` run view — `GET /api/events` (SSE) | `internal/serve/serve.go:670-676` | no — `stream_warning` covers a newer *stream* schema only; keeps forwarding by design | **YES — the run-feed contract verbatim** |
| `serve` gate — `POST /api/gate/{approve,reject}` | `internal/serve/gate.go:263` → `:190` `writeGateRefusal` | **YES** — the load error becomes the HTTP refusal body | **YES — HTTP status + body** |
| `serve` run selection | `internal/serve/resolve.go:77` | **no — swallowed.** `err == nil &&` means a broken run reads as "not in flight" and can still be chosen as the newest run to serve | n/a |
| `resume <run-id>` | `cmd/oh-my-graph/resume.go:153` | **YES — FATAL**, and here it is the honest answer: the snapshot really cannot be resumed | no |

Four of these are machine-readable and already carry human prose today:
`/api/cards`, `/api/cards/events`, `/api/transcript` and the gate endpoints — plus
`/api/graph`, whose JSON contract degrades to a plain-text 500. **None of them may
gain more prose**, and none may have its existing `error` string reshaped without
counting `internal/serve/ui/dashboard.js:253` and `app.js:503-509` as consumers.
The `error` field is the card's *only* explanation channel, so it cannot simply be
emptied either.

## 7. The throwaway programs (still on disk, untracked)

Both live under `tmp_measure/` at the repo root and were run with `go run`. Neither
was ever staged, and neither appears in any commit on this branch — but they were
**not** deleted: `rm` was refused by the harness permission mode on every attempt,
so `git status --short` still shows `?? tmp_measure/`, and because the directory
sits inside the module, `go vet ./...` and `go test ./...` compile it (two extra
`[no test files]` lines in the gate output). It is a stray build target, not a
repo change. Remove it with:

```sh
rm -rf /private/tmp/omg-runslist/tmp_measure
```

The full source of both programs is reproduced below, so deleting them loses
nothing.

`tmp_measure/main.go` — walks the runs root through the repo's own readers,
mirroring `summarizeRun` (`cmd/oh-my-graph/runs.go:159-226`) and attributing each
failure to a code site by re-calling the same sub-readers in the same order:

```go
package main

import (
	"encoding/json"; "errors"; "fmt"; "io/fs"; "os"; "path/filepath"; "sort"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

func runsRoot() string {
	if dir := os.Getenv("OMG_HOME"); dir != "" {
		return filepath.Join(dir, "runs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".oh-my-graph", "runs")
	}
	return filepath.Join(home, ".oh-my-graph", "runs")
}

// classify attributes a failure to one code site by calling the same
// sub-readers, in the same order, that Gather and summarizeRun call.
func classify(runDir string) string {
	if _, err := runfeed.LastLeg(filepath.Join(runDir, runfeed.FileName)); err != nil {
		return "stream unreadable (runstatus.go:281 <- runfeed.LastLeg)"
	}
	snap, err := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName))
	switch {
	case err == nil:
		if _, perr := graph.Parse(snap.Graph); perr != nil {
			return "snapshot graph will not parse (runstatus.go:290)"
		}
	case errors.Is(err, fs.ErrNotExist): // not an error at all
	default:
		var mismatch *runstate.SchemaMismatchError
		if errors.As(err, &mismatch) {
			return fmt.Sprintf("snapshot schema %d != %d (runstatus.go:301 <- runstate.go:621)",
				mismatch.Found, mismatch.Want)
		}
		var syn *json.SyntaxError
		var typ *json.UnmarshalTypeError
		if errors.As(err, &syn) || errors.As(err, &typ) {
			return "snapshot will not decode (runstatus.go:301 <- runstate.go:618)"
		}
		return "snapshot unreadable (runstatus.go:301 <- runstate.go:613)"
	}
	if _, err := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName)); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if _, aerr := runfeed.ReadAccounting(filepath.Join(runDir, runfeed.FileName)); aerr != nil &&
				!errors.Is(aerr, fs.ErrNotExist) {
				return "accounting walk failed (runs.go:178)"
			}
		}
	}
	return "unattributed"
}

func main() {
	root := runsRoot()
	entries, err := os.ReadDir(root)
	if err != nil {
		fmt.Printf("FATAL read runs dir %q: %v\n", root, err)
		os.Exit(1)
	}

	var dirs, nonDirs, listed, skipped, noSnapshot, spokenNo int
	reasons, schemas, statuses := map[string]int{}, map[int]int{}, map[string]int{}

	for _, entry := range entries {
		if !entry.IsDir() {
			nonDirs++
			continue
		}
		dirs++
		runDir := filepath.Join(root, entry.Name())

		// Raw schema tally, independent of the skip decision: read the file and
		// pull just the `schema` key (runstate.Snapshot.Schema).
		if data, rerr := os.ReadFile(filepath.Join(runDir, runstate.SnapshotFileName)); rerr == nil {
			var head struct {
				Schema int `json:"schema"`
			}
			if json.Unmarshal(data, &head) == nil {
				schemas[head.Schema]++
			} else {
				schemas[-1]++ // undecodable
			}
		} else if errors.Is(rerr, fs.ErrNotExist) {
			noSnapshot++
		} else {
			schemas[-2]++ // unreadable for another reason
		}

		// The real decision, through the real readers — summarizeRun's own flow.
		facts, gerr := runstatus.Gather(runDir)
		if gerr != nil {
			skipped++
			reasons[classify(runDir)]++
			continue
		}
		snap, lerr := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName))
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				if _, aerr := runfeed.ReadAccounting(filepath.Join(runDir, runfeed.FileName)); aerr != nil &&
					!errors.Is(aerr, fs.ErrNotExist) {
					skipped++
					reasons["accounting walk failed (runs.go:178)"]++
					continue
				}
				listed++
				if !runstatus.Spoken(facts) {
					spokenNo++
				}
				statuses[runstatus.Probe(runDir, facts).String()]++
				continue
			}
			skipped++
			reasons[classify(runDir)]++
			continue
		}
		if _, perr := graph.Parse(snap.Graph); perr != nil {
			skipped++
			reasons["reconstruct graph (runs.go:194)"]++
			continue
		}
		listed++
		if !runstatus.Spoken(facts) {
			spokenNo++
		}
		statuses[runstatus.Probe(runDir, facts).String()]++
	}

	fmt.Printf("root                : %s\n", root)
	fmt.Printf("entries (all)       : %d\n", len(entries))
	fmt.Printf("run directories     : %d\n", dirs)
	fmt.Printf("non-directory files : %d\n", nonDirs)
	fmt.Printf("listed (rows)       : %d\n", listed)
	fmt.Printf("skipped (WARNINGs)  : %d\n", skipped)
	fmt.Printf("rows with no status : %d\n", spokenNo)
	fmt.Printf("dirs with NO state.json (raw stat): %d\n", noSnapshot)
	// ... then prints reasons, schemas and statuses, each sorted.
}
```

`tmp_measure/cards/main.go` — asks the real dashboard handler for the real
payload, so the dashboard numbers are measured rather than inferred:

```go
package main

import (
	"encoding/json"; "fmt"; "net/http/httptest"; "os"; "path/filepath"; "sort"; "strings"

	"github.com/jitokim/oh-my-graph/internal/serve"
)

// runsRoot as above.

type card struct {
	RunID string `json:"run_id"`
	State string `json:"state"`
	Error string `json:"error"`
}

func main() {
	root := runsRoot()
	h := serve.NewDashboard(root).Handler()

	req := httptest.NewRequest("GET", "/api/cards", nil)
	req.Host = "127.0.0.1:8080" // requireLoopbackHost
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	var cards []card
	if err := json.Unmarshal(rec.Body.Bytes(), &cards); err != nil {
		fmt.Printf("decode cards: %v\n", err)
		os.Exit(1)
	}

	states, withError, schema2 := map[string]int{}, 0, 0
	for _, c := range cards {
		states[c.State]++
		if c.Error != "" {
			withError++
			if strings.Contains(c.Error, "has schema version 2, but this build understands version 3") {
				schema2++
			}
		}
	}
	fmt.Printf("cards total          : %d\n", len(cards))
	fmt.Printf("cards with error text: %d\n", withError)
	fmt.Printf("  of those, schema 2 : %d\n", schema2)
	// ... then prints the state histogram and rec.Body.Len().
	_ = sort.Strings
	_ = filepath.Join
}
```

## 8. What this implies for the fix

The problem is **one reason, repeated 261 times, on a surface with no
aggregation** — not a variety of damage and not a wrong verdict. The warning
itself is correct every single time: these snapshots genuinely were written by
schema 2 and genuinely cannot be resumed by a schema-3 build. So the fix is not
to stop refusing, and not to make `runs list` quieter about damage in general; it
is to stop paying one full sentence, including a 60-character absolute path, per
occurrence of an answer that is identical for 261 runs. A single aggregated line
("261 runs were written by an older snapshot schema (2); this build understands 3
— `runs list --verbose` to name them") collapses 261 lines to 1 and takes the
command from 326 lines to 66, while the eight currently-dark reasons in §5 keep
their per-run line, because each of those really is a different fact about a
different directory. Whatever grouping key is chosen must therefore be the
*reason*, not the run, and it must fall back to per-run reporting for anything it
cannot group — otherwise a genuinely corrupt snapshot gets summarized into
invisibility next to 261 merely-old ones.

Two constraints the inventory adds. First, **`runs list` is not where most of
this noise is spent.** The dashboard's `/api/cards` carries the same 261 prose
sentences in 191,884 bytes of JSON and paints 261 `unknown` cards, and
`/api/cards/events` re-sends them; `show` is worse than noisy — it exits 1 on
these runs and shows nothing at all. A fix that only touches `cmd/oh-my-graph/runs.go:108`
leaves the loudest surface untouched, which is the exact scar this repo already
carries. Second, **four of those surfaces are machine-readable** (`/api/cards`,
`/api/cards/events`, `/api/transcript`, the gate endpoints, plus `/api/graph`
whose JSON contract already degrades to a `text/plain` 500). Any aggregation must
stay on the human side of the line: the JSON `error` field is a per-run field and
must remain one, and `internal/serve/ui/dashboard.js:253` is its consumer. The
place to collapse 261 identical cards is the page, not the payload.

Two things worth deciding explicitly rather than by accident. `runs list`
already separates the channels — warnings on stderr, table on stdout
(`cmd/oh-my-graph/runs.go:41`) — so `2>/dev/null` gives a clean 65-line table
today; that is a workaround, not the fix, because it also hides real damage.
And `watch` (`watch.go:95`) and `ResolveRun` (`resolve.go:77`) currently *swallow*
this same error: they are the opposite defect on the same fact, and the fix
should decide whether that silence is deliberate rather than leaving the codebase
holding both extremes.
