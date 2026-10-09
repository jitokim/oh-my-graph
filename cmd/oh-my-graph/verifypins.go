package main

import (
	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// verifyPinSet is the files an `auto --verify-cmd` command line names, pinned
// once per invocation (#363). One set guards every verification of that
// command the invocation runs — the starting-tree baseline and every goal-loop
// cycle's sinks — so an edit a cycle made can never become a later cycle's
// pin. A nil set is a run with nothing to guard: `run`, `chat`, and an auto
// run without --verify-cmd, all of which keep the engine's verifier exactly as
// it was.
type verifyPinSet struct {
	command string
	files   []verify.PinnedFile
}

// pinVerifyCommand pins v's command line against the process cwd, which is
// where the baseline runs it and where an auto sink, which has no cwd of its
// own, runs it too. nil when no command was supplied.
func pinVerifyCommand(v coordinator.VerifyCommand) *verifyPinSet {
	if !v.Supplied() {
		return nil
	}
	return &verifyPinSet{command: v.Command, files: verify.PinCommand(v.Command, ".")}
}

// resumedVerifyPins rebuilds the first leg's set from the pins state.json
// recorded, for the command this resumed leg's sinks carry. A resume never
// re-pins: a file changed while the run was stopped is the same fault a file
// changed mid-run is. nil for a snapshot without pins — every run without
// --verify-cmd and every snapshot written before #363 — which then resumes
// exactly as before.
func resumedVerifyPins(command string, recorded []runstate.VerifyPin) *verifyPinSet {
	if command == "" || len(recorded) == 0 {
		return nil
	}
	files := make([]verify.PinnedFile, len(recorded))
	for i, pin := range recorded {
		files[i] = verify.PinnedFile{Word: pin.Word, Path: pin.Path, Digest: pin.SHA256}
	}
	return &verifyPinSet{command: command, files: files}
}

// guard wraps inner so each verification of the pinned command checks the
// pins before and after it runs. A nil set, or one whose command named no
// pinnable file, returns inner itself.
func (p *verifyPinSet) guard(inner verify.Verifier) verify.Verifier {
	if p == nil || len(p.files) == 0 {
		return inner
	}
	return verify.NewPinningVerifier(inner, p.command, p.files)
}

// record is the set as state.json holds it: nil, and so no key at all, when
// there is nothing pinned.
func (p *verifyPinSet) record() []runstate.VerifyPin {
	if p == nil || len(p.files) == 0 {
		return nil
	}
	pins := make([]runstate.VerifyPin, len(p.files))
	for i, f := range p.files {
		pins[i] = runstate.VerifyPin{Word: f.Word, Path: f.Path, SHA256: f.Digest}
	}
	return pins
}
