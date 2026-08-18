package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// limitCauseMsg is the real session-limit message shape the runner's matcher
// pins (internal/runner/sessionlimit_test.go).
const limitCauseMsg = "You've hit your session limit · resets 5:20pm"

// limitRunner limits the FIRST invocation of each prompt named in limitFirst
// — returning the outcome CLIRunner classifies for a limit-killed
// subprocess — and passes everything else, counting invocations so a test can
// prove exactly what each leg launched.
type limitRunner struct {
	mu          sync.Mutex
	invocations map[string]int
	limitFirst  map[string]bool
}

func (r *limitRunner) Run(_ context.Context, spec runner.NodeInvocation) (runner.NodeOutcome, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.invocations == nil {
		r.invocations = make(map[string]int)
	}
	r.invocations[spec.Prompt]++
	if r.limitFirst[spec.Prompt] && r.invocations[spec.Prompt] == 1 {
		return runner.NodeOutcome{ExitCode: 1, FailureCause: limitCauseMsg, SessionLimited: true}, nil
	}
	return runner.NodeOutcome{SessionID: "s-" + spec.Prompt, Result: "PASS", ExitCode: 0}, nil
}

func (r *limitRunner) count(prompt string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.invocations[prompt]
}

// TestRun_SessionLimitPausesThenRetryFailedFinishes is ADR 0009's operator
// story end to end: the first leg hits the limit, exits with the reset-time
// hint, records the limited node nowhere; `resume --retry-failed` then runs
// the unfinished nodes to completion, bracketed as its own leg on the stream.
func TestRun_SessionLimitPausesThenRetryFailedFinishes(t *testing.T) {
	isolateRunHome(t)
	g := mustParse(t, `{"name":"limit-flow","nodes":[
		{"id":"a","prompt":"a"},
		{"id":"b","prompt":"b","depends_on":["a"]}]}`)
	rec := &limitRunner{limitFirst: map[string]bool{"a": true}}
	runID := "run-limit"

	var runErr error
	out := captureStdout(t, func() {
		runErr = executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-flow.yaml", []byte("name: limit-flow\n"), false, nil, nil, nil)
	})
	var limited *schedule.LimitPausedError
	if !errors.As(runErr, &limited) {
		t.Fatalf("expected *LimitPausedError from the first leg, got %T: %v", runErr, runErr)
	}
	if !strings.Contains(out, "resets 5:20pm") || !strings.Contains(out, "resume "+runID+" --retry-failed") {
		t.Fatalf("the exit hint should carry the reset time and the exact resume command:\n%s", out)
	}
	if got := rec.count("b"); got != 0 {
		t.Fatalf("b ran %d time(s) on the limited leg, want 0", got)
	}

	var resumeErr error
	out = captureStdout(t, func() {
		resumeErr = executeResume(parseResumeFlags(t, []string{runID, "--retry-failed"}), rec, nil)
	})
	if resumeErr != nil {
		t.Fatalf("the retry leg should finish the run cleanly, got: %v", resumeErr)
	}
	if !strings.Contains(out, "running unfinished nodes") {
		t.Fatalf("the retry banner should say it is running unfinished nodes (nothing FAILED):\n%s", out)
	}
	if got := rec.count("a"); got != 2 {
		t.Fatalf("a ran %d time(s) across both legs, want 2 — the limited node was never marked passed", got)
	}
	if got := rec.count("b"); got != 1 {
		t.Fatalf("b ran %d time(s) after the retry leg, want 1", got)
	}

	events := readRunEvents(t, runID)
	if got := countEvents(events, runfeed.EventRunStarted, ""); got != 2 {
		t.Fatalf("events.jsonl holds %d run_started, want 2 — the retry is its own leg", got)
	}
	var outcomes, details []string
	for _, e := range events {
		if e.Type == runfeed.EventRunFinished {
			outcomes = append(outcomes, e.Outcome)
			details = append(details, e.Detail)
		}
	}
	if len(outcomes) != 2 || outcomes[0] != runfeed.OutcomePaused || outcomes[1] != runfeed.OutcomePassed {
		t.Fatalf("run_finished outcomes = %v, want [paused passed]", outcomes)
	}
	if !strings.Contains(details[0], "session limit reached at a") {
		t.Fatalf("the paused leg's run_finished detail should name the limit, got %q", details[0])
	}
	if eventSeen(events, runfeed.EventNodeFailed, "a") {
		t.Error("the limited node must never appear as node_failed on the stream")
	}
}

