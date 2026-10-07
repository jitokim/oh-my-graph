package handoff

import (
	"github.com/jitokim/oh-my-graph/internal/fence"
	"github.com/jitokim/oh-my-graph/internal/graph"
)

// RenderGateDescription interpolates gate node's `description:` against the
// run as it stands when the gate pauses, and returns it sanitised for a
// terminal (#346): "" for a gate with no description.
//
// It goes through InterpolateAs, the prompt machinery itself, so an artifact
// resolves to its persisted FILE PATH and an input to its bound value exactly
// as they would in a prompt, and an unresolvable reference is the same
// *InterpolationError. Before that, every well-formed token is judged by
// gateDescriptionTokenRefused — the predicate GateDescriptionIssues refuses
// with at lint, and that run, run --dry-run and resume refuse with at load —
// and a refused one is a *GateDescriptionError, so a graph that reached the
// scheduler without passing that load check still cannot print a model's
// reply where a person approves; its caller fails with the error. The interpolated text, which carries the
// operator's own input values verbatim, then goes through fence.SanitizeTerminalLine.
func (h *Handoff) RenderGateDescription(gate graph.Node) (string, error) {
	if gate.Description == "" {
		return "", nil
	}
	for _, token := range placeholderPattern.FindAllString(gate.Description, -1) {
		if reason := gateDescriptionTokenRefused(token); reason != "" {
			return "", &GateDescriptionError{NodeID: gate.ID, Token: token, Reason: reason}
		}
	}
	text, err := h.InterpolateAs(Self{ID: gate.ID}, gate.Description)
	if err != nil {
		return "", err
	}
	return fence.SanitizeTerminalLine(text), nil
}
