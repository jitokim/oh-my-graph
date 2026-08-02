package runner

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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

// TestBuildCmd_SessionIDArgv pins the full argv of the shape a pre-assigned
// session id actually occurs in: a fresh-session node (never a resuming one —
// NodeInvocation documents the two fields as mutually exclusive, and the
// scheduler enforces it). The id is what the scheduler already published on
// node_started, so the flag rendering here is the other half of that promise:
// the transcript a live view went looking for is the one this child writes.
func TestBuildCmd_SessionIDArgv(t *testing.T) {
	r := NewClaudeCLIRunner(WithBinary("claude"))
	cmd := r.buildCmd(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		SessionID:      "0f5a1c9e-2b3d-4a5e-8f6a-7b8c9d0e1f2a",
		Policy:         ToolPolicy{AllowedTools: []string{"Read"}},
	})

	want := []string{
		"claude",
		"-p", testPrompt,
		"--output-format", "json",
		"--permission-mode", "dontAsk",
		"--allowedTools", "Read",
		"--session-id", "0f5a1c9e-2b3d-4a5e-8f6a-7b8c9d0e1f2a",
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
		"--session-id",
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

// --- failure cause capture (scripted stub binaries) ---------------------------

// writeStub writes a scripted fake claude binary and returns its path — the
// runner test pattern for behaviour that only exists across a real exit code,
// without ever spawning real claude.
func writeStub(t *testing.T, script string) string {
	t.Helper()
	stub := filepath.Join(t.TempDir(), "claude-stub")
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	return stub
}

// TestRun_NonzeroExitCarriesEnvelopeErrorCause reproduces the incident this
// exists for: a subprocess killed by a subscription session limit exits 1 with
// an error envelope, and the failure detail used to say only "exit code 1".
// The envelope's own error report must reach NodeOutcome.FailureCause so the
// scheduler (and everything downstream — ledger, events.jsonl, watch, serve)
// can name the real cause.
func TestRun_NonzeroExitCarriesEnvelopeErrorCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
cat <<'JSON'
{"session_id":"s-limit","total_cost_usd":0.02,"subtype":"error_during_execution","is_error":true,"errors":["You've hit your session limit"]}
JSON
exit 1
`)

	r := NewClaudeCLIRunner(WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("a non-zero exit with a parseable envelope is an outcome, not a Run error: %v", err)
	}
	if outcome.ExitCode != 1 {
		t.Errorf("exit code = %d, want 1", outcome.ExitCode)
	}
	if outcome.FailureCause != "You've hit your session limit" {
		t.Errorf("FailureCause = %q, want the envelope's own error", outcome.FailureCause)
	}
}

// TestRun_NonzeroExitFallsBackToStderrCause proves the second-best diagnosis:
// when the envelope says nothing about why, a non-zero exit carries the stderr
// tail as the cause — and a CLEAN exit never does, however noisy stderr was.
func TestRun_NonzeroExitFallsBackToStderrCause(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	failing := writeStub(t, `#!/bin/sh
echo '{"session_id":"s-1","result":"partial","total_cost_usd":0.02}'
echo "You've hit your session limit" >&2
exit 1
`)
	r := NewClaudeCLIRunner(WithBinary(failing))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}
	if outcome.FailureCause != "You've hit your session limit" {
		t.Errorf("FailureCause = %q, want the stderr tail", outcome.FailureCause)
	}

	clean := writeStub(t, `#!/bin/sh
echo '{"session_id":"s-2","result":"PASS","total_cost_usd":0.01}'
echo "warning: noisy but harmless" >&2
exit 0
`)
	r = NewClaudeCLIRunner(WithBinary(clean))
	outcome, err = r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}
	if outcome.FailureCause != "" {
		t.Errorf("a clean exit must carry no FailureCause, got %q", outcome.FailureCause)
	}
}

// TestRun_SessionIDReachesTheChildAndComesBack closes the pre-assignment loop
// end to end without real claude: the stub echoes the `--session-id` value it
// received back as its envelope's session_id, so the assertion proves both
// that the flag reached the child's argv and that NodeOutcome.SessionID —
// still sourced from the envelope, exactly as before this flag existed —
// agrees with the id the scheduler published on node_started.
func TestRun_SessionIDReachesTheChildAndComesBack(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
sid=""
prev=""
for a in "$@"; do
  if [ "$prev" = "--session-id" ]; then sid="$a"; fi
  prev="$a"
done
printf '{"session_id":"%s","result":"PASS","total_cost_usd":0.01}' "$sid"
`)

	assigned := NewSessionID()
	r := NewClaudeCLIRunner(WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		SessionID:      assigned,
	})
	if err != nil {
		t.Fatalf("unexpected Run error: %v", err)
	}
	if outcome.SessionID != assigned {
		t.Errorf("outcome session id = %q, want the pre-assigned %q", outcome.SessionID, assigned)
	}
}

