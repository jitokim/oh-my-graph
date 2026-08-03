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

	"github.com/jitokim/oh-my-graph/internal/runfeed"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/schedule"
)

// limitCauseMsg is the real session-limit message shape the runner's matcher
// pins (internal/runner/sessionlimit_test.go).
const limitCauseMsg = "You've hit your session limit · resets 5:20pm"

// limitRunner limits the FIRST invocation of each prompt named in limitFirst
// — returning the outcome ClaudeCLIRunner classifies for a limit-killed
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
		runErr = executeGraph(context.Background(), runID, g, rec, commonRunFlags{inputs: inputFlag{}}, nil, 0, "limit-flow.yaml", []byte("name: limit-flow\n"), nil)
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
// subcommand — real ClaudeCLIRunner, real matcher — against a stub `claude`
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
	printPauseHint(&buf, "run-9", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: limitCauseMsg})
	out := buf.String()
	if !strings.Contains(out, "(resets 5:20pm)") || !strings.Contains(out, "Resume after 5:20pm") {
		t.Fatalf("a parseable cause should put the reset time in the hint:\n%s", out)
	}
	if !strings.Contains(out, "oh-my-graph resume run-9 --retry-failed") {
		t.Fatalf("the hint must carry the exact resume command:\n%s", out)
	}

	buf.Reset()
	printPauseHint(&buf, "run-9", &schedule.LimitPausedError{NodeIDs: []string{"a"}, Cause: "You've hit your session limit"})
	out = buf.String()
	if strings.Contains(out, "resets") {
		t.Fatalf("an unparseable cause must not invent a reset time:\n%s", out)
	}
	if !strings.Contains(out, "oh-my-graph resume run-9 --retry-failed") {
		t.Fatalf("the hint must still carry the exact resume command:\n%s", out)
	}
}
