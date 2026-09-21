package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// realLimitMessage pins the EXACT message shape observed from claude 2.1.220
// when a subscription session limit kills a run. The matcher is prose
// matching by necessity (the CLI offers no structured signal — ADR 0009), so
// this constant is the contract: if the CLI rewords the message, this test is
// where the one-line fix lands.
const realLimitMessage = "You've hit your session limit · resets 5:20pm"

// realModelLimitMessage pins Claude's OTHER limit sentence, byte for byte —
// URL and trailing clause included, nothing elided — as the is_error envelope's
// result read on run 20260921-071606.434336000-1 (2026-09-21, issue #283) when
// one model's allowance ran out while the account's session was fine. Same
// contract as realLimitMessage: a rewording lands here as a failing test.
const realModelLimitMessage = "You've reached your Fable limit. Switch to another model, or manage usage credits at claude.ai/settings/usage?from=cc_cli_limit_message, to continue."

func TestSessionLimitCause_PinsTheRealMessageShape(t *testing.T) {
	if !isSessionLimitCause(realLimitMessage) {
		t.Fatalf("the matcher must recognize the real CLI message %q", realLimitMessage)
	}
	// The cause may arrive flattened inside a larger report; the match is a
	// substring on purpose.
	if !isSessionLimitCause("API Error: 429 You've hit your session limit · resets 5:20pm") {
		t.Error("the matcher must recognize the message inside a wrapped cause")
	}
}

func TestSessionLimitCause_DoesNotMatchOtherFailures(t *testing.T) {
	for _, cause := range []string{
		"",
		"exit code 1",
		"Reached maximum budget ($0.001)",
		"node output error: claude produced no output",
		"You've hit your rate limit",
		// The per-model sentence is the OTHER Claude pattern's contract
		// (claudeModelLimitPattern). This one stays exactly as narrow as it
		// was: the two are OR'd in claudeProtocol.isLimitCause, never folded,
		// so neither covers the other and a rewording of one cannot move the
		// other's edge.
		realModelLimitMessage,
	} {
		if isSessionLimitCause(cause) {
			t.Errorf("cause %q must not read as a session limit", cause)
		}
	}
}

func TestClaudeModelLimitCause_PinsTheRealMessageShape(t *testing.T) {
	if !isClaudeModelLimitCause(realModelLimitMessage) {
		t.Fatalf("the matcher must recognize the real CLI message %q", realModelLimitMessage)
	}
	// Substring on purpose, like sessionLimitPattern: the cause may arrive
	// with the CLI's own prefix, or already flattened into the exit_zero
	// detail the scheduler builds ("exit code N: " + FailureCause).
	for _, wrapped := range []string{
		"API Error: 429 " + realModelLimitMessage,
		"exit code 1: " + realModelLimitMessage,
	} {
		if !isClaudeModelLimitCause(wrapped) {
			t.Errorf("the matcher must recognize the message inside a wrapped cause: %q", wrapped)
		}
	}
	// The question CLIRunner.Run actually asks: the claude protocol answers
	// true for EITHER of its two sentences.
	protocol := NewCLIRunner(RuntimeClaude).protocol
	for _, cause := range []string{realLimitMessage, realModelLimitMessage} {
		if !protocol.isLimitCause(cause) {
			t.Errorf("claude protocol isLimitCause(%q) = false, want true", cause)
		}
	}
}

