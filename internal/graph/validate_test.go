package graph

import (
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"slices"
	"strconv"
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

// --- success_check.result_matches: the same standing as its sibling ---------
//
// result_matches used to compile for the first time inside the scheduler's
// success-check evaluation, which runs only after its own node has been spawned
// and paid for: a typo'd pattern cost a full node before it was diagnosed.

// yamlSingleQuoted renders s as a YAML single-quoted scalar — the one scalar
// form with no escape sequences at all, where the only special sequence is a
// doubled `”` for a literal quote. Go's strconv.Quote would emit a Go string
// literal that YAML then reads as a DOUBLE-quoted scalar and re-escapes under
// its own rules; the two happen to agree on every pattern below, but a regex
// carrying an escape the two spell differently would reach the validator as
// something other than what the test names.
func yamlSingleQuoted(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

// resultMatchesGraph renders a one-node graph declaring the given
// result_matches pattern, so a regex full of backslashes and brackets reaches
// the validator verbatim.
func resultMatchesGraph(pattern string) []byte {
	return []byte("name: r\nnodes:\n  - id: judged\n    prompt: judged\n    success_check:\n      result_matches: " + yamlSingleQuoted(pattern) + "\n")
}

// TestParse_ResultMatchesAcceptedExactlyWhenCompilable states the property the
// fix has to hold, rather than one example of it: load accepts a result_matches
// pattern exactly when regexp accepts it. The oracle is regexp.Compile itself,
// so the test cannot pass by refusing everything or by waving everything
// through, and a graph that reaches the scheduler can carry no pattern the
// scheduler could fail to compile.
func TestParse_ResultMatchesAcceptedExactlyWhenCompilable(t *testing.T) {
	patterns := []string{
		`^[*_` + "`" + `\s]*PASS[*_` + "`" + `\s]*$`, // the shape graphs/fragments/e2e-verify.yaml ships
		`PASS`,
		`MERGED — [0-9a-f]{7,}`,
		`[unclosed`,
		`(`,
		`*`,
		`(?P<`,
		`a{2,1}`,
	}
	for _, pattern := range patterns {
		t.Run(pattern, func(t *testing.T) {
			_, compilable := regexp.Compile(pattern)
			_, err := Parse(resultMatchesGraph(pattern))

			if compilable == nil {
				if err != nil {
					t.Fatalf("pattern %q compiles, so load must accept it: %v", pattern, err)
				}
				return
			}
			vErr := asValidationError(t, err)
			if vErr.NodeID != "judged" {
				t.Fatalf("error named node %q, want judged", vErr.NodeID)
			}
			if !strings.Contains(vErr.Reason, "result_matches") {
				t.Errorf("reason should name the offending field: %q", vErr.Reason)
			}
			if !strings.Contains(vErr.Reason, pattern) {
				t.Errorf("reason should quote the offending pattern %q: %q", pattern, vErr.Reason)
			}
		})
	}
}

// TestParse_ResultMatchesSurvivesLoad is the other half of the property: a
// compilable pattern is not merely accepted, it reaches the scheduler intact.
// Validation that quietly dropped the field would satisfy the test above.
func TestParse_ResultMatchesSurvivesLoad(t *testing.T) {
	const pattern = `^\s*PASS\s*$`
	g, err := Parse(resultMatchesGraph(pattern))
	if err != nil {
		t.Fatalf("valid result_matches rejected: %v", err)
	}
	n, ok := g.NodeByID("judged")
	if !ok {
		t.Fatal("node judged did not survive parsing")
	}
	if n.SuccessCheck.ResultMatches != pattern {
		t.Errorf("result_matches = %q, want %q", n.SuccessCheck.ResultMatches, pattern)
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
	for _, id := range []string{`a\b`, "../escape", ".hidden", "-flag", "has space", "a/b/c", "a//b", "a/", "/b", "a/.hidden"} {
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

// TestParse_NamespacedNodeIDAcceptedAsBackstop pins the ONE loosening ADR 0027
// makes to the id grammar, and pins it here rather than leaving it implicit:
// Validate accepts `<using-id>/<internal-id>` because it is handed graphs it
// cannot tell apart — a multi-node fragment splice's output, and a resumed
// leg's snapshot that already holds joined ids. Refusing here would break both.
//
// The refusal of an AUTHORED '/' is not weakened by this; it moves to the two
// places an id is WRITTEN rather than read: the file loader
// (TestLoadFile_AuthoredNamespaceInIDRejected) and the coordinator
// (nodeFieldDispositions["ID"]). Exactly one slash, each side an otherwise
// valid segment — every other shape stays refused above.
func TestParse_NamespacedNodeIDAcceptedAsBackstop(t *testing.T) {
	g, err := Parse([]byte(`
name: spliced
nodes:
  - { id: qa-a/impl, prompt: a }
  - { id: qa-a/review, prompt: b, depends_on: [qa-a/impl] }
`))
	if err != nil {
		t.Fatalf("a spliced id must validate — a resumed snapshot is full of them: %v", err)
	}
	if _, ok := g.NodeByID("qa-a/review"); !ok {
		t.Errorf("namespaced node id did not survive parsing")
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

// TestParse_RetryMaxBoundJudgedAtLoad pins the other half of the same footgun:
// the scheduler only adds retry.max to the attempt count when it is positive,
// so a negative bound is discarded and the node silently runs once — the exact
// outcome a typoed cause produces. Stated as the boundary rather than one
// example: negative is refused, and the two bounds that mean something
// (declaring no extra attempt, declaring some) still load and keep their value.
func TestParse_RetryMaxBoundJudgedAtLoad(t *testing.T) {
	for _, max := range []int{-3, -1, 0, 2} {
		t.Run(fmt.Sprintf("max=%d", max), func(t *testing.T) {
			g, err := Parse([]byte(fmt.Sprintf(`
name: retry-bound
nodes:
  - { id: a, prompt: a, retry: { max: %d, on: [nonzero_exit] } }
`, max)))

			if max >= 0 {
				if err != nil {
					t.Fatalf("retry.max %d must be accepted: %v", max, err)
				}
				n, _ := g.NodeByID("a")
				if n.Retry == nil || n.Retry.Max != max {
					t.Fatalf("retry.max did not survive parsing: %+v", n.Retry)
				}
				return
			}
			vErr := asValidationError(t, err)
			if vErr.NodeID != "a" {
				t.Fatalf("error named node %q, want a", vErr.NodeID)
			}
			if !strings.Contains(vErr.Reason, "retry.max") {
				t.Errorf("reason should name the offending field: %q", vErr.Reason)
			}
			if !strings.Contains(vErr.Reason, strconv.Itoa(max)) {
				t.Errorf("reason should quote the offending bound %d: %q", max, vErr.Reason)
			}
		})
	}
}

// TestParse_UnknownPermissionModeRejected pins the load-time guard for the one
// enum that had none: permission_mode is passed through verbatim as
// `claude --permission-mode <mode>`, so a wrong-case `dontask` used to reach
// argv and kill the node at SPAWN — mid-run, after every earlier node had
// already spent real money, and a long way from the typo. The message must
// name the node, the bogus value, and every accepted mode.
func TestParse_UnknownPermissionModeRejected(t *testing.T) {
	_, err := Parse([]byte(`
name: bad-permission-mode
nodes:
  - { id: a, prompt: a, permission_mode: dontask }
`))
	vErr := asValidationError(t, err)
	if vErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", vErr.NodeID)
	}
	if !strings.Contains(vErr.Reason, `"dontask"`) {
		t.Errorf("reason should name the bogus mode: %q", vErr.Reason)
	}
	for _, valid := range permissionModes {
		if !strings.Contains(vErr.Reason, valid) {
			t.Errorf("reason should list valid mode %q: %q", valid, vErr.Reason)
		}
	}
}

// TestParse_ValidPermissionModesAccepted is the positive boundary: every mode
// the `claude` CLI accepts must pass validation, or the guard would refuse a
// graph that runs perfectly well. The set is measured from `claude --help`, not
// transcribed from DESIGN.md — which listed three of the six.
func TestParse_ValidPermissionModesAccepted(t *testing.T) {
	for _, mode := range permissionModes {
		g, err := Parse([]byte(`
name: permission-mode-ok
nodes:
  - { id: a, prompt: a, permission_mode: ` + mode + ` }
`))
		if err != nil {
			t.Fatalf("permission_mode: %s must be accepted: %v", mode, err)
		}
		if n, _ := g.NodeByID("a"); n.PermissionMode != mode {
			t.Errorf("permission_mode did not survive parsing: %q, want %q", n.PermissionMode, mode)
		}
	}
}

// TestParse_UndeclaredPermissionModeStaysEmpty pins backward compatibility and
// the reason empty is skipped by the validator: permission_mode is NOT
// normalized at decode the way type and handoff are — an undeclared one stays
// empty and the Scheduler substitutes its own unattended default. Rejecting
// empty here would refuse nearly every graph in the repo.
func TestParse_UndeclaredPermissionModeStaysEmpty(t *testing.T) {
	g, err := Parse([]byte(`
name: no-permission-mode
nodes:
  - { id: a, prompt: a }
`))
	if err != nil {
		t.Fatalf("graph without permission_mode must stay valid: %v", err)
	}
	if n, _ := g.NodeByID("a"); n.PermissionMode != "" {
		t.Errorf("PermissionMode = %q, want empty so the Scheduler's default applies", n.PermissionMode)
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
//
// Walked one token at a time over the set itself rather than asserted once over
// a hand-typed list: a single `on: [a, b, c]` line is satisfied in aggregate,
// so a token that no test ever names alone (output_error was one) rides on its
// neighbours. Adding a cause to the set therefore adds its own case here for
// free, which is the point of taking the set from RetryCauses().
func TestParse_ValidRetryCausesAccepted(t *testing.T) {
	causes := RetryCauses()
	if len(causes) == 0 {
		t.Fatal("RetryCauses() is empty: retry.on would accept nothing")
	}
	for _, cause := range causes {
		t.Run(cause, func(t *testing.T) {
			g, err := Parse([]byte(fmt.Sprintf(`
name: good-retry
nodes:
  - { id: a, prompt: a, retry: { max: 1, on: [%s] } }
`, cause)))
			if err != nil {
				t.Fatalf("valid retry cause %q must be accepted: %v", cause, err)
			}
			n, _ := g.NodeByID("a")
			if n.Retry == nil || len(n.Retry.On) != 1 || n.Retry.On[0] != cause {
				t.Errorf("retry.on %q did not survive parsing: %+v", cause, n.Retry)
			}
		})
	}

	// And all of them at once, which is the shape an author actually writes
	// when they want everything.
	g, err := Parse([]byte(fmt.Sprintf(`
name: good-retry-all
nodes:
  - { id: a, prompt: a, retry: { max: 1, on: [%s] } }
`, strings.Join(causes, ", "))))
	if err != nil {
		t.Fatalf("all valid retry causes must be accepted together: %v", err)
	}
	if n, _ := g.NodeByID("a"); n.Retry == nil || len(n.Retry.On) != len(causes) {
		t.Errorf("retry.on did not survive parsing: %+v", n)
	}
}

// TestRetryCauses_CoversEveryCauseConstant closes the set by CONSTRUCTION, not
// by convention. `retryCauses` is a hand-maintained slice beside a
// hand-maintained `Cause*` const block, and Go offers no way to enumerate
// constants at run time — so an eighth constant added without touching the
// slice would produce a token the scheduler can emit and the validator rejects,
// i.e. a failure cause no graph is allowed to name, and nothing would fail.
// This reads the const block out of the source and holds the two together.
func TestRetryCauses_CoversEveryCauseConstant(t *testing.T) {
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "graph.go", nil, 0)
	if err != nil {
		t.Fatalf("parse graph.go: %v", err)
	}

	declared := map[string]string{} // constant name -> its token value
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			vs, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range vs.Names {
				if !strings.HasPrefix(name.Name, "Cause") || i >= len(vs.Values) {
					continue
				}
				lit, ok := vs.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				value, err := strconv.Unquote(lit.Value)
				if err != nil {
					t.Fatalf("unquote %s: %v", name.Name, err)
				}
				declared[name.Name] = value
			}
		}
	}
	if len(declared) == 0 {
		t.Fatal("found no Cause* constants in graph.go — this guard is reading the wrong file and would pass on anything")
	}

	causes := RetryCauses()
	for name, value := range declared {
		if !slices.Contains(causes, value) {
			t.Errorf("graph.%s = %q is not in retryCauses — the scheduler can classify a failure as a token retry.on refuses at load, so no graph may name that cause", name, value)
		}
	}
	if len(causes) != len(declared) {
		t.Errorf("retryCauses has %d entries but graph.go declares %d Cause* constants — the closed set and the token list have drifted", len(causes), len(declared))
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

// --- unresolved fragments (the ADR 0013 backstop) ---------------------------

// asUnresolvedFragmentError extracts the distinct backstop type — distinct so
// the coordinator can recognize a fragment-naming planner reply, so the type
// identity is part of the contract, not an implementation detail.
func asUnresolvedFragmentError(t *testing.T, err error) *UnresolvedFragmentError {
	t.Helper()
	var fragErr *UnresolvedFragmentError
	if !errors.As(err, &fragErr) {
		t.Fatalf("expected *UnresolvedFragmentError, got %T: %v", err, err)
	}
	return fragErr
}

func TestParse_RefusesUnresolvedUse(t *testing.T) {
	// Parse has no file context, so it cannot resolve a fragment — and without
	// this refusal the node would validate with an EMPTY prompt and spend real
	// money running garbage.
	_, err := Parse([]byte(`
name: frag
nodes:
  - { id: e2e, use: e2e-verify, with: { checks: "run make local" } }
`))
	fragErr := asUnresolvedFragmentError(t, err)
	if fragErr.NodeID != "e2e" {
		t.Fatalf("error named node %q, want e2e", fragErr.NodeID)
	}
	if !strings.Contains(fragErr.Reason, "file loader") {
		t.Fatalf("reason should point at the file loader: %q", fragErr.Reason)
	}
}

// TestParse_UnresolvedFragmentIsAlsoAGraphValidationError pins the OTHER half
// of the backstop's contract: the specialized type must not cost a caller the
// general one. GraphValidationError documents itself as the single type
// Load/Parse return for a structurally invalid graph, so a renderer that asks
// only that question must see a fragment error too. Struct embedding alone
// does not give it — errors.As matches concrete types and walks Unwrap, not
// embedded fields — so without UnresolvedFragmentError.Unwrap this fails and
// the general question quietly answers "not a validation error".
func TestParse_UnresolvedFragmentIsAlsoAGraphValidationError(t *testing.T) {
	_, err := Parse([]byte(`{"name":"frag","nodes":[{"id":"e2e","use":"e2e-verify"}]}`))

	vErr := asValidationError(t, err)
	if vErr.NodeID != "e2e" {
		t.Fatalf("the general view named node %q, want e2e", vErr.NodeID)
	}
	// And the specialization still answers its own question, so the
	// coordinator's ADR 0013 refusal (which asks for the narrow type) keeps
	// working — one error must satisfy both callers, not either-or.
	if asUnresolvedFragmentError(t, err).NodeID != "e2e" {
		t.Fatal("the narrow view must still name the node carrying use:")
	}
}

func TestParse_RefusesWithWithoutUse(t *testing.T) {
	// A dead binding is a wiring bug: nothing would ever consume it, and the
	// author plainly believed something would.
	_, err := Parse([]byte(`
name: frag
nodes:
  - { id: a, prompt: fine, with: { checks: "x" } }
`))
	fragErr := asUnresolvedFragmentError(t, err)
	if fragErr.NodeID != "a" {
		t.Fatalf("error named node %q, want a", fragErr.NodeID)
	}
}

func TestParse_RefusesUseCarriedInJSON(t *testing.T) {
	// The snapshot-resume path hands Parse JSON bytes; a JSON-authored graph
	// carrying use: must hit the same refusal, never a silent drop.
	_, err := Parse([]byte(`{"name":"frag","nodes":[{"id":"e2e","use":"e2e-verify"}]}`))
	if asUnresolvedFragmentError(t, err).NodeID != "e2e" {
		t.Fatal("JSON-authored use: must be refused naming the node")
	}
}

// TestParse_RefusesPresentButEmptyWith pins that the backstop tests PRESENCE,
// not size. Both decoders turn `with: {}` into a non-nil EMPTY map, so a
// length test would wave through the one binding block that is dead by
// construction while refusing every populated one — the loudest case going
// quietest. Both notations, because the two decode paths (a YAML file, a JSON
// snapshot) reach this validator independently.
func TestParse_RefusesPresentButEmptyWith(t *testing.T) {
	for _, tc := range []struct{ name, src string }{
		{"yaml", "name: frag\nnodes:\n  - { id: a, prompt: fine, with: {} }\n"},
		{"json", `{"name":"frag","nodes":[{"id":"a","prompt":"fine","with":{}}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse([]byte(tc.src))
			if err == nil {
				t.Fatal("an empty but present with: is still a dead binding — it must be refused")
			}
			if asUnresolvedFragmentError(t, err).NodeID != "a" {
				t.Fatal("the refusal must name the node carrying the dead with:")
			}
		})
	}
}

// TestParse_AcceptsAnAbsentWith is the other half: a node that never declares
// with: at all must stay valid, or the presence test above has turned every
// ordinary node into an error.
func TestParse_AcceptsAnAbsentWith(t *testing.T) {
	if _, err := Parse([]byte("name: frag\nnodes:\n  - { id: a, prompt: fine }\n")); err != nil {
		t.Fatalf("a node with no with: key must be valid: %v", err)
	}
}
