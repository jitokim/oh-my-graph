package runstatus

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// --- the rule itself ---------------------------------------------------------

// TestDerive_AbandonedNeedsTwoAffirmativeFacts walks the whole product of the
// liveness inputs against an open and a closed leg, because the value of stating
// the rule once is that its table is the table — ADR 0015 §2's, exactly, which
// ADR 0023 imports whole rather than reopening.
func TestDerive_AbandonedNeedsTwoAffirmativeFacts(t *testing.T) {
	// A settled shape with everything a PASS needs, so the closed-leg rows below
	// isolate the lock as the only variable.
	passed := Facts{HasSnapshot: true, CompletedNodes: 2, TotalNodes: 2}
	open := Facts{OpenLeg: true}

	cases := []struct {
		facts Facts
		lock  runstate.Liveness
		want  Status
	}{
		// A closed leg settles whatever the lock says: the lock is not
		// consulted at all for a run that reported an end.
		{passed, runstate.LivenessFree, Pass},
		{passed, runstate.LivenessHeld, Pass},
		{passed, runstate.LivenessUnknown, Pass},
		// An open leg whose holder is alive is the ordinary running run.
		{open, runstate.LivenessHeld, Running},
		// Every doubt — a missing lock file, an unmarked one, a filesystem
		// whose flock is not this flock, a probe error — arrives here as
		// unknown and must read as today's answer, never as abandoned.
		{open, runstate.LivenessUnknown, Running},
		// The one abandoned arm: two affirmative facts.
		{open, runstate.LivenessFree, Abandoned},
		// And the split PLANNING introduces is INSIDE the in-flight side: a free
		// lock still answers first, so a planner call whose process died is
		// abandoned rather than stuck reading PLANNING forever.
		{Facts{OpenLeg: true, Phase: runfeed.PhasePlanning}, runstate.LivenessHeld, Planning},
		{Facts{OpenLeg: true, Phase: runfeed.PhasePlanning}, runstate.LivenessUnknown, Planning},
		{Facts{OpenLeg: true, Phase: runfeed.PhasePlanning}, runstate.LivenessFree, Abandoned},
	}
	for _, tc := range cases {
		if got := Derive(tc.facts, tc.lock); got != tc.want {
			t.Errorf("Derive(%+v, lock=%v) = %v, want %v", tc.facts, tc.lock, got, tc.want)
		}
	}
}

