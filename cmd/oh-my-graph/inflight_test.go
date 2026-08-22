package main

import (
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// "How many graphs are running?" used to be answerable only as
// `oh-my-graph runs list | grep -c RUNNING` reading 0 — an inference from
// ABSENCE, indistinguishable from a table that failed to render, a mis-filter,
// or a derivation that broke. These tests pin the answer being SAID.
//
// Every assertion here states presence, including the zero case: not one of
// them may be satisfiable by a line failing to appear, because a test of that
// shape is the very defect under repair.

// TestListRuns_SummaryStatesZeroInFlightWhenNothingIsRunning is the hardest
// case. One settled run, nothing alive — and the summary must still be on
// screen, still carrying the in-flight clause, and that clause must say zero.
func TestListRuns_SummaryStatesZeroInFlightWhenNothingIsRunning(t *testing.T) {
	isolateRunHome(t)
	completedRun(t, "run-done",
		`{"name":"done","nodes":[{"id":"a","prompt":"a"}]}`,
		map[string]runner.NodeOutcome{
			"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.10},
		})

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	summary := lineContaining(t, out.String(), "run(s) shown")
	if !strings.Contains(summary, "0 in flight") {
		t.Errorf("with nothing running the summary must SAY so, not leave it to be read off an empty STATUS column: %q", summary)
	}
	// The other counts stay on the same line: one read, not two.
	if !strings.Contains(summary, "1 of 1 run(s) shown") || !strings.Contains(summary, "0 skipped") {
		t.Errorf("the in-flight clause must extend the coverage line, not replace it: %q", summary)
	}
	if warn.Len() != 0 {
		t.Errorf("nothing was skipped, so the warning stream must stay empty, got %q", warn.String())
	}
}

// TestListRuns_SummaryNamesHowManyRunsAreInFlight is the other side: two live
// legs, and the number in the sentence is 2.
func TestListRuns_SummaryNamesHowManyRunsAreInFlight(t *testing.T) {
	root := t.TempDir()
	liveLegLock(t, openLegRun(t, root, "run-live-a", true))
	liveLegLock(t, openLegRun(t, root, "run-live-b", true))

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	summary := lineContaining(t, out.String(), "run(s) shown")
	if !strings.Contains(summary, "2 in flight") {
		t.Errorf("two live runs must be counted in the summary: %q", summary)
	}
}

// TestListRuns_InFlightCountIsTheDerivedVerdictNotTheRowCount is the mixed
// corpus: one live run, one settled, one abandoned, two unreadable. The
// in-flight number must be the one runstatus derives — not how many rows were
// printed, not how many directories were found, and not the live ones plus the
// dead one.
func TestListRuns_InFlightCountIsTheDerivedVerdictNotTheRowCount(t *testing.T) {
	isolateRunHome(t)
	root := runsRoot()

	liveLegLock(t, openLegRun(t, root, "run-live", true))
	deadLegLock(t, openLegRun(t, root, "run-dead", true))
	completedRun(t, "run-done",
		`{"name":"done","nodes":[{"id":"a","prompt":"a"}]}`,
		map[string]runner.NodeOutcome{
			"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.10},
		})
	staleSnapshotRun(t, root, "run-stale-a")
	staleSnapshotRun(t, root, "run-stale-b")

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()
	summary := lineContaining(t, got, "run(s) shown")

	// Three rows, five directories, one alive.
	for _, want := range []string{"3 of 5 run(s) shown", "1 in flight", "2 skipped"} {
		if !strings.Contains(summary, want) {
			t.Errorf("the summary is missing %q: %q", want, summary)
		}
	}
	// The three numbers a wrong implementation would produce instead.
	for _, wrong := range []string{"3 in flight", "5 in flight", "2 in flight"} {
		if strings.Contains(summary, wrong) {
			t.Errorf("the in-flight count is runstatus's verdict, not a row/directory/liveness-ish count (%q): %q", wrong, summary)
		}
	}
	// The abandoned run is named beside the live count, never inside it: it
	// holds a worktree and a lock nobody is using, which is worth a word.
	if !strings.Contains(summary, "1 abandoned") {
		t.Errorf("an abandoned run must be named in the same sentence: %q", summary)
	}
	// And the table itself is untouched by the new clause.
	if !strings.Contains(got, "run-live") || !strings.Contains(got, "run-dead") || !strings.Contains(got, "run-done") {
		t.Errorf("every readable run must still have its row:\n%s", got)
	}
}

// TestListRuns_SummarySaysNothingAboutAbandonedWhenThereAreNone keeps the
// sentence honest in the ordinary case: the words that appear always mean
// something is there.
func TestListRuns_SummarySaysNothingAboutAbandonedWhenThereAreNone(t *testing.T) {
	root := t.TempDir()
	liveLegLock(t, openLegRun(t, root, "run-live", true))

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	summary := lineContaining(t, out.String(), "run(s) shown")
	if !strings.Contains(summary, "1 in flight") {
		t.Fatalf("the live run must be counted: %q", summary)
	}
	if strings.Contains(summary, "abandoned") {
		t.Errorf("with no abandoned run the summary must not mention one: %q", summary)
	}
}

// TestListRuns_TheAnswerIsOneLine is the shape constraint, and it is the whole
// reason the clause extends the coverage line instead of following it: a second
// line can be scrolled past, cropped by a pager, or emitted only under some
// condition — the exact failure this change exists to kill.
func TestListRuns_TheAnswerIsOneLine(t *testing.T) {
	root := t.TempDir()
	liveLegLock(t, openLegRun(t, root, "run-live", true))
	deadLegLock(t, openLegRun(t, root, "run-dead", true))

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()
	summary := lineContaining(t, got, "run(s) shown")
	if !strings.Contains(summary, "1 in flight, 1 abandoned") {
		t.Fatalf("both counts belong to one sentence on one line: %q", summary)
	}
	if n := strings.Count(got, "in flight"); n != 1 {
		t.Errorf("the corpus answer is said once, got %d occurrences:\n%s", n, got)
	}
}
