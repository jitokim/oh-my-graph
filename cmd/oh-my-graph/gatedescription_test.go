package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/runstate"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// describedGateFlowRun is pausedGateFlowRun with a `description:` on the gate
// that interpolates an input and an artifact (#346), and ticket bound as the
// input's value. A second, undescribed gate after ship gives the run a later
// leg to carry the first gate's record through. It returns what the pausing run printed to stdout.
func describedGateFlowRun(t *testing.T, ticket string) (runID string, rec *capturingRunner, out string) {
	t.Helper()
	g := mustParse(t, `{"name":"gate-flow","inputs":["ticket"],"nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"],
		 "description":"ship {{ inputs.ticket }} from {{ artifacts.a }}?"},
		{"id":"ship","prompt":"ship","depends_on":["approve"]},
		{"id":"final","type":"gate","depends_on":["ship"]}]}`)
	rec = &capturingRunner{}
	runID = "run-1"
	var err error
	out = captureStdout(t, func() {
		err = executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{"ticket": ticket}}, nil, 0, "gate-flow.yaml", []byte("name: gate-flow\n"), false, nil, nil, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "approve" {
		t.Fatalf("expected the run to pause at approve, got %T: %v", err, err)
	}
	return runID, rec, out
}

// shownFlowDescription is what describedGateFlowRun's gate prints for a
// clean ticket value: the input's value and the artifact's PATH.
func shownFlowDescription(runID, ticket string) string {
	return "ship " + ticket + " from " + filepath.Join(runDirFor(runID), "a.out") + "?"
}

// TestPauseHint_PrintsGateDescription: #346 — the pause block names the gate
// with its interpolated description, and the resume commands stay as they were.
func TestPauseHint_PrintsGateDescription(t *testing.T) {
	isolateRunHome(t)
	runID, _, out := describedGateFlowRun(t, "T-42")

	want := "\nPaused at gate \"approve\" (" + shownFlowDescription(runID, "T-42") + "). Resume with:\n" +
		"  oh-my-graph resume run-1 --approve approve\n" +
		"  oh-my-graph resume run-1 --reject approve\n"
	if !strings.Contains(out, want) {
		t.Fatalf("pause block missing the described gate\nwant:\n%s\ngot:\n%s", want, out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("the raw description text was printed:\n%s", out)
	}
}

// TestResume_PausedMessagePrintsGateDescription: #346 — a --retry-failed on a
// gate-paused run names the gate with the same description the pause printed.
func TestResume_PausedMessagePrintsGateDescription(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42")

	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if err != nil {
		t.Fatalf("executeResume: %v", err)
	}
	want := "It is paused at gate \"approve\" (" + shownFlowDescription(runID, "T-42") +
		") — decide it with --approve approve or --reject approve instead.\n"
	if !strings.Contains(out, want) {
		t.Fatalf("paused message missing the described gate\nwant:\n%s\ngot:\n%s", want, out)
	}
}

// TestResume_UndecidedAndMismatchRefusalsPrintGateDescription: #346 — a bare
// resume and one naming the wrong gate both name the paused gate with its
// description.
func TestResume_UndecidedAndMismatchRefusalsPrintGateDescription(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42")
	described := "\"approve\" (" + shownFlowDescription(runID, "T-42") + ")"

	err := executeResume(parseResumeFlags(t, []string{runID}), rec, nil)
	if want := "run is paused at gate " + described + "; resume with --approve approve or --reject approve"; err == nil || err.Error() != want {
		t.Fatalf("bare resume: got %v, want %q", err, want)
	}
	err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "other"}), rec, nil)
	if want := "resume named gate \"other\" but the run is paused at " + described; err == nil || err.Error() != want {
		t.Fatalf("mismatched resume: got %v, want %q", err, want)
	}
}

// TestGateDescription_EscapeSequenceStrippedInPauseAndResume: #346 — an ESC
// sequence in an --input value reaches neither the pause block nor resume's
// paused message.
func TestGateDescription_EscapeSequenceStrippedInPauseAndResume(t *testing.T) {
	isolateRunHome(t)
	runID, rec, pauseOut := describedGateFlowRun(t, "T-42\x1b[2J\x1b]0;approved\x07")

	resumeOut := captureStdout(t, func() {
		if err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil); err != nil {
			t.Fatalf("executeResume: %v", err)
		}
	})
	clean := shownFlowDescription(runID, "T-42")
	for name, out := range map[string]string{"pause": pauseOut, "resume": resumeOut} {
		if strings.ContainsRune(out, '\x1b') || strings.ContainsRune(out, '\a') {
			t.Errorf("%s printout carries a raw control: %q", name, out)
		}
		if !strings.Contains(out, "\"approve\" ("+clean+")") {
			t.Errorf("%s printout missing the sanitised description %q:\n%s", name, clean, out)
		}
	}
}

