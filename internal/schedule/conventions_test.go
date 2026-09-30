package schedule

import (
	"context"
	"io"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/ledger"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// testConventions stands in for a staged conventions.md (ADR 0041). The
// scheduler treats it as an opaque prefix, so its exact shape is the
// conventions package's to test; what matters here is where and when it lands.
const testConventions = "The person who launched this run gave these conventions for every node. Follow them.\n\n" +
	"## Conventions 1/1: style.md\nuse tabs, and write {{ inputs.x }} literally\n\n---\n\n"

// TestConventions_DefaultOffLeavesEveryPromptAlone is ADR 0041 §5 test 1's
// scheduler half: with no conventions, a spawn's prompt is its interpolated
// prompt byte for byte.
func TestConventions_DefaultOffLeavesEveryPromptAlone(t *testing.T) {
	g := mustGraph(t, `
name: off
inputs: [topic]
nodes:
  - id: work
    prompt: "do {{ inputs.topic }}"
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{{Result: "ok", ExitCode: 0}}}
	h := handoff.New(t.TempDir(), map[string]string{"topic": "docs"})
	s := NewScheduler(rec, Options{ProgressWriter: io.Discard})
	if err := s.Run(context.Background(), g, h, ledger.New("test")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.prompts[0] != "do docs" {
		t.Errorf("prompt = %q, want the interpolated prompt untouched", rec.prompts[0])
	}
}

// TestConventions_PrefixesAFreshSpawnAfterInterpolation is test 2 and test 3's
// scheduler half: the prefix comes first, the node's interpolated prompt
// follows unchanged, and the prefix's own {{ }} is never template-processed.
func TestConventions_PrefixesAFreshSpawnAfterInterpolation(t *testing.T) {
	g := mustGraph(t, `
name: on
inputs: [topic, x]
nodes:
  - id: work
    prompt: "do {{ inputs.topic }}"
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{{Result: "ok", ExitCode: 0}}}
	h := handoff.New(t.TempDir(), map[string]string{"topic": "docs", "x": "RESOLVED"})
	s := NewScheduler(rec, Options{ProgressWriter: io.Discard, Conventions: testConventions})
	if err := s.Run(context.Background(), g, h, ledger.New("test")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if want := testConventions + "do docs"; rec.prompts[0] != want {
		t.Errorf("prompt:\n%q\nwant:\n%q", rec.prompts[0], want)
	}
	if strings.Contains(rec.prompts[0], "RESOLVED") {
		t.Error("the conventions text was interpolated; it must reach the node literally")
	}
}

// TestConventions_SessionResumingFirstAttemptIsNotPrefixed is test 4, and
// TestConventions_ColdRetryOfASessionNodeIsPrefixed is its partner, test 5:
// the rule keys on whether THIS spawn resumes a conversation, not on the
// node's handoff mode.
func TestConventions_SessionResumingFirstAttemptIsNotPrefixed(t *testing.T) {
	g := mustGraph(t, `
name: session
nodes:
  - id: parent
    prompt: parent
  - id: child
    prompt: child work
    depends_on: [parent]
    handoff: session
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", SessionID: "s-parent", ExitCode: 0},
		{Result: "PASS", ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{Conventions: testConventions})
	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.HasPrefix(rec.prompts[0], testConventions) {
		t.Errorf("the fresh parent spawn is not prefixed:\n%s", rec.prompts[0])
	}
	if rec.resumes[1] != "s-parent" {
		t.Fatalf("the child should resume its parent, got %q", rec.resumes[1])
	}
	if rec.prompts[1] != "child work" {
		t.Errorf("a session-resuming spawn was prefixed; its parent's turn already holds the conventions:\n%s", rec.prompts[1])
	}
}

func TestConventions_ColdRetryOfASessionNodeIsPrefixed(t *testing.T) {
	g := mustGraph(t, `
name: session-retry
nodes:
  - id: parent
    prompt: parent
  - id: child
    prompt: child work
    depends_on: [parent]
    handoff: session
    success_check: { result_matches: "PASS" }
    retry: { max: 2, on: [result_mismatch] }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", SessionID: "s-parent", ExitCode: 0},
		{Result: "FIRST-WRONG", ExitCode: 0},
		{Result: "SECOND-WRONG", ExitCode: 0},
		{Result: "PASS", ExitCode: 0},
	}}
	s, h, led := newHarness(t, rec, Options{Conventions: testConventions})
	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.prompts) != 4 {
		t.Fatalf("invocations = %d, want 4 (parent + three child attempts)", len(rec.prompts))
	}
	if rec.prompts[1] != "child work" {
		t.Errorf("the session-resuming first attempt was prefixed:\n%s", rec.prompts[1])
	}
	for i := 2; i <= 3; i++ {
		prompt := rec.prompts[i]
		if rec.resumes[i] != "" {
			t.Fatalf("attempt %d resumed %q; a retry starts cold", i, rec.resumes[i])
		}
		if !strings.HasPrefix(prompt, testConventions+"child work") {
			t.Errorf("cold retry %d does not open with the conventions then the node prompt:\n%s", i, prompt)
		}
		// Test 6: exactly once, never stacked across attempts.
		if n := strings.Count(prompt, "gave these conventions"); n != 1 {
			t.Errorf("attempt %d carries the header %d times, want exactly 1", i, n)
		}
	}
	// Test 6's second half: the ADR 0020 quote still follows the node's own
	// prompt, and it quotes the attempt immediately before.
	third := rec.prompts[3]
	if !strings.Contains(third, "SECOND-WRONG") || strings.Contains(third, "FIRST-WRONG") {
		t.Errorf("the third attempt must quote the second attempt only:\n%s", third)
	}
	if strings.Index(third, "child work") > strings.Index(third, "SECOND-WRONG") {
		t.Errorf("the prior-attempt quote must follow the node prompt:\n%s", third)
	}
}

// TestConventions_PriorLegRetryOfASessionNodeIsPrefixed is test 5's cross-leg
// case: a `resume --retry-failed` leg re-runs a session node that failed in an
// earlier process (the startCold path), and its first spawn in this leg is
// cold, so it is prefixed.
func TestConventions_PriorLegRetryOfASessionNodeIsPrefixed(t *testing.T) {
	g := mustGraph(t, `
name: prior-leg
nodes:
  - id: parent
    prompt: parent
  - id: child
    prompt: child work
    depends_on: [parent]
    handoff: session
    success_check: { result_matches: "PASS" }
`)
	rec := &recordingSequenceRunner{outcomes: []runner.NodeOutcome{
		{Result: "PASS", SessionID: "s-parent", ExitCode: 0},
		{Result: "PASS", SessionID: "s-child-2", ExitCode: 0},
	}}
	runDir := t.TempDir()
	h := handoff.New(runDir, nil)
	s := NewScheduler(rec, Options{ProgressWriter: io.Discard, Conventions: testConventions})
	seedPriorLegReply(t, runDir, h, "child", "LEG-ONE-WRONG-ANSWER")

	if err := s.Run(context.Background(), g, h, ledger.New("test")); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rec.resumes[1] != "" {
		t.Fatalf("the prior-leg retry resumed %q; it must start cold", rec.resumes[1])
	}
	if !strings.HasPrefix(rec.prompts[1], testConventions+"child work") {
		t.Errorf("the prior-leg retry is not prefixed:\n%s", rec.prompts[1])
	}
	if !strings.Contains(rec.prompts[1], "LEG-ONE-WRONG-ANSWER") {
		t.Errorf("the prior-leg quote was lost:\n%s", rec.prompts[1])
	}
}
