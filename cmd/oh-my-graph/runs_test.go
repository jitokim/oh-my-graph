package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// completedRun executes a small graph to completion under a scripted
// FakeRunner in the isolated $OMG_HOME, so `runs list` tests read state.json
// files produced by the real recorder — never hand-built fixtures that could
// drift from the snapshot contract.
func completedRun(t *testing.T, runID, spec string, outcomes map[string]runner.NodeOutcome) {
	t.Helper()
	g := mustParse(t, spec)
	err := executeGraph(context.Background(), runID, g, runner.NewFakeRunner(outcomes),
		commonRunFlags{inputs: inputFlag{}}, nil, 0, runID+".yaml", []byte("name: "+runID+"\n"), false, nil, nil, nil)
	if err != nil {
		t.Fatalf("fixture run %q should complete cleanly: %v", runID, err)
	}
}

// --- the table: one row per run, newest first, with a total ------------------

func TestListRuns_NewestFirstWithCostsVerdictsAndTotal(t *testing.T) {
	isolateRunHome(t)

	// Older run: two nodes, both pass, $0.30 total.
	completedRun(t, "20250101-000000",
		`{"name":"alpha","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b","depends_on":["a"]}]}`,
		map[string]runner.NodeOutcome{
			"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.10},
			"b": {SessionID: "s-b", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.20},
		})

	// Newer run: its only node fails, halting the run — an expected
	// *HaltError, not a fixture bug.
	g := mustParse(t, `{"name":"beta","nodes":[{"id":"boom","prompt":"boom"}]}`)
	err := executeGraph(context.Background(), "20250102-000000", g,
		runner.NewFakeRunner(map[string]runner.NodeOutcome{
			"boom": {SessionID: "s-boom", Result: "FAIL", ExitCode: 1, TotalCostUSD: 0.05},
		}),
		commonRunFlags{inputs: inputFlag{}}, nil, 0, "beta.yaml", []byte("name: beta\n"), false, nil, nil, nil)
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected the beta fixture run to fail, got %T: %v", err, err)
	}

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if warn.Len() != 0 {
		t.Fatalf("no run should have been skipped, got warnings:\n%s", warn.String())
	}
	got := out.String()

	newer := strings.Index(got, "20250102-000000")
	older := strings.Index(got, "20250101-000000")
	if newer == -1 || older == -1 {
		t.Fatalf("both runs must be listed:\n%s", got)
	}
	if newer > older {
		t.Errorf("runs must be listed newest first:\n%s", got)
	}

	alphaRow := lineContaining(t, got, "20250101-000000")
	for _, want := range []string{"alpha", " 2 ", "0.3000", "PASS"} {
		if !strings.Contains(alphaRow, want) {
			t.Errorf("alpha's row is missing %q: %q", want, alphaRow)
		}
	}
	betaRow := lineContaining(t, got, "20250102-000000")
	for _, want := range []string{"beta", " 1 ", "0.0500", "FAIL"} {
		if !strings.Contains(betaRow, want) {
			t.Errorf("beta's row is missing %q: %q", want, betaRow)
		}
	}
	if !strings.Contains(got, "2 run(s), TOTAL COST: $0.3500") {
		t.Errorf("footer must total the listed runs' costs:\n%s", got)
	}
}

func TestListRuns_UnknownCostUsesTokensInsteadOfZeroDollars(t *testing.T) {
	isolateRunHome(t)
	completedRun(t, "codex-run",
		`{"name":"codex","nodes":[{"id":"work","prompt":"work"}]}`,
		map[string]runner.NodeOutcome{
			"work": {SessionID: "thread-1", Result: "PASS", CostUnknown: true,
				Usage: runner.TokenUsage{InputTokens: 11, CachedInputTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 3}},
		})

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()
	row := lineContaining(t, got, "codex-run")
	if !strings.Contains(row, "unknown") || !strings.Contains(row, "11/2/5/3") {
		t.Errorf("Codex row is missing unknown cost or tokens: %q", row)
	}
	if strings.Contains(got, "TOTAL COST: $0.0000") || !strings.Contains(got, "TOTAL COST: unknown") {
		t.Errorf("unknown total was rendered as a known zero:\n%s", got)
	}
}

