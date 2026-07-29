package graph

import (
	"errors"
	"strings"
	"testing"
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

// TestParse_AgentFieldParses proves a node's `agent:` name round-trips onto
// Node.Agent, and that a node omitting it defaults to empty (plain claude -p).
func TestParse_AgentFieldParses(t *testing.T) {
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
// near-certain YAML typo — is rejected at load time rather than silently
// falling back to plain claude.
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
