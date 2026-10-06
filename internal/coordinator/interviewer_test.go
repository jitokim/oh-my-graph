package coordinator

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/interview"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// renderedInterviewPrefix runs a real interview against a scripted asker and
// returns its rendered prefix, so the prefix under test is the one the
// interview package actually hands the planner — fence, header and all.
func renderedInterviewPrefix(t *testing.T) string {
	t.Helper()
	replies := []string{"QUESTION: Should --json replace the text output or sit beside it?", "ENOUGH"}
	ask := func(context.Context, string) (string, interview.Accounting, error) {
		reply := replies[0]
		replies = replies[1:]
		return reply, interview.Accounting{}, nil
	}
	result, err := interview.Run(context.Background(), goldenGoal, ask,
		strings.NewReader("beside it; text stays the default\n"), io.Discard)
	if err != nil {
		t.Fatalf("interview: %v", err)
	}
	prefix, err := result.Render()
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if prefix == "" {
		t.Fatal("an answered interview rendered an empty prefix")
	}
	return prefix
}

// ADR 0044 §5 test 11
func TestInterviewPrefix_ChangesThePlannerInvocationInPromptAlone(t *testing.T) {
	prefix := renderedInterviewPrefix(t)
	plainFirst, plainRepair, plainCont := plannerPrompts(t)
	withFirst, withRepair, withCont := plannerPrompts(t, WithInterviewPrefix(prefix))

	for _, pair := range []struct {
		name        string
		plain, with runner.NodeInvocation
	}{
		{"first attempt", plainFirst, withFirst},
		{"repair attempt", plainRepair, withRepair},
		{"cycle-2 continuation", plainCont, withCont},
	} {
		if pair.with.Prompt == pair.plain.Prompt {
			t.Errorf("%s: the interview prefix did not reach the planner prompt", pair.name)
		}
		if !reflect.DeepEqual(pair.with.Policy, pair.plain.Policy) {
			t.Errorf("%s: tool policy changed with the prefix:\nwith    %+v\nwithout %+v", pair.name, pair.with.Policy, pair.plain.Policy)
		}
		if pair.with.PermissionMode != pair.plain.PermissionMode {
			t.Errorf("%s: permission mode %q with the prefix, %q without", pair.name, pair.with.PermissionMode, pair.plain.PermissionMode)
		}
		if pair.with.Policy.SettingSources != nil || pair.with.Policy.SettingSources != pair.plain.Policy.SettingSources {
			t.Errorf("%s: settings sources %v with the prefix, %v without; the planner is unisolated either way",
				pair.name, pair.with.Policy.SettingSources, pair.plain.Policy.SettingSources)
		}
		// Everything but Prompt, field for field: a field added to
		// NodeInvocation later is held to the same rule without this test
		// having to name it.
		with, plain := pair.with, pair.plain
		with.Prompt, plain.Prompt = "", ""
		if !reflect.DeepEqual(with, plain) {
			t.Errorf("%s: the invocation differs beyond Prompt:\nwith    %+v\nwithout %+v", pair.name, with, plain)
		}
	}
}

// The prefix sits in front of the whole planner instruction on every attempt —
// the first, the repair and the continuation — and the rest of each prompt is
// today's golden byte for byte, so the engine's rules and the JSON-only reply
// requirement stay the last words the planner reads.
func TestInterviewPrefix_PrecedesEveryPlannerPromptUnchanged(t *testing.T) {
	prefix := renderedInterviewPrefix(t)
	plainFirst, plainRepair, plainCont := plannerPrompts(t)
	withFirst, withRepair, withCont := plannerPrompts(t, WithInterviewPrefix(prefix))

	for _, pair := range []struct {
		name        string
		plain, with string
	}{
		{"first attempt", plainFirst.Prompt, withFirst.Prompt},
		{"repair attempt", plainRepair.Prompt, withRepair.Prompt},
		{"cycle-2 continuation", plainCont.Prompt, withCont.Prompt},
	} {
		if !strings.HasPrefix(pair.with, prefix) {
			t.Errorf("%s: the prompt does not open with the interview prefix", pair.name)
			continue
		}
		if got, want := withoutNonces(strings.TrimPrefix(pair.with, prefix)), withoutNonces(pair.plain); got != want {
			t.Errorf("%s: after the prefix the prompt is not today's prompt:\n--- want\n%s\n--- got\n%s", pair.name, want, got)
		}
	}
}

// An empty prefix — an interview with no answers renders "" — is the same
// prompt as no option at all, so /done at the first prompt sends the planner
// what a run without --interview sends it.
func TestInterviewPrefix_EmptyIsTheGolden(t *testing.T) {
	first, repair, continuation := plannerPrompts(t, WithInterviewPrefix(""))
	checkPlannerGolden(t, "first", first.Prompt)
	checkPlannerGolden(t, "repair", repair.Prompt)
	checkPlannerGolden(t, "continuation", continuation.Prompt)
}

// The interview is text for the planner only: nothing in the plan the
// coordinator returns — the spec that is persisted and replayed, or any
// planned node's prompt — carries it.
func TestInterviewPrefix_NeverReachesAPlannedNode(t *testing.T) {
	prefix := renderedInterviewPrefix(t)
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: validSpec})
	plan, err := New(fake, WithInterviewPrefix(prefix)).Plan(context.Background(), goldenGoal, goldenInputKeys)
	if err != nil {
		t.Fatal(err)
	}
	const answer = "beside it; text stays the default"
	if strings.Contains(string(plan.Spec), answer) || strings.Contains(string(plan.Spec), "--- interview ") {
		t.Errorf("the plan spec carries the interview: %s", plan.Spec)
	}
	for _, node := range plan.Graph.Nodes {
		if strings.Contains(node.Prompt, answer) || strings.Contains(node.Prompt, "--- interview ") {
			t.Errorf("planned node %q carries the interview in its prompt: %q", node.ID, node.Prompt)
		}
	}
}

