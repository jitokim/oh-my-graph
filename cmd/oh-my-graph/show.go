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
			NodeID:     nodeID,
			SessionID:  rec.SessionID,
			CostUSD:    rec.CostUSD,
			BudgetUSD:  rec.BudgetUSD,
			Verdict:    ledger.Verdict(rec.Verdict),
			Duration:   rec.Duration,
			Detail:     rec.Detail,
			Provenance: rec.Provenance,
		})
	}
	sort.Slice(records, func(i, j int) bool { return records[i].NodeID < records[j].NodeID })
	return records
}

// showRuleWidth is this table's divider: the header line's own length, summed
// from the columns rather than restated as a number. The VERDICT column is
// ledger.VerdictWidth — the same constant the end-of-run table sizes it by, not
// a copy of it — so widening the qualifier set moves both tables at once
// instead of leaving this one to drift a literal at a time.
const (
	showNodeWidth     = 16
	showSessionWidth  = 38
	showCostWidth     = 10
	showDurationWidth = 12
	showDetailWidth   = 6 // len("DETAIL"), the header's last cell
	showRuleWidth     = showNodeWidth + 1 + ledger.VerdictWidth + 1 + showSessionWidth +
		1 + showCostWidth + 1 + showDurationWidth + 2 + showDetailWidth
)

// printRunDetail writes the detail table: a header, one aligned row per node
// (id, verdict, session, cost, duration, detail), and a total-cost footer. The
// column style mirrors the end-of-run ledger table so the two read as one
// tool; unlike that table it shows the full session id (this is the detail
// view someone copies an id out of) and each node's wall-clock duration. The
// total is the per-node sum: the snapshot does not persist an auto run's
// one-time planning cost, so that call is not included here (unlike the
// end-of-run ledger total).
//
// The VERDICT column renders through ledger.VerdictCell, so a PASS is
// qualified here exactly as it is in the end-of-run table (ADR 0016 §6).
// `show` is the surface someone opens to re-read a finished run — the surface
// #119's reporter would have opened — so it is the last place a self-reported
// PASS should be able to read as a verified one.
func printRunDetail(w io.Writer, runID string, records []ledger.Record) {
	fmt.Fprintf(w, "Run %s — %d node(s)\n", runID, len(records))
	fmt.Fprintf(w, "%-*s %-*s %-*s %*s %*s  %s\n",
		showNodeWidth, "NODE",
		ledger.VerdictWidth, "VERDICT",
		showSessionWidth, "SESSION",
		showCostWidth, "COST(USD)",
		showDurationWidth, "DURATION",
		"DETAIL")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", showRuleWidth))

	var total float64
	for _, rec := range records {
		total += rec.CostUSD
		fmt.Fprintf(w, "%-*s %-*s %-*s %*.4f %*s  %s\n",
			showNodeWidth, rec.NodeID,
			ledger.VerdictWidth, ledger.VerdictCell(rec),
			showSessionWidth, sessionOrDash(rec.SessionID),
			showCostWidth, rec.CostUSD,
			showDurationWidth, formatDuration(rec.Duration),
			rec.Detail,
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", showRuleWidth))
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
