package runner

import (
	"context"
	"strings"
	"testing"
)

// prompt/tool fixtures reused across the argv assertions.
const (
	testPrompt = "write a haiku about graphs"
)

// noSettings is the layer-1 value under test: a pointer to the empty string,
// meaning "load none of the user/project/local settings files".
func noSettings() *string {
	none := ""
	return &none
}

// TestBuildCmd_Argv asserts the exact claude argv oh-my-graph builds for a
// planned node carrying every ceiling layer plus a budget, including flag
// ORDER. This is the contract every real run depends on — if the argv drifts,
// the whole tool silently invokes claude wrong.
//
// It deliberately does NOT set Agent. `--agent` and `--setting-sources ""` are
// mutually exclusive in reality (isolation disables agent discovery, so the
// combination fails at CLI startup — DESIGN.md, E2) and unreachable in this
// codebase (auto mode rejects `agent:`; hand-written graphs get no isolation).
// Pinning them together in the canonical argv test would enshrine a
// configuration that can never occur. `--agent`'s own position is pinned by
// TestBuildCmd_AgentArgv against the shape that does occur.
func TestBuildCmd_Argv(t *testing.T) {
	r := NewClaudeCLIRunner(WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		Cwd:            "/tmp/omg",
		PermissionMode: "acceptEdits",
		ResumeSession:  "sess-123",
		BudgetUSD:      0.50,
		Policy: ToolPolicy{
			AllowedTools:    []string{"Read", "Bash(make *)"},
			Tools:           []string{"Read", "Bash"},
			SettingSources:  noSettings(),
			StrictMCPConfig: true,
			DisallowedTools: []string{"WebFetch", "WebSearch"},
		},
	})

	want := []string{
		"claude",
		"-p", testPrompt,
		"--output-format", "json",
		"--permission-mode", "acceptEdits",
		"--max-budget-usd", "0.5",
		"--setting-sources", "",
		"--allowedTools", "Read,Bash(make *)",
		"--tools", "Read,Bash",
		"--strict-mcp-config",
		"--disallowedTools", "WebFetch,WebSearch",
		"--resume", "sess-123",
	}
	if got := cmd.Args; !equalArgs(got, want) {
		t.Fatalf("argv mismatch:\n got=%q\nwant=%q", got, want)
	}
	if cmd.Dir != "/tmp/omg" {
		t.Fatalf("cwd = %q, want /tmp/omg", cmd.Dir)
	}
}

// TestBuildCmd_AgentArgv pins the full argv of the shape `agent:` actually
// occurs in: a hand-written node, which carries its own allow rules and no
// ceiling layer at all.
func TestBuildCmd_AgentArgv(t *testing.T) {
	r := NewClaudeCLIRunner(WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "plan",
		Agent:          "code-reviewer",
		Policy:         ToolPolicy{AllowedTools: []string{"Read"}},
	})

	want := []string{
		"claude",
		"-p", testPrompt,
		"--output-format", "json",
		"--permission-mode", "plan",
		"--agent", "code-reviewer",
		"--allowedTools", "Read",
	}
	if got := cmd.Args; !equalArgs(got, want) {
		t.Fatalf("argv mismatch:\n got=%q\nwant=%q", got, want)
	}
}

// TestBuildCmd_OmitsOptionalFlags proves every optional flag is absent when
// nothing configured it — a fan-in node with a clean session, no tool grants
// and no imposed ceiling.
func TestBuildCmd_OmitsOptionalFlags(t *testing.T) {
	r := NewClaudeCLIRunner()
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "plan",
	})

	joined := strings.Join(cmd.Args, " ")
	for _, flag := range []string{
		"--max-budget-usd", "--allowedTools", "--disallowedTools", "--resume",
		"--setting-sources", "--tools", "--strict-mcp-config", "--agent",
	} {
		if strings.Contains(joined, flag) {
			t.Errorf("expected no %s flag, got argv: %q", flag, cmd.Args)
		}
	}
}

