package graph

import (
	"fmt"
	"strings"
)

// GraphValidationError names the offending node and the invariant it broke. It
// is the single error type Load/Parse return for a structurally invalid graph,
// so a caller can render a precise "node X: <why>" message without string
// matching.
type GraphValidationError struct {
	NodeID string
	Reason string
}

func (e *GraphValidationError) Error() string {
	if e.NodeID == "" {
		return fmt.Sprintf("invalid graph: %s", e.Reason)
	}
	return fmt.Sprintf("invalid graph: node %q: %s", e.NodeID, e.Reason)
}

// validTypes and validHandoffs are the closed sets a node's type/handoff may
// take. Kept as maps so membership is a single lookup and the error message can
// list the allowed values.
var (
	validTypes    = map[string]bool{TypeClaudeRun: true, TypeGate: true}
	validHandoffs = map[string]bool{HandoffArtifact: true, HandoffSession: true}
)

// Validate enforces the graph's structural invariants and returns the first
// violation as a *GraphValidationError. The checks run in dependency order so
// that later checks (cycles, handoff) may assume earlier ones (unique ids,
// existing parents) already hold:
//
//  1. every node id is non-empty and unique;
//  2. every type/handoff is a known value;
//  3. every depends_on id refers to a real node;
//  4. the depends_on relation is acyclic (DFS three-colour);
//  5. a session-handoff node has exactly one parent — the session it resumes
//     (a root has no session to resume; more than one can't be merged).
func (g *Graph) Validate() error {
	if err := g.validateNodesUnique(); err != nil {
		return err
	}
	if err := g.validateEnums(); err != nil {
		return err
	}
	if err := g.validateDependenciesExist(); err != nil {
		return err
	}
	if err := g.validateAcyclic(); err != nil {
		return err
	}
	return g.validateHandoffConstraints()
}

func (g *Graph) validateNodesUnique() error {
	seen := make(map[string]bool, len(g.Nodes))
	for _, n := range g.Nodes {
		if strings.TrimSpace(n.ID) == "" {
			return &GraphValidationError{Reason: "a node has an empty id"}
		}
		if seen[n.ID] {
			return &GraphValidationError{NodeID: n.ID, Reason: "duplicate node id"}
		}
		seen[n.ID] = true
	}
	return nil
}

func (g *Graph) validateEnums() error {
	for _, n := range g.Nodes {
		if !validTypes[n.Type] {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown type %q (want %s or %s)", n.Type, TypeClaudeRun, TypeGate),
			}
		}
		if !validHandoffs[n.Handoff] {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf("unknown handoff %q (want %s or %s)", n.Handoff, HandoffArtifact, HandoffSession),
			}
		}
	}
	return nil
}

func (g *Graph) validateDependenciesExist() error {
	for _, n := range g.Nodes {
		for _, parent := range n.DependsOn {
			if _, ok := g.byID[parent]; !ok {
				return &GraphValidationError{
					NodeID: n.ID,
					Reason: fmt.Sprintf("depends_on unknown node %q", parent),
				}
			}
			if parent == n.ID {
				return &GraphValidationError{NodeID: n.ID, Reason: "node depends on itself"}
			}
		}
	}
	return nil
}

// three-colour DFS marks for cycle detection: an unvisited node is white, a
// node on the current DFS stack is grey, a fully-explored node is black.
// Reaching a grey node means an edge closes a back-edge — a cycle.
const (
	colourWhite = iota
	colourGrey
	colourBlack
)

func (g *Graph) validateAcyclic() error {
	colour := make(map[string]int, len(g.Nodes))
	for _, n := range g.Nodes {
		if colour[n.ID] == colourWhite {
			if cycleNode, found := g.visit(n.ID, colour); found {
				return &GraphValidationError{
					NodeID: cycleNode,
					Reason: "dependency cycle detected",
				}
			}
		}
	}
	return nil
}

// visit explores id depth-first, following the depends_on edges. It returns the
// id at which a cycle was closed (a grey node reached again) and found=true, or
// found=false when the subtree rooted at id is acyclic.
func (g *Graph) visit(id string, colour map[string]int) (string, bool) {
	colour[id] = colourGrey
	node := g.byID[id]
	for _, parent := range node.DependsOn {
		switch colour[parent] {
		case colourGrey:
			return parent, true
		case colourWhite:
			if cycleNode, found := g.visit(parent, colour); found {
				return cycleNode, true
			}
		}
	}
	colour[id] = colourBlack
	return "", false
}

func (g *Graph) validateHandoffConstraints() error {
	for _, n := range g.Nodes {
		if n.Handoff == HandoffSession && len(n.DependsOn) != 1 {
			return &GraphValidationError{
				NodeID: n.ID,
				Reason: fmt.Sprintf(
					"handoff: session with %d parents — a session-handoff node must resume exactly one parent's session; use handoff: artifact for a root node or for fan-in",
					len(n.DependsOn),
				),
			}
		}
	}
	return nil
}
