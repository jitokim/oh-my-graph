package schedule

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// limitRecordAt is the instant the fake clock reads at the pause, so the
// record's time is asserted exactly.
var limitRecordAt = time.Date(2026, 10, 8, 9, 30, 0, 0, time.UTC)

// snapshotRecorderIn is the real, disk-backed recorder a CLI leg injects,
// writing state.json into a temp run directory, so a test can read back the
// bytes a run actually leaves.
func snapshotRecorderIn(t *testing.T) (*runstate.SnapshotRecorder, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), runstate.SnapshotFileName)
	return runstate.NewSnapshotRecorder(path, runstate.Snapshot{RunID: "run-358", Graph: []byte(`{}`)}), path
}

// TestScheduler_LimitPauseWritesTheRecord_358 (#358): ADR 0031 §8.1 end to end — a
// leg that stops on a Claude session limit, a Codex usage limit, or Claude's
// per-model limit persists the limited node ids, the cause exactly as the
// runtime printed it, and the scheduler clock's time of the pause, while the
// limited node itself stays out of Nodes (ADR 0009) and the returned error is
// unchanged.
func TestScheduler_LimitPauseWritesTheRecord_358(t *testing.T) {
	for _, shape := range limitShapes() {
		t.Run(shape.name, func(t *testing.T) {
			g := mustGraph(t, `
name: limit-record
concurrency: 3
nodes:
  - { id: b, prompt: b }
  - { id: a, prompt: a }
  - { id: ok, prompt: ok }
  - { id: after, prompt: after, depends_on: [a] }
`)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
				"a": shape.outcome, "b": shape.outcome, "ok": pass("s-ok", 0), "after": pass("s-after", 0),
			})
			rec, path := snapshotRecorderIn(t)
			clock := &fakeClock{t: limitRecordAt}
			s, h, led := newHarness(t, fake, Options{Recorder: rec, Now: clock.Now})

			err := s.Run(context.Background(), g, h, led)
			var limited *LimitPausedError
			if !errors.As(err, &limited) {
				t.Fatalf("expected *LimitPausedError, got %T: %v", err, err)
			}

			snap, loadErr := runstate.Load(path)
			if loadErr != nil {
				t.Fatalf("load state.json: %v", loadErr)
			}
			got := snap.LimitPause
			if got == nil {
				t.Fatal("a limit pause wrote no limit_pause record")
			}
			if want := []string{"a", "b"}; !reflect.DeepEqual(got.NodeIDs, want) {
				t.Errorf("record node ids = %v, want %v (sorted)", got.NodeIDs, want)
			}
			if got.Cause != shape.outcome.FailureCause {
				t.Errorf("record cause = %q, want the runtime's own text %q", got.Cause, shape.outcome.FailureCause)
			}
			if !got.At.Equal(limitRecordAt) {
				t.Errorf("record time = %v, want the scheduler clock's %v", got.At, limitRecordAt)
			}
			if !reflect.DeepEqual(got.NodeIDs, limited.NodeIDs) || got.Cause != limited.Cause {
				t.Errorf("record %+v disagrees with the returned error %+v", got, limited)
			}
			for _, id := range []string{"a", "b"} {
				if _, ok := snap.Nodes[id]; ok {
					t.Errorf("limited node %q reached Nodes — it must stay un-run (ADR 0009)", id)
				}
			}
			if snap.Nodes["ok"].Verdict != runstate.VerdictPass {
				t.Errorf("the drained sibling's record was lost: %+v", snap.Nodes)
			}
			if snap.Gate.PausedAt != "" {
				t.Errorf("a pure limit pause set the gate record: %+v", snap.Gate)
			}
		})
	}
}

// TestScheduler_NoLimitWritesNoRecord_358 (#358): a leg that passes, or pauses only
// at a gate, never writes the key — its state.json is what it was before the
// record existed.
func TestScheduler_NoLimitWritesNoRecord_358(t *testing.T) {
	cases := map[string]string{
		"a passing run": `
name: no-limit
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b, depends_on: [a] }
`,
		"a gate pause": `
name: gate-only
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`,
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			g := mustGraph(t, yaml)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": pass("s-a", 0), "b": pass("s-b", 0)})
			rec, path := snapshotRecorderIn(t)
			s, h, led := newHarness(t, fake, Options{Recorder: rec})

			err := s.Run(context.Background(), g, h, led)
			var limited *LimitPausedError
			if errors.As(err, &limited) {
				t.Fatalf("no node hit a limit, got %v", err)
			}
			raw, readErr := os.ReadFile(path)
			if readErr != nil {
				t.Fatalf("read state.json: %v", readErr)
			}
			if strings.Contains(string(raw), "limit_pause") {
				t.Fatalf("a leg without a limit wrote the key:\n%s", raw)
			}
		})
	}
}