// --- a paused run is neither a PASS nor a FAIL -------------------------------

// TestListRuns_APausedRunRendersAsPaused is #163's second defect, end to end
// through the real engine rather than a fixture. A graph whose only node is a
// root gate pauses immediately, touching no runner at all: the run stopped
// exactly as designed and is resumable, and until ADR 0023 `runs list` printed
// FAIL for it — indistinguishable from a run whose work failed.
func TestListRuns_APausedRunRendersAsPaused(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"gated","nodes":[{"id":"approve","type":"gate"}]}`)
	err := executeGraph(context.Background(), "run-paused", g, &capturingRunner{},
		commonRunFlags{inputs: inputFlag{}}, nil, 0, "gated.yaml", []byte("name: gated\n"), false, nil, nil, nil)
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the fixture run to pause, got %T: %v", err, err)
	}

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	got := out.String()
	row := lineContaining(t, got, "run-paused")
	if !strings.Contains(row, "PAUSED") {
		t.Errorf("a paused run must render as PAUSED: %q", row)
	}
	for _, forbidden := range []string{"FAIL", "PASS"} {
		if strings.Contains(row, forbidden) {
			t.Errorf("a paused run must not render as %s: %q", forbidden, row)
		}
	}
	// A PAUSED row is one a reader cannot act on without being told how, which
	// is the same argument that puts a hint under an ABANDONED one.
	if !strings.Contains(got, "oh-my-graph resume run-paused") {
		t.Errorf("a PAUSED row must carry its resume command under the table:\n%s", got)
	}
	// And the header must not still call the column a verdict: PAUSED is not
	// one (ADR 0023 §2.6).
	if !strings.Contains(got, "STATUS") || strings.Contains(got, "VERDICT") {
		t.Errorf("the column header must read STATUS, not VERDICT:\n%s", got)
	}
}

// --- nothing to list ---------------------------------------------------------

func TestListRuns_MissingRunsDirIsNotAnError(t *testing.T) {
	isolateRunHome(t)
	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("a missing runs dir must not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "No runs found.") {
		t.Errorf("expected the no-runs message, got %q", out.String())
	}
}

func TestListRuns_EmptyRunsDirSaysNoRuns(t *testing.T) {
	isolateRunHome(t)
	if err := os.MkdirAll(runsRoot(), 0o755); err != nil {
		t.Fatalf("create empty runs dir: %v", err)
	}
	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if !strings.Contains(out.String(), "No runs found.") {
		t.Errorf("expected the no-runs message, got %q", out.String())
	}
}

// --- a corrupt snapshot is skipped loudly, never hides the rest --------------

func TestListRuns_CorruptSnapshotIsWarnedAndSkipped(t *testing.T) {
	isolateRunHome(t)
	completedRun(t, "run-good",
		`{"name":"good","nodes":[{"id":"a","prompt":"a"}]}`,
		map[string]runner.NodeOutcome{
			"a": {SessionID: "s-a", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.10},
		})

	corruptDir := filepath.Join(runsRoot(), "run-corrupt")
	if err := os.MkdirAll(corruptDir, 0o755); err != nil {
		t.Fatalf("create corrupt run dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(corruptDir, stateFileName), []byte("not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot(), false); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if !strings.Contains(warn.String(), "run-corrupt") {
		t.Errorf("the skipped run must be named in a warning, got %q", warn.String())
	}
	got := out.String()
	if strings.Contains(got, "run-corrupt") {
		t.Errorf("the corrupt run must not appear as a row:\n%s", got)
	}
	if !strings.Contains(got, "run-good") || !strings.Contains(got, "1 run(s), TOTAL COST: $0.1000") {
		t.Errorf("the good run must still be listed and totaled:\n%s", got)
	}
}

// --- an in-flight run renders RUNNING, a settled one PASS/FAIL ---------------

// writeEventFixture writes dir's events.jsonl through the real StreamWriter,
// so the fixture's lines are exactly the bytes a real run emits — never
// hand-built JSON that could drift from the stream contract.
func writeEventFixture(t *testing.T, dir, runID string, events []runfeed.Event) {
	t.Helper()
	w, err := runfeed.NewStreamWriter(filepath.Join(dir, runfeed.FileName), runID)
	if err != nil {
		t.Fatalf("open fixture event stream: %v", err)
	}
	defer w.Close()
	for _, e := range events {
		if err := w.Emit(e); err != nil {
			t.Fatalf("emit fixture event %q: %v", e.Type, err)
		}
	}
}

// TestListRuns_StatusFollowsRunLifecycle walks the STATUS column through every
// shape a run directory can be in. Two rows here are ADR 0023's whole point:
// the gate pause and the session-limit pause used to render FAIL — "a failed,
// paused, or interrupted run all render as FAIL", as the code said in its own
// words — so a shepherd stopped at its approval gate, exit 2 and resumable, was
// listed as a failure. Two more are new shapes the ADR creates: a run inside its
// planner call, and a refused plan that settles with no snapshot at all.
func TestListRuns_StatusFollowsRunLifecycle(t *testing.T) {
	const runID = "run-under-test"
	const twoNode = `{"name":"demo","nodes":[{"id":"a","prompt":"a"},{"id":"b","prompt":"b","depends_on":["a"]}]}`
	const gated = `{"name":"gated","nodes":[{"id":"g","type":"gate"}]}`
	passA := runstate.NodeRecord{Verdict: runstate.VerdictPass, SessionID: "s-a", CostUSD: 0.10}
	passB := runstate.NodeRecord{Verdict: runstate.VerdictPass, SessionID: "s-b", CostUSD: 0.20}
	failA := runstate.NodeRecord{Verdict: runstate.VerdictFail, SessionID: "s-a", CostUSD: 0.05}

	cases := []struct {
		name string
		// events is appended to the fixture's events.jsonl via the real
		// StreamWriter; snapshot (when non-nil) is written via runstate.Write;
		// rawState (when non-nil) is written verbatim as a corrupt state.json.
		events   []runfeed.Event
		snapshot *runstate.Snapshot
		rawState []byte
		// rawEvents (when non-nil) is written verbatim as events.jsonl —
		// hand-built on purpose, because only a hand-built line can carry a
		// schema today's StreamWriter cannot stamp.
		rawEvents []byte
		// wantStatus is the row's STATUS cell; "" means the run must be
		// skipped with a warning instead of listed.
		wantStatus string
	}{
		{
			name: "open leg with no snapshot yet renders RUNNING",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventNodeStarted, NodeID: "a"},
			},
			wantStatus: "RUNNING",
		},
		{
			name: "open leg with a partial snapshot renders RUNNING, not FAIL",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
				{Type: runfeed.EventNodeStarted, NodeID: "b"},
			},
			snapshot: &runstate.Snapshot{
				RunID: runID,
				Graph: json.RawMessage(twoNode),
				Nodes: map[string]runstate.NodeRecord{"a": passA},
			},
			wantStatus: "RUNNING",
		},
		{
			name: "closed passed leg renders PASS",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePassed},
			},
			snapshot: &runstate.Snapshot{
				RunID: runID,
				Graph: json.RawMessage(twoNode),
				Nodes: map[string]runstate.NodeRecord{"a": passA, "b": passB},
			},
			wantStatus: "PASS",
		},
		{
			name: "closed failed leg renders FAIL",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventNodeFailed, NodeID: "a", Verdict: runfeed.VerdictFail},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
			},
			snapshot: &runstate.Snapshot{
				RunID: runID,
				Graph: json.RawMessage(twoNode),
				Nodes: map[string]runstate.NodeRecord{"a": failA},
			},
			wantStatus: "FAIL",
		},
		{
			// THE DEFECT, fixed. This row printed FAIL until ADR 0023.
			name: "a gate pause is PAUSED, not FAIL",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventGatePaused, NodeID: "g"},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
			},
			snapshot: &runstate.Snapshot{
				RunID: runID,
				Graph: json.RawMessage(gated),
				Gate: runstate.GateState{
					PausedAt:  "g",
					Decisions: map[string]runstate.GateDecision{"g": runstate.GatePause},
				},
			},
			wantStatus: "PAUSED",
		},
		{
			// The same value by the other route, and the one the dashboard
			// painted red: ADR 0009's session-limit pause has NO gate block at
			// all, which is why PAUSED is read off the stream's outcome.
			name: "a session-limit pause is PAUSED too, with no gate anywhere",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
				{Type: runfeed.EventNodePassed, NodeID: "a", Verdict: runfeed.VerdictPass},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused},
			},
			snapshot: &runstate.Snapshot{
				RunID: runID,
				Graph: json.RawMessage(twoNode),
				Nodes: map[string]runstate.NodeRecord{"a": passA},
			},
			wantStatus: "PAUSED",
		},
		{
			// #163 itself: through the planner call the directory holds a lock
			// and a one-line stream and nothing else.
			name: "a planner call in progress renders PLANNING",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
			},
			wantStatus: "PLANNING",
		},
		{
			// ADR 0023 §2.1.1's newest cell, and §9's named regression: a
			// settled run with NO snapshot at all must be a FAIL row, not a
			// WARNING+skip. The excuse for a missing snapshot is keyed on the
			// error, not on the status, precisely so this row exists.
			name: "a refused plan is a FAIL row, not a skipped directory",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
			},
			wantStatus: "FAIL",
		},
		{
			name:       "corrupt snapshot with no stream still warns and skips",
			rawState:   []byte("not json"),
			wantStatus: "",
		},
		{
			name:       "a stream schema newer than this binary warns and skips",
			rawEvents:  []byte(`{"schema":99,"ts":"2026-08-01T00:00:00Z","run_id":"run-under-test","event":"run_started"}` + "\n"),
			wantStatus: "",
		},
		{
			name: "corrupt snapshot is broken even under an open leg",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted},
			},
			rawState:   []byte("not json"),
			wantStatus: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, runID)
			if len(tc.events) > 0 {
				writeEventFixture(t, dir, runID, tc.events)
			}
			if tc.rawEvents != nil {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create fixture run dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, runfeed.FileName), tc.rawEvents, 0o644); err != nil {
					t.Fatalf("write raw fixture event stream: %v", err)
				}
			}
			if tc.snapshot != nil {
				if err := runstate.Write(filepath.Join(dir, stateFileName), *tc.snapshot); err != nil {
					t.Fatalf("write fixture snapshot: %v", err)
				}
			}
			if tc.rawState != nil {
				if err := os.MkdirAll(dir, 0o755); err != nil {
					t.Fatalf("create fixture run dir: %v", err)
				}
				if err := os.WriteFile(filepath.Join(dir, stateFileName), tc.rawState, 0o644); err != nil {
					t.Fatalf("write corrupt fixture snapshot: %v", err)
				}
			}

			var out, warn strings.Builder
			if err := listRuns(&out, &warn, root, false); err != nil {
				t.Fatalf("listRuns returned error: %v", err)
			}
			got := out.String()

			if tc.wantStatus == "" {
				if !strings.Contains(warn.String(), runID) {
					t.Errorf("the broken run must be named in a warning, got %q", warn.String())
				}
				if strings.Contains(got, runID) {
					t.Errorf("the broken run must not appear as a row:\n%s", got)
				}
				return
			}
			if warn.Len() != 0 {
				t.Fatalf("no run should have been skipped, got warnings:\n%s", warn.String())
			}
			row := lineContaining(t, got, runID)
			fields := strings.Fields(row)
			if status := fields[len(fields)-1]; status != tc.wantStatus {
				t.Errorf("status = %q, want %q (row %q)", status, tc.wantStatus, row)
			}
			if !strings.Contains(got, "1 run(s)") {
				t.Errorf("the row must count toward the run count:\n%s", got)
			}
		})
	}
}

func TestListRuns_RefusedPlanShowsDurablePlannerAccounting(t *testing.T) {
	root := t.TempDir()
	runID := "refused-codex-plan"
	dir := filepath.Join(root, runID)
	writeEventFixture(t, dir, runID, []runfeed.Event{
		{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed, CostUnknown: true,
			Usage: runfeed.TokenUsage{InputTokens: 5, CachedInputTokens: 1, OutputTokens: 3, ReasoningOutputTokens: 2}},
	})

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, root, false); err != nil {
		t.Fatalf("listRuns: %v", err)
	}
	got := out.String()
	row := lineContaining(t, got, runID)
	if !strings.Contains(row, "unknown") || !strings.Contains(row, "5/1/3/2") || !strings.Contains(got, "TOTAL COST: unknown") {
		t.Errorf("refused plan accounting was hidden:\n%s", got)
	}
}

// --- CLI contract: dispatch and argument errors ------------------------------

func TestRunRuns_MissingSubcommandErrors(t *testing.T) {
	err := runRuns(nil)
	if err == nil || !strings.Contains(err.Error(), "list") {
		t.Fatalf("a bare `runs` must error naming the list subcommand, got %v", err)
	}
}

func TestRunRuns_UnknownSubcommandErrors(t *testing.T) {
	err := runRuns([]string{"purge"})
	if err == nil || !strings.Contains(err.Error(), "purge") {
		t.Fatalf("an unknown runs subcommand must be named in the error, got %v", err)
	}
}

func TestRunRuns_ExtraArgumentErrors(t *testing.T) {
	err := runRuns([]string{"list", "extra"})
	if err == nil || !strings.Contains(err.Error(), "extra") {
		t.Fatalf("a trailing argument must be rejected and named, got %v", err)
	}
}

// TestRunRuns_Help pins #200 for `runs`: before the fix, `runs --help` fell
// straight into the switch's default arm and answered `unknown subcommand
// "--help" (want list)` — milder than its siblings (it at least named the
// alternative) but still not the flag list the user asked for.
func TestRunRuns_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		err := runRuns([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("runRuns([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph runs") {
			t.Errorf("usage.Error() = %q, want it to name `runs`'s synopsis", usage.Error())
		}
		if strings.Contains(usage.Error(), "unknown subcommand") {
			t.Errorf("usage.Error() = %q, must not read like an unknown-subcommand error", usage.Error())
		}
	}
}

