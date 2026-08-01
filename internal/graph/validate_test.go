package graph

import (
	"errors"
	"strings"
	"testing"
	"time"
)

// asValidationError extracts a *GraphValidationError or fails the test.
func asValidationError(t *testing.T, err error) *GraphValidationError {
	t.Helper()
	var vErr *GraphValidationError
	if !errors.As(err, &vErr) {
		t.Fatalf("expected *GraphValidationError, got %T: %v", err, err)
	}
	return vErr
}

// --- failure cases first ----------------------------------------------------

func TestParse_MissingDependency(t *testing.T) {
	_, err := Parse([]byte(`
name: bad
nodes:
  - { id: a, prompt: a, depends_on: [ghost] }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "ghost") {
		t.Fatalf("reason should name the missing dep: %q", vErr.Reason)
	}
}

func TestParse_DirectCycle(t *testing.T) {
	_, err := Parse([]byte(`
name: cyclic
nodes:
  - { id: a, prompt: a, depends_on: [b] }
  - { id: b, prompt: b, depends_on: [a] }
`))
	vErr := asValidationError(t, err)
	if !strings.Contains(vErr.Reason, "cycle") {
		t.Fatalf("reason should mention a cycle: %q", vErr.Reason)
	}
}

func TestParse_ThreeNodeCycle(t *testing.T) {
	_, err := Parse([]byte(`
name: cyclic3
nodes:
  - { id: a, prompt: a, depends_on: [c] }
  - { id: b, prompt: b, depends_on: [a] }
  - { id: c, prompt: c, depends_on: [b] }
`))
	vErr := asValidationError(t, err)
	if !strings.Contains(vErr.Reason, "cycle") {
		t.Fatalf("reason should mention a cycle: %q", vErr.Reason)
	}
}

func TestParse_SelfDependency(t *testing.T) {
	_, err := Parse([]byte(`
name: self
nodes:
  - { id: a, prompt: a, depends_on: [a] }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", vErr.NodeID)
	}
}

func TestParse_DuplicateNodeID(t *testing.T) {
	_, err := Parse([]byte(`
name: dup
nodes:
  - { id: a, prompt: a }
  - { id: a, prompt: a2 }
`))
	vErr := asValidationError(t, err)
	if !strings.Contains(vErr.Reason, "duplicate") {
		t.Fatalf("reason should mention duplicate: %q", vErr.Reason)
	}
}

func TestParse_EmptyNodeID(t *testing.T) {
	_, err := Parse([]byte(`
name: empty-id
nodes:
  - { prompt: a }
`))
	asValidationError(t, err)
}

func TestParse_UnknownType(t *testing.T) {
	_, err := Parse([]byte(`
name: bad-type
nodes:
  - { id: a, prompt: a, type: wizardry }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" || !strings.Contains(vErr.Reason, "type") {
		t.Fatalf("expected type error on node a: %+v", vErr)
	}
}

func TestParse_UnknownHandoff(t *testing.T) {
	_, err := Parse([]byte(`
name: bad-handoff
nodes:
  - { id: a, prompt: a, handoff: telepathy }
`))
	vErr := asValidationError(t, err)
	if !strings.Contains(vErr.Reason, "handoff") {
		t.Fatalf("expected handoff error: %q", vErr.Reason)
	}
}

func TestParse_SessionHandoffMultiParentRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: session-fanin
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - { id: c, prompt: c, depends_on: [a, b], handoff: session }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "c" {
		t.Fatalf("error named node %q, want c", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "session") {
		t.Fatalf("reason should explain the session/fan-in conflict: %q", vErr.Reason)
	}
}

func TestParse_SessionHandoffRootRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: session-root
nodes:
  - { id: a, prompt: a, handoff: session }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "session") {
		t.Fatalf("reason should explain the session/root conflict: %q", vErr.Reason)
	}
}

// TestParse_SessionHandoffGateParent pins which single parents a session child
// may actually resume: a gate parent is rejected at load — a gate spawns no
// subprocess and records no session id, so the resume could only die mid-run —
// while a claude-run parent stays valid.
func TestParse_SessionHandoffGateParent(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		wantErr bool
	}{
		{
			name: "gate parent rejected",
			yaml: `
name: gate-session
nodes:
  - { id: approve, type: gate }
  - { id: child, prompt: child, depends_on: [approve], handoff: session }
`,
			wantErr: true,
		},
		{
			name: "claude-run parent valid",
			yaml: `
name: run-session
nodes:
  - { id: dev, prompt: dev }
  - { id: child, prompt: child, depends_on: [dev], handoff: session }
`,
			wantErr: false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.yaml))
			if !tc.wantErr {
				if err != nil {
					t.Fatalf("expected a valid graph, got: %v", err)
				}
				return
			}
			vErr := asValidationError(t, err)
			if vErr.NodeID != "child" {
				t.Fatalf("error named node %q, want child", vErr.NodeID)
			}
			if !strings.Contains(vErr.Reason, `"approve"`) {
				t.Fatalf("reason should name the gate parent: %q", vErr.Reason)
			}
			if !strings.Contains(vErr.Reason, "handoff: artifact") {
				t.Fatalf("reason should state the remedy: %q", vErr.Reason)
			}
		})
	}
}

func TestParse_InvalidYAML(t *testing.T) {
	_, err := Parse([]byte("name: [unterminated"))
	if err == nil {
		t.Fatal("expected a parse error for malformed YAML")
	}
}

// --- success_check.verify: rejected at LOAD, never mid-run ------------------
//
// Everything below is knowable from the file alone. Discovering any of it while
// the graph is running would mean the user already paid for the nodes that ran
// first — so each one must fail at load, naming the node.

// verifyGraph renders a one-node graph whose verify block is the given YAML.
func verifyGraph(block string) []byte {
	return []byte("name: v\nnodes:\n  - id: check\n    prompt: check\n    success_check:\n      verify:\n" + block)
}

func TestParse_VerifyWithoutCommandRejected(t *testing.T) {
	_, err := Parse(verifyGraph("        timeout: 30s\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "check" {
		t.Fatalf("error named node %q, want check", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "command") {
		t.Fatalf("reason should name the missing command: %q", vErr.Reason)
	}
}

func TestParse_VerifyWithBlankCommandRejected(t *testing.T) {
	// A whitespace-only command is not "no verification": it would reach `sh -c`
	// and exit 0, turning the strictest predicate into an automatic pass.
	_, err := Parse(verifyGraph("        command: \"   \"\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "check" {
		t.Fatalf("error named node %q, want check", vErr.NodeID)
	}
}

func TestParse_VerifyWithUnparseableTimeoutRejected(t *testing.T) {
	_, err := Parse(verifyGraph("        command: make test\n        timeout: 2 minutes\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "check" {
		t.Fatalf("error named node %q, want check", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "2 minutes") {
		t.Fatalf("reason should quote the offending value: %q", vErr.Reason)
	}
}

func TestParse_VerifyTimeoutOverCeilingRejected(t *testing.T) {
	_, err := Parse(verifyGraph("        command: make test\n        timeout: 11m\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "check" {
		t.Fatalf("error named node %q, want check", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, maxVerifyTimeout.String()) {
		t.Fatalf("reason should state the ceiling it broke: %q", vErr.Reason)
	}
}

func TestParse_VerifyNonPositiveTimeoutRejected(t *testing.T) {
	for _, timeout := range []string{"0s", "-1m"} {
		t.Run(timeout, func(t *testing.T) {
			_, err := Parse(verifyGraph("        command: make test\n        timeout: " + timeout + "\n"))

			vErr := asValidationError(t, err)
			if !strings.Contains(vErr.Reason, "positive") {
				t.Fatalf("reason should say the timeout must be positive: %q", vErr.Reason)
			}
		})
	}
}

func TestParse_VerifyWithUncompilableOutputMatchesRejected(t *testing.T) {
	_, err := Parse(verifyGraph("        command: make test\n        output_matches: \"[unclosed\"\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "check" {
		t.Fatalf("error named node %q, want check", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "output_matches") {
		t.Fatalf("reason should name the offending field: %q", vErr.Reason)
	}
}

// --- success cases ----------------------------------------------------------

func TestParse_ValidDiamond(t *testing.T) {
	g, err := Parse([]byte(`
name: diamond
concurrency: 2
inputs: [repo]
nodes:
  - { id: root, prompt: root }
  - { id: left, prompt: left, depends_on: [root] }
  - { id: right, prompt: right, depends_on: [root] }
  - { id: join, prompt: join, depends_on: [left, right] }
`))
	if err != nil {
		t.Fatalf("valid diamond rejected: %v", err)
	}
	if g.Concurrency != 2 {
		t.Errorf("concurrency = %d, want 2", g.Concurrency)
	}
	if got := g.Roots(); len(got) != 1 || got[0] != "root" {
		t.Errorf("roots = %v, want [root]", got)
	}
	if got := g.DependentsOf("root"); len(got) != 2 {
		t.Errorf("dependents of root = %v, want left+right", got)
	}
}

func TestParse_DefaultsNormalized(t *testing.T) {
	g, err := Parse([]byte(`
name: defaults
nodes:
  - { id: a, prompt: a }
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	node, _ := g.NodeByID("a")
	if node.Type != TypeClaudeRun {
		t.Errorf("type defaulted to %q, want %s", node.Type, TypeClaudeRun)
	}
	if node.Handoff != HandoffArtifact {
		t.Errorf("handoff defaulted to %q, want %s", node.Handoff, HandoffArtifact)
	}
}

func TestParse_GateNodeParses(t *testing.T) {
	// A gate node must parse and validate like any other node; what happens
	// at execution time is the Scheduler's concern (it dispatches the node to
	// the injected GateController).
	g, err := Parse([]byte(`
name: with-gate
nodes:
  - { id: a, prompt: a }
  - { id: approve, type: gate, depends_on: [a] }
`))
	if err != nil {
		t.Fatalf("gate node should parse: %v", err)
	}
	node, _ := g.NodeByID("approve")
	if node.Type != TypeGate {
		t.Errorf("gate node type = %q, want %s", node.Type, TypeGate)
	}
}

func TestParse_SessionHandoffSingleParentAllowed(t *testing.T) {
	_, err := Parse([]byte(`
name: session-linear
nodes:
  - { id: dev, prompt: dev }
  - { id: e2e, prompt: e2e, depends_on: [dev], handoff: session }
`))
	if err != nil {
		t.Fatalf("single-parent session handoff should be valid: %v", err)
	}
}

func TestParse_VerifyNormalizedAndParsedAtLoad(t *testing.T) {
	g, err := Parse([]byte(`
name: verified
nodes:
  - id: defaulted
    prompt: defaulted
    success_check:
      verify: { command: "make test" }
  - id: declared
    prompt: declared
    success_check:
      verify: { command: "go test ./...", timeout: 90s, cwd: "/tmp/repo", output_matches: "^ok" }
`))
	if err != nil {
		t.Fatalf("valid verifications rejected: %v", err)
	}

	defaulted, _ := g.NodeByID("defaulted")
	if got := defaulted.SuccessCheck.Verify.TimeoutDuration(); got != defaultVerifyTimeout {
		t.Errorf("undeclared timeout = %s, want the %s default", got, defaultVerifyTimeout)
	}

	declared, _ := g.NodeByID("declared")
	// The parsed duration is what the engine uses: re-parsing the string on the
	// critical path is exactly what load-time parsing exists to avoid.
	if got := declared.SuccessCheck.Verify.TimeoutDuration(); got != 90*time.Second {
		t.Errorf("declared timeout = %s, want 1m30s", got)
	}
	if got := declared.SuccessCheck.Verify.Cwd; got != "/tmp/repo" {
		t.Errorf("verify cwd = %q, want /tmp/repo", got)
	}
}

func TestParse_VerifyAtTheCeilingIsAllowed(t *testing.T) {
	// The ceiling is inclusive: 10m is refused only above, so a graph that
	// declares exactly the documented maximum must load.
	_, err := Parse(verifyGraph("        command: make test\n        timeout: 10m\n"))
	if err != nil {
		t.Fatalf("a timeout exactly at the ceiling should be valid: %v", err)
	}
}

func TestParse_ExpectExitDistinguishesZeroFromUnset(t *testing.T) {
	// This is why ExpectExit is a *int: "expect exit 0" and "expect_exit not
	// declared" mean the same thing today, but a value type could not tell them
	// apart, and expect_exit: 1 ("this command is supposed to fail") has to be
	// expressible.
	g, err := Parse([]byte(`
name: exits
nodes:
  - id: unset
    prompt: unset
    success_check:
      verify: { command: "make test" }
  - id: explicit-zero
    prompt: explicit-zero
    success_check:
      verify: { command: "make test", expect_exit: 0 }
  - id: expects-failure
    prompt: expects-failure
    success_check:
      verify: { command: "grep -q TODO src", expect_exit: 1 }
`))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	unset, _ := g.NodeByID("unset")
	if unset.SuccessCheck.Verify.ExpectExit != nil {
		t.Error("an undeclared expect_exit must stay nil")
	}
	if got := unset.SuccessCheck.Verify.ExpectedExitCode(); got != 0 {
		t.Errorf("undeclared expect_exit resolves to %d, want 0", got)
	}

	explicitZero, _ := g.NodeByID("explicit-zero")
	if explicitZero.SuccessCheck.Verify.ExpectExit == nil {
		t.Error("an explicit expect_exit: 0 must be distinguishable from unset")
	}

	expectsFailure, _ := g.NodeByID("expects-failure")
	if got := expectsFailure.SuccessCheck.Verify.ExpectedExitCode(); got != 1 {
		t.Errorf("expect_exit = %d, want 1", got)
	}
}

func TestSuccessCheck_IsZeroAccountsForVerify(t *testing.T) {
	// The regression this guards: IsZero is what makes an empty success_check
	// mean "exit zero is enough". A check carrying ONLY a verify would, under the
	// old two-field IsZero, be read as empty — and the evidence command would
	// never run while the node reported PASS.
	cases := []struct {
		name  string
		check SuccessCheck
		want  bool
	}{
		{name: "nothing configured", check: SuccessCheck{}, want: true},
		{name: "only verify", check: SuccessCheck{Verify: &Verification{Command: "make test"}}, want: false},
		{name: "only exit_zero", check: SuccessCheck{ExitZero: true}, want: false},
		{name: "only result_matches", check: SuccessCheck{ResultMatches: "PASS"}, want: false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.check.IsZero(); got != tc.want {
				t.Errorf("IsZero() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestParse_AgentFieldRoundTrips proves a node's `agent:` name lands on
// Node.Agent and that a node omitting it defaults to empty — plain `claude -p`,
// the behaviour every existing graph relies on.
func TestParse_AgentFieldRoundTrips(t *testing.T) {
	g, err := Parse([]byte(`
name: with-agent
nodes:
  - { id: review, prompt: review, agent: code-reviewer }
  - { id: plain, prompt: plain }
`))
	if err != nil {
		t.Fatalf("node with agent field should parse: %v", err)
	}
	review, _ := g.NodeByID("review")
	if review.Agent != "code-reviewer" {
		t.Errorf("review.Agent = %q, want code-reviewer", review.Agent)
	}
	plain, _ := g.NodeByID("plain")
	if plain.Agent != "" {
		t.Errorf("plain.Agent = %q, want empty", plain.Agent)
	}
}

// TestParse_BlankAgentRejected proves a whitespace-only agent name — a
// near-certain YAML typo — fails at LOAD time naming the node, rather than
// reaching the argv as `--agent " "` and failing the node mid-run with a CLI
// error that names no graph at all.
func TestParse_BlankAgentRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: blank-agent
nodes:
  - { id: a, prompt: a, agent: "   " }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" || !strings.Contains(vErr.Reason, "agent") {
		t.Fatalf("expected agent error on node a: %+v", vErr)
	}
}

// TestParse_PaddedAgentRejected is the other half of the whitespace rule. A
// padded name is not "close enough": it reaches the argv verbatim as
// `--agent " code-reviewer "`, which claude cannot resolve, so the node dies
// mid-run with a CLI error naming no graph at all — the exact failure a
// load-time check exists to move earlier.
func TestParse_PaddedAgentRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: padded-agent
nodes:
  - { id: a, prompt: a, agent: "  code-reviewer  " }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" || !strings.Contains(vErr.Reason, "agent") {
		t.Fatalf("expected agent error on node a: %+v", vErr)
	}
}

// TestParse_UnknownAgentNameAccepted pins the deliberate NON-check: whether a
// name resolves depends on the user's ~/.claude/agents and the checkout's
// .claude/agents, which are properties of the machine, not of the graph file.
// Rejecting an unknown name at load would make a graph that is valid on one
// machine invalid on another.
func TestParse_UnknownAgentNameAccepted(t *testing.T) {
	if _, err := Parse([]byte(`
name: unknown-agent
nodes:
  - { id: a, prompt: a, agent: some-agent-this-machine-may-not-have }
`)); err != nil {
		t.Fatalf("an unresolvable agent name is a runtime concern, not a load error: %v", err)
	}
}

// TestParse_WorktreeUnsafeNameRejected proves a worktree name that is not a
// single safe path element fails at LOAD time naming the node. The name
// becomes a directory under the run dir AND a branch segment, so a separator
// or a leading dot would otherwise surface mid-run as a filesystem escape or
// a git ref error — after other nodes have already run and been paid for.
func TestParse_WorktreeUnsafeNameRejected(t *testing.T) {
	for _, name := range []string{"a/b", `a\b`, "../escape", ".hidden", "-flag", "has space", "   "} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(`
name: bad-worktree
nodes:
  - { id: a, prompt: a, worktree: "` + name + `" }
`))
			vErr := asValidationError(t, err)
			if vErr.NodeID != "a" || !strings.Contains(vErr.Reason, "worktree") {
				t.Fatalf("expected worktree error on node a for %q: %+v", name, vErr)
			}
		})
	}
}

