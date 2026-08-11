package main

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// Stable FakeRunner keys for the two engine calls a chat turn can make. Node
// invocations fall through to their prompt, as in the scheduler's own tests.
const (
	routerKey   = "router"
	autoPlanKey = "planner"
)

// newChatFake scripts a FakeRunner whose KeyFn tells the three kinds of chat
// invocation apart by the fixed header each prompt template carries: the
// router's classification call, the coordinator's planner call, and a planned
// node's own run.
func newChatFake(outcomes map[string]runner.NodeOutcome) *runner.FakeRunner {
	fake := runner.NewFakeRunner(outcomes)
	fake.KeyFn = func(spec runner.NodeInvocation) string {
		switch {
		case strings.Contains(spec.Prompt, "chat router for oh-my-graph"):
			return routerKey
		case strings.Contains(spec.Prompt, "planning coordinator for oh-my-graph"):
			return autoPlanKey
		default:
			return spec.Prompt
		}
	}
	return fake
}

// runChatLoop drives chatLoop over scripted stdin and returns what it printed.
func runChatLoop(t *testing.T, fake *runner.FakeRunner, stdin string) string {
	t.Helper()
	var out bytes.Buffer
	err := chatLoop(context.Background(), strings.NewReader(stdin), &out,
		coordinator.New(fake), fake, commonRunFlags{inputs: inputFlag{}})
	if err != nil {
		t.Fatalf("chatLoop returned error: %v", err)
	}
	return out.String()
}

func TestChatLoop_ConverseTurnPrintsReply(t *testing.T) {
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey: {Result: `{"mode":"converse","reply":"a DAG is a graph with no cycles"}`},
	})

	out := runChatLoop(t, fake, "what is a DAG?\n")

	if !strings.Contains(out, "a DAG is a graph with no cycles") {
		t.Errorf("converse reply not printed:\n%s", out)
	}
	if got := fake.InvocationCount(routerKey); got != 1 {
		t.Errorf("router invoked %d times, want 1", got)
	}
	if got := fake.InvocationCount(autoPlanKey); got != 0 {
		t.Errorf("a converse turn reached the planner %d times, want 0", got)
	}
}

// TestChatLoop_GraphTurnRoutesGoalThroughCoordinatorAndScheduler is the core
// hypothesis test: a graph-classified message must flow through the EXISTING
// auto path — coordinator.Plan, then the scheduler actually running the
// planned node — and leave the same on-disk trail as `oh-my-graph auto` (the
// saved spec and the run's events.jsonl).
func TestChatLoop_GraphTurnRoutesGoalThroughCoordinatorAndScheduler(t *testing.T) {
	plannedSpec := `{"name":"from-chat","nodes":[{"id":"work","prompt":"do the planned work","allowed_tools":["Read"]}]}`
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey:             {Result: `{"mode":"graph","goal":"tidy the docs"}`},
		autoPlanKey:           {Result: plannedSpec, TotalCostUSD: 0.01},
		"do the planned work": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
	})

	isolateRunHome(t)
	// The scheduler's ledger prints straight to os.Stdout; capture it so the
	// test output stays clean — the assertions below read the fake and the disk.
	captureStdout(t, func() {
		runChatLoop(t, fake, "clean up the docs please\ny\n")
	})

	if got := fake.InvocationCount(autoPlanKey); got != 1 {
		t.Errorf("planner invoked %d times, want 1", got)
	}
	if got := fake.InvocationCount("do the planned work"); got != 1 {
		t.Errorf("planned node invoked %d times, want 1 — the goal never reached the scheduler", got)
	}

	specs, err := filepath.Glob(filepath.Join(runsRoot(), "*", "graph.json"))
	if err != nil || len(specs) != 1 {
		t.Errorf("saved spec files = %v (err %v), want exactly one graph.json", specs, err)
	}
	feeds, err := filepath.Glob(filepath.Join(runsRoot(), "*", "events.jsonl"))
	if err != nil || len(feeds) != 1 {
		t.Errorf("event streams = %v (err %v), want exactly one events.jsonl — the chat path lost the run feed", feeds, err)
	}
}

