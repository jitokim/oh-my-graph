package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
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

// runSummary is one past run's row in the `runs list` table, derived entirely
// from its persisted snapshot — never from re-running anything.
type runSummary struct {
	runID     string
	graphName string
	nodeCount int
	// costUSD is the sum of the run's per-node reported costs. The snapshot
	// does not persist an auto run's one-time planning cost, so that call is
	// not included here (unlike the end-of-run ledger total).
	costUSD float64
	// passed is true only when every node in the graph reached VerdictPass —
	// a failed, paused, or interrupted run all render as FAIL.
	passed bool
}

// listRuns renders one row per run directory under root, newest first, plus a
// total across the listed runs. It is read-only over the run directories: a
// directory whose snapshot cannot be loaded (corrupt, or written by an
// incompatible schema) is reported as a warning on warnW and skipped, never
// deleted or rewritten. A missing root is not an error — it just means
// nothing has run yet.
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

// summarizeRun builds one run's row from its persisted snapshot. It reuses
// the two real readers rather than re-parsing anything by hand: runstate.Load
// (which refuses an incompatible schema loudly) for the snapshot, and
// graph.Parse on the snapshot's own Graph bytes for the graph's name and node
// count — the same reconstruction path `resume` trusts.
func summarizeRun(root, runID string) (runSummary, error) {
	snap, err := runstate.Load(filepath.Join(root, runID, stateFileName))
	if err != nil {
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
	return runSummary{
		// The directory name, not snap.RunID: the directory name is the handle
		// `resume <run-id>` actually takes, so it is the one worth printing
		// even for a snapshot copied in from elsewhere.
		runID:     runID,
		graphName: g.Name,
		nodeCount: len(g.Nodes),
		costUSD:   cost,
		passed:    len(snap.CompletedNodes()) == len(g.Nodes),
	}, nil
}

// printRuns writes the table: a header, one aligned row per run, and a footer
// with the run count and the cost total across every listed run. The column
// style mirrors the end-of-run ledger table so the two read as one tool.
func printRuns(w io.Writer, rows []runSummary) {
	fmt.Fprintf(w, "%-17s %-24s %6s %10s  %s\n", "RUN", "GRAPH", "NODES", "COST(USD)", "VERDICT")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))

	var total float64
	for _, row := range rows {
		total += row.costUSD
		fmt.Fprintf(w, "%-17s %-24s %6d %10.4f  %s\n",
			row.runID,
			row.graphName,
			row.nodeCount,
			row.costUSD,
			verdictWord(row.passed),
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))
	fmt.Fprintf(w, "%d run(s), TOTAL COST: $%.4f\n", len(rows), total)
}

// verdictWord renders a run's overall verdict in the same vocabulary as the
// per-node ledger: PASS only when the whole graph completed successfully.
func verdictWord(passed bool) string {
	if passed {
		return "PASS"
	}
	return "FAIL"
}
