package main

import (
	"reflect"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runner"
)

func TestParseCommandLineRuntime(t *testing.T) {
	tests := []struct {
		name        string
		args        []string
		wantRuntime runner.Runtime
		wantArgs    []string
	}{
		{name: "default", args: []string{"run", "graph.yaml"}, wantRuntime: runner.RuntimeClaude, wantArgs: []string{"run", "graph.yaml"}},
		{name: "separate value", args: []string{"--runtime", "codex", "auto", "goal"}, wantRuntime: runner.RuntimeCodex, wantArgs: []string{"auto", "goal"}},
		{name: "equals value", args: []string{"--runtime=codex", "chat"}, wantRuntime: runner.RuntimeCodex, wantArgs: []string{"chat"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gotRuntime, gotArgs, err := parseCommandLine(test.args)
			if err != nil {
				t.Fatalf("parseCommandLine: %v", err)
			}
			if gotRuntime != test.wantRuntime || !reflect.DeepEqual(gotArgs, test.wantArgs) {
				t.Errorf("parseCommandLine(%q) = (%q, %q), want (%q, %q)", test.args, gotRuntime, gotArgs, test.wantRuntime, test.wantArgs)
			}
		})
	}
}

func TestParseCommandLineRejectsInvalidRuntimeFlag(t *testing.T) {
	for _, args := range [][]string{
		{"--runtime"},
		{"--runtime", "gemini", "run", "graph.yaml"},
		{"--runtime=", "run", "graph.yaml"},
		{"--runtime", "codex", "--runtime", "claude", "run", "graph.yaml"},
	} {
		if _, _, err := parseCommandLine(args); err == nil || !strings.Contains(err.Error(), "runtime") {
			t.Errorf("parseCommandLine(%q) error = %v, want runtime error", args, err)
		}
	}
}

func TestParseCommandLineRequiresRuntimeBeforeSubcommand(t *testing.T) {
	gotRuntime, gotArgs, err := parseCommandLine([]string{"run", "graph.yaml", "--runtime", "codex"})
	if err != nil {
		t.Fatalf("parseCommandLine: %v", err)
	}
	if gotRuntime != runner.RuntimeClaude || !reflect.DeepEqual(gotArgs, []string{"run", "graph.yaml", "--runtime", "codex"}) {
		t.Fatalf("runtime after subcommand was consumed globally: runtime=%q args=%q", gotRuntime, gotArgs)
	}
}
