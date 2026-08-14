package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runstate"
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
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 3 {
		t.Fatalf("table is too short to have a header and a rule:\n%s", out.String())
	}
	header, rule := lines[1], lines[2]
	if len(rule) != len(header) {
		t.Errorf("rule is %d chars for a %d-char header:\n%s\n%s", len(rule), len(header), header, rule)
	}
	// The VERDICT cell is sized by the ledger's constant, not by a literal:
	// the header's widest qualifier must fit exactly where VerdictCell writes it.
	if !strings.Contains(header, "VERDICT"+strings.Repeat(" ", ledger.VerdictWidth-len("VERDICT"))+" SESSION") {
		t.Errorf("VERDICT column is not %d wide, so it no longer tracks ledger.VerdictWidth:\n%s", ledger.VerdictWidth, header)
	}
}
