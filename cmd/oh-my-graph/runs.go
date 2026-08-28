package main

import (
	"errors"
	"flag"
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
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// runRuns is the `runs` subcommand group. Its only action today is `list`;
// a group (rather than a bare `list` top-level command) keeps room for later
// run-directory queries (`runs show`, `runs clean`) without another top-level
// name.
func runRuns(args []string) error {
	// This slot holds a subcommand name rather than a value, so an unknown
	// dash-prefixed token keeps its existing `unknown subcommand` answer, which
	// already names the alternative. Only the help request is intercepted
	// (argslot.go), because that one had no answer at all (#200). The FlagSet
	// goes with it so `runs --help` lists --show-skipped's description, the same
	// answer `runs list --help` gives: `list` is the only subcommand, so the
	// group's help and its own are the same help.
	if req := helpRequest(args, "runs", newRunsListFlags().set); req != nil {
		return req
	}
	if len(args) == 0 {
		line, _ := usageSynopsisFor("runs")
		return fmt.Errorf("runs: missing subcommand (usage: %s)", line)
	}
	switch args[0] {
	case "list":
		f := newRunsListFlags()
		if err := f.parse(args[1:]); err != nil {
			return err
		}
		return listRunsForExit(os.Stdout, os.Stderr, runsRoot(), f.showSkipped, f.exitInFlight)
	default:
		return fmt.Errorf("runs: unknown subcommand %q (want list)", args[0])
	}
}

// showSkippedFlag is the flag the summary line advertises, spelled once so the
// line the user reads and the flag they then type cannot drift apart.
const showSkippedFlag = "--show-skipped"

// runsListFlags holds `runs list`'s options. It is the first FlagSet under the
// `runs` group; the group keeps its own dispatch, and the flags belong to
// `list` because that is the command whose output they change.
type runsListFlags struct {
	showSkipped bool
	// exitInFlight moves the table's own answer onto the exit code. It changes
	// no byte of the output — see listRunsForExit for why the machine channel
	// is a code and not a sentence.
	exitInFlight bool

	set *flag.FlagSet
}

// newRunsListFlags builds a runsListFlags with its FlagSet configured.
func newRunsListFlags() *runsListFlags {
	f := &runsListFlags{set: flag.NewFlagSet("runs list", flag.ContinueOnError)}
	f.set.BoolVar(&f.showSkipped, "show-skipped", false,
		"name every run directory the table could not read, one line each on stderr")
	f.set.BoolVar(&f.exitInFlight, "exit-in-flight", false,
		"exit 4 instead of 0 while any listed run is still PLANNING or RUNNING")
	return f
}

// parse reads `list`'s argv, which is flags only — the subcommand name was
// already consumed by the group's dispatch, and `list` takes no positional.
func (f *runsListFlags) parse(args []string) error {
	if req := helpRequest(args, "runs", f.set); req != nil {
		return req
	}
	if err := f.set.Parse(args); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		line, _ := usageSynopsisFor("runs")
		return fmt.Errorf("runs list: unexpected argument %q (usage: %s)", f.set.Arg(0), line)
	}
	return nil
}

// runSummary is one run's row in the `runs list` table, derived entirely from
// the two files the run directory persists (state.json and events.jsonl) —
// never from re-running anything.
type runSummary struct {
	runID     string
	graphName string
	nodeCount int
	// costUSD is the sum of planning and per-node reported costs so far.
	costUSD     float64
	costUnknown bool
	usage       runner.TokenUsage
	// status is the run's ONE user-visible status, from the shared derivation
	// and nowhere else (runstatus.Of, ADR 0023). This row used to carry a
	// second `verdict` string it composed itself out of the status and the
	// snapshot, and that composition is the defect ADR 0023 fixes: it printed
	// FAIL for a run paused at its gate, and again for one paused on the
	// session limit. There is now one value, and this column prints it.
	status runstatus.Status
	// spoken is runstatus.Spoken: false only for a directory whose stream has
	// said nothing at all, which has no status to print (ADR 0023 §2.1.1). The
	// row still exists — the directory is a fact — and its STATUS cell carries
	// the same "-" the columns below use for what is not known yet.
	spoken bool
	// hasSnapshot is false for the legitimate snapshot-less rows: a live run
	// whose first node has not completed yet (state.json is written only after
	// each node's terminal verdict), one that was abandoned before it ever got
	// that far, one still inside its planner call, and — since ADR 0023 §3 —
	// every refused plan, permanently. Graph name, node count and cost are
	// simply not known for any of them, and render as placeholders.
	hasSnapshot   bool
	hasAccounting bool
}

