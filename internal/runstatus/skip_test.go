package runstatus

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// --- ReasonOf: the one classifier ---------------------------------------------

// TestReasonOf_SchemaMismatchIsIncompatibleSnapshot pins the one error type that
// collapses: a *runstate.SchemaMismatchError, wrapped or bare, must classify as
// ReasonIncompatibleSnapshot — the reason the CLI's summary and the dashboard
// card's error_code both key their grouping on.
func TestReasonOf_SchemaMismatchIsIncompatibleSnapshot(t *testing.T) {
	bare := &runstate.SchemaMismatchError{Path: "/runs/x/state.json", Found: 2, Want: 3}
	wrapped := fmt.Errorf("load run %q: %w", "x", bare)

	for name, err := range map[string]error{"bare": bare, "wrapped": wrapped} {
		t.Run(name, func(t *testing.T) {
			if got := ReasonOf(err); got != ReasonIncompatibleSnapshot {
				t.Errorf("ReasonOf(%v) = %q, want %q", err, got, ReasonIncompatibleSnapshot)
			}
		})
	}
}

// TestReasonOf_EverythingElseIsUnreadable pins the deliberately flat side: a
// corrupt snapshot's decode error, a stream this build refuses, unparseable
// graph bytes, and a plain sentinel all classify as the ONE unreadable code —
// never a taxonomy, per skip.go's own reasoning.
func TestReasonOf_EverythingElseIsUnreadable(t *testing.T) {
	for name, err := range map[string]error{
		"decode error":        fmt.Errorf("decode snapshot %q: %w", "/runs/x/state.json", errors.New("unexpected EOF")),
		"newer stream schema": fmt.Errorf("event stream %q: schema %d is newer than this binary understands (max %d)", "/runs/x/events.jsonl", 99, 3),
		"unparseable graph":   fmt.Errorf("reconstruct graph: %w", errors.New("invalid character")),
		"plain sentinel":      errors.New("boom"),
	} {
		t.Run(name, func(t *testing.T) {
			if got := ReasonOf(err); got != ReasonUnreadable {
				t.Errorf("ReasonOf(%v) = %q, want %q", err, got, ReasonUnreadable)
			}
		})
	}
}

// --- Skip.Line and StatusUnavailable: the per-run sentences -------------------

// TestSkipLine_NamesTheRunAndCarriesTheErrorVerbatim pins the exact shape every
// surface's --verbose output and every non-collapsible reason depend on: the
// anchor `WARNING: skipping run` a reader might grep for, the quoted run id,
// and the error's own text.
func TestSkipLine_NamesTheRunAndCarriesTheErrorVerbatim(t *testing.T) {
	s := Skip{RunID: "20260730-204118", Err: errors.New("boom")}
	got := s.Line()
	if !strings.HasPrefix(got, `WARNING: skipping run "20260730-204118": `) {
		t.Errorf("Line() = %q, want the fixed WARNING+run-id prefix", got)
	}
	if !strings.Contains(got, "boom") {
		t.Errorf("Line() = %q, want it to carry the error verbatim", got)
	}
}

// TestStatusUnavailable_CarriesTheErrorVerbatim pins the sentence show and
// watch share for the ONE refusal that is theirs to report — an incompatible
// snapshot on an otherwise-derivable directory.
func TestStatusUnavailable_CarriesTheErrorVerbatim(t *testing.T) {
	err := &runstate.SchemaMismatchError{Path: "/runs/x/state.json", Found: 2, Want: 3}
	got := StatusUnavailable(err)
	if !strings.HasPrefix(got, "WARNING: this run's status could not be derived: ") {
		t.Errorf("StatusUnavailable() = %q, want the fixed prefix", got)
	}
	if !strings.Contains(got, err.Error()) {
		t.Errorf("StatusUnavailable() = %q, want it to carry %q verbatim", got, err.Error())
	}
}

// --- SkipReport: collapse by reason, never by run ------------------------------

// mismatch is a small constructor so the table-driven tests below can build a
// schema-pair fixture in one line.
func mismatch(path string, found, want int) *runstate.SchemaMismatchError {
	return &runstate.SchemaMismatchError{Path: path, Found: found, Want: want}
}

// TestSkipReport_EmptyReportPrintsNothing is the paired absence/presence case
// CONTRIBUTING requires: nil from both Summary and Detail on an empty report,
// asserted alongside nothing else — there is nothing else to pair it with here,
// because the positive half of this property is proven by every other test in
// this file, each of which starts from a non-empty report and gets non-nil
// lines back.
func TestSkipReport_EmptyReportPrintsNothing(t *testing.T) {
	var r SkipReport
	if lines := r.Summary(5, "oh-my-graph runs list --verbose"); lines != nil {
		t.Errorf("Summary() on an empty report = %v, want nil", lines)
	}
	if lines := r.Detail(5); lines != nil {
		t.Errorf("Detail() on an empty report = %v, want nil", lines)
	}
}

