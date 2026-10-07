package main

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
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
		OutputTruncated:  true,
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
		got.ExpectedExitCode != want.ExpectedExitCode || got.Status != string(want.Status) || got.OutputTail != want.OutputTail ||
		got.OutputTruncated != want.OutputTruncated {
		t.Errorf("verification = %+v (exit %v), want %+v (exit %d)", *got, got.ExitCode, want, exit)
	}
}

// assessorPromptForSinkRecord runs the goal loop's own assessment path for one
// sink node carrying rec: rec is written into a real state.json, read back by
// cycleEvidence, and handed to coordinator.Assess — the same two calls
// runGoalLoop makes — against a FakeRunner standing in for the assessor. It
// returns the prompt the assessor would have read, split into the text outside
// every nonce-bearing fence and the text inside one.
func assessorPromptForSinkRecord(t *testing.T, rec runstate.VerificationRecord) (prompt, outside, inside string) {
	t.Helper()
	t.Setenv("OMG_HOME", t.TempDir())
	const runID = "run-sink-verify"
	verdict := runstate.VerdictPass
	if rec.Status != runstate.VerificationPassed {
		verdict = runstate.VerdictFail
	}
	if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), runstate.Snapshot{
		RunID: runID,
		Nodes: map[string]runstate.NodeRecord{"sink": {Verdict: verdict, Verification: &rec}},
	}); err != nil {
		t.Fatalf("write fixture snapshot: %v", err)
	}
	plan := coordinator.Plan{Graph: &graph.Graph{Nodes: []graph.Node{{ID: "sink"}}}}
	evidence, err := cycleEvidence(runID, plan, verdict == runstate.VerdictPass)
	if err != nil {
		t.Fatalf("cycleEvidence: %v", err)
	}

	fake := runner.NewFakeRunner(nil)
	fake.KeyFn = func(runner.NodeInvocation) string { return "assess" }
	fake.SetOutcome("assess", runner.NodeOutcome{Result: cycleAssessMet})
	if _, err := coordinator.New(fake).Assess(context.Background(), "make the verify pass", evidence); err != nil {
		t.Fatalf("Assess: %v", err)
	}
	invocations := fake.Invocations()
	if len(invocations) != 1 {
		t.Fatalf("got %d assessor calls, want 1", len(invocations))
	}
	prompt = invocations[0].Prompt
	_, opening, found := strings.Cut(prompt, "--- node results ")
	if !found {
		t.Fatalf("assessor prompt carries no node-results fence:\n%s", prompt)
	}
	nonce, _, _ := strings.Cut(opening, " ")
	var out, in strings.Builder
	fenced := false
	for _, line := range strings.Split(prompt, "\n") {
		if strings.HasPrefix(line, "---") && strings.Contains(line, nonce) {
			fenced = !strings.HasPrefix(line, "--- end ")
			continue
		}
		if fenced {
			in.WriteString(line + "\n")
		} else {
			out.WriteString(line + "\n")
		}
	}
	return prompt, out.String(), in.String()
}

// #332 END TO END: a sink whose --verify-cmd printed one line reaches the
// assessor with the literal command — quotes and all, not an escaped copy —
// and its exit code as engine-observed facts outside the fence, and the line
// it printed inside it.
func TestGoalAssessment_SinkVerifyReachesTheAssessorVerbatim(t *testing.T) {
	const command = `sh -c 'printf "VERIFY OK\n"'`
	exit := 0
	prompt, outside, inside := assessorPromptForSinkRecord(t, runstate.VerificationRecord{
		Command: command, ExitCode: &exit, Status: runstate.VerificationPassed, OutputTail: "VERIFY OK\n",
	})
	for _, want := range []string{
		"ENGINE-OBSERVED verification of node sink",
		"  command: " + command + "\n  status: passed\n  exit code: 0\n",
	} {
		if !strings.Contains(outside, want) {
			t.Errorf("engine-observed %q is not outside the fence:\n%s", want, prompt)
		}
	}
	if !strings.Contains(inside, "VERIFY OK") || strings.Contains(outside, "VERIFY OK\n") {
		t.Errorf("the verify output must reach the assessor inside the fence only:\n%s", prompt)
	}
}

// #332: a sink verify that exited 1 reaches the assessor as a failure with its
// real exit code, whatever the node replied.
func TestGoalAssessment_FailingSinkVerifyShowsFailedAndItsExitCode(t *testing.T) {
	exit := 1
	prompt, outside, _ := assessorPromptForSinkRecord(t, runstate.VerificationRecord{
		Command: "go test ./...", ExitCode: &exit, Status: runstate.VerificationFailed, OutputTail: "--- FAIL: TestX\nFAIL",
	})
	if want := "  command: go test ./...\n  status: failed\n  exit code: 1\n"; !strings.Contains(outside, want) {
		t.Errorf("failing verify is missing %q outside the fence:\n%s", want, prompt)
	}
}

// #332 END TO END: a sink verify that printed ~100KB reaches the assessor
// tail-only and bounded. The record holds only verify.RetainedTail's tail —
// built here exactly as the scheduler builds it — and the assessor's material
// keeps the last line, never the first, and says ABOVE the fence, in the
// engine's own lines, that the engine kept only the tail of a longer output.
func TestGoalAssessment_HugeSinkVerifyOutputReachesTheAssessorTailOnly(t *testing.T) {
	const first, last = "FIRST-LINE-OF-OUTPUT", "LAST-LINE-OF-OUTPUT: VERIFY OK"
	var huge strings.Builder
	huge.WriteString(first + "\n")
	for huge.Len() < 100<<10 {
		huge.WriteString("progress: compiling package and running its tests\n")
	}
	huge.WriteString(last + "\n")
	tail, cut := verify.RetainedTail(huge.String())
	exit := 0
	prompt, outside, inside := assessorPromptForSinkRecord(t, runstate.VerificationRecord{
		Command: "./verify.sh", ExitCode: &exit, Status: runstate.VerificationPassed,
		OutputTail: tail, OutputTruncated: cut,
	})

	if !strings.Contains(inside, last) {
		t.Errorf("the output's last line did not reach the assessor:\n%s", prompt)
	}
	if strings.Contains(prompt, first) {
		t.Error("the output's first line reached the assessor; only the tail may")
	}
	if strings.Count(inside, "progress: compiling") > 100 {
		t.Errorf("the fenced output is not bounded: %d bytes inside the fences", len(inside))
	}
	if len(prompt) > 16<<10 {
		t.Errorf("assessor prompt is %d bytes for one verify node; the output was not bounded", len(prompt))
	}
	if !strings.Contains(outside, "the engine kept only the last") || !strings.Contains(outside, "everything earlier was dropped when the check was recorded") {
		t.Errorf("the material never says above the fence that the engine kept only the tail:\n%s", outside)
	}
	if !strings.Contains(outside, "  command: ./verify.sh\n  status: passed\n  exit code: 0\n") {
		t.Errorf("the engine record is missing beside the tail:\n%s", outside)
	}
}