// runsInFlightError is the answer `runs list --exit-in-flight` gives on the
// machine channel: at least one listed run is still working, so an operator
// asking "is everything done?" gets a no. It is not a failure of the command —
// the table printed, every directory that could be read was read — which is why
// it carries its own exit code (4) rather than joining exit 1, and why the
// sentence it holds is never printed: mainExitCode prints an error only for
// exit 1. The exit code IS the answer. A code exists at all because `runs
// list`'s table is explicitly not a contract (ADR 0015, open question 4), so a
// supervisor loop must never have to grep it.
type runsInFlightError struct{ count int }

func (e *runsInFlightError) Error() string {
	return fmt.Sprintf("%d listed run(s) still in flight", e.count)
}

// listRunsForExit is `runs list` as the CLI invokes it: the table for the human,
// then — only under --exit-in-flight — the same walk's answer for a machine.
// One derivation, two audiences: the count comes from the Status each row
// already holds (runstatus.Probe → Status.InFlight), so the settled/in-flight/
// abandoned rule is asked, never restated here.
//
// Without the flag the exit code is exactly what it has always been, so no
// existing `set -e` script changes behaviour.
//
// WHAT THE COUNT IS HONESTLY ABOUT: the runs this walk could READ. A directory
// skipped as unreadable is counted only on the coverage line, and a directory
// that has taken its lock but not yet written its first event has no status at
// all (runstatus.Spoken, ADR 0023 §2.1.1) and so counts as nothing here. Both
// are the shared rule's own limits rather than new ones taken on here, and
// neither is worth a second predicate: a loop polling this is asking about a
// corpus, and the answer it gets is about the corpus this binary could see.
func listRunsForExit(w, warnW io.Writer, root string, showSkipped, exitInFlight bool) error {
	inFlight, err := listRunsCountingInFlight(w, warnW, root, showSkipped)
	if err != nil {
		return err
	}
	if exitInFlight && inFlight > 0 {
		return &runsInFlightError{count: inFlight}
	}
	return nil
}

// listRuns is the table alone, for every caller that wants the rows rendered and
// has no exit code to decide — the shape this command had before the flag, and
// the shape the flag's default still produces.
func listRuns(w, warnW io.Writer, root string, showSkipped bool) error {
	return listRunsForExit(w, warnW, root, showSkipped, false)
}

