package runstatus

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// A run directory this binary cannot READ is the one directory that gets no
// status at all: Gather returns an error rather than Facts, and the surface
// decides what that means. `runs list` drops the row; `show` keeps its row and
// names the damage; `watch` tails the stream anyway; the dashboard renders an
// `unknown` card. Those answers are deliberate and stay (each surface knows
// what it can still show), but the CLASSIFICATION of the damage — what kind it
// is, and how a reader is told how much of it there was — must not be composed
// four times, for the reason this package exists at all.
//
// So the composition lives here, beside the status rule, in two shapes and no
// more. For a surface that WALKS a corpus there is Skipped: one value it
// accumulates, holding what was skipped, why, and the one line that reports it.
// For a surface that was handed a SINGLE run id — `show`, `watch` — there is
// Unreadable: the one sentence about one directory, which Skipped's own detail
// lines are built from, so the two can never word the same damage differently.
// The wording lives here too, exactly as Hint and PausedHint do.
//
// WHY THE LINE IS ALWAYS PRINTED, even when nothing was skipped: a reader must
// be able to tell "nothing was hidden from me" apart from "64 of 325 runs are
// shown". A summary that appears only when there is something to report leaves
// those two cases rendering identically, and the second one is the case where
// silence is a lie — 261 directories exist, and the table names none of them.
//
// The same argument is why the line now also states how many runs are IN
// FLIGHT, and states it at zero: "no run is running" and "the status column is
// broken" are the two cases an empty RUNNING column renders identically, and
// only the first is the one the operator concluded. The count itself is
// InFlightCount over statuses the caller already derived (inflight.go); nothing
// about what makes a run in flight is decided or restated here.

// SkipReason is why a run directory could not be read, at the coarseness the
// error can actually PROVE. There are two values and not the seven distinct
// failure sites the readers have, because only one of those sites returns a
// typed error: runstate.Load's schema refusal. The rest — a corrupt snapshot, a
// stream this binary will not read, graph bytes that will not parse — arrive as
// wrapped fmt errors with no discriminator on them, and telling them apart
// would mean matching their message text, which is a classification that breaks
// the next time a reader rewords itself. Two provable buckets and a per-run
// detail line that carries the reader's own sentence verbatim is the honest
// split; a third bucket can be added the day the site that would fill it grows
// an error type.
//
// The zero value is deliberately NOT one of them, following Status: nothing
// returns it, so an unclassified reason cannot masquerade as a real answer.
type SkipReason int

const (
	// SkipUnreadable is damage: a snapshot that will not decode, a stream this
	// binary refuses, graph bytes that will not parse. The bytes are wrong.
	SkipUnreadable SkipReason = iota + 1
	// SkipIncompatible is not damage: the files are intact and were written by
	// a build with a different snapshot schema. This is the whole of the
	// observed corpus — 261 of 325 directories on the measuring machine, every
	// one of them a version-2 snapshot under a version-3 build.
	SkipIncompatible
)

// String is the phrase the summary line puts in parentheses beside a count. It
// is a noun phrase and not a sentence, because it is rendered mid-line after a
// number.
func (r SkipReason) String() string {
	switch r {
	case SkipUnreadable:
		return "unreadable run files"
	case SkipIncompatible:
		return "written by an incompatible snapshot schema"
	default:
		return "unclassified"
	}
}

// ClassifySkip buckets an error returned by Gather — or by any reader a surface
// runs over the same directory — into the reason a reader is told about. It is
// total: anything it cannot prove incompatible is reported as damage, which is
// the safe direction (damage overstated costs a reader one look at
// --show-skipped; damage understated tells them intact files are fine when they
// are not).
func ClassifySkip(err error) SkipReason {
	var mismatch *runstate.SchemaMismatchError
	if errors.As(err, &mismatch) {
		return SkipIncompatible
	}
	return SkipUnreadable
}

// SkippedRun is one directory that was left out, with the reader's own error
// kept whole so the detail line can quote it rather than paraphrase it.
type SkippedRun struct {
	RunID  string
	Reason SkipReason
	Err    error
}

// Skipped accumulates the directories a corpus walk could not read. The zero
// value is ready to use and means nothing was skipped.
type Skipped struct {
	runs []SkippedRun
}

// Add records one unreadable directory, classifying it on the way in.
func (s *Skipped) Add(runID string, err error) {
	s.runs = append(s.runs, SkippedRun{RunID: runID, Reason: ClassifySkip(err), Err: err})
}

// Len is how many directories were skipped.
func (s *Skipped) Len() int { return len(s.runs) }