// TestClaudeModelLimitCause_DoesNotMatchOtherFailures is the narrow-contract
// test for claudeModelLimitPattern. Every case is a FailureCause shape that
// really reaches the matcher: on a non-zero exit with no envelope cause, the
// node's flattened stderr tail IS the cause (cli.go), so a node whose tools or
// docs merely mention a limit must not turn its own failure into a pause. The
// last cases would have matched a lazier pattern — named per case — and each
// lazier pattern is compiled here and shown to match, so the comment cannot rot
// into a claim.
func TestClaudeModelLimitCause_DoesNotMatchOtherFailures(t *testing.T) {
	lazyNoClause := regexp.MustCompile(`(?i)reached your .{1,40}? limit`)
	lazyOneWord := regexp.MustCompile(`(?i)reached your \w+ limit`)
	lazyBare := regexp.MustCompile(`(?i)limit`)
	for _, tc := range []struct {
		cause string
		lazy  *regexp.Regexp // a pattern that WOULD have matched; nil when none is claimed
	}{
		{"", nil},
		{"exit code 1", nil},
		{"Reached maximum budget ($0.001)", nil},
		{"node output error: claude produced no output", nil},
		{"You've hit your rate limit", lazyBare},
		// The OTHER Claude sentence: sessionLimitPattern's contract, not this one's.
		{realLimitMessage, lazyBare},
		// Codex's sentence, asked under Claude, stays false here too.
		{codexLimitCause(t), lazyBare},
		// Ordinary English a rate limiter prints; `reached your \w+ limit` takes it.
		{"You've reached your rate limit", lazyOneWord},
		// A tool's own quota message; the one-word pattern and a bare `limit` take it.
		{"You've reached your daily limit of 100 requests. Try again tomorrow.", lazyOneWord},
		// gh's quota, in a node's stderr tail; a bare `limit` takes it.
		{"gh: API rate limit exceeded for user", lazyBare},
		// THE false positive this pattern is shaped against: a node's flattened
		// stderr quoting the head of the sentence (a doc naming the wording),
		// then failing for its own reason. `reached your .{1,40}? limit` — the
		// memo's pattern minus the "Switch to another model" clause — pauses the
		// run on this; keeping the clause is what rejects it.
		{"warning: docs/LIMITATIONS.md still names the wording as 'reached your Fable limit' / exit 1", lazyNoClause},
	} {
		if isClaudeModelLimitCause(tc.cause) {
			t.Errorf("cause %q must not read as a model limit", tc.cause)
		}
		if tc.lazy != nil && !tc.lazy.MatchString(tc.cause) {
			t.Errorf("the lazier pattern %q was claimed to match %q and does not; the case is not the contract it says it is", tc.lazy, tc.cause)
		}
	}
}

func TestSessionLimitReset_BestEffort(t *testing.T) {
	cases := map[string]string{
		realLimitMessage:                                        "5:20pm",
		"You've hit your session limit":                         "",
		"Your limit will reset at 4pm (Asia/Seoul)":             "4pm",
		"hit your session limit · resets 10:00am · retry later": "10:00am",
		"exit code 1": "",
		// The per-model sentence names no reset time at all ("settings" and
		// "usage" do not contain "reset"); the hint prints without one.
		realModelLimitMessage: "",
	}
	for cause, want := range cases {
		if got := SessionLimitReset(cause); got != want {
			t.Errorf("SessionLimitReset(%q) = %q, want %q", cause, got, want)
		}
	}
}

