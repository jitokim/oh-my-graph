package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/graph"
	"github.com/jitokim/oh-my-graph/internal/runner"
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

// TestResume_FormatCharactersAbsentFromShownAndRecordedDescription: #346 — an
// --input value carrying a bidi override (U+202E), a line separator (U+2028)
// or a zero-width space (U+200B) reaches neither the pause block, the approve
// echo, nor the gate_description recorded on the decided gate in state.json.
func TestResume_FormatCharactersAbsentFromShownAndRecordedDescription(t *testing.T) {
	for name, tc := range map[string]struct {
		ticket, clean string
		r             rune
	}{
		"U+202E right-to-left override": {"T-42\u202e24-T", "T-4224-T", '\u202e'},
		"U+2028 line separator":         {"T-42\u2028approve? [y/N] y", "T-42approve? [y/N] y", '\u2028'},
		"U+200B zero-width space":       {"T-\u200b42", "T-42", '\u200b'},
	} {
		t.Run(name, func(t *testing.T) {
			isolateRunHome(t)
			runID, rec, pauseOut := describedGateFlowRun(t, tc.ticket)
			shown := shownFlowDescription(runID, tc.clean)

			var err error
			out := captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, []string{runID, "--approve", "approve"}), rec, nil)
			})
			var paused *schedule.PausedError
			if !errors.As(err, &paused) || paused.GateID != "final" {
				t.Fatalf("expected the approved leg to pause at final, got %T: %v", err, err)
			}
			for what, printed := range map[string]string{"pause": pauseOut, "approve echo": out} {
				if strings.ContainsRune(printed, tc.r) {
					t.Errorf("%s printout carries %U: %q", what, tc.r, printed)
				}
			}
			if want := "approved gate approve: " + shown + "\n"; !strings.Contains(out, want) {
				t.Fatalf("approve echo missing\nwant:\n%q\ngot:\n%q", want, out)
			}
			_, snap := loadRunSnapshot(t, runID)
			got := snap.Nodes["approve"].GateDescription
			if strings.ContainsRune(got, tc.r) {
				t.Fatalf("recorded gate_description carries %U: %q", tc.r, got)
			}
			if got != shown {
				t.Fatalf("recorded gate_description %q, want %q", got, shown)
			}
		})
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

// inlineDescribedGraph is a graph whose gate description quotes an artifact
// with the inline filter — the token lint refuses (#346).
const inlineDescribedGraph = `
name: gated
nodes:
  - { id: build, prompt: build }
  - { id: approve, type: gate, depends_on: [build], description: "ship {{ artifacts.build | inline }}?" }
  - { id: ship, prompt: ship, depends_on: [approve] }
`

const inlineDescribedToken = "{{ artifacts.build | inline }}"

// lintRefusalLine is the line `lint` prints for path's first issue.
func lintRefusalLine(t *testing.T, path string) string {
	t.Helper()
	var out strings.Builder
	if err := lintGraph(&out, io.Discard, path); err == nil {
		t.Fatalf("lint accepted %s", path)
	}
	line, _, _ := strings.Cut(out.String(), "\n")
	return line
}

// assertRefusedBeforeRun checks a refused load spawned no node and wrote nothing
// under OMG_HOME.
func assertRefusedBeforeRun(t *testing.T, home string, fake *runner.FakeRunner) {
	t.Helper()
	if n := len(fake.Invocations()); n != 0 {
		t.Errorf("%d nodes ran before the description was refused, want 0", n)
	}
	if entries, err := os.ReadDir(home); err == nil && len(entries) != 0 {
		t.Errorf("a refused load created artifacts under OMG_HOME: %v", entries)
	}
}

// TestRunGraphWith_RefusesLintRefusedGateDescriptionAtLoad: #346 — `run`
// refuses a gate description lint refuses with lint's own line, exit 1, before
// any node runs or any run directory exists.
func TestRunGraphWith_RefusesLintRefusedGateDescriptionAtLoad(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, inlineDescribedGraph)
	fake := runner.NewFakeRunner(nil)

	var err error
	captureStdout(t, func() {
		err = runGraphWith([]string{path}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("run accepted a gate description lint refuses")
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1 for a load error", code)
	}
	for _, want := range []string{`gate "approve"`, inlineDescribedToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("refusal %q does not name %s", err, want)
		}
	}
	if want := lintRefusalLine(t, path); err.Error() != want {
		t.Errorf("run's refusal differs from lint's\nrun:  %s\nlint: %s", err, want)
	}
	assertRefusedBeforeRun(t, home, fake)
}