// TestParse_WorktreeWithCwdRejected pins the mutual exclusion: a worktree
// node's directory is managed by the engine, so a declared cwd alongside it
// could only be dead text or a contradiction — rejected rather than silently
// preferring one.
func TestParse_WorktreeWithCwdRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: worktree-and-cwd
nodes:
  - { id: a, prompt: a, cwd: /somewhere, worktree: lane }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" || !strings.Contains(vErr.Reason, "worktree") {
		t.Fatalf("expected worktree/cwd conflict error on node a: %+v", vErr)
	}
}

// TestParse_NodeIDUnsafeRejected proves a node id that is not a single safe
// path element fails at LOAD time naming the offending id. The id becomes an
// artifact filename under the run dir (<node-id>.out) AND a URL parameter
// (serve's /api/result), so a separator or a leading dot would otherwise
// surface mid-run as a filesystem escape — after other nodes have already run
// and been paid for. Same rule as the worktree name, for the same reason.
func TestParse_NodeIDUnsafeRejected(t *testing.T) {
	for _, id := range []string{"a/b", `a\b`, "../escape", ".hidden", "-flag", "has space"} {
		t.Run(id, func(t *testing.T) {
			_, err := Parse([]byte(`
name: bad-node-id
nodes:
  - { id: '` + id + `', prompt: a }
`))
			vErr := asValidationError(t, err)
			if vErr.NodeID != id || !strings.Contains(vErr.Reason, "node id") {
				t.Fatalf("expected node id error naming %q: %+v", id, vErr)
			}
		})
	}
}