// TestDerive_IsTotalOverTheSixValues is ADR 0023 §2.1's precedence table, cell
// by cell, including the two cells that are the whole reason the enumeration is
// six rather than four and the one §3 makes common.
func TestDerive_IsTotalOverTheSixValues(t *testing.T) {
	for _, tc := range []struct {
		name  string
		facts Facts
		lock  runstate.Liveness
		want  Status
	}{
		{
			name:  "inside the planner call",
			facts: Facts{OpenLeg: true, Phase: runfeed.PhasePlanning},
			lock:  runstate.LivenessHeld,
			want:  Planning,
		},
		{
			// Affirmative in BOTH directions: the plan committed, so the LATEST
			// run_started carries no phase. Deriving this from "no node has
			// started yet" would paint the first instants of every hand-written
			// run PLANNING (ADR 0023 §2.3).
			name:  "the plan committed, before the first node launches",
			facts: Facts{OpenLeg: true},
			lock:  runstate.LivenessHeld,
			want:  Running,
		},
		{
			// The defect ADR 0023 §1.2 measures. PAUSED comes off the stream's
			// outcome, so this fixture carries NO gate anywhere — it is ADR
			// 0009's session-limit pause, the one the dashboard painted red.
			name:  "a session-limit pause, with no gate in sight",
			facts: Facts{LastOutcome: runfeed.OutcomePaused, HasSnapshot: true, CompletedNodes: 1, TotalNodes: 3},
			want:  Paused,
		},
		{
			// "paused" answers before the completed-count, so a pause that
			// happens to have finished every node is still PAUSED, not PASS.
			name:  "the pause outcome beats the completed count",
			facts: Facts{LastOutcome: runfeed.OutcomePaused, HasSnapshot: true, CompletedNodes: 3, TotalNodes: 3},
			want:  Paused,
		},
		{
			name:  "every node of the graph passed",
			facts: Facts{LastOutcome: runfeed.OutcomePassed, HasSnapshot: true, CompletedNodes: 3, TotalNodes: 3},
			want:  Pass,
		},
		{
			name:  "a node failed",
			facts: Facts{LastOutcome: runfeed.OutcomeFailed, HasSnapshot: true, CompletedNodes: 2, TotalNodes: 3},
			want:  Fail,
		},
		{
			// §2.1.1's newest cell, and the one §3 makes the shape of every
			// refused plan: a closed leg with no snapshot at all.
			name:  "a refused plan: settled, no snapshot",
			facts: Facts{LastOutcome: runfeed.OutcomeFailed},
			want:  Fail,
		},
		{
			// PASS is the ONE value that requires the snapshot, so the
			// conservative direction is forced: a passed leg whose snapshot
			// write was lost reads FAIL rather than claiming a PASS from a
			// stream token while the record that proves it is missing (§5).
			name:  "a passed outcome whose snapshot is missing cannot claim PASS",
			facts: Facts{LastOutcome: runfeed.OutcomePassed},
			want:  Fail,
		},
		{
			// The counts alone must not manufacture a PASS out of nothing:
			// 0 == 0 is true, and a snapshot-less directory has both.
			name:  "zero of zero is not a pass",
			facts: Facts{HasSnapshot: true},
			want:  Fail,
		},
		{
			// An open leg answers before ANY snapshot question — mid-run the
			// snapshot holds only the nodes completed so far.
			name:  "an open leg beats a completed-looking snapshot",
			facts: Facts{OpenLeg: true, HasSnapshot: true, CompletedNodes: 3, TotalNodes: 3},
			lock:  runstate.LivenessHeld,
			want:  Running,
		},
		{
			// ...and beats a stale outcome from a leg that already closed once,
			// which is exactly a resumed run's stream.
			name:  "an open leg beats the previous leg's paused outcome",
			facts: Facts{OpenLeg: true, LastOutcome: runfeed.OutcomePaused},
			lock:  runstate.LivenessHeld,
			want:  Running,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Derive(tc.facts, tc.lock); got != tc.want {
				t.Errorf("Derive(%+v, lock=%v) = %v, want %v", tc.facts, tc.lock, got, tc.want)
			}
		})
	}
}

// TestStatus_ZeroValueIsNoStatusAtAll pins the choice ADR 0023 makes about the
// enumeration's zero: it is NOT one of the six. Nothing derives it, so a Status
// that was never derived cannot masquerade as a run's real answer — and in
// particular cannot claim a run passed, is abandoned, or is settled.
func TestStatus_ZeroValueIsNoStatusAtAll(t *testing.T) {
	var s Status
	for _, real := range []Status{Planning, Running, Abandoned, Paused, Pass, Fail} {
		if s == real {
			t.Fatalf("zero Status equals %v — the zero value must not be one of the six", real)
		}
	}
	if s.Settled() || s.InFlight() {
		t.Errorf("zero Status: Settled=%v InFlight=%v, want both false", s.Settled(), s.InFlight())
	}
	if got := s.String(); got != "UNKNOWN" {
		t.Errorf("zero Status.String() = %q, want %q", got, "UNKNOWN")
	}
}

// TestStatus_StringIsTheWordEverySurfaceRenders pins the six words, because
// String is what the `runs list` STATUS column, `show`'s header, `watch`'s
// opening line and the dashboard's state token all put in front of an operator:
// changing one of them silently changes what the tool says a run is.
func TestStatus_StringIsTheWordEverySurfaceRenders(t *testing.T) {
	cases := []struct {
		status Status
		want   string
	}{
		{Planning, "PLANNING"},
		{Running, "RUNNING"},
		{Abandoned, "ABANDONED"},
		{Paused, "PAUSED"},
		{Pass, "PASS"},
		{Fail, "FAIL"},
	}
	seen := map[string]bool{}
	for _, tc := range cases {
		got := tc.status.String()
		if got != tc.want {
			t.Errorf("Status(%d).String() = %q, want %q", int(tc.status), got, tc.want)
		}
		if seen[got] {
			t.Errorf("two statuses render as %q — the six words must be distinguishable", got)
		}
		seen[got] = true
	}
}