// listRunsCountingInFlight renders one row per run directory under root, newest
// first, plus a total across the listed runs, and reports how many of those rows
// are IN FLIGHT — the count listRunsForExit turns into an exit code, taken from
// the rows this walk already derived rather than from a second pass or a second
// rule. Every row's STATUS is one of the six values the
// shared derivation produces (runstatus.Of) — PLANNING while a run is still
// inside its planner call, RUNNING, ABANDONED when its leg's process is gone,
// PAUSED, PASS or FAIL — including before its first completed node has produced
// a state.json. listRuns is read-only over the run directories: a directory
// whose stream or snapshot cannot be READ (corrupt, or written by an
// incompatible schema) is skipped, never deleted or rewritten; a file that is
// merely ABSENT is a fact about the run and keeps its row. A missing root is not
// an error — it just means nothing has run yet.
//
// WHAT A SKIPPED DIRECTORY COSTS THE READER is reported in one line under the
// table, always, from the shared accumulator (runstatus.Skipped): how many runs
// are shown out of how many were found, how many were skipped and per which
// reason. It used to be one WARNING line per skipped directory on warnW, which
// on a long-lived run home is the whole screen — 261 of 331 lines on the
// machine this was measured on, every one of them the same sentence about the
// same schema version. Those lines are not gone, they are behind showSkipped
// (--show-skipped) and still on warnW — one per skipped directory, now through
// runstatus.Unreadable (internal/runstatus/skipped.go:201), which replaced the
// old "WARNING: skipping run …" wording in this same commit because that
// sentence claimed a consequence only one of the three surfaces has. What
// changed by DEFAULT is that the counts are stated instead of enumerated.
//
// The count line is printed whether or not anything was skipped, because a
// reader must be able to tell "nothing was hidden from me" apart from "64 of
// 325 runs are shown" — a summary that appeared only when there was something
// to report would render those two cases identically. The one exception is a
// corpus with nothing in it at all, where "No runs found." already is that
// statement.
//
// HOW MANY RUNS ARE IN FLIGHT is on that same line, and at zero as much as at
// three. It used to be readable only as an inference from absence — no RUNNING
// row meant nothing was running, a conclusion equally consistent with a broken
// filter, a table that failed to render, or a derivation that answered wrong.
// The number is runstatus.InFlightClause over the rows' own derived statuses
// (shownStatuses), so this surface reports what runstatus decided rather than
// what the STATUS column happens to spell.
func listRunsCountingInFlight(w, warnW io.Writer, root string, showSkipped bool) (int, error) {
	// A root that does not exist yet reads as no entries at all: "nothing has
	// ever run here" and "nothing here is listable" are the same answer to the
	// user, so both fall through to the one empty-table path below rather than
	// printing the same message from two places.
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, fmt.Errorf("read runs dir %q: %w", root, err)
	}

	var rows []runSummary
	var skipped runstatus.Skipped
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		row, err := summarizeRun(root, entry.Name())
		if err != nil {
			skipped.Add(entry.Name(), err)
			continue
		}
		rows = append(rows, row)
	}

	// The detail stays on warnW rather than moving to w with the summary: it is
	// the same stream it has always been on, so a consumer redirecting the two
	// separately sees no change in either, and the table stays the only thing on
	// stdout besides its own footer.
	if showSkipped {
		for _, line := range skipped.Details() {
			fmt.Fprintln(warnW, line)
		}
	}

	if len(rows) == 0 {
		fmt.Fprintln(w, "No runs found.")
		// The one place the count line matters most: with every directory
		// unreadable, "No runs found." on its own says a full run home is empty.
		// nil, not the statuses of the rows: there are none, and "0 in flight"
		// is then a statement about a walk that read nothing, which is exactly
		// what the skipped count beside it says.
		if skipped.Len() > 0 {
			fmt.Fprintln(w, skipped.Line(0, nil, showSkippedFlag))
		}
		return 0, nil
	}

	// Newest first: a run id is a nanosecond UTC timestamp plus a per-process
	// sequence number, chosen to sort lexically (see newRunID), so a
	// descending string sort is a descending time sort.
	sort.Slice(rows, func(i, j int) bool { return rows[i].runID > rows[j].runID })

	printRuns(w, rows, skipped.Line(len(rows), shownStatuses(rows), showSkippedFlag))
	// The count, off the same Status the row printed. ABANDONED is deliberately
	// NOT counted: its leg's process is gone, so nothing is working on it, and a
	// consumer that read the event stream alone would have no way to say so —
	// it would see an open leg and wait on a corpse forever (docs/RUN-FEED.md,
	// "a consumer that cannot flock"). That is the half of ADR 0015's rule this
	// exit code exists to hand to a shell, and asking Status.InFlight is how it
	// is handed over rather than re-decided.
	inFlight := 0
	for _, row := range rows {
		if row.status.InFlight() {
			inFlight++
		}
	}
	return inFlight, nil
}

