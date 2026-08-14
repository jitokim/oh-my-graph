package runner

import (
	"context"
	"errors"
	"os"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestCodexBuildArgsFreshReadOnly(t *testing.T) {
	r := NewCLIRunner(RuntimeCodex)
	got := r.buildArgs(NodeInvocation{
		Prompt:         "inspect only",
		PermissionMode: "plan",
	})
	want := []string{
		"exec",
		"--json",
		"--color", "never",
		"--skip-git-repo-check",
		"--sandbox", "read-only",
		"--config", `approval_policy="never"`,
		"inspect only",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Codex argv = %#v, want %#v", got, want)
	}
}

func TestCodexBuildArgsResumePlacesExecOptionsBeforeSubcommand(t *testing.T) {
	r := NewCLIRunner(RuntimeCodex)
	got := r.buildArgs(NodeInvocation{
		Prompt:         "continue",
		PermissionMode: "dontAsk",
		ResumeSession:  "019c5a2b-62d5-7d81-98a7-68c9f4d84f84",
	})
	want := []string{
		"exec",
		"--json",
		"--color", "never",
		"--skip-git-repo-check",
		"--sandbox", "workspace-write",
		"--config", `approval_policy="never"`,
		"resume", "019c5a2b-62d5-7d81-98a7-68c9f4d84f84", "continue",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Codex resume argv = %#v, want %#v", got, want)
	}
}

func TestCodexBuildArgsMapsBypassToDangerFullAccess(t *testing.T) {
	r := NewCLIRunner(RuntimeCodex)
	got := r.buildArgs(NodeInvocation{Prompt: "work", PermissionMode: "bypassPermissions"})
	if !containsSequence(got, "--sandbox", "danger-full-access") {
		t.Fatalf("Codex bypass argv = %#v, want danger-full-access sandbox", got)
	}
}

func TestCodexBuildArgsIsolatesAutoOwnedInvocation(t *testing.T) {
	empty := ""
	r := NewCLIRunner(RuntimeCodex)
	got := r.buildArgs(NodeInvocation{
		Prompt: "work",
		Policy: ToolPolicy{SettingSources: &empty},
	})
	for _, sequence := range [][]string{
		{"--ignore-user-config"},
		{"--ignore-rules"},
		{"--config", "project_doc_max_bytes=0"},
		{"--config", "mcp_servers={}"},
	} {
		if !containsSequence(got, sequence...) {
			t.Errorf("Codex isolated argv = %#v, missing %#v", got, sequence)
		}
	}
}

func TestParseCodexJSONLReturnsThreadReplyAndUsage(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"019c5a2b-62d5-7d81-98a7-68c9f4d84f84"}`,
		`{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"PASS done"}}`,
		`{"type":"turn.completed","usage":{"input_tokens":11,"cached_input_tokens":3,"output_tokens":2,"reasoning_output_tokens":1}}`,
	}, "\n")
	var sessions []string
	got, err := parseCodexJSONL([]byte(raw), nil, func(id string) { sessions = append(sessions, id) })
	if err != nil {
		t.Fatalf("parseCodexJSONL: %v", err)
	}
	if got.SessionID != "019c5a2b-62d5-7d81-98a7-68c9f4d84f84" || got.Result != "PASS done" {
		t.Errorf("outcome = %+v", got)
	}
	if !got.CostUnknown {
		t.Error("Codex USD cost was marked known")
	}
	wantUsage := TokenUsage{InputTokens: 11, CachedInputTokens: 3, OutputTokens: 2, ReasoningOutputTokens: 1}
	if got.Usage != wantUsage {
		t.Errorf("usage = %+v, want %+v", got.Usage, wantUsage)
	}
	if !reflect.DeepEqual(sessions, []string{"019c5a2b-62d5-7d81-98a7-68c9f4d84f84"}) {
		t.Errorf("session callback = %q", sessions)
	}
}

func TestParseCodexJSONLUsesLastAgentMessage(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"draft"}}`,
		`{"type":"item.completed","item":{"type":"command_execution","command":"go test"}}`,
		`{"type":"item.completed","item":{"type":"agent_message","text":"final"}}`,
		`{"type":"turn.completed","usage":{}}`,
	}, "\n")
	got, err := parseCodexJSONL([]byte(raw), nil, nil)
	if err != nil {
		t.Fatalf("parseCodexJSONL: %v", err)
	}
	if got.Result != "final" {
		t.Errorf("result = %q, want final agent message", got.Result)
	}
}

