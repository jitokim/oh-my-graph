package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// This file tests `runs list`'s skip COLLAPSE (runstatus.SkipReport, wired
// through listRuns/printSkipped in runs.go): per-run warnings fold into one
// honest summary line by default, --verbose still names every one of them, and
// a reason that must not collapse (an unreadable snapshot or stream) keeps its
// own full line either way. runs_test.go already covers the single-skip and
// zero-skip shapes pre-dating the collapse; this file is the multi-run corpus
// the collapse itself exists for.

// writeSchemaMismatchState writes a run directory's state.json with a valid,
// decodable JSON body whose schema field does not match runstate.Schema — the
// ONE error shape that classifies as runstatus.ReasonIncompatibleSnapshot and
// is therefore eligible to collapse. It carries no other fields: Load reads
// the schema and refuses before touching anything else, so this is the whole
// fixture that matters.
func writeSchemaMismatchState(t *testing.T, dir string, found int) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("create run dir: %v", err)
	}
	body := []byte(`{"schema":` + strconv.Itoa(found) + `}`)
	if err := os.WriteFile(filepath.Join(dir, stateFileName), body, 0o644); err != nil {
		t.Fatalf("write schema-mismatch snapshot: %v", err)
	}
}

// writeGoodRun writes a minimal, cleanly-settled one-node run — no schema
// mismatch, no corruption — so a test corpus has a control run that must
// always survive the skip and appear as a row.
func writeGoodRun(t *testing.T, root, runID string) {
	t.Helper()
	dir := filepath.Join(root, runID)
	writeEventFixture(t, dir, runID, []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass, CostUSD: 0.01},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	})
	if err := runstate.Write(filepath.Join(dir, stateFileName), runstate.Snapshot{
		RunID: runID,
		Graph: json.RawMessage(`{"name":"good","nodes":[{"id":"a","prompt":"a"}]}`),
		Nodes: map[string]runstate.NodeRecord{"a": {Verdict: runstate.VerdictPass, CostUSD: 0.01}},
	}); err != nil {
		t.Fatalf("write good run snapshot: %v", err)
	}
}

// countOccurrences is strings.Count spelled at call sites where "how many
// times does this appear" is itself the assertion, not a helper to hide it
// behind.
func countOccurrences(haystack, needle string) int {
	return strings.Count(haystack, needle)
}

// --- the collapse: many identical schema mismatches become one count --------

// TestListRuns_ManySchemaMismatchesCollapseToOneSummaryLine is the corpus the
// whole change exists for: several runs skipped for the identical reason
// collapse to ONE count line in the default output, with no per-run spam, and
// the surviving runs are still all listed.
func TestListRuns_ManySchemaMismatchesCollapseToOneSummaryLine(t *testing.T) {
	root := t.TempDir()
	staleIDs := []string{"run-old-1", "run-old-2", "run-old-3"}
	for _, id := range staleIDs {
		writeSchemaMismatchState(t, filepath.Join(root, id), 2)
	}
	writeGoodRun(t, root, "run-good-1")
	writeGoodRun(t, root, "run-good-2")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	gotOut, gotWarn := out.String(), warn.String()

	// Presence: the collapsed count line, naming exactly 3 of 5.
	if !strings.Contains(gotWarn, "WARNING: 3 of 5 run directories could not be read and are not shown:") {
		t.Fatalf("warn = %q, want the collapsed count line for 3 of 5", gotWarn)
	}
	// Presence: the per-reason breakdown.
	if !strings.Contains(gotWarn, "3 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("warn = %q, want the schema-2 breakdown line", gotWarn)
	}
	// Presence: the offer to see them all under --verbose.
	if !strings.Contains(gotWarn, "`oh-my-graph runs list --verbose` names them one by one") {
		t.Errorf("warn = %q, want the --verbose offer", gotWarn)
	}
	// Absence, paired with the presence checks above: no per-run line for any
	// of the collapsed runs survives into the default output.
	for _, id := range staleIDs {
		if strings.Contains(gotWarn, `WARNING: skipping run "`+id+`"`) {
			t.Errorf("warn = %q, %q must not also get its own per-run line once collapsed", gotWarn, id)
		}
	}
	// Presence: the surviving runs are ALL listed, by id, so a passing test
	// cannot coexist with an empty table.
	for _, id := range []string{"run-good-1", "run-good-2"} {
		if !strings.Contains(gotOut, id) {
			t.Errorf("out = %q, want %q listed as a row", gotOut, id)
		}
	}
	if !strings.Contains(gotOut, "2 run(s), TOTAL COST: $0.0200") {
		t.Errorf("out = %q, want the footer to total exactly the 2 surviving runs", gotOut)
	}
}