// TestParse_NodeIDValidNamesAccepted is the positive boundary: ids that are
// plainly one safe path element parse clean.
func TestParse_NodeIDValidNamesAccepted(t *testing.T) {
	g, err := Parse([]byte(`
name: good-node-id
nodes:
  - { id: lane-1, prompt: a }
  - { id: "Feature_2.x", prompt: b }
`))
	if err != nil {
		t.Fatalf("valid node ids must be accepted: %v", err)
	}
	if _, ok := g.NodeByID("Feature_2.x"); !ok {
		t.Errorf("node id did not survive parsing")
	}
}

// TestParse_UnknownRetryCauseRejected pins the load-time guard against the
// silent footgun: a typoed retry.on cause matches no failure the scheduler
// ever produces, so without this check the node would just never retry. The
// message must name the node, the bogus cause, and the valid tokens.
func TestParse_UnknownRetryCauseRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: bad-retry-cause
nodes:
  - { id: a, prompt: a, retry: { max: 2, on: [nonzero-exit] } }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, `"nonzero-exit"`) {
		t.Errorf("reason should name the bogus cause: %q", vErr.Reason)
	}
	for _, valid := range retryCauses {
		if !strings.Contains(vErr.Reason, valid) {
			t.Errorf("reason should list valid cause %q: %q", valid, vErr.Reason)
		}
	}
}

