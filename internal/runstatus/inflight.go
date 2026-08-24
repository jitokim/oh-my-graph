package runstatus

import "fmt"

// HOW MANY RUNS ARE ALIVE is a question every human surface was answering by
// omission: `runs list` printed a RUNNING row per live run and said nothing at
// all when there were none, so an operator asking "is anything running" read the
// answer out of an ABSENCE — indistinguishable from a table that failed to
// render, a mis-filter, or a broken derivation. The count lives here, beside the
// rule that decides what alive means, for the reason the rest of this package
// exists: four surfaces must not each learn the membership for themselves.
//
// Nothing here decides ANYTHING new. Both counters are predicates over Statuses
// a caller already derived through Probe or Of, so the open-leg-plus-held-lock
// rule stays in Derive (ADR 0015 §4) and is not restated — not here, not in a
// surface's own loop.

// InFlightCount is how many of these runs a process is still working on: the
// count of Status.InFlight over statuses the caller already derived. It is the
// ONE place a corpus-wide "how many are alive" number is produced, so `runs
// list`, `show` and `watch` cannot disagree about it.
//
// It counts Statuses, never directories, and it does not derive them: whether a
// given run is in flight is settled by Derive from an open leg AND a held lock
// (ADR 0015 §4), and re-deciding it from any other fact — a RUNNING word in a
// table cell, a lock file's presence, a row that has no verdict yet — is the
// duplication this exists to prevent.
//
// The empty slice returns 0, which is a real answer and not a missing one: a
// corpus with nothing in it has nothing in flight. The zero Status counts
// toward neither counter, so a directory that has said nothing yet (Spoken is
// false, its Status was never derived) is claimed to be neither alive nor dead.
func InFlightCount(statuses ...Status) int {
	n := 0
	for _, s := range statuses {
		if s.InFlight() {
			n++
		}
	}
	return n
}

// AbandonedCount is how many of these runs hold a leg nobody is working on —
// Status.Abandoned, again over statuses the caller already derived. It exists
// beside InFlightCount and not inside it BECAUSE the two must never be added
// together: an operator asking "is anything running" is answered wrongly by a
// number that folds in dead runs.
func AbandonedCount(statuses ...Status) int {
	n := 0
	for _, s := range statuses {
		if s == Abandoned {
			n++
		}
	}
	return n
}

// InFlightClause is the ONE wording of that answer, so an operator learns one
// sentence rather than one per surface: `runs list` sets it in its coverage
// line, `show` and `watch` after the run's own status word, each over whatever
// statuses that surface holds (a corpus, or the single run it was handed).
//
// IT IS ALWAYS SAID, especially at zero — "0 in flight" is the whole point, and
// a clause that appeared only when something was running would leave "nothing is
// running" and "this line never rendered" reading identically, which is the
// defect it replaces.
//
// ABANDONED is named in the same clause but only when there is one, and never
// inside the in-flight number. Folding it in would answer "is anything running"
// with a yes about a corpse; dropping it entirely would stay silent about a run
// whose process died holding a worktree and a lock — the one state that costs
// the operator something while looking like nothing. So: strictly in-flight
// first, then the dead ones when they exist.
func InFlightClause(statuses ...Status) string {
	live := fmt.Sprintf("%d in flight", InFlightCount(statuses...))
	if dead := AbandonedCount(statuses...); dead > 0 {
		return fmt.Sprintf("%s, %d abandoned", live, dead)
	}
	return live
}
