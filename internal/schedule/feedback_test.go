package schedule

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// The feedback tests key the FakeRunner on the PROMPT (its default), because
// the prompt is where a feedback round is visible: impl's prompt embeds
// {{ feedback.review }} (empty on the first pass, the reviewer's payload
// after the arc fires) and review's embeds {{ artifacts.impl | inline }}
// (the round's fresh draft), so each round's executions resolve to distinct
// keys and can be scripted — and asserted on — independently. The scripting
// IS the assertion that the payload flowed: a wrong or missing payload
// resolves to a key with no scripted outcome, which fails loudly.
const feedbackLoopYAML = `
name: loop
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    feedback: { rerun: impl, max: 2 }
`

// result is a passing-exit outcome whose Result text drives the next node's
// prompt key (and, for review, the success_check verdict).
func result(text string, cost float64) runner.NodeOutcome {
	return runner.NodeOutcome{SessionID: "s-" + text, Result: text, TotalCostUSD: cost, ExitCode: 0}
}

// recordsFor filters a ledger's rows down to one node id, in recorded order.
func recordsFor(led *ledger.RunLedger, nodeID string) []ledger.Record {
	var out []ledger.Record
	for _, rec := range led.Records() {
		if rec.NodeID == nodeID {
			out = append(out, rec)
		}
	}
	return out
}

// TestScheduler_FeedbackReRunsBodyWithPayload is the happy loop: review's
// judgment failure re-runs impl → review once, handing review's result text
// to impl as {{ feedback.review }} — empty on the first pass, the payload on
// the second — and the run passes when the re-judged round does.
func TestScheduler_FeedbackReRunsBodyWithPayload(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0.10),
		"review: draft-v1": result("needs work", 0.20),
		"impl: needs work": result("draft-v2", 0.30),
		"review: draft-v2": result("ship it", 0.40),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	want := []string{"impl: ", "review: draft-v1", "impl: needs work", "review: draft-v2"}
	if got := fake.Calls(); !equalStrings(got, want) {
		t.Fatalf("call order = %v, want %v", got, want)
	}

	// The ledger prices every execution (ADR 0010): two rows per body node,
	// the round-1 rows saying which round they belong to.
	implRows := recordsFor(led, "impl")
	if len(implRows) != 2 {
		t.Fatalf("impl ledger rows = %d, want 2 (one per execution); rows=%+v", len(implRows), implRows)
	}
	if implRows[0].Detail != "" {
		t.Errorf("impl round-0 detail = %q, want empty (no round note outside a fired round)", implRows[0].Detail)
	}
	if implRows[1].Detail != "feedback round 1/2" {
		t.Errorf("impl round-1 detail = %q, want %q", implRows[1].Detail, "feedback round 1/2")
	}
	reviewRows := recordsFor(led, "review")
	if len(reviewRows) != 2 {
		t.Fatalf("review ledger rows = %d, want 2; rows=%+v", len(reviewRows), reviewRows)
	}
	if reviewRows[0].Verdict != ledger.VerdictFail || !strings.HasPrefix(reviewRows[0].Detail, "feedback round 1/2: ") {
		t.Errorf("review fire row = %+v, want a FAIL row whose detail opens with the fired round", reviewRows[0])
	}
	if reviewRows[1].Verdict != ledger.VerdictPass {
		t.Errorf("review final row verdict = %q, want PASS", reviewRows[1].Verdict)
	}
}

