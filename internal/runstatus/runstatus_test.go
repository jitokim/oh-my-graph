package runstatus

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// --- the rule itself ---------------------------------------------------------

// TestDerive_AbandonedNeedsTwoAffirmativeFacts walks the whole product of the
// two inputs, because the value of stating the rule once is that its table is
// the table — ADR 0015 §2's, exactly.
func TestDerive_AbandonedNeedsTwoAffirmativeFacts(t *testing.T) {
	cases := []struct {
		openLeg bool
		lock    runstate.Liveness
		want    Status
	}{
		// A closed leg is settled whatever the lock says: the lock is not
		// consulted at all for a run that reported an end.
		{false, runstate.LivenessFree, Settled},
		{false, runstate.LivenessHeld, Settled},
		{false, runstate.LivenessUnknown, Settled},
		// An open leg whose holder is alive is the ordinary running run.
		{true, runstate.LivenessHeld, InFlight},
		// Every doubt — a missing lock file, an unmarked one, a filesystem
		// whose flock is not this flock, a probe error — arrives here as
		// unknown and must read as today's answer, never as abandoned.
		{true, runstate.LivenessUnknown, InFlight},
		// The one abandoned arm: two affirmative facts.
		{true, runstate.LivenessFree, Abandoned},
	}
	for _, tc := range cases {
		if got := Derive(tc.openLeg, tc.lock); got != tc.want {
			t.Errorf("Derive(openLeg=%v, lock=%v) = %v, want %v", tc.openLeg, tc.lock, got, tc.want)
		}
	}
}

func TestStatus_ZeroValueIsSettled(t *testing.T) {
	// A Status nobody set must not be able to claim a run is abandoned — the
	// same reason runstate.Liveness's zero value is unknown.
	var s Status
	if s != Settled {
		t.Fatalf("zero Status = %v, want %v", s, Settled)
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

	cases := []struct {
		name   string
		events []runfeed.Event
		lock   string // "none", "held", "free", "legacy-gone", "legacy-alive"
		want   Status
	}{
		{"no stream at all", nil, "none", Settled},
		{"closed leg, free lock", closedLeg, "free", Settled},
		{"open leg, no lock file", openLeg, "none", InFlight},
		{"open leg, lock held", openLeg, "held", InFlight},
		{"open leg, lock free", openLeg, "free", Abandoned},
		// The shape both preserved specimens of ADR 0015's Context are on disk.
		// It reads exactly like a released marked lock here, and it has to: a
		// run abandoned before the upgrade has no other route to a verdict,
		// because no live binary will ever write the marker into its file.
		{"open leg, a pre-flock lock whose pid is gone", openLeg, "legacy-gone", Abandoned},
		// The same file with a pid that still names something is the in-flight
		// arm, because alive is inconclusive on a pre-flock lock.
		{"open leg, a pre-flock lock whose pid still names a process", openLeg, "legacy-alive", InFlight},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			runDir := t.TempDir()
			if len(tc.events) > 0 {
				writeEvents(t, runDir, "run-1", tc.events)
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
			// Probe is the same answer for a caller that already knows the leg
			// state — the dashboard card's path — and the two must never differ.
			open, err := runfeed.InFlight(filepath.Join(runDir, runfeed.FileName))
			if err != nil {
				t.Fatalf("runfeed.InFlight returned error: %v", err)
			}
			if probed := Probe(runDir, open); probed != got {
				t.Errorf("Probe = %v but Of = %v; the two forms of the rule disagree", probed, got)
			}
		})
	}
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

// --- the wording ADR 0015 §4 requires on four surfaces -----------------------

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

// --- fixtures ----------------------------------------------------------------

// writeEvents writes a run's events.jsonl through the real StreamWriter, so the
// fixture's bytes are exactly what a real leg emits.
func writeEvents(t *testing.T, runDir, runID string, events []runfeed.Event) {
	t.Helper()
	w, err := runfeed.NewStreamWriter(filepath.Join(runDir, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open fixture event stream: %v", err)
	}
	defer w.Close()
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
	if got := runstate.ProbeLock(path); got != runstate.LivenessHeld {
		t.Skipf("the lock probe cannot answer here (%v): no flock(2), or a filesystem outside the known-local set", got)
	}
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
	if got := runstate.ProbeLock(path); got != runstate.LivenessFree {
		t.Skipf("the lock probe cannot answer here (%v): no flock(2), or a filesystem outside the known-local set", got)
	}
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
	if got := runstate.ProbeLock(path); got != want {
		t.Skipf("the lock probe cannot answer here (%v, want %v): no flock(2), or a filesystem outside the known-local set", got, want)
	}
}
