package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// runRuns is the `runs` subcommand group. Its only action today is `list`;
// a group (rather than a bare `list` top-level command) keeps room for later
// run-directory queries (`runs show`, `runs clean`) without another top-level
// name.
func runRuns(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("runs: missing subcommand (usage: oh-my-graph runs list)")
	}
	switch args[0] {
	case "list":
		if len(args) > 1 {
			return fmt.Errorf("runs list: unexpected argument %q (usage: oh-my-graph runs list)", args[1])
		}
		return listRuns(os.Stdout, os.Stderr, runsRoot())
	default:
		return fmt.Errorf("runs: unknown subcommand %q (want list)", args[0])
	}
}

// runSummary is one run's row in the `runs list` table, derived entirely from
// the two files the run directory persists (state.json and events.jsonl) —
// never from re-running anything.
type runSummary struct {
	runID     string
	graphName string
	nodeCount int
	// costUSD is the sum of the run's per-node reported costs so far. The
	// snapshot does not persist an auto run's one-time planning cost, so that
	// call is not included here (unlike the end-of-run ledger total).
	costUSD float64
	// verdict is verdictRunning for an in-flight run (see runfeed.InFlight), else
	// PASS only when every node in the graph reached VerdictPass — a failed,
	// paused, or interrupted run all render as FAIL.
	verdict string
	// hasSnapshot is false for the one legitimate snapshot-less row: a live
	// run whose first node has not completed yet (state.json is written only
	// after each node's terminal verdict), where graph name, node count and
	// cost are simply not known yet and render as placeholders.
	hasSnapshot bool
}

// listRuns renders one row per run directory under root, newest first, plus a
// total across the listed runs. An in-flight run — one whose event stream's
// last leg is still open (runfeed.InFlight) — is listed with verdict RUNNING, even
// before its first completed node has produced a state.json. listRuns is
// read-only over the run directories: a directory whose snapshot cannot be
// loaded (corrupt, or written by an incompatible schema) is reported as a
// warning on warnW and skipped, never deleted or rewritten. A missing root is
// not an error — it just means nothing has run yet.
func listRuns(w, warnW io.Writer, root string) error {
	// A root that does not exist yet reads as no entries at all: "nothing has
	// ever run here" and "nothing here is listable" are the same answer to the
	// user, so both fall through to the one empty-table path below rather than
	// printing the same message from two places.
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read runs dir %q: %w", root, err)
	}

	var rows []runSummary
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		row, err := summarizeRun(root, entry.Name())
		if err != nil {
			fmt.Fprintf(warnW, "WARNING: skipping run %q: %v\n", entry.Name(), err)
			continue
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		fmt.Fprintln(w, "No runs found.")
		return nil
	}

	// Newest first: run ids are second-resolution UTC timestamps chosen to
	// sort lexically (see newRunID), so a descending string sort is a
	// descending time sort.
	sort.Slice(rows, func(i, j int) bool { return rows[i].runID > rows[j].runID })

	printRuns(w, rows)
	return nil
}

// summarizeRun builds one run's row from its persisted files. It reuses the
// real readers rather than re-parsing anything by hand: runfeed.InFlight over
// the event stream (which refuses a stream schema newer than this binary,
// surfaced here as the WARNING+skip path) to tell a live run from a settled
// one, runstate.Load (which refuses an incompatible schema loudly) for the
// snapshot, and graph.Parse on the snapshot's own Graph bytes for the graph's
// name and node count — the same reconstruction path `resume` trusts.
//
// A live run whose first node has not completed yet has an open leg but no
// state.json at all (the snapshot is written only after each node's terminal
// verdict). That is a healthy run, not a broken directory, so it renders as a
// RUNNING row with only what is honestly known — the run id — rather than
// being skipped with a warning. Rendering placeholders was chosen over
// persisting a seeded snapshot at run start because it keeps `runs list`
// strictly read-only and leaves the snapshot's "written after every node"
// write discipline (DESIGN.md, docs/RUN-FEED.md) untouched.
func summarizeRun(root, runID string) (runSummary, error) {
	inFlight, err := runfeed.InFlight(filepath.Join(root, runID, runfeed.FileName))
	if err != nil {
		return runSummary{}, err
	}

	snap, err := runstate.Load(filepath.Join(root, runID, stateFileName))
	if err != nil {
		// Only a snapshot that is legitimately not written yet is excused, and
		// only for an in-flight run. A corrupt or incompatible snapshot is a
		// genuinely broken directory whether or not a leg is open — state.json
		// is written atomically, so a live run never has a half-written one —
		// and keeps the WARNING+skip path.
		if inFlight && errors.Is(err, fs.ErrNotExist) {
			return runSummary{runID: runID, verdict: verdictRunning}, nil
		}
		return runSummary{}, err
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return runSummary{}, fmt.Errorf("reconstruct graph: %w", err)
	}

	var cost float64
	for _, rec := range snap.Nodes {
		cost += rec.CostUSD
	}
	verdict := verdictWord(len(snap.CompletedNodes()) == len(g.Nodes))
	if inFlight {
		// Mid-run the snapshot holds only the nodes completed so far, so the
		// completed==all test above would read a healthy in-flight run as
		// FAIL. The open leg is the ground truth that it simply isn't done.
		verdict = verdictRunning
	}
	return runSummary{
		// The directory name, not snap.RunID: the directory name is the handle
		// `resume <run-id>` actually takes, so it is the one worth printing
		// even for a snapshot copied in from elsewhere.
		runID:       runID,
		graphName:   g.Name,
		nodeCount:   len(g.Nodes),
		costUSD:     cost,
		verdict:     verdict,
		hasSnapshot: true,
	}, nil
}

// printRuns writes the table: a header, one aligned row per run, and a footer
// with the run count and the cost total across every listed run. The column
// style mirrors the end-of-run ledger table so the two read as one tool. A
// snapshot-less RUNNING row keeps the same column widths with "-" in place of
// the values it cannot know yet, and counts toward the run count (its cost so
// far is zero by definition, so the total stays honest).
func printRuns(w io.Writer, rows []runSummary) {
	fmt.Fprintf(w, "%-17s %-24s %6s %10s  %s\n", "RUN", "GRAPH", "NODES", "COST(USD)", "VERDICT")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))

	var total float64
	for _, row := range rows {
		total += row.costUSD
		graphName, nodes, cost := row.graphName, "-", "-"
		if row.hasSnapshot {
			nodes = strconv.Itoa(row.nodeCount)
			cost = fmt.Sprintf("%.4f", row.costUSD)
		} else {
			graphName = "-"
		}
		fmt.Fprintf(w, "%-17s %-24s %6s %10s  %s\n",
			row.runID,
			graphName,
			nodes,
			cost,
			row.verdict,
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))
	fmt.Fprintf(w, "%d run(s), TOTAL COST: $%.4f\n", len(rows), total)
}

// verdictRunning is the verdict rendered for an in-flight run — deliberately
// outside the per-node PASS/FAIL vocabulary, because it describes a run that
// has no terminal judgement yet.
const verdictRunning = "RUNNING"

// verdictWord renders a settled run's overall verdict in the same vocabulary
// as the per-node ledger: PASS only when the whole graph completed
// successfully.
func verdictWord(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}
