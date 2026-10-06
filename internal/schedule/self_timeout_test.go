package schedule

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// TestScheduler_SelfTimeoutQuotesTheDeclaredTimeout (#292): a node with an
// explicit timeout: 7m that quotes {{ self.timeout }} hands the runner a prompt
// carrying 7m0s — in its verify command too, which interpolates like a prompt.
func TestScheduler_SelfTimeoutQuotesTheDeclaredTimeout(t *testing.T) {
	g := mustGraph(t, `
name: self-timeout
nodes:
  - id: work
    prompt: "you have {{ self.timeout }} per attempt"
    timeout: 7m
    success_check:
      verify: { command: "echo {{ self.timeout }}" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{"you have 7m0s per attempt": result("done", 0)})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{"echo 7m0s": {ExitCode: 0}})
	s, h, led := newHarness(t, fake, Options{Verifier: verifier})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := fake.Invocations(); len(got) != 1 || got[0].Prompt != "you have 7m0s per attempt" {
		t.Fatalf("runner saw %+v, want one prompt quoting 7m0s", got)
	}
	if got := verifier.Calls(); len(got) != 1 || got[0].Command != "echo 7m0s" {
		t.Errorf("verifier saw %+v, want one command quoting 7m0s", got)
	}
}

// TestScheduler_SelfTimeoutQuotesTheRunnersDefault (#292): a node with NO
// timeout: receives the bound the runner actually applies to it, taken from
// the runner's own source rather than a literal — never empty, never 0s.
func TestScheduler_SelfTimeoutQuotesTheRunnersDefault(t *testing.T) {
	g := mustGraph(t, `
name: self-timeout-default
nodes:
  - { id: work, prompt: "you have {{ self.timeout }}" }
`)
	applied := runner.EffectiveTimeout(0)
	if applied <= 0 {
		t.Fatalf("runner.EffectiveTimeout(0) = %s; the applied default must be positive", applied)
	}
	want := "you have " + applied.String()
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{want: result("done", 0)})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if got := fake.Invocations(); len(got) != 1 || got[0].Prompt != want {
		t.Fatalf("runner saw %+v, want one prompt %q", got, want)
	}
}

// TestScheduler_SelfTimeoutIsTheSameOnRetriesAndFeedbackReRuns (#292): the
// token is the configured bound, not the time remaining, so a retried attempt
// and a feedback re-run each quote the same full value as the first attempt.
func TestScheduler_SelfTimeoutIsTheSameOnRetriesAndFeedbackReRuns(t *testing.T) {
	g := mustGraph(t, `
name: self-timeout-loop
nodes:
  - id: impl
    prompt: "impl within {{ self.timeout }}"
    timeout: 7m
    success_check: { result_matches: "^DONE" }
    retry: { max: 1, on: [result_mismatch] }
  - id: review
    prompt: "review within {{ self.timeout }}"
    depends_on: [impl]
    timeout: 90m
    success_check: { result_matches: "^CLEAN" }
    feedback: { rerun: impl, max: 1 }
`)
	fake := runner.NewFakeRunner(nil)
	// impl misses its check once per round, so every round retries it; review
	// never passes, so the arc fires once and the run ends failed.
	retried := false
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		key := nodePromptKey(spec)
		if strings.HasPrefix(key, "impl") && strings.Contains(spec.Prompt, RetryQuoteHeader) {
			retried = true
			return key + " (retry)"
		}
		return key
	}
	fake.SetOutcome("impl within 7m0s", result("not yet", 0))
	fake.SetOutcome("impl within 7m0s (retry)", result("DONE", 0))
	fake.SetOutcome("review within 1h30m0s", result("FINDINGS: again", 0))
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail once the arc's one re-run was spent")
	}
	if !retried {
		t.Fatal("impl was never retried; the fixture does not exercise a retry")
	}
	if got := fake.InvocationCount("impl within 7m0s (retry)"); got != 2 {
		t.Errorf("impl retries quoting 7m0s = %d, want 2 (one per round)", got)
	}
	if got := fake.InvocationCount("review within 1h30m0s"); got != 2 {
		t.Errorf("review runs quoting 1h30m0s = %d, want 2 (first pass + feedback re-run)", got)
	}
	for _, inv := range fake.Invocations() {
		base := nodePromptKey(inv)
		if base != "impl within 7m0s" && base != "review within 1h30m0s" {
			t.Errorf("an attempt quoted a different bound: %q", base)
		}
	}
}

// TestScheduler_SelfTimeoutFeedsTheSameSourceAsTheKill (#292): the invocation
// still carries the node's DECLARED timeout, and the runner maps it through
// runner.EffectiveTimeout — the same mapping the rendered value came from.
func TestScheduler_SelfTimeoutFeedsTheSameSourceAsTheKill(t *testing.T) {
	g := mustGraph(t, `
name: self-timeout-kill
nodes:
  - { id: declared, prompt: "a {{ self.timeout }}", timeout: 7m }
  - { id: plain, prompt: "b {{ self.timeout }}" }
`)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"a 7m0s": result("ok", 0),
		"b " + runner.EffectiveTimeout(0).String(): result("ok", 0),
	})
	s, h, led := newHarness(t, fake, Options{})
	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	for _, inv := range fake.Invocations() {
		rendered := strings.SplitN(inv.Prompt, " ", 2)[1]
		if applied := runner.EffectiveTimeout(inv.Timeout).String(); rendered != applied {
			t.Errorf("prompt %q quotes %s, but the runner would apply %s", inv.Prompt, rendered, applied)
		}
	}
}
