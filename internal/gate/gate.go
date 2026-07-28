// Package gate is the v1.1 human-pause seam, present in v0.1 as a stub only.
//
// A `gate` node lets a graph pause for human approval before its dependents run.
// The schema reserves the type now (so a graph containing a gate node parses and
// validates), but the pause/resume machinery — persisting run state, an
// `oh-my-graph resume` command — is deliberately NOT built in v0.1. Until it is,
// GateController rejects gate execution with a clear "not yet implemented"
// error rather than silently skipping it, which would let a graph run past an
// approval that was supposed to stop it.
package gate

import (
	"context"
	"errors"

	"github.com/jitokim/oh-my-graph/internal/graph"
)

// ErrGateNotImplemented is returned by the v0.1 stub when a graph tries to
// execute a gate node.
var ErrGateNotImplemented = errors.New(
	"gate nodes are reserved for v1.1 (human pause/approve) and are not yet implemented; " +
		"remove the gate node or wait for `oh-my-graph resume`",
)

// GateController decides whether a gate node's dependents may proceed. In v1.1
// this blocks for human approval; in v0.1 the only implementation refuses.
type GateController interface {
	Evaluate(ctx context.Context, node graph.Node) error
}

// StubController is the v0.1 GateController: it always refuses, naming the node.
type StubController struct{}

// NewStubController returns the v0.1 refuse-everything gate controller.
func NewStubController() StubController { return StubController{} }

// Evaluate always returns ErrGateNotImplemented (wrapped with the node id) so
// the Scheduler halts on a gate node instead of running past it.
func (StubController) Evaluate(_ context.Context, node graph.Node) error {
	return &UnsupportedGateError{NodeID: node.ID}
}

// UnsupportedGateError names the gate node a v0.1 run tripped over. It unwraps to
// ErrGateNotImplemented so callers can match on the sentinel.
type UnsupportedGateError struct {
	NodeID string
}

func (e *UnsupportedGateError) Error() string {
	return "node " + e.NodeID + ": " + ErrGateNotImplemented.Error()
}

func (e *UnsupportedGateError) Unwrap() error { return ErrGateNotImplemented }