// TestStatus_PredicatesPartitionTheSix is the symmetry ADR 0023 §2.1.1 demands:
// PLANNING splits the IN-FLIGHT side, so a predicate on only the settled side
// would leave every in-flight call site to re-derive membership by hand — and
// ResolveRun rewritten as `== Running` would silently stop preferring a planning
// run. Every value is on exactly one side.
func TestStatus_PredicatesPartitionTheSix(t *testing.T) {
	for _, tc := range []struct {
		status       Status
		wantSettled  bool
		wantInFlight bool
	}{
		{Planning, false, true},
		{Running, false, true},
		{Paused, true, false},
		{Pass, true, false},
		{Fail, true, false},
		// ABANDONED is on NEITHER side, and that is the point of it: the leg is
		// open (so it never settled) and no process is working on it (so it is
		// not in flight). Folding it into either would erase ADR 0015 §4.
		{Abandoned, false, false},
	} {
		if got := tc.status.Settled(); got != tc.wantSettled {
			t.Errorf("%v.Settled() = %v, want %v", tc.status, got, tc.wantSettled)
		}
		if got := tc.status.InFlight(); got != tc.wantInFlight {
			t.Errorf("%v.InFlight() = %v, want %v", tc.status, got, tc.wantInFlight)
		}
		if tc.status.Settled() && tc.status.InFlight() {
			t.Errorf("%v is both settled and in flight", tc.status)
		}
	}
}

// --- Of: the stream and the lock, read off a real run directory --------------

// TestOf_ComposesTheStreamWithTheLock exercises the composition against real
// files: a real event stream written by the real StreamWriter, and a real lock
// taken and released by runstate.AcquireLock. The interesting pair is the last
// two cases — the SAME open-leg stream reads in flight while a leg holds the
// lock and abandoned once it does not, which is the whole point of the change.
func TestOf_ComposesTheStreamWithTheLock(t *testing.T) {
	openLeg := []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodeStarted, NodeID: "a"},
	}
	closedLeg := append(append([]runfeed.Event{}, openLeg...),
		runfeed.Event{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
		runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
	)

	planningLeg := []runfeed.Event{{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning}}
	refusedPlan := []runfeed.Event{
		{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
	}
	pausedLeg := []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventNodeStarted, NodeID: "a"},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
	}

	cases := []struct {
		name   string
		events []runfeed.Event
		lock   string // "none", "held", "free", "legacy-gone", "legacy-alive"
		snap   string // "" for none; otherwise the node id that passed
		want   Status
	}{
		{name: "no stream and no snapshot at all", lock: "none", want: Fail},
		{name: "closed leg, free lock, every node passed", events: closedLeg, lock: "free", snap: "a", want: Pass},
		{name: "closed leg whose snapshot is missing cannot read PASS", events: closedLeg, lock: "free", want: Fail},
		{name: "open leg, no lock file", events: openLeg, lock: "none", want: Running},
		{name: "open leg, lock held", events: openLeg, lock: "held", want: Running},
		{name: "open leg, lock free", events: openLeg, lock: "free", want: Abandoned},
		// The shape both preserved specimens of ADR 0015's Context are on disk.
		// It reads exactly like a released marked lock here, and it has to: a
		// run abandoned before the upgrade has no other route to a verdict,
		// because no live binary will ever write the marker into its file.
		{name: "open leg, a pre-flock lock whose pid is gone", events: openLeg, lock: "legacy-gone", want: Abandoned},
		// The same file with a pid that still names something is the in-flight
		// arm, because alive is inconclusive on a pre-flock lock.
		{name: "open leg, a pre-flock lock whose pid still names a process", events: openLeg, lock: "legacy-alive", want: Running},
		// The three cells ADR 0023 adds, over real bytes.
		{name: "a planner call in progress", events: planningLeg, lock: "held", want: Planning},
		{name: "a planner call whose process died", events: planningLeg, lock: "free", want: Abandoned},
		{name: "a refused plan: two events, no graph.json, no state.json", events: refusedPlan, lock: "free", want: Fail},
		{name: "a paused leg, snapshot present but incomplete", events: pausedLeg, lock: "free", snap: "a", want: Paused},
		{name: "a paused leg with no snapshot at all", events: pausedLeg, lock: "free", want: Paused},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDir := t.TempDir()
			if len(tc.events) > 0 {
				writeEvents(t, runDir, "run-1", tc.events)
			}
			if tc.snap != "" {
				writeSnapshot(t, runDir, tc.snap)
			}
			switch tc.lock {
			case "held":
				holdLock(t, runDir)
			case "free":
				freeLock(t, runDir)
			case "legacy-gone":
				legacyLock(t, runDir, deadPID, runstate.LivenessFree)
			case "legacy-alive":
				legacyLock(t, runDir, os.Getpid(), runstate.LivenessUnknown)
			}

			got, err := Of(runDir)
			if err != nil {
				t.Fatalf("Of returned error: %v", err)
			}
			if got != tc.want {
				t.Errorf("Of = %v, want %v", got, tc.want)
			}
			// Probe is the same answer for a caller that already gathered the
			// facts — the dashboard card's path, which must not pay a second
			// read of either file — and the two must never differ.
			if probed := Probe(runDir, factsOf(t, runDir)); probed != got {
				t.Errorf("Probe = %v but Of = %v; the two forms of the rule disagree", probed, got)
			}
		})
	}
}

