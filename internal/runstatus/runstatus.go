// runstatus is the one place a run's USER-VISIBLE STATUS is derived. Since ADR
// 0023 that is one six-valued enumeration —
//
//	PLANNING → RUNNING → { PASS | FAIL | PAUSED | ABANDONED }
//
// — and not, as before, a three-valued liveness answer that each surface then
// combined with a verdict string of its own making. The liveness half is still
// ADR 0015 §2's rule, unchanged and un-duplicated:
//
//	in flight = an open leg AND a held lock; abandoned = an open leg AND a
//	free lock; anything else has settled and the lock is not consulted.
//
// It exists because SIX surfaces ask — `runs list`, the dashboard card,
// `serve`'s ResolveRun, `serve`'s single-run view (via /api/graph), `watch` and
// `show` — and a rule composed by hand six times is a rule that will be composed
// six different ways. It was: a run paused at its approval gate (exit 2,
// resumable, working exactly as designed) printed FAIL in `runs list` and
// painted red on the dashboard, because each surface folded "not passed" into
// FAIL on its own (ADR 0023 §1.2).
//
// The facts stay where they belong: runfeed keeps its pure, stdlib-only reading
// of the stream (runfeed.LastLeg, whose Open field is runfeed.InFlight's own
// answer), runstate keeps the lock and the snapshot it owns, and this package
// is only their composition — it spawns nothing, writes nothing, and holds no
// state. The one dependency the flattening added is internal/graph, a pure
// value-object package with no infra dependencies, and it is confined to Of:
// Derive stays pure by taking the node counts as plain integers, and Probe
// keeps taking facts its caller already has so the dashboard's hot path pays no
// second read (ADR 0023 §7).
//
// The recovery wording lives here too, for the same reason: ADR 0015 §4
// requires the hint to reach every surface a spender is reachable from — the
// `ABANDONED` row, `watch`'s refusal, `resume`'s own stderr, the dashboard card,
// and the run page that card links to, which is where the gate button actually
// is (the fifth surface, recorded in the ADR's dated note) — and the hazard it warns
// about (an orphaned `claude` still spending after the engine died) is
// mitigated by that wording and by nothing else.
package runstatus