// TestMainExitCode_SessionLimitMapsToExitCode2 drives the REAL `run`
// subcommand — real CLIRunner, real matcher — against a stub `claude`
// on PATH that dies exactly the way a limit-killed CLI does, and pins the
// resumable exit code.
func TestMainExitCode_SessionLimitMapsToExitCode2(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub claude is a shebang script; this pins the unix path")
	}
	isolateRunHome(t)
	dir := t.TempDir()
	stub := filepath.Join(dir, "claude")
	script := "#!/bin/sh\ncat <<'JSON'\n" +
		`{"session_id":"s","result":"` + limitCauseMsg + `","total_cost_usd":0,"is_error":true}` +
		"\nJSON\nexit 1\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub claude: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	graphPath := filepath.Join(dir, "limit.yaml")
	if err := os.WriteFile(graphPath, []byte("name: limit\nnodes:\n  - { id: a, prompt: a }\n"), 0o644); err != nil {
		t.Fatalf("write graph file: %v", err)
	}

	if code := mainExitCode([]string{"run", graphPath}); code != 2 {
		t.Fatalf("exit code = %d, want 2 for a session-limit pause", code)
	}
}

// TestPrintPauseHint_SessionLimit pins the hint's two formats: with the
// parsed reset time when the cause yields one, and without any time — never
// an invented one — when it doesn't.
func TestPrintPauseHint_SessionLimit(t *testing.T) {
	var buf bytes.Buffer
	printPauseHint(&buf, "run-9", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg}, coordinator.VerifyCommand{})
	out := buf.String()
	if !strings.Contains(out, "(resets 5:20pm)") || !strings.Contains(out, "Resume after 5:20pm") {
		t.Fatalf("a parseable cause should put the reset time in the hint:\n%s", out)
	}
	if !strings.Contains(out, "oh-my-graph resume run-9 --retry-failed") {
		t.Fatalf("the hint must carry the exact resume command:\n%s", out)
	}

	buf.Reset()
	printPauseHint(&buf, "run-9", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: "You've hit your session limit"}, coordinator.VerifyCommand{})
	out = buf.String()
	if strings.Contains(out, "resets") {
		t.Fatalf("an unparseable cause must not invent a reset time:\n%s", out)
	}
	if !strings.Contains(out, "oh-my-graph resume run-9 --retry-failed") {
		t.Fatalf("the hint must still carry the exact resume command:\n%s", out)
	}
}

