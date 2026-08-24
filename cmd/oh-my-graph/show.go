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
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// runShow is the `show` subcommand: parse argv and render one persisted run's
// detail. It is read-only over the run directory — it loads the snapshot via
// runstate.Load (the same versioned reader `resume` trusts, so an incompatible
// schema is refused loudly) and never rewrites or deletes anything.
func runShow(args []string) error {
	// A dash-prefixed argument is a flag, not a run id (argslot.go), so
	// `show --help` answers with the synopsis instead of reporting an unknown
	// run called "--help" (#200).
	if err := flagInPositionalSlot(args, "show"); err != nil {
		return err
	}
	if len(args) == 0 {
		return fmt.Errorf("show: missing run id (usage: oh-my-graph show <run-id>)")
	}
	if len(args) > 1 {
		return fmt.Errorf("show: unexpected argument %q (usage: oh-my-graph show <run-id>)", args[1])
	}
	runID := args[0]
	return showRun(os.Stdout, runDirFor(runID), runID)
}

// showRun prints the run's STATUS and then its per-node ledger and total. An
// unknown run id — no run directory on disk — is a distinct, clearly worded
// error rather than a raw file-not-found, because it is the one failure the
// user causes by mistyping an id.
//
// The status line is ADR 0023 §2.6's addition, and it closes the hole that made
// `show` the surface with NO run-level answer at all: re-opening a paused run
// after the fact used to say nothing whatsoever about it being paused, since the
// per-node table has no row for "the run stopped and is waiting for you".
//
// A run directory that EXISTS but has no snapshot is no longer the unknown-run
// error either, and that distinction is the point: a run inside its planner call
// and a refused plan both live there permanently (ADR 0023 §2.1.1), and telling
// their owner "unknown run" about a directory this binary just derived a status
// for would be a lie the enumeration exists to stop. A mistyped id still has no
// directory, so it still gets the error.
func showRun(w io.Writer, runDir, runID string) error {
	if _, err := os.Stat(runDir); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("unknown run %q: no run directory at %s (see the run ids under %s)", runID, runDir, filepath.Dir(runDir))
		}
		return fmt.Errorf("stat run %q: %w", runID, err)
	}
	// The shared derivation, so `show` cannot disagree with `runs list`, the
	// dashboard, `watch` or ResolveRun about the same directory. An
	// unanswerable one (an unreadable stream or a corrupt snapshot) is reported
	// below by the reader that actually needs the bytes WHERE THERE IS ONE:
	// the missing-snapshot path returns it. On the path where the snapshot
	// loads there is no such reader — runstate.Load does not parse the graph
	// and the per-node table does not need it — so the failure is named there
	// instead, in runstatus.Unreadable's words rather than this file's. Silence
	// would drop the status word with nothing on screen
	// saying why, while `runs list` reports that same directory's error: the
	// cross-surface disagreement internal/runstatus exists to end. Gather rather than Of
	// because this surface renders the WORD, and a directory whose stream has
	// said nothing has no word to render (runstatus.Spoken, ADR 0023 §2.1.1).
	facts, statusErr := runstatus.Gather(runDir)
	var status runstatus.Status
	spoken := false
	if statusErr == nil {
		status, spoken = runstatus.Probe(runDir, facts), runstatus.Spoken(facts)
	}
	word := statusWord(status, spoken, statusErr)
	// Whether THIS run is one of the ones still being worked on, in the words
	// `runs list` uses for the whole corpus and `watch` uses for one run — the
	// same runstatus.InFlightClause, over a slice of one. The status word alone
	// leaves it to be inferred from a six-value vocabulary the reader has to
	// know by heart; "0 in flight" says it. It is empty exactly when the word
	// is: a directory with no status makes no claim about being alive either,
	// and it is the one case where saying zero would be a guess rather than an
	// answer.
	live := ""
	if word != "" {
		live = runstatus.InFlightClause(status)
	}

	statePath := filepath.Join(runDir, stateFileName)
	snap, err := runstate.Load(statePath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			if statusErr != nil {
				return fmt.Errorf("read run %q: %w", runID, statusErr)
			}
			if word != "" {
				fmt.Fprintf(w, "Run %s — %s; %s.\n", runID, word, live)
			} else {
				fmt.Fprintf(w, "Run %s\n", runID)
			}
			fmt.Fprintf(w, "No per-node record yet: %s\n", noRecordReason(status, spoken, runDir))
			accounting, accountingErr := runfeed.ReadAccounting(filepath.Join(runDir, runfeed.FileName))
			if accountingErr != nil && !errors.Is(accountingErr, fs.ErrNotExist) {
				return accountingErr
			}
			usage := ledger.TokenUsage{
				InputTokens: accounting.Usage.InputTokens, CachedInputTokens: accounting.Usage.CachedInputTokens,
				OutputTokens: accounting.Usage.OutputTokens, ReasoningOutputTokens: accounting.Usage.ReasoningOutputTokens,
			}
			if accounting.CostUSD != 0 || accounting.CostUnknown || usage != (ledger.TokenUsage{}) {
				fmt.Fprintf(w, "PLANNING COST: %s\n", formatCost(accounting.CostUSD, accounting.CostUnknown))
				printAccountingFooter(w, accounting.CostUSD, accounting.CostUnknown, usage)
			}
			return nil
		}
		return fmt.Errorf("load run %q: %w", runID, err)
	}
	if statusErr != nil {
		// The shared sentence, not a wording of this command's: the same
		// directory named the same way by `runs list --show-skipped` and by
		// `watch`, carrying the same classification (runstatus.Unreadable). What
		// `show` adds is the table below it — the snapshot loaded, so there is
		// still a run to print; only its status is missing.
		fmt.Fprintln(w, runstatus.Unreadable(runID, statusErr))
	}
	printRunDetail(w, runID, word, live, showRecords(snap),
		snap.PlanningCostUSD, snap.PlanningCostUnknown, ledger.TokenUsage{
			InputTokens: snap.PlanningUsage.InputTokens, CachedInputTokens: snap.PlanningUsage.CachedInputTokens,
			OutputTokens: snap.PlanningUsage.OutputTokens, ReasoningOutputTokens: snap.PlanningUsage.ReasoningOutputTokens,
		})
	return nil
}

