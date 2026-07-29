// Package runner is the claude-execution seam of oh-my-graph. It defines the
// NodeRunner interface the Scheduler depends on, the value types crossing that
// boundary, and two implementations: ClaudeCLIRunner (the ONLY object in the
// whole program that touches os/exec) and FakeRunner (scripted, for tests).
//
// The seam exists so the entire scheduler — topological order, fan-out, fan-in,
// retry, halt-on-fail, cost summation — is unit-testable against FakeRunner with
// zero real claude subprocesses. The Scheduler never learns whether a real
// claude ran; it only ever sees a NodeOutcome or an error.
package runner

import "context"

// NodeInvocation is everything the runner needs to launch one node. It is a
// rendered spec: Prompt and Cwd are already interpolated by the Handoff, and
// ResumeSession is empty unless this node is resuming a session-parent.
type NodeInvocation struct {
	Prompt         string
	Cwd            string
	PermissionMode string
	ResumeSession  string
	AllowedTools   []string
	// Agent, when non-empty, is the name of a Claude Code subagent (as defined
	// in ~/.claude/agents or <cwd>/.claude/agents) this node should run as.
	// ClaudeCLIRunner passes it through as `--agent <name>`, which `claude -p`
	// resolves against the user's own subagent definitions — the node then
	// inherits that subagent's system prompt, tools, and model. Empty means
	// "plain claude -p", the v0.1 behaviour.
	Agent string
}

// NodeOutcome is the parsed result of one node run: the claude session id (for
// session handoff and the ledger), the .result text (for success_check and
// artifact handoff), the reported cost, and the process exit code.
type NodeOutcome struct {
	SessionID    string
	Result       string
	TotalCostUSD float64
	ExitCode     int
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
