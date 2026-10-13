package runner

import (
	"context"
	"testing"
)

// TestNeedsCompoundShellRule pins the predicate behind #380: only a dontAsk
// node holding a scoped Bash rule and no blanket Bash grant is told to keep
// each Bash call to one plain command. A blanket grant admits a compound
// command anyway, and outside dontAsk an unmatched call is not denied.
func TestNeedsCompoundShellRule(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		tools []string
		want  bool
	}{
		{"scoped prefix alone", "dontAsk", []string{"Bash(gh *)"}, true},
		{"bare Bash", "dontAsk", []string{"Bash"}, false},
		{"Bash(*)", "dontAsk", []string{"Bash(*)"}, false},
		{"scoped beside Bash(*)", "dontAsk", []string{"Bash(gh *)", "Bash(*)"}, false},
		{"scoped beside bare Bash", "dontAsk", []string{"Bash(gh *)", "Bash"}, false},
		{"no Bash at all", "dontAsk", []string{"Read", "Edit"}, false},
		{"empty", "dontAsk", nil, false},
		{"other mode", "acceptEdits", []string{"Bash(gh *)"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := NeedsCompoundShellRule(tc.mode, tc.tools); got != tc.want {
				t.Fatalf("NeedsCompoundShellRule(%q, %q) = %v, want %v", tc.mode, tc.tools, got, tc.want)
			}
		})
	}
}

// TestBuildCmd_CompoundShellRule pins where --append-system-prompt appears:
// exactly when the predicate holds, immediately followed by the rule text, and
// nowhere else.
func TestBuildCmd_CompoundShellRule(t *testing.T) {
	cases := []struct {
		name  string
		mode  string
		tools []string
		want  bool
	}{
		{"gh prefix", "dontAsk", []string{"Read", "Bash(gh *)"}, true},
		{"git status prefix", "dontAsk", []string{"Read", "Bash(git status*)"}, true},
		{"blanket Bash(*) beside a prefix", "dontAsk", []string{"Bash(gh *)", "Bash(*)"}, false},
		{"bare Bash", "dontAsk", []string{"Bash"}, false},
		{"no Bash", "dontAsk", []string{"Read", "Edit"}, false},
		{"no allowed tools", "dontAsk", nil, false},
		{"acceptEdits", "acceptEdits", []string{"Bash(gh *)"}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := NewCLIRunner(RuntimeClaude, WithBinary("claude"))
			cmd := r.buildCmd(context.Background(), NodeInvocation{
				Prompt:         testPrompt,
				PermissionMode: tc.mode,
				Policy:         ToolPolicy{AllowedTools: tc.tools},
			})
			_, present := flagValue(cmd.Args, "--append-system-prompt")
			if present != tc.want {
				t.Fatalf("--append-system-prompt present = %v, want %v: %q", present, tc.want, cmd.Args)
			}
			if tc.want && !hasFlagValue(cmd.Args, "--append-system-prompt", CompoundShellRule) {
				t.Fatalf("--append-system-prompt is not followed by CompoundShellRule: %q", cmd.Args)
			}
		})
	}
}

// The rule travels beside the prompt, never inside it: -p stays the
// author's rendered text, byte for byte.
func TestBuildCmd_CompoundShellRuleLeavesThePromptAlone(t *testing.T) {
	r := NewCLIRunner(RuntimeClaude, WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy:         ToolPolicy{AllowedTools: []string{"Read", "Bash(gh *)"}},
	})
	if !hasFlagValue(cmd.Args, "--append-system-prompt", CompoundShellRule) {
		t.Fatalf("argv lost the rule, so this test would pass for the wrong reason: %q", cmd.Args)
	}
	if !hasFlagValue(cmd.Args, "-p", testPrompt) {
		t.Fatalf("-p is not the rendered prompt unchanged: %q", cmd.Args)
	}
}