// TestScheduler_FeedbackExhaustionFailsWithSpendDetail proves the bound: after
// max rounds the declarer's next judgment failure is final, and the terminal
// FAIL names the spend ("feedback exhausted after 2 rounds of impl → review")
// wrapped around the underlying cause. The run halts like any other failure.
func TestScheduler_FeedbackExhaustionFailsWithSpendDetail(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("bad-1", 0),
		"impl: bad-1":      result("draft-v2", 0),
		"review: draft-v2": result("bad-2", 0),
		"impl: bad-2":      result("draft-v3", 0),
		"review: draft-v3": result("bad-3", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var halted *HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected *HaltError, got %T: %v", err, err)
	}
	if halted.NodeID != "review" {
		t.Fatalf("halted at %q, want review", halted.NodeID)
	}
	if msg := err.Error(); !strings.Contains(msg, "feedback exhausted after 2 rounds of impl → review") {
		t.Fatalf("halt error = %q, want the exhaustion spend named", msg)
	}
	if !strings.Contains(err.Error(), "result did not match") {
		t.Fatalf("halt error = %q, want the underlying judgment cause preserved", err)
	}

	if got := fake.InvocationCount("impl: "); got != 1 {
		t.Errorf("impl round-0 ran %d times, want 1", got)
	}
	// (1 + max) executions of each body node, then the failure is final.
	if rows := recordsFor(led, "impl"); len(rows) != 3 {
		t.Errorf("impl ledger rows = %d, want 3 (rounds 0..2)", len(rows))
	}
	reviewRows := recordsFor(led, "review")
	if len(reviewRows) != 3 {
		t.Fatalf("review ledger rows = %d, want 3; rows=%+v", len(reviewRows), reviewRows)
	}
	if !strings.HasPrefix(reviewRows[0].Detail, "feedback round 1/2: ") || !strings.HasPrefix(reviewRows[1].Detail, "feedback round 2/2: ") {
		t.Errorf("non-final review rows = %q, %q — each must price its own fired round", reviewRows[0].Detail, reviewRows[1].Detail)
	}
	if !strings.Contains(reviewRows[2].Detail, "feedback exhausted after 2 rounds") {
		t.Errorf("terminal review row detail = %q, want the exhaustion spend", reviewRows[2].Detail)
	}
}

