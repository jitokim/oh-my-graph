package main

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// legRecorder records every prompt a node was asked across BOTH legs and
// scripts one outcome per node prompt. It is the cross-process half of ADR 0016
// under test: what leg 2 asks can only have come off disk, because leg 1's
// process is gone and --retry-failed deleted its record.
type legRecorder struct {
	mu       sync.Mutex
	outcomes map[string]runner.NodeOutcome
	prompts  []string
}

func (r *legRecorder) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.prompts = append(r.prompts, spec.Prompt)
	return r.outcomes[nodePromptOf(spec.Prompt)], nil
}

func (r *legRecorder) promptsOf(nodePrompt string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []string
	for _, p := range r.prompts {
		if nodePromptOf(p) == nodePrompt {
			out = append(out, p)
		}
	}
	return out
}

// runOneFailingLeg runs a single-node graph to its terminal failure and returns
// the run id and the recorder, so a test can resume it.
func runOneFailingLeg(t *testing.T, runID, spec string, rec *legRecorder) {
	t.Helper()
	g := mustParse(t, spec)
	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "g.yaml", []byte(spec), false, nil, nil)
	if err == nil {
		t.Fatal("expected leg 1 to fail")
	}
}

// TestResume_RetryFailedCarriesThePreviousLegsReply is the whole reason the
// failed reply is written to disk rather than kept in memory: a
// `resume --retry-failed` is a DIFFERENT PROCESS, and it drops the FAIL record
// (with the ledger row and the capped detail) before it runs anything. The file
// is the only account of the attempt that crosses that boundary, and the retry
// leg quotes it back to the node it is repeating.
func TestResume_RetryFailedCarriesThePreviousLegsReply(t *testing.T) {
	isolateRunHome(t)
	const spec = `name: retry-across-legs
nodes:
  - id: work
    prompt: do the work
    success_check: { result_matches: "PASS" }
`
	rec := &legRecorder{outcomes: map[string]runner.NodeOutcome{
		"do the work": {Result: "LEG-ONE-REPLY", ExitCode: 0, SessionID: "s-1"},
	}}
	runOneFailingLeg(t, "run-retry-legs", spec, rec)

	// The record this leg wrote must say a check judged the reply, because that
	// — not the file's existence — is what leg 2 gates the quote on.
	snap := mustLoadSnapshot(t, "run-retry-legs")
	if !snap.Nodes["work"].Judged {
		t.Fatalf("leg 1 recorded work as unjudged; the cross-process quote has no gate to read")
	}

	rec.outcomes["do the work"] = runner.NodeOutcome{Result: "PASS", ExitCode: 0, SessionID: "s-2"}
	captureStdout(t, func() {
		if err := executeResume(parseResumeFlags(t, []string{"run-retry-legs", "--retry-failed"}), rec, nil); err != nil {
			t.Fatalf("executeResume --retry-failed: %v", err)
		}
	})

	prompts := rec.promptsOf("do the work")
	if len(prompts) != 2 {
		t.Fatalf("work ran %d time(s) across both legs, want 2", len(prompts))
	}
	if prompts[0] != "do the work" {
		t.Errorf("leg 1's prompt = %q, want the node's own prompt", prompts[0])
	}
	if !strings.Contains(prompts[1], "LEG-ONE-REPLY") {
		t.Errorf("the retry leg did not quote the previous leg's reply — the file on disk is the only "+
			"thing that survived that process, and nothing read it:\n%s", prompts[1])
	}
	if !strings.Contains(prompts[1], "--- previous attempt ") {
		t.Errorf("the quote is not fenced:\n%s", prompts[1])
	}
}

// TestResume_RetryFailedQuotesNothingForAnUnjudgedFailure pins the gate across
// the process boundary. A node killed by its budget also leaves a reply on disk
// — that file is for a human — but no check faulted it, so the retry leg must
// not tell it a check did. Without runstate.NodeRecord.Judged the two halves of
// one feature would disagree here.
func TestResume_RetryFailedQuotesNothingForAnUnjudgedFailure(t *testing.T) {
	isolateRunHome(t)
	const spec = `name: retry-across-legs-budget
nodes:
  - id: work
    prompt: do the work
    budget_usd: 0.01
`
	rec := &legRecorder{outcomes: map[string]runner.NodeOutcome{
		"do the work": {Result: "AN-EXPENSIVE-REPLY", ExitCode: 0, SessionID: "s-1", TotalCostUSD: 5},
	}}
	runOneFailingLeg(t, "run-retry-legs-budget", spec, rec)

	snap := mustLoadSnapshot(t, "run-retry-legs-budget")
	if snap.Nodes["work"].Judged {
		t.Fatalf("a budget kill was recorded as judged; nothing rendered a verdict on that reply")
	}

	rec.outcomes["do the work"] = runner.NodeOutcome{Result: "cheap", ExitCode: 0, SessionID: "s-2"}
	captureStdout(t, func() {
		if err := executeResume(parseResumeFlags(t, []string{"run-retry-legs-budget", "--retry-failed"}), rec, nil); err != nil {
			t.Fatalf("executeResume --retry-failed: %v", err)
		}
	})

	prompts := rec.promptsOf("do the work")
	if len(prompts) != 2 {
		t.Fatalf("work ran %d time(s) across both legs, want 2", len(prompts))
	}
	if prompts[1] != "do the work" {
		t.Errorf("the retry leg quoted a reply no check ever faulted:\n%s", prompts[1])
	}
}

func mustLoadSnapshot(t *testing.T, runID string) runstate.Snapshot {
	t.Helper()
	snap, err := runstate.Load(runDirFor(runID) + "/" + stateFileName)
	if err != nil {
		t.Fatalf("load snapshot for %s: %v", runID, err)
	}
	return snap
}