// TestBuildCmd_HandWrittenGraphArgvIsUnchanged pins the promise that the whole
// ceiling is auto mode's alone. internal/schedule builds a hand-written
// graph's policy from the node's own allowed_tools and nothing else, so its
// argv must be EXACTLY what it was before the ceiling existed — no isolation,
// no narrowing, no MCP bound, no deny list. A regression here would silently
// disable a user's own settings, hooks and MCP servers in the path whose whole
// purpose is precise user control.
func TestBuildCmd_HandWrittenGraphArgvIsUnchanged(t *testing.T) {
	r := NewClaudeCLIRunner(WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy:         ToolPolicy{AllowedTools: []string{"Read", "Bash(git *)"}},
	})

	want := []string{
		"claude",
		"-p", testPrompt,
		"--output-format", "json",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read,Bash(git *)",
	}
	if got := cmd.Args; !equalArgs(got, want) {
		t.Fatalf("hand-written graph argv must be unchanged:\n got=%q\nwant=%q", got, want)
	}
}

// TestBuildCmd_SettingSourcesEmptyIsRenderedNotOmitted is the load-bearing
// failure case of the whole ceiling. Layer 1's value IS the empty string, so
// the natural Go reflex — treating "" as unset and skipping the flag — would
// leave the argv looking clean while silently reloading the user's standing
// Bash(*) grant and reopening the exact gap this change closes. The distinction
// is carried by *string precisely so it cannot collapse: nil omits, &"" emits.
func TestBuildCmd_SettingSourcesEmptyIsRenderedNotOmitted(t *testing.T) {
	r := NewClaudeCLIRunner()

	isolated := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy:         ToolPolicy{SettingSources: noSettings()},
	})
	if !hasFlagValue(isolated.Args, "--setting-sources", "") {
		t.Errorf(`--setting-sources "" must be rendered as a flag with an empty value, got: %q`, isolated.Args)
	}

	notSpecified := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy:         ToolPolicy{},
	})
	if strings.Contains(strings.Join(notSpecified.Args, " "), "--setting-sources") {
		t.Errorf("a nil SettingSources must omit the flag entirely, got: %q", notSpecified.Args)
	}
}

// TestBuildCmd_ToolsNilOmitsEmptyDisables pins the other nil-vs-empty
// distinction. --tools "" is documented by the CLI as "disable all tools",
// which is the opposite of "use the default set" — so a non-nil empty slice
// must render the flag, and only nil may omit it.
func TestBuildCmd_ToolsNilOmitsEmptyDisables(t *testing.T) {
	r := NewClaudeCLIRunner()

	omitted := r.buildCmd(context.Background(), NodeInvocation{
		Prompt: testPrompt, PermissionMode: "dontAsk",
		Policy: ToolPolicy{Tools: nil},
	})
	if strings.Contains(strings.Join(omitted.Args, " "), "--tools") {
		t.Errorf("a nil Tools must omit --tools, got: %q", omitted.Args)
	}

	disabled := r.buildCmd(context.Background(), NodeInvocation{
		Prompt: testPrompt, PermissionMode: "dontAsk",
		Policy: ToolPolicy{Tools: []string{}},
	})
	if !hasFlagValue(disabled.Args, "--tools", "") {
		t.Errorf(`an empty non-nil Tools must render --tools "", got: %q`, disabled.Args)
	}
}

