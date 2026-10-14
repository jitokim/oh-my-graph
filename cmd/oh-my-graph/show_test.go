package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/runstatus"
)

// writeShowSnapshot persists a snapshot into dir/state.json through the real
// writer, so what the show tests read back is exactly what a run leaves on
// disk — same schema stamp, same field encoding.
func writeShowSnapshot(t *testing.T, dir string, snap runstate.Snapshot) {
	t.Helper()
	if err := runstate.Write(filepath.Join(dir, stateFileName), snap); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}
}

// lineFor returns the output line starting with the given node id, so a test
// can assert on one row's fields without other rows (or the dashed separator)
// matching by accident.
func lineFor(t *testing.T, out, nodeID string) string {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, nodeID+" ") {
			return line
		}
	}
	t.Fatalf("no row for node %q in output:\n%s", nodeID, out)
	return ""
}

func TestShowRun_RendersPerNodeLedgerAndTotal(t *testing.T) {
	dir := t.TempDir()
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "20260730-000101",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"alpha","prompt":"a"},{"id":"beta","prompt":"b","depends_on":["alpha"]}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"beta": {
				Verdict:  runstate.VerdictFail,
				CostUSD:  0.5327,
				Duration: 750 * time.Millisecond,
				Detail:   "verify failed: exit 1",
			},
			"alpha": {
				Verdict:   runstate.VerdictPass,
				SessionID: "0b7a4f6e-9c1d-4e2a-8b3f-5d6c7e8f9a0b",
				CostUSD:   0.7977,
				Duration:  1500 * time.Millisecond,
			},
		},
	})

	var out strings.Builder
	if err := showRun(&out, dir, "20260730-000101"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	got := out.String()

	// The header carries the RUN's status beside the node count since ADR 0023
	// §2.6. This fixture has no stream at all, so its leg reads closed and its
	// snapshot has a failed node: FAIL. Before the status line, `show` printed
	// no run-level answer whatsoever, which is why re-opening a paused run
	// after the fact told you nothing about it being paused.
	if !strings.Contains(got, "Run 20260730-000101 — FAIL, 2 node(s)") {
		t.Errorf("missing run header with its status:\n%s", got)
	}

	alpha := lineFor(t, got, "alpha")
	for _, want := range []string{"PASS", "0b7a4f6e-9c1d-4e2a-8b3f-5d6c7e8f9a0b", "0.7977", "1.5s"} {
		if !strings.Contains(alpha, want) {
			t.Errorf("alpha row missing %q: %q", want, alpha)
		}
	}
	// The detail view prints the FULL session id — it is the value a user
	// copies out of this screen — unlike the end-of-run table's trimmed stub.
	if strings.Contains(alpha, "…") {
		t.Errorf("alpha row truncated the session id: %q", alpha)
	}

	beta := lineFor(t, got, "beta")
	for _, want := range []string{"FAIL", "0.5327", "750ms", "verify failed: exit 1"} {
		if !strings.Contains(beta, want) {
			t.Errorf("beta row missing %q: %q", want, beta)
		}
	}
	// beta never got a session, which renders as "-" like the ledger table.
	if !strings.Contains(beta, " - ") {
		t.Errorf("beta row does not render its empty session as \"-\": %q", beta)
	}

	// Rows are sorted by node id regardless of map iteration order.
	if strings.Index(got, "\nalpha ") > strings.Index(got, "\nbeta ") {
		t.Errorf("rows not sorted by node id:\n%s", got)
	}

	if !strings.Contains(got, "TOTAL COST: $1.3304") {
		t.Errorf("total must sum the per-node costs (want $1.3304):\n%s", got)
	}
}

