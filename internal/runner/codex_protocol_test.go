package runner

import (
	"reflect"
	"strings"
	"testing"
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

func containsSequence(args []string, sequence ...string) bool {
	for i := 0; i+len(sequence) <= len(args); i++ {
		if reflect.DeepEqual(args[i:i+len(sequence)], sequence) {
			return true
		}
	}
	return false
}
