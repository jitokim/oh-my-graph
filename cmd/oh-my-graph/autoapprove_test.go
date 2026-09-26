package main

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// threeLaneGatedGraph is the #285 fixture: three lanes of work, three gates,
// so a run can approve some gates and pause at another in ONE leg. Lanes a and
// b are independent (worker, gate, dependent); the third gate JOINS on the
// first two lanes' dependents plus its own worker. The join is what makes the
// test deterministic: the scheduler evaluates gates concurrently and a pause
// stops every later launch (drain, not cancel), so with three fully
// independent lanes whether ship-a/ship-b get launched before gate-c pauses is
// a race. Behind the join, gate-c cannot be reached until both have run.
// Every test in this file that executes a run uses this fixture, so a
// regression in the default (no flag: pause at the first gate) and a
// regression in the feature (named gates approve) are judged on the same
// material.
const threeLaneGatedGraph = `
name: three-lanes
nodes:
  - { id: work-a, prompt: work-a }
  - { id: gate-a, type: gate, depends_on: [work-a] }
  - { id: ship-a, prompt: ship-a, depends_on: [gate-a] }
  - { id: work-b, prompt: work-b }
  - { id: gate-b, type: gate, depends_on: [work-b] }
  - { id: ship-b, prompt: ship-b, depends_on: [gate-b] }
  - { id: work-c, prompt: work-c }
  - { id: gate-c, type: gate, depends_on: [ship-a, ship-b, work-c] }
  - { id: ship-c, prompt: ship-c, depends_on: [gate-c] }
`

// threeLaneRunner scripts a passing outcome for every spawning node of the
// fixture, keyed by prompt (FakeRunner's default key).
func threeLaneRunner() *runner.FakeRunner {
	outcomes := map[string]runner.NodeOutcome{}
	for _, id := range []string{"work-a", "ship-a", "work-b", "ship-b", "work-c", "ship-c"} {
		outcomes[id] = runner.NodeOutcome{SessionID: "s-" + id, Result: "PASS", ExitCode: 0}
	}
	return runner.NewFakeRunner(outcomes)
}

// invokedPrompts reports which node prompts the fake saw, as a set.
func invokedPrompts(fake *runner.FakeRunner) map[string]bool {
	seen := map[string]bool{}
	for _, inv := range fake.Invocations() {
		seen[inv.Prompt] = true
	}
	return seen
}

// onlyRunID returns the id of the one run directory under $OMG_HOME/runs —
// runGraphWith mints its own run id, so a test finds the snapshot this way.
func onlyRunID(t *testing.T, home string) string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(home, "runs"))
	if err != nil {
		t.Fatalf("read runs root: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("want exactly one run directory, found %d", len(entries))
	}
	return entries[0].Name()
}

// --- flag parsing -------------------------------------------------------------

