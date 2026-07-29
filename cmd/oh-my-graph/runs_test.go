package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// completedRun executes a small graph to completion under a scripted
// FakeRunner in the current (temp) directory, so `runs list` tests read
// state.json files produced by the real recorder — never hand-built fixtures
// that could drift from the snapshot contract.
func completedRun(t *testing.T, runID, spec string, outcomes map[string]runner.NodeOutcome) {
	t.Helper()
	g := mustParse(t, spec)
	err := executeGraph(context.Background(), runID, g, runner.NewFakeRunner(outcomes),
		commonRunFlags{inputs: inputFlag{}}, nil, 0, runID+".yaml", []byte("name: "+runID+"\n"))
	if err != nil {
		t.Fatalf("fixture run %q should complete cleanly: %v", runID, err)
	}
}

// --- the table: one row per run, newest first, with a total ------------------

func TestListRuns_NewestFirstWithCostsVerdictsAndTotal(t *testing.T) {
	t.Chdir(t.TempDir())

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
		commonRunFlags{inputs: inputFlag{}}, nil, 0, "beta.yaml", []byte("name: beta\n"))
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected the beta fixture run to fail, got %T: %v", err, err)
	}

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot()); err != nil {
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

// --- a paused run is not a PASS ----------------------------------------------

func TestListRuns_PausedRunRendersAsFail(t *testing.T) {
	t.Chdir(t.TempDir())
	// A graph whose only node is a root gate pauses immediately, touching no
	// runner at all — the run is resumable, but it did not pass.
	g := mustParse(t, `{"name":"gated","nodes":[{"id":"approve","type":"gate"}]}`)
	err := executeGraph(context.Background(), "run-paused", g, &capturingRunner{},
		commonRunFlags{inputs: inputFlag{}}, nil, 0, "gated.yaml", []byte("name: gated\n"))
	var paused *schedule.PausedError
	if !errors.As(err, &paused) {
		t.Fatalf("expected the fixture run to pause, got %T: %v", err, err)
	}

	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot()); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	row := lineContaining(t, out.String(), "run-paused")
	if !strings.Contains(row, "FAIL") {
		t.Errorf("a paused run must not render as PASS: %q", row)
	}
}

// --- nothing to list ---------------------------------------------------------

func TestListRuns_MissingRunsDirIsNotAnError(t *testing.T) {
	t.Chdir(t.TempDir())
	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot()); err != nil {
		t.Fatalf("a missing runs dir must not be an error: %v", err)
	}
	if !strings.Contains(out.String(), "No runs found.") {
		t.Errorf("expected the no-runs message, got %q", out.String())
	}
}

func TestListRuns_EmptyRunsDirSaysNoRuns(t *testing.T) {
	t.Chdir(t.TempDir())
	if err := os.MkdirAll(runsRoot(), 0o755); err != nil {
		t.Fatalf("create empty runs dir: %v", err)
	}
	var out, warn strings.Builder
	if err := listRuns(&out, &warn, runsRoot()); err != nil {
		t.Fatalf("listRuns returned error: %v", err)
	}
	if !strings.Contains(out.String(), "No runs found.") {
		t.Errorf("expected the no-runs message, got %q", out.String())
	}
}

// --- a corrupt snapshot is skipped loudly, never hides the rest --------------

func TestListRuns_CorruptSnapshotIsWarnedAndSkipped(t *testing.T) {
	t.Chdir(t.TempDir())
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
	if err := listRuns(&out, &warn, runsRoot()); err != nil {
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

func TestMainExitCode_RunsListMapsToExitCode0(t *testing.T) {
	t.Chdir(t.TempDir())
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
