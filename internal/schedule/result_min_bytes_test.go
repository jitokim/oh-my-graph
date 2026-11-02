package schedule

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// minBytesGraph is one node whose success_check carries a length floor beside
// a result_matches, so a test can tell which of the two decided.
func minBytesGraph(t *testing.T, minBytes int, extra string) *graph.Graph {
	t.Helper()
	return mustGraph(t, fmt.Sprintf(`
name: min-bytes
nodes:
  - id: plan
    prompt: plan
    success_check: { exit_zero: true, result_min_bytes: %d, result_matches: "PLAN" }
%s`, minBytes, extra))
}

// runMinBytes runs g against a single scripted reply and returns the error and
// the plan node's ledger rows.
func runMinBytes(t *testing.T, g *graph.Graph, reply string) ([]ledger.Record, error) {
	t.Helper()
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"plan": {SessionID: "s", Result: reply, ExitCode: 0},
	})
	s, h, led := newHarness(t, fake, Options{})
	err := s.Run(context.Background(), g, h, led)
	return recordsFor(led, "plan"), err
}

// TestResultMinBytes_ShortResultFailsWithSizes is the predicate itself: a
// result under the floor FAILs, and the detail names the field, the result's
// byte length and the minimum — never the reply's own bytes.
func TestResultMinBytes_ShortResultFailsWithSizes(t *testing.T) {
	reply := "PLAN: tiny"
	rows, err := runMinBytes(t, minBytesGraph(t, 50, ""), reply)

	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) {
		t.Fatalf("expected a *NodeCheckError, got %T: %v", err, err)
	}
	if checkErr.Predicate != "result_min_bytes" {
		t.Errorf("predicate = %q, want result_min_bytes", checkErr.Predicate)
	}
	if len(rows) != 1 || rows[0].Verdict != ledger.VerdictFail {
		t.Fatalf("plan rows = %+v, want one FAIL", rows)
	}
	detail := rows[0].Detail
	for _, want := range []string{"result_min_bytes", fmt.Sprintf("%d bytes", len(reply)), "minimum of 50"} {
		if !strings.Contains(detail, want) {
			t.Errorf("detail = %q, want it to contain %q", detail, want)
		}
	}
	if strings.Contains(detail, reply) {
		t.Errorf("detail = %q quotes the reply's bytes", detail)
	}
}

// TestResultMinBytes_AtOrAboveFloorFallsThroughToResultMatches proves the floor
// is inclusive and is only a gate: a result of exactly the minimum or longer
// moves on, and result_matches still decides the verdict.
func TestResultMinBytes_AtOrAboveFloorFallsThroughToResultMatches(t *testing.T) {
	exact := "PLAN: " + strings.Repeat("x", 14) // 20 bytes
	cases := []struct {
		name      string
		reply     string
		wantPass  bool
		predicate string
	}{
		{name: "equal length, matches", reply: exact, wantPass: true},
		{name: "equal length, no match", reply: strings.Repeat("y", 20), predicate: "result_matches"},
		{name: "longer, matches", reply: exact + " and more", wantPass: true},
		{name: "longer, no match", reply: strings.Repeat("y", 40), predicate: "result_matches"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if len(tc.reply) < 20 {
				t.Fatalf("fixture reply is %d bytes, under the floor", len(tc.reply))
			}
			rows, err := runMinBytes(t, minBytesGraph(t, 20, ""), tc.reply)
			if tc.wantPass {
				if err != nil || len(rows) != 1 || rows[0].Verdict != ledger.VerdictPass {
					t.Fatalf("err = %v, rows = %+v; want PASS", err, rows)
				}
				return
			}
			var checkErr *NodeCheckError
			if !errors.As(err, &checkErr) || checkErr.Predicate != tc.predicate {
				t.Fatalf("err = %v, want a %s failure", err, tc.predicate)
			}
		})
	}
}

