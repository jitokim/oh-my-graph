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

func (codexProtocol) prepareSession(_ *NodeInvocation) string {
	return ""
}

func (codexProtocol) sessionFromLine(line []byte) string {
	var event codexEvent
	if json.Unmarshal(line, &event) == nil && event.Type == "thread.started" {
		return event.ThreadID
	}
	return ""
}

// buildArgs deliberately ignores NodeInvocation.Model, and this silence is a
// decision rather than an omission. Codex carries the SAME defect Claude did —
// `--ignore-user-config` above withholds $CODEX_HOME/config.toml, which is where
// the operator's `model` key lives — and the mechanism to repair it exists
// (`codex exec` takes a model flag, and `-c model="..."` overrides the same
// key). It is not used, for one reason: no codex node's model is observable in
// this repository's corpus at all — a codex thread writes no
// ~/.claude/projects transcript — so shipping it would be a fix for a
// population nobody has measured. It also needs a second foreign schema in a
// second format (TOML, and the repo has no dependency for one).
//
// So Codex is the documented-asymmetry runtime here, beside ADR 0009's session
// limits and ADR 0026's budget_usd (ADR 0034 §2.6): docs/LIMITATIONS.md states
// the absence where the user meets it, and the research above is written down
// here and in ADR 0034 §2.6 so the next person does not re-derive it. The
// follow-up itself is carried in the operator's private backlog (oh-my-graph-hq
// notes/open.md), not in the public tracker.
func (codexProtocol) buildArgs(spec NodeInvocation) []string {
	args := []string{
		"exec",
		"--json",
		"--color", "never",
		"--skip-git-repo-check",
		"--sandbox", CodexSandbox(spec.PermissionMode),
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

// CodexSandbox is the filesystem sandbox Codex receives for one graph
// permission_mode. It is exported so the CLI can describe the exact policy it
// will execute instead of presenting Claude's granular tool grants as if
// Codex enforced them.
func CodexSandbox(permissionMode string) string {
	switch permissionMode {
	case "plan":
		return "read-only"
	case "bypassPermissions":
		return "danger-full-access"
	default:
		return "workspace-write"
	}
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
			outcome.ExitCode = 1
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
