// Package runner is the claude-execution seam of oh-my-graph. It defines the
// NodeRunner interface the Scheduler depends on, the value types crossing that
// boundary, and two implementations: ClaudeCLIRunner (one of the exactly
// three objects in the whole program that touch os/exec —
// verify.ShellVerifier and worktree.GitManager are the others; see ADR 0002
// and ADR 0005) and FakeRunner (scripted, for tests).
//
// The seam exists so the entire scheduler — topological order, fan-out, fan-in,
// retry, halt-on-fail, cost summation — is unit-testable against FakeRunner with
// zero real claude subprocesses. The Scheduler never learns whether a real
// claude ran; it only ever sees a NodeOutcome or an error.
package runner

import "context"

// ToolPolicy is the complete tool ceiling for one node, as ONE value object
// rather than a handful of parallel fields — a caller cannot hand the runner
// three quarters of a ceiling and have it look fine.
//
// The layers are independent mechanisms, deliberately redundant, so a wrong
// assumption about any one of them degrades to a weaker ceiling rather than to
// none (DESIGN.md, "The tool ceiling"):
//
//	1 isolation  SettingSources ("" = load no user/project/local settings)
//	2 grant      AllowedTools   (--allowedTools, scoped patterns bind once 1 applies)
//	3 narrowing  Tools          (--tools, replaces the built-in tool set)
//	4 MCP        StrictMCPConfig(--strict-mcp-config)
//	5 residual   DisallowedTools(--disallowedTools, subtracts and beats a prior allow)
//
// Layer 1 is the load-bearing one, and it is why layer 2 is a real ceiling
// here and was only a declaration before. Measured on claude 2.1.220: with the
// user's own settings granting Bash(*), `--allowedTools "Bash(git *)"` alone
// still permitted an out-of-scope command; adding `--setting-sources ""`
// denied it while leaving `git ...` working. See DESIGN.md, E1.
//
// A hand-written graph gets a policy carrying ONLY AllowedTools: it is the
// user's own reviewed artifact and is meant to run under the user's own
// settings, hooks and MCP servers. The ceiling layers are auto mode's, because
// a planned graph is unreviewed LLM output run unattended.
type ToolPolicy struct {
	// AllowedTools renders as --allowedTools when non-empty.
	AllowedTools []string
	// DisallowedTools renders as --disallowedTools when non-empty.
	DisallowedTools []string
	// Tools renders as --tools, which REPLACES the built-in tool set rather
	// than adding to it — a tool omitted here does not exist for the node, and
	// naming it in AllowedTools does not bring it back (DESIGN.md, E4). nil
	// means "omit the flag" (the CLI's default set); a non-nil empty slice is
	// `--tools ""`, which the CLI documents as "disable all tools".
	Tools []string
	// SettingSources renders as --setting-sources. nil omits the flag (the
	// user's user/project/local settings load as usual); a pointer to "" loads
	// NONE of them, leaving this argv as the only allow-rule source. A pointer
	// is used rather than a plain string so "load none" and "not specified"
	// stay distinguishable — they are opposite policies.
	SettingSources *string
	// StrictMCPConfig renders as --strict-mcp-config: only MCP servers from an
	// explicit --mcp-config are used. oh-my-graph never passes --mcp-config, so
	// for a planned node this means no MCP servers at all.
	StrictMCPConfig bool
}

// NodeInvocation is everything the runner needs to launch one node. It is a
// rendered spec: Prompt and Cwd are already interpolated by the Handoff, and
// ResumeSession is empty unless this node is resuming a session-parent.
type NodeInvocation struct {
	Prompt         string
	Cwd            string
	PermissionMode string
	ResumeSession  string
	// Agent, when non-empty, is the name of a Claude Code subagent (as defined
	// in the user's own ~/.claude/agents or <cwd>/.claude/agents) this node
	// runs as, rendered as `--agent <name>`. The node then inherits that
	// subagent's system prompt, tools and model. Empty means plain `claude -p`.
	//
	// An unresolvable name is a NODE FAILURE, not a fallback: claude exits
	// non-zero having printed nothing on stdout (measured on 2.1.220), so the
	// node fails with a *NodeOutputError carrying the CLI's own stderr, which
	// names every agent that IS available. Hand-written graphs only — a
	// planned node may not set it (coordinator.validatePlannedNodes), and it is
	// mutually exclusive with ToolPolicy.SettingSources = "" anyway, which
	// disables discovery of the user's agent definitions (DESIGN.md, E2).
	Agent string
	// BudgetUSD is the node's declared budget_usd, or 0 when it declared none.
	// A positive value is passed to claude as --max-budget-usd, which makes the
	// CLI abort THIS invocation the moment its own running spend crosses the
	// budget — a real mid-run cost kill, on top of (not replacing) the
	// scheduler's post-hoc ledger check. The bound is per `claude -p` process,
	// so it maps onto oh-my-graph's per-node budget even for a session-resume
	// node (a resumed run does not re-count the parent session's spend). A
	// non-positive value adds no flag and leaves the argv unchanged.
	//
	// A plain scalar, deliberately NOT folded into Policy: it bounds spend, not
	// capability, and the two have no reason to change together.
	BudgetUSD float64
	// Policy is this node's complete tool ceiling.
	Policy ToolPolicy
}

// NodeOutcome is the parsed result of one node run: the claude session id (for
// session handoff and the ledger), the .result text (for success_check and
// artifact handoff), the reported cost, and the process exit code.
type NodeOutcome struct {
	SessionID    string
	Result       string
	TotalCostUSD float64
	ExitCode     int
	// BudgetExhausted is true when claude aborted the run because its own
	// --max-budget-usd cap was reached (envelope subtype error_max_budget_usd).
	// It is a budget failure, not the generic non-zero exit its ExitCode would
	// otherwise be read as: the Scheduler turns it into a *NodeBudgetError so it
	// shares the post-hoc check's budget_exceeded retry token and ledger
	// language instead of being retried by a plain nonzero_exit policy.
	BudgetExhausted bool
}

// NodeRunner runs one node to completion and returns its outcome. A non-nil
// error means the run could not produce a usable outcome at all (spawn failure,
// timeout, or unparseable output) — it is NOT how a node reports a failed
// success_check or a non-zero exit; those travel inside NodeOutcome and are
// judged by the Scheduler. Implementations must never return a zero NodeOutcome
// with a nil error to paper over a failure.
type NodeRunner interface {
	Run(ctx context.Context, spec NodeInvocation) (NodeOutcome, error)
}