func TestShowRun_UnknownCostIsNeverRenderedAsZero(t *testing.T) {
	dir := t.TempDir()
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "codex-run",
		Graph: json.RawMessage(`{"name":"codex","nodes":[{"id":"work","prompt":"work"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"work": {
				Verdict: runstate.VerdictPass, CostUnknown: true,
				Usage: runstate.TokenUsage{InputTokens: 11, CachedInputTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 3},
			},
		},
	})

	var out strings.Builder
	if err := showRun(&out, dir, "codex-run"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	got := out.String()
	if strings.Contains(got, "TOTAL COST: $0.0000") || !strings.Contains(got, "TOTAL COST: unknown") {
		t.Errorf("unknown cost was rendered as a known zero:\n%s", got)
	}
	if !strings.Contains(got, "TOKEN USAGE: input 11, cached 2, output 5, reasoning 3") {
		t.Errorf("token usage missing:\n%s", got)
	}
}

func TestShowRun_RefusedPlanShowsDurablePlannerAccounting(t *testing.T) {
	dir := t.TempDir()
	writeEventFixture(t, dir, "refused-plan", []runfeed.Event{
		{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
		{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed, CostUnknown: true,
			Usage: runfeed.TokenUsage{InputTokens: 5, CachedInputTokens: 1, OutputTokens: 3, ReasoningOutputTokens: 2}},
	})
	var out strings.Builder
	if err := showRun(&out, dir, "refused-plan"); err != nil {
		t.Fatalf("showRun: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "TOTAL COST: unknown") || !strings.Contains(got, "TOKEN USAGE: input 5, cached 1, output 3, reasoning 2") {
		t.Errorf("refused plan accounting was hidden:\n%s", got)
	}
}

func TestShowRun_UnknownRunID(t *testing.T) {
	var out strings.Builder
	err := showRun(&out, filepath.Join(t.TempDir(), "no-such-run"), "no-such-run")
	if err == nil {
		t.Fatal("showRun on a missing run directory must fail")
	}
	if !strings.Contains(err.Error(), `unknown run "no-such-run"`) {
		t.Errorf("error does not name the unknown run id: %v", err)
	}
}

// TestShowRun_ADirectoryWithNoSnapshotStillGetsItsStatus is the boundary the
// unknown-run error moved to in ADR 0023. A run inside its planner call, and a
// refused plan, both hold a run directory with NO state.json — the first for
// the length of the longest wait in the tool, the second permanently — so
// "unknown run" would be a lie about a directory this binary can derive a
// status for. A MISTYPED id still has no directory, and still gets the error
// (TestShowRun_UnknownRunID).
func TestShowRun_ADirectoryWithNoSnapshotStillGetsItsStatus(t *testing.T) {
	for _, tc := range []struct {
		name        string
		events      []runfeed.Event
		lock        string
		rejected    bool
		wantStatus  string
		wantReason  string
		wantMissing string
	}{
		{
			name:       "inside the planner call",
			events:     []runfeed.Event{{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning}},
			lock:       "held",
			wantStatus: "PLANNING",
			wantReason: "planner call",
		},
		{
			name: "a refused plan",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
			},
			lock:       "free",
			rejected:   true,
			wantStatus: "FAIL",
			wantReason: "rejected.json",
		},
		{
			// The other settled-without-a-snapshot shape, and the reason the
			// sentence above is conditional: a leg swept closed before the
			// recorder's first write reads FAIL too, and there is no rejected
			// spec in its directory to point at.
			name: "a settled failure that refused no plan",
			events: []runfeed.Event{
				{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning},
				{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomeFailed},
			},
			lock:        "free",
			wantStatus:  "FAIL",
			wantReason:  "no node ever settled",
			wantMissing: "rejected.json",
		},
		{
			name:       "a planner call whose process died",
			events:     []runfeed.Event{{Type: runfeed.EventRunStarted, Phase: runfeed.PhasePlanning}},
			lock:       "free",
			wantStatus: "ABANDONED",
			wantReason: "run the graph again",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "run-x")
			writeEventFixture(t, dir, "run-x", tc.events)
			if tc.rejected {
				if err := os.WriteFile(filepath.Join(dir, rejectedSpecFileName), []byte(`{"name":"x"}`), 0o600); err != nil {
					t.Fatalf("write rejected spec: %v", err)
				}
			}
			switch tc.lock {
			case "held":
				liveLegLock(t, dir)
			case "free":
				deadLegLock(t, dir)
			}

			var out strings.Builder
			if err := showRun(&out, dir, "run-x"); err != nil {
				t.Fatalf("showRun on a snapshot-less run directory returned error: %v", err)
			}
			got := out.String()
			if !strings.Contains(got, "Run run-x — "+tc.wantStatus) {
				t.Errorf("want the %s status line:\n%s", tc.wantStatus, got)
			}
			if tc.wantMissing != "" && strings.Contains(got, tc.wantMissing) {
				t.Errorf("the explanation names %q, but no such file is in this directory:\n%s", tc.wantMissing, got)
			}
			if !strings.Contains(got, tc.wantReason) {
				t.Errorf("the missing record must be explained (%q):\n%s", tc.wantReason, got)
			}
			// It must not read as damage: a missing snapshot is a fact about the
			// run at these statuses, not a broken directory (ADR 0023 §2.5).
			if strings.Contains(got, "unknown run") {
				t.Errorf("a derivable run directory must not be called unknown:\n%s", got)
			}
		})
	}
}

// TestShowRun_UnderivableStatusIsNamedInTheSharedSentence pins the surface
// half of the shared wording: when the snapshot loads but the status cannot be
// derived, `show` must PRINT runstatus.Unreadable — the same sentence, with the
// same classification, that `runs list --show-skipped` and `watch` print about
// the same directory — and must still print the table, because the snapshot is
// right there. The fixture is a snapshot whose graph bytes do not survive
// graph.Parse (an edge to a node that is not there):
// runstate.Load succeeds (it does not parse the graph), Gather fails on exactly
// that, which is the one shape that reaches this branch.
func TestShowRun_UnderivableStatusIsNamedInTheSharedSentence(t *testing.T) {
	dir := t.TempDir()
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "20260821-000909",
		Graph: json.RawMessage(`{"name":"unparseable","nodes":[{"id":"alpha","prompt":"a","depends_on":["ghost"]}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"alpha": {Verdict: runstate.VerdictPass, SessionID: "s-alpha", CostUSD: 0.25},
		},
	})

	var out strings.Builder
	if err := showRun(&out, dir, "20260821-000909"); err != nil {
		t.Fatalf("showRun must still render what the snapshot holds: %v", err)
	}
	got := out.String()

	warning := lineContaining(t, got, "could not be read in full")
	for _, want := range []string{`run "20260821-000909"`, runstatus.SkipUnreadable.String(), "reconstruct graph"} {
		if !strings.Contains(warning, want) {
			t.Errorf("the warning line is missing %q: %q", want, warning)
		}
	}
	// The old wording said the status could not be derived and nothing else;
	// this surface must not go back to composing its own.
	if strings.Contains(got, "this run's status could not be derived") {
		t.Errorf("`show` is still speaking its own prose about an unreadable run:\n%s", got)
	}
	// The table is the reason `show` keeps the row at all.
	if !strings.Contains(got, "Run 20260821-000909") || !strings.Contains(got, "alpha") {
		t.Errorf("the per-node table must survive an underivable status:\n%s", got)
	}
}

