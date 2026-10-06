package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/jitokim/oh-my-graph/internal/browser"
	"github.com/jitokim/oh-my-graph/internal/runner"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// baselineCmd carries shell metacharacters on purpose: they switch
// checkVerifyExecutable's PATH lookup off, so the flag parses on any machine
// and the only thing deciding the baseline is the FakeVerifier's script.
const baselineCmd = "go test ./... && echo done"

// runBaselineAuto runs `auto` through the real argv path with the baseline's
// verifier injected, capturing stdout. Mapping and activation are off so no
// part of the real ~/.claude is read.
func runBaselineAuto(t *testing.T, fake *runner.FakeRunner, verifier verify.Verifier, stdin terminalInput, args ...string) (string, error) {
	t.Helper()
	args = append(args, "--no-agent-mapping", "--no-skill-activation")
	var err error
	out := captureStdout(t, func() {
		err = runAutoWithRuntime(runner.RuntimeClaude, args, fake, browser.NewFakeOpener(), os.Stdout, stdin, verifier)
	})
	return out, err
}

// assertNothingSpent is the red baseline's whole promise (#315): no model
// call, and nothing under OMG_HOME — no run directory, no kept plan.
func assertNothingSpent(t *testing.T, home string, fake *runner.FakeRunner) {
	t.Helper()
	if calls := fake.Invocations(); len(calls) != 0 {
		t.Errorf("a red baseline still spawned %d model call(s), first: %q", len(calls), firstLine(calls[0].Prompt))
	}
	if entries, err := os.ReadDir(home); err == nil && len(entries) != 0 {
		t.Errorf("a red baseline left artifacts under OMG_HOME: %v", entries)
	}
}

func renderBaselineRed(t *testing.T, err error) string {
	t.Helper()
	var red *BaselineRedError
	if !errors.As(err, &red) {
		t.Fatalf("err = %v, want *BaselineRedError", err)
	}
	var buf bytes.Buffer
	red.Print(&buf)
	return buf.String()
}

// #315: a --verify-cmd already red on the starting tree stops `auto` before
// the planner call, with exit 5, the output's tail quoted and a 0-cycle,
// $0 goal summary — and no run directory.
func TestRunAuto_RedBaselineRefusesBeforeThePlannerCall(t *testing.T) {
	home := isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{
		baselineCmd: {ExitCode: 2, Output: "ok  pkg/a\n--- FAIL: TestB (0.00s)\n    b_test.go:9: want 1, got 2\nFAIL\n"},
	})

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "fix the flaky test", "--verify-cmd", baselineCmd)

	if code := exitCodeForError(err); code != 5 {
		t.Fatalf("exit code = %d (err %v), want 5", code, err)
	}
	assertNothingSpent(t, home, fake)
	if strings.Contains(out, "Baseline:") {
		t.Errorf("a red baseline printed the green line:\n%s", out)
	}
	rendered := renderBaselineRed(t, err)
	for _, want := range []string{
		"baseline red",
		"--verify-cmd '" + baselineCmd + "' exited 2",
		"  | --- FAIL: TestB (0.00s)",
		"  |     b_test.go:9: want 1, got 2",
		"Its output:",
		`GOAL SUMMARY — "fix the flaky test"`,
		"no cycle ran — baseline red",
		"GOAL TOTAL: $0.0000 across 0 cycle(s)",
	} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendering lacks %q:\n%s", want, rendered)
		}
	}
	if strings.Contains(rendered, "incomplete") {
		t.Errorf("a goal that started no cycle was reported with an incomplete one:\n%s", rendered)
	}
	if n := len(verifier.Calls()); n != 1 {
		t.Errorf("baseline ran %d times, want once", n)
	}
}

// #315: --plan-only buys a planner call, so a red baseline refuses it too.
func TestRunAuto_RedBaselineRefusesPlanOnly(t *testing.T) {
	home := isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 1, Output: "boom\n"}})

	_, err := runBaselineAuto(t, fake, verifier, osStdin(), "fix it", "--plan-only", "--verify-cmd", baselineCmd)

	if code := exitCodeForError(err); code != 5 {
		t.Fatalf("exit code = %d (err %v), want 5", code, err)
	}
	assertNothingSpent(t, home, fake)
}

// countingReader records whether anything was read from it.
type countingReader struct {
	r     io.Reader
	reads int
}

