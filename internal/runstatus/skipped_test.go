package runstatus

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// TestClassifySkip_SchemaMismatchIsIncompatibleEvenWrapped pins the one bucket
// the error can actually prove, through a wrap: summarizeRun and Gather both
// return the reader's error further up, sometimes inside a fmt wrapper, and a
// classification that only matched the bare value would silently report the
// whole observed corpus — 261 version-2 snapshots — as damage.
func TestClassifySkip_SchemaMismatchIsIncompatibleEvenWrapped(t *testing.T) {
	mismatch := &runstate.SchemaMismatchError{Path: "/runs/x/state.json", Found: 2, Want: 3}

	if got := ClassifySkip(mismatch); got != SkipIncompatible {
		t.Errorf("ClassifySkip(bare mismatch) = %v, want SkipIncompatible", got)
	}
	if got := ClassifySkip(fmt.Errorf("reconstruct graph: %w", mismatch)); got != SkipIncompatible {
		t.Errorf("ClassifySkip(wrapped mismatch) = %v, want SkipIncompatible", got)
	}
}

// TestClassifySkip_AnythingUnprovableIsDamage keeps the fallback pointed the
// safe way: an error this package cannot prove to be a version difference is
// reported as damage, never quietly excused as one.
func TestClassifySkip_AnythingUnprovableIsDamage(t *testing.T) {
	for _, err := range []error{
		errors.New(`decode snapshot "/runs/x/state.json": invalid character 'n'`),
		errors.New(`event stream "/runs/x/events.jsonl": schema 99 is newer than this binary understands (max 1)`),
		errors.New("reconstruct graph: node id must not be empty"),
	} {
		if got := ClassifySkip(err); got != SkipUnreadable {
			t.Errorf("ClassifySkip(%v) = %v, want SkipUnreadable", err, got)
		}
	}
}

// TestSkipped_LineIsPresentAndAffirmativeWhenNothingWasSkipped is the whole
// point of the summary being unconditional: "nothing was hidden from me" needs
// its own words on screen, or it is indistinguishable from a table that dropped
// four fifths of the corpus without saying so.
func TestSkipped_LineIsPresentAndAffirmativeWhenNothingWasSkipped(t *testing.T) {
	var skipped Skipped

	got := skipped.Line(3, "--show-skipped")
	if !strings.Contains(got, "3 of 3 run(s) shown") || !strings.Contains(got, "0 skipped") {
		t.Errorf("Line() = %q, want it to state 3 of 3 shown and 0 skipped", got)
	}
	// With nothing to reveal, the flag would be an instruction to nowhere.
	if strings.Contains(got, "--show-skipped") {
		t.Errorf("Line() = %q, must not advertise the detail flag when there is no detail", got)
	}
}

// TestSkipped_LineCountsEveryReason is the constraint stated as an assertion:
// the default line must carry both halves of "64 of 325 are shown", plus the
// per-reason counts, plus the flag that reveals the rest — and no run ids,
// because a count line that grows with the corpus is the noise this replaces.
func TestSkipped_LineCountsEveryReason(t *testing.T) {
	var skipped Skipped
	skipped.Add("run-old-a", &runstate.SchemaMismatchError{Path: "a/state.json", Found: 2, Want: 3})
	skipped.Add("run-old-b", &runstate.SchemaMismatchError{Path: "b/state.json", Found: 2, Want: 3})
	skipped.Add("run-broken", errors.New(`decode snapshot "c/state.json": invalid character 'n'`))

	if skipped.Len() != 3 {
		t.Fatalf("Len() = %d, want 3", skipped.Len())
	}

	got := skipped.Line(64, "--show-skipped")
	for _, want := range []string{
		"64 of 67 run(s) shown",
		"3 skipped",
		"1 unreadable run files",
		"2 written by an incompatible snapshot schema",
		"pass --show-skipped to name them",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("Line() = %q, missing %q", got, want)
		}
	}
	if strings.Contains(got, "run-old-a") || strings.Contains(got, "run-broken") {
		t.Errorf("Line() = %q, must name no individual run", got)
	}
	if lines := strings.Count(strings.TrimSuffix(got, "\n"), "\n"); lines != 0 {
		t.Errorf("Line() = %q, want exactly one line", got)
	}
}

// TestSkipped_LineOrdersReasonsDeterministically guards the map walk inside
// byReason: two identical corpora must render one identical line, or a reader
// diffing two invocations sees a change that is not one.
func TestSkipped_LineOrdersReasonsDeterministically(t *testing.T) {
	build := func() *Skipped {
		s := &Skipped{}
		s.Add("a", errors.New("decode snapshot"))
		s.Add("b", &runstate.SchemaMismatchError{Path: "b/state.json", Found: 2, Want: 3})
		s.Add("c", errors.New("read event stream"))
		s.Add("d", &runstate.SchemaMismatchError{Path: "d/state.json", Found: 1, Want: 3})
		return s
	}
	want := build().Line(1, "--show-skipped")
	for i := 0; i < 20; i++ {
		if got := build().Line(1, "--show-skipped"); got != want {
			t.Fatalf("Line() is not deterministic:\n  %q\n  %q", want, got)
		}
	}
}

// TestSkipped_DetailsNameEveryRunAndQuoteItsReader is the detail half: the flag
// must produce the reader's own sentence, not a paraphrase, so the operator who
// asks why still gets the exact schema versions the loader named.
func TestSkipped_DetailsNameEveryRunAndQuoteItsReader(t *testing.T) {
	var skipped Skipped
	skipped.Add("run-old", &runstate.SchemaMismatchError{Path: "/runs/run-old/state.json", Found: 2, Want: 3})
	skipped.Add("run-broken", errors.New(`decode snapshot "/runs/run-broken/state.json": invalid character 'n'`))

	details := skipped.Details()
	if len(details) != 2 {
		t.Fatalf("Details() returned %d lines, want one per skipped run", len(details))
	}
	if !strings.Contains(details[0], `skipping run "run-old"`) ||
		!strings.Contains(details[0], "has schema version 2, but this build understands version 3") {
		t.Errorf("details[0] = %q, want it to name the run and quote runstate.Load's own sentence", details[0])
	}
	if !strings.Contains(details[1], `skipping run "run-broken"`) ||
		!strings.Contains(details[1], "invalid character") {
		t.Errorf("details[1] = %q, want it to name the run and quote the decode failure", details[1])
	}
}