func TestShowRun_CorruptSnapshot(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, stateFileName), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write corrupt snapshot: %v", err)
	}

	var out strings.Builder
	err := showRun(&out, dir, "corrupt")
	if err == nil {
		t.Fatal("showRun on a corrupt snapshot must fail")
	}
	// A snapshot that exists but cannot be decoded is a load failure, not a
	// mistyped run id — the two must not share an error message.
	if strings.Contains(err.Error(), "unknown run") {
		t.Errorf("corrupt snapshot misreported as an unknown run: %v", err)
	}
}

func TestRunShow_ArgumentErrors(t *testing.T) {
	if err := runShow(nil); err == nil || !strings.Contains(err.Error(), "missing run id") {
		t.Errorf("bare `show` must ask for a run id, got: %v", err)
	}
	if err := runShow([]string{"a", "b"}); err == nil || !strings.Contains(err.Error(), `unexpected argument "b"`) {
		t.Errorf("`show` with extra arguments must reject them, got: %v", err)
	}
}

// TestMainExitCode_ShowUnknownRunIsNonZero pins the whole path the user
// actually hits: `oh-my-graph show <bad-id>` dispatches through run()'s
// switch and exits non-zero.
func TestMainExitCode_ShowUnknownRunIsNonZero(t *testing.T) {
	isolateRunHome(t)
	if code := mainExitCode([]string{"show", "nope"}); code != 1 {
		t.Errorf("show of an unknown run must exit 1, got %d", code)
	}
}

