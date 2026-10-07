package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// limitedOutcome is what CLIRunner classifies for a limit-killed subprocess,
// carrying cause as the CLI printed it.
func limitedOutcome(cause string) runner.NodeOutcome {
	return runner.NodeOutcome{ExitCode: 1, FailureCause: cause, SessionLimited: true}
}

// passedOutcome is an ordinary passing node.
func passedOutcome(key string) runner.NodeOutcome {
	return runner.NodeOutcome{SessionID: "s-" + key, Result: "PASS", ExitCode: 0}
}

// statePath is the run's state.json.
func statePath(runID string) string {
	return filepath.Join(runDirFor(runID), stateFileName)
}

// TestResume_LegPastTheLimitClearsTheRecord_358 (#358): ADR 0031 §8.1 — the record
// is written at the limit pause and cleared by the resumed leg that runs past
// it, while what resume relaunches and every exit code stay as they were.
func TestResume_LegPastTheLimitClearsTheRecord_358(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"limit-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"b","prompt":"b","depends_on":["a"]}]}`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome(limitCauseMsg), "b": passedOutcome("b")})
	runID := "run-limit-358"

	var runErr error
	captureStdout(t, func() {
		runErr = executeGraph(context.Background(), runID, g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-flow.yaml", []byte("name: limit-flow\n"), false, nil, nil, nil)
	})
	if code := exitCodeForError(runErr); code != 2 {
		t.Fatalf("limited leg exit code = %d, want 2 (err %v)", code, runErr)
	}
	paused := loadSnapshot(t, runID).LimitPause
	if paused == nil || !reflect.DeepEqual(paused.NodeIDs, []string{"a"}) || paused.Cause != limitCauseMsg || paused.At.IsZero() {
		t.Fatalf("the limited leg must record its pause, got %+v", paused)
	}

	fake.SetOutcome("a", passedOutcome("a"))
	var resumeErr error
	captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if code := exitCodeForError(resumeErr); code != 0 {
		t.Fatalf("the retry leg exit code = %d, want 0 (err %v)", code, resumeErr)
	}
	if got := fake.InvocationCount("a"); got != 2 {
		t.Fatalf("a ran %d time(s), want 2 — resume must still relaunch the limited node", got)
	}
	if got := loadSnapshot(t, runID).LimitPause; got != nil {
		t.Fatalf("a leg that ran past the limit must clear the record, still %+v", got)
	}
}

// TestResume_LimitAndGateRecordsKeepTheirOwnLifecycles_358 (#358): ADR 0031 §8.3 — a
// run that pauses on a limit and at a gate holds both records, and each is
// cleared by the leg that runs past it and rewritten by the leg that hits it
// again, independently of the other:
//
//	leg 1  x limited, g1 pauses          → gate g1, limit [x]
//	leg 2  --approve g1, b limited       → no gate, limit [b] (new cause)
//	leg 3  --retry-failed, g2 pauses     → gate g2, no limit
//	leg 4  --approve g2                  → neither
func TestResume_LimitAndGateRecordsKeepTheirOwnLifecycles_358(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"limit-gate","nodes":[
		{"id":"x","prompt":"x"},
		{"id":"g1","type":"gate"},
		{"id":"b","prompt":"b","depends_on":["g1"]},
		{"id":"g2","type":"gate","depends_on":["b"]},
		{"id":"done","prompt":"done","depends_on":["g2","x"]}]}`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"x": limitedOutcome(limitCauseMsg), "b": passedOutcome("b"), "done": passedOutcome("done"),
	})
	runID := "run-limit-gate-358"

	var legErr error
	captureStdout(t, func() {
		legErr = executeGraph(context.Background(), runID, g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-gate.yaml", []byte("name: limit-gate\n"), false, nil, nil, nil)
	})
	var gatePaused *schedule.PausedError
	if !errors.As(legErr, &gatePaused) || exitCodeForError(legErr) != 2 {
		t.Fatalf("leg 1: want a gate pause exiting 2, got %T: %v", legErr, legErr)
	}
	leg1 := loadSnapshot(t, runID)
	if leg1.Gate.PausedAt != "g1" {
		t.Fatalf("leg 1: gate record = %q, want g1", leg1.Gate.PausedAt)
	}
	if leg1.LimitPause == nil || !reflect.DeepEqual(leg1.LimitPause.NodeIDs, []string{"x"}) || leg1.LimitPause.Cause != limitCauseMsg {
		t.Fatalf("leg 1: limit record = %+v, want [x] with the Claude cause", leg1.LimitPause)
	}

	fake.SetOutcome("x", passedOutcome("x"))
	fake.SetOutcome("b", limitedOutcome(codexLimitCauseMsg))
	captureStdout(t, func() {
		legErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "g1"}), fake, nil)
	})
	var limitPaused *schedule.LimitPausedError
	if !errors.As(legErr, &limitPaused) || exitCodeForError(legErr) != 2 {
		t.Fatalf("leg 2: want a limit pause exiting 2, got %T: %v", legErr, legErr)
	}
	leg2 := loadSnapshot(t, runID)
	if leg2.Gate.PausedAt != "" {
		t.Fatalf("leg 2 ran past g1, but the gate record is still %q", leg2.Gate.PausedAt)
	}
	if leg2.LimitPause == nil || !reflect.DeepEqual(leg2.LimitPause.NodeIDs, []string{"b"}) || leg2.LimitPause.Cause != codexLimitCauseMsg {
		t.Fatalf("leg 2: the new limit must rewrite the record with its own nodes and cause, got %+v", leg2.LimitPause)
	}
	if leg2.LimitPause.At.Before(leg1.LimitPause.At) {
		t.Fatalf("leg 2: the rewritten record kept an earlier time: %v < %v", leg2.LimitPause.At, leg1.LimitPause.At)
	}

	fake.SetOutcome("b", passedOutcome("b"))
	captureStdout(t, func() {
		legErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if !errors.As(legErr, &gatePaused) || gatePaused.GateID != "g2" || exitCodeForError(legErr) != 2 {
		t.Fatalf("leg 3: want a pause at g2 exiting 2, got %T: %v", legErr, legErr)
	}
	leg3 := loadSnapshot(t, runID)
	if leg3.LimitPause != nil {
		t.Fatalf("leg 3 ran past the limit, but its record is still %+v", leg3.LimitPause)
	}
	if leg3.Gate.PausedAt != "g2" {
		t.Fatalf("leg 3: gate record = %q, want g2", leg3.Gate.PausedAt)
	}

	captureStdout(t, func() {
		legErr = executeResume(parseResumeFlags(t, []string{runID, "--approve", "g2"}), fake, nil)
	})
	if code := exitCodeForError(legErr); code != 0 {
		t.Fatalf("leg 4: exit code = %d, want 0 (err %v)", code, legErr)
	}
	leg4 := loadSnapshot(t, runID)
	if leg4.Gate.PausedAt != "" || leg4.LimitPause != nil {
		t.Fatalf("leg 4 finished the run, but records remain: gate %q, limit %+v", leg4.Gate.PausedAt, leg4.LimitPause)
	}
	for id, want := range map[string]int{"x": 2, "b": 2, "done": 1} {
		if got := fake.InvocationCount(id); got != want {
			t.Errorf("%s ran %d time(s) across the four legs, want %d", id, got, want)
		}
	}
}

// TestResume_OlderStateWithoutTheRecordResumesUnchanged_358 (#358): a limit-paused
// run whose state.json predates the field — the same snapshot with the key
// removed — resumes exactly as one carrying it: the same nodes relaunch, the
// same exit code, and no record afterwards.
func TestResume_OlderStateWithoutTheRecordResumesUnchanged_358(t *testing.T) {
	for _, withRecord := range []bool{true, false} {
		name := "older state.json"
		if withRecord {
			name = "with the record"
		}
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			g := mustParse(t, `{"name":"limit-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"b","prompt":"b","depends_on":["a"]}]}`)
			fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": limitedOutcome(limitCauseMsg), "b": passedOutcome("b")})
			runID := "run-older-358"
			captureStdout(t, func() {
				_ = executeGraph(context.Background(), runID, g, fake, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-flow.yaml", []byte("name: limit-flow\n"), false, nil, nil, nil)
			})
			if !withRecord {
				dropLimitPauseKey(t, statePath(runID))
			}

			fake.SetOutcome("a", passedOutcome("a"))
			var resumeErr error
			captureStdout(t, func() {
				resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
			})
			if code := exitCodeForError(resumeErr); code != 0 {
				t.Fatalf("exit code = %d, want 0 (err %v)", code, resumeErr)
			}
			if a, b := fake.InvocationCount("a"), fake.InvocationCount("b"); a != 2 || b != 1 {
				t.Fatalf("invocations a=%d b=%d, want a=2 b=1", a, b)
			}
			if _, ok := rawStateKeys(t, statePath(runID))["limit_pause"]; ok {
				t.Fatal("the finished leg's state.json still carries limit_pause")
			}
		})
	}
}

// rawStateKeys is state.json's top-level keys, undecoded.
func rawStateKeys(t *testing.T, path string) map[string]json.RawMessage {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return keys
}

// dropLimitPauseKey rewrites state.json as a binary from before the field
// would have written it: every other key byte for byte, and no limit_pause.
func dropLimitPauseKey(t *testing.T, path string) {
	t.Helper()
	keys := rawStateKeys(t, path)
	if _, ok := keys["limit_pause"]; !ok {
		t.Fatal("fixture: the limited leg wrote no limit_pause to drop")
	}
	delete(keys, "limit_pause")
	raw, err := json.Marshal(keys)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if snap, err := runstate.Load(path); err != nil || snap.LimitPause != nil {
		t.Fatalf("fixture: older state.json did not load without the record: %v %+v", err, snap.LimitPause)
	}
}