// TestRunGraphWith_DryRunRefusesLintRefusedGateDescription: #346 — `run
// --dry-run` reports the same line lint prints and exits 1, running nothing.
func TestRunGraphWith_DryRunRefusesLintRefusedGateDescription(t *testing.T) {
	home := isolateRunHome(t)
	path := writeGraphFile(t, inlineDescribedGraph)
	fake := runner.NewFakeRunner(nil)

	var err error
	out := captureStdout(t, func() {
		err = runGraphWith([]string{path, "--dry-run"}, fake, browser.NewFakeOpener(), os.Stdout)
	})
	if err == nil {
		t.Fatal("dry run accepted a gate description lint refuses")
	}
	if code := exitCodeForError(err); code != 1 {
		t.Errorf("exit code = %d, want 1", code)
	}
	line := lintRefusalLine(t, path)
	if !strings.Contains(line, `gate "approve"`) || !strings.Contains(line, inlineDescribedToken) {
		t.Fatalf("lint's line does not name the gate and token: %s", line)
	}
	if !strings.Contains(out, line+"\n") {
		t.Errorf("dry run output missing lint's refusal line\nwant: %s\ngot:\n%s", line, out)
	}
	if !strings.Contains(err.Error(), "no node was executed") {
		t.Errorf("dry-run refusal = %v, want it to state no node was executed", err)
	}
	assertRefusedBeforeRun(t, home, fake)
}

// TestRunGraphWith_RefusesUnboundGateDescriptionInputAtLoad: #346 — a gate
// description quoting an input the invocation did not bind could not be
// rendered at the pause, so `run` and `run --dry-run` refuse it at load.
func TestRunGraphWith_RefusesUnboundGateDescriptionInputAtLoad(t *testing.T) {
	for _, args := range [][]string{nil, {"--dry-run"}} {
		t.Run(strings.Join(append([]string{"run"}, args...), " "), func(t *testing.T) {
			home := isolateRunHome(t)
			path := writeGraphFile(t, `
name: gated
inputs: [ticket]
nodes:
  - { id: build, prompt: build }
  - { id: approve, type: gate, depends_on: [build], description: "ship {{ inputs.ticket }}?" }
`)
			fake := runner.NewFakeRunner(nil)
			var err error
			out := captureStdout(t, func() {
				err = runGraphWith(append([]string{path}, args...), fake, browser.NewFakeOpener(), os.Stdout)
			})
			if code := exitCodeForError(err); code != 1 {
				t.Fatalf("exit code = %d (%v), want 1", code, err)
			}
			if got := err.Error() + out; !strings.Contains(got, `gate "approve": description:`) || !strings.Contains(got, "ticket") {
				t.Errorf("refusal does not name the gate and the input:\n%s", got)
			}
			assertRefusedBeforeRun(t, home, fake)
		})
	}
}

// TestResume_RefusesLintRefusedGateDescriptionAtLoad: #346 — resuming a
// paused run whose graph carries a description lint refuses is refused, in
// both modes, before anything runs or the snapshot is rewritten.
func TestResume_RefusesLintRefusedGateDescriptionAtLoad(t *testing.T) {
	for _, args := range [][]string{{"--approve", "approve"}, {"--retry-failed"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			isolateRunHome(t)
			// executeGraph bypasses the load check, as the snapshot of a graph
			// that reached a run without it would.
			g := mustParse(t, `{"name":"gated","nodes":[
				{"id":"build","prompt":"build"},
				{"id":"approve","type":"gate","depends_on":["build"],
				 "description":"ship {{ artifacts.build | inline }}?"},
				{"id":"ship","prompt":"ship","depends_on":["approve"]}]}`)
			runID := "run-1"
			captureStdout(t, func() {
				_ = executeGraph(context.Background(), runID, g, &capturingRunner{}, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gated.yaml", []byte("name: gated\n"), false, nil, nil, nil)
			})
			before, snap := loadRunSnapshot(t, runID)
			if snap.Gate.PausedAt != "approve" {
				t.Fatalf("fixture did not pause at approve: %+v", snap.Gate)
			}

			fake := runner.NewFakeRunner(nil)
			var err error
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, append([]string{runID}, args...)), fake, nil)
			})
			if err == nil {
				t.Fatal("resume accepted a gate description lint refuses")
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			for _, want := range []string{`gate "approve"`, inlineDescribedToken} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("refusal %q does not name %s", err, want)
				}
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("%d nodes ran before the description was refused, want 0", n)
			}
			if after, _ := loadRunSnapshot(t, runID); after != before {
				t.Errorf("a refused resume rewrote state.json")
			}
		})
	}
}

