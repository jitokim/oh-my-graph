# The feedback-quoting predicate fires 3 times in 11, and every hit is real

**Verdict: the predicate is SOUND and it ships.** Over three corpora — this
repo's shipped graphs, a 26-lane operator corpus, and 288 local run snapshots
deduplicated to 201 distinct resolved graphs — `handoff.LintFeedbackQuoting`
finds **11 feedback declarers and 3 hits, all 3 real** — with the caveat that
belongs on the number and not three paragraphs below it: **the 3 hits are three
lanes of ONE graph in ONE run**, so the precision evidence is n=1 distinct
defective graph against n=1 distinct control, not three independent
confirmations. **Noise: 0 of 3.** No shipped graph fires, so nothing in
`graphs/` needed fixing. The corpus also carries the specimen's *repair*, three
minutes later, and the sweep is correctly silent on it.

**3 of the 11 declarers were written by the planner**, not by a person — which
is the split that falsifies ADR 0028's first draft of decision 5 ("only a person
can write what it condemns") and is why the ADR now escalates the sweep to a
plan refusal for auto mode. All 3 quoted their payload correctly.

- **Date:** 2026-08-17 (KST), macOS (darwin 22.6.0), one machine.
- **Corpora:**
  - `graphs/*.yaml` in this checkout — 8 entry graphs. `graphs/fragments/*.yaml`
    is **not** a second corpus that came back clean: a fragment file cannot be
    linted at all (`oh-my-graph lint graphs/fragments/repair-round.yaml` exits 1
    on `invalid timeout "{{ with.review_timeout }}"`, because an unbound
    substitution is not a valid graph), so a fragment's loop is judged only
    through a graph that cites it. Of the five shipped fragments, `repair-round`
    is cited (twice) by `adr-driven-dev.yaml` and so IS in this population,
    spliced. Parsed rather than grepped, all five declare **0** `feedback:` arcs
    — the four single-node ones may not carry one at all (ADR 0013: `feedback`
    is the using graph's wiring), and the multi-node `repair-round` declares
    none. Read the 0 as "no shipped fragment declares an arc today", not as
    "fragments were swept";
  - `~/IdeaProjects/oh-my-graph-hq/lanes/graphs/*.yaml` — 26 operator lanes;
  - every `~/.oh-my-graph/runs/*/state.json` — 288 run directories,
    deduplicated by the full resolved graph JSON, so a re-run of a lane
    collapses to one row: **201 distinct graphs**.
- **Cost:** zero `claude` spawns. Three corpus reads and a parse.
- **Method:** the shipped loader and parser, never `grep` — `graph.LintLoadFile`
  for files (so `use:` is resolved and ids/tokens are the spliced ones) and
  `graph.Parse` for snapshots (whose `graph` member is the resolved graph that
  actually ran).
- **Re-derivable:** the whole program is in "Method" below and every number
  quoted here is an `assert` in it, so it fails rather than reports if the
  corpus on this machine has moved.

## The rule measured

For every node `D` declaring `feedback: { rerun: R }`: if no node in the loop
body other than `D` itself quotes `{{ feedback.D }}` in its **prompt**, warn on
`R`, naming `D`. (ADR 0028 §Decision.)

## Finding

| corpus | graphs | feedback declarers | hits | real | noise |
|---|---:|---:|---:|---:|---:|
| shipped `graphs/*.yaml` | 8 | 2 | **0** | — | — |
| operator lanes | 26 | 2 | **0** | — | — |
| run snapshots (deduped) | 201 | 7 graphs / **11** declarers | **3**¹ | **3** | **0** |

¹ The 3 hits are the three lanes (`qa-a`, `qa-b`, `qa-c`) of one fragment cited
three times in one graph, in one run. The dedup key is the resolved-graph JSON,
which collapses re-runs but not lanes within a graph, so this is **one distinct
defective graph judged three times**, and the control is likewise one graph.
Enough to ship an advisory; not three independent measurements of precision.

### Every declarer in the run corpus, and its verdict

| run | declarer | rerun | body | verdict |
|---|---|---|---|---|
| `20260802-080142.150241000-1` | `review` | `impl` | `impl, review` | quiet — `impl` quotes it |
| `20260803-081608.190042000-1` | `review` | `impl` | `impl, review` | quiet |
| `20260803-081635.836216000-1` | `review` | `impl` | `impl, review` | quiet |
| `20260803-084704.248072000-1` | `verify` | `impl` | `impl, docs, verify` | quiet — `impl` quotes it |
| `20260814-153554.116076000-1` | `review-a` | `dev-a` | `dev-a, e2e-a, review-a` | quiet — `dev-a` quotes it |
| `20260816-163759.091162000-1` | `qa-a/check` | `qa-a/build` | 2 nodes | **HIT** |
| `20260816-163759.091162000-1` | `qa-b/check` | `qa-b/build` | 2 nodes | **HIT** |
| `20260816-163759.091162000-1` | `qa-c/check` | `qa-c/build` | 2 nodes | **HIT** |
| `20260816-163954.329528000-1` | `qa-a/check` | `qa-a/build` | 2 nodes | quiet — the repair |
| `20260816-163954.329528000-1` | `qa-b/check` | `qa-b/build` | 2 nodes | quiet — the repair |
| `20260816-163954.329528000-1` | `qa-c/check` | `qa-c/build` | 2 nodes | quiet — the repair |

### Who wrote these arcs: 3 planner, 8 hand-written

The split ADR 0028's first draft assumed away. A run's `graph_source_path` says
where its graph came from, and auto mode's accepted plan is saved as
`graph.json` **inside the run directory** (`generatedSpecFileName`,
cmd/oh-my-graph/main.go), so a source path that is this run's own `graph.json`
is a planner-authored graph and anything else is a file a person wrote:

| run | source | declarers | author |
|---|---|---:|---|
| `20260802-080142.150241000-1` | `/tmp/loop-demo/demo.yaml` | 1 | hand |
| `20260803-081608.190042000-1` | this run's `graph.json` | 1 | **planner** |
| `20260803-081635.836216000-1` | this run's `graph.json` | 1 | **planner** |
| `20260803-084704.248072000-1` | this run's `graph.json` | 1 | **planner** |
| `20260814-153554.116076000-1` | `graphs/backlog-batch.yaml` | 1 | hand |
| `20260816-163759.091162000-1` | `graphs/qachain.yaml` | 3 | hand |
| `20260816-163954.329528000-1` | `graphs/qachain.yaml` | 3 | hand |

**3 planner-authored declarers, 8 hand-written**, and all three planner arcs
quote their payload at `impl` — the planner followed the prompt sentence that
asks for the pairing. Two consequences, both in ADR 0028:

- "only a person can write what this condemns" is **false**, so the sweep's
  standing had to be re-derived (advisory because an absent token has one
  legitimate reading, not because no machine writes one);
- the auto-mode escalation is a guard against a shape the planner *can* write,
  with **zero measured planner failures** behind it — unlike the reach refusal,
  which answered a measured $14 one (#118). What justifies it anyway is the
  price of being wrong: one re-plan, and a correction that is harmless when
  unnecessary.

### Judging the three hits: all real

All three are lanes of run `20260816-163759.091162000-1`
(`graphs/qachain.yaml`), the specimen ADR 0028 was written for. Lane A's two
nodes, verbatim from that run's `state.json`:

```yaml
- id: qa-a/build
  prompt: |
    Write a file at /tmp/t090/out-a.txt.
    Its ONLY content must be one word, chosen by this rule:
      - if a FEEDBACK section appears below, write: alpha
      - if there is no feedback section, write: draft
    Then reply with the bare word WROTE on the first line.

- id: qa-a/check
  depends_on: [qa-a/build]
  success_check:
    exit_zero: true
    result_matches: '^[*_`\s]*CHECKED\b'
    verify: { command: 'test "$(cat /tmp/t090/out-a.txt)" = "alpha"' }
  feedback: { rerun: qa-a/build, max: 1 }
```

The prompt **branches on a FEEDBACK section that can never appear**: the arc is
declared and correct, the payload was written to
`feedback/qa-a~check.out`, and `qa-a/build` was re-run — with no
`{{ feedback.qa-a/check }}` anywhere in its prompt. It wrote `draft` a second
time and the verify failed identically. The ledger recorded
`feedback round 1/1` and then `feedback exhausted`. Two rounds paid for, one
round's worth of information. This is the fault, not a resemblance to it —
the author's intent is *in the prompt text* and the mechanism to satisfy it is
missing. Real, ×3.

### The control: the same graph, repaired

Run `20260816-163954.329528000-1`, three minutes later, is the same three-lane
graph with the token added to the build prompt. Same topology, same declarers,
same bodies — **0 hits**. So the predicate separates the broken loop from its
own fix on real data, which a fixture cannot demonstrate.

### Why the shipped count is 2, and why nothing needed fixing

`review-loop.yaml::review` (`rerun: impl`) and
`backlog-batch.yaml::review-a` (`rerun: dev-a`) are the only two `feedback:`
declarations in `graphs/`, confirming ADR 0027's correction of the same figure.
Both rerun targets quote their payload — `{{ feedback.review }}` at the end of
`impl`'s prompt, `{{ feedback.review-a }}` at the end of `dev-a`'s. The 26
operator lanes contain the same two declarers because
`lanes/graphs/review-loop.yaml` and `lanes/graphs/backlog-batch.yaml` are
**byte-identical copies** of the shipped templates (`diff` reports no
difference); the other 24 lanes declare no arc at all, which matches ADR 0027's
finding that operator lanes write their loops out longhand.

### The one narrowing decision, and what the corpus says about it

ADR 0028 counts a quote in **any body node except the declarer**, not in the
rerun target alone. The corpus contains two three-node bodies
(`dev-a → e2e-a → review-a` and `impl → docs → verify`) and in both the quote
sits at the rerun target, so **all 11 declarers get the same verdict under
either rule**. The narrowing is therefore unfalsified rather than confirmed: it
is reasoning about a shape (`build → refine → check`, quoted at `refine`) the
corpus does not yet contain, taken in the direction that cannot produce a false
accusation.

### Method for the authorship split

Same snapshots, same dedup, reading one more field. Re-derivable:

```sh
python3 - <<'EOF'
import json, glob, os
seen = set()
for p in sorted(glob.glob(os.path.expanduser("~/.oh-my-graph/runs/*/state.json"))):
    d = json.load(open(p))
    g = d.get("graph")
    if not g: continue
    key = json.dumps(g, sort_keys=True)
    if key in seen: continue
    seen.add(key)
    decl = [n["id"] for n in g["nodes"] if n.get("feedback")]
    if not decl: continue
    run, src = os.path.basename(os.path.dirname(p)), d.get("graph_source_path", "")
    print(run, len(decl), "planner" if os.path.basename(src) == "graph.json" and run in src else "hand", src)
EOF
```

Output on 2026-08-17: 7 rows, 3 marked `planner` (1 declarer each) and 4 marked
`hand` (8 declarers), summing to the 11 above.

## What this does not measure

- **Whether a quoted payload is actually used.** The sweep sees a token in the
  prompt text, not comprehension. All 8 quiet declarers are quiet because the
  token is present; none of them was checked for whether the round it feeds
  converged.
- **Anything about runs older than the local corpus.** 261 of the 288
  snapshots are schema 2, which `runstate.Load` refuses by design. This
  measurement reads the `graph` member directly and parses it, which is why the
  denominator is 201 distinct graphs and not the 25 a `Load`-based pass would
  have seen. A future corpus pass that uses `runstate.Load` is silently
  measuring the last two weeks.

## Method

Zero spawns. Save as `measure_feedback_quote.go` in the repo root of a checkout
carrying `handoff.LintFeedbackQuoting`, then:

```sh
go run ./measure_feedback_quote.go graphs/*.yaml
go run ./measure_feedback_quote.go ~/IdeaProjects/oh-my-graph-hq/lanes/graphs/*.yaml
go run ./measure_feedback_quote.go --runs
```

```go
//go:build ignore

// Corpus measurement for the feedback-quoting sweep (ADR 0028).
//
// Two modes, both loading through the SHIPPED loader/parser — never grep —
// so ids and tokens are the ones that actually ran (fragment splicing
// included):
//
//	go run ./measure_feedback_quote.go graphs/*.yaml …   # graph FILES
//	go run ./measure_feedback_quote.go --runs            # ~/.oh-my-graph/runs/*/state.json
//
// --runs deduplicates by the full resolved graph JSON, so a re-run of the same
// lane collapses to one row.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "--runs" {
		measureRuns()
		return
	}
	measureFiles(os.Args[1:])
}