// TestListRuns_VerboseNamesEveryRunAndTheCountMatchesTheLines is the honesty
// property, stated as an assertion: under --verbose the opening count and the
// number of per-run "skipping run" lines that follow are the SAME number, and
// every skipped run id and its reason are individually present.
func TestListRuns_VerboseNamesEveryRunAndTheCountMatchesTheLines(t *testing.T) {
	root := t.TempDir()
	staleIDs := []string{"run-old-1", "run-old-2", "run-old-3"}
	for _, id := range staleIDs {
		writeSchemaMismatchState(t, filepath.Join(root, id), 2)
	}
	writeGoodRun(t, root, "run-good-1")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, true); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	gotOut, gotWarn := out.String(), warn.String()

	if !strings.Contains(gotWarn, "WARNING: 3 of 4 run directories could not be read and are not shown:") {
		t.Fatalf("warn = %q, want the count line naming 3 of 4", gotWarn)
	}
	for _, id := range staleIDs {
		if !strings.Contains(gotWarn, `WARNING: skipping run "`+id+`": snapshot`) {
			t.Errorf("warn = %q, want %q individually named under --verbose", gotWarn, id)
		}
		if !strings.Contains(gotWarn, "schema version 2") {
			t.Errorf("warn = %q, want the schema-mismatch reason spelled out", gotWarn)
		}
	}
	// The honesty property itself: the count in the opening line equals the
	// number of per-run detail lines that follow it.
	gotLines := countOccurrences(gotWarn, "WARNING: skipping run ")
	if gotLines != 3 {
		t.Errorf("--verbose printed %d per-run lines, want exactly 3 — the opening count (3 of 4) must equal "+
			"the number of runs actually named, not merely claim it", gotLines)
	}
	// Presence: the surviving run is still listed.
	if !strings.Contains(gotOut, "run-good-1") {
		t.Errorf("out = %q, want run-good-1 listed", gotOut)
	}
}

// TestListRuns_TwoDistinctSchemaPairsReportSeparateCounts is §5's forced
// decision, exercised through listRuns: a corpus spanning two old snapshot
// formats reports two breakdown lines, not one anonymous total, because the
// per-run sentence differs by the schema pair.
func TestListRuns_TwoDistinctSchemaPairsReportSeparateCounts(t *testing.T) {
	root := t.TempDir()
	writeSchemaMismatchState(t, filepath.Join(root, "run-schema1-a"), 1)
	writeSchemaMismatchState(t, filepath.Join(root, "run-schema1-b"), 1)
	writeSchemaMismatchState(t, filepath.Join(root, "run-schema2-a"), 2)
	writeGoodRun(t, root, "run-good")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	gotOut, gotWarn := out.String(), warn.String()

	if !strings.Contains(gotWarn, "WARNING: 3 of 4 run directories could not be read and are not shown:") {
		t.Fatalf("warn = %q, want the count line naming 3 of 4", gotWarn)
	}
	if !strings.Contains(gotWarn, "2 written by snapshot schema 1, which this build (schema 3) does not read") {
		t.Errorf("warn = %q, want a breakdown line for the two schema-1 runs", gotWarn)
	}
	if !strings.Contains(gotWarn, "1 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("warn = %q, want a separate breakdown line for the one schema-2 run", gotWarn)
	}
	if !strings.Contains(gotOut, "run-good") {
		t.Errorf("out = %q, want the surviving run listed", gotOut)
	}
}

