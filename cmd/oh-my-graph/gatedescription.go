package main

import (
	"errors"
	"fmt"
	"io"

	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/handoff"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// gateDescriptionRefusal is lint's gate-description refusal
// (handoff.GateDescriptionIssues) applied where a graph is loaded to be run
// (#346): `run`, `run --dry-run` and `resume` all refuse a description lint
// would refuse, before any node runs, rather than meeting it only when the
// gate pauses. It returns the first issue, exactly as graph.LoadFile returns
// the first of LintLoadFile's, so a run names the gate and the token lint's
// first line names; nil when every description passes.
func gateDescriptionRefusal(g *graph.Graph) error {
	if issues := handoff.GateDescriptionIssues(g); len(issues) > 0 {
		return issues[0]
	}
	return nil
}

// gateDescriptionInputIssues is the half of a gate description lint cannot
// judge: an {{ inputs.<name> }} it quotes must be bound by this invocation's
// --input values, or the description could not be rendered when the gate
// pauses. Each gate's description is rendered through
// handoff.RenderGateDescription against a Handoff seeded with the same
// placeholder artifact paths inputIssues uses, so an artifact token — which
// only resolves once its producer has run — is never what is judged here.
func gateDescriptionInputIssues(g *graph.Graph, inputs map[string]string) []error {
	h := handoff.New("", inputs)
	for _, node := range g.Nodes {
		h.Seed(node.ID, handoff.SanitizeNodeID(node.ID)+".out", "")
	}
	var issues []error
	for _, node := range g.Nodes {
		if node.Type != graph.TypeGate || node.Description == "" {
			continue
		}
		if _, err := h.RenderGateDescription(node); err != nil && !isArtifactSide(err) {
			issues = append(issues, fmt.Errorf("gate %q: description: %w", node.ID, err))
		}
	}
	return issues
}

// shownGateDescription is gateID's `description:` exactly as a person deciding
// it reads it (#346): rendered against the run as it stands and sanitised by
// handoff.RenderGateDescription, never the raw YAML text. "" for a gate with
// no description and for an id that names no gate. A description the render
// refuses is an error, not a dropped line: run, run --dry-run and resume
// refuse such a description at load (gateDescriptionRefusal,
// gateDescriptionInputIssues), so one reaching here came from a graph that
// bypassed load, and that must fail loudly rather than quietly show the
// person deciding the gate less than its author wrote.
func shownGateDescription(g *graph.Graph, h *handoff.Handoff, gateID string) (string, error) {
	node, ok := g.NodeByID(gateID)
	if !ok || node.Type != graph.TypeGate {
		return "", nil
	}
	return h.RenderGateDescription(node)
}

// snapshotGateDescription is shownGateDescription for a run known only by its
// snapshot: the graph it holds, and a Handoff seeded from its records exactly
// as continueRun seeds a resumed leg's, so an artifact token resolves to the
// same path the pause printed. A snapshot whose graph does not parse shows no
// description; the caller's own parse reports that failure.
func snapshotGateDescription(runID string, snap runstate.Snapshot, gateID string) (string, error) {
	g, err := graph.Parse(snap.Graph)
	if err != nil {
		return "", nil
	}
	h := handoff.New(runDirFor(runID), snap.Inputs)
	for nodeID, rec := range snap.Nodes {
		h.Seed(nodeID, rec.ArtifactPath, rec.SessionID)
	}
	return shownGateDescription(g, h, gateID)
}

// pausedGateDescription is the shown description of the gate runErr paused
// at, or "" when runErr is not a gate pause. A description that cannot be
// shown is returned as an error naming the run and the gate, so the caller
// fails with it instead of printing a pause hint without it.
func pausedGateDescription(runID string, runErr error, g *graph.Graph, h *handoff.Handoff) (string, error) {
	var paused *schedule.PausedError
	if !errors.As(runErr, &paused) {
		return "", nil
	}
	description, err := shownGateDescription(g, h, paused.GateID)
	if err != nil {
		return "", fmt.Errorf("run %q paused at gate %q, but its description cannot be shown: %w", runID, paused.GateID, err)
	}
	return description, nil
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