// --auto-approve is repeatable and order-preserving, and lives on `run` only.
func TestRunFlags_CollectsRepeatedAutoApprove(t *testing.T) {
	f := newRunFlags()
	err := f.parse([]string{"graphs/x.yaml", "--auto-approve", "gate-a", "--auto-approve", "gate-b"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := []string(f.autoApprove); len(got) != 2 || got[0] != "gate-a" || got[1] != "gate-b" {
		t.Errorf("autoApprove = %v, want both ids in order", got)
	}
}

// A blank id is a typo, and a typo that silently approved nothing would read
// exactly like a pre-approval that took.
func TestGateIDFlag_RejectsABlankID(t *testing.T) {
	var f gateIDFlag
	if err := f.Set("  "); err == nil {
		t.Fatal("expected an error for a blank --auto-approve value")
	}
	if len(f) != 0 {
		t.Errorf("autoApprove = %v, want nothing collected from a rejected value", f)
	}
}

// `auto` cannot reach a gate (the coordinator refuses a planned gate node), so
// the flag must not be advertised there: a flag that can never apply is a
// support ticket waiting to happen.
func TestAutoFlags_DoesNotRegisterAutoApprove(t *testing.T) {
	f := newAutoFlags()
	err := f.parse([]string{"write the design doc", "--auto-approve", "gate-a"})
	if err == nil || !strings.Contains(err.Error(), "auto-approve") {
		t.Fatalf("auto accepted --auto-approve (err = %v); it must be a `run`-only flag", err)
	}
}

// --- checkAutoApprove ---------------------------------------------------------

func TestCheckAutoApprove_MissingIDNamesTheGraphsGates(t *testing.T) {
	g := mustParse(t, `{"name":"g","nodes":[
		{"id":"lane-roots-gate","type":"gate"},
		{"id":"lane-qa-gate","type":"gate"},
		{"id":"lane-turn-gate","type":"gate"}]}`)
	err := checkAutoApprove(g, []string{"lane-qa-gaet"})
	want := `run: --auto-approve "lane-qa-gaet": no such node (gate nodes in this graph: lane-qa-gate, lane-roots-gate, lane-turn-gate)`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestCheckAutoApprove_NonGateIDIsRefused(t *testing.T) {
	g := mustParse(t, `{"name":"g","nodes":[
		{"id":"work","prompt":"work"},
		{"id":"approve","type":"gate","depends_on":["work"]}]}`)
	err := checkAutoApprove(g, []string{"work"})
	want := `run: --auto-approve "work": node is not a gate (type claude-run) (gate nodes in this graph: approve)`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestCheckAutoApprove_DuplicateIDIsAcceptedOnce(t *testing.T) {
	g := mustParse(t, `{"name":"g","nodes":[{"id":"approve","type":"gate"}]}`)
	if err := checkAutoApprove(g, []string{"approve", "approve"}); err != nil {
		t.Fatalf("a duplicated id must be accepted, got %v", err)
	}
	decisions := autoApproveDecisions([]string{"approve", "approve"})
	if len(decisions) != 1 {
		t.Fatalf("decisions = %v, want the one gate once", decisions)
	}
}

func TestCheckAutoApprove_GraphWithNoGatesSaysSo(t *testing.T) {
	g := mustParse(t, `{"name":"g","nodes":[{"id":"work","prompt":"work"}]}`)
	err := checkAutoApprove(g, []string{"approve"})
	want := `run: --auto-approve "approve": no such node (this graph has no gate nodes)`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
}

func TestWithLaunchApprovals_AddsUnreachedNamedGatesOnly(t *testing.T) {
	recorded := map[string]gate.Decision{"review": gate.DecisionReject}
	got := withLaunchApprovals(recorded, []string{"review", "release"})
	if got["review"] != gate.DecisionReject {
		t.Errorf("a recorded decision must win over the launch list, got %q", got["review"])
	}
	if got["release"] != gate.DecisionApprove {
		t.Errorf("an unreached named gate must approve, got %q", got["release"])
	}
	if recorded["release"] != "" {
		t.Error("the caller's map was mutated")
	}
	if withLaunchApprovals(nil, nil) != nil {
		t.Error("nothing to add must return the input unchanged")
	}
}

func TestCheckAutoApprove_EmptyListPassesAnyGraph(t *testing.T) {
	g := mustParse(t, `{"name":"g","nodes":[{"id":"work","prompt":"work"}]}`)
	if err := checkAutoApprove(g, nil); err != nil {
		t.Fatalf("no flag must validate against any graph, got %v", err)
	}
	// And the controller a flag-less run injects is the one it always was.
	if _, ok := gateControllerFor(nil).(gate.PauseController); !ok {
		t.Fatalf("gateControllerFor(nil) = %T, want gate.PauseController", gateControllerFor(nil))
	}
	if _, ok := gateControllerFor([]string{"approve"}).(*gate.RecordedController); !ok {
		t.Fatalf("gateControllerFor(named) = %T, want *gate.RecordedController", gateControllerFor([]string{"approve"}))
	}
}

// --- the run itself ------------------------------------------------------------

// TestRunGraphWith_AutoApproveRunsNamedGatesAndPausesAtTheRest is #285's
// whole point, through the real `run` argv path against a FakeRunner: the two
// named gates approve and their dependents run in the first leg, the third
// gate pauses the run exactly as it always has (exit 2), and the record —
// state.json and events.jsonl — says so. Then one ordinary `resume --approve`
// finishes the run and the launch-time list survives the resumed leg's
// whole-snapshot rewrite.
func TestRunGraphWith_AutoApproveRunsNamedGatesAndPausesAtTheRest(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, threeLaneGatedGraph)
	fake := threeLaneRunner()

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path, "--auto-approve", "gate-a", "--auto-approve", "gate-b"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "gate-c" {
		t.Fatalf("expected the run to pause at gate-c, got %T: %v", err, err)
	}
	if code := exitCodeForError(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a paused run", code)
	}
	seen := invokedPrompts(fake)
	for _, id := range []string{"ship-a", "ship-b"} {
		if !seen[id] {
			t.Errorf("%s did not run, but its gate was pre-approved; saw %v", id, seen)
		}
	}
	if seen["ship-c"] {
		t.Error("ship-c ran, but gate-c was not named and must pause")
	}

	runID := onlyRunID(t, home)
	snap := loadSnapshot(t, runID)
	for _, id := range []string{"gate-a", "gate-b"} {
		if snap.Gate.Decisions[id] != runstate.GateApprove {
			t.Errorf("Gate.Decisions[%s] = %q, want approve", id, snap.Gate.Decisions[id])
		}
	}
	// A pause is recorded as a decision of its own (runstate.GatePause), so the
	// assertion is "not approved", not "absent".
	if got := snap.Gate.Decisions["gate-c"]; got != runstate.GatePause {
		t.Errorf("Gate.Decisions[gate-c] = %q, want pause: nobody approved it", got)
	}
	if snap.Gate.PausedAt != "gate-c" {
		t.Errorf("PausedAt = %q, want gate-c", snap.Gate.PausedAt)
	}
	if got := snap.AutoApprove; len(got) != 2 || got[0] != "gate-a" || got[1] != "gate-b" {
		t.Errorf("snapshot auto_approve = %v, want the launch list in argv order", got)
	}

	events := readRunEvents(t, runID)
	for _, id := range []string{"gate-a", "gate-b"} {
		if !eventSeen(events, runfeed.EventGateApproved, id) {
			t.Errorf("events.jsonl has no gate_approved for %s", id)
		}
		if eventSeen(events, runfeed.EventGatePaused, id) {
			t.Errorf("events.jsonl has a gate_paused for pre-approved %s", id)
		}
	}
	if !eventSeen(events, runfeed.EventGatePaused, "gate-c") {
		t.Error("events.jsonl has no gate_paused for gate-c")
	}
	if eventSeen(events, runfeed.EventGateApproved, "gate-c") {
		t.Error("events.jsonl has a gate_approved for gate-c, which nobody approved")
	}

	// The third gate is answered the way every gate was answered before #285.
	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "gate-c"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume --approve gate-c returned error: %v", err)
	}
	if !invokedPrompts(fake)["ship-c"] {
		t.Error("ship-c should have run once gate-c was approved on resume")
	}
	after := loadSnapshot(t, runID)
	if got := after.AutoApprove; len(got) != 2 || got[0] != "gate-a" || got[1] != "gate-b" {
		t.Errorf("the resumed leg erased auto_approve: %v", got)
	}
	if after.Gate.Decisions["gate-c"] != runstate.GateApprove {
		t.Errorf("Gate.Decisions[gate-c] = %q after resume, want approve", after.Gate.Decisions["gate-c"])
	}
}

// TestRunGraphWith_AutoApproveBadIDFailsAtLoad: an id that exists but is not
// a gate, and an id not in the graph, each fail the load with exit 1 — no node
// ran, no run directory exists, and the message names every real gate.
func TestRunGraphWith_AutoApproveBadIDFailsAtLoad(t *testing.T) {
	for _, tc := range []struct{ name, id, wantMsg string }{
		{"not a gate", "work-a", `run: --auto-approve "work-a": node is not a gate (type claude-run) (gate nodes in this graph: gate-a, gate-b, gate-c)`},
		{"no such node", "gate-x", `run: --auto-approve "gate-x": no such node (gate nodes in this graph: gate-a, gate-b, gate-c)`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateRunHome(t)
			path := writeGraphFile(t, threeLaneGatedGraph)
			fake := threeLaneRunner()

			var err error
			captureStdout(t, func() {
				err = runGraphWith([]string{path, "--auto-approve", "gate-a", "--auto-approve", tc.id}, fake, browser.NewFakeOpener(), os.Stdout)
			})
			if err == nil || err.Error() != tc.wantMsg {
				t.Fatalf("err = %v\nwant %s", err, tc.wantMsg)
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d, want 1 for a load error", code)
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("%d nodes ran before the bad id was refused, want 0", n)
			}
			if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
				t.Errorf("a refused load created artifacts under OMG_HOME: %v", entries)
			}
		})
	}
}

