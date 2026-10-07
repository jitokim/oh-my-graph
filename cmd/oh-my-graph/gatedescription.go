package main

import (
	"errors"
	"fmt"
	"os"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// shownGateDescription is gateID's `description:` exactly as a person deciding
// it reads it (#346): rendered against the run as it stands and sanitised by
// handoff.RenderGateDescription, never the raw YAML text. "" for a gate with
// no description, for an id that names no gate, and for a description the
// render refuses — which is warned about on stderr and otherwise leaves the
// gate's lines as they print without one, because a description is advice to
// the decider and its absence must never stand between a run and its resume
// command.
func shownGateDescription(g *graph.Graph, h *handoff.Handoff, gateID string) string {
	node, ok := g.NodeByID(gateID)
	if !ok || node.Type != graph.TypeGate {
		return ""
	}
	text, err := h.RenderGateDescription(node)
	if err != nil {
		fmt.Fprintf(os.Stderr, "warning: gate %q description not shown: %v\n", gateID, err)
		return ""
	}
	return text
}

// snapshotGateDescription is shownGateDescription for a run known only by its
// snapshot: the graph it holds, and a Handoff seeded from its records exactly
// as continueRun seeds a resumed leg's, so an artifact token resolves to the
// same path the pause printed. A snapshot whose graph does not parse shows no
// description; the caller's own parse reports that failure.
func snapshotGateDescription(runID string, snap runstate.Snapshot, gateID string) string {
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return ""
	}
	h := handoff.New(runDirFor(runID), snap.Inputs)
	for nodeID, rec := range snap.Nodes {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
	}
	return shownGateDescription(g, h, gateID)
}

// pausedGateDescription is the shown description of the gate runErr paused
// at, or "" when runErr is not a gate pause.
func pausedGateDescription(runErr error, g *graph.Graph, h *handoff.Handoff) string {
	var paused *schedule.PausedError
	if !errors.As(runErr, &paused) {
		return ""
	}
	return shownGateDescription(g, h, paused.GateID)
}

// describedGate names a paused gate in a message: its quoted id, followed by
// its shown description in parentheses when it has one. Without one it is
// exactly the %q the message printed before #346, so a gate with no
// description reads byte-for-byte as it always has.
func describedGate(gateID, description string) string {
	if description == "" {
		return fmt.Sprintf("%q", gateID)
	}
	return fmt.Sprintf("%q (%s)", gateID, description)
}
