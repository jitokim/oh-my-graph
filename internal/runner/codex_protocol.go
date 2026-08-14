package runner

import (
	"encoding/json"
	"fmt"
	"strings"
)

const defaultCodexBinary = "codex"

type codexProtocol struct{}

func (codexProtocol) runtime() Runtime { return RuntimeCodex }
func (codexProtocol) binary() string   { return defaultCodexBinary }

func (codexProtocol) prepareSession(spec *NodeInvocation) string {
	return spec.ResumeSession
}

func (codexProtocol) buildArgs(spec NodeInvocation) []string {
	sandbox := "workspace-write"
	switch spec.PermissionMode {
	case "plan":
		sandbox = "read-only"
	case "bypassPermissions":
		sandbox = "danger-full-access"
	}
	args := []string{
		"exec",
		"--json",
		"--color", "never",
		"--sandbox", sandbox,
		"--config", `approval_policy="never"`,
	}
	if spec.Policy.SettingSources != nil {
		args = append(args,
			"--ignore-user-config",
			"--ignore-rules",
			"--config", "project_doc_max_bytes=0",
			"--config", "mcp_servers={}",
		)
	}
	if spec.ResumeSession != "" {
		return append(args, "resume", spec.ResumeSession, spec.Prompt)
	}
	return append(args, spec.Prompt)
}

type codexEvent struct {
	Type     string          `json:"type"`
	ThreadID string          `json:"thread_id"`
	Item     codexItem       `json:"item"`
	Usage    TokenUsage      `json:"usage"`
	Error    json.RawMessage `json:"error"`
}

type codexItem struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

func (codexProtocol) parse(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error) {
	return parseCodexJSONL(stdout, stderr, sessionStarted)
}

func parseCodexJSONL(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return NodeOutcome{}, codexOutputError("codex produced no output", trimmed, stderr, nil)
	}

	outcome := NodeOutcome{CostUnknown: true}
	terminal := false
	failed := false
	for index, line := range strings.Split(trimmed, "\n") {
		var event codexEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			return NodeOutcome{}, codexOutputError(fmt.Sprintf("codex output line %d was not a JSON event", index+1), trimmed, stderr, err)
		}
		switch event.Type {
		case "thread.started":
			if event.ThreadID == "" {
				return NodeOutcome{}, codexOutputError("codex thread.started omitted thread_id", trimmed, stderr, nil)
			}
			if outcome.SessionID == "" {
				outcome.SessionID = event.ThreadID
				if sessionStarted != nil {
					sessionStarted(event.ThreadID)
				}
			}
		case "item.completed":
			if event.Item.Type == "agent_message" {
				outcome.Result = event.Item.Text
			}
		case "turn.completed":
			terminal = true
			outcome.Usage = event.Usage
		case "turn.failed":
			terminal = true
			failed = true
			outcome.FailureCause = codexFailureMessage(event.Error)
		}
	}
	if outcome.SessionID == "" {
		return NodeOutcome{}, codexOutputError("codex output contained no thread.started event", trimmed, stderr, nil)
	}
	if !terminal {
		return NodeOutcome{}, codexOutputError("codex output contained no terminal turn event", trimmed, stderr, nil)
	}
	if !failed && strings.TrimSpace(outcome.Result) == "" {
		return NodeOutcome{}, codexOutputError("codex completed without an agent message", trimmed, stderr, nil)
	}
	return outcome, nil
}

func codexFailureMessage(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "codex turn failed"
	}
	var object struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &object) == nil && object.Message != "" {
		return flattenLines(object.Message)
	}
	var message string
	if json.Unmarshal(raw, &message) == nil && message != "" {
		return flattenLines(message)
	}
	return flattenLines(string(raw))
}

func codexOutputError(reason, output string, stderr []byte, err error) *NodeOutputError {
	return &NodeOutputError{
		Runtime: string(RuntimeCodex),
		Reason:  reason,
		Output:  output,
		Stderr:  tailOf(stderr, maxStderrInError),
		Err:     err,
	}
}
