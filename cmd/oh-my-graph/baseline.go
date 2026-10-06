// The starting-tree baseline (#315): `auto --verify-cmd 'CMD'` runs CMD once on
// the invocation directory as it is, before the interview, the first planner
// call and the first run directory. A command that is already red there would
// otherwise be learned at the end of a whole paid cycle — the sinks would fail
// it on work that never touched the cause.
//
// It runs through the same verify.Verifier seam the sinks use, with the same
// resolved --verify-timeout, and adds no spawner of its own (internal/invariants).
package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/jitokim/oh-my-graph/internal/coordinator"
	"github.com/jitokim/oh-my-graph/internal/verify"
)

// The tail of a red baseline's output that the refusal quotes: the last
// baselineTailLines lines, and of those no more than baselineTailBytes. Enough
// for a compiler error or a failing test's assertion, which is what the
// operator needs to decide what to fix; the full output is one re-run of the
// command away, in their own shell.
const (
	baselineTailLines = 40
	baselineTailBytes = 4 * 1024
)

// runBaseline runs the --verify-cmd once on the starting tree ("." — the same
// tree the planned nodes and the sinks run in) and returns a *BaselineRedError
// when it is red: a non-zero exit, or no result at all (could not run, timed
// out). A timeout keeps what the command printed before it was killed, so the
// refusal can quote it. Green prints one line and returns nil. A zero command
// is no baseline at all: nothing runs and nothing prints.
//
// An interrupt (Ctrl-C, SIGTERM) during the baseline is not a red baseline:
// it returns a plain error wrapping ctx.Err(), which leaves `auto` on the
// generic path (exit 1) like an interrupted interview or planner call. It is
// checked before the verifier's verdict, because a killed command can also
// come back as a non-zero exit.
func runBaseline(ctx context.Context, out io.Writer, verifier verify.Verifier, v coordinator.VerifyCommand, goal string) error {
	if !v.Supplied() {
		return nil
	}
	result, err := verifier.Verify(ctx, verify.Request{Command: v.Command, Cwd: ".", Timeout: v.ResolvedTimeout()})
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("auto: interrupted during the --verify-cmd baseline '%s': %w", v.Command, ctxErr)
	}
	if err != nil {
		red := &BaselineRedError{Goal: goal, Command: v.Command, RunErr: err}
		var timedOut *verify.TimeoutError
		if errors.As(err, &timedOut) {
			red.Output = timedOut.Output
		}
		return red
	}
	if result.ExitCode != 0 {
		return &BaselineRedError{Goal: goal, Command: v.Command, ExitCode: result.ExitCode, Output: result.Output}
	}
	fmt.Fprintf(out, "Baseline: --verify-cmd '%s' passed on the starting tree (exit 0).\n", v.Command)
	return nil
}

// BaselineRedError is `auto`'s refusal when its --verify-cmd is already red on
// the starting tree (#315). Like coordinator.MissingBuildEvidenceError it is
// printed whole to STDOUT by mainExitCode rather than as a one-line stderr
// error, and it maps to its own exit code (5): nothing was planned, nothing
// was billed, and no run directory exists.
type BaselineRedError struct {
	// Goal is the goal that ran no cycle, for the goal summary.
	Goal string
	// Command is the --verify-cmd that was run.
	Command string
	// ExitCode is the command's exit status. Meaningful only when RunErr is nil.
	ExitCode int
	// RunErr is why the command produced no result — it could not be spawned or
	// timed out. Nil when the command ran and exited non-zero. Never an
	// interrupt: runBaseline returns that as a plain error, not a red baseline.
	RunErr error
	// Output is the command's full combined output — or, when RunErr is a
	// *verify.TimeoutError, the bounded tail it printed before it was killed.
	// Print quotes only its tail.
	Output string
}

func (e *BaselineRedError) Error() string {
	var timedOut *verify.TimeoutError
	if errors.As(e.RunErr, &timedOut) {
		return fmt.Sprintf("auto: baseline red: --verify-cmd '%s' timed out after %s on the starting tree", e.Command, timedOut.Timeout)
	}
	if e.RunErr != nil {
		return fmt.Sprintf("auto: baseline red: --verify-cmd '%s' could not run on the starting tree: %v", e.Command, e.RunErr)
	}
	return fmt.Sprintf("auto: baseline red: --verify-cmd '%s' exited %d on the starting tree", e.Command, e.ExitCode)
}

func (e *BaselineRedError) Unwrap() error { return e.RunErr }

// Print writes the whole refusal: what failed, the tail of its output, and the
// goal summary of a goal that ran no cycle. The summary is written here rather
// than through printGoalSummary, whose error branch would claim an incomplete
// cycle — and no cycle was started.
func (e *BaselineRedError) Print(w io.Writer) {
	fmt.Fprintf(w, "%s.\n\n", e.Error())
	fmt.Fprint(w,
		"Nothing was planned, nothing was billed, and no run directory was created:\n"+
			"every cycle's sinks would run this same command, so it must pass on the\n"+
			"tree as it is first. Fix the tree (or the command) and re-run.\n\n")
	tail := tailOutput(e.Output, baselineTailLines, baselineTailBytes)
	switch {
	case e.RunErr != nil && tail.text == "":
		// No result, so no output to quote.
	case tail.text == "":
		fmt.Fprint(w, "It printed nothing.\n\n")
	case tail.omittedLines > 0 || tail.omittedBytes > 0:
		fmt.Fprintf(w, "The last %d line(s) of its output (cut: %d earlier line(s), %d byte(s) omitted):\n\n",
			tail.lines, tail.omittedLines, tail.omittedBytes)
	default:
		fmt.Fprint(w, "Its output:\n\n")
	}
	if tail.text != "" {
		for _, line := range strings.Split(tail.text, "\n") {
			fmt.Fprintf(w, "  | %s\n", line)
		}
		fmt.Fprintln(w)
	}
	fmt.Fprintf(w, "GOAL SUMMARY — %q\n", e.Goal)
	fmt.Fprint(w, "  no cycle ran — baseline red\n")
	fmt.Fprintf(w, "GOAL TOTAL: %s across 0 cycle(s)\n", formatCost(0, false))
}

// outputTail is the bounded end of a command's output and what was cut to get
// there.
type outputTail struct {
	text         string
	lines        int
	omittedLines int
	omittedBytes int
}

// tailOutput keeps the last maxLines lines of output, then — if those still
// exceed maxBytes — only the whole lines that fit in the last maxBytes, or the
// last maxBytes of a single over-long line, cut on a UTF-8 boundary. Trailing
// newlines are not counted as an empty last line.
func tailOutput(output string, maxLines, maxBytes int) outputTail {
	trimmed := strings.TrimRight(output, "\r\n")
	if trimmed == "" {
		return outputTail{}
	}
	all := strings.Split(trimmed, "\n")
	kept := all
	if len(kept) > maxLines {
		kept = kept[len(kept)-maxLines:]
	}
	text := strings.Join(kept, "\n")
	if len(text) > maxBytes {
		text = text[len(text)-maxBytes:]
		if i := strings.IndexByte(text, '\n'); i >= 0 {
			text = text[i+1:]
		} else {
			for len(text) > 0 && !utf8.RuneStart(text[0]) {
				text = text[1:]
			}
		}
	}
	lines := strings.Count(text, "\n") + 1
	return outputTail{
		text:         text,
		lines:        lines,
		omittedLines: len(all) - lines,
		omittedBytes: len(trimmed) - len(text),
	}
}