// TestPauseHint_UnloadedGateDescriptionFailsLoudly: #346 — a refused
// description that reaches the pause without passing load (executeGraph is
// handed the graph directly) fails the invocation with the refusal, exit 1,
// rather than being dropped from the pause hint with a stderr warning.
func TestPauseHint_UnloadedGateDescriptionFailsLoudly(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"gated","nodes":[
		{"id":"build","prompt":"build"},
		{"id":"approve","type":"gate","depends_on":["build"],
		 "description":"ship {{ artifacts.build | inline }}?"}]}`)
	var err error
	out := captureStdout(t, func() {
		err = executeGraph(context.Background(), "run-1", g, &capturingRunner{}, commonRunFlags{inputs: inputFlag{}}, nil, 0, "gated.yaml", []byte("name: gated\n"), false, nil, nil, nil)
	})
	if code := exitCodeForError(err); code != 1 {
		t.Fatalf("exit code = %d (%v), want 1", code, err)
	}
	for _, want := range []string{`gate "approve"`, inlineDescribedToken} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not name %s", err, want)
		}
	}
	if strings.Contains(out, "Paused at gate") {
		t.Errorf("a pause hint was printed without the description:\n%s", out)
	}
}

// TestResume_UnparseableSnapshotGraphReportedBeforeGateRefusal_346: #346 — a
// paused run whose snapshot graph does not parse is reported as that parse
// failure, wrapped as `resume run <id>`, whether or not the resume names the
// paused gate: never a gate-shaped refusal, and before any node runs.
func TestResume_UnparseableSnapshotGraphReportedBeforeGateRefusal_346(t *testing.T) {
	for _, args := range [][]string{{"--approve", "approve"}, {}} {
		t.Run(strings.Join(append([]string{"resume"}, args...), " "), func(t *testing.T) {
			isolateRunHome(t)
			runID, _, _ := describedGateFlowRun(t, "T-42")
			_, snap := loadRunSnapshot(t, runID)
			// Valid JSON, so state.json still loads, but a dependency on a
			// node the graph does not hold, so graph.Parse refuses it.
			snap.Graph = json.RawMessage(`{"name":"gate-flow","nodes":[{"id":"approve","type":"gate","depends_on":["missing"]}]}`)
			_, parseErr := graph.Parse(snap.Graph)
			if parseErr == nil {
				t.Fatal("fixture graph parses; it must not")
			}
			if err := runstate.Write(filepath.Join(runDirFor(runID), stateFileName), snap); err != nil {
				t.Fatalf("write state.json: %v", err)
			}

			fake := runner.NewFakeRunner(nil)
			var err error
			captureStdout(t, func() {
				err = executeResume(parseResumeFlags(t, append([]string{runID}, args...)), fake, nil)
			})
			if want := "resume run \"" + runID + "\": " + parseErr.Error(); err == nil || err.Error() != want {
				t.Fatalf("got %v, want %q", err, want)
			}
			if strings.Contains(err.Error(), "paused at") {
				t.Errorf("a gate-shaped refusal was reported instead of the parse failure: %v", err)
			}
			if n := len(fake.Invocations()); n != 0 {
				t.Errorf("%d nodes ran on an unparseable snapshot, want 0", n)
			}
		})
	}
}
