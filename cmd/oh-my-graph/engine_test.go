package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// setVersion stands in for an -ldflags -X main.Version=... build for the rest
// of the test, restoring the real value afterwards.
func setVersion(t *testing.T, v string) {
	t.Helper()
	saved := Version
	t.Cleanup(func() { Version = saved })
	Version = v
}

// readStateKeys decodes the run's state.json into raw top-level keys, so a test
// can see what is ABSENT as well as what is present.
func readStateKeys(t *testing.T, runDir string) map[string]json.RawMessage {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(runDir, stateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	return keys
}

// assertStateEngine checks state.json names version plus this test binary's
// own commit/modified, under the omit rules: no empty commit, no
// modified:false, no modified without a commit.
func assertStateEngine(t *testing.T, runDir, version string) {
	t.Helper()
	keys := readStateKeys(t, runDir)
	if got := string(keys["engine_version"]); got != `"`+version+`"` {
		t.Errorf("state.json engine_version = %s, want %q", got, version)
	}
	build := currentEngine()
	commit, hasCommit := keys["engine_commit"]
	switch {
	case build.Commit == "" && hasCommit:
		t.Errorf("state.json carries engine_commit %s for a build with no VCS info", commit)
	case build.Commit != "" && string(commit) != `"`+build.Commit+`"`:
		t.Errorf("state.json engine_commit = %s, want %q", commit, build.Commit)
	}
	modified, hasModified := keys["engine_modified"]
	switch {
	case build.Modified && string(modified) != "true":
		t.Errorf("state.json engine_modified = %s, want true", modified)
	case !build.Modified && hasModified:
		t.Errorf("state.json carries engine_modified %s, want it absent", modified)
	}
}

// assertEventEngine is assertStateEngine for one run_started.
func assertEventEngine(t *testing.T, e runfeed.Event, version string) {
	t.Helper()
	build := currentEngine()
	want := runfeed.Engine{Version: version, Commit: build.Commit, Modified: build.Modified}
	if e.Engine != want {
		t.Errorf("run_started (phase %q) engine = %+v, want %+v", e.Phase, e.Engine, want)
	}
}

func runStartedEvents(events []runfeed.Event) []runfeed.Event {
	var opens []runfeed.Event
	for _, e := range events {
		if e.Type == runfeed.EventRunStarted {
			opens = append(opens, e)
		}
	}
	return opens
}

// TestEngine_RunAndResumeLegsRecordTheirOwnBuild (#373): the first leg stamps
// its binary's version on state.json and its run_started; a resume leg run
// by a different build re-stamps state.json with ITS values and opens its own
// run_started with them, while the first leg's run_started keeps its own.
func TestEngine_RunAndResumeLegsRecordTheirOwnBuild(t *testing.T) {
	isolateRunHome(t)
	setVersion(t, "9.9.9-test")
	runID, rec := pausedGateFlowRun(t)
	runDir := runDirFor(runID)

	assertStateEngine(t, runDir, "9.9.9-test")
	opens := runStartedEvents(readRunEvents(t, runID))
	if len(opens) != 1 {
		t.Fatalf("first leg wrote %d run_started, want 1", len(opens))
	}
	assertEventEngine(t, opens[0], "9.9.9-test")

	Version = "9.9.10-test"
	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
		t.Fatalf("executeResume: %v", err)
	}
	assertStateEngine(t, runDir, "9.9.10-test")
	opens = runStartedEvents(readRunEvents(t, runID))
	if len(opens) != 2 {
		t.Fatalf("stream has %d run_started after the resume, want 2", len(opens))
	}
	assertEventEngine(t, opens[0], "9.9.9-test")
	assertEventEngine(t, opens[1], "9.9.10-test")
}

// TestEngine_AutoStampsThePlanningPhaseAndTheSchedulerLeg (#373): an auto
// run's planning-phase run_started carries the stamp too, not only the
// scheduler's.
func TestEngine_AutoStampsThePlanningPhaseAndTheSchedulerLeg(t *testing.T) {
	isolateRunHome(t)
	setVersion(t, "9.9.9-test")
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec},
		"work-1": {SessionID: "s-1", Result: "PASS", ExitCode: 0},
	})
	captureStdout(t, func() {
		if err := planAndExecute(context.Background(), io.Discard, coordinator.New(fake), fake,
			commonRunFlags{inputs: inputFlag{}}, "add a README section", singleCycle, false, nil, nil); err != nil {
			t.Fatalf("planAndExecute: %v", err)
		}
	})

	runDir := soleRunDir(t)
	assertStateEngine(t, runDir, "9.9.9-test")
	opens := runStartedEvents(readStreamEvents(t, runDir))
	if len(opens) != 2 || opens[0].Phase != runfeed.PhasePlanning {
		t.Fatalf("want a planning run_started then the scheduler's, got %+v", opens)
	}
	for _, e := range opens {
		assertEventEngine(t, e, "9.9.9-test")
	}
}

// TestEngine_AnUnstampedRunStillLoads (#373): a run written before the stamp
// existed — state.json and run_started without any engine key — still shows,
// lists and resumes, and the resume leg stamps it from then on.
func TestEngine_AnUnstampedRunStillLoads(t *testing.T) {
	root := isolateRunHome(t)
	runID, rec := pausedGateFlowRun(t)
	runDir := runDirFor(runID)
	stripEngineKeys(t, filepath.Join(runDir, stateFileName))
	stripEngineKeys(t, filepath.Join(runDir, runfeed.FileName))
	if _, err := runstate.Load(filepath.Join(runDir, stateFileName)); err != nil {
		t.Fatalf("an unstamped snapshot must load: %v", err)
	}

	var shown bytes.Buffer
	if err := showRun(&shown, runDir, runID); err != nil {
		t.Fatalf("show on an unstamped run: %v", err)
	}
	var listed bytes.Buffer
	if err := listRuns(&listed, io.Discard, filepath.Join(root, "runs"), false); err != nil {
		t.Fatalf("runs list with an unstamped run: %v", err)
	}
	if !strings.Contains(listed.String(), runID) {
		t.Fatalf("runs list dropped the unstamped run:\n%s", listed.String())
	}

	setVersion(t, "9.9.9-test")
	if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
		t.Fatalf("resume of an unstamped run: %v", err)
	}
	assertStateEngine(t, runDir, "9.9.9-test")
}

// stripEngineKeys rewrites a state.json or events.jsonl as a pre-#373 binary
// would have written it: the same documents with no engine_* key. A state.json
// is one document; events.jsonl is one per line.
func stripEngineKeys(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	docs := []string{string(data)}
	if filepath.Base(path) == runfeed.FileName {
		docs = strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	}
	var out []string
	for _, doc := range docs {
		var keys map[string]json.RawMessage
		if err := json.Unmarshal([]byte(doc), &keys); err != nil {
			t.Fatalf("decode %s: %v", path, err)
		}
		delete(keys, "engine_version")
		delete(keys, "engine_commit")
		delete(keys, "engine_modified")
		encoded, err := json.Marshal(keys)
		if err != nil {
			t.Fatalf("encode %s: %v", path, err)
		}
		out = append(out, string(encoded))
	}
	if err := os.WriteFile(path, []byte(strings.Join(out, "\n")+"\n"), 0o600); err != nil {
		t.Fatalf("rewrite %s: %v", path, err)
	}
}