import (
	"errors"
	"fmt"
	"io/fs"
	"path/filepath"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// Status is how a run reads right now: ONE value, from a closed set of six.
//
// It is six and not four because two of the terminal values are load-bearing
// and must not collapse into FAIL. ABANDONED, because a FAIL is a verdict about
// the work and the work never got one (ADR 0015 §4). PAUSED, because a run that
// stopped exactly as designed — at a gate, or on the subscription's session
// limit — is resumable, exits 2, and reading it as a failure is the defect ADR
// 0023 §1.2 measures.
//
// The zero value is deliberately NOT one of the six: nothing derives it, so a
// Status that was never derived cannot masquerade as a real answer (it renders
// "UNKNOWN" and is neither Settled nor InFlight).
type Status int

const (
	// Planning means a leg is open, its process is alive, and it is inside its
	// planner call — no graph exists yet. This is the value ADR 0023 adds: for
	// the whole of the longest single wait in the tool, and the first one a new
	// user meets, there was previously no run id, therefore no run directory,
	// therefore nothing on disk for any reader to find (#163).
	Planning Status = iota + 1
	// Running means a leg is open, its process is alive, and the scheduler has
	// it.
	Running
	// Abandoned means a leg is open and the lock is affirmatively free: the
	// process that opened that leg is gone, and no event will ever close it.
	// Deliberately not FAIL — a FAIL is a verdict about the work, and the work
	// never got one (ADR 0015 §4).
	Abandoned
	// Paused means the leg closed on a pause and the run is resumable. It is
	// read off the stream's own run_finished outcome, never off the snapshot's
	// gate block, and that is what makes it cover BOTH pause shapes: a gate
	// pause (ADR 0003) and a session-limit pause (ADR 0009), which has no gate
	// to point at and is the one the dashboard painted red.
	Paused
	// Pass means the leg closed and every node of the graph reached PASS. It is
	// the one value that requires the snapshot, so a settled run without one
	// cannot read it.
	Pass
	// Fail means the leg closed and it did not pass — a settled run with no
	// snapshot at all included. That combination used to be nearly unreachable;
	// since ADR 0023 §3 it is the shape of every REFUSED plan, whose directory
	// holds resume.lock, a two-line events.jsonl, rejected.json and neither
	// graph.json nor state.json. The closed leg and the last outcome decide the
	// status; the missing snapshot decides nothing, and in particular it must
	// not decide whether the row exists.
	Fail
)

// String is the word every surface renders — the `runs list` STATUS column,
// `watch`'s opening line, `show`'s status line, and (lower-cased) the
// dashboard's state token. UNKNOWN is the zero value's word and is not one of
// the six: it means nobody derived this Status, which is a programming error
// rather than a state a run can be in.
func (s Status) String() string {
	switch s {
	case Planning:
		return "PLANNING"
	case Running:
		return "RUNNING"
	case Abandoned:
		return "ABANDONED"
	case Paused:
		return "PAUSED"
	case Pass:
		return "PASS"
	case Fail:
		return "FAIL"
	default:
		return "UNKNOWN"
	}
}

// Settled reports whether the run is over: it has a verdict, or it is paused
// and waiting for a human. Three call sites spelled "not settled" by hand
// before ADR 0023 and would otherwise each have to learn the new membership.
//
// PAUSED is settled here, and that is not a contradiction of "resumable": the
// LEG closed, which is the fact this predicate is about. Nothing is running.
func (s Status) Settled() bool {
	return s == Pass || s == Fail || s == Paused
}

// InFlight reports whether a process is alive and working on this run — the
// predicate PLANNING splits, which is exactly why it exists.
//
// A predicate on only the settled side would be asymmetric with the split this
// enumeration introduces, and the asymmetry has a concrete victim:
// serve.ResolveRun prefers the newest in-flight run for the single-run view,
// and written as `== Running` it would stop preferring a planning run —
// silently withholding the state ADR 0023 exists to make visible, on the very
// surface #163 names (§2.1.1).
func (s Status) InFlight() bool {
	return s == Planning || s == Running
}

// Facts is everything Derive reads — plus AnyLeg, which only Spoken reads —
// and every one of them is AFFIRMATIVE: an open leg, a probed lock, a phase
// field, a run_finished outcome, a snapshot that is there. No status is decided
// by the absence of a file — the snapshot only ever REFINES Pass (ADR 0023
// §2.5).
//
// What is deliberately NOT here is the snapshot's gate.paused_at. PAUSED comes
// off the stream's outcome, which is the only formulation that covers ADR
// 0009's gate-less session-limit pause, and handing the gate block back into
// the derivation is the precise re-entry point for the §1.2 defect.
type Facts struct {
	// OpenLeg is runfeed.Leg.Open — the stream's own answer, never restated
	// here.
	OpenLeg bool
	// AnyLeg is runfeed.Leg.Started: a run_started has been seen at all. It is
	// the ONE field Derive does not read — Spoken does, to answer whether this
	// directory has a status to derive in the first place.
	AnyLeg bool
	// Phase is the latest run_started's phase (runfeed.PhasePlanning or empty).
	// Read only when OpenLeg.
	Phase string
	// LastOutcome is the last run_finished's outcome. Read only when the leg is
	// closed.
	LastOutcome string
	// HasSnapshot says a state.json was read back. Pass is the one value that
	// needs it: claiming PASS from a stream token while the record that proves
	// it is missing is the worse error (ADR 0023 §5).
	HasSnapshot bool
	// CompletedNodes and TotalNodes are len(snap.CompletedNodes()) and the
	// graph's node count — plain integers, so Derive stays pure and this
	// package's graph dependency is confined to Of.
	CompletedNodes int
	TotalNodes     int
}

// Derive is the rule itself, over the facts and nothing else.
//
// Precedence is exactly as written, and it is load-bearing: an open leg answers
// before any snapshot question (mid-run the snapshot holds only the nodes
// completed so far, so a completed==all test would read a healthy run as FAIL),
// a free lock answers before the phase question, and "paused" answers before
// the completed-count.
//
// Abandoned still requires TWO affirmative facts, never the absence of one: an
// open leg in the stream, and a LivenessFree that runstate actually
// established. There are two ways runstate can establish it, and a reader of an
// old run directory needs to know which one answered, because they are not
// equally strong. A MARKED lock file — one a post-ADR-0015 binary wrote — that
// nothing flocks, on a filesystem whose flock means what §1 assumes, is the
// primary rule. An UNMARKED, pre-flock lock file has no flock to read at all,
// so it is answered by the one question it can answer: its recorded pid naming
// no process (runstate.legacyLiveness). A run whose lock predates the upgrade
// therefore resolves under that second rule, and that is the only reason such a
// run can read abandoned here rather than in flight forever.
//
// Every doubt runstate folds into LivenessUnknown — including an unmarked file
// whose pid still names something — lands in the in-flight arm here, never in
// the abandoned one. Adding PLANNING only splits WHICH in-flight arm a run
// lands in, and the split is decided by an affirmative event field, never by
// the absence of node events.
func Derive(f Facts, lock runstate.Liveness) Status {
	switch {
	case f.OpenLeg && lock == runstate.LivenessFree:
		return Abandoned
	case f.OpenLeg && f.Phase == runfeed.PhasePlanning:
		return Planning
	case f.OpenLeg:
		return Running
	case f.LastOutcome == runfeed.OutcomePaused:
		return Paused
	case f.HasSnapshot && f.TotalNodes > 0 && f.CompletedNodes == f.TotalNodes:
		return Pass
	default:
		return Fail
	}
}

// Probe answers for a run directory whose facts the caller already holds — the
// dashboard card, which walks the stream once for per-node state, the leg
// boundaries, the phase and the outcome together, and loads the snapshot once
// for the graph and the cost, rather than paying a second read of either on its
// hot path.
//
// A closed leg is derived without probing anything: the lock says nothing about
// a run that finished, and not opening the file is one less syscall per card
// per tick.
func Probe(runDir string, f Facts) Status {
	if !f.OpenLeg {
		return Derive(f, runstate.LivenessUnknown)
	}
	return Derive(f, runstate.ProbeLock(filepath.Join(runDir, runstate.LockFileName)))
}

// Spoken reports whether the run directory has said ANYTHING about itself yet.
//
// It is not a seventh status and it is not derived from one: ADR 0023 §2.1.1
// names one cell of the table that has NO status — a directory whose stream has
// said nothing at all, which is the window between AcquireLock creating it and
// the first event reaching the file, and is permanent for a directory whose
// stream could never be created. Derive answers Fail there, because Derive is
// total and something has to come out of its default arm, and every surface
// that renders a word must therefore ask this first.
//
// It exists as a shared predicate rather than as a guard each surface writes
// because it WAS one: the dashboard held it and the two CLI surfaces did not,
// so one directory read `pending` on the card and FAIL in the table — surfaces
// disagreeing about the same bytes, which is the condition this package exists
// to end. Each surface still renders it in the vocabulary it already has for
// "not known yet" (the card's `pending`, the table's "-", the omitted word in
// `show`'s header); what they share is the question.
//
// Like every fact here it is AFFIRMATIVE: a run_started that was actually
// written, or a snapshot that is there — a pre-runfeed directory has no stream
// at all and has still spoken. A lone run_finished is neither: a close with no
// open before it is damage rather than a leg, and it leaves the directory as
// silent as it found it.
func Spoken(f Facts) bool {
	return f.AnyLeg || f.HasSnapshot
}

// Gather reads the two files a run directory persists and returns what they
// say, for a caller that needs more than the word — Spoken above is not
// recoverable from a Status, and a surface that renders a status must ask both.
//
// The error contract is the distinction ADR 0023 §2.1.1 turns on — the ABSENCE
// of a file is a fact about the run, while the UNREADABILITY of one is a fact
// about the reader. So a missing stream is not an error (no legs at all) and a
// missing snapshot is not an error (Pass simply becomes unreachable), while a
// stream this binary refuses to read, a corrupt snapshot, or graph bytes that
// will not parse all are: the caller decides what that means, and only that
// second kind may make a row disappear.
func Gather(runDir string) (Facts, error) {
	leg, err := runfeed.LastLeg(filepath.Join(runDir, runfeed.FileName))
	if err != nil {
		return Facts{}, err
	}
	facts := Facts{OpenLeg: leg.Open, AnyLeg: leg.Started, Phase: leg.Phase, LastOutcome: leg.LastOutcome}

	snap, err := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName))
	switch {
	case err == nil:
		g, parseErr := graph.Parse(snap.Graph)
		if parseErr != nil {
			return Facts{}, fmt.Errorf("reconstruct graph: %w", parseErr)
		}
		facts.HasSnapshot = true
		facts.CompletedNodes = len(snap.CompletedNodes())
		facts.TotalNodes = len(g.Nodes)
	case errors.Is(err, fs.ErrNotExist):
		// Legitimate at every status, not only before a run settles:
		// state.json is written atomically after a node's terminal verdict, so
		// its absence never means damage — and since ADR 0023 §3 a refused
		// plan settles without one for good.
	default:
		return Facts{}, err
	}
	return facts, nil
}

