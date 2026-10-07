package runstatus

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// TestOf_LimitPauseRecordChangesNoStatus_358: ADR 0031 §8.2 — the snapshot's
// limit_pause record is for display, and like gate.paused_at it is not a fact
// the derivation reads. A limit-paused run is PAUSED because its stream says
// so, and every leg derives the same status, from the same Facts, with the
// record and without it.
func TestOf_LimitPauseRecordChangesNoStatus_358(t *testing.T) {
	started := runfeed.Event{Type: runfeed.EventRunStarted}
	finished := func(outcome string) runfeed.Event {
		return runfeed.Event{Type: runfeed.EventRunFinished, Outcome: outcome}
	}
	cases := []struct {
		name   string
		events []runfeed.Event
		want   Status
	}{
		{"a limit-paused leg", []runfeed.Event{started, finished(runfeed.OutcomePaused)}, Paused},
		{"a passed leg", []runfeed.Event{started, finished(runfeed.OutcomePassed)}, Pass},
		{"an open leg", []runfeed.Event{started}, Running},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			derive := func(record *runstate.LimitPause) (Status, Facts) {
				runDir := t.TempDir()
				writeEvents(t, runDir, "run-1", tc.events)
				if err := runstate.Write(filepath.Join(runDir, runstate.SnapshotFileName), runstate.Snapshot{
					RunID:      "run-1",
					Graph:      []byte(`{"name":"fixture","nodes":[{"id":"a","prompt":"p"}]}`),
					Nodes:      map[string]runstate.NodeRecord{"a": {Verdict: runstate.VerdictPass}},
					LimitPause: record,
				}); err != nil {
					t.Fatalf("write fixture snapshot: %v", err)
				}
				if tc.want == Running {
					holdLock(t, runDir)
				}
				got, err := Of(runDir)
				if err != nil {
					t.Fatalf("Of: %v", err)
				}
				facts, err := Gather(runDir)
				if err != nil {
					t.Fatalf("Gather: %v", err)
				}
				return got, facts
			}

			without, factsWithout := derive(nil)
			with, factsWith := derive(&runstate.LimitPause{
				NodeIDs: []string{"a"},
				Cause:   "You've hit your session limit · resets 5:20pm",
				At:      time.Date(2026, 10, 8, 17, 0, 0, 0, time.UTC),
			})
			if without != tc.want || with != tc.want {
				t.Errorf("status without the record = %v, with it = %v, want %v for both", without, with, tc.want)
			}
			if factsWith != factsWithout {
				t.Errorf("the record leaked into Facts: %+v vs %+v", factsWith, factsWithout)
			}
		})
	}
}
