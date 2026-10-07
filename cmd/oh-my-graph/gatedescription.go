package main

import (
	"errors"
	"fmt"
	"io"
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

// echoGateDecision prints the decision a `resume --approve/--reject` is about
// to apply beside the description the decider was shown (#346):
// "approved gate X: <description>". Silent for a gate with no description, so
// such a resume prints exactly what it did before.
func echoGateDecision(w io.Writer, gateID, verb, description string) {
	if description == "" {
		return
	}
	fmt.Fprintf(w, "%s gate %s: %s\n", verb, gateID, description)
}

// describedGateRecorder stamps the decided gate's shown description onto that
// gate's NodeRecord as it is written (runstate.NodeRecord.GateDescription),
// so state.json keeps what the person read when they decided it. Every other
// call passes through untouched. It wraps the leg's recorder rather than
// teaching the scheduler about descriptions: the description is the CLI's to
// render and print, and the scheduler records whatever the gate's verdict is.
type describedGateRecorder struct {
	schedule.Recorder
	gateID      string
	description string
}

func (r describedGateRecorder) RecordNode(nodeID string, rec runstate.NodeRecord) error {
	if nodeID == r.gateID {
		rec.GateDescription = r.description
	}
	return r.Recorder.RecordNode(nodeID, rec)
}

// decidedGate is the gate a `resume --approve/--reject` leg decides and the
// description shown for it; the zero value (a --retry-failed leg) decides
// none.
type decidedGate struct {
	id          string
	description string
}

// recorderFor wraps recorder so the decided gate's record carries its shown
// description, or returns recorder itself when there is none to carry — the
// snapshot of a gate without a description is then byte-identical.
func (d decidedGate) recorderFor(recorder schedule.Recorder) schedule.Recorder {
	if d.description == "" {
		return recorder
	}
	return describedGateRecorder{Recorder: recorder, gateID: d.id, description: d.description}
}
