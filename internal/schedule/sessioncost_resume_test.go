package schedule

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
)

// The session-cost correction across legs (#398): a session child launched
// after a resume subtracts its parent's raw figure as persisted in
// state.json, since the in-memory baseline died with the parent's leg.

const sessionResumeGraph = `
name: session-resume
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`

// TestSessionCost_ResumedLegSubtractsThePersistedBaseline: leg 1 persists
// dev's PASS (raw 0.40) and fails e2e; a second scheduler, seeded only from
// the loaded snapshot, books e2e at 0.65 − 0.40.
func TestSessionCost_ResumedLegSubtractsThePersistedBaseline(t *testing.T) {
	g := mustGraph(t, sessionResumeGraph)
	rec, path := snapshotRecorderIn(t)
	leg1 := runner.NewFakeRunner(map[string]runner.NodeOutcome{"dev": pass("s-dev", 0.40)})
	leg1.InjectError("e2e", errors.New("spawn failed"))
	s1, h1, led1 := newHarness(t, leg1, Options{Recorder: rec})
	if err := s1.Run(context.Background(), g, h1, led1); err == nil {
		t.Fatal("leg 1 succeeded, want e2e's spawn error to fail it")
	}

	snap, err := runstate.Load(path)
	if err != nil {
		t.Fatalf("load leg 1 snapshot: %v", err)
	}
	carried := map[string]runstate.NodeRecord{"dev": snap.Nodes["dev"]}
	if r := carried["dev"].ReportedCostUSD; r == nil || !nearly(*r, 0.40) {
		t.Fatalf("persisted dev reported = %v, want 0.40", r)
	}

	led := runResumedSessionLeg(t, carried)
	e2e := mustRecord(t, led, "e2e")
	if e2e.CostUnknown || !nearly(e2e.CostUSD, 0.25) || !nearly(e2e.ReportedCostUSD, 0.65) {
		t.Fatalf("e2e = cost %v (unknown %v) reported %v, want 0.25 / 0.65", e2e.CostUSD, e2e.CostUnknown, e2e.ReportedCostUSD)
	}
}

// TestSessionCost_ResumedLegWithOldParentRecordIsUnknown: a carried PASS with
// no raw figure — a pre-#398 record — gives its session child an unknown cost
// and the note, never its cumulative total.
func TestSessionCost_ResumedLegWithOldParentRecordIsUnknown(t *testing.T) {
	led := runResumedSessionLeg(t, map[string]runstate.NodeRecord{
		"dev": {Verdict: runstate.VerdictPass, SessionID: "s-dev", CostUSD: 0.40},
	})
	e2e := mustRecord(t, led, "e2e")
	if !e2e.CostUnknown || e2e.CostUSD != 0 {
		t.Fatalf("e2e cost = %v (unknown %v), want unknown", e2e.CostUSD, e2e.CostUnknown)
	}
	if !strings.Contains(e2e.Detail, sessionCostUnknownNote) {
		t.Fatalf("e2e detail %q should carry %q", e2e.Detail, sessionCostUnknownNote)
	}
}

// runResumedSessionLeg runs the leg a resume would: dev carried as completed
// and seeded into the handoff, e2e reporting a raw session total of 0.65.
func runResumedSessionLeg(t *testing.T, carried map[string]runstate.NodeRecord) *ledger.RunLedger {
	t.Helper()
	leg := runner.NewFakeRunner(map[string]runner.NodeOutcome{"e2e": pass("s-dev", 0.65)})
	snap := runstate.Snapshot{Nodes: carried}
	s, _, led := newHarness(t, leg, Options{
		CompletedNodes: snap.CompletedNodes(),
		SettledNodes:   snap.SettledNodes(),
		CarriedRecords: carried,
	})
	h := handoff.New(t.TempDir(), nil)
	h.Seed("dev", "", carried["dev"].SessionID)
	if err := s.Run(context.Background(), mustGraph(t, sessionResumeGraph), h, led); err != nil {
		t.Fatalf("resumed leg: %v", err)
	}
	if invs := leg.Invocations(); len(invs) != 1 || invs[0].ResumeSession != "s-dev" {
		t.Fatalf("resumed leg invocations = %+v, want one e2e resuming s-dev", invs)
	}
	return led
}

// TestPersistedReportedCost: the raw figure goes to state.json and the feed
// only beside a session and a known USD amount — a gate or a codex call
// writes no key.
func TestPersistedReportedCost(t *testing.T) {
	cases := []struct {
		name string
		rec  ledger.Record
		want *float64
	}{
		{"session, known", ledger.Record{SessionID: "s", ReportedCostUSD: 0.65}, usdPtr(0.65)},
		{"session, known zero", ledger.Record{SessionID: "s"}, usdPtr(0)},
		{"session, unknown (codex)", ledger.Record{SessionID: "t", ReportedCostUnknown: true}, nil},
		{"no session (gate)", ledger.Record{Verdict: ledger.VerdictPass}, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := persistedReportedCost(tc.rec)
			if (got == nil) != (tc.want == nil) || (got != nil && *got != *tc.want) {
				t.Fatalf("persistedReportedCost = %v, want %v", got, tc.want)
			}
			if node := toNodeRecord(tc.rec, "", 0, false); (node.ReportedCostUSD == nil) != (tc.want == nil) {
				t.Fatalf("toNodeRecord reported = %v, want %v", node.ReportedCostUSD, tc.want)
			}
			if ev := terminalEvent("node_passed", tc.rec, 0, 0); (ev.ReportedCostUSD == nil) != (tc.want == nil) {
				t.Fatalf("terminalEvent reported = %v, want %v", ev.ReportedCostUSD, tc.want)
			}
		})
	}
}

// TestCarriedReportedCosts: only a carried PASS seeds a baseline, and a PASS
// without a raw figure seeds an unknown one.
func TestCarriedReportedCosts(t *testing.T) {
	got := carriedReportedCosts(map[string]runstate.NodeRecord{
		"known":  {Verdict: runstate.VerdictPass, ReportedCostUSD: usdPtr(0.4)},
		"old":    {Verdict: runstate.VerdictPass},
		"failed": {Verdict: runstate.VerdictFail, ReportedCostUSD: usdPtr(0.9)},
		"marker": {Round: 1, ReportedCostUSD: usdPtr(0.9)},
	})
	want := map[string]reportedCost{"known": {usd: 0.4}, "old": {unknown: true}}
	if len(got) != len(want) || got["known"] != want["known"] || got["old"] != want["old"] {
		t.Fatalf("carriedReportedCosts = %+v, want %+v", got, want)
	}
}

func usdPtr(v float64) *float64 { return &v }