// TestListRuns_UnreadableReasonKeepsItsOwnLineAlongsideTheCollapse pins §5's
// other half through the real command: a corrupt (non-schema-mismatch)
// snapshot never collapses, even sitting in a corpus where identical schema
// mismatches DO collapse next to it — folding real damage into a count would
// summarize it into invisibility.
func TestListRuns_UnreadableReasonKeepsItsOwnLineAlongsideTheCollapse(t *testing.T) {
	root := t.TempDir()
	writeSchemaMismatchState(t, filepath.Join(root, "run-old-1"), 2)
	writeSchemaMismatchState(t, filepath.Join(root, "run-old-2"), 2)

	corruptDir := filepath.Join(root, "run-corrupt")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatalf("create corrupt run dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, stateFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}
	writeGoodRun(t, root, "run-good")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	gotOut, gotWarn := out.String(), warn.String()

	if !strings.Contains(gotWarn, "WARNING: 3 of 4 run directories could not be read and are not shown:") {
		t.Fatalf("warn = %q, want the count line naming 3 of 4", gotWarn)
	}
	if !strings.Contains(gotWarn, "2 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("warn = %q, want the collapsed schema-2 breakdown line", gotWarn)
	}
	// Presence: the corrupt run's own full, unindented line survives.
	if !strings.Contains(gotWarn, `WARNING: skipping run "run-corrupt":`) {
		t.Errorf("warn = %q, want the corrupt run's own per-run line to survive the collapse", gotWarn)
	}
	// Exactly one "skipping run" line exists (the corrupt one) — the two
	// schema mismatches must not also produce theirs.
	if got := countOccurrences(gotWarn, "WARNING: skipping run "); got != 1 {
		t.Errorf("warn has %d per-run lines, want exactly 1 (only the uncollapsible corrupt run): %q", got, gotWarn)
	}
	if !strings.Contains(gotOut, "run-good") {
		t.Errorf("out = %q, want the surviving run listed", gotOut)
	}
}

// --- edge cases -----------------------------------------------------------

// TestListRuns_EveryRunSkippedStillReportsNoRunsFoundWithASummary is the
// corpus where every run is skipped: the table says "No runs found." (there
// is nothing to list) and the summary above it still reports the truth — the
// two must not be conflated into a silent, empty answer.
func TestListRuns_EveryRunSkippedStillReportsNoRunsFoundWithASummary(t *testing.T) {
	root := t.TempDir()
	writeSchemaMismatchState(t, filepath.Join(root, "run-old-1"), 2)
	writeSchemaMismatchState(t, filepath.Join(root, "run-old-2"), 2)

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if !strings.Contains(out.String(), "No runs found.") {
		t.Errorf("out = %q, want the no-runs message when every run was skipped", out.String())
	}
	gotWarn := warn.String()
	if !strings.Contains(gotWarn, "WARNING: 2 of 2 run directories could not be read and are not shown:") {
		t.Errorf("warn = %q, want the count line naming 2 of 2 even though the table is empty", gotWarn)
	}
	if !strings.Contains(gotWarn, "2 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("warn = %q, want the breakdown line", gotWarn)
	}
}

// TestListRuns_ZeroSkipsPrintsNoSummaryAndStillListsEveryRun is the paired
// absence/presence case CONTRIBUTING requires: on a clean corpus, no summary
// line is printed AND the runs themselves are all listed by id and counted —
// so a test that only checked "no WARNING" could never pass by accident on an
// empty or crashed listing.
func TestListRuns_ZeroSkipsPrintsNoSummaryAndStillListsEveryRun(t *testing.T) {
	root := t.TempDir()
	writeGoodRun(t, root, "run-good-1")
	writeGoodRun(t, root, "run-good-2")
	writeGoodRun(t, root, "run-good-3")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("warn = %q, want no summary at all when nothing was skipped", warn.String())
	}
	gotOut := out.String()
	for _, id := range []string{"run-good-1", "run-good-2", "run-good-3"} {
		if !strings.Contains(gotOut, id) {
			t.Errorf("out = %q, want %q listed", gotOut, id)
		}
	}
	if !strings.Contains(gotOut, "3 run(s), TOTAL COST: $0.0300") {
		t.Errorf("out = %q, want the footer to count and total all 3 runs", gotOut)
	}
}

// TestListRuns_ADirectoryWithNoSnapshotAtAllIsListedNotSkipped is the positive
// control the corrupt/mismatch fixtures need: an ABSENT state.json (a run
// whose first node has not completed, or one still inside its planner call)
// is a fact about the run, not damage, and must never be folded into the skip
// report at all — it is a row, with placeholders, not a warning.
func TestListRuns_ADirectoryWithNoSnapshotAtAllIsListedNotSkipped(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "run-fresh")
	writeEventFixture(t, dir, "run-fresh", []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodeStarted, NodeID: "a"},
	})

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if warn.Len() != 0 {
		t.Errorf("warn = %q, a directory with no snapshot yet must not be reported as skipped", warn.String())
	}
	gotOut := out.String()
	if !strings.Contains(gotOut, "run-fresh") {
		t.Errorf("out = %q, want run-fresh listed as a row despite having no snapshot", gotOut)
	}
	if !strings.Contains(gotOut, "1 run(s)") {
		t.Errorf("out = %q, want the row to count toward the run count", gotOut)
	}
}
