package runstatus

import (
	"errors"
	"fmt"

	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// A SKIP is a run directory a reader refused — Gather returned an error, so the
// surface has no facts to render and leaves the run out. This file is the ONE
// place that turns those refusals into something a reader sees, for the reason
// the rest of this package exists: six surfaces ask, and a report composed by
// hand six times is a report that will be composed six different ways.
//
// It exists because the per-run line stopped scaling. Measured on 2026-08-21
// against a real 320-run corpus (notes/measurements/runs-list-noise-2026-08-21.md):
// 261 directories were skipped, ALL of them for one reason — a snapshot written
// by schema 2 that a schema-3 build will not read — and `runs list` printed that
// one sentence 261 times, 80.1% of everything the command emitted. The table it
// exists to print was 59 rows buried under 261 copies of one fact.
//
// The collapse is therefore keyed on the REASON, never on the run, and only a
// reason whose text is genuinely identical for every run it covers may be
// collapsed at all:
//
//   - An incompatible snapshot schema is one fact about a build, not 261 facts
//     about 261 directories. Its per-run sentence carries nothing but a path, so
//     261 of them carry nothing at all. It collapses, grouped by the schema pair
//     (found, want) so a corpus spanning two old formats still reports two
//     numbers rather than one anonymous total.
//
//   - Everything else keeps its per-run line, in full, in the default output. A
//     corrupt snapshot, a stream this build refuses, graph bytes that will not
//     parse: each is a different fact about a different directory, and each had
//     ZERO occurrences in that corpus. Folding them into a count would summarize
//     genuine damage into invisibility next to a pile of merely-old runs, which
//     is the one way this change could do harm.
//
// What is never collapsed is the COUNT. A summary always opens with "N of M",
// so "nothing was hidden from me" and "261 of 320 runs are not shown" can never
// read the same, and a surface that skipped nothing prints nothing at all.
type SkipReason string

const (
	// ReasonIncompatibleSnapshot is a state.json written by another build's
	// snapshot schema. It is not damage — the file is intact and a build of the
	// right vintage reads it — and it is not recoverable here either: this build
	// can neither list nor resume such a run, which is exactly what makes the
	// answer identical for every run that has it.
	ReasonIncompatibleSnapshot SkipReason = "incompatible_snapshot"
	// ReasonUnreadable is every other refusal: a snapshot that will not decode,
	// one that cannot be read at all, a stream carrying a newer event schema,
	// graph bytes that will not parse. They are deliberately ONE code and not a
	// taxonomy — none has ever been seen in the field, and inventing codes for
	// eight dark channels would be guessing at names nobody has read yet. What
	// distinguishes them is their error, which they keep, verbatim, per run.
	ReasonUnreadable SkipReason = "unreadable"
)

// ReasonOf classifies a Gather (or runstate.Load) error into the machine-readable
// code above. It is the ONE classifier: the CLI's summary and the dashboard
// card's `error_code` field both come through here, so the page and the table
// can never disagree about which runs are the same kind of unreadable.
func ReasonOf(err error) SkipReason {
	var mismatch *runstate.SchemaMismatchError
	if errors.As(err, &mismatch) {
		return ReasonIncompatibleSnapshot
	}
	return ReasonUnreadable
}

// Skip is one refused run directory: the id a user would type, and the error the
// reader gave back.
type Skip struct {
	RunID string
	Err   error
}

// Reason is this skip's machine-readable code.
func (s Skip) Reason() SkipReason { return ReasonOf(s.Err) }

// Line is the per-run warning, unchanged from the one `runs list` has always
// printed: it is what --verbose prints, and what a reason that must not be
// collapsed still prints by default, so nothing a reader learned to grep for
// stopped existing.
func (s Skip) Line() string {
	return fmt.Sprintf("WARNING: skipping run %q: %v", s.RunID, s.Err)
}

// SkipReport collects the refusals a single pass over the runs root produced,
// and reports them once at the end instead of once per run. It holds no writer
// and prints nothing itself: a caller takes the lines and puts them on whichever
// channel it already warns on.
type SkipReport struct {
	skips []Skip
}

// Add records one refused run directory. err must be non-nil — a run that was
// read is not a skip.
func (r *SkipReport) Add(runID string, err error) {
	r.skips = append(r.skips, Skip{RunID: runID, Err: err})
}

// Len is how many run directories were skipped.
func (r *SkipReport) Len() int { return len(r.skips) }

// Skips is every refusal in the order it was met, for a caller that wants the
// codes rather than the prose.
func (r *SkipReport) Skips() []Skip { return r.skips }

// schemaPair is the grouping key for ReasonIncompatibleSnapshot: the schema the
// snapshot was written by and the one this build reads. Two old formats in one
// corpus are two lines, not one lump.
type schemaPair struct{ found, want int }

// Summary is the collapsed report: nil when nothing was skipped, and otherwise a
// count of what is missing, a line per collapsible reason, the full per-run line
// for every refusal that must not be collapsed, and — only when something was
// actually collapsed — the command that names the runs behind the counts.
//
// shown is how many runs the caller DID render, so the opening line can say "N
// of M". detailCmd is the caller's own way to spell out the collapsed runs
// (`oh-my-graph runs list --verbose`); a surface that has no such command passes
// "" and the offer is simply omitted rather than pointing at a flag that does
// not exist.
func (r *SkipReport) Summary(shown int, detailCmd string) []string {
	if len(r.skips) == 0 {
		return nil
	}

	lines := []string{fmt.Sprintf("WARNING: %d of %d run directories could not be read and are not shown:",
		len(r.skips), shown+len(r.skips))}

	// One pass, keeping first-seen order for both halves: the collapsible
	// reasons in the order their first run was met, then the ones that keep
	// their own line in the order they were met.
	var order []schemaPair
	counts := map[schemaPair]int{}
	var perRun []string
	for _, skip := range r.skips {
		var mismatch *runstate.SchemaMismatchError
		if !errors.As(skip.Err, &mismatch) {
			perRun = append(perRun, "  "+skip.Line())
			continue
		}
		key := schemaPair{found: mismatch.Found, want: mismatch.Want}
		if counts[key] == 0 {
			order = append(order, key)
		}
		counts[key]++
	}

	for _, key := range order {
		lines = append(lines, fmt.Sprintf(
			"  %d written by snapshot schema %d, which this build (schema %d) does not read — not damaged, "+
				"but this build can neither list nor resume them",
			counts[key], key.found, key.want))
	}
	lines = append(lines, perRun...)
	if len(order) > 0 && detailCmd != "" {
		lines = append(lines, fmt.Sprintf("  `%s` names every skipped run", detailCmd))
	}
	return lines
}

// Detail is every refusal, one per line, in the order they were met — the
// output the collapse replaced, kept behind an explicit request. It opens with
// the same count line as Summary, because a reader who asked for the detail
// still deserves the total without counting lines.
func (r *SkipReport) Detail(shown int) []string {
	if len(r.skips) == 0 {
		return nil
	}
	lines := []string{fmt.Sprintf("WARNING: %d of %d run directories could not be read and are not shown:",
		len(r.skips), shown+len(r.skips))}
	for _, skip := range r.skips {
		lines = append(lines, "  "+skip.Line())
	}
	return lines
}