// TestRun_ClassifiesSessionLimitFromEnvelope proves the classification happens
// where ADR 0009 says it does — in CLIRunner.Run, on the captured
// failure cause — so the scheduler receives a typed SessionLimited outcome,
// never a string it has to re-interpret.
func TestRun_ClassifiesSessionLimitFromEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
cat <<'JSON'
{"session_id":"s-limit","result":"You've hit your session limit · resets 5:20pm","total_cost_usd":0,"is_error":true}
JSON
exit 1
`)
	r := NewCLIRunner(RuntimeClaude, WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("a limit-killed run with a parseable envelope is an outcome, not a Run error: %v", err)
	}
	if !outcome.SessionLimited {
		t.Fatalf("SessionLimited = false, want true (FailureCause %q)", outcome.FailureCause)
	}
	if got := SessionLimitReset(outcome.FailureCause); got != "5:20pm" {
		t.Errorf("reset hint from the captured cause = %q, want 5:20pm", got)
	}
}

// TestRun_ClassifiesModelLimitFromEnvelope mirrors
// TestRun_ClassifiesSessionLimitFromEnvelope for Claude's per-model sentence,
// with the one thing that run showed and the session-limit fixture never did:
// the envelope carried a real spend. The runner must classify it as a limit AND
// keep the cost on the outcome — whatever drops the 4.6529 from the ledger is
// downstream of here (the scheduler's SessionLimited branch), pinned there, not
// hidden by a runner that zeroed it.
func TestRun_ClassifiesModelLimitFromEnvelope(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, "#!/bin/sh\ncat <<'JSON'\n"+
		`{"session_id":"s-model-limit","result":"`+realModelLimitMessage+`","total_cost_usd":4.6529,"is_error":true}`+
		"\nJSON\nexit 1\n")
	r := NewCLIRunner(RuntimeClaude, WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
	if err != nil {
		t.Fatalf("a limit-killed run with a parseable envelope is an outcome, not a Run error: %v", err)
	}
	if !outcome.SessionLimited {
		t.Fatalf("SessionLimited = false, want true (FailureCause %q)", outcome.FailureCause)
	}
	if outcome.FailureCause != realModelLimitMessage {
		t.Errorf("FailureCause = %q, want the CLI's sentence untouched", outcome.FailureCause)
	}
	if outcome.TotalCostUSD != 4.6529 {
		t.Errorf("TotalCostUSD = %v, want 4.6529: the runner preserves the spend a limited node made", outcome.TotalCostUSD)
	}
	if got := SessionLimitReset(outcome.FailureCause); got != "" {
		t.Errorf("reset hint from a sentence that names no time = %q, want \"\"", got)
	}
}

// The fixture is the WHOLE stream `codex exec --json` wrote when this machine's
// Codex login hit its usage limit — four records, captured 2026-09-04, nothing
// synthesised and nothing elided:
//
//	{"type":"thread.started","thread_id":"…"}
//	{"type":"turn.started"}
//	{"type":"error","message":"You've hit your usage limit. …"}
//	{"type":"turn.failed","error":{"message":"You've hit your usage limit. …"}}
//
// It is the whole stream rather than the interesting half on purpose. An
// earlier capture held only the last two records with their messages elided,
// so the test had to prepend a synthetic `thread.started` for the parser to
// accept the stream at all — and a fixture that carries scaffolding is a
// fixture that can drift from what the CLI does without the test noticing.
// The limit reproduces on demand until the quota resets, so the real capture
// cost one command.
//
// Records are pulled out by TYPE, never by index, so a capture that gains a
// record does not silently shift what these tests are asserting about.

func codexLimitRecord(t *testing.T, kind string) string {
	t.Helper()
	for _, line := range strings.Split(codexLimitRecords(t), "\n") {
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(line), &probe); err != nil {
			t.Fatalf("fixture line is not JSON: %v", err)
		}
		if probe.Type == kind {
			return line
		}
	}
	t.Fatalf("the recorded stream carries no %q record", kind)
	return ""
}

func codexLimitRecords(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "codex-usage-limit.jsonl"))
	if err != nil {
		t.Fatalf("reading the recorded codex limit stream: %v", err)
	}
	stream := strings.TrimSpace(string(raw))
	if got := len(strings.Split(stream, "\n")); got != 4 {
		t.Fatalf("the fixture is the whole recorded stream, four records; got %d lines", got)
	}
	return stream
}

// codexLimitCause is the FailureCause the parser really produces from the
// recorded stream — read through parseCodexJSONL rather than retyped, so the
// matcher table below is tested against the string the engine will actually
// hand it.
func codexLimitCause(t *testing.T) string {
	t.Helper()
	outcome, err := parseCodexJSONL([]byte(codexLimitRecords(t)), nil, nil)
	if err != nil {
		t.Fatalf("the recorded limit stream must parse: %v", err)
	}
	if outcome.FailureCause == "" {
		t.Fatal("the recorded turn.failed must reach the engine as a FailureCause")
	}
	return outcome.FailureCause
}

// TestLimitCause_MatchesEachRuntimesOwnWordingOnly pins both halves of decision
// 1: Codex's limit is recognized from the recorded wording, and neither
// runtime's pattern reaches into the other's. The negative cases are shapes
// that actually occur — `turn.failed` is Codex's single terminal-failure
// record, so "not a limit" has to be decided on the sentence it carries.
//
// The question is put the way CLIRunner.Run puts it — select the runtime, ask
// the protocol it selected — so this table also pins the wiring from a
// --runtime value to the pattern that answers for it.
func TestLimitCause_MatchesEachRuntimesOwnWordingOnly(t *testing.T) {
	codexCause := codexLimitCause(t)
	for _, tc := range []struct {
		name    string
		runtime Runtime
		cause   string
		want    bool
	}{
		{"codex: the recorded usage limit", RuntimeCodex, codexCause, true},
		{"codex: the same cause flattened into a wider report", RuntimeCodex, "codex run failed / " + codexCause, true},
		{"codex: a turn.failed from an unavailable model", RuntimeCodex, "model unavailable", false},
		{"codex: the stubbed turn.failed the cmd tests script", RuntimeCodex, "stub codex: failing on request", false},
		{"codex: the fallback cause for an error-less turn.failed", RuntimeCodex, "codex turn failed", false},
		{"codex: a network-blocked run", RuntimeCodex, "error connecting to api.github.com", false},
		{"codex: no failure at all", RuntimeCodex, "", false},
		{"claude: the recorded session limit still matches", RuntimeClaude, realLimitMessage, true},
		{"claude: a rate limit is still not a session limit", RuntimeClaude, "You've hit your rate limit", false},
		{"claude: the recorded per-model limit is a limit too", RuntimeClaude, realModelLimitMessage, true},
		{"claude: the per-model limit flattened into a wider report", RuntimeClaude, "API Error: 429 " + realModelLimitMessage, true},
		{"claude's wording does not match under codex", RuntimeCodex, realLimitMessage, false},
		{"claude's per-model wording does not match under codex", RuntimeCodex, realModelLimitMessage, false},
		{"codex's wording does not match under claude", RuntimeClaude, codexCause, false},
		// A runtime no protocol claims cannot reach here from the CLI —
		// ParseRuntime rejects it — and NewCLIRunner falls back to the claude
		// protocol, which owes nothing to Codex's wording. Kept as the third
		// direction of the same rule: an unrecognized selection gets some
		// protocol's narrow pattern, never a permissive one.
		{"an unnamed runtime is owed no codex signal", Runtime("gemini"), codexCause, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			protocol := NewCLIRunner(tc.runtime).protocol
			if got := protocol.isLimitCause(tc.cause); got != tc.want {
				t.Errorf("%q protocol isLimitCause(%q) = %v, want %v", tc.runtime, tc.cause, got, tc.want)
			}
		})
	}
}

// TestRun_ClassifiesCodexUsageLimitFromTheRecordedStream is the Codex mirror of
// TestRun_ClassifiesSessionLimitFromEnvelope: it proves the classification
// happens in CLIRunner.Run, so the scheduler receives the SAME typed
// NodeOutcome.SessionLimited the Claude path already sets — no second
// vocabulary for the same condition. FakeRunner cannot stand in here, because
// it bypasses CLIRunner entirely; this spawns a shell stub, never real codex.
func TestRun_ClassifiesCodexUsageLimitFromTheRecordedStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	errRecord := codexLimitRecord(t, "error")
	turnFailed := codexLimitRecord(t, "turn.failed")
	// The real opening record, not a placeholder: parseCodexJSONL rejects a
	// stream without one, and the capture supplies it.
	threadStarted := codexLimitRecord(t, "thread.started")
	for _, tc := range []struct {
		name        string
		stream      []string
		exit        int
		wantLimited bool
	}{
		{
			name:        "the recorded usage-limit stream is a limit",
			stream:      []string{threadStarted, errRecord, turnFailed},
			exit:        1,
			wantLimited: true,
		},
		{
			name:        "a turn.failed from another cause is an ordinary failure",
			stream:      []string{threadStarted, `{"type":"turn.failed","error":{"message":"model unavailable"}}`},
			exit:        1,
			wantLimited: false,
		},
		{
			name: "a completed turn is never limited",
			stream: []string{
				threadStarted,
				`{"type":"item.completed","item":{"type":"agent_message","text":"done"}}`,
				`{"type":"turn.completed","usage":{}}`,
			},
			exit:        0,
			wantLimited: false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			stub := writeStub(t, "#!/bin/sh\ncat <<'JSON'\n"+strings.Join(tc.stream, "\n")+"\nJSON\nexit "+strconv.Itoa(tc.exit)+"\n")
			r := NewCLIRunner(RuntimeCodex, WithBinary(stub))
			outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: testPrompt, PermissionMode: "dontAsk"})
			if err != nil {
				t.Fatalf("a parseable codex stream is an outcome, not a Run error: %v", err)
			}
			if outcome.SessionLimited != tc.wantLimited {
				t.Fatalf("SessionLimited = %v, want %v (FailureCause %q)", outcome.SessionLimited, tc.wantLimited, outcome.FailureCause)
			}
			if tc.wantLimited && !strings.Contains(outcome.FailureCause, "hit your usage limit") {
				t.Errorf("the limit cause must still carry the CLI's own message; got %q", outcome.FailureCause)
			}
		})
	}
}

// TestSessionLimitReset_CarriesCodexProseUntouched reads the reset hint out of
// BOTH recorded records — the leading error record the parser never decodes,
// and the FailureCause the engine actually holds. That the second one yields a
// time is the thing the elided capture hid: the reset clause is not stranded in
// an undecoded record, so a Codex pause prints a time like a Claude one.
//
// It is carried as the CLI wrote it and never turned into a clock:
// "Sep 13th, 2026 10:04 PM" names no timezone, and ADR 0009 already refused to
// sleep on a weaker version of this string.
func TestSessionLimitReset_CarriesCodexProseUntouched(t *testing.T) {
	const want = "Sep 13th, 2026 10:04 PM"

	errRecord := codexLimitRecord(t, "error")
	var record struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal([]byte(errRecord), &record); err != nil {
		t.Fatalf("the recorded error record must be JSON: %v", err)
	}
	if got := SessionLimitReset(record.Message); got != want {
		t.Errorf("SessionLimitReset(%q) = %q, want %q", record.Message, got, want)
	}

	cause := codexLimitCause(t)
	if got := SessionLimitReset(cause); got != want {
		t.Errorf("reset hint from the cause the engine holds = %q, want %q (cause %q)", got, want, cause)
	}
}