func measureFiles(paths []string) {
	graphs, declarers, hits, unloadable := 0, 0, 0, 0
	for _, path := range paths {
		issues, _, loaded, err := graph.LintLoadFile(path)
		if err != nil || len(issues) > 0 {
			unloadable++
			fmt.Printf("SKIP %s (load: err=%v issues=%d)\n", path, err, len(issues))
			continue
		}
		graphs++
		g := loaded.Graph
		for _, n := range g.Nodes {
			if n.Feedback == nil {
				continue
			}
			declarers++
			fmt.Printf("  declarer %s::%s rerun=%s body=%v\n", path, n.ID, n.Feedback.Rerun, g.FeedbackBody(n.ID))
		}
		for _, w := range handoff.LintFeedbackQuoting(g) {
			hits++
			fmt.Printf("  HIT %s: %s\n", path, w)
		}
	}
	fmt.Printf("\ngraphs loaded=%d unloadable=%d declarers=%d hits=%d\n", graphs, unloadable, declarers, hits)
	// Asserted for the two file corpora the ADR quotes: `graphs/*.yaml` (8
	// entry graphs, 2 declarers) and the 26 operator lanes (2 declarers, the
	// same two templates). Both must be hit-free — a shipped graph that fires
	// its own new lint is a defect in the graph.
	assert(hits == 0, "file-corpus hits", hits)
	assert(declarers == 2, "file-corpus declarers", declarers)
	fmt.Println("all asserts hold")
}