// TestMainExitCode_ShowMissingRunIDMapsToExitCode1 is the regression guard
// for the OTHER pre-existing error #200 must leave alone: a bare `show`, with
// no positional at all, must still exit 1 on its unchanged "missing run id"
// message — the guard ahead of it only ever answers a help token or an
// unknown flag, never an empty argv.
func TestMainExitCode_ShowMissingRunIDMapsToExitCode1(t *testing.T) {
	isolateRunHome(t)
	if code := mainExitCode([]string{"show"}); code != 1 {
		t.Errorf("bare `show` must exit 1, got %d", code)
	}
}

// TestRunShow_Help pins #200: before the fix, `show --help` read "--help" as
// the run id and reported `unknown run "--help"` — the answer a mistyped id
// gets, at the one moment the user wanted the flag list instead. Both spelled
// forms must now answer with the synopsis and must never reach showRun.
func TestRunShow_Help(t *testing.T) {
	for _, arg := range []string{"--help", "-h"} {
		err := runShow([]string{arg})
		var usage *usageRequest
		if !errors.As(err, &usage) {
			t.Fatalf("runShow([%q]) = %v (%T), want a *usageRequest", arg, err, err)
		}
		if !strings.Contains(usage.Error(), "oh-my-graph show") {
			t.Errorf("usage.Error() = %q, want it to name `show`'s synopsis", usage.Error())
		}
		if strings.Contains(usage.Error(), "unknown run") {
			t.Errorf("usage.Error() = %q, must not read like an unknown-run error", usage.Error())
		}
	}
}

// TestRunShow_DashPrefixedPositionalIsNotARunID is the guard the other
// direction: an unrecognised flag in the run-id slot is reported as an
// unknown flag, never looked up as a run.
func TestRunShow_DashPrefixedPositionalIsNotARunID(t *testing.T) {
	err := runShow([]string{"--bogus"})
	if err == nil {
		t.Fatal("expected an error for an unknown flag in the run-id slot")
	}
	if !strings.Contains(err.Error(), `unknown flag "--bogus"`) {
		t.Errorf("err = %v, want it to name the unrecognised flag", err)
	}
	if strings.Contains(err.Error(), "unknown run") {
		t.Errorf("err = %v, an unrecognised flag must not read as an unknown run", err)
	}
}

// TestRunShow_ValidDashFreeRunIDStillReachesTheLookup is the regression guard
// for the fix's own stated boundary: a run id that does not begin with "-"
// must still resolve through runDirFor and show exactly the run there,
// completely unaffected by the new guard ahead of it.
func TestRunShow_ValidDashFreeRunIDStillReachesTheLookup(t *testing.T) {
	home := isolateRunHome(t)
	dir := filepath.Join(home, "runs", "20260806-081500.000000000-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir run dir: %v", err)
	}
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "20260806-081500.000000000-1",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"apply","prompt":"a"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"apply": {Verdict: runstate.VerdictPass},
		},
	})

	var runErr error
	captureStdout(t, func() { runErr = runShow([]string{"20260806-081500.000000000-1"}) })
	if runErr != nil {
		t.Fatalf("a valid, dash-free run id must still resolve: %v", runErr)
	}
}

