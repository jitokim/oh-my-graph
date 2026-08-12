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
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
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
	// status is the run's ONE user-visible status, from the shared derivation
	// and nowhere else (runstatus.Of, ADR 0023). This row used to carry a
	// second `verdict` string it composed itself out of the status and the
	// snapshot, and that composition is the defect ADR 0023 fixes: it printed
	// FAIL for a run paused at its gate, and again for one paused on the
	// session limit. There is now one value, and this column prints it.
	status runstatus.Status
	// hasSnapshot is false for the legitimate snapshot-less rows: a live run
	// whose first node has not completed yet (state.json is written only after
	// each node's terminal verdict), one that was abandoned before it ever got
	// that far, one still inside its planner call, and — since ADR 0023 §3 —
	// every refused plan, permanently. Graph name, node count and cost are
	// simply not known for any of them, and render as placeholders.
	hasSnapshot bool
}

// listRuns renders one row per run directory under root, newest first, plus a
// total across the listed runs. Every row's STATUS is one of the six values the
// shared derivation produces (runstatus.Of) — PLANNING while a run is still
// inside its planner call, RUNNING, ABANDONED when its leg's process is gone,
// PAUSED, PASS or FAIL — including before its first completed node has produced
// a state.json. listRuns is read-only over the run directories: a directory
// whose stream or snapshot cannot be READ (corrupt, or written by an
// incompatible schema) is reported as a warning on warnW and skipped, never
// deleted or rewritten; a file that is merely ABSENT is a fact about the run and
// keeps its row. A missing root is not an error — it just means nothing has run
// yet.
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

	// Newest first: a run id is a nanosecond UTC timestamp plus a per-process
	// sequence number, chosen to sort lexically (see newRunID), so a
	// descending string sort is a descending time sort.
	sort.Slice(rows, func(i, j int) bool { return rows[i].runID > rows[j].runID })

	printRuns(w, rows)
	return nil
}

// summarizeRun builds one run's row from its persisted files. It reuses the
// real readers rather than re-parsing anything by hand: runstatus.Of over the
// event stream and the run's lock (the shared derivation the dashboard card,
// ResolveRun and `watch` also go through; its stream walk refuses a schema
// newer than this binary, surfaced here as the WARNING+skip path) to tell a
// live run from an abandoned or settled one, runstate.Load (which refuses an
// incompatible schema loudly) for the snapshot, and graph.Parse on the
// snapshot's own Graph bytes for the graph's name and node count — the same
// reconstruction path `resume` trusts.
//
// A run whose first node has not completed yet has an open leg but no
// state.json at all (the snapshot is written only after each node's terminal
// verdict). That is a healthy run — or, if its leg died there, exactly the run
// an operator most needs to see — not a broken directory, so it renders as a
// row with only what is honestly known, the run id, rather than being skipped
// with a warning. Rendering placeholders was chosen over persisting a seeded
// snapshot at run start because it keeps `runs list` strictly read-only and
// leaves the snapshot's "written after every node" write discipline (DESIGN.md,
// docs/RUN-FEED.md) untouched.
//
// THE MISSING SNAPSHOT IS EXCUSED ON THE ERROR ALONE, never on the status, and
// that is ADR 0023 §2.1.1's guard rather than a simplification. Until then the
// excuse lapsed once a run SETTLED — which was survivable only because a settled
// run without a snapshot was nearly unreachable. Under six values FAIL is
// settled and every refused plan settles with no snapshot at all, so a
// status-keyed excuse would make each of those rows vanish behind a WARNING:
// this change would have shipped as a net loss, closing the corrupt-run channel
// for the PLANNING window it set out to fix while newly routing every refused
// plan into it. Keying on the error is also the honest predicate independently
// of the ADR — state.json is written atomically after a node's terminal
// verdict, so its ABSENCE never means damage at any status, while a corrupt or
// schema-incompatible one always does and keeps the WARNING+skip path.
func summarizeRun(root, runID string) (runSummary, error) {
	status, err := runstatus.Of(filepath.Join(root, runID))
	if err != nil {
		return runSummary{}, err
	}

	snap, err := runstate.Load(filepath.Join(root, runID, stateFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return runSummary{runID: runID, status: status}, nil
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
	return runSummary{
		// The directory name, not snap.RunID: the directory name is the handle
		// `resume <run-id>` actually takes, so it is the one worth printing
		// even for a snapshot copied in from elsewhere.
		runID:       runID,
		graphName:   g.Name,
		nodeCount:   len(g.Nodes),
		costUSD:     cost,
		status:      status,
		hasSnapshot: true,
	}, nil
}

// printRuns writes the table: a header, one aligned row per run, and a footer
// with the run count and the cost total across every listed run, and then one
// recovery hint per abandoned run and one resume hint per paused run. The
// column style mirrors the end-of-run ledger table so the two read as one tool.
// A snapshot-less row keeps the same column widths with "-" in place of the
// values it cannot know yet, and counts toward the run count (its cost so far
// is zero by definition, so the total stays honest).
//
// The header says STATUS and not VERDICT since ADR 0023 §2.6. The whole
// diagnosis behind that ADR is that liveness and verdict were being conflated,
// and leaving PLANNING/PAUSED/ABANDONED under a header that says VERDICT would
// preserve the conflation in the one place a user reads it. ADR 0015 already
// recorded this column as not a contract, so the rename costs nothing the
// content change did not already cost.
func printRuns(w io.Writer, rows []runSummary) {
	fmt.Fprintf(w, "%-17s %-24s %6s %10s  %s\n", "RUN", "GRAPH", "NODES", "COST(USD)", "STATUS")
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
			row.status,
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 70))
	fmt.Fprintf(w, "%d run(s), TOTAL COST: $%.4f\n", len(rows), total)

	// The per-row hints, under the table rather than interleaved between rows:
	// ABANDONED and PAUSED are the two rows a reader cannot act on without
	// being told how, and each hint is a sentence, not a column. Keeping the
	// table itself uniform is also what lets a human — or the `awk`-shaped
	// script ADR 0015 declines to promise anything to — keep reading it as a
	// table.
	for _, row := range rows {
		switch row.status {
		case runstatus.Abandoned:
			fmt.Fprintf(w, "\n%s\n", runstatus.Hint(row.runID, row.hasSnapshot))
		case runstatus.Paused:
			fmt.Fprintf(w, "\n%s\n", runstatus.PausedHint(row.runID))
		}
	}
}