// TestSkipReport_Summary_CollapsesManyIdenticalSchemaMismatchesToOneCount is
// the corpus the whole change exists for: N runs skipped for the identical
// reason collapse to one count line, with no per-run line surviving into the
// default output.
func TestSkipReport_Summary_CollapsesManyIdenticalSchemaMismatchesToOneCount(t *testing.T) {
	var r SkipReport
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		r.Add(id, mismatch(fmt.Sprintf("/runs/%s/state.json", id), 2, 3))
	}

	lines := r.Summary(59 /* shown */, "oh-my-graph runs list --verbose")

	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "WARNING: 3 of 62 run directories could not be read and are not shown:") {
		t.Fatalf("Summary() = %q, want the opening count line naming 3 of 62", joined)
	}
	if !strings.Contains(joined, "3 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("Summary() = %q, want the collapsed schema-2 breakdown line", joined)
	}
	if !strings.Contains(joined, "`oh-my-graph runs list --verbose` names them one by one") {
		t.Errorf("Summary() = %q, want the offer to name them under --verbose", joined)
	}
	// The honesty property, stated as an assertion: nothing a collapsed reason
	// covers may still print its own per-run line in the default output.
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		if strings.Contains(joined, fmt.Sprintf("WARNING: skipping run %q", id)) {
			t.Errorf("Summary() still names %q per-run: %q, a collapsed reason must not also spell its members", id, joined)
		}
	}
}

// TestSkipReport_Summary_TwoDistinctSchemaPairsReportSeparateCounts pins §5's
// forced decision: the collapse is keyed on the schema PAIR, not on the reason
// alone, so a corpus spanning two old formats reports two numbers.
func TestSkipReport_Summary_TwoDistinctSchemaPairsReportSeparateCounts(t *testing.T) {
	var r SkipReport
	r.Add("run-a", mismatch("/runs/run-a/state.json", 1, 3))
	r.Add("run-b", mismatch("/runs/run-b/state.json", 1, 3))
	r.Add("run-c", mismatch("/runs/run-c/state.json", 2, 3))

	joined := strings.Join(r.Summary(1, "oh-my-graph runs list --verbose"), "\n")

	if !strings.Contains(joined, "WARNING: 3 of 4 run directories could not be read and are not shown:") {
		t.Fatalf("Summary() = %q, want the opening count line naming 3 of 4", joined)
	}
	if !strings.Contains(joined, "2 written by snapshot schema 1, which this build (schema 3) does not read") {
		t.Errorf("Summary() = %q, want a breakdown line for the two schema-1 runs", joined)
	}
	if !strings.Contains(joined, "1 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("Summary() = %q, want a separate breakdown line for the one schema-2 run", joined)
	}
}

// TestSkipReport_Summary_UnreadableReasonNeverCollapses pins the other half of
// §5: a reason that is not schema mismatch keeps its own full line even when
// several runs share it, because ReasonUnreadable is deliberately not a
// taxonomy and folding distinct damage into one count would summarize it into
// invisibility.
func TestSkipReport_Summary_UnreadableReasonNeverCollapses(t *testing.T) {
	var r SkipReport
	r.Add("run-corrupt-1", errors.New("decode snapshot: unexpected EOF"))
	r.Add("run-corrupt-2", errors.New("decode snapshot: invalid character"))

	lines := r.Summary(0, "oh-my-graph runs list --verbose")
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "WARNING: 2 of 2 run directories could not be read and are not shown:") {
		t.Fatalf("Summary() = %q, want the opening count line naming 2 of 2", joined)
	}
	for _, want := range []string{
		`WARNING: skipping run "run-corrupt-1": decode snapshot: unexpected EOF`,
		`WARNING: skipping run "run-corrupt-2": decode snapshot: invalid character`,
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("Summary() = %q, want the unindented per-run line %q", joined, want)
		}
	}
	// No collapsed reason exists in this corpus, so no offer to name anything
	// "one by one" should appear.
	if strings.Contains(joined, "names them one by one") {
		t.Errorf("Summary() = %q, must not offer --verbose when nothing was collapsed", joined)
	}
}

// TestSkipReport_Summary_MixedReasonsCollapseOnlyTheCollapsible is the
// corpus with more than one distinct skip reason: the schema mismatches fold
// into their breakdown, the unreadable one keeps its full line, and the count
// covers all of them.
func TestSkipReport_Summary_MixedReasonsCollapseOnlyTheCollapsible(t *testing.T) {
	var r SkipReport
	r.Add("run-old-1", mismatch("/runs/run-old-1/state.json", 2, 3))
	r.Add("run-old-2", mismatch("/runs/run-old-2/state.json", 2, 3))
	r.Add("run-corrupt", errors.New("decode snapshot: unexpected EOF"))

	lines := r.Summary(1, "oh-my-graph runs list --verbose")
	joined := strings.Join(lines, "\n")

	if !strings.Contains(joined, "WARNING: 3 of 4 run directories could not be read and are not shown:") {
		t.Fatalf("Summary() = %q, want the opening count line naming 3 of 4", joined)
	}
	if !strings.Contains(joined, "2 written by snapshot schema 2, which this build (schema 3) does not read") {
		t.Errorf("Summary() = %q, want the collapsed schema-2 breakdown line", joined)
	}
	if !strings.Contains(joined, `WARNING: skipping run "run-corrupt": decode snapshot: unexpected EOF`) {
		t.Errorf("Summary() = %q, want the corrupt run's own full line to survive the collapse", joined)
	}
	for _, id := range []string{"run-old-1", "run-old-2"} {
		if strings.Contains(joined, fmt.Sprintf("WARNING: skipping run %q", id)) {
			t.Errorf("Summary() = %q, the collapsed schema-mismatch runs %q must not also print a per-run line", joined, id)
		}
	}
	if !strings.Contains(joined, "names them one by one") {
		t.Errorf("Summary() = %q, want the --verbose offer because something WAS collapsed", joined)
	}
}

