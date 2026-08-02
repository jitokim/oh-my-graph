package main

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// promptOutcomeRunner scripts one outcome per exact prompt, so a feedback
// round is observable end to end: impl's prompt embeds {{ feedback.review }}
// (empty on the first pass, the reviewer's payload after the arc fires) and
// review's embeds the round's draft via {{ artifacts.impl | inline }}, so
// each round's executions resolve to distinct prompts. A prompt with no
// scripted outcome fails loudly — a wrong or missing payload cannot pass. A
// test rescripts prompts between legs to model work that succeeds on resume.
type promptOutcomeRunner struct {
	mu       sync.Mutex
	outcomes map[string]runner.NodeOutcome
	counts   map[string]int
}

func (r *promptOutcomeRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.counts == nil {
		r.counts = make(map[string]int)
	}
	r.counts[spec.Prompt]++
	outcome, scripted := r.outcomes[spec.Prompt]
	if !scripted {
		return runner.NodeOutcome{}, fmt.Errorf("no scripted outcome for prompt %q", spec.Prompt)
	}
	return outcome, nil
}

func (r *promptOutcomeRunner) set(prompt string, outcome runner.NodeOutcome) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.outcomes[prompt] = outcome
}

func (r *promptOutcomeRunner) count(prompt string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.counts[prompt]
}

func passOutcome(text string, cost float64) runner.NodeOutcome {
	return runner.NodeOutcome{SessionID: "s-" + text, Result: text, TotalCostUSD: cost, ExitCode: 0}
}

// feedbackLoopGraph is the two-node review loop both tests run: review's
// judgment failure re-runs impl with review's findings, at most maxRounds
// times.
func feedbackLoopGraph(t *testing.T, maxRounds int) string {
	t.Helper()
	return fmt.Sprintf(`{"name":"loop","nodes":[
		{"id":"impl","prompt":"impl: {{ feedback.review }}"},
		{"id":"review","prompt":"review: {{ artifacts.impl | inline }}","depends_on":["impl"],
		 "success_check":{"result_matches":"ship it"},
		 "feedback":{"rerun":"impl","max":%d}}]}`, maxRounds)
}

func loadSnapshot(t *testing.T, runID string) runstate.Snapshot {
	t.Helper()
	snap, err := runstate.Load(filepath.Join(runDirFor(runID), stateFileName))
	if err != nil {
		t.Fatalf("load snapshot for %q: %v", runID, err)
	}
	return snap
}

// TestResume_MidLoopMarkerReEntersTheLoop is the mid-loop resume story end to
// end (ADR 0010): leg 1 fires the arc (round 1) and then hits the
// subscription session limit on the re-executed impl, leaving the declarer's
// non-terminal marker in state.json. `resume --retry-failed` must re-enter
// round 1 — impl re-runs WITH the re-seeded feedback payload, review
// re-judges — never restart round 0, and the finished loop's snapshot must
// price each node's rounds cumulatively.
func TestResume_MidLoopMarkerReEntersTheLoop(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, feedbackLoopGraph(t, 2))
	rec := &promptOutcomeRunner{outcomes: map[string]runner.NodeOutcome{
		"impl: ":           passOutcome("draft-v1", 0.25),
		"review: draft-v1": passOutcome("needs work", 0.50),
		"impl: needs work": {SessionLimited: true, FailureCause: "5-hour limit reached"},
	}}
	runID := "run-loop-limit"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "loop.yaml", []byte("name: loop\n"), nil)
	var limited *schedule.LimitPausedError
	if !errors.As(err, &limited) {
		t.Fatalf("expected leg 1 to pause on the session limit, got %T: %v", err, err)
	}

	snap := loadSnapshot(t, runID)
	marker, ok := snap.Nodes["review"]
	if !ok || marker.Verdict != "" || marker.Round != 1 {
		t.Fatalf("review record after leg 1 = %+v, want the non-terminal round-1 marker", marker)
	}
	if impl := snap.Nodes["impl"]; impl.Verdict != runstate.VerdictPass || impl.Round != 0 {
		t.Fatalf("impl record after leg 1 = %+v, want its round-0 PASS (the limited round-1 run is recorded nowhere)", impl)
	}

	// The limit reset; this time the round-1 impl completes and review ships.
	rec.set("impl: needs work", passOutcome("draft-v2", 0.25))
	rec.set("review: draft-v2", passOutcome("ship it", 0.25))

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(out, "running unfinished nodes") {
		t.Fatalf("banner should treat the mid-loop marker as unfinished work, not a failure:\n%s", out)
	}

	if got := rec.count("impl: "); got != 1 {
		t.Errorf("round-0 impl ran %d times, want 1 — resume must re-enter round 1, not restart the loop", got)
	}
	if got := rec.count("impl: needs work"); got != 2 {
		t.Errorf("round-1 impl ran %d times across legs, want 2 (limited, then resumed with the payload)", got)
	}
	if got := rec.count("review: draft-v1"); got != 1 {
		t.Errorf("round-0 review ran %d times, want 1", got)
	}

	final := loadSnapshot(t, runID)
	review := final.Nodes["review"]
	if review.Verdict != runstate.VerdictPass || review.Round != 1 {
		t.Fatalf("final review record = %+v, want PASS at round 1", review)
	}
	if review.CostUSD != 0.75 {
		t.Errorf("final review cost = %v, want 0.75 — the marker's spend must carry into the record that replaces it", review.CostUSD)
	}
	impl := final.Nodes["impl"]
	if impl.Verdict != runstate.VerdictPass || impl.Round != 1 || impl.CostUSD != 0.50 {
		t.Errorf("final impl record = %+v, want PASS at round 1 costing 0.50 (round 0 + round 1)", impl)
	}

	events := readRunEvents(t, runID)
	if got := countEvents(events, runfeed.EventRunStarted, ""); got != 2 {
		t.Errorf("run_started count = %d, want 2 — the resume is its own leg", got)
	}
}

