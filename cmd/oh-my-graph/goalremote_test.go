package main

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// goalRemoteRecord is one leg that saw a branch created at the run's own
// commit, with the pull-request part not compared.
func goalRemoteRecord() *runstate.RemoteChanges {
	return &runstate.RemoteChanges{Legs: []runstate.RemoteLeg{{
		Changes: &runstate.RemoteDiff{
			Observed: []runstate.RemotePart{runstate.RemotePartRemotes, runstate.RemotePartBranches},
			Entries: []runstate.RemoteChangeEntry{{
				Kind: runstate.BranchCreated, Remote: "origin", Branch: "feat/x",
				NewSHA: "3333333333333333333333333333333333333333", Attribution: runstate.AttributionRunCommit,
			}},
			NotCompared: []runstate.RemoteNotCompared{{Part: runstate.RemotePartPullRequests, Reason: "gh: not authenticated"}},
		},
	}}}
}

// ADR 0046 §8 item 10: cycleEvidence copies the run's remote change record
// and its guard flag out of state.json, and the assessor prompt built from it
// carries the ENGINE-OBSERVED remote changes block, guarded run or not.
func TestCycleEvidence_CarriesTheRemoteChangeRecordToTheAssessor(t *testing.T) {
	for _, guarded := range []bool{false, true} {
		t.Run(map[bool]string{false: "unguarded", true: "guarded"}[guarded], func(t *testing.T) {
			t.Setenv("OMG_HOME", t.TempDir())
			const runID = "run-remote"
			record := goalRemoteRecord()
			if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), runstate.Snapshot{
				RunID:               runID,
				Nodes:               map[string]runstate.NodeRecord{"work": {Verdict: runstate.VerdictPass}},
				RemoteChanges:       record,
				ForbidRemoteChanges: guarded,
			}); err != nil {
				t.Fatalf("write fixture snapshot: %v", err)
			}
			plan := coordinator.Plan{Graph: &graph.Graph{Nodes: []graph.Node{{ID: "work"}}}}
			evidence, err := cycleEvidence(runID, plan, true)
			if err != nil {
				t.Fatalf("cycleEvidence: %v", err)
			}
			if evidence.RemoteChanges == nil || !reflect.DeepEqual(*evidence.RemoteChanges, *record) {
				t.Fatalf("RemoteChanges = %+v, want %+v", evidence.RemoteChanges, *record)
			}
			if evidence.ForbidRemoteChanges != guarded {
				t.Errorf("ForbidRemoteChanges = %v, want %v", evidence.ForbidRemoteChanges, guarded)
			}

			fake := runner.NewFakeRunner(nil)
			fake.KeyFn = func(runner.NodeInvocation) string { return "assess" }
			fake.SetOutcome("assess", runner.NodeOutcome{Result: cycleAssessMet})
			if _, err := coordinator.New(fake).Assess(context.Background(), "ship it", evidence); err != nil {
				t.Fatalf("Assess: %v", err)
			}
			prompt := fake.Invocations()[0].Prompt
			for _, want := range []string{
				"\nENGINE-OBSERVED remote changes",
				"branch_created origin/feat/x at 333333333333 — " + coordinator.RemoteLabelRunCommit,
				"not compared: pull_requests: gh: not authenticated",
			} {
				if !strings.Contains(prompt, want) {
					t.Errorf("assessor prompt lacks %q:\n%s", want, prompt)
				}
			}
			if got := strings.Contains(prompt, "\n  guarded: remotes, branches\n"); got != guarded {
				t.Errorf("guarded parts line present = %v, want %v:\n%s", got, guarded, prompt)
			}
			if strings.Contains(prompt, "made by") {
				t.Errorf("assessor prompt says \"made by\":\n%s", prompt)
			}
		})
	}
}

// A run from before the record existed has no remote_changes in state.json:
// cycleEvidence leaves the record nil, and the block is its one no-record line.
func TestCycleEvidence_NoRemoteRecordStaysNil(t *testing.T) {
	t.Setenv("OMG_HOME", t.TempDir())
	const runID = "run-old"
	if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), runstate.Snapshot{
		RunID: runID,
		Nodes: map[string]runstate.NodeRecord{"work": {Verdict: runstate.VerdictPass}},
	}); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
	evidence, err := cycleEvidence(runID, coordinator.Plan{Graph: &graph.Graph{Nodes: []graph.Node{{ID: "work"}}}}, true)
	if err != nil {
		t.Fatalf("cycleEvidence: %v", err)
	}
	if evidence.RemoteChanges != nil || evidence.ForbidRemoteChanges {
		t.Errorf("evidence = record %+v, flag %v; want nil, false", evidence.RemoteChanges, evidence.ForbidRemoteChanges)
	}
}