// TestScheduler_FeedbackMaxOneFiresExactlyOnce pins the smallest bound and the
// singular exhaustion wording: max: 1 buys exactly one re-run, and the second
// judgment failure reads "after 1 round", not "1 rounds".
func TestScheduler_FeedbackMaxOneFiresExactlyOnce(t *testing.T) {
	g := mustGraph(t, `
name: loop-once
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    feedback: { rerun: impl, max: 1 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("bad-1", 0),
		"impl: bad-1":      result("draft-v2", 0),
		"review: draft-v2": result("bad-2", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	if err == nil {
		t.Fatal("expected the run to fail once the single round was spent")
	}
	if !strings.Contains(err.Error(), "feedback exhausted after 1 round of impl → review") {
		t.Fatalf("error = %q, want the singular one-round exhaustion detail", err)
	}
	if calls := fake.Calls(); len(calls) != 4 {
		t.Fatalf("total executions = %v, want exactly 4 (two per body node)", calls)
	}
}

// TestScheduler_NonJudgmentCauseNeverFiresTheArc pins the trigger set's
// boundary: a budget failure reaches the same "after retries" decision point
// but is a spend fault, not a judgment — the arc must not fire, the body must
// not re-run, and the node fails final immediately.
func TestScheduler_NonJudgmentCauseNeverFiresTheArc(t *testing.T) {
	g := mustGraph(t, `
name: loop-budget
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: review
    depends_on: [impl]
    budget_usd: 0.10
    feedback: { rerun: impl, max: 2 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ": result("draft-v1", 0),
		"review": result("looks fine", 5.00), // blows budget_usd
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var budgetErr *NodeBudgetError
	if !errors.As(err, &budgetErr) {
		t.Fatalf("expected the budget failure to surface unchanged, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), "feedback") {
		t.Fatalf("error = %q — a non-judgment cause must not be wrapped in feedback wording", err)
	}
	if got := fake.InvocationCount("impl: "); got != 1 {
		t.Errorf("impl ran %d times, want 1 — the body must not re-run for a spend fault", got)
	}
	if got := fake.InvocationCount("review"); got != 1 {
		t.Errorf("review ran %d times, want 1", got)
	}
}

// TestScheduler_VerifyInfrastructureFaultNeverFiresTheArc pins the other side
// of the trigger boundary: an infrastructure fault in the declarer's own
// verify block — here an interpolation typo in the command, which resolves to
// nothing at run time — carries the verify_failed retry cause but rendered no
// verdict on the work, so the arc must not fire, the body must not re-run,
// and nothing is ever spawned for the unresolvable command.
func TestScheduler_VerifyInfrastructureFaultNeverFiresTheArc(t *testing.T) {
	g := mustGraph(t, `
name: loop-verify-typo
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check:
      verify: { command: "check {{ artifacts.impll }}" }
    feedback: { rerun: impl, max: 2 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("looks fine", 0),
	})
	verifier := verify.NewFakeVerifier(nil)
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	err := s.Run(context.Background(), g, h, led)
	var checkErr *NodeCheckError
	if !errors.As(err, &checkErr) || !checkErr.Infrastructure {
		t.Fatalf("expected the infrastructure verify fault to surface unchanged, got %T: %v", err, err)
	}
	if strings.Contains(err.Error(), "feedback") {
		t.Fatalf("error = %q — an infrastructure fault must not be wrapped in feedback wording", err)
	}
	if got := fake.InvocationCount("impl: "); got != 1 {
		t.Errorf("impl ran %d times, want 1 — the body must not re-run for a fault no re-run can repair", got)
	}
	if got := fake.InvocationCount("review: draft-v1"); got != 1 {
		t.Errorf("review ran %d times, want 1", got)
	}
	if calls := verifier.Calls(); len(calls) != 0 {
		t.Errorf("nothing must be spawned for an unresolvable command; verifier ran %v", calls)
	}
}

// TestScheduler_FeedbackFiresOnlyAfterRetries pins the composition order:
// retry answers "try me again" inside one round, and the arc is consulted only
// once the declarer's retries are spent — every round, including the last.
func TestScheduler_FeedbackFiresOnlyAfterRetries(t *testing.T) {
	g := mustGraph(t, `
name: loop-retry
nodes:
  - { id: impl, prompt: "impl: {{ feedback.review }}" }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    retry: { max: 1, on: [result_mismatch] }
    feedback: { rerun: impl, max: 1 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("bad-1", 0),
		"impl: bad-1":      result("draft-v2", 0),
		"review: draft-v2": result("bad-2", 0),
	})
	fake.KeyFn = nodePromptKey
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail after retries and rounds were both spent")
	}
	// Each round runs review twice (attempt + one retry); impl never retries.
	if got := fake.InvocationCount("review: draft-v1"); got != 2 {
		t.Errorf("round-0 review executions = %d, want 2 (retry precedes the arc)", got)
	}
	if got := fake.InvocationCount("review: draft-v2"); got != 2 {
		t.Errorf("round-1 review executions = %d, want 2 (retries reset per execution)", got)
	}
	if got := fake.InvocationCount("impl: bad-1"); got != 1 {
		t.Errorf("impl round-1 executions = %d, want 1", got)
	}
}

// TestScheduler_FeedbackLeavesParallelSiblingUntouched proves the blast radius
// is exactly the body: a sibling branch sharing the loop's root runs once,
// however many rounds the loop takes.
func TestScheduler_FeedbackLeavesParallelSiblingUntouched(t *testing.T) {
	g := mustGraph(t, `
name: loop-sibling
nodes:
  - { id: root, prompt: root }
  - { id: side, prompt: side, depends_on: [root] }
  - { id: impl, prompt: "impl: {{ feedback.review }}", depends_on: [root] }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    feedback: { rerun: impl, max: 2 }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"root":             result("root-out", 0),
		"side":             result("side-out", 0),
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("needs work", 0),
		"impl: needs work": result("draft-v2", 0),
		"review: draft-v2": result("ship it", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := fake.InvocationCount("side"); got != 1 {
		t.Errorf("side ran %d times, want 1 — a fired arc re-arms only its body", got)
	}
	if got := fake.InvocationCount("root"); got != 1 {
		t.Errorf("root ran %d times, want 1 — an out-of-body parent stays satisfied", got)
	}
}

// TestScheduler_ContinueOnFailPrunesExhaustedLoopBranchOnly proves exhaustion
// hands the failure to the existing policy story unchanged: under
// continue-on-fail the exhausted declarer prunes its own subtree while the
// independent branch completes, and the run reports the declarer as THE
// failed node — not its body, whose members all passed their executions.
func TestScheduler_ContinueOnFailPrunesExhaustedLoopBranchOnly(t *testing.T) {
	g := mustGraph(t, `
name: loop-continue
on_fail: continue
nodes:
  - { id: root, prompt: root }
  - { id: side, prompt: side, depends_on: [root] }
  - { id: impl, prompt: "impl: {{ feedback.review }}", depends_on: [root] }
  - id: review
    prompt: "review: {{ artifacts.impl | inline }}"
    depends_on: [impl]
    success_check: { result_matches: "ship it" }
    feedback: { rerun: impl, max: 1 }
  - { id: ship, prompt: ship, depends_on: [review] }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"root":             result("root-out", 0),
		"side":             result("side-out", 0),
		"impl: ":           result("draft-v1", 0),
		"review: draft-v1": result("bad-1", 0),
		"impl: bad-1":      result("draft-v2", 0),
		"review: draft-v2": result("bad-2", 0),
	})
	s, h, led := newHarness(t, fake, Options{})

	err := s.Run(context.Background(), g, h, led)
	var runFailed *RunFailedError
	if !errors.As(err, &runFailed) {
		t.Fatalf("expected *RunFailedError, got %T: %v", err, err)
	}
	if len(runFailed.FailedNodes) != 1 || runFailed.FailedNodes[0] != "review" {
		t.Fatalf("failed nodes = %v, want exactly [review]", runFailed.FailedNodes)
	}
	if got := fake.InvocationCount("side"); got != 1 {
		t.Errorf("side ran %d times, want 1 — the independent branch completes", got)
	}
	if got := fake.InvocationCount("ship"); got != 0 {
		t.Errorf("ship ran %d times, want 0 — the exhausted declarer's subtree is pruned", got)
	}
}

// TestScheduler_FeedbackEventsNarrateRounds asserts the stream contract
// (docs/RUN-FEED.md): the declarer's non-final judgment failure is a
// node_retried — never a node_failed, which stays terminal — carrying the
// round and the re-run detail, and every event a re-execution emits carries
// round k while the initial pass omits the field entirely.
func TestScheduler_FeedbackEventsNarrateRounds(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ": result("draft-v1", 0),
		"review: draft-v1": {
			SessionID: "thread-needs-work", Result: "needs work", CostUnknown: true,
			Usage: runner.TokenUsage{InputTokens: 11, CachedInputTokens: 2, OutputTokens: 5, ReasoningOutputTokens: 3},
		},
		"impl: needs work": result("draft-v2", 0),
		"review: draft-v2": result("ship it", 0),
	})
	feed, path := newEventStream(t, "run-feedback")
	s, h, led := newHarness(t, fake, Options{EventSink: feed})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	events := readEventStream(t, path)
	want := []string{
		"run_started",
		"node_started impl", "node_passed impl",
		"node_started review", "node_retried review",
		"node_started impl", "node_passed impl",
		"node_started review", "node_passed review",
		"run_finished",
	}
	if got := eventTypes(events); !equalStrings(got, want) {
		t.Fatalf("event sequence = %v, want %v", got, want)
	}

	for i, wantRound := range []int{0, 0, 0, 0, 1, 1, 1, 1, 1, 0} {
		if events[i].Round != wantRound {
			t.Errorf("event %d (%s) round = %d, want %d", i, events[i].Type, events[i].Round, wantRound)
		}
	}
	retried := events[4]
	if retried.Detail != "feedback round 1/2: re-running impl → review" {
		t.Errorf("node_retried detail = %q, want the re-run narration", retried.Detail)
	}
	if retried.Retries != 0 {
		t.Errorf("node_retried retries = %d, want 0 — a feedback round is not a same-node retry", retried.Retries)
	}
	if !retried.CostUnknown || retried.Usage.InputTokens != 11 || retried.Usage.ReasoningOutputTokens != 3 {
		t.Errorf("node_retried accounting = unknown:%v usage:%+v, want the completed feedback attempt", retried.CostUnknown, retried.Usage)
	}
}