func (c *countingReader) Read(p []byte) (int, error) {
	c.reads++
	return c.r.Read(p)
}

// #315: with --interview on a terminal, a red baseline is refused before the
// interview's first question — no interviewer call, and stdin never read.
func TestRunAuto_RedBaselineRefusesBeforeTheInterview(t *testing.T) {
	home := isolateRunHome(t)
	fake := newInterviewFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 1, Output: "boom\n"}})
	reader := &countingReader{r: strings.NewReader("an answer\n/done\n")}
	stdin := terminalInput{r: reader, isTerminal: func() bool { return true }}

	_, err := runBaselineAuto(t, fake, verifier, stdin, "fix it", "--interview", "--verify-cmd", baselineCmd)

	if code := exitCodeForError(err); code != 5 {
		t.Fatalf("exit code = %d (err %v), want 5", code, err)
	}
	assertNothingSpent(t, home, fake)
	if reader.reads != 0 {
		t.Errorf("stdin was read %d time(s) before a red-baseline refusal", reader.reads)
	}
}

// #315: a green baseline prints one line and changes nothing else — the
// planner is called and the run proceeds as before.
func TestRunAuto_GreenBaselinePrintsOneLineAndProceeds(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 0, Output: "all good\n"}})

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--verify-cmd", baselineCmd)

	if err != nil {
		t.Fatalf("a green baseline must not stop the run: %v", err)
	}
	want := "Baseline: --verify-cmd '" + baselineCmd + "' passed on the starting tree (exit 0).\n"
	if strings.Count(out, want) != 1 {
		t.Errorf("want the green line exactly once, got:\n%s", out)
	}
	if strings.Contains(out, "all good") {
		t.Errorf("a green baseline quoted its output:\n%s", out)
	}
	if len(plannerCalls(fake)) != 1 {
		t.Errorf("planner calls = %d, want 1 after a green baseline", len(plannerCalls(fake)))
	}
	if n := len(verifier.Calls()); n != 1 {
		t.Errorf("baseline ran %d times, want once", n)
	}
}

// #315: without --verify-cmd there is no baseline — the verifier is never
// asked and no baseline line is printed.
func TestRunAuto_NoVerifyCmdRunsNoBaseline(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417}})
	verifier := verify.NewFakeVerifier(nil)

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--accept-no-build-evidence")

	if err != nil {
		t.Fatalf("auto without --verify-cmd: %v", err)
	}
	if calls := verifier.Calls(); len(calls) != 0 {
		t.Errorf("the verifier was called without --verify-cmd: %+v", calls)
	}
	if strings.Contains(out, "Baseline:") || strings.Contains(out, "baseline red") {
		t.Errorf("a run without --verify-cmd mentioned a baseline:\n%s", out)
	}
}

// #315: a command that could not run, or timed out, has no result to judge,
// and that is red — never a pass.
func TestRunAuto_BaselineThatCannotRunIsRed(t *testing.T) {
	home := isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
	verifier := verify.NewFakeVerifier(nil)
	verifier.InjectError(baselineCmd, context.DeadlineExceeded)

	_, err := runBaselineAuto(t, fake, verifier, osStdin(), "fix it", "--verify-cmd", baselineCmd)

	if code := exitCodeForError(err); code != 5 {
		t.Fatalf("exit code = %d (err %v), want 5", code, err)
	}
	assertNothingSpent(t, home, fake)
	rendered := renderBaselineRed(t, err)
	for _, want := range []string{"baseline red", "could not run", context.DeadlineExceeded.Error(), "GOAL TOTAL: $0.0000 across 0 cycle(s)"} {
		if !strings.Contains(rendered, want) {
			t.Errorf("rendering lacks %q:\n%s", want, rendered)
		}
	}
}