// Of is Probe for a caller that has not read the directory yet: it gathers the
// facts and composes them. A caller that also renders the word asks Gather
// itself, so it can put Spoken's question before the status.
func Of(runDir string) (Status, error) {
	facts, err := Gather(runDir)
	if err != nil {
		return 0, err
	}
	return Probe(runDir, facts), nil
}

// OrphanWarning is the sentence every surface prints beside an abandoned run,
// and it is the ONLY mitigation ADR 0015 accepts for its largest cost. The lock
// fd is O_CLOEXEC, so a model-CLI child does not hold it: a death that takes the
// engine without taking its children (SIGHUP from a closed terminal, kill -9, a
// panic, an OOM kill — the ordinary ways a multi-hour run dies) leaves an
// orphaned subprocess still running and still spending while the run reads
// abandoned. Resuming then runs that node alongside its own orphan. The ADR
// rejects probing for it — that would be a fifth exec seam — so this warning is
// what stands between the operator and a double spend, on every surface.
//
// It covers a planner call too, and needs no rewording to do it: a planner call
// is a model-CLI subprocess spawned through the same seam, in its own process
// group, and it can outlive the engine exactly as a node's can (ADR 0023 §2.2).
const OrphanWarning = "a model-CLI subprocess started by the dead leg may still be running and spending, so check for one before you spend again"