// TestRunGraphWith_NoAutoApprovePausesAtFirstGate pins the default on the
// SAME fixture: with no flag a fresh run still pauses at a gate with exit 2,
// no gate's dependents run, and no gate is decided.
func TestRunGraphWith_NoAutoApprovePausesAtFirstGate(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, threeLaneGatedGraph)
	fake := threeLaneRunner()

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || !strings.HasPrefix(paused.GateID, "gate-") {
		t.Fatalf("expected the run to pause at a gate, got %T: %v", err, err)
	}
	if code := exitCodeForError(err); code != 2 {
		t.Errorf("exit code = %d, want 2 for a paused run", code)
	}
	seen := invokedPrompts(fake)
	for _, id := range []string{"ship-a", "ship-b", "ship-c"} {
		if seen[id] {
			t.Errorf("%s ran, but no gate was pre-approved", id)
		}
	}
	snap := loadSnapshot(t, onlyRunID(t, home))
	for id, d := range snap.Gate.Decisions {
		if d == runstate.GateApprove {
			t.Errorf("Gate.Decisions[%s] = approve without --auto-approve", id)
		}
	}
	if snap.AutoApprove != nil {
		t.Errorf("snapshot auto_approve = %v, want absent without the flag", snap.AutoApprove)
	}
}