// TestOf_MissingSnapshotIsNeverAnErrorButAnUnreadableOneAlwaysIs is ADR 0023
// §2.1.1's central distinction, at the layer that decides it: the ABSENCE of a
// file is a fact about the run (derivable, and since §3 permanent for a refused
// plan), while the UNREADABILITY of one is a fact about the reader (an error,
// and the only kind that may make a row disappear).
func TestOf_MissingSnapshotIsNeverAnErrorButAnUnreadableOneAlwaysIs(t *testing.T) {
	settled := []runfeed.Event{
		{Type: runfeed.EventRunStarted},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
	}

	t.Run("absent", func(t *testing.T) {
		runDir := t.TempDir()
		writeEvents(t, runDir, "run-1", settled)
		got, err := Of(runDir)
		if err != nil {
			t.Fatalf("Of over a settled run with no snapshot = error %v, want a status", err)
		}
		if got != Fail {
			t.Errorf("Of = %v, want %v", got, Fail)
		}
	})

	t.Run("corrupt", func(t *testing.T) {
		runDir := t.TempDir()
		writeEvents(t, runDir, "run-1", settled)
		if err := os.WriteFile(filepath.Join(runDir, runstate.SnapshotFileName), []byte("{not json"), 0o600); err != nil {
			t.Fatalf("write corrupt snapshot: %v", err)
		}
		if _, err := Of(runDir); err == nil {
			t.Fatal("Of over a corrupt snapshot = nil error, want the reader's own refusal")
		}
	})

	t.Run("schema this binary refuses", func(t *testing.T) {
		runDir := t.TempDir()
		writeEvents(t, runDir, "run-1", settled)
		if err := os.WriteFile(filepath.Join(runDir, runstate.SnapshotFileName), []byte(`{"schema":99}`), 0o600); err != nil {
			t.Fatalf("write incompatible snapshot: %v", err)
		}
		if _, err := Of(runDir); err == nil {
			t.Fatal("Of over an incompatible snapshot schema = nil error, want a refusal")
		}
	})

	t.Run("graph bytes that will not parse", func(t *testing.T) {
		runDir := t.TempDir()
		writeEvents(t, runDir, "run-1", settled)
		// Valid JSON, valid snapshot schema, and graph bytes that are not a
		// graph — the one shape runstate.Write itself cannot produce.
		body := `{"schema":` + strconv.Itoa(runstate.Schema) + `,"run_id":"run-1","graph":{"name":"x","nodes":"not a list"}}`
		if err := os.WriteFile(filepath.Join(runDir, runstate.SnapshotFileName), []byte(body), 0o600); err != nil {
			t.Fatalf("write snapshot: %v", err)
		}
		if _, err := Of(runDir); err == nil {
			t.Fatal("Of over unparseable graph bytes = nil error, want a refusal")
		}
	})
}