func measureRuns() {
	home, _ := os.UserHomeDir()
	states, _ := filepath.Glob(filepath.Join(home, ".oh-my-graph", "runs", "*", "state.json"))
	sort.Strings(states)

	seen := make(map[string]string) // resolved graph JSON -> first run id carrying it
	runsRead, distinct, withFeedback, declarers, hits, unparseable := 0, 0, 0, 0, 0, 0
	for _, path := range states {
		// Read the snapshot's `graph` member directly rather than through
		// runstate.Load: Load refuses a schema version this build does not
		// understand, and 261 of the 288 run directories on this machine are
		// schema 2. The graph member itself is re-parseable JSON in both
		// schemas, and it is the resolved graph — what actually ran.
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var snap struct {
			Graph           json.RawMessage `json:"graph"`
			GraphSourcePath string          `json:"graph_source_path"`
		}
		if err := json.Unmarshal(raw, &snap); err != nil || len(snap.Graph) == 0 {
			continue
		}
		runsRead++
		var canonical any
		if err := json.Unmarshal(snap.Graph, &canonical); err != nil {
			continue
		}
		key, _ := json.Marshal(canonical)
		runID := filepath.Base(filepath.Dir(path))
		if _, dup := seen[string(key)]; dup {
			continue
		}
		seen[string(key)] = runID
		distinct++

		g, err := graph.Parse(snap.Graph)
		if err != nil {
			unparseable++
			fmt.Printf("SKIP %s (parse: %v)\n", runID, err)
			continue
		}
		local := 0
		for _, n := range g.Nodes {
			if n.Feedback == nil {
				continue
			}
			local++
			declarers++
			fmt.Printf("  declarer %s::%s rerun=%s body=%v\n", runID, n.ID, n.Feedback.Rerun, g.FeedbackBody(n.ID))
		}
		if local > 0 {
			withFeedback++
		}
		for _, w := range handoff.LintFeedbackQuoting(g) {
			hits++
			fmt.Printf("  HIT %s (%s): %s\n", runID, snap.GraphSourcePath, w)
		}
	}
	fmt.Printf("\nruns read=%d distinct graphs=%d unparseable=%d with a feedback arc=%d declarers=%d hits=%d\n",
		runsRead, distinct, unparseable, withFeedback, declarers, hits)
	// Every number quoted in docs/measurements/0028-feedback-quote-corpus.md,
	// asserted rather than reported: if the corpus on this machine has moved,
	// this fails instead of quietly printing a different measurement.
	assert(runsRead == 288, "runs read", runsRead)
	assert(distinct == 201, "distinct graphs", distinct)
	assert(unparseable == 0, "unparseable", unparseable)
	assert(withFeedback == 7, "graphs with a feedback arc", withFeedback)
	assert(declarers == 11, "declarers", declarers)
	assert(hits == 3, "hits", hits)
	fmt.Println("all asserts hold")
}

func assert(ok bool, label string, got int) {
	if !ok {
		fmt.Fprintf(os.Stderr, "ASSERT FAILED: %s = %d\n", label, got)
		os.Exit(1)
	}
}
```

Output on 2026-08-17:

```
graphs loaded=8  unloadable=0 declarers=2 hits=0     # graphs/*.yaml
graphs loaded=26 unloadable=0 declarers=2 hits=0     # lanes/graphs/*.yaml
runs read=288 distinct graphs=201 unparseable=0 with a feedback arc=7 declarers=11 hits=3
```