// TestShowRun_QualifiesAPassWithItsProvenance — `show` is the surface someone
// opens to re-read a finished run, which is exactly what #119's reporter would
// have done after the fact. The snapshot carries the qualifier (ADR 0016 §6)
// and this table renders it through the same ledger.VerdictCell the end-of-run
// summary uses, so a self-reported PASS cannot read as a verified one on the
// one screen a user goes back to.
func TestShowRun_QualifiesAPassWithItsProvenance(t *testing.T) {
	dir := t.TempDir()
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "20260806-081500",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"apply","prompt":"a"},{"id":"legacy","prompt":"l"},{"id":"verify","prompt":"v"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"apply":  {Verdict: runstate.VerdictPass, CostUSD: 11.01, Provenance: "self-reported"},
			"verify": {Verdict: runstate.VerdictPass, CostUSD: 0.13, Provenance: "verified"},
			// A row from a snapshot written before the field existed.
			"legacy": {Verdict: runstate.VerdictPass, CostUSD: 0.02},
		},
	})

	var out strings.Builder
	if err := showRun(&out, dir, "20260806-081500"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	got := out.String()

	if apply := lineFor(t, got, "apply"); !strings.Contains(apply, "PASS (self-reported)") {
		t.Errorf("apply row does not qualify its PASS: %q", apply)
	}
	if verify := lineFor(t, got, "verify"); !strings.Contains(verify, "PASS (verified)") {
		t.Errorf("verify row does not qualify its PASS: %q", verify)
	}
	// Absent is not a qualifier: a pre-ADR-0016 row renders exactly as before
	// rather than being rounded up into a claim about what was measured.
	if legacy := lineFor(t, got, "legacy"); strings.Contains(legacy, "PASS (") {
		t.Errorf("a row with no recorded provenance invented one: %q", legacy)
	}
}

// TestShowRun_DividerMatchesTheHeader pins this table's rule to the header it
// underlines, which is the only thing that stops the two from drifting apart
// as columns change width. It also fails if the VERDICT column stops tracking
// ledger.VerdictWidth — the drift that already happened once, when a widened
// qualifier set moved the end-of-run table and left a literal here behind.
func TestShowRun_DividerMatchesTheHeader(t *testing.T) {
	dir := t.TempDir()
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID: "20260806-090000",
		Graph: json.RawMessage(`{"name":"demo","nodes":[{"id":"apply","prompt":"a"}]}`),
		Nodes: map[string]runstate.NodeRecord{
			"apply": {Verdict: runstate.VerdictPass, Provenance: "self-reported"},
		},
	})

	var out strings.Builder
	if err := showRun(&out, dir, "20260806-090000"); err != nil {
		t.Fatalf("showRun returned error: %v", err)
	}
	// The header is found by its first cell rather than its line number: the
	// engine line (#373) sits between it and the run line.
	lines := strings.Split(out.String(), "\n")
	at := -1
	for i, line := range lines {
		if strings.HasPrefix(line, "NODE ") {
			at = i
			break
		}
	}
	if at < 0 || at+1 >= len(lines) {
		t.Fatalf("table is too short to have a header and a rule:\n%s", out.String())
	}
	header, rule := lines[at], lines[at+1]
	if len(rule) != len(header) {
		t.Errorf("rule is %d chars for a %d-char header:\n%s\n%s", len(rule), len(header), header, rule)
	}
	// The VERDICT cell is sized by the ledger's constant, not by a literal:
	// the header's widest qualifier must fit exactly where VerdictCell writes it.
	if !strings.Contains(header, "VERDICT"+strings.Repeat(" ", ledger.VerdictWidth-len("VERDICT"))+" SESSION") {
		t.Errorf("VERDICT column is not %d wide, so it no longer tracks ledger.VerdictWidth:\n%s", ledger.VerdictWidth, header)
	}
}

// showEngineFixture writes a one-node snapshot stamped with engine, plus an
// event stream with one run_started per entry of legs (none when legs is nil),
// and returns what `show` prints for it.
func showEngineFixture(t *testing.T, engine runfeed.Engine, legs []runfeed.Engine) string {
	t.Helper()
	dir := t.TempDir()
	if legs != nil {
		var events []runfeed.Event
		for _, e := range legs {
			events = append(events,
				runfeed.Event{Type: runfeed.EventRunStarted, Engine: e},
				runfeed.Event{Type: runfeed.EventRunFinished, Outcome: runfeed.OutcomePaused})
		}
		writeEventFixture(t, dir, "engine-run", events)
	}
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID:          "engine-run",
		Graph:          json.RawMessage(`{"name":"demo","nodes":[{"id":"apply","prompt":"a"}]}`),
		Nodes:          map[string]runstate.NodeRecord{"apply": {Verdict: runstate.VerdictPass}},
		EngineVersion:  engine.Version,
		EngineCommit:   engine.Commit,
		EngineModified: engine.Modified,
	})
	var out strings.Builder
	if err := showRun(&out, dir, "engine-run"); err != nil {
		t.Fatalf("showRun: %v", err)
	}
	return out.String()
}

