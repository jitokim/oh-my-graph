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
// names the damage; the dashboard renders an `unknown` card. Those three
// answers are deliberate and stay (each surface knows what it can still show),
// but the CLASSIFICATION of the damage — what kind it is, and how a reader is
// told how much of it there was — must not be composed three times, for the
// reason this package exists at all.
//
// So the composition lives here, beside the status rule, and it is ONE value a
// caller accumulates as it walks a corpus of run directories: what was skipped,
// why, and the one line that reports it. The wording lives here too, exactly as
// Hint and PausedHint do.
//
// WHY THE LINE IS ALWAYS PRINTED, even when nothing was skipped: a reader must
// be able to tell "nothing was hidden from me" apart from "64 of 325 runs are
// shown". A summary that appears only when there is something to report leaves
// those two cases rendering identically, and the second one is the case where
// silence is a lie — 261 directories exist, and the table names none of them.

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
// found, how many were skipped and under which reason — counts only, no run
// ids, no prose about any individual run — and it names the flag that prints
// the rest. Naming a flag from here follows Recovery, which names `resume
// --retry-failed` for the same reason: the reader needs the command, and there
// is one place the wording can be kept consistent across surfaces.
//
// listed is the number of rows the caller actually rendered; the total is
// listed plus the skipped count, so the two numbers a reader compares always
// come from the same walk.
func (s *Skipped) Line(listed int, flag string) string {
	total := listed + len(s.runs)
	if len(s.runs) == 0 {
		return fmt.Sprintf("%d of %d run(s) shown; 0 skipped.", listed, total)
	}
	return fmt.Sprintf("%d of %d run(s) shown; %d skipped (%s) — pass %s to name them.",
		listed, total, len(s.runs), s.byReason(), flag)
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

// Details is one line per skipped directory, naming it and quoting the reader's
// own error — the output that used to be unconditional, now what the flag Line
// advertises turns back on. It is the same sentence it always was: a caller
// that prints these prints exactly what it printed before this summary existed.
func (s *Skipped) Details() []string {
	lines := make([]string, 0, len(s.runs))
	for _, run := range s.runs {
		lines = append(lines, fmt.Sprintf("WARNING: skipping run %q: %v", run.RunID, run.Err))
	}
	return lines
}