// --- cancellation kills the child tree (real spawn) ---------------------------

// TestRun_CancelledRunKillsTheChild proves defaultTimeout's promise — a wedged
// child can never hang the whole graph — actually holds through Run: cancelling
// the context must kill the child's whole process tree, not just the direct
// child, and Run must return promptly instead of blocking on a stdout pipe a
// grandchild still holds open. It mirrors verify's
// TestVerify_CancelledRunKillsTheChild on the other seam.
//
// The stub is not claude: like the verify package's tests, this spawns a free,
// offline shell script, because kill-the-tree behaviour cannot be proven
// without a process. Every OTHER runner test stays spawn-free via buildCmd and
// FakeRunner.
func TestRun_CancelledRunKillsTheChild(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix process-group path")
	}
	// The stub ignores its argv and wedges like a hung claude whose own child
	// (here: sleep) inherits stdout. Without the process-group kill, cancel
	// would kill only the script itself and Run would stay blocked on the pipe
	// until sleep exited on its own. It touches `started` first so the test
	// cancels only once the stub is provably running: cancelling after a bare
	// sleep can, on a stalled machine, land before the stub even spawns — and
	// then the test proves nothing about killing a live process tree.
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude-stub")
	started := filepath.Join(dir, "started")
	script := "#!/bin/sh\ntouch '" + started + "'\nsleep 30\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}

	r := NewClaudeCLIRunner(WithBinary(stub))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan error, 1)
	go func() {
		_, err := r.Run(ctx, NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
		done <- err
	}()

	waitForFile(t, started)
	cancel()

	var err error
	select {
	case err = <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after its context was cancelled")
	}
	if err == nil {
		t.Fatal("a cancelled node must not report success")
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected a context.Canceled error, got %T: %v", err, err)
	}
}

// waitForFile polls for path until it exists, failing the test at a deadline.
// It is the start signal for the real-spawn cancellation test above.
func waitForFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("stub never signalled it started (no %s)", path)
}

// --- per-invocation timeout (ADR 0007) ----------------------------------------

// TestRun_InvocationTimeoutBoundsTheRun proves a node's declared `timeout:`
// actually governs the run: with the runner still on its 20m default, an
// invocation carrying a tiny Timeout must be killed by the deadline, promptly,
// and surface as the context error the Scheduler classifies as a run_error.
// Like the cancellation test above this needs a real (free, offline) stub —
// a deadline cannot be proven against code that never runs.
func TestRun_InvocationTimeoutBoundsTheRun(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, "#!/bin/sh\nsleep 30\n")

	r := NewClaudeCLIRunner(WithBinary(stub))
	start := time.Now()
	_, err := r.Run(context.Background(), NodeInvocation{
		Prompt:         testPrompt,
		PermissionMode: "dontAsk",
		Timeout:        100 * time.Millisecond,
	})
	if err == nil {
		t.Fatal("a timed-out node must not report success")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected a context.DeadlineExceeded error, got %T: %v", err, err)
	}
	// The expiry is named in the run's own terms, not the raw Go plumbing
	// string — this message is what the ledger detail and events carry.
	if !strings.Contains(err.Error(), "timed out after 100ms (node timeout)") {
		t.Errorf("a node-timeout expiry must name itself, got %q", err)
	}
	if elapsed := time.Since(start); elapsed > 5*time.Second {
		t.Fatalf("Run took %s to honour a 100ms invocation timeout", elapsed)
	}
}

// TestRun_ZeroInvocationTimeoutFallsBackToRunnerDefault pins the other half of
// the contract: an invocation that declared nothing (Timeout zero) runs under
// the runner's own timeout, so no node is ever unbounded. The runner's timeout
// is shrunk to make the fallback observable without waiting 20 minutes.
func TestRun_ZeroInvocationTimeoutFallsBackToRunnerDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, "#!/bin/sh\nsleep 30\n")

	r := NewClaudeCLIRunner(WithBinary(stub), WithTimeout(100*time.Millisecond))
	_, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected the runner's own timeout to fire, got %T: %v", err, err)
	}
	if !strings.Contains(err.Error(), "timed out after 100ms (node timeout)") {
		t.Errorf("the default-timeout expiry must name itself too, got %q", err)
	}
}