// TestShowRun_EngineLine (#373): the build state.json names is one line, its
// version and commit together, ", modified" only for a dirty tree and
// "(commit unknown)" for a build with no VCS info, never the bare version.
func TestShowRun_EngineLine(t *testing.T) {
	for _, tc := range []struct {
		name   string
		engine runfeed.Engine
		want   string
	}{
		{"with a commit", runfeed.Engine{Version: "0.19.0", Commit: "baeae87"},
			"engine: oh-my-graph 0.19.0 (commit baeae87)\n"},
		{"a modified build", runfeed.Engine{Version: "0.19.0", Commit: "baeae87", Modified: true},
			"engine: oh-my-graph 0.19.0 (commit baeae87, modified)\n"},
		{"no VCS info", runfeed.Engine{Version: "0.19.0"},
			"engine: oh-my-graph 0.19.0 (commit unknown)\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := showEngineFixture(t, tc.engine, nil)
			if !strings.Contains(got, tc.want) {
				t.Errorf("want %q in:\n%s", tc.want, got)
			}
			if !tc.engine.Modified && strings.Contains(got, "modified") {
				t.Errorf("a clean build reads as modified:\n%s", got)
			}
			if tc.engine.Commit == "" && lineContaining(t, got, "engine:") == "engine: oh-my-graph "+tc.engine.Version {
				t.Errorf("a build with no commit printed the bare version, which reads as the release binary:\n%s", got)
			}
			if strings.Contains(got, "distinct engines, in first-seen leg order:") {
				t.Errorf("a run with no stream has no legs to break down:\n%s", got)
			}
		})
	}
}

// TestShowRun_OldRunSaysVersionNotRecorded (#373): a pre-#373 snapshot names
// no build, and `show` must say so rather than claim the run for whatever
// binary happens to be reading it.
func TestShowRun_OldRunSaysVersionNotRecorded(t *testing.T) {
	setVersion(t, "9.9.9-sentinel")
	got := showEngineFixture(t, runfeed.Engine{}, []runfeed.Engine{{}, {}})
	if !strings.Contains(got, "engine: version not recorded\n") {
		t.Errorf("an unstamped run must say its version was not recorded:\n%s", got)
	}
	if strings.Contains(got, "9.9.9-sentinel") {
		t.Errorf("an unstamped run was stamped with the reading binary's version:\n%s", got)
	}
	if strings.Contains(got, "distinct engines, in first-seen leg order:") {
		t.Errorf("a stream with no engine fields has nothing to break down:\n%s", got)
	}
}

// TestShowRun_EnginesByLeg (#373): legs run by different builds are each
// named, distinct builds once each, in the order the legs ran; legs that all
// agree — an auto leg's planning and scheduler run_started included — print
// only the one line.
func TestShowRun_EnginesByLeg(t *testing.T) {
	first := runfeed.Engine{Version: "0.18.0", Commit: "aaaaaaa"}
	second := runfeed.Engine{Version: "0.19.0", Commit: "bbbbbbb", Modified: true}

	got := showEngineFixture(t, second, []runfeed.Engine{first, first, second})
	want := "distinct engines, in first-seen leg order:\n" +
		"  engine: oh-my-graph 0.18.0 (commit aaaaaaa)\n" +
		"  engine: oh-my-graph 0.19.0 (commit bbbbbbb, modified)\n"
	if !strings.Contains(got, want) {
		t.Errorf("want both legs' engines in leg order:\n%s", got)
	}
	if !strings.Contains(got, "engine: oh-my-graph 0.19.0 (commit bbbbbbb, modified)\n") {
		t.Errorf("the state.json engine line is missing:\n%s", got)
	}

	agreed := showEngineFixture(t, first, []runfeed.Engine{first, first})
	if strings.Contains(agreed, "distinct engines, in first-seen leg order:") {
		t.Errorf("legs that agree must print only the one engine line:\n%s", agreed)
	}

	// A pre-#373 leg resumed by a stamped build: the old leg is named as
	// unrecorded, not dropped and not credited to the newer build.
	resumed := showEngineFixture(t, first, []runfeed.Engine{{}, first})
	if !strings.Contains(resumed, "distinct engines, in first-seen leg order:\n  engine: version not recorded\n  engine: oh-my-graph 0.18.0 (commit aaaaaaa)\n") {
		t.Errorf("an unstamped leg must be named as such in the breakdown:\n%s", resumed)
	}
}