// Recovery is what an operator can actually do about an abandoned run, and it
// splits on whether the run ever wrote a snapshot.
//
// A run killed before its first node settled has no state.json — the snapshot
// is written only at a node's terminal verdict, and a run killed inside its
// PLANNER CALL has not even reached a graph — and `resume` loads the snapshot
// before it branches, so it fails outright on such a run, --retry-failed
// included. That is not a gap to paper over: with no recorded graph there is
// nothing to resume FROM, and the only honest recovery is to run the graph
// again (ADR 0015 §5).
func Recovery(runID string, hasSnapshot bool) string {
	if hasSnapshot {
		return fmt.Sprintf("continue it with `oh-my-graph resume %s --retry-failed`", runID)
	}
	return "it never wrote a snapshot, so there is nothing to resume from — run the graph again"
}

// Hint is the one-line recovery hint ADR 0015 §4 requires beside every
// abandoned run: on the `runs list` row, in `watch`'s refusal, on the dashboard
// card, and on the single-run page that card links to — the two web surfaces
// because the gate button between them is one click that spends money. `resume`
// prints OrphanWarning directly instead, because it IS the recovery.
//
// What it does NOT say, deliberately, is that a run whose lock predates the
// marker takes two steps to recover: the resume it offers is refused by the
// acquire path's legacy arm, which names the stale file to delete (ADR 0015 §4
// keeps a human there on purpose). Teaching this wording about lock formats
// would leak runstate's file layout into the composition layer; the sequence is
// written down in docs/RUN-FEED.md's "Liveness" section and in DESIGN.md
// instead, and the refusal itself carries the exact command.
func Hint(runID string, hasSnapshot bool) string {
	return fmt.Sprintf("run %s is ABANDONED — a leg started and never reported an end: %s. WARNING: %s.",
		runID, Recovery(runID, hasSnapshot), OrphanWarning)
}

// PausedHint is the sentence beside a PAUSED row, and it exists for the same
// reason Hint does: it is a row a reader cannot act on without being told how.
// Until ADR 0023 such a run rendered FAIL, so there was nothing to tell them.
//
// It names both resume shapes rather than picking one, because the reading
// surfaces genuinely cannot tell them apart cheaply: PAUSED is derived from the
// stream's outcome, which covers a gate pause and a session-limit pause alike,
// and asking the snapshot's gate block which one it was is the derivation
// re-entry ADR 0023 §9 forbids. Naming the wrong one costs a command that
// refuses itself; naming neither costs the reader the whole row.
//
// It does not promise the command will be accepted: a run started with
// --verify-cmd cannot be resumed at all (ADR 0016 §4), and the row cannot
// cheaply know that. The refusal carries its own explanation, so the cost is
// one wasted command rather than a wrong action (ADR 0023 §5).
func PausedHint(runID string) string {
	return fmt.Sprintf("run %s is PAUSED — it stopped as designed and is resumable: after a session limit, "+
		"`oh-my-graph resume %s --retry-failed`; at a gate, `oh-my-graph resume %s --approve <gate-id>` "+
		"or `--reject <gate-id>` (`oh-my-graph watch %s` replays the stream and names the gate).",
		runID, runID, runID, runID)
}