func TestParseCodexJSONLRejectsMalformedOrIncompleteStream(t *testing.T) {
	for _, raw := range []string{
		`not-json`,
		`{"type":"thread.started","thread_id":"thread-1"}`,
		"{\"type\":\"thread.started\",\"thread_id\":\"thread-1\"}\n{\"type\":\"turn.completed\",\"usage\":{}}",
	} {
		if _, err := parseCodexJSONL([]byte(raw), []byte("diagnosis"), nil); err == nil {
			t.Errorf("parseCodexJSONL(%q) succeeded", raw)
		}
	}
}

func TestParseCodexJSONLTurnFailureIsANonzeroOutcome(t *testing.T) {
	raw := strings.Join([]string{
		`{"type":"thread.started","thread_id":"thread-1"}`,
		`{"type":"turn.failed","error":{"message":"model unavailable"}}`,
	}, "\n")
	got, err := parseCodexJSONL([]byte(raw), nil, nil)
	if err != nil {
		t.Fatalf("parseCodexJSONL: %v", err)
	}
	if got.ExitCode == 0 || got.FailureCause != "model unavailable" {
		t.Errorf("failed turn outcome = %+v, want nonzero with the reported cause", got)
	}
}

func TestCodexRunPublishesThreadWhileProcessIsRunning(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	release := t.TempDir() + "/release"
	stub := writeStub(t, `#!/bin/sh
[ -n "$OMG_TEST_WARMUP" ] && exit 0
echo '{"type":"thread.started","thread_id":"thread-live"}'
while [ ! -f '`+release+`' ]; do sleep 0.01; done
echo '{"type":"item.completed","item":{"type":"agent_message","text":"PASS"}}'
echo '{"type":"turn.completed","usage":{"input_tokens":1,"output_tokens":1}}'
`)
	warmStubExec(t, stub)

	started := make(chan string, 1)
	done := make(chan error, 1)
	r := NewCLIRunner(RuntimeCodex, WithBinary(stub), WithTimeout(2*time.Second))
	go func() {
		_, err := r.Run(context.Background(), NodeInvocation{
			Prompt: "work",
			SessionStarted: func(id string) {
				started <- id
			},
		})
		done <- err
	}()

	select {
	case id := <-started:
		if id != "thread-live" {
			t.Errorf("session id = %q, want thread-live", id)
		}
	case <-time.After(500 * time.Millisecond):
		_ = os.WriteFile(release, nil, 0o600)
		t.Fatal("thread.started was not published until after the process completed")
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatalf("release stub: %v", err)
	}
	if err := <-done; err != nil {
		t.Fatalf("Run: %v", err)
	}
}

func TestCodexRunReturnsKnownThreadOnTimeout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
[ -n "$OMG_TEST_WARMUP" ] && exit 0
echo '{"type":"thread.started","thread_id":"thread-timeout"}'
sleep 30
`)
	warmStubExec(t, stub)

	started := make(chan string, 1)
	type result struct {
		outcome NodeOutcome
		err     error
	}
	done := make(chan result, 1)
	r := NewCLIRunner(RuntimeCodex, WithBinary(stub), WithTimeout(500*time.Millisecond))
	go func() {
		outcome, err := r.Run(context.Background(), NodeInvocation{
			Prompt: "work", SessionStarted: func(id string) { started <- id },
		})
		done <- result{outcome: outcome, err: err}
	}()
	select {
	case id := <-started:
		if id != "thread-timeout" {
			t.Fatalf("published session = %q", id)
		}
	case <-time.After(300 * time.Millisecond):
		t.Fatal("stub did not publish thread.started before timeout")
	}
	got := <-done
	var timeoutErr *NodeTimeoutError
	if !errors.As(got.err, &timeoutErr) {
		t.Fatalf("Run error = %T: %v, want NodeTimeoutError", got.err, got.err)
	}
	if got.outcome.SessionID != "thread-timeout" {
		t.Fatalf("timeout outcome session = %q, want the already-published thread", got.outcome.SessionID)
	}
}

func TestCodexRunReturnsKnownThreadOnParseError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shebang script; this pins the unix path")
	}
	stub := writeStub(t, `#!/bin/sh
echo '{"type":"thread.started","thread_id":"thread-malformed"}'
echo 'not-json'
`)

	r := NewCLIRunner(RuntimeCodex, WithBinary(stub))
	outcome, err := r.Run(context.Background(), NodeInvocation{Prompt: "work"})
	var outputErr *NodeOutputError
	if !errors.As(err, &outputErr) {
		t.Fatalf("Run error = %T: %v, want NodeOutputError", err, err)
	}
	if outcome.SessionID != "thread-malformed" {
		t.Fatalf("parse-error outcome session = %q, want the already-published thread", outcome.SessionID)
	}
}

func containsSequence(args []string, sequence ...string) bool {
	for i := 0; i+len(sequence) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