// TestChatLoop_GraphTurnConfirmation covers the `Run this plan? [y/N]` gate a
// chat graph turn puts between printing the plan and executing it: only y/yes
// (case-insensitive) proceeds, anything else — including the empty default —
// discards the plan and the loop keeps serving turns, and EOF at the prompt
// ends the session as gracefully as EOF at the main prompt.
func TestChatLoop_GraphTurnConfirmation(t *testing.T) {
	plannedSpec := `{"name":"from-chat","nodes":[{"id":"work","prompt":"do the planned work","allowed_tools":["Read"]}]}`
	cases := []struct {
		name           string
		stdin          string
		wantNodeRuns   int
		wantRouterHits int
		wantDiscarded  bool
	}{
		{name: "y executes the plan", stdin: "tidy the docs\ny\n", wantNodeRuns: 1, wantRouterHits: 1},
		{name: "yes is accepted case-insensitively", stdin: "tidy the docs\nYes\n", wantNodeRuns: 1, wantRouterHits: 1},
		{name: "n discards and the loop serves the next turn", stdin: "tidy the docs\nn\ntidy the docs\ny\n", wantNodeRuns: 1, wantRouterHits: 2, wantDiscarded: true},
		{name: "empty answer defaults to no", stdin: "tidy the docs\n\nexit\n", wantNodeRuns: 0, wantRouterHits: 1, wantDiscarded: true},
		{name: "EOF at the prompt ends the session cleanly", stdin: "tidy the docs\n", wantNodeRuns: 0, wantRouterHits: 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := newChatFake(map[string]runner.NodeOutcome{
				routerKey:             {Result: `{"mode":"graph","goal":"tidy the docs"}`},
				autoPlanKey:           {Result: plannedSpec, TotalCostUSD: 0.01},
				"do the planned work": {SessionID: "s-work", Result: "PASS", ExitCode: 0},
			})

			isolateRunHome(t)
			var out string
			captureStdout(t, func() {
				out = runChatLoop(t, fake, tc.stdin)
			})

			if !strings.Contains(out, "Run this plan? [y/N]") {
				t.Errorf("confirmation prompt not printed:\n%s", out)
			}
			if got := fake.InvocationCount("do the planned work"); got != tc.wantNodeRuns {
				t.Errorf("planned node invoked %d times, want %d", got, tc.wantNodeRuns)
			}
			if got := fake.InvocationCount(routerKey); got != tc.wantRouterHits {
				t.Errorf("router invoked %d times, want %d", got, tc.wantRouterHits)
			}
			if got := strings.Contains(out, "plan discarded."); got != tc.wantDiscarded {
				t.Errorf("output contains %q = %v, want %v:\n%s", "plan discarded.", got, tc.wantDiscarded, out)
			}
		})
	}
}

// TestChatLoop_MalformedRouterJSONFallsBackWithoutPanic scripts a router reply
// with no JSON in it: the loop must print the raw text as a converse turn and
// keep serving the next line rather than panicking or exiting.
func TestChatLoop_MalformedRouterJSONFallsBackWithoutPanic(t *testing.T) {
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey: {Result: "no JSON here, just vibes"},
	})

	out := runChatLoop(t, fake, "first turn\nsecond turn\n")

	if !strings.Contains(out, "no JSON here, just vibes") {
		t.Errorf("fallback reply not printed:\n%s", out)
	}
	if got := fake.InvocationCount(routerKey); got != 2 {
		t.Errorf("router invoked %d times, want 2 — the loop must survive a malformed turn", got)
	}
}

// TestChatLoop_RouterErrorIsPrintedAndLoopContinues: a router-level failure (a
// non-zero exit) is a printed error, not a session killer.
func TestChatLoop_RouterErrorIsPrintedAndLoopContinues(t *testing.T) {
	fake := newChatFake(map[string]runner.NodeOutcome{
		routerKey: {Result: "overloaded", ExitCode: 1},
	})

	out := runChatLoop(t, fake, "hello\nexit\n")

	if !strings.Contains(out, "exited with code 1") {
		t.Errorf("router error not surfaced to the user:\n%s", out)
	}
}

func TestChatLoop_ExitAndQuitEndTheLoop(t *testing.T) {
	for _, word := range []string{"exit", "quit"} {
		t.Run(word, func(t *testing.T) {
			fake := newChatFake(nil)

			runChatLoop(t, fake, word+"\n")

			if got := fake.InvocationCount(routerKey); got != 0 {
				t.Errorf("%q still hit the router %d times, want 0", word, got)
			}
		})
	}
}

func TestChatLoop_EOFEndsTheLoop(t *testing.T) {
	fake := newChatFake(nil)

	runChatLoop(t, fake, "") // immediate EOF, no turns

	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("EOF alone caused runner calls: %v", calls)
	}
}

func TestChatLoop_BlankLinesAreSkipped(t *testing.T) {
	fake := newChatFake(nil)

	runChatLoop(t, fake, "\n   \nexit\n")

	if calls := fake.Calls(); len(calls) != 0 {
		t.Errorf("blank lines reached the router: %v", calls)
	}
}

// chat accepts the mapping opt-outs only — --no-agent-mapping, its per-agent
// form --no-agent, and --no-skill-mapping (a chat graph turn is an auto run
// and must honor the same opt-outs); every other flag and any positional
// argument is still rejected before the loop starts.
func TestRunChat_RejectsArguments(t *testing.T) {
	if err := runChat([]string{"--concurrency", "2"}); err == nil {
		t.Fatal("want an error for a flag chat does not define")
	}
	err := runChat([]string{"stray"})
	if err == nil || !strings.Contains(err.Error(), "unexpected argument") {
		t.Fatalf("err = %v, want an unexpected-argument error", err)
	}
}

// A chat turn plans and runs unattended on the same terms as `auto`, so the
// opt-out that keeps one node's ceiling has to be sayable here too — the
// granular one, not only the all-or-nothing one.
func TestChatFlags_AcceptsPerAgentOptOut(t *testing.T) {
	f := newChatFlags()
	if err := f.parse([]string{"--no-agent", "architect"}); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := []string(f.noAgents); len(got) != 1 || got[0] != "architect" {
		t.Errorf("noAgents = %v, want [architect]", got)
	}
}