// TestOf_UnreadableStreamIsTheCallersProblem pins that a stream this binary
// refuses to read is surfaced as an error rather than quietly becoming
// "settled" — `runs list` turns that into its WARNING+skip row, and nothing
// may turn it into a verdict.
func TestOf_UnreadableStreamIsTheCallersProblem(t *testing.T) {
	runDir := t.TempDir()
	writeRawEvents(t, runDir, `{"schema":99,"ts":"2026-08-01T00:00:00Z","run_id":"r","event":"run_started"}`+"\n")

	if _, err := Of(runDir); err == nil {
		t.Fatal("Of accepted a stream schema newer than this binary, want an error")
	}
}

// --- the wording ADR 0015 §4 requires on every surface a spender is on -------

func TestHint_SplitsOnWhetherThereIsAnythingToResumeFrom(t *testing.T) {
	withSnapshot := Hint("run-7", true)
	if !strings.Contains(withSnapshot, "oh-my-graph resume run-7 --retry-failed") {
		t.Errorf("a resumable abandoned run must be told the exact command: %q", withSnapshot)
	}

	// The shape ADR 0015 §5 insists on getting right: no snapshot means no
	// recorded graph, so there is nothing to resume FROM and advising `resume`
	// would send the operator into a command that fails.
	withoutSnapshot := Hint("run-7", false)
	if strings.Contains(withoutSnapshot, "oh-my-graph resume") {
		t.Errorf("a snapshot-less run must not be handed a resume command that would fail: %q", withoutSnapshot)
	}
	if !strings.Contains(withoutSnapshot, "run the graph again") {
		t.Errorf("a snapshot-less run's only honest recovery is re-running the graph: %q", withoutSnapshot)
	}

	// The orphan hazard is the ADR's largest accepted cost and the wording is
	// its only mitigation, so it rides on both arms.
	for _, hint := range []string{withSnapshot, withoutSnapshot} {
		if !strings.Contains(hint, OrphanWarning) {
			t.Errorf("every hint must carry the orphaned-claude warning: %q", hint)
		}
		if !strings.Contains(hint, "run-7") {
			t.Errorf("a hint must name its run: %q", hint)
		}
	}
}

// TestPausedHint_NamesBothResumeShapes pins the wording ADR 0023 adds beside the
// value it un-collapses. PAUSED is derived from the stream's outcome, which
// covers a gate pause and a session-limit pause alike, so the hint must name
// both commands rather than guessing — asking the snapshot's gate block which
// one it was is the derivation re-entry §9 forbids.
func TestPausedHint_NamesBothResumeShapes(t *testing.T) {
	hint := PausedHint("run-9")
	for _, want := range []string{"run-9", "--retry-failed", "--approve", "--reject"} {
		if !strings.Contains(hint, want) {
			t.Errorf("PausedHint must mention %q: %q", want, hint)
		}
	}
	// It must not read as damage: a paused run stopped exactly as designed, and
	// the whole defect being fixed is that it was reported as a failure.
	for _, forbidden := range []string{"FAIL", "ABANDONED"} {
		if strings.Contains(hint, forbidden) {
			t.Errorf("PausedHint must not describe a paused run as %q: %q", forbidden, hint)
		}
	}
}

// --- fixtures ----------------------------------------------------------------