// A system prompt is per process, so a resumed invocation must carry the rule
// again rather than trusting the parent session to have had it.
func TestBuildCmd_CompoundShellRuleOnResume(t *testing.T) {
	r := NewCLIRunner(RuntimeClaude, WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		ResumeSession:  "sess-380",
		Policy:         ToolPolicy{AllowedTools: []string{"Read", "Bash(gh *)"}},
	})
	if !hasFlagValue(cmd.Args, "--resume", "sess-380") {
		t.Fatalf("argv is not a resumed invocation: %q", cmd.Args)
	}
	if !hasFlagValue(cmd.Args, "--append-system-prompt", CompoundShellRule) {
		t.Fatalf("resumed invocation lost the rule: %q", cmd.Args)
	}
}

// Codex has no such flag and is never handed the rule, even for an
// invocation the claude runtime would give it to.
func TestBuildCmd_CodexNeverCarriesTheCompoundShellRule(t *testing.T) {
	r := NewCLIRunner(RuntimeCodex, WithBinary("codex"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy:         ToolPolicy{AllowedTools: []string{"Read", "Bash(gh *)"}},
	})
	for _, arg := range cmd.Args {
		if arg == "--append-system-prompt" || arg == CompoundShellRule {
			t.Fatalf("codex argv carries the claude-only rule: %q", cmd.Args)
		}
	}
}

// TestAppendsCompoundShellRule_AgreesWithTheArgv pins the report the run feed
// records against the argv itself: for every runtime and every case, the
// runner says it appends the rule exactly when its argv carries the flag, and
// a FakeRunner of that runtime says the same, so an engine test's record means
// what a real run's would.
func TestAppendsCompoundShellRule_AgreesWithTheArgv(t *testing.T) {
	invocations := map[string]NodeInvocation{
		"dontAsk + gh prefix": {PermissionMode: "dontAsk", Policy: ToolPolicy{AllowedTools: []string{"Read", "Bash(gh *)"}}},
		"dontAsk + Bash(*)":   {PermissionMode: "dontAsk", Policy: ToolPolicy{AllowedTools: []string{"Read", "Bash(*)"}}},
		"auto + gh prefix":    {PermissionMode: "auto", Policy: ToolPolicy{AllowedTools: []string{"Read", "Bash(gh *)"}}},
	}
	for _, runtime := range []Runtime{RuntimeClaude, RuntimeCodex} {
		for name, spec := range invocations {
			t.Run(string(runtime)+"/"+name, func(t *testing.T) {
				spec.Prompt = testPrompt
				r := NewCLIRunner(runtime, WithBinary(string(runtime)))
				_, inArgv := flagValue(r.buildCmd(context.Background(), spec).Args, "--append-system-prompt")
				if got := AppendsCompoundShellRule(r, spec); got != inArgv {
					t.Fatalf("CLIRunner reports %v, but its argv carries the flag: %v", got, inArgv)
				}
				fake := NewFakeRunner(nil)
				fake.SetRuntime(runtime)
				if got := AppendsCompoundShellRule(fake, spec); got != inArgv {
					t.Fatalf("FakeRunner reports %v, but a %s argv carries the flag: %v", got, runtime, inArgv)
				}
			})
		}
	}
	if !AppendsCompoundShellRule(NewFakeRunner(nil), invocations["dontAsk + gh prefix"]) {
		t.Fatal("a FakeRunner with no runtime set must answer as claude, the default runtime")
	}
}

// unansweringRunner is a NodeRunner that cannot say what its argv carries.
type unansweringRunner struct{}

func (unansweringRunner) Run(context.Context, NodeInvocation) (NodeOutcome, error) {
	return NodeOutcome{}, nil
}

// A runner that cannot answer is reported as not appending the rule, so a
// record never claims what nothing confirmed was sent.
func TestAppendsCompoundShellRule_UnansweringRunnerIsFalse(t *testing.T) {
	spec := NodeInvocation{PermissionMode: "dontAsk", Policy: ToolPolicy{AllowedTools: []string{"Bash(gh *)"}}}
	if AppendsCompoundShellRule(unansweringRunner{}, spec) {
		t.Fatal("a runner without AppendsCompoundShellRule was reported as appending the rule")
	}
}