// TestScheduler_GatePauseAndLimitPersistBoth_358 (#358): ADR 0031 §8.3 — a leg in
// which a gate pauses AND a node hits the limit still returns the gate's
// *PausedError (the gate needs its human first), but state.json carries both
// records.
func TestScheduler_GatePauseAndLimitPersistBoth_358(t *testing.T) {
	for _, shape := range limitShapes() {
		t.Run(shape.name, func(t *testing.T) {
			g := mustGraph(t, `
name: gate-and-limit
concurrency: 2
nodes:
  - { id: approve, type: gate }
  - { id: a, prompt: a }
`)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": shape.outcome})
			rec, path := snapshotRecorderIn(t)
			clock := &fakeClock{t: limitRecordAt}
			s, h, led := newHarness(t, fake, Options{Recorder: rec, Now: clock.Now, Gate: gate.NewPauseController()})

			err := s.Run(context.Background(), g, h, led)
			var paused *PausedError
			if !errors.As(err, &paused) || paused.GateID != "approve" {
				t.Fatalf("the gate must win the returned error, got %T: %v", err, err)
			}

			snap, loadErr := runstate.Load(path)
			if loadErr != nil {
				t.Fatalf("load state.json: %v", loadErr)
			}
			if snap.Gate.PausedAt != "approve" {
				t.Errorf("gate record = %+v, want paused at approve", snap.Gate)
			}
			if snap.LimitPause == nil || !reflect.DeepEqual(snap.LimitPause.NodeIDs, []string{"a"}) ||
				snap.LimitPause.Cause != shape.outcome.FailureCause || !snap.LimitPause.At.Equal(limitRecordAt) {
				t.Errorf("limit record = %+v, want [a] with the runtime's cause at %v", snap.LimitPause, limitRecordAt)
			}
		})
	}
}

// TestScheduler_LimitRecordWriteFailureChangesNoError_358 (#358): the record is for
// display only (ADR 0031 §8.2), so a failed write of it is surfaced on the
// progress feed — like a failed RecordNode — and leaves the leg's returned
// error exactly what it would have been: the limit pause, or the gate pause
// that outranks it.
func TestScheduler_LimitRecordWriteFailureChangesNoError_358(t *testing.T) {
	cases := map[string]string{
		"a pure limit pause": `
name: limit-only
nodes:
  - { id: a, prompt: a }
`,
		"a gate pause beside the limit": `
name: gate-and-limit
concurrency: 2
nodes:
  - { id: approve, type: gate }
  - { id: a, prompt: a }
`,
	}
	for name, yaml := range cases {
		t.Run(name, func(t *testing.T) {
			run := func(fail error) (error, *fakeRecorder, string) {
				g := mustGraph(t, yaml)
				fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome()})
				fr := newFakeRecorder()
				fr.failRecordLimitPause = fail
				feed := &strings.Builder{}
				s, h, led := newHarness(t, fake, Options{Recorder: fr, ProgressWriter: feed})
				return s.Run(context.Background(), g, h, led), fr, feed.String()
			}

			wantErr, _, _ := run(nil)
			gotErr, fr, feed := run(errors.New("disk full"))

			if gotErr == nil || wantErr == nil || gotErr.Error() != wantErr.Error() || reflect.TypeOf(gotErr) != reflect.TypeOf(wantErr) {
				t.Fatalf("a failed limit-record write changed the returned error: %T %v, want %T %v", gotErr, gotErr, wantErr, wantErr)
			}
			if len(fr.limitPauses) != 1 {
				t.Fatalf("RecordLimitPause called %d time(s), want 1", len(fr.limitPauses))
			}
			if !strings.Contains(feed, "limit pause snapshot write failed: disk full") {
				t.Errorf("the failed write must be surfaced on the progress feed, got:\n%s", feed)
			}
		})
	}
}
