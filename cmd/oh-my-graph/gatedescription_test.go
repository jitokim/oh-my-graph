package main

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// describedGateFlowRun is pausedGateFlowRun with a `description:` on the gate
// that interpolates an input and an artifact (#346), and ticket bound as the
// input's value. It returns what the pausing run printed to stdout.
func describedGateFlowRun(t *testing.T, ticket string) (runID string, rec *capturingRunner, out string) {
	t.Helper()
	g := mustParse(t, `{"name":"gate-flow","inputs":["ticket"],"nodes":[
		{"id":"a","prompt":"a"},
		{"id":"approve","type":"gate","depends_on":["a"],
		 "description":"ship {{ inputs.ticket }} from {{ artifacts.a }}?"},
		{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
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
