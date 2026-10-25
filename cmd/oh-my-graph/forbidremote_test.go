package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// The guard tests of ADR 0046 §8.9 at the CLI: the flag parses on `run` and
// `auto`, lands in state.json with the schema stamp it requires, and every
// resumed leg carries it from the snapshot alone.

func TestForbidRemoteChanges_ParsesOnRunAndAuto(t *testing.T) {
	runF := newRunFlags()
	if err := runF.parse([]string{"graphs/x.yaml", "--forbid-remote-changes"}); err != nil {
		t.Fatalf("run: %v", err)
	}
	if !runF.forbidRemoteChanges {
		t.Error("run: forbid-remote-changes flag not set")
	}
	autoF := newAutoFlags()
	if err := autoF.parse([]string{"tidy the docs", "--forbid-remote-changes"}); err != nil {
		t.Fatalf("auto: %v", err)
	}
	if !autoF.forbidRemoteChanges {
		t.Error("auto: forbid-remote-changes flag not set")
	}
	if newRunFlags().forbidRemoteChanges || newAutoFlags().forbidRemoteChanges {
		t.Error("the guard is on without the flag")
	}
}

// resume reads the guard from the snapshot and has no spelling of it: one
// that could turn it off would let a later leg run unguarded.
func TestForbidRemoteChanges_ResumeRegistersNoFlag(t *testing.T) {
	for _, name := range []string{"forbid-remote-changes", "allow-remote-changes", "no-forbid-remote-changes"} {
		if newResumeFlags().set.Lookup(name) != nil {
			t.Errorf("resume registers --%s", name)
		}
	}
}

// stateSchemaAndGuard reads a run's raw schema stamp and whether state.json
// carries the forbid_remote_changes key at all.
func stateSchemaAndGuard(t *testing.T, runID string) (schema int, guard json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	var head struct {
		Schema int             `json:"schema"`
		Guard  json.RawMessage `json:"forbid_remote_changes"`
	}
	if err := json.Unmarshal(raw, &head); err != nil {
		t.Fatalf("decode state.json: %v", err)
	}
	return head.Schema, head.Guard
}

func TestRunGraph_TheGuardStampsSchema5AndItsAbsenceKeeps3(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  int
		guard string
	}{
		{"guarded", []string{"--forbid-remote-changes"}, runstate.SchemaWithRemoteGuard, "true"},
		{"unguarded", nil, runstate.Schema, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			path := writeGraphFile(t, "name: remote\nnodes:\n  - { id: work, prompt: work }\n")
			var err error
			captureStdout(t, func() {
				err = runGraphWith(append([]string{path}, tc.args...), passingWork(), browser.NewFakeOpener(), os.Stdout)
			})
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			schema, guard := stateSchemaAndGuard(t, soleRunID(t))
			if schema != tc.want || string(guard) != tc.guard {
				t.Errorf("state.json schema = %d, forbid_remote_changes = %q; want %d, %q", schema, guard, tc.want, tc.guard)
			}
		})
	}
}

func TestRunAuto_TheGuardStampsSchema5WithConventionsAndItsAbsenceKeeps4(t *testing.T) {
	for _, tc := range []struct {
		name  string
		args  []string
		want  int
		guard string
	}{
		{"guarded", []string{"--forbid-remote-changes"}, runstate.SchemaWithRemoteGuard, "true"},
		{"unguarded", nil, runstate.SchemaWithConventions, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			conventionsHome(t)
			style, mixed := conventionFiles(t)
			fake := newConventionsFake(map[string]runner.NodeOutcome{
				"plan-1": {Result: cycleSpec, TotalCostUSD: 0.04},
				"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
			}, nil)
			args := append([]string{"tidy the docs", "--conventions", style, "--conventions", mixed, "--accept-no-build-evidence"}, tc.args...)
			var err error
			captureStdout(t, func() {
				err = runAutoWith(args, fake, browser.NewFakeOpener(), os.Stdout)
			})
			if err != nil {
				t.Fatalf("auto: %v", err)
			}
			runID := soleRunID(t)
			schema, guard := stateSchemaAndGuard(t, runID)
			if schema != tc.want || string(guard) != tc.guard {
				t.Errorf("state.json schema = %d, forbid_remote_changes = %q; want %d, %q", schema, guard, tc.want, tc.guard)
			}
			if _, record := stateSchemaAndConventions(t, runID); record == nil {
				t.Error("state.json lost its conventions record")
			}
		})
	}
}

