package main

import (
	"path/filepath"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// #332: the assessor's material is read back from state.json, so the engine's
// verify record has to cross that seam or the assessor judges a verified node
// on its bare reply. A node with a record carries it field for field; a node
// without a verify carries none, rather than an empty record that would read
// as a check with no exit code.
func TestCycleEvidence_CarriesTheEngineVerificationRecord(t *testing.T) {
	t.Setenv("OMG_HOME", t.TempDir())
	const runID = "run-verify"
	exit := 3
	want := runstate.VerificationRecord{
		Command:          "go test ./...",
		ExitCode:         &exit,
		ExpectedExitCode: 0,
		Status:           runstate.VerificationFailed,
		OutputTail:       "--- FAIL: TestX\nFAIL",
	}
	if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), runstate.Snapshot{
		RunID: runID,
		Nodes: map[string]runstate.NodeRecord{
			"check": {Verdict: runstate.VerdictFail, Detail: "verify failed", Verification: &want},
			"work":  {Verdict: runstate.VerdictPass},
		},
	}); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
	plan := coordinator.Plan{Graph: &graph.Graph{Nodes: []graph.Node{{ID: "work"}, {ID: "check"}}}}

	evidence, err := cycleEvidence(runID, plan, false)
	if err != nil {
		t.Fatalf("cycleEvidence: %v", err)
	}
	if len(evidence.Nodes) != 2 {
		t.Fatalf("got %d nodes, want 2: %+v", len(evidence.Nodes), evidence.Nodes)
	}
	if work := evidence.Nodes[0]; work.Verification != nil {
		t.Errorf("node without a verify carries a record: %+v", *work.Verification)
	}
	got := evidence.Nodes[1].Verification
	if got == nil {
		t.Fatal("the check node's verification record did not reach NodeEvidence")
	}
	if got.Command != want.Command || got.ExitCode == nil || *got.ExitCode != exit ||
		got.ExpectedExitCode != want.ExpectedExitCode || got.Status != string(want.Status) || got.OutputTail != want.OutputTail {
		t.Errorf("verification = %+v (exit %v), want %+v (exit %d)", *got, got.ExitCode, want, exit)
	}
}