// TestSkipReport_Summary_NoDetailCmdOmitsTheOffer pins the surface that has no
// --verbose flag of its own (the dashboard, one day; today it is any caller
// that has no equivalent command): passing "" must drop the offer sentence
// rather than point at a flag that does not exist, while the count and the
// breakdown are unaffected.
func TestSkipReport_Summary_NoDetailCmdOmitsTheOffer(t *testing.T) {
	var r SkipReport
	r.Add("run-old", mismatch("/runs/run-old/state.json", 2, 3))

	lines := r.Summary(0, "")
	joined := strings.Join(lines, "\n")
	if !strings.Contains(joined, "WARNING: 1 of 1 run directories could not be read and are not shown:") {
		t.Fatalf("Summary() = %q, want the count line even with no detailCmd", joined)
	}
	if !strings.Contains(joined, "1 written by snapshot schema 2") {
		t.Errorf("Summary() = %q, want the breakdown line even with no detailCmd", joined)
	}
	if strings.Contains(joined, "names them one by one") {
		t.Errorf("Summary() = %q, must not offer a command it was not given", joined)
	}
}

// --- SkipReport.Detail: the uncollapsed --verbose output -----------------------

// TestSkipReport_Detail_NamesEveryRunWithACountThatMatchesTheLines is the
// honesty property CONTRIBUTING calls for, stated as an assertion: the opening
// count and the number of per-run lines that follow must be the same number.
func TestSkipReport_Detail_NamesEveryRunWithACountThatMatchesTheLines(t *testing.T) {
	var r SkipReport
	ids := []string{"run-a", "run-b", "run-c", "run-d"}
	for _, id := range ids {
		r.Add(id, mismatch("/runs/"+id+"/state.json", 2, 3))
	}

	lines := r.Detail(6)
	if len(lines) == 0 {
		t.Fatal("Detail() returned no lines for a non-empty report")
	}
	if want := "WARNING: 4 of 10 run directories could not be read and are not shown:"; lines[0] != want {
		t.Fatalf("Detail()[0] = %q, want %q", lines[0], want)
	}

	detailLines := lines[1:]
	if len(detailLines) != len(ids) {
		t.Fatalf("Detail() produced %d per-run lines, want exactly %d (one per skipped run) — "+
			"the count in the opening line and the number of detail lines must agree", len(detailLines), len(ids))
	}
	// Every run named, in the order it was added, with its reason.
	for i, id := range ids {
		if !strings.Contains(detailLines[i], fmt.Sprintf("WARNING: skipping run %q", id)) {
			t.Errorf("Detail()[%d] = %q, want it to name run %q", i+1, detailLines[i], id)
		}
		if !strings.Contains(detailLines[i], "schema version 2") {
			t.Errorf("Detail()[%d] = %q, want it to carry the schema-mismatch reason", i+1, detailLines[i])
		}
	}
	// Restated as an arithmetic assertion rather than only a length check, so
	// the property reads as "the count IS the number of lines" rather than an
	// implementation detail of this test's fixture.
	gotCount := 0
	fmt.Sscanf(lines[0], "WARNING: %d of", &gotCount)
	if gotCount != len(detailLines) {
		t.Errorf("opening count = %d, detail lines = %d: --verbose must name exactly as many runs as it counts",
			gotCount, len(detailLines))
	}
}

// TestSkipReport_Detail_NeverCollapsesEvenIdenticalReasons is Detail's own
// contrast with Summary: the exact same corpus that collapses to one
// breakdown line under Summary must still print one line PER RUN under
// Detail — that is the entire reason --verbose exists.
func TestSkipReport_Detail_NeverCollapsesEvenIdenticalReasons(t *testing.T) {
	var r SkipReport
	r.Add("run-a", mismatch("/runs/run-a/state.json", 2, 3))
	r.Add("run-b", mismatch("/runs/run-b/state.json", 2, 3))
	r.Add("run-c", mismatch("/runs/run-c/state.json", 2, 3))

	lines := r.Detail(0)
	if len(lines) != 4 { // 1 count line + 3 per-run lines
		t.Fatalf("Detail() returned %d lines, want 4 (1 count + 3 per-run)", len(lines))
	}
	for _, id := range []string{"run-a", "run-b", "run-c"} {
		if !strings.Contains(strings.Join(lines, "\n"), fmt.Sprintf("WARNING: skipping run %q", id)) {
			t.Errorf("Detail() = %v, want run %q named on its own line", lines, id)
		}
	}
}