// TestParse_UnknownOnFailRejected pins the load-time guard for the graph-level
// failure policy: a typoed on_fail would silently mean today's halt behaviour
// (every lane cancelled by one failure) — the exact surprise the field exists
// to prevent. The message must name the bogus value and both valid ones, the
// same contract retry.on causes get.
func TestParse_UnknownOnFailRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: bad-on-fail
on_fail: keep-going
nodes:
  - { id: a, prompt: a }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "" {
		t.Fatalf("on_fail is graph-level; error named node %q, want none", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, `"keep-going"`) {
		t.Errorf("reason should name the bogus value: %q", vErr.Reason)
	}
	for _, valid := range []string{OnFailHalt, OnFailContinue} {
		if !strings.Contains(vErr.Reason, valid) {
			t.Errorf("reason should list valid value %q: %q", valid, vErr.Reason)
		}
	}
}

// TestParse_OnFailDefaultsToHalt pins backward compatibility: a graph that
// never mentions on_fail normalizes to halt — today's behaviour, unchanged.
func TestParse_OnFailDefaultsToHalt(t *testing.T) {
	g, err := Parse([]byte(`
name: no-on-fail
nodes:
  - { id: a, prompt: a }
`))
	if err != nil {
		t.Fatalf("graph without on_fail must stay valid: %v", err)
	}
	if g.OnFail != OnFailHalt {
		t.Errorf("OnFail = %q, want %q", g.OnFail, OnFailHalt)
	}
	if g.ContinuesOnFail() {
		t.Error("ContinuesOnFail() = true for an undeclared on_fail, want false")
	}
}

