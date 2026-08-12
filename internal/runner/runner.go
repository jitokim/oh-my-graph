// Package runner is the claude-execution seam of oh-my-graph. It defines the
// NodeRunner interface the Scheduler depends on, the value types crossing that
// boundary, and two implementations: ClaudeCLIRunner (one of the exactly
// four objects in the whole program that touch os/exec —
// verify.ShellVerifier, worktree.GitManager and browser.ExecOpener are the
// others; see ADR 0002, ADR 0005 and ADR 0006) and FakeRunner (scripted, for
// tests).
//
// The seam exists so the entire scheduler — topological order, fan-out, fan-in,
// retry, halt-on-fail, cost summation — is unit-testable against FakeRunner with
// zero real claude subprocesses. The Scheduler never learns whether a real
// claude ran; it only ever sees a NodeOutcome or an error.
package runner

import (
	"context"
	"time"
)

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
	// PluginDirs renders as one --plugin-dir <dir> per entry. It is NOT a sixth
	// ceiling layer and must not be read as one: it ADDS definitions — skill
	// bodies (ADR 0017) or the one agent a node was mapped onto (ADR 0022) —
	// and grants no capability at all. A skill's body can only reach tools this
	// policy's other five layers already permit, the Skill tool itself exists
	// only when layer 3's Tools names it (ADR 0017 (f): --plugin-dir with
	// --tools Read and no Skill loads the definitions and cannot run them), and
	// a staged agent's frontmatter does not widen past --tools either
	// (DESIGN.md E6; ADR 0022's K-FM-GIT re-ran it on a staged definition).
	//
	// It exists because layer 1 stays "": --setting-sources "" withholds the
	// DEFINITIONS along with the user's settings — skills and agents alike —
	// and a plugin directory is the one source of definitions that does not
	// reopen layer 1 (ADR 0017, measurements (f) and (g); ADR 0022,
	// measurement (k)). Empty omits the flag, which is every hand-written graph
	// and every planned run that neither activated nor mapped.
	//
	// A directory that does not exist is accepted silently by the CLI for
	// SKILLS — the node simply has none — and is a NODE FAILURE for an agent,
	// since `--agent <name>` then resolves nothing and the CLI exits 1. Either
	// way a value here is only as good as whatever guarantees the directory is
	// present: see coordinator.SkillStaging and coordinator.AgentStaging, both
	// of which re-materialize before every spawn rather than trusting the path.
	PluginDirs []string
}

// NodeInvocation is everything the runner needs to launch one node. It is a
// rendered spec: Prompt and Cwd are already interpolated by the Handoff, and
// ResumeSession is empty unless this node is resuming a session-parent.
type NodeInvocation struct {
	Prompt         string
	Cwd            string
	PermissionMode string
	ResumeSession  string
	// SessionID, when non-empty, is the PRE-ASSIGNED claude session id for this
	// invocation, rendered as `--session-id <uuid>` ("Use a specific session ID
	// for the conversation (must be a valid UUID)" — claude --help, verified
	// 2026-08-02, help text only). Assigning the id before the child spawns is
	// what lets the scheduler publish it on node_started, so a live view can
	// locate a RUNNING node's transcript instead of waiting for the terminal
	// envelope. Produce it with NewSessionID.
	//
	// Mutually exclusive with ResumeSession: a resuming node continues an
	// EXISTING session (whose id is already known — it is the resume target),
	// so the scheduler never sets both. Empty means "let claude mint the id",
	// which is exactly the pre-flag behaviour.
	SessionID string
	// Agent, when non-empty, is the name of a Claude Code subagent (as defined
	// in the user's own ~/.claude/agents or <cwd>/.claude/agents) this node
	// runs as, rendered as `--agent <name>`. The node then inherits that
	// subagent's system prompt, tools and model. Empty means plain `claude -p`.
	//
	// An unresolvable name is a NODE FAILURE, not a fallback: claude exits
	// non-zero having printed nothing on stdout (measured on 2.1.220), so the
	// node fails with a *NodeOutputError carrying the CLI's own stderr, which
	// names every agent that IS available. A PLAN may not set it
	// (coordinator.validatePlannedNodes) — it arrives on a planned node only
	// from trusted code, after validation (coordinator.applyAgentMapping).
	//
	// It is NOT mutually exclusive with ToolPolicy.SettingSources = "", and the
	// comment here said otherwise until 2026-08-12. `--setting-sources ""`
	// disables DISCOVERY of agent definitions (DESIGN.md, E2), which a
	// ToolPolicy.PluginDirs entry carrying the definition routes around: a
	// mapped planned node runs both flags together and resolves the staged copy
	// (ADR 0022). A hand-written node's agent is still discovered, because
	// oh-my-graph stages nothing for it and its policy imposes no layer 1.
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
	// Timeout, when positive, bounds this node's whole run, replacing the
	// runner's own default (20m). It carries the node's `timeout:` declaration,
	// already parsed and proven positive at load (graph.Node.TimeoutDuration —
	// ADR 0007). Zero means "the node declared none": the runner applies its
	// default, so an invocation is never unbounded either way. Like BudgetUSD
	// it stays outside Policy — it bounds wall-clock, not capability.
	Timeout time.Duration
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
	// FailureCause is a concise single-line explanation of WHY the subprocess
	// failed, present only when the run actually failed (an error envelope, or
	// a non-zero exit). ClaudeCLIRunner fills it from the most informative
	// source available — the errors the CLI reported inside its own JSON
	// envelope, else the tail of its stderr — so a failure detail can name the
	// real cause (a subscription session limit, say) instead of only "exit
	// code 1". Empty on success and when the child left no diagnosis.
	FailureCause string
	// BudgetExhausted is true when claude aborted the run because its own
	// --max-budget-usd cap was reached (envelope subtype error_max_budget_usd).
	// It is a budget failure, not the generic non-zero exit its ExitCode would
	// otherwise be read as: the Scheduler turns it into a *NodeBudgetError so it
	// shares the post-hoc check's budget_exceeded retry token and ledger
	// language instead of being retried by a plain nonzero_exit policy.
	BudgetExhausted bool
	// SessionLimited is true when the run died because the user's subscription
	// hit its session limit — FailureCause matched the limit's message shape
	// (sessionlimit.go, ADR 0009). Unlike every other failure this one is not
	// the node's fault and not final: the Scheduler treats it as a pause (the
	// node stays un-run, the run drains and exits resumable) rather than a
	// FAIL. FailureCause still carries the full message, including the "resets
	// <time>" hint the CLI prints.
	SessionLimited bool
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
