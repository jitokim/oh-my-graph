// runstatus is the one place the rule ADR 0015 §2 derives an abandoned run
// from is written down:
//
//	in flight = an open leg AND a held lock; abandoned = an open leg AND a
//	free lock; anything else is settled or, where the lock cannot answer,
//	today's answer.
//
// It exists because FOUR surfaces ask that question — `runs list`, the
// dashboard card, `serve`'s ResolveRun and `watch` — and a rule composed by
// hand four times is a rule that will be composed four different ways. The two
// facts stay where they belong: runfeed keeps its pure, stdlib-only reading of
// the stream (runfeed.InFlight means exactly "the last leg is open", as it
// always did), runstate keeps the lock it owns (runstate.ProbeLock), and this
// package is only their composition — it spawns nothing, writes nothing, and
// holds no state.
//
// The recovery wording lives here too, for the same reason: ADR 0015 §4
// requires the hint to reach four surfaces, and the residual hazard it warns
// about (an orphaned `claude` still spending after the engine died) is
// mitigated by that wording and by nothing else.
package runstatus

import (
	"fmt"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// Status is how a run reads right now. It is three-valued because "the process
// that was running this is gone" is a different thing to tell an operator than
// either "it is thinking" or "it finished" — the distinction between waiting
// and resuming (ADR 0015, Context).
type Status int

const (
	// Settled means the stream's last leg is closed: the run has a verdict, or
	// is paused at a gate, and the lock is not consulted at all.
	Settled Status = iota
	// InFlight means a leg is open and a process still holds the run's lock —
	// or the lock could not answer, which reads as in flight because that is
	// the pre-ADR-0015 answer and the safe one (a false "abandoned" would
	// authorise a second scheduler over a live run).
	InFlight
	// Abandoned means a leg is open and the lock is affirmatively free: the
	// process that opened that leg is gone, and no event will ever close it.
	// Deliberately not FAIL — a FAIL is a verdict about the work, and the work
	// never got one (ADR 0015 §4).
	Abandoned
)

func (s Status) String() string {
	switch s {
	case InFlight:
		return "in flight"
	case Abandoned:
		return "abandoned"
	default:
		return "settled"
	}
}

// Derive is the rule itself, over the two facts and nothing else. Abandoned
// requires TWO affirmative facts, never the absence of one: an open leg in the
// stream, and a LivenessFree that runstate actually established.
//
// There are two ways runstate can establish it, and a reader of an old run
// directory needs to know which one answered, because they are not equally
// strong. A MARKED lock file — one a post-ADR-0015 binary wrote — that nothing
// flocks, on a filesystem whose flock means what §1 assumes, is the primary
// rule. An UNMARKED, pre-flock lock file has no flock to read at all, so it is
// answered by the one question it can answer: its recorded pid naming no
// process (runstate.legacyLiveness). A run whose lock predates the upgrade
// therefore resolves under that second rule, and that is the only reason such a
// run can read abandoned here rather than in flight forever.
//
// Every doubt runstate folds into LivenessUnknown — including an unmarked file
// whose pid still names something — lands in the in-flight arm here, never in
// the abandoned one.
func Derive(openLeg bool, lock runstate.Liveness) Status {
	switch {
	case !openLeg:
		return Settled
	case lock == runstate.LivenessFree:
		return Abandoned
	default:
		return InFlight
	}
}

// Probe answers for a run directory whose leg state the caller already knows —
// the dashboard card, which walks the stream once for per-node state, the leg
// boundaries and the open leg together rather than paying a second read on its
// hot path.
//
// A closed leg is settled without probing anything: the lock says nothing
// about a run that finished, and not opening the file is one less syscall per
// card per tick.
func Probe(runDir string, openLeg bool) Status {
	if !openLeg {
		return Settled
	}
	return Derive(openLeg, runstate.ProbeLock(filepath.Join(runDir, runstate.LockFileName)))
}

// Of is Probe for a caller that has not read the stream yet: it takes the leg
// state from runfeed.InFlight — the contract's own reading, so this package
// never restates it — and composes it with the lock. A missing stream is not
// an error (no legs at all, i.e. settled); a stream this binary refuses to
// read is, and the caller decides what that means, exactly as before.
func Of(runDir string) (Status, error) {
	openLeg, err := runfeed.InFlight(filepath.Join(runDir, runfeed.FileName))
	if err != nil {
		return Settled, err
	}
	return Probe(runDir, openLeg), nil
}

// OrphanWarning is the sentence every surface prints beside an abandoned run,
// and it is the ONLY mitigation ADR 0015 accepts for its largest cost. The lock
// fd is O_CLOEXEC, so a `claude` child does not hold it: a death that takes the
// engine without taking its children (SIGHUP from a closed terminal, kill -9, a
// panic, an OOM kill — the ordinary ways a multi-hour run dies) leaves an
// orphaned subprocess still running and still spending while the run reads
// abandoned. Resuming then runs that node alongside its own orphan. The ADR
// rejects probing for it — that would be a fifth exec seam — so this warning is
// what stands between the operator and a double spend, on all four surfaces.
const OrphanWarning = "a `claude` subprocess started by the dead leg may still be running and spending, so check for one before you spend again"

// Recovery is what an operator can actually do about an abandoned run, and it
// splits on whether the run ever wrote a snapshot.
//
// A run killed before its first node settled has no state.json — the snapshot
// is written only at a node's terminal verdict — and `resume` loads the
// snapshot before it branches, so it fails outright on such a run,
// --retry-failed included. That is not a gap to paper over: with no recorded
// graph there is nothing to resume FROM, and the only honest recovery is to run
// the graph again (ADR 0015 §5).
func Recovery(runID string, hasSnapshot bool) string {
	if hasSnapshot {
		return fmt.Sprintf("continue it with `oh-my-graph resume %s --retry-failed`", runID)
	}
	return "it never wrote a snapshot, so there is nothing to resume from — run the graph again"
}

// Hint is the one-line recovery hint ADR 0015 §4 requires beside every
// abandoned run: on the `runs list` row, in `watch`'s refusal and on the
// dashboard card, whose gate button is one click and spends money. `resume`
// prints OrphanWarning directly instead, because it IS the recovery.
func Hint(runID string, hasSnapshot bool) string {
	return fmt.Sprintf("run %s is ABANDONED — a leg started and never reported an end: %s. WARNING: %s.",
		runID, Recovery(runID, hasSnapshot), OrphanWarning)
}