// TestParse_OnFailValuesAccepted is the positive boundary: both members of the
// closed set parse clean, and only continue reports ContinuesOnFail.
func TestParse_OnFailValuesAccepted(t *testing.T) {
	for value, wantContinue := range map[string]bool{OnFailHalt: false, OnFailContinue: true} {
		g, err := Parse([]byte(`
name: on-fail-` + value + `
on_fail: ` + value + `
nodes:
  - { id: a, prompt: a }
`))
		if err != nil {
			t.Fatalf("on_fail: %s must be accepted: %v", value, err)
		}
		if g.OnFail != value {
			t.Errorf("OnFail = %q, want %q", g.OnFail, value)
		}
		if g.ContinuesOnFail() != wantContinue {
			t.Errorf("ContinuesOnFail() with on_fail: %s = %v, want %v", value, g.ContinuesOnFail(), wantContinue)
		}
	}
}

// TestParse_ValidRetryCausesAccepted is the positive boundary: every token the
// scheduler can actually produce must pass validation, so the guard can never
// reject a working retry policy.
func TestParse_ValidRetryCausesAccepted(t *testing.T) {
	g, err := Parse([]byte(`
name: good-retry
nodes:
  - id: a
    prompt: a
    retry:
      max: 1
      on: [nonzero_exit, run_error, output_error, budget_exceeded, verify_failed, result_mismatch]
`))
	if err != nil {
		t.Fatalf("all valid retry causes must be accepted: %v", err)
	}
	if n, _ := g.NodeByID("a"); n.Retry == nil || len(n.Retry.On) != len(retryCauses) {
		t.Errorf("retry.on did not survive parsing: %+v", n)
	}
}