// shownStatuses is the derived Status of every row the table renders, in row
// order — the input the coverage line's in-flight clause is counted from. It is
// a projection and nothing more: each value came from runstatus.Probe in
// summarizeRun, so the count is over the SHARED derivation rather than over
// what the STATUS column happens to spell, and `runs list` cannot arrive at a
// different number than `show` does about the same run.
//
// A row that has NOT spoken contributes nothing, for the same reason its STATUS
// cell says "-" and no hint is printed beneath it (statusCell, printRuns): the
// derivation is total, so Probe answers for that directory anyway, and letting
// its default arm into a count would put a run the table declines to classify
// on one side of the ledger. Nothing is lost by dropping it — a stream that has
// said nothing has no open leg, so such a row can never be the in-flight one.
func shownStatuses(rows []runSummary) []runstatus.Status {
	statuses := make([]runstatus.Status, 0, len(rows))
	for _, row := range rows {
		if !row.spoken {
			continue
		}
		statuses = append(statuses, row.status)
	}
	return statuses
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
	runDir := filepath.Join(root, runID)
	// Gather rather than Of, because this row renders the WORD: a directory
	// whose stream has said nothing has no status, and that is a fact only the
	// facts carry (runstatus.Spoken). The snapshot is then loaded a second time
	// here for the graph name and the per-node costs, which the derivation does
	// not carry and this table cannot do without; one shared rule is worth the
	// second read on a command that runs once.
	facts, err := runstatus.Gather(runDir)
	if err != nil {
		return runSummary{}, err
	}
	status, spoken := runstatus.Probe(runDir, facts), runstatus.Spoken(facts)

	snap, err := runstate.Load(filepath.Join(runDir, stateFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			accounting, accountingErr := runfeed.ReadAccounting(filepath.Join(runDir, runfeed.FileName))
			if accountingErr != nil && !errors.Is(accountingErr, fs.ErrNotExist) {
				return runSummary{}, accountingErr
			}
			usage := runner.TokenUsage{
				InputTokens: accounting.Usage.InputTokens, CachedInputTokens: accounting.Usage.CachedInputTokens,
				OutputTokens: accounting.Usage.OutputTokens, ReasoningOutputTokens: accounting.Usage.ReasoningOutputTokens,
			}
			return runSummary{
				runID: runID, status: status, spoken: spoken,
				costUSD: accounting.CostUSD, costUnknown: accounting.CostUnknown, usage: usage,
				hasAccounting: accounting.CostUSD != 0 || accounting.CostUnknown || usage != (runner.TokenUsage{}),
			}, nil
		}
		return runSummary{}, err
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return runSummary{}, fmt.Errorf("reconstruct graph: %w", err)
	}

	cost := snap.PlanningCostUSD
	costUnknown := snap.PlanningCostUnknown
	usage := runner.TokenUsage{
		InputTokens: snap.PlanningUsage.InputTokens, CachedInputTokens: snap.PlanningUsage.CachedInputTokens,
		OutputTokens: snap.PlanningUsage.OutputTokens, ReasoningOutputTokens: snap.PlanningUsage.ReasoningOutputTokens,
	}
	for _, rec := range snap.Nodes {
		cost += rec.CostUSD
		costUnknown = costUnknown || rec.CostUnknown
		usage.InputTokens += rec.Usage.InputTokens
		usage.CachedInputTokens += rec.Usage.CachedInputTokens
		usage.OutputTokens += rec.Usage.OutputTokens
		usage.ReasoningOutputTokens += rec.Usage.ReasoningOutputTokens
	}
	return runSummary{
		// The directory name, not snap.RunID: the directory name is the handle
		// `resume <run-id>` actually takes, so it is the one worth printing
		// even for a snapshot copied in from elsewhere.
		runID:         runID,
		graphName:     g.Name,
		nodeCount:     len(g.Nodes),
		costUSD:       cost,
		costUnknown:   costUnknown,
		usage:         usage,
		status:        status,
		spoken:        spoken,
		hasSnapshot:   true,
		hasAccounting: cost != 0 || costUnknown || usage != (runner.TokenUsage{}),
	}, nil
}

// statusCell is the STATUS column's text: the derived word, or "-" for the one
// directory that has no status at all — one whose stream has said nothing yet
// (ADR 0023 §2.1.1). "-" is this table's own placeholder for what is not known
// yet, already carried by the three columns beside it, so the row stays a row
// and says only what it knows. Printing the derivation's default arm there
// instead would put a confident FAIL beside a card the dashboard is rendering
// as `pending`, about the same bytes.
func statusCell(row runSummary) string {
	if !row.spoken {
		return "-"
	}
	return row.status.String()
}

