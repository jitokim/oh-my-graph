package coordinator

import (
	"context"
	"flag"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// updatePlannerGoldens regenerates testdata/planner-prompt-*.golden — run
// `go test ./internal/coordinator -run PlannerPromptGoldens -update-planner-goldens`
// and REVIEW the diff: these files are what "a run without --interview sends
// the planner exactly what it sent before" is measured against (ADR 0044 §5
// test 1), so a regenerated golden is a changed planner prompt.
var updatePlannerGoldens = flag.Bool("update-planner-goldens", false, "regenerate testdata/planner-prompt-*.golden")

// The fixed inputs every planner-prompt golden is rendered from.
const (
	goldenGoal          = "add a --json flag to the status command"
	goldenRemaining     = "the status command still prints text when --json is passed"
	goldenStartCommit   = "0123456789abcdef0123456789abcdef01234567"
	goldenVerifyCommand = "make test"
)

var goldenInputKeys = []string{"repo", "branch"}

// fenceNonceInMarker finds the nonce in a fence marker line of the planner
// prompt ("--- previous remaining 1a2b3c (DATA, ...", "--- end validator
// refusals 1a2b3c ---"). The nonce is minted per call, so a golden holds a
// placeholder in its place.
var fenceNonceInMarker = regexp.MustCompile(`(?m)^--- (?:end )?[a-z ]+? ([0-9a-f]{6})(?: |$)`)

// withoutNonces replaces every fence nonce in prompt with <NONCE>, so two
// renderings of the same prompt compare equal byte for byte everywhere else.
func withoutNonces(prompt string) string {
	for _, m := range fenceNonceInMarker.FindAllStringSubmatch(prompt, -1) {
		prompt = strings.ReplaceAll(prompt, m[1], "<NONCE>")
	}
	return prompt
}

// plannerPrompts drives the three planner prompts through FakeRunner — a first
// attempt, the repair attempt after a refused reply, and a cycle-2
// continuation — and returns each invocation as the coordinator built it.
//
// The invocation directory defaults to an empty temp dir, so the reuse menu
// (#338) is absent unless a caller's own WithInvocationDir plants a catalog:
// a golden must not depend on which fragments the checkout running it ships.
func plannerPrompts(t *testing.T, opts ...Option) (first, repair, continuation runner.NodeInvocation) {
	t.Helper()
	ctx := context.Background()
	opts = append([]Option{WithInvocationDir(t.TempDir())}, opts...)

	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	if _, err := New(fake, opts...).Plan(ctx, goldenGoal, goldenInputKeys); err != nil {
		t.Fatalf("first-attempt plan: %v", err)
	}
	first = onlyInvocation(t, fake)

	repairFake, _ := newRepairFake(
		runner.NodeOutcome{Result: toolRefusedSpec},
		runner.NodeOutcome{Result: validSpec},
	)
	if _, err := New(repairFake, opts...).Plan(ctx, goldenGoal, goldenInputKeys); err != nil {
		t.Fatalf("repaired plan: %v", err)
	}
	calls := repairFake.Invocations()
	if len(calls) != 2 {
		t.Fatalf("a repaired plan made %d planner calls, want 2", len(calls))
	}
	repair = calls[1]

	contFake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	if _, err := New(contFake, opts...).plan(ctx, goldenGoal, goldenInputKeys, goldenRemaining); err != nil {
		t.Fatalf("continuation plan: %v", err)
	}
	continuation = onlyInvocation(t, contFake)
	return first, repair, continuation
}

func onlyInvocation(t *testing.T, fake *runner.FakeRunner) runner.NodeInvocation {
	t.Helper()
	calls := fake.Invocations()
	if len(calls) != 1 {
		t.Fatalf("made %d planner calls, want 1", len(calls))
	}
	return calls[0]
}

// checkPlannerGolden compares prompt, nonces replaced, with the named golden.
func checkPlannerGolden(t *testing.T, name, prompt string) {
	t.Helper()
	path := filepath.Join("testdata", "planner-prompt-"+name+".golden")
	got := withoutNonces(prompt)
	if *updatePlannerGoldens {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (regenerate with -update-planner-goldens): %v", err)
	}
	if got != string(want) {
		t.Errorf("the %s planner prompt drifted from %s — regenerate with -update-planner-goldens and REVIEW the diff:\n--- golden\n%s\n--- prompt\n%s",
			name, path, want, got)
	}
}

// ADR 0044 §5 test 1
func TestPlannerPromptGoldens_OffIsByteIdentical(t *testing.T) {
	first, repair, continuation := plannerPrompts(t)
	checkPlannerGolden(t, "first", first.Prompt)
	checkPlannerGolden(t, "repair", repair.Prompt)
	checkPlannerGolden(t, "continuation", continuation.Prompt)
}

// #338: the same three prompts with a one-entry menu offered, so a change to
// the menu block or to where it sits is a reviewed golden diff. The planted
// shape is named read-and-report, so the verdict-pattern advice's pointer at
// the menu entry renders too.
func TestPlannerPromptGoldens_MenuOn(t *testing.T) {
	dir := plantCatalog(t, map[string]string{"read-and-report": admissibleFragment("read-and-report")})
	first, repair, continuation := plannerPrompts(t, WithInvocationDir(dir))
	checkPlannerGolden(t, "first-menu", first.Prompt)
	checkPlannerGolden(t, "repair-menu", repair.Prompt)
	checkPlannerGolden(t, "continuation-menu", continuation.Prompt)
}

// #414: the first-cycle prompt with the start commit known, so the paragraph
// that measures scope from it is a reviewed golden diff.
func TestPlannerPromptGoldens_StartCommit(t *testing.T) {
	first, _, _ := plannerPrompts(t, WithStartCommit(goldenStartCommit))
	checkPlannerGolden(t, "first-start-commit", first.Prompt)
}

// #396: the first-cycle prompt with a --verify-cmd, so the with-command
// final-check paragraph — the single-sink rule among it — is a reviewed golden
// diff. No other golden supplies a command, so none renders that paragraph.
func TestPlannerPromptGoldens_VerifyCommand(t *testing.T) {
	first, _, _ := plannerPrompts(t, WithVerifyCommand(VerifyCommand{Command: goldenVerifyCommand}))
	checkPlannerGolden(t, "first-verify-cmd", first.Prompt)
}

// ADR 0046 §8 test 11: the first-cycle prompt with --forbid-remote-changes, so
// the one planner sentence (§5.2) is a reviewed golden diff. Without the
// option every other golden here is the prompt unchanged.
func TestPlannerPromptGoldens_ForbidRemoteChanges(t *testing.T) {
	first, _, _ := plannerPrompts(t, WithForbidRemoteChanges())
	checkPlannerGolden(t, "first-forbid-remote", first.Prompt)
}

// ADR 0046 §5.2: the cycle-2 continuation prompt with --forbid-remote-changes,
// so the sentence's place in it — where the first-cycle golden puts it, before
// the continuation section — is a reviewed golden diff too.
func TestPlannerPromptGoldens_ForbidRemoteChangesContinuation(t *testing.T) {
	_, _, continuation := plannerPrompts(t, WithForbidRemoteChanges())
	checkPlannerGolden(t, "continuation-forbid-remote", continuation.Prompt)
}

// ADR 0046 §5.2: the sentence reaches the first attempt, its repair and a
// continuation exactly once each, before the continuation section; without
// the option it reaches none of them.
func TestPlannerPrompt_ForbidRemoteChangesSentence(t *testing.T) {
	first, repair, continuation := plannerPrompts(t, WithForbidRemoteChanges())
	for name, call := range map[string]runner.NodeInvocation{"first": first, "repair": repair, "continuation": continuation} {
		if n := strings.Count(call.Prompt, plannerForbidRemoteChangesSentence); n != 1 {
			t.Errorf("%s prompt carries the forbid-remote sentence %d times, want 1", name, n)
		}
	}
	if s, c := strings.Index(continuation.Prompt, plannerForbidRemoteChangesSentence), strings.Index(continuation.Prompt, goldenRemaining); s < 0 || c < 0 || s > c {
		t.Errorf("continuation: sentence at %d, remaining at %d; want the sentence before the continuation section", s, c)
	}
	first, repair, continuation = plannerPrompts(t)
	for name, call := range map[string]runner.NodeInvocation{"first": first, "repair": repair, "continuation": continuation} {
		if strings.Contains(call.Prompt, "forbade remote changes") {
			t.Errorf("%s prompt names forbidden remote changes without the option", name)
		}
	}
}