// writeSnapshot writes a real snapshot for a one-node graph whose only node
// passed, through runstate.Write — so Of's snapshot load and its graph.Parse
// both run against bytes a real run would have left.
func writeSnapshot(t *testing.T, runDir, passedNodeID string) {
	t.Helper()
	if err := runstate.Write(filepath.Join(runDir, runstate.SnapshotFileName), runstate.Snapshot{
		RunID: "run-1",
		Graph: []byte(`{"name":"fixture","nodes":[{"id":"` + passedNodeID + `","prompt":"p"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			passedNodeID: {Verdict: runstate.VerdictPass},
		},
	}); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
}

// factsOf gathers the facts a caller with both files already open would hold —
// the dashboard card's position — so Probe can be judged against Of over the
// same directory.
func factsOf(t *testing.T, runDir string) Facts {
	t.Helper()
	leg, err := runfeed.LastLeg(filepath.Join(runDir, runfeed.FileName))
	if err != nil {
		t.Fatalf("runfeed.LastLeg: %v", err)
	}
	facts := Facts{OpenLeg: leg.Open, Phase: leg.Phase, LastOutcome: leg.LastOutcome}
	snap, err := runstate.Load(filepath.Join(runDir, runstate.SnapshotFileName))
	if err != nil {
		return facts
	}
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		t.Fatalf("graph.Parse: %v", err)
	}
	facts.HasSnapshot = true
	facts.CompletedNodes = len(snap.CompletedNodes())
	facts.TotalNodes = len(g.Nodes)
	return facts
}

// writeEvents writes a run's events.jsonl through the real StreamWriter, so the
// fixture's bytes are exactly what a real leg emits.
func writeEvents(t *testing.T, runDir, runID string, events []runfeed.Event) {
	t.Helper()
	w, err := runfeed.NewStreamWriter(filepath.Join(runDir, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open fixture event stream: %v", err)
	}
	t.Cleanup(func() {
		if err := w.Close(); err != nil {
			t.Errorf("close fixture event stream: %v", err)
		}
	})
	for _, e := range events {
		if err := w.Emit(e); err != nil {
			t.Fatalf("emit fixture event %q: %v", e.Type, err)
		}
	}
}

func writeRawEvents(t *testing.T, runDir, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(runDir, runfeed.FileName), []byte(content), 0o644); err != nil {
		t.Fatalf("write raw fixture event stream: %v", err)
	}
}

// holdLock takes the run's real lock and holds it for the test, the way a live
// leg does. It skips rather than fails where the probe cannot answer (no
// flock(2), or a filesystem outside the known-local allowlist): there the
// derivation is unknown by design, and asserting otherwise would be asserting
// against ADR 0015's own safety gate.
func holdLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, runstate.LockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	t.Cleanup(func() { release() })
	requireLiveness(t, runstate.ProbeLock(path), runstate.LivenessHeld)
}

// freeLock leaves behind exactly what a leg that died leaves behind: a marked
// lock file, written by the real AcquireLock, that nothing holds.
func freeLock(t *testing.T, runDir string) {
	t.Helper()
	path := filepath.Join(runDir, runstate.LockFileName)
	release, err := runstate.AcquireLock(path)
	if err != nil {
		t.Fatalf("acquire fixture lock: %v", err)
	}
	if err := release(); err != nil {
		t.Fatalf("release fixture lock: %v", err)
	}
	requireLiveness(t, runstate.ProbeLock(path), runstate.LivenessFree)
}

// deadPID is a pid no process can bear — above pid_max on both platforms this
// project ships to — so the "gone" branch is assertable without spawning
// anything (the design forbids a fifth exec seam for exactly this question).
const deadPID = 2147483647

// legacyLock leaves behind what a leg from BEFORE the flock upgrade leaves
// behind: a bare `<pid>\n` with no marker, its writer having taken no flock at
// all. Both preserved zombie runs are byte-for-byte this. It skips rather than
// fails where the probe is gated off, for the same reason freeLock does.
func legacyLock(t *testing.T, runDir string, pid int, want runstate.Liveness) {
	t.Helper()
	path := filepath.Join(runDir, runstate.LockFileName)
	if err := os.WriteFile(path, []byte(strconv.Itoa(pid)+"\n"), 0o644); err != nil {
		t.Fatalf("write a pre-flock fixture lock: %v", err)
	}
	requireLiveness(t, runstate.ProbeLock(path), want)
}

// requireLiveness gates a fixture on the probe having actually answered. Only
// LivenessUnknown skips — no flock(2), or a filesystem outside the known-local
// set, leaves the derivation unknown BY DESIGN, and asserting past that would
// be asserting against ADR 0015's own safety gate. Every other disagreement is
// a contradiction (a held lock reading free, or a free one reading held) and
// must fail, not quietly skip the test that would have caught it.
func requireLiveness(t *testing.T, got, want runstate.Liveness) {
	t.Helper()
	if got == runstate.LivenessUnknown {
		t.Skipf("the lock probe cannot answer here (%v): no flock(2), or a filesystem outside the known-local set", got)
	}
	if got != want {
		t.Fatalf("ProbeLock over the fixture lock = %v, want %v", got, want)
	}
}