// TestBuildCmd_PlannedNodeCeilingRendersEveryLayer is the argv half of auto
// mode's ceiling: a policy built by coordinator.toolPolicyFor must reach the
// argv with all five layers intact. Layers 3 and 5 are deliberate redundancy —
// dropping either silently would still look like a working ceiling in every
// other test, so they are asserted individually rather than as one blob.
func TestBuildCmd_PlannedNodeCeilingRendersEveryLayer(t *testing.T) {
	r := NewClaudeCLIRunner()
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Policy: ToolPolicy{
			AllowedTools:    []string{"Read", "Bash(git *)"},
			Tools:           []string{"Read", "Bash"},
			SettingSources:  noSettings(),
			StrictMCPConfig: true,
			DisallowedTools: []string{"Edit", "Write", "WebFetch"},
		},
	})

	joined := strings.Join(cmd.Args, " ")
	for layer, want := range map[string]string{
		"1 isolation": "--setting-sources",
		"2 grant":     "--allowedTools Read,Bash(git *)",
		"3 narrowing": "--tools Read,Bash",
		"4 MCP":       "--strict-mcp-config",
		"5 residual":  "--disallowedTools Edit,Write,WebFetch",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("layer %s missing from planned argv (want %q): %q", layer, want, cmd.Args)
		}
	}
}

// TestBuildCmd_AgentOnlyWhenNamed proves `agent:` reaches the argv as
// --agent <name> and that the default (no agent) leaves plain `claude -p`
// exactly as it was.
func TestBuildCmd_AgentOnlyWhenNamed(t *testing.T) {
	r := NewClaudeCLIRunner()

	named := r.buildCmd(context.Background(), NodeInvocation{
		Prompt: testPrompt, PermissionMode: "dontAsk", Agent: "code-reviewer",
	})
	if !hasFlagValue(named.Args, "--agent", "code-reviewer") {
		t.Errorf("expected --agent code-reviewer in argv, got: %q", named.Args)
	}

	plain := r.buildCmd(context.Background(), NodeInvocation{
		Prompt: testPrompt, PermissionMode: "dontAsk",
	})
	if strings.Contains(strings.Join(plain.Args, " "), "--agent") {
		t.Errorf("expected no --agent flag when Agent is empty, got argv: %q", plain.Args)
	}
}

// TestBuildCmd_MaxBudgetOnlyWhenPositive pins the mid-run cost kill: a node with
// a positive budget_usd renders --max-budget-usd (so claude aborts the run once
// its own spend crosses it), and a node with no budget — or a non-positive one —
// renders NO such flag, leaving budget-less graphs on exactly the argv they had
// before this guard existed. The value is a plain decimal, never scientific
// notation, so even a tiny budget is a token claude parses.
func TestBuildCmd_MaxBudgetOnlyWhenPositive(t *testing.T) {
	r := NewClaudeCLIRunner()

	budgeted := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		BudgetUSD:      0.000001,
	})
	if !strings.Contains(strings.Join(budgeted.Args, " "), "--max-budget-usd 0.000001") {
		t.Errorf("budgeted node argv missing plain-decimal --max-budget-usd: %q", budgeted.Args)
	}

	for _, budget := range []float64{0, -1} {
		cmd := r.buildCmd(context.Background(), NodeInvocation{
			Prompt:         testPrompt,
			PermissionMode: "dontAsk",
			BudgetUSD:      budget,
		})
		if strings.Contains(strings.Join(cmd.Args, " "), "--max-budget-usd") {
			t.Errorf("budget %v must render no flag, got: %q", budget, cmd.Args)
		}
	}
}

// TestParseEnvelope_BudgetExhausted proves the runner recognizes claude's own
// --max-budget-usd abort: the subtype error_max_budget_usd (with no result and a
// cost at/over budget) sets BudgetExhausted, and an ordinary success envelope
// does not. This is the empirically-observed shape from claude 2.1.220, not an
// assumption — the Scheduler depends on this flag to classify the failure.
func TestParseEnvelope_BudgetExhausted(t *testing.T) {
	killed := `{"session_id":"s1","total_cost_usd":0.4776,"subtype":"error_max_budget_usd","is_error":true,"errors":["Reached maximum budget ($0.001)"]}`
	outcome, err := parseEnvelope([]byte(killed), nil)
	if err != nil {
		t.Fatalf("a budget-abort envelope is still valid JSON and must parse: %v", err)
	}
	if !outcome.BudgetExhausted {
		t.Errorf("subtype error_max_budget_usd must set BudgetExhausted; got %+v", outcome)
	}
	if outcome.TotalCostUSD != 0.4776 {
		t.Errorf("cost = %v, want 0.4776 (the already-spent amount)", outcome.TotalCostUSD)
	}

	ok := `{"session_id":"s2","result":"hi","total_cost_usd":0.03,"subtype":"success"}`
	normal, err := parseEnvelope([]byte(ok), nil)
	if err != nil {
		t.Fatalf("parse success envelope: %v", err)
	}
	if normal.BudgetExhausted {
		t.Errorf("a success envelope must not set BudgetExhausted; got %+v", normal)
	}
}