// TestResume_RetryFailedReArmsAnExhaustedLoop pins the salvage rule (ADR
// 0010, "salvage means re-arming the loop, not re-running the declarer
// alone"): clearing an exhausted declarer's FAIL also clears its body's
// retained PASS records and resets the rounds budget, so the retry leg
// re-runs the whole loop from round 0 with empty feedback — not the declarer
// alone against unchanged artifacts.
func TestResume_RetryFailedReArmsAnExhaustedLoop(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, feedbackLoopGraph(t, 1))
	rec := &promptOutcomeRunner{outcomes: map[string]runner.NodeOutcome{
		"impl: ":           passOutcome("draft-v1", 0.25),
		"review: draft-v1": passOutcome("needs work", 0.25),
		"impl: needs work": passOutcome("draft-v2", 0.25),
		"review: draft-v2": passOutcome("still bad", 0.25),
	}}
	runID := "run-loop-exhausted"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "loop.yaml", []byte("name: loop\n"), nil)
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected leg 1 to halt on exhaustion, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "feedback exhausted after 1 round") {
		t.Fatalf("halt error = %v, want the exhaustion detail", err)
	}

	// The human fixed whatever the loop could not; round 0 now ships.
	rec.set("review: draft-v1", passOutcome("ship it", 0.25))

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(out, "retrying failed nodes: impl, review") {
		t.Fatalf("banner should name the whole cleared loop body, not just the declarer:\n%s", out)
	}

	if got := rec.count("impl: "); got != 2 {
		t.Errorf("empty-feedback impl ran %d times across legs, want 2 — the salvaged loop restarts at round 0 with a fresh rounds budget", got)
	}
	if got := rec.count("review: draft-v1"); got != 2 {
		t.Errorf("round-0 review ran %d times across legs, want 2", got)
	}

	final := loadSnapshot(t, runID)
	review := final.Nodes["review"]
	if review.Verdict != runstate.VerdictPass || review.Round != 0 {
		t.Fatalf("final review record = %+v, want PASS at round 0 (fresh rounds)", review)
	}
	if review.CostUSD != 0.25 {
		t.Errorf("final review cost = %v, want 0.25 — a cleared record's spend does not carry, matching --retry-failed's existing rule", review.CostUSD)
	}
	if impl := final.Nodes["impl"]; impl.Verdict != runstate.VerdictPass || impl.Round != 0 {
		t.Errorf("final impl record = %+v, want PASS at round 0", impl)
	}
}

// TestResume_RetryFailedRoundZeroFailureRetriesDeclarerAlone pins the
// body-clearing gate: a declarer that FAILED at round 0 never fired its arc
// (a blown budget_usd here — a non-judgment fault), so the loop never judged
// the body's artifacts and there is nothing to re-arm. `resume --retry-failed`
// must clear and re-run the declarer alone, retaining the body's round-0
// PASSes exactly as it would for a node with no arc at all.
func TestResume_RetryFailedRoundZeroFailureRetriesDeclarerAlone(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"loop","nodes":[
		{"id":"impl","prompt":"impl: {{ feedback.review }}"},
		{"id":"review","prompt":"review: {{ artifacts.impl | inline }}","depends_on":["impl"],
		 "success_check":{"result_matches":"ship it"},
		 "budget_usd":0.10,
		 "feedback":{"rerun":"impl","max":2}}]}`)
	rec := &promptOutcomeRunner{outcomes: map[string]runner.NodeOutcome{
		"impl: ":           passOutcome("draft-v1", 0.25),
		"review: draft-v1": passOutcome("ship it", 5.00), // blows budget_usd at round 0
	}}
	runID := "run-loop-round0"

	err := executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "loop.yaml", []byte("name: loop\n"), nil)
	var halted *schedule.HaltError
	if !errors.As(err, &halted) {
		t.Fatalf("expected leg 1 to halt on the budget fault, got %T: %v", err, err)
	}

	snap := loadSnapshot(t, runID)
	if review := snap.Nodes["review"]; review.Verdict != runstate.VerdictFail || review.Round != 0 {
		t.Fatalf("review record after leg 1 = %+v, want a round-0 FAIL (the arc never fired)", review)
	}

	// The human raised nothing; review simply costs what it should this time.
	rec.set("review: draft-v1", passOutcome("ship it", 0.05))

	var resumeErr error
	out := captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec)
	})
	if resumeErr != nil {
		t.Fatalf("executeResume returned error: %v", resumeErr)
	}
	if !strings.Contains(out, "retrying failed nodes: review)") {
		t.Fatalf("banner should name the declarer alone — the un-fired loop's body is retained:\n%s", out)
	}

	if got := rec.count("impl: "); got != 1 {
		t.Errorf("impl ran %d times across legs, want 1 — a round-0 declarer failure must not clear the body", got)
	}
	if got := rec.count("review: draft-v1"); got != 2 {
		t.Errorf("review ran %d times across legs, want 2 (failed, then retried against the retained artifact)", got)
	}

	final := loadSnapshot(t, runID)
	if review := final.Nodes["review"]; review.Verdict != runstate.VerdictPass || review.Round != 0 {
		t.Errorf("final review record = %+v, want PASS at round 0", review)
	}
	if impl := final.Nodes["impl"]; impl.Verdict != runstate.VerdictPass || impl.Round != 0 {
		t.Errorf("final impl record = %+v, want its retained round-0 PASS", impl)
	}
}