// #315: a baseline that timed out is still red (exit 5), says it timed out
// and after how long, and quotes what it printed before it was killed through
// the same bounded tail as a non-zero exit — cut note included.
func TestRunAuto_TimedOutBaselineQuotesItsOutput(t *testing.T) {
	var long strings.Builder
	for i := 1; i <= 100; i++ {
		fmt.Fprintf(&long, "line %03d\n", i)
	}
	for _, tc := range []struct {
		name   string
		output string
		want   []string
		absent []string
	}{
		{
			name:   "short output",
			output: "compiling pkg/a\nwaiting on lock\n",
			want:   []string{"Its output:", "  | compiling pkg/a", "  | waiting on lock"},
		},
		{
			name:   "over 40 lines",
			output: long.String(),
			want: []string{
				"The last 40 line(s) of its output (cut: 60 earlier line(s), 540 byte(s) omitted):",
				"  | line 061\n", "  | line 100\n",
			},
			absent: []string{"  | line 060\n"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
			verifier := verify.NewFakeVerifier(nil)
			verifier.InjectError(baselineCmd, &verify.TimeoutError{Command: baselineCmd, Timeout: 3 * time.Minute, Output: tc.output})

			_, err := runBaselineAuto(t, fake, verifier, osStdin(), "fix it", "--verify-cmd", baselineCmd, "--verify-timeout", "3m")

			if code := exitCodeForError(err); code != 5 {
				t.Fatalf("exit code = %d (err %v), want 5", code, err)
			}
			assertNothingSpent(t, home, fake)
			rendered := renderBaselineRed(t, err)
			want := append([]string{
				"baseline red",
				"--verify-cmd '" + baselineCmd + "' timed out after 3m0s on the starting tree",
				"no cycle ran — baseline red",
			}, tc.want...)
			for _, w := range want {
				if !strings.Contains(rendered, w) {
					t.Errorf("rendering lacks %q:\n%s", w, rendered)
				}
			}
			for _, a := range append([]string{"could not run"}, tc.absent...) {
				if strings.Contains(rendered, a) {
					t.Errorf("rendering has %q:\n%s", a, rendered)
				}
			}
		})
	}
}

// #315: the baseline runs in the invocation directory as it is — Cwd "." —
// so a file nobody committed is part of what it checks.
func TestRunAuto_BaselineRunsOnTheInvocationDirectoryAsItIs(t *testing.T) {
	isolateRunHome(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("wip"), 0o644); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Chdir(dir)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417}})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 0}})
	var visible bool
	verifier.OnVerify(baselineCmd, func(context.Context) {
		calls := verifier.Calls()
		_, statErr := os.Stat(filepath.Join(calls[len(calls)-1].Cwd, "untracked.txt"))
		visible = statErr == nil
	})

	if _, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--verify-cmd", baselineCmd); err != nil {
		t.Fatalf("auto: %v", err)
	}
	calls := verifier.Calls()
	if len(calls) != 1 || calls[0].Cwd != "." {
		t.Fatalf("baseline requests = %+v, want one with Cwd \".\"", calls)
	}
	if !visible {
		t.Error("the untracked file was not visible from the baseline's Cwd")
	}
}

// #315: the baseline gets the same bound the sinks get — the --verify-timeout
// value, or the resolved default when none was given.
func TestRunAuto_BaselineCarriesTheVerifyTimeout(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
		want time.Duration
	}{
		{"explicit", []string{"--verify-timeout", "3m"}, 3 * time.Minute},
		{"default", nil, 10 * time.Minute},
	} {
		t.Run(tc.name, func(t *testing.T) {
			isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417}})
			verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 0}})
			args := append([]string{"add a README section", "--plan-only", "--verify-cmd", baselineCmd}, tc.args...)

			if _, err := runBaselineAuto(t, fake, verifier, osStdin(), args...); err != nil {
				t.Fatalf("auto: %v", err)
			}
			calls := verifier.Calls()
			if len(calls) != 1 || calls[0].Timeout != tc.want {
				t.Fatalf("baseline requests = %+v, want one with Timeout %s", calls, tc.want)
			}
		})
	}
}

