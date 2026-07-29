package coordinator

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

// routeOK runs Route and fails the test on error — for cases asserting a
// successful classification.
func routeOK(t *testing.T, fake *runner.FakeRunner, message string) Route {
	t.Helper()
	route, err := New(fake).Route(context.Background(), message)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	return route
}

func TestRoute_ParsesConverse(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: `{"mode":"converse","reply":"hello there"}`})

	route := routeOK(t, fake, "hi")
	if route.Mode != RouteConverse || route.Reply != "hello there" {
		t.Errorf("route = %+v, want converse with reply %q", route, "hello there")
	}
}

func TestRoute_ParsesGraphGoal(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: `{"mode":"graph","goal":"run the tests and fix failures"}`})

	route := routeOK(t, fake, "please fix the failing tests")
	if route.Mode != RouteGraph || route.Goal != "run the tests and fix failures" {
		t.Errorf("route = %+v, want graph with the router's goal", route)
	}
}

// TestRoute_ToleratesFenceAndProse pins the same parsing tolerance Plan has:
// a code fence or prose around the JSON object must not break classification.
func TestRoute_ToleratesFenceAndProse(t *testing.T) {
	reply := "Sure, routing this one.\n```json\n{\"mode\":\"graph\",\"goal\":\"build the project\"}\n```\n"
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: reply})

	route := routeOK(t, fake, "build it")
	if route.Mode != RouteGraph || route.Goal != "build the project" {
		t.Errorf("route = %+v, want graph despite the fence and prose", route)
	}
}

// TestRoute_MalformedFallsBackToConverse covers every malformed shape the
// router can emit: no JSON at all, invalid JSON, an unknown mode, and a graph
// decision missing its goal. All of them must degrade to a converse turn
// echoing the raw reply — never an error, never a panic — because one bad
// classification must not kill the chat loop.
func TestRoute_MalformedFallsBackToConverse(t *testing.T) {
	cases := []struct {
		name  string
		reply string
	}{
		{"no JSON object", "I could not decide, sorry."},
		{"invalid JSON", "{mode: converse, oops}"},
		{"unknown mode", `{"mode":"panic","reply":"???"}`},
		{"graph without goal", `{"mode":"graph","goal":"  "}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake, _ := newPlannerFake(runner.NodeOutcome{Result: tc.reply})

			route := routeOK(t, fake, "hmm")
			if route.Mode != RouteConverse {
				t.Fatalf("route mode = %q, want the converse fallback", route.Mode)
			}
			if route.Reply != strings.TrimSpace(tc.reply) {
				t.Errorf("fallback reply = %q, want the raw router text %q", route.Reply, tc.reply)
			}
		})
	}
}

func TestRoute_NonZeroExitIsError(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: "boom", ExitCode: 1})

	_, err := New(fake).Route(context.Background(), "hi")
	if err == nil || !strings.Contains(err.Error(), "exited with code 1") {
		t.Fatalf("err = %v, want a router exit-code error", err)
	}
}

func TestRoute_EmptyMessageIsError(t *testing.T) {
	fake, _ := newPlannerFake(runner.NodeOutcome{Result: "unused"})

	_, err := New(fake).Route(context.Background(), "   ")
	if err == nil {
		t.Fatal("expected an error for an empty message")
	}
	if got := fake.InvocationCount(plannerKey); got != 0 {
		t.Errorf("router invoked %d times for an empty message, want 0", got)
	}
}

// TestRoute_MakesOneReadOnlyDeniedCall pins the router to the planner's own
// stance: exactly one call, read-only permission mode, no tools granted, and
// the full deny list of a node that declared nothing — the router only
// classifies text, so it must not be the least constrained call in a chat
// session.
func TestRoute_MakesOneReadOnlyDeniedCall(t *testing.T) {
	fake, captured := newPlannerFake(runner.NodeOutcome{Result: `{"mode":"converse","reply":"ok"}`})

	routeOK(t, fake, "what does this repo do?")
	if got := fake.InvocationCount(plannerKey); got != 1 {
		t.Errorf("router invoked %d times, want exactly 1", got)
	}
	if captured.PermissionMode != "plan" {
		t.Errorf("router permission mode = %q, want plan", captured.PermissionMode)
	}
	if len(captured.Policy.AllowedTools) != 0 {
		t.Errorf("router must grant no tools, got %v", captured.Policy.AllowedTools)
	}
	denied := strings.Join(captured.Policy.DisallowedTools, ",")
	for _, tool := range []string{"Bash", "Write", "WebFetch"} {
		if !strings.Contains(denied, tool) {
			t.Errorf("router deny list %q is missing %q", denied, tool)
		}
	}
	if !strings.Contains(captured.Prompt, "what does this repo do?") {
		t.Error("router prompt does not contain the user's message")
	}
}
