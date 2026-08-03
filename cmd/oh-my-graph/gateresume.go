package main

import (
	"context"
	"fmt"
	"io"

	"github.com/jitokim/oh-my-graph/internal/gate"
	"github.com/jitokim/oh-my-graph/internal/runner"
)

// cliGateResumer is the live view's serve.GateResumer: the browser's approve
// and reject buttons, wired to the CLI's own resume. It is deliberately thin —
// it turns (run id, gate id, decision) into exactly the resumeFlags a
// `oh-my-graph resume <run-id> --approve <gate-id>` invocation parses and hands
// them to executeResume, the same function runResume calls. There is one
// resume implementation in this repo and this is a second front-end onto it,
// not a second copy of it: the lock, the snapshot load, the gate-id check, the
// RecordedController and the leg all come from executeResume.
//
// A leg started from the browser therefore behaves exactly as a leg started
// from the terminal, including where it reports: the banner, the ledger and
// any failure go to the serve process's own stdout/stderr, while the browser
// watches the same run feed the CLI writes.
type cliGateResumer struct {
	nodeRunner runner.NodeRunner
	// errOut receives the leg's failure, since the HTTP response is long gone
	// by the time a leg ends (the POST is answered 202 the moment the leg
	// starts). A rejected gate's run failure lands here too — it is the
	// honest outcome of the decision, not a fault of the resume.
	errOut io.Writer
}

// Resume applies one decision to one gate and runs the leg, blocking until it
// ends. The context is not threaded through on purpose: executeResume's leg
// installs its own signal-cancelled context, exactly as the CLI's does, so
// Ctrl-C on the serve process stops the leg — while the browser tab that
// started it can close without killing it (serve's handleGateDecision detaches
// the call from the request for the same reason).
//
// The leg gets no live-view Opener (nil), unlike the one `resume` starts from
// a terminal: this leg is already being watched. The `serve` process that owns
// this resumer is itself the live view of this very run, and the browser that
// POSTed the decision is sitting on it — an embedded second server on a second
// port, opening a second tab onto the same run feed, would be a duplicate of
// the page the human is already looking at.
func (r cliGateResumer) Resume(_ context.Context, runID, gateID string, decision gate.Decision) error {
	flags := newResumeFlags()
	flags.runID = runID
	switch decision {
	case gate.DecisionApprove:
		flags.approveGate = gateID
	case gate.DecisionReject:
		flags.rejectGate = gateID
	default:
		// Unreachable: serve's routes are the only callers and each names one
		// of the two decisions a human can make. DecisionPause is what a gate
		// does without a human, never something to resume with.
		return fmt.Errorf("resume gate %q: %q is not a decision a live view can apply", gateID, decision)
	}

	//nolint:contextcheck // deliberate: the leg installs its own signal-cancelled context (see above).
	err := executeResume(flags, r.nodeRunner, nil)
	if err != nil {
		fmt.Fprintf(r.errOut, "live view: resume of run %s at gate %s: %v\n", runID, gateID, err)
	}
	return err
}