// #315: a long output is quoted by its tail only, bounded by lines AND by
// bytes, and the refusal says how much was cut.
func TestBaselineRedError_QuotesABoundedTail(t *testing.T) {
	t.Run("line bound", func(t *testing.T) {
		var b strings.Builder
		for i := 1; i <= 100; i++ {
			fmt.Fprintf(&b, "line %03d\n", i)
		}
		rendered := renderBaselineRed(t, &BaselineRedError{Goal: "g", Command: "make", ExitCode: 1, Output: b.String()})
		if strings.Contains(rendered, "| line 060\n") || !strings.Contains(rendered, "| line 061\n") || !strings.Contains(rendered, "| line 100\n") {
			t.Errorf("want exactly lines 61..100 quoted:\n%s", rendered)
		}
		// 60 lines of "line NNN\n" = 540 bytes.
		if !strings.Contains(rendered, "The last 40 line(s) of its output (cut: 60 earlier line(s), 540 byte(s) omitted):") {
			t.Errorf("the cut was not stated:\n%s", rendered)
		}
	})
	t.Run("byte bound", func(t *testing.T) {
		long := strings.Repeat("x", 199) // 200 bytes a line with its newline
		var b strings.Builder
		for i := 1; i <= 30; i++ {
			fmt.Fprintf(&b, "%02d%s\n", i, long[2:])
		}
		rendered := renderBaselineRed(t, &BaselineRedError{Goal: "g", Command: "make", ExitCode: 1, Output: b.String()})
		quoted := 0
		for _, line := range strings.Split(rendered, "\n") {
			if strings.HasPrefix(line, "  | ") {
				quoted += len(line) - len("  | ") + 1
			}
		}
		if quoted > baselineTailBytes {
			t.Errorf("quoted %d bytes, want at most %d", quoted, baselineTailBytes)
		}
		// 4096 bytes hold 20 whole 200-byte lines; the partial 21st is dropped.
		if !strings.Contains(rendered, "| 30") || !strings.Contains(rendered, "| 11") || strings.Contains(rendered, "| 10") {
			t.Errorf("want exactly lines 11..30 quoted:\n%s", rendered)
		}
		if !strings.Contains(rendered, "The last 20 line(s) of its output (cut: 10 earlier line(s), 2000 byte(s) omitted):") {
			t.Errorf("the cut was not stated:\n%s", rendered)
		}
	})
	t.Run("one over-long line", func(t *testing.T) {
		output := "é" + strings.Repeat("y", 2*baselineTailBytes)
		tail := tailOutput(output, baselineTailLines, baselineTailBytes)
		if len(tail.text) > baselineTailBytes || tail.omittedBytes != len(output)-len(tail.text) || tail.lines != 1 {
			t.Errorf("tail = %d bytes, %d lines, %d omitted; want ≤ %d bytes of the one line", len(tail.text), tail.lines, tail.omittedBytes, baselineTailBytes)
		}
	})
}

// #315: the new type gets its own exit code, wrapped or not.
func TestExitCodeForError_BaselineRedIsFive(t *testing.T) {
	red := &BaselineRedError{Command: "make", ExitCode: 2}
	if code := exitCodeForError(red); code != 5 {
		t.Errorf("exitCodeForError(*BaselineRedError) = %d, want 5", code)
	}
	if code := exitCodeForError(fmt.Errorf("wrapped: %w", red)); code != 5 {
		t.Errorf("exitCodeForError(wrapped *BaselineRedError) = %d, want 5", code)
	}
}

// #315, the multi-cycle case: the baseline refusal is not a single-cycle
// feature. A goal loop (--max-cycles > 1, with or without the cross-cycle
// budget) takes the same path to the same refusal — exit 5, a
// *BaselineRedError, no planner call and nothing under OMG_HOME.
func TestRunAuto_RedBaselineRefusesUnderMaxCycles(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"max-cycles 2", []string{"--max-cycles", "2"}},
		{"max-cycles 3 with a goal budget", []string{"--max-cycles", "3", "--max-goal-budget-usd", "5"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
			verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 1, Output: "boom\n"}})
			args := append([]string{"fix it", "--verify-cmd", baselineCmd}, tc.args...)

			_, err := runBaselineAuto(t, fake, verifier, osStdin(), args...)

			if code := exitCodeForError(err); code != 5 {
				t.Fatalf("exit code = %d (err %v), want 5", code, err)
			}
			var red *BaselineRedError
			if !errors.As(err, &red) {
				t.Fatalf("err = %v, want *BaselineRedError", err)
			}
			assertNothingSpent(t, home, fake)
			if n := len(verifier.Calls()); n != 1 {
				t.Errorf("baseline ran %d times, want once", n)
			}
		})
	}
}