// TestResultMinBytes_ClassifiedAsResultMismatch pins the token: a too-short
// result shares result_matches' retry cause and feedback trigger.
func TestResultMinBytes_ClassifiedAsResultMismatch(t *testing.T) {
	err := &NodeCheckError{NodeID: "plan", Predicate: "result_min_bytes", Detail: "result is 1 bytes, below the minimum of 2"}
	if got := causeFromCheck(err); got != graph.CauseResultMismatch {
		t.Errorf("causeFromCheck = %q, want %q", got, graph.CauseResultMismatch)
	}
	if !isJudgmentCause(causeFromCheck(err)) {
		t.Error("a too-short result must be a judgment failure, like a result_matches miss")
	}
}

// TestResultMinBytes_RetriesOnResultMismatch proves a too-short result takes a
// result_matches miss's retry path: `on: [result_mismatch]` re-runs it, and the
// retry is handed the short reply as any judged failure's retry is.
func TestResultMinBytes_RetriesOnResultMismatch(t *testing.T) {
	g := minBytesGraph(t, 30, "    retry: { max: 1, on: [result_mismatch] }\n")
	short := "PLAN: see above"
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: short, ExitCode: 0},
		{Result: "PLAN: " + strings.Repeat("step ", 10), ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if len(rec.prompts) != 2 {
		t.Fatalf("attempts = %d, want 2 (the short reply retried once)", len(rec.prompts))
	}
	if !strings.Contains(rec.prompts[1], short) {
		t.Errorf("retry prompt does not quote the short reply:\n%s", rec.prompts[1])
	}
	if strings.Contains(rec.prompts[1], "result_min_bytes") {
		t.Errorf("retry prompt names the check predicate:\n%s", rec.prompts[1])
	}
}

// TestResultMinBytes_FiresFeedbackArc proves a too-short result on a feedback
// declarer fires its arc exactly as a result_matches miss does, handing the
// short reply to the body as {{ feedback.<id> }}.
func TestResultMinBytes_FiresFeedbackArc(t *testing.T) {
	g := mustGraph(t, `
name: loop
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_min_bytes: 20, result_matches: "ship it" }
    feedback: { rerun: impl, max: 1 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("ship it", 0), // matches, but 7 bytes
		"impl: ship it":    result("draft-v2", 0),
		"review: draft-v2": result("ship it, all checks are fine", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	want := []string{"impl: ", "review: draft-v1", "impl: ship it", "review: draft-v2"}
	if got := fake.Calls(); !equalStrings(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}
	reviewRows := recordsFor(led, "review")
	if len(reviewRows) != 2 || !strings.Contains(reviewRows[0].Detail, "result_min_bytes") {
		t.Errorf("review rows = %+v, want the fire row to name result_min_bytes", reviewRows)
	}
}

// TestResultMinBytes_Issue446Reproduction is #446 itself: a closing remark that
// points at "the edits above" matches a PLAN regex, and with a 200-byte floor
// it now FAILs instead of losing the real plan.
func TestResultMinBytes_Issue446Reproduction(t *testing.T) {
	reply := "PLAN: see the edits above.\nEDIT notes/journal/x.md"
	rows, err := runMinBytes(t, minBytesGraph(t, 200, ""), reply)

	var halted *HaltError
	if !errors.As(err, &halted) || halted.NodeID != "plan" {
		t.Fatalf("expected the run to halt at plan, got %T: %v", err, err)
	}
	if len(rows) != 1 || rows[0].Verdict != ledger.VerdictFail {
		t.Fatalf("plan rows = %+v, want one FAIL", rows)
	}
	if want := fmt.Sprintf("result is %d bytes, below the minimum of 200", len(reply)); !strings.Contains(rows[0].Detail, want) {
		t.Errorf("detail = %q, want %q", rows[0].Detail, want)
	}
}

// TestResultMinBytes_PassIsSelfReported proves a PASS judged only by the length
// floor is labelled self-reported, not exit-only: it measured what the node
// said, which is still nothing outside the node.
func TestResultMinBytes_PassIsSelfReported(t *testing.T) {
	node := graph.Node{ID: "plan", SuccessCheck: graph.SuccessCheck{ResultMinBytes: 10}}
	if got := passProvenance(node); got != runfeed.ProvenanceSelfReported {
		t.Errorf("passProvenance = %q, want %q", got, runfeed.ProvenanceSelfReported)
	}
}