// Runs is what was skipped, in the order it was added.
func (s *Skipped) Runs() []SkippedRun { return s.runs }

// Line is the ONE line every surface prints beside its listing, whether or not
// anything was skipped. It states how many runs are shown out of how many were
// found, HOW MANY OF THEM ARE IN FLIGHT, and how many were skipped and under
// which reason — counts only, no run ids, no prose about any individual run —
// and it names the flag that prints the rest. Naming a flag from here follows
// Recovery, which names `resume --retry-failed` for the same reason: the reader
// needs the command, and there is one place the wording can be kept consistent
// across surfaces.
//
// listed is the number of rows the caller actually rendered; the total is
// listed plus the skipped count, so the two numbers a reader compares always
// come from the same walk.
//
// shown is the derived Status of each of those rendered rows, and the in-flight
// clause is rendered from it by InFlightClause — the one wording, shared with
// `show` and `watch`. It is a REQUIRED parameter rather than a variadic
// convenience precisely because "0 in flight" must never be produced by a
// caller that forgot to pass anything: nil is an explicit statement that
// nothing was shown, which is the truth on the all-skipped path.
//
// The clause sits right after the shown/found pair and BEFORE the skipped
// counts, so it cannot end up behind the per-reason breakdown and the trailing
// `--show-skipped` hint, which are the parts of this line that grow.
//
// It counts what this walk could READ. A directory whose bytes were unreadable
// is in the skipped count on the same line and in no other, so a reader who is
// told "0 in flight; 3 skipped" can see exactly how much of the corpus the
// first number covers — which is the same coverage contract the shown/found
// pair has always carried.
func (s *Skipped) Line(listed int, shown []Status, flag string) string {
	total := listed + len(s.runs)
	live := InFlightClause(shown...)
	if len(s.runs) == 0 {
		return fmt.Sprintf("%d of %d run(s) shown; %s; 0 skipped.", listed, total, live)
	}
	return fmt.Sprintf("%d of %d run(s) shown; %s; %d skipped (%s) — pass %s to name them.",
		listed, total, live, len(s.runs), s.byReason(), flag)
}

// byReason renders the per-reason counts in a fixed order, so two runs of the
// same command over the same directories produce the same line.
func (s *Skipped) byReason() string {
	counts := map[SkipReason]int{}
	for _, run := range s.runs {
		counts[run.Reason]++
	}
	reasons := make([]SkipReason, 0, len(counts))
	for reason := range counts {
		reasons = append(reasons, reason)
	}
	sort.Slice(reasons, func(i, j int) bool { return reasons[i] < reasons[j] })

	parts := make([]string, 0, len(reasons))
	for _, reason := range reasons {
		parts = append(parts, fmt.Sprintf("%d %s", counts[reason], reason))
	}
	return strings.Join(parts, ", ")
}

// Unreadable is the ONE sentence any surface says about a SINGLE run directory
// it could not read: which run, which of the two provable classes the damage is
// in, and the reader's own error quoted whole rather than paraphrased. Every
// human surface that says anything at all about such a directory says exactly
// this — `runs list --show-skipped` once per skipped row (Details below is
// literally this function in a loop), `show` above the table it can still
// print, `watch` in place of the status line it cannot print — so one directory
// reads the same wherever it is named, and no surface classifies for itself.
//
// It deliberately states no CONSEQUENCE, because the consequence is the one
// thing the surfaces legitimately disagree about: the row is dropped, the table
// is printed anyway, the tail runs on regardless. Each surface's own output
// already shows which, and a sentence that claimed one of them would be a lie
// on the other two — "skipping run …", which this replaces, was exactly that
// lie the moment a second surface printed it.
//
// "in full" is load-bearing: on `show` the snapshot DID load (the table below
// the warning is real) and only the status could not be derived, while on
// `runs list` nothing of the directory survived. Both are cases of not reading
// it in full; neither is misdescribed.
func Unreadable(runID string, err error) string {
	return fmt.Sprintf("WARNING: run %q could not be read in full (%s): %v", runID, ClassifySkip(err), err)
}

// Details is one line per skipped directory — the output that used to be
// unconditional, now what the flag Line advertises turns back on. It is
// Unreadable per run and nothing else, so the detail a corpus walk prints and
// the sentence a single-run surface prints cannot drift apart.
func (s *Skipped) Details() []string {
	lines := make([]string, 0, len(s.runs))
	for _, run := range s.runs {
		lines = append(lines, Unreadable(run.RunID, run.Err))
	}
	return lines
}
