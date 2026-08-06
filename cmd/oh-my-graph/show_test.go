package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/ledger"
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

	if !strings.Contains(got, "Run 20260730-000101 — 2 node(s)") {
		t.Errorf("missing run header:\n%s", got)
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