// The interviewer's call is the planner's stance exactly: coordinatorInvocation
// with the interview's prompt, so read-only, no tools, the deny list and no
// SettingSources.
func TestInterviewer_UsesThePlannersInvocation(t *testing.T) {
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: "ENOUGH"})
	reply, _, err := New(fake).Interviewer()(context.Background(), "ask one question")
	if err != nil {
		t.Fatal(err)
	}
	if reply != "ENOUGH" {
		t.Errorf("reply = %q, want the call's result", reply)
	}
	if want := coordinatorInvocation("ask one question"); !reflect.DeepEqual(*captured, want) {
		t.Errorf("interviewer invocation = %+v, want coordinatorInvocation's %+v", *captured, want)
	}
	if captured.Policy.SettingSources != nil {
		t.Errorf("the interviewer is isolated (%v); it must be unisolated like the planner", captured.Policy.SettingSources)
	}
	if len(captured.Policy.AllowedTools) != 0 {
		t.Errorf("the interviewer was granted tools: %v", captured.Policy.AllowedTools)
	}
}

// Every call's cost reaches the interview's Result, including the call that
// ended it, so the interview's spend is reported rather than dropped.
func TestInterviewer_CostIsSummedIntoTheResult(t *testing.T) {
	calls := 0
	fake := runner.NewFakeRunner(map[string]runner.NodeOutcome{
		"question": {Result: "QUESTION: Which commands need --json?", TotalCostUSD: 0.25,
			Usage: runner.TokenUsage{InputTokens: 100, CachedInputTokens: 10, OutputTokens: 20, ReasoningOutputTokens: 5}},
		"enough": {Result: "ENOUGH", TotalCostUSD: 0.5, CostUnknown: true,
			Usage: runner.TokenUsage{InputTokens: 200, CachedInputTokens: 30, OutputTokens: 1, ReasoningOutputTokens: 2}},
	})
	fake.KeyFn = func(runner.NodeInvocation) string {
		calls++
		if calls == 1 {
			return "question"
		}
		return "enough"
	}

	result, err := interview.Run(context.Background(), goldenGoal, New(fake).Interviewer(),
		strings.NewReader("only status\n"), io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("interviewer called %d times, want 2", calls)
	}
	want := interview.Accounting{CostUSD: 0.75, CostUnknown: true,
		Usage: interview.Usage{InputTokens: 300, CachedInputTokens: 40, OutputTokens: 21, ReasoningOutputTokens: 7}}
	if result.Cost != want {
		t.Errorf("interview cost = %+v, want %+v", result.Cost, want)
	}
}

// A non-zero exit is an error the interview abandons on, and its cost still
// comes back; a spawn that never replied has no cost to return.
func TestInterviewer_FailuresKeepWhatWasSpent(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: "session limit", ExitCode: 1, TotalCostUSD: 0.04})
	_, accounting, err := New(fake).Interviewer()(context.Background(), "ask")
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("non-zero exit: err = %v, want one naming the exit code", err)
	}
	if accounting.CostUSD != 0.04 {
		t.Errorf("non-zero exit dropped its cost: %+v", accounting)
	}

	spawn := runner.NewFakeRunner(nil)
	spawn.KeyFn = func(runner.NodeInvocation) string { return "x" }
	boom := errors.New("transport down")
	spawn.InjectError("x", boom)
	_, accounting, err = New(spawn).Interviewer()(context.Background(), "ask")
	if !errors.Is(err, boom) {
		t.Fatalf("runner error: err = %v, want it wrapped", err)
	}
	if accounting != (interview.Accounting{}) {
		t.Errorf("a call that never replied reported cost %+v", accounting)
	}
}

// interview.Usage restates runner.TokenUsage so that package imports no
// runner. interviewAccounting copies field by field, so a counter added to
// one and not the other would be dropped from the interview's cost silently.
func TestInterviewAccounting_CopiesEveryUsageField(t *testing.T) {
	runtime := fieldNames(reflect.TypeOf(runner.TokenUsage{}))
	restated := fieldNames(reflect.TypeOf(interview.Usage{}))
	if !reflect.DeepEqual(runtime, restated) {
		t.Fatalf("interview.Usage fields %v, runner.TokenUsage fields %v: they must match", restated, runtime)
	}

	var usage runner.TokenUsage
	v := reflect.ValueOf(&usage).Elem()
	for i := 0; i < v.NumField(); i++ {
		v.Field(i).SetInt(int64(i + 1))
	}
	got := reflect.ValueOf(interviewAccounting(runner.NodeOutcome{Usage: usage}).Usage)
	for i, name := range runtime {
		if got.FieldByName(name).Int() != v.FieldByName(name).Int() {
			t.Errorf("Usage.%s was not copied (field %d)", name, i)
		}
	}
}

func fieldNames(typ reflect.Type) []string {
	names := make([]string, 0, typ.NumField())
	for i := 0; i < typ.NumField(); i++ {
		names = append(names, typ.Field(i).Name)
	}
	return names
}