// TestBuildCmd_NeverBareOrNoSessionPersistence is a guard against the two flags
// that would break the subscription-auth / fleetops-observability contract:
// --bare disables OAuth, and --no-session-persistence hides the run from
// fleetops. Neither must ever appear.
func TestBuildCmd_NeverBareOrNoSessionPersistence(t *testing.T) {
	r := NewClaudeCLIRunner()
	cmd := r.buildCmd(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	joined := strings.Join(cmd.Args, " ")
	for _, forbidden := range []string{"--bare", "--no-session-persistence"} {
		if strings.Contains(joined, forbidden) {
			t.Errorf("argv must never contain %s, got: %q", forbidden, cmd.Args)
		}
	}
}

// TestBuildCmd_ScrubsSubscriptionAuthEnv is the load-bearing test of the whole
// project: even when the PARENT process has ANTHROPIC_API_KEY and
// ANTHROPIC_AUTH_TOKEN set, the built child command's env must contain NEITHER —
// otherwise claude silently switches from the user's subscription (OAuth) to
// metered API billing.
//
// The parent env is injected via a fake environ so the assertion does not depend
// on the developer's real shell, and a benign variable is included to prove the
// scrub is surgical (it removes only the two auth keys, not the whole env).
//
// This is the runner's CALL SITE of the shared policy: the rule itself (which
// keys, matched how) lives in internal/childenv and is tested there, and
// verify.ShellVerifier — the only other spawner — has the mirror image of this
// test. Both must keep passing, or one kind of child process is billed
// differently from the other.
func TestBuildCmd_ScrubsSubscriptionAuthEnv(t *testing.T) {
	parentEnv := []string{
		"ANTHROPIC_API_KEY=sk-should-be-scrubbed",
		"ANTHROPIC_AUTH_TOKEN=tok-should-be-scrubbed",
		"PATH=/usr/bin",
		"HOME=/home/dev",
	}
	r := NewClaudeCLIRunner(withEnviron(func() []string { return parentEnv }))

	cmd := r.buildCmd(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})

	for _, kv := range cmd.Env {
		key, _, _ := strings.Cut(kv, "=")
		if key == "ANTHROPIC_API_KEY" {
			t.Errorf("ANTHROPIC_API_KEY leaked into child env: %q", kv)
		}
		if key == "ANTHROPIC_AUTH_TOKEN" {
			t.Errorf("ANTHROPIC_AUTH_TOKEN leaked into child env: %q", kv)
		}
	}

	// Surgical: the benign variables must survive.
	if !containsEnv(cmd.Env, "PATH=/usr/bin") || !containsEnv(cmd.Env, "HOME=/home/dev") {
		t.Errorf("scrub removed benign env vars; child env = %q", cmd.Env)
	}
}

// hasFlagValue reports whether args contains flag immediately followed by
// value. Needed because the values under test include the EMPTY string, which
// a naive strings.Contains over a space-joined argv cannot distinguish from
// the flag being present with the next flag as its value.
func hasFlagValue(args []string, flag, value string) bool {
	for i := 0; i < len(args)-1; i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}

func equalArgs(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func containsEnv(env []string, want string) bool {
	for _, kv := range env {
		if kv == want {
			return true
		}
	}
	return false
}