// TestParse_WorktreeValidNamesAccepted is the positive boundary: names that
// are plainly one safe path element parse clean, and the field round-trips
// through normalization.
func TestParse_WorktreeValidNamesAccepted(t *testing.T) {
	g, err := Parse([]byte(`
name: good-worktree
nodes:
  - { id: a, prompt: a, worktree: lane-1 }
  - { id: b, prompt: b, worktree: "Feature_2.x" }
`))
	if err != nil {
		t.Fatalf("valid worktree names must be accepted: %v", err)
	}
	if n, _ := g.NodeByID("a"); n.Worktree != "lane-1" {
		t.Errorf("worktree did not survive parsing: %+v", n)
	}
}

// --- node-level timeout: rejected at LOAD, never mid-run --------------------
//
// The node `timeout:` (ADR 0007) replaces the runner's 20m default for one
// node. Like the verify timeout it is parsed once at load; unlike it there is
// no ceiling — raising the bound is the point of declaring it.

func TestParse_NodeTimeoutUnparseableRejected(t *testing.T) {
	_, err := Parse([]byte("name: t\nnodes:\n  - { id: slow, prompt: slow, timeout: 45 minutes }\n"))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "slow" {
		t.Fatalf("error named node %q, want slow", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, "45 minutes") {
		t.Fatalf("reason should quote the offending value: %q", vErr.Reason)
	}
}

func TestParse_NodeTimeoutNonPositiveRejected(t *testing.T) {
	for _, timeout := range []string{"0s", "-5m"} {
		t.Run(timeout, func(t *testing.T) {
			_, err := Parse([]byte("name: t\nnodes:\n  - { id: slow, prompt: slow, timeout: " + timeout + " }\n"))

			vErr := asValidationError(t, err)
			if vErr.NodeID != "slow" {
				t.Fatalf("error named node %q, want slow", vErr.NodeID)
			}
			if !strings.Contains(vErr.Reason, "positive") {
				t.Fatalf("reason should say the timeout must be positive: %q", vErr.Reason)
			}
		})
	}
}

func TestParse_NodeTimeoutParsedAtLoad(t *testing.T) {
	g, err := Parse([]byte(`
name: long-node
nodes:
  - { id: slow, prompt: slow, timeout: 45m }
  - { id: plain, prompt: plain }
`))
	if err != nil {
		t.Fatalf("valid node timeout rejected: %v", err)
	}

	// Node is a value in both Nodes and byID, so the parsed duration must be
	// visible through both — the Scheduler reads via NodeByID.
	slow, _ := g.NodeByID("slow")
	if got := slow.TimeoutDuration(); got != 45*time.Minute {
		t.Errorf("declared timeout = %s, want 45m", got)
	}
	if got := g.Nodes[0].TimeoutDuration(); got != 45*time.Minute {
		t.Errorf("timeout via Nodes slice = %s, want 45m", got)
	}

	plain, _ := g.NodeByID("plain")
	if got := plain.TimeoutDuration(); got != 0 {
		t.Errorf("undeclared timeout = %s, want 0 (the runner's default applies)", got)
	}

	// The ceiling that bounds a verify timeout deliberately does not apply
	// here: a node may declare far more than 10m.
	if _, err := Parse([]byte("name: t\nnodes:\n  - { id: vslow, prompt: v, timeout: 3h }\n")); err != nil {
		t.Errorf("a node timeout over the verify ceiling must still be valid: %v", err)
	}
}
