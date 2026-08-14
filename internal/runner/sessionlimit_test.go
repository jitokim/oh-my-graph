package runner

import (
	"context"
	"runtime"
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