// TestPrintPauseHint_VerifyRunIsOfferedTheFlagBack is the hint's half of ADR
// 0016 §4, and the half #198 got wrong from the other side. A resumed leg takes
// no verification from the run directory, so a bare `oh-my-graph resume <id>
// --retry-failed` on an auto run started with --verify-cmd is refused — the
// hint has to hand the command back, and the command it hands back has to be
// the one the user typed, or the paste does not finish the run.
//
// Both pause shapes are pinned, because both print a command. Before this the
// hint printed no command at all and said the run "cannot be resumed", which was
// true only because `resume` had no flag; that sentence is now gone, and this
// test is what stops it coming back.
func TestPrintPauseHint_VerifyRunIsOfferedTheFlagBack(t *testing.T) {
	gradlew := coordinator.VerifyCommand{Command: "./gradlew build"}
	for _, tc := range []struct {
		name    string
		runErr  error
		verify  coordinator.VerifyCommand
		want    string // what the hint must still say about WHY it paused
		command string // the full resume command it must offer
	}{
		{"limit with a reset time", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg}, gradlew,
			"resets 5:20pm", "oh-my-graph resume run-9 --retry-failed --verify-cmd './gradlew build'"},
		{"limit with no reset time", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: "You've hit your session limit"}, gradlew,
			"Session limit reached", "oh-my-graph resume run-9 --retry-failed --verify-cmd './gradlew build'"},
		{"gate", &schedule.PausedError{GateID: "review"}, gradlew,
			`Paused at gate "review"`, "oh-my-graph resume run-9 --approve review --verify-cmd './gradlew build'"},
		// A single quote is IN verifyShellMetachars — a --verify-cmd may carry
		// one, it just stands the pre-flight down — so a bare '%s' wrap prints a
		// line that a shell reads as `--verify-cmd "sh -c make"` FOLLOWED BY
		// `&& ./x`: a different evidence command, plus a command executed on
		// paste. The expected string below is the POSIX rule ('\'' closes,
		// escapes and reopens), spelled out so a "simplification" back to the
		// wrap fails here.
		{"a command carrying a single quote", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg},
			coordinator.VerifyCommand{Command: `sh -c 'make && ./x'`},
			"resets 5:20pm", `oh-my-graph resume run-9 --retry-failed --verify-cmd 'sh -c '\''make && ./x'\'''`},
		// The bound is part of what the next leg has to be given: resumed with
		// --verify-cmd alone, a run started under a 2m bound would silently get
		// the 10m default — the one place the next leg's check would differ from
		// the leg it continues.
		{"a bound the user chose", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg},
			coordinator.VerifyCommand{Command: "./gradlew build", Timeout: 2 * time.Minute},
			"resets 5:20pm", "oh-my-graph resume run-9 --retry-failed --verify-cmd './gradlew build' --verify-timeout 2m0s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			printPauseHint(&buf, "run-9", tc.runErr, tc.verify)
			out := buf.String()
			if !strings.Contains(out, tc.command) {
				t.Fatalf("the hint must offer the whole command, flag included (%q):\n%s", tc.command, out)
			}
			if !strings.Contains(out, "has to come from you") {
				t.Fatalf("the hint must say why the flag is repeated rather than remembered:\n%s", out)
			}
			if !strings.Contains(out, tc.want) {
				t.Fatalf("the hint must still say why the run paused (%q):\n%s", tc.want, out)
			}
		})
	}

	// The other direction of the same rule: the DEFAULT bound is the ceiling
	// every verification has, so printing it back would be noise on every hint
	// the common path produces.
	t.Run("the default bound is not printed back", func(t *testing.T) {
		var buf bytes.Buffer
		printPauseHint(&buf, "run-9", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg},
			coordinator.VerifyCommand{Command: "./gradlew build", Timeout: 10 * time.Minute})
		if out := buf.String(); strings.Contains(out, "--verify-timeout") {
			t.Errorf("the hint spells out a bound that is already the default:\n%s", out)
		}
	})
}

// TestPrintPauseHint_NoVerifyCommandLeavesTheHintAlone is the control: a run
// carrying no injected evidence command must print exactly the hint it always
// did, with no flag appended and no note about one.
func TestPrintPauseHint_NoVerifyCommandLeavesTheHintAlone(t *testing.T) {
	var buf bytes.Buffer
	printPauseHint(&buf, "run-9", &schedule.PausedError{GateID: "review"}, coordinator.VerifyCommand{})
	out := buf.String()
	if strings.Contains(out, "--verify-cmd") {
		t.Fatalf("a run with no evidence command was offered the flag anyway:\n%s", out)
	}
	if !strings.Contains(out, "oh-my-graph resume run-9 --approve review\n") ||
		!strings.Contains(out, "oh-my-graph resume run-9 --reject review\n") {
		t.Fatalf("the gate hint must carry both decisions verbatim:\n%s", out)
	}
}