// historyRecorder captures every RecordNode call in order — unlike
// fakeRecorder, which keeps only the last record per node — so a test can see
// a declarer's non-terminal marker BEFORE the terminal record that replaces
// it.
type historyRecorder struct {
	mu      sync.Mutex
	history []recordedNode
}

type recordedNode struct {
	nodeID string
	rec    runstate.NodeRecord
}

func (r *historyRecorder) RecordNode(nodeID string, rec runstate.NodeRecord) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.history = append(r.history, recordedNode{nodeID: nodeID, rec: rec})
	return nil
}
func (r *historyRecorder) RecordGateDecision(string, runstate.GateDecision) error { return nil }
func (r *historyRecorder) RecordPause(string) error                               { return nil }

func (r *historyRecorder) recordsFor(nodeID string) []runstate.NodeRecord {
	r.mu.Lock()
	defer r.mu.Unlock()
	var out []runstate.NodeRecord
	for _, entry := range r.history {
		if entry.nodeID == nodeID {
			out = append(out, entry.rec)
		}
	}
	return out
}

// TestScheduler_FeedbackWritesMarkerNotFailToSnapshot pins the durability half
// of a fired arc: the declarer's snapshot record mid-loop is a non-terminal
// MARKER — round k, no verdict, the failing execution's spend — never a FAIL
// (which would settle it and collapse the loop on resume), and the terminal
// record that eventually replaces it carries the same round.
func TestScheduler_FeedbackWritesMarkerNotFailToSnapshot(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ": result("draft-v1", 0.10),
		"review: draft-v1": {
			SessionID: "s-needs-work", Result: "needs work", TotalCostUSD: 0.25, CostUnknown: true,
			Usage: runner.TokenUsage{InputTokens: 13, CachedInputTokens: 4, OutputTokens: 6, ReasoningOutputTokens: 2},
		},
		"impl: needs work": result("draft-v2", 0.10),
		"review: draft-v2": result("ship it", 0.40),
	})
	recorder := &historyRecorder{}
	s, h, led := newHarness(t, fake, Options{Recorder: recorder})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}

	reviewRecords := recorder.recordsFor("review")
	if len(reviewRecords) != 2 {
		t.Fatalf("review snapshot records = %d, want 2 (marker, then terminal); got %+v", len(reviewRecords), reviewRecords)
	}
	marker := reviewRecords[0]
	if marker.Verdict != "" || marker.Round != 1 {
		t.Errorf("marker = %+v, want no verdict and round 1", marker)
	}
	if marker.CostUSD != 0.25 {
		t.Errorf("marker cost = %v, want the failing execution's 0.25", marker.CostUSD)
	}
	if !marker.CostUnknown || marker.Usage.InputTokens != 13 || marker.Usage.ReasoningOutputTokens != 2 {
		t.Errorf("marker accounting = unknown:%v usage:%+v, want the failing execution's accounting", marker.CostUnknown, marker.Usage)
	}
	if terminal := reviewRecords[1]; terminal.Verdict != runstate.VerdictPass || terminal.Round != 1 {
		t.Errorf("terminal record = %+v, want PASS at round 1", terminal)
	}

	implRecords := recorder.recordsFor("impl")
	if len(implRecords) != 2 || implRecords[0].Round != 0 || implRecords[1].Round != 1 {
		t.Errorf("impl snapshot rounds = %+v, want round 0 then round 1", implRecords)
	}
}