// TestShowRun_UnreadableStreamDoesNotFailTheEngineLine (#373): the per-leg
// breakdown is extra, so a stream that cannot be read drops it and nothing else.
func TestShowRun_UnreadableStreamDoesNotFailTheEngineLine(t *testing.T) {
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, runfeed.FileName), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeShowSnapshot(t, dir, runstate.Snapshot{
		RunID:         "engine-run",
		Graph:         json.RawMessage(`{"name":"demo","nodes":[{"id":"apply","prompt":"a"}]}`),
		Nodes:         map[string]runstate.NodeRecord{"apply": {Verdict: runstate.VerdictPass}},
		EngineVersion: "0.19.0",
	})
	var out strings.Builder
	if err := showRun(&out, dir, "engine-run"); err != nil {
		t.Fatalf("an unreadable stream must not fail show: %v", err)
	}
	if !strings.Contains(out.String(), "engine: oh-my-graph 0.19.0 (commit unknown)\n") {
		t.Errorf("the engine line is missing:\n%s", out.String())
	}
}

// TestShowRun_VersionWithoutCommitSaysCommitUnknown (#373): a run stamped with
// a version but no commit must not read as that version's release binary.
func TestShowRun_VersionWithoutCommitSaysCommitUnknown(t *testing.T) {
	got := showEngineFixture(t, runfeed.Engine{Version: "0.20.0"}, nil)
	if !strings.Contains(got, "engine: oh-my-graph 0.20.0 (commit unknown)\n") {
		t.Errorf("a version-only stamp must say its commit is unknown:\n%s", got)
	}
	if strings.Contains(got, "engine: oh-my-graph 0.20.0\n") {
		t.Errorf("a version-only stamp printed the bare version:\n%s", got)
	}
}

// TestShowRun_NoEngineVersionIsExactlyNotRecorded (#373): the
// "(commit unknown)" rule is for a stamped version only; a run with no
// engine_version keeps exactly the "version not recorded" line.
func TestShowRun_NoEngineVersionIsExactlyNotRecorded(t *testing.T) {
	got := showEngineFixture(t, runfeed.Engine{}, nil)
	if line := lineContaining(t, got, "engine:"); line != "engine: version not recorded" {
		t.Errorf("engine line = %q, want exactly %q", line, "engine: version not recorded")
	}
	if strings.Contains(got, "commit unknown") {
		t.Errorf("an unstamped run must not claim a version with an unknown commit:\n%s", got)
	}
}

// TestShowRun_DistinctEnginesListLabelsAndDedups (#373): the mixed-legs list
// is distinct builds in first-seen order, so legs A, B, A print A then B under
// a header that says so, and a version-only leg says "(commit unknown)".
func TestShowRun_DistinctEnginesListLabelsAndDedups(t *testing.T) {
	stamped := runfeed.Engine{Version: "0.19.0", Commit: "bbbbbbb"}
	versionOnly := runfeed.Engine{Version: "0.20.0"}

	got := showEngineFixture(t, stamped, []runfeed.Engine{stamped, versionOnly, stamped})
	want := "distinct engines, in first-seen leg order:\n" +
		"  engine: oh-my-graph 0.19.0 (commit bbbbbbb)\n" +
		"  engine: oh-my-graph 0.20.0 (commit unknown)\n" +
		"NODE"
	if !strings.Contains(got, want) {
		t.Errorf("want the distinct engines once each, version-only leg as commit unknown:\n%s", got)
	}
	if strings.Contains(got, "engines by leg:") {
		t.Errorf("the old per-leg header is still printed:\n%s", got)
	}
}