// statusWord is the status for `show`'s header, or "" when there is none to
// print — in which case the header simply omits it. There are two such cases
// and they are different in kind: the derivation could not ANSWER (an
// unreadable stream or a corrupt snapshot), in which case the reader that
// needed those bytes reports the damage itself and one directory should not
// produce two complaints; or the directory has said NOTHING yet, which has no
// status at all rather than a status this command failed to obtain.
func statusWord(status runstatus.Status, spoken bool, err error) string {
	if err != nil || !spoken {
		return ""
	}
	return status.String()
}

// noRecordReason says WHY a run directory holds no state.json, in terms of the
// status that was derived for it — the same split runstatus.Recovery makes,
// asked in the present tense. Without it the line reads as damage, which is
// exactly the misreading ADR 0023 §2.5 spends itself preventing: a missing
// snapshot is normal at three of the six statuses and permanent at one.
//
// The FAIL arm asks the directory rather than asserting: a refused plan does
// keep its rejected.json there (§3.1), but it is not the only settled run
// without a snapshot — a leg swept closed before the recorder's first write
// lands here too, with no rejected spec to point at, and pointing at a file
// that is not there is worse than saying less.
func noRecordReason(status runstatus.Status, spoken bool, runDir string) string {
	if !spoken {
		return "nothing has been written to this run's event stream yet, so it has no status either — the directory is created when the run takes its lock, an instant before its first event"
	}
	switch status {
	case runstatus.Planning:
		return "this run is still inside its planner call, so it has no graph yet"
	case runstatus.Abandoned:
		return "its leg died before any node settled, so there is nothing to resume from — run the graph again"
	case runstatus.Fail:
		if _, err := os.Stat(filepath.Join(runDir, rejectedSpecFileName)); err == nil {
			return "no node ever settled: this run's planner call was refused, and the spec it rejected is kept as " + rejectedSpecFileName + " in this directory"
		}
		return "no node ever settled — the run ended before its first node did"
	default:
		return "the snapshot is written after a node's first terminal verdict, and none has landed yet"
	}
}