// TestRunRuns_DashPrefixedUnknownSubcommandKeepsItsOwnAnswer is the guard
// specific to `runs`: unlike show/watch/lint/init, its positional slot holds
// a SUBCOMMAND NAME, not a value — so the general "dash-prefixed positional
// is a flag" rule must NOT apply here beyond the help token. `runs --purge`
// must keep exactly its pre-fix answer, `unknown subcommand "--purge" (want
// list)`, rather than being reinterpreted as an unrecognised flag.
func TestRunRuns_DashPrefixedUnknownSubcommandKeepsItsOwnAnswer(t *testing.T) {
	err := runRuns([]string{"--purge"})
	if err == nil || !strings.Contains(err.Error(), `unknown subcommand "--purge"`) || !strings.Contains(err.Error(), "want list") {
		t.Fatalf(`runRuns([--purge]) = %v, want the unchanged "unknown subcommand \"--purge\" (want list)" answer`, err)
	}
	var usage *usageRequest
	if errors.As(err, &usage) {
		t.Errorf("a non-help dash-prefixed subcommand must not be answered as help: %v", err)
	}
}

func TestMainExitCode_RunsListMapsToExitCode0(t *testing.T) {
	isolateRunHome(t)
	if code := mainExitCode([]string{"runs", "list"}); code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
}

// lineContaining returns the one line of out containing needle, failing the
// test if it appears on no line.
func lineContaining(t *testing.T, out, needle string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, needle) {
			return line
		}
	}
	t.Fatalf("no line contains %q:\n%s", needle, out)
	return ""
}