// #315, the multi-cycle case end to end through mainExitCode — the same argv
// path main takes, minus os.Exit. The verifier here is the real ShellVerifier
// (it runs `sh -c`, nothing else), and `claude` on PATH is a stub that only
// satisfies the PATH preflight: the baseline refuses before anything could
// spawn it, and if a regression ever got that far the stub exits 1 and bills
// nothing.
func TestMainExitCode_RedBaselineUnderMaxCyclesExitsFive(t *testing.T) {
	home := isolateRunHome(t)
	bin := t.TempDir()
	if err := os.WriteFile(filepath.Join(bin, "claude"), []byte("#!/bin/sh\nexit 1\n"), 0o755); err != nil {
		t.Fatalf("fixture: %v", err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Chdir(t.TempDir())

	var code int
	stdout := captureStdout(t, func() {
		code = mainExitCode([]string{"auto", "fix it", "--max-cycles", "2", "--verify-cmd", "echo red && exit 7",
			"--no-agent-mapping", "--no-skill-activation", "--no-web"})
	})

	if code != 5 {
		t.Fatalf("mainExitCode = %d, want 5:\n%s", code, stdout)
	}
	for _, want := range []string{"exited 7 on the starting tree", "  | red", "no cycle ran — baseline red"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("stdout lacks %q:\n%s", want, stdout)
		}
	}
	if entries, err := os.ReadDir(home); err == nil && len(entries) != 0 {
		t.Errorf("a red baseline left artifacts under OMG_HOME: %v", entries)
	}
}

// interruptingVerifier stands in for a --verify-cmd that the operator
// interrupts: it sends this process SIGINT — which runAutoWithRuntime's
// signal.NotifyContext catches, exactly as a Ctrl-C at the terminal — waits
// for the run context to be cancelled, and only then answers with its
// scripted error or result.
type interruptingVerifier struct {
	err    error
	result verify.Result
	calls  int
}

func (v *interruptingVerifier) Verify(ctx context.Context, _ verify.Request) (verify.Result, error) {
	v.calls++
	if err := syscall.Kill(os.Getpid(), syscall.SIGINT); err != nil {
		return verify.Result{}, err
	}
	select {
	case <-ctx.Done():
	case <-time.After(10 * time.Second):
		return verify.Result{}, errors.New("interruptingVerifier: SIGINT never cancelled the run context")
	}
	return v.result, v.err
}

// #315: an interrupted baseline is not a red baseline. Ctrl-C while the
// --verify-cmd runs must not tell a script to fix its tree: no
// *BaselineRedError, no "baseline red", no exit 5 — the generic exit 1 an
// interrupted interview or planner call takes, with context.Canceled still
// reachable, and no planner call. That holds whether the killed command comes
// back as an error or as a non-zero exit.
func TestRunAuto_InterruptedBaselineIsNotRed(t *testing.T) {
	for _, tc := range []struct {
		name     string
		verifier *interruptingVerifier
	}{
		{"verifier error", &interruptingVerifier{err: context.Canceled}},
		{"non-zero result", &interruptingVerifier{result: verify.Result{ExitCode: 130, Output: "signal: interrupt\n"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := isolateRunHome(t)
			fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})

			var stdout string
			stderr, err := captureStderr(t, func() error {
				var runErr error
				stdout, runErr = runBaselineAuto(t, fake, tc.verifier, osStdin(), "fix it", "--verify-cmd", baselineCmd)
				return runErr
			})

			var red *BaselineRedError
			if errors.As(err, &red) {
				t.Fatalf("err = %v, an interrupted baseline came back as *BaselineRedError", err)
			}
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("err = %v, want it to wrap context.Canceled", err)
			}
			if code := exitCodeForError(err); code != 1 {
				t.Errorf("exit code = %d (err %v), want 1", code, err)
			}
			for _, text := range []string{err.Error(), stdout, stderr} {
				if strings.Contains(text, "baseline red") {
					t.Errorf("an interrupted baseline printed %q:\n%s", "baseline red", text)
				}
			}
			if tc.verifier.calls != 1 {
				t.Errorf("baseline ran %d times, want once", tc.verifier.calls)
			}
			if n := len(plannerCalls(fake)); n != 0 {
				t.Errorf("planner calls = %d, want 0 after an interrupted baseline", n)
			}
			assertNothingSpent(t, home, fake)
		})
	}
}