// showRecords converts the snapshot's per-node records into ledger.Record rows
// sorted by node id — the same runstate→ledger conversion `resume` performs
// when it reconstructs a carried-forward ledger, reused here as the row type
// so the two views cannot disagree about what a node record means.
func showRecords(snap runstate.Snapshot) []ledger.Record {
	records := make([]ledger.Record, 0, len(snap.Nodes))
	for nodeID, rec := range snap.Nodes {
		records = append(records, ledger.Record{
			NodeID:      nodeID,
			SessionID:   rec.SessionID,
			CostUSD:     rec.CostUSD,
			CostUnknown: rec.CostUnknown,
			Usage: ledger.TokenUsage{
				InputTokens: rec.Usage.InputTokens, CachedInputTokens: rec.Usage.CachedInputTokens,
				OutputTokens: rec.Usage.OutputTokens, ReasoningOutputTokens: rec.Usage.ReasoningOutputTokens,
			},
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
// total includes the snapshot's run-wide planning accounting plus every node
// record, matching the end-of-run ledger even when the runtime reports tokens
// without a USD total.
//
// The VERDICT column renders through ledger.VerdictCell, so a PASS is
// qualified here exactly as it is in the end-of-run table (ADR 0016 §6).
// `show` is the surface someone opens to re-read a finished run — the surface
// #119's reporter would have opened — so it is the last place a self-reported
// PASS should be able to read as a verified one.
//
// status is the RUN-level word from the shared enumeration (ADR 0023 §2.6), on
// the header line beside the node count because that is the line a reader's eye
// already lands on; empty means the derivation could not answer and the header
// says only what it knows. It is deliberately not a column: it describes the
// run, and every row below it describes a node.
//
// live is runstatus.InFlightClause for that same run and rides the same line
// for the same reason — one read, not two — and the two arrive together
// precisely so they cannot disagree: both are computed from the one derived
// Status in showRun, and live is empty exactly when status is.
func printRunDetail(w io.Writer, runID, status, live string, records []ledger.Record, planningCostUSD float64, planningCostUnknown bool, planningUsage ledger.TokenUsage) {
	if status != "" {
		fmt.Fprintf(w, "Run %s — %s, %d node(s); %s.\n", runID, status, len(records), live)
	} else {
		fmt.Fprintf(w, "Run %s — %d node(s)\n", runID, len(records))
	}
	fmt.Fprintf(w, "%-*s %-*s %-*s %*s %*s  %s\n",
		showNodeWidth, "NODE",
		ledger.VerdictWidth, "VERDICT",
		showSessionWidth, "SESSION",
		showCostWidth, "COST(USD)",
		showDurationWidth, "DURATION",
		"DETAIL")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", showRuleWidth))

	total := planningCostUSD
	costUnknown := planningCostUnknown
	usage := planningUsage
	for _, rec := range records {
		total += rec.CostUSD
		costUnknown = costUnknown || rec.CostUnknown
		usage.Add(rec.Usage)
		cost := fmt.Sprintf("%.4f", rec.CostUSD)
		if rec.CostUnknown {
			cost = "unknown"
		}
		fmt.Fprintf(w, "%-*s %-*s %-*s %*s %*s  %s\n",
			showNodeWidth, rec.NodeID,
			ledger.VerdictWidth, ledger.VerdictCell(rec),
			showSessionWidth, sessionOrDash(rec.SessionID),
			showCostWidth, cost,
			showDurationWidth, formatDuration(rec.Duration),
			rec.Detail,
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", showRuleWidth))
	if planningCostUnknown || planningCostUSD != 0 {
		fmt.Fprintf(w, "PLANNING COST: %s\n", formatCost(planningCostUSD, planningCostUnknown))
	}
	printAccountingFooter(w, total, costUnknown, usage)
}

func printAccountingFooter(w io.Writer, total float64, costUnknown bool, usage ledger.TokenUsage) {
	if costUnknown {
		fmt.Fprintf(w, "TOTAL COST: %s\n", formatCost(total, true))
	} else {
		fmt.Fprintf(w, "TOTAL COST: $%.4f\n", total)
	}
	if usage != (ledger.TokenUsage{}) {
		fmt.Fprintf(w, "TOKEN USAGE: input %d, cached %d, output %d, reasoning %d\n",
			usage.InputTokens, usage.CachedInputTokens, usage.OutputTokens, usage.ReasoningOutputTokens)
	}
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
