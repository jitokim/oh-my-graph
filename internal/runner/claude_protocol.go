package runner

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

const defaultClaudeBinary = "claude"

// CompoundShellRule is appended to the system prompt of a claude node that
// NeedsCompoundShellRule picks out (#380). Under dontAsk a Bash prefix rule
// such as `Bash(gh *)` admits a command only when the whole command plainly
// matches it, so a node's own `gh pr ready 379; echo "exit=$?"` was denied
// even though every program in it was one the node held. It travels as
// --append-system-prompt rather than inside -p so the rendered prompt — the
// author's text, and what a session handoff resumes — is left as it was.
const CompoundShellRule = "Run exactly one command per Bash call. " +
	"This node runs under dontAsk, and a command only runs when it plainly matches a granted prefix, " +
	"so do not write compound shell: no `;`, `&&`, `||`, pipes, `$(...)` or backticks, " +
	"no redirections such as `2>/dev/null`, and no `cd <dir> && ...`. " +
	"To act in another directory, use the tool's own flag (`git -C <dir>`, `make -C <dir>`). " +
	"The exit status is already reported to you, so never append `; echo $?`. " +
	"If a command you need is denied, do not look for a workaround: report it."

// NeedsCompoundShellRule reports whether a claude node should carry
// CompoundShellRule: it runs under dontAsk, holds at least one scoped Bash
// rule (`Bash(gh *)`, `Bash(git status*)`), and holds no blanket Bash grant
// (bare `Bash` or `Bash(*)`), under which a compound command runs anyway.
func NeedsCompoundShellRule(permissionMode string, allowedTools []string) bool {
	if permissionMode != graph.PermissionDontAsk {
		return false
	}
	scoped := false
	for _, tool := range allowedTools {
		switch {
		case tool == "Bash" || tool == "Bash(*)":
			return false
		case strings.HasPrefix(tool, "Bash(") && strings.HasSuffix(tool, ")"):
			scoped = true
		}
	}
	return scoped
}

type claudeProtocol struct{}

func (claudeProtocol) runtime() Runtime { return RuntimeClaude }
func (claudeProtocol) binary() string   { return defaultClaudeBinary }

// appendsCompoundShellRule is the one place a claude invocation's
// --append-system-prompt is decided: buildArgs emits the flag on it, and
// CLIRunner.AppendsCompoundShellRule reports it to the run feed, so the record
// and the argv cannot disagree (#380).
func (claudeProtocol) appendsCompoundShellRule(spec NodeInvocation) bool {
	return NeedsCompoundShellRule(spec.PermissionMode, spec.Policy.AllowedTools)
}

// costIncludesResumedSession is true exactly when the invocation resumes a
// session: claude's total_cost_usd then "continues from the total its
// transcript saved" (the CLI's own description of the field, 2.1.295), so the
// figure parse reads is the whole session's, the resumed parent's spend
// included (#398). A cold start — a root, an artifact node, a retry that
// dropped its ResumeSession — reports its own spend alone.
func (claudeProtocol) costIncludesResumedSession(spec NodeInvocation) bool {
	return spec.ResumeSession != ""
}

func (claudeProtocol) prepareSession(spec *NodeInvocation) string {
	if spec.ResumeSession != "" {
		return spec.ResumeSession
	}
	if spec.sessionID == "" {
		spec.sessionID = newClaudeSessionID()
	}
	return spec.sessionID
}

func (claudeProtocol) sessionFromLine(_ []byte) string { return "" }

func (p claudeProtocol) buildArgs(spec NodeInvocation) []string {
	args := []string{
		"-p", spec.Prompt,
		"--output-format", "json",
		"--permission-mode", spec.PermissionMode,
	}
	// The operator's own model choice, verbatim, and never for an agent-mapped
	// node: `--agent` supplies its own model from the definition's frontmatter,
	// which is a choice the operator made in a more specific place. The CLI
	// tracks `modelCli` and `agentSelectedByCli` as distinct model sources and
	// which of them wins is undocumented, so this does not find out by shipping —
	// it declines to send both. Empty means the node expressed no choice and the
	// CLI's own default stands.
	if spec.Model != "" && spec.Agent == "" {
		args = append(args, "--model", spec.Model)
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
	// Not part of the ceiling: it narrows nothing, it tells a node holding only
	// scoped Bash rules the command form those rules admit (#380). A resumed
	// invocation carries it too, since a system prompt is per process.
	if p.appendsCompoundShellRule(spec) {
		args = append(args, "--append-system-prompt", CompoundShellRule)
	}
	if spec.ResumeSession != "" {
		args = append(args, "--resume", spec.ResumeSession)
	}
	if spec.sessionID != "" {
		args = append(args, "--session-id", spec.sessionID)
	}
	return args
}

// newClaudeSessionID returns the canonical UUIDv4 required by Claude's
// --session-id flag. Session allocation belongs to this protocol, not to the
// scheduler, because Codex obtains its corresponding id from thread.started.
func newClaudeSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
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
