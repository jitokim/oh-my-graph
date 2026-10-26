package main

import (
	"context"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// forbidRemotePlannerMarker is a phrase of the planner sentence
// --forbid-remote-changes adds (ADR 0046 §5.2), and of nothing else in the
// prompt.
const forbidRemotePlannerMarker = "The operator forbade remote changes"

// ADR 0046 §8 test 11: `auto --forbid-remote-changes`, parsed by its real
// flag set and mapped by remoteChangesOptions, tells the planner; without the
// flag the planner is told nothing.
func TestRemoteChangesOptions_AutoFlagReachesThePlanner(t *testing.T) {
	plain := `{"name":"planned","nodes":[{"id":"scan","prompt":"scan","allowed_tools":["Read"]}]}`
	for _, tc := range []struct {
		args []string
		want bool
	}{
		{args: []string{"audit the readme"}, want: false},
		{args: []string{"audit the readme", "--forbid-remote-changes"}, want: true},
	} {
		auto := newAutoFlags()
		if err := auto.parse(tc.args); err != nil {
			t.Fatal(err)
		}
		fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: plain}})
		opts := append(remoteChangesOptions(auto.forbidRemoteChanges), coordinator.WithInvocationDir(t.TempDir()))
		if _, err := coordinator.New(fake, opts...).Plan(context.Background(), "audit the readme", nil); err != nil {
			t.Fatalf("%v: Plan: %v", tc.args, err)
		}
		if got := strings.Contains(fake.Invocations()[0].Prompt, forbidRemotePlannerMarker); got != tc.want {
			t.Errorf("%v: planner told of forbidden remote changes = %v, want %v", tc.args, got, tc.want)
		}
	}
}

// `chat` and `design` plan but register no --forbid-remote-changes, so
// neither is wired to the planner sentence: the flag is refused at parse.
func TestRemoteChangesOptions_ChatAndDesignDoNotAcceptTheFlag(t *testing.T) {
	if err := newChatFlags().parse([]string{"--forbid-remote-changes"}); err == nil {
		t.Error("chat accepted --forbid-remote-changes")
	}
	if err := newDesignFlags().parse([]string{"audit the readme", "--out", "g.yaml", "--forbid-remote-changes"}); err == nil {
		t.Error("design accepted --forbid-remote-changes")
	}
}