// failingRecorder refuses RecordNode for one node id — the scripted
// persistence failure the terminality test below needs.
type failingRecorder struct {
	historyRecorder
	failNodeID string
}

func (r *failingRecorder) RecordNode(nodeID string, rec runstate.NodeRecord) error {
	if nodeID == r.failNodeID {
		return errors.New("disk full")
	}
	return r.historyRecorder.RecordNode(nodeID, rec)
}

// TestScheduler_FeedbackMarkerPersistenceFailureIsTerminal pins the
// durability half of the trio from the other side: when the declarer's
// round marker cannot be written, the arc must NOT fire — no node_retried,
// no body re-run — because a paid round without a durable marker would
// replay the whole loop on resume. The run fails terminally on the
// persistence error instead, pricing the failing execution exactly once.
func TestScheduler_FeedbackMarkerPersistenceFailureIsTerminal(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0.10),
		"review: draft-v1": result("needs work", 0.25),
		"impl: needs work": result("draft-v2", 0.10), // must never run
	})
	recorder := &failingRecorder{failNodeID: "review"}
	feed, path := newEventStream(t, "run-marker-fail")
	s, h, led := newHarness(t, fake, Options{Recorder: recorder, EventSink: feed})

	err := s.Run(context.Background(), g, h, led)

	if err == nil || !strings.Contains(err.Error(), `persist feedback marker for node "review"`) {
		t.Fatalf("Run error = %v, want the marker persistence failure", err)
	}
	wantCalls := []string{"impl: ", "review: draft-v1"}
	if got := fake.Calls(); !equalStrings(got, wantCalls) {
		t.Fatalf("call order = %v, want %v — no re-run may start without a durable marker", got, wantCalls)
	}

	events := eventTypes(readEventStream(t, path))
	sawFailed := false
	for _, ev := range events {
		if strings.HasPrefix(ev, "node_retried") {
			t.Errorf("node_retried emitted despite the failed marker write: %v", events)
		}
		if ev == "node_failed review" {
			sawFailed = true
		}
	}
	if !sawFailed {
		t.Errorf("events = %v, want a terminal node_failed for review", events)
	}

	reviewRows := recordsFor(led, "review")
	if len(reviewRows) != 1 || reviewRows[0].Verdict != ledger.VerdictFail {
		t.Fatalf("review ledger rows = %+v, want exactly one FAIL row (no double pricing)", reviewRows)
	}
}