// #325: --no-baseline skips the baseline and nothing else. With a --verify-cmd
// that is red on the starting tree (an acceptance test of the goal), the run
// reaches the planner, the verifier is never asked, the skip is printed once,
// and the plan still shows the command at its sink — and --plan-only runs it
// nowhere.
func TestRunAuto_NoBaselineSkipsTheBaselineAndKeepsTheSinkCommand(t *testing.T) {
	isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec, TotalCostUSD: 0.0417}})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{baselineCmd: {ExitCode: 1, Output: "--- FAIL: the change is not there yet\n"}})

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--no-baseline", "--verify-cmd", baselineCmd)

	if err != nil {
		t.Fatalf("--no-baseline with a red --verify-cmd must reach the planner: %v", err)
	}
	if calls := verifier.Calls(); len(calls) != 0 {
		t.Errorf("--no-baseline still ran the baseline: %+v", calls)
	}
	want := "Baseline: skipped (--no-baseline); --verify-cmd '" + baselineCmd + "' is still the command at every sink.\n"
	if strings.Count(out, want) != 1 {
		t.Errorf("want the skip line exactly once, got:\n%s", out)
	}
	if strings.Contains(out, "baseline red") || strings.Contains(out, "passed on the starting tree") {
		t.Errorf("a skipped baseline reported a verdict:\n%s", out)
	}
	if len(plannerCalls(fake)) != 1 {
		t.Errorf("planner calls = %d, want 1", len(plannerCalls(fake)))
	}
	for _, want := range []string{"build evidence", baselineCmd, "ENGINE runs this"} {
		if !strings.Contains(out, want) {
			t.Errorf("skipping the baseline dropped the sink command from the plan; output lacks %q:\n%s", want, out)
		}
	}
}

// #325: skipping the baseline must not skip verification. A real command that
// exits 1 still fails the run at its sink, although the baseline that would
// have caught it at the start was skipped.
func TestRunAuto_NoBaselineStillVerifiesAtTheSinks(t *testing.T) {
	isolateRunHome(t)
	script := writeVerifyScript(t, 1)
	fake := newCycleFake(map[string]runner.NodeOutcome{
		"plan-1": {Result: cycleSpec, TotalCostUSD: 0.10},
		"work-1": {SessionID: "s-work", Result: "PASS", ExitCode: 0, TotalCostUSD: 0.50},
	})
	verifier := verify.NewFakeVerifier(map[string]verify.Result{script: {ExitCode: 1}})

	out, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--no-baseline", "--verify-cmd", script)

	if err == nil {
		t.Fatalf("a sink whose command exits 1 must fail the run, --no-baseline or not:\n%s", out)
	}
	var red *BaselineRedError
	if errors.As(err, &red) {
		t.Fatalf("--no-baseline still refused on the baseline: %v", err)
	}
	if calls := verifier.Calls(); len(calls) != 0 {
		t.Errorf("--no-baseline still ran the baseline: %+v", calls)
	}
	if !strings.Contains(out, "FAIL") || strings.Contains(out, "PASS (verified)") {
		t.Errorf("the sink's failed verification is not in the ledger:\n%s", out)
	}
}

// #325: --no-baseline names a command to skip, so without --verify-cmd it is
// refused at parse — before any model call — instead of sitting in a script
// doing nothing.
func TestRunAuto_NoBaselineWithoutVerifyCmdIsRefused(t *testing.T) {
	home := isolateRunHome(t)
	fake := newCycleFake(map[string]runner.NodeOutcome{"plan-1": {Result: cycleSpec}})
	verifier := verify.NewFakeVerifier(nil)

	_, err := runBaselineAuto(t, fake, verifier, osStdin(), "add a README section", "--plan-only", "--no-baseline", "--accept-no-build-evidence")

	if err == nil || !strings.Contains(err.Error(), "--no-baseline") || !strings.Contains(err.Error(), "no --verify-cmd") {
		t.Fatalf("err = %v, want the --no-baseline usage refusal", err)
	}
	assertNothingSpent(t, home, fake)
	if calls := verifier.Calls(); len(calls) != 0 {
		t.Errorf("the verifier was called: %+v", calls)
	}
}

// #325: the red-baseline refusal tells an acceptance-test user the way out.
func TestBaselineRedError_NamesNoBaseline(t *testing.T) {
	var buf bytes.Buffer
	(&BaselineRedError{Goal: "g", Command: baselineCmd, ExitCode: 1}).Print(&buf)
	if !strings.Contains(buf.String(), "re-run with --no-baseline: the sinks still run it") {
		t.Errorf("the refusal does not name --no-baseline:\n%s", buf.String())
	}
}
