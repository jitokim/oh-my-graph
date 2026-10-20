package main

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// The session-cost correction across a resume (#398). The parent passes in
// leg 1 and its child fails there; `resume --retry-failed` re-runs the child in
// a second leg with a FRESH FakeRunner, so the only place the parent's raw
// figure can come from is the record state.json persisted. No real spawn.

const sessionCostResumeGraph = `
name: session-cost-resume
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`

// runSessionCostLeg1 runs leg 1: dev passes at a raw $0.40 on session s-dev,
// and e2e fails on a spawn error, so the run halts with dev's record on disk
// and e2e cleared for --retry-failed.
func runSessionCostLeg1(t *testing.T) string {
	t.Helper()
	home := isolateRunHome(t)
	path := writeGraphFile(t, sessionCostResumeGraph)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"dev": {SessionID: "s-dev", Result: "done", TotalCostUSD: 0.40},
	})
	fake.InjectError("e2e", errors.New("spawn failed"))
	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("leg 1 succeeded, want e2e's spawn error to fail it")
	}
	return onlyRunID(t, home)
}

// resumeSessionCostLeg2 re-runs e2e in a new leg whose session total is a raw
// $0.65, and returns the fake so the caller can check what e2e resumed.
func resumeSessionCostLeg2(t *testing.T, runID string) *runner.FakeRunner {
	t.Helper()
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"e2e": {SessionID: "s-dev", Result: "done", TotalCostUSD: 0.65},
	})
	var err error
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume --retry-failed: %v", err)
	}
	invs := fake.Invocations()
	if len(invs) != 1 || invs[0].ResumeSession != "s-dev" {
		t.Fatalf("leg 2 invocations = %+v, want one e2e resuming s-dev", invs)
	}
	return fake
}

func terminalNodeEvent(t *testing.T, runID, nodeID string) runfeed.Event {
	t.Helper()
	var last *runfeed.Event
	for _, e := range readRunEvents(t, runID) {
		if e.NodeID == nodeID && (e.Type == runfeed.EventNodePassed || e.Type == runfeed.EventNodeFailed) {
			last = &e
		}
	}
	if last == nil {
		t.Fatalf("no terminal event for %q", nodeID)
	}
	return *last
}

func nearlyUSD(a, b float64) bool { return math.Abs(a-b) < 1e-9 }

// TestResume_SessionChildSubtractsThePersistedParentRaw: the child that runs
// after the resume is booked rawChild − rawParent = 0.65 − 0.40, with 0.40
// read back out of dev's persisted record.
func TestResume_SessionChildSubtractsThePersistedParentRaw(t *testing.T) {
	runID := runSessionCostLeg1(t)
	dev := loadSnapshot(t, runID).Nodes["dev"]
	if dev.ReportedCostUSD == nil || !nearlyUSD(*dev.ReportedCostUSD, 0.40) {
		t.Fatalf("leg 1 persisted dev reported_cost_usd = %v, want 0.40", dev.ReportedCostUSD)
	}

	resumeSessionCostLeg2(t, runID)

	e2e := loadSnapshot(t, runID).Nodes["e2e"]
	if e2e.Verdict != "PASS" || e2e.CostUnknown || !nearlyUSD(e2e.CostUSD, 0.25) {
		t.Fatalf("e2e record = %+v, want a PASS costing its own 0.25", e2e)
	}
	if e2e.ReportedCostUSD == nil || !nearlyUSD(*e2e.ReportedCostUSD, 0.65) {
		t.Fatalf("e2e reported_cost_usd = %v, want the raw cumulative 0.65", e2e.ReportedCostUSD)
	}
	if strings.Contains(e2e.Detail, "session-cost correction") {
		t.Fatalf("e2e detail %q carries a correction note, want none", e2e.Detail)
	}
	ev := terminalNodeEvent(t, runID, "e2e")
	if ev.CostUnknown || !nearlyUSD(ev.CostUSD, 0.25) || ev.ReportedCostUSD == nil || !nearlyUSD(*ev.ReportedCostUSD, 0.65) {
		t.Fatalf("e2e node_passed = cost %v (unknown %v) reported %v, want 0.25 / 0.65", ev.CostUSD, ev.CostUnknown, ev.ReportedCostUSD)
	}
}

// TestResume_OldFormatParentRecordMakesSessionChildUnknown: a parent record
// written before reported_cost_usd existed has no baseline, so the child's
// cost is recorded unknown with the note — never its cumulative total.
func TestResume_OldFormatParentRecordMakesSessionChildUnknown(t *testing.T) {
	runID := runSessionCostLeg1(t)
	stripReportedCost(t, runID, "dev")

	resumeSessionCostLeg2(t, runID)

	e2e := loadSnapshot(t, runID).Nodes["e2e"]
	if e2e.Verdict != "PASS" || !e2e.CostUnknown || e2e.CostUSD != 0 {
		t.Fatalf("e2e record = %+v, want a PASS whose cost is unknown", e2e)
	}
	if !strings.Contains(e2e.Detail, "session-cost correction not applied: parent's raw cost unknown") {
		t.Fatalf("e2e detail = %q, want the unknown-baseline note", e2e.Detail)
	}
	if ev := terminalNodeEvent(t, runID, "e2e"); !ev.CostUnknown || ev.CostUSD != 0 {
		t.Fatalf("e2e node_passed = cost %v (unknown %v), want unknown", ev.CostUSD, ev.CostUnknown)
	}
}

// stripReportedCost rewrites state.json as an engine from before #398 wrote
// it: nodeID's record without the reported_cost_usd key.
func stripReportedCost(t *testing.T, runID, nodeID string) {
	t.Helper()
	path := filepath.Join(runDirFor(runID), stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	rec := raw["nodes"].(map[string]any)[nodeID].(map[string]any)
	if _, ok := rec["reported_cost_usd"]; !ok {
		t.Fatalf("%s record has no reported_cost_usd to strip", nodeID)
	}
	delete(rec, "reported_cost_usd")
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, out, 0o600); err != nil {
		t.Fatal(err)
	}
}