// gateFlowGraph pauses at approve after a and runs ship once approved.
const gateFlowGraph = `{"name":"gate-flow","nodes":[
	{"id":"a","prompt":"a"},
	{"id":"approve","type":"gate","depends_on":["a"]},
	{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`

func gateFlowFake() *runner.FakeRunner {
	return runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a":    {Result: "PASS", ExitCode: 0},
		"ship": {Result: "PASS", ExitCode: 0},
	})
}

// pauseGateFlow runs gate-flow's first leg as run-1 under flags and
// requires it to pause at the gate.
func pauseGateFlow(t *testing.T, fake *runner.FakeRunner, flags commonRunFlags) {
	t.Helper()
	var err error
	captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", mustParse(t, gateFlowGraph), fake, flags, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the first leg to pause, got %v", err)
	}
}

func TestResume_CarriesTheRemoteGuard(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"approve", []string{"run-1", "--approve", "approve"}},
		{"retry-failed", []string{"run-1", "--retry-failed"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := gateFlowFake()
			if tc.name == "retry-failed" {
				// A failed first leg, retried: the other resume path.
				fake = runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": {Result: "boom", ExitCode: 1}})
				var err error
				captureStdout(t, func() {
					err = executeGraph(context.Background(), "run-1", mustParse(t, `{"name":"one","nodes":[{"id":"a","prompt":"a"}]}`), fake,
						commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true}, nil, 0, "one.yaml", []byte("name: one\n"), false, nil, nil, nil)
				})
				if err == nil {
					t.Fatal("expected the first leg to fail")
				}
				fake = runner.NewFakeRunner(map[string]runner.NodeOutcome{"a": {Result: "PASS", ExitCode: 0}})
				// The retried prompt quotes the failed attempt; a is the only node.
				fake.KeyFn = func(runner.NodeInvocation) string { return "a" }
			} else {
				pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}, forbidRemoteChanges: true})
			}
			if schema, guard := stateSchemaAndGuard(t, "run-1"); schema != runstate.SchemaWithRemoteGuard || string(guard) != "true" {
				t.Fatalf("first leg: schema = %d, forbid_remote_changes = %q", schema, guard)
			}

			var err error
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, tc.args), fake, nil)
			})
			if err != nil {
				t.Fatalf("resume: %v", err)
			}
			if schema, guard := stateSchemaAndGuard(t, "run-1"); schema != runstate.SchemaWithRemoteGuard || string(guard) != "true" {
				t.Errorf("resumed leg: schema = %d, forbid_remote_changes = %q; the guard must hold on every leg", schema, guard)
			}
			if _, snap := loadRunSnapshot(t, "run-1"); !snap.ForbidRemoteChanges {
				t.Error("the resumed snapshot lost the guard")
			}
		})
	}
}

// A schema-3 snapshot written before the remote change record existed, with
// no remote_changes, no run_base_sha and no forbid_remote_changes, still
// loads and resumes, unguarded, and stays schema 3 (ADR 0046 §8.9).
func TestResume_APreGuardSchema3SnapshotResumes(t *testing.T) {
	isolateRunHome(t)
	fake := gateFlowFake()
	pauseGateFlow(t, fake, commonRunFlags{inputs: inputFlag{}})

	path := filepath.Join(runDirFor("run-1"), stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"remote_changes", "run_base_sha", "forbid_remote_changes"} {
		delete(keys, key)
	}
	if string(keys["schema"]) != "3" {
		t.Fatalf("the unguarded first leg stamped schema %s, want 3", keys["schema"])
	}
	old, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, old, 0o600); err != nil {
		t.Fatal(err)
	}

	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{"run-1", "--approve", "approve"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume of a pre-guard schema-3 snapshot: %v", err)
	}
	schema, guard := stateSchemaAndGuard(t, "run-1")
	if schema != runstate.Schema || guard != nil {
		t.Errorf("resumed: schema = %d, forbid_remote_changes = %q; want %d and no key", schema, guard, runstate.Schema)
	}
	if _, snap := loadRunSnapshot(t, "run-1"); snap.Nodes["ship"].Verdict != runstate.VerdictPass {
		t.Errorf("ship did not run on the resumed leg: %+v", snap.Nodes)
	}
}