// printRuns writes the table: a header, one aligned row per run, a footer with
// the run count and the cost total across every listed run, then coverage — the
// one line saying how much of the run home this table actually covers and how
// many of the runs in it are in flight, from the shared accumulator
// (runstatus.Skipped.Line) — and then one recovery hint per
// abandoned run and one resume hint per paused run. Coverage sits directly under
// the footer and above the hints because it is about the table, and because the
// hints are unbounded: a reader with forty abandoned runs would otherwise scroll
// past forty sentences to learn how many rows they were owed. The
// column style mirrors the end-of-run ledger table so the two read as one tool.
// A snapshot-less row keeps the same column widths with "-" in place of the
// values it cannot know yet, and counts toward the run count (its cost so far
// is zero by definition, so the total stays honest). STATUS takes the same "-"
// for the one directory that has no status either (see statusCell).
//
// The header says STATUS and not VERDICT since ADR 0023 §2.6. The whole
// diagnosis behind that ADR is that liveness and verdict were being conflated,
// and leaving PLANNING/PAUSED/ABANDONED under a header that says VERDICT would
// preserve the conflation in the one place a user reads it. ADR 0015 already
// recorded this column as not a contract, so the rename costs nothing the
// content change did not already cost.
func printRuns(w io.Writer, rows []runSummary, coverage string) {
	fmt.Fprintf(w, "%-17s %-24s %6s %10s %-19s  %s\n", "RUN", "GRAPH", "NODES", "COST(USD)", "TOKENS(I/C/O/R)", "STATUS")
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 90))

	var total float64
	var anyUnknown bool
	for _, row := range rows {
		total += row.costUSD
		anyUnknown = anyUnknown || row.costUnknown
		graphName, nodes, cost, tokens := row.graphName, "-", "-", "-"
		if row.hasSnapshot || row.hasAccounting {
			nodes = strconv.Itoa(row.nodeCount)
			if !row.hasSnapshot {
				nodes = "-"
			}
			cost = fmt.Sprintf("%.4f", row.costUSD)
			if row.costUnknown {
				cost = "unknown"
			}
			if row.usage != (runner.TokenUsage{}) {
				tokens = fmt.Sprintf("%d/%d/%d/%d", row.usage.InputTokens, row.usage.CachedInputTokens, row.usage.OutputTokens, row.usage.ReasoningOutputTokens)
			}
		} else {
			graphName = "-"
		}
		if !row.hasSnapshot {
			graphName = "-"
		}
		fmt.Fprintf(w, "%-17s %-24s %6s %10s %-19s  %s\n",
			row.runID,
			graphName,
			nodes,
			cost,
			tokens,
			statusCell(row),
		)
	}
	fmt.Fprintf(w, "%s\n", strings.Repeat("-", 90))
	if anyUnknown {
		fmt.Fprintf(w, "%d run(s), TOTAL COST: %s\n", len(rows), formatCost(total, true))
	} else {
		fmt.Fprintf(w, "%d run(s), TOTAL COST: $%.4f\n", len(rows), total)
	}
	fmt.Fprintln(w, coverage)

	// The per-row hints, under the table rather than interleaved between rows:
	// ABANDONED and PAUSED are the two rows a reader cannot act on without
	// being told how, and each hint is a sentence, not a column. Keeping the
	// table itself uniform is also what lets a human — or the `awk`-shaped
	// script ADR 0015 declines to promise anything to — keep reading it as a
	// table.
	//
	// Gated on `spoken` for the same reason statusCell is: a row whose STATUS
	// cell says "-" must not be followed by a sentence that names a status. A
	// truncated stream carrying only a `run_finished{paused}` — a close with no
	// open before it, which runstatus reads as damage rather than a leg — is
	// exactly that row, and it would otherwise print "-" in the table and
	// "run X is PAUSED" beneath it: one directory, two answers, from the one
	// surface W1 just made consistent.
	for _, row := range rows {
		if !row.spoken {
			continue
		}
		switch row.status {
		case runstatus.Abandoned:
			fmt.Fprintf(w, "\n%s\n", runstatus.Hint(row.runID, row.hasSnapshot))
		case runstatus.Paused:
			fmt.Fprintf(w, "\n%s\n", runstatus.PausedHint(row.runID))
		}
	}
}
