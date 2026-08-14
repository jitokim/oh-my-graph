package runner

import (
	"encoding/json"
	"strconv"
	"strings"
)

const defaultClaudeBinary = "claude"

type claudeProtocol struct{}

func (claudeProtocol) runtime() Runtime { return RuntimeClaude }
func (claudeProtocol) binary() string   { return defaultClaudeBinary }

func (claudeProtocol) buildArgs(spec NodeInvocation) []string {
	args := []string{
		"-p", spec.Prompt,
		"--output-format", "json",
		"--permission-mode", spec.PermissionMode,
	}
	if spec.BudgetUSD > 0 {
		args = append(args, "--max-budget-usd", strconv.FormatFloat(spec.BudgetUSD, 'f', -1, 64))
	}
	policy := spec.Policy
	if policy.SettingSources != nil {
		args = append(args, "--setting-sources", *policy.SettingSources)
	}
	for _, dir := range policy.PluginDirs {
		args = append(args, "--plugin-dir", dir)
	}
	if spec.Agent != "" {
		args = append(args, "--agent", spec.Agent)
	}
	if len(policy.AllowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(policy.AllowedTools, ","))
	}
	if policy.Tools != nil {
		args = append(args, "--tools", strings.Join(policy.Tools, ","))
	}
	if policy.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
	if len(policy.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(policy.DisallowedTools, ","))
	}
	if spec.ResumeSession != "" {
		args = append(args, "--resume", spec.ResumeSession)
	}
	if spec.SessionID != "" {
		args = append(args, "--session-id", spec.SessionID)
	}
	return args
}

const budgetExhaustedSubtype = "error_max_budget_usd"

type claudeEnvelope struct {
	SessionID    string   `json:"session_id"`
	Result       string   `json:"result"`
	TotalCostUSD float64  `json:"total_cost_usd"`
	Subtype      string   `json:"subtype"`
	IsError      bool     `json:"is_error"`
	Errors       []string `json:"errors"`
}

func (env claudeEnvelope) failureCause() string {
	if len(env.Errors) > 0 {
		return flattenLines(strings.Join(env.Errors, "\n"))
	}
	if env.IsError {
		return flattenLines(env.Result)
	}
	return ""
}

func (claudeProtocol) parse(stdout, stderr []byte, sessionStarted func(string)) (NodeOutcome, error) {
	outcome, err := parseEnvelope(stdout, stderr)
	if err == nil && sessionStarted != nil && outcome.SessionID != "" {
		sessionStarted(outcome.SessionID)
	}
	return outcome, err
}

func parseEnvelope(stdout, stderr []byte) (NodeOutcome, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return NodeOutcome{}, &NodeOutputError{
			Runtime: string(RuntimeClaude),
			Reason:  "claude produced no output",
			Stderr:  tailOf(stderr, maxStderrInError),
		}
	}
	var env claudeEnvelope
	if err := json.Unmarshal([]byte(trimmed), &env); err != nil {
		return NodeOutcome{}, &NodeOutputError{
			Runtime: string(RuntimeClaude),
			Reason:  "claude output was not a JSON envelope",
			Output:  trimmed,
			Stderr:  tailOf(stderr, maxStderrInError),
			Err:     err,
		}
	}
	return NodeOutcome{
		SessionID:       env.SessionID,
		Result:          env.Result,
		TotalCostUSD:    env.TotalCostUSD,
		BudgetExhausted: env.Subtype == budgetExhaustedSubtype,
		FailureCause:    env.failureCause(),
	}, nil
}