// TestGateDescription_AbsentPrintsAsBefore: #346 — a gate with no description
// prints its pause block, paused message and refusal exactly as before.
func TestGateDescription_AbsentPrintsAsBefore(t *testing.T) {
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	pauseOut := captureStdout(t, func() { runID, rec = pausedGateFlowRun(t) })
	if want := "\nPaused at gate \"approve\". Resume with:\n  oh-my-graph resume run-1 --approve approve\n  oh-my-graph resume run-1 --reject approve\n"; !strings.HasSuffix(pauseOut, want) {
		t.Fatalf("pause block changed\nwant suffix:\n%q\ngot:\n%q", want, pauseOut)
	}

	resumeOut := captureStdout(t, func() {
		if err := executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil); err != nil {
			t.Fatalf("executeResume: %v", err)
		}
	})
	if want := "run \"run-1\" has no failed nodes to retry.\nIt is paused at gate \"approve\" — decide it with --approve approve or --reject approve instead.\n"; resumeOut != want {
		t.Fatalf("paused message changed\nwant:\n%q\ngot:\n%q", want, resumeOut)
	}

	err := executeResume(parseResumeFlags(t, []string{runID}), rec, nil)
	if want := `run is paused at gate "approve"; resume with --approve approve or --reject approve`; err == nil || err.Error() != want {
		t.Fatalf("bare resume: got %v, want %q", err, want)
	}
}

// loadRunSnapshot reads runID's state.json, raw and decoded.
func loadRunSnapshot(t *testing.T, runID string) (raw string, snap runstate.Snapshot) {
	t.Helper()
	path := filepath.Join(runDirFor(runID), stateFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read state.json: %v", err)
	}
	snap, err = runstate.Load(path)
	if err != nil {
		t.Fatalf("load state.json: %v", err)
	}
	return string(data), snap
}

// TestResume_ApproveEchoesAndRecordsGateDescription: #346 — --approve echoes
// the decision with the description as shown, records exactly that string on
// the gate's state.json record, and a later leg carries it forward.
func TestResume_ApproveEchoesAndRecordsGateDescription(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42")
	shown := shownFlowDescription(runID, "T-42")

	var err error
	out := captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
	})
	var paused *schedule.PausedError
	if !errors.As(err, &paused) || paused.GateID != "final" {
		t.Fatalf("expected the approved leg to pause at final, got %T: %v", err, err)
	}
	if want := "approved gate approve: " + shown + "\n"; !strings.Contains(out, want) {
		t.Fatalf("approve echo missing\nwant:\n%s\ngot:\n%s", want, out)
	}
	if _, snap := loadRunSnapshot(t, runID); snap.Nodes["approve"].GateDescription != shown {
		t.Fatalf("recorded gate_description %q, want %q", snap.Nodes["approve"].GateDescription, shown)
	}

	captureStdout(t, func() {
		err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "final"}), rec, nil)
	})
	if err != nil {
		t.Fatalf("final leg: %v", err)
	}
	_, snap := loadRunSnapshot(t, runID)
	if got := snap.Nodes["approve"].GateDescription; got != shown {
		t.Fatalf("a later leg dropped the gate description: got %q, want %q", got, shown)
	}
	if got := snap.Nodes["final"].GateDescription; got != "" {
		t.Fatalf("an undescribed gate was recorded with %q", got)
	}
}

// TestResume_RejectEchoesAndRecordsGateDescription: #346 — --reject echoes the
// decision with the description and records it on the rejected gate's record.
func TestResume_RejectEchoesAndRecordsGateDescription(t *testing.T) {
	isolateRunHome(t)
	runID, rec, _ := describedGateFlowRun(t, "T-42\x1b[2J")
	shown := shownFlowDescription(runID, "T-42")

	out := captureStdout(t, func() {
		_ = executeResume(parseResumeFlags(t, []string{runID, "--reject", "approve"}), rec, nil)
	})
	if want := "rejected gate approve: " + shown + "\n"; !strings.Contains(out, want) {
		t.Fatalf("reject echo missing\nwant:\n%s\ngot:\n%q", want, out)
	}
	if strings.ContainsRune(out, '\x1b') {
		t.Fatalf("reject echo carries a raw ESC: %q", out)
	}
	_, snap := loadRunSnapshot(t, runID)
	if snap.Gate.Decisions["approve"] != runstate.GateReject {
		t.Fatalf("approve not recorded as rejected: %v", snap.Gate.Decisions)
	}
	if got := snap.Nodes["approve"].GateDescription; got != shown {
		t.Fatalf("recorded gate_description %q, want %q", got, shown)
	}
}

// TestResume_UndescribedGateEchoesAndRecordsNothing: #346 — deciding a gate
// with no description prints no echo line and writes no gate_description key.
func TestResume_UndescribedGateEchoesAndRecordsNothing(t *testing.T) {
	isolateRunHome(t)
	var runID string
	var rec *capturingRunner
	captureStdout(t, func() { runID, rec = pausedGateFlowRun(t) })

	out := captureStdout(t, func() {
		if err := executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil); err != nil {
			t.Fatalf("executeResume: %v", err)
		}
	})
	if strings.Contains(out, "approved gate") {
		t.Fatalf("an undescribed gate was echoed:\n%s", out)
	}
	if !strings.HasPrefix(out, "Resuming run \"run-1\" (gate \"approve\" approved)\n\n") {
		t.Fatalf("resume output no longer starts with its banner:\n%q", out)
	}
	if raw, _ := loadRunSnapshot(t, runID); strings.Contains(raw, "gate_description") {
		t.Fatalf("state.json carries a gate_description for an undescribed gate:\n%s", raw)
	}
}
