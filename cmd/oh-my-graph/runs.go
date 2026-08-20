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

// runsListFlags is `runs list`'s one flag. It is a FlagSet rather than a hand
// rolled argv check so that `runs --help` and `runs list --help` answer with the
// flag's own description (argslot.go's usageRequest prints it), and so that
// usage_test.go can hold the synopsis to what the parser really accepts.
type runsListFlags struct {
	set *flag.FlagSet
	// verbose names every skipped run instead of counting the collapsible ones.
	// It is the escape hatch the collapse owes its reader: the default output
	// says how many runs are missing and why, and this says which.
	verbose bool
}

func newRunsListFlags() *runsListFlags {
	f := &runsListFlags{set: flag.NewFlagSet("runs list", flag.ContinueOnError)}
	f.set.BoolVar(&f.verbose, "verbose", false, "name every skipped run instead of counting them by reason")
	return f
}

// parse reads `list`'s own argv — the tokens after the subcommand name. The help
// token is intercepted before flag.Parse for the reason #200 records: Go's flag
// package answers `--help` with flag.ErrHelp, which would travel as a failure
// and exit 1 at the one moment the flag list is what the user asked for.
func (f *runsListFlags) parse(args []string) error {
	if req := helpRequest(args, "runs", f.set); req != nil {
		return req
	}
	if err := f.set.Parse(args); err != nil {
		return err
	}
	if f.set.NArg() > 0 {
		return fmt.Errorf("runs list: unexpected argument %q (usage: oh-my-graph runs list [--verbose])", f.set.Arg(0))
	}
	return nil
}

// runRuns is the `runs` subcommand group. Its only action today is `list`;
// a group (rather than a bare `list` top-level command) keeps room for later
// run-directory queries (`runs show`, `runs clean`) without another top-level
// name.
func runRuns(args []string) error {
	// This slot holds a subcommand name rather than a value, so an unknown
	// dash-prefixed token keeps its existing `unknown subcommand` answer, which
	// already names the alternative. Only the help request is intercepted
	// (argslot.go), because that one had no answer at all (#200).
	flags := newRunsListFlags()
	if req := helpRequest(args, "runs", flags.set); req != nil {
		return req
	}
	if len(args) == 0 {
		return fmt.Errorf("runs: missing subcommand (usage: oh-my-graph runs list)")
	}
	switch args[0] {
	case "list":
		if err := flags.parse(args[1:]); err != nil {
			return err
		}
		return listRuns(os.Stdout, os.Stderr, runsRoot(), flags.verbose)
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
//
// The skips are COLLECTED and reported once, at the end, rather than printed as
// they are met (runstatus.SkipReport). On the corpus that motivated this, that
// was 261 copies of one sentence for 59 rows of table; the reason is one fact
// about this build, so it is now stated once with a count. Nothing is hidden:
// the report always says how many of how many directories are missing, verbose
// still names every one of them, and a refusal the report must not collapse
// still prints its own full line by default.
//
// It goes where the 261 lines went — BEFORE the table, on warnW. Before, so the
// count is the first thing read rather than a footnote under sixty rows: the one
// thing this report must never fail to deliver is that something is missing. On
// warnW, because it is a warning and that is where this command's warnings have
// always gone; the same `2>/dev/null` that silenced 261 lines yesterday silences
// one today, and the table on stdout is byte-identical either way.
func listRuns(w, warnW io.Writer, root string, verbose bool) error {
	// A root that does not exist yet reads as no entries at all: "nothing has
	// ever run here" and "nothing here is listable" are the same answer to the
	// user, so both fall through to the one empty-table path below rather than
	// printing the same message from two places.
	entries, err := os.ReadDir(root)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read runs dir %q: %w", root, err)
	}

	var rows []runSummary
	var skipped runstatus.SkipReport
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
	// Both numbers are known before anything is printed, which is what lets the
	// report open with "N of M" and still stand above the table.
	printSkipped(warnW, skipped, len(rows), verbose)

	if len(rows) == 0 {
		// The report above is what keeps this honest, and this is the case that
		// needs it most: "No runs found." with a silent stderr means an empty
		// runs root, and with the report above it means every run there is was
		// skipped. Those are very different answers to the same three words.
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

// printSkipped writes the skip report, or nothing at all when nothing was
// skipped — the silence is the promise that the table above is the whole truth,
// so it must never be spent on an empty summary.
func printSkipped(warnW io.Writer, skipped runstatus.SkipReport, shown int, verbose bool) {
	var lines []string
	if verbose {
		lines = skipped.Detail(shown)
	} else {
		lines = skipped.Summary(shown, "oh-my-graph runs list --verbose")
	}
	for _, line := range lines {
		fmt.Fprintln(warnW, line)
	}
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

// printRuns writes the table: a header, one aligned row per run, and a footer
// with the run count and the cost total across every listed run, and then one
// recovery hint per abandoned run and one resume hint per paused run. The
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
func printRuns(w io.Writer, rows []runSummary) {
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