// TestScheduler_SessionLimitMidRoundPausesTheLoop proves the interplay with
// ADR 0009 mid-loop: when a re-executed body node hits the subscription
// session limit, the run pauses exactly as anywhere else — the limited
// execution is recorded nowhere, and the declarer's durable marker means a
// later `resume --retry-failed` re-enters round k instead of restarting or
// abandoning the loop.
func TestScheduler_SessionLimitMidRoundPausesTheLoop(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0.10),
		"review: draft-v1": result("needs work", 0.25),
		"impl: needs work": {SessionLimited: true, FailureCause: "5-hour limit reached"},
	})
	recorder := &historyRecorder{}
	s, h, led := newHarness(t, fake, Options{Recorder: recorder})

	err := s.Run(context.Background(), g, h, led)
	var limited *LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected *LimitPausedError, got %T: %v", err, err)
	}
	if len(limited.NodeIDs) != 1 || limited.NodeIDs[0] != "impl" {
		t.Fatalf("limited nodes = %v, want [impl]", limited.NodeIDs)
	}

	// The marker survives the pause; the limited round-1 impl execution left
	// no record at all (it never really ran), so impl's last record is its
	// round-0 PASS.
	reviewRecords := recorder.recordsFor("review")
	if len(reviewRecords) != 1 || reviewRecords[0].Verdict != "" || reviewRecords[0].Round != 1 {
		t.Fatalf("review records at pause = %+v, want exactly the round-1 marker", reviewRecords)
	}
	implRecords := recorder.recordsFor("impl")
	if len(implRecords) != 1 || implRecords[0].Verdict != runstate.VerdictPass || implRecords[0].Round != 0 {
		t.Fatalf("impl records at pause = %+v, want exactly the round-0 PASS", implRecords)
	}
}

// TestScheduler_ResumedLegReEntersTheLoopMidRound drives Options.NodeRounds
// and the superseded-record drop the resume path performs (see
// cmd/oh-my-graph continueRun): seeded at round 1 with impl's round-0 PASS
// dropped from the carried sets, the leg re-runs exactly the unfinished
// remainder of round 1 — impl with the re-seeded payload, then review — with
// max − 1 rounds left, and stamps round 1 on everything it records.
func TestScheduler_ResumedLegReEntersTheLoopMidRound(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: needs work": result("draft-v2", 0.30),
		"review: draft-v2": result("ship it", 0.40),
	})
	recorder := &historyRecorder{}
	s, h, led := newHarness(t, fake, Options{
		Recorder: recorder,
		// What continueRun seeds after reading a round-1 marker: every body
		// node at round 1, impl's superseded round-0 PASS in neither carried
		// set (so it re-runs), and the persisted payload back in the handoff.
		NodeRounds: map[string]int{"impl": 1, "review": 1},
	})
	if err := h.SetFeedback("review", "needs work"); err != nil {
		t.Fatalf("seed feedback payload: %v", err)
	}

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got, want := fake.Calls(), []string{"impl: needs work", "review: draft-v2"}; !equalStrings(got, want) {
		t.Fatalf("call order = %v, want %v — the leg must finish round 1, not restart round 0", got, want)
	}
	reviewRecords := recorder.recordsFor("review")
	if len(reviewRecords) != 1 || reviewRecords[0].Round != 1 || reviewRecords[0].Verdict != runstate.VerdictPass {
		t.Fatalf("review records = %+v, want one round-1 PASS", reviewRecords)
	}
	if rows := recordsFor(led, "impl"); len(rows) != 1 || rows[0].Detail != "feedback round 1/2" {
		t.Fatalf("impl ledger rows = %+v, want one round-1 row", rows)
	}
}

// TestScheduler_ResumedRoundsCountAgainstMax proves a resumed leg cannot buy
// extra rounds: seeded at round 2 of max 2, the declarer's next judgment
// failure is exhaustion, not a third round.
func TestScheduler_ResumedRoundsCountAgainstMax(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: needs work": result("draft-v3", 0),
		"review: draft-v3": result("still bad", 0),
	})
	s, h, led := newHarness(t, fake, Options{
		NodeRounds: map[string]int{"impl": 2, "review": 2},
	})
	if err := h.SetFeedback("review", "needs work"); err != nil {
		t.Fatalf("seed feedback payload: %v", err)
	}

	err := s.Run(context.Background(), g, h, led)
	if err == nil || !strings.Contains(err.Error(), "feedback exhausted after 2 rounds of impl → review") {
		t.Fatalf("error = %v, want exhaustion at the seeded round", err)
	}
	if got := fake.InvocationCount("impl: needs work"); got != 1 {
		t.Errorf("impl ran %d times, want 1 — no third round exists to fire", got)
	}
}
