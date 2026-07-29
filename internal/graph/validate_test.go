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
	// A gate node must PARSE and validate in v0.1 (execution is rejected
	// elsewhere) so schema-reserved graphs load.
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
