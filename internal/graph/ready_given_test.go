package graph

import (
	"reflect"
	"testing"
)

// parseGraph is a local helper: parse YAML or fail the test.
func parseGraph(t *testing.T, y string) *Graph {
	t.Helper()
	g, err := Parse([]byte(y))
	if err != nil {
		t.Fatalf("parse graph: %v", err)
	}
	return g
}

// diamond is the shared shape for the topology tests: root → {left,right} → join.
const diamondYAML = `
name: diamond
nodes:
  - { id: root, prompt: root }
  - { id: left, prompt: left, depends_on: [root] }
  - { id: right, prompt: right, depends_on: [root] }
  - { id: join, prompt: join, depends_on: [left, right] }
`

// --- ReadyGiven must not unblock a node whose parents are not all done -------

func TestReadyGiven_PartialParentsNotReady(t *testing.T) {
	g := parseGraph(t, diamondYAML)
	// left is done, right is not: join has two parents and must stay blocked.
	got := g.ReadyGiven(map[string]bool{"root": true, "left": true})
	if contains(got, "join") {
		t.Fatalf("join became ready with only one parent done: %v", got)
	}
	// right, the still-unfinished sibling, is the only newly-ready node.
	if !reflect.DeepEqual(got, []string{"right"}) {
		t.Fatalf("ready = %v, want [right]", got)
	}
}

func TestReadyGiven_CompletedNodeNotReturned(t *testing.T) {
	g := parseGraph(t, diamondYAML)
	// root is done; it must never re-appear as ready (it must not re-run).
	got := g.ReadyGiven(map[string]bool{"root": true})
	if contains(got, "root") {
		t.Fatalf("a completed node was returned as ready: %v", got)
	}
	if !reflect.DeepEqual(got, []string{"left", "right"}) {
		t.Fatalf("ready after root = %v, want [left right]", got)
	}
}

func TestReadyGiven_JoinReadyOnlyWhenBothParentsDone(t *testing.T) {
	g := parseGraph(t, diamondYAML)
	got := g.ReadyGiven(map[string]bool{"root": true, "left": true, "right": true})
	if !reflect.DeepEqual(got, []string{"join"}) {
		t.Fatalf("ready = %v, want [join]", got)
	}
}

func TestReadyGiven_UnknownDoneIdIgnored(t *testing.T) {
	g := parseGraph(t, diamondYAML)
	// A done set naming a node that is not in the graph must not panic and must
	// not change the answer — resume feeds whatever the snapshot recorded.
	got := g.ReadyGiven(map[string]bool{"ghost": true})
	if !reflect.DeepEqual(got, []string{"root"}) {
		t.Fatalf("ready = %v, want [root]", got)
	}
}

// --- ReadyGiven(nil) is exactly Roots() -------------------------------------

func TestReadyGivenNil_MatchesRoots(t *testing.T) {
	graphs := []string{
		diamondYAML,
		`
name: two-roots
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - { id: c, prompt: c, depends_on: [a, b] }
`,
		`
name: single
nodes:
  - { id: only, prompt: only }
`,
	}
	for _, y := range graphs {
		g := parseGraph(t, y)
		roots := g.Roots()
		ready := g.ReadyGiven(nil)
		if !reflect.DeepEqual(roots, ready) {
			t.Fatalf("ReadyGiven(nil) = %v but Roots() = %v for graph %q", ready, roots, g.Name)
		}
	}
}

func TestReadyGiven_NeverNil(t *testing.T) {
	g := parseGraph(t, diamondYAML)
	// Every node completed: nothing is newly ready, but the result must be a
	// non-nil empty slice so a caller can range over it unconditionally.
	got := g.ReadyGiven(map[string]bool{
		"root": true, "left": true, "right": true, "join": true,
	})
	if got == nil {
		t.Fatal("ReadyGiven returned nil, want an empty non-nil slice")
	}
	if len(got) != 0 {
		t.Fatalf("expected nothing ready, got %v", got)
	}
}

func TestReadyGiven_PreservesNodeOrder(t *testing.T) {
	// Declared order a,b,c must be the ready-set order, so the ready set is
	// deterministic regardless of map iteration.
	g := parseGraph(t, `
name: ordered
nodes:
  - { id: a, prompt: a }
  - { id: b, prompt: b }
  - { id: c, prompt: c }
`)
	if got := g.ReadyGiven(nil); !reflect.DeepEqual(got, []string{"a", "b", "c"}) {
		t.Fatalf("ready = %v, want [a b c] in declared order", got)
	}
}

func contains(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
