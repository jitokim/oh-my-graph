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
	"time"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// runShow is the `show` subcommand: parse argv and render one persisted run's
// detail. It is read-only over the run directory — it loads the snapshot via
// runstate.Load (the same versioned reader `resume` trusts, so an incompatible
// schema is refused loudly) and never rewrites or deletes anything.
func runShow(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("show: missing run id (usage: oh-my-graph show <run-id>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("show: unexpected argument %q (usage: oh-my-graph show <run-id>)", args[1])
	}
	runID := args[0]
	return showRun(os.Stdout, runDirFor(runID), runID)
}

// showRun loads runDir's snapshot and prints the run's per-node ledger and
// total. An unknown run id — no snapshot on disk — is a distinct, clearly
// worded error rather than a raw file-not-found, because it is the one
// failure the user causes by mistyping an id.
func showRun(w io.Writer, runDir, runID string) error {
	statePath := filepath.Join(runDir, stateFileName)
	snap, err := runstate.Load(statePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unknown run %q: no snapshot at %s (see the run ids under %s)", runID, statePath, filepath.Dir(runDir))
		}
		return fmt.Errorf("load run %q: %w", runID, err)
	}
	printRunDetail(w, runID, showRecords(snap))
	return nil
}

// showRecords converts the snapshot's per-node records into ledger.Record rows
// sorted by node id — the same runstate→ledger conversion `resume` performs
// when it reconstructs a carried-forward ledger, reused here as the row type
// so the two views cannot disagree about what a node record means.
func showRecords(snap runstate.Snapshot) []ledger.Record {
	records := make([]ledger.Record, 0, len(snap.Nodes))
	for nodeID, rec := range snap.Nodes {
		records = append(records, ledger.Record{
			NodeID:    nodeID,
			SessionID: rec.SessionID,
			CostUSD:   rec.CostUSD,
			BudgetUSD: rec.BudgetUSD,
			Verdict:   ledger.Verdict(rec.Verdict),
			Duration:  rec.Duration,
			Detail:    rec.Detail,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].NodeID < records[j].NodeID })
	return records
}

// printRunDetail writes the detail table: a header, one aligned row per node
// (id, verdict, session, cost, duration, detail), and a total-cost footer. The
// column style mirrors the end-of-run ledger table so the two read as one
// tool; unlike that table it shows the full session id (this is the detail
// view someone copies an id out of) and each node's wall-clock duration. The
// total is the per-node sum: the snapshot does not persist an auto run's
// one-time planning cost, so that call is not included here (unlike the
// end-of-run ledger total).
func printRunDetail(w io.Writer, runID string, records []ledger.Record) {
	fmt.Fprintf(w, "Run %s — %d node(s)\n", runID, len(records))
	fmt.Fprintf(w, "%-16s %-10s %-38s %10s %12s  %s\n", "NODE", "VERDICT", "SESSION", "COST(USD)", "DURATION", "DETAIL")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 92))

	var total float64
	for _, rec := range records {
		total += rec.CostUSD
		fmt.Fprintf(w, "%-16s %-10s %-38s %10.4f %12s  %s\n",
			rec.NodeID,
			string(rec.Verdict),
			sessionOrDash(rec.SessionID),
			rec.CostUSD,
			formatDuration(rec.Duration),
			rec.Detail,
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 92))
	fmt.Fprintf(w, "TOTAL COST: $%.4f\n", total)
}

// sessionOrDash renders an empty session id as "-", matching the end-of-run
// ledger's convention for a node that never got a session.
func sessionOrDash(id string) string {
	if id == "" {
		return "-"
	}
	return id
}

// formatDuration renders a node's wall-clock time rounded to the millisecond —
// the snapshot stores nanoseconds, which are noise at human scale.
func formatDuration(d time.Duration) string {
	return d.Round(time.Millisecond).String()
}