// TestRunGraphWith_AutoApproveOutlivesTheLegItWasTypedIn: the named gate sits
// BEHIND an unnamed one, so the first leg pauses before ever reaching it. The
// resume that answers the unnamed gate must then walk straight through the
// named one — a launch-time approval holds for the whole run, not for the leg
// that happened to be running when it was typed — and record it exactly as
// the first leg would have.
func TestRunGraphWith_AutoApproveOutlivesTheLegItWasTypedIn(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, `
name: gate-behind-gate
nodes:
  - { id: work, prompt: work }
  - { id: review, type: gate, depends_on: [work] }
  - { id: mid, prompt: mid, depends_on: [review] }
  - { id: release, type: gate, depends_on: [mid] }
  - { id: ship, prompt: ship, depends_on: [release] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"work": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
		"mid":  {SessionID: "s-mid", Result: "PASS", ExitCode: 0},
		"ship": {SessionID: "s-ship", Result: "PASS", ExitCode: 0},
	})

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path, "--auto-approve", "release"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "review" {
		t.Fatalf("expected the first leg to pause at the unnamed gate review, got %T: %v", err, err)
	}
	runID := onlyRunID(t, home)
	if _, reached := loadSnapshot(t, runID).Gate.Decisions["release"]; reached {
		t.Fatal("release was decided in a leg that never reached it")
	}

	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "review"}), fake, nil)
	})
	if err != nil {
		t.Fatalf("resume --approve review returned error: %v (the pre-approved release gate must not pause)", err)
	}
	if !invokedPrompts(fake)["ship"] {
		t.Error("ship did not run: the resumed leg paused at, or skipped, the pre-approved gate")
	}
	snap := loadSnapshot(t, runID)
	if snap.Gate.Decisions["release"] != runstate.GateApprove {
		t.Errorf("Gate.Decisions[release] = %q, want approve recorded by the leg that reached it", snap.Gate.Decisions["release"])
	}
	if !eventSeen(readRunEvents(t, runID), runfeed.EventGateApproved, "release") {
		t.Error("events.jsonl has no gate_approved for release")
	}
}

// --- --dry-run --------------------------------------------------------------------

func TestDryRun_AutoApprovePrintsPreApprovedGates(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, threeLaneGatedGraph)
	fake := threeLaneRunner()

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path, "--dry-run", "--auto-approve", "gate-a", "--auto-approve", "gate-b"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err != nil {
		t.Fatalf("dry run returned error: %v", err)
	}
	if !strings.Contains(out, "Pre-approved gates (--auto-approve): gate-a, gate-b") {
		t.Errorf("plan output does not state the pre-approved gates:\n%s", out)
	}
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("--dry-run invoked %d nodes, want 0", n)
	}
	if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
		t.Errorf("--dry-run created artifacts under OMG_HOME: %v", entries)
	}
}

func TestDryRun_AutoApproveBadIDFailsTheSameWay(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, threeLaneGatedGraph)
	fake := threeLaneRunner()

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path, "--dry-run", "--auto-approve", "gate-x"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	want := `run: --auto-approve "gate-x": no such node (gate nodes in this graph: gate-a, gate-b, gate-c)`
	if err == nil || err.Error() != want {
		t.Fatalf("err = %v\nwant %s", err, want)
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	if entries, readErr := os.ReadDir(home); readErr == nil && len(entries) != 0 {
		t.Errorf("a refused dry run created artifacts under OMG_HOME: %v", entries)
	}
}

// --- exit code through the real subcommand ---------------------------------------

// TestMainExitCode_AutoApprovedGateOnlyGraphMapsToExitCode0 is
// TestMainExitCode_PauseMapsToExitCode2's sibling: the same gate-only graph,
// which never touches the NodeRunner, exits 0 instead of 2 once its one gate
// is pre-approved on the command line.
func TestMainExitCode_AutoApprovedGateOnlyGraphMapsToExitCode0(t *testing.T) {
	isolateRunHome(t)
	graphPath := writeGraphFile(t, "name: gate-only\nnodes:\n  - { id: approve, type: gate }\n")

	var code int
	captureStdout(t, func() {
		code = mainExitCode([]string{"run", graphPath, "--auto-approve", "approve"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0 for a run whose only gate was pre-approved", code)
	}
}
