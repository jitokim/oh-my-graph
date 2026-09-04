package runner

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
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
	} {
		if isSessionLimitCause(cause) {
			t.Errorf("cause %q must not read as a session limit", cause)
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
		{"claude's wording does not match under codex", RuntimeCodex, realLimitMessage, false},
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
