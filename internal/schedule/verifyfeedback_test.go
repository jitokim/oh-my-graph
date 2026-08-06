package schedule

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// verifyFeedbackYAML is the auto-mode shape after ADR 0016 §2: a check node
// that self-reports `PASS` via result_matches, now ALSO carrying the evidence
// command trusted code attached, with a feedback arc back to the node that
// does the work.
//
// This is the exact configuration the companion clause exists for. The check
// node's result text is the word `PASS` — result_matches passed; the
// VERIFICATION is what failed — so the pre-ADR payload rule ("the declarer's
// result when it produced one") would hand the fixer node the single string
// the engine had just contradicted.
const verifyFeedbackYAML = `
name: build-loop
nodes:
  - { id: impl, prompt: "impl: {{ feedback.check }}" }
  - id: check
    prompt: "check"
    depends_on: [impl]
    success_check:
      result_matches: "^PASS$"
      verify: { command: "./gradlew build" }
    feedback: { rerun: impl, max: 1 }
`

// compilerOutput is deliberately longer than maxDetailRunes: the point of the
// payload bound is that it is NOT the ledger's, and a fixture that fits inside
// 240 runes could not tell the two apart.
func compilerOutput() string {
	var b strings.Builder
	b.WriteString("> Task :compileKotlin FAILED\n")
	for i := 0; i < 40; i++ {
		b.WriteString("e: file:///repo/src/main/kotlin/Widget.kt:")
		b.WriteString(string(rune('0' + i%10)))
		b.WriteString(":5 unresolved reference: renderWidgetPanel\n")
	}
	b.WriteString("BUILD FAILED in 12s\n")
	return b.String()
}

// TestScheduler_FeedbackPayloadIsTheEvidenceNotTheNarration is ADR 0016 §2's
// required companion. Without it the mechanism half-works: the run correctly
// fails, and then pays for a re-run that was told nothing usable.
func TestScheduler_FeedbackPayloadIsTheEvidenceNotTheNarration(t *testing.T) {
	g := mustGraph(t, verifyFeedbackYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl":  result("draft", 0.10),
		"check": result("PASS", 0.20),
	})
	// Keyed by node rather than by prompt: the second impl prompt carries a
	// whole compiler log, which is precisely the thing under test and so must
	// not have to be predicted to be scripted.
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		if strings.HasPrefix(spec.Prompt, "impl:") {
			return "impl"
		}
		return "check"
	}
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"./gradlew build": {ExitCode: 1, Output: compilerOutput()},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	// The loop exhausts its one round (the build fails both times), so the run
	// fails — which is correct, and not what this test is about.
	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail: the build never succeeded")
	}

	var implPrompts []string
	for _, invocation := range fake.Invocations() {
		if strings.HasPrefix(invocation.Prompt, "impl:") {
			implPrompts = append(implPrompts, invocation.Prompt)
		}
	}
	if len(implPrompts) != 2 {
		t.Fatalf("impl ran %d times, want 2 (the initial pass and one feedback round); prompts=%q", len(implPrompts), implPrompts)
	}

	payload := strings.TrimPrefix(implPrompts[1], "impl: ")
	if !strings.Contains(payload, "unresolved reference: renderWidgetPanel") {
		t.Errorf("the re-run was not handed the compiler's own words; payload = %q", payload)
	}
	if !strings.Contains(payload, "./gradlew build") {
		t.Errorf("the payload does not say WHICH command failed; payload = %q", payload)
	}
	// The narration the verification contradicted must not be what the fixer
	// node reads. `PASS` is the declarer's whole result text here, so its
	// absence is exactly the property under test.
	if strings.Contains(payload, "PASS") {
		t.Errorf("the re-run was handed the node's own PASS — the narration the verification just contradicted; payload = %q", payload)
	}
}

// TestScheduler_FeedbackPayloadIsNotBoundedByTheLedgersCap pins the bound
// itself. maxDetailRunes exists to keep an end-of-run TABLE readable; a
// compiler's error list is the payload's whole point, and 240 runes of a build
// log is the last sentence with the errors cut off.
func TestScheduler_FeedbackPayloadIsNotBoundedByTheLedgersCap(t *testing.T) {
	g := mustGraph(t, verifyFeedbackYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl": result("draft", 0.10), "check": result("PASS", 0.20),
	})
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		if strings.HasPrefix(spec.Prompt, "impl:") {
			return "impl"
		}
		return "check"
	}
	output := compilerOutput()
	if utf8.RuneCountInString(output) <= maxDetailRunes {
		t.Fatalf("fixture is only %d runes, so it could not distinguish the two bounds", utf8.RuneCountInString(output))
	}
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		"./gradlew build": {ExitCode: 1, Output: output},
	})
	s, h, led := newVerifyHarness(t, fake, verifier, Options{})

	if err := s.Run(context.Background(), g, h, led); err == nil {
		t.Fatal("expected the run to fail")
	}

	var payload string
	for _, invocation := range fake.Invocations() {
		if strings.HasPrefix(invocation.Prompt, "impl: ") && len(invocation.Prompt) > len("impl: ") {
			payload = strings.TrimPrefix(invocation.Prompt, "impl: ")
		}
	}
	if payload == "" {
		t.Fatal("the feedback round handed the re-run an empty payload")
	}
	if got := utf8.RuneCountInString(payload); got <= maxDetailRunes {
		t.Errorf("payload is %d runes, which fits inside the ledger's %d-rune table bound — the compiler's error list is what the re-run needs, not its last sentence",
			got, maxDetailRunes)
	}

	// And the LEDGER's own bound is untouched: this changes what the model
	// reads, not what the end-of-run table prints. The +1 is capDetail's own
	// one-rune cut marker, which it prepends to the kept tail.
	for _, rec := range recordsFor(led, "check") {
		if got := utf8.RuneCountInString(rec.Detail); got > maxDetailRunes+1 {
			t.Errorf("ledger detail is %d runes, over the %d-rune table bound", got, maxDetailRunes)
		}
	}
}

// TestScheduler_FeedbackPayloadStillPrefersTheNodesOwnWords is the control for
// the case that is NOT a verification: an ordinary reviewing node's prose is
// exactly the feedback a fixing node needs, and the evidence rule must not
// have quietly replaced it with an error string.
func TestScheduler_FeedbackPayloadStillPrefersTheNodesOwnWords(t *testing.T) {
	g := mustGraph(t, feedbackLoopYAML)
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"impl: ":           result("draft-v1", 0.10),
		"review: draft-v1": result("needs work", 0.20),
		"impl: needs work": result("draft-v2", 0.30),
		"review: draft-v2": result("ship it", 0.40),
	})
	s, h, led := newHarness(t, fake, Options{})

	if err := s.Run(context.Background(), g, h, led); err != nil {
		t.Fatalf("run failed: %v", err)
	}
	// The scripted key "impl: needs work" only resolves if the reviewer's own
	// words were the payload; anything else fails the runner loudly.
	if got := fake.InvocationCount("impl: needs work"); got != 1 {
		t.Errorf("the re-run was not handed the reviewer's own words (%d matching invocations)", got)
	}
}
