package graph

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// #346: `description:` is what a person reads when deciding a gate, so it is
// valid on `type: gate` only and refused at load on every other node.

// --- failure cases first ----------------------------------------------------

func TestParse_DescriptionOnNonGateIsRefused(t *testing.T) {
	for name, spec := range map[string]string{
		"explicit claude-run": "name: t\nnodes:\n  - { id: build, type: claude-run, prompt: p, description: approve the build }\n",
		"defaulted type":      "name: t\nnodes:\n  - { id: build, prompt: p, description: approve the build }\n",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := Parse([]byte(spec))
			vErr := asValidationError(t, err)
			if vErr.NodeID != "build" {
				t.Errorf("error named node %q, want build", vErr.NodeID)
			}
			for _, want := range []string{"description", "valid only on type: gate", "type: claude-run"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not say %q", err, want)
				}
			}
			var misplaced *MisplacedDescriptionError
			if !errors.As(err, &misplaced) {
				t.Errorf("want *MisplacedDescriptionError, got %T", err)
			}
		})
	}
}

// TestLoadFile_DescriptionSplicedOntoNonGateIsRefused: a description reaching a
// non-gate node through a fragment is refused like one written inline (#346).
func TestLoadFile_DescriptionSplicedOntoNonGateIsRefused(t *testing.T) {
	frag := "fragment: check\ndescription: a check\nnode: { prompt: p, description: approve it }\n"
	entry := "name: t\nnodes:\n  - { id: c, use: check }\n"
	path := writeGraphDir(t, entry, map[string]string{"check": frag})

	_, err := LoadFile(path)
	var misplaced *MisplacedDescriptionError
	if !errors.As(err, &misplaced) || misplaced.NodeID != "c" {
		t.Fatalf("want *MisplacedDescriptionError naming c, got %T: %v", err, err)
	}
}

// --- success cases ------------------------------------------------------------

func TestParse_GateWithDescriptionLoads(t *testing.T) {
	g, err := Parse([]byte(`
name: t
nodes:
  - { id: build, prompt: p }
  - id: approve
    type: gate
    depends_on: [build]
    description: Approve to merge {{ artifacts.build }}.
`))
	if err != nil {
		t.Fatalf("a gate with a description must load (#346): %v", err)
	}
	gate, _ := g.NodeByID("approve")
	if gate.Description != "Approve to merge {{ artifacts.build }}." {
		t.Errorf("Description = %q", gate.Description)
	}
	build, _ := g.NodeByID("build")
	if build.Description != "" {
		t.Errorf("a node declaring no description must carry none, got %q", build.Description)
	}
}

// TestGraphJSON_GateDescriptionRoundTrips: the snapshot `resume` re-parses
// keeps the description, and a node without one writes no key (omitempty).
func TestGraphJSON_GateDescriptionRoundTrips(t *testing.T) {
	g, err := Parse([]byte("name: t\nnodes:\n  - { id: build, prompt: p }\n  - { id: approve, type: gate, depends_on: [build], description: 'ship it?' }\n"))
	if err != nil {
		t.Fatal(err)
	}
	data, err := json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(data), `"description"`) != 1 {
		t.Errorf("want exactly one description key, on the gate: %s", data)
	}
	back, err := Parse(data)
	if err != nil {
		t.Fatalf("snapshot must re-parse: %v", err)
	}
	if gate, _ := back.NodeByID("approve"); gate.Description != "ship it?" {
		t.Errorf("description lost across the snapshot: %q", gate.Description)
	}
}

// TestLoadFile_GateDescriptionSurvivesSplice: a fragment splice carries the
// field like any other key — from the fragment's body, and overridden by the
// citing node's own (#346).
func TestLoadFile_GateDescriptionSurvivesSplice(t *testing.T) {
	frag := "fragment: approval\ndescription: a human gate\nnode: { type: gate, description: from the fragment }\n"
	entry := "name: t\nnodes:\n" +
		"  - { id: build, prompt: p }\n" +
		"  - { id: inherits, use: approval, depends_on: [build] }\n" +
		"  - { id: overrides, use: approval, depends_on: [build], description: from the citing node }\n"
	path := writeGraphDir(t, entry, map[string]string{"approval": frag})

	res, err := LoadFile(path)
	if err != nil {
		t.Fatalf("a spliced gate with a description must load: %v", err)
	}
	for id, want := range map[string]string{"inherits": "from the fragment", "overrides": "from the citing node"} {
		if n, _ := res.Graph.NodeByID(id); n.Description != want {
			t.Errorf("%s: Description = %q, want %q", id, n.Description, want)
		}
	}
}

// TestParsePlannerReply_LeavesMisplacedDescriptionToTheCoordinator: the planner
// path passes the refusal through so the coordinator refuses it in its own
// words (#346); Parse on the same bytes still refuses.
func TestParsePlannerReply_LeavesMisplacedDescriptionToTheCoordinator(t *testing.T) {
	reply := []byte(`{"name":"t","nodes":[{"id":"a","prompt":"p","description":"approve?"}]}`)
	g, err := ParsePlannerReply(reply)
	if err != nil {
		t.Fatalf("ParsePlannerReply must leave a misplaced description to the coordinator: %v", err)
	}
	if n, _ := g.NodeByID("a"); n.Description != "approve?" {
		t.Errorf("Description = %q", n.Description)
	}
	if _, err := Parse(reply); err == nil {
		t.Error("Parse must still refuse a description on a non-gate node")
	}
}
